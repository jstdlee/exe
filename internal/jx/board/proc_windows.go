//go:build windows

package board

import "os/exec"

func setGroup(cmd *exec.Cmd) {}

// signalGroup kills the CLI: Windows has no process groups to signal, and
// no TERM to ask nicely with.
func signalGroup(cmd *exec.Cmd, kill bool) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
