package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"exe/internal/config"
	"exe/internal/peer"
)

// testNode is one simulated exe daemon: a Server + sync engine behind a
// real HTTP listener, with its own state dir.
type testNode struct {
	srv *Server
	eng *peer.Engine
	ts  *httptest.Server
	dir string
}

func (n *testNode) addr(t *testing.T) string {
	u, err := url.Parse(n.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func (n *testNode) dataFile(app, rel string) string {
	return filepath.Join(n.dir, "appdata", app, filepath.FromSlash(rel))
}

func newTestNode(t *testing.T) *testNode {
	t.Helper()
	dir := t.TempDir()
	srv := New(&config.Config{}, nil, nil, "", dir)
	ident, err := peer.LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	port := new(string)
	eng, err := peer.NewEngine(peer.EngineConfig{
		StateDir: dir,
		DataDir:  filepath.Join(dir, "appdata"),
		Self:     ident,
		PortFn:   func() string { return *port },
		OnApply:  srv.BroadcastAppData,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Peers = eng
	ts := httptest.NewServer(srv.Handler())
	u, _ := url.Parse(ts.URL)
	*port = u.Port()
	eng.Start()
	t.Cleanup(func() { eng.Stop(); ts.Close() })
	return &testNode{srv: srv, eng: eng, ts: ts, dir: dir}
}

// installBundle makes the app-data API accept writes for an app (the API
// checks for an installed bundle; peer sync deliberately does not).
func (n *testNode) installBundle(t *testing.T, name string) {
	dir := filepath.Join(n.dir, "apps", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"title":"`+name+`"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func pairNodes(t *testing.T, a, b *testNode) {
	t.Helper()
	code, _ := b.eng.MintCode()
	if _, err := a.eng.Join(b.addr(t), code); err != nil {
		t.Fatalf("join: %v", err)
	}
}

func todosDoc(id, text string, updated int64) string {
	return fmt.Sprintf(`{"version":2,"items":[{"id":%q,"text":%q,"done":false,"created":1,"updated":%d}]}`, id, text, updated)
}

func TestPairAndPushThroughAPI(t *testing.T) {
	a, b := newTestNode(t), newTestNode(t)
	a.installBundle(t, "Todo")
	pairNodes(t, a, b)

	doc := todosDoc("t1", "written on A", 100)
	req, _ := http.NewRequest("PUT", a.ts.URL+"/v1/apps/Todo/data/todos.json", strings.NewReader(doc))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}

	waitFor(t, "file to sync to B", func() bool {
		got, err := os.ReadFile(b.dataFile("Todo", "todos.json"))
		return err == nil && bytes.Contains(got, []byte("written on A"))
	})
}

func TestDivergentFilesMergeAfterPairing(t *testing.T) {
	a, b := newTestNode(t), newTestNode(t)
	// both nodes have pre-existing, different todo lists BEFORE pairing
	for n, doc := range map[*testNode]string{
		a: todosDoc("from-a", "a's item", 10),
		b: todosDoc("from-b", "b's item", 20),
	} {
		p := n.dataFile("Todo", "todos.json")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(doc), 0o644)
	}
	pairNodes(t, a, b)

	waitFor(t, "both nodes to converge on the union", func() bool {
		ga, ea := os.ReadFile(a.dataFile("Todo", "todos.json"))
		gb, eb := os.ReadFile(b.dataFile("Todo", "todos.json"))
		return ea == nil && eb == nil && bytes.Equal(ga, gb) &&
			bytes.Contains(ga, []byte("a's item")) && bytes.Contains(ga, []byte("b's item"))
	})
}

func TestNonMergeableLWWConverges(t *testing.T) {
	a, b := newTestNode(t), newTestNode(t)
	for n, doc := range map[*testNode]string{
		a: `{"station":{"id":"111"},"range":1}`,
		b: `{"station":{"id":"222"},"range":3}`,
	} {
		p := n.dataFile("Tides", "settings.json")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(doc), 0o644)
	}
	pairNodes(t, a, b)

	waitFor(t, "settings to converge to one winner", func() bool {
		ga, ea := os.ReadFile(a.dataFile("Tides", "settings.json"))
		gb, eb := os.ReadFile(b.dataFile("Tides", "settings.json"))
		return ea == nil && eb == nil && bytes.Equal(ga, gb)
	})
}

func TestThirdNodeJoinsTransitively(t *testing.T) {
	a, b, c := newTestNode(t), newTestNode(t), newTestNode(t)
	a.installBundle(t, "Notes")
	pairNodes(t, a, b)
	// C joins B only — it must still learn about and sync with A
	pairNodes(t, c, b)

	waitFor(t, "C to adopt A as a peer via gossip", func() bool {
		for _, p := range c.eng.ListPeers() {
			if p.ID == a.eng.SelfID() {
				return true
			}
		}
		return false
	})

	doc := `{"notes":[{"id":"n1","text":"hello mesh","created":1,"updated":100}]}`
	req, _ := http.NewRequest("PUT", a.ts.URL+"/v1/apps/Notes/data/notes.json", strings.NewReader(doc))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	waitFor(t, "A's note to reach C", func() bool {
		got, err := os.ReadFile(c.dataFile("Notes", "notes.json"))
		return err == nil && bytes.Contains(got, []byte("hello mesh"))
	})
}

func TestDeletePropagates(t *testing.T) {
	a, b := newTestNode(t), newTestNode(t)
	a.installBundle(t, "Todo")
	pairNodes(t, a, b)

	doc := todosDoc("t1", "to be deleted", 100)
	req, _ := http.NewRequest("PUT", a.ts.URL+"/v1/apps/Todo/data/todos.json", strings.NewReader(doc))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitFor(t, "file on B", func() bool {
		_, err := os.Stat(b.dataFile("Todo", "todos.json"))
		return err == nil
	})

	req, _ = http.NewRequest("DELETE", a.ts.URL+"/v1/apps/Todo/data/todos.json", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitFor(t, "delete to reach B", func() bool {
		_, err := os.Stat(b.dataFile("Todo", "todos.json"))
		return os.IsNotExist(err)
	})
}

func TestAppDataSeqDropsOlderContent(t *testing.T) {
	a := newTestNode(t)
	a.installBundle(t, "Paint")
	put := func(seq, body string) string {
		req, _ := http.NewRequest("PUT", a.ts.URL+"/v1/apps/Paint/data/canvas.png", strings.NewReader(body))
		if seq != "" {
			req.Header.Set("X-Exe-Seq", seq)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return fmt.Sprint(m["status"])
	}
	if s := put("1000", "newest"); s != "saved" {
		t.Fatalf("first write: %s", s)
	}
	// an older-content PUT arriving late (racing in-flight save) is dropped
	if s := put("500", "older"); s != "stale" {
		t.Fatalf("older seq must be dropped: %s", s)
	}
	if got, _ := os.ReadFile(a.dataFile("Paint", "canvas.png")); string(got) != "newest" {
		t.Fatalf("older content clobbered newer: %q", got)
	}
	// a genuinely newer content timestamp still wins
	if s := put("2000", "evenNewer"); s != "saved" {
		t.Fatalf("newer write: %s", s)
	}
	if got, _ := os.ReadFile(a.dataFile("Paint", "canvas.png")); string(got) != "evenNewer" {
		t.Fatalf("newer content should win: %q", got)
	}
}

func TestConcurrentDeleteVsLivePreservesData(t *testing.T) {
	// The hello scenario end-to-end: node B independently deleted a file that
	// node A has real data for. The versions are concurrent (they were never
	// paired), so A's data must survive and propagate to B — a stale tombstone
	// must never delete data the deleting node hadn't seen.
	a, b := newTestNode(t), newTestNode(t)
	a.installBundle(t, "Todo")
	b.installBundle(t, "Todo")

	// A has real todos
	real := todosDoc("real", "keep me", 100)
	req, _ := http.NewRequest("PUT", a.ts.URL+"/v1/apps/Todo/data/todos.json", strings.NewReader(real))
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// B independently creates then deletes its own todos (a concurrent tombstone)
	req, _ = http.NewRequest("PUT", b.ts.URL+"/v1/apps/Todo/data/todos.json", strings.NewReader(todosDoc("btmp", "scratch", 50)))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	req, _ = http.NewRequest("DELETE", b.ts.URL+"/v1/apps/Todo/data/todos.json", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	pairNodes(t, a, b)

	waitFor(t, "A's real data to survive and reach B", func() bool {
		ga, ea := os.ReadFile(a.dataFile("Todo", "todos.json"))
		gb, eb := os.ReadFile(b.dataFile("Todo", "todos.json"))
		return ea == nil && eb == nil && bytes.Contains(ga, []byte("keep me")) &&
			bytes.Contains(gb, []byte("keep me")) && bytes.Equal(ga, gb)
	})
}

func TestPeerRoutesRejectOutsiders(t *testing.T) {
	a := newTestNode(t)
	// unsigned manifest request must 401
	resp, err := http.Get(a.ts.URL + "/v1/peer/manifest")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned manifest: want 401, got %d", resp.StatusCode)
	}
	// bad join code must 403
	body, _ := json.Marshal(map[string]string{"code": "wrong", "id": "x", "pubkey": "x", "port": "1"})
	resp, err = http.Post(a.ts.URL+"/v1/peer/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad code: want 403, got %d", resp.StatusCode)
	}
}
