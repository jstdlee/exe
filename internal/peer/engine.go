package peer

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// EncodeVector renders a version's vector for the X-Exe-Ver wire header
// (base64 of its JSON), and ParseVersion reconstructs it on the other side.
func EncodeVector(v Version) string {
	b, _ := json.Marshal(v.Vec)
	return base64.StdEncoding.EncodeToString(b)
}

func ParseVersion(verB64 string, deleted bool) Version {
	vec := map[string]int64{}
	if b, err := base64.StdEncoding.DecodeString(verB64); err == nil {
		json.Unmarshal(b, &vec)
	}
	return Version{Vec: vec, Deleted: deleted}
}

// EngineConfig wires the engine into the daemon.
type EngineConfig struct {
	StateDir string
	DataDir  string // ~/.exe/appdata — the tree being synced
	Self     *Identity
	PortFn   func() string // our API port; peers dial <tailscale-ip>:<port>
	// OnApply fires after a remote change lands on disk so the server can
	// tell open app windows to reload (SSE -> desktop -> iframe).
	OnApply func(app, rel string, deleted bool)
	Logf    func(format string, args ...any)
}

// Engine syncs ~/.exe/appdata with every enrolled peer: local writes push
// immediately, a reconcile loop diffs manifests to catch anything missed,
// and mergeable documents (todos.json, notes.json) union item-by-item
// instead of last-writer-wins.
type Engine struct {
	cfg   EngineConfig
	peers *Store
	man   *Manifest

	client *http.Client

	fileMu sync.Mutex // serializes apply/scan against each other

	codeMu sync.Mutex
	codes  map[string]time.Time // active join codes -> expiry

	statusMu sync.Mutex
	status   map[string]*Status
	statusAt time.Time

	kick chan struct{}
	stop chan struct{}
}

// validKey guards every remote-driven file path: a sync key must be
// "App/relpath" with a filesystem-local relative portion. Peer manifests are
// untrusted input, so a "../config.json" key that would escape DataDir is
// rejected before it ever reaches filePath/os.Remove/writeFileAtomic.
func validKey(key string) bool {
	if !strings.Contains(key, "/") {
		return false
	}
	return filepath.IsLocal(filepath.FromSlash(key))
}

// Status is one row of the Join dialog's peer table.
type Status struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Addr      string `json:"addr"`
	Online    bool   `json:"online"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	LastSeen  int64  `json:"last_seen,omitempty"` // unix ms
}

// PeerInfo is the wire form of a peer entry (pairing + mesh gossip).
type PeerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	PubKey string `json:"pubkey"`
	Addr   string `json:"addr"`
}

type PairRequest struct {
	Code   string `json:"code"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	PubKey string `json:"pubkey"`
	Port   string `json:"port"`
}

type PairResponse struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	PubKey string     `json:"pubkey"`
	Port   string     `json:"port"`
	Peers  []PeerInfo `json:"peers"`
}

type manifestResponse struct {
	ID    string             `json:"id"`
	Name  string             `json:"name"`
	Port  string             `json:"port"`
	Files map[string]Version `json:"files"`
	Peers []PeerInfo         `json:"peers"`
}

const (
	codeTTL       = 10 * time.Minute
	reconcileTick = 30 * time.Second
	statusTTL     = 5 * time.Second
)

func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	store, err := LoadStore(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("peers.json: %w", err)
	}
	man, err := LoadManifest(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("sync-manifest.json: %w", err)
	}
	return &Engine{
		cfg:    cfg,
		peers:  store,
		man:    man,
		client: &http.Client{Timeout: 15 * time.Second},
		codes:  map[string]time.Time{},
		status: map[string]*Status{},
		kick:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
	}, nil
}

// Start runs the reconcile loop until Stop.
func (e *Engine) Start() {
	go func() {
		t := time.NewTicker(reconcileTick)
		defer t.Stop()
		// first pass shortly after boot, once listeners are up
		first := time.NewTimer(2 * time.Second)
		defer first.Stop()
		for {
			select {
			case <-e.stop:
				return
			case <-first.C:
			case <-t.C:
			case <-e.kick:
			}
			e.ScanLocal()
			for _, p := range e.peers.List() {
				e.reconcile(p)
			}
		}
	}()
}

func (e *Engine) Stop() { close(e.stop) }

// ReconcileNow schedules an immediate pass (after pairing).
func (e *Engine) ReconcileNow() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) SelfID() string     { return e.cfg.Self.ID }
func (e *Engine) SelfName() string   { return e.cfg.Self.Name }
func (e *Engine) SelfPubKey() string { return e.cfg.Self.PubKey() }
func (e *Engine) Port() string       { return e.cfg.PortFn() }

func (e *Engine) ListPeers() []*Peer { return e.peers.List() }

// VerifyPeer authenticates an inbound signed peer request.
func (e *Engine) VerifyPeer(r *http.Request, body []byte) (*Peer, error) {
	return VerifyRequest(e.peers, r, body, e.cfg.Self.ID)
}

// LockFiles runs fn while holding the mutex that serializes remote applies,
// so a local app-data write and its versioning can't interleave with an
// ApplyRemote of the same file (last-writer-loses clobbers). The server's
// app-data PUT/DELETE handlers wrap their write + LocalWrite/LocalDelete in
// this.
func (e *Engine) LockFiles(fn func()) {
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	fn()
}

func (e *Engine) ManifestSnapshot() map[string]Version { return e.man.Snapshot() }

// ---- pairing ----------------------------------------------------------------

// MintCode issues a single-use join code another node can pair with.
func (e *Engine) MintCode() (string, time.Time) {
	b := make([]byte, 4)
	rand.Read(b)
	code := hex.EncodeToString(b)
	exp := time.Now().Add(codeTTL)
	e.codeMu.Lock()
	e.codes[code] = exp
	e.codeMu.Unlock()
	return code, exp
}

func (e *Engine) checkCode(code string) bool {
	e.codeMu.Lock()
	defer e.codeMu.Unlock()
	exp, ok := e.codes[code]
	if !ok || time.Now().After(exp) {
		delete(e.codes, code)
		return false
	}
	delete(e.codes, code) // single use
	return true
}

// HandlePair enrolls a joining node (authenticated by join code, since it
// has no key on file yet) and returns our identity plus the peer list so
// the joiner meshes with everyone we know.
func (e *Engine) HandlePair(remoteHost string, req PairRequest) (*PairResponse, error) {
	if !e.checkCode(req.Code) {
		time.Sleep(300 * time.Millisecond) // slow down code guessing
		return nil, errors.New("invalid or expired join code")
	}
	if err := verifyClaimedKey(req.ID, req.PubKey); err != nil {
		return nil, err
	}
	port := req.Port
	if port == "" {
		port = "7777"
	}
	p := &Peer{ID: req.ID, Name: req.Name, PubKey: req.PubKey, Addr: net.JoinHostPort(remoteHost, port)}
	if err := e.peers.Upsert(p); err != nil {
		return nil, err
	}
	e.cfg.Logf("peer: paired with %s (%s) at %s", p.Name, p.ID, p.Addr)
	e.ReconcileNow()
	return &PairResponse{
		ID: e.cfg.Self.ID, Name: e.cfg.Self.Name, PubKey: e.cfg.Self.PubKey(),
		Port: e.cfg.PortFn(), Peers: e.PeerInfos(),
	}, nil
}

// verifyClaimedKey checks a claimed id actually fingerprints the claimed key.
func verifyClaimedKey(id, pubKey string) error {
	p := Peer{PubKey: pubKey}
	key, err := p.Key()
	if err != nil {
		return err
	}
	if Fingerprint(key) != id {
		return errors.New("peer id does not match its key")
	}
	return nil
}

// PeerInfos is the gossiped wire form of the peer list.
func (e *Engine) PeerInfos() []PeerInfo {
	out := []PeerInfo{}
	for _, p := range e.peers.List() {
		out = append(out, PeerInfo{ID: p.ID, Name: p.Name, PubKey: p.PubKey, Addr: p.Addr})
	}
	return out
}

// Join dials addr's pair endpoint with a code its owner read to us.
func (e *Engine) Join(addr, code string) (*Peer, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("address required")
	}
	if !strings.Contains(addr, ":") {
		addr = net.JoinHostPort(addr, "7777")
	}
	body, _ := json.Marshal(PairRequest{
		Code: strings.TrimSpace(code), ID: e.cfg.Self.ID, Name: e.cfg.Self.Name,
		PubKey: e.cfg.Self.PubKey(), Port: e.cfg.PortFn(),
	})
	resp, err := e.client.Post("http://"+addr+"/v1/peer/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error != "" {
			return nil, errors.New(apiErr.Error)
		}
		return nil, fmt.Errorf("pair: HTTP %d", resp.StatusCode)
	}
	var pr PairResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	if err := verifyClaimedKey(pr.ID, pr.PubKey); err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	port := pr.Port
	if port == "" {
		port = "7777"
	}
	p := &Peer{ID: pr.ID, Name: pr.Name, PubKey: pr.PubKey, Addr: net.JoinHostPort(host, port)}
	if err := e.peers.Upsert(p); err != nil {
		return nil, err
	}
	// transitive mesh: everyone the other node knows becomes our peer too
	for _, pi := range pr.Peers {
		e.adoptPeer(pi)
	}
	e.cfg.Logf("peer: joined %s (%s) at %s", p.Name, p.ID, p.Addr)
	e.ReconcileNow()
	return p, nil
}

// adoptPeer enrolls a gossiped peer, ignoring self and key conflicts.
func (e *Engine) adoptPeer(pi PeerInfo) {
	if pi.ID == e.cfg.Self.ID || pi.ID == "" {
		return
	}
	if err := verifyClaimedKey(pi.ID, pi.PubKey); err != nil {
		e.cfg.Logf("peer: ignoring gossiped %s: %v", pi.ID, err)
		return
	}
	if err := e.peers.Upsert(&Peer{ID: pi.ID, Name: pi.Name, PubKey: pi.PubKey, Addr: pi.Addr}); err != nil {
		e.cfg.Logf("peer: gossiped %s: %v", pi.ID, err)
	}
}

// Leave drops a peer, telling it first (best effort) so it drops us too.
func (e *Engine) Leave(id string) error {
	p, ok := e.peers.Get(id)
	if !ok {
		return errors.New("no such peer")
	}
	req, err := http.NewRequest("POST", "http://"+p.Addr+"/v1/peer/unpair", nil)
	if err == nil {
		SignRequest(e.cfg.Self, req, nil, p.ID)
		if resp, perr := e.client.Do(req); perr == nil {
			resp.Body.Close()
		}
	}
	e.statusMu.Lock()
	delete(e.status, id)
	e.statusMu.Unlock()
	return e.peers.Remove(id)
}

// Unpair handles the inbound side of Leave.
func (e *Engine) Unpair(id string) error {
	e.statusMu.Lock()
	delete(e.status, id)
	e.statusMu.Unlock()
	return e.peers.Remove(id)
}

// ---- local writes -----------------------------------------------------------

func (e *Engine) filePath(key string) string {
	return filepath.Join(e.cfg.DataDir, filepath.FromSlash(key))
}

// LocalWrite versions a write that just landed through the app-data API and
// pushes it to every peer.
func (e *Engine) LocalWrite(app, rel string) {
	key := app + "/" + rel
	fi, err := os.Stat(e.filePath(key))
	if err != nil {
		return
	}
	e.man.Bump(key, e.cfg.Self.ID, false, fi.Size(), fi.ModTime().UnixNano())
	go e.pushAll(key)
}

// LocalDelete versions an API delete (tombstoned in the manifest).
func (e *Engine) LocalDelete(app, rel string) {
	key := app + "/" + rel
	e.man.Bump(key, e.cfg.Self.ID, true, 0, 0)
	go e.pushAll(key)
}

// ScanLocal picks up changes that bypassed the API: files edited or dropped
// directly in ~/.exe/appdata (agents do this), plus first-boot inventory.
func (e *Engine) ScanLocal() {
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	seen := map[string]bool{}
	root := e.cfg.DataDir
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // temp files (.put-*, .sync-*) are never inventory
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if !strings.Contains(key, "/") {
			return nil // stray file at the appdata root; not app data
		}
		seen[key] = true
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if !e.man.Fingerprint(key, fi.Size(), fi.ModTime().UnixNano()) {
			e.man.Bump(key, e.cfg.Self.ID, false, fi.Size(), fi.ModTime().UnixNano())
			go e.pushAll(key)
		}
		return nil
	})
	for key, v := range e.man.Snapshot() {
		if !v.Deleted && !seen[key] {
			e.man.Bump(key, e.cfg.Self.ID, true, 0, 0)
			go e.pushAll(key)
		}
	}
}

// ---- push / fetch -----------------------------------------------------------

func peerFileURL(addr, key string) string {
	parts := strings.Split(key, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return "http://" + addr + "/v1/peer/file/" + strings.Join(parts, "/")
}

func (e *Engine) pushAll(key string) {
	for _, p := range e.peers.List() {
		e.pushTo(p, key)
	}
}

// pushTo sends our current version of key to one peer. The receiving side
// answers "stale" when it already has something newer — that's fine, its
// own push or the next reconcile settles it.
func (e *Engine) pushTo(p *Peer, key string) {
	v, ok := e.man.Get(key)
	if !ok {
		return
	}
	var body []byte
	if !v.Deleted {
		b, err := os.ReadFile(e.filePath(key))
		if err != nil {
			return
		}
		body = b
	}
	req, err := http.NewRequest("PUT", peerFileURL(p.Addr, key), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set(hdrVer, EncodeVector(v))
	if v.Deleted {
		req.Header.Set(hdrDeleted, "1")
	}
	SignRequest(e.cfg.Self, req, body, p.ID)
	resp, err := e.client.Do(req)
	if err != nil {
		e.cfg.Logf("peer: push %s to %s: %v", key, p.Name, err)
		return
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.cfg.Logf("peer: push %s to %s: HTTP %d", key, p.Name, resp.StatusCode)
	}
}

func (e *Engine) fetchFrom(p *Peer, key string, rv Version) {
	if rv.Deleted {
		if _, err := e.ApplyRemote(key, nil, rv); err != nil {
			e.cfg.Logf("peer: apply delete %s from %s: %v", key, p.Name, err)
		}
		return
	}
	req, err := http.NewRequest("GET", peerFileURL(p.Addr, key), nil)
	if err != nil {
		return
	}
	SignRequest(e.cfg.Self, req, nil, p.ID)
	resp, err := e.client.Do(req)
	if err != nil {
		e.cfg.Logf("peer: fetch %s from %s: %v", key, p.Name, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1))
	if err != nil || len(body) > 10<<20 {
		return
	}
	got := ParseVersion(resp.Header.Get(hdrVer), false)
	if len(got.Vec) == 0 {
		got = rv // response carried no version header; use the manifest one
	}
	if _, err := e.ApplyRemote(key, body, got); err != nil {
		e.cfg.Logf("peer: apply %s from %s: %v", key, p.Name, err)
	}
}

// FileForPeer serves a peer's GET: content plus our version of it.
func (e *Engine) FileForPeer(app, rel string) ([]byte, Version, error) {
	key := app + "/" + rel
	v, ok := e.man.Get(key)
	if !ok || v.Deleted {
		return nil, Version{}, os.ErrNotExist
	}
	b, err := os.ReadFile(e.filePath(key))
	if err != nil {
		return nil, Version{}, err
	}
	return b, v, nil
}

// ---- applying remote changes ------------------------------------------------

// ApplyRemote lands a peer's version of key locally, using the version
// vectors to separate causal order from concurrency:
//
//	stale       — we already dominate the remote; the sender takes ours
//	applied     — remote strictly dominated ours; adopted verbatim
//	merged      — concurrent mergeable docs unioned item-by-item
//	resolved    — concurrent non-mergeable conflict; deterministic winner
//	              kept, the loser preserved under ~/.exe/sync-conflicts
//	kept-live   — concurrent delete vs. our live file; the live file survives
//	resurrected — concurrent live remote vs. our tombstone; content restored
//
// Every concurrency resolution sets the version to the pointwise max of both
// inputs (no self-increment), so all nodes converge on the same vector with
// no ping-pong. A delete only wins when it causally dominates the local
// version, so a stale tombstone can never delete concurrent real data.
func (e *Engine) ApplyRemote(key string, body []byte, rv Version) (string, error) {
	if !validKey(key) {
		return "", errors.New("invalid sync key")
	}
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	p := e.filePath(key)
	local, exists := e.man.Get(key)
	// Version any on-disk content the manifest hasn't seen yet — data that
	// predates pairing, or direct edits under ~/.exe/appdata — BEFORE
	// comparing, so a push racing the first scan can't clobber unversioned
	// local work (it becomes a concurrent version, not a dominated one).
	if fi, err := os.Stat(p); err == nil {
		if !exists || !e.man.Fingerprint(key, fi.Size(), fi.ModTime().UnixNano()) {
			local = e.man.Bump(key, e.cfg.Self.ID, false, fi.Size(), fi.ModTime().UnixNano())
			exists = true
		}
	}
	app, rel, _ := strings.Cut(key, "/")
	if !exists {
		return e.adoptRemote(key, app, rel, p, body, rv)
	}
	switch {
	case local.SameVersion(rv), local.Dominates(rv):
		return "stale", nil
	case rv.Dominates(local):
		return e.adoptRemote(key, app, rel, p, body, rv)
	default:
		return e.resolveConcurrent(key, app, rel, p, body, rv, local)
	}
}

// adoptRemote takes the remote version verbatim — it dominates ours, or we
// had nothing: its bytes for a write, or a delete of our copy.
func (e *Engine) adoptRemote(key, app, rel, p string, body []byte, rv Version) (string, error) {
	if rv.Deleted {
		os.Remove(p)
		e.man.Apply(key, rv, 0, 0)
		e.notifyApply(app, rel, true)
		return "applied", nil
	}
	if err := writeFileAtomic(p, body); err != nil {
		return "", err
	}
	fi, _ := os.Stat(p)
	e.man.Apply(key, rv, fi.Size(), fi.ModTime().UnixNano())
	e.notifyApply(app, rel, false)
	return "applied", nil
}

// resolveConcurrent handles a genuine conflict — neither version has seen the
// other. The result carries the pointwise-max vector (dominating both), so
// every node converges; then we push so peers that resolved differently or
// not yet catch up.
func (e *Engine) resolveConcurrent(key, app, rel, p string, body []byte, rv, local Version) (string, error) {
	max := Version{Vec: MaxVec(local.Vec, rv.Vec)}

	// delete vs. live: the live side always survives, so a stale tombstone
	// never deletes data the deleting node had not seen.
	if local.Deleted != rv.Deleted {
		if rv.Deleted { // we are live, remote deleted -> keep ours
			fi, _ := os.Stat(p)
			e.man.Apply(key, max, sizeOf(fi), mtimeOf(fi))
			go e.pushAll(key)
			return "kept-live", nil
		}
		if err := writeFileAtomic(p, body); err != nil { // we are a tombstone -> restore
			return "", err
		}
		fi, _ := os.Stat(p)
		e.man.Apply(key, max, fi.Size(), fi.ModTime().UnixNano())
		e.notifyApply(app, rel, false)
		go e.pushAll(key)
		return "resurrected", nil
	}
	if local.Deleted { // both tombstones -> converge the vector, stay deleted
		e.man.Apply(key, Version{Vec: max.Vec, Deleted: true}, 0, 0)
		return "stale", nil
	}

	// both live and mergeable: union the records item-by-item
	if Mergeable(key) {
		if out, err, handled := e.mergeConcurrent(key, app, rel, p, body, max); handled {
			return out, err
		}
	}

	// non-mergeable (or unparseable) both-live conflict: deterministic winner
	// by content hash; the loser is preserved under sync-conflicts.
	localBytes, _ := os.ReadFile(p)
	if bytes.Equal(localBytes, body) { // same content, just converge the vector
		fi, _ := os.Stat(p)
		e.man.Apply(key, max, sizeOf(fi), mtimeOf(fi))
		go e.pushAll(key)
		return "stale", nil
	}
	if contentHash(body) > contentHash(localBytes) { // remote wins
		e.backupConflict(app, rel, localBytes)
		if err := writeFileAtomic(p, body); err != nil {
			return "", err
		}
		fi, _ := os.Stat(p)
		e.man.Apply(key, max, fi.Size(), fi.ModTime().UnixNano())
		e.notifyApply(app, rel, false)
		go e.pushAll(key)
		return "resolved", nil
	}
	e.backupConflict(app, rel, body) // local wins; remote copy preserved
	fi, _ := os.Stat(p)
	e.man.Apply(key, max, sizeOf(fi), mtimeOf(fi))
	go e.pushAll(key)
	return "resolved", nil
}

// mergeConcurrent unions two mergeable docs. handled=false when our own copy
// won't parse, so the caller falls through to the content-hash path; a
// remote that won't parse (a legacy v1 blob) never erases our v2 doc.
func (e *Engine) mergeConcurrent(key, app, rel, p string, body []byte, max Version) (string, error, bool) {
	localBytes, err := os.ReadFile(p)
	if err != nil {
		return "", nil, false
	}
	if _, lok := CanonicalFile(key, localBytes); !lok {
		return "", nil, false
	}
	if _, rok := CanonicalFile(key, body); !rok { // keep our parseable content
		fi, _ := os.Stat(p)
		e.man.Apply(key, max, sizeOf(fi), mtimeOf(fi))
		go e.pushAll(key)
		return "kept-live", nil, true
	}
	mergedBytes, ok := MergeFile(key, localBytes, body)
	if !ok {
		return "", nil, false
	}
	if bytes.Equal(mergedBytes, localBytes) { // nothing new from the remote
		fi, _ := os.Stat(p)
		e.man.Apply(key, max, sizeOf(fi), mtimeOf(fi))
		go e.pushAll(key)
		return "stale", nil, true
	}
	if err := writeFileAtomic(p, mergedBytes); err != nil {
		return "", err, true
	}
	fi, _ := os.Stat(p)
	e.man.Apply(key, max, fi.Size(), fi.ModTime().UnixNano())
	e.notifyApply(app, rel, false)
	go e.pushAll(key)
	return "merged", nil, true
}

func sizeOf(fi os.FileInfo) int64 {
	if fi == nil {
		return 0
	}
	return fi.Size()
}

func mtimeOf(fi os.FileInfo) int64 {
	if fi == nil {
		return 0
	}
	return fi.ModTime().UnixNano()
}

func contentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// backupConflict preserves the losing side of a non-mergeable conflict under
// ~/.exe/sync-conflicts/<app>/, outside appdata so it never itself syncs.
func (e *Engine) backupConflict(app, rel string, body []byte) {
	if len(body) == 0 {
		return
	}
	dir := filepath.Join(e.cfg.StateDir, "sync-conflicts", app)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	name := fmt.Sprintf("%s.%d.bak", strings.ReplaceAll(rel, "/", "_"), time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		e.cfg.Logf("peer: conflict backup %s/%s failed: %v", app, rel, err)
		return
	}
	e.cfg.Logf("peer: conflict on %s/%s — losing copy saved to sync-conflicts/%s/%s", app, rel, app, name)
}

func (e *Engine) notifyApply(app, rel string, deleted bool) {
	if e.cfg.OnApply != nil {
		e.cfg.OnApply(app, rel, deleted)
	}
}

func writeFileAtomic(p string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".sync-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(body); err == nil {
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

// ---- reconcile --------------------------------------------------------------

// reconcile diffs manifests with one peer: fetch what it has newer, push
// what we have newer, and adopt any peers it knows that we don't (that's
// what makes a third node joining B also sync with A).
func (e *Engine) reconcile(p *Peer) {
	start := time.Now()
	req, err := http.NewRequest("GET", "http://"+p.Addr+"/v1/peer/manifest", nil)
	if err != nil {
		return
	}
	SignRequest(e.cfg.Self, req, nil, p.ID)
	resp, err := e.client.Do(req)
	if err != nil {
		e.setStatus(p, 0, err)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		e.setStatus(p, 0, fmt.Errorf("HTTP %d", resp.StatusCode))
		return
	}
	var mr manifestResponse
	if err := json.Unmarshal(raw, &mr); err != nil {
		e.setStatus(p, 0, err)
		return
	}
	latency := time.Since(start)
	if mr.ID != p.ID {
		e.setStatus(p, 0, fmt.Errorf("address now answers as %s", mr.ID))
		return
	}
	e.setStatus(p, latency, nil)
	if mr.Name != "" && mr.Name != p.Name {
		e.peers.Upsert(&Peer{ID: p.ID, Name: mr.Name, PubKey: p.PubKey})
	}
	for _, pi := range mr.Peers {
		if _, known := e.peers.Get(pi.ID); !known {
			e.adoptPeer(pi)
		}
	}
	localFiles := e.man.Snapshot()
	for key, rv := range mr.Files {
		if !validKey(key) {
			e.cfg.Logf("peer: %s offered invalid sync key %q — ignored", p.Name, key)
			continue
		}
		lv, ok := localFiles[key]
		// pull anything we don't have, the peer strictly newer, or a
		// concurrent version we must resolve
		if !ok || rv.Dominates(lv) || rv.Concurrent(lv) {
			e.fetchFrom(p, key, rv)
		}
	}
	for key, lv := range localFiles {
		rv, ok := mr.Files[key]
		if !ok || lv.Dominates(rv) || lv.Concurrent(rv) {
			e.pushTo(p, key)
		}
	}
}

func (e *Engine) setStatus(p *Peer, latency time.Duration, err error) {
	e.statusMu.Lock()
	st := e.status[p.ID]
	if st == nil {
		st = &Status{}
		e.status[p.ID] = st
	}
	wasOnline := st.Online
	st.ID, st.Name, st.Addr = p.ID, p.Name, p.Addr
	if err != nil {
		st.Online, st.Error = false, err.Error()
		e.statusMu.Unlock()
		return
	}
	st.Online, st.Error = true, ""
	st.LatencyMS = latency.Milliseconds()
	st.LastSeen = time.Now().UnixMilli()
	e.statusMu.Unlock()
	// A peer that just came back (e.g. a laptop woke) shouldn't wait up to a
	// full reconcile interval — kick an immediate pass. The latency Probe
	// (every 5s while the Join dialog is open) also drives this transition.
	if !wasOnline {
		e.ReconcileNow()
	}
}

// ---- status / latency -------------------------------------------------------

// Probe measures round-trip latency to every peer, cached for 5 s so the
// dialog's poll doesn't hammer the mesh; force bypasses the cache.
func (e *Engine) Probe(force bool) []*Status {
	e.statusMu.Lock()
	fresh := time.Since(e.statusAt) < statusTTL
	e.statusMu.Unlock()
	if !force && fresh {
		return e.statusList()
	}
	var wg sync.WaitGroup
	for _, p := range e.peers.List() {
		wg.Add(1)
		go func(p *Peer) {
			defer wg.Done()
			start := time.Now()
			req, err := http.NewRequest("GET", "http://"+p.Addr+"/v1/peer/ping", nil)
			if err != nil {
				return
			}
			SignRequest(e.cfg.Self, req, nil, p.ID)
			client := &http.Client{Timeout: 4 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				e.setStatus(p, 0, errors.New("unreachable"))
				return
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				e.setStatus(p, 0, fmt.Errorf("HTTP %d", resp.StatusCode))
				return
			}
			e.setStatus(p, time.Since(start), nil)
		}(p)
	}
	wg.Wait()
	e.statusMu.Lock()
	e.statusAt = time.Now()
	e.statusMu.Unlock()
	return e.statusList()
}

func (e *Engine) statusList() []*Status {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	var out []*Status
	for _, p := range e.peers.List() {
		if st, ok := e.status[p.ID]; ok {
			cp := *st
			cp.Name, cp.Addr = p.Name, p.Addr
			out = append(out, &cp)
		} else {
			out = append(out, &Status{ID: p.ID, Name: p.Name, Addr: p.Addr})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name+out[i].ID < out[j].Name+out[j].ID })
	return out
}
