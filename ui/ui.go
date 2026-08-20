package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
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

// NoteKind classifies the severity of a modal feedback note. It mirrors
// modal.NoteKind, which ui cannot import; the model converts between them.
type NoteKind int

const (
	NoteNeutral NoteKind = iota
	NoteSuccess
	NoteWarning
	NoteError
)

// EditorParams carries the prompt-template editor's presentation state. Its
// zero value keeps every other input modal on the generic renderer.
type EditorParams struct {
	Enabled      bool
	Title        string
	Identity     string
	Note         string
	NoteKind     NoteKind
	Dirty        bool
	EmptyWarning bool
}

// PromptPickerWidth and PromptPickerNoteRows are the prompt-template picker's
// fixed geometry. They are exported because the model owns the target list and
// therefore computes the panel height; ui cannot see len(promptTemplateTargets).
const (
	PromptPickerWidth    = 42
	PromptPickerNoteRows = 1
)

const BranchPrompt = "New branch"
const FlowTitlePrompt = "New flow title"
const FlowInstructionsPrompt = "New flow instructions"
const FlowBaseRefPrompt = "New flow base ref"
const LaunchInstructionsPrompt = "Launch instructions"
const WorktreeMovePrompt = "Move worktree to"
const PRWorktreePrompt = "PR worktree"
const PromptTemplateSelectPrompt = "Prompt templates"
const WorktreeInputPlaceholder = "branch, tag, or new branch name"
const FlowTitleInputPlaceholder = "flow title"
const FlowInstructionsInputPlaceholder = "task instructions"
const FlowBaseRefInputPlaceholder = "optional base ref"
const WorktreeMoveInputPlaceholder = "new path or sibling name"
const BranchInputPlaceholder = "branch name"
const PRWorktreeInputPlaceholder = "PR number or URL"
const AgentInputPlaceholder = "codex, claude, or cursor-agent"
const FlowCloseReasonInputPlaceholder = "why this flow is closed"

// FlowCloseAction is what pressing C on the selected Flow will do.
type FlowCloseAction int

const (
	FlowCloseActionNone FlowCloseAction = iota
	FlowCloseActionClose
	FlowCloseActionReopen
)

type SelectItem struct {
	Label string
	Value string
}

type FormFieldKind int

const (
	FormText FormFieldKind = iota
	FormMultilineText
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

// PRBabysitterRow is the display-only projection for one authoritative Flow.
type PRBabysitterRow struct {
	Flow         flowstore.FlowRecord
	Repo         string
	Title        string
	BeadID       string
	Mergeability string
	Checks       string
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
	ModeActiveFlows
	ModePRBabysitter
	ModeBeadsReady
	ModeBeadsBlocked
	ModeBeadsOpen
	ModeBeadsInProgress
	ModeBeadsClosed
)

// IsGitMode reports whether mode is one of the five git-oriented subviews
// grouped under the top-level Git view (worktrees, branches, stashes,
// history, reflog).
func IsGitMode(mode Mode) bool {
	return mode >= ModeWorktrees && mode <= ModeReflog
}

// IsBeadsMode reports whether mode is one of the five Beads subviews grouped
// under the top-level Beads view.
func IsBeadsMode(mode Mode) bool {
	return mode >= ModeBeadsReady && mode <= ModeBeadsClosed
}

// Pane identifies the logical focus owner. The renderer still draws a single
// content pane; PaneTop and PaneBottom preserve which stored mode owns it for
// the later stacked-pane layout.
type Pane int

const (
	PaneRepos Pane = iota
	PaneTop
	PaneBottom
)

// PaneForMode reports which stored content pane owns mode. Takeovers are not
// stored pane modes, and repositories own no mode, so neither has a mapping.
func PaneForMode(mode Mode) (Pane, bool) {
	if IsGitMode(mode) || IsBeadsMode(mode) {
		return PaneTop, true
	}
	switch mode {
	case ModeSessions, ModePlans, ModeFlows:
		return PaneBottom, true
	default:
		return PaneRepos, false
	}
}

const LeftPaneWidth = 30

// MinStackedPaneListContentHeight is the minimum useful list area retained in
// each stored pane before the content column degrades to one full-height pane.
const MinStackedPaneListContentHeight = 6

const (
	stackedPaneBorderRows       = 2
	stackedTopPaneHeaderRows    = 2
	stackedBottomPaneHeaderRows = 1
	// MinStackedPaneOuterRows is the smallest shared outer-row budget that
	// keeps both stored panes and their minimum useful list content visible.
	MinStackedPaneOuterRows = 2*MinStackedPaneListContentHeight +
		2*stackedPaneBorderRows + stackedTopPaneHeaderRows + stackedBottomPaneHeaderRows
)

// StackedPaneLayout is a pure allocation of the content column's outer rows.
// In split mode the top pane receives the odd row. In degraded mode exactly
// one allocation is non-zero.
type StackedPaneLayout struct {
	Split      bool
	TopRows    int
	BottomRows int
}

// StackedContentLayout allocates the shared outer row budget after terminal
// dock subtraction.
func StackedContentLayout(sharedRows int, focused, remembered Pane) StackedPaneLayout {
	if sharedRows < 0 {
		sharedRows = 0
	}
	topRows := (sharedRows + 1) / 2
	bottomRows := sharedRows / 2
	topListRows := topRows - stackedPaneBorderRows - stackedTopPaneHeaderRows
	bottomListRows := bottomRows - stackedPaneBorderRows - stackedBottomPaneHeaderRows
	if topListRows >= MinStackedPaneListContentHeight && bottomListRows >= MinStackedPaneListContentHeight {
		return StackedPaneLayout{Split: true, TopRows: topRows, BottomRows: bottomRows}
	}
	visible := focused
	if visible != PaneTop && visible != PaneBottom {
		visible = remembered
	}
	if visible == PaneBottom {
		return StackedPaneLayout{BottomRows: sharedRows}
	}
	return StackedPaneLayout{TopRows: sharedRows}
}

// CollapsedRepoPaneWidth is the total width of the collapsed repos strip,
// including its left and right borders.
const CollapsedRepoPaneWidth = 4

// ShortcutPaneWidth is the total width reserved for the right-hand keyboard
// shortcut rail, including its left and right borders.
const ShortcutPaneWidth = 28

const (
	shortcutKeyColumnWidth = 6
	shortcutOverflowMarker = "..."
	paneShortcutKey        = "tab"
	paneBackShortcutKey    = "bksp"
)

const (
	launchInstructionsMaxWidth = 72
	launchInstructionsMinWidth = 32
	launchInstructionsMaxLines = 6
	flowCreateFormMaxWidth     = 56
	flowCreateFormMaxTextLines = 4
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

// GitContentOverhead is the number of rows consumed by chrome around the git
// subview lists: the base right-pane chrome plus the grouped Git header's
// extra subview row.
const GitContentOverhead = BranchContentOverhead + 1

// BeadsContentOverhead is the number of rows consumed by chrome around the
// Beads subview lists, including the grouped Beads header's second row.
const BeadsContentOverhead = BranchContentOverhead + 1

// WorktreeContentOverhead is the number of rows consumed by chrome around the
// worktree list. Worktrees is a git subview, so it renders under the two-row
// grouped Git header.
const WorktreeContentOverhead = GitContentOverhead

// StashContentOverhead is the number of rows consumed by chrome around the
// stash list. Stashes is a git subview, so it renders under the two-row
// grouped Git header.
const StashContentOverhead = GitContentOverhead

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

// TerminalChipRows is the list-space reserved by a collapsed dock chip.
const TerminalChipRows = 1

type EmbeddedTerminalDockState int

const (
	EmbeddedTerminalDockCollapsed EmbeddedTerminalDockState = iota
	EmbeddedTerminalDockExpanded
)

// EmbeddedTerminalDockAllocation is the mode-independent vertical allocation
// for the terminal dock and the shared content surface.
type EmbeddedTerminalDockAllocation struct {
	State           EmbeddedTerminalDockState
	DockRows        int
	RenderPTYRows   int
	BackendPTYRows  int
	SharedOuterRows int
}

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
	Repos                          []scanner.Repo
	ActiveTerminalRepoPaths        map[string]bool
	Selected                       int
	Width                          int
	Height                         int
	Mode                           Mode
	TopMode                        Mode
	BottomMode                     Mode
	ContentPane                    Pane
	ActiveFlows                    bool
	PRBabysitter                   bool
	Branches                       []gitquery.BranchRow
	Stashes                        []gitquery.Stash
	BranchSelected                 int
	StashSelected                  int
	Overlay                        OverlayState
	OverlayDiff                    string
	OverlayScroll                  int
	ConfirmPrompt                  string
	ConfirmForce                   bool
	InputPrompt                    string
	InputPlaceholder               string
	InputValue                     string
	InputError                     string
	InputMode                      InputMode
	InputHeight                    int
	InputCursor                    int
	Editor                         EditorParams
	WorktreeInputPrompt            string
	WorktreeInputPlaceholder       string
	WorktreeInput                  string
	WorktreeInputErr               string
	SelectPrompt                   string
	SelectItems                    []SelectItem
	SelectSelected                 int
	SelectWidth                    int
	SelectHeight                   int
	SelectNote                     string
	SelectNoteKind                 NoteKind
	SelectPlacement                SelectPlacement
	Form                           FormView
	BranchScroll                   int
	RepoScroll                     int
	StashScroll                    int
	ActivePane                     Pane
	RepoPaneCollapsed              bool
	Destructive                    bool
	Worktrees                      []gitquery.Worktree
	WorktreeSelected               int
	WorktreeScroll                 int
	WorktreeSessions               []sessions.SessionRecord
	WorktreeSessionSelected        int
	WorktreeSessionScroll          int
	InlineWorktreeSessions         bool
	Commits                        []gitquery.Commit
	CommitSelected                 int
	CommitScroll                   int
	Reflogs                        []gitquery.ReflogEntry
	ReflogSelected                 int
	ReflogScroll                   int
	Sessions                       []sessions.SessionRecord
	SessionSelected                int
	SessionScroll                  int
	EmbeddedTerminals              []EmbeddedTerminalTab
	EmbeddedTerminalLines          []string
	EmbeddedTerminalPrefix         bool
	EmbeddedTerminalVisible        bool
	EmbeddedTerminalFocused        bool
	Plans                          []planstore.PlanRecord
	PlanSelected                   int
	PlanScroll                     int
	Flows                          []flowstore.FlowRecord
	PRBabysitterRows               []PRBabysitterRow
	FlowSelected                   int
	FlowScroll                     int
	FlowDegradationWarning         string
	ActiveFlowDegradationWarning   string
	PRBabysitterDegradationWarning string
	TopDegradationWarning          string
	BottomDegradationWarning       string
	BeadsOpen                      []beadsquery.Bead
	BeadsOpenSelected              int
	BeadsOpenScroll                int
	BeadsOpenAvailable             bool
	BeadsOpenPending               bool
	ReadyBeadFlowCreateAvailable   bool
	ReadyBeadFlowStartAvailable    bool
	ReadyBeadFlowKeysOwned         bool
	BeadSliceEpicAvailable         bool
	EpicAutoOnAvailable            bool
	EpicAutoOffAvailable           bool
	EpicAutoKeyOwned               bool
	BeadsError                     string
	BeadsQuery                     string
	BeadsSourceCount               int
	BeadsClosedTotal               int
	BeadExpansion                  BeadExpansion
	FlowTerminalActivity           []FlowTerminalActivity
	ExpandedPlanID                 string
	ExpandedFlowID                 string
	SelectedPlanPhaseID            string
	SelectedFlowPhaseID            string
	FlowHeadless                   bool
	FlowAutoModeSelected           bool
	FlowIssueTargetSelected        bool
	FlowPRTargetSelected           bool
	FlowAgentLabel                 string
	FlowModel                      string
	FlowReasoningEffort            string
	FlowNextLaunchReady            bool
	FlowWorktreeAgentReady         bool
	FlowRepairReady                bool
	FlowManualMergeReadySelected   bool
	FlowAutofixReadySelected       bool
	FlowCloseActionSelected        FlowCloseAction
	FlowPhaseResetReadySelected    bool
	FlowPhaseReleaseSelected       bool
	FlowPhaseResumableSelected     bool
	OverlayText                    string
	TransientError                 string
	TransientErrorFadeStep         int
	SearchActive                   bool
	RepoSearch                     string
	ItemSearch                     string
	ItemSourceCount                int
	TopItemSearch                  string
	BottomItemSearch               string
	TopItemSourceCount             int
	BottomItemSourceCount          int
	RepoEmptyMessage               string
	RightEmptyMessage              string
	TopListError                   string
	BottomListError                string
	ActiveFlowsListError           string
	PRBabysitterListError          string
	FetchAvailable                 bool
	FetchVisibleAvailable          bool
	RepoCreateAvailable            bool
	PullAvailable                  bool
	WorktreeMoveAvailable          bool
	WorktreeSessionsOpen           bool
	AgentAvailable                 bool
	NewAgentAvailable              bool
	// TmuxAttachAvailable reports that [launch].backend is "tmux" and tmux is
	// installed, so the T attach affordance has something to offer.
	TmuxAttachAvailable bool
}

// ResolveEmbeddedTerminalDock reserves enough shared outer rows for both
// stored panes before allocating an expanded terminal. If the remaining dock
// cannot draw a header and one PTY row inside its frame, it resolves to the
// existing collapsed chip while retaining a backend-safe PTY height.
func ResolveEmbeddedTerminalDock(height int, expansionRequested bool) EmbeddedTerminalDockAllocation {
	viewportRows := max(0, height)
	statusRows := min(1, viewportRows)
	availableDockRows := max(0, viewportRows-statusRows-MinStackedPaneOuterRows)
	_, preferredDockRows := EmbeddedTerminalDockHeights(max(0, viewportRows-BranchContentOverhead), EmbeddedTerminalDockExpanded)
	candidateDockRows := min(preferredDockRows, availableDockRows)
	minimumExpandedRows := EmbeddedTerminalFrameRows + EmbeddedTerminalHeaderRows + 1

	allocation := EmbeddedTerminalDockAllocation{
		State:          EmbeddedTerminalDockCollapsed,
		BackendPTYRows: 1,
	}
	if expansionRequested && candidateDockRows >= minimumExpandedRows {
		allocation.State = EmbeddedTerminalDockExpanded
		allocation.DockRows = candidateDockRows
		allocation.RenderPTYRows = candidateDockRows - EmbeddedTerminalFrameRows - EmbeddedTerminalHeaderRows
		allocation.BackendPTYRows = max(1, allocation.RenderPTYRows)
	} else if viewportRows > GitContentOverhead {
		allocation.DockRows = TerminalChipRows
	}
	allocation.SharedOuterRows = max(0, viewportRows-statusRows-allocation.DockRows)
	return allocation
}

// EmbeddedTerminalDockRows is the number of full-width rows the top-level
// terminal dock consumes between the pane row and the status bar.
func EmbeddedTerminalDockRows(height int, state EmbeddedTerminalDockState) int {
	return ResolveEmbeddedTerminalDock(height, state == EmbeddedTerminalDockExpanded).DockRows
}

func EmbeddedTerminalDockHeights(height int, state EmbeddedTerminalDockState) (listHeight, terminalHeight int) {
	if height <= 0 {
		return 0, 0
	}
	if state != EmbeddedTerminalDockExpanded {
		return height, 0
	}
	terminalHeight = height * 3 / 8
	if terminalHeight < 1 {
		terminalHeight = 1
	}
	listHeight = height - terminalHeight
	if listHeight < 1 {
		listHeight = 1
		terminalHeight = height - listHeight
	}
	return listHeight, terminalHeight
}

// RightContentWidth returns the render width inside the right pane after the
// left pane, right-pane border, and optional shortcut pane are accounted for.
func RightContentWidth(width, height int, activeStatusQuery, repoPaneCollapsed bool) int {
	repoPaneWidth := LeftPaneWidth
	if repoPaneCollapsed {
		repoPaneWidth = CollapsedRepoPaneWidth
	}
	rightContentWidth := width - repoPaneWidth - 2
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
	activeFlows := p.ActiveFlows || p.Mode == ModeActiveFlows
	prBabysitter := p.PRBabysitter || p.Mode == ModePRBabysitter
	takeover := activeFlows || prBabysitter
	embeddedTerminalActive := len(p.EmbeddedTerminals) > 0
	terminalDockRequested := embeddedTerminalActive && p.EmbeddedTerminalVisible
	dockAllocation := ResolveEmbeddedTerminalDock(p.Height, terminalDockRequested)
	terminalEffectivelyExpanded := embeddedTerminalActive && dockAllocation.State == EmbeddedTerminalDockExpanded
	terminalFocused := terminalEffectivelyExpanded && p.EmbeddedTerminalFocused
	flowSurfaceActive := p.Mode == ModeFlows || takeover
	terminalShortcutsActive := terminalFocused
	sessionSelected := p.Mode == ModeSessions && p.SessionSelected >= 0 && p.SessionSelected < len(p.Sessions)
	planSelected := p.Mode == ModePlans && p.PlanSelected >= 0 && p.PlanSelected < len(p.Plans)
	flowSelected := flowSurfaceActive && p.FlowSelected >= 0 && p.FlowSelected < len(p.Flows)
	flowPlanLinked := false
	flowWorktreePathSelected := false
	flowAutoModeSelected := false
	flowIssueTargetSelected := false
	flowPRTargetSelected := false
	if flowSelected {
		flowPlanLinked = strings.TrimSpace(p.Flows[p.FlowSelected].PlanID) != ""
		flowWorktreePathSelected = strings.TrimSpace(p.Flows[p.FlowSelected].WorktreePath) != ""
		flowAutoModeSelected = p.FlowAutoModeSelected
		flowIssueTargetSelected = p.FlowIssueTargetSelected
		flowPRTargetSelected = p.FlowPRTargetSelected
	}
	worktreeSessionSelected := p.Mode == ModeWorktrees && p.InlineWorktreeSessions && p.WorktreeSessionSelected >= 0 && p.WorktreeSessionSelected < len(p.WorktreeSessions)
	selectedPlanPhaseID := scopedSelectedPlanPhaseID(p, planSelected)
	selectedFlowPhaseID := scopedSelectedFlowPhaseID(p, flowSelected)
	flowDeletableSelected := flowSelected && selectedFlowPhaseID == ""
	if terminalFocused {
		flowSelected = false
		selectedFlowPhaseID = ""
		flowDeletableSelected = false
		flowWorktreePathSelected = false
		flowAutoModeSelected = false
		flowIssueTargetSelected = false
		flowPRTargetSelected = false
	}
	planPhaseSelected := selectedPlanPhaseID != ""
	flowPhaseSelected := selectedFlowPhaseID != ""
	if flowPhaseSelected {
		flowIssueTargetSelected = false
		flowPRTargetSelected = false
	}
	status := statusBarParams{
		Width:                        p.Width,
		Mode:                         p.Mode,
		ActiveFlows:                  activeFlows,
		PRBabysitter:                 prBabysitter,
		Overlay:                      p.Overlay,
		InputMode:                    inputRenderParamsFrom(p).mode,
		PromptEditor:                 p.Editor.Enabled,
		FormHasMultiline:             formHasMultilineField(p.Form),
		WorktreeInputPrompt:          p.WorktreeInputPrompt,
		ActivePane:                   p.ActivePane,
		RepoPaneCollapsed:            p.RepoPaneCollapsed,
		Destructive:                  p.Destructive,
		RepoSelected:                 repoPath != "",
		WorktreeSelected:             worktreeSelected,
		StaleSelected:                staleSelected,
		DirtySelected:                dirtySelected,
		LockedSelected:               lockedSelected,
		WorktreeDeletableSelected:    worktreeDeletableSelected,
		WorktreeOpenableSelected:     worktreeOpenableSelected,
		WorktreeMoveSelected:         worktreeMoveSelected,
		WorktreeSessionsOpen:         p.WorktreeSessionsOpen,
		WorktreeSessionSelected:      worktreeSessionSelected,
		BranchDirtySelected:          branchDirtySelected,
		BranchDeletableSelected:      branchDeletableSelected,
		BranchOpenableSelected:       branchOpenableSelected,
		StashSelected:                stashSelected,
		CommitSelected:               commitSelected,
		ReflogSelected:               reflogSelected,
		SessionSelected:              sessionSelected,
		EmbeddedTerminalActive:       terminalShortcutsActive,
		EmbeddedTerminalExists:       embeddedTerminalActive,
		EmbeddedTerminalPrefix:       p.EmbeddedTerminalPrefix,
		EmbeddedTerminalVisible:      p.EmbeddedTerminalVisible,
		PlanSelected:                 planSelected,
		PlanPhaseSelected:            planPhaseSelected,
		FlowSelected:                 flowSelected,
		FlowPhaseSelected:            flowPhaseSelected,
		FlowDeletableSelected:        flowDeletableSelected,
		FlowWorktreePathSelected:     flowWorktreePathSelected,
		FlowPlanLinked:               flowPlanLinked,
		FlowIssueTargetSelected:      flowIssueTargetSelected,
		FlowPRTargetSelected:         flowPRTargetSelected,
		FlowHeadless:                 p.FlowHeadless,
		FlowAutoModeSelected:         flowAutoModeSelected,
		FlowAgentLabel:               p.FlowAgentLabel,
		FlowModel:                    p.FlowModel,
		FlowReasoningEffort:          p.FlowReasoningEffort,
		FlowNextLaunchReady:          p.FlowNextLaunchReady,
		FlowWorktreeAgentReady:       p.FlowWorktreeAgentReady,
		FlowRepairReady:              p.FlowRepairReady,
		FlowManualMergeReadySelected: p.FlowManualMergeReadySelected && flowSelected && !flowPhaseSelected,
		FlowAutofixReadySelected:     p.FlowAutofixReadySelected && flowSelected && !flowPhaseSelected,
		FlowCloseActionSelected:      p.FlowCloseActionSelected,
		FlowPhaseResetReadySelected:  p.FlowPhaseResetReadySelected,
		FlowPhaseReleaseSelected:     p.FlowPhaseReleaseSelected,
		FlowPhaseResumableSelected:   p.FlowPhaseResumableSelected,
		TmuxAttachAvailable:          p.TmuxAttachAvailable,
		TransientError:               p.TransientError,
		TransientErrorFadeStep:       p.TransientErrorFadeStep,
		SearchActive:                 p.SearchActive,
		RepoSearch:                   p.RepoSearch,
		ItemSearch:                   p.ItemSearch,
		FetchAvailable:               p.FetchAvailable,
		FetchVisibleAvailable:        p.FetchVisibleAvailable,
		RepoCreateAvailable:          p.RepoCreateAvailable,
		PullAvailable:                p.PullAvailable,
		AgentAvailable:               p.AgentAvailable,
		NewAgent:                     p.NewAgentAvailable,
		ReadyBeadFlowCreateAvailable: p.ReadyBeadFlowCreateAvailable,
		ReadyBeadFlowStartAvailable:  p.ReadyBeadFlowStartAvailable,
		ReadyBeadFlowKeysOwned:       p.ReadyBeadFlowKeysOwned,
		BeadSliceEpicAvailable:       p.BeadSliceEpicAvailable,
		EpicAutoOnAvailable:          p.EpicAutoOnAvailable,
		EpicAutoOffAvailable:         p.EpicAutoOffAvailable,
		EpicAutoKeyOwned:             p.EpicAutoKeyOwned,
	}
	dockState := dockAllocation.State
	dockRows := dockAllocation.DockRows
	paneRowHeight := max(0, dockAllocation.SharedOuterRows-stackedPaneBorderRows)
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
	} else if p.ActivePane != PaneRepos && !terminalFocused {
		rightBorderColor = activeBorderColor
	}
	if p.ActivePane == PaneRepos && !p.RepoPaneCollapsed {
		leftBorderColor = activeBorderColor
	}

	leftContentWidth := LeftPaneWidth - 2 // left + right border
	leftLines := renderRepoList(p.Repos, p.Selected, p.RepoScroll, leftContentWidth, paneRowHeight, p.RepoEmptyMessage, p.ActiveTerminalRepoPaths)
	if p.RepoPaneCollapsed {
		leftContentWidth = CollapsedRepoPaneWidth - 2
		leftLines = renderCollapsedRepoPane(p.Repos, p.Selected, paneRowHeight, !terminalShortcutsActive)
	}
	leftContent := strings.Join(leftLines, "\n")
	leftPane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(leftBorderColor).
		Width(leftContentWidth).
		Height(paneRowHeight).
		Render(leftContent)

	rightContentWidth := RightContentWidth(p.Width, p.Height, activeStatusQuery, p.RepoPaneCollapsed)

	modeHeader := renderModeHeaderWithBeads(
		p.Mode, rightContentWidth, p.BeadsSourceCount, p.BeadsClosedTotal,
		p.BeadsOpenAvailable, p.BeadsOpenPending,
	)
	rightContentHeight := p.Height - BranchContentOverhead
	if IsGitMode(p.Mode) {
		rightContentHeight = p.Height - GitContentOverhead
	} else if IsBeadsMode(p.Mode) {
		rightContentHeight = p.Height - BeadsContentOverhead
	}
	rightContentHeight -= dockRows
	if rightContentHeight < 0 {
		rightContentHeight = 0
	}

	// Hide cursor in right pane when left pane is active
	branchSel := p.BranchSelected
	stashSel := p.StashSelected
	commitSel := p.CommitSelected
	worktreeSel := p.WorktreeSelected
	reflogSel := p.ReflogSelected
	sessionSel := p.SessionSelected
	planSel := p.PlanSelected
	flowSel := p.FlowSelected
	beadSel := p.BeadsOpenSelected
	if p.ActivePane == PaneRepos {
		branchSel = -1
		stashSel = -1
		commitSel = -1
		worktreeSel = -1
		reflogSel = -1
		sessionSel = -1
		planSel = -1
		flowSel = -1
		beadSel = -1
		selectedPlanPhaseID = ""
		selectedFlowPhaseID = ""
	}
	if terminalFocused {
		flowSel = -1
		selectedFlowPhaseID = ""
	}

	repoDisplayNames := repoDisplayNamesByPath(p.Repos)
	listHeight := rightContentHeight
	var rightLines []string
	switch {
	case prBabysitter && len(p.PRBabysitterRows) > 0:
		rightLines = renderPRBabysitterPane(p.PRBabysitterRows, flowSel, p.FlowScroll, rightContentWidth, listHeight, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity)
	case flowSurfaceActive && len(p.Flows) > 0:
		rightLines = renderFlowPane(p.Flows, flowSel, p.FlowScroll, rightContentWidth, listHeight, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity, activeFlows, repoDisplayNames)
	case flowSurfaceActive:
		emptyMessage := p.RightEmptyMessage
		if emptyMessage == "" {
			emptyMessage = "No flows"
		}
		rightLines = renderPlaceholderPane(rightContentWidth, listHeight, emptyMessage)
	case p.Mode == ModeWorktrees && len(p.Worktrees) > 0:
		rightLines = renderWorktreePaneWithSessions(p.Worktrees, worktreeSel, p.WorktreeScroll, rightContentWidth, listHeight, p.InlineWorktreeSessions, p.WorktreeSessions, p.WorktreeSessionSelected, p.WorktreeSessionScroll)
	case p.Mode == ModeBranches && len(p.Branches) > 0:
		rightLines = renderBranchPaneSelected(p.Branches, branchSel, p.BranchScroll, rightContentWidth, listHeight, repoPath)
	case p.Mode == ModeStashes && len(p.Stashes) > 0:
		rightLines = renderStashPane(p.Stashes, stashSel, p.StashScroll, rightContentWidth, listHeight)
	case p.Mode == ModeHistory && len(p.Commits) > 0:
		rightLines = renderCommitPane(p.Commits, commitSel, p.CommitScroll, rightContentWidth, listHeight)
	case p.Mode == ModeReflog && len(p.Reflogs) > 0:
		rightLines = renderReflogPane(p.Reflogs, reflogSel, p.ReflogScroll, rightContentWidth, listHeight)
	case p.Mode == ModeSessions && len(p.Sessions) > 0:
		rightLines = renderSessionPane(p.Sessions, sessionSel, p.SessionScroll, rightContentWidth, listHeight)
	case p.Mode == ModePlans && len(p.Plans) > 0:
		rightLines = renderPlanPane(p.Plans, planSel, p.PlanScroll, rightContentWidth, listHeight, p.ExpandedPlanID, selectedPlanPhaseID)
	case IsBeadsMode(p.Mode) && p.BeadsOpenPending:
		message := p.RightEmptyMessage
		if message == "" {
			message = "loading " + beadsModeLabel(p.Mode) + " beads"
		}
		rightLines = renderPlaceholderPane(rightContentWidth, listHeight, message)
	case IsBeadsMode(p.Mode) && p.BeadsError != "":
		message := "Could not load " + beadsModeLabel(p.Mode) + " beads: " + terminalSafeSingleLine(p.BeadsError)
		rightLines = renderPlaceholderPane(rightContentWidth, listHeight, message)
	case IsBeadsMode(p.Mode) && len(p.BeadsOpen) > 0:
		rightLines = renderBeadsPane(p.BeadsOpen, beadSel, p.BeadsOpenScroll, rightContentWidth, listHeight, p.BeadExpansion)
	case IsBeadsMode(p.Mode):
		message := p.RightEmptyMessage
		if message == "" {
			switch {
			case p.BeadsOpenPending:
				message = "loading " + beadsModeLabel(p.Mode) + " beads"
			case p.BeadsOpenAvailable:
				message = "no " + beadsModeLabel(p.Mode) + " beads"
			default:
				message = "beads not configured"
			}
		}
		rightLines = renderPlaceholderPane(rightContentWidth, listHeight, message)
	default:
		rightLines = renderPlaceholderPane(rightContentWidth, listHeight, p.RightEmptyMessage)
	}

	rightContentLines := append(strings.Split(modeHeader, "\n"), rightLines...)
	if len(rightContentLines) > paneRowHeight {
		// lipgloss Height is a minimum, not a clip: at degenerate viewport
		// heights the fixed mode header would otherwise grow the pane row
		// past the viewport.
		rightContentLines = rightContentLines[:paneRowHeight]
	}
	rightContent := strings.Join(rightContentLines, "\n")
	rightPane := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(rightBorderColor).
		Width(rightContentWidth).
		Height(paneRowHeight).
		Render(rightContent)
	if p.TopMode != 0 && p.BottomMode != 0 {
		sharedOuterRows := paneRowHeight + stackedPaneBorderRows
		if takeover {
			focused := p.ActivePane != PaneRepos && !terminalFocused
			takeoverMode := ModeActiveFlows
			if prBabysitter {
				takeoverMode = ModePRBabysitter
			}
			rightPane = renderStackedModePane(p, takeoverMode, rightContentWidth, sharedOuterRows, focused, true, repoPath)
		} else {
			layout := StackedContentLayout(sharedOuterRows, p.ActivePane, p.ContentPane)
			stacked := make([]string, 0, 2)
			if layout.TopRows > 0 {
				stacked = append(stacked, renderStackedModePane(p, p.TopMode, rightContentWidth, layout.TopRows, p.ActivePane == PaneTop && !terminalFocused, false, repoPath))
			}
			if layout.BottomRows > 0 {
				stacked = append(stacked, renderStackedModePane(p, p.BottomMode, rightContentWidth, layout.BottomRows, p.ActivePane == PaneBottom && !terminalFocused, false, repoPath))
			}
			rightPane = lipgloss.JoinVertical(lipgloss.Left, stacked...)
		}
	}

	panes := []string{leftPane, rightPane}
	if showShortcutPane {
		shortcutContentWidth := ShortcutPaneWidth - 2
		shortcutPane := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(inactiveBorderColor).
			Width(shortcutContentWidth).
			Height(paneRowHeight).
			Render(renderShortcutPane(status, shortcutContentWidth, paneRowHeight))
		panes = append(panes, shortcutPane)
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	if dockLines := renderEmbeddedTerminalDockPane(p.EmbeddedTerminals, p.EmbeddedTerminalLines, p.EmbeddedTerminalPrefix, terminalFocused, terminalDockRequested, p.Width, dockRows, dockState); len(dockLines) > 0 {
		content += "\n" + strings.Join(dockLines, "\n")
	}

	return content + "\n" + statusBar
}

func renderStackedModePane(p RenderParams, mode Mode, width, outerRows int, focused, takeover bool, repoPath string) string {
	if outerRows <= 0 {
		return ""
	}
	header := renderStackedModeHeader(p, mode, width, takeover)
	headerLines := strings.Split(header, "\n")
	contentRows := outerRows - stackedPaneBorderRows
	if contentRows < 0 {
		contentRows = 0
	}
	listRows := contentRows - len(headerLines)
	if listRows < 0 {
		listRows = 0
	}

	branchSel, stashSel := p.BranchSelected, p.StashSelected
	commitSel, worktreeSel, reflogSel := p.CommitSelected, p.WorktreeSelected, p.ReflogSelected
	sessionSel, planSel, flowSel, beadSel := p.SessionSelected, p.PlanSelected, p.FlowSelected, p.BeadsOpenSelected
	worktreeSessionSel := p.WorktreeSessionSelected
	selectedPlanPhaseID, selectedFlowPhaseID := p.SelectedPlanPhaseID, p.SelectedFlowPhaseID
	if !focused {
		branchSel, stashSel, commitSel, worktreeSel, reflogSel = -1, -1, -1, -1, -1
		sessionSel, planSel, flowSel, beadSel, worktreeSessionSel = -1, -1, -1, -1, -1
		selectedPlanPhaseID, selectedFlowPhaseID = "", ""
	}

	repoDisplayNames := repoDisplayNamesByPath(p.Repos)
	var body []string
	hasRows := stackedModePaneHasRows(p, mode, takeover)
	_, sourceCount := paneItemFilterState(p, mode)
	showCachedWarning := ShowsCachedListWarning(paneListError(p, mode), hasRows, sourceCount)
	degradationWarning := paneDegradationWarning(p, mode)
	bodyRows := listRows
	if showCachedWarning && bodyRows > 0 {
		bodyRows--
	}
	if degradationWarning != "" && bodyRows > 0 {
		bodyRows--
	}
	switch {
	case mode == ModePRBabysitter && len(p.PRBabysitterRows) > 0:
		body = renderPRBabysitterPane(p.PRBabysitterRows, flowSel, p.FlowScroll, width, bodyRows, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity)
	case (mode == ModeFlows || mode == ModeActiveFlows) && len(p.Flows) > 0:
		body = renderFlowPane(p.Flows, flowSel, p.FlowScroll, width, bodyRows, p.ExpandedFlowID, selectedFlowPhaseID, p.FlowTerminalActivity, takeover, repoDisplayNames)
	case mode == ModeFlows || takeover:
		message := paneEmptyMessage(p, mode, repoPath)
		if takeover && message == "" {
			if mode == ModePRBabysitter {
				message = "No PRs awaiting merge"
			} else {
				message = "No active flows"
			}
		}
		body = renderPlaceholderPane(width, bodyRows, message)
	case mode == ModeWorktrees && len(p.Worktrees) > 0:
		body = renderWorktreePaneWithSessions(p.Worktrees, worktreeSel, p.WorktreeScroll, width, bodyRows, p.InlineWorktreeSessions, p.WorktreeSessions, worktreeSessionSel, p.WorktreeSessionScroll)
	case mode == ModeBranches && len(p.Branches) > 0:
		body = renderBranchPaneSelected(p.Branches, branchSel, p.BranchScroll, width, bodyRows, repoPath)
	case mode == ModeStashes && len(p.Stashes) > 0:
		body = renderStashPane(p.Stashes, stashSel, p.StashScroll, width, bodyRows)
	case mode == ModeHistory && len(p.Commits) > 0:
		body = renderCommitPane(p.Commits, commitSel, p.CommitScroll, width, bodyRows)
	case mode == ModeReflog && len(p.Reflogs) > 0:
		body = renderReflogPane(p.Reflogs, reflogSel, p.ReflogScroll, width, bodyRows)
	case mode == ModeSessions && len(p.Sessions) > 0:
		body = renderSessionPane(p.Sessions, sessionSel, p.SessionScroll, width, bodyRows)
	case mode == ModePlans && len(p.Plans) > 0:
		body = renderPlanPane(p.Plans, planSel, p.PlanScroll, width, bodyRows, p.ExpandedPlanID, selectedPlanPhaseID)
	case IsBeadsMode(mode) && p.BeadsOpenPending:
		body = renderPlaceholderPane(width, bodyRows, "loading "+beadsModeLabel(mode)+" beads")
	case IsBeadsMode(mode) && p.BeadsError != "":
		body = renderPlaceholderPane(width, bodyRows, "Could not load "+beadsModeLabel(mode)+" beads: "+terminalSafeSingleLine(p.BeadsError))
	case IsBeadsMode(mode) && len(p.BeadsOpen) > 0:
		body = renderBeadsPane(p.BeadsOpen, beadSel, p.BeadsOpenScroll, width, bodyRows, p.BeadExpansion)
	case IsBeadsMode(mode):
		message := "beads not configured"
		if repoPath == "" || p.BeadsOpenAvailable {
			message = paneEmptyMessage(p, mode, repoPath)
		}
		body = renderPlaceholderPane(width, bodyRows, message)
	default:
		body = renderPlaceholderPane(width, bodyRows, paneEmptyMessage(p, mode, repoPath))
	}
	var warnings []string
	if degradationWarning != "" {
		warnings = append(warnings, truncateToWidth(aheadBehindStyle.Render(" "+terminalSafeSingleLine(degradationWarning)), width))
	}
	if showCachedWarning {
		warning := " Could not refresh " + modeDataLabel(mode) + "; showing cached data"
		warnings = append(warnings, truncateToWidth(dirtyRedStyle.Render(warning), width))
	}
	if len(warnings) > 0 {
		body = append(warnings, body...)
	}

	lines := append(headerLines, body...)
	if len(lines) > contentRows {
		lines = lines[:contentRows]
	}
	borderColor := clearDarkTheme.inactiveBorder
	if focused {
		borderColor = clearDarkTheme.activeBorder
		if p.Destructive {
			borderColor = clearDarkTheme.destructiveBorder
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Width(width).
		Height(contentRows).
		Render(strings.Join(lines, "\n"))
}

func stackedModePaneHasRows(p RenderParams, mode Mode, takeover bool) bool {
	switch {
	case mode == ModePRBabysitter:
		return len(p.PRBabysitterRows) > 0
	case mode == ModeFlows || takeover:
		return len(p.Flows) > 0
	case mode == ModeWorktrees:
		return len(p.Worktrees) > 0
	case mode == ModeBranches:
		return len(p.Branches) > 0
	case mode == ModeStashes:
		return len(p.Stashes) > 0
	case mode == ModeHistory:
		return len(p.Commits) > 0
	case mode == ModeReflog:
		return len(p.Reflogs) > 0
	case mode == ModeSessions:
		return len(p.Sessions) > 0
	case mode == ModePlans:
		return len(p.Plans) > 0
	case IsBeadsMode(mode):
		return !p.BeadsOpenPending && p.BeadsError == "" && len(p.BeadsOpen) > 0
	default:
		return false
	}
}

func paneEmptyMessage(p RenderParams, mode Mode, repoPath string) string {
	if (mode == ModeActiveFlows || mode == ModePRBabysitter) && mode == p.Mode && strings.TrimSpace(p.ItemSearch) != "" && strings.Contains(p.RightEmptyMessage, " results for ") {
		return p.RightEmptyMessage
	}
	if mode != ModeActiveFlows && mode != ModePRBabysitter && repoPath == "" {
		if strings.TrimSpace(p.RepoSearch) != "" {
			return "No matching repo"
		}
		return "No selected repo"
	}
	query, sourceCount := paneItemFilterState(p, mode)
	if strings.TrimSpace(query) != "" && sourceCount > 0 {
		return "No " + modeResultLabel(mode) + " results for " + query
	}
	// Preserve the focused-pane compatibility path for callers that only
	// provide the original aggregate search fields.
	if mode == p.Mode && strings.TrimSpace(p.ItemSearch) != "" && strings.Contains(p.RightEmptyMessage, " results for ") {
		return p.RightEmptyMessage
	}
	listError := paneListError(p, mode)
	if listError != "" {
		message := "Could not load " + modeDataLabel(mode)
		if p.TransientError == listError {
			message += "; see status bar"
		}
		return message
	}
	if mode == p.Mode && p.RightEmptyMessage != "" {
		return p.RightEmptyMessage
	}
	if IsBeadsMode(mode) {
		if strings.TrimSpace(p.BeadsQuery) != "" && p.BeadsSourceCount > 0 && len(p.BeadsOpen) == 0 {
			return "No bead results for " + p.BeadsQuery
		}
		if p.BeadsOpenAvailable {
			return "no " + beadsModeLabel(mode) + " beads"
		}
		return "beads not configured"
	}
	switch mode {
	case ModeWorktrees:
		return "No worktrees"
	case ModeBranches:
		return "No branches"
	case ModeStashes:
		return "No stashes"
	case ModeHistory:
		return "No commits"
	case ModeReflog:
		return "No reflog entries"
	case ModeSessions:
		return "No sessions"
	case ModePlans:
		return "No plans"
	case ModeFlows:
		return "No flows"
	case ModeActiveFlows:
		return "No active flows"
	case ModePRBabysitter:
		return "No PRs awaiting merge"
	default:
		return p.RightEmptyMessage
	}
}

// ShowsCachedListWarning reports whether a stacked pane prepends the "showing
// cached data" banner above its list, which costs one list row. The model
// reserves the same row so selection and scroll geometry match what renders.
func ShowsCachedListWarning(listError string, hasRows bool, sourceCount int) bool {
	return listError != "" && (hasRows || sourceCount > 0)
}

func paneListError(p RenderParams, mode Mode) string {
	pane, paneOK := PaneForMode(mode)
	switch {
	case mode == ModeActiveFlows:
		return p.ActiveFlowsListError
	case mode == ModePRBabysitter:
		return p.PRBabysitterListError
	case paneOK && pane == PaneTop:
		return p.TopListError
	case paneOK && pane == PaneBottom:
		return p.BottomListError
	default:
		return ""
	}
}

func paneDegradationWarning(p RenderParams, mode Mode) string {
	switch mode {
	case ModeFlows:
		return p.FlowDegradationWarning
	case ModeActiveFlows:
		return p.ActiveFlowDegradationWarning
	case ModePRBabysitter:
		return p.PRBabysitterDegradationWarning
	}
	pane, ok := PaneForMode(mode)
	if !ok {
		return ""
	}
	if pane == PaneTop {
		return p.TopDegradationWarning
	}
	return p.BottomDegradationWarning
}

func paneItemFilterState(p RenderParams, mode Mode) (string, int) {
	if mode == ModeActiveFlows || mode == ModePRBabysitter {
		return p.ItemSearch, p.ItemSourceCount
	}
	pane, ok := PaneForMode(mode)
	if !ok {
		return "", 0
	}
	if pane == PaneTop {
		return p.TopItemSearch, p.TopItemSourceCount
	}
	return p.BottomItemSearch, p.BottomItemSourceCount
}

func modeResultLabel(mode Mode) string {
	switch mode {
	case ModeWorktrees:
		return "worktree"
	case ModeBranches:
		return "branch"
	case ModeStashes:
		return "stash"
	case ModeHistory:
		return "commit"
	case ModeReflog:
		return "reflog"
	case ModeSessions:
		return "session"
	case ModePlans:
		return "plan"
	case ModeFlows, ModeActiveFlows, ModePRBabysitter:
		return "flow"
	default:
		if IsBeadsMode(mode) {
			return "bead"
		}
		return "item"
	}
}

func modeDataLabel(mode Mode) string {
	switch mode {
	case ModeWorktrees:
		return "worktrees"
	case ModeBranches:
		return "branches"
	case ModeStashes:
		return "stashes"
	case ModeHistory:
		return "commits"
	case ModeReflog:
		return "reflog"
	case ModeSessions:
		return "sessions"
	case ModePlans:
		return "plans"
	case ModeFlows:
		return "flows"
	case ModeActiveFlows:
		return "active flows"
	case ModePRBabysitter:
		return "PR babysitter"
	default:
		if IsBeadsMode(mode) {
			return beadsModeLabel(mode) + " beads"
		}
		return "data"
	}
}

func renderStackedModeHeader(p RenderParams, mode Mode, width int, takeover bool) string {
	if takeover {
		return renderModeHeaderRowWithRightItems(nil, takeoverHeaderItems(mode), width)
	}
	if IsGitMode(mode) || IsBeadsMode(mode) {
		topLevel := []modeHeaderItem{
			{key: "1", name: "git", active: IsGitMode(mode)},
			{key: "2", name: "beads", active: IsBeadsMode(mode)},
		}
		header := renderModeHeaderRowWithRightItems(topLevel, takeoverHeaderItems(mode), width)
		if IsGitMode(mode) {
			return header + "\n" + renderModeHeaderRow([]modeHeaderItem{
				{key: "w", name: "worktrees", active: mode == ModeWorktrees},
				{key: "b", name: "branches", active: mode == ModeBranches},
				{key: "s", name: "stashes", active: mode == ModeStashes},
				{key: "h", name: "history", active: mode == ModeHistory},
				{key: "r", name: "reflog", active: mode == ModeReflog},
			}, width)
		}
		closedName := "closed"
		if mode == ModeBeadsClosed && p.BeadsOpenAvailable && !p.BeadsOpenPending {
			closedName = fmt.Sprintf("closed %d", p.BeadsSourceCount)
			if p.BeadsClosedTotal > p.BeadsSourceCount {
				closedName = fmt.Sprintf("closed %d of %d", p.BeadsSourceCount, p.BeadsClosedTotal)
			}
		}
		return header + "\n" + renderModeHeaderRowKeepingActive([]modeHeaderItem{
			{key: "r", name: "ready", active: mode == ModeBeadsReady},
			{key: "b", name: "blocked", active: mode == ModeBeadsBlocked},
			{key: "o", name: "open", active: mode == ModeBeadsOpen},
			{key: "i", name: "in-progress", active: mode == ModeBeadsInProgress},
			{key: "c", name: closedName, active: mode == ModeBeadsClosed},
		}, width)
	}
	return renderModeHeaderRow([]modeHeaderItem{
		{key: "1", name: "sessions", active: mode == ModeSessions},
		{key: "2", name: "plans", active: mode == ModePlans},
		{key: "3", name: "flows", active: mode == ModeFlows},
	}, width)
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

// modeHeaderItem is one selectable entry in a mode header row.
type modeHeaderItem struct {
	key    string
	name   string
	active bool
}

// renderModeHeader produces the view selector shown at the top of the right
// pane: a single top-level row normally, plus a second subview row while Git
// or Beads is active.
func renderModeHeader(mode Mode, width int) string {
	return renderModeHeaderWithBeads(mode, width, 0, 0, false, false)
}

func renderModeHeaderWithBeads(mode Mode, width, fetched, total int, available, pending bool) string {
	topLevel := []modeHeaderItem{
		{key: "1", name: "git", active: IsGitMode(mode)},
		{key: "2", name: "sessions", active: mode == ModeSessions},
		{key: "3", name: "plans", active: mode == ModePlans},
		{key: "4", name: "flows", active: mode == ModeFlows},
		{key: "5", name: "beads", active: IsBeadsMode(mode)},
	}
	takeovers := takeoverHeaderItems(mode)
	separator := strings.Repeat("─", width)
	header := renderModeHeaderRowWithRightItems(topLevel, takeovers, width)
	if IsGitMode(mode) {
		subviews := []modeHeaderItem{
			{key: "w", name: "worktrees", active: mode == ModeWorktrees},
			{key: "b", name: "branches", active: mode == ModeBranches},
			{key: "s", name: "stashes", active: mode == ModeStashes},
			{key: "h", name: "history", active: mode == ModeHistory},
			{key: "r", name: "reflog", active: mode == ModeReflog},
		}
		header += "\n" + renderModeHeaderRow(subviews, width)
	} else if IsBeadsMode(mode) {
		closedName := "closed"
		if mode == ModeBeadsClosed && available && !pending {
			closedName = fmt.Sprintf("closed %d", fetched)
			if total > fetched {
				closedName = fmt.Sprintf("closed %d of %d", fetched, total)
			}
		}
		subviews := []modeHeaderItem{
			{key: "r", name: "ready", active: mode == ModeBeadsReady},
			{key: "b", name: "blocked", active: mode == ModeBeadsBlocked},
			{key: "o", name: "open", active: mode == ModeBeadsOpen},
			{key: "i", name: "in-progress", active: mode == ModeBeadsInProgress},
			{key: "c", name: closedName, active: mode == ModeBeadsClosed},
		}
		header += "\n" + renderModeHeaderRowKeepingActive(subviews, width)
	}
	return header + "\n" + separator
}

func renderModeHeaderItem(item modeHeaderItem) string {
	if item.active {
		return activeModeStyle.Render(fmt.Sprintf("[%s] %s", item.key, item.name))
	}
	return inactiveModeStyle.Render(fmt.Sprintf(" %s %s", item.key, item.name))
}

func renderModeHeaderRow(items []modeHeaderItem, width int) string {
	var parts []string
	for _, item := range items {
		parts = append(parts, renderModeHeaderItem(item))
	}
	return ansi.Truncate(" "+strings.Join(parts, " "), width, "")
}

func renderModeHeaderRowKeepingActive(items []modeHeaderItem, width int) string {
	row := renderModeHeaderRow(items, width)
	fullWidth := ansi.StringWidth(" " + strings.Join(renderedModeHeaderItems(items), " "))
	if fullWidth <= width {
		return row
	}
	return renderModeHeaderRow(activeHeaderItemFirst(items), width)
}

func renderedModeHeaderItems(items []modeHeaderItem) []string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, renderModeHeaderItem(item))
	}
	return parts
}

func renderModeHeaderRowWithRight(left []modeHeaderItem, right *modeHeaderItem, width int) string {
	if right == nil {
		return renderModeHeaderRowWithRightItems(left, nil, width)
	}
	return renderModeHeaderRowWithRightItems(left, []modeHeaderItem{*right}, width)
}

func takeoverHeaderItems(mode Mode) []modeHeaderItem {
	return []modeHeaderItem{
		{key: "^p", name: "PR babysitter", active: mode == ModePRBabysitter},
		{key: "^a", name: "active flows", active: mode == ModeActiveFlows},
	}
}

func renderModeHeaderRowWithRightItems(left, right []modeHeaderItem, width int) string {
	leftLine := renderModeHeaderRow(left, width)
	if len(right) == 0 {
		return leftLine
	}
	rightLine := strings.Join(renderedModeHeaderItems(right), " ")
	rightWidth := ansi.StringWidth(rightLine)
	if width <= rightWidth {
		return ansi.Truncate(" "+rightLine, width, "")
	}
	leftWidth := ansi.StringWidth(leftLine)
	if leftWidth+rightWidth <= width {
		return leftLine + strings.Repeat(" ", width-leftWidth-rightWidth) + rightLine
	}
	leftLine = renderModeHeaderRow(activeHeaderItemFirst(left), width-rightWidth)
	leftWidth = ansi.StringWidth(leftLine)
	return leftLine + strings.Repeat(" ", width-leftWidth-rightWidth) + rightLine
}

func activeHeaderItemFirst(items []modeHeaderItem) []modeHeaderItem {
	for i, item := range items {
		if !item.active || i == 0 {
			continue
		}
		prioritized := make([]modeHeaderItem, 0, len(items))
		prioritized = append(prioritized, item)
		prioritized = append(prioritized, items[:i]...)
		prioritized = append(prioritized, items[i+1:]...)
		return prioritized
	}
	return items
}

// RenderStatusBar produces the bottom status bar (hints only, no mode tabs).
func RenderStatusBar(width int, mode Mode, overlay OverlayState, activePane Pane, destructive, staleSelected, dirtySelected bool) string {
	contentFocused := activePane != PaneRepos
	fetchAvailable := contentFocused && (mode == ModeWorktrees || mode == ModeBranches)
	pullAvailable := contentFocused && mode == ModeWorktrees
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
		WorktreeDeletableSelected: contentFocused && mode == ModeWorktrees && !staleSelected,
		WorktreeOpenableSelected:  contentFocused && mode == ModeWorktrees && !staleSelected,
		BranchDeletableSelected:   contentFocused && mode == ModeBranches,
		BranchOpenableSelected:    contentFocused && mode == ModeBranches,
		StashSelected:             contentFocused && mode == ModeStashes,
		CommitSelected:            contentFocused && mode == ModeHistory,
		ReflogSelected:            contentFocused && mode == ModeReflog,
		FetchAvailable:            fetchAvailable,
		PullAvailable:             pullAvailable,
		NewAgent:                  newAgentAvailable,
	})
}

// statusBarParams groups the many fields the status-bar renderer needs,
// avoiding a long and error-prone positional parameter list.
type statusBarParams struct {
	Width                        int
	Mode                         Mode
	ActiveFlows                  bool
	PRBabysitter                 bool
	Overlay                      OverlayState
	InputMode                    InputMode
	PromptEditor                 bool
	FormHasMultiline             bool
	WorktreeInputPrompt          string
	SelectPrompt                 string
	ActivePane                   Pane
	RepoPaneCollapsed            bool
	Destructive                  bool
	RepoSelected                 bool
	WorktreeSelected             bool
	StaleSelected                bool
	DirtySelected                bool
	LockedSelected               bool
	WorktreeDeletableSelected    bool
	WorktreeOpenableSelected     bool
	WorktreeMoveSelected         bool
	WorktreeSessionsOpen         bool
	WorktreeSessionSelected      bool
	BranchDirtySelected          bool
	BranchDeletableSelected      bool
	BranchOpenableSelected       bool
	StashSelected                bool
	CommitSelected               bool
	ReflogSelected               bool
	SessionSelected              bool
	EmbeddedTerminalActive       bool
	EmbeddedTerminalExists       bool
	EmbeddedTerminalPrefix       bool
	EmbeddedTerminalVisible      bool
	PlanSelected                 bool
	PlanPhaseSelected            bool
	FlowSelected                 bool
	FlowPhaseSelected            bool
	FlowDeletableSelected        bool
	FlowWorktreePathSelected     bool
	FlowPlanLinked               bool
	FlowIssueTargetSelected      bool
	FlowPRTargetSelected         bool
	FlowHeadless                 bool
	FlowAutoModeSelected         bool
	FlowAgentLabel               string
	FlowModel                    string
	FlowReasoningEffort          string
	FlowNextLaunchReady          bool
	FlowWorktreeAgentReady       bool
	FlowRepairReady              bool
	FlowManualMergeReadySelected bool
	FlowAutofixReadySelected     bool
	FlowCloseActionSelected      FlowCloseAction
	FlowPhaseResetReadySelected  bool
	FlowPhaseReleaseSelected     bool
	FlowPhaseResumableSelected   bool
	TmuxAttachAvailable          bool
	TransientError               string
	TransientErrorFadeStep       int
	SearchActive                 bool
	RepoSearch                   string
	ItemSearch                   string
	FetchAvailable               bool
	FetchVisibleAvailable        bool
	RepoCreateAvailable          bool
	PullAvailable                bool
	AgentAvailable               bool
	NewAgent                     bool
	ReadyBeadFlowCreateAvailable bool
	ReadyBeadFlowStartAvailable  bool
	ReadyBeadFlowKeysOwned       bool
	BeadSliceEpicAvailable       bool
	EpicAutoOnAvailable          bool
	EpicAutoOffAvailable         bool
	EpicAutoKeyOwned             bool
}

type shortcutHint struct {
	Key           string
	Label         string
	SuccessSuffix string
	Warning       bool
	Inline        bool
	Muted         bool
	Ungrouped     bool
}

type shortcutSection struct {
	Title string
	Hints []shortcutHint
}

func shouldRenderShortcutPane(width, height int, sp statusBarParams) bool {
	return !hasActiveStatusQuery(sp) && shouldRenderShortcutPaneForInnerHeight(width, height)
}

func shouldRenderShortcutPaneForViewport(width, height int, activeStatusQuery bool) bool {
	// The terminal dock chip row is always reserved below the pane row, so it
	// never counts toward the shortcut rail's usable height. The gate stays
	// dock-state-agnostic on purpose — it also decides pane widths via
	// RightContentWidth, which the model calls without dock state — so an
	// expanded dock clips the rail (with the overflow marker) rather than
	// hiding it.
	return !activeStatusQuery && shouldRenderShortcutPaneForInnerHeight(width, height-RepoContentOverhead-TerminalChipRows)
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
	if sp.ActivePane == PaneRepos {
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
		transientError = terminalSafeSingleLine(transientError)
		return statusStyle.Width(width).Render("  " + transientStatusStyle(sp.TransientErrorFadeStep).Render(transientError))
	}

	label := "items"
	query := itemSearch
	if activePane == PaneRepos {
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
		if sp.PromptEditor {
			return renderStatusText(width, promptEditorStatusText(width))
		}
		if sp.InputMode == InputMultiLine {
			return renderStatusText(width, "  enter: submit  alt+enter: newline  esc: cancel  bksp/del: edit")
		}
		return renderStatusText(width, "  enter: submit  esc: cancel  bksp/del: edit  left/right: move")
	case overlay == OverlaySelect:
		if sp.SelectPrompt == PromptTemplateSelectPrompt {
			return renderStatusText(width, "  up/down select  enter: edit  r: reset  v: preview  esc: cancel")
		}
		return renderStatusText(width, "  up/down select  enter: confirm  esc: cancel")
	case overlay == OverlayForm:
		if sp.FormHasMultiline {
			return renderStatusText(width, "  tab/shift+tab: fields  alt+enter: newline  enter: submit  esc: cancel")
		}
		return renderStatusText(width, "  tab/shift+tab: fields  space: toggle/select  enter: submit  esc: cancel")
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
	title := titleStyle.Render("Shortcuts") + "  " + modeStyle.Render(modeShortcutTitleForStatus(sp))
	lines = append(lines, ansi.Truncate(" "+title, width, ""))
	compact := height <= 3
	flowSurfaceActive := sp.Mode == ModeFlows || sp.Mode == ModeActiveFlows || sp.Mode == ModePRBabysitter || sp.ActiveFlows || sp.PRBabysitter
	tight := height <= 7 || (flowSurfaceActive && height <= 14)
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
				grouped = append(grouped, shortcutHint{Key: "↑/↓ ←/→", Label: "select/view", Warning: hint.Warning || next.Warning})
				i++
				continue
			case hint.Key == "f" && next.Key == "F" && !hint.Ungrouped && !next.Ungrouped:
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
	flowSurfaceActive := sp.Mode == ModeFlows || sp.Mode == ModeActiveFlows || sp.Mode == ModePRBabysitter || sp.ActiveFlows || sp.PRBabysitter
	if sp.EmbeddedTerminalActive {
		hints := []shortcutHint{{Key: "ctrl+]", Label: "commands"}}
		if sp.EmbeddedTerminalPrefix {
			hints = []shortcutHint{
				{Key: "ctrl+]", Label: "send"},
				{Key: "i", Label: "input"},
				{Key: "t", Label: "hide"},
				{Key: "l", Label: "sessions"},
				{Key: "left/right", Label: "terminal"},
				{Key: "d", Label: "detach"},
				{Key: "x", Label: "close"},
				{Key: "q/esc", Label: "quit"},
				{Key: "1-9", Label: "switch"},
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
	}
	if sp.ActivePane == PaneRepos && sp.RepoSelected {
		navigation = append(navigation, shortcutHint{Key: "enter", Label: "pane", Inline: true})
	}
	if sp.ActivePane != PaneRepos {
		if sp.Mode != ModeActiveFlows && sp.Mode != ModePRBabysitter && !sp.ActiveFlows && !sp.PRBabysitter {
			navigation = append(navigation, shortcutHint{Key: "←/→", Label: "view", Inline: true})
		}
		if IsGitMode(sp.Mode) {
			navigation = append(navigation, shortcutHint{Key: "w/b/s/h/r", Label: "subview", Inline: true})
		} else if IsBeadsMode(sp.Mode) {
			navigation = append(navigation, shortcutHint{Key: "r/b/o/i/c", Label: "subview", Inline: true})
		}
	}
	global := []shortcutHint{
		{Key: paneShortcutKeyForStatus(sp), Label: "pane"},
		{Key: "q/esc", Label: "quit"},
		{Key: "ctrl+a", Label: "active flows"},
		{Key: "ctrl+p", Label: "PR babysitter"},
		{Key: "f2", Label: "edit prompts"},
		{Key: "f5", Label: "refresh"},
	}
	if sp.RepoPaneCollapsed {
		global = slices.Insert(global, 0, shortcutHint{Key: "^r", Label: "repos"})
	}
	if sp.EmbeddedTerminalExists {
		label := "show terminal"
		if sp.EmbeddedTerminalVisible {
			label = "hide terminal"
		}
		global = slices.Insert(global, 1, shortcutHint{Key: "ctrl+t", Label: label})
	}
	if !flowSurfaceActive {
		global = slices.Insert(global, 2, shortcutHint{Key: "A", Label: "set agent"})
	}
	// Only offered in tmux mode: with the embedded backend there is no per-repo
	// tmux session to attach to.
	if sp.TmuxAttachAvailable {
		global = slices.Insert(global, 1, shortcutHint{Key: "T", Label: "attach tmux"})
	}

	var actions []shortcutHint
	if sp.ActivePane == PaneRepos && sp.FetchVisibleAvailable {
		actions = append(actions, shortcutHint{Key: "f", Label: "fetch visible"})
	}
	if sp.ActivePane == PaneRepos && sp.RepoCreateAvailable {
		actions = append(actions, shortcutHint{Key: "n", Label: "new repo"})
	}
	if sp.Mode == ModeBeadsReady && sp.ReadyBeadFlowCreateAvailable {
		actions = append(actions, shortcutHint{Key: "f", Label: "new flow", Ungrouped: true})
	}
	if sp.Mode == ModeBeadsReady && sp.ReadyBeadFlowStartAvailable {
		actions = append(actions, shortcutHint{Key: "F", Label: "new flow + start", Ungrouped: true})
	}
	if sp.Mode == ModeBeadsReady && sp.BeadSliceEpicAvailable {
		actions = append(actions, shortcutHint{Key: "S", Label: "slice epic", Ungrouped: true})
	}
	if IsBeadsMode(sp.Mode) && sp.EpicAutoOnAvailable {
		actions = append(actions, shortcutHint{Key: "a", Label: "auto on", Ungrouped: true})
	}
	if IsBeadsMode(sp.Mode) && sp.EpicAutoOffAvailable {
		actions = append(actions, shortcutHint{Key: "a", Label: "auto off", Ungrouped: true})
	}
	if flowSurfaceActive {
		return flowShortcutSections(sp, actions, navigation, global)
	}
	switch sp.Mode {
	case ModeWorktrees:
		if sp.ActivePane != PaneRepos && sp.RepoSelected && !sp.StaleSelected {
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
		if sp.ActivePane != PaneRepos && sp.RepoSelected && sp.StaleSelected && sp.NewAgent {
			actions = append(actions, shortcutHint{Key: "N", Label: "new+agent"})
		}
		if sp.ActivePane != PaneRepos && sp.StaleSelected && sp.Destructive && sp.WorktreeSelected && !sp.LockedSelected {
			actions = append(actions, shortcutHint{Key: "p", Label: "prune", Warning: true})
		}
		if sp.ActivePane != PaneRepos && sp.WorktreeSelected && sp.LockedSelected {
			actions = append(actions, shortcutHint{Key: "u", Label: "unlock"})
		}
	case ModeBranches:
		if sp.ActivePane != PaneRepos {
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
		if sp.ActivePane != PaneRepos && sp.StashSelected {
			actions = append(actions, shortcutHint{Key: "enter", Label: "diff"})
			if sp.Destructive {
				actions = append(actions, shortcutHint{Key: "d", Label: "drop", Warning: true})
			}
		}
	case ModeHistory:
		if sp.ActivePane != PaneRepos {
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
		if sp.ActivePane != PaneRepos && sp.ReflogSelected {
			actions = append(actions,
				shortcutHint{Key: "enter", Label: "diff"},
				shortcutHint{Key: "y", Label: "copy hash"},
			)
		}
	case ModeSessions:
		if sp.ActivePane != PaneRepos && sp.SessionSelected {
			actions = append(actions,
				shortcutHint{Key: "o", Label: "transcript"},
				shortcutHint{Key: "r", Label: "resume"},
				shortcutHint{Key: "s", Label: "summary"},
				shortcutHint{Key: "y", Label: "copy id"},
			)
		}
	case ModePlans:
		if sp.ActivePane != PaneRepos && sp.PlanSelected {
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
	}
	if sp.ActivePane != PaneRepos && sp.Mode != ModeWorktrees && sp.Mode != ModeBranches && !sp.ActiveFlows && !sp.PRBabysitter {
		if sp.FetchAvailable {
			actions = append(actions, shortcutHint{Key: "f", Label: "fetch"})
		}
		if sp.PullAvailable && !(sp.Mode == ModeBeadsReady && sp.ReadyBeadFlowKeysOwned) {
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
		return shortcutHint{Key: "a", Label: "auto: on", SuccessSuffix: "on"}
	}
	return shortcutHint{Key: "a", Label: "auto: off"}
}

func flowShortcutSections(sp statusBarParams, actions, navigation, global []shortcutHint) []shortcutSection {
	var flowModeControls []shortcutHint
	var flowAgentControls []shortcutHint
	if sp.ActivePane != PaneRepos && sp.RepoSelected {
		if !sp.ActiveFlows && !sp.PRBabysitter {
			actions = append(actions, shortcutHint{Key: "n", Label: "new flow"})
		}
		if sp.FlowSelected {
			headlessLabel := "headless off"
			headlessSuccessSuffix := ""
			if sp.FlowHeadless {
				headlessLabel = "headless on"
				headlessSuccessSuffix = "on"
			}
			flowModeControls = append(flowModeControls, shortcutHint{Key: "h", Label: headlessLabel, SuccessSuffix: headlessSuccessSuffix})
			actions = append(actions, shortcutHint{Key: "enter", Label: "phases"})
			if sp.FlowWorktreeAgentReady {
				actions = append(actions, shortcutHint{Key: "s", Label: "start agent"})
			}
			if sp.FlowRepairReady {
				actions = append(actions, shortcutHint{Key: "R", Label: "repair"})
			}
			if sp.FlowNextLaunchReady {
				actions = append(actions, shortcutHint{Key: "g", Label: "launch next"})
			}
			if !sp.FlowPhaseSelected && sp.FlowManualMergeReadySelected {
				actions = append(actions, shortcutHint{Key: "m", Label: "mark merged"})
			}
			if !sp.FlowPhaseSelected && sp.FlowAutofixReadySelected {
				actions = append(actions, shortcutHint{Key: "U", Label: "autofix PR"})
			}
			if !sp.FlowPhaseSelected {
				switch sp.FlowCloseActionSelected {
				case FlowCloseActionClose:
					actions = append(actions, shortcutHint{Key: "C", Label: "close"})
				case FlowCloseActionReopen:
					actions = append(actions, shortcutHint{Key: "C", Label: "reopen"})
				}
			}
			if sp.FlowPhaseSelected && sp.FlowPhaseResetReadySelected {
				actions = append(actions, shortcutHint{Key: "x", Label: "reset ready"})
			}
			// Never both: release applies only where reset does not, so x is
			// unambiguous. The labels still differ, because the confirmation is
			// otherwise the only thing telling the two apart at press time.
			if sp.FlowPhaseSelected && sp.FlowPhaseReleaseSelected {
				actions = append(actions, shortcutHint{Key: "x", Label: "release session"})
			}
			if !sp.FlowPhaseSelected && sp.FlowPlanLinked {
				actions = append(actions, shortcutHint{Key: "o", Label: "open"})
			}
			actions = append(actions, shortcutHint{Key: "c", Label: "copy id"})
			if sp.FlowIssueTargetSelected {
				actions = append(actions, shortcutHint{Key: "i", Label: "open issue"})
			}
			if sp.FlowPRTargetSelected {
				actions = append(actions, shortcutHint{Key: "p", Label: "open PR"})
			}
			if sp.FlowWorktreePathSelected {
				actions = append(actions, shortcutHint{Key: "y", Label: "copy path"})
			}
			if sp.FlowPhaseSelected && sp.FlowPhaseResumableSelected {
				actions = append(actions, shortcutHint{Key: "r", Label: "resume"})
			}
			if !sp.FlowPhaseSelected && sp.Destructive && sp.FlowDeletableSelected {
				actions = append(actions, shortcutHint{Key: "d", Label: "delete", Warning: true})
			}
			// A closed Flow refuses auto mode, so the hint has to go with it.
			// It lives in flowModeControls, so suppressing it leaves h behind
			// and the section survives.
			if sp.FlowCloseActionSelected != FlowCloseActionReopen {
				flowModeControls = append(flowModeControls, flowAutoModeShortcutHint(sp.FlowAutoModeSelected))
			}
		}
	}
	if !sp.Destructive {
		actions = append([]shortcutHint{{Key: "D", Label: "destructive mode"}}, actions...)
	}
	agentLabel, agentConfigured := flowAgentShortcut(sp.FlowAgentLabel)
	flowAgentControls = append(flowAgentControls, shortcutHint{Key: "A", Label: agentLabel})
	if modelLabel := flowModelShortcutLabel(sp.FlowModel); agentConfigured && modelLabel != "" {
		flowAgentControls = append(flowAgentControls, shortcutHint{Key: "M", Label: modelLabel})
	}
	if effortLabel := flowReasoningEffortShortcutLabel(sp.FlowReasoningEffort); agentConfigured && effortLabel != "" {
		flowAgentControls = append(flowAgentControls, shortcutHint{Key: "E", Label: effortLabel})
	}
	var sections []shortcutSection
	if len(actions) > 0 {
		sections = append(sections, shortcutSection{Title: "Actions", Hints: actions})
	}
	if len(flowModeControls) > 0 {
		sections = append(sections, shortcutSection{Title: "Mode", Hints: flowModeControls})
	}
	if len(flowAgentControls) > 0 {
		sections = append(sections, shortcutSection{Title: "Agent", Hints: flowAgentControls})
	}
	sections = append(sections, shortcutSection{Title: "Global", Hints: append(navigation, global...)})
	return sections
}

const flowChooseAgentLabel = "choose agent"

func flowAgentShortcut(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == flowChooseAgentLabel {
		return flowChooseAgentLabel, false
	}
	return value, true
}

func shortcutsMuted(sp statusBarParams) bool {
	return sp.EmbeddedTerminalActive && !sp.EmbeddedTerminalPrefix
}

func flowReasoningEffortShortcutLabel(value string) string {
	value = strings.TrimSpace(value)
	return value
}

func flowModelShortcutLabel(value string) string {
	value = strings.TrimSpace(value)
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
	flowSurfaceActive := sp.Mode == ModeFlows || sp.Mode == ModeActiveFlows || sp.Mode == ModePRBabysitter || sp.ActiveFlows || sp.PRBabysitter
	if height < 20 && sp.Mode != ModeFlows && sp.Mode != ModeActiveFlows && sp.Mode != ModePRBabysitter && !sp.ActiveFlows && !sp.PRBabysitter {
		paneKey := paneShortcutKeyForStatus(sp)
		sections = prioritizeShortcutInSection(sections, "Global", "A", paneKey)
	}
	if flowSurfaceActive && height < 24 {
		sections = withoutShortcutKey(sections, "f2")
	}
	if flowSurfaceActive && !sp.FlowSelected && height <= 9 {
		sections = withoutShortcutKeys(sections, "D", "n", "f5")
	}
	if flowSurfaceActive && sp.FlowSelected && height <= 12 {
		sections = withoutShortcutKey(sections, "n")
	}
	if height < 20 {
		return withoutShortcutKey(sections, "f5")
	}
	return sections
}

func prioritizeShortcutInSection(sections []shortcutSection, title, key, beforeKey string) []shortcutSection {
	for si, section := range sections {
		if section.Title != title {
			continue
		}
		keyIndex := -1
		beforeIndex := -1
		for i, hint := range section.Hints {
			if hint.Key == key {
				keyIndex = i
			}
			if hint.Key == beforeKey {
				beforeIndex = i
			}
		}
		if keyIndex < 0 || beforeIndex < 0 || keyIndex < beforeIndex {
			return sections
		}
		hint := section.Hints[keyIndex]
		hints := append([]shortcutHint{}, section.Hints[:keyIndex]...)
		hints = append(hints, section.Hints[keyIndex+1:]...)
		hints = slices.Insert(hints, beforeIndex, hint)
		sections[si].Hints = hints
		return sections
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
	if sp.Mode == ModeFlows || sp.Mode == ModeActiveFlows || sp.Mode == ModePRBabysitter || sp.ActiveFlows || sp.PRBabysitter {
		return renderFlowFooterShortcuts(sp, sections)
	}
	return renderGenericFooterShortcuts(sp, sections)
}

func paneShortcutKeyForStatus(sp statusBarParams) string {
	if sp.ActivePane == PaneRepos {
		return paneShortcutKey
	}
	return paneBackShortcutKey
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
	base := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, paneShortcutKeyForStatus(sp), "q/esc")...)
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
	// Group subview hints are rail-only; the space-constrained footer keeps its
	// established hint set.
	sections = withoutShortcutKeys(sections, "w/b/s/h/r", "r/b/o/i/c")
	paneKey := paneShortcutKeyForStatus(sp)
	for _, drop := range [][]string{
		{},
		{"ctrl+p"},
		{"ctrl+p", "f5"},
		{"ctrl+p", "f5", "f2"},
		{"ctrl+p", "f5", "f2", "ctrl+a"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A", "D"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A", "D", "←/→"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A", "D", "←/→", "↑/↓"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A", "D", "←/→", "↑/↓", "q/esc"},
		{"ctrl+p", "f5", "f2", "ctrl+a", "A", "D", "←/→", "↑/↓", "q/esc", paneKey},
	} {
		candidate := "  " + renderFooterHintList(footerSectionOrder(withoutShortcutKeys(sections, drop...)))
		if lipgloss.Width(candidate) <= sp.Width {
			return candidate
		}
	}
	candidate := "  " + renderFooterHintList(footerSectionOrder(withoutShortcutKeys(sections, "f5", "f2", "ctrl+a", "ctrl+p", "A", "D", "←/→", "↑/↓", "q/esc", paneKey)))
	return ansi.Truncate(candidate, sp.Width, "")
}

func renderFlowFooterShortcuts(sp statusBarParams, sections []shortcutSection) string {
	if sp.EmbeddedTerminalActive {
		return renderGenericFooterShortcuts(sp, sections)
	}
	full := "  " + renderFooterHintList(flowFooterSectionOrder(sections))
	if lipgloss.Width(full) <= sp.Width {
		return full
	}
	for _, drop := range [][]string{
		{"ctrl+p"},
		{"ctrl+p", "f2"},
		{"ctrl+p", "f2", "f5"},
		{"ctrl+p", "f2", "f5", "ctrl+a"},
	} {
		candidate := "  " + renderFooterHintList(flowFooterSectionOrder(withoutShortcutKeys(sections, drop...)))
		if lipgloss.Width(candidate) <= sp.Width {
			return candidate
		}
	}
	hints := flattenShortcutHints(sections)
	base := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, paneShortcutKeyForStatus(sp), "q/esc")...)
	compactBase := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, paneBackShortcutKey, "q/esc")...)
	tinyBase := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, "q/esc")...)
	toggle := footerHintsForKeys(hints, "ctrl+a")
	upDown := footerHintsForKeys(hints, "↑/↓")
	arrow := footerHintsForKeys(hints, "←/→")
	coreActions := footerHintsForKeys(hints, "D", "h", "enter", "g", "m", "d")
	coreActionsWithoutSafety := footerHintsForKeys(hints, "h", "enter", "g", "m", "d")
	coreActionsWithAuto := footerHintsForKeys(hints, "D", "h", "enter", "g", "m", "d", "a")
	coreActionsWithAutoWithoutSafety := footerHintsForKeys(hints, "h", "enter", "g", "m", "d", "a")
	selectedActionsWithAuto := footerHintsForKeys(hints, "D", "h", "enter", "g", "m", "x", "o", "y", "d", "r", "a")
	actions := footerHintsForKeys(hints, "D", "n", "h", "enter", "g", "m", "x", "o", "y", "d", "r", "a", "A", "M", "E", "f", "F")
	actionsWithoutEffort := footerHintsForKeys(hints, "D", "n", "h", "enter", "g", "m", "x", "o", "y", "d", "r", "a", "A", "M", "f", "F")
	actionsWithoutModelAndEffort := footerHintsForKeys(hints, "D", "n", "h", "enter", "g", "m", "x", "o", "y", "d", "r", "a", "A", "f", "F")
	actionsWithoutAgentAndPreferences := footerHintsForKeys(hints, "D", "n", "h", "enter", "g", "m", "x", "o", "y", "d", "r", "a", "f", "F")

	for _, parts := range [][]string{
		appendParts(base, toggle, upDown, arrow, actions),
		appendParts(base, toggle, upDown, arrow, actionsWithoutEffort),
		appendParts(base, toggle, arrow, actionsWithoutModelAndEffort),
		appendParts(base, toggle, arrow, actionsWithoutAgentAndPreferences),
		appendParts(base, toggle, coreActionsWithAuto),
		appendParts(base, toggle, coreActions),
		appendParts(base, upDown, arrow, actions),
		appendParts(base, upDown, arrow, actionsWithoutEffort),
		appendParts(base, upDown, arrow, actionsWithoutModelAndEffort),
		appendParts(base, arrow, actionsWithoutEffort),
		appendParts(base, arrow, actionsWithoutModelAndEffort),
		appendParts(base, upDown, arrow, actionsWithoutAgentAndPreferences),
		appendParts(base, arrow, actionsWithoutAgentAndPreferences),
		appendParts(base, arrow, selectedActionsWithAuto),
		appendParts(compactBase, selectedActionsWithAuto),
		appendParts(base, arrow, coreActionsWithAuto),
		appendParts(base, arrow, coreActions),
		appendParts(compactBase, coreActionsWithAuto),
		appendParts(compactBase, coreActions),
		appendParts(compactBase, coreActionsWithAutoWithoutSafety),
		appendParts(compactBase, coreActionsWithoutSafety),
		appendParts(arrow, selectedActionsWithAuto),
		appendParts(arrow, coreActionsWithAuto),
		appendParts(arrow, coreActions),
		appendParts(coreActionsWithAuto),
		appendParts(coreActions),
		base,
		compactBase,
		tinyBase,
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
	base := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, paneShortcutKeyForStatus(sp), "q/esc")...)
	nav := footerHintsForKeys(hints, "↑/↓", "←/→")
	actions := footerHintsForKeys(hints, "D", "n", "enter", "d", "f", "F", "t", "c", "a")

	full := append(append(append([]string{}, base...), actions...), nav...)
	baseActions := append(append([]string{}, base...), actions...)
	baseNav := append(append([]string{}, base...), nav...)
	baseArrow := footerHintsForKeys(hints, footerKeysWithRepoRestore(sp, paneShortcutKeyForStatus(sp), "q/esc", "←/→")...)

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

// footerKeysWithRepoRestore builds the always-present footer keys every mode's
// renderer starts from. T joins them only in tmux mode, where it is the one way
// back to a running agent from the TUI.
func footerKeysWithRepoRestore(sp statusBarParams, keys ...string) []string {
	if sp.TmuxAttachAvailable {
		keys = append(append([]string{}, keys...), "T")
	}
	if !sp.RepoPaneCollapsed {
		return keys
	}
	return append([]string{"^r"}, keys...)
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

func flowFooterSectionOrder(sections []shortcutSection) []shortcutSection {
	ordered := make([]shortcutSection, 0, len(sections))
	for _, title := range []string{"Actions", "Mode", "Agent", "Global"} {
		for _, section := range sections {
			if section.Title == title {
				ordered = append(ordered, section)
			}
		}
	}
	for _, section := range sections {
		if section.Title != "Actions" && section.Title != "Mode" && section.Title != "Agent" && section.Title != "Global" {
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
	case ModeActiveFlows:
		return "Active flows"
	case ModePRBabysitter:
		return "PR babysitter"
	case ModeBeadsReady, ModeBeadsBlocked, ModeBeadsOpen, ModeBeadsInProgress, ModeBeadsClosed:
		return "Beads " + beadsModeTitle(mode)
	default:
		return "Items"
	}
}

func beadsModeLabel(mode Mode) string {
	switch mode {
	case ModeBeadsReady:
		return "ready"
	case ModeBeadsBlocked:
		return "blocked"
	case ModeBeadsOpen:
		return "open"
	case ModeBeadsInProgress:
		return "in-progress"
	case ModeBeadsClosed:
		return "closed"
	default:
		return ""
	}
}

func beadsModeTitle(mode Mode) string {
	label := beadsModeLabel(mode)
	if label == "" {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

func modeShortcutTitleForStatus(sp statusBarParams) string {
	if sp.PRBabysitter || sp.Mode == ModePRBabysitter {
		return "PR babysitter"
	}
	if sp.ActiveFlows || sp.Mode == ModeActiveFlows {
		return "Active flows"
	}
	return modeShortcutTitle(sp.Mode)
}

func renderRepoList(repos []scanner.Repo, selected, scroll, width, height int, emptyMessage string, activeTerminalRepoPaths map[string]bool) []string {
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

	showActivityColumn := repoListHasActiveTerminal(repos, activeTerminalRepoPaths)

	for i := 0; i < height; i++ {
		idx := scroll + i
		if idx < len(repos) {
			name := repos[idx].DisplayName
			activeRepo := repoHasActiveTerminal(activeTerminalRepoPaths, repos[idx].Path)
			activityMarker := ""
			if showActivityColumn {
				activityMarker = "  "
				if activeRepo {
					activityMarker = "● "
				}
			}
			if idx == selected {
				lines[i] = renderSelectedRepoRow(name, activityMarker, activeRepo, showActivityColumn, width)
			} else {
				lines[i] = renderRepoRow(name, activityMarker, activeRepo, showActivityColumn, width)
			}
		} else {
			lines[i] = strings.Repeat(" ", width)
		}
	}

	return lines
}

func renderCollapsedRepoPane(repos []scanner.Repo, selected, height int, showRestoreHint bool) []string {
	if height <= 0 {
		return nil
	}
	const contentWidth = CollapsedRepoPaneWidth - 2
	lines := make([]string, 0, height)
	restoreHint := strings.Repeat(" ", contentWidth)
	if showRestoreHint {
		restoreHint = "^r"
	}
	lines = append(lines, restoreHint)
	if selected >= 0 && selected < len(repos) {
		for _, r := range repos[selected].DisplayName {
			if len(lines) >= height {
				break
			}
			width := ansi.StringWidth(string(r))
			if width <= 0 || width > contentWidth {
				continue
			}
			line := string(r)
			if width < contentWidth {
				line += strings.Repeat(" ", contentWidth-width)
			}
			lines = append(lines, line)
		}
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}
	return lines
}

func renderSelectedRepoRow(name, activityMarker string, activeRepo, showActivityColumn bool, width int) string {
	line := selectedStyle.Render(" > ")
	if showActivityColumn {
		if activeRepo {
			line += selectedSegment(cleanStyle, "●") + selectedStyle.Render(" ")
		} else {
			line += selectedStyle.Render(activityMarker)
		}
	}
	line += selectedStyle.Render(name)
	return renderSelectedRow(line, width)
}

func renderRepoRow(name, activityMarker string, activeRepo, showActivityColumn bool, width int) string {
	line := repoStyle.Render("   ")
	if showActivityColumn {
		if activeRepo {
			line += cleanStyle.Render("●") + repoStyle.Render(" ")
		} else {
			line += repoStyle.Render(activityMarker)
		}
	}
	line += repoStyle.Render(name)
	return renderStyledRow(line, repoStyle, width)
}

func repoHasActiveTerminal(activeTerminalRepoPaths map[string]bool, repoPath string) bool {
	if len(activeTerminalRepoPaths) == 0 {
		return false
	}
	if activeTerminalRepoPaths[repoPath] {
		return true
	}
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return false
	}
	return activeTerminalRepoPaths[filepath.Clean(repoPath)]
}

func repoListHasActiveTerminal(repos []scanner.Repo, activeTerminalRepoPaths map[string]bool) bool {
	if len(activeTerminalRepoPaths) == 0 {
		return false
	}
	for _, repo := range repos {
		if repoHasActiveTerminal(activeTerminalRepoPaths, repo.Path) {
			return true
		}
	}
	return false
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

func renderBeadsOpenPane(beads []beadsquery.Bead, selected, scroll, width, height int) []string {
	return renderBeadsPane(beads, selected, scroll, width, height, BeadExpansion{})
}

func terminalSafeSingleLine(text string) string {
	text = ansi.Strip(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
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

// renderEmbeddedTerminalDockPane renders the top-level terminal dock that
// spans the full application width between the pane row and the status bar:
// the framed terminal pane when expanded, or the single chip row otherwise.
func renderEmbeddedTerminalDockPane(tabs []EmbeddedTerminalTab, liveLines []string, prefixActive, focused, expansionRequested bool, width, dockRows int, state EmbeddedTerminalDockState) []string {
	if dockRows <= 0 || width <= 0 {
		return nil
	}
	if state == EmbeddedTerminalDockExpanded {
		return renderEmbeddedTerminalPane(tabs, liveLines, prefixActive, focused, width, dockRows)
	}
	chip := renderEmbeddedTerminalChip(tabs, expansionRequested, width)
	if pad := width - lipgloss.Width(chip); pad > 0 {
		chip += strings.Repeat(" ", pad)
	}
	return scrollAndPad([]string{chip}, 0, dockRows)
}

func renderEmbeddedTerminalChip(tabs []EmbeddedTerminalTab, expansionRequested bool, width int) string {
	if width <= 0 {
		return ""
	}
	if len(tabs) == 0 {
		for _, candidate := range []string{"no terminals running", "no terminals"} {
			if lipgloss.Width(candidate) <= width {
				return statusStyle.Render(candidate)
			}
		}
		return truncateToWidth(statusStyle.Render("no terminals"), width)
	}
	active := tabs[0]
	for _, tab := range tabs {
		if tab.Active {
			active = tab
			break
		}
	}
	activeLabel := fmt.Sprintf("%d", active.Number)
	for _, value := range []string{active.Provider, active.Identity, active.State} {
		if value = strings.TrimSpace(value); value != "" {
			activeLabel += " " + value
		}
	}
	countLabel := "terminal"
	if len(tabs) != 1 {
		countLabel = "terminals"
	}
	action := "ctrl+t show"
	if expansionRequested {
		action = "auto-collapsed · ctrl+t hide"
	}
	candidates := []string{
		fmt.Sprintf("● %d %s · %s · %s", len(tabs), countLabel, activeLabel, action),
		fmt.Sprintf("● %d · %s · ctrl+t", len(tabs), activeLabel),
		fmt.Sprintf("● %d · %s", len(tabs), activeLabel),
		fmt.Sprintf("● %d · ctrl+t", len(tabs)),
		fmt.Sprintf("● %d", len(tabs)),
		"●",
	}
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= width {
			return statusStyle.Render(candidate)
		}
	}
	return truncateToWidth(statusStyle.Render("●"), width)
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
	flowRepoWidth    = 16
	flowBranchWidth  = 20
	flowPhaseWidth   = 34
	flowIssueWidth   = 8
	flowPlanWidth    = 12
	flowPRWidth      = 8
	flowUpdatedWidth = 10
)

const (
	prBabysitterRepoWidth         = 16
	prBabysitterFlowWidth         = 34
	prBabysitterBeadWidth         = 14
	prBabysitterMergeabilityWidth = 12
	prBabysitterChecksWidth       = 10
)

func renderPRBabysitterPane(rows []PRBabysitterRow, selected, scroll, width, height int, expandedFlowID, selectedPhaseID string, activity []FlowTerminalActivity) []string {
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatPRBabysitterColumns("   ", "Mergeability", "Checks", "Repo", "Flow", "Bead")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}
	active := newFlowTerminalActivitySet(activity)
	visualRows := make([]string, 0, len(rows))
	for index, row := range rows {
		record := row.Flow
		title := terminalSafeSingleLine(row.Title)
		repo := terminalSafeSingleLine(row.Repo)
		beadID := terminalSafeSingleLine(row.BeadID)
		mergeability := terminalSafeSingleLine(row.Mergeability)
		checks := terminalSafeSingleLine(row.Checks)
		prefix := flowRowPrefix(false, active.hasFlow(record.FlowID))
		line := formatPRBabysitterColumns(
			prefix,
			prBabysitterStatusStyle(mergeability).Render(fitSessionColumn(mergeability, prBabysitterMergeabilityWidth)),
			prBabysitterStatusStyle(checks).Render(fitSessionColumn(checks, prBabysitterChecksWidth)),
			statusStyle.Render(fitSessionColumn(repo, prBabysitterRepoWidth)),
			stashMsgStyle.Render(fitSessionColumn(title, prBabysitterFlowWidth)),
			statusStyle.Render(fitSessionColumn(beadID, prBabysitterBeadWidth)),
		)
		if index == selected && selectedPhaseID == "" {
			line = renderSelectedRow(formatPRBabysitterColumns(
				selectedFlowRowPrefix(active.hasFlow(record.FlowID)),
				selectedStyle.Render(fitSessionColumn(mergeability, prBabysitterMergeabilityWidth)),
				selectedStyle.Render(fitSessionColumn(checks, prBabysitterChecksWidth)),
				selectedStyle.Render(fitSessionColumn(repo, prBabysitterRepoWidth)),
				selectedStyle.Render(fitSessionColumn(title, prBabysitterFlowWidth)),
				selectedStyle.Render(fitSessionColumn(beadID, prBabysitterBeadWidth)),
			), width)
		}
		visualRows = append(visualRows, truncateToWidth(line, width))
		if record.FlowID == expandedFlowID {
			visualRows = append(visualRows, renderFlowPhaseRows(record, width, selectedPhaseID, active, true)...)
		}
	}
	return append([]string{header}, scrollAndPad(visualRows, scroll, rowHeight)...)
}

func formatPRBabysitterColumns(prefix, mergeability, checks, repo, flow, bead string) string {
	return fmt.Sprintf("%s%s  %s  %s  %s  %s",
		prefix,
		fitSessionColumn(mergeability, prBabysitterMergeabilityWidth),
		fitSessionColumn(checks, prBabysitterChecksWidth),
		fitSessionColumn(repo, prBabysitterRepoWidth),
		fitSessionColumn(flow, prBabysitterFlowWidth),
		fitSessionColumn(bead, prBabysitterBeadWidth),
	)
}

func prBabysitterStatusStyle(status string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "mergeable", "passing":
		return cleanStyle
	case "conflicting", "failing":
		return dirtyRedStyle
	case "pending":
		return aheadBehindStyle
	default:
		return statusStyle
	}
}

func renderFlowPane(records []flowstore.FlowRecord, selected, scroll, width, height int, expandedFlowID, selectedPhaseID string, activity []FlowTerminalActivity, showRepo bool, repoDisplayNames map[string]string) []string {
	if height <= 0 {
		return nil
	}
	header := truncateToWidth(statusStyle.Render(formatFlowColumns(showRepo, "   ", "Status", "Repo", "Branch", "Phase", "Issue", "Plan", "PR", "Updated", "Title")), width)
	rowHeight := height - TableHeaderRows
	if rowHeight <= 0 {
		return []string{header}
	}

	active := newFlowTerminalActivitySet(activity)
	var rows []string
	for i, record := range records {
		phase := flowPhaseProgress(record)
		issue := flowIssueLabel(record)
		plan := flowPlanLabel(record)
		pr := flowPRLabel(record)
		updated := flowUpdatedLabel(record)
		repo := ""
		if showRepo {
			repo = flowRepoLabel(record, repoDisplayNames)
		}
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
		repoCell := statusStyle.Render(fitSessionColumn(repo, flowRepoWidth))
		branchCell := branchStyle.Render(fitSessionColumn(branch, flowBranchWidth))
		phaseCell := diffHdrStyle.Render(fitSessionColumn(phase, flowPhaseWidth))
		issueCell := statusStyle.Render(fitSessionColumn(issue, flowIssueWidth))
		planCell := statusStyle.Render(fitSessionColumn(plan, flowPlanWidth))
		prCell := statusStyle.Render(fitSessionColumn(pr, flowPRWidth))
		updatedCell := stashDateStyle.Render(fitSessionColumn(updated, flowUpdatedWidth))
		title := terminalSafeSingleLine(record.Title)
		if reason := strings.TrimSpace(record.Closed.Reason); reason != "" && flowstore.FlowClosed(record) {
			title += "  (closed: " + terminalSafeSingleLine(reason) + ")"
		}
		titleCell := stashMsgStyle.Render(title)
		line := formatFlowColumns(showRepo, flowRowPrefix(false, active.hasFlow(record.FlowID)),
			statusCell,
			repoCell,
			branchCell,
			phaseCell,
			issueCell,
			planCell,
			prCell,
			updatedCell,
			titleCell,
		)
		if rowSelected {
			line = renderSelectedFlowColumns(showRepo, selectedFlowRowPrefix(active.hasFlow(record.FlowID)),
				record.Status,
				repo,
				branch,
				phase,
				issue,
				plan,
				pr,
				updated,
				title,
				width)
		}
		rows = append(rows, truncateToWidth(line, width))
		if record.FlowID == expandedFlowID {
			rows = append(rows, renderFlowPhaseRows(record, width, selectedPhaseID, active, showRepo)...)
		}
	}
	return append([]string{header}, scrollAndPad(rows, scroll, rowHeight)...)
}

func renderFlowPhaseRows(record flowstore.FlowRecord, width int, selectedPhaseID string, active flowTerminalActivitySet, showRepo bool) []string {
	if len(record.Phases) == 0 {
		if showRepo {
			return []string{truncateToWidth(formatFlowColumns(showRepo, flowPhaseRowPrefix(false, false), "", "", "", "No phases", "", "", "", "", ""), width)}
		}
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
		line := formatFlowColumns(showRepo, flowPhaseRowPrefix(false, rowActive),
			statusStyle.Render(fitSessionColumn(phase.Status, flowStatusWidth)),
			"",
			"",
			diffHdrStyle.Render(fitSessionColumn(phase.PhaseID+":"+state, flowPhaseWidth)),
			"",
			"",
			"",
			"",
			stashMsgStyle.Render(title),
		)
		if phase.PhaseID == selectedPhaseID {
			line = renderSelectedFlowColumns(showRepo, selectedFlowPhaseRowPrefix(rowActive),
				phase.Status,
				"",
				"",
				phase.PhaseID+":"+state,
				"",
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

func formatFlowColumns(showRepo bool, prefix, status, repo, branch, phase, issue, plan, pr, updated, title string) string {
	if showRepo {
		return fmt.Sprintf("%s%s  %s  %s  %s  %s  %s  %s  %s  %s",
			prefix,
			fitSessionColumn(status, flowStatusWidth),
			fitSessionColumn(repo, flowRepoWidth),
			fitSessionColumn(branch, flowBranchWidth),
			fitSessionColumn(phase, flowPhaseWidth),
			fitSessionColumn(issue, flowIssueWidth),
			fitSessionColumn(plan, flowPlanWidth),
			fitSessionColumn(pr, flowPRWidth),
			fitSessionColumn(updated, flowUpdatedWidth),
			title,
		)
	}
	return fmt.Sprintf("%s%s  %s  %s  %s  %s  %s  %s  %s",
		prefix,
		fitSessionColumn(status, flowStatusWidth),
		fitSessionColumn(branch, flowBranchWidth),
		fitSessionColumn(phase, flowPhaseWidth),
		fitSessionColumn(issue, flowIssueWidth),
		fitSessionColumn(plan, flowPlanWidth),
		fitSessionColumn(pr, flowPRWidth),
		fitSessionColumn(updated, flowUpdatedWidth),
		title,
	)
}

func renderSelectedFlowColumns(showRepo bool, prefix, status, repo, branch, phase, issue, plan, pr, updated, title string, width int) string {
	line := prefix
	line += selectedStyle.Render(fitSessionColumn(status, flowStatusWidth))
	line += selectedStyle.Render("  ")
	if showRepo {
		line += selectedStyle.Render(fitSessionColumn(repo, flowRepoWidth))
		line += selectedStyle.Render("  ")
	}
	line += selectedStyle.Render(fitSessionColumn(branch, flowBranchWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(phase, flowPhaseWidth))
	line += selectedStyle.Render("  ")
	line += selectedStyle.Render(fitSessionColumn(issue, flowIssueWidth))
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

func repoDisplayNamesByPath(repos []scanner.Repo) map[string]string {
	if len(repos) == 0 {
		return nil
	}
	displayNames := make(map[string]string, len(repos))
	for _, repo := range repos {
		path := strings.TrimSpace(repo.Path)
		name := strings.TrimSpace(repo.DisplayName)
		if path == "" || name == "" {
			continue
		}
		displayNames[filepath.Clean(path)] = name
	}
	return displayNames
}

func flowRepoLabel(record flowstore.FlowRecord, repoDisplayNames map[string]string) string {
	repoPath := strings.TrimSpace(record.RepoPath)
	if repoPath == "" {
		return ""
	}
	cleanRepoPath := filepath.Clean(repoPath)
	if name := repoDisplayNames[cleanRepoPath]; name != "" {
		return name
	}
	return filepath.Base(cleanRepoPath)
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
	if reason, ok := flowstore.RecoverableRunningPhaseResetReason(phase); ok {
		return reason
	}
	if phase.Status == flowstore.PhaseRunning && flowstore.PhaseAwaitingSession(phase) {
		return flowstore.PhaseResetReasonAwaitSession
	}
	if phase.Status == flowstore.PhaseRunning && flowstore.PhaseLatestLaunchEnded(phase) {
		return flowstore.PhaseResetReasonEndedSession
	}
	if session, ok := flowstore.LatestPhaseSession(phase, false); ok && strings.TrimSpace(session.SessionID) == "" {
		return "missing-session-id"
	}
	if flowstore.SemanticKind(phase) == flowstore.KindAutoreview && flowMissingPRTarget(record) && phaseCanReportMissingPR(phase) {
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

func flowIssueLabel(record flowstore.FlowRecord) string {
	if record.Issue.Number > 0 {
		return fmt.Sprintf("#%d", record.Issue.Number)
	}
	if record.Issue.URL != "" {
		return filepath.Base(record.Issue.URL)
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
		if flowstore.SemanticKind(phase) == flowstore.KindPRCreation && phase.Status == flowstore.PhaseCompleted {
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
	panel := renderSelectPanel(selectPanelSpec{
		prompt:   p.SelectPrompt,
		items:    p.SelectItems,
		selected: p.SelectSelected,
		note:     p.SelectNote,
		noteKind: p.SelectNoteKind,
		width:    panelWidth,
		height:   panelHeight,
	})
	x, y := selectPanelPosition(p.Width, bodyHeight, panelWidth, panelHeight, p.SelectPlacement)
	body = compositePanel(body, panel, x, y, p.Width)
	return joinBodyAndStatus(body, statusBar)
}

func renderSelectOverlayStatusBar(p RenderParams) string {
	return renderStatusBarWithState(statusBarParams{
		Width:                  p.Width,
		Mode:                   p.Mode,
		Overlay:                p.Overlay,
		SelectPrompt:           p.SelectPrompt,
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

// selectPanelSpec groups the shared select renderer's inputs so the reserved
// note row can be added without a seventh positional argument.
type selectPanelSpec struct {
	prompt   string
	items    []SelectItem
	selected int
	note     string
	noteKind NoteKind
	width    int
	height   int
}

func renderSelectPanel(spec selectPanelSpec) []string {
	prompt, items, selected := spec.prompt, spec.items, spec.selected
	width, height := spec.width, spec.height
	if width <= 0 || height <= 0 {
		return nil
	}
	if prompt == "" {
		prompt = "Choose"
	}
	if selected < 0 || selected >= len(items) {
		selected = 0
	}
	// A note takes a row only when the panel already has a spare item slot.
	// Designed picker height includes that spare; a clamped panel does not, so
	// the note is dropped instead of hiding an item that would otherwise fit.
	itemRows := height - 3
	if itemRows < 0 {
		itemRows = 0
	}
	noteRows := 0
	if spec.note != "" && height > 3 && itemRows > len(items) {
		noteRows = 1
		itemRows--
	}
	lines := make([]string, 0, height)
	lines = append(lines, selectPanelBorderLine("┌", "─", "┐", width))
	if height > 1 {
		lines = append(lines, selectPanelContentLine(activeModeStyle.Render(prompt), width))
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
	if noteRows == 1 {
		noteWidth := width - 4
		if noteWidth < 1 {
			noteWidth = 1
		}
		note := noteStyle(spec.noteKind).Render(truncateWithEllipsis(spec.note, noteWidth))
		lines = append(lines, selectPanelContentLine(note, width))
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
		PromptEditor:           p.Editor.Enabled,
		FormHasMultiline:       formHasMultilineField(p.Form),
		WorktreeInputPrompt:    inputParams.prompt,
		SelectPrompt:           p.SelectPrompt,
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
		var lines []string
		if p.Editor.Enabled {
			lines = renderPromptEditorDialog(p, p.Width, contentHeight)
		} else {
			lines = renderInputDialog(inputParams, p.Width, contentHeight)
		}
		return strings.Join(lines, "\n") + "\n" + statusBar
	}
	if p.Overlay == OverlayForm {
		lines := renderFormDialog(p.Form, p.Width, contentHeight)
		if p.Form.Purpose == "flow-create" {
			lines = renderFormDialogOverApplication(p, contentHeight)
		}
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

func renderFormDialogOverApplication(p RenderParams, contentHeight int) []string {
	baseParams := p
	baseParams.Overlay = OverlayNone
	base := strings.Split(renderApplication(baseParams), "\n")
	lines := make([]string, contentHeight)
	for i := 0; i < contentHeight && i < len(base); i++ {
		lines[i] = base[i]
	}
	panelLines, x, y := formDialogPanel(p.Form, p.Width, contentHeight)
	return compositePanel(lines, panelLines, x, y, p.Width)
}

func renderConfirmDialog(prompt string, force bool, width, height int) []string {
	prompt = terminalSafeSingleLine(prompt)
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
	panelLines, _, top := formDialogPanel(form, width, height)
	if len(panelLines) == 0 {
		return lines
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

func formDialogPanel(form FormView, width, height int) ([]string, int, int) {
	if width <= 0 || height <= 0 {
		return nil, 0, 0
	}
	panelWidth := formPanelWidth(form, width)
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
	renderedPanelWidth := 0
	for _, line := range panelLines {
		if lineWidth := lipgloss.Width(line); lineWidth > renderedPanelWidth {
			renderedPanelWidth = lineWidth
		}
	}
	x := (width - renderedPanelWidth) / 2
	if x < 0 {
		x = 0
	}
	return panelLines, x, top
}

func formHasMultilineField(form FormView) bool {
	for _, field := range form.Fields {
		if field.Kind == FormMultilineText {
			return true
		}
	}
	return false
}

func formPanelWidth(form FormView, width int) int {
	panelWidth := width - 4
	maxWidth := launchInstructionsMaxWidth
	if form.Purpose == "flow-create" {
		maxWidth = flowCreateFormMaxWidth
	}
	if panelWidth > maxWidth {
		panelWidth = maxWidth
	}
	if panelWidth < launchInstructionsMinWidth {
		panelWidth = width
	}
	if panelWidth < 4 {
		panelWidth = width
	}
	return panelWidth
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
	case FormMultilineText:
		return formMultilineTextFieldLines(field, focused, prefix, label, width)
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

func formMultilineTextFieldLines(field FormField, focused bool, prefix, label string, width int) []string {
	labelLine := prefix + label + ":"
	valuePrefix := strings.Repeat(" ", lipgloss.Width(prefix))
	if field.Value == "" {
		value := placeholderStyle.Render(field.Placeholder)
		if focused {
			value += activeModeStyle.Render("█")
		}
		lines := wrapPlainText(labelLine, width)
		return append(lines, wrapEditableInputLine(valuePrefix+value, width)...)
	}

	value := field.Value
	if focused {
		value = insertCursorGlyph(value, field.Cursor)
	}
	logicalLines := strings.Split(value, "\n")
	lines := make([]string, 0, len(logicalLines))
	for _, line := range logicalLines {
		lines = append(lines, wrapEditableInputLine(valuePrefix+line, width)...)
	}
	cursorLine := lineIndexContainingCursor(lines)
	lines = compactInputDialogLines(lines, flowCreateFormMaxTextLines, cursorLine)
	return append(wrapPlainText(labelLine, width), lines...)
}

type inputRenderParams struct {
	prompt      string
	placeholder string
	value       string
	errText     string
	mode        InputMode
	height      int
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
			height:      p.InputHeight,
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
		height:      p.InputHeight,
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
	maxInputLines := maxInputDialogLines(height, params.errText, contentWidth, params.height)
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
	prompt = terminalSafeSingleLine(prompt)
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

func maxInputDialogLines(height int, errText string, contentWidth int, configuredLines int) int {
	maxLines := launchInstructionsMaxLines
	if configuredLines > 0 {
		maxLines = configuredLines
	}
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
	return renderStyledRow(line, selectedStyle, width)
}

func renderStyledRow(line string, style lipgloss.Style, width int) string {
	line = truncateToWidth(line, width)
	if padding := width - lipgloss.Width(line); padding > 0 {
		line += style.Render(strings.Repeat(" ", padding))
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
