package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/internal/artifacts"
	"github.com/brian-bell/wtui/ui"
)

type FlowPhaseLaunchRoute int

const (
	FlowPhaseLaunchExternal FlowPhaseLaunchRoute = iota
	FlowPhaseLaunchEmbedded
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
}

func (m Model) flowPhaseLauncher() FlowPhaseLauncher {
	command, model, reasoningEffort := m.flowLaunchAgentSettings()
	return FlowPhaseLauncher{
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
	switch command {
	case agent.CommandCodex, agent.CommandClaude:
		route = FlowPhaseLaunchEmbedded
		ctx.FlowLaunchTracked = true
		ctx.Embedded = true
		ctx.Headless = req.Headless
	}
	return FlowPhaseLaunchResult{Context: ctx, Route: route}, nil
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

func nextAutoLaunchPhase(record flowstore.FlowRecord) (flowstore.FlowPhase, bool) {
	phase, _, ok := flowstore.FirstLaunchablePhase(record)
	return phase, ok
}

func (m Model) selectedFlowNextLaunchablePhase() (flowstore.FlowRecord, flowstore.FlowPhase, bool) {
	record, ok := m.selectedFlow()
	if !ok || record.FlowID == "" {
		return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
	}
	ordered := flowstore.OrderedPhases(record.Phases)
	orderedRecord := record
	orderedRecord.Phases = ordered
	for i, phase := range ordered {
		if flowPhaseCanLaunchAtIndex(orderedRecord, i) {
			return record, phase, true
		}
	}
	return flowstore.FlowRecord{}, flowstore.FlowPhase{}, false
}

type flowPhaseLaunchTarget struct {
	FlowPhaseLaunchPreparedRequest
	AutoAdvanceRetryFlowID  string
	AutoAdvanceRetryPhaseID string
}

func (m Model) selectedFlowNextLaunchTarget() (flowPhaseLaunchTarget, bool, Model) {
	record, phase, ok := m.selectedFlowNextLaunchablePhase()
	if !ok {
		m = m.setStatus(statusOther, "No launchable Flow phase")
		return flowPhaseLaunchTarget{}, false, m
	}
	target, ok, m, _ := m.flowPhaseLaunchTarget(FlowPhaseLaunchRequest{
		Record:   record,
		Phase:    phase,
		Headless: m.flowHeadless,
	})
	return target, ok, m
}

func (m Model) launchFlowPhaseTarget(target flowPhaseLaunchTarget) (tea.Model, tea.Cmd) {
	return m, m.prepareFlowPhaseLaunch(target)
}

func (m Model) flowPhaseLaunchTarget(req FlowPhaseLaunchRequest) (flowPhaseLaunchTarget, bool, Model, tea.Cmd) {
	prepared, err := m.flowPhaseLauncher().Preflight(req)
	if err != nil {
		if !req.AutoLaunch {
			m = m.setStatus(statusOther, err.Error())
			return flowPhaseLaunchTarget{}, false, m, nil
		}
		var statusCmd tea.Cmd
		m, statusCmd = m.setAutoAdvanceStatus("Flow " + flowTitleForStatus(req.Record) + ": " + err.Error())
		return flowPhaseLaunchTarget{}, false, m, statusCmd
	}
	return flowPhaseLaunchTarget{FlowPhaseLaunchPreparedRequest: prepared}, true, m, nil
}

func (m Model) prepareFlowPhaseLaunch(target flowPhaseLaunchTarget) tea.Cmd {
	return func() tea.Msg {
		result, err := m.flowPhaseLauncher().Prepare(target.FlowPhaseLaunchPreparedRequest)
		if err != nil {
			return ActionFailedMsg{
				RepoPath:                target.RepoPath,
				Err:                     err.Error(),
				AutoAdvanceRetryFlowID:  target.AutoAdvanceRetryFlowID,
				AutoAdvanceRetryPhaseID: target.AutoAdvanceRetryPhaseID,
			}
		}
		if result.Skipped {
			return nil
		}
		return m.flowPhaseLaunchMessage(result)
	}
}

func (m Model) flowPhaseLaunchMessage(result FlowPhaseLaunchResult) tea.Msg {
	if result.Route == FlowPhaseLaunchEmbedded {
		return FlowEmbeddedLaunchRequestedMsg{LaunchContext: result.Context}
	}
	return PlanLaunchRequestedMsg{LaunchContext: result.Context}
}

func (m Model) prepareAutoFlowPhaseLaunch(previousFlows, currentFlows []flowstore.FlowRecord) (Model, tea.Cmd, []string) {
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
		previous, ok := previousByFlowID[record.FlowID]
		if !ok {
			continue
		}
		if len(newlyStoppedAutoAdvanceFlowPhases(previous, record)) > 0 {
			m = m.disarmAutoAdvanceDrain(record.FlowID)
			continue
		}
		if len(newlyCompletedFlowPhases(previous, record)) > 0 {
			m = m.armAutoAdvanceDrain(record.FlowID)
		}
	}
	m, cmd := m.prepareAutoAdvanceDrainLaunches(currentFlows)
	return m, cmd, nil
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
		if phaseID == "" || !autoAdvanceStopStatus(phase.Status) {
			continue
		}
		previousPhase, ok := previousByPhaseID[phaseID]
		if ok && previousPhase.Status != phase.Status {
			stopped = append(stopped, phase)
		}
	}
	return stopped
}

func autoAdvanceStopStatus(status string) bool {
	switch status {
	case flowstore.PhaseSkipped, flowstore.PhaseBlocked, flowstore.PhaseNeedsAttention:
		return true
	default:
		return false
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
		if m.flowAutoAdvanceOccupied(record) {
			continue
		}
		phase, ok := nextAutoLaunchPhase(record)
		if !ok {
			m = m.disarmAutoAdvanceDrain(flowID)
			continue
		}
		target, targetOK, next, statusCmd := m.flowPhaseLaunchTarget(FlowPhaseLaunchRequest{
			Record:     record,
			Phase:      phase,
			AutoLaunch: true,
			Headless:   true,
		})
		m = next
		if !targetOK {
			cmds = append(cmds, statusCmd)
			continue
		}
		m = m.disarmAutoAdvanceDrain(record.FlowID)
		target.AutoAdvanceRetryFlowID = record.FlowID
		target.AutoAdvanceRetryPhaseID = phase.PhaseID
		m.autoAdvanceLaunchedPhases = append(m.autoAdvanceLaunchedPhases, autoAdvanceLaunchedPhase{
			FlowTitle: record.Title,
			PhaseID:   phase.PhaseID,
		})
		if cmd := m.prepareFlowPhaseLaunch(target); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, batchNonNil(cmds...)
}

func (m Model) flowAutoAdvanceOccupied(record flowstore.FlowRecord) bool {
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
