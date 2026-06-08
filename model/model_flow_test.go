package model_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func flowsInRightPane(t *testing.T, m model.Model, records []flowstore.FlowRecord) model.Model {
	t.Helper()
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 18})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: records, ListRequest: m.ListRequest(ui.ModeFlows)})
	return m
}

func TestModel_Key8SwitchesToFlowsAndFetches(t *testing.T) {
	var gotFilter flowstore.FlowFilter
	want := []flowstore.FlowRecord{
		{FlowID: "flow-1", Title: "Add Flow mode", RepoPath: "/dev/alpha", Branch: "flow/add-flow-mode", Status: flowstore.StatusPending},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %d, want flows", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	if gotFilter.RepoPath != "" {
		t.Fatalf("flow lister ran before command execution: %#v", gotFilter)
	}
	msg, ok := cmd().(model.FlowResultMsg)
	if !ok {
		t.Fatalf("expected FlowResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	got := m.Flows()
	if len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("Flows() = %#v, want %#v", got, want)
	}
}

func TestModel_ChangingRepoRefetchesFlowsMode(t *testing.T) {
	var filters []flowstore.FlowFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			filters = append(filters, filter)
			return []flowstore.FlowRecord{{FlowID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusPending}}, nil
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if cmd == nil {
		t.Fatal("expected initial flows fetch")
	}
	m, _ = update(m, cmd())
	if got := m.Flows(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Flows() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("expected nil cmd switching to repo pane, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected flows refetch after repo change")
	}
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("expected flows cleared before refetch, got %#v", got)
	}
	m, _ = update(m, cmd())
	if got := m.Flows(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Flows() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("flow filters = %#v", filters)
	}
}

func TestModel_StaleFlowResultIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{
		{FlowID: "stale", RepoPath: "/dev/alpha", Title: "T", Status: flowstore.StatusPending},
	}, ListRequest: 999999})
	if got := m.Flows(); len(got) != 0 {
		t.Fatalf("stale flow result should be ignored, got %#v", got)
	}
}

func TestModel_FlowListErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("flows unavailable")
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if cmd == nil {
		t.Fatal("expected flows fetch command")
	}
	m, _ = update(m, cmd())
	if got := m.View(); !strings.Contains(got, "failed to load flows") || !strings.Contains(got, "Could not load flows") {
		t.Fatalf("expected flow load error in view:\n%s", got)
	}
}

func TestModel_RightNavigationReachesFlowsWithoutChangingExistingModeNumbers(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	for _, key := range []rune{'1', '2', '3', '4', '5', '6', '7'} {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got := int(m.Mode()); got != int(key-'0') {
			t.Fatalf("key %c set mode %d, want %c", key, got, key)
		}
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	if m.Mode() != ui.ModeFlows || cmd == nil {
		t.Fatalf("key 8 mode=%d cmd=%v, want flows fetch", m.Mode(), cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("right from flows mode = %d, want still flows", m.Mode())
	}
	if cmd != nil {
		t.Fatalf("right from terminal mode should not fetch, got %T", cmd)
	}
}

func TestModel_FlowSearchIncludesPhasesAndMetadata(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})
	m, _ = update(m, model.FlowResultMsg{RepoPath: "/dev/alpha", Flows: []flowstore.FlowRecord{{
		FlowID:       "flow-1",
		Title:        "Add Flow mode",
		Status:       flowstore.StatusInProgress,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-mode",
		Branch:       "flow/add-flow-mode",
		PR:           flowstore.PullRequest{URL: "https://github.com/brian-bell/wtui/pull/123"},
		UpdatedAt:    time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady},
		},
	}}, ListRequest: m.ListRequest(ui.ModeFlows)})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Review")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.Flows(); len(got) != 1 || got[0].FlowID != "flow-1" {
		t.Fatalf("flow search by phase title should keep row, got %#v", got)
	}
}

func TestModel_OKeyOnFlowOpensLinkedPlanText(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadPlan: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("ReadPlan called with %q", planID)
			}
			return "# Flow plan\n\nfull body\n", nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:    "flow-1",
		RepoPath:  "/dev/alpha",
		Title:     "Linked flow",
		Status:    flowstore.StatusInProgress,
		PlanID:    "plan-1",
		PlanPath:  "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		UpdatedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC),
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("flows-mode o should return a plan read command for linked plan")
	}
	if m.Overlay() != ui.OverlayPlanText {
		t.Fatalf("expected plan text overlay, got %d", m.Overlay())
	}
	m, _ = update(m, cmd())
	view := m.View()
	for _, want := range []string{"# Flow plan", "full body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("linked flow plan overlay missing %q:\n%s", want, view)
		}
	}
}

func TestModel_OKeyOnFlowWithoutPlanShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Title:    "Unlinked flow",
		Status:   flowstore.StatusPending,
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatalf("unlinked flow o returned command %T, want nil", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected no overlay, got %d", m.Overlay())
	}
	if got := m.TransientError(); !strings.Contains(got, "Flow has no linked plan") {
		t.Fatalf("status = %q, want missing linked plan message", got)
	}
}

func TestModel_EnterOnFlowRowLaunchesReadyImplementation(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	flow := readyImplementationFlow()
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected implementation launch preparation command")
	}
	msg, ok := cmd().(model.FlowImplementationLaunchRequestedMsg)
	if !ok {
		t.Fatalf("launch preparation returned %T, want FlowImplementationLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, msg)
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}

	if launchUpdate.FlowID != flow.FlowID || launchUpdate.PhaseID != "implementation" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.Command != "codex" ||
		launched.FlowID != flow.FlowID ||
		launched.FlowPhaseID != "implementation" ||
		launched.PlanID != flow.PlanID ||
		launched.PlanPath != flow.PlanPath ||
		launched.WorktreePath != flow.WorktreePath ||
		launched.WorkingDir != flow.WorktreePath ||
		launched.RepoPath != flow.RepoPath ||
		launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
		launched.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("launch context = %#v", launched)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{"wtui-flow", "approved flow plan", "custom implementation instructions", "wtui flow phase set --flow-id", "--phase-id implementation", "completed", "needs_attention", "blocked"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation prompt missing %q:\n%s", want, launched.InitialPrompt)
		}
	}
}

func TestModel_EnterOnImplementationPhaseRowLaunchesExactPhase(t *testing.T) {
	var launchUpdate flowstore.PhaseLaunchUpdate
	flow := readyImplementationFlow()
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	for m.SelectedFlowPhaseID() != "implementation" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected implementation phase launch command")
	}
	msg := cmd()
	intent, ok := msg.(model.FlowImplementationLaunchRequestedMsg)
	if !ok {
		t.Fatalf("launch preparation returned %T, want FlowImplementationLaunchRequestedMsg", msg)
	}
	_, _ = update(m, intent)
	if launchUpdate.PhaseID != "implementation" {
		t.Fatalf("launched phase = %q, want implementation", launchUpdate.PhaseID)
	}
}

func TestModel_EnterOnImplementationPhaseRejectsUnapprovedPlanReview(t *testing.T) {
	flow := readyImplementationFlow()
	for i := range flow.Phases {
		if flow.Phases[i].PhaseID == "plan-review" {
			flow.Phases[i].Outcome = "changes_requested"
		}
	}
	m := flowsInRightPane(t, model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"}), []flowstore.FlowRecord{flow})
	for m.SelectedFlowPhaseID() != "implementation" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("unapproved implementation returned command %T, want nil", cmd)
	}
	if got := m.TransientError(); !strings.Contains(got, "approved Plan Review") {
		t.Fatalf("status = %q, want plan-review gate message", got)
	}
}

func TestModel_EnterOnImplementationResumesLatestEligibleSession(t *testing.T) {
	flow := readyImplementationFlow()
	for i := range flow.Phases {
		if flow.Phases[i].PhaseID == "implementation" {
			flow.Phases[i].Status = flowstore.PhaseRunning
			flow.Phases[i].Sessions = []flowstore.Session{
				{Provider: "claude", SessionID: "claude-old", StartedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC), CWD: "/tmp/claude"},
				{Provider: "codex", SessionID: "codex-old", StartedAt: time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC), CWD: "/tmp/old"},
				{Provider: "codex", SessionID: "codex-new", EndedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC), CWD: "/tmp/new"},
				{Provider: "codex", SessionID: "codex-active", LastSeenAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC), CWD: "/tmp/active"},
			}
		}
	}
	var launched actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex-app",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected implementation resume command")
	}
	_, _ = update(m, cmd())

	if launched.Command != "codex-app" ||
		launched.ResumeSessionID != "codex-active" ||
		launched.WorkingDir != "/tmp/active" ||
		launched.WorktreePath != flow.WorktreePath ||
		launched.FlowPhaseID != "implementation" ||
		launched.LaunchID == "" {
		t.Fatalf("resume launch context = %#v", launched)
	}
}

func TestModel_OKeyOnImplementationPhaseOpensLatestPhaseTranscript(t *testing.T) {
	flow := readyImplementationFlow()
	for i := range flow.Phases {
		switch flow.Phases[i].PhaseID {
		case "plan-review":
			flow.Phases[i].Sessions = []flowstore.Session{{Provider: "codex", SessionID: "review", TranscriptPath: "/state/review.jsonl", EndedAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)}}
		case "implementation":
			flow.Phases[i].Sessions = []flowstore.Session{
				{Provider: "codex", SessionID: "impl-old", TranscriptPath: "/state/old.jsonl", EndedAt: time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)},
				{Provider: "codex", SessionID: "impl-new", TranscriptPath: "/state/new.jsonl", EndedAt: time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)},
				{Provider: "codex", SessionID: "impl-active", TranscriptPath: "/state/active.jsonl", LastSeenAt: time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)},
			}
		}
	}
	var gotProvider sessions.Provider
	var gotSessionID string
	m := model.NewWithOptions(testRepos(), model.Options{
		ReadTranscript: func(provider sessions.Provider, sessionID string) ([]sessions.TranscriptEvent, error) {
			gotProvider = provider
			gotSessionID = sessionID
			return []sessions.TranscriptEvent{{Role: "assistant", Text: "implemented the flow"}}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{flow})
	for m.SelectedFlowPhaseID() != "implementation" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("expected transcript read command")
	}
	if m.Overlay() != ui.OverlaySessionTranscript {
		t.Fatalf("overlay = %d, want transcript", m.Overlay())
	}
	m, _ = update(m, cmd())

	if gotProvider != sessions.ProviderCodex || gotSessionID != "impl-active" {
		t.Fatalf("read transcript target = %s/%s, want codex/impl-active", gotProvider, gotSessionID)
	}
	if !strings.Contains(m.View(), "implemented the flow") {
		t.Fatalf("transcript overlay missing event:\n%s", m.View())
	}
}

func TestModel_ImplementationLaunchRequestIgnoredAfterRepoChange(t *testing.T) {
	launched := false
	addLaunchCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchCalled = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{readyImplementationFlow()})

	_, launchPrep := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if launchPrep == nil {
		t.Fatal("expected implementation launch preparation command")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	msg := launchPrep()
	m, cmd := update(m, msg)

	if cmd != nil {
		t.Fatalf("stale implementation launch returned command %T, want nil", cmd)
	}
	if launched {
		t.Fatal("stale implementation launch should not start an agent")
	}
	if addLaunchCalled {
		t.Fatal("stale implementation launch should not record a launch ID")
	}
}

func TestModel_PhaseRowImplementationLaunchIgnoredAfterPhaseChange(t *testing.T) {
	launched := false
	addLaunchCalled := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addLaunchCalled = true
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{readyImplementationFlow()})
	for m.SelectedFlowPhaseID() != "implementation" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	_, launchPrep := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if launchPrep == nil {
		t.Fatal("expected implementation launch preparation command")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
	msg := launchPrep()
	m, cmd := update(m, msg)

	if cmd != nil {
		t.Fatalf("stale phase-row launch returned command %T, want nil", cmd)
	}
	if launched {
		t.Fatal("stale phase-row implementation launch should not start an agent")
	}
	if addLaunchCalled {
		t.Fatal("stale phase-row implementation launch should not record a launch ID")
	}
}

func TestModel_ImplementationLaunchFailureMarksImplementationNeedsAttention(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{readyImplementationFlow()}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("agent unavailable")
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{readyImplementationFlow()})

	_, launchPrep := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if launchPrep == nil {
		t.Fatal("expected implementation launch preparation command")
	}
	m, cmd := update(m, launchPrep())
	if cmd == nil {
		t.Fatal("expected flow refresh after failed implementation launch")
	}

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "implementation" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "agent unavailable") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
	if got := m.TransientError(); !strings.Contains(got, "agent unavailable") {
		t.Fatalf("status = %q, want launch failure", got)
	}
}

func TestModel_NewFlowPromptsForTitle(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected opening new-flow title prompt to return no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("overlay = %d, want input overlay", m.Overlay())
	}
	if got := m.ConfirmPrompt(); got != ui.FlowTitlePrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowTitlePrompt)
	}
	if got := m.WorktreeInput(); got != "" {
		t.Fatalf("initial title input = %q, want empty", got)
	}
}

func TestModel_NewFlowCreatesWorktreeRecordsLaunchAndStartsPlanAgent(t *testing.T) {
	var created flowstore.FlowRecord
	var startUpdate flowstore.StartMetadataUpdate
	var launchUpdate flowstore.PhaseLaunchUpdate
	var launched actions.AgentLaunchContext
	var calls []string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			created = record
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			if repoPath != "/dev/alpha" || title != "Add Flow Mode" || baseRef != "main" {
				t.Fatalf("CreateFlowWorktree(%q, %q, %q)", repoPath, title, baseRef)
			}
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetFlowStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			startUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID, WorktreePath: update.WorktreePath, Branch: update.Branch, BaseRef: update.BaseRef}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "add-launch")
			launchUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{{PhaseID: update.PhaseID, Status: flowstore.PhaseRunning, LaunchIDs: []string{update.LaunchID}}}}, nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			calls = append(calls, "launch-agent")
			launched = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Add Flow Mode")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	if got := m.ConfirmPrompt(); got != ui.FlowInstructionsPrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowInstructionsPrompt)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Build the thing")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	if got := m.ConfirmPrompt(); got != ui.FlowBaseRefPrompt {
		t.Fatalf("prompt = %q, want %q", got, ui.FlowBaseRefPrompt)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("creation command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, cmd = update(m, launchMsg)
	if cmd == nil {
		t.Fatal("expected agent result command")
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,add-launch,launch-agent" {
		t.Fatalf("call order = %#v", calls)
	}
	if created.Title != "Add Flow Mode" || created.Instructions != "Build the thing" || created.RepoPath != "/dev/alpha" || created.BaseRef != "main" {
		t.Fatalf("created record = %#v", created)
	}
	if startUpdate.FlowID != "flow-1" ||
		startUpdate.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		startUpdate.Branch != "flow/add-flow-mode" ||
		startUpdate.BaseRef != "main" {
		t.Fatalf("start update = %#v", startUpdate)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "plan" || launchUpdate.LaunchID == "" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if launched.Command != "codex" ||
		launched.RepoPath != "/dev/alpha" ||
		launched.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		launched.Branch != "flow/add-flow-mode" ||
		launched.SessionStateRoot != "/state/wtui/sessions/v1" ||
		launched.FlowID != "flow-1" ||
		launched.FlowPhaseID != "plan" ||
		launched.PlanPhaseID != "plan" ||
		launched.LaunchID != launchUpdate.LaunchID {
		t.Fatalf("launch context = %#v", launched)
	}
	prompt := strings.ToLower(launched.InitialPrompt)
	for _, want := range []string{"wtui-flow", "build the thing", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("launch prompt missing %q: %q", want, launched.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"flow-1", "flow/add-flow-mode", "/dev/alpha-worktrees/flow-add-flow-mode", "base ref", "add flow mode"} {
		if strings.Contains(prompt, strings.ToLower(unwanted)) {
			t.Fatalf("launch prompt should not include metadata %q: %q", unwanted, launched.InitialPrompt)
		}
	}
}

func TestModel_NewFlowRunsBootstrapBeforeLaunchingPlanAgent(t *testing.T) {
	var gotCtx actions.BootstrapContext
	var gotHook actions.BootstrapHook
	var calls []string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetFlowStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "add-launch")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		BootstrapHookForRepo: func(repoPath string) (actions.BootstrapHook, bool) {
			if repoPath != "/dev/alpha" {
				t.Fatalf("BootstrapHookForRepo(%q)", repoPath)
			}
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, hook actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			gotCtx = ctx
			gotHook = hook
			return nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			calls = append(calls, "launch-agent")
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "main")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	_, _ = update(m, launchMsg)

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,bootstrap,add-launch,launch-agent" {
		t.Fatalf("call order = %#v", calls)
	}
	if gotCtx.RepoPath != "/dev/alpha" ||
		gotCtx.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		gotCtx.Ref != "flow/add-flow-mode" ||
		gotCtx.Kind != actions.WorktreeCreateFlow {
		t.Fatalf("bootstrap context = %#v", gotCtx)
	}
	if gotHook.Script != ".wtui/bootstrap" || gotHook.TimeoutSeconds != 7 {
		t.Fatalf("bootstrap hook = %#v", gotHook)
	}
}

func TestModel_NewFlowStaleLaunchIgnoredAfterRepoChange(t *testing.T) {
	launched := false
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, nil
		},
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			if repoPath != "/dev/alpha" {
				t.Fatalf("CreateFlowWorktree repo = %q, want /dev/alpha", repoPath)
			}
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-stale", Branch: "flow/stale"}, nil
		},
		SetFlowStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Stale Flow")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Do the stale thing")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("main")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected flow creation command")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	staleMsg := createCmd()
	if launchMsg, ok := staleMsg.(model.PlanLaunchRequestedMsg); !ok || launchMsg.Request == 0 {
		t.Fatalf("creation command returned %#v, want tagged PlanLaunchRequestedMsg", staleMsg)
	}
	m, cmd = update(m, staleMsg)
	if cmd != nil {
		t.Fatalf("stale launch returned command %T, want nil", cmd)
	}
	if launched {
		t.Fatal("stale flow creation launch should be ignored after repo change")
	}
}

func TestModel_NewFlowWarnsWhenNoAgentConfigured(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})

	if cmd != nil {
		t.Fatalf("expected no command, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("overlay = %d, want none", m.Overlay())
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatalf("expected missing-agent warning in view:\n%s", m.View())
	}
}

func TestModel_NewFlowBootstrapFailureBlocksPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var calls []string
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetFlowStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			return errors.New("missing env file")
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-phase")
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("launch ID should not be recorded after bootstrap failure")
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("agent should not launch after bootstrap failure")
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	_, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg, ok := cmd().(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,bootstrap,set-phase" {
		t.Fatalf("call order = %#v", calls)
	}
	if !strings.Contains(msg.Err, "Bootstrap hook failed") || !strings.Contains(msg.Err, "missing env file") {
		t.Fatalf("error = %q, want bootstrap failure", msg.Err)
	}
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "missing env file") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_NewFlowWorktreeFailureBlocksPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	if _, ok := msg.(model.FlowCreateFailedMsg); !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "branch exists") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestModel_NewFlowWorktreeFailureReportsBlockedPhaseUpdateFailure(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("disk full")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	_, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg, ok := cmd().(model.FlowCreateFailedMsg)
	if !ok {
		t.Fatalf("command returned %T, want FlowCreateFailedMsg", msg)
	}
	if !strings.Contains(msg.Err, "branch exists") || !strings.Contains(msg.Err, "mark flow blocked: disk full") {
		t.Fatalf("error = %q, want worktree and flow-update failures", msg.Err)
	}
}

func TestModel_NewFlowLaunchFailureMarksPlanNeedsAttention(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateFlowWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetFlowStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("no terminal")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := submitNewFlowPrompts(t, m, "Add Flow Mode", "Build the thing", "")
	if cmd == nil {
		t.Fatal("expected flow creation command")
	}
	msg := cmd()
	launchMsg, ok := msg.(model.PlanLaunchRequestedMsg)
	if !ok {
		t.Fatalf("command returned %T, want PlanLaunchRequestedMsg", msg)
	}
	m, _ = update(m, launchMsg)

	if len(phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one launch failure update", phaseUpdates)
	}
	update := phaseUpdates[0]
	if update.FlowID != "flow-1" ||
		update.PhaseID != "plan" ||
		update.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(update.Notes, "no terminal") {
		t.Fatalf("phase update = %#v", update)
	}
}

func TestModel_FlowAgentResultFailureMarksPhaseAndRefreshesFlows(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var listed bool
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listed = true
			if filter.RepoPath != "/dev/alpha" {
				t.Fatalf("flow filter = %#v", filter)
			}
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T", Status: flowstore.StatusNeedsAttention}}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseNeedsAttention ||
		!strings.Contains(phaseUpdate.Notes, "agent exited") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
	m, _ = update(m, cmd())
	if !listed {
		t.Fatal("expected ListFlows to run")
	}
	if got := m.Flows(); len(got) != 1 || got[0].Status != flowstore.StatusNeedsAttention {
		t.Fatalf("flows = %#v", got)
	}
}

func TestModel_FlowAgentResultFailureReportsPhaseUpdateFailure(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListFlows: func(filter flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", RepoPath: filter.RepoPath, Title: "T"}}, nil
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("state root locked")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'8'}})

	m, cmd := update(m, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "plan"},
		Err:           "agent exited",
		Detached:      true,
	})
	if cmd == nil {
		t.Fatal("expected flow refresh command")
	}
	if got := m.TransientError(); !strings.Contains(got, "agent exited") || !strings.Contains(got, "update flow phase: state root locked") {
		t.Fatalf("status = %q, want launch and phase-update failures", got)
	}
}

func submitNewFlowPrompts(t *testing.T, m model.Model, title, instructions, baseRef string) (model.Model, tea.Cmd) {
	t.Helper()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(title)})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected title submit command")
	}
	m, _ = update(m, cmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(instructions)})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected instructions submit command")
	}
	m, _ = update(m, cmd())
	if baseRef != "" {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(baseRef)})
	}
	return update(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func readyImplementationFlow() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Implement Flow phase",
		Instructions: "Custom implementation instructions",
		Status:       flowstore.StatusInProgress,
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Branch:       "flow/implementation",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "plan-review", Title: "Plan Review", Status: flowstore.PhaseCompleted, Outcome: "approved", Order: 2},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 3},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhasePending, Order: 4},
		},
	}
}
