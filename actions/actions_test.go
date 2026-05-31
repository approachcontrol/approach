package actions_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/actions"
)

func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func prependFakePath(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestRemoveWorktree(t *testing.T) {
	// Set up a bare repo with a commit so worktrees work
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	worktreePath := filepath.Join(dir, "wt")

	mustRun(t, dir, "git", "init", repoPath)
	mustRun(t, repoPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, repoPath, "git", "config", "user.name", "Test")
	mustRun(t, repoPath, "git", "commit", "--allow-empty", "-m", "init")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")

	// Worktree dir should exist before removal
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree dir should exist before removal: %v", err)
	}

	err := actions.RemoveWorktree(repoPath, worktreePath)
	if err != nil {
		t.Fatalf("RemoveWorktree returned error: %v", err)
	}

	// Worktree dir should be gone
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("expected worktree dir to be removed")
	}

	// git worktree list should no longer show the worktree
	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list").Output()
	if strings.Contains(string(out), worktreePath) {
		t.Errorf("worktree still listed after removal:\n%s", out)
	}
}

func TestRemoveWorktree_Error(t *testing.T) {
	err := actions.RemoveWorktree("/nonexistent", "/also/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent paths, got nil")
	}
}

func setupRepo(t *testing.T) (repoPath string) {
	t.Helper()
	dir := t.TempDir()
	repoPath = filepath.Join(dir, "repo")
	mustRun(t, dir, "git", "init", repoPath)
	mustRun(t, repoPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, repoPath, "git", "config", "user.name", "Test")
	mustRun(t, repoPath, "git", "commit", "--allow-empty", "-m", "init")
	return repoPath
}

func TestForceRemoveWorktree(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-dirty")

	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "dirty-feat")

	// Write a dirty file so normal remove fails
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	// Normal remove should fail
	if err := actions.RemoveWorktree(repoPath, worktreePath); err == nil {
		t.Fatal("expected normal remove to fail on dirty worktree")
	}

	// Force remove should succeed
	if err := actions.ForceRemoveWorktree(repoPath, worktreePath); err != nil {
		t.Fatalf("ForceRemoveWorktree returned error: %v", err)
	}

	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("expected worktree dir to be removed after force")
	}
}

func TestRemoveWorktree_PrunesStaleReference(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-prune")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "prune-feat")

	// Remove normally, then re-create a stale admin reference to simulate
	// older git versions that don't clean up .git/worktrees/ on remove.
	mustRun(t, repoPath, "git", "worktree", "remove", worktreePath)

	// Synthetically recreate the admin entry pointing to a non-existent path
	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-prune")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(worktreePath+"/.git\n"), 0644)
	headBytes, _ := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	os.WriteFile(filepath.Join(adminDir, "HEAD"), headBytes, 0644)

	// Confirm the stale reference appears
	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if !strings.Contains(string(out), worktreePath) {
		t.Fatal("expected synthetic stale reference to appear in worktree list")
	}

	// RemoveWorktree should prune the stale reference
	_ = actions.RemoveWorktree(repoPath, worktreePath)

	out, _ = exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), worktreePath) {
		t.Errorf("stale worktree reference should be pruned:\n%s", out)
	}
}

func TestForceRemoveWorktree_PrunesStaleReference(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-force-prune")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "force-prune-feat")
	mustRun(t, repoPath, "git", "worktree", "remove", worktreePath)

	// Synthetically recreate a stale admin entry
	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-force-prune")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(worktreePath+"/.git\n"), 0644)
	headBytes, _ := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	os.WriteFile(filepath.Join(adminDir, "HEAD"), headBytes, 0644)

	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if !strings.Contains(string(out), worktreePath) {
		t.Fatal("expected synthetic stale reference")
	}

	_ = actions.ForceRemoveWorktree(repoPath, worktreePath)

	out, _ = exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), worktreePath) {
		t.Errorf("stale worktree reference should be pruned after force remove:\n%s", out)
	}
}

func TestRemoveWorktree_DoesNotPruneOnFailure(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-nopruneonfail")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "nopruneonfail-feat")
	mustRun(t, repoPath, "git", "worktree", "remove", worktreePath)

	// Synthetically recreate a stale admin entry
	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-nopruneonfail")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(worktreePath+"/.git\n"), 0644)
	headBytes, _ := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	os.WriteFile(filepath.Join(adminDir, "HEAD"), headBytes, 0644)

	// Call RemoveWorktree with bogus path so the remove step fails
	err := actions.RemoveWorktree(repoPath, "/nonexistent/worktree")
	if err == nil {
		t.Fatal("expected RemoveWorktree to fail for nonexistent path")
	}

	// Stale reference should still exist because prune should NOT have run
	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if !strings.Contains(string(out), worktreePath) {
		t.Error("stale worktree reference should NOT be pruned when removal fails")
	}
}

func TestForceRemoveWorktree_DoesNotPruneOnFailure(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-forcenopruneonfail")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "forcenopruneonfail-feat")
	mustRun(t, repoPath, "git", "worktree", "remove", worktreePath)

	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-forcenopruneonfail")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(worktreePath+"/.git\n"), 0644)
	headBytes, _ := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	os.WriteFile(filepath.Join(adminDir, "HEAD"), headBytes, 0644)

	err := actions.ForceRemoveWorktree(repoPath, "/nonexistent/worktree")
	if err == nil {
		t.Fatal("expected ForceRemoveWorktree to fail for nonexistent path")
	}

	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if !strings.Contains(string(out), worktreePath) {
		t.Error("stale worktree reference should NOT be pruned when force removal fails")
	}
}

func TestPruneWorktree(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-pruneaction")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "pruneaction-feat")
	mustRun(t, repoPath, "git", "worktree", "remove", worktreePath)

	// Synthetically recreate a stale admin entry
	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-pruneaction")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(worktreePath+"/.git\n"), 0644)
	headBytes, _ := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	os.WriteFile(filepath.Join(adminDir, "HEAD"), headBytes, 0644)

	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if !strings.Contains(string(out), worktreePath) {
		t.Fatal("expected stale reference before prune")
	}

	if err := actions.PruneWorktree(repoPath); err != nil {
		t.Fatalf("PruneWorktree returned error: %v", err)
	}

	out, _ = exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), worktreePath) {
		t.Error("stale worktree reference should be pruned after PruneWorktree")
	}
}

func TestDefaultWorktreePath(t *testing.T) {
	path := actions.DefaultWorktreePath("/tmp/repo", "feature/new thing")
	expected := filepath.Join("/tmp", "repo-worktrees", "feature-new-thing")
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestWorktreeSessionName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/repo-worktrees/feature-api", "feature-api"},
		{"/tmp/repo-worktrees/feature/api:oauth", "api-oauth"},
		{"/tmp/repo-worktrees/../repo", "repo"},
		{"/", "worktree"},
	}

	for _, tt := range tests {
		if got := actions.WorktreeSessionName(tt.path); got != tt.want {
			t.Errorf("WorktreeSessionName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestTerminalLaunch_InsideTmuxCreatesOrSwitchesSession(t *testing.T) {
	prependFakePath(t, "tmux")
	t.Setenv("TMUX", "/tmp/tmux-socket")
	t.Setenv("ZELLIJ", "")
	worktreePath := filepath.Join(t.TempDir(), "feature:oauth")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}

	launch, err := actions.TerminalLaunch(worktreePath)
	if err != nil {
		t.Fatalf("TerminalLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("inside-tmux launch should be non-interactive")
	}
	if got := launch.Cmd.Args; len(got) != 6 || got[0] != "sh" || got[1] != "-c" || got[3] != "wtui" || got[4] != "feature-oauth" || got[5] != worktreePath {
		t.Fatalf("unexpected tmux launch args: %#v", got)
	}
}

func TestTerminalLaunch_InsideZellijSwitchesSessionWithCwd(t *testing.T) {
	prependFakePath(t, "zellij", "tmux")
	t.Setenv("ZELLIJ", "0")
	t.Setenv("TMUX", "/tmp/tmux-socket")
	worktreePath := filepath.Join(t.TempDir(), "feat")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}

	launch, err := actions.TerminalLaunch(worktreePath)
	if err != nil {
		t.Fatalf("TerminalLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("inside-zellij launch should be non-interactive")
	}
	want := []string{"zellij", "action", "switch-session", "feat", "--cwd", worktreePath}
	if strings.Join(launch.Cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected zellij launch args: got %#v want %#v", launch.Cmd.Args, want)
	}
}

func TestCreateWorktree_FromExistingBranch(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "branch", "feature/existing")

	worktreePath, err := actions.CreateWorktree(repoPath, "feature/existing")
	if err != nil {
		t.Fatalf("CreateWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feature-existing")
	if worktreePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, worktreePath)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree directory to exist: %v", err)
	}

	out, _ := exec.Command("git", "-C", worktreePath, "branch", "--show-current").Output()
	if strings.TrimSpace(string(out)) != "feature/existing" {
		t.Fatalf("expected checked out branch feature/existing, got %q", strings.TrimSpace(string(out)))
	}
}

func TestCreateWorktree_FromNewBranchName(t *testing.T) {
	repoPath := setupRepo(t)

	worktreePath, err := actions.CreateWorktree(repoPath, "feature/new")
	if err != nil {
		t.Fatalf("CreateWorktree returned error: %v", err)
	}

	out, _ := exec.Command("git", "-C", worktreePath, "branch", "--show-current").Output()
	if strings.TrimSpace(string(out)) != "feature/new" {
		t.Fatalf("expected checked out branch feature/new, got %q", strings.TrimSpace(string(out)))
	}
	out, _ = exec.Command("git", "-C", repoPath, "branch", "--list", "feature/new").Output()
	if !strings.Contains(string(out), "feature/new") {
		t.Fatal("expected new branch to exist in repo")
	}
}

func TestCreateWorktree_FromTag(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "tag", "v1.0.0")

	worktreePath, err := actions.CreateWorktree(repoPath, "v1.0.0")
	if err != nil {
		t.Fatalf("CreateWorktree returned error: %v", err)
	}

	out, _ := exec.Command("git", "-C", worktreePath, "branch", "--show-current").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected detached HEAD for tag worktree, got branch %q", strings.TrimSpace(string(out)))
	}
}

func TestCreateWorktree_EmptyInputFails(t *testing.T) {
	repoPath := setupRepo(t)
	if _, err := actions.CreateWorktree(repoPath, "  "); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestCreateWorktree_RefStartingWithDashFails(t *testing.T) {
	repoPath := setupRepo(t)
	_, err := actions.CreateWorktree(repoPath, "--detach")
	if err == nil {
		t.Fatal("expected error for ref starting with dash")
	}
	if !strings.Contains(err.Error(), "cannot start with -") {
		t.Fatalf("expected invalid ref error, got %v", err)
	}
}

func TestUnlockWorktree(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-unlock")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "unlock-feat")
	mustRun(t, repoPath, "git", "worktree", "lock", worktreePath)

	if err := actions.UnlockWorktree(repoPath, worktreePath); err != nil {
		t.Fatalf("UnlockWorktree returned error: %v", err)
	}

	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), "locked") {
		t.Errorf("worktree should not be locked after unlock:\n%s", out)
	}
}

func TestUnlockWorktree_AlreadyUnlockedReturnsError(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-already-unlocked")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "already-unlocked-feat")

	if err := actions.UnlockWorktree(repoPath, worktreePath); err == nil {
		t.Fatal("expected UnlockWorktree to return an error for already-unlocked worktree")
	}
}

// TestRemoveWorktreeThenDeleteBranch verifies the combined flow the model
// uses: remove worktree, then force-delete the branch.
func TestRemoveWorktreeThenDeleteBranch(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-branchdel")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "branchdel-feat")

	if err := actions.RemoveWorktree(repoPath, worktreePath); err != nil {
		t.Fatalf("RemoveWorktree returned error: %v", err)
	}

	// Branch still exists after worktree removal alone
	out, _ := exec.Command("git", "-C", repoPath, "branch").Output()
	if !strings.Contains(string(out), "branchdel-feat") {
		t.Fatal("expected branch to still exist after worktree-only removal")
	}

	// Force-delete the branch (needed because branch may have unmerged commits)
	if err := actions.ForceDeleteBranch(repoPath, "branchdel-feat"); err != nil {
		t.Fatalf("ForceDeleteBranch returned error: %v", err)
	}

	out, _ = exec.Command("git", "-C", repoPath, "branch").Output()
	if strings.Contains(string(out), "branchdel-feat") {
		t.Error("branch should be gone after ForceDeleteBranch")
	}
}

func TestRemoveWorktree_EndToEnd_NoStaleRef(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "wt-e2e")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "e2e-feat")

	if err := actions.RemoveWorktree(repoPath, worktreePath); err != nil {
		t.Fatalf("RemoveWorktree returned error: %v", err)
	}

	// Check: does git worktree list still show it?
	out, _ := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if strings.Contains(string(out), worktreePath) {
		t.Errorf("worktree path still in 'git worktree list' after RemoveWorktree:\n%s", out)
	}

	// Check: does the .git/worktrees/ admin entry still exist?
	adminDir := filepath.Join(repoPath, ".git", "worktrees", "wt-e2e")
	if _, err := os.Stat(adminDir); err == nil {
		entries, _ := os.ReadDir(adminDir)
		t.Errorf(".git/worktrees/wt-e2e still exists after RemoveWorktree, entries: %v", entries)
	}
}

func TestDeleteBranch(t *testing.T) {
	repoPath := setupRepo(t)
	// Create and merge a branch so -d works
	mustRun(t, repoPath, "git", "checkout", "-b", "merged-feat")
	mustRun(t, repoPath, "git", "checkout", "-")

	if err := actions.DeleteBranch(repoPath, "merged-feat"); err != nil {
		t.Fatalf("DeleteBranch returned error: %v", err)
	}

	out, _ := exec.Command("git", "-C", repoPath, "branch").Output()
	if strings.Contains(string(out), "merged-feat") {
		t.Error("branch should be gone after DeleteBranch")
	}
}

func TestDeleteBranch_UnmergedFails(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "checkout", "-b", "unmerged-feat")
	mustRun(t, repoPath, "git", "commit", "--allow-empty", "-m", "unmerged commit")
	mustRun(t, repoPath, "git", "checkout", "-")

	if err := actions.DeleteBranch(repoPath, "unmerged-feat"); err == nil {
		t.Error("expected DeleteBranch to fail for unmerged branch")
	}
}

func TestForceDeleteBranch(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "checkout", "-b", "unmerged-feat")
	mustRun(t, repoPath, "git", "commit", "--allow-empty", "-m", "unmerged commit")
	mustRun(t, repoPath, "git", "checkout", "-")

	if err := actions.ForceDeleteBranch(repoPath, "unmerged-feat"); err != nil {
		t.Fatalf("ForceDeleteBranch returned error: %v", err)
	}

	out, _ := exec.Command("git", "-C", repoPath, "branch").Output()
	if strings.Contains(string(out), "unmerged-feat") {
		t.Error("branch should be gone after ForceDeleteBranch")
	}
}

func TestDropStash(t *testing.T) {
	repoPath := setupRepo(t)

	// Create a file and stash it
	if err := os.WriteFile(filepath.Join(repoPath, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, repoPath, "git", "add", ".")
	mustRun(t, repoPath, "git", "stash")

	// Confirm stash exists
	out, _ := exec.Command("git", "-C", repoPath, "stash", "list").Output()
	if !strings.Contains(string(out), "stash@{0}") {
		t.Fatal("expected stash to exist before drop")
	}

	if err := actions.DropStash(repoPath, 0); err != nil {
		t.Fatalf("DropStash returned error: %v", err)
	}

	// Stash list should be empty
	out, _ = exec.Command("git", "-C", repoPath, "stash", "list").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected stash list empty after drop, got: %s", out)
	}
}

func TestDropStash_Error(t *testing.T) {
	err := actions.DropStash("/nonexistent", 0)
	if err == nil {
		t.Error("expected error for nonexistent repo, got nil")
	}
}

func TestOpenTerminal_Error(t *testing.T) {
	err := actions.OpenTerminal("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
}

func TestCopyToClipboard(t *testing.T) {
	if _, err := exec.LookPath("pbcopy"); err != nil {
		t.Skip("pbcopy not available")
	}
	err := actions.CopyToClipboard("test-hash-abc123")
	if err != nil {
		t.Fatalf("CopyToClipboard returned error: %v", err)
	}
	// Verify clipboard contents
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Fatalf("pbpaste failed: %v", err)
	}
	if string(out) != "test-hash-abc123" {
		t.Errorf("expected clipboard %q, got %q", "test-hash-abc123", string(out))
	}
}

func TestOpenVSCode_RunsWithoutPanic(t *testing.T) {
	if os.Getenv("TEST_LAUNCH_APPS") == "" {
		t.Skip("skipping: set TEST_LAUNCH_APPS=1 to run tests that launch GUI apps")
	}
	if _, err := exec.LookPath("code"); err != nil {
		t.Skip("code not in PATH")
	}
	// code exits 0 for any path; just verify no panic
	_ = actions.OpenVSCode(t.TempDir())
}
