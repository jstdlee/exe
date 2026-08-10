package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/coder/websocket"
)

// hostShell is an interactive shell on a local pseudo-terminal. Close ends
// the session and reaps the shell process.
type hostShell interface {
	io.ReadWriteCloser
	Resize(cols, rows int)
}

// handleHostTerminal bridges a browser WebSocket to a login shell on the
// host itself — the desktop's Terminal window. Same wire protocol as the
// VM terminal: binary frames carry terminal bytes both ways, text frames
// carry control messages ({"resize":[cols,rows]}).
func (s *Server) handleHostTerminal(w http.ResponseWriter, r *http.Request) {
	sh, err := startHostShell(80, 24)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		sh.Close()
		return
	}
	c.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer sh.Close()
	defer c.CloseNow()

	out := &wsWriter{ctx: ctx, c: c}
	go func() {
		io.Copy(out, sh) // pty output → browser; EOF when the shell exits
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
			if _, err := sh.Write(data); err != nil {
				return
			}
		case websocket.MessageText:
			var msg struct {
				Resize []int `json:"resize"`
			}
			if json.Unmarshal(data, &msg) == nil && len(msg.Resize) == 2 {
				sh.Resize(msg.Resize[0], msg.Resize[1])
			}
		}
	}
}
