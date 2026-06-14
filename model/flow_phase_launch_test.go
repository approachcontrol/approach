package model_test

import (
	"strings"
	"testing"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model"
)

func TestFlowPhaseLauncherPrepareManualReadyPhaseLaunch(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Branch:       "flow/implementation",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/wtui/plans/plan-1/plan.md",
		Phases:       []flowstore.FlowPhase{phase},
	}
	persistedPhase := phase
	persistedPhase.Status = flowstore.PhaseRunning
	persistedPhase.LaunchIDs = []string{"launch-1"}
	var updates []flowstore.PhaseLaunchUpdate
	readPlanCalled := false
	launcher := model.FlowPhaseLauncher{
		ReadPlan: func(string) (string, error) {
			readPlanCalled = true
			return "plan body", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return flowstore.FlowRecord{
				FlowID: record.FlowID,
				Phases: []flowstore.FlowPhase{
					persistedPhase,
				},
			}, nil
		},
		NewLaunchID:      func() string { return "launch-1" },
		SessionStateRoot: "/state/wtui/sessions/v1",
		AgentCommand:     "codex",
		ReasoningEffort:  "high",
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{
		Record:   record,
		Phase:    phase,
		Headless: true,
	})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := launcher.Prepare(prepared)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("launch updates = %#v, want one", updates)
	}
	update := updates[0]
	if update.FlowID != "flow-1" ||
		update.PhaseID != "implementation" ||
		update.LaunchID != "launch-1" ||
		update.AutoLaunch {
		t.Fatalf("launch update = %#v, want manual implementation launch", update)
	}
	if readPlanCalled {
		t.Fatal("built-in implementation prompt should not read the linked plan body")
	}
	if result.Route != model.FlowPhaseLaunchEmbedded {
		t.Fatalf("route = %d, want embedded", result.Route)
	}
	ctx := result.Context
	if ctx.Command != "codex" ||
		ctx.ReasoningEffort != "high" ||
		ctx.LaunchID != "launch-1" ||
		ctx.RepoPath != record.RepoPath ||
		ctx.WorktreePath != record.WorktreePath ||
		ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit ||
		ctx.SessionStateRoot != "/state/wtui/sessions/v1" ||
		ctx.PlanID != record.PlanID ||
		ctx.PlanPath != record.PlanPath ||
		ctx.FlowID != record.FlowID ||
		ctx.FlowPhaseID != phase.PhaseID ||
		!ctx.Embedded ||
		!ctx.Headless ||
		!ctx.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", ctx)
	}
	wantPrompt := model.FlowPhasePromptForTest(record, persistedPhase, record.PlanPath, "", model.FlowPromptTemplates{})
	if ctx.InitialPrompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", ctx.InitialPrompt, wantPrompt)
	}
}

func TestFlowPhaseLauncherStandardTemplateDoesNotReadPlanBody(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		PlanID:       "plan-1",
		PlanPath:     "/state/wtui/plans/plan-1/plan.md",
		Phases:       []flowstore.FlowPhase{phase},
	}
	readPlanCalled := false
	launcher := model.FlowPhaseLauncher{
		ReadPlan: func(string) (string, error) {
			readPlanCalled = true
			return "secret plan body", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{phase}}, nil
		},
		NewLaunchID:     func() string { return "launch-1" },
		AgentCommand:    "codex-app",
		PromptTemplates: model.FlowPromptTemplates{Implementation: "Implementation template: {plan_body}"},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: phase})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := launcher.Prepare(prepared)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if readPlanCalled {
		t.Fatal("standard implementation template should not read the linked plan body")
	}
	if strings.Contains(result.Context.InitialPrompt, "secret plan body") {
		t.Fatalf("standard implementation prompt included plan body: %q", result.Context.InitialPrompt)
	}
}

func TestFlowPhaseLauncherGenericTemplateReadsPlanBody(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "qa-check", Title: "QA Check", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-qa",
		PlanID:       "plan-1",
		PlanPath:     "/state/wtui/plans/plan-1/plan.md",
		Phases:       []flowstore.FlowPhase{phase},
	}
	readPlanCalled := false
	launcher := model.FlowPhaseLauncher{
		ReadPlan: func(string) (string, error) {
			readPlanCalled = true
			return "generic plan body", nil
		},
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID, Phases: []flowstore.FlowPhase{phase}}, nil
		},
		NewLaunchID:     func() string { return "launch-1" },
		AgentCommand:    "codex-app",
		PromptTemplates: model.FlowPromptTemplates{Generic: "Generic template: {plan_body}"},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: phase})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := launcher.Prepare(prepared)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if !readPlanCalled {
		t.Fatal("generic phase template should read the linked plan body")
	}
	if !strings.Contains(result.Context.InitialPrompt, "generic plan body") {
		t.Fatalf("generic phase prompt missing plan body: %q", result.Context.InitialPrompt)
	}
}
