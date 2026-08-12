// The Newsfeed system app: one shared timeline of notable events — VMs
// created and deleted, sync conflicts, nodes joining and leaving — visible
// on every desk in the mesh.
//
// It rides the app-data sync tree instead of adding wire routes: each node
// appends only to its own journal, ~/.exe/appdata/Newsfeed/<node-id>.json,
// so no two nodes ever write the same synced file and the peer engine
// carries journals around conflict-free like any other app data.
// GET /v1/newsfeed folds every journal (ours plus synced-in ones) into one
// feed; each item is stamped with the posting node's name and time, which
// the UI shows as the small byline under the message. The app name
// "Newsfeed" is reserved for this built-in — an installed bundle of the
// same name would fight over the data directory.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"exe/internal/vmm"
)

const (
	newsApp  = "Newsfeed"
	newsKeep = 100 // per-node journal cap — the oldest items fall off
	newsPage = 200 // most the merged GET returns
)

type newsItem struct {
	ID    string `json:"id"`
	Time  int64  `json:"time"` // unix ms on the posting node
	Host  string `json:"host"` // that node's name — the UI's byline
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

func (s *Server) newsDir() string { return s.appDataDir(newsApp) }

// newsSelf is this node's journal filename and byline; without a sync
// identity the feed still works, it just stays local.
func (s *Server) newsSelf() (file, host string) {
	if s.Peers != nil {
		return s.Peers.SelfID() + ".json", s.Peers.SelfName()
	}
	host, _ = os.Hostname()
	if host == "" {
		host = "this node"
	}
	return "local.json", host
}

// PostNews appends one event to this node's journal, versions it for sync
// and notifies open desktops. Safe from any goroutine.
func (s *Server) PostNews(kind, title, body string) {
	file, host := s.newsSelf()
	s.newsMu.Lock()
	defer s.newsMu.Unlock()
	s.newsSeq++
	it := newsItem{
		ID:   fmt.Sprintf("%d.%d", time.Now().UnixMilli(), s.newsSeq),
		Time: time.Now().UnixMilli(), Host: host, Kind: kind, Title: title, Body: body,
	}
	ok := false
	s.withFileLock(func() {
		p := filepath.Join(s.newsDir(), file)
		var items []newsItem
		if b, err := os.ReadFile(p); err == nil {
			json.Unmarshal(b, &items)
		}
		items = append(items, it)
		if len(items) > newsKeep {
			items = items[len(items)-newsKeep:]
		}
		b, err := json.Marshal(items)
		if err == nil {
			err = writeNewsFile(p, b)
		}
		if err != nil {
			log.Printf("newsfeed: %v", err)
			return
		}
		ok = true
		if s.Peers != nil {
			s.Peers.LocalWrite(newsApp, file)
		}
	})
	if ok {
		s.BroadcastAppData(newsApp, file, false, "")
	}
}

// writeNewsFile writes atomically (temp + rename) so a crash can't leave a
// half-written journal; the dot-prefixed temp name is invisible to sync.
func writeNewsFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// vmNewsLine describes a VM for feed items.
func vmNewsLine(spec vmm.Spec) string {
	return fmt.Sprintf("%s — %d CPUs, %d MB RAM, %d GB disk", spec.Name, spec.CPUs, spec.MemoryMB, spec.DiskGB)
}

// handleNewsfeedGet merges every node's journal into one feed, newest first.
func (s *Server) handleNewsfeedGet(w http.ResponseWriter, r *http.Request) {
	items := []newsItem{}
	ents, _ := os.ReadDir(s.newsDir())
	for _, en := range ents {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.newsDir(), en.Name()))
		if err != nil {
			continue
		}
		var its []newsItem
		if json.Unmarshal(b, &its) == nil {
			items = append(items, its...)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time > items[j].Time })
	if len(items) > newsPage {
		items = items[:newsPage]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleNewsfeedPost lets the UI and agents put their own word on every
// desk: {"title":"...","body":"...","kind":"note"}.
func (s *Server) handleNewsfeedPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad request body"))
		return
	}
	req.Title = clipText(strings.TrimSpace(req.Title), 200)
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, errors.New("title is required"))
		return
	}
	if req.Kind = clipText(strings.TrimSpace(req.Kind), 16); req.Kind == "" {
		req.Kind = "note"
	}
	s.PostNews(req.Kind, req.Title, clipText(strings.TrimSpace(req.Body), 2000))
	writeJSON(w, http.StatusOK, map[string]string{"status": "posted"})
}

// handleNewsfeedDelete removes one item — wherever it lives — from the feed
// on every node. Journals are one-writer in the append path, but a delete
// edits the origin node's journal in place: the version-vector sync carries
// the smaller file everywhere, and the rare concurrent append to the same
// journal is a plain LWW conflict the engine already resolves (with the
// losing copy backed up). Fine for a notifications feed.
func (s *Server) handleNewsfeedDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var changed []string
	s.newsMu.Lock()
	defer s.newsMu.Unlock()
	s.withFileLock(func() {
		ents, _ := os.ReadDir(s.newsDir())
		for _, en := range ents {
			if en.IsDir() || !strings.HasSuffix(en.Name(), ".json") {
				continue
			}
			p := filepath.Join(s.newsDir(), en.Name())
			b, err := os.ReadFile(p)
			var items []newsItem
			if err != nil || json.Unmarshal(b, &items) != nil {
				continue
			}
			kept := items[:0]
			for _, it := range items {
				if it.ID != id {
					kept = append(kept, it)
				}
			}
			if len(kept) == len(items) {
				continue
			}
			nb, err := json.Marshal(kept)
			if err == nil {
				err = writeNewsFile(p, nb)
			}
			if err != nil {
				log.Printf("newsfeed: %v", err)
				continue
			}
			if s.Peers != nil {
				s.Peers.LocalWrite(newsApp, en.Name())
			}
			changed = append(changed, en.Name())
		}
	})
	if len(changed) == 0 {
		writeErr(w, http.StatusNotFound, errors.New("no such item"))
		return
	}
	for _, f := range changed {
		s.BroadcastAppData(newsApp, f, false, r.Header.Get("X-Exe-Client"))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func clipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
