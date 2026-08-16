package agentenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapCreateListRestoreDelete(t *testing.T) {
	state := t.TempDir()
	vm := "box"
	disk := DiskPath(state, vm)
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, []byte("disk-v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := CreateSnap(state, vm, "before")
	if err != nil {
		t.Fatal(err)
	}
	if s.Label != "before" || s.ID == "" {
		t.Fatalf("snap=%+v", s)
	}
	list, err := ListSnaps(state, vm)
	if err != nil || len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := os.WriteFile(disk, []byte("disk-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnap(state, vm, s.ID); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "disk-v1" {
		t.Fatalf("restored %q", got)
	}
	if err := DeleteSnap(state, vm, s.ID); err != nil {
		t.Fatal(err)
	}
	list, err = ListSnaps(state, vm)
	if err != nil || len(list) != 0 {
		t.Fatalf("after delete list=%v err=%v", list, err)
	}
}
