package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"exe/internal/sshexec"
)

// ChatStreamOpenAI talks to an OpenAI-compatible /v1/chat/completions
// endpoint (xAI, Anthropic's compat layer, ollama.com/v1, custom bridges).
func ChatStreamOpenAI(ctx context.Context, cfg Config, msgs []Message, tools []Tool, onDelta func(string)) (*Message, error) {
	type oaiMsg struct {
		Role       string `json:"role"`
		Content    any    `json:"content,omitempty"`
		Name       string `json:"name,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		ToolCalls  []any  `json:"tool_calls,omitempty"`
	}
	var out []oaiMsg
	for _, m := range msgs {
		om := oaiMsg{Role: m.Role, Content: m.Content}
		switch m.Role {
		case "tool":
			om.ToolCallID = m.ToolCallID
			om.Name = m.ToolName
			if om.ToolCallID == "" {
				om.Role = "user"
				om.Content = m.Content
			}
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var tcs []any
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Function.Name,
							"arguments": string(tc.Function.Arguments),
						},
					})
				}
				om.ToolCalls = tcs
			}
		}
		out = append(out, om)
	}
	body := map[string]any{
		"model":    cfg.Model,
		"messages": out,
		"stream":   true,
	}
	if len(tools) > 0 {
		var ots []any
		for _, t := range tools {
			ots = append(ots, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Function.Name,
					"description": t.Function.Description,
					"parameters":  t.Function.Parameters,
				},
			})
		}
		body["tools"] = ots
	}
	if cfg.Temperature > 0 {
		body["temperature"] = cfg.Temperature
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	for k, v := range cfg.ExtraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("openai %s: HTTP %d: %s", cfg.Model, resp.StatusCode, sshexec.Truncate(string(errBody), 2000))
	}

	full := Message{Role: "assistant"}
	type acc struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*acc{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return nil, fmt.Errorf("openai: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			full.Content += d.Content
			if onDelta != nil {
				onDelta(d.Content)
			}
		}
		for _, tc := range d.ToolCalls {
			a := calls[tc.Index]
			if a == nil {
				a = &acc{}
				calls[tc.Index] = a
			}
			if tc.ID != "" {
				a.id = tc.ID
			}
			if tc.Function.Name != "" {
				a.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				a.args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("stream openai response: %w", err)
	}
	if len(calls) > 0 {
		idxs := make([]int, 0, len(calls))
		for i := range calls {
			idxs = append(idxs, i)
		}
		// insertion order is not guaranteed; sort by index
		for i := 0; i < len(idxs); i++ {
			for j := i + 1; j < len(idxs); j++ {
				if idxs[j] < idxs[i] {
					idxs[i], idxs[j] = idxs[j], idxs[i]
				}
			}
		}
		for _, i := range idxs {
			a := calls[i]
			var tc ToolCall
			tc.ID = a.id
			tc.Function.Name = a.name
			tc.Function.Arguments = json.RawMessage(a.args.String())
			if !json.Valid(tc.Function.Arguments) {
				tc.Function.Arguments = json.RawMessage([]byte("{}"))
			}
			full.ToolCalls = append(full.ToolCalls, tc)
		}
	}
	return &full, nil
}
