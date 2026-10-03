// Package envplan turns a project's manifests (compose files, GitHub Actions
// workflows, package.json, pyproject.toml, go.mod, Cargo.toml, apt.txt, ...)
// into a bootstrap plan for a fresh guest, and renders that plan as a shell
// script for Debian (apt) or Alpine (apk). Everything here is pure: the
// input is a map of project-relative paths to file contents.
package envplan

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The guest distributions a plan renders for.
const (
	Debian = "debian"
	Alpine = "alpine"
)

// DistroFor maps a VM image name (vmm.Info.Image: "", "debian", "alpine")
// to the distribution whose package manager the script uses.
func DistroFor(image string) string {
	if strings.EqualFold(strings.TrimSpace(image), Alpine) {
		return Alpine
	}
	return Debian
}

// Plan is what a guest needs before the project can build and run.
type Plan struct {
	// Sources are the files the plan was built from.
	Sources []string `json:"sources"`
	// Packages are system packages per distribution (Debian, Alpine).
	Packages map[string][]string `json:"packages"`
	// Steps run in order after the packages are installed.
	Steps []Step `json:"steps"`
	// Ports are ports the project's services listen on (compose).
	Ports []int `json:"ports,omitempty"`
	// Notes say what the plan could not map, or what to check by hand.
	Notes []string `json:"notes,omitempty"`
}

// Step is one shell snippet of the bootstrap.
type Step struct {
	Name string `json:"name"`
	// Root steps run as root; the others run as the VM's user in ~/work.
	Root bool `json:"root,omitempty"`
	// Distro limits the step to one distribution; "" means all.
	Distro string `json:"distro,omitempty"`
	Run    string `json:"run"`
}

// version is a runtime version request: Spec is the major/minor text
// ("22", "3.12", "1.23.1"), Min marks a lower bound (">=18") that any
// newer distro build satisfies.
type version struct {
	Spec string
	Min  bool
	prio int
	from string
}

// set keeps the request with the highest priority (an explicit pin file
// beats a CI setup step, which beats an image tag or an engines range).
func (v *version) set(spec string, min bool, prio int, from string) {
	if spec == "" || (v.Spec != "" && prio <= v.prio) {
		return
	}
	*v = version{Spec: spec, Min: min, prio: prio, from: from}
}

// Version source priorities.
const (
	prioRange   = 1 // engines, requires-python
	prioImage   = 2 // compose image tag
	prioCI      = 3 // setup-* action in CI
	prioPinFile = 4 // .nvmrc, go.mod, .python-version
)

type builder struct {
	files   map[string][]byte
	sources map[string]bool
	deb     map[string]bool
	apk     map[string]bool
	guessed map[string]bool // Alpine names guessed from Debian ones

	node, python, golang, java version

	// what the project needs, from any source
	nodeWanted, pythonWanted, goWanted, rustWanted, javaWanted, rubyWanted bool
	uvWanted, pnpmWanted                                                   bool

	services  map[string]service // alpine openrc services to enable
	ports     map[int]bool
	rootExtra []Step // CI root installs (npm -g, ...)
	userExtra []Step // CI user installs (pip install x, cargo install y)
	notes     []string
	noteSeen  map[string]bool
}

func (b *builder) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	if !b.noteSeen[n] {
		b.noteSeen[n] = true
		b.notes = append(b.notes, n)
	}
}

func (b *builder) source(p string) { b.sources[p] = true }

func (b *builder) addDeb(pkgs ...string) {
	for _, p := range pkgs {
		b.deb[p] = true
	}
}

func (b *builder) addApk(pkgs ...string) {
	for _, p := range pkgs {
		b.apk[p] = true
	}
}

// addDebMapped adds Debian package names (apt.txt, apt-get install in CI)
// and their Alpine equivalents.
func (b *builder) addDebMapped(pkgs ...string) {
	for _, p := range pkgs {
		b.deb[p] = true
		if a, ok := debToApk[p]; ok {
			for _, x := range strings.Fields(a) {
				b.apk[x] = true
			}
			continue
		}
		b.apk[p] = true
		b.guessed[p] = true
	}
}

// FromFiles builds a plan from project files, keyed by slash-separated
// paths relative to the project root.
func FromFiles(files map[string][]byte) Plan {
	b := &builder{
		files: files, sources: map[string]bool{},
		deb: map[string]bool{}, apk: map[string]bool{}, guessed: map[string]bool{},
		services: map[string]service{}, ports: map[int]bool{}, noteSeen: map[string]bool{},
	}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	// Pin files first so their notes come before CI's; the version
	// priorities make the order irrelevant for the outcome.
	for _, p := range names {
		switch base := path.Base(p); {
		case p == ".nvmrc" || p == ".node-version":
			b.source(p)
			b.nodeWanted = true
			spec, min := nodeSpec(firstLine(files[p]))
			b.node.set(spec, min, prioPinFile, p)
		case p == ".python-version":
			b.source(p)
			b.pythonWanted = true
			b.python.set(leadingVersion(firstLine(files[p])), false, prioPinFile, p)
		case isCompose(p):
			b.parseCompose(p, files[p])
		case isWorkflow(p):
			b.parseWorkflow(p, files[p])
		case p == "package.json":
			b.parsePackageJSON(files[p])
		case p == "pyproject.toml":
			b.parsePyproject(files[p])
		case p == "go.mod":
			b.parseGoMod(files[p])
		case p == "Cargo.toml":
			b.parseCargo(files[p])
		case p == "apt.txt" || p == "packages.txt":
			b.source(p)
			b.addDebMapped(listFile(files[p])...)
		case p == base && strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
			b.source(p)
			b.pythonWanted = true
		}
	}
	return b.plan()
}

func firstLine(data []byte) string {
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(line)
}

// listFile reads one-package-per-line text (apt.txt): comments and blank
// lines skipped, several names per line allowed.
func listFile(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "#")
		out = append(out, strings.Fields(line)...)
	}
	return out
}

// leadingVersion returns the dotted number at the start of s ("3.12-slim"
// -> "3.12", "v20.11.1" -> "20.11.1"); "" when s has none.
func leadingVersion(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	return strings.Trim(s[:end], ".")
}

// major returns the first number of a dotted version.
func major(v string) string {
	m, _, _ := strings.Cut(v, ".")
	return m
}

// nodeSpec reads a Node version request ("22", "v20.11.1", "^20", "20.x",
// ">=18", "18 || 20", "lts/*") into a major version and whether it is
// only a lower bound. Aliases ("lts/*", "node") give "".
func nodeSpec(s string) (spec string, min bool) {
	s = strings.TrimSpace(s)
	if alts := strings.Split(s, "||"); len(alts) > 1 {
		best := ""
		for _, a := range alts {
			if m, _ := nodeSpec(a); versionLess(best, m) {
				best = m
			}
		}
		return best, false
	}
	if strings.HasPrefix(s, ">") {
		return major(leadingVersion(strings.TrimLeft(s, ">= "))), true
	}
	s = strings.TrimLeft(s, "^~=v ")
	return major(leadingVersion(s)), false
}

// versionLess compares dotted versions numerically; "" sorts first.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	if a == "" {
		return b != ""
	}
	for i := 0; i < len(as) || i < len(bs); i++ {
		if i >= len(as) {
			return true
		}
		if i >= len(bs) {
			return false
		}
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (b *builder) has(p string) bool { _, ok := b.files[p]; return ok }

func (b *builder) plan() Plan {
	p := Plan{Packages: map[string][]string{}}
	b.addDeb("ca-certificates", "curl", "git", "tar", "xz-utils")
	b.addApk("ca-certificates", "curl", "git", "tar", "xz")
	var root, user []Step

	if b.nodeWanted {
		root = append(root, b.nodeSteps()...)
		user = append(user, b.nodeUserSteps()...)
	}
	if b.pythonWanted || b.uvWanted {
		user = append(user, b.pythonSteps()...)
	}
	if b.goWanted {
		root = append(root, goSteps(b.golang)...)
		if b.has("go.mod") {
			user = append(user, Step{Name: "go mod download", Run: "go mod download"})
		}
	}
	if b.rustWanted {
		b.addDeb("build-essential", "pkg-config")
		b.addApk("build-base", "pkgconf")
		user = append(user, Step{Name: "Rust (rustup)", Run: "command -v rustup >/dev/null 2>&1 || curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal"})
		if b.has("Cargo.toml") {
			user = append(user, Step{Name: "cargo fetch", Run: "cargo fetch"})
		}
	}
	if b.javaWanted {
		if m := major(b.java.Spec); m != "" {
			b.addDeb("openjdk-" + m + "-jdk-headless")
			b.addApk("openjdk" + m + "-jdk")
		} else {
			b.addDeb("default-jdk-headless")
			b.addApk("openjdk21-jdk")
		}
	}
	if b.rubyWanted {
		b.addDeb("ruby-full")
		b.addApk("ruby", "ruby-dev")
	}
	for _, name := range sortedKeys(b.services) {
		root = append(root, b.services[name].alpineStep())
	}
	root = append(root, b.rootExtra...)
	user = append(user, b.userExtra...)
	p.Steps = append(root, user...)

	p.Packages[Debian] = sortedKeys(b.deb)
	p.Packages[Alpine] = sortedKeys(b.apk)
	if len(b.guessed) > 0 {
		b.note("Alpine: no known mapping for %s; the script tries the Debian name", strings.Join(sortedKeys(b.guessed), ", "))
	}
	p.Sources = sortedKeys(b.sources)
	for port := range b.ports {
		p.Ports = append(p.Ports, port)
	}
	sort.Ints(p.Ports)
	p.Notes = b.notes
	return p
}
