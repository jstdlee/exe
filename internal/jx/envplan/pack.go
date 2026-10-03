package envplan

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MaxManifestSize caps how much of one manifest a plan reads.
const MaxManifestSize = 1 << 20

// IsManifest reports whether a project-relative slash path is a file the
// plan reads: root-level manifests and lockfiles, and GitHub workflows.
func IsManifest(p string) bool {
	if isWorkflow(p) {
		return true
	}
	if strings.Contains(p, "/") {
		return false
	}
	switch p {
	case "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
		"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", ".yarnrc.yml",
		"bun.lockb", "bun.lock", ".nvmrc", ".node-version",
		"pyproject.toml", "uv.lock", "poetry.lock", "Pipfile", ".python-version",
		"go.mod", "Cargo.toml", "apt.txt", "packages.txt":
		return true
	}
	return strings.HasPrefix(p, "requirements") && strings.HasSuffix(p, ".txt")
}

// PresenceOnly reports whether only a manifest's existence matters (a
// lockfile), so its content need not be read or sent.
func PresenceOnly(p string) bool {
	switch p {
	case "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock",
		"uv.lock", "poetry.lock":
		return true
	}
	return false
}

// Collect reads a project's manifests (IsManifest) from dir. Lockfiles come
// back empty; other files are read up to MaxManifestSize.
func Collect(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	read := func(rel string) error {
		if !IsManifest(rel) {
			return nil
		}
		full := filepath.Join(dir, filepath.FromSlash(rel))
		st, err := os.Stat(full)
		if err != nil || !st.Mode().IsRegular() {
			return nil
		}
		if PresenceOnly(rel) {
			files[rel] = nil
			return nil
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, MaxManifestSize))
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	}
	for _, sub := range []string{"", ".github/workflows"} {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(sub)))
		if err != nil {
			if sub != "" && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if err := read(path.Join(sub, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// ---- upload ----------------------------------------------------------------

// skipDirs are never uploaded: dependencies and build output the guest
// rebuilds itself.
var skipDirs = map[string]bool{"node_modules": true, ".venv": true, "target": true, "__pycache__": true}

// PackOptions tunes WriteTar.
type PackOptions struct {
	// Git includes the .git directory.
	Git bool
}

// PackStats counts what WriteTar wrote.
type PackStats struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// WriteTar writes dir as a tar stream with project-relative names. It skips
// node_modules, .venv, target, __pycache__, .git (unless opt.Git) and what
// the root .gitignore excludes; it keeps symlinks as links and drops other
// special files.
func WriteTar(w io.Writer, dir string, opt PackOptions) (PackStats, error) {
	var st PackStats
	ign := Ignore{}
	if data, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil {
		ign = ParseGitignore(data)
	}
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.Name() == ".git" && !opt.Git || d.IsDir() && skipDirs[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ign.Match(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(full); err != nil {
				return err
			}
		case info.IsDir(), info.Mode().IsRegular():
		default:
			return nil // sockets, devices, pipes
		}
		hdr, err := tar.FileInfoHeader(info, filepath.ToSlash(link))
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(full)
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, f)
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		st.Files++
		st.Bytes += n
		return nil
	})
	if err != nil {
		return st, err
	}
	return st, tw.Close()
}

// ---- .gitignore ------------------------------------------------------------

// Ignore holds the basic rules of a .gitignore: globs, ** segments,
// anchored (leading or inner /) and directory-only (trailing /) patterns,
// and ! negation. The last matching rule wins.
type Ignore struct{ rules []ignoreRule }

type ignoreRule struct {
	segs     []string
	negate   bool
	dirOnly  bool
	anchored bool
}

// ParseGitignore reads .gitignore text.
func ParseGitignore(data []byte) Ignore {
	var ig Ignore
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r ignoreRule
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		line = strings.TrimPrefix(line, `\`) // \# and \! escapes
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			r.anchored, line = true, strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		r.segs = strings.Split(line, "/")
		ig.rules = append(ig.rules, r)
	}
	return ig
}

// Match reports whether rel (a slash path relative to the project root) is
// ignored. Callers walk top-down and skip ignored directories, so a rule
// only has to match the entry itself, not its parents.
func (ig Ignore) Match(rel string, dir bool) bool {
	ignored := false
	segs := strings.Split(rel, "/")
	for _, r := range ig.rules {
		if r.dirOnly && !dir {
			continue
		}
		var ok bool
		if r.anchored {
			ok = matchSegs(r.segs, segs)
		} else {
			ok = len(r.segs) == 1 && globMatch(r.segs[0], segs[len(segs)-1])
		}
		if ok {
			ignored = !r.negate
		}
	}
	return ignored
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	return len(segs) > 0 && globMatch(pat[0], segs[0]) && matchSegs(pat[1:], segs[1:])
}

func globMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}
