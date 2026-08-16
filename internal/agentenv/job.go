package agentenv

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// FileDrop is a file attached to a Job and written into the guest.
type FileDrop struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

// Job is one invocation into an Environment.
type Job struct {
	ID        string     `json:"id"`
	VM        string     `json:"vm"`
	Session   string     `json:"session"`
	Cmd       string     `json:"cmd,omitempty"`
	Script    string     `json:"script,omitempty"`
	Prompt    string     `json:"prompt,omitempty"`
	Files     []FileDrop `json:"files,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Turn is one Session step.
type Turn struct {
	JobID        string    `json:"job_id"`
	ShellOutput  string    `json:"shell_output"`
	AgentOutput  string    `json:"agent_output,omitempty"`
	ExitCode     int       `json:"exit_code"`
	Downloads    []string  `json:"downloads,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Session is a multi-turn Job conversation in one Environment.
type Session struct {
	ID    string `json:"id"`
	VM    string `json:"vm"`
	Turns []Turn `json:"turns"`
}

func NewJobID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmtTimeID(b)
}

func fmtTimeID(suf []byte) string {
	return hex.EncodeToString(suf)
}

func sessionPath(stateDir, vm, id string) string {
	return filepath.Join(stateDir, "vms", vm, "jobs", id+".json")
}

func LoadSession(stateDir, vm, id string) (*Session, error) {
	b, err := os.ReadFile(sessionPath(stateDir, vm, id))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveSession(stateDir string, s *Session) error {
	p := sessionPath(stateDir, s.VM, s.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func AppendTurn(stateDir string, s *Session, t Turn) error {
	s.Turns = append(s.Turns, t)
	return SaveSession(stateDir, s)
}
