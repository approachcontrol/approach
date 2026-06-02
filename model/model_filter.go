package model

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/brian-bell/wtui/gitquery"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

func (m Model) activeSearchQuery() string {
	if m.activePane == 0 {
		return m.repoSearch
	}
	return m.itemSearch
}

func (m Model) setActiveSearchQuery(query string) Model {
	if m.activePane == 0 {
		m.repoSearch = query
		m.selected = 0
		m.repoScroll = 0
		return m
	}

	m.itemSearch = query
	switch m.mode {
	case ui.ModeWorktrees:
		m.worktreeSelected = 0
		m.worktreeScroll = 0
	case ui.ModeBranches:
		m.branchSelected = 0
		m.branchScroll = 0
	case ui.ModeStashes:
		m.stashSelected = 0
		m.stashScroll = 0
	case ui.ModeHistory:
		m.commitSelected = 0
		m.commitScroll = 0
	case ui.ModeReflog:
		m.reflogSelected = 0
		m.reflogScroll = 0
	}
	return m
}

func (m Model) clampSelectionsAfterFilter() Model {
	repos := m.filteredRepos()
	if len(repos) == 0 {
		m.selected = 0
		m.repoScroll = 0
	} else if m.selected >= len(repos) {
		m.selected = len(repos) - 1
	}

	m.worktreeSelected = clampIndex(m.worktreeSelected, len(m.filteredWorktrees()))
	m.branchSelected = clampIndex(m.branchSelected, len(m.filteredRows()))
	m.stashSelected = clampIndex(m.stashSelected, len(m.filteredStashes()))
	m.commitSelected = clampIndex(m.commitSelected, len(m.filteredCommits()))
	m.reflogSelected = clampIndex(m.reflogSelected, len(m.filteredReflogs()))

	m = m.ensureRepoVisible()
	m = m.ensureWorktreeVisible()
	m = m.ensureBranchVisible()
	m = m.ensureStashVisible()
	m = m.ensureCommitVisible()
	m = m.ensureReflogVisible()
	return m
}

func (m Model) selectFilteredRepo(repoPath string) Model {
	for i, repo := range m.filteredRepos() {
		if repo.Path == repoPath {
			m.selected = i
			return m
		}
	}
	return m
}

func clampIndex(index, length int) int {
	if length <= 0 || index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func (m Model) filteredRepos() []scanner.Repo {
	if m.repoSearch == "" {
		return m.repos
	}
	var filtered []scanner.Repo
	for _, repo := range m.repos {
		if fuzzyMatch(m.repoSearch, repo.DisplayName+" "+repo.Path) {
			filtered = append(filtered, repo)
		}
	}
	return filtered
}

func (m Model) filteredRows() []gitquery.BranchRow {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	if m.itemSearch == "" {
		return m.rows
	}
	var filtered []gitquery.BranchRow
	for _, row := range m.rows {
		if fuzzyMatch(m.itemSearch, branchSearchText(row)) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m Model) filteredStashes() []gitquery.Stash {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	if m.itemSearch == "" {
		return m.stashes
	}
	var filtered []gitquery.Stash
	for _, stash := range m.stashes {
		if fuzzyMatch(m.itemSearch, fmt.Sprintf("stash@{%d} %s %s", stash.Index, stash.Date, stash.Message)) {
			filtered = append(filtered, stash)
		}
	}
	return filtered
}

func (m Model) filteredWorktrees() []gitquery.Worktree {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	if m.itemSearch == "" {
		return m.worktrees
	}
	var filtered []gitquery.Worktree
	for _, wt := range m.worktrees {
		if fuzzyMatch(m.itemSearch, strings.Join([]string{wt.BranchName, wt.Path, wt.LockReason}, " ")) {
			filtered = append(filtered, wt)
		}
	}
	return filtered
}

func (m Model) filteredCommits() []gitquery.Commit {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	if m.itemSearch == "" {
		return m.commits
	}
	var filtered []gitquery.Commit
	for _, commit := range m.commits {
		if fuzzyMatch(m.itemSearch, strings.Join([]string{commit.Hash, commit.Author, commit.Date, commit.Subject}, " ")) {
			filtered = append(filtered, commit)
		}
	}
	return filtered
}

func (m Model) filteredReflogs() []gitquery.ReflogEntry {
	if len(m.filteredRepos()) == 0 {
		return nil
	}
	if m.itemSearch == "" {
		return m.reflogs
	}
	var filtered []gitquery.ReflogEntry
	for _, entry := range m.reflogs {
		if fuzzyMatch(m.itemSearch, strings.Join([]string{entry.Hash, entry.Selector, entry.Date, entry.Subject}, " ")) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func branchSearchText(row gitquery.BranchRow) string {
	parts := []string{row.Branch.Name, row.WorktreePath}
	parts = append(parts, row.Branch.Unpushed...)
	return strings.Join(parts, " ")
}

func fuzzyMatch(query, target string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}

	if fuzzyMatchRunes(query, target) {
		return true
	}

	tokens := strings.FieldsFunc(query, func(r rune) bool { return unicode.IsSpace(r) })
	if len(tokens) <= 1 {
		return false
	}
	for _, token := range tokens {
		if !fuzzyMatchRunes(token, target) {
			return false
		}
	}
	return true
}

func fuzzyMatchRunes(query, target string) bool {
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(target))
	next := 0
	for _, r := range t {
		if next < len(q) && q[next] == r {
			next++
		}
	}
	return next == len(q)
}
