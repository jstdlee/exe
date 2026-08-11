package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version is a per-file version vector: origin node id -> the number of
// writes that node has made to this file. It captures causality, so two
// versions can be compared as one-happened-before-the-other or genuinely
// concurrent. Browser wall clocks never participate — the daemon stamps
// versions itself. MTime is wall time for display only.
//
// A single Lamport counter (the previous design) could not tell a delete
// that causally followed your edit from a delete concurrent with it, so a
// stale tombstone with a high counter would silently delete a fresh node's
// real data. Vectors make that concurrency visible, and the apply logic
// then lets a live file survive a concurrent delete.
type Version struct {
	Vec     map[string]int64 `json:"vec"`
	Deleted bool             `json:"deleted,omitempty"`
	MTime   int64            `json:"mtime"` // unix ms, informational
}

func cloneVec(v map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(v))
	for k, c := range v {
		if c != 0 {
			out[k] = c
		}
	}
	return out
}

// vecLeq reports a <= b pointwise (missing entries count as 0).
func vecLeq(a, b map[string]int64) bool {
	for o, c := range a {
		if b[o] < c {
			return false
		}
	}
	return true
}

// MaxVec returns the pointwise maximum of two vectors — the version that has
// seen everything both inputs have seen.
func MaxVec(a, b map[string]int64) map[string]int64 {
	out := cloneVec(a)
	for o, c := range b {
		if c > out[o] {
			out[o] = c
		}
	}
	return out
}

// DominatesOrEqual reports whether v has seen everything o has (v happened
// at-or-after o).
func (v Version) DominatesOrEqual(o Version) bool { return vecLeq(o.Vec, v.Vec) }

// Dominates reports whether v strictly supersedes o (v knows everything o
// knows and strictly more).
func (v Version) Dominates(o Version) bool {
	return vecLeq(o.Vec, v.Vec) && !vecLeq(v.Vec, o.Vec)
}

// Concurrent reports that neither version has seen the other — a genuine
// conflict that must be merged or resolved rather than ordered.
func (v Version) Concurrent(o Version) bool {
	return !vecLeq(o.Vec, v.Vec) && !vecLeq(v.Vec, o.Vec)
}

// SameVersion reports vector identity (ignoring informational MTime).
func (v Version) SameVersion(o Version) bool {
	return vecLeq(v.Vec, o.Vec) && vecLeq(o.Vec, v.Vec) && v.Deleted == o.Deleted
}

// fileState is a manifest entry: the version plus a filesystem fingerprint
// so writes that bypassed the API (agents editing ~/.exe/appdata directly)
// are detected on the next scan and versioned like any other local write.
type fileState struct {
	Version
	Size      int64 `json:"size"`
	DiskMTime int64 `json:"disk_mtime"` // unix ns of the file when stamped
}

// Manifest persists per-file versions in ~/.exe/sync-manifest.json —
// outside appdata, so it never syncs itself.
type Manifest struct {
	path  string
	mu    sync.Mutex
	files map[string]*fileState
}

func LoadManifest(stateDir string) (*Manifest, error) {
	m := &Manifest{path: filepath.Join(stateDir, "sync-manifest.json"), files: map[string]*fileState{}}
	b, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return m, nil
	} else if err != nil {
		return nil, err
	}
	// Load struct accepts both the new vector form and the legacy single-
	// counter form ({ctr, origin}), migrating the latter to a one-entry
	// vector so an in-place upgrade keeps its history.
	var f struct {
		Files map[string]struct {
			Vec       map[string]int64 `json:"vec"`
			Ctr       int64            `json:"ctr"`
			Origin    string           `json:"origin"`
			Deleted   bool             `json:"deleted"`
			MTime     int64            `json:"mtime"`
			Size      int64            `json:"size"`
			DiskMTime int64            `json:"disk_mtime"`
		} `json:"files"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for k, e := range f.Files {
		vec := e.Vec
		if len(vec) == 0 && e.Origin != "" && e.Ctr > 0 {
			vec = map[string]int64{e.Origin: e.Ctr}
		}
		if vec == nil {
			vec = map[string]int64{}
		}
		m.files[k] = &fileState{
			Version:   Version{Vec: vec, Deleted: e.Deleted, MTime: e.MTime},
			Size:      e.Size,
			DiskMTime: e.DiskMTime,
		}
	}
	return m, nil
}

func (m *Manifest) saveLocked() {
	b, err := json.MarshalIndent(map[string]any{"files": m.files}, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".sync-manifest-*")
	if err != nil {
		return
	}
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), m.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
}

func (m *Manifest) Get(key string) (Version, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.files[key]; ok {
		return st.Version, true
	}
	return Version{}, false
}

// Bump records a genuine local write: this node's own component advances,
// producing a version that dominates everything previously known for the
// file.
func (m *Manifest) Bump(key, origin string, deleted bool, size, diskMTime int64) Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.files[key]
	if st == nil {
		st = &fileState{Version: Version{Vec: map[string]int64{}}}
		m.files[key] = st
	}
	vec := cloneVec(st.Vec)
	vec[origin]++
	st.Version = Version{Vec: vec, Deleted: deleted, MTime: time.Now().UnixMilli()}
	st.Size, st.DiskMTime = size, diskMTime
	m.saveLocked()
	return st.Version
}

// Apply records a version arrived at by adopting or resolving a remote one:
// the vector is taken verbatim (a conflict resolution passes the pointwise
// max, which dominates both inputs, so both nodes converge on the same
// vector without a divergent self-increment).
func (m *Manifest) Apply(key string, v Version, size, diskMTime int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.files[key]
	if st == nil {
		st = &fileState{}
		m.files[key] = st
	}
	st.Version = Version{Vec: cloneVec(v.Vec), Deleted: v.Deleted, MTime: time.Now().UnixMilli()}
	st.Size, st.DiskMTime = size, diskMTime
	m.saveLocked()
}

// Fingerprint reports whether the on-disk stat still matches the recorded
// state; false means the file changed behind the API's back.
func (m *Manifest) Fingerprint(key string, size, diskMTime int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.files[key]
	return ok && st.Size == size && st.DiskMTime == diskMTime
}

// Snapshot is the version map peers diff against during reconcile.
func (m *Manifest) Snapshot() map[string]Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Version, len(m.files))
	for k, st := range m.files {
		out[k] = st.Version
	}
	return out
}
