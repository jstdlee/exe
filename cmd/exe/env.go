package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"exe/internal/agentenv"
	"exe/internal/config"
)

const envUsage = `exe env — portable agent Environment

  exe env ls
  exe env init NAME [--flavor debian] [--from FILE]...
  exe env stop NAME
  exe env rm NAME
  exe env snap NAME create [label]
  exe env snap NAME ls
  exe env snap NAME restore ID
  exe env snap NAME rm ID
  exe env run NAME [--script FILE] [--cmd CMD] [--file FILE]... [--prompt TEXT] [--session ID] [--json]

init reads Manifests (compose / GitHub YAML / pyproject / text) and bootstraps
Debian inside the VM. run injects files and a command or script, then prints
shell output, optional agent output, and a one-shot download URL.
`

func cmdEnv(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(envUsage)
		return nil
	}
	switch args[0] {
	case "ls":
		return cmdLs()
	case "init":
		return cmdEnvInit(args[1:])
	case "stop":
		return cmdSimpleVM(args[1:], "stop")
	case "rm", "destroy":
		return cmdRm(args[1:])
	case "snap":
		return cmdEnvSnap(args[1:])
	case "run":
		return cmdEnvRun(args[1:])
	default:
		return fmt.Errorf("unknown env command %q\n\n%s", args[0], envUsage)
	}
}

func cmdEnvInit(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return fmt.Errorf("usage: exe env init NAME [--flavor debian] [--from FILE]...")
	}
	fs := flag.NewFlagSet("env init", flag.ContinueOnError)
	flavor := fs.String("flavor", "debian", "Flavor name or path (debian only for now)")
	var from []string
	fs.Func("from", "Manifest file (repeatable)", func(s string) error {
		from = append(from, s)
		return nil
	})
	if err := fs.Parse(rest); err != nil {
		return err
	}
	var files []map[string]string
	for _, p := range from {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, map[string]string{"name": filepath.Base(p), "text": string(b)})
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "env init %s flavor=%s manifests=%d (first boot may download Debian)...\n", name, *flavor, len(files))
	resp, err := api(cfg, "POST", "/v1/env/init", map[string]any{
		"name": name, "flavor": *flavor, "from": files,
	}, 45*time.Minute)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := decodeInto(resp, &out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdEnvSnap(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: exe env snap NAME create|ls|restore|rm ...")
	}
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: exe env snap NAME create|ls|restore|rm ...")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch rest[0] {
	case "ls", "list":
		resp, err := api(cfg, "GET", "/v1/vms/"+name+"/snaps", nil, 30*time.Second)
		if err != nil {
			return err
		}
		var list []agentenv.Snap
		if err := decodeInto(resp, &list); err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("(no snapshots)")
			return nil
		}
		for _, s := range list {
			fmt.Printf("%s\t%s\t%s\t%d\n", s.ID, s.Label, s.CreatedAt.Format(time.RFC3339), s.Bytes)
		}
		return nil
	case "create":
		label := ""
		if len(rest) > 1 {
			label = strings.Join(rest[1:], " ")
		}
		resp, err := api(cfg, "POST", "/v1/vms/"+name+"/snaps", map[string]string{"label": label}, 30*time.Minute)
		if err != nil {
			return err
		}
		var s agentenv.Snap
		if err := decodeInto(resp, &s); err != nil {
			return err
		}
		fmt.Printf("snap %s %s\n", s.ID, s.Label)
		return nil
	case "restore":
		if len(rest) < 2 {
			return fmt.Errorf("usage: exe env snap NAME restore ID")
		}
		resp, err := api(cfg, "POST", "/v1/vms/"+name+"/snaps/"+rest[1]+"/restore", map[string]string{}, 30*time.Minute)
		if err != nil {
			return err
		}
		return decodeInto(resp, nil)
	case "rm", "delete":
		if len(rest) < 2 {
			return fmt.Errorf("usage: exe env snap NAME rm ID")
		}
		resp, err := api(cfg, "DELETE", "/v1/vms/"+name+"/snaps/"+rest[1], nil, 2*time.Minute)
		if err != nil {
			return err
		}
		return decodeInto(resp, nil)
	default:
		return fmt.Errorf("unknown snap verb %q", rest[0])
	}
}

func cmdEnvRun(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return fmt.Errorf("usage: exe env run NAME [--script F] [--cmd CMD] [--file F]... [--prompt T] [--session ID]")
	}
	fs := flag.NewFlagSet("env run", flag.ContinueOnError)
	script := fs.String("script", "", "shell script to run in the VM")
	cmd := fs.String("cmd", "", "command to run in the VM")
	prompt := fs.String("prompt", "", "prompt for a named agent inside the VM")
	session := fs.String("session", "", "continue this session")
	asJSON := fs.Bool("json", false, "print raw JSON")
	var files []string
	fs.Func("file", "attach a file (repeatable)", func(s string) error {
		files = append(files, s)
		return nil
	})
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *cmd == "" && fs.NArg() > 0 {
		*cmd = strings.Join(fs.Args(), " ")
	}
	body := map[string]any{
		"cmd": *cmd, "prompt": *prompt, "session": *session,
	}
	if *script != "" {
		b, err := os.ReadFile(*script)
		if err != nil {
			return err
		}
		body["script"] = string(b)
	}
	var drops []map[string]string
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		drops = append(drops, map[string]string{
			"name":    filepath.Base(p),
			"content": base64.StdEncoding.EncodeToString(b),
		})
	}
	if drops != nil {
		body["files"] = drops
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/vms/"+name+"/jobs", body, 20*time.Minute)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s", strings.TrimSpace(string(raw)))
	}
	if *asJSON {
		os.Stdout.Write(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return nil
	}
	var out struct {
		Session     string `json:"session"`
		Job         string `json:"job"`
		ExitCode    int    `json:"exit_code"`
		ShellOutput string `json:"shell_output"`
		AgentOutput string `json:"agent_output"`
		Downloads   []struct {
			Label string `json:"label"`
			URL   string `json:"url"`
			Token string `json:"token"`
		} `json:"downloads"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	fmt.Printf("session %s  job %s  exit %d\n", out.Session, out.Job, out.ExitCode)
	if out.ShellOutput != "" {
		fmt.Print(out.ShellOutput)
		if !strings.HasSuffix(out.ShellOutput, "\n") {
			fmt.Println()
		}
	}
	if out.AgentOutput != "" {
		fmt.Println("--- agent ---")
		fmt.Println(out.AgentOutput)
	}
	for _, d := range out.Downloads {
		fmt.Println("download:", d.URL)
	}
	fmt.Println("continue: exe env run", name, "--session", out.Session, "--cmd '...'")
	return nil
}
