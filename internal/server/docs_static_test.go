package server

import (
	"os"
	"strings"
	"testing"
)

func readMarkdownAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDocsReflectGuestToolsAndNoRemovedChatUI(t *testing.T) {
	docs := readMarkdownAsset(t, "docs.md")
	for _, want := range []string{
		"**Tools** — launch code-agent CLIs and developer tools inside this VM terminal",
		"File transfer stays in Workspace",
		"copy/paste uses native browser/OS selection",
		"Help → Agent Skill Guide",
		"`exe env`",
	} {
		if !strings.Contains(docs, want) {
			t.Fatalf("docs.md missing current guidance %q", want)
		}
	}
	for _, bad := range []string{
		"**Chat**",
		"Host Chat",
		"Chat window",
		"built-in Agent",
		"**Transcripts**",
		"host transcripts",
	} {
		if strings.Contains(docs, bad) {
			t.Fatalf("docs.md still documents removed UI %q", bad)
		}
	}
}

func TestSkillReflectsCurrentAgentAndFileTransferSurface(t *testing.T) {
	skill := readMarkdownAsset(t, "skill.md")
	for _, want := range []string{
		"Host Workspace, Notes, Editor, apps",
		"VM Tools tab has Code-agent CLI buttons",
		"File transfer: use `scp -P 2222`, `sftp -P 2222`, or `/v1/workspace`",
		"copy/paste uses native browser/OS selection",
		"Use `exe skill` or `GET /skill.md`",
	} {
		if !strings.Contains(skill, want) {
			t.Fatalf("skill.md missing current guidance %q", want)
		}
	}
	for _, bad := range []string{
		"Host Chat",
		"Chat send",
		"desktop File menu / mobile Menu drawer",
		"Host Workspace, Notes, Editor, Chat, Browser apps",
		"host transcripts",
		"right-click to copy; Ctrl + right-click",
	} {
		if strings.Contains(skill, bad) {
			t.Fatalf("skill.md still documents removed/stale UI %q", bad)
		}
	}
}
