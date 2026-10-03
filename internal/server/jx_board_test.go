package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"exe/internal/config"
	"exe/internal/jx/board"
	"exe/internal/vmm"
)

// boardVMs is a VM backend with a running "dev" and a stopped "idle".
type boardVMs struct {
	vmm.Manager
	mu     sync.Mutex
	vms    map[string]*vmm.Info
	starts int
}

func newBoardVMs() *boardVMs {
	return &boardVMs{vms: map[string]*vmm.Info{
		"dev":  {Name: "dev", State: "running", IP: "192.0.2.10"},
		"idle": {Name: "idle", State: "stopped"},
	}}
}

func (v *boardVMs) Get(_ context.Context, name string) (*vmm.Info, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, ok := v.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	c := *i
	return &c, nil
}

func (v *boardVMs) Start(_ context.Context, name string) (*vmm.Info, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, ok := v.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	i.State, i.IP = "running", "192.0.2.11"
	v.starts++
	c := *i
	return &c, nil
}

// fixtureRunner replays the recorded CLI streams in internal/jx/board;
// with gate set, a turn waits for it (or its stop) first.
func fixtureRunner(gate chan struct{}) board.Runner {
	return board.RunnerFunc(func(ctx context.Context, spec board.Spec, status func(string), line func([]byte)) error {
		status("starting " + spec.Target)
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		name := "claude_tool.jsonl"
		if spec.Agent == board.Codex {
			name = "codex_tool.jsonl"
		}
		b, err := os.ReadFile(filepath.Join("..", "jx", "board", "testdata", name))
		if err != nil {
			return err
		}
		for _, l := range bytes.Split(b, []byte("\n")) {
			line(l)
		}
		return nil
	})
}

func newBoardServer(t *testing.T, cfg *config.Config, runner board.Runner) (*Server, *httptest.Server, *jxBoard, *boardVMs) {
	t.Helper()
	vms := newBoardVMs()
	if cfg == nil {
		cfg = &config.Config{SSHUser: "exe"}
	}
	s := New(cfg, vms, nil, "", t.TempDir())
	b, err := s.jxBoardOf(runner)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv, b, vms
}

func call(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, url, err, raw)
		}
	}
	return resp.StatusCode
}

func waitIdle(t *testing.T, b *jxBoard, id string) board.Thread {
	t.Helper()
	for i := 0; i < 500; i++ {
		if !b.eng.Running(id) {
			th, _, _ := b.eng.Store().Get(id)
			return th
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("thread never settled")
	return board.Thread{}
}

func TestBoardThreadLifecycleOverHTTP(t *testing.T) {
	gate := make(chan struct{}, 4)
	_, srv, b, _ := newBoardServer(t, nil, fixtureRunner(gate))
	api := srv.URL + "/v1/jx/board"

	for _, bad := range []struct {
		body map[string]any
		code int
	}{
		{map[string]any{"target": "nope", "agent": "claude", "prompt": "x"}, http.StatusNotFound},
		{map[string]any{"target": "dev", "agent": "gemini", "prompt": "x"}, http.StatusBadRequest},
		{map[string]any{"target": "dev", "agent": "claude", "prompt": " "}, http.StatusBadRequest},
		{map[string]any{"target": "dev", "agent": "claude", "session": "x; rm -rf ~", "prompt": "x"}, http.StatusBadRequest},
		{map[string]any{"target": "dev", "agent": "codex", "session": "abc", "fork": true, "prompt": "x"}, http.StatusBadRequest},
	} {
		var e map[string]string
		if code := call(t, "POST", api+"/threads", bad.body, &e); code != bad.code || e["error"] == "" {
			t.Errorf("%v: %d %v", bad.body, code, e)
		}
	}

	var created struct {
		Thread board.Thread `json:"thread"`
		TurnID string       `json:"turn_id"`
	}
	if code := call(t, "POST", api+"/threads", map[string]any{"target": "dev", "agent": "claude", "prompt": "run the tests"}, &created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	id := created.Thread.ID
	if created.TurnID == "" || created.Thread.Title != "run the tests" || created.Thread.Origin != "user" {
		t.Fatalf("created = %+v", created)
	}

	for i := 0; i < 500; i++ { // the turn has started (and waits at the gate)
		if th, _, _ := b.eng.Store().Get(id); th.State == board.ThreadRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// a message sent while the turn runs waits its turn
	var said struct {
		TurnID string `json:"turn_id"`
		Queued bool   `json:"queued"`
	}
	if code := call(t, "POST", api+"/threads/"+id+"/turns", map[string]string{"prompt": "and the lint"}, &said); code != http.StatusAccepted || !said.Queued {
		t.Fatalf("say = %d %+v", code, said)
	}
	var list []board.Thread
	call(t, "GET", api+"/threads", nil, &list)
	if len(list) != 1 || list[0].State != board.ThreadRunning || list[0].Queued != 1 {
		t.Fatalf("list while running = %+v", list)
	}
	gate <- struct{}{}
	gate <- struct{}{}
	waitIdle(t, b, id)

	var view struct {
		Thread board.Thread     `json:"thread"`
		Turns  []board.TurnView `json:"turns"`
	}
	if code := call(t, "GET", api+"/threads/"+id, nil, &view); code != http.StatusOK {
		t.Fatalf("get = %d", code)
	}
	if len(view.Turns) != 2 || view.Turns[0].State != "done" || view.Turns[1].ID != said.TurnID || len(view.Turns[1].Events) != 11 {
		t.Fatalf("view = %+v", view)
	}
	if view.Thread.SessionID != "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10" || view.Thread.State != "idle" {
		t.Errorf("thread = %+v", view.Thread)
	}

	// the stream replays what came after ?after=, by seq
	resp, err := http.Get(api + "/threads/" + id + "/events?after=15")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var seqs []int64
	for sc.Scan() && len(seqs) < 7 {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var ev board.Event
			json.Unmarshal([]byte(d), &ev)
			seqs = append(seqs, ev.Seq)
		}
	}
	resp.Body.Close()
	if len(seqs) != 7 || seqs[0] != 16 || seqs[6] != 22 {
		t.Errorf("replayed seqs = %v", seqs)
	}

	title := "Tests"
	var patched board.Thread
	if code := call(t, "PATCH", api+"/threads/"+id, map[string]any{"title": title, "archived": true}, &patched); code != http.StatusOK || patched.Title != "Tests" || !patched.Archived {
		t.Errorf("patch = %d %+v", code, patched)
	}
	if code := call(t, "POST", api+"/threads/"+id+"/stop", nil, nil); code != http.StatusNoContent {
		t.Errorf("stop idle = %d", code)
	}
	if code := call(t, "DELETE", api+"/threads/"+id, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code := call(t, "GET", api+"/threads/"+id, nil, nil); code != http.StatusNotFound {
		t.Errorf("get deleted = %d", code)
	}
	if code := call(t, "POST", api+"/threads/"+id+"/stop", nil, nil); code != http.StatusNotFound {
		t.Errorf("stop deleted = %d", code)
	}
}

func TestBoardStopOverHTTP(t *testing.T) {
	_, srv, b, _ := newBoardServer(t, nil, fixtureRunner(make(chan struct{})))
	var created struct {
		Thread board.Thread `json:"thread"`
	}
	call(t, "POST", srv.URL+"/v1/jx/board/threads", map[string]any{"target": "host", "agent": "codex", "prompt": "forever"}, &created)
	id := created.Thread.ID
	for i := 0; i < 100 && !b.eng.Running(id); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if code := call(t, "POST", srv.URL+"/v1/jx/board/threads/"+id+"/stop", nil, nil); code != http.StatusNoContent {
		t.Fatalf("stop = %d", code)
	}
	waitIdle(t, b, id)
	_, turns, _ := b.eng.View(id)
	if turns[0].State != board.TurnStopped {
		t.Errorf("turn = %+v", turns[0].Turn)
	}
}

// The board-wide stream announces thread changes and pings; event streams
// take the API token as ?token=, as browsers' EventSource cannot set
// headers.
func TestBoardEventsStreamAndToken(t *testing.T) {
	old := boardPing
	boardPing = 50 * time.Millisecond
	defer func() { boardPing = old }()
	_, srv, b, _ := newBoardServer(t, &config.Config{SSHUser: "exe", APIToken: "tok"}, fixtureRunner(nil))

	if resp, err := http.Get(srv.URL + "/v1/jx/board/events"); err != nil {
		t.Fatal(err)
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d", resp.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/jx/board/events?token=tok", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events = %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	<-lines // ": connected"

	th, _, _, err := b.submit(context.Background(), BoardSubmitRequest{Target: "dev", Agent: "claude", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var sawThread, sawIdle, sawPing bool
	deadline := time.After(5 * time.Second)
	for !(sawThread && sawIdle && sawPing) {
		select {
		case l := <-lines:
			if l == ": ping" {
				sawPing = true
			}
			if d, ok := strings.CutPrefix(l, "data: "); ok {
				var m map[string]any
				json.Unmarshal([]byte(d), &m)
				if m["type"] == "thread" && m["thread_id"] == th.ID {
					sawThread = true
					if m["state"] == "idle" {
						sawIdle = true
					}
				}
			}
		case <-deadline:
			t.Fatalf("thread %v idle %v ping %v", sawThread, sawIdle, sawPing)
		}
	}
}

// guestFake answers the Board's guest scripts like a Debian VM would.
type guestFake struct {
	mu      sync.Mutex
	scripts []string
	stdins  []string
	probe   string
	listing string
	during  func()
}

func (g *guestFake) Exec(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	g.mu.Lock()
	g.scripts = append(g.scripts, script)
	g.stdins = append(g.stdins, in)
	g.mu.Unlock()
	if stdout == nil {
		stdout = io.Discard
	}
	switch {
	case script == board.ProbeScript || script == board.ReadyScript:
		io.WriteString(stdout, g.probe)
	case script == board.SessionsScript(board.Claude):
		io.WriteString(stdout, g.listing)
	case strings.Contains(script, "apt-get install"):
		io.WriteString(stdout, "exe: installing Claude Code (claude.ai/install.sh)\n")
	case strings.Contains(script, "exec claude "):
		if in != "fix it" {
			io.WriteString(stdout, `{"type":"result","is_error":true,"result":"wrong prompt on stdin"}`+"\n")
			return nil
		}
		if g.during != nil {
			g.during()
		}
		b, err := os.ReadFile(filepath.Join("..", "jx", "board", "testdata", "claude_resume.jsonl"))
		if err != nil {
			return err
		}
		stdout.Write(b)
	}
	return nil
}

// A VM turn starts the stopped VM, holds the board lease while it runs,
// writes the credentials over stdin, and runs the CLI with the prompt on
// stdin; no secret shows in a script, a response or the log.
func TestBoardVMTurnThroughGuest(t *testing.T) {
	ensure := jxEnsureVMUp
	jxEnsureVMUp = nil // this test drives the plain start path
	defer func() { jxEnsureVMUp = ensure }()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	s, srv, b, vms := newBoardServer(t, nil, nil)
	guest := &guestFake{probe: "os=debian\narch=x86_64\npath_claude=\npath_codex=\nhas_git=1\nhas_tmux=1\n"}
	var holders []string
	guest.during = func() {
		for _, h := range s.Leases().Get("idle").Holders {
			holders = append(holders, h.Reason)
		}
	}
	b.remote = func(info *vmm.Info) board.Remote { return guest }

	const tok = "sk-ant-oat01-TOPSECRET-value"
	var st map[string]bool
	if code := call(t, "PUT", srv.URL+"/v1/jx/agents/secrets", map[string]string{"claude_oauth_token": tok}, &st); code != http.StatusOK || !st["claude_oauth_token_set"] || st["codex_api_key_set"] {
		t.Fatalf("put secrets = %d %v", code, st)
	}

	var created struct {
		Thread board.Thread `json:"thread"`
	}
	if code := call(t, "POST", srv.URL+"/v1/jx/board/threads", map[string]any{"target": "idle", "agent": "claude", "prompt": "fix it"}, &created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	th := waitIdle(t, b, created.Thread.ID)
	_, turns, _ := b.eng.View(th.ID)
	if turns[0].State != board.TurnDone || th.SessionID != "5d3e8f20-1a2b-4c3d-9e8f-001122334455" {
		t.Fatalf("turn = %+v, thread %+v", turns[0].Turn, th)
	}
	var status []string
	for _, ev := range turns[0].Events {
		if ev.Type == board.EvStatus {
			status = append(status, ev.Text)
		}
	}
	if strings.Join(status, "|") != "starting idle|installing Claude Code in idle|installing Claude Code (claude.ai/install.sh)|context compacted" {
		t.Errorf("status = %q", status)
	}
	if vms.starts != 1 {
		t.Errorf("VM started %d times", vms.starts)
	}
	if strings.Join(holders, ",") != "board" {
		t.Errorf("holders during the turn = %v", holders)
	}
	if h := s.Leases().Get("idle").Holders; len(h) != 0 {
		t.Errorf("lease outlives the turn: %v", h)
	}

	guest.mu.Lock()
	scripts, stdins := guest.scripts, guest.stdins
	guest.mu.Unlock()
	wroteEnv := false
	for i, sc := range scripts {
		if strings.Contains(sc, tok) {
			t.Fatalf("the token reached a command line: %q", sc)
		}
		if strings.Contains(stdins[i], "CLAUDE_CODE_OAUTH_TOKEN='"+tok+"'") {
			wroteEnv = true
		}
	}
	if !wroteEnv {
		t.Error("the token never reached the guest")
	}

	var got map[string]any
	call(t, "GET", srv.URL+"/v1/jx/agents/secrets", nil, &got)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "TOPSECRET") || got["claude_oauth_token_set"] != true {
		t.Errorf("GET secrets = %s", raw)
	}
	var e map[string]string
	if code := call(t, "PUT", srv.URL+"/v1/jx/agents/secrets", map[string]any{"claude_oauth_token": 42}, &e); code != http.StatusBadRequest {
		t.Errorf("bad put = %d", code)
	}
	if strings.Contains(logs.String(), "TOPSECRET") {
		t.Error("the token reached the log")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(s.jxDir(), "secrets.json")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("secrets.json = %v %v", fi.Mode().Perm(), err)
		}
	}
	call(t, "PUT", srv.URL+"/v1/jx/agents/secrets", map[string]string{"claude_oauth_token": ""}, &st)
	if st["claude_oauth_token_set"] {
		t.Error("clearing the token did not clear it")
	}
}

func TestBoardSessionsAndAgentStatus(t *testing.T) {
	_, srv, b, _ := newBoardServer(t, nil, fixtureRunner(nil))
	guest := &guestFake{
		probe: "os=alpine\narch=aarch64\npath_claude=/home/exe/.local/bin/claude\nver_claude=2.0.14 (Claude Code)\nlogin_claude=1\npath_codex=\n",
		listing: "\x1eF 1700000300 /home/exe/.claude/projects/-home-exe-work/0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10.jsonl\n" +
			`{"cwd":"/home/exe/work","type":"user","message":{"role":"user","content":"fix the login bug"}}` + "\n",
	}
	b.remote = func(info *vmm.Info) board.Remote { return guest }

	// a thread on the session: the list names it
	th, _, _, _ := b.submit(context.Background(), BoardSubmitRequest{Target: "dev", Agent: "claude", Prompt: "x"})
	waitIdle(t, b, th.ID)

	var sessions []board.Session
	if code := call(t, "GET", srv.URL+"/v1/jx/board/sessions?target=dev&agent=claude", nil, &sessions); code != http.StatusOK {
		t.Fatalf("sessions = %d", code)
	}
	if len(sessions) != 1 || sessions[0].Title != "fix the login bug" || sessions[0].Cwd != "/home/exe/work" || sessions[0].ThreadID != th.ID {
		t.Errorf("sessions = %+v", sessions)
	}
	if code := call(t, "GET", srv.URL+"/v1/jx/board/sessions?target=idle&agent=claude", nil, nil); code != http.StatusConflict {
		t.Errorf("stopped VM sessions = %d", code)
	}
	if code := call(t, "GET", srv.URL+"/v1/jx/board/sessions?target=dev&agent=x", nil, nil); code != http.StatusBadRequest {
		t.Errorf("bad agent = %d", code)
	}

	var status map[string]board.AgentProbe
	if code := call(t, "GET", srv.URL+"/v1/jx/agents/status?target=dev", nil, &status); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if c := status["claude"]; !c.Installed || c.Version != "2.0.14 (Claude Code)" || c.Auth != "login" {
		t.Errorf("claude = %+v", c)
	}
	if c := status["codex"]; c.Installed || c.Auth != "none" {
		t.Errorf("codex = %+v", c)
	}
}

func TestBoardInstallReportsOnBoardEvents(t *testing.T) {
	_, srv, b, _ := newBoardServer(t, nil, fixtureRunner(nil))
	guest := &guestFake{}
	b.remote = func(info *vmm.Info) board.Remote { return guest }
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = true
	b.mu.Unlock()

	if code := call(t, "POST", srv.URL+"/v1/jx/agents/install", map[string]string{"target": "host", "agent": "claude"}, nil); code != http.StatusBadRequest {
		t.Errorf("host install = %d", code)
	}
	if code := call(t, "POST", srv.URL+"/v1/jx/agents/install", map[string]string{"target": "dev", "agent": "claude"}, nil); code != http.StatusAccepted {
		t.Fatalf("install = %d", code)
	}
	var texts []string
	for done := false; !done; {
		select {
		case raw := <-ch:
			var m map[string]any
			json.Unmarshal(raw, &m)
			if m["type"] != "install" {
				continue
			}
			texts = append(texts, m["text"].(string))
			done = m["done"] == true
			if m["error"] != nil {
				t.Errorf("install error: %v", m["error"])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("install never finished: %q", texts)
		}
	}
	if texts[0] != "installing Claude Code in dev" || texts[len(texts)-1] != "Claude Code is ready" {
		t.Errorf("install messages = %q", texts)
	}
}

// Cron posts through jxBoardSubmit, with its origin.
func TestBoardSubmitHook(t *testing.T) {
	s, _, b, _ := newBoardServer(t, nil, fixtureRunner(nil))
	threadID, turnID, err := jxBoardSubmit(s, context.Background(), BoardSubmitRequest{Target: "host", Agent: "codex", Prompt: "nightly report", Origin: "cron:j1"})
	if err != nil || threadID == "" || turnID == "" {
		t.Fatalf("submit = %q %q %v", threadID, turnID, err)
	}
	th := waitIdle(t, b, threadID)
	if th.Origin != "cron:j1" || th.State != board.ThreadIdle {
		t.Errorf("thread = %+v", th)
	}
	_, turn2, err := jxBoardSubmit(s, context.Background(), BoardSubmitRequest{ThreadID: threadID, Prompt: "again", Origin: "cron:j1"})
	if err != nil || turn2 == "" || turn2 == turnID {
		t.Errorf("second submit = %q %v", turn2, err)
	}
	waitIdle(t, b, threadID)
}
