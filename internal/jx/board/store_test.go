package board

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStorePersistsThreadsAndEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "board")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	th := Thread{ID: NewID(), Title: "t", Target: "dev", Agent: Claude, State: ThreadIdle, CreatedAt: now, UpdatedAt: now}
	if err := s.Create(th); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(th); err == nil {
		t.Fatal("a second create of the same id succeeded")
	}
	turn := Turn{ID: NewID(), Prompt: "hi", State: TurnQueued, CreatedAt: now}
	if _, err := s.Update(th.ID, func(t *Thread, turns []Turn) ([]Turn, error) {
		return append(turns, turn), nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ev, err := s.Append(th.ID, Event{TurnID: turn.ID, Type: EvText, Text: "x"})
		if err != nil || ev.Seq != int64(i+1) || ev.At.IsZero() {
			t.Fatalf("append %d = %+v, %v", i, ev, err)
		}
	}
	evs, err := s.Events(th.ID, 1)
	if err != nil || len(evs) != 2 || evs[0].Seq != 2 {
		t.Fatalf("events after 1 = %+v, %v", evs, err)
	}

	// a crash mid-append leaves a torn last line: skipped, seq goes on
	f, _ := os.OpenFile(filepath.Join(dir, th.ID, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"seq":4,"turn_id":"`)
	f.Close()

	s2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, turns, err := s2.Get(th.ID)
	if err != nil || got.Title != "t" || len(turns) != 1 || turns[0].Prompt != "hi" {
		t.Fatalf("reloaded = %+v %+v %v", got, turns, err)
	}
	ev, err := s2.Append(th.ID, Event{TurnID: turn.ID, Type: EvText})
	if err != nil || ev.Seq != 4 {
		t.Fatalf("seq after reload = %d, %v", ev.Seq, err)
	}

	if runtime.GOOS != "windows" {
		for p, want := range map[string]os.FileMode{
			dir:                                      0o700,
			filepath.Join(dir, th.ID):                0o700,
			filepath.Join(dir, th.ID, "thread.json"): 0o600,
			filepath.Join(dir, th.ID, "events.jsonl"): 0o600,
		} {
			st, err := os.Stat(p)
			if err != nil || st.Mode().Perm() != want {
				t.Errorf("%s mode = %v, want %v", p, st.Mode().Perm(), want)
			}
		}
	}

	if err := s2.Delete(th.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, th.ID)); !os.IsNotExist(err) {
		t.Errorf("thread folder survives delete: %v", err)
	}
	if _, _, err := s2.Get(th.ID); err != ErrNotFound {
		t.Errorf("get deleted = %v", err)
	}
}

func TestStoreListNewestFirst(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	base := time.Now().UTC()
	var ids []string
	for i := 0; i < 3; i++ {
		th := Thread{ID: NewID(), UpdatedAt: base.Add(time.Duration(i) * time.Minute)}
		s.Create(th)
		ids = append(ids, th.ID)
	}
	l := s.List()
	if len(l) != 3 || l[0].ID != ids[2] || l[2].ID != ids[0] {
		t.Errorf("order = %v", l)
	}
}
