package model

import (
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

const flowPlanPhaseID = "plan"

// FlowStartRequest contains the user operation inputs needed to create a Flow
// and optionally prepare the initial plan-phase agent launch.
type FlowStartRequest struct {
	RepoPath            string
	Title               string
	Instructions        string
	BaseRef             string
	AgentCommand        string
	Model               string
	ReasoningEffort     string
	SessionStateRoot    string
	FlowPromptTemplates FlowPromptTemplates
	// FlowPromptTemplatesProvided forces StartPlan to use FlowPromptTemplates
	// even when every template has been reset to the built-in default.
	FlowPromptTemplatesProvided bool
	PlanPhaseID                 string
	PlanPhaseTitle              string
	PlanPhaseStatus             string
}

// FlowStartResult is the prepared or launch-ready result of creating a new Flow.
type FlowStartResult struct {
	Flow          flowstore.FlowRecord
	Worktree      actions.FlowWorktreeCreateResult
	Commit        string
	LaunchID      string
	LaunchSkipped bool
	LaunchContext actions.AgentLaunchContext
}

// FlowStarterOptions groups the deeper orchestration adapters for starting a
// Flow. Tests can replace these directly without widening Model.Options.
type FlowStarterOptions struct {
	CreateFlow           func(flowstore.FlowRecord, flowstore.PhaseAgentSettings) (flowstore.FlowRecord, error)
	CreateWorktree       func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	SetStartMetadata     func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	SetPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	AddPhaseLaunchID     func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	BootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	ResolveCommit        func(string) string
	NewLaunchID          func() string
	FlowPromptTemplates  FlowPromptTemplates
}

// FlowStarter owns the persistence, worktree, bootstrap, and recovery sequence
// for the initial Flow plan phase.
type FlowStarter struct {
	createFlow           func(flowstore.FlowRecord, flowstore.PhaseAgentSettings) (flowstore.FlowRecord, error)
	createWorktree       func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	setStartMetadata     func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	setPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	addPhaseLaunchID     func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	bootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	runBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	resolveCommit        func(string) string
	newLaunchID          func() string
	flowPromptTemplates  FlowPromptTemplates
}

func NewFlowStarter(opts FlowStarterOptions) FlowStarter {
	starter := FlowStarter{
		createFlow:           opts.CreateFlow,
		createWorktree:       opts.CreateWorktree,
		setStartMetadata:     opts.SetStartMetadata,
		setPhase:             opts.SetPhase,
		addPhaseLaunchID:     opts.AddPhaseLaunchID,
		bootstrapHookForRepo: opts.BootstrapHookForRepo,
		runBootstrapHook:     opts.RunBootstrapHook,
		resolveCommit:        opts.ResolveCommit,
		newLaunchID:          opts.NewLaunchID,
		flowPromptTemplates:  opts.FlowPromptTemplates,
	}
	if starter.createFlow == nil {
		starter.createFlow = func(flowstore.FlowRecord, flowstore.PhaseAgentSettings) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, fmt.Errorf("flow starter missing CreateFlow")
		}
	}
	if starter.createWorktree == nil {
		starter.createWorktree = actions.CreateFlowWorktree
	}
	if starter.setStartMetadata == nil {
		starter.setStartMetadata = func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		}
	}
	if starter.setPhase == nil {
		starter.setPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) { return flowstore.FlowRecord{}, nil }
	}
	if starter.addPhaseLaunchID == nil {
		starter.addPhaseLaunchID = func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		}
	}
	if starter.bootstrapHookForRepo == nil {
		starter.bootstrapHookForRepo = func(string) (actions.BootstrapHook, bool) { return actions.BootstrapHook{}, false }
	}
	if starter.runBootstrapHook == nil {
		starter.runBootstrapHook = actions.RunBootstrapHook
	}
	if starter.resolveCommit == nil {
		starter.resolveCommit = actions.ResolveWorktreeCommit
	}
	if starter.newLaunchID == nil {
		starter.newLaunchID = newLaunchID
	}
	return starter
}

func (s FlowStarter) StartPlan(req FlowStartRequest) (FlowStartResult, error) {
	result, err := s.PrepareFlow(req)
	if err != nil {
		return result, err
	}
	flow := result.Flow
	worktree := result.Worktree
	commit := result.Commit
	phase, ok := initialFlowLaunchPhase(flow, req.PlanPhaseID)
	if !ok {
		result.LaunchSkipped = true
		return result, nil
	}
	phaseID := phase.PhaseID

	if err := validateInitialFlowLaunchPhase(flow, phase); err != nil {
		return result, err
	}
	launchID := s.newLaunchID()
	launchedFlow, err := s.addPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   flow.FlowID,
		PhaseID:  phaseID,
		LaunchID: launchID,
	})
	if err != nil {
		return result, err
	}
	flow = launchedFlow
	result.Flow = flow
	result.LaunchID = launchID

	phaseTitle := req.PlanPhaseTitle
	if phaseTitle == "" {
		phaseTitle = phase.Title
	}
	if phaseTitle == "" {
		phaseTitle = phaseID
	}
	phaseStatus := req.PlanPhaseStatus
	if phaseStatus == "" {
		phaseStatus = flowstore.PhaseRunning
	}
	result.LaunchContext = actions.AgentLaunchContext{
		Command:          req.AgentCommand,
		Model:            req.Model,
		ReasoningEffort:  req.ReasoningEffort,
		LaunchID:         launchID,
		RepoPath:         req.RepoPath,
		WorktreePath:     worktree.WorktreePath,
		Branch:           worktree.Branch,
		Commit:           commit,
		SessionStateRoot: req.SessionStateRoot,
		PlanPhaseID:      phaseID,
		PlanPhaseTitle:   phaseTitle,
		PlanPhaseStatus:  phaseStatus,
		FlowID:           flow.FlowID,
		FlowPhaseID:      phaseID,
		FlowPhaseKind:    flowstore.SemanticKind(phase),
		InitialPrompt:    initialFlowLaunchPrompt(flowStartPromptRecord(flow, req, worktree, commit), phase, s.promptTemplatesForRequest(req)),
	}
	return result, nil
}

func validateInitialFlowLaunchPhase(flow flowstore.FlowRecord, phase flowstore.FlowPhase) error {
	if flowstore.SemanticKind(phase) == flowstore.KindPlanReview && flow.PlanID == "" {
		return fmt.Errorf("Plan Review needs a linked plan before launch")
	}
	return nil
}

func (s FlowStarter) promptTemplatesForRequest(req FlowStartRequest) FlowPromptTemplates {
	if req.FlowPromptTemplatesProvided || req.FlowPromptTemplates != (FlowPromptTemplates{}) {
		return req.FlowPromptTemplates
	}
	return s.flowPromptTemplates
}

func (s FlowStarter) PrepareFlow(req FlowStartRequest) (FlowStartResult, error) {
	flow, err := s.createFlow(flowstore.FlowRecord{
		Title:        req.Title,
		Instructions: req.Instructions,
		RepoPath:     req.RepoPath,
		BaseRef:      req.BaseRef,
	}, phaseAgentSettingsForRequest(req))
	if err != nil {
		return FlowStartResult{}, err
	}
	phaseID := ""
	if phase, ok := initialFlowLaunchPhase(flow, req.PlanPhaseID); ok {
		phaseID = phase.PhaseID
	}
	result := FlowStartResult{Flow: flow}

	worktree, err := s.createWorktree(req.RepoPath, req.Title, req.BaseRef)
	if err != nil {
		return result, s.blockStartupFailurePhases(flow, phaseID, "Worktree creation failed: "+err.Error(), err.Error())
	}
	result.Worktree = worktree

	commit := s.resolveCommit(worktree.WorktreePath)
	result.Commit = commit
	startedFlow, err := s.setStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:       flow.FlowID,
		WorktreePath: worktree.WorktreePath,
		Branch:       worktree.Branch,
		BaseRef:      req.BaseRef,
		Commit:       commit,
	})
	if err != nil {
		return result, err
	}
	flow = startedFlow
	result.Flow = flow

	if err := s.runBootstrap(req.RepoPath, worktree); err != nil {
		errText := "Bootstrap hook failed: " + err.Error()
		return result, s.blockStartupFailurePhases(flow, phaseID, errText, errText)
	}

	return result, nil
}

// phaseAgentSettingsForRequest captures the request's agent selection for the
// seeded phases, but only when the triple is valid: Flow creation succeeded for
// every agent selection before phases carried settings, and it must keep doing
// so. An unusable selection stamps nothing, which means "resolve from the
// global setting at launch".
func phaseAgentSettingsForRequest(req FlowStartRequest) flowstore.PhaseAgentSettings {
	settings := flowstore.PhaseAgentSettingsFrom(agent.Settings{
		Command:         req.AgentCommand,
		Model:           req.Model,
		ReasoningEffort: req.ReasoningEffort,
	}).Normalize()
	if settings.Validate() != nil {
		return flowstore.PhaseAgentSettings{}
	}
	return settings
}

func initialFlowLaunchPhase(flow flowstore.FlowRecord, requestedPhaseID string) (flowstore.FlowPhase, bool) {
	if requestedPhaseID != "" {
		if phase, ok := findFlowPhaseByID(flow, requestedPhaseID); ok {
			return phase, true
		}
		return flowstore.FlowPhase{PhaseID: requestedPhaseID, Title: "Plan", Kind: flowstore.KindPlan}, true
	}
	if len(flow.Phases) == 0 {
		return flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}, true
	}
	if phase, _, ok := flowstore.FirstLaunchablePhase(flow); ok {
		return phase, true
	}
	return flowstore.FlowPhase{}, false
}

func findFlowPhaseByID(flow flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	for _, phase := range flow.Phases {
		if phase.PhaseID == phaseID {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

func initialFlowLaunchPrompt(flow flowstore.FlowRecord, phase flowstore.FlowPhase, templates FlowPromptTemplates) string {
	if flowstore.SemanticKind(phase) == flowstore.KindPlan {
		return flowPlanPrompt(flow, phase, templates)
	}
	return flowPhasePrompt(flow, phase, flow.PlanPath, "", templates)
}

func (s FlowStarter) runBootstrap(repoPath string, worktree actions.FlowWorktreeCreateResult) error {
	hook, ok := s.bootstrapHookForRepo(repoPath)
	if !ok {
		return nil
	}
	return s.runBootstrapHook(actions.BootstrapContext{
		RepoPath:     repoPath,
		WorktreePath: worktree.WorktreePath,
		Ref:          worktree.Branch,
		Kind:         actions.WorktreeCreateFlow,
	}, hook)
}

func (s FlowStarter) blockPlanPhase(flowID, phaseID, notes, resultErr string) error {
	if _, err := s.setPhase(flowstore.PhaseUpdate{
		FlowID:  flowID,
		PhaseID: phaseID,
		Status:  flowstore.PhaseBlocked,
		Notes:   notes,
	}); err != nil {
		return fmt.Errorf("%s; mark flow blocked: %v", resultErr, err)
	}
	return fmt.Errorf("%s", resultErr)
}

func (s FlowStarter) blockStartupFailurePhases(flow flowstore.FlowRecord, fallbackPhaseID, notes, resultErr string) error {
	phases := launchablePhases(flow)
	if len(phases) == 0 {
		if fallbackPhaseID == "" {
			return fmt.Errorf("%s", resultErr)
		}
		if phase, ok := findFlowPhaseByID(flow, fallbackPhaseID); ok {
			phases = []flowstore.FlowPhase{phase}
		} else {
			phases = []flowstore.FlowPhase{{PhaseID: fallbackPhaseID}}
		}
	}
	for _, phase := range phases {
		if _, err := s.setPhase(blockedPhaseUpdate(flow.FlowID, phase, notes)); err != nil {
			return fmt.Errorf("%s; mark flow blocked: %v", resultErr, err)
		}
	}
	return fmt.Errorf("%s", resultErr)
}

func launchablePhases(flow flowstore.FlowRecord) []flowstore.FlowPhase {
	ordered := flowstore.OrderedPhases(flow.Phases)
	orderedFlow := flow
	orderedFlow.Phases = ordered
	var phases []flowstore.FlowPhase
	seen := make(map[string]bool)
	for i, phase := range ordered {
		if !flowstore.PhaseLaunchEligible(orderedFlow, i) || seen[phase.PhaseID] {
			continue
		}
		seen[phase.PhaseID] = true
		phases = append(phases, phase)
	}
	return phases
}

func blockedPhaseUpdate(flowID string, phase flowstore.FlowPhase, notes string) flowstore.PhaseUpdate {
	update := flowstore.PhaseUpdate{
		FlowID:  flowID,
		PhaseID: phase.PhaseID,
		Status:  flowstore.PhaseBlocked,
		Notes:   notes,
	}
	if flowstore.SemanticKind(phase) == flowstore.KindPlanReview {
		update.Outcome = flowstore.OutcomeBlocked
	}
	return update
}

func flowStartPromptRecord(flow flowstore.FlowRecord, req FlowStartRequest, worktree actions.FlowWorktreeCreateResult, commit string) flowstore.FlowRecord {
	if flow.Title == "" {
		flow.Title = req.Title
	}
	if flow.Instructions == "" {
		flow.Instructions = req.Instructions
	}
	if flow.RepoPath == "" {
		flow.RepoPath = req.RepoPath
	}
	if flow.WorktreePath == "" {
		flow.WorktreePath = worktree.WorktreePath
	}
	if flow.Branch == "" {
		flow.Branch = worktree.Branch
	}
	if flow.BaseRef == "" {
		flow.BaseRef = req.BaseRef
	}
	if flow.Commit == "" {
		flow.Commit = commit
	}
	return flow
}

func flowPlanPrompt(flow flowstore.FlowRecord, phase flowstore.FlowPhase, templates FlowPromptTemplates) string {
	if strings.TrimSpace(phase.PhaseID) == "" {
		phase = flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}
	}
	if strings.TrimSpace(templates.Plan) != "" {
		prompt := renderFlowPromptTemplate(templates.Plan, flow, phase, flow.PlanPath, "")
		return ensureFlowPhaseDoneInstruction(prompt, templates.Plan)
	}
	var b strings.Builder
	b.WriteString("Use the approach-flow skill for this launch.\n\n")
	b.WriteString(flow.Instructions)
	b.WriteString("\n\nProduce a plan only; do not start coding in this phase.")
	b.WriteString("\nCreate and persist the plan with approach plan save, link it back with approach flow plan set, then report Flow persistence failures explicitly before ending.")
	b.WriteString("\nIf the task references a GitHub issue, link it with approach flow issue set using the issue number and URL; when only #N is given, derive the URL from an unambiguous GitHub origin remote or note the ambiguity instead of guessing.")
	return ensureFlowPhaseDoneInstruction(b.String(), "")
}
