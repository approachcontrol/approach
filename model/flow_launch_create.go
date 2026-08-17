package model

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/sessions"
)

const flowCreateInProgressStatus = "Flow creation is already in progress"

// flowLaunchCreatePresentation is the typed identity of the UI request that
// owns creation-time status and focus. New-Flow and Ready-Bead requests use
// independent counters and must never clear or present for one another.
type flowLaunchCreatePresentation struct {
	Origin  flowLaunchOrigin
	Request uint64
}

// flowLaunchCreateRequest is the creation-only portion of a lifecycle intent.
// Presentation and RepoPath together are its source-aware presentation fence;
// the remaining fields are immutable request-time snapshots.
type flowLaunchCreateRequest struct {
	Presentation flowLaunchCreatePresentation
	RepoPath     string
	Title        string
	Instructions string
	Bead         flowstore.BeadLink
	BaseRef      string
	Headless     bool
}

// flowLaunchCreateRequestedMsg keeps the form thin: it carries intent and the
// submitting Model's settings snapshot back to Update, where requestFlowLaunch
// performs admission and owns every side effect.
type flowLaunchCreateRequestedMsg struct {
	Create   flowLaunchCreateRequest
	Settings flowLaunchAgentSettingsSnapshot
}

type flowLaunchCreateProof int

const (
	flowLaunchCreateProofNone flowLaunchCreateProof = iota
	flowLaunchCreateProofAbsent
	flowLaunchCreateProofPresent
	flowLaunchCreateProofUnknown
)

func (m Model) createFlowLaunchOriginCurrent(create flowLaunchCreateRequest) bool {
	if create.Presentation.Request == 0 || !m.isCurrentRepo(create.RepoPath) {
		return false
	}
	switch create.Presentation.Origin {
	case flowLaunchOriginNewFlow:
		return m.isCurrentFlowCreateRequest(create.Presentation.Request)
	case flowLaunchOriginReadyBead:
		return m.isCurrentReadyBeadFlowCreateRequest(create.Presentation.Request)
	default:
		return false
	}
}

func (m Model) clearFlowLaunchCreatePresentation(create flowLaunchCreateRequest) Model {
	switch create.Presentation.Origin {
	case flowLaunchOriginNewFlow:
		return m.clearFlowCreateRequest(create.Presentation.Request)
	case flowLaunchOriginReadyBead:
		m = m.clearReadyBeadFlowCreateRequest(create.Presentation.Request)
		return m.releaseFlowPreparation(flowPreparationReadyBead, m.flowPreparationOwner.Token)
	default:
		return m
	}
}

func (m Model) admitCreateFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	create := intent.Create
	if create.Presentation.Origin == 0 {
		create.Presentation.Origin = intent.Origin
	}
	if intent.Origin == 0 {
		intent.Origin = create.Presentation.Origin
	}
	if create.Presentation.Origin != intent.Origin {
		return m.clearFlowLaunchCreatePresentation(create), nil, false
	}
	intent.Create = create
	if !m.createFlowLaunchOriginCurrent(create) {
		return m.clearFlowLaunchCreatePresentation(create), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m.clearFlowLaunchCreatePresentation(create).setStatus(statusOther, "Unable to allocate a Flow launch ID"), nil, false
	}
	settings := intent.Settings
	command := agent.Normalize(settings.Command)
	if command == "" || agent.NormalizeStored(settings.Command) != command {
		return m.clearFlowLaunchCreatePresentation(create).setStatus(statusOther, flowLaunchNoAgentCommandStatus), nil, false
	}
	if err := agent.Validate(command); err != nil {
		return m.clearFlowLaunchCreatePresentation(create).setStatus(statusOther, err.Error()), nil, false
	}
	settings.Command = command
	// At admission, before a Flow ID is allocated and long before a worktree
	// exists: this is the only stage of the create pipeline where a refusal
	// leaves nothing behind, and the phase this creates is tracked exactly like
	// one launched from preflight.
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return m.clearFlowLaunchCreatePresentation(create).setStatus(statusOther, refusal), nil, false
	}
	allocate := m.launchSeams.AllocateFlowID
	if allocate == nil {
		return m.clearFlowLaunchCreatePresentation(create).setStatus(statusOther, "Flow launch lifecycle is missing ID allocation"), nil, false
	}
	return m, func() tea.Msg {
		flowID, err := allocate(create.Title)
		event := flowLaunchEventMsg{
			Token: token, Kind: flowLaunchKindCreatePhase, Stage: flowLaunchStageCreateAllocated,
			FlowID: strings.TrimSpace(flowID), Create: create, Settings: settings,
		}
		if err != nil {
			event.Err = err.Error()
		}
		return event
	}, true
}

func (m Model) handleCreateFlowLaunchEvent(msg flowLaunchEventMsg) (Model, tea.Cmd) {
	if msg.Stage == flowLaunchStageCreateAllocated {
		return m.handleCreateFlowAllocated(msg)
	}
	want, ok := createFlowLaunchStageState(msg.Stage)
	if !ok || msg.From != want {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	attempt, ok := m.matchingFlowLaunchAttempt(msg.FlowID, msg.Token, flowLaunchKindCreatePhase, want)
	if !ok {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	if !m.createFlowLaunchOriginCurrent(attempt.Create) {
		return m.cancelCreateFlowLaunch(attempt, msg)
	}

	switch msg.Stage {
	case flowLaunchStageCreateSessionsRead:
		if msg.Err != "" {
			return m.finishCreateBeforeWrite(attempt, msg.Err)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateWriting)
		if !ok {
			return m, nil
		}
		return next, createFlowLaunchWriteCmd(next.launchSeams, attempt, msg)

	case flowLaunchStageCreateWritten:
		if msg.Err != "" {
			return m.finishCreateBeforeWrite(attempt, fmt.Sprintf("create flow %s: %s", msg.FlowID, msg.Err))
		}
		if msg.Record.FlowID != msg.FlowID {
			if attempt.Origin == flowLaunchOriginReadyBead && msg.PreparationFinalizer != nil {
				return m.beginReadyPreparationCompensation(attempt, msg, []string{fmt.Sprintf("create flow %s returned flow %q", msg.FlowID, msg.Record.FlowID)})
			}
			return m.finishCreateBeforeWrite(attempt, fmt.Sprintf("create flow %s returned flow %q", msg.FlowID, msg.Record.FlowID))
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateReserving)
		if !ok {
			return m, nil
		}
		return next, createFlowLaunchReserveCmd(next.launchSeams, attempt, msg)

	case flowLaunchStageCreateReserved:
		if msg.Err != "" {
			releaseFlowLaunchReservation(msg.Release)
			msg.Release = nil
			if attempt.Origin == flowLaunchOriginReadyBead {
				return m.beginReadyPreparationCompensation(attempt, msg, []string{"reserve launch: " + msg.Err})
			}
			return m.finishCreateAfterWrite(attempt, "reserve launch: "+msg.Err)
		}
		if msg.Record.FlowID != msg.FlowID {
			releaseFlowLaunchReservation(msg.Release)
			msg.Release = nil
			if attempt.Origin == flowLaunchOriginReadyBead {
				return m.beginReadyPreparationCompensation(attempt, msg, []string{fmt.Sprintf("reserve launch returned flow %q", msg.Record.FlowID)})
			}
			return m.finishCreateAfterWrite(attempt, fmt.Sprintf("reserve launch returned flow %q", msg.Record.FlowID))
		}
		if createFlowReservedRecordClaimed(msg.CreatedRecord, msg.Record) {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, "reserve launch: flow was claimed by another launch before tracked reservation")
		}
		roots := launchablePhases(msg.Record)
		attempt.StartupRoots = append([]flowstore.FlowPhase(nil), roots...)
		m = m.withFlowLaunchAttempt(attempt)
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateWorktree)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		msg.StartupRoots = roots
		return next, createFlowLaunchWorktreeCmd(next.launchSeams, attempt, msg)

	case flowLaunchStageCreateWorktree:
		if msg.Err != "" {
			if attempt.Origin == flowLaunchOriginReadyBead {
				return m.beginReadyPreparationCompensation(attempt, msg, []string{"create worktree: " + msg.Err})
			}
			return m.beginCreateFlowRecovery(attempt, msg, []string{"create worktree: " + msg.Err}, false, true)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateMetadata)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		return next, createFlowLaunchMetadataCmd(next.launchSeams, msg)

	case flowLaunchStageCreateMetadata:
		if msg.Err != "" {
			return m.recoverCreateFlowMetadataFailure(attempt, msg, want, true)
		}
		return m.continueCreateAfterMetadata(attempt, msg, want)

	case flowLaunchStageCreateBootstrap:
		if msg.Err != "" {
			if msg.GenerationLost || msg.PreparationUnknown {
				releaseFlowLaunchReservation(msg.Release)
				return m.finishCreateAfterWrite(attempt, msg.ErrOp+": "+msg.Err)
			}
			operation := msg.ErrOp
			if operation == "" {
				operation = "run bootstrap"
			}
			return m.beginCreateFlowRecovery(attempt, msg, []string{operation + ": " + msg.Err}, false, true)
		}
		if len(msg.StartupRoots) == 0 {
			releaseFlowLaunchReservation(msg.Release)
			m = m.releaseFlowLaunchAttempt(msg.FlowID, msg.Token).clearFlowLaunchCreatePresentation(attempt.Create)
			m = m.setStatus(statusOther, "Created flow: "+attempt.Create.Title)
			if m.flowRefreshSurfaceVisible() {
				return m.startFlowSurfaceFetch()
			}
			return m, nil
		}
		root := msg.StartupRoots[0]
		if !createFlowLaunchRootStillLaunchable(msg.Record, root) {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"validate startup root: no longer launchable"}, false, true)
		}
		if err := validateInitialFlowLaunchPhase(msg.Record, root); err != nil {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"validate startup root: " + err.Error()}, false, true)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateLaunchID)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		return next, createFlowLaunchIDCmd(next.launchSeams, msg)

	case flowLaunchStageCreateLaunchID:
		if msg.GenerationLost {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, "AddPhaseLaunchID: "+msg.Err)
		}
		if msg.Err != "" {
			parts := []string{"AddPhaseLaunchID: " + msg.Err}
			if len(msg.RecoveryErrs) > 0 {
				parts = append(parts, msg.RecoveryErrs...)
			}
			block := msg.Proof == flowLaunchCreateProofPresent || msg.Proof == flowLaunchCreateProofUnknown
			if msg.Proof == flowLaunchCreateProofPresent {
				m = m.markFlowLaunchAttemptMutatedPhase(msg.FlowID, msg.Token)
				attempt.MutatedPhase = true
			}
			return m.beginCreateFlowRecovery(attempt, msg, parts, false, block)
		}
		root := msg.StartupRoots[0]
		phase, ok := flowPhaseByID(msg.Record, root.PhaseID)
		if !ok || !flowPhaseContainsLaunch(phase, msg.Token) {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"AddPhaseLaunchID: launch proof missing"}, false, true)
		}
		m = m.markFlowLaunchAttemptMutatedPhase(msg.FlowID, msg.Token)
		attempt.MutatedPhase = true
		settings, err := resolveFlowStartPhaseAgentSettings(createFlowStartRequest(attempt), phase)
		if err != nil {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"resolve phase agent settings: " + err.Error()}, false, true)
		}
		msg.Context = createFlowLaunchContext(attempt, msg, phase, settings)
		if phase.Status != flowstore.PhaseRunning {
			return m.failCreateFlowLaunchEmbedded(attempt, msg.Context, "launch proof changed before embedded install", msg.Release)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStatePreparing)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		msg.Route = flowLaunchRouteEmbedded
		msg.From = flowLaunchStatePreparing
		msg.Stage = flowLaunchStagePrepared
		msg.WorktreeNote = createdFlowWorktreeNote(msg.Record)
		attempt.State = flowLaunchStatePreparing
		return next.installFlowLaunchEmbedded(attempt, msg)

	case flowLaunchStageCreateRecovered:
		if next, cmd, ok := m.retryCreateFlowRecovery(attempt, msg); ok {
			return next, cmd
		}
		releaseFlowLaunchReservation(msg.Release)
		present := m.createFlowLaunchOriginCurrent(attempt.Create)
		m = m.releaseFlowLaunchAttempt(msg.FlowID, msg.Token).clearFlowLaunchCreatePresentation(attempt.Create)
		if present {
			m = m.setStatus(statusOther, "Flow "+msg.FlowID+": "+msg.Err)
		}
		if present && m.flowRefreshSurfaceVisible() {
			return m.startFlowSurfaceFetch()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleCreateFlowAllocated(msg flowLaunchEventMsg) (Model, tea.Cmd) {
	if msg.Kind != flowLaunchKindCreatePhase || strings.TrimSpace(msg.Token) == "" {
		return m, nil
	}
	if !m.createFlowLaunchOriginCurrent(msg.Create) {
		return m.clearFlowLaunchCreatePresentation(msg.Create), nil
	}
	if msg.Err != "" {
		return m.clearFlowLaunchCreatePresentation(msg.Create).setStatus(statusOther, msg.Err), nil
	}
	if !artifacts.IsSafeID(msg.FlowID) {
		return m.clearFlowLaunchCreatePresentation(msg.Create).
			setStatus(statusOther, fmt.Sprintf("Flow ID allocation returned invalid ID %q", msg.FlowID)), nil
	}
	if m.flowLaunchRuntimeOccupied(msg.FlowID) {
		return m.clearFlowLaunchCreatePresentation(msg.Create).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	attempt := flowLaunchAttempt{
		Token: msg.Token, Kind: flowLaunchKindCreatePhase, FlowID: msg.FlowID,
		Origin: msg.Create.Presentation.Origin, Settings: msg.Settings, Create: msg.Create,
	}
	next, ok := m.reserveFlowLaunchAttempt(attempt, flowLaunchStateCreateSessionReading)
	if !ok {
		return m.clearFlowLaunchCreatePresentation(msg.Create).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	return next, createFlowLaunchSessionsCmd(next.launchSeams, attempt)
}

func createFlowLaunchStageState(stage flowLaunchStage) (flowLaunchState, bool) {
	switch stage {
	case flowLaunchStageCreateSessionsRead:
		return flowLaunchStateCreateSessionReading, true
	case flowLaunchStageCreateWritten:
		return flowLaunchStateCreateWriting, true
	case flowLaunchStageCreateReserved:
		return flowLaunchStateCreateReserving, true
	case flowLaunchStageCreateWorktree:
		return flowLaunchStateCreateWorktree, true
	case flowLaunchStageCreateBootstrap:
		return flowLaunchStateCreateBootstrap, true
	case flowLaunchStageCreateLaunchID:
		return flowLaunchStateCreateLaunchID, true
	case flowLaunchStageCreateMetadata:
		return flowLaunchStateCreateMetadata, true
	case flowLaunchStageCreateRecovered:
		return flowLaunchStateFailurePersisting, true
	default:
		return 0, false
	}
}

func createFlowLaunchSessionsCmd(seams flowLaunchSeams, attempt flowLaunchAttempt) tea.Cmd {
	return func() tea.Msg {
		event := createFlowLaunchEvent(attempt, flowLaunchStageCreateSessionsRead, flowLaunchStateCreateSessionReading)
		if seams.ListFlowSessions == nil {
			event.Err = "Flow launch lifecycle is missing session lookup"
			return event
		}
		records, err := seams.ListFlowSessions(attempt.FlowID)
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if len(records) != 0 {
			event.Err = "Flow ID is already associated with a saved session"
		}
		for _, record := range records {
			if sessions.IsActive(record.Status, record.EndedAt) {
				event.Err = "Flow ID has an active session"
				break
			}
		}
		return event
	}
}

func createFlowLaunchWriteCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := createFlowLaunchEvent(attempt, flowLaunchStageCreateWritten, flowLaunchStateCreateWriting)
		req := createFlowStartRequest(attempt)
		record := flowstore.FlowRecord{
			FlowID: attempt.FlowID, RepoPath: req.RepoPath, Title: req.Title,
			Instructions: req.Instructions, Bead: req.Bead, BaseRef: req.BaseRef,
		}
		opts := flowstore.CreateOptions{Headless: req.Headless, PhaseAgent: phaseAgentSettingsForRequest(req)}
		var err error
		if attempt.Origin == flowLaunchOriginReadyBead {
			if seams.CreatePreparation == nil {
				event.Err = "Flow launch lifecycle is missing exact-ID preparation"
				return event
			}
			record, event.PreparationFinalizer, err = seams.CreatePreparation(record, opts)
		} else {
			if seams.CreateFlow == nil {
				event.Err = "Flow launch lifecycle is missing exact-ID creation"
				return event
			}
			record, err = seams.CreateFlow(record, opts)
		}
		event.Record = record
		if err != nil {
			event.Err = err.Error()
		}
		return event
	}
}

func createFlowLaunchReserveCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := createFlowLaunchEvent(attempt, flowLaunchStageCreateReserved, flowLaunchStateCreateReserving)
		event.CreatedRecord = prior.Record
		event.PreparationFinalizer = prior.PreparationFinalizer
		if seams.ReserveLaunch == nil {
			event.Err = "Flow launch lifecycle is missing launch reservation"
			return event
		}
		record, release, err := seams.ReserveLaunch(attempt.FlowID)
		event.Record, event.Release = record, release
		if err != nil {
			event.Err = err.Error()
		}
		return event
	}
}

func createFlowReservedRecordClaimed(created, reserved flowstore.FlowRecord) bool {
	if !createFlowSameGeneration(created, reserved) || flowstore.FlowClosed(reserved) || reserved.Status != created.Status {
		return true
	}
	for _, pair := range [][2]string{
		{created.WorktreePath, reserved.WorktreePath},
		{created.Branch, reserved.Branch},
		{created.Commit, reserved.Commit},
		{created.PlanID, reserved.PlanID},
		{created.PlanPath, reserved.PlanPath},
	} {
		if strings.TrimSpace(pair[0]) != strings.TrimSpace(pair[1]) {
			return true
		}
	}
	if len(created.Phases) != len(reserved.Phases) {
		return true
	}
	createdPhases := make(map[string]flowstore.FlowPhase, len(created.Phases))
	for _, phase := range created.Phases {
		createdPhases[phase.PhaseID] = phase
	}
	for _, phase := range reserved.Phases {
		initial, ok := createdPhases[phase.PhaseID]
		if !ok || phase.Status != initial.Status ||
			!slices.Equal(phase.LaunchIDs, initial.LaunchIDs) || !slices.Equal(phase.Sessions, initial.Sessions) {
			return true
		}
	}
	return false
}

func createFlowSameGeneration(created, current flowstore.FlowRecord) bool {
	if created.FlowID == "" || current.FlowID != created.FlowID {
		return false
	}
	if !flowstore.SamePreparationIdentity(created, current) {
		return false
	}
	if !created.CreatedAt.IsZero() && !current.CreatedAt.Equal(created.CreatedAt) {
		return false
	}
	for _, pair := range [][2]string{
		{created.RepoPath, current.RepoPath},
		{created.Title, current.Title},
		{created.Instructions, current.Instructions},
		{created.BaseRef, current.BaseRef},
		{created.PresetName, current.PresetName},
	} {
		if strings.TrimSpace(pair[0]) != strings.TrimSpace(pair[1]) {
			return false
		}
	}
	return true
}

func createFlowLaunchWorktreeCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From = flowLaunchStageCreateWorktree, flowLaunchStateCreateWorktree
		if seams.CreateWorktree == nil {
			event.Err = "Flow launch lifecycle is missing worktree creation"
			return event
		}
		worktree, err := seams.CreateWorktree(attempt.Create.RepoPath, attempt.Create.Title, attempt.Create.BaseRef)
		event.Worktree = worktree
		if err != nil {
			event.Err = err.Error()
		}
		return event
	}
}

func createFlowLaunchBootstrapCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From, event.Err = flowLaunchStageCreateBootstrap, flowLaunchStateCreateBootstrap, ""
		runBootstrap := func() error {
			if hook, ok := seams.BootstrapHookForRepo(attempt.Create.RepoPath); ok {
				if seams.RunBootstrapHook == nil {
					return fmt.Errorf("Flow launch lifecycle is missing bootstrap runner")
				}
				if err := seams.RunBootstrapHook(actions.BootstrapContext{
					RepoPath: attempt.Create.RepoPath, WorktreePath: prior.Worktree.WorktreePath,
					Ref: prior.Worktree.Branch, Kind: actions.WorktreeCreateFlow,
				}, hook); err != nil {
					return err
				}
			}
			return nil
		}
		if seams.BootstrapHookForRepo == nil {
			runBootstrap = func() error { return nil }
		}
		if prior.PreparationFinalizer != nil {
			var bootstrapErr error
			finalized, err := prior.PreparationFinalizer.Finalize(func() error {
				bootstrapErr = runBootstrap()
				return bootstrapErr
			})
			event.Record = finalized
			if err != nil {
				event.ErrOp = "finalize preparation"
				if bootstrapErr != nil {
					event.ErrOp = "run bootstrap"
				}
				event.Err = err.Error()
				event.PreparationUnknown = flowstore.IsPreparationUnknown(err)
				return event
			}
			if finalized.PreparedAt == nil {
				event.ErrOp = "finalize preparation"
				event.Err = "finalizer returned no preparation receipt"
				return event
			}
		} else if err := runBootstrap(); err != nil {
			event.Err = err.Error()
			event.ErrOp = "run bootstrap"
			return event
		}
		if seams.ReadFlow == nil {
			event.Err, event.ErrOp = "read seam unavailable", "reread flow before AddPhaseLaunchID"
			return event
		}
		fresh, err := seams.ReadFlow(prior.FlowID)
		if err != nil {
			event.Err, event.ErrOp = err.Error(), "reread flow before AddPhaseLaunchID"
			return event
		}
		if fresh.FlowID != prior.FlowID {
			event.Err, event.ErrOp = fmt.Sprintf("returned flow %q", fresh.FlowID), "reread flow before AddPhaseLaunchID"
			return event
		}
		if !createFlowSameGeneration(prior.CreatedRecord, fresh) {
			event.Err, event.ErrOp, event.GenerationLost = "flow generation changed", "reread flow before AddPhaseLaunchID", true
			return event
		}
		event.Record = fresh
		return event
	}
}

func createFlowLaunchIDCmd(seams flowLaunchSeams, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From, event.Err = flowLaunchStageCreateLaunchID, flowLaunchStateCreateLaunchID, ""
		root := prior.StartupRoots[0]
		if seams.AddPhaseLaunchID == nil {
			event.Err, event.Proof = "Flow launch lifecycle is missing launch-ID persistence", flowLaunchCreateProofUnknown
			return event
		}
		record, err := seams.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: prior.FlowID, PhaseID: root.PhaseID, LaunchID: prior.Token})
		if err == nil && record.FlowID != prior.FlowID {
			err = fmt.Errorf("returned flow %q", record.FlowID)
		}
		if err == nil && !createFlowSameGeneration(prior.CreatedRecord, record) {
			event.Err, event.Proof, event.GenerationLost = "flow generation changed", flowLaunchCreateProofUnknown, true
			return event
		}
		if err == nil {
			if phase, ok := flowPhaseByID(record, root.PhaseID); ok && flowPhaseContainsLaunch(phase, prior.Token) {
				event.Record, event.Proof = record, flowLaunchCreateProofPresent
				return event
			}
			err = fmt.Errorf("write result did not prove launch %q", prior.Token)
		}
		if err != nil {
			event.Err = err.Error()
		}
		if seams.ReadFlow == nil {
			event.Proof = flowLaunchCreateProofUnknown
			event.RecoveryErrs = []string{"reread flow after AddPhaseLaunchID: read seam unavailable"}
			return event
		}
		fresh, readErr := seams.ReadFlow(prior.FlowID)
		if readErr != nil {
			event.Proof = flowLaunchCreateProofUnknown
			event.RecoveryErrs = []string{"reread flow after AddPhaseLaunchID: " + readErr.Error()}
			return event
		}
		if fresh.FlowID != prior.FlowID {
			event.Proof = flowLaunchCreateProofUnknown
			event.RecoveryErrs = []string{fmt.Sprintf("reread flow after AddPhaseLaunchID: returned flow %q", fresh.FlowID)}
			return event
		}
		if !createFlowSameGeneration(prior.CreatedRecord, fresh) {
			event.Err += "; reread flow after AddPhaseLaunchID: flow generation changed"
			event.Proof, event.GenerationLost = flowLaunchCreateProofUnknown, true
			return event
		}
		event.Record = fresh
		if phase, ok := flowPhaseByID(fresh, root.PhaseID); ok && flowPhaseContainsLaunch(phase, prior.Token) {
			event.Proof = flowLaunchCreateProofPresent
		} else {
			event.Proof = flowLaunchCreateProofAbsent
		}
		return event
	}
}

func createFlowLaunchMetadataCmd(seams flowLaunchSeams, prior flowLaunchEventMsg) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From, event.Err = flowLaunchStageCreateMetadata, flowLaunchStateCreateMetadata, ""
		if seams.ResolveCommit != nil {
			event.Commit = seams.ResolveCommit(prior.Worktree.WorktreePath)
		}
		if seams.SetStartMetadata == nil {
			event.Err = "Flow launch lifecycle is missing start metadata persistence"
			return event
		}
		record, err := seams.SetStartMetadata(flowstore.StartMetadataUpdate{
			FlowID: event.FlowID, WorktreePath: event.Worktree.WorktreePath, Branch: event.Worktree.Branch,
			BaseRef: event.Create.BaseRef, Commit: event.Commit,
		})
		if err != nil {
			event.Err = err.Error()
			return event
		}
		if record.FlowID != prior.FlowID {
			event.Err = fmt.Sprintf("start metadata returned flow %q", record.FlowID)
			return event
		}
		event.Record = record
		return event
	}
}

func createFlowLaunchEvent(attempt flowLaunchAttempt, stage flowLaunchStage, from flowLaunchState) flowLaunchEventMsg {
	return flowLaunchEventMsg{
		Token: attempt.Token, Kind: flowLaunchKindCreatePhase, From: from, FlowID: attempt.FlowID,
		Stage: stage, Create: attempt.Create, StartupRoots: append([]flowstore.FlowPhase(nil), attempt.StartupRoots...),
	}
}

func createFlowStartRequest(attempt flowLaunchAttempt) FlowStartRequest {
	create := attempt.Create
	headless := create.Headless
	return FlowStartRequest{
		RepoPath: create.RepoPath, Title: create.Title, Instructions: create.Instructions, Bead: create.Bead, BaseRef: create.BaseRef,
		AgentCommand: attempt.Settings.Command, Model: attempt.Settings.Model, ReasoningEffort: attempt.Settings.ReasoningEffort,
		AgentPreferences: attempt.Settings.Preferences, AgentPreferencesProvided: true,
		Headless: &headless,
	}
}

func createFlowLaunchContext(attempt flowLaunchAttempt, msg flowLaunchEventMsg, phase flowstore.FlowPhase, settings agent.Settings) actions.AgentLaunchContext {
	req := createFlowStartRequest(attempt)
	record := flowStartPromptRecord(msg.Record, req, msg.Worktree, msg.Commit)
	title := phase.Title
	if strings.TrimSpace(title) == "" {
		title = phase.PhaseID
	}
	ctx := actions.AgentLaunchContext{
		Command: settings.Command, Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		LaunchID: attempt.Token, RepoPath: req.RepoPath, WorktreePath: msg.Worktree.WorktreePath,
		Branch: msg.Worktree.Branch, Commit: msg.Commit, SessionStateRoot: attempt.Settings.SessionStateRoot,
		PlanPhaseID: phase.PhaseID, PlanPhaseTitle: title, PlanPhaseStatus: flowstore.PhaseRunning,
		FlowID: msg.FlowID, FlowPhaseID: phase.PhaseID, FlowPhaseKind: flowstore.SemanticKind(phase),
		Headless:      msg.Record.Headless,
		InitialPrompt: initialFlowLaunchPrompt(record, phase, attempt.Settings.PromptTemplates, attempt.Settings.Pin.ExecutablePath),
	}
	return applyLaunchPin(ctx, attempt.Settings.Pin)
}

func flowPhaseContainsLaunch(phase flowstore.FlowPhase, launchID string) bool {
	for _, candidate := range phase.LaunchIDs {
		if strings.TrimSpace(candidate) == strings.TrimSpace(launchID) {
			return true
		}
	}
	return false
}

func createFlowLaunchRootStillLaunchable(record flowstore.FlowRecord, root flowstore.FlowPhase) bool {
	phase, ok := flowPhaseByID(record, root.PhaseID)
	return ok && flowPhaseCanLaunch(record, phase)
}

func (m Model) finishCreateBeforeWrite(attempt flowLaunchAttempt, errText string) (Model, tea.Cmd) {
	m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	current := m.createFlowLaunchOriginCurrent(attempt.Create)
	m = m.clearFlowLaunchCreatePresentation(attempt.Create)
	if current {
		m = m.setStatus(statusOther, errText)
	}
	return m, nil
}

func (m Model) finishCreateAfterWrite(attempt flowLaunchAttempt, errText string) (Model, tea.Cmd) {
	m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	present := m.createFlowLaunchOriginCurrent(attempt.Create)
	m = m.clearFlowLaunchCreatePresentation(attempt.Create)
	if present {
		m = m.setStatus(statusOther, "Flow "+attempt.FlowID+": "+errText)
	}
	if present && m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func (m Model) cancelCreateFlowLaunch(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	if msg.GenerationLost {
		releaseFlowLaunchReservation(msg.Release)
		return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
	}
	switch msg.Stage {
	case flowLaunchStageCreateSessionsRead:
		return m.finishCreateBeforeWrite(attempt, "")
	case flowLaunchStageCreateWritten:
		if msg.Err != "" {
			return m.finishCreateBeforeWrite(attempt, "")
		}
		if attempt.Origin == flowLaunchOriginReadyBead {
			return m.beginReadyPreparationCompensation(attempt, msg, []string{"creation canceled after repository changed"})
		}
		return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
	case flowLaunchStageCreateReserved:
		if msg.Err != "" || msg.Record.FlowID != msg.FlowID {
			releaseFlowLaunchReservation(msg.Release)
			msg.Release = nil
			if attempt.Origin == flowLaunchOriginReadyBead {
				parts := []string{"creation canceled after repository changed"}
				if msg.Err != "" {
					parts = append([]string{"reserve launch: " + msg.Err}, parts...)
				} else {
					parts = append([]string{fmt.Sprintf("reserve launch returned flow %q", msg.Record.FlowID)}, parts...)
				}
				return m.beginReadyPreparationCompensation(attempt, msg, parts)
			}
			return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
		}
		if createFlowReservedRecordClaimed(msg.CreatedRecord, msg.Record) {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
		}
		msg.StartupRoots = launchablePhases(msg.Record)
		attempt.StartupRoots = append([]flowstore.FlowPhase(nil), msg.StartupRoots...)
		m = m.withFlowLaunchAttempt(attempt)
		if attempt.Origin == flowLaunchOriginReadyBead {
			return m.beginReadyPreparationCompensation(attempt, msg, []string{"creation canceled after repository changed"})
		}
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, false, true)
	case flowLaunchStageCreateWorktree:
		if attempt.Origin == flowLaunchOriginReadyBead {
			parts := []string{"creation canceled after repository changed"}
			if msg.Err != "" {
				parts = append([]string{"create worktree: " + msg.Err}, parts...)
			} else {
				worktreeRecord := msg.Record
				worktreeRecord.WorktreePath = msg.Worktree.WorktreePath
				worktreeRecord.Branch = msg.Worktree.Branch
				if note := createdFlowWorktreeNote(worktreeRecord); note != "" {
					parts = append(parts, note)
				}
			}
			return m.beginReadyPreparationCompensation(attempt, msg, parts)
		}
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, msg.Err == "", true)
	case flowLaunchStageCreateMetadata:
		if msg.Err != "" {
			return m.recoverCreateFlowMetadataFailure(attempt, msg, flowLaunchStateCreateMetadata, false)
		}
		if len(msg.StartupRoots) == 0 {
			releaseFlowLaunchReservation(msg.Release)
			return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
				clearFlowLaunchCreatePresentation(attempt.Create), nil
		}
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, false, true)
	case flowLaunchStageCreateBootstrap:
		if msg.PreparationUnknown {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, "")
		}
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, false, true)
	case flowLaunchStageCreateLaunchID:
		parts := []string{"creation canceled after repository changed"}
		if msg.Err != "" {
			parts = append([]string{"AddPhaseLaunchID: " + msg.Err}, msg.RecoveryErrs...)
			parts = append(parts, "creation canceled after repository changed")
		}
		return m.beginCreateFlowRecovery(attempt, msg, parts, false, true)
	case flowLaunchStageCreateRecovered:
		if next, cmd, ok := m.retryCreateFlowRecovery(attempt, msg); ok {
			return next, cmd
		}
		releaseFlowLaunchReservation(msg.Release)
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).clearFlowLaunchCreatePresentation(attempt.Create), nil
	default:
		releaseFlowLaunchReservation(msg.Release)
		return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
	}
}

func (m Model) beginCreateFlowRecovery(attempt flowLaunchAttempt, msg flowLaunchEventMsg, parts []string, metadata, block bool) (Model, tea.Cmd) {
	next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, attempt.State, flowLaunchStateFailurePersisting)
	if !ok {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	msg.RecoveryErrs = append([]string(nil), parts...)
	msg.RootBlockRetryable = false
	return next, createFlowLaunchRecoveryCmd(next.launchSeams, attempt, msg, parts, metadata, block)
}

func (m Model) retryCreateFlowRecovery(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd, bool) {
	if next, cmd, ok := m.retryReadyPreparationCompensation(attempt, msg); ok {
		return next, cmd, true
	}
	return m.retryCreateFlowRootBlocks(attempt, msg)
}

func (m Model) retryCreateFlowRootBlocks(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd, bool) {
	if !msg.RootBlockRetryable || len(msg.StartupRoots) == 0 {
		return m, nil, false
	}
	notes := msg.RecoveryErrs
	if len(notes) == 0 {
		notes = []string{"creation canceled after repository changed"}
	}
	next, cmd := m.beginCreateFlowRecovery(attempt, msg, notes, false, true)
	return next, cmd, true
}

const readyPreparationCompensationAttemptLimit = 3

func (m Model) beginReadyPreparationCompensation(attempt flowLaunchAttempt, msg flowLaunchEventMsg, parts []string) (Model, tea.Cmd) {
	next, ok := m.transitionFlowLaunchAttempt(attempt.FlowID, attempt.Token, attempt.State, flowLaunchStateFailurePersisting)
	if !ok {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	msg.RecoveryErrs = append([]string(nil), parts...)
	msg.CompensationRetryable = false
	return next, createReadyPreparationCompensationCmd(msg, parts)
}

func (m Model) retryReadyPreparationCompensation(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd, bool) {
	if !msg.CompensationRetryable || msg.PreparationFinalizer == nil {
		return m, nil, false
	}
	notes := msg.RecoveryErrs
	if len(notes) == 0 {
		notes = []string{"creation canceled after repository changed"}
	}
	next, cmd := m.beginReadyPreparationCompensation(attempt, msg, notes)
	return next, cmd, true
}

func createReadyPreparationCompensationCmd(prior flowLaunchEventMsg, parts []string) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From = flowLaunchStageCreateRecovered, flowLaunchStateFailurePersisting
		event.CompensationRetryable = false
		errs := append([]string(nil), parts...)
		if prior.PreparationFinalizer == nil {
			errs = append(errs, "compensate preparation: finalizer unavailable")
		} else {
			notes := strings.Join(parts, "; ")
			var (
				record flowstore.FlowRecord
				err    error
			)
			if prior.Release != nil {
				record, err = prior.PreparationFinalizer.CompensateUnderReservation(notes)
			} else {
				record, err = prior.PreparationFinalizer.Compensate(notes)
			}
			event.Record = record
			if err != nil {
				errs = append(errs, "compensate preparation: "+err.Error())
				if compensationRetryable(err) && prior.CompensationRetries+1 < readyPreparationCompensationAttemptLimit {
					event.CompensationRetryable = true
					event.CompensationRetries = prior.CompensationRetries + 1
				}
			}
		}
		event.Err = strings.Join(errs, "; ")
		return event
	}
}

func compensationRetryable(err error) bool {
	return flowstore.IsPreparationIncomplete(err) || flowstore.IsPreparationReservation(err)
}

func (m Model) continueCreateAfterMetadata(attempt flowLaunchAttempt, msg flowLaunchEventMsg, want flowLaunchState) (Model, tea.Cmd) {
	if !createFlowSameGeneration(msg.CreatedRecord, msg.Record) {
		releaseFlowLaunchReservation(msg.Release)
		return m.finishCreateAfterWrite(attempt, "record start metadata: flow generation changed")
	}
	next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateBootstrap)
	if !ok {
		releaseFlowLaunchReservation(msg.Release)
		return m, nil
	}
	return next, createFlowLaunchBootstrapCmd(next.launchSeams, attempt, msg)
}

func (m Model) recoverCreateFlowMetadataFailure(attempt flowLaunchAttempt, msg flowLaunchEventMsg, want flowLaunchState, continueIfLanded bool) (Model, tea.Cmd) {
	worktreeRecord := msg.Record
	worktreeRecord.WorktreePath = msg.Worktree.WorktreePath
	worktreeRecord.Branch = msg.Worktree.Branch
	parts := []string{"record start metadata: " + msg.Err}
	if note := createdFlowWorktreeNote(worktreeRecord); note != "" {
		parts = append(parts, note)
	}
	landed, record, readErr := reconcileCreateStartMetadata(m.launchSeams, attempt, msg)
	if readErr != nil && attempt.Origin == flowLaunchOriginReadyBead && msg.PreparationFinalizer != nil {
		for i := 0; i < 2 && readErr != nil; i++ {
			landed, record, readErr = reconcileCreateStartMetadata(m.launchSeams, attempt, msg)
		}
		if readErr != nil {
			// Do not Compensate: a landed own write looks like a foreign claim
			// and would consume the finalizer without blocking or continuing.
			// Do not SetPhase: that bypasses the nonce/claim fence.
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, strings.Join(parts, "; ")+"; reread start metadata: "+readErr.Error())
		}
	}
	if readErr == nil && landed {
		if continueIfLanded {
			msg.Err = ""
			msg.Record = record
			return m.continueCreateAfterMetadata(attempt, msg, want)
		}
		return m.beginCreateFlowRecovery(attempt, msg, parts, false, true)
	}
	if attempt.Origin == flowLaunchOriginReadyBead && msg.PreparationFinalizer != nil {
		return m.beginReadyPreparationCompensation(attempt, msg, parts)
	}
	return m.beginCreateFlowRecovery(attempt, msg, parts, false, true)
}

func reconcileCreateStartMetadata(seams flowLaunchSeams, attempt flowLaunchAttempt, msg flowLaunchEventMsg) (bool, flowstore.FlowRecord, error) {
	if seams.ReadFlow == nil {
		return false, flowstore.FlowRecord{}, fmt.Errorf("read seam unavailable")
	}
	fresh, err := seams.ReadFlow(msg.FlowID)
	if err != nil {
		return false, flowstore.FlowRecord{}, err
	}
	commit := strings.TrimSpace(msg.Commit)
	if commit == "" && seams.ResolveCommit != nil {
		commit = seams.ResolveCommit(msg.Worktree.WorktreePath)
	}
	return parkedStartMetadataLanded(fresh, msg.Worktree, attempt.Create.BaseRef, commit), fresh, nil
}

func createFlowRootBlockStillNeeded(seams flowLaunchSeams, flowID string, phase flowstore.FlowPhase, launchID string) bool {
	if seams.ReadFlow == nil {
		return true
	}
	record, err := seams.ReadFlow(flowID)
	if err != nil {
		return true
	}
	ordered := flowstore.OrderedPhases(record.Phases)
	orderedFlow := record
	orderedFlow.Phases = ordered
	for i, current := range ordered {
		if current.PhaseID != phase.PhaseID {
			continue
		}
		if flowstore.PhaseGraphLaunchEligible(orderedFlow, i) {
			return true
		}
		// AddPhaseLaunchID may have made this exact token running before the
		// block write failed. Keep retrying that root; a foreign running phase
		// without this token is a concurrent claim and is left alone.
		return current.Status == flowstore.PhaseRunning &&
			strings.TrimSpace(launchID) != "" &&
			flowPhaseContainsLaunch(current, launchID)
	}
	return false
}

func createFlowLaunchRecoveryCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg, parts []string, metadata, block bool) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From = flowLaunchStageCreateRecovered, flowLaunchStateFailurePersisting
		errs := append([]string(nil), parts...)
		if metadata {
			commit := prior.Commit
			if strings.TrimSpace(commit) == "" && seams.ResolveCommit != nil {
				commit = seams.ResolveCommit(prior.Worktree.WorktreePath)
			}
			if seams.SetStartMetadata == nil {
				errs = append(errs, "record start metadata: seam unavailable")
			} else if _, err := seams.SetStartMetadata(flowstore.StartMetadataUpdate{
				FlowID: prior.FlowID, WorktreePath: prior.Worktree.WorktreePath, Branch: prior.Worktree.Branch,
				BaseRef: attempt.Create.BaseRef, Commit: commit,
			}); err != nil {
				errs = append(errs, "record start metadata: "+err.Error())
			}
		}
		if block {
			var remaining []flowstore.FlowPhase
			for _, phase := range prior.StartupRoots {
				if seams.SetPhase == nil {
					errs = append(errs, "block phase "+phase.PhaseID+": seam unavailable")
					remaining = append(remaining, phase)
					continue
				}
				notes := strings.Join(parts, "; ")
				if _, err := seams.SetPhase(blockedPhaseUpdate(prior.FlowID, phase, notes)); err != nil {
					errs = append(errs, "block phase "+phase.PhaseID+": "+err.Error())
					if createFlowRootBlockStillNeeded(seams, prior.FlowID, phase, prior.Token) {
						remaining = append(remaining, phase)
					}
				}
			}
			event.StartupRoots = remaining
			if len(remaining) > 0 && prior.CompensationRetries+1 < readyPreparationCompensationAttemptLimit {
				event.RootBlockRetryable = true
				event.CompensationRetries = prior.CompensationRetries + 1
			}
		}
		event.Err = strings.Join(errs, "; ")
		return event
	}
}
