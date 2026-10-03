package server

// Board (jstdlee Feature A): task threads for the agent CLIs. A thread is
// one Claude Code or Codex session on one target, a VM or the host; each
// message is one headless turn the daemon runs and keeps
// (internal/jx/board). This file is the HTTP glue; jx_board_run.go runs
// turns on targets, jx_board_agents.go answers the agent set-up routes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"exe/internal/jx/board"
	"exe/internal/vmm"
)

// jxBoard is the Board's state for one Server.
type jxBoard struct {
	s       *Server
	eng     *board.Engine
	secrets *board.SecretStore
	guests  *board.Guests

	// remote opens a VM's shell; tests replace it with a fake.
	remote func(info *vmm.Info) board.Remote

	resume sync.Once

	// board-wide event stream subscribers (GET /v1/jx/board/events)
	mu   sync.Mutex
	subs map[chan []byte]bool

	// guestLocks serializes guest setup (package installs, credentials)
	// per VM: two at once collide on apt's or apk's lock.
	guestLocks sync.Map // vm -> *sync.Mutex

	installMu sync.Mutex
	installs  map[string]bool // vm/agent installs in flight
}

// jxBoards holds each Server's Board, so the state needs no field in
// upstream's Server struct.
var jxBoards sync.Map // *Server -> *jxBoard

var jxBoardMu sync.Mutex // one Board opened per Server

// jxBoardOf returns s's Board, opening it on first use; runner == nil runs
// turns on real targets.
func (s *Server) jxBoardOf(runner board.Runner) (*jxBoard, error) {
	if v, ok := jxBoards.Load(s); ok {
		return v.(*jxBoard), nil
	}
	jxBoardMu.Lock()
	defer jxBoardMu.Unlock()
	if v, ok := jxBoards.Load(s); ok {
		return v.(*jxBoard), nil
	}
	dir := filepath.Join(s.jxDir(), "board")
	store, err := board.OpenStore(dir)
	if err != nil {
		return nil, fmt.Errorf("board: %w", err)
	}
	secrets := board.NewSecretStore(filepath.Join(s.jxDir(), "secrets.json"))
	b := &jxBoard{s: s, secrets: secrets, guests: board.NewGuests(secrets),
		subs: map[chan []byte]bool{}, installs: map[string]bool{}}
	b.remote = func(info *vmm.Info) board.Remote { return board.SSHRemote{Target: s.vmTarget(info)} }
	if runner == nil {
		runner = &boardRunner{b: b}
	}
	b.eng = board.NewEngine(store, runner)
	b.eng.OnChange = b.threadChanged
	jxBoards.Store(s, b)
	return b, nil
}

func (s *Server) jxBoard() (*jxBoard, error) { return s.jxBoardOf(nil) }

func init() {
	jxFeatureRegs = append(jxFeatureRegs, registerBoard)
	jxBoardSubmit = func(s *Server, ctx context.Context, req BoardSubmitRequest) (string, string, error) {
		b, err := s.jxBoard()
		if err != nil {
			return "", "", err
		}
		th, turn, _, err := b.submit(ctx, req)
		return th.ID, turn.ID, err
	}
}

func registerBoard(s *Server, mux *http.ServeMux) {
	if b, err := s.jxBoard(); err == nil {
		// turns queued when the daemon stopped start again; one running
		// then ends as interrupted
		b.resume.Do(b.eng.Resume)
	}
	h := func(fn func(b *jxBoard, w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, err := s.jxBoard()
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
			fn(b, w, r)
		}
	}
	mux.HandleFunc("GET /v1/jx/board/threads", h((*jxBoard).handleThreads))
	mux.HandleFunc("POST /v1/jx/board/threads", h((*jxBoard).handleThreadNew))
	mux.HandleFunc("GET /v1/jx/board/threads/{id}", h((*jxBoard).handleThread))
	mux.HandleFunc("PATCH /v1/jx/board/threads/{id}", h((*jxBoard).handleThreadPatch))
	mux.HandleFunc("DELETE /v1/jx/board/threads/{id}", h((*jxBoard).handleThreadDelete))
	mux.HandleFunc("POST /v1/jx/board/threads/{id}/turns", h((*jxBoard).handleTurnNew))
	mux.HandleFunc("POST /v1/jx/board/threads/{id}/stop", h((*jxBoard).handleThreadStop))
	mux.HandleFunc("GET /v1/jx/board/threads/{id}/events", h((*jxBoard).handleThreadEvents))
	mux.HandleFunc("GET /v1/jx/board/events", h((*jxBoard).handleBoardEvents))
	mux.HandleFunc("GET /v1/jx/board/sessions", h((*jxBoard).handleSessions))
	mux.HandleFunc("GET /v1/jx/agents/status", h((*jxBoard).handleAgentsStatus))
	mux.HandleFunc("POST /v1/jx/agents/install", h((*jxBoard).handleAgentsInstall))
	mux.HandleFunc("GET /v1/jx/agents/secrets", h((*jxBoard).handleSecretsGet))
	mux.HandleFunc("PUT /v1/jx/agents/secrets", h((*jxBoard).handleSecretsPut))
}

// boardErr writes an engine error with its status code.
func boardErr(w http.ResponseWriter, err error) {
	var in *board.InputError
	switch {
	case errors.As(err, &in):
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, board.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	default:
		writeErr(w, errCode(err), err)
	}
}

// submit checks a request's target and session, then hands it to the
// engine. A VM target must exist (it need not run: the turn starts it).
func (b *jxBoard) submit(ctx context.Context, req BoardSubmitRequest) (board.Thread, board.Turn, bool, error) {
	if req.ThreadID == "" {
		if req.Session != "" && !board.ValidSessionID(req.Session) {
			return board.Thread{}, board.Turn{}, false, &board.InputError{Msg: "session is not a valid session id"}
		}
		if req.Target != "" && req.Target != board.HostTarget {
			if b.s.VMs == nil {
				return board.Thread{}, board.Turn{}, false, vmm.ErrNoBackend
			}
			if _, err := b.s.VMs.Get(ctx, req.Target); err != nil {
				return board.Thread{}, board.Turn{}, false, err
			}
		}
	}
	return b.eng.Submit(board.SubmitRequest{ThreadID: req.ThreadID, Target: req.Target, Agent: req.Agent,
		Session: req.Session, Fork: req.Fork, Prompt: req.Prompt, Title: req.Title, Origin: req.Origin})
}

func (b *jxBoard) handleThreads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.eng.Store().List())
}

func (b *jxBoard) handleThreadNew(w http.ResponseWriter, r *http.Request) {
	var req BoardSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.ThreadID = "" // a new thread; turns on old ones post to /turns
	if req.Origin == "" {
		req.Origin = "user"
	}
	th, turn, _, err := b.submit(r.Context(), req)
	if err != nil {
		boardErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thread": th, "turn_id": turn.ID})
}

func (b *jxBoard) handleThread(w http.ResponseWriter, r *http.Request) {
	th, turns, err := b.eng.View(r.PathValue("id"))
	if err != nil {
		boardErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": th, "turns": turns})
}

func (b *jxBoard) handleTurnNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
		Origin string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Origin == "" {
		req.Origin = "user"
	}
	_, turn, queued, err := b.submit(r.Context(), BoardSubmitRequest{ThreadID: r.PathValue("id"), Prompt: req.Prompt, Origin: req.Origin})
	if err != nil {
		boardErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"turn_id": turn.ID, "queued": queued})
}

func (b *jxBoard) handleThreadStop(w http.ResponseWriter, r *http.Request) {
	if err := b.eng.Stop(r.PathValue("id")); err != nil {
		boardErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *jxBoard) handleThreadPatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	th, err := b.eng.Patch(r.PathValue("id"), req.Title, req.Archived)
	if err != nil {
		boardErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, th)
}

func (b *jxBoard) handleThreadDelete(w http.ResponseWriter, r *http.Request) {
	if err := b.eng.Delete(r.PathValue("id")); err != nil {
		boardErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sseStart writes an event stream's headers and greeting.
func sseStart(w http.ResponseWriter) (http.Flusher, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	return fl, true
}

// boardPing is how often an idle stream gets a comment line, so proxies
// do not time it out.
var boardPing = 25 * time.Second

// handleThreadEvents streams a thread's events: first those after ?after=
// (or Last-Event-ID) from the store, then live ones. Subscribing before
// reading the store means nothing falls between the two; the seq drops
// what both deliver.
func (b *jxBoard) handleThreadEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > after {
			after = n
		}
	}
	if _, _, err := b.eng.Store().Get(id); err != nil {
		boardErr(w, err)
		return
	}
	ch, cancel := b.eng.Subscribe(id)
	defer cancel()
	past, err := b.eng.Store().Events(id, after)
	if err != nil {
		boardErr(w, err)
		return
	}
	fl, ok := sseStart(w)
	if !ok {
		return
	}
	last := after
	send := func(ev board.Event) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, data)
		last = ev.Seq
	}
	for _, ev := range past {
		send(ev)
	}
	fl.Flush()
	ping := time.NewTicker(boardPing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return // deleted, or fell behind: the client reconnects with after
			}
			if ev.Seq > last {
				send(ev)
				fl.Flush()
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// broadcast sends one message to every board-wide stream; a subscriber
// that cannot keep up misses it (the list refetches on the next one).
func (b *jxBoard) broadcast(v any) {
	data, _ := json.Marshal(v)
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- data:
		default:
		}
	}
}

func (b *jxBoard) threadChanged(t board.Thread, deleted bool) {
	b.broadcast(map[string]any{"type": "thread", "thread_id": t.ID, "state": t.State,
		"updated_at": t.UpdatedAt, "queued": t.Queued, "deleted": deleted})
}

func (b *jxBoard) handleBoardEvents(w http.ResponseWriter, r *http.Request) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}()
	fl, ok := sseStart(w)
	if !ok {
		return
	}
	ping := time.NewTicker(boardPing)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// guestLock locks vm's guest setup and returns the unlock.
func (b *jxBoard) guestLock(vm string) func() {
	m, _ := b.guestLocks.LoadOrStore(vm, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
