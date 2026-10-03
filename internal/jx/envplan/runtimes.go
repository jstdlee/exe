package envplan

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// ---- Node ------------------------------------------------------------------

type packageJSON struct {
	Engines        map[string]string `json:"engines"`
	PackageManager string            `json:"packageManager"`
}

func (b *builder) parsePackageJSON(data []byte) {
	b.source("package.json")
	b.nodeWanted = true
	var pj packageJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		b.note("package.json: not valid JSON (%v)", err)
		return
	}
	if spec, min := nodeSpec(pj.Engines["node"]); spec != "" {
		b.node.set(spec, min, prioRange, "package.json")
	}
}

// nodeManager picks the package manager from packageManager, then the
// lockfile: pnpm, yarn (classic or berry), bun, else npm.
func (b *builder) nodeManager() string {
	pm := ""
	if data, ok := b.files["package.json"]; ok {
		var pj packageJSON
		if json.Unmarshal(data, &pj) == nil {
			pm, _, _ = strings.Cut(pj.PackageManager, "@")
			if pm == "yarn" && !strings.HasPrefix(strings.TrimPrefix(pj.PackageManager, "yarn@"), "1.") {
				pm = "yarn-berry"
			}
		}
	}
	switch {
	case pm != "":
	case b.has("pnpm-lock.yaml"):
		pm = "pnpm"
	case b.has("yarn.lock") && b.has(".yarnrc.yml"):
		pm = "yarn-berry"
	case b.has("yarn.lock"):
		pm = "yarn"
	case b.has("bun.lockb") || b.has("bun.lock"):
		pm = "bun"
	case b.pnpmWanted:
		pm = "pnpm"
	default:
		pm = "npm"
	}
	return pm
}

// Debian 13 ships Node.js 20; a pinned major other than a lower bound gets
// the official build instead, which also brings corepack.
func (b *builder) nodeSteps() []Step {
	var steps []Step
	spec := b.node.Spec
	if spec != "" && !b.node.Min {
		b.addApk("nodejs", "npm")
		b.note("Alpine: Node.js %s requested (%s); the guest gets Alpine's nodejs package, which may be another major", spec, b.node.from)
		steps = append(steps, Step{Name: "Node.js " + spec + " (official build)", Root: true, Distro: Debian, Run: strings.ReplaceAll(`if ! node --version 2>/dev/null | grep -q '^vMAJOR\.'; then
	base=https://nodejs.org/dist/latest-vMAJOR.x
	f=$(curl -fsSL "$base/SHASUMS256.txt" | awk '{print $2}' | grep "linux-$nodearch.tar.xz$")
	curl -fsSL "$base/$f" | tar -xJ -C /usr/local --strip-components=1 --no-same-owner --exclude='*/CHANGELOG.md' --exclude='*/LICENSE' --exclude='*/README.md'
fi`, "MAJOR", spec)})
	} else {
		b.addDeb("nodejs", "npm")
		b.addApk("nodejs", "npm")
	}
	switch b.nodeManager() {
	case "pnpm", "yarn", "yarn-berry":
		steps = append(steps, Step{Name: "corepack", Root: true, Run: "command -v corepack >/dev/null 2>&1 || npm install -g corepack\ncorepack enable"})
	}
	return steps
}

func (b *builder) nodeUserSteps() []Step {
	if !b.has("package.json") {
		return nil
	}
	pm := b.nodeManager()
	run := ""
	switch pm {
	case "pnpm":
		run = "pnpm install"
		if b.has("pnpm-lock.yaml") {
			run += " --frozen-lockfile"
		}
	case "yarn":
		run = "yarn install --frozen-lockfile"
	case "yarn-berry":
		run = "yarn install --immutable"
	case "bun":
		b.note("bun lockfile found: the plan installs with npm; install bun by hand for exact resolution")
		run = "npm install"
	default:
		run = "npm install"
		if b.has("package-lock.json") || b.has("npm-shrinkwrap.json") {
			run = "npm ci"
		}
	}
	return []Step{{Name: run, Run: run}}
}

// ---- Python ----------------------------------------------------------------

var (
	reTOMLSection    = regexp.MustCompile(`(?m)^\s*\[([^\]]+)\]\s*$`)
	reRequiresPython = regexp.MustCompile(`(?m)^\s*requires-python\s*=\s*["']([^"']+)["']`)
	reTOMLString     = regexp.MustCompile(`["']([^"']+)["']`)
)

// tomlSections splits a TOML file into its [section] bodies (top-level
// keys under ""). Enough for the few keys a plan reads.
func tomlSections(data []byte) map[string]string {
	s := string(data)
	out := map[string]string{}
	locs := reTOMLSection.FindAllStringSubmatchIndex(s, -1)
	prev, name := 0, ""
	for _, l := range locs {
		out[name] += s[prev:l[0]]
		name = strings.TrimSpace(s[l[2]:l[3]])
		prev = l[1]
	}
	out[name] += s[prev:]
	return out
}

// tomlArray returns the strings of key = [ ... ] in a section body.
func tomlArray(body, key string) ([]string, bool) {
	re := regexp.MustCompile(`(?ms)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*\[(.*?)\]\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil, false
	}
	var out []string
	for _, line := range strings.Split(m[1], "\n") {
		line, _, _ = strings.Cut(line, "#")
		for _, sm := range reTOMLString.FindAllStringSubmatch(line, -1) {
			out = append(out, sm[1])
		}
	}
	return out, true
}

func (b *builder) parsePyproject(data []byte) {
	b.source("pyproject.toml")
	b.pythonWanted = true
	if m := reRequiresPython.FindStringSubmatch(string(data)); m != nil {
		spec := strings.TrimSpace(m[1])
		min := strings.HasPrefix(spec, ">")
		b.python.set(leadingVersion(strings.TrimLeft(spec, "><=~^! ")), min, prioRange, "pyproject.toml")
	}
}

// pythonSteps install the project's Python deps into ~/work/.venv: with uv
// when the project locks with uv, poetry when it uses poetry, else pip.
func (b *builder) pythonSteps() []Step {
	b.addDeb("python3", "python3-venv", "python3-pip")
	b.addApk("python3", "py3-pip")
	if v := b.python; v.Spec != "" && !v.Min {
		b.note("Python %s requested (%s); the guest uses its distro python3 (uv can fetch others: uv python install %s)", v.Spec, v.from, v.Spec)
	}
	uv := Step{Name: "uv", Run: "command -v uv >/dev/null 2>&1 || curl -LsSf https://astral.sh/uv/install.sh | sh"}
	sections := tomlSections(b.files["pyproject.toml"])
	_, poetry := sections["tool.poetry"]
	switch {
	case b.has("uv.lock"):
		return []Step{uv, {Name: "uv sync", Run: "uv sync"}}
	case b.has("poetry.lock") || poetry && b.has("pyproject.toml"):
		return []Step{
			{Name: "poetry", Run: "command -v poetry >/dev/null 2>&1 || curl -sSL https://install.python-poetry.org | python3 -"},
			{Name: "poetry install", Run: "POETRY_VIRTUALENVS_IN_PROJECT=true poetry install --no-interaction"},
		}
	}
	steps := []Step{{Name: "python venv", Run: "[ -d .venv ] || python3 -m venv .venv"}}
	if b.uvWanted {
		steps = append([]Step{uv}, steps...)
	}
	var reqs []string
	for _, p := range sortedKeys(b.files) {
		if strings.HasPrefix(p, "requirements") && strings.HasSuffix(p, ".txt") && !strings.Contains(p, "/") {
			reqs = append(reqs, "-r "+p)
		}
	}
	if len(reqs) > 0 {
		steps = append(steps, Step{Name: "pip install requirements", Run: ".venv/bin/pip install " + strings.Join(reqs, " ")})
	}
	if _, ok := sections["build-system"]; ok {
		steps = append(steps, Step{Name: "pip install -e .", Run: ".venv/bin/pip install -e ."})
	} else if deps, ok := tomlArray(sections["project"], "dependencies"); ok && len(deps) > 0 {
		steps = append(steps, Step{Name: "pip install project dependencies", Run: ".venv/bin/pip install " + shellWords(deps)})
	}
	if b.has("Pipfile") {
		b.note("Pipfile found: run pipenv install by hand")
	}
	return steps
}

// shellWords single-quotes each word for a POSIX shell.
func shellWords(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// ---- Go --------------------------------------------------------------------

func (b *builder) parseGoMod(data []byte) {
	b.source("go.mod")
	b.goWanted = true
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "go":
			b.golang.set(f[1], false, prioPinFile, "go.mod")
		case "toolchain":
			b.golang.set(strings.TrimPrefix(f[1], "go"), false, prioPinFile+1, "go.mod")
		}
	}
}

var reExactGo = regexp.MustCompile(`^1\.(\d+)(\.\d+|rc\d+|beta\d+)?$`)

// goSteps install the official Go build for the guest's architecture. An
// exact version ("1.23.4"; "1.20" before Go 1.21 named releases that way)
// is fetched as is; "1.22" or no version resolves to the newest matching
// release from go.dev at run time.
func goSteps(v version) []Step {
	want := ""
	if m := reExactGo.FindStringSubmatch(v.Spec); m != nil {
		if minor, _ := strconv.Atoi(m[1]); m[2] != "" || minor < 21 {
			want = "go" + v.Spec
		}
	}
	resolve := `v=` + want
	if want == "" {
		pat := `go[0-9.]*`
		label := "latest"
		if v.Spec != "" {
			pat = "go" + strings.ReplaceAll(v.Spec, ".", `\.`) + `\.[0-9]*`
			label = v.Spec + ".x"
		}
		resolve = `v=$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' | grep -o '"version": *"` + pat + `"' | head -n1 | grep -o 'go[0-9.]*')
[ -n "$v" ] || { echo "no Go release matches ` + label + `" >&2; exit 1; }`
	}
	return []Step{{Name: "Go " + orLatest(v.Spec) + " (official build)", Root: true, Run: resolve + `
if [ "$(/usr/local/go/bin/go env GOVERSION 2>/dev/null)" != "$v" ]; then
	rm -rf /usr/local/go
	curl -fsSL "https://go.dev/dl/$v.linux-$goarch.tar.gz" | tar -xz -C /usr/local
fi
ln -sf /usr/local/go/bin/go /usr/local/go/bin/gofmt /usr/local/bin/`}}
}

func orLatest(s string) string {
	if s == "" {
		return "latest"
	}
	return s
}

// ---- Rust ------------------------------------------------------------------

func (b *builder) parseCargo(data []byte) {
	b.source("Cargo.toml")
	b.rustWanted = true
	s := string(data)
	if strings.Contains(s, "openssl") || strings.Contains(s, "native-tls") {
		b.addDeb("libssl-dev")
		b.addApk("openssl-dev")
	}
}

// ---- Debian -> Alpine package names ----------------------------------------

// debToApk maps common Debian package names to Alpine ones ("" drops the
// package: Alpine needs nothing for it).
var debToApk = map[string]string{
	"build-essential":            "build-base",
	"pkg-config":                 "pkgconf",
	"libssl-dev":                 "openssl-dev",
	"libffi-dev":                 "libffi-dev",
	"zlib1g-dev":                 "zlib-dev",
	"libpq-dev":                  "libpq-dev",
	"libsqlite3-dev":             "sqlite-dev",
	"sqlite3":                    "sqlite",
	"libxml2-dev":                "libxml2-dev",
	"libxslt1-dev":               "libxslt-dev",
	"libjpeg-dev":                "libjpeg-turbo-dev",
	"libpng-dev":                 "libpng-dev",
	"libyaml-dev":                "yaml-dev",
	"libreadline-dev":            "readline-dev",
	"libcurl4-openssl-dev":       "curl-dev",
	"libbz2-dev":                 "bzip2-dev",
	"liblzma-dev":                "xz-dev",
	"libncurses-dev":             "ncurses-dev",
	"libgl1":                     "mesa-gl",
	"libglib2.0-0":               "glib",
	"python3-dev":                "python3-dev",
	"python3-pip":                "py3-pip",
	"python3-venv":               "",
	"python-is-python3":          "",
	"xz-utils":                   "xz",
	"default-jdk":                "openjdk21-jdk",
	"default-jre":                "openjdk21-jre",
	"redis-server":               "redis",
	"redis-tools":                "redis",
	"postgresql-client":          "postgresql-client",
	"golang":                     "go",
	"golang-go":                  "go",
	"ripgrep":                    "ripgrep",
	"fd-find":                    "fd",
	"chromium":                   "chromium",
	"netcat-openbsd":             "netcat-openbsd",
	"dnsutils":                   "bind-tools",
	"iputils-ping":               "iputils",
	"procps":                     "procps",
	"locales":                    "",
	"software-properties-common": "",
	"apt-transport-https":        "",
	"gnupg":                      "gnupg",
	"lsb-release":                "",
}

func init() {
	// identity mappings for names both distros share
	for _, p := range []string{"git", "curl", "wget", "make", "gcc", "g++", "cmake", "unzip", "zip", "jq",
		"ffmpeg", "imagemagick", "graphviz", "nodejs", "npm", "python3", "ruby", "openssl", "rsync",
		"tmux", "vim", "less", "file", "patch", "bash", "ca-certificates", "tar", "gzip", "bzip2",
		"nginx", "memcached", "htop", "strace", "autoconf", "automake", "libtool"} {
		if _, ok := debToApk[p]; !ok {
			debToApk[p] = p
		}
	}
}
