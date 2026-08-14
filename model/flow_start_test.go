package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
)

func TestFlowStarterStartPlanReturnsLaunchContext(t *testing.T) {
	var calls []string
	var created flowstore.FlowRecord
	var startUpdate flowstore.StartMetadataUpdate
	var launchUpdate flowstore.PhaseLaunchUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		SessionStateRoot: "/state/approach/sessions/v1",
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
		ctx.SessionStateRoot != "/state/approach/sessions/v1" ||
		ctx.FlowID != "flow-1" ||
		ctx.FlowPhaseID != "plan" ||
		ctx.PlanPhaseID != "plan" ||
		ctx.PlanPhaseTitle != "Plan" ||
		ctx.PlanPhaseStatus != flowstore.PhaseRunning ||
		ctx.ReasoningEffort != "high" {
		t.Fatalf("launch context = %#v", ctx)
	}
	prompt := strings.ToLower(ctx.InitialPrompt)
	for _, want := range []string{"approach-flow", "build the thing", "produce a plan only", "do not start coding", "create and persist the plan", "approach plan save", "approach flow plan set"} {
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

func TestFlowStarterStartPlanUsesAuthoritativeNormalizedPhase(t *testing.T) {
	createdPhase := flowstore.FlowPhase{PhaseID: " Plan ", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}
	authoritativePhase := flowstore.FlowPhase{
		PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseRunning,
		Agent: agent.CommandClaude,
	}
	var flow flowstore.FlowRecord
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{createdPhase}
			flow = record
			return record, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/worktree", Branch: "flow/one"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			flow.WorktreePath = update.WorktreePath
			flow.Branch = update.Branch
			flow.Commit = update.Commit
			return flow, nil
		},
		AddPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			flow.Phases = []flowstore.FlowPhase{authoritativePhase}
			return flow, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		NewLaunchID:   func() string { return "launch-1" },
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:    "/repo",
		Title:       "Normalized phase",
		PlanPhaseID: " Plan ",
		AgentPreferences: agent.Preferences{
			Command:      agent.CommandCodex,
			CodexModel:   agent.ModelGPT55,
			ClaudeModel:  agent.ModelClaudeSonnet5,
			ClaudeEffort: agent.ReasoningEffortMax,
		},
		AgentPreferencesProvided: true,
	})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	ctx := result.LaunchContext
	if ctx.Command != agent.CommandClaude || ctx.Model != agent.ModelClaudeSonnet5 || ctx.ReasoningEffort != agent.ReasoningEffortMax {
		t.Fatalf("launch settings = %q/%q/%q, want authoritative Claude settings", ctx.Command, ctx.Model, ctx.ReasoningEffort)
	}
	if ctx.FlowPhaseID != "plan" || ctx.PlanPhaseID != "plan" {
		t.Fatalf("launch phase IDs = flow %q plan %q, want canonical plan", ctx.FlowPhaseID, ctx.PlanPhaseID)
	}
}

func TestFlowStarterPersistsRequestedHeadlessPreferenceBeforeLaunch(t *testing.T) {
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
			starter := model.NewFlowStarter(model.FlowStarterOptions{
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
				AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					flow.Phases[0].Status = flowstore.PhaseRunning
					flow.Phases[0].LaunchIDs = []string{update.LaunchID}
					return flow, nil
				},
				ResolveCommit: func(string) string { return "abc123" },
				NewLaunchID:   func() string { return "launch-1" },
			})

			result, err := starter.StartPlan(model.FlowStartRequest{
				RepoPath: "/repo", Title: "Headless Flow", Instructions: "Create it.", AgentCommand: "codex", Headless: tt.headless,
			})
			if err != nil {
				t.Fatalf("StartPlan() error = %v", err)
			}
			if gotOptions.Headless != tt.headless {
				t.Fatalf("CreateOptions.Headless = %v, want request pointer %v", gotOptions.Headless, tt.headless)
			}
			if result.Flow.Headless != tt.want || result.LaunchContext.Headless != tt.want {
				t.Fatalf("headless result = flow %v launch %v, want %v", result.Flow.Headless, result.LaunchContext.Headless, tt.want)
			}
		})
	}
}

func testFlowStartBoolPtr(value bool) *bool {
	return &value
}

func TestFlowStarterStartPlanLaunchesFirstReadyRoot(t *testing.T) {
	var launchedPhase string
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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

func TestFlowStarterStartPlanRejectsPlanReviewRootWithoutLinkedPlan(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "review", Title: "Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
			}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/repo/worktrees/review", Branch: "flow/review"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "review", Title: "Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddPhaseLaunchID() should not run for plan-review root without plan: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
		NewLaunchID: func() string { return "launch-1" },
	})

	_, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/repo",
		Title:        "Review flow",
		Instructions: "Review the plan.",
	})
	if err == nil {
		t.Fatal("StartPlan() error = nil, want linked plan requirement")
	}
	if !strings.Contains(err.Error(), "Plan Review needs a linked plan before launch") {
		t.Fatalf("StartPlan() error = %q, want linked plan requirement", err)
	}
}

func TestFlowStarterStartPlanParksFlowWhenNoPhaseIsLaunchable(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			record.Phases = []flowstore.FlowPhase{
				{PhaseID: "ship", Title: "Ship", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, Order: 1},
			}
			return record, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/repo/worktrees/ship", Branch: "flow/ship"}, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Phases: []flowstore.FlowPhase{
					{PhaseID: "ship", Title: "Ship", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, Order: 1},
				},
			}, nil
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatalf("AddPhaseLaunchID() should not run when no phase is launchable: %#v", update)
			return flowstore.FlowRecord{}, nil
		},
		NewLaunchID: func() string { return "launch-1" },
	})

	result, err := starter.StartPlan(model.FlowStartRequest{
		RepoPath:     "/repo",
		Title:        "Merge-only flow",
		Instructions: "Track an externally merged change.",
	})
	if err != nil {
		t.Fatalf("StartPlan() error = %v", err)
	}
	if !result.LaunchSkipped {
		t.Fatal("StartPlan() LaunchSkipped = false, want parked Flow without launch")
	}
	if result.LaunchID != "" || result.LaunchContext.FlowID != "" {
		t.Fatalf("launch result = id %q context %#v, want no launch", result.LaunchID, result.LaunchContext)
	}
	if result.Flow.FlowID != "flow-1" || result.Flow.WorktreePath != "/repo/worktrees/ship" {
		t.Fatalf("result flow = %#v", result.Flow)
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
	for _, want := range []string{"Use the approach-flow skill", "Build reset", "After completing this phase goal"} {
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
	if gotHook.Script != ".approach/bootstrap" || gotHook.TimeoutSeconds != 7 {
		t.Fatalf("bootstrap hook = %#v", gotHook)
	}
}

func TestFlowStarterStartPlanBootstrapFailureBlocksPlanPhase(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	var calls []string

	starter := model.NewFlowStarter(model.FlowStarterOptions{
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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
		CreateFlow: func(record flowstore.FlowRecord, _ flowstore.CreateOptions) (flowstore.FlowRecord, error) {
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

	_, err := starter.StartPlan(model.FlowStartRequest{RepoPath: "/dev/alpha", Title: "Add Flow Mode", Instructions: "Build the thing"})
	if err == nil {
		t.Fatal("StartPlan returned nil error, want worktree failure")
	}
	if !strings.Contains(err.Error(), "branch exists") || !strings.Contains(err.Error(), "mark flow blocked: disk full") {
		t.Fatalf("error = %q, want worktree and flow-update failures", err)
	}
}

func TestPrepareFlowStampsRequestAgentSettings(t *testing.T) {
	var captured flowstore.PhaseAgentSettings
	starter := model.NewFlowStarter(model.FlowStarterOptions{
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

	if _, err := starter.PrepareFlow(model.FlowStartRequest{
		RepoPath:        "/dev/alpha",
		Title:           "One",
		Instructions:    "Build it",
		AgentCommand:    "claude",
		Model:           "claude-opus-5",
		ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("PrepareFlow() error = %v", err)
	}
	want := flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"}
	if captured != want {
		t.Fatalf("createFlow settings = %#v, want %#v", captured, want)
	}
}

func TestPrepareFlowDropsUnusableAgentSettings(t *testing.T) {
	tests := []struct {
		name string
		req  model.FlowStartRequest
	}{
		{name: "unsupported command", req: model.FlowStartRequest{AgentCommand: "gemini", Model: "claude-opus-5", ReasoningEffort: "high"}},
		{name: "model from another agent", req: model.FlowStartRequest{AgentCommand: "codex", Model: "claude-opus-5"}},
		{name: "no command", req: model.FlowStartRequest{Model: "claude-opus-5"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captured := flowstore.PhaseAgentSettings{Agent: "sentinel"}
			starter := model.NewFlowStarter(model.FlowStarterOptions{
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
			if _, err := starter.PrepareFlow(req); err != nil {
				t.Fatalf("PrepareFlow() error = %v", err)
			}
			if !captured.IsZero() {
				t.Fatalf("createFlow settings = %#v, want nothing stamped", captured)
			}
		})
	}
}

func TestPrepareFlowRejectsCodexAppBeforeCreatingFlowOrWorktree(t *testing.T) {
	createFlowCalled := false
	createWorktreeCalled := false
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateFlow: func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			createFlowCalled = true
			return flowstore.FlowRecord{}, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			createWorktreeCalled = true
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	result, err := starter.PrepareFlow(model.FlowStartRequest{
		RepoPath: "/dev/alpha", Title: "One", Instructions: "Build it", AgentCommand: "codex-app",
	})
	want := `unsupported agent "codex-app"; choose codex or claude`
	if err == nil || err.Error() != want {
		t.Fatalf("PrepareFlow() = %#v, %v, want error %q", result, err, want)
	}
	if createFlowCalled || createWorktreeCalled {
		t.Fatalf("rejected input performed work: createFlow=%t createWorktree=%t", createFlowCalled, createWorktreeCalled)
	}
}
