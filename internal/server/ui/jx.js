// jstdlee desktop extensions; see docs/jstdlee/PLAN.md
//
// Loaded after the desktop's one classic script, so every top-level
// function declaration there is a property of window that can be wrapped
// here (callers look the name up at call time), and its let/const
// bindings are reachable by bare name. Nothing here redeclares a name of
// that script: all of it lives inside this one function.
(() => {
  "use strict";
  const W = window;
  const safely = (what, fn) => { try { fn(); } catch (e) { console.warn("jx: " + what + ":", e); } };
  const store = {
    get: (k, d) => { try { const v = localStorage.getItem(k); return v === null ? d : v; } catch (e) { return d; } },
    set: (k, v) => { try { localStorage.setItem(k, v); } catch (e) {} },
  };

  // ==================================================================
  // 1. Only Claude Code and Codex: the removed agent UI goes away
  // ==================================================================
  // The daemon answers these 410 (jx.go jxRemoved); the desktop never asks.
  const REMOVED = p => /^\/v1\/chat\/(send|sessions)(?=$|[/?])/.test(p)
    || /^\/v1\/vms\/[^/?]+\/(agent|memory|transcripts)(?=$|[/?])/.test(p);

  // api(): the removed routes fail at once without a request, and the
  // first 401 opens the Set API Token window with a note — once, until the
  // token is changed, so a desk full of polls never loops on it
  let tokenAsked = false;
  const tokenNote = el("div", { class: "jx-token-note", hidden: "" },
    "The daemon refused a request (401 Unauthorized). Enter its API token — api_token in ~/.exe/config.json on the host.");
  safely("token note", () => {
    const body = $("#win-token .win-body");
    if (body) body.prepend(tokenNote);
    tokenInput.addEventListener("change", () => { tokenAsked = false; tokenNote.hidden = true; });
    const done = $("#token-done");
    if (done) done.addEventListener("click", () => { tokenNote.hidden = true; });
  });
  function askToken() {
    if (tokenAsked) return;
    tokenAsked = true;
    tokenNote.hidden = false;
    safely("token window", () => ACTIONS.token());
  }
  safely("api", () => {
    const orig = W.api;
    W.api = async function jxApi(path, opts) {
      if (typeof path === "string" && REMOVED(path)) {
        const e = new Error("removed in this build: use the Board with Claude Code or Codex");
        e.status = 410;
        throw e;
      }
      try {
        return await orig.call(this, path, opts);
      } catch (e) {
        if (e && e.message === "HTTP 401") askToken();
        throw e;
      }
    };
  });

  // Configuration: the Ollama and OpenAI groups go (the chat backends);
  // boot's loadConfig is still waiting on its fetch when this runs, so the
  // first render already lacks them. showOpenAIUsage stays: the Codex
  // window's usage meter uses it.
  safely("config groups", () => {
    for (let i = CONFIG_FIELDS.length - 1; i >= 0; i--)
      if (CONFIG_FIELDS[i].group === "Ollama" || CONFIG_FIELDS[i].group === "OpenAI") CONFIG_FIELDS.splice(i, 1);
    if (cfgActiveTab >= CONFIG_FIELDS.length) cfgActiveTab = 0;
  });

  // Chat: its icon, menu item, window and backend switcher never show
  // (jx.css hides them for good; this keeps the state honest)
  safely("chat", () => {
    W.chatDetect = async function () {
      try { chatDetected = false; } catch (e) {}
      const ic = $("#chat-icon"), it = $("#dd-chat-item");
      if (ic) ic.hidden = true;
      if (it) it.hidden = true;
    };
    W.chatDetect();
    // anything still reaching for Chat lands on the Board
    W.openChatWin = () => openBoard();
    delete ACTIONS.winchat;
    delete DESK_ACTS.chat;
    delete DESK_ACTS.winchat;
    WIN_SYNC_OPEN.delete("win-chat");
    MOB_RESTORE.delete("win-chat");
    WIN_OPENERS["win-chat"] = () => {};
    const cw = $("#win-chat");
    if (cw && !cw.hidden) closeWin(cw);
    const cb = $("#cb-overlay");
    if (cb) cb.hidden = true;
  });

  // the VM window: no Agent or Sessions tab (jx.css), no agent memory under
  // the notes, and a deep link to either lands on Services
  safely("vm window", () => {
    const showPane0 = W.showPane;
    W.showPane = function (key) { return showPane0.call(this, key === "vibe" || key === "sess" ? "svc" : key); };
    W.loadVMSessions = async () => {};
    W.loadMemory = async () => {
      const h = $("#n-mem-head"), m = $("#n-memory");
      if (h) h.hidden = true;
      if (m) m.hidden = true;
    };
    const hint = $("#pane-notes .row > span.muted");
    if (hint) hint.textContent = "Notes about this VM — saved automatically.";
  });

  // menus: no "Chat with this VM", no chat lines in the desk menu
  safely("menus", () => {
    const vmCtx0 = W.vmCtxItems;
    W.vmCtxItems = function (vm) {
      return tidy(vmCtx0.call(this, vm).filter(it => !(it && typeof it === "object" && it.label === "Chat with this VM")));
    };
    const CHAT = new Set(["chat", "winchat"]);
    const prune = nodes => (nodes || []).filter(n => !(n && CHAT.has(n.action)))
      .map(n => n && n.items ? Object.assign({}, n, { items: prune(n.items) }) : n);
    const dmi0 = W.deskMenuItems;
    W.deskMenuItems = function (nodes) { return tidy(dmi0.call(this, prune(nodes))); };
  });
  // a menu that lost lines never shows two rules in a row, or one at an end
  function tidy(items) {
    const out = [];
    for (const it of items) {
      if (it === "-" && (!out.length || out[out.length - 1] === "-")) continue;
      out.push(it);
    }
    while (out[out.length - 1] === "-") out.pop();
    return out;
  }

  // ==================================================================
  // 2. The Board on the desktop
  // ==================================================================
  // The sysapp brings its own icon (sysapps/board). The Windows menu gets
  // a line for it, ACTIONS.board serves the menu bar, and the desk menu
  // can say "app board" (or "board").
  function openBoard() { openAppWin("board"); }
  safely("board menu", () => {
    ACTIONS.board = openBoard;
    DESK_ACTS.board = { run: openBoard };
    const dd = $("#dd-windows");
    if (dd && !dd.querySelector('[data-act="board"]')) {
      const item = el("div", { class: "dd-item", "data-act": "board" }, "Board");
      item.addEventListener("click", () => { menuClose(); openBoard(); });
      const after = dd.querySelector('[data-act="winmyapps"]');
      if (after) after.after(item); else dd.prepend(item);
    }
  });

  // ==================================================================
  // 3. Terminals on phones: key bar, compose box, select mode
  // ==================================================================
  // Every terminal comes from newTerm(); the wrapped one patches the new
  // Terminal's open(el), which tells which window it lives in, and its
  // input path, where a one-shot Ctrl or Alt from the bar meets the next
  // key typed on the phone's own keyboard.
  const KIT_PREF = "jx_keybar"; // "1" or "0": the desktop's last choice
  const HIST_KEY = "jx_compose_hist";
  safely("newTerm", () => {
    const newTerm0 = W.newTerm;
    W.newTerm = function () {
      const t = newTerm0.apply(this, arguments);
      safely("hook terminal", () => hookTerm(t));
      return t;
    };
  });
  function hookTerm(t) {
    const open0 = t.open.bind(t);
    t.open = parent => {
      open0(parent);
      safely("attach kit", () => attachKit(t, parent));
    };
    const cs = t._core && t._core.coreService;
    if (cs && typeof cs.triggerDataEvent === "function") {
      const trigger0 = cs.triggerDataEvent.bind(cs);
      cs.triggerDataEvent = (data, user) => {
        const k = t._jxKit;
        if (k && user && typeof data === "string" && data && (k.ctrl || k.alt)) data = k.modify(data);
        return trigger0(data, user);
      };
      t._jxSend = data => trigger0(data, true); // raw bytes, past the modifiers
    }
  }

  // Ctrl turns the next character into its control code; Alt prefixes ESC;
  // an arrow takes them as xterm's modifier parameter
  function ctrlOf(ch) {
    const c = ch.charCodeAt(0);
    if (c >= 97 && c <= 122) return String.fromCharCode(c - 96);
    if (c >= 64 && c <= 95) return String.fromCharCode(c - 64); // @ A–Z [ \ ] ^ _
    if (ch === " " || ch === "2") return "\x00";
    if (ch === "?" || ch === "8") return "\x7f";
    if (ch === "3") return "\x1b";
    if (ch === "4") return "\x1c";
    if (ch === "5") return "\x1d";
    if (ch === "6") return "\x1e";
    if (ch === "7" || ch === "/" || ch === "-") return "\x1f";
    return ch;
  }
  const ARROWS = { up: "A", down: "B", right: "C", left: "D" };

  function attachKit(t, box) {
    const w = box.closest(".window");
    if (!w) return;
    const vmTab = box.id === "term-box";
    if (!vmTab && !w.classList.contains("term-window")) return;
    const kit = w._jxKit || (w._jxKit = makeKit(w, box, vmTab));
    kit.term = t;
    kit.box = box;
    t._jxKit = kit;
    w._term = t;
    kit.disarm();
    kit.select(false);
    if (vmTab) wireVMTabTouch(t, box);
  }

  function makeKit(w, box, vmTab) {
    const kit = { w, box, term: null, ctrl: false, alt: false, ctrlLock: false, altLock: false, shown: false, composing: false };
    const keys = el("div", { class: "jx-keys" });
    const root = el("div", { class: "jx-kit", hidden: "" }, keys);
    const btn = {};
    const KEYS = [
      ["Esc", "\x1b", "Escape"], ["Tab", "\t", "Tab"], ["Ctrl", "ctrl", "Control: the next key (hold to lock)"],
      ["Alt", "alt", "Alt/Meta: the next key (hold to lock)"],
      ["↑", "up", "Up"], ["↓", "down", "Down"], ["←", "left", "Left"], ["→", "right", "Right"],
      ["PgUp", "\x1b[5~", "Page Up"], ["PgDn", "\x1b[6~", "Page Down"],
      ["|", "|"], ["~", "~"], ["/", "/"], ["-", "-"],
      ["^C", "\x03", "Interrupt (Ctrl+C)"], ["Compose", "compose", "Write a line or a block here, then Send it"],
      ["Select", "select", "Show the text as plain, selectable text"],
    ];
    for (const [label, what, tip] of KEYS) {
      const b = el("button", { class: "ghost jx-key", type: "button", title: tip || label, tabindex: "-1" }, label);
      btn[what] = b;
      // the press never takes the focus: the phone's keyboard stays up and
      // xterm keeps the keys
      b.addEventListener("pointerdown", e => { e.preventDefault(); });
      b.addEventListener("mousedown", e => { e.preventDefault(); });
      if (what === "ctrl" || what === "alt") {
        let held = 0, locked = false;
        b.addEventListener("pointerdown", () => {
          locked = false;
          clearTimeout(held);
          held = setTimeout(() => { locked = true; kit.lock(what); }, 500);
        });
        const cancel = () => clearTimeout(held);
        b.addEventListener("pointerup", cancel);
        b.addEventListener("pointerleave", cancel);
        b.addEventListener("pointercancel", cancel);
        b.addEventListener("contextmenu", e => e.preventDefault());
        b.addEventListener("click", () => { if (locked) { locked = false; return; } kit.arm(what); });
      } else b.addEventListener("click", () => kit.press(what));
      keys.append(b);
    }

    // the compose box: a native field the phone's keyboard, dictation and
    // IME treat as any text field; Send pastes it into the terminal
    const ta = el("textarea", { class: "jx-ta", rows: "2", spellcheck: "true", autocapitalize: "off",
      placeholder: "Compose here — Send pastes it into the terminal" });
    const hist = el("select", { "aria-label": "Recent" });
    const enter = el("input", { type: "checkbox" });
    enter.checked = store.get("jx_compose_enter", "1") === "1";
    enter.onchange = () => store.set("jx_compose_enter", enter.checked ? "1" : "0");
    const sendB = el("button", { class: "ghost", type: "button" }, "Send");
    const clearB = el("button", { class: "ghost", type: "button" }, "Clear");
    const compose = el("div", { class: "jx-compose", hidden: "" }, ta,
      el("div", { class: "jx-crow" },
        el("span", { class: "popup jx-hist" }, hist, el("span", { class: "well" }, el("i", {}, el("b")))),
        el("label", { class: "jx-check", title: "Press Return after the text" }, enter, " Return"),
        el("span", { class: "jx-grow" }), clearB, sendB));
    root.append(compose);
    const fillHist = () => {
      const h = history();
      hist.replaceChildren(el("option", { value: "" }, h.length ? "Recent…" : "No recent"),
        ...h.map((s, i) => el("option", { value: String(i) }, s.replace(/\s+/g, " ").slice(0, 60))));
      hist.value = "";
    };
    hist.onchange = () => {
      const h = history(), i = parseInt(hist.value, 10);
      if (h[i] !== undefined) { ta.value = h[i]; fitTA(); ta.focus(); }
      hist.value = "";
    };
    const fitTA = () => {
      ta.style.height = "auto";
      const max = Math.max(60, Math.round(window.innerHeight * 0.3));
      ta.style.height = Math.min(max, Math.max(40, ta.scrollHeight + 2)) + "px";
    };
    ta.addEventListener("input", fitTA);
    ta.addEventListener("keydown", e => {
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !e.isComposing) { e.preventDefault(); kit.send(); }
      e.stopPropagation(); // the desktop's own shortcuts stay out of the field
    });
    sendB.onclick = () => kit.send();
    clearB.onclick = () => { ta.value = ""; fitTA(); ta.focus(); };

    // select mode: the screen and its scrollback as plain text, selected
    // with the system's own handles, copied with a button
    const pre = el("pre", { class: "jx-sel-text" });
    const copyB = el("button", { class: "ghost", type: "button" }, "Copy");
    const doneB = el("button", { class: "ghost", type: "button" }, "Done");
    const selNote = el("span", { class: "jx-grow jx-sel-note" }, "Select text, then Copy.");
    const sel = el("div", { class: "jx-sel", hidden: "" }, pre, el("div", { class: "jx-sel-bar" }, selNote, copyB, doneB));
    for (const ev of ["touchstart", "touchmove", "touchend", "wheel", "mousedown", "contextmenu", "keydown"])
      sel.addEventListener(ev, e => e.stopPropagation(), { passive: true });
    copyB.onclick = async () => {
      const s = window.getSelection();
      const picked = s && !s.isCollapsed && pre.contains(s.anchorNode) ? s.toString() : "";
      await copyText(picked || pre.textContent, picked ? "selection" : "all of the text");
    };
    doneB.onclick = () => kit.select(false);

    // where it goes: under the screen, before the held row, in a terminal
    // window; under the box in the VM window's Terminal tab. The select
    // layer sits over the screen in the screen's own container
    const host = vmTab ? box.parentElement : w.querySelector(".win-body");
    if (vmTab) box.after(root);
    else {
      const frame = w.querySelector(".win-frame"), held = frame && frame.querySelector(":scope > .held");
      if (held) frame.insertBefore(root, held); else if (frame) frame.append(root);
    }
    host.classList.add("jx-host");
    host.append(sel);

    kit.refit = () => requestAnimationFrame(() => {
      if (vmTab) {
        host.style.setProperty("--jx-kit-h", (kit.shown ? root.offsetHeight : 0) + "px");
        safely("refit", () => termResize());
      } else if (typeof w._fit === "function") safely("refit", () => w._fit());
    });
    kit.setShown = on => {
      kit.shown = !!on;
      root.hidden = !kit.shown;
      host.classList.toggle("jx-has-kit", kit.shown);
      if (!kit.shown) { kit.disarm(); kit.compose(false); }
      kit.refit();
    };
    kit.toggle = () => {
      kit.setShown(!kit.shown);
      if (!IS_MOBILE) store.set(KIT_PREF, kit.shown ? "1" : "0");
    };
    kit.paint = () => {
      btn.ctrl.classList.toggle("on", kit.ctrl && !kit.ctrlLock);
      btn.ctrl.classList.toggle("lock", kit.ctrlLock);
      btn.alt.classList.toggle("on", kit.alt && !kit.altLock);
      btn.alt.classList.toggle("lock", kit.altLock);
      btn.compose.classList.toggle("lock", kit.composing);
    };
    kit.disarm = () => { kit.ctrl = kit.alt = kit.ctrlLock = kit.altLock = false; kit.paint(); };
    kit.arm = which => {
      const lock = which === "ctrl" ? "ctrlLock" : "altLock";
      if (kit[lock]) { kit[lock] = false; kit[which] = false; }
      else kit[which] = !kit[which];
      kit.paint();
      // the next key is typed on the phone's keyboard: bring it up
      if (kit[which] && kit.term && document.activeElement !== kit.term.textarea) safely("focus", () => kit.term.focus());
    };
    kit.lock = which => {
      kit[which] = true;
      kit[which === "ctrl" ? "ctrlLock" : "altLock"] = true;
      kit.paint();
    };
    kit.spend = () => {
      if (!kit.ctrlLock) kit.ctrl = false;
      if (!kit.altLock) kit.alt = false;
      kit.paint();
    };
    kit.modify = data => {
      let out = data;
      const arrow = /^\x1b(?:\[|O)([ABCD])$/.exec(data);
      if (arrow) out = "\x1b[1;" + (1 + (kit.alt ? 2 : 0) + (kit.ctrl ? 4 : 0)) + arrow[1];
      else {
        let first = data.charAt(0);
        const rest = data.slice(1);
        if (kit.ctrl) first = ctrlOf(first);
        out = (kit.alt ? "\x1b" : "") + first + rest;
      }
      kit.spend();
      return out;
    };
    kit.raw = data => {
      const t = kit.term;
      if (!t) return;
      if (t._jxSend) t._jxSend(data);
      else safely("send", () => t._core.coreService.triggerDataEvent(data, true));
    };
    kit.press = what => {
      const t = kit.term;
      if (what === "compose") { kit.compose(!kit.composing); return; }
      if (what === "select") { kit.select(true); return; }
      if (!t) return;
      if (ARROWS[what]) {
        const mods = (kit.alt ? 2 : 0) + (kit.ctrl ? 4 : 0);
        const app = t.modes && t.modes.applicationCursorKeysMode;
        kit.raw(mods ? "\x1b[1;" + (1 + mods) + ARROWS[what] : (app ? "\x1bO" : "\x1b[") + ARROWS[what]);
        kit.spend();
        return;
      }
      // a character goes the typed road, so an armed Ctrl or Alt applies
      safely("press", () => t._core.coreService.triggerDataEvent(what, true));
    };
    kit.compose = on => {
      kit.composing = !!on;
      compose.hidden = !kit.composing;
      kit.paint();
      if (kit.composing) { fillHist(); fitTA(); ta.focus(); }
      kit.refit();
    };
    kit.send = () => {
      const t = kit.term, text = ta.value;
      if (!t || !text) return;
      remember(text);
      // xterm's paste: bracketed (ESC[200~ … ESC[201~) whenever the program
      // asked for it, which shells and the agent CLIs do, newlines as CR
      t.paste(text);
      if (enter.checked) setTimeout(() => kit.raw("\r"), 60); // after the paste's end mark, so it submits
      ta.value = "";
      fitTA();
      fillHist();
      ta.focus();
    };
    kit.select = on => {
      if (!on) {
        if (!sel.hidden) { sel.hidden = true; pre.textContent = ""; if (kit.term && !IS_MOBILE) safely("focus", () => kit.term.focus()); }
        return;
      }
      const t = kit.term;
      if (!t) return;
      const hr = host.getBoundingClientRect(), br = kit.box.getBoundingClientRect();
      sel.style.left = (br.left - hr.left + host.scrollLeft) + "px";
      sel.style.top = (br.top - hr.top + host.scrollTop) + "px";
      sel.style.width = br.width + "px";
      sel.style.height = Math.max(120, br.height) + "px";
      const alt = t.buffer.active.type === "alternate";
      pre.textContent = bufferText(t);
      selNote.textContent = alt ? "The screen as text (the program keeps its own history)." : "Screen and scrollback as text.";
      sel.hidden = false;
      t.blur();
      pre.scrollTop = pre.scrollHeight;
    };
    function history() {
      try { const h = JSON.parse(store.get(HIST_KEY, "[]")); return Array.isArray(h) ? h.filter(s => typeof s === "string") : []; }
      catch (e) { return []; }
    }
    function remember(text) {
      const h = history().filter(s => s !== text);
      h.unshift(text);
      store.set(HIST_KEY, JSON.stringify(h.slice(0, 20)));
    }
    kit.setShown(IS_MOBILE || store.get(KIT_PREF, "0") === "1");
    return kit;
  }

  // the xterm buffer as plain text: wrapped rows joined back into their
  // lines, trailing blank rows dropped
  function bufferText(t) {
    const b = t.buffer.active, out = [];
    for (let i = 0; i < b.length; i++) {
      const line = b.getLine(i);
      if (!line) continue;
      const s = line.translateToString(true);
      if (line.isWrapped && out.length) out[out.length - 1] += s;
      else out.push(s);
    }
    while (out.length && !out[out.length - 1].trim()) out.pop();
    return out.join("\n");
  }

  // the terminal's contextmenu gets two lines: the key bar, and Select.
  // A capture listener on the document hears the right press before the
  // terminal's own (wireTermMenu), so the wrapped showCtxMenu knows the
  // menu it is asked for belongs to a terminal
  let ctxTarget = null;
  document.addEventListener("contextmenu", e => {
    ctxTarget = e.target;
    setTimeout(() => { ctxTarget = null; }, 0);
  }, true);
  safely("ctx menu", () => {
    const show0 = W.showCtxMenu;
    W.showCtxMenu = function (x, y, items, onclose) {
      try {
        const box = ctxTarget && ctxTarget.closest ? ctxTarget.closest(".hostterm-box, #term-box") : null;
        const w = box && box.closest(".window"), kit = w && w._jxKit;
        if (kit && Array.isArray(items) && items.some(it => it && it.label === "Select All"))
          items = [...items, "-",
            { label: kit.shown ? "Hide Key Bar" : "Show Key Bar", act: () => kit.toggle() },
            { label: "Select as Text…", act: () => kit.select(true) }];
      } catch (e) {}
      return show0.call(this, x, y, items, onclose);
    };
  });

  // ==================================================================
  // 4. VM terminals in guest tmux
  // ==================================================================
  // A VM terminal opened with no command of its own runs tmux's session
  // "main" (attached if it is there), so a dropped link loses nothing and
  // Reconnect comes back to the same screen. One shell line decides on the
  // guest, in the same round trip: a guest without tmux gets its login
  // shell, as before. The mouse is on in that session, so the wheel scrolls
  // tmux's history and a drag copies through OSC 52 (newTerm). Off with
  // localStorage jx_vm_tmux = "0".
  const TMUX_CMD = 'if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s main \\; set-option mouse on; '
    + 'else exec "${SHELL:-/bin/sh}" -l; fi';
  const vmTmux = () => store.get("jx_vm_tmux", "1") !== "0";
  safely("websocket", () => {
    const WS0 = W.WebSocket;
    const vmTerm = /^(wss?:\/\/[^/]+\/v1\/vms\/[^/?#]+\/terminal)(\?[^#]*)?$/;
    function JXWebSocket(url, protocols) {
      let u = String(url);
      try {
        const m = vmTerm.exec(u);
        if (m && vmTmux()) {
          const q = new URLSearchParams((m[2] || "").slice(1));
          if (!q.has("cmd")) { q.set("cmd", TMUX_CMD); u = m[1] + "?" + q.toString(); }
        }
      } catch (e) {}
      return protocols === undefined ? new WS0(u) : new WS0(u, protocols);
    }
    JXWebSocket.prototype = WS0.prototype;
    for (const k of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) JXWebSocket[k] = WS0[k];
    W.WebSocket = JXWebSocket;
  });
  // the Terminal tab: a link that drops says so and offers Reconnect, which
  // reattaches the tmux session
  safely("reconnect", () => {
    const openB = $("#term-open");
    const open0 = openB.onclick;
    openB.onclick = function (e) {
      const r = open0.call(this, e);
      try {
        const ws = termWS;
        const close0 = ws.onclose;
        ws.onclose = ev => {
          if (close0) close0.call(ws, ev);
          if (termWS !== ws) return;
          openB.textContent = "Reconnect";
          openB.title = vmTmux() ? "Attach to the VM's tmux session again" : "Open a new shell";
          if (term && vmTmux()) term.write("\x1b[90m[Reconnect reattaches tmux session main, when the guest has tmux]\x1b[0m\r\n");
        };
      } catch (err) {}
      return r;
    };
    const close0 = W.closeTerm;
    W.closeTerm = function () { openB.textContent = "Connect"; openB.title = ""; return close0.apply(this, arguments); };
  });
  // a finger on the Terminal tab while tmux (the alternate screen) shows:
  // each cell travelled is a wheel notch on the screen, which xterm reports
  // to tmux's mouse; on the normal screen the box scrolls as it always did
  function wireVMTabTouch(t, box) {
    const sync = () => box.classList.toggle("jx-alt", t.buffer.active.type === "alternate");
    t.buffer.onBufferChange(sync);
    sync();
    if (box._jxTouch) return;
    box._jxTouch = true;
    let y0 = 0, acc = 0;
    box.addEventListener("touchstart", e => {
      if (e.touches.length === 1) { y0 = e.touches[0].clientY; acc = 0; }
    }, { passive: true });
    box.addEventListener("touchmove", e => {
      if (e.touches.length !== 1 || !box.classList.contains("jx-alt")) return;
      const y = e.touches[0].clientY, x = e.touches[0].clientX;
      acc += y - y0;
      y0 = y;
      const tt = term || t; // the tab's current Terminal (each Connect makes a new one)
      const scr = box.querySelector(".xterm-screen");
      const cell = (scr && tt.rows && scr.getBoundingClientRect().height / tt.rows) || 15;
      const target = box.querySelector(".xterm");
      while (target && Math.abs(acc) >= cell) {
        target.dispatchEvent(new WheelEvent("wheel", { deltaY: acc > 0 ? -1 : 1, deltaMode: 1,
          clientX: x, clientY: y, bubbles: true, cancelable: true }));
        acc -= Math.sign(acc) * cell;
      }
    }, { passive: true });
  }
})();
