# Workspace Tree, Editor, and Notes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Workspace the tree-based file navigator, keep editor windows single-file focused, remove redundant hide controls, and verify responsive desktop/mobile behavior.

**Architecture:** Use the existing daemon Workspace APIs and existing internal editor/image windows. Convert the existing Finder window UI from icon grid to tree DOM nodes and keep all file mutations scoped through `/v1/workspace`.

**Tech Stack:** Go HTTP server, static HTML/CSS/JavaScript, Playwright with system Chrome for rendered QA.

## Global Constraints

- Do not expose `.Trash` or dotfiles in Workspace navigation.
- Do not add a new backend storage model.
- Keep edits inside `~/.exe/workspace` through existing Workspace APIs.
- Preserve mobile one-window layout and Menu live-window list.
- Do not expose the standalone Editor app icon/window.

---

### Task 1: Regression tests for app list filtering and hidden Workspace paths

**Files:**
- Modify: `internal/server/apps_test.go`

**Interfaces:**
- Consumes: `Server.installBundle`, app list route, Workspace list route.
- Produces: tests that fail if Editor is exposed as a desktop app or hidden paths appear in flat Workspace listing.

- [x] Add tests proving `/v1/apps` omits `Editor`, while other apps remain.
- [x] Add tests proving `/v1/workspace` omits `.Trash/*` and dotfiles from the flat list.
- [x] Run the focused tests and verify they fail before implementation.

### Task 2: Backend filtering

**Files:**
- Modify: `internal/server/apps.go`

**Interfaces:**
- Produces: `handleAppsList` skips `Editor`; Workspace flat list excludes hidden path segments.

- [x] Filter the desktop app list so the `Editor` bundle is not returned.
- [x] Filter Workspace flat recursive listing so any dot-prefixed path segment is hidden.
- [x] Run focused tests and verify they pass.

### Task 3: Workspace tree UI

**Files:**
- Modify: `internal/server/ui/index.html`

**Interfaces:**
- Consumes: `/v1/workspace?dir=<rel>` returning `{entries, free}`.
- Produces: tree DOM with `.workspace-tree`, `.tree-node`, `.tree-row`, `.tree-children`.

- [x] Replace Finder icon grid markup with a tree container.
- [x] Add tree CSS for full-height desktop and mobile display.
- [x] Implement tree loading/rendering/open behavior.
- [x] Preserve drag/drop upload and context menu behavior with tree node targets.
- [x] Remove Finder hide-files state and control.

### Task 4: Editor and Notes cleanup

**Files:**
- Modify: `internal/server/ui/index.html`
- Modify: `data/apps/Editor/index.html`
- Modify: `data/apps/Notes/index.html`

**Interfaces:**
- Produces: no hide/collapse controls; standalone Editor page is single-file only.

- [x] Remove VM Notes Hide Header button and JS.
- [x] Remove internal editor Hide Info button and JS.
- [x] Simplify `data/apps/Notes/index.html` by removing Hide Notes button and side-collapse CSS/JS.
- [x] Simplify `data/apps/Editor/index.html` to accept `?path=<rel>` and edit only that file, with no file list/sidebar.

### Task 5: Docs and rendered QA

**Files:**
- Modify: `internal/server/docs.md`
- Verify: browser-rendered UI.

**Interfaces:**
- Produces: documentation matching Workspace tree and single-file editor behavior.

- [x] Update docs to say Workspace is the file tree and Editor is invoked from Workspace file selection.
- [x] Run Go tests, UI parse checks, and build.
- [x] Restart `exe.service`.
- [x] Verify desktop viewport: load, File/Menu list, Workspace tree, context menu actions, editor autosave, terminal fit.
- [x] Verify mobile viewport: top bar Menu only, Workspace tree fills content, window list works, no hide controls.
