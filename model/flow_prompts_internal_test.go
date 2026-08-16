package model

import (
	"regexp"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

// bareApproachCommand matches an agent-facing `approach flow`/`approach plan`
// invocation that is not preceded by a pinned path. A launched agent resolving
// `approach` from ambient PATH can be a different build from the launcher, which
// is the failure this whole Flow exists to close, so no generated prompt may
// contain one.
var bareApproachCommand = regexp.MustCompile(`(^|[^/\w-])approach (flow|plan) `)

func promptPhaseKinds() []flowstore.FlowPhase {
	return []flowstore.FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan},
		{PhaseID: "plan-review", Title: "Plan Review", Kind: flowstore.KindPlanReview},
		{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation},
		{PhaseID: "review-loop", Title: "Review Loop", Kind: flowstore.KindReviewLoop},
		{PhaseID: "pr-creation", Title: "PR Creation", Kind: flowstore.KindPRCreation},
		{PhaseID: "autoreview", Title: "Autoreview", Kind: flowstore.KindAutoreview},
		{PhaseID: "merge", Title: "Merge", Kind: flowstore.KindMerge},
		{PhaseID: "custom", Title: "Custom", Kind: ""},
	}
}

func promptRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Prevent schema drift",
		Instructions: "Do the work.",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktree",
		Branch:       "flow/alpha",
		BaseRef:      "main",
		Commit:       "abcdef",
		PlanID:       "plan-1",
		PlanPath:     "/state/plans/plan-1/plan.md",
	}
}

func TestGeneratedPhasePromptsNeverNameABareApproachBinary(t *testing.T) {
	record := promptRecord()
	pinned := "/state/approach/sessions/v1/bin/approach-abc123"
	for _, phase := range promptPhaseKinds() {
		for _, status := range []string{flowstore.PhaseReady, flowstore.PhaseNeedsAttention, flowstore.PhaseBlocked} {
			phase := phase
			phase.Status = status
			t.Run(phase.PhaseID+"/"+status, func(t *testing.T) {
				for name, binary := range map[string]string{"pinned": pinned, "unpinned": ""} {
					prompt := flowPhasePrompt(record, phase, record.PlanPath, "", FlowPromptTemplates{}, binary)
					if match := bareApproachCommand.FindString(prompt); match != "" {
						t.Fatalf("%s prompt names a bare approach command %q:\n%s", name, strings.TrimSpace(match), prompt)
					}
					if name == "pinned" && strings.Contains(prompt, "approach flow") && !strings.Contains(prompt, pinned) {
						t.Fatalf("pinned prompt does not interpolate the pinned path:\n%s", prompt)
					}
				}
			})
		}
	}
}

func TestGeneratedPhasePromptsInterpolateThePinnedBinary(t *testing.T) {
	record := promptRecord()
	record.PR = flowstore.PullRequest{Provider: "github", Number: 7, URL: "https://example.test/pr/7", HeadBranch: "flow/alpha", BaseBranch: "main"}
	pinned := "/state/approach/sessions/v1/bin/approach-abc123"
	phase := flowstore.FlowPhase{PhaseID: "merge", Title: "Merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady}

	prompt := flowPhasePrompt(record, phase, record.PlanPath, "", FlowPromptTemplates{}, pinned)
	if !strings.Contains(prompt, pinned+" flow phase set") {
		t.Fatalf("merge prompt does not run the pinned binary:\n%s", prompt)
	}
	if !strings.Contains(prompt, pinned+" flow merge set") {
		t.Fatalf("merge prompt does not run the pinned binary for merge set:\n%s", prompt)
	}
}

func TestUnpinnedPromptsFallBackToTheSkillResolvedBinary(t *testing.T) {
	record := promptRecord()
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseNeedsAttention}

	prompt := flowPhasePrompt(record, phase, "", "", FlowPromptTemplates{}, "")
	if !strings.Contains(prompt, flowPromptBinaryFallback+" flow phase restart") {
		t.Fatalf("unpinned prompt does not fall back to %s:\n%s", flowPromptBinaryFallback, prompt)
	}
}

func TestFlowPromptBinaryQuotesPathsThatNeedIt(t *testing.T) {
	tests := map[string]string{
		"":                          flowPromptBinaryFallback,
		"   ":                       flowPromptBinaryFallback,
		"/state/bin/approach-abc":   "/state/bin/approach-abc",
		"/Users/a b/bin/approach":   "'/Users/a b/bin/approach'",
		"/state/$(whoami)/approach": "'/state/$(whoami)/approach'",
	}
	for input, want := range tests {
		if got := flowPromptBinary(input); got != want {
			t.Fatalf("flowPromptBinary(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFlowRepairPromptRunsThePinnedBinary(t *testing.T) {
	pinned := "/state/approach/sessions/v1/bin/approach-abc123"
	prompt := flowRepairPrompt(promptRecord(), flowRepairObstruction{Description: "stale launch"}, pinned)
	if match := bareApproachCommand.FindString(prompt); match != "" {
		t.Fatalf("repair prompt names a bare approach command %q:\n%s", strings.TrimSpace(match), prompt)
	}
	if !strings.Contains(prompt, pinned+" flow read") {
		t.Fatalf("repair prompt does not run the pinned binary:\n%s", prompt)
	}
}

func TestFlowPlanPromptRunsThePinnedBinary(t *testing.T) {
	pinned := "/state/approach/sessions/v1/bin/approach-abc123"
	phase := flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}
	prompt := flowPlanPrompt(promptRecord(), phase, FlowPromptTemplates{}, pinned)
	if match := bareApproachCommand.FindString(prompt); match != "" {
		t.Fatalf("plan prompt names a bare approach command %q:\n%s", strings.TrimSpace(match), prompt)
	}
	if !strings.Contains(prompt, pinned+" plan save") {
		t.Fatalf("plan prompt does not run the pinned binary:\n%s", prompt)
	}
}
