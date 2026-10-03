package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"exe/internal/config"
	"exe/internal/jx/cron"
)

func init() {
	jxCommands["cron"] = cmdCron
	jxUsageLines["cron"] = `  exe cron [ls]                            scheduled jobs, next and last runs
  exe cron add -name n -schedule '0 3 * * *' -target <vm> (-shell 'cmd' | -agent claude|codex -prompt '...' [-thread id])
               [-tz Area/City] [-timeout minutes] [-disabled]
  exe cron rm|run <id>                     delete a job / run it now
  exe cron runs <id> [-out]                its last 20 runs (-out prints their output)`
}

// cronJob is a GET /v1/jx/cron entry.
type cronJob struct {
	cron.Job
	NextRun    *time.Time `json:"next_run"`
	LastRun    *time.Time `json:"last_run"`
	LastStatus string     `json:"last_status"`
	Running    bool       `json:"running"`
}

func cmdCron(args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls":
		return cmdCronLs()
	case "add":
		return cmdCronAdd(args)
	case "rm":
		return cmdCronID(args, "DELETE", "", "deleted")
	case "run":
		return cmdCronID(args, "POST", "/run", "started")
	case "runs":
		return cmdCronRuns(args)
	}
	return fmt.Errorf("unknown cron command %q (ls, add, rm, run, runs)", sub)
}

func cmdCronLs() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "GET", "/v1/jx/cron", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var jobs []cronJob
	if err := decodeInto(resp, &jobs); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSCHEDULE\tKIND\tTARGET\tNEXT\tLAST")
	for _, j := range jobs {
		sched := j.Schedule
		if j.TZ != "" {
			sched += " (" + j.TZ + ")"
		}
		next := "-"
		switch {
		case !j.Enabled:
			next = "disabled"
		case j.NextRun != nil:
			next = j.NextRun.Local().Format("Jan 2 15:04")
		}
		last := "-"
		if j.LastRun != nil {
			last = j.LastRun.Local().Format("Jan 2 15:04") + " " + j.LastStatus
		}
		if j.Running {
			last = "running"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Name, sched, j.Kind, j.Target, next, last)
	}
	tw.Flush()
	return nil
}

func cmdCronAdd(args []string) error {
	fs := flag.NewFlagSet("cron add", flag.ExitOnError)
	name := fs.String("name", "", "job name")
	schedule := fs.String("schedule", "", "5-field cron, or @every 30m, @hourly, @daily, @weekly")
	tz := fs.String("tz", "", "time zone, e.g. Europe/Berlin (default: the daemon's local time)")
	target := fs.String("target", "", "VM name (or host, for agent jobs)")
	shell := fs.String("shell", "", "command to run in the VM")
	agent := fs.String("agent", "", "claude or codex: post -prompt to the Board")
	prompt := fs.String("prompt", "", "prompt for the agent")
	thread := fs.String("thread", "", "continue this Board thread instead of starting one per run")
	timeout := fs.Int("timeout", 0, "run timeout in minutes (default 60)")
	disabled := fs.Bool("disabled", false, "add the job switched off")
	fs.Parse(args)
	if *schedule == "" {
		return fmt.Errorf("-schedule is required")
	}
	j := cron.Job{
		Name: *name, Schedule: *schedule, TZ: *tz, Enabled: !*disabled, Target: *target,
		ThreadID: *thread, TimeoutMinutes: *timeout,
	}
	switch {
	case *shell != "" && (*agent != "" || *prompt != ""):
		return fmt.Errorf("give either -shell or -agent/-prompt, not both")
	case *shell != "":
		j.Kind, j.Command = cron.KindShell, *shell
	case *prompt != "":
		j.Kind, j.Agent, j.Prompt = cron.KindBoard, *agent, *prompt
	default:
		return fmt.Errorf("give -shell 'command' or -agent claude|codex -prompt '...'")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/jx/cron", j, 30*time.Second)
	if err != nil {
		return err
	}
	var got cronJob
	if err := decodeInto(resp, &got); err != nil {
		return err
	}
	next := "never"
	if got.NextRun != nil {
		next = got.NextRun.Local().Format("Mon Jan 2 15:04 MST")
	}
	fmt.Printf("%s: added %q, next run %s\n", got.ID, got.Name, next)
	return nil
}

func cmdCronID(args []string, method, suffix, done string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("job id required (see `exe cron ls`)")
	}
	id := args[0]
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, method, "/v1/jx/cron/"+id+suffix, nil, 30*time.Second)
	if err != nil {
		return err
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	if err := decodeInto(resp, &out); err != nil {
		return err
	}
	if out.RunID != "" {
		fmt.Printf("%s: %s run %s (see `exe cron runs %s`)\n", id, done, out.RunID, id)
		return nil
	}
	fmt.Printf("%s: %s\n", id, done)
	return nil
}

func cmdCronRuns(args []string) error {
	id, rest, err := splitName(args)
	if err != nil {
		return fmt.Errorf("job id required (see `exe cron ls`)")
	}
	fs := flag.NewFlagSet("cron runs", flag.ExitOnError)
	showOut := fs.Bool("out", false, "print each run's output")
	fs.Parse(rest)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "GET", "/v1/jx/cron/"+id+"/runs", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var runs []cron.Run
	if err := decodeInto(resp, &runs); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tSTARTED\tTOOK\tSTATUS\tEXIT\tNOTE")
	for _, r := range runs {
		took, exit := "-", "-"
		if r.EndedAt != nil {
			took = r.EndedAt.Sub(r.StartedAt).Round(time.Second).String()
		}
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		note := r.Error
		if r.ThreadID != "" {
			note = strings.TrimSpace(note + " thread " + r.ThreadID)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.StartedAt.Local().Format("Jan 2 15:04:05"), took, r.Status, exit, note)
	}
	tw.Flush()
	if *showOut {
		for _, r := range runs {
			if r.Output == "" {
				continue
			}
			fmt.Printf("\n--- run %s (%s) ---\n%s", r.ID, r.StartedAt.Local().Format("Jan 2 15:04:05"), r.Output)
			if !strings.HasSuffix(r.Output, "\n") {
				fmt.Println()
			}
		}
	}
	return nil
}
