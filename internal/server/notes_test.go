package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"exe/internal/config"
	"exe/internal/vmm"
)

type notesVMs struct {
	vms map[string]*vmm.Info
}

func (n notesVMs) Create(context.Context, vmm.Spec) (*vmm.Info, error) { return nil, vmm.ErrNotFound }
func (n notesVMs) Start(context.Context, string) (*vmm.Info, error)    { return nil, vmm.ErrNotFound }
func (n notesVMs) Stop(context.Context, string) error                  { return vmm.ErrNotFound }
func (n notesVMs) Delete(context.Context, string) error                { return vmm.ErrNotFound }
func (n notesVMs) List(context.Context) ([]*vmm.Info, error) {
	out := make([]*vmm.Info, 0, len(n.vms))
	for _, vm := range n.vms {
		out = append(out, vm)
	}
	return out, nil
}
func (n notesVMs) Get(_ context.Context, name string) (*vmm.Info, error) {
	vm, ok := n.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	return vm, nil
}

func TestNotesDeleteRemovesSavedNotes(t *testing.T) {
	dir := t.TempDir()
	s := New(&config.Config{}, notesVMs{vms: map[string]*vmm.Info{
		"demo": {Name: "demo", State: "running", CreatedAt: time.Now()},
	}}, nil, "", dir)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodPut, "/v1/vms/demo/notes", bytes.NewReader([]byte(`{"notes":"keep this briefly"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT notes status=%d body=%s", rec.Code, rec.Body.String())
	}
	path := filepath.Join(dir, "vms", "demo", "notes.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("notes file was not saved: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/vms/demo/notes", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE notes status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("notes file after DELETE: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/vms/demo/notes", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET notes status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Notes string `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Notes != "" {
		t.Fatalf("notes after DELETE = %q, want empty", got.Notes)
	}
}
