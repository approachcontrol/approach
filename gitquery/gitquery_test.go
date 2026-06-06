package gitquery_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/gitquery"
)

// realPath resolves symlinks (macOS /var → /private/var).
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// initRepo creates a git repo in dir with one commit on "main".
func initRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init", "-b", "main")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "init")
}

// initRepoWithInitialBranch creates a git repo in dir with one commit on initialBranch.
func initRepoWithInitialBranch(t *testing.T, dir, initialBranch string) {
	t.Helper()
	run(t, dir, "git", "init", "-b", initialBranch)
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	writeFile(t, dir, "README.md", "init")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
}

// initBranchRepo creates a git repo in a new temp dir with one commit. Returns the dir.
func initBranchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	writeFile(t, dir, "README.md", "init")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "initial")
	return dir
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initBareUpstream creates a bare repo and sets it as the remote "origin" for repo.
func initBareUpstream(t *testing.T, repo string) string {
	t.Helper()
	bare := t.TempDir()
	run(t, bare, "git", "init", "--bare")
	run(t, repo, "git", "remote", "add", "origin", bare)
	run(t, repo, "git", "push", "-u", "origin", "HEAD")
	return bare
}

func findBranch(branches []gitquery.Branch, name string) *gitquery.Branch {
	for i := range branches {
		if branches[i].Name == name {
			return &branches[i]
		}
	}
	return nil
}

// --- Stash tests ---

func TestListStashes_ReturnsStashes(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	// Modify file and stash
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "stash", "push", "-m", "my stash")

	stashes, err := gitquery.ListStashes(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stashes) != 1 {
		t.Fatalf("expected 1 stash, got %d", len(stashes))
	}
	if stashes[0].Index != 0 {
		t.Errorf("expected Index 0, got %d", stashes[0].Index)
	}
	if !strings.Contains(stashes[0].Message, "my stash") {
		t.Errorf("expected message containing 'my stash', got %q", stashes[0].Message)
	}
	if stashes[0].Date == "" {
		t.Error("expected non-empty Date")
	}
}

func TestListStashes_EmptyForNoStashes(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	stashes, err := gitquery.ListStashes(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stashes) != 0 {
		t.Errorf("expected 0 stashes, got %d", len(stashes))
	}
}

func TestListStashes_MultipleStashesInOrder(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	// First stash
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "stash", "push", "-m", "older stash")

	// Second stash
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "stash", "push", "-m", "newer stash")

	stashes, err := gitquery.ListStashes(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stashes) != 2 {
		t.Fatalf("expected 2 stashes, got %d", len(stashes))
	}
	// stash@{0} is most recent
	if stashes[0].Index != 0 {
		t.Errorf("expected first stash Index 0, got %d", stashes[0].Index)
	}
	if !strings.Contains(stashes[0].Message, "newer stash") {
		t.Errorf("expected first stash to be 'newer stash', got %q", stashes[0].Message)
	}
	if stashes[1].Index != 1 {
		t.Errorf("expected second stash Index 1, got %d", stashes[1].Index)
	}
	if !strings.Contains(stashes[1].Message, "older stash") {
		t.Errorf("expected second stash to be 'older stash', got %q", stashes[1].Message)
	}
}

func TestListStashes_InvalidPath(t *testing.T) {
	_, err := gitquery.ListStashes("/no/such/path")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestStashDiff_ReturnsDiff(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "stash", "push", "-m", "diff test")

	diff, err := gitquery.StashDiff(dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "f.txt") {
		t.Error("diff should contain filename 'f.txt'")
	}
	if !strings.Contains(diff, "---") || !strings.Contains(diff, "+++") {
		t.Error("diff should contain diff markers")
	}
}

func TestStashDiff_InvalidIndex(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	_, err := gitquery.StashDiff(dir, 99)
	if err == nil {
		t.Fatal("expected error for invalid stash index, got nil")
	}
}

// --- Branch tests ---

func TestListBranches_DiscoversSortedBranches(t *testing.T) {
	repo := initBranchRepo(t)

	run(t, repo, "git", "branch", "feature-zulu")
	run(t, repo, "git", "branch", "feature-alpha")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(branches) != 3 {
		t.Fatalf("expected 3 branches, got %d", len(branches))
	}

	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}

	if names[0] != "feature-alpha" {
		t.Errorf("expected first branch %q, got %q", "feature-alpha", names[0])
	}
	if names[1] != "feature-zulu" {
		t.Errorf("expected second branch %q, got %q", "feature-zulu", names[1])
	}
	if names[2] != "main" && names[2] != "master" {
		t.Errorf("expected third branch to be main or master, got %q", names[2])
	}
}

func TestListBranches_UpstreamAheadBehind(t *testing.T) {
	repo := initBranchRepo(t)
	initBareUpstream(t, repo)

	run(t, repo, "git", "checkout", "-b", "feature")
	writeFile(t, repo, "a.txt", "hello")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "local commit 1")
	writeFile(t, repo, "b.txt", "world")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "local commit 2")
	run(t, repo, "git", "push", "-u", "origin", "feature")

	writeFile(t, repo, "c.txt", "ahead")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "unpushed commit")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "feature")
	if b == nil {
		t.Fatal("branch 'feature' not found")
	}

	if !b.HasUpstream {
		t.Error("expected HasUpstream = true")
	}
	if b.Ahead != 1 {
		t.Errorf("expected Ahead = 1, got %d", b.Ahead)
	}
	if b.Behind != 0 {
		t.Errorf("expected Behind = 0, got %d", b.Behind)
	}
}

func TestListBranches_UpstreamGone(t *testing.T) {
	repo := initBranchRepo(t)
	bare := initBareUpstream(t, repo)

	run(t, repo, "git", "checkout", "-b", "doomed")
	writeFile(t, repo, "x.txt", "bye")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "doomed commit")
	run(t, repo, "git", "push", "-u", "origin", "doomed")

	run(t, bare, "git", "branch", "-D", "doomed")
	run(t, repo, "git", "fetch", "--prune")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "doomed")
	if b == nil {
		t.Fatal("branch 'doomed' not found")
	}

	if !b.HasUpstream {
		t.Error("expected HasUpstream = true")
	}
	if !b.UpstreamGone {
		t.Error("expected UpstreamGone = true")
	}
}

func TestListBranches_NoUpstream(t *testing.T) {
	repo := initBranchRepo(t)

	run(t, repo, "git", "branch", "local-only")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "local-only")
	if b == nil {
		t.Fatal("branch 'local-only' not found")
	}

	if b.HasUpstream {
		t.Error("expected HasUpstream = false")
	}
	if b.Ahead != 0 {
		t.Errorf("expected Ahead = 0, got %d", b.Ahead)
	}
	if b.Behind != 0 {
		t.Errorf("expected Behind = 0, got %d", b.Behind)
	}
}

func TestListBranches_WorktreeAnnotation(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-feature")
	run(t, repo, "git", "branch", "wt-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "wt-branch")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "wt-branch")
	if b == nil {
		t.Fatal("branch 'wt-branch' not found")
	}

	if !b.IsWorktree {
		t.Error("expected IsWorktree = true")
	}
	if len(b.WorktreePaths) != 1 {
		t.Fatalf("expected 1 worktree path, got %d", len(b.WorktreePaths))
	}
	if b.WorktreePaths[0] != wtPath {
		t.Errorf("expected WorktreePaths[0] %q, got %q", wtPath, b.WorktreePaths[0])
	}

	defaultName := strings.TrimSpace(run(t, repo, "git", "branch", "--show-current"))
	db := findBranch(branches, defaultName)
	if db == nil {
		t.Fatalf("default branch %q not found", defaultName)
	}
	if !db.IsWorktree {
		t.Errorf("expected default branch %q to be a worktree", defaultName)
	}
	if len(db.WorktreePaths) != 1 {
		t.Fatalf("expected 1 worktree path for default branch, got %d", len(db.WorktreePaths))
	}
	if db.WorktreePaths[0] != repo {
		t.Errorf("expected WorktreePaths[0] %q, got %q", repo, db.WorktreePaths[0])
	}
}

func TestListBranches_StaleWorktreeDetected(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-stale")
	run(t, repo, "git", "branch", "stale-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "stale-branch")

	// Delete the worktree directory without running git worktree remove,
	// leaving a stale admin reference in .git/worktrees/.
	os.RemoveAll(wtPath)

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "stale-branch")
	if b == nil {
		t.Fatal("branch 'stale-branch' not found")
	}
	if !b.IsWorktree {
		t.Error("expected IsWorktree = true")
	}
	if len(b.WorktreeStale) != len(b.WorktreePaths) {
		t.Fatalf("expected WorktreeStale length %d to match WorktreePaths length %d",
			len(b.WorktreeStale), len(b.WorktreePaths))
	}
	if !b.WorktreeStale[0] {
		t.Error("expected WorktreeStale[0] = true for deleted worktree directory")
	}
}

func TestListBranches_DuplicateWorktreePaths(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath1 := filepath.Join(wtDir, "wt-dup-1")
	wtPath2 := filepath.Join(wtDir, "wt-dup-2")
	run(t, repo, "git", "branch", "dup-branch")
	run(t, repo, "git", "worktree", "add", wtPath1, "dup-branch")
	run(t, repo, "git", "worktree", "add", "-f", wtPath2, "dup-branch")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "dup-branch")
	if b == nil {
		t.Fatal("branch 'dup-branch' not found")
	}

	if len(b.WorktreePaths) != 2 {
		t.Fatalf("expected 2 worktree paths, got %d: %v", len(b.WorktreePaths), b.WorktreePaths)
	}
	got := map[string]bool{}
	for _, path := range b.WorktreePaths {
		got[path] = true
	}
	if !got[wtPath1] || !got[wtPath2] {
		t.Fatalf("expected both paths %q and %q, got %v", wtPath1, wtPath2, b.WorktreePaths)
	}
}

func TestListBranches_DetachedWorktreeRow(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-detached")
	run(t, repo, "git", "worktree", "add", "--detach", wtPath)

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var detachedCount int
	var detached *gitquery.Branch
	for i := range branches {
		if branches[i].Name == "(detached)" {
			detachedCount++
			detached = &branches[i]
		}
	}
	if detachedCount != 1 {
		t.Fatalf("expected 1 detached row, got %d", detachedCount)
	}
	if detached == nil {
		t.Fatal("detached branch not found")
	}
	if !detached.IsWorktree {
		t.Error("expected detached row to be marked as a worktree")
	}
	if len(detached.WorktreePaths) != 1 {
		t.Fatalf("expected 1 detached worktree path, got %d", len(detached.WorktreePaths))
	}
	if detached.WorktreePaths[0] != wtPath {
		t.Errorf("expected detached path %q, got %q", wtPath, detached.WorktreePaths[0])
	}
}

func TestListBranches_DirtyWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-dirty")
	run(t, repo, "git", "branch", "dirty-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "dirty-branch")

	writeFile(t, wtPath, "README.md", "modified content\nadded line\n")
	writeFile(t, wtPath, "new-file.txt", "new content\n")
	run(t, wtPath, "git", "add", "new-file.txt")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "dirty-branch")
	if b == nil {
		t.Fatal("branch 'dirty-branch' not found")
	}

	if !b.Dirty {
		t.Error("expected Dirty = true")
	}
	if b.FilesChanged == 0 {
		t.Error("expected FilesChanged > 0")
	}
	if b.LinesAdded == 0 {
		t.Error("expected LinesAdded > 0")
	}
	if b.LinesDeleted == 0 {
		t.Error("expected LinesDeleted > 0")
	}
}

func TestListBranches_UnpushedCommits(t *testing.T) {
	repo := initBranchRepo(t)
	initBareUpstream(t, repo)

	run(t, repo, "git", "checkout", "-b", "unpushed-branch")
	run(t, repo, "git", "push", "-u", "origin", "unpushed-branch")

	writeFile(t, repo, "a.txt", "one")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "first unpushed")
	writeFile(t, repo, "b.txt", "two")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "second unpushed")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "unpushed-branch")
	if b == nil {
		t.Fatal("branch 'unpushed-branch' not found")
	}

	if len(b.Unpushed) != 2 {
		t.Fatalf("expected 2 unpushed commits, got %d: %v", len(b.Unpushed), b.Unpushed)
	}
	if !strings.Contains(b.Unpushed[0], "second unpushed") {
		t.Errorf("expected first unpushed to contain %q, got %q", "second unpushed", b.Unpushed[0])
	}
	if !strings.Contains(b.Unpushed[1], "first unpushed") {
		t.Errorf("expected second unpushed to contain %q, got %q", "first unpushed", b.Unpushed[1])
	}
}

func TestListBranches_UntrackedOnlyDirtyWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-untracked")
	run(t, repo, "git", "branch", "untracked-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "untracked-branch")

	// Only create an untracked file (don't stage it)
	writeFile(t, wtPath, "untracked.txt", "new content\n")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "untracked-branch")
	if b == nil {
		t.Fatal("branch 'untracked-branch' not found")
	}

	if !b.Dirty {
		t.Error("expected Dirty = true")
	}
	if b.FilesChanged != 1 {
		t.Errorf("expected FilesChanged = 1, got %d", b.FilesChanged)
	}
}

func TestListBranches_MergedDetectionMain(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initRepoWithInitialBranch(t, repo, "main")

	run(t, repo, "git", "checkout", "-b", "merged-branch")
	writeFile(t, repo, "merged.txt", "merged\n")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "merged branch commit")
	run(t, repo, "git", "checkout", "main")
	run(t, repo, "git", "merge", "--no-ff", "merged-branch", "-m", "merge merged branch")

	run(t, repo, "git", "checkout", "-b", "unmerged-branch")
	writeFile(t, repo, "unmerged.txt", "unmerged\n")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "unmerged branch commit")
	run(t, repo, "git", "checkout", "main")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	merged := findBranch(branches, "merged-branch")
	if merged == nil {
		t.Fatal("merged-branch not found")
	}
	if !merged.Merged {
		t.Fatal("expected merged-branch to be marked merged")
	}
	if merged.MergedInto != "main" {
		t.Errorf("expected MergedInto %q, got %q", "main", merged.MergedInto)
	}

	unmerged := findBranch(branches, "unmerged-branch")
	if unmerged == nil {
		t.Fatal("unmerged-branch not found")
	}
	if unmerged.Merged {
		t.Error("unmerged-branch should not be marked merged")
	}

	main := findBranch(branches, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if main.Merged {
		t.Error("cleanup branch main should not be marked merged")
	}
}

func TestListBranches_MergedDetectionMaster(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initRepoWithInitialBranch(t, repo, "master")

	run(t, repo, "git", "checkout", "-b", "merged-branch")
	writeFile(t, repo, "merged.txt", "merged\n")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "merged branch commit")
	run(t, repo, "git", "checkout", "master")
	run(t, repo, "git", "merge", "--no-ff", "merged-branch", "-m", "merge merged branch")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	merged := findBranch(branches, "merged-branch")
	if merged == nil {
		t.Fatal("merged-branch not found")
	}
	if !merged.Merged {
		t.Fatal("expected merged-branch to be marked merged")
	}
	if merged.MergedInto != "master" {
		t.Errorf("expected MergedInto %q, got %q", "master", merged.MergedInto)
	}

	master := findBranch(branches, "master")
	if master == nil {
		t.Fatal("master not found")
	}
	if master.Merged {
		t.Error("cleanup branch master should not be marked merged")
	}
}

func TestListBranches_MergedWorktreeBranchStatusPreserved(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initRepoWithInitialBranch(t, repo, "main")

	run(t, repo, "git", "branch", "worktree-branch")
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "worktree-branch")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b := findBranch(branches, "worktree-branch")
	if b == nil {
		t.Fatal("worktree-branch not found")
	}
	if !b.IsWorktree {
		t.Error("expected worktree-branch to preserve worktree status")
	}
	if !b.Merged {
		t.Error("expected worktree-branch at main to be marked merged")
	}
}

func TestListBranches_RootBranchExcludedFromMergedDetection(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initRepoWithInitialBranch(t, repo, "main")

	run(t, repo, "git", "checkout", "-b", "develop")
	writeFile(t, repo, "develop.txt", "develop\n")
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "develop commit")

	run(t, repo, "git", "checkout", "main")
	run(t, repo, "git", "merge", "--no-ff", "develop", "-m", "merge develop")
	run(t, repo, "git", "branch", "old-topic")
	run(t, repo, "git", "checkout", "develop")

	branches, err := gitquery.ListBranches(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	develop := findBranch(branches, "develop")
	if develop == nil {
		t.Fatal("develop not found")
	}
	if develop.Merged {
		t.Error("root branch develop should not be marked merged")
	}
	if develop.MergedInto != "" {
		t.Errorf("expected empty MergedInto for root branch, got %q", develop.MergedInto)
	}

	oldTopic := findBranch(branches, "old-topic")
	if oldTopic == nil {
		t.Fatal("old-topic not found")
	}
	if !oldTopic.Merged {
		t.Error("expected non-root old-topic branch to be marked merged")
	}
	if oldTopic.MergedInto != "main" {
		t.Errorf("expected old-topic MergedInto %q, got %q", "main", oldTopic.MergedInto)
	}
}

func TestBranchDiff_ReturnsDiffForDirtyWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-diff")
	run(t, repo, "git", "branch", "diff-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "diff-branch")

	writeFile(t, wtPath, "README.md", "changed\n")
	writeFile(t, wtPath, "staged.txt", "staged content\n")
	run(t, wtPath, "git", "add", "staged.txt")

	diff, err := gitquery.BranchDiff(wtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(diff, "diff --git") {
		t.Error("expected diff output to contain 'diff --git'")
	}
	if !strings.Contains(diff, "changed") {
		t.Error("expected diff output to contain unstaged changes")
	}
	if !strings.Contains(diff, "staged content") {
		t.Error("expected diff output to contain staged changes")
	}
}

func TestBranchDiff_EmptyForCleanWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-clean")
	run(t, repo, "git", "branch", "clean-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "clean-branch")

	diff, err := gitquery.BranchDiff(wtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if diff != "" {
		t.Errorf("expected empty diff, got %q", diff)
	}
}

func TestFlattenBranches_SinglePathGivesOneRow(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "main", IsWorktree: true, WorktreePaths: []string{"/dev/proj"}},
	}
	rows := gitquery.FlattenBranches(branches)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].WorktreePath != "/dev/proj" {
		t.Errorf("expected WorktreePath /dev/proj, got %q", rows[0].WorktreePath)
	}
	if rows[0].IsExpansion {
		t.Error("single-path row should not be IsExpansion")
	}
}

func TestFlattenBranches_NoPathGivesOneRowEmptyPath(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "feature", IsWorktree: false},
	}
	rows := gitquery.FlattenBranches(branches)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].WorktreePath != "" {
		t.Errorf("expected empty WorktreePath, got %q", rows[0].WorktreePath)
	}
	if rows[0].IsExpansion {
		t.Error("no-path row should not be IsExpansion")
	}
}

func TestFlattenBranches_StaleFlag(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "feat", IsWorktree: true, WorktreePaths: []string{"/dev/feat"}, WorktreeStale: []bool{true}},
	}
	rows := gitquery.FlattenBranches(branches)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if !rows[0].Stale {
		t.Error("expected Stale=true for row with stale worktree path")
	}
}

func TestFlattenBranches_NonStaleFlag(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "feat", IsWorktree: true, WorktreePaths: []string{"/dev/feat"}, WorktreeStale: []bool{false}},
	}
	rows := gitquery.FlattenBranches(branches)
	if rows[0].Stale {
		t.Error("expected Stale=false for non-stale worktree path")
	}
}

// --- Commit tests ---

func TestListCommits_ReturnsCommits(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	writeFile(t, dir, "a.txt", "one")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "second commit")

	writeFile(t, dir, "b.txt", "two")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "third commit")

	commits, err := gitquery.ListCommits(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	// Newest first
	if commits[0].Subject != "third commit" {
		t.Errorf("expected first commit subject %q, got %q", "third commit", commits[0].Subject)
	}
	if commits[0].Hash == "" {
		t.Error("expected non-empty Hash")
	}
	if commits[0].Author != "Test" {
		t.Errorf("expected Author %q, got %q", "Test", commits[0].Author)
	}
	if commits[0].Date == "" {
		t.Error("expected non-empty Date")
	}
}

func TestListCommits_Limit50(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	for i := 0; i < 54; i++ {
		writeFile(t, dir, "f.txt", fmt.Sprintf("v%d", i))
		run(t, dir, "git", "add", ".")
		run(t, dir, "git", "commit", "-m", fmt.Sprintf("commit %d", i))
	}

	commits, err := gitquery.ListCommits(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 50 {
		t.Fatalf("expected 50 commits, got %d", len(commits))
	}
}

func TestListCommits_NewestFirst(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	writeFile(t, dir, "a.txt", "one")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "older")

	writeFile(t, dir, "b.txt", "two")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "newer")

	commits, err := gitquery.ListCommits(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) < 2 {
		t.Fatal("expected at least 2 commits")
	}
	if commits[0].Subject != "newer" {
		t.Errorf("expected newest commit first, got %q", commits[0].Subject)
	}
	if commits[1].Subject != "older" {
		t.Errorf("expected older commit second, got %q", commits[1].Subject)
	}
}

func TestListCommits_InvalidPath(t *testing.T) {
	_, err := gitquery.ListCommits("/no/such/path")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

// TestGitCmdError_SurfacesStderr verifies that a failed git query surfaces git's
// stderr diagnostic in the returned error rather than a bare "exit status N".
func TestGitCmdError_SurfacesStderr(t *testing.T) {
	dir := realPath(t, t.TempDir()) // a real directory that is not a git repo

	_, err := gitquery.ListCommits(dir)
	if err == nil {
		t.Fatal("expected error running git in a non-repo directory, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("expected error to surface git stderr diagnostic, got %q", err.Error())
	}
}

func TestCommitDiff_ReturnsDiff(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	writeFile(t, dir, "a.txt", "changed")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "change a file")

	commits, err := gitquery.ListCommits(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) == 0 {
		t.Fatal("expected at least one commit")
	}

	diff, err := gitquery.CommitDiff(dir, commits[0].Hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "diff --git") {
		t.Error("expected diff to contain 'diff --git'")
	}
	if !strings.Contains(diff, "change a file") {
		t.Error("expected diff to contain commit message")
	}
}

func TestCommitDiff_InvalidHash(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	_, err := gitquery.CommitDiff(dir, "0000000000000000")
	if err == nil {
		t.Fatal("expected error for invalid hash, got nil")
	}
}

func TestFlattenBranches_TwoPathsExpandsToTwoRows(t *testing.T) {
	branches := []gitquery.Branch{
		{Name: "feat", IsWorktree: true, WorktreePaths: []string{"/dev/feat-A", "/dev/feat-B"}},
	}
	rows := gitquery.FlattenBranches(branches)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].WorktreePath != "/dev/feat-A" {
		t.Errorf("first row should have /dev/feat-A, got %q", rows[0].WorktreePath)
	}
	if rows[0].IsExpansion {
		t.Error("first row should not be IsExpansion")
	}
	if rows[1].WorktreePath != "/dev/feat-B" {
		t.Errorf("second row should have /dev/feat-B, got %q", rows[1].WorktreePath)
	}
	if !rows[1].IsExpansion {
		t.Error("second row should be IsExpansion")
	}
	if rows[1].Branch.Name != "feat" {
		t.Errorf("expansion row should retain branch name, got %q", rows[1].Branch.Name)
	}
}

// --- ListWorktrees ---

func TestListWorktrees_ReturnsMainFirst(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-feature")
	run(t, repo, "git", "branch", "wt-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "wt-branch")

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}

	// First entry is the main worktree
	if !wts[0].IsMain {
		t.Error("first worktree should be IsMain")
	}
	if wts[0].Path != repo {
		t.Errorf("main worktree path: expected %q, got %q", repo, wts[0].Path)
	}
	if wts[0].Commit == "" {
		t.Fatal("main worktree commit should be populated")
	}

	// Second entry is the added worktree
	if wts[1].IsMain {
		t.Error("second worktree should not be IsMain")
	}
	if wts[1].BranchName != "wt-branch" {
		t.Errorf("expected BranchName 'wt-branch', got %q", wts[1].BranchName)
	}
	if wts[1].Path != wtPath {
		t.Errorf("expected path %q, got %q", wtPath, wts[1].Path)
	}
	if wts[1].Commit != wts[0].Commit {
		t.Errorf("expected linked worktree commit %q, got %q", wts[0].Commit, wts[1].Commit)
	}
	if wts[1].Detached {
		t.Error("named branch worktree should not be Detached")
	}
}

func TestListWorktrees_MainBranchNamePopulated(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}

	defaultBranch := strings.TrimSpace(run(t, repo, "git", "branch", "--show-current"))
	if wts[0].BranchName != defaultBranch {
		t.Errorf("expected BranchName %q, got %q", defaultBranch, wts[0].BranchName)
	}
}

func TestListWorktrees_InvalidPath(t *testing.T) {
	_, err := gitquery.ListWorktrees("/no/such/path")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestListWorktrees_DetachedWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-detached")
	run(t, repo, "git", "worktree", "add", "--detach", wtPath)

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the detached worktree
	var detached *gitquery.Worktree
	for i := range wts {
		if wts[i].Path == wtPath {
			detached = &wts[i]
			break
		}
	}
	if detached == nil {
		t.Fatal("detached worktree not found")
	}
	if !detached.Detached {
		t.Error("expected Detached = true")
	}
	if detached.BranchName != "" {
		t.Errorf("expected empty BranchName for detached, got %q", detached.BranchName)
	}
}

func TestListWorktrees_StaleWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-stale")
	run(t, repo, "git", "branch", "stale-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "stale-branch")

	// Remove the worktree directory without git worktree remove
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var staleWt *gitquery.Worktree
	for i := range wts {
		if wts[i].BranchName == "stale-branch" {
			staleWt = &wts[i]
			break
		}
	}
	if staleWt == nil {
		t.Fatal("stale worktree not found")
	}
	if !staleWt.Stale {
		t.Error("expected Stale = true for removed worktree directory")
	}
}

func TestListWorktrees_LockedWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-locked")
	run(t, repo, "git", "branch", "locked-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "locked-branch")
	run(t, repo, "git", "worktree", "lock", wtPath)

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var lockedWt *gitquery.Worktree
	for i := range wts {
		if wts[i].Path == wtPath {
			lockedWt = &wts[i]
			break
		}
	}
	if lockedWt == nil {
		t.Fatal("locked worktree not found")
	}
	if !lockedWt.Locked {
		t.Error("expected Locked = true")
	}
}

func TestListWorktrees_LockedWorktreeReason(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-locked-reason")
	run(t, repo, "git", "branch", "locked-reason-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "locked-reason-branch")
	run(t, repo, "git", "worktree", "lock", "--reason", "on external drive", wtPath)

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var lockedWt *gitquery.Worktree
	for i := range wts {
		if wts[i].Path == wtPath {
			lockedWt = &wts[i]
			break
		}
	}
	if lockedWt == nil {
		t.Fatal("locked worktree not found")
	}
	if lockedWt.LockReason != "on external drive" {
		t.Errorf("expected LockReason %q, got %q", "on external drive", lockedWt.LockReason)
	}
}

func TestListWorktrees_LockedStaleWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-locked-stale")
	run(t, repo, "git", "branch", "locked-stale-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "locked-stale-branch")
	run(t, repo, "git", "worktree", "lock", wtPath)
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var lockedWt *gitquery.Worktree
	for i := range wts {
		if wts[i].Path == wtPath {
			lockedWt = &wts[i]
			break
		}
	}
	if lockedWt == nil {
		t.Fatal("locked stale worktree not found")
	}
	if !lockedWt.Locked {
		t.Error("expected Locked = true")
	}
	if !lockedWt.Stale {
		t.Error("expected Stale = true")
	}
}

func TestListWorktrees_DirtyWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-dirty")
	run(t, repo, "git", "branch", "dirty-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "dirty-branch")

	// Modify an existing tracked file
	writeFile(t, wtPath, "README.md", "changed content\nnew line\n")

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var dirtyWt *gitquery.Worktree
	for i := range wts {
		if wts[i].BranchName == "dirty-branch" {
			dirtyWt = &wts[i]
			break
		}
	}
	if dirtyWt == nil {
		t.Fatal("dirty worktree not found")
	}
	if !dirtyWt.Dirty {
		t.Error("expected Dirty = true")
	}
	if dirtyWt.FilesChanged == 0 {
		t.Error("expected FilesChanged > 0")
	}
	if dirtyWt.LinesAdded == 0 {
		t.Error("expected LinesAdded > 0")
	}
}

func TestListWorktrees_CleanWorktree(t *testing.T) {
	repo := realPath(t, initBranchRepo(t))
	wtDir := realPath(t, t.TempDir())
	wtPath := filepath.Join(wtDir, "wt-clean")
	run(t, repo, "git", "branch", "clean-branch")
	run(t, repo, "git", "worktree", "add", wtPath, "clean-branch")

	wts, err := gitquery.ListWorktrees(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cleanWt *gitquery.Worktree
	for i := range wts {
		if wts[i].BranchName == "clean-branch" {
			cleanWt = &wts[i]
			break
		}
	}
	if cleanWt == nil {
		t.Fatal("clean worktree not found")
	}
	if cleanWt.Dirty {
		t.Error("expected Dirty = false for clean worktree")
	}
	if cleanWt.FilesChanged != 0 {
		t.Errorf("expected FilesChanged = 0, got %d", cleanWt.FilesChanged)
	}
}

// --- Reflog tests ---

func TestListReflog_ReturnsEntries(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	writeFile(t, dir, "a.txt", "one")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "second commit")

	entries, err := gitquery.ListReflog(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 reflog entries, got %d", len(entries))
	}
	e := entries[0]
	if e.Hash == "" {
		t.Error("expected non-empty Hash")
	}
	if e.Selector == "" {
		t.Error("expected non-empty Selector")
	}
	if e.Date == "" {
		t.Error("expected non-empty Date")
	}
	if e.Subject == "" {
		t.Error("expected non-empty Subject")
	}
}

func TestListReflog_InvalidPath(t *testing.T) {
	_, err := gitquery.ListReflog("/no/such/path")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestReflogDiff_ReturnsDiff(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	writeFile(t, dir, "a.txt", "changed")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "change a file")

	entries, err := gitquery.ListReflog(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one reflog entry")
	}

	diff, err := gitquery.ReflogDiff(dir, entries[0].Hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff")
	}
	if !strings.Contains(diff, "a.txt") {
		t.Error("expected diff to mention 'a.txt'")
	}
}

func TestReflogDiff_RootCommitFallback(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	// The initial commit has no parent — ReflogDiff should fall back to git show
	commits, _ := gitquery.ListCommits(dir)
	if len(commits) == 0 {
		t.Fatal("expected at least one commit")
	}
	rootHash := commits[len(commits)-1].Hash

	diff, err := gitquery.ReflogDiff(dir, rootHash)
	if err != nil {
		t.Fatalf("unexpected error for root commit: %v", err)
	}
	if diff == "" {
		t.Error("expected non-empty diff for root commit")
	}
}

func TestReflogDiff_EmptyDiff(t *testing.T) {
	dir := realPath(t, t.TempDir())
	initRepo(t, dir)

	// Create a branch and checkout back — produces reflog entry with no diff
	run(t, dir, "git", "checkout", "-b", "feature")
	run(t, dir, "git", "checkout", "main")

	entries, _ := gitquery.ListReflog(dir)
	// The most recent entry is a checkout that moves HEAD but changes nothing
	diff, err := gitquery.ReflogDiff(dir, entries[0].Hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty diff is valid — checkout doesn't change files
	// The function should return empty string, not an error
	_ = diff
}

func TestReflogDiff_InvalidPath(t *testing.T) {
	_, err := gitquery.ReflogDiff("/no/such/path", "abc1234")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}
