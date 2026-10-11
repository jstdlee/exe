// Package llm keeps the LLM providers that VM agents can run on (an
// OpenAI- or Anthropic-compatible endpoint: base URL, API key, default
// model), lists a provider's models from its /models path, and builds the
// guest script that starts an agent CLI on a chosen provider, model and
// thinking level. The built-in Magpie provider is the host's local gateway
// at http://127.0.0.1:3425/v1; a loopback provider reaches a VM through an
// SSH port forward the daemon opens for the terminal's life.
package llm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Provider kinds: the API the endpoint speaks. Both means it serves the
// OpenAI paths and Anthropic's /v1/messages at the same root (Magpie).
const (
	OpenAI    = "openai"
	Anthropic = "anthropic"
	Both      = "both"
)

// MagpieID is the built-in provider's id.
const MagpieID = "magpie"

// Provider is one endpoint. APIKey never leaves the daemon through the
// API: KeySet says whether one is stored.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"` // the default model
	Builtin bool   `json:"builtin,omitempty"`
	KeySet  bool   `json:"api_key_set,omitempty"`
}

// Magpie is the built-in provider: the host's local Magpie gateway.
func Magpie() Provider {
	return Provider{ID: MagpieID, Name: "Magpie (this host)", Kind: Both, BaseURL: "http://127.0.0.1:3425/v1", Builtin: true}
}

// Speaks reports whether p serves the API kind (OpenAI or Anthropic).
func (p Provider) Speaks(kind string) bool { return p.Kind == kind || p.Kind == Both }

// Choice is one remembered launch: the provider ("" = the agent's own
// sign-in), the model ("" = the default) and the thinking level.
type Choice struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// State is the store's file.
type State struct {
	Providers []Provider        `json:"providers"`
	Last      map[string]Choice `json:"last,omitempty"`
}

// Store is llm.json in the jx state folder, written 0600 (it holds keys).
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(dir string) *Store { return &Store{path: filepath.Join(dir, "llm.json")} }

func (s *Store) read() State {
	var st State
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func (s *Store) write(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Providers returns Magpie first, then the saved providers, keys included.
func (s *Store) Providers() []Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Provider{Magpie()}, s.read().Providers...)
}

// Get returns the provider with id.
func (s *Store) Get(id string) (Provider, bool) {
	for _, p := range s.Providers() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Public is the providers without their keys, for the API.
func Public(ps []Provider) []Provider {
	out := make([]Provider, len(ps))
	for i, p := range ps {
		p.KeySet = p.APIKey != ""
		p.APIKey = ""
		out[i] = p
	}
	return out
}

// SetProviders replaces the saved providers. An entry whose api_key is
// empty keeps the key stored under its id; "-" clears it. New entries get
// an id. The built-in one is never saved.
func (s *Store) SetProviders(in []Provider) ([]Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.read()
	old := map[string]Provider{}
	for _, p := range st.Providers {
		old[p.ID] = p
	}
	var out []Provider
	seen := map[string]bool{MagpieID: true}
	for _, p := range in {
		if p.Builtin || p.ID == MagpieID {
			continue
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
		p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Model = strings.TrimSpace(p.Model)
		p.APIKey = strings.TrimSpace(p.APIKey)
		p.KeySet = false
		if err := validate(p); err != nil {
			return nil, err
		}
		if p.ID == "" || seen[p.ID] {
			p.ID = newID()
		}
		seen[p.ID] = true
		switch {
		case p.APIKey == "-":
			p.APIKey = ""
		case p.APIKey == "":
			p.APIKey = old[p.ID].APIKey
		}
		out = append(out, p)
	}
	st.Providers = out
	if err := s.write(st); err != nil {
		return nil, err
	}
	return out, nil
}

func validate(p Provider) error {
	if p.Name == "" {
		return errors.New("a provider needs a name")
	}
	if p.Kind != OpenAI && p.Kind != Anthropic && p.Kind != Both {
		return fmt.Errorf("%s: kind must be openai, anthropic or both", p.Name)
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s: the URL must start with http:// or https://", p.Name)
	}
	if strings.ContainsAny(p.Model+p.APIKey, "\n\r") {
		return fmt.Errorf("%s: the model and key are one line each", p.Name)
	}
	return nil
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "p" + hex.EncodeToString(b)
}

// Last returns the remembered launch choices by agent.
func (s *Store) Last() map[string]Choice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read().Last
}

// Remember saves agent's last launch choice.
func (s *Store) Remember(agent string, c Choice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.read()
	if st.Last == nil {
		st.Last = map[string]Choice{}
	}
	if st.Last[agent] == c {
		return nil
	}
	st.Last[agent] = c
	return s.write(st)
}

// ---- URLs ----------------------------------------------------------------

// OpenAIBase is the base the OpenAI SDKs take: the URL as entered.
func (p Provider) OpenAIBase() string { return strings.TrimRight(p.BaseURL, "/") }

// AnthropicRoot is the base Anthropic's SDKs take (they add /v1/messages):
// the URL without a trailing /v1.
func (p Provider) AnthropicRoot() string {
	return strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/v1")
}

// Loopback reports whether the provider's host is this machine's loopback,
// which a VM cannot reach without a forward. It returns the host:port to
// dial on the host.
func (p Provider) Loopback() (hostport string, ok bool) {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return "", false
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	if h != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(h, port), true
}

// Rehost returns p with its URL's host:port swapped for hostport (the
// guest end of a forward).
func (p Provider) Rehost(hostport string) Provider {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return p
	}
	u.Host = hostport
	p.BaseURL = strings.TrimRight(u.String(), "/")
	return p
}

// ---- models --------------------------------------------------------------

// Model is one entry of a provider's model list.
type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Efforts []string `json:"efforts,omitempty"` // reasoning levels it lists, if any
	Context int      `json:"context,omitempty"` // context window, tokens
	MaxOut  int      `json:"max_out,omitempty"` // most output tokens per reply
}

// Models lists p's models from its /models path (OpenAI's shape, which
// Anthropic's /v1/models shares: data[].id).
func Models(ctx context.Context, client *http.Client, p Provider) ([]Model, error) {
	u := p.OpenAIBase() + "/models"
	if !p.Speaks(OpenAI) {
		u = p.AnthropicRoot() + "/v1/models?limit=1000"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		if p.Kind == Anthropic {
			req.Header.Set("x-api-key", p.APIKey)
		}
	}
	if p.Kind == Anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s: %s", u, res.Status, oneLine(string(body), 200))
	}
	var doc struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Name        string `json:"name"`
			Context     int    `json:"context_window"`
			ContextLen  int    `json:"context_length"`
			MaxOut      int    `json:"max_output_tokens"`
			Levels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: not a model list: %w", u, err)
	}
	out := make([]Model, 0, len(doc.Data))
	for _, d := range doc.Data {
		if d.ID == "" {
			continue
		}
		m := Model{ID: d.ID, Name: d.DisplayName, Context: d.Context, MaxOut: d.MaxOut}
		if m.Context == 0 {
			m.Context = d.ContextLen
		}
		if m.Name == "" {
			m.Name = d.Name
		}
		for _, l := range d.Levels {
			if l.Effort != "" {
				m.Efforts = append(m.Efforts, l.Effort)
			}
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
