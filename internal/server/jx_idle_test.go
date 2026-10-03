package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"exe/internal/config"
	"exe/internal/jx/idle"
	"exe/internal/vmm"
)

// jxFakeVMs is an in-memory vmm.Manager for the jx tests.
type jxFakeVMs struct {
	mu      sync.Mutex
	vms     map[string]*vmm.Info
	started []string
	stopped []string
	onStop  func(name string)
}

func newJXFakeVMs(vms ...vmm.Info) *jxFakeVMs {
	f := &jxFakeVMs{vms: map[string]*vmm.Info{}}
	for i := range vms {
		v := vms[i]
		f.vms[v.Name] = &v
	}
	return f
}

func (f *jxFakeVMs) Create(ctx context.Context, spec vmm.Spec) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := &vmm.Info{Name: spec.Name, State: "running", MemoryMB: spec.MemoryMB}
	f.vms[spec.Name] = v
	cp := *v
	return &cp, nil
}

func (f *jxFakeVMs) Start(ctx context.Context, name string) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	v.State = "running"
	f.started = append(f.started, name)
	cp := *v
	return &cp, nil
}

func (f *jxFakeVMs) Stop(ctx context.Context, name string) error {
	f.mu.Lock()
	v, ok := f.vms[name]
	if !ok {
		f.mu.Unlock()
		return vmm.ErrNotFound
	}
	v.State = "stopped"
	f.stopped = append(f.stopped, name)
	cb := f.onStop
	f.mu.Unlock()
	if cb != nil {
		cb(name)
	}
	return nil
}

func (f *jxFakeVMs) Delete(ctx context.Context, name string) error { return nil }

func (f *jxFakeVMs) List(ctx context.Context) ([]*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*vmm.Info
	for _, v := range f.vms {
		cp := *v
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *jxFakeVMs) Get(ctx context.Context, name string) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

const jxTestToken = "jx-test-token"

// newJXTestServer builds a daemon on fake VMs with its state in a temp dir
// and no background loops.
func newJXTestServer(t *testing.T, vms *jxFakeVMs) (*Server, *httptest.Server) {
	t.Helper()
	prev := jxStartLoops
	jxStartLoops = false
	t.Cleanup(func() { jxStartLoops = prev })
	var mgr vmm.Manager
	if vms != nil {
		mgr = vms
	}
	s := New(&config.Config{APIToken: jxTestToken, DefaultMemoryMB: 2048}, mgr, nil, "", t.TempDir())
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

// jxDo sends a request with the token and decodes a JSON answer into out.
func jxDo(t *testing.T, ts *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+jxTestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func TestJXIdleSettingsAPI(t *testing.T) {
	_, ts := newJXTestServer(t, newJXFakeVMs())
	var set idle.Settings
	if c := jxDo(t, ts, "GET", "/v1/jx/settings", nil, &set); c != 200 || set != idle.DefaultSettings() {
		t.Fatalf("GET settings %d %+v", c, set)
	}
	if c := jxDo(t, ts, "PUT", "/v1/jx/settings", `{"warn_minutes": 5, "idle_defaults": {"dev": 30}}`, &set); c != 200 {
		t.Fatalf("PUT settings %d", c)
	}
	want := idle.DefaultSettings()
	want.WarnMinutes, want.IdleDefaults.Dev = 5, 30
	if set != want {
		t.Fatalf("merged settings %+v, want %+v", set, want)
	}
	if c := jxDo(t, ts, "PUT", "/v1/jx/settings", `{"memory_cap_percent": 150}`, nil); c != 400 {
		t.Fatalf("cap 150: %d", c)
	}
	jxDo(t, ts, "GET", "/v1/jx/settings", nil, &set)
	if set != want {
		t.Fatalf("a refused PUT changed the settings: %+v", set)
	}
	if c := jxDo(t, ts, "GET", "/v1/jx/settings", nil, nil); c != 200 {
		t.Fatal(c)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/v1/jx/settings", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Fatalf("no token: %v %v", resp.StatusCode, err)
	}
}

func TestJXIdleVMsAPI(t *testing.T) {
	s, ts := newJXTestServer(t, newJXFakeVMs(
		vmm.Info{Name: "a", State: "running", MemoryMB: 2048},
		vmm.Info{Name: "b", State: "stopped", MemoryMB: 1024},
	))
	s.Leases().Touch("a")
	var rows []idle.Row
	if c := jxDo(t, ts, "GET", "/v1/jx/vms", nil, &rows); c != 200 || len(rows) != 2 {
		t.Fatalf("GET vms %d %+v", c, rows)
	}
	a, b := rows[0], rows[1]
	if a.VM != "a" || a.Kind != "dev" || a.EffectiveMinutes != 60 || a.StopAt == nil || a.LastBusy == nil || a.IdleMinutes != nil {
		t.Fatalf("row a = %+v", a)
	}
	if b.StopAt != nil || b.State != "stopped" {
		t.Fatalf("row b = %+v", b)
	}

	var row idle.Row
	if c := jxDo(t, ts, "PUT", "/v1/jx/vms/a", `{"kind": "agent", "pinned": true, "idle_minutes": 7}`, &row); c != 200 {
		t.Fatalf("PUT %d", c)
	}
	if row.Kind != "agent" || !row.Pinned || row.IdleMinutes == nil || *row.IdleMinutes != 7 || row.EffectiveMinutes != 7 || row.StopAt != nil {
		t.Fatalf("after PUT %+v", row)
	}
	if len(row.Holders) != 1 || row.Holders[0].Reason != "pin" {
		t.Fatalf("pinned VM holders = %+v", row.Holders)
	}
	jxDo(t, ts, "PUT", "/v1/jx/vms/a", `{"pinned": false, "idle_minutes": null}`, &row)
	if row.Pinned || row.IdleMinutes != nil || row.EffectiveMinutes != 20 || len(row.Holders) != 0 || row.StopAt == nil || row.Kind != "agent" {
		t.Fatalf("after unpin %+v", row)
	}
	for _, bad := range []string{`{"kind": "pet"}`, `{"idle_minutes": -3}`, `{"colour": "red"}`, `{"pinned": "yes"}`} {
		if c := jxDo(t, ts, "PUT", "/v1/jx/vms/a", bad, nil); c != 400 {
			t.Errorf("PUT %s: %d", bad, c)
		}
	}
	if c := jxDo(t, ts, "PUT", "/v1/jx/vms/ghost", `{"kind": "job"}`, nil); c != 404 {
		t.Fatalf("PUT unknown VM: %d", c)
	}
	if c := jxDo(t, ts, "POST", "/v1/jx/vms/b/keep", nil, nil); c != 204 {
		t.Fatalf("keep: %d", c)
	}
	if c := jxDo(t, ts, "POST", "/v1/jx/vms/ghost/keep", nil, nil); c != 404 {
		t.Fatalf("keep unknown: %d", c)
	}

	// A pin survives a daemon restart (the policy file is re-read and the
	// lease taken again).
	jxDo(t, ts, "PUT", "/v1/jx/vms/b", `{"pinned": true}`, nil)
	s2 := New(&config.Config{APIToken: jxTestToken}, s.VMs, nil, "", s.StateDir)
	if h := s2.Leases().Get("b").Holders; len(h) != 0 {
		t.Fatalf("lease before init: %+v", h)
	}
	s2.jxIdleRT()
	if h := s2.Leases().Get("b").Holders; len(h) != 1 || h[0].Reason != "pin" {
		t.Fatalf("pin after restart: %+v", h)
	}
}

func TestJXMemoryGuard(t *testing.T) {
	vms := newJXFakeVMs(
		vmm.Info{Name: "idle1", State: "running", MemoryMB: 4000},
		vmm.Info{Name: "busy", State: "running", MemoryMB: 4000},
		vmm.Info{Name: "cold", State: "stopped", MemoryMB: 4000},
		vmm.Info{Name: "huge", State: "stopped", MemoryMB: 64000},
	)
	s, ts := newJXTestServer(t, vms)
	ctl, err := s.jxIdleCtl()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	avail := 6000 // of 20000: used 14000, cap 80% = 16000
	ctl.Mem = func() idle.Memory {
		mu.Lock()
		defer mu.Unlock()
		return idle.Memory{TotalMB: 20000, AvailableMB: avail}
	}
	ctl.Settle = 0
	vms.onStop = func(string) { mu.Lock(); avail += 4000; mu.Unlock() }
	defer s.JXHold("busy", "terminal")()

	// 14000 + 4000 > 16000: idle1 (no lease) stops, busy (a terminal) stays.
	var info vmm.Info
	if c := jxDo(t, ts, "POST", "/v1/vms/cold/start", nil, &info); c != 200 || info.State != "running" {
		t.Fatalf("start cold: %d %+v", c, info)
	}
	if strings.Join(vms.stopped, ",") != "idle1" {
		t.Fatalf("stopped %v, want idle1", vms.stopped)
	}
	mu.Lock()
	avail -= 4000 // cold now uses its memory
	mu.Unlock()

	// Even after stopping cold, huge does not fit: 507, and it says so.
	var e struct{ Error string }
	if c := jxDo(t, ts, "POST", "/v1/vms/huge/start", nil, &e); c != http.StatusInsufficientStorage ||
		!strings.Contains(e.Error, "not enough memory to start huge") || !strings.Contains(e.Error, "stopped cold") {
		t.Fatalf("start huge: %d %q", c, e.Error)
	}
	if strings.Join(vms.stopped, ",") != "idle1,cold" {
		t.Fatalf("stopped %v", vms.stopped)
	}
	if c := jxDo(t, ts, "POST", "/v1/vms", vmm.Spec{Name: "new", MemoryMB: 50000}, &e); c != http.StatusInsufficientStorage {
		t.Fatalf("create big: %d %q", c, e.Error)
	}
	// The guard reads the create body and hands it on intact; the default
	// memory (2048) fits: 10000 + 2048 <= 16000.
	if c := jxDo(t, ts, "POST", "/v1/vms", vmm.Spec{Name: "small"}, &info); c != 201 || info.Name != "small" || info.MemoryMB != 2048 {
		t.Fatalf("create small: %d %+v", c, info)
	}
	// Starting a running VM never runs the guard.
	if c := jxDo(t, ts, "POST", "/v1/vms/busy/start", nil, &info); c != 200 {
		t.Fatalf("start running: %d", c)
	}
	// Cap 0 = off.
	jxDo(t, ts, "PUT", "/v1/jx/settings", `{"memory_cap_percent": 0}`, nil)
	if c := jxDo(t, ts, "POST", "/v1/vms/huge/start", nil, &info); c != 200 {
		t.Fatalf("start huge with the guard off: %d", c)
	}
	var rep idle.Report
	jxDo(t, ts, "GET", "/v1/jx/memory", nil, &rep)
	if rep.TotalMB != 20000 || rep.CapPercent != 0 || rep.UsedMB+rep.AvailableMB != 20000 {
		t.Fatalf("memory report %+v", rep)
	}
}

func TestJXEnsureVMUpGuardsAndStarts(t *testing.T) {
	vms := newJXFakeVMs(vmm.Info{Name: "x", State: "stopped", MemoryMB: 8000})
	s, _ := newJXTestServer(t, vms)
	ctl, _ := s.jxIdleCtl()
	ctl.Mem = func() idle.Memory { return idle.Memory{TotalMB: 16000, AvailableMB: 4000} }
	err := jxEnsureVMUp(s, context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "not enough memory") || len(vms.started) != 0 {
		t.Fatalf("err = %v, started %v", err, vms.started)
	}
	if err := jxEnsureVMUp(s, context.Background(), "ghost"); err == nil {
		t.Fatal("unknown VM came up")
	}
}

// Deleting a VM drops its policy, so a new VM with the same name starts
// from the defaults instead of inheriting, say, a 1-minute idle limit.
func TestJXDeleteForgetsPolicy(t *testing.T) {
	s, ts := newJXTestServer(t, newJXFakeVMs(vmm.Info{Name: "a", State: "running", MemoryMB: 512}))
	if c := jxDo(t, ts, "PUT", "/v1/jx/vms/a", `{"pinned": true, "idle_minutes": 1}`, nil); c != 200 {
		t.Fatalf("put: %d", c)
	}
	if c := jxDo(t, ts, "DELETE", "/v1/vms/a", nil, nil); c >= 300 {
		t.Fatalf("delete: %d", c)
	}
	ctl, err := s.jxIdleCtl()
	if err != nil {
		t.Fatal(err)
	}
	if p := ctl.Store().Policy("a"); p.Pinned || p.IdleMinutes != nil {
		t.Fatalf("policy survived delete: %+v", p)
	}
	if st := s.Leases().Get("a"); len(st.Holders) != 0 {
		t.Fatalf("pin lease survived delete: %+v", st.Holders)
	}
}
