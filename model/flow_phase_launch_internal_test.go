package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/internal/flowlease"
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
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{
		InspectFlowLease: func(string, string) (flowlease.LeaseState, error) { return flowlease.Free, nil },
	})
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
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{
		InspectFlowLease: func(string, string) (flowlease.LeaseState, error) { return flowlease.Free, nil },
	})
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

func TestApplyLaunchStampStampsTheLaunchingBuild(t *testing.T) {
	pin := controlplane.Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		Version:        "v0.10.3",
		SchemaVersion:  6,
	}
	ctx := applyLaunchStamp(actions.AgentLaunchContext{Command: "codex"}, launchStamp{Pin: pin})
	if ctx.Executable != pin.ExecutablePath || ctx.BuildVersion != "v0.10.3" || ctx.DBSchemaVersion != 6 {
		t.Fatalf("pinned context = %+v", ctx)
	}
	unpinned := applyLaunchStamp(actions.AgentLaunchContext{Command: "codex"}, launchStamp{Pin: controlplane.Pin{}})
	if unpinned.Executable != "" || unpinned.BuildVersion != "" || unpinned.DBSchemaVersion != 0 {
		t.Fatalf("zero pin stamped a context: %+v", unpinned)
	}
}

// stubRetainLaunchPin records claims instead of writing them, and returns the
// recorder.
func stubRetainLaunchPin(t *testing.T) *[][3]string {
	t.Helper()
	original := retainLaunchPin
	t.Cleanup(func() { retainLaunchPin = original })
	var claims [][3]string
	retainLaunchPin = func(root, launchID, digest string) error {
		claims = append(claims, [3]string{root, launchID, digest})
		return nil
	}
	return &claims
}

func TestApplyLaunchStampClaimsTheCachedBinary(t *testing.T) {
	claims := stubRetainLaunchPin(t)
	pin := controlplane.Pin{ExecutablePath: "/state/bin/approach-abc123", Digest: "abc123def456"}

	applyLaunchStamp(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		SessionStateRoot: "/state",
	}, launchStamp{Pin: pin})

	if len(*claims) != 1 {
		t.Fatalf("claims = %v, want exactly one", *claims)
	}
	if got, want := (*claims)[0], [3]string{"/state", "launch-1", "abc123def456"}; got != want {
		t.Fatalf("claim = %v, want %v", got, want)
	}
}

// nonLaunchingContextFiles construct an actions.AgentLaunchContext that never
// starts an agent, so they have nothing to pin and nothing to claim.
var nonLaunchingContextFiles = map[string]string{
	"flow_launch_lifecycle.go": "bookkeeping context for persisting a launch FAILURE; no agent is started",
	"flow_session_release.go":  "finalization context for a session that has already ended",
	"tmux_mode.go":             "a Command-only probe passed to tmuxRouteEligible, never launched",
}

// Every launch kind bakes the pinned path into its provider session-hook argv,
// so every launch kind has to stamp the pin and claim its cached copy. Both
// happen in applyLaunchStamp, and this is the fence that keeps a NEW launch kind
// from quietly skipping it: exercising the eight existing paths would say
// nothing about the ninth, which is the one that will be wrong.
//
// It is a file-granularity fence, and deliberately so rather than by oversight.
// It cannot see a second unpinned literal in a file that already pins one, and
// it matches the composite literal rather than `var ctx actions.AgentLaunchContext`.
// What it does catch is the realistic mistake — a launch kind added in a new
// file — and it costs no fixtures to do it.
func TestEveryLaunchingContextGoesThroughApplyLaunchStamp(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	seenExempt := map[string]bool{}
	launchers := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		if !strings.Contains(text, "actions.AgentLaunchContext{") {
			continue
		}
		if _, exempt := nonLaunchingContextFiles[name]; exempt {
			seenExempt[name] = true
			if strings.Contains(text, "applyLaunchStamp(") {
				t.Fatalf("%s is listed as non-launching but stamps a pin; remove it from nonLaunchingContextFiles", name)
			}
			continue
		}
		if !strings.Contains(text, "applyLaunchStamp(") {
			t.Fatalf("%s builds an agent launch context but never calls applyLaunchStamp, so its agent runs whatever "+
				"`approach` PATH resolves and its cached binary is unclaimed. Stamp the pin, or document the file in "+
				"nonLaunchingContextFiles with the reason it launches nothing.", name)
		}
		launchers++
	}
	if launchers == 0 {
		t.Fatal("found no launching files; the scan matched nothing and would pass vacuously")
	}
	for name, reason := range nonLaunchingContextFiles {
		if !seenExempt[name] {
			t.Fatalf("nonLaunchingContextFiles lists %s (%q) but nothing there builds a launch context anymore", name, reason)
		}
	}
}

func TestApplyLaunchStampDoesNotClaimWhenThereIsNothingToProtect(t *testing.T) {
	claims := stubRetainLaunchPin(t)
	cached := controlplane.Pin{ExecutablePath: "/state/bin/approach-abc123", Digest: "abc123def456"}
	degraded := controlplane.Pin{ExecutablePath: "/usr/local/bin/approach", Digest: "abc123def456", Degraded: true}

	// A degraded pin runs the source binary; there is no cached copy retention
	// could evict, so a claim would only leak a file.
	applyLaunchStamp(actions.AgentLaunchContext{LaunchID: "launch-1", SessionStateRoot: "/state"}, launchStamp{Pin: degraded})
	// RetainPin rejects an empty launch id, and an unrooted context has nowhere
	// to write. Both are untracked launches, not failures.
	applyLaunchStamp(actions.AgentLaunchContext{SessionStateRoot: "/state"}, launchStamp{Pin: cached})
	applyLaunchStamp(actions.AgentLaunchContext{LaunchID: "launch-2"}, launchStamp{Pin: cached})
	applyLaunchStamp(actions.AgentLaunchContext{LaunchID: "launch-3", SessionStateRoot: "/state"}, launchStamp{Pin: controlplane.Pin{}})

	if len(*claims) != 0 {
		t.Fatalf("claims = %v, want none", *claims)
	}
}

// stubVerifyLaunchPin replaces the verification seam so a test can fail the
// check without corrupting a real cached binary.
func stubVerifyLaunchPin(t *testing.T, err error) {
	t.Helper()
	original := verifyLaunchPin
	t.Cleanup(func() { verifyLaunchPin = original })
	verifyLaunchPin = func(controlplane.Pin) error { return err }
}

func TestRefuseUnverifiedLaunchPinNamesTheCause(t *testing.T) {
	pin := controlplane.Pin{ExecutablePath: "/state/bin/approach-abc123", Version: "v0.10.3", SchemaVersion: 6}

	stubVerifyLaunchPin(t, nil)
	if refusal := refuseUnverifiedLaunchPin(pin); refusal != "" {
		t.Fatalf("a verified pin was refused: %s", refusal)
	}

	stubVerifyLaunchPin(t, controlplane.ErrPinDigestMismatch)
	refusal := refuseUnverifiedLaunchPin(pin)
	for _, want := range []string{pin.ExecutablePath, "no longer matches", "v0.10.3", "schema 6"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal %q does not name %q", refusal, want)
		}
	}
}

// An unpinned launch has nothing to verify. Refusing one would break every
// caller that never had a pin, which is the pre-pin behaviour and still correct
// for a manually started session.
func TestRefuseUnverifiedLaunchPinIgnoresAnUnpinnedLaunch(t *testing.T) {
	verified := false
	original := verifyLaunchPin
	t.Cleanup(func() { verifyLaunchPin = original })
	verifyLaunchPin = func(pin controlplane.Pin) error {
		verified = true
		return original(pin)
	}
	if refusal := refuseUnverifiedLaunchPin(controlplane.Pin{}); refusal != "" {
		t.Fatalf("an unpinned launch was refused: %s", refusal)
	}
	if !verified {
		t.Fatal("refuseUnverifiedLaunchPin never consulted the seam")
	}
}

// Files exempt from the verification fence, with the reason each one cannot
// reach a wrong build. Empty on purpose: every file that stamps a pin refuses an
// unverified one, including the non-Flow routes in model_keys.go. A new entry
// here is a claim that needs a reason as specific as the ones in
// nonLaunchingContextFiles.
var unverifiedLaunchPinFiles = map[string]string{}

// preflight is not the only path that marks a phase running and bakes the
// pinned path into a detached agent's argv: create, resume, saved-session
// resume, repair, autofix, and the generic worktree agent all reserve and write
// without going through it, and the plan, session-resume, and plain repository
// agents launch outside Flow entirely while still running `approach` commands
// against the same store. A check that lived only in preflight would leave every
// one of them launching an unverified binary, so this is the fence that keeps
// the NEXT launch kind from skipping it too — the same file-granularity trade as
// TestEveryLaunchingContextGoesThroughApplyLaunchStamp, for the same reason.
func TestEveryFlowLaunchRouteRefusesAnUnverifiedPin(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	seenExempt := map[string]bool{}
	verifiers := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(source)
		if !strings.Contains(text, "applyLaunchStamp(") {
			continue
		}
		if reason, exempt := unverifiedLaunchPinFiles[name]; exempt {
			seenExempt[name] = true
			if strings.Contains(text, "refuseUnverifiedLaunchPin(") {
				t.Fatalf("%s is exempt (%q) but now refuses unverified pins; remove it from unverifiedLaunchPinFiles", name, reason)
			}
			continue
		}
		// flow_launch_pin.go defines the helpers; flow_phase_launch.go owns the
		// preflight spelling. Everything else has to call the shared refusal.
		if name == "flow_launch_pin.go" || strings.Contains(text, "l.verifyPin()") {
			verifiers++
			continue
		}
		if !strings.Contains(text, "refuseUnverifiedLaunchPin(") {
			t.Fatalf("%s stamps a launch pin but never refuses an unverified one, so it can launch a binary the "+
				"state root lost or an upgrade replaced. Refuse before it reserves or writes, or document the file "+
				"in unverifiedLaunchPinFiles with the reason it cannot.", name)
		}
		verifiers++
	}
	if verifiers == 0 {
		t.Fatal("found no verifying files; the scan matched nothing and would pass vacuously")
	}
	for name, reason := range unverifiedLaunchPinFiles {
		if !seenExempt[name] {
			t.Fatalf("unverifiedLaunchPinFiles lists %s (%q) but nothing there stamps a launch pin anymore", name, reason)
		}
	}
}
