package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"exe/internal/config"
)

func writeTestApp(t *testing.T, root, name, title string) {
	t.Helper()
	dir := filepath.Join(root, "apps", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"title":` + strconvQuote(title) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "app.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>"+title+"</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func testServerWithState(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s := &Server{StateDir: dir}
	s.cfg.Store(&config.Config{})
	s.ensureStateDirs()
	return s
}

func TestAppsListDoesNotExposeStandaloneEditorApp(t *testing.T) {
	s := testServerWithState(t)
	writeTestApp(t, s.StateDir, "Editor", "Editor")
	writeTestApp(t, s.StateDir, "Notes", "Notes")

	req := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
	rec := httptest.NewRecorder()
	s.handleApps(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apps status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []appMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, app := range got {
		names[app.Name] = true
	}
	if names["Editor"] {
		t.Fatalf("Editor app exposed in /v1/apps: %+v", got)
	}
	if !names["Notes"] {
		t.Fatalf("Notes app missing from /v1/apps: %+v", got)
	}
}

func TestWorkspaceFlatListHidesDotfilesAndTrash(t *testing.T) {
	s := testServerWithState(t)
	files := map[string]string{
		"visible.txt":              "ok",
		".secret.txt":              "secret",
		".Trash/deleted.txt":       "deleted",
		"folder/.hidden.txt":       "hidden",
		"folder/visible-child.txt": "child",
	}
	for rel, body := range files {
		p := filepath.Join(s.workspaceDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/workspace", nil)
	rec := httptest.NewRecorder()
	s.handleWorkspaceList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Files []storedFile `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, f := range got.Files {
		paths[f.Path] = true
	}
	if !paths["visible.txt"] || !paths["folder/visible-child.txt"] {
		t.Fatalf("visible files missing from workspace list: %+v", got.Files)
	}
	for _, hidden := range []string{".secret.txt", ".Trash/deleted.txt", "folder/.hidden.txt"} {
		if paths[hidden] {
			t.Fatalf("hidden path %q exposed in workspace list: %+v", hidden, got.Files)
		}
	}
}
