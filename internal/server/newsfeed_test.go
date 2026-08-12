package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"exe/internal/config"
)

func TestNewsfeedJournalCapsAndMerges(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	for i := 0; i < newsKeep+20; i++ {
		s.PostNews("note", fmt.Sprintf("event %d", i), "")
	}

	file, host := s.newsSelf()
	b, err := os.ReadFile(filepath.Join(s.newsDir(), file))
	if err != nil {
		t.Fatal(err)
	}
	var items []newsItem
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != newsKeep {
		t.Fatalf("journal holds %d items, want the %d cap", len(items), newsKeep)
	}
	last := items[len(items)-1]
	if last.Title != fmt.Sprintf("event %d", newsKeep+19) || last.Host != host || last.Time == 0 {
		t.Fatalf("newest item wrong: %+v", last)
	}

	// a second node's synced-in journal shows up merged, newest first
	other := []newsItem{{ID: "x", Time: time.Now().UnixMilli() + 1000, Host: "other-node", Title: "from other"}}
	ob, _ := json.Marshal(other)
	os.WriteFile(filepath.Join(s.newsDir(), "deadbeef.json"), ob, 0o644)

	rec := httptest.NewRecorder()
	s.handleNewsfeedGet(rec, httptest.NewRequest("GET", "/v1/newsfeed", nil))
	var resp struct {
		Items []newsItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != newsKeep+1 {
		t.Fatalf("merged feed holds %d items, want %d", len(resp.Items), newsKeep+1)
	}
	if resp.Items[0].Title != "from other" || resp.Items[0].Host != "other-node" {
		t.Fatalf("newest-first order broken, got %+v", resp.Items[0])
	}
}

func TestNewsfeedPostAPIRequiresTitle(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "", t.TempDir())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/newsfeed", strings.NewReader(`{"body":"no title"}`))
	s.handleNewsfeedPost(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("titleless post: got %d, want 400", rec.Code)
	}
}

func TestNewsfeedSyncsBetweenNodes(t *testing.T) {
	a, b := newTestNode(t), newTestNode(t)
	pairNodes(t, a, b)

	resp, err := http.Post(a.ts.URL+"/v1/newsfeed", "application/json",
		strings.NewReader(`{"title":"hello mesh","body":"posted on A"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST /v1/newsfeed: %d", resp.StatusCode)
	}

	// B's merged feed must pick the item up, bylined with A's node name
	waitFor(t, "the item to reach B's feed", func() bool {
		r, err := http.Get(b.ts.URL + "/v1/newsfeed")
		if err != nil {
			return false
		}
		defer r.Body.Close()
		var feed struct {
			Items []newsItem `json:"items"`
		}
		if json.NewDecoder(r.Body).Decode(&feed) != nil {
			return false
		}
		for _, it := range feed.Items {
			if it.Title == "hello mesh" && it.Host == a.eng.SelfName() {
				return true
			}
		}
		return false
	})
}
