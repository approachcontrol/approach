package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
)

// OverlayState represents what overlay (if any) is displayed.
type OverlayState int

const (
	OverlayNone OverlayState = iota
	OverlayStashDiff
	OverlayBranchDiff
	OverlayConfirm
	OverlayCommitDiff
	OverlayWorktreeDiff
	OverlayReflogDiff
	OverlaySessionTranscript
	OverlayWorktreeInput
)

const BranchPrompt = "New branch"
const WorktreeMovePrompt = "Move worktree to"
const PRWorktreePrompt = "PR worktree"
const WorktreeInputPlaceholder = "branch, tag, or new branch name"
const WorktreeMoveInputPlaceholder = "new path or sibling name"
const BranchInputPlaceholder = "branch name"
const PRWorktreeInputPlaceholder = "PR number or URL"
const AgentInputPlaceholder = "codex or claude"

// Mode represents the active right-pane view. The model owns the application
// state, but the renderer needs the same typed value (and the model imports ui,
// not the other way around), so the type lives here to avoid an import cycle.
type Mode int

const (
	ModeWorktrees Mode = iota + 1
	ModeBranches
	ModeStashes
	ModeHistory
	ModeReflog
	ModeSessions
)

const LeftPaneWidth = 30

// ShortcutPaneWidth is the total width reserved for the right-hand keyboard
// shortcut rail, including its left and right borders.
const ShortcutPaneWidth = 28

// MinContentPaneWidth keeps the primary item pane useful before the shortcut
// rail is shown. Narrow terminals continue using footer hints instead.
const MinContentPaneWidth = 48

// RepoContentOverhead is the number of rows consumed by chrome around the
// repo list: status bar (1) + top/bottom borders (2).
const RepoContentOverhead = 3

// BranchContentOverhead is the number of rows consumed by chrome around the
// branch list: status bar (1) + top/bottom borders (2) + mode header with
// separator (2). Both the model (ensureBranchVisible) and the renderer use
// this constant so they stay in sync.
const BranchContentOverhead = 5

// WorktreeContentOverhead is the number of rows consumed by chrome around the
// worktree list. Currently identical to BranchContentOverhead (both share the
// right-pane chrome: status bar + borders + mode header).
const WorktreeContentOverhead = BranchContentOverhead

// StashContentOverhead is the number of rows consumed by chrome around the
// stash list. Currently identical to BranchContentOverhead (both share the
// right-pane chrome: status bar + borders + mode header).
const StashContentOverhead = BranchContentOverhead

// StashPrefixWidth is the visible width consumed by the stash line prefix:
// indent/cursor (3) + date (10) + separator (2).
const StashPrefixWidth = 15

// ANSI palette codes used below (8-/16-color + 256-color grays):
//
//	5   = magenta        6   = cyan          9   = bright red
//	10  = bright green   11  = bright yellow  12  = bright blue
//	14  = bright cyan    15  = bright white   238 = dark gray
//	241 = medium gray
var (
	repoStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                          // 10 = bright green
	selectedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Reverse(true) // 10 = bright green
	placeholderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)            // 241 = medium gray
	statusStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	branchStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)               // 15 = bright white
	cleanStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                          // 10 = bright green
	commitStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	activeModeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)               // 15 = bright white
	inactiveModeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	shortcutTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)               // 15 = bright white
	shortcutGroupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)               // 14 = bright cyan
	shortcutKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)               // 12 = bright blue
	shortcutTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))                          // 15 = bright white
	stashDateStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	stashMsgStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))                          // 15 = bright white
	stashSelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true).Reverse(true) // 15 = bright white
	branchSelStyle     = lipgloss.NewStyle().Bold(true).Reverse(true)
	rootStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // 12 = bright blue
	lockedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // 14 = bright cyan
	noUpstreamStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))  // 5 = magenta
	aheadBehindStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // 11 = bright yellow
	mergedStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // 6 = cyan
	dirtyRedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // 9 = bright red
	diffAddStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // 10 = bright green
	diffDelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // 9 = bright red
	diffHdrStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // 14 = bright cyan
)

// RenderParams holds everything the renderer needs.
type RenderParams struct {
	Repos                    []scanner.Repo
	Selected                 int
	Width                    int
	Height                   int
	Mode                     Mode
	Branches                 []gitquery.BranchRow
	Stashes                  []gitquery.Stash
	BranchSelected           int
	StashSelected            int
	Overlay                  OverlayState
	OverlayDiff              string
	OverlayScroll            int
	ConfirmPrompt            string
	ConfirmForce             bool
	WorktreeInputPrompt      string
	WorktreeInputPlaceholder string
	WorktreeInput            string
	WorktreeInputErr         string
	BranchScroll             int
	RepoScroll               int
	StashScroll              int
	ActivePane               int
	Destructive              bool
	Worktrees                []gitquery.Worktree
	WorktreeSelected         int
	WorktreeScroll           int
	Commits                  []gitquery.Commit
	CommitSelected           int
	CommitScroll             int
	Reflogs                  []gitquery.ReflogEntry
	ReflogSelected           int
	ReflogScroll             int
	Sessions                 []sessions.SessionRecord
	SessionSelected          int
	SessionScroll            int
	TransientError           string
	TransientErrorFadeStep   int
	SearchActive             bool
	RepoSearch               string
	ItemSearch               string
	RepoEmptyMessage         string
	RightEmptyMessage        string
	FetchAvailable           bool
	FetchVisibleAvailable    bool
	PullAvailable            bool
	WorktreeMoveAvailable    bool
	AgentAvailable           bool
	NewAgentAvailable        bool
}

// Render produces the full terminal view string.
func Render(p RenderParams) string {
	if p.Width == 0 {
		p.Width = 80
	}
	if p.Height == 0 {
		p.Height = 24
	}

	// Overlay takes over the entire screen
	if p.Overlay != OverlayNone {
		return renderOverlay(p)
	}

	var repoPath string
	if p.Selected >= 0 && p.Selected < len(p.Repos) {
		repoPath = p.Repos[p.Selected].Path
	}

	var worktreeSelected, staleSelected, dirtySelected, lockedSelected, worktreeDeletableSelected, worktreeOpenableSelected, worktreeMoveSelected bool
	if p.Mode == ModeWorktrees && p.WorktreeSelected >= 0 && p.WorktreeSelected < len(p.Worktrees) {
		worktreeSelected = true
		wt := p.Worktrees[p.WorktreeSelected]
		staleSelected = wt.Stale
		dirtySelected = wt.Dirty
		lockedSelected = wt.Locked
		worktreeDeletableSelected = !wt.IsMain && !wt.Stale && !wt.Locked
		worktreeOpenableSelected = !wt.Stale
		worktreeMoveSelected = p.WorktreeMoveAvailable
	}
	var branchDirtySelected, branchDeletableSelected, branchOpenableSelected bool
	if p.Mode == ModeBranches && p.BranchSelected >= 0 && p.BranchSelected < len(p.Branches) {
		row := p.Branches[p.BranchSelected]
		branchDirtySelected = row.Branch.Dirty && row.Branch.IsWorktree
		branchDeletableSelected = row.WorktreePath != repoPath
		branchOpenableSelected = row.WorktreePath != ""
	}
	stashSelected := p.Mode == ModeStashes && p.StashSelected >= 0 && p.StashSelected < len(p.Stashes)
	commitSelected := p.Mode == ModeHistory && p.CommitSelected >= 0 && p.CommitSelected < len(p.Commits)
	reflogSelected := p.Mode == ModeReflog && p.ReflogSelected >= 0 && p.ReflogSelected < len(p.Reflogs)
	sessionSelected := p.Mode == ModeSessions && p.SessionSelected >= 0 && p.SessionSelected < len(p.Sessions)
	status := statusBarParams{
		Width:                     p.Width,
		Mode:                      p.Mode,
		Overlay:                   p.Overlay,
		ActivePane:                p.ActivePane,
		Destructive:               p.Destructive,
		RepoSelected:              repoPath != "",
		WorktreeSelected:          worktreeSelected,
		StaleSelected:             staleSelected,
		DirtySelected:             dirtySelected,
		LockedSelected:            lockedSelected,
		WorktreeDeletableSelected: worktreeDeletableSelected,
		WorktreeOpenableSelected:  worktreeOpenableSelected,
		WorktreeMoveSelected:      worktreeMoveSelected,
		BranchDirtySelected:       branchDirtySelected,
		BranchDeletableSelected:   branchDeletableSelected,
		BranchOpenableSelected:    branchOpenableSelected,
		StashSelected:             stashSelected,
		CommitSelected:            commitSelected,
		ReflogSelected:            reflogSelected,
		SessionSelected:           sessionSelected,
		TransientError:            p.TransientError,
		TransientErrorFadeStep:    p.TransientErrorFadeStep,
		SearchActive:              p.SearchActive,
		RepoSearch:                p.RepoSearch,
		ItemSearch:                p.ItemSearch,
		FetchAvailable:            p.FetchAvailable,
		FetchVisibleAvailable:     p.FetchVisibleAvailable,
		PullAvailable:             p.PullAvailable,
		AgentAvailable:            p.AgentAvailable,
		NewAgent:                  p.NewAgentAvailable,
	}
	showShortcutPane := !hasActiveStatusQuery(status) && shouldRenderShortcutPane(p.Width, p.Height, status)
	statusBar := renderFooterStatusBar(status, !showShortcutPane)

	// Border colors based on active pane
	activeBorderColor := lipgloss.Color("12")
	inactiveBorderColor := lipgloss.Color("238")
	destructiveBorderColor := lipgloss.Color("9")

	leftBorderColor := inactiveBorderColor
	rightBorderColor := inactiveBorderColor
	if p.Destructive {
		rightBorderColor = destructiveBorderColor
	} else if p.ActivePane == 1 {
		rightBorderColor = activeBorderColor
	}
	if p.ActivePane == 0 {
		leftBorderColor = activeBorderColor
	}

	leftContentWidth := LeftPaneWidth - 2 // left + right border
	innerHeight := p.Height - 3           // status bar + top/bottom borders

	leftLines := renderRepoList(p.Repos, p.Selected, p.RepoScroll, leftContentWidth, innerHeight, p.RepoEmptyMessage)
	leftContent := strings.Join(leftLines, "\n")
	leftPane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(leftBorderColor).
		Width(leftContentWidth).
		Height(innerHeight).
		Render(leftContent)

	rightContentWidth := p.Width - LeftPaneWidth - 2 // left + right border
	if showShortcutPane {
		rightContentWidth = p.Width - LeftPaneWidth - ShortcutPaneWidth - 2
	}
	if rightContentWidth < 0 {
		rightContentWidth = 0
	}

	modeHeader := renderModeHeader(p.Mode, rightContentWidth)
	rightContentHeight := p.Height - BranchContentOverhead

	// Hide cursor in right pane when left pane is active
	branchSel := p.BranchSelected
	stashSel := p.StashSelected
	commitSel := p.CommitSelected
	worktreeSel := p.WorktreeSelected
	reflogSel := p.ReflogSelected
	sessionSel := p.SessionSelected
	if p.ActivePane == 0 {
		branchSel = -1
		stashSel = -1
		commitSel = -1
		worktreeSel = -1
		reflogSel = -1
		sessionSel = -1
	}

	var rightLines []string
	switch {
	case p.Mode == ModeWorktrees && len(p.Worktrees) > 0:
		rightLines = renderWorktreePane(p.Worktrees, worktreeSel, p.WorktreeScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeBranches && len(p.Branches) > 0:
		rightLines = renderBranchPaneSelected(p.Branches, branchSel, p.BranchScroll, rightContentWidth, rightContentHeight, repoPath)
	case p.Mode == ModeStashes && len(p.Stashes) > 0:
		rightLines = renderStashPane(p.Stashes, stashSel, p.StashScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeHistory && len(p.Commits) > 0:
		rightLines = renderCommitPane(p.Commits, commitSel, p.CommitScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeReflog && len(p.Reflogs) > 0:
		rightLines = renderReflogPane(p.Reflogs, reflogSel, p.ReflogScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeSessions && len(p.Sessions) > 0:
		rightLines = renderSessionPane(p.Sessions, sessionSel, p.SessionScroll, rightContentWidth, rightContentHeight)
	default:
		rightLines = renderPlaceholderPane(rightContentWidth, rightContentHeight, p.RightEmptyMessage)
	}

	rightContent := modeHeader + "\n" + strings.Join(rightLines, "\n")
	rightPane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(rightBorderColor).
		Width(rightContentWidth).
		Height(innerHeight).
		Render(rightContent)

	panes := []string{leftPane, rightPane}
	if showShortcutPane {
		shortcutContentWidth := ShortcutPaneWidth - 2
		shortcutPane := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(inactiveBorderColor).
			Width(shortcutContentWidth).
			Height(innerHeight).
			Render(renderShortcutPane(status, shortcutContentWidth, innerHeight))
		panes = append(panes, shortcutPane)
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top, panes...)

	return content + "\n" + statusBar
}

// renderModeHeader produces the mode selector line shown at the top of the right pane.
func renderModeHeader(mode Mode, width int) string {
	modes := []struct {
		key  Mode
		name string
	}{
		{ModeWorktrees, "worktrees"},
		{ModeBranches, "branches"},
		{ModeStashes, "stashes"},
		{ModeHistory, "history"},
		{ModeReflog, "reflog"},
		{ModeSessions, "sessions"},
	}

	var parts []string
	for _, m := range modes {
		if mode == m.key {
			parts = append(parts, activeModeStyle.Render(fmt.Sprintf("[%d] %s", m.key, m.name)))
		} else {
			parts = append(parts, inactiveModeStyle.Render(fmt.Sprintf(" %d %s", m.key, m.name)))
		}
	}
	line := " " + strings.Join(parts, " ")
	separator := strings.Repeat("─", width)
	return line + "\n" + separator
}

// RenderStatusBar produces the bottom status bar (hints only, no mode tabs).
func RenderStatusBar(width int, mode Mode, overlay OverlayState, activePane int, destructive, staleSelected, dirtySelected bool) string {
	fetchAvailable := activePane == 1 && (mode == ModeWorktrees || mode == ModeBranches)
	pullAvailable := activePane == 1 && mode == ModeWorktrees
	newAgentAvailable := false
	if mode == ModeWorktrees && staleSelected {
		fetchAvailable = false
		pullAvailable = false
	}
	return renderStatusBarWithState(statusBarParams{
		Width:                     width,
		Mode:                      mode,
		Overlay:                   overlay,
		ActivePane:                activePane,
		Destructive:               destructive,
		RepoSelected:              true,
		WorktreeSelected:          mode == ModeWorktrees,
		StaleSelected:             staleSelected,
		DirtySelected:             dirtySelected,
		WorktreeDeletableSelected: activePane == 1 && mode == ModeWorktrees && !staleSelected,
		WorktreeOpenableSelected:  activePane == 1 && mode == ModeWorktrees && !staleSelected,
		BranchDeletableSelected:   activePane == 1 && mode == ModeBranches,
		BranchOpenableSelected:    activePane == 1 && mode == ModeBranches,
		StashSelected:             activePane == 1 && mode == ModeStashes,
		CommitSelected:            activePane == 1 && mode == ModeHistory,
		ReflogSelected:            activePane == 1 && mode == ModeReflog,
		FetchAvailable:            fetchAvailable,
		PullAvailable:             pullAvailable,
		NewAgent:                  newAgentAvailable,
	})
}

// statusBarParams groups the many fields the status-bar renderer needs,
// avoiding a long and error-prone positional parameter list.
type statusBarParams struct {
	Width                     int
	Mode                      Mode
	Overlay                   OverlayState
	ActivePane                int
	Destructive               bool
	RepoSelected              bool
	WorktreeSelected          bool
	StaleSelected             bool
	DirtySelected             bool
	LockedSelected            bool
	WorktreeDeletableSelected bool
	WorktreeOpenableSelected  bool
	WorktreeMoveSelected      bool
	BranchDirtySelected       bool
	BranchDeletableSelected   bool
	BranchOpenableSelected    bool
	StashSelected             bool
	CommitSelected            bool
	ReflogSelected            bool
	SessionSelected           bool
	TransientError            string
	TransientErrorFadeStep    int
	SearchActive              bool
	RepoSearch                string
	ItemSearch                string
	FetchAvailable            bool
	FetchVisibleAvailable     bool
	PullAvailable             bool
	AgentAvailable            bool
	NewAgent                  bool
}

type shortcutHint struct {
	Key     string
	Label   string
	Warning bool
	Inline  bool
}

type shortcutSection struct {
	Title string
	Hints []shortcutHint
}

func shouldRenderShortcutPane(width, height int, sp statusBarParams) bool {
	if width < LeftPaneWidth+ShortcutPaneWidth+MinContentPaneWidth {
		return false
	}
	return shortcutPaneLineCount(shortcutSections(sp)) <= height-3
}

func renderFooterStatusBar(sp statusBarParams, includeHints bool) string {
	if includeHints {
		return renderStatusBarWithState(sp)
	}
	query := sp.ItemSearch
	if sp.ActivePane == 0 {
		query = sp.RepoSearch
	}
	if sp.TransientError != "" || sp.SearchActive || query != "" {
		return renderStatusBarWithState(sp)
	}
	return statusStyle.Width(sp.Width).Render("")
}

func hasActiveStatusQuery(sp statusBarParams) bool {
	return sp.SearchActive
}

func renderStatusBarWithState(sp statusBarParams) string {
	width := sp.Width
	overlay := sp.Overlay
	activePane := sp.ActivePane
	transientError := sp.TransientError
	searchActive := sp.SearchActive
	repoSearch := sp.RepoSearch
	itemSearch := sp.ItemSearch

	if transientError != "" {
		return statusStyle.Width(width).Render("  " + transientStatusStyle(sp.TransientErrorFadeStep).Render(transientError))
	}

	label := "items"
	query := itemSearch
	if activePane == 0 {
		label = "repos"
		query = repoSearch
	}
	if searchActive || query != "" {
		if searchActive {
			return statusStyle.Width(width).Render(fmt.Sprintf("  / %s: %s  enter: keep  esc: clear  backspace: edit", label, query))
		}
		return statusStyle.Width(width).Render(fmt.Sprintf("  filtered %s: %s  /: edit  esc: clear", label, query))
	}

	switch {
	case overlay == OverlayConfirm:
		return statusStyle.Width(width).Render("  y: confirm  n/esc: cancel")
	case overlay == OverlayWorktreeInput:
		return statusStyle.Width(width).Render("  enter: create/set  esc: cancel  backspace: delete")
	case overlay != OverlayNone:
		return statusStyle.Width(width).Render("  ↑/↓ scroll  esc: close")
	}

	return statusStyle.Width(width).Render(renderFooterShortcuts(sp, shortcutSections(sp)))
}

func renderShortcutPane(sp statusBarParams, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, 0, height)
	title := fmt.Sprintf("Shortcuts  %s", modeShortcutTitle(sp.Mode))
	lines = append(lines, truncateToWidth(" "+shortcutTitleStyle.Render(title), width))

	for _, section := range shortcutSections(sp) {
		if len(section.Hints) == 0 {
			continue
		}
		if len(lines) < height {
			lines = append(lines, truncateToWidth(" "+shortcutGroupStyle.Render(section.Title), width))
		}
		for _, hint := range section.Hints {
			if len(lines) >= height {
				break
			}
			keyStyle := shortcutKeyStyle
			if hint.Warning {
				keyStyle = dirtyRedStyle.Bold(true)
			}
			key := keyStyle.Render(hint.Key + shortcutSeparator(hint))
			label := shortcutTextStyle.Render(hint.Label)
			lines = append(lines, truncateToWidth(" "+key+" "+label, width))
		}
		if len(lines) >= height {
			break
		}
	}

	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	truncateLines(lines, width)
	return strings.Join(lines, "\n")
}

func shortcutPaneLineCount(sections []shortcutSection) int {
	lines := 1 // title
	for _, section := range sections {
		if len(section.Hints) == 0 {
			continue
		}
		lines += 1 + len(section.Hints)
	}
	return lines
}

func shortcutSections(sp statusBarParams) []shortcutSection {
	navigation := []shortcutHint{{Key: "↑/↓", Label: "select", Inline: true}}

	sections := []shortcutSection{
		{
			Title: "Global",
			Hints: []shortcutHint{
				{Key: "tab", Label: "pane"},
				{Key: "q/esc", Label: "quit"},
				{Key: "A", Label: "set agent"},
			},
		},
		{
			Title: "Navigate",
			Hints: navigation,
		},
	}

	var actions []shortcutHint
	if sp.ActivePane == 0 && sp.FetchVisibleAvailable {
		actions = append(actions, shortcutHint{Key: "f", Label: "fetch visible"})
	}
	switch sp.Mode {
	case ModeWorktrees:
		if sp.ActivePane == 1 && sp.RepoSelected && !sp.StaleSelected {
			actions = append(actions, shortcutHint{Key: "n", Label: "new worktree"})
			if sp.NewAgent {
				actions = append(actions, shortcutHint{Key: "N", Label: "new+agent"})
			}
			actions = append(actions, shortcutHint{Key: "P", Label: "PR"})
			if sp.DirtySelected {
				actions = append(actions, shortcutHint{Key: "enter", Label: "diff"})
			}
			if sp.WorktreeMoveSelected {
				actions = append(actions, shortcutHint{Key: "m", Label: "move"})
			}
			if sp.Destructive && sp.WorktreeDeletableSelected {
				actions = append(actions, shortcutHint{Key: "d", Label: "delete", Warning: true})
			}
			if sp.FetchAvailable {
				actions = append(actions, shortcutHint{Key: "f", Label: "fetch"})
			}
			if sp.PullAvailable {
				actions = append(actions, shortcutHint{Key: "F", Label: "pull"})
			}
			if sp.WorktreeOpenableSelected {
				actions = append(actions,
					shortcutHint{Key: "t", Label: "terminal"},
					shortcutHint{Key: "c", Label: "code"},
				)
				if sp.AgentAvailable {
					actions = append(actions, shortcutHint{Key: "a", Label: "agent"})
				}
			}
		}
		if sp.ActivePane == 1 && sp.RepoSelected && sp.StaleSelected && sp.NewAgent {
			actions = append(actions, shortcutHint{Key: "N", Label: "new+agent"})
		}
		if sp.ActivePane == 1 && sp.StaleSelected && sp.Destructive && sp.WorktreeSelected && !sp.LockedSelected {
			actions = append(actions, shortcutHint{Key: "p", Label: "prune", Warning: true})
		}
		if sp.ActivePane == 1 && sp.WorktreeSelected && sp.LockedSelected {
			actions = append(actions, shortcutHint{Key: "u", Label: "unlock"})
		}
	case ModeBranches:
		if sp.ActivePane == 1 {
			if sp.RepoSelected {
				actions = append(actions, shortcutHint{Key: "n", Label: "new branch"})
			}
			if sp.BranchDirtySelected {
				actions = append(actions, shortcutHint{Key: "enter", Label: "diff"})
			}
			if sp.BranchOpenableSelected {
				actions = append(actions,
					shortcutHint{Key: "t", Label: "terminal"},
					shortcutHint{Key: "c", Label: "code"},
				)
				if sp.AgentAvailable {
					actions = append(actions, shortcutHint{Key: "a", Label: "agent"})
				}
			}
			if sp.Destructive && sp.BranchDeletableSelected {
				actions = append(actions, shortcutHint{Key: "d", Label: "delete", Warning: true})
			}
			if sp.FetchAvailable {
				actions = append(actions, shortcutHint{Key: "f", Label: "fetch"})
			}
			if sp.PullAvailable {
				actions = append(actions, shortcutHint{Key: "F", Label: "pull"})
			}
		}
	case ModeStashes:
		if sp.ActivePane == 1 && sp.StashSelected {
			actions = append(actions, shortcutHint{Key: "enter", Label: "diff"})
			if sp.Destructive {
				actions = append(actions, shortcutHint{Key: "d", Label: "drop", Warning: true})
			}
		}
	case ModeHistory:
		if sp.ActivePane == 1 {
			if sp.CommitSelected {
				actions = append(actions,
					shortcutHint{Key: "enter", Label: "diff"},
					shortcutHint{Key: "y", Label: "copy hash"},
				)
			}
			if sp.RepoSelected {
				actions = append(actions,
					shortcutHint{Key: "t", Label: "terminal"},
					shortcutHint{Key: "c", Label: "code"},
				)
			}
		}
	case ModeReflog:
		if sp.ActivePane == 1 && sp.ReflogSelected {
			actions = append(actions,
				shortcutHint{Key: "enter", Label: "diff"},
				shortcutHint{Key: "y", Label: "copy hash"},
			)
		}
	case ModeSessions:
		if sp.ActivePane == 1 && sp.SessionSelected {
			actions = append(actions, shortcutHint{Key: "enter", Label: "transcript"})
		}
	}
	if sp.ActivePane == 1 && sp.Mode != ModeWorktrees && sp.Mode != ModeBranches {
		if sp.FetchAvailable {
			actions = append(actions, shortcutHint{Key: "f", Label: "fetch"})
		}
		if sp.PullAvailable {
			actions = append(actions, shortcutHint{Key: "F", Label: "pull"})
		}
	}
	if !sp.Destructive && (sp.Mode == ModeWorktrees || sp.Mode == ModeBranches || sp.Mode == ModeStashes) {
		actions = append([]shortcutHint{{Key: "D", Label: "destructive mode"}}, actions...)
	}
	if len(actions) > 0 {
		sections = append(sections, shortcutSection{Title: "Actions", Hints: actions})
	}
	if sp.Mode == ModeBranches {
		sections = append(sections, shortcutSection{
			Title: "Legend",
			Hints: []shortcutHint{
				{Key: "✔", Label: "clean"},
				{Key: "●", Label: "ahead/behind"},
				{Key: "●", Label: "dirty", Warning: true},
				{Key: "●", Label: "no upstream"},
				{Key: "merged", Label: "merged"},
			},
		})
	}
	return sections
}

func renderFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	if sp.Mode == ModeWorktrees {
		return renderWorktreeFooterShortcuts(sp, sections)
	}
	if sp.Mode == ModeBranches {
		legend, rest := splitLegendSection(sections)
		rest = withoutSection(rest, "Navigate")
		rest = branchFooterSectionOrder(rest)
		keys := renderFooterHintList(rest)
		if keys != "" {
			return renderFooterLegend(legend) + "  |  " + keys
		}
		return renderFooterLegend(legend)
	}
	return "  " + renderFooterHintList(sections)
}

func transientStatusStyle(fadeStep int) lipgloss.Style {
	switch fadeStep {
	case 1:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	case 2:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	default:
		return dirtyRedStyle
	}
}

func renderWorktreeFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	hints := flattenShortcutHints(sections)
	parts := []string{}
	for _, key := range []string{"tab", "q/esc"} {
		if hint, ok := findShortcutHint(hints, key); ok {
			parts = append(parts, renderFooterHint(hint))
		}
	}

	required := worktreeFooterParts(hints, false)
	requiredWithDestructive := worktreeFooterParts(hints, true)
	if hint, ok := findShortcutHint(hints, "A"); ok {
		candidate := append(append([]string{}, parts...), renderFooterHint(hint))
		// When agent actions are visible, keep A by making room from the D
		// toggle first; otherwise preserve D ahead of lower-priority A.
		if sp.AgentAvailable || sp.NewAgent {
			if footerPartsFit(sp.Width, candidate, append([]string{renderFooterHint(shortcutHint{Key: "↑/↓", Label: "select", Inline: true})}, required...)...) {
				parts = candidate
			}
		} else if footerPartsFit(sp.Width, candidate, append([]string{renderFooterHint(shortcutHint{Key: "↑/↓", Label: "select", Inline: true})}, requiredWithDestructive...)...) {
			parts = candidate
		}
	}
	if hint, ok := findShortcutHint(hints, "↑/↓"); ok {
		parts = append(parts, renderFooterHint(hint))
	}
	if hint, ok := findShortcutHint(hints, "D"); ok {
		candidate := append(append([]string{}, parts...), renderFooterHint(hint))
		if footerPartsFit(sp.Width, candidate, required...) {
			parts = candidate
		}
	}
	for _, key := range []string{"n", "N", "m", "d", "p", "u", "enter", "f", "F"} {
		if hint, ok := findShortcutHint(hints, key); ok {
			parts = append(parts, renderFooterHint(hint))
		}
	}

	open := ""
	if _, ok := findShortcutHint(hints, "t"); ok {
		if _, ok := findShortcutHint(hints, "c"); ok {
			open = "t: terminal c: code"
		}
	}
	if open != "" {
		parts = append(parts, open)
	}
	if hint, ok := findShortcutHint(hints, "P"); ok {
		parts = append(parts, renderFooterHint(hint))
	}
	if hint, ok := findShortcutHint(hints, "a"); ok {
		parts = append(parts, renderFooterHint(hint))
	}

	return "  " + strings.Join(parts, " ")
}

func worktreeFooterParts(hints []shortcutHint, includeDestructiveMode bool) []string {
	var parts []string
	keys := []string{"n", "N", "m", "d", "p", "u", "enter", "f", "F"}
	if includeDestructiveMode {
		keys = append([]string{"D"}, keys...)
	}
	for _, key := range keys {
		if hint, ok := findShortcutHint(hints, key); ok {
			parts = append(parts, renderFooterHint(hint))
		}
	}
	if _, ok := findShortcutHint(hints, "t"); ok {
		if _, ok := findShortcutHint(hints, "c"); ok {
			parts = append(parts, "t: terminal c: code")
		}
	}
	if hint, ok := findShortcutHint(hints, "P"); ok {
		parts = append(parts, renderFooterHint(hint))
	}
	if hint, ok := findShortcutHint(hints, "a"); ok {
		parts = append(parts, renderFooterHint(hint))
	}
	return parts
}

func footerPartsFit(width int, parts []string, extra ...string) bool {
	all := append(append([]string{}, parts...), extra...)
	return lipgloss.Width("  "+strings.Join(all, " ")) <= width
}

func flattenShortcutHints(sections []shortcutSection) []shortcutHint {
	var hints []shortcutHint
	for _, section := range sections {
		hints = append(hints, section.Hints...)
	}
	return hints
}

func findShortcutHint(hints []shortcutHint, key string) (shortcutHint, bool) {
	for _, hint := range hints {
		if hint.Key == key {
			return hint, true
		}
	}
	return shortcutHint{}, false
}

func branchFooterSectionOrder(sections []shortcutSection) []shortcutSection {
	ordered := make([]shortcutSection, 0, len(sections))
	for _, title := range []string{"Global", "Safety", "Actions"} {
		for _, section := range sections {
			if section.Title == title {
				ordered = append(ordered, section)
			}
		}
	}
	for _, section := range sections {
		if section.Title != "Global" && section.Title != "Safety" && section.Title != "Actions" {
			ordered = append(ordered, section)
		}
	}
	return ordered
}

func withoutSection(sections []shortcutSection, title string) []shortcutSection {
	filtered := make([]shortcutSection, 0, len(sections))
	for _, section := range sections {
		if section.Title == title {
			continue
		}
		filtered = append(filtered, section)
	}
	return filtered
}

func splitLegendSection(sections []shortcutSection) ([]shortcutHint, []shortcutSection) {
	rest := make([]shortcutSection, 0, len(sections))
	var legend []shortcutHint
	for _, section := range sections {
		if section.Title == "Legend" {
			legend = section.Hints
			continue
		}
		rest = append(rest, section)
	}
	return legend, rest
}

func renderFooterLegend(hints []shortcutHint) string {
	if len(hints) == 0 {
		return ""
	}
	parts := make([]string, 0, len(hints))
	for _, hint := range hints {
		parts = append(parts, renderFooterHint(hint))
	}
	return " " + strings.Join(parts, "  ")
}

func renderFooterHintList(sections []shortcutSection) string {
	var parts []string
	for _, section := range sections {
		for _, hint := range section.Hints {
			parts = append(parts, renderFooterHint(hint))
		}
	}
	return strings.Join(parts, "  ")
}

func renderFooterHint(hint shortcutHint) string {
	switch hint.Key {
	case "✔":
		return cleanStyle.Render("✔") + " " + hint.Label
	case "●":
		return styledDotForLabel(hint.Label) + " " + hint.Label
	case "merged":
		return mergedStyle.Render("merged")
	}

	text := hint.Key + shortcutSeparator(hint) + " " + hint.Label
	if hint.Warning {
		return dirtyRedStyle.Render(text)
	}
	return text
}

func styledDotForLabel(label string) string {
	switch label {
	case "ahead/behind":
		return aheadBehindStyle.Render("●")
	case "dirty":
		return dirtyRedStyle.Render("●")
	case "no upstream":
		return noUpstreamStyle.Render("●")
	default:
		return shortcutKeyStyle.Render("●")
	}
}

func shortcutSeparator(hint shortcutHint) string {
	if hint.Inline {
		return ""
	}
	return ":"
}

func modeShortcutTitle(mode Mode) string {
	switch mode {
	case ModeWorktrees:
		return "Worktrees"
	case ModeBranches:
		return "Branches"
	case ModeStashes:
		return "Stashes"
	case ModeHistory:
		return "History"
	case ModeReflog:
		return "Reflog"
	case ModeSessions:
		return "Sessions"
	default:
		return "Items"
	}
}

func renderRepoList(repos []scanner.Repo, selected, scroll, width, height int, emptyMessage string) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	if len(repos) == 0 && emptyMessage != "" {
		for i := range lines {
			lines[i] = strings.Repeat(" ", width)
		}
		lines[height/2] = renderPlaceholderLine(emptyMessage, width)
		return lines
	}

	for i := 0; i < height; i++ {
		idx := scroll + i
		if idx < len(repos) {
			name := repos[idx].DisplayName
			if idx == selected {
				line := truncateToWidth(fmt.Sprintf(" > %s", name), width)
				lines[i] = selectedStyle.Width(width).Render(line)
			} else {
				line := truncateToWidth(fmt.Sprintf("   %s", name), width)
				lines[i] = repoStyle.Width(width).Render(line)
			}
		} else {
			lines[i] = strings.Repeat(" ", width)
		}
	}

	return lines
}

func renderBranchPaneSelected(rows []gitquery.BranchRow, selected, scroll, width, height int, repoPath string) []string {
	var content []string

	for i, row := range rows {
		b := row.Branch
		branch := branchStyle.Render(b.Name)

		var indicators string
		if b.Ahead > 0 || b.Behind > 0 {
			indicators += aheadBehindStyle.Render(" ●")
			indicators += fmt.Sprintf(" +%d/-%d", b.Ahead, b.Behind)
		}
		if b.Dirty {
			indicators += renderDirtyIndicator(b.FilesChanged, b.LinesAdded, b.LinesDeleted)
		}
		if !b.HasUpstream || b.UpstreamGone {
			indicators += noUpstreamStyle.Render(" ●")
		}
		if b.Merged {
			indicators += mergedStyle.Render(" merged")
		}
		if indicators == "" {
			indicators = cleanStyle.Render(" ✔")
		}

		var locationLabel string
		if row.WorktreePath != "" {
			if repoPath != "" && row.WorktreePath == repoPath {
				locationLabel = " " + rootStyle.Render("[root]")
			} else {
				locationLabel = " " + commitStyle.Render(fmt.Sprintf("[%s]", row.WorktreePath))
			}
		}

		line := "   " + branch + indicators + locationLabel
		if i == selected {
			line = branchSelStyle.Render(" > " + strings.TrimPrefix(line, "   "))
		}
		content = append(content, line)

		// Unpushed commits (max 5) — skipped for expansion rows
		if !row.IsExpansion {
			maxShow := 5
			for j, msg := range b.Unpushed {
				if j >= maxShow {
					remaining := len(b.Unpushed) - maxShow
					content = append(content, "    "+commitStyle.Render(fmt.Sprintf("... and %d more", remaining)))
					break
				}
				content = append(content, "    "+commitStyle.Render(msg))
			}
		}
	}

	truncateLines(content, width)
	return scrollAndPad(content, scroll, height)
}

// StashLineCount returns the number of visual lines a stash entry occupies
// at the given pane width (1 or 2).
func StashLineCount(msg string, paneWidth int) int {
	if lipgloss.Width(msg) > paneWidth-StashPrefixWidth {
		return 2
	}
	return 1
}

// splitAtWidth splits s into two parts where the first fits within maxWidth
// visible columns.
func splitAtWidth(s string, maxWidth int) (string, string) {
	if maxWidth <= 0 {
		return "", s
	}
	if lipgloss.Width(s) <= maxWidth {
		return s, ""
	}
	runes := []rune(s)
	for i := 1; i <= len(runes); i++ {
		if lipgloss.Width(string(runes[:i])) > maxWidth {
			return string(runes[:i-1]), string(runes[i-1:])
		}
	}
	return s, ""
}

func renderStashPane(stashes []gitquery.Stash, selected, scroll, width, height int) []string {
	var content []string
	msgWidth := width - StashPrefixWidth
	if msgWidth < 1 {
		msgWidth = 1
	}
	contIndent := strings.Repeat(" ", StashPrefixWidth)

	for i, s := range stashes {
		date := s.Date
		if len(date) > 10 {
			date = date[:10]
		}

		msgFirst, msgRest := splitAtWidth(s.Message, msgWidth)

		if i == selected {
			line := truncateToWidth(fmt.Sprintf(" > %s  %s", date, msgFirst), width)
			content = append(content, stashSelStyle.Width(width).Render(line))
		} else {
			dateStr := stashDateStyle.Render(date)
			msgStr := stashMsgStyle.Render(msgFirst)
			content = append(content, truncateToWidth(fmt.Sprintf("   %s  %s", dateStr, msgStr), width))
		}

		if msgRest != "" {
			if i == selected {
				contLine := truncateToWidth(contIndent+msgRest, width)
				content = append(content, stashSelStyle.Width(width).Render(contLine))
			} else {
				contLine := truncateToWidth(contIndent+stashMsgStyle.Render(msgRest), width)
				content = append(content, contLine)
			}
		}
	}

	return scrollAndPad(content, scroll, height)
}

func renderCommitPane(commits []gitquery.Commit, selected, scroll, width, height int) []string {
	var content []string
	for i, c := range commits {
		hashStr := diffHdrStyle.Render(c.Hash)
		authorStr := branchStyle.Render(c.Author)
		dateStr := stashDateStyle.Render(c.Date)
		subjectStr := stashMsgStyle.Render(c.Subject)
		line := fmt.Sprintf("   %s  %s  %s  %s", hashStr, authorStr, dateStr, subjectStr)

		if i == selected {
			line = stashSelStyle.Width(width).Render(fmt.Sprintf(" > %s  %s  %s  %s", c.Hash, c.Author, c.Date, c.Subject))
		}

		content = append(content, truncateToWidth(line, width))
	}

	return scrollAndPad(content, scroll, height)
}

func renderReflogPane(entries []gitquery.ReflogEntry, selected, scroll, width, height int) []string {
	var content []string
	for i, e := range entries {
		hashStr := diffHdrStyle.Render(e.Hash)
		selectorStr := branchStyle.Render(e.Selector)
		dateStr := stashDateStyle.Render(e.Date)
		subjectStr := stashMsgStyle.Render(e.Subject)
		line := fmt.Sprintf("   %s  %s  %s  %s", hashStr, selectorStr, dateStr, subjectStr)

		if i == selected {
			line = stashSelStyle.Width(width).Render(fmt.Sprintf(" > %s  %s  %s  %s", e.Hash, e.Selector, e.Date, e.Subject))
		}

		content = append(content, truncateToWidth(line, width))
	}

	return scrollAndPad(content, scroll, height)
}

func renderSessionPane(records []sessions.SessionRecord, selected, scroll, width, height int) []string {
	var content []string
	for i, record := range records {
		provider := string(record.Provider)
		worktree := filepath.Base(record.WorktreePath)
		if worktree == "." || worktree == string(filepath.Separator) {
			worktree = ""
		}
		line := fmt.Sprintf("   %s  %s  %s  %s  %s",
			diffHdrStyle.Render(provider),
			branchStyle.Render(record.Branch),
			stashDateStyle.Render(worktree),
			statusStyle.Render(record.Status),
			stashMsgStyle.Render(record.Summary),
		)
		if i == selected {
			selectedLine := truncateToWidth(fmt.Sprintf(" > %s  %s  %s  %s  %s",
				provider,
				record.Branch,
				worktree,
				record.Status,
				record.Summary,
			), width)
			line = stashSelStyle.Width(width).Render(selectedLine)
		}
		content = append(content, truncateToWidth(line, width))
	}
	return scrollAndPad(content, scroll, height)
}

func renderWorktreePane(worktrees []gitquery.Worktree, selected, scroll, width, height int) []string {
	var content []string
	for i, wt := range worktrees {
		name := branchStyle.Render(wt.BranchName)
		if wt.Detached {
			name = branchStyle.Render("(detached)")
		}

		var indicators string
		if wt.Locked {
			indicators = renderLockedIndicator(wt.LockReason)
			if !wt.Stale {
				if wt.Dirty {
					indicators += renderDirtyIndicator(wt.FilesChanged, wt.LinesAdded, wt.LinesDeleted)
				} else {
					indicators += cleanStyle.Render(" ✔")
				}
			}
		} else if wt.Stale {
			indicators = dirtyRedStyle.Render(" ✗") + " " + dirtyRedStyle.Render("stale")
		} else if wt.Dirty {
			indicators = renderDirtyIndicator(wt.FilesChanged, wt.LinesAdded, wt.LinesDeleted)
		} else {
			indicators = cleanStyle.Render(" ✔")
		}

		var rootLabel string
		if wt.IsMain {
			rootLabel = " " + rootStyle.Render("[root]")
		}

		path := " " + commitStyle.Render(wt.Path)

		line := "   " + name + indicators + rootLabel + path
		if i == selected {
			line = branchSelStyle.Render(" > " + strings.TrimPrefix(line, "   "))
		}
		content = append(content, line)
	}

	truncateLines(content, width)
	return scrollAndPad(content, scroll, height)
}

func renderOverlay(p RenderParams) string {
	statusBar := renderStatusBarWithState(statusBarParams{
		Width:                  p.Width,
		Mode:                   p.Mode,
		Overlay:                p.Overlay,
		ActivePane:             p.ActivePane,
		Destructive:            p.Destructive,
		TransientError:         p.TransientError,
		TransientErrorFadeStep: p.TransientErrorFadeStep,
		SearchActive:           p.SearchActive,
		RepoSearch:             p.RepoSearch,
		ItemSearch:             p.ItemSearch,
		FetchAvailable:         p.FetchAvailable,
		PullAvailable:          p.PullAvailable,
		AgentAvailable:         p.AgentAvailable,
		NewAgent:               p.NewAgentAvailable,
	})
	contentHeight := p.Height - 1

	// Confirmation dialog overlay
	if p.Overlay == OverlayConfirm {
		lines := renderConfirmDialog(p.ConfirmPrompt, p.ConfirmForce, p.Width, contentHeight)
		return strings.Join(lines, "\n") + "\n" + statusBar
	}
	if p.Overlay == OverlayWorktreeInput {
		lines := renderWorktreeInputDialog(p.WorktreeInputPrompt, p.WorktreeInputPlaceholder, p.WorktreeInput, p.WorktreeInputErr, p.Width, contentHeight)
		return strings.Join(lines, "\n") + "\n" + statusBar
	}

	var diffLines []string
	if p.OverlayDiff != "" {
		diffLines = strings.Split(p.OverlayDiff, "\n")
	} else if p.Overlay == OverlayReflogDiff { // empty diff (e.g. checkout entry)
		lines := make([]string, contentHeight)
		msg := placeholderStyle.Render("No changes at this reflog entry")
		mid := contentHeight / 2
		pad := (p.Width - lipgloss.Width(msg)) / 2
		if pad < 0 {
			pad = 0
		}
		lines[mid] = strings.Repeat(" ", pad) + msg
		return strings.Join(lines, "\n") + "\n" + statusBar
	}

	// Apply scroll offset
	start := p.OverlayScroll
	if start > len(diffLines) {
		start = len(diffLines)
	}
	visible := diffLines[start:]

	lines := make([]string, contentHeight)
	for i := 0; i < contentHeight; i++ {
		if i >= len(visible) {
			break
		}
		line := visible[i]
		switch {
		case strings.HasPrefix(line, "+"):
			lines[i] = diffAddStyle.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = diffDelStyle.Render(line)
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff "):
			lines[i] = diffHdrStyle.Render(line)
		default:
			lines[i] = line
		}
		lines[i] = truncateToWidth(lines[i], p.Width)
	}

	return strings.Join(lines, "\n") + "\n" + statusBar
}

func renderConfirmDialog(prompt string, force bool, width, height int) []string {
	lines := make([]string, height)
	mid := height / 2
	if mid < len(lines) {
		pad := (width - lipgloss.Width(prompt)) / 2
		if pad < 0 {
			pad = 0
		}
		style := activeModeStyle
		if force {
			style = dirtyRedStyle.Bold(true)
		}
		lines[mid] = strings.Repeat(" ", pad) + style.Render(prompt)
	}
	return lines
}

func renderWorktreeInputDialog(promptText, placeholder, input, errText string, width, height int) []string {
	lines := make([]string, height)
	mid := height / 2
	if mid >= len(lines) {
		return lines
	}

	if promptText == "" {
		promptText = "Create worktree from"
	}
	label := strings.TrimSpace(promptText) + ": "
	if placeholder == "" {
		placeholder = WorktreeInputPlaceholder
	}
	if promptText == BranchPrompt {
		label = "Create branch: "
	} else if promptText == PRWorktreePrompt {
		label = "Create PR worktree from: "
	}
	value := input
	if value == "" {
		value = placeholderStyle.Render(placeholder)
	}
	line := label + value + activeModeStyle.Render("█")
	lines[mid] = centeredLine(line, width)

	if errText != "" && mid+1 < len(lines) {
		lines[mid+1] = centeredLine(dirtyRedStyle.Render(errText), width)
	}
	return lines
}

func centeredLine(s string, width int) string {
	pad := (width - lipgloss.Width(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + truncateToWidth(s, width)
}

// truncateToWidth trims a styled string to fit within maxWidth visible columns.
func truncateToWidth(s string, maxWidth int) string {
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	// Strip ANSI, truncate runes, re-measure. Crude but correct for our use.
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > maxWidth {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

// scrollAndPad applies a scroll offset to content and returns a zero-padded
// slice of exactly height lines.
func scrollAndPad(content []string, scroll, height int) []string {
	if scroll > len(content) {
		scroll = len(content)
	}
	visible := content[scroll:]
	lines := make([]string, height)
	copy(lines, visible)
	return lines
}

// truncateLines truncates every line in place to fit within maxWidth visible columns.
func truncateLines(lines []string, width int) {
	for i, line := range lines {
		lines[i] = truncateToWidth(line, width)
	}
}

// renderDirtyIndicator returns the styled dirty-file indicator string
// (red dot + file count + added/deleted).
func renderDirtyIndicator(filesChanged, linesAdded, linesDeleted int) string {
	s := dirtyRedStyle.Render(" ●")
	s += fmt.Sprintf(" %d files ", filesChanged)
	s += diffAddStyle.Render(fmt.Sprintf("+%d", linesAdded))
	s += "/" + diffDelStyle.Render(fmt.Sprintf("-%d", linesDeleted))
	return s
}

// MaxLockReasonWidth caps the visible width of a lock reason in the worktree
// pane so a long reason cannot push the path off the end of the line.
const MaxLockReasonWidth = 40

func renderLockedIndicator(reason string) string {
	s := lockedStyle.Render(" 🔒") + " " + lockedStyle.Render("locked")
	if reason != "" {
		s += " " + lockedStyle.Render(truncateReason(reason, MaxLockReasonWidth))
	}
	return s
}

func truncateReason(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return truncateToWidth(s, max-lipgloss.Width("…")) + "…"
}

func renderPlaceholderPane(width, height int, message string) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, height)
	if message == "" {
		// Keep a generic fallback for direct renderer callers; the model
		// supplies mode-specific messages during normal application rendering.
		message = "nothing here yet"
	}
	mid := height / 2
	lines[mid] = renderPlaceholderLine(message, width)
	return lines
}

func renderPlaceholderLine(message string, width int) string {
	if width <= 0 {
		return ""
	}
	message = truncateToWidth(message, width)
	placeholder := placeholderStyle.Render(message)
	pad := (width - lipgloss.Width(placeholder)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + placeholder
}
