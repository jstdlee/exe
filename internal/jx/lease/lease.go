// Package lease tracks what keeps a VM busy. A lease is held by anything
// that needs the VM up: an open terminal or SSH session, an agent turn,
// an env job, a cron run, a pin. A VM is idle only while it holds no lease;
// its idle time counts from the moment the last lease was released (or from
// the last Touch, for traffic that has no open/close, like proxy requests).
package lease

import (
	"sort"
	"sync"
	"time"
)

// Holder describes one live lease.
type Holder struct {
	ID     uint64    `json:"id"`
	Reason string    `json:"reason"` // "terminal", "ssh", "board", "env", "cron", "pin", ...
	Since  time.Time `json:"since"`
}

// Status is a VM's lease state.
type Status struct {
	VM       string    `json:"vm"`
	Holders  []Holder  `json:"holders"`
	LastBusy time.Time `json:"last_busy"` // last release or touch; zero = never seen
}

// Idle reports how long the VM has held no lease, at now. A VM with holders
// is never idle (0, false).
func (st Status) Idle(now time.Time) (time.Duration, bool) {
	if len(st.Holders) > 0 || st.LastBusy.IsZero() {
		return 0, false
	}
	return now.Sub(st.LastBusy), true
}

type vmState struct {
	holders  map[uint64]Holder
	lastBusy time.Time
}

// Table is safe for concurrent use. The zero value is not usable; use New.
type Table struct {
	mu   sync.Mutex
	next uint64
	vms  map[string]*vmState
	now  func() time.Time
}

func New() *Table { return &Table{vms: map[string]*vmState{}, now: time.Now} }

// SetClock replaces time.Now (tests).
func (t *Table) SetClock(now func() time.Time) { t.mu.Lock(); t.now = now; t.mu.Unlock() }

func (t *Table) state(vm string) *vmState {
	st := t.vms[vm]
	if st == nil {
		st = &vmState{holders: map[uint64]Holder{}}
		t.vms[vm] = st
	}
	return st
}

// Acquire takes a lease on vm and returns its release func. Release is
// idempotent, so `defer t.Acquire(vm, "x")()` is always safe.
func (t *Table) Acquire(vm, reason string) (release func()) {
	t.mu.Lock()
	t.next++
	id := t.next
	now := t.now()
	st := t.state(vm)
	st.holders[id] = Holder{ID: id, Reason: reason, Since: now}
	st.lastBusy = now
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			st := t.state(vm)
			delete(st.holders, id)
			st.lastBusy = t.now()
		})
	}
}

// Touch marks vm busy now without holding it (proxy traffic, a VM start).
func (t *Table) Touch(vm string) {
	t.mu.Lock()
	t.state(vm).lastBusy = t.now()
	t.mu.Unlock()
}

// Forget drops vm's state (VM deleted). Live holders' releases stay safe.
func (t *Table) Forget(vm string) {
	t.mu.Lock()
	delete(t.vms, vm)
	t.mu.Unlock()
}

// Get returns vm's status.
func (t *Table) Get(vm string) Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status(vm)
}

func (t *Table) status(vm string) Status {
	st := Status{VM: vm, Holders: []Holder{}}
	if s := t.vms[vm]; s != nil {
		for _, h := range s.holders {
			st.Holders = append(st.Holders, h)
		}
		sort.Slice(st.Holders, func(i, j int) bool { return st.Holders[i].ID < st.Holders[j].ID })
		st.LastBusy = s.lastBusy
	}
	return st
}

// All returns every known VM's status, by name.
func (t *Table) All() []Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Status, 0, len(t.vms))
	for vm := range t.vms {
		out = append(out, t.status(vm))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VM < out[j].VM })
	return out
}
