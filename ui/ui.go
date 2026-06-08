package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/planstore"
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
	OverlayPlanText
	OverlayWorktreeInput
	OverlayAgentSelect
)

const BranchPrompt = "New branch"
const FlowTitlePrompt = "New flow title"
const FlowInstructionsPrompt = "New flow instructions"
const FlowBaseRefPrompt = "New flow base ref"
const LaunchInstructionsPrompt = "Launch instructions"
const WorktreeMovePrompt = "Move worktree to"
const PRWorktreePrompt = "PR worktree"
const WorktreeInputPlaceholder = "branch, tag, or new branch name"
const FlowTitleInputPlaceholder = "flow title"
const FlowInstructionsInputPlaceholder = "task instructions"
const FlowBaseRefInputPlaceholder = "optional base ref"
const WorktreeMoveInputPlaceholder = "new path or sibling name"
const BranchInputPlaceholder = "branch name"
const PRWorktreeInputPlaceholder = "PR number or URL"
const AgentInputPlaceholder = "codex, codex-app, or claude"

type SelectItem struct {
	Label string
	Value string
}

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
	ModePlans
	ModeFlows
)

const LeftPaneWidth = 30

// ShortcutPaneWidth is the total width reserved for the right-hand keyboard
// shortcut rail, including its left and right borders.
const ShortcutPaneWidth = 28

const (
	shortcutKeyColumnWidth = 6
	shortcutOverflowMarker = "..."
)

const (
	launchInstructionsMaxWidth = 72
	launchInstructionsMinWidth = 32
	launchInstructionsMaxLines = 6
)

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

// TableHeaderRows is the number of rows consumed by table headers inside
// table-style right panes.
const TableHeaderRows = 1

// SessionContentOverhead is the number of rows consumed before session data
// rows can render: right-pane chrome plus the sessions table header.
const SessionContentOverhead = BranchContentOverhead + TableHeaderRows

// PlanContentOverhead is the number of rows consumed before plan data rows can
// render: right-pane chrome plus the plans table header.
const PlanContentOverhead = BranchContentOverhead + TableHeaderRows

// FlowContentOverhead is the number of rows consumed before flow data rows can
// render: right-pane chrome plus the flows table header.
const FlowContentOverhead = BranchContentOverhead + TableHeaderRows

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
	shortcutModeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)               // 12 = bright blue
	shortcutGroupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)               // 14 = bright cyan
	shortcutKeyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)               // 12 = bright blue
	shortcutTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))                         // 250 = light gray
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
	SelectPrompt             string
	SelectItems              []SelectItem
	SelectSelected           int
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
	Plans                    []planstore.PlanRecord
	PlanSelected             int
	PlanScroll               int
	Flows                    []flowstore.FlowRecord
	FlowSelected             int
	FlowScroll               int
	ExpandedPlanID           string
	ExpandedFlowID           string
	SelectedPlanPhaseID      string
	OverlayText              string
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
	planSelected := p.Mode == ModePlans && p.PlanSelected >= 0 && p.PlanSelected < len(p.Plans)
	flowSelected := p.Mode == ModeFlows && p.FlowSelected >= 0 && p.FlowSelected < len(p.Flows)
	selectedPlanPhaseID := scopedSelectedPlanPhaseID(p, planSelected)
	planPhaseSelected := selectedPlanPhaseID != ""
	status := statusBarParams{
		Width:                     p.Width,
		Mode:                      p.Mode,
		Overlay:                   p.Overlay,
		WorktreeInputPrompt:       p.WorktreeInputPrompt,
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
		PlanSelected:              planSelected,
		PlanPhaseSelected:         planPhaseSelected,
		FlowSelected:              flowSelected,
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
	innerHeight := p.Height - 3 // status bar + top/bottom borders
	showShortcutPane := !hasActiveStatusQuery(status) && shouldRenderShortcutPane(p.Width, innerHeight, status)
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
	planSel := p.PlanSelected
	flowSel := p.FlowSelected
	if p.ActivePane == 0 {
		branchSel = -1
		stashSel = -1
		commitSel = -1
		worktreeSel = -1
		reflogSel = -1
		sessionSel = -1
		planSel = -1
		flowSel = -1
		selectedPlanPhaseID = ""
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
	case p.Mode == ModePlans && len(p.Plans) > 0:
		rightLines = renderPlanPane(p.Plans, planSel, p.PlanScroll, rightContentWidth, rightContentHeight, p.ExpandedPlanID, selectedPlanPhaseID)
	case p.Mode == ModeFlows && len(p.Flows) > 0:
		rightLines = renderFlowPane(p.Flows, flowSel, p.FlowScroll, rightContentWidth, rightContentHeight, p.ExpandedFlowID)
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

func scopedSelectedPlanPhaseID(p RenderParams, planSelected bool) string {
	if !planSelected || p.SelectedPlanPhaseID == "" {
		return ""
	}
	plan := p.Plans[p.PlanSelected]
	if p.ExpandedPlanID != plan.PlanID {
		return ""
	}
	for _, phase := range plan.Phases {
		if phase.PhaseID == p.SelectedPlanPhaseID {
			return p.SelectedPlanPhaseID
		}
	}
	return ""
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
		{ModePlans, "plans"},
		{ModeFlows, "flows"},
	}

	var parts []string
	for _, m := range modes {
		if mode == m.key {
			parts = append(parts, activeModeStyle.Render(fmt.Sprintf("[%d] %s", m.key, m.name)))
		} else {
			parts = append(parts, inactiveModeStyle.Render(fmt.Sprintf(" %d %s", m.key, m.name)))
		}
	}
	line := ansi.Truncate(" "+strings.Join(parts, " "), width, "")
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
	WorktreeInputPrompt       string
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
	PlanSelected              bool
	PlanPhaseSelected         bool
	FlowSelected              bool
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
	return height >= 3
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
		if sp.WorktreeInputPrompt == LaunchInstructionsPrompt {
			return statusStyle.Width(width).Render("  enter: launch  esc: cancel  backspace: delete")
		}
		return statusStyle.Width(width).Render("  enter: create/set  esc: cancel  backspace: delete")
	case overlay == OverlayAgentSelect:
		return statusStyle.Width(width).Render("  up/down select  enter: confirm  esc: cancel")
	case overlay != OverlayNone:
		return statusStyle.Width(width).Render("  ↑/↓ scroll  esc: close")
	}

	return statusStyle.Width(width).Render(renderFooterShortcuts(sp, shortcutSections(sp)))
}

func renderShortcutPane(sp statusBarParams, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, 0)
	title := shortcutTitleStyle.Render("Shortcuts") + "  " + shortcutModeStyle.Render(modeShortcutTitle(sp.Mode))
	lines = append(lines, ansi.Truncate(" "+title, width, ""))
	compact := height <= 3
	tight := height <= 7
	if !compact && !tight {
		lines = append(lines, strings.Repeat(" ", width))
	}
	sectionCount := 0

	for _, section := range shortcutSections(sp) {
		hints := sidebarShortcutHints(section.Hints)
		if len(hints) == 0 {
			continue
		}
		if !compact {
			if sectionCount > 0 && !tight {
				lines = append(lines, strings.Repeat(" ", width))
			}
			lines = append(lines, truncateToWidth(" "+shortcutGroupStyle.Render(section.Title), width))
		}
		for _, hint := range hints {
			lines = append(lines, renderShortcutPaneHint(hint, width))
		}
		sectionCount++
	}

	if len(lines) > height {
		if height == 1 {
			lines = []string{truncateToWidth(" "+statusStyle.Render(shortcutOverflowMarker), width)}
		} else {
			lines = append(lines[:height-1], truncateToWidth(" "+statusStyle.Render(shortcutOverflowMarker), width))
		}
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	truncateLines(lines, width)
	return strings.Join(lines, "\n")
}

func renderShortcutPaneHint(hint shortcutHint, width int) string {
	if hint.Key == "merged" && hint.Label == "merged" {
		return ansi.Truncate(" "+shortcutTextStyle.Render(hint.Label), width, "")
	}
	keyStyle := shortcutKeyStyle
	if hint.Warning {
		keyStyle = dirtyRedStyle.Bold(true)
	}
	key := padShortcutKey(keyStyle.Render(hint.Key), shortcutKeyColumnWidth)
	label := shortcutTextStyle.Render(hint.Label)
	return ansi.Truncate(" "+key+" "+label, width, "")
}

func sidebarShortcutHints(hints []shortcutHint) []shortcutHint {
	grouped := make([]shortcutHint, 0, len(hints))
	for i := 0; i < len(hints); i++ {
		hint := hints[i]
		if i+1 < len(hints) {
			next := hints[i+1]
			switch {
			case hint.Key == "f" && next.Key == "F":
				grouped = append(grouped, shortcutHint{Key: "f/F", Label: hint.Label + " / " + next.Label, Warning: hint.Warning || next.Warning})
				i++
				continue
			case hint.Key == "t" && next.Key == "c":
				grouped = append(grouped, shortcutHint{Key: "t/c", Label: hint.Label + " / " + next.Label, Warning: hint.Warning || next.Warning})
				i++
				continue
			}
		}
		grouped = append(grouped, hint)
	}
	return grouped
}

func padShortcutKey(key string, width int) string {
	padding := width - lipgloss.Width(key)
	if padding <= 0 {
		return key
	}
	return key + strings.Repeat(" ", padding)
}

func shortcutSections(sp statusBarParams) []shortcutSection {
	navigation := []shortcutHint{{Key: "↑/↓", Label: "select", Inline: true}}
	global := []shortcutHint{
		{Key: "tab", Label: "pane"},
		{Key: "q/esc", Label: "quit"},
		{Key: "A", Label: "set agent"},
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
			actions = append(actions,
				shortcutHint{Key: "enter", Label: "transcript"},
				shortcutHint{Key: "r", Label: "resume"},
				shortcutHint{Key: "s", Label: "summary"},
				shortcutHint{Key: "y", Label: "copy id"},
			)
		}
	case ModePlans:
		if sp.ActivePane == 1 && sp.PlanSelected {
			implementLabel := "implement"
			if sp.PlanPhaseSelected {
				implementLabel = "implement phase"
			}
			actions = append(actions,
				shortcutHint{Key: "enter", Label: "phases"},
				shortcutHint{Key: "o", Label: "open"},
				shortcutHint{Key: "i", Label: implementLabel},
				shortcutHint{Key: "y", Label: "copy path"},
			)
		}
	case ModeFlows:
		if sp.ActivePane == 1 && sp.RepoSelected {
			actions = append(actions, shortcutHint{Key: "n", Label: "new flow"})
			if sp.FlowSelected {
				actions = append(actions, shortcutHint{Key: "x", Label: "phases"})
			}
			if sp.AgentAvailable {
				actions = append(actions, shortcutHint{Key: "a", Label: "launch phase"})
			}
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
	var sections []shortcutSection
	if len(actions) > 0 {
		sections = append(sections, shortcutSection{Title: "Actions", Hints: actions})
	}
	sections = append(sections,
		shortcutSection{Title: "Navigate", Hints: navigation},
		shortcutSection{Title: "Global", Hints: global},
	)
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
	return "  " + renderFooterHintList(footerSectionOrder(sections))
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

func footerSectionOrder(sections []shortcutSection) []shortcutSection {
	ordered := make([]shortcutSection, 0, len(sections))
	for _, title := range []string{"Global", "Navigate", "Actions", "Legend"} {
		for _, section := range sections {
			if section.Title == title {
				ordered = append(ordered, section)
			}
		}
	}
	for _, section := range sections {
		if section.Title != "Global" && section.Title != "Navigate" && section.Title != "Actions" && section.Title != "Legend" {
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
	case ModePlans:
		return "Plans"
	case ModeFlows:
		return "Flows"
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
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatSessionColumns("   ", "Provider", "Branch", "Worktree", "Status", "Summary")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}

	var rows []string
	for i, record := range records {
		provider := string(record.Provider)
		worktree := filepath.Base(record.WorktreePath)
		if worktree == "." || worktree == string(filepath.Separator) {
			worktree = ""
		}
		summary := sessionSummaryDisplayText(record.Summary)
		line := formatSessionColumns("   ",
			diffHdrStyle.Render(fitSessionColumn(provider, sessionProviderWidth)),
			branchStyle.Render(fitSessionColumn(record.Branch, sessionBranchWidth)),
			stashDateStyle.Render(fitSessionColumn(worktree, sessionWorktreeWidth)),
			statusStyle.Render(fitSessionColumn(record.Status, sessionStatusWidth)),
			stashMsgStyle.Render(summary),
		)
		if i == selected {
			selectedLine := truncateToWidth(formatSessionColumns(" > ",
				provider,
				record.Branch,
				worktree,
				record.Status,
				summary,
			), width)
			line = stashSelStyle.Width(width).Render(selectedLine)
		}
		rows = append(rows, truncateToWidth(line, width))
	}
	return append([]string{header}, scrollAndPad(rows, scroll, rowHeight)...)
}

func sessionSummaryDisplayText(summary string) string {
	return strings.Join(strings.Fields(summary), " ")
}

const (
	sessionProviderWidth = 8
	sessionBranchWidth   = 24
	sessionWorktreeWidth = 18
	sessionStatusWidth   = 10
)

func formatSessionColumns(prefix, provider, branch, worktree, status, summary string) string {
	return fmt.Sprintf("%s%s  %s  %s  %s  %s",
		prefix,
		fitSessionColumn(provider, sessionProviderWidth),
		fitSessionColumn(branch, sessionBranchWidth),
		fitSessionColumn(worktree, sessionWorktreeWidth),
		fitSessionColumn(status, sessionStatusWidth),
		summary,
	)
}

func fitSessionColumn(value string, width int) string {
	value = truncateToWidth(value, width)
	if lipgloss.Width(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-lipgloss.Width(value))
}

const (
	planStatusWidth  = 12
	planBranchWidth  = 20
	planPhaseWidth   = 7
	planUpdatedWidth = 10
)

func renderPlanPane(records []planstore.PlanRecord, selected, scroll, width, height int, expandedPlanID, selectedPhaseID string) []string {
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatPlanColumns("   ", "Status", "Branch", "Phase", "Updated", "Title")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}

	var rows []string
	for i, record := range records {
		phase := planPhaseProgress(record)
		updated := planUpdatedLabel(record)
		line := formatPlanColumns("   ",
			statusStyle.Render(fitSessionColumn(record.Status, planStatusWidth)),
			branchStyle.Render(fitSessionColumn(record.Branch, planBranchWidth)),
			diffHdrStyle.Render(fitSessionColumn(phase, planPhaseWidth)),
			stashDateStyle.Render(fitSessionColumn(updated, planUpdatedWidth)),
			stashMsgStyle.Render(record.Title),
		)
		if i == selected && selectedPhaseID == "" {
			selectedLine := truncateToWidth(formatPlanColumns(" > ",
				record.Status,
				record.Branch,
				phase,
				updated,
				record.Title,
			), width)
			line = stashSelStyle.Width(width).Render(selectedLine)
		}
		rows = append(rows, truncateToWidth(line, width))
		if record.PlanID == expandedPlanID {
			rows = append(rows, renderPlanPhaseRows(record, width, selectedPhaseID)...)
		}
	}
	return append([]string{header}, scrollAndPad(rows, scroll, rowHeight)...)
}

func renderPlanPhaseRows(record planstore.PlanRecord, width int, selectedPhaseID string) []string {
	if len(record.Phases) == 0 {
		return []string{truncateToWidth("      No phases", width)}
	}
	rows := make([]string, 0, len(record.Phases))
	for _, phase := range record.Phases {
		line := formatPlanColumns("      ",
			statusStyle.Render(fitSessionColumn(phase.Status, planStatusWidth)),
			"",
			"",
			"",
			stashMsgStyle.Render(phase.Title),
		)
		if phase.PhaseID == selectedPhaseID {
			selectedLine := truncateToWidth(formatPlanColumns(" > ",
				phase.Status,
				"",
				"",
				"",
				phase.Title,
			), width)
			line = stashSelStyle.Width(width).Render(selectedLine)
		}
		rows = append(rows, truncateToWidth(line, width))
	}
	return rows
}

func formatPlanColumns(prefix, status, branch, phase, updated, title string) string {
	return fmt.Sprintf("%s%s  %s  %s  %s  %s",
		prefix,
		fitSessionColumn(status, planStatusWidth),
		fitSessionColumn(branch, planBranchWidth),
		fitSessionColumn(phase, planPhaseWidth),
		fitSessionColumn(updated, planUpdatedWidth),
		title,
	)
}

// planPhaseProgress reports completed/total phases, e.g. "1/2", or "-" when no phases are recorded.
func planPhaseProgress(record planstore.PlanRecord) string {
	if len(record.Phases) == 0 {
		return "-"
	}
	completed := 0
	for _, phase := range record.Phases {
		if phase.Status == "completed" {
			completed++
		}
	}
	return fmt.Sprintf("%d/%d", completed, len(record.Phases))
}

func planUpdatedLabel(record planstore.PlanRecord) string {
	if record.UpdatedAt.IsZero() {
		return ""
	}
	return record.UpdatedAt.UTC().Format("2006-01-02")
}

const (
	flowStatusWidth  = 15
	flowBranchWidth  = 20
	flowPhaseWidth   = 34
	flowPlanWidth    = 12
	flowPRWidth      = 8
	flowUpdatedWidth = 10
)

func renderFlowPane(records []flowstore.FlowRecord, selected, scroll, width, height int, expandedFlowID string) []string {
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatFlowColumns("   ", "Status", "Branch", "Phase", "Plan", "PR", "Updated", "Title")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}

	var rows []string
	for i, record := range records {
		phase := flowPhaseProgress(record)
		plan := flowPlanLabel(record)
		pr := flowPRLabel(record)
		updated := flowUpdatedLabel(record)
		branch := record.Branch
		if branch == "" {
			if record.WorktreePath != "" {
				branch = filepath.Base(record.WorktreePath)
			} else if flowMissingWorktree(record) {
				branch = "missing-worktree"
			}
		}
		line := formatFlowColumns("   ",
			statusStyle.Render(fitSessionColumn(record.Status, flowStatusWidth)),
			branchStyle.Render(fitSessionColumn(branch, flowBranchWidth)),
			diffHdrStyle.Render(fitSessionColumn(phase, flowPhaseWidth)),
			statusStyle.Render(fitSessionColumn(plan, flowPlanWidth)),
			statusStyle.Render(fitSessionColumn(pr, flowPRWidth)),
			stashDateStyle.Render(fitSessionColumn(updated, flowUpdatedWidth)),
			stashMsgStyle.Render(record.Title),
		)
		if i == selected {
			selectedLine := truncateToWidth(formatFlowColumns(" > ",
				record.Status,
				branch,
				phase,
				plan,
				pr,
				updated,
				record.Title,
			), width)
			line = stashSelStyle.Width(width).Render(selectedLine)
		}
		rows = append(rows, truncateToWidth(line, width))
		if record.FlowID == expandedFlowID {
			rows = append(rows, renderFlowPhaseRows(record, width)...)
		}
	}
	return append([]string{header}, scrollAndPad(rows, scroll, rowHeight)...)
}

func renderFlowPhaseRows(record flowstore.FlowRecord, width int) []string {
	if len(record.Phases) == 0 {
		return []string{truncateToWidth("      No phases", width)}
	}
	rows := make([]string, 0, len(record.Phases))
	for _, phase := range flowstore.OrderedPhases(record.Phases) {
		state := flowPhaseState(record, phase)
		title := phase.Title
		if phase.ParentPhaseID != "" {
			title = "  " + title
		}
		line := formatFlowColumns("      ",
			statusStyle.Render(fitSessionColumn(phase.Status, flowStatusWidth)),
			"",
			diffHdrStyle.Render(fitSessionColumn(phase.PhaseID+":"+state, flowPhaseWidth)),
			"",
			"",
			"",
			stashMsgStyle.Render(title),
		)
		rows = append(rows, truncateToWidth(line, width))
	}
	return rows
}

func formatFlowColumns(prefix, status, branch, phase, plan, pr, updated, title string) string {
	return fmt.Sprintf("%s%s  %s  %s  %s  %s  %s  %s",
		prefix,
		fitSessionColumn(status, flowStatusWidth),
		fitSessionColumn(branch, flowBranchWidth),
		fitSessionColumn(phase, flowPhaseWidth),
		fitSessionColumn(plan, flowPlanWidth),
		fitSessionColumn(pr, flowPRWidth),
		fitSessionColumn(updated, flowUpdatedWidth),
		title,
	)
}

func flowPhaseProgress(record flowstore.FlowRecord) string {
	if len(record.Phases) == 0 {
		return "-"
	}
	completed := 0
	current := flowstore.FlowPhase{}
	phases := flowstore.OrderedPhases(record.Phases)
	for _, phase := range phases {
		if phase.Status == flowstore.PhaseCompleted || phase.Status == flowstore.PhaseSkipped {
			completed++
			continue
		}
		if current.PhaseID == "" {
			current = phase
		}
	}
	if current.PhaseID == "" {
		current = phases[len(phases)-1]
	}
	state := flowSummaryPhaseState(record, current)
	return fmt.Sprintf("%d/%d %s:%s", completed, len(phases), current.PhaseID, state)
}

func flowSummaryPhaseState(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	state := flowPhaseState(record, phase)
	if flowMissingWorktree(record) && state == flowBasePhaseState(phase) {
		return "recover-worktree"
	}
	return state
}

func flowPhaseState(record flowstore.FlowRecord, phase flowstore.FlowPhase) string {
	if flowPhaseSessionMismatch(phase) {
		return "session-mismatch"
	}
	if phase.Status == flowstore.PhaseRunning && flowPhaseAwaitingSession(phase) {
		return "await-session"
	}
	if phase.PhaseID == "autoreview" && flowMissingPRTarget(record) && phaseCanReportMissingPR(phase) {
		return "missing-pr"
	}
	return flowBasePhaseState(phase)
}

func phaseCanReportMissingPR(phase flowstore.FlowPhase) bool {
	return phase.Status == flowstore.PhasePending || phase.Status == flowstore.PhaseReady
}

func flowBasePhaseState(phase flowstore.FlowPhase) string {
	state := phase.Status
	if phase.Outcome != "" {
		state = phase.Outcome
	}
	return state
}

func flowMissingWorktree(record flowstore.FlowRecord) bool {
	return record.WorktreePath == "" && record.Branch == ""
}

func flowPhaseAwaitingSession(phase flowstore.FlowPhase) bool {
	latestLaunchID := ""
	for i := len(phase.LaunchIDs) - 1; i >= 0; i-- {
		if phase.LaunchIDs[i] != "" {
			latestLaunchID = phase.LaunchIDs[i]
			break
		}
	}
	if latestLaunchID == "" {
		return false
	}
	for _, session := range phase.Sessions {
		if session.LaunchID == latestLaunchID {
			return false
		}
	}
	return true
}

func flowPhaseSessionMismatch(phase flowstore.FlowPhase) bool {
	if len(phase.Sessions) == 0 {
		return false
	}
	launches := make(map[string]struct{}, len(phase.LaunchIDs))
	for _, launchID := range phase.LaunchIDs {
		if launchID != "" {
			launches[launchID] = struct{}{}
		}
	}
	for _, session := range phase.Sessions {
		if session.LaunchID == "" {
			return true
		}
		if _, ok := launches[session.LaunchID]; !ok {
			return true
		}
	}
	return false
}

func flowPlanLabel(record flowstore.FlowRecord) string {
	if record.PlanID != "" {
		return record.PlanID
	}
	return "-"
}

func flowPRLabel(record flowstore.FlowRecord) string {
	if record.PR.Number > 0 {
		return fmt.Sprintf("#%d", record.PR.Number)
	}
	if record.PR.URL != "" {
		return filepath.Base(record.PR.URL)
	}
	if flowMissingPRTarget(record) {
		return "missing"
	}
	return "-"
}

func flowMissingPRTarget(record flowstore.FlowRecord) bool {
	if flowstore.HasPRTarget(record.PR) {
		return false
	}
	for _, phase := range record.Phases {
		if phase.PhaseID == "pr-creation" && phase.Status == flowstore.PhaseCompleted {
			return true
		}
	}
	return false
}

func flowUpdatedLabel(record flowstore.FlowRecord) string {
	if record.UpdatedAt.IsZero() {
		return ""
	}
	return record.UpdatedAt.UTC().Format("2006-01-02")
}

// renderPlainTextOverlay renders scrollable plain text with no diff coloring.
func renderPlainTextOverlay(body string, scroll, width, height int) []string {
	lines := make([]string, height)
	if height <= 0 {
		return lines
	}
	var content []string
	if body != "" {
		content = strings.Split(body, "\n")
	}
	start := scroll
	if start > len(content) {
		start = len(content)
	}
	visible := content[start:]
	for i := 0; i < height; i++ {
		if i >= len(visible) {
			break
		}
		lines[i] = truncateToWidth(visible[i], width)
	}
	return lines
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
		WorktreeInputPrompt:    p.WorktreeInputPrompt,
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
	if p.Overlay == OverlayAgentSelect {
		lines := renderSelectDialog(p.SelectPrompt, p.SelectItems, p.SelectSelected, p.Width, contentHeight)
		return strings.Join(lines, "\n") + "\n" + statusBar
	}
	if p.Overlay == OverlayPlanText {
		lines := renderPlainTextOverlay(p.OverlayText, p.OverlayScroll, p.Width, contentHeight)
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

func renderSelectDialog(prompt string, items []SelectItem, selected int, width, height int) []string {
	lines := make([]string, height)
	if height <= 0 {
		return lines
	}
	if prompt == "" {
		prompt = "Choose"
	}
	if selected < 0 || selected >= len(items) {
		selected = 0
	}

	blockHeight := len(items) + 1
	start := (height - blockHeight) / 2
	if start < 0 {
		start = 0
	}
	if start < len(lines) {
		lines[start] = centeredLine(activeModeStyle.Render(prompt), width)
	}
	for i, item := range items {
		row := start + 1 + i
		if row >= len(lines) {
			break
		}
		label := item.Label
		if label == "" {
			label = item.Value
		}
		line := "  " + label
		if i == selected {
			line = selectedStyle.Render("> " + label)
		}
		lines[row] = centeredLine(line, width)
	}
	return lines
}

func renderWorktreeInputDialog(promptText, placeholder, input, errText string, width, height int) []string {
	if promptText == LaunchInstructionsPrompt {
		return renderLaunchInstructionsDialog(promptText, placeholder, input, errText, width, height)
	}

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

func renderLaunchInstructionsDialog(promptText, placeholder, input, errText string, width, height int) []string {
	lines := make([]string, height)
	if width <= 0 || height <= 0 {
		return lines
	}
	if placeholder == "" {
		placeholder = "launch instructions"
	}

	panelWidth := width - 4
	if panelWidth > launchInstructionsMaxWidth {
		panelWidth = launchInstructionsMaxWidth
	}
	if panelWidth < launchInstructionsMinWidth {
		panelWidth = width
	}
	if panelWidth < 4 {
		panelWidth = width
	}
	contentWidth := panelWidth - 4 // border plus one-space left/right padding
	if contentWidth < 1 {
		contentWidth = 1
	}

	value := input
	placeholderVisible := false
	if value == "" {
		value = placeholder
		placeholderVisible = true
	}
	wrapWidth := contentWidth - lipgloss.Width("█")
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	bodyLines := wrapPlainText(value, wrapWidth)
	if len(bodyLines) > launchInstructionsMaxLines {
		bodyLines = compactLaunchInstructionLines(bodyLines, launchInstructionsMaxLines)
	}

	content := []string{activeModeStyle.Render(promptText), ""}
	for i, line := range bodyLines {
		if placeholderVisible {
			line = placeholderStyle.Render(line)
		}
		if i == len(bodyLines)-1 {
			line += activeModeStyle.Render("█")
		}
		content = append(content, line)
	}
	if errText != "" {
		content = append(content, "")
		for _, line := range wrapPlainText(errText, contentWidth) {
			content = append(content, dirtyRedStyle.Render(line))
		}
	}

	for i, line := range content {
		content[i] = " " + fitSessionColumn(line, contentWidth) + " "
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("12")).
		Width(contentWidth + 2).
		Render(strings.Join(content, "\n"))
	panelLines := strings.Split(panel, "\n")
	top := (height - len(panelLines)) / 2
	if top < 0 {
		top = 0
	}
	for i, line := range panelLines {
		row := top + i
		if row >= len(lines) {
			break
		}
		lines[row] = centeredLine(line, width)
	}
	return lines
}

func compactLaunchInstructionLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	if maxLines == 1 {
		return []string{shortcutOverflowMarker}
	}
	if maxLines == 2 {
		return []string{lines[0], shortcutOverflowMarker}
	}
	headCount := (maxLines - 1) / 2
	tailCount := maxLines - headCount - 1
	compact := make([]string, 0, maxLines)
	compact = append(compact, lines[:headCount]...)
	compact = append(compact, shortcutOverflowMarker)
	compact = append(compact, lines[len(lines)-tailCount:]...)
	return compact
}

func wrapPlainText(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{""}
	}
	if s == "" {
		return []string{""}
	}

	var lines []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		current := ""
		for _, word := range words {
			for word != "" {
				if current == "" {
					if lipgloss.Width(word) <= maxWidth {
						current = word
						word = ""
						continue
					}
					head, rest := splitAtWidth(word, maxWidth)
					if head == "" {
						runes := []rune(word)
						head = string(runes[:1])
						rest = string(runes[1:])
					}
					lines = append(lines, head)
					word = rest
					continue
				}
				candidate := current + " " + word
				if lipgloss.Width(candidate) <= maxWidth {
					current = candidate
					word = ""
					continue
				}
				lines = append(lines, current)
				current = ""
			}
		}
		if current != "" {
			lines = append(lines, current)
		}
	}
	if len(lines) == 0 {
		return []string{""}
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
