package peer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func todoJSON(items ...string) []byte {
	return []byte(`{"version":2,"items":[` + join(items) + `]}`)
}

func join(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ","
		}
		out += it
	}
	return out
}

func TestMergeTodosUnionAndLWW(t *testing.T) {
	a := todoJSON(
		`{"id":"x","text":"shared old","done":false,"created":1,"updated":10}`,
		`{"id":"a","text":"only a","done":false,"created":2,"updated":5}`)
	b := todoJSON(
		`{"id":"x","text":"shared new","done":true,"created":1,"updated":20}`,
		`{"id":"b","text":"only b","done":false,"created":3,"updated":6}`)
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	if err := json.Unmarshal(merged, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 3 {
		t.Fatalf("want 3 items, got %d: %s", len(d.Items), merged)
	}
	byID := map[string]todoItem{}
	for _, it := range d.Items {
		byID[it.ID] = it
	}
	if byID["x"].Text != "shared new" || !byID["x"].Done {
		t.Fatalf("LWW lost: %+v", byID["x"])
	}
	if _, ok := byID["a"]; !ok {
		t.Fatal("union lost a")
	}
	if _, ok := byID["b"]; !ok {
		t.Fatal("union lost b")
	}
	// commutative and canonical: both directions produce identical bytes
	rev, ok := MergeFile("Todo/todos.json", b, a)
	if !ok || !bytes.Equal(merged, rev) {
		t.Fatalf("merge not commutative:\n%s\n%s", merged, rev)
	}
	// idempotent: canonical form is a fixed point
	again, ok := MergeFile("Todo/todos.json", merged, merged)
	if !ok || !bytes.Equal(merged, again) {
		t.Fatal("canonical form is not a fixed point")
	}
}

func TestMergeTodosTombstoneWins(t *testing.T) {
	now := time.Now().UnixMilli()
	a := todoJSON(fmt.Sprintf(`{"id":"x","text":"edited","done":false,"created":1,"updated":%d}`, now-1000))
	b := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, now, now))
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	json.Unmarshal(merged, &d)
	if len(d.Items) != 1 || d.Items[0].Deleted == 0 {
		t.Fatalf("tombstone should win: %s", merged)
	}
}

func TestMergeTombstoneWinsUpdatedTie(t *testing.T) {
	// A stripped (resurrected) copy and its tombstone tie on `updated`; the
	// tombstone must win regardless of byte order, or deletions come back.
	ts := time.Now().UnixMilli()
	live := todoJSON(fmt.Sprintf(`{"id":"x","text":"back from the dead","done":false,"created":1,"updated":%d}`, ts))
	tomb := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, ts, ts))
	for _, order := range [][2][]byte{{live, tomb}, {tomb, live}} {
		merged, ok := MergeFile("Todo/todos.json", order[0], order[1])
		if !ok {
			t.Fatal("merge not ok")
		}
		var d todoDoc
		json.Unmarshal(merged, &d)
		if len(d.Items) != 1 || d.Items[0].Deleted == 0 {
			t.Fatalf("tombstone must win the tie both ways: %s", merged)
		}
	}
}

func TestMergeTodosGCsOldTombstones(t *testing.T) {
	old := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	a := todoJSON(fmt.Sprintf(`{"id":"x","text":"","done":false,"created":1,"updated":%d,"deleted":%d}`, old, old))
	b := todoJSON(`{"id":"y","text":"keep","done":false,"created":2,"updated":3}`)
	merged, ok := MergeFile("Todo/todos.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	var d todoDoc
	json.Unmarshal(merged, &d)
	if len(d.Items) != 1 || d.Items[0].ID != "y" {
		t.Fatalf("40-day tombstone should be gone: %s", merged)
	}
}

func TestMergeTodosV1FallsBackToLWW(t *testing.T) {
	v1 := []byte(`[{"text":"legacy","done":false,"created":"2026-01-01T00:00:00Z"}]`)
	v2 := todoJSON(`{"id":"x","text":"new","done":false,"created":1,"updated":2}`)
	if _, ok := MergeFile("Todo/todos.json", v1, v2); ok {
		t.Fatal("v1 array must not merge")
	}
	if _, ok := MergeFile("Paint/canvas.png", []byte("a"), []byte("b")); ok {
		t.Fatal("non-mergeable file must not merge")
	}
}

func TestMergeNotesDropsLegacySel(t *testing.T) {
	a := []byte(`{"notes":[{"id":"n1","text":"from a","created":1,"updated":10}],"sel":"n1"}`)
	b := []byte(`{"notes":[{"id":"n2","text":"from b","created":2,"updated":20}],"sel":"n2"}`)
	merged, ok := MergeFile("Notes/notes.json", a, b)
	if !ok {
		t.Fatal("merge not ok")
	}
	if bytes.Contains(merged, []byte(`"sel"`)) {
		t.Fatalf("sel must not survive: %s", merged)
	}
	var d noteDoc
	json.Unmarshal(merged, &d)
	if len(d.Notes) != 2 {
		t.Fatalf("want 2 notes: %s", merged)
	}
	// newest-updated first, the app's display order
	if d.Notes[0].ID != "n2" {
		t.Fatalf("order should be updated-desc: %s", merged)
	}
}

func TestVersionOrdering(t *testing.T) {
	a := Version{Ctr: 2, Origin: "aa"}
	b := Version{Ctr: 1, Origin: "zz"}
	if !a.Newer(b) || b.Newer(a) {
		t.Fatal("higher ctr must win")
	}
	c := Version{Ctr: 2, Origin: "bb"}
	if !c.Newer(a) || a.Newer(c) {
		t.Fatal("origin must break ties one way")
	}
	if !a.Same(Version{Ctr: 2, Origin: "aa", MTime: 999}) {
		t.Fatal("Same must ignore MTime")
	}
}
