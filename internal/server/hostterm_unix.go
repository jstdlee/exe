//go:build !windows

package server

import (
	"os"
	"os/exec"
	"runtime"

	"github.com/creack/pty"
)

type unixShell struct {
	f   *os.File
	cmd *exec.Cmd
}

func (s *unixShell) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *unixShell) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *unixShell) Resize(cols, rows int) {
	pty.Setsize(s.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *unixShell) Close() error {
	err := s.f.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()
	return err
}

// startHostShell starts the user's login shell on a pty.
func startHostShell(cols, rows int) (hostShell, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		} else {
			shell = "/bin/sh"
		}
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixShell{f: f, cmd: cmd}, nil
}
