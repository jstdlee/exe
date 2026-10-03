package board

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGuest is a Remote that answers the Board's scripts the way a guest
// would, and records what it was asked.
type fakeGuest struct {
	mu        sync.Mutex
	scripts   []string
	stdins    []string
	installed map[string]bool
	turn      func(stdout io.Writer) error
}

func (g *fakeGuest) Exec(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	g.mu.Lock()
	g.scripts = append(g.scripts, script)
	g.stdins = append(g.stdins, in)
	g.mu.Unlock()
	if stdout == nil {
		stdout = io.Discard
	}
	switch {
	case script == ProbeScript || script == ReadyScript:
		io.WriteString(stdout, "os=alpine\narch=aarch64\n")
		for _, a := range []string{Claude, Codex} {
			if g.installed[a] {
				io.WriteString(stdout, "path_"+a+"=/home/exe/.local/bin/"+a+"\nver_"+a+"=1.0\n")
			} else {
				io.WriteString(stdout, "path_"+a+"=\n")
			}
		}
		io.WriteString(stdout, "has_git=1\nhas_tmux=1\nhas_curl=1\n")
	case strings.Contains(script, "apk add"):
		io.WriteString(stdout, "exe: installing Claude Code (claude.ai/install.sh)\nsome chatter\n")
		if strings.Contains(script, "claude.ai/install.sh") {
			g.installed[Claude] = true
		}
		if strings.Contains(script, "codex-$a-unknown-linux-musl") {
			g.installed[Codex] = true
		}
	case strings.HasPrefix(script, guestPath+"[ -f /etc/alpine-release ]") && g.turn != nil:
		return g.turn(stdout)
	}
	return nil
}

func (g *fakeGuest) calls() ([]string, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.scripts...), append([]string(nil), g.stdins...)
}

func TestPrepareInstallsAndWritesCredsOnStdin(t *testing.T) {
	secrets := NewSecretStore(filepath.Join(t.TempDir(), "secrets.json"))
	tok, key := "sk-ant-oat01-SECRETVALUE", "sk-proj-OPENAISECRET"
	if err := secrets.Set(&tok, &key); err != nil {
		t.Fatal(err)
	}
	g := NewGuests(secrets)
	guest := &fakeGuest{installed: map[string]bool{}}
	var status []string
	if err := g.Prepare(context.Background(), "dev", guest, Claude, func(s string) { status = append(status, s) }); err != nil {
		t.Fatal(err)
	}
	scripts, stdins := guest.calls()
	if len(scripts) != 3 || scripts[0] != ReadyScript || !strings.Contains(scripts[1], "claude.ai/install.sh") || scripts[2] != writeEnvScript {
		t.Fatalf("scripts = %q", scripts)
	}
	if status[0] != "installing Claude Code in dev" || status[1] != "installing Claude Code (claude.ai/install.sh)" || len(status) != 2 {
		t.Errorf("status = %q", status)
	}
	if !strings.Contains(stdins[2], "export CLAUDE_CODE_OAUTH_TOKEN='"+tok+"'") || !strings.Contains(stdins[2], "export OPENAI_API_KEY='"+key+"'") {
		t.Errorf("env file = %q", stdins[2])
	}
	for _, s := range scripts {
		if strings.Contains(s, tok) || strings.Contains(s, key) {
			t.Fatalf("a secret reached a command line: %q", s)
		}
	}

	// the same generation is not written twice; a change is
	if err := g.Prepare(context.Background(), "dev", guest, Claude, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if scripts, _ = guest.calls(); len(scripts) != 4 {
		t.Fatalf("second prepare ran %q", scripts[3:])
	}
	empty := ""
	secrets.Set(&empty, &empty)
	g.Prepare(context.Background(), "dev", guest, Claude, func(string) {})
	if scripts, _ = guest.calls(); scripts[len(scripts)-1] != clearEnvScript {
		t.Errorf("cleared secrets ran %q", scripts[len(scripts)-1])
	}

	// codex signs in with its key from the env file, never the command line
	secrets.Set(nil, &key)
	if err := g.Prepare(context.Background(), "dev", guest, Codex, func(string) {}); err != nil {
		t.Fatal(err)
	}
	scripts, _ = guest.calls()
	n := len(scripts)
	if !strings.Contains(scripts[n-3], "codex-$a-unknown-linux-musl") || scripts[n-2] != writeEnvScript || scripts[n-1] != codexLoginScript {
		t.Errorf("codex prepare = %q", scripts[n-3:])
	}
}

func TestTurnScript(t *testing.T) {
	s := TurnScript(Spec{Agent: Claude, SessionID: "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10", Fork: true})
	for _, want := range []string{
		`export PATH="$HOME/.local/bin:$PATH"`,
		`. "$HOME/.config/exe/agent.env"`,
		`mkdir -p "$HOME/work" && cd "$HOME/work"`,
		`/.claude/projects/*/'0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10'.jsonl`,
		`exec claude '-p' '--output-format' 'stream-json' '--verbose' '--permission-mode' 'bypassPermissions' '--resume' '0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10' '--fork-session'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("claude turn script lacks %q:\n%s", want, s)
		}
	}
	c := TurnScript(Spec{Agent: Codex, SessionID: "0199a213-81c0-7800-8aa1-bbab2a035a53"})
	if !strings.Contains(c, `exec codex 'exec' '--json' '--skip-git-repo-check' '--dangerously-bypass-approvals-and-sandbox' 'resume' '0199a213-81c0-7800-8aa1-bbab2a035a53' '-'`) ||
		!strings.Contains(c, `"$HOME/.codex/sessions"`) {
		t.Errorf("codex turn script:\n%s", c)
	}
	if n := TurnScript(Spec{Agent: Codex}); strings.Contains(n, "resume") || strings.Contains(n, "sessions") {
		t.Errorf("new codex thread script:\n%s", n)
	}
}

func TestCLIArgsHostIsSafer(t *testing.T) {
	if got := strings.Join(CLIArgs(Claude, "", false, true), " "); got != "-p --output-format stream-json --verbose --permission-mode acceptEdits" {
		t.Errorf("host claude = %s", got)
	}
	if got := strings.Join(CLIArgs(Codex, "", false, true), " "); got != "exec --json --skip-git-repo-check --sandbox workspace-write -" {
		t.Errorf("host codex = %s", got)
	}
}

func TestParseProbe(t *testing.T) {
	p := ParseProbe("os=debian\narch=x86_64\npath_claude=/home/exe/.local/bin/claude\nver_claude=2.0.14 (Claude Code)\npath_codex=\nlogin_codex=1\ntoken_claude=1\nhas_git=1\n")
	if p.OS != "debian" || p.Arch != "x86_64" || !p.Has["git"] || p.Has["tmux"] {
		t.Errorf("probe = %+v", p)
	}
	if c := p.Agent(Claude); !c.Installed || c.Version != "2.0.14 (Claude Code)" || c.Auth != "token" {
		t.Errorf("claude = %+v", c)
	}
	if c := p.Agent(Codex); c.Installed || c.Auth != "login" {
		t.Errorf("codex = %+v", c)
	}
}

func TestValidSessionID(t *testing.T) {
	for id, ok := range map[string]bool{
		"0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10": true,
		"abc_DEF.1":                            true,
		"":                                     false,
		"x'; rm -rf ~":                         false,
		"a b":                                  false,
	} {
		if ValidSessionID(id) != ok {
			t.Errorf("ValidSessionID(%q) != %v", id, ok)
		}
	}
}
