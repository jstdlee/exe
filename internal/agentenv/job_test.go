package agentenv

import (
	"os"
	"testing"
	"time"
)

func TestSessionAppendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Session{ID: "sess1", VM: "box"}
	if err := SaveSession(dir, s); err != nil {
		t.Fatal(err)
	}
	turn := Turn{JobID: "j1", ShellOutput: "hello from guest", ExitCode: 0, CreatedAt: time.Now().UTC()}
	if err := AppendTurn(dir, s, turn); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(dir, "box", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Turns) != 1 || got.Turns[0].ShellOutput != "hello from guest" {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(sessionPath(dir, "box", "sess1")); err != nil {
		t.Fatal(err)
	}
}
