package agent_test

import (
	"strings"
	"testing"

	"github.com/approachcontrol/approach/agent"
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

func TestReasoningEffortChoicesAreProviderSpecific(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{agent.CommandCodex, []string{"default", "minimal", "low", "medium", "high", "xhigh"}},
		{agent.CommandClaude, []string{"default", "low", "medium", "high", "xhigh", "max"}},
		{agent.CommandCodexApp, []string{"default"}},
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
}

func TestModelChoicesAreProviderSpecific(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{agent.CommandCodex, []string{"default", "gpt-5.5", "gpt-5.6-sol"}},
		{agent.CommandClaude, []string{"default", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5", "claude-fable-5"}},
		{agent.CommandCodexApp, []string{"default"}},
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
	if err := agent.ValidateModel(agent.CommandCodexApp, "gpt-5.5"); err == nil {
		t.Fatal("expected codex-app non-default model to be rejected")
	}
}
