package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/internal/artifacts"
)

const flowPhaseDoneInstruction = "After completing this phase goal, mark this Flow phase done with wtui-flow."

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

func (templates FlowPromptTemplates) templateForPhase(phaseID string) string {
	switch canonicalFlowPromptPhaseID(phaseID) {
	case "plan":
		return templates.Plan
	case "plan-review":
		return templates.PlanReview
	case "implementation":
		return templates.Implementation
	case "review-loop":
		return templates.ReviewLoop
	case "pr-creation":
		return templates.PRCreation
	case "autoreview":
		return templates.Autoreview
	case "merge":
		return templates.Merge
	default:
		return templates.Generic
	}
}

func canonicalFlowPromptPhaseID(phaseID string) string {
	switch normalized := artifacts.NormalizePhaseID(phaseID); normalized {
	case "review-loop-1":
		return "review-loop"
	case "review-loop-2":
		return "autoreview"
	default:
		return normalized
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
		"{pr_provider}", record.PR.Provider,
		"{pr_number}", prNumberPlaceholder(record.PR.Number),
		"{pr_url}", record.PR.URL,
		"{pr_head}", record.PR.HeadBranch,
		"{pr_base}", record.PR.BaseBranch,
		"{pr_status}", record.PR.Status,
	)
	return replacer.Replace(template)
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

func flowPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates) string {
	if template := templates.templateForPhase(phase.PhaseID); strings.TrimSpace(template) != "" {
		prompt := renderFlowPromptTemplate(template, record, phase, planPath, planBody)
		return ensureFlowPhaseDoneInstruction(prompt, template)
	}
	var prompt string
	switch canonicalFlowPromptPhaseID(phase.PhaseID) {
	case "plan-review":
		prompt = flowPlanReviewPrompt(record, phase, planPath, planBody)
	case "implementation":
		prompt = flowImplementationPrompt(record, phase, planPath, planBody)
	case "review-loop":
		prompt = flowReviewLoopPrompt(record, phase, planPath, planBody)
	case "pr-creation":
		prompt = flowPRCreationPrompt(record, phase, planPath, planBody)
	case "autoreview":
		prompt = flowAutoreviewPrompt(record, phase, planPath, planBody)
	case "merge":
		prompt = flowMergePrompt(record, phase, planPath, planBody)
	default:
		prompt = flowGenericPhasePrompt(record, phase, planPath, planBody)
	}
	return ensureFlowPhaseDoneInstruction(prompt, "")
}

func flowPhasePromptNeedsPlanBody(phaseID string) bool {
	switch canonicalFlowPromptPhaseID(phaseID) {
	case "plan-review", "implementation", "review-loop", "pr-creation", "autoreview", "merge":
		return false
	default:
		return true
	}
}

func flowPlanReviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	return flowMinimalArtifactPrompt("Use the review-loop skill to review the saved plan, max 6 loops.\nUse the wtui-flow skill to record the Plan Review verdict before finishing; the phase is not done until the verdict is persisted.", planPath, record, phase)
}

func flowImplementationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	if strings.TrimSpace(planPath) == "" {
		return flowImplementationWithoutPlanPrompt(record, phase)
	}
	return flowMinimalArtifactPrompt("Implement the approved plan.\nUse the commit skill before completing this phase.", planPath, record, phase)
}

func flowImplementationWithoutPlanPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString("Implement the Flow instructions.\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowPromptHeader(&b, record, "")
	writeFlowPromptPlanContext(&b, record, "")
	writeFlowPromptPhaseSummary(&b, record, "Plan Review context", "plan-review")
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	b.WriteString("\nUse the commit skill before completing this phase.")
	b.WriteString("\nAdvance this phase with `wtui flow phase set` only after the implementation is complete, blocked, or needs attention.")
	return b.String()
}

func flowReviewLoopPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	return flowMinimalChangePrompt("Use the review-loop workflow with goal: review-and-revise.\nUse the commit skill when revisions are made.\nUse the wtui-flow skill to record the Review Loop result before finishing; the phase is not done until the result is persisted.", record, phase)
}

func flowPRCreationPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	head := strings.TrimSpace(record.Branch)
	if head == "" {
		head = "<head>"
	}
	base := strings.TrimSpace(record.BaseRef)
	if base == "" {
		base = "<base>"
	}
	instruction := fmt.Sprintf("Use the ship skill to create a PR for the changes.\nAfter the PR exists, run `wtui flow pr set --flow-id %s --provider github --number <number> --url <url> --head %s --base %s` before completing this phase.", record.FlowID, head, base)
	return flowMinimalChangePrompt(instruction, record, phase)
}

func flowMinimalArtifactPrompt(instruction, planPath string, record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\nPlan: ")
	b.WriteString(planPath)
	b.WriteString("\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	return b.String()
}

func flowAutoreviewPrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Use the autoreview skill for this second-level review.\n")
	b.WriteString("Review the local worktree changes, not the pull request.\n")
	b.WriteString("Use the commit skill when local revisions are made.\n")
	b.WriteString("Use the wtui-flow skill to record the Autoreview result before finishing; the phase is not done until the result is persisted.\n\n")
	writeFlowChangeMetadata(&b, record)
	return b.String()
}

func writeFlowRestartPromptIfNeeded(b *strings.Builder, record flowstore.FlowRecord, phase flowstore.FlowPhase) {
	if phase.Status != flowstore.PhaseNeedsAttention && phase.Status != flowstore.PhaseBlocked {
		return
	}
	fmt.Fprintf(b, "\nRestart required: this phase is %s. Before marking it completed, record the rerun:\n", phase.Status)
	fmt.Fprintf(b, "wtui flow phase restart --flow-id %s --phase-id %s --notes \"Rerunning %s after addressing prior findings.\"\n", record.FlowID, phase.PhaseID, phase.Title)
}

func flowMergePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Merge the PR deliberately.\n\n")
	writeFlowChangeMetadata(&b, record)
	if flowstore.HasPRTarget(record.PR) {
		fmt.Fprintf(&b, "\n\nPR target:\n- PR: %s #%d\n- URL: %s\n- Head: %s\n- Base: %s\n", record.PR.Provider, record.PR.Number, record.PR.URL, record.PR.HeadBranch, record.PR.BaseBranch)
		if record.PR.Status != "" {
			fmt.Fprintf(&b, "- Status: %s\n", record.PR.Status)
		}
	} else {
		b.WriteString("\n\nPR target: missing. Do not merge until `wtui flow pr set` records provider, number, URL, head, and base.\n")
	}
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	fmt.Fprintf(&b, "\nmerged:\nwtui flow phase set --flow-id %s --phase-id %s --status completed --outcome merged --summary \"...\"\n", record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "wtui flow merge set --flow-id %s --status merged --commit <merge-commit> --merged-at <rfc3339>\n\n", record.FlowID)
	fmt.Fprintf(&b, "blocked:\nwtui flow phase set --flow-id %s --phase-id %s --status blocked --outcome blocked --notes \"...\"\n", record.FlowID, phase.PhaseID)
	fmt.Fprintf(&b, "wtui flow merge set --flow-id %s --status blocked", record.FlowID)
	return b.String()
}

func flowMinimalChangePrompt(instruction string, record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n")
	writeFlowChangeMetadata(&b, record)
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	return b.String()
}

func writeFlowChangeMetadata(b *strings.Builder, record flowstore.FlowRecord) {
	b.WriteString("Worktree: ")
	b.WriteString(record.WorktreePath)
	b.WriteString("\nBranch: ")
	b.WriteString(record.Branch)
	b.WriteString("\nStart commit: ")
	b.WriteString(record.Commit)
}

func flowGenericPhasePrompt(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string) string {
	var b strings.Builder
	b.WriteString("Use the wtui-flow skill for this launch.\n\n")
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
	writeFlowRestartPromptIfNeeded(&b, record, phase)
	b.WriteString("\nAdvance this phase with `wtui flow phase set` only after the corresponding work is complete, blocked, or needs attention.")
	return b.String()
}

func writeFlowPromptPhaseSummary(b *strings.Builder, record flowstore.FlowRecord, title, phaseID string) {
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	if phase, ok := flowPhaseByID(record, phaseID); ok {
		writeFlowPhaseContext(b, phase)
		return
	}
	b.WriteString("- Phase: ")
	b.WriteString(phaseID)
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
	if plan, ok := flowPhaseByID(record, "plan"); ok {
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
