package gitquery

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Stash represents a single git stash entry.
type Stash struct {
	Index   int
	Date    string
	Message string
}

// Commit represents a single git commit entry.
type Commit struct {
	Hash    string
	Author  string
	Date    string
	Subject string
}

// ReflogEntry represents a single HEAD reflog entry.
type ReflogEntry struct {
	Hash     string
	Selector string
	Date     string
	Subject  string
}

// Worktree represents a single git worktree checkout.
type Worktree struct {
	Path         string
	BranchName   string
	Detached     bool
	Stale        bool
	IsMain       bool
	Locked       bool
	LockReason   string
	Dirty        bool
	FilesChanged int
	LinesAdded   int
	LinesDeleted int
}

// Branch represents a local git branch with its status.
type Branch struct {
	Name          string
	HasUpstream   bool
	UpstreamGone  bool
	Ahead         int
	Behind        int
	Unpushed      []string
	Merged        bool
	MergedInto    string
	IsWorktree    bool
	WorktreePaths []string
	WorktreeStale []bool // parallel to WorktreePaths; true when directory is missing
	Dirty         bool
	FilesChanged  int
	LinesAdded    int
	LinesDeleted  int
}

// BranchRow is one display row in the branch pane.
// A branch with N worktree paths expands into N rows.
type BranchRow struct {
	Branch       Branch
	WorktreePath string // specific path for this row; empty for non-worktree branches
	IsExpansion  bool   // true for 2nd+ rows of a multi-worktree branch
	Stale        bool   // true when the worktree directory no longer exists on disk
}

// FlattenBranches converts a branch list into display rows,
// expanding multi-worktree branches into one row per path.
func FlattenBranches(branches []Branch) []BranchRow {
	var rows []BranchRow
	for _, b := range branches {
		if len(b.WorktreePaths) == 0 {
			rows = append(rows, BranchRow{Branch: b})
			continue
		}
		for i, p := range b.WorktreePaths {
			stale := i < len(b.WorktreeStale) && b.WorktreeStale[i]
			rows = append(rows, BranchRow{Branch: b, WorktreePath: p, IsExpansion: i > 0, Stale: stale})
		}
	}
	return rows
}

// ListWorktrees returns all worktrees for the given repo, with the main worktree first.
func ListWorktrees(repoPath string) ([]Worktree, error) {
	out, err := gitCmd(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	first := true
	for _, wt := range ParseWorktreeList(out) {
		if wt.IsBare {
			continue
		}

		w := Worktree{
			Path:       wt.Path,
			Detached:   wt.Detached,
			IsMain:     first,
			Locked:     wt.Locked,
			LockReason: wt.LockReason,
		}
		if wt.Detached {
			w.BranchName = ""
		} else {
			w.BranchName = wt.Branch
		}
		first = false
		worktrees = append(worktrees, w)
	}

	paths := make([]string, len(worktrees))
	for i := range worktrees {
		paths[i] = worktrees[i].Path
	}
	staleFlags := checkStale(paths)
	for i := range worktrees {
		worktrees[i].Stale = staleFlags[i]
		if !worktrees[i].Stale {
			populateWorktreeDirtyStatus(&worktrees[i])
		}
	}

	return worktrees, nil
}

func populateWorktreeDirtyStatus(wt *Worktree) {
	statusOut, err := gitCmd(wt.Path, "status", "--porcelain")
	if err != nil {
		return
	}
	statusLines := splitLines(statusOut)
	if len(statusLines) == 0 {
		return
	}
	wt.Dirty = true
	wt.FilesChanged = len(statusLines)

	diffOut, err := gitCmd(wt.Path, "diff", "HEAD", "--numstat")
	if err != nil {
		return
	}
	wt.LinesAdded, wt.LinesDeleted = ParseNumstat(diffOut)
}

// ListCommits returns the most recent 50 commits for the given repo path.
func ListCommits(repoPath string) ([]Commit, error) {
	text, err := gitCmd(repoPath, "log", "--format=%h%x00%an%x00%ar%x00%s", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}
	return ParseCommitLog(text), nil
}

// ListReflog returns the most recent 50 HEAD reflog entries for the given repo path.
func ListReflog(repoPath string) ([]ReflogEntry, error) {
	text, err := gitCmd(repoPath, "reflog", "--format=%h%x00%gd%x00%ar%x00%gs", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing reflog: %w", err)
	}
	return ParseReflog(text), nil
}

// ReflogDiff returns the diff for a reflog entry by running git diff <hash>^ <hash>.
// Falls back to git show <hash> for root commits where <hash>^ doesn't exist.
func ReflogDiff(repoPath string, hash string) (string, error) {
	out, err := gitCmd(repoPath, "diff", hash+"^", hash)
	if err != nil {
		// Root commit has no parent — fall back to git show
		out, err = gitCmd(repoPath, "show", hash)
		if err != nil {
			return "", fmt.Errorf("reflog diff for %s: %w", hash, err)
		}
	}
	return out, nil
}

// CommitDiff returns the full git show output for a specific commit.
func CommitDiff(repoPath string, hash string) (string, error) {
	out, err := gitCmd(repoPath, "show", hash)
	if err != nil {
		return "", fmt.Errorf("commit diff for %s: %w", hash, err)
	}
	return out, nil
}

// ListStashes returns stash entries for the given repo path.
func ListStashes(repoPath string) ([]Stash, error) {
	text, err := gitCmd(repoPath, "stash", "list", "--format=%gd%x00%ai%x00%s")
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %w", err)
	}
	return ParseStashList(text), nil
}

// StashDiff returns the diff for a specific stash entry.
func StashDiff(repoPath string, index int) (string, error) {
	ref := fmt.Sprintf("stash@{%d}", index)
	out, err := gitCmd(repoPath, "stash", "show", "-p", ref)
	if err != nil {
		return "", fmt.Errorf("stash diff for %s: %w", ref, err)
	}
	return out, nil
}

const refFormat = "%(refname:short)\t%(upstream)\t%(upstream:track)"

// ListBranches returns all local branches sorted alphabetically by name.
func ListBranches(repoPath string) ([]Branch, error) {
	out, err := gitCmd(repoPath, "for-each-ref", "--format="+refFormat, "refs/heads/")
	if err != nil {
		return nil, err
	}

	wtMap, detachedPaths, err := branchWorktreeMap(repoPath)
	if err != nil {
		return nil, err
	}

	lines := splitLines(out)
	rootBranch := rootWorktreeBranch(repoPath, wtMap)
	cleanupBranch := defaultCleanupBranch(repoPath, lines, rootBranch)

	branches := make([]Branch, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		b, upstream := ParseBranchLine(line)

		if b.HasUpstream && !b.UpstreamGone {
			ahead, behind, err := branchAheadBehind(repoPath, b.Name, upstream)
			if err == nil {
				b.Ahead = ahead
				b.Behind = behind
			}
			if b.Ahead > 0 {
				b.Unpushed = unpushedCommits(repoPath, b.Name, upstream)
			}
		}

		if wtPaths, ok := wtMap[b.Name]; ok {
			b.IsWorktree = true
			b.WorktreePaths = wtPaths
			b.WorktreeStale = checkStale(wtPaths)
			populateDirtyStatus(&b, wtPaths)
		}
		// Do not mark the user's active root worktree branch as a cleanup
		// candidate, even when it is technically an ancestor of cleanupBranch.
		if cleanupBranch != "" && b.Name != cleanupBranch && b.Name != rootBranch && branchMergedInto(repoPath, b.Name, cleanupBranch) {
			b.Merged = true
			b.MergedInto = cleanupBranch
		}

		branches = append(branches, b)
	}

	for _, path := range detachedPaths {
		b := Branch{
			Name:          "(detached)",
			IsWorktree:    true,
			WorktreePaths: []string{path},
			WorktreeStale: checkStale([]string{path}),
		}
		populateDirtyStatus(&b, b.WorktreePaths)
		branches = append(branches, b)
	}

	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Name != branches[j].Name {
			return branches[i].Name < branches[j].Name
		}
		return firstWorktreePath(branches[i].WorktreePaths) < firstWorktreePath(branches[j].WorktreePaths)
	})

	return branches, nil
}

// BranchDiff returns the diff output for a worktree.
func BranchDiff(worktreePath string) (string, error) {
	return gitCmd(worktreePath, "diff", "HEAD")
}

// branchWorktreeMap returns a map of branch name -> worktree paths and detached worktree paths.
func branchWorktreeMap(repoPath string) (map[string][]string, []string, error) {
	out, err := gitCmd(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, err
	}

	m := make(map[string][]string)
	var detachedPaths []string
	for _, wt := range ParseWorktreeList(out) {
		if wt.IsBare {
			continue
		}
		if wt.Detached {
			detachedPaths = append(detachedPaths, wt.Path)
			continue
		}
		if wt.Branch != "" {
			m[wt.Branch] = append(m[wt.Branch], wt.Path)
		}
	}
	return m, detachedPaths, nil
}

func branchAheadBehind(repoPath, branchName, upstream string) (int, int, error) {
	out, err := gitCmd(repoPath, "rev-list", "--count", "--left-right", branchName+"..."+upstream)
	if err != nil {
		return 0, 0, err
	}
	ahead, behind := ParseAheadBehind(out)
	return ahead, behind, nil
}

func rootWorktreeBranch(repoPath string, wtMap map[string][]string) string {
	for branch, paths := range wtMap {
		for _, path := range paths {
			if path == repoPath {
				return branch
			}
		}
	}
	return strings.TrimSpace(maybeGitCmd(repoPath, "branch", "--show-current"))
}

func defaultCleanupBranch(repoPath string, branchLines []string, fallback string) string {
	branches := make(map[string]bool, len(branchLines))
	for _, line := range branchLines {
		b, _ := ParseBranchLine(line)
		if b.Name != "" {
			branches[b.Name] = true
		}
	}
	for _, name := range []string{"main", "master"} {
		if branches[name] {
			return name
		}
	}
	// Repos without main/master fall back to the root worktree branch, treating
	// branches already merged into that active branch as cleanup candidates.
	if fallback != "" && branches[fallback] {
		return fallback
	}
	ref := strings.TrimSpace(maybeGitCmd(repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"))
	ref = strings.TrimPrefix(ref, "origin/")
	if branches[ref] {
		return ref
	}
	return ""
}

func branchMergedInto(repoPath, branchName, cleanupBranch string) bool {
	return gitCmdRun(repoPath, "merge-base", "--is-ancestor", branchName, cleanupBranch) == nil
}

func unpushedCommits(repoPath, branchName, upstream string) []string {
	out, err := gitCmd(repoPath, "log", "--oneline", upstream+".."+branchName)
	if err != nil {
		return nil
	}
	return splitLines(out)
}

func populateDirtyStatus(b *Branch, paths []string) {
	for _, path := range paths {
		statusOut, err := gitCmd(path, "status", "--porcelain")
		if err != nil {
			continue
		}
		statusLines := splitLines(statusOut)
		if len(statusLines) == 0 {
			continue
		}
		b.Dirty = true
		b.FilesChanged += len(statusLines)

		diffOut, err := gitCmd(path, "diff", "HEAD", "--numstat")
		if err != nil {
			continue
		}
		a, d := ParseNumstat(diffOut)
		b.LinesAdded += a
		b.LinesDeleted += d
	}
}

func checkStale(paths []string) []bool {
	stale := make([]bool, len(paths))
	for i, p := range paths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			stale[i] = true
		}
	}
	return stale
}

func firstWorktreePath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func maybeGitCmd(dir string, args ...string) string {
	out, err := gitCmd(dir, args...)
	if err != nil {
		return ""
	}
	return out
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func gitCmdRun(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
