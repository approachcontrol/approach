package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
)

func TestStatusBar_BranchesModeContainsIndicatorLegend(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	for _, legend := range []string{"✔ clean", "● ahead/behind", "● dirty", "● no upstream", "merged"} {
		if !strings.Contains(bar, legend) {
			t.Errorf("branches mode status bar should contain legend %q", legend)
		}
	}
}

func TestStatusBar_IndicatorLegendSpacing(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	for _, pair := range [][2]string{
		{"clean", "●"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar[a+len(pair[0]):], pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : a+len(pair[0])+b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between legend items, got %q", gap)
		}
	}
}

func TestStatusBar_StashesModeOmitsIndicatorLegend(t *testing.T) {
	bar := RenderStatusBar(120, 3, 0, 1, true, false, false)
	if strings.Contains(bar, "clean") {
		t.Error("stashes mode status bar should not contain indicator legend")
	}
}

func TestStatusBar_PipeSeparatesLegendAndHints(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 1, true, false, false)
	upstreamIdx := strings.Index(bar, "no upstream")
	tabIdx := strings.Index(bar, "tab: pane")
	if upstreamIdx == -1 || tabIdx == -1 {
		t.Fatalf("expected both 'no upstream' and 'tab: pane' in bar: %q", bar)
	}
	between := bar[upstreamIdx+len("no upstream") : tabIdx]
	if !strings.Contains(between, "|") {
		t.Errorf("expected pipe separator between legend and hints, got %q", between)
	}
}

func TestStatusBar_TabAndQuitBeforeOtherHints(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false)
	tabIdx := strings.Index(bar, "tab: pane")
	tIdx := strings.Index(bar, "t: terminal")
	if tabIdx == -1 || tIdx == -1 {
		t.Fatalf("expected both hints in bar: %q", bar)
	}
	if tabIdx > tIdx {
		t.Error("tab: pane should appear before t: terminal")
	}
	qIdx := strings.Index(bar, "q/esc: quit")
	if qIdx > tIdx {
		t.Error("q/esc: quit should appear before t: terminal")
	}
}

func TestStatusBar_ActionHintsHiddenWhenLeftPaneActive(t *testing.T) {
	bar := RenderStatusBar(120, 2, 0, 0, true, false, false) // activePane=0 (left), destructive=true
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code", "d: delete"} {
		if strings.Contains(bar, hint) {
			t.Errorf("hint %q should be hidden when left pane is active", hint)
		}
	}
	// tab and q/esc should still appear
	for _, hint := range []string{"tab: pane", "q/esc: quit"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("hint %q should appear even when left pane is active", hint)
		}
	}
}

func TestStatusBar_ActionHintsShownWhenRightPaneActive(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false) // activePane=1 (right)
	for _, hint := range []string{"n: new branch", "f: fetch", "t: terminal", "c: code", "d: delete"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("hint %q should be shown when right pane is active", hint)
		}
	}
	if strings.Contains(bar, "F: pull") {
		t.Error("public status bar helper should not show pull for branches mode without a selected worktree branch")
	}
}

func TestRender_BranchesModeShowsPullWhenAvailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main", IsWorktree: true}, WorktreePath: "/a"},
		},
		ActivePane:     1,
		FetchAvailable: true,
		PullAvailable:  true,
	})
	for _, hint := range []string{"f: fetch", "F: pull"} {
		if !strings.Contains(view, hint) {
			t.Errorf("branches render should contain %q when available", hint)
		}
	}
}

func TestRender_LeftPaneShowsFetchVisibleWhenReposExist(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                10,
		Mode:                  1,
		ActivePane:            0,
		FetchVisibleAvailable: true,
	})
	if !strings.Contains(view, "f: fetch visible") {
		t.Fatalf("left-pane render should expose fetch-visible hint, got:\n%s", view)
	}
}

func TestRender_SessionsModeShowsHeaderAndRows(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    180,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:     sessions.ProviderCodex,
			SessionID:    "codex-session-1",
			Status:       "ended",
			RepoPath:     "/dev/wtui",
			WorktreePath: "/dev/wtui-worktrees/sessions",
			Branch:       "feature/sessions",
			Summary:      "Implement session capture",
		}},
		ActivePane:      1,
		SessionSelected: 0,
	})

	for _, want := range []string{"[6] sessions", "codex", "feature/sessions", "sessions", "ended", "Implement session capture"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sessions view missing %q:\n%s", want, view)
		}
	}
}

func TestRender_SessionsModeEmptyMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{name: "empty", message: "No sessions"},
		{name: "fetch failure", message: "Could not load sessions; see status bar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:             []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
				Selected:          0,
				Width:             120,
				Height:            10,
				Mode:              ModeSessions,
				RightEmptyMessage: tc.message,
			})
			if !strings.Contains(view, tc.message) {
				t.Fatalf("sessions empty view missing %q:\n%s", tc.message, view)
			}
		})
	}
}

func TestRender_SessionsModeShowsTranscriptShortcut(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/dev/wtui", DisplayName: "wtui"}},
		Selected: 0,
		Width:    120,
		Height:   10,
		Mode:     ModeSessions,
		Sessions: []sessions.SessionRecord{{
			Provider:  sessions.ProviderCodex,
			SessionID: "codex-session-1",
			Status:    "ended",
			RepoPath:  "/dev/wtui",
			Summary:   "Implement session capture",
		}},
		ActivePane:      1,
		SessionSelected: 0,
	})
	if !strings.Contains(view, "enter: transcript") {
		t.Fatalf("sessions view should expose transcript shortcut:\n%s", view)
	}
}

func TestRender_LeftPaneShortcutPaneShowsFetchVisibleWhenReposExist(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 120,
		Height:                24,
		Mode:                  1,
		ActivePane:            0,
		FetchVisibleAvailable: true,
	})
	if !strings.Contains(view, "f: fetch visible") {
		t.Fatalf("left-pane shortcut pane should expose fetch-visible hint, got:\n%s", view)
	}
}

func TestRender_LeftPaneHidesFetchVisibleWhenNoReposVisible(t *testing.T) {
	view := Render(RenderParams{
		Width:      120,
		Height:     10,
		Mode:       1,
		ActivePane: 0,
	})
	if strings.Contains(view, "f: fetch visible") {
		t.Fatalf("empty left-pane render should hide fetch-visible hint, got:\n%s", view)
	}
}

func TestStatusBar_WorktreesModeShowsNewWorktreeHint(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	if !strings.Contains(bar, "n: new worktree") {
		t.Fatalf("expected new worktree hint in worktrees mode, got %q", bar)
	}
	if !strings.Contains(bar, "P: PR") {
		t.Fatalf("expected PR worktree hint in worktrees mode, got %q", bar)
	}
}

func TestStatusBar_ShowsSetAgentHint(t *testing.T) {
	for _, activePane := range []int{0, 1} {
		bar := RenderStatusBar(160, 1, 0, activePane, false, false, false)
		if !strings.Contains(bar, "A: set agent") {
			t.Fatalf("expected set agent hint for activePane %d, got %q", activePane, bar)
		}
	}
}

func TestRender_WorktreesModeShowsAgentHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             140,
		Height:            10,
		Mode:              1,
		ActivePane:        1,
		Worktrees:         []gitquery.Worktree{{Path: "/a", BranchName: "main", IsMain: true}},
		AgentAvailable:    true,
		NewAgentAvailable: true,
	})
	for _, hint := range []string{"A: set agent", "a: agent", "N: new+agent"} {
		if !strings.Contains(view, hint) {
			t.Fatalf("expected worktrees view to contain %q, got %q", hint, view)
		}
	}
}

func TestRender_StaleWorktreeShowsSetAgentHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             140,
		Height:            10,
		Mode:              1,
		ActivePane:        1,
		Worktrees:         []gitquery.Worktree{{Path: "/gone", BranchName: "gone", Stale: true}},
		NewAgentAvailable: true,
	})
	for _, hint := range []string{"A: set agent", "N: new+agent"} {
		if !strings.Contains(view, hint) {
			t.Fatalf("expected stale worktree view to contain %q, got %q", hint, view)
		}
	}
}

func TestRender_BranchesModeShowsAgentHintOnlyWhenTargetAvailable(t *testing.T) {
	params := RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      140,
		Height:     10,
		Mode:       2,
		ActivePane: 1,
		Branches:   []gitquery.BranchRow{{Branch: gitquery.Branch{Name: "feat"}}},
	}
	view := Render(params)
	if strings.Contains(view, "a: agent") {
		t.Fatalf("bare branch should not show agent hint, got %q", view)
	}

	params.AgentAvailable = true
	params.Branches = []gitquery.BranchRow{{Branch: gitquery.Branch{Name: "main", IsWorktree: true}, WorktreePath: "/a"}}
	view = Render(params)
	if !strings.Contains(view, "a: agent") {
		t.Fatalf("checked-out branch should show agent hint, got %q", view)
	}
}

func TestStatusBar_WorktreeInputOverlayShowsInputHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, OverlayWorktreeInput, 1, false, false, false)
	for _, hint := range []string{"enter: create", "esc: cancel", "backspace: delete"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("expected hint %q in input overlay bar %q", hint, bar)
		}
	}
}

func TestStatusBar_KeyHintSpacingIs2(t *testing.T) {
	bar := RenderStatusBar(160, 2, 0, 1, true, false, false)
	for _, pair := range [][2]string{
		{"tab: pane", "q/esc: quit"},
		{"d: delete", "f: fetch"},
		{"t: terminal", "c: code"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar, pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between %q and %q, got %q", pair[0], pair[1], gap)
		}
	}
}

func TestModeHeader_ShowsActiveMode(t *testing.T) {
	header := renderModeHeader(1, 60)
	if !strings.Contains(header, "[1] worktrees") {
		t.Error("mode header should show active mode 1 bracketed")
	}
	if strings.Contains(header, "[2]") {
		t.Error("inactive mode 2 should not be bracketed")
	}
	header = renderModeHeader(3, 60)
	if !strings.Contains(header, "[3] stashes") {
		t.Error("mode header should show active mode 3 bracketed")
	}
}

func TestModeHeader_HasSeparatorLine(t *testing.T) {
	header := renderModeHeader(1, 40)
	lines := strings.Split(header, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected mode header to have at least 2 lines, got %d", len(lines))
	}
	// Second line should be a separator (dashes or similar)
	separator := lines[1]
	if !strings.Contains(separator, "─") {
		t.Errorf("expected separator line with ─ chars, got %q", separator)
	}
}

func TestRender_ModeHeaderInRightPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
	})
	if !strings.Contains(view, "[1] worktrees") {
		t.Error("render should contain mode header '[1] worktrees' in right pane")
	}
}

func TestRender_WideLayoutShowsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		FetchAvailable:   true,
		PullAvailable:    true,
	})

	for _, want := range []string{"Shortcuts", "Worktrees", "Global", "Navigate", "Actions", "n", "new worktree", "F", "pull"} {
		if !strings.Contains(view, want) {
			t.Errorf("wide render should include shortcut pane text %q", want)
		}
	}
}

func TestRender_WideLayoutReplacesFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 1,
	})

	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if strings.Contains(footer, "tab: pane") || strings.Contains(footer, "q/esc: quit") {
		t.Fatalf("wide render footer should not carry shortcut hints, got %q", footer)
	}
	if !strings.Contains(view, "tab:") || !strings.Contains(view, "pane") {
		t.Fatal("wide render should still expose global shortcuts in the shortcut pane")
	}
}

func TestRender_NarrowLayoutKeepsFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   18,
		Mode:     ModeWorktrees,
	})

	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "tab: pane") {
		t.Fatalf("narrow render should keep footer hints, got %q", footer)
	}
	if strings.Contains(view, "Shortcuts") {
		t.Fatal("narrow render should not reserve a shortcut pane")
	}
}

func TestRender_ShortWideLayoutFallsBackToFooterHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   12,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		FetchAvailable:   true,
		PullAvailable:    true,
	})

	if strings.Contains(view, "Shortcuts") {
		t.Fatal("wide but short render should not replace footer hints with a truncated shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	for _, want := range []string{"tab: pane", "n: new worktree", "F: pull", "c: code"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("short wide footer should retain %q, got %q", want, footer)
		}
	}
}

func TestRender_HistoryShortcutPaneOmitsDestructiveMode(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeHistory,
		ActivePane: 1,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("history wide render should use shortcut pane")
	}
	if strings.Contains(view, "D: destructive mode") {
		t.Fatal("history shortcut pane should not advertise destructive mode")
	}
}

func TestRender_LeftPaneShortcutPaneOmitsModeNumberHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 0,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide render should use shortcut pane")
	}
	if strings.Contains(view, "1-5: switch views") {
		t.Fatal("left-pane shortcut pane should not advertise right-pane-only mode number keys")
	}
}

func TestRender_BranchShortcutPaneKeepsLegend(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   24,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "feature", HasUpstream: false, Dirty: true, IsWorktree: true}, WorktreePath: "/a-feature"},
		},
		BranchSelected: 0,
		ActivePane:     1,
		Destructive:    true,
		FetchAvailable: true,
		PullAvailable:  true,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("branch wide render should use shortcut pane when the full shortcut list fits")
	}
	for _, want := range []string{"Legend", "clean", "ahead/behind", "dirty", "no upstream", "merged", "d: delete", "F: pull"} {
		if !strings.Contains(view, want) {
			t.Fatalf("branch shortcut pane should include %q, got:\n%s", want, view)
		}
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if strings.Contains(footer, "tab: pane") || strings.Contains(footer, "no upstream") {
		t.Fatalf("branch wide footer should not duplicate shortcut or legend hints, got %q", footer)
	}
}

func TestRender_SearchActiveSuppressesShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:        []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:     0,
		Width:        120,
		Height:       18,
		Mode:         ModeWorktrees,
		ActivePane:   1,
		SearchActive: true,
		ItemSearch:   "feat",
	})

	if strings.Contains(view, "Shortcuts") {
		t.Fatal("active search should suppress normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "/ items: feat") || !strings.Contains(footer, "enter: keep") {
		t.Fatalf("active search footer should show search controls, got %q", footer)
	}
}

func TestRender_FilteredItemStateKeepsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 1,
		ItemSearch: "feat",
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("filtered item state should keep normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "filtered items: feat") || !strings.Contains(footer, "esc: clear") {
		t.Fatalf("filtered footer should show filter controls, got %q", footer)
	}
}

func TestRender_FilteredRepoStateKeepsShortcutPane(t *testing.T) {
	view := Render(RenderParams{
		Repos:      []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:   0,
		Width:      120,
		Height:     18,
		Mode:       ModeWorktrees,
		ActivePane: 0,
		RepoSearch: "alp",
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("filtered repo state should keep normal shortcut pane")
	}
	lines := strings.Split(view, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "filtered repos: alp") || !strings.Contains(footer, "esc: clear") {
		t.Fatalf("filtered repo footer should show filter controls, got %q", footer)
	}
}

func TestRender_LeftPaneOmitsRightPaneActions(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		forbidden []string
	}{
		{name: "stashes", mode: ModeStashes, forbidden: []string{"enter: diff", "d: drop"}},
		{name: "history", mode: ModeHistory, forbidden: []string{"enter: diff", "y: copy hash", "t: terminal", "c: code"}},
		{name: "reflog", mode: ModeReflog, forbidden: []string{"enter: diff", "y: copy hash"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:       []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:    0,
				Width:       120,
				Height:      18,
				Mode:        tt.mode,
				ActivePane:  0,
				Destructive: true,
			})

			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide left-pane render should still show global/navigation shortcuts")
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(view, forbidden) {
					t.Fatalf("left-pane %s render should not advertise %q, got:\n%s", tt.name, forbidden, view)
				}
			}
		})
	}
}

func TestRender_WorktreeRootOmitsDeleteHint(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    120,
		Height:   18,
		Mode:     ModeWorktrees,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
		Destructive:      true,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide worktree render should use shortcut pane")
	}
	if strings.Contains(view, "d: delete") {
		t.Fatalf("root worktree should not advertise delete, got:\n%s", view)
	}
}

func TestRender_WorktreeMoveHintAvailability(t *testing.T) {
	tests := []struct {
		name     string
		worktree gitquery.Worktree
		canMove  bool
		wantMove bool
	}{
		{
			name:     "movable linked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat"},
			canMove:  true,
			wantMove: true,
		},
		{
			name:     "dirty linked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Dirty: true},
			canMove:  true,
			wantMove: true,
		},
		{
			name:     "main worktree",
			worktree: gitquery.Worktree{Path: "/a", BranchName: "main", IsMain: true},
		},
		{
			name:     "stale worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Stale: true},
		},
		{
			name:     "locked worktree",
			worktree: gitquery.Worktree{Path: "/a-worktrees/feat", BranchName: "feat", Locked: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:              0,
				Width:                 120,
				Height:                18,
				Mode:                  ModeWorktrees,
				Worktrees:             []gitquery.Worktree{tt.worktree},
				WorktreeSelected:      0,
				ActivePane:            1,
				WorktreeMoveAvailable: tt.canMove,
			})
			hasMove := strings.Contains(view, "m: move")
			if hasMove != tt.wantMove {
				t.Fatalf("move hint visibility mismatch, want %v got %v:\n%s", tt.wantMove, hasMove, view)
			}
		})
	}
}

func TestRender_NarrowWorktreeFooterIncludesMoveWhenAvailable(t *testing.T) {
	view := Render(RenderParams{
		Repos:                 []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:              0,
		Width:                 100,
		Height:                18,
		Mode:                  ModeWorktrees,
		Worktrees:             []gitquery.Worktree{{Path: "/a-worktrees/feat", BranchName: "feat"}},
		WorktreeSelected:      0,
		ActivePane:            1,
		WorktreeMoveAvailable: true,
	})
	if strings.Contains(view, "Shortcuts") {
		t.Fatal("narrow render should keep shortcuts in footer")
	}
	footer := strings.Split(view, "\n")[len(strings.Split(view, "\n"))-1]
	if !strings.Contains(footer, "m: move") {
		t.Fatalf("narrow footer should include move hint, got %q", footer)
	}
}

func TestRender_BranchShortcutPaneShowsDiffButOmitsRootDelete(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   24,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main", HasUpstream: true, Dirty: true, IsWorktree: true}, WorktreePath: "/a"},
		},
		BranchSelected: 0,
		ActivePane:     1,
		Destructive:    true,
	})

	if !strings.Contains(view, "enter: diff") {
		t.Fatalf("dirty branch worktree should advertise diff action, got:\n%s", view)
	}
	if strings.Contains(view, "d: delete") {
		t.Fatalf("root branch should not advertise delete action, got:\n%s", view)
	}
}

func TestRender_BranchWithoutWorktreeOmitsOpenHints(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    160,
		Height:   24,
		Mode:     ModeBranches,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "feature", HasUpstream: true}},
		},
		BranchSelected: 0,
		ActivePane:     1,
	})

	if !strings.Contains(view, "Shortcuts") {
		t.Fatal("wide branch render should use shortcut pane")
	}
	for _, forbidden := range []string{"t: terminal", "c: code"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("non-worktree branch should not advertise %q, got:\n%s", forbidden, view)
		}
	}
}

func TestRender_EmptyItemPanesOmitItemActions(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		forbidden []string
	}{
		{name: "stashes", mode: ModeStashes, forbidden: []string{"enter: diff", "d: drop"}},
		{name: "history", mode: ModeHistory, forbidden: []string{"enter: diff", "y: copy hash"}},
		{name: "reflog", mode: ModeReflog, forbidden: []string{"enter: diff", "y: copy hash"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := Render(RenderParams{
				Repos:       []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
				Selected:    0,
				Width:       120,
				Height:      18,
				Mode:        tt.mode,
				ActivePane:  1,
				Destructive: true,
			})

			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide empty-pane render should still show global/navigation shortcuts")
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(view, forbidden) {
					t.Fatalf("empty %s render should not advertise %q, got:\n%s", tt.name, forbidden, view)
				}
			}
		})
	}
}

func TestRender_NonWorktreeModesShowSyncHintsWhenAvailable(t *testing.T) {
	tests := []struct {
		name   string
		params RenderParams
	}{
		{
			name: "stashes",
			params: RenderParams{
				Mode:          ModeStashes,
				Stashes:       []gitquery.Stash{{Index: 0, Date: "2026-01-01", Message: "wip"}},
				StashSelected: 0,
			},
		},
		{
			name: "history",
			params: RenderParams{
				Mode:           ModeHistory,
				Commits:        []gitquery.Commit{{Hash: "abc1234", Author: "alice", Date: "today", Subject: "commit"}},
				CommitSelected: 0,
			},
		},
		{
			name: "reflog",
			params: RenderParams{
				Mode:           ModeReflog,
				Reflogs:        []gitquery.ReflogEntry{{Hash: "abc1234", Selector: "HEAD@{0}", Date: "today", Subject: "checkout"}},
				ReflogSelected: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := tt.params
			params.Repos = []scanner.Repo{{Path: "/a", DisplayName: "alpha"}}
			params.Selected = 0
			params.Width = 120
			params.Height = 18
			params.ActivePane = 1
			params.FetchAvailable = true
			params.PullAvailable = true

			view := Render(params)
			if !strings.Contains(view, "Shortcuts") {
				t.Fatal("wide render should use shortcut pane")
			}
			for _, want := range []string{"f: fetch", "F: pull"} {
				if !strings.Contains(view, want) {
					t.Fatalf("%s render should include %q when available, got:\n%s", tt.name, want, view)
				}
			}
		})
	}
}

func TestRepoList_ScrollsWhenSelectionExceedsHeight(t *testing.T) {
	repos := []scanner.Repo{
		{Path: "/a", DisplayName: "alpha"},
		{Path: "/b", DisplayName: "bravo"},
		{Path: "/c", DisplayName: "charlie"},
		{Path: "/d", DisplayName: "delta"},
		{Path: "/e", DisplayName: "echo"},
	}
	// Height of 3 means only 3 visible at a time; scroll=2 shows repos 2-4
	lines := renderRepoList(repos, 4, 2, LeftPaneWidth-2, 3, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "echo") {
		t.Error("selected item 'echo' should be visible")
	}
	if strings.Contains(joined, "alpha") {
		t.Error("'alpha' should be scrolled off the top")
	}
}

func TestRepoList_TruncatesLongNames(t *testing.T) {
	width := LeftPaneWidth - 2
	repos := []scanner.Repo{
		{Path: "/a", DisplayName: "this-is-a-very-long-repository-name-that-exceeds-width"},
	}
	lines := renderRepoList(repos, 0, 0, width, 3, "")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			t.Errorf("line %d width %d exceeds pane width %d", i, lipgloss.Width(line), width)
		}
	}
}

func TestStashPane_LongMessageAlwaysShowsTwoLines(t *testing.T) {
	width := 50
	longMsg := "this is a very long stash message that should wrap to a second line always"
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: longMsg},
	}
	// Not selected (selected=-1): should still show 2 lines for the long message
	lines := renderStashPane(stashes, -1, 0, width, 10)
	// Count non-empty lines
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty < 2 {
		t.Errorf("expected at least 2 non-empty lines for long stash message, got %d", nonEmpty)
	}
}

func TestStashPane_ShortMessageShowsOneLine(t *testing.T) {
	width := 50
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: "short"},
	}
	lines := renderStashPane(stashes, -1, 0, width, 10)
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		t.Errorf("expected 1 non-empty line for short stash message, got %d", nonEmpty)
	}
}

func TestStashPane_SelectedLongMessageHighlightsBothLines(t *testing.T) {
	width := 50
	// Message wraps to 2 lines but the remainder is short, so selected
	// (padded to full width) and unselected (unpadded) must differ.
	longMsg := "this is a long stash message that wraps ok"
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18 10:00:00", Message: longMsg},
	}
	// Render with stash selected vs not selected
	selLines := renderStashPane(stashes, 0, 0, width, 10)
	unselLines := renderStashPane(stashes, -1, 0, width, 10)

	// The continuation line (index 1) should differ between selected and
	// unselected renders — stashSelStyle.Width(width) pads the selected
	// continuation to full width, while the unselected one is unpadded.
	if selLines[1] == unselLines[1] {
		t.Error("continuation line should be styled differently when stash is selected")
	}
}

func TestStashPane_ScrollOffset(t *testing.T) {
	width := 50
	stashes := []gitquery.Stash{
		{Index: 0, Date: "2026-03-18", Message: "first"},
		{Index: 1, Date: "2026-03-17", Message: "second"},
		{Index: 2, Date: "2026-03-16", Message: "third"},
	}
	// scroll=1 should skip the first stash line
	lines := renderStashPane(stashes, 1, 1, width, 3)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "first") {
		t.Error("'first' should be scrolled off the top")
	}
	if !strings.Contains(joined, "second") {
		t.Error("'second' should be visible")
	}
}

func TestBranchPane_CleanBranchShowsGreenCheck(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, Ahead: 0, Behind: 0, Dirty: false}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "main") {
		t.Error("should contain branch name 'main'")
	}
	if !strings.Contains(joined, "✔") {
		t.Error("clean branch with upstream should show ✔")
	}
	if strings.Contains(joined, "●") {
		t.Error("clean branch with upstream should not show ●")
	}
}

func TestBranchPane_AheadBehindShowsYellowDotWithCounts(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feature/auth", HasUpstream: true, Ahead: 3, Behind: 1, Dirty: false}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("ahead/behind branch should show ● indicator")
	}
	if !strings.Contains(joined, "+3/-1") {
		t.Error("should show ahead/behind counts as +3/-1")
	}
	if strings.Contains(joined, "✔") {
		t.Error("ahead/behind branch should not show ✔")
	}
}

func TestBranchPane_DirtyShowsRedDotWithFileStats(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feature/wip", HasUpstream: true, Dirty: true, IsWorktree: true,
			FilesChanged: 3, LinesAdded: 10, LinesDeleted: 5}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("dirty branch should show ● indicator")
	}
	if !strings.Contains(joined, "3 files") {
		t.Error("dirty branch should show file count")
	}
	if !strings.Contains(joined, "+10") {
		t.Error("dirty branch should show lines added")
	}
	if !strings.Contains(joined, "-5") {
		t.Error("dirty branch should show lines deleted")
	}
}

func TestBranchPane_NoUpstreamShowsPurpleDot(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "local-only", HasUpstream: false}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("no-upstream branch should show ● indicator")
	}
	if strings.Contains(joined, "✔") {
		t.Error("no-upstream branch should not show ✔")
	}
}

func TestBranchPane_UpstreamGoneShowsPurpleDot(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "stale", HasUpstream: true, UpstreamGone: true}},
	}
	lines := renderBranchPane(rows, 50, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "●") {
		t.Error("upstream-gone branch should show ● indicator")
	}
	if strings.Contains(joined, "✔") {
		t.Error("upstream-gone branch should not show ✔")
	}
}

func TestBranchPane_StacksAheadAndDirtyIndicators(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 2, Behind: 0, Dirty: true, IsWorktree: true,
			FilesChanged: 1, LinesAdded: 5, LinesDeleted: 2}},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	// Should have both +2/-0 (ahead) and 1 files (dirty)
	if !strings.Contains(joined, "+2/-0") {
		t.Error("stacked: should show ahead/behind counts")
	}
	if !strings.Contains(joined, "1 files") {
		t.Error("stacked: should show dirty file count")
	}
	// Should have two ● indicators
	if strings.Count(joined, "●") < 2 {
		t.Errorf("stacked: expected at least 2 dot indicators, got %d", strings.Count(joined, "●"))
	}
	if strings.Contains(joined, "✔") {
		t.Error("stacked: should not show ✔ when there are indicators")
	}
}

func TestBranchPane_WorktreeAnnotation(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/proj-feat"},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[/dev/proj-feat]") {
		t.Error("worktree branch should show [<path>] annotation")
	}
}

func TestBranchPane_DuplicateWorktreeAnnotation(t *testing.T) {
	b := gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}
	rows := []gitquery.BranchRow{
		{Branch: b, WorktreePath: "/dev/proj-feat"},
		{Branch: b, WorktreePath: "/tmp/proj-feat-copy", IsExpansion: true},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/proj-feat") || !strings.Contains(joined, "/tmp/proj-feat-copy") {
		t.Error("duplicate worktree branch should show both paths")
	}
	if strings.Contains(joined, "duplicate") || strings.Contains(joined, "wt:") {
		t.Error("duplicate worktree branch should not show labels")
	}
}

func TestBranchPane_DetachedWorktreeRow(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "(detached)", IsWorktree: true}, WorktreePath: "/tmp/wt-detached"},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(detached)") {
		t.Error("detached worktree should render as a detached row")
	}
	if !strings.Contains(joined, "[/tmp/wt-detached]") {
		t.Error("detached worktree should show its path annotation")
	}
}

func TestBranchPane_RootAnnotationUsesBlueStyle(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	blueRoot := rootStyle.Render("[root]")
	if !strings.Contains(joined, blueRoot) {
		t.Error("root label in branch pane should use blue rootStyle")
	}
}

func TestBranchPane_NonWorktreeNoAnnotation(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: false}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "[wt:") || strings.Contains(joined, "[duplicate:") {
		t.Error("non-worktree branch should not show worktree annotation")
	}
}

func TestRender_HighlightsSelectedBranch(t *testing.T) {
	// BranchSelected: 0 highlights first branch (clean), not the dirty one
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "clean"}},
			{Branch: gitquery.Branch{Name: "dirty", IsWorktree: true, Dirty: true}, WorktreePath: "/a"},
		},
		BranchSelected: 0,
		ActivePane:     1,
	})
	if !strings.Contains(view, "> clean") {
		t.Error("first branch should be highlighted when BranchSelected=0")
	}
	if strings.Contains(view, "> dirty") {
		t.Error("dirty branch should not be highlighted when BranchSelected=0")
	}
}

func TestRender_HighlightsSecondBranch(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "clean"}},
			{Branch: gitquery.Branch{Name: "dirty", IsWorktree: true, Dirty: true}, WorktreePath: "/a"},
		},
		BranchSelected: 1,
		ActivePane:     1,
	})
	if !strings.Contains(view, "> dirty") {
		t.Error("dirty branch should be highlighted when BranchSelected=1")
	}
	if strings.Contains(view, "> clean") {
		t.Error("clean branch should not be highlighted when BranchSelected=1")
	}
}

func TestRender_HidesCursorWhenLeftPaneActive(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     2,
		Branches: []gitquery.BranchRow{
			{Branch: gitquery.Branch{Name: "main"}},
		},
		BranchSelected: 0,
		ActivePane:     0,
	})
	if strings.Contains(view, "> main") {
		t.Error("branch cursor should be hidden when left pane is active")
	}
}

func TestBranchPane_CursorDoesNotShiftBranchName(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "first", HasUpstream: true}},
		{Branch: gitquery.Branch{Name: "second", HasUpstream: true}},
	}
	// Render with no selection (selected = -1)
	unselected := renderBranchPaneSelected(rows, -1, 0, 80, 10, "/dev/alpha")
	// Render with first selected
	selected := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")

	// Find position of "first" in both renders — should be at the same column
	unselIdx := strings.Index(unselected[0], "first")
	selIdx := strings.Index(selected[0], "first")
	if unselIdx == -1 || selIdx == -1 {
		t.Fatalf("branch name 'first' not found in output: unsel=%q sel=%q", unselected[0], selected[0])
	}
	if unselIdx != selIdx {
		t.Errorf("branch name shifts when selected: unselected col %d, selected col %d", unselIdx, selIdx)
	}
}

func TestBranchPane_UnpushedCommitsShown(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 2,
			Unpushed: []string{"abc1234 Fix bug", "def5678 Add feature"}}},
	}
	lines := renderBranchPane(rows, 60, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Fix bug") {
		t.Error("should show unpushed commit message")
	}
	if !strings.Contains(joined, "Add feature") {
		t.Error("should show second unpushed commit message")
	}
}

func TestBranchPane_MergedBranchShowsCleanupCandidate(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "merged-feat", HasUpstream: true, Merged: true, MergedInto: "main"}},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "merged-feat") {
		t.Error("should show merged branch name")
	}
	if !strings.Contains(joined, "merged") {
		t.Errorf("should show merged cleanup indicator, got %q", joined)
	}
	if strings.Contains(joined, "✔") {
		t.Errorf("merged branch should not also render clean-only indicator, got %q", joined)
	}
}

func TestBranchPane_UnpushedCapsAt5WithOverflow(t *testing.T) {
	msgs := make([]string, 8)
	for i := range msgs {
		msgs[i] = fmt.Sprintf("abc%d commit message %d", i, i)
	}
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, Ahead: 8, Unpushed: msgs}},
	}
	lines := renderBranchPane(rows, 60, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "and 3 more") {
		t.Error("should show 'and 3 more' overflow for 8 commits with cap of 5")
	}
	// Count lines that contain commit content
	var commitLines int
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.Contains(trimmed, "commit message") || strings.Contains(trimmed, "and 3 more") {
			commitLines++
		}
	}
	if commitLines != 6 {
		t.Errorf("expected 6 commit-related lines (5 + overflow), got %d", commitLines)
	}
}

func TestBranchPane_ScrollsToSelectedBranch(t *testing.T) {
	rows := make([]gitquery.BranchRow, 10)
	for i := range rows {
		rows[i] = gitquery.BranchRow{Branch: gitquery.Branch{Name: fmt.Sprintf("branch-%d", i)}}
	}
	// BranchScroll=8 with height=3 means we see branches 8 and 9
	lines := renderBranchPaneSelected(rows, 9, 8, 60, 3, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "branch-9") {
		t.Error("should show branch-9 when scrolled to see it")
	}
	if strings.Contains(joined, "branch-0") {
		t.Error("branch-0 should be scrolled out of view")
	}
}

func TestRender_CombinesPanesWithDivider(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
	})
	if !strings.Contains(view, "│") {
		t.Error("view should contain divider")
	}
	if !strings.Contains(view, "alpha") {
		t.Error("view should contain repo name")
	}
}

func TestRender_ConfirmDialogShowsPrompt(t *testing.T) {
	view := Render(RenderParams{
		Repos:         []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:         80,
		Height:        24,
		Mode:          1,
		Overlay:       OverlayConfirm,
		ConfirmPrompt: "Remove worktree /dev/alpha/feat? (y/n)",
	})
	if !strings.Contains(view, "Remove worktree /dev/alpha/feat") {
		t.Error("confirm dialog should show prompt text")
	}
	if !strings.Contains(view, "y/n") {
		t.Error("confirm dialog should show y/n hint")
	}
}

func TestRender_ForceConfirmDialogShowsPrompt(t *testing.T) {
	view := Render(RenderParams{
		Repos:         []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:         80,
		Height:        24,
		Mode:          1,
		Overlay:       OverlayConfirm,
		ConfirmPrompt: "Force delete /dev/alpha/feat? (y/n)",
		ConfirmForce:  true,
	})
	if !strings.Contains(view, "Force delete /dev/alpha/feat") {
		t.Error("force confirm dialog should show prompt text")
	}
}

func TestRender_WorktreeInputDialogShowsInputAndError(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     1,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPlaceholder: WorktreeInputPlaceholder,
		WorktreeInput:            "feature/new",
		WorktreeInputErr:         "already exists",
	})
	if !strings.Contains(view, "Create worktree from: feature/new") {
		t.Error("worktree input dialog should show typed input")
	}
	if !strings.Contains(view, "already exists") {
		t.Error("worktree input dialog should show error")
	}
}

func TestRender_WorktreeInputDialogShowsPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     1,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPlaceholder: WorktreeInputPlaceholder,
	})
	if !strings.Contains(view, "branch, tag, or new branch name") {
		t.Error("worktree input dialog should show placeholder when input is empty")
	}
}

func TestRender_WorktreeMoveInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     1,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPrompt:      WorktreeMovePrompt,
		WorktreeInputPlaceholder: WorktreeMoveInputPlaceholder,
	})
	if !strings.Contains(view, "Move worktree to:") {
		t.Error("move input dialog should show move prompt")
	}
	if !strings.Contains(view, WorktreeMoveInputPlaceholder) {
		t.Error("move input dialog should show move placeholder")
	}
}

func TestRender_BranchInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     2,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPrompt:      BranchPrompt,
		WorktreeInputPlaceholder: BranchInputPlaceholder,
	})
	if !strings.Contains(view, "Create branch:") {
		t.Error("branch input dialog should show branch-specific prompt")
	}
	if !strings.Contains(view, "branch name") {
		t.Error("branch input dialog should show branch-specific placeholder")
	}
}

func TestRender_PullRequestWorktreeInputDialogShowsPromptAndPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     1,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPrompt:      PRWorktreePrompt,
		WorktreeInputPlaceholder: PRWorktreeInputPlaceholder,
	})
	if !strings.Contains(view, "Create PR worktree from:") {
		t.Error("PR input dialog should show PR-specific prompt")
	}
	if !strings.Contains(view, "PR number or URL") {
		t.Error("PR input dialog should show PR-specific placeholder")
	}
}

func TestRender_AgentInputDialogUsesExplicitPlaceholder(t *testing.T) {
	view := Render(RenderParams{
		Repos:                    []scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}},
		Width:                    80,
		Height:                   24,
		Mode:                     1,
		Overlay:                  OverlayWorktreeInput,
		WorktreeInputPrompt:      "Choose interactive helper",
		WorktreeInputPlaceholder: AgentInputPlaceholder,
	})
	if !strings.Contains(view, "Choose interactive helper:") {
		t.Error("agent input dialog should show caller-provided prompt")
	}
	if !strings.Contains(view, AgentInputPlaceholder) {
		t.Error("agent input dialog should show caller-provided placeholder")
	}
}

func TestStatusBar_StashesModeHintsSpacing(t *testing.T) {
	bar := RenderStatusBar(120, 3, 0, 1, true, false, false)
	for _, hint := range []string{"f: fetch", "F: pull"} {
		if strings.Contains(bar, hint) {
			t.Errorf("stashes mode status bar should not contain %q", hint)
		}
	}
	for _, pair := range [][2]string{
		{"tab: pane", "q/esc: quit"},
		{"↑/↓ select", "enter: diff"},
		{"enter: diff", "d: drop"},
	} {
		a := strings.Index(bar, pair[0])
		b := strings.Index(bar, pair[1])
		if a == -1 || b == -1 {
			t.Errorf("expected both %q and %q in bar", pair[0], pair[1])
			continue
		}
		gap := bar[a+len(pair[0]) : b]
		if gap != "  " {
			t.Errorf("expected 2 spaces between %q and %q, got %q", pair[0], pair[1], gap)
		}
	}
}

func TestBranchPane_MultiWorktreeExpandsRows(t *testing.T) {
	b := gitquery.Branch{Name: "feat", HasUpstream: true, Unpushed: []string{"abc1234 Fix thing"}}
	rows := []gitquery.BranchRow{
		{Branch: b, WorktreePath: "/dev/feat-A"},
		{Branch: b, WorktreePath: "/dev/feat-B", IsExpansion: true},
	}
	lines := renderBranchPane(rows, 80, 10)
	joined := strings.Join(lines, "\n")
	// Both paths should appear
	if !strings.Contains(joined, "/dev/feat-A") {
		t.Error("should show first worktree path /dev/feat-A")
	}
	if !strings.Contains(joined, "/dev/feat-B") {
		t.Error("should show second worktree path /dev/feat-B")
	}
	// Unpushed commit should appear once (on first row), not on expansion row
	if strings.Count(joined, "Fix thing") != 1 {
		t.Errorf("unpushed commit should appear exactly once, got %d", strings.Count(joined, "Fix thing"))
	}
}

func TestBranchPane_MainWorktreeShowsRootLabelAfterIndicators(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "main", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[root]") {
		t.Errorf("main worktree branch should show [root] label, got: %q", joined)
	}
	// [root] should appear after the branch name, not before
	mainIdx := strings.Index(joined, "main")
	rootIdx := strings.Index(joined, "[root]")
	if mainIdx == -1 || rootIdx == -1 || rootIdx < mainIdx {
		t.Errorf("expected [root] after branch name 'main', got: %q", joined)
	}
}

func TestBranchPane_AdditionalWorktreeShowsPath(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "feat", HasUpstream: true, IsWorktree: true}, WorktreePath: "/dev/alpha-worktrees/feat"},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/alpha-worktrees/feat") {
		t.Errorf("additional worktree branch should show path, got: %q", joined)
	}
	if strings.Contains(joined, "[root]") {
		t.Error("additional worktree branch should not show [root]")
	}
}

func TestBranchPane_NonWorktreeBranchShowsNoLabel(t *testing.T) {
	rows := []gitquery.BranchRow{
		{Branch: gitquery.Branch{Name: "stale", HasUpstream: true}, WorktreePath: ""},
	}
	lines := renderBranchPaneSelected(rows, 0, 0, 80, 10, "/dev/alpha")
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "[root]") {
		t.Error("non-worktree branch should not show [root]")
	}
}

// --- History (mode 3) tests ---

func TestModeHeader_ShowsFiveModes(t *testing.T) {
	header := renderModeHeader(4, 80)
	if !strings.Contains(header, "[4] history") {
		t.Error("expected active '[4] history' in header")
	}
	if !strings.Contains(header, "1 worktrees") {
		t.Error("expected inactive '1 worktrees' in header")
	}
	if !strings.Contains(header, "2 branches") {
		t.Error("expected inactive '2 branches' in header")
	}
	if !strings.Contains(header, "3 stashes") {
		t.Error("expected inactive '3 stashes' in header")
	}
	if !strings.Contains(header, "5 reflog") {
		t.Error("expected inactive '5 reflog' in header")
	}
	// Test mode 5 active
	header5 := renderModeHeader(5, 80)
	if !strings.Contains(header5, "[5] reflog") {
		t.Error("expected active '[5] reflog' in header")
	}
	if !strings.Contains(header5, "4 history") {
		t.Error("expected inactive '4 history' in header when mode 5 active")
	}
}

func TestStatusBar_HistoryModeShowsHistoryHints(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, false, false, false)
	for _, hint := range []string{"enter: diff", "y: copy hash", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("mode 3 status bar should contain %q", hint)
		}
	}
}

func TestStatusBar_HistoryModeOmitsDeleteHint(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, true, false, false)
	if strings.Contains(bar, "d: delete") {
		t.Error("mode 3 status bar should not contain 'd: delete'")
	}
	if strings.Contains(bar, "d: drop") {
		t.Error("mode 3 status bar should not contain 'd: drop'")
	}
}

func TestStatusBar_HistoryModeOmitsDestructiveHint(t *testing.T) {
	bar := RenderStatusBar(120, 4, 0, 1, false, false, false)
	if strings.Contains(bar, "D: destructive mode") {
		t.Error("mode 3 status bar should not contain 'D: destructive mode'")
	}
}

func TestCommitPane_ShowsCommitDetails(t *testing.T) {
	commits := []gitquery.Commit{
		{Hash: "abc1234", Author: "alice", Date: "2 hours ago", Subject: "Fix login bug"},
		{Hash: "def5678", Author: "bob", Date: "3 days ago", Subject: "Add profile page"},
	}
	lines := renderCommitPane(commits, 0, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "abc1234") {
		t.Error("expected hash 'abc1234' in output")
	}
	if !strings.Contains(joined, "alice") {
		t.Error("expected author 'alice' in output")
	}
	if !strings.Contains(joined, "2 hours ago") {
		t.Error("expected date '2 hours ago' in output")
	}
	if !strings.Contains(joined, "Fix login bug") {
		t.Error("expected subject 'Fix login bug' in output")
	}
}

func TestCommitPane_ScrollsToSelectedCommit(t *testing.T) {
	commits := make([]gitquery.Commit, 20)
	for i := range commits {
		commits[i] = gitquery.Commit{
			Hash:    fmt.Sprintf("abc%04d", i),
			Author:  "test",
			Date:    "now",
			Subject: fmt.Sprintf("commit-%d", i),
		}
	}
	// Scroll past first 10, show 5 lines
	lines := renderCommitPane(commits, 12, 10, 80, 5)
	joined := strings.Join(lines, "\n")
	// commit-10 should be visible (it's at offset 0 after scroll)
	if !strings.Contains(joined, "commit-10") {
		t.Error("expected 'commit-10' visible after scroll")
	}
	// commit-9 should not be visible (before scroll)
	if strings.Contains(joined, "commit-9") {
		t.Error("expected 'commit-9' not visible after scroll")
	}
}

// --- Worktree pane ---

func TestWorktreePane_ShowsBranchName(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feature"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "main") {
		t.Error("expected 'main' branch name in worktree pane")
	}
	if !strings.Contains(joined, "feature") {
		t.Error("expected 'feature' branch name in worktree pane")
	}
}

func TestWorktreePane_DetachedLabel(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/detached", Detached: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(detached)") {
		t.Error("expected '(detached)' label for detached worktree")
	}
}

func TestWorktreePane_RootAnnotation(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[root]") {
		t.Error("expected '[root]' annotation for main worktree")
	}
}

func TestWorktreePane_ShowsPath(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/dev/alpha-feat") {
		t.Error("expected worktree path in output")
	}
}

func TestWorktreePane_DirtyIndicators(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "dirty", Dirty: true, FilesChanged: 3, LinesAdded: 10, LinesDeleted: 5},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "3 files") {
		t.Error("expected '3 files' in dirty indicator")
	}
	if !strings.Contains(joined, "+10") {
		t.Error("expected '+10' lines added in dirty indicator")
	}
	if !strings.Contains(joined, "-5") {
		t.Error("expected '-5' lines deleted in dirty indicator")
	}
}

func TestWorktreePane_CleanCheckmark(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "clean"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✔") {
		t.Error("expected checkmark for clean worktree")
	}
}

func TestWorktreePane_StaleIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "stale-branch", Stale: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✗") {
		t.Error("expected cross mark for stale worktree")
	}
	if !strings.Contains(joined, "stale") {
		t.Error("expected 'stale' label for stale worktree")
	}
}

func TestWorktreePane_LockedIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "🔒") {
		t.Error("expected lock icon for locked worktree")
	}
	if !strings.Contains(joined, "locked") {
		t.Error("expected 'locked' label for locked worktree")
	}
}

func TestWorktreePane_LockedReason(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true, LockReason: "on external drive"},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "on external drive") {
		t.Error("expected lock reason for locked worktree")
	}
}

func TestWorktreePane_LongLockReasonTruncated(t *testing.T) {
	longReason := strings.Repeat("x", 100)
	wts := []gitquery.Worktree{
		{Path: "/dev/locked", BranchName: "locked-branch", Locked: true, LockReason: longReason},
	}
	lines := renderWorktreePane(wts, -1, 0, 200, 10)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, longReason) {
		t.Error("expected long lock reason to be truncated, not rendered in full")
	}
	if !strings.Contains(joined, "…") {
		t.Error("expected ellipsis marker for truncated lock reason")
	}
}

func TestTruncateReason_UsesVisibleWidth(t *testing.T) {
	reason := strings.Repeat("界", MaxLockReasonWidth)
	truncated := truncateReason(reason, MaxLockReasonWidth)
	if lipgloss.Width(truncated) > MaxLockReasonWidth {
		t.Errorf("truncated reason width %d exceeds max %d", lipgloss.Width(truncated), MaxLockReasonWidth)
	}
	if !strings.Contains(truncated, "…") {
		t.Error("expected ellipsis marker for wide truncated lock reason")
	}
}

func TestWorktreePane_LockedStalePrefersLockedIndicator(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/gone", BranchName: "offline", Locked: true, Stale: true},
	}
	lines := renderWorktreePane(wts, -1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "🔒") || !strings.Contains(joined, "locked") {
		t.Error("expected locked indicator for locked stale worktree")
	}
	if strings.Contains(joined, "✗") || strings.Contains(joined, "stale") {
		t.Error("locked stale worktree should not render stale indicator")
	}
}

func TestWorktreePane_CursorHighlight(t *testing.T) {
	wts := []gitquery.Worktree{
		{Path: "/dev/alpha", BranchName: "main", IsMain: true},
		{Path: "/dev/alpha-feat", BranchName: "feat"},
	}
	lines := renderWorktreePane(wts, 1, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "> feat") {
		t.Error("expected '> feat' cursor on second item")
	}
}

func TestWorktreePane_ScrollOffset(t *testing.T) {
	wts := make([]gitquery.Worktree, 10)
	for i := range wts {
		wts[i] = gitquery.Worktree{Path: fmt.Sprintf("/dev/wt-%d", i), BranchName: fmt.Sprintf("branch-%d", i)}
	}
	lines := renderWorktreePane(wts, 9, 8, 80, 3)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "branch-9") {
		t.Error("expected 'branch-9' visible at scroll=8")
	}
	if strings.Contains(joined, "branch-0") {
		t.Error("expected 'branch-0' not visible at scroll=8")
	}
}

func TestStatusBar_WorktreesModeShowsNavHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	for _, hint := range []string{"tab: pane", "q/esc: quit", "↑/↓ select"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode status bar should contain %q", hint)
		}
	}
}

func TestStatusBar_WorktreesModeShowsDiffHintWhenDirty(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, true)
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should show %q when dirty", hint)
		}
	}
}

func TestStatusBar_WorktreesModeHidesDiffHintWhenClean(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	if strings.Contains(bar, "enter: diff") {
		t.Error("worktrees mode should NOT show 'enter: diff' when clean")
	}
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should show %q when clean and not stale", hint)
		}
	}
}

func TestStatusBar_WorktreesModeStaleHidesAllActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, true, true)
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code"} {
		if strings.Contains(bar, hint) {
			t.Errorf("worktrees mode should NOT show %q when stale", hint)
		}
	}
}

func TestStatusBar_WorktreesModeDestructiveNonStaleShowsDelete(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, false, false) // destructive, not stale
	if !strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode destructive non-stale should show 'd: delete'")
	}
	if strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode destructive non-stale should NOT show 'p: prune'")
	}
}

func TestStatusBar_WorktreesModeDestructiveStaleShowsPrune(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, true, false) // destructive, stale
	if !strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode destructive stale should show 'p: prune'")
	}
	if strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode destructive stale should NOT show 'd: delete'")
	}
}

func TestStatusBar_WorktreesModeReadOnlyShowsDestructiveHint(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false) // read-only
	if !strings.Contains(bar, "D: destructive mode") {
		t.Error("worktrees mode read-only should show 'D: destructive mode'")
	}
	if strings.Contains(bar, "d: delete") {
		t.Error("worktrees mode read-only should NOT show 'd: delete'")
	}
	if strings.Contains(bar, "p: prune") {
		t.Error("worktrees mode read-only should NOT show 'p: prune'")
	}
}

func TestStatusBar_WorktreesModeReadOnlyShowsDestructiveHintBeforeActions(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, false, false, false)
	dIdx := strings.Index(bar, "D: destructive mode")
	nIdx := strings.Index(bar, "n: new worktree")
	if dIdx == -1 || nIdx == -1 {
		t.Fatalf("expected destructive and new-worktree hints in %q", bar)
	}
	if dIdx > nIdx {
		t.Fatalf("expected destructive hint before worktree actions, got %q", bar)
	}
}

func TestStatusBar_WorktreesModeRightPaneShowsActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 1, true, false, false) // right pane active
	for _, hint := range []string{"f: fetch", "F: pull", "t: terminal", "c: code"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("worktrees mode right pane should show %q", hint)
		}
	}
}

func TestStatusBar_WorktreesModeLeftPaneHidesActionHints(t *testing.T) {
	bar := RenderStatusBar(120, 1, 0, 0, true, false, true) // left pane active, destructive
	for _, hint := range []string{"enter: diff", "f: fetch", "F: pull", "t: terminal", "c: code", "d: delete", "p: prune"} {
		if strings.Contains(bar, hint) {
			t.Errorf("worktrees mode left pane should hide %q", hint)
		}
	}
}

func TestRender_WorktreesModeShowsData(t *testing.T) {
	view := Render(RenderParams{
		Repos:    []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected: 0,
		Width:    80,
		Height:   10,
		Mode:     1,
		Worktrees: []gitquery.Worktree{
			{Path: "/a", BranchName: "main", IsMain: true},
			{Path: "/a-feat", BranchName: "feat"},
		},
		WorktreeSelected: 0,
		ActivePane:       1,
	})
	if !strings.Contains(view, "main") {
		t.Error("render should contain worktree branch name 'main'")
	}
	if !strings.Contains(view, "feat") {
		t.Error("render should contain worktree branch name 'feat'")
	}
	if strings.Contains(view, "nothing here yet") {
		t.Error("render should not show placeholder when worktree data exists")
	}
}

func TestRender_UsesProvidedEmptyStateMessages(t *testing.T) {
	view := Render(RenderParams{
		Repos:             []scanner.Repo{{Path: "/a", DisplayName: "alpha"}},
		Selected:          0,
		Width:             80,
		Height:            10,
		Mode:              ModeWorktrees,
		RightEmptyMessage: "No worktrees to show",
	})
	if !strings.Contains(view, "No worktrees to show") {
		t.Fatalf("render should show provided right-pane empty message, got:\n%s", view)
	}
	if strings.Contains(view, "nothing here yet") {
		t.Fatal("render should not fall back to generic placeholder when an empty message is provided")
	}

	view = Render(RenderParams{
		Width:            80,
		Height:           10,
		Mode:             ModeWorktrees,
		RepoEmptyMessage: "No repo results for zzz",
	})
	if !strings.Contains(view, "No repo results for zzz") {
		t.Fatalf("render should show provided repo empty message, got:\n%s", view)
	}
}

func TestRender_EmptyStateMessagesDoNotPanicAtTinyHeights(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("height_%d", height), func(t *testing.T) {
			_ = Render(RenderParams{
				Width:             80,
				Height:            height,
				Mode:              ModeWorktrees,
				RepoEmptyMessage:  "No repo results for zzz",
				RightEmptyMessage: "No selected repo",
			})
		})
	}
}

func TestRender_EmptyStateMessagesFitPaneWidth(t *testing.T) {
	longMessage := "No repo results for " + strings.Repeat("z", 80)

	repoLines := renderRepoList(nil, 0, 0, 12, 3, longMessage)
	for i, line := range repoLines {
		if lipgloss.Width(line) > 12 {
			t.Fatalf("repo empty line %d width %d exceeds pane width 12: %q", i, lipgloss.Width(line), line)
		}
	}

	rightLines := renderPlaceholderPane(16, 3, longMessage)
	for i, line := range rightLines {
		if lipgloss.Width(line) > 16 {
			t.Fatalf("right empty line %d width %d exceeds pane width 16: %q", i, lipgloss.Width(line), line)
		}
	}
}

func TestRepoList_EmptyMessageIsVerticallyCentered(t *testing.T) {
	lines := renderRepoList(nil, 0, 0, 20, 5, "No repos")
	for i, line := range lines {
		hasMessage := strings.Contains(line, "No repos")
		if i == 2 && !hasMessage {
			t.Fatalf("expected empty message centered on line 2, got %#v", lines)
		}
		if i != 2 && hasMessage {
			t.Fatalf("empty message should only appear on centered line, got line %d in %#v", i, lines)
		}
	}
}

// --- Reflog pane ---

func TestReflogPane_ShowsEntryDetails(t *testing.T) {
	entries := []gitquery.ReflogEntry{
		{Hash: "abc1234", Selector: "HEAD@{0}", Date: "2 hours ago", Subject: "commit: Fix login bug"},
		{Hash: "def5678", Selector: "HEAD@{1}", Date: "3 days ago", Subject: "checkout: moving from main to feature"},
	}
	lines := renderReflogPane(entries, 0, 0, 80, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "abc1234") {
		t.Error("expected hash 'abc1234' in output")
	}
	if !strings.Contains(joined, "HEAD@{0}") {
		t.Error("expected selector 'HEAD@{0}' in output")
	}
	if !strings.Contains(joined, "2 hours ago") {
		t.Error("expected date '2 hours ago' in output")
	}
	if !strings.Contains(joined, "commit: Fix login bug") {
		t.Error("expected subject 'commit: Fix login bug' in output")
	}
}

func TestReflogPane_SelectedHighlighted(t *testing.T) {
	entries := []gitquery.ReflogEntry{
		{Hash: "abc1234", Selector: "HEAD@{0}", Date: "2 hours ago", Subject: "commit: Fix login bug"},
		{Hash: "def5678", Selector: "HEAD@{1}", Date: "3 days ago", Subject: "checkout: main to feature"},
	}
	lines := renderReflogPane(entries, 1, 0, 80, 10)
	if !strings.Contains(lines[1], " > ") {
		t.Error("expected selected row to contain ' > ' prefix")
	}
}

func TestReflogDiffOverlay_EmptyDiffShowsMessage(t *testing.T) {
	view := Render(RenderParams{
		Width:   80,
		Height:  24,
		Mode:    5,
		Overlay: OverlayReflogDiff,
		// OverlayDiff is empty
	})
	if !strings.Contains(view, "No changes at this reflog entry") {
		t.Error("expected 'No changes at this reflog entry' in empty reflog diff overlay")
	}
}

func TestReflogDiffOverlay_NonEmptyDiffShowsContent(t *testing.T) {
	view := Render(RenderParams{
		Width:       80,
		Height:      24,
		Mode:        5,
		Overlay:     OverlayReflogDiff,
		OverlayDiff: "diff --git a/f.txt\n+added line",
	})
	if !strings.Contains(view, "diff --git") {
		t.Error("expected diff content in reflog diff overlay")
	}
	if strings.Contains(view, "No changes") {
		t.Error("should not show 'No changes' when diff has content")
	}
}

func TestStatusBar_ReflogModeHints(t *testing.T) {
	bar := RenderStatusBar(120, 5, 0, 1, false, false, false)
	for _, hint := range []string{"enter: diff", "y: copy hash", "tab: pane", "q/esc: quit"} {
		if !strings.Contains(bar, hint) {
			t.Errorf("reflog status bar should contain %q", hint)
		}
	}
	for _, forbidden := range []string{"d: delete", "d: drop", "D: destructive mode", "t: terminal", "c: code"} {
		if strings.Contains(bar, forbidden) {
			t.Errorf("reflog status bar should not contain %q", forbidden)
		}
	}
}
