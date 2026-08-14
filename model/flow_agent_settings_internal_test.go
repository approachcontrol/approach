package model

import (
	"testing"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

func TestResolveFlowStartPhaseAgentSettingsUsesAuthoritativeCrossProviderStamp(t *testing.T) {
	prefs := agent.Preferences{
		Command:      agent.CommandCodex,
		CodexModel:   agent.ModelGPT55,
		ClaudeModel:  agent.ModelClaudeSonnet5,
		CodexEffort:  agent.ReasoningEffortMedium,
		ClaudeEffort: agent.ReasoningEffortMax,
	}
	got, err := resolveFlowStartPhaseAgentSettings(FlowStartRequest{
		AgentPreferences:         prefs,
		AgentPreferencesProvided: true,
	}, flowstore.FlowPhase{
		Agent: agent.CommandClaude,
		Model: agent.ModelClaudeOpus5,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := agent.Settings{
		Command: agent.CommandClaude, Model: agent.ModelClaudeOpus5, ReasoningEffort: agent.ReasoningEffortMax,
	}
	if got != want {
		t.Fatalf("initial Plan settings = %#v, want %#v", got, want)
	}
}

func TestResolveFlowRepairAgentSettingsUsesPhaseOrGraphScope(t *testing.T) {
	prefs := agent.Preferences{
		Command:      agent.CommandCodex,
		CodexModel:   agent.ModelGPT55,
		ClaudeModel:  agent.ModelClaudeSonnet5,
		CodexEffort:  agent.ReasoningEffortMedium,
		ClaudeEffort: agent.ReasoningEffortMax,
	}
	t.Run("phase scoped obstruction", func(t *testing.T) {
		record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation", Status: flowstore.PhaseBlocked,
			Agent: agent.CommandClaude, Model: agent.ModelClaudeOpus5,
		}}}
		got, err := resolveFlowRepairAgentSettings(record, prefs)
		if err != nil {
			t.Fatal(err)
		}
		want := agent.Settings{
			Command: agent.CommandClaude, Model: agent.ModelClaudeOpus5, ReasoningEffort: agent.ReasoningEffortMax,
		}
		if got != want {
			t.Fatalf("phase repair settings = %#v, want %#v", got, want)
		}
	})
	t.Run("graph scoped obstruction", func(t *testing.T) {
		record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{
			{PhaseID: "left", Status: flowstore.PhasePending, DependsOn: []string{"right"}},
			{PhaseID: "right", Status: flowstore.PhasePending, DependsOn: []string{"left"}},
		}}
		obstruction, ok := flowRepairObstructionForRecord(record)
		if !ok || obstruction.HasPhase {
			t.Fatalf("graph obstruction = %#v, %v", obstruction, ok)
		}
		got, err := resolveFlowRepairAgentSettings(record, prefs)
		if err != nil {
			t.Fatal(err)
		}
		want := agent.Settings{
			Command: agent.CommandCodex, Model: agent.ModelGPT55, ReasoningEffort: agent.ReasoningEffortMedium,
		}
		if got != want {
			t.Fatalf("graph repair settings = %#v, want %#v", got, want)
		}
	})
}
