package agentenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssueAndRedeemTokenOnce(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(payload, []byte("hello-delivery"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := IssueToken(dir, payload, "out.txt", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ID == "" || tok.Used {
		t.Fatalf("bad token: %+v", tok)
	}
	got, err := RedeemToken(dir, tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != payload {
		t.Fatalf("path=%q want %q", got.Path, payload)
	}
	body, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello-delivery" {
		t.Fatalf("file %q", body)
	}
	if _, err := RedeemToken(dir, tok.ID); err != ErrTokenUsed {
		t.Fatalf("second redeem err=%v want used", err)
	}
}

func TestRedeemUnknownAndExpired(t *testing.T) {
	dir := t.TempDir()
	if _, err := RedeemToken(dir, "nope"); err != ErrTokenMissing {
		t.Fatalf("missing: %v", err)
	}
	p := filepath.Join(dir, "x")
	os.WriteFile(p, []byte("x"), 0o644)
	tok, err := IssueToken(dir, p, "x", -time.Hour) // ttl<=0 becomes 24h, so force expire
	if err != nil {
		t.Fatal(err)
	}
	// rewrite expiry in the store
	st, err := openTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Items[tok.ID].Expires = time.Now().Add(-time.Minute)
	if err := st.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := RedeemToken(dir, tok.ID); err != ErrTokenExpired {
		t.Fatalf("expired: %v", err)
	}
}
