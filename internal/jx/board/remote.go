package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"exe/internal/sshexec"
)

// Remote is a target's shell: Exec runs a POSIX sh script there with stdin
// fed and stdout/stderr streamed. A non-zero exit is an *ExitError.
// Cancelling ctx kills the script and every process it started.
type Remote interface {
	Exec(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error
}

// ExitError is a script's non-zero exit.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// SSHRemote is a VM's shell over SSH.
type SSHRemote struct{ Target sshexec.Target }

// pgidMark opens the stdout of every script SSHRemote runs: the script's
// process group, so a stop can kill the whole tree (the CLI and the
// shells and builds it started) from a second session. sshd makes each
// session's command a session leader, so the group is the script's own;
// it is read from /proc rather than assumed, in case a guest's sshd does
// not.
const pgidMark = "\x1eexe-pgid "

const pgidPreamble = `printf '\036exe-pgid %s\n' "$(cut -d' ' -f5 /proc/$$/stat 2>/dev/null || echo $$)"` + "\n"

// Exec runs script under sh in the guest. The command line carries only
// the script; secrets and prompts travel on stdin.
func (r SSHRemote) Exec(ctx context.Context, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	client, err := r.Target.Dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	sniff := &pgidSniffer{w: stdout}
	sess.Stdin, sess.Stdout, sess.Stderr = stdin, sniff, stderr
	// exec: the login shell becomes sh, keeping its pid and group, and the
	// script is plain sh whatever the guest user's shell is
	if err := sess.Start("exec sh -c " + sshexec.Quote(pgidPreamble+script)); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err := <-done:
		return sshExit(err)
	case <-ctx.Done():
	}
	sess.Signal(ssh.SIGTERM) // honoured by recent OpenSSH; the kill below is what counts
	if pg := sniff.PGID(); pg > 1 {
		if k, err := client.NewSession(); err == nil {
			// TERM first so the CLI can save its session; KILL whatever is
			// left a few seconds later, detached so this session returns
			k.Run(fmt.Sprintf("kill -TERM -%d 2>/dev/null; (sleep 4; kill -KILL -%d 2>/dev/null) >/dev/null 2>&1 </dev/null & true", pg, pg))
			k.Close()
		}
	}
	select {
	case <-done:
	case <-time.After(8 * time.Second):
	}
	return ctx.Err()
}

func sshExit(err error) error {
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitStatus()}
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) {
		return errors.New("the session ended without an exit status (connection lost?)")
	}
	return err
}

// pgidSniffer passes stdout through, minus its first line when that is the
// pgid mark, which it keeps.
type pgidSniffer struct {
	w    io.Writer
	mu   sync.Mutex
	head []byte
	done bool
	pgid int
}

func (p *pgidSniffer) Write(b []byte) (int, error) {
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return p.w.Write(b)
	}
	n := len(b)
	p.head = append(p.head, b...)
	i := bytes.IndexByte(p.head, '\n')
	if i < 0 && len(p.head) < 64 && bytes.HasPrefix([]byte(pgidMark), p.head[:min(len(p.head), len(pgidMark))]) {
		p.mu.Unlock()
		return n, nil // still could be the mark: wait for the rest of the line
	}
	p.done = true
	rest := p.head
	p.head = nil
	if i >= 0 && bytes.HasPrefix(rest, []byte(pgidMark)) {
		p.pgid, _ = strconv.Atoi(strings.TrimSpace(string(rest[len(pgidMark):i])))
		rest = rest[i+1:]
	}
	p.mu.Unlock()
	if len(rest) > 0 {
		if _, err := p.w.Write(rest); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// PGID is the remote process group, 0 until the mark arrived.
func (p *pgidSniffer) PGID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pgid
}

// LineWriter is an io.Writer that hands each complete line to fn (without
// its newline); Flush hands over a last unterminated one. Lines longer
// than 32 MB are cut there (a CLI's JSON lines can carry whole files).
type LineWriter struct {
	fn  func([]byte)
	buf []byte
}

func NewLineWriter(fn func([]byte)) *LineWriter { return &LineWriter{fn: fn} }

const lineMax = 32 << 20

func (l *LineWriter) Write(b []byte) (int, error) {
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.fn(bytes.TrimRight(l.buf[:i], "\r"))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > lineMax {
		l.fn(l.buf)
		l.buf = nil
	}
	if len(l.buf) == 0 {
		l.buf = nil // let a big line's backing array go
	}
	return len(b), nil
}

func (l *LineWriter) Flush() {
	if len(l.buf) > 0 {
		l.fn(l.buf)
		l.buf = nil
	}
}

// Tail keeps the last n bytes written to it: a CLI's stderr, for the error
// a failed turn reports.
type Tail struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func NewTail(n int) *Tail { return &Tail{n: n} }

func (t *Tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.n {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.n:]...)
	}
	t.mu.Unlock()
	return len(b), nil
}

func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(stripANSI(string(t.buf)))
}

// exitErr words a CLI's failed exit with the tail of its stderr.
func exitErr(err error, stderr *Tail) error {
	if err == nil {
		return nil
	}
	if s := stderr.String(); s != "" {
		lines := strings.Split(s, "\n")
		if len(lines) > 6 {
			lines = lines[len(lines)-6:]
		}
		return fmt.Errorf("%v: %s", err, strings.Join(lines, "\n"))
	}
	return err
}
