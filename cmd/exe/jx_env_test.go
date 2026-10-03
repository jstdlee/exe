package main

import (
	"archive/tar"
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	image := fs.String("image", "", "")
	git := fs.Bool("git", false, "")
	pos, err := parseInterspersed(fs, []string{"-image", "alpine", "box", "-git", "./proj"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pos, []string{"box", "./proj"}) || *image != "alpine" || !*git {
		t.Fatalf("pos %q image %q git %v", pos, *image, *git)
	}
}

func outputsTar(t *testing.T, hdrs ...tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range hdrs {
		h.Size = int64(len(h.Uname)) // Uname carries the body here
		body := h.Uname
		h.Uname = ""
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	return &buf
}

func TestExtractTar(t *testing.T) {
	dest := t.TempDir()
	err := extractTar(outputsTar(t,
		tar.Header{Name: "./dist/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "./dist/app.txt", Typeflag: tar.TypeReg, Mode: 0o644, Uname: "artifact"},
		tar.Header{Name: "./report/deep/r.json", Typeflag: tar.TypeReg, Mode: 0o600, Uname: "{}"},
	), dest)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "dist", "app.txt")); string(b) != "artifact" {
		t.Fatalf("dist/app.txt = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "report", "deep", "r.json")); string(b) != "{}" {
		t.Fatalf("report = %q", b)
	}

	if err := extractTar(outputsTar(t, tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Uname: "x"}), dest); err == nil {
		t.Fatal("extracted ../evil")
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	err = extractTar(outputsTar(t,
		tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: outside},
		tar.Header{Name: "link/pwned", Typeflag: tar.TypeReg, Mode: 0o644, Uname: "x"},
	), dest)
	if err == nil {
		t.Error("wrote through a symlink out of dest")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned")); err == nil {
		t.Fatal("file landed outside dest")
	}
}
