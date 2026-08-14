package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
)

const flowPlanPhaseID = "plan"

// ErrFlowWorktreeUnrecorded reports that EnsureWorktree created a worktree the
// store then refused to record. It is a sentinel because the launch status
// depends on it: the directory exists, so "worktree creation failed" would be
// the one claim that is not true.
var ErrFlowWorktreeUnrecorded = errors.New("worktree not recorded")

// ErrFlowWorktreeUnreserved reports that EnsureWorktree could not take the
// Flow's launch reservation, so it created nothing at all. It is a sentinel
// because it is the one ensure refusal that clears on its own: the usual cause
// is another launch holding the reservation while it provisions this very Flow.
var ErrFlowWorktreeUnreserved = errors.New("launch reservation unavailable")

// FlowStartRequest contains the user operation inputs needed to create and
// provision a parked Flow. Creation-time launches use flowLaunchCreateRequest.
type FlowStartRequest struct {
	RepoPath         string
	Title            string
	Instructions     string
	Bead             flowstore.BeadLink
	BaseRef          string
	AgentCommand     string
	Model            string
	ReasoningEffort  string
	AgentPreferences agent.Preferences
	// AgentPreferencesProvided distinguishes production's full per-provider
	// bundle from callers that provide only the selected triple.
	AgentPreferencesProvided bool
	Headless                 *bool
}

// FlowStartResult is the create-only result returned by Options.CreateFlow.
type FlowStartResult struct {
	Flow     flowstore.FlowRecord
	Worktree actions.FlowWorktreeCreateResult
	Commit   string
}

// flowCreatorOptions groups the persistence and provisioning adapters for
// parked creation and lifecycle worktree recovery.
type flowCreatorOptions struct {
	CreateFlow     func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	CreateWorktree func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	// AttachWorktree gives a branch the Flow already records a worktree of its
	// own. It reports actions.ErrFlowBranchMissing when that branch does not
	// exist, which is the only case CreateWorktree may answer instead.
	AttachWorktree       func(repoPath, branch string) (actions.FlowWorktreeCreateResult, error)
	SetStartMetadata     func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	SetPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	ReserveLaunch        func(flowID string) (flowstore.FlowRecord, func(), error)
	BootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	ResolveCommit        func(string) string
}

// flowCreator owns parked creation plus worktree/bootstrap mechanics. It never
// reserves an agent launch, writes a launch ID, or starts a process.
type flowCreator struct {
	createFlow           func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	createWorktree       func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	attachWorktree       func(repoPath, branch string) (actions.FlowWorktreeCreateResult, error)
	setStartMetadata     func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	setPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	reserveLaunch        func(flowID string) (flowstore.FlowRecord, func(), error)
	bootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	runBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	resolveCommit        func(string) string
}

func newFlowCreator(opts flowCreatorOptions) flowCreator {
	starter := flowCreator{
		createFlow:           opts.CreateFlow,
		createWorktree:       opts.CreateWorktree,
		attachWorktree:       opts.AttachWorktree,
		setStartMetadata:     opts.SetStartMetadata,
		setPhase:             opts.SetPhase,
		reserveLaunch:        opts.ReserveLaunch,
		bootstrapHookForRepo: opts.BootstrapHookForRepo,
		runBootstrapHook:     opts.RunBootstrapHook,
		resolveCommit:        opts.ResolveCommit,
	}
	if starter.createFlow == nil {
		starter.createFlow = func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, fmt.Errorf("flow starter missing CreateFlow")
		}
	}
	if starter.createWorktree == nil {
		starter.createWorktree = actions.CreateFlowWorktree
	}
	if starter.attachWorktree == nil {
		starter.attachWorktree = actions.AttachFlowWorktree
	}
	if starter.setStartMetadata == nil {
		starter.setStartMetadata = func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		}
	}
	if starter.setPhase == nil {
		starter.setPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) { return flowstore.FlowRecord{}, nil }
	}
	if starter.reserveLaunch == nil {
		starter.reserveLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{FlowID: flowID}, func() {}, nil
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
	return starter
}

func flowStartAgentPreferences(req FlowStartRequest) agent.Preferences {
	if req.AgentPreferencesProvided {
		return req.AgentPreferences
	}
	prefs := agent.Preferences{Command: req.AgentCommand}
	switch agent.Normalize(req.AgentCommand) {
	case agent.CommandCodex:
		prefs.CodexModel = req.Model
		prefs.CodexEffort = req.ReasoningEffort
	case agent.CommandClaude:
		prefs.ClaudeModel = req.Model
		prefs.ClaudeEffort = req.ReasoningEffort
	}
	return prefs
}

func resolveFlowStartPhaseAgentSettings(req FlowStartRequest, phase flowstore.FlowPhase) (agent.Settings, error) {
	if !req.AgentPreferencesProvided &&
		agent.Normalize(req.AgentCommand) == "" &&
		agent.NormalizeModel(req.Model) == "" &&
		agent.NormalizeReasoningEffort(req.ReasoningEffort) == "" &&
		phase.AgentSettings().IsZero() {
		return agent.Settings{}, nil
	}
	return flowstore.ResolvePhaseAgentSettings(flowStartAgentPreferences(req), phase.AgentSettings())
}

func validateInitialFlowLaunchPhase(flow flowstore.FlowRecord, phase flowstore.FlowPhase) error {
	if flowstore.SemanticKind(phase) == flowstore.KindPlanReview && flow.PlanID == "" {
		return fmt.Errorf("Plan Review needs a linked plan before launch")
	}
	return nil
}

func (s flowCreator) Create(req FlowStartRequest) (FlowStartResult, error) {
	if agent.NormalizeStored(req.AgentCommand) != agent.Normalize(req.AgentCommand) {
		return FlowStartResult{}, agent.Validate(req.AgentCommand)
	}
	flow, err := s.createFlow(flowstore.FlowRecord{
		Title:        req.Title,
		Instructions: req.Instructions,
		Bead:         req.Bead,
		RepoPath:     req.RepoPath,
		BaseRef:      req.BaseRef,
	}, flowstore.CreateOptions{
		Headless:   req.Headless,
		PhaseAgent: phaseAgentSettingsForRequest(req),
	})
	if err != nil {
		return FlowStartResult{}, err
	}
	phaseID := ""
	if phase, ok := initialFlowLaunchPhase(flow, ""); ok {
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

// EnsureWorktree gives a worktree-less Flow the worktree its launch contract
// already implies, in the same order parked creation uses. It reports failures instead of
// blocking phases: the caller owns that decision, behind its own fence.
//
// The record it returns is the persisted one, complete with phases, because the
// launch lifecycle threads it forward and looks the launching phase up in it.
// That also holds for the bootstrap failure below, which returns a record whose
// worktree is real — the caller must not report that one as a creation failure.
// A recorded worktree path means the directory exists and the store names it.
// It does not mean the bootstrap hook completed: the hook runs after
// SetStartMetadata, so a Flow whose hook failed passes through here and launches
// into the worktree it did get. That is the same contract PrepareFlow has had
// since Flow creation gained a hook, and the TUI guide documents it for the
// second `g`. Making path existence imply a finished bootstrap needs a
// provisioning marker in the store, which is a change to Flow creation too.
func (s flowCreator) EnsureWorktree(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
	if strings.TrimSpace(record.WorktreePath) != "" {
		return record, nil
	}
	// Guarded here as well as at the launch call site: `git -C ""` would run in
	// Approach's own working directory and put the worktree wherever that is.
	if strings.TrimSpace(record.RepoPath) == "" {
		return record, fmt.Errorf("flow has no repository of its own")
	}
	// Everything below creates a branch and a directory and then persists them,
	// and the launch reservation is not taken until the spawn, several hops
	// later. Without a fence here two readers of the same worktree-less Flow —
	// two Approach processes, or one whose reading attempt was released mid
	// ensure — each allocate a pair and race the metadata write, leaving the
	// loser's agent in a worktree no record names. The reservation is the
	// existing per-Flow lock and it answers with the authoritative record, so it
	// closes the window and supplies the re-read in one step.
	fresh, release, err := s.reserveLaunch(record.FlowID)
	if err != nil {
		// Wrapped, not returned bare: the reservation is held across the
		// bootstrap hook, whose budget is 120 s by default against the store's
		// 5 s lock timeout, so a Flow another process is provisioning right now
		// is the ordinary reason this fails. The sentinel is what stops the
		// caller from reading that as a permanent refusal — nothing was created
		// here, so there is nothing to clean up and every reason to come back.
		return record, fmt.Errorf("%w: %w", ErrFlowWorktreeUnreserved, err)
	}
	defer releaseFlowLaunchReservation(release)
	// The store is authoritative under the lock: whoever went first has already
	// recorded a worktree, and this launch belongs in that one rather than in a
	// second pair of its own.
	if strings.TrimSpace(fresh.WorktreePath) != "" {
		return fresh, nil
	}
	worktree, err := s.ensureWorktreeFor(record)
	if err != nil {
		return record, err
	}
	commit := s.resolveCommit(worktree.WorktreePath)
	// SetStartMetadata is additive and returns the fresh record, so it is safe on
	// a Flow that already exists. Branch is written back rather than preserved
	// because ensureWorktreeFor only allocates a new name when the recorded one
	// resolves to nothing.
	started, err := s.setStartMetadata(flowstore.StartMetadataUpdate{
		FlowID:       record.FlowID,
		WorktreePath: worktree.WorktreePath,
		Branch:       worktree.Branch,
		BaseRef:      record.BaseRef,
		Commit:       commit,
	})
	if err != nil {
		// The worktree exists and nothing records it, and the retry allocates a
		// fresh name rather than adopting this one, so the path is named here:
		// an unattributable directory is worse than a wordy status. The sentinel
		// is what lets the launcher pick a headline that does not claim creation
		// failed, which is the one thing that did not.
		return record, fmt.Errorf("%w: %s: %w", ErrFlowWorktreeUnrecorded, worktree.WorktreePath, err)
	}
	if err := s.runBootstrap(record.RepoPath, worktree); err != nil {
		// The worktree and its metadata survive a hook failure — the same
		// trade-off PrepareFlow makes — so a retry takes the passthrough above.
		return started, fmt.Errorf("bootstrap hook failed: %w", err)
	}
	return started, nil
}

// ensureWorktreeFor honors the branch a Flow already records instead of
// replacing it. A recorded branch is a promise the rest of the app keeps —
// prompts render it as the push target and `flow pr set --head` validates
// against it — so allocating a second flow/<slug> beside it would leave the
// record naming a branch no agent ever touches. Only a branch that resolves to
// nothing is replaced, which is the case the store cannot tell apart on its own.
func (s flowCreator) ensureWorktreeFor(record flowstore.FlowRecord) (actions.FlowWorktreeCreateResult, error) {
	if branch := strings.TrimSpace(record.Branch); branch != "" {
		worktree, err := s.attachWorktree(record.RepoPath, branch)
		if err == nil {
			return worktree, nil
		}
		if !errors.Is(err, actions.ErrFlowBranchMissing) {
			return actions.FlowWorktreeCreateResult{}, err
		}
	}
	return s.createWorktree(record.RepoPath, record.Title, record.BaseRef)
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
	return flowPhaseByID(flow, phaseID)
}

func initialFlowLaunchPrompt(flow flowstore.FlowRecord, phase flowstore.FlowPhase, templates FlowPromptTemplates) string {
	if flowstore.SemanticKind(phase) == flowstore.KindPlan {
		return flowPlanPrompt(flow, phase, templates)
	}
	return flowPhasePrompt(flow, phase, flow.PlanPath, "", templates)
}

func (s flowCreator) runBootstrap(repoPath string, worktree actions.FlowWorktreeCreateResult) error {
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

func (s flowCreator) blockStartupFailurePhases(flow flowstore.FlowRecord, fallbackPhaseID, notes, resultErr string) error {
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
