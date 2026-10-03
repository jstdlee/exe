//go:build linux

package board

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// localSh is a Remote running scripts with this machine's sh under a fake
// HOME: the guest scripts themselves, run for real (Linux tools stand in
// for a Debian guest's).
type localSh struct{ home string }

func (l localSh) Exec(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = []string{"HOME=" + l.home, "PATH=/usr/local/bin:/usr/bin:/bin"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return &ExitError{Code: ee.ExitCode()}
	}
	return err
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestGuestScriptsParse(t *testing.T) {
	for name, s := range map[string]string{
		"probe": ProbeScript, "ready": ReadyScript, "install claude": InstallScript(Claude),
		"install codex": InstallScript(Codex), "env": writeEnvScript, "clear": clearEnvScript,
		"login": codexLoginScript, "sessions claude": SessionsScript(Claude), "sessions codex": SessionsScript(Codex),
		"turn": TurnScript(Spec{Agent: Codex, SessionID: "abc"}), "pgid": pgidPreamble,
	} {
		if out, err := exec.Command("sh", "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", name, err, out)
		}
	}
}

func TestSessionsScriptOnRealFiles(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude/projects/-home-exe-work/0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10.jsonl"),
		`{"type":"summary","summary":"x"}`+"\n"+
			`{"cwd":"/home/exe/work","type":"user","message":{"role":"user","content":"fix the login bug"},"entrypoint":"cli"}`+"\n"+
			`{"type":"ai-title","aiTitle":"Login fix"}`+"\n")
	write(t, filepath.Join(home, ".codex/sessions/2025/10/01/rollout-2025-10-01T10-00-00-0199a213-81c0-7800-8aa1-bbab2a035a53.jsonl"),
		`{"timestamp":"2025-10-01T10:00:00Z","type":"session_meta","payload":{"id":"0199a213-81c0-7800-8aa1-bbab2a035a53","timestamp":"2025-10-01T10:00:00Z","cwd":"/home/exe/app","originator":"codex_cli_rs","cli_version":"0.46.0","instructions":"`+strings.Repeat("long ", 2000)+`","source":"cli"}}`+"\n"+
			`{"type":"event_msg","payload":{"type":"user_message","message":"add a NOTES.md","images":[]}}`+"\n")
	rem := localSh{home}
	cl, err := ListSessions(context.Background(), rem, Claude)
	if err != nil || len(cl) != 1 || cl[0].Title != "Login fix" || cl[0].Cwd != "/home/exe/work" || cl[0].UpdatedAt.IsZero() {
		t.Errorf("claude = %+v %v", cl, err)
	}
	cx, err := ListSessions(context.Background(), rem, Codex)
	if err != nil || len(cx) != 1 || cx[0].ID != "0199a213-81c0-7800-8aa1-bbab2a035a53" || cx[0].Title != "add a NOTES.md" || cx[0].Cwd != "/home/exe/app" {
		t.Errorf("codex = %+v %v", cx, err)
	}
	empty, err := ListSessions(context.Background(), localSh{t.TempDir()}, Codex)
	if err != nil || len(empty) != 0 {
		t.Errorf("no sessions = %+v %v", empty, err)
	}
}

// The turn script finds a resumed session's folder, sources agent.env,
// and hands the CLI its arguments and the prompt intact.
func TestTurnScriptRunsTheCLI(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "proj dir")
	os.MkdirAll(proj, 0o755)
	write(t, filepath.Join(home, ".claude/projects/-proj/5d3e8f20-1a2b-4c3d-9e8f-001122334455.jsonl"),
		`{"cwd":"`+proj+`","type":"user","message":{"role":"user","content":"hi"}}`+"\n")
	write(t, filepath.Join(home, ".config/exe/agent.env"), "export CLAUDE_CODE_OAUTH_TOKEN='tok'\n")
	write(t, filepath.Join(home, ".local/bin/claude"), "#!/bin/sh\npwd\necho \"$CLAUDE_CODE_OAUTH_TOKEN\"\nfor a in \"$@\"; do echo \"[$a]\"; done\ncat\n")

	var out bytes.Buffer
	spec := Spec{Agent: Claude, SessionID: "5d3e8f20-1a2b-4c3d-9e8f-001122334455", Fork: true, Prompt: "it's \"quoted\"\n$HOME `x`"}
	err := RunVMTurn(context.Background(), localSh{home}, spec, func(b []byte) { out.Write(append(b, '\n')) })
	if err != nil {
		t.Fatal(err)
	}
	want := proj + "\ntok\n[-p]\n[--output-format]\n[stream-json]\n[--verbose]\n[--permission-mode]\n[bypassPermissions]\n[--resume]\n[5d3e8f20-1a2b-4c3d-9e8f-001122334455]\n[--fork-session]\nit's \"quoted\"\n$HOME `x`\n"
	if out.String() != want {
		t.Errorf("CLI saw:\n%s\nwant:\n%s", out.String(), want)
	}

	// a new thread runs in ~/work, created
	out.Reset()
	if err := RunVMTurn(context.Background(), localSh{home}, Spec{Agent: Claude, Prompt: "x"}, func(b []byte) { out.Write(append(b, '\n')) }); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), filepath.Join(home, "work")+"\n") {
		t.Errorf("new thread ran in %q", strings.SplitN(out.String(), "\n", 2)[0])
	}

	// a missing CLI is an exit with its reason
	os.Remove(filepath.Join(home, ".local/bin/claude"))
	err = RunVMTurn(context.Background(), localSh{home}, Spec{Agent: Claude, Prompt: "x"}, func([]byte) {})
	if err == nil || !strings.Contains(err.Error(), "exit status 127") {
		t.Errorf("missing CLI = %v", err)
	}
}

func TestWriteEnvScriptKeepsSecretsPrivate(t *testing.T) {
	home := t.TempDir()
	rem := localSh{home}
	g := NewGuests(NewSecretStore(filepath.Join(t.TempDir(), "s.json")))
	tok := "sk-ant-oat01-x'y"
	g.Secrets.Set(&tok, nil)
	if err := g.WriteCreds(context.Background(), "dev", rem, Claude, false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".config/exe/agent.env")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("agent.env = %v %v", st, err)
	}
	if d, _ := os.Stat(filepath.Dir(p)); d.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode = %v", d.Mode().Perm())
	}
	out, _ := exec.Command("sh", "-c", `. "$1"; printf %s "$CLAUDE_CODE_OAUTH_TOKEN"`, "sh", p).Output()
	if string(out) != tok {
		t.Errorf("sourced token = %q", out)
	}
	empty := ""
	g.Secrets.Set(&empty, nil)
	g.WriteCreds(context.Background(), "dev", rem, Claude, false)
	if b, _ := os.ReadFile(p); len(b) != 0 {
		t.Errorf("cleared agent.env = %q", b)
	}
	// a file of the person's own is not touched by a clear
	os.WriteFile(p, []byte("export ANTHROPIC_API_KEY=mine\n"), 0o600)
	g.WriteCreds(context.Background(), "dev", rem, Claude, true)
	if b, _ := os.ReadFile(p); string(b) != "export ANTHROPIC_API_KEY=mine\n" {
		t.Errorf("own agent.env = %q", b)
	}
}
