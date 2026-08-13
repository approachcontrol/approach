package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
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
		PlanPath:     "/state/approach/plans/plan-1/plan.md",
		Headless:     true,
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
				FlowID:   record.FlowID,
				Headless: record.Headless,
				Phases: []flowstore.FlowPhase{
					persistedPhase,
				},
			}, nil
		},
		NewLaunchID:      func() string { return "launch-1" },
		SessionStateRoot: "/state/approach/sessions/v1",
		AgentCommand:     "codex",
		Model:            "gpt-5.5",
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
		ctx.Model != "gpt-5.5" ||
		ctx.ReasoningEffort != "high" ||
		ctx.LaunchID != "launch-1" ||
		ctx.RepoPath != record.RepoPath ||
		ctx.WorktreePath != record.WorktreePath ||
		ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit ||
		ctx.SessionStateRoot != "/state/approach/sessions/v1" ||
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

func TestFlowPhaseLauncherPrepareRefreshesManualHeadlessPreferenceAfterReservation(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}

	for _, tt := range []struct {
		name          string
		cached        bool
		authoritative bool
		autoLaunch    bool
		want          bool
	}{
		{name: "manual launch observes headless disabled", cached: true, authoritative: false, want: false},
		{name: "manual launch observes headless enabled", cached: false, authoritative: true, want: true},
		{name: "auto launch remains headless", cached: true, authoritative: false, autoLaunch: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := flowstore.FlowRecord{
				FlowID:       "flow-1",
				RepoPath:     "/dev/alpha",
				WorktreePath: "/dev/alpha-worktrees/flow-implementation",
				Headless:     tt.cached,
				Phases:       []flowstore.FlowPhase{phase},
			}
			launcher := model.FlowPhaseLauncher{
				AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
					updated := record
					updated.Headless = tt.authoritative
					updated.Phases[0].Status = flowstore.PhaseRunning
					updated.UpdatedAt = time.Unix(1, 0)
					return updated, nil
				},
				NewLaunchID:  func() string { return "launch-1" },
				AgentCommand: "codex",
			}

			prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{
				Record:     record,
				Phase:      phase,
				AutoLaunch: tt.autoLaunch,
				Headless:   tt.cached,
			})
			if err != nil {
				t.Fatalf("Preflight() error = %v", err)
			}
			result, err := launcher.Prepare(prepared)
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if result.Context.Headless != tt.want {
				t.Fatalf("Headless = %v, want %v", result.Context.Headless, tt.want)
			}
		})
	}
}

func TestFlowPhaseLauncherLaunchesParkedPlanPhaseFromSavedFlow(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-parked",
		Title:        "Parked Flow",
		Instructions: "Write the initial plan later",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-parked",
		Branch:       "flow/parked",
		BaseRef:      "main",
		Commit:       "abc123",
		Headless:     true,
		Phases:       []flowstore.FlowPhase{phase},
	}
	persistedPhase := phase
	persistedPhase.Status = flowstore.PhaseRunning
	persistedPhase.LaunchIDs = []string{"launch-parked"}
	var updates []flowstore.PhaseLaunchUpdate
	launcher := model.FlowPhaseLauncher{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			updates = append(updates, update)
			return flowstore.FlowRecord{FlowID: record.FlowID, Headless: record.Headless, Phases: []flowstore.FlowPhase{persistedPhase}}, nil
		},
		NewLaunchID:      func() string { return "launch-parked" },
		SessionStateRoot: "/state/approach/sessions/v1",
		AgentCommand:     "codex",
		ReasoningEffort:  "high",
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: phase, Headless: true})
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
	if update := updates[0]; update.FlowID != "flow-parked" || update.PhaseID != "plan" || update.LaunchID != "launch-parked" || update.AutoLaunch {
		t.Fatalf("launch update = %#v", update)
	}
	ctx := result.Context
	if result.Route != model.FlowPhaseLaunchEmbedded ||
		ctx.LaunchID != "launch-parked" ||
		ctx.RepoPath != record.RepoPath ||
		ctx.WorktreePath != record.WorktreePath ||
		ctx.Branch != record.Branch ||
		ctx.Commit != record.Commit ||
		ctx.FlowID != record.FlowID ||
		ctx.FlowPhaseID != "plan" ||
		!ctx.Embedded ||
		!ctx.Headless ||
		!ctx.FlowLaunchTracked {
		t.Fatalf("launch result = route %d context %#v", result.Route, ctx)
	}
	for _, want := range []string{
		"Use the approach-flow skill for this launch.",
		"Write the initial plan later",
		"Produce a plan only; do not start coding in this phase.",
		"approach plan save",
		"approach flow plan set",
	} {
		if !strings.Contains(ctx.InitialPrompt, want) {
			t.Fatalf("prompt missing %q: %q", want, ctx.InitialPrompt)
		}
	}
}

func TestFlowPhaseLauncherStandardTemplateDoesNotReadPlanBody(t *testing.T) {
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		PlanID:       "plan-1",
		PlanPath:     "/state/approach/plans/plan-1/plan.md",
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
		AgentCommand:    "codex",
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
		PlanPath:     "/state/approach/plans/plan-1/plan.md",
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
		AgentCommand:    "codex",
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

func tmuxRouteLauncher(t *testing.T, backend string, tmuxAvailable bool) model.FlowPhaseLauncher {
	t.Helper()
	return model.FlowPhaseLauncher{
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID: update.FlowID,
				Phases: []flowstore.FlowPhase{{
					PhaseID:   update.PhaseID,
					Status:    flowstore.PhaseRunning,
					LaunchIDs: []string{update.LaunchID},
				}},
			}, nil
		},
		NewLaunchID:   func() string { return "launch-1" },
		AgentCommand:  "codex",
		Backend:       backend,
		TmuxAvailable: func() bool { return tmuxAvailable },
	}
}

func tmuxRoutePreparedRequest(t *testing.T, launcher model.FlowPhaseLauncher, headless bool) model.FlowPhaseLaunchPreparedRequest {
	t.Helper()
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-implementation",
		Phases:       []flowstore.FlowPhase{phase},
	}
	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: phase, Headless: headless})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	return prepared
}

func TestFlowPhaseLauncherPrepareSelectsTmuxRoute(t *testing.T) {
	launcher := tmuxRouteLauncher(t, "tmux", true)
	result, err := launcher.Prepare(tmuxRoutePreparedRequest(t, launcher, false))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if result.Route != model.FlowPhaseLaunchTmux {
		t.Fatalf("route = %d, want tmux", result.Route)
	}
	if result.FallbackNote != "" {
		t.Fatalf("tmux route must not report a fallback, got %q", result.FallbackNote)
	}
	ctx := result.Context
	// Embedded=false is load-bearing: it is what makes InitialPrompt reach the
	// agent as argv instead of waiting for a dock prefill that never comes.
	if ctx.Embedded {
		t.Fatal("tmux route context must not be embedded")
	}
	if !ctx.FlowLaunchTracked {
		t.Fatal("tmux route launches stay phase-tracked")
	}
	if ctx.Headless {
		t.Fatal("tmux route is interactive only")
	}
}

func TestFlowPhaseLauncherPrepareFallsBackToEmbeddedWithoutTmux(t *testing.T) {
	launcher := tmuxRouteLauncher(t, "tmux", false)
	result, err := launcher.Prepare(tmuxRoutePreparedRequest(t, launcher, false))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	if result.Route != model.FlowPhaseLaunchEmbedded {
		t.Fatalf("route = %d, want embedded fallback", result.Route)
	}
	if !strings.Contains(result.FallbackNote, "tmux") {
		t.Fatalf("fallback note = %q, want it to mention tmux", result.FallbackNote)
	}
	if !result.Context.Embedded {
		t.Fatal("fallback context must be embedded")
	}
}

func TestFlowPhaseLauncherPrepareReportsNoFallbackWithoutTmuxMode(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		tmux     bool
		headless bool
	}{
		// The embedded backend is a choice, not a fallback.
		{name: "embedded backend", backend: "embedded", tmux: false},
		// Headless is a design exclusion, not a fallback.
		{name: "headless in tmux mode", backend: "tmux", tmux: true, headless: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launcher := tmuxRouteLauncher(t, tt.backend, tt.tmux)
			result, err := launcher.Prepare(tmuxRoutePreparedRequest(t, launcher, tt.headless))
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if result.Route != model.FlowPhaseLaunchEmbedded {
				t.Fatalf("route = %d, want embedded", result.Route)
			}
			if result.FallbackNote != "" {
				t.Fatalf("fallback note = %q, want none", result.FallbackNote)
			}
		})
	}
}
