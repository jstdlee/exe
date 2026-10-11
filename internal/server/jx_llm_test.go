package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"exe/internal/jx/llm"
	"exe/internal/sshexec"
	"exe/internal/vmm"
)

func TestJXLLMRoutes(t *testing.T) {
	_, ts := newJXTestServer(t, nil)
	var got struct {
		Providers []llm.Provider        `json:"providers"`
		Agents    []llm.Agent           `json:"agents"`
		Last      map[string]llm.Choice `json:"last"`
	}
	if code := jxDo(t, ts, "GET", "/v1/jx/llm", nil, &got); code != 200 || len(got.Providers) != 1 || got.Providers[0].ID != llm.MagpieID || len(got.Agents) != 6 {
		t.Fatalf("GET = %d %+v", code, got)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-x" {
			http.Error(w, "no key", 401)
			return
		}
		w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer up.Close()
	body := map[string]any{"providers": []map[string]any{{"name": "Up", "kind": "openai", "base_url": up.URL + "/v1", "api_key": "sk-x", "model": "m1"}}}
	if code := jxDo(t, ts, "PUT", "/v1/jx/llm/providers", body, &got); code != 200 || len(got.Providers) != 2 {
		t.Fatalf("PUT = %d %+v", code, got)
	}
	p := got.Providers[1]
	if p.APIKey != "" || !p.KeySet {
		t.Fatalf("the key came back: %+v", p)
	}
	// a saved provider by id, its key from the store
	var ms struct{ Models []llm.Model }
	if code := jxDo(t, ts, "POST", "/v1/jx/llm/models", map[string]string{"id": p.ID}, &ms); code != 200 || len(ms.Models) != 2 {
		t.Fatalf("models = %d %+v", code, ms)
	}
	// the form being edited: no key typed uses the saved one
	if code := jxDo(t, ts, "POST", "/v1/jx/llm/models", map[string]string{"id": p.ID, "kind": "openai", "base_url": up.URL + "/v1"}, &ms); code != 200 {
		t.Fatalf("form models = %d", code)
	}
	if code := jxDo(t, ts, "POST", "/v1/jx/llm/models", map[string]string{"kind": "openai", "base_url": up.URL + "/v1"}, nil); code != http.StatusBadGateway {
		t.Fatalf("keyless models = %d", code)
	}
}

func TestJXLaunchCommand(t *testing.T) {
	vms := newJXFakeVMs(vmm.Info{Name: "box", State: "running", IP: "10.0.0.2"})
	s, _ := newJXTestServer(t, vms)
	var wrote []string
	stopped := 0
	prevW, prevF, prevM := jxGuestWrite, jxForward, jxLLMModels
	t.Cleanup(func() { jxGuestWrite, jxForward, jxLLMModels = prevW, prevF, prevM })
	jxGuestWrite = func(ctx context.Context, tg sshexec.Target, script, stdin string) error {
		wrote = append(wrote, script, stdin)
		return nil
	}
	jxForward = func(ctx context.Context, tg sshexec.Target, hostport string) (string, func(), error) {
		if hostport != "127.0.0.1:3425" {
			t.Errorf("forward to %s", hostport)
		}
		return "127.0.0.1:40123", func() { stopped++ }, nil
	}
	jxLLMModels = func(ctx context.Context, c *http.Client, p llm.Provider) ([]llm.Model, error) {
		return []llm.Model{{ID: "g/m", Context: 256000, MaxOut: 384000}}, nil
	}

	cmd, done, err := s.jxLaunchCommand(context.Background(), "box", "tool=omp&provider=magpie&model=g%2Fm&thinking=high")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, `. "$HOME/.config/exe/llm/launch-`) || !strings.Contains(cmd, "@oh-my-pi/pi-coding-agent") {
		t.Errorf("cmd = %s", cmd)
	}
	if len(wrote) != 2 || !strings.Contains(wrote[0], "umask 077") || strings.Contains(wrote[0], llm.KeyVar) {
		t.Fatalf("write = %q", wrote)
	}
	if st := wrote[1]; !strings.Contains(st, `"baseUrl":"http://127.0.0.1:40123/v1"`) || !strings.Contains(st, `"maxTokens":32768`) ||
		!strings.Contains(st, "exec omp --provider exe --model 'g/m' --thinking 'high'") {
		t.Errorf("script = %s", st)
	}
	done()
	if stopped != 1 {
		t.Errorf("forward stopped %d times", stopped)
	}
	if c := s.jxLLM().Last()["omp"]; c.Provider != "magpie" || c.Model != "g/m" || c.Thinking != "high" {
		t.Errorf("last = %+v", c)
	}

	// own sign-in: no forward, no key
	wrote = nil
	cmd, done, err = s.jxLaunchCommand(context.Background(), "box", "tool=codex&thinking=low")
	if err != nil {
		t.Fatal(err)
	}
	done()
	if stopped != 1 || !strings.Contains(wrote[1], `exec codex -c 'model_reasoning_effort="low"'`) || strings.Contains(wrote[1], llm.KeyVar) {
		t.Errorf("own = %q (stopped %d)", wrote, stopped)
	}

	// refusals: not an agent, Claude Code on an OpenAI-only provider, a stopped VM
	s.jxLLM().SetProviders([]llm.Provider{{Name: "OAI", Kind: llm.OpenAI, BaseURL: "https://api.example.com/v1", Model: "x"}})
	oai := s.jxLLM().Providers()[1].ID
	for _, raw := range []string{"tool=btop", "tool=claude&provider=" + oai, "tool=codex&provider=nope"} {
		if _, _, err := s.jxLaunchCommand(context.Background(), "box", raw); err == nil {
			t.Errorf("%s: no error", raw)
		}
	}
	vms.Stop(context.Background(), "box")
	if _, _, err := s.jxLaunchCommand(context.Background(), "box", "tool=codex"); err == nil {
		t.Error("stopped VM: no error")
	}
}

// The hook leaves an ordinary terminal alone and turns a failed launch
// into a message in the terminal.
func TestJXLaunchHook(t *testing.T) {
	s, _ := newJXTestServer(t, newJXFakeVMs())
	r := httptest.NewRequest("GET", "/v1/vms/box/terminal?cmd=btop", nil)
	s.jxLaunch(r, "box")()
	if r.URL.Query().Get("cmd") != "btop" {
		t.Errorf("cmd = %q", r.URL.Query().Get("cmd"))
	}
	r = httptest.NewRequest("GET", "/v1/vms/box/terminal?cmd=jx-launch%3Atool%3Dcodex", nil)
	s.jxLaunch(r, "box")()
	if c := r.URL.Query().Get("cmd"); !strings.HasPrefix(c, "printf '%s\\n' 'exe: ") || !strings.Contains(c, "exec \"${SHELL:-/bin/sh}\" -l") {
		t.Errorf("cmd = %q", c)
	}
}
