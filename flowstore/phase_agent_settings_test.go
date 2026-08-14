package flowstore_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
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
			name: "raw effort without agent fails before fallback",
			raw:  flowstore.PhaseAgentSettings{ReasoningEffort: agent.ReasoningEffortHigh},
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

func TestStoreSetPhaseAgentSettingsPreservesRawLegacyRecordOutsideExactTarget(t *testing.T) {
	root := t.TempDir()
	createdAt := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return createdAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Raw settings preservation",
		Instructions: "Keep unrelated legacy JSON untouched.",
		RepoPath:     "/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	data := readSQLiteRecordForTest(t, root, record.FlowID)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	phases := raw["phases"].([]any)
	canonical := phases[0].(map[string]any)
	canonical["agent"] = " CODEX "
	canonical["model"] = "GPT-5.5"
	canonical["depends_on"] = []any{" Plan Review "}
	unrelated := phases[1].(map[string]any)
	delete(unrelated, "kind")
	unrelated["agent"] = " CLAUDE "
	unrelated["model"] = "gpt-5.5" // Deliberately invalid for this unrelated provider.
	unrelated["reasoning_effort"] = " HIGH "
	unrelated["depends_on"] = []any{" Plan "}
	emptyKeys := phases[2].(map[string]any)
	emptyKeys["agent"] = ""
	emptyKeys["model"] = ""
	emptyKeys["reasoning_effort"] = ""
	duplicate := make(map[string]any, len(canonical))
	for key, value := range canonical {
		duplicate[key] = value
	}
	duplicate["phase_id"] = " Plan "
	duplicate["title"] = "Exact legacy duplicate"
	duplicate["agent"] = "codex"
	duplicate["model"] = "default"
	delete(duplicate, "reasoning_effort")
	raw["phases"] = append(phases, duplicate)
	seeded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", seeded, record.FlowID); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	updatedAt := createdAt.Add(time.Minute)
	store, err = flowstore.NewStore(flowstore.StoreOptions{
		Root: root,
		Now:  func() time.Time { return updatedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	updated, err := store.SetPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{
		FlowID:  record.FlowID,
		PhaseID: " Plan ",
		Settings: flowstore.PhaseAgentSettings{
			Agent:           agent.CommandClaude,
			Model:           agent.ModelClaudeOpus5,
			ReasoningEffort: agent.ReasoningEffortMax,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Phases[len(updated.Phases)-1].AgentSettings(); got != (flowstore.PhaseAgentSettings{
		Agent: agent.CommandClaude, Model: agent.ModelClaudeOpus5, ReasoningEffort: agent.ReasoningEffortMax,
	}) {
		t.Fatalf("exact duplicate settings = %#v", got)
	}

	var want map[string]any
	if err := json.Unmarshal(seeded, &want); err != nil {
		t.Fatal(err)
	}
	want["updated_at"] = updatedAt.Format(time.RFC3339Nano)
	wantPhases := want["phases"].([]any)
	wantTarget := wantPhases[len(wantPhases)-1].(map[string]any)
	wantTarget["agent"] = agent.CommandClaude
	wantTarget["model"] = agent.ModelClaudeOpus5
	wantTarget["reasoning_effort"] = agent.ReasoningEffortMax
	wantTarget["updated_at"] = updatedAt.Format(time.RFC3339Nano)

	var got map[string]any
	if err := json.Unmarshal(readSQLiteRecordForTest(t, root, record.FlowID), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("settings-only write changed unrelated raw JSON\nwant:\n%s\ngot:\n%s", wantJSON, gotJSON)
	}
}
