package board

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Guest scripts. Each is plain POSIX sh for Debian (bash/dash, GNU tools)
// and Alpine (busybox ash and tools); Remote runs it under sh. User-level
// installs land in ~/.local/bin, which no login profile is trusted to put
// on PATH, so every script puts it there itself.
const guestPath = `export PATH="$HOME/.local/bin:$PATH"` + "\n"

// envPath is the guest's credentials file (EnvFile), sourced by turns.
const envPath = `$HOME/.config/exe/agent.env`

// Titles name agents in status lines.
var Titles = map[string]string{Claude: "Claude Code", Codex: "Codex"}

// AgentProbe is what a target says about one agent CLI.
type AgentProbe struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// Auth is "token" (a credential in the environment the turn runs
	// with), "login" (the CLI's own sign-in) or "none".
	Auth string `json:"auth"`
}

// Probe is a target's state as the Board needs it.
type Probe struct {
	OS    string // "debian", "alpine" or "other"
	Arch  string
	Path  map[string]string // agent -> CLI path
	Ver   map[string]string // agent -> --version
	Login map[string]bool   // agent -> its own sign-in file exists
	Token map[string]bool   // agent -> agent.env carries its credential
	Has   map[string]bool   // curl, git, tmux, bash
}

// Agent sums up one agent's probe.
func (p Probe) Agent(a string) AgentProbe {
	ap := AgentProbe{Installed: p.Path[a] != "", Version: p.Ver[a], Auth: "none"}
	switch {
	case p.Token[a]:
		ap.Auth = "token"
	case p.Login[a]:
		ap.Auth = "login"
	}
	return ap
}

// ProbeScript prints the target's state as key=value lines (ParseProbe).
const ProbeScript = guestPath + `os=other
if [ -f /etc/alpine-release ]; then os=alpine; elif [ -f /etc/debian_version ]; then os=debian; fi
echo "os=$os"
echo "arch=$(uname -m)"
for a in claude codex; do
  p=$(command -v "$a" 2>/dev/null)
  echo "path_$a=$p"
  if [ -n "$p" ]; then echo "ver_$a=$("$a" --version 2>/dev/null | head -n1)"; fi
done
[ -f "$HOME/.claude/.credentials.json" ] && echo login_claude=1
[ -f "$HOME/.codex/auth.json" ] && echo login_codex=1
f="` + envPath + `"
if [ -f "$f" ]; then
  grep -q '^export CLAUDE_CODE_OAUTH_TOKEN=' "$f" && echo token_claude=1
  grep -q '^export OPENAI_API_KEY=' "$f" && echo token_codex=1
fi
for t in curl git tmux bash; do command -v "$t" >/dev/null 2>&1 && echo "has_$t=1"; done
exit 0
`

// ReadyScript is the probe a turn runs first: only what Prepare needs to
// decide on an install, without starting the CLIs for their versions.
const ReadyScript = guestPath + `for a in claude codex; do echo "path_$a=$(command -v "$a" 2>/dev/null)"; done
for t in curl git tmux bash; do command -v "$t" >/dev/null 2>&1 && echo "has_$t=1"; done
exit 0
`

// ParseProbe reads ProbeScript's (or ReadyScript's) output.
func ParseProbe(out string) Probe {
	p := Probe{Path: map[string]string{}, Ver: map[string]string{}, Login: map[string]bool{},
		Token: map[string]bool{}, Has: map[string]bool{}}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		pre, name, _ := strings.Cut(k, "_")
		switch {
		case k == "os":
			p.OS = v
		case k == "arch":
			p.Arch = v
		case pre == "path":
			p.Path[name] = v
		case pre == "ver":
			p.Ver[name] = v
		case pre == "login":
			p.Login[name] = v == "1"
		case pre == "token":
			p.Token[name] = v == "1"
		case pre == "has":
			p.Has[name] = v == "1"
		}
	}
	return p
}

// prereqScript installs what the CLIs and the terminal need: curl, git,
// tmux, and on Alpine the glibc-free runtime Claude Code's native build
// wants (libgcc, libstdc++, ripgrep) and bash for its installer. Root
// comes from sudo (Debian) or doas (Alpine), never by prompting.
const prereqScript = `SUDO=""
if [ "$(id -u)" != 0 ]; then
  if command -v sudo >/dev/null 2>&1; then SUDO="sudo -n"; elif command -v doas >/dev/null 2>&1; then SUDO="doas -n"; fi
fi
if [ -f /etc/alpine-release ]; then
  pk="bash curl ca-certificates libgcc libstdc++ ripgrep git tmux"
  if [ "$(apk info -e $pk 2>/dev/null | wc -l)" -ne 8 ]; then
    echo "exe: apk add $pk"
    $SUDO apk add --no-cache $pk || exit 1
  fi
else
  need=""
  for t in curl git tmux; do command -v "$t" >/dev/null 2>&1 || need="$need $t"; done
  if [ -n "$need" ]; then
    echo "exe: apt-get install$need"
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get update -q || exit 1
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -q ca-certificates$need || exit 1
  fi
fi
`

const claudeInstall = `if ! command -v claude >/dev/null 2>&1; then
  echo "exe: installing Claude Code (claude.ai/install.sh)"
  curl -fsSL https://claude.ai/install.sh | bash || exit 1
fi
command -v claude >/dev/null 2>&1 || { echo "claude is not on PATH after the install" >&2; exit 1; }
echo "exe: $(claude --version 2>/dev/null | head -n1)"
`

// codexInstall fetches the static musl build, which runs on Debian and
// Alpine alike.
const codexInstall = `if ! command -v codex >/dev/null 2>&1; then
  case "$(uname -m)" in
    x86_64|amd64) a=x86_64 ;;
    aarch64|arm64) a=aarch64 ;;
    *) echo "no Codex build for $(uname -m)" >&2; exit 1 ;;
  esac
  echo "exe: installing Codex (codex-$a-unknown-linux-musl)"
  mkdir -p "$HOME/.local/bin" || exit 1
  t=$(mktemp -d) || exit 1
  if curl -fsSL "https://github.com/openai/codex/releases/latest/download/codex-$a-unknown-linux-musl.tar.gz" | tar -xzf - -C "$t"; then
    f=$(find "$t" -type f -name 'codex*' | head -n1)
    [ -n "$f" ] && mv -f "$f" "$HOME/.local/bin/codex" && chmod 755 "$HOME/.local/bin/codex"
  fi
  rm -rf "$t"
fi
command -v codex >/dev/null 2>&1 || { echo "codex is not on PATH after the install" >&2; exit 1; }
echo "exe: $(codex --version 2>/dev/null | head -n1)"
`

// InstallScript installs agent's CLI and the prerequisites in a guest.
func InstallScript(agent string) string {
	s := guestPath + prereqScript
	if agent == Codex {
		return s + codexInstall
	}
	return s + claudeInstall
}

// writeEnvScript replaces agent.env with stdin, 0600 in a 0700 folder.
const writeEnvScript = `umask 077
d="$HOME/.config/exe"
mkdir -p "$d" && chmod 700 "$d" || exit 1
cat > "$d/agent.env.new" && chmod 600 "$d/agent.env.new" && mv -f "$d/agent.env.new" "$d/agent.env"
`

// clearEnvScript empties agent.env when the daemon wrote it: the secrets
// were cleared. A file of the person's own is left alone.
const clearEnvScript = `f="` + envPath + `"
if [ -f "$f" ] && head -n1 "$f" | grep -q '^# written by exe'; then : > "$f"; fi
`

// codexLoginScript signs Codex in with the key from agent.env, fed on
// stdin, so it never shows in a process list.
const codexLoginScript = guestPath + `f="` + envPath + `"
[ -f "$f" ] && . "$f"
if [ -n "$OPENAI_API_KEY" ] && command -v codex >/dev/null 2>&1; then
  printf '%s\n' "$OPENAI_API_KEY" | codex login --with-api-key >/dev/null 2>&1 || echo "codex login failed" >&2
fi
`

// CLIArgs are the agent CLI's arguments for one headless turn, the prompt
// on stdin. On the host the CLI runs with the safer permissions: the host
// is not a sandbox.
func CLIArgs(agent, session string, fork, host bool) []string {
	if agent == Codex {
		// exec's own flags go before the resume subcommand: clap accepts a
		// parent's flags there in every version
		args := []string{"exec", "--json", "--skip-git-repo-check"}
		if host {
			args = append(args, "--sandbox", "workspace-write")
		} else {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		}
		if session != "" {
			args = append(args, "resume", session)
		}
		return append(args, "-")
	}
	mode := "bypassPermissions"
	if host {
		mode = "acceptEdits"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode}
	if session != "" {
		args = append(args, "--resume", session)
		if fork {
			args = append(args, "--fork-session")
		}
	}
	return args
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ValidSessionID reports whether id can be a CLI session id. Ids reach
// command lines (quoted), so anything odd is refused outright.
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// TurnScript runs one turn in a guest: ~/work (created) as the folder, or
// the folder a resumed session was started in, so the CLI finds it;
// agent.env sourced; the CLI exec'd so it owns the script's process group.
func TurnScript(spec Spec) string {
	var b strings.Builder
	b.WriteString(guestPath)
	b.WriteString("[ -f /etc/alpine-release ] && export USE_BUILTIN_RIPGREP=0\n")
	b.WriteString(`if [ -f "` + envPath + `" ]; then . "` + envPath + `"; fi` + "\n")
	b.WriteString(`mkdir -p "$HOME/work" && cd "$HOME/work" || exit 1` + "\n")
	if spec.SessionID != "" {
		q := shq(spec.SessionID)
		if spec.Agent == Codex {
			b.WriteString(`f=$(find "$HOME/.codex/sessions" -type f -name "rollout-*"` + q + `".jsonl" 2>/dev/null | head -n1)` + "\n")
		} else {
			b.WriteString(`f=$(ls "$HOME"/.claude/projects/*/` + q + `.jsonl 2>/dev/null | head -n1)` + "\n")
		}
		b.WriteString(`if [ -n "$f" ]; then d=$(head -c 262144 "$f" | grep -o '"cwd":"[^"]*"' | head -n1 | sed 's/^"cwd":"//; s/"$//'); [ -n "$d" ] && [ -d "$d" ] && cd "$d"; fi` + "\n")
	}
	b.WriteString("exec " + spec.Agent)
	for _, a := range CLIArgs(spec.Agent, spec.SessionID, spec.Fork, false) {
		b.WriteString(" " + shq(a))
	}
	b.WriteString("\n")
	return b.String()
}

// Guests readies VMs for turns: the CLI installed, the credentials of the
// current secrets generation written. What it did is remembered for the
// daemon's life, so a turn costs one probe, not a reinstall.
type Guests struct {
	Secrets *SecretStore

	mu    sync.Mutex
	creds map[string]uint64 // vm/agent -> secrets generation written
}

func NewGuests(secrets *SecretStore) *Guests {
	return &Guests{Secrets: secrets, creds: map[string]uint64{}}
}

// RunProbe runs ProbeScript on rem.
func RunProbe(ctx context.Context, rem Remote) (Probe, error) { return runProbe(ctx, rem, ProbeScript) }

func runProbe(ctx context.Context, rem Remote, script string) (Probe, error) {
	var out bytes.Buffer
	stderr := NewTail(2048)
	if err := rem.Exec(ctx, script, nil, &out, stderr); err != nil {
		return Probe{}, exitErr(err, stderr)
	}
	return ParseProbe(out.String()), nil
}

// Install installs agent's CLI on rem, reporting the steps it takes
// through status.
func Install(ctx context.Context, rem Remote, agent string, status func(string)) error {
	stderr := NewTail(4096)
	lines := NewLineWriter(func(b []byte) {
		s := strings.TrimSpace(stripANSI(string(b)))
		if t, ok := strings.CutPrefix(s, "exe: "); ok {
			status(oneLine(t, 200))
		}
	})
	both := NewLineWriter(func(b []byte) {
		stderr.Write(append(b, '\n'))
		s := strings.TrimSpace(stripANSI(string(b)))
		if errorish.MatchString(s) {
			status(oneLine(s, 200))
		}
	})
	err := rem.Exec(ctx, InstallScript(agent), nil, lines, both)
	lines.Flush()
	both.Flush()
	if err != nil {
		return fmt.Errorf("installing %s: %w", Titles[agent], exitErr(err, stderr))
	}
	return nil
}

// Prepare readies vm for a turn of agent: installs the CLI when missing
// and writes the credentials when this daemon has not written the current
// ones there yet. With no secret set, the guest's own login is used.
func (g *Guests) Prepare(ctx context.Context, vm string, rem Remote, agent string, status func(string)) error {
	p, err := runProbe(ctx, rem, ReadyScript)
	if err != nil {
		return fmt.Errorf("probing %s: %w", vm, err)
	}
	if p.Path[agent] == "" || !p.Has["git"] || !p.Has["tmux"] {
		status(fmt.Sprintf("installing %s in %s", Titles[agent], vm))
		if err := Install(ctx, rem, agent, status); err != nil {
			return err
		}
	}
	return g.WriteCreds(ctx, vm, rem, agent, false)
}

// WriteCreds writes the current secrets to vm's agent.env (and signs
// Codex in with its key) unless that generation is there already, or
// force. The secrets travel on stdin.
func (g *Guests) WriteCreds(ctx context.Context, vm string, rem Remote, agent string, force bool) error {
	sec, gen := g.Secrets.Get()
	key := vm + "/" + agent
	g.mu.Lock()
	done := g.creds[key] == gen
	g.mu.Unlock()
	if done && !force {
		return nil
	}
	stderr := NewTail(1024)
	if sec == (Secrets{}) {
		if err := rem.Exec(ctx, clearEnvScript, nil, nil, stderr); err != nil {
			return fmt.Errorf("clearing credentials in %s: %w", vm, exitErr(err, stderr))
		}
	} else {
		if err := rem.Exec(ctx, writeEnvScript, strings.NewReader(EnvFile(sec)), nil, stderr); err != nil {
			return fmt.Errorf("writing credentials to %s: %w", vm, exitErr(err, stderr))
		}
		if agent == Codex && sec.CodexAPIKey != "" {
			if err := rem.Exec(ctx, codexLoginScript, nil, nil, stderr); err != nil {
				return fmt.Errorf("codex login in %s: %w", vm, exitErr(err, stderr))
			}
		}
	}
	g.mu.Lock()
	g.creds[key] = gen
	g.mu.Unlock()
	return nil
}

// Forget drops what is remembered about vm (it was deleted or recreated).
func (g *Guests) Forget(vm string) {
	g.mu.Lock()
	for k := range g.creds {
		if strings.HasPrefix(k, vm+"/") {
			delete(g.creds, k)
		}
	}
	g.mu.Unlock()
}

// RunVMTurn runs one prepared turn on rem: the prompt on stdin, stdout
// lines to line. It returns the CLI's exit, worded with its stderr.
func RunVMTurn(ctx context.Context, rem Remote, spec Spec, line func([]byte)) error {
	stderr := NewTail(4096)
	lw := NewLineWriter(line)
	err := rem.Exec(ctx, TurnScript(spec), strings.NewReader(spec.Prompt), lw, stderr)
	lw.Flush()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return exitErr(err, stderr)
}
