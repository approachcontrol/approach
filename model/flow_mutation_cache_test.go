package model

import (
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

// A repository Flow fetch can be issued while an Active Flows fetch is still
// outstanding, so acknowledging by the newer request alone would retire a
// mutation the older request still needs.
func TestPruneAcknowledgedFlowMutationsWaitsForEverySurface(t *testing.T) {
	mutation := cachedFlowMutation{
		record:     flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha"},
		generation: 10,
	}

	for _, tt := range []struct {
		name          string
		flowsRequest  uint64
		activeRequest uint64
		activeVisible bool
		wantRetained  bool
	}{
		{name: "outstanding Active Flows request", flowsRequest: 20, activeRequest: 5, activeVisible: true, wantRetained: true},
		{name: "outstanding repository request", flowsRequest: 5, activeRequest: 20, activeVisible: true, wantRetained: true},
		{name: "both surfaces refetched", flowsRequest: 20, activeRequest: 15, activeVisible: true, wantRetained: false},
		{name: "acknowledging request equals generation", flowsRequest: 10, activeRequest: 20, activeVisible: true, wantRetained: true},
		{name: "hidden Active Flows surface does not block pruning", flowsRequest: 20, activeRequest: 5, wantRetained: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m Model
			m.activeFlowSurface = tt.activeVisible
			m.listRequests[int(ui.ModeFlows)] = tt.flowsRequest
			m.listRequests[int(ui.ModeActiveFlows)] = tt.activeRequest
			m.latestFlowMutations = []cachedFlowMutation{mutation}

			m = m.pruneAcknowledgedFlowMutations()

			if retained := len(m.latestFlowMutations) == 1; retained != tt.wantRetained {
				t.Fatalf("retained = %v, want %v (mutations = %#v)", retained, tt.wantRetained, m.latestFlowMutations)
			}
		})
	}
}

func TestPruneAcknowledgedFlowMutationsRetainsOverlayOrderingWatermark(t *testing.T) {
	var m Model
	m.listRequests[int(ui.ModeFlows)] = 20
	m.latestFlowMutations = []cachedFlowMutation{{
		record: flowstore.FlowRecord{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
		},
		generation: 10,
		field:      flowPhaseAgentSettingsMutationField("implementation"),
	}}

	m = m.pruneAcknowledgedFlowMutations()

	if len(m.latestFlowMutations) != 1 {
		t.Fatalf("overlay ordering watermark was pruned: %#v", m.latestFlowMutations)
	}
}
