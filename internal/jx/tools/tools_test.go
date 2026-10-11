package tools

import (
	"os/exec"
	"testing"
)

func TestCatalogCommandsParse(t *testing.T) {
	seen := map[string]bool{}
	cats := map[string]bool{}
	for _, c := range Categories {
		cats[c] = true
	}
	for _, tl := range Catalog() {
		if seen[tl.ID] {
			t.Errorf("duplicate id %s", tl.ID)
		}
		seen[tl.ID] = true
		if !cats[tl.Category] {
			t.Errorf("%s: unknown category %q", tl.ID, tl.Category)
		}
		if tl.Agent == "" && tl.Install == "" && tl.Debian == "" && tl.Alpine == "" {
			t.Errorf("%s: no package anywhere", tl.ID)
		}
		if out, err := exec.Command("sh", "-n", "-c", tl.Command).CombinedOutput(); err != nil {
			t.Errorf("%s: sh -n: %v %s", tl.ID, err, out)
		}
	}
	if len(seen) < 30 {
		t.Errorf("catalog has %d tools", len(seen))
	}
}

// An installed tool runs at once: no package manager is touched.
func TestInstalledToolSkipsInstall(t *testing.T) {
	tl := Tool{ID: "x", Name: "x", Bin: "sh", Run: "echo RAN", Debian: "x", Alpine: "x"}
	out, err := exec.Command("sh", "-c", Command(tl)).CombinedOutput()
	if err != nil || string(out) != "RAN\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
