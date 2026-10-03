//go:build linux || darwin

package board

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeCLI writes an executable shell script standing in for an agent CLI.
func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunHostStreamsAndPassesPromptOnStdin(t *testing.T) {
	fixture, _ := filepath.Abs(filepath.Join("testdata", "claude_tool.jsonl"))
	dir := t.TempDir()
	bin := fakeCLI(t, `cat > prompt.txt; echo "$@" > args.txt; cat `+shq(fixture)+`; echo "a warning" >&2`)
	p := NewParser(Claude)
	var evs []Event
	err := RunHost(context.Background(), bin, CLIArgs(Claude, "", false, true), dir, os.Environ(), "hello\nworld", func(b []byte) {
		evs = append(evs, p.Feed(b)...)
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "prompt.txt")); string(b) != "hello\nworld" {
		t.Errorf("prompt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "args.txt")); !strings.Contains(string(b), "--permission-mode acceptEdits") {
		t.Errorf("args = %q", b)
	}
	if len(evs) != 8 || p.Result().SessionID == "" {
		t.Errorf("events %d, result %+v", len(evs), p.Result())
	}

	bad := fakeCLI(t, `echo "Error: no such session" >&2; exit 2`)
	err = RunHost(context.Background(), bad, nil, dir, os.Environ(), "", func([]byte) {})
	if err == nil || !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "no such session") {
		t.Errorf("failed CLI = %v", err)
	}
}

func TestRunHostStopKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	bin := fakeCLI(t, `sleep 300 & echo $! > child.pid; wait`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunHost(ctx, bin, nil, dir, os.Environ(), "", func([]byte) {}) }()
	var pid int
	for i := 0; i < 200 && pid == 0; i++ {
		b, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
		pid = atoi(strings.TrimSpace(string(b)))
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake CLI never started its child")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not end the run")
	}
	for i := 0; i < 300; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("the CLI's child survived the stop")
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
