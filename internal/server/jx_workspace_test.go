package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// An empty file must list with "size": 0. Without it the desktop's
// "size < 1 MB" test fails on undefined and a new, empty text file opens
// as a blob download instead of in the editor.
func TestDirEntryEmptyFileHasSize(t *testing.T) {
	b, err := json.Marshal(dirEntry{Name: "test.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"size":0`) {
		t.Fatalf("entry %s has no size", b)
	}
}
