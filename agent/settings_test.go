package agent_test

import (
	"testing"

	"github.com/approachcontrol/approach/agent"
)

func TestResolvePicksProviderSpecificSettings(t *testing.T) {
	prefs := agent.Preferences{
		CodexModel:   agent.ModelGPT55,
		ClaudeModel:  agent.ModelClaudeOpus5,
		CodexEffort:  agent.ReasoningEffortMedium,
		ClaudeEffort: agent.ReasoningEffortHigh,
	}
	tests := []struct {
		name    string
		command string
		want    agent.Settings
	}{
		{
			name:    "codex",
			command: " Codex ",
			want: agent.Settings{
				Command:         agent.CommandCodex,
				Model:           agent.ModelGPT55,
				ReasoningEffort: agent.ReasoningEffortMedium,
			},
		},
		{
			name:    "claude",
			command: "CLAUDE",
			want: agent.Settings{
				Command:         agent.CommandClaude,
				Model:           agent.ModelClaudeOpus5,
				ReasoningEffort: agent.ReasoningEffortHigh,
			},
		},
		{
			name:    "unknown command is echoed back",
			command: "gemini",
			want:    agent.Settings{Command: "gemini"},
		},
		{
			name:    "empty command",
			command: "   ",
			want:    agent.Settings{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefs := prefs
			prefs.Command = tc.command
			if got := agent.Resolve(prefs); got != tc.want {
				t.Fatalf("Resolve(%q) = %#v, want %#v", tc.command, got, tc.want)
			}
		})
	}
}
