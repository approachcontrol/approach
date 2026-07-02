package model_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

func TestModel_Key7SwitchesToPlansAndFetches(t *testing.T) {
	var gotFilter planstore.PlanFilter
	want := []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Persist plans", RepoPath: "/dev/alpha", Branch: "main", Status: "draft"},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.Mode() != ui.ModePlans {
		t.Fatalf("mode = %d, want plans", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected plans fetch command")
	}
	if gotFilter.RepoPath != "" {
		t.Fatalf("plan lister ran before command execution: %#v", gotFilter)
	}
	msg, ok := cmd().(model.PlanResultMsg)
	if !ok {
		t.Fatalf("expected PlanResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	got := m.Plans()
	if len(got) != 1 || got[0].PlanID != "plan-1" {
		t.Fatalf("Plans() = %#v, want %#v", got, want)
	}
}

func TestModel_ChangingRepoRefetchesPlansMode(t *testing.T) {
	var filters []planstore.PlanFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			filters = append(filters, filter)
			return []planstore.PlanRecord{{PlanID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath, Title: "T", Status: "draft"}}, nil
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if cmd == nil {
		t.Fatal("expected initial plans fetch")
	}
	m, _ = update(m, cmd())
	if got := m.Plans(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Plans() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if cmd != nil {
		t.Fatalf("expected nil cmd switching to repo pane, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected plans refetch after repo change")
	}
	if got := m.Plans(); len(got) != 0 {
		t.Fatalf("expected plans cleared before refetch, got %#v", got)
	}
	m, _ = update(m, cmd())
	if got := m.Plans(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Plans() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("plan filters = %#v", filters)
	}
}

func TestModel_StalePlanResultIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(planstore.PlanFilter) ([]planstore.PlanRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	// A result with a stale (zero) list request must be ignored.
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "stale", RepoPath: "/dev/alpha", Title: "T", Status: "draft"},
	}, ListRequest: 999999})
	if got := m.Plans(); len(got) != 0 {
		t.Fatalf("stale plan result should be ignored, got %#v", got)
	}
}

func TestModel_PlanListErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			return nil, errors.New("plans unavailable")
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if cmd == nil {
		t.Fatal("expected plans fetch command")
	}
	m, _ = update(m, cmd())
	if got := m.View(); !strings.Contains(got, "failed to load plans") {
		t.Fatalf("expected plan load error in view:\n%s", got)
	}
}

func TestModel_IKeyOpensPlanLaunchInstructionsInput(t *testing.T) {
	var launched bool
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/plans",
		Branch:       "feature/plans",
		Commit:       "abc123",
	}}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	if launched {
		t.Fatal("opening launch instructions must not launch the agent")
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected launch instructions input overlay, got %d", m.Overlay())
	}
	if got := m.InputMode(); got != modal.InputMultiLine {
		t.Fatalf("launch instructions input mode = %v, want multi-line", got)
	}
	if !strings.Contains(m.View(), "Launch instructions") {
		t.Fatalf("expected launch instructions prompt in view:\n%s", m.View())
	}
	prompt := strings.ToLower(m.WorktreeInput())
	for _, want := range []string{"implement plans", "plan-1", "/state/wtui/sessions/v1/plans/plan-1/plan.md", "read the plan file", "begin implementation"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("initial prompt missing %q: %q", want, m.WorktreeInput())
		}
	}
}

func TestModel_AKeyOpensPlanLaunchInstructionsInput(t *testing.T) {
	var launched bool
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand: "codex",
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{{
		PlanID:   "plan-1",
		Title:    "Implement plans",
		Status:   "approved",
		RepoPath: "/dev/alpha",
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	if launched {
		t.Fatal("opening launch instructions must not launch the agent")
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected launch instructions input overlay, got %d", m.Overlay())
	}
	if got := m.InputMode(); got != modal.InputMultiLine {
		t.Fatalf("launch instructions input mode = %v, want multi-line", got)
	}
	if !strings.Contains(m.WorktreeInput(), "Implement the saved wtui plan") {
		t.Fatalf("expected plan launch prompt, got %q", m.WorktreeInput())
	}
}

func TestModel_PlanPromptTemplateReplacesSupportedPlaceholders(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:       "codex",
		PlanPromptTemplate: "Do {title} ({plan_id}) from {plan_path} in {repo_path} at {worktree_path}; keep {unknown}",
		PlanMarkdownPath:   func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/plan-repo",
		WorktreePath: "/dev/plan-worktree",
	}}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	want := "Do Implement plans (plan-1) from /state/plans/plan-1/plan.md in /dev/plan-repo at /dev/plan-worktree; keep {unknown}"
	if m.WorktreeInput() != want {
		t.Fatalf("launch instructions = %q, want %q", m.WorktreeInput(), want)
	}
}

func TestModel_PlanPromptTemplateAppliesToSelectedPlanPhase(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:       "codex",
		PlanPromptTemplate: "Do phase {phase_id}: {phase_title} ({phase_status}) for {title} from {plan_path} in {repo_path} at {worktree_path}; keep {unknown}",
		PlanMarkdownPath:   func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/plan-repo",
		WorktreePath: "/dev/plan-worktree",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Store tracer bullet", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "CLI subcommands", Status: "pending", Order: 2},
		},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	want := "Do phase p2: CLI subcommands (pending) for Implement plans from /state/plans/plan-1/plan.md in /dev/plan-repo at /dev/plan-worktree; keep {unknown}"
	if m.WorktreeInput() != want {
		t.Fatalf("phase launch instructions = %q, want %q", m.WorktreeInput(), want)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	_, launchCmd := update(m, cmd())
	if launchCmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.InitialPrompt != want {
		t.Fatalf("submitted phase launch prompt = %q, want %q", got.InitialPrompt, want)
	}
	if got.PlanPhaseID != "p2" || got.PlanPhaseTitle != "CLI subcommands" || got.PlanPhaseStatus != "pending" {
		t.Fatalf("unexpected phase launch context: %#v", got)
	}
}

func TestModel_PlanPromptTemplateBlankFallsBackToDefault(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:       "codex",
		PlanPromptTemplate: "   ",
		PlanMarkdownPath:   func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	want := `Implement the saved wtui plan "Implement plans" (ID: plan-1) at /state/plans/plan-1/plan.md. Read the plan file, then begin implementation.`
	if m.WorktreeInput() != want {
		t.Fatalf("default launch instructions = %q, want %q", m.WorktreeInput(), want)
	}
}

func TestModel_PlanLaunchInstructionsSubmitLaunchesAgent(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:         "codex",
		CodexReasoningEffort: "xhigh",
		SessionStateRoot:     "/state/wtui/sessions/v1",
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/plans",
		Branch:       "feature/plans",
		Commit:       "abc123",
	}}, ListRequest: m.ListRequest(ui.ModePlans)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if got := m.InputMode(); got != modal.InputMultiLine {
		t.Fatalf("launch instructions input mode = %v, want multi-line", got)
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Custom instructions")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyHome})
	for range len("Custom ") {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("launch")})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if got.Command != "" {
		t.Fatalf("submit command should defer launch until handled, got %#v", got)
	}
	m, launchCmd := update(m, cmd())
	if launchCmd == nil {
		t.Fatal("expected agent launch command")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected launch instructions overlay closed, got %d", m.Overlay())
	}
	if got.Command != "codex" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha-worktrees/plans" ||
		got.Branch != "feature/plans" ||
		got.Commit != "abc123" ||
		got.SessionStateRoot != "/state/wtui/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/wtui/sessions/v1/plans/plan-1/plan.md" ||
		got.ReasoningEffort != "xhigh" ||
		got.InitialPrompt != "Custom launch\ninstructions" {
		t.Fatalf("unexpected launch context: %#v", got)
	}
	if got.LaunchID == "" {
		t.Fatalf("expected launch ID in context: %#v", got)
	}
}

func TestModel_PlanLaunchInstructionsEscCancels(t *testing.T) {
	var launched bool
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd cancelling launch instructions, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected overlay closed, got %d", m.Overlay())
	}
	if launched {
		t.Fatal("cancelled launch instructions must not launch agent")
	}
}

func TestModel_PlanLaunchInstructionsRejectsBlankSubmit(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected nil cmd for blank launch instructions, got %T", cmd)
	}
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected input overlay to remain, got %d", m.Overlay())
	}
	if m.WorktreeInputErr() != "enter launch instructions" {
		t.Fatalf("expected launch instructions error, got %q", m.WorktreeInputErr())
	}
}

func TestModel_IKeyLaunchesAgentFromSelectedPlanPhase(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/plans",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Store tracer bullet", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "CLI subcommands", Status: "pending", Order: 2},
		},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if gotPhase := m.SelectedPlanPhaseID(); gotPhase != "p2" {
		t.Fatalf("selected phase = %q, want p2", gotPhase)
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	if got.Command != "" {
		t.Fatalf("opening launch instructions should defer launch, got %#v", got)
	}
	prompt := strings.ToLower(m.WorktreeInput())
	for _, want := range []string{"implement only", "selected phase", "p2", "cli subcommands", "pending", "read the plan file"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("phase prompt missing %q: %q", want, m.WorktreeInput())
		}
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	_, launchCmd := update(m, cmd())
	if launchCmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.PlanPhaseID != "p2" || got.PlanPhaseTitle != "CLI subcommands" || got.PlanPhaseStatus != "pending" {
		t.Fatalf("unexpected phase launch context: %#v", got)
	}
	launchPrompt := strings.ToLower(got.InitialPrompt)
	for _, want := range []string{"implement only", "selected phase", "p2", "cli subcommands", "pending", "read the plan file"} {
		if !strings.Contains(launchPrompt, want) {
			t.Fatalf("phase prompt missing %q: %q", want, got.InitialPrompt)
		}
	}
}

func TestModel_AKeyLaunchesAgentFromSelectedPlanPhase(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanMarkdownPath: func(string) (string, error) {
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/plans",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Store tracer bullet", Status: "completed", Order: 1},
			{PhaseID: "p2", Title: "CLI subcommands", Status: "pending", Order: 2},
		},
	}})

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if gotPhase := m.SelectedPlanPhaseID(); gotPhase != "p2" {
		t.Fatalf("selected phase = %q, want p2", gotPhase)
	}
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	_, launchCmd := update(m, cmd())
	if launchCmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.PlanPhaseID != "p2" || got.PlanPhaseTitle != "CLI subcommands" || got.PlanPhaseStatus != "pending" {
		t.Fatalf("unexpected phase launch context: %#v", got)
	}
}

func TestModel_XKeyTogglesPlanPhaseRows(t *testing.T) {
	m := plansInRightPane(t, model.New(testRepos()), []planstore.PlanRecord{{
		PlanID: "plan-1",
		Title:  "Implement plans",
		Status: "approved",
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Store tracer bullet", Status: "completed", Order: 1},
		},
	}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatalf("toggle phases returned command %T, want nil", cmd)
	}
	if got := m.ExpandedPlanID(); got != "plan-1" {
		t.Fatalf("expanded plan = %q, want plan-1", got)
	}
	if got := m.SelectedPlanPhaseID(); got != "" {
		t.Fatalf("expanding plan should keep plan row selected, got phase %q", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if got := m.ExpandedPlanID(); got != "" {
		t.Fatalf("second toggle expanded plan = %q, want collapsed", got)
	}
}

func TestModel_IKeyNoOpsWithNoSelectedPlan(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd with no selected plan, got %T", cmd)
	}
}

func TestModel_IKeyWithNoSelectedAgentShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("expected status expiry command without selected agent")
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatal("expected unset-agent status")
	}
}

func TestModel_IKeyLaunchPathFallsBackFromPlanRepoToSelectedRepo(t *testing.T) {
	tests := []struct {
		name     string
		plan     planstore.PlanRecord
		wantRepo string
		wantPath string
	}{
		{
			name:     "plan repo",
			plan:     planstore.PlanRecord{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/plan-repo"},
			wantRepo: "/dev/plan-repo",
			wantPath: "/dev/plan-repo",
		},
		{
			name:     "selected repo",
			plan:     planstore.PlanRecord{PlanID: "plan-1", Title: "Implement plans", Status: "approved"},
			wantRepo: "/dev/alpha",
			wantPath: "/dev/alpha",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got actions.AgentLaunchContext
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:     "codex",
				PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					got = ctx
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{tc.plan}, ListRequest: m.ListRequest(ui.ModePlans)})

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			if cmd != nil {
				t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
			}
			m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("expected submit command")
			}
			_, launchCmd := update(m, cmd())
			if launchCmd == nil {
				t.Fatal("expected launch command")
			}
			if got.RepoPath != tc.wantRepo || got.WorktreePath != tc.wantPath {
				t.Fatalf("launch context repo/path = %q/%q, want %q/%q", got.RepoPath, got.WorktreePath, tc.wantRepo, tc.wantPath)
			}
		})
	}
}

func TestModel_IKeyPlanPathResolverErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "", errors.New("bad plan path") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("expected status expiry command on resolver error")
	}
	if !strings.Contains(m.View(), "bad plan path") {
		t.Fatal("expected resolver error in status")
	}
}

func TestModel_IKeyMissingPlanLaunchPathShowsStatus(t *testing.T) {
	m := model.NewWithOptions(nil, model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("expected status expiry command for missing launch path")
	}
	if !strings.Contains(m.View(), "Cannot determine launch path for this plan") {
		t.Fatal("expected missing launch path status")
	}
}

func TestModel_IKeyLaunchErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("agent unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd opening launch instructions, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	m, launchCmd := update(m, cmd())
	if launchCmd == nil {
		t.Fatal("expected status expiry command on launch error")
	}
	if !strings.Contains(m.View(), "agent unavailable") {
		t.Fatal("expected launch error in status")
	}
}

func TestModel_YKeyCopiesSelectedPlanMarkdownPath(t *testing.T) {
	var copied string
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		CopyToClipboard: func(text string) error {
			copied = text
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	if msg := cmd(); msg != (model.ClipboardResultMsg{}) {
		t.Fatalf("copy command msg = %#v", msg)
	}
	if copied != "/state/wtui/sessions/v1/plans/plan-1/plan.md" {
		t.Fatalf("copied = %q", copied)
	}
}

func TestModel_EKeyEditsSelectedPlanAndRefreshesPlansAfterExit(t *testing.T) {
	var editedPaths []string
	var filters []planstore.PlanFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		EditFile: func(path string) (actions.TerminalLaunchSpec, error) {
			editedPaths = append(editedPaths, path)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			filters = append(filters, filter)
			return []planstore.PlanRecord{
				{PlanID: "plan-1", Title: "Edited plan", Status: "approved", RepoPath: filter.RepoPath},
			}, nil
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	})
	before := listRequests(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("expected edit command")
	}
	assertListRequestsUnchanged(t, before, m)

	m, refreshCmd := update(m, cmd())
	if len(editedPaths) != 1 || editedPaths[0] != "/state/wtui/sessions/v1/plans/plan-1/plan.md" {
		t.Fatalf("edited paths = %#v", editedPaths)
	}
	if refreshCmd == nil {
		t.Fatal("expected plans refresh command after editor exits")
	}
	assertOnlyListRequestAdvanced(t, before, m, ui.ModePlans)

	msg, ok := refreshCmd().(model.PlanResultMsg)
	if !ok {
		t.Fatalf("refresh command returned %T, want PlanResultMsg", msg)
	}
	if msg.ListRequest != m.ListRequest(ui.ModePlans) {
		t.Fatalf("refresh ListRequest = %d, want %d", msg.ListRequest, m.ListRequest(ui.ModePlans))
	}
	if len(filters) != 1 || filters[0].RepoPath != "/dev/alpha" {
		t.Fatalf("refresh filters = %#v", filters)
	}
}

func TestModel_EKeyPlanEditorErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		EditFile: func(string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("editor unavailable")
		},
	})
	m = plansInRightPane(t, m, []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("expected status expiry command on editor error")
	}
	if !strings.Contains(m.View(), "editor unavailable") {
		t.Fatal("expected editor error in status")
	}
}

func TestModel_StalePlanEditResultDoesNotShowStatusOrRefresh(t *testing.T) {
	m := plansInRightPane(t, model.New(testRepos()), []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	})
	before := listRequests(m)

	m, cmd := update(m, model.PlanEditResultMsg{RepoPath: "/dev/bravo", Err: "editor failed"})
	if cmd != nil {
		t.Fatalf("expected nil cmd for stale edit error, got %T", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
	if strings.Contains(m.View(), "editor failed") {
		t.Fatal("stale edit error should not show in status")
	}

	m, cmd = update(m, model.PlanEditResultMsg{RepoPath: "/dev/bravo"})
	if cmd != nil {
		t.Fatalf("expected nil cmd for stale edit success, got %T", cmd)
	}
	assertListRequestsUnchanged(t, before, m)
}

func TestModel_YKeyUsesDefaultPlanMarkdownPathResolver(t *testing.T) {
	root := t.TempDir()
	var copied string
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: root,
		CopyToClipboard: func(text string) error {
			copied = text
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	_ = cmd()
	want := filepath.Join(root, "plans", "plan-1", "plan.md")
	if copied != want {
		t.Fatalf("copied = %q, want %q", copied, want)
	}
}

func TestModel_YKeyNoOpsWithNoSelectedPlan(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd with no selected plan, got %T", cmd)
	}
}

func TestModel_YKeyPlanPathResolverErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(string) (string, error) { return "", errors.New("cannot resolve plan path") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected status expiry command on resolver error")
	}
	if !strings.Contains(m.View(), "cannot resolve plan path") {
		t.Fatal("expected resolver error in status")
	}
}

func TestModel_YKeyPlanClipboardErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		CopyToClipboard:  func(string) error { return errors.New("clipboard unavailable") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "clipboard unavailable") {
		t.Fatal("expected clipboard error in status")
	}
}

func TestModel_YKeyHistoryAndReflogCopyUseInjectedClipboard(t *testing.T) {
	t.Run("history", func(t *testing.T) {
		var copied string
		m := model.NewWithOptions(testRepos(), model.Options{
			CopyToClipboard: func(text string) error {
				copied = text
				return nil
			},
		})
		m = inRightPane(m)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
		m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()[:1], ListRequest: m.ListRequest(ui.ModeHistory)})

		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd == nil {
			t.Fatal("expected copy command")
		}
		_ = cmd()
		if copied != testCommits()[0].Hash {
			t.Fatalf("copied = %q, want %q", copied, testCommits()[0].Hash)
		}
	})
	t.Run("reflog", func(t *testing.T) {
		var copied string
		m := model.NewWithOptions(testRepos(), model.Options{
			CopyToClipboard: func(text string) error {
				copied = text
				return nil
			},
		})
		m = inRightPane(m)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()[:1], ListRequest: m.ListRequest(ui.ModeReflog)})

		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd == nil {
			t.Fatal("expected copy command")
		}
		_ = cmd()
		if copied != testReflogs()[0].Hash {
			t.Fatalf("copied = %q, want %q", copied, testReflogs()[0].Hash)
		}
	})
}
