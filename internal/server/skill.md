---
name: exe-vms
description: >
  Drive exe, a portable Debian agent Environment on this host: VM lifecycle,
  exe env init/run/snap, SSH gate :2222, jobs with attached files and one-shot
  download links, and Cloudflare publish. Prefer exe env / HTTP jobs over the
  host desktop. Named agents (claude, opencode, pi, codex) run inside the VM.
---

# exe — portable agent Environment

exe boots persistent **Debian** Firecracker VMs on this Linux host. The
Platinum UI at `$BASE/` is a **minimal host desktop** for a human: VM list,
Host Terminal, host Workspace / Notes / Editor / Chat / Browser. **Bots
should use this skill and the HTTP/SSH/CLI APIs — not the desktop.**

**Host vs VM**

| Host (this machine) | Inside a VM |
|---|---|
| VM list / create / start / stop / delete | Agent CLIs (claude, opencode, pi, codex) |
| Host Terminal (`GET /v1/host/terminal`) | VM shell (`GET /v1/vms/{name}/terminal` or `ssh -p 2222`) |
| Host Workspace, Notes, Editor, Chat, Browser apps | `exe env run` jobs, files you attach |
| Cloudflare tunnel / expose | Services bound to `0.0.0.0` in the guest |

Do **not** use Host Terminal for project work.

**Base URL**: scheme://host:port you fetched this file from
(default `http://127.0.0.1:7777` or `http://192.168.8.222:7777`).
`$HOST` is the same host without scheme/port.

## Auth

If `api_token` is set, every `/v1/*` request needs
`Authorization: Bearer <token>`. WebSockets accept `?token=<token>`.
`GET /healthz` is open. One-shot job downloads use `GET /dl/{token}`
(the path token **is** the auth; **one use**, then 410).

```sh
curl -s -H "Authorization: Bearer $TOKEN" $BASE/v1/vms
```

Errors are JSON `{"error":"..."}`. `404` = missing, `409` = wrong VM state
(usually not running), `400` = bad request.

## Prefer `exe env` (portable Environment)

```sh
exe env ls
exe env init NAME --flavor debian --from docker-compose.yml --from pyproject.toml
exe env run NAME --cmd 'uname -a'
exe env run NAME --script setup.sh --file app.py --prompt 'run the tests'
exe env run NAME --session SESSION --cmd 'make'
exe env snap NAME create before
exe env snap NAME ls
exe env snap NAME restore ID
exe env snap NAME rm ID
exe env stop NAME
exe env rm NAME          # destroy disk
```

- **Flavor**: Debian 13 only (`flavors/debian.yaml`).
- **Manifest** (`--from`): docker-compose YAML, GitHub workflow YAML,
  `pyproject.toml`, or a text package list. These are **init recipes**, not
  a Docker runtime. Init writes `~/BOOTSTRAP.md` and runs `~/bootstrap.sh`.
- **Job** (`run`): script and/or argv, multiple `--file` attachments, optional
  `--prompt` (runs a named agent **in the VM** if installed). Returns
  `shell_output`, `agent_output`, `session`, and `downloads[].url`.
- **Snapshot**: copies `disk.raw` (VM is stopped first).

HTTP equivalents:

| Method | Path | Body |
|---|---|---|
| `GET` | `/v1/env/flavors` | — |
| `POST` | `/v1/env/init` | `{"name","flavor","from":[{"name","text"}]}` |
| `POST` | `/v1/vms/{name}/jobs` | `{"cmd","script","prompt","session","files":[{"name","content":"<base64>"}]}` |
| `GET` | `/v1/vms/{name}/jobs/{session}` | session turns |
| `GET/POST` | `/v1/vms/{name}/snaps` | create: `{"label"}` |
| `POST` | `/v1/vms/{name}/snaps/{id}/restore` | — |
| `DELETE` | `/v1/vms/{name}/snaps/{id}` | — |
| `GET` | `/dl/{token}` | one-shot file |

## VM object and lifecycle

```json
{
  "name": "demo",
  "state": "running",
  "cpus": 2,
  "memory_mb": 2048,
  "disk_gb": 8,
  "ip": "10.77.0.2",
  "created_at": "2026-08-16T00:00:00Z"
}
```

Guest user is `dev` (`ssh_user`) with passwordless sudo. The VM is the
sandbox boundary.

| Method & path | Effect |
|---|---|
| `GET /v1/vms` | List |
| `POST /v1/vms` | Create **and boot** (`name` required) |
| `GET /v1/vms/{name}` | One VM |
| `POST /v1/vms/{name}/start` | Boot |
| `POST /v1/vms/{name}/stop` | Power-off (disk stays) |
| `DELETE /v1/vms/{name}` | Destroy disk — **confirm with the user** |

Create/start are synchronous (wait for SSH). First create may download ~3 GB.

## SSH gate (:2222)

- `ssh -p 2222 <vm>@$HOST` — into the guest (auto-starts). scp/sftp/`-L` work.
- `ssh -p 2222 exe@$HOST <cmd>` — lobby: `ls`, `new`, `start`, `stop`, `rm`,
  `ip`, `code`, `expose`, `routes` (`--json`).

Keys: daemon user's `~/.ssh/*.pub`, `~/.exe/ssh/id_ed25519`, or
`~/.exe/ssh/authorized_clients`.

## Services and publish

- `GET /v1/vms/{name}/ports` — guest listeners on non-loopback (bind `0.0.0.0`).
- `POST /v1/vms/{name}/expose` `{"port":8000,"subdomain":"guestbook"}` —
  needs Cloudflare (`cloudflare.domain`).
- `GET /v1/routes` / `DELETE /v1/routes/{host}`.

## Interactive VM terminal (WebSocket)

`GET /v1/vms/{name}/terminal` — binary frames = PTY bytes; text =
`{"resize":[cols,rows]}`. Auth `?token=`. Prefer `ssh -p 2222`.

## Named agents

Install and sign in **inside the VM**. The Platinum **Agent & Tools** tab has
Code-agent CLI buttons (Codex, Gemini CLI, OpenCode, Aider, Qwen Code, Pi,
Claude) plus a grid of Linux developer tools (git, build-essential, Python,
Node, Docker, Go, Rust, jq, ripgrep, fd, fzf, tmux, Neovim, htop, gh, cmake,
sqlite, zip, tree, rsync, uv, pnpm, Bun, lazygit, hyperfine, direnv, just,
bat, eza, HTTPie, yq, delta). Clicking a button opens a VM PTY that
**installs if missing** (`?run=`), then execs the agent TUI or drops to a
shell. CLI-agent sign-in and history stay in the guest; these VM CLI launches
do not create host transcripts.

Host Chat (desktop File menu / mobile Menu drawer) is a **host** operator for
VM lifecycle, not a guest agent. It does **not** have its own LLM tab or API
keys. Pick an **agent + model** that is already signed in on this host:

| Agent | Host files | How to sign in |
|---|---|---|
| Grok | `~/.grok/auth.json`, `~/.grok/config.toml` | `grok` CLI (OAuth) |
| Claude | `~/.claude/.credentials.json` or `~/.claude.json` | `claude` CLI / `/login` |
| Codex | `~/.codex/auth.json`, `~/.codex/config.toml` | `codex login` |

`GET /v1/host/agents` lists ready agents and models. Chat send accepts
`{"agent","model"}`. Configuration only has **idle_stop_minutes** (stop a
VM after N minutes with no terminal/job/SSH; `0` = never).

## Other

`GET /v1/config`, `GET /v1/logs`, `GET /v1/host/agents`.
`GET /v1/host/stats` — host CPU %, disk I/O B/s, net B/s, disk free/total
(shown on the Virtual Machines window and the menu bar).
`GET /v1/browse/https/host/path` (or `?url=`) — same-origin proxy for the
desk Browser. Forwards HTML/CSS/JS/images, strips frame-busting headers,
rewrites asset URLs so styles and scripts load.
`POST /v1/vms/{name}/publish` — GitHub, **only if the user asks**.
`POST /v1/newsfeed` — host Newsfeed.

**Terminal copy/paste** (host + VM xterm): hold **Ctrl**, select text,
right-click to copy; Ctrl + right-click with no selection pastes. Do not
use the browser’s native file-drag on desktop icons.

Desktop background: File → Desktop Background… or right-click the desktop.
`GET/PUT /v1/desktop` `{mode,color}` — mode is `center`, `stretch`, or `cover`
(expand all, keep ratio). `GET/PUT/DELETE /v1/desktop/wallpaper` is the picture.

Leave config PUT, daemon restart, workspace, apps, and UI state alone
unless the user explicitly asks.
