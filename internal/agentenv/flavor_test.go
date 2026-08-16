package agentenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFlavorDebianBuiltin(t *testing.T) {
	f, err := LoadFlavor("debian")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "debian" || !strings.Contains(f.ImageURL, "debian-13") {
		t.Fatalf("%+v", f)
	}
	if f.InitPrompt == "" {
		t.Fatal("empty init prompt")
	}
}

func TestLoadFlavorFileOverridesImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "custom.yaml")
	body := "name: debian\nimage_url: https://example.test/debian.raw\ncpus: 4\nmemory_mb: 4096\ndisk_gb: 16\ninit_prompt: |\n  hello flavor\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFlavor(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.ImageURL != "https://example.test/debian.raw" {
		t.Fatalf("image=%q", f.ImageURL)
	}
	if f.CPUs != 4 || f.MemoryMB != 4096 || f.DiskGB != 16 {
		t.Fatalf("sizes %+v", f)
	}
	if !strings.Contains(f.InitPrompt, "hello flavor") {
		t.Fatalf("prompt=%q", f.InitPrompt)
	}
}
