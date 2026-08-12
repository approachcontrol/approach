package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/ui"
)

type FlowPhaseLaunchRoute int

const (
	FlowPhaseLaunchExternal FlowPhaseLaunchRoute = iota
	FlowPhaseLaunchEmbedded
	// FlowPhaseLaunchTmux runs the agent as a window in the repo's tmux
	// session. It is external-style: nothing in the TUI owns the process.
	FlowPhaseLaunchTmux
)

type FlowPhaseLaunchRequest struct {
	Record     flowstore.FlowRecord
	Phase      flowstore.FlowPhase
	AutoLaunch bool
	Headless   bool
}

type FlowPhaseLaunchPreparedRequest struct {
	FlowPhaseLaunchRequest
	RepoPath     string
	WorktreePath string
	PlanPath     string
	LaunchID     string
}

type FlowPhaseLaunchResult struct {
	Context actions.AgentLaunchContext
	Route   FlowPhaseLaunchRoute
	Skipped bool
	// FallbackNote is set only when the tmux route was eligible and the
	// availability probe failed. Choosing the embedded backend, and the
	// headless design exclusion, are not fallbacks and set nothing.
	FallbackNote string
}

type FlowPhaseLaunchValidationError struct {
	Message string
}

func (err FlowPhaseLaunchValidationError) Error() string {
	return err.Message
}

type FlowPhaseLauncher struct {
	CurrentRepoPath      func() (string, bool)
	PlanMarkdownPath     func(string) (string, error)
	ReadPlan             func(string) (string, error)
	AddFlowPhaseLaunchID func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	NewLaunchID          func() string
	SessionStateRoot     string
	AgentCommand         string
	Model                string
	ReasoningEffort      string
	PromptTemplates      FlowPromptTemplates
	// Backend is config's [launch].backend. Empty means embedded.
	Backend string
	// TmuxAvailable probes for tmux. Nil means "probe the real PATH".
	TmuxAvailable func() bool
}

func (m Model) flowPhaseLauncher() FlowPhaseLauncher {
	command, model, reasoningEffort := m.flowLaunchAgentSettings()
	return FlowPhaseLauncher{
		Backend:              m.launchBackend,
		TmuxAvailable:        m.tmuxAvailable,
		CurrentRepoPath:      m.currentRepoPath,
		PlanMarkdownPath:     m.planMarkdownPath,
		ReadPlan:             m.readPlan,
		AddFlowPhaseLaunchID: m.addFlowPhaseLaunchID,
		NewLaunchID:          newLaunchID,
		SessionStateRoot:     m.sessionStateRoot,
		AgentCommand:         command,
		Model:                model,
		ReasoningEffort:      reasoningEffort,
		PromptTemplates:      m.flowPromptTemplates,
	}
}

func (l FlowPhaseLauncher) Preflight(req FlowPhaseLaunchRequest) (FlowPhaseLaunchPreparedRequest, error) {
	if agent.Normalize(l.AgentCommand) == "" {
		return FlowPhaseLaunchPreparedRequest{}, FlowPhaseLaunchValidationError{
			Message: "Press A to choose " + ui.AgentInputPlaceholder + " before launching an agent",
		}
	}
	repoPath := req.Record.RepoPath
	if repoPath == "" && l.CurrentRepoPath != nil {
		repoPath, _ = l.CurrentRepoPath()
	}
	worktreePath := req.Record.WorktreePath
	if worktreePath == "" {
		worktreePath = repoPath
	}
	if worktreePath == "" {
		return FlowPhaseLaunchPreparedRequest{}, FlowPhaseLaunchValidationError{Message: "Cannot determine launch path for this flow"}
	}
	planPath := req.Record.PlanPath
	if req.Record.PlanID != "" && planPath == "" {
		if l.PlanMarkdownPath == nil {
			return FlowPhaseLaunchPreparedRequest{}, FlowPhaseLaunchValidationError{Message: "Cannot determine linked plan path"}
		}
		var err error
		planPath, err = l.PlanMarkdownPath(req.Record.PlanID)
		if err != nil {
			return FlowPhaseLaunchPreparedRequest{}, FlowPhaseLaunchValidationError{Message: err.Error()}
		}
	}
	if flowstore.SemanticKind(req.Phase) == flowstore.KindPlanReview && req.Record.PlanID == "" {
		return FlowPhaseLaunchPreparedRequest{}, FlowPhaseLaunchValidationError{Message: "Plan Review needs a linked plan before launch"}
	}
	generateLaunchID := l.NewLaunchID
	if generateLaunchID == nil {
		generateLaunchID = newLaunchID
	}
	return FlowPhaseLaunchPreparedRequest{
		FlowPhaseLaunchRequest: req,
		RepoPath:               repoPath,
		WorktreePath:           worktreePath,
		PlanPath:               planPath,
		LaunchID:               generateLaunchID(),
	}, nil
}

func (l FlowPhaseLauncher) Prepare(req FlowPhaseLaunchPreparedRequest) (FlowPhaseLaunchResult, error) {
	planBody := ""
	if req.Record.PlanID != "" && flowPhasePromptNeedsPlanBody(req.Phase) {
		body, err := l.readPlan(req.Record.PlanID)
		if err != nil {
			return FlowPhaseLaunchResult{}, fmt.Errorf("failed to read linked plan %s: %w", req.Record.PlanID, err)
		}
		planBody = body
	}
	updated, err := l.addFlowPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:     req.Record.FlowID,
		PhaseID:    req.Phase.PhaseID,
		LaunchID:   req.LaunchID,
		AutoLaunch: req.AutoLaunch,
	})
	if err != nil {
		if req.AutoLaunch && flowstore.IsAutoLaunchOutdated(err) {
			return FlowPhaseLaunchResult{Skipped: true}, nil
		}
		return FlowPhaseLaunchResult{}, fmt.Errorf("failed to mark flow phase running: %w", err)
	}
	launchPhase := req.Phase
	if persistedPhase, ok := flowPhaseByID(updated, req.Phase.PhaseID); ok {
		launchPhase = persistedPhase
	}
	command := agent.Normalize(l.AgentCommand)
	ctx := actions.AgentLaunchContext{
		Command:          command,
		Model:            l.model(command),
		ReasoningEffort:  l.reasoningEffort(command),
		LaunchID:         req.LaunchID,
		RepoPath:         req.RepoPath,
		WorktreePath:     req.WorktreePath,
		Branch:           req.Record.Branch,
		Commit:           req.Record.Commit,
		SessionStateRoot: l.SessionStateRoot,
		PlanID:           req.Record.PlanID,
		PlanPath:         req.PlanPath,
		FlowID:           req.Record.FlowID,
		FlowPhaseID:      launchPhase.PhaseID,
		FlowPhaseKind:    flowstore.SemanticKind(launchPhase),
		FlowAutoLaunch:   req.AutoLaunch,
		InitialPrompt:    flowPhasePrompt(req.Record, launchPhase, req.PlanPath, planBody, l.PromptTemplates),
	}
	route := FlowPhaseLaunchExternal
	fallbackNote := ""
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		route = FlowPhaseLaunchEmbedded
		ctx.FlowLaunchTracked = true
		ctx.Embedded = true
		ctx.Headless = req.Headless
		// Persisted reservations carry UpdatedAt. A zero-time partial result from
		// an injected launcher seam cannot authoritatively replace preferences.
		if !req.AutoLaunch && !updated.UpdatedAt.IsZero() {
			ctx.Headless = updated.Headless
		}
		// Headless is resolved above, so the tmux decision is made against the
		// value that actually launches rather than the requested one.
		if tmuxRoute, fellBack := l.tmuxLaunchRoute(ctx); tmuxRoute {
			route = FlowPhaseLaunchTmux
			// A tmux window has no dock to prefill and renders its own output,
			// so it is external-style: the TUI owns no part of the process.
			ctx.Embedded = false
		} else if fellBack {
			fallbackNote = tmuxFallbackNote
		}
	}
	return FlowPhaseLaunchResult{Context: ctx, Route: route, FallbackNote: fallbackNote}, nil
}

// tmuxLaunchRoute applies the shared routing rule to the launcher's own copy of
// the backend and probe, which a lifecycle attempt snapshots at admission.
func (l FlowPhaseLauncher) tmuxLaunchRoute(ctx actions.AgentLaunchContext) (route bool, fellBack bool) {
	return tmuxLaunchRouteFor(l.Backend, l.TmuxAvailable, ctx)
}

func (l FlowPhaseLauncher) readPlan(planID string) (string, error) {
	if l.ReadPlan == nil {
		return "", nil
	}
	return l.ReadPlan(planID)
}

func (l FlowPhaseLauncher) addFlowPhaseLaunchID(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
	if l.AddFlowPhaseLaunchID == nil {
		return flowstore.FlowRecord{}, nil
	}
	return l.AddFlowPhaseLaunchID(update)
}

func (l FlowPhaseLauncher) reasoningEffort(command string) string {
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		return l.ReasoningEffort
	default:
		return ""
	}
}

func (l FlowPhaseLauncher) model(command string) string {
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		return l.Model
	default:
		return ""
	}
}

func newlyCompletedFlowPhase(previous, current flowstore.FlowRecord) (flowstore.FlowPhase, bool) {
	previousByPhaseID := make(map[string]flowstore.FlowPhase, len(previous.Phases))
	for _, phase := range previous.Phases {
		if phaseID := artifacts.NormalizePhaseID(phase.PhaseID); phaseID != "" {
			previousByPhaseID[phaseID] = phase
		}
	}
	for _, phase := range flowstore.OrderedPhases(current.Phases) {
		phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
		if phaseID == "" || phase.Status != flowstore.PhaseCompleted {
			continue
		}
		previousPhase, ok := previousByPhaseID[phaseID]
		if ok && previousPhase.Status != flowstore.PhaseCompleted {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

// nextAutoLaunchPhase is what flowLaunchCandidatePhase computes for an AutoMode
// intent with no named candidate. It survives as its own name because repair's
// marker consumption still calls it and routing repair through the lifecycle is
// a later bead; do not collapse the two until it is, or repair inherits the
// lifecycle's candidate rules by accident.
func nextAutoLaunchPhase(record flowstore.FlowRecord) (flowstore.FlowPhase, bool) {
	phase, _, ok := flowstore.FirstLaunchablePhase(record)
	return phase, ok
}

type repairAutoDrainMarker struct {
	MinimumRequest uint64
	CleanExit      bool
}

func (m Model) prepareAutoFlowPhaseLaunch(previousFlows, currentFlows []flowstore.FlowRecord) (Model, tea.Cmd, []string) {
	return m.prepareAutoFlowPhaseLaunchForRequest(previousFlows, currentFlows, 0)
}

func (m Model) prepareAutoFlowPhaseLaunchForRequest(previousFlows, currentFlows []flowstore.FlowRecord, request uint64) (Model, tea.Cmd, []string) {
	previousByFlowID := make(map[string]flowstore.FlowRecord, len(previousFlows))
	for _, record := range previousFlows {
		if record.FlowID != "" {
			previousByFlowID[record.FlowID] = record
		}
	}
	for _, record := range currentFlows {
		if record.FlowID == "" {
			continue
		}
		if !record.AutoMode {
			m = m.disarmAutoAdvanceDrain(record.FlowID)
			continue
		}
		if m.hasFlowRepairEmbeddedTerminalForFlow(record.FlowID) || m.hasPendingRepairAutoDrainMarker(record.FlowID) {
			m = m.disarmAutoAdvanceDrain(record.FlowID)
			continue
		}
		previous, ok := previousByFlowID[record.FlowID]
		if !ok {
			continue
		}
		if len(newlyStoppedAutoAdvanceFlowPhases(previous, record)) > 0 {
			// The stop edge suppresses an older retry without discarding prepare
			// work that may already have persisted its launch ID.
			m = m.suppressUnpersistedAutoFlowLaunchRetry(record.FlowID).disarmAutoAdvanceDrain(record.FlowID)
			continue
		}
		if len(newlyCompletedFlowPhases(previous, record)) > 0 {
			m = m.armAutoAdvanceDrain(record.FlowID)
		}
	}
	if request != 0 {
		m = m.consumeRepairAutoDrainMarkers(currentFlows, request)
	}
	m, cmd := m.prepareAutoAdvanceDrainLaunches(currentFlows)
	return m, cmd, nil
}

func (m Model) armRepairAutoDrain(flowID string) Model {
	return m.setRepairAutoDrainMarker(flowID, true)
}

func (m Model) suppressRepairAutoDrain(flowID string) Model {
	return m.setRepairAutoDrainMarker(flowID, false)
}

func (m Model) setRepairAutoDrainMarker(flowID string, cleanExit bool) Model {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return m
	}
	if m.pendingRepairAutoDrainFlowIDs == nil {
		m.pendingRepairAutoDrainFlowIDs = make(map[string]repairAutoDrainMarker)
	}
	marker := repairAutoDrainMarker{
		MinimumRequest: m.autoAdvanceRequestSeq + 1,
		CleanExit:      cleanExit,
	}
	// Repairs cannot overlap after launch reservation. The latest terminal
	// removal is therefore authoritative: a successful retry must replace an
	// earlier failed outcome, while a later failure must suppress an earlier
	// clean handoff that has not been consumed yet.
	m.pendingRepairAutoDrainFlowIDs[flowID] = marker
	return m
}

func (m Model) hasPendingRepairAutoDrainMarker(flowID string) bool {
	_, ok := m.pendingRepairAutoDrainFlowIDs[strings.TrimSpace(flowID)]
	return ok
}

// withoutRepairAutoDrainMarker drops a marker before the poll that would have
// consumed it. It copies rather than deleting in place because the caller is a
// message handler whose Model may still share this map with a value copy the
// poll captured.
func (m Model) withoutRepairAutoDrainMarker(flowID string) Model {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return m
	}
	if _, ok := m.pendingRepairAutoDrainFlowIDs[flowID]; !ok {
		return m
	}
	pending := make(map[string]repairAutoDrainMarker, len(m.pendingRepairAutoDrainFlowIDs)-1)
	for existingFlowID, marker := range m.pendingRepairAutoDrainFlowIDs {
		if existingFlowID != flowID {
			pending[existingFlowID] = marker
		}
	}
	if len(pending) == 0 {
		pending = nil
	}
	m.pendingRepairAutoDrainFlowIDs = pending
	return m
}

func (m Model) consumeRepairAutoDrainMarkers(records []flowstore.FlowRecord, request uint64) Model {
	if request == 0 || len(m.pendingRepairAutoDrainFlowIDs) == 0 {
		return m
	}
	recordsByID := make(map[string]flowstore.FlowRecord, len(records))
	for _, record := range records {
		if record.FlowID != "" {
			recordsByID[record.FlowID] = record
		}
	}
	for flowID, marker := range m.pendingRepairAutoDrainFlowIDs {
		if request < marker.MinimumRequest {
			continue
		}
		m = m.disarmAutoAdvanceDrain(flowID)
		record, ok := recordsByID[flowID]
		if !marker.CleanExit {
			if !ok {
				delete(m.pendingRepairAutoDrainFlowIDs, flowID)
				continue
			}
			switch flowstore.DeriveStatus(record) {
			case flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusAbandoned, flowstore.StatusClosed:
				delete(m.pendingRepairAutoDrainFlowIDs, flowID)
				continue
			}
			if !record.AutoMode {
				continue
			}
			if _, launchable := nextAutoLaunchPhase(record); !launchable {
				// A detached repair can mutate the Flow long after the first
				// post-removal poll. Retain suppression until such a mutation
				// actually exposes work that AutoMode could otherwise launch.
				continue
			}
			delete(m.pendingRepairAutoDrainFlowIDs, flowID)
			continue
		}
		delete(m.pendingRepairAutoDrainFlowIDs, flowID)
		if !ok || !record.AutoMode {
			continue
		}
		switch flowstore.DeriveStatus(record) {
		case flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusAbandoned, flowstore.StatusClosed:
			continue
		}
		m = m.armAutoAdvanceDrain(flowID)
	}
	if len(m.pendingRepairAutoDrainFlowIDs) == 0 {
		m.pendingRepairAutoDrainFlowIDs = nil
	}
	return m
}

func newlyCompletedFlowPhases(previous, current flowstore.FlowRecord) []flowstore.FlowPhase {
	previousByPhaseID := make(map[string]flowstore.FlowPhase, len(previous.Phases))
	for _, phase := range previous.Phases {
		if phaseID := artifacts.NormalizePhaseID(phase.PhaseID); phaseID != "" {
			previousByPhaseID[phaseID] = phase
		}
	}
	var completed []flowstore.FlowPhase
	for _, phase := range flowstore.OrderedPhases(current.Phases) {
		phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
		if phaseID == "" || phase.Status != flowstore.PhaseCompleted {
			continue
		}
		previousPhase, ok := previousByPhaseID[phaseID]
		if ok && previousPhase.Status != flowstore.PhaseCompleted {
			completed = append(completed, phase)
		}
	}
	return completed
}

func newlyStoppedAutoAdvanceFlowPhases(previous, current flowstore.FlowRecord) []flowstore.FlowPhase {
	previousByPhaseID := make(map[string]flowstore.FlowPhase, len(previous.Phases))
	for _, phase := range previous.Phases {
		if phaseID := artifacts.NormalizePhaseID(phase.PhaseID); phaseID != "" {
			previousByPhaseID[phaseID] = phase
		}
	}
	var stopped []flowstore.FlowPhase
	for _, phase := range flowstore.OrderedPhases(current.Phases) {
		phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
		if phaseID == "" {
			continue
		}
		previousPhase, ok := previousByPhaseID[phaseID]
		if ok && autoAdvanceStopped(previousPhase.Status, phase.Status) {
			stopped = append(stopped, phase)
		}
	}
	return stopped
}

func autoAdvanceStopped(previousStatus, currentStatus string) bool {
	switch currentStatus {
	case flowstore.PhaseSkipped, flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention:
		return previousStatus != currentStatus
	default:
		return previousStatus == flowstore.PhaseRunning && currentStatus == flowstore.PhaseReady
	}
}

func (m Model) armAutoAdvanceDrain(flowID string) Model {
	if strings.TrimSpace(flowID) == "" {
		return m
	}
	if m.autoAdvanceDrainFlows == nil {
		m.autoAdvanceDrainFlows = make(map[string]struct{})
	}
	m.autoAdvanceDrainFlows[flowID] = struct{}{}
	return m
}

func (m Model) disarmAutoAdvanceDrain(flowID string) Model {
	if len(m.autoAdvanceDrainFlows) == 0 {
		return m
	}
	delete(m.autoAdvanceDrainFlows, flowID)
	if len(m.autoAdvanceDrainFlows) == 0 {
		m.autoAdvanceDrainFlows = nil
	}
	return m
}

func (m Model) prepareAutoAdvanceDrainLaunches(records []flowstore.FlowRecord) (Model, tea.Cmd) {
	if len(m.autoAdvanceDrainFlows) == 0 {
		return m, nil
	}
	recordsByID := make(map[string]flowstore.FlowRecord, len(records))
	for _, record := range records {
		if record.FlowID != "" {
			recordsByID[record.FlowID] = record
		}
	}
	var cmds []tea.Cmd
	for flowID := range m.autoAdvanceDrainFlows {
		record, ok := recordsByID[flowID]
		if !ok || !record.AutoMode {
			m = m.disarmAutoAdvanceDrain(flowID)
			continue
		}
		// Closure is checked before occupancy, unlike every other reason to
		// stop draining. Occupancy only defers, so a closed Flow held by a
		// running terminal would keep its drain armed for as long as the
		// terminal lives, and a peer process closing the Flow emits no local
		// message that would clear it.
		if flowstore.FlowClosed(record) {
			m = m.disarmAutoAdvanceDrain(flowID)
			m = m.withoutRepairAutoDrainMarker(flowID)
			continue
		}
		// A cheap snapshot filter, not policy: the lifecycle re-checks
		// occupancy against the fresh record. Keeping it here means the two
		// occupancy sets are applied as a union.
		if m.flowAutoAdvanceOccupied(record) {
			continue
		}
		// Selection stays with the drain because "no candidate disarms" is
		// drain bookkeeping the lifecycle has no way to express, but it goes
		// through the lifecycle's own resolver rather than a second rule.
		phase, ok := flowLaunchCandidatePhase(flowLaunchKindAutoPhase, record, "")
		if !ok {
			m = m.disarmAutoAdvanceDrain(flowID)
			continue
		}
		// The snapshot half of the lifecycle's session check, and the reason a
		// stalled phase does not cost a session-store walk every second: the
		// lifecycle's own check re-arms the drain, so without this the next
		// poll would re-admit and call ListFlowSessions again at 1 Hz for as
		// long as the session stays live. The mirrored sessions the poll
		// already carries answer the common case for free. Deferral is silent
		// and leaves the drain armed, exactly as the lifecycle's retry does.
		if phaseHasMatchingLiveSession(phase) {
			continue
		}
		next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{
			Kind:      flowLaunchKindAutoPhase,
			FlowID:    record.FlowID,
			PhaseID:   phase.PhaseID,
			FlowTitle: flowTitleForStatus(record),
			Origin:    flowLaunchOriginAutoMode,
		})
		m = next
		if !admitted {
			// Silent by design; the drain stays armed for the next poll.
			continue
		}
		m = m.disarmAutoAdvanceDrain(record.FlowID)
		cmds = append(cmds, cmd)
	}
	return m, batchNonNil(cmds...)
}

func (m Model) flowAutoAdvanceOccupied(record flowstore.FlowRecord) bool {
	// A pending repair is a lifecycle attempt now, so attempt occupancy covers
	// it without a second signal.
	if m.flowLaunchAttemptOccupied(record.FlowID) {
		return true
	}
	for _, phase := range record.Phases {
		if phase.Status == flowstore.PhaseRunning {
			return true
		}
	}
	return m.hasFlowEmbeddedTerminalForFlow(record.FlowID)
}

func (m Model) hasFlowEmbeddedTerminalForFlow(flowID string) bool {
	if strings.TrimSpace(flowID) == "" {
		return false
	}
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == embeddedTerminalScopeFlow && slot.FlowID == flowID && slot.Terminal != nil {
			return true
		}
	}
	return false
}

func (m Model) hasFlowRepairEmbeddedTerminalForFlow(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return false
	}
	for _, slot := range m.embeddedTerminals {
		if slot.Scope == embeddedTerminalScopeFlow && slot.FlowID == flowID && slot.FlowRepair {
			return true
		}
	}
	return false
}

func flowPhaseCanLaunch(record flowstore.FlowRecord, phase flowstore.FlowPhase) bool {
	ordered := flowstore.OrderedPhases(record.Phases)
	orderedRecord := record
	orderedRecord.Phases = ordered
	phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
	for i, candidate := range ordered {
		if artifacts.NormalizePhaseID(candidate.PhaseID) == phaseID && candidate.Status == phase.Status {
			return flowPhaseCanLaunchAtIndex(orderedRecord, i)
		}
	}
	return false
}

func flowPhaseCanLaunchAtIndex(record flowstore.FlowRecord, phaseIndex int) bool {
	// The merge-kind and autoreview-rerun branches below bypass
	// PhaseLaunchEligible, so a closed Flow needs its own guard here.
	if flowstore.FlowClosed(record) {
		return false
	}
	if phaseIndex < 0 || phaseIndex >= len(record.Phases) {
		return false
	}
	phase := record.Phases[phaseIndex]
	if phase.Status == flowstore.PhaseReady {
		if flowstore.SemanticKind(phase) == flowstore.KindMerge {
			return flowstore.PhasePredecessorsSatisfied(record, phase.PhaseID)
		}
		return flowstore.PhaseLaunchEligible(record, phaseIndex)
	}
	return flowstore.SemanticKind(phase) == flowstore.KindAutoreview &&
		(phase.Status == flowstore.PhaseNeedsAttention || phase.Status == flowstore.PhaseBlocked) &&
		flowstore.HasPRTarget(record.PR) &&
		flowstore.PhasePredecessorsSatisfied(record, phase.PhaseID)
}

func flowPhaseStatusDetail(phase flowstore.FlowPhase) string {
	detail := strings.TrimSpace(phase.Status)
	if detail == "" {
		detail = "unknown"
	}
	if phase.Outcome != "" {
		detail += " / " + phase.Outcome
	}
	if phase.Notes != "" {
		detail += ": " + phase.Notes
	} else if phase.Summary != "" {
		detail += ": " + phase.Summary
	}
	return detail
}

func flowAutoreviewMissingPRTarget(record flowstore.FlowRecord) bool {
	if flowstore.HasPRTarget(record.PR) {
		return false
	}
	prCreation, hasPRCreation := flowstore.FindPhaseByKind(record, flowstore.KindPRCreation)
	autoreview, hasAutoreview := flowstore.FindPhaseByKind(record, flowstore.KindAutoreview)
	if !hasPRCreation || !hasAutoreview || prCreation.Status != flowstore.PhaseCompleted {
		return false
	}
	return autoreview.Status == flowstore.PhasePending ||
		autoreview.Status == flowstore.PhaseNeedsAttention ||
		autoreview.Status == flowstore.PhaseBlocked
}
