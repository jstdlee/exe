package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"exe/internal/agent"
	"exe/internal/chat"
	"exe/internal/codex"
	"exe/internal/config"
	"exe/internal/hostagent"
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
- Pushing to github.com: use github_push — VMs hold no GitHub credentials, so git push via bash always fails; never configure credentials or tokens inside a VM.
- Creating a VM takes ~10 seconds and it boots with an IP; the very first creation ever downloads a 3 GB image.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, summarize what changed and give the address where it can be reached.`

// The system prompt of a session pinned to one VM: same operator, but the
// fleet is out of scope and the tools already target the pinned VM.
const chatPinnedTmpl = `You are the operator of %q, one Debian Linux VM in exe, a personal VM cloud running on this Mac. All your tools act on this VM only — other VMs are out of scope for this conversation. Inside the VM you act as user %s over SSH, with passwordless sudo.
%s

Rules:
- To inspect or change anything inside the VM, use bash (non-interactive commands only). Install packages with sudo apt-get install -y. Services you set up should bind 0.0.0.0 and run under systemd so they survive.
- Only call unexpose when the user explicitly asked for that; for anything destructive, restate what you are about to do first.
- Pushing to github.com: use github_push — the VM holds no GitHub credentials, so git push via bash always fails; never configure credentials or tokens inside the VM.
- Format answers in Markdown (lists, tables, code blocks and links render nicely), and keep them concise. When you finish a task, summarize what changed and give the address where it can be reached.`

func (s *Server) chatDir() string { return filepath.Join(s.StateDir, "chat") }

func chatSystemPrompt(sshUser, domain, vm string) string {
	pub := "Publishing: cloudflare.domain is not configured, so expose only adds a local proxy route — suggest the Cloudflare wizard if the user wants public URLs."
	if domain != "" {
		pub = fmt.Sprintf("Publishing: expose makes a VM port reachable at https://<subdomain>.%s.", domain)
	}
	if vm != "" {
		return fmt.Sprintf(chatPinnedTmpl, vm, sshUser, pub)
	}
	return fmt.Sprintf(chatSystemTmpl, sshUser, pub)
}

// ---- backend detection ----

// chatProvider is the Chat window's backend: "openai" (ChatGPT
// subscription) when configured, otherwise "ollama".
func chatProvider(cfg *config.Config) string {
	switch cfg.ChatProvider {
	case "openai", "claude", "grok", "custom":
		return cfg.ChatProvider
	default:
		return "ollama"
	}
}

// handleChatStatus reports whether a host agent is ready. Chat no longer
// stores API keys — it uses ~/.grok, ~/.claude and ~/.codex.
func (s *Server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	agents := hostagent.List()
	res := map[string]any{
		"provider": cfg.HostAgent,
		"model":    cfg.HostModel,
		"detected": false,
		"agents":   agents,
		"source":   "host",
	}
	var ready []hostagent.Info
	for _, a := range agents {
		if a.Ready {
			ready = append(ready, a)
		}
	}
	if len(ready) == 0 {
		res["reason"] = "no host agent is signed in (Grok ~/.grok, Claude ~/.claude, Codex ~/.codex)"
		writeJSON(w, http.StatusOK, res)
		return
	}
	res["detected"] = true
	pick := cfg.HostAgent
	var info hostagent.Info
	found := false
	for _, a := range ready {
		if a.ID == pick {
			info = a
			found = true
			break
		}
	}
	if !found {
		info = ready[0]
		pick = info.ID
	}
	res["provider"] = pick
	if cfg.HostModel != "" {
		res["model"] = cfg.HostModel
	} else {
		res["model"] = info.DefaultModel
	}
	res["auth"] = info.Auth
	res["email"] = info.Email
	res["source"] = info.Source
	writeJSON(w, http.StatusOK, res)
}

// handleChatModels lists models for a host agent (query ?provider=grok|claude|codex).
func (s *Server) handleChatModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = s.Config().HostAgent
	}
	for _, a := range hostagent.List() {
		if a.ID == provider {
			writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "models": a.Models})
			return
		}
	}
	writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown host agent %q", provider))
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

func (s *Server) handleChatSessionPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := chat.Rename(s.chatDir(), r.PathValue("id"), req.Title); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	sess, err := chat.Load(s.chatDir(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, sess.Meta)
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
		// VM pins a NEW session to one VM; ignored when Session is set —
		// the pin is a property of the session, not of the request.
		VM    string `json:"vm"`
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	agentID := strings.TrimSpace(req.Agent)
	if agentID == "" {
		agentID = cfg.HostAgent
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = cfg.HostModel
	}
	backend, rerr := hostagent.Resolve(agentID, model)
	if rerr != nil {
		writeErr(w, http.StatusConflict, rerr)
		return
	}

	var sess *chat.Session
	var err error
	if req.Session == "" {
		if req.VM != "" {
			if _, err := s.VMs.Get(r.Context(), req.VM); err != nil {
				writeErr(w, http.StatusNotFound, err)
				return
			}
		}
		sess, err = chat.New(s.chatDir(), req.Message, req.VM)
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

	acfg := agent.Config{
		BaseURL: backend.BaseURL, APIKey: backend.APIKey, Model: backend.Model,
		ExtraHeaders: backend.ExtraHeaders,
	}
	if backend.Kind == "openai" {
		acfg.Style = "openai"
	}
	system := agent.Message{Role: "system", Content: chatSystemPrompt(cfg.SSHUser, cfg.Cloudflare.Domain, sess.VM)}
	tools := chatTools(sess.VM != "")
	// callModel runs one turn on the host agent. Codex uses the ChatGPT
	// Responses API via ~/.codex tokens; everything else is OpenAI-compat
	// or Ollama /api/chat.
	callModel := func(ctx context.Context, msgs []agent.Message, onDelta func(string)) (*agent.Message, error) {
		if backend.Kind != "codex" {
			return agent.ChatStream(ctx, acfg, msgs, tools, onDelta)
		}
		// Prefer the host Codex CLI login (~/.codex); fall back to the
		// daemon's own ~/.exe/openai.json if that's what signed in.
		creds := hostCodexCreds()
		if creds == nil {
			var err error
			creds, err = s.codexToken(ctx, false)
			if err != nil || creds == nil {
				return nil, errors.New("Codex is not signed in on this host (~/.codex/auth.json)")
			}
		}
		ccfg := codex.ClientConfig{AccessToken: creds.AccessToken, AccountID: creds.AccountID,
			Model: backend.Model, Effort: cfg.OpenAI.Effort, SessionKey: sess.ID}
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
			if sess.VM != "" {
				pinChatArgs(name, args, sess.VM)
			}
			emit(map[string]any{"type": "tool_call", "name": name, "summary": chatToolSummary(name, args)})
			result := s.execChatTool(r.Context(), name, args, sess.VM)
			emit(map[string]any{"type": "tool_result", "name": name, "output": sshexec.Truncate(result, 4000)})
			sess.Messages = append(sess.Messages, agent.Message{Role: "tool", ToolName: name, ToolCallID: tc.ID, Content: result})
		}
		save()
	}
	emit(map[string]any{"type": "done", "meta": sess.Meta})
}

// ---- tools ----

func chatTools(pinned bool) []agent.Tool {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	num := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	vm := str("VM name")
	tools := []agent.Tool{
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
		agent.MkTool("github_push", "Push a project folder in a VM to the user's GitHub. The daemon holds the credentials and pushes on the VM's behalf — a plain `git push` inside the VM always fails, so use this tool for any push to github.com. It commits uncommitted work, creates the repository if needed, and pushes the current branch.", map[string]any{
			"vm":      vm,
			"path":    str("absolute project folder in the VM, e.g. /home/dev/app"),
			"repo":    str("repository name — omit to reuse the folder's origin remote (or the folder name on a first publish)"),
			"private": map[string]any{"type": "boolean", "description": "create the repository as private (default true; ignored when it already exists)"},
		}, []string{"vm", "path"}),
		agent.MkTool("daemon_logs", "Read the tail of the exe daemon's own log — useful to diagnose expose/DNS/VM issues.", map[string]any{
			"lines": num("how many lines (default 100, max 400)"),
		}, nil),
	}
	if !pinned {
		return tools
	}
	// A pinned session can't even express touching another VM: the fleet
	// tools disappear and the VM-name parameter is stripped from every
	// schema — pinChatArgs injects the pinned name at call time instead.
	out := tools[:0]
	for _, t := range tools {
		switch t.Function.Name {
		case "list_vms", "create_vm", "delete_vm":
			continue
		}
		props := t.Function.Parameters["properties"].(map[string]any)
		delete(props, "vm")
		delete(props, "name")
		if req, ok := t.Function.Parameters["required"].([]string); ok {
			keep := req[:0]
			for _, k := range req {
				if k != "vm" && k != "name" {
					keep = append(keep, k)
				}
			}
			t.Function.Parameters["required"] = keep
		}
		out = append(out, t)
	}
	return out
}

// pinChatArgs overwrites a tool call's VM-target argument with the session's
// pinned VM. The pinned schemas no longer carry the parameter, and a
// hallucinated value must not win either — overwriting beats validating.
func pinChatArgs(name string, args map[string]any, vm string) {
	switch name {
	case "bash", "write_file", "read_file", "list_ports", "expose", "github_push":
		args["vm"] = vm
	case "start_vm", "stop_vm":
		args["name"] = vm
	}
}

// routeBelongsTo verifies that host's proxy route points at the named VM's
// current IP, so a pinned chat can only unexpose its own routes.
func (s *Server) routeBelongsTo(ctx context.Context, vmName, host string) error {
	backend, ok := s.Proxy.Snapshot()[strings.ToLower(host)]
	if !ok {
		return fmt.Errorf("no route for %q", host)
	}
	info, err := s.runningVM(ctx, vmName)
	if err != nil {
		return err
	}
	u, err := url.Parse(backend)
	if err != nil || u.Hostname() == "" || u.Hostname() != info.IP {
		return fmt.Errorf("%s does not route to VM %s", host, vmName)
	}
	return nil
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
	case "github_push":
		return fmt.Sprintf("push %s:%s to github", str("vm"), str("path"))
	default:
		b, _ := json.Marshal(args)
		if len(args) == 0 {
			return name
		}
		return name + " " + string(b)
	}
}

func (s *Server) execChatTool(ctx context.Context, name string, args map[string]any, pin string) string {
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

	// Belt and braces for pinned sessions: the fleet tools aren't offered,
	// but a model may still emit one from prior context; and unexpose takes
	// a hostname, which schema shaping can't scope to the pin.
	if pin != "" {
		switch name {
		case "list_vms", "create_vm", "delete_vm":
			return fmt.Sprintf("error: this chat is pinned to VM %s; %s is not available here", pin, name)
		case "unexpose":
			if err := s.routeBelongsTo(tctx, pin, str("host")); err != nil {
				return "error: " + err.Error()
			}
		}
	}

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
		if vm := str("vm"); vm != "" {
			s.touchVM(vm)
		}
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
	case "github_push":
		creds, err := s.ghRequireAuth(tctx)
		if err != nil {
			return "error: " + err.Error() + " — ask the user to sign in there, then retry"
		}
		info, err := s.runningVM(tctx, str("vm"))
		if err != nil {
			return "error: " + err.Error()
		}
		private := true
		if v, ok := args["private"].(bool); ok {
			private = v
		}
		var steps strings.Builder
		step := func(format string, a ...any) { fmt.Fprintf(&steps, format+"\n", a...) }
		// Its own deadline: a first publish may apt-get install git, and a
		// large push can outgrow the budget for quick commands.
		pctx, pcancel := context.WithTimeout(ctx, publishTimeout)
		defer pcancel()
		repo, err := s.publishVM(pctx, s.vmTarget(info), creds, str("path"), str("repo"), private, "", step)
		if err != nil {
			return steps.String() + "error: " + err.Error()
		}
		s.PostNews("vm", "Published to GitHub", str("vm")+": "+str("path")+" → "+repo.HTMLURL)
		return steps.String() + "pushed: " + repo.HTMLURL
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
