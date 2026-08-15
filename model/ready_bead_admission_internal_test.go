package model

import (
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func TestStaleReadyBeadLaunchCleanupOwnsAdmissionUntilPersistenceCompletes(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateLaunchID)
	m = m.invalidateReadyBeadFlowCreateRequest()

	m, cmd = m.handleFlowLaunchEvent(event)
	if cmd == nil || !m.flowPreparationAdmission {
		t.Fatalf("stale launch recovery = cmd %v, admission %t; want persistence while admission remains held", cmd != nil, m.flowPreparationAdmission)
	}

	recovered, ok := cmd().(flowLaunchEventMsg)
	if !ok || recovered.Stage != flowLaunchStageCreateRecovered {
		t.Fatalf("cleanup command returned %#v; want create recovery result", recovered)
	}
	m, _ = m.handleFlowLaunchEvent(recovered)
	if m.flowPreparationAdmission {
		t.Fatal("cleanup result did not release Ready admission")
	}
}
