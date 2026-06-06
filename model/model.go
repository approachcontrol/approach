package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/agent"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/model/modal"
	"github.com/brian-bell/wtui/model/pane"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

const listRequestSlots = int(ui.ModeReflog) + 1

// Model is the bubbletea application model.
type Model struct {
	repos                    pane.Pane[scanner.Repo]
	width                    int
	height                   int
	mode                     ui.Mode
	rows                     pane.Pane[gitquery.BranchRow]
	stashes                  pane.Pane[gitquery.Stash]
	worktrees                pane.Pane[gitquery.Worktree]
	commits                  pane.Pane[gitquery.Commit]
	reflogs                  pane.Pane[gitquery.ReflogEntry]
	modal                    modal.Modal
	diffRequestSeq           uint64
	listRequestSeq           uint64
	listRequests             [listRequestSlots]uint64
	activePane               int // 0=left (repos), 1=right (content)
	destructive              bool
	status                   statusError
	searchActive             bool
	pendingBranchSelection   string
	pendingWorktreeSelection string
	agentCommand             string
	saveAgent                func(string) error
	launchAgent              func(string, string) (actions.TerminalLaunchSpec, error)
}

type statusSource int

const (
	statusNone statusSource = iota
	statusFetch
	statusGitMutation
	statusOther
)

type statusError struct {
	Text      string
	Source    statusSource
	FetchKind FetchKind
	Mode      ui.Mode
}

// Options customizes production-only integrations while keeping New(repos)
// simple for tests.
type Options struct {
	AgentCommand     string
	SaveAgentCommand func(string) error
	LaunchAgent      func(string, string) (actions.TerminalLaunchSpec, error)
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return NewWithOptions(repos, Options{})
}

// NewWithOptions creates a Model from discovered repos and startup options.
func NewWithOptions(repos []scanner.Repo, opts Options) Model {
	saveAgent := opts.SaveAgentCommand
	if saveAgent == nil {
		saveAgent = func(string) error { return nil }
	}
	launchAgent := opts.LaunchAgent
	if launchAgent == nil {
		launchAgent = actions.AgentLaunch
	}
	m := Model{
		repos:        newRepoPane().SetItems(repos),
		rows:         newBranchPane(),
		stashes:      newStashPane(),
		worktrees:    newWorktreePane(),
		commits:      newCommitPane(),
		reflogs:      newReflogPane(),
		mode:         ui.ModeWorktrees,
		agentCommand: agent.Normalize(opts.AgentCommand),
		saveAgent:    saveAgent,
		launchAgent:  launchAgent,
	}
	for mode := ui.ModeWorktrees; mode <= ui.ModeReflog; mode++ {
		m.listRequestSeq++
		m.listRequests[int(mode)] = m.listRequestSeq
	}
	return m
}

func (m Model) Selected() int              { return m.repos.SelectedIndex() }
func (m Model) Width() int                 { return m.width }
func (m Model) Height() int                { return m.height }
func (m Model) Mode() ui.Mode              { return m.mode }
func (m Model) Rows() []gitquery.BranchRow { rows, _, _ := m.rows.View(); return rows }
func (m Model) Stashes() []gitquery.Stash  { stashes, _, _ := m.stashes.View(); return stashes }
func (m Model) BranchSelected() int        { return m.rows.SelectedIndex() }
func (m Model) StashSelected() int         { return m.stashes.SelectedIndex() }
func (m Model) Worktrees() []gitquery.Worktree {
	worktrees, _, _ := m.worktrees.View()
	return worktrees
}
func (m Model) WorktreeSelected() int           { return m.worktrees.SelectedIndex() }
func (m Model) WorktreeScroll() int             { return m.worktrees.Scroll() }
func (m Model) Commits() []gitquery.Commit      { commits, _, _ := m.commits.View(); return commits }
func (m Model) CommitSelected() int             { return m.commits.SelectedIndex() }
func (m Model) CommitScroll() int               { return m.commits.Scroll() }
func (m Model) Reflogs() []gitquery.ReflogEntry { reflogs, _, _ := m.reflogs.View(); return reflogs }
func (m Model) ReflogSelected() int             { return m.reflogs.SelectedIndex() }
func (m Model) ReflogScroll() int               { return m.reflogs.Scroll() }
func (m Model) Overlay() ui.OverlayState        { return m.overlayState() }
func (m Model) OverlayDiff() string             { return m.modal.View().Diff }
func (m Model) OverlayScroll() int              { return m.modal.View().Scroll }
func (m Model) ConfirmPrompt() string           { return m.modal.View().Prompt }
func (m Model) ConfirmForce() bool              { return m.modal.View().Force }
func (m Model) WorktreeInput() string           { return m.modal.View().Input }
func (m Model) WorktreeInputErr() string        { return m.modal.View().InputErr }
func (m Model) BranchScroll() int               { return m.rows.Scroll() }
func (m Model) RepoScroll() int                 { return m.repos.Scroll() }
func (m Model) StashScroll() int                { return m.stashes.Scroll() }
func (m Model) ActivePane() int                 { return m.activePane }
func (m Model) Destructive() bool               { return m.destructive }
func (m Model) TransientError() string          { return m.status.Text }
func (m Model) SearchActive() bool              { return m.searchActive }
func (m Model) RepoSearch() string              { return m.repos.Query() }
func (m Model) ItemSearch() string              { return m.activeItemPaneQuery() }
func (m Model) ListRequest(mode ui.Mode) uint64 { return m.currentListRequest(mode) }
func (m Model) AgentCommand() string            { return m.agentCommand }

func (m Model) Init() tea.Cmd {
	return m.fetchForMode()
}

func (m Model) View() string {
	repos, selected, repoScroll := m.repos.View()
	worktrees, worktreeSelected, worktreeScroll := m.worktrees.View()
	rows, branchSelected, branchScroll := m.rows.View()
	stashes, stashSelected, stashScroll := m.stashes.View()
	commits, commitSelected, commitScroll := m.commits.View()
	reflogs, reflogSelected, reflogScroll := m.reflogs.View()
	repoEmptyMessage := m.repoEmptyMessage(len(repos))
	rightEmptyMessage := m.rightEmptyMessage(len(repos), len(worktrees), len(rows), len(stashes), len(commits), len(reflogs))
	if len(repos) == 0 {
		worktrees = nil
		rows = nil
		stashes = nil
		commits = nil
		reflogs = nil
	}
	modalView := m.modal.View()
	return ui.Render(ui.RenderParams{
		Repos:                    repos,
		Selected:                 selected,
		Width:                    m.width,
		Height:                   m.height,
		Mode:                     m.mode,
		Branches:                 rows,
		Stashes:                  stashes,
		BranchSelected:           branchSelected,
		StashSelected:            stashSelected,
		Overlay:                  m.overlayState(),
		OverlayDiff:              modalView.Diff,
		OverlayScroll:            modalView.Scroll,
		ConfirmPrompt:            modalView.Prompt,
		ConfirmForce:             modalView.Force,
		WorktreeInputPrompt:      modalView.Prompt,
		WorktreeInputPlaceholder: modalView.Placeholder,
		WorktreeInput:            modalView.Input,
		WorktreeInputErr:         modalView.InputErr,
		BranchScroll:             branchScroll,
		RepoScroll:               repoScroll,
		StashScroll:              stashScroll,
		ActivePane:               m.activePane,
		Destructive:              m.destructive,
		Worktrees:                worktrees,
		WorktreeSelected:         worktreeSelected,
		WorktreeScroll:           worktreeScroll,
		Commits:                  commits,
		CommitSelected:           commitSelected,
		CommitScroll:             commitScroll,
		Reflogs:                  reflogs,
		ReflogSelected:           reflogSelected,
		ReflogScroll:             reflogScroll,
		TransientError:           m.status.Text,
		SearchActive:             m.searchActive,
		RepoSearch:               m.repos.Query(),
		ItemSearch:               m.activeItemPaneQuery(),
		RepoEmptyMessage:         repoEmptyMessage,
		RightEmptyMessage:        rightEmptyMessage,
		FetchAvailable:           m.canFetch(),
		PullAvailable:            m.canPull(),
		WorktreeMoveAvailable:    m.canMoveWorktree(),
		AgentAvailable:           m.canLaunchAgent(),
		NewAgentAvailable:        m.canCreateAndLaunchAgent(),
	})
}

func (m Model) repoEmptyMessage(filteredRepos int) string {
	if filteredRepos > 0 {
		return ""
	}
	itemCount := m.repos.ItemCount()
	if m.repos.Query() != "" && itemCount > 0 {
		return "No repo results for " + m.repos.Query()
	}
	if itemCount == 0 {
		return "No repositories found"
	}
	return "No repo results"
}

func (m Model) rightEmptyMessage(filteredRepos, filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs int) string {
	if filteredRepos == 0 {
		if m.repos.Query() != "" && m.repos.ItemCount() > 0 {
			return "No matching repo"
		}
		return "No selected repo"
	}
	sourceCount, filteredCount := m.activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs)
	if m.activeItemPaneQuery() != "" && sourceCount > 0 && filteredCount == 0 {
		return "No " + modeResultName(m.mode) + " results for " + m.activeItemPaneQuery()
	}
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList && m.status.Mode == m.mode {
		return "Could not load " + modeDataName(m.mode) + "; see status bar"
	}
	return modeEmptyMessage(m.mode)
}

func (m Model) activeItemCounts(filteredWorktrees, filteredBranches, filteredStashes, filteredCommits, filteredReflogs int) (int, int) {
	switch m.mode {
	case ui.ModeWorktrees:
		return m.worktrees.ItemCount(), filteredWorktrees
	case ui.ModeBranches:
		return m.rows.ItemCount(), filteredBranches
	case ui.ModeStashes:
		return m.stashes.ItemCount(), filteredStashes
	case ui.ModeHistory:
		return m.commits.ItemCount(), filteredCommits
	case ui.ModeReflog:
		return m.reflogs.ItemCount(), filteredReflogs
	default:
		return 0, 0
	}
}

func modeDataName(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "worktrees"
	case ui.ModeBranches:
		return "branches"
	case ui.ModeStashes:
		return "stashes"
	case ui.ModeHistory:
		return "commits"
	case ui.ModeReflog:
		return "reflog"
	default:
		return "items"
	}
}

func modeResultName(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "worktree"
	case ui.ModeBranches:
		return "branch"
	case ui.ModeStashes:
		return "stash"
	case ui.ModeHistory:
		return "commit"
	case ui.ModeReflog:
		return "reflog"
	default:
		return "item"
	}
}

func modeEmptyMessage(mode ui.Mode) string {
	switch mode {
	case ui.ModeWorktrees:
		return "No worktrees to show"
	case ui.ModeBranches:
		return "No branches to show"
	case ui.ModeStashes:
		return "No stashes"
	case ui.ModeHistory:
		return "No commits"
	case ui.ModeReflog:
		return "No reflog entries"
	default:
		return "nothing here yet"
	}
}

func (m Model) overlayState() ui.OverlayState {
	view := m.modal.View()
	switch view.Kind {
	case modal.Confirm:
		return ui.OverlayConfirm
	case modal.Input:
		return ui.OverlayWorktreeInput
	case modal.Diff:
		switch view.DiffKind {
		case modal.DiffStash:
			return ui.OverlayStashDiff
		case modal.DiffBranch:
			return ui.OverlayBranchDiff
		case modal.DiffCommit:
			return ui.OverlayCommitDiff
		case modal.DiffWorktree:
			return ui.OverlayWorktreeDiff
		case modal.DiffReflog:
			return ui.OverlayReflogDiff
		}
	}
	return ui.OverlayNone
}

func (m Model) openDiff(kind modal.DiffKind) Model {
	m.diffRequestSeq++
	m.modal = modal.OpenDiff(kind, "").WithRequest(m.diffRequestSeq)
	return m
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.clampSelectionsAfterFilter()
	case BranchResultMsg:
		return m.handleBranchResult(msg), nil
	case StashResultMsg:
		return m.handleStashResult(msg), nil
	case StashDiffResultMsg:
		return m.handleStashDiffResult(msg), nil
	case BranchDiffResultMsg:
		return m.handleBranchDiffResult(msg), nil
	case StashDroppedMsg:
		return m.handleStashDropped(msg)
	case BranchDeletedMsg:
		return m.handleBranchDeleted(msg)
	case BranchCreatedMsg:
		return m.handleBranchCreated(msg)
	case BranchCreateFailedMsg:
		return m.handleBranchCreateFailed(msg), nil
	case WorktreeResultMsg:
		return m.handleWorktreeResult(msg), nil
	case WorktreeRemovedMsg:
		return m.handleWorktreeRemoved(msg)
	case WorktreeDeleteCompletedMsg:
		return m, nil
	case WorktreePrunedMsg:
		return m.handleWorktreePruned(msg)
	case WorktreeUnlockedMsg:
		return m.handleWorktreeUnlocked(msg)
	case WorktreeUnlockFailedMsg:
		return m.handleWorktreeUnlockFailed(msg), nil
	case GitFetchedMsg:
		return m.handleGitFetched(msg)
	case GitFetchFailedMsg:
		return m.handleGitFetchFailed(msg), nil
	case GitPulledMsg:
		return m.handleGitPulled(msg)
	case GitPullFailedMsg:
		return m.handleGitPullFailed(msg), nil
	case WorktreeCreatedMsg:
		return m.handleWorktreeCreated(msg)
	case WorktreeCreateFailedMsg:
		return m.handleWorktreeCreateFailed(msg), nil
	case WorktreeMovedMsg:
		return m.handleWorktreeMoved(msg)
	case WorktreeMoveFailedMsg:
		return m.handleWorktreeMoveFailed(msg), nil
	case CommitResultMsg:
		return m.handleCommitResult(msg), nil
	case ReflogResultMsg:
		return m.handleReflogResult(msg), nil
	case WorktreeDiffResultMsg:
		return m.handleWorktreeDiffResult(msg), nil
	case CommitDiffResultMsg:
		return m.handleCommitDiffResult(msg), nil
	case ReflogDiffResultMsg:
		return m.handleReflogDiffResult(msg), nil
	case ClipboardResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case TerminalResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case AgentSetMsg:
		return m.handleAgentSet(msg), nil
	case AgentSetFailedMsg:
		return m.handleAgentSetFailed(msg), nil
	case AgentResultMsg:
		if msg.Err != "" {
			m = m.setStatus(statusOther, msg.Err)
		}
		return m, nil
	case DeleteFailedMsg:
		return m.handleDeleteFailed(msg), nil
	case ForceDeleteFailedMsg:
		return m.handleForceDeleteFailed(msg), nil
	case FetchErrorMsg:
		return m.handleFetchError(msg), nil
	case ActionFailedMsg:
		return m.handleActionFailed(msg), nil
	}
	return m, nil
}

// --- Helpers ---

func (m Model) selectedRow() (gitquery.BranchRow, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.BranchRow{}, false
	}
	return m.rows.Selected()
}

func (m Model) selectedWorktree() (gitquery.Worktree, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Worktree{}, false
	}
	return m.worktrees.Selected()
}

func (m Model) selectedStash() (gitquery.Stash, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Stash{}, false
	}
	return m.stashes.Selected()
}

func (m Model) selectedCommit() (gitquery.Commit, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Commit{}, false
	}
	return m.commits.Selected()
}

func (m Model) selectedReflog() (gitquery.ReflogEntry, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.ReflogEntry{}, false
	}
	return m.reflogs.Selected()
}

func (m Model) isSelectedBranchDirtyWorktree() bool {
	row, ok := m.selectedRow()
	return ok && row.Branch.Dirty && row.Branch.IsWorktree
}

func (m Model) reflowStashes() Model {
	contentHeight := m.height - ui.StashContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	rightContentWidth := m.width - ui.LeftPaneWidth - 2
	m.stashes = m.stashes.Reflow(contentHeight, rightContentWidth)
	return m
}

func (m Model) reflowRepos() Model {
	contentHeight := m.height - ui.RepoContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	m.repos = m.repos.Reflow(contentHeight, ui.LeftPaneWidth-2)
	return m
}

func (m Model) reflowWorktrees() Model {
	contentHeight := m.height - ui.WorktreeContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.worktrees = m.worktrees.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowReflogs() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.reflogs = m.reflogs.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowCommits() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.commits = m.commits.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) reflowBranches() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	m.rows = m.rows.Reflow(contentHeight, m.contentWidth())
	return m
}

func (m Model) contentWidth() int {
	width := m.width - ui.LeftPaneWidth - 2
	if width < 0 {
		return 0
	}
	return width
}
