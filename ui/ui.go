package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/internal/artifacts"
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
	OverlayInput
	OverlaySelect
	OverlayForm
	OverlayAgentSelect   = OverlaySelect
	OverlayWorktreeInput = OverlayInput
)

type InputMode int

const (
	InputSingleLine InputMode = iota
	InputMultiLine
)

type SelectPlacement int

const (
	SelectPlacementCenter SelectPlacement = iota
	SelectPlacementTopCenter
	SelectPlacementBottomCenter
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

type FormFieldKind int

const (
	FormText FormFieldKind = iota
	FormCheckbox
	FormChoice
)

type FormField struct {
	ID            string
	Kind          FormFieldKind
	Label         string
	Placeholder   string
	Value         string
	Cursor        int
	Checked       bool
	Options       []SelectItem
	SelectedIndex int
}

type FormView struct {
	Purpose    string
	Title      string
	Fields     []FormField
	FocusIndex int
	Error      string
}

type EmbeddedTerminalTab struct {
	Number   int
	Provider string
	Identity string
	State    string
	Active   bool
}

type FlowTerminalActivity struct {
	FlowID  string
	PhaseID string
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

const flowSplitMinPanelHeight = 4

const (
	// EmbeddedTerminalFrameColumns is the number of columns consumed by the
	// embedded terminal pane's left and right border.
	EmbeddedTerminalFrameColumns = 2
	// EmbeddedTerminalFrameRows is the number of rows consumed by the embedded
	// terminal pane's top and bottom border.
	EmbeddedTerminalFrameRows = 2
	// EmbeddedTerminalHeaderRows is the non-PTY tab/header row inside the
	// embedded terminal frame.
	EmbeddedTerminalHeaderRows = 1
	// EmbeddedTerminalSidePadding is the horizontal padding between each
	// embedded terminal border and its content.
	EmbeddedTerminalSidePadding = 1
)

// StashPrefixWidth is the visible width consumed by the stash line prefix:
// indent/cursor (3) + date (10) + separator (2).
const StashPrefixWidth = 15

// RenderParams holds everything the renderer needs.
type RenderParams struct {
	Repos                       []scanner.Repo
	Selected                    int
	Width                       int
	Height                      int
	Mode                        Mode
	Branches                    []gitquery.BranchRow
	Stashes                     []gitquery.Stash
	BranchSelected              int
	StashSelected               int
	Overlay                     OverlayState
	OverlayDiff                 string
	OverlayScroll               int
	ConfirmPrompt               string
	ConfirmForce                bool
	InputPrompt                 string
	InputPlaceholder            string
	InputValue                  string
	InputError                  string
	InputMode                   InputMode
	InputCursor                 int
	WorktreeInputPrompt         string
	WorktreeInputPlaceholder    string
	WorktreeInput               string
	WorktreeInputErr            string
	SelectPrompt                string
	SelectItems                 []SelectItem
	SelectSelected              int
	SelectWidth                 int
	SelectHeight                int
	SelectPlacement             SelectPlacement
	Form                        FormView
	BranchScroll                int
	RepoScroll                  int
	StashScroll                 int
	ActivePane                  int
	Destructive                 bool
	Worktrees                   []gitquery.Worktree
	WorktreeSelected            int
	WorktreeScroll              int
	WorktreeSessions            []sessions.SessionRecord
	WorktreeSessionSelected     int
	WorktreeSessionScroll       int
	InlineWorktreeSessions      bool
	Commits                     []gitquery.Commit
	CommitSelected              int
	CommitScroll                int
	Reflogs                     []gitquery.ReflogEntry
	ReflogSelected              int
	ReflogScroll                int
	Sessions                    []sessions.SessionRecord
	SessionSelected             int
	SessionScroll               int
	EmbeddedTerminals           []EmbeddedTerminalTab
	EmbeddedTerminalLines       []string
	EmbeddedTerminalPrefix      bool
	Plans                       []planstore.PlanRecord
	PlanSelected                int
	PlanScroll                  int
	Flows                       []flowstore.FlowRecord
	FlowSelected                int
	FlowScroll                  int
	FlowEmbeddedTerminals       []EmbeddedTerminalTab
	FlowEmbeddedTerminalLines   []string
	FlowEmbeddedTerminalPrefix  bool
	FlowTerminalActivity        []FlowTerminalActivity
	FlowTerminalFocused         bool
	ExpandedPlanID              string
	ExpandedFlowID              string
	SelectedPlanPhaseID         string
	SelectedFlowPhaseID         string
	FlowHeadless                bool
	FlowAutoModeSelected        bool
	FlowReasoningEffort         string
	FlowPhaseLaunchReady        bool
	FlowNextLaunchReady         bool
	FlowPhaseResetReadySelected bool
	FlowPhaseResumableSelected  bool
	OverlayText                 string
	TransientError              string
	TransientErrorFadeStep      int
	SearchActive                bool
	RepoSearch                  string
	ItemSearch                  string
	RepoEmptyMessage            string
	RightEmptyMessage           string
	FetchAvailable              bool
	FetchVisibleAvailable       bool
	RepoCreateAvailable         bool
	PullAvailable               bool
	WorktreeMoveAvailable       bool
	WorktreeSessionsOpen        bool
	AgentAvailable              bool
	NewAgentAvailable           bool
}

func FlowSplitPanelHeights(height int) (listHeight, terminalHeight int) {
	if height <= 0 {
		return 0, 0
	}
	if height < flowSplitMinPanelHeight*2 {
		listHeight = height / 2
		if listHeight < 1 {
			listHeight = 1
		}
		return listHeight, height - listHeight
	}
	listHeight = height / 4
	if listHeight < flowSplitMinPanelHeight {
		listHeight = flowSplitMinPanelHeight
	}
	terminalHeight = height - listHeight
	if terminalHeight < 1 {
		terminalHeight = 1
		listHeight = height - terminalHeight
	}
	return listHeight, terminalHeight
}

// RightContentWidth returns the render width inside the right pane after the
// left pane, right-pane border, and optional shortcut pane are accounted for.
func RightContentWidth(width, height int, activeStatusQuery bool) int {
	rightContentWidth := width - LeftPaneWidth - 2
	if shouldRenderShortcutPaneForViewport(width, height, activeStatusQuery) {
		rightContentWidth -= ShortcutPaneWidth
	}
	if rightContentWidth < 0 {
		return 0
	}
	return rightContentWidth
}

// EmbeddedTerminalRenderContentWidth returns the drawable content width after
// the embedded terminal frame and effective side padding are reserved.
func EmbeddedTerminalRenderContentWidth(outerWidth int) int {
	available := outerWidth - EmbeddedTerminalFrameColumns
	if available <= 0 {
		return 0
	}
	width := available - 2*embeddedTerminalEffectiveSidePadding(outerWidth)
	if width > 0 {
		return width
	}
	return 0
}

func embeddedTerminalEffectiveSidePadding(outerWidth int) int {
	available := outerWidth - EmbeddedTerminalFrameColumns
	if available >= 2*EmbeddedTerminalSidePadding+1 {
		return EmbeddedTerminalSidePadding
	}
	return 0
}

// EmbeddedTerminalRenderBodyHeight returns the number of live-output rows that
// can render inside an embedded terminal pane allocation.
func EmbeddedTerminalRenderBodyHeight(outerHeight int) int {
	height := outerHeight - EmbeddedTerminalFrameRows - EmbeddedTerminalHeaderRows
	if height > 0 {
		return height
	}
	return 0
}

// EmbeddedTerminalPTYWidth returns the PTY width for an embedded terminal pane
// allocation. PTY dimensions are clamped positive because the terminal backend
// normalizes to positive sizes, even when a tiny frame leaves no drawable body.
func EmbeddedTerminalPTYWidth(outerWidth int) int {
	if width := EmbeddedTerminalRenderContentWidth(outerWidth); width > 0 {
		return width
	}
	return 1
}

// EmbeddedTerminalPTYHeight returns the PTY height for an embedded terminal
// pane allocation, excluding the border and non-PTY header row. Tiny panes with
// no drawable body still receive the backend's positive minimum height.
func EmbeddedTerminalPTYHeight(outerHeight int) int {
	if height := EmbeddedTerminalRenderBodyHeight(outerHeight); height > 0 {
		return height
	}
	return 1
}

// Render produces the full terminal view string.
func Render(p RenderParams) string {
	if p.Width == 0 {
		p.Width = 80
	}
	if p.Height == 0 {
		p.Height = 24
	}

	if p.Overlay != OverlayNone {
		if p.Overlay == OverlaySelect {
			return renderSelectOverlay(p)
		}
		return renderOverlay(p)
	}

	return renderApplication(p)
}

func renderApplication(p RenderParams) string {
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
	embeddedTerminalActive := p.Mode == ModeSessions && len(p.EmbeddedTerminals) > 0
	flowEmbeddedTerminalActive := p.Mode == ModeFlows && len(p.FlowEmbeddedTerminals) > 0
	terminalShortcutsActive := embeddedTerminalActive || (flowEmbeddedTerminalActive && p.FlowTerminalFocused)
	sessionSelected := p.Mode == ModeSessions && !embeddedTerminalActive && p.SessionSelected >= 0 && p.SessionSelected < len(p.Sessions)
	planSelected := p.Mode == ModePlans && p.PlanSelected >= 0 && p.PlanSelected < len(p.Plans)
	flowSelected := p.Mode == ModeFlows && p.FlowSelected >= 0 && p.FlowSelected < len(p.Flows)
	flowPlanLinked := false
	flowAutoModeSelected := false
	if flowSelected {
		flowPlanLinked = strings.TrimSpace(p.Flows[p.FlowSelected].PlanID) != ""
		flowAutoModeSelected = p.FlowAutoModeSelected
	}
	worktreeSessionSelected := p.Mode == ModeWorktrees && p.InlineWorktreeSessions && p.WorktreeSessionSelected >= 0 && p.WorktreeSessionSelected < len(p.WorktreeSessions)
	selectedPlanPhaseID := scopedSelectedPlanPhaseID(p, planSelected)
	selectedFlowPhaseID := scopedSelectedFlowPhaseID(p, flowSelected)
	flowDeletableSelected := flowSelected && selectedFlowPhaseID == ""
	if p.FlowTerminalFocused {
		flowSelected = false
		selectedFlowPhaseID = ""
		flowDeletableSelected = false
		flowAutoModeSelected = false
	}
	planPhaseSelected := selectedPlanPhaseID != ""
	flowPhaseSelected := selectedFlowPhaseID != ""
	status := statusBarParams{
		Width:                       p.Width,
		Mode:                        p.Mode,
		Overlay:                     p.Overlay,
		InputMode:                   inputRenderParamsFrom(p).mode,
		WorktreeInputPrompt:         p.WorktreeInputPrompt,
		ActivePane:                  p.ActivePane,
		Destructive:                 p.Destructive,
		RepoSelected:                repoPath != "",
		WorktreeSelected:            worktreeSelected,
		StaleSelected:               staleSelected,
		DirtySelected:               dirtySelected,
		LockedSelected:              lockedSelected,
		WorktreeDeletableSelected:   worktreeDeletableSelected,
		WorktreeOpenableSelected:    worktreeOpenableSelected,
		WorktreeMoveSelected:        worktreeMoveSelected,
		WorktreeSessionsOpen:        p.WorktreeSessionsOpen,
		WorktreeSessionSelected:     worktreeSessionSelected,
		BranchDirtySelected:         branchDirtySelected,
		BranchDeletableSelected:     branchDeletableSelected,
		BranchOpenableSelected:      branchOpenableSelected,
		StashSelected:               stashSelected,
		CommitSelected:              commitSelected,
		ReflogSelected:              reflogSelected,
		SessionSelected:             sessionSelected,
		EmbeddedTerminalActive:      terminalShortcutsActive,
		EmbeddedTerminalPrefix:      p.EmbeddedTerminalPrefix || p.FlowEmbeddedTerminalPrefix,
		PlanSelected:                planSelected,
		PlanPhaseSelected:           planPhaseSelected,
		FlowSelected:                flowSelected,
		FlowPhaseSelected:           flowPhaseSelected,
		FlowDeletableSelected:       flowDeletableSelected,
		FlowPlanLinked:              flowPlanLinked,
		FlowHeadless:                p.FlowHeadless,
		FlowAutoModeSelected:        flowAutoModeSelected,
		FlowReasoningEffort:         p.FlowReasoningEffort,
		FlowPhaseLaunchReady:        p.FlowPhaseLaunchReady,
		FlowNextLaunchReady:         p.FlowNextLaunchReady,
		FlowPhaseResetReadySelected: p.FlowPhaseResetReadySelected,
		FlowPhaseResumableSelected:  p.FlowPhaseResumableSelected,
		TransientError:              p.TransientError,
		TransientErrorFadeStep:      p.TransientErrorFadeStep,
		SearchActive:                p.SearchActive,
		RepoSearch:                  p.RepoSearch,
		ItemSearch:                  p.ItemSearch,
		FetchAvailable:              p.FetchAvailable,
		FetchVisibleAvailable:       p.FetchVisibleAvailable,
		RepoCreateAvailable:         p.RepoCreateAvailable,
		PullAvailable:               p.PullAvailable,
		AgentAvailable:              p.AgentAvailable,
		NewAgent:                    p.NewAgentAvailable,
	}
	innerHeight := p.Height - 3 // status bar + top/bottom borders
	activeStatusQuery := hasActiveStatusQuery(status)
	showShortcutPane := shouldRenderShortcutPaneForViewport(p.Width, p.Height, activeStatusQuery)
	statusBar := renderFooterStatusBar(status, !showShortcutPane)

	// Border colors based on active pane.
	activeBorderColor := clearDarkTheme.activeBorder
	inactiveBorderColor := clearDarkTheme.inactiveBorder
	destructiveBorderColor := clearDarkTheme.destructiveBorder

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

	rightContentWidth := RightContentWidth(p.Width, p.Height, activeStatusQuery)

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
		selectedFlowPhaseID = ""
	}
	if p.FlowTerminalFocused {
		flowSel = -1
		selectedFlowPhaseID = ""
	}

	var rightLines []string
	switch {
	case p.Mode == ModeWorktrees && len(p.Worktrees) > 0:
		rightLines = renderWorktreePaneWithSessions(p.Worktrees, worktreeSel, p.WorktreeScroll, rightContentWidth, rightContentHeight, p.InlineWorktreeSessions, p.WorktreeSessions, p.WorktreeSessionSelected, p.WorktreeSessionScroll)
	case p.Mode == ModeBranches && len(p.Branches) > 0:
		rightLines = renderBranchPaneSelected(p.Branches, branchSel, p.BranchScroll, rightContentWidth, rightContentHeight, repoPath)
	case p.Mode == ModeStashes && len(p.Stashes) > 0:
		rightLines = renderStashPane(p.Stashes, stashSel, p.StashScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeHistory && len(p.Commits) > 0:
		rightLines = renderCommitPane(p.Commits, commitSel, p.CommitScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeReflog && len(p.Reflogs) > 0:
		rightLines = renderReflogPane(p.Reflogs, reflogSel, p.ReflogScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModeSessions && len(p.EmbeddedTerminals) > 0:
		rightLines = renderEmbeddedTerminalPane(p.EmbeddedTerminals, p.EmbeddedTerminalLines, p.EmbeddedTerminalPrefix, p.ActivePane == 1, rightContentWidth, rightContentHeight)
	case p.Mode == ModeSessions && len(p.Sessions) > 0:
		rightLines = renderSessionPane(p.Sessions, sessionSel, p.SessionScroll, rightContentWidth, rightContentHeight)
	case p.Mode == ModePlans && len(p.Plans) > 0:
		rightLines = renderPlanPane(p.Plans, planSel, p.PlanScroll, rightContentWidth, rightContentHeight, p.ExpandedPlanID, selectedPlanPhaseID)
	case p.Mode == ModeFlows && len(p.FlowEmbeddedTerminals) > 0:
		rightLines = renderFlowSplitPane(p.Flows, flowSel, p.FlowScroll, rightContentWidth, rightContentHeight, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity, p.FlowEmbeddedTerminals, p.FlowEmbeddedTerminalLines, p.FlowEmbeddedTerminalPrefix, p.ActivePane == 1 && p.FlowTerminalFocused)
	case p.Mode == ModeFlows && len(p.Flows) > 0:
		rightLines = renderFlowPane(p.Flows, flowSel, p.FlowScroll, rightContentWidth, rightContentHeight, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity)
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

func scopedSelectedFlowPhaseID(p RenderParams, flowSelected bool) string {
	if !flowSelected || p.SelectedFlowPhaseID == "" {
		return ""
	}
	flow := p.Flows[p.FlowSelected]
	if p.ExpandedFlowID != flow.FlowID {
		return ""
	}
	wantPhaseID := artifacts.NormalizePhaseID(p.SelectedFlowPhaseID)
	if wantPhaseID == "" {
		return ""
	}
	for _, phase := range flowstore.OrderedPhases(flow.Phases) {
		if artifacts.NormalizePhaseID(phase.PhaseID) == wantPhaseID {
			return p.SelectedFlowPhaseID
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
	Width                       int
	Mode                        Mode
	Overlay                     OverlayState
	InputMode                   InputMode
	WorktreeInputPrompt         string
	ActivePane                  int
	Destructive                 bool
	RepoSelected                bool
	WorktreeSelected            bool
	StaleSelected               bool
	DirtySelected               bool
	LockedSelected              bool
	WorktreeDeletableSelected   bool
	WorktreeOpenableSelected    bool
	WorktreeMoveSelected        bool
	WorktreeSessionsOpen        bool
	WorktreeSessionSelected     bool
	BranchDirtySelected         bool
	BranchDeletableSelected     bool
	BranchOpenableSelected      bool
	StashSelected               bool
	CommitSelected              bool
	ReflogSelected              bool
	SessionSelected             bool
	EmbeddedTerminalActive      bool
	EmbeddedTerminalPrefix      bool
	PlanSelected                bool
	PlanPhaseSelected           bool
	FlowSelected                bool
	FlowPhaseSelected           bool
	FlowDeletableSelected       bool
	FlowPlanLinked              bool
	FlowHeadless                bool
	FlowAutoModeSelected        bool
	FlowReasoningEffort         string
	FlowPhaseLaunchReady        bool
	FlowNextLaunchReady         bool
	FlowPhaseResetReadySelected bool
	FlowPhaseResumableSelected  bool
	TransientError              string
	TransientErrorFadeStep      int
	SearchActive                bool
	RepoSearch                  string
	ItemSearch                  string
	FetchAvailable              bool
	FetchVisibleAvailable       bool
	RepoCreateAvailable         bool
	PullAvailable               bool
	AgentAvailable              bool
	NewAgent                    bool
}

type shortcutHint struct {
	Key           string
	Label         string
	SuccessSuffix string
	Warning       bool
	Inline        bool
	Muted         bool
}

type shortcutSection struct {
	Title string
	Hints []shortcutHint
}

func shouldRenderShortcutPane(width, height int, sp statusBarParams) bool {
	return !hasActiveStatusQuery(sp) && shouldRenderShortcutPaneForInnerHeight(width, height)
}

func shouldRenderShortcutPaneForViewport(width, height int, activeStatusQuery bool) bool {
	return !activeStatusQuery && shouldRenderShortcutPaneForInnerHeight(width, height-RepoContentOverhead)
}

func shouldRenderShortcutPaneForInnerHeight(width, height int) bool {
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
	case overlay == OverlayInput:
		if sp.InputMode == InputMultiLine {
			return renderStatusText(width, "  enter: submit  alt+enter: newline  esc: cancel  bksp/del: edit")
		}
		return renderStatusText(width, "  enter: submit  esc: cancel  bksp/del: edit  left/right: move")
	case overlay == OverlaySelect:
		return renderStatusText(width, "  up/down select  enter: confirm  esc: cancel")
	case overlay == OverlayForm:
		return renderStatusText(width, "  tab/up/down: fields  space: toggle/select  enter: submit  esc: cancel")
	case overlay != OverlayNone:
		return statusStyle.Width(width).Render("  ↑/↓ scroll  esc: close")
	}

	sections := shortcutSections(sp)
	shortcuts := renderFooterShortcuts(sp, sections)
	if strings.Contains(shortcuts, "\n") || lipgloss.Width(shortcuts) > width {
		shortcuts = renderFooterShortcuts(sp, withoutShortcutKey(sections, "f5"))
	}
	return statusStyle.Width(width).Render(shortcuts)
}

func renderStatusText(width int, text string) string {
	return statusStyle.Width(width).Render(ansi.Truncate(text, width, ""))
}

func renderShortcutPane(sp statusBarParams, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, 0)
	titleStyle := shortcutTitleStyle
	modeStyle := shortcutModeStyle
	groupStyle := shortcutGroupStyle
	if shortcutsMuted(sp) {
		titleStyle = statusStyle
		modeStyle = statusStyle
		groupStyle = statusStyle
	}
	title := titleStyle.Render("Shortcuts") + "  " + modeStyle.Render(modeShortcutTitle(sp.Mode))
	lines = append(lines, ansi.Truncate(" "+title, width, ""))
	compact := height <= 3
	tight := height <= 7
	if !compact && !tight {
		lines = append(lines, strings.Repeat(" ", width))
	}
	sectionCount := 0

	for _, section := range shortcutSectionsForPane(sp, height) {
		hints := sidebarShortcutHints(section.Hints)
		if len(hints) == 0 {
			continue
		}
		if !compact {
			if sectionCount > 0 && !tight {
				lines = append(lines, strings.Repeat(" ", width))
			}
			lines = append(lines, truncateToWidth(" "+groupStyle.Render(section.Title), width))
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
	labelStyle := shortcutTextStyle
	if hint.Warning {
		keyStyle = dirtyRedStyle.Bold(true)
	} else if hint.Muted {
		keyStyle = statusStyle
		labelStyle = statusStyle
	}
	key := padShortcutKey(keyStyle.Render(hint.Key), shortcutKeyColumnWidth)
	label := renderShortcutHintLabel(hint, labelStyle)
	return ansi.Truncate(" "+key+" "+label, width, "")
}

func renderShortcutHintLabel(hint shortcutHint, labelStyle lipgloss.Style) string {
	return renderShortcutHintLabelWithRestore(hint, labelStyle, "")
}

func renderShortcutHintLabelWithRestore(hint shortcutHint, labelStyle lipgloss.Style, restoreSequence string) string {
	if hint.SuccessSuffix == "" || hint.Warning || hint.Muted {
		return labelStyle.Render(hint.Label)
	}
	prefix, ok := strings.CutSuffix(hint.Label, hint.SuccessSuffix)
	if !ok {
		return labelStyle.Render(hint.Label)
	}
	return labelStyle.Render(prefix) + shortcutSuccessStyle.Render(hint.SuccessSuffix) + restoreSequence
}

// styleStartSequence returns the zero-width ANSI prefix for restoring a style
// after a nested styled segment resets terminal attributes.
func styleStartSequence(style lipgloss.Style) string {
	const marker = "\x00"
	rendered := style.Render(marker)
	before, _, ok := strings.Cut(rendered, marker)
	if !ok {
		return ""
	}
	return before
}

func sidebarShortcutHints(hints []shortcutHint) []shortcutHint {
	grouped := make([]shortcutHint, 0, len(hints))
	for i := 0; i < len(hints); i++ {
		hint := hints[i]
		if i+1 < len(hints) {
			next := hints[i+1]
			switch {
			case hint.Key == "↑/↓" && next.Key == "←/→":
				grouped = append(grouped, shortcutHint{Key: "↑/↓ ←/→", Label: "select/pane/view", Warning: hint.Warning || next.Warning})
				i++
				continue
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
	if (sp.Mode == ModeSessions || sp.Mode == ModeFlows) && sp.EmbeddedTerminalActive {
		hints := []shortcutHint{{Key: "ctrl+]", Label: "commands"}}
		if sp.EmbeddedTerminalPrefix {
			hints = []shortcutHint{
				{Key: "ctrl+]", Label: "send"},
				{Key: "x", Label: "close"},
				{Key: "q/esc", Label: "quit"},
				{Key: "1-9", Label: "switch"},
			}
			if sp.Mode == ModeSessions {
				hints = slices.Insert(hints, 1, shortcutHint{Key: "l", Label: "sessions"})
			} else {
				hints = slices.Insert(hints, 1,
					shortcutHint{Key: "i", Label: "input"},
					shortcutHint{Key: "left/right", Label: "terminal"},
				)
			}
		}
		sections := []shortcutSection{{Title: "Terminal", Hints: hints}}
		if !sp.EmbeddedTerminalPrefix {
			sections = muteShortcutSections(sections)
		}
		return sections
	}

	navigation := []shortcutHint{
		{Key: "↑/↓", Label: "select", Inline: true},
		{Key: "←/→", Label: "pane/view", Inline: true},
	}
	global := []shortcutHint{
		{Key: "tab", Label: "pane"},
		{Key: "q/esc", Label: "quit"},
		{Key: "A", Label: "set agent"},
		{Key: "f5", Label: "refresh"},
	}

	var actions []shortcutHint
	if sp.ActivePane == 0 && sp.FetchVisibleAvailable {
		actions = append(actions, shortcutHint{Key: "f", Label: "fetch visible"})
	}
	if sp.ActivePane == 0 && sp.RepoCreateAvailable {
		actions = append(actions, shortcutHint{Key: "n", Label: "new repo"})
	}
	switch sp.Mode {
	case ModeWorktrees:
		if sp.ActivePane == 1 && sp.RepoSelected && !sp.StaleSelected {
			actions = append(actions, shortcutHint{Key: "n", Label: "new worktree"})
			if sp.NewAgent {
				actions = append(actions, shortcutHint{Key: "N", Label: "new+agent"})
			}
			actions = append(actions, shortcutHint{Key: "P", Label: "PR"})
			if sp.WorktreeSessionsOpen && sp.WorktreeSessionSelected {
				actions = append(actions, shortcutHint{Key: "enter", Label: "resume"})
			} else if sp.WorktreeSelected && !sp.FetchAvailable && !sp.PullAvailable && !sp.AgentAvailable {
				actions = append(actions, shortcutHint{Key: "x", Label: "sessions"})
			}
			if sp.DirtySelected && !sp.WorktreeSessionsOpen {
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
				shortcutHint{Key: "o", Label: "transcript"},
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
				shortcutHint{Key: "x", Label: "phases"},
				shortcutHint{Key: "o", Label: "open"},
				shortcutHint{Key: "e", Label: "edit"},
				shortcutHint{Key: "a", Label: implementLabel},
				shortcutHint{Key: "y", Label: "copy path"},
			)
		}
	case ModeFlows:
		if sp.ActivePane == 1 && sp.RepoSelected {
			actions = append(actions, shortcutHint{Key: "n", Label: "new flow"})
			headlessLabel := "headless off"
			headlessSuccessSuffix := ""
			if sp.FlowHeadless {
				headlessLabel = "headless on"
				headlessSuccessSuffix = "on"
			}
			actions = append(actions, shortcutHint{Key: "h", Label: headlessLabel, SuccessSuffix: headlessSuccessSuffix})
			if sp.FlowSelected {
				autoHint := flowAutoModeShortcutHint(sp.FlowAutoModeSelected)
				if sp.FlowPhaseSelected {
					actions = append(actions, shortcutHint{Key: "enter", Label: "phases"})
					if sp.FlowNextLaunchReady {
						actions = append(actions, shortcutHint{Key: "ctrl+enter", Label: "launch next"})
					}
					if sp.FlowPhaseResetReadySelected {
						actions = append(actions, shortcutHint{Key: "x", Label: "reset ready"})
					}
					actions = append(actions, shortcutHint{Key: "y", Label: "copy phase id"})
					if sp.FlowPhaseResumableSelected {
						actions = append(actions, shortcutHint{Key: "r", Label: "resume"})
					}
					actions = append(actions, autoHint)
				} else {
					actions = append(actions, shortcutHint{Key: "enter", Label: "phases"})
					if sp.FlowNextLaunchReady {
						actions = append(actions, shortcutHint{Key: "ctrl+enter", Label: "launch next"})
					}
					if sp.FlowPlanLinked {
						actions = append(actions, shortcutHint{Key: "o", Label: "open"})
					}
					actions = append(actions, shortcutHint{Key: "y", Label: "copy id"})
					if sp.Destructive && sp.FlowDeletableSelected {
						actions = append(actions, shortcutHint{Key: "d", Label: "delete", Warning: true})
					}
					actions = append(actions, autoHint)
				}
			}
			actions = append(actions, shortcutHint{Key: "E", Label: flowReasoningEffortShortcutLabel(sp.FlowReasoningEffort)})
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
	if !sp.Destructive && (sp.Mode == ModeWorktrees || sp.Mode == ModeBranches || sp.Mode == ModeStashes || sp.Mode == ModeFlows) {
		actions = append([]shortcutHint{{Key: "D", Label: "destructive mode"}}, actions...)
	}
	var sections []shortcutSection
	if len(actions) > 0 {
		sections = append(sections, shortcutSection{Title: "Actions", Hints: actions})
	}
	sections = append(sections, shortcutSection{Title: "Navigate", Hints: navigation})
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
	sections = append(sections, shortcutSection{Title: "Global", Hints: global})
	return sections
}

func flowAutoModeShortcutHint(enabled bool) shortcutHint {
	if enabled {
		return shortcutHint{Key: "m", Label: "auto: on", SuccessSuffix: "on"}
	}
	return shortcutHint{Key: "m", Label: "auto: off"}
}

func shortcutsMuted(sp statusBarParams) bool {
	return (sp.Mode == ModeSessions || sp.Mode == ModeFlows) && sp.EmbeddedTerminalActive && !sp.EmbeddedTerminalPrefix
}

func flowReasoningEffortShortcutLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "effort: default"
	}
	return value
}

func muteShortcutSections(sections []shortcutSection) []shortcutSection {
	muted := make([]shortcutSection, 0, len(sections))
	for _, section := range sections {
		hints := make([]shortcutHint, 0, len(section.Hints))
		for _, hint := range section.Hints {
			hint.Muted = true
			hints = append(hints, hint)
		}
		section.Hints = hints
		muted = append(muted, section)
	}
	return muted
}

func shortcutSectionsForPane(sp statusBarParams, height int) []shortcutSection {
	sections := shortcutSections(sp)
	if height < 20 {
		return withoutShortcutKey(sections, "f5")
	}
	return sections
}

func withoutShortcutKey(sections []shortcutSection, key string) []shortcutSection {
	return withoutShortcutKeys(sections, key)
}

func withoutShortcutKeys(sections []shortcutSection, keys ...string) []shortcutSection {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}
	filtered := make([]shortcutSection, 0, len(sections))
	for _, section := range sections {
		hints := make([]shortcutHint, 0, len(section.Hints))
		for _, hint := range section.Hints {
			if _, ok := drop[hint.Key]; ok {
				continue
			}
			hints = append(hints, hint)
		}
		if len(hints) == 0 {
			continue
		}
		section.Hints = hints
		filtered = append(filtered, section)
	}
	return filtered
}

func renderFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	if sp.Mode == ModeWorktrees {
		return renderWorktreeFooterShortcuts(sp, sections)
	}
	if sp.Mode == ModeBranches {
		return renderBranchFooterShortcuts(sp, sections)
	}
	if sp.Mode == ModeFlows {
		return renderFlowFooterShortcuts(sp, sections)
	}
	return renderGenericFooterShortcuts(sp, sections)
}

func transientStatusStyle(fadeStep int) lipgloss.Style {
	switch fadeStep {
	case 1:
		return lipgloss.NewStyle().Foreground(clearDarkTheme.palette.muted)
	case 2:
		return lipgloss.NewStyle().Foreground(clearDarkTheme.palette.borderMuted)
	default:
		return dirtyRedStyle
	}
}

func renderWorktreeFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	hints := flattenShortcutHints(sections)
	base := footerHintsForKeys(hints, "tab", "q/esc")
	agent := footerHintsForKeys(hints, "A")
	upDown := footerHintsForKeys(hints, "↑/↓")
	arrow := footerHintsForKeys(hints, "←/→")
	safety := footerHintsForKeys(hints, "D")
	allActions := worktreeFooterParts(hints, false)
	compactActions := worktreeCompactFooterParts(hints)

	if len(allActions) == 0 {
		for _, parts := range [][]string{
			appendParts(base, agent, upDown, arrow, safety),
			appendParts(base, upDown, arrow, safety),
			appendParts(base, arrow, safety),
			appendParts(base, arrow),
			appendParts(base, upDown, safety),
			base,
		} {
			if candidate, ok := footerCandidate(sp.Width, parts); ok {
				return candidate
			}
		}
	}

	candidates := [][]string{
		appendParts(base, agent, upDown, arrow, safety, allActions),
		appendParts(base, upDown, safety, allActions),
		appendParts(base, upDown, allActions),
		appendParts(base, arrow, compactActions),
		appendParts(base, compactActions),
		appendParts(arrow, compactActions),
		compactActions,
		base,
	}
	for _, parts := range candidates {
		if candidate, ok := footerCandidate(sp.Width, parts); ok {
			return candidate
		}
	}
	candidate := "  " + strings.Join(compactActions, " ")
	return ansi.Truncate(candidate, sp.Width, "")
}

func renderGenericFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	for _, drop := range [][]string{
		{},
		{"f5"},
		{"f5", "A"},
		{"f5", "A", "D"},
		{"f5", "A", "D", "←/→"},
		{"f5", "A", "D", "←/→", "↑/↓"},
		{"f5", "A", "D", "←/→", "↑/↓", "q/esc"},
		{"f5", "A", "D", "←/→", "↑/↓", "q/esc", "tab"},
	} {
		candidate := "  " + renderFooterHintList(footerSectionOrder(withoutShortcutKeys(sections, drop...)))
		if lipgloss.Width(candidate) <= sp.Width {
			return candidate
		}
	}
	candidate := "  " + renderFooterHintList(footerSectionOrder(withoutShortcutKeys(sections, "f5", "A", "D", "←/→", "↑/↓", "q/esc", "tab")))
	return ansi.Truncate(candidate, sp.Width, "")
}

func renderFlowFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	if sp.EmbeddedTerminalActive {
		return renderGenericFooterShortcuts(sp, sections)
	}
	full := "  " + renderFooterHintList(footerSectionOrder(sections))
	if lipgloss.Width(full) <= sp.Width {
		return full
	}
	hints := flattenShortcutHints(sections)
	base := footerHintsForKeys(hints, "tab", "q/esc")
	upDown := footerHintsForKeys(hints, "↑/↓")
	arrow := footerHintsForKeys(hints, "←/→")
	coreActions := footerHintsForKeys(hints, "D", "h", "enter", "ctrl+enter", "d")
	actions := footerHintsForKeys(hints, "D", "n", "h", "enter", "ctrl+enter", "x", "o", "y", "d", "r", "E", "f", "F")
	actionsWithoutEffort := footerHintsForKeys(hints, "D", "n", "h", "enter", "ctrl+enter", "x", "o", "y", "d", "r", "f", "F")

	for _, parts := range [][]string{
		appendParts(base, upDown, arrow, actions),
		appendParts(base, upDown, arrow, actionsWithoutEffort),
		appendParts(base, arrow, actionsWithoutEffort),
		appendParts(base, arrow, actions),
		appendParts(base, arrow, coreActions),
		appendParts(arrow, coreActions),
		appendParts(coreActions),
		base,
	} {
		if candidate, ok := footerCandidate(sp.Width, parts); ok {
			return candidate
		}
	}
	candidate := "  " + strings.Join(appendParts(arrow, coreActions), " ")
	return ansi.Truncate(candidate, sp.Width, "")
}

func renderBranchFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	legend, rest := splitLegendSection(sections)
	rest = branchFooterSectionOrder(rest)
	hints := flattenShortcutHints(rest)
	base := footerHintsForKeys(hints, "tab", "q/esc")
	nav := footerHintsForKeys(hints, "↑/↓", "←/→")
	actions := footerHintsForKeys(hints, "D", "n", "enter", "d", "f", "F", "t", "c", "a")

	full := append(append(append([]string{}, base...), actions...), nav...)
	baseActions := append(append([]string{}, base...), actions...)
	baseNav := append(append([]string{}, base...), nav...)
	baseArrow := footerHintsForKeys(hints, "tab", "q/esc", "←/→")

	for _, parts := range [][]string{full, baseActions} {
		if candidate, ok := branchFooterCandidateWithLegend(sp.Width, legend, parts); ok {
			return candidate
		}
	}
	if sp.BranchDirtySelected {
		for _, parts := range [][]string{full, baseActions} {
			if candidate, ok := branchFooterCandidateWithoutLegend(sp.Width, parts); ok {
				return candidate
			}
		}
	}
	for _, parts := range [][]string{baseNav, baseArrow, base} {
		if candidate, ok := branchFooterCandidateWithLegend(sp.Width, legend, parts); ok {
			return candidate
		}
	}
	for _, parts := range [][]string{full, baseActions, baseNav, baseArrow, base} {
		if candidate, ok := branchFooterCandidateWithoutLegend(sp.Width, parts); ok {
			return candidate
		}
	}
	if len(base) > 0 {
		return "  " + strings.Join(base, " ")
	}
	return renderFooterLegend(legend)
}

func branchFooterCandidateWithLegend(width int, legend []shortcutHint, parts []string) (string, bool) {
	keys := strings.Join(parts, "  ")
	if keys == "" {
		candidate := renderFooterLegend(legend)
		return candidate, lipgloss.Width(candidate) <= width
	}
	candidate := renderFooterLegend(legend) + "  |  " + keys
	return candidate, lipgloss.Width(candidate) <= width
}

func branchFooterCandidateWithoutLegend(width int, parts []string) (string, bool) {
	keys := strings.Join(parts, "  ")
	if keys == "" {
		return "", false
	}
	candidate := "  " + keys
	return candidate, lipgloss.Width(candidate) <= width
}

func footerHintsForKeys(hints []shortcutHint, keys ...string) []string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if hint, ok := findShortcutHint(hints, key); ok {
			parts = append(parts, renderFooterHint(hint))
		}
	}
	return parts
}

func appendParts(groups ...[]string) []string {
	var parts []string
	for _, group := range groups {
		parts = append(parts, group...)
	}
	return parts
}

func footerCandidate(width int, parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	candidate := "  " + strings.Join(parts, " ")
	return candidate, lipgloss.Width(candidate) <= width
}

func worktreeCompactFooterParts(hints []shortcutHint) []string {
	critical := footerHintsForKeys(hints, "d", "p", "u", "enter")
	if len(critical) > 0 {
		return appendParts(critical, footerHintsForKeys(hints, "f", "F"))
	}
	return appendParts(footerHintsForKeys(hints, "n", "N", "m"), footerHintsForKeys(hints, "f", "F"))
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

	if hint.Warning {
		return dirtyRedStyle.Render(hint.Key + shortcutSeparator(hint) + " " + hint.Label)
	}
	if hint.Muted {
		return statusStyle.Render(hint.Key + shortcutSeparator(hint) + " " + hint.Label)
	}
	return hint.Key + shortcutSeparator(hint) + " " + renderShortcutHintLabelWithRestore(hint, lipgloss.NewStyle(), styleStartSequence(statusStyle))
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
			line = renderSelectedBranchRow(row, repoPath, width)
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

func renderSelectedBranchRow(row gitquery.BranchRow, repoPath string, width int) string {
	b := row.Branch
	line := selectedStyle.Render(" > ") + selectedSegment(branchStyle, b.Name)

	var indicators string
	if b.Ahead > 0 || b.Behind > 0 {
		indicators += selectedSegment(aheadBehindStyle, " ●")
		indicators += selectedStyle.Render(fmt.Sprintf(" +%d/-%d", b.Ahead, b.Behind))
	}
	if b.Dirty {
		indicators += renderSelectedDirtyIndicator(b.FilesChanged, b.LinesAdded, b.LinesDeleted)
	}
	if !b.HasUpstream || b.UpstreamGone {
		indicators += selectedSegment(noUpstreamStyle, " ●")
	}
	if b.Merged {
		indicators += selectedSegment(mergedStyle, " merged")
	}
	if indicators == "" {
		indicators = selectedSegment(cleanStyle, " ✔")
	}
	line += indicators

	if row.WorktreePath != "" {
		line += selectedStyle.Render(" ")
		if repoPath != "" && row.WorktreePath == repoPath {
			line += selectedSegment(rootStyle, "[root]")
		} else {
			line += selectedSegment(commitStyle, fmt.Sprintf("[%s]", row.WorktreePath))
		}
	}
	return renderSelectedRow(line, width)
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

func renderEmbeddedTerminalPane(tabs []EmbeddedTerminalTab, liveLines []string, prefixActive, focused bool, outerWidth, outerHeight int) []string {
	if outerHeight <= 0 {
		return nil
	}
	contentWidth := EmbeddedTerminalRenderContentWidth(outerWidth)
	header := truncateToWidth(renderEmbeddedTerminalHeader(tabs), contentWidth)
	if prefixActive {
		header = truncateToWidth(header+"  "+statusStyle.Render("ctrl+]"), contentWidth)
	}
	bodyHeight := EmbeddedTerminalRenderBodyHeight(outerHeight)
	if len(liveLines) > bodyHeight {
		liveLines = liveLines[len(liveLines)-bodyHeight:]
	}
	contentHeight := outerHeight - EmbeddedTerminalFrameRows
	if contentHeight < 0 {
		contentHeight = 0
	}
	contentLines := make([]string, 0, contentHeight)
	if contentHeight >= EmbeddedTerminalHeaderRows {
		contentLines = append(contentLines, header)
	}
	for _, line := range liveLines {
		contentLines = append(contentLines, truncateToWidth(line, contentWidth))
	}
	if len(contentLines) < contentHeight {
		contentLines = append(contentLines, make([]string, contentHeight-len(contentLines))...)
	}
	return renderEmbeddedTerminalFrame(contentLines, focused, outerWidth, outerHeight)
}

func renderEmbeddedTerminalFrame(contentLines []string, focused bool, outerWidth, outerHeight int) []string {
	if outerHeight <= 0 {
		return nil
	}
	contentWidth := EmbeddedTerminalRenderContentWidth(outerWidth)
	frameInnerWidth := outerWidth - EmbeddedTerminalFrameColumns
	if frameInnerWidth < 0 {
		frameInnerWidth = 0
	}
	style := embeddedTerminalBorderStyle(focused)
	top := style.Render("┌" + strings.Repeat("─", frameInnerWidth) + "┐")
	bottom := style.Render("└" + strings.Repeat("─", frameInnerWidth) + "┘")
	if outerWidth <= 1 {
		top = style.Render("┌")
		bottom = style.Render("└")
	}
	lines := []string{top}
	if outerHeight >= 2 {
		if outerHeight > 2 {
			for i := 0; i < outerHeight-2; i++ {
				content := ""
				if i < len(contentLines) {
					content = contentLines[i]
				}
				lines = append(lines, renderEmbeddedTerminalFrameContentLine(content, style, contentWidth, outerWidth))
			}
		}
		lines = append(lines, bottom)
	}
	return fitEmbeddedTerminalFrameLines(lines, outerWidth, outerHeight)
}

func renderEmbeddedTerminalFrameContentLine(content string, borderStyle lipgloss.Style, contentWidth, outerWidth int) string {
	if outerWidth <= 0 {
		return ""
	}
	if outerWidth == 1 {
		return borderStyle.Render("│")
	}
	content = truncateToWidth(content, contentWidth)
	if padding := contentWidth - lipgloss.Width(content); padding > 0 {
		content += strings.Repeat(" ", padding)
	}
	sidePadding := strings.Repeat(" ", embeddedTerminalEffectiveSidePadding(outerWidth))
	return borderStyle.Render("│") + sidePadding + content + sidePadding + borderStyle.Render("│")
}

func embeddedTerminalBorderStyle(focused bool) lipgloss.Style {
	color := clearDarkTheme.inactiveBorder
	if focused {
		color = clearDarkTheme.activeBorder
	}
	return lipgloss.NewStyle().Foreground(color)
}

func fitEmbeddedTerminalFrameLines(lines []string, outerWidth, outerHeight int) []string {
	if outerHeight <= 0 {
		return nil
	}
	fitted := make([]string, outerHeight)
	copy(fitted, lines)
	for i, line := range fitted {
		fitted[i] = truncateToWidth(line, outerWidth)
	}
	return fitted
}

func renderEmbeddedTerminalHeader(tabs []EmbeddedTerminalTab) string {
	parts := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := fmt.Sprintf("%d", tab.Number)
		for _, value := range []string{tab.Provider, tab.Identity, tab.State} {
			if strings.TrimSpace(value) != "" {
				label += " " + strings.TrimSpace(value)
			}
		}
		if tab.Active {
			label = selectedStyle.Render(label)
		} else {
			label = statusStyle.Render(label)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "  ")
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

func renderFlowSplitPane(records []flowstore.FlowRecord, selected, scroll, width, height int, expandedFlowID, selectedPhaseID string, activity []FlowTerminalActivity, terminals []EmbeddedTerminalTab, terminalLines []string, prefixActive, terminalFocused bool) []string {
	if height <= 0 {
		return nil
	}
	listHeight, terminalHeight := FlowSplitPanelHeights(height)
	lines := make([]string, 0, height)
	if len(records) > 0 {
		lines = append(lines, renderFlowPane(records, selected, scroll, width, listHeight, expandedFlowID, selectedPhaseID, activity)...)
	} else {
		lines = append(lines, renderPlaceholderPane(width, listHeight, "No flows")...)
	}
	lines = append(lines, renderEmbeddedTerminalPane(terminals, terminalLines, prefixActive, terminalFocused, width, terminalHeight)...)
	return scrollAndPad(lines, 0, height)
}

func renderFlowPane(records []flowstore.FlowRecord, selected, scroll, width, height int, expandedFlowID, selectedPhaseID string, activity []FlowTerminalActivity) []string {
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatFlowColumns("   ", "Status", "Branch", "Phase", "Plan", "PR", "Updated", "Title")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}

	active := newFlowTerminalActivitySet(activity)
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
		rowSelected := i == selected && selectedPhaseID == ""
		statusCell := statusStyle.Render(fitSessionColumn(record.Status, flowStatusWidth))
		branchCell := branchStyle.Render(fitSessionColumn(branch, flowBranchWidth))
		phaseCell := diffHdrStyle.Render(fitSessionColumn(phase, flowPhaseWidth))
		planCell := statusStyle.Render(fitSessionColumn(plan, flowPlanWidth))
		prCell := statusStyle.Render(fitSessionColumn(pr, flowPRWidth))
		updatedCell := stashDateStyle.Render(fitSessionColumn(updated, flowUpdatedWidth))
		titleCell := stashMsgStyle.Render(record.Title)
		if record.AutoMode && !rowSelected {
			statusCell = flowAutoModeStyle.Render(fitSessionColumn(record.Status, flowStatusWidth))
			branchCell = flowAutoModeStyle.Render(fitSessionColumn(branch, flowBranchWidth))
			phaseCell = flowAutoModeStyle.Render(fitSessionColumn(phase, flowPhaseWidth))
			planCell = flowAutoModeStyle.Render(fitSessionColumn(plan, flowPlanWidth))
			prCell = flowAutoModeStyle.Render(fitSessionColumn(pr, flowPRWidth))
			updatedCell = flowAutoModeStyle.Render(fitSessionColumn(updated, flowUpdatedWidth))
			titleCell = flowAutoModeStyle.Render(record.Title)
		}
		line := formatFlowColumns(flowRowPrefix(false, active.hasFlow(record.FlowID)),
			statusCell,
			branchCell,
			phaseCell,
			planCell,
			prCell,
			updatedCell,
			titleCell,
		)
		if rowSelected {
			line = renderSelectedFlowColumns(selectedFlowRowPrefix(active.hasFlow(record.FlowID)),
				record.Status,
				branch,
				phase,
				plan,
				pr,
				updated,
				record.Title,
				width)
		}
		rows = append(rows, truncateToWidth(line, width))
		if record.FlowID == expandedFlowID {
			rows = append(rows, renderFlowPhaseRows(record, width, selectedPhaseID, active)...)
		}
	}
	return append([]string{header}, scrollAndPad(rows, scroll, rowHeight)...)
}

func renderFlowPhaseRows(record flowstore.FlowRecord, width int, selectedPhaseID string, active flowTerminalActivitySet) []string {
	if len(record.Phases) == 0 {
		return []string{truncateToWidth("      No phases", width)}
	}
	rows := make([]string, 0, len(record.Phases))
	for _, phase := range flowstore.OrderedPhases(record.Phases) {
		state := flowPhaseState(record, phase)
		sessionSummary := flowPhaseSessionSummary(phase)
		title := phase.Title
		if phase.ParentPhaseID != "" {
			title = "  " + title
		}
		if sessionSummary != "" {
			title += "  " + sessionSummary
		}
		rowActive := active.hasPhase(record.FlowID, phase.PhaseID)
		line := formatFlowColumns(flowPhaseRowPrefix(false, rowActive),
			statusStyle.Render(fitSessionColumn(phase.Status, flowStatusWidth)),
			"",
			diffHdrStyle.Render(fitSessionColumn(phase.PhaseID+":"+state, flowPhaseWidth)),
			"",
			"",
			"",
			stashMsgStyle.Render(title),
		)
		if phase.PhaseID == selectedPhaseID {
			line = renderSelectedFlowColumns(selectedFlowPhaseRowPrefix(rowActive),
				phase.Status,
				"",
				phase.PhaseID+":"+state,
				"",
				"",
				"",
				title,
				width)
		}
		rows = append(rows, truncateToWidth(line, width))
	}
	return rows
}

type flowTerminalActivitySet struct {
	flows  map[string]struct{}
	phases map[string]map[string]struct{}
}

func newFlowTerminalActivitySet(activity []FlowTerminalActivity) flowTerminalActivitySet {
	set := flowTerminalActivitySet{
		flows:  make(map[string]struct{}, len(activity)),
		phases: make(map[string]map[string]struct{}, len(activity)),
	}
	for _, item := range activity {
		if item.FlowID == "" {
			continue
		}
		set.flows[item.FlowID] = struct{}{}
		phaseID := artifacts.NormalizePhaseID(item.PhaseID)
		if phaseID == "" {
			continue
		}
		if set.phases[item.FlowID] == nil {
			set.phases[item.FlowID] = make(map[string]struct{}, 1)
		}
		set.phases[item.FlowID][phaseID] = struct{}{}
	}
	return set
}

func (s flowTerminalActivitySet) hasFlow(flowID string) bool {
	_, ok := s.flows[flowID]
	return ok
}

func (s flowTerminalActivitySet) hasPhase(flowID, phaseID string) bool {
	phases, ok := s.phases[flowID]
	if !ok {
		return false
	}
	_, ok = phases[artifacts.NormalizePhaseID(phaseID)]
	return ok
}

func flowPhaseRowPrefix(selected, active bool) string {
	return "   " + flowRowPrefix(selected, active)
}

func selectedFlowPhaseRowPrefix(active bool) string {
	return selectedStyle.Render("   ") + selectedFlowRowPrefix(active)
}

func flowRowPrefix(selected, active bool) string {
	selection := " "
	if selected {
		selection = ">"
	}
	marker := " "
	if active {
		marker = flowTerminalStyle.Render("●")
	}
	return selection + marker + " "
}

func selectedFlowRowPrefix(active bool) string {
	if active {
		return selectedStyle.Render(">") + selectedSegment(flowTerminalStyle, "●") + selectedStyle.Render(" ")
	}
	return selectedStyle.Render(">  ")
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

func renderSelectedFlowColumns(prefix, status, branch, phase, plan, pr, updated, title string, width int) string {
	line := prefix
	line += selectedStyle.Render(fitSessionColumn(status, flowStatusWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(branch, flowBranchWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(phase, flowPhaseWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(plan, flowPlanWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(pr, flowPRWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(updated, flowUpdatedWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(title)
	return renderSelectedRow(line, width)
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
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		return "missing-session-id"
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
	return flowstore.PhaseAwaitingSession(phase)
}

func flowPhaseSessionMismatch(phase flowstore.FlowPhase) bool {
	return flowstore.PhaseSessionLaunchMismatch(phase)
}

func flowPhaseSessionSummary(phase flowstore.FlowPhase) string {
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		return ""
	}
	session, ok := flowstore.LatestPhaseSession(phase, true)
	if !ok {
		return ""
	}
	count := 0
	for _, session := range phase.Sessions {
		if strings.TrimSpace(session.SessionID) != "" {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	label := "sessions"
	if count == 1 {
		label = "session"
	}
	parts := []string{fmt.Sprintf("%d %s", count, label)}
	if session.Provider != "" {
		parts = append(parts, session.Provider)
	}
	if session.Status != "" {
		parts = append(parts, session.Status)
	}
	return strings.Join(parts, " ")
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
	return renderWorktreePaneWithSessions(worktrees, selected, scroll, width, height, false, nil, 0, 0)
}

func renderWorktreePaneWithSessions(worktrees []gitquery.Worktree, selected, scroll, width, height int, inlineSessions bool, records []sessions.SessionRecord, sessionSelected, sessionScroll int) []string {
	inlineHeight := 0
	if inlineSessions && selected >= 0 && selected < len(worktrees) {
		inlineHeight = visibleInlineWorktreeSessionHeight(records, height-1)
		scroll = scrollForInlineWorktreeSessions(selected, scroll, height, inlineHeight)
	}
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
			line = renderSelectedWorktreeRow(wt, width)
		}
		content = append(content, line)
		if inlineSessions && i == selected {
			content = append(content, renderInlineWorktreeSessions(records, sessionSelected, sessionScroll, width, inlineHeight)...)
		}
	}

	truncateLines(content, width)
	return scrollAndPad(content, scroll, height)
}

func visibleInlineWorktreeSessionHeight(records []sessions.SessionRecord, maxHeight int) int {
	if maxHeight <= 0 {
		return 0
	}
	if len(records) == 0 {
		return 1
	}
	return min(len(records)+1, maxHeight)
}

func scrollForInlineWorktreeSessions(selected, scroll, height, inlineHeight int) int {
	if height <= 0 || inlineHeight <= 0 {
		return scroll
	}
	selectedLine := selected - scroll
	maxSelectedLine := height - inlineHeight - 1
	if maxSelectedLine < 0 {
		maxSelectedLine = 0
	}
	switch {
	case selectedLine < 0:
		scroll = selected
	case selectedLine > maxSelectedLine:
		scroll = selected - maxSelectedLine
	}
	return max(scroll, 0)
}

func renderInlineWorktreeSessions(records []sessions.SessionRecord, selected, scroll, width, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(records) == 0 {
		return []string{"   " + statusStyle.Render("Sessions: none")}
	}
	contentWidth := width - 3
	if contentWidth < 0 {
		contentWidth = 0
	}
	lines := renderSessionPane(records, selected, scroll, contentWidth, height)
	out := make([]string, 0, len(lines)+1)
	out = append(out, "   "+statusStyle.Render("Sessions"))
	for _, line := range lines[1:] {
		out = append(out, "   "+line)
	}
	return out
}

func renderSelectedWorktreeRow(wt gitquery.Worktree, width int) string {
	name := wt.BranchName
	if wt.Detached {
		name = "(detached)"
	}
	line := selectedStyle.Render(" > ") + selectedSegment(branchStyle, name)

	if wt.Locked {
		line += renderSelectedLockedIndicator(wt.LockReason)
		if !wt.Stale {
			if wt.Dirty {
				line += renderSelectedDirtyIndicator(wt.FilesChanged, wt.LinesAdded, wt.LinesDeleted)
			} else {
				line += selectedSegment(cleanStyle, " ✔")
			}
		}
	} else if wt.Stale {
		line += selectedSegment(dirtyRedStyle, " ✗")
		line += selectedStyle.Render(" ")
		line += selectedSegment(dirtyRedStyle, "stale")
	} else if wt.Dirty {
		line += renderSelectedDirtyIndicator(wt.FilesChanged, wt.LinesAdded, wt.LinesDeleted)
	} else {
		line += selectedSegment(cleanStyle, " ✔")
	}

	if wt.IsMain {
		line += selectedStyle.Render(" ")
		line += selectedSegment(rootStyle, "[root]")
	}
	line += selectedStyle.Render(" ")
	line += selectedSegment(commitStyle, wt.Path)
	return renderSelectedRow(line, width)
}

func renderSelectOverlay(p RenderParams) string {
	bodyHeight := p.Height - 1
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	statusBar := renderSelectOverlayStatusBar(p)
	body := selectOverlayBaseBody(p, bodyHeight)
	if p.Width <= 0 || bodyHeight <= 0 {
		return joinBodyAndStatus(body, statusBar)
	}

	panelWidth, panelHeight := selectPanelDimensions(p.SelectPrompt, p.SelectItems, p.SelectWidth, p.SelectHeight, p.Width, bodyHeight)
	if panelWidth <= 0 || panelHeight <= 0 {
		return joinBodyAndStatus(body, statusBar)
	}
	panel := renderSelectPanel(p.SelectPrompt, p.SelectItems, p.SelectSelected, panelWidth, panelHeight)
	x, y := selectPanelPosition(p.Width, bodyHeight, panelWidth, panelHeight, p.SelectPlacement)
	body = compositePanel(body, panel, x, y, p.Width)
	return joinBodyAndStatus(body, statusBar)
}

func renderSelectOverlayStatusBar(p RenderParams) string {
	return renderStatusBarWithState(statusBarParams{
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
}

func selectOverlayBaseBody(p RenderParams, bodyHeight int) []string {
	if bodyHeight <= 0 {
		return nil
	}
	if p.Width <= 0 || p.Width < LeftPaneWidth+2 || p.Height < RepoContentOverhead {
		return blankLines(p.Width, bodyHeight)
	}
	base := p
	base.Overlay = OverlayNone
	lines := strings.Split(renderApplication(base), "\n")
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	body := make([]string, bodyHeight)
	copy(body, lines)
	for i := range body {
		body[i] = fitLineToWidth(body[i], p.Width)
	}
	return body
}

func blankLines(width, height int) []string {
	lines := make([]string, height)
	if width <= 0 {
		return lines
	}
	blank := strings.Repeat(" ", width)
	for i := range lines {
		lines[i] = blank
	}
	return lines
}

func joinBodyAndStatus(body []string, statusBar string) string {
	if len(body) == 0 {
		return statusBar
	}
	return strings.Join(body, "\n") + "\n" + statusBar
}

func selectPanelDimensions(prompt string, items []SelectItem, configuredWidth, configuredHeight, terminalWidth, bodyHeight int) (int, int) {
	if terminalWidth <= 0 || bodyHeight <= 0 {
		return 0, 0
	}
	width := configuredWidth
	if width <= 0 {
		width = autoSelectPanelWidth(prompt, items)
	}
	width = min(width, terminalWidth)
	if width < 1 {
		width = 1
	}
	height := configuredHeight
	if height <= 0 {
		height = 2 + 1 + len(items)
	}
	height = min(height, bodyHeight)
	if height < 1 {
		height = 1
	}
	return width, height
}

func autoSelectPanelWidth(prompt string, items []SelectItem) int {
	if prompt == "" {
		prompt = "Choose"
	}
	widest := lipgloss.Width(prompt)
	for _, item := range items {
		label := selectItemLabel(item)
		if width := lipgloss.Width(label); width > widest {
			widest = width
		}
	}
	return max(12, widest+4)
}

func selectPanelPosition(terminalWidth, bodyHeight, panelWidth, panelHeight int, placement SelectPlacement) (int, int) {
	x := (terminalWidth - panelWidth) / 2
	if x < 0 {
		x = 0
	}
	var y int
	switch placement {
	case SelectPlacementTopCenter:
		y = 0
	case SelectPlacementBottomCenter:
		y = bodyHeight - panelHeight
	default:
		y = (bodyHeight - panelHeight) / 2
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

func renderSelectPanel(prompt string, items []SelectItem, selected, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if prompt == "" {
		prompt = "Choose"
	}
	if selected < 0 || selected >= len(items) {
		selected = 0
	}
	lines := make([]string, 0, height)
	lines = append(lines, selectPanelBorderLine("┌", "─", "┐", width))
	if height > 1 {
		lines = append(lines, selectPanelContentLine(activeModeStyle.Render(prompt), width))
	}
	itemRows := height - 3
	if itemRows < 0 {
		itemRows = 0
	}
	start := selectItemViewportStart(len(items), selected, itemRows)
	for i := 0; i < itemRows; i++ {
		itemIndex := start + i
		if itemIndex >= len(items) {
			lines = append(lines, selectPanelContentLine("", width))
			continue
		}
		label := selectItemLabel(items[itemIndex])
		line := "  " + label
		if itemIndex == selected {
			line = selectedStyle.Render("> " + label)
		}
		lines = append(lines, selectPanelContentLine(line, width))
	}
	if height > 2 {
		lines = append(lines, selectPanelBorderLine("└", "─", "┘", width))
	}
	for len(lines) < height {
		lines = append(lines, selectPanelContentLine("", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = truncateToWidth(lines[i], width)
	}
	return lines
}

func selectPanelBorderLine(left, fill, right string, width int) string {
	if width <= 0 {
		return ""
	}
	line := left + strings.Repeat(fill, max(0, width-2)) + right
	return truncateToWidth(lipgloss.NewStyle().Foreground(clearDarkTheme.activeBorder).Render(line), width)
}

func selectPanelContentLine(content string, width int) string {
	if width <= 0 {
		return ""
	}
	border := lipgloss.NewStyle().Foreground(clearDarkTheme.activeBorder)
	if width == 1 {
		return truncateToWidth(border.Render("│"), width)
	}
	innerWidth := width - 2
	if width >= 4 {
		strippedContent := ansi.Strip(content)
		if strings.HasPrefix(strippedContent, "> ") || strings.HasPrefix(strippedContent, "  ") {
			content = truncateToWidth(content, innerWidth)
			padding := innerWidth - lipgloss.Width(content)
			if padding < 0 {
				padding = 0
			}
			return border.Render("│") + content + strings.Repeat(" ", padding) + border.Render("│")
		}
		contentWidth := width - 4
		content = truncateToWidth(content, contentWidth)
		padding := contentWidth - lipgloss.Width(content)
		if padding < 0 {
			padding = 0
		}
		inner := " " + content + strings.Repeat(" ", padding) + " "
		return border.Render("│") + inner + border.Render("│")
	}
	content = truncateToWidth(content, innerWidth)
	padding := innerWidth - lipgloss.Width(content)
	if padding < 0 {
		padding = 0
	}
	return border.Render("│") + content + strings.Repeat(" ", padding) + border.Render("│")
}

func selectItemViewportStart(total, selected, visibleRows int) int {
	if total <= 0 || visibleRows <= 0 || total <= visibleRows {
		return 0
	}
	if selected < 0 || selected >= total {
		selected = 0
	}
	start := selected - visibleRows + 1
	if start < 0 {
		return 0
	}
	maxStart := total - visibleRows
	if start > maxStart {
		return maxStart
	}
	return start
}

func selectItemLabel(item SelectItem) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Value
}

func compositePanel(base, panel []string, x, y, width int) []string {
	if width <= 0 {
		return base
	}
	out := append([]string(nil), base...)
	for i, panelLine := range panel {
		row := y + i
		if row < 0 || row >= len(out) {
			continue
		}
		panelLine = truncateToWidth(panelLine, width-x)
		panelWidth := lipgloss.Width(panelLine)
		line := fitLineToWidth(out[row], width)
		left := ansi.Cut(line, 0, x)
		right := ansi.Cut(line, x+panelWidth, width)
		out[row] = fitLineToWidth(left+panelLine+right, width)
	}
	return out
}

func fitLineToWidth(line string, width int) string {
	line = truncateToWidth(line, width)
	padding := width - lipgloss.Width(line)
	if padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}

func renderOverlay(p RenderParams) string {
	inputParams := inputRenderParamsFrom(p)
	statusBar := renderStatusBarWithState(statusBarParams{
		Width:                  p.Width,
		Mode:                   p.Mode,
		Overlay:                p.Overlay,
		InputMode:              inputParams.mode,
		WorktreeInputPrompt:    inputParams.prompt,
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
	if p.Overlay == OverlayInput {
		lines := renderInputDialog(inputParams, p.Width, contentHeight)
		return strings.Join(lines, "\n") + "\n" + statusBar
	}
	if p.Overlay == OverlayForm {
		lines := renderFormDialog(p.Form, p.Width, contentHeight)
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

func renderFormDialog(form FormView, width, height int) []string {
	lines := make([]string, height)
	if width <= 0 || height <= 0 {
		return lines
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
	contentWidth := panelWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}

	content := formDialogBodyLines(form, contentWidth)
	if form.Error != "" {
		content = append(content, "")
		for _, line := range wrapPlainText(form.Error, contentWidth) {
			content = append(content, dirtyRedStyle.Render(line))
		}
	}
	for i, line := range content {
		content[i] = " " + fitSessionColumn(line, contentWidth) + " "
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(clearDarkTheme.activeBorder).
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

func formDialogBodyLines(form FormView, width int) []string {
	title := strings.TrimSpace(form.Title)
	if title == "" {
		title = "Form"
	}
	lines := []string{activeModeStyle.Render(truncateToWidth(title, width))}
	for i, field := range form.Fields {
		focused := i == form.FocusIndex
		lines = append(lines, formFieldLines(field, focused, width)...)
	}
	return lines
}

func formFieldLines(field FormField, focused bool, width int) []string {
	prefix := "  "
	if focused {
		prefix = "> "
	}
	label := strings.TrimSpace(field.Label)
	if label == "" {
		label = field.ID
	}
	switch field.Kind {
	case FormCheckbox:
		box := "[ ]"
		if field.Checked {
			box = "[x]"
		}
		return wrapPlainText(prefix+box+" "+label, width)
	case FormChoice:
		var options []string
		for i, option := range field.Options {
			marker := "( )"
			if i == field.SelectedIndex {
				marker = "(o)"
			}
			options = append(options, marker+" "+selectItemLabel(option))
		}
		line := prefix + label
		if len(options) > 0 {
			line += ": " + strings.Join(options, "  ")
		}
		return wrapPlainText(line, width)
	default:
		value := field.Value
		if focused {
			value = insertCursorGlyph(value, field.Cursor)
		}
		if value == "" {
			value = placeholderStyle.Render(field.Placeholder)
			if focused {
				value += activeModeStyle.Render("█")
			}
		}
		return wrapEditableInputLine(prefix+label+": "+value, width)
	}
}

type inputRenderParams struct {
	prompt      string
	placeholder string
	value       string
	errText     string
	mode        InputMode
	cursor      int
}

func inputRenderParamsFrom(p RenderParams) inputRenderParams {
	usesNeutral := p.InputPrompt != "" ||
		p.InputPlaceholder != "" ||
		p.InputValue != "" ||
		p.InputError != "" ||
		p.InputCursor != 0 ||
		p.InputMode != InputSingleLine
	if usesNeutral {
		return inputRenderParams{
			prompt:      p.InputPrompt,
			placeholder: p.InputPlaceholder,
			value:       p.InputValue,
			errText:     p.InputError,
			mode:        p.InputMode,
			cursor:      p.InputCursor,
		}
	}
	cursor := p.InputCursor
	if p.WorktreeInput != "" {
		cursor = len([]rune(p.WorktreeInput))
	}
	return inputRenderParams{
		prompt:      p.WorktreeInputPrompt,
		placeholder: p.WorktreeInputPlaceholder,
		value:       p.WorktreeInput,
		errText:     p.WorktreeInputErr,
		mode:        p.InputMode,
		cursor:      cursor,
	}
}

func renderInputDialog(params inputRenderParams, width, height int) []string {
	lines := make([]string, height)
	if width <= 0 || height <= 0 {
		return lines
	}
	if params.prompt == "" {
		params.prompt = "Create worktree from"
	}
	if params.placeholder == "" {
		params.placeholder = "input"
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

	bodyLines := inputDialogBodyLines(params, contentWidth)
	cursorLine := lineIndexContainingCursor(bodyLines)
	maxInputLines := maxInputDialogLines(height, params.errText, contentWidth)
	bodyLines = compactInputDialogLines(bodyLines, maxInputLines, cursorLine)

	content := make([]string, 0, len(bodyLines)+3)
	content = append(content, bodyLines...)
	if params.errText != "" {
		content = append(content, "")
		for _, line := range wrapPlainText(params.errText, contentWidth) {
			content = append(content, dirtyRedStyle.Render(line))
		}
	}

	for i, line := range content {
		content[i] = " " + fitSessionColumn(line, contentWidth) + " "
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(clearDarkTheme.activeBorder).
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

func inputDialogBodyLines(params inputRenderParams, contentWidth int) []string {
	label := inputDialogLabel(params.prompt)
	if params.value == "" {
		return inputDialogPlaceholderLines(label, params.placeholder, contentWidth)
	}

	value := insertCursorGlyph(params.value, params.cursor)
	logicalLines := strings.Split(value, "\n")
	lines := make([]string, 0, len(logicalLines))
	for i, line := range logicalLines {
		if i == 0 {
			line = label + line
		}
		lines = append(lines, wrapEditableInputLine(line, contentWidth)...)
	}
	if len(lines) == 0 {
		return []string{label + activeModeStyle.Render("█")}
	}
	return lines
}

func inputDialogPlaceholderLines(label, placeholder string, contentWidth int) []string {
	lines := wrapPlainText(label+placeholder, contentWidth)
	styled := styleInputDialogPlaceholderLines(lines, label, placeholder)
	cursor := activeModeStyle.Render("█")
	if len(styled) == 0 {
		return []string{cursor}
	}
	last := len(styled) - 1
	if lipgloss.Width(styled[last]+cursor) <= contentWidth {
		styled[last] += cursor
		return styled
	}
	return append(styled, cursor)
}

func styleInputDialogPlaceholderLines(lines []string, label, placeholder string) []string {
	normalizedLabel := strings.Join(strings.Fields(label), " ")
	normalizedPlaceholder := strings.Join(strings.Fields(placeholder), " ")
	placeholderStart := len([]rune(normalizedLabel))
	if normalizedLabel != "" && normalizedPlaceholder != "" {
		placeholderStart++
	}

	plainRunes := []rune(strings.Join(strings.Fields(label+placeholder), " "))
	styled := make([]string, len(lines))
	offset := 0
	for i, line := range lines {
		for offset < len(plainRunes) && unicode.IsSpace(plainRunes[offset]) {
			offset++
		}
		lineStart := offset
		styled[i] = styleInputDialogPlaceholderLine(line, lineStart, placeholderStart)
		offset += len([]rune(line))
	}
	return styled
}

func styleInputDialogPlaceholderLine(line string, lineStart, placeholderStart int) string {
	runes := []rune(line)
	split := placeholderStart - lineStart
	if split <= 0 {
		return placeholderStyle.Render(line)
	}
	if split >= len(runes) {
		return line
	}
	return string(runes[:split]) + placeholderStyle.Render(string(runes[split:]))
}

func inputDialogLabel(prompt string) string {
	switch strings.TrimSpace(prompt) {
	case BranchPrompt:
		return "Create branch: "
	case PRWorktreePrompt:
		return "Create PR worktree from: "
	default:
		return strings.TrimSpace(prompt) + ": "
	}
}

func wrapEditableInputLine(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{""}
	}
	if s == "" {
		return []string{""}
	}

	lines := make([]string, 0, lipgloss.Width(s)/maxWidth+1)
	for s != "" {
		if lipgloss.Width(s) <= maxWidth {
			lines = append(lines, s)
			break
		}
		head, rest := splitEditableInputAtWidth(s, maxWidth)
		if head == "" {
			runes := []rune(s)
			head = string(runes[:1])
			rest = string(runes[1:])
		}
		lines = append(lines, head)
		s = rest
	}
	return lines
}

func splitEditableInputAtWidth(s string, maxWidth int) (string, string) {
	if maxWidth <= 0 {
		return "", s
	}
	if lipgloss.Width(s) <= maxWidth {
		return s, ""
	}
	runes := []rune(s)
	lastSpaceSplit := -1
	for i := 1; i <= len(runes); i++ {
		if lipgloss.Width(string(runes[:i])) > maxWidth {
			if lastSpaceSplit > 0 {
				return string(runes[:lastSpaceSplit]), string(runes[lastSpaceSplit:])
			}
			return string(runes[:i-1]), string(runes[i-1:])
		}
		if unicode.IsSpace(runes[i-1]) {
			lastSpaceSplit = i
		}
	}
	return s, ""
}

func insertCursorGlyph(value string, cursor int) string {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	out := make([]rune, 0, len(runes)+1)
	out = append(out, runes[:cursor]...)
	out = append(out, '█')
	out = append(out, runes[cursor:]...)
	return string(out)
}

func lineIndexContainingCursor(lines []string) int {
	for i, line := range lines {
		if strings.Contains(line, "█") {
			return i
		}
	}
	return -1
}

func maxInputDialogLines(height int, errText string, contentWidth int) int {
	maxLines := launchInstructionsMaxLines
	available := height - 2
	if errText != "" {
		available -= 1 + len(wrapPlainText(errText, contentWidth))
	}
	if available < 1 {
		return 1
	}
	if available < maxLines {
		maxLines = available
	}
	return maxLines
}

func compactInputDialogLines(lines []string, maxLines, cursorLine int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	if cursorLine < 0 {
		return compactLaunchInstructionLines(lines, maxLines)
	}
	if maxLines == 1 {
		if cursorLine >= 0 && cursorLine < len(lines) {
			return []string{lines[cursorLine]}
		}
		return []string{shortcutOverflowMarker}
	}
	start := cursorLine - maxLines/2
	if start < 0 {
		start = 0
	}
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
		start = end - maxLines
		if start < 0 {
			start = 0
		}
	}
	if start > 0 && cursorLine == start {
		start--
	}
	if end < len(lines) && cursorLine == end-1 {
		end++
	}
	for end-start > maxLines {
		if cursorLine-start > end-1-cursorLine {
			start++
		} else {
			end--
		}
	}
	compact := append([]string(nil), lines[start:end]...)
	if start > 0 {
		compact[0] = shortcutOverflowMarker
	}
	if end < len(lines) {
		compact[len(compact)-1] = shortcutOverflowMarker
	}
	return compact
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
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	return ansi.Truncate(s, maxWidth, "")
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

func renderSelectedDirtyIndicator(filesChanged, linesAdded, linesDeleted int) string {
	s := selectedSegment(dirtyRedStyle, " ●")
	s += selectedStyle.Render(fmt.Sprintf(" %d files ", filesChanged))
	s += selectedSegment(diffAddStyle, fmt.Sprintf("+%d", linesAdded))
	s += selectedStyle.Render("/")
	s += selectedSegment(diffDelStyle, fmt.Sprintf("-%d", linesDeleted))
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

func renderSelectedLockedIndicator(reason string) string {
	s := selectedSegment(lockedStyle, " 🔒")
	s += selectedStyle.Render(" ")
	s += selectedSegment(lockedStyle, "locked")
	if reason != "" {
		s += selectedStyle.Render(" ")
		s += selectedSegment(lockedStyle, truncateReason(reason, MaxLockReasonWidth))
	}
	return s
}

func selectedSegment(style lipgloss.Style, text string) string {
	return style.Background(clearDarkTheme.palette.selectionBg).Bold(true).Render(text)
}

func renderSelectedRow(line string, width int) string {
	line = truncateToWidth(line, width)
	if padding := width - lipgloss.Width(line); padding > 0 {
		line += selectedStyle.Render(strings.Repeat(" ", padding))
	}
	return line
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
