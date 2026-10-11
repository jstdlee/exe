# jstdlee branch: plan and contracts

Branch `jstdlee` = upstream `livid/exe` `main` + the features below.
`main` mirrors upstream. The old fork is `legacy` (tag `legacy-2026-08-21`);
read it for *behaviour* only. Do not copy its code: rebuild each feature
for current upstream, and fix its known bugs.

## Ground rules

- **Upstream merges must stay cheap.** New code goes in new files:
  `internal/jx/<feature>/` (logic, no `server` import) and
  `internal/server/jx_<feature>.go` (HTTP glue), `cmd/exe/jx_<feature>.go`
  (CLI), `internal/server/ui/jx.js` + `jx.css` (desktop hooks),
  `internal/server/sysapps/board/` (Board app). Upstream files get
  one-line hooks only. The hooks already in place:
  - `server.go`: `s.registerJX(mux)`, `s.jxWrap(mux)`, field `jx jxState`.
  - `sshgate.go`: `defer g.s.JXHold(name, "ssh")()` per SSH-gate VM session.
  - `cmd/exe/main.go`: `jxCommands` dispatch; `exe code` removed.
- Register routes from your file's `init()`:
  `jxFeatureRegs = append(jxFeatureRegs, func(s *Server, mux *http.ServeMux) {...})`.
  All jx routes live under `/v1/jx/`. Auth is the existing `/v1/*` token.
- Register CLI commands from `init()`: `jxCommands["env"] = cmdEnv`,
  `jxUsageLines["env"] = "  exe env ..."`.
- State lives under `s.jxDir()` (`~/.exe/jx/`). Write files 0600, dirs 0700,
  atomically (temp + rename). Never log secrets.
- Tests: `go test ./...`. Never touch `~/.exe` or port 7777 in tests: use
  `t.TempDir()`. Real VMs are not available in tests: put the VM boundary
  behind small interfaces (`Runner` that runs a command over SSH and streams
  stdout) and fake them.
- Platforms: Linux, macOS, Windows hosts all build. Guests: Debian 13 and
  Alpine 3.24 (`vmm.Info.Image`: "" or "debian" / "alpine").
- Commit as `jstdlee <302753485+jstdlee@users.noreply.github.com>`.

## Only Claude Code and Codex

Removed (jx.go `jxRemoved` answers 410): `/v1/chat/send`,
`/v1/chat/sessions*`, `/v1/vms/{name}/agent|memory|transcripts*`, `exe code`,
lobby `code`. Kept: `/v1/chat/complete|models|status` (Blue Pencil, Hub
composer), `/v1/openai/*` (Codex window usage meter), host Claude Code /
Codex tmux windows, hub agent. The UI hides the Chat icon, Windows→Chat,
the VM Agent and Sessions tabs, the Notes memory block, "Chat with this VM",
the chat backend switcher and the Ollama/OpenAI config groups (jx.js).

## Feature A — Board (task threads for agent CLIs)

A thread = one agent CLI session (Claude Code or Codex) on one target (a VM,
or `host`). Each message is one headless turn; the daemon owns the run, so it
survives closed tabs and lost networks.

- Turn command, run as the VM's ssh user in `~/work` (created) over SSH, the
  prompt on **stdin** (no quoting):
  - Claude: `claude -p --output-format stream-json --verbose
    --permission-mode bypassPermissions [--resume <id> [--fork-session]]`.
    Session id from the `system/init` event's `session_id`.
  - Codex: `codex exec --json --skip-git-repo-check
    --dangerously-bypass-approvals-and-sandbox -` or
    `codex exec resume <id> --json ... -`. Thread id from `thread.started`.
  - Host target: same CLIs on the host via os/exec, cwd = workspace, but
    Claude `--permission-mode acceptEdits` and Codex `--sandbox
    workspace-write` (the host is not a sandbox).
- Credentials (VM): `~/.exe/jx/secrets.json` holds `claude_oauth_token`
  (from `claude setup-token`) and `codex_api_key`. On first use per VM,
  write them to `~/.config/exe/agent.env` (0600) in the guest; turns source
  it (`CLAUDE_CODE_OAUTH_TOKEN`, `OPENAI_API_KEY`; for Codex also run
  `codex login --with-api-key` once from stdin). If no secret is set, the
  turn runs anyway and uses whatever login the guest has (logging in once
  per VM through its terminal is the fallback).
- Install on demand: if the CLI is missing in the guest, install it (status
  events say so). Claude: official `curl -fsSL https://claude.ai/install.sh | bash`.
  Codex: the musl release binary from github.com/openai/codex releases for
  the guest arch. Alpine needs `apk add bash curl libgcc libstdc++ ripgrep`
  first. Also ensure `tmux` and `git` (used by the terminal and agents).
- The VM is started if stopped (`jxEnsureVMUp`), and the turn holds lease
  reason `board` for its whole life.
- One turn at a time per thread; a message sent while a turn runs is
  **queued** and starts when it ends. Stop kills the remote process group.
- Events are normalized and stored per thread (append-only JSONL, plus a
  `thread.json`), seq-numbered, so the UI can resume a stream with `after`.

API (all JSON):

| Method, path | Body / query | Result |
|---|---|---|
| GET `/v1/jx/board/threads` | | `[Thread]` newest first |
| POST `/v1/jx/board/threads` | `BoardSubmitRequest` (jx_contract.go) | `{thread: Thread, turn_id}` |
| GET `/v1/jx/board/threads/{id}` | | `{thread: Thread, turns: [Turn]}` (events included) |
| POST `/v1/jx/board/threads/{id}/turns` | `{prompt}` | `{turn_id, queued: bool}` |
| POST `/v1/jx/board/threads/{id}/stop` | | 204 |
| PATCH `/v1/jx/board/threads/{id}` | `{title?, archived?}` | Thread |
| DELETE `/v1/jx/board/threads/{id}` | | 204 |
| GET `/v1/jx/board/threads/{id}/events?after=N` | SSE (`?token=` ok) | `data: Event` lines; ping comment every 25 s |
| GET `/v1/jx/board/events` | SSE | `data: {thread_id, state, updated_at}` on any thread change |
| GET `/v1/jx/board/sessions?target=&agent=` | | `[{id, title, updated_at, cwd, thread_id?}]` existing CLI sessions in that target (Claude `~/.claude/projects/*/*.jsonl`, Codex `~/.codex/sessions/**/rollout-*.jsonl`), newest first, max 30 |
| GET `/v1/jx/agents/status?target=` | | `{claude: {installed, version, auth}, codex: {...}}`, auth = `token` / `login` / `none` |
| POST `/v1/jx/agents/install` | `{target, agent}` | 202, progress as board-style status events on `/v1/jx/board/events` |
| GET `/v1/jx/agents/secrets` | | `{claude_oauth_token_set, codex_api_key_set}` (never the values) |
| PUT `/v1/jx/agents/secrets` | `{claude_oauth_token?, codex_api_key?}` ("" clears) | same as GET |

```
Thread {id, title, target, agent, session_id, state: idle|running|queued|error,
        origin, created_at, updated_at, last_text, archived}
Turn   {id, prompt, origin, state: queued|running|done|error|stopped,
        started_at, ended_at, error?, usage?: {input_tokens, output_tokens, cost_usd?},
        events: [Event]}
Event  {seq, turn_id, at, type, ...}
  type "status"      {text}                      e.g. "starting VM", "installing codex"
  type "text"        {text}                      assistant text (markdown)
  type "tool"        {id, name, summary}          one line, e.g. "Bash: npm test"
  type "tool_result" {id, output, is_error}       output clipped to 4 KB
  type "turn_start"  {prompt}
  type "turn_end"    {state, error?, usage?}
```

## Feature B — Leases, idle stop, memory guard, cron

Leases (`internal/jx/lease`, done): holders are `terminal` (VM terminal
WebSocket, jxWrap), `ssh` (SSH gate), `board`, `env`, `cron`, `pin`.
Proxy traffic and VM start `Touch` the VM.

Policy per VM, in `~/.exe/jx/vms.json`: `kind` = `dev` | `agent` | `service`
| `job` (default `dev`), `pinned`, `idle_minutes` (null = kind default).

Defaults (`/v1/jx/settings`): `dev` 60, `agent` 20, `service` 0 (never),
`job` 5 minutes; `warn_minutes` 2; `memory_cap_percent` 80.

- Controller loop every 30 s: a running VM with no holders, not pinned,
  limit > 0, idle ≥ limit → stop it (graceful `VMs.Stop`), log it, push a
  newsfeed/webpush note. At limit − warn, send one warning (webpush if
  subscribed) and expose `stop_at` in the API.
- A VM that was idle-stopped is not added to upstream's autostart list on
  the next daemon restart.
- Memory guard: on VM create/start (jxWrap and `jxEnsureVMUp`), estimate
  host memory after the start (`MemAvailable` from /proc/meminfo on Linux;
  `hostinfo` on others) + VM memory_mb. If it crosses the cap, stop idle
  (no-holder, not pinned, not service) VMs, least recently busy first, until
  it fits; if it still does not fit, refuse with 507 and a clear message.
  Cap 0 = off.
- Cron (`internal/jx/cron`): jobs in `~/.exe/jx/cron.json`. Schedule =
  5-field cron or `@every 30m` / `@hourly` / `@daily`, local time (or `tz`).
  Kinds: `board` (posts `prompt` to `thread_id` or a new thread on
  `target`+`agent`, via `jxBoardSubmit`) and `shell` (runs `command` in the
  VM over SSH, output kept, 64 KB clip). A run holds lease `cron`, starts
  the VM if needed (`jxEnsureVMUp`), has a timeout (default 1 h), never
  overlaps itself (skips and records "skipped: still running"). Keep the
  last 20 runs per job.

| Method, path | Body | Result |
|---|---|---|
| GET `/v1/jx/settings` / PUT | `{idle_defaults: {dev, agent, service, job}, warn_minutes, memory_cap_percent}` | same |
| GET `/v1/jx/vms` | | `[{vm, state, kind, pinned, idle_minutes, effective_minutes, holders: [Holder], last_busy, idle_seconds, stop_at?}]` |
| PUT `/v1/jx/vms/{name}` | `{kind?, pinned?, idle_minutes? (null = default)}` | that row |
| POST `/v1/jx/vms/{name}/keep` | | resets the idle timer (Touch); 204 |
| GET `/v1/jx/memory` | | `{total_mb, used_mb, available_mb, cap_percent, cap_mb}` |
| GET `/v1/jx/cron` | | `[Job]` with `next_run`, `last_run`, `last_status` |
| POST `/v1/jx/cron` / PUT `/v1/jx/cron/{id}` / DELETE | `Job` | Job |
| POST `/v1/jx/cron/{id}/run` | | 202 `{run_id}` |
| GET `/v1/jx/cron/{id}/runs` | | `[{id, started_at, ended_at, status: ok|error|skipped|running, output?, thread_id?}]` |

```
Job {id, name, schedule, tz?, enabled, kind: board|shell, target, agent?,
     thread_id?, prompt?, command?, timeout_minutes?}
```

CLI: `exe idle [ls | set <vm> kind|pin|minutes ...]`, `exe cron [ls | add |
rm | run]`.

## Feature C — Environments, snapshots, small keeps

- `exe env plan <dir>`: reads `compose.yaml`/`docker-compose.yml`
  (services' images → packages where obvious, ports), `.github/workflows/*.yml`
  (setup-node/python/go/java versions, `run:` install steps), `pyproject.toml`
  / `requirements*.txt`, `package.json` (engines, lockfile → npm/pnpm/yarn),
  `go.mod`, `Cargo.toml`, `apt.txt`/`packages.txt`; prints a guest
  bootstrap script for **Debian (apt) or Alpine (apk)** and notes on what it
  could not map. Logic in `internal/jx/envplan`, pure and table-tested.
- `exe env up <vm> [dir] [-image debian|alpine] [-mem MB]`: creates the VM
  if missing, uploads the project (tar over SSH, honouring `.gitignore`
  basics and skipping `node_modules`, `.venv`, `target`, `.git` unless
  `-git`), runs the bootstrap, holds lease `env` while it runs.
- `exe env run <vm> [-o path]... -- <command>`: runs a job in `~/work`,
  streams output, then copies listed output paths back. Lease `env`.
  API: POST `/v1/jx/env/plan`, POST `/v1/jx/env/up`, POST `/v1/jx/env/run`
  (NDJSON stream), so the CLI is a thin client.
- Snapshots (`internal/jx/snap`): stop the VM if running, copy the disk
  with a reflink when the filesystem supports it (`FICLONE` on Linux,
  `clonefile` on macOS), else a sparse copy (skip zero blocks / `SEEK_DATA`);
  never a dense full copy. Store under `~/.exe/jx/snapshots/<vm>/<id>/`
  with `meta.json` (label, created_at, size). Restore = stop + copy back.
  Find the disk file per backend by reading `internal/vmm` (do not change
  vmm). API: GET/POST `/v1/jx/vms/{name}/snapshots`, POST
  `.../snapshots/{id}/restore`, DELETE `.../snapshots/{id}`. CLI: `exe snap
  ls|create|restore|rm <vm> ...`.
- `exe docs` / `exe skill`: print the embedded `docs.md` / `skill.md`
  (export them from `internal/server` with tiny accessor funcs in a jx file).
- VM terminal (`internal/server/terminal.go`, small edit allowed): write
  dial/PTY errors into the terminal as text before closing; send SIGHUP to
  the remote session on close; ignore resize values ≤ 0 or > 1000.

## Feature D — Desktop UI

- `ui/jx.js` (one IIFE; never redeclare upstream top-level names) and
  `ui/jx.css`, loaded by two lines in `index.html` (after `</style>` and
  after the main `</script>`).
- **Board sysapp** `sysapps/board/` (`index.html`, `app.json`, `icon.svg`),
  Platinum style per `docs/platinum.md`. Views:
  - Thread list (title, target, agent, state dot, last text, time), filter
    by target. "New" opens the composer.
  - Thread view: messages (prompt bubbles, assistant markdown, tool lines
    folded, status lines), live via SSE with `after`, Stop, a queued badge.
  - Composer (bottom, native textarea, works with phone keyboards, IME and
    dictation): Target (VM list + host), Agent (Claude Code / Codex),
    Session: **New / Continue ▸ list from `/v1/jx/board/sessions`** /
    Fork. Cmd/Ctrl+Enter sends.
  - Schedules: list/add/edit cron jobs (board or shell), run now, last runs.
  - Machines: per-VM kind, pin, idle minutes, holders, "stops in N min",
    Keep; settings (defaults, memory cap); memory bar from `/v1/jx/memory`;
    agent status + Install buttons; secrets form (write-only).
- **Terminal on phones** (jx.js, all terminal windows incl. the VM tab):
  - Key bar: Esc, Tab, Ctrl (sticky one-shot; long-press = lock), Alt, ↑ ↓
    ← →, PgUp, PgDn, `|`, `~`, `/`, `-`, ^C, and a Compose toggle.
  - Compose box: native textarea; Send = bracketed paste + optional Enter.
  - Select mode: shows the screen + scrollback as plain selectable text
    (read the xterm buffer) with Copy; Done returns.
  - Desktop too: the bar can be shown from the terminal's context menu.
- VM terminals open in guest tmux (`?cmd=tmux new -A -s main` when the
  guest has tmux) so they survive a dropped connection; reconnect button.
- 401 from any API call opens the Set API Token window once (with a note).
- Hide the removed agent UI (list in "Only Claude Code and Codex").

## Feature E — Hub bridge (private hub as the task board)

A private exe-hub (loopback only, open gate) is the conversation channel.
An owner's root post starting with `@agent [claude|codex] [on <vm|host>]:`
starts a Board thread; owner replies in that hub thread are its next turns;
`/stop`, `/status` control it. The agent answers under its own key
(`jx/hubbridge_ed25519`), one reply per turn (clipped to 8 KB). Only
owners' posts are acted on. API `GET|PUT /v1/jx/hubbridge`. Code:
`internal/jx/hubbridge/`, `internal/server/jx_hubbridge.go`.

## Feature F — LLM providers for VM agents

Configuration → LLM Providers (jx.js) edits `~/.exe/jx/llm.json` (0600):
providers `{id, name, kind: openai|anthropic|both, base_url, api_key,
model}` plus `last[agent] = {provider, model, thinking}`. Magpie
(`http://127.0.0.1:3425/v1`, kind both) is built in and never saved. API:
`GET /v1/jx/llm` (keys never returned, `api_key_set` instead),
`PUT /v1/jx/llm/providers` (empty key keeps the saved one, `-` clears it),
`POST /v1/jx/llm/models` (a saved provider by id, or the form's values).
VM Tools agents (claude, codex, opencode, pi, omp, grok) open a Start
dialog; Start opens the VM terminal with `cmd=jx-launch:<query>`. The
`jxWrap` terminal hook (`jxLaunch`) remembers the choice, opens an SSH
remote forward (guest `127.0.0.1:<port>` → host) for a loopback provider,
writes the launch script on stdin to `~/.config/exe/llm/launch-<id>.sh`
(it removes itself, then exports `EXE_LLM_API_KEY`, writes the agent's
`exe` provider entry and execs the CLI), and swaps in
`tools.LaunchCommand`. Codex installs with OpenAI's standalone installer
(`install.sh`, CODEX_NON_INTERACTIVE=1); a bare `~/.local/bin/codex` from
older builds counts as missing. Code: `internal/jx/llm/`,
`internal/server/jx_llm.go`, `internal/jx/tools/` (installers).
