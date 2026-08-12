package flowstore_test

import (
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func TestPhaseIsActionable(t *testing.T) {
	actionable := []string{
		flowstore.PhaseReady,
		flowstore.PhaseRunning,
		flowstore.PhaseNeedsAttention,
		flowstore.PhaseBlocked,
	}
	for _, status := range actionable {
		if !flowstore.PhaseIsActionable(flowstore.FlowPhase{Status: status}) {
			t.Errorf("PhaseIsActionable(%q) = false, want true", status)
		}
	}
	inert := []string{
		flowstore.PhasePending,
		flowstore.PhaseCompleted,
		flowstore.PhaseSkipped,
		"",
		"bogus",
	}
	for _, status := range inert {
		if flowstore.PhaseIsActionable(flowstore.FlowPhase{Status: status}) {
			t.Errorf("PhaseIsActionable(%q) = true, want false", status)
		}
	}
}

func TestNextActionablePhaseReturnsFirstInGraphOrder(t *testing.T) {
	record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{
		{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1},
		{PhaseID: "implementation", Status: flowstore.PhaseBlocked, Order: 2},
		{PhaseID: "review-loop", Status: flowstore.PhaseReady, Order: 3},
	}}
	phase, ok := flowstore.NextActionablePhase(record)
	if !ok {
		t.Fatalf("NextActionablePhase() ok = false, want true")
	}
	if phase.PhaseID != "implementation" {
		t.Fatalf("NextActionablePhase() = %q, want %q", phase.PhaseID, "implementation")
	}
}

func TestNextActionablePhaseGroupsChildrenUnderParent(t *testing.T) {
	record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{
		{PhaseID: "implementation", Status: flowstore.PhaseCompleted, Order: 1},
		{PhaseID: "review-loop", Status: flowstore.PhaseReady, Order: 2},
		{PhaseID: "impl-child", ParentPhaseID: "implementation", Status: flowstore.PhaseReady, Order: 1},
	}}
	phase, ok := flowstore.NextActionablePhase(record)
	if !ok {
		t.Fatalf("NextActionablePhase() ok = false, want true")
	}
	if phase.PhaseID != "impl-child" {
		t.Fatalf("NextActionablePhase() = %q, want %q", phase.PhaseID, "impl-child")
	}
}

func TestNextActionablePhaseNoneWhenAllTerminal(t *testing.T) {
	record := flowstore.FlowRecord{Phases: []flowstore.FlowPhase{
		{PhaseID: "plan", Status: flowstore.PhaseCompleted},
		{PhaseID: "implementation", Status: flowstore.PhaseSkipped},
		{PhaseID: "merge", Status: flowstore.PhasePending},
	}}
	if phase, ok := flowstore.NextActionablePhase(record); ok {
		t.Fatalf("NextActionablePhase() = %q, true; want false", phase.PhaseID)
	}
	if _, ok := flowstore.NextActionablePhase(flowstore.FlowRecord{}); ok {
		t.Fatalf("NextActionablePhase(empty) ok = true, want false")
	}
}
