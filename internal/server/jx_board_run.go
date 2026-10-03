package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"exe/internal/jx/board"
	"exe/internal/vmm"
)

// boardRunner runs Board turns on real targets: a VM over SSH (started if
// stopped, the CLI installed and the credentials written on demand), or
// the host's own CLIs.
type boardRunner struct{ b *jxBoard }

func (r *boardRunner) Run(ctx context.Context, spec board.Spec, status func(string), line func([]byte)) error {
	if spec.Target == board.HostTarget {
		return r.b.runHost(ctx, spec, line)
	}
	// the turn keeps its VM up for its whole life, start included
	defer r.b.s.JXHold(spec.Target, "board")()
	rem, err := r.b.vmRemote(ctx, spec.Target, status)
	if err != nil {
		return err
	}
	if err := r.b.guests.Prepare(ctx, spec.Target, rem, spec.Agent, status); err != nil {
		return err
	}
	return board.RunVMTurn(ctx, rem, spec, line)
}

// vmRemote returns vm's shell, starting the VM first when it is not
// running (through the idle feature's jxEnsureVMUp, which applies the
// memory guard, when it is built in).
func (b *jxBoard) vmRemote(ctx context.Context, vm string, status func(string)) (board.Remote, error) {
	s := b.s
	if s.VMs == nil {
		return nil, vmm.ErrNoBackend
	}
	info, err := s.VMs.Get(ctx, vm)
	if err != nil {
		return nil, err
	}
	if info.State != "running" || info.IP == "" {
		status("starting " + vm)
		if jxEnsureVMUp != nil {
			err = jxEnsureVMUp(s, ctx, vm)
		} else {
			if info.State != "running" {
				_, err = s.VMs.Start(ctx, vm)
			}
			if err == nil {
				err = b.waitSSH(ctx, vm, 3*time.Minute)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("starting %s: %w", vm, err)
		}
		if info, err = s.VMs.Get(ctx, vm); err != nil {
			return nil, err
		}
		if info.IP == "" {
			return nil, fmt.Errorf("%s has no IP address", vm)
		}
	}
	return b.remote(info), nil
}

// waitSSH waits until vm has an IP and its sshd runs a command.
func (b *jxBoard) waitSSH(ctx context.Context, vm string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var last error
	for {
		info, err := b.s.VMs.Get(ctx, vm)
		switch {
		case err != nil:
			last = err
		case info.IP == "":
			last = errors.New("no IP address yet")
		default:
			if last = b.remote(info).Exec(ctx, "true", nil, nil, nil); last == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ssh did not answer: %v", last)
		case <-time.After(2 * time.Second):
		}
	}
}

// runHost runs a turn with the host's own CLI, in the Workspace folder or
// the folder a resumed session was started in, with the CLIs' safer
// permission modes.
func (b *jxBoard) runHost(ctx context.Context, spec board.Spec, line func([]byte)) error {
	a, ok := hostAgents[spec.Agent]
	if !ok {
		return fmt.Errorf("unknown agent %q", spec.Agent)
	}
	bin := agentPath(a)
	if bin == "" {
		return fmt.Errorf("%s is not installed on this host", a.title)
	}
	dir := b.s.workspaceDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if spec.SessionID != "" {
		if d := hostSessionDir(spec.Agent, spec.SessionID); d != "" {
			dir = d
		}
	}
	var env []string
	for _, kv := range cliEnv(bin) {
		// the daemon may itself run under a Claude Code session; the turn
		// is no nested session of it
		if !strings.HasPrefix(kv, "CLAUDECODE=") && !strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=") {
			env = append(env, kv)
		}
	}
	return board.RunHost(ctx, bin, board.CLIArgs(spec.Agent, spec.SessionID, spec.Fork, true), dir, env, spec.Prompt, line)
}

// hostSessionDir is the folder a host session was started in, "" when it
// is unknown or gone. Claude Code finds a session to resume only from its
// own folder.
func hostSessionDir(agent, id string) string {
	var dir string
	if agent == board.Claude {
		if t := claudeSessionByID(claudeHome(), id); t != nil {
			dir = t.cwd
		}
	} else if p := codexRolloutPath(codexHome(), id); p != "" {
		if r := readCodexRollout(p); r != nil {
			dir = r.cwd
		}
	}
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

// codexRolloutPath finds a Codex thread's rollout file by its id.
func codexRolloutPath(home, id string) string {
	if home == "" || !board.ValidSessionID(id) {
		return ""
	}
	var found string
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), id+".jsonl") {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found
}
