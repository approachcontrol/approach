package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/scanner"
)

type flowPhaseLaunchTestTerminal struct {
	state string
}

func (t flowPhaseLaunchTestTerminal) VisibleLines(width, height int) []string { return nil }
func (t flowPhaseLaunchTestTerminal) Write(p []byte) (int, error)             { return len(p), nil }
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

	gotRecord, gotPhase, ok := m.previewFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
	})
	if !ok {
		t.Fatal("previewFlowLaunch() found no launchable phase")
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
	launcher := flowLaunchPreparation{AgentCommand: "codex"}
	_, err := launcher.preflight(flowPhaseLaunchRequest{
		Record: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha"},
		Phase:  flowstore.FlowPhase{PhaseID: "design-review", Kind: flowstore.KindPlanReview},
	})
	if err == nil || !strings.Contains(err.Error(), "Plan Review needs a linked plan") {
		t.Fatalf("Preflight() error = %v, want linked-plan guard", err)
	}
}

func TestPreviewFlowLaunchSkipsDuplicateReadyRow(t *testing.T) {
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

	_, phase, ok := m.previewFlowLaunch(flowLaunchIntent{
		Kind:   flowLaunchKindManualPhase,
		FlowID: record.FlowID,
	})
	if !ok {
		t.Fatal("previewFlowLaunch() found no launchable phase")
	}
	if phase.PhaseID != "step-2" {
		t.Fatalf("previewFlowLaunch() = %q, want step-2", phase.PhaseID)
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

func TestFlowPhaseLaunchCoordinatorNormalizesPhaseIDsForPreflightAndRecovery(t *testing.T) {
	launcher := flowLaunchPreparation{AgentCommand: "codex"}
	_, err := launcher.preflight(flowPhaseLaunchRequest{
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
			URL:        "https://github.com/approachcontrol/approach/pull/115",
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
	m := newAutoAdvanceTestModel(nil, Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			launched := current
			launched.Phases[1].Status = flowstore.PhaseRunning
			launched.Phases[1].LaunchIDs = []string{update.LaunchID}
			return launched, nil
		},
	})

	m, cmd, _ := autoAdvancePrepare(m, []flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareAutoFlowPhaseLaunch() returned nil, want auto-launch command")
	}
	_, launch := firstFlowEmbeddedLaunchFromAutoAdvance(t, m, cmd)
	if launch.Context.FlowPhaseID != "implementation" ||
		len(updates) != 1 ||
		!updates[0].AutoLaunch ||
		updates[0].PhaseID != "implementation" {
		t.Fatalf("launch = %#v updates = %#v, want implementation auto-launch", launch.Context, updates)
	}
}

func TestFlowPhaseLaunchCoordinatorDrainIgnoresObsoleteSuppression(t *testing.T) {
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
	m := newAutoAdvanceTestModel(nil, Options{
		AgentCommand: "codex",
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return current, nil
		},
	})
	m, cmd, _ := autoAdvancePrepare(m, []flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareAutoFlowPhaseLaunch() returned nil, want drain launch command")
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates before command runs = %#v, want none", updates)
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
	m := newAutoAdvanceTestModel(nil, Options{
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

	m, cmd, _ := autoAdvancePrepare(m, []flowstore.FlowRecord{previous}, []flowstore.FlowRecord{current})
	if cmd != nil {
		t.Fatalf("prepareAutoFlowPhaseLaunch() returned command %T while source terminal was running", cmd)
	}
	if len(updates) != 0 {
		t.Fatalf("launch updates while source terminal was running = %#v, want none", updates)
	}
	if _, ok := m.autoAdvanceDrainFlows["flow-1"]; !ok {
		t.Fatalf("autoAdvanceDrainFlows = %#v, want flow-1 armed", m.autoAdvanceDrainFlows)
	}

	m.embeddedTerminals = nil
	m, cmd = autoAdvanceDrain(m, []flowstore.FlowRecord{current})
	if cmd == nil {
		t.Fatal("prepareAutoAdvanceDrainLaunches() returned nil after source terminal closed")
	}
	firstFlowEmbeddedLaunchFromAutoAdvance(t, m, cmd)
	if len(updates) != 1 || !updates[0].AutoLaunch || updates[0].PhaseID != "implementation" {
		t.Fatalf("launch updates after source terminal closed = %#v, want auto implementation launch", updates)
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
	}, "")
	if !strings.Contains(prompt, "Custom review loop for  Review-Loop") {
		t.Fatalf("normalized phase template was not used:\n%s", prompt)
	}

	prompt = flowPhasePrompt(record, phase, "", "", FlowPromptTemplates{}, "")
	if !strings.Contains(prompt, "Use the review-loop workflow with goal: review-and-revise.") {
		t.Fatalf("normalized built-in review-loop prompt was not used:\n%s", prompt)
	}
}

// A launch refused because its pinned binary no longer verifies must leave the
// phase exactly as it found it: no `running` stamp, no worktree.
func TestFlowPhaseLaunchPreflightRefusesUnverifiablePinWithoutMutatingState(t *testing.T) {
	worktreeCalls := 0
	launchIDs := 0
	launcher := flowLaunchPreparation{
		AgentCommand: "codex",
		Pin: controlplane.Pin{
			ExecutablePath: "/state/approach/sessions/v1/bin/approach-abc123",
			Version:        "v0.10.3",
			SchemaVersion:  6,
		},
		VerifyPin: func(controlplane.Pin) error {
			return fmt.Errorf("%w: /state/approach/sessions/v1/bin/approach-abc123", controlplane.ErrPinDigestMismatch)
		},
		EnsureWorktree: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			worktreeCalls++
			return record, nil
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchIDs++
			return flowstore.FlowRecord{}, nil
		},
	}

	_, err := launcher.preflight(flowPhaseLaunchRequest{
		Record: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktree"},
		Phase:  flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseReady},
	})
	var validation flowPhaseLaunchValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("preflight error = %v, want a validation refusal", err)
	}
	for _, want := range []string{"/state/approach/sessions/v1/bin/approach-abc123", "v0.10.3", "schema 6"} {
		if !strings.Contains(validation.Message, want) {
			t.Fatalf("refusal %q does not name %q", validation.Message, want)
		}
	}
	if worktreeCalls != 0 || launchIDs != 0 {
		t.Fatalf("refused launch mutated state: worktree=%d launchIDs=%d", worktreeCalls, launchIDs)
	}
}

func TestFlowPhaseLaunchPreflightAcceptsAnUnpinnedLaunch(t *testing.T) {
	launcher := flowLaunchPreparation{
		AgentCommand: "codex",
		VerifyPin: func(controlplane.Pin) error {
			t.Fatal("an unpinned launch must not verify anything")
			return nil
		},
	}
	if _, err := launcher.preflight(flowPhaseLaunchRequest{
		Record: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", WorktreePath: "/dev/alpha-worktree"},
		Phase:  flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseReady},
	}); err != nil {
		t.Fatalf("preflight on an unpinned launch: %v", err)
	}
}

func TestApplyLaunchPinStampsTheLaunchingBuild(t *testing.T) {
	pin := controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
	}
	ctx := applyLaunchPin(actions.AgentLaunchContext{Command: "codex"}, pin)
	if ctx.Executable != pin.ExecutablePath || ctx.BuildVersion != "v0.10.3" || ctx.DBSchemaVersion != 6 {
		t.Fatalf("pinned context = %+v", ctx)
	}
	unpinned := applyLaunchPin(actions.AgentLaunchContext{Command: "codex"}, controlplane.Pin{})
	if unpinned.Executable != "" || unpinned.BuildVersion != "" || unpinned.DBSchemaVersion != 0 {
		t.Fatalf("zero pin stamped a context: %+v", unpinned)
	}
}
