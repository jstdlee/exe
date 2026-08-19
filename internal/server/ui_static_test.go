package server

import (
	"os"
	"strings"
	"testing"
)

func readUITemplate(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("mobile titlebar controls should stay available; missing %q", want)
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
		`w.classList.contains("maximized")`,
		`w._max`,
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
		`const HIDDEN_DESKTOP_APPS = new Set(["Editor"])`,
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
		`#a-launchers { display: flex; flex-wrap: wrap;`,
		`body.mobile #a-launchers { flex-wrap: wrap; }`,
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
		`height: w.style.height || (w.offsetHeight + "px")`,
		`["left","top","width","height"]`,
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
		`Click a row to replay the log`,
		`Terminal-launched CLIs keep their own history`,
	} {
		if !strings.Contains(ui, want) {
			t.Fatalf("transcripts explanation missing %q", want)
		}
	}
}

