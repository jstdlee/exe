package server

// VM disk snapshots (Feature C): /v1/jx/vms/{name}/snapshots. A snapshot
// stops the VM, copies its disk (reflink or sparse, internal/jx/snap) and
// starts it again if it was running. Restore does the same in reverse.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"exe/internal/jx/snap"
	"exe/internal/vmm"
)

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/vms/{name}/snapshots", s.handleJXSnapList)
		mux.HandleFunc("POST /v1/jx/vms/{name}/snapshots", s.handleJXSnapCreate)
		mux.HandleFunc("POST /v1/jx/vms/{name}/snapshots/{id}/restore", s.handleJXSnapRestore)
		mux.HandleFunc("DELETE /v1/jx/vms/{name}/snapshots/{id}", s.handleJXSnapDelete)
	})
}

func (s *Server) snapStore() snap.Store {
	return snap.Store{Root: filepath.Join(s.jxDir(), "snapshots")}
}

// jxDiskBusy marks VMs whose disk a snapshot or restore is copying; env
// jobs refuse to start them meanwhile.
var jxDiskBusy sync.Map // vm -> struct{}

func jxSnapBusy(vm string) bool { _, ok := jxDiskBusy.Load(vm); return ok }

// SnapRequest is the body of a snapshot create or restore (both optional).
type SnapRequest struct {
	Label string `json:"label,omitempty"`
	// Force snapshots a VM that has lease holders (an open terminal, an
	// agent turn, ...): they lose the VM while it is stopped.
	Force bool `json:"force,omitempty"`
}

// SnapResult answers a create or restore.
type SnapResult struct {
	Snapshot  snap.Meta `json:"snapshot"`
	Restarted bool      `json:"restarted"`
	Warning   string    `json:"warning,omitempty"`
}

func (s *Server) handleJXSnapList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := vmm.ValidateName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	list, err := s.snapStore().List(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleJXSnapCreate(w http.ResponseWriter, r *http.Request) {
	s.snapOp(w, r, http.StatusCreated, func(ctx context.Context, name string, req SnapRequest) (snap.Meta, error) {
		return s.snapStore().Create(ctx, name, snap.DiskPath(s.StateDir, name), strings.TrimSpace(req.Label))
	})
}

func (s *Server) handleJXSnapRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.snapStore().Get(r.PathValue("name"), id); err != nil {
		writeErr(w, snapErrCode(err), err)
		return
	}
	s.snapOp(w, r, http.StatusOK, func(ctx context.Context, name string, _ SnapRequest) (snap.Meta, error) {
		if err := s.snapStore().Restore(ctx, name, id, snap.DiskPath(s.StateDir, name)); err != nil {
			return snap.Meta{}, err
		}
		s.PostNews("vm", "VM restored", fmt.Sprintf("%s was restored from snapshot %s.", name, id))
		return s.snapStore().Get(name, id)
	})
}

func (s *Server) handleJXSnapDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := vmm.ValidateName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.snapStore().Delete(name, r.PathValue("id")); err != nil {
		writeErr(w, snapErrCode(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func snapErrCode(err error) int {
	if errors.Is(err, snap.ErrNotFound) {
		return http.StatusNotFound
	}
	return errCode(err)
}

// snapOp runs a disk operation on a stopped VM: refuse a VM others hold
// (unless force), stop it, run op, start it again if it was running.
func (s *Server) snapOp(w http.ResponseWriter, r *http.Request, okCode int, op func(context.Context, string, SnapRequest) (snap.Meta, error)) {
	name := r.PathValue("name")
	if err := vmm.ValidateName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req SnapRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	info, err := s.VMs.Get(r.Context(), name)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	if _, busy := jxDiskBusy.LoadOrStore(name, struct{}{}); busy {
		writeErr(w, http.StatusConflict, fmt.Errorf("a snapshot or restore of %s is already running", name))
		return
	}
	defer jxDiskBusy.Delete(name)
	if holders := s.Leases().Get(name).Holders; len(holders) > 0 && !req.Force {
		reasons := make([]string, 0, len(holders))
		for _, h := range holders {
			reasons = append(reasons, h.Reason)
		}
		writeErr(w, http.StatusConflict, fmt.Errorf("%s is in use (%s); the snapshot stops it: close those or pass force", name, strings.Join(reasons, ", ")))
		return
	}
	wasRunning := info.State != "stopped"
	if wasRunning {
		if err := s.VMs.Stop(r.Context(), name); err != nil && !errors.Is(err, vmm.ErrNotRunning) {
			writeErr(w, errCode(err), fmt.Errorf("stop %s: %w", name, err))
			return
		}
	}
	meta, opErr := op(r.Context(), name, req)
	res := SnapResult{Snapshot: meta}
	if wasRunning {
		// the client may be gone; the VM still comes back
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		if _, err := s.VMs.Start(ctx, name); err != nil {
			res.Warning = fmt.Sprintf("%s did not start again: %v", name, err)
		} else {
			res.Restarted = true
			s.Leases().Touch(name)
		}
	}
	if opErr != nil {
		msg := opErr.Error()
		if res.Warning != "" {
			msg += "; " + res.Warning
		}
		writeErr(w, snapErrCode(opErr), errors.New(msg))
		return
	}
	writeJSON(w, okCode, res)
}
