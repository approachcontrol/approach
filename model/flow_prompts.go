package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/approachcontrol/approach/flowstore"
)

const flowPhaseDoneInstruction = "After completing this phase goal, mark this Flow phase done with approach-flow."

// FlowPromptTemplates stores optional launch prompt templates for Flow phases.
// Unknown placeholders are left literal so users can evolve templates safely.
type FlowPromptTemplates struct {
	Plan           string
	PlanReview     string
	Implementation string
	ReviewLoop     string
	PRCreation     string
	Autoreview     string
	Merge          string
	Generic        string
}

func (templates FlowPromptTemplates) templateForPhase(phase flowstore.FlowPhase) string {
	switch flowstore.SemanticKind(phase) {
	case flowstore.KindPlan:
		return templates.Plan
	case flowstore.KindPlanReview:
		return templates.PlanReview
	case flowstore.KindImplementation:
		return templates.Implementation
	case flowstore.KindReviewLoop:
		return templates.ReviewLoop
	case flowstore.KindPRCreation:
		return templates.PRCreation
	case flowstore.KindAutoreview:
		return templates.Autoreview
	case flowstore.KindMerge:
		return templates.Merge
	default:
		return templates.Generic
	}
}

func renderFlowPromptTemplate(template string, record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	phaseTitle := phase.Title
	if phaseTitle == "" {
		phaseTitle = phase.PhaseID
	}
	replacer := strings.NewReplacer(
		"{flow_id}", record.FlowID,
		"{flow_title}", record.Title,
		"{instructions}", record.Instructions,
		"{phase_id}", phase.PhaseID,
		"{phase_title}", phaseTitle,
		"{plan_id}", record.PlanID,
		"{plan_path}", planPath,
		"{plan_body}", planBody,
		"{repo_path}", record.RepoPath,
		"{worktree_path}", record.WorktreePath,
		"{branch}", record.Branch,
		"{commit}", record.Commit,
		"{base_ref}", record.BaseRef,
		"{issue_provider}", record.Issue.Provider,
		"{issue_number}", issueNumberPlaceholder(record.Issue.Number),
		"{issue_url}", record.Issue.URL,
		"{pr_provider}", record.PR.Provider,
		"{pr_number}", prNumberPlaceholder(record.PR.Number),
		"{pr_url}", record.PR.URL,
		"{pr_head}", record.PR.HeadBranch,
		"{pr_base}", record.PR.BaseBranch,
		"{pr_status}", record.PR.Status,
	)
	return replacer.Replace(template)
}

func issueNumberPlaceholder(number int) string {
	if number == 0 {
		return ""
	}
	return strconv.Itoa(number)
}

func prNumberPlaceholder(number int) string {
	if number == 0 {
		return ""
	}
	return strconv.Itoa(number)
}

func ensureFlowPhaseDoneInstruction(prompt, guardSource string) string {
	guard := guardSource
	if strings.TrimSpace(guard) == "" {
		guard = prompt
	}
	if lastNonEmptyPromptLine(guard) == flowPhaseDoneInstruction {
		return strings.TrimRight(prompt, " \t\r\n")
	}
	return strings.TrimRight(prompt, " \t\r\n") + "\n\n" + flowPhaseDoneInstruction
}

func lastNonEmptyPromptLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSuffix(lines[i], "\r")
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// flowPromptBinaryFallback is what a generated prompt names when, and only
// when, the launch carries no pinned executable. The bundled skills export
// APPROACH_BIN from APPROACH_EXECUTABLE and fall back to PATH themselves, so an
// unpinned prompt still runs, while a bare `approach` — which resolves to
// whatever build is on PATH, not the one that launched the agent — never
// appears in either case.
//
// It is the self-healing `:=` form, matching the skills, and not a bare
// `$APPROACH_BIN`: APPROACH_BIN is a shell variable the skills set, NOT an
// exported environment variable, so a bare reference expands to nothing in any
// shell that has not run the skill's resolution block and the command word
// silently shifts. `:=` re-resolves from the exported APPROACH_EXECUTABLE, or
// PATH, on first use.
//
// Nothing may interpolate this constant directly either. Every prompt goes
// through the `bin` that flowPhasePrompt derives, so a pinned launch names its
// pinned path everywhere or nowhere.
// TestGeneratedPhasePromptsNeverNameABareApproachBinary pins that.
const flowPromptBinaryFallback = "${APPROACH_BIN:=${APPROACH_EXECUTABLE:-approach}}"

// flowPromptBinary renders executable as a shell command word. A prompt is
// copied into a shell by the agent, so a path containing spaces or shell
// metacharacters has to be quoted rather than pasted raw.
func flowPromptBinary(executable string) string {
	trimmed := strings.TrimSpace(executable)
	if trimmed == "" {
		return flowPromptBinaryFallback
	}
	if flowPromptBinarySafe.MatchString(trimmed) {
		return trimmed
	}
	return "'" + strings.ReplaceAll(trimmed, "'", `'\''`) + "'"
}

var flowPromptBinarySafe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// flowPhasePrompt builds a phase's launch prompt. binary is the pinned approach
// executable for this launch; see flowPromptBinary for the unpinned case.
func flowPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates, binary string) string {
	if template := templates.templateForPhase(phase); strings.TrimSpace(template) != "" {
		prompt := renderFlowPromptTemplate(template, record, phase, planPath, planBody)
		return ensureFlowPhaseDoneInstruction(prompt, template)
	}
	bin := flowPromptBinary(binary)
	var prompt string
	switch flowstore.SemanticKind(phase) {
	case flowstore.KindPlan:
		prompt = flowPlanPrompt(record, phase, templates, binary)
	case flowstore.KindPlanReview:
		prompt = flowPlanReviewPrompt(record, phase, planPath, planBody, bin)
	case flowstore.KindImplementation:
		prompt = flowImplementationPrompt(record, phase, planPath, planBody, bin)
	case flowstore.KindReviewLoop:
		prompt = flowReviewLoopPrompt(record, phase, planPath, planBody, bin)
	case flowstore.KindPRCreation:
		prompt = flowPRCreationPrompt(record, phase, planPath, planBody, bin)
	case flowstore.KindAutoreview:
		prompt = flowAutoreviewPrompt(record, phase, planPath, planBody, bin)
	case flowstore.KindMerge:
		prompt = flowMergePrompt(record, phase, planPath, planBody, bin)
	default:
		prompt = flowGenericPhasePrompt(record, phase, planPath, planBody, bin)
	}
	return ensureFlowPhaseDoneInstruction(prompt, "")
}

func flowPhasePromptNeedsPlanBody(phase flowstore.FlowPhase) bool {
	switch flowstore.SemanticKind(phase) {
	case flowstore.KindPlan, flowstore.KindPlanReview, flowstore.KindImplementation, flowstore.KindReviewLoop, flowstore.KindPRCreation, flowstore.KindAutoreview, flowstore.KindMerge:
		return false
	default:
		return true
	}
}

func flowPlanReviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	return flowMinimalArtifactPrompt("Use the review-loop skill to review the saved plan, max 6 loops.\nUse the approach-flow skill to record the Plan Review verdict before finishing; the phase is not done until the verdict is persisted.", planPath, record, phase, bin)
}

func flowImplementationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	if strings.TrimSpace(planPath) == "" {
		return flowImplementationWithoutPlanPrompt(record, phase, bin)
	}
	return flowMinimalArtifactPrompt("Implement the approved plan.\nUse the commit skill before completing this phase.", planPath, record, phase, bin)
}

func flowImplementationWithoutPlanPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, bin string) string {
	var b strings.Builder
	b.WriteString("Implement the Flow instructions.\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowPromptHeader(&b, record, "")
	writeFlowPromptPlanContext(&b, record, "")
	writeFlowPromptPhaseSummaryByKind(&b, record, "Plan Review context", flowstore.KindPlanReview)
	writeFlowRestartPromptIfNeeded(&b, record, phase, bin)
	b.WriteString("\nUse the commit skill before completing this phase.")
	b.WriteString("\nAdvance this phase with `" + bin + " flow phase set` only after the implementation is complete, blocked, or needs attention.")
	return b.String()
}

func flowReviewLoopPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	return flowMinimalChangePrompt("Use the review-loop workflow with goal: review-and-revise.\nUse the commit skill when revisions are made.\nUse the approach-flow skill to record the Review Loop result before finishing; the phase is not done until the result is persisted.", record, phase, bin)
}

func flowPRCreationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	head := strings.TrimSpace(record.Branch)
	if head == "" {
		head = "<head>"
	}
	base := strings.TrimSpace(record.BaseRef)
	if base == "" {
		base = "<base>"
	}
	instruction := fmt.Sprintf("Use the ship skill to create a PR for the changes.\nAfter the PR exists, run `%s flow pr set --flow-id %s --provider github --number <number> --url <url> --head %s --base %s` before completing this phase.", bin, record.FlowID, head, base)
	return flowMinimalChangePrompt(instruction, record, phase, bin)
}

func flowMinimalArtifactPrompt(instruction, planPath string, record flowstore.FlowRecord, phase flowstore.FlowPhase, bin string) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\nPlan: ")
	b.WriteString(planPath)
	b.WriteString("\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase, bin)
	return b.String()
}

func flowAutoreviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	var b strings.Builder
	b.WriteString("Use the autoreview skill for this second-level review.\n")
	b.WriteString("Use the ship skill when fixes require commits or pushes.\n")
	b.WriteString("Use the approach-flow skill to record the Autoreview result before finishing; the phase is not done until the result is persisted.\n\n")
	writeFlowChangeMetadata(&b, record)
	if flowstore.HasPRTarget(record.PR) {
		fmt.Fprintf(&b, "\nPR target:\n- PR: %s #%d\n- URL: %s\n- Head: %s\n- Base: %s", record.PR.Provider, record.PR.Number, record.PR.URL, record.PR.HeadBranch, record.PR.BaseBranch)
		if record.PR.Status != "" {
			fmt.Fprintf(&b, "\n- Status: %s", record.PR.Status)
		}
	} else {
		b.WriteString("\nPR target: missing. Do not run Autoreview until `" + bin + " flow pr set` records provider, number, URL, head, and base.\n")
	}
	return b.String()
}

func writeFlowRestartPromptIfNeeded(b *strings.Builder, record flowstore.FlowRecord, phase flowstore.FlowPhase, bin string) {
	if phase.Status != flowstore.PhaseNeedsAttention && phase.Status != flowstore.PhaseBlocked {
		return
	}
	fmt.Fprintf(b, "\nRestart required: this phase is %s. Before marking it completed, record the rerun:\n", phase.Status)
	fmt.Fprintf(b, "%s flow phase restart --flow-id %s --phase-id %s --notes \"Rerunning %s after addressing prior findings.\"\n", bin, record.FlowID, phase.PhaseID, phase.Title)
}

func flowMergePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	var b strings.Builder
	b.WriteString("Merge the PR deliberately.\n\n")
	writeFlowChangeMetadata(&b, record)
	if flowstore.HasPRTarget(record.PR) {
		fmt.Fprintf(&b, "\n\nPR target:\n- PR: %s #%d\n- URL: %s\n- Head: %s\n- Base: %s\n", record.PR.Provider, record.PR.Number, record.PR.URL, record.PR.HeadBranch, record.PR.BaseBranch)
		if record.PR.Status != "" {
			fmt.Fprintf(&b, "- Status: %s\n", record.PR.Status)
		}
	} else {
		b.WriteString("\n\nPR target: missing. Do not merge until `" + bin + " flow pr set` records provider, number, URL, head, and base.\n")
	}
	writeFlowRestartPromptIfNeeded(&b, record, phase, bin)
	fmt.Fprintf(&b, "\nmerged:\n%s flow phase set --flow-id %s --phase-id %s --status completed --outcome merged --summary \"...\"\n", bin, record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "%s flow merge set --flow-id %s --status merged --commit <merge-commit> --merged-at <rfc3339>\n\n", bin, record.FlowID)
	fmt.Fprintf(&b, "blocked:\n%s flow phase set --flow-id %s --phase-id %s --status blocked --outcome blocked --notes \"...\"\n", bin, record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "%s flow merge set --flow-id %s --status blocked", bin, record.FlowID)
	return b.String()
}

func flowMinimalChangePrompt(instruction string, record flowstore.FlowRecord, phase flowstore.FlowPhase, bin string) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase, bin)
	return b.String()
}

func writeFlowChangeMetadata(b *strings.Builder, record flowstore.FlowRecord) {
	b.WriteString("Worktree: ")
	b.WriteString(record.WorktreePath)
	b.WriteString("\nBranch: ")
	b.WriteString(record.Branch)
	b.WriteString("\nStart commit: ")
	b.WriteString(record.Commit)
	if flowstore.HasIssueTarget(record.Issue) {
		b.WriteString("\nIssue: ")
		b.WriteString(record.Issue.Provider)
		b.WriteString(" #")
		b.WriteString(strconv.Itoa(record.Issue.Number))
		b.WriteString(" ")
		b.WriteString(record.Issue.URL)
	}
}

func flowGenericPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody, bin string) string {
	var b strings.Builder
	b.WriteString("Use the approach-flow skill for this launch.\n\n")
	b.WriteString("Flow phase: ")
	if phase.Title != "" {
		b.WriteString(phase.Title)
	} else {
		b.WriteString(phase.PhaseID)
	}
	b.WriteString(" (")
	b.WriteString(phase.PhaseID)
	b.WriteString(").\n")
	writeFlowPromptHeader(&b, record, planPath)
	writeFlowPromptPlanContext(&b, record, planBody)
	writeFlowRestartPromptIfNeeded(&b, record, phase, bin)
	b.WriteString("\nAdvance this phase with `" + bin + " flow phase set` only after the corresponding work is complete, blocked, or needs attention.")
	return b.String()
}

func writeFlowPromptPhaseSummaryByKind(b *strings.Builder, record flowstore.FlowRecord, title, kind string) {
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	if phase, ok := flowstore.FindPhaseByKind(record, kind); ok {
		writeFlowPhaseContext(b, phase)
		return
	}
	b.WriteString("- Phase: ")
	b.WriteString(kind)
	b.WriteString("\n")
}

func writeFlowPromptHeader(b *strings.Builder, record flowstore.FlowRecord, planPath string) {
	if record.Instructions != "" {
		b.WriteString("\nCustom instructions:\n")
		b.WriteString(record.Instructions)
		b.WriteString("\n")
	}
	if record.PlanID != "" {
		b.WriteString("\nLinked plan: ")
		b.WriteString(record.PlanID)
		if planPath != "" {
			b.WriteString(" at ")
			b.WriteString(planPath)
		}
		b.WriteString("\n")
	}
}

func writeFlowPromptPlanContext(b *strings.Builder, record flowstore.FlowRecord, planBody string) {
	if plan, ok := flowstore.FindPhaseByKind(record, flowstore.KindPlan); ok {
		b.WriteString("\nPrior Plan context:\n")
		writeFlowPhaseContext(b, plan)
	}
	if planBody != "" {
		b.WriteString("\nSaved plan body:\n")
		b.WriteString(planBody)
		if !strings.HasSuffix(planBody, "\n") {
			b.WriteString("\n")
		}
	}
}

func writeFlowPhaseContext(b *strings.Builder, phase flowstore.FlowPhase) {
	if phase.PhaseID != "" {
		b.WriteString("- Phase: ")
		b.WriteString(phase.PhaseID)
		b.WriteString("\n")
	}
	if phase.Title != "" {
		b.WriteString("- Title: ")
		b.WriteString(phase.Title)
		b.WriteString("\n")
	}
	b.WriteString("- Status: ")
	b.WriteString(phase.Status)
	b.WriteString("\n")
	if phase.Outcome != "" {
		b.WriteString("- Outcome: ")
		b.WriteString(phase.Outcome)
		b.WriteString("\n")
	}
	if phase.Summary != "" {
		b.WriteString("- Summary: ")
		b.WriteString(phase.Summary)
		b.WriteString("\n")
	}
	if phase.Notes != "" {
		b.WriteString("- Notes: ")
		b.WriteString(phase.Notes)
		b.WriteString("\n")
	}
}
