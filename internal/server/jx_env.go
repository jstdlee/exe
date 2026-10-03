package server

// Environments (Feature C): plan a guest bootstrap from a project's
// manifests, bring a VM up with the project uploaded and bootstrapped, and
// run jobs in it. The CLI is a thin client; up and run stream NDJSON
// EnvEvents. Both hold lease "env" for their whole life.

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"exe/internal/jx/envplan"
	"exe/internal/sshexec"
	"exe/internal/vmm"

	"golang.org/x/crypto/ssh"
)

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("POST /v1/jx/env/plan", s.handleJXEnvPlan)
		mux.HandleFunc("POST /v1/jx/env/up", s.handleJXEnvUp)
		mux.HandleFunc("POST /v1/jx/env/run", s.handleJXEnvRun)
	})
}

// envMaxUpload caps a project upload (after the CLI's exclusions).
const envMaxUpload = 4 << 30

// envSSHWait is how long env waits for a started VM's SSH.
var envSSHWait = 3 * time.Minute

// EnvEvent is one NDJSON line of /v1/jx/env/up and /v1/jx/env/run.
//
//	status {text}      progress
//	stdout {text}      guest output
//	stderr {text}
//	plan   {plan}      the bootstrap plan (up)
//	exit   {code}      the job's exit status (run), before outputs are copied
//	tar    {data}      base64 chunk of a tar stream of the outputs (run)
//	error  {error}     a failure; done follows unless the stream is cut
//	done   {code}      last line: the bootstrap's or job's exit status
type EnvEvent struct {
	Type  string        `json:"type"`
	Text  string        `json:"text,omitempty"`
	Data  string        `json:"data,omitempty"`
	Code  *int          `json:"code,omitempty"`
	Plan  *envplan.Plan `json:"plan,omitempty"`
	Error string        `json:"error,omitempty"`
}

// EnvPlanRequest is the body of POST /v1/jx/env/plan.
type EnvPlanRequest struct {
	// Files maps project-relative slash paths to contents (envplan.Collect).
	Files map[string]string `json:"files"`
	Image string            `json:"image,omitempty"` // "debian" (default) or "alpine"
}

// EnvPlanResponse answers POST /v1/jx/env/plan.
type EnvPlanResponse struct {
	Plan   envplan.Plan `json:"plan"`
	Distro string       `json:"distro"`
	Script string       `json:"script"`
}

// EnvRunRequest is the body of POST /v1/jx/env/run.
type EnvRunRequest struct {
	VM      string `json:"vm"`
	Command string `json:"command"`
	// Outputs are paths relative to ~/work copied back as a tar stream.
	Outputs []string `json:"outputs,omitempty"`
}

// envRunner runs one command in a VM, streaming its output. Tests fake it.
type envRunner interface {
	Run(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
}

// jxEnvRunner builds the runner for a VM (SSH; replaced in tests).
var jxEnvRunner = func(s *Server, info *vmm.Info) envRunner { return sshRunner{s.vmTarget(info)} }

type sshRunner struct{ t sshexec.Target }

func (r sshRunner) Run(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	client, err := r.t.Dial(ctx)
	if err != nil {
		return -1, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return -1, err
	}
	defer sess.Close()
	sess.Stdin, sess.Stdout, sess.Stderr = stdin, stdout, stderr
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sess.Signal(ssh.SIGTERM)
			sess.Close()
			client.Close()
		case <-done:
		}
	}()
	err = sess.Run(command)
	close(done)
	var ee *ssh.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitStatus(), nil
	case ctx.Err() != nil:
		return -1, ctx.Err()
	}
	return -1, err
}

// ---- plan ------------------------------------------------------------------

func (s *Server) handleJXEnvPlan(w http.ResponseWriter, r *http.Request) {
	var req EnvPlanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	image, err := vmm.NormalizeImage(req.Image)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	files := map[string][]byte{}
	for p, data := range req.Files {
		files[p] = []byte(data)
	}
	res := EnvPlanResponse{Plan: envplan.FromFiles(files), Distro: envplan.DistroFor(image)}
	if res.Script, err = envplan.Script(res.Plan, res.Distro); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- NDJSON stream ---------------------------------------------------------

type ndjson struct {
	mu  sync.Mutex
	enc *json.Encoder
	fl  http.Flusher
}

func newNDJSON(w http.ResponseWriter) *ndjson {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	n := &ndjson{enc: json.NewEncoder(w), fl: fl}
	n.flush()
	return n
}

func (n *ndjson) flush() {
	if n.fl != nil {
		n.fl.Flush()
	}
}

func (n *ndjson) send(ev EnvEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enc.Encode(ev)
	n.flush()
}

func (n *ndjson) status(format string, args ...any) {
	n.send(EnvEvent{Type: "status", Text: fmt.Sprintf(format, args...)})
}

func (n *ndjson) fail(err error) { n.send(EnvEvent{Type: "error", Error: err.Error()}) }

func (n *ndjson) done(code int) { n.send(EnvEvent{Type: "done", Code: &code}) }

// abort ends the stream on a failure: an error line, then done -1.
func (n *ndjson) abort(err error) {
	n.fail(err)
	n.done(-1)
}

// textWriter turns guest output into stdout/stderr events, holding back an
// incomplete UTF-8 sequence until its rest arrives.
type textWriter struct {
	n    *ndjson
	typ  string
	pend []byte
}

func (t *textWriter) Write(p []byte) (int, error) {
	buf := append(t.pend, p...)
	cut := len(buf)
	for i := len(buf) - 1; i >= 0 && i >= len(buf)-utf8.UTFMax; i-- {
		if utf8.RuneStart(buf[i]) {
			if !utf8.FullRune(buf[i:]) {
				cut = i
			}
			break
		}
	}
	if cut > 0 {
		t.n.send(EnvEvent{Type: t.typ, Text: string(buf[:cut])})
	}
	t.pend = bytes.Clone(buf[cut:])
	return len(p), nil
}

func (t *textWriter) Flush() {
	if len(t.pend) > 0 {
		t.n.send(EnvEvent{Type: t.typ, Text: string(t.pend)})
		t.pend = nil
	}
}

// chunkWriter sends binary data as base64 "tar" events of up to 48 KiB.
type chunkWriter struct {
	n   *ndjson
	buf []byte
}

const tarChunk = 48 << 10

func (c *chunkWriter) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	for len(c.buf) >= tarChunk {
		c.n.send(EnvEvent{Type: "tar", Data: base64.StdEncoding.EncodeToString(c.buf[:tarChunk])})
		c.buf = c.buf[tarChunk:]
	}
	return len(p), nil
}

func (c *chunkWriter) Flush() {
	if len(c.buf) > 0 {
		c.n.send(EnvEvent{Type: "tar", Data: base64.StdEncoding.EncodeToString(c.buf)})
		c.buf = nil
	}
}

// ---- VM up -----------------------------------------------------------------

// envVMUp starts vm when it is stopped (through jxEnsureVMUp when the idle
// feature provides it) and waits until SSH answers.
func (s *Server) envVMUp(ctx context.Context, name string, n *ndjson) (*vmm.Info, error) {
	info, err := s.VMs.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if info.State != "running" || info.IP == "" {
		n.status("starting %s", name)
		if jxEnsureVMUp != nil {
			if err := jxEnsureVMUp(s, ctx, name); err != nil {
				return nil, err
			}
			if info, err = s.VMs.Get(ctx, name); err != nil {
				return nil, err
			}
		} else {
			if info, err = s.VMs.Start(ctx, name); err != nil {
				return nil, err
			}
			s.Leases().Touch(name)
		}
	}
	return info, s.envWaitSSH(ctx, info)
}

func (s *Server) envWaitSSH(ctx context.Context, info *vmm.Info) error {
	ctx, cancel := context.WithTimeout(ctx, envSSHWait)
	defer cancel()
	run := jxEnvRunner(s, info)
	for {
		code, err := run.Run(ctx, "true", nil, io.Discard, io.Discard)
		if err == nil && code == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			if err == nil {
				err = fmt.Errorf("exit %d", code)
			}
			return fmt.Errorf("%s: SSH did not answer: %v", info.Name, err)
		case <-time.After(2 * time.Second):
		}
	}
}

// ---- up --------------------------------------------------------------------

// errUpload marks a malformed upload (400); errTooLarge an oversized one.
var (
	errUpload   = errors.New("bad upload")
	errTooLarge = errors.New("upload too large")
)

// spoolTar copies the uploaded tar stream to a temp file under dir while
// reading it: it collects the manifests the plan needs and refuses entries
// that could land outside ~/work (absolute, .., through a symlink) or are
// not files, directories or symlinks.
func spoolTar(body io.Reader, dir string, max int64) (spool string, files map[string][]byte, st envplan.PackStats, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, st, err
	}
	f, err := os.CreateTemp(dir, "upload-*.tar")
	if err != nil {
		return "", nil, st, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(f.Name())
		}
	}()
	lr := &io.LimitedReader{R: body, N: max + 1}
	tee := io.TeeReader(lr, f)
	tr := tar.NewReader(tee)
	files = map[string][]byte{}
	links := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if lr.N <= 0 {
				return "", nil, st, errTooLarge
			}
			return "", nil, st, fmt.Errorf("%w: %v", errUpload, err)
		}
		name := path.Clean(h.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return "", nil, st, fmt.Errorf("%w: entry %q leaves the project", errUpload, h.Name)
		}
		for p := path.Dir(name); p != "." && p != "/"; p = path.Dir(p) {
			if links[p] {
				return "", nil, st, fmt.Errorf("%w: entry %q goes through symlink %q", errUpload, h.Name, p)
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeSymlink:
			links[name] = true
		case tar.TypeReg:
			st.Files++
			st.Bytes += h.Size
			if envplan.IsManifest(name) {
				var data []byte
				if !envplan.PresenceOnly(name) {
					if data, err = io.ReadAll(io.LimitReader(tr, envplan.MaxManifestSize)); err != nil {
						return "", nil, st, fmt.Errorf("%w: %v", errUpload, err)
					}
				}
				files[name] = data
			}
		default:
			return "", nil, st, fmt.Errorf("%w: entry %q has unsupported type %q", errUpload, h.Name, h.Typeflag)
		}
	}
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return "", nil, st, err
	}
	if lr.N <= 0 {
		return "", nil, st, errTooLarge
	}
	return f.Name(), files, st, nil
}

func queryInt(q map[string][]string, key string) (int, error) {
	v := ""
	if vals := q[key]; len(vals) > 0 {
		v = vals[0]
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: not a number: %q", key, v)
	}
	return n, nil
}

// handleJXEnvUp: POST /v1/jx/env/up?vm=NAME[&image=debian|alpine][&mem=MB]
// [&cpus=N][&disk=GB], body = tar of the project (envplan.WriteTar). It
// creates the VM if missing (else starts it), unpacks the project into
// ~/work and runs the bootstrap envplan derives from its manifests.
func (s *Server) handleJXEnvUp(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := q.Get("vm")
	if err := vmm.ValidateName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	image, err := vmm.NormalizeImage(q.Get("image"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var spec vmm.Spec
	for key, dst := range map[string]*int{"mem": &spec.MemoryMB, "cpus": &spec.CPUs, "disk": &spec.DiskGB} {
		if *dst, err = queryInt(q, key); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if jxSnapBusy(name) {
		writeErr(w, http.StatusConflict, fmt.Errorf("a snapshot or restore of %s is running", name))
		return
	}
	// Take the whole upload before answering: nothing streams back until
	// the project is in.
	spool, files, stats, err := spoolTar(r.Body, filepath.Join(s.jxDir(), "tmp"), envMaxUpload)
	switch {
	case errors.Is(err, errTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("upload exceeds %d GiB", envMaxUpload>>30))
		return
	case errors.Is(err, errUpload):
		writeErr(w, http.StatusBadRequest, err)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer os.Remove(spool)
	defer s.JXHold(name, "env")()

	ctx := r.Context()
	n := newNDJSON(w)
	info, err := s.VMs.Get(ctx, name)
	switch {
	case errors.Is(err, vmm.ErrNotFound):
		n.status("creating %s", name)
		spec.Name, spec.Image = name, image
		s.fillSpec(&spec)
		// the same memory guard as POST /v1/vms (jx_idle.go)
		if ctl, cerr := s.jxIdleCtl(); cerr == nil {
			if err = ctl.MakeRoom(ctx, name, spec.MemoryMB); err != nil {
				n.abort(fmt.Errorf("create %s: %w", name, err))
				return
			}
		}
		if info, err = s.VMs.Create(ctx, spec); err != nil {
			n.abort(fmt.Errorf("create %s: %w", name, err))
			return
		}
		s.PostNews("vm", "VM created", vmNewsLine(spec))
		s.Leases().Touch(name)
		if err = s.envWaitSSH(ctx, info); err != nil {
			n.abort(err)
			return
		}
	case err != nil:
		n.abort(err)
		return
	default:
		if image != "" && image != info.Image && !(image == vmm.ImageDebian && info.Image == "") {
			n.status("%s already exists with image %q; -image %s is ignored", name, info.Image, image)
		}
		if info, err = s.envVMUp(ctx, name, n); err != nil {
			n.abort(err)
			return
		}
	}
	run := jxEnvRunner(s, info)
	stdout, stderr := &textWriter{n: n, typ: "stdout"}, &textWriter{n: n, typ: "stderr"}
	if stats.Files > 0 {
		n.status("uploading %d files (%d bytes) to ~/work", stats.Files, stats.Bytes)
		f, err := os.Open(spool)
		if err != nil {
			n.abort(err)
			return
		}
		code, err := run.Run(ctx, `mkdir -p "$HOME/work" && tar -xf - -C "$HOME/work"`, f, stdout, stderr)
		f.Close()
		stdout.Flush()
		stderr.Flush()
		if err == nil && code != 0 {
			err = fmt.Errorf("unpacking the project failed (tar exit %d)", code)
		}
		if err != nil {
			n.abort(err)
			return
		}
	}

	plan := envplan.FromFiles(files)
	n.send(EnvEvent{Type: "plan", Plan: &plan})
	distro := envplan.DistroFor(info.Image)
	script, err := envplan.Script(plan, distro)
	if err != nil {
		n.abort(err)
		return
	}
	n.status("bootstrapping (%s); the script stays at ~/.exe-env/bootstrap.sh", distro)
	code, err := run.Run(ctx, `mkdir -p "$HOME/.exe-env" && cat >"$HOME/.exe-env/bootstrap.sh" && exec sh "$HOME/.exe-env/bootstrap.sh" </dev/null`,
		strings.NewReader(script), stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		n.fail(err)
		code = -1
	}
	n.done(code)
}

// ---- run -------------------------------------------------------------------

// cleanOutput validates an output path: relative to ~/work, inside it.
func cleanOutput(p string) (string, error) {
	c := path.Clean(strings.TrimSpace(p))
	if c == "." || c == "" || path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("output %q must be a path inside ~/work", p)
	}
	return c, nil
}

// handleJXEnvRun: POST /v1/jx/env/run with EnvRunRequest. It runs the
// command in ~/work through a login shell, streams its output, then copies
// the listed outputs back as tar events.
func (s *Server) handleJXEnvRun(w http.ResponseWriter, r *http.Request) {
	var req EnvRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := vmm.ValidateName(req.VM); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("command is required"))
		return
	}
	var outs []string
	for _, o := range req.Outputs {
		c, err := cleanOutput(o)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		outs = append(outs, sshexec.Quote("./"+c))
	}
	if jxSnapBusy(req.VM) {
		writeErr(w, http.StatusConflict, fmt.Errorf("a snapshot or restore of %s is running", req.VM))
		return
	}
	if _, err := s.VMs.Get(r.Context(), req.VM); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	defer s.JXHold(req.VM, "env")()

	ctx := r.Context()
	n := newNDJSON(w)
	info, err := s.envVMUp(ctx, req.VM, n)
	if err != nil {
		n.abort(err)
		return
	}
	run := jxEnvRunner(s, info)
	stdout, stderr := &textWriter{n: n, typ: "stdout"}, &textWriter{n: n, typ: "stderr"}
	code, err := run.Run(ctx, `mkdir -p "$HOME/work" && cd "$HOME/work" && exec sh -lc `+sshexec.Quote(req.Command), nil, stdout, stderr)
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		n.abort(err)
		return
	}
	n.send(EnvEvent{Type: "exit", Code: &code})
	if len(outs) > 0 {
		n.status("copying back %s", strings.Join(req.Outputs, ", "))
		tw := &chunkWriter{n: n}
		tcode, err := run.Run(ctx, `cd "$HOME/work" && tar -cf - `+strings.Join(outs, " "), nil, tw, stderr)
		tw.Flush()
		stderr.Flush()
		if err == nil && tcode != 0 {
			err = fmt.Errorf("copying outputs failed (tar exit %d)", tcode)
		}
		if err != nil {
			n.fail(err)
		}
	}
	n.done(code)
}
