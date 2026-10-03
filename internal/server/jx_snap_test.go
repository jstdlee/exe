package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"exe/internal/config"
	"exe/internal/jx/snap"
	"exe/internal/vmm"
)

func snapTestServer(t *testing.T, state string) (*Server, *envFakeVMs, *httptest.Server) {
	t.Helper()
	vms := newEnvFakeVMs(&vmm.Info{Name: "box", State: "running", IP: "192.0.2.10"})
	s := New(&config.Config{SSHUser: "dev"}, vms, nil, filepath.Join(t.TempDir(), "id"), state)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	disk := snap.DiskPath(state, "box")
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(disk)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(8 << 20)
	f.WriteAt([]byte("version one"), 1<<20)
	f.Close()
	return s, vms, srv
}

func do(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestJXSnapLifecycle(t *testing.T) {
	state := t.TempDir()
	s, vms, srv := snapTestServer(t, state)
	base := srv.URL + "/v1/jx/vms/box/snapshots"

	resp, b := do(t, "POST", base, `{"label":"clean install"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	var res SnapResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Restarted || res.Snapshot.Label != "clean install" || !snap.ValidID(res.Snapshot.ID) {
		t.Fatalf("result = %+v", res)
	}
	if calls := vms.Calls(); !slices.Equal(calls, []string{"stop box", "start box"}) {
		t.Fatalf("VM calls = %q", calls)
	}
	if _, err := os.Stat(filepath.Join(state, "jx", "snapshots", "box", res.Snapshot.ID, "disk.raw")); err != nil {
		t.Fatal(err)
	}

	// change the disk, then restore
	disk := snap.DiskPath(state, "box")
	f, _ := os.OpenFile(disk, os.O_WRONLY, 0)
	f.WriteAt([]byte("version two"), 1<<20)
	f.Close()

	// a held VM is refused, unless force
	release := s.JXHold("box", "terminal")
	resp, b = do(t, "POST", base+"/"+res.Snapshot.ID+"/restore", "")
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "terminal") {
		t.Fatalf("restore while held: %d %s", resp.StatusCode, b)
	}
	resp, b = do(t, "POST", base, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("create while held: %d %s", resp.StatusCode, b)
	}
	resp, b = do(t, "POST", base+"/"+res.Snapshot.ID+"/restore", `{"force":true}`)
	release()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %d %s", resp.StatusCode, b)
	}
	got, _ := os.ReadFile(disk)
	if !bytes.Contains(got, []byte("version one")) {
		t.Fatal("restore did not bring version one back")
	}

	resp, b = do(t, "GET", base, "")
	var list []snap.Meta
	if resp.StatusCode != 200 || json.Unmarshal(b, &list) != nil || len(list) != 1 {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}

	for _, tc := range []struct{ method, url string }{
		{"DELETE", base + "/20200101-000000-abcdef"},
		{"POST", base + "/nope/restore"},
		{"POST", srv.URL + "/v1/jx/vms/ghost/snapshots"},
	} {
		if resp, b := do(t, tc.method, tc.url, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", tc.method, tc.url, resp.StatusCode, b)
		}
	}
	if resp, _ := do(t, "GET", srv.URL+"/v1/jx/vms/Bad_Name/snapshots", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad name: %d", resp.StatusCode)
	}
	if resp, b := do(t, "DELETE", base+"/"+res.Snapshot.ID, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "GET", base, ""); string(bytes.TrimSpace(b)) != "[]" {
		t.Fatalf("list after delete: %d %s", resp.StatusCode, b)
	}
}

func TestJXSnapStoppedVMStaysStopped(t *testing.T) {
	state := t.TempDir()
	_, vms, srv := snapTestServer(t, state)
	vms.vms["box"].State = "stopped"
	resp, b := do(t, "POST", srv.URL+"/v1/jx/vms/box/snapshots", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	var res SnapResult
	json.Unmarshal(b, &res)
	if res.Restarted || len(vms.Calls()) != 0 {
		t.Fatalf("stopped VM was touched: %+v %q", res, vms.Calls())
	}
}

func TestJXSnapBusyRefused(t *testing.T) {
	_, _, srv := snapTestServer(t, t.TempDir())
	jxDiskBusy.Store("box", struct{}{})
	defer jxDiskBusy.Delete("box")
	if resp, b := do(t, "POST", srv.URL+"/v1/jx/vms/box/snapshots", ""); resp.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent snapshot: %d %s", resp.StatusCode, b)
	}
}

func TestJXDocsAccessors(t *testing.T) {
	if !bytes.Equal(DocsMarkdown(), docsMD) || len(docsMD) == 0 {
		t.Fatal("DocsMarkdown differs from the embedded docs.md")
	}
	skill := SkillMarkdown()
	if !bytes.Equal(skill, skillMD) || !bytes.Contains(skill, []byte("## jstdlee extensions")) {
		t.Fatal("SkillMarkdown lacks the jstdlee section")
	}
	skill[0] = 'X'
	if skillMD[0] == 'X' {
		t.Fatal("SkillMarkdown hands out the embedded slice")
	}
}
