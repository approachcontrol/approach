package agent_test

import (
	"strings"
	"testing"

	"github.com/brian-bell/wtui/agent"
)

func TestNormalizeSupportsCodexApp(t *testing.T) {
	if got := agent.Normalize("  CoDeX-App  "); got != agent.CommandCodexApp {
		t.Fatalf("Normalize = %q, want %q", got, agent.CommandCodexApp)
	}
}

func TestSupportedIncludesCodexApp(t *testing.T) {
	for _, command := range []string{agent.CommandCodex, agent.CommandCodexApp, agent.CommandClaude} {
		t.Run(command, func(t *testing.T) {
			if !agent.Supported(command) {
				t.Fatalf("expected %q to be supported", command)
			}
			if err := agent.Validate(command); err != nil {
				t.Fatalf("Validate(%q) returned error: %v", command, err)
			}
		})
	}
}

func TestValidateUnsupportedAgentMentionsCodexApp(t *testing.T) {
	err := agent.Validate("vim")
	if err == nil {
		t.Fatal("expected unsupported agent error")
	}
	for _, want := range []string{"codex", "codex-app", "claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error %q to mention %q", err.Error(), want)
		}
	}
}
