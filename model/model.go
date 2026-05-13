package model

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

// Mode represents the active right-pane view.
type Mode int

const (
	ModeWorktrees Mode = iota + 1
	ModeBranches
	ModeStashes
	ModeHistory
	ModeReflog
)

// OverlayState is an alias so tests can reference model.OverlayNone etc.
type OverlayState = ui.OverlayState

const (
	OverlayNone         = ui.OverlayNone
	OverlayStashDiff    = ui.OverlayStashDiff
	OverlayBranchDiff   = ui.OverlayBranchDiff
	OverlayConfirm      = ui.OverlayConfirm
	OverlayCommitDiff   = ui.OverlayCommitDiff
	OverlayWorktreeDiff = ui.OverlayWorktreeDiff
	OverlayReflogDiff   = ui.OverlayReflogDiff
)

// --- Messages ---

type BranchResultMsg struct {
	RepoPath string
	Branches []gitquery.Branch
}

type StashResultMsg struct {
	RepoPath string
	Stashes  []gitquery.Stash
}

type StashDiffResultMsg struct {
	RepoPath string
	Index    int
	Diff     string
}

type BranchDiffResultMsg struct {
	RepoPath   string
	BranchName string
	Diff       string
}

type BranchDeletedMsg struct {
	RepoPath string
}

type StashDroppedMsg struct {
	RepoPath string
}

type WorktreeResultMsg struct {
	RepoPath  string
	Worktrees []gitquery.Worktree
}

type CommitResultMsg struct {
	RepoPath string
	Commits  []gitquery.Commit
}

type CommitDiffResultMsg struct {
	RepoPath string
	Hash     string
	Diff     string
}

type WorktreeDiffResultMsg struct {
	RepoPath     string
	WorktreePath string
	Diff         string
}

type WorktreeRemovedMsg struct {
	RepoPath   string
	BranchName string // empty if detached
}

type WorktreeDeleteCompletedMsg struct {
	RepoPath string
}

type WorktreePrunedMsg struct {
	RepoPath string
}

type ReflogResultMsg struct {
	RepoPath string
	Reflogs  []gitquery.ReflogEntry
}

type ReflogDiffResultMsg struct {
	RepoPath string
	Hash     string
	Diff     string
}

type ClipboardResultMsg struct{}

type DeleteFailedMsg struct {
	RepoPath    string
	Target      string       // display name (branch name or worktree path)
	ForceAction func() error // the --force variant to call
	SuccessMsg  tea.Msg      // returned after force succeeds; defaults to BranchDeletedMsg
}

// Model is the bubbletea application model.
type Model struct {
	repos            []scanner.Repo
	selected         int
	width            int
	height           int
	mode             Mode
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
	overlay          OverlayState
	overlayDiff      string
	overlayScroll    int
	confirmPrompt    string
	confirmAction    func() tea.Cmd
	confirmForce     bool
	branchScroll     int
	repoScroll       int
	stashScroll      int
	activePane       int // 0=left (repos), 1=right (content)
	destructive      bool
}

// New creates a Model from discovered repos.
func New(repos []scanner.Repo) Model {
	return Model{repos: repos, mode: ModeWorktrees}
}

func (m Model) Selected() int                   { return m.selected }
func (m Model) Width() int                      { return m.width }
func (m Model) Height() int                     { return m.height }
func (m Model) Mode() Mode                      { return m.mode }
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
func (m Model) Overlay() OverlayState           { return m.overlay }
func (m Model) OverlayDiff() string             { return m.overlayDiff }
func (m Model) OverlayScroll() int              { return m.overlayScroll }
func (m Model) ConfirmPrompt() string           { return m.confirmPrompt }
func (m Model) ConfirmForce() bool              { return m.confirmForce }
func (m Model) BranchScroll() int               { return m.branchScroll }
func (m Model) RepoScroll() int                 { return m.repoScroll }
func (m Model) StashScroll() int                { return m.stashScroll }
func (m Model) ActivePane() int                 { return m.activePane }
func (m Model) Destructive() bool               { return m.destructive }

func (m Model) Init() tea.Cmd {
	return m.fetchForMode()
}

func (m Model) View() string {
	return ui.Render(ui.RenderParams{
		Repos:            m.repos,
		Selected:         m.selected,
		Width:            m.width,
		Height:           m.height,
		Mode:             int(m.mode),
		Branches:         m.rows,
		Stashes:          m.stashes,
		BranchSelected:   m.branchSelected,
		StashSelected:    m.stashSelected,
		Overlay:          m.overlay,
		OverlayDiff:      m.overlayDiff,
		OverlayScroll:    m.overlayScroll,
		ConfirmPrompt:    m.confirmPrompt,
		ConfirmForce:     m.confirmForce,
		BranchScroll:     m.branchScroll,
		RepoScroll:       m.repoScroll,
		StashScroll:      m.stashScroll,
		ActivePane:       m.activePane,
		Destructive:      m.destructive,
		Worktrees:        m.worktrees,
		WorktreeSelected: m.worktreeSelected,
		WorktreeScroll:   m.worktreeScroll,
		Commits:          m.commits,
		CommitSelected:   m.commitSelected,
		CommitScroll:     m.commitScroll,
		Reflogs:          m.reflogs,
		ReflogSelected:   m.reflogSelected,
		ReflogScroll:     m.reflogScroll,
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
		m = m.ensureStashVisible()
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
		return m, nil
	case DeleteFailedMsg:
		return m.handleDeleteFailed(msg), nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.overlay == OverlayConfirm {
		return m.handleConfirmKey(key)
	}
	if m.overlay != OverlayNone {
		if m.activePane == 0 {
			return m.handleLeftPaneKey(key)
		}
		return m.handleOverlayKey(key)
	}

	if key == "D" {
		m.destructive = !m.destructive
		return m, nil
	}

	if m.activePane == 0 {
		return m.handleLeftPaneKey(key)
	}
	return m.handleRightPaneKey(key)
}

// --- Key handlers by context ---

func (m Model) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "enter":
		action := m.confirmAction
		m = m.clearConfirm()
		return m, action()
	case "n", "q", "esc":
		m = m.clearConfirm()
	}
	return m, nil
}

func (m Model) handleLeftPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "tab":
		m.activePane = 1
	case "up", "k":
		if len(m.repos) > 0 {
			if m.selected > 0 {
				m.selected--
			} else {
				m.selected = len(m.repos) - 1
			}
			m = m.ensureRepoVisible()
			m = m.resetRightPaneCursors()
			return m, m.fetchForMode()
		}
	case "down", "j":
		if len(m.repos) > 0 {
			if m.selected < len(m.repos)-1 {
				m.selected++
			} else {
				m.selected = 0
			}
			m = m.ensureRepoVisible()
			m = m.resetRightPaneCursors()
			return m, m.fetchForMode()
		}
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleOverlayKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		m.overlay = OverlayNone
		m.overlayDiff = ""
		m.overlayScroll = 0
	case "up", "k":
		if m.overlayScroll > 0 {
			m.overlayScroll--
		}
	case "down", "j":
		m.overlayScroll++
	}
	return m, nil
}

func (m Model) handleRightPaneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "left", "h":
		if m.mode > ModeWorktrees {
			m.mode--
			m.branchSelected = 0
			m.stashSelected = 0
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchForMode()
		}
	case "right", "l":
		if m.mode < ModeReflog {
			m.mode++
			m.branchSelected = 0
			m.stashSelected = 0
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchForMode()
		}
	case "1":
		if m.mode != ModeWorktrees {
			m.mode = ModeWorktrees
			m.branchSelected = 0
			m.stashSelected = 0
			m.worktreeSelected = 0
			m.worktreeScroll = 0
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchWorktrees()
		}
	case "2":
		if m.mode != ModeBranches {
			m.mode = ModeBranches
			m.branchSelected = 0
			m.stashSelected = 0
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchBranches()
		}
	case "3":
		if m.mode != ModeStashes {
			m.mode = ModeStashes
			m.branchSelected = 0
			m.stashSelected = 0
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchStashes()
		}
	case "4":
		if m.mode != ModeHistory {
			m.mode = ModeHistory
			m.commitSelected = 0
			m.commitScroll = 0
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchCommits()
		}
	case "5":
		if m.mode != ModeReflog {
			m.mode = ModeReflog
			m.reflogSelected = 0
			m.reflogScroll = 0
			return m, m.fetchReflog()
		}
	case "y":
		return m.handleCopyHash()
	case "tab":
		m.activePane = 0
	case "enter":
		return m.handleEnter()
	case "d":
		return m.handleDelete()
	case "p":
		return m.handlePrune()
	case "t":
		return m.handleOpenTerminal()
	case "c":
		return m.handleOpenCode()
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	}
	return m, nil
}

// --- Cursor navigation ---

func (m Model) handleCursorUp() (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeWorktrees:
		if len(m.worktrees) > 0 {
			if m.worktreeSelected > 0 {
				m.worktreeSelected--
			} else {
				m.worktreeSelected = len(m.worktrees) - 1
			}
			m = m.ensureWorktreeVisible()
		}
	case ModeBranches:
		if len(m.rows) > 0 {
			if m.branchSelected > 0 {
				m.branchSelected--
			} else {
				m.branchSelected = len(m.rows) - 1
			}
			m = m.ensureBranchVisible()
		}
	case ModeStashes:
		if len(m.stashes) > 0 {
			if m.stashSelected > 0 {
				m.stashSelected--
			} else {
				m.stashSelected = len(m.stashes) - 1
			}
			m = m.ensureStashVisible()
		}
	case ModeHistory:
		if len(m.commits) > 0 {
			if m.commitSelected > 0 {
				m.commitSelected--
			} else {
				m.commitSelected = len(m.commits) - 1
			}
			m = m.ensureCommitVisible()
		}
	case ModeReflog:
		if len(m.reflogs) > 0 {
			if m.reflogSelected > 0 {
				m.reflogSelected--
			} else {
				m.reflogSelected = len(m.reflogs) - 1
			}
			m = m.ensureReflogVisible()
		}
	}
	return m, nil
}

func (m Model) handleCursorDown() (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeWorktrees:
		if len(m.worktrees) > 0 {
			if m.worktreeSelected < len(m.worktrees)-1 {
				m.worktreeSelected++
			} else {
				m.worktreeSelected = 0
			}
			m = m.ensureWorktreeVisible()
		}
	case ModeBranches:
		if len(m.rows) > 0 {
			if m.branchSelected < len(m.rows)-1 {
				m.branchSelected++
			} else {
				m.branchSelected = 0
			}
			m = m.ensureBranchVisible()
		}
	case ModeStashes:
		if len(m.stashes) > 0 {
			if m.stashSelected < len(m.stashes)-1 {
				m.stashSelected++
			} else {
				m.stashSelected = 0
			}
			m = m.ensureStashVisible()
		}
	case ModeHistory:
		if len(m.commits) > 0 {
			if m.commitSelected < len(m.commits)-1 {
				m.commitSelected++
			} else {
				m.commitSelected = 0
			}
			m = m.ensureCommitVisible()
		}
	case ModeReflog:
		if len(m.reflogs) > 0 {
			if m.reflogSelected < len(m.reflogs)-1 {
				m.reflogSelected++
			} else {
				m.reflogSelected = 0
			}
			m = m.ensureReflogVisible()
		}
	}
	return m, nil
}

// --- Action handlers ---

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	if m.mode == ModeWorktrees {
		wt, ok := m.selectedWorktree()
		if ok && wt.Dirty && !wt.Stale {
			m.overlay = OverlayWorktreeDiff
			return m, m.fetchWorktreeDiff()
		}
		return m, nil
	}
	if m.mode == ModeBranches && m.isSelectedBranchDirtyWorktree() {
		m.overlay = OverlayBranchDiff
		return m, m.fetchBranchDiff()
	}
	if m.mode == ModeStashes && len(m.stashes) > 0 {
		m.overlay = OverlayStashDiff
		return m, m.fetchStashDiff()
	}
	if m.mode == ModeHistory && len(m.commits) > 0 {
		m.overlay = OverlayCommitDiff
		return m, m.fetchCommitDiff()
	}
	if m.mode == ModeReflog && len(m.reflogs) > 0 {
		m.overlay = OverlayReflogDiff
		return m, m.fetchReflogDiff()
	}
	return m, nil
}

func (m Model) handleDelete() (tea.Model, tea.Cmd) {
	if !m.destructive {
		return m, nil
	}
	if m.mode == ModeHistory || m.mode == ModeReflog {
		return m, nil
	}
	if m.mode == ModeStashes && len(m.stashes) > 0 && len(m.repos) > 0 {
		return m.confirmStashDrop()
	}
	if m.mode == ModeBranches && len(m.repos) > 0 {
		return m.confirmBranchDelete()
	}
	if m.mode == ModeWorktrees && len(m.worktrees) > 0 && len(m.repos) > 0 {
		return m.confirmWorktreeDelete()
	}
	return m, nil
}

func (m Model) openAtPath(action func(string) error) (tea.Model, tea.Cmd) {
	if m.mode == ModeWorktrees {
		if wt, ok := m.selectedWorktree(); ok && !wt.Stale {
			path := wt.Path
			return m, func() tea.Msg { _ = action(path); return nil }
		}
		return m, nil
	}
	if m.mode == ModeHistory && len(m.repos) > 0 {
		path := m.repos[m.selected].Path
		return m, func() tea.Msg { _ = action(path); return nil }
	}
	if m.mode == ModeBranches && len(m.repos) > 0 {
		if row, ok := m.selectedRow(); ok && row.WorktreePath != "" {
			path := row.WorktreePath
			return m, func() tea.Msg { _ = action(path); return nil }
		}
	}
	return m, nil
}

func (m Model) handleOpenTerminal() (tea.Model, tea.Cmd) {
	return m.openAtPath(actions.OpenTerminal)
}

func (m Model) handleOpenCode() (tea.Model, tea.Cmd) {
	return m.openAtPath(actions.OpenVSCode)
}

// --- Confirm dialogs ---

func (m Model) confirmStashDrop() (tea.Model, tea.Cmd) {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}
	idx := m.stashes[m.stashSelected].Index
	m.confirmPrompt = fmt.Sprintf("Drop stash@{%d}? (y/n)", idx)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			_ = actions.DropStash(repoPath, idx)
			return StashDroppedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = OverlayConfirm
	return m, nil
}

func (m Model) confirmBranchDelete() (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m, nil
	}

	// Root branch cannot be deleted
	if row.WorktreePath == repoPath {
		return m, nil
	}

	branchName := row.Branch.Name
	m.confirmPrompt = fmt.Sprintf("Delete branch %s? (y/n)", branchName)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DeleteBranch(repoPath, branchName); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      branchName,
					ForceAction: func() error { return actions.ForceDeleteBranch(repoPath, branchName) },
				}
			}
			return BranchDeletedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = OverlayConfirm
	return m, nil
}

func (m Model) confirmWorktreeDelete() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	if wt.IsMain {
		return m, nil
	}
	if wt.Locked {
		return m, nil
	}
	if wt.Stale {
		return m, nil
	}

	repoPath, ok2 := m.currentRepoPath()
	if !ok2 {
		return m, nil
	}
	wtPath := wt.Path
	branchName := wt.BranchName
	if wt.Detached {
		branchName = ""
	}

	m.confirmPrompt = fmt.Sprintf("Remove worktree at %s? (y/n)", wtPath)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.RemoveWorktree(repoPath, wtPath); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      wtPath,
					ForceAction: func() error { return actions.ForceRemoveWorktree(repoPath, wtPath) },
					SuccessMsg:  WorktreeRemovedMsg{RepoPath: repoPath, BranchName: branchName},
				}
			}
			return WorktreeRemovedMsg{RepoPath: repoPath, BranchName: branchName}
		}
	}
	m.overlay = OverlayConfirm
	return m, nil
}

func (m Model) handlePrune() (tea.Model, tea.Cmd) {
	if !m.destructive {
		return m, nil
	}
	if m.mode == ModeWorktrees && len(m.worktrees) > 0 && len(m.repos) > 0 {
		return m.confirmWorktreePrune()
	}
	return m, nil
}

func (m Model) confirmWorktreePrune() (tea.Model, tea.Cmd) {
	wt, ok := m.selectedWorktree()
	if !ok {
		return m, nil
	}
	if !wt.Stale {
		return m, nil
	}

	repoPath, ok2 := m.currentRepoPath()
	if !ok2 {
		return m, nil
	}
	m.confirmPrompt = "Prune stale worktrees? (y/n)"
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			_ = actions.PruneWorktree(repoPath)
			return WorktreePrunedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = OverlayConfirm
	return m, nil
}

func (m Model) clearConfirm() Model {
	m.overlay = OverlayNone
	m.confirmPrompt = ""
	m.confirmAction = nil
	m.confirmForce = false
	return m
}

func (m Model) resetRightPaneCursors() Model {
	m.branchSelected = 0
	m.stashSelected = 0
	m.branchScroll = 0
	m.stashScroll = 0
	m.worktreeSelected = 0
	m.worktreeScroll = 0
	m.commitSelected = 0
	m.commitScroll = 0
	m.reflogSelected = 0
	m.reflogScroll = 0
	m.rows = nil
	m.stashes = nil
	m.worktrees = nil
	m.commits = nil
	m.reflogs = nil
	return m
}

// --- Message handlers ---

func (m Model) currentRepoPath() (string, bool) {
	if len(m.repos) == 0 || m.selected >= len(m.repos) {
		return "", false
	}
	return m.repos[m.selected].Path, true
}

func (m Model) isCurrentRepo(repoPath string) bool {
	current, ok := m.currentRepoPath()
	return ok && current == repoPath
}

func (m Model) handleWorktreeResult(msg WorktreeResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.worktrees = msg.Worktrees
		if len(m.worktrees) == 0 || m.worktreeSelected >= len(m.worktrees) {
			m.worktreeSelected = 0
		}
	}
	return m
}

func (m Model) handleWorktreeRemoved(msg WorktreeRemovedMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentRepo(msg.RepoPath) {
		return m, nil
	}
	if m.worktreeSelected >= len(m.worktrees)-1 && m.worktreeSelected > 0 {
		m.worktreeSelected--
	}
	if msg.BranchName == "" {
		return m, m.fetchWorktrees()
	}
	repoPath := msg.RepoPath
	branchName := msg.BranchName
	m.confirmPrompt = fmt.Sprintf("Also delete branch %s? (y/n)", branchName)
	m.confirmAction = func() tea.Cmd {
		return func() tea.Msg {
			if err := actions.DeleteBranch(repoPath, branchName); err != nil {
				return DeleteFailedMsg{
					RepoPath:    repoPath,
					Target:      branchName,
					ForceAction: func() error { return actions.ForceDeleteBranch(repoPath, branchName) },
				}
			}
			return WorktreeDeleteCompletedMsg{RepoPath: repoPath}
		}
	}
	m.overlay = OverlayConfirm
	return m, m.fetchWorktrees()
}

func (m Model) handleWorktreePruned(msg WorktreePrunedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.worktreeSelected >= len(m.worktrees)-1 && m.worktreeSelected > 0 {
			m.worktreeSelected--
		}
		return m, m.fetchWorktrees()
	}
	return m, nil
}

func (m Model) handleBranchResult(msg BranchResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		allRows := gitquery.FlattenBranches(msg.Branches)
		var filtered []gitquery.BranchRow
		for _, row := range allRows {
			if row.Branch.IsWorktree && row.WorktreePath != msg.RepoPath {
				continue
			}
			filtered = append(filtered, row)
		}
		// Pin root worktree to position 0
		for i, row := range filtered {
			if row.WorktreePath == msg.RepoPath {
				if i != 0 {
					root := filtered[i]
					copy(filtered[1:i+1], filtered[:i])
					filtered[0] = root
				}
				break
			}
		}
		m.rows = filtered
		if len(m.rows) == 0 || m.branchSelected >= len(m.rows) {
			m.branchSelected = 0
		}
	}
	return m
}

func (m Model) handleStashResult(msg StashResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.stashes = msg.Stashes
	}
	return m
}

func (m Model) handleStashDiffResult(msg StashDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.overlayDiff = msg.Diff
	}
	return m
}

func (m Model) handleWorktreeDiffResult(msg WorktreeDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if wt, ok := m.selectedWorktree(); ok && wt.Path == msg.WorktreePath {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleBranchDiffResult(msg BranchDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if row, ok := m.selectedRow(); ok && row.Branch.Name == msg.BranchName {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleStashDropped(msg StashDroppedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.stashSelected >= len(m.stashes)-1 && m.stashSelected > 0 {
			m.stashSelected--
		}
		return m, m.fetchStashes()
	}
	return m, nil
}

func (m Model) handleBranchDeleted(msg BranchDeletedMsg) (tea.Model, tea.Cmd) {
	if m.isCurrentRepo(msg.RepoPath) {
		return m, m.fetchBranches()
	}
	return m, nil
}

func (m Model) handleDeleteFailed(msg DeleteFailedMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.confirmPrompt = fmt.Sprintf("Force delete %s? (y/n)", msg.Target)
		m.confirmForce = true
		m.overlay = OverlayConfirm
		successMsg := msg.SuccessMsg
		m.confirmAction = func() tea.Cmd {
			return func() tea.Msg {
				_ = msg.ForceAction()
				if successMsg != nil {
					return successMsg
				}
				return BranchDeletedMsg{RepoPath: msg.RepoPath}
			}
		}
	}
	return m
}

func (m Model) handleCommitResult(msg CommitResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.commits = msg.Commits
		if len(m.commits) == 0 || m.commitSelected >= len(m.commits) {
			m.commitSelected = 0
		}
	}
	return m
}

func (m Model) handleReflogResult(msg ReflogResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		m.reflogs = msg.Reflogs
		if len(m.reflogs) == 0 || m.reflogSelected >= len(m.reflogs) {
			m.reflogSelected = 0
		}
	}
	return m
}

func (m Model) handleCommitDiffResult(msg CommitDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.commitSelected < len(m.commits) && m.commits[m.commitSelected].Hash == msg.Hash {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleReflogDiffResult(msg ReflogDiffResultMsg) Model {
	if m.isCurrentRepo(msg.RepoPath) {
		if m.reflogSelected < len(m.reflogs) && m.reflogs[m.reflogSelected].Hash == msg.Hash {
			m.overlayDiff = msg.Diff
		}
	}
	return m
}

func (m Model) handleCopyHash() (tea.Model, tea.Cmd) {
	var hash string
	switch {
	case m.mode == ModeHistory && len(m.commits) > 0:
		hash = m.commits[m.commitSelected].Hash
	case m.mode == ModeReflog && len(m.reflogs) > 0:
		hash = m.reflogs[m.reflogSelected].Hash
	default:
		return m, nil
	}
	return m, func() tea.Msg {
		_ = actions.CopyToClipboard(hash)
		return ClipboardResultMsg{}
	}
}

// --- Fetch commands ---

func (m Model) fetchForMode() tea.Cmd {
	switch m.mode {
	case ModeWorktrees:
		return m.fetchWorktrees()
	case ModeBranches:
		return m.fetchBranches()
	case ModeStashes:
		return m.fetchStashes()
	case ModeHistory:
		return m.fetchCommits()
	case ModeReflog:
		return m.fetchReflog()
	}
	return nil
}

func (m Model) fetchWorktrees() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktrees, _ := gitquery.ListWorktrees(repoPath)
		return WorktreeResultMsg{RepoPath: repoPath, Worktrees: worktrees}
	}
}

func (m Model) fetchBranches() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		branches, _ := gitquery.ListBranches(repoPath)
		return BranchResultMsg{RepoPath: repoPath, Branches: branches}
	}
}

func (m Model) fetchStashes() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		stashes, _ := gitquery.ListStashes(repoPath)
		return StashResultMsg{RepoPath: repoPath, Stashes: stashes}
	}
}

func (m Model) fetchBranchDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	row, ok := m.selectedRow()
	if !ok || !row.Branch.Dirty || !row.Branch.IsWorktree {
		return nil
	}
	worktreePath := row.WorktreePath
	if worktreePath == "" {
		worktreePath = repoPath
	}
	branchName := row.Branch.Name

	return func() tea.Msg {
		diff, _ := gitquery.BranchDiff(worktreePath)
		return BranchDiffResultMsg{
			RepoPath:   repoPath,
			BranchName: branchName,
			Diff:       diff,
		}
	}
}

func (m Model) fetchWorktreeDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	wt, ok := m.selectedWorktree()
	if !ok || !wt.Dirty || wt.Stale {
		return nil
	}
	worktreePath := wt.Path
	return func() tea.Msg {
		diff, _ := gitquery.BranchDiff(worktreePath)
		return WorktreeDiffResultMsg{
			RepoPath:     repoPath,
			WorktreePath: worktreePath,
			Diff:         diff,
		}
	}
}

func (m Model) fetchStashDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || len(m.stashes) == 0 {
		return nil
	}
	index := m.stashes[m.stashSelected].Index
	return func() tea.Msg {
		diff, _ := gitquery.StashDiff(repoPath, index)
		return StashDiffResultMsg{RepoPath: repoPath, Index: index, Diff: diff}
	}
}

func (m Model) fetchCommits() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		commits, _ := gitquery.ListCommits(repoPath)
		return CommitResultMsg{RepoPath: repoPath, Commits: commits}
	}
}

func (m Model) fetchReflog() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		reflogs, _ := gitquery.ListReflog(repoPath)
		return ReflogResultMsg{RepoPath: repoPath, Reflogs: reflogs}
	}
}

func (m Model) fetchReflogDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || len(m.reflogs) == 0 {
		return nil
	}
	hash := m.reflogs[m.reflogSelected].Hash
	return func() tea.Msg {
		diff, _ := gitquery.ReflogDiff(repoPath, hash)
		return ReflogDiffResultMsg{RepoPath: repoPath, Hash: hash, Diff: diff}
	}
}

func (m Model) fetchCommitDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || len(m.commits) == 0 {
		return nil
	}
	hash := m.commits[m.commitSelected].Hash
	return func() tea.Msg {
		diff, _ := gitquery.CommitDiff(repoPath, hash)
		return CommitDiffResultMsg{RepoPath: repoPath, Hash: hash, Diff: diff}
	}
}

// --- Helpers ---

func (m Model) selectedRow() (gitquery.BranchRow, bool) {
	if m.branchSelected < 0 || m.branchSelected >= len(m.rows) {
		return gitquery.BranchRow{}, false
	}
	return m.rows[m.branchSelected], true
}

func (m Model) selectedWorktree() (gitquery.Worktree, bool) {
	if m.worktreeSelected < 0 || m.worktreeSelected >= len(m.worktrees) {
		return gitquery.Worktree{}, false
	}
	return m.worktrees[m.worktreeSelected], true
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
	for i, s := range m.stashes {
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
	for i, row := range m.rows {
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
