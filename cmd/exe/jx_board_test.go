package main

import (
	"bytes"
	"strings"
	"testing"

	"exe/internal/jx/board"
)

func TestPrintEvent(t *testing.T) {
	cost := 0.0421
	var b bytes.Buffer
	for _, ev := range []board.Event{
		{Type: board.EvTurnStart, Prompt: "run the tests\nplease"},
		{Type: board.EvStatus, Text: "starting dev"},
		{Type: board.EvTool, Summary: "Bash: npm test"},
		{Type: board.EvToolResult, Output: "1\n2\n3\n4\n5\n6"},
		{Type: board.EvToolResult, Output: "no such file", IsError: true},
		{Type: board.EvText, Text: "All pass."},
		{Type: board.EvTurnEnd, State: "done", Usage: &board.Usage{InputTokens: 10, OutputTokens: 2, CostUSD: &cost}},
		{Type: board.EvTurnEnd, State: "error", Error: "boom"},
	} {
		printEvent(&b, ev)
	}
	want := strings.Join([]string{
		"",
		"> run the tests",
		"> please",
		"  [starting dev]",
		"  · Bash: npm test",
		"    1",
		"    2",
		"    3",
		"    4",
		"    … (2 more lines)",
		"  ! no such file",
		"",
		"All pass.",
		"-- done (in 10, out 2, $0.0421)",
		"-- error: boom",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
