package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/flowstore"
)

const (
	progressionLaunchRepo  = "/dev/alpha"
	progressionLaunchEpic  = "epic"
	progressionLaunchChild = "epic.b"
)

type progressionLaunchFixture struct {
	harness *createLaunchHarness
	key     string
	source  flowstore.FlowRecord
	updates []flowstore.EpicProgressionHaltUpdate
	halted  int
}

// newProgressionLaunchFixture builds the state one advance leaves behind at the
// moment it submits its create-then-launch intent: a tracked baseline on the
// completed source Flow and the advance's preparation admission still held.
func newProgressionLaunchFixture(t *testing.T, outcome flowstore.EpicProgressionSuccessorOutcome, reconcileErr error) (*progressionLaunchFixture, Model) {
	t.Helper()
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: progressionLaunchChild, EpicID: progressionLaunchEpic}
	h.record.Headless = true
	fixture := &progressionLaunchFixture{
		harness: h,
		key:     epicProgressionBaselineKey(progressionLaunchRepo, progressionLaunchEpic),
		source:  progressionAdvanceFlow("flow-source", progressionLaunchRepo, "epic.a", progressionLaunchEpic, flowstore.StatusCompleted),
	}
	m := h.model(t)
	m.launchSeams.ReconcileEpicSuccessor = func(update flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
		h.order = append(h.order, "reconcile:"+update.FlowID)
		if update.FlowID != h.record.FlowID || update.Bead != h.record.Bead ||
			update.Key != (flowstore.EpicProgressionKey{RepoPath: progressionLaunchRepo, EpicID: progressionLaunchEpic}) {
			t.Fatalf("successor reconciliation = %#v", update)
		}
		if h.record.PreparedAt == nil {
			t.Fatal("successor reconciled before the preparation receipt existed")
		}
		if reconcileErr != nil {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorRetryable}, reconcileErr
		}
		return flowstore.EpicProgressionSuccessorResult{Outcome: outcome, Flow: h.record}, nil
	}
	m.haltEpicProgression = func(update flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
		fixture.halted++
		fixture.updates = append(fixture.updates, update)
		halt := update.Halt
		return flowstore.EpicProgression{RepoPath: progressionLaunchRepo, EpicID: progressionLaunchEpic, Halt: &halt}, nil
	}
	m.readEpicProgression = func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
		return flowstore.EpicProgression{RepoPath: progressionLaunchRepo, EpicID: progressionLaunchEpic, Enabled: true}, true, nil
	}
	m.epicProgressionBaselines = map[string]flowstore.FlowRecord{fixture.key: fixture.source}
	m.epicProgressionBaselineMinimumRequests = map[string]uint64{}
	m.epicProgressionOwnedSuccessors = map[string]epicProgressionOwnedSuccessor{}

	var token uint64
	var admitted bool
	m, token, admitted = m.acquireFlowPreparation(flowPreparationEpicAdvance)
	if !admitted {
		t.Fatal("fixture could not take the advance preparation admission")
	}
	m.epicProgressionAdvanceSeq++
	m.activeEpicProgressionAdvance = epicProgressionAdvanceRequest{
		Request: m.epicProgressionAdvanceSeq, OwnerToken: token,
		EpicKey: fixture.key, SourceFlowID: fixture.source.FlowID,
	}
	return fixture, m
}

func (f *progressionLaunchFixture) admit(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	create := flowLaunchCreateRequest{
		Presentation: flowLaunchCreatePresentation{
			Origin: flowLaunchOriginEpicProgression, Request: m.activeEpicProgressionAdvance.Request,
		},
		RepoPath: progressionLaunchRepo, Title: "New Flow", Instructions: "Write the plan.",
		Bead: f.harness.record.Bead, BaseRef: "main", Headless: true,
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(""))
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind: flowLaunchKindCreatePhase, Origin: flowLaunchOriginEpicProgression, Create: create, Settings: settings,
	})
	if !admitted || cmd == nil {
		t.Fatalf("progression create admission = %v, cmd = %v, status = %q", admitted, cmd != nil, next.status.Text)
	}
	return next, cmd
}

// drainProgressionLaunch runs the whole attempt the way Update would: every
// command the create pipeline, its failure persistence, and the progression
// halt produce, until nothing is left to deliver.
func drainProgressionLaunch(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for step := 0; len(queue) > 0 && step < 40; step++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		for _, msg := range immediateFlowRefreshMessages(next) {
			var out tea.Cmd
			switch typed := msg.(type) {
			case flowLaunchEventMsg:
				m, out = m.handleFlowLaunchEvent(typed)
			case flowLaunchFailurePersistedMsg:
				m, out = m.handleFlowLaunchFailurePersisted(typed)
			case epicProgressionHaltResultMsg:
				m, out = m.handleEpicProgressionHaltResult(typed)
			}
			queue = append(queue, out)
		}
	}
	return m
}

func TestProgressionCreateLaunchesTheFirstPhaseAndOwnsItsSuccessor(t *testing.T) {
	fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
	h := fixture.harness
	var createdHeadless *bool
	inner := m.launchSeams.CreatePreparation
	m.launchSeams.CreatePreparation = func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
		createdHeadless = opts.Headless
		created, finalizer, err := inner(record, opts)
		// The store enables AutoMode on every new record and the pipeline never
		// opts out, so the child drains its later phases without a key press.
		h.record.AutoMode = true
		created.AutoMode = true
		return created, finalizer, err
	}

	m, cmd := fixture.admit(t, m)
	if got := m.epicProgressionOwnedSuccessors[fixture.key]; got.FlowID != "" {
		t.Fatalf("ownership installed before the record was written: %#v", got)
	}
	m = drainProgressionLaunch(t, m, cmd)

	if createdHeadless == nil || !*createdHeadless || !h.record.AutoMode {
		t.Fatalf("child creation options: headless=%v record=%#v", createdHeadless, h.record)
	}
	if len(h.contexts) != 1 || h.contexts[0].FlowID != h.record.FlowID || h.contexts[0].FlowPhaseID != "plan" || !h.contexts[0].Headless {
		t.Fatalf("progression child launch contexts = %#v", h.contexts)
	}
	joined := strings.Join(h.order, ",")
	if reconcileAt, launchAt := strings.Index(joined, "reconcile:"), strings.Index(joined, "launch-id:"); reconcileAt < 0 || launchAt < 0 || reconcileAt > launchAt {
		t.Fatalf("order = %#v, want reconciliation before the first phase is made running", h.order)
	}
	if got := m.epicProgressionBaselines[fixture.key]; got.FlowID != h.record.FlowID {
		t.Fatalf("baseline = %#v, want the accepted child", got)
	}
	if got := m.epicProgressionBaselineMinimumRequests[fixture.key]; got != m.autoAdvanceRequestSeq+1 {
		t.Fatalf("baseline minimum request = %d", got)
	}
	if len(m.epicProgressionOwnedSuccessors) != 0 || m.flowPreparationAdmission || m.activeEpicProgressionAdvance.Request != 0 {
		t.Fatalf("advance did not settle: owned=%#v admission=%t active=%#v",
			m.epicProgressionOwnedSuccessors, m.flowPreparationAdmission, m.activeEpicProgressionAdvance)
	}
	if fixture.halted != 0 {
		t.Fatalf("successful launch halted auto-progression %d times", fixture.halted)
	}
}

func TestProgressionChildAutoModeDrainsItsLaterPhases(t *testing.T) {
	child := progressionAdvanceFlow("flow-child", progressionLaunchRepo, progressionLaunchChild, progressionLaunchEpic, flowstore.StatusInProgress)
	child.AutoMode = true
	child.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseRunning},
		{PhaseID: "implement", Kind: flowstore.KindImplementation, Status: flowstore.PhaseBlocked},
	}
	completed := cloneFlowRecord(child)
	completed.Phases = []flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted},
		{PhaseID: "implement", Kind: flowstore.KindImplementation, Status: flowstore.PhaseBlocked},
	}

	m := Model{}
	m, _, _ = m.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{child}, []flowstore.FlowRecord{completed}, 1)
	if _, armed := m.autoAdvanceDrainFlows[child.FlowID]; !armed {
		t.Fatalf("AutoMode child was not armed for drain: %#v", m.autoAdvanceDrainFlows)
	}

	manual := cloneFlowRecord(child)
	manual.AutoMode = false
	manualCompleted := cloneFlowRecord(completed)
	manualCompleted.AutoMode = false
	next := Model{}
	next, _, _ = next.prepareAutoFlowPhaseLaunchForRequest(
		[]flowstore.FlowRecord{manual}, []flowstore.FlowRecord{manualCompleted}, 1)
	if len(next.autoAdvanceDrainFlows) != 0 {
		t.Fatalf("AutoMode off still armed drain: %#v", next.autoAdvanceDrainFlows)
	}
}

func TestProgressionSuccessorReconciliationOutcomesAbortBeforeLaunch(t *testing.T) {
	for _, tt := range []struct {
		name       string
		outcome    flowstore.EpicProgressionSuccessorOutcome
		err        error
		wantStatus string
		wantHalt   bool
	}{
		{name: "inactive", outcome: flowstore.EpicProgressionSuccessorInactive, wantStatus: "no longer active"},
		{name: "released", outcome: flowstore.EpicProgressionSuccessorReleased, wantStatus: "was released", wantHalt: true},
		{name: "owned obstruction", outcome: flowstore.EpicProgressionSuccessorOwnedObstruction, wantStatus: "blocks auto-progression", wantHalt: true},
		{name: "unreadable", err: errors.New("store busy"), wantStatus: "could not reconcile", wantHalt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture, m := newProgressionLaunchFixture(t, tt.outcome, tt.err)
			h := fixture.harness
			if tt.outcome == flowstore.EpicProgressionSuccessorOwnedObstruction {
				h.record.Status = flowstore.StatusBlocked
			}
			m, cmd := fixture.admit(t, m)
			m = drainProgressionLaunch(t, m, cmd)

			if len(h.contexts) != 0 {
				t.Fatalf("aborted reconciliation still launched an agent: %#v", h.contexts)
			}
			// The receipt already exists here, so the abort blocks the child's
			// startup roots instead of compensating a consumed finalizer.
			if len(h.phaseUpdates) == 0 || h.phaseUpdates[0].Status != flowstore.PhaseBlocked {
				t.Fatalf("aborted reconciliation left the child launchable: %#v", h.phaseUpdates)
			}
			if !strings.Contains(m.status.Text, tt.wantStatus) {
				t.Fatalf("status = %q, want %q", m.status.Text, tt.wantStatus)
			}
			if len(m.epicProgressionOwnedSuccessors) != 0 || m.activeEpicProgressionAdvance.Request != 0 {
				t.Fatalf("aborted advance retained runtime state: owned=%#v active=%#v",
					m.epicProgressionOwnedSuccessors, m.activeEpicProgressionAdvance)
			}
			if tt.wantHalt {
				if fixture.halted != 1 {
					t.Fatalf("abort halted %d times, want 1", fixture.halted)
				}
				if _, tracked := m.epicProgressionBaselines[fixture.key]; tracked {
					t.Fatal("halted abort retained the epic baseline")
				}
			} else if fixture.halted != 0 {
				t.Fatalf("already-inactive abort halted auto-progression: %#v", fixture.updates)
			}
		})
	}
}

func TestProgressionCreateFailureHaltsTheEpicWithItsCause(t *testing.T) {
	for _, tt := range []struct {
		name    string
		arrange func(*createLaunchHarness)
		flowID  bool
	}{
		{name: "create", arrange: func(h *createLaunchHarness) { h.createErr = errors.New("disk full") }},
		{name: "reserve", arrange: func(h *createLaunchHarness) { h.reserveErr = errors.New("locked") }, flowID: true},
		{name: "worktree", arrange: func(h *createLaunchHarness) { h.worktreeErr = errors.New("branch exists") }, flowID: true},
		{name: "launch id", arrange: func(h *createLaunchHarness) { h.addErr = errors.New("write failed") }, flowID: true},
		{name: "terminal", arrange: func(h *createLaunchHarness) { h.terminalErr = errors.New("no pty") }, flowID: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
			tt.arrange(fixture.harness)
			m, cmd := fixture.admit(t, m)
			m = drainProgressionLaunch(t, m, cmd)

			if fixture.halted != 1 || len(fixture.updates) != 1 {
				t.Fatalf("failed launch halted %d times: %#v (status %q)", fixture.halted, fixture.updates, m.status.Text)
			}
			halt := fixture.updates[0].Halt
			if halt.ChildBeadID != progressionLaunchChild || halt.Status != flowstore.StatusBlocked {
				t.Fatalf("halt tuple = %#v", halt)
			}
			wantSubject := "child Flow could not be created"
			if tt.flowID {
				wantSubject = "child Flow " + fixture.harness.record.FlowID + " could not launch its first phase"
			}
			if !strings.HasPrefix(halt.Message, wantSubject+": ") {
				t.Fatalf("halt message = %q, want a %q cause", halt.Message, wantSubject)
			}
		})
	}
}

func TestProgressionHaltTearsDownTrackingSoNoFurtherChildIsCreated(t *testing.T) {
	fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
	fixture.harness.worktreeErr = errors.New("branch exists")
	m, cmd := fixture.admit(t, m)
	m = drainProgressionLaunch(t, m, cmd)
	if _, tracked := m.epicProgressionBaselines[fixture.key]; tracked {
		t.Fatalf("halt retained the epic baseline: %#v", m.epicProgressionBaselines)
	}
	if len(m.epicProgressionOwnedSuccessors) != 0 || m.flowPreparationAdmission {
		t.Fatalf("halt retained runtime state: owned=%#v admission=%t", m.epicProgressionOwnedSuccessors, m.flowPreparationAdmission)
	}
	if fixture.halted != 1 {
		t.Fatalf("teardown without a durable halt: %#v", fixture.updates)
	}
	// The failure's own message keeps the status slot: it names the same cause
	// the halt records, and a create-failure status outranks the poll's
	// announcement rather than being replaced by a vaguer one.
	if !strings.Contains(m.status.Text, "create worktree: branch exists") {
		t.Fatalf("halt status = %q", m.status.Text)
	}
}

func TestProgressionCreateRefusesASupersededAdvanceAndReleasesAdmission(t *testing.T) {
	fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
	create := flowLaunchCreateRequest{
		Presentation: flowLaunchCreatePresentation{
			Origin: flowLaunchOriginEpicProgression, Request: m.activeEpicProgressionAdvance.Request,
		},
		RepoPath: progressionLaunchRepo, Title: "New Flow", Instructions: "Write the plan.",
		Bead: fixture.harness.record.Bead, BaseRef: "main", Headless: true,
	}
	// A newer advance took the admission before the selection landed.
	m.activeEpicProgressionAdvance.Request++
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(""))
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
		Kind: flowLaunchKindCreatePhase, Origin: flowLaunchOriginEpicProgression, Create: create, Settings: settings,
	})
	if admitted || cmd != nil {
		t.Fatalf("superseded progression create was admitted: %v", admitted)
	}
	if len(fixture.harness.order) != 0 {
		t.Fatalf("refused create reached persistence: %#v", fixture.harness.order)
	}
	if fixture.halted != 0 {
		t.Fatal("a superseded request halted auto-progression")
	}
	if next.activeEpicProgressionAdvance.Request != m.activeEpicProgressionAdvance.Request || !next.flowPreparationAdmission {
		t.Fatalf("refusal disturbed the newer advance: active=%#v admission=%t",
			next.activeEpicProgressionAdvance, next.flowPreparationAdmission)
	}

	// The advance that does own the admission releases it when its own create
	// request is refused before persistence.
	m.launchSeams.AllocateFlowID = nil
	create.Presentation.Request = m.activeEpicProgressionAdvance.Request
	next, _, admitted = m.requestFlowLaunch(flowLaunchIntent{
		Kind: flowLaunchKindCreatePhase, Origin: flowLaunchOriginEpicProgression, Create: create, Settings: settings,
	})
	if admitted {
		t.Fatal("create without ID allocation was admitted")
	}
	if next.activeEpicProgressionAdvance.Request != 0 {
		t.Fatalf("refused create retained its advance: %#v", next.activeEpicProgressionAdvance)
	}
	// The advance admission was released and immediately re-taken by the halt
	// this refusal raises: an epic whose child cannot even be allocated is as
	// stuck as one whose child failed mid-pipeline.
	if next.flowPreparationOwner.Kind != flowPreparationEpicHalt || next.activeEpicProgressionHalt.Request == 0 {
		t.Fatalf("refused create did not hand admission to the halt: owner=%#v halt=%#v",
			next.flowPreparationOwner, next.activeEpicProgressionHalt)
	}
}

// TestProgressionCreateSurfacesBeadSlotRefusalAndHalts covers the residual race
// the selection filter cannot close: another process commits a Flow for the
// selected child between the advance's listFlows snapshot and this create. The
// store refuses, and because a refusal writes nothing there is no record, no
// worktree and no receipt to recover — so the pre-write exit runs, naming the
// Flow that holds the Bead, and halts the epic exactly as every other pre-write
// create failure does. A halt is the bounded outcome: it is visible, it names
// the cause, and re-enabling is one key away.
func TestProgressionCreateSurfacesBeadSlotRefusalAndHalts(t *testing.T) {
	winner := flowstore.FlowRecord{
		FlowID: "20260816T025735Z-winner",
		Bead:   flowstore.BeadLink{ID: progressionLaunchChild},
		Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseNeedsAttention}},
	}
	fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
	h := fixture.harness
	h.createErr = &flowstore.BeadFlowActiveError{
		RepoPath: progressionLaunchRepo, BeadID: progressionLaunchChild, Existing: winner,
	}

	m, cmd := fixture.admit(t, m)
	m = drainProgressionLaunch(t, m, cmd)

	want := conflictStatus(winner, true)
	if m.status.Text != want {
		t.Fatalf("status = %q, want the named conflict %q", m.status.Text, want)
	}
	if !strings.Contains(m.status.Text, winner.FlowID) {
		t.Fatalf("status %q does not name the Flow holding the Bead", m.status.Text)
	}
	// Nothing was written, so nothing may have been provisioned or reconciled.
	for _, step := range h.order {
		if step == "worktree" || strings.HasPrefix(step, "reconcile:") {
			t.Fatalf("refused create provisioned or reconciled: %#v", h.order)
		}
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("refused create mutated phases: %#v", h.phaseUpdates)
	}
	if fixture.halted != 1 {
		t.Fatalf("refused create halted %d times, want exactly one durable halt: %#v", fixture.halted, fixture.updates)
	}
	if fixture.updates[0].Halt.ChildBeadID != progressionLaunchChild {
		t.Fatalf("halt names child %q, want %q", fixture.updates[0].Halt.ChildBeadID, progressionLaunchChild)
	}
	if !strings.Contains(fixture.updates[0].Halt.Message, winner.FlowID) {
		t.Fatalf("halt message %q does not name the Flow holding the Bead", fixture.updates[0].Halt.Message)
	}
	// The advance must not keep the epic pinned: a refusal that leaked the
	// admission would wedge every later poll behind a create that never ran.
	if m.activeEpicProgressionAdvance.Request != 0 || m.flowPreparationAdmission {
		t.Fatalf("refused create retained runtime state: active=%#v admission=%t",
			m.activeEpicProgressionAdvance, m.flowPreparationAdmission)
	}
	if len(m.epicProgressionOwnedSuccessors) != 0 {
		t.Fatalf("owned successors = %#v, want none; adopting the winner livelocks reconciliation", m.epicProgressionOwnedSuccessors)
	}
}

func TestProgressionCreateUnreadableBeadRefusalHaltsAndStopsLaterPolls(t *testing.T) {
	refusal := &flowstore.BeadFlowUnreadableError{
		RepoPath: progressionLaunchRepo, BeadID: progressionLaunchChild,
		FlowID: "20260823T210000Z-unreadable", Err: errors.New("unsupported schema version 9999"),
	}
	fixture, m := newProgressionLaunchFixture(t, flowstore.EpicProgressionSuccessorAccepted, nil)
	h := fixture.harness
	h.createErr = refusal

	m, cmd := fixture.admit(t, m)
	m = drainProgressionLaunch(t, m, cmd)

	if fixture.halted != 1 || len(fixture.updates) != 1 {
		t.Fatalf("unreadable refusal halted %d times: %#v", fixture.halted, fixture.updates)
	}
	halt := fixture.updates[0].Halt
	if halt.ChildBeadID != progressionLaunchChild || halt.Status != flowstore.StatusBlocked ||
		!strings.Contains(halt.Message, refusal.FlowID) {
		t.Fatalf("halt tuple = %#v, want child %q and unreadable Flow %q", halt, progressionLaunchChild, refusal.FlowID)
	}
	if m.status.Text != refusal.Error() {
		t.Fatalf("status = %q, want refusal %q", m.status.Text, refusal.Error())
	}
	for _, step := range h.order {
		if step == "worktree" || step == "finalize" || strings.HasPrefix(step, "reconcile:") || strings.HasPrefix(step, "launch-id:") {
			t.Fatalf("unreadable refusal produced a post-create side effect: %#v", h.order)
		}
	}
	if len(h.contexts) != 0 || len(h.phaseUpdates) != 0 {
		t.Fatalf("unreadable refusal launched or updated phases: contexts=%#v updates=%#v", h.contexts, h.phaseUpdates)
	}
	if m.activeEpicProgressionAdvance.Request != 0 || m.flowPreparationAdmission {
		t.Fatalf("halt retained advance admission: active=%#v admission=%t", m.activeEpicProgressionAdvance, m.flowPreparationAdmission)
	}
	if _, tracked := m.epicProgressionBaselines[fixture.key]; tracked {
		t.Fatalf("halt retained baseline: %#v", m.epicProgressionBaselines)
	}
	if len(m.epicProgressionBaselineMinimumRequests) != 0 || len(m.epicProgressionOwnedSuccessors) != 0 {
		t.Fatalf("halt retained progression tracking: minimum=%#v owned=%#v",
			m.epicProgressionBaselineMinimumRequests, m.epicProgressionOwnedSuccessors)
	}

	readCalls := 0
	m.readEpicProgression = func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
		readCalls++
		return flowstore.EpicProgression{RepoPath: progressionLaunchRepo, EpicID: progressionLaunchEpic, Enabled: true}, true, nil
	}
	m.autoAdvanceInFlight = 1
	later := cloneFlowRecord(fixture.source)
	later.Status = flowstore.StatusCompleted
	next, laterCmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{later}, Request: 1})
	for _, msg := range immediateFlowRefreshMessages(laterCmd) {
		if _, ok := msg.(epicProgressionAdvanceResultMsg); ok {
			t.Fatal("later poll scheduled the durably halted epic again")
		}
	}
	if readCalls != 0 || next.activeEpicProgressionAdvance.Request != 0 || next.flowPreparationAdmission {
		t.Fatalf("later poll touched halted progression: reads=%d active=%#v admission=%t",
			readCalls, next.activeEpicProgressionAdvance, next.flowPreparationAdmission)
	}
}
