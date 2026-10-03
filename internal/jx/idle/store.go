// Package idle stops VMs nobody uses and keeps host memory under a cap.
// Per-VM policy (kind, pin, idle limit) and the settings live in two JSON
// files; the Controller reads the lease table (internal/jx/lease) to see
// what is busy. It knows nothing about the server: the VM manager, the
// memory reading and the notifications come in as small interfaces and
// funcs, so tests drive it with fakes and a fake clock.
package idle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// VM kinds. The kind picks the default idle limit.
const (
	KindDev     = "dev"
	KindAgent   = "agent"
	KindService = "service"
	KindJob     = "job"
)

// ValidKind reports whether k is a known kind.
func ValidKind(k string) bool {
	return k == KindDev || k == KindAgent || k == KindService || k == KindJob
}

// Policy is one VM's idle policy.
type Policy struct {
	Kind   string `json:"kind"`
	Pinned bool   `json:"pinned"`
	// IdleMinutes overrides the kind's default; nil = default, 0 = never.
	IdleMinutes *int `json:"idle_minutes"`
}

// IdleDefaults are the idle limits per kind, in minutes (0 = never).
type IdleDefaults struct {
	Dev     int `json:"dev"`
	Agent   int `json:"agent"`
	Service int `json:"service"`
	Job     int `json:"job"`
}

// Settings are the node-wide idle and memory settings.
type Settings struct {
	IdleDefaults     IdleDefaults `json:"idle_defaults"`
	WarnMinutes      int          `json:"warn_minutes"`
	MemoryCapPercent int          `json:"memory_cap_percent"` // 0 = guard off
}

// DefaultSettings are the settings of a node that never changed them.
func DefaultSettings() Settings {
	return Settings{
		IdleDefaults:     IdleDefaults{Dev: 60, Agent: 20, Service: 0, Job: 5},
		WarnMinutes:      2,
		MemoryCapPercent: 80,
	}
}

// Validate rejects negative limits and a cap over 100 percent.
func (s Settings) Validate() error {
	d := s.IdleDefaults
	if d.Dev < 0 || d.Agent < 0 || d.Service < 0 || d.Job < 0 {
		return errors.New("idle_defaults must not be negative")
	}
	if s.WarnMinutes < 0 {
		return errors.New("warn_minutes must not be negative")
	}
	if s.MemoryCapPercent < 0 || s.MemoryCapPercent > 100 {
		return errors.New("memory_cap_percent must be 0 (off) to 100")
	}
	return nil
}

// DefaultMinutes is kind's default idle limit.
func (s Settings) DefaultMinutes(kind string) int {
	switch kind {
	case KindAgent:
		return s.IdleDefaults.Agent
	case KindService:
		return s.IdleDefaults.Service
	case KindJob:
		return s.IdleDefaults.Job
	}
	return s.IdleDefaults.Dev
}

// Limit is p's effective idle limit in minutes (0 = never).
func (s Settings) Limit(p Policy) int {
	if p.IdleMinutes != nil {
		return *p.IdleMinutes
	}
	return s.DefaultMinutes(p.Kind)
}

// Store keeps <dir>/vms.json (policies by VM name) and <dir>/settings.json.
// Safe for concurrent use.
type Store struct {
	dir      string
	mu       sync.Mutex
	policies map[string]Policy
	settings Settings
}

// Open loads the store; missing files mean defaults.
func Open(dir string) (*Store, error) {
	st := &Store{dir: dir, policies: map[string]Policy{}, settings: DefaultSettings()}
	if err := readJSON(st.policiesPath(), &st.policies); err != nil {
		return nil, err
	}
	if st.policies == nil {
		st.policies = map[string]Policy{}
	}
	if err := readJSON(st.settingsPath(), &st.settings); err != nil {
		return nil, err
	}
	return st, nil
}

func (st *Store) policiesPath() string { return filepath.Join(st.dir, "vms.json") }
func (st *Store) settingsPath() string { return filepath.Join(st.dir, "settings.json") }

// Policy returns vm's policy; a VM never configured is a dev VM.
func (st *Store) Policy(vm string) Policy {
	st.mu.Lock()
	defer st.mu.Unlock()
	p, ok := st.policies[vm]
	if !ok || p.Kind == "" {
		p.Kind = KindDev
	}
	return p
}

// Policies returns every configured policy.
func (st *Store) Policies() map[string]Policy {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[string]Policy, len(st.policies))
	for k, v := range st.policies {
		out[k] = v
	}
	return out
}

// SetPolicy stores vm's policy.
func (st *Store) SetPolicy(vm string, p Policy) error {
	if vm == "" {
		return errors.New("vm name required")
	}
	if p.Kind == "" {
		p.Kind = KindDev
	}
	if !ValidKind(p.Kind) {
		return fmt.Errorf("kind must be dev, agent, service or job, not %q", p.Kind)
	}
	if p.IdleMinutes != nil && *p.IdleMinutes < 0 {
		return errors.New("idle_minutes must not be negative")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	next := make(map[string]Policy, len(st.policies)+1)
	for k, v := range st.policies {
		next[k] = v
	}
	next[vm] = p
	if err := writeJSONAtomic(st.policiesPath(), next); err != nil {
		return err
	}
	st.policies = next
	return nil
}

// Settings returns the settings.
func (st *Store) Settings() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.settings
}

// SetSettings validates and stores the settings.
func (st *Store) SetSettings(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := writeJSONAtomic(st.settingsPath(), s); err != nil {
		return err
	}
	st.settings = s
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

// writeJSONAtomic writes v to path through a temp file and a rename, 0600
// in a 0700 directory.
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(b, '\n')) // CreateTemp made it 0600
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
