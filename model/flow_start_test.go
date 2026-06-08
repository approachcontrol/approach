package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model"
)

func TestFlowStarterStartPlanReturnsLaunchContext(t *testing.T) {
	var calls []string
	var created flowstore.FlowRecord
	var startUpdate flowstore.StartMetadataUpdate
	var launchUpdate flowstore.PhaseLaunchUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			calls = append(calls, "create-flow")
			created = record
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			if repoPath != "/dev/alpha" || title != "Add Flow Mode" || baseRef != "main" {
				t.Fatalf("CreateWorktree(%q, %q, %q)", repoPath, title, baseRef)
			}
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode", Branch: "flow/add-flow-mode"}, nil
		},
		ResolveCommit: func(path string) string {
			calls = append(calls, "resolve-commit")
			if path != "/dev/alpha-worktrees/flow-add-flow-mode" {
				t.Fatalf("ResolveCommit(%q)", path)
			}
			return "abc123"
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			startUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID, Instructions: "Build the thing", WorktreePath: update.WorktreePath, Branch: update.Branch, BaseRef: update.BaseRef, Commit: update.Commit}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "add-launch")
			launchUpdate = update
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Instructions: "Build the thing",
				WorktreePath: startUpdate.WorktreePath,
				Branch:       startUpdate.Branch,
				BaseRef:      startUpdate.BaseRef,
				Commit:       startUpdate.Commit,
				Phases:       []flowstore.FlowPhase{{PhaseID: update.PhaseID, Status: flowstore.PhaseRunning, LaunchIDs: []string{update.LaunchID}}},
			}, nil
		},
		NewLaunchID: func() string {
			return "launch-1"
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:         "/dev/alpha",
		Title:            "Add Flow Mode",
		Instructions:     "Build the thing",
		BaseRef:          "main",
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanPhaseID:      "plan",
		PlanPhaseTitle:   "Plan",
		PlanPhaseStatus:  flowstore.PhaseRunning,
	})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,resolve-commit,set-start,add-launch" {
		t.Fatalf("call order = %#v", calls)
	}
	if created.Title != "Add Flow Mode" || created.Instructions != "Build the thing" || created.RepoPath != "/dev/alpha" || created.BaseRef != "main" {
		t.Fatalf("created record = %#v", created)
	}
	if startUpdate.FlowID != "flow-1" ||
		startUpdate.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		startUpdate.Branch != "flow/add-flow-mode" ||
		startUpdate.BaseRef != "main" ||
		startUpdate.Commit != "abc123" {
		t.Fatalf("start update = %#v", startUpdate)
	}
	if launchUpdate.FlowID != "flow-1" || launchUpdate.PhaseID != "plan" || launchUpdate.LaunchID != "launch-1" {
		t.Fatalf("launch update = %#v", launchUpdate)
	}
	if result.Flow.FlowID != "flow-1" ||
		result.Flow.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		result.Flow.Branch != "flow/add-flow-mode" ||
		result.Flow.BaseRef != "main" ||
		result.Flow.Commit != "abc123" ||
		len(result.Flow.Phases) != 1 ||
		result.Flow.Phases[0].Status != flowstore.PhaseRunning ||
		result.Flow.Phases[0].LaunchIDs[0] != "launch-1" {
		t.Fatalf("result flow = %#v", result.Flow)
	}

	ctx := result.LaunchContext
	if ctx.Command != "codex" ||
		ctx.LaunchID != "launch-1" ||
		ctx.RepoPath != "/dev/alpha" ||
		ctx.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		ctx.Branch != "flow/add-flow-mode" ||
		ctx.Commit != "abc123" ||
		ctx.SessionStateRoot != "/state/wtui/sessions/v1" ||
		ctx.FlowID != "flow-1" ||
		ctx.FlowPhaseID != "plan" ||
		ctx.PlanPhaseID != "plan" ||
		ctx.PlanPhaseTitle != "Plan" ||
		ctx.PlanPhaseStatus != flowstore.PhaseRunning {
		t.Fatalf("launch context = %#v", ctx)
	}
	prompt := strings.ToLower(ctx.InitialPrompt)
	for _, want := range []string{"wtui-flow", "build the thing", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("launch prompt missing %q: %q", want, ctx.InitialPrompt)
		}
	}
	for _, unwanted := range []string{"flow-1", "flow/add-flow-mode", "/dev/alpha-worktrees/flow-add-flow-mode", "base ref", "add flow mode"} {
		if strings.Contains(prompt, strings.ToLower(unwanted)) {
			t.Fatalf("launch prompt should not include metadata %q: %q", unwanted, ctx.InitialPrompt)
		}
	}
}

func TestFlowStarterStartPlanRequiresCreateFlow(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("worktree should not be created without a Flow persistence adapter")
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("StartPlan returned nil error, want missing adapter failure")
	}
	if !strings.Contains(err.Error(), "missing CreateFlow") {
		t.Fatalf("error = %q, want missing CreateFlow", err)
	}
}

func TestFlowStarterStartPlanRunsBootstrapBeforeLaunchID(t *testing.T) {
	var gotCtx actions.BootstrapContext
	var gotHook actions.BootstrapHook
	var calls []string

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, hook actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			gotCtx = ctx
			gotHook = hook
			return nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "add-launch")
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		NewLaunchID: func() string {
			return "launch-1"
		},
	})

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,bootstrap,add-launch" {
		t.Fatalf("call order = %#v", calls)
	}
	if gotCtx.RepoPath != "/dev/alpha" ||
		gotCtx.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		gotCtx.Ref != "flow/add-flow-mode" ||
		gotCtx.Kind != actions.WorktreeCreateFlow {
		t.Fatalf("bootstrap context = %#v", gotCtx)
	}
	if gotHook.Script != ".wtui/bootstrap" || gotHook.TimeoutSeconds != 7 {
		t.Fatalf("bootstrap hook = %#v", gotHook)
	}
}

func TestFlowStarterStartPlanBootstrapFailureBlocksPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var calls []string

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
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
		AddPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("launch ID should not be recorded after bootstrap failure")
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("StartPlan returned nil error, want bootstrap failure")
	}

	if strings.Join(calls, ",") != "create-flow,create-worktree,set-start,bootstrap,set-phase" {
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

func TestFlowStarterStartPlanWorktreeFailureBlocksRequestedPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
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

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing", PlanPhaseID: "custom-plan"})
	if err == nil {
		t.Fatal("StartPlan returned nil error, want worktree failure")
	}

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "custom-plan" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "Worktree creation failed") ||
		!strings.Contains(phaseUpdate.Notes, "branch exists") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestFlowStarterStartPlanWorktreeFailureReportsBlockedPhaseUpdateFailure(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("StartPlan returned nil error, want worktree failure")
	}
	if !strings.Contains(err.Error(), "branch exists") || !strings.Contains(err.Error(), "mark flow blocked: disk full") {
		t.Fatalf("error = %q, want worktree and flow-update failures", err)
	}
}
