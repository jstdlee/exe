package hubbridge

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"exe/internal/jx/board"
)

// MaxText is the hub's cap on a post's text, in bytes.
const MaxText = 8 << 10

// Task is a new task parsed from a root post.
type Task struct {
	Agent  string // "claude" | "codex"
	Target string // VM name or "host"
	Prompt string
}

// directive is the optional header between the trigger and the colon:
// "[claude|codex] [on <target>]".
var directive = regexp.MustCompile(`(?i)^\s*(claude|codex)?\s*(?:\bon\s+([A-Za-z0-9][A-Za-z0-9._-]{0,62}))?\s*$`)

// ParseTask reads a root post's text. It is a task when the text starts
// with "@agent" or "@<agentName>" (any case), followed by the end, a
// space, ':' or ','. Directives come before a colon:
//
//	@agent codex on myvm: <task>
//	@agent claude on host: <task>
//	@agent on myvm: <task>
//	@agent: <task>
//	@agent <task>
//
// Missing parts take defAgent and defTarget. An empty task is no task.
func ParseTask(text, agentName, defAgent, defTarget string) (Task, bool) {
	rest, ok := cutTrigger(strings.TrimLeft(text, " \t\r\n"), agentName)
	if !ok {
		return Task{}, false
	}
	t := Task{Agent: defAgent, Target: defTarget}
	rest = strings.TrimLeft(rest, " \t,")
	head, tail, colon := strings.Cut(rest, ":")
	if colon && !strings.Contains(head, "\n") {
		if m := directive.FindStringSubmatch(head); m != nil {
			if m[1] != "" {
				t.Agent = strings.ToLower(m[1])
			}
			if m[2] != "" {
				t.Target = m[2]
				if strings.EqualFold(t.Target, board.HostTarget) {
					t.Target = board.HostTarget
				}
			}
			rest = tail
		}
	}
	t.Prompt = strings.TrimSpace(rest)
	return t, t.Prompt != ""
}

// cutTrigger strips a leading "@agent" or "@<name>" and returns the rest.
func cutTrigger(s, name string) (string, bool) {
	for _, trig := range []string{"agent", name} {
		if trig == "" {
			continue
		}
		n := 1 + len(trig)
		if len(s) < n || s[0] != '@' || !strings.EqualFold(s[1:n], trig) {
			continue
		}
		rest := s[n:]
		if rest == "" {
			return "", true
		}
		switch rest[0] {
		case ' ', '\t', '\n', '\r', ':', ',':
			return rest, true
		}
	}
	return "", false
}

// Command is a control reply in a bridged thread.
type Command int

const (
	CmdNone Command = iota
	CmdStop
	CmdStatus
)

// ParseCommand recognizes "/stop" and "/status" (the whole reply).
func ParseCommand(text string) Command {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/stop":
		return CmdStop
	case "/status":
		return CmdStatus
	}
	return CmdNone
}

// FinalText is a turn's final assistant text: the text events after the
// turn's last tool step, joined. A turn that only spoke gives all of it.
func FinalText(events []board.Event) string {
	var out []string
	for _, ev := range events {
		switch ev.Type {
		case board.EvTool, board.EvToolResult:
			out = out[:0]
		case board.EvText:
			if t := strings.TrimSpace(ev.Text); t != "" {
				out = append(out, t)
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// Clip fits text in the hub's limit; a clipped text ends with a pointer
// to the Board thread that holds all of it.
func Clip(text, threadID string) string {
	if len(text) <= MaxText {
		return text
	}
	suffix := "… (full output in Board thread " + threadID + ")"
	cut := MaxText - len(suffix) - 1
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimRight(text[:cut], " \t\r\n") + "\n" + suffix
}

// oneLine flattens s to one line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
