package hubbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"exe/internal/jx/board"
)

// Post is a hub post as the bridge reads it.
type Post struct {
	ID       string
	Author   string // profile id
	Text     string
	ReplyTo  string
	TS       int64 // the author's claim, ms
	Received int64 // the hub's clock, ms (0 if the hub did not say)
}

func (p Post) at() int64 {
	if p.Received > 0 {
		return p.Received
	}
	return p.TS
}

// Hub is the hub as the bridge uses it. Reply posts under the agent's
// own key and returns the new post's id.
type Hub interface {
	Post(ctx context.Context, id string) (Post, error)
	Reply(ctx context.Context, replyTo, text string) (string, error)
}

// SubmitRequest starts a Board thread (ThreadID == "") or adds a turn.
type SubmitRequest struct {
	ThreadID, Target, Agent, Prompt, Title, Origin string
}

// TurnResult is a Board turn as the bridge needs it.
type TurnResult struct {
	Ended bool
	State string // board.Turn* state
	Error string
	Text  string // FinalText of its events
}

// ThreadStatus is a Board thread as /status shows it.
type ThreadStatus struct {
	State    string
	Queued   int
	LastText string
}

// Board is the Board as the bridge uses it.
type Board interface {
	Submit(ctx context.Context, req SubmitRequest) (threadID, turnID string, err error)
	Stop(threadID string) error
	Thread(threadID string) (ThreadStatus, error)
	Turn(threadID, turnID string) (TurnResult, error)
}

// ErrBoardGone marks a thread or turn the Board no longer has.
var ErrBoardGone = errors.New("board thread is gone")

// Config is one running bridge's setup.
type Config struct {
	Settings Settings
	AgentID  string // the agent's own profile id: never acted on
	Debugf   func(format string, args ...any)
	Logf     func(format string, args ...any)
}

// Engine decides what each hub post means and what each turn end posts.
// Its methods are safe for concurrent use; they run one at a time.
type Engine struct {
	cfg    Config
	owners map[string]bool
	st     *State
	hub    Hub
	board  Board

	mu      sync.Mutex
	parents map[string]string // post id -> its reply_to, as read so far
	lastErr string
}

// New returns an engine over st.
func New(cfg Config, st *State, hub Hub, b Board) *Engine {
	if cfg.Debugf == nil {
		cfg.Debugf = func(string, ...any) {}
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	e := &Engine{cfg: cfg, owners: map[string]bool{}, st: st, hub: hub, board: b, parents: map[string]string{}}
	for _, o := range cfg.Settings.Owners {
		if o != cfg.AgentID { // the agent never acts on itself: no loops
			e.owners[o] = true
		}
	}
	return e
}

// IsOwner reports whether the bridge acts on posts by profile id.
func (e *Engine) IsOwner(id string) bool { return e.owners[id] }

// LastError is the last failure, "" after a success.
func (e *Engine) LastError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastErr
}

// Owns reports whether threadID is a Board thread the bridge started.
func (e *Engine) Owns(threadID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.st.Threads {
		if t.ThreadID == threadID {
			return true
		}
	}
	return false
}

func (e *Engine) fail(format string, args ...any) {
	e.lastErr = fmt.Sprintf(format, args...)
	e.cfg.Logf("hub bridge: %s", e.lastErr)
}

func (e *Engine) save() {
	if err := e.st.Save(); err != nil {
		e.fail("saving state: %v", err)
	}
}

// Handle acts on one hub post: a new task, a turn, a command, or nothing.
// catchUp marks a post read from the feed rather than the live stream:
// posts from before the bridge first ran are not acted on then.
func (e *Engine) Handle(ctx context.Context, p Post, catchUp bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p.ID == "" || e.st.Seen(p.ID) {
		return
	}
	if p.ReplyTo != "" {
		e.parents[p.ID] = p.ReplyTo
	}
	if !e.owners[p.Author] {
		e.cfg.Debugf("hub bridge: ignoring %s by %s (not an owner)", short(p.ID), p.Author)
		return
	}
	if catchUp && p.at() > 0 && p.at() < e.st.Since {
		e.cfg.Debugf("hub bridge: ignoring %s (before the bridge started)", short(p.ID))
		return
	}
	if p.ReplyTo == "" {
		e.handleRoot(ctx, p)
		return
	}
	e.handleReply(ctx, p)
}

func (e *Engine) handleRoot(ctx context.Context, p Post) {
	set := e.cfg.Settings
	task, ok := ParseTask(p.Text, set.AgentName, set.DefaultAgent, set.DefaultTarget)
	e.st.MarkHandled(p.ID)
	if !ok {
		e.save()
		return // a note, not a task
	}
	// remembered before submitting: a crash in between loses a task
	// rather than running it twice
	e.save()
	threadID, turnID, err := e.board.Submit(ctx, SubmitRequest{Target: task.Target, Agent: task.Agent,
		Prompt: task.Prompt, Title: oneLine(task.Prompt, 60), Origin: "hub:" + p.ID})
	if err != nil {
		e.fail("task %s: %v", short(p.ID), err)
		e.reply(ctx, p.ID, "⚠ "+err.Error())
		return
	}
	e.st.Threads[p.ID] = ThreadRef{Root: p.ID, ThreadID: threadID, Target: task.Target, Agent: task.Agent,
		CreatedAt: time.Now().UTC()}
	e.st.Pending[turnID] = Pending{ThreadID: threadID, Root: p.ID, ReplyTo: p.ID}
	e.save()
	e.cfg.Logf("hub bridge: task %s -> Board thread %s (%s in %s)", short(p.ID), threadID, task.Agent, task.Target)
	if set.Ack {
		e.reply(ctx, p.ID, fmt.Sprintf("on it — %s in %s", task.Agent, task.Target))
	}
}

func (e *Engine) handleReply(ctx context.Context, p Post) {
	root, err := e.rootOf(ctx, p)
	if err != nil {
		e.fail("reply %s: finding its thread: %v", short(p.ID), err)
		return // not marked: the next catch-up tries again
	}
	ref, ok := e.st.Threads[root]
	e.st.MarkHandled(p.ID)
	if !ok {
		e.save()
		e.cfg.Debugf("hub bridge: ignoring reply %s (thread %s is not the bridge's)", short(p.ID), short(root))
		return
	}
	e.save()
	switch ParseCommand(p.Text) {
	case CmdStop:
		err := e.board.Stop(ref.ThreadID)
		msg := "stopped"
		if err != nil {
			msg = "⚠ " + err.Error()
		}
		e.reply(ctx, p.ID, msg)
		return
	case CmdStatus:
		e.reply(ctx, p.ID, e.status(ref))
		return
	}
	_, turnID, err := e.board.Submit(ctx, SubmitRequest{ThreadID: ref.ThreadID, Prompt: strings.TrimSpace(p.Text),
		Origin: "hub:" + p.ID})
	if err != nil {
		e.fail("turn %s: %v", short(p.ID), err)
		e.reply(ctx, p.ID, "⚠ "+err.Error())
		return
	}
	e.st.Pending[turnID] = Pending{ThreadID: ref.ThreadID, Root: root, ReplyTo: p.ID}
	e.save()
	e.cfg.Logf("hub bridge: reply %s -> turn %s on Board thread %s", short(p.ID), turnID, ref.ThreadID)
}

func (e *Engine) status(ref ThreadRef) string {
	th, err := e.board.Thread(ref.ThreadID)
	if err != nil {
		return "⚠ " + err.Error()
	}
	s := fmt.Sprintf("%s in %s: %s", ref.Agent, ref.Target, th.State)
	if th.Queued > 0 {
		s += fmt.Sprintf(", %d queued", th.Queued)
	}
	s += " (Board thread " + ref.ThreadID + ")"
	if th.LastText != "" {
		s += "\nlast: " + th.LastText
	}
	return s
}

// rootOf walks reply_to up to the thread's root post.
func (e *Engine) rootOf(ctx context.Context, p Post) (string, error) {
	id, parent := p.ID, p.ReplyTo
	for n := 0; parent != ""; n++ {
		if n >= 64 {
			return "", errors.New("thread too deep")
		}
		if _, ok := e.st.Threads[parent]; ok {
			return parent, nil
		}
		id = parent
		next, ok := e.parents[id]
		if !ok {
			q, err := e.hub.Post(ctx, id)
			if err != nil {
				return "", err
			}
			next = q.ReplyTo
			e.parents[id] = next
		}
		parent = next
	}
	return id, nil
}

// CheckTurns posts the end of every pending turn that has ended. A post
// the hub did not take stays pending and is tried again next time.
func (e *Engine) CheckTurns(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for turnID, pd := range e.st.Pending {
		if ctx.Err() != nil {
			return
		}
		res, err := e.board.Turn(pd.ThreadID, turnID)
		if errors.Is(err, ErrBoardGone) {
			delete(e.st.Pending, turnID)
			e.save()
			continue
		}
		if err != nil {
			e.fail("turn %s: %v", turnID, err)
			continue
		}
		if !res.Ended {
			continue
		}
		var text string
		switch res.State {
		case board.TurnStopped:
			// "/stop" answered already; a stop from the Board app says nothing
		case board.TurnError:
			msg := res.Error
			if msg == "" {
				msg = "turn failed"
			}
			text = Clip("⚠ "+msg, pd.ThreadID)
		default:
			text = res.Text
			if text == "" {
				text = "done (no text output)"
			}
			text = Clip(text, pd.ThreadID)
		}
		if text != "" {
			if _, err := e.hub.Reply(ctx, pd.ReplyTo, text); err != nil {
				e.fail("posting turn %s: %v", turnID, err)
				continue
			}
			e.lastErr = ""
		}
		delete(e.st.Pending, turnID)
		e.save()
	}
}

// reply posts under replyTo, noting a failure.
func (e *Engine) reply(ctx context.Context, replyTo, text string) {
	id, err := e.hub.Reply(ctx, replyTo, Clip(text, ""))
	if err != nil {
		e.fail("reply under %s: %v", short(replyTo), err)
		return
	}
	e.lastErr = ""
	e.parents[id] = replyTo
}

// Threads is the owned threads, newest first.
func (e *Engine) Threads() []ThreadRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.ThreadList()
}

// ProfileIs reports whether the agent's hub profile already carries name
// on hub.
func (e *Engine) ProfileIs(name, hub string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.ProfileName == name && e.st.ProfileHub == hub
}

// SetProfile records that the agent's profile on hub is name.
func (e *Engine) SetProfile(name, hub string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.st.ProfileName, e.st.ProfileHub = name, hub
	e.save()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
