package flowstore_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

func TestResolvePhaseAgentSettings(t *testing.T) {
	prefs := agent.Preferences{
		Command:      agent.CommandCodex,
		CodexModel:   agent.ModelGPT55,
		ClaudeModel:  agent.ModelClaudeSonnet5,
		CodexEffort:  agent.ReasoningEffortMedium,
		ClaudeEffort: agent.ReasoningEffortMax,
	}
	tests := []struct {
		name string
		raw  flowstore.PhaseAgentSettings
		want agent.Settings
		err  bool
	}{
		{
			name: "legacy empty stamp uses selected globals",
			want: agent.Settings{Command: agent.CommandCodex, Model: agent.ModelGPT55, ReasoningEffort: agent.ReasoningEffortMedium},
		},
		{
			name: "agent-only stamp uses that provider globals",
			raw:  flowstore.PhaseAgentSettings{Agent: " CLAUDE "},
			want: agent.Settings{Command: agent.CommandClaude, Model: agent.ModelClaudeSonnet5, ReasoningEffort: agent.ReasoningEffortMax},
		},
		{
			name: "non-empty fields override independently",
			raw:  flowstore.PhaseAgentSettings{Agent: agent.CommandClaude, Model: agent.ModelClaudeOpus5},
			want: agent.Settings{Command: agent.CommandClaude, Model: agent.ModelClaudeOpus5, ReasoningEffort: agent.ReasoningEffortMax},
		},
		{
			name: "literal default remains explicit",
			raw:  flowstore.PhaseAgentSettings{Agent: agent.CommandCodex, Model: agent.ModelDefault, ReasoningEffort: agent.ReasoningEffortDefault},
			want: agent.Settings{Command: agent.CommandCodex, Model: agent.ModelDefault, ReasoningEffort: agent.ReasoningEffortDefault},
		},
		{
			name: "raw model without agent fails before fallback",
			raw:  flowstore.PhaseAgentSettings{Model: agent.ModelGPT55},
			err:  true,
		},
		{
			name: "provider-incompatible raw model fails before fallback",
			raw:  flowstore.PhaseAgentSettings{Agent: agent.CommandClaude, Model: agent.ModelGPT55},
			err:  true,
		},
		{
			name: "codex-app overrides fail before fallback",
			raw:  flowstore.PhaseAgentSettings{Agent: agent.CommandCodexApp, ReasoningEffort: agent.ReasoningEffortHigh},
			err:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := flowstore.ResolvePhaseAgentSettings(prefs, tt.raw)
			if tt.err {
				if err == nil {
					t.Fatalf("ResolvePhaseAgentSettings() = %#v, nil; want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePhaseAgentSettings() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolvePhaseAgentSettings() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStoreSetPhaseAgentSettingsReplacesAndClearsOnlyTheTargetStamp(t *testing.T) {
	now := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	record, err := store.CreateWithOptions(flowstore.FlowRecord{Title: "Agent settings", Instructions: "Test settings.", RepoPath: "/repo"}, flowstore.CreateOptions{
		PhaseAgent: flowstore.PhaseAgentSettings{Agent: agent.CommandCodex, Model: agent.ModelGPT55},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforePlan := record.Phases[0]
	beforeOther := record.Phases[1]

	now = now.Add(time.Minute)
	updated, err := store.SetPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{
		FlowID:  record.FlowID,
		PhaseID: beforePlan.PhaseID,
		Settings: flowstore.PhaseAgentSettings{
			Agent:           " CLAUDE ",
			ReasoningEffort: " HIGH ",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gotPlan := updated.Phases[0]
	if got := gotPlan.AgentSettings(); got != (flowstore.PhaseAgentSettings{Agent: agent.CommandClaude, ReasoningEffort: agent.ReasoningEffortHigh}) {
		t.Fatalf("target settings = %#v", got)
	}
	if gotPlan.Status != beforePlan.Status || gotPlan.Outcome != beforePlan.Outcome || gotPlan.UpdatedAt != now {
		t.Fatalf("target unrelated fields changed: before=%#v after=%#v", beforePlan, gotPlan)
	}
	if !reflect.DeepEqual(updated.Phases[1], beforeOther) {
		t.Fatalf("unrelated phase changed: before=%#v after=%#v", beforeOther, updated.Phases[1])
	}

	unchanged, err := store.SetPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{
		FlowID:   record.FlowID,
		PhaseID:  beforePlan.PhaseID,
		Settings: gotPlan.AgentSettings(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.UpdatedAt != updated.UpdatedAt || unchanged.Phases[0].UpdatedAt != gotPlan.UpdatedAt {
		t.Fatal("identical replacement changed timestamps")
	}

	now = now.Add(time.Minute)
	cleared, err := store.SetPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{FlowID: record.FlowID, PhaseID: beforePlan.PhaseID})
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.Phases[0].AgentSettings().IsZero() {
		t.Fatalf("cleared settings = %#v", cleared.Phases[0].AgentSettings())
	}
}
