package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"exe/internal/agentenv"
)

func TestHandleDeliveryServesIssuedFileOnce(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "artifact.txt")
	if err := os.WriteFile(payload, []byte("delivery-body-ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := agentenv.IssueToken(dir, payload, "artifact.txt", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{StateDir: dir}
	req := httptest.NewRequest(http.MethodGet, "/dl/"+tok.ID, nil)
	req.SetPathValue("token", tok.ID)
	rec := httptest.NewRecorder()
	s.handleDelivery(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "delivery-body-ok" {
		t.Fatalf("body %q", rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	s.handleDelivery(rec2, req)
	if rec2.Code != http.StatusGone {
		t.Fatalf("second download status %d", rec2.Code)
	}
}
