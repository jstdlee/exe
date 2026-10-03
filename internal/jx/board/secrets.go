package board

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

// Secrets are the agent credentials the daemon hands to VMs: a Claude Code
// OAuth token (from `claude setup-token`) and an OpenAI API key for Codex.
// They are written to disk 0600, never returned by the API, never logged,
// and reach a guest on SSH stdin, never on a command line.
type Secrets struct {
	ClaudeOAuthToken string `json:"claude_oauth_token,omitempty"`
	CodexAPIKey      string `json:"codex_api_key,omitempty"`
}

// SecretStatus is all the API ever says about the secrets.
type SecretStatus struct {
	ClaudeOAuthTokenSet bool `json:"claude_oauth_token_set"`
	CodexAPIKeySet      bool `json:"codex_api_key_set"`
}

// SecretStore is the secrets file. Gen counts changes, so a guest that got
// the credentials of an older generation gets them again.
type SecretStore struct {
	path string

	mu     sync.Mutex
	loaded bool
	cur    Secrets
	gen    uint64
}

func NewSecretStore(path string) *SecretStore { return &SecretStore{path: path, gen: 1} }

func (s *SecretStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.cur)
	}
}

// Get returns the secrets and their generation.
func (s *SecretStore) Get() (Secrets, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.cur, s.gen
}

// Status says which secrets are set.
func (s *SecretStore) Status() SecretStatus {
	sec, _ := s.Get()
	return SecretStatus{ClaudeOAuthTokenSet: sec.ClaudeOAuthToken != "", CodexAPIKeySet: sec.CodexAPIKey != ""}
}

// Set changes the secrets: nil leaves one as it is, "" clears it.
func (s *SecretStore) Set(claude, codex *string) error {
	for _, v := range []*string{claude, codex} {
		if v != nil && strings.ContainsFunc(*v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return inputErr("a secret cannot hold control characters or line breaks")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	next := s.cur
	if claude != nil {
		next.ClaudeOAuthToken = strings.TrimSpace(*claude)
	}
	if codex != nil {
		next.CodexAPIKey = strings.TrimSpace(*codex)
	}
	if next == s.cur {
		return nil
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(s.path, b, 0o600); err != nil {
		// the error names the path, never the content
		return errors.New("cannot save the secrets file: " + err.Error())
	}
	s.cur = next
	s.gen++
	return nil
}

// envHeader marks a guest's agent.env as the daemon's own, so clearing the
// secrets can empty it without touching a file the person wrote.
const envHeader = "# written by exe (Board): agent credentials; rewritten when they change"

// EnvFile is the guest's ~/.config/exe/agent.env for sec: shell exports a
// turn sources before it runs the CLI.
func EnvFile(sec Secrets) string {
	var b strings.Builder
	b.WriteString(envHeader + "\n")
	if sec.ClaudeOAuthToken != "" {
		b.WriteString("export CLAUDE_CODE_OAUTH_TOKEN=" + shq(sec.ClaudeOAuthToken) + "\n")
	}
	if sec.CodexAPIKey != "" {
		b.WriteString("export OPENAI_API_KEY=" + shq(sec.CodexAPIKey) + "\n")
		b.WriteString("export CODEX_API_KEY=" + shq(sec.CodexAPIKey) + "\n")
	}
	return b.String()
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
