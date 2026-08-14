package model

import (
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
)

func TestStaleReadyBeadLaunchCleanupOwnsAdmissionUntilPersistenceCompletes(t *testing.T) {
	for _, tt := range []struct {
		name          string
		activeRequest uint64
	}{
		{name: "request invalidated", activeRequest: 0},
		{name: "current request changed repository", activeRequest: 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			releases := 0
			m := Model{
				activeReadyBeadFlowCreate: tt.activeRequest,
				flowPreparationAdmission:  true,
				setFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
			}
			ctx := actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan", RepoPath: "/other-repo"}
			next, accepted, cmd := m.acceptCreationTimeFlowLaunch(ctx, 0, 7, func() { releases++ })
			if accepted || cmd == nil {
				t.Fatalf("acceptCreationTimeFlowLaunch() = accepted %t, cmd %v; want rejected cleanup", accepted, cmd)
			}
			if !next.flowPreparationAdmission || next.activeReadyBeadFlowCreate != 0 {
				t.Fatalf("rejected handoff state = admission %t, request %d; want held admission and cleared request", next.flowPreparationAdmission, next.activeReadyBeadFlowCreate)
			}

			msg, ok := cmd().(flowLaunchFailurePersistedMsg)
			if !ok {
				t.Fatalf("cleanup command returned unexpected message")
			}
			if releases != 1 || !next.flowPreparationAdmission {
				t.Fatalf("post-persistence command state = releases %d, admission %t; want reservation released but admission held", releases, next.flowPreparationAdmission)
			}
			next, _ = next.handleFlowLaunchFailurePersisted(msg)
			if next.flowPreparationAdmission {
				t.Fatal("cleanup result did not release Ready admission")
			}
		})
	}
}
