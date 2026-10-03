package server

// Hub bridge: the user's exe-hub as a task board between the user and an
// agent (internal/jx/hubbridge). An owner's "@agent ..." root post starts
// a Board thread; the owner's replies in that hub thread are its next
// turns; each turn's end comes back as a hub reply signed with the
// agent's own key (jx/hubbridge_ed25519, never the node's peer key).
//
// The bridge follows the hub like the hub agent does (hubagent.go): catch
// up on the recent feed, then read /v1/events, reconnecting with backoff.
// Turn ends come from the Board's own change broadcast (jx_board.go),
// which this file subscribes to in-process, plus a slow poll as a net.

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"exe/internal/jx/board"
	"exe/internal/jx/hubbridge"
	"exe/internal/peer"
)

// Timings tests shorten.
var (
	hubBridgePoll     = 20 * time.Second // how often pending turns are checked without a Board event
	hubBridgeMinDelay = 2 * time.Second  // first reconnect delay
	hubBridgeMaxDelay = time.Minute
)

// hubBridgeDebug turns on the bridge's debug lines (ignored posts).
var hubBridgeDebug = os.Getenv("EXE_HUBBRIDGE_DEBUG") != ""

// hubBridgeRuns counts live bridge loops (tests check none starts while
// disabled).
var hubBridgeRuns atomic.Int32

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/hubbridge", s.handleHubBridgeGet)
		mux.HandleFunc("PUT /v1/jx/hubbridge", s.handleHubBridgePut)
		if s.StateDir == "" || !jxStartLoops {
			return
		}
		hb := s.jxHubBridge()
		set, err := hubbridge.LoadSettings(s.jxDir())
		hb.mu.Lock()
		defer hb.mu.Unlock()
		hb.set = set
		if err != nil {
			hb.lastErr = err.Error()
			log.Printf("hub bridge: %v", err)
			return
		}
		if set.Enabled {
			hb.startLocked()
		}
	})
}

// jxHubBridgeRT is one Server's bridge.
type jxHubBridgeRT struct {
	s *Server

	mu      sync.Mutex
	loaded  bool
	set     hubbridge.Settings
	agentID string
	lastErr string // why the bridge is not running
	eng     *hubbridge.Engine
	st      *hubbridge.State
	cancel  context.CancelFunc
	done    chan struct{}
}

var jxHubBridges sync.Map // *Server -> *jxHubBridgeRT

func (s *Server) jxHubBridge() *jxHubBridgeRT {
	v, _ := jxHubBridges.LoadOrStore(s, &jxHubBridgeRT{s: s})
	hb := v.(*jxHubBridgeRT)
	hb.mu.Lock()
	if !hb.loaded {
		hb.loaded = true
		set, err := hubbridge.LoadSettings(s.jxDir())
		hb.set = set
		if err != nil {
			hb.lastErr = err.Error()
		}
	}
	hb.mu.Unlock()
	return hb
}

// hubBridgeKey loads the agent's key, minting it on first use.
func hubBridgeKey(path string) (*peer.Identity, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if err := board.WriteFileAtomic(path, pemBytes, 0o600); err != nil {
			return nil, err
		}
	}
	return peer.LoadIdentityFile(path)
}

// hubBridgeHubURL is the hub the bridge talks to.
func (hb *jxHubBridgeRT) hubURLLocked() (string, error) {
	raw := hb.set.HubURL
	if raw == "" {
		raw = hb.s.Config().Hub.URL
	}
	if raw == "" {
		return "", errors.New("no hub: set hub_url here or hub.url in the config")
	}
	return hubURL(raw)
}

// startLocked starts the bridge loop; hb.mu is held.
func (hb *jxHubBridgeRT) startLocked() {
	if hb.cancel != nil {
		return
	}
	s := hb.s
	fail := func(err error) {
		hb.lastErr = err.Error()
		log.Printf("hub bridge: %v — not running", err)
	}
	hub, err := hb.hubURLLocked()
	if err != nil {
		fail(err)
		return
	}
	ident, err := hubBridgeKey(filepath.Join(s.jxDir(), hubbridge.KeyFile))
	if err != nil {
		fail(fmt.Errorf("agent key: %w", err))
		return
	}
	hb.agentID = ident.ID
	set := hb.set
	if len(set.Owners) == 0 {
		node, err := s.hubIdentity()
		if err != nil {
			fail(fmt.Errorf("node identity: %w", err))
			return
		}
		set.Owners = []string{node.ID}
	}
	st, err := hubbridge.LoadState(s.jxDir(), time.Now())
	if err != nil {
		fail(err)
		return
	}
	logf := log.Printf
	debugf := func(string, ...any) {}
	if hubBridgeDebug {
		debugf = log.Printf
	}
	hc := &hubBridgeHub{hub: hub, ident: ident}
	eng := hubbridge.New(hubbridge.Config{Settings: set, AgentID: ident.ID, Logf: logf, Debugf: debugf},
		st, hc, &hubBridgeBoard{s: s})
	ctx, cancel := context.WithCancel(context.Background())
	hb.eng, hb.st, hb.cancel, hb.done, hb.lastErr = eng, st, cancel, make(chan struct{}), ""
	log.Printf("hub bridge: agent %s on %s, acting for %d owner(s)", ident.ID, hub, len(set.Owners))
	hubBridgeRuns.Add(1)
	go hb.run(ctx, hb.done, eng, hc, set.AgentName)
}

// stop ends the loop and waits for it.
func (hb *jxHubBridgeRT) stop() {
	hb.mu.Lock()
	cancel, done := hb.cancel, hb.done
	hb.cancel, hb.done, hb.eng = nil, nil, nil
	hb.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (hb *jxHubBridgeRT) run(ctx context.Context, done chan struct{}, eng *hubbridge.Engine, hc *hubBridgeHub, name string) {
	defer close(done)
	defer hubBridgeRuns.Add(-1)

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		hb.watchBoard(ctx, eng)
	}()

	delay := hubBridgeMinDelay
	for ctx.Err() == nil {
		hb.setProfile(eng, hc, name)
		if err := hb.catchUp(ctx, eng, hc); err != nil {
			log.Printf("hub bridge: catch-up: %v", err)
		}
		eng.CheckTurns(ctx)
		start := time.Now()
		err := hb.follow(ctx, eng, hc)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			delay = hubBridgeMinDelay
		}
		log.Printf("hub bridge: %v — reconnecting in %s", err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > hubBridgeMaxDelay {
			delay = hubBridgeMaxDelay
		}
	}
}

// watchBoard checks pending turns whenever a Board thread changes, and on
// a slow tick in case a change was dropped.
func (hb *jxHubBridgeRT) watchBoard(ctx context.Context, eng *hubbridge.Engine) {
	ch := make(chan []byte, 64)
	if b, err := hb.s.jxBoard(); err == nil {
		b.mu.Lock()
		b.subs[ch] = true
		b.mu.Unlock()
		defer func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
		}()
	}
	tick := time.NewTicker(hubBridgePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var ev struct {
				ThreadID string `json:"thread_id"`
				State    string `json:"state"`
			}
			if json.Unmarshal(msg, &ev) == nil && ev.State != board.ThreadRunning && eng.Owns(ev.ThreadID) {
				eng.CheckTurns(ctx)
			}
		case <-tick.C:
			eng.CheckTurns(ctx)
		}
	}
}

// setProfile names the agent on the hub, once per name and hub.
func (hb *jxHubBridgeRT) setProfile(eng *hubbridge.Engine, hc *hubBridgeHub, name string) {
	if eng.ProfileIs(name, hc.hub) {
		return
	}
	body, _ := json.Marshal(map[string]string{"name": name})
	if _, err := hc.send("profile.set", body); err != nil {
		log.Printf("hub bridge: setting the agent's name: %v", err)
		return
	}
	eng.SetProfile(name, hc.hub)
}

// catchUp handles the recent feed, replies included, oldest first.
func (hb *jxHubBridgeRT) catchUp(ctx context.Context, eng *hubbridge.Engine, hc *hubBridgeHub) error {
	var feed struct {
		Posts []hubPost `json:"posts"`
	}
	if err := hubGetJSON(ctx, hc.hub+"/v1/feed?replies=1&limit=100", &feed); err != nil {
		return err
	}
	for i := len(feed.Posts) - 1; i >= 0 && ctx.Err() == nil; i-- {
		if p := feed.Posts[i]; eng.IsOwner(p.Author) {
			eng.Handle(ctx, hubBridgePost(p), true)
		}
	}
	return nil
}

// follow reads the hub's event stream until it breaks; an owner's new
// post is fetched and handled, in the order the hub announced them.
func (hb *jxHubBridgeRT) follow(ctx context.Context, eng *hubbridge.Engine, hc *hubBridgeHub) error {
	req, err := http.NewRequestWithContext(ctx, "GET", hc.hub+"/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := hubStreamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("events: status %d", resp.StatusCode)
	}
	stall := time.AfterFunc(hubAgentStall, func() { resp.Body.Close() })
	defer stall.Stop()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		stall.Reset(hubAgentStall)
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev hubEvent
		if json.Unmarshal([]byte(line[6:]), &ev) != nil || ev.Type != "post.create" {
			continue
		}
		if !eng.IsOwner(ev.Author) {
			if hubBridgeDebug {
				log.Printf("hub bridge: ignoring %s by %s (not an owner)", hubShort(ev.ID), ev.Author)
			}
			continue
		}
		p, err := hc.Post(ctx, ev.ID)
		if err != nil {
			log.Printf("hub bridge: post %s: %v", hubShort(ev.ID), err)
			continue // the next catch-up finds it
		}
		eng.Handle(ctx, p, false)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("events stream ended")
}

func hubBridgePost(p hubPost) hubbridge.Post {
	return hubbridge.Post{ID: p.ID, Author: p.Author, Text: p.Text, ReplyTo: p.ReplyTo, TS: p.TS, Received: p.Received}
}

// hubBridgeHub is the hub, written to under the agent's key.
type hubBridgeHub struct {
	hub   string
	ident *peer.Identity
}

func (h *hubBridgeHub) Post(ctx context.Context, id string) (hubbridge.Post, error) {
	var th struct {
		Post hubPost `json:"post"`
	}
	if err := hubGetJSON(ctx, h.hub+"/v1/post/"+id+"?limit=1", &th); err != nil {
		return hubbridge.Post{}, err
	}
	return hubBridgePost(th.Post), nil
}

func (h *hubBridgeHub) Reply(ctx context.Context, replyTo, text string) (string, error) {
	body, _ := json.Marshal(map[string]string{"text": text, "reply_to": replyTo})
	return h.send("post.create", body)
}

// send signs one envelope with the agent's key (hubSend) and returns the
// message id the hub answered.
func (h *hubBridgeHub) send(typ string, body json.RawMessage) (string, error) {
	resp, err := hubSend(h.hub, h.ident, typ, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var res struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	json.Unmarshal(raw, &res)
	if resp.StatusCode != 200 {
		if res.Error == "" {
			res.Error = strings.TrimSpace(string(raw))
		}
		return "", fmt.Errorf("hub refused %s (%d): %s", typ, resp.StatusCode, res.Error)
	}
	return res.ID, nil
}

// hubBridgeBoard is the Board.
type hubBridgeBoard struct{ s *Server }

func (b *hubBridgeBoard) Submit(ctx context.Context, req hubbridge.SubmitRequest) (string, string, error) {
	if jxBoardSubmit == nil {
		return "", "", errors.New("board not available")
	}
	return jxBoardSubmit(b.s, ctx, BoardSubmitRequest{ThreadID: req.ThreadID, Target: req.Target,
		Agent: req.Agent, Prompt: req.Prompt, Title: req.Title, Origin: req.Origin})
}

func (b *hubBridgeBoard) Stop(threadID string) error {
	bd, err := b.s.jxBoard()
	if err != nil {
		return err
	}
	return bd.eng.Stop(threadID)
}

func (b *hubBridgeBoard) Thread(threadID string) (hubbridge.ThreadStatus, error) {
	bd, err := b.s.jxBoard()
	if err != nil {
		return hubbridge.ThreadStatus{}, err
	}
	th, _, err := bd.eng.Store().Get(threadID)
	if errors.Is(err, board.ErrNotFound) {
		return hubbridge.ThreadStatus{}, hubbridge.ErrBoardGone
	}
	if err != nil {
		return hubbridge.ThreadStatus{}, err
	}
	return hubbridge.ThreadStatus{State: th.State, Queued: th.Queued, LastText: th.LastText}, nil
}

func (b *hubBridgeBoard) Turn(threadID, turnID string) (hubbridge.TurnResult, error) {
	bd, err := b.s.jxBoard()
	if err != nil {
		return hubbridge.TurnResult{}, err
	}
	_, turns, err := bd.eng.View(threadID)
	if errors.Is(err, board.ErrNotFound) {
		return hubbridge.TurnResult{}, hubbridge.ErrBoardGone
	}
	if err != nil {
		return hubbridge.TurnResult{}, err
	}
	for _, tu := range turns {
		if tu.ID != turnID {
			continue
		}
		switch tu.State {
		case board.TurnDone, board.TurnError, board.TurnStopped:
			return hubbridge.TurnResult{Ended: true, State: tu.State, Error: tu.Error,
				Text: hubbridge.FinalText(tu.Events)}, nil
		}
		return hubbridge.TurnResult{State: tu.State}, nil
	}
	return hubbridge.TurnResult{}, hubbridge.ErrBoardGone
}

// hubBridgeView is GET /v1/jx/hubbridge.
type hubBridgeView struct {
	hubbridge.Settings
	AgentID   string                `json:"agent_id"`
	Running   bool                  `json:"running"`
	LastError string                `json:"last_error"`
	Threads   []hubbridge.ThreadRef `json:"threads"`
}

func (hb *jxHubBridgeRT) view() hubBridgeView {
	hb.mu.Lock()
	v := hubBridgeView{Settings: hb.set, AgentID: hb.agentID, Running: hb.cancel != nil, LastError: hb.lastErr}
	eng, st := hb.eng, hb.st
	hb.mu.Unlock()
	if v.AgentID == "" {
		if id, err := peer.LoadIdentityFile(filepath.Join(hb.s.jxDir(), hubbridge.KeyFile)); err == nil {
			v.AgentID = id.ID
		}
	}
	if eng != nil {
		if e := eng.LastError(); e != "" {
			v.LastError = e
		}
		v.Threads = eng.Threads()
	} else if st == nil {
		if st, err := hubbridge.LoadState(hb.s.jxDir(), time.Now()); err == nil {
			v.Threads = st.ThreadList()
		}
	} else {
		v.Threads = st.ThreadList()
	}
	if v.Owners == nil {
		v.Owners = []string{}
		if node, err := hb.s.hubIdentity(); err == nil {
			v.Owners = []string{node.ID}
		}
	}
	if v.Threads == nil {
		v.Threads = []hubbridge.ThreadRef{}
	}
	return v
}

func (s *Server) handleHubBridgeGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.jxHubBridge().view())
}

func (s *Server) handleHubBridgePut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled       *bool     `json:"enabled"`
		HubURL        *string   `json:"hub_url"`
		Owners        *[]string `json:"owners"`
		DefaultTarget *string   `json:"default_target"`
		DefaultAgent  *string   `json:"default_agent"`
		AgentName     *string   `json:"agent_name"`
		Ack           *bool     `json:"ack"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hb := s.jxHubBridge()
	hb.mu.Lock()
	set := hb.set
	hb.mu.Unlock()
	if in.Enabled != nil {
		set.Enabled = *in.Enabled
	}
	if in.HubURL != nil {
		set.HubURL = *in.HubURL
	}
	if in.Owners != nil {
		set.Owners = *in.Owners
	}
	if in.DefaultTarget != nil {
		set.DefaultTarget = *in.DefaultTarget
	}
	if in.DefaultAgent != nil {
		set.DefaultAgent = *in.DefaultAgent
	}
	if in.AgentName != nil {
		set.AgentName = *in.AgentName
	}
	if in.Ack != nil {
		set.Ack = *in.Ack
	}
	if err := set.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if set.HubURL != "" {
		if _, err := hubURL(set.HubURL); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("hub_url: %w", err))
			return
		}
	}
	if err := hubbridge.SaveSettings(s.jxDir(), set); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// any change restarts the loop with the new settings
	hb.stop()
	hb.mu.Lock()
	hb.set, hb.lastErr = set, ""
	if set.Enabled {
		hb.startLocked()
	}
	hb.mu.Unlock()
	writeJSON(w, http.StatusOK, hb.view())
}
