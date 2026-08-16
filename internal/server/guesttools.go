package server

// GuestRunScript is the bash -lc program started in a VM PTY for the
// Agent & Tools tab. The CLI/tool owns the TTY from the first byte —
// we do not type the installer into a login shell (that ate keystrokes).

type guestItem struct {
	ID, Title, Bin, Install, Hint string
}

const guestAgentPrelude = `ensure_guest_swap() {
  if swapon --show=NAME | grep -qx /swapfile; then return 0; fi
  if [ ! -f /swapfile ]; then
    echo "==> adding 1G swapfile for agent installers/TUIs"
    sudo fallocate -l 1G /swapfile 2>/dev/null || sudo dd if=/dev/zero of=/swapfile bs=1M count=1024 status=none
    sudo chmod 600 /swapfile
    sudo mkswap /swapfile >/dev/null
  fi
  sudo swapon /swapfile 2>/dev/null || true
}
export PATH="/usr/local/sbin:/usr/sbin:/sbin:$HOME/.local/bin:$HOME/.cargo/bin:$HOME/go/bin:$HOME/.opencode/bin:$HOME/.npm-global/bin:/usr/local/go/bin:$PATH"
ensure_guest_swap || true
export NPM_CONFIG_UPDATE_NOTIFIER=false
export NPM_CONFIG_FUND=false
export NPM_CONFIG_AUDIT=false
`

func guestInstallThen(bin, title, install string) string {
	return `set -e
` + guestAgentPrelude + `
hash -r
if command -v ` + bin + ` >/dev/null 2>&1; then
  echo "==> ` + title + ` already installed: $(command -v ` + bin + `)"
  ` + bin + ` --version 2>/dev/null || ` + bin + ` version 2>/dev/null || true
  echo
  exec ` + bin + `
fi
echo "==> installing ` + title + `…"
` + install + `
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/go/bin:$HOME/.opencode/bin:$HOME/.npm-global/bin:/usr/local/go/bin:$PATH"
hash -r
if command -v ` + bin + ` >/dev/null 2>&1; then
  echo "==> ` + title + ` ready"
  exec ` + bin + `
fi
echo "install finished but ` + bin + ` is not on PATH"
exec bash -l`
}

// For packages that are libraries/toolchains (not a long-running TUI),
// drop into a login shell after install so the user can type immediately.
func guestInstallThenShell(bin, title, install string) string {
	return `set -e
export DEBIAN_FRONTEND=noninteractive
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/go/bin:/usr/local/go/bin:$PATH"
hash -r
if command -v ` + bin + ` >/dev/null 2>&1; then
  echo "==> ` + title + ` already installed: $(command -v ` + bin + `)"
  ` + bin + ` --version 2>/dev/null || ` + bin + ` version 2>/dev/null || true
  echo
  exec bash -l
fi
echo "==> installing ` + title + `…"
` + install + `
export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$HOME/go/bin:/usr/local/go/bin:$PATH"
hash -r
if command -v ` + bin + ` >/dev/null 2>&1; then
  echo "==> ` + title + ` ready: $(command -v ` + bin + `)"
  ` + bin + ` --version 2>/dev/null || ` + bin + ` version 2>/dev/null || true
else
  echo "install finished but ` + bin + ` is not on PATH"
fi
echo
exec bash -l`
}

func GuestToolScript(id string) string {
	for _, a := range guestAgents {
		if a.ID == id {
			return guestInstallThen(a.Bin, a.Title, a.Install)
		}
	}
	for _, t := range guestTools {
		if t.ID == id {
			return guestInstallThenShell(t.Bin, t.Title, t.Install)
		}
	}
	return ""
}

var guestAgents = []guestItem{
	{ID: "codex", Title: "Codex", Bin: "codex",
		Install: `if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
sudo npm i -g @openai/codex`},
	{ID: "gemini", Title: "Gemini CLI", Bin: "gemini",
		Install: `if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
sudo npm i -g @google/gemini-cli`},
	{ID: "opencode", Title: "OpenCode", Bin: "opencode",
		Install: `curl -fsSL https://opencode.ai/install | bash`},
	{ID: "aider", Title: "Aider", Bin: "aider",
		Install: `sudo apt-get update -y && sudo apt-get install -y python3 python3-pip python3-venv pipx
python3 -m pipx ensurepath || true
pipx install aider-chat`},
	{ID: "qwen", Title: "Qwen Code", Bin: "qwen",
		Install: `if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
sudo npm i -g @qwen-code/qwen-code`},
	{ID: "pi", Title: "Pi", Bin: "pi",
		Install: `if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
sudo npm i -g @mariozechner/pi-coding-agent || sudo npm i -g pi`},
	{ID: "claude", Title: "Claude", Bin: "claude",
		Install: `curl -fsSL https://claude.ai/install.sh | bash || {
  if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
  sudo npm i -g @anthropic-ai/claude-code
}`},
}

var guestTools = []guestItem{
	{ID: "git", Title: "git", Bin: "git", Hint: "version control",
		Install: `sudo apt-get update -y && sudo apt-get install -y git`},
	{ID: "build-essential", Title: "build-essential", Bin: "gcc", Hint: "gcc make g++",
		Install: `sudo apt-get update -y && sudo apt-get install -y build-essential pkg-config`},
	{ID: "python3", Title: "Python 3", Bin: "python3", Hint: "python3 + pip + venv",
		Install: `sudo apt-get update -y && sudo apt-get install -y python3 python3-pip python3-venv python3-dev`},
	{ID: "node", Title: "Node.js", Bin: "node", Hint: "node + npm",
		Install: `curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs`},
	{ID: "docker", Title: "Docker", Bin: "docker", Hint: "containers",
		Install: `curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER" || true`},
	{ID: "go", Title: "Go", Bin: "go", Hint: "golang toolchain",
		Install: `ver=$(curl -fsSL https://go.dev/VERSION?m=text | head -1)
curl -fsSL "https://go.dev/dl/${ver}.linux-amd64.tar.gz" -o /tmp/go.tgz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz
mkdir -p "$HOME/go/bin"`},
	{ID: "rust", Title: "Rust", Bin: "rustc", Hint: "rustup + cargo",
		Install: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y`},
	{ID: "jq", Title: "jq", Bin: "jq", Hint: "JSON CLI",
		Install: `sudo apt-get update -y && sudo apt-get install -y jq`},
	{ID: "ripgrep", Title: "ripgrep", Bin: "rg", Hint: "fast grep",
		Install: `sudo apt-get update -y && sudo apt-get install -y ripgrep`},
	{ID: "fd", Title: "fd", Bin: "fd", Hint: "find alternative",
		Install: `sudo apt-get update -y && sudo apt-get install -y fd-find
mkdir -p "$HOME/.local/bin"
ln -sfn "$(command -v fdfind)" "$HOME/.local/bin/fd"`},
	{ID: "fzf", Title: "fzf", Bin: "fzf", Hint: "fuzzy finder",
		Install: `sudo apt-get update -y && sudo apt-get install -y fzf`},
	{ID: "tmux", Title: "tmux", Bin: "tmux", Hint: "terminal mux",
		Install: `sudo apt-get update -y && sudo apt-get install -y tmux`},
	{ID: "neovim", Title: "Neovim", Bin: "nvim", Hint: "editor",
		Install: `sudo apt-get update -y && sudo apt-get install -y neovim`},
	{ID: "htop", Title: "htop", Bin: "htop", Hint: "process viewer",
		Install: `sudo apt-get update -y && sudo apt-get install -y htop`},
	{ID: "gh", Title: "GitHub CLI", Bin: "gh", Hint: "gh",
		Install: `sudo apt-get update -y && sudo apt-get install -y gh || {
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list
  sudo apt-get update -y && sudo apt-get install -y gh
}`},
	{ID: "cmake", Title: "CMake", Bin: "cmake", Hint: "build system",
		Install: `sudo apt-get update -y && sudo apt-get install -y cmake ninja-build`},
	{ID: "sqlite3", Title: "SQLite", Bin: "sqlite3", Hint: "embedded SQL",
		Install: `sudo apt-get update -y && sudo apt-get install -y sqlite3`},
	{ID: "unzip", Title: "zip/unzip", Bin: "unzip", Hint: "archives",
		Install: `sudo apt-get update -y && sudo apt-get install -y zip unzip tar`},
	{ID: "tree", Title: "tree", Bin: "tree", Hint: "directory tree",
		Install: `sudo apt-get update -y && sudo apt-get install -y tree`},
	{ID: "rsync", Title: "rsync", Bin: "rsync", Hint: "file sync",
		Install: `sudo apt-get update -y && sudo apt-get install -y rsync`},
	{ID: "uv", Title: "uv", Bin: "uv", Hint: "Python package manager",
		Install: `curl -LsSf https://astral.sh/uv/install.sh | sh`},
	{ID: "pnpm", Title: "pnpm", Bin: "pnpm", Hint: "Node package manager",
		Install: `if ! command -v npm >/dev/null 2>&1; then curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs; fi
sudo npm i -g pnpm`},
	{ID: "bun", Title: "Bun", Bin: "bun", Hint: "JS runtime/toolkit",
		Install: `curl -fsSL https://bun.sh/install | bash`},
	{ID: "lazygit", Title: "lazygit", Bin: "lazygit", Hint: "git TUI",
		Install: `sudo apt-get update -y && sudo apt-get install -y lazygit || {
  sudo apt-get install -y curl jq tar
  ver=$(curl -fsSL https://api.github.com/repos/jesseduffield/lazygit/releases/latest | jq -r .tag_name | sed 's/^v//')
  arch=$(dpkg --print-architecture)
  case "$arch" in amd64) arch=x86_64 ;; arm64) arch=arm64 ;; *) echo "unsupported arch: $arch"; exit 1 ;; esac
  curl -fsSL "https://github.com/jesseduffield/lazygit/releases/download/v${ver}/lazygit_${ver}_Linux_${arch}.tar.gz" -o /tmp/lazygit.tgz
  tar -C /tmp -xzf /tmp/lazygit.tgz lazygit
  sudo install /tmp/lazygit /usr/local/bin/lazygit
}`},
	{ID: "hyperfine", Title: "hyperfine", Bin: "hyperfine", Hint: "benchmark runner",
		Install: `sudo apt-get update -y && sudo apt-get install -y hyperfine`},
	{ID: "direnv", Title: "direnv", Bin: "direnv", Hint: "directory env",
		Install: `sudo apt-get update -y && sudo apt-get install -y direnv`},
	{ID: "just", Title: "just", Bin: "just", Hint: "command runner",
		Install: `sudo apt-get update -y && sudo apt-get install -y just`},
	{ID: "bat", Title: "bat", Bin: "batcat", Hint: "cat with syntax",
		Install: `sudo apt-get update -y && sudo apt-get install -y bat
mkdir -p "$HOME/.local/bin"
ln -sfn "$(command -v batcat)" "$HOME/.local/bin/bat"`},
	{ID: "eza", Title: "eza", Bin: "eza", Hint: "modern ls",
		Install: `sudo apt-get update -y && sudo apt-get install -y eza`},
	{ID: "httpie", Title: "HTTPie", Bin: "http", Hint: "HTTP CLI",
		Install: `sudo apt-get update -y && sudo apt-get install -y httpie`},
	{ID: "yq", Title: "yq", Bin: "yq", Hint: "YAML CLI",
		Install: `sudo apt-get update -y && sudo apt-get install -y yq`},
	{ID: "delta", Title: "delta", Bin: "delta", Hint: "git diff viewer",
		Install: `sudo apt-get update -y && sudo apt-get install -y git-delta`},
}

func GuestRunScript(id string) string { return GuestToolScript(id) }
