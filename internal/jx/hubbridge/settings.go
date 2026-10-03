// Package hubbridge turns the user's exe-hub into a task board between the
// user and an agent. A root post by an owner that starts with "@agent"
// becomes a Board thread (Claude Code or Codex on a VM or the host); the
// owner's replies in that hub thread become the thread's next turns; the
// end of each turn comes back as a hub reply, signed with the agent's own
// key.
//
// The package knows nothing of the daemon: the hub and the Board sit
// behind the Hub and Board interfaces, which the server implements and
// tests fake.
package hubbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"exe/internal/jx/board"
)

// File names under the daemon's jx directory.
const (
	SettingsFile = "hubbridge.json"
	StateFile    = "hubbridge-threads.json"
	KeyFile      = "hubbridge_ed25519"
)

// Settings is what the user configures (GET/PUT /v1/jx/hubbridge).
type Settings struct {
	Enabled bool `json:"enabled"`
	// HubURL is the hub's base address; "" uses the daemon's hub.url.
	HubURL string `json:"hub_url"`
	// Owners are the profile ids whose posts the bridge acts on; empty
	// means this node's own id (filled in by the server).
	Owners        []string `json:"owners"`
	DefaultTarget string   `json:"default_target"` // VM name or "host"
	DefaultAgent  string   `json:"default_agent"`  // "claude" | "codex"
	AgentName     string   `json:"agent_name"`
	// Ack posts a short "on it" reply when a task starts.
	Ack bool `json:"ack"`
}

// Defaults are the settings before the user changes anything.
func Defaults() Settings {
	return Settings{DefaultTarget: board.HostTarget, DefaultAgent: board.Claude, AgentName: "Agent", Ack: true}
}

var (
	profileID = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// vmName matches the targets a directive may name.
	vmName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
)

// Normalize fills empty fields with defaults and checks the rest.
func (s *Settings) Normalize() error {
	d := Defaults()
	s.HubURL = strings.TrimRight(strings.TrimSpace(s.HubURL), "/")
	s.DefaultTarget = strings.TrimSpace(s.DefaultTarget)
	if s.DefaultTarget == "" {
		s.DefaultTarget = d.DefaultTarget
	}
	if !vmName.MatchString(s.DefaultTarget) {
		return errors.New("default_target: not a VM name")
	}
	s.DefaultAgent = strings.ToLower(strings.TrimSpace(s.DefaultAgent))
	if s.DefaultAgent == "" {
		s.DefaultAgent = d.DefaultAgent
	}
	if s.DefaultAgent != board.Claude && s.DefaultAgent != board.Codex {
		return errors.New(`default_agent: "claude" or "codex"`)
	}
	s.AgentName = strings.TrimSpace(s.AgentName)
	if s.AgentName == "" {
		s.AgentName = d.AgentName
	}
	if len(s.AgentName) > 64 || strings.ContainsAny(s.AgentName, "\n\r@:") {
		return errors.New("agent_name: at most 64 bytes, one line, no @ or :")
	}
	var owners []string
	seen := map[string]bool{}
	for _, o := range s.Owners {
		o = strings.ToLower(strings.TrimSpace(o))
		if o == "" || seen[o] {
			continue
		}
		if !profileID.MatchString(o) {
			return fmt.Errorf("owners: %q is not a profile id (16 hex characters)", o)
		}
		seen[o] = true
		owners = append(owners, o)
	}
	s.Owners = owners
	return nil
}

// LoadSettings reads dir/hubbridge.json; a missing file is the defaults.
func LoadSettings(dir string) (Settings, error) {
	s := Defaults()
	b, err := os.ReadFile(filepath.Join(dir, SettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", SettingsFile, err)
	}
	err = s.Normalize()
	return s, err
}

// SaveSettings writes dir/hubbridge.json (0600, atomically).
func SaveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return board.WriteFileAtomic(filepath.Join(dir, SettingsFile), append(b, '\n'), 0o600)
}
