package board

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeRunner replays a recorded CLI stream per agent. With gate set, each
// turn waits for a value on it (or its cancel) before replaying.
type fakeRunner struct {
	mu      sync.Mutex
	specs   []Spec
	fixture map[string]string
	gate    chan struct{}
	started chan string // turn ids, as turns start
	err     error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{fixture: map[string]string{Claude: "claude_tool.jsonl", Codex: "codex_tool.jsonl"},
		started: make(chan string, 16)}
}

func (f *fakeRunner) Run(ctx context.Context, spec Spec, status func(string), line func([]byte)) error {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	gate, fixture, err := f.gate, f.fixture[spec.Agent], f.err
	f.mu.Unlock()
	f.started <- spec.TurnID
	status("starting " + spec.Target)
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b, rerr := os.ReadFile(filepath.Join("testdata", fixture))
	if rerr != nil {
		return rerr
	}
	for _, l := range bytes.Split(b, []byte("\n")) {
		line(l)
	}
	return err
}

func (f *fakeRunner) spec(i int) Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[i]
}

func newEngine(t *testing.T, r Runner) *Engine {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewEngine(s, r)
}

// settled waits until the thread's turn loop has finished.
func settled(t *testing.T, e *Engine, id string) Thread {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !e.Running(id) {
			th, _, _ := e.Store().Get(id)
			return th
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("thread never settled")
	return Thread{}
}

func waitStarted(t *testing.T, f *fakeRunner) string {
	t.Helper()
	select {
	case id := <-f.started:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
		return ""
	}
}

func TestEngineRunsTurnAndResumesSession(t *testing.T) {
	f := newFakeRunner()
	e := newEngine(t, f)
	var changes []Thread
	var mu sync.Mutex
	e.OnChange = func(t Thread, _ bool) { mu.Lock(); changes = append(changes, t); mu.Unlock() }

	th, turn, queued, err := e.Submit(SubmitRequest{Target: "dev", Agent: Claude, Prompt: "  run the tests\nplease  "})
	if err != nil || queued {
		t.Fatalf("submit = %v queued=%v", err, queued)
	}
	if th.Title != "run the tests" || th.Origin != "user" {
		t.Errorf("thread = %+v", th)
	}
	waitStarted(t, f)
	th = settled(t, e, th.ID)
	if th.State != ThreadIdle || th.SessionID != "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10" || th.LastText != "All 12 tests pass. There is no NOTES.md yet." {
		t.Errorf("thread after turn = %+v", th)
	}
	_, views, err := e.View(th.ID)
	if err != nil || len(views) != 1 {
		t.Fatalf("view = %v %v", views, err)
	}
	v := views[0]
	if v.ID != turn.ID || v.State != TurnDone || v.StartedAt == nil || v.EndedAt == nil || v.Usage == nil || v.Usage.OutputTokens != 210 {
		t.Errorf("turn = %+v", v.Turn)
	}
	if got := types(v.Events); got != "turn_start,status,text,tool,tool_result,tool,tool_result,tool,tool_result,text,turn_end" {
		t.Errorf("events = %s", got)
	}
	for i, ev := range v.Events {
		if ev.Seq != int64(i+1) || ev.TurnID != turn.ID {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	if v.Events[0].Prompt != "run the tests\nplease" || v.Events[len(v.Events)-1].State != TurnDone {
		t.Errorf("start/end = %+v / %+v", v.Events[0], v.Events[len(v.Events)-1])
	}

	// the next turn resumes the session the first one reported
	if _, _, _, err := e.Submit(SubmitRequest{ThreadID: th.ID, Prompt: "and again"}); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, f)
	settled(t, e, th.ID)
	if s := f.spec(1); s.SessionID != "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10" || s.Prompt != "and again" || s.Fork {
		t.Errorf("second spec = %+v", s)
	}
	mu.Lock()
	n := len(changes)
	mu.Unlock()
	if n < 4 {
		t.Errorf("only %d change notifications", n)
	}
}

func TestEngineQueuesOneTurnAtATime(t *testing.T) {
	f := newFakeRunner()
	f.gate = make(chan struct{})
	e := newEngine(t, f)
	th, t1, _, _ := e.Submit(SubmitRequest{Target: "dev", Agent: Codex, Prompt: "one"})
	if waitStarted(t, f) != t1.ID {
		t.Fatal("first turn did not start first")
	}
	_, t2, queued, err := e.Submit(SubmitRequest{ThreadID: th.ID, Prompt: "two"})
	if err != nil || !queued {
		t.Fatalf("second submit queued=%v err=%v", queued, err)
	}
	_, t3, queued, _ := e.Submit(SubmitRequest{ThreadID: th.ID, Prompt: "three"})
	if !queued {
		t.Fatal("third turn not queued")
	}
	got, _, _ := e.Store().Get(th.ID)
	if got.State != ThreadRunning || got.Queued != 2 {
		t.Errorf("while running = %s queued %d", got.State, got.Queued)
	}
	select {
	case id := <-f.started:
		t.Fatalf("turn %s started while another ran", id)
	case <-time.After(50 * time.Millisecond):
	}
	f.gate <- struct{}{}
	if waitStarted(t, f) != t2.ID {
		t.Fatal("queue out of order")
	}
	f.gate <- struct{}{}
	if waitStarted(t, f) != t3.ID {
		t.Fatal("queue out of order")
	}
	f.gate <- struct{}{}
	got = settled(t, e, th.ID)
	if got.State != ThreadIdle || got.Queued != 0 || got.SessionID != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Errorf("after queue = %+v", got)
	}
	if f.spec(1).SessionID == "" {
		t.Error("queued turn did not resume the session")
	}
}

func TestEngineStopKillsTurnAndDropsQueue(t *testing.T) {
	f := newFakeRunner()
	f.gate = make(chan struct{}) // never opened: the turn runs until stopped
	e := newEngine(t, f)
	th, t1, _, _ := e.Submit(SubmitRequest{Target: "dev", Agent: Claude, Prompt: "long job"})
	waitStarted(t, f)
	_, t2, _, _ := e.Submit(SubmitRequest{ThreadID: th.ID, Prompt: "after"})

	sub, cancel := e.Subscribe(th.ID)
	defer cancel()
	if err := e.Stop(th.ID); err != nil {
		t.Fatal(err)
	}
	got := settled(t, e, th.ID)
	if got.State != ThreadIdle || got.Queued != 0 {
		t.Errorf("after stop = %+v", got)
	}
	_, views, _ := e.View(th.ID)
	if views[0].ID != t1.ID || views[0].State != TurnStopped || views[1].ID != t2.ID || views[1].State != TurnStopped {
		t.Errorf("turns = %+v / %+v", views[0].Turn, views[1].Turn)
	}
	if views[1].StartedAt != nil {
		t.Error("the dropped turn ran")
	}
	ends := 0
	for done := false; !done; {
		select {
		case ev := <-sub:
			if ev.Type == EvTurnEnd && ev.State == TurnStopped {
				ends++
			}
		case <-time.After(100 * time.Millisecond):
			done = true
		}
	}
	if ends != 2 {
		t.Errorf("%d stopped turn_end events, want 2", ends)
	}
	select {
	case id := <-f.started:
		t.Errorf("turn %s started after stop", id)
	default:
	}
}

func TestEngineRunnerErrorEndsTurnInError(t *testing.T) {
	f := newFakeRunner()
	f.fixture[Claude] = "claude_error.jsonl"
	e := newEngine(t, f)
	th, _, _, _ := e.Submit(SubmitRequest{Target: "host", Agent: Claude, Prompt: "x"})
	waitStarted(t, f)
	got := settled(t, e, th.ID)
	_, views, _ := e.View(th.ID)
	if got.State != ThreadError || views[0].Error != "Invalid API key · Please run /login" {
		t.Errorf("thread %s, turn %+v", got.State, views[0].Turn)
	}

	// an exit without a stream error reports the runner's own
	f.fixture[Claude] = "claude_resume.jsonl"
	f.err = errors.New("exit status 2: boom")
	e.Submit(SubmitRequest{ThreadID: th.ID, Prompt: "y"})
	waitStarted(t, f)
	settled(t, e, th.ID)
	_, views, _ = e.View(th.ID)
	if views[1].State != TurnError || views[1].Error != "exit status 2: boom" {
		t.Errorf("turn = %+v", views[1].Turn)
	}
}

func TestEngineForkFirstTurnOnly(t *testing.T) {
	f := newFakeRunner()
	f.fixture[Claude] = "claude_resume.jsonl"
	e := newEngine(t, f)
	th, _, _, err := e.Submit(SubmitRequest{Target: "dev", Agent: Claude, Session: "11111111-2222-4333-8444-555555555555", Fork: true, Prompt: "branch off"})
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, f)
	got := settled(t, e, th.ID)
	if s := f.spec(0); s.SessionID != "11111111-2222-4333-8444-555555555555" || !s.Fork {
		t.Errorf("first spec = %+v", s)
	}
	if got.Fork || got.SessionID != "5d3e8f20-1a2b-4c3d-9e8f-001122334455" {
		t.Errorf("thread after fork = %+v", got)
	}
}

func TestEngineRejectsBadRequests(t *testing.T) {
	e := newEngine(t, newFakeRunner())
	for _, req := range []SubmitRequest{
		{Target: "dev", Agent: Claude, Prompt: "  "},
		{Target: "", Agent: Claude, Prompt: "x"},
		{Target: "dev", Agent: "gemini", Prompt: "x"},
		{Target: "dev", Agent: Claude, Fork: true, Prompt: "x"},
		{Target: "dev", Agent: Codex, Session: "abc", Fork: true, Prompt: "x"},
	} {
		var in *InputError
		if _, _, _, err := e.Submit(req); !errors.As(err, &in) {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
	if _, _, _, err := e.Submit(SubmitRequest{ThreadID: "0123456789abcdef", Prompt: "x"}); err != ErrNotFound {
		t.Errorf("unknown thread: %v", err)
	}
}

func TestEngineResumeAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir)
	now := time.Now().UTC()
	th := Thread{ID: NewID(), Target: "dev", Agent: Claude, State: ThreadRunning, CreatedAt: now, UpdatedAt: now}
	s.Create(th)
	s.Update(th.ID, func(t *Thread, turns []Turn) ([]Turn, error) {
		return append(turns,
			Turn{ID: "a", Prompt: "was running", State: TurnRunning, StartedAt: &now},
			Turn{ID: "b", Prompt: "was queued", State: TurnQueued}), nil
	})

	s2, _ := OpenStore(dir) // the daemon came back
	f := newFakeRunner()
	e := NewEngine(s2, f)
	e.Resume()
	if waitStarted(t, f) != "b" {
		t.Fatal("queued turn did not restart")
	}
	settled(t, e, th.ID)
	_, views, _ := e.View(th.ID)
	if views[0].State != TurnError || views[0].Error == "" || views[1].State != TurnDone {
		t.Errorf("turns = %+v / %+v", views[0].Turn, views[1].Turn)
	}
}

func TestEnginePatchAndDelete(t *testing.T) {
	f := newFakeRunner()
	f.gate = make(chan struct{})
	e := newEngine(t, f)
	th, _, _, _ := e.Submit(SubmitRequest{Target: "dev", Agent: Claude, Prompt: "x"})
	waitStarted(t, f)
	title, yes := "Renamed", true
	got, err := e.Patch(th.ID, &title, &yes)
	if err != nil || got.Title != "Renamed" || !got.Archived {
		t.Fatalf("patch = %+v %v", got, err)
	}
	empty := " "
	if _, err := e.Patch(th.ID, &empty, nil); err == nil {
		t.Error("empty title accepted")
	}
	sub, _ := e.Subscribe(th.ID)
	if err := e.Delete(th.ID); err != nil {
		t.Fatal(err)
	}
	for range sub { // closed by the delete
	}
	if _, _, err := e.View(th.ID); err != ErrNotFound {
		t.Errorf("view after delete = %v", err)
	}
	if e.Running(th.ID) {
		t.Error("loop survives delete")
	}
}
