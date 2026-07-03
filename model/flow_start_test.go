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
		Model:            "gpt-5.5",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanPhaseID:      "plan",
		PlanPhaseTitle:   "Plan",
		PlanPhaseStatus:  flowstore.PhaseRunning,
		ReasoningEffort:  "high",
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
		ctx.Model != "gpt-5.5" ||
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
		ctx.PlanPhaseStatus != flowstore.PhaseRunning ||
		ctx.ReasoningEffort != "high" {
		t.Fatalf("launch context = %#v", ctx)
	}
	prompt := strings.ToLower(ctx.InitialPrompt)
	for _, want := range []string{"wtui-flow", "build the thing", "produce a plan only", "do not start coding", "create and persist the plan", "wtui plan save", "wtui flow plan set"} {
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

func TestFlowStarterStartPlanLaunchesFirstReadyRoot(t *testing.T) {
	var launchedPhase string
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"research"}},
			}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/repo/worktrees/research", Branch: "flow/research"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID: update.FlowID,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
					{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"research"}},
				},
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launchedPhase = update.PhaseID
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		NewLaunchID: func() string { return "launch-1" },
		ResolveCommit: func(string) string {
			return "abc123"
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/repo",
		Title:        "Research flow",
		Instructions: "Plan the research.",
	})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	if launchedPhase != "research" {
		t.Fatalf("launched phase = %q, want research", launchedPhase)
	}
	if result.LaunchContext.FlowPhaseID != "research" || result.LaunchContext.PlanPhaseID != "research" {
		t.Fatalf("launch context phase IDs = flow %q plan %q, want research", result.LaunchContext.FlowPhaseID, result.LaunchContext.PlanPhaseID)
	}
}

func TestFlowStarterStartPlanUsesGenericPromptForNonPlanRoot(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "triage", Title: "Triage", Kind: "", Status: flowstore.PhaseReady, Order: 1},
			}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/repo/worktrees/triage", Branch: "flow/triage"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID: update.FlowID,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "triage", Title: "Triage", Kind: "", Status: flowstore.PhaseReady, Order: 1},
				},
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		NewLaunchID: func() string { return "launch-1" },
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/repo",
		Title:        "Triage flow",
		Instructions: "Sort the task.",
	})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	if !strings.Contains(result.LaunchContext.InitialPrompt, "Flow phase: Triage (triage).") {
		t.Fatalf("initial prompt did not use generic phase prompt:\n%s", result.LaunchContext.InitialPrompt)
	}
	if strings.Contains(result.LaunchContext.InitialPrompt, "Produce a plan only") {
		t.Fatalf("generic root prompt should not use plan-only wording:\n%s", result.LaunchContext.InitialPrompt)
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

func TestFlowStarterPrepareFlowCreatesLaunchableFlowWithoutLaunchID(t *testing.T) {
	var calls []string
	var created flowstore.FlowRecord
	var startUpdate flowstore.StartMetadataUpdate

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
		AddPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("PrepareFlow should not allocate a launch ID")
			return flowstore.FlowRecord{}, nil
		},
	})

	result, err := starter.PrepareFlow(model.FlowStartRequest{
		RepoPath:     "/dev/alpha",
		Title:        "Parked Flow",
		Instructions: "Plan later",
		BaseRef:      "main",
	})
	if err != nil {
		t.Fatalf("PrepareFlow returned error: %v", err)
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
		result.Flow.Commit != "abc123" ||
		result.LaunchID != "" ||
		result.LaunchContext.FlowID != "" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Flow.Phases) != 1 ||
		result.Flow.Phases[0].PhaseID != "plan" ||
		result.Flow.Phases[0].Status != flowstore.PhaseReady ||
		len(result.Flow.Phases[0].LaunchIDs) != 0 {
		t.Fatalf("plan phase = %#v", result.Flow.Phases)
	}
}

func TestFlowStarterStartPlanUsesConfiguredPromptTemplate(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-plan", Branch: "flow/plan"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Instructions: "Build the thing",
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				BaseRef:      update.BaseRef,
				Commit:       update.Commit,
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		ResolveCommit: func(string) string {
			return "abc123"
		},
		NewLaunchID: func() string {
			return "launch-1"
		},
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "Plan {flow_id}: {instructions} in {worktree_path} on {branch} from {commit}; keep {unknown}",
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/dev/alpha",
		Title:        "Add Flow Mode",
		Instructions: "Build the thing",
		BaseRef:      "main",
		AgentCommand: "codex",
	})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	want := appendFlowDoneInstructionForTest("Plan flow-1: Build the thing in /dev/alpha-worktrees/flow-plan on flow/plan from abc123; keep {unknown}")
	if result.LaunchContext.InitialPrompt != want {
		t.Fatalf("plan prompt = %q, want %q", result.LaunchContext.InitialPrompt, want)
	}
}

func TestFlowStarterStartPlanTemplateUsesCustomRootPhase(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"research"}},
			}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/research", Branch: "flow/research"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Instructions: "Research it",
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
					{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 2, DependsOn: []string{"research"}},
				},
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		NewLaunchID: func() string {
			return "launch-1"
		},
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "Start {phase_id}: {phase_title}",
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/dev/alpha",
		Title:        "Research Flow",
		Instructions: "Research it",
		AgentCommand: "codex",
	})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	want := appendFlowDoneInstructionForTest("Start research: Research")
	if result.LaunchContext.InitialPrompt != want {
		t.Fatalf("plan prompt = %q, want %q", result.LaunchContext.InitialPrompt, want)
	}
}

func TestFlowStarterStartPlanUsesRequestTimePromptTemplate(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-live"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/live", Branch: "flow/live"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Instructions: "Build live",
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Commit:       update.Commit,
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		NewLaunchID:   func() string { return "launch-live" },
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "old template {flow_id}",
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/dev/alpha",
		Title:        "Live",
		Instructions: "Build live",
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "new template {flow_id}",
		},
	})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	want := appendFlowDoneInstructionForTest("new template flow-live")
	if result.LaunchContext.InitialPrompt != want {
		t.Fatalf("plan prompt = %q, want %q", result.LaunchContext.InitialPrompt, want)
	}
}

func TestFlowStarterStartPlanUsesExplicitZeroRequestTimePromptTemplates(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-reset"
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/reset", Branch: "flow/reset"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				Title:        "Reset",
				Instructions: "Build reset",
				RepoPath:     "/dev/alpha",
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Commit:       update.Commit,
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		NewLaunchID:   func() string { return "launch-reset" },
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan: "old startup template {flow_id}",
		},
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:                    "/dev/alpha",
		Title:                       "Reset",
		Instructions:                "Build reset",
		FlowPromptTemplates:         model.FlowPromptTemplates{},
		FlowPromptTemplatesProvided: true,
	})
	if err != nil {
		t.Fatalf("StartPlan returned error: %v", err)
	}

	if strings.Contains(result.LaunchContext.InitialPrompt, "old startup template") {
		t.Fatalf("explicit zero request templates should not use startup template: %q", result.LaunchContext.InitialPrompt)
	}
	for _, want := range []string{"Use the wtui-flow skill", "Build reset", "After completing this phase goal"} {
		if !strings.Contains(result.LaunchContext.InitialPrompt, want) {
			t.Fatalf("built-in plan prompt missing %q: %q", want, result.LaunchContext.InitialPrompt)
		}
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

func TestFlowStarterStartPlanBootstrapFailureBlocksAllLaunchableRootPhases(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
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

func TestFlowStarterPrepareFlowWorktreeFailureBlocksFirstLaunchablePhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
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

	_, err := starter.PrepareFlow(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Research Flow", Instructions: "Plan research"})
	if err == nil {
		t.Fatal("PrepareFlow returned nil error, want worktree failure")
	}

	if phaseUpdate.FlowID != "flow-1" ||
		phaseUpdate.PhaseID != "research" ||
		phaseUpdate.Status != flowstore.PhaseBlocked ||
		!strings.Contains(phaseUpdate.Notes, "Worktree creation failed") ||
		!strings.Contains(phaseUpdate.Notes, "branch exists") {
		t.Fatalf("phase update = %#v", phaseUpdate)
	}
}

func TestFlowStarterPrepareFlowWorktreeFailureBlocksAllLaunchableRootPhases(t *testing.T) {
	var phaseUpdates []flowstore.PhaseUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "research", Title: "Research", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
				{PhaseID: "spike", Title: "Spike", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 2},
				{PhaseID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, Status: flowstore.PhasePending, Order: 3, DependsOn: []string{"research"}},
			}
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch exists")
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates = append(phaseUpdates, update)
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := starter.PrepareFlow(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Research Flow", Instructions: "Plan research"})
	if err == nil {
		t.Fatal("PrepareFlow returned nil error, want worktree failure")
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
