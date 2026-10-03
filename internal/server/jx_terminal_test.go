package server

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"exe/internal/config"
	"exe/internal/keys"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

func termURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/vms/guest/terminal"
}

// A dial failure reaches the browser as terminal text, then a close.
func TestJXTerminalDialErrorShown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens: the dial is refused
	keyPath, _, err := keys.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(&config.Config{SSHUser: "guest"}, terminalVM{addr: addr}, nil, keyPath, t.TempDir())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c, _, err := websocket.Dial(ctx, termURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var text strings.Builder
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if !errors.As(err, &ce) || ce.Code != websocket.StatusInternalError {
				t.Fatalf("close = %v", err)
			}
			break
		}
		if typ == websocket.MessageBinary {
			text.Write(data)
		}
	}
	if !strings.Contains(text.String(), "exe: ") || !strings.Contains(text.String(), "refused") {
		t.Fatalf("terminal text = %q", text.String())
	}
}

// Bad resize values never reach the guest; closing the window sends SIGHUP.
func TestJXTerminalResizeAndHangup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keyPath, _, err := keys.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
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
	defer ln.Close()
	requests := make(chan *ssh.Request, 32)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		ch := <-chans
		if ch == nil {
			return
		}
		channel, reqs2, err := ch.Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		for req := range reqs2 {
			requests <- req
			if req.WantReply {
				req.Reply(true, nil)
			}
			if req.Type == "shell" {
				channel.Write([]byte("ready\r\n"))
			}
		}
	}()
	s := New(&config.Config{SSHUser: "guest"}, terminalVM{addr: ln.Addr().String()}, nil, keyPath, t.TempDir())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	c, _, err := websocket.Dial(ctx, termURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	next := func() *ssh.Request {
		t.Helper()
		select {
		case r := <-requests:
			return r
		case <-ctx.Done():
			t.Fatal("no SSH request")
			return nil
		}
	}
	for _, want := range []string{"pty-req", "shell"} {
		if r := next(); r.Type != want {
			t.Fatalf("request %q, want %q", r.Type, want)
		}
	}
	for _, msg := range []string{`{"resize":[0,30]}`, `{"resize":[100,-1]}`, `{"resize":[5000,30]}`, `{"resize":[120,40]}`} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	r := next()
	var size struct{ Cols, Rows, Width, Height uint32 }
	if r.Type != "window-change" || ssh.Unmarshal(r.Payload, &size) != nil || size.Cols != 120 || size.Rows != 40 {
		t.Fatalf("first resize to reach the guest: %s %+v", r.Type, size)
	}
	c.Close(websocket.StatusNormalClosure, "window closed")
	r = next()
	var sig struct{ Signal string }
	if r.Type != "signal" || ssh.Unmarshal(r.Payload, &sig) != nil || sig.Signal != "HUP" {
		t.Fatalf("on close: %s %q", r.Type, sig.Signal)
	}
}

func TestTermDim(t *testing.T) {
	for n, want := range map[int]bool{-1: false, 0: false, 1: true, 80: true, 1000: true, 1001: false} {
		if termDim(n) != want {
			t.Errorf("termDim(%d) = %v", n, !want)
		}
	}
}
