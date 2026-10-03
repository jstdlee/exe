package envplan

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

func TestIgnore(t *testing.T) {
	ig := ParseGitignore([]byte(`# build output
*.log
!keep.log
/dist
build/
docs/**/*.tmp
**/cache
\#weird
secret.env
`))
	for _, tc := range []struct {
		path string
		dir  bool
		want bool
	}{
		{"app.log", false, true},
		{"sub/deep/app.log", false, true},
		{"keep.log", false, false},
		{"dist", true, true},
		{"sub/dist", true, false},
		{"build", true, true},
		{"build", false, false}, // dir-only rule, plain file
		{"src/build", true, true},
		{"docs/a/b/x.tmp", false, true},
		{"docs/x.tmp", false, true},
		{"x.tmp", false, false},
		{"a/cache", true, true},
		{"cache", true, true},
		{"#weird", false, true},
		{"secret.env", false, true},
		{"main.go", false, false},
	} {
		if got := ig.Match(tc.path, tc.dir); got != tc.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}

func TestWriteTar(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.log\n/out/\n")
	write("main.go", "package main\n")
	write("app.log", "noise")
	write("out/bin", "binary")
	write("web/node_modules/left-pad/index.js", "x")
	write("web/src/index.ts", "export {}")
	write(".venv/bin/python", "x")
	write("target/debug/app", "x")
	write(".git/HEAD", "ref: refs/heads/main\n")
	write("pkg/__pycache__/m.pyc", "x")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("main.go", filepath.Join(dir, "link.go")); err != nil {
			t.Fatal(err)
		}
	}

	read := func(opt PackOptions) ([]string, PackStats) {
		var buf bytes.Buffer
		st, err := WriteTar(&buf, dir, opt)
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(&buf)
		var names []string
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Typeflag == tar.TypeSymlink && h.Linkname != "main.go" {
				t.Errorf("link %s -> %s", h.Name, h.Linkname)
			}
			names = append(names, h.Name)
		}
		sort.Strings(names)
		return names, st
	}

	names, st := read(PackOptions{})
	want := []string{".gitignore", "main.go", "pkg/", "web/", "web/src/", "web/src/index.ts"}
	if runtime.GOOS != "windows" {
		want = append(want, "link.go")
		sort.Strings(want)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tar entries = %q, want %q", names, want)
	}
	if st.Files != 3 || st.Bytes != int64(len("*.log\n/out/\n")+len("package main\n")+len("export {}")) {
		t.Errorf("stats = %+v", st)
	}

	names, _ = read(PackOptions{Git: true})
	found := false
	for _, n := range names {
		found = found || n == ".git/HEAD"
	}
	if !found {
		t.Errorf("Git: true did not include .git/HEAD: %q", names)
	}
}
