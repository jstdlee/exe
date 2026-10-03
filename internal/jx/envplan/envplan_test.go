package envplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func files(kv ...string) map[string][]byte {
	m := map[string][]byte{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = []byte(kv[i+1])
	}
	return m
}

func stepNames(p Plan) []string {
	var out []string
	for _, s := range p.Steps {
		name := s.Name
		if s.Root {
			name = "root:" + name
		}
		if s.Distro != "" {
			name += "@" + s.Distro
		}
		out = append(out, name)
	}
	return out
}

func hasNote(p Plan, sub string) bool {
	for _, n := range p.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

const composeDjango = `
services:
  web:
    build: .
    command: python manage.py runserver 0.0.0.0:8000
    ports:
      - "8000:8000"
    depends_on: [db, cache]
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
    ports:
      - target: 5432
        published: 15432
  cache:
    image: docker.io/library/redis:7.2
  search:
    image: docker.elastic.co/elasticsearch/elasticsearch:8.13.0
`

const workflowNode = `
name: CI
on:
  push:
    branches: [main]
jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
        node-version: [18.x, 20.x, 22.x]
    services:
      postgres:
        image: postgres:15
        ports: ["5432:5432"]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node-version }}
          cache: npm
      - name: System deps
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends libvips-dev \
            build-essential
      - run: npm ci
      - run: pip install pre-commit
      - run: npm install -g typescript
      - name: Test
        run: |
          npm run lint
          npm test
      - uses: codecov/codecov-action@v4
  windows:
    runs-on: windows-latest
    steps:
      - run: choco install make
`

func TestFromFiles(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      map[string][]byte
		sources    []string
		deb, apk   []string // must be present
		notDeb     []string // must be absent
		steps      []string // exact, in order
		ports      []int
		notes      []string // substrings
		scriptHas  map[string][]string
		scriptLack map[string][]string
	}{
		{
			name: "pnpm with nvmrc",
			files: files(
				".nvmrc", "v22.11.0\n",
				"package.json", `{"name":"web","engines":{"node":">=18"},"packageManager":"pnpm@9.12.0"}`,
				"pnpm-lock.yaml", "",
			),
			sources: []string{".nvmrc", "package.json"},
			deb:     []string{"curl", "git", "xz-utils"},
			notDeb:  []string{"nodejs"},
			apk:     []string{"nodejs", "npm"},
			steps:   []string{"root:Node.js 22 (official build)@debian", "root:corepack", "pnpm install --frozen-lockfile"},
			notes:   []string{"Alpine: Node.js 22 requested (.nvmrc)"},
			scriptHas: map[string][]string{
				Debian: {"latest-v22.x", "corepack enable", "pnpm install --frozen-lockfile", "apt-get update"},
				Alpine: {"apk add --no-cache", "pnpm install --frozen-lockfile"},
			},
			scriptLack: map[string][]string{Alpine: {"latest-v22.x", "apt-get"}},
		},
		{
			name:  "npm engines lower bound uses the distro node",
			files: files("package.json", `{"engines":{"node":">=18.17"}}`, "package-lock.json", ""),
			deb:   []string{"nodejs", "npm"},
			steps: []string{"npm ci"},
		},
		{
			name: "yarn berry",
			files: files("package.json", `{"packageManager":"yarn@4.5.0"}`, "yarn.lock", "",
				".yarnrc.yml", "nodeLinker: node-modules\n"),
			steps: []string{"root:corepack", "yarn install --immutable"},
		},
		{
			name: "compose django",
			files: files(
				"compose.yaml", composeDjango,
				"requirements.txt", "Django==5.1\npsycopg[binary]\n",
				"requirements-dev.txt", "pytest\n",
			),
			sources: []string{"compose.yaml", "requirements-dev.txt", "requirements.txt"},
			deb:     []string{"postgresql", "redis-server", "python3", "python3-venv"},
			apk:     []string{"postgresql", "redis", "python3", "py3-pip"},
			steps: []string{"root:start postgresql@alpine", "root:start redis@alpine",
				"python venv", "pip install requirements"},
			ports: []int{5432, 8000},
			notes: []string{
				`service "db" sets POSTGRES_USER, POSTGRES_PASSWORD`,
				`service "search" uses image docker.elastic.co/elasticsearch/elasticsearch:8.13.0`,
				`service "web" builds from a Dockerfile`,
			},
			scriptHas: map[string][]string{
				Debian: {".venv/bin/pip install -r requirements-dev.txt -r requirements.txt"},
				Alpine: {"rc-service postgresql setup", "rc-update add redis default"},
			},
			scriptLack: map[string][]string{Debian: {"rc-service", "secret"}},
		},
		{
			name:    "github workflow",
			files:   files(".github/workflows/ci.yml", workflowNode, "package.json", `{}`, "package-lock.json", ""),
			sources: []string{".github/workflows/ci.yml", "package.json"},
			deb:     []string{"libvips-dev", "build-essential", "postgresql"},
			apk:     []string{"build-base", "libvips-dev"},
			steps: []string{"root:Node.js 22 (official build)@debian", "root:start postgresql@alpine",
				"root:ci: npm install -g typescript", "npm ci", "python venv", "ci: pip install pre-commit"},
			notes: []string{
				`CI step "Test" not run (not an install step): npm run lint (+1 more)`,
				"action codecov/codecov-action is not mapped",
				`job "windows" runs on Windows or macOS only`,
				"Alpine: no known mapping for libvips-dev",
			},
		},
		{
			name: "go toolchain",
			files: files("go.mod", "module example.com/app\n\ngo 1.22.0\n\ntoolchain go1.23.4\n\nrequire golang.org/x/sys v0.20.0\n",
				".github/workflows/go.yml", "jobs:\n  b:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.21'\n      - run: go build ./...\n"),
			steps: []string{"root:Go 1.23.4 (official build)", "go mod download"},
			notes: []string{`CI step "go build ./..." not run`},
			scriptHas: map[string][]string{
				Debian: {"v=go1.23.4\n", "https://go.dev/dl/$v.linux-$goarch.tar.gz", "go mod download"},
				Alpine: {"v=go1.23.4\n"},
			},
		},
		{
			name:  "go minor resolves at run time",
			files: files("go.mod", "module m\n\ngo 1.22\n"),
			steps: []string{"root:Go 1.22 (official build)", "go mod download"},
			scriptHas: map[string][]string{
				Debian: {`grep -o '"version": *"go1\.22\.[0-9]*"'`},
			},
		},
		{
			name:  "go before 1.21 is an exact release",
			files: files("go.mod", "module m\n\ngo 1.20\n"),
			scriptHas: map[string][]string{
				Debian: {"v=go1.20\n"},
			},
		},
		{
			name:  "rust with openssl",
			files: files("Cargo.toml", "[package]\nname = \"svc\"\nrust-version = \"1.80\"\n\n[dependencies]\nopenssl = \"0.10\"\ntokio = { version = \"1\", features = [\"full\"] }\n"),
			deb:   []string{"build-essential", "pkg-config", "libssl-dev"},
			apk:   []string{"build-base", "pkgconf", "openssl-dev"},
			steps: []string{"Rust (rustup)", "cargo fetch"},
		},
		{
			name:    "uv project",
			files:   files("pyproject.toml", "[project]\nname = \"x\"\nrequires-python = \">=3.11\"\ndependencies = [\"httpx>=0.27\"]\n", "uv.lock", ""),
			sources: []string{"pyproject.toml"},
			steps:   []string{"uv", "uv sync"},
		},
		{
			name: "pyproject dependencies without a build system",
			files: files("pyproject.toml", `[project]
name = "tool"
requires-python = "==3.12.*"
dependencies = [
  "requests[socks]>=2.31",  # http
  "rich",
]

[tool.ruff]
line-length = 100
`),
			steps: []string{"python venv", "pip install project dependencies"},
			notes: []string{"Python 3.12 requested (pyproject.toml)"},
			scriptHas: map[string][]string{
				Debian: {`.venv/bin/pip install 'requests[socks]>=2.31' 'rich'`},
			},
		},
		{
			name:  "poetry",
			files: files("pyproject.toml", "[tool.poetry]\nname = \"p\"\n\n[tool.poetry.dependencies]\npython = \"^3.11\"\n", "poetry.lock", ""),
			steps: []string{"poetry", "poetry install"},
		},
		{
			name:    "apt.txt",
			files:   files("apt.txt", "# system deps\nlibpq-dev\nffmpeg graphviz\nlibfoo-special1\n"),
			sources: []string{"apt.txt"},
			deb:     []string{"libpq-dev", "ffmpeg", "graphviz", "libfoo-special1"},
			apk:     []string{"libpq-dev", "ffmpeg", "graphviz", "libfoo-special1"},
			notes:   []string{"Alpine: no known mapping for libfoo-special1"},
		},
		{
			name:  "broken yaml",
			files: files("docker-compose.yml", "services:\n  web:\n    image: [unclosed\n"),
			notes: []string{"docker-compose.yml: not valid YAML"},
		},
		{
			name:  "empty project",
			files: files(),
			deb:   []string{"ca-certificates", "curl", "git", "tar", "xz-utils"},
			apk:   []string{"ca-certificates", "curl", "git", "tar", "xz"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := FromFiles(tc.files)
			if tc.sources != nil && !reflect.DeepEqual(p.Sources, tc.sources) {
				t.Errorf("sources = %v, want %v", p.Sources, tc.sources)
			}
			for _, d := range tc.deb {
				if !slices.Contains(p.Packages[Debian], d) {
					t.Errorf("debian packages %v lack %s", p.Packages[Debian], d)
				}
			}
			for _, d := range tc.notDeb {
				if slices.Contains(p.Packages[Debian], d) {
					t.Errorf("debian packages %v have %s", p.Packages[Debian], d)
				}
			}
			for _, a := range tc.apk {
				if !slices.Contains(p.Packages[Alpine], a) {
					t.Errorf("alpine packages %v lack %s", p.Packages[Alpine], a)
				}
			}
			if tc.steps != nil && !reflect.DeepEqual(stepNames(p), tc.steps) {
				t.Errorf("steps = %q, want %q", stepNames(p), tc.steps)
			}
			if tc.ports != nil && !reflect.DeepEqual(p.Ports, tc.ports) {
				t.Errorf("ports = %v, want %v", p.Ports, tc.ports)
			}
			for _, n := range tc.notes {
				if !hasNote(p, n) {
					t.Errorf("no note with %q in %q", n, p.Notes)
				}
			}
			for _, distro := range []string{Debian, Alpine} {
				script, err := Script(p, distro)
				if err != nil {
					t.Fatal(err)
				}
				checkSyntax(t, script)
				for _, s := range tc.scriptHas[distro] {
					if !strings.Contains(script, s) {
						t.Errorf("%s script lacks %q:\n%s", distro, s, script)
					}
				}
				for _, s := range tc.scriptLack[distro] {
					if strings.Contains(script, s) {
						t.Errorf("%s script has %q:\n%s", distro, s, script)
					}
				}
			}
		})
	}
}

// checkSyntax parses the script with the host's sh (-n: no execution).
func checkSyntax(t *testing.T, script string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		return
	}
	cmd := exec.Command(sh, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v: %s\n%s", err, out, script)
	}
}

func TestScriptRejectsUnknownDistro(t *testing.T) {
	if _, err := Script(Plan{}, "fedora"); err == nil {
		t.Fatal("want an error for fedora")
	}
	if _, err := Script(Plan{Steps: []Step{{Name: "x", Run: "echo " + rootMarker}}}, Debian); err == nil {
		t.Fatal("want an error for a step holding the here-document marker")
	}
}

func TestDistroFor(t *testing.T) {
	for in, want := range map[string]string{"": Debian, "debian": Debian, "alpine": Alpine, " Alpine ": Alpine} {
		if got := DistroFor(in); got != want {
			t.Errorf("DistroFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeSpec(t *testing.T) {
	for _, tc := range []struct {
		in   string
		spec string
		min  bool
	}{
		{"22", "22", false},
		{"v20.11.1", "20", false},
		{"^20.11", "20", false},
		{"20.x", "20", false},
		{">=18", "18", true},
		{">= 18.17.0", "18", true},
		{"18 || 20", "20", false},
		{"lts/*", "", false},
		{"", "", false},
	} {
		spec, min := nodeSpec(tc.in)
		if spec != tc.spec || min != tc.min {
			t.Errorf("nodeSpec(%q) = %q, %v; want %q, %v", tc.in, spec, min, tc.spec, tc.min)
		}
	}
}

func TestContainerPort(t *testing.T) {
	p := FromFiles(files("compose.yml", `services:
  a:
    image: nginx
    ports: ["127.0.0.1:8080:80/tcp", "3000", "9000-9001:9000-9001", "bad"]
    expose: ["9229"]
`))
	if want := []int{80, 3000, 9000, 9229}; !reflect.DeepEqual(p.Ports, want) {
		t.Fatalf("ports = %v, want %v", p.Ports, want)
	}
}

func TestIsManifest(t *testing.T) {
	for p, want := range map[string]bool{
		"package.json": true, "requirements-dev.txt": true, ".github/workflows/ci.yaml": true,
		"go.mod": true, "web/package.json": false, ".github/workflows/x/y.yml": false,
		"README.md": false, "Cargo.toml": true, "pnpm-lock.yaml": true,
	} {
		if got := IsManifest(p); got != want {
			t.Errorf("IsManifest(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCollect(t *testing.T) {
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
	write("package.json", "{}")
	write("package-lock.json", strings.Repeat("x", 1000))
	write("README.md", "hi")
	write(".github/workflows/ci.yml", "jobs: {}")
	write("src/go.mod", "module nested")
	got, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{"package.json": []byte("{}"), "package-lock.json": nil, ".github/workflows/ci.yml": []byte("jobs: {}")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect = %q, want %q", got, want)
	}
}
