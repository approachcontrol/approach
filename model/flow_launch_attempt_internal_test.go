package model

import (
	"testing"

	"github.com/approachcontrol/approach/scanner"
)

func attemptTestModel() Model {
	return New([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}})
}

func TestFlowLaunchAttemptReservationIsExclusivePerFlow(t *testing.T) {
	m := attemptTestModel()
	m, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-1", Kind: flowLaunchKindManualPhase, FlowID: "flow-1"}, flowLaunchStateReserved)
	if !ok {
		t.Fatal("first reservation should succeed")
	}
	if _, again := m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-2", Kind: flowLaunchKindManualPhase, FlowID: "flow-1"}, flowLaunchStateReserved); again {
		t.Fatal("a second reservation for the same Flow must fail")
	}
	if _, other := m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-3", Kind: flowLaunchKindManualPhase, FlowID: "flow-2"}, flowLaunchStateReserved); !other {
		t.Fatal("another Flow must still be reservable")
	}
}

func TestFlowLaunchAttemptRejectsBlankIdentifiers(t *testing.T) {
	m := attemptTestModel()
	if _, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-1"}, flowLaunchStateReserved); ok {
		t.Fatal("a blank Flow ID must not reserve")
	}
	if _, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{FlowID: "flow-1"}, flowLaunchStateReserved); ok {
		t.Fatal("a blank token must not reserve")
	}
	if m.flowLaunchAttemptOccupied("") {
		t.Fatal("a blank Flow ID is never occupied")
	}
}

func TestFlowLaunchAttemptTransitionsAreFenced(t *testing.T) {
	m := attemptTestModel()
	m, _ = m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-1", Kind: flowLaunchKindManualPhase, FlowID: "flow-1"}, flowLaunchStateReserved)

	if _, ok := m.transitionFlowLaunchAttempt("flow-1", "token-2", flowLaunchStateReserved, flowLaunchStateReading); ok {
		t.Fatal("a wrong token must not transition the attempt")
	}
	if _, ok := m.transitionFlowLaunchAttempt("flow-1", "token-1", flowLaunchStatePreparing, flowLaunchStateReading); ok {
		t.Fatal("a wrong source state must not transition the attempt")
	}

	next, ok := m.transitionFlowLaunchAttempt("flow-1", "token-1", flowLaunchStateReserved, flowLaunchStateReading)
	if !ok {
		t.Fatal("the matching transition should apply")
	}
	attempt, _ := next.flowLaunchAttempt("flow-1")
	if attempt.State != flowLaunchStateReading {
		t.Fatalf("state = %v, want reading", attempt.State)
	}
	if _, ok := next.matchingFlowLaunchAttempt("flow-1", "token-1", flowLaunchKindAutoPhase, flowLaunchStateReading); ok {
		t.Fatal("a wrong kind must not match")
	}
}

func TestFlowLaunchAttemptReleaseIsTokenOnly(t *testing.T) {
	m := attemptTestModel()
	m, _ = m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-1", Kind: flowLaunchKindManualPhase, FlowID: "flow-1"}, flowLaunchStateReserved)

	if stale := m.releaseFlowLaunchAttempt("flow-1", "token-superseded"); !stale.flowLaunchAttemptOccupied("flow-1") {
		t.Fatal("a superseded token must not free a live attempt")
	}
	if freed := m.releaseFlowLaunchAttempt("flow-1", "token-1"); freed.flowLaunchAttemptOccupied("flow-1") {
		t.Fatal("the matching token should free the Flow")
	}
}

func TestFlowLaunchAttemptMutatedPhaseIsRecordedForTheOwner(t *testing.T) {
	m := attemptTestModel()
	m, _ = m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "token-1", Kind: flowLaunchKindManualPhase, FlowID: "flow-1"}, flowLaunchStatePreparing)

	if other := m.markFlowLaunchAttemptMutatedPhase("flow-1", "token-2"); func() bool {
		attempt, _ := other.flowLaunchAttempt("flow-1")
		return attempt.MutatedPhase
	}() {
		t.Fatal("a foreign token must not mark the attempt")
	}

	m = m.markFlowLaunchAttemptMutatedPhase("flow-1", "token-1")
	attempt, _ := m.flowLaunchAttempt("flow-1")
	if !attempt.MutatedPhase {
		t.Fatal("the owning token should mark the attempt")
	}
}
