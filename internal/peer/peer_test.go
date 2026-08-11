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

func TestManifestBumpApplyClaimPersist(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1 := m.Bump("Todo/todos.json", "nodeA", false, 10, 111)
	if v1.Ctr != 1 || v1.Origin != "nodeA" {
		t.Fatalf("bump: %+v", v1)
	}
	v2 := m.Bump("Todo/todos.json", "nodeA", false, 12, 222)
	if v2.Ctr != 2 {
		t.Fatalf("bump must advance: %+v", v2)
	}
	if !m.Fingerprint("Todo/todos.json", 12, 222) || m.Fingerprint("Todo/todos.json", 12, 999) {
		t.Fatal("fingerprint")
	}
	m.Apply("Notes/notes.json", Version{Ctr: 7, Origin: "nodeB"}, 5, 333)
	cl := m.Claim("Todo/todos.json", "nodeA", 9, false, 12, 222)
	if cl.Ctr != 9 {
		t.Fatalf("claim: %+v", cl)
	}
	// reload from disk
	m2, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m2.Get("Todo/todos.json"); !ok || v.Ctr != 9 || v.Origin != "nodeA" {
		t.Fatalf("persist: %+v %v", v, ok)
	}
	if v, ok := m2.Get("Notes/notes.json"); !ok || v.Ctr != 7 || v.Origin != "nodeB" {
		t.Fatalf("persist applied: %+v %v", v, ok)
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
	req.Header.Set(hdrCtr, "3")
	req.Header.Set(hdrOrigin, idA.ID)
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
	req.Header.Set(hdrCtr, "4")
	if _, err := VerifyRequest(store, req, body, idB.ID); err == nil {
		t.Fatal("tampered version must fail")
	}
	req.Header.Set(hdrCtr, "3")
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
	for _, key := range []string{"../config.json", "..\\config.json", "Todo/../../config.json", "noslashkey"} {
		if _, err := eng.ApplyRemote(key, []byte("pwned"), Version{Ctr: 9, Origin: "ffff", Deleted: false}); err == nil {
			t.Fatalf("key %q must be rejected", key)
		}
		if _, err := eng.ApplyRemote(key, nil, Version{Ctr: 9, Origin: "ffff", Deleted: true}); err == nil {
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

	// a legacy peer pushes a bare v1 array with a higher counter
	v1 := []byte(`[{"text":"legacy","done":false,"created":"2026-01-01T00:00:00Z"}]`)
	outcome, err := eng.ApplyRemote("Todo/todos.json", v1, Version{Ctr: 99, Origin: "ffffffffffffffff"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "stale" {
		t.Fatalf("v1 remote should be rejected as stale, got %s", outcome)
	}
	if got, _ := os.ReadFile(p); !bytes.Contains(got, []byte("real work")) {
		t.Fatalf("v2 local was clobbered by v1 remote: %s", got)
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
	outcome, err := eng.ApplyRemote("Todo/todos.json", remote, Version{Ctr: 1, Origin: "ffffffffffffffff"})
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
	if v, ok := eng.man.Get("Todo/todos.json"); !ok || v.Ctr != 2 || v.Origin != ident.ID {
		t.Fatalf("merged version should be ctr2/self: %+v", v)
	}
}
