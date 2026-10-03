package board

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Session is an agent CLI session found in a target, for "Continue".
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
	Cwd       string    `json:"cwd"`
	ThreadID  string    `json:"thread_id,omitempty"`
	// Headless marks a session a headless run made (claude -p, codex
	// exec): the Board's own turns, scripts. Lists keep only those the
	// Board has a thread for.
	Headless bool `json:"-"`
}

// SessionsMax caps a session list.
const SessionsMax = 30

// sessionsScanned is how many of the latest files a scan reads: some are
// dropped (headless, empty, sub-agents), and the list still fills.
const sessionsScanned = 60

// SessionsScript prints compact metadata about agent's latest sessions in a
// guest — never whole transcripts: per file a mark line with its mtime and
// path, then a few clipped lines that hold its folder, first prompt and
// title (ParseSessions).
func SessionsScript(agent string) string {
	n := strconv.Itoa(sessionsScanned)
	if agent == Codex {
		return `d="$HOME/.codex/sessions"
[ -d "$d" ] || exit 0
find "$d" -type f -name 'rollout-*.jsonl' -exec stat -c '%Y %n' {} + 2>/dev/null | sort -rn | head -n ` + n + ` | while read -r m f; do
  printf '\036F %s %s\n' "$m" "$f"
  head -n1 "$f" | grep -oE '"(id|cwd|originator|parent_thread_id|thread_source)":"[^"]*"' | head -n 8
  head -n1 "$f" | grep -q '"source":{' && echo '"subagent":"1"'
  grep -m2 '"type":"user_message"' "$f" | cut -c1-3000
done
i="$HOME/.codex/session_index.jsonl"
if [ -f "$i" ]; then printf '\036I\n'; tail -n 400 "$i" | cut -c1-500; fi
exit 0
`
	}
	return `d="$HOME/.claude/projects"
[ -d "$d" ] || exit 0
find "$d" -mindepth 2 -maxdepth 2 -type f -name '*.jsonl' -exec stat -c '%Y %n' {} + 2>/dev/null | sort -rn | head -n ` + n + ` | while read -r m f; do
  printf '\036F %s %s\n' "$m" "$f"
  grep -m3 '"type":"user"' "$f" | cut -c1-3000
  grep -m1 -o '"entrypoint":"[^"]*"' "$f"
  grep -e '"type":"custom-title"' -e '"type":"ai-title"' "$f" | tail -n 4 | cut -c1-1000
done
exit 0
`
}

// ListSessions runs SessionsScript on rem and parses it.
func ListSessions(ctx context.Context, rem Remote, agent string) ([]Session, error) {
	var out bytes.Buffer
	stderr := NewTail(2048)
	if err := rem.Exec(ctx, SessionsScript(agent), nil, &out, stderr); err != nil {
		return nil, exitErr(err, stderr)
	}
	return ParseSessions(agent, out.String()), nil
}

// ParseSessions reads SessionsScript's output: sessions newest first,
// without sub-agents' threads (MarkThreads does the rest of the sifting).
func ParseSessions(agent, out string) []Session {
	type file struct {
		mtime int64
		path  string
		lines []string
	}
	var files []*file
	var index []string
	inIndex := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\x1eF "):
			inIndex = false
			m, p, _ := strings.Cut(line[3:], " ")
			mt, _ := strconv.ParseInt(m, 10, 64)
			files = append(files, &file{mtime: mt, path: p})
		case line == "\x1eI":
			inIndex = true
		case inIndex:
			index = append(index, line)
		case len(files) > 0:
			files[len(files)-1].lines = append(files[len(files)-1].lines, line)
		}
	}
	names := map[string]string{}
	for _, l := range index {
		if id := jsonField(l, "id"); id != "" {
			if n := jsonField(l, "thread_name"); n != "" {
				names[id] = n
			}
		}
	}
	var list []Session
	for _, f := range files {
		var s Session
		var ok bool
		if agent == Codex {
			s, ok = codexSession(f.path, f.lines, names)
		} else {
			s, ok = claudeSession(f.path, f.lines)
		}
		if !ok {
			continue
		}
		s.UpdatedAt = time.Unix(f.mtime, 0).UTC()
		list = append(list, s)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	return list
}

func claudeSession(p string, lines []string) (Session, bool) {
	s := Session{ID: strings.TrimSuffix(path.Base(p), ".jsonl")}
	if !ValidSessionID(s.ID) {
		return s, false
	}
	var prompt, ai, custom string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, `"entrypoint":"`):
			s.Headless = strings.HasPrefix(jsonField(l, "entrypoint"), "sdk")
		case strings.Contains(l, `"type":"custom-title"`):
			if t := jsonField(l, "customTitle"); t != "" {
				custom = t
			}
		case strings.Contains(l, `"type":"ai-title"`):
			if t := jsonField(l, "aiTitle"); t != "" {
				ai = t
			}
		case strings.Contains(l, `"type":"user"`):
			if s.Cwd == "" {
				s.Cwd = jsonField(l, "cwd")
			}
			if e := jsonField(l, "entrypoint"); e != "" {
				s.Headless = strings.HasPrefix(e, "sdk")
			}
			if prompt != "" || strings.Contains(l, `"isMeta":true`) || strings.Contains(l, `"tool_result"`) {
				continue
			}
			t := jsonField(l, "content")
			if t == "" {
				t = jsonField(l, "text")
			}
			if t = strings.TrimSpace(t); t != "" && !strings.HasPrefix(t, "<") {
				prompt = t
			}
		}
	}
	s.Title = custom
	if s.Title == "" {
		s.Title = ai
	}
	if s.Title == "" {
		s.Title = prompt
	}
	s.Title = oneLine(s.Title, 80)
	return s, true
}

func codexSession(p string, lines []string, names map[string]string) (Session, bool) {
	var s Session
	var originator, prompt string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, `"id":"`) && s.ID == "":
			s.ID = jsonField(l, "id")
		case strings.HasPrefix(l, `"cwd":"`) && s.Cwd == "":
			s.Cwd = jsonField(l, "cwd")
		case strings.HasPrefix(l, `"originator":"`):
			originator = jsonField(l, "originator")
		case strings.HasPrefix(l, `"parent_thread_id":"`) && jsonField(l, "parent_thread_id") != "",
			strings.HasPrefix(l, `"subagent":"`):
			return s, false // a sub-agent's thread belongs to its parent
		case strings.HasPrefix(l, `"thread_source":"`):
			if v := jsonField(l, "thread_source"); v != "" && v != "user" {
				return s, false
			}
		case strings.Contains(l, `"user_message"`) && prompt == "":
			if t := strings.TrimSpace(jsonField(l, "message")); t != "" && !strings.HasPrefix(t, "<") {
				prompt = t
			}
		}
	}
	if s.ID == "" {
		// an old rollout without the id in its first line: the file name
		// ends with it (rollout-<time>-<uuid>.jsonl)
		base := strings.TrimSuffix(path.Base(p), ".jsonl")
		if len(base) > 36 {
			s.ID = base[len(base)-36:]
		}
	}
	if !ValidSessionID(s.ID) {
		return s, false
	}
	s.Headless = strings.Contains(originator, "exec")
	s.Title = names[s.ID]
	if s.Title == "" {
		s.Title = prompt
	}
	s.Title = oneLine(s.Title, 80)
	return s, true
}

// jsonField is the string value of "key":"…" in a JSON line, decoded —
// the first occurrence, and tolerant of a line clipped mid-value (what
// the scripts' cut leaves), where it returns what survived the cut.
func jsonField(line, key string) string {
	pat := `"` + key + `":"`
	i := strings.Index(line, pat)
	if i < 0 {
		return ""
	}
	s := line[i+len(pat):]
	j := 0
	for j < len(s) && s[j] != '"' {
		if s[j] == '\\' {
			j++
		}
		j++
	}
	raw := s[:min(j, len(s))]
	var out string
	if json.Unmarshal([]byte(`"`+raw+`"`), &out) == nil {
		return out
	}
	// clipped inside an escape: drop back before it
	if k := strings.LastIndexByte(raw, '\\'); k >= 0 && json.Unmarshal([]byte(`"`+raw[:k]+`"`), &out) == nil {
		return out
	}
	return ""
}

// MarkThreads fills ThreadID for sessions a thread holds, naming an
// untitled one after its thread, and drops what a person would not pick:
// headless sessions no thread holds (scripts, other tools) and sessions
// with no prompt yet. At most SessionsMax remain.
func MarkThreads(list []Session, threads []Thread, target, agent string) []Session {
	held := map[string]Thread{}
	for _, t := range threads {
		if t.Target == target && t.Agent == agent && t.SessionID != "" {
			if _, ok := held[t.SessionID]; !ok {
				held[t.SessionID] = t // threads come newest first: the newest wins
			}
		}
	}
	out := []Session{}
	for _, s := range list {
		t, ok := held[s.ID]
		if ok {
			s.ThreadID = t.ID
			if s.Title == "" {
				s.Title = t.Title
			}
		}
		if (s.Headless && !ok) || s.Title == "" {
			continue
		}
		out = append(out, s)
		if len(out) == SessionsMax {
			break
		}
	}
	return out
}
