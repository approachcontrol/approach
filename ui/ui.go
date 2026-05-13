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

var (
	repoStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	selectedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Reverse(true)
	placeholderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)
	statusStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	branchStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	cleanStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	commitStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	activeModeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	inactiveModeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	stashDateStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	stashMsgStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	stashSelStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true).Reverse(true)
	branchSelStyle    = lipgloss.NewStyle().Bold(true).Reverse(true)
	rootStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	lockedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	noUpstreamStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	aheadBehindStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	dirtyRedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffAddStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	diffDelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffHdrStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

// RenderParams holds everything the renderer needs.
type RenderParams struct {
	Repos            []scanner.Repo
	Selected         int
	Width            int
	Height           int
	Mode             int
	Branches         []gitquery.BranchRow
	Stashes          []gitquery.Stash
	BranchSelected   int
	StashSelected    int
	Overlay          OverlayState
	OverlayDiff      string
	OverlayScroll    int
	ConfirmPrompt    string
	ConfirmForce     bool
	BranchScroll     int
	RepoScroll       int
	StashScroll      int
	ActivePane       int
	Destructive      bool
	Worktrees        []gitquery.Worktree
	WorktreeSelected int
	WorktreeScroll   int
	Commits          []gitquery.Commit
	CommitSelected   int
	CommitScroll     int
	Reflogs          []gitquery.ReflogEntry
	ReflogSelected   int
	ReflogScroll     int
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
	if p.Mode == 1 && p.WorktreeSelected >= 0 && p.WorktreeSelected < len(p.Worktrees) {
		wt := p.Worktrees[p.WorktreeSelected]
		staleSelected = wt.Stale
		dirtySelected = wt.Dirty
		lockedSelected = wt.Locked
	}
	statusBar := renderStatusBarWithState(p.Width, p.Mode, p.Overlay, p.ActivePane, p.Destructive, staleSelected, dirtySelected, lockedSelected)

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

	leftLines := renderRepoList(p.Repos, p.Selected, p.RepoScroll, leftContentWidth, innerHeight)
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
	case p.Mode == 1 && len(p.Worktrees) > 0:
		rightLines = renderWorktreePane(p.Worktrees, worktreeSel, p.WorktreeScroll, rightContentWidth, rightContentHeight)
	case p.Mode == 2 && len(p.Branches) > 0:
		rightLines = renderBranchPaneSelected(p.Branches, branchSel, p.BranchScroll, rightContentWidth, rightContentHeight, repoPath)
	case p.Mode == 3 && len(p.Stashes) > 0:
		rightLines = renderStashPane(p.Stashes, stashSel, p.StashScroll, rightContentWidth, rightContentHeight)
	case p.Mode == 4 && len(p.Commits) > 0:
		rightLines = renderCommitPane(p.Commits, commitSel, p.CommitScroll, rightContentWidth, rightContentHeight)
	case p.Mode == 5 && len(p.Reflogs) > 0:
		rightLines = renderReflogPane(p.Reflogs, reflogSel, p.ReflogScroll, rightContentWidth, rightContentHeight)
	default:
		rightLines = renderPlaceholderPane(rightContentWidth, rightContentHeight)
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
func renderModeHeader(mode, width int) string {
	modes := []struct {
		key  int
		name string
	}{
		{1, "worktrees"},
		{2, "branches"},
		{3, "stashes"},
		{4, "history"},
		{5, "reflog"},
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
func RenderStatusBar(width, mode int, overlay OverlayState, activePane int, destructive, staleSelected, dirtySelected bool) string {
	return renderStatusBarWithState(width, mode, overlay, activePane, destructive, staleSelected, dirtySelected, false)
}

func renderStatusBarWithState(width, mode int, overlay OverlayState, activePane int, destructive, staleSelected, dirtySelected, lockedSelected bool) string {
	var hints string
	switch {
	case overlay == OverlayConfirm:
		hints = "  y: confirm  n/esc: cancel"
	case overlay != OverlayNone:
		hints = "  ↑/↓ scroll  esc: close"
	case mode == 5:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff  y: copy hash"
	case mode == 4:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff  y: copy hash  t: terminal  c: code"
	case mode == 3:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select  enter: diff"
		if destructive {
			hints += "  " + dirtyRedStyle.Render("d: drop")
		} else {
			hints += "  D: destructive mode"
		}
	case mode == 2:
		keys := "  |  tab: pane  q/esc: quit"
		if activePane == 1 {
			keys += "  t: terminal  c: code"
			if destructive {
				keys += "  " + dirtyRedStyle.Render("d: delete")
			}
		}
		if !destructive {
			keys += "  D: destructive mode"
		}
		hints = " " + cleanStyle.Render("✔") + " clean  " + aheadBehindStyle.Render("●") + " ahead/behind  " + dirtyRedStyle.Render("●") + " dirty  " + noUpstreamStyle.Render("●") + " no upstream" + keys
	case mode == 1:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select"
		if activePane == 1 && !staleSelected {
			if dirtySelected {
				hints += "  enter: diff"
			}
			hints += "  t: terminal  c: code"
			if destructive && !lockedSelected {
				hints += "  " + dirtyRedStyle.Render("d: delete")
			}
		}
		if activePane == 1 && staleSelected && destructive && !lockedSelected {
			hints += "  " + dirtyRedStyle.Render("p: prune")
		}
		if !destructive {
			hints += "  D: destructive mode"
		}
	default:
		hints = "  tab: pane  q/esc: quit  ↑/↓ select"
	}

	return statusStyle.Width(width).Render(hints)
}

func renderRepoList(repos []scanner.Repo, selected, scroll, width, height int) []string {
	lines := make([]string, height)

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

func renderBranchPane(rows []gitquery.BranchRow, width, height int) []string {
	return renderBranchPaneSelected(rows, 0, 0, width, height, "")
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
	statusBar := RenderStatusBar(p.Width, p.Mode, p.Overlay, p.ActivePane, p.Destructive, false, false)
	contentHeight := p.Height - 1

	// Confirmation dialog overlay
	if p.Overlay == OverlayConfirm {
		lines := renderConfirmDialog(p.ConfirmPrompt, p.ConfirmForce, p.Width, contentHeight)
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

func renderLockedIndicator(reason string) string {
	s := lockedStyle.Render(" 🔒") + " " + lockedStyle.Render("locked")
	if reason != "" {
		s += " " + lockedStyle.Render(reason)
	}
	return s
}

func renderPlaceholderPane(width, height int) []string {
	lines := make([]string, height)
	placeholder := placeholderStyle.Render("nothing here yet")
	mid := height / 2
	pad := (width - lipgloss.Width(placeholder)) / 2
	if pad < 0 {
		pad = 0
	}
	lines[mid] = strings.Repeat(" ", pad) + placeholder
	return lines
}
