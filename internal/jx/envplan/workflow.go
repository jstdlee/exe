package envplan

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func isWorkflow(p string) bool {
	dir, ext := path.Dir(p), path.Ext(p)
	return dir == ".github/workflows" && (ext == ".yml" || ext == ".yaml")
}

type ghWorkflow struct {
	Jobs map[string]ghJob `yaml:"jobs"`
}

type ghJob struct {
	RunsOn   yaml.Node `yaml:"runs-on"`
	Strategy struct {
		Matrix map[string]yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
	Services map[string]composeService `yaml:"services"`
	Steps    []ghStep                  `yaml:"steps"`
}

type ghStep struct {
	Name string               `yaml:"name"`
	Uses string               `yaml:"uses"`
	With map[string]yaml.Node `yaml:"with"`
	Run  string               `yaml:"run"`
}

func (b *builder) parseWorkflow(p string, data []byte) {
	b.source(p)
	var wf ghWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		b.note("%s: not valid YAML (%v)", p, err)
		return
	}
	for _, id := range sortedKeys(wf.Jobs) {
		job := wf.Jobs[id]
		if !job.linux() {
			b.note("%s: job %q runs on Windows or macOS only; skipped", p, id)
			continue
		}
		for _, name := range sortedKeys(job.Services) {
			b.composeService(p, name, job.Services[name], false)
		}
		for _, st := range job.Steps {
			if st.Uses != "" {
				b.setupAction(p, job, st)
			}
			if st.Run != "" {
				b.runStep(p, st)
			}
		}
	}
}

// linux reports whether the job runs on Linux for at least one matrix
// entry; an unresolved runner counts as Linux.
func (j ghJob) linux() bool {
	var runners []string
	collect := func(s string) {
		if vals := j.resolveAll(s); len(vals) > 0 {
			runners = append(runners, vals...)
		} else {
			runners = append(runners, s)
		}
	}
	switch j.RunsOn.Kind {
	case yaml.ScalarNode:
		collect(j.RunsOn.Value)
	case yaml.SequenceNode:
		for _, c := range j.RunsOn.Content {
			collect(c.Value)
		}
	default:
		return true
	}
	for _, r := range runners {
		r = strings.ToLower(r)
		if !strings.Contains(r, "windows") && !strings.Contains(r, "macos") {
			return true
		}
	}
	return false
}

var matrixRef = regexp.MustCompile(`^\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}$`)

// resolveAll returns the matrix values an expression like
// "${{ matrix.node }}" can take; nil when it is not a matrix reference.
func (j ghJob) resolveAll(s string) []string {
	m := matrixRef.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return nil
	}
	n, ok := j.Strategy.Matrix[m[1]]
	if !ok {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return []string{n.Value}
}

// resolve returns a with: value, a matrix reference resolved to its highest
// version, or "" when it stays an unresolved expression.
func (j ghJob) resolve(n yaml.Node) string {
	s := n.Value
	if n.Kind == yaml.SequenceNode && len(n.Content) > 0 {
		s = n.Content[len(n.Content)-1].Value
	}
	if vals := j.resolveAll(s); vals != nil {
		best := ""
		for _, v := range vals {
			if versionLess(leadingVersion(best), leadingVersion(v)) {
				best = v
			}
		}
		return best
	}
	if strings.Contains(s, "${{") {
		return ""
	}
	return s
}

func (b *builder) setupAction(src string, job ghJob, st ghStep) {
	action, _, _ := strings.Cut(st.Uses, "@")
	with := func(key string) string {
		n, ok := st.With[key]
		if !ok {
			return ""
		}
		return job.resolve(n)
	}
	switch strings.ToLower(action) {
	case "actions/setup-node":
		b.nodeWanted = true
		spec := with("node-version")
		if f := with("node-version-file"); spec == "" && f != "" {
			spec = firstLine(b.files[f])
		}
		s, min := nodeSpec(spec)
		b.node.set(s, min, prioCI, src)
	case "actions/setup-python":
		b.pythonWanted = true
		b.python.set(leadingVersion(with("python-version")), false, prioCI, src)
	case "actions/setup-go":
		b.goWanted = true
		v := with("go-version")
		if v == "stable" || v == "oldstable" {
			v = ""
		}
		b.golang.set(strings.TrimSuffix(strings.TrimPrefix(v, "^"), ".x"), false, prioCI, src)
	case "actions/setup-java":
		b.javaWanted = true
		b.java.set(major(leadingVersion(with("java-version"))), false, prioCI, src)
	case "pnpm/action-setup":
		b.nodeWanted, b.pnpmWanted = true, true
	case "astral-sh/setup-uv":
		b.uvWanted = true
	case "dtolnay/rust-toolchain", "actions-rs/toolchain", "actions-rust-lang/setup-rust-toolchain":
		b.rustWanted = true
	case "ruby/setup-ruby":
		b.rubyWanted = true
	case "actions/checkout", "actions/cache", "actions/upload-artifact", "actions/download-artifact",
		"actions/github-script", "actions/setup-dotnet", "docker/setup-buildx-action", "docker/login-action",
		"docker/build-push-action", "docker/setup-qemu-action", "docker/metadata-action":
	default:
		b.note("%s: action %s is not mapped", src, action)
	}
}

// runSegments splits a run: block into simple commands: continuation lines
// joined, then split on newlines, && and ;. Good enough to find install
// commands; nothing here is executed as parsed.
func runSegments(run string) []string {
	run = strings.ReplaceAll(run, "\\\n", " ")
	var out []string
	for _, line := range strings.Split(run, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, a := range strings.Split(line, "&&") {
			for _, seg := range strings.Split(a, ";") {
				if seg = strings.TrimSpace(seg); seg != "" {
					out = append(out, seg)
				}
			}
		}
	}
	return out
}

func (b *builder) runStep(src string, st ghStep) {
	var skipped []string
	for _, seg := range runSegments(st.Run) {
		if !b.installCommand(seg) {
			skipped = append(skipped, seg)
		}
	}
	if len(skipped) == 0 {
		return
	}
	label := st.Name
	if label == "" {
		label = clipText(skipped[0], 60)
	}
	more := ""
	if len(skipped) > 1 {
		more = " (+" + strconv.Itoa(len(skipped)-1) + " more)"
	}
	b.note("%s: CI step %q not run (not an install step): %s%s", src, label, clipText(skipped[0], 80), more)
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// installCommand classifies one CI command. Package installs feed the plan's
// package lists; dependency installs the plan already covers (npm ci, uv
// sync, go mod download, ...) are dropped; other installs (pip install x,
// cargo install y) become steps. It reports false for anything that is not
// an install (tests, builds), which the caller lists as a note.
func (b *builder) installCommand(seg string) bool {
	f := strings.Fields(seg)
	for len(f) > 0 && (f[0] == "sudo" || f[0] == "-E" || strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-")) {
		f = f[1:]
	}
	if len(f) == 0 {
		return true
	}
	args := func(from int) []string {
		var out []string
		for _, a := range f[from:] {
			if !strings.HasPrefix(a, "-") {
				a, _, _ = strings.Cut(a, "=")
				out = append(out, a)
			}
		}
		return out
	}
	cmd := strings.Join(f, " ")
	if strings.Contains(cmd, "${{") {
		return false
	}
	user := func() bool {
		b.userExtra = appendStep(b.userExtra, Step{Name: "ci: " + clipText(cmd, 60), Run: cmd})
		return true
	}
	switch f[0] {
	case "apt-get", "apt", "aptitude":
		if len(f) > 1 && f[1] == "install" {
			b.addDebMapped(args(2)...)
			return true
		}
		return len(f) > 1 && (f[1] == "update" || f[1] == "upgrade")
	case "apk":
		if len(f) > 1 && f[1] == "add" {
			b.addApk(args(2)...)
			return true
		}
		return len(f) > 1 && f[1] == "update"
	case "brew", "choco", "winget":
		return true // other OSes; nothing to do on Linux
	case "corepack":
		b.nodeWanted = true
		return true
	case "npm", "pnpm", "yarn", "bun":
		b.nodeWanted = true
		if f[0] == "pnpm" {
			b.pnpmWanted = true
		}
		if len(f) == 1 && f[0] == "yarn" {
			return true
		}
		if len(f) > 1 && (f[1] == "ci" || f[1] == "install" || f[1] == "i" || f[1] == "add") {
			if hasArg(f, "-g", "--global") {
				b.rootExtra = appendStep(b.rootExtra, Step{Name: "ci: " + clipText(cmd, 60), Root: true, Run: cmd})
				return true
			}
			if f[1] == "ci" || len(args(2)) == 0 {
				return true // project deps: the package manager step covers them
			}
			return user()
		}
		return false
	case "pip", "pip3", "python", "python3", "uv", "poetry", "pipenv":
		b.pythonWanted = true
		if f[0] == "uv" {
			b.uvWanted = true
		}
		rest := f[1:]
		if (f[0] == "python" || f[0] == "python3") && len(rest) >= 2 && rest[0] == "-m" && rest[1] == "pip" {
			rest = rest[2:]
		} else if f[0] == "uv" && len(rest) >= 1 && rest[0] == "pip" {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return false
		}
		if len(rest) >= 2 && rest[0] == "-m" && rest[1] == "venv" {
			return true // the Python step makes the venv
		}
		switch rest[0] {
		case "sync", "lock":
			return f[0] == "uv"
		case "install":
			if f[0] == "poetry" || f[0] == "pipenv" {
				return true
			}
			pkgs := args(len(f) - len(rest) + 1)
			if hasArg(rest, "-r", "--requirement") || hasArg(rest, "-e", "--editable") || onlyTooling(pkgs) {
				return true // project deps or tooling: the Python step covers them
			}
			return user()
		}
		return false
	case "go":
		b.goWanted = true
		if len(f) > 2 && f[1] == "mod" && f[2] == "download" {
			return true
		}
		if len(f) > 2 && f[1] == "install" {
			return user()
		}
		return false
	case "cargo", "rustup":
		b.rustWanted = true
		if f[0] == "rustup" || len(f) > 1 && f[1] == "fetch" {
			return true
		}
		if len(f) > 2 && f[1] == "install" {
			return user()
		}
		return false
	case "gem":
		b.rubyWanted = true
		if len(f) > 2 && f[1] == "install" {
			return user()
		}
		return false
	case "bundle":
		b.rubyWanted = true
		if len(f) > 1 && f[1] == "install" {
			return user()
		}
		return false
	}
	return false
}

// onlyTooling reports whether a pip install names only the project itself
// or packaging tools.
func onlyTooling(pkgs []string) bool {
	for _, p := range pkgs {
		switch strings.ToLower(p) {
		case ".", "pip", "setuptools", "wheel", "uv":
		default:
			return false
		}
	}
	return true
}

func hasArg(f []string, names ...string) bool {
	for _, a := range f {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func appendStep(steps []Step, s Step) []Step {
	for _, x := range steps {
		if x.Run == s.Run && x.Root == s.Root {
			return steps
		}
	}
	return append(steps, s)
}
