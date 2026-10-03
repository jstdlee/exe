package server

// jstdlee extensions ("jx"). Everything this branch adds to the daemon hangs
// off three hooks in upstream files, so upstream merges stay small:
//   - server.go: s.registerJX(mux) and s.jxWrap(mux) in Handler
//   - sshgate.go: one JXHold per SSH-gate session into a VM
//   - cmd/exe/main.go: JXWrapProxy around the reverse proxy
// Feature code lives in jx_*.go here and in internal/jx/*.

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"exe/internal/jx/lease"
)

type jxState struct {
	once   sync.Once
	leases *lease.Table

	// ipVM caches guest IP -> VM name for proxy traffic (refreshed lazily).
	ipMu sync.Mutex
	ipVM map[string]string
	ipAt time.Time
}

func (s *Server) jxInit() { s.jx.once.Do(func() { s.jx.leases = lease.New() }) }

// jxDir is where jx features keep their state: <state>/jx.
func (s *Server) jxDir() string { return filepath.Join(s.StateDir, "jx") }

// Leases is the VM lease table (idle detection).
func (s *Server) Leases() *lease.Table { s.jxInit(); return s.jx.leases }

// JXHold takes a lease on vm for reason and returns its release.
func (s *Server) JXHold(vm, reason string) func() { return s.Leases().Acquire(vm, reason) }

// jxFeatureRegs are the route registrations of jx features (jx_*.go init).
var jxFeatureRegs []func(s *Server, mux *http.ServeMux)

func (s *Server) registerJX(mux *http.ServeMux) {
	s.jxInit()
	mux.HandleFunc("GET /v1/jx/leases", s.handleJXLeases)
	for _, reg := range jxFeatureRegs {
		reg(s, mux)
	}
}

func (s *Server) handleJXLeases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Leases().All())
}

// jxRemoved answers 410 for the upstream agent features this branch drops:
// only Claude Code and Codex remain (host windows and Board agents). The
// one-shot /v1/chat/complete, models and status stay: Blue Pencil and the
// Hub composer call them. /v1/openai/* stays: the Codex window shows its
// usage meter from it.
func jxRemoved(r *http.Request) bool {
	p := r.URL.Path
	if rest, ok := strings.CutPrefix(p, "/v1/chat/"); ok {
		return rest == "send" || rest == "sessions" || strings.HasPrefix(rest, "sessions/")
	}
	if _, sub, ok := vmFromPath(p); ok {
		return sub == "agent" || sub == "memory" || sub == "transcripts" || strings.HasPrefix(sub, "transcripts/")
	}
	return false
}

// vmFromPath returns the VM name of /v1/vms/{name}/... paths.
func vmFromPath(p string) (name, sub string, ok bool) {
	rest, found := strings.CutPrefix(p, "/v1/vms/")
	if !found || rest == "" {
		return "", "", false
	}
	name, sub, _ = strings.Cut(rest, "/")
	return name, sub, name != ""
}

func (s *Server) jxWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if jxRemoved(r) {
			writeJSON(w, http.StatusGone, map[string]string{"error": "removed in this build: use the Board with Claude Code or Codex"})
			return
		}
		if !s.jxMemoryGuard(w, r) { // jx_idle.go
			return
		}
		if name, sub, ok := vmFromPath(r.URL.Path); ok {
			switch {
			case sub == "terminal":
				// the WebSocket handler blocks for the session's life
				defer s.JXHold(name, "terminal")()
			case r.Method == http.MethodPost && sub == "start":
				s.Leases().Touch(name)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// JXWrapProxy counts proxy traffic as activity of the VM behind the route.
func (s *Server) JXWrapProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if vm := s.vmForHost(r.Host); vm != "" {
			s.Leases().Touch(vm)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) vmForHost(host string) string {
	if s.Proxy == nil {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	backend := s.Proxy.Snapshot()[strings.ToLower(host)]
	u, err := url.Parse(backend)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return s.vmForIP(u.Hostname())
}

func (s *Server) vmForIP(ip string) string {
	s.jx.ipMu.Lock()
	defer s.jx.ipMu.Unlock()
	if vm, ok := s.jx.ipVM[ip]; ok && time.Since(s.jx.ipAt) < time.Minute {
		return vm
	}
	if s.VMs == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, err := s.VMs.List(ctx)
	if err != nil {
		return ""
	}
	s.jx.ipVM = map[string]string{}
	for _, v := range list {
		if v.IP != "" {
			s.jx.ipVM[v.IP] = v.Name
		}
	}
	s.jx.ipAt = time.Now()
	return s.jx.ipVM[ip]
}
