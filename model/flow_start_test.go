package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
)

type preparedFlowFinalizerForTest struct {
	latest *flowstore.FlowRecord
}

type preparationFinalizerFuncForTest func(func() error) (flowstore.FlowRecord, error)

func (f preparationFinalizerFuncForTest) Finalize(callback func() error) (flowstore.FlowRecord, error) {
	return f(callback)
}

func (f preparationFinalizerFuncForTest) Compensate(string) (flowstore.FlowRecord, error) {
	return flowstore.FlowRecord{}, nil
}

func (f preparationFinalizerFuncForTest) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	return f.Compensate(notes)
}

func (f preparedFlowFinalizerForTest) Finalize(callback func() error) (flowstore.FlowRecord, error) {
	if callback != nil {
		if err := callback(); err != nil {
			return flowstore.FlowRecord{}, err
		}
	}
	prepared := *f.latest
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	prepared.PreparedAt = &stamp
	*f.latest = prepared
	return prepared, nil
}

func (f preparedFlowFinalizerForTest) Compensate(string) (flowstore.FlowRecord, error) {
	return *f.latest, nil
}

func (f preparedFlowFinalizerForTest) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	return f.Compensate(notes)
}

type compensatingPreparationFinalizerForTest struct {
	record *flowstore.FlowRecord
	notes  *string
}

func (f compensatingPreparationFinalizerForTest) Finalize(func() error) (flowstore.FlowRecord, error) {
	return *f.record, errors.New("test finalizer must not finalize")
}

func (f compensatingPreparationFinalizerForTest) Compensate(notes string) (flowstore.FlowRecord, error) {
	return f.CompensateUnderReservation(notes)
}

func (f compensatingPreparationFinalizerForTest) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	if f.notes != nil {
		*f.notes = notes
	}
	flow := *f.record
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
	*f.record = flow
	return flow, nil
}

type recordingPreparationFinalizerForTest struct {
	record *flowstore.FlowRecord
	calls  *[]string
}

func (f recordingPreparationFinalizerForTest) Finalize(callback func() error) (flowstore.FlowRecord, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "finalize")
	}
	if callback != nil {
		if err := callback(); err != nil {
			return *f.record, err
		}
	}
	prepared := *f.record
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	prepared.PreparedAt = &stamp
	*f.record = prepared
	return prepared, nil
}

func (f recordingPreparationFinalizerForTest) Compensate(notes string) (flowstore.FlowRecord, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "compensate")
	}
	return *f.record, nil
}

func (f recordingPreparationFinalizerForTest) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	return f.Compensate(notes)
}

type retryableThenCompensatingFinalizerForTest struct {
	record    *flowstore.FlowRecord
	notes     *string
	remaining *int
}

func (f retryableThenCompensatingFinalizerForTest) Finalize(func() error) (flowstore.FlowRecord, error) {
	return *f.record, errors.New("test finalizer must not finalize")
}

func (f retryableThenCompensatingFinalizerForTest) Compensate(notes string) (flowstore.FlowRecord, error) {
	return f.CompensateUnderReservation(notes)
}

func (f retryableThenCompensatingFinalizerForTest) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	if f.remaining != nil && *f.remaining > 0 {
		*f.remaining--
		return *f.record, errors.Join(flowstore.ErrPreparationReservation, errors.New("timed out waiting for Flow launch/close lock"))
	}
	return compensatingPreparationFinalizerForTest{record: f.record, notes: f.notes}.CompensateUnderReservation(notes)
}

func newFlowCreatorForTest(opts flowCreatorOptions) flowCreator {
	var latest flowstore.FlowRecord
	if opts.CreatePreparation == nil && opts.CreateFlow != nil {
		createFlow := opts.CreateFlow
		setStartMetadata := opts.SetStartMetadata
		opts.CreatePreparation = func(record flowstore.FlowRecord, createOpts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			created, err := createFlow(record, createOpts)
			if err != nil {
				return flowstore.FlowRecord{}, nil, err
			}
			latest = created
			return created, preparedFlowFinalizerForTest{latest: &latest}, nil
		}
		if setStartMetadata != nil {
			opts.SetStartMetadata = func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
				started, err := setStartMetadata(update)
				if err == nil {
					latest = started
				}
				return started, err
			}
		}
	}
	if opts.ReserveLaunch == nil && opts.CreatePreparation != nil {
		opts.ReserveLaunch = func(flowID string) (flowstore.FlowRecord, func(), error) {
			if latest.FlowID == "" {
				return flowstore.FlowRecord{FlowID: flowID}, func() {}, nil
			}
			return latest, func() {}, nil
		}
	}
	if opts.ReadFlow == nil && opts.CreatePreparation != nil {
		opts.ReadFlow = func(flowID string) (flowstore.FlowRecord, error) {
			if latest.FlowID == "" {
				return flowstore.FlowRecord{FlowID: flowID}, nil
			}
			return latest, nil
		}
	}
	return newFlowCreator(opts)
}

func TestFlowCreatorCreateCreatesLaunchableFlowWithoutLaunchID(t *testing.T) {
	var calls []string
	var created flowstore.FlowRecord
	var startUpdate flowstore.StartMetadataUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			created = record
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-parked", Branch: "flow/parked"}, nil
		},
		ResolveCommit: func(path string) string {
			calls = append(calls, "resolve-commit")
			return "abc123"
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			startUpdate = update
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Title:        "Parked Flow",
				Instructions: "Plan later",
				RepoPath:     "/dev/alpha",
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				BaseRef:      update.BaseRef,
				Commit:       update.Commit,
				Phases:       []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}},
			}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath:     "/dev/alpha",
		Title:        "Parked Flow",
		Instructions: "Plan later",
		BaseRef:      "main",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,resolve-commit,set-start" {
		t.Fatalf("call order = %#v", calls)
	}
	if created.Title != "Parked Flow" || created.Instructions != "Plan later" || created.RepoPath != "/dev/alpha" || created.BaseRef != "main" {
		t.Fatalf("created record = %#v", created)
	}
	if startUpdate.FlowID != "flow-1" ||
		startUpdate.WorktreePath != "/dev/alpha-worktrees/flow-parked" ||
		startUpdate.Branch != "flow/parked" ||
		startUpdate.BaseRef != "main" ||
		startUpdate.Commit != "abc123" {
		t.Fatalf("start update = %#v", startUpdate)
	}
	if result.Flow.FlowID != "flow-1" ||
		result.Flow.WorktreePath != "/dev/alpha-worktrees/flow-parked" ||
		result.Flow.Branch != "flow/parked" ||
		result.Flow.Commit != "abc123" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Flow.Phases) != 1 ||
		result.Flow.Phases[0].PhaseID != "plan" ||
		result.Flow.Phases[0].Status != flowstore.PhaseReady ||
		len(result.Flow.Phases[0].LaunchIDs) != 0 {
		t.Fatalf("plan phase = %#v", result.Flow.Phases)
	}
}

func TestFlowCreatorCreateRequiresAuthoritativeReservationBeforeCreate(t *testing.T) {
	creates := 0
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			creates++
			return flowstore.FlowRecord{}, nil, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Reservation required"})
	if err == nil || !strings.Contains(err.Error(), "missing authoritative ReserveLaunch") {
		t.Fatalf("Create() error = %v, want authoritative reservation requirement", err)
	}
	if creates != 0 {
		t.Fatalf("Create created %d receipt-less Flows before rejecting its configuration", creates)
	}
}

func TestFlowCreatorPreparationFailureDoesNotBlockFreshRunningPhase(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Concurrent launch",
		Instructions: "Preserve the live phase.",
		RepoPath:     "/dev/alpha",
		Phases: []flowstore.FlowPhase{{
			PhaseID: "plan",
			Title:   "Plan",
			Kind:    flowstore.KindPlan,
			Status:  flowstore.PhaseReady,
		}},
	}
	running := created
	running.Phases = append([]flowstore.FlowPhase(nil), created.Phases...)
	running.Phases[0].Status = flowstore.PhaseRunning
	released := false
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, preparationFinalizerFuncForTest(func(callback func() error) (flowstore.FlowRecord, error) {
				if callback != nil {
					if err := callback(); err != nil {
						return flowstore.FlowRecord{}, err
					}
				}
				return created, errors.Join(flowstore.ErrPreparationIncomplete, errors.New("flow is no longer pending"))
			}), nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/flow-concurrent-launch",
				Branch:       "flow/concurrent-launch",
			}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			started := created
			started.WorktreePath = update.WorktreePath
			started.Branch = update.Branch
			return started, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			if flowID != created.FlowID {
				t.Fatalf("ReserveLaunch(%q), want %q", flowID, created.FlowID)
			}
			return running, func() { released = true }, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("SetPhase(%#v) must not overwrite a freshly running phase", update)
			return flowstore.FlowRecord{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: "Concurrent launch", Instructions: "Preserve the live phase.",
	})
	if !flowstore.IsPreparationIncomplete(err) && (err == nil || !strings.Contains(err.Error(), "flow preparation is incomplete")) {
		t.Fatalf("Create() error = %v, want confirmed incomplete preparation", err)
	}
	if !released {
		t.Fatal("Create() did not release the compensation launch reservation")
	}
	if len(result.Flow.Phases) != 1 || result.Flow.Phases[0].Status != flowstore.PhaseRunning {
		t.Fatalf("Create() result = %#v, want authoritative running phase", result.Flow)
	}
}

func TestFlowCreatorStalePreparationDoesNotCompensateReplacementFlow(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Original", Instructions: "Preserve another generation.", RepoPath: "/dev/alpha",
		Phases: []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}},
	}
	replacement := created
	replacement.Title = "Replacement"
	reserves := 0
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, preparationFinalizerFuncForTest(func(func() error) (flowstore.FlowRecord, error) {
				return replacement, errors.Join(flowstore.ErrPreparationStale, errors.New("generation changed"))
			}), nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-original", Branch: "flow/original"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			started := created
			started.WorktreePath = update.WorktreePath
			started.Branch = update.Branch
			return started, nil
		},
		ReserveLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			reserves++
			if reserves > 1 {
				t.Fatal("stale preparation attempted compensation")
			}
			return created, func() {}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title, Instructions: created.Instructions,
	})
	if !flowstore.IsPreparationStale(err) {
		t.Fatalf("Create() error = %v, want stale preparation", err)
	}
	if result.Flow.Title != created.Title {
		t.Fatalf("Create() result = %#v, want original generation", result.Flow)
	}
}

func TestFlowCreatorPreparationAdmissionFailureRetainsFencedRecoveryFlow(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Claim child", RepoPath: "/dev/alpha", ProgressionClaim: true,
		PreparationGeneration: "generation-1",
	}
	claimErr := errors.New("already claimed by another actor")
	var order []string
	marked := false
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			order = append(order, "create")
			marked = record.ProgressionClaim
			return created, preparationFinalizerFuncForTest(func(func() error) (flowstore.FlowRecord, error) {
				t.Fatal("claim failure reached preparation finalization")
				return flowstore.FlowRecord{}, nil
			}), nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			order = append(order, "reserve")
			if flowID != created.FlowID {
				t.Fatalf("ReserveLaunch(%q), want %q", flowID, created.FlowID)
			}
			return created, func() { order = append(order, "release") }, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("claim failure created a worktree")
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title,
		AfterFlowPersisted: func() error {
			order = append(order, "claim")
			return claimErr
		},
	})
	if !errors.Is(err, claimErr) {
		t.Fatalf("Create() error = %v, want claim error", err)
	}
	if result.Flow.FlowID != created.FlowID || !result.Flow.ProgressionClaim {
		t.Fatalf("Create() result = %#v, want marked recovery Flow", result.Flow)
	}
	if !marked {
		t.Fatal("Create did not persist the progression claim marker")
	}
	if got, want := strings.Join(order, " -> "), "create -> reserve -> claim -> release"; got != want {
		t.Fatalf("preparation admission order = %q, want %q", got, want)
	}
}

func TestFlowCreatorPreparationReservationRejectsReplacementBeforeAdmission(t *testing.T) {
	created := flowstore.FlowRecord{FlowID: "flow-1", Title: "Original", RepoPath: "/dev/alpha", PreparationGeneration: "generation-1"}
	replacement := created
	replacement.Title = "Replacement"
	replacement.PreparationGeneration = ""
	sideEffects := 0
	released := false
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, preparationFinalizerFuncForTest(func(func() error) (flowstore.FlowRecord, error) {
				t.Fatal("replacement reservation reached finalization")
				return flowstore.FlowRecord{}, nil
			}), nil
		},
		ReserveLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return replacement, func() { released = true }, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			sideEffects++
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title,
		AfterFlowPersisted: func() error {
			sideEffects++
			return nil
		},
	})
	if !flowstore.IsPreparationStale(err) {
		t.Fatalf("Create() error = %v, want stale reservation", err)
	}
	if sideEffects != 0 || !released {
		t.Fatalf("replacement reservation side effects/released = %d/%t, want 0/true", sideEffects, released)
	}
	if result.Flow.Title != created.Title {
		t.Fatalf("Create() result = %#v, want original generation", result.Flow)
	}
}

func TestFlowCreatorPreparationReservationRejectsNonceReplacementBeforeAdmission(t *testing.T) {
	created := flowstore.FlowRecord{FlowID: "flow-1", Title: "Original", RepoPath: "/dev/alpha", PreparationNonce: "nonce-a"}
	replacement := created
	replacement.Title = "Replacement"
	replacement.PreparationNonce = "nonce-b"
	sideEffects := 0
	released := false
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, preparationFinalizerFuncForTest(func(func() error) (flowstore.FlowRecord, error) {
				t.Fatal("nonce replacement reservation reached finalization")
				return flowstore.FlowRecord{}, nil
			}), nil
		},
		ReserveLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return replacement, func() { released = true }, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			sideEffects++
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title,
		AfterFlowPersisted: func() error {
			sideEffects++
			return nil
		},
	})
	if !flowstore.IsPreparationStale(err) {
		t.Fatalf("Create() error = %v, want stale reservation", err)
	}
	if sideEffects != 0 || !released {
		t.Fatalf("nonce replacement reservation side effects/released = %d/%t, want 0/true", sideEffects, released)
	}
	if result.Flow.Title != created.Title {
		t.Fatalf("Create() result = %#v, want original nonce", result.Flow)
	}
}

func TestFlowCreatorCreateRunsBootstrapDuringPreparation(t *testing.T) {
	var gotCtx actions.BootstrapContext
	var gotHook actions.BootstrapHook
	var calls []string

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		BootstrapHookForRepo: func(repoPath string) (actions.BootstrapHook, bool) {
			if repoPath != "/dev/alpha" {
				t.Fatalf("BootstrapHookForRepo(%q)", repoPath)
			}
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, hook actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			gotCtx = ctx
			gotHook = hook
			return nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,bootstrap" {
		t.Fatalf("call order = %#v", calls)
	}
	if gotCtx.RepoPath != "/dev/alpha" ||
		gotCtx.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		gotCtx.Ref != "flow/add-flow-mode" ||
		gotCtx.Kind != actions.WorktreeCreateFlow {
		t.Fatalf("bootstrap context = %#v", gotCtx)
	}
	if gotHook.Script != ".approach/bootstrap" || gotHook.TimeoutSeconds != 7 {
		t.Fatalf("bootstrap hook = %#v", gotHook)
	}
}

func TestFlowCreatorCreateBootstrapFailureBlocksPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var calls []string

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			calls = append(calls, "reserve")
			return flowstore.FlowRecord{FlowID: flowID}, func() { calls = append(calls, "release") }, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			return errors.New("missing env file")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-phase")
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}

	if strings.Join(calls, ",") != "create-flow,reserve,create-worktree,set-start,bootstrap,set-phase,release" {
		t.Fatalf("call order = %#v", calls)
	}
	if !strings.Contains(err.Error(), "Bootstrap hook failed") || !strings.Contains(err.Error(), "missing env file") {
		t.Fatalf("error = %q, want bootstrap failure", err)
	}
	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "plan" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "missing env file") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestFlowCreatorCreateBootstrapFailureBlocksAllLaunchableRootPhases(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				{PhaseID: "review", Title: "Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 2},
				{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 3, DependsOn: []string{"research"}},
			}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
					{PhaseID: "review", Title: "Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 2},
					{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 3, DependsOn: []string{"research"}},
				},
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}

	if len(phaseUpdates) != 2 {
		t.Fatalf("phase updates = %#v, want research and review blocked", phaseUpdates)
	}
	if phaseUpdates[0].PhaseID != "research" || phaseUpdates[0].Status != flowstore.PhaseBlocked || phaseUpdates[0].Outcome != "" {
		t.Fatalf("first phase update = %#v, want blocked research", phaseUpdates[0])
	}
	if phaseUpdates[1].PhaseID != "review" ||
		phaseUpdates[1].Status != flowstore.PhaseBlocked ||
		phaseUpdates[1].Outcome != flowstore.OutcomeBlocked ||
		!strings.Contains(phaseUpdates[1].Notes, "Bootstrap hook failed") {
		t.Fatalf("second phase update = %#v, want blocked review with outcome", phaseUpdates[1])
	}
}

func TestFlowCreatorCreateBootstrapFailureRereadsPhasesBeforeCompensation(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-race", Branch: "flow/race"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID: flowID,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}
	if phaseUpdate.PhaseID != "research" {
		t.Fatalf("blocked phase = %q, want the re-read record's research phase rather than the stale plan snapshot", phaseUpdate.PhaseID)
	}
}

func TestFlowCreatorCreateBootstrapFailureSkipsCompensationWhenRereadFails(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-race", Branch: "flow/race"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("store unavailable")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "could not confirm current Flow state") || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("error = %q, want unreadable-flow compensation refusal", err)
	}
	if (phaseUpdate != flowstore.PhaseUpdate{}) {
		t.Fatalf("phase update = %#v, want no compensation without a trustworthy re-read", phaseUpdate)
	}
}

func TestFlowCreatorCreateBootstrapFailureSkipsCompensationOnGenerationMismatch(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-race", Branch: "flow/race"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:                update.FlowID,
				WorktreePath:          update.WorktreePath,
				PreparationGeneration: "generation-a",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: flowID, PreparationGeneration: "generation-b"}, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "changed before compensation could run") {
		t.Fatalf("error = %q, want generation-mismatch compensation refusal", err)
	}
	if (phaseUpdate != flowstore.PhaseUpdate{}) {
		t.Fatalf("phase update = %#v, want no compensation against a replaced Flow", phaseUpdate)
	}
}

func TestFlowCreatorCreateBootstrapFailureSkipsCompensationOnNonceMismatch(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-race", Branch: "flow/race"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:           update.FlowID,
				WorktreePath:     update.WorktreePath,
				PreparationNonce: "nonce-a",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: flowID, PreparationNonce: "nonce-b"}, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "changed before compensation could run") {
		t.Fatalf("error = %q, want nonce-mismatch compensation refusal", err)
	}
	if (phaseUpdate != flowstore.PhaseUpdate{}) {
		t.Fatalf("phase update = %#v, want no compensation against a replaced Flow", phaseUpdate)
	}
}

func TestFlowCreatorMetadataFailureCompensatesLaunchableRoots(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Parked", Instructions: "Write the plan.", RepoPath: "/dev/alpha",
		PreparationNonce: "nonce-parked",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
			{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, DependsOn: []string{"plan"}},
		},
	}
	var notes string
	released := false
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, compensatingPreparationFinalizerForTest{record: &created, notes: &notes}, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			if flowID != created.FlowID {
				t.Fatalf("ReserveLaunch(%q), want %q", flowID, created.FlowID)
			}
			return created, func() { released = true }, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/flow-parked",
				Branch:       "flow/parked",
			}, nil
		},
		SetStartMetadata: func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("database busy")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			if flowID != created.FlowID {
				t.Fatalf("ReadFlow(%q), want %q", flowID, created.FlowID)
			}
			return created, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("SetPhase(%#v) must not replace finalizer compensation", update)
			return flowstore.FlowRecord{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title, Instructions: created.Instructions,
	})
	if err == nil || !strings.Contains(err.Error(), "database busy") {
		t.Fatalf("Create() error = %v, want metadata failure", err)
	}
	if !released {
		t.Fatal("Create() did not release the preparation reservation")
	}
	if len(result.Flow.Phases) == 0 || result.Flow.Phases[0].Status != flowstore.PhaseBlocked {
		t.Fatalf("Create() result = %#v, want compensated launchable root", result.Flow)
	}
	if !strings.Contains(notes, "database busy") || !strings.Contains(notes, "/dev/alpha-worktrees/flow-parked") {
		t.Fatalf("compensation notes = %q, want metadata error and surviving worktree path", notes)
	}
}

func TestFlowCreatorMetadataCommitUnknownContinuesWhenMetadataLanded(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Parked", Instructions: "Write the plan.", RepoPath: "/dev/alpha",
		PreparationNonce: "nonce-parked",
		Phases:           []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}},
	}
	landed := created
	landed.WorktreePath = "/dev/alpha-worktrees/flow-parked"
	landed.Branch = "flow/parked"
	landed.Commit = "abc123"
	var calls []string
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, recordingPreparationFinalizerForTest{record: &landed, calls: &calls}, nil
		},
		ReserveLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return created, func() {}, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: landed.WorktreePath, Branch: landed.Branch}, nil
		},
		ResolveCommit: func(string) string { return landed.Commit },
		SetStartMetadata: func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("commit acknowledgement failed")
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return landed, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("SetPhase(%#v) must not compensate a landed metadata write", update)
			return flowstore.FlowRecord{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title, Instructions: created.Instructions,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want finalization after landed metadata", err)
	}
	if result.Flow.PreparedAt == nil || strings.Join(calls, ",") != "finalize" {
		t.Fatalf("Create() result = %#v calls=%#v, want finalized landed metadata", result.Flow, calls)
	}
}

func TestFlowCreatorReservationFailureRetriesRetryableCompensation(t *testing.T) {
	created := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Parked", Instructions: "Write the plan.", RepoPath: "/dev/alpha",
		PreparationNonce: "nonce-parked",
		Phases:           []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}},
	}
	var notes string
	retries := 1
	creator := newFlowCreator(flowCreatorOptions{
		CreatePreparation: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			return created, retryableThenCompensatingFinalizerForTest{record: &created, notes: &notes, remaining: &retries}, nil
		},
		ReserveLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flowstore.FlowRecord{}, nil, errors.New("lock timeout")
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("reservation failure created a worktree")
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: created.Title, Instructions: created.Instructions,
	})
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("Create() error = %v, want reservation failure", err)
	}
	if retries != 0 {
		t.Fatalf("retryable compensation remaining = %d, want a retry", retries)
	}
	if len(result.Flow.Phases) == 0 || result.Flow.Phases[0].Status != flowstore.PhaseBlocked {
		t.Fatalf("Create() result = %#v, want compensated launchable root", result.Flow)
	}
	if !strings.Contains(notes, "lock timeout") {
		t.Fatalf("compensation notes = %q, want reservation error", notes)
	}
}

func TestFlowCreatorCreateWorktreeFailureBlocksFirstLaunchablePhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"research"}},
			}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Research Flow", Instructions: "Plan research"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "research" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "Worktree creation failed") ||
		!strings.Contains(phaseUpdate.Notes, "branch exists") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestFlowCreatorCreateWorktreeFailureBlocksAllLaunchableRootPhases(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate
	authoritative := flowstore.FlowRecord{
		FlowID: "flow-1",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
			{PhaseID: "spike", Title: "Spike", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 2},
			{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 3, DependsOn: []string{"research"}},
		},
	}

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			authoritative.Title = record.Title
			authoritative.Instructions = record.Instructions
			authoritative.RepoPath = record.RepoPath
			return authoritative, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			for i := range authoritative.Phases {
				if authoritative.Phases[i].PhaseID == update.PhaseID {
					authoritative.Phases[i].Status = update.Status
					authoritative.Phases[i].Notes = update.Notes
				}
			}
			return authoritative, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Research Flow", Instructions: "Plan research"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}

	if len(phaseUpdates) != 2 {
		t.Fatalf("phase updates = %#v, want research and spike blocked", phaseUpdates)
	}
	for i, wantPhaseID := range []string{"research", "spike"} {
		update := phaseUpdates[i]
		if update.FlowID != "flow-1" ||
			update.PhaseID != wantPhaseID ||
			update.Status != flowstore.PhaseBlocked ||
			!strings.Contains(update.Notes, "Worktree creation failed") ||
			!strings.Contains(update.Notes, "branch exists") {
			t.Fatalf("phase update %d = %#v, want blocked %s", i, update, wantPhaseID)
		}
	}
	if result.Flow.Phases[0].Status != flowstore.PhaseBlocked || result.Flow.Phases[1].Status != flowstore.PhaseBlocked {
		t.Fatalf("compensated result Flow = %#v, want both roots blocked", result.Flow)
	}
}

func TestFlowCreatorCreateWorktreeFailureRereadsPhasesBeforeCompensation(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
			}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID: flowID,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}
	if phaseUpdate.PhaseID != "research" {
		t.Fatalf("blocked phase = %q, want the re-read record's research phase rather than the stale plan snapshot", phaseUpdate.PhaseID)
	}
}

func TestFlowCreatorCreateWorktreeFailureSkipsCompensationWhenRereadFails(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("store unavailable")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}
	if !strings.Contains(err.Error(), "could not confirm current Flow state") || !strings.Contains(err.Error(), "store unavailable") ||
		!strings.Contains(err.Error(), "branch exists") {
		t.Fatalf("error = %q, want unreadable-flow compensation refusal", err)
	}
	if (phaseUpdate != flowstore.PhaseUpdate{}) {
		t.Fatalf("phase update = %#v, want no compensation without a trustworthy re-read", phaseUpdate)
	}
}

func TestFlowCreatorCreateWorktreeFailureSkipsCompensationOnGenerationMismatch(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.PreparationGeneration = "generation-a"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: flowID, PreparationGeneration: "generation-b"}, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Race", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}
	if !strings.Contains(err.Error(), "changed before compensation could run") {
		t.Fatalf("error = %q, want generation-mismatch compensation refusal", err)
	}
	if (phaseUpdate != flowstore.PhaseUpdate{}) {
		t.Fatalf("phase update = %#v, want no compensation against a replaced Flow", phaseUpdate)
	}
}

func TestFlowCreatorCreateWorktreeFailureReportsBlockedPhaseUpdateFailure(t *testing.T) {
	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("disk full")
		},
	})

	_, err := creator.Create(FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("Create returned nil error, want worktree failure")
	}
	if !strings.Contains(err.Error(), "branch exists") || !strings.Contains(err.Error(), "mark flow blocked: disk full") {
		t.Fatalf("error = %q, want worktree and flow-update failures", err)
	}
}

func TestFlowCreatorCreateStampsRequestAgentSettings(t *testing.T) {
	var captured flowstore.PhaseAgentSettings
	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			captured = opts.PhaseAgent
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-one", Branch: "flow/one"}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
	})

	if _, err := creator.Create(FlowStartRequest{
		RepoPath:        "/dev/alpha",
		Title:           "One",
		Instructions:    "Build it",
		AgentCommand:    "claude",
		Model:           "claude-opus-5",
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	want := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	if captured != want {
		t.Fatalf("createFlow settings = %#v, want %#v", captured, want)
	}
}

func TestFlowCreatorCreateDropsUnusableAgentSettings(t *testing.T) {
	tests := []struct {
		name string
		req  FlowStartRequest
	}{
		{name: "unsupported command", req: FlowStartRequest{AgentCommand: "gemini", Model: "claude-opus-5", ReasoningEffort: "high"}},
		{name: "model from another agent", req: FlowStartRequest{AgentCommand: "codex", Model: "claude-opus-5"}},
		{name: "no command", req: FlowStartRequest{Model: "claude-opus-5"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captured := flowstore.PhaseAgentSettings{Agent: "sentinel"}
			creator := newFlowCreatorForTest(flowCreatorOptions{
				CreateFlow: func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
					captured = opts.PhaseAgent
					record.FlowID = "flow-1"
					record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
					return record, nil
				},
				CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
					return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-one", Branch: "flow/one"}, nil
				},
				ResolveCommit: func(string) string { return "abc123" },
			})
			req := tc.req
			req.RepoPath = "/dev/alpha"
			req.Title = "One"
			req.Instructions = "Build it"
			if _, err := creator.Create(req); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if !captured.IsZero() {
				t.Fatalf("createFlow settings = %#v, want nothing stamped", captured)
			}
		})
	}
}

func TestFlowCreatorCreateRejectsCodexAppBeforeCreatingFlowOrWorktree(t *testing.T) {
	createFlowCalled := false
	createWorktreeCalled := false
	creator := newFlowCreatorForTest(flowCreatorOptions{
		CreateFlow: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			createFlowCalled = true
			return flowstore.FlowRecord{}, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			createWorktreeCalled = true
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := creator.Create(FlowStartRequest{
		RepoPath: "/dev/alpha", Title: "One", Instructions: "Build it", AgentCommand: "codex-app",
	})
	want := `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`
	if err == nil || err.Error() != want {
		t.Fatalf("Create() = %#v, %v, want error %q", result, err, want)
	}
	if createFlowCalled || createWorktreeCalled {
		t.Fatalf("rejected input performed work: createFlow=%t createWorktree=%t", createFlowCalled, createWorktreeCalled)
	}
}

func TestFlowCreatorCreatePersistsRequestedHeadlessPreference(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless *bool
		want     bool
	}{
		{name: "omitted defaults on", want: true},
		{name: "explicit off", headless: testFlowStartBoolPtr(false), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var flow flowstore.FlowRecord
			var gotOptions flowstore.CreateOptions
			creator := newFlowCreatorForTest(flowCreatorOptions{
				CreateFlow: func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
					gotOptions = opts
					record.FlowID = "flow-1"
					record.Headless = opts.Headless == nil || *opts.Headless
					record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}}
					flow = record
					return record, nil
				},
				CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
					return actions.FlowWorktreeCreateResult{WorktreePath: "/repo-worktrees/flow-1", Branch: "flow/one"}, nil
				},
				SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
					flow.WorktreePath = update.WorktreePath
					flow.Branch = update.Branch
					return flow, nil
				},
				ResolveCommit: func(string) string { return "abc123" },
			})

			result, err := creator.Create(FlowStartRequest{
				RepoPath: "/repo", Title: "Headless Flow", Instructions: "Create it.", AgentCommand: "codex", Headless: tt.headless,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if gotOptions.Headless != tt.headless {
				t.Fatalf("CreateOptions.Headless = %v, want request pointer %v", gotOptions.Headless, tt.headless)
			}
			if result.Flow.Headless != tt.want {
				t.Fatalf("headless result = flow %v, want %v", result.Flow.Headless, tt.want)
			}
		})
	}
}

func testFlowStartBoolPtr(value bool) *bool {
	return &value
}
