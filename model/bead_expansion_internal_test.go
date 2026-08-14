package model

import (
	"errors"
	"testing"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
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

func TestBeadExpansionKeepsProgressionIndependentFromChildAndReadinessFailures(t *testing.T) {
	index, _ := beadSubviewIndex(ui.ModeBeadsOpen)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	base := Model{
		topMode:       ui.ModeBeadsOpen,
		beads:         newBeadSubviews(),
		beadExpansion: beadExpansionSnapshot{target: target, projection: ui.BeadExpansion{EpicID: "epic", State: ui.BeadExpansionLoading}},
	}
	base.beads[index].available = true
	base.beads[index].repoPath = "/repo"
	base.beads[index].pane = base.beads[index].pane.SetItems([]beadsquery.Bead{{ID: "epic", IssueType: "epic"}})

	enabled := flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Enabled: true}
	next := base.handleBeadExpansionResult(beadExpansionResultMsg{
		target: target, childrenErr: errors.New("children failed"), progression: enabled, progressionFound: true,
	})
	if next.beadExpansion.projection.State != ui.BeadExpansionError || !next.beadExpansion.projection.ProgressionKnown || !next.beadExpansion.projection.ProgressionEnabled {
		t.Fatalf("child failure lost known progression: %#v", next.beadExpansion.projection)
	}

	next = base.handleBeadExpansionResult(beadExpansionResultMsg{
		target:   target,
		children: []beadsquery.Bead{{ID: "child"}}, ready: []beadsquery.Bead{{ID: "child"}},
		progressionErr: errors.New("progression failed"),
	})
	if next.beadExpansion.projection.State != ui.BeadExpansionLoaded || !next.beadExpansion.projection.ReadinessKnown || next.beadExpansion.projection.ProgressionKnown {
		t.Fatalf("progression failure damaged child/readiness projection: %#v", next.beadExpansion.projection)
	}
}

func TestUnknownToggleResultReflowsProgressionWarningHeight(t *testing.T) {
	index, _ := beadSubviewIndex(ui.ModeBeadsOpen)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	m := Model{
		topMode:       ui.ModeBeadsOpen,
		beads:         newBeadSubviews(),
		beadExpansion: beadExpansionSnapshot{target: target, projection: ui.BeadExpansion{EpicID: "epic", State: ui.BeadExpansionLoaded, ReadinessKnown: true}},
	}
	m.beads[index].available = true
	m.beads[index].repoPath = "/repo"
	m.beads[index].pane = m.beads[index].pane.SetItems([]beadsquery.Bead{{ID: "epic", IssueType: "epic"}})
	m = m.reflowBeadExpansionPane()

	next, _ := m.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target: target, status: "database unavailable",
	})
	probe := next.beads[index].pane.ScrollBy(999, 1, 80)
	_, _, scroll := probe.View()
	if scroll != 2 {
		t.Fatalf("warning-height max scroll = %d, want 2 for three rendered lines", scroll)
	}
}
