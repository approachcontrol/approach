package model

import (
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestPRBabysitterEligible(t *testing.T) {
	base := flowstore.FlowRecord{
		Status: flowstore.StatusInProgress,
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     42,
			URL:        "https://github.com/acme/project/pull/42",
			HeadBranch: "flow/feature",
			BaseBranch: "main",
		},
		Merge: flowstore.Merge{Status: flowstore.MergePending},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseCompleted},
			{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, DependsOn: []string{"implementation"}},
		},
	}

	clone := func(mutate func(*flowstore.FlowRecord)) flowstore.FlowRecord {
		record := base
		record.Phases = append([]flowstore.FlowPhase(nil), base.Phases...)
		mutate(&record)
		return record
	}

	tests := []struct {
		name   string
		record flowstore.FlowRecord
		want   bool
	}{
		{name: "ready merge", record: base, want: true},
		{name: "completed manual boundary", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = flowstore.PhaseCompleted }), want: true},
		{name: "blocked pending despite regressed predecessor", record: clone(func(r *flowstore.FlowRecord) {
			r.Phases[0].Status = flowstore.PhaseRunning
			r.Phases[1].Status = flowstore.PhaseBlocked
		}), want: true},
		{name: "blocked recorded merge block", record: clone(func(r *flowstore.FlowRecord) {
			r.Phases[1].Status = flowstore.PhaseBlocked
			r.Merge.Status = flowstore.MergeBlocked
		}), want: true},
		{name: "ready with regressed predecessor", record: clone(func(r *flowstore.FlowRecord) { r.Phases[0].Status = flowstore.PhaseRunning })},
		{name: "pending phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = flowstore.PhasePending })},
		{name: "running phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = flowstore.PhaseRunning })},
		{name: "needs attention phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = flowstore.PhaseNeedsAttention })},
		{name: "skipped phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = flowstore.PhaseSkipped })},
		{name: "unknown phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Status = "mystery" })},
		{name: "merge already merged", record: clone(func(r *flowstore.FlowRecord) { r.Merge.Status = flowstore.MergeMerged })},
		{name: "missing PR", record: clone(func(r *flowstore.FlowRecord) { r.PR = flowstore.PullRequest{} })},
		{name: "derived merged despite stale stored status", record: clone(func(r *flowstore.FlowRecord) {
			r.Merge.Status = flowstore.MergeMerged
			r.Merge.Commit = "abcdef"
		})},
		{name: "closed", record: clone(func(r *flowstore.FlowRecord) {
			now := time.Now()
			r.Closed = flowstore.Closure{Reason: "done", ClosedAt: &now}
		})},
		{name: "abandoned", record: clone(func(r *flowstore.FlowRecord) { r.Status = flowstore.StatusAbandoned })},
		{name: "missing merge phase", record: clone(func(r *flowstore.FlowRecord) { r.Phases = r.Phases[:1] })},
		{name: "duplicate merge semantics", record: clone(func(r *flowstore.FlowRecord) {
			r.Phases = append(r.Phases, flowstore.FlowPhase{PhaseID: "ship", Kind: flowstore.KindMerge, Status: flowstore.PhaseBlocked})
		})},
		{name: "child merge", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].ParentPhaseID = "implementation" })},
		{name: "kind wins over merge id", record: clone(func(r *flowstore.FlowRecord) { r.Phases[1].Kind = flowstore.KindImplementation })},
		{name: "unresolved recovered graph", record: clone(func(r *flowstore.FlowRecord) {
			r.GraphRecovery.Status = flowstore.GraphRecoveryMissingEdgesUnresolved
		})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := prBabysitterEligible(tc.record); got != tc.want {
				t.Fatalf("prBabysitterEligible() = %v, want %v", got, tc.want)
			}
		})
	}
}
