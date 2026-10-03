package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"exe/internal/config"
	"exe/internal/jx/board"
)

// exe board: a thin client of the Board API (/v1/jx/board/*).

func init() {
	jxCommands["board"] = cmdBoard
	jxUsageLines["board"] = `  exe board ls                             Board threads (Claude Code / Codex task threads)
  exe board new <vm|host> <claude|codex> [-session id] [-fork] [-title t] [-f] <prompt...>
  exe board say <thread> [-f] <prompt...>  send a message (queued while a turn runs)
  exe board tail <thread> [-after N] [-f=false]   print a thread's events, then follow
  exe board stop <thread>                  stop the running turn and drop queued ones`
}

const boardUsage = "usage: exe board ls | new <target> <agent> [-session id] [-fork] <prompt...> | say <thread> <prompt...> | tail <thread> | stop <thread>"

func cmdBoard(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", boardUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls", "list":
		return boardLs(cfg)
	case "new":
		return boardNew(cfg, rest)
	case "say":
		return boardSay(cfg, rest)
	case "tail":
		return boardTail(cfg, rest)
	case "stop":
		if len(rest) != 1 {
			return fmt.Errorf("usage: exe board stop <thread>")
		}
		resp, err := api(cfg, "POST", "/v1/jx/board/threads/"+url.PathEscape(rest[0])+"/stop", nil, 30*time.Second)
		if err != nil {
			return err
		}
		return decodeInto(resp, nil)
	default:
		return fmt.Errorf("unknown board command %q\n%s", sub, boardUsage)
	}
}

func boardLs(cfg *config.Config) error {
	resp, err := api(cfg, "GET", "/v1/jx/board/threads", nil, 30*time.Second)
	if err != nil {
		return err
	}
	var list []board.Thread
	if err := decodeInto(resp, &list); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTARGET\tAGENT\tSTATE\tUPDATED\tTITLE")
	for _, t := range list {
		if t.Archived {
			continue
		}
		state := t.State
		if t.Queued > 0 {
			state += fmt.Sprintf("+%d", t.Queued)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Target, t.Agent, state,
			t.UpdatedAt.Local().Format("Jan 2 15:04"), t.Title)
	}
	return tw.Flush()
}

// promptArg is the prompt from the rest of the command line, or stdin.
func promptArg(args []string) (string, error) {
	p := strings.TrimSpace(strings.Join(args, " "))
	if p == "" {
		b, _ := io.ReadAll(os.Stdin)
		p = strings.TrimSpace(string(b))
	}
	if p == "" {
		return "", fmt.Errorf("prompt required")
	}
	return p, nil
}

func boardNew(cfg *config.Config, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: exe board new <vm|host> <claude|codex> [-session id] [-fork] [-title t] [-f] <prompt...>")
	}
	target, agent := args[0], args[1]
	fs := flag.NewFlagSet("board new", flag.ContinueOnError)
	session := fs.String("session", "", "continue this Claude Code session / Codex thread")
	fork := fs.Bool("fork", false, "fork the session instead of continuing it (Claude Code)")
	title := fs.String("title", "", "thread title (default: the prompt's first line)")
	follow := fs.Bool("f", false, "follow the turn until it ends")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	prompt, err := promptArg(fs.Args())
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/jx/board/threads", map[string]any{
		"target": target, "agent": agent, "session": *session, "fork": *fork,
		"title": *title, "prompt": prompt, "origin": "cli"}, 60*time.Second)
	if err != nil {
		return err
	}
	var out struct {
		Thread board.Thread `json:"thread"`
		TurnID string       `json:"turn_id"`
	}
	if err := decodeInto(resp, &out); err != nil {
		return err
	}
	fmt.Printf("thread %s (turn %s)\n", out.Thread.ID, out.TurnID)
	if *follow {
		return boardFollow(cfg, out.Thread.ID, 0, out.TurnID)
	}
	fmt.Printf("follow it: exe board tail %s\n", out.Thread.ID)
	return nil
}

func boardSay(cfg *config.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: exe board say <thread> [-f] <prompt...>")
	}
	id := args[0]
	fs := flag.NewFlagSet("board say", flag.ContinueOnError)
	follow := fs.Bool("f", false, "follow the turn until it ends")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	prompt, err := promptArg(fs.Args())
	if err != nil {
		return err
	}
	resp, err := api(cfg, "POST", "/v1/jx/board/threads/"+url.PathEscape(id)+"/turns",
		map[string]string{"prompt": prompt, "origin": "cli"}, 60*time.Second)
	if err != nil {
		return err
	}
	var out struct {
		TurnID string `json:"turn_id"`
		Queued bool   `json:"queued"`
	}
	if err := decodeInto(resp, &out); err != nil {
		return err
	}
	if out.Queued {
		fmt.Printf("turn %s queued behind the running one\n", out.TurnID)
	} else {
		fmt.Printf("turn %s started\n", out.TurnID)
	}
	if *follow {
		return boardFollow(cfg, id, 0, out.TurnID)
	}
	return nil
}

func boardTail(cfg *config.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: exe board tail <thread> [-after N] [-f=false]")
	}
	id := args[0]
	fs := flag.NewFlagSet("board tail", flag.ContinueOnError)
	after := fs.Int64("after", 0, "only events after this seq")
	follow := fs.Bool("f", true, "keep following new events")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*follow {
		resp, err := api(cfg, "GET", "/v1/jx/board/threads/"+url.PathEscape(id), nil, 60*time.Second)
		if err != nil {
			return err
		}
		var out struct {
			Turns []board.TurnView `json:"turns"`
		}
		if err := decodeInto(resp, &out); err != nil {
			return err
		}
		for _, t := range out.Turns {
			for _, ev := range t.Events {
				if ev.Seq > *after {
					printEvent(os.Stdout, ev)
				}
			}
		}
		return nil
	}
	return boardFollow(cfg, id, *after, "")
}

// boardFollow prints a thread's event stream; untilTurn != "" returns once
// that turn has ended. A dropped stream reconnects from the last seq.
func boardFollow(cfg *config.Config, id string, after int64, untilTurn string) error {
	for {
		resp, err := api(cfg, "GET", fmt.Sprintf("/v1/jx/board/threads/%s/events?after=%d", url.PathEscape(id), after), nil, 0)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 {
			return decodeInto(resp, nil)
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev board.Event
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			after = ev.Seq
			printEvent(os.Stdout, ev)
			if untilTurn != "" && ev.TurnID == untilTurn && ev.Type == board.EvTurnEnd {
				resp.Body.Close()
				if ev.State == board.TurnError {
					return fmt.Errorf("turn failed: %s", ev.Error)
				}
				return nil
			}
		}
		resp.Body.Close()
		time.Sleep(time.Second)
	}
}

// printEvent writes one event as readable text.
func printEvent(w io.Writer, ev board.Event) {
	indent := func(s, pre string) string {
		return pre + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+pre)
	}
	switch ev.Type {
	case board.EvTurnStart:
		fmt.Fprintf(w, "\n%s\n", indent(ev.Prompt, "> "))
	case board.EvText:
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(ev.Text, "\n"))
	case board.EvTool:
		fmt.Fprintf(w, "  · %s\n", ev.Summary)
	case board.EvToolResult:
		lines := strings.Split(strings.TrimRight(ev.Output, "\n"), "\n")
		if len(lines) > 4 {
			lines = append(lines[:4], fmt.Sprintf("… (%d more lines)", len(lines)-4))
		}
		mark := "    "
		if ev.IsError {
			mark = "  ! "
		}
		if out := strings.Join(lines, "\n"); strings.TrimSpace(out) != "" {
			fmt.Fprintln(w, indent(out, mark))
		}
	case board.EvStatus:
		fmt.Fprintf(w, "  [%s]\n", ev.Text)
	case board.EvTurnEnd:
		line := "-- " + ev.State
		if ev.Error != "" {
			line += ": " + ev.Error
		}
		if u := ev.Usage; u != nil {
			line += fmt.Sprintf(" (in %d, out %d", u.InputTokens, u.OutputTokens)
			if u.CostUSD != nil {
				line += fmt.Sprintf(", $%.4f", *u.CostUSD)
			}
			line += ")"
		}
		fmt.Fprintln(w, line)
	}
}
