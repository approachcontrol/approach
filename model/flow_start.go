package model

import (
	"fmt"
	"strings"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
)

const flowPlanPhaseID = "plan"

// FlowStartRequest contains the user operation inputs needed to create a Flow
// and prepare the initial plan-phase agent launch.
type FlowStartRequest struct {
	RepoPath         string
	Title            string
	Instructions     string
	BaseRef          string
	AgentCommand     string
	SessionStateRoot string
	PlanPhaseID      string
	PlanPhaseTitle   string
	PlanPhaseStatus  string
}

// FlowStartResult is the launch-ready result of starting a new Flow plan phase.
type FlowStartResult struct {
	Flow          flowstore.FlowRecord
	Worktree      actions.FlowWorktreeCreateResult
	Commit        string
	LaunchID      string
	LaunchContext actions.AgentLaunchContext
}

// FlowStarterOptions groups the deeper orchestration adapters for starting a
// Flow. Tests can replace these directly without widening Model.Options.
type FlowStarterOptions struct {
	CreateFlow           func(flowstore.FlowRecord) (flowstore.FlowRecord, error)
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
	createFlow           func(flowstore.FlowRecord) (flowstore.FlowRecord, error)
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
		starter.createFlow = func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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
	phaseID := req.PlanPhaseID
	if phaseID == "" {
		phaseID = flowPlanPhaseID
	}

	flow, err := s.createFlow(flowstore.FlowRecord{
		Title:        req.Title,
		Instructions: req.Instructions,
		RepoPath:     req.RepoPath,
		BaseRef:      req.BaseRef,
	})
	if err != nil {
		return FlowStartResult{}, err
	}

	worktree, err := s.createWorktree(req.RepoPath, req.Title, req.BaseRef)
	if err != nil {
		return FlowStartResult{}, s.blockPlanPhase(flow.FlowID, phaseID, "Worktree creation failed: "+err.Error(), err.Error())
	}

	commit := s.resolveCommit(worktree.WorktreePath)
	startedFlow, err := s.setStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:       flow.FlowID,
		WorktreePath: worktree.WorktreePath,
		Branch:       worktree.Branch,
		BaseRef:      req.BaseRef,
		Commit:       commit,
	})
	if err != nil {
		return FlowStartResult{}, err
	}
	flow = startedFlow

	if err := s.runBootstrap(req.RepoPath, worktree); err != nil {
		errText := "Bootstrap hook failed: " + err.Error()
		return FlowStartResult{}, s.blockPlanPhase(flow.FlowID, phaseID, errText, errText)
	}

	launchID := s.newLaunchID()
	launchedFlow, err := s.addPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:   flow.FlowID,
		PhaseID:  phaseID,
		LaunchID: launchID,
	})
	if err != nil {
		return FlowStartResult{}, err
	}
	flow = launchedFlow

	phaseTitle := req.PlanPhaseTitle
	if phaseTitle == "" {
		phaseTitle = "Plan"
	}
	phaseStatus := req.PlanPhaseStatus
	if phaseStatus == "" {
		phaseStatus = flowstore.PhaseRunning
	}
	ctx := actions.AgentLaunchContext{
		Command:          req.AgentCommand,
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
		InitialPrompt:    flowPlanPrompt(flowStartPromptRecord(flow, req, worktree, commit), s.flowPromptTemplates),
	}
	return FlowStartResult{
		Flow:          flow,
		Worktree:      worktree,
		Commit:        commit,
		LaunchID:      launchID,
		LaunchContext: ctx,
	}, nil
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

func flowPlanPrompt(flow flowstore.FlowRecord, templates FlowPromptTemplates) string {
	if strings.TrimSpace(templates.Plan) != "" {
		return renderFlowPromptTemplate(templates.Plan, flow, flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan"}, flow.PlanPath, "")
	}
	var b strings.Builder
	b.WriteString("Use the wtui-flow skill for this launch.\n\n")
	b.WriteString(flow.Instructions)
	b.WriteString("\n\nProduce a plan only; do not start coding in this phase.")
	b.WriteString("\nCreate and persist the plan with wtui plan save, link it back with wtui flow plan set, then report Flow persistence failures explicitly before ending.")
	return b.String()
}
