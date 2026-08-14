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
	EpicID         string
	Children       []beadsquery.Bead
	ReadyIDs       map[string]bool
	ReadinessKnown bool
	State          BeadExpansionState
	Detail         string
}

// BeadVisualHeight is the visual-line contract shared with model scrolling.
func BeadVisualHeight(bead beadsquery.Bead, expansion BeadExpansion) int {
	if expansion.EpicID == "" || bead.ID != expansion.EpicID {
		return 1
	}
	switch expansion.State {
	case BeadExpansionLoaded:
		height := 1 + len(expansion.Children)
		if len(expansion.Children) == 0 {
			height++
		}
		if !expansion.ReadinessKnown {
			height++
		}
		return height
	case BeadExpansionLoading, BeadExpansionError:
		return 2
	default:
		return 1
	}
}

func renderBeadsPane(beads []beadsquery.Bead, selected, scroll, width, height int, expansion BeadExpansion) []string {
	content := make([]string, 0, len(beads))
	for i, bead := range beads {
		content = append(content, expansionVisualLines(bead, i == selected, width, expansion)...)
	}
	return scrollAndPad(content, scroll, height)
}

func expansionVisualLines(bead beadsquery.Bead, selected bool, width int, expansion BeadExpansion) []string {
	lines := []string{renderBeadRow(bead, selected, width)}
	if expansion.EpicID == "" || bead.ID != expansion.EpicID {
		return lines
	}

	stateLine := func(text string) {
		lines = append(lines, truncateToWidth("     "+terminalSafeSingleLine(text), width))
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
				body := fmt.Sprintf("↳ %s  P%d  %s", id, child.Priority, title)
				if expansion.ReadinessKnown && expansion.ReadyIDs[child.ID] {
					body += "  [ready]"
				}
				lines = append(lines, truncateToWidth("     "+body, width))
			}
		}
		if !expansion.ReadinessKnown {
			stateLine("Readiness unavailable: " + expansion.Detail)
		}
	}
	return lines
}

func renderBeadRow(bead beadsquery.Bead, selected bool, width int) string {
	id := terminalSafeSingleLine(bead.ID)
	title := terminalSafeSingleLine(bead.Title)
	assignee := terminalSafeSingleLine(bead.Assignee)
	body := fmt.Sprintf("%s  P%d  %s", id, bead.Priority, title)
	if assignee != "" {
		body += "  " + assignee
	}
	marker := ""
	if strings.EqualFold(strings.TrimSpace(bead.IssueType), "epic") {
		marker = "  [epic]"
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
