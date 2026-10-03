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

	Command string `json:"command"`
}

// Categories in display order.
var Categories = []string{"Agents", "Monitor", "Files", "Git", "Edit", "Data", "Network", "Shell tools", "Languages"}

var catalog = []Tool{
	{ID: "claude", Name: "Claude Code", Category: "Agents", Desc: "Anthropic's coding agent", Agent: board.Claude, Bin: "claude", Run: "claude"},
	{ID: "codex", Name: "Codex", Category: "Agents", Desc: "OpenAI's coding agent", Agent: board.Codex, Bin: "codex", Run: "codex"},

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

// shellAfter is what a tool without a program leaves you in.
const shellAfter = `exec "${SHELL:-/bin/sh}" -l`

// Command is the one shell line a VM terminal runs for t.
func Command(t Tool) string {
	var b strings.Builder
	b.WriteString(`export PATH="$HOME/.local/bin:/usr/local/bin:$PATH"; `)
	// "have" is true when any of the tool's binaries is on PATH.
	fmt.Fprintf(&b, `have() { for x in %s; do command -v "$x" >/dev/null 2>&1 && return 0; done; return 1; }; `, t.Bin)
	b.WriteString(`if ! have; then echo "exe: installing ` + t.Name + `..."; `)
	if t.Agent != "" {
		// the Board's own installer: prerequisites, the CLI, PATH
		fmt.Fprintf(&b, `sh -c %s || { echo "exe: install failed"; %s; }; `, quote(board.InstallScript(t.Agent)), shellAfter)
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
	if t.Run == "" {
		fmt.Fprintf(&b, `echo; echo "%s is ready. Try: %s --help"; %s`, t.Name, strings.Fields(t.Bin)[0], shellAfter)
	} else {
		b.WriteString(t.Run)
	}
	return b.String()
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
