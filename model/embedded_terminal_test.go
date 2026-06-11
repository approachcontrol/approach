package model

import (
	"slices"
	"testing"

	"github.com/brian-bell/wtui/actions"
)

func TestEmbeddedTerminalCommandDisablesCodexAltScreen(t *testing.T) {
	cmd, err := embeddedTerminalCommand(actions.AgentLaunchContext{
		Command:         "codex",
		ResumeSessionID: "codex-session-1",
		WorktreePath:    "/tmp",
	})
	if err != nil {
		t.Fatalf("embeddedTerminalCommand returned error: %v", err)
	}
	if !slices.Contains(cmd.Args, "--no-alt-screen") {
		t.Fatalf("codex embedded args = %#v, want --no-alt-screen", cmd.Args)
	}
}

func TestEmbeddedTerminalCommandLeavesClaudeArgsAlone(t *testing.T) {
	cmd, err := embeddedTerminalCommand(actions.AgentLaunchContext{
		Command:         "claude",
		ResumeSessionID: "claude-session-1",
		WorktreePath:    "/tmp",
	})
	if err != nil {
		t.Fatalf("embeddedTerminalCommand returned error: %v", err)
	}
	if slices.Contains(cmd.Args, "--no-alt-screen") {
		t.Fatalf("claude embedded args = %#v, should not include codex flag", cmd.Args)
	}
}
