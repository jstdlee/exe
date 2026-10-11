package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreKeepsKeysAndMagpie(t *testing.T) {
	st := NewStore(t.TempDir())
	ps := st.Providers()
	if len(ps) != 1 || ps[0].ID != MagpieID || ps[0].BaseURL != "http://127.0.0.1:3425/v1" {
		t.Fatalf("fresh store = %+v", ps)
	}
	out, err := st.SetProviders([]Provider{{Name: "Remote", Kind: "OpenAI", BaseURL: "https://api.example.com/v1/", APIKey: "sk-1", Model: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	id := out[0].ID
	if id == "" || out[0].BaseURL != "https://api.example.com/v1" || out[0].Kind != OpenAI {
		t.Fatalf("saved = %+v", out[0])
	}
	// an empty key keeps the saved one; the API never shows it
	if _, err := st.SetProviders([]Provider{{ID: id, Name: "Remote", Kind: OpenAI, BaseURL: "https://api.example.com/v1"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Get(id)
	if p.APIKey != "sk-1" {
		t.Fatalf("key lost: %+v", p)
	}
	pub := Public(st.Providers())
	if pub[1].APIKey != "" || !pub[1].KeySet {
		t.Fatalf("public = %+v", pub[1])
	}
	// "-" clears it
	st.SetProviders([]Provider{{ID: id, Name: "Remote", Kind: OpenAI, BaseURL: "https://api.example.com/v1", APIKey: "-"}})
	if p, _ := st.Get(id); p.APIKey != "" {
		t.Fatalf("key not cleared: %+v", p)
	}
	if _, err := st.SetProviders([]Provider{{Name: "x", Kind: OpenAI, BaseURL: "ftp://x"}}); err == nil {
		t.Fatal("ftp URL accepted")
	}
	if err := st.Remember("codex", Choice{Provider: MagpieID, Model: "m", Thinking: "high"}); err != nil {
		t.Fatal(err)
	}
	if c := NewStore(filepath.Dir(st.path)).Last()["codex"]; c.Model != "m" || c.Thinking != "high" {
		t.Fatalf("last = %+v", c)
	}
	if fi, err := os.Stat(st.path); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Fatalf("llm.json mode: %v %v", fi, err)
	}
}

func TestURLs(t *testing.T) {
	m := Magpie()
	if m.AnthropicRoot() != "http://127.0.0.1:3425" || m.OpenAIBase() != "http://127.0.0.1:3425/v1" {
		t.Fatalf("magpie urls %q %q", m.AnthropicRoot(), m.OpenAIBase())
	}
	hp, ok := m.Loopback()
	if !ok || hp != "127.0.0.1:3425" {
		t.Fatalf("loopback = %q %v", hp, ok)
	}
	if got := m.Rehost("127.0.0.1:40001").BaseURL; got != "http://127.0.0.1:40001/v1" {
		t.Fatalf("rehost = %q", got)
	}
	if _, ok := (Provider{BaseURL: "https://api.example.com/v1"}).Loopback(); ok {
		t.Fatal("remote host taken for loopback")
	}
	if hp, ok := (Provider{BaseURL: "http://localhost/v1"}).Loopback(); !ok || hp != "localhost:80" {
		t.Fatalf("localhost = %q %v", hp, ok)
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "no", 401)
			return
		}
		w.Write([]byte(`{"data":[{"id":"b","display_name":"Bee","context_window":1000,"max_output_tokens":99999,
			"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},{"id":"a"}]}`))
	}))
	defer srv.Close()
	ms, err := Models(context.Background(), srv.Client(), Provider{Kind: OpenAI, BaseURL: srv.URL + "/v1", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "a" || ms[1].Name != "Bee" || strings.Join(ms[1].Efforts, ",") != "low,high" || ms[1].Context != 1000 {
		t.Fatalf("models = %+v", ms)
	}
	if lim := limits(ms[1]); lim[0] != 1000 || lim[1] != 1000 {
		t.Fatalf("limits = %v", lim)
	}
	if _, err := Models(context.Background(), srv.Client(), Provider{Kind: OpenAI, BaseURL: srv.URL + "/v1"}); err == nil {
		t.Fatal("401 not an error")
	}
}

func TestScriptsParse(t *testing.T) {
	m := Magpie()
	for _, a := range AgentList() {
		for _, lvl := range append([]string{""}, Levels...) {
			for _, p := range []*Provider{nil, &m, {Name: "A", Kind: Anthropic, BaseURL: "https://x.example/v1", APIKey: "it's"}} {
				spec := Spec{Agent: a.ID, Provider: p, Model: Model{ID: "group/m-1", Context: 200000, MaxOut: 64000}, Thinking: lvl}
				s, err := Script(spec, `"$HOME/l.sh"`)
				if p != nil && a.API(*p) == "" {
					if err == nil {
						t.Errorf("%s on %s: no error", a.ID, p.Name)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s %s: %v", a.ID, lvl, err)
				}
				if out, err := exec.Command("sh", "-n", "-c", s).CombinedOutput(); err != nil {
					t.Errorf("%s %q: sh -n: %v %s\n%s", a.ID, lvl, err, out, s)
				}
				if !strings.HasPrefix(s, `rm -f "$HOME/l.sh"`) {
					t.Errorf("%s: does not remove itself first", a.ID)
				}
			}
		}
	}
	if _, err := Script(Spec{Agent: "codex", Provider: &m}, "x"); err == nil {
		t.Error("no model accepted")
	}
	if _, err := Script(Spec{Agent: "codex", Thinking: "huge"}, "x"); err == nil {
		t.Error("bad level accepted")
	}
}

// runScript runs s up to its exec (replaced by printing the command) in a
// scratch HOME.
func runScript(t *testing.T, home, s string) string {
	t.Helper()
	s = strings.Replace(s, "\nexec ", "\necho ", 1)
	cmd := exec.Command("sh", "-c", s)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s\n%s", err, out, s)
	}
	return string(out)
}

func TestOmpMerge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	home := t.TempDir()
	f := filepath.Join(home, ".omp/agent/models.yml")
	os.MkdirAll(filepath.Dir(f), 0o700)
	os.WriteFile(f, []byte("# mine\nproviders:\n    local:\n        baseUrl: http://x\nother: 1\n"), 0o600)
	m := Magpie()
	s, _ := Script(Spec{Agent: "omp", Provider: &m, Model: Model{ID: "g/m"}, Thinking: "high"}, `"$HOME/l.sh"`)
	out := runScript(t, home, s)
	if !strings.Contains(out, "omp --provider exe --model g/m --thinking high") {
		t.Errorf("run = %q", out)
	}
	runScript(t, home, s) // twice: one block, not two
	b, _ := os.ReadFile(f)
	got := string(b)
	if strings.Count(got, "exe: {") != 1 || !strings.Contains(got, "\n    exe: {\"api\":\"openai-completions\"") ||
		!strings.Contains(got, "    local:\n        baseUrl: http://x\nother: 1\n") || !strings.HasPrefix(got, "# mine\nproviders:\n") {
		t.Errorf("models.yml =\n%s", got)
	}
	// a URL with & and a backslash in a model id arrive as written
	q := Provider{Name: "Q", Kind: OpenAI, BaseURL: "https://x.example/v1?a=1&b=2"}
	s2, _ := Script(Spec{Agent: "omp", Provider: &q, Model: Model{ID: `odd\id`}}, `"$HOME/l.sh"`)
	runScript(t, home, s2)
	b, _ = os.ReadFile(f)
	if !strings.Contains(string(b), `"baseUrl":"https://x.example/v1?a=1&b=2"`) || !strings.Contains(string(b), `"id":"odd\\id"`) {
		t.Errorf("escaped models.yml =\n%s", b)
	}
	// no file at all
	home2 := t.TempDir()
	runScript(t, home2, s)
	b, _ = os.ReadFile(filepath.Join(home2, ".omp/agent/models.yml"))
	if !strings.HasPrefix(string(b), "providers:\n  # >>> exe provider") {
		t.Errorf("fresh models.yml =\n%s", b)
	}
}

func TestGrokBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	home := t.TempDir()
	f := filepath.Join(home, ".grok/config.toml")
	os.MkdirAll(filepath.Dir(f), 0o700)
	os.WriteFile(f, []byte("[models]\ndefault = \"grok-4.7\"\n"), 0o600)
	m := Magpie()
	s, _ := Script(Spec{Agent: "grok", Provider: &m, Model: Model{ID: "g/m"}, Thinking: "low"}, `"$HOME/l.sh"`)
	runScript(t, home, s)
	out := runScript(t, home, s)
	if !strings.Contains(out, "grok -m exe --reasoning-effort low") {
		t.Errorf("run = %q", out)
	}
	b, _ := os.ReadFile(f)
	if got := string(b); strings.Count(got, "[model.exe]") != 1 || !strings.HasPrefix(got, "[models]\ndefault = \"grok-4.7\"\n") ||
		!strings.Contains(got, `base_url = "http://127.0.0.1:3425/v1"`) || strings.Contains(got, "exe-none") {
		t.Errorf("config.toml =\n%s", got)
	}
}

func TestPiMerge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	home := t.TempDir()
	f := filepath.Join(home, ".pi/agent/models.json")
	os.MkdirAll(filepath.Dir(f), 0o700)
	os.WriteFile(f, []byte(`{"providers":{"mine":{"baseUrl":"http://x"}},"keep":true}`), 0o600)
	p := Provider{Name: "A", Kind: Anthropic, BaseURL: "https://x.example/v1", APIKey: "secret"}
	s, _ := Script(Spec{Agent: "pi", Provider: &p, Model: Model{ID: "claude-x"}}, `"$HOME/l.sh"`)
	out := runScript(t, home, s)
	if !strings.Contains(out, "pi --provider exe --model claude-x") {
		t.Errorf("run = %q", out)
	}
	var d struct {
		Providers map[string]map[string]any `json:"providers"`
		Keep      bool                      `json:"keep"`
	}
	b, _ := os.ReadFile(f)
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	e := d.Providers["exe"]
	if !d.Keep || d.Providers["mine"] == nil || e["baseUrl"] != "https://x.example" || e["api"] != "anthropic-messages" || e["apiKey"] != KeyVar {
		t.Errorf("models.json = %s", b)
	}
	if strings.Contains(string(b), "secret") {
		t.Error("the key reached models.json")
	}
	// a file that is not JSON is left alone
	os.WriteFile(f, []byte("{oops"), 0o600)
	cmd := exec.Command("sh", "-c", strings.Replace(s, "\nexec ", "\necho ", 1))
	cmd.Env = append(os.Environ(), "HOME="+home)
	if err := cmd.Run(); err == nil {
		t.Error("broken models.json overwritten")
	}
	if b, _ := os.ReadFile(f); string(b) != "{oops" {
		t.Errorf("models.json = %q", b)
	}
}
