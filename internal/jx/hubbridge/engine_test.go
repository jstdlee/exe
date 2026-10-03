package hubbridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"exe/internal/jx/board"
)

const (
	owner    = "00000000000000aa"
	stranger = "00000000000000bb"
	agentID  = "00000000000000cc"
)

type fakeHub struct {
	mu      sync.Mutex
	posts   map[string]Post
	replies []Post // posted by the agent
	n       int
	fail    error
}

func newFakeHub() *fakeHub { return &fakeHub{posts: map[string]Post{}} }

func (h *fakeHub) add(p Post) Post {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.posts[p.ID] = p
	return p
}

func (h *fakeHub) Post(_ context.Context, id string) (Post, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.posts[id]
	if !ok {
		return Post{}, fmt.Errorf("no post %s", id)
	}
	return p, nil
}

func (h *fakeHub) Reply(_ context.Context, replyTo, text string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail != nil {
		return "", h.fail
	}
	h.n++
	p := Post{ID: fmt.Sprintf("agent%d", h.n), Author: agentID, Text: text, ReplyTo: replyTo}
	h.posts[p.ID] = p
	h.replies = append(h.replies, p)
	return p.ID, nil
}

func (h *fakeHub) texts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.replies {
		out = append(out, r.ReplyTo+"|"+r.Text)
	}
	return out
}

type fakeTurn struct {
	thread string
	res    TurnResult
}

type fakeBoard struct {
	mu      sync.Mutex
	submits []SubmitRequest
	turns   map[string]*fakeTurn
	stopped []string
	n       int
	err     error
}

func newFakeBoard() *fakeBoard { return &fakeBoard{turns: map[string]*fakeTurn{}} }

func (b *fakeBoard) Submit(_ context.Context, req SubmitRequest) (string, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return "", "", b.err
	}
	b.n++
	b.submits = append(b.submits, req)
	th := req.ThreadID
	if th == "" {
		th = fmt.Sprintf("th%d", b.n)
	}
	turn := fmt.Sprintf("tu%d", b.n)
	b.turns[turn] = &fakeTurn{thread: th, res: TurnResult{State: board.TurnRunning}}
	return th, turn, nil
}

func (b *fakeBoard) Stop(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = append(b.stopped, id)
	for _, t := range b.turns {
		if t.thread == id && !t.res.Ended {
			t.res = TurnResult{Ended: true, State: board.TurnStopped}
		}
	}
	return nil
}

func (b *fakeBoard) Thread(id string) (ThreadStatus, error) {
	return ThreadStatus{State: board.ThreadIdle, LastText: "PONG"}, nil
}

func (b *fakeBoard) Turn(thread, turn string) (TurnResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.turns[turn]
	if !ok {
		return TurnResult{}, ErrBoardGone
	}
	return t.res, nil
}

func (b *fakeBoard) finish(turn string, res TurnResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	res.Ended = true
	b.turns[turn].res = res
}

func (b *fakeBoard) calls() []SubmitRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]SubmitRequest(nil), b.submits...)
}

func newEngine(t *testing.T, dir string, hub *fakeHub, b *fakeBoard, mod func(*Settings)) *Engine {
	t.Helper()
	set := Defaults()
	set.Owners = []string{owner, agentID}
	if mod != nil {
		mod(&set)
	}
	if err := set.Normalize(); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(dir, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{Settings: set, AgentID: agentID}, st, hub, b)
}

func TestParseTask(t *testing.T) {
	for _, c := range []struct {
		text string
		ok   bool
		want Task
	}{
		{"@agent fix the tests", true, Task{"claude", "host", "fix the tests"}},
		{"  @AGENT: fix it", true, Task{"claude", "host", "fix it"}},
		{"@agent codex on myvm: reply PONG", true, Task{"codex", "myvm", "reply PONG"}},
		{"@agent Claude on HOST: x", true, Task{"claude", "host", "x"}},
		{"@agent on dev: build", true, Task{"claude", "dev", "build"}},
		{"@agent codex: go", true, Task{"codex", "host", "go"}},
		{"@Bot codex on box: hi", true, Task{"codex", "box", "hi"}},
		{"@agent explain this: why does it fail", true, Task{"claude", "host", "explain this: why does it fail"}},
		{"@agent, do it", true, Task{"claude", "host", "do it"}},
		{"@agentx do it", false, Task{}},
		{"@agent", false, Task{}},
		{"@agent codex on vm:   ", false, Task{}},
		{"note to self @agent", false, Task{}},
		{"hello", false, Task{}},
	} {
		got, ok := ParseTask(c.text, "Bot", "claude", "host")
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q = %+v %v, want %+v %v", c.text, got, ok, c.want, c.ok)
		}
	}
}

func TestFinalTextAndClip(t *testing.T) {
	evs := []board.Event{
		{Type: board.EvText, Text: "looking"},
		{Type: board.EvTool, Name: "Bash"},
		{Type: board.EvToolResult, Output: "ok"},
		{Type: board.EvText, Text: "All done."},
		{Type: board.EvText, Text: "PONG"},
		{Type: board.EvTurnEnd, State: "done"},
	}
	if got := FinalText(evs); got != "All done.\n\nPONG" {
		t.Errorf("FinalText = %q", got)
	}
	if got := FinalText(evs[:1]); got != "looking" {
		t.Errorf("FinalText(text only) = %q", got)
	}
	long := strings.Repeat("é", MaxText) // 2 bytes each
	c := Clip(long, "th1")
	if len(c) > MaxText || !strings.HasSuffix(c, "… (full output in Board thread th1)") {
		t.Errorf("clip: %d bytes, tail %q", len(c), c[len(c)-50:])
	}
	if !strings.HasPrefix(c, "éé") || strings.ContainsRune(c, '�') {
		t.Error("clip broke a rune")
	}
	if Clip("short", "x") != "short" {
		t.Error("short text changed")
	}
}

func TestOwnerOnlyAndTrigger(t *testing.T) {
	hub, b := newFakeHub(), newFakeBoard()
	e := newEngine(t, t.TempDir(), hub, b, nil)
	ctx := context.Background()
	e.Handle(ctx, hub.add(Post{ID: "s1", Author: stranger, Text: "@agent rm -rf /"}), false)
	e.Handle(ctx, hub.add(Post{ID: "a1", Author: agentID, Text: "@agent loop"}), false)
	e.Handle(ctx, hub.add(Post{ID: "n1", Author: owner, Text: "just a note"}), false)
	if len(b.calls()) != 0 || len(hub.texts()) != 0 {
		t.Fatalf("acted on non-tasks: %v %v", b.calls(), hub.texts())
	}
	e.Handle(ctx, hub.add(Post{ID: "r1", Author: owner, Text: "@agent codex on host: reply PONG"}), false)
	calls := b.calls()
	if len(calls) != 1 || calls[0].Agent != "codex" || calls[0].Target != "host" || calls[0].Prompt != "reply PONG" || calls[0].ThreadID != "" {
		t.Fatalf("submit = %+v", calls)
	}
	if got := hub.texts(); len(got) != 1 || got[0] != "r1|on it — codex in host" {
		t.Fatalf("ack = %v", got)
	}
	// a stranger replying in the bridged thread is ignored too
	e.Handle(ctx, hub.add(Post{ID: "s2", Author: stranger, Text: "do evil", ReplyTo: "r1"}), false)
	if len(b.calls()) != 1 {
		t.Fatal("stranger's reply became a turn")
	}
}

func TestRepliesBecomeTurnsThroughNesting(t *testing.T) {
	hub, b := newFakeHub(), newFakeBoard()
	e := newEngine(t, t.TempDir(), hub, b, func(s *Settings) { s.Ack = false })
	ctx := context.Background()
	e.Handle(ctx, hub.add(Post{ID: "root", Author: owner, Text: "@agent: write a haiku"}), false)
	b.finish("tu1", TurnResult{State: board.TurnDone, Text: "a haiku"})
	e.CheckTurns(ctx)
	got := hub.texts()
	if len(got) != 1 || got[0] != "root|a haiku" {
		t.Fatalf("turn end post = %v", got)
	}
	// the owner answers the agent's reply (nested), then a reply to a
	// reply of a stranger in the same thread, unknown to the engine
	hub.add(Post{ID: "x1", Author: stranger, Text: "nice", ReplyTo: "agent1"})
	e.Handle(ctx, hub.add(Post{ID: "o1", Author: owner, Text: "another", ReplyTo: "agent1"}), false)
	// x1 was never handled: the engine asks the hub for its parent
	e.Handle(ctx, hub.add(Post{ID: "o2", Author: owner, Text: "and one more", ReplyTo: "x1"}), false)
	calls := b.calls()
	if len(calls) != 3 || calls[1].ThreadID != "th1" || calls[1].Prompt != "another" || calls[2].ThreadID != "th1" {
		t.Fatalf("turns = %+v", calls)
	}
	b.finish("tu2", TurnResult{State: board.TurnError, Error: "boom"})
	b.finish("tu3", TurnResult{State: board.TurnDone, Text: ""})
	e.CheckTurns(ctx)
	got = hub.texts()
	want := map[string]bool{"o1|⚠ boom": true, "o2|done (no text output)": true}
	for _, g := range got[1:] {
		if !want[g] {
			t.Errorf("unexpected post %q", g)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Errorf("missing posts %v (got %v)", want, got)
	}
	// a reply in a thread the bridge does not own is ignored
	hub.add(Post{ID: "other", Author: owner, Text: "my notes"})
	e.Handle(ctx, hub.add(Post{ID: "o3", Author: owner, Text: "more notes", ReplyTo: "other"}), false)
	if len(b.calls()) != 3 {
		t.Fatal("reply in a foreign thread became a turn")
	}
}

func TestStopAndStatus(t *testing.T) {
	hub, b := newFakeHub(), newFakeBoard()
	e := newEngine(t, t.TempDir(), hub, b, func(s *Settings) { s.Ack = false })
	ctx := context.Background()
	e.Handle(ctx, hub.add(Post{ID: "root", Author: owner, Text: "@agent: long job"}), false)
	e.Handle(ctx, hub.add(Post{ID: "st", Author: owner, Text: "/status", ReplyTo: "root"}), false)
	e.Handle(ctx, hub.add(Post{ID: "sp", Author: owner, Text: " /STOP ", ReplyTo: "root"}), false)
	if len(b.stopped) != 1 || b.stopped[0] != "th1" {
		t.Fatalf("stopped = %v", b.stopped)
	}
	if len(b.calls()) != 1 {
		t.Fatal("a command became a turn")
	}
	e.CheckTurns(ctx) // the stopped turn ends without a post of its own
	got := hub.texts()
	if len(got) != 2 || !strings.HasPrefix(got[0], "st|claude in host: idle (Board thread th1)\nlast: PONG") || got[1] != "sp|stopped" {
		t.Fatalf("posts = %q", got)
	}
	if len(e.st.Pending) != 0 {
		t.Fatal("stopped turn still pending")
	}
}

func TestIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	hub, b := newFakeHub(), newFakeBoard()
	e := newEngine(t, dir, hub, b, nil)
	ctx := context.Background()
	root := hub.add(Post{ID: "root", Author: owner, Text: "@agent: go", Received: 2000})
	e.Handle(ctx, root, false)
	e.Handle(ctx, root, true) // the same post again from catch-up
	if len(b.calls()) != 1 {
		t.Fatal("double submit")
	}
	// restart: a new engine over the same dir, catch-up replays the feed
	e = newEngine(t, dir, hub, b, nil)
	e.Handle(ctx, root, true)
	if len(b.calls()) != 1 {
		t.Fatal("submitted again after restart")
	}
	// the pending turn survives the restart and is posted once
	b.finish("tu1", TurnResult{State: board.TurnDone, Text: "PONG"})
	e.CheckTurns(ctx)
	e.CheckTurns(ctx)
	if got := hub.texts(); len(got) != 2 || got[1] != "root|PONG" {
		t.Fatalf("posts = %v", got)
	}
	// a hub failure keeps the turn pending until the post goes through
	e.Handle(ctx, hub.add(Post{ID: "o1", Author: owner, Text: "again", ReplyTo: "root"}), false)
	b.finish("tu2", TurnResult{State: board.TurnDone, Text: strings.Repeat("x", MaxText+10)})
	hub.fail = fmt.Errorf("hub down")
	e.CheckTurns(ctx)
	if e.LastError() == "" {
		t.Error("no last error after a failed post")
	}
	hub.fail = nil
	e = newEngine(t, dir, hub, b, nil)
	e.CheckTurns(ctx)
	got := hub.texts()
	last := got[len(got)-1]
	if len(got) != 3 || !strings.HasPrefix(last, "o1|xxx") || !strings.HasSuffix(last, "… (full output in Board thread th1)") || len(last)-3 > MaxText {
		t.Fatalf("clipped post: %d posts, %d bytes", len(got), len(last))
	}
	// posts older than the bridge's first start are not replayed by catch-up
	old := hub.add(Post{ID: "old", Author: owner, Text: "@agent: old task", Received: 500})
	e.Handle(ctx, old, true)
	if len(b.calls()) != 2 {
		t.Fatal("old feed post acted on")
	}
}

func TestStateBoundedAndPrivate(t *testing.T) {
	dir := t.TempDir()
	st, _ := LoadState(dir, time.Now())
	for i := 0; i < MaxHandled+10; i++ {
		st.MarkHandled(fmt.Sprint(i))
	}
	if len(st.Handled) != MaxHandled || st.Seen("0") || !st.Seen(fmt.Sprint(MaxHandled+9)) {
		t.Fatalf("handled = %d", len(st.Handled))
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, StateFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v %v", fi.Mode(), err)
	}
	if err := SaveSettings(dir, Defaults()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, SettingsFile)); fi.Mode().Perm() != 0o600 {
		t.Fatal("settings file not 0600")
	}
	s := Defaults()
	s.Owners = []string{"nope"}
	if s.Normalize() == nil {
		t.Error("bad owner accepted")
	}
}
