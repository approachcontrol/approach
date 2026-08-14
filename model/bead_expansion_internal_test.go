package model

import (
	"testing"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/ui"
)

func TestSelectStoredTopModeClearsBeadExpansion(t *testing.T) {
	m := Model{
		topMode: ui.ModeBeadsOpen,
		beadExpansion: beadExpansionSnapshot{
			target: beadExpansionTarget{token: 1, mode: ui.ModeBeadsOpen, epicID: "epic"},
		},
	}

	next, ok := m.selectStoredMode(ui.ModeBranches)
	if !ok {
		t.Fatal("selectStoredMode(ModeBranches) rejected a stored mode")
	}
	if next.beadExpansion.target.token != 0 {
		t.Fatalf("programmatic top-mode exit retained expansion target: %#v", next.beadExpansion.target)
	}
}

func TestBeadExpansionResultRequiresCurrentTopSelection(t *testing.T) {
	index, _ := beadSubviewIndex(ui.ModeBeadsOpen)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	m := Model{
		topMode: ui.ModeBranches,
		beadExpansion: beadExpansionSnapshot{
			target: target,
			projection: ui.BeadExpansion{
				EpicID: "epic",
				State:  ui.BeadExpansionLoading,
			},
		},
	}
	m.beads[index].pane = newBeadPane().SetItems([]beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}})
	m.beads[index].available = true
	m.beads[index].repoPath = "/repo"

	next := m.handleBeadExpansionResult(beadExpansionResultMsg{
		target:   target,
		children: []beadsquery.Bead{{ID: "epic-child", Title: "Child"}},
	})
	if next.beadExpansion.projection.State != ui.BeadExpansionLoading || len(next.beadExpansion.projection.Children) != 0 {
		t.Fatalf("stale result mutated expansion after top-mode exit: %#v", next.beadExpansion.projection)
	}
}
