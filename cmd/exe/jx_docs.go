package main

import (
	"os"

	"exe/internal/server"
)

func init() {
	jxCommands["docs"] = func([]string) error { _, err := os.Stdout.Write(server.DocsMarkdown()); return err }
	jxCommands["skill"] = func([]string) error { _, err := os.Stdout.Write(server.SkillMarkdown()); return err }
	jxUsageLines["docs"] = "  exe docs                                 print the user manual (markdown)"
	jxUsageLines["skill"] = "  exe skill                                print the agent skill guide (markdown)"
}
