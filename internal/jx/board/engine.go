package board

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// InputError is a request the engine refuses as malformed (HTTP 400).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErr(msg string) error { return &InputError{Msg: msg} }

// SubmitRequest posts a prompt as a new thread (ThreadID == "") or as a new
// turn on an existing one. It mirrors server.BoardSubmitRequest.
type SubmitRequest struct {
	ThreadID string
	Target   string
	Agent    string
	Session  string // continue this CLI session (new threads only)
	Fork     bool   // fork Session instead of continuing it (Claude only)
	Prompt   string
	Title    string
	Origin   string
}

// Engine runs threads' turns: one at a time per thread, the rest queued in
// order, each turn's output parsed into events that are stored and fanned
// out to subscribers.
type Engine struct {
	store  *Store
	runner Runner
	now    func() time.Time

	// OnChange, when set, hears every thread change (state, title, a new
	// turn) and deletion; it is called without the engine's locks held.
	OnChange func(t Thread, deleted bool)

	mu   sync.Mutex
	runs map[string]*threadRun          // thread id -> its turn loop, while one runs
	subs map[string]map[chan Event]bool // thread id -> live event subscribers
}

type threadRun struct {
	turnID  string
	cancel  context.CancelFunc
	stopped bool // Stop was asked for the current turn
	done    chan struct{}
}

// NewEngine returns an engine over store, running turns with runner.
func NewEngine(store *Store, runner Runner) *Engine {
	return &Engine{store: store, runner: runner, now: time.Now,
		runs: map[string]*threadRun{}, subs: map[string]map[chan Event]bool{}}
}

// Store returns the engine's store.
func (e *Engine) Store() *Store { return e.store }

// SetRunner replaces the runner for turns started from now on (tests).
func (e *Engine) SetRunner(r Runner) { e.mu.Lock(); e.runner = r; e.mu.Unlock() }

// Resume settles what a daemon restart left behind: a turn that was
// running has lost its process and ends as an error; queued turns start.
func (e *Engine) Resume() {
	for _, t := range e.store.List() {
		_, turns, err := e.store.Get(t.ID)
		if err != nil {
			continue
		}
		queued := false
		for _, tu := range turns {
			switch tu.State {
			case TurnRunning:
				e.endTurn(t.ID, tu.ID, TurnError, "interrupted: the daemon stopped during this turn", nil, "")
			case TurnQueued:
				queued = true
			}
		}
		if queued {
			e.mu.Lock()
			e.kickLocked(t.ID)
			e.mu.Unlock()
		}
	}
}

// Submit validates req, stores the turn and starts it, or queues it behind
// the thread's running turn (queued = true).
func (e *Engine) Submit(req SubmitRequest) (th Thread, turn Turn, queued bool, err error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return Thread{}, Turn{}, false, inputErr("prompt is required")
	}
	origin := req.Origin
	if origin == "" {
		origin = "user"
	}
	now := e.now().UTC()
	if req.ThreadID == "" {
		if req.Target == "" {
			return Thread{}, Turn{}, false, inputErr("target is required (a VM name, or host)")
		}
		if !ValidAgent(req.Agent) {
			return Thread{}, Turn{}, false, inputErr(`agent must be "claude" or "codex"`)
		}
		if req.Fork && req.Session == "" {
			return Thread{}, Turn{}, false, inputErr("fork needs a session to fork")
		}
		if req.Fork && req.Agent == Codex {
			return Thread{}, Turn{}, false, inputErr("codex cannot fork a thread headlessly; continue it, or start a new one")
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = oneLine(prompt, 60)
		}
		th = Thread{ID: NewID(), Title: title, Target: req.Target, Agent: req.Agent,
			SessionID: req.Session, Fork: req.Fork, State: ThreadIdle, Origin: origin,
			CreatedAt: now, UpdatedAt: now}
		if err := e.store.Create(th); err != nil {
			return Thread{}, Turn{}, false, err
		}
		req.ThreadID = th.ID
	}
	turn = Turn{ID: NewID(), Prompt: prompt, Origin: origin, State: TurnQueued, CreatedAt: now}
	e.mu.Lock()
	th, err = e.store.Update(req.ThreadID, func(t *Thread, turns []Turn) ([]Turn, error) {
		turns = append(turns, turn)
		t.Archived = false // a new message brings an archived thread back
		t.UpdatedAt = now
		settle(t, turns)
		return turns, nil
	})
	if err == nil {
		queued = e.kickLocked(req.ThreadID)
	}
	e.mu.Unlock()
	if err != nil {
		return Thread{}, Turn{}, false, err
	}
	e.changed(th, false)
	return th, turn, queued, nil
}

// kickLocked starts the thread's turn loop unless one runs; it reports
// whether one did (the new turn waits behind it). Callers hold e.mu, which
// also orders it against the loop's own exit check: a turn stored before
// the kick is always seen by a loop.
func (e *Engine) kickLocked(id string) (running bool) {
	if _, ok := e.runs[id]; ok {
		return true
	}
	r := &threadRun{done: make(chan struct{})}
	e.runs[id] = r
	go e.loop(id, r)
	return false
}

func (e *Engine) loop(id string, r *threadRun) {
	defer close(r.done)
	for {
		e.mu.Lock()
		turn, ok := e.nextQueued(id)
		if !ok {
			delete(e.runs, id)
			e.mu.Unlock()
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		r.turnID, r.cancel, r.stopped = turn.ID, cancel, false
		runner := e.runner
		e.mu.Unlock()
		e.runTurn(ctx, id, turn, r, runner)
		cancel()
	}
}

func (e *Engine) nextQueued(id string) (Turn, bool) {
	_, turns, err := e.store.Get(id)
	if err != nil {
		return Turn{}, false
	}
	for _, t := range turns {
		if t.State == TurnQueued {
			return t, true
		}
	}
	return Turn{}, false
}

func (e *Engine) runTurn(ctx context.Context, id string, turn Turn, r *threadRun, runner Runner) {
	now := e.now().UTC()
	th, _, err := e.store.UpdateTurn(id, turn.ID, func(t *Thread, tu *Turn) {
		tu.State, tu.StartedAt = TurnRunning, &now
		t.UpdatedAt = now
	})
	if err != nil {
		return // deleted under us
	}
	e.settleThread(id)
	e.emit(id, Event{TurnID: turn.ID, Type: EvTurnStart, Prompt: turn.Prompt})

	spec := Spec{ThreadID: id, TurnID: turn.ID, Target: th.Target, Agent: th.Agent,
		SessionID: th.SessionID, Fork: th.Fork, Prompt: turn.Prompt}
	parser := NewParser(th.Agent)
	session := th.SessionID
	status := func(text string) {
		e.emit(id, Event{TurnID: turn.ID, Type: EvStatus, Text: text})
	}
	line := func(b []byte) {
		for _, ev := range parser.Feed(b) {
			ev.TurnID = turn.ID
			e.emit(id, ev)
		}
		// keep the session as soon as the CLI names it, so a turn stopped
		// half way is still continued, not restarted, by the next one
		if sid := parser.Result().SessionID; sid != "" && sid != session {
			session = sid
			e.store.Update(id, func(t *Thread, turns []Turn) ([]Turn, error) {
				t.SessionID, t.Fork = sid, false
				return turns, nil
			})
		}
	}
	runErr := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = errors.New("runner crashed")
			}
		}()
		if runner == nil {
			return errors.New("no runner")
		}
		return runner.Run(ctx, spec, status, line)
	}()

	res := parser.Result()
	e.mu.Lock()
	stopped := r.stopped
	e.mu.Unlock()
	state, msg := TurnDone, ""
	switch {
	case stopped:
		state = TurnStopped
	case res.Error != "":
		state, msg = TurnError, res.Error
	case runErr != nil:
		state, msg = TurnError, runErr.Error()
	}
	e.endTurn(id, turn.ID, state, msg, res.Usage, res.LastText)
}

// endTurn records a turn's end and emits its turn_end event.
func (e *Engine) endTurn(id, turnID, state, msg string, usage *Usage, lastText string) {
	now := e.now().UTC()
	_, _, err := e.store.UpdateTurn(id, turnID, func(t *Thread, tu *Turn) {
		tu.State, tu.EndedAt, tu.Error, tu.Usage = state, &now, msg, usage
		t.UpdatedAt = now
		if lastText != "" {
			t.LastText = oneLine(strings.Join(strings.Fields(lastText), " "), 200)
		}
	})
	if err != nil {
		return
	}
	e.emit(id, Event{TurnID: turnID, Type: EvTurnEnd, State: state, Error: msg, Usage: usage})
	e.settleThread(id)
}

// settle derives a thread's state and queue count from its turns.
func settle(t *Thread, turns []Turn) {
	t.Queued = 0
	running := false
	last := ""
	for _, tu := range turns {
		switch tu.State {
		case TurnRunning:
			running = true
		case TurnQueued:
			t.Queued++
		default:
			last = tu.State
		}
	}
	switch {
	case running:
		t.State = ThreadRunning
	case t.Queued > 0:
		t.State = ThreadQueued
	case last == TurnError:
		t.State = ThreadError
	default:
		t.State = ThreadIdle
	}
}

func (e *Engine) settleThread(id string) {
	th, err := e.store.Update(id, func(t *Thread, turns []Turn) ([]Turn, error) {
		settle(t, turns)
		return turns, nil
	})
	if err == nil {
		e.changed(th, false)
	}
}

func (e *Engine) changed(t Thread, deleted bool) {
	if e.OnChange != nil {
		e.OnChange(t, deleted)
	}
}

// Stop ends the thread's running turn (its process group is killed) and
// drops the turns queued behind it: stop means stop, not "next".
func (e *Engine) Stop(id string) error {
	if _, _, err := e.store.Get(id); err != nil {
		return err
	}
	e.mu.Lock()
	var dropped []string
	_, err := e.store.Update(id, func(t *Thread, turns []Turn) ([]Turn, error) {
		now := e.now().UTC()
		for i := range turns {
			if turns[i].State == TurnQueued {
				turns[i].State, turns[i].EndedAt = TurnStopped, &now
				dropped = append(dropped, turns[i].ID)
			}
		}
		return turns, nil
	})
	var done chan struct{}
	if r := e.runs[id]; r != nil {
		r.stopped = true
		if r.cancel != nil {
			r.cancel()
		}
		done = r.done
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, tid := range dropped {
		e.emit(id, Event{TurnID: tid, Type: EvTurnEnd, State: TurnStopped})
	}
	e.settleThread(id)
	if done != nil {
		// The runner kills the process group and returns; wait a little so
		// the caller sees the thread settled, but never hang on a target
		// that does not answer.
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

// Patch renames or (un)archives a thread.
func (e *Engine) Patch(id string, title *string, archived *bool) (Thread, error) {
	th, err := e.store.Update(id, func(t *Thread, turns []Turn) ([]Turn, error) {
		if title != nil {
			s := strings.TrimSpace(*title)
			if s == "" {
				return nil, inputErr("title cannot be empty")
			}
			t.Title = oneLine(s, 200)
		}
		if archived != nil {
			t.Archived = *archived
		}
		t.UpdatedAt = e.now().UTC()
		return turns, nil
	})
	if err == nil {
		e.changed(th, false)
	}
	return th, err
}

// Delete stops a thread and removes it.
func (e *Engine) Delete(id string) error {
	th, _, err := e.store.Get(id)
	if err != nil {
		return err
	}
	if err := e.Stop(id); err != nil {
		return err
	}
	if err := e.store.Delete(id); err != nil {
		return err
	}
	e.mu.Lock()
	for ch := range e.subs[id] {
		close(ch)
	}
	delete(e.subs, id)
	e.mu.Unlock()
	e.changed(th, true)
	return nil
}

// View returns a thread with its turns and their events.
func (e *Engine) View(id string) (Thread, []TurnView, error) {
	th, turns, err := e.store.Get(id)
	if err != nil {
		return Thread{}, nil, err
	}
	evs, err := e.store.Events(id, 0)
	if err != nil {
		return Thread{}, nil, err
	}
	byTurn := map[string][]Event{}
	for _, ev := range evs {
		byTurn[ev.TurnID] = append(byTurn[ev.TurnID], ev)
	}
	out := make([]TurnView, 0, len(turns))
	for _, tu := range turns {
		ev := byTurn[tu.ID]
		if ev == nil {
			ev = []Event{}
		}
		out = append(out, TurnView{Turn: tu, Events: ev})
	}
	return th, out, nil
}

// Running reports whether a turn loop is live for the thread.
func (e *Engine) Running(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.runs[id]
	return ok
}

// emit stores ev and hands it to the thread's subscribers. A subscriber
// too slow to keep up is dropped (its channel closed): it reconnects with
// ?after= and reads what it missed from the store, so nothing is lost.
func (e *Engine) emit(id string, ev Event) {
	ev, err := e.store.Append(id, ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subs[id] {
		select {
		case ch <- ev:
		default:
			delete(e.subs[id], ch)
			close(ch)
		}
	}
}

// Subscribe returns a channel of the thread's events from now on, and its
// cancel. The channel is closed when the thread is deleted or the
// subscriber falls behind.
func (e *Engine) Subscribe(id string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	e.mu.Lock()
	if e.subs[id] == nil {
		e.subs[id] = map[chan Event]bool{}
	}
	e.subs[id][ch] = true
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		if e.subs[id][ch] {
			delete(e.subs[id], ch)
			close(ch)
		}
		e.mu.Unlock()
	}
}
