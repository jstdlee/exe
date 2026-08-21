"use strict";
const $ = s => document.querySelector(s);
// Phones get the one-app-fullscreen mobile mode: no drag, grow or shade,
// one window at a time, single-tap opens, and no participation in the
// shared desktop layout snapshot. Zoom toggles a centered restored size.
// Tablets and desktops keep the spatial windows. The UA test catches phone
// browsers; the media test catches other small coarse-pointer screens;
// ?mobile=1 / ?mobile=0 forces it.
function detectMobile() {
  const q = new URLSearchParams(location.search);
  if (q.has("mobile")) return q.get("mobile") !== "0";
  if (/iPhone|iPod|Android.*Mobile|Windows Phone/i.test(navigator.userAgent)) return true;
  if (matchMedia("(pointer: coarse)").matches && Math.min(screen.width, screen.height) < 700) return true;
  // Chrome desktop resize / DevTools device mode — follow the viewport
  return window.innerWidth < 720;
}
let IS_MOBILE = detectMobile();
if (IS_MOBILE) document.body.classList.add("mobile");
// iOS Safari auto-zooms into focused inputs whose font is under 16px;
// maximum-scale=1 disables only that auto-zoom (pinch zoom still works
// since iOS 10), keeping the 12px Platinum type. Scoped to iOS — the same
// clamp would take away Android's pinch zoom entirely.
if (IS_MOBILE && /iPhone|iPod|iPad/i.test(navigator.userAgent))
  document.querySelector('meta[name="viewport"]').content = "width=device-width, initial-scale=1, maximum-scale=1";
// objects open on double-click everywhere; single-click only selects.
const objectOpen = (n, fn) => n.addEventListener("dblclick", fn);
function addLongPressMenu(n, menuFn) {
  if (!IS_MOBILE) return;
  let touchStart = null, longTimer = null;
  const clear = () => { touchStart = null; if (longTimer) { clearTimeout(longTimer); longTimer = null; } };
  n.addEventListener("touchstart", e => {
    if (e.touches.length !== 1) return;
    touchStart = e.touches[0];
    longTimer = setTimeout(() => {
      const t = touchStart; if (!t) return;
      menuFn(t.clientX, t.clientY);
      clear();
    }, 650);
  }, { passive: true });
  n.addEventListener("touchend", clear, { passive: true });
  n.addEventListener("touchmove", clear, { passive: true });
}
function el(tag, attrs = {}, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const kid of kids) n.append(kid);
  return n;
}
function toast(msg) {
  const b = $("#status-bar");
  b.textContent = msg;
  b.classList.add("show");
  clearTimeout(b._t);
  b._t = setTimeout(() => b.classList.remove("show"), 3500);
}
const fmtTime = iso => new Date(iso).toLocaleString();

// Platinum confirm / note / paste — never window.confirm
let askWait = null;
function platAsk(message, opts) {
  opts = opts || {};
  return new Promise(resolve => {
    if (askWait) { const prev = askWait; askWait = null; prev(false); }
    askWait = resolve;
    $("#ask-title").textContent = opts.title || (opts.note ? "Note" : opts.paste ? "Paste" : "Confirm");
    $("#ask-msg").textContent = message;
    $("#ask-ok").textContent = opts.ok || "OK";
    $("#ask-cancel").hidden = !!opts.note;
    const ta = $("#ask-paste");
    ta.hidden = !opts.paste;
    ta.value = "";
    $("#ask-overlay").hidden = false;
    setTimeout(() => (opts.paste ? ta : $("#ask-ok")).focus(), 30);
  });
}
function askFinish(ok) {
  $("#ask-overlay").hidden = true;
  const r = askWait; askWait = null;
  if (!r) return;
  if (ok && !$("#ask-paste").hidden) r($("#ask-paste").value);
  else r(!!ok);
}
$("#ask-ok").onclick = () => askFinish(true);
$("#ask-cancel").onclick = () => askFinish(false);
$("#ask-x").onclick = () => askFinish(false);

// ---- api ----
const tokenInput = $("#token");
tokenInput.value = localStorage.getItem("exe_token") || "";
tokenInput.addEventListener("change", () => localStorage.setItem("exe_token", tokenInput.value));
let tokenPromptOpen = false;
async function api(path, opts = {}) {
  opts.headers = Object.assign({}, opts.headers);
  if (tokenInput.value) opts.headers["Authorization"] = "Bearer " + tokenInput.value;
  opts.headers["X-Exe-Client"] = WIN_CLIENT; // lets change events skip our own echo
  if (opts.json !== undefined) {
    opts.body = JSON.stringify(opts.json);
    opts.headers["Content-Type"] = "application/json";
    delete opts.json;
  }
  const r = await fetch(path, opts);
  if (r.status === 401) {
    if (!tokenPromptOpen) {
      tokenPromptOpen = true;
      $("#token-status").textContent = "The daemon requires Authorization: Bearer <token>. Enter the api_token from config.json or EXE_API_TOKEN.";
      openWin("#win-token");
      tokenInput.focus();
    }
  }
  if (!r.ok) {
    let m = "HTTP " + r.status;
    try { const j = await r.json(); if (j.error) m = j.error; } catch (e) {}
    throw new Error(m);
  }
  return r;
}
const j = (p, o) => api(p, o).then(r => r.json());

// ---- window manager ----
let zTop = 10;
function focusWin(w) {
  zTop++;
  w.style.zIndex = zTop;
  // modals keep their hardcoded .front — only desktop windows trade focus
  document.querySelectorAll("#desktop .window").forEach(x => x.classList.toggle("front", x === w));
  winSave();
}
// mobile: one fullscreen window at a time. Opening a window hides the one
// on screen and remembers it, so closing walks back through the stack like
// a phone's back button; closing the last one lands on the desktop icons.
let mobileStack = [];
function updateMobileMinimizedBars() {
  const mins = [...document.querySelectorAll("#desktop .window.minimized")]
    .filter(w => !w.hidden && !MOB_DIALOG.has(w.id))
    .sort((a, b) => (+a.style.zIndex || 0) - (+b.style.zIndex || 0));
  mins.forEach((w, i) => w.style.setProperty("--mobile-min-bottom", (i * 20) + "px"));
  document.body.style.setProperty("--mobile-minimized-stack", IS_MOBILE ? (mins.length * 20) + "px" : "0px");
}
// dialog-ish windows float centered over the current window like the
// Cloudflare status modal instead of going fullscreen; they never enter
// the back-stack and just vanish when the user switches windows
const MOB_DIALOG = new Set(["win-about"]);
function mobileSwapIn(w) {
  mobileStack = mobileStack.filter(id => id !== w.id);
  w.classList.remove("mob-restored");
  document.querySelectorAll("#desktop .window").forEach(x => {
    if (x !== w && !x.hidden && !x.classList.contains("minimized")) {
      x.hidden = true;
      if (!MOB_DIALOG.has(x.id)) mobileStack.push(x.id);
    }
  });
}
// Mobile UI state survives reloads via its own localStorage key — phone-
// local only, never mixed into the shared desktop layout snapshot. Live
// terminals and transient dialogs don't restore; win-detail needs the VM
// name, carried alongside.
const MOB_RESTORE = new Set(["win-vms", "win-config", "win-log", "win-chat", "win-news", "win-trash", "win-detail", "win-docs"]);
const mobRestorable = id => MOB_RESTORE.has(id)
  || id.startsWith("win-app-") || id.startsWith("win-ws-") || id.startsWith("win-ed-") || id.startsWith("win-iv-");
function mobSave() {
  const vis = [...document.querySelectorAll("#desktop .window")].find(w => !w.hidden && !w.classList.contains("minimized") && !MOB_DIALOG.has(w.id));
  localStorage.setItem("exe_mobile_winstate", JSON.stringify({
    stack: mobileStack.filter(mobRestorable),
    top: vis && mobRestorable(vis.id) ? vis.id : null,
    vm: currentVM,
  }));
}
// reopen the remembered stack bottom-up, then the visible window — each
// open re-stacks the one before it, rebuilding mobileStack as it goes
function mobRestore() {
  let st = null;
  try { st = JSON.parse(localStorage.getItem("exe_mobile_winstate") || "null"); } catch (e) {}
  if (!st) { openWin("#win-vms"); return; } // first visit — no state yet
  // #win-vms ships visible in the markup; hide it before the replay so a
  // state where the user closed it stays closed, and it can't sneak into
  // the rebuilt stack under whatever window opens on top of it
  $("#win-vms").hidden = true;
  for (const id of [...(st.stack || []), st.top].filter(Boolean).filter(mobRestorable)) {
    if (id === "win-detail") { if (st.vm) openVM(st.vm); }
    else if (id.startsWith("win-app-")) openAppWin(id.slice(8));
    else if (id.startsWith("win-ws-")) openFinderWin(decodeURIComponent(id.slice(7)));
    else if (id.startsWith("win-ed-")) openEditorWin(decodeURIComponent(id.slice(7)));
    else if (id.startsWith("win-iv-")) openImageWin(decodeURIComponent(id.slice(7)));
    else (WIN_OPENERS[id] || (() => openWin("#" + id)))();
  }
}
function openWin(sel) {
  const w = typeof sel === "string" ? $(sel) : sel;
  if (!w) return null;
  w.classList.remove("minimized");
  if (IS_MOBILE && !MOB_DIALOG.has(w.id)) mobileSwapIn(w);
  w.hidden = false;
  w.classList.remove("shaded");
  focusWin(w);
  updateMobileMinimizedBars();
  if (IS_MOBILE) mobSave();
  return w;
}
function closeWin(w) {
  if (!w) return;
  const wasFront = w.classList.contains("front");
  w.hidden = true;
  w.classList.remove("minimized");
  if (w.id === "win-token") tokenPromptOpen = false;
  if (w.id === "win-detail") { closeTerm(); flushNotes(); currentVM = null; }
  if (w.id === "win-newvm") $("#c-status").textContent = "";
  if (w.id === "win-upload") {
    $("#up-status").textContent = "";
    $("#up-file").value = "";
    $("#up-name").textContent = "no file selected";
  }
  if (w.id === "win-log") stopLogStream();
  // a closed Terminal is gone for good: end the shell and drop the window
  if (w.classList.contains("term-window")) { w._teardown(); w.remove(); }
  if (IS_MOBILE) {
    if (!MOB_DIALOG.has(w.id)) {
      const prev = document.getElementById(mobileStack.pop() || "");
      if (prev) { prev.hidden = false; focusWin(prev); }
    }
    mobSave();
  } else if (wasFront) {
    // OS 9: closing the active window activates the one revealed beneath
    // it — z order doubles as activation recency since zTop is monotonic
    const wins = [...document.querySelectorAll("#desktop .window")].filter(x => !x.hidden && !x.classList.contains("minimized"));
    wins.sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0));
    if (wins[0]) focusWin(wins[0]);
  }
  updateMobileMinimizedBars();
  winSave();
}
function minimizeWin(w) {
  if (!w || w.hidden || w.classList.contains("modal")) return;
  const wasFront = w.classList.contains("front");
  w.classList.add("minimized");
  w.classList.remove("front");
  if (IS_MOBILE) {
    const prev = document.getElementById(mobileStack.pop() || "");
    if (prev) { prev.hidden = false; focusWin(prev); }
    mobSave();
  } else if (wasFront) {
    const wins = [...document.querySelectorAll("#desktop .window")].filter(x => !x.hidden && !x.classList.contains("minimized"));
    wins.sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0));
    if (wins[0]) focusWin(wins[0]);
  }
  updateMobileMinimizedBars();
  winSave();
}
function closeFrontWindow() {
  const wins = [...document.querySelectorAll("#desktop .window")].filter(w => !w.hidden && !w.classList.contains("minimized"));
  wins.sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0));
  if (wins[0]) closeWin(wins[0]);
}
function captureWindowFrameHeights(w) {
  return [...w.querySelectorAll(".app-frame, .workspace-tree, .ed-text, .iv-box, .chat-wrap, #log-out, #news-list, #docs-body, .win-body")]
    .map(el => ({ el, height: el.style.height }));
}
function restoreWindowFrameHeights(w, frameHeights) {
  for (const h of frameHeights || []) {
    if (h.el && w.contains(h.el)) h.el.style.height = h.height || "";
  }
}
function clearWindowFrameHeights(frameHeights) {
  for (const h of frameHeights || []) {
    if (h.el) h.el.style.height = "";
  }
}
function initWindow(w) {
  w.addEventListener("mousedown", () => { if (!w.closest(".overlay")) focusWin(w); });
  const tb = w.querySelector(".titlebar");
  const cb = tb.querySelector(".tbox.close");
  if (cb && !cb.id) {
    // ensure the X mark is present
    if (!cb.querySelector("i")) cb.append(el("i"));
    cb.addEventListener("click", e => { e.stopPropagation(); closeWin(w); });
  }
  const minb = tb.querySelector(".tbox.min");
  if (minb) minb.addEventListener("click", e => { e.stopPropagation(); minimizeWin(w); });
  const sb = tb.querySelector(".tbox.shade");
  if (sb) sb.addEventListener("click", e => { e.stopPropagation(); w.classList.toggle("shaded"); termResize(); winSave(); });
  const maxb = tb.querySelector(".tbox.max");
  if (maxb) {
    const setMaxIcon = () => maxb.classList.toggle("restore", w.classList.contains("maximized") || w.classList.contains("mob-restored"));
    maxb.addEventListener("click", e => {
      e.stopPropagation();
      if (IS_MOBILE && w.classList.contains("minimized")) { openWin(w); return; }
      const wasMax = w.classList.contains("maximized") || w.classList.contains("mob-restored");
      w.classList.remove("maximized");
      w.classList.remove("mob-restored");
      if (wasMax) {
        if (w._max) {
          ["left","top","width","height"].forEach(k => { w.style[k] = w._max.style[k]; });
          restoreWindowFrameHeights(w, w._max.frameHeights);
          w._max = null;
        }
      } else {
        // Save geometry before maximizing. Inner bodies are the resizable
        // surface for many window types, so keep their heights too.
        w._max = { style: {
          left: w.style.left || (w.offsetLeft + "px"),
          top: w.style.top || (w.offsetTop + "px"),
          width: w.style.width || (w.offsetWidth + "px"),
          height: w.style.height || ""
        }, frameHeights: captureWindowFrameHeights(w) };
        clearWindowFrameHeights(w._max.frameHeights);
        if (IS_MOBILE) {
          w.classList.add("mob-restored");
        } else {
          w.classList.add("maximized");
        }
      }
      setMaxIcon();
      focusWin(w); termResize(); if (w._termResize) w._termResize();
    });
    setMaxIcon();
  }
  tb.addEventListener("click", e => {
    if (e.target.closest(".tbox")) return;
    if (IS_MOBILE && w.classList.contains("minimized")) { openWin(w); return; }
  });
  tb.addEventListener("dblclick", e => {
    if (IS_MOBILE || e.target.closest(".tbox") || w.classList.contains("modal")) return;
    w.classList.toggle("shaded");
    termResize();
    winSave();
  });
  // all non-modal windows can be dragged by the titlebar; on mobile we still
  // allow dragging the restored/zoomed window so it doesn't feel pinned.
  if (w.classList.contains("modal")) return;
  tb.addEventListener("pointerdown", e => {
    if (e.target.closest(".tbox")) return;
    e.preventDefault();
    focusWin(w);
    if (w.classList.contains("maximized") || (IS_MOBILE && !w.classList.contains("mob-restored"))) return;
    // capture the drag's pointer stream on the title bar — otherwise the
    // pointer escapes into an app window's iframe and the drag freezes
    try { tb.setPointerCapture(e.pointerId); } catch (err) {}
    // grab offset in #desktop coordinates (offsetLeft/Top) — client rects are
    // viewport-based, and the desktop origin sits 20px lower (menu bar), which
    // made windows jump down by that much on the first move
    const ox = e.clientX - w.offsetLeft, oy = e.clientY - w.offsetTop;
    const width = w.offsetWidth;
    winDragging = w;
    document.body.classList.add("win-drag");
    const move = ev => {
      if (IS_MOBILE) {
        // In mobile mode the window is already clamped to viewport; allow a
        // small drag nudge but keep it fully on screen.
        w.style.left = Math.min(window.innerWidth - Math.min(width, window.innerWidth), Math.max(0, ev.clientX - ox)) + "px";
        w.style.top = Math.min(window.innerHeight - 60, Math.max(20, ev.clientY - oy)) + "px";
      } else {
        w.style.left = Math.min(window.innerWidth - 60, Math.max(60 - width, ev.clientX - ox)) + "px";
        w.style.top = Math.min(window.innerHeight - 46, Math.max(0, ev.clientY - oy)) + "px";
      }
      winSaveLive();
    };
    const up = () => {
      winDragging = null;
      document.body.classList.remove("win-drag");
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      winSave();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  });
}
document.querySelectorAll(".window").forEach(initWindow);

// invisible edge grips on resizable windows: dragging the left or right
// edge resizes width (the left edge keeps the right side anchored), the
// bottom edge resizes the body height. Same clamps and live-sync as the
// grow corner; the .win-edge CSS keeps them hidden unless the window is
// .grow and unshaded.
function wireEdges(w, body, minW, opts = {}) {
  if (IS_MOBILE) return;
  w._edge = { body, minW, opts };
  if (w.querySelector(":scope > .win-edge")) return;
  for (const dir of ["l", "r", "b"]) {
    const grip = el("div", { class: "win-edge edge-" + dir });
    grip.addEventListener("pointerdown", e => {
      if (w.classList.contains("shaded") || w.classList.contains("maximized")) return;
      e.preventDefault(); e.stopPropagation();
      focusWin(w);
      // capture the drag's pointer stream — it crosses iframes mid-drag
      try { grip.setPointerCapture(e.pointerId); } catch (err) {}
      const { body, minW, opts } = w._edge;
      const sw = w.offsetWidth, sl = w.offsetLeft, sh = body.offsetHeight;
      const sx = e.clientX, sy = e.clientY;
      const st = opts.stickEl;
      const stick = st && st.scrollHeight - st.scrollTop - st.clientHeight < 4;
      winDragging = w;
      const move = ev => {
        if (dir === "b") {
          body.style.height = Math.max(opts.minH || 96, Math.min(window.innerHeight - (opts.hPad || 170), sh + ev.clientY - sy)) + "px";
          if (stick) st.scrollTop = st.scrollHeight;
        } else {
          const dx = dir === "r" ? ev.clientX - sx : sx - ev.clientX;
          const nw = Math.max(minW, Math.min(window.innerWidth - 40, sw + dx));
          w.style.width = nw + "px";
          if (dir === "l") w.style.left = (sl + sw - nw) + "px";
        }
        if (opts.onMove) opts.onMove();
        winSaveLive();
      };
      const up = () => {
        winDragging = null;
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", up);
        winSave();
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", up);
    });
    w.append(grip);
  }
}
// shared grow-box drag: resizes the window's width and one body element's
// height, live-syncing the layout snapshot. stickEl keeps a scroller pinned
// to its end during the resize (log tail, chat transcript).
function wireGrow(grip, w, body, minW, opts = {}) {
  if (IS_MOBILE) return; // no resize in mobile mode — the grow box is hidden
  wireEdges(w, body, minW, opts); // window edges get the one-axis drags
  grip.addEventListener("pointerdown", e => {
    if (w.classList.contains("maximized")) return;
    e.preventDefault(); e.stopPropagation();
    focusWin(w);
    const sw = w.offsetWidth, sh = body.offsetHeight, sx = e.clientX, sy = e.clientY;
    const st = opts.stickEl;
    const stick = st && st.scrollHeight - st.scrollTop - st.clientHeight < 4;
    winDragging = w;
    const move = ev => {
      w.style.width = Math.max(minW, Math.min(window.innerWidth - 40, sw + ev.clientX - sx)) + "px";
      body.style.height = Math.max(opts.minH || 96, Math.min(window.innerHeight - 170, sh + ev.clientY - sy)) + "px";
      if (stick) st.scrollTop = st.scrollHeight;
      if (opts.onMove) opts.onMove();
      winSaveLive();
    };
    const up = () => {
      winDragging = null;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      winSave();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  });
}
wireGrow($("#chat-grow"), $("#win-chat"), $(".chat-wrap"), 560, { minH: 240, stickEl: $("#chat-msgs") });
wireGrow($("#news-grow"), $("#win-news"), $("#news-list"), 320, { minH: 160 });
wireGrow($("#docs-grow"), $("#win-docs"), $("#docs-body"), 420, { minH: 160 });
// Virtual Machines has no grow corner (its statusbar is full of buttons),
// but the edges resize it: width, and the list body's height
wireEdges($("#win-vms"), $("#win-vms .win-body"), 600, { minH: 120 });
// Configuration likewise: no grow corner, edges only. The body scrolls
// once its height is pinned below the form's natural size.
wireEdges($("#win-config"), $("#win-config .win-body"), 560, { minH: 200 });
// VM detail likewise; a live terminal refits to the new width as it drags.
// The 360px height floor keeps every tab's fixed rows + the 280px panel
// minimum inside the body, so the Terminal tab's clipped body never hides
// content — the shrinking terminal means a 360px body has no dead space.
wireEdges($("#win-detail"), $("#win-detail .win-body"), 600, { minH: 360, onMove: termResize });

// overflow-y:auto scrollers whose frame line must stay single: the border
// yields to the scrollbar's own black edge whenever the bar is present
function autoVBar(el) {
  const upd = () => el.classList.toggle("has-vbar", el.scrollHeight > el.clientHeight);
  new ResizeObserver(upd).observe(el);
  new MutationObserver(upd).observe(el, { childList: true, subtree: true, characterData: true });
  upd();
}
autoVBar($("#chat-msgs"));
autoVBar($("#chat-sessions"));

// ---- window layout: persist + cross-browser sync ----
// The whole desktop layout (per-window geometry, stacking, shade, and which
// document windows are open) is one JSON snapshot of the DOM. It is cached
// in localStorage for instant restore, PUT to the daemon, and fanned out to
// every other connected browser over /v1/ui/events (SSE) — so dragging a
// window here moves it there. Snapshots are last-write-wins; because each
// save re-reads the DOM, concurrent editors converge.
const WIN_CLIENT = Math.random().toString(36).slice(2) + Date.now().toString(36);
// windows whose open/closed state is restored and synced; win-detail needs a
// current VM and the dialogs are transient, so those only keep geometry
const WIN_SYNC_OPEN = new Set(["win-vms", "win-config", "win-log", "win-chat", "win-news", "win-trash", "win-docs"]);
const WIN_OPENERS = { "win-log": () => openLogWin(), "win-chat": () => openChatWin(), "win-news": () => openNewsWin(), "win-config": () => openConfigWin(), "win-docs": () => openDocsWin() };
let winApplying = false; // applying a stored/remote snapshot — don't echo it back
let winDragging = null;  // window under the local pointer — remote moves lose
let winRev = 0;

function winSnapshot() {
  const st = {};
  document.querySelectorAll("#desktop .window").forEach(w => {
    if (w.id.startsWith("win-app-") && isHiddenDesktopApp(w.id.slice(8))) return;
    // style.*, not offset*: hidden windows are display:none, which reads 0
    st[w.id] = {
      l: parseInt(w.style.left) || 0, t: parseInt(w.style.top) || 0,
      w: parseInt(w.style.width) || w.offsetWidth,
      z: +w.style.zIndex || 0,
      open: !w.hidden, shaded: w.classList.contains("shaded"), minimized: w.classList.contains("minimized"),
    };
    // app, Finder, editor, image, chat and terminal windows are also resizable
    const g = w.querySelector(".app-frame, .workspace-tree, .ed-text, .iv-box, .chat-wrap");
    if (g) st[w.id].h = parseInt(g.style.height) || g.offsetHeight || 0;
  });
  st["win-log"].h = parseInt($("#log-out").style.height) || $("#log-out").offsetHeight || 380;
  st["win-news"].h = parseInt($("#news-list").style.height) || $("#news-list").offsetHeight || 380;
  st["win-docs"].h = parseInt($("#docs-body").style.height) || $("#docs-body").offsetHeight || 420;
  // VM list height only once edge-resized — otherwise it sizes to its rows
  const vb = $("#win-vms .win-body");
  if (vb.style.height) st["win-vms"].h = parseInt(vb.style.height);
  // Configuration height likewise only once edge-resized
  const cb = $("#win-config .win-body");
  if (cb.style.height) st["win-config"].h = parseInt(cb.style.height);
  // VM detail height likewise only once edge-resized
  const db = $("#win-detail .win-body");
  if (db.style.height) st["win-detail"].h = parseInt(db.style.height);
  return st;
}
function applyWinState(st) {
  if (!st || IS_MOBILE) return; // snapshots describe the spatial desktop, not the phone view
  winApplying = true;
  try {
    // pass 1: open/close (openers bump z and .front; geometry fixes that after)
    for (const [id, v] of Object.entries(st)) {
      const w = document.getElementById(id);
      if (id.startsWith("win-app-")) {
        if (isHiddenDesktopApp(id.slice(8))) {
          if (w) w.remove();
          continue;
        }
        // app windows are created on demand, so this browser may not have
        // the element yet — openAppWin builds it before pass 2 places it
        if (v.open && !w) openAppWin(id.slice(8));
        else if (w && v.open && w.hidden) openWin(w);
        else if (w && !v.open && !w.hidden) closeWin(w);
        continue;
      }
      if (id.startsWith("win-ws-") || id.startsWith("win-ed-") || id.startsWith("win-iv-")) {
        // Finder folder / editor / image windows: same on-demand pattern as
        // app windows, with the path recovered from the id
        const open = id.startsWith("win-ws-") ? openFinderWin
          : id.startsWith("win-ed-") ? openEditorWin : openImageWin;
        if (v.open && !w) open(decodeURIComponent(id.slice(7)));
        else if (w && v.open && w.hidden) openWin(w);
        else if (w && !v.open && !w.hidden) closeWin(w);
        continue;
      }
      if (!w || !WIN_SYNC_OPEN.has(id)) continue;
      if (v.open && w.hidden) (WIN_OPENERS[id] || (() => openWin(w)))();
      else if (!v.open && !w.hidden) closeWin(w);
    }
    // pass 2: geometry + stacking, clamped so title bars stay reachable
    let maxZ = 0;
    for (const [id, v] of Object.entries(st)) {
      const w = document.getElementById(id);
      if (!w || w === winDragging) continue;
      const width = v.w || w.offsetWidth || 220;
      // floor at the window's wired minimum: a snapshot saved by a narrow
      // viewport (the innerWidth clamp below persists via winSave) must not
      // pin a resizable window below the width its own drags enforce
      if (v.w) w.style.width = Math.max(w._edge ? w._edge.minW : 0, Math.min(window.innerWidth - 24, v.w)) + "px";
      if (v.l !== undefined) w.style.left = Math.min(window.innerWidth - 60, Math.max(60 - width, v.l)) + "px";
      if (v.t !== undefined) w.style.top = Math.min(window.innerHeight - 46, Math.max(0, v.t)) + "px";
      if (v.z) { w.style.zIndex = v.z; maxZ = Math.max(maxZ, v.z); }
      w.classList.toggle("shaded", !!v.shaded);
      w.classList.toggle("minimized", !!v.minimized);
      if (id === "win-log" && v.h) $("#log-out").style.height = Math.max(96, Math.min(window.innerHeight - 170, v.h)) + "px";
      if (id === "win-news" && v.h) $("#news-list").style.height = Math.max(96, Math.min(window.innerHeight - 170, v.h)) + "px";
      if (id === "win-docs" && v.h) $("#docs-body").style.height = Math.max(96, Math.min(window.innerHeight - 170, v.h)) + "px";
      // vms/config/detail carry h only once edge-resized — an absent h means
      // natural content height, so clear any leftover pin instead of keeping it
      if (id === "win-vms") $("#win-vms .win-body").style.height = v.h ? Math.max(120, Math.min(window.innerHeight - 170, v.h)) + "px" : "";
      if (id === "win-config") $("#win-config .win-body").style.height = v.h ? Math.max(200, Math.min(window.innerHeight - 170, v.h)) + "px" : "";
      if (id === "win-detail") $("#win-detail .win-body").style.height = v.h ? Math.max(360, Math.min(window.innerHeight - 170, v.h)) + "px" : "";
      if (id === "win-chat" && v.h) $(".chat-wrap").style.height = Math.max(240, Math.min(window.innerHeight - 190, v.h)) + "px";
      if ((id.startsWith("win-ws-") || id.startsWith("win-ed-") || id.startsWith("win-iv-")) && v.h) {
        const g = w.querySelector(".workspace-tree, .ed-text, .iv-box");
        if (g) g.style.height = Math.max(96, Math.min(window.innerHeight - 170, v.h)) + "px";
      }
      if (id.startsWith("win-app-") && v.h) {
        const fr = w.querySelector(".app-frame");
        if (fr) fr.style.height = Math.max(120, Math.min(window.innerHeight - 120, v.h)) + "px";
      }
    }
    zTop = Math.max(zTop, maxZ);
    const wins = [...document.querySelectorAll("#desktop .window")].filter(x => !x.hidden && !x.classList.contains("minimized"));
    wins.sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0));
    document.querySelectorAll("#desktop .window").forEach(x => x.classList.toggle("front", x === wins[0]));
    termResize();
  } finally { winApplying = false; }
}

let winSaveT = null, winPushAt = 0;
function winPush() {
  // the phone's transient fullscreen stack must never overwrite the
  // desktop layout other browsers share
  if (IS_MOBILE) return;
  const st = winSnapshot();
  localStorage.setItem("exe_winstate", JSON.stringify(st));
  j("/v1/ui/state", { method: "PUT", json: { client: WIN_CLIENT, state: st } })
    .then(r => { winRev = Math.max(winRev, r.rev || 0); })
    .catch(() => {}); // daemon away — localStorage still has it
}
function winSave() {
  if (winApplying) return;
  clearTimeout(winSaveT);
  winSaveT = setTimeout(winPush, 250);
}
// mid-drag: leading-edge throttle so the move streams to other browsers,
// with the trailing debounce catching the final resting spot
function winSaveLive() {
  if (winApplying) return;
  const now = performance.now();
  if (now - winPushAt >= 150) { winPushAt = now; winPush(); }
  winSave();
}

let winES = null;
function winSubscribe() {
  if (winES) winES.close();
  const tok = tokenInput.value ? "?token=" + encodeURIComponent(tokenInput.value) : "";
  winES = new EventSource("/v1/ui/events" + tok);
  winES.onmessage = e => {
    let ev;
    try { ev = JSON.parse(e.data); } catch (err) { return; }
    const stale = (ev.rev || 0) <= winRev;
    winRev = Math.max(winRev, ev.rev || 0);
    if (ev.client === WIN_CLIENT || stale || !ev.state) return;
    localStorage.setItem("exe_winstate", JSON.stringify(ev.state));
    applyWinState(ev.state);
  };
}
tokenInput.addEventListener("change", () => { if (winES) winSubscribe(); });
function applyMobileMode(on) {
  const was = document.body.classList.contains("mobile");
  IS_MOBILE = !!on;
  document.body.classList.toggle("mobile", IS_MOBILE);
  const hint = $("#vm-hint");
  if (hint) hint.textContent = "double-click a VM to open it";
  if (IS_MOBILE && !was) {
    const wins = [...document.querySelectorAll("#desktop .window")].filter(w => !w.hidden && !MOB_DIALOG.has(w.id));
    wins.sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0));
    if (wins[0]) mobileSwapIn(wins[0]);
  } else if (!IS_MOBILE && was) {
    mobileStack = [];
    try {
      const cached = JSON.parse(localStorage.getItem("exe_winstate") || "null");
      if (cached) applyWinState(cached);
    } catch (e) {}
  }
  updateMobileMinimizedBars();
  termResize();
  document.querySelectorAll(".term-window").forEach(w => { if (w._termResize) w._termResize(); });
}
function syncMobileMode() { applyMobileMode(detectMobile()); }
window.addEventListener("resize", () => {
  clearTimeout(syncMobileMode._t);
  syncMobileMode._t = setTimeout(syncMobileMode, 80);
});

function winRestore() {
  // mobile restores its own phone-local state, never the desktop snapshot
  if (IS_MOBILE) { mobRestore(); return; }
  let cached = null;
  try { cached = JSON.parse(localStorage.getItem("exe_winstate") || "null"); } catch (e) {}
  if (cached) applyWinState(cached); else openWin("#win-vms");
  j("/v1/ui/state").then(r => {
    winRev = r.rev || 0;
    if (r.state) {
      localStorage.setItem("exe_winstate", JSON.stringify(r.state));
      applyWinState(r.state);
    }
  }).catch(() => {}).finally(winSubscribe);
}

// ---- menus ----
let openMenuEl = null;
function menuClose() {
  if (!openMenuEl) return;
  openMenuEl.classList.remove("open");
  const dd = $("#dd-" + openMenuEl.dataset.m);
  if (dd) dd.hidden = true;
  openMenuEl = null;
}
function winMenuLabel(w) {
  const label = (w.querySelector(".title")?.textContent || w.id.replace(/^win-/, "")).trim();
  return w.classList.contains("minimized") ? label + " — minimized" : label;
}
function windowMenuItems() {
  const seen = new Set(), out = [];
  const add = w => {
    if (!w || seen.has(w.id) || MOB_DIALOG.has(w.id)) return;
    if (w.id.startsWith("win-app-") && isHiddenDesktopApp(w.id.slice(8))) return;
    if (!w.closest("#desktop")) return;
    seen.add(w.id);
    out.push(w);
  };
  [...document.querySelectorAll("#desktop .window")]
    .filter(w => !w.hidden && !MOB_DIALOG.has(w.id))
    .sort((a, b) => (+b.style.zIndex || 0) - (+a.style.zIndex || 0))
    .forEach(add);
  if (IS_MOBILE) [...mobileStack].reverse().forEach(id => add(document.getElementById(id)));
  return out;
}
function renderWindowListMenu(target) {
  if (!target) return;
  target.replaceChildren();
  const wins = windowMenuItems();
  if (!wins.length) {
    target.append(el("div", { class: "dd-item dis" }, "No open windows"));
    return;
  }
  for (const w of wins) {
    const item = el("div", {
      class: "dd-item"
        + (w.classList.contains("front") ? " win-active" : "")
        + (w.classList.contains("minimized") ? " win-minimized" : ""),
    }, winMenuLabel(w));
    item.addEventListener("click", () => { menuClose(); openWin(w); });
    target.append(item);
  }
}
function menuShow(m) {
  menuClose();
  openMenuEl = m;
  m.classList.add("open");
  if (m.dataset.m === "file") renderWindowListMenu($("#file-windowlist"));
  if (m.dataset.m === "windows") renderWindowListMenu($("#mobile-windowlist"));
  const dd = $("#dd-" + m.dataset.m);
  if (!dd) return;
  dd.hidden = false;
  dd.style.left = Math.max(4, Math.min(m.getBoundingClientRect().left, window.innerWidth - dd.offsetWidth - 4)) + "px";
}
document.querySelectorAll(".menu[data-m]").forEach(m => {
  m.addEventListener("mousedown", e => {
    e.preventDefault(); e.stopPropagation();
    if (openMenuEl === m) menuClose(); else menuShow(m);
  });
  m.addEventListener("mouseenter", () => { if (openMenuEl && openMenuEl !== m) menuShow(m); });
});
document.addEventListener("mousedown", e => {
  if (openMenuEl && !e.target.closest(".menu,.dropdown")) menuClose();
});
const ACTIONS = {
  about: () => openAboutWin(),
  newvm: () => { openWin("#win-newvm"); $("#c-name").focus(); },
  desktop: () => openDesktopWin(),
  upload: () => openWin("#win-upload"),
  closewin: closeFrontWindow,
  refresh: () => { Promise.all([loadVMs(), loadApps()]).then(() => toast("Refreshed")).catch(e => toast(e.message)); },
  winvms: () => openWin("#win-vms"),
  winchat: () => openChatWin(),
  winnews: () => openNewsWin(),
  winconfig: openConfigWin,
  winlog: openLogWin,
  cfstatus: () => sumOpen(),
  cfwizard: () => wizOpen(false),
  join: () => joinOpen(),
  token: () => { openWin("#win-token"); tokenInput.focus(); },
  docs: () => openDocsWin(),
  skillguide: () => openSkillWin(),
};
const MENU_DOUBLE_OPEN_ACTIONS = new Set(["winconfig"]);
function selectMenuItem(it) {
  it.parentElement?.querySelectorAll(".dd-item.sel").forEach(x => x.classList.remove("sel"));
  it.classList.add("sel");
}
document.querySelectorAll(".dd-item").forEach(it => {
  if (MENU_DOUBLE_OPEN_ACTIONS.has(it.dataset.act)) {
    it.addEventListener("click", e => { e.stopPropagation(); selectMenuItem(it); });
    objectOpen(it, () => { menuClose(); const a = ACTIONS[it.dataset.act]; if (a) a(); });
  } else {
    it.addEventListener("click", () => { menuClose(); const a = ACTIONS[it.dataset.act]; if (a) a(); });
  }
});

// ---- Apple → About: show this host's name and IPs, click an IP to copy ----
// hostinfo is fetched BEFORE the window shows so it opens at its final
// size — filling it in afterwards grew the window and made it jump
// (worst on mobile, where the dialog is centered on its own height)
async function openAboutWin() {
  const box = $("#about-host");
  try {
    const h = await j("/v1/hostinfo");
    $("#about-tagline").textContent = "your personal cloud, right on this " + (h.machine || "machine");
    box.replaceChildren();
    if (h.hostname)
      box.append(el("div", {}, el("span", { class: "muted" }, "Host "), document.createTextNode(h.hostname)));
    // LAN and Tailscale IPs share one row, middot-separated; each is click-to-copy
    const ips = el("div", {});
    const addIP = (label, ip) => {
      if (ips.childNodes.length) ips.append(document.createTextNode(" · "));
      ips.append(el("span", { class: "muted" }, label + " "),
        el("span", { class: "ip-copy", title: "Click to copy", onclick: () => copyIP(ip) }, ip));
    };
    if (h.lan_ip) addIP("LAN", h.lan_ip);
    if (h.tailscale_ip) addIP("Tailscale", h.tailscale_ip);
    if (ips.childNodes.length) box.append(ips);
  } catch (e) {
    box.replaceChildren(el("span", { class: "muted" }, "host info unavailable"));
  }
  openWin("#win-about");
}
async function copyIP(ip) {
  let ok = false;
  try { await navigator.clipboard.writeText(ip); ok = true; }
  catch (e) { // plain-http origins have no clipboard API — fall back to execCommand
    const ta = document.createElement("textarea");
    ta.value = ip; ta.style.position = "fixed"; ta.style.top = "-1000px";
    document.body.appendChild(ta); ta.focus(); ta.select();
    try { ok = document.execCommand("copy"); } catch (e2) {}
    ta.remove();
  }
  toast(ok ? "Copied IP " + ip : "Copy failed — select and copy " + ip);
}

// ---- Help → exe Documentation: the manual ships in the binary (docs.md,
// served at /docs.md the way the skill file is) and is fetched once per
// page load, rendered with the chat's markdown pipeline ----
let docsLoaded = false;
async function openDocsWin() {
  $("#docs-url").textContent = location.origin + "/docs.md";
  openWin("#win-docs");
  if (docsLoaded) return;
  try {
    const r = await fetch("/docs.md");
    if (!r.ok) throw new Error("HTTP " + r.status);
    // docs.md is hard-wrapped in the source; unlike chat (breaks:true),
    // fold those newlines back into flowing paragraphs
    $("#docs-md").innerHTML = DOMPurify.sanitize(marked.parse(await r.text(), { breaks: false }));
    docsLoaded = true;
  } catch (e) {
    $("#docs-md").replaceChildren(el("span", { class: "muted" }, "Could not load the documentation — " + e.message));
  }
}

// ---- Help → Agent Skill Guide ----
function skillSnippet() {
  const base = location.origin, host = location.hostname;
  let s = `Read ${base}/skill.md and follow it. It documents the exe daemon at ${base}: ` +
    `an HTTP API for creating and managing persistent Linux VMs, an SSH gate for running ` +
    `commands inside them (ssh -p 2222 <vm>@${host}), and endpoints for discovering and ` +
    `publishing VM services. When I ask you to build, run, or test something, use one of ` +
    `these VMs as your sandbox.`;
  const tok = localStorage.getItem("exe_token");
  if (tok) s += `\n\nAPI requests need the header: Authorization: Bearer ${tok}`;
  return s;
}
function openSkillWin() {
  $("#skill-url").textContent = location.origin + "/skill.md";
  $("#skill-snippet").value = skillSnippet();
  $("#skill-note").textContent = localStorage.getItem("exe_token")
    ? "Includes your API token — share carefully." : "";
  openWin("#win-skill");
}
$("#skill-copy").addEventListener("click", async () => {
  const ta = $("#skill-snippet");
  ta.focus();
  ta.select();
  let ok = false;
  try { await navigator.clipboard.writeText(ta.value); ok = true; }
  catch (e) { ok = document.execCommand("copy"); } // plain-http origins have no clipboard API
  toast(ok ? "Prompt copied" : "Copy failed — copy the selected text manually");
});

// ---- clock ----
function tickClock() {
  $("#clock").textContent = new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}
tickClock();
setInterval(tickClock, 10000);

// ---- desktop background (picture + color) ----
let deskPrefs = { mode: "center", color: "#ccccff", image: false, rev: 0 };
function deskWallpaperURL() {
  const tok = tokenInput.value ? "&token=" + encodeURIComponent(tokenInput.value) : "";
  return "/v1/desktop/wallpaper?r=" + (deskPrefs.rev || Date.now()) + tok;
}
function paintDeskLayer(el, prefs, preview) {
  prefs = prefs || deskPrefs;
  el.style.backgroundColor = prefs.color || "#ccccff";
  if (!prefs.image) {
    el.style.backgroundImage = preview ? "none" : "";
    el.style.backgroundSize = "";
    el.style.backgroundRepeat = "";
    el.style.backgroundPosition = "";
    return;
  }
  el.style.backgroundImage = "url(\"" + deskWallpaperURL() + "\")";
  el.style.backgroundRepeat = "no-repeat";
  el.style.backgroundPosition = "center center";
  if (prefs.mode === "stretch") el.style.backgroundSize = "100% 100%";
  else if (prefs.mode === "cover") el.style.backgroundSize = "cover";
  else el.style.backgroundSize = "auto";
}
function applyDesktop() {
  const bg = $("#desktop-bg");
  document.body.classList.toggle("custom-desk", !!deskPrefs.image);
  if (deskPrefs.color) document.body.style.backgroundColor = deskPrefs.color;
  else document.body.style.backgroundColor = "";
  if (!bg) return;
  if (!deskPrefs.image) {
    bg.style.backgroundImage = "";
    bg.style.backgroundSize = "";
    bg.style.backgroundColor = "";
    return;
  }
  paintDeskLayer(bg, deskPrefs, false);
}
async function loadDesktop() {
  try { deskPrefs = await j("/v1/desktop"); } catch (e) { return; }
  applyDesktop();
}
function deskModeValue() {
  const n = document.querySelector("input[name=desk-mode]:checked");
  return n ? n.value : "center";
}
function deskFillForm() {
  $("#desk-color").value = /^#[0-9a-fA-F]{6}$/.test(deskPrefs.color) ? deskPrefs.color : "#ccccff";
  document.querySelectorAll("input[name=desk-mode]").forEach(r => { r.checked = r.value === (deskPrefs.mode || "center"); });
  paintDeskLayer($("#desk-preview"), {
    mode: deskModeValue(), color: $("#desk-color").value, image: deskPrefs.image, rev: deskPrefs.rev,
  }, true);
}
function openDesktopWin() {
  deskFillForm();
  $("#desk-overlay").hidden = false;
}
function deskClose() { $("#desk-overlay").hidden = true; }
async function deskSavePrefs() {
  try {
    deskPrefs = await j("/v1/desktop", { method: "PUT", json: { mode: deskModeValue(), color: $("#desk-color").value } });
    applyDesktop();
    deskFillForm();
  } catch (e) { toast(e.message); }
}
$("#desk-x").onclick = deskClose;
$("#desk-ok").onclick = async () => { await deskSavePrefs(); deskClose(); };
$("#desk-color").addEventListener("input", () => {
  deskPrefs.color = $("#desk-color").value;
  applyDesktop();
  deskFillForm();
});
$("#desk-color").addEventListener("change", deskSavePrefs);
document.querySelectorAll("input[name=desk-mode]").forEach(r => r.addEventListener("change", deskSavePrefs));
$("#desk-choose").onclick = () => $("#desk-file").click();
$("#desk-file").onchange = async () => {
  const f = $("#desk-file").files[0];
  $("#desk-file").value = "";
  if (!f) return;
  if (f.size > 8 << 20) { toast("picture larger than 8 MB"); return; }
  try {
    const r = await api("/v1/desktop/wallpaper", { method: "PUT", body: f, headers: { "Content-Type": f.type || "application/octet-stream" } });
    deskPrefs = await r.json();
    applyDesktop();
    deskFillForm();
    toast("Desktop picture set");
  } catch (e) { toast(e.message); }
};
$("#desk-clear").onclick = async () => {
  try {
    deskPrefs = await j("/v1/desktop/wallpaper", { method: "DELETE" });
    applyDesktop();
    deskFillForm();
    toast("Default desktop pattern");
  } catch (e) { toast(e.message); }
};

// ---- desktop icons ----
const SCREEN_COLORS = { running: "#7FD67F", starting: "#E8B34B", stopping: "#E8B34B", stopped: "#3A3F46", error: "#D66A6A" };
function macIcon(state, size) {
  const c = SCREEN_COLORS[state] || "#3A3F46";
  return `<svg width="${size}" height="${size}" viewBox="0 0 32 32" shape-rendering="crispEdges">
    <rect x="6.5" y="1.5" width="19" height="24" fill="#DAD5CA" stroke="#000"/>
    <rect x="9.5" y="4.5" width="13" height="10" fill="#6e6a61" stroke="#000"/>
    <rect x="10.5" y="5.5" width="11" height="8" fill="${c}" stroke="#000"/>
    <rect x="12" y="18" width="8" height="2" fill="#55524B"/>
    <rect x="20" y="21" width="3" height="2" fill="#B7B2A7"/>
    <rect x="9.5" y="25.5" width="13" height="3" fill="#C8C3B8" stroke="#000"/>
  </svg>`;
}
$("#about-icon").innerHTML = macIcon("running", 44);
let selectedIcon = null;
const ICON_GW = 88, ICON_GH = 80;
function loadIconPos() {
  try { return JSON.parse(localStorage.getItem("exe_iconpos") || "{}"); } catch (e) { return {}; }
}
function saveIconPos(p) { localStorage.setItem("exe_iconpos", JSON.stringify(p)); }
function snapIcon(x, y) {
  const desk = $("#desktop");
  const maxX = Math.max(0, desk.clientWidth - 82);
  const maxY = Math.max(0, desk.clientHeight - 70);
  x = Math.max(0, Math.min(maxX, Math.round(x / ICON_GW) * ICON_GW));
  y = Math.max(0, Math.min(maxY, Math.round(y / ICON_GH) * ICON_GH));
  return { x, y };
}
function placeDicon(ic, key, index) {
  const pos = loadIconPos()[key];
  const desk = $("#desktop");
  let x, y;
  if (pos) { x = pos.x; y = pos.y; }
  else {
    x = Math.max(0, (desk.clientWidth || 800) - 96);
    y = 12 + index * ICON_GH;
  }
  const s = snapIcon(x, y);
  ic.style.left = s.x + "px";
  ic.style.top = s.y + "px";
  wireIconDrag(ic, key);
}
function wireIconDrag(ic, key) {
  if (ic._dragWired) return;
  ic._dragWired = true;
  // native <img> / shortcut drag looks like "dragging a file" and steals
  // the pointer — same handler for Workspace, Terminal, VMs and desk apps
  ic.addEventListener("dragstart", e => e.preventDefault());
  ic.querySelectorAll("img").forEach(img => { img.draggable = false; });
  // On mobile the icons flow as a grid, but keep small drag nudges so the
  // user can reorder; double-click still opens.
  ic.addEventListener("pointerdown", e => {
    if (e.button !== 0) return;
    const r = ic.getBoundingClientRect();
    const desk = $("#desktop").getBoundingClientRect();
    const ox = e.clientX - r.left, oy = e.clientY - r.top;
    const sx = e.clientX, sy = e.clientY;
    let moved = false;
    try { ic.setPointerCapture(e.pointerId); } catch (err) {}
    const move = ev => {
      if (!moved && (ev.clientX - sx) ** 2 + (ev.clientY - sy) ** 2 < 16) return;
      if (!moved) { moved = true; ic.classList.add("dragging"); ev.preventDefault(); }
      ic.style.left = (ev.clientX - desk.left - ox) + "px";
      ic.style.top = (ev.clientY - desk.top - oy) + "px";
    };
    const up = ev => {
      ic.classList.remove("dragging");
      try { ic.releasePointerCapture(ev.pointerId); } catch (err) {}
      ic.removeEventListener("pointermove", move);
      ic.removeEventListener("pointerup", up);
      if (!moved) return;
      const s = snapIcon(parseFloat(ic.style.left) || 0, parseFloat(ic.style.top) || 0);
      ic.style.left = s.x + "px";
      ic.style.top = s.y + "px";
      const all = loadIconPos();
      all[key] = s;
      saveIconPos(all);
    };
    ic.addEventListener("pointermove", move);
    ic.addEventListener("pointerup", up);
  });
}
function renderIcons(vms) {
  const box = $("#icons");
  box.replaceChildren();
  let slot = 0;
  {
    const ic = el("div", { class: "dicon" + (selectedIcon === "ws" ? " sel" : ""), title: "Host Workspace — not inside a VM" });
    ic.innerHTML = FOLDER_ICON;
    ic.append(el("span", { class: "lbl" }, "Workspace"));
    ic.addEventListener("click", e => {
      e.stopPropagation();
      selectedIcon = "ws";
      box.querySelectorAll(".dicon").forEach(d => d.classList.remove("sel"));
      ic.classList.add("sel");
    });
    objectOpen(ic, () => openFinderWin(""));
    box.append(ic);
    placeDicon(ic, "ws", slot++);
  }
  {
    const ic = el("div", { class: "dicon" + (selectedIcon === "hostterm" ? " sel" : ""), title: "Host Terminal — the only host workplace" });
    ic.innerHTML = TERMINAL_ICON;
    ic.append(el("span", { class: "lbl" }, "Terminal"));
    ic.addEventListener("click", e => {
      e.stopPropagation();
      selectedIcon = "hostterm";
      box.querySelectorAll(".dicon").forEach(d => d.classList.remove("sel"));
      ic.classList.add("sel");
    });
    objectOpen(ic, () => openHostTermWin());
    box.append(ic);
    placeDicon(ic, "hostterm", slot++);
  }
  for (const vm of vms) {
    const ic = el("div", { class: "dicon" + (selectedIcon === vm.name ? " sel" : ""), title: `${vm.name} — ${vm.state}` });
    ic.innerHTML = macIcon(vm.state, 32);
    ic.append(el("span", { class: "lbl" }, vm.name));
    ic.addEventListener("click", e => {
      e.stopPropagation();
      selectedIcon = vm.name;
      box.querySelectorAll(".dicon").forEach(d => d.classList.remove("sel"));
      ic.classList.add("sel");
    });
    objectOpen(ic, () => openVM(vm.name));
    box.append(ic);
    placeDicon(ic, "vm:" + vm.name, slot++);
  }
  const appsMenuList = $("#dd-apps-list");
  const drawerAppsList = $("#dd-drawer-apps-list");
  if (appsMenuList) appsMenuList.replaceChildren();
  if (drawerAppsList) drawerAppsList.replaceChildren();
  for (const app of appsList) {
    const key = "app:" + app.name;
    const ic = el("div", { class: "dicon" + (selectedIcon === key ? " sel" : ""), title: app.title || app.name });
    ic.innerHTML = app.icon
      ? `<img src="/apps/${encodeURIComponent(app.name)}/${app.icon.split("/").map(encodeURIComponent).join("/")}" alt="" draggable="false">`
      : APP_FALLBACK_ICON;
    ic.append(el("span", { class: "lbl" }, app.title || app.name));
    ic.addEventListener("click", e => {
      e.stopPropagation();
      selectedIcon = key;
      box.querySelectorAll(".dicon").forEach(d => d.classList.remove("sel"));
      ic.classList.add("sel");
    });
    objectOpen(ic, () => openAppWin(app.name));
    addLongPressMenu(ic, (x, y) => showCtxMenu(x, y, [
      { label: "Open", act: () => openAppWin(app.name) },
      { label: "Get Info", act: () => toast(app.title || app.name) },
    ]));
    box.append(ic);
    placeDicon(ic, "app:" + app.name, slot++);
    // Apps cascaded menu items
    const menuItem = (target) => {
      const d = el("div", { class: "dd-item" }, app.title || app.name);
      d.addEventListener("click", e => { e.stopPropagation(); selectMenuItem(d); });
      objectOpen(d, () => { menuClose(); openAppWin(app.name); });
      target.append(d);
    };
    if (appsMenuList) menuItem(appsMenuList);
    if (drawerAppsList) menuItem(drawerAppsList);
  }
  if (appsMenuList && !appsMenuList.children.length) appsMenuList.append(el("div", { class: "dd-item dis" }, "No apps"));
  if (drawerAppsList && !drawerAppsList.children.length) drawerAppsList.append(el("div", { class: "dd-item dis" }, "No apps"));
}

// ---- desktop apps (folders in ~/.exe/apps with app.json + index.html) ----
let appsList = [];
const APP_FALLBACK_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">
  <path d="M16 2.5 L29.5 16 L16 29.5 L2.5 16 Z" fill="#DAD5CA" stroke="#000"/>
  <path d="M16 8.5 L23.5 16 L16 23.5 L8.5 16 Z" fill="#9C9AF2" stroke="#000"/>
</svg>`;
// OS 9 folder, downsampled pixel-for-pixel from a real Mac OS 9 Finder
// capture (Macintosh HD window): lavender perspective folder, navy outline,
// quantized to the sampled 9-color ramp and run-length encoded per color.
const FOLDER_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges"><g transform="translate(0 0)"><path fill="#6666be" d="M2 0h1v1h-1zM4 1h1v1h-1zM0 2h2v1h-2zM6 2h1v1h-1zM0 3h1v1h-1zM8 3h1v1h-1zM0 4h1v1h-1zM10 4h1v1h-1zM0 5h1v1h-1zM12 5h1v1h-1zM15 5h1v1h-1zM17 5h1v1h-1zM0 6h1v1h-1zM14 6h1v1h-1zM19 6h1v1h-1zM21 7h1v1h-1zM15 8h1v1h-1zM17 9h1v1h-1zM22 9h1v1h-1zM19 10h1v1h-1zM21 11h1v1h-1zM23 11h1v1h-1zM22 12h2v1h-2zM23 13h1v1h-1zM23 14h1v1h-1zM19 29h1v1h-1zM21 30h1v1h-1z"/><path fill="#4c4c96" d="M3 0h1v1h-1zM2 1h1v1h-1zM5 1h1v1h-1zM7 2h1v1h-1zM3 3h1v1h-1zM9 3h1v1h-1zM5 4h1v1h-1zM11 4h1v1h-1zM7 5h1v1h-1zM13 5h1v1h-1zM18 5h1v1h-1zM9 6h1v1h-1zM20 6h1v1h-1zM0 7h1v1h-1zM11 7h1v1h-1zM0 8h1v1h-1zM13 8h1v1h-1zM22 8h1v1h-1zM0 9h1v1h-1zM15 9h1v1h-1zM0 10h1v1h-1zM17 10h1v1h-1zM23 10h1v1h-1zM0 11h1v1h-1zM19 11h1v1h-1zM0 12h1v1h-1zM0 13h1v1h-1zM0 14h1v1h-1zM0 15h1v1h-1zM23 15h1v1h-1zM0 16h1v1h-1zM23 16h1v1h-1zM0 17h1v1h-1zM23 17h1v1h-1zM0 18h1v1h-1zM23 18h1v1h-1zM0 19h1v1h-1zM23 19h1v1h-1zM0 20h1v1h-1zM23 20h1v1h-1zM2 21h1v1h-1zM23 21h1v1h-1zM4 22h1v1h-1zM23 22h1v1h-1zM6 23h1v1h-1zM23 23h1v1h-1zM8 24h1v1h-1zM23 24h1v1h-1zM10 25h1v1h-1zM23 25h1v1h-1zM12 26h1v1h-1zM23 26h1v1h-1zM14 27h1v1h-1zM23 27h1v1h-1zM16 28h1v1h-1zM23 28h1v1h-1zM18 29h1v1h-1zM23 29h1v1h-1zM20 30h1v1h-1zM23 30h1v1h-1z"/><path fill="#30305e" d="M4 0h1v1h-1zM6 1h1v1h-1zM2 2h1v1h-1zM8 2h1v1h-1zM4 3h1v1h-1zM10 3h1v1h-1zM6 4h1v1h-1zM12 4h1v1h-1zM15 4h3v1h-3zM8 5h1v1h-1zM14 5h1v1h-1zM19 5h1v1h-1zM10 6h1v1h-1zM21 6h1v1h-1zM12 7h1v1h-1zM22 7h1v1h-1zM14 8h1v1h-1zM16 9h1v1h-1zM23 9h1v1h-1zM18 10h1v1h-1zM24 10h1v1h-1zM20 11h1v1h-1zM21 12h1v1h-1zM22 13h1v1h-1zM22 14h1v1h-1zM22 15h1v1h-1zM22 16h1v1h-1zM22 17h1v1h-1zM22 18h1v1h-1zM22 19h1v1h-1zM22 20h1v1h-1zM1 21h1v1h-1zM22 21h1v1h-1zM3 22h1v1h-1zM22 22h1v1h-1zM5 23h1v1h-1zM22 23h1v1h-1zM25 23h2v1h-2zM7 24h1v1h-1zM22 24h1v1h-1zM25 24h3v1h-3zM9 25h1v1h-1zM22 25h1v1h-1zM25 25h2v1h-2zM11 26h1v1h-1zM22 26h1v1h-1zM25 26h1v1h-1zM13 27h1v1h-1zM22 27h1v1h-1zM15 28h1v1h-1zM22 28h1v1h-1zM17 29h1v1h-1zM22 29h1v1h-1zM19 30h1v1h-1zM22 30h1v1h-1zM21 31h1v1h-1z"/><path fill="#9a9af4" d="M3 1h1v1h-1zM4 2h2v1h-2zM6 3h2v1h-2zM8 4h2v1h-2zM10 5h2v1h-2zM16 5h1v1h-1zM12 6h1v1h-1zM15 6h1v1h-1zM2 20h1v1h-1zM20 20h1v1h-1zM4 21h1v1h-1zM19 21h2v1h-2zM6 22h1v1h-1zM18 22h3v1h-3zM8 23h1v1h-1zM17 23h4v1h-4zM10 24h1v1h-1zM16 24h5v1h-5zM12 25h1v1h-1zM15 25h6v1h-6zM14 26h7v1h-7zM16 27h5v1h-5zM18 28h3v1h-3z"/><path fill="#8080dc" d="M3 2h1v1h-1zM2 3h1v1h-1zM5 3h1v1h-1zM4 4h1v1h-1zM7 4h1v1h-1zM6 5h1v1h-1zM9 5h1v1h-1zM8 6h1v1h-1zM11 6h1v1h-1zM13 6h1v1h-1zM16 6h3v1h-3zM10 7h1v1h-1zM13 7h8v1h-8zM12 8h1v1h-1zM16 8h6v1h-6zM14 9h1v1h-1zM18 9h4v1h-4zM16 10h1v1h-1zM20 10h3v1h-3zM18 11h1v1h-1zM22 11h1v1h-1zM20 12h1v1h-1zM21 14h1v1h-1zM21 15h1v1h-1zM21 16h1v1h-1zM21 17h1v1h-1zM21 18h1v1h-1zM21 19h1v1h-1zM1 20h1v1h-1zM21 20h1v1h-1zM3 21h1v1h-1zM21 21h1v1h-1zM5 22h1v1h-1zM21 22h1v1h-1zM7 23h1v1h-1zM21 23h1v1h-1zM9 24h1v1h-1zM21 24h1v1h-1zM11 25h1v1h-1zM21 25h1v1h-1zM13 26h1v1h-1zM21 26h1v1h-1zM15 27h1v1h-1zM21 27h1v1h-1zM17 28h1v1h-1zM21 28h1v1h-1zM20 29h2v1h-2z"/><path fill="#ececfd" d="M1 3h1v1h-1zM3 4h1v1h-1z"/><path fill="#d2d2fa" d="M1 4h2v1h-2zM1 5h5v1h-5zM1 6h7v1h-7zM1 7h9v1h-9zM1 8h7v1h-7zM11 8h1v1h-1zM1 9h5v1h-5zM13 9h1v1h-1zM1 10h5v1h-5zM15 10h1v1h-1zM1 11h4v1h-4zM17 11h1v1h-1zM1 12h3v1h-3zM19 12h1v1h-1zM1 13h2v1h-2zM21 13h1v1h-1zM1 14h1v1h-1z"/><path fill="#b6b6f4" d="M8 8h3v1h-3zM6 9h7v1h-7zM6 10h9v1h-9zM5 11h12v1h-12zM4 12h15v1h-15zM3 13h18v1h-18zM2 14h19v1h-19zM1 15h20v1h-20zM1 16h20v1h-20zM1 17h20v1h-20zM1 18h20v1h-20zM1 19h20v1h-20zM3 20h17v1h-17zM5 21h14v1h-14zM7 22h11v1h-11zM9 23h8v1h-8zM11 24h5v1h-5zM13 25h2v1h-2z"/><path fill="#222244" d="M24 11h1v1h-1zM24 12h1v1h-1zM24 13h1v1h-1zM24 14h1v1h-1zM24 15h1v1h-1zM24 16h1v1h-1zM24 17h1v1h-1zM24 18h1v1h-1zM24 19h1v1h-1zM24 20h1v1h-1zM24 21h1v1h-1zM24 22h1v1h-1zM24 23h1v1h-1zM24 24h1v1h-1zM24 25h1v1h-1zM24 26h1v1h-1zM24 27h1v1h-1zM24 28h1v1h-1zM24 29h1v1h-1zM24 30h1v1h-1zM22 31h2v1h-2z"/><path fill="#000" fill-opacity="0.25" d="M25 20h1v1h-1zM27 21h1v1h-1zM29 22h1v1h-1zM30 26h1v1h-1zM29 27h1v1h-1zM28 28h1v1h-1zM27 29h1v1h-1zM26 30h1v1h-1zM25 31h1v1h-1z"/><path fill="#000" fill-opacity="0.5" d="M25 21h1v1h-1zM25 22h3v1h-3zM27 23h2v1h-2zM28 24h2v1h-2zM27 25h3v1h-3zM26 26h3v1h-3zM25 27h3v1h-3zM25 28h2v1h-2zM25 29h1v1h-1zM24 31h1v1h-1z"/><path fill="#000" fill-opacity="0.4" d="M26 21h1v1h-1zM28 22h1v1h-1zM30 25h1v1h-1z"/><path fill="#000" fill-opacity="0.45" d="M29 23h1v1h-1zM30 24h1v1h-1zM29 26h1v1h-1zM28 27h1v1h-1zM27 28h1v1h-1zM26 29h1v1h-1zM25 30h1v1h-1z"/><path fill="#000" fill-opacity="0.35" d="M30 23h1v1h-1z"/><path fill="#000" fill-opacity="0.2" d="M31 24h1v1h-1z"/></g></svg>`;
// OS 9 generic document: white page, black outline, folded top-right corner
const DOC_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">
  <path fill="#fff" stroke="#000" d="M7.5 3.5h11l6 6v19h-17z"/>
  <path fill="#ccc" stroke="#000" d="M18.5 3.5v6h6z"/>
</svg>`;
// image document: the page with a framed landscape — sun over two hills
const IMAGE_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">
  <path fill="#fff" stroke="#000" d="M7.5 3.5h11l6 6v19h-17z"/>
  <path fill="#ccc" stroke="#000" d="M18.5 3.5v6h6z"/>
  <rect x="9.5" y="12.5" width="13" height="12" fill="#9cf" stroke="#000"/>
  <path fill="#ff3" d="M12 14h2v1h-2zM12 15h2v1h-2z"/>
  <path fill="#7b7" d="M14 18h1v1h-1zM13 19h3v1h-3zM12 20h5v1h-5zM11 21h7v1h-7zM10 22h9v1h-9zM10 23h10v1h-10z"/>
  <path fill="#363" d="M18 17h1v1h-1zM17 18h3v1h-3zM16 19h5v1h-5zM15 20h7v1h-7zM14 21h8v1h-8zM13 22h9v1h-9zM12 23h10v1h-10z"/>
</svg>`;
// archive document (zip/dmg/rar/7z): the page with a zipper down the middle
const ARCHIVE_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">
  <path fill="#fff" stroke="#000" d="M7.5 3.5h11l6 6v19h-17z"/>
  <path fill="#ccc" stroke="#000" d="M18.5 3.5v6h6z"/>
  <rect x="11.5" y="3.5" width="5" height="25" fill="#eee" stroke="#000"/>
  <path fill="#888" d="M12 5h2v1h-2zM14 7h2v1h-2zM12 9h2v1h-2zM14 11h2v1h-2zM14 15h2v1h-2zM12 17h2v1h-2zM14 19h2v1h-2zM12 21h2v1h-2zM14 23h2v1h-2zM12 25h2v1h-2zM14 27h2v1h-2z"/>
  <path fill="#444" d="M12 13h4v2h-4zM13 15h2v1h-2z"/>
</svg>`;
const fileIcon = en => en.dir ? FOLDER_ICON
  : isImageFile(en.name) ? IMAGE_ICON
  : isArchiveFile(en.name) ? ARCHIVE_ICON : DOC_ICON;
// host Terminal: the compact Mac with a dark screen showing a green prompt
const TERMINAL_ICON = `<svg width="32" height="32" viewBox="0 0 32 32" shape-rendering="crispEdges">
  <rect x="6.5" y="1.5" width="19" height="24" fill="#DAD5CA" stroke="#000"/>
  <rect x="9.5" y="4.5" width="13" height="10" fill="#6e6a61" stroke="#000"/>
  <rect x="10.5" y="5.5" width="11" height="8" fill="#000" stroke="#000"/>
  <path fill="#3f3" d="M12 7h1v1h-1zM13 8h1v1h-1zM12 9h1v1h-1zM15 9h3v1h-3z"/>
  <rect x="12" y="18" width="8" height="2" fill="#55524B"/>
  <rect x="20" y="21" width="3" height="2" fill="#B7B2A7"/>
  <rect x="9.5" y="25.5" width="13" height="3" fill="#C8C3B8" stroke="#000"/>
</svg>`;
const HIDDEN_DESKTOP_APPS = new Set(["Editor", "Browser"]);
function isHiddenDesktopApp(name) { return HIDDEN_DESKTOP_APPS.has(name); }
async function loadApps() {
  appsList = (await j("/v1/apps")).filter(a => !isHiddenDesktopApp(a.name));
  document.querySelectorAll("#desktop .app-window").forEach(w => {
    if (isHiddenDesktopApp(w.id.slice(8))) w.remove();
  });
  renderIcons(lastVMs);
  // app windows restored from a layout snapshot before the list arrived get
  // their real title and height once metadata is here
  for (const a of appsList) {
    const w = document.getElementById("win-app-" + a.name);
    if (!w) continue;
    w.querySelector(".title").textContent = a.title || a.name;
    w.classList.toggle("grow", !!(a.window && a.window.grow));
    // app.json height is only a default — never clobber a height the user
    // resized (restored from the layout snapshot as an inline style)
    const fr = w.querySelector(".app-frame");
    if (a.window && a.window.height && !fr.style.height) fr.style.height = a.window.height + "px";
  }
}
function openAppWin(name) {
  if (isHiddenDesktopApp(name)) return;
  const id = "win-app-" + name;
  let w = document.getElementById(id);
  if (!w) {
    const app = appsList.find(a => a.name === name) || {};
    w = el("div", { class: "window app-window", id });
    const n = document.querySelectorAll("#desktop .app-window").length;
    w.style.left = (120 + n * 24) + "px";
    w.style.top = (70 + n * 20) + "px";
    w.style.width = ((app.window && app.window.width) || 420) + "px";
    w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
      <div class="title"></div>
      <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
      <div class="win-frame"><iframe class="app-frame"></iframe></div>`;
    w.querySelector(".title").textContent = app.title || name;
    if (app.window && app.window.grow) w.classList.add("grow");
    const fr = w.querySelector(".app-frame");
    if (app.window && app.window.height) fr.style.height = app.window.height + "px";
    const tok = tokenInput.value ? "?token=" + encodeURIComponent(tokenInput.value) : "";
    fr.src = "/apps/" + encodeURIComponent(name) + "/" + tok;
    $("#desktop").append(w);
    initWindow(w);
    // the app's grow box lives inside its iframe (postMessage bridge), so
    // the edge grips are wired here, with the bridge's own clamps
    wireEdges(w, fr, 240, { minH: 120, hPad: 120 });
  }
  openWin(w);
}
// app windows: an OS 9 grow box inside the app's iframe streams pointer
// deltas up via postMessage (the app can't reach its own window chrome);
// the desktop resizes the Platinum window and iframe around it
let appGrow = null; // { win, frame, w, h } captured at grow-start
window.addEventListener("message", e => {
  if (e.origin !== location.origin || !e.data || typeof e.data.exe !== "string") return;
  const fr = [...document.querySelectorAll(".app-frame")].find(f => f.contentWindow === e.source);
  if (!fr) return;
  const w = fr.closest(".window");
  if (e.data.exe === "confirm") {
    platAsk(e.data.message || "Are you sure?", { title: e.data.title || "Confirm" }).then(ok => {
      try { e.source.postMessage({ exe: "confirm-result", id: e.data.id, ok: !!ok }, location.origin); } catch (err) {}
    });
    return;
  } else if (e.data.exe === "prompt") {
    platAsk(e.data.message || "", { title: e.data.title || "Name", paste: true, ok: "OK" }).then(val => {
      try { e.source.postMessage({ exe: "prompt-result", id: e.data.id, value: typeof val === "string" ? val : "" }, location.origin); } catch (err) {}
    });
    return;
  } else if (e.data.exe === "focus") {
    // clicks inside the iframe never reach the window's own mousedown
    // handler, so apps raise their window through the bridge
    focusWin(w);
  } else if (e.data.exe === "grow-start" && !IS_MOBILE) {
    appGrow = { win: w, frame: fr, w: w.offsetWidth, h: fr.offsetHeight };
    winDragging = w;
    document.body.classList.add("win-drag");
    focusWin(w);
  } else if (e.data.exe === "grow" && appGrow && appGrow.win === w) {
    w.style.width = Math.max(240, Math.min(window.innerWidth - 40, appGrow.w + (+e.data.dx || 0))) + "px";
    fr.style.height = Math.max(120, Math.min(window.innerHeight - 120, appGrow.h + (+e.data.dy || 0))) + "px";
    winSaveLive();
  } else if (e.data.exe === "grow-end" && appGrow && appGrow.win === w) {
    appGrow = null;
    winDragging = null;
    document.body.classList.remove("win-drag");
    winSave();
  }
});

// ---- Workspace Finder: spatial windows over ~/.exe/workspace ----
// Like the real OS 9 Finder, every folder opens its OWN window (no "up"
// navigation), created on demand with id win-ws-<encoded path> so the
// layout snapshot restores each folder window's geometry — spatial Finder
// remembered per-folder window positions, and the snapshot gives us that.
const fmtBytes = n => n >= 2 ** 30 ? (n / 2 ** 30).toFixed(1) + " GB"
  : n >= 2 ** 20 ? (n / 2 ** 20).toFixed(1) + " MB" : Math.max(1, Math.round(n / 1024)) + " K";
const GROW_TILE = `<svg class="grow-box" width="15" height="15" viewBox="0 0 15 15" shape-rendering="crispEdges">
  <rect width="15" height="15" fill="#ccc"/>
  <path fill="#000" d="M0 0h15v1H0zM0 1h1v14H0z"/>
  <path fill="#fff" d="M1 1h14v1H1zM1 2h1v13H1z"/>
  <path fill="#fff" d="M9 4h2v1H9zM8 5h1v1H8zM7 6h1v1H7zM6 7h1v1H6zM5 8h1v1H5zM4 9h1v1H4zM11 6h2v1h-2zM10 7h1v1h-1zM9 8h1v1H9zM8 9h1v1H8zM7 10h1v1H7zM6 11h1v1H6zM13 8h2v1h-2zM12 9h1v1h-1zM11 10h1v1h-1zM10 11h1v1h-1zM9 12h1v1H9zM8 13h1v1H8z"/>
  <path fill="#777" d="M10 5h1v1h-1zM9 6h1v1H9zM8 7h1v1H8zM7 8h1v1H7zM6 9h1v1H6zM5 10h1v1H5zM12 7h1v1h-1zM11 8h1v1h-1zM10 9h1v1h-1zM9 10h1v1H9zM8 11h1v1H8zM7 12h1v1H7zM14 9h1v1h-1zM13 10h1v1h-1zM12 11h1v1h-1zM11 12h1v1h-1zM10 13h1v1h-1zM9 14h1v1H9z"/>
  <path fill="#aaa" d="M4 10h1v1H4zM6 12h1v1H6zM8 14h1v1H8z"/>
</svg>`;
function openFinderWin(path, fromWin) {
  const id = "win-ws-" + encodeURIComponent(path);
  let w = document.getElementById(id);
  if (!w) {
    w = el("div", { class: "window grow finder-window", id });
    // a folder opened from a parent window appears offset from it, OS 9 style
    if (fromWin) {
      w.style.left = (parseInt(fromWin.style.left) || 130) + 24 + "px";
      w.style.top = (parseInt(fromWin.style.top) || 60) + 24 + "px";
    } else {
      const n = document.querySelectorAll("#desktop .finder-window").length;
      w.style.left = (130 + n * 26) + "px";
      w.style.top = (60 + n * 22) + "px";
    }
    w.style.width = "560px";
    w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
      <div class="title"></div>
      <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
      <div class="win-frame">
        <div class="finder-status"><span class="ws-items">…</span></div>
        <div class="finder-toolbar"></div>
        <div class="workspace-tree doc-scroll" role="tree"></div>
        ${GROW_TILE}
      </div>`;
    w.querySelector(".title").textContent = path ? path.split("/").pop() : "Workspace";
    w.dataset.path = path;
    const box = w.querySelector(".workspace-tree");
    box.addEventListener("click", e => {
      if (e.target === box) box.querySelectorAll(".tree-row").forEach(d => d.classList.remove("sel"));
    });
    // white-area menu: tree rows run their own contextmenu handler and stop
    // propagation, so anything that reaches the box is empty space
    box.addEventListener("contextmenu", e => {
      e.preventDefault();
      showCtxMenu(e.clientX, e.clientY, [
        { label: "New Folder…", act: () => nfOpen(w, true, w.dataset.path) },
        { label: "New Text File…", act: () => nfOpen(w, false, w.dataset.path) },
        "-",
        { label: "Upload…", act: () => nfPickUpload(w.dataset.path) },
      ]);
    });
    wireGrow(w.querySelector(".grow-box"), w, box, 320);
    wireFinderDrop(w);
    $("#desktop").append(w);
    initWindow(w);
    if (IS_MOBILE) renderFinderToolbar(w);
  }
  openWin(w);
  loadFinder(w);
}
const wsJoin = (dir, name) => dir ? dir + "/" + name : name;
const wsParent = rel => rel.split("/").slice(0, -1).join("/");
function selectTreeRow(tree, row) {
  tree.querySelectorAll(".tree-row.sel").forEach(d => d.classList.remove("sel"));
  row.classList.add("sel");
}
async function loadFinder(w) {
  const path = w.dataset.path, items = w.querySelector(".ws-items");
  let data;
  try { data = await j("/v1/workspace?dir=" + encodeURIComponent(path)); }
  catch (e) { items.textContent = e.message; return; }
  const n = data.entries.length;
  items.textContent = `${n} item${n === 1 ? "" : "s"}, ${fmtBytes(data.free)} available`;
  w._entries = data.entries; // Duplicate consults this for copy-name collisions
  const box = w.querySelector(".workspace-tree");
  box.replaceChildren();
  if (!data.entries.length) {
    box.append(el("div", { class: "tree-empty muted" }, "Empty folder"));
    return;
  }
  for (const en of data.entries) box.append(renderTreeNode(w, path, en, 0));
}
function renderTreeNode(w, base, en, depth) {
  const rel = wsJoin(base, en.name);
  const node = el("div", { class: "tree-node " + (en.dir ? "dir" : "file"), title: en.name });
  node.dataset.path = rel;
  node.dataset.dir = en.dir ? "1" : "0";
  const row = el("div", { class: "tree-row", role: "treeitem", tabindex: "0" });
  row.dataset.path = rel;
  row.dataset.dir = en.dir ? "1" : "0";
  row.dataset.dropDir = en.dir ? rel : wsParent(rel);
  row.style.paddingLeft = (8 + depth * 16) + "px";
  const twist = el("span", { class: "tree-twist" }, en.dir ? "▸" : "");
  const ico = el("span", { class: "tree-ico" });
  ico.innerHTML = fileIcon(en);
  row.append(twist, ico, el("span", { class: "tree-name" }, en.name));
  node.append(row);
  if (en.dir) node.append(el("div", { class: "tree-children", hidden: "" }));
  row.addEventListener("click", e => {
    e.stopPropagation();
    selectTreeRow(w.querySelector(".workspace-tree"), row);
    if (IS_MOBILE) renderFinderToolbar(w);
  });
  row.addEventListener("keydown", e => {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    en.dir ? toggleTreeNode(w, node, rel, depth + 1) : openEntry(en, rel, w);
  });
  objectOpen(row, () => en.dir ? toggleTreeNode(w, node, rel, depth + 1) : openEntry(en, rel, w));
  twist.addEventListener("click", e => {
    if (!en.dir) return;
    e.preventDefault(); e.stopPropagation();
    selectTreeRow(w.querySelector(".workspace-tree"), row);
    toggleTreeNode(w, node, rel, depth + 1);
  });
  row.addEventListener("contextmenu", e => {
    e.preventDefault(); e.stopPropagation();
    selectTreeRow(w.querySelector(".workspace-tree"), row);
    showTreeCtxMenu(e.clientX, e.clientY, w, en, rel, node, depth);
    if (IS_MOBILE) renderFinderToolbar(w);
  });
  addLongPressMenu(row, (x, y) => {
    selectTreeRow(w.querySelector(".workspace-tree"), row);
    showTreeCtxMenu(x, y, w, en, rel, node, depth);
    renderFinderToolbar(w);
  });
  return node;
}
async function toggleTreeNode(w, node, rel, depth) {
  if (!node || node.dataset.dir !== "1") return;
  const kids = node.querySelector(":scope > .tree-children");
  const twist = node.querySelector(":scope > .tree-row > .tree-twist");
  if (node.classList.contains("expanded")) {
    node.classList.remove("expanded");
    kids.hidden = true;
    if (twist) twist.textContent = "▸";
    return;
  }
  if (!node.dataset.loaded) {
    kids.replaceChildren(el("div", { class: "tree-empty muted" }, "Loading…"));
    let data;
    try { data = await j("/v1/workspace?dir=" + encodeURIComponent(rel)); }
    catch (e) { kids.replaceChildren(el("div", { class: "tree-empty muted" }, e.message)); return; }
    kids.replaceChildren();
    if (!data.entries.length) kids.append(el("div", { class: "tree-empty muted" }, "Empty folder"));
    for (const en of data.entries) kids.append(renderTreeNode(w, rel, en, depth));
    node.dataset.loaded = "1";
  }
  node.classList.add("expanded");
  kids.hidden = false;
  if (twist) twist.textContent = "▾";
}
function showTreeCtxMenu(x, y, w, en, rel, node, depth) {
  const targetDir = en.dir ? rel : wsParent(rel);
  showCtxMenu(x, y, [
    { label: "Open", act: () => en.dir ? toggleTreeNode(w, node, rel, depth + 1) : openEntry(en, rel, w) },
    { label: "Edit with Editor", act: () => openEditorWin(rel, w), dis: !!en.dir },
    { label: "Move To Trash", act: () => wsTrash(rel, w) },
    "-",
    { label: "New Folder…", act: () => nfOpen(w, true, targetDir) },
    { label: "New Text File…", act: () => nfOpen(w, false, targetDir) },
    { label: "Upload…", act: () => nfPickUpload(targetDir) },
    "-",
    { label: "Get Info", act: () => showGetInfo(en, rel) },
    { label: "Duplicate", act: () => wsDuplicate(en, rel, w), dis: !!en.dir },
    { label: "Download", act: () => wsDownload(en, rel), dis: !!en.dir },
  ]);
}
function selectedFinderEntry(w) {
  const row = w.querySelector(".workspace-tree .tree-row.sel");
  if (!row) return null;
  const path = row.dataset.path;
  const en = (w._entries || []).find(e => wsJoin(w.dataset.path, e.name) === path);
  return en ? { row, en, rel: path } : null;
}
function renderFinderToolbar(w) {
  const bar = w.querySelector(".finder-toolbar");
  if (!bar) return;
  const base = w.dataset.path;
  const sel = selectedFinderEntry(w);
  const selDir = sel ? (sel.en.dir ? sel.rel : wsParent(sel.rel)) : base;
  const dirOnly = !sel || sel.en.dir;
  const btn = (label, dis, act) => el("button", { class: "ghost sm" + (dis ? " dis" : ""), onclick: dis ? null : act }, label);
  bar.replaceChildren(
    btn("New Folder", false, () => nfOpen(w, true, base)),
    btn("New Text", false, () => nfOpen(w, false, base)),
    btn("Upload", false, () => nfPickUpload(base)),
    btn("Info", !sel, () => showGetInfo(sel.en, sel.rel)),
    btn("Delete", !sel, () => wsTrash(sel.rel, w)),
    btn("Duplicate", !sel || dirOnly, () => wsDuplicate(sel.en, sel.rel, w)),
    btn("Download", !sel || dirOnly, () => wsDownload(sel.en, sel.rel)),
  );
}
function openEntry(en, rel, w) {
  if (en.dir) openFinderWin(rel, w);
  else if (isImageFile(en.name)) openImageWin(rel, w);
  else if (isTextFile(en.name) && en.size < 1 << 20) openEditorWin(rel, w);
  else openWorkspaceFile(rel);
}
const wsUrl = rel => "/v1/workspace/" + rel.split("/").map(encodeURIComponent).join("/");
// open a file in a new tab via an authed fetch — the token never lands in a URL
async function openWorkspaceFile(rel) {
  try {
    const r = await api(wsUrl(rel));
    const u = URL.createObjectURL(await r.blob());
    window.open(u);
    setTimeout(() => URL.revokeObjectURL(u), 60000);
  } catch (e) {}
}

// ---- Finder contextual menu, styled from a real OS 9 capture ----
const ctxMenu = $("#ctx-menu");
function hideCtxMenu() {
  if (ctxMenu.hidden) return;
  ctxMenu.hidden = true;
  const f = ctxMenu._onclose;
  ctxMenu._onclose = null;
  if (f) f();
}
function showCtxMenu(x, y, items, onclose) {
  hideCtxMenu();
  ctxMenu._onclose = onclose || null;
  ctxMenu.replaceChildren();
  for (const it of items) {
    if (it === "-") { ctxMenu.append(el("div", { class: "dd-sep" })); continue; }
    const d = el("div", { class: "dd-item" + (it.dis ? " dis" : "") }, it.label);
    if (!it.dis) d.addEventListener("click", () => { hideCtxMenu(); it.act(); });
    ctxMenu.append(d);
  }
  ctxMenu.hidden = false;
  const r = ctxMenu.getBoundingClientRect();
  ctxMenu.style.left = Math.min(x, window.innerWidth - r.width - 4) + "px";
  ctxMenu.style.top = Math.min(y, window.innerHeight - r.height - 4) + "px";
}
document.addEventListener("mousedown", e => { if (!e.target.closest("#ctx-menu")) hideCtxMenu(); });
document.addEventListener("keydown", e => { if (e.key === "Escape") hideCtxMenu(); });

// Move To Trash parks the item in the dot-hidden .Trash folder (the move
// endpoint creates it); a timestamp prefix keeps trashed names unique
function refreshFinderWindows() {
  document.querySelectorAll('[id^="win-ws-"]').forEach(f => { if (!f.hidden) loadFinder(f); });
}
async function wsTrash(rel, w) {
  if (!await platAsk("Move “" + rel.split("/").pop() + "” to Trash?", { title: "Move to Trash" })) return;
  try {
    await api(wsUrl(rel), { method: "POST", body: JSON.stringify({ to: ".Trash/" + Date.now() + "-" + rel.split("/").pop() }) });
  } catch (e) {}
  refreshFinderWindows();
}
// Download fetches with the auth header, then hands the blob to an <a download>
async function wsDownload(en, rel) {
  try {
    const u = URL.createObjectURL(await (await api(wsUrl(rel))).blob());
    el("a", { href: u, download: en.name }).click();
    setTimeout(() => URL.revokeObjectURL(u), 60000);
  } catch (e) {}
}
async function wsDuplicate(en, rel, w) {
  try {
    const blob = await (await api(wsUrl(rel))).blob();
    const dot = en.name.lastIndexOf(".");
    const base = dot > 0 ? en.name.slice(0, dot) : en.name;
    const ext = dot > 0 ? en.name.slice(dot) : "";
    let cp = base + " copy" + ext, n = 1;
    const dir = rel.split("/").slice(0, -1).join("/");
    let entries = dir === w.dataset.path ? (w._entries || []) : [];
    if (dir !== w.dataset.path) entries = (await j("/v1/workspace?dir=" + encodeURIComponent(dir))).entries || [];
    while (entries.some(x => x.name === cp)) cp = base + " copy " + ++n + ext;
    await api(wsUrl(dir ? dir + "/" + cp : cp), { method: "PUT", body: blob });
  } catch (e) {}
  refreshFinderWindows();
}
// ---- New Folder / New Text File: name prompt over a Finder window ----
let nfWin = null, nfFolder = false, nfDir = "";
function nfOpen(w, folder, dir) {
  nfWin = w; nfFolder = folder; nfDir = dir || "";
  $("#nf-title").textContent = folder ? "New Folder" : "New Text File";
  const inp = $("#nf-name");
  inp.value = folder ? "untitled folder" : "untitled.txt";
  $("#nf-overlay").hidden = false;
  inp.focus();
  // select up to the extension, so typing replaces the name but keeps .txt
  const dot = inp.value.lastIndexOf(".");
  inp.setSelectionRange(0, dot > 0 ? dot : inp.value.length);
}
function nfClose() { $("#nf-overlay").hidden = true; }
$("#nf-x").onclick = nfClose;
$("#nf-cancel").onclick = nfClose;
$("#nf-name").addEventListener("keydown", e => { if (e.key === "Enter") $("#nf-go").click(); });
$("#nf-go").onclick = async () => {
  const name = $("#nf-name").value.trim();
  if (!name) { toast("name required"); return; }
  // slashes would nest, dot-prefixed names vanish from the listing
  if (name.includes("/") || name.startsWith(".")) { toast("that name won't work"); return; }
  const targetDir = nfDir || "";
  let entries = [];
  try { entries = (await j("/v1/workspace?dir=" + encodeURIComponent(targetDir))).entries || []; }
  catch (e) { toast(e.message); return; }
  if (entries.some(x => x.name === name)) { toast(`“${name}” is already taken`); return; }
  const rel = targetDir ? targetDir + "/" + name : name;
  $("#nf-go").disabled = true;
  try {
    if (nfFolder) await api(wsUrl(rel) + "?mkdir=1", { method: "POST" });
    else await api(wsUrl(rel), { method: "PUT", body: "" });
    nfClose();
    refreshFinderWindows();
  } catch (e) { toast(e.message); }
  $("#nf-go").disabled = false;
};
// Upload… aims a throwaway file input at the window's folder
const nfUpInput = el("input", { type: "file", multiple: "" });
nfUpInput.onchange = () => { wsUpload(nfUpInput._dir, [...nfUpInput.files]); nfUpInput.value = ""; };
function nfPickUpload(dir) { nfUpInput._dir = dir; nfUpInput.click(); }

function showGetInfo(en, rel) {
  $("#win-getinfo .title").textContent = en.name + " Info";
  $("#gi-icon").innerHTML = fileIcon(en);
  $("#gi-name").textContent = en.name;
  $("#gi-kind").textContent = en.dir ? "folder" : "document";
  $("#gi-size").textContent = en.dir ? "—" : `${fmtBytes(en.size || 0)} (${(en.size || 0).toLocaleString()} bytes)`;
  const dir = rel.split("/").slice(0, -1).join("/");
  $("#gi-where").textContent = "Workspace" + (dir ? " :" + dir.split("/").join(" :") : "");
  $("#gi-mod").textContent = edFmtDate(en.modified);
  const dl = $("#gi-download");
  dl.disabled = !!en.dir;
  dl.onclick = () => wsDownload(en, rel); // singleton dialog: reassign, don't stack listeners
  openWin("#win-getinfo");
}

// ---- text editor windows, modeled on BBEdit 2.1.3 ----
// Text files double-clicked in a Finder window open here, one spatial
// window per file (id win-ed-<encoded path>, geometry in the snapshot).
// Edits autosave (debounced PUT) and refresh the Last Saved line.
const ED_EXT = new Set(("txt md markdown json js mjs ts tsx css html htm xml svg sh bash zsh py rb go rs c h " +
  "cpp hpp java kt swift yaml yml toml ini cfg conf log csv tsv sql env gitignore service").split(" "));
const ED_NAMES = new Set(["README", "LICENSE", "Makefile", "Dockerfile", "CHANGELOG", "TODO"]);
function isTextFile(name) {
  if (ED_NAMES.has(name)) return true;
  const dot = name.lastIndexOf(".");
  return dot > 0 && ED_EXT.has(name.slice(dot + 1).toLowerCase());
}
// small b/w pixel icons for the info bar: pencil, mini document, marker
const ED_PENCIL = `<svg width="16" height="16" viewBox="0 0 16 16" shape-rendering="crispEdges"><path fill="#000" d="M10 1h2v1h-2zM9 2h1v1H9zM12 2h1v1h-1zM8 3h1v1H8zM10 3h1v1h-1zM13 3h1v1h-1zM7 4h1v1H7zM11 4h1v1h-1zM13 4h1v1h-1zM6 5h1v1H6zM12 5h1v1h-1zM5 6h1v1H5zM11 6h1v1h-1zM4 7h1v1H4zM10 7h1v1h-1zM3 8h1v1H3zM9 8h1v1H9zM3 9h1v1H3zM8 9h1v1H8zM2 10h1v1H2zM7 10h1v1H7zM2 11h2v1H2zM5 11h2v1H5zM2 12h1v1H2z"/></svg>`;
const ED_DOC = `<svg width="16" height="16" viewBox="0 0 16 16" shape-rendering="crispEdges"><path fill="#fff" stroke="#000" d="M3.5 1.5h6l3 3v10h-9z"/><path fill="#ccc" stroke="#000" d="M9.5 1.5v3h3z"/></svg>`;
const ED_MARK = `<svg width="16" height="16" viewBox="0 0 16 16" shape-rendering="crispEdges"><path d="M8 4.5 L11.5 8 L8 11.5 L4.5 8 Z" fill="#eee" stroke="#000"/></svg>`;
const edFmtDate = d => new Date(d).toLocaleString("en-US",
  { month: "numeric", day: "numeric", year: "2-digit", hour: "numeric", minute: "2-digit", second: "2-digit" }).replace(",", "");
function openEditorWin(path, fromWin) {
  const id = "win-ed-" + encodeURIComponent(path);
  let w = document.getElementById(id);
  if (!w) {
    w = el("div", { class: "window grow editor-window", id });
    if (fromWin) {
      w.style.left = (parseInt(fromWin.style.left) || 130) + 24 + "px";
      w.style.top = (parseInt(fromWin.style.top) || 60) + 24 + "px";
    } else {
      const n = document.querySelectorAll("#desktop .editor-window").length;
      w.style.left = (150 + n * 26) + "px";
      w.style.top = (80 + n * 22) + "px";
    }
    w.style.width = "620px";
    w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
      <div class="title"></div>
      <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
      <div class="win-frame">
        <div class="ed-info">
          ${ED_PENCIL}<div class="ed-main">${ED_DOC}<span class="ed-path"></span></div>
          ${ED_MARK}<span class="ed-saved">…</span>
          <div class="ed-tools"><button class="ghost sm danger ed-delete" type="button">Delete</button></div>
        </div>
        <textarea class="ed-text doc-scroll" wrap="soft" spellcheck="false" autocomplete="off"></textarea>
        ${GROW_TILE}
      </div>`;
    w.querySelector(".title").textContent = path.split("/").pop();
    // classic Mac colon path, with the workspace as the volume
    w.querySelector(".ed-path").textContent = "Workspace :" + path.split("/").join(" :");
    w.dataset.path = path;
    const ta = w.querySelector(".ed-text");
    let saveT = null;
    w.querySelector(".ed-delete").addEventListener("click", async () => {
      const curPath = w.dataset.path;
      if (!curPath) return;
      if (!await platAsk("Move “" + curPath.split("/").pop() + "” to Trash?", { title: "Move to Trash", ok: "Move" })) return;
      clearTimeout(saveT);
      try {
        await api(wsUrl(curPath), { method: "POST", body: JSON.stringify({ to: ".Trash/" + Date.now() + "-" + curPath.split("/").pop() }) });
        delete w.dataset.dirty;
        closeWin(w);
        refreshFinderWindows();
        toast("Moved to Trash " + curPath);
      } catch (e) {
        w.querySelector(".ed-saved").textContent = "Move failed: " + e.message;
      }
    });
    ta.addEventListener("input", () => {
      w.dataset.dirty = "1";
      clearTimeout(saveT);
      saveT = setTimeout(async () => {
        try {
          await api("/v1/workspace/" + path.split("/").map(encodeURIComponent).join("/"),
            { method: "PUT", body: ta.value });
          delete w.dataset.dirty;
          w.querySelector(".ed-saved").textContent = "Last Saved: " + edFmtDate(Date.now());
        } catch (e) {
          w.querySelector(".ed-saved").textContent = "Save failed: " + e.message;
        }
      }, 400);
    });
    wireGrow(w.querySelector(".grow-box"), w, ta, 360);
    $("#desktop").append(w);
    initWindow(w);
  }
  openWin(w);
  loadEditor(w);
}
async function loadEditor(w) {
  if (w.dataset.dirty) return; // unsaved edits in flight — don't clobber
  const path = w.dataset.path, ta = w.querySelector(".ed-text");
  try {
    const r = await api("/v1/workspace/" + path.split("/").map(encodeURIComponent).join("/"));
    ta.value = await r.text();
  } catch (e) {
    w.querySelector(".ed-saved").textContent = e.message;
    if (/404|not found/i.test(e.message || "")) {
      closeWin(w);
      w.remove();
      winSave();
    }
    return;
  }
  // Last Saved comes from the parent folder's listing, so restores work too
  try {
    const dir = path.split("/").slice(0, -1).join("/");
    const data = await j("/v1/workspace?dir=" + encodeURIComponent(dir));
    const en = data.entries.find(x => x.name === path.split("/").pop());
    if (en) w.querySelector(".ed-saved").textContent = "Last Saved: " + edFmtDate(en.modified);
  } catch (e) {}
}
// ---- image viewer windows, modeled on PictureViewer ----
// Pictures double-clicked in a Finder window open here instead of a browser
// tab, one spatial window per file (id win-iv-<encoded path>, geometry in
// the snapshot). SVG stays with the text editor: it's markup.
const IMG_EXT = new Set("png jpg jpeg gif webp bmp ico avif".split(" "));
function isImageFile(name) {
  const dot = name.lastIndexOf(".");
  return dot > 0 && IMG_EXT.has(name.slice(dot + 1).toLowerCase());
}
const ARC_EXT = new Set("zip dmg rar 7z tar gz tgz bz2 xz".split(" "));
function isArchiveFile(name) {
  const dot = name.lastIndexOf(".");
  return dot > 0 && ARC_EXT.has(name.slice(dot + 1).toLowerCase());
}
function openImageWin(path, fromWin) {
  const id = "win-iv-" + encodeURIComponent(path);
  let w = document.getElementById(id);
  if (!w) {
    w = el("div", { class: "window grow image-window", id });
    if (fromWin) {
      w.style.left = (parseInt(fromWin.style.left) || 130) + 24 + "px";
      w.style.top = (parseInt(fromWin.style.top) || 60) + 24 + "px";
      // a user-opened window sizes itself to the picture once it loads;
      // snapshot restores (no fromWin) keep their saved geometry instead
      w.dataset.fit = "1";
    } else {
      const n = document.querySelectorAll("#desktop .image-window").length;
      w.style.left = (170 + n * 26) + "px";
      w.style.top = (90 + n * 22) + "px";
    }
    w.style.width = "520px";
    w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
      <div class="title"></div>
      <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
      <div class="win-frame">
        <div class="iv-info"><span class="iv-path"></span><span class="iv-dims">…</span></div>
        <div class="iv-box"></div>
        ${GROW_TILE}
      </div>`;
    w.querySelector(".title").textContent = path.split("/").pop();
    w.querySelector(".iv-path").textContent = "Workspace :" + path.split("/").join(" :");
    w.dataset.path = path;
    wireGrow(w.querySelector(".grow-box"), w, w.querySelector(".iv-box"), 240, { minH: 120 });
    $("#desktop").append(w);
    initWindow(w);
  }
  openWin(w);
  loadImage(w);
}
async function loadImage(w) {
  const path = w.dataset.path, box = w.querySelector(".iv-box"), dims = w.querySelector(".iv-dims");
  try {
    const r = await api(wsUrl(path));
    const blob = await r.blob();
    if (w._imgURL) URL.revokeObjectURL(w._imgURL); // reopen refetches — drop the old blob
    w._imgURL = URL.createObjectURL(blob);
    const img = el("img", { alt: path.split("/").pop() });
    img.onload = () => {
      dims.textContent = `${img.naturalWidth} × ${img.naturalHeight} · ${fmtBytes(blob.size)}`;
      if (w.dataset.fit) {
        // PictureViewer opened windows at picture size: scale the picture
        // to the viewport if needed and hug the scaled size, so the window
        // never letterboxes (+10 is the frame inset)
        delete w.dataset.fit;
        if (!IS_MOBILE) {
          const scale = Math.min((window.innerWidth - 90) / img.naturalWidth,
            (window.innerHeight - 200) / img.naturalHeight, 1);
          w.style.width = Math.max(240, Math.round(img.naturalWidth * scale) + 10) + "px";
          box.style.height = Math.max(120, Math.round(img.naturalHeight * scale)) + "px";
          winSave();
        }
      }
    };
    img.src = w._imgURL;
    box.replaceChildren(img);
  } catch (e) { dims.textContent = e.message; }
}
$("#desktop").addEventListener("mousedown", e => {
  if (e.target === $("#desktop") || e.target === $("#icons") || e.target === $("#desktop-bg")) {
    selectedIcon = null;
    document.querySelectorAll(".dicon").forEach(d => d.classList.remove("sel"));
  }
});
$("#desktop").addEventListener("contextmenu", e => {
  if (e.target !== $("#desktop") && e.target !== $("#icons") && e.target !== $("#desktop-bg")) return;
  e.preventDefault();
  showCtxMenu(e.clientX, e.clientY, [{ label: "Desktop Background…", act: () => openDesktopWin() }]);
});
const trashIcon = $("#trash-icon");
trashIcon.addEventListener("click", e => { e.stopPropagation(); trashIcon.classList.add("sel"); selectedIcon = null; });
objectOpen(trashIcon, () => openWin("#win-trash"));
document.addEventListener("mousedown", e => { if (!e.target.closest("#trash-icon")) trashIcon.classList.remove("sel"); });
const chatIcon = $("#chat-icon");
chatIcon.addEventListener("click", e => { e.stopPropagation(); chatIcon.classList.add("sel"); selectedIcon = null; });
objectOpen(chatIcon, () => openChatWin());
document.addEventListener("mousedown", e => { if (!e.target.closest("#chat-icon")) chatIcon.classList.remove("sel"); });
const newsIcon = $("#news-icon");
newsIcon.addEventListener("click", e => { e.stopPropagation(); newsIcon.classList.add("sel"); selectedIcon = null; });
objectOpen(newsIcon, () => openNewsWin());
document.addEventListener("mousedown", e => { if (!e.target.closest("#news-icon")) newsIcon.classList.remove("sel"); });

// OS 9 scrollbar end-merge: thumbs render with the leading border merged into
// the frame (the unscrolled default); flag elements once they scroll away so
// CSS can restore the thumb's own black border. Capture phase — scroll
// events don't bubble from overflow elements.
document.addEventListener("scroll", e => {
  const el = e.target;
  if (!(el instanceof Element)) return;
  el.classList.toggle("scrolled-y", el.scrollTop >= 1);
  el.classList.toggle("scrolled-x", el.scrollLeft >= 1);
  el.classList.toggle("at-y-end", el.scrollTop >= 1 && el.scrollTop >= el.scrollHeight - el.clientHeight - 1);
  el.classList.toggle("at-x-end", el.scrollLeft >= 1 && el.scrollLeft >= el.scrollWidth - el.clientWidth - 1);
}, { capture: true, passive: true });

// ---- vm list ----
let currentVM = null;
let lastVMs = [];
function chip(state) { return el("span", { class: "chip " + state }, state); }

// column sorting, OS 9 style: click a header to sort by it (again to flip),
// or flip with the direction widget at the right end of the header bar
const ipNum = ip => (ip || "").split(".").reduce((a, o) => a * 256 + (+o || 0), 0);
const VM_COLS = [
  { key: "name",  label: "Name",  cmp: (a, b) => a.name.localeCompare(b.name) },
  { key: "state", label: "State", cmp: (a, b) => a.state.localeCompare(b.state) },
  { key: "specs", label: "Specs", cmp: (a, b) => (a.cpus - b.cpus) || (a.memory_mb - b.memory_mb) || (a.disk_gb - b.disk_gb) },
  { key: "ip",    label: "IP",    cmp: (a, b) => ipNum(a.ip) - ipNum(b.ip) },
];
let vmSort = { key: "name", dir: 1 };
try { vmSort = { ...vmSort, ...JSON.parse(localStorage.getItem("exe_vmsort") || "{}") } } catch (e) {}
function setVMSort(key, dir) {
  vmSort = { key, dir };
  localStorage.setItem("exe_vmsort", JSON.stringify(vmSort));
  renderVMHead();
  renderVMList(lastVMs);
}
function renderVMHead() {
  const tr = $("#vm-head");
  tr.replaceChildren();
  for (const c of VM_COLS) {
    const th = el("th", {
      class: c.key === vmSort.key ? "sorted" : "",
      onclick: () => setVMSort(c.key, c.key === vmSort.key ? -vmSort.dir : 1),
    }, c.label);
    // every header carries an arrow, invisible on unsorted columns — the
    // reserved space keeps column widths stable when the sort moves
    const active = c.key === vmSort.key;
    th.append(el("span", { class: "sortarrow" + (active ? "" : " ph") }, active && vmSort.dir === -1 ? "▼" : "▲"));
    tr.append(th);
  }
  tr.append(el("th", {})); // actions
}
function sortedVMs(vms) {
  const col = VM_COLS.find(c => c.key === vmSort.key) || VM_COLS[0];
  return [...vms].sort((a, b) => (col.cmp(a, b) || a.name.localeCompare(b.name)) * vmSort.dir);
}

async function loadVMs() {
  renderVMList(await j("/v1/vms"));
}
function renderVMList(rawVMs) {
  lastVMs = rawVMs;
  const vms = sortedVMs(rawVMs);
  // skip the re-render when nothing changed — a rebuild mid-double-click
  // splits the two clicks across DOM generations and swallows the dblclick
  const sig = JSON.stringify([vmSort, vms]);
  if (sig === renderVMList._last) return;
  renderVMList._last = sig;
  const tb = $("#vm-rows");
  tb.replaceChildren();
  $("#vm-empty").hidden = vms.length > 0;
  $("#vm-count").textContent = vms.length + (vms.length === 1 ? " item" : " items");
  renderVMPanel(vms);
  renderIcons(vms);
  for (const vm of vms) {
    const actions = el("td", { class: "actions" });
    if (vm.state === "running") {
      actions.append(el("button", { class: "ghost sm", onclick: e => { e.stopPropagation(); vmAction(vm.name, "stop"); } }, "Stop"));
    } else {
      actions.append(el("button", { class: "ghost sm", onclick: e => { e.stopPropagation(); vmAction(vm.name, "start"); } }, "Start"));
    }
    const del = el("button", { class: "ghost sm danger" }, "Delete");
    del.onclick = async e => {
      e.stopPropagation();
      if (!await platAsk("Delete VM “" + vm.name + "” and its disk? This cannot be undone.", { title: "Delete VM" })) return;
      try { await api("/v1/vms/" + vm.name, { method: "DELETE" }); toast(vm.name + " deleted"); loadVMs(); }
      catch (err) { toast(err.message); }
    };
    actions.append(del);
    const cells = [
      el("td", { class: "mono" }, vm.name),
      el("td", {}, chip(vm.state)),
      el("td", { class: "muted" }, `${vm.cpus} cpu · ${vm.memory_mb} MB · ${vm.disk_gb} GB`),
      el("td", { class: "mono" }, vm.ip || "—"),
    ];
    const sc = VM_COLS.findIndex(c => c.key === vmSort.key);
    if (cells[sc]) cells[sc].classList.add("sortcol");
    const row = el("tr", { class: "vm-row" }, ...cells, actions);
    objectOpen(row, () => openVM(vm.name));
    // desktop: right-click context menu; mobile: long-press context menu.
    const vmCtx = (x, y) => {
      row.classList.add("ctx");
      const running = vm.state === "running";
      showCtxMenu(x, y, [
        { label: "Open", act: () => openVM(vm.name) },
        { label: "Open Terminal", act: () => openVM(vm.name, "term") },
        "-",
        running ? { label: "Stop", act: () => vmAction(vm.name, "stop") }
                : { label: "Start", act: () => vmAction(vm.name, "start") },
        { label: "Restart", act: () => vmRestart(vm.name), dis: !running },
        "-",
        { label: "Copy IP", act: () => copyIP(vm.ip), dis: !vm.ip },
        { label: "Expose Port…", act: () => openVM(vm.name, "expose") },
        { label: "Publish to GitHub…", act: () => openPublishWin(vm.name), dis: !running },
        "-",
        { label: "Delete", act: () => del.click() },
      ], () => row.classList.remove("ctx"));
    };
    if (!IS_MOBILE) row.addEventListener("contextmenu", e => {
      e.preventDefault();
      vmCtx(e.clientX, e.clientY);
    });

    tb.append(row);
  }
}
function renderVMPanel(vms) {
  const panel = $("#vm-panel");
  if (!panel) return;
  if (!vms.length) { panel.hidden = true; return; }
  panel.hidden = false;
  const counts = { running: 0, stopped: 0, other: 0 };
  for (const vm of vms) {
    if (vm.state === "running") counts.running++;
    else if (vm.state === "stopped") counts.stopped++;
    else counts.other++;
  }
  const anyRunning = counts.running > 0;
  const anyStopped = counts.stopped > 0;
  const stat = (label, n, cls) => el("span", { class: "vm-stats" }, el("span", { class: "muted" }, label), el("span", { class: "chip " + cls }, String(n)));
  const left = el("span", { class: "vm-stats" },
    stat("running", counts.running, "running"),
    stat("stopped", counts.stopped, "stopped"),
    stat("other", counts.other, "stopping"));
  const right = el("span", { class: "vm-stats" });
  if (anyStopped) {
    right.append(el("button", { class: "ghost sm", onclick: () => vmBulkAction(vms.filter(v => v.state === "stopped").map(v => v.name), "start") }, "Start All"));
  }
  if (anyRunning) {
    right.append(el("button", { class: "ghost sm", onclick: () => vmBulkAction(vms.filter(v => v.state === "running").map(v => v.name), "stop") }, "Stop All"));
  }
  panel.replaceChildren(left, right);
}
async function vmBulkAction(names, action) {
  toast(`${action}ing ${names.length} VM${names.length > 1 ? "s" : ""}…`);
  await Promise.all(names.map(n => api(`/v1/vms/${n}/${action}`, { method: "POST", json: {} }).catch(e => toast(`${n}: ${e.message}`))));
  toast(`${action} done`);
  loadVMs();
}
// no restart endpoint: a plain stop → start sequence
async function vmRestart(name) {
  toast(name + ": restart…");
  try {
    await api(`/v1/vms/${name}/stop`, { method: "POST", json: {} });
    await api(`/v1/vms/${name}/start`, { method: "POST", json: {} });
    toast(name + " restarted");
  } catch (e) { toast(e.message); }
  loadVMs();
  if (currentVM === name) refreshDetailHead();
}
async function vmAction(name, action) {
  toast(name + ": " + action + "…");
  try { await api(`/v1/vms/${name}/${action}`, { method: "POST", json: {} }); toast(name + " " + action + " ok"); }
  catch (e) { toast(e.message); }
  loadVMs();
  if (currentVM === name) refreshDetailHead();
}
function fmtRate(n) {
  n = Math.max(0, +n || 0);
  if (n < 1000) return Math.round(n) + " B/s";
  if (n < 1000 * 1000) return (n / 1000).toFixed(n < 10e3 ? 1 : 0) + " KB/s";
  if (n < 1000 * 1000 * 1000) return (n / 1e6).toFixed(n < 10e6 ? 1 : 0) + " MB/s";
  return (n / 1e9).toFixed(1) + " GB/s";
}
function fmtBytesShort(n) {
  n = Math.max(0, +n || 0);
  if (n >= 1e12) return (n / 1e12).toFixed(1) + " TB";
  if (n >= 1e9) return (n / 1e9).toFixed(n >= 10e9 ? 0 : 1) + " GB";
  if (n >= 1e6) return (n / 1e6).toFixed(0) + " MB";
  return Math.round(n / 1024) + " KB";
}
function resMeter(pct, hot) {
  const m = el("span", { class: "res-meter" + (hot ? " hot" : "") });
  const i = el("i");
  i.style.width = Math.max(0, Math.min(100, pct)) + "%";
  m.append(i);
  return m;
}
function renderHostStats(st) {
  if (!st) return;
  const cpu = +st.cpu_pct || 0;
  const dio = (+st.disk_read_bps || 0) + (+st.disk_write_bps || 0);
  const nio = (+st.net_rx_bps || 0) + (+st.net_tx_bps || 0);
  const free = +st.disk_free || 0, tot = +st.disk_total || 0;
  const usedPct = tot > 0 ? (1 - free / tot) * 100 : 0;
  const cpuHot = cpu >= 85, diskHot = tot > 0 && free / tot < 0.1;
  const mb = $("#host-res");
  if (mb) {
    mb.replaceChildren(
      el("span", cpuHot ? { class: "hot" } : {}, "CPU " + cpu.toFixed(0) + "%"),
      el("span", {}, "disk " + fmtRate(dio)),
      el("span", {}, "net " + fmtRate(nio)),
      el("span", diskHot ? { class: "hot" } : {}, fmtBytesShort(free) + " free"));
    mb.title = "Host: CPU " + cpu.toFixed(1) + "% · disk r " + fmtRate(st.disk_read_bps) +
      " w " + fmtRate(st.disk_write_bps) + " · net ↓ " + fmtRate(st.net_rx_bps) +
      " ↑ " + fmtRate(st.net_tx_bps) + " · " + fmtBytesShort(free) + " of " + fmtBytesShort(tot) + " free";
  }
  const panel = $("#host-panel");
  if (panel) {
    const item = (label, val, extra, hot) =>
      el("span", { class: "res-item" + (hot ? " hot" : "") },
        el("span", { class: "lbl" }, label), extra || "", el("span", { class: "val" }, val));
    panel.replaceChildren(
      item("CPU", cpu.toFixed(0) + "%", resMeter(cpu, cpuHot), cpuHot),
      item("Disk I/O", "↓ " + fmtRate(st.disk_read_bps) + "  ↑ " + fmtRate(st.disk_write_bps)),
      item("Net", "↓ " + fmtRate(st.net_rx_bps) + "  ↑ " + fmtRate(st.net_tx_bps)),
      item("Free", fmtBytesShort(free) + (tot ? " / " + fmtBytesShort(tot) : ""),
        tot ? resMeter(usedPct, diskHot) : null, diskHot));
  }
  const bar = $("#vm-res");
  if (bar) {
    const item = (label, val, extra, hot) =>
      el("span", { class: "res-item" + (hot ? " hot" : "") },
        el("span", { class: "lbl" }, label), extra || "", el("span", { class: "val" }, val));
    bar.replaceChildren(
      item("CPU", cpu.toFixed(0) + "%", resMeter(cpu, cpuHot), cpuHot),
      item("Disk I/O", "↓ " + fmtRate(st.disk_read_bps) + "  ↑ " + fmtRate(st.disk_write_bps)),
      item("Net", "↓ " + fmtRate(st.net_rx_bps) + "  ↑ " + fmtRate(st.net_tx_bps)),
      item("Free", fmtBytesShort(free) + (tot ? " / " + fmtBytesShort(tot) : ""),
        tot ? resMeter(usedPct, diskHot) : null, diskHot));
  }
}
async function pollHostStats() {
  try { renderHostStats(await j("/v1/host/stats")); } catch (e) {}
}
async function loadHostProcs() {
  const tb = $("#host-procs");
  if (!tb) return;
  try {
    const procs = await j("/v1/host/procs");
    tb.replaceChildren();
    if (!procs.length) {
      tb.append(el("tr", {}, el("td", { colspan: "4", class: "muted" }, "No process data")));
      $("#host-count").textContent = "0 processes";
      return;
    }
    for (const p of procs) {
      tb.append(el("tr", {},
        el("td", { class: "mono" }, String(p.pid)),
        el("td", {}, (p.cpu || 0).toFixed(1) + "%"),
        el("td", {}, (p.mem || 0).toFixed(1) + "%"),
        el("td", { class: "mono" }, p.cmd || "—")));
    }
    $("#host-count").textContent = procs.length + " process" + (procs.length === 1 ? "" : "es");
  } catch (e) {
    tb.replaceChildren(el("tr", {}, el("td", { colspan: "4", class: "muted" }, e.message)));
  }
}
$("#host-res").onclick = () => { openWin("#win-host"); loadHostProcs(); };
$("#host-refresh").onclick = loadHostProcs;
$("#vms-new").onclick = ACTIONS.newvm;
$("#vms-refresh").onclick = ACTIONS.refresh;
$("#c-cancel").onclick = () => closeWin($("#win-newvm"));
$("#token-done").onclick = () => {
  localStorage.setItem("exe_token", tokenInput.value);
  tokenPromptOpen = false;
  closeWin($("#win-token"));
};
$("#c-go").onclick = async () => {
  const name = $("#c-name").value.trim();
  if (!name) { toast("name required"); return; }
  const body = { name };
  for (const [id, key] of [["#c-cpus", "cpus"], ["#c-mem", "memory_mb"], ["#c-disk", "disk_gb"]]) {
    const v = parseInt($(id).value, 10);
    if (v > 0) body[key] = v;
  }
  $("#c-go").disabled = true;
  $("#c-status").textContent = "creating… (usually ~10s; first ever run downloads a 3 GB image)";
  try {
    await j("/v1/vms", { method: "POST", json: body });
    $("#c-name").value = "";
    toast(name + " is ready");
    closeWin($("#win-newvm"));
  } catch (e) { toast(e.message); }
  $("#c-go").disabled = false;
  $("#c-status").textContent = "";
  loadVMs();
};

// ---- File → Upload to Workspace ----
$("#up-choose").onclick = () => $("#up-file").click();
$("#up-file").onchange = () => {
  const f = $("#up-file").files[0];
  $("#up-name").textContent = f ? f.name + " (" + fmtBytes(f.size) + ")" : "no file selected";
};
$("#up-cancel").onclick = () => closeWin($("#win-upload"));
$("#up-go").onclick = async () => {
  const f = $("#up-file").files[0];
  if (!f) { toast("choose a file first"); return; }
  $("#up-go").disabled = true;
  $("#up-status").textContent = "uploading…";
  const n = await wsUpload("", [f]);
  $("#up-go").disabled = false;
  $("#up-status").textContent = "";
  if (n) closeWin($("#win-upload"));
};

// ---- Drag-and-drop upload: a file dropped on the desktop lands in the
// Workspace root, on a Workspace window in its folder, on a folder tree row
// in that folder, or on a file tree row in that file's parent folder.
// Uploads PUT each file; toasts report skips and failures.
// Genuinely new files (not overwrites) are announced on the Newsfeed. ----
async function wsUpload(dir, files) {
  let done = 0, last = "";
  const fresh = [];
  for (const f of files) {
    if (f.size > 10 << 20) { toast(f.name + " exceeds 10 MB"); continue; }
    const rel = dir ? dir + "/" + f.name : f.name;
    try {
      const r = await (await api(wsUrl(rel), { method: "PUT", body: f })).json();
      done++; last = f.name;
      if (r.created) fresh.push(rel);
    } catch (e) { toast(f.name + ": " + e.message); }
  }
  if (done) {
    toast(done === 1 ? "Uploaded " + last : "Uploaded " + done + " files");
    refreshFinderWindows();
  }
  if (fresh.length)
    j("/v1/newsfeed", { method: "POST", json: {
      kind: "file",
      title: fresh.length === 1 ? "File added to Workspace" : fresh.length + " files added to Workspace",
      body: fresh.join(", "),
    }}).catch(() => {});
  return done;
}
const dndHasFiles = e => !!e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files");
// dropped directories can't be read as bodies — filter them out with a toast
function dndDropFiles(e) {
  const files = [];
  let dirs = 0;
  for (const it of e.dataTransfer.items || []) {
    if (it.kind !== "file") continue;
    const en = it.webkitGetAsEntry ? it.webkitGetAsEntry() : null;
    if (en && en.isDirectory) { dirs++; continue; }
    const f = it.getAsFile();
    if (f) files.push(f);
  }
  if (!files.length && !dirs) files.push(...e.dataTransfer.files);
  if (dirs) toast("folders can't be uploaded");
  return files;
}
// without these a stray drop navigates the page away from the desktop
document.addEventListener("dragover", e => { if (dndHasFiles(e)) e.preventDefault(); });
document.addEventListener("drop", e => { if (dndHasFiles(e)) e.preventDefault(); });
const desk = $("#desktop");
desk.addEventListener("dragover", e => {
  if (!dndHasFiles(e)) return;
  if (e.target.closest(".window")) { desk.classList.remove("drop"); return; }
  e.dataTransfer.dropEffect = "copy";
  desk.classList.add("drop");
});
desk.addEventListener("dragleave", e => {
  if (!desk.contains(e.relatedTarget)) desk.classList.remove("drop");
});
desk.addEventListener("drop", e => {
  desk.classList.remove("drop");
  if (!dndHasFiles(e) || e.target.closest(".window")) return;
  e.preventDefault();
  wsUpload("", dndDropFiles(e));
});
function wireFinderDrop(w) {
  let hi = null; // the tree row currently under the drag, if any
  const clear = () => {
    w.classList.remove("drop");
    if (hi) { hi.classList.remove("drop"); hi = null; }
  };
  const rowAt = e => {
    const row = e.target.closest(".tree-row");
    return row && row.dataset.dropDir !== undefined ? row : null;
  };
  w.addEventListener("dragover", e => {
    if (!dndHasFiles(e)) return;
    e.dataTransfer.dropEffect = "copy";
    w.classList.add("drop");
    const t = rowAt(e);
    if (t !== hi) { if (hi) hi.classList.remove("drop"); hi = t; if (hi) hi.classList.add("drop"); }
  });
  w.addEventListener("dragleave", e => { if (!w.contains(e.relatedTarget)) clear(); });
  w.addEventListener("drop", e => {
    if (!dndHasFiles(e)) return;
    e.preventDefault();
    const t = rowAt(e);
    clear();
    wsUpload(t ? t.dataset.dropDir : w.dataset.path, dndDropFiles(e));
  });
}

// ---- vm detail ----
function showPane(key) {
  document.querySelectorAll("#win-detail .tab").forEach(t => t.classList.toggle("active", t.dataset.tab === key));
  document.querySelectorAll("#win-detail .pane").forEach(p => { p.hidden = p.id !== "pane-" + key; });
  if (key === "term") {
    // auto-connect on tab click; readyState >= 2 catches sessions that died remotely
    const dead = !termWS || termWS.readyState >= 2;
    if (dead && $("#d-state").textContent === "running") $("#term-open").click();
    termResize();
  }
  if (key === "vibe") {
    chatDetect().then(() => fillVibeSelects(chatProv, (hostAgents.find(a => a.id === chatProv) || {}).default_model || ""));
  }
}
document.querySelectorAll("#win-detail .tab").forEach(t =>
  t.addEventListener("click", () => showPane(t.dataset.tab)));
async function openVM(name, tab) {
  if (currentVM !== name) closeTerm();
  flushNotes();
  currentVM = name;
  $("#d-title").textContent = name;
  $("#e-result").textContent = "";
  showPane("svc");
  openWin("#win-detail");
  loadNotes().catch(() => {});
  await refreshDetailHead();
  // deep-links wait for the fresh state: the Terminal tab's auto-connect
  // reads #d-state, which still shows the previous VM until here
  if (tab) showPane(tab);
  loadPorts().catch(() => {});
  loadTranscripts().catch(e => toast(e.message));
}
async function refreshDetailHead() {
  try {
    const vm = await j("/v1/vms/" + currentVM);
    const st = $("#d-state");
    st.className = "chip " + vm.state;
    st.textContent = vm.state;
    $("#d-ip").textContent = vm.ip || "";
    return vm;
  } catch (e) { toast(e.message); }
  return null;
}

// ---- terminal clipboard: OS copy/paste, no host/browser fight, no junk chars ----
function sanitizePaste(s) {
  return String(s || "")
    .replace(/\uFEFF/g, "")
    .replace(/[\u200B-\u200D\u2060\u00AD]/g, "")
    .replace(/\u00A0/g, " ")
    .replace(/[\u2028\u2029]/g, "\n")
    .replace(/\r\n/g, "\n").replace(/\r/g, "\n")
    .replace(/[\u2018\u2019]/g, "'")
    .replace(/[\u201C\u201D]/g, '"')
    .replace(/\u2026/g, "...")
    .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g, "");
}
async function copyText(s) {
  if (!s) return;
  try { await navigator.clipboard.writeText(s); }
  catch (e) {
    const ta = document.createElement("textarea");
    ta.value = s; document.body.append(ta); ta.select();
    try { document.execCommand("copy"); } catch (err) {}
    ta.remove();
  }
}
function wireXtermClipboard(term, box) {
  const pasteInto = async raw => {
    const text = sanitizePaste(raw);
    if (!text) return;
    term.paste(text);
  };
  const readClipboard = async () => {
    if (navigator.clipboard && navigator.clipboard.readText) {
      try { return await navigator.clipboard.readText(); } catch (e) {}
    }
    return "";
  };
  const copySelection = () => {
    if (!term.hasSelection()) return false;
    const s = sanitizePaste(term.getSelection());
    copyText(s);
    term.clearSelection();
    toast("Copied");
    return true;
  };
  // Desktop: Ctrl/Cmd+C copies selection, otherwise passes ^C to guest.
  // Ctrl/Cmd+V pastes from clipboard.
  term.attachCustomKeyEventHandler(ev => {
    if (ev.type !== "keydown") return true;
    const mod = ev.ctrlKey || ev.metaKey;
    if (mod && ev.code === "KeyC" && !ev.altKey) {
      if (term.hasSelection()) { ev.preventDefault(); copySelection(); return false; }
      return true;
    }
    if (mod && ev.code === "KeyV" && !ev.altKey) {
      ev.preventDefault();
      readClipboard().then(pasteInto).catch(() => {});
      return false;
    }
    return true;
  });
  // Browser copy/paste events (work on http LAN via the hidden textarea
  // fallback when the clipboard API is unavailable).
  box.addEventListener("paste", e => {
    e.preventDefault();
    e.stopPropagation();
    pasteInto(e.clipboardData && e.clipboardData.getData("text/plain"));
  });
  box.addEventListener("copy", e => {
    if (!term.hasSelection()) return;
    e.preventDefault();
    const s = sanitizePaste(term.getSelection());
    e.clipboardData.setData("text/plain", s);
    copyText(s);
  });
  // Pointer-driven copy/paste that works on desktop and mobile.
  // - Plain right-click / long-press shows a small inline menu with Copy,
  //   Paste, Select All.
  // - Ctrl/Cmd+right-click still copies selection or pastes.
  let touchStart = null, longTimer = null;
  const clearTouch = () => { touchStart = null; if (longTimer) { clearTimeout(longTimer); longTimer = null; } };
  const showTermMenu = (x, y) => {
    const items = [
      { label: "Copy", act: () => copySelection(), dis: !term.hasSelection() },
      { label: "Paste", act: () => readClipboard().then(pasteInto).catch(() => {}) },
      { label: "Select All", act: () => term.selectAll() },
    ];
    showCtxMenu(x, y, items);
  };
  box.addEventListener("contextmenu", e => {
    e.preventDefault();
    if (e.ctrlKey || e.metaKey) {
      if (!copySelection()) readClipboard().then(pasteInto).catch(() => {});
      return;
    }
    showTermMenu(e.clientX, e.clientY);
  });
  box.addEventListener("touchstart", e => {
    if (e.touches.length !== 1) return;
    touchStart = e.touches[0];
    longTimer = setTimeout(() => {
      const t = touchStart;
      if (!t) return;
      showTermMenu(t.clientX, t.clientY);
      clearTouch();
    }, 650);
  }, { passive: true });
  box.addEventListener("touchend", clearTouch, { passive: true });
  box.addEventListener("touchmove", clearTouch, { passive: true });
}

// ---- terminal ----
let term = null, termWS = null, fitAddon = null, termFitCtl = null;
// xterm scrolls its scrollback itself (the viewport is a sibling of the
// screen the cursor hovers, so it is never in the browser's scroll chain);
// swallow the default here or leftover wheel deltas scroll the window body.
$("#term-box").addEventListener("wheel", e => e.preventDefault(), { passive: false });
function closeTerm() {
  window.removeEventListener("resize", termResize);
  if (termFitCtl) { termFitCtl.stop(); termFitCtl = null; }
  if (termWS) {
    if (termWS._watch) termWS._watch.stop();
    termWS.onclose = null;
    try { termWS.close(); } catch (e) {}
    termWS = null;
  }
  if (term) { term.dispose(); term = null; }
  $("#term-box").hidden = true;
  $("#term-hint").hidden = false;
  $("#term-close").hidden = true;
  $("#term-open").hidden = false;
}
function termResize() {
  if (!fitAddon || !term) return;
  try { fitAddon.fit(); } catch (e) {}
  if (termWS && termWS.readyState === 1) {
    termWS.send(JSON.stringify({ resize: [term.cols, term.rows] }));
  }
  termFitCheck($("#term-box"), term, termWS);
}
// Keep xterm cols/rows glued to the box: window zoom, grow-box, edge
// drag, and font/DPR settle all change the box without a window resize.
function attachTermFit(term, fit, box, getWS) {
  let lastC = 0, lastR = 0, tmr = 0;
  const go = () => {
    if (box.clientWidth < 8 || box.clientHeight < 8) return;
    try { fit.fit(); } catch (e) {}
    const c = term.cols | 0, r = term.rows | 0;
    if (c < 2 || r < 2) return;
    const ws = typeof getWS === "function" ? getWS() : getWS;
    termFitCheck(box, term, ws);
    if (c === lastC && r === lastR) return;
    lastC = c; lastR = r;
    if (ws && ws.readyState === 1) {
      try { ws.send(JSON.stringify({ resize: [c, r] })); } catch (e) {}
    }
  };
  const schedule = () => {
    clearTimeout(tmr);
    tmr = setTimeout(go, 40);
  };
  box.addEventListener("mousedown", () => { try { term.focus(); } catch (e) {} });
  let ro;
  try {
    ro = new ResizeObserver(schedule);
    ro.observe(box);
  } catch (e) {}
  return { go, stop: () => { clearTimeout(tmr); if (ro) ro.disconnect(); } };
}
// FitAddon trusts the renderer's measured cell height, which Windows
// fractional display scaling can misreport (a ~5px reading fit ~3x the
// rows the box holds, overflowing the viewport into a scrollbar). Once
// the render settles, re-derive rows from the painted screen's actual
// cell height and correct any gross misfit from that ground truth.
function termFitCheck(box, t, ws) {
  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (!(box && t)) return;
    const scr = box.querySelector(".xterm-screen");
    const vp = box.querySelector(".xterm-viewport");
    if (!scr || !vp || !t.rows) return;
    const cell = scr.getBoundingClientRect().height / t.rows;
    const rows = Math.floor((vp.clientHeight - 10) / cell); // 10 = .xterm padding
    if (cell > 0 && rows >= 4 && Math.abs(rows - t.rows) >= 2) {
      t.resize(t.cols, rows);
      if (ws && ws.readyState === 1) {
        try { ws.send(JSON.stringify({ resize: [t.cols, rows] })); } catch (e) {}
      }
    }
    termSpacerFix(box, t);
  }));
}
// xterm sizes its scroll spacer from the viewport's rounded offsetHeight;
// fractional display scale (Windows 150% leaves the viewport at n+2/3 CSS
// px) rounds that past the true height, and the sub-pixel overflow enables
// the scrollbar with nothing to scroll. While there is no scrollback, pin
// the spacer at or under the real fractional height so the bar stays in
// its disabled-flat state; real scrollback overwrites it and scrolls.
function termSpacerFix(box, t) {
  const vp = box.querySelector(".xterm-viewport"), sa = box.querySelector(".xterm-scroll-area");
  if (!vp || !sa || t.buffer.active.length > t.rows) return;
  const h = Math.floor(vp.getBoundingClientRect().height) + "px";
  if (sa.style.height !== h) sa.style.height = h;
}
function watchVMSSH(t, status, vm) {
  let gotData = false, stopped = false;
  const write = s => { try { if (!stopped) t.write(s); } catch (e) {} };
  const slow = setTimeout(() => {
    if (gotData || stopped) return;
    if (status) status.textContent = vm + " · waiting for SSH…";
    write("\r\n\x1b[90m[exe] waiting for VM SSH; the guest may still be booting or sshd may be wedged\x1b[0m\r\n");
  }, 4000);
  const timeout = setTimeout(() => {
    if (gotData || stopped) return;
    if (status) status.textContent = vm + " · SSH not responding";
    write("\r\n\x1b[33m[exe] no SSH banner yet. If this stays blank, restart the VM and reopen the tool.\x1b[0m\r\n");
  }, 15000);
  const clear = () => { clearTimeout(slow); clearTimeout(timeout); };
  const stop = () => { stopped = true; clear(); };
  return {
    stop,
    open() { if (status) status.textContent = vm + " · SSH handshake…"; },
    data() { if (!gotData) { gotData = true; clear(); if (status) status.textContent = vm + " · connected"; } },
    close() {
      const hadData = gotData;
      if (!hadData) {
        write("\r\n\x1b[31m[exe] VM SSH connection failed before any output\x1b[0m\r\n");
        if (status) status.textContent = "VM SSH unavailable — close and reopen after restart";
      } else {
        write("\r\n\x1b[90m[disconnected]\x1b[0m\r\n");
        if (status) status.textContent = "disconnected — close and reopen";
      }
      stop();
    },
  };
}
$("#term-close").onclick = closeTerm;
$("#term-pop").onclick = () => {
  if (!currentVM) { toast("open a VM first"); return; }
  if ($("#d-state").textContent !== "running") { toast("start the VM first"); return; }
  openVMTermWin(currentVM);
};
$("#term-open").onclick = () => {
  closeTerm();
  $("#term-hint").hidden = true;
  const box = $("#term-box");
  box.hidden = false;
  term = new Terminal({
    fontSize: 12, cursorBlink: true,
    fontFamily: '"MesloLGS NF", "Monaco", ui-monospace, SFMono-Regular, Menlo, monospace',
    theme: { background: "#000000" },
  });
  fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(box);
  wireXtermClipboard(term, box);
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const tok = tokenInput.value ? "?token=" + encodeURIComponent(tokenInput.value) : "";
  termWS = new WebSocket(`${proto}://${location.host}/v1/vms/${currentVM}/terminal${tok}`);
  termWS.binaryType = "arraybuffer";
  const enc = new TextEncoder();
  termFitCtl = attachTermFit(term, fitAddon, box, () => termWS);
  const fitCtl = termFitCtl;
  termWS._watch = watchVMSSH(term, null, currentVM);
  termWS.onopen = () => {
    termWS._watch.open();
    fitCtl.go();
    term.focus();
    setTimeout(fitCtl.go, 400);
  };
  termWS.onmessage = e => {
    if (termWS && termWS._watch) termWS._watch.data();
    term.write(new Uint8Array(e.data));
  };
  termWS.onclose = () => {
    if (term && termWS && termWS._watch) termWS._watch.close();
    $("#term-close").hidden = true;
    $("#term-open").hidden = false;
  };
  term.onData(d => { if (termWS && termWS.readyState === 1) termWS.send(enc.encode(d)); });
  window.addEventListener("resize", termResize);
  $("#term-open").hidden = true;
  $("#term-close").hidden = false;
};

// ---- host Terminal windows: every double-click opens a fresh login shell
// in its own window, like a real terminal app. Terminal windows are live
// sessions, so they are never restored from the layout snapshot, and
// closing one ends its shell and removes the window for good. ----
let termSeq = 0;
function openHostTermWin() {
  const n = ++termSeq;
  const w = el("div", { class: "window grow term-window", id: "win-term-" + n });
  const open = document.querySelectorAll("#desktop .term-window").length;
  w.style.left = (150 + open * 26) + "px";
  w.style.top = (100 + open * 22) + "px";
  w.style.width = "640px";
  w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
    <div class="title"></div>
    <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
    <div class="win-frame">
      <div class="win-body" style="padding:0"><div class="hostterm-box"></div></div>
      <div class="statusbar"><span class="term-status">…</span><span class="muted"> · Ctrl+select+right-click copy</span></div>
      ${GROW_TILE}
    </div>`;
  w.querySelector(".title").textContent = n === 1 ? "Terminal" : "Terminal " + n;
  const box = w.querySelector(".hostterm-box"), status = w.querySelector(".term-status");
  box.addEventListener("wheel", e => e.preventDefault(), { passive: false });
  $("#desktop").append(w);
  initWindow(w);
  openWin(w);

  const term = new Terminal({
    fontSize: 12, cursorBlink: true,
    fontFamily: '"MesloLGS NF", "Monaco", ui-monospace, SFMono-Regular, Menlo, monospace',
    theme: { background: "#000000" },
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(box);
  wireXtermClipboard(term, box);
  box.querySelector(".xterm-viewport").classList.add("vflush");
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const tok = tokenInput.value ? "?token=" + encodeURIComponent(tokenInput.value) : "";
  status.textContent = "connecting…";
  const ws = new WebSocket(`${proto}://${location.host}/v1/host/terminal${tok}`);
  ws.binaryType = "arraybuffer";
  const enc = new TextEncoder();
  const fitCtl = attachTermFit(term, fit, box, () => ws);
  const resize = () => fitCtl.go();
  ws.onopen = () => {
    status.textContent = "connected";
    fitCtl.go();
    term.focus();
    setTimeout(fitCtl.go, 400);
  };
  ws.onmessage = e => term.write(new Uint8Array(e.data));
  ws.onclose = () => {
    term.write("\r\n\x1b[90m[disconnected]\x1b[0m\r\n");
    status.textContent = "disconnected — close and reopen for a new shell";
  };
  term.onData(d => { if (ws.readyState === 1) ws.send(enc.encode(d)); });
  window.addEventListener("resize", resize);
  wireGrow(w.querySelector(".grow-box"), w, box, 400, { minH: 160, onMove: resize });
  w._termResize = resize;
  w._teardown = () => {
    window.removeEventListener("resize", resize);
    fitCtl.stop();
    ws.onclose = null;
    try { ws.close(); } catch (e) {}
    term.dispose();
  };
}

// ---- VM shell windows: independent of the VM detail tab, zoomable ----
const VM_AGENTS = [
  { id: "codex", title: "Codex" },
  { id: "gemini", title: "Gemini" },
  { id: "opencode", title: "OpenCode" },
  { id: "aider", title: "Aider" },
  { id: "qwen", title: "Qwen Code" },
  { id: "pi", title: "Pi" },
  { id: "claude", title: "Claude" },
];
const VM_TOOLS = [
  { id: "git", title: "git" },
  { id: "build-essential", title: "build-essential" },
  { id: "python3", title: "Python 3" },
  { id: "node", title: "Node.js" },
  { id: "docker", title: "Docker" },
  { id: "go", title: "Go" },
  { id: "rust", title: "Rust" },
  { id: "jq", title: "jq" },
  { id: "ripgrep", title: "ripgrep" },
  { id: "fd", title: "fd" },
  { id: "fzf", title: "fzf" },
  { id: "tmux", title: "tmux" },
  { id: "neovim", title: "Neovim" },
  { id: "htop", title: "htop" },
  { id: "gh", title: "GitHub CLI" },
  { id: "cmake", title: "CMake" },
  { id: "sqlite3", title: "SQLite" },
  { id: "unzip", title: "zip/unzip" },
  { id: "tree", title: "tree" },
  { id: "rsync", title: "rsync" },
  { id: "uv", title: "uv" },
  { id: "pnpm", title: "pnpm" },
  { id: "bun", title: "Bun" },
  { id: "lazygit", title: "lazygit" },
  { id: "hyperfine", title: "hyperfine" },
  { id: "direnv", title: "direnv" },
  { id: "just", title: "just" },
  { id: "bat", title: "bat" },
  { id: "eza", title: "eza" },
  { id: "httpie", title: "HTTPie" },
  { id: "yq", title: "yq" },
  { id: "delta", title: "delta" },
];
function openVMTermWin(vm, opts) {
  opts = opts || {};
  if (!vm) { toast("open a VM first"); return; }
  if (opts.run) {
    const existing = [...document.querySelectorAll(".vmterm-window")]
      .find(w => w.dataset.vm === vm && w.dataset.run === opts.run && w.dataset.live !== "0");
    if (existing) {
      openWin(existing);
      if (existing._termResize) existing._termResize();
      return existing;
    }
  }
  const n = ++termSeq;
  const w = el("div", { class: "window grow term-window vmterm-window", id: "win-vmterm-" + n });
  w.dataset.vm = vm;
  w.dataset.run = opts.run || "";
  w.dataset.live = "1";
  const open = document.querySelectorAll("#desktop .term-window").length;
  w.style.left = (160 + open * 26) + "px";
  w.style.top = (90 + open * 22) + "px";
  w.style.width = "720px";
  w.innerHTML = `<div class="titlebar"><button class="tbox close"></button><div class="stripe"></div>
    <div class="title"></div>
    <div class="stripe"></div><button class="tbox min" title="Minimize"></button><button class="tbox shade" title="Collapse"></button><button class="tbox max" title="Maximize"></button></div>
    <div class="win-frame">
      <div class="win-body" style="padding:0"><div class="vmterm-box"></div></div>
      <div class="statusbar"><span class="term-status">…</span><span class="muted"> · Ctrl+select+right-click copy</span></div>
      ${GROW_TILE}
    </div>`;
  w.querySelector(".title").textContent = (opts.title || vm) + " — shell";
  const box = w.querySelector(".vmterm-box"), status = w.querySelector(".term-status");
  box.addEventListener("wheel", e => e.preventDefault(), { passive: false });
  $("#desktop").append(w);
  initWindow(w);
  openWin(w);

  const t = new Terminal({
    fontSize: 13, cursorBlink: true, macOptionIsMeta: true,
    fontFamily: '"MesloLGS NF", "Monaco", ui-monospace, SFMono-Regular, Menlo, monospace',
    theme: { background: "#000000" },
    scrollback: 5000,
  });
  const fit = new FitAddon.FitAddon();
  t.loadAddon(fit);
  t.open(box);
  wireXtermClipboard(t, box);
  box.querySelector(".xterm-viewport").classList.add("vflush");
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const q = new URLSearchParams();
  if (tokenInput.value) q.set("token", tokenInput.value);
  if (opts.run) q.set("run", opts.run);
  const qs = q.toString() ? "?" + q.toString() : "";
  status.textContent = "connecting…";
  const ws = new WebSocket(`${proto}://${location.host}/v1/vms/${encodeURIComponent(vm)}/terminal${qs}`);
  ws.binaryType = "arraybuffer";
  const enc = new TextEncoder();
  const fitCtl = attachTermFit(t, fit, box, () => ws);
  const resize = () => fitCtl.go();
  ws._watch = watchVMSSH(t, status, vm);
  ws.onopen = () => {
    ws._watch.open();
    fitCtl.go();
    t.focus();
    setTimeout(() => { fitCtl.go(); t.focus(); }, 400);
  };
  ws.onmessage = e => {
    ws._watch.data();
    t.write(new Uint8Array(e.data));
  };
  ws.onclose = () => {
    w.dataset.live = "0";
    ws._watch.close();
  };
  t.onData(d => { if (ws.readyState === 1) ws.send(enc.encode(d)); });
  window.addEventListener("resize", resize);
  wireGrow(w.querySelector(".grow-box"), w, box, 480, { minH: 180, onMove: resize });
  w._termResize = resize;
  w._teardown = () => {
    window.removeEventListener("resize", resize);
    fitCtl.stop();
    ws._watch.stop();
    ws.onclose = null;
    try { ws.close(); } catch (e) {}
    t.dispose();
  };
}

// ---- web services ----
$("#svc-refresh").onclick = () => loadPorts().catch(() => {});
async function loadPorts() {
  const box = $("#svc-list");
  if (!currentVM) {
    box.replaceChildren(el("span", { class: "muted" }, "Open a VM first."));
    return;
  }
  if ($("#d-state").textContent !== "running") {
    box.replaceChildren(el("span", { class: "muted" }, "Start the VM first, then refresh services."));
    return;
  }
  box.replaceChildren(el("span", { class: "muted" }, "scanning…"));
  let d;
  try {
    d = await j(`/v1/vms/${currentVM}/ports`);
  } catch (e) {
    box.replaceChildren(el("span", { class: "muted" }, e.message));
    return;
  }
  const routes = await j("/v1/routes").catch(() => ({}));
  // one row per service: published routes are merged onto their port by backend
  const prefix = `http://${d.ip}:`;
  const hostForPort = {};
  for (const [host, backend] of Object.entries(routes)) {
    if (backend.startsWith(prefix)) hostForPort[parseInt(backend.slice(prefix.length), 10)] = host;
  }
  const svcs = d.ports.map(p => ({ port: p.port, process: p.process || "", host: hostForPort[p.port], local: p.local }));
  for (const port of Object.keys(hostForPort).map(Number)) {
    if (!svcs.some(s => s.port === port)) svcs.push({ port, process: "", host: hostForPort[port], dead: true });
  }
  svcs.sort((a, b) => a.port - b.port);

  box.replaceChildren();
  if (!svcs.length) {
    box.append(el("span", { class: "muted" }, "Nothing listening besides SSH yet. Switch to the Agent tab and ask it to build & start something, then hit Refresh."));
    return;
  }
  const tbody = el("tbody", {});
  for (const s of svcs) {
    let pub, action;
    if (s.host) {
      pub = el("span", { style: "display:inline-flex; align-items:center; gap:6px" },
        el("span", { class: "chip running" }),
        el("a", { href: "https://" + s.host, target: "_blank", rel: "noopener", class: "mono svc" }, s.host + " ↗"));
      const un = el("button", { class: "ghost sm danger" }, "Unexpose");
      un.onclick = async () => {
        if (!un.classList.contains("armed")) {
          un.classList.add("armed");
          un.textContent = "Sure?";
          setTimeout(() => { un.classList.remove("armed"); un.textContent = "Unexpose"; }, 3000);
          return;
        }
        un.disabled = true;
        un.textContent = "Removing…";
        try {
          const res = await j("/v1/routes/" + encodeURIComponent(s.host), { method: "DELETE" });
          let msg = s.host + " unpublished";
          for (const wmsg of res.warnings || []) msg += " — ⚠ " + wmsg;
          toast(msg);
        } catch (e) { toast(e.message); }
        loadPorts().catch(() => {});
      };
      action = un;
    } else {
      pub = el("span", { class: "muted" }, "—");
      const ex = el("button", { class: "ghost sm" }, "Expose");
      ex.onclick = async () => {
        ex.disabled = true;
        ex.textContent = "Exposing…";
        await doExpose(s.port, "");
        ex.disabled = false;
        ex.textContent = "Expose";
      };
      action = ex;
    }
    tbody.append(el("tr", {},
      el("td", { class: "mono" }, String(s.port)),
      el("td", { class: "muted" }, s.dead ? "not listening" : s.process),
      el("td", {}, s.dead ? el("span", { class: "muted" }, "—")
        : el("a", { href: `http://${s.local || `${d.ip}:${s.port}`}/`, target: "_blank", rel: "noopener", class: "mono svc" }, `${d.ip}:${s.port} ↗`)),
      el("td", {}, pub),
      el("td", { style: "text-align:right" }, action)));
  }
  box.append(el("div", { class: "listbox" },
    el("table", {},
      el("thead", {}, el("tr", {},
        el("th", {}, "Port"), el("th", {}, "Process"), el("th", {}, "Local"), el("th", {}, "Published"), el("th", {}, ""))),
      tbody)));
}

// ---- daemon log ----
let logAbort = null, logRetry = null;
function stopLogStream() {
  clearTimeout(logRetry);
  logRetry = null;
  if (logAbort) { logAbort.abort(); logAbort = null; }
}
function openLogWin() {
  openWin("#win-log");
  if (!logAbort) streamLog();
}
async function streamLog() {
  stopLogStream();
  const ac = logAbort = new AbortController();
  const out = $("#log-out"), st = $("#log-status");
  out.textContent = "";
  st.textContent = "connecting…";
  try {
    const resp = await api("/v1/logs", { signal: ac.signal });
    st.textContent = "streaming";
    const rd = resp.body.getReader();
    const dec = new TextDecoder();
    for (;;) {
      const { done, value } = await rd.read();
      if (done) break;
      // stick to the bottom only if the user hasn't scrolled up
      const stick = out.scrollHeight - out.scrollTop - out.clientHeight < 4;
      out.textContent += dec.decode(value, { stream: true });
      if (out.textContent.length > 400000) out.textContent = out.textContent.slice(-300000);
      if (stick) out.scrollTop = out.scrollHeight;
    }
  } catch (e) {
    if (ac.signal.aborted) return;
  }
  // stream ended or errored without an abort (e.g. daemon restart):
  // keep retrying quietly while the window is open
  logAbort = null;
  st.textContent = "disconnected — reconnecting…";
  logRetry = setTimeout(streamLog, 3000);
}
wireGrow($("#log-grow"), $("#win-log"), $("#log-out"), 380, { stickEl: $("#log-out") });

// ---- Agent & Tools: named CLIs + popular Linux dev tools inside the VM ----
function launchGuest(item) {
  if (!currentVM) { toast("open a VM first"); return; }
  if ($("#d-state").textContent !== "running") { toast("start the VM first"); return; }
  openVMTermWin(currentVM, { title: currentVM + " · " + item.title, run: item.id });
}
(function fillAgentLaunchers() {
  const box = $("#a-launchers");
  if (box) for (const a of VM_AGENTS) {
    const b = el("button", { class: "btn", type: "button" }, a.title);
    b.onclick = () => launchGuest(a);
    box.append(b);
  }
  const tools = $("#a-tools");
  if (tools) for (const t of VM_TOOLS) {
    const b = el("button", { class: "ghost sm", type: "button", title: "Install if missing, then open a shell" }, t.title);
    b.onclick = () => launchGuest(t);
    tools.append(b);
  }
})();

function fillVibeSelects(prefAgent, prefModel) {
  const psel = $("#vibe-provider"), msel = $("#vibe-model");
  if (!psel || !msel) return;
  const ready = hostAgents.filter(a => a.ready);
  psel.replaceChildren(...(ready.length ? ready : hostAgents).map(a =>
    el("option", { value: a.id }, a.name + (a.ready ? "" : " (not signed in)"))));
  const pick = hostAgents.find(a => a.id === (prefAgent || chatProv)) || ready[0] || hostAgents[0];
  if (pick) psel.value = pick.id;
  const a = hostAgents.find(x => x.id === psel.value);
  const models = (a && a.models) || [];
  msel.replaceChildren(...models.map(m => el("option", { value: m }, m)));
  if (a && a.default_model) msel.append(el("option", { value: a.default_model }, a.default_model));
  const want = prefModel || (a && a.default_model) || "";
  if (want && (models.includes(want) || want === (a && a.default_model))) msel.value = want;
  else if (models.length) msel.value = models[0];
}
$("#vibe-provider") && $("#vibe-provider").addEventListener("change", () => fillVibeSelects($("#vibe-provider").value, ""));
async function runVibeAgent() {
  const prompt = $("#vibe-prompt").value.trim();
  if (!currentVM) { toast("open a VM first"); return; }
  if ($("#d-state").textContent !== "running") { toast("start the VM first"); return; }
  if (!prompt) { toast("type a task first"); return; }
  const provider = $("#vibe-provider").value;
  const model = $("#vibe-model").value;
  const log = $("#vibe-log");
  const status = $("#vibe-status");
  log.hidden = false;
  log.textContent = "";
  status.textContent = "running…";
  $("#vibe-run").disabled = true;
  try {
    const resp = await api(`/v1/vms/${currentVM}/agent`, {
      method: "POST",
      json: { prompt, provider, model },
    });
    const rd = resp.body.getReader();
    const dec = new TextDecoder();
    for (;;) {
      const { done, value } = await rd.read();
      if (done) break;
      log.textContent += dec.decode(value, { stream: true });
      log.scrollTop = log.scrollHeight;
    }
    status.textContent = "done";
    loadTranscripts();
  } catch (e) {
    log.textContent += "\nERROR: " + e.message;
    status.textContent = "failed";
  }
  $("#vibe-run").disabled = false;
}
$("#vibe-run").onclick = runVibeAgent;

// ---- expose ----
function cfIncomplete(cfg) {
  const c = (cfg && cfg.cloudflare) || {};
  return !(c.api_token && c.account_id && c.zone_id && c.tunnel_id && c.domain);
}
async function doExpose(port, subdomain) {
  try { loadedConfig = await j("/v1/config"); } catch (e) {}
  if (cfIncomplete(loadedConfig)) {
    toast("Cloudflare isn't set up yet — finish the wizard, then expose again.");
    wizOpen();
    return;
  }
  try {
    const res = await j(`/v1/vms/${currentVM}/expose`, { method: "POST", json: { port, subdomain } });
    const warns = res.warnings || [];
    const lines = [`route: ${res.host} → ${res.backend}`];
    if (res.ingress) lines.push("tunnel ingress → " + res.ingress);
    for (const wmsg of warns) lines.push("⚠ " + wmsg);
    lines.push("url: " + res.url);
    $("#e-result").textContent = lines.join("\n");
    $("#e-result").style.whiteSpace = "pre-wrap";
    loadPorts().catch(() => {});
    if (warns.some(wmsg => wmsg.startsWith("dns:") || wmsg.startsWith("ingress:") || wmsg.includes("not fully configured"))) {
      toast("Cloudflare API calls failed — re-validate in the wizard.");
      wizOpen();
    } else {
      toast(res.host + " → :" + port);
    }
  } catch (e) { toast(e.message); }
}
$("#e-go").onclick = async () => {
  const port = parseInt($("#e-port").value, 10);
  if (!port) { toast("port required"); return; }
  $("#e-go").disabled = true;
  await doExpose(port, $("#e-sub").value.trim());
  $("#e-go").disabled = false;
};

// ---- transcripts ----
function closeTranscriptModal() {
  $("#tr-overlay").hidden = true;
}
$("#tr-x").onclick = closeTranscriptModal;
$("#tr-overlay").addEventListener("mousedown", e => {
  if (e.target === $("#tr-overlay")) closeTranscriptModal();
});
function openTranscriptModal(d) {
  $("#tr-modal-title").textContent = "Transcript";
  $("#tr-modal-log").textContent = `# ${d.meta.model} · ${fmtTime(d.meta.started_at)} · ${d.meta.status}` +
    (d.meta.error ? " — " + d.meta.error : "") + "\n# " + d.meta.prompt + "\n\n" + d.log;
  $("#tr-overlay").hidden = false;
}
async function loadTranscripts() {
  const list = await j(`/v1/vms/${currentVM}/transcripts`);
  const box = $("#t-list");
  box.replaceChildren();
  if (!list.length) {
    box.append(el("span", { class: "muted" }, "No daemon-run Agent transcripts yet. Terminal-launched CLIs keep their own history inside the VM."));
    return;
  }
  for (const t of list) {
    const item = el("div", { class: "t-item" },
      el("span", { class: "muted mono", style: "font-size:11px" }, fmtTime(t.started_at)),
      chip(t.status),
      el("span", { class: "t-prompt", title: t.prompt }, t.prompt));
    item.onclick = () => {
      for (const n of box.children) n.classList.remove("sel");
      item.classList.add("sel");
    };
    objectOpen(item, async () => {
      try {
        const d = await j(`/v1/vms/${currentVM}/transcripts/${t.id}`);
        openTranscriptModal(d);
      } catch (e) { toast(e.message); }
    });
    box.append(item);
  }
}

// ---- notes ----
// Debounced autosave: a PUT fires ~800ms after typing stops, and pending
// edits flush on blur, tab/window close, and VM switch. notesVM pins each
// save to the VM whose text is in the box, so a flush that lands after the
// user has already opened another VM still writes to the right place.
let notesTimer = null, notesVM = null;
const notesText = $("#n-text");
function notesStatus(t) { $("#n-status").textContent = t; }
async function loadNotes() {
  clearTimeout(notesTimer);
  notesTimer = null;
  notesVM = currentVM;
  notesText.value = "";
  notesStatus("");
  try {
    const d = await j(`/v1/vms/${notesVM}/notes`);
    if (notesVM === currentVM) notesText.value = d.notes || "";
  } catch (e) { notesStatus(e.message); }
}
function saveNotes(vm, text) {
  // keepalive lets a flush fired from pagehide finish after the page is gone
  api(`/v1/vms/${vm}/notes`, { method: "PUT", json: { notes: text }, keepalive: true })
    .then(() => { if (vm === notesVM) notesStatus("Saved"); })
    .catch(e => { if (vm === notesVM) notesStatus("Save failed: " + e.message); });
}
function flushNotes() {
  if (!notesTimer) return;
  clearTimeout(notesTimer);
  notesTimer = null;
  saveNotes(notesVM, notesText.value);
}
$("#notes-delete").onclick = async () => {
  if (!currentVM || notesVM !== currentVM) return;
  if (!await platAsk("Delete notes for “" + currentVM + "”? A backup is kept in the VM folder.", { title: "Delete Notes", ok: "Delete" })) return;
  clearTimeout(notesTimer);
  notesTimer = null;
  try {
    await api(`/v1/vms/${currentVM}/notes`, { method: "DELETE" });
    if (notesVM === currentVM) {
      notesText.value = "";
      notesStatus("Deleted");
    }
  } catch (e) {
    notesStatus("Delete failed: " + e.message);
  }
};
notesText.addEventListener("input", () => {
  notesStatus("…");
  clearTimeout(notesTimer);
  notesTimer = setTimeout(() => { notesTimer = null; saveNotes(notesVM, notesText.value); }, 800);
});
notesText.addEventListener("blur", flushNotes);
window.addEventListener("pagehide", flushNotes);

// ---- chat ----
let chatCur = null, chatBusy = false;
// chatScope filters the session list to one VM's chats (and pins new ones
// to it); chatCurVM is the ACTIVE session's pin — a property of the
// session itself, shown in the titlebar however the session was reached.
let chatScope = null, chatCurVM = null, chatDetected = false, chatProv = "";
const chatMsgs = $("#chat-msgs"), chatInput = $("#chat-input");

// ---- newsfeed ----
// The merged cross-node event feed (newsfeed.go): every node appends to its
// own synced journal and GET /v1/newsfeed folds them together, so the same
// timeline shows on every desk. The byline under each item says which node
// posted it and when.
function openNewsWin() {
  openWin("#win-news");
  loadNews().catch(e => toast(e.message));
}
async function loadNews() {
  const d = await j("/v1/newsfeed");
  const items = d.items || [];
  const list = $("#news-list");
  list.replaceChildren();
  if (!items.length) {
    list.append(el("div", { class: "muted", style: "padding:10px" },
      "No news yet — VM launches, sync conflicts and node changes from every joined node land here."));
  }
  newsOpenRow = null; // rows were just rebuilt — no swipe is open anymore
  for (const it of items) {
    const wrap = el("div", { class: "news-swipe" }, el("div", { class: "news-title" }, it.title || ""));
    if (it.body) wrap.append(el("div", { class: "news-body" }, it.body));
    wrap.append(el("div", { class: "news-meta" }, (it.host || "?") + " · " + new Date(it.time).toLocaleString()));
    const row = el("div", { class: "news-item" });
    if (IS_MOBILE) {
      row.append(el("button", { class: "news-del", onclick: () => deleteNews(it.id) }, "Delete"));
      wireNewsSwipe(row, wrap);
    } else {
      // desktop: the shared OS 9 contextual menu instead of a swipe
      row.addEventListener("contextmenu", e => {
        e.preventDefault();
        showCtxMenu(e.clientX, e.clientY, [{ label: "Delete", act: () => deleteNews(it.id) }]);
      });
    }
    row.append(wrap);
    list.append(row);
  }
  $("#news-count").textContent = items.length === 1 ? "1 item" : items.length + " items";
}

// mobile swipe-to-delete: drag a row left to park its content at -72px and
// reveal Delete underneath; drag right, tap the row, or start a drag on
// another row to close it again. touch-action: pan-y on the row keeps
// vertical list scrolling native while horizontal drags reach us.
let newsOpenRow = null;
function newsCloseSwipe() {
  if (newsOpenRow) { newsOpenRow.style.transform = ""; newsOpenRow = null; }
}
function wireNewsSwipe(row, wrap) {
  const W = 72;
  let sx = 0, sy = 0, dx = 0, horiz = null;
  row.addEventListener("touchstart", e => {
    if (newsOpenRow && newsOpenRow !== wrap) newsCloseSwipe();
    sx = e.touches[0].clientX; sy = e.touches[0].clientY;
    dx = newsOpenRow === wrap ? -W : 0;
    horiz = null;
  }, { passive: true });
  row.addEventListener("touchmove", e => {
    const mx = e.touches[0].clientX - sx, my = e.touches[0].clientY - sy;
    if (horiz === null && (Math.abs(mx) > 6 || Math.abs(my) > 6)) horiz = Math.abs(mx) > Math.abs(my);
    if (!horiz) return;
    dx = Math.max(-W, Math.min(0, (newsOpenRow === wrap ? -W : 0) + mx));
    wrap.style.transition = "none";
    wrap.style.transform = "translateX(" + dx + "px)";
  }, { passive: true });
  const settle = () => {
    if (!horiz) return;
    wrap.style.transition = "";
    if (dx < -W / 2) { wrap.style.transform = "translateX(-" + W + "px)"; newsOpenRow = wrap; }
    else { wrap.style.transform = ""; if (newsOpenRow === wrap) newsOpenRow = null; }
  };
  row.addEventListener("touchend", settle);
  row.addEventListener("touchcancel", settle);
  // a plain tap (no drag this touch) on an open row closes it
  wrap.addEventListener("click", () => { if (newsOpenRow === wrap && horiz === null) newsCloseSwipe(); });
}
async function deleteNews(id) {
  try {
    await api("/v1/newsfeed/" + encodeURIComponent(id), { method: "DELETE" });
  } catch (e) { toast(e.message); }
  loadNews().catch(() => {});
}

// the Chat window appears once any host agent (Grok/Claude/Codex) is signed in
let hostAgents = [];
async function chatDetect(force) {
  try {
    const st = await j("/v1/host/agents");
    hostAgents = st.agents || [];
    const ready = hostAgents.filter(a => a.ready);
    chatDetected = ready.length > 0;
    chatProv = st.host_agent || (ready[0] && ready[0].id) || "";
    document.querySelectorAll("[data-act=\"winchat\"]").forEach(it => { it.hidden = !chatDetected; });
    $("#chat-icon").hidden = !chatDetected;
    fillHostAgentSelects(st.host_agent, st.host_model);
    const a = ready.find(x => x.id === chatProv) || ready[0];
    const prov = chatProviderLabel(chatProv);
    const model = st.host_model || (a && a.default_model) || "";
    $("#chat-status").textContent = chatDetected
      ? (prov + (prov && model ? " / " : "") + model + (a && a.source ? " — " + a.source : "") + (a && a.email ? " — " + a.email : ""))
      : "no host agent signed in (~/.grok, ~/.claude, ~/.codex)";
    if (!chatDetected && !$("#win-chat").hidden) toast("No host agent signed in");
    chatUsage(force);
  } catch (e) { /* daemon unreachable; leave as-is */ }
}
function fillHostAgentSelects(prefAgent, prefModel) {
  const asel = $("#chat-agent"), msel = $("#chat-model");
  if (!asel || !msel) return;
  const ready = hostAgents.filter(a => a.ready);
  const pick = ready.find(a => a.id === prefAgent) || ready[0];
  asel.replaceChildren(...(ready.length ? ready : hostAgents).map(a =>
    el("option", { value: a.id }, a.name + (a.ready ? "" : " (not signed in)"))));
  if (pick) asel.value = pick.id;
  // Use the explicitly configured host_model if it belongs to this agent,
  // otherwise fall back to the agent's default.
  const models = (pick && pick.models) || [];
  const modelPref = prefModel && (models.includes(prefModel) || prefModel === pick?.default_model) ? prefModel : "";
  fillHostModels(msel, pick, modelPref || (pick && pick.default_model));
}
function fillHostModels(sel, agent, prefModel) {
  const models = (agent && agent.models) || [];
  const cur = prefModel || (agent && agent.default_model) || "";
  if (!models.length) {
    sel.replaceChildren(el("option", { value: cur }, cur || "(no models)"));
    return;
  }
  sel.replaceChildren(...models.map(m => el("option", { value: m }, m)));
  if (cur && !models.includes(cur)) sel.append(el("option", { value: cur }, cur));
  sel.value = cur || models[0];
}
$("#chat-agent") && $("#chat-agent").addEventListener("change", () => {
  const a = hostAgents.find(x => x.id === $("#chat-agent").value);
  fillHostModels($("#chat-model"), a, a && a.default_model);
  persistHostAgent();
});
$("#chat-model") && $("#chat-model").addEventListener("change", persistHostAgent);
async function persistHostAgent() {
  try {
    const cfg = loadedConfig || await j("/v1/config");
    const nc = JSON.parse(JSON.stringify(cfg));
    nc.host_agent = $("#chat-agent").value;
    nc.host_model = $("#chat-model").value;
    await j("/v1/config", { method: "PUT", json: nc });
    loadedConfig = nc;
    chatDetect(true);
  } catch (e) { /* ignore */ }
}
// While ChatGPT is the backend, the status line's far right shows how much
// of each rolling rate-limit window the subscription has used. Cached for a
// minute; a finished reply forces a fresh read since it just spent tokens.
let chatUsageAt = 0;
async function chatUsage(force) {
  const box = $("#chat-usage");
  if (chatProv !== "codex" || !chatDetected) { box.hidden = true; chatUsageAt = 0; return; }
  if ($("#win-chat").hidden) return;
  if (!force && Date.now() - chatUsageAt < 60000) return;
  chatUsageAt = Date.now();
  let u;
  try { u = await j("/v1/openai/usage"); }
  catch (e) { box.hidden = true; chatUsageAt = 0; return; }
  const rl = u.rate_limit || {};
  const spans = [], tips = [];
  for (const w of [rl.primary_window, rl.secondary_window]) {
    if (!w) continue;
    const pct = Math.max(0, Math.min(100, Math.round(w.used_percent)));
    if (spans.length) spans.push(" · ");
    spans.push(el("span", pct >= 90 ? { class: "hot" } : {}, chatWinLabel(w.limit_window_seconds) + " " + pct + "%"));
    tips.push(oaiWindowLabel(w.limit_window_seconds, "?") + ": " + pct + "% used — resets " + oaiResetAt(w.reset_at));
  }
  if (!spans.length) { box.hidden = true; return; }
  box.replaceChildren(...spans);
  box.title = tips.join("\n");
  box.hidden = false;
}
// compact window names for the status line: 5h, wk, 30d
function chatWinLabel(sec) {
  if (!sec) return "";
  const h = Math.round(sec / 3600);
  if (h < 24) return h + "h";
  const d = Math.round(h / 24);
  return d === 7 ? "wk" : d + "d";
}
// openChatWin(vm) scopes the window to that VM: the session list filters
// to its chats and a new chat is pinned to it. No argument (the Chat icon,
// the menu) shows every session.
function openChatWin(vm) {
  // Host Chat (icon / Windows menu) is never scoped to a VM.
  if (vm === undefined) {
    if (chatScope) {
      if (chatBusy) { toast("Wait for the current reply to finish."); openWin("#win-chat"); return; }
      setChatScope(null);
      chatCurVM = null;
      chatTitle();
    }
  } else if (vm !== chatScope) {
    if (chatBusy) { toast("Wait for the current reply to finish."); openWin("#win-chat"); return; }
    setChatScope(vm);
    if (chatCurVM !== vm) {
      chatCur = null;
      chatCurTitle = "";
      chatCurVM = vm;
      chatTitle();
      chatHint();
    }
  }
  openWin("#win-chat");
  if (!chatMsgs.children.length) chatHint();
  loadChatSessions().catch(e => toast(e.message));
  chatUsage();
  chatInput.focus();
}
function setChatScope(vm) {
  chatScope = vm || null;
  $("#chat-scope").hidden = !chatScope;
  $("#chat-scope-vm").textContent = chatScope || "";
}
$("#chat-scope-x").onclick = () => {
  setChatScope(null);
  loadChatSessions().catch(() => {});
};
let chatCurTitle = "";
function chatTitle() {
  const t = chatCurTitle || "Chat";
  $("#win-chat .title").textContent = t;
  const inp = $("#chat-title-edit");
  if (inp && document.activeElement !== inp) inp.value = chatCurTitle || "";
}
function chatProviderLabel(id) {
  const a = hostAgents.find(x => x.id === id);
  return a ? a.name : (id || "");
}

function chatHint() {
  chatMsgs.replaceChildren(el("div", { class: "chat-hint" },
    "Host Chat operates the VM cloud (list/create/start/stop). It is not a VM shell. Work inside a guest with Agent or exe env. Pick a host agent + model above (Grok / Claude / Codex — already signed in on this machine).",
    el("br"), el("br"), "Try: “what's running right now?”"));
}
function chatStick(force) {
  // during streaming only follow the bottom if the user hasn't scrolled up
  if (force || chatMsgs.scrollHeight - chatMsgs.scrollTop - chatMsgs.clientHeight < 40)
    chatMsgs.scrollTop = chatMsgs.scrollHeight;
}
function addMsg(cls, text) {
  const d = el("div", { class: "msg " + cls }, text);
  chatMsgs.append(d);
  chatStick(true);
  return d;
}
// assistant markdown: marked parses, DOMPurify strips anything dangerous
marked.use({ gfm: true, breaks: true });
DOMPurify.addHook("afterSanitizeAttributes", n => {
  if (n.tagName === "A") { n.setAttribute("target", "_blank"); n.setAttribute("rel", "noopener"); }
});
function renderMD(node, text, live) {
  node.innerHTML = DOMPurify.sanitize(marked.parse(text));
  if (!live) renderMermaid(node);
}
function renderMermaid(node) {
  node.querySelectorAll("pre code").forEach(code => {
    const cls = (code.className || "").toLowerCase();
    if (!cls.includes("mermaid")) return;
    const src = (code.textContent || "").trim();
    if (!src) return;
    const wrap = el("div", { class: "mermaid" });
    wrap.append(el("img", {
      src: "https://mermaid.ink/svg/" + btoa(unescape(encodeURIComponent(src))),
      alt: "mermaid diagram",
    }));
    (code.closest("pre") || code).replaceWith(wrap);
  });
}
function addMDMsg(text) {
  const d = addMsg("asst", "");
  renderMD(d, text, false);
  chatStick(true);
  return d;
}
function addToolOut(out) {
  if (out && out.trim()) addMsg("tool-out", out.length > 4000 ? out.slice(0, 4000) + "…" : out);
}
// mirrors the server's chatToolSummary for replaying stored sessions
function toolSummary(name, rawArgs) {
  let a = rawArgs || {};
  if (typeof a === "string") { try { a = JSON.parse(a); } catch (e) { a = {}; } }
  // pinned sessions store tool calls without a vm argument (the server
  // injects the pin), so the prefix only appears when the args carry one
  const at = a.vm ? a.vm + ":" : "";
  switch (name) {
    case "bash": return (a.vm ? a.vm + " " : "") + "$ " + (a.command || "");
    case "write_file": return `write ${at}${a.path} (${(a.content || "").length} bytes)`;
    case "read_file": return `read ${at}${a.path}`;
    case "expose": return `expose ${at}${a.port}` + (a.subdomain ? ` as "${a.subdomain}"` : "");
    default: return name + (Object.keys(a).length ? " " + JSON.stringify(a) : "");
  }
}
function renderChatSession(sess) {
  chatMsgs.replaceChildren();
  for (const m of sess.messages || []) {
    if (m.role === "user") addMsg("user", m.content);
    else if (m.role === "assistant") {
      if ((m.content || "").trim()) addMDMsg(m.content.trim());
      for (const tc of m.tool_calls || []) addMsg("tool", "▸ " + toolSummary(tc.function.name, tc.function.arguments));
    } else if (m.role === "tool") addToolOut(m.content || "");
  }
  if (!chatMsgs.children.length) chatHint();
}
async function loadChatSessions() {
  const list = (await j("/v1/chat/sessions")).filter(m => !chatScope || m.vm === chatScope);
  const box = $("#chat-sessions");
  box.replaceChildren();
  if (!list.length) box.append(el("div", { class: "muted", style: "padding:8px" },
    chatScope ? "No chats with this VM yet." : "No chats yet."));
  for (const m of list) {
    const x = el("button", { class: "ghost sm danger", title: "Delete this chat" }, "×");
    x.onclick = async e => {
      e.stopPropagation();
      if (!await platAsk("Delete chat “" + m.title + "”?", { title: "Delete Chat" })) return;
      try { await api("/v1/chat/sessions/" + m.id, { method: "DELETE" }); } catch (err) { toast(err.message); }
      if (chatCur === m.id) { chatCur = null; chatCurVM = chatScope; chatTitle(); chatHint(); }
      loadChatSessions().catch(() => {});
    };
    const title = el("div", { class: "cs-title", title: m.title + (m.vm ? " · " + m.vm : "") }, m.title);
    // in the all-sessions view, VM-pinned chats carry their VM's name;
    // under a scope every row is that VM's, so the label would be noise
    if (m.vm && !chatScope) title.append(el("span", { class: "cs-vm" }, " · " + m.vm));
    const item = el("div", { class: "cs-item" + (m.id === chatCur ? " sel" : "") },
      el("div", { style: "flex:1; min-width:0" },
        title,
        el("div", { class: "cs-time" }, fmtTime(m.updated_at))),
      x);
    item.onclick = e => { if (!e.target.closest("button")) selectChat(m.id); };
    box.append(item);
  }
}
async function selectChat(id) {
  if (chatBusy) { toast("Wait for the current reply to finish."); return; }
  chatCur = id;
  try {
    const sess = await j("/v1/chat/sessions/" + id);
    renderChatSession(sess);
    chatCurVM = sess.vm || null;
    chatCurTitle = sess.title || "";
    chatTitle();
  } catch (e) { toast(e.message); }
  loadChatSessions().catch(() => {});
  chatInput.focus();
}
$("#chat-new").onclick = () => {
  if (chatBusy) { toast("Wait for the current reply to finish."); return; }
  chatCur = null;
  chatCurTitle = "";
  chatCurVM = chatScope;
  chatTitle();
  chatHint();
  loadChatSessions().catch(() => {});
  chatInput.focus();
};
async function renameCurrentChat() {
  const inp = $("#chat-title-edit");
  if (!inp || !chatCur) return;
  const title = inp.value.trim();
  if (!title) return;
  try {
    const meta = await j("/v1/chat/sessions/" + chatCur, { method: "PUT", json: { title } });
    chatCurTitle = meta.title || title;
    chatTitle();
    loadChatSessions().catch(() => {});
  } catch (e) { toast(e.message); }
}
$("#chat-title-edit") && $("#chat-title-edit").addEventListener("change", renameCurrentChat);

async function chatSend() {
  const text = chatInput.value.trim();
  if (!text || chatBusy) return;
  chatBusy = true;
  $("#chat-send").disabled = true;
  const hint = chatMsgs.querySelector(".chat-hint");
  if (hint) hint.remove();
  addMsg("user", text);
  chatInput.value = "";
  const think = addMsg("think", "working");
  let cur = null, curText = "";
  const handle = ev => {
    if (ev.type === "delta") {
      // grow the streaming assistant bubble in place, re-rendering markdown
      if (!cur) { cur = el("div", { class: "msg asst" }); think.before(cur); curText = ""; }
      curText += ev.text;
      renderMD(cur, curText, true);
      chatStick();
      return;
    }
    cur = null;
    think.remove();
    if (ev.type === "session") { chatCur = ev.meta.id; chatCurVM = ev.meta.vm || null; chatCurTitle = ev.meta.title || ""; chatTitle(); }
    else if (ev.type === "text") addMDMsg(ev.text);
    else if (ev.type === "tool_call") addMsg("tool", "▸ " + ev.summary);
    else if (ev.type === "tool_result") addToolOut(ev.output || "");
    else if (ev.type === "error") addMsg("err", ev.error);
    if (ev.type !== "done") { chatMsgs.append(think); chatStick(); }
  };
  try {
    const resp = await api("/v1/chat/send", { method: "POST", json: {
      session: chatCur || "", message: text, vm: chatCur ? "" : (chatScope || ""),
      agent: ($("#chat-agent") && $("#chat-agent").value) || "",
      model: ($("#chat-model") && $("#chat-model").value) || "",
    } });
    const rd = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await rd.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, i).trim();
        buf = buf.slice(i + 1);
        if (line) handle(JSON.parse(line));
      }
    }
  } catch (e) { addMsg("err", e.message); }
  think.remove();
  const last = [...chatMsgs.querySelectorAll(".msg.asst")].pop();
  if (last) renderMermaid(last);
  chatBusy = false;
  $("#chat-send").disabled = false;
  loadChatSessions().catch(() => {});
  loadVMs().catch(() => {});
  chatUsage(true);
  chatInput.focus();
}
$("#chat-send").onclick = chatSend;
chatInput.addEventListener("keydown", e => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); chatSend(); }
});

// ---- chat backend switcher ----
// Clicking the Chat status line flips the host agent + model.
let cbCfg = null;
async function cbOpen() {
  try { cbCfg = await j("/v1/config"); } catch (e) { toast(e.message); return; }
  $("#cb-status").textContent = "";
  const ready = hostAgents.filter(a => a.ready);
  const list = ready.length ? ready : hostAgents;
  $("#cb-provider").replaceChildren(...list.map(a =>
    el("option", { value: a.id }, a.name + (a.ready ? "" : " (not signed in)"))));
  $("#cb-provider").value = cbCfg.host_agent || (list[0] && list[0].id) || "";
  $("#cb-overlay").hidden = false;
  cbFill();
}
function cbFill() {
  const p = $("#cb-provider").value;
  const a = hostAgents.find(x => x.id === p);
  fillHostModels($("#cb-model"), a, (cbCfg && cbCfg.host_model) || (a && a.default_model));
}
$("#cb-provider").onchange = cbFill;
$("#cb-x").onclick = () => { $("#cb-overlay").hidden = true; };
$("#cb-save").onclick = async () => {
  if (!cbCfg) return;
  const nc = JSON.parse(JSON.stringify(cbCfg));
  nc.host_agent = $("#cb-provider").value;
  nc.host_model = $("#cb-model").value;
  $("#cb-save").disabled = true;
  try {
    await j("/v1/config", { method: "PUT", json: nc });
    loadedConfig = nc;
    $("#cb-overlay").hidden = true;
    toast("Chat: " + nc.host_agent + " — " + nc.host_model);
    chatDetect(true);
  } catch (e) { toast(e.message); }
  $("#cb-save").disabled = false;
};
$("#chat-status").title = "Switch host agent / model…";
$("#chat-status").onclick = cbOpen;

// ---- config ----
const CONFIG_FIELDS = [
  { group: "Daemon", fields: [
    { path: "listen", hint: "rebinds live on Save — bind a Tailscale IP for remote access" },
    { path: "proxy_listen", hint: "rebinds live on Save" },
    { path: "ssh_listen", hint: "SSH gate — ssh -p 2222 exe@host (lobby) or <vm>@host; \"off\" disables; rebinds live on Save" },
    { path: "advertise_host", hint: "this Mac as seen from the cloudflared host" },
    { path: "api_token", secret: true },
    { path: "apps_dirs", list: true, hint: "extra app-bundle folders, comma-separated — e.g. ~/Developer/exe-apps" },
    { path: "ssh_user", restart: true },
    { path: "image_url", restart: true },
  ]},
  { group: "VM Defaults", fields: [
    { path: "default_cpus", num: true },
    { path: "default_memory_mb", num: true },
    { path: "default_disk_gb", num: true },
    { path: "idle_stop_minutes", num: true, hint: "Stop a running VM after this many minutes with no terminal, job or SSH. 0 = never." },
  ]},
  { group: "Firecracker", fields: [
    { path: "firecracker.binary", restart: true },
    { path: "firecracker.kernel_url", restart: true },
    { path: "firecracker.network_helper", restart: true },
    { path: "firecracker.network_cidr", restart: true },
    { path: "firecracker.outbound_interface", restart: true, hint: "empty uses the default route" },
  ]},
  { group: "GitHub", fields: [
    { path: "github.client_id", hint: "an OAuth app client ID with device flow enabled (github.com → Settings → Developer settings → OAuth Apps) — used by Publish to GitHub; the token stays on this host" },
  ]},
  { group: "Cloudflare", fields: [
    { path: "cloudflare.api_token", secret: true },
    { path: "cloudflare.account_id" },
    { path: "cloudflare.zone_id" },
    { path: "cloudflare.tunnel_id" },
    { path: "cloudflare.domain" },
  ]},
];
// modelSelect is a dropdown of a backend's live model list (Ollama's
// /api/tags or the ChatGPT catalog via /v1/chat/models). Until the list
// arrives it holds only the configured value; when the list can't be
// fetched (backend down, not signed in) it degrades to a text input so
// the model stays editable either way.
function modelSelect(f, val) {
  const sel = el("select", { id: "cfg-" + f.path },
    el("option", { value: val }, val || "(pick a model)"));
  j("/v1/chat/models?provider=" + f.models).then(res => {
    const models = res.models || [];
    if (!models.length) throw new Error("empty model list");
    sel.replaceChildren(...models.map(m => el("option", { value: m }, m)));
    if (val && !models.includes(val)) sel.append(el("option", { value: val }, val + " (configured)"));
    sel.value = val || models[0];
  }).catch(() => {
    sel.replaceWith(el("input", { id: "cfg-" + f.path, type: "text", value: val }));
  });
  return sel;
}
const getPath = (o, p) => p.split(".").reduce((a, k) => (a == null ? a : a[k]), o);
const setPath = (o, p, v) => { const ks = p.split("."); const last = ks.pop(); let t = o; for (const k of ks) { t[k] = t[k] || {}; t = t[k]; } t[last] = v; };
let loadedConfig = null;
let cfgActiveTab = 0;
function openConfigWin() {
  openWin("#win-config");
  loadConfig().catch(e => toast(e.message));
}
function showCfgPane(i) {
  cfgActiveTab = i;
  document.querySelectorAll("#cfg-tabs .tab").forEach((t, idx) => t.classList.toggle("active", idx === i));
  document.querySelectorAll("#cfg-form .pane").forEach((p, idx) => { p.hidden = idx !== i; });
}
async function loadConfig() {
  loadedConfig = await j("/v1/config");
  const bar = $("#cfg-tabs");
  const panel = $("#cfg-form");
  bar.replaceChildren();
  panel.replaceChildren();
  CONFIG_FIELDS.forEach((g, i) => {
    bar.append(el("div", { class: "tab" + (i === cfgActiveTab ? " active" : ""), onclick: () => showCfgPane(i) }, g.group));
    const pane = el("div", { class: "pane" });
    pane.hidden = i !== cfgActiveTab;
    if (g.group === "Cloudflare") {
      pane.append(el("div", { class: "row", style: "justify-content:flex-end; margin-bottom:8px" },
        el("button", { class: "ghost sm", onclick: () => wizOpen(false) }, "Cloudflare Setup Wizard…")));
    }
    if (g.group === "GitHub") pane.append(githubAuthBox());
    if (g.group === "VM Defaults") {
      pane.prepend(el("div", { class: "muted", style: "margin-bottom:8px" },
        "Chat uses host agents already signed in on this machine (Grok ~/.grok, Claude ~/.claude, Codex ~/.codex). There is no LLM tab — pick agent + model in the Chat window."));
    }
    if (g.group === "Daemon") {
      const tools = el("div", { class: "row", style: "justify-content:flex-end; margin-bottom:8px" });
      const rb = el("button", { class: "ghost sm" }, "Restart Daemon…");
      rb.onclick = () => daemonRestart(rb);
      tools.append(rb);
      pane.append(tools);
      j("/v1/tailscale").then(ts => {
        if (!ts.detected) return;
        const b = el("button", { class: "ghost sm", title: "Set listen to " + ts.ip + " (Tailscale)" }, "Bind to Tailscale IP");
        b.onclick = () => {
          const inp = document.getElementById("cfg-listen");
          const port = (inp.value.match(/:(\d+)\s*$/) || [null, "7777"])[1];
          inp.value = ts.ip + ":" + port;
          inp.focus();
          toast("listen set to " + inp.value + " — click Save to rebind. This page at " + location.host + " may stop responding after that.");
        };
        tools.prepend(b);
      }).catch(() => {});
    }
    const grid = el("div", { class: "grid" });
    for (const f of g.fields) {
      const val = getPath(loadedConfig, f.path);
      let input;
      if (f.models) {
        input = modelSelect(f, val == null ? "" : String(val));
      } else if (f.options) {
        input = el("select", { id: "cfg-" + f.path },
          ...f.options.map(o => el("option", { value: o }, o === "" ? "(default)" : o)));
        input.value = f.options.includes(val) ? val : f.options[0];
      } else {
        input = el("input", {
          id: "cfg-" + f.path,
          type: f.secret ? "password" : (f.num ? "number" : "text"),
          value: val == null ? "" : (f.list ? val.join(", ") : String(val)),
        });
      }
      // inside a group tab the "ollama." / "cloudflare." prefix is redundant
      const label = f.path.includes(".") ? f.path.split(".").pop() : f.path;
      const field = el("div", { class: "field" },
        el("label", { for: "cfg-" + f.path }, label + (f.restart ? " *" : "")), input);
      if (f.hint) field.append(el("div", { class: "hint" }, f.hint));
      grid.append(field);
    }
    pane.append(grid);
    panel.append(pane);
  });
  // populate placeholders elsewhere in the UI
  $("#c-cpus").placeholder = loadedConfig.default_cpus || "cpus";
  $("#c-mem").placeholder = loadedConfig.default_memory_mb || "mem MB";
  $("#c-disk").placeholder = loadedConfig.default_disk_gb || "disk GB";
}
async function daemonRestart(btn) {
  if (!btn.classList.contains("armed")) {
    btn.classList.add("armed");
    btn.textContent = "Sure? VMs restart too";
    setTimeout(() => { btn.classList.remove("armed"); btn.textContent = "Restart Daemon…"; }, 3500);
    return;
  }
  btn.disabled = true;
  btn.textContent = "Restarting…";
  try {
    await api("/v1/daemon/restart", { method: "POST", json: {} });
  } catch (e) {
    toast(e.message);
    btn.disabled = false;
    btn.classList.remove("armed");
    btn.textContent = "Restart Daemon…";
    return;
  }
  toast("Daemon restarting…");
  const t0 = Date.now();
  let back = false;
  while (Date.now() - t0 < 30000) {
    await new Promise(r => setTimeout(r, 700));
    try {
      const r2 = await fetch("/healthz", { cache: "no-store" });
      if (r2.ok) { back = true; break; }
    } catch (e) { /* still down */ }
  }
  if (back) {
    toast("Daemon is back — previously running VMs are starting again.");
    renderVMList._last = null;
    loadVMs().catch(() => {});
    loadConfig().catch(() => {});
    cfHeartbeat(true);
    chatDetect(true);
  } else {
    toast("Daemon didn't come back within 30s — check `exe serve` in the terminal.");
    btn.disabled = false;
    btn.classList.remove("armed");
    btn.textContent = "Restart Daemon…";
  }
}
$("#cfg-save").onclick = async () => {
  if (!loadedConfig) return;
  const nc = JSON.parse(JSON.stringify(loadedConfig));
  for (const g of CONFIG_FIELDS) {
    for (const f of g.fields) {
      // getElementById, not querySelector: ids like cfg-ollama.base_url
      // contain dots that a selector would parse as class names
      const raw = document.getElementById("cfg-" + f.path).value;
      setPath(nc, f.path, f.list ? raw.split(",").map(s => s.trim()).filter(Boolean)
        : f.num ? (f.path.endsWith("temperature") ? (parseFloat(raw) || 0) : (parseInt(raw, 10) || 0)) : raw);
    }
  }
  $("#cfg-save").disabled = true;
  try {
    const res = await j("/v1/config", { method: "PUT", json: nc });
    loadedConfig = nc;
    let msg = "saved";
    if (res.rebinding) msg += " — " + res.rebinding.join(" & ") + " rebinding now (reload the page if it moved)";
    if (res.restart_required) msg += " — click Restart Daemon… to apply: " + res.restart_required.join(", ");
    if (res.ingress_synced) {
      const n = res.ingress_synced.length;
      msg += n ? " — repointed " + n + " public route" + (n === 1 ? "" : "s") + ": " + res.ingress_synced.join(", ")
               : " — public routes already up to date";
    }
    if (res.ingress_warning) msg += " — " + res.ingress_warning;
    $("#cfg-status").textContent = msg;
    toast(msg);
    chatDetect(true);
    const gb = document.getElementById("cfg-gh-auth");
    if (gb) renderGitHubAuth(gb); // a freshly-saved client_id enables Sign In
  } catch (e) { toast(e.message); }
  $("#cfg-save").disabled = false;
};

// ---- Sign in with ChatGPT (Configuration → OpenAI) ----
// The daemon runs the Codex CLI's OAuth flow; the browser callback lands on
// localhost:1455 next to the daemon, so when the daemon is on another
// machine the user pastes the final redirect URL instead.
let oaiPoll = 0;
function openaiAuthBox() {
  const box = el("div", { style: "margin-bottom:10px" });
  renderOpenAIAuth(box);
  return box;
}
async function renderOpenAIAuth(box) {
  let st;
  try { st = await j("/v1/openai/status"); }
  catch (e) { box.replaceChildren(el("div", { class: "muted" }, "status: " + e.message)); return; }
  box.replaceChildren();
  const row = el("div", { class: "row" });
  if (st.authenticated) {
    const out = el("button", { class: "ghost sm danger" }, "Sign Out");
    out.onclick = () => openaiSignOut(out);
    row.append(el("span", { style: "flex:1" },
      "Signed in as ", el("b", {}, st.email || st.account_id || "?"),
      st.plan ? " — ChatGPT " + st.plan + " plan" : ""), out);
  } else {
    const go = el("button", { class: "btn" }, "Sign in with ChatGPT…");
    go.onclick = () => openaiSignIn(box, go);
    row.append(el("span", { class: "muted", style: "flex:1" }, "Not signed in."), go);
  }
  box.append(row);
  if (st.authenticated) {
    const ub = el("div", { style: "margin-top:8px" });
    box.append(ub);
    renderOpenAIUsage(ub);
  }
}
// Rate-limit usage for the signed-in subscription: the rolling 5-hour and
// weekly windows the ChatGPT backend meters, plus any credit balance.
async function renderOpenAIUsage(box) {
  const rows = el("div", { class: "wiz-review" });
  const row = (label, right) => rows.append(el("div", {},
    el("span", { class: "muted" }, label), right));
  const meter = (pct, hot, text) => el("span", { class: "oai-usage" },
    el("span", { class: "oai-meter" + (hot ? " hot" : "") },
      el("span", { style: "width:" + pct + "%" })), text);
  // Placeholder rows mirror the loaded layout so the tab doesn't jump
  // when the fetch resolves.
  row("5-hour limit", meter(0, false, el("span", { class: "muted" }, "loading…")));
  row("Weekly limit", meter(0, false, el("span", { class: "muted" }, "loading…")));
  box.replaceChildren(rows);
  let u;
  try { u = await j("/v1/openai/usage"); }
  catch (e) {
    rows.replaceChildren(el("div", {},
      el("span", { class: "muted" }, "Usage"), el("span", {}, e.message)));
    return;
  }
  rows.replaceChildren();
  const win = (label, w) => {
    const pct = Math.max(0, Math.min(100, Math.round(w.used_percent)));
    row(label, meter(pct, pct >= 90, pct + "% used — resets " + oaiResetAt(w.reset_at)));
  };
  const rl = u.rate_limit || {};
  if (rl.primary_window) win(oaiWindowLabel(rl.primary_window.limit_window_seconds, "5-hour"), rl.primary_window);
  if (rl.secondary_window) win(oaiWindowLabel(rl.secondary_window.limit_window_seconds, "Weekly"), rl.secondary_window);
  if (u.credits && (u.credits.unlimited || u.credits.has_credits))
    row("Credits", el("span", {}, u.credits.unlimited ? "unlimited" : u.credits.balance));
  if (!rows.children.length) box.replaceChildren();
}
function oaiWindowLabel(sec, fallback) {
  if (!sec) return fallback + " limit";
  const h = Math.round(sec / 3600);
  if (h < 24) return h + "-hour limit";
  const d = Math.round(h / 24);
  return d === 7 ? "Weekly limit" : d + "-day limit";
}
function oaiResetAt(unix) {
  if (!unix) return "?";
  const d = new Date(unix * 1000);
  const t = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  if (d.toDateString() === new Date().toDateString()) return t;
  return d.toLocaleDateString([], { weekday: "short" }) + " " + t;
}
async function openaiSignIn(box, btn) {
  btn.disabled = true;
  let res;
  try { res = await j("/v1/openai/oauth/start", { method: "POST", json: {} }); }
  catch (e) { toast(e.message); btn.disabled = false; return; }
  window.open(res.url, "_blank", "noopener");
  const inp = el("input", { placeholder: "http://localhost:1455/auth/callback?code=…", style: "flex:1" });
  const ok = el("button", { class: "btn" }, "Complete");
  ok.onclick = async () => {
    ok.disabled = true;
    try {
      const r2 = await j("/v1/openai/oauth/complete", { method: "POST", json: { input: inp.value } });
      openaiDone(r2.email, r2.plan);
    } catch (e) { toast(e.message); ok.disabled = false; }
  };
  box.replaceChildren(
    el("div", { class: "muted", style: "margin-bottom:6px" },
      "Waiting for the browser sign-in to finish… If no tab opened, ",
      el("a", { href: res.url, target: "_blank", rel: "noopener" }, "open the sign-in page"), "."),
    el("div", { class: "hint", style: "margin-bottom:6px" },
      "If the last page can't connect, edit its address: replace localhost with " +
      location.hostname + " (keep :1455 and the rest). Or paste that full URL here:"),
    el("div", { class: "row" }, inp, ok));
  clearInterval(oaiPoll);
  oaiPoll = setInterval(async () => {
    try {
      const st = await j("/v1/openai/status");
      if (st.authenticated) openaiDone(st.email, st.plan);
    } catch (e) { /* daemon briefly unreachable */ }
  }, 2000);
  setTimeout(() => clearInterval(oaiPoll), 10 * 60 * 1000);
}
function openaiDone(email, plan) {
  clearInterval(oaiPoll);
  toast("Signed in to ChatGPT as " + (email || "?") + (plan ? " — " + plan + " plan" : ""));
  loadConfig().catch(() => {});
  chatDetect(true);
}
async function openaiSignOut(btn) {
  if (!btn.classList.contains("armed")) {
    btn.classList.add("armed");
    btn.textContent = "Sure?";
    setTimeout(() => { btn.classList.remove("armed"); btn.textContent = "Sign Out"; }, 3000);
    return;
  }
  try { await j("/v1/openai/logout", { method: "POST", json: {} }); }
  catch (e) { toast(e.message); return; }
  toast("Signed out of ChatGPT.");
  loadConfig().catch(() => {});
  chatDetect(true);
}

// ---- Sign in with GitHub (device flow) ----
// The daemon fetches a device code and polls GitHub in the background; the
// user enters the code at github.com/login/device. Configuration → GitHub
// and the Publish modal share this box.
let ghPollTimer = 0;
function githubAuthBox() {
  const box = el("div", { id: "cfg-gh-auth", style: "margin-bottom:10px" });
  renderGitHubAuth(box);
  return box;
}
function ghAuthRow(left, right) {
  return el("div", { class: "row" }, el("span", { style: "flex:1" }, left), right);
}
async function renderGitHubAuth(box) {
  // placeholder mirrors the loaded row (text + button) so the pane
  // doesn't jump when the status arrives
  box.replaceChildren(ghAuthRow(el("span", { class: "muted" }, "GitHub: checking sign-in…"),
    el("button", { class: "btn", style: "visibility:hidden" }, "Sign in with GitHub…")));
  let st;
  try { st = await j("/v1/github/status"); }
  catch (e) { box.replaceChildren(el("div", { class: "muted" }, "status: " + e.message)); return; }
  if (box.id === "pub-auth") $("#pub-go").disabled = !st.authenticated;
  if (st.authenticated) {
    const out = el("button", { class: "ghost sm danger" }, "Sign Out");
    out.onclick = () => githubSignOut(out, box);
    box.replaceChildren(ghAuthRow(el("span", {}, "Signed in as ", el("b", {}, st.login || "?")), out));
  } else {
    const go = el("button", { class: "btn" }, "Sign in with GitHub…");
    go.onclick = () => githubSignIn(box, go);
    const why = st.configured ? "Not signed in."
      : box.id === "cfg-gh-auth" ? "Enter a client ID below, Save, then sign in."
      : "Set github.client_id in Configuration → GitHub first.";
    box.replaceChildren(ghAuthRow(el("span", { class: "muted" }, why), go));
    if (!st.configured) go.disabled = true;
  }
}
async function githubSignIn(box, btn) {
  btn.disabled = true;
  let res;
  try { res = await j("/v1/github/oauth/start", { method: "POST", json: {} }); }
  catch (e) { toast(e.message); btn.disabled = false; return; }
  window.open(res.verification_uri, "_blank", "noopener");
  box.replaceChildren(
    el("div", { class: "muted", style: "margin-bottom:2px" },
      "Enter this code at ",
      el("a", { href: res.verification_uri, target: "_blank", rel: "noopener" },
        res.verification_uri.replace(/^https:\/\//, "")), ":"),
    el("div", { class: "gh-code" }, res.user_code),
    el("div", { class: "muted" }, "Waiting for GitHub…"));
  clearInterval(ghPollTimer);
  ghPollTimer = setInterval(async () => {
    try {
      const st = await j("/v1/github/status");
      if (st.authenticated) {
        clearInterval(ghPollTimer);
        toast("Signed in to GitHub as " + (st.login || "?"));
        renderGitHubAuth(box);
      } else if (st.error) {
        clearInterval(ghPollTimer);
        toast("GitHub sign-in failed: " + st.error);
        renderGitHubAuth(box);
      }
    } catch (e) { /* daemon briefly unreachable */ }
  }, 2000);
  setTimeout(() => clearInterval(ghPollTimer), 15 * 60 * 1000);
}
async function githubSignOut(btn, box) {
  if (!btn.classList.contains("armed")) {
    btn.classList.add("armed");
    btn.textContent = "Sure?";
    setTimeout(() => { btn.classList.remove("armed"); btn.textContent = "Sign Out"; }, 3000);
    return;
  }
  try { await j("/v1/github/logout", { method: "POST", json: {} }); }
  catch (e) { toast(e.message); return; }
  toast("Signed out of GitHub.");
  renderGitHubAuth(box);
}

// ---- Publish to GitHub (VM context menu) ----
// The daemon does everything: commits in the VM if needed, creates the
// repository, and pushes through a one-shot host-side proxy — the GitHub
// token never enters the VM.
let pubVM = null;
function openPublishWin(name) {
  pubVM = name;
  $("#pub-title").textContent = "Publish " + name + " to GitHub";
  const log = $("#pub-log");
  log.hidden = true;
  log.replaceChildren();
  $("#pub-name").value = "";
  delete $("#pub-name").dataset.edited;
  $("#pub-go").disabled = true; // enabled by renderGitHubAuth once signed in
  $("#pub-overlay").hidden = false;
  renderGitHubAuth($("#pub-auth"));
  pubScan();
}
function pubClose() { $("#pub-overlay").hidden = true; }
$("#pub-x").onclick = pubClose;
$("#pub-cancel").onclick = pubClose;
$("#pub-name").addEventListener("input", () => { $("#pub-name").dataset.edited = "1"; });
async function pubScan() {
  // rebuild the select each open — a failed scan swaps it for a text input
  const sel = el("select", { id: "pub-dir", style: "width:100%", onchange: pubSyncName },
    el("option", { value: "" }, "scanning the VM…"));
  document.getElementById("pub-dir").replaceWith(sel);
  const freeform = () => sel.replaceWith(el("input", {
    id: "pub-dir", type: "text", placeholder: "/home/dev/myapp", spellcheck: "false",
    style: "width:100%", oninput: pubSyncName }));
  try {
    const res = await j("/v1/vms/" + pubVM + "/publish/scan");
    const dirs = res.dirs || [];
    if (!dirs.length) { freeform(); return; }
    sel.replaceChildren(...dirs.map(d => el("option", { value: d }, d)));
    pubSyncName();
  } catch (e) { freeform(); toast(e.message); }
}
function pubSyncName() {
  if ($("#pub-name").dataset.edited) return;
  const p = document.getElementById("pub-dir").value;
  $("#pub-name").value = (p.split("/").filter(Boolean).pop() || "").replace(/[^A-Za-z0-9._-]+/g, "-");
}
$("#pub-go").onclick = async () => {
  const path = document.getElementById("pub-dir").value.trim();
  const name = $("#pub-name").value.trim();
  if (!path) { toast("pick a project folder"); return; }
  if (!name) { toast("repository name required"); return; }
  const log = $("#pub-log");
  log.hidden = false;
  log.replaceChildren();
  const line = (cls, text) => {
    log.append(el("div", { class: cls }, text));
    log.scrollTop = log.scrollHeight;
  };
  $("#pub-go").disabled = true;
  try {
    const resp = await api("/v1/vms/" + pubVM + "/publish", { method: "POST",
      json: { path, repo: name, private: $("#pub-private").checked } });
    const rd = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await rd.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf("\n")) >= 0) {
        const l = buf.slice(0, i).trim();
        buf = buf.slice(i + 1);
        if (!l) continue;
        const ev = JSON.parse(l);
        if (ev.type === "step") line("", ev.text);
        else if (ev.type === "error") { line("err", ev.error); toast(ev.error); }
        else if (ev.type === "done") {
          line("", "Published.");
          log.append(el("div", {}, el("a", { href: ev.url, target: "_blank", rel: "noopener" }, ev.url)));
          log.scrollTop = log.scrollHeight;
          toast("Published " + ev.repo + " to GitHub");
        }
      }
    }
  } catch (e) { line("err", e.message); toast(e.message); }
  $("#pub-go").disabled = false;
};

// ---- cloudflare wizard ----
const WIZ_STEPS = [
  { key: "api_token", title: "API token", secret: true,
    desc: () => el("span", {},
      "Create a token at ", el("a", { href: "https://dash.cloudflare.com/profile/api-tokens", target: "_blank", rel: "noopener" }, "dash.cloudflare.com → API Tokens"),
      " → Create Token → Custom token, with two permissions: ",
      el("b", {}, "Zone → DNS → Edit"), " and ", el("b", {}, "Account → Cloudflare Tunnel → Edit"),
      ". Paste it below — it is validated against Cloudflare before moving on.") },
  { key: "account_id", title: "Account", optionsKey: "accounts",
    desc: () => el("span", {}, "The Cloudflare account that owns your tunnel. If no list appears, copy the Account ID from the dashboard's right sidebar on any zone page.") },
  { key: "zone_id", title: "Zone", optionsKey: "zones",
    desc: () => el("span", {}, "The zone (domain) whose DNS records exe will manage. Validation confirms the token can edit DNS there.") },
  { key: "tunnel_id", title: "Tunnel", optionsKey: "tunnels",
    desc: () => el("span", {}, "Your cloudflared tunnel (Zero Trust → Networks → Tunnels) — the one running on your LAN server. Remotely-managed tunnels work best: exe can push ingress rules to them via the API.") },
  { key: "domain", title: "Base domain",
    desc: () => el("span", {}, "Apps publish as ", el("span", { class: "mono" }, "<name>.<this domain>"),
      ". Use the zone apex (example.com) or a namespace like apps.example.com.") },
  { key: "review", title: "Review & save",
    desc: () => el("span", {}, "Everything validated. Save writes these values to the cloudflare section of your config (hot-reloaded, no restart needed).") },
];
const wiz = { idx: 0, values: {}, options: {}, zoneName: "" };

function wizOpen(fresh) {
  const c = fresh === true ? {} : (loadedConfig && loadedConfig.cloudflare) || {};
  wiz.idx = 0;
  wiz.values = { api_token: c.api_token || "", account_id: c.account_id || "",
    zone_id: c.zone_id || "", tunnel_id: c.tunnel_id || "", domain: c.domain || "" };
  wiz.options = {};
  wiz.zoneName = "";
  $("#wiz-overlay").hidden = false;
  wizRender();
}
function wizClose() { $("#wiz-overlay").hidden = true; }
$("#wiz-x").onclick = wizClose;
document.addEventListener("keydown", e => {
  if (e.key !== "Escape") return;
  menuClose();
  if (!$("#wiz-overlay").hidden) wizClose();
  if (!$("#sum-overlay").hidden) $("#sum-overlay").hidden = true;
  if (!$("#join-overlay").hidden) joinClose();
  if (!$("#cb-overlay").hidden) $("#cb-overlay").hidden = true;
  if (!$("#nf-overlay").hidden) nfClose();
  if (!$("#pub-overlay").hidden) pubClose();
  if (!$("#ask-overlay").hidden) askFinish(false);
  if (!$("#desk-overlay").hidden) deskClose();
});

function wizFeedback(kind, text) {
  const fb = $("#wiz-fb");
  fb.replaceChildren();
  if (text) fb.append(el("div", { class: "wiz-fb " + kind }, text));
}

function wizOptionLabel(key, o) {
  if (key === "tunnels") return `${o.name} — ${o.status}${o.remote_config ? "" : " (locally managed)"}`;
  return `${o.name} (${o.id.slice(0, 8)}…)`;
}

function wizRender() {
  const step = WIZ_STEPS[wiz.idx];
  $("#wiz-title").textContent = "Cloudflare Setup — " + step.title;
  $("#wiz-ind").textContent = `Step ${wiz.idx + 1} of ${WIZ_STEPS.length}`;
  $("#wiz-desc").replaceChildren(step.desc());
  $("#wiz-back").style.visibility = wiz.idx === 0 ? "hidden" : "visible";
  $("#wiz-next").textContent = step.key === "review" ? "Save Configuration" : "Validate & Continue";
  wizFeedback("", "");
  const box = $("#wiz-input");
  box.replaceChildren();

  if (step.key === "review") {
    const rev = el("div", { class: "wiz-review mono", style: "font-size:12px" });
    const show = v => v || "—";
    rev.append(
      el("div", {}, el("span", { class: "muted" }, "api_token"), el("span", {}, wiz.values.api_token ? "••••" + wiz.values.api_token.slice(-4) : "—")),
      el("div", {}, el("span", { class: "muted" }, "account_id"), el("span", {}, show(wiz.values.account_id))),
      el("div", {}, el("span", { class: "muted" }, "zone_id"), el("span", {}, show(wiz.values.zone_id) + (wiz.zoneName ? ` (${wiz.zoneName})` : ""))),
      el("div", {}, el("span", { class: "muted" }, "tunnel_id"), el("span", {}, show(wiz.values.tunnel_id))),
      el("div", {}, el("span", { class: "muted" }, "domain"), el("span", {}, show(wiz.values.domain))));
    box.append(rev);
    if (loadedConfig && !loadedConfig.advertise_host) {
      box.append(el("div", { class: "wiz-fb warn", style: "margin-top:10px" },
        "advertise_host is empty — after saving, set it in Config to this Mac's LAN or Tailscale IP (as reachable from the tunnel server), or published hostnames won't route back here."));
    }
    return;
  }

  const opts = step.optionsKey ? wiz.options[step.optionsKey] : null;
  if (opts && opts.length) {
    const sel = el("select", { id: "wiz-val" });
    for (const o of opts) {
      const opt = el("option", { value: o.id }, wizOptionLabel(step.optionsKey, o));
      if (o.id === wiz.values[step.key]) opt.selected = true;
      sel.append(opt);
    }
    sel.append(el("option", { value: "__manual" }, "Enter an ID manually…"));
    sel.addEventListener("change", () => {
      if (sel.value === "__manual") {
        box.replaceChildren(el("input", { id: "wiz-val", type: "text", value: wiz.values[step.key] || "", placeholder: step.key }));
        box.querySelector("input").focus();
      }
    });
    box.append(sel);
  } else {
    const input = el("input", {
      id: "wiz-val",
      type: step.secret ? "password" : "text",
      value: wiz.values[step.key] || "",
      placeholder: step.key === "domain" && wiz.zoneName ? wiz.zoneName : step.key,
    });
    input.addEventListener("keydown", e => { if (e.key === "Enter") $("#wiz-next").click(); });
    box.append(input);
  }
}

$("#wiz-back").onclick = () => { if (wiz.idx > 0) { wiz.idx--; wizRender(); } };
$("#wiz-next").onclick = async () => {
  const step = WIZ_STEPS[wiz.idx];
  const btn = $("#wiz-next");

  if (step.key === "review") {
    btn.disabled = true;
    try {
      const cur = await j("/v1/config");
      cur.cloudflare = {
        api_token: wiz.values.api_token, account_id: wiz.values.account_id,
        zone_id: wiz.values.zone_id, tunnel_id: wiz.values.tunnel_id, domain: wiz.values.domain,
      };
      await j("/v1/config", { method: "PUT", json: cur });
      toast("Cloudflare configuration saved");
      wizClose();
      loadConfig().catch(() => {});
      cfHeartbeat(true);
    } catch (e) { wizFeedback("err", e.message); }
    btn.disabled = false;
    return;
  }

  let val = $("#wiz-val").value.trim();
  if (val === "__manual") { wizFeedback("err", "Pick an entry or choose manual input."); return; }
  if (step.key === "domain" && !val && wiz.zoneName) val = wiz.zoneName;
  wiz.values[step.key] = val;

  btn.disabled = true;
  btn.textContent = "Checking…";
  wizFeedback("", "");
  try {
    const res = await j("/v1/cloudflare/wizard", { method: "POST", json: {
      step: step.key, zone_name: wiz.zoneName,
      api_token: wiz.values.api_token, account_id: wiz.values.account_id,
      zone_id: wiz.values.zone_id, tunnel_id: wiz.values.tunnel_id, domain: wiz.values.domain,
    }});
    if (!res.ok) {
      wizFeedback("err", res.message || "validation failed");
    } else {
      for (const k of ["accounts", "zones", "tunnels"]) if (res[k]) wiz.options[k] = res[k];
      if (res.zone_name) wiz.zoneName = res.zone_name;
      wizFeedback("ok", res.message + (res.warning ? "\n\n⚠ " + res.warning : ""));
      setTimeout(() => { wiz.idx++; wizRender(); }, res.warning ? 2200 : 700);
    }
  } catch (e) { wizFeedback("err", e.message); }
  btn.disabled = false;
  btn.textContent = WIZ_STEPS[wiz.idx].key === "review" ? "Save Configuration" : "Validate & Continue";
};

// ---- cloudflare heartbeat ----
let cfLast = null;
async function cfHeartbeat(force) {
  try {
    const h = await j("/v1/cloudflare/health" + (force ? "?force=1" : ""));
    cfLast = h;
    $("#cf-dot-circle").style.background =
      `radial-gradient(circle at 30% 28%, rgba(255,255,255,.85), rgba(255,255,255,0) 55%), ${h.status === "ok" ? "var(--green)" : "#e0c341"}`;
    $("#cf-dot").title = h.message || ("Cloudflare status: " + h.status);
  } catch (e) { /* keep last known color */ }
}
$("#cf-dot").onclick = () => {
  if (cfLast && cfLast.status === "ok") sumOpen();
  else wizOpen(false);
};

// ---- cloudflare status summary ----
async function sumOpen() {
  $("#sum-overlay").hidden = false;
  $("#sum-body").replaceChildren(el("span", { class: "muted" }, "loading…"));
  try { loadedConfig = await j("/v1/config"); } catch (e) {}
  renderSum();
}
function renderSum() {
  const c = (loadedConfig && loadedConfig.cloudflare) || {};
  const body = $("#sum-body");
  body.replaceChildren();
  if (cfLast) {
    body.append(el("div", { class: "wiz-fb " + (cfLast.status === "ok" ? "ok" : "warn") },
      cfLast.message || cfLast.status));
  }
  const rev = el("div", { class: "wiz-review mono", style: "font-size:12px; margin-top:12px" });
  const row = (k, v) => el("div", {}, el("span", { class: "muted" }, k), el("span", {}, v || "—"));
  const mask = v => (v && v.length >= 12) ? v.slice(0, 4) + "••••" + v.slice(-4) : v;
  rev.append(
    row("api_token", c.api_token ? "••••" + c.api_token.slice(-4) : ""),
    row("account_id", mask(c.account_id)),
    row("zone_id", mask(c.zone_id)),
    row("tunnel_id", cfLast && cfLast.tunnel_name && c.tunnel_id
      ? cfLast.tunnel_name + " (" + mask(c.tunnel_id) + ")" : mask(c.tunnel_id)),
    row("domain", c.domain),
    row("last checked", cfLast && cfLast.checked_at ? new Date(cfLast.checked_at).toLocaleString() : ""));
  body.append(rev);
}
$("#sum-x").onclick = () => { $("#sum-overlay").hidden = true; };
$("#sum-recheck").onclick = async () => {
  const b = $("#sum-recheck");
  b.disabled = true;
  b.textContent = "Checking…";
  await cfHeartbeat(true);
  renderSum();
  b.disabled = false;
  b.textContent = "Re-check Now";
};
$("#sum-restart").onclick = () => {
  $("#sum-overlay").hidden = true;
  wizOpen(true);
};
setInterval(() => { if (!document.hidden) cfHeartbeat(false); }, 30000);
document.addEventListener("visibilitychange", () => { if (!document.hidden) cfHeartbeat(false); });

// ---- node sync: Join dialog (Special ▸ Join…) ----
// Latency refreshes every 5 s while the dialog is open (the daemon probes
// its peers and caches for 5 s; ?force=1 bypasses that cache), matching the
// VM list's polling cadence.
let joinTimer = null;
function joinOpen() {
  $("#join-overlay").hidden = false;
  $("#join-fb").textContent = "";
  joinRefresh(true);
  clearInterval(joinTimer);
  joinTimer = setInterval(() => {
    if (!document.hidden && !$("#join-overlay").hidden) joinRefresh(false);
  }, 5000);
}
function joinClose() {
  $("#join-overlay").hidden = true;
  clearInterval(joinTimer);
  joinTimer = null;
}
$("#join-x").onclick = joinClose;

async function joinRefresh(full) {
  try {
    if (full) {
      // /v1/peers returns cached statuses so the dialog paints instantly;
      // follow with a real probe (blocks server-side up to 4 s per
      // unreachable peer) to fill in fresh online/latency.
      const d = await j("/v1/peers");
      renderJoinSelf(d.self || {});
      renderJoinPeers(d.peers || []);
      const f = await j("/v1/peers/status?force=1");
      renderJoinPeers(f.peers || []);
    } else {
      const d = await j("/v1/peers/status?force=1");
      renderJoinPeers(d.peers || []);
    }
  } catch (e) {
    $("#join-self").textContent = "Unavailable: " + e.message;
  }
}

function renderJoinSelf(self) {
  const addr = self.ip ? self.ip + ":" + self.port
    : "no Tailscale IP detected — peers must reach port " + (self.port || "7777");
  $("#join-self").textContent = (self.name || "this node") + "  ·  " + addr + "  ·  " + (self.id || "");
}

function renderJoinPeers(peers) {
  const box = $("#join-peers");
  box.replaceChildren();
  if (!peers.length) {
    box.append(el("div", { class: "muted" }, "Not joined with any node yet."));
    return;
  }
  for (const p of peers) {
    const leave = el("button", { class: "ghost sm" }, "Leave");
    leave.onclick = () => {
      if (!leave.classList.contains("armed")) {
        leave.classList.add("armed");
        leave.textContent = "Sure?";
        setTimeout(() => { leave.classList.remove("armed"); leave.textContent = "Leave"; }, 3000);
        return;
      }
      api("/v1/peers/" + encodeURIComponent(p.id), { method: "DELETE" })
        .then(() => { toast("Left " + (p.name || p.id)); joinRefresh(true); })
        .catch(e => toast(e.message));
    };
    // online=false with no error means "not probed yet" (cached paint on
    // open) — show a pending "…" instead of flashing the peer as offline.
    const probing = !p.online && !p.error && !p.last_seen;
    const dot = el("span", { class: "dot", title: p.online ? "online" : probing ? "checking…" : (p.error || "offline") });
    dot.style.background = p.online ? "var(--green)" : "var(--gray)";
    box.append(el("div", { class: "join-peer" },
      dot,
      el("span", { class: "join-name", title: p.id + (p.addr ? " · " + p.addr : "") }, p.name || p.id),
      el("span", { class: "mono muted" }, p.addr || ""),
      el("span", { class: "join-lat" }, p.online ? p.latency_ms + " ms" : probing ? "…" : "—"),
      leave));
  }
}

$("#join-mint").onclick = async () => {
  try {
    const d = await j("/v1/peers/code", { method: "POST" });
    $("#join-mycode").textContent = d.code;
    $("#join-mycode").title = "Single use, valid 10 minutes — type it into the other node's Join dialog";
  } catch (e) { toast(e.message); }
};

$("#join-go").onclick = async () => {
  const addr = $("#join-addr").value.trim(), code = $("#join-code").value.trim();
  const fb = $("#join-fb");
  if (!addr || !code) { fb.textContent = "Enter the other node's IP and its join code."; return; }
  $("#join-go").disabled = true;
  fb.textContent = "Joining…";
  try {
    const d = await j("/v1/peers/join", { method: "POST", json: { addr: addr, code: code } });
    fb.textContent = "Joined " + ((d.joined && d.joined.name) || addr) + " — syncing.";
    $("#join-addr").value = ""; $("#join-code").value = "";
    joinRefresh(true);
  } catch (e) {
    fb.textContent = e.message;
  }
  $("#join-go").disabled = false;
};

// ---- app-data sync events -> open app windows ----
// When the sync engine lands a peer's change on disk, the daemon announces
// it over SSE; forward it into the matching app's iframe so an open window
// reloads/merges instead of clobbering the synced-in doc with stale memory.
let appES = null;
function appDataSubscribe() {
  if (appES) appES.close();
  const tok = tokenInput.value ? "?token=" + encodeURIComponent(tokenInput.value) : "";
  appES = new EventSource("/v1/apps/events" + tok);
  appES.onmessage = e => {
    let ev;
    try { ev = JSON.parse(e.data); } catch (err) { return; }
    if (!ev || !ev.app) return;
    if (ev.app === "@workspace") { workspaceChanged(ev.path, !!ev.deleted, ev.client || ""); return; }
    // a feed item landed (posted here or synced in) — refresh an open window
    if (ev.app === "Newsfeed") { if (!$("#win-news").hidden) loadNews().catch(() => {}); return; }
    // Forward even to a closed window: closeWin only hides the iframe, which
    // keeps running, so a hidden app must still hear the change or it would
    // reopen with stale in-memory state and clobber the synced-in doc.
    const w = document.getElementById("win-app-" + ev.app);
    const fr = w && w.querySelector(".app-frame");
    if (fr && fr.contentWindow) {
      fr.contentWindow.postMessage(
        { exe: "data-changed", path: ev.path, deleted: !!ev.deleted, client: ev.client || "" },
        location.origin);
    }
  };
}
tokenInput.addEventListener("change", () => { if (appES) appDataSubscribe(); });

// A workspace change landed — a peer's file synced in, or another window on
// this node wrote one. Refresh every open Workspace window, plus any editor
// (unless it holds unsaved typing) or picture viewer on the changed file.
function workspaceChanged(path, deleted, client) {
  if (client && client === WIN_CLIENT) return; // our own write; already refreshed
  document.querySelectorAll('[id^="win-ws-"]').forEach(w => { if (!w.hidden) loadFinder(w); });
  const ed = document.getElementById("win-ed-" + encodeURIComponent(path));
  if (ed && !ed.hidden) deleted ? closeWin(ed) : loadEditor(ed);
  const iv = document.getElementById("win-iv-" + encodeURIComponent(path));
  if (iv && !iv.hidden) deleted ? closeWin(iv) : loadImage(iv);
}

// ---- boot ----
if (IS_MOBILE) $("#vm-hint").textContent = "double-click a VM to open it";
syncMobileMode();
winRestore();
appDataSubscribe();
renderVMHead();
loadVMs().catch(e => toast(e.message));
loadApps().catch(() => {});
loadConfig().catch(() => {});
cfHeartbeat(false);
chatDetect(false);
pollHostStats();
loadDesktop().catch(() => {});
setInterval(() => { if (!document.hidden) loadVMs().catch(() => {}); }, 5000);
setInterval(() => { if (!document.hidden) pollHostStats(); }, 2000);
if (!sessionStorage.getItem("exe_startup_note")) {
  sessionStorage.setItem("exe_startup_note", "1");
  platAsk(
    "Terminal copy/paste (host + VM): hold Ctrl, select text, right-click to copy. Ctrl + right-click with no selection pastes.\n\n" +
    "Desk app icons drag like Workspace — not as files. Editor opens from Workspace text files.\n\n" +
    "Chat picks a host agent + model (Grok / Claude / Codex) already signed in on this machine.\n\n" +
    "Expose publishes a VM port via Cloudflare. Transcripts are host logs of built-in Agent runs only.",
    { title: "exe", note: true, ok: "OK" }
  );
}
