package model

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/scanner"
)

type flowPhaseLaunchTestTerminal struct {
	state string
}

func (t flowPhaseLaunchTestTerminal) VisibleLines(width, height int) []string { return nil }
func (t flowPhaseLaunchTestTerminal) Write([]byte) (int, error)               { return 0, nil }
func (t flowPhaseLaunchTestTerminal) Resize(width, height int) error          { return nil }
func (t flowPhaseLaunchTestTerminal) Terminate() error                        { return nil }
func (t flowPhaseLaunchTestTerminal) Wait(context.Context) error              { return nil }
func (t flowPhaseLaunchTestTerminal) State() string                           { return t.state }

func TestFlowPhaseLaunchCoordinatorSelectsFirstLaunchablePhase(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhasePending, Order: 1},
			{PhaseID: "review-loop", Title: "Review loop", Status: flowstore.PhaseReady, Order: 2},
			{PhaseID: "pr-creation", Title: "PR creation", Status: flowstore.PhaseReady, Order: 3},
		},
	}
	m := New([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}})
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})

	gotRecord, gotPhase, ok := m.selectedFlowNextLaunchablePhase()
	if !ok {
		t.Fatal("selectedFlowNextLaunchablePhase() found no launchable phase")
	}
	if gotRecord.FlowID != "flow-1" || gotPhase.PhaseID != "review-loop" {
		t.Fatalf("selected launch target = flow %q phase %q, want flow-1 review-loop", gotRecord.FlowID, gotPhase.PhaseID)
	}
}

func TestNextAutoLaunchPhaseSkipsDuplicateReadyRow(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "Step-1", Title: "Step 1", DependsOn: []string{}, Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "step-1", Title: "Step 1 stale duplicate", DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 2},
			{PhaseID: "step-2", Title: "Step 2", DependsOn: []string{"step-1"}, Status: flowstore.PhaseReady, Order: 3},
		},
	}

	phase, ok := nextAutoLaunchPhase(record)
	if !ok {
		t.Fatal("nextAutoLaunchPhase() found no launchable phase")
	}
	if phase.PhaseID != "step-2" {
		t.Fatalf("nextAutoLaunchPhase() = %q, want step-2", phase.PhaseID)
	}
}

func TestNextAutoLaunchPhaseSkipsMergeKindCustomID(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "ship", Kind: flowstore.KindMerge, Title: "Ship", DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 1},
			{PhaseID: "verify", Kind: flowstore.KindImplementation, Title: "Verify", DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 2},
		},
	}

	phase, ok := nextAutoLaunchPhase(record)
	if !ok {
		t.Fatal("nextAutoLaunchPhase() found no launchable phase")
	}
	if phase.PhaseID != "verify" {
		t.Fatalf("nextAutoLaunchPhase() = %q, want verify", phase.PhaseID)
	}
}

func TestFlowPhaseLaunchPreflightRequiresPlanForReviewKindCustomID(t *testing.T) {
	launcher := FlowPhaseLauncher{AgentCommand: "codex"}
	_, err := launcher.Preflight(FlowPhaseLaunchRequest{
		Record: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha"},
		Phase:  flowstore.FlowPhase{PhaseID: "design-review", Kind: flowstore.KindPlanReview},
	})
	if err == nil || !strings.Contains(err.Error(), "Plan Review needs a linked plan") {
		t.Fatalf("Preflight() error = %v, want linked-plan guard", err)
	}
}

func TestSelectedFlowNextLaunchablePhaseSkipsDuplicateReadyRow(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "Step-1", Title: "Step 1", DependsOn: []string{}, Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "step-1", Title: "Step 1 stale duplicate", DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 2},
			{PhaseID: "step-2", Title: "Step 2", DependsOn: []string{"step-1"}, Status: flowstore.PhaseReady, Order: 3},
		},
	}
	m := New([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}})
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})

	_, phase, ok := m.selectedFlowNextLaunchablePhase()
	if !ok {
		t.Fatal("selectedFlowNextLaunchablePhase() found no launchable phase")
	}
	if phase.PhaseID != "step-2" {
		t.Fatalf("selectedFlowNextLaunchablePhase() = %q, want step-2", phase.PhaseID)
	}
}

func TestNextAutoLaunchPhaseSkipsReadyPhaseWithUnsatisfiedDependency(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:   "flow-1",
		RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "root", Title: "Root", DependsOn: []string{}, Status: flowstore.PhaseRunning, Order: 1},
			{PhaseID: "stale", Title: "Stale", DependsOn: []string{"root"}, Status: flowstore.PhaseReady, Order: 2},
			{PhaseID: "independent", Title: "Independent", DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 3},
		},
	}

	phase, ok := nextAutoLaunchPhase(record)
	if !ok {
		t.Fatal("nextAutoLaunchPhase() found no launchable phase")
	}
	if phase.PhaseID != "independent" {
		t.Fatalf("nextAutoLaunchPhase() = %q, want independent", phase.PhaseID)
	}
}

func TestFlowPhaseLaunchTargetManualPreflightFailureSetsStatus(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}})
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-manual",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1},
		},
	}

	_, ok, m, _ := m.flowPhaseLaunchTarget(FlowPhaseLaunchRequest{
		Record: record,
		Phase:  record.Phases[0],
	})
	if ok {
		t.Fatal("flowPhaseLaunchTarget() succeeded without an agent command")
	}
	if m.status.Source != statusOther || !strings.Contains(m.status.Text, "Press A to choose") {
		t.Fatalf("status = %#v, want manual preflight failure status", m.status)
	}
}

func TestFlowPhaseLaunchCoordinatorNormalizesPhaseIDsForPreflightAndRecovery(t *testing.T) {
	launcher := FlowPhaseLauncher{AgentCommand: "codex"}
	_, err := launcher.Preflight(FlowPhaseLaunchRequest{
		Record: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha"},
		Phase:  flowstore.FlowPhase{PhaseID: " Plan-Review ", Status: flowstore.PhaseReady},
	})
	if err == nil || err.Error() != "Plan Review needs a linked plan before launch" {
		t.Fatalf("Preflight() error = %v, want normalized plan-review linked-plan guard", err)
	}

	record := flowstore.FlowRecord{
		FlowID: "flow-1",
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     115,
			URL:        "https://github.com/brian-bell/wtui/pull/115",
			HeadBranch: "flow/review",
			BaseBranch: "main",
		},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "plan-review", Status: flowstore.PhaseCompleted, Outcome: flowstore.OutcomeApproved, Order: 2},
			{PhaseID: "implementation", Status: flowstore.PhaseCompleted, Order: 3},
			{PhaseID: "review-loop", Status: flowstore.PhaseCompleted, Order: 4},
			{PhaseID: "pr-creation", Status: flowstore.PhaseCompleted, Order: 5},
			{PhaseID: " Autoreview ", Status: flowstore.PhaseBlocked, Order: 6},
		},
	}
	if !flowPhaseCanLaunch(record, record.Phases[5]) {
		t.Fatal("flowPhaseCanLaunch() should allow normalized autoreview recovery launch")
	}
}

func TestFlowPhaseLaunchCoordinatorPreparesDirectAutoLaunchTarget(t *testing.T) {
	previous := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		AutoMode:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan-review", Status: flowstore.PhaseRunning, Order: 1},
			{PhaseID: "implementation", Status: flowstore.PhasePending, Order: 2},
		},
	}
	current := previous
	current.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Status: flowstore.PhaseCompleted, Order: 1},
		{PhaseID: "implementation", Status: flowstore.PhaseReady, Order: 2},
	}
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(nil, Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			launched.Phases[1].Status = flowstore.PhaseRunning
			launched.Phases[1].LaunchIDs = []string{update.LaunchID}
			return launched, nil
		},
	})

	_, cmd, _ := m.prepareAutoFlowPhaseLaunch([]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareAutoFlowPhaseLaunch() returned nil, want auto-launch command")
	}
	msg := cmd()
	launch, ok := msg.(FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("auto-launch command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	if launch.LaunchContext.FlowPhaseID != "implementation" ||
		len(updates) != 1 ||
		!updates[0].AutoLaunch ||
		updates[0].PhaseID != "implementation" {
		t.Fatalf("launch = %#v updates = %#v, want implementation auto-launch", launch.LaunchContext, updates)
	}
}

func TestFlowPhaseLaunchCoordinatorClearsSuppressedAutoLaunchWithoutRelaunch(t *testing.T) {
	previous := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		AutoMode:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan-review", Status: flowstore.PhaseRunning, LaunchIDs: []string{"source-launch"}, Order: 1},
			{PhaseID: "implementation", Status: flowstore.PhasePending, Order: 2},
		},
	}
	current := previous
	current.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Status: flowstore.PhaseCompleted, LaunchIDs: []string{"source-launch"}, Order: 1},
		{PhaseID: "implementation", Status: flowstore.PhaseReady, Order: 2},
	}
	m := NewWithOptions(nil, Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddFlowPhaseLaunchID() should not run for a suppressed source phase: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
	})
	m = m.suppressAutoFlowPhaseLaunch("flow-1", "plan-review", "source-launch")

	m, cmd, _ := m.prepareAutoFlowPhaseLaunch([]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("prepareAutoFlowPhaseLaunch() returned command %T for suppressed source phase", cmd)
	}
	if len(m.suppressedAutoFlowLaunches) != 0 {
		t.Fatalf("suppressed auto-launches = %#v, want cleared", m.suppressedAutoFlowLaunches)
	}
}

func TestFlowPhaseLaunchCoordinatorDefersAutoLaunchUntilSourceTerminalCloses(t *testing.T) {
	previous := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-auto",
		AutoMode:     true,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan-review", Status: flowstore.PhaseRunning, LaunchIDs: []string{"source-launch"}, Order: 1},
			{PhaseID: "implementation", Status: flowstore.PhasePending, Order: 2},
		},
	}
	current := previous
	current.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan-review", Status: flowstore.PhaseCompleted, LaunchIDs: []string{"source-launch"}, Order: 1},
		{PhaseID: "implementation", Status: flowstore.PhaseReady, Order: 2},
	}
	var updates []flowstore.PhaseLaunchUpdate
	m := NewWithOptions(nil, Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			launched.Phases[1].Status = flowstore.PhaseRunning
			launched.Phases[1].LaunchIDs = []string{update.LaunchID}
			return launched, nil
		},
	})
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Scope:       embeddedTerminalScopeFlow,
		FlowID:      "flow-1",
		FlowPhaseID: "plan-review",
		LaunchID:    "source-launch",
		Terminal:    flowPhaseLaunchTestTerminal{state: "running"},
	}}

	m, cmd, _ := m.prepareAutoFlowPhaseLaunch([]flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("prepareAutoFlowPhaseLaunch() returned command %T while source terminal was running", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates while source terminal was running = %#v, want none", updates)
	}
	if _, ok := m.deferredAutoFlowLaunches[deferredAutoFlowLaunchKey{FlowID: "flow-1", PhaseID: "plan-review"}]; !ok {
		t.Fatalf("deferred auto-launches = %#v, want plan-review deferred", m.deferredAutoFlowLaunches)
	}

	m.embeddedTerminals = nil
	m, cmd = m.prepareDeferredAutoFlowPhaseLaunchesFrom([]flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareDeferredAutoFlowPhaseLaunchesFrom() returned nil after source terminal closed")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok && len(batch) == 1 {
		msg = batch[0]()
	}
	if _, ok := msg.(FlowEmbeddedLaunchRequestedMsg); !ok {
		t.Fatalf("deferred auto-launch command returned %T, want FlowEmbeddedLaunchRequestedMsg", msg)
	}
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after source terminal closed = %#v, want auto implementation launch", updates)
	}
	if len(m.deferredAutoFlowLaunches) != 0 {
		t.Fatalf("deferred auto-launches after launch = %#v, want empty", m.deferredAutoFlowLaunches)
	}
}

func TestFlowPhasePromptTemplatesNormalizePhaseIDs(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		WorktreePath: "/dev/alpha-worktrees/flow-review",
		Branch:       "flow/review",
		Commit:       "abc123",
	}
	phase := flowstore.FlowPhase{PhaseID: " Review-Loop ", Status: flowstore.PhaseReady}

	prompt := flowPhasePrompt(record, phase, "", "", FlowPromptTemplates{
		ReviewLoop: "Custom review loop for {phase_id}",
	})
	if !strings.Contains(prompt, "Custom review loop for  Review-Loop") {
		t.Fatalf("normalized phase template was not used:\n%s", prompt)
	}

	prompt = flowPhasePrompt(record, phase, "", "", FlowPromptTemplates{})
	if !strings.Contains(prompt, "Use the review-loop workflow with goal: review-and-revise.") {
		t.Fatalf("normalized built-in review-loop prompt was not used:\n%s", prompt)
	}
}
