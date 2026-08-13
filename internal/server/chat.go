package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/codex"
	"exe/internal/config"
	"exe/internal/sshexec"
	"exe/internal/vmm"
)

// The Chat window's agent: unlike the per-VM vibecoding agent it operates
// the whole daemon — VMs, SSH into them, routes, logs — over a persisted
// multi-turn session.

const (
	chatMaxTurns      = 40
	chatToolTimeout   = 5 * time.Minute
	chatMaxToolOutput = 12000
)

const chatSystemTmpl = `You are the operator of exe, a personal VM cloud running on this Mac. You manage Debian Linux VMs (Virtualization.framework) through tools; inside every running VM you act as user %s over SSH, with passwordless sudo.
%s

Rules:
- VM names: lowercase letters, digits, hyphens, max 31 chars.
- To inspect or change anything inside a VM, use bash (non-interactive commands only). Install packages with sudo apt-get install -y. Services you set up should bind 0.0.0.0 and run under systemd so they survive.
- Only call delete_vm or unexpose when the user explicitly asked for that; for anything destructive, restate what you are about to do first.
- Creating a VM takes ~10 seconds and it boots with an IP; the very first creation ever downloads a 3 GB image.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, summarize what changed and give the address where it can be reached.`

func (s *Server) chatDir() string { return filepath.Join(s.StateDir, "chat") }

func chatSystemPrompt(sshUser, domain string) string {
	pub := "Publishing: cloudflare.domain is not configured, so expose only adds a local proxy route — suggest the Cloudflare wizard if the user wants public URLs."
	if domain != "" {
		pub = fmt.Sprintf("Publishing: expose makes a VM port reachable at https://<subdomain>.%s.", domain)
	}
	return fmt.Sprintf(chatSystemTmpl, sshUser, pub)
}

// ---- backend detection ----

// chatProvider is the Chat window's backend: "openai" (ChatGPT
// subscription) when configured, otherwise "ollama".
func chatProvider(cfg *config.Config) string {
	if cfg.ChatProvider == "openai" {
		return "openai"
	}
	return "ollama"
}

// handleChatStatus reports whether the chat backend is usable, cached for a
// minute so the UI can ask freely; ?force=1 bypasses the cache. The ChatGPT
// path is a local credentials check — no probe, no cache needed.
func (s *Server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	if chatProvider(cfg) == "openai" {
		res := map[string]any{"provider": "openai", "detected": false,
			"model": cfg.OpenAI.Model, "effort": cfg.OpenAI.Effort}
		if c := s.codexCreds(); c != nil {
			res["detected"] = true
			res["plan"] = c.Plan
			res["email"] = c.Email
		} else {
			res["reason"] = "not signed in to ChatGPT (Configuration → OpenAI)"
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	key := cfg.Ollama.BaseURL + "|" + cfg.Ollama.APIKey + "|" + cfg.Ollama.Model + "|" + cfg.Ollama.Effort
	s.chatStatMu.Lock()
	if s.chatStatRes != nil && s.chatStatKey == key &&
		time.Since(s.chatStatAt) < time.Minute && r.URL.Query().Get("force") != "1" {
		res := s.chatStatRes
		s.chatStatMu.Unlock()
		writeJSON(w, http.StatusOK, res)
		return
	}
	s.chatStatMu.Unlock()

	res := map[string]any{"provider": "ollama", "detected": false,
		"model": cfg.Ollama.Model, "effort": cfg.Ollama.Effort, "base_url": cfg.Ollama.BaseURL}
	switch {
	case cfg.Ollama.BaseURL == "":
		res["reason"] = "ollama.base_url is not configured"
	case cfg.Ollama.APIKey == "" && strings.Contains(cfg.Ollama.BaseURL, "ollama.com"):
		res["reason"] = "ollama.api_key is not configured (or set OLLAMA_API_KEY)"
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		v, err := agent.Version(ctx, agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey})
		if err != nil {
			res["reason"] = err.Error()
		} else {
			res["detected"] = true
			res["version"] = v
		}
	}
	s.chatStatMu.Lock()
	s.chatStatAt, s.chatStatKey, s.chatStatRes = time.Now(), key, res
	s.chatStatMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// handleChatModels lists the models a backend offers, for the
// Configuration window's model dropdowns. ?provider=ollama|openai picks
// which backend to ask (default: the active chat provider).
func (s *Server) handleChatModels(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = chatProvider(cfg)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var models []string
	var err error
	switch provider {
	case "openai":
		var creds *codex.Creds
		if creds, err = s.codexToken(ctx, false); err == nil {
			models, err = codex.Models(ctx, creds.AccessToken, creds.AccountID)
		}
	case "ollama":
		if cfg.Ollama.BaseURL == "" {
			err = errors.New("ollama.base_url is not configured")
		} else {
			models, err = agent.Models(ctx, agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey})
		}
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown provider %q", provider))
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "models": models})
}

// ---- session CRUD ----

func (s *Server) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	list, err := chat.List(s.chatDir())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleChatSession(w http.ResponseWriter, r *http.Request) {
	sess, err := chat.Load(s.chatDir(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleChatSessionDelete(w http.ResponseWriter, r *http.Request) {
	if err := chat.Delete(s.chatDir(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---- send: the tool loop ----

// handleChatSend appends a user message to a session (creating one when the
// id is empty) and streams the agent loop back as NDJSON events:
// session, delta (one per streamed content fragment), tool_call,
// tool_result, error, done.
func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	var req struct {
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	provider := chatProvider(cfg)
	if provider == "openai" {
		if s.codexCreds() == nil {
			writeErr(w, http.StatusConflict, errors.New("not signed in to ChatGPT (Configuration → OpenAI)"))
			return
		}
	} else {
		if cfg.Ollama.BaseURL == "" {
			writeErr(w, http.StatusConflict, errors.New("ollama.base_url is not configured"))
			return
		}
		if cfg.Ollama.APIKey == "" && strings.Contains(cfg.Ollama.BaseURL, "ollama.com") {
			writeErr(w, http.StatusConflict, errors.New("ollama.api_key is not configured (or set OLLAMA_API_KEY)"))
			return
		}
	}

	var sess *chat.Session
	var err error
	if req.Session == "" {
		sess, err = chat.New(s.chatDir(), req.Message)
	} else {
		sess, err = chat.Load(s.chatDir(), req.Session)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	lock, _ := s.chatLocks.LoadOrStore(sess.ID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	if !mu.TryLock() {
		writeErr(w, http.StatusConflict, errors.New("a reply is already streaming in this session"))
		return
	}
	defer mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	emit := func(ev map[string]any) {
		enc.Encode(ev)
		if fl != nil {
			fl.Flush()
		}
	}
	emit(map[string]any{"type": "session", "meta": sess.Meta})

	dir := s.chatDir()
	save := func() {
		if err := chat.Save(dir, sess); err != nil {
			emit(map[string]any{"type": "error", "error": "save session: " + err.Error()})
		}
	}
	sess.Messages = append(sess.Messages, agent.Message{Role: "user", Content: req.Message})
	save()

	acfg := agent.Config{BaseURL: cfg.Ollama.BaseURL, APIKey: cfg.Ollama.APIKey,
		Model: cfg.Ollama.Model, Effort: cfg.Ollama.Effort}
	system := agent.Message{Role: "system", Content: chatSystemPrompt(cfg.SSHUser, cfg.Cloudflare.Domain)}
	tools := chatTools()
	// callModel runs one turn on the configured backend. The ChatGPT path
	// resolves (and auto-refreshes) the token per turn and retries once
	// after a 401 in case the token was revoked out from under us.
	callModel := func(ctx context.Context, msgs []agent.Message, onDelta func(string)) (*agent.Message, error) {
		if provider != "openai" {
			return agent.ChatStream(ctx, acfg, msgs, tools, onDelta)
		}
		creds, err := s.codexToken(ctx, false)
		if err != nil {
			return nil, err
		}
		ccfg := codex.ClientConfig{AccessToken: creds.AccessToken, AccountID: creds.AccountID,
			Model: cfg.OpenAI.Model, Effort: cfg.OpenAI.Effort, SessionKey: sess.ID}
		msg, err := codex.ChatStream(ctx, ccfg, msgs, tools, onDelta)
		if errors.Is(err, codex.ErrUnauthorized) {
			if creds, err = s.codexToken(ctx, true); err != nil {
				return nil, err
			}
			ccfg.AccessToken, ccfg.AccountID = creds.AccessToken, creds.AccountID
			msg, err = codex.ChatStream(ctx, ccfg, msgs, tools, onDelta)
		}
		return msg, err
	}
	for turn := 0; turn < chatMaxTurns; turn++ {
		msg, err := callModel(r.Context(), append([]agent.Message{system}, sess.Messages...),
			func(d string) { emit(map[string]any{"type": "delta", "text": d}) })
		if err != nil {
			emit(map[string]any{"type": "error", "error": err.Error()})
			break
		}
		sess.Messages = append(sess.Messages, *msg)
		if len(msg.ToolCalls) == 0 {
			save()
			break
		}
		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			args := agent.ParseArgs(tc.Function.Arguments)
			emit(map[string]any{"type": "tool_call", "name": name, "summary": chatToolSummary(name, args)})
			result := s.execChatTool(r.Context(), name, args)
			emit(map[string]any{"type": "tool_result", "name": name, "output": sshexec.Truncate(result, 4000)})
			sess.Messages = append(sess.Messages, agent.Message{Role: "tool", ToolName: name, ToolCallID: tc.ID, Content: result})
		}
		save()
	}
	emit(map[string]any{"type": "done", "meta": sess.Meta})
}

// ---- tools ----

func chatTools() []agent.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	vm := str("VM name")
	return []agent.Tool{
		agent.MkTool("list_vms", "List all VMs with state, IP and specs.", map[string]any{}, nil),
		agent.MkTool("create_vm", "Create and boot a new VM; unset specs use the configured defaults.", map[string]any{
			"name": vm, "cpus": num("CPU count"), "memory_mb": num("memory in MB"), "disk_gb": num("disk in GB"),
		}, []string{"name"}),
		agent.MkTool("start_vm", "Start a stopped VM.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("stop_vm", "Stop a running VM.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("delete_vm", "Permanently delete a VM and its disk. Only when the user explicitly asked.", map[string]any{"name": vm}, []string{"name"}),
		agent.MkTool("bash", "Run a shell command inside a running VM; returns combined output and exit code.", map[string]any{
			"vm": vm, "command": str("shell command to run"),
		}, []string{"vm", "command"}),
		agent.MkTool("write_file", "Create or overwrite a file inside a running VM; parent directories are created.", map[string]any{
			"vm": vm, "path": str("absolute path"), "content": str("file content"),
		}, []string{"vm", "path", "content"}),
		agent.MkTool("read_file", "Read a text file from a running VM.", map[string]any{
			"vm": vm, "path": str("absolute path"),
		}, []string{"vm", "path"}),
		agent.MkTool("list_ports", "List TCP services listening inside a running VM (SSH excluded).", map[string]any{"vm": vm}, []string{"vm"}),
		agent.MkTool("list_routes", "List published routes: hostname -> VM backend.", map[string]any{}, nil),
		agent.MkTool("expose", "Publish a VM port on the internet as https://<subdomain>.<configured domain>.", map[string]any{
			"vm": vm, "port": num("VM port to publish"), "subdomain": str("subdomain (defaults to the VM name)"),
		}, []string{"vm", "port"}),
		agent.MkTool("unexpose", "Remove a published hostname (DNS, tunnel ingress, proxy route). Only when the user explicitly asked.", map[string]any{
			"host": str("full hostname as shown by list_routes"),
		}, []string{"host"}),
		agent.MkTool("daemon_logs", "Read the tail of the exe daemon's own log — useful to diagnose expose/DNS/VM issues.", map[string]any{
			"lines": num("how many lines (default 100, max 400)"),
		}, nil),
	}
}

// chatToolSummary is the one-liner the UI shows for a tool call.
func chatToolSummary(name string, args map[string]any) string {
	str := func(k string) string { v, _ := args[k].(string); return v }
	switch name {
	case "bash":
		return str("vm") + " $ " + str("command")
	case "write_file":
		return fmt.Sprintf("write %s:%s (%d bytes)", str("vm"), str("path"), len(str("content")))
	case "read_file":
		return fmt.Sprintf("read %s:%s", str("vm"), str("path"))
	case "expose":
		p, _ := args["port"].(float64)
		return fmt.Sprintf("expose %s:%d as %q", str("vm"), int(p), str("subdomain"))
	default:
		b, _ := json.Marshal(args)
		if len(args) == 0 {
			return name
		}
		return name + " " + string(b)
	}
}

func (s *Server) execChatTool(ctx context.Context, name string, args map[string]any) string {
	cfg := s.Config()
	str := func(k string) string { v, _ := args[k].(string); return v }
	num := func(k string) int { v, _ := args[k].(float64); return int(v) }
	asJSON := func(v any, err error) string {
		if err != nil {
			return "error: " + err.Error()
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	target := func(ctx context.Context) (sshexec.Target, error) {
		info, err := s.runningVM(ctx, str("vm"))
		if err != nil {
			return sshexec.Target{}, err
		}
		return s.vmTarget(info), nil
	}
	tctx, cancel := context.WithTimeout(ctx, chatToolTimeout)
	defer cancel()

	switch name {
	case "list_vms":
		return asJSON(s.VMs.List(tctx))
	case "create_vm":
		spec := vmm.Spec{Name: str("name"), CPUs: num("cpus"), MemoryMB: num("memory_mb"), DiskGB: num("disk_gb")}
		s.fillSpec(&spec)
		info, err := s.VMs.Create(tctx, spec)
		if err == nil {
			s.PostNews("vm", "VM created", vmNewsLine(spec))
		}
		return asJSON(info, err)
	case "start_vm":
		return asJSON(s.VMs.Start(tctx, str("name")))
	case "stop_vm":
		if err := s.VMs.Stop(tctx, str("name")); err != nil {
			return "error: " + err.Error()
		}
		return "stopped"
	case "delete_vm":
		if err := s.VMs.Delete(tctx, str("name")); err != nil {
			return "error: " + err.Error()
		}
		s.PostNews("vm", "VM deleted", str("name")+" and its disk were removed.")
		return "deleted"
	case "bash":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		out, code, err := t.Run(tctx, str("command"), chatMaxToolOutput)
		if err != nil {
			out = fmt.Sprintf("error: %v\n%s", err, out)
		}
		if code != 0 {
			out += fmt.Sprintf("\n[exit code %d]", code)
		} else if strings.TrimSpace(out) == "" {
			out = "(no output, exit code 0)"
		}
		return out
	case "write_file":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		if err := t.WriteFile(tctx, str("path"), []byte(str("content"))); err != nil {
			return "error: " + err.Error()
		}
		return "ok"
	case "read_file":
		t, err := target(tctx)
		if err != nil {
			return "error: " + err.Error()
		}
		out, err := t.ReadFile(tctx, str("path"), chatMaxToolOutput)
		if err != nil {
			return "error: " + err.Error()
		}
		return out
	case "list_ports":
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		ports, err := s.scanPorts(tctx, info)
		if err != nil {
			return "error: " + err.Error()
		}
		return asJSON(map[string]any{"ip": info.IP, "ports": ports}, nil)
	case "list_routes":
		return asJSON(s.Proxy.Snapshot(), nil)
	case "expose":
		port := num("port")
		if port <= 0 || port > 65535 {
			return "error: port is required (1-65535)"
		}
		if cfg.Cloudflare.Domain == "" {
			return "error: cloudflare.domain is not configured — ask the user to run the Cloudflare wizard"
		}
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		return asJSON(s.exposeVM(tctx, info, str("vm"), str("subdomain"), port))
	case "unexpose":
		return asJSON(s.removeRoute(tctx, str("host")))
	case "daemon_logs":
		if s.Logs == nil {
			return "error: daemon log not available"
		}
		n := num("lines")
		if n <= 0 {
			n = 100
		}
		if n > 400 {
			n = 400
		}
		backlog, _, cancelSub := s.Logs.Subscribe()
		cancelSub()
		if len(backlog) > n {
			backlog = backlog[len(backlog)-n:]
		}
		return strings.Join(backlog, "\n")
	default:
		return fmt.Sprintf("unknown tool %q", name)
	}
}
