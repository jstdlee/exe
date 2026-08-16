package agentenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestComposeUsesImageAsDebianPackages(t *testing.T) {
	raw := `
services:
  api:
    image: python:3.12-slim
    ports:
      - "8000:8000"
  cache:
    image: redis:7
`
	m := ParseManifest("docker-compose.yml", raw)
	if m.Kind != "compose" {
		t.Fatalf("kind=%q", m.Kind)
	}
	has := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}
	if !has(m.Services, "api") || !has(m.Services, "cache") {
		t.Fatalf("services=%v", m.Services)
	}
	if !has(m.Images, "python:3.12-slim") {
		t.Fatalf("images=%v", m.Images)
	}
	if !has(m.Packages, "python3") || !has(m.Packages, "redis-tools") {
		t.Fatalf("packages=%v want python3 and redis-tools", m.Packages)
	}
	if !strings.Contains(m.Prompt, "python3") {
		t.Fatalf("prompt missing packages: %s", m.Prompt)
	}
}

func TestParseManifestPyprojectReadsDependencies(t *testing.T) {
	raw := `
[project]
name = "demo"
requires-python = ">=3.11"
dependencies = [
  "flask>=3",
  "requests",
]
`
	m := ParseManifest("pyproject.toml", raw)
	if m.Kind != "pyproject" {
		t.Fatalf("kind=%q", m.Kind)
	}
	joined := strings.Join(m.Python, " ")
	if !strings.Contains(joined, "flask") || !strings.Contains(joined, "requests") {
		t.Fatalf("python=%v", m.Python)
	}
	if !contains(m.Packages, "python3") {
		t.Fatalf("packages=%v", m.Packages)
	}
}

func TestParseManifestGitHubSetupActions(t *testing.T) {
	raw := `
name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
      - run: apt-get install -y git curl
      - run: pip install pytest
`
	m := ParseManifest(".github/workflows/ci.yml", raw)
	if m.Kind != "github" {
		t.Fatalf("kind=%q", m.Kind)
	}
	if !contains(m.Packages, "nodejs") || !contains(m.Packages, "git") {
		t.Fatalf("packages=%v", m.Packages)
	}
	if !contains(m.Python, "pytest") {
		t.Fatalf("python=%v", m.Python)
	}
}

func TestParseManifestTextList(t *testing.T) {
	m := ParseManifest("deps.txt", "# tools\ngit\ncurl build-essential\n")
	if m.Kind != "text" {
		t.Fatalf("kind=%q", m.Kind)
	}
	if !contains(m.Packages, "git") || !contains(m.Packages, "curl") || !contains(m.Packages, "build-essential") {
		t.Fatalf("packages=%v", m.Packages)
	}
}

func TestParseManifestFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "requirements-ish.txt")
	if err := os.WriteFile(p, []byte("ripgrep\nfd-find\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifestFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(m.Packages, "ripgrep") || !contains(m.Packages, "fd-find") {
		t.Fatalf("packages=%v", m.Packages)
	}
}

func TestBootstrapScriptContainsFlavorAndPackages(t *testing.T) {
	f := Debian()
	m := ParseManifest("deps.txt", "git\n")
	sh := BootstrapScript(f, m)
	if !strings.Contains(sh, "apt-get install") || !strings.Contains(sh, "git") {
		t.Fatalf("script:\n%s", sh)
	}
	if !strings.Contains(sh, f.Name) || !strings.Contains(sh, "BOOTSTRAP.md") {
		t.Fatalf("missing flavor/bootstrap.md:\n%s", sh)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
