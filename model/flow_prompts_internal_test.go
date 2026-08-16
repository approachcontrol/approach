package model

import (
	"os"
	"os/exec"
	"path/filepath"
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
			// planPath is a dimension, not a constant. Several prompts branch on
			// it — flowImplementationWithoutPlanPrompt is reachable ONLY when it
			// is empty — so a table that always passed a plan path would leave
			// those branches unrendered and the guard would not cover them.
			for _, planPath := range []string{record.PlanPath, ""} {
				name := phase.PhaseID + "/" + status
				if planPath == "" {
					name += "/no-plan"
				}
				t.Run(name, func(t *testing.T) {
					requirePromptNamesOnlyItsResolvedBinary(t, record, phase, planPath, pinned)
				})
			}
		}
	}
}

func requirePromptNamesOnlyItsResolvedBinary(t *testing.T, record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, pinned string) {
	t.Helper()
	unpinnedPrompt := flowPhasePrompt(record, phase, planPath, "", FlowPromptTemplates{}, "")
	pinnedPrompt := flowPhasePrompt(record, phase, planPath, "", FlowPromptTemplates{}, pinned)
	for name, prompt := range map[string]string{"pinned": pinnedPrompt, "unpinned": unpinnedPrompt} {
		if match := bareApproachCommand.FindString(prompt); match != "" {
			t.Fatalf("%s prompt names a bare approach command %q:\n%s", name, strings.TrimSpace(match), prompt)
		}
	}
	// The bare-command matcher alone cannot catch the regression that
	// matters: `$APPROACH_BIN flow phase set` is not a bare
	// `approach`, but APPROACH_BIN is a shell variable the skills set,
	// NOT an exported one, so in a pinned prompt it expands to nothing
	// and the command word silently shifts. A pinned launch must name
	// the pinned path everywhere the unpinned one named the fallback.
	if strings.Contains(pinnedPrompt, flowPromptBinaryFallback) {
		t.Fatalf("pinned prompt emits the unpinned fallback %s:\n%s", flowPromptBinaryFallback, pinnedPrompt)
	}
	wantPinned := strings.Count(unpinnedPrompt, flowPromptBinaryFallback)
	if got := strings.Count(pinnedPrompt, pinned); got != wantPinned {
		t.Fatalf("pinned prompt names the pinned path %d time(s), want %d (one per fallback in the unpinned rendering):\n%s",
			got, wantPinned, pinnedPrompt)
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

// A configured template is the user's own text, so it cannot be rewritten to
// name the pin — but leaving it with no way to name anything except a bare
// `approach` means a supported prompt path routes the agent back to ambient
// PATH. The placeholder is what closes that, and it has to resolve through the
// same flowPromptBinary the built-in prompts use, including the unpinned
// fallback.
func TestConfiguredTemplatesResolveThePinnedBinary(t *testing.T) {
	record := promptRecord()
	pinned := "/Users/a b/state/bin/approach-abc123"
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady}
	templates := FlowPromptTemplates{Implementation: "Run {approach_bin} flow phase complete --flow-id {flow_id}."}

	prompt := flowPhasePrompt(record, phase, record.PlanPath, "", templates, pinned)
	if want := "Run '" + pinned + "' flow phase complete --flow-id flow-1."; !strings.Contains(prompt, want) {
		t.Fatalf("configured template did not resolve the pinned binary:\n%s", prompt)
	}
	if match := bareApproachCommand.FindString(prompt); match != "" {
		t.Fatalf("configured template still names a bare approach command %q:\n%s", strings.TrimSpace(match), prompt)
	}

	unpinned := flowPhasePrompt(record, phase, record.PlanPath, "", templates, "")
	if !strings.Contains(unpinned, flowPromptBinaryFallback+" flow phase complete") {
		t.Fatalf("unpinned template did not fall back to %s:\n%s", flowPromptBinaryFallback, unpinned)
	}
}

// The Plan phase renders through flowPlanPrompt, which returns its configured
// template on a different branch from every other kind.
func TestConfiguredPlanTemplateResolvesThePinnedBinary(t *testing.T) {
	pinned := "/state/bin/approach-abc123"
	phase := flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}
	templates := FlowPromptTemplates{Plan: "Save it with {approach_bin} plan save."}

	prompt := flowPlanPrompt(promptRecord(), phase, templates, pinned)
	if !strings.Contains(prompt, pinned+" plan save") {
		t.Fatalf("configured plan template did not resolve the pinned binary:\n%s", prompt)
	}
}

// The unpinned fallback is pasted into a shell by an agent, and that agent
// inherits the ambient environment of the TUI that launched it. APPROACH_BIN is
// an ordinary name a user's shell profile may already export, so if the
// expansion consulted it before APPROACH_EXECUTABLE, a stale value would
// silently outrank the pin — the mixed-build failure the pin exists to stop.
// Run through a real /bin/sh rather than asserting on the string: the ordering
// this protects is shell semantics, not text.
func TestUnpinnedPromptsPreferTheExportedPinOverInheritedShellState(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	pinned, inherited := write("pinned"), write("inherited")

	run := func(env ...string) string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", flowPromptBinaryFallback)
		cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run %q with %v: %v", flowPromptBinaryFallback, env, err)
		}
		return strings.TrimSpace(string(out))
	}

	if got := run("APPROACH_EXECUTABLE="+pinned, "APPROACH_BIN="+inherited); got != "pinned" {
		t.Fatalf("an inherited APPROACH_BIN outranked the exported pin: ran %q", got)
	}
	// With no pin the agent has nothing better than the ambient value, so an
	// inherited APPROACH_BIN is honoured rather than ignored.
	if got := run("APPROACH_BIN=" + inherited); got != "inherited" {
		t.Fatalf("unpinned launch ignored the inherited APPROACH_BIN: ran %q", got)
	}

	// The fallback is a command word, and the values it resolves are arbitrary
	// user-supplied paths. Unquoted, a perfectly valid path with a space
	// word-splits and every generated persistence command dies on an argument
	// the agent never wrote.
	spaced := filepath.Join(dir, "a directory")
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spacedBinary := filepath.Join(spaced, "spaced")
	if err := os.WriteFile(spacedBinary, []byte("#!/bin/sh\necho spaced\n"), 0o755); err != nil {
		t.Fatalf("write spaced binary: %v", err)
	}
	if got := run("APPROACH_EXECUTABLE=" + spacedBinary); got != "spaced" {
		t.Fatalf("a pinned path containing a space did not survive expansion: ran %q", got)
	}
}
