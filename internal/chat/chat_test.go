package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameChangesTitle(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "first prompt about networking", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Rename(dir, s.ID, "  Network plan  "); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Network plan" {
		t.Fatalf("title=%q", got.Title)
	}
	if _, err := os.Stat(filepath.Join(dir, s.ID+".json")); err != nil {
		t.Fatal(err)
	}
}
