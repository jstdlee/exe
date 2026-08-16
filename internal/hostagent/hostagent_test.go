package hostagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndResolveFromHostFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// user.Current / UserHomeDir may ignore HOME on some systems — pin via
	// both HOME and a fake XDG; UserHomeDir on Linux uses HOME.
	if err := os.MkdirAll(filepath.Join(dir, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := map[string]any{
		"https://auth.x.ai::abc": map[string]any{
			"key":   "test-jwt",
			"email": "dev@example.com",
		},
	}
	b, _ := json.Marshal(auth)
	if err := os.WriteFile(filepath.Join(dir, ".grok", "auth.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `[models]
default = "grok-4.6"

[model.kimi]
model = "kimi-k3:cloud"
base_url = "https://ollama.com/v1"
api_key = "kimi-key"
`
	if err := os.WriteFile(filepath.Join(dir, ".grok", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := []byte(`{"models":["grok-4.6","grok-4.5"]}`)
	if err := os.WriteFile(filepath.Join(dir, ".grok", "models_cache.json"), cache, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"apiKey":"sk-ant-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"atok","account_id":"acct"}}`)
	if err := os.WriteFile(filepath.Join(dir, ".codex", "auth.json"), codex, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte("model = \"gpt-5.6-terra\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list := List()
	byID := map[string]Info{}
	for _, a := range list {
		byID[a.ID] = a
	}
	if !byID["grok"].Ready || byID["grok"].Email != "dev@example.com" {
		t.Fatalf("grok: %+v", byID["grok"])
	}
	if byID["grok"].DefaultModel != "grok-4.6" {
		t.Fatalf("grok default = %q", byID["grok"].DefaultModel)
	}
	if !byID["claude"].Ready || byID["claude"].Auth != "apikey" {
		t.Fatalf("claude: %+v", byID["claude"])
	}
	if !byID["codex"].Ready || byID["codex"].DefaultModel != "gpt-5.6-terra" {
		t.Fatalf("codex: %+v", byID["codex"])
	}

	g, err := Resolve("grok", "kimi")
	if err != nil {
		t.Fatal(err)
	}
	if g.Kind != "openai" || g.BaseURL != "https://ollama.com/v1" || g.APIKey != "kimi-key" || g.Model != "kimi-k3:cloud" {
		t.Fatalf("resolve kimi: %+v", g)
	}
	gn, err := Resolve("grok", "grok-4.6")
	if err != nil {
		t.Fatal(err)
	}
	if gn.Kind != "openai" || gn.APIKey != "test-jwt" || gn.Model != "grok-4.6" {
		t.Fatalf("resolve grok native: %+v", gn)
	}
	c, err := Resolve("claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "openai" || c.APIKey != "sk-ant-test" || c.Model != "claude-sonnet-4-6" {
		t.Fatalf("resolve claude: %+v", c)
	}
	x, err := Resolve("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if x.Kind != "codex" || x.Model != "gpt-5.6-terra" {
		t.Fatalf("resolve codex: %+v", x)
	}
}

func TestGrokSkipsExtraHeaderSections(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := map[string]any{"https://auth.x.ai::x": map[string]any{"key": "jwt"}}
	b, _ := json.Marshal(auth)
	os.WriteFile(filepath.Join(dir, ".grok", "auth.json"), b, 0o600)
	os.WriteFile(filepath.Join(dir, ".grok", "models_cache.json"), []byte(`{"models":{"grok-4.6":{"info":{"id":"grok-4.6","model":"grok-4.6","base_url":"https://cli-chat-proxy.grok.com/v1"}}}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".grok", "config.toml"), []byte(`
[model."minimax-m2.7"]
model = "MiniMax-M2.7"
base_url = "https://api.minimax.io/anthropic"
[model."minimax-m2.7".extra_headers]
anthropic-version = "2023-06-01"
`), 0o644)
	info := listGrok()
	for _, m := range info.Models {
		if strings.Contains(m, "extra_headers") {
			t.Fatalf("models include extra_headers: %v", info.Models)
		}
	}
	if !contains(info.Models, "grok-4.6") || !contains(info.Models, "minimax-m2.7") {
		t.Fatalf("models = %v", info.Models)
	}
	be, err := Resolve("grok", "grok-4.6")
	if err != nil {
		t.Fatal(err)
	}
	if be.BaseURL != "https://cli-chat-proxy.grok.com/v1" || be.Model != "grok-4.6" {
		t.Fatalf("%+v", be)
	}
}

func contains(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}

func TestTOMLSections(t *testing.T) {
	src := `
[models]
default = "a"
[model.foo]
base_url = "https://x"
[model."bar.baz"]
model = "M"
`
	sec := splitTOMLSections(src)
	if tomlKey(sec["models"], "default") != "a" {
		t.Fatalf("models: %q", sec["models"])
	}
	if tomlKey(sec["model.foo"], "base_url") != "https://x" {
		t.Fatalf("foo: %q", sec["model.foo"])
	}
	if tomlKey(sec[`model."bar.baz"`], "model") != "M" {
		t.Fatalf("bar: %q", sec[`model."bar.baz"`])
	}
}
