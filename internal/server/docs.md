# Using exe

exe is a personal VM cloud: a single binary running on this machine that
creates persistent Linux virtual machines, lets you (and AI agents) work
inside them over SSH, and can publish any VM port to a real HTTPS address
through your Cloudflare Tunnel. This desktop is its control panel — and
everything you see here can also be driven from a terminal or by an agent
over HTTP and SSH.

## Context, permissions and exposed ports

exe has two security contexts:

- Host windows and apps — Workspace, Terminal, Notes, Configuration, apps and
  this desktop — run against the host daemon and host files under `~/.exe`.
  They are not inside a VM sandbox.
- VM windows — Services, Terminal, Tools, Expose and Notes — act on one named
  guest. Tools launch CLIs inside that guest; those CLIs get passwordless sudo
  through the VM user, and the VM boundary is the sandbox.

Treat the UI/API listener as a control-plane port. If `listen` is bound to
`127.0.0.1`, only local browsers can reach it. If it is bound to a LAN IP,
`0.0.0.0`, or a Tailscale IP, every device on that network path can try to
reach it, so set `api_token` first and keep the token out of screenshots,
logs and shared browser profiles. Tailscale limits reachability to your
tailnet; LAN binding exposes it to the local network; `0.0.0.0` exposes it on
every host interface.

Publishing a VM service is a separate exposure path. Expose/Cloudflare Tunnel
routes traffic from the public hostname to exe's host reverse proxy
(`proxy_listen`) and then into the VM port. The VM service itself should still
have application authentication if it handles private data. If you route the UI
or a VM service through Cloudflare, configure Cloudflare Access or an equivalent
policy when it should not be public.

## The desktop

The menu bar works like the classic Mac it resembles:

- **File** — Upload to Workspace…, Close Window, the live open-window list,
  Show shortcuts for core windows, Refresh, Set API Token….
- **Virtual Machine** — New VM… and reopen the Virtual Machines window.
- **Special** — Join… (pair another exe machine), Cloudflare Status and
  Setup Wizard.
- **Help** — this page, and the Agent Skill Guide for handing exe to a
  coding agent.

Desktop icons: **Workspace** (shared files), **Terminal** (a shell on this
host machine), one icon per VM, one per installed app, plus **Newsfeed** and
the **Trash**. Double-click opens things.

Windows behave like OS 9 windows: drag the title bar to move, drag the
left/right/bottom edges or the grow corner to resize, click the shade box to
collapse a window to its title bar, the zoom box to toggle its size. The
whole layout — positions, stacking, which windows are open — is saved on the
daemon and mirrored live to every browser looking at this desk, so dragging
a window here moves it on your other screens too.

On a phone the desktop becomes a home screen of icons and windows go
fullscreen, one at a time. The mobile **Windows** menu sits beside **Menu**
for quick switching, and minimizing collapses a window to a visible bar
instead of hiding it. Closing a window walks back through the stack like a
phone's back button.

## Virtual machines

Choose **File → New VM…** (or the New VM… button in the Virtual Machines
window). Only the name is required; the defaults are 2 CPUs, 2048 MB of
memory and a 20 GB disk. The very first VM downloads the Debian base image
(~3 GB) once — later VMs clone it and boot in seconds. VMs persist: stopping
one keeps its disk, starting boots it again, deleting destroys the disk too.

Double-click a VM in the list to open its window. The tabs:

- **Services** — TCP ports listening inside the VM, with one-click links,
  plus the routes already published to the web. Servers must bind
  `0.0.0.0` (not `127.0.0.1`) to show up here or be exposable.
- **Terminal** — a full SSH terminal in the browser. Terminal copy/paste uses native browser/OS selection, copy and paste; Ctrl/Cmd+C copies selected terminal text and Ctrl/Cmd+V pastes.
- **Tools** — launch code-agent CLIs and developer tools inside this VM terminal.
  Missing CLIs install first, then the TUI or shell owns the terminal. File transfer stays in Workspace, or use `scp -P 2222` / `sftp -P 2222` directly through the SSH gate.
- **Expose** — publish a VM port to an HTTPS subdomain (see below).
- **Notes** — free-form notes about the VM, saved automatically. Agents are
  told to read these before working in an unfamiliar VM, so write down what
  runs where.

## SSH from your own terminal

The daemon speaks SSH on port **2222**, and the username picks where you
land:

```sh
ssh -p 2222 demo@this-host        # straight into the VM "demo" (auto-starts it)
scp -P 2222 app.py demo@this-host:~/          # scp, sftp, -L/-R all work
ssh -p 2222 -L 8000:localhost:8000 demo@this-host   # tunnel a VM port

ssh -p 2222 exe@this-host         # the lobby: ls, new, start, stop, rm,
                                  # ip, code, expose, routes (--json too)
```

Keys that get in: any public key in the daemon user's `~/.ssh`, the service
key in `~/.exe/ssh/`, and keys listed in `~/.exe/ssh/authorized_clients`
(authorized_keys format — add your phone or laptop key there; edits apply
immediately). There is no first-come key adoption, so the gate is safe to
leave on a LAN.

## VM Tools and agent workflow

The **Tools** tab in a VM window launches guest-side code-agent CLIs and dev
tools inside that VM. CLI agents keep credentials, settings and history in the
guest. Code-agent CLI buttons include Codex, Gemini CLI, OpenCode, Aider, Qwen
Code, Pi and Claude. Developer-tool buttons include git, build-essential,
Python, Node, Docker, Go, Rust, jq, ripgrep, fd, fzf, tmux, Neovim, htop, gh,
CMake, SQLite, zip/unzip, tree, rsync, uv, pnpm, Bun, lazygit, hyperfine,
direnv, just, bat, eza, HTTPie, yq and delta.

For repeatable automation, prefer `exe env`: it can bootstrap a VM from
manifests, attach files, run commands/scripts, continue sessions and create
snapshots. For your own coding agent, use **Help → Agent Skill Guide** or
`exe skill`: exe serves `/skill.md`, a self-contained guide that teaches Claude
Code, Codex, opencode or another agent how to drive the API, SSH gate, jobs,
Workspace transfer and Cloudflare publish flow.

## Publishing to the web

One-time setup: run **Special → Cloudflare Setup Wizard…** with a Cloudflare
API token (Zone → DNS → Edit, Account → Cloudflare Tunnel → Edit) and a
remotely-managed tunnel. The Cloudflare dot in the menu bar shows tunnel
health at a glance.

Then, in a VM's **Expose** tab, pick a port and an optional subdomain (it
defaults to the VM name). exe creates the DNS record, updates the tunnel
ingress, and routes the hostname through its reverse proxy to the VM — one
click later the service is live at `https://<sub>.<your-domain>`. Current
routes are listed in the Services tab and in **Special → Cloudflare
Status…**, where they can be unpublished.

## Publishing to GitHub

Right-click a running VM and choose **Publish to GitHub…** to turn a
project folder inside it into a GitHub repository. One-time setup: create
an OAuth app under github.com → Settings → Developer settings → OAuth Apps
(enable **Device Flow**; no callback URL or client secret needed), put its
client ID in **Configuration → GitHub**, and sign in — a code appears here,
you enter it at github.com/login/device, done.

The dialog lists the folders in the VM's home; pick one, name the
repository (private by default), and Publish. exe installs git in the VM if
needed, commits any uncommitted work as your GitHub account's noreply
identity, creates the repository, and pushes. Publishing again later pushes
the new commits to the same repository.

The point of the design: **no GitHub credentials ever enter the VM.** The
sign-in token lives only on this machine (`~/.exe/github.json`), and the
push travels through a proxy that exists just for that one operation and
answers only for that one repository — the VM's git talks to it without
ever holding a token, on disk or in memory.

## Workspace and files

The **Workspace** is `~/.exe/workspace` on this machine: a shared folder
where you, agents and apps exchange files. The desktop icon opens a tree
view — open folders in the tree, double-click text files to edit them in
place, and double-click images to view them. Right-click a tree node for
Open, Edit, Move To Trash, Get Info, Duplicate and Download; New Folder,
New Text File and Upload target the clicked folder, or the parent folder
when the clicked node is a file. Right-click the tree's empty space to add
items to that window's folder. **File → Upload to Workspace…** brings files
in from this browser. Files can also be dragged from your computer onto the
desktop (lands in the Workspace root), onto a Workspace window (lands in its
folder), onto a folder row (lands in that folder), or onto a file row (lands
beside that file).
New files brought in this way are announced on the Newsfeed, so every desk
in the mesh sees them arrive; overwriting an existing file stays quiet.

## Apps

Icons beyond the built-ins are desktop apps: folders in `~/.exe/apps`, each
just an `app.json` plus an `index.html`, served straight from disk — edit
one and reopen its window, no rebuild. Each app gets private storage under
`~/.exe/appdata` plus the shared Workspace. Apps are a good thing to ask a
coding agent to build for you.

## Joining desks together

**Special → Join…** pairs this exe with another one (say, your laptop's)
using a short one-time code. Joined desks sync continuously: app data,
Workspace files and the Newsfeed flow both ways, with conflicting edits
resolved automatically and the losing copy preserved next to the winner.

The **Newsfeed** is the shared timeline of the mesh: VMs created and
deleted, nodes joining, sync conflicts — and agents can post to it, so
finished work or problems show up on every desk.

## Configuration

**File → Show → Configuration** edits `~/.exe/config.json` in place; most fields
hot-reload on Save, and fields marked `*` take effect after a daemon
restart. Highlights:

- `listen` — the address of this UI and API. Bind it to your Tailscale IP
  to use exe from your phone.
- `api_token` — set it before listening beyond localhost; every API call
  then needs it. Paste it into **File → Set API Token…** in each browser
  (or **Menu → Set API Token…** on mobile; it is kept in localStorage).
- `ssh_user` — the user created in every VM (default `dev`).
- `idle_stop_minutes` — stop a running VM after N minutes with no
  terminal, job or SSH (`0` = never).
- `cloudflare.*` — publishing / Expose.

**File → Show → Daemon Log** streams the daemon's own log when something needs
a closer look. This page lives at `/docs.md`, and the machine-readable
counterpart for agents at `/skill.md`.
