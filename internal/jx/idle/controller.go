package idle

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"exe/internal/jx/lease"
)

// VM is what the controller needs to know of a VM.
type VM struct {
	Name     string
	State    string // "running", "stopped", ...
	MemoryMB int
}

// Manager is the VM boundary: list and stop.
type Manager interface {
	List(ctx context.Context) ([]VM, error)
	Stop(ctx context.Context, name string) error
}

// Memory is the host's memory, in MB.
type Memory struct {
	TotalMB     int
	AvailableMB int
}

// Stop reasons passed to OnStop.
const (
	ReasonIdle   = "idle"
	ReasonMemory = "memory"
)

// Controller stops idle VMs (Tick) and makes room in memory (MakeRoom).
type Controller struct {
	store  *Store
	leases *lease.Table
	vms    Manager

	// Mem reads host memory; nil or a zero total turns the guard off.
	Mem func() Memory
	// Logf logs (log.Printf by default).
	Logf func(format string, args ...any)
	// OnWarn is called once per idle countdown, warn minutes before stopAt.
	OnWarn func(vm string, stopAt time.Time)
	// OnStop is called after the controller stopped vm (ReasonIdle or
	// ReasonMemory); idleFor is how long it had been idle.
	OnStop func(vm, reason string, idleFor time.Duration)
	// Settle is how long MakeRoom waits after a stop before it reads
	// memory again (the host needs a moment to reclaim the guest's pages).
	Settle time.Duration
	// StopTimeout bounds one graceful stop.
	StopTimeout time.Duration

	mu       sync.Mutex
	warned   map[string]time.Time // vm -> the stopAt it was warned about
	running  map[string]bool      // VMs running at the last Tick
	guardMu  sync.Mutex           // one memory check at a time
	memNoted bool
}

// NewController returns a controller over the policy store, the lease
// table and the VM manager.
func NewController(store *Store, leases *lease.Table, vms Manager) *Controller {
	return &Controller{
		store: store, leases: leases, vms: vms,
		Logf: log.Printf, Settle: 2 * time.Second, StopTimeout: 2 * time.Minute,
		warned: map[string]time.Time{}, running: map[string]bool{},
	}
}

// Store is the controller's policy store.
func (c *Controller) Store() *Store { return c.store }

// Row is one VM in GET /v1/jx/vms.
type Row struct {
	VM               string         `json:"vm"`
	State            string         `json:"state"`
	MemoryMB         int            `json:"memory_mb"`
	Kind             string         `json:"kind"`
	Pinned           bool           `json:"pinned"`
	IdleMinutes      *int           `json:"idle_minutes"`
	EffectiveMinutes int            `json:"effective_minutes"`
	Holders          []lease.Holder `json:"holders"`
	LastBusy         *time.Time     `json:"last_busy"`
	IdleSeconds      int64          `json:"idle_seconds"`
	StopAt           *time.Time     `json:"stop_at,omitempty"`
}

// Row describes vm at now.
func (c *Controller) Row(vm VM, now time.Time) Row {
	p := c.store.Policy(vm.Name)
	set := c.store.Settings()
	st := c.leases.Get(vm.Name)
	r := Row{
		VM: vm.Name, State: vm.State, MemoryMB: vm.MemoryMB, Kind: p.Kind, Pinned: p.Pinned,
		IdleMinutes: p.IdleMinutes, EffectiveMinutes: set.Limit(p), Holders: st.Holders,
	}
	if !st.LastBusy.IsZero() {
		lb := st.LastBusy
		r.LastBusy = &lb
	}
	if vm.State == "running" {
		if d, ok := st.Idle(now); ok {
			r.IdleSeconds = int64(d / time.Second)
		}
		if at, ok := stopAt(p, set, st); ok {
			r.StopAt = &at
		}
	}
	return r
}

// Rows describes every VM, by name.
func (c *Controller) Rows(ctx context.Context, now time.Time) ([]Row, error) {
	vms, err := c.vms.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
	rows := make([]Row, 0, len(vms))
	for _, v := range vms {
		rows = append(rows, c.Row(v, now))
	}
	return rows, nil
}

// stopAt is when an idle VM is due to stop; false while it is busy, pinned
// or has no limit.
func stopAt(p Policy, set Settings, st lease.Status) (time.Time, bool) {
	limit := set.Limit(p)
	if p.Pinned || limit <= 0 || len(st.Holders) > 0 || st.LastBusy.IsZero() {
		return time.Time{}, false
	}
	return st.LastBusy.Add(time.Duration(limit) * time.Minute), true
}

// Tick looks at every running VM once: it stops the ones idle past their
// limit and warns the ones about to be. A VM that was not running at the
// last Tick (the daemon just started, or something started it without
// touching the lease table) starts its idle clock now, so an old last-busy
// time never stops a VM that has just booted.
func (c *Controller) Tick(ctx context.Context, now time.Time) {
	vms, err := c.vms.List(ctx)
	if err != nil {
		c.Logf("idle: list VMs: %v", err)
		return
	}
	set := c.store.Settings()
	live := map[string]bool{}
	c.mu.Lock()
	was := c.running
	c.mu.Unlock()
	for _, vm := range vms {
		if vm.State != "running" {
			continue
		}
		live[vm.Name] = true
		st := c.leases.Get(vm.Name)
		if !was[vm.Name] || st.LastBusy.IsZero() && len(st.Holders) == 0 {
			c.leases.Touch(vm.Name)
			continue
		}
		p := c.store.Policy(vm.Name)
		at, ok := stopAt(p, set, st)
		if !ok {
			c.unwarn(vm.Name)
			continue
		}
		if !now.Before(at) {
			// A session may have opened since we looked.
			if again := c.leases.Get(vm.Name); len(again.Holders) > 0 || !again.LastBusy.Equal(st.LastBusy) {
				continue
			}
			idleFor := now.Sub(st.LastBusy)
			if err := c.stop(ctx, vm.Name); err != nil {
				c.Logf("idle: stop %s: %v", vm.Name, err)
				continue
			}
			c.unwarn(vm.Name)
			c.Logf("idle: stopped %s (%s VM, idle %s, limit %d min)", vm.Name, p.Kind, idleFor.Round(time.Second), set.Limit(p))
			if c.OnStop != nil {
				c.OnStop(vm.Name, ReasonIdle, idleFor)
			}
			continue
		}
		warn := time.Duration(set.WarnMinutes) * time.Minute
		if warn <= 0 || now.Before(at.Add(-warn)) {
			continue
		}
		c.mu.Lock()
		already := c.warned[vm.Name].Equal(at)
		c.warned[vm.Name] = at
		c.mu.Unlock()
		if !already {
			c.Logf("idle: %s stops at %s unless used", vm.Name, at.Format(time.Kitchen))
			if c.OnWarn != nil {
				c.OnWarn(vm.Name, at)
			}
		}
	}
	c.mu.Lock()
	for name := range c.warned {
		if !live[name] {
			delete(c.warned, name)
		}
	}
	c.running = live
	c.mu.Unlock()
}

func (c *Controller) unwarn(vm string) {
	c.mu.Lock()
	delete(c.warned, vm)
	c.mu.Unlock()
}

func (c *Controller) stop(ctx context.Context, vm string) error {
	if c.StopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.StopTimeout)
		defer cancel()
	}
	return c.vms.Stop(ctx, vm)
}

// Loop runs Tick every interval until ctx ends.
func (c *Controller) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			c.Tick(ctx, now)
		}
	}
}

// Victims are the VMs the memory guard may stop to make room for exclude:
// running, no lease holders, not pinned, not a service; least recently
// busy first.
func (c *Controller) Victims(ctx context.Context, exclude string) ([]VM, error) {
	vms, err := c.vms.List(ctx)
	if err != nil {
		return nil, err
	}
	type cand struct {
		vm   VM
		last time.Time
	}
	var cs []cand
	for _, vm := range vms {
		if vm.State != "running" || vm.Name == exclude {
			continue
		}
		p := c.store.Policy(vm.Name)
		st := c.leases.Get(vm.Name)
		if p.Pinned || p.Kind == KindService || len(st.Holders) > 0 {
			continue
		}
		cs = append(cs, cand{vm, st.LastBusy})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if !cs[i].last.Equal(cs[j].last) {
			return cs[i].last.Before(cs[j].last)
		}
		return cs[i].vm.Name < cs[j].vm.Name
	})
	out := make([]VM, len(cs))
	for i, x := range cs {
		out[i] = x.vm
	}
	return out, nil
}

// Report is GET /v1/jx/memory.
type Report struct {
	TotalMB     int `json:"total_mb"`
	UsedMB      int `json:"used_mb"`
	AvailableMB int `json:"available_mb"`
	CapPercent  int `json:"cap_percent"`
	CapMB       int `json:"cap_mb"`
}

// MemoryReport reads host memory against the cap.
func (c *Controller) MemoryReport() Report {
	var m Memory
	if c.Mem != nil {
		m = c.Mem()
	}
	pct := c.store.Settings().MemoryCapPercent
	return Report{
		TotalMB: m.TotalMB, UsedMB: m.TotalMB - m.AvailableMB, AvailableMB: m.AvailableMB,
		CapPercent: pct, CapMB: m.TotalMB * pct / 100,
	}
}

// NoRoomError says why the guard refused a start.
type NoRoomError struct {
	VM                      string
	NeedMB, UsedMB, TotalMB int
	CapPercent, CapMB       int
	Stopped                 []string
}

func (e *NoRoomError) Error() string {
	msg := fmt.Sprintf("not enough memory to start %s (%d MB): the host would use %d of %d MB, over the %d%% cap (%d MB), and no idle VM is left to stop",
		e.VM, e.NeedMB, e.UsedMB+e.NeedMB, e.TotalMB, e.CapPercent, e.CapMB)
	if len(e.Stopped) > 0 {
		msg += " (stopped " + strings.Join(e.Stopped, ", ") + ")"
	}
	return msg + "; stop a VM or raise memory_cap_percent"
}

// ErrNoRoom matches a *NoRoomError with errors.Is.
var ErrNoRoom = errors.New("not enough memory")

func (e *NoRoomError) Is(target error) bool { return target == ErrNoRoom }

// MakeRoom checks that starting vm, which needs needMB, keeps host memory
// use under the cap. If not, it stops victims one at a time, reading
// memory again after each, until the start fits; if it never does, it
// returns a *NoRoomError. A cap of 0, needMB <= 0 or no memory reading
// lets every start through.
func (c *Controller) MakeRoom(ctx context.Context, vm string, needMB int) error {
	c.guardMu.Lock()
	defer c.guardMu.Unlock()
	pct := c.store.Settings().MemoryCapPercent
	if pct <= 0 || needMB <= 0 || c.Mem == nil {
		return nil
	}
	m := c.Mem()
	if m.TotalMB <= 0 {
		if !c.memNoted {
			c.memNoted = true
			c.Logf("memory guard off: this host reports no memory figures")
		}
		return nil
	}
	capMB := m.TotalMB * pct / 100
	tried := map[string]bool{}
	var stopped []string
	for {
		used := m.TotalMB - m.AvailableMB
		if used+needMB <= capMB {
			if len(stopped) > 0 {
				c.Logf("memory guard: room for %s after stopping %s", vm, strings.Join(stopped, ", "))
			}
			return nil
		}
		vs, err := c.Victims(ctx, vm)
		if err != nil {
			return err
		}
		var v *VM
		for i := range vs {
			if !tried[vs[i].Name] {
				v = &vs[i]
				break
			}
		}
		if v == nil {
			err := &NoRoomError{VM: vm, NeedMB: needMB, UsedMB: used, TotalMB: m.TotalMB, CapPercent: pct, CapMB: capMB, Stopped: stopped}
			c.Logf("memory guard: %v", err)
			return err
		}
		tried[v.Name] = true
		idleFor := time.Duration(0)
		if d, ok := c.leases.Get(v.Name).Idle(time.Now()); ok {
			idleFor = d
		}
		if err := c.stop(ctx, v.Name); err != nil {
			c.Logf("memory guard: stop %s: %v", v.Name, err)
			continue
		}
		stopped = append(stopped, v.Name)
		c.Logf("memory guard: stopped %s (%d MB) to make room for %s (%d MB)", v.Name, v.MemoryMB, vm, needMB)
		if c.OnStop != nil {
			c.OnStop(v.Name, ReasonMemory, idleFor)
		}
		if c.Settle > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.Settle):
			}
		}
		m = c.Mem()
	}
}

// WaitFor calls probe every interval until it returns nil or ctx ends; then
// it returns ctx's error together with the last probe error.
func WaitFor(ctx context.Context, interval time.Duration, probe func(ctx context.Context) error) error {
	for {
		err := probe(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last: %v)", ctx.Err(), err)
		case <-time.After(interval):
		}
	}
}
