package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/ui"
)

type promptTemplateTarget struct {
	Section string
	Key     string
	Title   string
}

// promptNote is one piece of modal feedback: text plus severity. Its zero
// value renders no note at all.
type promptNote struct {
	Text string
	Kind modal.NoteKind
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
	{Section: "flow_prompts", Key: "autofix", Title: "Autofix"},
}

func (m Model) handlePromptTemplates() (tea.Model, tea.Cmd) {
	return m.openPromptTemplatePicker(0, promptNote{}), nil
}

// openPromptTemplatePicker rebuilds the picker from scratch, which is why the
// note is a parameter rather than something applied at each call site.
func (m Model) openPromptTemplatePicker(selected int, note promptNote) Model {
	m.modal = modal.OpenSelectWithLayout(
		ui.PromptTemplateSelectPrompt,
		m.promptTemplateSelectItems(),
		selected,
		modal.Layout{
			Width:     ui.PromptPickerWidth,
			Height:    len(promptTemplateTargets) + 3 + ui.PromptPickerNoteRows,
			Placement: modal.PlacementCenter,
		},
		func(value string) tea.Cmd {
			return func() tea.Msg { return promptTemplateEditRequestedMsg{Value: value} }
		},
	).SetSelectNote(note.Text, note.Kind)
	return m
}

func (m Model) handlePromptTemplateModalKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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
		index := promptTemplateTargetIndex(target)
		if strings.TrimSpace(m.promptTemplateValue(target)) == "" {
			return m.openPromptTemplatePicker(index, promptNote{
				Text: target.Title + " is already default",
				Kind: modal.NoteNeutral,
			}), nil, true
		}
		reset := m
		m.modal = modal.OpenConfirm(
			"Reset "+target.Title+" to its built-in default?",
			func() tea.Cmd { return reset.resetPromptTemplateCommand(target, ResetFromPicker, "") },
		).WithCancel(func(modal.View) tea.Cmd {
			return promptTemplatePickerReturn(target, promptNote{
				Text: target.Title + " unchanged",
				Kind: modal.NoteNeutral,
			})
		})
		return m, nil, true
	case "v":
		target, ok := selectedPromptTemplateTarget(view)
		if !ok {
			return m, nil, true
		}
		m.modal = modal.OpenText(m.builtInPromptTemplatePreview(target)).
			WithCancel(func(modal.View) tea.Cmd {
				return promptTemplatePickerReturn(target, promptNote{})
			})
		return m, nil, true
	default:
		return m, nil, false
	}
}

func promptTemplatePickerReturn(target promptTemplateTarget, note promptNote) tea.Cmd {
	return func() tea.Msg {
		return promptTemplatePickerReturnMsg{Target: target, Note: note}
	}
}

func (m Model) handlePromptTemplatePickerReturn(msg promptTemplatePickerReturnMsg) Model {
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(msg.Target), msg.Note)
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
	value := m.promptTemplateValue(target)
	return m.openPromptTemplateEditor(target, value, value, len([]rune(value)), promptNote{})
}

// openPromptTemplateEditor builds the editor for a target. draft is what the
// user sees and original is the persisted value it is compared against; they
// differ only when an editor is reconstructed after a failed write, which is
// exactly when it must open already dirty.
func (m Model) openPromptTemplateEditor(target promptTemplateTarget, draft, original string, cursor int, note promptNote) Model {
	submitModel := m
	m.modal = modal.OpenRawMultiLineInput(
		"Edit "+target.Title,
		"prompt template",
		draft,
		nil,
		func(input string) tea.Cmd {
			// A blank or whitespace-only save keeps its existing meaning:
			// reset to the built-in default.
			if strings.TrimSpace(input) == "" {
				return submitModel.resetPromptTemplateCommand(target, ResetFromEditor, input)
			}
			return submitModel.savePromptTemplateCommand(target, input)
		},
	).AsEditor(modal.EditorSpec{
		Title:    target.Title,
		Identity: promptTemplateIdentity(target, original),
		Original: original,
		Cursor:   cursor,
		Note:     note.Text,
		NoteKind: note.Kind,
	}).WithCancel(func(view modal.View) tea.Cmd {
		if view.Editor.Dirty {
			return promptTemplatePickerReturn(target, promptNote{
				Text: "Discarded changes to " + target.Title,
				Kind: modal.NoteWarning,
			})
		}
		return promptTemplatePickerReturn(target, promptNote{
			Text: "No changes to " + target.Title,
			Kind: modal.NoteNeutral,
		})
	})
	return m
}

func promptTemplateIdentity(target promptTemplateTarget, value string) string {
	return promptTemplateTargetValue(target) + "  " + promptTemplateState(value)
}

func promptTemplateState(value string) string {
	if strings.TrimSpace(value) != "" {
		return "custom"
	}
	return "default"
}

func (m Model) savePromptTemplateCommand(target promptTemplateTarget, value string) tea.Cmd {
	return func() tea.Msg {
		if err := m.savePromptTemplate(target.Section, target.Key, value); err != nil {
			return PromptTemplateSaveFailedMsg{Section: target.Section, Key: target.Key, Value: value, Err: err.Error()}
		}
		return PromptTemplateSavedMsg{Section: target.Section, Key: target.Key, Value: value}
	}
}

func (m Model) resetPromptTemplateCommand(target promptTemplateTarget, origin PromptTemplateResetOrigin, draft string) tea.Cmd {
	return func() tea.Msg {
		if err := m.resetPromptTemplate(target.Section, target.Key); err != nil {
			return PromptTemplateResetFailedMsg{
				Section: target.Section,
				Key:     target.Key,
				Origin:  origin,
				Draft:   draft,
				Err:     err.Error(),
			}
		}
		return PromptTemplateResetMsg{Section: target.Section, Key: target.Key, Origin: origin}
	}
}

func (m Model) handlePromptTemplateSaved(msg PromptTemplateSavedMsg) Model {
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, "Prompt template is unavailable")
	}
	m = m.withPromptTemplateValue(target, msg.Value)
	m = m.clearStatus(statusOther)
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(target), promptNote{
		Text: "Saved " + target.Title,
		Kind: modal.NoteSuccess,
	})
}

// handlePromptTemplateSaveFailed reconstructs the editor with the user's draft
// and cursor so the write can be retried with ctrl+s or abandoned with esc. It
// never reports failed persistence as success.
func (m Model) handlePromptTemplateSaveFailed(msg PromptTemplateSaveFailedMsg) Model {
	errText := msg.Err
	if errText == "" {
		errText = "Unable to persist prompt template"
	}
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, errText)
	}
	// Failure never mutates the in-memory value, so the original re-derived
	// here still differs from the draft and the editor opens dirty.
	return m.openPromptTemplateEditor(target, msg.Value, m.promptTemplateValue(target), msg.Cursor, promptNote{
		Text: errText,
		Kind: modal.NoteError,
	})
}

func (m Model) handlePromptTemplateReset(msg PromptTemplateResetMsg) Model {
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, "Prompt template is unavailable")
	}
	m = m.withPromptTemplateValue(target, "")
	m = m.clearStatus(statusOther)
	note := "Restored " + target.Title + " to default"
	if msg.Origin == ResetFromEditor {
		note = target.Title + " reset to default"
	}
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(target), promptNote{
		Text: note,
		Kind: modal.NoteSuccess,
	})
}

func (m Model) handlePromptTemplateResetFailed(msg PromptTemplateResetFailedMsg) Model {
	errText := msg.Err
	if errText == "" {
		errText = "Unable to reset prompt template"
	}
	target, ok := promptTemplateTargetBySectionKey(msg.Section, msg.Key)
	if !ok {
		return m.setStatus(statusOther, errText)
	}
	note := promptNote{Text: errText, Kind: modal.NoteError}
	if msg.Origin == ResetFromEditor {
		// Cursor is the pre-submit position stamped on the way out; a
		// picker-origin reset never reaches this branch, so an untagged zero
		// value cannot strand a cursor here.
		return m.openPromptTemplateEditor(target, msg.Draft, m.promptTemplateValue(target), msg.Cursor, note)
	}
	return m.openPromptTemplatePicker(promptTemplateTargetIndex(target), note)
}

func (m Model) promptTemplateSelectItems() []modal.SelectItem {
	items := make([]modal.SelectItem, 0, len(promptTemplateTargets))
	for _, target := range promptTemplateTargets {
		items = append(items, modal.SelectItem{
			Label: fmt.Sprintf("%-16s %s", target.Title, promptTemplateState(m.promptTemplateValue(target))),
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
	case "autofix":
		return m.flowPromptTemplates.Autofix
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
	case "autofix":
		m.flowPromptTemplates.Autofix = value
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
		Issue: flowstore.Issue{
			Provider: "{issue_provider}",
			Number:   123,
			URL:      "{issue_url}",
		},
		PR: flowstore.PullRequest{
			Provider:   "{pr_provider}",
			Number:     123,
			URL:        "{pr_url}",
			HeadBranch: "{pr_head}",
			BaseBranch: "{pr_base}",
			Status:     "{pr_status}",
		},
	}
	if target.Key == "autofix" {
		return defaultAutofixPromptTemplate
	}
	if target.Key == "plan" {
		return flowPlanPrompt(record, flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}, FlowPromptTemplates{}, "")
	}
	phase := flowstore.FlowPhase{PhaseID: flowPhaseIDForPromptTemplateKey(target.Key), Title: "{phase_title}"}
	return flowPhasePrompt(record, phase, "{plan_path}", "{plan_body}", FlowPromptTemplates{}, "")
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
