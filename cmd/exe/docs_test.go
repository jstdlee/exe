package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, cpErr := io.Copy(&buf, r); cpErr != nil {
		t.Fatal(cpErr)
	}
	r.Close()
	return buf.String(), err
}

func TestUsageIncludesDocsAndSkillCommands(t *testing.T) {
	for _, want := range []string{
		"exe docs",
		"exe skill",
	} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage missing %q", want)
		}
	}
}

func TestCmdDocsAndSkillPrintEmbeddedMarkdown(t *testing.T) {
	docs, err := captureStdout(t, cmdDocs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Using exe", "VM Tools", "Workspace"} {
		if !strings.Contains(docs, want) {
			t.Fatalf("exe docs output missing %q", want)
		}
	}

	skill, err := captureStdout(t, cmdSkill)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# exe — portable agent Environment", "SSH gate", "exe env"} {
		if !strings.Contains(skill, want) {
			t.Fatalf("exe skill output missing %q", want)
		}
	}
}
