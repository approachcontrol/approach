package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

const flowCreateInProgressStatus = "Flow creation is already in progress"

// flowLaunchCreateRequest is the creation-only portion of a lifecycle intent.
// Request and RepoPath together are its presentation fence; the remaining
// fields are immutable form snapshots.
type flowLaunchCreateRequest struct {
	Request      uint64
	RepoPath     string
	Title        string
	Instructions string
	BaseRef      string
	Headless     bool
}

// flowLaunchCreateRequestedMsg keeps the form thin: it carries intent back to
// Update, where requestFlowLaunch performs admission and owns every side effect.
type flowLaunchCreateRequestedMsg struct {
	Create flowLaunchCreateRequest
}

type flowLaunchCreateProof int

const (
	flowLaunchCreateProofNone flowLaunchCreateProof = iota
	flowLaunchCreateProofAbsent
	flowLaunchCreateProofPresent
	flowLaunchCreateProofUnknown
)

func (m Model) createFlowLaunchOriginCurrent(create flowLaunchCreateRequest) bool {
	return create.Request != 0 && m.isCurrentFlowCreateRequest(create.Request) && m.isCurrentRepo(create.RepoPath)
}

func (m Model) admitCreateFlowLaunch(intent flowLaunchIntent) (Model, tea.Cmd, bool) {
	create := intent.Create
	if !m.createFlowLaunchOriginCurrent(create) {
		return m.clearFlowCreateRequest(create.Request), nil, false
	}
	token := strings.TrimSpace(m.launchSeams.newLaunchID())
	if token == "" {
		return m.clearFlowCreateRequest(create.Request).setStatus(statusOther, "Unable to allocate a Flow launch ID"), nil, false
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(token))
	command := agent.Normalize(settings.Command)
	if command == "" || agent.NormalizeStored(settings.Command) != command {
		return m.clearFlowCreateRequest(create.Request).setStatus(statusOther, flowLaunchNoAgentCommandStatus), nil, false
	}
	if err := agent.Validate(command); err != nil {
		return m.clearFlowCreateRequest(create.Request).setStatus(statusOther, err.Error()), nil, false
	}
	settings.Command = command
	allocate := m.launchSeams.AllocateFlowID
	if allocate == nil {
		return m.clearFlowCreateRequest(create.Request).setStatus(statusOther, "Flow launch lifecycle is missing ID allocation"), nil, false
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
			return m.finishCreateAfterWrite(attempt, "reserve launch: "+msg.Err)
		}
		if msg.Record.FlowID != msg.FlowID {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, fmt.Sprintf("reserve launch returned flow %q", msg.Record.FlowID))
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
			return m.beginCreateFlowRecovery(attempt, msg, []string{"create worktree: " + msg.Err}, false, true)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateBootstrap)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		return next, createFlowLaunchBootstrapCmd(next.launchSeams, attempt, msg)

	case flowLaunchStageCreateBootstrap:
		if msg.Err != "" {
			operation := msg.ErrOp
			if operation == "" {
				operation = "run bootstrap"
			}
			return m.beginCreateFlowRecovery(attempt, msg, []string{operation + ": " + msg.Err}, true, true)
		}
		if len(msg.StartupRoots) == 0 {
			next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateMetadata)
			if !ok {
				releaseFlowLaunchReservation(msg.Release)
				return m, nil
			}
			msg.Parked = true
			return next, createFlowLaunchMetadataCmd(next.launchSeams, msg)
		}
		root := msg.StartupRoots[0]
		if !createFlowLaunchRootStillLaunchable(msg.Record, root) {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"validate startup root: no longer launchable"}, true, true)
		}
		if err := validateInitialFlowLaunchPhase(msg.Record, root); err != nil {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"validate startup root: " + err.Error()}, true, true)
		}
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateLaunchID)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		return next, createFlowLaunchIDCmd(next.launchSeams, msg)

	case flowLaunchStageCreateLaunchID:
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
			return m.beginCreateFlowRecovery(attempt, msg, parts, true, block)
		}
		root := msg.StartupRoots[0]
		phase, ok := flowPhaseByID(msg.Record, root.PhaseID)
		if !ok || !flowPhaseContainsLaunch(phase, msg.Token) {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"AddPhaseLaunchID: launch proof missing"}, true, true)
		}
		m = m.markFlowLaunchAttemptMutatedPhase(msg.FlowID, msg.Token)
		attempt.MutatedPhase = true
		settings, err := resolveFlowStartPhaseAgentSettings(createFlowStartRequest(attempt), phase)
		if err != nil {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"resolve phase agent settings: " + err.Error()}, true, true)
		}
		msg.Context = createFlowLaunchContext(attempt, msg, phase, settings)
		next, ok := m.transitionFlowLaunchAttempt(msg.FlowID, msg.Token, want, flowLaunchStateCreateMetadata)
		if !ok {
			releaseFlowLaunchReservation(msg.Release)
			return m, nil
		}
		return next, createFlowLaunchMetadataCmd(next.launchSeams, msg)

	case flowLaunchStageCreateMetadata:
		if msg.Err != "" {
			if msg.Parked {
				releaseFlowLaunchReservation(msg.Release)
				return m.finishCreateAfterWrite(attempt, "record start metadata: "+msg.Err)
			}
			return m.beginCreateFlowRecovery(attempt, msg, []string{"record start metadata: " + msg.Err}, false, true)
		}
		if msg.Parked {
			releaseFlowLaunchReservation(msg.Release)
			m = m.releaseFlowLaunchAttempt(msg.FlowID, msg.Token).clearFlowCreateRequest(attempt.Create.Request)
			m = m.setStatus(statusOther, "Created flow: "+attempt.Create.Title)
			if m.flowRefreshSurfaceVisible() {
				return m.startFlowSurfaceFetch()
			}
			return m, nil
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
		releaseFlowLaunchReservation(msg.Release)
		m = m.releaseFlowLaunchAttempt(msg.FlowID, msg.Token).clearFlowCreateRequest(attempt.Create.Request)
		m = m.setStatus(statusOther, "Flow "+msg.FlowID+": "+msg.Err)
		if m.flowRefreshSurfaceVisible() {
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
		return m.clearFlowCreateRequest(msg.Create.Request), nil
	}
	if msg.Err != "" {
		return m.clearFlowCreateRequest(msg.Create.Request).setStatus(statusOther, msg.Err), nil
	}
	if !artifacts.IsSafeID(msg.FlowID) {
		return m.clearFlowCreateRequest(msg.Create.Request).
			setStatus(statusOther, fmt.Sprintf("Flow ID allocation returned invalid ID %q", msg.FlowID)), nil
	}
	if m.flowLaunchAdmissionOccupied(msg.FlowID) {
		return m.clearFlowCreateRequest(msg.Create.Request).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
	}
	attempt := flowLaunchAttempt{
		Token: msg.Token, Kind: flowLaunchKindCreatePhase, FlowID: msg.FlowID,
		Origin: flowLaunchOriginNewFlow, Settings: msg.Settings, Create: msg.Create,
	}
	next, ok := m.reserveFlowLaunchAttempt(attempt, flowLaunchStateCreateSessionReading)
	if !ok {
		return m.clearFlowCreateRequest(msg.Create.Request).setStatus(statusOther, noLaunchableFlowPhaseStatus), nil
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
		for _, record := range records {
			if flowSessionLive(record.Status, record.EndedAt) {
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
		if seams.CreateFlow == nil {
			event.Err = "Flow launch lifecycle is missing exact-ID creation"
			return event
		}
		req := createFlowStartRequest(attempt)
		record, err := seams.CreateFlow(flowstore.FlowRecord{
			FlowID: attempt.FlowID, RepoPath: req.RepoPath, Title: req.Title,
			Instructions: req.Instructions, BaseRef: req.BaseRef,
		}, flowstore.CreateOptions{Headless: req.Headless, PhaseAgent: phaseAgentSettingsForRequest(req)})
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
		if seams.ResolveCommit != nil {
			event.Commit = seams.ResolveCommit(prior.Worktree.WorktreePath)
		}
		if seams.BootstrapHookForRepo != nil {
			if hook, ok := seams.BootstrapHookForRepo(attempt.Create.RepoPath); ok {
				if seams.RunBootstrapHook == nil {
					event.Err = "Flow launch lifecycle is missing bootstrap runner"
					return event
				}
				if err := seams.RunBootstrapHook(actions.BootstrapContext{
					RepoPath: attempt.Create.RepoPath, WorktreePath: prior.Worktree.WorktreePath,
					Ref: prior.Worktree.Branch, Kind: actions.WorktreeCreateFlow,
				}, hook); err != nil {
					event.Err = err.Error()
					event.ErrOp = "run bootstrap"
					return event
				}
			}
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
		if seams.SetStartMetadata == nil {
			event.Err = "Flow launch lifecycle is missing start metadata persistence"
			return event
		}
		record, err := seams.SetStartMetadata(flowstore.StartMetadataUpdate{
			FlowID: prior.FlowID, WorktreePath: prior.Worktree.WorktreePath, Branch: prior.Worktree.Branch,
			BaseRef: prior.Create.BaseRef, Commit: prior.Commit,
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
		RepoPath: create.RepoPath, Title: create.Title, Instructions: create.Instructions, BaseRef: create.BaseRef,
		AgentCommand: attempt.Settings.Command, Model: attempt.Settings.Model, ReasoningEffort: attempt.Settings.ReasoningEffort,
		AgentPreferences: attempt.Settings.Preferences, AgentPreferencesProvided: true,
		SessionStateRoot: attempt.Settings.SessionStateRoot, FlowPromptTemplates: attempt.Settings.PromptTemplates,
		FlowPromptTemplatesProvided: true, Headless: &headless,
	}
}

func createFlowLaunchContext(attempt flowLaunchAttempt, msg flowLaunchEventMsg, phase flowstore.FlowPhase, settings agent.Settings) actions.AgentLaunchContext {
	req := createFlowStartRequest(attempt)
	record := flowStartPromptRecord(msg.Record, req, msg.Worktree, msg.Commit)
	title := phase.Title
	if strings.TrimSpace(title) == "" {
		title = phase.PhaseID
	}
	return actions.AgentLaunchContext{
		Command: settings.Command, Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
		LaunchID: attempt.Token, RepoPath: req.RepoPath, WorktreePath: msg.Worktree.WorktreePath,
		Branch: msg.Worktree.Branch, Commit: msg.Commit, SessionStateRoot: attempt.Settings.SessionStateRoot,
		PlanPhaseID: phase.PhaseID, PlanPhaseTitle: title, PlanPhaseStatus: flowstore.PhaseRunning,
		FlowID: msg.FlowID, FlowPhaseID: phase.PhaseID, FlowPhaseKind: flowstore.SemanticKind(phase),
		Headless: msg.Record.Headless, InitialPrompt: initialFlowLaunchPrompt(record, phase, attempt.Settings.PromptTemplates),
	}
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
	current := m.isCurrentFlowCreateRequest(attempt.Create.Request)
	m = m.clearFlowCreateRequest(attempt.Create.Request)
	if current && m.isCurrentRepo(attempt.Create.RepoPath) {
		m = m.setStatus(statusOther, errText)
	}
	return m, nil
}

func (m Model) finishCreateAfterWrite(attempt flowLaunchAttempt, errText string) (Model, tea.Cmd) {
	m = m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token)
	current := m.isCurrentFlowCreateRequest(attempt.Create.Request)
	present := current && m.isCurrentRepo(attempt.Create.RepoPath)
	m = m.clearFlowCreateRequest(attempt.Create.Request)
	if present {
		m = m.setStatus(statusOther, "Flow "+attempt.FlowID+": "+errText)
	}
	if present && m.flowRefreshSurfaceVisible() {
		return m.startFlowSurfaceFetch()
	}
	return m, nil
}

func (m Model) cancelCreateFlowLaunch(attempt flowLaunchAttempt, msg flowLaunchEventMsg) (Model, tea.Cmd) {
	switch msg.Stage {
	case flowLaunchStageCreateSessionsRead:
		return m.finishCreateBeforeWrite(attempt, "")
	case flowLaunchStageCreateWritten:
		if msg.Err != "" {
			return m.finishCreateBeforeWrite(attempt, "")
		}
		return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
	case flowLaunchStageCreateReserved:
		if msg.Err != "" || msg.Record.FlowID != msg.FlowID {
			releaseFlowLaunchReservation(msg.Release)
			return m.finishCreateAfterWrite(attempt, "creation canceled after repository changed")
		}
		msg.StartupRoots = launchablePhases(msg.Record)
		attempt.StartupRoots = append([]flowstore.FlowPhase(nil), msg.StartupRoots...)
		m = m.withFlowLaunchAttempt(attempt)
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, false, true)
	case flowLaunchStageCreateWorktree:
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, msg.Err == "", true)
	case flowLaunchStageCreateBootstrap:
		return m.beginCreateFlowRecovery(attempt, msg, []string{"creation canceled after repository changed"}, true, true)
	case flowLaunchStageCreateLaunchID:
		parts := []string{"creation canceled after repository changed"}
		if msg.Err != "" {
			parts = append([]string{"AddPhaseLaunchID: " + msg.Err}, msg.RecoveryErrs...)
			parts = append(parts, "creation canceled after repository changed")
		}
		return m.beginCreateFlowRecovery(attempt, msg, parts, true, true)
	case flowLaunchStageCreateMetadata:
		if msg.Err != "" {
			return m.beginCreateFlowRecovery(attempt, msg, []string{"record start metadata: " + msg.Err}, false, true)
		}
		if msg.Parked {
			releaseFlowLaunchReservation(msg.Release)
			return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).
				clearFlowCreateRequest(attempt.Create.Request), nil
		}
		return m.failCreateFlowLaunchEmbedded(attempt, msg.Context, "creation canceled after repository changed", msg.Release)
	case flowLaunchStageCreateRecovered:
		releaseFlowLaunchReservation(msg.Release)
		return m.releaseFlowLaunchAttempt(attempt.FlowID, attempt.Token).clearFlowCreateRequest(attempt.Create.Request), nil
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
	return next, createFlowLaunchRecoveryCmd(next.launchSeams, attempt, msg, parts, metadata, block)
}

func createFlowLaunchRecoveryCmd(seams flowLaunchSeams, attempt flowLaunchAttempt, prior flowLaunchEventMsg, parts []string, metadata, block bool) tea.Cmd {
	return func() tea.Msg {
		event := prior
		event.Stage, event.From = flowLaunchStageCreateRecovered, flowLaunchStateFailurePersisting
		errs := append([]string(nil), parts...)
		if metadata {
			if seams.SetStartMetadata == nil {
				errs = append(errs, "record start metadata: seam unavailable")
			} else if _, err := seams.SetStartMetadata(flowstore.StartMetadataUpdate{
				FlowID: prior.FlowID, WorktreePath: prior.Worktree.WorktreePath, Branch: prior.Worktree.Branch,
				BaseRef: attempt.Create.BaseRef, Commit: prior.Commit,
			}); err != nil {
				errs = append(errs, "record start metadata: "+err.Error())
			}
		}
		if block {
			for _, phase := range prior.StartupRoots {
				if seams.SetPhase == nil {
					errs = append(errs, "block phase "+phase.PhaseID+": seam unavailable")
					continue
				}
				if _, err := seams.SetPhase(blockedPhaseUpdate(prior.FlowID, phase, strings.Join(parts, "; "))); err != nil {
					errs = append(errs, "block phase "+phase.PhaseID+": "+err.Error())
				}
			}
		}
		event.Err = strings.Join(errs, "; ")
		return event
	}
}
