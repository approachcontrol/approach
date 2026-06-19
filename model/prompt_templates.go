package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

const promptTemplateEditorVisibleLines = 16

type promptTemplateTarget struct {
	Section string
	Key     string
	Title   string
}

var promptTemplateTargets = []promptTemplateTarget{
	{Section: "agent", Key: "plan_prompt", Title: "Plan launch"},
	{Section: "flow_prompts", Key: "plan", Title: "Flow plan"},
	{Section: "flow_prompts", Key: "plan_review", Title: "Plan review"},
	{Section: "flow_prompts", Key: "implementation", Title: "Implementation"},
	{Section: "flow_prompts", Key: "review_loop", Title: "Review loop"},
	{Section: "flow_prompts", Key: "pr_creation", Title: "PR creation"},
	{Section: "flow_prompts", Key: "autoreview", Title: "Autoreview"},
	{Section: "flow_prompts", Key: "merge", Title: "Merge"},
	{Section: "flow_prompts", Key: "generic", Title: "Generic"},
}

func (m Model) handlePromptTemplates() (tea.Model, tea.Cmd) {
	return m.openPromptTemplatePicker(0), nil
}

func (m Model) openPromptTemplatePicker(selected int) Model {
	m.modal = modal.OpenSelectWithLayout(
		ui.PromptTemplateSelectPrompt,
		m.promptTemplateSelectItems(),
		selected,
		modal.Layout{Width: 42, Height: len(promptTemplateTargets) + 3, Placement: modal.PlacementCenter},
		func(value string) tea.Cmd {
			return func() tea.Msg { return promptTemplateEditRequestedMsg{Value: value} }
		},
	)
	return m
}

func (m Model) handlePromptTemplateModalKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	view := m.modal.View()
	if view.Kind != modal.Select || view.Prompt != ui.PromptTemplateSelectPrompt {
		return m, nil, false
	}
	switch msg.String() {
	case "r":
		target, ok := selectedPromptTemplateTarget(view)
		if !ok {
			return m, nil, true
		}
		return m, m.resetPromptTemplateCommand(target), true
	case "v":
		target, ok := selectedPromptTemplateTarget(view)
		if !ok {
			return m, nil, true
		}
		m.modal = modal.OpenText(m.builtInPromptTemplatePreview(target))
		return m, nil, true
	default:
		return m, nil, false
	}
}

func selectedPromptTemplateTarget(view modal.View) (promptTemplateTarget, bool) {
	if view.SelectIndex < 0 || view.SelectIndex >= len(view.SelectItems) {
		return promptTemplateTarget{}, false
	}
	return promptTemplateTargetByValue(view.SelectItems[view.SelectIndex].Value)
}

func (m Model) handlePromptTemplateEditRequested(msg promptTemplateEditRequestedMsg) Model {
	target, ok := promptTemplateTargetByValue(msg.Value)
	if !ok {
		return m.setStatus(statusOther, "Prompt template is unavailable")
	}
	m.modal = modal.OpenRawMultiLineInput(
		"Edit "+target.Title,
		"prompt template",
		m.promptTemplateValue(target),
		nil,
		func(input string) tea.Cmd {
			if strings.TrimSpace(input) == "" {
				return m.resetPromptTemplateCommand(target)
			}
			return m.savePromptTemplateCommand(target, input)
		},
	).WithInputHeight(promptTemplateEditorVisibleLines)
	return m
}

func (m Model) savePromptTemplateCommand(target promptTemplateTarget, value string) tea.Cmd {
	return func() tea.Msg {
		if err := m.savePromptTemplate(target.Section, target.Key, value); err != nil {
			return PromptTemplateSaveFailedMsg{Section: target.Section, Key: target.Key, Value: value, Err: err.Error()}
		}
		return PromptTemplateSavedMsg{Section: target.Section, Key: target.Key, Value: value}
	}
}

func (m Model) resetPromptTemplateCommand(target promptTemplateTarget) tea.Cmd {
	return func() tea.Msg {
		if err := m.resetPromptTemplate(target.Section, target.Key); err != nil {
			return PromptTemplateResetFailedMsg{Section: target.Section, Key: target.Key, Err: err.Error()}
		}
		return PromptTemplateResetMsg{Section: target.Section, Key: target.Key}
	}
}

func (m Model) handlePromptTemplateSaved(msg PromptTemplateSavedMsg) Model {
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, "Prompt template is unavailable")
	}
	m = m.withPromptTemplateValue(target, msg.Value)
	m = m.clearStatus(statusOther)
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(target))
}

func (m Model) handlePromptTemplateSaveFailed(msg PromptTemplateSaveFailedMsg) Model {
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if ok {
		m = m.openPromptTemplatePicker(promptTemplateTargetIndex(target))
	}
	errText := msg.Err
	if errText == "" {
		errText = "Unable to persist prompt template"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) handlePromptTemplateReset(msg PromptTemplateResetMsg) Model {
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, "Prompt template is unavailable")
	}
	m = m.withPromptTemplateValue(target, "")
	m = m.clearStatus(statusOther)
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(target))
}

func (m Model) handlePromptTemplateResetFailed(msg PromptTemplateResetFailedMsg) Model {
	errText := msg.Err
	if errText == "" {
		errText = "Unable to reset prompt template"
	}
	return m.setStatus(statusOther, errText)
}

func (m Model) promptTemplateSelectItems() []modal.SelectItem {
	items := make([]modal.SelectItem, 0, len(promptTemplateTargets))
	for _, target := range promptTemplateTargets {
		status := "default"
		if strings.TrimSpace(m.promptTemplateValue(target)) != "" {
			status = "custom"
		}
		items = append(items, modal.SelectItem{
			Label: fmt.Sprintf("%-16s %s", target.Title, status),
			Value: promptTemplateTargetValue(target),
		})
	}
	return items
}

func promptTemplateTargetValue(target promptTemplateTarget) string {
	return target.Section + "." + target.Key
}

func promptTemplateTargetByValue(value string) (promptTemplateTarget, bool) {
	for _, target := range promptTemplateTargets {
		if promptTemplateTargetValue(target) == value {
			return target, true
		}
	}
	return promptTemplateTarget{}, false
}

func promptTemplateTargetBySectionKey(section, key string) (promptTemplateTarget, bool) {
	for _, target := range promptTemplateTargets {
		if target.Section == section && target.Key == key {
			return target, true
		}
	}
	return promptTemplateTarget{}, false
}

func promptTemplateTargetIndex(target promptTemplateTarget) int {
	for i, candidate := range promptTemplateTargets {
		if candidate.Section == target.Section && candidate.Key == target.Key {
			return i
		}
	}
	return 0
}

func (m Model) promptTemplateValue(target promptTemplateTarget) string {
	if target.Section == "agent" && target.Key == "plan_prompt" {
		return m.planPromptTemplate
	}
	if target.Section != "flow_prompts" {
		return ""
	}
	switch target.Key {
	case "plan":
		return m.flowPromptTemplates.Plan
	case "plan_review":
		return m.flowPromptTemplates.PlanReview
	case "implementation":
		return m.flowPromptTemplates.Implementation
	case "review_loop":
		return m.flowPromptTemplates.ReviewLoop
	case "pr_creation":
		return m.flowPromptTemplates.PRCreation
	case "autoreview":
		return m.flowPromptTemplates.Autoreview
	case "merge":
		return m.flowPromptTemplates.Merge
	case "generic":
		return m.flowPromptTemplates.Generic
	default:
		return ""
	}
}

func (m Model) withPromptTemplateValue(target promptTemplateTarget, value string) Model {
	if target.Section == "agent" && target.Key == "plan_prompt" {
		m.planPromptTemplate = value
		return m
	}
	if target.Section != "flow_prompts" {
		return m
	}
	switch target.Key {
	case "plan":
		m.flowPromptTemplates.Plan = value
	case "plan_review":
		m.flowPromptTemplates.PlanReview = value
	case "implementation":
		m.flowPromptTemplates.Implementation = value
	case "review_loop":
		m.flowPromptTemplates.ReviewLoop = value
	case "pr_creation":
		m.flowPromptTemplates.PRCreation = value
	case "autoreview":
		m.flowPromptTemplates.Autoreview = value
	case "merge":
		m.flowPromptTemplates.Merge = value
	case "generic":
		m.flowPromptTemplates.Generic = value
	}
	return m
}

func (m Model) builtInPromptTemplatePreview(target promptTemplateTarget) string {
	if target.Section == "agent" && target.Key == "plan_prompt" {
		return defaultImplementationPrompt(planstore.PlanRecord{
			PlanID: "{plan_id}",
			Title:  "{title}",
		}, "{plan_path}")
	}

	record := flowstore.FlowRecord{
		FlowID:       "{flow_id}",
		Title:        "{flow_title}",
		Instructions: "{instructions}",
		RepoPath:     "{repo_path}",
		WorktreePath: "{worktree_path}",
		Branch:       "{branch}",
		Commit:       "{commit}",
		BaseRef:      "{base_ref}",
		PlanID:       "{plan_id}",
		PlanPath:     "{plan_path}",
		PR: flowstore.PullRequest{
			Provider:   "{pr_provider}",
			Number:     123,
			URL:        "{pr_url}",
			HeadBranch: "{pr_head}",
			BaseBranch: "{pr_base}",
			Status:     "{pr_status}",
		},
	}
	if target.Key == "plan" {
		return flowPlanPrompt(record, FlowPromptTemplates{})
	}
	phase := flowstore.FlowPhase{PhaseID: flowPhaseIDForPromptTemplateKey(target.Key), Title: "{phase_title}"}
	return flowPhasePrompt(record, phase, "{plan_path}", "{plan_body}", FlowPromptTemplates{})
}

func flowPhaseIDForPromptTemplateKey(key string) string {
	switch key {
	case "plan_review":
		return "plan-review"
	case "review_loop":
		return "review-loop"
	case "pr_creation":
		return "pr-creation"
	case "generic":
		return "{phase_id}"
	default:
		return key
	}
}
