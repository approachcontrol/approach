package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
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
	OverlayWorktreeInput
)

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
)

const LeftPaneWidth = 30

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
	repoStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                          // 10 = bright green
	selectedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Reverse(true) // 10 = bright green
	placeholderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)            // 241 = medium gray
	statusStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	branchStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)               // 15 = bright white
	cleanStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                          // 10 = bright green
	commitStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	activeModeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)               // 15 = bright white
	inactiveModeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	stashDateStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))                         // 241 = medium gray
	stashMsgStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))                          // 15 = bright white
	stashSelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true).Reverse(true) // 15 = bright white
	branchSelStyle    = lipgloss.NewStyle().Bold(true).Reverse(true)
	rootStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // 12 = bright blue
	lockedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // 14 = bright cyan
	noUpstreamStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))  // 5 = magenta
	aheadBehindStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // 11 = bright yellow
	mergedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // 6 = cyan
	dirtyRedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // 9 = bright red
	diffAddStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // 10 = bright green
	diffDelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // 9 = bright red
	diffHdrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // 14 = bright cyan
)

// RenderParams holds everything the renderer needs.
type RenderParams struct {
	Repos             []scanner.Repo
	Selected          int
	Width             int
	Height            int
	Mode              Mode
	Branches          []gitquery.BranchRow
	Stashes           []gitquery.Stash
	BranchSelected    int
	StashSelected     int
	Overlay           OverlayState
	OverlayDiff       string
	OverlayScroll     int
	ConfirmPrompt     string
	ConfirmForce      bool
	WorktreeInput     string
	WorktreeInputErr  string
	BranchScroll      int
	RepoScroll        int
	StashScroll       int
	ActivePane        int
	Destructive       bool
	Worktrees         []gitquery.Worktree
	WorktreeSelected  int
	WorktreeScroll    int
	Commits           []gitquery.Commit
	CommitSelected    int
	CommitScroll      int
	Reflogs           []gitquery.ReflogEntry
	ReflogSelected    int
	ReflogScroll      int
	TransientError    string
	SearchActive      bool
	RepoSearch        string
	ItemSearch        string
	RepoEmptyMessage  string
	RightEmptyMessage string
	FetchAvailable    bool
	PullAvailable     bool
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

	var staleSelected, dirtySelected, lockedSelected bool
	if p.Mode == ModeWorktrees && p.WorktreeSelected >= 0 && p.WorktreeSelected < len(p.Worktrees) {
		wt := p.Worktrees[p.WorktreeSelected]
		staleSelected = wt.Stale
		dirtySelected = wt.Dirty
		lockedSelected = wt.Locked
	}
	statusBar := renderStatusBarWithState(statusBarParams{
		Width:          p.Width,
		Mode:           p.Mode,
		Overlay:        p.Overlay,
		ActivePane:     p.ActivePane,
		Destructive:    p.Destructive,
		StaleSelected:  staleSelected,
		DirtySelected:  dirtySelected,
		LockedSelected: lockedSelected,
		TransientError: p.TransientError,
		SearchActive:   p.SearchActive,
		RepoSearch:     p.RepoSearch,
		ItemSearch:     p.ItemSearch,
		FetchAvailable: p.FetchAvailable,
		PullAvailable:  p.PullAvailable,
	})

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
	if rightContentWidth < 0 {
		rightContentWidth = 0
	}

	modeHeader := renderModeHeader(p.Mode, rightContentWidth)
	rightContentHeight := p.Height - BranchContentOverhead

	var repoPath string
	if p.Selected < len(p.Repos) {
		repoPath = p.Repos[p.Selected].Path
	}

	// Hide cursor in right pane when left pane is active
	branchSel := p.BranchSelected
	stashSel := p.StashSelected
	commitSel := p.CommitSelected
	worktreeSel := p.WorktreeSelected
	reflogSel := p.ReflogSelected
	if p.ActivePane == 0 {
		branchSel = -1
		stashSel = -1
		commitSel = -1
		worktreeSel = -1
		reflogSel = -1
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

	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

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
	if mode == ModeWorktrees && staleSelected {
		fetchAvailable = false
		pullAvailable = false
	}
	return renderStatusBarWithState(statusBarParams{
		Width:          width,
		Mode:           mode,
		Overlay:        overlay,
		ActivePane:     activePane,
		Destructive:    destructive,
		StaleSelected:  staleSelected,
		DirtySelected:  dirtySelected,
		FetchAvailable: fetchAvailable,
		PullAvailable:  pullAvailable,
	})
}

// statusBarParams groups the many fields the status-bar renderer needs,
// avoiding a long and error-prone positional parameter list.
type statusBarParams struct {
	Width          int
	Mode           Mode
	Overlay        OverlayState
	ActivePane     int
	Destructive    bool
	StaleSelected  bool
	DirtySelected  bool
	LockedSelected bool
	TransientError string
	SearchActive   bool
	RepoSearch     string
	ItemSearch     string
	FetchAvailable bool
	PullAvailable  bool
}

func renderStatusBarWithState(sp statusBarParams) string {
	width := sp.Width
	mode := sp.Mode
	overlay := sp.Overlay
	activePane := sp.ActivePane
	destructive := sp.Destructive
	staleSelected := sp.StaleSelected
	dirtySelected := sp.DirtySelected
	lockedSelected := sp.LockedSelected
	transientError := sp.TransientError
	searchActive := sp.SearchActive
	repoSearch := sp.RepoSearch
	itemSearch := sp.ItemSearch
	fetchAvailable := sp.FetchAvailable
	pullAvailable := sp.PullAvailable

	if transientError != "" {
		return statusStyle.Width(width).Render("  " + dirtyRedStyle.Render(transientError))
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

	var hints string
	switch {
	case overlay == OverlayConfirm:
		hints = "  y: confirm  n/esc: cancel"
	case overlay == OverlayWorktreeInput:
		hints = "  enter: create  esc: cancel  backspace: delete"
	case overlay != OverlayNone:
		hints = "  ↑/↓ scroll  esc: close"
	case mode == ModeReflog:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff  y: copy hash"
		if fetchAvailable {
			hints += "  f: fetch"
		}
		if pullAvailable {
			hints += "  F: pull"
		}
	case mode == ModeHistory:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff  y: copy hash  t: terminal  c: code"
		if fetchAvailable {
			hints += "  f: fetch"
		}
		if pullAvailable {
			hints += "  F: pull"
		}
	case mode == ModeStashes:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff"
		if destructive {
			hints += "  " + dirtyRedStyle.Render("d: drop")
		} else {
			hints += "  D: destructive mode"
		}
		if fetchAvailable {
			hints += "  f: fetch"
		}
		if pullAvailable {
			hints += "  F: pull"
		}
	case mode == ModeBranches:
		keys := "  |  tab: pane  q/esc: quit"
		if !destructive {
			keys += "  D: destructive mode"
		}
		if activePane == 1 {
			keys += "  t: terminal  c: code"
			if destructive {
				keys += "  " + dirtyRedStyle.Render("d: delete")
			}
			if fetchAvailable {
				keys += "  f: fetch"
			}
			if pullAvailable {
				keys += "  F: pull"
			}
		}
		hints = " " + cleanStyle.Render("✔") + " clean  " + aheadBehindStyle.Render("●") + " ahead/behind  " + dirtyRedStyle.Render("●") + " dirty  " + noUpstreamStyle.Render("●") + " no upstream  " + mergedStyle.Render("merged") + keys
	case mode == ModeWorktrees:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select"
		if activePane == 1 && !staleSelected {
			hints += "  n: new worktree"
			if dirtySelected {
				hints += "  enter: diff"
			}
			if destructive && !lockedSelected {
				hints += "  " + dirtyRedStyle.Render("d: delete")
			}
			if fetchAvailable {
				hints += "  f: fetch"
			}
			if pullAvailable {
				hints += "  F: pull"
			}
			hints += "  t: terminal  c: code"
		}
		if activePane == 1 && staleSelected && destructive && !lockedSelected {
			hints += "  " + dirtyRedStyle.Render("p: prune")
		}
		if activePane == 1 && lockedSelected {
			hints += "  u: unlock"
		}
		if !destructive {
			hints += "  D: destructive mode"
		}
	default:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select"
	}

	return statusStyle.Width(width).Render(hints)
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
		Width:          p.Width,
		Mode:           p.Mode,
		Overlay:        p.Overlay,
		ActivePane:     p.ActivePane,
		Destructive:    p.Destructive,
		TransientError: p.TransientError,
		SearchActive:   p.SearchActive,
		RepoSearch:     p.RepoSearch,
		ItemSearch:     p.ItemSearch,
		FetchAvailable: p.FetchAvailable,
		PullAvailable:  p.PullAvailable,
	})
	contentHeight := p.Height - 1

	// Confirmation dialog overlay
	if p.Overlay == OverlayConfirm {
		lines := renderConfirmDialog(p.ConfirmPrompt, p.ConfirmForce, p.Width, contentHeight)
		return strings.Join(lines, "\n") + "\n" + statusBar
	}
	if p.Overlay == OverlayWorktreeInput {
		lines := renderWorktreeInputDialog(p.WorktreeInput, p.WorktreeInputErr, p.Width, contentHeight)
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

func renderWorktreeInputDialog(input, errText string, width, height int) []string {
	lines := make([]string, height)
	mid := height / 2
	if mid >= len(lines) {
		return lines
	}

	label := "Create worktree from: "
	value := input
	if value == "" {
		value = placeholderStyle.Render("branch, tag, or new branch name")
	}
	prompt := label + value + activeModeStyle.Render("█")
	lines[mid] = centeredLine(prompt, width)

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
