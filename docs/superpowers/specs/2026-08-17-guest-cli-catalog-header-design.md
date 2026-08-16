# Guest CLI Catalog and Header Design

## Goal

Expand the VM Agent & Tools pane with more code-agent CLI launchers and developer-tool installers, and make the pane copy explicit that these launchers run inside the VM terminal without host transcripts.

## Requirements

- Add code-agent CLI launchers for Gemini CLI, Aider, and Qwen Code.
- Add developer-tool launchers for uv, pnpm, bun, lazygit, hyperfine, direnv, just, bat, eza, httpie, yq, and delta.
- Keep the existing behavior: clicking a button opens a VM PTY with `?run=<id>`, installs the CLI/tool if missing, then either execs the agent TUI or drops to a login shell for tools.
- VM CLI agents must not create host-side transcripts. The Transcripts tab remains for built-in `/v1/vms/{name}/agent` runs only.
- Add clear headers/copy in the Agent & Tools pane: one section for code-agent CLIs and one for developer tools.
- Keep credentials/history for guest CLIs inside the VM.
- On mobile, show a standalone **Windows** menu beside **Menu** for switching open windows.
- On mobile, minimizing a window collapses it to a visible title bar instead of hiding it.

## Approach

The change stays in the existing static catalog pattern. Backend `guesttools.go` remains the authority for `?run=<id>` scripts. Frontend `VM_AGENTS` and `VM_TOOLS` get matching IDs and titles so the UI can launch them. Tests cover the new backend IDs and the no-transcript expectation by checking the terminal script path does not reference transcript storage. Static UI tests cover the mobile standalone Windows menu and visible minimized-bar behavior.

## Out of scope

- Making the catalog config-driven.
- Running third-party installers during tests.
- Recording or syncing guest CLI histories.
