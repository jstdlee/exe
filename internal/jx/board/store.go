package board

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown thread or turn.
var ErrNotFound = errors.New("thread not found")

// Store keeps threads on disk, one folder per thread under dir:
// thread.json (the thread and its turns, rewritten atomically) and
// events.jsonl (append-only, one Event per line). Thread records live in
// memory too; events are read back from disk on demand, as a long thread's
// history is only wanted by a client opening it.
type Store struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	threads map[string]*record
}

type record struct {
	Thread Thread `json:"thread"`
	Turns  []Turn `json:"turns"`
	seq    int64  // last event seq written
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// NewID returns a fresh random thread or turn id.
func NewID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// OpenStore loads every thread under dir, creating dir if needed.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, now: time.Now, threads: map[string]*record{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !idPattern.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "thread.json"))
		if err != nil {
			continue // a folder left half-made by a crash: nothing to show
		}
		var r record
		if json.Unmarshal(b, &r) != nil || r.Thread.ID != e.Name() {
			continue
		}
		r.seq = lastSeq(filepath.Join(dir, e.Name(), "events.jsonl"))
		s.threads[r.Thread.ID] = &r
	}
	return s, nil
}

// lastSeq is the seq of the last whole event in an events file, 0 when
// there is none. A torn last line (a crash mid-append) is skipped.
func lastSeq(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	var seq int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		var e struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Seq > seq {
			seq = e.Seq
		}
	}
	return seq
}

func (s *Store) threadDir(id string) string { return filepath.Join(s.dir, id) }

func (s *Store) save(r *record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.threadDir(r.Thread.ID), 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.threadDir(r.Thread.ID), "thread.json"), b, 0o600)
}

// Create stores a new thread.
func (s *Store) Create(t Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[t.ID]; ok {
		return fmt.Errorf("thread %s exists", t.ID)
	}
	r := &record{Thread: t, Turns: []Turn{}}
	if err := s.save(r); err != nil {
		return err
	}
	s.threads[t.ID] = r
	return nil
}

// Update applies fn to a thread's record under the store's lock and saves
// it; fn's error aborts the change.
func (s *Store) Update(id string, fn func(t *Thread, turns []Turn) ([]Turn, error)) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.threads[id]
	if !ok {
		return Thread{}, ErrNotFound
	}
	t := r.Thread
	turns, err := fn(&t, append([]Turn(nil), r.Turns...))
	if err != nil {
		return Thread{}, err
	}
	old := *r
	r.Thread, r.Turns = t, turns
	if err := s.save(r); err != nil {
		r.Thread, r.Turns = old.Thread, old.Turns
		return Thread{}, err
	}
	return t, nil
}

// UpdateTurn applies fn to one turn of a thread and saves.
func (s *Store) UpdateTurn(id, turnID string, fn func(t *Thread, turn *Turn)) (Thread, Turn, error) {
	var out Turn
	t, err := s.Update(id, func(t *Thread, turns []Turn) ([]Turn, error) {
		for i := range turns {
			if turns[i].ID == turnID {
				fn(t, &turns[i])
				out = turns[i]
				return turns, nil
			}
		}
		return nil, ErrNotFound
	})
	return t, out, err
}

// Get returns a thread and its turns (without events).
func (s *Store) Get(id string) (Thread, []Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.threads[id]
	if !ok {
		return Thread{}, nil, ErrNotFound
	}
	return r.Thread, append([]Turn(nil), r.Turns...), nil
}

// List returns every thread, latest update first.
func (s *Store) List() []Thread {
	s.mu.Lock()
	out := make([]Thread, 0, len(s.threads))
	for _, r := range s.threads {
		out = append(out, r.Thread)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Delete removes a thread and its files.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[id]; !ok {
		return ErrNotFound
	}
	delete(s.threads, id)
	return os.RemoveAll(s.threadDir(id))
}

// Append numbers ev with the thread's next seq, stamps it, and appends it
// to the thread's events file.
func (s *Store) Append(id string, ev Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.threads[id]
	if !ok {
		return Event{}, ErrNotFound
	}
	ev.Seq = r.seq + 1
	if ev.At.IsZero() {
		ev.At = s.now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return Event{}, err
	}
	f, err := os.OpenFile(filepath.Join(s.threadDir(id), "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Event{}, err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Event{}, err
	}
	r.seq = ev.Seq
	return ev, nil
}

// Events returns the thread's events with seq > after, in order.
func (s *Store) Events(id string, after int64) ([]Event, error) {
	s.mu.Lock()
	_, ok := s.threads[id]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.threadDir(id), "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Event{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var ev Event
		if json.Unmarshal(line, &ev) != nil || ev.Seq <= after {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// WriteFileAtomic writes data to path through a temp file in the same
// folder and a rename, so a reader never sees a half-written file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
