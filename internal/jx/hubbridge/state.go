package hubbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"exe/internal/jx/board"
)

// MaxHandled bounds the remembered post ids; the oldest go first.
const MaxHandled = 5000

// ThreadRef is a hub thread the bridge owns: its root post's Board thread.
type ThreadRef struct {
	Root      string    `json:"root"`
	ThreadID  string    `json:"thread_id"`
	Target    string    `json:"target"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"created_at"`
}

// Pending is a Board turn whose end is still to be posted, under ReplyTo.
type Pending struct {
	ThreadID string `json:"thread_id"`
	Root     string `json:"root"`
	ReplyTo  string `json:"reply_to"`
}

// State is what the bridge keeps across restarts (hubbridge-threads.json).
type State struct {
	// Since is when the bridge first ran: catch-up ignores older posts,
	// so enabling it never replays an old feed.
	Since   int64                `json:"since"` // unix ms
	Threads map[string]ThreadRef `json:"threads"`
	Pending map[string]Pending   `json:"pending"` // Board turn id -> where its end goes
	Handled []string             `json:"handled"` // post ids acted on (or decided against), oldest first
	// ProfileName is the agent name last set on the hub, so profile.set
	// is sent once per name.
	ProfileName string `json:"profile_name,omitempty"`
	// ProfileHub is the hub ProfileName was set on.
	ProfileHub string `json:"profile_hub,omitempty"`

	path    string
	handled map[string]bool
}

// LoadState reads dir/hubbridge-threads.json; a missing file is a fresh
// state whose Since is now.
func LoadState(dir string, now time.Time) (*State, error) {
	st := &State{path: filepath.Join(dir, StateFile)}
	b, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.Since = now.UnixMilli()
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, st); err != nil {
			return nil, fmt.Errorf("%s: %w", StateFile, err)
		}
	}
	if st.Threads == nil {
		st.Threads = map[string]ThreadRef{}
	}
	if st.Pending == nil {
		st.Pending = map[string]Pending{}
	}
	st.handled = map[string]bool{}
	for _, id := range st.Handled {
		st.handled[id] = true
	}
	return st, nil
}

// Save writes the state (0600, atomically).
func (st *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return board.WriteFileAtomic(st.path, append(b, '\n'), 0o600)
}

// Seen reports whether post id was handled.
func (st *State) Seen(id string) bool { return st.handled[id] }

// MarkHandled remembers post id, forgetting the oldest past MaxHandled.
func (st *State) MarkHandled(id string) {
	if st.handled[id] {
		return
	}
	st.handled[id] = true
	st.Handled = append(st.Handled, id)
	if over := len(st.Handled) - MaxHandled; over > 0 {
		for _, old := range st.Handled[:over] {
			delete(st.handled, old)
		}
		st.Handled = append([]string(nil), st.Handled[over:]...)
	}
}

// ThreadList is the owned threads, newest first.
func (st *State) ThreadList() []ThreadRef {
	out := make([]ThreadRef, 0, len(st.Threads))
	for _, t := range st.Threads {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
