package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"exe/internal/config"
	"exe/internal/jx/snap"
	"exe/internal/server"
)

func init() {
	jxCommands["snap"] = cmdSnap
	jxUsageLines["snap"] = `  exe snap ls <vm>                         list disk snapshots
  exe snap create <vm> [label] [-force]    stop, snapshot the disk, start again
  exe snap restore <vm> <id> [-force]      put a snapshot back (replaces the disk)
  exe snap rm <vm> <id>                    delete a snapshot`
}

const snapUsage = `exe snap — VM disk snapshots

  exe snap ls <vm>
  exe snap create <vm> [label] [-force]
  exe snap restore <vm> <id> [-force]
  exe snap rm <vm> <id>

create and restore stop the VM for the copy and start it again if it was
running. They refuse a VM in use (open terminal, SSH session, agent turn,
env job) unless -force. Copies are reflinks where the filesystem allows,
else sparse copies.
`

func cmdSnap(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(snapUsage)
		return nil
	}
	verb := args[0]
	fs := flag.NewFlagSet("snap "+verb, flag.ContinueOnError)
	force := fs.Bool("force", false, "go ahead even if the VM is in use")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("vm name required\n\n%s", snapUsage)
	}
	vm := url.PathEscape(pos[0])
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	base := "/v1/jx/vms/" + vm + "/snapshots"
	needID := func() (string, error) {
		if len(pos) != 2 {
			return "", fmt.Errorf("usage: exe snap %s <vm> <id>", verb)
		}
		return url.PathEscape(pos[1]), nil
	}
	switch verb {
	case "ls", "list":
		resp, err := api(cfg, "GET", base, nil, 30*time.Second)
		if err != nil {
			return err
		}
		var list []snap.Meta
		if err := decodeInto(resp, &list); err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("(no snapshots)")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCREATED\tSIZE\tUSED\tMETHOD\tLABEL")
		for _, m := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.CreatedAt.Local().Format("2006-01-02 15:04"),
				humanBytes(m.Size), humanBytes(m.Used), m.Method, m.Label)
		}
		return tw.Flush()
	case "create":
		req := server.SnapRequest{Label: strings.Join(pos[1:], " "), Force: *force}
		fmt.Fprintf(os.Stderr, "snapshotting %s (the VM stops during the copy)...\n", pos[0])
		resp, err := api(cfg, "POST", base, req, 2*time.Hour)
		if err != nil {
			return err
		}
		var res server.SnapResult
		if err := decodeInto(resp, &res); err != nil {
			return err
		}
		printSnapResult("snapshot", res)
		return nil
	case "restore":
		id, err := needID()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "restoring %s from %s...\n", pos[0], pos[1])
		resp, err := api(cfg, "POST", base+"/"+id+"/restore", server.SnapRequest{Force: *force}, 2*time.Hour)
		if err != nil {
			return err
		}
		var res server.SnapResult
		if err := decodeInto(resp, &res); err != nil {
			return err
		}
		printSnapResult("restored", res)
		return nil
	case "rm", "delete":
		id, err := needID()
		if err != nil {
			return err
		}
		resp, err := api(cfg, "DELETE", base+"/"+id, nil, time.Minute)
		if err != nil {
			return err
		}
		if err := decodeInto(resp, nil); err != nil {
			return err
		}
		fmt.Printf("%s: snapshot %s deleted\n", pos[0], pos[1])
		return nil
	}
	return fmt.Errorf("unknown snap command %q\n\n%s", verb, snapUsage)
}

func printSnapResult(what string, res server.SnapResult) {
	m := res.Snapshot
	fmt.Printf("%s %s %s (%s, %s)\n", what, m.ID, m.Label, humanBytes(m.Size), m.Method)
	if res.Restarted {
		fmt.Printf("%s is running again\n", m.VM)
	}
	if res.Warning != "" {
		fmt.Fprintln(os.Stderr, "warning:", res.Warning)
	}
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
