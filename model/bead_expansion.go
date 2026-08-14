package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/ui"
)

type beadExpansionResultMsg struct {
	target      beadExpansionTarget
	children    []beadsquery.Bead
	childrenErr error
	ready       []beadsquery.Bead
	readyErr    error
}

func isEpicBead(bead beadsquery.Bead) bool {
	return strings.EqualFold(strings.TrimSpace(bead.IssueType), "epic")
}

// reconcileBeadExpansion aligns the one active inline expansion with the
// selected row in the stored top Beads pane. It deliberately does not consult
// focus: the top pane remains visible and must reflow while the bottom pane is
// focused.
func (m Model) reconcileBeadExpansion() (Model, tea.Cmd) {
	mode := m.topMode
	index, ok := beadSubviewIndex(mode)
	if !ok {
		return m.clearBeadExpansion(), nil
	}
	state := m.beads[index]
	if !state.available || state.pending || state.error != "" || state.repoPath == "" {
		return m.clearBeadExpansion(), nil
	}
	epic, ok := state.pane.Selected()
	if !ok || !isEpicBead(epic) {
		return m.clearBeadExpansion(), nil
	}
	target := m.beadExpansion.target
	if target.repoPath == state.repoPath && target.mode == mode && target.epicID == epic.ID && target.token != 0 {
		return m, nil
	}

	m = m.clearBeadExpansion()
	m.beadExpansionSeq++
	target = beadExpansionTarget{
		token:    m.beadExpansionSeq,
		repoPath: state.repoPath,
		mode:     mode,
		epicID:   epic.ID,
	}
	m.beadExpansion = beadExpansionSnapshot{
		target: target,
		projection: ui.BeadExpansion{
			EpicID: epic.ID,
			State:  ui.BeadExpansionLoading,
		},
	}
	m = m.reflowBeadExpansionPane()

	listChildren := m.listChildrenBeads
	listReady := m.listReadyBeads
	return m, func() tea.Msg {
		children, childrenErr := listChildren(target.repoPath, target.epicID)
		ready, readyErr := listReady(target.repoPath)
		return beadExpansionResultMsg{
			target: target, children: children, childrenErr: childrenErr,
			ready: ready, readyErr: readyErr,
		}
	}
}

func (m Model) handleBeadExpansionResult(msg beadExpansionResultMsg) Model {
	if msg.target.token == 0 || msg.target != m.beadExpansion.target {
		return m
	}
	index, ok := beadSubviewIndex(msg.target.mode)
	if !ok || m.topMode != msg.target.mode {
		return m
	}
	state := m.beads[index]
	selected, selectedOK := state.pane.Selected()
	if !state.available || state.pending || state.error != "" ||
		state.repoPath != msg.target.repoPath || !selectedOK ||
		selected.ID != msg.target.epicID || !isEpicBead(selected) {
		return m
	}
	projection := ui.BeadExpansion{EpicID: msg.target.epicID}
	if msg.childrenErr != nil {
		projection.State = ui.BeadExpansionError
		projection.Detail = msg.childrenErr.Error()
		m.beadExpansion.projection = projection
		return m.reflowBeadExpansionPane()
	}

	projection.State = ui.BeadExpansionLoaded
	if msg.readyErr != nil {
		projection.Children = append([]beadsquery.Bead(nil), msg.children...)
		projection.Detail = msg.readyErr.Error()
		m.beadExpansion.projection = projection
		return m.reflowBeadExpansionPane()
	}

	direct := make(map[string]beadsquery.Bead, len(msg.children))
	for _, child := range msg.children {
		direct[child.ID] = child
	}
	projection.ReadinessKnown = true
	projection.ReadyIDs = make(map[string]bool)
	seen := make(map[string]bool, len(msg.children))
	for _, ready := range msg.ready {
		child, ok := direct[ready.ID]
		if !ok || seen[ready.ID] {
			continue
		}
		projection.Children = append(projection.Children, child)
		projection.ReadyIDs[ready.ID] = true
		seen[ready.ID] = true
	}
	for _, child := range msg.children {
		if seen[child.ID] {
			continue
		}
		projection.Children = append(projection.Children, child)
		seen[child.ID] = true
	}
	m.beadExpansion.projection = projection
	return m.reflowBeadExpansionPane()
}

func (m Model) clearBeadExpansion() Model {
	if m.beadExpansion.target.token == 0 {
		return m
	}
	mode := m.beadExpansion.target.mode
	m.beadExpansionSeq++
	m.beadExpansion = beadExpansionSnapshot{}
	if index, ok := beadSubviewIndex(mode); ok {
		m.beads[index].pane = m.beads[index].pane.SetItemHeight(beadItemHeight(ui.BeadExpansion{}))
		m = m.reflowBeads(mode)
	}
	return m
}

func (m Model) reflowBeadExpansionPane() Model {
	mode := m.beadExpansion.target.mode
	index, ok := beadSubviewIndex(mode)
	if !ok {
		return m
	}
	projection := cloneBeadExpansion(m.beadExpansion.projection)
	m.beads[index].pane = m.beads[index].pane.SetItemHeight(beadItemHeight(projection))
	m = m.reflowBeads(mode)

	beads, selected, scroll := m.beads[index].pane.View()
	if selected < 0 || selected >= len(beads) || beads[selected].ID != projection.EpicID {
		return m
	}
	viewHeight := m.contentHeightForMode(mode)
	line := 0
	for i := 0; i < selected; i++ {
		line += ui.BeadVisualHeight(beads[i], projection)
	}
	height := ui.BeadVisualHeight(beads[selected], projection)
	target := scroll
	if scroll > line {
		target = line
	}
	if height <= viewHeight && line+height > target+viewHeight {
		target = line + height - viewHeight
	} else if height > viewHeight && line+1 >= target+viewHeight {
		target = line
	}
	if target != scroll {
		m.beads[index].pane = m.beads[index].pane.ScrollBy(target-scroll, viewHeight, m.contentWidth())
	}
	return m
}

func (m Model) canScrollBeadExpansion(delta, viewHeight int) bool {
	target := m.beadExpansion.target
	if target.token == 0 || target.mode != m.focusedMode() {
		return false
	}
	index, ok := beadSubviewIndex(target.mode)
	if !ok {
		return false
	}
	beads, selected, scroll := m.beads[index].pane.View()
	if selected < 0 || selected >= len(beads) || beads[selected].ID != target.epicID {
		return false
	}
	if viewHeight <= 0 {
		viewHeight = 1
	}
	line := 0
	for i := 0; i < selected; i++ {
		line += ui.BeadVisualHeight(beads[i], m.beadExpansion.projection)
	}
	height := ui.BeadVisualHeight(beads[selected], m.beadExpansion.projection)
	if delta > 0 {
		return line+height > scroll+viewHeight
	}
	if delta < 0 {
		return scroll > line
	}
	return false
}

func beadItemHeight(expansion ui.BeadExpansion) func(beadsquery.Bead, int) int {
	return func(bead beadsquery.Bead, _ int) int {
		return ui.BeadVisualHeight(bead, expansion)
	}
}

func cloneBeadExpansion(expansion ui.BeadExpansion) ui.BeadExpansion {
	expansion.Children = append([]beadsquery.Bead(nil), expansion.Children...)
	if expansion.ReadyIDs != nil {
		readyIDs := make(map[string]bool, len(expansion.ReadyIDs))
		for id, ready := range expansion.ReadyIDs {
			readyIDs[id] = ready
		}
		expansion.ReadyIDs = readyIDs
	}
	return expansion
}
