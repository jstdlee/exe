//go:build linux

package board

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"exe/internal/keys"
	"exe/internal/sshexec"
)

// sshShell serves SSH exec requests by running them with the local sh in a
// session of their own, as sshd does: a real shell behind a real SSH
// connection, so SSHRemote's process-group stop is exercised end to end.
func sshShell(t *testing.T) sshexec.Target {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc and setsid")
	}
	keyPath, _, err := keys.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(keyPath)
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range creqs {
							if req.Type != "exec" {
								req.Reply(false, nil)
								continue
							}
							var p struct{ Command string }
							ssh.Unmarshal(req.Payload, &p)
							req.Reply(true, nil)
							go func() {
								cmd := exec.Command("sh", "-c", p.Command)
								cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
								cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
								cmd.WaitDelay = time.Second
								code := 0
								if err := cmd.Run(); err != nil {
									code = 255
									var ee *exec.ExitError
									if errors.As(err, &ee) && ee.ExitCode() >= 0 {
										code = ee.ExitCode()
									}
								}
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
								ch.Close()
							}()
						}
					}()
				}
			}()
		}
	}()
	addr := ln.Addr().String()
	return sshexec.Target{Host: "guest", User: "exe", KeyPath: keyPath,
		Dialer: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}}
}

func TestSSHRemoteStdinStdoutExit(t *testing.T) {
	rem := SSHRemote{Target: sshShell(t)}
	var out, errb bytes.Buffer
	err := rem.Exec(context.Background(), `read x; echo "got $x"; echo oops >&2`, strings.NewReader("secret-on-stdin\n"), &out, &errb)
	if err != nil || out.String() != "got secret-on-stdin\n" || errb.String() != "oops\n" {
		t.Fatalf("out %q err %q: %v", out.String(), errb.String(), err)
	}
	err = rem.Exec(context.Background(), "exit 3", nil, nil, nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Errorf("exit = %v", err)
	}
}

// Cancelling a remote turn kills its whole process group: the CLI and
// what it started, not only the session's shell.
func TestSSHRemoteCancelKillsProcessGroup(t *testing.T) {
	rem := SSHRemote{Target: sshShell(t)}
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- rem.Exec(ctx, `sleep 300 & echo $! > `+shq(pidFile)+`; echo started; wait`, nil, io.Discard, io.Discard)
	}()
	var pid int
	for i := 0; i < 200 && pid == 0; i++ {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("remote job never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("exec = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not end the exec")
	}
	for i := 0; i < 300; i++ {
		if !alive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("the remote job's child survived the stop")
}

// alive reports whether pid runs (a zombie has ended).
func alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}
