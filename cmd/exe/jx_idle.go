package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"exe/internal/config"
	"exe/internal/jx/idle"
)

func init() {
	jxCommands["idle"] = cmdIdle
	jxUsageLines["idle"] = `  exe idle [ls]                            VMs' idle policy, holders and stop times
  exe idle set <vm> [-kind dev|agent|service|job] [-pin|-unpin] [-minutes N|-default]
  exe idle keep <vm>                       reset a VM's idle timer`
}

func cmdIdle(args []string) error {
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls":
		return cmdIdleLs()
	case "set":
		return cmdIdleSet(args)
	case "keep":
		return cmdIdleKeep(args)
	}
	return fmt.Errorf("unknown idle command %q (ls, set, keep)", sub)
}

func cmdIdleLs() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "GET", "/v1/jx/vms", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var rows []idle.Row
	if err := decodeInto(resp, &rows); err != nil {
		return err
	}
	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tKIND\tPIN\tLIMIT\tHOLDERS\tIDLE\tSTOPS")
	for _, r := range rows {
		limit := "never"
		if r.EffectiveMinutes > 0 {
			limit = fmt.Sprintf("%dm", r.EffectiveMinutes)
		}
		if r.IdleMinutes == nil {
			limit += " (default)"
		}
		pin := "-"
		if r.Pinned {
			pin = "yes"
		}
		var holders []string
		for _, h := range r.Holders {
			holders = append(holders, h.Reason)
		}
		hs := strings.Join(holders, ",")
		if hs == "" {
			hs = "-"
		}
		idleFor, stops := "-", "-"
		if r.State == "running" && len(r.Holders) == 0 && r.LastBusy != nil {
			idleFor = shortDur(time.Duration(r.IdleSeconds) * time.Second)
		}
		if r.StopAt != nil {
			if d := r.StopAt.Sub(now); d > 0 {
				stops = "in " + shortDur(d)
			} else {
				stops = "now"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.VM, r.State, r.Kind, pin, limit, hs, idleFor, stops)
	}
	tw.Flush()
	if resp, err := api(cfg, "GET", "/v1/jx/memory", nil, 10*time.Second); err == nil {
		var m idle.Report
		if decodeInto(resp, &m) == nil && m.TotalMB > 0 {
			capNote := "guard off"
			if m.CapPercent > 0 {
				capNote = fmt.Sprintf("cap %d%% = %d MB", m.CapPercent, m.CapMB)
			}
			fmt.Printf("\nhost memory: %d of %d MB used, %d MB available (%s)\n", m.UsedMB, m.TotalMB, m.AvailableMB, capNote)
		}
	}
	return nil
}

func shortDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func cmdIdleSet(args []string) error {
	name, rest, err := splitName(args)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("idle set", flag.ExitOnError)
	kind := fs.String("kind", "", "dev, agent, service or job")
	pin := fs.Bool("pin", false, "never stop this VM for idleness or memory")
	unpin := fs.Bool("unpin", false, "undo -pin")
	minutes := fs.Int("minutes", -1, "idle limit in minutes (0 = never)")
	def := fs.Bool("default", false, "use the kind's default idle limit")
	fs.Parse(rest)
	body := map[string]any{}
	if *kind != "" {
		body["kind"] = *kind
	}
	switch {
	case *pin && *unpin:
		return fmt.Errorf("-pin and -unpin together")
	case *pin:
		body["pinned"] = true
	case *unpin:
		body["pinned"] = false
	}
	switch {
	case *def && *minutes >= 0:
		return fmt.Errorf("-minutes and -default together")
	case *def:
		body["idle_minutes"] = nil
	case *minutes >= 0:
		body["idle_minutes"] = *minutes
	}
	if len(body) == 0 {
		return fmt.Errorf("nothing to set: give -kind, -pin, -unpin, -minutes or -default")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "PUT", "/v1/jx/vms/"+name, body, 30*time.Second)
	if err != nil {
		return err
	}
	var r idle.Row
	if err := decodeInto(resp, &r); err != nil {
		return err
	}
	limit := "never"
	if r.EffectiveMinutes > 0 {
		limit = fmt.Sprintf("%d min", r.EffectiveMinutes)
	}
	fmt.Printf("%s: kind %s, pinned %v, idle limit %s\n", r.VM, r.Kind, r.Pinned, limit)
	return nil
}

func cmdIdleKeep(args []string) error {
	name, _, err := splitName(args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/jx/vms/"+name+"/keep", nil, 30*time.Second)
	if err != nil {
		return err
	}
	if err := decodeInto(resp, nil); err != nil {
		return err
	}
	fmt.Printf("%s: idle timer reset\n", name)
	return nil
}
