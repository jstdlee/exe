package server

import (
	"os"
	"strings"
	"testing"
)

func readUITemplate(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	for _, name := range []string{"ui/index.html", "ui/app.css", "ui/app.js"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	return out.String()
}

func TestUIAssetsAreSplit(t *testing.T) {
	index, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	for _, want := range []string{
		`<link rel="stylesheet" href="/ui/app.css">`,
		`<script src="/ui/app.js"></script>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("index should load split UI asset %q", want)
		}
	}
	if strings.Contains(html, `<style>`) {
		t.Fatal("index.html should not carry the full inline stylesheet")
	}
	if strings.Contains(html, "\n<script>\n") {
		t.Fatal("index.html should not carry the full inline app script")
	}
}

func TestMobileWindowsMenuIsStandalone(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`id="menu-windows" data-m="windows"`,
		`id="dd-windows"`,
		`id="mobile-windowlist"`,
		`m.dataset.m === "windows"`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile standalone Windows menu missing %q", want)
		}
	}
	if strings.Contains(ui, `id="drawer-windowlist"`) {
		t.Fatal("mobile drawer should not own the window switcher")
	}
}

func TestMobileMinimizeCollapsesToVisibleBar(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`body.mobile #desktop .window.minimized`,
		`display: flex !important`,
		`body.mobile #desktop .window.minimized > .win-frame`,
		`display: none !important`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile minimized windows should collapse to a visible bar; missing %q", want)
		}
	}
}

func TestMobileTitlebarControlsStayAvailable(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`body.mobile .grow-box { display: none; }`,
		`body.mobile .tbox.max { display: inline-block; }`,
		`w.classList.add("mob-restored")`,
		`if (IS_MOBILE && w.classList.contains("minimized")) { openWin(w); return; }`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile titlebar controls should stay available; missing %q", want)
		}
	}
}

func TestMobileMinimizedTitlebarRestoresWindow(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`if (IS_MOBILE && w.classList.contains("minimized")) { openWin(w); return; }`,
		`w.classList.remove("minimized");`,
		`updateMobileMinimizedBars();`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile minimized titlebar should restore the window; missing %q", want)
		}
	}
}

func TestMobileRestoreWindowIsCenteredAndClamped(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`body.mobile #desktop .window.mob-restored`,
		`left: 50% !important`,
		`top: 50% !important`,
		`transform: translate(-50%, -50%)`,
		`max-width: min(92vw, 640px) !important`,
		`max-height: calc(100dvh - 40px) !important`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile restored windows should be centered and viewport-clamped; missing %q", want)
		}
	}
}

func TestApi401OpensTokenWindow(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`if (r.status === 401) {`,
		`openWin("#win-token")`,
		`tokenInput.focus()`,
		`tokenPromptOpen = true`,
		`id="token-status"`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("API 401 should open the token window; missing %q", want)
		}
	}
}

func TestApiTokenDescriptionsOnNotesAndLogin(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`The daemon requires <span class="mono">Authorization: Bearer &lt;token&gt;</span>`,
		`EXE_API_TOKEN`,
		`localStorage</span> as <span class="mono">exe_token</span>`,
		`API token: the daemon's shared secret`,
		`Never paste the token into VM notes`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("API token descriptions should be present; missing %q", want)
		}
	}
}

func TestDesktopMaximizeButtonAndRestore(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`.tbox.max::before`,
		`.window.maximized`,
		`display: flex !important`,
		`w.classList.contains("maximized")`,
		`w._max`,
		`frameHeights: captureWindowFrameHeights(w)`,
		`restoreWindowFrameHeights(w, w._max.frameHeights)`,
		`body.mobile .tbox.max { display: inline-block; }`,
		`tbox.max.restore`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("desktop maximize button/restore behavior missing %q", want)
		}
	}
}

func TestVMPanelShowsStatusAndBulkStart(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`id="vm-panel"`,
		`renderVMPanel`,
		`vmBulkAction`,
		`Start All`,
		`Stop All`,
		`counts.running`,
		`counts.stopped`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("VM status/start panel missing %q", want)
		}
	}
}

func TestMobileFinderToolbarReplacesContextMenu(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`.finder-toolbar`,
		`renderFinderToolbar`,
		`selectedFinderEntry`,
		`nfOpen(w, true, base)`,
		`nfPickUpload(base)`,
		`showGetInfo(sel.en, sel.rel)`,
		`wsTrash(sel.rel, w)`,
		`wsDownload(sel.en, sel.rel)`,
		`body.mobile .finder-window .finder-toolbar`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile Finder toolbar for right-click replacement missing %q", want)
		}
	}
}

func TestMobileDesktopIconsScrollableAndAllAppsVisible(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`body.mobile #icons { overflow-y: auto;`,
		`body.mobile #sys-icons { z-index: 2; }`,
		`for (const app of appsList)`,
		`const HIDDEN_DESKTOP_APPS = new Set(["Editor", "Browser"])`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile desktop app visibility missing %q", want)
		}
	}
}

func TestTerminalCopyPasteMenu(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`showTermMenu`,
		`"Copy"`,
		`"Paste"`,
		`"Select All"`,
		`box.addEventListener("touchstart"`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("terminal copy/paste menu missing %q", want)
		}
	}
}

func TestHostStatsMovedToMonitorApp(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`id="win-host"`,
		`id="host-panel"`,
		`id="host-procs"`,
		`loadHostProcs`,
		`Host Monitor`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("Host Monitor app missing %q", want)
		}
	}
}

func TestAgentLaunchersWrapOnMobile(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`.agent-tools-panel`,
		`#a-launchers { display: grid; grid-template-columns: repeat(auto-fit, minmax(118px, 1fr));`,
		`.tool-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(92px, 1fr));`,
		`body.mobile #a-launchers { grid-template-columns: repeat(auto-fit, minmax(104px, 1fr)); }`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("agent launchers mobile wrap missing %q", want)
		}
	}
}

func TestMaximizeRestoresSize(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`width: w.style.width || (w.offsetWidth + "px")`,
		`height: w.style.height || ""`,
		`["left","top","width","height"]`,
		`clearWindowFrameHeights(w._max.frameHeights)`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("maximize size restore missing %q", want)
		}
	}
}

func TestLongPressOpensContextMenu(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`function addLongPressMenu`,
		`addLongPressMenu(row`,
		`addLongPressMenu(ic`,
		`setTimeout(`,
		`650`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("long-press context menu missing %q", want)
		}
	}
}

func TestTranscriptsExplanationClear(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`stored on the host next to the VM
              disk`,
		`survive stop/start`,
		`Double-click a row to open the transcript`,
		`Terminal-launched CLIs keep their own history`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("transcripts explanation missing %q", want)
		}
	}
}

func TestDesktopAppsMenuRemovedFromTopBar(t *testing.T) {
	ui := readUITemplate(t)
	if strings.Contains(ui, `<div class="menu" data-m="apps">Apps</div>`) {
		t.Fatal("desktop top bar should not include the Apps menu")
	}
	if !strings.Contains(ui, `id="dd-drawer-apps-list"`) {
		t.Fatal("mobile drawer should keep app launch entries")
	}
}

func TestBrowserAppIsHiddenFromDesktopAndDrawer(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`const HIDDEN_DESKTOP_APPS = new Set(["Editor", "Browser"])`,
		`appsList = (await j("/v1/apps")).filter(a => !isHiddenDesktopApp(a.name));`,
		`if (isHiddenDesktopApp(w.id.slice(8))) w.remove();`,
		`if (isHiddenDesktopApp(name)) return;`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("Browser app should be hidden from desktop app surfaces; missing %q", want)
		}
	}
	if strings.Contains(ui, "Notes, Browser") {
		t.Fatal("startup copy should not advertise the hidden Browser app")
	}
}

func TestObjectsOpenOnDoubleClick(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`const objectOpen = (n, fn) => n.addEventListener("dblclick", fn);`,
		`objectOpen(ic, () => openFinderWin(""))`,
		`objectOpen(row, () => openVM(vm.name))`,
		`objectOpen(trashIcon, () => openWin("#win-trash"))`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("objects should open on double-click; missing %q", want)
		}
	}
	if strings.Contains(ui, `const tapOpen = (n, fn) => n.addEventListener(IS_MOBILE ? "click" : "dblclick", fn);`) {
		t.Fatal("object opening should no longer switch mobile to single-click")
	}
}

func TestIconDragDoesNotSuppressDoubleClick(t *testing.T) {
	ui := readUITemplate(t)
	if strings.Contains(ui, `ic.addEventListener("pointerdown", e => {
    if (e.button !== 0) return;
    e.preventDefault();`) {
		t.Fatal("icon pointerdown should not prevent default before a drag threshold; it suppresses dblclick")
	}
	if !strings.Contains(ui, `if (!moved) { moved = true; ic.classList.add("dragging"); ev.preventDefault(); }`) {
		t.Fatal("icon drag should prevent default only once an actual drag starts")
	}
}

func TestTranscriptDetailsUseModal(t *testing.T) {
	ui := readUITemplate(t)
	for _, want := range []string{
		`id="tr-overlay"`,
		`id="tr-modal-log"`,
		`function openTranscriptModal(d)`,
		`objectOpen(item, async () => {`,
		`openTranscriptModal(d)`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("transcript modal missing %q", want)
		}
	}
	if strings.Contains(ui, `id="t-log" hidden`) {
		t.Fatal("transcript pane should not reserve inline detail log space")
	}
}
