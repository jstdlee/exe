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
		`body.mobile .tbox.shade, body.mobile .grow-box { display: none; }`,
		`body.mobile .tbox.zoom { display: inline-block; }`,
		`w.classList.toggle("mob-restored")`,
		`if (IS_MOBILE) return; // no resize in mobile mode`,
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
		`max-width: 92vw !important`,
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
