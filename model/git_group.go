package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/ui"
)

// topLevelModeForNumberedKey maps the top-level number keys to their views.
// 1 is the Git view, which resolves to the last-used git subview; 5-9 are
// unbound and report !ok so callers can treat them as no-ops.
func (m Model) topLevelModeForNumberedKey(key string) (ui.Mode, bool) {
	switch key {
	case "1":
		return m.lastGitSubview(), true
	case "2":
		return ui.ModeSessions, true
	case "3":
		return ui.ModePlans, true
	case "4":
		return ui.ModeFlows, true
	}
	return ui.ModeWorktrees, false
}

// lastGitSubview is where entering the top-level Git view lands: the
// last-used git subview, defaulting to worktrees on first-ever entry.
func (m Model) lastGitSubview() ui.Mode {
	if ui.IsGitMode(m.lastGitMode) {
		return m.lastGitMode
	}
	return ui.ModeWorktrees
}

// rememberGitSubview records the active git subview so Git re-entry is sticky.
func (m Model) rememberGitSubview() Model {
	if ui.IsGitMode(m.mode) {
		m.lastGitMode = m.mode
	}
	return m
}

// gitSubviewForLetter maps the Git view's direct subview keys to their modes.
func gitSubviewForLetter(key string) (ui.Mode, bool) {
	switch key {
	case "w":
		return ui.ModeWorktrees, true
	case "b":
		return ui.ModeBranches, true
	case "s":
		return ui.ModeStashes, true
	case "h":
		return ui.ModeHistory, true
	case "r":
		return ui.ModeReflog, true
	}
	return ui.ModeWorktrees, false
}

// handleGitSubviewKey switches among the git subviews via their direct letter
// keys. The letters only apply while a git subview is already active, so the
// same keys keep their meanings in the other top-level views.
func (m Model) handleGitSubviewKey(key string) (Model, tea.Cmd, bool) {
	if !ui.IsGitMode(m.mode) {
		return m, nil, false
	}
	target, ok := gitSubviewForLetter(key)
	if !ok {
		return m, nil, false
	}
	if m.mode == target {
		return m, nil, true
	}
	previousMode := m.mode
	m.mode = target
	m = m.rememberGitSubview()
	m = m.resetModeCursorsForSwitch(previousMode, target)
	next, cmd := m.startFetchMode(target)
	return next, cmd, true
}
