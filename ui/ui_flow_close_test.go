package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/flowstore"
)

func closeHintParams() statusBarParams {
	return statusBarParams{
		Width:        400,
		Mode:         ModeFlows,
		ActivePane:   PaneBottom,
		RepoSelected: true,
		FlowSelected: true,
	}
}

func TestStatusBar_FlowCloseHintReflectsTheActionCWillTake(t *testing.T) {
	closable := closeHintParams()
	closable.FlowCloseActionSelected = FlowCloseActionClose
	if bar := renderStatusBarWithState(closable); !strings.Contains(bar, "C: close") {
		t.Fatalf("closable Flow row should offer C: close, got %q", bar)
	}

	reopenable := closeHintParams()
	reopenable.FlowCloseActionSelected = FlowCloseActionReopen
	bar := renderStatusBarWithState(reopenable)
	if !strings.Contains(bar, "C: reopen") {
		t.Fatalf("closed Flow row should offer C: reopen, got %q", bar)
	}
	if strings.Contains(bar, "C: close") {
		t.Fatalf("closed Flow row should not offer C: close, got %q", bar)
	}

	none := closeHintParams()
	none.FlowCloseActionSelected = FlowCloseActionNone
	if bar := renderStatusBarWithState(none); strings.Contains(bar, "C: ") {
		t.Fatalf("ineligible Flow row should hide the close shortcut, got %q", bar)
	}

	phaseRow := closeHintParams()
	phaseRow.FlowCloseActionSelected = FlowCloseActionClose
	phaseRow.FlowPhaseSelected = true
	if bar := renderStatusBarWithState(phaseRow); strings.Contains(bar, "C: ") {
		t.Fatalf("selected Flow phase should hide the close shortcut, got %q", bar)
	}
}

// The a hint is emitted unconditionally from flowAutoModeShortcutHint, so it is
// the one blocked key AC7 has to suppress here rather than through a model
// accessor. It lives in the Mode section, so h has to survive alongside it.
func TestStatusBar_ClosedFlowHidesAutoModeButKeepsHeadless(t *testing.T) {
	sp := closeHintParams()
	sp.FlowCloseActionSelected = FlowCloseActionReopen

	bar := renderStatusBarWithState(sp)
	if strings.Contains(bar, "a: auto") {
		t.Fatalf("closed Flow advertises auto mode: %q", bar)
	}
	if !strings.Contains(bar, "h: headless") {
		t.Fatalf("closed Flow should still offer the headless preference: %q", bar)
	}

	open := closeHintParams()
	open.FlowCloseActionSelected = FlowCloseActionClose
	if bar := renderStatusBarWithState(open); !strings.Contains(bar, "a: auto") {
		t.Fatalf("open Flow should still offer auto mode: %q", bar)
	}
}

func TestRender_FlowPaneShowsCloseReasonOnClosedRow(t *testing.T) {
	closedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	flows := []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Closed flow",
		Status: flowstore.StatusClosed,
		Branch: "flow/closed",
		Closed: flowstore.Closure{Reason: "superseded by #42", ClosedAt: &closedAt},
	}}
	view := strings.Join(renderFlowPane(flows, 0, 0, 240, 5, "", "", nil, false, nil), "\n")
	row := ansi.Strip(lineContaining(view, "flow/closed"))
	if !strings.Contains(row, "closed") {
		t.Fatalf("closed Flow row = %q, want status closed:\n%s", row, view)
	}
	if !strings.Contains(row, "(closed: superseded by #42)") {
		t.Fatalf("closed Flow row = %q, want the reason in the title cell:\n%s", row, view)
	}
}

func TestRender_FlowPaneKeepsMultilineCloseReasonOnOneLine(t *testing.T) {
	closedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	flows := []flowstore.FlowRecord{{
		FlowID: "flow-1",
		Title:  "Closed flow",
		Status: flowstore.StatusClosed,
		Branch: "flow/closed",
		Closed: flowstore.Closure{Reason: "first line\nsecond line", ClosedAt: &closedAt},
	}}
	rows := renderFlowPane(flows, 0, 0, 300, 5, "", "", nil, false, nil)
	view := strings.Join(rows, "\n")
	for _, row := range rows {
		if strings.Contains(ansi.Strip(row), "second line") && strings.Contains(ansi.Strip(row), "first line") {
			return
		}
	}
	t.Fatalf("a newline-bearing close reason should render on one line:\n%s", view)
}
