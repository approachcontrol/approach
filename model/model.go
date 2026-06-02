package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

// Model is the bubbletea application model.
type Model struct {
	repos            []scanner.Repo
	selected         int
	width            int
	height           int
	mode             ui.Mode
	rows             []gitquery.BranchRow
	stashes          []gitquery.Stash
	branchSelected   int
	stashSelected    int
	worktrees        []gitquery.Worktree
	worktreeSelected int
	worktreeScroll   int
	commits          []gitquery.Commit
	commitSelected   int
	commitScroll     int
	reflogs          []gitquery.ReflogEntry
	reflogSelected   int
	reflogScroll     int
	overlay          ui.OverlayState
	overlayDiff      string
	overlayScroll    int
	confirmPrompt    string
	confirmAction    func() tea.Cmd
	confirmForce     bool
	worktreeInput    string
	worktreeInputErr string
	branchScroll     int
	repoScroll       int
	stashScroll      int
	activePane       int // 0=left (repos), 1=right (content)
	destructive      bool
	transientError   string
	searchActive     bool
	repoSearch       string
	itemSearch       string
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return Model{repos: repos, mode: ui.ModeWorktrees}
}

func (m Model) Selected() int                   { return m.selected }
func (m Model) Width() int                      { return m.width }
func (m Model) Height() int                     { return m.height }
func (m Model) Mode() ui.Mode                   { return m.mode }
func (m Model) Rows() []gitquery.BranchRow      { return m.rows }
func (m Model) Stashes() []gitquery.Stash       { return m.stashes }
func (m Model) BranchSelected() int             { return m.branchSelected }
func (m Model) StashSelected() int              { return m.stashSelected }
func (m Model) Worktrees() []gitquery.Worktree  { return m.worktrees }
func (m Model) WorktreeSelected() int           { return m.worktreeSelected }
func (m Model) WorktreeScroll() int             { return m.worktreeScroll }
func (m Model) Commits() []gitquery.Commit      { return m.commits }
func (m Model) CommitSelected() int             { return m.commitSelected }
func (m Model) CommitScroll() int               { return m.commitScroll }
func (m Model) Reflogs() []gitquery.ReflogEntry { return m.reflogs }
func (m Model) ReflogSelected() int             { return m.reflogSelected }
func (m Model) ReflogScroll() int               { return m.reflogScroll }
func (m Model) Overlay() ui.OverlayState        { return m.overlay }
func (m Model) OverlayDiff() string             { return m.overlayDiff }
func (m Model) OverlayScroll() int              { return m.overlayScroll }
func (m Model) ConfirmPrompt() string           { return m.confirmPrompt }
func (m Model) ConfirmForce() bool              { return m.confirmForce }
func (m Model) WorktreeInput() string           { return m.worktreeInput }
func (m Model) WorktreeInputErr() string        { return m.worktreeInputErr }
func (m Model) BranchScroll() int               { return m.branchScroll }
func (m Model) RepoScroll() int                 { return m.repoScroll }
func (m Model) StashScroll() int                { return m.stashScroll }
func (m Model) ActivePane() int                 { return m.activePane }
func (m Model) Destructive() bool               { return m.destructive }
func (m Model) TransientError() string          { return m.transientError }
func (m Model) SearchActive() bool              { return m.searchActive }
func (m Model) RepoSearch() string              { return m.repoSearch }
func (m Model) ItemSearch() string              { return m.itemSearch }

func (m Model) Init() tea.Cmd {
	return m.fetchForMode()
}

func (m Model) View() string {
	repos := m.filteredRepos()
	worktrees := m.filteredWorktrees()
	rows := m.filteredRows()
	stashes := m.filteredStashes()
	commits := m.filteredCommits()
	reflogs := m.filteredReflogs()
	if len(repos) == 0 {
		worktrees = nil
		rows = nil
		stashes = nil
		commits = nil
		reflogs = nil
	}
	return ui.Render(ui.RenderParams{
		Repos:            repos,
		Selected:         m.selected,
		Width:            m.width,
		Height:           m.height,
		Mode:             m.mode,
		Branches:         rows,
		Stashes:          stashes,
		BranchSelected:   m.branchSelected,
		StashSelected:    m.stashSelected,
		Overlay:          m.overlay,
		OverlayDiff:      m.overlayDiff,
		OverlayScroll:    m.overlayScroll,
		ConfirmPrompt:    m.confirmPrompt,
		ConfirmForce:     m.confirmForce,
		WorktreeInput:    m.worktreeInput,
		WorktreeInputErr: m.worktreeInputErr,
		BranchScroll:     m.branchScroll,
		RepoScroll:       m.repoScroll,
		StashScroll:      m.stashScroll,
		ActivePane:       m.activePane,
		Destructive:      m.destructive,
		Worktrees:        worktrees,
		WorktreeSelected: m.worktreeSelected,
		WorktreeScroll:   m.worktreeScroll,
		Commits:          commits,
		CommitSelected:   m.commitSelected,
		CommitScroll:     m.commitScroll,
		Reflogs:          reflogs,
		ReflogSelected:   m.reflogSelected,
		ReflogScroll:     m.reflogScroll,
		TransientError:   m.transientError,
		SearchActive:     m.searchActive,
		RepoSearch:       m.repoSearch,
		ItemSearch:       m.itemSearch,
		FetchAvailable:   m.canFetch(),
		PullAvailable:    m.canPull(),
	})
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.ensureRepoVisible()
		m = m.ensureBranchVisible()
		m = m.ensureStashVisible()
		m = m.ensureWorktreeVisible()
		m = m.ensureCommitVisible()
		m = m.ensureReflogVisible()
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
			m.transientError = msg.Err
		}
		return m, nil
	case TerminalResultMsg:
		if msg.Err != "" {
			m.transientError = msg.Err
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
	rows := m.filteredRows()
	if m.branchSelected < 0 || m.branchSelected >= len(rows) {
		return gitquery.BranchRow{}, false
	}
	return rows[m.branchSelected], true
}

func (m Model) selectedWorktree() (gitquery.Worktree, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Worktree{}, false
	}
	worktrees := m.filteredWorktrees()
	if m.worktreeSelected < 0 || m.worktreeSelected >= len(worktrees) {
		return gitquery.Worktree{}, false
	}
	return worktrees[m.worktreeSelected], true
}

func (m Model) selectedStash() (gitquery.Stash, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Stash{}, false
	}
	stashes := m.filteredStashes()
	if m.stashSelected < 0 || m.stashSelected >= len(stashes) {
		return gitquery.Stash{}, false
	}
	return stashes[m.stashSelected], true
}

func (m Model) selectedCommit() (gitquery.Commit, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.Commit{}, false
	}
	commits := m.filteredCommits()
	if m.commitSelected < 0 || m.commitSelected >= len(commits) {
		return gitquery.Commit{}, false
	}
	return commits[m.commitSelected], true
}

func (m Model) selectedReflog() (gitquery.ReflogEntry, bool) {
	if _, ok := m.currentRepoPath(); !ok {
		return gitquery.ReflogEntry{}, false
	}
	reflogs := m.filteredReflogs()
	if m.reflogSelected < 0 || m.reflogSelected >= len(reflogs) {
		return gitquery.ReflogEntry{}, false
	}
	return reflogs[m.reflogSelected], true
}

func (m Model) isSelectedBranchDirtyWorktree() bool {
	row, ok := m.selectedRow()
	return ok && row.Branch.Dirty && row.Branch.IsWorktree
}

func (m Model) ensureStashVisible() Model {
	contentHeight := m.height - ui.StashContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	rightContentWidth := m.width - ui.LeftPaneWidth - 2
	line := 0
	for i, s := range m.filteredStashes() {
		if i == m.stashSelected {
			break
		}
		line += ui.StashLineCount(s.Message, rightContentWidth)
	}
	if m.stashScroll > line {
		m.stashScroll = line
	}
	if line >= m.stashScroll+contentHeight {
		m.stashScroll = line - contentHeight + 1
	}
	return m
}

func (m Model) ensureRepoVisible() Model {
	contentHeight := m.height - ui.RepoContentOverhead
	if contentHeight <= 0 {
		contentHeight = 1
	}
	if len(m.filteredRepos()) == 0 {
		m.repoScroll = 0
		return m
	}
	if m.repoScroll > m.selected {
		m.repoScroll = m.selected
	}
	if m.selected >= m.repoScroll+contentHeight {
		m.repoScroll = m.selected - contentHeight + 1
	}
	return m
}

func (m Model) ensureWorktreeVisible() Model {
	contentHeight := m.height - ui.WorktreeContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	if len(m.filteredWorktrees()) == 0 {
		m.worktreeScroll = 0
		return m
	}
	if m.worktreeScroll > m.worktreeSelected {
		m.worktreeScroll = m.worktreeSelected
	}
	if m.worktreeSelected >= m.worktreeScroll+contentHeight {
		m.worktreeScroll = m.worktreeSelected - contentHeight + 1
	}
	return m
}

func (m Model) ensureReflogVisible() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	if len(m.filteredReflogs()) == 0 {
		m.reflogScroll = 0
		return m
	}
	if m.reflogScroll > m.reflogSelected {
		m.reflogScroll = m.reflogSelected
	}
	if m.reflogSelected >= m.reflogScroll+contentHeight {
		m.reflogScroll = m.reflogSelected - contentHeight + 1
	}
	return m
}

func (m Model) ensureCommitVisible() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	if len(m.filteredCommits()) == 0 {
		m.commitScroll = 0
		return m
	}
	if m.commitScroll > m.commitSelected {
		m.commitScroll = m.commitSelected
	}
	if m.commitSelected >= m.commitScroll+contentHeight {
		m.commitScroll = m.commitSelected - contentHeight + 1
	}
	return m
}

func (m Model) ensureBranchVisible() Model {
	contentHeight := m.height - ui.BranchContentOverhead
	if contentHeight <= 0 {
		contentHeight = 16
	}
	line := 0
	for i, row := range m.filteredRows() {
		if i == m.branchSelected {
			break
		}
		line++
		if !row.IsExpansion {
			n := len(row.Branch.Unpushed)
			if n > 5 {
				line += 6
			} else {
				line += n
			}
		}
	}
	if m.branchScroll > line {
		m.branchScroll = line
	}
	if line >= m.branchScroll+contentHeight {
		m.branchScroll = line - contentHeight + 1
	}
	return m
}
