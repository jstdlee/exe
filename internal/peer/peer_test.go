package peer

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestManifestBumpApplyPersist(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1 := m.Bump("Todo/todos.json", "nodeA", false, 10, 111)
	if v1.Vec["nodeA"] != 1 {
		t.Fatalf("bump: %+v", v1)
	}
	v2 := m.Bump("Todo/todos.json", "nodeA", false, 12, 222)
	if v2.Vec["nodeA"] != 2 {
		t.Fatalf("bump must advance: %+v", v2)
	}
	if !m.Fingerprint("Todo/todos.json", 12, 222) || m.Fingerprint("Todo/todos.json", 12, 999) {
		t.Fatal("fingerprint")
	}
	// adopt a remote version verbatim (a merged pointwise-max vector)
	m.Apply("Notes/notes.json", Version{Vec: map[string]int64{"nodeA": 2, "nodeB": 7}}, 5, 333)
	// reload from disk
	m2, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m2.Get("Todo/todos.json"); !ok || v.Vec["nodeA"] != 2 {
		t.Fatalf("persist: %+v %v", v, ok)
	}
	if v, ok := m2.Get("Notes/notes.json"); !ok || v.Vec["nodeB"] != 7 || v.Vec["nodeA"] != 2 {
		t.Fatalf("persist applied: %+v %v", v, ok)
	}
}

func TestManifestMigratesLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	// a pre-vector manifest with the old {ctr, origin} shape
	legacy := `{"files":{"Todo/todos.json":{"ctr":5,"origin":"nodeX","deleted":false,"mtime":123,"size":9,"disk_mtime":456}}}`
	if err := os.WriteFile(filepath.Join(dir, "sync-manifest.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := m.Get("Todo/todos.json")
	if !ok || v.Vec["nodeX"] != 5 {
		t.Fatalf("legacy ctr/origin must migrate to a one-entry vector: %+v", v)
	}
}

func TestIdentityStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || a.PubKey() != b.PubKey() {
		t.Fatal("identity must be stable across loads")
	}
	if err := verifyClaimedKey(a.ID, a.PubKey()); err != nil {
		t.Fatal(err)
	}
	if err := verifyClaimedKey("0000000000000000", a.PubKey()); err == nil {
		t.Fatal("wrong id must not verify")
	}
}

func TestSignedRequestRoundtrip(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	idA, _ := LoadIdentity(dirA)
	idB, _ := LoadIdentity(dirB)
	store, err := LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Upsert(&Peer{ID: idA.ID, Name: "a", PubKey: idA.PubKey(), Addr: "127.0.0.1:1"})

	// idB is the recipient; every signed request is bound to idB.ID.
	body := []byte(`{"hello":true}`)
	req := httptest.NewRequest("PUT", "/v1/peer/file/Todo/todos.json", nil)
	req.Header.Set(hdrVer, EncodeVector(Version{Vec: map[string]int64{idA.ID: 3}}))
	SignRequest(idA, req, body, idB.ID)
	p, err := VerifyRequest(store, req, body, idB.ID)
	if err != nil || p.ID != idA.ID {
		t.Fatalf("verify: %v", err)
	}
	// tampered body
	if _, err := VerifyRequest(store, req, []byte(`{"hello":false}`), idB.ID); err == nil {
		t.Fatal("tampered body must fail")
	}
	// tampered version header (it's part of the canonical string)
	orig := req.Header.Get(hdrVer)
	req.Header.Set(hdrVer, EncodeVector(Version{Vec: map[string]int64{idA.ID: 4}}))
	if _, err := VerifyRequest(store, req, body, idB.ID); err == nil {
		t.Fatal("tampered version must fail")
	}
	req.Header.Set(hdrVer, orig)
	// re-targeted to a different node: a signature meant for idB must not
	// verify at some other daemon (replay via a relaying peer)
	if _, err := VerifyRequest(store, req, body, "someothernode"); err == nil {
		t.Fatal("request addressed to idB must fail at another node")
	}
	// stale timestamp
	req2 := httptest.NewRequest("GET", "/v1/peer/ping", nil)
	SignRequest(idA, req2, nil, idB.ID)
	req2.Header.Set(hdrTS, strconv.FormatInt(time.Now().Add(-10*time.Minute).UnixMilli(), 10))
	if _, err := VerifyRequest(store, req2, nil, idB.ID); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	// unknown signer
	req3 := httptest.NewRequest("GET", "/v1/peer/ping", nil)
	SignRequest(idB, req3, nil, idB.ID)
	if _, err := VerifyRequest(store, req3, nil, idB.ID); err == nil {
		t.Fatal("unenrolled peer must fail")
	}
}

func TestStoreUpsertKeyPinning(t *testing.T) {
	dir := t.TempDir()
	idA, _ := LoadIdentity(t.TempDir())
	idB, _ := LoadIdentity(t.TempDir())
	s, err := LoadStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "a", PubKey: idA.PubKey(), Addr: "x:1"}); err != nil {
		t.Fatal(err)
	}
	// same id, different key: must be rejected (trust-on-enroll)
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "evil", PubKey: idB.PubKey()}); err == nil {
		t.Fatal("key swap must be rejected")
	}
	// addr/name refresh with the same key is fine
	if err := s.Upsert(&Peer{ID: idA.ID, Name: "renamed", PubKey: idA.PubKey(), Addr: "y:2"}); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadStore(dir)
	p, ok := s2.Get(idA.ID)
	if !ok || p.Name != "renamed" || p.Addr != "y:2" {
		t.Fatalf("persist: %+v", p)
	}
}

func TestApplyRemoteRejectsTraversalKeys(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	// plant a file the traversal would target
	victim := filepath.Join(dir, "config.json")
	os.WriteFile(victim, []byte("original"), 0o644)
	rv := Version{Vec: map[string]int64{"ffff": 9}}
	rvDel := Version{Vec: map[string]int64{"ffff": 9}, Deleted: true}
	for _, key := range []string{"../config.json", "..\\config.json", "Todo/../../config.json", "noslashkey"} {
		if _, err := eng.ApplyRemote(key, []byte("pwned"), rv); err == nil {
			t.Fatalf("key %q must be rejected", key)
		}
		if _, err := eng.ApplyRemote(key, nil, rvDel); err == nil {
			t.Fatalf("delete key %q must be rejected", key)
		}
	}
	if got, _ := os.ReadFile(victim); string(got) != "original" {
		t.Fatalf("traversal escaped DataDir: victim is now %q", got)
	}
}

func TestApplyRemoteV1RemoteDoesNotClobberV2Local(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	v2 := todoJSON(`{"id":"keep","text":"real work","done":false,"created":1,"updated":5}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, v2, 0o644)

	// a legacy peer pushes a bare v1 array with a concurrent version
	v1 := []byte(`[{"text":"legacy","done":false,"created":"2026-01-01T00:00:00Z"}]`)
	outcome, err := eng.ApplyRemote("Todo/todos.json", v1, Version{Vec: map[string]int64{"ffffffffffffffff": 99}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "kept-live" {
		t.Fatalf("v1 remote should be kept-live (ours preserved), got %s", outcome)
	}
	if got, _ := os.ReadFile(p); !bytes.Contains(got, []byte("real work")) {
		t.Fatalf("v2 local was clobbered by v1 remote: %s", got)
	}
}

func TestConcurrentDeleteLosesToLiveData(t *testing.T) {
	// The hello scenario: a node has real data for a file that another node
	// independently deleted. The delete is concurrent (neither saw the
	// other), so the live data must survive rather than be tombstoned.
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	real := todoJSON(`{"id":"real","text":"do not lose me","done":false,"created":1,"updated":1}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, real, 0o644)

	// a concurrent delete from another node (a stale tombstone, high counter)
	del := Version{Vec: map[string]int64{"othernode": 7}, Deleted: true}
	outcome, err := eng.ApplyRemote("Todo/todos.json", nil, del)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "kept-live" {
		t.Fatalf("concurrent delete must not win over live data, got %s", outcome)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("live file was deleted by a concurrent tombstone")
	}
	// but a delete that causally dominates our version DOES win
	local, _ := eng.man.Get("Todo/todos.json")
	dominating := Version{Vec: MaxVec(local.Vec, map[string]int64{"othernode": 8}), Deleted: true}
	// make it strictly dominate by bumping the other node past the merged vec
	dominating.Vec["othernode"] = local.Vec["othernode"] + 1
	for k, v := range local.Vec {
		if dominating.Vec[k] < v {
			dominating.Vec[k] = v
		}
	}
	if outcome, err := eng.ApplyRemote("Todo/todos.json", nil, dominating); err != nil || outcome != "applied" {
		t.Fatalf("a causally-dominating delete must win: outcome=%s err=%v", outcome, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("dominating delete should have removed the file")
	}
}

func TestConcurrentNonMergeableBacksUpLoser(t *testing.T) {
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "appdata", "Paint", "canvas.png")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("LOCAL-ART"), 0o644) // scan-versioned on apply

	outcome, err := eng.ApplyRemote("Paint/canvas.png", []byte("REMOTE-ART"), Version{Vec: map[string]int64{"other": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "resolved" {
		t.Fatalf("non-mergeable concurrent conflict should resolve, got %s", outcome)
	}
	// the loser must be preserved under sync-conflicts, nothing silently lost
	conflicts := filepath.Join(dir, "sync-conflicts", "Paint")
	ents, err := os.ReadDir(conflicts)
	if err != nil || len(ents) != 1 {
		t.Fatalf("expected one conflict backup, got %v (err %v)", ents, err)
	}
	winner, _ := os.ReadFile(p)
	backup, _ := os.ReadFile(filepath.Join(conflicts, ents[0].Name()))
	// winner + loser are exactly the two inputs (deterministic by content hash)
	got := map[string]bool{string(winner): true, string(backup): true}
	if !got["LOCAL-ART"] || !got["REMOTE-ART"] {
		t.Fatalf("winner/backup should be the two inputs, got winner=%q backup=%q", winner, backup)
	}
}

func TestApplyRemoteVersionsUnseenLocalFile(t *testing.T) {
	// A push racing the first local scan must merge, not clobber: the
	// receiving node's pre-existing file has no manifest entry yet.
	dir := t.TempDir()
	ident, _ := LoadIdentity(dir)
	eng, err := NewEngine(EngineConfig{
		StateDir: dir, DataDir: filepath.Join(dir, "appdata"), Self: ident,
		PortFn: func() string { return "0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	local := todoJSON(`{"id":"mine","text":"local","done":false,"created":1,"updated":1}`)
	p := filepath.Join(dir, "appdata", "Todo", "todos.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, local, 0o644)

	remote := todoJSON(`{"id":"theirs","text":"remote","done":false,"created":2,"updated":2}`)
	outcome, err := eng.ApplyRemote("Todo/todos.json", remote, Version{Vec: map[string]int64{"ffffffffffffffff": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "merged" {
		t.Fatalf("want merged, got %s", outcome)
	}
	got, _ := os.ReadFile(p)
	for _, want := range []string{"mine", "theirs"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("merged file lost %q: %s", want, got)
		}
	}
	// merged version is the pointwise max: our scan-bump {self:1} plus {ffff:1}
	if v, ok := eng.man.Get("Todo/todos.json"); !ok || v.Vec[ident.ID] != 1 || v.Vec["ffffffffffffffff"] != 1 {
		t.Fatalf("merged version should dominate both inputs: %+v", v)
	}
}
