package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/ui"
)

// topLevelModeForNumberedKey maps numbers within the focused stored pane.
// Each pane restarts numbering at one; keys not owned by that pane are silent
// no-ops.
func (m Model) topLevelModeForNumberedKey(key string) (ui.Mode, bool) {
	switch m.activePane {
	case ui.PaneTop:
		switch key {
		case "1":
			return m.lastGitSubview(), true
		case "2":
			return m.lastBeadsSubview(), true
		}
	case ui.PaneBottom:
		switch key {
		case "1":
			return ui.ModeSessions, true
		case "2":
			return ui.ModePlans, true
		case "3":
			return ui.ModeFlows, true
		}
	}
	return ui.ModeWorktrees, false
}

// lastBeadsSubview is where entering the top-level Beads view lands: the
// last-used Beads subview, defaulting to Ready on first-ever entry.
func (m Model) lastBeadsSubview() ui.Mode {
	if ui.IsBeadsMode(m.lastBeadsMode) {
		return m.lastBeadsMode
	}
	return ui.ModeBeadsReady
}

// lastGitSubview is where entering the top-level Git view lands: the
// last-used git subview, defaulting to worktrees on first-ever entry.
func (m Model) lastGitSubview() ui.Mode {
	if ui.IsGitMode(m.lastGitMode) {
		return m.lastGitMode
	}
	return ui.ModeWorktrees
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
	currentMode := m.focusedMode()
	if !ui.IsGitMode(currentMode) {
		return m, nil, false
	}
	target, ok := gitSubviewForLetter(key)
	if !ok {
		return m, nil, false
	}
	if currentMode == target {
		return m, nil, true
	}
	previousMode := currentMode
	m, _ = m.selectStoredMode(target)
	m = m.resetModeCursorsForSwitch(previousMode, target)
	next, cmd := m.startFetchMode(target)
	return next, cmd, true
}

func beadsSubviewForLetter(key string) (ui.Mode, bool) {
	switch key {
	case "r":
		return ui.ModeBeadsReady, true
	case "b":
		return ui.ModeBeadsBlocked, true
	case "o":
		return ui.ModeBeadsOpen, true
	case "i":
		return ui.ModeBeadsInProgress, true
	case "c":
		return ui.ModeBeadsClosed, true
	default:
		return 0, false
	}
}

func (m Model) handleBeadsSubviewKey(key string) (Model, tea.Cmd, bool) {
	currentMode := m.focusedMode()
	if !ui.IsBeadsMode(currentMode) {
		return m, nil, false
	}
	target, ok := beadsSubviewForLetter(key)
	if !ok {
		return m, nil, false
	}
	if currentMode == target {
		return m, nil, true
	}
	previousMode := currentMode
	m, _ = m.selectStoredMode(target)
	m = m.resetModeCursorsForSwitch(previousMode, target)
	next, cmd := m.startFetchMode(target)
	return next, cmd, true
}
