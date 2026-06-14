package model

import (
	"strconv"
	"strings"

	"github.com/brian-bell/wtui/flowstore"
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
	switch phaseID {
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
	lines := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
