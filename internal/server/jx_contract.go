package server

import "context"

// Cross-feature contract between jx features built in parallel. Each var is
// set by the feature that owns it (in its init); callers must nil-check.

// BoardSubmit posts a prompt to the Board, as a new thread (req.ThreadID ==
// "") or a new turn on an existing one, and returns the thread and turn ids.
// Owned by jx_board.go; called by cron (jx_cron.go).
var jxBoardSubmit func(s *Server, ctx context.Context, req BoardSubmitRequest) (threadID, turnID string, err error)

type BoardSubmitRequest struct {
	ThreadID string `json:"thread_id,omitempty"`
	Target   string `json:"target"` // VM name, or "host"
	Agent    string `json:"agent"`  // "claude" | "codex"
	// Session continues an agent CLI session (Claude session id / Codex
	// thread id) not yet on the Board; ignored when ThreadID is set.
	Session string `json:"session,omitempty"`
	Fork    bool   `json:"fork,omitempty"`
	Prompt  string `json:"prompt"`
	Title   string `json:"title,omitempty"`
	Origin  string `json:"origin,omitempty"` // "user" | "cron:<job id>"
}

// jxEnsureVMUp starts vm if it is stopped (memory guard applies) and waits
// until SSH answers. Owned by jx_idle.go; used by board, env and cron.
var jxEnsureVMUp func(s *Server, ctx context.Context, vm string) error
