// Package tools is the VM Tools catalog: terminal programs a VM window can
// open with one click. Each entry's Command is one self-contained shell
// line for the guest: install the package if the binary is missing (apt on
// Debian, apk on Alpine), then exec the program in the terminal. Entries
// with no program open a login shell once the package is in, for tools you
// run with arguments (ripgrep, jq).
package tools

import (
	"fmt"
	"strings"

	"exe/internal/jx/board"
)

// Tool is one catalog entry.
type Tool struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Desc     string `json:"desc"`
	// Bin is the binary that shows the tool is installed (Debian names
	// some differently: fdfind, batcat). Run is what the terminal runs;
	// "" opens a shell after install.
	Bin string `json:"-"`
	Run string `json:"-"`
	// Debian and Alpine name the package; "" = not packaged there.
	Debian string `json:"debian,omitempty"`
	Alpine string `json:"alpine,omitempty"`
	// Agent marks Claude Code / Codex, installed by the Board's script.
	Agent string `json:"agent,omitempty"`
	// Install is the guest installer of an agent the Board does not run
	// (it follows board.Prereqs, which sets SUDO).
	Install string `json:"-"`
	// Launch: an agent CLI the desktop starts through the provider dialog
	// (llm.Agents), so its terminal runs LaunchCommand.
	Launch bool `json:"launch,omitempty"`

	Command string `json:"command"`
}

// Categories in display order.
var Categories = []string{"Agents", "Monitor", "Files", "Git", "Edit", "Data", "Network", "Shell tools", "Languages"}

var catalog = []Tool{
	{ID: "claude", Name: "Claude Code", Category: "Agents", Desc: "Anthropic's coding agent", Agent: board.Claude, Bin: "claude", Run: "claude", Launch: true},
	{ID: "codex", Name: "Codex", Category: "Agents", Desc: "OpenAI's coding agent", Agent: board.Codex, Bin: "codex", Run: "codex", Launch: true},
	{ID: "opencode", Name: "OpenCode", Category: "Agents", Desc: "open source coding agent, any provider", Bin: "opencode", Run: "opencode", Install: opencodeInstall, Launch: true},
	{ID: "pi", Name: "Pi", Category: "Agents", Desc: "minimal coding agent (pi-coding-agent)", Bin: "pi", Run: "pi", Install: piInstall, Launch: true},
	{ID: "omp", Name: "Oh My Pi", Category: "Agents", Desc: "omp: Pi with batteries included", Bin: "omp", Run: "omp", Install: ompInstall, Launch: true},
	{ID: "grok", Name: "Grok Build", Category: "Agents", Desc: "xAI's coding agent", Bin: "grok", Run: "grok", Install: grokInstall, Launch: true},

	{ID: "btop", Name: "btop", Category: "Monitor", Desc: "CPU, memory, disks, processes", Bin: "btop", Run: "btop", Debian: "btop", Alpine: "btop"},
	{ID: "htop", Name: "htop", Category: "Monitor", Desc: "process viewer", Bin: "htop", Run: "htop", Debian: "htop", Alpine: "htop"},
	{ID: "glances", Name: "Glances", Category: "Monitor", Desc: "system overview", Bin: "glances", Run: "glances", Debian: "glances", Alpine: "glances"},
	{ID: "duf", Name: "duf", Category: "Monitor", Desc: "disk usage by mount", Bin: "duf", Run: "duf; exec ${SHELL:-/bin/sh} -l", Debian: "duf"},
	{ID: "iftop", Name: "iftop", Category: "Monitor", Desc: "bandwidth per connection", Bin: "iftop", Run: `S=sudo; command -v sudo >/dev/null || S=doas; $S iftop`, Debian: "iftop", Alpine: "iftop"},
	{ID: "nethogs", Name: "NetHogs", Category: "Monitor", Desc: "bandwidth per process", Bin: "nethogs", Run: `S=sudo; command -v sudo >/dev/null || S=doas; $S nethogs`, Debian: "nethogs", Alpine: "nethogs"},

	{ID: "ncdu", Name: "ncdu", Category: "Files", Desc: "what fills the disk", Bin: "ncdu", Run: "ncdu ~", Debian: "ncdu", Alpine: "ncdu"},
	{ID: "nnn", Name: "nnn", Category: "Files", Desc: "fast file manager", Bin: "nnn", Run: "nnn ~", Debian: "nnn", Alpine: "nnn"},
	{ID: "mc", Name: "Midnight Commander", Category: "Files", Desc: "two-pane file manager", Bin: "mc", Run: "mc", Debian: "mc", Alpine: "mc"},
	{ID: "ranger", Name: "ranger", Category: "Files", Desc: "file manager with previews", Bin: "ranger", Run: "ranger ~", Debian: "ranger", Alpine: "ranger"},
	{ID: "tree", Name: "tree", Category: "Files", Desc: "folder tree", Bin: "tree", Debian: "tree", Alpine: "tree"},

	{ID: "lazygit", Name: "lazygit", Category: "Git", Desc: "git in a terminal UI", Bin: "lazygit", Run: "cd ~/work 2>/dev/null; lazygit", Debian: "lazygit", Alpine: "lazygit"},
	{ID: "gitui", Name: "gitui", Category: "Git", Desc: "git terminal UI (Rust)", Bin: "gitui", Run: "cd ~/work 2>/dev/null; gitui", Alpine: "gitui"},
	{ID: "tig", Name: "tig", Category: "Git", Desc: "git history browser", Bin: "tig", Run: "cd ~/work 2>/dev/null; tig", Debian: "tig", Alpine: "tig"},
	{ID: "gh", Name: "GitHub CLI", Category: "Git", Desc: "gh: PRs, issues, runs", Bin: "gh", Debian: "gh", Alpine: "github-cli"},
	{ID: "delta", Name: "delta", Category: "Git", Desc: "readable git diffs", Bin: "delta", Debian: "git-delta", Alpine: "delta"},

	{ID: "vim", Name: "Vim", Category: "Edit", Desc: "editor", Bin: "vim", Run: "vim", Debian: "vim", Alpine: "vim"},
	{ID: "nvim", Name: "Neovim", Category: "Edit", Desc: "editor", Bin: "nvim", Run: "nvim", Debian: "neovim", Alpine: "neovim"},
	{ID: "micro", Name: "micro", Category: "Edit", Desc: "easy editor, mouse and Ctrl-S", Bin: "micro", Run: "micro", Debian: "micro", Alpine: "micro"},
	{ID: "nano", Name: "nano", Category: "Edit", Desc: "simple editor", Bin: "nano", Run: "nano", Debian: "nano", Alpine: "nano"},

	{ID: "sqlite", Name: "SQLite", Category: "Data", Desc: "sqlite3 shell", Bin: "sqlite3", Run: "sqlite3", Debian: "sqlite3", Alpine: "sqlite"},
	{ID: "psql", Name: "psql", Category: "Data", Desc: "PostgreSQL client", Bin: "psql", Debian: "postgresql-client", Alpine: "postgresql17-client"},
	{ID: "redis-cli", Name: "redis-cli", Category: "Data", Desc: "Redis client", Bin: "redis-cli", Debian: "redis-tools", Alpine: "redis"},
	{ID: "jq", Name: "jq", Category: "Data", Desc: "JSON processor", Bin: "jq", Debian: "jq", Alpine: "jq"},

	{ID: "httpie", Name: "HTTPie", Category: "Network", Desc: "http / https requests", Bin: "http", Debian: "httpie", Alpine: "httpie"},
	{ID: "w3m", Name: "w3m", Category: "Network", Desc: "text web browser", Bin: "w3m", Run: "w3m https://duckduckgo.com/lite", Debian: "w3m", Alpine: "w3m"},
	{ID: "lynx", Name: "Lynx", Category: "Network", Desc: "text web browser", Bin: "lynx", Run: "lynx https://duckduckgo.com/lite", Debian: "lynx", Alpine: "lynx"},

	{ID: "tmux", Name: "tmux", Category: "Shell tools", Desc: "session that survives disconnects", Bin: "tmux", Run: "tmux new-session -A -s main", Debian: "tmux", Alpine: "tmux"},
	{ID: "ripgrep", Name: "ripgrep", Category: "Shell tools", Desc: "rg: fast code search", Bin: "rg", Debian: "ripgrep", Alpine: "ripgrep"},
	{ID: "fd", Name: "fd", Category: "Shell tools", Desc: "fast file finder", Bin: "fd fdfind", Debian: "fd-find", Alpine: "fd"},
	{ID: "bat", Name: "bat", Category: "Shell tools", Desc: "cat with colours", Bin: "bat batcat", Debian: "bat", Alpine: "bat"},
	{ID: "fzf", Name: "fzf", Category: "Shell tools", Desc: "fuzzy finder", Bin: "fzf", Debian: "fzf", Alpine: "fzf"},

	{ID: "python", Name: "Python", Category: "Languages", Desc: "python3 REPL", Bin: "python3", Run: "python3", Debian: "python3", Alpine: "python3"},
	{ID: "ipython", Name: "IPython", Category: "Languages", Desc: "better Python REPL", Bin: "ipython3 ipython", Run: "ipython3 2>/dev/null || ipython", Debian: "ipython3", Alpine: "ipython"},
	{ID: "node", Name: "Node.js", Category: "Languages", Desc: "node REPL", Bin: "node", Run: "node", Debian: "nodejs", Alpine: "nodejs"},
	{ID: "go", Name: "Go", Category: "Languages", Desc: "go toolchain", Bin: "go", Debian: "golang-go", Alpine: "go"},
	{ID: "rust", Name: "Rust", Category: "Languages", Desc: "rustc and cargo", Bin: "cargo", Debian: "cargo", Alpine: "cargo"},
}

// Catalog returns every tool with its Command filled in.
func Catalog() []Tool {
	out := make([]Tool, len(catalog))
	for i, t := range catalog {
		t.Command = Command(t)
		out[i] = t
	}
	return out
}

// Find returns the tool with id.
func Find(id string) (Tool, bool) {
	for _, t := range catalog {
		if t.ID == id {
			t.Command = Command(t)
			return t, true
		}
	}
	return Tool{}, false
}

// pkgFn installs distro packages: apk on Alpine, apt on Debian (without a
// fresh index first: most VMs already have one).
const pkgFn = `pkg() {
  if [ -f /etc/alpine-release ]; then $SUDO apk add --no-cache "$@"; return; fi
  $SUDO env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 install -y -q "$@" ||
    { $SUDO env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 update -q &&
      $SUDO env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 install -y -q "$@"; }
}
`

const opencodeInstall = `echo "exe: installing OpenCode (opencode.ai/install)"
command -v tar >/dev/null 2>&1 || pkg tar || exit 1
curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path || exit 1
`

const grokInstall = `echo "exe: installing Grok Build (x.ai/cli/install.sh)"
curl -fsSL https://x.ai/cli/install.sh | bash || exit 1
`

// piInstall needs Node.js 22.19 or newer: Alpine's package is new enough,
// Debian 13's (20) is not, so there Node 22 comes from nodejs.org into
// ~/.local/share/exe-node.
const piInstall = `node_ok() {
  v=$(node -v 2>/dev/null | sed 's/^v//'); [ -n "$v" ] || return 1
  maj=${v%%.*}; r=${v#*.}; min=${r%%.*}
  [ "$maj" -gt 22 ] || { [ "$maj" -eq 22 ] && [ "$min" -ge 19 ]; }
}
if ! node_ok; then
  if [ -f /etc/alpine-release ]; then
    pkg nodejs npm || exit 1
  else
    case "$(uname -m)" in x86_64|amd64) na=x64 ;; aarch64|arm64) na=arm64 ;; *) echo "no Node.js build for $(uname -m)" >&2; exit 1 ;; esac
    n=$(curl -fsSL https://nodejs.org/dist/latest-v22.x/SHASUMS256.txt | grep -o "node-v[0-9.]*-linux-$na.tar.gz" | head -n1)
    [ -n "$n" ] || { echo "cannot find Node.js 22 for linux-$na" >&2; exit 1; }
    echo "exe: installing Node.js ($n)"
    d="$HOME/.local/share/exe-node"
    rm -rf "$d" && mkdir -p "$d" "$HOME/.local/bin" || exit 1
    curl -fsSL "https://nodejs.org/dist/latest-v22.x/$n" | tar -xzf - -C "$d" --strip-components=1 || exit 1
    ln -sf "$d/bin/node" "$d/bin/npm" "$d/bin/npx" "$HOME/.local/bin/"
  fi
fi
node_ok || { echo "Node.js 22.19 or newer is needed" >&2; exit 1; }
echo "exe: installing Pi (npm @earendil-works/pi-coding-agent)"
npm install -g --prefix "$HOME/.local" @earendil-works/pi-coding-agent || exit 1
`

// ompInstall: omp runs on Bun (1.3.14 or newer), whose installer wants unzip.
const ompInstall = `command -v unzip >/dev/null 2>&1 || pkg unzip || exit 1
if command -v bun >/dev/null 2>&1; then
  bun upgrade >/dev/null 2>&1 || true
else
  echo "exe: installing Bun (bun.sh/install)"
  curl -fsSL https://bun.sh/install | bash || exit 1
fi
echo "exe: installing Oh My Pi (bun @oh-my-pi/pi-coding-agent)"
"$HOME/.bun/bin/bun" install -g @oh-my-pi/pi-coding-agent || bun install -g @oh-my-pi/pi-coding-agent || exit 1
`

// guestPATH is where the agent installers put their CLIs.
const guestPATH = `export PATH="$HOME/.local/bin:$HOME/.bun/bin:$HOME/.opencode/bin:$HOME/.grok/bin:/usr/local/bin:$PATH"; `

// shellAfter is what a tool without a program leaves you in.
const shellAfter = `exec "${SHELL:-/bin/sh}" -l`

// Command is the one shell line a VM terminal runs for t.
func Command(t Tool) string { return command(t, t.Run) }

// LaunchCommand is Command for an agent started on a provider: after the
// install it sources script (a shell word, "$HOME/..."), the file the
// daemon wrote with the agent's settings and key (llm.Script), which
// execs the CLI.
func LaunchCommand(t Tool, script string) string { return command(t, ". "+script) }

func command(t Tool, run string) string {
	var b strings.Builder
	b.WriteString(guestPATH)
	// "have" is true when any of the tool's binaries is on PATH; a Codex
	// that is not a whole package counts as missing (board.InstallScript)
	fmt.Fprintf(&b, `have() { for x in %s; do command -v "$x" >/dev/null 2>&1 && return 0; done; return 1; }; `, t.Bin)
	if t.Agent == board.Codex {
		b.WriteString(`have() { p=$(command -v codex 2>/dev/null) || return 1; case "$(readlink -f "$p" 2>/dev/null)" in */.codex/packages/*) return 0 ;; esac; [ "$p" != "$HOME/.local/bin/codex" ]; }; `)
	}
	b.WriteString(`if ! have; then echo "exe: installing ` + t.Name + `..."; `)
	if t.Agent != "" {
		// the Board's own installer: prerequisites, the CLI, PATH
		fmt.Fprintf(&b, `sh -c %s || { echo "exe: install failed"; %s; }; `, quote(board.InstallScript(t.Agent)), shellAfter)
	} else if t.Install != "" {
		fmt.Fprintf(&b, `sh -c %s || { echo "exe: install failed"; %s; }; `, quote(board.Prereqs()+pkgFn+t.Install), shellAfter)
		b.WriteString(`have || { echo "exe: ` + t.Name + ` is not on PATH after the install"; ` + shellAfter + `; }; `)
	} else {
		b.WriteString(`S=""; [ "$(id -u)" = 0 ] || { command -v sudo >/dev/null 2>&1 && S="sudo -n" || S="doas -n"; }; `)
		b.WriteString(`if [ -f /etc/alpine-release ]; then `)
		if t.Alpine == "" {
			b.WriteString(`echo "exe: ` + t.Name + ` is not packaged for Alpine"; ` + shellAfter + `; `)
		} else {
			b.WriteString(`$S apk add --no-cache ` + t.Alpine + ` || { echo "exe: install failed"; ` + shellAfter + `; }; `)
		}
		b.WriteString(`else `)
		if t.Debian == "" {
			b.WriteString(`echo "exe: ` + t.Name + ` is not packaged for Debian"; ` + shellAfter + `; `)
		} else {
			// try without a fresh index first: most VMs already have one
			b.WriteString(`$S env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 install -y -q ` + t.Debian +
				` || { $S env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 update -q && $S env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=300 install -y -q ` + t.Debian +
				`; } || { echo "exe: install failed"; ` + shellAfter + `; }; `)
		}
		b.WriteString(`fi; `)
	}
	b.WriteString(`fi; `)
	if run == "" {
		fmt.Fprintf(&b, `echo; echo "%s is ready. Try: %s --help"; %s`, t.Name, strings.Fields(t.Bin)[0], shellAfter)
	} else {
		b.WriteString(run)
	}
	return b.String()
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
