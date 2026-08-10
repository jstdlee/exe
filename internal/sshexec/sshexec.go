// Package sshexec runs commands and transfers files over SSH.
package sshexec

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DialFunc opens the TCP connection to a target, overriding net.Dial for
// backends whose guest network lives inside the daemon process.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type Target struct {
	Host    string
	User    string
	KeyPath string
	// Dialer, when set, replaces the default TCP dial to Host:22.
	Dialer DialFunc
}

func (t Target) dial(ctx context.Context) (*ssh.Client, error) {
	key, err := os.ReadFile(t.KeyPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	dialTCP := t.Dialer
	if dialTCP == nil {
		d := net.Dialer{Timeout: 10 * time.Second}
		dialTCP = d.DialContext
	}
	conn, err := dialTCP(ctx, "tcp", net.JoinHostPort(t.Host, "22"))
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, t.Host, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// Dial opens an SSH client connection to the target; the caller closes it.
func (t Target) Dial(ctx context.Context) (*ssh.Client, error) { return t.dial(ctx) }

// Run executes a command via the remote user's shell and returns combined
// output (truncated to maxOut bytes) plus the exit code.
func (t Target) Run(ctx context.Context, command string, maxOut int) (string, int, error) {
	client, err := t.dial(ctx)
	if err != nil {
		return "", -1, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
			client.Close()
		case <-done:
		}
	}()
	out, err := sess.CombinedOutput(command)
	close(done)
	text := Truncate(string(out), maxOut)
	if err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			return text, ee.ExitStatus(), nil
		}
		if ctx.Err() != nil {
			return text, -1, fmt.Errorf("command canceled or timed out: %w", ctx.Err())
		}
		return text, -1, err
	}
	return text, 0, nil
}

func (t Target) WriteFile(ctx context.Context, p string, data []byte) error {
	client, err := t.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.Stdin = bytes.NewReader(data)
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s", shq(path.Dir(p)), shq(p))
	if out, err := sess.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("write %s: %v: %s", p, err, out)
	}
	return nil
}

func (t Target) ReadFile(ctx context.Context, p string, maxOut int) (string, error) {
	out, code, err := t.Run(ctx, "cat "+shq(p), maxOut)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("cat exited %d: %s", code, out)
	}
	return out, nil
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Truncate keeps the head and tail of s when it exceeds max bytes.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	half := max / 2
	return s[:half] + fmt.Sprintf("\n... [%d bytes truncated] ...\n", len(s)-max) + s[len(s)-half:]
}
