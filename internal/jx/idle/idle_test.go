package idle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"exe/internal/jx/lease"
)

type fakeVMs struct {
	mu      sync.Mutex
	vms     map[string]*VM
	stopped []string
	onStop  func(name string) // e.g. frees memory
}

func newFakeVMs(vms ...VM) *fakeVMs {
	f := &fakeVMs{vms: map[string]*VM{}}
	for i := range vms {
		v := vms[i]
		f.vms[v.Name] = &v
	}
	return f
}

func (f *fakeVMs) List(ctx context.Context) ([]VM, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []VM
	for _, v := range f.vms {
		out = append(out, *v)
	}
	return out, nil
}

func (f *fakeVMs) Stop(ctx context.Context, name string) error {
	f.mu.Lock()
	f.vms[name].State = "stopped"
	f.stopped = append(f.stopped, name)
	cb := f.onStop
	f.mu.Unlock()
	if cb != nil {
		cb(name)
	}
	return nil
}

type rig struct {
	now    time.Time
	vms    *fakeVMs
	leases *lease.Table
	ctl    *Controller
	warns  []string
	stops  []string
}

func newRig(t *testing.T, vms ...VM) *rig {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{now: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), vms: newFakeVMs(vms...), leases: lease.New()}
	r.leases.SetClock(func() time.Time { return r.now })
	r.ctl = NewController(st, r.leases, r.vms)
	r.ctl.Logf = t.Logf
	r.ctl.Settle = 0
	r.ctl.OnWarn = func(vm string, at time.Time) { r.warns = append(r.warns, vm+"@"+at.Format("15:04")) }
	r.ctl.OnStop = func(vm, reason string, _ time.Duration) { r.stops = append(r.stops, vm+":"+reason) }
	return r
}

func (r *rig) tick(d time.Duration) {
	r.now = r.now.Add(d)
	r.ctl.Tick(context.Background(), r.now)
}

func intp(n int) *int { return &n }

func TestTickWarnThenStop(t *testing.T) {
	r := newRig(t, VM{Name: "a", State: "running"}, VM{Name: "off", State: "stopped"})
	r.tick(0) // first sighting starts the clock at 10:00
	if len(r.vms.stopped) != 0 {
		t.Fatal("stopped on first sighting")
	}
	r.tick(57 * time.Minute) // 10:57: limit 60, warn 2 -> not yet
	if len(r.warns) != 0 {
		t.Fatalf("early warning %v", r.warns)
	}
	r.tick(time.Minute) // 10:58: warn
	r.tick(30 * time.Second)
	if !reflect.DeepEqual(r.warns, []string{"a@11:00"}) {
		t.Fatalf("warns = %v, want one", r.warns)
	}
	row := r.ctl.Row(VM{Name: "a", State: "running"}, r.now)
	if row.StopAt == nil || !row.StopAt.Equal(time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)) || row.EffectiveMinutes != 60 || row.Kind != KindDev {
		t.Fatalf("row = %+v", row)
	}
	r.tick(90 * time.Second) // 11:00
	if !reflect.DeepEqual(r.vms.stopped, []string{"a"}) || !reflect.DeepEqual(r.stops, []string{"a:idle"}) {
		t.Fatalf("stopped %v, notes %v", r.vms.stopped, r.stops)
	}
}

func TestTickActivityResetsCountdown(t *testing.T) {
	r := newRig(t, VM{Name: "a", State: "running"})
	r.ctl.Store().SetPolicy("a", Policy{Kind: KindAgent}) // 20 min
	r.tick(0)
	r.tick(18 * time.Minute)
	if len(r.warns) != 1 {
		t.Fatalf("warns = %v", r.warns)
	}
	r.leases.Touch("a") // proxy traffic
	r.tick(5 * time.Minute)
	if len(r.vms.stopped) != 0 {
		t.Fatal("stopped after activity")
	}
	r.tick(13 * time.Minute) // 18 min since the touch: a new countdown warns again
	if len(r.warns) != 2 {
		t.Fatalf("warns = %v, want a second", r.warns)
	}
	r.tick(2 * time.Minute)
	if len(r.vms.stopped) != 1 {
		t.Fatal("not stopped")
	}
}

func TestTickFreshStartGetsFullLimit(t *testing.T) {
	r := newRig(t, VM{Name: "a", State: "stopped"})
	r.leases.Touch("a") // last busy long ago, then stopped
	r.tick(0)
	r.tick(5 * time.Hour)
	r.vms.vms["a"].State = "running" // started by a path that does not Touch
	r.tick(30 * time.Second)
	if len(r.vms.stopped) != 0 {
		t.Fatal("a VM that just booted was stopped for its old idle time")
	}
	r.tick(59 * time.Minute)
	if len(r.vms.stopped) != 0 {
		t.Fatal("stopped before its limit")
	}
	r.tick(time.Minute)
	if len(r.vms.stopped) != 1 {
		t.Fatal("not stopped after its limit")
	}
}

func TestTickNeverStops(t *testing.T) {
	r := newRig(t,
		VM{Name: "held", State: "running"},
		VM{Name: "pinned", State: "running"},
		VM{Name: "svc", State: "running"},
		VM{Name: "zero", State: "running"},
		VM{Name: "job", State: "running"},
	)
	st := r.ctl.Store()
	st.SetPolicy("pinned", Policy{Kind: KindJob, Pinned: true})
	st.SetPolicy("svc", Policy{Kind: KindService})
	st.SetPolicy("zero", Policy{Kind: KindJob, IdleMinutes: intp(0)})
	release := r.leases.Acquire("held", "terminal")
	r.tick(0)
	r.tick(24 * time.Hour)
	if !reflect.DeepEqual(r.vms.stopped, []string{"job"}) {
		t.Fatalf("stopped %v, want only job", r.vms.stopped)
	}
	release()
	r.tick(59 * time.Minute)
	r.tick(time.Minute)
	if !reflect.DeepEqual(r.vms.stopped, []string{"job", "held"}) {
		t.Fatalf("stopped %v, want held after release + 60 min", r.vms.stopped)
	}
	// warn 0 = no warnings at all
	set := st.Settings()
	set.WarnMinutes = 0
	st.SetSettings(set)
	r2 := newRig(t, VM{Name: "b", State: "running"})
	r2.ctl.Store().SetSettings(set)
	r2.tick(0)
	r2.tick(time.Hour)
	if len(r2.warns) != 0 || len(r2.vms.stopped) != 1 {
		t.Fatalf("warns %v stopped %v", r2.warns, r2.vms.stopped)
	}
}

func TestStorePersistsAtomically(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir)
	if err := st.SetPolicy("a", Policy{Kind: "bogus"}); err == nil {
		t.Fatal("bad kind accepted")
	}
	if err := st.SetPolicy("a", Policy{Kind: KindAgent, IdleMinutes: intp(-1)}); err == nil {
		t.Fatal("negative minutes accepted")
	}
	if err := st.SetPolicy("a", Policy{Kind: KindAgent, Pinned: true, IdleMinutes: intp(7)}); err != nil {
		t.Fatal(err)
	}
	bad := DefaultSettings()
	bad.MemoryCapPercent = 101
	if err := st.SetSettings(bad); err == nil {
		t.Fatal("cap 101 accepted")
	}
	set := DefaultSettings()
	set.IdleDefaults.Dev = 90
	if err := st.SetSettings(set); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"vms.json", "settings.json"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("temp files left: %v", ents)
	}
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := st2.Policy("a")
	if p.Kind != KindAgent || !p.Pinned || *p.IdleMinutes != 7 || st2.Settings().IdleDefaults.Dev != 90 {
		t.Fatalf("reloaded %+v %+v", p, st2.Settings())
	}
	if st2.Policy("new").Kind != KindDev {
		t.Fatal("unknown VM should be dev")
	}
	if got := st2.Settings().Limit(Policy{Kind: KindJob}); got != 5 {
		t.Fatalf("job default = %d", got)
	}
}

func TestVictimOrder(t *testing.T) {
	r := newRig(t,
		VM{Name: "new", State: "running"},
		VM{Name: "old", State: "running"},
		VM{Name: "never", State: "running"},
		VM{Name: "held", State: "running"},
		VM{Name: "pin", State: "running"},
		VM{Name: "svc", State: "running"},
		VM{Name: "off", State: "stopped"},
		VM{Name: "me", State: "running"},
	)
	r.ctl.Store().SetPolicy("pin", Policy{Pinned: true})
	r.ctl.Store().SetPolicy("svc", Policy{Kind: KindService})
	r.leases.Touch("old")
	r.now = r.now.Add(time.Minute)
	r.leases.Touch("new")
	r.leases.Acquire("held", "board")
	got, _ := r.ctl.Victims(context.Background(), "me")
	var names []string
	for _, v := range got {
		names = append(names, v.Name)
	}
	if !reflect.DeepEqual(names, []string{"never", "old", "new"}) {
		t.Fatalf("victims = %v", names)
	}
}

func TestMakeRoom(t *testing.T) {
	r := newRig(t,
		VM{Name: "a", State: "running", MemoryMB: 4000},
		VM{Name: "b", State: "running", MemoryMB: 4000},
		VM{Name: "busy", State: "running", MemoryMB: 4000},
	)
	r.leases.Touch("b")
	r.now = r.now.Add(time.Minute)
	r.leases.Touch("a") // b is the least recently busy
	r.leases.Acquire("busy", "ssh")
	var mu sync.Mutex
	avail := 3000 // of 16000; cap 80% = 12800; used 13000
	r.ctl.Mem = func() Memory { mu.Lock(); defer mu.Unlock(); return Memory{TotalMB: 16000, AvailableMB: avail} }
	r.vms.onStop = func(name string) { mu.Lock(); avail += 4000; mu.Unlock() }

	// 13000 used + 2000 > 12800: stop b (7000 avail -> 9000 used + 2000 fits)
	if err := r.ctl.MakeRoom(context.Background(), "new", 2000); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.vms.stopped, []string{"b"}) || !reflect.DeepEqual(r.stops, []string{"b:memory"}) {
		t.Fatalf("stopped %v notes %v", r.vms.stopped, r.stops)
	}
	// 9000 used + 8000: stop a (5000 used + 8000 = 13000 > 12800), then
	// nothing is left (busy holds a lease): refuse.
	err := r.ctl.MakeRoom(context.Background(), "big", 8000)
	var nr *NoRoomError
	if !errors.As(err, &nr) || !errors.Is(err, ErrNoRoom) {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(nr.Stopped, []string{"a"}) || !strings.Contains(err.Error(), "80% cap") {
		t.Fatalf("NoRoom = %+v: %v", nr, err)
	}
	// Cap 0 = off.
	set := r.ctl.Store().Settings()
	set.MemoryCapPercent = 0
	r.ctl.Store().SetSettings(set)
	if err := r.ctl.MakeRoom(context.Background(), "big", 1<<20); err != nil {
		t.Fatal(err)
	}
	// No memory figures = off.
	set.MemoryCapPercent = 80
	r.ctl.Store().SetSettings(set)
	r.ctl.Mem = func() Memory { return Memory{} }
	if err := r.ctl.MakeRoom(context.Background(), "big", 1<<20); err != nil {
		t.Fatal(err)
	}
	rep := r.ctl.MemoryReport()
	if rep.CapPercent != 80 || rep.TotalMB != 0 {
		t.Fatalf("report %+v", rep)
	}
}

func TestWaitFor(t *testing.T) {
	n := 0
	err := WaitFor(context.Background(), time.Millisecond, func(context.Context) error {
		if n++; n < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("err=%v n=%d", err, n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = WaitFor(ctx, 5*time.Millisecond, func(context.Context) error { return errors.New("no route") })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "no route") {
		t.Fatalf("err = %v", err)
	}
}
