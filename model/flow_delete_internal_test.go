package model

import (
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func TestConfirmFlowDeleteAllowsStalePhaseSelectionOnly(t *testing.T) {
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{})
	m.bottomMode = ui.ModeFlows
	m.contentPane = ui.PaneBottom
	m.activeFlowSurface = false
	m.activePane = m.contentPane
	m.destructive = true
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Delete me",
		Status:   flowstore.StatusPending,
		Phases: []flowstore.FlowPhase{{
			PhaseID: "plan",
			Title:   "Plan",
			Status:  flowstore.PhaseReady,
		}},
	}})
	m.expandedFlowID = "flow-1"
	m.selectedFlowPhaseID = "missing"

	next, cmd := m.confirmFlowDelete()
	if cmd != nil {
		t.Fatalf("opening Flow delete confirm returned command %T, want nil", cmd)
	}
	if next.(Model).Overlay() != ui.OverlayConfirm {
		t.Fatalf("stale selected phase overlay = %d, want confirm", next.(Model).Overlay())
	}

	m.selectedFlowPhaseID = "plan"
	next, cmd = m.confirmFlowDelete()
	if cmd != nil {
		t.Fatalf("d on real selected Flow phase returned command %T, want nil", cmd)
	}
	if next.(Model).Overlay() != ui.OverlayNone {
		t.Fatalf("real selected phase overlay = %d, want none", next.(Model).Overlay())
	}
}
