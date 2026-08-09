package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/ui"
)

// topLevelModeForNumberedKey maps the top-level number keys to their views.
// 1 is the Git view and 5 is the Beads view; each resolves to its last-used
// subview. Keys 6-9 are unbound and report !ok so callers can treat them as
// no-ops.
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
	case "5":
		return m.lastBeadsSubview(), true
	}
	return ui.ModeWorktrees, false
}

// lastBeadsSubview is where entering the top-level Beads view lands: the
// last-used Beads subview, defaulting to Open on first-ever entry.
func (m Model) lastBeadsSubview() ui.Mode {
	if ui.IsBeadsMode(m.lastBeadsMode) {
		return m.lastBeadsMode
	}
	return ui.ModeBeadsOpen
}

// rememberBeadsSubview records the active Beads subview so Beads re-entry is
// sticky.
func (m Model) rememberBeadsSubview() Model {
	if ui.IsBeadsMode(m.mode) {
		m.lastBeadsMode = m.mode
	}
	return m
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
	if !ui.IsBeadsMode(m.mode) {
		return m, nil, false
	}
	target, ok := beadsSubviewForLetter(key)
	if !ok {
		return m, nil, false
	}
	if m.mode == target {
		return m, nil, true
	}
	previousMode := m.mode
	m.mode = target
	m = m.rememberBeadsSubview()
	m = m.resetModeCursorsForSwitch(previousMode, target)
	next, cmd := m.startFetchMode(target)
	return next, cmd, true
}
