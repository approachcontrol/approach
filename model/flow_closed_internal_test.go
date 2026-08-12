package model

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func closedTestClosure() flowstore.Closure {
	closedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	return flowstore.Closure{Reason: "closing this line of work", ClosedAt: &closedAt}
}

func closedGuardRecord(phases ...flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-closed",
		Title:        "Closed Flow",
		RepoPath:     "/dev/approach",
		WorktreePath: "/dev/approach-worktrees/flow-closed",
		Branch:       "flow/closed",
		Status:       flowstore.StatusInProgress,
		Phases:       phases,
	}
}

// flowPhaseCanLaunchAtIndex has two branches that bypass PhaseLaunchEligible —
// a ready Merge phase and an autoreview rerun — so guarding the flowstore
// primitive alone leaves both open on a closed Flow.
func TestFlowPhaseCanLaunchAtIndexRefusesClosedFlow(t *testing.T) {
	prTarget := flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/closed",
		BaseBranch: "main",
	}
	tests := []struct {
		name   string
		record flowstore.FlowRecord
	}{
		{
			name: "ready implementation phase",
			record: closedGuardRecord(flowstore.FlowPhase{
				PhaseID: "implementation", Kind: flowstore.KindImplementation,
				Status: flowstore.PhaseReady, DependsOn: []string{},
			}),
		},
		{
			name: "ready merge phase",
			record: closedGuardRecord(flowstore.FlowPhase{
				PhaseID: "merge", Kind: flowstore.KindMerge,
				Status: flowstore.PhaseReady, DependsOn: []string{},
			}),
		},
		{
			name: "autoreview rerun with PR metadata",
			record: func() flowstore.FlowRecord {
				record := closedGuardRecord(flowstore.FlowPhase{
					PhaseID: "autoreview", Kind: flowstore.KindAutoreview,
					Status: flowstore.PhaseNeedsAttention, DependsOn: []string{},
				})
				record.PR = prTarget
				return record
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !flowPhaseCanLaunchAtIndex(tc.record, 0) {
				t.Fatal("fixture is not launchable before closing; the closed case would pass vacuously")
			}
			closed := tc.record
			closed.Closed = closedTestClosure()
			if flowPhaseCanLaunchAtIndex(closed, 0) {
				t.Fatal("flowPhaseCanLaunchAtIndex(closed) = true, want false")
			}
		})
	}
}

func TestFlowLaunchablePhaseRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation", Kind: flowstore.KindImplementation,
		Status: flowstore.PhaseReady, DependsOn: []string{},
	})
	if _, ok := flowLaunchablePhase(record, ""); !ok {
		t.Fatal("fixture has no launchable phase before closing")
	}
	record.Closed = closedTestClosure()
	if phase, ok := flowLaunchablePhase(record, ""); ok {
		t.Fatalf("flowLaunchablePhase(closed, any) = %q, true; want no phase", phase.PhaseID)
	}
	if phase, ok := flowLaunchablePhase(record, "implementation"); ok {
		t.Fatalf("flowLaunchablePhase(closed, implementation) = %q, true; want no phase", phase.PhaseID)
	}
}

// The auto-mode drain reaches the guarded flowstore primitive through
// nextAutoLaunchPhase, which is the route AC4's auto-mode clause depends on.
func TestNextAutoLaunchPhaseRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "implementation", Kind: flowstore.KindImplementation,
		Status: flowstore.PhaseReady, DependsOn: []string{}, Order: 1,
	})
	record.AutoMode = true
	if _, ok := nextAutoLaunchPhase(record); !ok {
		t.Fatal("fixture has no auto-launchable phase before closing")
	}
	record.Closed = closedTestClosure()
	if phase, ok := nextAutoLaunchPhase(record); ok {
		t.Fatalf("nextAutoLaunchPhase(closed) = %q, true; want no phase", phase.PhaseID)
	}
}

func TestFlowManualMergeEligibleRefusesClosedFlow(t *testing.T) {
	record := closedGuardRecord(flowstore.FlowPhase{
		PhaseID: "merge", Kind: flowstore.KindMerge,
		Status: flowstore.PhaseReady, DependsOn: []string{},
	})
	record.PR = flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/closed",
		BaseBranch: "main",
	}
	if !flowManualMergeEligible(record) {
		t.Fatal("fixture is not manual-merge eligible before closing")
	}
	record.Closed = closedTestClosure()
	if flowManualMergeEligible(record) {
		t.Fatal("flowManualMergeEligible(closed) = true, want false")
	}
}

func TestFlowSearchTextIncludesClosure(t *testing.T) {
	record := closedGuardRecord()
	record.Closed = closedTestClosure()
	text := flowSearchText(record)
	if !strings.Contains(text, "closing this line of work") {
		t.Fatalf("flowSearchText() = %q, want the close reason", text)
	}
	want := record.Closed.ClosedAt.UTC().Format(time.RFC3339)
	if !strings.Contains(text, want) {
		t.Fatalf("flowSearchText() = %q, want the formatted closed-at %q", text, want)
	}
}

func TestActiveFlowRecordsDropsClosedFlows(t *testing.T) {
	open := closedGuardRecord()
	open.FlowID = "flow-open"

	closed := closedGuardRecord()
	closed.FlowID = "flow-closed"
	closed.Closed = closedTestClosure()
	closed.Status = flowstore.StatusClosed

	merged := closedGuardRecord()
	merged.FlowID = "flow-merged"
	merged.Status = flowstore.StatusMerged

	active := activeFlowRecords([]flowstore.FlowRecord{open, closed, merged})
	if len(active) != 1 || active[0].FlowID != "flow-open" {
		t.Fatalf("activeFlowRecords() = %#v, want only the open Flow", active)
	}
}
