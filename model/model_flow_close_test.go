package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/ui"
)

func closeTestClosedAt() time.Time {
	return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
}

// closableFlow is an ordinary in-progress Flow with a ready phase, so every key
// AC4 blocks is genuinely offered before the close.
func closableFlow() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-1",
		Title:        "Closable Flow",
		Status:       flowstore.StatusInProgress,
		Branch:       "flow/closable",
		AutoMode:     false,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted, Order: 1, DependsOn: []string{}},
			{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 2, DependsOn: []string{}},
		},
	}
}

func closedFlowFrom(record flowstore.FlowRecord, reason string) flowstore.FlowRecord {
	closedAt := closeTestClosedAt()
	closed := record
	closed.Closed = flowstore.Closure{Reason: reason, ClosedAt: &closedAt}
	closed.Status = flowstore.StatusClosed
	closed.UpdatedAt = closedAt
	return closed
}

func pressC(m model.Model) (model.Model, tea.Cmd) {
	return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
}

func typeModalInput(m model.Model, text string) model.Model {
	for _, r := range text {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestModel_CKeyOpensCloseReasonModalForSelectedFlow(t *testing.T) {
	flow := closableFlow()
	m := newTestModel(testRepos(), model.Options{})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := pressC(m)
	if cmd != nil {
		t.Fatalf("opening the close modal returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayInput {
		t.Fatalf("overlay = %d, want an input modal", m.Overlay())
	}
	prompt := m.ConfirmPrompt()
	if !strings.Contains(prompt, "Closable Flow") || !strings.Contains(prompt, "flow-1") {
		t.Fatalf("close prompt = %q, want it to name the Flow and its id", prompt)
	}
}

func TestModel_CloseFlowModalRejectsBlankReason(t *testing.T) {
	flow := closableFlow()
	calls := 0
	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			calls++
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, _ = pressC(m)
	m = typeModalInput(m, "   ")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("submitting a blank reason returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayInput {
		t.Fatalf("overlay = %d, want the close modal to stay open", m.Overlay())
	}
	if m.WorktreeInputErr() == "" {
		t.Fatal("blank reason should set an inline validation error")
	}
	if calls != 0 {
		t.Fatalf("CloseFlow called %d times for a blank reason, want 0", calls)
	}
}

func TestModel_CloseFlowPersistsReasonAndRerendersRow(t *testing.T) {
	flow := closableFlow()
	closed := closedFlowFrom(flow, "superseded by #42")
	var updates []flowstore.ClosureUpdate
	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(update flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return closed, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, _ = pressC(m)
	m = typeModalInput(m, "  superseded by #42  ")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a reason should return a command")
	}
	raw := cmd()
	if _, ok := raw.(model.FlowEmbeddedLaunchRequestedMsg); ok {
		t.Fatal("closing a Flow must not launch a phase")
	}
	if _, ok := raw.(model.FlowClosedMsg); !ok {
		t.Fatalf("close command returned %T, want FlowClosedMsg", raw)
	}
	m, _ = update(m, raw)

	if len(updates) != 1 || updates[0].FlowID != "flow-1" || updates[0].Reason != "superseded by #42" {
		t.Fatalf("CloseFlow calls = %#v, want one trimmed reason for flow-1", updates)
	}
	got := m.Flows()
	if len(got) != 1 || got[0].Status != flowstore.StatusClosed || got[0].Closed.Reason != "superseded by #42" {
		t.Fatalf("Flows() = %#v, want the closed replacement", got)
	}
}

func TestModel_CloseFlowFailureSetsStatus(t *testing.T) {
	flow := closableFlow()
	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("disk on fire")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, _ = pressC(m)
	m = typeModalInput(m, "no longer needed")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a reason should return a command")
	}
	raw := cmd()
	if _, ok := raw.(model.FlowCloseFailedMsg); !ok {
		t.Fatalf("failed close returned %T, want FlowCloseFailedMsg", raw)
	}
	m, _ = update(m, raw)
	if !strings.Contains(m.TransientError(), "disk on fire") {
		t.Fatalf("status = %q, want the store error", m.TransientError())
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusInProgress {
		t.Fatalf("Flows() = %#v, want the record left open", got)
	}
}

func TestModel_CloseFlowRemovesRowFromActiveFlows(t *testing.T) {
	first := closableFlow()
	second := closableFlow()
	second.FlowID = "flow-2"
	second.Title = "Second Flow"
	closed := closedFlowFrom(first, "done with this")

	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			return closed, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{first, second})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

	m, _ = pressC(m)
	if m.Overlay() != ui.OverlayInput {
		t.Fatalf("overlay = %d, want the close modal from the Active Flows surface", m.Overlay())
	}
	m = typeModalInput(m, "done with this")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a reason should return a command")
	}
	m, _ = update(m, cmd())

	active := model.ActiveFlowsForTest(m)
	if len(active) != 1 || active[0].FlowID != "flow-2" {
		t.Fatalf("Active Flows = %#v, want the closed row removed", active)
	}
}

func TestModel_CloseFlowRemovesLastActiveFlowRowWithoutPanicking(t *testing.T) {
	flow := closableFlow()
	closed := closedFlowFrom(flow, "the only one")
	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			return closed, nil
		},
	})
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{flow})

	m, _ = pressC(m)
	m = typeModalInput(m, "the only one")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a reason should return a command")
	}
	m, _ = update(m, cmd())

	if active := model.ActiveFlowsForTest(m); len(active) != 0 {
		t.Fatalf("Active Flows = %#v, want empty", active)
	}
	if view := m.View(); view == "" {
		t.Fatal("rendering an emptied Active Flows surface produced no view")
	}
}

func TestModel_CKeyReopensClosedFlowThroughConfirm(t *testing.T) {
	flow := closableFlow()
	closed := closedFlowFrom(flow, "paused for now")
	reopened := flow
	reopened.UpdatedAt = closeTestClosedAt().Add(time.Hour)

	var reopenIDs []string
	m := newTestModel(testRepos(), model.Options{
		ReopenFlow: func(flowID string) (flowstore.FlowRecord, error) {
			reopenIDs = append(reopenIDs, flowID)
			return reopened, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})

	m, cmd := pressC(m)
	if cmd != nil {
		t.Fatalf("opening the reopen confirm returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("overlay = %d, want a confirm modal", m.Overlay())
	}
	if prompt := m.ConfirmPrompt(); !strings.Contains(prompt, "Reopen Flow") || !strings.Contains(prompt, "Closable Flow") {
		t.Fatalf("reopen prompt = %q, want it to name the Flow", prompt)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming a reopen should return a command")
	}
	m, _ = update(m, cmd())

	if len(reopenIDs) != 1 || reopenIDs[0] != "flow-1" {
		t.Fatalf("ReopenFlow calls = %#v, want one for flow-1", reopenIDs)
	}
	got := m.Flows()
	if len(got) != 1 || got[0].Status != flowstore.StatusInProgress || got[0].Closed.ClosedAt != nil {
		t.Fatalf("Flows() = %#v, want the reopened record at its prior status", got)
	}
}

func TestModel_ReopenConfirmDeclinedLeavesFlowClosed(t *testing.T) {
	closed := closedFlowFrom(closableFlow(), "paused for now")
	calls := 0
	m := newTestModel(testRepos(), model.Options{
		ReopenFlow: func(string) (flowstore.FlowRecord, error) {
			calls++
			return flowstore.FlowRecord{}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})

	m, _ = pressC(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatalf("declining the reopen returned command %T, want nil", cmd)
	}
	if calls != 0 {
		t.Fatalf("ReopenFlow called %d times after declining, want 0", calls)
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusClosed {
		t.Fatalf("Flows() = %#v, want the record still closed", got)
	}
}

func TestModel_ReopenFromFlowsPaneReadmitsRowToActiveFlows(t *testing.T) {
	flow := closableFlow()
	closed := closedFlowFrom(flow, "paused for now")
	reopened := flow
	reopened.UpdatedAt = closeTestClosedAt().Add(time.Hour)

	m := newTestModel(testRepos(), model.Options{
		ReopenFlow: func(string) (flowstore.FlowRecord, error) {
			return reopened, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})
	// Seed the unfiltered Active Flows cache with the closed record, then return
	// to the flows pane — the only surface a closed Flow is selectable from.
	m = enterActiveFlowsWithRecords(t, m, []flowstore.FlowRecord{closed})
	if active := model.ActiveFlowsForTest(m); len(active) != 0 {
		t.Fatalf("Active Flows = %#v, want the closed row hidden before the reopen", active)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	m, _ = pressC(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("confirming a reopen should return a command")
	}
	m, _ = update(m, cmd())

	active := model.ActiveFlowsForTest(m)
	if len(active) != 1 || active[0].FlowID != "flow-1" {
		t.Fatalf("Active Flows = %#v, want the reopened row re-admitted", active)
	}
}

func TestModel_CKeyIsInertWhereClosingDoesNotApply(t *testing.T) {
	merged := closableFlow()
	merged.Status = flowstore.StatusMerged
	merged.Merge = flowstore.Merge{Status: flowstore.MergeMerged}
	abandoned := closableFlow()
	abandoned.Status = flowstore.StatusAbandoned

	tests := []struct {
		name  string
		build func(t *testing.T) model.Model
	}{
		{
			name: "phase row selected",
			build: func(t *testing.T) model.Model {
				m := newTestModel(testRepos(), model.Options{
					CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
						t.Fatal("CloseFlow must not run for a selected phase row")
						return flowstore.FlowRecord{}, nil
					},
				})
				m = flowsInRightPane(t, m, []flowstore.FlowRecord{closableFlow()})
				return selectFlowPhaseByID(t, m, "implementation")
			},
		},
		{
			name: "no Flow selected",
			build: func(t *testing.T) model.Model {
				m := newTestModel(testRepos(), model.Options{
					CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
						t.Fatal("CloseFlow must not run with no Flow selected")
						return flowstore.FlowRecord{}, nil
					},
				})
				return flowsInRightPane(t, m, nil)
			},
		},
		{
			name: "merged Flow",
			build: func(t *testing.T) model.Model {
				m := newTestModel(testRepos(), model.Options{
					CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
						t.Fatal("CloseFlow must not run for a merged Flow")
						return flowstore.FlowRecord{}, nil
					},
				})
				return flowsInRightPane(t, m, []flowstore.FlowRecord{merged})
			},
		},
		{
			name: "abandoned Flow",
			build: func(t *testing.T) model.Model {
				m := newTestModel(testRepos(), model.Options{
					CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
						t.Fatal("CloseFlow must not run for an abandoned Flow")
						return flowstore.FlowRecord{}, nil
					},
				})
				return flowsInRightPane(t, m, []flowstore.FlowRecord{abandoned})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			m, cmd := pressC(m)
			if cmd != nil {
				t.Fatalf("C returned command %T, want nil", cmd)
			}
			if m.Overlay() != ui.OverlayNone {
				t.Fatalf("C opened overlay %d, want none", m.Overlay())
			}
		})
	}
}

// A close must not depend on the Flow being idle: a running phase with a live
// embedded terminal keeps running, and the guards only block new launches.
func TestModel_CloseSucceedsWithRunningPhase(t *testing.T) {
	flow := closableFlow()
	flow.Phases[1].Status = flowstore.PhaseRunning
	flow.Phases[1].LaunchIDs = []string{"launch-1"}
	flow.Phases[1].Sessions = []flowstore.Session{{
		Provider: "codex", SessionID: "s1", LaunchID: "launch-1", Status: "last_seen",
	}}
	closed := closedFlowFrom(flow, "stopping this work")

	m := newTestModel(testRepos(), model.Options{
		CloseFlow: func(flowstore.ClosureUpdate) (flowstore.FlowRecord, error) {
			return closed, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, _ = pressC(m)
	m = typeModalInput(m, "stopping this work")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submitting a reason should return a command")
	}
	m, _ = update(m, cmd())

	got := m.Flows()
	if len(got) != 1 || got[0].Status != flowstore.StatusClosed {
		t.Fatalf("Flows() = %#v, want the closed replacement", got)
	}
	if phase := got[0].Phases[1]; phase.Status != flowstore.PhaseRunning {
		t.Fatalf("phase status = %q, want the running session untouched", phase.Status)
	}
}

// AC7 end to end: no key a closed Flow refuses is advertised in the footer.
func TestModel_ClosedFlowAdvertisesNoBlockedKeys(t *testing.T) {
	closed := closedFlowFrom(closableFlow(), "closed for the hint test")
	closed.PR = flowstore.PullRequest{
		Provider:   "github",
		Number:     116,
		URL:        "https://github.com/approachcontrol/approach/pull/116",
		HeadBranch: "flow/closable",
		BaseBranch: "main",
	}
	closed.Phases = append(closed.Phases, flowstore.FlowPhase{
		PhaseID: "merge", Title: "Merge", Kind: flowstore.KindMerge,
		Status: flowstore.PhaseReady, Order: 3, DependsOn: []string{},
	})
	closed.Phases[1].Status = flowstore.PhaseRunning
	closed.Phases[1].LaunchIDs = []string{"launch-1"}
	closed.Phases[1].Sessions = []flowstore.Session{{
		Provider: "codex", SessionID: "s1", LaunchID: "launch-1", Status: "ended",
	}}

	m := newTestModel(testRepos(), model.Options{AgentCommand: "codex"})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})
	m, _ = update(m, tea.WindowSizeMsg{Width: 400, Height: 40})

	blocked := []string{"launch next", "repair", "mark merged", "auto:"}
	view := ansi.Strip(m.View())
	for _, label := range blocked {
		if strings.Contains(view, label) {
			t.Fatalf("closed Flow row advertises blocked key %q:\n%s", label, view)
		}
	}
	if !strings.Contains(view, "C") || !strings.Contains(view, "reopen") {
		t.Fatalf("closed Flow row should offer the reopen shortcut:\n%s", view)
	}

	m = selectFlowPhaseByID(t, m, "implementation")
	phaseView := ansi.Strip(m.View())
	for _, label := range append(blocked, "resume", "reset ready") {
		if strings.Contains(phaseView, label) {
			t.Fatalf("closed Flow phase row advertises blocked key %q:\n%s", label, phaseView)
		}
	}
	if !strings.Contains(phaseView, "headless") {
		t.Fatalf("closed Flow should still offer the headless preference:\n%s", phaseView)
	}
}

// r, a and x prepare a Flow to run again, so a closed Flow must refuse all
// three. h is a pure preference and stays available.
func TestModel_ClosedFlowRefusesRelaunchKeysButKeepsHeadless(t *testing.T) {
	closed := closedFlowFrom(closableFlow(), "closed for the key test")
	closed.Phases[1].Status = flowstore.PhaseRunning
	closed.Phases[1].LaunchIDs = []string{"launch-1"}
	closed.Phases[1].Sessions = []flowstore.Session{{
		Provider: "codex", SessionID: "s1", LaunchID: "launch-1", Status: "ended",
	}}

	for _, key := range []rune{'r', 'a', 'x'} {
		t.Run(string(key), func(t *testing.T) {
			m := newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				SetFlowAutoMode: func(flowstore.AutoModeUpdate) (flowstore.FlowRecord, error) {
					t.Fatalf("%c must not toggle auto mode on a closed Flow", key)
					return flowstore.FlowRecord{}, nil
				},
				ResetFlowPhase: func(flowstore.PhaseResetUpdate) (flowstore.FlowRecord, error) {
					t.Fatalf("%c must not reset a phase on a closed Flow", key)
					return flowstore.FlowRecord{}, nil
				},
				AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					t.Fatalf("%c must not record a launch on a closed Flow", key)
					return flowstore.FlowRecord{}, nil
				},
			})
			m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			if cmd != nil {
				if _, ok := cmd().(model.FlowEmbeddedLaunchRequestedMsg); ok {
					t.Fatalf("%c launched an agent on a closed Flow", key)
				}
			}
			if m.Overlay() != ui.OverlayNone {
				t.Fatalf("%c opened overlay %d on a closed Flow, want none", key, m.Overlay())
			}
		})
	}

	t.Run("h still toggles", func(t *testing.T) {
		var updates []flowstore.HeadlessUpdate
		m := newTestModel(testRepos(), model.Options{
			SetFlowHeadless: func(update flowstore.HeadlessUpdate) (flowstore.FlowRecord, error) {
				updates = append(updates, update)
				return closed, nil
			},
		})
		m = flowsInRightPane(t, m, []flowstore.FlowRecord{closed})

		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
		if cmd == nil {
			t.Fatal("h on a closed Flow should still return a command")
		}
		m, _ = update(m, cmd())
		if len(updates) != 1 || updates[0].FlowID != "flow-1" {
			t.Fatalf("SetFlowHeadless calls = %#v, want one for flow-1", updates)
		}
	})
}
