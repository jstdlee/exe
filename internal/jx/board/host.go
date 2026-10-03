package board

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// RunHost runs one turn's CLI on the host: bin with args in dir, the
// prompt on stdin, stdout lines to line. The CLI gets a process group of
// its own (Unix), so a stop ends it and everything it started; on Windows
// the CLI itself is killed.
func RunHost(ctx context.Context, bin string, args []string, dir string, env []string, prompt string, line func([]byte)) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env = dir, env
	stderr := NewTail(4096)
	lw := NewLineWriter(line)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(prompt), lw, stderr
	// a background process the CLI left holding its stdout must not keep
	// the turn open once the CLI has exited
	cmd.WaitDelay = 5 * time.Second
	setGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		signalGroup(cmd, false) // TERM: let the CLI save its session
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			signalGroup(cmd, true)
			<-done
		}
		lw.Flush()
		return ctx.Err()
	}
	lw.Flush()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // the CLI exited 0; only a straggler held the pipe
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitErr(&ExitError{Code: ee.ExitCode()}, stderr)
	}
	return err
}
