package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Parser turns one CLI's JSONL stdout into Events. Feed takes one line at a
// time and returns the events it completes (without seq, turn or time:
// the engine stamps those); Result is what the stream said about the turn
// as a whole once it ended.
type Parser interface {
	Feed(line []byte) []Event
	Result() Result
}

// Result is a turn's outcome as the CLI reported it.
type Result struct {
	SessionID string // the session (Claude) or thread (Codex) the turn ran in
	Done      bool   // the CLI reported the turn's end (result / turn.completed)
	Error     string // the CLI's own error report, "" for success
	Usage     *Usage
	LastText  string // the last assistant text
}

// NewParser returns the stream parser for agent.
func NewParser(agent string) Parser {
	if agent == Codex {
		return &codexParser{started: map[string]bool{}}
	}
	return &claudeParser{}
}

// Output and summary caps: tool output is for a glance (the CLI's own
// transcript keeps it all), a summary is one line.
const (
	outputMax  = 4 << 10
	summaryMax = 160
)

// clipBytes cuts s to at most n bytes on a rune boundary, marking the cut.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + fmt.Sprintf("\n… [%d bytes clipped]", len(s)-i)
}

// oneLine collapses s to its first non-empty line, at most n runes.
func oneLine(s string, n int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			s = l
			break
		}
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

var errorish = regexp.MustCompile(`(?i)\b(error|fatal|failed|denied|not found|panic|unauthori[sz]ed|forbidden|invalid|cannot|refused)\b`)

// nonJSON handles a stdout line that is not an event: the CLI printing a
// complaint before its stream starts (a bad flag, a missing login). Only
// lines that read like an error become status events; chatter is dropped.
func nonJSON(line []byte) []Event {
	s := strings.TrimSpace(stripANSI(string(line)))
	if s == "" || !errorish.MatchString(s) {
		return nil
	}
	return []Event{{Type: EvStatus, Text: oneLine(s, 300)}}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07|\r`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// ---- Claude Code: claude -p --output-format stream-json --verbose -------------

type claudeParser struct{ res Result }

func (p *claudeParser) Result() Result { return p.res }

type claudeLine struct {
	Type         string          `json:"type"`
	Subtype      string          `json:"subtype"`
	SessionID    string          `json:"session_id"`
	Message      json.RawMessage `json:"message"`
	IsError      bool            `json:"is_error"`
	Result       string          `json:"result"`
	Errors       []string        `json:"errors"`
	Usage        *claudeUsage    `json:"usage"`
	TotalCostUSD *float64        `json:"total_cost_usd"`
	Model        string          `json:"model"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (p *claudeParser) Feed(raw []byte) []Event {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var l claudeLine
	if raw[0] != '{' || json.Unmarshal(raw, &l) != nil {
		return nonJSON(raw)
	}
	if l.SessionID != "" {
		p.res.SessionID = l.SessionID
	}
	switch l.Type {
	case "system":
		if l.Subtype == "compact_boundary" {
			return []Event{{Type: EvStatus, Text: "context compacted"}}
		}
	case "assistant":
		var m struct {
			Content []claudeBlock `json:"content"`
		}
		if json.Unmarshal(l.Message, &m) != nil {
			return nil
		}
		var out []Event
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				p.res.LastText = b.Text
				out = append(out, Event{Type: EvText, Text: b.Text})
			case "tool_use", "server_tool_use":
				out = append(out, Event{Type: EvTool, ID: b.ID, Name: b.Name, Summary: toolSummary(b.Name, b.Input)})
			}
		}
		return out
	case "user":
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(l.Message, &m) != nil {
			return nil
		}
		var blocks []claudeBlock
		if json.Unmarshal(m.Content, &blocks) != nil {
			return nil // a plain-string user message: the prompt echoed back
		}
		var out []Event
		for _, b := range blocks {
			if b.Type == "tool_result" {
				out = append(out, Event{Type: EvToolResult, ID: b.ToolUseID,
					Output: clipBytes(blockText(b.Content), outputMax), IsError: b.IsError})
			}
		}
		return out
	case "result":
		p.res.Done = true
		if l.Usage != nil {
			u := &Usage{
				InputTokens:       l.Usage.InputTokens + l.Usage.CacheCreationInputTokens + l.Usage.CacheReadInputTokens,
				CachedInputTokens: l.Usage.CacheReadInputTokens,
				OutputTokens:      l.Usage.OutputTokens,
				CostUSD:           l.TotalCostUSD,
			}
			p.res.Usage = u
		}
		if l.IsError || (l.Subtype != "" && l.Subtype != "success") {
			msg := strings.TrimSpace(l.Result)
			if msg == "" && len(l.Errors) > 0 {
				msg = strings.Join(l.Errors, "; ")
			}
			if msg == "" {
				msg = strings.ReplaceAll(l.Subtype, "_", " ")
			}
			if msg == "" {
				msg = "the turn failed"
			}
			p.res.Error = msg
		}
	}
	return nil
}

// blockText is a tool_result's content as text: a string, or the text
// blocks of a list (images and other blocks are named, not shown).
func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else if b.Type != "" {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// toolSummary is one line naming what a Claude Code tool call does:
// "Bash: npm test", "Edit: src/main.go".
func toolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	var arg string
	for _, k := range []string{"command", "file_path", "notebook_path", "path", "pattern", "url", "query", "description", "prompt", "skill"} {
		if arg = str(k); arg != "" {
			break
		}
	}
	if arg == "" {
		if todos, ok := in["todos"].([]any); ok {
			arg = fmt.Sprintf("%d items", len(todos))
		}
	}
	if arg == "" {
		// any string argument says more than none
		keys := make([]string, 0, len(in))
		for k := range in {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if arg = str(k); arg != "" {
				break
			}
		}
	}
	if arg == "" {
		return oneLine(name, summaryMax)
	}
	return oneLine(name+": "+arg, summaryMax)
}

// ---- Codex: codex exec --json ------------------------------------------------

type codexParser struct {
	res     Result
	started map[string]bool // items whose tool event went out at item.started
	lastErr string          // the last top-level error, kept for a failure with no message
}

func (p *codexParser) Result() Result {
	r := p.res
	if !r.Done && r.Error == "" && p.lastErr != "" {
		r.Error = p.lastErr
	}
	return r
}

type codexLine struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Message  string     `json:"message"`
	Item     *codexItem `json:"item"`
	Usage    *struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
	Query     string          `json:"query"`
	Message   string          `json:"message"`
	Items     []struct {
		Text      string `json:"text"`
		Completed bool   `json:"completed"`
	} `json:"items"`
}

func (p *codexParser) Feed(raw []byte) []Event {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var l codexLine
	if raw[0] != '{' || json.Unmarshal(raw, &l) != nil {
		return nonJSON(raw)
	}
	switch l.Type {
	case "thread.started":
		if l.ThreadID != "" {
			p.res.SessionID = l.ThreadID
		}
	case "turn.completed":
		p.res.Done = true
		if l.Usage != nil {
			p.res.Usage = &Usage{InputTokens: l.Usage.InputTokens, CachedInputTokens: l.Usage.CachedInputTokens, OutputTokens: l.Usage.OutputTokens}
		}
	case "turn.failed":
		p.res.Done = true
		msg := ""
		if l.Error != nil {
			msg = strings.TrimSpace(l.Error.Message)
		}
		if msg == "" {
			msg = p.lastErr
		}
		if msg == "" {
			msg = "the turn failed"
		}
		p.res.Error = msg
	case "error":
		// Also sent for retries the CLI recovers from ("Reconnecting…"):
		// a status line, and the failure's reason if turn.failed says none.
		if msg := strings.TrimSpace(l.Message); msg != "" {
			p.lastErr = msg
			return []Event{{Type: EvStatus, Text: oneLine(msg, 300)}}
		}
	case "item.started", "item.updated", "item.completed":
		if l.Item != nil {
			return p.item(l.Type, l.Item)
		}
	}
	return nil
}

func (p *codexParser) item(kind string, it *codexItem) []Event {
	done := kind == "item.completed"
	var out []Event
	tool := func(name, summary string) {
		if !p.started[it.ID] {
			p.started[it.ID] = true
			out = append(out, Event{Type: EvTool, ID: it.ID, Name: name, Summary: oneLine(summary, summaryMax)})
		}
	}
	result := func(output string, isErr bool) {
		if done {
			out = append(out, Event{Type: EvToolResult, ID: it.ID, Output: clipBytes(output, outputMax), IsError: isErr})
		}
	}
	failed := it.Status == "failed" || it.Status == "declined"
	switch it.Type {
	case "agent_message":
		if done && strings.TrimSpace(it.Text) != "" {
			p.res.LastText = it.Text
			out = append(out, Event{Type: EvText, Text: it.Text})
		}
	case "reasoning":
		// the model's private notes: not part of the conversation
	case "command_execution":
		tool("shell", "shell: "+it.Command)
		isErr := failed || (it.ExitCode != nil && *it.ExitCode != 0)
		output := it.AggregatedOutput
		if it.ExitCode != nil && *it.ExitCode != 0 {
			output = strings.TrimRight(output, "\n") + fmt.Sprintf("\n(exit %d)", *it.ExitCode)
		}
		result(output, isErr)
	case "file_change":
		var parts []string
		for _, c := range it.Changes {
			parts = append(parts, c.Kind+" "+c.Path)
		}
		tool("edit", "edit: "+strings.Join(parts, ", "))
		result(strings.Join(parts, "\n"), failed)
	case "mcp_tool_call":
		name := it.Server + "." + it.Tool
		args := strings.TrimSpace(string(it.Arguments))
		if args == "null" {
			args = ""
		}
		tool(name, name+": "+args)
		output, isErr := mcpText(it.Result), failed
		if e := strings.TrimSpace(string(it.Error)); e != "" && e != "null" {
			var em struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(it.Error, &em) == nil && em.Message != "" {
				e = em.Message
			}
			output, isErr = e, true
		}
		result(output, isErr)
	case "web_search":
		tool("web_search", "web_search: "+it.Query)
		result(it.Query, failed)
	case "todo_list":
		var lines []string
		for _, t := range it.Items {
			box := "[ ]"
			if t.Completed {
				box = "[x]"
			}
			lines = append(lines, box+" "+t.Text)
		}
		tool("todo", fmt.Sprintf("todo: %d items", len(it.Items)))
		result(strings.Join(lines, "\n"), false)
	case "error":
		if done {
			out = append(out, Event{Type: EvStatus, Text: oneLine(it.Message, 300)})
		}
	}
	return out
}

// mcpText is an MCP result ({content: [...]}) as text: its text blocks, or
// the raw JSON when it has none.
func mcpText(raw json.RawMessage) string {
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return ""
	}
	var r struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &r) == nil && len(r.Content) > 0 {
		if t := blockText(r.Content); t != "" {
			return t
		}
	}
	return string(raw)
}
