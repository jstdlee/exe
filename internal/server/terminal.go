package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"exe/internal/sshexec"
)

func quoteGuest(s string) string { return sshexec.Quote(s) }

// wsWriter serializes terminal output into binary WebSocket frames.
type wsWriter struct {
	ctx context.Context
	c   *websocket.Conn
	mu  sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.c.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// handleTerminal bridges a browser WebSocket to an interactive SSH shell in
// the VM. Binary frames carry terminal bytes both ways; text frames carry
// control messages ({"resize":[cols,rows]}).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, err := s.runningVM(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	target := s.vmTarget(info)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer c.CloseNow()

	out := &wsWriter{ctx: ctx, c: c}
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	client, err := target.Dial(dctx)
	dcancel()
	if err != nil {
		out.Write([]byte("\r\n\x1b[31m[exe] VM SSH unavailable: " + err.Error() + "\x1b[0m\r\n"))
		c.Close(websocket.StatusTryAgainLater, "VM SSH unavailable")
		return
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		out.Write([]byte("\r\n\x1b[31m[exe] could not open SSH session: " + err.Error() + "\x1b[0m\r\n"))
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer func() {
		_ = sess.Signal(ssh.SIGHUP)
		_ = sess.Close()
	}()

	sess.Stdout = out
	sess.Stderr = out
	stdin, err := sess.StdinPipe()
	if err != nil {
		out.Write([]byte("\r\n\x1b[31m[exe] could not open terminal input: " + err.Error() + "\x1b[0m\r\n"))
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		out.Write([]byte("\r\n\x1b[31m[exe] could not allocate VM PTY: " + err.Error() + "\x1b[0m\r\n"))
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}
	// ?run=claude|git|… starts that program as the PTY so the TUI owns
	// stdin from the first byte (typing an installer into a login shell
	// left agents deaf and leftover keystrokes in the buffer).
	if script := GuestRunScript(r.URL.Query().Get("run")); script != "" {
		if err := sess.Start("bash -lc " + quoteGuest(script)); err != nil {
			out.Write([]byte("\r\n\x1b[31m[exe] could not start VM tool: " + err.Error() + "\x1b[0m\r\n"))
			c.Close(websocket.StatusInternalError, err.Error())
			return
		}
	} else if err := sess.Shell(); err != nil {
		out.Write([]byte("\r\n\x1b[31m[exe] could not start VM shell: " + err.Error() + "\x1b[0m\r\n"))
		c.Close(websocket.StatusInternalError, err.Error())
		return
	}

	go func() {
		sess.Wait()
		c.Close(websocket.StatusNormalClosure, "session ended")
		cancel()
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageBinary:
			s.touchVM(name)
			if _, err := stdin.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var msg struct {
				Resize []int `json:"resize"`
			}
			if json.Unmarshal(data, &msg) == nil && len(msg.Resize) == 2 {
				cols, rows := msg.Resize[0], msg.Resize[1]
				if cols >= 2 && rows >= 2 {
					sess.WindowChange(rows, cols)
				}
			}
		}
	}
}
