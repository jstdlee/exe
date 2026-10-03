package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"exe/internal/jx/board"
	"exe/internal/vmm"
)

// The Board's agent set-up routes: sessions to continue, CLI status,
// install, and the write-only secrets.

// runningRemote resolves a running VM's shell for a read-only look into
// it: a look does not start a VM (a turn does), so a stopped one is a 409.
func (b *jxBoard) runningRemote(ctx context.Context, vm string) (board.Remote, error) {
	if b.s.VMs == nil {
		return nil, vmm.ErrNoBackend
	}
	info, err := b.s.VMs.Get(ctx, vm)
	if err != nil {
		return nil, err
	}
	if info.State != "running" || info.IP == "" {
		return nil, fmt.Errorf("%s is %s; start it to look inside: %w", vm, info.State, vmm.ErrNotRunning)
	}
	return b.remote(info), nil
}

func (b *jxBoard) handleSessions(w http.ResponseWriter, r *http.Request) {
	target, agent := r.URL.Query().Get("target"), r.URL.Query().Get("agent")
	if target == "" || !board.ValidAgent(agent) {
		writeErr(w, http.StatusBadRequest, errors.New(`target and agent ("claude" or "codex") are required`))
		return
	}
	var list []board.Session
	if target == board.HostTarget {
		list = hostSessions(agent)
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		rem, err := b.runningRemote(ctx, target)
		if err != nil {
			writeErr(w, errCode(err), err)
			return
		}
		if list, err = board.ListSessions(ctx, rem, agent); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, board.MarkThreads(list, b.eng.Store().List(), target, agent))
}

// hostSessionsScanned bounds the transcripts read on the host per list.
const hostSessionsScanned = 60

// hostSessions lists the host's sessions with the readers the Claude Code
// and Codex windows use (claudesessions.go, codexthreads.go).
func hostSessions(agent string) []board.Session {
	var out []board.Session
	if agent == board.Claude {
		files := claudeTranscriptFiles(claudeHome())
		for i, f := range files {
			if i == hostSessionsScanned {
				break
			}
			t := claudeTranscriptOf(f.path, f.size)
			if t == nil {
				continue
			}
			title := t.title
			if title == "" {
				title = t.prompt
			}
			out = append(out, board.Session{ID: t.id, Title: title, Cwd: t.cwd,
				UpdatedAt: f.mtime.UTC(), Headless: claudeHeadless(t.entrypoint)})
		}
		return out
	}
	home := codexHome()
	if home == "" {
		return out
	}
	type file struct {
		path  string
		mtime time.Time
	}
	var files []file
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{p, info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	names := readCodexIndex(home)
	for i, f := range files {
		if i == hostSessionsScanned {
			break
		}
		ro := readCodexRollout(f.path)
		if ro == nil || !ro.user {
			continue
		}
		title := names[ro.id]
		if title == "" {
			title = ro.prompt
		}
		out = append(out, board.Session{ID: ro.id, Title: title, Cwd: ro.cwd,
			UpdatedAt: f.mtime.UTC(), Headless: strings.Contains(ro.originator, "exec")})
	}
	return out
}

func (b *jxBoard) handleAgentsStatus(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		writeErr(w, http.StatusBadRequest, errors.New("target is required"))
		return
	}
	if target == board.HostTarget {
		writeJSON(w, http.StatusOK, map[string]board.AgentProbe{
			board.Claude: hostAgentStatus(board.Claude),
			board.Codex:  hostAgentStatus(board.Codex),
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rem, err := b.runningRemote(ctx, target)
	if err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	p, err := board.RunProbe(ctx, rem)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	sec, _ := b.secrets.Get()
	out := map[string]board.AgentProbe{}
	for _, a := range []string{board.Claude, board.Codex} {
		ap := p.Agent(a)
		// a set secret reaches the guest before its next turn
		if (a == board.Claude && sec.ClaudeOAuthToken != "") || (a == board.Codex && sec.CodexAPIKey != "") {
			ap.Auth = "token"
		}
		out[a] = ap
	}
	writeJSON(w, http.StatusOK, out)
}

// hostAgentStatus is one host CLI's status: installed, its version, and
// how it signs in — a token in the daemon's environment, or the CLI's own
// login.
func hostAgentStatus(agent string) board.AgentProbe {
	bin := agentPath(hostAgents[agent])
	ap := board.AgentProbe{Installed: bin != "", Auth: "none"}
	if bin != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, bin, "--version")
		cmd.Env = cliEnv(bin)
		if out, err := cmd.Output(); err == nil {
			ap.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
		cancel()
	}
	exists := func(p string) bool { _, err := os.Stat(p); return p != "" && err == nil }
	if agent == board.Claude {
		home := claudeHome()
		switch {
		case os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "":
			ap.Auth = "token"
		case exists(filepath.Join(home, ".credentials.json")) || claudeAccountSaved(home):
			ap.Auth = "login"
		}
	} else {
		switch {
		case os.Getenv("CODEX_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "":
			ap.Auth = "token"
		case codexHome() != "" && exists(filepath.Join(codexHome(), "auth.json")):
			ap.Auth = "login"
		}
	}
	return ap
}

// claudeAccountSaved says whether Claude Code has an account signed in
// whose credentials it keeps elsewhere (the macOS keychain): its config
// file names the account.
func claudeAccountSaved(home string) bool {
	if home == "" {
		return false
	}
	cfg := filepath.Join(filepath.Dir(home), ".claude.json")
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		cfg = filepath.Join(home, ".claude.json")
	}
	b, err := os.ReadFile(cfg)
	return err == nil && bytes.Contains(b, []byte(`"oauthAccount"`))
}

// handleAgentsInstall installs an agent CLI in a VM, in the background:
// 202 now, progress as "install" messages on /v1/jx/board/events.
func (b *jxBoard) handleAgentsInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
		Agent  string `json:"agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !board.ValidAgent(req.Agent) {
		writeErr(w, http.StatusBadRequest, errors.New(`agent must be "claude" or "codex"`))
		return
	}
	if req.Target == "" || req.Target == board.HostTarget {
		writeErr(w, http.StatusBadRequest, errors.New("install targets a VM; the host's CLIs are installed by hand"))
		return
	}
	if b.s.VMs == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("no VM backend"))
		return
	}
	if _, err := b.s.VMs.Get(r.Context(), req.Target); err != nil {
		writeErr(w, errCode(err), err)
		return
	}
	key := req.Target + "/" + req.Agent
	b.installMu.Lock()
	if b.installs[key] {
		b.installMu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "installing"})
		return
	}
	b.installs[key] = true
	b.installMu.Unlock()
	go b.install(req.Target, req.Agent)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "installing"})
}

func (b *jxBoard) install(vm, agent string) {
	defer func() {
		b.installMu.Lock()
		delete(b.installs, vm+"/"+agent)
		b.installMu.Unlock()
	}()
	defer b.s.JXHold(vm, "board")()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	status := func(text string) {
		b.broadcast(map[string]any{"type": "install", "target": vm, "agent": agent, "text": text})
	}
	end := func(err error) {
		m := map[string]any{"type": "install", "target": vm, "agent": agent, "done": true}
		if err != nil {
			m["error"] = err.Error()
			m["text"] = "install failed: " + err.Error()
		} else {
			m["text"] = board.Titles[agent] + " is ready"
		}
		b.broadcast(m)
	}
	status(fmt.Sprintf("installing %s in %s", board.Titles[agent], vm))
	rem, err := b.vmRemote(ctx, vm, status)
	if err == nil {
		unlock := b.guestLock(vm)
		err = board.Install(ctx, rem, agent, status)
		if err == nil {
			err = b.guests.WriteCreds(ctx, vm, rem, agent, true)
		}
		unlock()
	}
	end(err)
}

func (b *jxBoard) handleSecretsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, b.secrets.Status())
}

// handleSecretsPut sets the agent secrets: a field left out stays as it
// is, "" clears it. The answer says only which are set.
func (b *jxBoard) handleSecretsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Claude *string `json:"claude_oauth_token"`
		Codex  *string `json:"codex_api_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("body must be {claude_oauth_token?, codex_api_key?} with string values"))
		return
	}
	if err := b.secrets.Set(req.Claude, req.Codex); err != nil {
		boardErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b.secrets.Status())
}
