package gitquery

import (
	"fmt"
	"os"
	"path/filepath"
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
	Commit       string
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
	FullRef       string
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

// ListWorktrees returns non-bare worktree checkouts for the given repo.
// A checkout whose path equals repoPath is marked as the root worktree.
func ListWorktrees(repoPath string) ([]Worktree, error) {
	return defaultQuery().ListWorktrees(repoPath)
}

// CurrentBranch returns the checked-out branch for path, or an empty string
// when the worktree is detached.
func CurrentBranch(path string) (string, error) {
	return defaultQuery().CurrentBranch(path)
}

func (q *Querier) CurrentBranch(path string) (string, error) {
	out, err := q.git.Run(path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ListWorktrees returns non-bare worktree checkouts for the given repo.
// Bare roots are omitted, so a central bare repository can return zero rows.
func (q *Querier) ListWorktrees(repoPath string) ([]Worktree, error) {
	out, err := q.git.Run(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	for _, wt := range ParseWorktreeList(out) {
		if wt.IsBare {
			continue
		}

		w := Worktree{
			Path:       wt.Path,
			Commit:     wt.Commit,
			Detached:   wt.Detached,
			IsMain:     samePath(wt.Path, repoPath),
			Locked:     wt.Locked,
			LockReason: wt.LockReason,
		}
		if wt.Detached {
			w.BranchName = ""
		} else {
			w.BranchName = wt.Branch
		}
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
			q.populateWorktreeDirtyStatus(&worktrees[i])
		}
	}

	return worktrees, nil
}

// readDirtyStatus inspects a worktree path and reports how many files are
// changed along with the number of lines added and deleted relative to HEAD.
// A path with no changes yields zero counts and a nil error.
func (q *Querier) readDirtyStatus(path string) (files, added, deleted int, err error) {
	statusOut, err := q.git.Run(path, "status", "--porcelain")
	if err != nil {
		return 0, 0, 0, err
	}
	statusLines := splitLines(statusOut)
	if len(statusLines) == 0 {
		return 0, 0, 0, nil
	}
	files = len(statusLines)

	diffOut, err := q.git.Run(path, "diff", "HEAD", "--numstat")
	if err != nil {
		return files, 0, 0, err
	}
	added, deleted = ParseNumstat(diffOut)
	return files, added, deleted, nil
}

func (q *Querier) populateWorktreeDirtyStatus(wt *Worktree) {
	files, added, deleted, _ := q.readDirtyStatus(wt.Path)
	if files == 0 {
		return
	}
	wt.Dirty = true
	wt.FilesChanged = files
	wt.LinesAdded = added
	wt.LinesDeleted = deleted
}

// ListCommits returns the most recent 50 commits for the given repo path.
func ListCommits(repoPath string) ([]Commit, error) {
	return defaultQuery().ListCommits(repoPath)
}

// ListCommits returns the most recent 50 commits for the given repo path.
func (q *Querier) ListCommits(repoPath string) ([]Commit, error) {
	text, err := q.git.Run(repoPath, "log", "--format=%h%x00%an%x00%ar%x00%s", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}
	return ParseCommitLog(text), nil
}

// ListReflog returns the most recent 50 HEAD reflog entries for the given repo path.
func ListReflog(repoPath string) ([]ReflogEntry, error) {
	return defaultQuery().ListReflog(repoPath)
}

// ListReflog returns the most recent 50 HEAD reflog entries for the given repo path.
func (q *Querier) ListReflog(repoPath string) ([]ReflogEntry, error) {
	text, err := q.git.Run(repoPath, "reflog", "--format=%h%x00%gd%x00%ar%x00%gs", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing reflog: %w", err)
	}
	return ParseReflog(text), nil
}

// ReflogDiff returns the diff for a reflog entry by running git diff <hash>^ <hash>.
// Falls back to git show <hash> for root commits where <hash>^ doesn't exist.
func ReflogDiff(repoPath string, hash string) (string, error) {
	return defaultQuery().ReflogDiff(repoPath, hash)
}

// ReflogDiff returns the diff for a reflog entry by running git diff <hash>^ <hash>.
// Falls back to git show <hash> for root commits where <hash>^ doesn't exist.
func (q *Querier) ReflogDiff(repoPath string, hash string) (string, error) {
	out, err := q.git.Run(repoPath, "diff", hash+"^", hash)
	if err != nil {
		// Root commit has no parent — fall back to git show
		out, err = q.git.Run(repoPath, "show", hash)
		if err != nil {
			return "", fmt.Errorf("reflog diff for %s: %w", hash, err)
		}
	}
	return out, nil
}

// CommitDiff returns the full git show output for a specific commit.
func CommitDiff(repoPath string, hash string) (string, error) {
	return defaultQuery().CommitDiff(repoPath, hash)
}

// CommitDiff returns the full git show output for a specific commit.
func (q *Querier) CommitDiff(repoPath string, hash string) (string, error) {
	out, err := q.git.Run(repoPath, "show", hash)
	if err != nil {
		return "", fmt.Errorf("commit diff for %s: %w", hash, err)
	}
	return out, nil
}

// ListStashes returns stash entries for the given repo path.
func ListStashes(repoPath string) ([]Stash, error) {
	return defaultQuery().ListStashes(repoPath)
}

// ListStashes returns stash entries for the given repo path.
func (q *Querier) ListStashes(repoPath string) ([]Stash, error) {
	text, err := q.git.Run(repoPath, "stash", "list", "--format=%gd%x00%ai%x00%s")
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %w", err)
	}
	return ParseStashList(text), nil
}

// StashDiff returns the diff for a specific stash entry.
func StashDiff(repoPath string, index int) (string, error) {
	return defaultQuery().StashDiff(repoPath, index)
}

// StashDiff returns the diff for a specific stash entry.
func (q *Querier) StashDiff(repoPath string, index int) (string, error) {
	ref := fmt.Sprintf("stash@{%d}", index)
	out, err := q.git.Run(repoPath, "stash", "show", "-p", ref)
	if err != nil {
		return "", fmt.Errorf("stash diff for %s: %w", ref, err)
	}
	return out, nil
}

const refFormat = "%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)"

// ListBranches returns all local branches sorted alphabetically by name.
func ListBranches(repoPath string) ([]Branch, error) {
	return defaultQuery().ListBranches(repoPath)
}

// ListBranches returns all local branches sorted alphabetically by name.
func (q *Querier) ListBranches(repoPath string) ([]Branch, error) {
	out, err := q.git.Run(repoPath, "for-each-ref", "--format="+refFormat, "refs/heads/")
	if err != nil {
		return nil, err
	}

	wtMap, detachedPaths, err := q.branchWorktreeMap(repoPath)
	if err != nil {
		return nil, err
	}

	lines := splitLines(out)
	rootBranch := q.rootWorktreeBranch(repoPath, wtMap)
	cleanupBranch := q.defaultCleanupBranch(repoPath, lines, rootBranch)

	branches := make([]Branch, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		b, upstream := ParseBranchLine(line)

		if b.HasUpstream && !b.UpstreamGone {
			ahead, behind, err := q.branchAheadBehind(repoPath, b.Name, upstream)
			if err == nil {
				b.Ahead = ahead
				b.Behind = behind
			}
			if b.Ahead > 0 {
				b.Unpushed = q.unpushedCommits(repoPath, b.Name, upstream)
			}
		}

		if wtPaths, ok := wtMap[b.Name]; ok {
			b.IsWorktree = true
			b.WorktreePaths = wtPaths
			b.WorktreeStale = checkStale(wtPaths)
			q.populateDirtyStatus(&b, wtPaths)
		}
		// Do not mark the user's active root worktree branch as a cleanup
		// candidate, even when it is technically an ancestor of cleanupBranch.
		if cleanupBranch != "" && b.Name != cleanupBranch && b.Name != rootBranch && q.branchMergedInto(repoPath, b.Name, cleanupBranch) {
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
		q.populateDirtyStatus(&b, b.WorktreePaths)
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
	return defaultQuery().BranchDiff(worktreePath)
}

// BranchDiff returns the diff output for a worktree.
func (q *Querier) BranchDiff(worktreePath string) (string, error) {
	return q.git.Run(worktreePath, "diff", "HEAD")
}

// branchWorktreeMap returns a map of branch name -> worktree paths and detached worktree paths.
func (q *Querier) branchWorktreeMap(repoPath string) (map[string][]string, []string, error) {
	out, err := q.git.Run(repoPath, "worktree", "list", "--porcelain")
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

func (q *Querier) branchAheadBehind(repoPath, branchName, upstream string) (int, int, error) {
	out, err := q.git.Run(repoPath, "rev-list", "--count", "--left-right", branchName+"..."+upstream)
	if err != nil {
		return 0, 0, err
	}
	ahead, behind := ParseAheadBehind(out)
	return ahead, behind, nil
}

func (q *Querier) rootWorktreeBranch(repoPath string, wtMap map[string][]string) string {
	for branch, paths := range wtMap {
		for _, path := range paths {
			if samePath(path, repoPath) {
				return branch
			}
		}
	}
	return strings.TrimSpace(q.maybeGitCmd(repoPath, "branch", "--show-current"))
}

func (q *Querier) defaultCleanupBranch(repoPath string, branchLines []string, fallback string) string {
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
	ref := strings.TrimSpace(q.maybeGitCmd(repoPath, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"))
	ref = strings.TrimPrefix(ref, "origin/")
	if branches[ref] {
		return ref
	}
	return ""
}

func (q *Querier) branchMergedInto(repoPath, branchName, cleanupBranch string) bool {
	return q.git.Ok(repoPath, "merge-base", "--is-ancestor", branchName, cleanupBranch) == nil
}

func (q *Querier) unpushedCommits(repoPath, branchName, upstream string) []string {
	out, err := q.git.Run(repoPath, "log", "--oneline", upstream+".."+branchName)
	if err != nil {
		return nil
	}
	return splitLines(out)
}

func (q *Querier) populateDirtyStatus(b *Branch, paths []string) {
	for _, path := range paths {
		files, added, deleted, _ := q.readDirtyStatus(path)
		if files == 0 {
			continue
		}
		b.Dirty = true
		b.FilesChanged += files
		b.LinesAdded += added
		b.LinesDeleted += deleted
	}
}

func checkStale(paths []string) []bool {
	stale := make([]bool, len(paths))
	for i, p := range paths {
		// Treat any stat failure (missing, permission denied, etc.) as stale.
		// Being conservative avoids falsely reporting an inaccessible-but-existing
		// worktree as healthy.
		if _, err := os.Stat(p); err != nil {
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

func samePath(a, b string) bool {
	return canonicalPath(a) == canonicalPath(b)
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func (q *Querier) maybeGitCmd(dir string, args ...string) string {
	out, err := q.git.Run(dir, args...)
	if err != nil {
		return ""
	}
	return out
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
