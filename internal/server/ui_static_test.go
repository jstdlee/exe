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
