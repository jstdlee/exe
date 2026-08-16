package agentenv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Manifest is a dependency recipe turned into a bootstrap plan.
// It is never executed as Docker.
type Manifest struct {
	Source   string   `json:"source"`
	Kind     string   `json:"kind"` // compose | github | pyproject | text
	Packages []string `json:"packages"`
	Images   []string `json:"images,omitempty"`
	Services []string `json:"services,omitempty"`
	Python   []string `json:"python,omitempty"`
	Prompt   string   `json:"prompt"`
}

var (
	reImage      = regexp.MustCompile(`(?m)^\s*image:\s*["']?([^\s"']+)`)
	reService    = regexp.MustCompile(`(?m)^  ([A-Za-z0-9._-]+):\s*$`)
	reSetupAct   = regexp.MustCompile(`setup-(python|node|go|java|dotnet)@`)
	reApt        = regexp.MustCompile(`apt-get\s+install(?:\s+-y)?\s+([^\n\\]+)`)
	rePip        = regexp.MustCompile(`(?:pip3?|uv pip)\s+install\s+([^\n\\]+)`)
	rePyDep      = regexp.MustCompile(`(?m)^\s*["']([A-Za-z0-9_.-]+)(?:[<>=!~].*)?["']`)
	reRequiresPy = regexp.MustCompile(`requires-python\s*=\s*["']([^"']+)["']`)
	rePkgToken   = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9.+_-]*`)
)

// ParseManifestFile reads one Manifest from disk (or "-" for stdin via ParseManifest).
func ParseManifestFile(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(path, string(b)), nil
}

// ParseManifest classifies raw text and extracts a bootstrap plan.
func ParseManifest(name, raw string) Manifest {
	base := strings.ToLower(filepath.Base(name))
	m := Manifest{Source: name}
	switch {
	case strings.Contains(base, "compose") || strings.Contains(raw, "\nservices:"):
		m.Kind = "compose"
		parseCompose(&m, raw)
	case strings.Contains(base, "pyproject") || strings.Contains(raw, "[project]"):
		m.Kind = "pyproject"
		parsePyproject(&m, raw)
	case strings.Contains(base, ".yml") || strings.Contains(base, ".yaml") || strings.Contains(raw, "runs-on:") || strings.Contains(raw, "actions/"):
		m.Kind = "github"
		parseGitHub(&m, raw)
	default:
		m.Kind = "text"
		parseText(&m, raw)
	}
	m.Packages = uniq(m.Packages)
	m.Images = uniq(m.Images)
	m.Services = uniq(m.Services)
	m.Python = uniq(m.Python)
	m.Prompt = renderManifestPrompt(m)
	return m
}

func parseCompose(m *Manifest, raw string) {
	for _, sm := range reImage.FindAllStringSubmatch(raw, -1) {
		m.Images = append(m.Images, sm[1])
		m.Packages = append(m.Packages, imageToDeb(sm[1])...)
	}
	inServices := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "services:" {
			inServices = true
			continue
		}
		if inServices && len(line) > 0 && line[0] != ' ' && line[0] != '\t' && strings.Contains(line, ":") {
			inServices = false
		}
		if inServices {
			if sm := reService.FindStringSubmatch(line); sm != nil {
				m.Services = append(m.Services, sm[1])
			}
		}
	}
}

func parseGitHub(m *Manifest, raw string) {
	for _, sm := range reSetupAct.FindAllStringSubmatch(raw, -1) {
		switch sm[1] {
		case "python":
			m.Packages = append(m.Packages, "python3", "python3-pip", "python3-venv")
		case "node":
			m.Packages = append(m.Packages, "nodejs", "npm")
		case "go":
			m.Packages = append(m.Packages, "golang")
		case "java":
			m.Packages = append(m.Packages, "default-jdk")
		}
	}
	for _, sm := range reApt.FindAllStringSubmatch(raw, -1) {
		m.Packages = append(m.Packages, tokens(sm[1])...)
	}
	for _, sm := range rePip.FindAllStringSubmatch(raw, -1) {
		m.Python = append(m.Python, tokens(sm[1])...)
	}
}

func parsePyproject(m *Manifest, raw string) {
	m.Packages = append(m.Packages, "python3", "python3-pip", "python3-venv")
	if sm := reRequiresPy.FindStringSubmatch(raw); sm != nil {
		m.Prompt = "Python " + sm[1]
	}
	// only the first dependencies = [ ... ] list
	if i := strings.Index(raw, "dependencies"); i >= 0 {
		chunk := raw[i:]
		if a, b := strings.Index(chunk, "["), strings.Index(chunk, "]"); a >= 0 && b > a {
			for _, sm := range rePyDep.FindAllStringSubmatch(chunk[a:b+1], -1) {
				m.Python = append(m.Python, sm[1])
			}
		}
	}
}

func parseText(m *Manifest, raw string) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.Packages = append(m.Packages, tokens(line)...)
	}
}

func imageToDeb(image string) []string {
	low := strings.ToLower(image)
	switch {
	case strings.Contains(low, "python"):
		return []string{"python3", "python3-pip", "python3-venv"}
	case strings.Contains(low, "node"):
		return []string{"nodejs", "npm"}
	case strings.Contains(low, "golang"), strings.Contains(low, "go:"):
		return []string{"golang"}
	case strings.Contains(low, "postgres"):
		return []string{"postgresql-client"}
	case strings.Contains(low, "redis"):
		return []string{"redis-tools"}
	case strings.Contains(low, "nginx"):
		return []string{"nginx"}
	default:
		return nil
	}
}

func tokens(s string) []string {
	var out []string
	for _, t := range rePkgToken.FindAllString(s, -1) {
		switch strings.ToLower(t) {
		case "y", "yes", "qq", "quiet", "install", "apt", "get", "pip", "pip3", "uv", "and":
			continue
		}
		out = append(out, t)
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func renderManifestPrompt(m Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Manifest kind=%s source=%s\n", m.Kind, m.Source)
	if len(m.Services) > 0 {
		fmt.Fprintf(&b, "Services: %s\n", strings.Join(m.Services, ", "))
	}
	if len(m.Images) > 0 {
		fmt.Fprintf(&b, "Images (do not pull; install Debian equivalents): %s\n", strings.Join(m.Images, ", "))
	}
	if len(m.Packages) > 0 {
		fmt.Fprintf(&b, "apt packages: %s\n", strings.Join(m.Packages, " "))
	}
	if len(m.Python) > 0 {
		fmt.Fprintf(&b, "pip packages: %s\n", strings.Join(m.Python, " "))
	}
	return strings.TrimSpace(b.String())
}

// MergeManifests concatenates several Manifests into one plan.
func MergeManifests(ms []Manifest) Manifest {
	out := Manifest{Kind: "merged", Source: "merged"}
	for _, m := range ms {
		out.Packages = append(out.Packages, m.Packages...)
		out.Images = append(out.Images, m.Images...)
		out.Services = append(out.Services, m.Services...)
		out.Python = append(out.Python, m.Python...)
		if out.Source == "merged" {
			out.Source = m.Source
		} else {
			out.Source += "," + m.Source
		}
	}
	out.Packages = uniq(out.Packages)
	out.Images = uniq(out.Images)
	out.Services = uniq(out.Services)
	out.Python = uniq(out.Python)
	out.Prompt = renderManifestPrompt(out)
	return out
}
