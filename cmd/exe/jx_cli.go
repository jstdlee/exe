package main

import (
	"sort"
	"strings"
)

// jx (jstdlee extension) CLI commands. Each feature registers its commands
// from its own jx_*.go file in init: jxCommands["env"] = cmdEnv, plus one
// usage line per command in jxUsageLines.
var (
	jxCommands   = map[string]func(args []string) error{}
	jxUsageLines = map[string]string{} // command -> usage lines (no trailing newline)
)

func jxUsage() string {
	keys := make([]string, 0, len(jxUsageLines))
	for k := range jxUsageLines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(jxUsageLines[k])
		b.WriteByte('\n')
	}
	return b.String()
}

func usageText() string { return strings.Replace(usage, "%JX_USAGE%", jxUsage(), 1) }
