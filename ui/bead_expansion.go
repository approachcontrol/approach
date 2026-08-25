package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/beadsquery"
)

// BeadExpansionState identifies the selected epic's inline display state.
type BeadExpansionState string

const (
	BeadExpansionLoading BeadExpansionState = "loading"
	BeadExpansionLoaded  BeadExpansionState = "loaded"
	BeadExpansionError   BeadExpansionState = "error"
)

// BeadExpansion is the immutable rendering projection for one selected epic.
// Request identity and query errors remain model concerns.
type BeadExpansion struct {
	EpicID             string
	Children           []beadsquery.Bead
	ReadyIDs           map[string]bool
	ReadinessKnown     bool
	State              BeadExpansionState
	Detail             string
	ProgressionKnown   bool
	ProgressionEnabled bool
	ProgressionDetail  string
	// ProgressionHaltDetail names the child and state that halted
	// auto-progression, e.g. "epic.8 is blocked". It is the single source of
	// truth for both the halted row marker and the halted expansion line, so
	// the two can never disagree about whether the line exists.
	ProgressionHaltDetail string
}

// beadRowMarkers are the persistent markers a selected epic row can carry.
// Halted outranks auto: it is the state that wants the user's attention.
type beadRowMarkers struct {
	Auto   bool
	Halted bool
}

// BeadVisualHeight is the visual-line contract shared with model scrolling.
func BeadVisualHeight(bead beadsquery.Bead, expansion BeadExpansion) int {
	if expansion.EpicID == "" || bead.ID != expansion.EpicID {
		return 1
	}
	height := 1
	switch expansion.State {
	case BeadExpansionLoaded:
		height += len(expansion.Children)
		if len(expansion.Children) == 0 {
			height++
		}
		if !expansion.ReadinessKnown {
			height++
		}
	case BeadExpansionLoading, BeadExpansionError:
		height++
	}
	if strings.TrimSpace(expansion.ProgressionHaltDetail) != "" {
		height++
	}
	if strings.TrimSpace(expansion.ProgressionDetail) != "" {
		height++
	}
	return height
}

func renderBeadsPane(beads []beadsquery.Bead, selected, scroll, width, height int, expansion BeadExpansion, repoPath string) []string {
	content := make([]string, 0, len(beads))
	for i, bead := range beads {
		content = append(content, expansionVisualLines(bead, i == selected, width, expansion, repoPath)...)
	}
	return scrollAndPad(content, scroll, height)
}

func expansionVisualLines(bead beadsquery.Bead, selected bool, width int, expansion BeadExpansion, repoPath string) []string {
	own := selected && bead.ID == expansion.EpicID
	markers := beadRowMarkers{
		Auto:   own && expansion.ProgressionKnown && expansion.ProgressionEnabled,
		Halted: own && strings.TrimSpace(expansion.ProgressionHaltDetail) != "",
	}
	lines := []string{renderBeadRow(bead, selected, width, markers, repoPath)}
	if expansion.EpicID == "" || bead.ID != expansion.EpicID {
		return lines
	}

	appendExpansionLine := func(line string) {
		line = truncateToWidth(line, width)
		if selected {
			line = renderStyledRow(stashSelStyle.Render(line), stashSelStyle, width)
		}
		lines = append(lines, line)
	}
	stateLine := func(text string) {
		appendExpansionLine("     " + terminalSafeSingleLine(text))
	}
	switch expansion.State {
	case BeadExpansionLoading:
		stateLine("loading direct children")
	case BeadExpansionError:
		stateLine("Could not load children: " + expansion.Detail)
	case BeadExpansionLoaded:
		if len(expansion.Children) == 0 {
			stateLine("no direct children")
		} else {
			for _, child := range expansion.Children {
				id := terminalSafeSingleLine(child.ID)
				title := terminalSafeSingleLine(child.Title)
				body := fmt.Sprintf("↳ %s  P%d  %s", hyperlink(id, beadHyperlinkTarget(repoPath, child.ID)), child.Priority, title)
				marker := ""
				if expansion.ReadinessKnown && expansion.ReadyIDs[child.ID] {
					marker = "  [ready]"
				}
				const indent = "     "
				body = truncateToWidth(body, width-ansi.StringWidth(indent)-ansi.StringWidth(marker))
				appendExpansionLine(indent + body + marker)
			}
		}
		if !expansion.ReadinessKnown {
			stateLine("Readiness unavailable: " + expansion.Detail)
		}
	}
	// Cause precedes a read failure when a projection carries both.
	if strings.TrimSpace(expansion.ProgressionHaltDetail) != "" {
		stateLine("Auto-progression halted: " + expansion.ProgressionHaltDetail)
	}
	if strings.TrimSpace(expansion.ProgressionDetail) != "" {
		stateLine("Auto-progression unavailable: " + expansion.ProgressionDetail)
	}
	return lines
}

func renderBeadRow(bead beadsquery.Bead, selected bool, width int, markers beadRowMarkers, repoPath string) string {
	id := terminalSafeSingleLine(bead.ID)
	title := terminalSafeSingleLine(bead.Title)
	assignee := terminalSafeSingleLine(bead.Assignee)
	body := fmt.Sprintf("%s  P%d  %s", hyperlink(id, beadHyperlinkTarget(repoPath, bead.ID)), bead.Priority, title)
	if assignee != "" {
		body += "  " + assignee
	}
	marker := ""
	if strings.EqualFold(strings.TrimSpace(bead.IssueType), "epic") {
		marker = "  [epic]"
		switch {
		case markers.Halted:
			marker += "  [halted]"
		case markers.Auto:
			marker += "  [auto]"
		}
	}
	prefix := "   "
	if selected {
		prefix = " > "
	}
	body = truncateToWidth(body, width-ansi.StringWidth(prefix)-ansi.StringWidth(marker))
	line := prefix + body + marker
	if selected {
		line = renderStyledRow(stashSelStyle.Render(line), stashSelStyle, width)
	}
	return truncateToWidth(line, width)
}
