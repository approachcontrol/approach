package agent_test

import (
	"strings"
	"testing"

	"github.com/approachcontrol/approach/agent"
)

func TestNormalizeStoredMapsRetiredCommands(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: "  CoDeX-App  ", want: agent.CommandCodex},
		{command: " CODEX ", want: agent.CommandCodex},
		{command: " Claude ", want: agent.CommandClaude},
		{command: " Gemini ", want: "gemini"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := agent.NormalizeStored(tt.command); got != tt.want {
				t.Fatalf("NormalizeStored(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestSupportedAgents(t *testing.T) {
	for _, command := range []string{agent.CommandCodex, agent.CommandClaude, agent.CommandCursor} {
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

func TestValidateRejectsCodexApp(t *testing.T) {
	if agent.Supported("codex-app") {
		t.Fatal("retired agent must not be supported")
	}
	err := agent.Validate("codex-app")
	if err == nil {
		t.Fatal("expected unsupported agent error")
	}
	want := `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`
	if err.Error() != want {
		t.Fatalf("Validate() error = %q, want %q", err, want)
	}
}

func TestReasoningEffortChoicesAreProviderSpecific(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{agent.CommandCodex, []string{"default", "minimal", "low", "medium", "high", "xhigh"}},
		{agent.CommandClaude, []string{"default", "low", "medium", "high", "xhigh", "max"}},
		{agent.CommandCursor, []string{"default"}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := agent.ReasoningEffortChoices(tt.command)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("ReasoningEffortChoices(%q) = %#v, want %#v", tt.command, got, tt.want)
			}
		})
	}
}

func TestValidateReasoningEffortRejectsUnsupportedProviderValues(t *testing.T) {
	if err := agent.ValidateReasoningEffort(agent.CommandCodex, "max"); err == nil {
		t.Fatal("expected codex max effort to be rejected")
	}
	if err := agent.ValidateReasoningEffort(agent.CommandCodex, "minimal"); err != nil {
		t.Fatalf("expected codex minimal effort to be accepted, got %v", err)
	}
	if err := agent.ValidateReasoningEffort(agent.CommandClaude, "xhigh"); err != nil {
		t.Fatalf("expected claude xhigh effort to be accepted, got %v", err)
	}
	if err := agent.ValidateReasoningEffort(agent.CommandCodex, ""); err != nil {
		t.Fatalf("expected empty codex effort to mean default, got %v", err)
	}
	if err := agent.ValidateReasoningEffort(agent.CommandClaude, " DEFAULT "); err != nil {
		t.Fatalf("expected default claude effort to be accepted, got %v", err)
	}
	if err := agent.ValidateReasoningEffort(agent.CommandCursor, "default"); err != nil {
		t.Fatalf("expected cursor default effort to be accepted, got %v", err)
	}
	if err := agent.ValidateReasoningEffort(agent.CommandCursor, "high"); err == nil {
		t.Fatal("expected cursor high effort to be rejected")
	}
}

func TestModelChoicesAreProviderSpecific(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{agent.CommandCodex, []string{"default", "gpt-5.5", "gpt-5.6-sol"}},
		{agent.CommandClaude, []string{"default", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5", "claude-fable-5"}},
		{agent.CommandCursor, []string{"default", "composer-2.5", "grok-4.6", "opus-5", "fable-5", "gpt-5.6-sol"}},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := agent.ModelChoices(tt.command)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("ModelChoices(%q) = %#v, want %#v", tt.command, got, tt.want)
			}
		})
	}
}

func TestValidateModelRejectsUnsupportedProviderValues(t *testing.T) {
	if err := agent.ValidateModel(agent.CommandCodex, "claude-sonnet-5"); err == nil {
		t.Fatal("expected codex claude-sonnet-5 model to be rejected")
	}
	if err := agent.ValidateModel(agent.CommandCodex, " GPT-5.5 "); err != nil {
		t.Fatalf("expected codex gpt-5.5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandClaude, "claude-fable-5"); err != nil {
		t.Fatalf("expected claude-fable-5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandClaude, "claude-opus-5"); err != nil {
		t.Fatalf("expected claude-opus-5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCodex, "gpt-5.6-sol"); err != nil {
		t.Fatalf("expected codex gpt-5.6-sol model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandClaude, "gpt-5.6-sol"); err == nil {
		t.Fatal("expected claude gpt-5.6-sol model to be rejected")
	}
	if err := agent.ValidateModel(agent.CommandCodex, ""); err != nil {
		t.Fatalf("expected empty codex model to mean default, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandClaude, " DEFAULT "); err != nil {
		t.Fatalf("expected default claude model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "composer-2.5"); err != nil {
		t.Fatalf("expected cursor composer-2.5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "composer"); err == nil {
		t.Fatal("expected bare composer slug to be rejected in favor of composer-2.5")
	}
	if err := agent.ValidateModel(agent.CommandCursor, "grok-4.6"); err != nil {
		t.Fatalf("expected cursor grok-4.6 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "opus-5"); err != nil {
		t.Fatalf("expected cursor opus-5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "fable-5"); err != nil {
		t.Fatalf("expected cursor fable-5 model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "gpt-5.6-sol"); err != nil {
		t.Fatalf("expected cursor gpt-5.6-sol model to be accepted, got %v", err)
	}
	if err := agent.ValidateModel(agent.CommandCursor, "claude-sonnet-5"); err == nil {
		t.Fatal("expected cursor claude-sonnet-5 model to be rejected")
	}
}
