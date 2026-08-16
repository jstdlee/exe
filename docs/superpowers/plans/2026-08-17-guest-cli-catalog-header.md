# Guest CLI Catalog and Header Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add more VM code-agent CLI and developer-tool launchers with clearer Agent & Tools headers and no host transcripts for VM CLI agents. Include the late mobile clarification: standalone mobile Windows switcher and visible minimized bars.

**Architecture:** Extend the existing hardcoded guest catalog. Backend `GuestToolScript` remains the authority for scripts started through `/v1/vms/{name}/terminal?run=<id>`. Frontend launcher arrays mirror those IDs and only open VM terminal windows. Mobile window state remains in the existing static window manager.

**Tech Stack:** Go HTTP server, static HTML/CSS/JavaScript, Playwright/Chrome for UI smoke, Go tests for backend catalog scripts.

## Global Constraints

- Guest CLI agents run inside the VM terminal and do not create host transcripts.
- Built-in `/v1/vms/{name}/agent` transcripts remain unchanged.
- Do not run third-party installers during tests.
- Keep credentials and CLI histories inside the VM.
- Mobile minimize collapses to a visible bar; it does not hide the window.
- Mobile window switching uses a standalone **Windows** menu beside **Menu**.

---

### Task 1: Backend catalog tests

**Files:**
- Modify: `internal/server/guesttools_test.go`

**Interfaces:**
- Consumes: `GuestRunScript(id string) string`
- Produces: failing tests for new agent/tool IDs before implementation.

- [x] Add table-driven assertions for agent IDs: `gemini`, `aider`, `qwen`.
- [x] Add table-driven assertions for tool IDs: `uv`, `pnpm`, `bun`, `lazygit`, `hyperfine`, `direnv`, `just`, `bat`, `eza`, `httpie`, `yq`, `delta`.
- [x] Assert agent scripts contain `exec <bin>` and no transcript references.
- [x] Assert tool scripts end in `exec bash -l`.
- [x] Run focused test and verify failure before implementation.

### Task 2: Backend catalog implementation

**Files:**
- Modify: `internal/server/guesttools.go`

**Interfaces:**
- Produces: `GuestRunScript` support for all new IDs.

- [x] Add agent entries for Gemini CLI, Aider, and Qwen Code.
- [x] Add tool entries for uv, pnpm, bun, lazygit, hyperfine, direnv, just, bat, eza, httpie, yq, and delta.
- [x] Run focused tests and verify they pass.

### Task 3: Frontend headers and launcher arrays

**Files:**
- Modify: `internal/server/ui/index.html`

**Interfaces:**
- Consumes: IDs implemented by `GuestRunScript`.
- Produces: visible Agent & Tools section headers and matching launcher buttons.

- [x] Add “Code-agent CLIs” header/copy above agent buttons.
- [x] Add “Developer tools” header/copy above tool buttons.
- [x] Add frontend agent entries for `gemini`, `aider`, and `qwen`.
- [x] Add frontend tool entries for all new developer tools.
- [x] Keep launcher behavior as `openVMTermWin(currentVM, { run: item.id })`.

### Task 4: Mobile window clarification

**Files:**
- Modify: `internal/server/ui/index.html`
- Add: `internal/server/ui_static_test.go`

**Interfaces:**
- Consumes: existing mobile `openWin`, `minimizeWin`, and window-list menu rendering.
- Produces: mobile standalone Windows dropdown and minimized windows rendered as bottom bars.

- [x] Add failing source assertions for standalone mobile Windows menu and visible minimized bars.
- [x] Add `menu-windows` / `dd-windows` beside the mobile `Menu` drawer.
- [x] Remove the window switcher from the mobile drawer.
- [x] Override mobile minimized-window CSS to display a 20px collapsed bar.
- [x] Reserve bottom space and stack multiple minimized bars.
- [x] Verify the focused mobile static tests pass.

### Task 5: Docs and verification

**Files:**
- Modify: `internal/server/docs.md`
- Modify: `internal/server/skill.md`

**Interfaces:**
- Produces: docs that match the expanded catalog and transcript behavior.

- [x] Update docs to list the new agent/tool catalog.
- [x] State that VM CLI agents do not create host transcripts; only built-in Agent runs do.
- [x] Document mobile Windows menu and minimized-bar behavior.
- [x] Run Go tests.
- [x] Browser-smoke source UI through a local stub server for load, mobile Windows menu, minimized-bar behavior, and Browser app open.
- [x] Build/restart `exe.service` if final verification passes.
