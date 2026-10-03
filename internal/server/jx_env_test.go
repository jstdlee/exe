package server

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"exe/internal/config"
	"exe/internal/jx/envplan"
	"exe/internal/jx/lease"
	"exe/internal/vmm"
)

// envFakeVMs is an in-memory vmm.Manager.
type envFakeVMs struct {
	mu    sync.Mutex
	vms   map[string]*vmm.Info
	calls []string
}

func newEnvFakeVMs(vms ...*vmm.Info) *envFakeVMs {
	f := &envFakeVMs{vms: map[string]*vmm.Info{}}
	for _, v := range vms {
		f.vms[v.Name] = v
	}
	return f
}

func (f *envFakeVMs) log(s string) { f.calls = append(f.calls, s) }

func (f *envFakeVMs) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *envFakeVMs) Create(_ context.Context, spec vmm.Spec) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log(fmt.Sprintf("create %s image=%s mem=%d", spec.Name, spec.Image, spec.MemoryMB))
	v := &vmm.Info{Name: spec.Name, State: "running", IP: "192.0.2.10", Image: spec.Image, MemoryMB: spec.MemoryMB}
	f.vms[spec.Name] = v
	c := *v
	return &c, nil
}

func (f *envFakeVMs) Start(_ context.Context, name string) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("start " + name)
	v, ok := f.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	v.State, v.IP = "running", "192.0.2.10"
	c := *v
	return &c, nil
}

func (f *envFakeVMs) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log("stop " + name)
	v, ok := f.vms[name]
	if !ok {
		return vmm.ErrNotFound
	}
	if v.State != "running" {
		return vmm.ErrNotRunning
	}
	v.State, v.IP = "stopped", ""
	return nil
}

func (f *envFakeVMs) Delete(context.Context, string) error { return errors.New("not in tests") }

func (f *envFakeVMs) List(context.Context) ([]*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*vmm.Info
	for _, v := range f.vms {
		c := *v
		out = append(out, &c)
	}
	return out, nil
}

func (f *envFakeVMs) Get(_ context.Context, name string) (*vmm.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vms[name]
	if !ok {
		return nil, vmm.ErrNotFound
	}
	c := *v
	return &c, nil
}

// fakeRunner plays the guest: it answers the commands env sends.
type fakeRunner struct {
	t        *testing.T
	s        *Server
	mu       sync.Mutex
	commands []string
	uploaded []string // tar entry names
	script   string
}

func (r *fakeRunner) Run(_ context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if command != "true" && !slices.ContainsFunc(r.s.Leases().Get("box").Holders, func(h lease.Holder) bool { return h.Reason == "env" }) {
		r.t.Errorf("%q ran without lease env", command)
	}
	switch {
	case command == "true":
		return 0, nil
	case strings.Contains(command, "tar -xf -"):
		tr := tar.NewReader(stdin)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			r.mu.Lock()
			r.uploaded = append(r.uploaded, h.Name)
			r.mu.Unlock()
		}
		return 0, nil
	case strings.Contains(command, "bootstrap.sh"):
		data, _ := io.ReadAll(stdin)
		r.mu.Lock()
		r.script = string(data)
		r.mu.Unlock()
		io.WriteString(stdout, "==> packages\nhéllo\n")
		return 0, nil
	case strings.Contains(command, "sh -lc"):
		io.WriteString(stdout, "built\n")
		io.WriteString(stderr, "warn\n")
		return 3, nil
	case strings.Contains(command, "tar -cf -"):
		tw := tar.NewWriter(stdout)
		tw.WriteHeader(&tar.Header{Name: "./dist/", Typeflag: tar.TypeDir, Mode: 0o755})
		body := "artifact"
		tw.WriteHeader(&tar.Header{Name: "./dist/app.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		io.WriteString(tw, body)
		tw.Close()
		return 0, nil
	}
	r.t.Errorf("unexpected command %q", command)
	return 127, nil
}

func (r *fakeRunner) Commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.commands)
}

func envTestServer(t *testing.T, vms *envFakeVMs) (*Server, *fakeRunner, *httptest.Server) {
	t.Helper()
	s := New(&config.Config{SSHUser: "dev"}, vms, nil, filepath.Join(t.TempDir(), "id"), t.TempDir())
	run := &fakeRunner{t: t, s: s}
	old := jxEnvRunner
	jxEnvRunner = func(*Server, *vmm.Info) envRunner { return run }
	t.Cleanup(func() { jxEnvRunner = old })
	// the real jxEnsureVMUp (jx_idle.go) dials SSH; here a start is enough
	oldUp := jxEnsureVMUp
	jxEnsureVMUp = func(s *Server, ctx context.Context, vm string) error {
		_, err := s.VMs.Start(ctx, vm)
		return err
	}
	t.Cleanup(func() { jxEnsureVMUp = oldUp })
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, run, srv
}

func readEvents(t *testing.T, resp *http.Response) []EnvEvent {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var out []EnvEvent
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var ev EnvEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		out = append(out, ev)
	}
	return out
}

func lastDone(t *testing.T, evs []EnvEvent) int {
	t.Helper()
	if len(evs) == 0 || evs[len(evs)-1].Type != "done" || evs[len(evs)-1].Code == nil {
		t.Fatalf("stream does not end in done: %+v", evs)
	}
	return *evs[len(evs)-1].Code
}

func TestJXEnvPlan(t *testing.T) {
	_, _, srv := envTestServer(t, newEnvFakeVMs())
	body := `{"files":{"package.json":"{\"engines\":{\"node\":\"22\"}}","apt.txt":"ffmpeg\n"},"image":"alpine"}`
	resp, err := http.Post(srv.URL+"/v1/jx/env/plan", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res EnvPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || resp.StatusCode != 200 {
		t.Fatalf("status %d, %v", resp.StatusCode, err)
	}
	if res.Distro != envplan.Alpine || !strings.Contains(res.Script, "apk add") || !strings.Contains(res.Script, "ffmpeg") {
		t.Fatalf("plan = %+v", res)
	}
	resp, _ = http.Post(srv.URL+"/v1/jx/env/plan", "application/json", strings.NewReader(`{"image":"fedora"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad image: status %d", resp.StatusCode)
	}
}

func projectTar(t *testing.T) *bytes.Buffer {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range map[string]string{
		"package.json":                 `{"engines":{"node":">=18"}}`,
		"package-lock.json":            "{}",
		"src/index.js":                 "console.log(1)",
		"node_modules/left-pad/a.js":   "x",
		".gitignore":                   "*.log\n",
		"debug.log":                    "noise",
		"requirements.txt":             "flask\n",
		"node_modules/.package-lock.j": "x",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := envplan.WriteTar(&buf, dir, envplan.PackOptions{}); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestJXEnvUpCreatesVM(t *testing.T) {
	vms := newEnvFakeVMs()
	s, run, srv := envTestServer(t, vms)
	resp, err := http.Post(srv.URL+"/v1/jx/env/up?vm=box&image=alpine&mem=1024", "application/x-tar", projectTar(t))
	if err != nil {
		t.Fatal(err)
	}
	evs := readEvents(t, resp)
	if code := lastDone(t, evs); code != 0 {
		t.Fatalf("done code %d: %+v", code, evs)
	}
	if calls := vms.Calls(); !slices.Equal(calls, []string{"create box image=alpine mem=1024"}) {
		t.Errorf("VM calls = %q", calls)
	}
	var plan *envplan.Plan
	var out strings.Builder
	for _, ev := range evs {
		switch ev.Type {
		case "plan":
			plan = ev.Plan
		case "stdout":
			out.WriteString(ev.Text)
		case "error":
			t.Errorf("error event: %s", ev.Error)
		}
	}
	if plan == nil || !slices.Equal(plan.Sources, []string{"package.json", "requirements.txt"}) {
		t.Fatalf("plan = %+v", plan)
	}
	if out.String() != "==> packages\nhéllo\n" {
		t.Errorf("stdout = %q", out.String())
	}
	run.mu.Lock()
	uploaded, script := slices.Clone(run.uploaded), run.script
	run.mu.Unlock()
	slices.Sort(uploaded)
	want := []string{".gitignore", "package-lock.json", "package.json", "requirements.txt", "src/", "src/index.js"}
	if !slices.Equal(uploaded, want) {
		t.Errorf("uploaded %q, want %q", uploaded, want)
	}
	if !strings.Contains(script, "apk add") || !strings.Contains(script, "npm ci") || !strings.Contains(script, "-r requirements.txt") {
		t.Errorf("bootstrap script:\n%s", script)
	}
	if h := s.Leases().Get("box").Holders; len(h) != 0 {
		t.Errorf("lease still held after up: %+v", h)
	}
	if entries, _ := os.ReadDir(filepath.Join(s.jxDir(), "tmp")); len(entries) != 0 {
		t.Errorf("upload spool left behind: %v", entries)
	}
}

func TestJXEnvUpStartsStoppedVM(t *testing.T) {
	vms := newEnvFakeVMs(&vmm.Info{Name: "box", State: "stopped", Image: "debian"})
	_, run, srv := envTestServer(t, vms)
	resp, err := http.Post(srv.URL+"/v1/jx/env/up?vm=box", "application/x-tar", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	evs := readEvents(t, resp)
	if lastDone(t, evs) != 0 {
		t.Fatalf("events %+v", evs)
	}
	if calls := vms.Calls(); !slices.Equal(calls, []string{"start box"}) {
		t.Errorf("VM calls = %q", calls)
	}
	for _, c := range run.Commands() {
		if strings.Contains(c, "tar -xf") {
			t.Error("an empty upload still ran tar")
		}
	}
	if !strings.Contains(run.script, "apt-get") {
		t.Errorf("debian VM got script:\n%s", run.script)
	}
}

func TestJXEnvUpRejects(t *testing.T) {
	_, _, srv := envTestServer(t, newEnvFakeVMs())
	evil := func(entries ...tar.Header) io.Reader {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range entries {
			tw.WriteHeader(&h)
		}
		tw.Close()
		return &buf
	}
	for name, tc := range map[string]struct {
		query string
		body  io.Reader
		code  int
	}{
		"bad name":     {"vm=Bad_Name", nil, http.StatusBadRequest},
		"bad image":    {"vm=box&image=arch", nil, http.StatusBadRequest},
		"bad mem":      {"vm=box&mem=lots", nil, http.StatusBadRequest},
		"dot dot":      {"vm=box", evil(tar.Header{Name: "../escape", Typeflag: tar.TypeReg}), http.StatusBadRequest},
		"absolute":     {"vm=box", evil(tar.Header{Name: "/etc/passwd", Typeflag: tar.TypeReg}), http.StatusBadRequest},
		"via symlink":  {"vm=box", evil(tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc"}, tar.Header{Name: "l/passwd", Typeflag: tar.TypeReg}), http.StatusBadRequest},
		"device":       {"vm=box", evil(tar.Header{Name: "dev", Typeflag: tar.TypeChar}), http.StatusBadRequest},
		"not a tar":    {"vm=box", strings.NewReader(strings.Repeat("garbage!", 200)), http.StatusBadRequest},
		"snapshotting": {"vm=busy", nil, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.query == "vm=busy" {
				jxDiskBusy.Store("busy", struct{}{})
				defer jxDiskBusy.Delete("busy")
			}
			body := tc.body
			if body == nil {
				body = bytes.NewReader(nil)
			}
			resp, err := http.Post(srv.URL+"/v1/jx/env/up?"+tc.query, "application/x-tar", body)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.code {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.code)
			}
		})
	}
}

func TestJXEnvRun(t *testing.T) {
	vms := newEnvFakeVMs(&vmm.Info{Name: "box", State: "stopped"})
	_, run, srv := envTestServer(t, vms)
	post := func(body string) *http.Response {
		resp, err := http.Post(srv.URL+"/v1/jx/env/run", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	evs := readEvents(t, post(`{"vm":"box","command":"make && echo 'done'","outputs":["dist","./dist/../dist"]}`))
	if code := lastDone(t, evs); code != 3 {
		t.Fatalf("done code %d, want 3: %+v", code, evs)
	}
	var stdout, stderr strings.Builder
	var tarData []byte
	exit := -1
	for _, ev := range evs {
		switch ev.Type {
		case "stdout":
			stdout.WriteString(ev.Text)
		case "stderr":
			stderr.WriteString(ev.Text)
		case "exit":
			exit = *ev.Code
		case "tar":
			b, err := base64.StdEncoding.DecodeString(ev.Data)
			if err != nil {
				t.Fatal(err)
			}
			tarData = append(tarData, b...)
		case "error":
			t.Errorf("error event %s", ev.Error)
		}
	}
	if stdout.String() != "built\n" || stderr.String() != "warn\n" || exit != 3 {
		t.Errorf("stdout %q stderr %q exit %d", stdout.String(), stderr.String(), exit)
	}
	tr := tar.NewReader(bytes.NewReader(tarData))
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if !slices.Equal(names, []string{"./dist/", "./dist/app.txt"}) {
		t.Errorf("tar entries %q", names)
	}
	cmds := run.Commands()
	if !slices.Contains(cmds, `mkdir -p "$HOME/work" && cd "$HOME/work" && exec sh -lc 'make && echo '\''done'\'''`) {
		t.Errorf("commands = %q", cmds)
	}
	if !slices.Contains(cmds, `cd "$HOME/work" && tar -cf - './dist' './dist'`) {
		t.Errorf("commands = %q", cmds)
	}
	if calls := vms.Calls(); !slices.Equal(calls, []string{"start box"}) {
		t.Errorf("VM calls = %q", calls)
	}

	for body, code := range map[string]int{
		`{"vm":"box","command":""}`:                        http.StatusBadRequest,
		`{"vm":"box","command":"ls","outputs":["../etc"]}`: http.StatusBadRequest,
		`{"vm":"box","command":"ls","outputs":["/etc"]}`:   http.StatusBadRequest,
		`{"vm":"nope","command":"ls"}`:                     http.StatusNotFound,
		`{"vm":"Bad Name","command":"ls"}`:                 http.StatusBadRequest,
		`not json`:                                         http.StatusBadRequest,
	} {
		resp := post(body)
		resp.Body.Close()
		if resp.StatusCode != code {
			t.Errorf("%s: status %d, want %d", body, resp.StatusCode, code)
		}
	}
}

func TestTextWriterKeepsRunesWhole(t *testing.T) {
	var buf bytes.Buffer
	n := &ndjson{enc: json.NewEncoder(&buf)}
	w := &textWriter{n: n, typ: "stdout"}
	s := "héllo, 世界"
	for i := 0; i < len(s); i++ {
		w.Write([]byte{s[i]})
	}
	w.Flush()
	var got strings.Builder
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var ev EnvEvent
		if err := dec.Decode(&ev); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(ev.Text, '�') {
			t.Fatalf("event split a rune: %q", ev.Text)
		}
		got.WriteString(ev.Text)
	}
	if got.String() != s {
		t.Fatalf("got %q", got.String())
	}
}
