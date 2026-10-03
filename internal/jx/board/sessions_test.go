package board

import (
	"strings"
	"testing"
	"time"
)

func TestParseClaudeSessions(t *testing.T) {
	long := strings.Repeat("x", 3100)
	out := strings.Join([]string{
		"\x1eF 1700000300 /home/exe/.claude/projects/-home-exe-work/0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10.jsonl",
		`{"parentUuid":null,"isSidechain":false,"userType":"external","cwd":"/home/exe/work","sessionId":"0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10","version":"2.0.14","gitBranch":"","type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"},"isMeta":true}`,
		`{"parentUuid":"a","cwd":"/home/exe/work","type":"user","message":{"role":"user","content":"fix the \"login\" bug\nin auth.go"},"entrypoint":"cli"}`,
		`"entrypoint":"cli"`,
		`{"type":"ai-title","aiTitle":"Fix login bug","sessionId":"0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10"}`,
		`{"type":"custom-title","customTitle":"Auth fixes","sessionId":"0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10"}`,
		"\x1eF 1700000200 /home/exe/.claude/projects/-home-exe-app/5d3e8f20-1a2b-4c3d-9e8f-001122334455.jsonl",
		`{"cwd":"/home/exe/app","type":"user","message":{"role":"user","content":[{"type":"text","text":"` + long[:2900], // clipped mid-value
		"\x1eF 1700000100 /home/exe/.claude/projects/-home-exe-work/7c0d5e1a-2b3c-4d5e-8f90-a1b2c3d4e5f6.jsonl",
		`{"cwd":"/home/exe/work","type":"user","message":{"role":"user","content":"headless run"}}`,
		`"entrypoint":"sdk-cli"`,
		"\x1eF 1700000050 /home/exe/.claude/projects/-home-exe-work/notes.jsonl",
		"",
	}, "\n")
	list := ParseSessions(Claude, out)
	if len(list) != 4 || list[3].Title != "" { // no prompt yet: MarkThreads drops it
		t.Fatalf("sessions = %+v", list)
	}
	if s := list[0]; s.ID != "0b6c2f1e-6b1a-4c55-9a43-2f1d2b7f8e10" || s.Title != "Auth fixes" || s.Cwd != "/home/exe/work" || s.Headless || !s.UpdatedAt.Equal(time.Unix(1700000300, 0)) {
		t.Errorf("first = %+v", s)
	}
	if s := list[1]; s.Cwd != "/home/exe/app" || !strings.HasPrefix(s.Title, "xxxx") || !strings.HasSuffix(s.Title, "…") {
		t.Errorf("clipped = %+v", s)
	}
	if !list[2].Headless {
		t.Errorf("sdk session = %+v", list[2])
	}

	// the Board's own headless session is listed with its thread; others not
	marked := MarkThreads(list, []Thread{{ID: "t1", Target: "dev", Agent: Claude, SessionID: "7c0d5e1a-2b3c-4d5e-8f90-a1b2c3d4e5f6"}}, "dev", Claude)
	if len(marked) != 3 || marked[2].ThreadID != "t1" {
		t.Errorf("marked = %+v", marked)
	}
	if marked := MarkThreads(list, nil, "dev", Claude); len(marked) != 2 {
		t.Errorf("unmarked = %+v", marked)
	}
}

func TestParseCodexSessions(t *testing.T) {
	out := strings.Join([]string{
		"\x1eF 1700000300 /home/exe/.codex/sessions/2025/10/01/rollout-2025-10-01T10-00-00-0199a213-81c0-7800-8aa1-bbab2a035a53.jsonl",
		`"id":"0199a213-81c0-7800-8aa1-bbab2a035a53"`,
		`"cwd":"/home/exe/work"`,
		`"originator":"codex_cli_rs"`,
		`{"timestamp":"2025-10-01T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"add a NOTES.md","images":[]}}`,
		"\x1eF 1700000200 /home/exe/.codex/sessions/2025/10/01/rollout-2025-10-01T09-00-00-0199a2c4-1f00-7d11-9a2b-3c4d5e6f7a8b.jsonl",
		`"id":"0199a2c4-1f00-7d11-9a2b-3c4d5e6f7a8b"`,
		`"cwd":"/home/exe/work"`,
		`"originator":"codex_exec"`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"scripted run"}}`,
		"\x1eF 1700000100 /home/exe/.codex/sessions/2025/10/01/rollout-2025-10-01T08-00-00-0199a000-0000-7000-8000-000000000001.jsonl",
		`"id":"0199a000-0000-7000-8000-000000000001"`,
		`"parent_thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"`,
		"\x1eF 1700000090 /home/exe/.codex/sessions/2025/10/01/rollout-2025-10-01T07-00-00-0199a000-0000-7000-8000-000000000002.jsonl",
		`"id":"0199a000-0000-7000-8000-000000000002"`,
		`"subagent":"1"`,
		"\x1eI",
		`{"id":"0199a213-81c0-7800-8aa1-bbab2a035a53","thread_name":"Notes file","updated_at":"2025-10-01T10:05:00Z"}`,
	}, "\n")
	list := ParseSessions(Codex, out)
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	if s := list[0]; s.ID != "0199a213-81c0-7800-8aa1-bbab2a035a53" || s.Title != "Notes file" || s.Cwd != "/home/exe/work" || s.Headless {
		t.Errorf("first = %+v", s)
	}
	if s := list[1]; !s.Headless || s.Title != "scripted run" {
		t.Errorf("exec = %+v", s)
	}
}

func TestJSONFieldClipped(t *testing.T) {
	for line, want := range map[string]string{
		`{"a":"x\"y"}`:    `x"y`,
		`{"a":"tab\tz`:    "tab\tz",
		`{"a":"cut\u00`:   "cut",
		`{"a":"cut here\`: "cut here",
		`{"b":"nope"}`:    "",
	} {
		if got := jsonField(line, "a"); got != want {
			t.Errorf("jsonField(%q) = %q, want %q", line, got, want)
		}
	}
}
