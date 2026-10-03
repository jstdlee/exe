package server

// jx Feature B, part 1: idle stop and the memory guard (internal/jx/idle).
//
// The controller loop starts lazily from this file's route registration
// (registerJX runs once per Handler) and lives as long as the process; it
// has no other start hook. A VM the controller stops is stopped like any
// other, so upstream's autostart record (written at shutdown from the VMs
// still running) never lists it: nothing here touches autostart.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"exe/internal/hostinfo"
	"exe/internal/jx/idle"
	"exe/internal/vmm"
)

const (
	jxIdleEvery   = 30 * time.Second
	jxSSHWaitMax  = 3 * time.Minute
	jxCreateLimit = 1 << 20 // bytes of a POST /v1/vms body the guard reads
)

// jxStartLoops lets tests build a Server without the background loops.
var jxStartLoops = true

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/settings", s.handleJXSettingsGet)
		mux.HandleFunc("PUT /v1/jx/settings", s.handleJXSettingsPut)
		mux.HandleFunc("GET /v1/jx/vms", s.handleJXVMs)
		mux.HandleFunc("PUT /v1/jx/vms/{name}", s.handleJXVMPut)
		mux.HandleFunc("POST /v1/jx/vms/{name}/keep", s.handleJXVMKeep)
		mux.HandleFunc("GET /v1/jx/memory", s.handleJXMemory)
		s.jxIdleStart()
	})
	jxEnsureVMUp = jxEnsureVMUpImpl
}

// jxIdleRuntime is the per-Server idle state. jxState (jx.go) belongs to
// the scaffold, so the feature keeps its own, keyed by Server.
type jxIdleRuntime struct {
	once sync.Once
	ctl  *idle.Controller
	err  error

	loopOnce sync.Once
	startMu  sync.Mutex // one guarded start at a time (jxEnsureVMUp)

	pinMu sync.Mutex
	pins  map[string]func() // vm -> release of its "pin" lease
}

var jxIdleRTs sync.Map // *Server -> *jxIdleRuntime

func (s *Server) jxIdleRT() *jxIdleRuntime {
	v, _ := jxIdleRTs.LoadOrStore(s, &jxIdleRuntime{})
	rt := v.(*jxIdleRuntime)
	rt.once.Do(func() { rt.init(s) })
	return rt
}

func (rt *jxIdleRuntime) init(s *Server) {
	rt.pins = map[string]func(){}
	store, err := idle.Open(s.jxDir())
	if err != nil {
		rt.err = fmt.Errorf("idle settings: %w", err)
		log.Printf("jx: %v", rt.err)
		return
	}
	ctl := idle.NewController(store, s.Leases(), jxIdleVMs{s})
	ctl.Mem = jxHostMemory
	ctl.OnWarn = s.jxIdleWarn
	ctl.OnStop = s.jxIdleStopped
	rt.ctl = ctl
	for vm, p := range store.Policies() {
		if p.Pinned {
			rt.pin(s, vm, true)
		}
	}
}

// jxIdleCtl is the idle controller, or why there is none.
func (s *Server) jxIdleCtl() (*idle.Controller, error) {
	rt := s.jxIdleRT()
	return rt.ctl, rt.err
}

// pin holds a "pin" lease while a VM is pinned, so the lease table (and
// GET /v1/jx/leases) shows why it never idles.
func (rt *jxIdleRuntime) pin(s *Server, vm string, on bool) {
	rt.pinMu.Lock()
	defer rt.pinMu.Unlock()
	release, held := rt.pins[vm]
	switch {
	case on && !held:
		rt.pins[vm] = s.JXHold(vm, "pin")
	case !on && held:
		release()
		delete(rt.pins, vm)
	}
}

// jxIdleStart starts the controller loop once per Server. It runs until
// the process exits. Without a VM backend (tests) there is nothing to do.
func (s *Server) jxIdleStart() {
	if s.VMs == nil || !jxStartLoops {
		return
	}
	rt := s.jxIdleRT()
	if rt.ctl == nil {
		return
	}
	rt.loopOnce.Do(func() { go rt.ctl.Loop(context.Background(), jxIdleEvery) })
}

// jxIdleVMs adapts vmm.Manager to the controller's VM boundary.
type jxIdleVMs struct{ s *Server }

func (a jxIdleVMs) List(ctx context.Context) ([]idle.VM, error) {
	if a.s.VMs == nil {
		return nil, errors.New("no VM backend")
	}
	list, err := a.s.VMs.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]idle.VM, 0, len(list))
	for _, v := range list {
		out = append(out, idle.VM{Name: v.Name, State: v.State, MemoryMB: v.MemoryMB})
	}
	return out, nil
}

func (a jxIdleVMs) Stop(ctx context.Context, name string) error { return a.s.VMs.Stop(ctx, name) }

// jxHostMemory reads host memory. On Linux this is /proc/meminfo's
// MemTotal and MemAvailable; on a GB10 (unified memory) the GPU allocates
// from the same pool, so MemAvailable already counts what models hold.
// macOS and Windows use their own figures (hostinfo); a host that reports
// none turns the guard off with a log line.
func jxHostMemory() idle.Memory {
	m := hostinfo.Mem()
	return idle.Memory{TotalMB: int(m.Total >> 20), AvailableMB: int(m.Available >> 20)}
}

func (s *Server) jxIdleWarn(vm string, stopAt time.Time) {
	mins := int(time.Until(stopAt).Round(time.Minute) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	msg := pushMessage{
		Title: vm + " stops soon",
		Body:  fmt.Sprintf("%s has been idle and stops in about %d min. Use it or press Keep to hold it up.", vm, mins),
		Tag:   "jx-idle-" + vm,
		URL:   "/",
	}
	s.pushMu.Lock()
	subs := len(s.loadPushSubs())
	s.pushMu.Unlock()
	if subs == 0 {
		return // the warning is in the log and in GET /v1/jx/vms (stop_at)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, errs := s.pushAll(ctx, msg); len(errs) > 0 {
			log.Printf("idle: push: %s", strings.Join(errs, "; "))
		}
	}()
}

func (s *Server) jxIdleStopped(vm, reason string, idleFor time.Duration) {
	if reason == idle.ReasonMemory {
		s.PostNews("vm", "VM stopped for memory", vm+" was stopped to make room in memory for another VM. Its disk is kept; start it again any time.")
		return
	}
	s.PostNews("vm", "VM stopped (idle)", fmt.Sprintf("%s was idle for %s and was stopped. Its disk is kept; start it again any time.", vm, jxRoundDur(idleFor)))
}

func jxRoundDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return strings.TrimSuffix(d.String(), "0s")
}

// ---- memory guard ----------------------------------------------------------

// jxMemoryGuard runs in jxWrap before POST /v1/vms and POST
// /v1/vms/{name}/start: if the start would push host memory over the cap
// it stops idle VMs to make room, or answers 507 and returns false.
// Anything it cannot judge (a bad body, an unknown VM) goes through to the
// handler, which reports it.
func (s *Server) jxMemoryGuard(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || s.VMs == nil {
		return true
	}
	var name string
	var need int
	if r.URL.Path == "/v1/vms" {
		body, err := io.ReadAll(io.LimitReader(r.Body, jxCreateLimit))
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		if err != nil {
			return true
		}
		var spec vmm.Spec
		if json.Unmarshal(body, &spec) != nil {
			return true
		}
		s.fillSpec(&spec)
		name, need = spec.Name, spec.MemoryMB
	} else {
		vm, sub, ok := vmFromPath(r.URL.Path)
		if !ok || sub != "start" {
			return true
		}
		info, err := s.VMs.Get(r.Context(), vm)
		if err != nil || info.State == "running" {
			return true
		}
		name, need = vm, info.MemoryMB
	}
	ctl, err := s.jxIdleCtl()
	if err != nil {
		return true
	}
	if err := ctl.MakeRoom(r.Context(), name, need); err != nil {
		if errors.Is(err, idle.ErrNoRoom) {
			writeErr(w, http.StatusInsufficientStorage, err)
			return false
		}
		log.Printf("memory guard: %v (letting the start through)", err)
	}
	return true
}

// jxEnsureVMUpImpl starts vm if it is stopped (memory guard first) and
// waits until its SSH server takes our key.
func jxEnsureVMUpImpl(s *Server, ctx context.Context, vm string) error {
	if s.VMs == nil {
		return errors.New("no VM backend on this node")
	}
	// a snapshot or restore is copying the disk (jx_snap.go)
	if jxSnapBusy(vm) {
		return fmt.Errorf("%s: a snapshot is copying its disk; try again when it is done", vm)
	}
	info, err := s.VMs.Get(ctx, vm)
	if err != nil {
		return err
	}
	if info.State != "running" {
		rt := s.jxIdleRT()
		rt.startMu.Lock()
		info, err = s.VMs.Get(ctx, vm)
		if err == nil && info.State != "running" {
			if rt.ctl != nil {
				err = rt.ctl.MakeRoom(ctx, vm, info.MemoryMB)
			}
			if err == nil {
				log.Printf("jx: starting %s", vm)
				info, err = s.VMs.Start(ctx, vm)
			}
		}
		rt.startMu.Unlock()
		if err != nil {
			return fmt.Errorf("start %s: %w", vm, err)
		}
	}
	s.Leases().Touch(vm)
	return s.jxWaitSSH(ctx, vm)
}

// jxWaitSSH waits (at most jxSSHWaitMax) until vm has an IP and an SSH
// login with the daemon's key succeeds.
func (s *Server) jxWaitSSH(ctx context.Context, vm string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, jxSSHWaitMax)
		defer cancel()
	}
	err := idle.WaitFor(ctx, time.Second, func(ctx context.Context) error {
		info, err := s.runningVM(ctx, vm)
		if err != nil {
			return err
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		c, err := s.vmTarget(info).Dial(dctx)
		if err != nil {
			return err
		}
		return c.Close()
	})
	if err != nil {
		return fmt.Errorf("%s: SSH did not answer: %w", vm, err)
	}
	return nil
}

// ---- handlers --------------------------------------------------------------

func (s *Server) jxIdleCtlOr500(w http.ResponseWriter) *idle.Controller {
	ctl, err := s.jxIdleCtl()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return nil
	}
	return ctl
}

func (s *Server) handleJXSettingsGet(w http.ResponseWriter, r *http.Request) {
	if ctl := s.jxIdleCtlOr500(w); ctl != nil {
		writeJSON(w, http.StatusOK, ctl.Store().Settings())
	}
}

// PUT /v1/jx/settings merges the fields given into the current settings.
func (s *Server) handleJXSettingsPut(w http.ResponseWriter, r *http.Request) {
	ctl := s.jxIdleCtlOr500(w)
	if ctl == nil {
		return
	}
	set := ctl.Store().Settings()
	if err := json.NewDecoder(r.Body).Decode(&set); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := ctl.Store().SetSettings(set); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) handleJXVMs(w http.ResponseWriter, r *http.Request) {
	ctl := s.jxIdleCtlOr500(w)
	if ctl == nil {
		return
	}
	rows, err := ctl.Rows(r.Context(), time.Now())
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// PUT /v1/jx/vms/{name} {kind?, pinned?, idle_minutes?}: fields left out
// keep their value; idle_minutes null goes back to the kind's default.
func (s *Server) handleJXVMPut(w http.ResponseWriter, r *http.Request) {
	ctl := s.jxIdleCtlOr500(w)
	if ctl == nil {
		return
	}
	name := r.PathValue("name")
	info, err := s.jxVMInfo(r.Context(), name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	var req map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p := ctl.Store().Policy(name)
	for k, raw := range req {
		var err error
		switch k {
		case "kind":
			err = json.Unmarshal(raw, &p.Kind)
		case "pinned":
			err = json.Unmarshal(raw, &p.Pinned)
		case "idle_minutes":
			p.IdleMinutes = nil
			if string(raw) != "null" {
				err = json.Unmarshal(raw, &p.IdleMinutes)
			}
		default:
			err = fmt.Errorf("unknown field %q (want kind, pinned, idle_minutes)", k)
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("%s: %w", k, err))
			return
		}
	}
	if err := ctl.Store().SetPolicy(name, p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.jxIdleRT().pin(s, name, p.Pinned)
	writeJSON(w, http.StatusOK, ctl.Row(idle.VM{Name: info.Name, State: info.State, MemoryMB: info.MemoryMB}, time.Now()))
}

func (s *Server) handleJXVMKeep(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.jxVMInfo(r.Context(), name); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	s.Leases().Touch(name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleJXMemory(w http.ResponseWriter, r *http.Request) {
	if ctl := s.jxIdleCtlOr500(w); ctl != nil {
		writeJSON(w, http.StatusOK, ctl.MemoryReport())
	}
}

func (s *Server) jxVMInfo(ctx context.Context, name string) (*vmm.Info, error) {
	if s.VMs == nil {
		return nil, vmm.ErrNoBackend
	}
	return s.VMs.Get(ctx, name)
}
