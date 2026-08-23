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
	// AfterFlowPersisted admits the receipt-less Flow before any worktree side
	// effect. The Flow retains its progression claim marker on failure so a
	// retry can reconcile an externally uncertain admission outcome.
	AfterFlowPersisted func() error
}

// FlowStartResult is the create-only result returned by Options.CreateFlow.
type FlowStartResult struct {
	Flow     flowstore.FlowRecord
	Worktree actions.FlowWorktreeCreateResult
	Commit   string
}

// FlowPreparationOptions wires the UI-independent Flow preparation lifecycle
// for callers such as the CLI. Preparation creates the record, provisions its
// worktree, records start metadata, runs any bootstrap hook, and stamps the
// preparation receipt without launching an agent.
type FlowPreparationOptions struct {
	Store                *flowstore.Store
	Preset               *flowstore.Preset
	BootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
}

// PrepareFlow runs the same persisted preparation lifecycle used by TUI Flow
// creation, without reserving or starting an agent launch.
func PrepareFlow(req FlowStartRequest, opts FlowPreparationOptions) (FlowStartResult, error) {
	if opts.Store == nil {
		return FlowStartResult{}, fmt.Errorf("flow preparation requires a store")
	}
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(record flowstore.FlowRecord, createOpts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			createOpts.Preset = opts.Preset
			return opts.Store.CreatePreparation(record, createOpts)
		},
		CreateWorktree: actions.CreateFlowWorktree,
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return opts.Store.SetStartMetadata(update)
		},
		SetPhase:             opts.Store.SetPhase,
		ReserveLaunch:        opts.Store.ReserveAgentLaunch,
		ReadFlow:             opts.Store.Read,
		BootstrapHookForRepo: opts.BootstrapHookForRepo,
		RunBootstrapHook:     opts.RunBootstrapHook,
		ResolveCommit:        actions.ResolveWorktreeCommit,
	})
	return creator.Create(req)
}

// flowCreatorOptions groups the persistence and provisioning adapters for
// parked creation and lifecycle worktree recovery.
type flowCreatorOptions struct {
	CreateFlow        func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	CreatePreparation func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error)
	CreateWorktree    func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	// AttachWorktree gives a branch the Flow already records a worktree of its
	// own. It reports actions.ErrFlowBranchMissing when that branch does not
	// exist, which is the only case CreateWorktree may answer instead.
	AttachWorktree       func(repoPath, branch string) (actions.FlowWorktreeCreateResult, error)
	SetStartMetadata     func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	SetPhase             func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	ReserveLaunch        func(flowID string) (flowstore.FlowRecord, func(), error)
	ReadFlow             func(flowID string) (flowstore.FlowRecord, error)
	BootstrapHookForRepo func(string) (actions.BootstrapHook, bool)
	RunBootstrapHook     func(actions.BootstrapContext, actions.BootstrapHook) error
	ResolveCommit        func(string) string
}

// flowCreator owns parked creation plus worktree/bootstrap mechanics. It never
// reserves an agent launch, writes a launch ID, or starts a process.
type flowCreator struct {
	createFlow                       func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	createPreparation                func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error)
	createWorktree                   func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	attachWorktree                   func(repoPath, branch string) (actions.FlowWorktreeCreateResult, error)
	setStartMetadata                 func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	setPhase                         func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	reserveLaunch                    func(flowID string) (flowstore.FlowRecord, func(), error)
	readFlow                         func(flowID string) (flowstore.FlowRecord, error)
	bootstrapHookForRepo             func(string) (actions.BootstrapHook, bool)
	runBootstrapHook                 func(actions.BootstrapContext, actions.BootstrapHook) error
	resolveCommit                    func(string) string
	preparationReservationConfigured bool
}

func newFlowCreator(opts flowCreatorOptions) flowCreator {
	starter := flowCreator{
		createFlow:                       opts.CreateFlow,
		createPreparation:                opts.CreatePreparation,
		createWorktree:                   opts.CreateWorktree,
		attachWorktree:                   opts.AttachWorktree,
		setStartMetadata:                 opts.SetStartMetadata,
		setPhase:                         opts.SetPhase,
		reserveLaunch:                    opts.ReserveLaunch,
		readFlow:                         opts.ReadFlow,
		bootstrapHookForRepo:             opts.BootstrapHookForRepo,
		runBootstrapHook:                 opts.RunBootstrapHook,
		resolveCommit:                    opts.ResolveCommit,
		preparationReservationConfigured: opts.ReserveLaunch != nil,
	}
	if starter.createFlow == nil {
		starter.createFlow = func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, fmt.Errorf("flow starter missing CreateFlow")
		}
	}
	if starter.createPreparation == nil {
		// Compatibility for direct flowCreator callers that predate preparation
		// receipts. Production Model wiring always supplies CreatePreparation.
		starter.createPreparation = func(record flowstore.FlowRecord, createOpts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			created, err := starter.createFlow(record, createOpts)
			if err != nil {
				return flowstore.FlowRecord{}, nil, err
			}
			return created, callbackPreparationFinalizer{flow: created}, nil
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
	if starter.readFlow == nil {
		starter.readFlow = func(string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, fmt.Errorf("flow starter missing ReadFlow")
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
	case agent.CommandCursor:
		prefs.CursorModel = req.Model
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
	if s.createPreparation == nil {
		return FlowStartResult{}, fmt.Errorf("flow starter missing CreatePreparation")
	}
	if !s.preparationReservationConfigured {
		return FlowStartResult{}, fmt.Errorf("flow starter missing authoritative ReserveLaunch for preparation")
	}
	flow, finalizer, err := s.createPreparation(flowstore.FlowRecord{
		Title:            req.Title,
		Instructions:     req.Instructions,
		Bead:             req.Bead,
		RepoPath:         req.RepoPath,
		BaseRef:          req.BaseRef,
		ProgressionClaim: req.AfterFlowPersisted != nil,
	}, flowstore.CreateOptions{
		Headless:   req.Headless,
		PhaseAgent: phaseAgentSettingsForRequest(req),
	})
	if err != nil {
		return FlowStartResult{}, err
	}
	reserved, release, err := s.reserveLaunch(flow.FlowID)
	if err != nil {
		notes := "reserve flow preparation identity: " + err.Error()
		compensated, compensationErr := compensateParkedPreparation(finalizer, false, notes)
		result := FlowStartResult{Flow: flow}
		if compensationErr == nil {
			result.Flow = compensated
			return result, fmt.Errorf("%s", notes)
		}
		return result, fmt.Errorf("%s; compensate preparation: %w", notes, compensationErr)
	}
	releaseIdentity := func() {
		if release != nil {
			release()
			release = nil
		}
	}
	defer releaseIdentity()
	if reserved.FlowID != "" && reserved.FlowID != flow.FlowID {
		return FlowStartResult{Flow: flow}, errors.Join(ErrFlowWorktreeUnreserved,
			fmt.Errorf("preparation reserved flow %q instead of %q", reserved.FlowID, flow.FlowID))
	}
	if !flowstore.SamePreparationIdentity(reserved, flow) {
		return FlowStartResult{Flow: flow}, errors.Join(flowstore.ErrPreparationStale,
			fmt.Errorf("flow %q generation changed before preparation admission", flow.FlowID))
	}
	if flowstore.FlowClosed(reserved) || flowstore.DeriveStatus(reserved) != flowstore.StatusPending {
		return FlowStartResult{Flow: reserved}, errors.Join(flowstore.ErrPreparationIncomplete,
			fmt.Errorf("flow %q state changed before preparation admission", flow.FlowID))
	}
	if req.AfterFlowPersisted != nil {
		if err := req.AfterFlowPersisted(); err != nil {
			return FlowStartResult{Flow: flow}, err
		}
	}
	phaseID := ""
	if phase, ok := initialFlowLaunchPhase(flow, ""); ok {
		phaseID = phase.PhaseID
	}
	result := FlowStartResult{Flow: flow}

	worktree, err := s.createWorktree(req.RepoPath, req.Title, req.BaseRef)
	if err != nil {
		// The held reservation only orders this against Close, not against a
		// concurrent phase mutation, so the snapshot in flow can no longer be
		// trusted by the time worktree creation itself fails. Re-read the
		// authoritative record before selecting phases to block; an
		// unreadable record or a generation mismatch means guessing risks
		// blocking a phase that already moved on, so report the failure
		// without blocking anything instead.
		fresh, freshErr := s.verifiedFreshFlow(flow)
		if freshErr != nil {
			return result, fmt.Errorf("Worktree creation failed: %s; %w", err.Error(), freshErr)
		}
		compensated, compensationErr := s.blockStartupFailurePhasesReserved(fresh, phaseID, "Worktree creation failed: "+err.Error(), err.Error())
		result.Flow = compensated
		return result, compensationErr
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
		worktreeRecord := flow
		worktreeRecord.WorktreePath = worktree.WorktreePath
		worktreeRecord.Branch = worktree.Branch
		notes := "record start metadata: " + err.Error()
		if note := createdFlowWorktreeNote(worktreeRecord); note != "" {
			notes += "; " + note
		}
		fresh, freshErr := s.verifiedFreshFlow(flow)
		if freshErr != nil {
			return result, fmt.Errorf("%s; %w", notes, freshErr)
		}
		if !parkedStartMetadataLanded(fresh, worktree, req.BaseRef, commit) {
			compensated, compensationErr := compensateParkedPreparation(finalizer, true, notes)
			if compensationErr == nil {
				result.Flow = compensated
				return result, fmt.Errorf("%s", notes)
			}
			return result, fmt.Errorf("%s; %w", notes, compensationErr)
		}
		startedFlow = fresh
	}
	flow = startedFlow
	result.Flow = flow

	var bootstrapErr error
	finalized, err := finalizer.Finalize(func() error {
		bootstrapErr = s.runBootstrap(req.RepoPath, worktree)
		return bootstrapErr
	})
	if err != nil {
		if flowstore.IsPreparationUnknown(err) || flowstore.IsPreparationStale(err) {
			return result, err
		}
		errText := "Preparation receipt persistence failed: " + err.Error()
		if bootstrapErr != nil {
			errText = "Bootstrap hook failed: " + bootstrapErr.Error()
		}
		if finalized.FlowID == flow.FlowID {
			flow = finalized
			result.Flow = finalized
		} else {
			// Finalize returns an empty record ahead of its transactional read
			// when bootstrap itself fails, so the pre-bootstrap snapshot in
			// flow is otherwise all that is left to compensate. Re-read the
			// authoritative record before selecting phases to block instead.
			fresh, freshErr := s.verifiedFreshFlow(flow)
			if freshErr != nil {
				return result, fmt.Errorf("%s; %w", errText, freshErr)
			}
			flow = fresh
			result.Flow = fresh
		}
		compensated, compensationErr := s.blockStartupFailurePhasesReserved(flow, phaseID, errText, errText)
		result.Flow = compensated
		return result, compensationErr
	}
	if finalized.PreparedAt == nil {
		errText := "Preparation receipt persistence failed: finalizer returned no preparation receipt"
		compensated, compensationErr := s.blockStartupFailurePhasesReserved(flow, phaseID, errText, errText)
		result.Flow = compensated
		return result, compensationErr
	}
	flow = finalized
	result.Flow = finalized

	return result, nil
}

type callbackPreparationFinalizer struct {
	flow flowstore.FlowRecord
}

func (f callbackPreparationFinalizer) Finalize(callback func() error) (flowstore.FlowRecord, error) {
	if callback != nil {
		if err := callback(); err != nil {
			return f.flow, err
		}
	}
	return f.flow, nil
}

func (f callbackPreparationFinalizer) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	return f.Compensate(notes)
}

func (f callbackPreparationFinalizer) Compensate(notes string) (flowstore.FlowRecord, error) {
	flow := f.flow
	for _, root := range launchablePhases(flow) {
		for i := range flow.Phases {
			if flow.Phases[i].PhaseID != root.PhaseID {
				continue
			}
			update := blockedPhaseUpdate(flow.FlowID, root, notes)
			flow.Phases[i].Status = update.Status
			flow.Phases[i].Notes = update.Notes
			flow.Phases[i].Outcome = update.Outcome
		}
	}
	return flow, nil
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
// into the worktree it did get. That is the same contract prepared creation has
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
		// trade-off flowCreator.Create makes — so a retry takes the passthrough above.
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

func initialFlowLaunchPrompt(flow flowstore.FlowRecord, phase flowstore.FlowPhase, templates FlowPromptTemplates, binary string) string {
	if flowstore.SemanticKind(phase) == flowstore.KindPlan {
		return flowPlanPrompt(flow, phase, templates, binary)
	}
	return flowPhasePrompt(flow, phase, flow.PlanPath, "", templates, binary)
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

// verifiedFreshFlow re-reads the authoritative Flow record and confirms its
// preparation generation still matches the given snapshot. The launch/close
// reservation Create holds across startup failures only orders this against
// Close, not against a concurrent phase mutation, so a snapshot taken earlier
// in Create can no longer be trusted by the time a startup step fails: an
// unreadable record or a changed generation means the caller must not guess
// which phase is still safe to block.
func compensateParkedPreparation(finalizer flowstore.PreparationFinalizer, underReservation bool, notes string) (flowstore.FlowRecord, error) {
	var (
		record flowstore.FlowRecord
		err    error
	)
	for attempt := 0; attempt < readyPreparationCompensationAttemptLimit; attempt++ {
		if underReservation {
			record, err = finalizer.CompensateUnderReservation(notes)
		} else {
			record, err = finalizer.Compensate(notes)
		}
		if err == nil || !compensationRetryable(err) {
			return record, err
		}
	}
	return record, err
}

func parkedStartMetadataLanded(record flowstore.FlowRecord, worktree actions.FlowWorktreeCreateResult, baseRef, commit string) bool {
	if strings.TrimSpace(record.WorktreePath) != strings.TrimSpace(worktree.WorktreePath) ||
		strings.TrimSpace(record.Branch) != strings.TrimSpace(worktree.Branch) {
		return false
	}
	if baseRef != "" && strings.TrimSpace(record.BaseRef) != strings.TrimSpace(baseRef) {
		return false
	}
	if commit != "" && strings.TrimSpace(record.Commit) != strings.TrimSpace(commit) {
		return false
	}
	return true
}

func (s flowCreator) verifiedFreshFlow(flow flowstore.FlowRecord) (flowstore.FlowRecord, error) {
	fresh, readErr := s.readFlow(flow.FlowID)
	if readErr != nil {
		return flowstore.FlowRecord{}, fmt.Errorf("could not confirm current Flow state before compensating: %w", readErr)
	}
	if !flowstore.SamePreparationIdentity(fresh, flow) {
		return flowstore.FlowRecord{}, fmt.Errorf("flow %q changed before compensation could run", flow.FlowID)
	}
	return fresh, nil
}

// blockStartupFailurePhasesReserved compensates while Create still owns the
// preparation generation's launch/close reservation. Callers must not release
// and reacquire by Flow ID around this mutation: a same-ID replacement could
// otherwise receive the original generation's blocked phases.
func (s flowCreator) blockStartupFailurePhasesReserved(flow flowstore.FlowRecord, fallbackPhaseID, notes, resultErr string) (flowstore.FlowRecord, error) {
	phases := launchablePhases(flow)
	if len(phases) == 0 {
		if len(flow.Phases) > 0 {
			return flow, fmt.Errorf("%s", resultErr)
		}
		if fallbackPhaseID == "" {
			return flow, fmt.Errorf("%s", resultErr)
		}
		if phase, ok := findFlowPhaseByID(flow, fallbackPhaseID); ok {
			phases = []flowstore.FlowPhase{phase}
		} else {
			phases = []flowstore.FlowPhase{{PhaseID: fallbackPhaseID}}
		}
	}
	for _, phase := range phases {
		updated, err := s.setPhase(blockedPhaseUpdate(flow.FlowID, phase, notes))
		if err != nil {
			return flow, fmt.Errorf("%s; mark flow blocked: %v", resultErr, err)
		}
		if updated.FlowID == flow.FlowID {
			flow = updated
		}
	}
	return flow, fmt.Errorf("%s", resultErr)
}

func launchablePhases(flow flowstore.FlowRecord) []flowstore.FlowPhase {
	ordered := flowstore.OrderedPhases(flow.Phases)
	orderedFlow := flow
	orderedFlow.Phases = ordered
	var phases []flowstore.FlowPhase
	seen := make(map[string]bool)
	for i, phase := range ordered {
		if !flowstore.PhaseGraphLaunchEligible(orderedFlow, i) || seen[phase.PhaseID] {
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

func flowPlanPrompt(flow flowstore.FlowRecord, phase flowstore.FlowPhase, templates FlowPromptTemplates, binary string) string {
	if strings.TrimSpace(phase.PhaseID) == "" {
		phase = flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}
	}
	bin := flowPromptBinary(binary)
	if strings.TrimSpace(templates.Plan) != "" {
		prompt := renderFlowPromptTemplate(templates.Plan, flow, phase, flow.PlanPath, "", bin)
		return ensureFlowPhaseWorkflowSuffix(prompt, templates.Plan)
	}
	var b strings.Builder
	b.WriteString("Use the approach-flow skill for this launch.\n\n")
	writeUntrustedFlowRecord(&b, flow.Instructions)
	b.WriteString("\nProduce a plan only; do not start coding in this phase.")
	b.WriteString("\nCreate and persist the plan with " + bin + " plan save, link it back with " + bin + " flow plan set, then report Flow persistence failures explicitly before ending.")
	b.WriteString("\nIf the task references a GitHub issue, link it with " + bin + " flow issue set using the issue number and URL; when only #N is given, derive the URL from an unambiguous GitHub origin remote or note the ambiguity instead of guessing.")
	return ensureFlowPhaseWorkflowSuffix(b.String(), "")
}
