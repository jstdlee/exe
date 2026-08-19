package server

// The SSH gate is the daemon's SSH interface on ssh_listen (default :2222),
// mirroring exe.dev's ssh UX on a single host. The SSH username picks the
// destination:
//
//	ssh -p 2222 exe@host             interactive lobby (VM lifecycle REPL)
//	ssh -p 2222 exe@host ls --json   one-shot lobby commands (agent-friendly)
//	ssh -p 2222 <vm>@host            full SSH into the VM — shell, exec,
//	                                 scp, sftp, -L/-R forwarding; the VM is
//	                                 auto-started if stopped
//
// Client keys accepted: the service key (~/.exe/ssh/id_ed25519), the daemon
// user's ~/.ssh/*.pub, and lines in ~/.exe/ssh/authorized_clients.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"exe/internal/keys"
	"exe/internal/sshexec"
	"exe/internal/vmm"
)

type SSHGate struct {
	s          *Server
	scfg       *ssh.ServerConfig
	servicePub ssh.PublicKey
}

func NewSSHGate(s *Server) (*SSHGate, error) {
	signer, err := keys.EnsureHostKey(s.StateDir)
	if err != nil {
		return nil, fmt.Errorf("ssh gate host key: %w", err)
	}
	g := &SSHGate{s: s}
	if b, err := os.ReadFile(s.KeyPath + ".pub"); err == nil {
		if k, _, _, _, kerr := ssh.ParseAuthorizedKey(b); kerr == nil {
			g.servicePub = k
		}
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: g.authenticate,
		ServerVersion:     "SSH-2.0-exe",
	}
	cfg.AddHostKey(signer)
	g.scfg = cfg
	return g, nil
}

func (g *SSHGate) clientsPath() string {
	return filepath.Join(g.s.StateDir, "ssh", "authorized_clients")
}

func (g *SSHGate) authenticate(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	wire := key.Marshal()
	if g.servicePub != nil && bytes.Equal(wire, g.servicePub.Marshal()) {
		return nil, nil
	}
	for _, cand := range g.authorizedKeys() {
		if bytes.Equal(wire, cand.Marshal()) {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("key %s for %q not authorized — add it to %s",
		ssh.FingerprintSHA256(key), conn.User(), g.clientsPath())
}

// authorizedKeys loads the daemon user's ~/.ssh/*.pub plus
// ~/.exe/ssh/authorized_clients, re-read on every attempt so edits apply
// without a restart.
func (g *SSHGate) authorizedKeys() []ssh.PublicKey {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		m, _ := filepath.Glob(filepath.Join(home, ".ssh", "*.pub"))
		files = append(files, m...)
	}
	files = append(files, g.clientsPath())
	var out []ssh.PublicKey
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for len(b) > 0 {
			k, _, _, rest, err := ssh.ParseAuthorizedKey(b)
			if err != nil {
				if i := bytes.IndexByte(b, '\n'); i >= 0 {
					b = b[i+1:]
					continue
				}
				break
			}
			out = append(out, k)
			b = rest
		}
	}
	return out
}

// Serve accepts connections until the listener is closed.
func (g *SSHGate) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go g.handleConn(c)
	}
}

func (g *SSHGate) handleConn(c net.Conn) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, g.scfg)
	if err != nil {
		c.Close()
		return
	}
	defer sconn.Close()
	user := sconn.User()
	if user != "exe" {
		if _, err := g.s.VMs.Get(context.Background(), user); err == nil {
			log.Printf("ssh gate: %s -> vm %s", sconn.RemoteAddr(), user)
			g.bridgeVM(sconn, chans, reqs, user)
			return
		}
	}
	log.Printf("ssh gate: %s -> lobby (user %q)", sconn.RemoteAddr(), user)
	g.lobbyConn(chans, reqs, user)
}

// ---- direct VM access: transparent SSH bridge ------------------------------

// bridgeVM relays the whole SSH connection to the VM's sshd, authenticating
// with the service key. Channels and global requests pass through untouched
// in both directions, so shell, exec, scp, sftp and -L/-R forwarding all
// work as if the client had connected to the VM directly.
func (g *SSHGate) bridgeVM(sconn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, name string) {
	cfg := g.s.Config()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	go func() { sconn.Wait(); cancel() }()

	failAll := func(err error) {
		log.Printf("ssh gate: vm %s: %v", name, err)
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			nc.Reject(ssh.ConnectionFailed, fmt.Sprintf("vm %s: %v", name, err))
		}
	}

	info, err := g.s.VMs.Get(ctx, name)
	if err != nil {
		failAll(err)
		return
	}
	if info.State != "running" || info.IP == "" {
		log.Printf("ssh gate: starting vm %s for %s", name, sconn.RemoteAddr())
		if info, err = g.s.VMs.Start(ctx, name); err != nil {
			failAll(fmt.Errorf("start: %w", err))
			return
		}
	}
	vconn, vchans, vreqs, err := dialVMRaw(ctx, info.IP, cfg.SSHUser, g.s.KeyPath, g.s.guestDial())
	if err != nil {
		failAll(fmt.Errorf("dial: %w", err))
		return
	}
	defer vconn.Close()

	done := make(chan struct{}, 2)
	go func() { bridgeChannels(vconn, chans); done <- struct{}{} }()
	go func() { bridgeChannels(sconn.Conn, vchans); done <- struct{}{} }()
	go forwardGlobalReqs(vconn, reqs)
	go forwardGlobalReqs(sconn.Conn, vreqs)
	<-done
}

// dialVMRaw opens an unwrapped SSH client connection so every channel and
// request stays visible to the bridge (ssh.NewClient would swallow
// forwarded-tcpip channels for its own forwarding machinery).
func dialVMRaw(ctx context.Context, host, user, keyPath string, dial sshexec.DialFunc) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	ccfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	if dial == nil {
		d := net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	tc, err := dial(ctx, "tcp", net.JoinHostPort(host, "22"))
	if err != nil {
		return nil, nil, nil, err
	}
	conn, chans, reqs, err := ssh.NewClientConn(tc, host, ccfg)
	if err != nil {
		tc.Close()
		return nil, nil, nil, err
	}
	return conn, chans, reqs, nil
}

func forwardGlobalReqs(dst ssh.Conn, reqs <-chan *ssh.Request) {
	for r := range reqs {
		ok, payload, err := dst.SendRequest(r.Type, r.WantReply, r.Payload)
		if err != nil {
			ok, payload = false, nil
		}
		if r.WantReply {
			r.Reply(ok, payload)
		}
	}
}

func forwardChannelReqs(dst ssh.Channel, reqs <-chan *ssh.Request) {
	for r := range reqs {
		ok, err := dst.SendRequest(r.Type, r.WantReply, r.Payload)
		if err != nil {
			ok = false
		}
		if r.WantReply {
			r.Reply(ok, nil)
		}
	}
}

func bridgeChannels(dst ssh.Conn, src <-chan ssh.NewChannel) {
	for nc := range src {
		go bridgeChannel(dst, nc)
	}
}

// bridgeChannel splices one channel: data, stderr and channel requests flow
// both ways; each side is closed once the other has fully finished, so
// exit-status makes it across before the close.
func bridgeChannel(dst ssh.Conn, nc ssh.NewChannel) {
	dch, dreqs, err := dst.OpenChannel(nc.ChannelType(), nc.ExtraData())
	if err != nil {
		var oce *ssh.OpenChannelError
		if errors.As(err, &oce) {
			nc.Reject(oce.Reason, oce.Message)
		} else {
			nc.Reject(ssh.ConnectionFailed, err.Error())
		}
		return
	}
	sch, sreqs, err := nc.Accept()
	if err != nil {
		dch.Close()
		return
	}
	var wg sync.WaitGroup
	relay := func(from ssh.Channel, fromReqs <-chan *ssh.Request, to ssh.Channel) {
		defer wg.Done()
		var w sync.WaitGroup
		w.Add(2)
		go func() { defer w.Done(); io.Copy(to, from); to.CloseWrite() }()
		go func() { defer w.Done(); io.Copy(to.Stderr(), from.Stderr()) }()
		forwardChannelReqs(to, fromReqs) // returns when from's side closes
		w.Wait()
		to.Close()
	}
	wg.Add(2)
	go relay(sch, sreqs, dch)
	go relay(dch, dreqs, sch)
	wg.Wait()
}

// ---- lobby -----------------------------------------------------------------

func (g *SSHGate) lobbyConn(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, user string) {
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.Prohibited, "the exe lobby is commands-only; ssh <vm>@host for full SSH")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go (&lobbySession{g: g, ch: ch, user: user, cols: 80, rows: 24}).run(creqs)
	}
}

type lobbySession struct {
	g    *SSHGate
	ch   ssh.Channel
	user string

	mu         sync.Mutex
	pty        bool
	cols, rows int
	term       *term.Terminal
}

func (l *lobbySession) run(reqs <-chan *ssh.Request) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer l.ch.Close()
	started := false
	for r := range reqs {
		switch r.Type {
		case "pty-req":
			var p struct {
				Term                 string
				Cols, Rows, PxW, PxH uint32
				Modes                string
			}
			if ssh.Unmarshal(r.Payload, &p) == nil {
				l.mu.Lock()
				l.pty = true
				if p.Cols > 0 && p.Rows > 0 { // 0x0 = client has no real tty
					l.cols, l.rows = int(p.Cols), int(p.Rows)
				}
				l.mu.Unlock()
			}
			r.Reply(true, nil)
		case "window-change":
			var p struct{ Cols, Rows, PxW, PxH uint32 }
			if ssh.Unmarshal(r.Payload, &p) == nil && p.Cols > 0 && p.Rows > 0 {
				l.mu.Lock()
				l.cols, l.rows = int(p.Cols), int(p.Rows)
				if l.term != nil {
					l.term.SetSize(l.cols, l.rows)
				}
				l.mu.Unlock()
			}
		case "env":
			r.Reply(true, nil)
		case "shell":
			if started {
				r.Reply(false, nil)
				continue
			}
			started = true
			r.Reply(true, nil)
			go func() { l.exit(l.repl(ctx)) }()
		case "exec":
			if started {
				r.Reply(false, nil)
				continue
			}
			started = true
			var p struct{ Command string }
			ssh.Unmarshal(r.Payload, &p)
			r.Reply(true, nil)
			go func() { l.exit(l.g.runLobbyCommand(ctx, l.out(), p.Command)) }()
		case "subsystem":
			// no sftp in the lobby — that's what ssh <vm>@host is for
			r.Reply(false, nil)
		default:
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}
}

func (l *lobbySession) exit(code int) {
	l.ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
	l.ch.Close()
}

// out returns the session writer, inserting \r before \n when a pty is up.
func (l *lobbySession) out() io.Writer {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pty {
		return &crlfWriter{w: l.ch}
	}
	return l.ch
}

func (l *lobbySession) repl(ctx context.Context) int {
	host, _ := os.Hostname()
	l.mu.Lock()
	pty := l.pty
	l.mu.Unlock()
	if !pty {
		// piped input (`echo ls | ssh -p 2222 exe@host`): no prompt, no echo
		br := bufio.NewReader(l.ch)
		for {
			line, err := br.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "exit" || line == "quit" {
				return 0
			}
			if line != "" {
				l.g.runLobbyCommand(ctx, l.ch, line)
			}
			if err != nil {
				return 0
			}
		}
	}
	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{l.ch, &crlfWriter{w: l.ch}}, "exe> ")
	l.mu.Lock()
	l.term = t
	t.SetSize(l.cols, l.rows)
	l.mu.Unlock()
	who := ""
	if l.user != "exe" {
		who = fmt.Sprintf("(no VM named %q — you're in the lobby)\n", l.user)
	}
	fmt.Fprintf(t, "exe lobby on %s — \"help\" lists commands, \"exit\" leaves.\n%sfull VM access: ssh %s@%s (auto-starts it)\n\n", host, who, "<vm>", host)
	for {
		line, err := t.ReadLine()
		if err != nil {
			return 0
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" || line == "logout" {
			return 0
		}
		l.g.runLobbyCommand(ctx, t, line)
	}
}

// crlfWriter rewrites bare \n as \r\n for raw pty output.
type crlfWriter struct {
	w    io.Writer
	last byte
}

func (c *crlfWriter) Write(p []byte) (int, error) {
	var buf bytes.Buffer
	for _, b := range p {
		if b == '\n' && c.last != '\r' {
			buf.WriteByte('\r')
		}
		buf.WriteByte(b)
		c.last = b
	}
	if _, err := c.w.Write(buf.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ---- lobby commands ---------------------------------------------------------

const lobbyHelp = `exe lobby — VM lifecycle over SSH

  help                               this help
  ls [--json]                        list VMs
  new [name] [-cpus N] [-mem MB] [-disk GB] [--json]
                                     create + boot a VM (name invented if omitted)
  start <vm> [--json]                boot a stopped VM
  stop <vm>                          power off a VM
  rm <vm>                            delete a VM and its disk
  ip <vm>                            print the VM's IP
  code <vm> [-m model] <prompt>      vibecode: the agent builds inside the VM
  expose <vm> <port> [subdomain]     publish https://<sub>.<domain> -> VM port
  unexpose <host>                    remove a published hostname
  routes [--json]                    list proxy routes
  exit                               leave the lobby

Full SSH into a VM (shell, scp, sftp, -L/-R; auto-starts it):
  ssh -p <gate port> <vm>@<this host>
`

// runLobbyCommand executes one lobby command line and returns an exit code.
func (g *SSHGate) runLobbyCommand(ctx context.Context, out io.Writer, line string) int {
	args := splitLine(line)
	if len(args) == 0 {
		return 0
	}
	cmd, rest := args[0], args[1:]
	fail := func(format string, a ...any) int {
		fmt.Fprintf(out, "error: "+format+"\n", a...)
		return 1
	}
	switch cmd {
	case "help", "?":
		io.WriteString(out, lobbyHelp)
		return 0
	case "ls", "list":
		vms, err := g.s.VMs.List(ctx)
		if err != nil {
			return fail("%v", err)
		}
		if vms == nil {
			vms = []*vmm.Info{}
		}
		if hasJSONFlag(rest) {
			return writeAsJSON(out, vms)
		}
		printVMTable(out, vms)
		return 0
	case "new", "create":
		return g.lobbyNew(ctx, out, rest, fail)
	case "start":
		if len(rest) < 1 {
			return fail("usage: start <vm>")
		}
		fmt.Fprintf(out, "starting %s...\n", rest[0])
		info, err := g.s.VMs.Start(ctx, rest[0])
		if err != nil {
			return fail("%v", err)
		}
		if hasJSONFlag(rest) {
			return writeAsJSON(out, info)
		}
		printVMTable(out, []*vmm.Info{info})
		return 0
	case "stop":
		if len(rest) < 1 {
			return fail("usage: stop <vm>")
		}
		if err := g.s.VMs.Stop(ctx, rest[0]); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "%s: stopped\n", rest[0])
		return 0
	case "rm", "delete":
		if len(rest) < 1 {
			return fail("usage: rm <vm>")
		}
		if err := g.s.VMs.Delete(ctx, rest[0]); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "%s: deleted\n", rest[0])
		return 0
	case "ip":
		if len(rest) < 1 {
			return fail("usage: ip <vm>")
		}
		info, err := g.s.VMs.Get(ctx, rest[0])
		if err != nil {
			return fail("%v", err)
		}
		if info.IP == "" {
			return fail("vm %s has no IP (state=%s)", rest[0], info.State)
		}
		fmt.Fprintln(out, info.IP)
		return 0
	case "code":
		return g.lobbyCode(ctx, out, rest, fail)
	case "expose":
		return g.lobbyExpose(ctx, out, rest, fail)
	case "unexpose":
		if len(rest) < 1 {
			return fail("usage: unexpose <host>")
		}
		res, err := g.s.removeRoute(ctx, rest[0])
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "%s: route removed\n", rest[0])
		printWarnings(out, res)
		return 0
	case "routes":
		routes := g.s.Proxy.Snapshot()
		if hasJSONFlag(rest) {
			return writeAsJSON(out, routes)
		}
		tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "HOST\tBACKEND")
		for h, b := range routes {
			fmt.Fprintf(tw, "%s\t%s\n", h, b)
		}
		tw.Flush()
		return 0
	case "exit", "quit":
		return 0
	default:
		return fail("unknown command %q — try \"help\"", cmd)
	}
}

func (g *SSHGate) lobbyNew(ctx context.Context, out io.Writer, rest []string, fail func(string, ...any) int) int {
	name := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(out)
	cpus := fs.Int("cpus", 0, "vCPUs")
	mem := fs.Int("mem", 0, "memory MB")
	disk := fs.Int("disk", 0, "disk GB")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	if name == "" {
		name = g.pickName(ctx)
	}
	if name == "exe" {
		return fail("the name \"exe\" is reserved for the lobby")
	}
	if err := vmm.ValidateName(name); err != nil {
		return fail("%v", err)
	}
	spec := vmm.Spec{Name: name, CPUs: *cpus, MemoryMB: *mem, DiskGB: *disk}
	g.s.fillSpec(&spec)
	if !*jsonOut {
		fmt.Fprintf(out, "creating %s (%d cpu, %d MB, %d GB) — the first VM ever downloads the base image, which can take a while...\n",
			name, spec.CPUs, spec.MemoryMB, spec.DiskGB)
	}
	info, err := g.s.VMs.Create(ctx, spec)
	if err != nil {
		return fail("%v", err)
	}
	if *jsonOut {
		return writeAsJSON(out, info)
	}
	printVMTable(out, []*vmm.Info{info})
	host, _ := os.Hostname()
	fmt.Fprintf(out, "\nnext:\n  ssh %s@%s\n  code %s \"build me ...\"\n  expose %s 8000\n", name, host, name, name)
	return 0
}

func (g *SSHGate) lobbyCode(ctx context.Context, out io.Writer, rest []string, fail func(string, ...any) int) int {
	if len(rest) < 1 {
		return fail("usage: code <vm> [-m model] <prompt>")
	}
	name := rest[0]
	rest = rest[1:]
	model := ""
	if len(rest) >= 2 && (rest[0] == "-m" || rest[0] == "--model") {
		model, rest = rest[1], rest[2:]
	}
	prompt := strings.TrimSpace(strings.Join(rest, " "))
	if prompt == "" {
		return fail("usage: code <vm> [-m model] <prompt>")
	}
	info, err := g.s.agentPrecheck(ctx, name, prompt, "")
	if err != nil {
		return fail("%v", err)
	}
	if err := g.s.agentRun(ctx, info, name, prompt, "", model, func(line string) {
		io.WriteString(out, line)
	}); err != nil {
		return 1
	}
	return 0
}

func (g *SSHGate) lobbyExpose(ctx context.Context, out io.Writer, rest []string, fail func(string, ...any) int) int {
	if len(rest) < 2 {
		return fail("usage: expose <vm> <port> [subdomain]")
	}
	name := rest[0]
	port, err := strconv.Atoi(rest[1])
	if err != nil || port <= 0 || port > 65535 {
		return fail("bad port %q", rest[1])
	}
	sub := ""
	if len(rest) > 2 {
		sub = rest[2]
	}
	if g.s.Config().Cloudflare.Domain == "" {
		return fail("cloudflare.domain is not configured")
	}
	info, err := g.s.runningVM(ctx, name)
	if err != nil {
		return fail("%v", err)
	}
	res, err := g.s.exposeVM(ctx, info, name, sub, port)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(out, "route: %s -> %s\n", res["host"], res["backend"])
	if v, ok := res["dns"]; ok {
		fmt.Fprintln(out, "dns:", v)
	}
	if v, ok := res["ingress"]; ok {
		fmt.Fprintln(out, "tunnel ingress ->", v)
	}
	printWarnings(out, res)
	fmt.Fprintln(out, "url:", res["url"])
	return 0
}

var (
	nameAdjs  = []string{"amber", "brisk", "calm", "dapper", "eager", "fuzzy", "gentle", "happy", "ivory", "jolly", "lucky", "mellow", "nifty", "perky", "quiet", "rosy", "sunny", "tidy", "vivid", "witty"}
	nameNouns = []string{"ant", "bat", "crab", "dove", "elk", "fox", "gull", "hare", "ibis", "jay", "koi", "lark", "mole", "newt", "otter", "pika", "quail", "seal", "toad", "wren"}
)

// pickName invents an unused VM name, exe.dev-style.
func (g *SSHGate) pickName(ctx context.Context) string {
	for i := 0; i < 20; i++ {
		n := nameAdjs[rand.Intn(len(nameAdjs))] + "-" + nameNouns[rand.Intn(len(nameNouns))]
		if _, err := g.s.VMs.Get(ctx, n); errors.Is(err, vmm.ErrNotFound) {
			return n
		}
	}
	return fmt.Sprintf("vm-%04d", rand.Intn(10000))
}

func printVMTable(out io.Writer, vms []*vmm.Info) {
	tw := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tCPUS\tMEM\tDISK\tIP")
	for _, v := range vms {
		ip := v.IP
		if ip == "" {
			ip = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%dMB\t%dGB\t%s\n", v.Name, v.State, v.CPUs, v.MemoryMB, v.DiskGB, ip)
	}
	tw.Flush()
}

func printWarnings(out io.Writer, res map[string]any) {
	if ws, ok := res["warnings"].([]string); ok {
		for _, w := range ws {
			fmt.Fprintln(out, "warning:", w)
		}
	}
}

func writeAsJSON(out io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 1
	}
	out.Write(append(b, '\n'))
	return 0
}

func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}

// splitLine splits a command line into fields, honoring quotes.
func splitLine(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	inTok := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inTok = true
		case c == ' ' || c == '\t':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			cur.WriteByte(c)
			inTok = true
		}
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out
}
