package server

import (
	"strings"
	"testing"
)

func TestGuestRunScriptAgentsAndTools(t *testing.T) {
	if GuestRunScript("") != "" {
		t.Fatal("empty id")
	}
	if GuestRunScript("nope") != "" {
		t.Fatal("unknown")
	}
	cl := GuestRunScript("claude")
	if !strings.Contains(cl, "exec claude") || !strings.Contains(cl, "installing") {
		t.Fatalf("claude script: %s", cl)
	}
	op := GuestRunScript("opencode")
	if !strings.Contains(op, "ensure_guest_swap") || !strings.Contains(op, "/usr/sbin") || !strings.Contains(op, "swapon /swapfile") || !strings.Contains(op, "exec opencode") {
		t.Fatalf("opencode script should add swap before running: %s", op)
	}
	git := GuestRunScript("git")
	if !strings.Contains(git, "apt-get install -y git") || !strings.Contains(git, "exec bash -l") {
		t.Fatalf("git script: %s", git)
	}
}

func TestGuestRunScriptIncludesExpandedAgentCatalogWithoutTranscripts(t *testing.T) {
	for _, tc := range []struct {
		id  string
		bin string
	}{
		{id: "gemini", bin: "gemini"},
		{id: "aider", bin: "aider"},
		{id: "qwen", bin: "qwen"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			script := GuestRunScript(tc.id)
			if script == "" {
				t.Fatalf("%s script missing", tc.id)
			}
			if !strings.Contains(script, "exec "+tc.bin) {
				t.Fatalf("%s script should exec %s: %s", tc.id, tc.bin, script)
			}
			if strings.Contains(strings.ToLower(script), "transcript") {
				t.Fatalf("%s VM CLI script must not create host transcripts: %s", tc.id, script)
			}
		})
	}
}

func TestGuestRunScriptIncludesExpandedDevToolCatalog(t *testing.T) {
	for _, tc := range []struct {
		id      string
		wantBin string
	}{
		{id: "uv", wantBin: "uv"},
		{id: "pnpm", wantBin: "pnpm"},
		{id: "bun", wantBin: "bun"},
		{id: "lazygit", wantBin: "lazygit"},
		{id: "hyperfine", wantBin: "hyperfine"},
		{id: "direnv", wantBin: "direnv"},
		{id: "just", wantBin: "just"},
		{id: "bat", wantBin: "batcat"},
		{id: "eza", wantBin: "eza"},
		{id: "httpie", wantBin: "http"},
		{id: "yq", wantBin: "yq"},
		{id: "delta", wantBin: "delta"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			script := GuestRunScript(tc.id)
			if script == "" {
				t.Fatalf("%s script missing", tc.id)
			}
			if !strings.Contains(script, "command -v "+tc.wantBin) {
				t.Fatalf("%s script should check for %s: %s", tc.id, tc.wantBin, script)
			}
			if !strings.Contains(script, "exec bash -l") {
				t.Fatalf("%s tool script should end in a login shell: %s", tc.id, script)
			}
		})
	}
}
