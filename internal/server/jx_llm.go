package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"exe/internal/jx/llm"
	"exe/internal/jx/tools"
	"exe/internal/sshexec"
)

// LLM providers for VM agents (internal/jx/llm): Configuration → LLM
// Providers edits them, the VM Tools tab starts an agent on one. The
// launch rides the VM terminal: the desktop opens it with
// cmd=jx-launch:<query>, and jxLaunch (hooked in jxWrap) writes the
// agent's settings into the guest, forwards a loopback provider into it
// for the terminal's life, and swaps in the real command.

const jxLaunchPrefix = "jx-launch:"

func (s *Server) jxLLM() *llm.Store {
	s.jx.llmOnce.Do(func() { s.jx.llm = llm.NewStore(s.jxDir()) })
	return s.jx.llm
}

// jxLLMModels lists a provider's models (a var for the tests).
var jxLLMModels = llm.Models

func init() {
	jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/jx/llm", s.handleJXLLM)
		mux.HandleFunc("PUT /v1/jx/llm/providers", s.handleJXLLMProviders)
		mux.HandleFunc("POST /v1/jx/llm/models", s.handleJXLLMModels)
	})
}

func (s *Server) handleJXLLM(w http.ResponseWriter, r *http.Request) {
	st := s.jxLLM()
	last := st.Last()
	if last == nil {
		last = map[string]llm.Choice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": llm.Public(st.Providers()),
		"agents":    llm.AgentList(),
		"levels":    llm.Levels,
		"last":      last,
	})
}

func (s *Server) handleJXLLMProviders(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Providers []llm.Provider `json:"providers"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, err := s.jxLLM().SetProviders(body.Providers); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": llm.Public(s.jxLLM().Providers())})
}

// handleJXLLMModels lists a provider's models: a saved one by id, or the
// one being edited (kind, base_url, api_key; an empty key uses the key
// saved under id).
func (s *Server) handleJXLLMModels(w http.ResponseWriter, r *http.Request) {
	var p llm.Provider
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	saved, ok := s.jxLLM().Get(p.ID)
	switch {
	case p.BaseURL == "" && !ok:
		writeErr(w, http.StatusNotFound, fmt.Errorf("no provider %q", p.ID))
		return
	case p.BaseURL == "":
		p = saved
	case p.APIKey == "" && ok:
		p.APIKey = saved.APIKey
	}
	if p.Kind == "" {
		p.Kind = llm.OpenAI
	}
	models, err := jxLLMModels(r.Context(), http.DefaultClient, p)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// jxLaunch turns a terminal request whose cmd is jx-launch:<query> (tool,
// provider, model, thinking) into the agent's real command. It returns
// what to run when the terminal ends: closing the forward. A failure
// shows in the terminal itself, which then opens a shell.
func (s *Server) jxLaunch(r *http.Request, vm string) func() {
	q := r.URL.Query()
	spec, ok := strings.CutPrefix(q.Get("cmd"), jxLaunchPrefix)
	if !ok {
		return func() {}
	}
	cmd, done, err := s.jxLaunchCommand(r.Context(), vm, spec)
	if err != nil {
		cmd = "printf '%s\\n' " + sshexec.Quote("exe: "+err.Error()) + `; exec "${SHELL:-/bin/sh}" -l`
	}
	q.Set("cmd", cmd)
	r.URL.RawQuery = q.Encode()
	return done
}

func (s *Server) jxLaunchCommand(ctx context.Context, vm, raw string) (cmd string, done func(), err error) {
	done = func() {}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return "", done, err
	}
	tool, ok := tools.Find(v.Get("tool"))
	if !ok || !tool.Launch {
		return "", done, fmt.Errorf("%q is not an agent", v.Get("tool"))
	}
	choice := llm.Choice{Provider: v.Get("provider"), Model: v.Get("model"), Thinking: v.Get("thinking")}
	spec := llm.Spec{Agent: tool.ID, Thinking: choice.Thinking, Model: llm.Model{ID: choice.Model}}
	var p llm.Provider
	if choice.Provider != "" {
		if p, ok = s.jxLLM().Get(choice.Provider); !ok {
			return "", done, fmt.Errorf("no LLM provider %q (Configuration → LLM Providers)", choice.Provider)
		}
		if llm.Agents[tool.ID].API(p) == "" {
			return "", done, fmt.Errorf("%s cannot run on %s", tool.Name, p.Name)
		}
	}
	// the dialog offers this choice next time, even if the VM is not up
	s.jxLLM().Remember(tool.ID, choice)
	info, err := s.runningVM(ctx, vm)
	if err != nil {
		return "", done, err
	}
	target := s.vmTarget(info)

	if choice.Provider != "" {
		if spec.Model.ID == "" {
			spec.Model.ID = p.Model
		}
		// the model's limits, when its list gives them (omp and pi need them)
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if ms, err := jxLLMModels(lctx, http.DefaultClient, p); err == nil {
			for _, m := range ms {
				if m.ID == spec.Model.ID {
					spec.Model = m
				}
			}
		}
		cancel()
		if hostport, loop := p.Loopback(); loop {
			guest, stop, err := jxForward(ctx, target, hostport)
			if err != nil {
				return "", done, fmt.Errorf("forwarding %s into %s: %w", hostport, vm, err)
			}
			done = stop
			p = p.Rehost(guest)
		}
		spec.Provider = &p
	}

	id := make([]byte, 6)
	rand.Read(id)
	path := `"$HOME/.config/exe/llm/launch-` + hex.EncodeToString(id) + `.sh"`
	script, err := llm.Script(spec, path)
	if err != nil {
		done()
		return "", func() {}, err
	}
	// the script holds the key: it goes on stdin into a 0600 file, never
	// on a command line; launch files left by a terminal that never ran
	// are dropped after an hour
	write := `umask 077; d="$HOME/.config/exe/llm"; mkdir -p "$d" && chmod 700 "$d" || exit 1
find "$d" -name 'launch-*.sh' -mmin +60 -exec rm -f {} \; 2>/dev/null
cat > ` + path + "\n"
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := jxGuestWrite(wctx, target, write, script); err != nil {
		done()
		return "", func() {}, fmt.Errorf("writing the launch settings: %w", err)
	}
	return tools.LaunchCommand(tool, path), done, nil
}

// jxGuestWrite runs script in the guest with stdin.
var jxGuestWrite = func(ctx context.Context, t sshexec.Target, script, stdin string) error {
	client, err := t.Dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(stdin)
	out, err := sess.CombinedOutput("sh -c " + sshexec.Quote(script))
	if err != nil {
		return fmt.Errorf("%w %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// jxForward listens on the guest's loopback (a port sshd picks) and
// carries each connection to hostport on this machine. It returns the
// guest's host:port and the stop.
var jxForward = func(ctx context.Context, t sshexec.Target, hostport string) (string, func(), error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	client, err := t.Dial(dctx)
	cancel()
	if err != nil {
		return "", nil, err
	}
	ln, err := client.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return "", nil, fmt.Errorf("the VM's sshd refused a port forward (AllowTcpForwarding): %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				h, err := net.DialTimeout("tcp", hostport, 10*time.Second)
				if err != nil {
					return
				}
				defer h.Close()
				go func() { io.Copy(h, c); closeWrite(h) }()
				io.Copy(c, h)
			}()
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { ln.Close(); client.Close() }) }
	if port == "" || port == "0" {
		stop()
		return "", nil, errors.New("sshd did not say which port it opened")
	}
	return net.JoinHostPort("127.0.0.1", port), stop, nil
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}
