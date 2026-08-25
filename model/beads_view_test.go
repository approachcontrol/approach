package model_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func TestBeadsSubviewLettersSwitchAndStartMatchingDeferredQuery(t *testing.T) {
	tests := []struct {
		key  rune
		mode ui.Mode
		name string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, name: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, name: "blocked"},
		{key: 'o', mode: ui.ModeBeadsOpen, name: "open"},
		{key: 'i', mode: ui.ModeBeadsInProgress, name: "in-progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, name: "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := ""
			query := func(name string) func(string) ([]beadsquery.Bead, error) {
				return func(string) ([]beadsquery.Bead, error) {
					called = name
					return []beadsquery.Bead{{ID: "bd-" + name, Priority: 1, Title: name}}, nil
				}
			}
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				ListReadyBeads:      query("ready"),
				ListBlockedBeads:    query("blocked"),
				ListOpenBeads:       query("open"),
				ListInProgressBeads: query("in-progress"),
				ListClosedBeads:     query("closed"),
				CountClosedBeads:    func(string) (int, error) { return 1, nil },
			}))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{differentBeadsSubviewKey(tt.mode)})})
			before := m.ListRequest(tt.mode)

			m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			if m.Mode() != tt.mode {
				t.Fatalf("mode = %v, want %v", m.Mode(), tt.mode)
			}
			if cmd == nil {
				t.Fatal("letter switch returned nil query command")
			}
			if m.ListRequest(tt.mode) == before {
				t.Fatalf("request token for %v did not advance", tt.mode)
			}
			if called != "" {
				t.Fatalf("query ran before command execution: %q", called)
			}
			msg := cmd()
			if called != tt.name {
				t.Fatalf("query = %q, want %q", called, tt.name)
			}
			if got := beadsResultMode(msg); got != tt.mode {
				t.Fatalf("result mode = %v for %T, want %v", got, msg, tt.mode)
			}
		})
	}
}

func TestSelectedEpicExpansionQueriesDeferredAndRendersReadyFirst(t *testing.T) {
	t.Parallel()

	childrenCalls, readyCalls := 0, 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(repoPath, parentID string) ([]beadsquery.Bead, error) {
			childrenCalls++
			if repoPath != "/dev/alpha" || parentID != "bd-epic" {
				t.Fatalf("ListChildrenBeads(%q, %q), want alpha/epic", repoPath, parentID)
			}
			return []beadsquery.Bead{{ID: "bd-child-1", Priority: 2, Title: "Neutral"}, {ID: "bd-child-2", Priority: 1, Title: "Ready"}}, nil
		},
		ListReadyBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			readyCalls++
			return []beadsquery.Bead{{ID: "bd-child-2", Priority: 1, Title: "Ready"}, {ID: "other", Priority: 0, Title: "Outside epic"}}, nil
		},
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 18})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, cmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "bd-epic", Priority: 1, Title: "Parent", IssueType: " ePiC "}},
	})
	if cmd == nil || childrenCalls != 0 || readyCalls != 0 {
		t.Fatalf("accepted epic result = cmd %T calls %d/%d, want deferred command and zero calls", cmd, childrenCalls, readyCalls)
	}
	if view := ansi.Strip(viewContent(m)); !strings.Contains(view, "loading direct children") || !strings.Contains(view, "[epic]") {
		t.Fatalf("loading expansion missing from view:\n%s", view)
	}

	m, _ = applyTestCommand(m, cmd)
	if childrenCalls != 1 || readyCalls != 1 {
		t.Fatalf("query calls = %d/%d, want 1/1", childrenCalls, readyCalls)
	}
	view := ansi.Strip(viewContent(m))
	readyIndex, neutralIndex := strings.Index(view, "bd-child-2"), strings.Index(view, "bd-child-1")
	if readyIndex < 0 || neutralIndex < 0 || readyIndex > neutralIndex || !strings.Contains(view, "Ready  [ready]") || strings.Contains(view, "Neutral  [ready]") {
		t.Fatalf("accepted expansion did not render ready-first with positive marker only:\n%s", view)
	}
}

func TestBeadExpansionNonEpicAndLegacyMetadataDoNotQuery(t *testing.T) {
	t.Parallel()

	calls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads:     func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { calls++; return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	for _, bead := range []beadsquery.Bead{{ID: "legacy", Title: "Legacy"}, {ID: "task", Title: "Task", IssueType: "task"}} {
		var cmd tea.Cmd
		m, cmd = update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{bead}})
		if cmd != nil || calls != 0 || strings.Contains(ansi.Strip(viewContent(m)), "direct children") {
			t.Fatalf("non-epic %#v started expansion: cmd=%T calls=%d\n%s", bead, cmd, calls, ansi.Strip(viewContent(m)))
		}
	}
}

func TestBeadExpansionPartialFailuresRemainLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		childrenErr error
		readyErr    error
		want        string
		wantChild   bool
	}{
		{name: "children", childrenErr: errors.New("children\nfailed"), want: "Could not load children: children failed"},
		{name: "readiness", readyErr: beadsquery.ErrNotConfigured, want: "Readiness unavailable: beads not configured", wantChild: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
				ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-child", Priority: 1, Title: "Child"}}, tt.childrenErr
				},
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, tt.readyErr },
			}))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
			m, cmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{{ID: "bd-epic", Title: "Epic", IssueType: "epic"}}})
			m, _ = applyTestCommand(m, cmd)
			view := ansi.Strip(viewContent(m))
			if !strings.Contains(view, tt.want) || strings.Contains(view, "beads not configured\n") || strings.Contains(view, "Could not load open beads") {
				t.Fatalf("partial failure escaped local expansion state:\n%s", view)
			}
			if strings.Contains(view, "bd-child") != tt.wantChild {
				t.Fatalf("child visibility = %v, want %v:\n%s", strings.Contains(view, "bd-child"), tt.wantChild, view)
			}
			if !m.BeadsAvailable(ui.ModeBeadsOpen) {
				t.Fatal("local expansion failure changed parent list availability")
			}
		})
	}
}

func TestBeadExpansionRejectsStaleResultAfterCursorAndFilterChanges(t *testing.T) {
	t.Parallel()

	childrenFor := ""
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			childrenFor = parentID
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 14})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, oldCmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{
		{ID: "epic-a", Title: "Alpha", IssueType: "epic"}, {ID: "epic-b", Title: "Bravo", IssueType: "EPIC"},
	}})
	m, replacementCmd := update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if replacementCmd == nil || childrenFor != "" || !strings.Contains(ansi.Strip(viewContent(m)), "epic-b") {
		t.Fatalf("cursor transition did not synchronously replace expansion: cmd=%T called=%q\n%s", replacementCmd, childrenFor, ansi.Strip(viewContent(m)))
	}
	m, _ = applyTestCommand(m, oldCmd)
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-a-child") {
		t.Fatalf("stale cursor result restored old expansion:\n%s", ansi.Strip(viewContent(m)))
	}
	m, _ = applyTestCommand(m, replacementCmd)
	if !strings.Contains(ansi.Strip(viewContent(m)), "epic-b-child") {
		t.Fatalf("replacement result missing:\n%s", ansi.Strip(viewContent(m)))
	}

	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
	m, filterCmd := update(m, tea.KeyPressMsg{Text: string([]rune("Alpha"))})
	if filterCmd == nil || strings.Contains(ansi.Strip(viewContent(m)), "epic-b-child") {
		t.Fatalf("filter did not invalidate and start selected replacement: cmd=%T\n%s", filterCmd, ansi.Strip(viewContent(m)))
	}
}

func TestBeadExpansionFilterEditRestartsUnchangedSelectedEpic(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, oldCmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic-alpha", Title: "Alpha epic", IssueType: "epic"}},
	})
	if oldCmd == nil {
		t.Fatal("accepted epic did not start expansion")
	}
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
	m, replacementCmd := update(m, tea.KeyPressMsg{Text: string([]rune("Alpha"))})
	if replacementCmd == nil {
		t.Fatal("filter edit retaining the selected epic did not restart expansion")
	}
	m, _ = applyTestCommand(m, oldCmd)
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-alpha-child") {
		t.Fatalf("pre-filter result restored after same-selection filter edit:\n%s", ansi.Strip(viewContent(m)))
	}
}

func TestProgrammaticTopModeExitClearsPendingBeadExpansion(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, staleCmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}},
	})
	if staleCmd == nil || !strings.Contains(ansi.Strip(viewContent(m)), "loading direct children") {
		t.Fatal("setup did not start pending expansion")
	}

	m, _ = update(m, model.BranchCreatedMsg{RepoPath: "/dev/alpha", Name: "feature/review-loop"})
	if m.Mode() != ui.ModeBranches || strings.Contains(ansi.Strip(viewContent(m)), "loading direct children") {
		t.Fatalf("programmatic top-mode exit retained expansion:\n%s", ansi.Strip(viewContent(m)))
	}
	m, _ = applyTestCommand(m, staleCmd)
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-child") {
		t.Fatalf("stale result was accepted after programmatic top-mode exit:\n%s", ansi.Strip(viewContent(m)))
	}
}

func TestReturningFromActiveFlowsRestartsSelectedEpicExpansion(t *testing.T) {
	t.Parallel()

	childrenCalls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			childrenCalls++
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListFlows:      func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, staleCmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}},
	})
	if staleCmd == nil {
		t.Fatal("accepted epic did not start expansion")
	}

	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m, returnCmd := update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.Mode() != ui.ModeBeadsOpen || returnCmd == nil || childrenCalls != 0 {
		t.Fatalf("return from Active Flows = mode %v cmd %T calls %d, want Beads/Open deferred restart", m.Mode(), returnCmd, childrenCalls)
	}
	m, _ = applyTestCommand(m, staleCmd)
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-child") {
		t.Fatalf("pre-takeover result restored after return:\n%s", ansi.Strip(viewContent(m)))
	}

	batch, ok := returnCmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("return command = %T, want batch with Flow refresh and expansion", returnCmd())
	}
	for _, cmd := range batch {
		m, _ = applyTestCommand(m, cmd)
	}
	if childrenCalls != 2 || !strings.Contains(ansi.Strip(viewContent(m)), "epic-child") {
		t.Fatalf("return expansion calls/view = %d:\n%s", childrenCalls, ansi.Strip(viewContent(m)))
	}
}

func TestActiveFlowsTakeoverInvalidatesBottomFocusedBeadExpansion(t *testing.T) {
	t.Parallel()

	childrenCalls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			childrenCalls++
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListFlows:      func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, staleCmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic-before", Title: "Before", IssueType: "epic"}},
	})
	if staleCmd == nil {
		t.Fatal("accepted epic did not start expansion")
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %v, want bottom Flows focus", m.Mode())
	}
	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.Mode() != ui.ModeActiveFlows {
		t.Fatalf("mode = %v, want Active Flows takeover", m.Mode())
	}

	var backgroundCmd tea.Cmd
	m, backgroundCmd = update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic-background", Title: "Background", IssueType: "epic"}},
	})
	if backgroundCmd != nil {
		t.Fatalf("background Beads result started expansion during takeover: %T", backgroundCmd)
	}
	m, _ = applyTestCommand(m, staleCmd)
	if childrenCalls != 1 {
		t.Fatalf("stale command calls = %d, want one executed query", childrenCalls)
	}

	m, returnCmd := update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.Mode() != ui.ModeFlows || returnCmd == nil {
		t.Fatalf("return from takeover = mode %v cmd %T, want Flows and deferred refresh", m.Mode(), returnCmd)
	}
	result := returnCmd()
	if batch, ok := result.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			m, _ = applyTestCommand(m, cmd)
		}
	} else {
		m, _ = update(m, result)
	}
	if childrenCalls != 2 || !strings.Contains(ansi.Strip(viewContent(m)), "epic-background-child") {
		t.Fatalf("return did not start a fresh expansion: calls=%d\n%s", childrenCalls, ansi.Strip(viewContent(m)))
	}
}

func TestBeadExpansionFilterClearInvalidatesFilteredEpicAndRestartsTopEpic(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, oldAlphaCmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{
		{ID: "epic-alpha", Title: "Alpha", IssueType: "epic"}, {ID: "epic-bravo", Title: "Bravo", IssueType: "epic"},
	}})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
	m, bravoCmd := update(m, tea.KeyPressMsg{Text: string([]rune("Bravo"))})
	if bravoCmd == nil {
		t.Fatal("filter edit returned nil replacement expansion command")
	}
	m, _ = applyTestCommand(m, oldAlphaCmd)
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-alpha-child") {
		t.Fatalf("pre-filter result restored stale alpha expansion:\n%s", ansi.Strip(viewContent(m)))
	}
	bravoResults := testCommandMessages(bravoCmd)
	for _, result := range bravoResults {
		m, _ = update(m, result)
	}
	if !strings.Contains(ansi.Strip(viewContent(m)), "epic-bravo-child") {
		t.Fatalf("filtered bravo expansion missing:\n%s", ansi.Strip(viewContent(m)))
	}

	m, alphaCmd := update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if alphaCmd == nil || strings.Contains(ansi.Strip(viewContent(m)), "epic-bravo-child") {
		t.Fatalf("filter clear did not synchronously replace bravo: cmd=%T\n%s", alphaCmd, ansi.Strip(viewContent(m)))
	}
	for _, result := range bravoResults {
		m, _ = update(m, result)
	}
	if strings.Contains(ansi.Strip(viewContent(m)), "epic-bravo-child") {
		t.Fatalf("stale filtered result restored after clear:\n%s", ansi.Strip(viewContent(m)))
	}
}

func TestBeadExpansionUsesStoredTopPaneWhileBottomFocused(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "child-1", Title: "One"}, {ID: "child-2", Title: "Two"}, {ID: "child-3", Title: "Three"}}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{
			{ID: "before-0", Title: "Before zero"}, {ID: "before-1", Title: "Before one"},
			{ID: "before-2", Title: "Before two"}, {ID: "before-3", Title: "Before three"},
			{ID: "before-4", Title: "Before four"}, {ID: "before-5", Title: "Before five"},
			{ID: "epic", Title: "Epic", IssueType: "epic"}, {ID: "after", Title: "After"},
		},
	})
	var cmd tea.Cmd
	for range 6 {
		m, cmd = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if cmd == nil || m.BeadsScroll(ui.ModeBeadsOpen) != 1 {
		t.Fatalf("selecting epic = cmd %T scroll %d, want pending expansion at scroll 1", cmd, m.BeadsScroll(ui.ModeBeadsOpen))
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %v, want bottom Flows focus", m.Mode())
	}
	m, _ = applyTestCommand(m, cmd)
	view := ansi.Strip(viewContent(m))
	if got := m.BeadsScroll(ui.ModeBeadsOpen); got != 3 {
		t.Fatalf("stored top expansion scroll = %d, want 3 while bottom focused:\n%s", got, view)
	}
	if !strings.Contains(view, "child-1") || !strings.Contains(view, "child-3") || strings.Contains(view, "after") {
		t.Fatalf("stored top expansion did not expose the expected reflowed lines while bottom focused:\n%s", view)
	}
}

func TestBottomPaneInputDoesNotRestartClearedTopBeadExpansion(t *testing.T) {
	t.Parallel()

	childrenCalls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			childrenCalls++
			return nil, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, childCmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}}})
	if childCmd == nil {
		t.Fatal("accepted epic did not start initial command")
	}
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.Mode() != ui.ModeFlows {
		t.Fatalf("mode = %v, want restored bottom focus", m.Mode())
	}
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Text: string([]rune{'/'})},
		{Text: string([]rune("flow"))},
	} {
		var cmd tea.Cmd
		m, cmd = update(m, key)
		if cmd != nil {
			t.Fatalf("bottom input %q restarted top expansion with command %T", key.String(), cmd)
		}
	}
	if childrenCalls != 0 {
		t.Fatalf("bottom input ran child query %d times", childrenCalls)
	}
}

func TestBeadExpansionLifecycleInvalidatesAcrossSubviewRepoAndRefresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		transition func(model.Model) (model.Model, tea.Cmd)
		mode       ui.Mode
		repoPath   string
	}{
		{
			name: "letter subview",
			transition: func(m model.Model) (model.Model, tea.Cmd) {
				return update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
			},
			mode: ui.ModeBeadsReady, repoPath: "/dev/alpha",
		},
		{
			name: "repository cursor",
			transition: func(m model.Model) (model.Model, tea.Cmd) {
				m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
				return update(m, tea.KeyPressMsg{Code: tea.KeyDown})
			},
			mode: ui.ModeBeadsOpen, repoPath: "/dev/bravo",
		},
		{
			name: "refresh",
			transition: func(m model.Model) (model.Model, tea.Cmd) {
				return update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			},
			mode: ui.ModeBeadsOpen, repoPath: "/dev/alpha",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			childrenCalls := 0
			query := func(string) ([]beadsquery.Bead, error) { return nil, nil }
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				ScanRepos:           func() ([]scanner.Repo, error) { return testRepos(), nil },
				ListReadyBeads:      query,
				ListBlockedBeads:    query,
				ListOpenBeads:       query,
				ListInProgressBeads: query,
				ListClosedBeads:     query,
				CountClosedBeads:    func(string) (int, error) { return 0, nil },
				ListChildrenBeads: func(_ string, parentID string) ([]beadsquery.Bead, error) {
					childrenCalls++
					return []beadsquery.Bead{{ID: parentID + "-child", Title: "Child"}}, nil
				},
			}))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
			m, oldCmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{{ID: "old-epic", Title: "Old", IssueType: "epic"}}})
			if oldCmd == nil || childrenCalls != 0 {
				t.Fatalf("setup expansion = cmd %T calls %d", oldCmd, childrenCalls)
			}

			m, transitionCmd := tt.transition(m)
			if transitionCmd == nil || childrenCalls != 0 || strings.Contains(ansi.Strip(viewContent(m)), "old-epic-child") {
				t.Fatalf("transition did not synchronously invalidate without querying: cmd=%T calls=%d\n%s", transitionCmd, childrenCalls, ansi.Strip(viewContent(m)))
			}
			m, _ = applyTestCommand(m, oldCmd)
			if strings.Contains(ansi.Strip(viewContent(m)), "old-epic-child") {
				t.Fatalf("stale result restored old expansion:\n%s", ansi.Strip(viewContent(m)))
			}

			msg := beadsResultMessageForTest(tt.mode, tt.repoPath, m.ListRequest(tt.mode), []beadsquery.Bead{{ID: "new-epic", Title: "New", IssueType: "epic"}})
			m, replacementCmd := update(m, msg)
			if replacementCmd == nil {
				t.Fatalf("accepted replacement in %v returned nil child command", tt.mode)
			}
		})
	}
}

func TestBeadExpansionHeightDrivesVisualScrollingPastExpandedEpic(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{
				{ID: "child-1", Title: "One"}, {ID: "child-2", Title: "Two"},
				{ID: "child-3", Title: "Three"}, {ID: "child-4", Title: "Four"},
			}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, cmd := update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true, Beads: []beadsquery.Bead{
		{ID: "epic", Title: "Epic", IssueType: "epic"}, {ID: "after", Title: "After"},
	}})
	m, _ = applyTestCommand(m, cmd)
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.BeadsScroll(ui.ModeBeadsOpen); got != 1 {
		t.Fatalf("scroll = %d, want visual offset accounting for expanded epic", got)
	}
	view := ansi.Strip(viewContent(m))
	if !strings.Contains(view, "child-1") || !strings.Contains(view, "child-2") || strings.Contains(view, "after") {
		t.Fatalf("expanded-row scrolling did not traverse child lines before moving selection:\n%s", view)
	}
	for m.BeadsSelected(ui.ModeBeadsOpen) == 0 {
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !strings.Contains(ansi.Strip(viewContent(m)), "> after") {
		t.Fatalf("cursor did not move after expansion was fully scrolled:\n%s", ansi.Strip(viewContent(m)))
	}
}

func TestSingleBeadExpansionKeepsBottomScrollAtBoundary(t *testing.T) {
	t.Parallel()

	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{
				{ID: "child-1", Title: "One"}, {ID: "child-2", Title: "Two"},
				{ID: "child-3", Title: "Three"}, {ID: "child-4", Title: "Four"},
			}, nil
		},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, cmd := update(m, model.BeadsOpenResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsOpen), Available: true,
		Beads: []beadsquery.Bead{{ID: "epic", Title: "Epic", IssueType: "epic"}},
	})
	m, _ = applyTestCommand(m, cmd)
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.BeadsScroll(ui.ModeBeadsOpen); got != 2 {
		t.Fatalf("scroll after reaching final child = %d, want 2", got)
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	view := ansi.Strip(viewContent(m))
	if got := m.BeadsScroll(ui.ModeBeadsOpen); got != 2 {
		t.Fatalf("extra Down reset single expanded epic scroll to %d, want 2:\n%s", got, view)
	}
	if m.BeadsSelected(ui.ModeBeadsOpen) != 0 || !strings.Contains(view, "child-4") || strings.Contains(view, "> epic") {
		t.Fatalf("single expanded epic boundary lost the final-child viewport:\n%s", view)
	}
}

func beadsResultMessageForTest(mode ui.Mode, repoPath string, request uint64, beads []beadsquery.Bead) tea.Msg {
	switch mode {
	case ui.ModeBeadsReady:
		return model.BeadsReadyResultMsg{RepoPath: repoPath, ListRequest: request, Available: true, Beads: beads}
	case ui.ModeBeadsBlocked:
		return model.BeadsBlockedResultMsg{RepoPath: repoPath, ListRequest: request, Available: true, Beads: beads}
	case ui.ModeBeadsOpen:
		return model.BeadsOpenResultMsg{RepoPath: repoPath, ListRequest: request, Available: true, Beads: beads}
	case ui.ModeBeadsInProgress:
		return model.BeadsInProgressResultMsg{RepoPath: repoPath, ListRequest: request, Available: true, Beads: beads}
	case ui.ModeBeadsClosed:
		return model.BeadsClosedResultMsg{RepoPath: repoPath, ListRequest: request, Available: true, Beads: beads}
	default:
		panic("unsupported Beads mode")
	}
}

func beadsResultMode(msg tea.Msg) ui.Mode {
	switch msg.(type) {
	case model.BeadsReadyResultMsg:
		return ui.ModeBeadsReady
	case model.BeadsBlockedResultMsg:
		return ui.ModeBeadsBlocked
	case model.BeadsOpenResultMsg:
		return ui.ModeBeadsOpen
	case model.BeadsInProgressResultMsg:
		return ui.ModeBeadsInProgress
	case model.BeadsClosedResultMsg:
		return ui.ModeBeadsClosed
	default:
		return 0
	}
}

func TestBeadsSubviewActiveLetterIsHandledNoOp(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inRightPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = enterBeadsSubview(t, m, beadSubviewCase{key: tt.key, mode: tt.mode})
			before := m.ListRequest(tt.mode)
			m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			if m.Mode() != tt.mode || cmd != nil || m.ListRequest(tt.mode) != before {
				t.Fatalf("active letter changed state: mode=%v cmd=%T request=%d, want mode=%v nil %d", m.Mode(), cmd, m.ListRequest(tt.mode), tt.mode, before)
			}
		})
	}
}

func TestBeadsSubviewLettersAreScopedOutsideBeads(t *testing.T) {
	for _, key := range []rune{'r', 'b', 'o', 'i', 'c'} {
		t.Run(string(key), func(t *testing.T) {
			m := inRightPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{key})})
			if ui.IsBeadsMode(m.Mode()) {
				t.Fatalf("key %q entered Beads outside the Beads group: mode=%v", key, m.Mode())
			}
		})
	}
}

func TestKeyTwoReentersRememberedBeadsSubview(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if m.Mode() != ui.ModeBeadsReady {
				t.Fatalf("first key 2 mode = %v, want ModeBeadsReady", m.Mode())
			}
			if tt.mode == ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'b'})})
			}
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			before := listRequests(m)
			m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if m.Mode() != tt.mode || cmd != nil {
				t.Fatalf("active key 2 changed Beads state: mode=%v cmd=%T, want %v and nil", m.Mode(), cmd, tt.mode)
			}
			assertListRequestsUnchanged(t, before, m)

			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'1'})})
			before = listRequests(m)
			m, cmd = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if m.Mode() != tt.mode {
				t.Fatalf("re-entry mode = %v, want remembered %v", m.Mode(), tt.mode)
			}
			if cmd == nil {
				t.Fatal("re-entry returned nil fetch command")
			}
			assertOnlyListRequestChanged(t, before, m, tt.mode)
		})
	}
}

func TestKeyTwoFirstEntersReadyAndStartsDeferredQuery(t *testing.T) {
	calls := 0
	m := inRightPane(newTestModel(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			calls++
			return []beadsquery.Bead{{ID: "bd-ready"}}, nil
		},
	}))
	before := listRequests(m)

	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})

	if m.Mode() != ui.ModeBeadsReady {
		t.Fatalf("mode = %v, want ModeBeadsReady", m.Mode())
	}
	if cmd == nil {
		t.Fatal("key 2 returned nil Ready fetch command")
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeBeadsReady)
	if calls != 0 {
		t.Fatalf("Ready query ran before command execution: %d call(s)", calls)
	}
	if msg := cmd(); calls != 1 {
		t.Fatalf("Ready query calls = %d, want 1", calls)
	} else if _, ok := msg.(model.BeadsReadyResultMsg); !ok {
		t.Fatalf("fetch message = %T, want BeadsReadyResultMsg", msg)
	}
}

func TestReadyAndOpenKeepIndependentResultsWithSharedID(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-shared", Title: "Ready copy"}})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-shared", Title: "Open copy"}})

	if got := m.BeadsReady(); len(got) != 1 || got[0].Title != "Ready copy" || !m.BeadsAvailable(ui.ModeBeadsReady) {
		t.Fatalf("Ready state = %#v available=%v, want independent shared result", got, m.BeadsAvailable(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 1 || got[0].Title != "Open copy" || !m.BeadsAvailable(ui.ModeBeadsOpen) {
		t.Fatalf("Open state = %#v available=%v, want independent shared result", got, m.BeadsAvailable(ui.ModeBeadsOpen))
	}
}

func TestBeadsResultForPriorSubviewAfterLetterSwitchIsRejected(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sourceKey rune
		source    ui.Mode
		targetKey rune
		target    ui.Mode
	}{
		{name: "ready", sourceKey: 'r', source: ui.ModeBeadsReady, targetKey: 'b', target: ui.ModeBeadsBlocked},
		{name: "blocked", sourceKey: 'b', source: ui.ModeBeadsBlocked, targetKey: 'o', target: ui.ModeBeadsOpen},
		{name: "open", sourceKey: 'o', source: ui.ModeBeadsOpen, targetKey: 'i', target: ui.ModeBeadsInProgress},
		{name: "in-progress", sourceKey: 'i', source: ui.ModeBeadsInProgress, targetKey: 'c', target: ui.ModeBeadsClosed},
		{name: "closed", sourceKey: 'c', source: ui.ModeBeadsClosed, targetKey: 'r', target: ui.ModeBeadsReady},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if tt.source != ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.sourceKey})})
			}
			sourceRequest := m.ListRequest(tt.source)
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.targetKey})})

			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.source, "/dev/alpha", sourceRequest, available, []beadsquery.Bead{{ID: "bd-stale", Title: "Stale"}})
				if len(m.Beads(tt.source)) != 0 || m.BeadsAvailable(tt.source) || !m.BeadsPending(tt.target) {
					t.Fatalf("prior-mode result changed state: source=%#v available=%v targetPending=%v", m.Beads(tt.source), m.BeadsAvailable(tt.source), m.BeadsPending(tt.target))
				}
			}
		})
	}
}

func TestBeadsSubviewStaleRepoAndSupersededRequestResultsAreRejectedPerMode(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			m := inBeadsPane(newTestModel(testRepos(), opts))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if tt.mode != ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			}
			alphaRequest := m.ListRequest(tt.mode)
			m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
			bravoRequest := m.ListRequest(tt.mode)

			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.mode, "/dev/alpha", alphaRequest, available, []beadsquery.Bead{{ID: "bd-stale-repo"}})
				assertBeadsModePending(t, m, tt.mode, "")
			}

			m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, true, []beadsquery.Bead{{ID: "bd-current"}})
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			if m.ListRequest(tt.mode) == bravoRequest {
				t.Fatal("F5 did not supersede the active Beads request")
			}
			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, available, []beadsquery.Bead{{ID: "bd-stale-request"}})
				assertBeadsModePending(t, m, tt.mode, "bd-current")
			}
		})
	}
}

func TestBeadsSubviewFetchPreservesOnlyTargetStateWhilePending(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-open"}})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-ready"}})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'b'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsBlocked, true, []beadsquery.Bead{{ID: "bd-blocked"}})

	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	if got := m.BeadsReady(); cmd == nil || !m.BeadsPending(ui.ModeBeadsReady) || len(got) != 1 || got[0].ID != "bd-ready" || m.BeadsAvailable(ui.ModeBeadsReady) {
		t.Fatalf("Ready target did not retain rows in loading state: rows=%#v available=%v pending=%v", got, m.BeadsAvailable(ui.ModeBeadsReady), m.BeadsPending(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-open" || !m.BeadsAvailable(ui.ModeBeadsOpen) {
		t.Fatalf("Ready fetch changed Open state: %#v available=%v", got, m.BeadsAvailable(ui.ModeBeadsOpen))
	}
	if got := m.BeadsBlocked(); len(got) != 1 || got[0].ID != "bd-blocked" || !m.BeadsAvailable(ui.ModeBeadsBlocked) {
		t.Fatalf("Ready fetch changed Blocked state: %#v available=%v", got, m.BeadsAvailable(ui.ModeBeadsBlocked))
	}
}

func beadQueryOptions() model.Options {
	empty := func(string) ([]beadsquery.Bead, error) { return nil, nil }
	return model.Options{
		ListReadyBeads: empty, ListBlockedBeads: empty, ListOpenBeads: empty,
		ListInProgressBeads: empty, ListClosedBeads: empty,
		CountClosedBeads: func(string) (int, error) { return 0, nil },
	}
}

type beadSubviewCase struct {
	key  rune
	mode ui.Mode
	name string
}

func beadSubviewCases() []beadSubviewCase {
	return []beadSubviewCase{
		{key: 'r', mode: ui.ModeBeadsReady, name: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, name: "blocked"},
		{key: 'o', mode: ui.ModeBeadsOpen, name: "open"},
		{key: 'i', mode: ui.ModeBeadsInProgress, name: "in-progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, name: "closed"},
	}
}

func beadQueryOptionsFor(mode ui.Mode, query func(string) ([]beadsquery.Bead, error)) model.Options {
	opts := beadQueryOptions()
	switch mode {
	case ui.ModeBeadsReady:
		opts.ListReadyBeads = query
	case ui.ModeBeadsBlocked:
		opts.ListBlockedBeads = query
	case ui.ModeBeadsOpen:
		opts.ListOpenBeads = query
	case ui.ModeBeadsInProgress:
		opts.ListInProgressBeads = query
	case ui.ModeBeadsClosed:
		opts.ListClosedBeads = query
	}
	return opts
}

func enterBeadsSubview(t *testing.T, m model.Model, subview beadSubviewCase) (model.Model, tea.Cmd) {
	t.Helper()
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{differentBeadsSubviewKey(subview.mode)})})
	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
	if m.Mode() != subview.mode {
		t.Fatalf("entered mode %v, want %v", m.Mode(), subview.mode)
	}
	return m, cmd
}

func differentBeadsSubviewKey(mode ui.Mode) rune {
	if mode == ui.ModeBeadsBlocked {
		return 'o'
	}
	return 'b'
}

func beadResultFields(t *testing.T, msg tea.Msg) (bool, string, []beadsquery.Bead) {
	t.Helper()
	switch msg := msg.(type) {
	case model.BeadsReadyResultMsg:
		return msg.Available, msg.Error, msg.Beads
	case model.BeadsBlockedResultMsg:
		return msg.Available, msg.Error, msg.Beads
	case model.BeadsOpenResultMsg:
		return msg.Available, msg.Error, msg.Beads
	case model.BeadsInProgressResultMsg:
		return msg.Available, msg.Error, msg.Beads
	case model.BeadsClosedResultMsg:
		return msg.Available, msg.Error, msg.Beads
	default:
		t.Fatalf("message = %T, want typed Beads result", msg)
		return false, "", nil
	}
}

func applyBeadsResult(t *testing.T, m model.Model, mode ui.Mode, available bool, beads []beadsquery.Bead) model.Model {
	t.Helper()
	return applyBeadsResultFor(t, m, mode, "/dev/alpha", m.ListRequest(mode), available, beads)
}

func applyBeadsResultFor(t *testing.T, m model.Model, mode ui.Mode, repoPath string, request uint64, available bool, beads []beadsquery.Bead) model.Model {
	t.Helper()
	var msg tea.Msg
	switch mode {
	case ui.ModeBeadsReady:
		msg = model.BeadsReadyResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsBlocked:
		msg = model.BeadsBlockedResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsOpen:
		msg = model.BeadsOpenResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsInProgress:
		msg = model.BeadsInProgressResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsClosed:
		msg = model.BeadsClosedResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	default:
		t.Fatalf("unsupported Beads mode %v", mode)
	}
	next, _ := update(m, msg)
	return next
}

func applyBeadsResultWithError(t *testing.T, m model.Model, mode ui.Mode, detail string) model.Model {
	t.Helper()
	return applyBeadsResultForWithError(t, m, mode, "/dev/alpha", m.ListRequest(mode), detail)
}

func applyBeadsResultForWithError(t *testing.T, m model.Model, mode ui.Mode, repoPath string, request uint64, detail string) model.Model {
	t.Helper()
	var msg tea.Msg
	switch mode {
	case ui.ModeBeadsReady:
		msg = model.BeadsReadyResultMsg{RepoPath: repoPath, ListRequest: request, Error: detail}
	case ui.ModeBeadsBlocked:
		msg = model.BeadsBlockedResultMsg{RepoPath: repoPath, ListRequest: request, Error: detail}
	case ui.ModeBeadsOpen:
		msg = model.BeadsOpenResultMsg{RepoPath: repoPath, ListRequest: request, Error: detail}
	case ui.ModeBeadsInProgress:
		msg = model.BeadsInProgressResultMsg{RepoPath: repoPath, ListRequest: request, Error: detail}
	case ui.ModeBeadsClosed:
		msg = model.BeadsClosedResultMsg{RepoPath: repoPath, ListRequest: request, Error: detail}
	default:
		t.Fatalf("unsupported Beads mode %v", mode)
	}
	next, _ := update(m, msg)
	return next
}

func assertBeadsModePending(t *testing.T, m model.Model, mode ui.Mode, wantID string) {
	t.Helper()
	got := m.Beads(mode)
	if wantID == "" {
		if len(got) != 0 || m.BeadsAvailable(mode) || !m.BeadsPending(mode) {
			t.Fatalf("Beads mode %v state = rows %#v available=%v pending=%v, want cleared pending", mode, got, m.BeadsAvailable(mode), m.BeadsPending(mode))
		}
		return
	}
	if len(got) != 1 || got[0].ID != wantID || m.BeadsAvailable(mode) || !m.BeadsPending(mode) {
		t.Fatalf("Beads mode %v state = rows %#v available=%v pending=%v, want retained %q pending", mode, got, m.BeadsAvailable(mode), m.BeadsPending(mode), wantID)
	}
}

func TestBeadsOpen_ExplicitSubviewStartsDeferredFetchForSelectedRepo(t *testing.T) {
	calls := 0
	queriedRepo := ""
	m := newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			calls++
			queriedRepo = repoPath
			return []beadsquery.Bead{{ID: "bd-1", Priority: 1, Title: "First"}}, nil
		},
	})
	m = inRightPane(m)
	beforeRequest := m.ListRequest(ui.ModeBeadsOpen)
	if beforeRequest == 0 {
		t.Fatal("new model has zero Beads Open request token")
	}

	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, cmd = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	if m.Mode() != ui.ModeBeadsOpen {
		t.Fatalf("mode = %v, want ModeBeadsOpen", m.Mode())
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == beforeRequest {
		t.Fatalf("Beads Open request = %d, want advancement from %d", request, beforeRequest)
	}
	if cmd == nil {
		t.Fatal("open switch returned nil fetch command")
	}
	if calls != 0 {
		t.Fatalf("ListOpenBeads ran before command execution: %d call(s)", calls)
	}
	if !m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = false before asynchronous result")
	}
	if view := viewContent(m); !strings.Contains(view, "loading open beads") || strings.Contains(view, "beads not configured") {
		t.Fatalf("pending Beads fetch did not render a neutral loading state:\n%s", view)
	}

	msg := cmd()
	if calls != 1 || queriedRepo != "/dev/alpha" {
		t.Fatalf("ListOpenBeads call = (%d, %q), want (1, %q)", calls, queriedRepo, "/dev/alpha")
	}
	result, ok := msg.(model.BeadsOpenResultMsg)
	if !ok {
		t.Fatalf("fetch message = %T, want BeadsOpenResultMsg", msg)
	}
	if result.RepoPath != "/dev/alpha" || !result.Available || result.ListRequest != m.ListRequest(ui.ModeBeadsOpen) {
		t.Fatalf("fetch result = %#v, want current available alpha result", result)
	}
}

func TestBeadsOpen_QueryFailureProducesUnavailableResult(t *testing.T) {
	m := inRightPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) {
			return nil, errors.New("bd exploded")
		},
	}))
	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, cmd = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})

	rawMsg := cmd()
	msg, ok := rawMsg.(model.BeadsOpenResultMsg)
	if !ok {
		t.Fatalf("fetch message = %T, want BeadsOpenResultMsg", rawMsg)
	}
	if msg.Available || len(msg.Beads) != 0 {
		t.Fatalf("failed query result = %#v, want unavailable without rows", msg)
	}
	if got := m.TransientError(); got != "" {
		t.Fatalf("TransientError() = %q before result application, want empty", got)
	}
}

func TestBeadsSubviewQueryResultsDistinguishNotConfiguredFromErrors(t *testing.T) {
	for _, subview := range beadSubviewCases() {
		t.Run(subview.name, func(t *testing.T) {
			for _, result := range []struct {
				name          string
				err           error
				notConfigured bool
				wantDetail    string
			}{
				{name: "not configured", err: fmt.Errorf("query unavailable: %w", beadsquery.ErrNotConfigured), notConfigured: true},
				{name: "configured error", err: errors.New("bd exploded with useful detail"), wantDetail: "bd exploded with useful detail"},
			} {
				t.Run(result.name, func(t *testing.T) {
					m := inRightPane(newTestModel(testRepos(), beadQueryOptionsFor(subview.mode, func(string) ([]beadsquery.Bead, error) {
						return []beadsquery.Bead{{ID: "bd-ignored"}}, result.err
					})))
					m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 14})
					m, cmd := enterBeadsSubview(t, m, subview)
					if cmd == nil {
						t.Fatal("entering Beads subview returned nil query command")
					}

					raw := cmd()
					available, detail, rows := beadResultFields(t, raw)
					if available || len(rows) != 0 {
						t.Fatalf("query result = available %v rows %#v, want unavailable without partial rows", available, rows)
					}
					if result.notConfigured && detail != "" {
						t.Fatalf("not-configured detail = %q, want calm empty detail", detail)
					}
					if !result.notConfigured && detail != result.wantDetail {
						t.Fatalf("configured error detail = %q, want %q", detail, result.wantDetail)
					}

					m, _ = update(m, raw)
					view := viewContent(m)
					if result.notConfigured {
						if !strings.Contains(view, "beads not configured") || strings.Contains(view, "Could not load") {
							t.Fatalf("not-configured result rendered as an error:\n%s", view)
						}
					} else {
						wantLabel := "Could not load " + subview.name + " beads"
						if !strings.Contains(view, wantLabel) || !strings.Contains(view, result.wantDetail) || strings.Contains(view, "beads not configured") {
							t.Fatalf("configured error did not render persistent detail:\n%s", view)
						}
					}
					if m.BeadsPending(subview.mode) || m.BeadsAvailable(subview.mode) || len(m.Beads(subview.mode)) != 0 {
						t.Fatalf("settled result state = pending %v available %v rows %#v", m.BeadsPending(subview.mode), m.BeadsAvailable(subview.mode), m.Beads(subview.mode))
					}
					if m.TransientError() != "" {
						t.Fatalf("Beads result leaked into transient status: %q", m.TransientError())
					}
				})
			}
		})
	}
}

func TestBeadsSubviewErrorLifecycleAndStaleGuards(t *testing.T) {
	for _, subview := range beadSubviewCases() {
		t.Run(subview.name, func(t *testing.T) {
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			m := inBeadsPane(newTestModel(testRepos(), opts))
			m, _ = enterBeadsSubview(t, m, subview)
			m = applyBeadsResultWithError(t, m, subview.mode, "first failure")
			if got := m.BeadsError(subview.mode); got != "first failure" || !strings.Contains(viewContent(m), got) {
				t.Fatalf("accepted error = %q view:\n%s", got, viewContent(m))
			}

			m, _ = update(m, tea.WindowSizeMsg{Width: 96, Height: 14})
			if got := m.BeadsError(subview.mode); got != "first failure" || !strings.Contains(viewContent(m), got) {
				t.Fatalf("resize did not preserve error = %q view:\n%s", got, viewContent(m))
			}

			m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			if cmd == nil || !m.BeadsPending(subview.mode) || m.BeadsError(subview.mode) != "" {
				t.Fatalf("refresh state = cmd %T pending %v error %q", cmd, m.BeadsPending(subview.mode), m.BeadsError(subview.mode))
			}
			if view := viewContent(m); !strings.Contains(view, "loading "+subview.name+" beads") || strings.Contains(view, "first failure") {
				t.Fatalf("refresh did not replace error with loading:\n%s", view)
			}

			m = applyBeadsResult(t, m, subview.mode, true, []beadsquery.Bead{{ID: "bd-success", Title: "Loaded"}})
			if m.BeadsError(subview.mode) != "" || !m.BeadsAvailable(subview.mode) || !strings.Contains(viewContent(m), "bd-success") {
				t.Fatalf("success did not replace error: error=%q available=%v view:\n%s", m.BeadsError(subview.mode), m.BeadsAvailable(subview.mode), viewContent(m))
			}

			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			m = applyBeadsResultWithError(t, m, subview.mode, "second failure")
			if len(m.Beads(subview.mode)) != 0 || m.BeadsAvailable(subview.mode) || m.BeadsPending(subview.mode) {
				t.Fatalf("error did not clear successful rows: rows=%#v available=%v pending=%v", m.Beads(subview.mode), m.BeadsAvailable(subview.mode), m.BeadsPending(subview.mode))
			}
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			m = applyBeadsResult(t, m, subview.mode, false, nil)
			if m.BeadsError(subview.mode) != "" || !strings.Contains(viewContent(m), "beads not configured") || strings.Contains(viewContent(m), "second failure") {
				t.Fatalf("not-configured result did not replace error: error=%q view:\n%s", m.BeadsError(subview.mode), viewContent(m))
			}

			currentRequest := m.ListRequest(subview.mode)
			m = applyBeadsResultForWithError(t, m, subview.mode, "/dev/bravo", currentRequest, "wrong repo")
			m = applyBeadsResultForWithError(t, m, subview.mode, "/dev/alpha", currentRequest-1, "old request")
			if m.BeadsError(subview.mode) != "" || !strings.Contains(viewContent(m), "beads not configured") {
				t.Fatalf("stale error replaced current calm state: error=%q view:\n%s", m.BeadsError(subview.mode), viewContent(m))
			}

			target := beadSubviewCases()[0]
			if target.mode == subview.mode {
				target = beadSubviewCases()[1]
			}
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{target.key})})
			m = applyBeadsResultForWithError(t, m, subview.mode, "/dev/alpha", currentRequest, "inactive error")
			if m.BeadsError(subview.mode) != "" || !m.BeadsPending(target.mode) {
				t.Fatalf("inactive error changed state: source error=%q target pending=%v", m.BeadsError(subview.mode), m.BeadsPending(target.mode))
			}
		})
	}
}

func TestBeadsClosed_FetchListsThenCountsAndReturnsTotal(t *testing.T) {
	calls := []string{}
	opts := beadQueryOptions()
	opts.ListClosedBeads = func(repoPath string) ([]beadsquery.Bead, error) {
		calls = append(calls, "list "+repoPath)
		return []beadsquery.Bead{{ID: "bd-1", Priority: 1, Title: "Closed"}}, nil
	}
	opts.CountClosedBeads = func(repoPath string) (int, error) {
		calls = append(calls, "count "+repoPath)
		return 1432, nil
	}
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	if cmd == nil {
		t.Fatal("Closed switch returned nil fetch command")
	}

	rawMsg := cmd()
	msg, ok := rawMsg.(model.BeadsClosedResultMsg)
	if !ok {
		t.Fatalf("fetch message = %T, want BeadsClosedResultMsg", rawMsg)
	}
	if got := strings.Join(calls, ", "); got != "list /dev/alpha, count /dev/alpha" {
		t.Fatalf("Closed fetch calls = %q, want list then count", got)
	}
	if !msg.Available || msg.Total != 1432 || len(msg.Beads) != 1 || msg.Beads[0].ID != "bd-1" {
		t.Fatalf("Closed fetch result = %#v, want available row and total 1432", msg)
	}
}

func TestBeadsClosed_ListOrCountFailureReturnsNoPartialRows(t *testing.T) {
	tests := []struct {
		name           string
		listErr        error
		countErr       error
		wantCountCalls int
		wantError      string
	}{
		{name: "list failure skips count", listErr: errors.New("list failed"), wantError: "list failed"},
		{name: "count failure discards rows", countErr: errors.New("count failed"), wantCountCalls: 1, wantError: "count failed"},
		{name: "not-configured count stays calm", countErr: beadsquery.ErrNotConfigured, wantCountCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			countCalls := 0
			opts := beadQueryOptions()
			opts.ListClosedBeads = func(string) ([]beadsquery.Bead, error) {
				return []beadsquery.Bead{{ID: "bd-partial", Title: "Partial"}}, tt.listErr
			}
			opts.CountClosedBeads = func(string) (int, error) {
				countCalls++
				return 1432, tt.countErr
			}
			m := inBeadsPane(newTestModel(testRepos(), opts))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})

			rawMsg := cmd()
			msg, ok := rawMsg.(model.BeadsClosedResultMsg)
			if !ok {
				t.Fatalf("fetch message = %T, want BeadsClosedResultMsg", rawMsg)
			}
			if msg.Available || len(msg.Beads) != 0 || msg.Total != 0 {
				t.Fatalf("failed Closed fetch = %#v, want unavailable without partial data", msg)
			}
			if msg.Error != tt.wantError {
				t.Fatalf("failed Closed fetch error = %q, want %q", msg.Error, tt.wantError)
			}
			if countCalls != tt.wantCountCalls {
				t.Fatalf("count calls = %d, want %d", countCalls, tt.wantCountCalls)
			}
		})
	}
}

func TestBeadsRepoChangeClearsEverySubviewError(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	for _, subview := range beadSubviewCases() {
		if !ui.IsBeadsMode(m.Mode()) {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
		}
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
		}
		m = applyBeadsResultWithError(t, m, subview.mode, subview.name+" failure")
	}

	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd == nil {
		t.Fatal("repo change returned nil active-subview fetch")
	}
	for _, subview := range beadSubviewCases() {
		if got := m.BeadsError(subview.mode); got != "" {
			t.Fatalf("repo change retained %s error %q", subview.name, got)
		}
	}
}

func TestBeadsClosed_CurrentSuccessRendersAcceptedTotal(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsClosed),
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-1", Title: "Closed"}},
		Total:       1432,
	})

	if view := viewContent(m); !strings.Contains(view, "closed 1 of 1432") {
		t.Fatalf("current Closed result did not render accepted total:\n%s", view)
	}
}

func TestBeadsClosed_StaleAndWrongRepoTotalsAreIgnored(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	request := m.ListRequest(ui.ModeBeadsClosed)
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath: "/dev/alpha", ListRequest: request, Available: true,
		Beads: []beadsquery.Bead{{ID: "bd-current", Title: "Current"}}, Total: 1432,
	})

	for _, msg := range []model.BeadsClosedResultMsg{
		{RepoPath: "/dev/bravo", ListRequest: request, Available: true, Total: 9001},
		{RepoPath: "/dev/alpha", ListRequest: request - 1, Available: true, Total: 9002},
	} {
		m, _ = update(m, msg)
		if view := viewContent(m); !strings.Contains(view, "closed 1 of 1432") {
			t.Fatalf("ignored result changed Closed count:\n%s", view)
		}
	}
}

func TestBeadsClosed_RefreshAndRepoChangeClearTotalWhilePending(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	request := m.ListRequest(ui.ModeBeadsClosed)
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath: "/dev/alpha", ListRequest: request, Available: true,
		Beads: []beadsquery.Bead{{ID: "bd-current", Title: "Current"}}, Total: 1432,
	})

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	if view := viewContent(m); !m.BeadsPending(ui.ModeBeadsClosed) || !strings.Contains(view, "loading closed beads") || strings.Contains(view, "closed 1") {
		t.Fatalf("refresh did not suppress the accepted Closed count:\n%s", view)
	}
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath: "/dev/alpha", ListRequest: request, Available: true, Total: 9001,
	})
	if view := viewContent(m); !strings.Contains(view, "loading closed beads") || strings.Contains(view, "closed 1") {
		t.Fatalf("stale refresh result restored the Closed count:\n%s", view)
	}

	currentRequest := m.ListRequest(ui.ModeBeadsClosed)
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath: "/dev/alpha", ListRequest: currentRequest, Available: true,
		Beads: []beadsquery.Bead{{ID: "bd-refreshed", Title: "Refreshed"}}, Total: 1500,
	})
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if view := viewContent(m); !m.BeadsPending(ui.ModeBeadsClosed) || len(m.BeadsClosed()) != 0 || !strings.Contains(view, "loading closed beads") || strings.Contains(view, "of 1500") {
		t.Fatalf("repo change did not clear and suppress the Closed result:\n%s", view)
	}
}

func TestBeadsClosed_HeaderCountUsesUnfilteredAcceptedRows(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 180, Height: 16})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	rows := make([]beadsquery.Bead, 100)
	for i := range rows {
		rows[i] = beadsquery.Bead{ID: fmt.Sprintf("bd-%d", i), Title: "Other"}
	}
	rows[0].Title = "Needle"
	m, _ = update(m, model.BeadsClosedResultMsg{
		RepoPath: "/dev/alpha", ListRequest: m.ListRequest(ui.ModeBeadsClosed),
		Available: true, Beads: rows, Total: 1432,
	})
	if view := viewContent(m); !strings.Contains(view, "closed 100 of 1432") {
		t.Fatalf("unfiltered Closed header has wrong count:\n%s", view)
	}

	m = setBeadsQuery(t, m, "needle")
	if got := m.BeadsClosed(); len(got) != 1 || got[0].Title != "Needle" {
		t.Fatalf("filtered Closed rows = %#v, want one matching row", got)
	}
	if view := viewContent(m); !strings.Contains(view, "closed 100 of 1432") {
		t.Fatalf("filter changed Closed source count:\n%s", view)
	}
}

func TestBeadsOpen_CurrentSuccessReplacesPaneAndMarksAvailable(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})

	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-1", Priority: 1, Title: "First"},
			{ID: "bd-2", Priority: 2, Title: "Second"},
		},
	})

	if got := m.BeadsOpen(); len(got) != 2 || got[1].ID != "bd-2" {
		t.Fatalf("BeadsOpen() = %#v, want current result", got)
	}
	if !m.BeadsOpenAvailable() {
		t.Fatal("BeadsOpenAvailable() = false after successful query")
	}
	if m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = true after successful query")
	}
}

func TestBeadsOpen_CurrentUnavailableClearsPaneWithoutFetchStatus(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	request := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-old", Title: "Old"}},
	})

	m, _ = update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: request})

	if got := m.BeadsOpen(); len(got) != 0 {
		t.Fatalf("BeadsOpen() = %#v, want cleared pane", got)
	}
	if m.BeadsOpenAvailable() {
		t.Fatal("BeadsOpenAvailable() = true after unavailable result")
	}
	if m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = true after unavailable result")
	}
	if got := m.TransientError(); got != "" {
		t.Fatalf("TransientError() = %q, want blanket UI state without fetch error", got)
	}
}

func TestBeadsOpen_RepoCursorChangeClearsAvailabilityAndRefetches(t *testing.T) {
	queriedRepo := ""
	m := newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			queriedRepo = repoPath
			return nil, nil
		},
	})
	m = inBeadsPane(m)
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-alpha", Title: "Alpha"}},
	})
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	before := m.ListRequest(ui.ModeBeadsOpen)

	m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd == nil {
		t.Fatal("repo cursor change returned nil Beads fetch command")
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == before {
		t.Fatalf("Beads request = %d, want advancement from %d", request, before)
	}
	if len(m.BeadsOpen()) != 0 || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("repo change Beads state: rows=%#v available=%v pending=%v, want empty pending state", m.BeadsOpen(), m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
	if queriedRepo != "" {
		t.Fatalf("ListOpenBeads ran before command execution for %q", queriedRepo)
	}
	var msg model.BeadsOpenResultMsg
	found := false
	for _, rawMsg := range immediateTestCommandMessages(cmd) {
		if candidate, ok := rawMsg.(model.BeadsOpenResultMsg); ok {
			msg = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("repo change fetch = %T, want batch containing BeadsOpenResultMsg", cmd())
	}
	if queriedRepo != "/dev/bravo" || msg.RepoPath != "/dev/bravo" {
		t.Fatalf("repo change queried %q and returned %q, want /dev/bravo", queriedRepo, msg.RepoPath)
	}
}

func TestBeadsOpen_StaleSuccessAndUnavailableResultsAreIgnored(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) { return testRepos(), nil },
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) {
			return nil, nil
		},
	})
	m = inBeadsPane(m)
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	alphaRequest := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	bravoRequest := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/bravo",
		ListRequest: bravoRequest,
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-bravo", Title: "Bravo"}},
	})

	staleRows := []beadsquery.Bead{{ID: "bd-stale", Title: "Stale"}}
	for _, stale := range []model.BeadsOpenResultMsg{
		{RepoPath: "/dev/alpha", ListRequest: alphaRequest, Available: true, Beads: staleRows},
		{RepoPath: "/dev/alpha", ListRequest: alphaRequest},
	} {
		m, _ = update(m, stale)
		assertCurrentBravoBead(t, m)
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	if m.ListRequest(ui.ModeBeadsOpen) == bravoRequest {
		t.Fatal("F5 did not supersede Beads Open request")
	}
	for _, stale := range []model.BeadsOpenResultMsg{
		{RepoPath: "/dev/bravo", ListRequest: bravoRequest, Available: true, Beads: staleRows},
		{RepoPath: "/dev/bravo", ListRequest: bravoRequest},
	} {
		m, _ = update(m, stale)
		assertBeadsOpenPending(t, m)
	}
}

func assertCurrentBravoBead(t *testing.T, m model.Model) {
	t.Helper()
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-bravo" || !m.BeadsOpenAvailable() {
		t.Fatalf("stale result changed current Beads state: rows=%#v available=%v", got, m.BeadsOpenAvailable())
	}
}

func assertBeadsOpenPending(t *testing.T, m model.Model) {
	t.Helper()
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-bravo" || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("stale result changed retained pending Beads state: rows=%#v available=%v pending=%v", got, m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
}

func TestBeadsSubviews_RightPaneSlashFiltersEachSubview(t *testing.T) {
	for _, tt := range []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, query: "needle-id"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "needle title"},
		{key: 'o', mode: ui.ModeBeadsOpen, query: "alice"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "needle-id"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "alice"},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if tt.mode != ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			}
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-other", Title: "Other", Assignee: "bob"},
				{ID: "needle-id", Title: "Needle title", Assignee: "alice"},
			})

			m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
			if cmd != nil || !m.SearchActive() {
				t.Fatalf("right-pane slash = cmd %T active %v, want nil/true", cmd, m.SearchActive())
			}
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune(tt.query))})
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.SearchActive() || m.ItemSearch() != tt.query {
				t.Fatalf("filter lifecycle = active %v query %q, want false/%q", m.SearchActive(), m.ItemSearch(), tt.query)
			}
			if got := m.Beads(tt.mode); len(got) != 1 || got[0].ID != "needle-id" {
				t.Fatalf("filtered Beads = %#v, want needle row", got)
			}
		})
	}
}

func TestBeadsOpen_F5RefetchesSelectedRepo(t *testing.T) {
	var queried []string
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) { return testRepos(), nil },
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			queried = append(queried, repoPath)
			return nil, nil
		},
	}))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-old", Priority: 1, Title: "Old result"}},
	})
	before := m.ListRequest(ui.ModeBeadsOpen)

	m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	if cmd == nil {
		t.Fatal("F5 returned nil command")
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == before {
		t.Fatalf("Beads request = %d, want advancement from %d", request, before)
	}
	if len(queried) != 0 {
		t.Fatalf("ListOpenBeads ran before refresh command execution: %v", queried)
	}
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-old" || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("F5 pending state: rows=%#v available=%v pending=%v, want retained pending state", got, m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
	if view := viewContent(m); !strings.Contains(view, "loading open beads") || strings.Contains(view, "bd-old") {
		t.Fatalf("F5 did not replace stale rows with loading state:\n%s", view)
	}
	msgs := immediateTestCommandMessages(cmd)
	if len(queried) != 1 || queried[0] != "/dev/alpha" {
		t.Fatalf("F5 queried repos = %v, want [/dev/alpha]", queried)
	}
	if !hasListFetchForMode(msgs, ui.ModeBeadsOpen, m.ListRequest(ui.ModeBeadsOpen)) {
		t.Fatalf("F5 messages = %#v, want current Beads Open result", msgs)
	}
}

func TestBeadsOpen_ModelViewExposesRowsSelectionAndAvailability(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 16})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-1", Priority: 1, Title: "First"},
			{ID: "bd-2", Priority: 2, Title: "Second", Assignee: "alice"},
		},
	})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})

	view := viewContent(m)
	if !strings.Contains(view, "   bd-1  P1  First") || !strings.Contains(view, " > bd-2  P2  Second  alice") {
		t.Fatalf("model view did not expose Beads pane selection:\n%s", view)
	}
}

func TestKeyTwoIsInertWhileRepoPaneFocusedAtReadyDefault(t *testing.T) {
	m := newTestModel(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	})
	before := m.ListRequest(ui.ModeBeadsReady)

	m, cmd := update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})

	if m.Mode() != ui.ModeBeadsReady || cmd != nil || m.ListRequest(ui.ModeBeadsReady) != before {
		t.Fatalf("repo-pane key 2 changed state: mode=%v cmd=%T request=%d want mode=%v request=%d", m.Mode(), cmd, m.ListRequest(ui.ModeBeadsReady), ui.ModeBeadsReady, before)
	}
}

func TestBeadsOpen_CursorUsesGroupedContentHeightAndScroll(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"}, {ID: "bd-3"},
		},
	})
	for range 3 {
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}

	if m.BeadsOpenSelected() != 3 {
		t.Fatalf("BeadsOpenSelected() = %d, want 3", m.BeadsOpenSelected())
	}
	if scroll := m.BeadsOpenScroll(); scroll != 1 {
		t.Fatalf("Beads scroll = %d, want 1 for stacked viewport", scroll)
	}
	if !strings.Contains(viewContent(m), "> bd-3") {
		t.Fatalf("selected Bead did not render after scrolling:\n%s", viewContent(m))
	}
}

func TestBeadsSubviews_RestoreIndependentPaneStateAcrossSwitchPaths(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	openRows := []beadsquery.Bead{
		{ID: "open-0", Title: "Open zero"},
		{ID: "open-1", Title: "Open one"},
		{ID: "open-2", Title: "Open two"},
		{ID: "open-3", Title: "Open three"},
	}
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m = setBeadsQuery(t, m, "open")
	for range 3 {
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	readyRows := []beadsquery.Bead{
		{ID: "ready-0", Title: "Ready zero"},
		{ID: "ready-1", Title: "Ready one"},
		{ID: "ready-2", Title: "Ready two"},
		{ID: "ready-3", Title: "Ready three"},
	}
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, readyRows)
	m = setBeadsQuery(t, m, "ready")
	for range 2 {
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	assertBeadsPaneState(t, m, ui.ModeBeadsOpen, "open", 4, 3, 1)
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'1'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	assertBeadsPaneState(t, m, ui.ModeBeadsOpen, "open", 4, 3, 1)

	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	assertBeadsPaneState(t, m, ui.ModeBeadsReady, "ready", 4, 2, 0)
}

func setBeadsQuery(t *testing.T, m model.Model, query string) model.Model {
	t.Helper()
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune(query))})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func assertBeadsPaneState(t *testing.T, m model.Model, mode ui.Mode, query string, rows, selected, scroll int) {
	t.Helper()
	if m.Mode() != mode || m.ItemSearch() != query || len(m.Beads(mode)) != rows ||
		m.BeadsSelected(mode) != selected || m.BeadsScroll(mode) != scroll {
		t.Fatalf("Beads pane %v = mode %v query %q rows %d selected %d scroll %d, want query %q rows %d selected %d scroll %d",
			mode, m.Mode(), m.ItemSearch(), len(m.Beads(mode)), m.BeadsSelected(mode), m.BeadsScroll(mode),
			query, rows, selected, scroll)
	}
}

func TestBeadsSubviews_SameRepoRefetchRetainsThenRefiltersAndClamps(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			m := inBeadsPane(newTestModel(testRepos(), opts))
			m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if tt.mode != ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			}
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-old-0", Title: "Needle zero"},
				{ID: "bd-old-1", Title: "Needle one"},
				{ID: "bd-old-2", Title: "Needle two"},
				{ID: "bd-old-3", Title: "Needle three"},
			})
			m = setBeadsQuery(t, m, "needle")
			for range 3 {
				m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
			}

			m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			if cmd == nil || !m.BeadsPending(tt.mode) || m.BeadsAvailable(tt.mode) {
				t.Fatalf("F5 state = cmd %T pending %v available %v", cmd, m.BeadsPending(tt.mode), m.BeadsAvailable(tt.mode))
			}
			assertBeadsPaneState(t, m, tt.mode, "needle", 4, 3, 1)
			if view := viewContent(m); !strings.Contains(view, "loading ") || strings.Contains(view, "bd-old-3") {
				t.Fatalf("pending view exposed retained rows instead of loading state:\n%s", view)
			}

			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-new-0", Title: "Needle new zero"},
				{ID: "bd-new-1", Title: "Needle new one"},
			})
			assertBeadsPaneState(t, m, tt.mode, "needle", 2, 1, 0)
			if !m.BeadsAvailable(tt.mode) || m.BeadsPending(tt.mode) {
				t.Fatalf("short replacement availability/pending = %v/%v, want true/false", m.BeadsAvailable(tt.mode), m.BeadsPending(tt.mode))
			}

			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{{ID: "bd-other", Title: "Other"}})
			assertBeadsPaneState(t, m, tt.mode, "needle", 0, 0, 0)
			if !m.BeadsAvailable(tt.mode) || m.BeadsPending(tt.mode) {
				t.Fatalf("zero-match replacement availability/pending = %v/%v, want true/false", m.BeadsAvailable(tt.mode), m.BeadsPending(tt.mode))
			}
		})
	}
}

func TestBeadsRepoChangeClearsEveryPaneButRetainsQueries(t *testing.T) {
	subviews := []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'o', mode: ui.ModeBeadsOpen, query: "open-query"},
		{key: 'r', mode: ui.ModeBeadsReady, query: "ready-query"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "blocked-query"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "progress-query"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "closed-query"},
	}
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 1})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	for _, subview := range subviews {
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
		}
		m = applyBeadsResult(t, m, subview.mode, true, []beadsquery.Bead{
			{ID: subview.query + "-0", Title: subview.query},
			{ID: subview.query + "-1", Title: subview.query},
		})
		m = setBeadsQuery(t, m, subview.query)
		m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	before := listRequests(m)
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, cmd := update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if cmd == nil || m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("repo change = cmd %T mode %v, want Closed fetch", cmd, m.Mode())
	}
	for _, subview := range subviews {
		if m.ListRequest(subview.mode) == before[subview.mode] {
			t.Fatalf("repo change did not invalidate %v request", subview.mode)
		}
		if len(m.Beads(subview.mode)) != 0 || m.BeadsAvailable(subview.mode) ||
			m.BeadsSelected(subview.mode) != 0 || m.BeadsScroll(subview.mode) != 0 {
			t.Fatalf("repo change left state in %v: rows=%#v available=%v selected=%d scroll=%d",
				subview.mode, m.Beads(subview.mode), m.BeadsAvailable(subview.mode), m.BeadsSelected(subview.mode), m.BeadsScroll(subview.mode))
		}
		wantPending := subview.mode == ui.ModeBeadsClosed
		if m.BeadsPending(subview.mode) != wantPending {
			t.Fatalf("repo change pending(%v) = %v, want %v", subview.mode, m.BeadsPending(subview.mode), wantPending)
		}
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	for i := len(subviews) - 1; i >= 0; i-- {
		subview := subviews[i]
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
		}
		if got := m.ItemSearch(); got != subview.query {
			t.Fatalf("repo change query for %v = %q, want %q", subview.mode, got, subview.query)
		}
	}
}

func TestBeadsUnavailableResultClearsOnlyCurrentPaneAndRetainsQuery(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 1})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	openRows := []beadsquery.Bead{{ID: "open-0", Title: "Open"}, {ID: "open-1", Title: "Open"}}
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m = setBeadsQuery(t, m, "open")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "ready-0", Title: "Ready"}, {ID: "ready-1", Title: "Ready"}})
	m = setBeadsQuery(t, m, "ready")
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, false, []beadsquery.Bead{{ID: "ignored"}})

	assertBeadsPaneState(t, m, ui.ModeBeadsReady, "ready", 0, 0, 0)
	if m.BeadsAvailable(ui.ModeBeadsReady) || m.BeadsPending(ui.ModeBeadsReady) {
		t.Fatalf("unavailable Ready state = available %v pending %v, want false/false", m.BeadsAvailable(ui.ModeBeadsReady), m.BeadsPending(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 2 || got[1].ID != "open-1" || !m.BeadsAvailable(ui.ModeBeadsOpen) || m.BeadsSelected(ui.ModeBeadsOpen) != 1 || m.BeadsScroll(ui.ModeBeadsOpen) != 1 {
		t.Fatalf("unavailable Ready result changed inactive Open pane: rows=%#v available=%v selected=%d scroll=%d",
			got, m.BeadsAvailable(ui.ModeBeadsOpen), m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen))
	}
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	if got := m.ItemSearch(); got != "open" {
		t.Fatalf("inactive Open query = %q after return, want open", got)
	}
}

// A pending query hides the retained rows, so the pane must report loading even
// when the retained rows no longer match the subview's filter. Otherwise a
// refetch answers "no results" about rows it is in the middle of replacing.
func TestBeadsPendingViewReportsLoadingUnderZeroMatchQuery(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-0", Title: "Alpha"}})
	m = setBeadsQuery(t, m, "zzz")
	if view := viewContent(m); !strings.Contains(view, "No bead results for zzz") {
		t.Fatalf("settled zero-match view did not report the filter verdict:\n%s", view)
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	if !m.BeadsPending(ui.ModeBeadsOpen) {
		t.Fatal("f5 did not leave Open pending")
	}
	view := viewContent(m)
	if !strings.Contains(view, "loading open beads") || strings.Contains(view, "No bead results") {
		t.Fatalf("pending zero-match view did not report loading:\n%s", view)
	}
}

// Changing repositories while Active Flows overlays the content pane still
// switches which repository Beads describes, so no Beads pane may keep the old
// repository's rows or cursor once the surface is dismissed.
func TestBeadsRepoChangeUnderActiveFlowsClearsPaneState(t *testing.T) {
	subviews := []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'o', mode: ui.ModeBeadsOpen, query: "open"},
		{key: 'r', mode: ui.ModeBeadsReady, query: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "blocked"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "closed"},
	}
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	opts.ListFlows = func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	for _, subview := range subviews {
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
		}
		m = applyBeadsResult(t, m, subview.mode, true, []beadsquery.Bead{
			{ID: subview.query + "-0", Title: subview.query},
			{ID: subview.query + "-1", Title: subview.query},
			{ID: subview.query + "-2", Title: subview.query},
			{ID: subview.query + "-3", Title: subview.query},
		})
		m = setBeadsQuery(t, m, subview.query)
		for range 3 {
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
		}
	}

	before := listRequests(m)
	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	for _, subview := range subviews {
		if m.ListRequest(subview.mode) == before[subview.mode] {
			t.Fatalf("Active Flows repo change did not invalidate %v request", subview.mode)
		}
		wantPending := subview.mode == ui.ModeBeadsClosed
		if len(m.Beads(subview.mode)) != 0 || m.BeadsAvailable(subview.mode) || m.BeadsPending(subview.mode) != wantPending ||
			m.BeadsSelected(subview.mode) != 0 || m.BeadsScroll(subview.mode) != 0 {
			t.Fatalf("Active Flows repo change left state in %v: rows=%#v available=%v pending=%v selected=%d scroll=%d",
				subview.mode, m.Beads(subview.mode), m.BeadsAvailable(subview.mode), m.BeadsPending(subview.mode),
				m.BeadsSelected(subview.mode), m.BeadsScroll(subview.mode))
		}
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})

	if m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("active flows toggle returned to mode %v, want remembered Beads Closed", m.Mode())
	}
	for i := len(subviews) - 1; i >= 0; i-- {
		subview := subviews[i]
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{subview.key})})
		}
		assertBeadsPaneState(t, m, subview.mode, subview.query, 0, 0, 0)
	}
}

// While a subview is pending its rows are hidden behind the loading message, so
// cursor keys must not silently move a selection the user cannot see.
func TestBeadsCursorIsInertWhilePending(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + ui.TerminalChipRows + 2})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"},
	})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	wantSelected, wantScroll := m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen)

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyF5})
	for _, key := range []rune{tea.KeyDown, tea.KeyDown, tea.KeyUp} {
		m, _ = update(m, tea.KeyPressMsg{Code: key})
	}
	if got, gotScroll := m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen); got != wantSelected || gotScroll != wantScroll {
		t.Fatalf("cursor moved behind the loading placeholder: selected %d scroll %d, want %d/%d", got, gotScroll, wantSelected, wantScroll)
	}

	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"},
	})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.BeadsSelected(ui.ModeBeadsOpen); got != wantSelected+1 {
		t.Fatalf("cursor stayed inert after the result landed: selected %d, want %d", got, wantSelected+1)
	}
}

// The remembered subview is view state, not repo-owned data, so a repo change
// must not send Beads back to Ready.
func TestBeadsRememberedSubviewSurvivesRepoChange(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'c'})})
	m, _ = update(m, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	if m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("key 2 after repo change = %v, want remembered Beads Closed", m.Mode())
	}
}

// esc is the primary way a filter is undone, and it must restore the subview's
// full row set rather than only leaving search-edit mode.
func TestBeadsFilterClearsWithEsc(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 3})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-needle", Title: "Needle"},
		{ID: "bd-other", Title: "Other"},
	})
	m = setBeadsQuery(t, m, "needle")
	if got := m.Beads(ui.ModeBeadsOpen); len(got) != 1 || got[0].ID != "bd-needle" {
		t.Fatalf("filtered Open rows = %#v, want the needle row", got)
	}

	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.SearchActive() || m.ItemSearch() != "" {
		t.Fatalf("esc left filter state: active %v query %q", m.SearchActive(), m.ItemSearch())
	}
	if got := m.Beads(ui.ModeBeadsOpen); len(got) != 2 {
		t.Fatalf("esc did not restore the unfiltered Open rows: %#v", got)
	}
}

func TestBeadsSubviews_ModelViewUsesExactQuietStates(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
		name string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, name: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, name: "blocked"},
		{key: 'o', mode: ui.ModeBeadsOpen, name: "open"},
		{key: 'i', mode: ui.ModeBeadsInProgress, name: "in-progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, name: "closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
			if tt.mode != ui.ModeBeadsReady {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{tt.key})})
			}
			if view := viewContent(m); !strings.Contains(view, "loading "+tt.name+" beads") || strings.Contains(view, "beads not configured") {
				t.Fatalf("pending view has wrong quiet state:\n%s", view)
			}
			m = applyBeadsResult(t, m, tt.mode, true, nil)
			if view := viewContent(m); !strings.Contains(view, "no "+tt.name+" beads") || strings.Contains(view, "beads not configured") {
				t.Fatalf("empty view has wrong quiet state:\n%s", view)
			}
			m = applyBeadsResult(t, m, tt.mode, false, []beadsquery.Bead{{ID: "bd-ignored"}})
			if view := viewContent(m); !strings.Contains(view, "beads not configured") || strings.Contains(view, "no "+tt.name+" beads") {
				t.Fatalf("unavailable view has wrong quiet state:\n%s", view)
			}
			if m.TransientError() != "" {
				t.Fatalf("unavailable state exposed status %q", m.TransientError())
			}
		})
	}
}
