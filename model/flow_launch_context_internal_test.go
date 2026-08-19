package model

import (
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
)

// launchContextVariantRecord is the fixture every variant row derives its
// target from. It is deliberately a plain literal rather than the generic
// agent's harness record: the builder is being pinned at its own interface,
// so the row must not inherit whatever the prepare stage's fixture happens to
// set.
func launchContextVariantRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree",
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
	}
}

func launchContextVariantSettings() flowLaunchAgentSettingsSnapshot {
	return flowLaunchAgentSettingsSnapshot{
		Command:          "codex",
		Model:            "gpt-5",
		ReasoningEffort:  "high",
		SessionStateRoot: "/state",
	}
}

// TestNewFlowLaunchContextPinsEachVariantsCanonicalContext compares the whole
// returned struct, not a subset of fields, so a field added to any Flow role's
// launch context has to be declared in this table before it can ship.
func TestNewFlowLaunchContextPinsEachVariantsCanonicalContext(t *testing.T) {
	record := launchContextVariantRecord()
	for _, variant := range []struct {
		name   string
		target flowLaunchTarget
		want   actions.AgentLaunchContext
		route  flowLaunchRoute
	}{
		{
			name: "worktree agent",
			target: worktreeAgentTarget{
				LaunchID: "launch-1",
				Record:   record,
				PlanPath: record.PlanPath,
			},
			want: actions.AgentLaunchContext{
				Command:          "codex",
				LaunchID:         "launch-1",
				RepoPath:         record.RepoPath,
				WorktreePath:     record.WorktreePath,
				WorkingDir:       record.WorktreePath,
				Branch:           record.Branch,
				Commit:           record.Commit,
				SessionStateRoot: "/state",
				PlanID:           record.PlanID,
				PlanPath:         record.PlanPath,
				FlowID:           record.FlowID,
				FlowAgent:        true,
				Embedded:         true,
				Model:            "gpt-5",
				ReasoningEffort:  "high",
			},
			route: flowLaunchRouteEmbedded,
		},
	} {
		t.Run(variant.name, func(t *testing.T) {
			ctx, route, err := newFlowLaunchContext(variant.target, launchContextVariantSettings())
			if err != nil {
				t.Fatalf("newFlowLaunchContext: %v", err)
			}
			if ctx != variant.want {
				t.Fatalf("context = %#v, want %#v", ctx, variant.want)
			}
			if route != variant.route {
				t.Fatalf("route = %v, want %v", route, variant.route)
			}
		})
	}
}

func TestNewFlowLaunchContextStampsThePinnedBuild(t *testing.T) {
	claims := stubRetainLaunchPin(t)
	settings := launchContextVariantSettings()
	settings.Pin = controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
		Digest:         "abc123def456",
	}

	ctx, _, err := newFlowLaunchContext(worktreeAgentTarget{
		LaunchID: "launch-1",
		Record:   launchContextVariantRecord(),
	}, settings)
	if err != nil {
		t.Fatalf("newFlowLaunchContext: %v", err)
	}
	if ctx.Executable != settings.Pin.ExecutablePath || ctx.BuildVersion != "v0.10.3" || ctx.DBSchemaVersion != 6 {
		t.Fatalf("builder did not stamp the pin: %#v", ctx)
	}
	if len(*claims) != 1 || (*claims)[0] != [3]string{"/state", "launch-1", "abc123def456"} {
		t.Fatalf("pin claims = %v, want exactly one for this launch", *claims)
	}
}

func TestNewFlowLaunchContextRejectsIncompletePayloads(t *testing.T) {
	for _, missing := range []struct {
		name   string
		target worktreeAgentTarget
	}{
		{
			name:   "launch id",
			target: worktreeAgentTarget{Record: launchContextVariantRecord()},
		},
		{
			name: "flow id",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.FlowID = "  "
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
		{
			name: "worktree path",
			target: func() worktreeAgentTarget {
				record := launchContextVariantRecord()
				record.WorktreePath = ""
				return worktreeAgentTarget{LaunchID: "launch-1", Record: record}
			}(),
		},
	} {
		t.Run("missing "+missing.name, func(t *testing.T) {
			ctx, route, err := newFlowLaunchContext(missing.target, launchContextVariantSettings())
			if err == nil {
				t.Fatalf("incomplete payload built a context: %#v", ctx)
			}
			if route != 0 {
				t.Fatalf("failed build returned route %v", route)
			}
		})
	}
}
