# Using exe

exe is a personal VM cloud: a single binary running on this machine that
creates persistent Linux virtual machines, lets you (and AI agents) work
inside them over SSH, and can publish any VM port to a real HTTPS address
through your Cloudflare Tunnel. This desktop is its control panel — and
everything you see here can also be driven from a terminal or by an agent
over HTTP and SSH.

On the [documentation website](https://exe.v2core.com/docs/), the toolbar's
joined sun and moon buttons choose light or dark paper. The pressed segment
shows the current choice, which this browser remembers between visits.

## The desktop

The menu bar works like the classic Mac it resembles:

- **Apple menu** — About This Computer (see below).
- **File** — New VM…, Upload to Workspace…, Close Window, Refresh.
- **Windows** — reopen the core windows: Virtual Machines, Chat, Newsfeed,
  Icon Editor, Configuration, Log Viewer.
- **Special** — Mac OS 9, Join… (pair another exe machine), Cloudflare Status and
  Setup Wizard, Set API Token….
- **Help** — this page, and the Agent Skill Guide for handing exe to a
  coding agent.
- **Show All Windows** — the button at the right, left of the magnifier:
  every open window shrinks into a grid. The one under the pointer turns
  blue and shows its name; click it to bring it forward, or click the desk,
  the button again, or press Escape to put them all back. The desktop menu
  reaches it as `showall`.
- **Search** — the magnifier: one box finds VMs, chat sessions, notes and
  todos.

**About This Computer**, under the Apple menu, is the OS 9 box: the build,
the machine, its addresses (click one to copy), Built-in Memory, how much of
it is allotted to running VMs, Disk Space where VM disks live, and Largest
Unused Block — what the host could still hand a new VM. Below the rule, one
memory bar per running VM shows its allotment, scaled so the largest fills
the column, with exe's own footprint as the first row. It refreshes every
few seconds while open.

**Updates arrive by themselves.** When the daemon restarts with a new
desktop or new system apps, an open desktop notices and reloads — a few
seconds after the daemon is back, at once in a tab you are not looking at.
It waits while you are typing or clicking, while a menu or dialog is up, and
for as long as there is unsaved work: text typed into a field and still
there (in a closed window too), an editor with unsaved changes, unsaved
Icon Editor pixels. Blue Pencil saves its drafts as you type, so its text
never holds an update. While work holds it, the Apple menu shows **Reload
for Update** to take the update anyway. A restart that changes nothing the
browser runs leaves your windows alone.

Right-click the desktop (long-press on a phone) for the **desktop menu**: a
NeXT-style menu at the pointer, with shortcuts to VMs, apps, windows and
tools. Choose **Customize…** to edit it. The
[Desktop context menu](https://exe.v2core.com/docs/using/desktop-context-menu)
chapter covers the format, every supported action and examples.

Desktop icons: **Workspace** (shared files), **Terminal** (a shell on this
host machine, which on a host with tmux outlives a reload or a closed
browser — below), **Claude Code** and **Codex** (each appears when its CLI is
installed on this host; one persistent session per agent, so closing the
window and reopening it returns to the same conversation), one icon per VM,
one per installed app, plus **Newsfeed**, **Chat** (appears when Ollama is
reachable) and the **Trash**. Double-click opens things.

The **Control Strip** in the bottom-left corner is OS 9's tray. Its first
module is the Cloudflare heartbeat: the lamp on the cloud is green while
the tunnel is healthy, yellow when it needs attention, grey while it is
still checking. Its menu shows the tunnel's replicas and edge connections,
plus this node's request rate, active requests, totals and origin errors.
Open **Cloudflare Status…** for the same live counters and connector uptime.
Local traffic refreshes every five seconds while either view is open;
tunnel-wide connection counts refresh every 30 seconds. Traffic totals
are for this node's connector, include every hostname on its tunnel, and
reset when cloudflared restarts. Unavailable data is shown as a dash.
The menu also offers the Setup Wizard and Check Now.
Right of it, on a machine with Tailscale installed, the **Tailscale**
module: a panel of nine lamps that lights Tailscale's four while the tailnet
is connected, blue while the machine's traffic leaves through an exit node,
yellow when something needs attention (a health warning, a login due), all
dim while Tailscale is off. Its menu says which machine this is and how many
devices are online; **Tailscale Active** and **Inactive** turn it on and off;
**Exit Node** picks one of the devices offering one (and Allow LAN Access);
**Devices** lists the online ones and **Serve** the Tailscale Serve rules —
pick a row to copy its address; then Accept Routes, Use Tailscale DNS,
Shields Up and Tailscale SSH toggle (rest the pointer on one for what it
does), and Admin Console… opens Tailscale's.
Turning it off or raising shields while the desktop is itself reached
through Tailscale asks first, because the desktop would go with it. The
daemon asks the `tailscale` CLI on the machine and changes only those
settings (`GET /v1/tailscale`, `POST /v1/tailscale/set`); it runs as
Tailscale's operator user, so no sudo. Beside it, the **Solana ticker**
shows a token's coin and its dollar
price: SOL, or PUMP, MET or SKR — pick the one the tile wears from its menu.
The menu lists them all with their day change, and the ecosystem tokens
with their price in SOL as well — the zeros after the point fold into a
small sunk count, so PUMP's 0.00003717 SOL reads 0.0₄3717 SOL, MET's
0.002404 SOL reads 0.0₂2404 SOL, and the column lines up (a dollar price
folds from three zeros); the figures are Coinbase's public spot
prices, fetched once a minute by the daemon and shared by every desktop on
the node. **Notify Me of Big Moves** in that menu turns on push
notifications for this device (on a phone, the app added to the Home
Screen): you hear when a token moves more in an hour or a day than it
rarely does — SOL 2.5% / 6%, PUMP 5% / 12%, MET 6% / 15%, SKR 8% / 25% —
never more than four times per token in 24 hours; a move that qualifies
while the four are spent is counted into the next one. **Recent Moves**
lists the last ones, **Send a Test Notification** checks the road, and the
menu item again turns it off. Next to it, the **agent usage meter** shows
how many tokens Claude Code or Codex has used on this machine today —
pick the agent the tile wears from its menu, and **Show Plan Limit** puts
the fullest of that agent's usage windows there instead (`46% wk`). The
menu lists both agents with today's tokens and their five-hour and weekly
windows, the windows in columns so one agent's week stands under the
other's (a window an agent lacks stays blank); each agent's submenu splits today and the last seven days into
tokens sent fresh, read back from the prompt cache and written, draws the
week as a column a day — a figure on the busiest day and on today, every
day's own split in its tooltip — and says when each window resets. The daemon counts from the CLIs' own logs —
Claude Code's transcripts, Codex's rollouts — so every session on the
machine is in, not only the ones opened from this desktop
(`GET /v1/agents/usage`). Claude Code's windows are as fresh as its last
reply in a Claude Code window here; Codex's are read live on the ChatGPT
sign-in. An agent appears once it is installed and has left figures; with
neither, the tile stays away. The tab at the right hides the strip down to
the tab alone.

With notifications on, the daemon also watches the sky over the first city
in the Weather app — the row your drag put on top. When Open-Meteo's
quarter-hour rows put rain in the next 60 minutes, a push says **Rain
possible soon** — or **likely**, when the chance is high — and a second
one says **Next hour looks dry** when a fresh forecast clears it. Three
more hazards ride the same watch, their lines the National Weather
Service's own: **Dangerous heat** at a feels-like of 105°F, **High wind**
at gusts of 35 mph, and **Fire weather** when humidity at or under 15%
meets those gusts — a red-flag hour that then keeps the plain wind push
quiet, so a Santa Ana taps once. Each hazard clears with its own easing
push, at most four warnings per hazard in 24 hours, in °F and mph for a US
city and °C and km/h elsewhere. Reorder the Weather list and the watcher
follows; there is no setting to flip.

A Claude Code window's status line shows the session's figures at its
right — the model, context in use, tokens, cost and the plan's 5-hour and
7-day usage windows — kept current after every reply by Claude Code's own
status-line hook; hover for the long form. A status line of your own in
`~/.claude/settings.json` keeps working inside the terminal. A Codex
window's shows the ChatGPT subscription's 5-hour and weekly usage windows
(the sign-in under **Configuration → OpenAI**), re-read once a minute
while the window is open; hover for the reset times.

The mouse wheel scrolls back through the conversation in either window.
Claude Code takes the wheel itself and scrolls its own transcript. Codex
leaves its transcript to the terminal, and on a host with tmux that is
the tmux session's history: the wheel scrolls it there (tmux's copy
mode, with its `[123/980]` position at the top right), and typing
returns to the live screen first, so the keys reach Codex as in any
terminal.

To copy text, drag across it. In a Codex window that selects it as in
any terminal; right-click and choose **Copy**. Claude Code in its
fullscreen mode draws the selection itself, and as the drag ends the
text goes straight onto your computer's clipboard. Should the browser
refuse the page its clipboard, the text waits for **Copy** in the
right-click menu instead, until your next click or key. Shift+drag
(Option+drag on a Mac) makes the terminal's own selection in a Claude
Code window, for **Copy** the same way.

To hand the agent a file — a screenshot, a log, a document — drag it from
your computer onto the window. It is uploaded to the Workspace root, the
same as a drop on the desktop, and its path on this machine is typed at
the cursor, quoted if it needs it, so "look at this" is all the prompt
then needs; the agent reads the file from there (Claude Code shows an
image it is given a path to). Several files arrive as several paths. A
plain Terminal window takes a drop the same way; a VM's window does not,
since a host path means nothing in the guest. If the window's link is
down when the upload lands — a laptop back from sleep, the daemon
restarting — the file is in the Workspace all the same, and its path
waits in a row under the terminal: **Insert** types it once the window
is connected again, **Copy** puts it on the clipboard for another window,
**Dismiss** forgets it. Nothing is uploaded twice.

On a host with tmux, a Claude Code or Codex window lists the agent's
sessions down its left, one row per tmux session, titled the way the CLI
titles its terminal — Claude Code keeps that on its current task, and
Codex, which the desktop starts with its terminal title set to the
thread, on the thread's name (a session started before that keeps its
old title until it is restarted). Click a
row and the window moves to that session; **New** starts another
conversation in a session of its own, at the top: the list runs latest
first, the icon's own session at the bottom. While the window
shows another session, a pulsing green dot marks one still working and a
black dot one that waits for you — its turn finished, or a permission or
question pending. Claude Code reports those through its hooks; Codex
through its `notify` command, which the desktop points at the session
(a `notify` of your own in `~/.codex/config.toml` still runs after it),
and through the terminal bell, which the desktop has Codex ring at the
end of a turn and for an approval so tmux notes it while no window is
looking. Hover a row for which. The status line's
figures follow the session on screen. A window opens on the session it
showed last — from any browser, and after the daemon restarts: tmux
itself remembers where the window was — with its row scrolled into
view, and dragging the line between the list and the terminal sets the list's
width, kept with the window's layout. A row's contextual menu opens
with when the session started, in grey, then starts a new session,
copies the row's title, or archives the session, after a dialog: the session and its process end, and the conversation stays on
this machine for `/resume` inside Claude Code or Codex — the desktop keeps no list
of its own. Archive shows only once the conversation exists; when the
window was showing that session it moves to the one before it.

A window whose link drops — the tab left behind while the laptop slept, a
network that came and went, the daemon restarted — reconnects on its own,
in the background, to the session it showed; a window whose tmux client
was detached with the sessions still there does the same. Only when the
agent's last session has ended does it stop and say so: close and reopen
it for a fresh conversation. The same session can be open in more than
one window at once — a phone beside the desktop — and the terminal takes
the size of whichever was attached, typed in or resized last.

A Terminal window keeps its shell the same way on a host with tmux: each
one is a tmux session of its own (`exe-term-1`, `exe-term-2`, … — `tmux
attach -t exe-term-2` picks one up over SSH), so reloading the page,
closing the browser by accident, the laptop sleeping or the daemon
restarting only takes the window away from the shell, never the shell.
The window reconnects on its own, and the next time the desktop loads in
this browser the Terminal windows it had come back where they were, with
whatever ran in them still running; so does any Terminal no window shows
anywhere, one left on a phone say. A Terminal another desk is showing
stays there. The close box ends the shell, as in any terminal app, and so
does `exit`: the window closes with it. tmux stays out of the way in
these sessions — no status line, no prefix key, so Ctrl+B reaches the
shell, and `tmux` run inside works as it would anywhere — and the wheel
scrolls back through the session's history as in a Codex window. On a
host without tmux a Terminal is a one-off shell that ends with its window.

A Codex thread started elsewhere on this machine — in the ChatGPT app on
your phone (its remote Codex runs here), the Codex app, VS Code — is a
conversation on this machine all the same, and the Codex window's column
lists the latest ten under a rule below its sessions, titled the way the
app titles them, with a hollow dot (a green one while a turn runs there).
Hover a row for where and when it started. Click it, or choose **Continue
Here** from its menu, and the thread opens in a session of its own, in the
folder it was started in: the row moves up among the sessions and the
conversation carries on here; leave it be and it stays where it is. The
API takes the same: `POST /v1/agents/codex/sessions` with `{"resume":
"<thread id>"}`, and `GET …/sessions` lists the threads beside the
sessions.

The Claude Code window's column lists Claude Code sessions started
elsewhere on this machine the same way — `claude` run in a Terminal
window, over SSH, in an IDE — the latest ten, titled as Claude Code
titles them (your own `/rename` first), with the hollow dot; a green one
while its CLI is still open there, or a turn runs there. A session whose
window or shell died with it — a Terminal window closed, the daemon
restarted — is right there to click: it continues in a session of the
column's own, in the folder it was started in, as `claude --resume` would.
Headless runs (`claude -p`, the hub watcher's turns) are not listed, nor
are the column's own sessions. `POST /v1/agents/claude/sessions` with
`{"resume": "<session id>"}` is the same from the API.

The column is also an API, for tools that want a conversation you can
watch: `GET /v1/agents/claude/sessions` lists the rows with their states,
`POST /v1/agents/claude/sessions` opens a numbered session with a first
message (and, for Claude Code, a session to resume or fork, a permission
mode and a `model`), `POST …/sessions/<name>/prompt` types a message into one, `DELETE
…/sessions/<name>` ends it. The message's `prompt` goes in as a paste, and
Claude Code treats pasted text as material rather than as your words, so
give the request a `say` as well: one line, typed ahead of the paste, that
tells the session what to do with it. The hub watcher builds this way: an instruction
you post in one of Claude's hub threads opens as a session in the Claude
Code window's column, works there in view, reports in the thread when it
is done and stays open in the window to be continued.

Windows behave like OS 9 windows: drag the title bar to move, drag the
left/right/bottom edges or the grow corner to resize, click the shade box to
collapse a window to its title bar, the zoom box to toggle its size. The
whole layout — positions, stacking, which windows are open — is saved on the
daemon and mirrored live to every browser looking at this desk, so dragging
a window here moves it on your other screens too.

On a phone the desktop becomes a home screen of icons and windows go
fullscreen, one at a time; a tapped icon swells and fades as its window
comes up, and closing a window walks back through the stack like a
phone's back button. In a Claude Code or Codex window a finger drag
scrolls back through the session's history, and a flick keeps it going.
In Todo and Weather a finger scrolls the list; to move a row, press and
hold it for half a second until it tints, then drag it to its new place.

Over HTTPS — a Tailscale Serve address, say — the desktop installs as an
app: **Add to Home Screen** on an iPhone or iPad, the install button in
Chrome's or Edge's address bar. It then opens in a window of its own with
the menu bar right under the status bar. When the daemon is not answering —
restarting, or the device offline — the desktop shows an alert in place of
the browser's error page and comes back by itself once the daemon does.

Every system icon is hand-plotted pixel art, and **Windows → Icon Editor**
lets you repaint it: the gallery lists each one (the VM Mac, folders,
documents, the Trash, built-in apps such as Mac OS 9, the minis in search
results, even the Apple menu),
and double-clicking opens a fat-bits editor — pencil, eraser, eyedropper,
fill, undo, the Platinum palette plus a custom color well. Save applies the
art everywhere at once, follows the desk into every browser and joined
node, and survives restarts; Revert brings the factory icon back. On a VM
icon, pixels painted the factory screen-green keep changing color with the
VM's state. **New Icon…** adds icons of your own on a 32×32 or 16×16 grid —
draw them, copy their SVG for use anywhere, delete them when done. System
icons can only be repainted, never deleted. Restored editors keep their position,
stacking and shaded state even when their icon loads after the desktop layout.

## Desktop context menu

The desktop context menu is a NeXT-style menu that opens at the pointer.
Right-click an empty part of the desktop, or long-press it on a touch
screen. A quick two-finger tap also opens the menu; on a phone this works
inside an ordinary window too, away from controls, menus and dialogs.

The factory menu offers New VM, Terminal, Workspace, lists of VMs, apps
and windows, Show All Windows, tools, Cloudflare and Help. You can rename
or reorder items, add shortcuts and group them in submenus.

### Edit and save

Choose **Customize…** to open the menu editor. Edit the text, then click
**Save**, or press **Command-S** on a Mac or **Ctrl-S** elsewhere. Save
checks the format first: an error identifies and selects the bad line,
and your current menu stays in place until the file is valid.
The scrollbars run along the document's right and bottom edges; drag the
resize tile where they meet to make the editor wider or taller.

The menu is stored as `System :menu.txt` (normally
`~/.exe/appdata/System/menu.txt`). A saved change reaches other open
desktops and syncs to joined nodes. Each desktop fills the automatic lists
from its own VMs, installed apps and open windows. Existing custom menus
keep their contents when the factory menu changes; add new shortcuts to
your own file with **Customize…**.

### Menu format

Write one item per line. Separate the label from its action with a tab or
at least two spaces. Single spaces are fine inside a label; two spaces
end it. Action names, list names and VM tab names are lowercase and
case-sensitive.

In the format examples below, `<TAB>` means a tab character, not the
literal text `<TAB>`. The complete example menu further down uses spaces
and can be copied directly.

| Line | Meaning |
|---|---|
| `Terminal<TAB>terminal` | A label, a tab, then an action. Two or more spaces work too. |
| `Tools` | A submenu heading. Put at least one indented item below it. |
| `-` | A separator. A line made only of several hyphens works too. |
| `# My shortcuts` | A comment on its own line; it may be indented. Blank lines are ignored. |
| `@apps` | Insert an automatic list at this position. |
| `Applications<TAB>@apps` | Put an automatic list inside a named submenu. |

Indent submenu children consistently, for example with two spaces per
level. The top level has no indentation. When returning to a parent
level, use exactly its earlier indentation. Only a plain submenu heading
can have indented children; an action or automatic list cannot.

Except for `terminal`, arguments are separated by whitespace: quotation
marks do not combine words into one argument. A Workspace path or app
name containing spaces therefore cannot be used directly in an `edit`,
`workspace` or `app` item. Use `workspace` to browse to that file or folder,
or `@apps` to list apps by their display names. Put comments on their own
lines, rather than after an action.

### All supported actions

The following table lists every supported action. In the syntax column,
angle brackets mark a required value and square brackets an optional one;
replace these placeholders rather than typing the brackets. Each action
needs a label before it, as in `My terminal<TAB>terminal`.

| Action syntax | What it does |
|---|---|
| `about` | Open About This Computer. |
| `newvm` | Open the New VM dialog. |
| `upload` | Open Upload to Workspace. |
| `closewin` | Close the front window. |
| `showall` | Toggle the overview of all open windows. |
| `refresh` | Refresh the VM and app lists, including My Apps when it is open. |
| `terminal [command]` | Open a new terminal on the host. With a command, run that command instead of an interactive shell. |
| `claude` | Open the host's Claude Code window. Disabled when its CLI is unavailable. |
| `codex` | Open the host's Codex window. Disabled when its CLI is unavailable. |
| `workspace [folder]` | Open the Workspace root, or a folder relative to that root. |
| `edit <file>` | Open a file in the text editor, using a path relative to the Workspace root. |
| `app <name>` | Open an installed app by its app ID. Disabled when it is unavailable. |
| `vm <name> [tab]` | Open a VM's detail window, optionally on a specific tab listed below. Defaults to Services; disabled if the VM does not exist on this node. |
| `chat [vm]` | Open Chat. With a VM name, show that VM's chats and pin new chats to it; without one, show all chat sessions. Disabled when Chat is unavailable. |
| `winvms` | Open the Virtual Machines window. |
| `winmyapps` | Open My Apps, the window of published sites and their domains. |
| `winchat` | Open Chat with all sessions, the same as `chat` without an argument. Disabled when Chat is unavailable. |
| `winnews` | Open the Newsfeed. |
| `winicons` | Open the Icon Editor. |
| `winconfig` | Open Configuration. |
| `winlog` | Open the Log Viewer. |
| `search` | Open desktop Search. |
| `trash` | Open the Trash. |
| `join` | Open the Join dialog to pair another exe node. |
| `cfstatus` | Open Cloudflare Status. |
| `cfwizard` | Open the Cloudflare Setup Wizard. |
| `token` | Open Set API Token for this browser. |
| `url <address>` | Open an `http://` or `https://` address in a new browser tab. |
| `docs` | Open the Using exe manual in the desktop. |
| `skillguide` | Open the Agent Skill Guide. |
| `customize` | Open this menu's editor. |

For `workspace` and `edit`, use relative paths such as `Projects/demo` or
`Projects/demo/README.md`. Absolute paths and paths that escape the
Workspace are rejected. For built-in apps, use their lowercase IDs:
`app macos9`, `app hub` or `app bluepencil`. Other apps use their installed
folder name. There is no separate `macos9` action in the customization
format; use `app macos9`.

The command after `terminal` is kept as one command line, including spaces,
quotes and shell operators. For example, `Monitor<TAB>terminal btop` opens
btop on the host if it is installed. On Linux and macOS it runs in the
host user's login shell; on Windows it runs in PowerShell. The terminal
session ends when the command exits. To reach a VM's shell instead, use
`vm <name> term`.

A command's window opens at 80×31 characters at least — what btop needs
with its GPU box, and room enough for any other tool — and cannot be
dragged smaller. It remembers its size and position, so the next open of
the same command puts it back where it was left. Choosing the item again
while the command runs brings its window forward; after the command has
exited, it runs it again in the same window's place.

### VM tabs

Use these values as the optional second argument to `vm`. For example,
`Demo terminal<TAB>vm demo term` opens the Terminal tab of the VM named
`demo`.

| Tab | Opens |
|---|---|
| `svc` | Services (the default). |
| `term` | Terminal. |
| `vibe` | Agent. |
| `expose` | Expose. |
| `sess` | Sessions. |
| `notes` | Notes. |

### Automatic lists

These lists fill themselves when you open the menu. Use a list name alone
to insert its entries in place, or give it a label to make a submenu.
They take no arguments. An empty list shows a disabled **No VMs**,
**No Apps** or **No Windows** entry.

| List | Contents | Named submenu example |
|---|---|---|
| `@vms` | Every VM on this node, each with an operations submenu. | `Virtual Machines<TAB>@vms` |
| `@apps` | Installed apps, including built-in apps, shown by their display names. | `Applications<TAB>@apps` |
| `@windows` | Open windows on this desktop, sorted by title. Pick one to bring it forward. | `Windows<TAB>@windows` |

Each `@vms` submenu offers **Open**, **Open Terminal**, **btop**, **Chat with this
VM**, **Start** or **Stop**, **Restart**, **Copy IP**, **Expose Port…** and
**Publish to GitHub…**. Availability follows the VM's state, its IP and
Chat availability. Delete is not included. These operations are supplied
by `@vms`; names such as `start`, `stop` and `restart` are not standalone
customization actions.

### Example menu

This example uses two spaces between a label and its action, and two
spaces of indentation for each submenu level. Replace `demo` and the
Workspace paths with your own VM and files; install btop on the host to
use the monitor shortcut.

```text
# My desktop menu
New VM…  newvm
Terminal  terminal
Workspace  workspace
-
Projects
  Demo folder  workspace Projects/demo
  Demo README  edit Projects/demo/README.md
  Demo terminal  vm demo term
  Demo notes  vm demo notes
Tools
  Monitor  terminal btop
  Claude Code  claude
  Codex  codex
  Mac OS 9  app macos9
  Hub  app hub
Virtual Machines  @vms
Applications  @apps
Windows  @windows
Show All Windows  showall
-
Help
  Using exe  docs
  Online documentation  url https://exe.v2core.com/docs/
Customize…  customize
```

### Limits and recovery

The menu may contain up to 300 entries, counting headings, actions,
automatic-list entries in the file and separators across all levels.
Expanded list contents do not count toward that limit. Labels may be up
to 60 characters; the file must be UTF-8 and at most 64 KiB. You can nest
three submenu levels below the root (four levels in total).

If you remove `customize`, the desktop appends **Customize…** so you can
always get back to the editor. If a stored or synced file cannot be
parsed, the desktop uses the factory menu and adds **The menu file has an
error…** to open the editor on the problem.

To reset, choose **Restore Defaults** and confirm, or clear the editor
completely and **Save**. This removes the custom menu from every desktop
sharing the desk. Only an empty or whitespace-only file restores defaults;
a file containing just comments is still a custom menu.

## Mac OS 9

Choose **Special → Mac OS 9** (also available among the built-in apps) to open
an interactive Power Mac G4 running Mac OS 9.2.2. Its Monitors control panel
offers only 640×480, 800×600, and 1024×768, with 800×600 as the default. The first launch shows each
setup step: preparing QEMU, downloading the 497 MiB Universal installer,
checking its checksum, creating a 2 GB persistent disk, and starting the Mac.
**Setup details** opens these steps and live download progress in a compact in-app
dialog over the Mac. Close it with **OK** or Escape; setup continues, and the
guest display keeps its size and connection.
Automatic emulator installation supports Ubuntu 24.04; other hosts need
`qemu-system-ppc` and `qemu-img` installed first (on macOS, `brew install qemu`).

The app guides you through Drive Setup and Apple Software Restore inside the
Mac. After Restore reports success, shut down the guest and click
**Installation finished — start my Mac**. Subsequent launches use the saved
installation. Setup can be paused and retried, and closing the window keeps
both setup and a running Mac alive. An open window reconnects automatically
after an exe restart or a temporary network interruption, returning to the same
running Mac. A failed Start keeps its error visible so you can address the cause
and retry. If exe restarts during a download, choose Continue setup once
the connection returns. Use **Resume installer** if the Mac was shut down before Restore completed.
Completed installer downloads are reused
after checksum verification; partial downloads restart.

The pointer follows your browser cursor directly, including when you leave and
re-enter the window. A bundled open-source USB tablet driver loads at boot;
no guest installation is needed. Mouse-wheel scrolling is not supported by
that driver; use the Mac’s scrollbar controls.

The Mac’s clock starts at this machine’s own local date and time, so a browser
in the guest judges today’s certificates against today’s date and HTTPS works
without a visit to Date & Time. Only the installation boot is pinned to 2003,
where the Universal installer and the era’s software expect to be.

Click **CD…** in the toolbar to see the full mounted CD filename, mount an
available disc image, or **Eject** it. The toolbar also shows the filename when
space permits. **Upload image…** adds an ISO, CDR, IMG, or Toast raw disc image
(up to 2 GiB) from your browser; select it and click **Mount**. Images stay on
this node and existing files are kept when filenames match. Changes take effect
without restarting the Mac. The displayed filename follows ejects inside Mac
OS 9 too. If the Mac locks the disc, eject it in Finder first, or close programs
using it before choosing **Force eject**. Force eject can leave the old volume
visible in Finder; restart the Mac if that happens. After a Mac restart, mount the desired
CD again. On a phone, tap the CD glyph to see its full filename and controls.

If this node has the optional Mac audio runtime installed, click **Sound off**
to enable sound in your browser; the button changes to **Sound on**. Click it
again to mute. Browser playback needs this first click. Sound stops when the
window is closed or hidden, and an enabled session resumes after reconnecting.
The button is disabled on nodes without audio support. On narrow screens it
shows a speaker glyph. Sound travels through the existing authenticated display
connection; it does not play through the server's speakers.

If the connected display turns black after being idle, choose **Mac keys… →
Wake display**, or press Shift with the Mac focused. This wakes Energy Saver
without typing or restarting the guest; mouse movement alone may not wake it.
To keep the Mac awake, open **Apple menu → Control Panels → Energy Saver**
inside the guest and set system sleep to **Never**. Under **Show Details**,
also disable a separate display-sleep timer if one is enabled.

The app window fits the selected guest resolution automatically. Each guest
pixel occupies a whole number of physical screen pixels (1×, 2×, 3×, and so on),
including at Windows’ 125% or 150% display scaling. The largest crisp size that
fits is used; this can be smaller than a fractionally enlarged display.
There is no separate grow tile. If even 1× will not fit, the display shrinks
proportionally to keep the whole Mac visible. Full screen uses the same scaling
and adjusts when you move between monitors or change browser zoom.

Use **Full screen** for more room. When browser fullscreen is unavailable,
including in iPad Home Screen apps, the Mac expands within exe; tap
**Exit full screen** to return. Browser fullscreen also supports older iPad
Safari; use the browser’s exit control or Escape to return.
Use **Mac keys…** for common Command-key
shortcuts. Shut down from **Special → Shut Down inside the Mac** to save its
files cleanly. The installed guest has a `sungem` Ethernet adapter with outbound NAT and
DHCP; networking is disabled while booting the installer. Classic HTTP browsers work; modern HTTPS compatibility depends on the
guest browser. Audio is not configured.

The runtime, installer, setup progress and disk live in `~/.exe/mac-os9/`
(or `$EXE_HOME/mac-os9/`), outside app-data sync. The display uses private local
sockets and the same API token as exe. No public VNC port is opened. The API is
`GET /v1/macos9`, `POST /v1/macos9/start`, `/cancel`, `/finish`, and the binary
WebSocket at `/v1/macos9/console`.

## Virtual machines

Choose **File → New VM…** (or the New VM… button in the Virtual Machines
window). Only the name is required; the defaults are 2 CPUs, 2048 MB of
memory and a 20 GB disk. The very first VM downloads the Debian base image
(~3 GB) once — later VMs clone it and boot in seconds. VMs persist: stopping
one keeps its disk, starting boots it again, deleting destroys the disk too.

The **System** pop-up picks the VM's Linux: **Debian 13**, the default, or
**Alpine 3.24** — a ~93 MB download the first time, a lean guest for
disposable experiments. An Alpine guest runs OpenRC and `apk`, not systemd
and `apt`; its user's shell is `ash`, and prebuilt glibc binaries do not run
on its musl libc. Chat, a chat pinned to the VM and the Agent tab are told
which system a VM runs — its recorded image opens their context — so on
Alpine they reach for `apk` and `doas`; Publish installs with `apt` and
expects the Debian image.

A node without a hypervisor — a NAS, a container without `/dev/kvm` — runs
the desktop without VMs: the list stays empty and says why, About This
Computer shows the same reason, and everything else works as usual.

Right-click a running VM and choose **btop** to open its process monitor in
an 80×24 terminal window. It runs the VM's installed `btop` over SSH; press
**q** to quit and close the window. A failed launch leaves its error visible.

Double-click a VM in the list to open its window. Its status line shows the
state lamp, then middot-separated: the IP, how much disk the VM really holds
on the host — allocated space, not the sparse file's nominal size — and the
guest's system with its version, read from the guest itself. At the line's
right edge, its load average over one, five and fifteen minutes, refreshed
every five seconds while the window is open. The tabs:

- **Services** — TCP ports listening inside the VM, with one-click links,
  plus the routes already published to the web. Servers must bind
  `0.0.0.0` (not `127.0.0.1`) to show up here or be exposable.
- **Terminal** — a full SSH terminal in the browser.
- **Agent** — tell the built-in coding agent what to build; it gets a shell
  in this VM and streams its work live.
- **Expose** — publish a VM port to an HTTPS subdomain (see below).
- **Sessions** — every chat pinned to this VM, agent runs included, each
  with a model-written one-line summary of what it accomplished; click one
  to reopen it in the Chat window.
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

## The coding agent

> **This build (jstdlee):** the Ollama/ChatGPT agent below is removed. Use the **Board** (see *jstdlee extensions* at the end) with Claude Code or Codex.

Point exe at Ollama in **Windows → Configuration** (`ollama.base_url` and
`ollama.model`; `ollama.effort` sets the thinking effort on models that
support it, or `off` to disable thinking). A local signed-in Ollama at `http://127.0.0.1:11434` can
use cloud models like `glm-5.2:cloud` with no API key; `https://ollama.com`
needs one. Then:

- The **Agent** tab in a VM window starts the agent on that VM: **Run
  Agent** opens a new chat pinned to the VM, your prompt its first
  message, and the Chat window streams the run and takes what you say
  next. It can install packages, write code and start services — it has
  passwordless sudo (doas on Alpine) *inside the VM*, and the VM is the
  sandbox boundary.
- The **Chat** icon and window appear once a chat backend is usable: a
  conversation that can see and drive your whole VM cloud. Replies run in
  the daemon, not in the browser: closing the tab (or losing the network)
  never interrupts a long task — reopen the chat and select the session,
  marked with a green dot while streaming, to rejoin it live. The **Stop**
  button actually cancels the run.

The Chat window can also run on a **ChatGPT subscription** instead of
Ollama: in **Windows → Configuration → OpenAI**, click **Sign in with
ChatGPT…** (the OAuth flow the Codex CLI uses — no API key), set
`chat_provider` to `openai`, pick a model (`gpt-5.4`, `gpt-5.4-codex`, …)
plus an optional reasoning effort, and Save. The browser sign-in redirects to `localhost:1455`; the daemon
listens on all interfaces there, so when it runs on another machine swap
`localhost` for the daemon's host in that final URL — or paste the URL
into the tab's paste field. Tokens live in `~/.exe/openai.json` and refresh themselves.
While signed in the tab also shows the subscription's rate-limit usage —
the rolling 5-hour and weekly windows, with their reset times — and any
credit balance. A VM's Agent tab follows the same choice, since what it
starts is a chat: with `chat_provider` set to `openai` its runs are on
the ChatGPT subscription too. What stays on Ollama whatever is chosen is
`exe code` on the command line, and the `POST /v1/vms/{name}/agent` call
behind it.

Prefer your own agent? See **Help → Agent Skill Guide**: exe serves a
`/skill.md` file that teaches Claude Code, Codex or any other coding agent
how to drive the API and the VMs.

## Publishing to the web

One-time setup: run **Special → Cloudflare Setup Wizard…** with a Cloudflare
API token (Zone → DNS → Edit, Account → Cloudflare Tunnel → Edit) and a
remotely-managed tunnel. The Cloudflare module in the Control Strip
(bottom-left) shows tunnel health at a glance.

Live local traffic uses cloudflared's loopback metrics listener at
`http://127.0.0.1:20241`. If it uses another local port, set
`cloudflare.metrics_url` in **Configuration → Cloudflare** to the base
address (without `/metrics`). exe verifies that the local connector belongs
to the selected tunnel. A machine without a local cloudflared connector
can still show the tunnel-wide connection counts; its local counters stay
unavailable. A Cloudflare API failure labels cached connection counts as
last known rather than reporting them as live.

Then, in a VM's **Expose** tab, pick a port and an optional subdomain (it
defaults to the VM name). exe creates the DNS record, updates the tunnel
ingress, and routes the hostname through its reverse proxy to the VM — one
click later the service is live at `https://<sub>.<your-domain>`. Current
routes are listed in the Services tab and in **Special → Cloudflare
Status…**, where they can be unpublished.

The CLI can also publish a permanent redirect without a VM:

```sh
exe expose example.com -redirect https://exe.example.com
exe expose www.example.com -redirect https://exe.example.com
```

Use your configured domain or one of its full subdomain names. The target
must be an HTTP(S) origin, with no path, query, fragment or credentials.
exe creates the same DNS and tunnel rules, then answers with **308 Permanent
Redirect**, preserving each request's path and query string. For example,
`https://example.com/docs/?from=home` becomes
`https://exe.example.com/docs/?from=home`. The method and body survive when
the client follows the redirect. `exe routes` lists these routes as
`redirect:https://…`; `exe unexpose <host>` removes one.

A hostname can also route to a service on this machine that is not a VM,
such as a daemon on the loopback address:

```sh
exe expose charts.example.com -backend http://127.0.0.1:7799
```

The backend is an HTTP(S) origin with no path; every request's own path
and query reach it, with the hostname in `Host`, so one backend can serve
several names. The same DNS record, tunnel ingress and proxy route are
created, and `exe unexpose <host>` removes them. The API form is `POST
/v1/routes` with `host` and `backend`.

### Local services

A daemon on this machine's loopback, such as `127.0.0.1:7799`, is out of a
browser's reach when the desk is opened over Tailscale Serve or from a
phone. Name it in `~/.exe/config.json` and the daemon relays to it:

```json
"services": { "planet": "http://127.0.0.1:7799" }
```

`/v1/svc/planet/<path>` then reaches `http://127.0.0.1:7799/<path>` with
the method, body and query unchanged. The API token is checked here and
never forwarded, nor are the desk's cookies; the service's cookies and
CORS headers stay behind. A change to `services` through **PUT
/v1/config** takes effect at once.

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

The Chat agent can do the same: tell it to "push to github" and it uses the
daemon's github_push tool — a plain `git push` inside a VM always fails,
because that is the point.

The point of the design: **no GitHub credentials ever enter the VM.** The
sign-in token lives only on this machine (`~/.exe/github.json`), and the
push travels through a proxy that exists just for that one operation and
answers only for that one repository — the VM's git talks to it without
ever holding a token, on disk or in memory.

## Workspace and files

The **Workspace** is `~/.exe/workspace` on this machine: a shared folder
where you, agents and apps exchange files. The desktop icon opens a Finder
view — double-click text files to edit them in place, images to view them,
web pages (`.html`) to see them rendered in a window of their own (the page
runs sandboxed, apart from the desktop; the right-click menu's **Open in
New Window** shows it in a browser window instead, and **Edit Source**
opens the text). A movie (`.mp4`, `.mov`, `.webm`) or a sound (`.m4a`,
`.mp3`, `.wav` and friends) opens in a QuickTime-style player window: play,
scrub, step a frame at a time, click the speaker to mute. The movie streams
from the daemon, so a long clip starts at once and scrubbing seeks instead
of downloading. The **Artifacts** folder is where the agents
publish the pages they make — Claude's claude.ai artifacts land there.
Right-click for Get Info and Download; right-click a window's empty space
for New Folder, New Text File and Upload; **File → Upload to Workspace…**
brings files in from this browser. Files can also be dragged from your
computer onto the desktop (lands in the Workspace root), onto a Finder
window (lands in its folder), onto a folder icon (lands in that folder),
or onto a Claude Code, Codex or Terminal window (lands in the root, and
the path is typed into the window).
New files brought in this way are announced on the Newsfeed, so every desk
in the mesh sees them arrive; overwriting an existing file stays quiet.

## Apps

Built-in app IDs use lowercase names (`macos9`, `hub`, `bluepencil`); their
display titles come from `app.json`. Older links and saved settings still
work. Existing app data keeps its original storage and sync namespace.

Icons beyond the built-ins are desktop apps: folders in `~/.exe/apps`, each
just an `app.json` plus an `index.html`, served straight from disk — edit
one and reopen its window, no rebuild. Each app gets private storage under
`~/.exe/appdata` plus the shared Workspace. Apps are a good thing to ask a
coding agent to build for you.

Two apps come with exe-planet, the site builder that runs beside exe (its
`apps` folder goes in `apps_dirs`). Writer edits any Markdown file in the
Workspace: the text on the left, the page it makes on the right, saved as
you type; an edit made elsewhere reloads a clean file and leaves an unsaved
one alone. Planet is three columns like the Mac app: your sites, the chosen
site's posts, pages and drafts as a list view, and the chosen article's
built page. New Post and Edit open the article in Writer; right-click a
site or an article for the rest. Publish… is where a site leaves this
node: expose it under a name in your domain (the same route, DNS record
and tunnel rule a VM's Expose makes), or send every changed build to
IPFS under a key this node keeps, or both. Both are off until you turn
them on, and Export… bundles the site with its key for another node.

The same sheet can put an exposed site on an exe hub. Turn on **Announce**
and give the hub's address: this node invites the site's own key there
(the site's key is also its IPNS name and a Solana address), and from then
on each post you publish — a draft moved into Posts — announces itself on
the hub under the site's name, with a link back. Under the post on your
site, a Reply window and a Replies window show the answers it gets, live;
a reader replies with a Solana wallet that meets the hub's gate, signing
a message and never a transaction. Posts published before you turned it
on stay put; right-click one and choose **Announce on Hub** to send it.

## Joining desks together

**Special → Join…** pairs this exe with another one (say, your laptop's)
using a short one-time code. Joined desks sync continuously: app data,
Workspace files and the Newsfeed flow both ways, with conflicting edits
resolved automatically and the losing copy preserved next to the winner.

The **Newsfeed** is the shared timeline of the mesh: VMs created and
deleted, nodes joining, sync conflicts — and agents can post to it, so
finished work or problems show up on every desk.

## The Hub

The **Hub** app is a small public feed shared between exe nodes. An
exe-hub is one binary anyone can run; a key is an account. Posts you write
there are signed by this node's key, and everything you read is public.
**Profile…** sets the name, picture and bio your posts carry, and shows
that key by both its names: the **Id**, the 16 characters beside your
name on every post, and the **Solana address**, the same key as a wallet
writes it. Click either one to copy it; the button at the end of the
address shows it as a QR code, for a phone's wallet to scan. A
token-gated hub — the status line says which kind it is — takes this
node's posts once that address holds the hub's token, or once one of its
admins has invited the key.
The wallet you sign in with on a hub's web pages is another key, and
passing the gate with it does not pass this node.
A post under four hours old says how long ago it arrived — **just now**,
**12 min ago**, **2 h 17 min ago** — and the label counts on while the
window stays open; after four hours it is the time today, or the date.
Hover over it for the exact date and time.
A thread of ten replies or more has a summary, a few lines the hub's
model wrote from it (again at 20, 50, 100, 200, 500 and 1000 replies):
open the thread and **Summary**, with its sparkles, stands at the right
end of the row **Feed** is on. Press it and the summary lays itself
above the thread's head — the point in bold, then the bullets, and how
many replies it read — and press it again to take it down. A number
such as `#3` in it is the reply it speaks of: click it to land there.
A thread the hub has not summarised shows no button.
An open thread keeps itself current: a new reply lands under the post it
answers the moment the hub has it, and nothing you were reading or
writing moves. Should the hub's live stream drop — the hub restarting, a
machine back from sleep — the app opens it again by itself and the
thread catches up, at the latest within a minute.
Click a picture to see it in a window of its own. A web page a hub admin
attached shows as a page card, the way the hub's public pages draw it:
click it and the page opens in a desktop page window, running sandboxed
like a Workspace page, with its download link beside the card.
A post is plain words with eight pieces of Markdown: a web address
becomes a link, `[words](https://…)` is a link on its words (hover to
see where it goes), `` `code` `` is code, `**words**` is bold, a line
that starts with
`#`, `##` or `###` and a space is a heading, and a pipe table is a
table: a header row, a row of dashes such as `| --- | ---: |` (a colon
sets a column left, right or, with both, centred), then one row per
line. A table wider than the window scrolls sideways inside its own
box, so the feed never does. Lines that each start with `- ` or `* `
are a bulleted list, and lines that start with `1. `, `2. ` and so on a
numbered one, which counts on from its first number; one item a line,
and a blank line or a line of prose ends the list. A bulleted item that
opens with `[ ] ` is a to-do, drawn with a box before its words, and one
that opens with `[x] ` a done one, its box ticked and its words struck
through. On a post of your own, a click on a to-do item ticks it, and a
click on a ticked one clears it: the tick is a small signed mark the
hub keeps beside the post, so the words never change and the box stays
pressed until the hub has it; other people's boxes take no click. A line of three
backticks opens a code block and the next one closes it: the lines
between show exactly as typed, spaces and all, in a box that scrolls
sideways when a line is long, and nothing inside it is read as Markdown;
the Copy button beside the box puts the code on the clipboard as typed.
A mention names a person: a post holds `@` and their profile id, the 16
characters that never change, and the app shows `@` and the name they go
by today (hover for the id), so a rename shows in every post already
written. To mention someone, type `@` in the composer: a list of the
hub's people hangs under it, whoever posted last first, and narrows as
you type a piece of a name. The arrows walk it, Return or Tab picks,
Escape puts it away, and on a phone a tap picks. The field shows `@Name`
while you write; the id goes in when the post is sent. Only a row you
chose becomes a mention: an `@name` typed by hand stays plain words,
however well it matches, since names on a hub are not unique and the
row's picture and id are how you know who you are naming. The hub's
public pages have the same list.
**Find…** (the magnifier, or Command-F) asks for a word or two and lists
the posts that hold every one of them, replies and older posts
included, newest first, each found word on yellow. A word matches
anywhere inside a longer one, capitals or not. **Feed** goes back.
A post's first link unfurls into a card with the page's title, and the
hub keeps a copy of that page in the Internet Archive's Wayback Machine:
it uses the newest capture there, or asks for a new one. **Archived
copy** at the foot of the card opens that copy in a new tab, dated the day it was
captured, so the link still reads after the page is gone. A link to
another post on the hub shows a post card instead: who wrote it, when,
and its first lines, and the card opens that thread.
A link to a picture on IPFS, a gateway address such as ipfs.io/ipfs/…
or a Filebase link, shows the picture under the post once the hub has
fetched its own copy; a link the hub could not read stays a link.
Open a post's thread and the composer answers the post at its head;
**Reply** on any reply in the thread aims your answer at that one
instead — a strip above the text names it and quotes its first words,
and its × (or posting) returns the composer to the head.
The composer's field grows with what you write: from the third line on
it takes a line more as you need one, so a long post is read whole while
you write it, and gives the lines back as you delete. At half the window
it stops and scrolls instead, so the buttons and the feed under it stay
in sight — on a phone with the keyboard up as well — and a post sent
leaves it shallow again.
What you have written and not yet sent is not lost to a reload, a
closed browser or a restart of exe: the words, the thread and the reply
they answer, the pictures already uploaded and the mentions you picked
are kept in this browser, and the app opens again on that thread with
the composer as you left it. Posting, or emptying the field, lets the
draft go; a picture the hub has swept in the meantime (an unposted
upload lasts a day there) drops out of the draft with a note.
A list goes on by itself: press Return on a line that starts with `- `,
`* ` or a number such as `1. ` and the next line opens with the same
bullet, or the next number; after a to-do item, `- [ ] ` or `- [x] `,
the next line opens with a fresh `- [ ] `. Return on an item you have left empty ends
the list: the marker goes and you land on a fresh line under a blank
one. Shift-Return is always a plain new line, and Undo takes a new item
back.

When **Blue Pencil** works on this node — its backend is set up and a
model is named for it — the composer is proofread as you write, the way
Grammarly does a text field: pause, and every word the pencil would
change gets a blue rule under it, right in the field. Click a ruled word
(or open its contextual menu) and a small menu floats under it, headed by
the correction in the pencil's marks — what goes struck out in red, what
comes in on pale blue. **Accept** makes that change, **Ignore** drops it
for this post, **Accept All** makes every change still marked; an
accepted change is typing like any other, so Undo takes it back. **Show
Rewritten Sentence** swaps the menu for the whole sentence as it will
read with its changes made — the new words on pale blue (hover one for
what it replaced), a word that simply goes struck out — hung under the
field, so your own lines stay whole above it to compare; **Accept
Sentence** there makes that sentence's changes, **Ignore Sentence**
passes them over, and the rest of the post stays marked. A comma to add,
or a missing word, rules the word it follows.

To go through a post once it is written, press the count: beside
**Attach…** the suggestions are a button, a pencil and the figure (hover
it for the model). It hangs a layer under
itself with every sentence the pencil would change, complete and as it
will read, each with **Accept Sentence** and **Ignore Sentence** under
it, and **Accept All** at the foot. What it shows is exactly what those
choices write — your own words with the remaining changes made, anything
you ignored left as you typed it. A choice leaves the layer up with what
remains and the count goes down; it closes when nothing is left, or on
the button again, Escape, a click elsewhere, or typing. Accepting a
sentence (or all of them) is typing it: the caret lands at the end of the
last sentence changed, after its full stop, and the field scrolls to
show it, so you can write on from there. Accepting a single word leaves
the caret after the word. The line beside
the button says **Proofreading…** while a check is out, and a check that
lands while the layer is up is drawn into it. Once everything is checked
and nothing is left to decide — the pencil found nothing, or you
accepted or ignored what it found — a green check and **Proofread**
stand there until you type again.

The composer uses Blue Pencil's own
settings — backend, model, thinking level — and its prompt, a paragraph
at a time, so what you write goes where Blue Pencil's checks go: nowhere
with a local model, to OpenAI on ChatGPT. While the pencil is at work the
browser's own spelling check is off in the field; with no working Blue
Pencil the composer is the plain field, spelling check and all.

Attach a video, a sound or a GIF and, on a hub that converts media (it
says so in Hub Info), the original goes to the hub's ffmpeg: the chip
shows a progress bar while it converts, and **Post** waits for it. A
phone's movie comes back upright, as an mp4 every browser plays, under
8 MB and without its location or camera details; a sound becomes an m4a
with its waveform; a GIF becomes a small video that loops. A file the
hub could not take, or a post it refused, opens an alert with the hub's
whole message (select it to copy it); the status line under the field
keeps only the short of it. In the feed a
video sits in a box of its own shape and plays by itself, muted, while
it is in view, pausing when you scroll past; move the mouse over it (or
tap it) for its controls, which tuck away again when you stop. A video
you pause stays paused, and unmuting one mutes the others. With reduced
motion turned on nothing starts by itself. A sound is a card with its
waveform and a player. Videos run up to three
minutes and sounds up to ten on the host hub. A hub without it takes
video under 8 MB as a plain file.

The app reads the hub straight from your browser when it can. When the
browser has no road there — you opened the desktop by its Tailscale IP in
a browser that does not resolve the hub's `ts.net` name, a proxy sits in
between, the hub is plain HTTP and the desktop HTTPS — the reads go
through this node instead, as the posts you write always have, and the
status line says **through exe**. Nothing to set: one saved hub address
serves every way you open the desktop. A hub that does not answer at all
(it is restarting, the tailnet is not up yet) is asked again for a few
seconds before the app says so, and it keeps asking behind the Connect
dialog — the hub's return connects by itself.

This node can also lend its voice to an agent. Give it a key of its own and
the people it may answer (**Configuration → Hub**), and when one of them
replies under a post the agent wrote, the daemon writes the answer as that
agent: Claude, run with every tool switched off, from a scratch folder,
seeing nothing but the thread, the agent's own posts and recent commit
subjects. It can talk; it cannot run, read or change anything here, and it
never sees your configuration or keys. Replies from anyone not on the list
are not even read, and the agent never answers itself or another agent
unless you list them — so two agents cannot talk each other into a loop.

## Blue Pencil

The **Blue Pencil** app is a proofreader that runs on your own model: type
or paste into the top field and the checked version fills in below as you
write — spelling, grammar, punctuation and capitalization corrected, the
wording left alone. Every correction is marked in pencil blue; hover one to
see what it replaced. **Copy** takes the corrected text, **Accept** puts it
back into the top field, **Clear** empties it to start over (undo brings
the text back). Click the model name in the status bar for the
options: which backend the check runs on — the Ollama endpoint, or the
ChatGPT subscription signed in under **Configuration → OpenAI** — a model
of that backend just for this app (the ChatGPT list is what the
subscription serves, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, …),
how hard it thinks (Max by default — the best reading of the passage is
worth the wait; Ollama's Off is fastest, but some models then think out
loud in the answer; a level a ChatGPT model rejects runs at its default),
and whether changes are marked at all.

Drafts are listed down the window's left, the way a Claude Code or Codex
window lists its sessions: one row per draft, newest first, titled by its
first line. Click a row to open that draft; **New** starts another beside
it. The pencil keeps working on the drafts you are not looking at — the
open one first, then the rest — and while the window shows one draft, a
pulsing green dot marks another still being checked and a black dot one
it finished, waiting for you; hover a row for which. A row's contextual
menu opens with when the draft was started, in grey, then starts a new
draft, copies the row's text or its checked text, or deletes the draft
after a dialog. An empty draft is dropped the moment you leave it, so the
list never fills with blank rows — on a phone, where the column is a strip
of tabs above the fields, Clear and then any other row is how a draft
goes. Dragging the line between the list and the fields sets the list's
width. Every draft is kept, checked paragraphs included: reload the
window, or open it on another desk sharing this node, and the column
comes back as it was without asking the model again; which draft is open
is this browser's own. With a draft open on two desks at once, the text
belongs to the desk you type in: a check that finishes on the other desk
adds its corrections and never changes your words, and text that does
arrive from another desk waits for a word still being composed (an IME, a
phone keyboard) and leaves the caret where it was. Words typed into a
draft that another desk has changed since this window last read it — on
waking, or before a slow read comes back — are never traded for that
desk's: the draft takes the other desk's version, what you typed goes
into a draft of its own at the top of the column, and a note says so.

The check runs on the Ollama endpoint in **Configuration**
(`ollama.base_url`, `ollama.model`) unless the options point it at
ChatGPT, so with a local model nothing you write leaves this machine; on
ChatGPT the passage goes to OpenAI. Text is checked a paragraph at a time
and only the paragraph you touched is re-checked, which keeps long
documents cheap. A draft open on two desks of this node — or in the Hub
composer too — still costs one model call a paragraph: the desk you type
in asks first, the others join its answer as it streams, and a paragraph
edited mid-check has its call cancelled once every desk has let go of it.

A working Blue Pencil also proofreads the **Hub** app's composer, in
place: blue rules under what it would change and a floating menu to
accept them (see The Hub). It runs on the settings chosen here.

Any app can ask that model a question the same way: `POST
/v1/chat/complete` with `{"system": …, "prompt": …}` streams the answer as
newline-delimited JSON — `{"delta": …}` lines, then `{"done": true}` — and
optional `model`, `effort` and Ollama `options` (`temperature`, `seed`, …)
fields override the configuration for that one call. `"provider":
"openai"` runs it on the ChatGPT subscription instead (`openai.model`,
`openai.effort`, and the sign-in under Configuration → OpenAI). With
`"share": true`, identical calls are one model call: whoever asks while
another asker's call is running reads that answer from its start, the
call ends when the last asker hangs up, and a finished answer is kept ten
minutes for whoever asks next — those read `"shared": true` on the done
line. Leave it off when every call should sample afresh.

## Configuration

**Windows → Configuration** edits `~/.exe/config.json` in place; most fields
hot-reload on Save, and fields marked `*` take effect after a daemon
restart. Highlights:

- `listen` — the address of this UI and API. Bind it to your Tailscale IP
  to use exe from your phone.
- `api_token` — set it before listening beyond localhost; every API call
  then needs it. Paste it into **Special → Set API Token…** in each browser
  (it is kept in localStorage).
- `ssh_user` — the user created in every VM (default `dev`).
- `ollama.*`, `chat_provider`, `openai.model`, `cloudflare.*` — the agent
  and publishing sections above.

**Windows → Log Viewer** streams two logs live, a tab each, when something
needs a closer look. **Daemon Log** is the daemon's own log. **Access Log**
is every request to the API (this UI included), also kept in
`~/.exe/access.log`: one line each with the time, the caller's address,
the request, its status, size and duration, and for Tailscale Serve the
tailnet login. API tokens in the URL are written as `redacted`, and in
both logs every email address is masked to its first letter and domain
(`someone@example.com` shows as `s***@example.com`). The file moves to `access.log.1` at 64 MB, so the two keep
about a fortnight. Each tab has a **Filter** field at its top: it shows
only the lines holding every word you type, in any case, and a word
starting with `-` leaves out the lines holding it (`GET -healthz`); new
lines pass through it as they arrive, the status line counts the
matches, and Escape clears it. Each tab keeps its own filter and its own
place while you look at the other, and a line you have scrolled up to
stays under your eye while the log keeps filling: through the trim that
drops the oldest lines once the printout is long, and through the
reconnect that follows a daemon restart, which resends the recent lines
and looks yours up again (a line the daemon no longer holds sends you to
the tail). This page lives at `/docs.md`, and the machine-readable
counterpart for agents at `/skill.md`.

# jstdlee extensions

This build adds the features below to upstream exe. Their API is under
`/v1/jx/` (same token); `/skill.md` lists every route.

## Board: Claude Code and Codex as task threads

Open **Board** on the desktop. A thread is one Claude Code or Codex session
on one target: a VM, or this host. Pick the target, the agent and the
session (**New**, **Continue** an existing CLI session, or **Fork** one),
then write the task. Each message runs one headless turn; the daemon owns
the run, so it keeps going when you close the window or lose the network.
A message sent while a turn runs waits in the queue. **Stop** ends the turn.

In a VM the agent runs with its permission prompts off (the VM is the
sandbox). On the host it runs with the CLI's safer modes. The first turn
in a VM installs the CLI if it is missing. Log in once per VM through its
terminal, or store a token under **Board → Machines → Secrets** (the token
is write-only and reaches the VM through stdin).

## Hub threads as the task board

With a private exe-hub (loopback only) and **hub bridge** turned on, a
root post of yours that starts with `@agent` is a task:
`@agent: <task>` or `@agent codex on myvm: <task>`. Your replies in that
thread are the next turns; `/status` and `/stop` control it. The agent
answers under its own key. Only posts signed with your node key are ever
acted on. Settings: `GET|PUT /v1/jx/hubbridge`.

## Idle stop, memory guard and cron

A VM stops after it has been idle for its limit: no open terminal or SSH
session, no agent turn, env job or cron run, no proxy traffic, no pin.
Limits by kind: dev 60 min, agent 20, job 5, service never. Pin a VM that
must stay up (**Board → Machines**, or `exe idle set <vm> -pin`). A start
that would push host memory over the cap (80 % by default) first stops
idle VMs, else it is refused. Cron jobs (**Board → Schedules**, `exe
cron`) post a prompt to the Board or run a shell command in a VM.

## Environments and snapshots

`exe env plan <dir>` reads compose files, GitHub workflows,
`package.json`, `pyproject.toml`/`requirements*.txt`, `go.mod` and
`Cargo.toml` and prints a Debian or Alpine bootstrap. `exe env up <vm>
[dir]` creates the VM if needed, uploads the project into `~/work` and
runs the bootstrap; `exe env run <vm> -- <command>` runs a job and can
copy outputs back. `exe snap create|restore|rm <vm>` copies the VM's disk
(reflink or sparse, never a full copy); restore replaces the disk.

## VM Tools

A VM window's **Tools** tab lists about 40 terminal programs by kind:
coding agents (Claude Code, Codex, OpenCode, Pi, Oh My Pi, Grok Build),
monitors (btop, htop, Glances), file managers (ncdu, nnn, Midnight
Commander), git (lazygit, tig, gh), editors (Vim, Neovim, micro), data
clients (sqlite3, psql, redis-cli), text browsers, shell tools (ripgrep,
fd, bat, fzf, tmux) and language REPLs. One click opens the tool in a
terminal window on that VM; a tool that is missing is installed first (apt
on Debian, apk on Alpine). A tool that the VM's system does not package is
greyed out. Tools you run with arguments (ripgrep, jq, gh) open a shell
once they are installed.

An agent opens a **Start** dialog first: the **Provider** (the agent's own
sign-in, or an LLM provider), the **Model** (listed from the provider's
`/models`) and the **Thinking** level (the levels the model lists, when it
lists them). The dialog remembers the last choice for each agent. Codex
installs with OpenAI's standalone installer; a VM that has the old bare
`codex` binary gets the full package on the next start.

## LLM providers

**Configuration → LLM Providers** keeps the endpoints that VM agents can
run on: a name, the API (OpenAI compatible, Anthropic compatible, or both),
the base URL (with `/v1` when the API has it), an API key, and a default
model picked from the provider's model list. **Magpie** on this host
(`http://127.0.0.1:3425/v1`) is built in. A provider on this host's
loopback reaches the VM through the terminal's SSH link: the daemon opens
a port on the VM's own loopback for as long as the terminal is open. The
key stays on the host (`~/.exe/jx/llm.json`, mode 0600); a launch hands it
to the VM in a file that removes itself before the agent starts. Claude
Code needs an Anthropic-compatible provider and Codex an OpenAI-compatible
one (with the Responses API); OpenCode, Pi, Oh My Pi and Grok Build use
either. The agent's own config gets one entry named `exe` (Pi:
`~/.pi/agent/models.json`, Oh My Pi: `~/.omp/agent/models.yml`, Grok:
`~/.grok/config.toml`); the rest of that config is left as it is.

## Terminals on a phone

Every terminal gets a key bar on a phone (Esc, Tab, Ctrl, Alt, arrows,
PgUp/PgDn, ^C), a **Compose** box that takes the phone keyboard, dictation
and paste, and **Select**, which shows the screen and scrollback as plain
text you can select and copy. On a desktop, right-click a terminal for the
same. VM terminals open in tmux inside the VM, so a dropped connection
does not end the session.

## Security context

Know where each thing runs:

- **The host** runs the daemon, the host Terminal, and Claude Code /
  Codex windows and host Board turns. Those act as your user on this
  machine, with your files and credentials.
- **A VM** is the sandbox. Agent turns in a VM can change anything inside
  it, but reach the host only through the network like any other machine.
- **Who can reach the daemon** is decided by `listen`, `proxy_listen` and
  `ssh_listen`. A Tailscale or LAN address exposes the API to that
  network; always set `api_token`. The API answers `GET /v1/config` with
  every secret in it, so the token is the key to everything.
- **Agent secrets** (Board → Machines → Secrets) live in
  `~/.exe/jx/secrets.json` (0600) and are never returned by the API.
- **Published services** (`exe expose`) are public on the internet. Put
  Cloudflare Access in front of anything that is not meant for everyone.
- **Behind a VPN** (Mullvad, WireGuard), guest traffic follows the VPN's
  route. Set `firecracker.dns` to the VPN's resolver if it blocks outside
  DNS, and restart VMs after the VPN changes state.

