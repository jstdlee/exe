package server

import (
	"context"
	"log"
	"strconv"
	"time"
)

// touchVM records activity that should keep a VM from being idle-stopped:
// a terminal keystroke, a job, an agent run, SSH via Chat, start/create.
func (s *Server) touchVM(name string) {
	if name == "" {
		return
	}
	s.idleMu.Lock()
	if s.idleAt == nil {
		s.idleAt = map[string]time.Time{}
	}
	s.idleAt[name] = time.Now()
	s.idleMu.Unlock()
}

func (s *Server) lastVMActivity(name string) time.Time {
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	return s.idleAt[name]
}

func (s *Server) startIdleWatcher() {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			s.stopIdleVMs()
		}
	}()
}

func (s *Server) stopIdleVMs() {
	mins := s.Config().IdleStopMinutes
	if mins <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(mins) * time.Minute)
	vms, err := s.VMs.List(context.Background())
	if err != nil {
		return
	}
	for _, vm := range vms {
		if vm.State != "running" {
			continue
		}
		// an in-flight vibecode run is activity even if the user is idle
		busy := false
		s.activeRuns.Range(func(k, _ any) bool {
			id, _ := k.(string)
			if stringsHasVM(id, vm.Name) {
				busy = true
				return false
			}
			return true
		})
		if busy {
			s.touchVM(vm.Name)
			continue
		}
		last := s.lastVMActivity(vm.Name)
		if last.IsZero() {
			// never touched this process: treat CreatedAt / now-on-first-see
			s.touchVM(vm.Name)
			continue
		}
		if last.After(cutoff) {
			continue
		}
		log.Printf("idle-stop %s: no activity for %d minutes", vm.Name, mins)
		if err := s.VMs.Stop(context.Background(), vm.Name); err != nil {
			log.Printf("idle-stop %s: %v", vm.Name, err)
			continue
		}
		s.PostNews("vm", "VM idle-stopped", vm.Name+" was idle for "+strconv.Itoa(mins)+" minutes")
	}
}

func stringsHasVM(id, name string) bool {
	return id == name || (len(id) > len(name) && id[:len(name)+1] == name+"/")
}
