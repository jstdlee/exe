package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// appEvents fans app-data change notifications out to connected desktops
// over SSE (GET /v1/apps/events). Both a peer sync landing a remote change
// and a node's own API write fire one; the desktop forwards it into the
// matching app's iframe via postMessage so any OTHER open window reloads
// instead of showing stale data. The event carries the writer's client tag
// so the window that made the write ignores its own echo (the same scheme
// the window-layout sync uses), which matters because apps auto-save on
// every keystroke.
type appEvents struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// BroadcastAppData announces a change to app's stored file rel. client is the
// X-Exe-Client tag of the writer (empty for a peer-applied remote change).
func (s *Server) BroadcastAppData(app, rel string, deleted bool, client string) {
	ev, _ := json.Marshal(map[string]any{"app": app, "path": rel, "deleted": deleted, "client": client})
	s.appEv.mu.Lock()
	defer s.appEv.mu.Unlock()
	for ch := range s.appEv.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: apps refetch the whole doc on any event,
			// so a dropped one is caught by the next
		}
	}
}

func (s *Server) handleAppDataEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	ch := make(chan []byte, 16)
	s.appEv.mu.Lock()
	if s.appEv.subs == nil {
		s.appEv.subs = make(map[chan []byte]struct{})
	}
	s.appEv.subs[ch] = struct{}{}
	s.appEv.mu.Unlock()
	defer func() {
		s.appEv.mu.Lock()
		delete(s.appEv.subs, ch)
		s.appEv.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", ev)
			fl.Flush()
		case <-ping.C: // comment line keeps idle proxies from timing the stream out
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
