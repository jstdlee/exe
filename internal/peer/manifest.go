package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version orders writes of one file across nodes: a per-file Lamport
// counter with the origin node as tiebreak. Browser wall clocks never
// participate — the daemon stamps versions itself, so skew between
// machines can't reorder history. MTime is wall time for display only.
type Version struct {
	Ctr     int64  `json:"ctr"`
	Origin  string `json:"origin"`
	Deleted bool   `json:"deleted,omitempty"`
	MTime   int64  `json:"mtime"` // unix ms, informational
}

// Newer reports whether v should replace o.
func (v Version) Newer(o Version) bool {
	if v.Ctr != o.Ctr {
		return v.Ctr > o.Ctr
	}
	return v.Origin > o.Origin
}

// Same reports version identity, ignoring the informational MTime.
func (v Version) Same(o Version) bool {
	return v.Ctr == o.Ctr && v.Origin == o.Origin && v.Deleted == o.Deleted
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
	var f struct {
		Files map[string]*fileState `json:"files"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Files != nil {
		m.files = f.Files
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

// Bump records a local write: the file's counter advances past everything
// this node has seen for it, with this node as origin.
func (m *Manifest) Bump(key, origin string, deleted bool, size, diskMTime int64) Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.files[key]
	if st == nil {
		st = &fileState{}
		m.files[key] = st
	}
	st.Version = Version{Ctr: st.Ctr + 1, Origin: origin, Deleted: deleted, MTime: time.Now().UnixMilli()}
	st.Size, st.DiskMTime = size, diskMTime
	m.saveLocked()
	return st.Version
}

// Claim sets an explicit local version — used after a merge, where the
// result must order after BOTH inputs: ctr = max(local, remote)+1.
func (m *Manifest) Claim(key, origin string, ctr int64, deleted bool, size, diskMTime int64) Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.files[key]
	if st == nil {
		st = &fileState{}
		m.files[key] = st
	}
	st.Version = Version{Ctr: ctr, Origin: origin, Deleted: deleted, MTime: time.Now().UnixMilli()}
	st.Size, st.DiskMTime = size, diskMTime
	m.saveLocked()
	return st.Version
}

// Apply records a remote version we adopted verbatim.
func (m *Manifest) Apply(key string, v Version, size, diskMTime int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.files[key]
	if st == nil {
		st = &fileState{}
		m.files[key] = st
	}
	st.Version = v
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
