// Package board runs agent CLI threads: one Claude Code or Codex session on
// one target (a VM, or the host), driven one headless turn at a time. The
// daemon owns each run, so a turn survives closed tabs and lost networks;
// every turn's output is normalized into Events and kept per thread, seq
// numbered, so a client can resume its stream where it left off.
//
// The package knows nothing of the daemon: the target boundary is a Runner
// (and, for VMs, a Remote shell), which the server implements over SSH and
// os/exec and tests replace with fakes.
package board

import (
	"context"
	"time"
)

// Agents.
const (
	Claude = "claude"
	Codex  = "codex"
)

// HostTarget is the target name of the daemon's own machine.
const HostTarget = "host"

// Thread states.
const (
	ThreadIdle    = "idle"
	ThreadRunning = "running"
	ThreadQueued  = "queued"
	ThreadError   = "error"
)

// Turn states.
const (
	TurnQueued  = "queued"
	TurnRunning = "running"
	TurnDone    = "done"
	TurnError   = "error"
	TurnStopped = "stopped"
)

// Event types.
const (
	EvStatus     = "status"
	EvText       = "text"
	EvTool       = "tool"
	EvToolResult = "tool_result"
	EvTurnStart  = "turn_start"
	EvTurnEnd    = "turn_end"
)

// Thread is one agent CLI session on one target.
type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Target    string    `json:"target"`
	Agent     string    `json:"agent"`
	SessionID string    `json:"session_id"`
	State     string    `json:"state"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastText  string    `json:"last_text"`
	Archived  bool      `json:"archived"`
	// Queued counts the turns waiting behind the running one, for the
	// list's badge without fetching every thread.
	Queued int `json:"queued"`
	// Fork asks the next turn to fork SessionID into a new session
	// (Claude's --fork-session); cleared once a turn reports its own id.
	Fork bool `json:"fork,omitempty"`
}

// Usage is what one turn cost. InputTokens counts all input, cached or not;
// CachedInputTokens the part read from the prompt cache.
type Usage struct {
	InputTokens       int64    `json:"input_tokens"`
	CachedInputTokens int64    `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64    `json:"output_tokens"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
}

// Turn is one prompt and the run it started.
type Turn struct {
	ID        string     `json:"id"`
	Prompt    string     `json:"prompt"`
	Origin    string     `json:"origin"`
	State     string     `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	Usage     *Usage     `json:"usage,omitempty"`
}

// TurnView is a Turn with its events, as GET thread returns it.
type TurnView struct {
	Turn
	Events []Event `json:"events"`
}

// Event is one normalized step of a turn. Which fields are set depends on
// Type (see the PLAN's table); the rest stay empty and are omitted.
type Event struct {
	Seq     int64     `json:"seq"`
	TurnID  string    `json:"turn_id"`
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	Text    string    `json:"text,omitempty"`
	Prompt  string    `json:"prompt,omitempty"`
	ID      string    `json:"id,omitempty"`
	Name    string    `json:"name,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Output  string    `json:"output,omitempty"`
	IsError bool      `json:"is_error,omitempty"`
	State   string    `json:"state,omitempty"`
	Error   string    `json:"error,omitempty"`
	Usage   *Usage    `json:"usage,omitempty"`
}

// Spec is one turn to run.
type Spec struct {
	ThreadID, TurnID string
	Target, Agent    string
	SessionID        string // resume this session; "" starts a new one
	Fork             bool   // fork SessionID instead of continuing it
	Prompt           string
}

// Runner runs one turn of an agent CLI on its target. It reports its own
// progress (starting the VM, installing the CLI) through status and hands
// every line the CLI writes to stdout to line, in order, from one
// goroutine. It returns when the CLI exits: nil for exit 0, else an error
// carrying the tail of its stderr. Cancelling ctx stops the CLI and
// everything it started.
type Runner interface {
	Run(ctx context.Context, spec Spec, status func(string), line func([]byte)) error
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, spec Spec, status func(string), line func([]byte)) error

func (f RunnerFunc) Run(ctx context.Context, spec Spec, status func(string), line func([]byte)) error {
	return f(ctx, spec, status, line)
}

// ValidAgent reports whether a is an agent the Board drives.
func ValidAgent(a string) bool { return a == Claude || a == Codex }
