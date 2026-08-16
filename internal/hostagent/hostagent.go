// Package hostagent discovers the host user's already-signed-in coding
// agents (Grok, Claude Code, Codex) and turns their on-disk config into a
// Chat backend. exe does not store its own LLM API keys.
package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Info is a host agent as shown in the Chat picker. Secrets never leave this
// package — Resolve keeps them on the daemon side.
type Info struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Ready        bool     `json:"ready"`
	Reason       string   `json:"reason,omitempty"`
	Auth         string   `json:"auth,omitempty"`
	Source       string   `json:"source,omitempty"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model,omitempty"`
	Email        string   `json:"email,omitempty"`
}

// Backend is what ChatSend needs to talk to a model. Kind is "codex"
// (ChatGPT Responses via ~/.codex), "openai" (/v1/chat/completions) or
// "ollama" (Ollama /api/chat).
type Backend struct {
	Kind         string
	BaseURL      string
	APIKey       string
	Model        string
	ExtraHeaders map[string]string
}

type grokEntry struct {
	Key          string `json:"key"`
	AuthMode     string `json:"auth_mode"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
	OIDCIssuer   string `json:"oidc_issuer"`
	OIDCClientID string `json:"oidc_client_id"`
}

type grokModelSpec struct {
	ID       string
	Model    string
	BaseURL  string
	APIKey   string
	APIStyle string // openai | ollama | messages
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

// List returns the host agents, ready or not, in a stable order.
func List() []Info {
	return []Info{listGrok(), listClaude(), listCodex()}
}

// Resolve picks an agent + model and returns a usable backend. Empty id
// selects the first ready agent. Empty model uses that agent's default.
func Resolve(id, model string) (*Backend, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	model = strings.TrimSpace(model)
	if id == "" {
		for _, a := range List() {
			if a.Ready {
				id = a.ID
				break
			}
		}
	}
	switch id {
	case "grok":
		return resolveGrok(model)
	case "claude":
		return resolveClaude(model)
	case "codex":
		return resolveCodex(model)
	case "":
		return nil, errors.New("no host agent is signed in (Grok ~/.grok, Claude ~/.claude, Codex ~/.codex)")
	default:
		return nil, fmt.Errorf("unknown host agent %q", id)
	}
}

func listGrok() Info {
	info := Info{ID: "grok", Name: "Grok", Source: "~/.grok", Models: nil}
	home := home()
	raw, err := os.ReadFile(filepath.Join(home, ".grok", "auth.json"))
	if err != nil {
		info.Reason = "no ~/.grok/auth.json — sign in with the grok CLI"
		return info
	}
	entry, key := firstGrokEntry(raw)
	if key == "" {
		info.Reason = "~/.grok/auth.json has no OAuth token"
		return info
	}
	info.Ready = true
	info.Auth = "oauth"
	info.Email = entry.Email
	info.Models = grokModels(home)
	info.DefaultModel = grokDefaultModel(home, info.Models)
	return info
}

func firstGrokEntry(raw []byte) (grokEntry, string) {
	var m map[string]grokEntry
	if json.Unmarshal(raw, &m) != nil {
		return grokEntry{}, ""
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := m[k]
		if strings.TrimSpace(e.Key) != "" {
			return e, e.Key
		}
	}
	return grokEntry{}, ""
}

func grokModels(home string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, m := range grokCacheModels(home) {
		add(m.ID)
	}
	cfg := readFile(filepath.Join(home, ".grok", "config.toml"))
	for _, spec := range parseGrokModelTables(cfg) {
		add(spec.ID)
	}
	if len(out) == 0 {
		add("grok-4")
		add("grok-3")
	}
	return out
}

type grokCached struct {
	ID      string
	Model   string
	BaseURL string
	Backend string
}

func grokCacheModels(home string) []grokCached {
	b, err := os.ReadFile(filepath.Join(home, ".grok", "models_cache.json"))
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	mb, ok := raw["models"]
	if !ok {
		return nil
	}
	var out []grokCached
	var list []string
	if json.Unmarshal(mb, &list) == nil {
		for _, id := range list {
			if id != "" {
				out = append(out, grokCached{ID: id, Model: id})
			}
		}
		return out
	}
	var obj map[string]struct {
		Info struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			BaseURL string `json:"base_url"`
			Backend string `json:"api_backend"`
		} `json:"info"`
	}
	if json.Unmarshal(mb, &obj) != nil {
		return nil
	}
	var keys []string
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		info := obj[k].Info
		id := info.ID
		if id == "" {
			id = k
		}
		model := info.Model
		if model == "" {
			model = id
		}
		out = append(out, grokCached{ID: id, Model: model, BaseURL: info.BaseURL, Backend: info.Backend})
	}
	return out
}

func grokDefaultModel(home string, models []string) string {
	cfg := readFile(filepath.Join(home, ".grok", "config.toml"))
	if d := tomlKey(cfg, "default"); d != "" {
		return d
	}
	if len(models) > 0 {
		return models[0]
	}
	return "grok-4"
}

func parseGrokModelTables(cfg string) []grokModelSpec {
	var specs []grokModelSpec
	// [model.name] or [model."name.with.dots"]
	sections := splitTOMLSections(cfg)
	for name, body := range sections {
		if !strings.HasPrefix(name, "model.") {
			continue
		}
		id := strings.TrimPrefix(name, "model.")
		if strings.HasPrefix(id, `"`) {
			end := strings.Index(id[1:], `"`)
			if end < 0 {
				continue
			}
			if rest := id[end+2:]; rest != "" { // [model."foo".extra_headers]
				continue
			}
			id = id[1 : end+1]
		} else if strings.Contains(id, ".") { // [model.foo.extra_headers]
			continue
		}
		spec := grokModelSpec{
			ID:       id,
			Model:    tomlKey(body, "model"),
			BaseURL:  tomlKey(body, "base_url"),
			APIKey:   tomlKey(body, "api_key"),
			APIStyle: tomlKey(body, "api_backend"),
		}
		if spec.Model == "" {
			spec.Model = id
		}
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs
}

func resolveGrok(model string) (*Backend, error) {
	home := home()
	raw, err := os.ReadFile(filepath.Join(home, ".grok", "auth.json"))
	if err != nil {
		return nil, errors.New("no ~/.grok/auth.json — sign in with the grok CLI")
	}
	entry, token := firstGrokEntry(raw)
	if token == "" {
		return nil, errors.New("~/.grok/auth.json has no OAuth token")
	}
	if expired(entry.ExpiresAt) && entry.RefreshToken != "" {
		if nt, err := refreshGrok(entry); err == nil && nt != "" {
			token = nt
			_ = writeGrokToken(filepath.Join(home, ".grok", "auth.json"), raw, token)
		}
	}
	if model == "" {
		model = grokDefaultModel(home, grokModels(home))
	}
	for _, cached := range grokCacheModels(home) {
		if cached.ID == model || cached.Model == model {
			base := strings.TrimRight(cached.BaseURL, "/")
			if base == "" {
				base = "https://cli-chat-proxy.grok.com/v1"
			}
			return &Backend{Kind: "openai", BaseURL: base, APIKey: token, Model: cached.Model}, nil
		}
	}
	cfg := readFile(filepath.Join(home, ".grok", "config.toml"))
	for _, spec := range parseGrokModelTables(cfg) {
		if spec.ID == model || spec.Model == model {
			base := strings.TrimRight(spec.BaseURL, "/")
			if base == "" {
				break
			}
			key := spec.APIKey
			if key == "" {
				key = token
			}
			kind := "openai"
			if spec.APIStyle == "" && !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1") {
				kind = "ollama"
			}
			return &Backend{Kind: kind, BaseURL: base, APIKey: key, Model: spec.Model}, nil
		}
	}
	// Native grok-* models go through the same proxy the CLI uses.
	return &Backend{
		Kind:    "openai",
		BaseURL: "https://cli-chat-proxy.grok.com/v1",
		APIKey:  token,
		Model:   model,
	}, nil
}

func expired(rfc3339 string) bool {
	if rfc3339 == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		t, err = time.Parse(time.RFC3339, rfc3339)
	}
	if err != nil {
		return false
	}
	return time.Now().After(t.Add(-2 * time.Minute))
}

func refreshGrok(e grokEntry) (string, error) {
	issuer := strings.TrimRight(e.OIDCIssuer, "/")
	if issuer == "" {
		issuer = "https://auth.x.ai"
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {e.RefreshToken},
		"client_id":     {e.OIDCClientID},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("grok token refresh: HTTP %d", resp.StatusCode)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil || tr.AccessToken == "" {
		return "", errors.New("grok token refresh: no access_token")
	}
	return tr.AccessToken, nil
}

func writeGrokToken(path string, orig []byte, newKey string) error {
	var m map[string]map[string]any
	if json.Unmarshal(orig, &m) != nil {
		return errors.New("parse grok auth")
	}
	for k, e := range m {
		if s, _ := e["key"].(string); s != "" {
			e["key"] = newKey
			e["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
			m[k] = e
			break
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func listClaude() Info {
	info := Info{
		ID: "claude", Name: "Claude", Source: "~/.claude",
		Models:       []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"},
		DefaultModel: "claude-sonnet-4-6",
	}
	home := home()
	cj := readJSON(filepath.Join(home, ".claude.json"))
	if ms, ok := cj["models"].([]any); ok {
		for _, m := range ms {
			if s, ok := m.(string); ok && s != "" {
				info.Models = appendUnique(info.Models, s)
			}
		}
	}
	if key, _ := cj["apiKey"].(string); strings.TrimSpace(key) != "" {
		info.Ready = true
		info.Auth = "apikey"
		return info
	}
	cred := readJSON(filepath.Join(home, ".claude", ".credentials.json"))
	oauth, _ := cred["claudeAiOauth"].(map[string]any)
	tok, _ := oauth["accessToken"].(string)
	if strings.TrimSpace(tok) == "" {
		info.Reason = "no Claude API key or OAuth — sign in with the claude CLI"
		return info
	}
	info.Ready = true
	info.Auth = "oauth"
	if oa, ok := cj["oauthAccount"].(map[string]any); ok {
		if e, _ := oa["emailAddress"].(string); e != "" {
			info.Email = e
		}
	}
	return info
}

func resolveClaude(model string) (*Backend, error) {
	home := home()
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	cj := readJSON(filepath.Join(home, ".claude.json"))
	if key, _ := cj["apiKey"].(string); strings.TrimSpace(key) != "" {
		return &Backend{
			Kind:    "openai",
			BaseURL: "https://api.anthropic.com/v1",
			APIKey:  strings.TrimSpace(key),
			Model:   model,
			ExtraHeaders: map[string]string{
				"anthropic-version": "2023-06-01",
				"x-api-key":         strings.TrimSpace(key),
			},
		}, nil
	}
	cred := readJSON(filepath.Join(home, ".claude", ".credentials.json"))
	oauth, _ := cred["claudeAiOauth"].(map[string]any)
	tok, _ := oauth["accessToken"].(string)
	if strings.TrimSpace(tok) == "" {
		return nil, errors.New("no Claude API key or OAuth — sign in with the claude CLI")
	}
	return &Backend{
		Kind:    "openai",
		BaseURL: "https://api.anthropic.com/v1",
		APIKey:  tok,
		Model:   model,
		ExtraHeaders: map[string]string{
			"anthropic-version": "2023-06-01",
			"anthropic-beta":    "oauth-2025-04-20",
		},
	}, nil
}

func listCodex() Info {
	info := Info{ID: "codex", Name: "Codex", Source: "~/.codex",
		Models: []string{"gpt-5.4", "gpt-5.4-codex"}, DefaultModel: "gpt-5.4"}
	home := home()
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		info.Reason = "no ~/.codex/auth.json — sign in with the Codex CLI"
		return info
	}
	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
		APIKey string `json:"OPENAI_API_KEY"`
	}
	if json.Unmarshal(raw, &auth) != nil {
		info.Reason = "could not parse ~/.codex/auth.json"
		return info
	}
	if auth.Tokens.AccessToken == "" && auth.APIKey == "" {
		info.Reason = "~/.codex/auth.json has no token — run: codex login"
		return info
	}
	info.Ready = true
	info.Auth = "chatgpt"
	if auth.AuthMode != "" {
		info.Auth = auth.AuthMode
	}
	cfg := readFile(filepath.Join(home, ".codex", "config.toml"))
	if m := tomlKey(cfg, "model"); m != "" {
		info.DefaultModel = m
		info.Models = appendUnique([]string{m}, info.Models...)
	}
	return info
}

func resolveCodex(model string) (*Backend, error) {
	info := listCodex()
	if !info.Ready {
		return nil, errors.New(info.Reason)
	}
	if model == "" {
		model = info.DefaultModel
	}
	return &Backend{Kind: "codex", Model: model}, nil
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func readJSON(p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return map[string]any{}
	}
	return m
}

func appendUnique(dst []string, extra ...string) []string {
	seen := map[string]bool{}
	for _, s := range dst {
		seen[s] = true
	}
	for _, s := range extra {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		dst = append(dst, s)
	}
	return dst
}

// tomlKey returns the first `key = "value"` or `key = 'value'` in src.
func tomlKey(src, key string) string {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		if k != key {
			continue
		}
		return unquoteTOML(strings.TrimSpace(line[eq+1:]))
	}
	return ""
}

func unquoteTOML(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

// splitTOMLSections maps "[section]" heading → body (until next heading).
func splitTOMLSections(src string) map[string]string {
	out := map[string]string{}
	var cur string
	var buf bytes.Buffer
	flush := func() {
		if cur != "" {
			out[cur] = buf.String()
		}
		buf.Reset()
	}
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") && !strings.HasPrefix(trim, "[[") {
			flush()
			cur = strings.TrimSpace(trim[1 : len(trim)-1])
			continue
		}
		if cur != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	return out
}
