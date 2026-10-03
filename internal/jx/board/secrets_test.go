package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecretStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	s := NewSecretStore(path)
	if st := s.Status(); st.ClaudeOAuthTokenSet || st.CodexAPIKeySet {
		t.Fatalf("fresh = %+v", st)
	}
	_, gen0 := s.Get()
	tok := " sk-ant-oat01-abc'def \n"
	if err := s.Set(&tok, nil); err == nil {
		t.Fatal("a value with a line break was accepted")
	}
	tok = " sk-ant-oat01-abc'def "
	if err := s.Set(&tok, nil); err != nil {
		t.Fatal(err)
	}
	sec, gen1 := s.Get()
	if sec.ClaudeOAuthToken != "sk-ant-oat01-abc'def" || gen1 == gen0 {
		t.Errorf("after set = %q gen %d", sec.ClaudeOAuthToken, gen1)
	}
	if err := s.Set(&sec.ClaudeOAuthToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, g := s.Get(); g != gen1 {
		t.Error("an unchanged set bumped the generation")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
			t.Errorf("secrets file mode = %v %v", st.Mode().Perm(), err)
		}
	}
	// a fresh store reads the file back
	s2 := NewSecretStore(path)
	if st := s2.Status(); !st.ClaudeOAuthTokenSet || st.CodexAPIKeySet {
		t.Errorf("reloaded = %+v", st)
	}
	b, _ := json.Marshal(s2.Status())
	if strings.Contains(string(b), "abc") {
		t.Errorf("status carries the value: %s", b)
	}

	env := EnvFile(Secrets{ClaudeOAuthToken: "a'b", CodexAPIKey: "k"})
	if !strings.HasPrefix(env, envHeader+"\n") || !strings.Contains(env, `export CLAUDE_CODE_OAUTH_TOKEN='a'\''b'`) || !strings.Contains(env, "export OPENAI_API_KEY='k'") {
		t.Errorf("env file = %q", env)
	}
}
