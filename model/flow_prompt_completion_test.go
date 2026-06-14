package model_test

import (
	"strings"
	"testing"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model"
)

func TestFlowPlanPromptAppendsPhaseDoneInstruction(t *testing.T) {
	record := flowstore.FlowRecord{
		Instructions: "Build the thing",
	}

	want := strings.Join([]string{
		"Use the wtui-flow skill for this launch.",
		"",
		"Build the thing",
		"",
		"Produce a plan only; do not start coding in this phase.",
		"Create and persist the plan with wtui plan save, link it back with wtui flow plan set, then report Flow persistence failures explicitly before ending.",
		"",
		model.FlowPhaseDoneInstructionForTest(),
	}, "\n")
	if got := model.FlowPlanPromptForTest(record, model.FlowPromptTemplates{}); got != want {
		t.Fatalf("plan prompt = %q, want %q", got, want)
	}
}

func TestFlowPhasePromptsAppendPhaseDoneInstruction(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Instructions: "Build the requested change.",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-build",
		Branch:       "flow/build",
		Commit:       "abc123",
		BaseRef:      "main",
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     42,
			URL:        "https://github.com/brian-bell/wtui/pull/42",
			HeadBranch: "flow/build",
			BaseBranch: "main",
			Status:     "open",
		},
	}
	tests := []struct {
		name     string
		phase    flowstore.FlowPhase
		planPath string
		planBody string
	}{
		{name: "plan-review", phase: flowstore.FlowPhase{PhaseID: "plan-review", Title: "Plan Review"}, planPath: record.PlanPath},
		{name: "implementation with plan", phase: flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation"}, planPath: record.PlanPath},
		{name: "implementation without plan", phase: flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation"}},
		{name: "review-loop", phase: flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop"}},
		{name: "pr-creation", phase: flowstore.FlowPhase{PhaseID: "pr-creation", Title: "PR Creation"}},
		{name: "autoreview", phase: flowstore.FlowPhase{PhaseID: "autoreview", Title: "Autoreview"}},
		{name: "merge", phase: flowstore.FlowPhase{PhaseID: "merge", Title: "Merge"}},
		{name: "generic", phase: flowstore.FlowPhase{PhaseID: "qa-check", Title: "QA Check"}, planPath: record.PlanPath, planBody: "Confirm the release notes."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.FlowPhasePromptForTest(record, tt.phase, tt.planPath, tt.planBody, model.FlowPromptTemplates{})
			assertFinalFlowDoneInstruction(t, got)
		})
	}
}

func TestFlowPromptTemplatesAppendPhaseDoneInstruction(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Instructions: "Build the thing",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/flow-template",
		Branch:       "flow/template",
		Commit:       "c0ffee",
		BaseRef:      "main",
		PR: flowstore.PullRequest{
			Provider:   "github",
			Number:     24,
			URL:        "https://github.com/brian-bell/wtui/pull/24",
			HeadBranch: "flow/template",
			BaseBranch: "main",
			Status:     "open",
		},
	}
	template := "Custom {phase_id} for {flow_id}: {plan_path} at {worktree_path}; keep {unknown}"
	tests := []struct {
		name      string
		phase     flowstore.FlowPhase
		templates model.FlowPromptTemplates
	}{
		{name: "plan", phase: flowstore.FlowPhase{PhaseID: "plan", Title: "Plan"}, templates: model.FlowPromptTemplates{Plan: template}},
		{name: "plan_review", phase: flowstore.FlowPhase{PhaseID: "plan-review", Title: "Plan Review"}, templates: model.FlowPromptTemplates{PlanReview: template}},
		{name: "implementation", phase: flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation"}, templates: model.FlowPromptTemplates{Implementation: template}},
		{name: "review_loop", phase: flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop"}, templates: model.FlowPromptTemplates{ReviewLoop: template}},
		{name: "pr_creation", phase: flowstore.FlowPhase{PhaseID: "pr-creation", Title: "PR Creation"}, templates: model.FlowPromptTemplates{PRCreation: template}},
		{name: "autoreview", phase: flowstore.FlowPhase{PhaseID: "autoreview", Title: "Autoreview"}, templates: model.FlowPromptTemplates{Autoreview: template}},
		{name: "merge", phase: flowstore.FlowPhase{PhaseID: "merge", Title: "Merge"}, templates: model.FlowPromptTemplates{Merge: template}},
		{name: "generic", phase: flowstore.FlowPhase{PhaseID: "qa-check", Title: "QA Check"}, templates: model.FlowPromptTemplates{Generic: template}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if tt.phase.PhaseID == "plan" {
				got = model.FlowPlanPromptForTest(record, tt.templates)
			} else {
				got = model.FlowPhasePromptForTest(record, tt.phase, record.PlanPath, "", tt.templates)
			}
			want := strings.ReplaceAll(template, "{phase_id}", tt.phase.PhaseID)
			want = strings.ReplaceAll(want, "{flow_id}", record.FlowID)
			want = strings.ReplaceAll(want, "{plan_path}", record.PlanPath)
			want = strings.ReplaceAll(want, "{worktree_path}", record.WorktreePath)
			want += "\n\n" + model.FlowPhaseDoneInstructionForTest()
			if got != want {
				t.Fatalf("templated prompt = %q, want %q", got, want)
			}
		})
	}
}

func TestFlowPromptPhaseDoneInstructionDuplicateGuard(t *testing.T) {
	instruction := model.FlowPhaseDoneInstructionForTest()
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		Instructions: "Earlier placeholder says: " + instruction,
		PlanPath:     "/state/plans/plan-1/plan.md",
		WorktreePath: "/dev/alpha-worktrees/flow-template",
		Branch:       "flow/template",
		Commit:       "abc123",
	}

	t.Run("plan template final standalone line", func(t *testing.T) {
		template := "Plan {flow_id}\n\n" + instruction + "  \n\n"
		got := model.FlowPlanPromptForTest(record, model.FlowPromptTemplates{Plan: template})
		if strings.Count(got, instruction) != 1 {
			t.Fatalf("prompt should contain one completion instruction:\n%s", got)
		}
		assertFinalFlowDoneInstruction(t, got)
	})

	t.Run("non-plan template final standalone line", func(t *testing.T) {
		template := "Review {flow_id}\n\n" + instruction + "  \n\n"
		got := model.FlowPhasePromptForTest(record, flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop"}, record.PlanPath, "", model.FlowPromptTemplates{ReviewLoop: template})
		if strings.Count(got, instruction) != 1 {
			t.Fatalf("prompt should contain one completion instruction:\n%s", got)
		}
		assertFinalFlowDoneInstruction(t, got)
	})

	t.Run("earlier occurrence still appends final instruction", func(t *testing.T) {
		template := instruction + "\n\nContinue {phase_id}."
		got := model.FlowPhasePromptForTest(record, flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop"}, record.PlanPath, "", model.FlowPromptTemplates{ReviewLoop: template})
		if strings.Count(got, instruction) != 2 {
			t.Fatalf("prompt should append completion instruction after earlier occurrence:\n%s", got)
		}
		assertFinalFlowDoneInstruction(t, got)
	})

	t.Run("larger final line still appends standalone instruction", func(t *testing.T) {
		template := "Continue, then " + instruction
		got := model.FlowPhasePromptForTest(record, flowstore.FlowPhase{PhaseID: "review-loop", Title: "Review Loop"}, record.PlanPath, "", model.FlowPromptTemplates{ReviewLoop: template})
		if strings.Count(got, instruction) != 2 {
			t.Fatalf("prompt should append standalone completion instruction after larger final line:\n%s", got)
		}
		assertFinalFlowDoneInstruction(t, got)
	})

	t.Run("rendered placeholder occurrence still appends final instruction", func(t *testing.T) {
		template := "Generic body:\n{plan_body}"
		got := model.FlowPhasePromptForTest(record, flowstore.FlowPhase{PhaseID: "qa-check", Title: "QA Check"}, record.PlanPath, instruction, model.FlowPromptTemplates{Generic: template})
		if strings.Count(got, instruction) != 2 {
			t.Fatalf("prompt should append completion instruction when only rendered placeholder ends with it:\n%s", got)
		}
		assertFinalFlowDoneInstruction(t, got)
	})
}

func assertFinalFlowDoneInstruction(t *testing.T, prompt string) {
	t.Helper()
	instruction := model.FlowPhaseDoneInstructionForTest()
	if got := lastNonEmptyLine(prompt); got != instruction {
		t.Fatalf("last non-empty prompt line = %q, want %q\n%s", got, instruction, prompt)
	}
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func appendFlowDoneInstructionForTest(prompt string) string {
	return strings.TrimRight(prompt, " \t\r\n") + "\n\n" + model.FlowPhaseDoneInstructionForTest()
}
