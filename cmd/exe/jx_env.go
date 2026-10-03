package main

import (
	"archive/tar"
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"exe/internal/config"
	"exe/internal/jx/envplan"
	"exe/internal/server"
)

func init() {
	jxCommands["env"] = cmdEnv
	jxUsageLines["env"] = `  exe env plan [dir] [-image debian|alpine] [-json]   print a bootstrap script for the project
  exe env up <vm> [dir] [-image I] [-mem MB] [-git]    create/start the VM, upload the project, bootstrap it
  exe env run <vm> [-o path]... [-dest dir] -- <cmd>   run a job in ~/work, copy outputs back`
}

const envUsage = `exe env — project environments in VMs

  exe env plan [dir] [-image debian|alpine] [-json]
      Read the project's manifests (compose, GitHub workflows, package.json,
      pyproject.toml, requirements*.txt, go.mod, Cargo.toml, apt.txt) and
      print a guest bootstrap script; notes go to stderr.
  exe env up <vm> [dir] [-image debian|alpine] [-mem MB] [-cpus N] [-disk GB] [-git]
      Create the VM if missing (else start it), upload the project to ~/work
      (skips .git unless -git, node_modules, .venv, target and what the root
      .gitignore excludes) and run the bootstrap.
  exe env run <vm> [-o path]... [-dest dir] -- <command>
      Run a command in ~/work, stream its output, then copy each -o path
      (relative to ~/work) back under -dest (default .). Exits with the
      command's status.
`

func cmdEnv(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(envUsage)
		return nil
	}
	switch args[0] {
	case "plan":
		return cmdEnvPlan(args[1:])
	case "up":
		return cmdEnvUp(args[1:])
	case "run":
		return cmdEnvRun(args[1:])
	}
	return fmt.Errorf("unknown env command %q\n\n%s", args[0], envUsage)
}

// parseInterspersed parses flags that may come before, between or after
// positional arguments, and returns the positional ones.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func cmdEnvPlan(args []string) error {
	fs := flag.NewFlagSet("env plan", flag.ContinueOnError)
	image := fs.String("image", "", "guest image: debian (default) or alpine")
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	files, err := envplan.Collect(dir)
	if err != nil {
		return err
	}
	req := server.EnvPlanRequest{Files: map[string]string{}, Image: *image}
	for p, data := range files {
		req.Files[p] = string(data)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var res server.EnvPlanResponse
	resp, err := api(cfg, "POST", "/v1/jx/env/plan", req, time.Minute)
	if err != nil {
		// planning is pure: without a daemon, plan here
		fmt.Fprintf(os.Stderr, "(%v; planning locally)\n", err)
		res.Plan = envplan.FromFiles(files)
		res.Distro = envplan.DistroFor(*image)
		if res.Script, err = envplan.Script(res.Plan, res.Distro); err != nil {
			return err
		}
	} else if err := decodeInto(resp, &res); err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Print(res.Script)
	printPlanSummary(os.Stderr, res.Plan, "")
	return nil
}

func printPlanSummary(w io.Writer, p envplan.Plan, vm string) {
	if len(p.Sources) == 0 {
		fmt.Fprintln(w, "plan: no manifests found; base tools only")
	} else {
		fmt.Fprintf(w, "plan: from %s\n", strings.Join(p.Sources, ", "))
	}
	for _, n := range p.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if len(p.Ports) > 0 {
		ports := make([]string, len(p.Ports))
		for i, port := range p.Ports {
			ports[i] = strconv.Itoa(port)
		}
		if vm == "" {
			vm = "<vm>"
		}
		fmt.Fprintf(w, "ports: %s (publish one with: exe expose %s -port %d)\n", strings.Join(ports, ", "), vm, p.Ports[0])
	}
}

// jxPost sends a streaming request to the daemon: no client timeout, any
// body and content type.
func jxPost(cfg *config.Config, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", "http://"+displayAddr(cfg.Listen)+path, body)
	if err != nil {
		return nil, err
	}
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return nil, fmt.Errorf("cannot reach the exe daemon at %s — is `exe serve` running?", displayAddr(cfg.Listen))
		}
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, decodeInto(resp, nil)
	}
	return resp, nil
}

// envStream prints an NDJSON env stream and returns the done code. tarOut,
// when set, receives the decoded tar chunks.
func envStream(resp *http.Response, vm string, tarOut io.Writer) (int, error) {
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var failed []string
	for sc.Scan() {
		var ev server.EnvEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return -1, fmt.Errorf("bad stream line: %v", err)
		}
		switch ev.Type {
		case "status":
			fmt.Fprintf(os.Stderr, "==> %s\n", ev.Text)
		case "stdout":
			io.WriteString(os.Stdout, ev.Text)
		case "stderr":
			io.WriteString(os.Stderr, ev.Text)
		case "plan":
			if ev.Plan != nil {
				printPlanSummary(os.Stderr, *ev.Plan, vm)
			}
		case "error":
			failed = append(failed, ev.Error)
			fmt.Fprintf(os.Stderr, "error: %s\n", ev.Error)
		case "tar":
			if tarOut == nil {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(ev.Data)
			if err != nil {
				return -1, fmt.Errorf("bad output chunk: %v", err)
			}
			if _, err := tarOut.Write(data); err != nil {
				return -1, fmt.Errorf("copy outputs: %v", err)
			}
		case "done":
			code := -1
			if ev.Code != nil {
				code = *ev.Code
			}
			return code, nil
		}
	}
	if err := sc.Err(); err != nil {
		return -1, err
	}
	if len(failed) > 0 {
		return -1, errors.New(failed[len(failed)-1])
	}
	return -1, errors.New("the daemon closed the stream early")
}

func cmdEnvUp(args []string) error {
	fs := flag.NewFlagSet("env up", flag.ContinueOnError)
	image := fs.String("image", "", "base image for a new VM: debian (default) or alpine")
	mem := fs.Int("mem", 0, "memory MB for a new VM")
	cpus := fs.Int("cpus", 0, "vCPUs for a new VM")
	disk := fs.Int("disk", 0, "disk GB for a new VM")
	git := fs.Bool("git", false, "upload the .git directory too")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || len(pos) > 2 {
		return fmt.Errorf("usage: exe env up <vm> [dir] [-image debian|alpine] [-mem MB] [-git]")
	}
	vm, dir := pos[0], "."
	if len(pos) == 2 {
		dir = pos[1]
	}
	if st, err := os.Stat(dir); err != nil {
		return err
	} else if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	q := url.Values{"vm": {vm}}
	if *image != "" {
		q.Set("image", *image)
	}
	for k, v := range map[string]int{"mem": *mem, "cpus": *cpus, "disk": *disk} {
		if v > 0 {
			q.Set(k, strconv.Itoa(v))
		}
	}
	pr, pw := io.Pipe()
	go func() {
		_, err := envplan.WriteTar(pw, dir, envplan.PackOptions{Git: *git})
		pw.CloseWithError(err)
	}()
	fmt.Fprintf(os.Stderr, "==> packing %s\n", dir)
	resp, err := jxPost(cfg, "/v1/jx/env/up?"+q.Encode(), "application/x-tar", pr)
	pr.Close()
	if err != nil {
		return err
	}
	code, err := envStream(resp, vm, nil)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("bootstrap failed (exit %d); rerun it with: exe ssh %s sh .exe-env/bootstrap.sh", code, vm)
	}
	fmt.Fprintf(os.Stderr, "==> %s is ready: exe ssh %s, or exe env run %s -- <command>\n", vm, vm, vm)
	return nil
}

func cmdEnvRun(args []string) error {
	var cmdWords []string
	for i, a := range args {
		if a == "--" {
			args, cmdWords = args[:i], args[i+1:]
			break
		}
	}
	fs := flag.NewFlagSet("env run", flag.ContinueOnError)
	var outputs []string
	fs.Func("o", "copy this path (relative to ~/work) back after the run; repeatable", func(s string) error {
		outputs = append(outputs, s)
		return nil
	})
	dest := fs.String("dest", ".", "local directory for -o outputs")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: exe env run <vm> [-o path]... -- <command>")
	}
	vm := pos[0]
	command := strings.Join(append(pos[1:], cmdWords...), " ")
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("usage: exe env run <vm> [-o path]... -- <command>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	body, err := json.Marshal(server.EnvRunRequest{VM: vm, Command: command, Outputs: outputs})
	if err != nil {
		return err
	}
	resp, err := jxPost(cfg, "/v1/jx/env/run", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	var tarOut io.Writer
	var pw *io.PipeWriter
	extracted := make(chan error, 1)
	if len(outputs) > 0 {
		if err := os.MkdirAll(*dest, 0o755); err != nil {
			resp.Body.Close()
			return err
		}
		var pr *io.PipeReader
		pr, pw = io.Pipe()
		tarOut = pw
		go func() {
			err := extractTar(pr, *dest)
			pr.CloseWithError(err)
			extracted <- err
		}()
	} else {
		extracted <- nil
	}
	code, err := envStream(resp, vm, tarOut)
	if pw != nil {
		pw.Close()
	}
	if xerr := <-extracted; xerr != nil && err == nil {
		err = fmt.Errorf("copy outputs: %w", xerr)
	}
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// extractTar unpacks the outputs stream under dest. os.Root keeps every
// entry, symlinks included, inside dest.
func extractTar(r io.Reader, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if name == "." {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("refusing %q", h.Name)
		}
		local := filepath.FromSlash(name)
		if dir := filepath.Dir(local); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(local, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := root.OpenFile(local, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "==> %s\n", filepath.Join(dest, local))
		case tar.TypeSymlink:
			root.Remove(local)
			if err := root.Symlink(h.Linkname, local); err != nil {
				return err
			}
		}
	}
}
