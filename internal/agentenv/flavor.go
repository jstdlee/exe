// Package agentenv is the portable-agent-environment layer: Flavors,
// Manifests, Jobs, Snapshots, and one-shot delivery tokens.
package agentenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Flavor is a 发行版 definition: one base image and the init prompt
// that configures a development Environment from a Manifest.
type Flavor struct {
	Name      string `json:"name"`
	ImageURL  string `json:"image_url"`
	KernelURL string `json:"kernel_url,omitempty"`
	CPUs      int    `json:"cpus"`
	MemoryMB  int    `json:"memory_mb"`
	DiskGB    int    `json:"disk_gb"`
	// InitPrompt is written into the guest at init and used as the system
	// preamble for Jobs that carry a --prompt.
	InitPrompt string `json:"init_prompt"`
}

// Debian is the only Flavor in this map.
func Debian() Flavor {
	return Flavor{
		Name:     "debian",
		ImageURL: "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.raw",
		CPUs:     2,
		MemoryMB: 2048,
		DiskGB:   8,
		InitPrompt: `You are configuring a Debian 13 Environment for an agent.
Install only what the Manifest needs. Prefer apt packages. Put project
files under ~/work. Write what you did to ~/BOOTSTRAP.md. Do not touch
the host. Sign-in for claude/codex/opencode/pi stays inside this VM.`,
	}
}

// LoadFlavor reads a JSON or simple YAML flavor file. Unknown names fall
// back to Debian. "debian" (no path) is always the built-in Flavor.
func LoadFlavor(nameOrPath string) (Flavor, error) {
	if nameOrPath == "" || nameOrPath == "debian" {
		return Debian(), nil
	}
	b, err := os.ReadFile(nameOrPath)
	if err != nil {
		// also try flavors/<name>.yaml next to the exe app
		alt := filepath.Join(filepath.Dir(nameOrPath), nameOrPath)
		b, err = os.ReadFile(alt)
		if err != nil {
			return Flavor{}, fmt.Errorf("flavor %q: %w", nameOrPath, err)
		}
	}
	f := Debian()
	f.Name = strings.TrimSuffix(filepath.Base(nameOrPath), filepath.Ext(nameOrPath))
	applyFlavorKV(string(b), &f)
	if f.Name == "" {
		f.Name = "debian"
	}
	return f, nil
}

func applyFlavorKV(raw string, f *Flavor) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.ToLower(k))
		v = strings.TrimSpace(strings.Trim(v, `"'`))
		switch k {
		case "name":
			f.Name = v
		case "image_url", "image-url":
			if v != "" {
				f.ImageURL = v
			}
		case "kernel_url", "kernel-url":
			f.KernelURL = v
		case "cpus":
			fmt.Sscanf(v, "%d", &f.CPUs)
		case "memory_mb", "memory-mb":
			fmt.Sscanf(v, "%d", &f.MemoryMB)
		case "disk_gb", "disk-gb":
			fmt.Sscanf(v, "%d", &f.DiskGB)
		}
	}
	// multi-line init_prompt: |
	if i := strings.Index(raw, "init_prompt:"); i >= 0 {
		rest := raw[i+len("init_prompt:"):]
		rest = strings.TrimLeft(rest, " |\t")
		var lines []string
		for _, line := range strings.Split(rest, "\n") {
			if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.Contains(line, ":") {
				break
			}
			lines = append(lines, strings.TrimRight(line, "\r"))
		}
		if p := strings.TrimSpace(strings.Join(lines, "\n")); p != "" {
			f.InitPrompt = p
		}
	}
}
