package board

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replay feeds a recorded CLI stream through agent's parser.
func replay(t *testing.T, agent, fixture string) ([]Event, Result) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	p := NewParser(agent)
	var evs []Event
	for _, line := range bytes.Split(b, []byte("\n")) {
		evs = append(evs, p.Feed(line)...)
	}
	return evs, p.Result()
}

func types(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Type)
	}
	return strings.Join(s, ",")
}

func TestClaudeStreamToolCalls(t *testing.T) {
	evs, res := replay(t, Claude, "claude_tool.jsonl")
	want := "text,tool,tool_result,tool,tool_result,tool,tool_result,text"
	if got := types(evs); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	if evs[1].ID != "toolu_01A9" || evs[1].Name != "Bash" || evs[1].Summary != "Bash: npm test" {
		t.Errorf("bash tool = %+v", evs[1])
	}
	if evs[2].ID != "toolu_01A9" || evs[2].IsError || !strings.Contains(evs[2].Output, "# pass 12") {
		t.Errorf("bash result = %+v", evs[2])
	}
	if evs[3].Summary != "Read: /home/exe/work/NOTES.md" || !evs[4].IsError {
		t.Errorf("read = %+v / %+v", evs[3], evs[4])
	}
	if evs[5].Summary != "TodoWrite: 2 items" || evs[6].Output != "Todos have been modified successfully." {
		t.Errorf("todo = %+v / %+v", evs[5], evs[6])
	}
	if res.SessionID != "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10" || !res.Done || res.Error != "" {
		t.Errorf("result = %+v", res)
	}
	if res.LastText != "All 12 tests pass. There is no NOTES.md yet." {
		t.Errorf("last text = %q", res.LastText)
	}
	u := res.Usage
	if u == nil || u.InputTokens != 12+4400+15500 || u.CachedInputTokens != 15500 || u.OutputTokens != 210 || u.CostUSD == nil || *u.CostUSD != 0.0421 {
		t.Errorf("usage = %+v", u)
	}
}

func TestClaudeStreamError(t *testing.T) {
	evs, res := replay(t, Claude, "claude_error.jsonl")
	if types(evs) != "text" {
		t.Fatalf("events = %s", types(evs))
	}
	if res.Error != "Invalid API key · Please run /login" || !res.Done {
		t.Errorf("result = %+v", res)
	}
}

func TestClaudeStreamResume(t *testing.T) {
	evs, res := replay(t, Claude, "claude_resume.jsonl")
	if got := types(evs); got != "tool,tool_result,status,text" {
		t.Fatalf("events = %s", got)
	}
	if evs[0].Summary != "Edit: /home/exe/work/app/main.go" || evs[2].Text != "context compacted" {
		t.Errorf("events = %+v", evs)
	}
	if res.SessionID != "5d3e8f20-1a2b-4c3d-9e8f-001122334455" || res.Error != "" {
		t.Errorf("result = %+v", res)
	}
}

func TestClaudeResultErrorSubtype(t *testing.T) {
	p := NewParser(Claude)
	p.Feed([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"s1"}`))
	if r := p.Result(); r.Error != "error max turns" || r.SessionID != "s1" {
		t.Errorf("result = %+v", r)
	}
}

func TestCodexStreamToolCalls(t *testing.T) {
	evs, res := replay(t, Codex, "codex_tool.jsonl")
	want := "tool,tool_result,tool,tool_result,tool,tool,tool_result,tool,tool_result,tool_result,text"
	if got := types(evs); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	if evs[0].ID != "item_1" || evs[0].Summary != "shell: bash -lc 'npm test'" || evs[1].IsError {
		t.Errorf("npm test = %+v / %+v", evs[0], evs[1])
	}
	if !evs[3].IsError || !strings.Contains(evs[3].Output, "No such file") || !strings.Contains(evs[3].Output, "(exit 1)") {
		t.Errorf("failed command = %+v", evs[3])
	}
	if evs[4].Summary != "todo: 2 items" || evs[5].Summary != "edit: add /home/exe/work/NOTES.md" {
		t.Errorf("todo/edit = %+v / %+v", evs[4], evs[5])
	}
	if evs[7].Name != "docs.search" || evs[8].Output != "node --test runs *.test.js" {
		t.Errorf("mcp = %+v / %+v", evs[7], evs[8])
	}
	if evs[9].ID != "item_3" || evs[9].Output != "[x] Run tests\n[x] Write notes" {
		t.Errorf("todo result = %+v", evs[9])
	}
	if res.SessionID != "0199a213-81c0-7800-8aa1-bbab2a035a53" || !res.Done || res.Error != "" {
		t.Errorf("result = %+v", res)
	}
	if u := res.Usage; u == nil || u.InputTokens != 24763 || u.CachedInputTokens != 24448 || u.OutputTokens != 122 || u.CostUSD != nil {
		t.Errorf("usage = %+v", u)
	}
}

func TestCodexStreamError(t *testing.T) {
	evs, res := replay(t, Codex, "codex_error.jsonl")
	if types(evs) != "status,status" || evs[0].Text != "Reconnecting... 1/5" {
		t.Fatalf("events = %+v", evs)
	}
	if !strings.Contains(res.Error, "401 Unauthorized") {
		t.Errorf("result = %+v", res)
	}
}

func TestCodexStreamResume(t *testing.T) {
	evs, res := replay(t, Codex, "codex_resume.jsonl")
	if types(evs) != "tool,tool_result,text" || evs[0].Summary != "web_search: node test runner coverage flag" {
		t.Fatalf("events = %+v", evs)
	}
	if res.SessionID != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Errorf("result = %+v", res)
	}
}

// A stream that dies with only an error event still reports why.
func TestCodexErrorWithoutTurnFailed(t *testing.T) {
	p := NewParser(Codex)
	p.Feed([]byte(`{"type":"error","message":"stream disconnected before completion"}`))
	if r := p.Result(); r.Done || r.Error != "stream disconnected before completion" {
		t.Errorf("result = %+v", r)
	}
}

// Unknown event types are ignored; non-JSON lines become status lines
// only when they read like an error.
func TestParsersTolerateNoise(t *testing.T) {
	for _, agent := range []string{Claude, Codex} {
		p := NewParser(agent)
		var evs []Event
		for _, l := range []string{
			`{"type":"stream_event","event":{"type":"content_block_delta"}}`,
			`{"type":"something.new","item":{"id":"x"}}`,
			`{"type":"item.completed","item":{"id":"y","type":"brand_new_item"}}`,
			`not json, just chatter`,
			"\x1b[31mError: unknown option '--frobnicate'\x1b[0m",
			`{"truncated":`,
			``,
		} {
			evs = append(evs, p.Feed([]byte(l))...)
		}
		if len(evs) != 1 || evs[0].Type != EvStatus || evs[0].Text != "Error: unknown option '--frobnicate'" {
			t.Errorf("%s: events = %+v", agent, evs)
		}
	}
}

func TestClipBytes(t *testing.T) {
	s := strings.Repeat("é", 3000) // 6000 bytes
	c := clipBytes(s, outputMax)
	if !strings.HasPrefix(c, strings.Repeat("é", outputMax/2)) || !strings.Contains(c, "bytes clipped") {
		t.Errorf("clip = %q…", c[:20])
	}
	if strings.ContainsRune(c, '�') {
		t.Error("clip split a rune")
	}
}
