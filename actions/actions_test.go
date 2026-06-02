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

func runOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func setupRemoteRepo(t *testing.T) (localPath, upstreamPath, branch string) {
	t.Helper()
	dir := t.TempDir()
	upstreamPath = filepath.Join(dir, "upstream")
	originPath := filepath.Join(dir, "origin.git")
	localPath = filepath.Join(dir, "local")

	mustRun(t, dir, "git", "init", upstreamPath)
	mustRun(t, upstreamPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, upstreamPath, "git", "config", "user.name", "Test")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "init")
	mustRun(t, dir, "git", "init", "--bare", originPath)
	mustRun(t, upstreamPath, "git", "remote", "add", "origin", originPath)
	mustRun(t, upstreamPath, "git", "push", "-u", "origin", "HEAD")

	mustRun(t, dir, "git", "clone", originPath, localPath)
	mustRun(t, localPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, localPath, "git", "config", "user.name", "Test")
	branch = runOutput(t, localPath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	return localPath, upstreamPath, branch
}

func TestFetch(t *testing.T) {
	localPath, upstreamPath, branch := setupRemoteRepo(t)
	oldRemote := runOutput(t, localPath, "git", "rev-parse", "origin/"+branch)

	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "remote change")
	wantRemote := runOutput(t, upstreamPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "push")

	if err := actions.Fetch(localPath); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	gotRemote := runOutput(t, localPath, "git", "rev-parse", "origin/"+branch)
	if gotRemote == oldRemote {
		t.Fatal("expected fetch to advance the remote-tracking branch")
	}
	if gotRemote != wantRemote {
		t.Fatalf("expected origin/%s at %s, got %s", branch, wantRemote, gotRemote)
	}
}

func TestFetch_Error(t *testing.T) {
	if err := actions.Fetch("/nonexistent"); err == nil {
		t.Fatal("expected Fetch to fail for nonexistent path")
	}
}

func TestPull(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "remote change")
	wantHead := runOutput(t, upstreamPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "push")

	if err := actions.Pull(localPath); err != nil {
		t.Fatalf("Pull returned error: %v", err)
	}

	gotHead := runOutput(t, localPath, "git", "rev-parse", "HEAD")
	if gotHead != wantHead {
		t.Fatalf("expected local HEAD at %s, got %s", wantHead, gotHead)
	}
}

func TestPull_Error(t *testing.T) {
	if err := actions.Pull("/nonexistent"); err == nil {
		t.Fatal("expected Pull to fail for nonexistent path")
	}
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
		path       string
		wantPrefix string
	}{
		{"/tmp/repo-worktrees/feature-api", "feature-api-"},
		{"/tmp/repo-worktrees/feature/api:oauth", "api-oauth-"},
		{"/tmp/repo-worktrees/../repo", "repo-"},
		{"/", "worktree-"},
	}

	for _, tt := range tests {
		got := actions.WorktreeSessionName(tt.path)
		if !strings.HasPrefix(got, tt.wantPrefix) {
			t.Errorf("WorktreeSessionName(%q) = %q, want prefix %q", tt.path, got, tt.wantPrefix)
		}
		suffix := strings.TrimPrefix(got, tt.wantPrefix)
		if len(suffix) != 8 {
			t.Errorf("WorktreeSessionName(%q) hash suffix length = %d, want 8", tt.path, len(suffix))
		}
	}
}

func TestWorktreeSessionName_DisambiguatesSameLeafName(t *testing.T) {
	first := actions.WorktreeSessionName("/tmp/api-worktrees/feature-auth")
	second := actions.WorktreeSessionName("/tmp/web-worktrees/feature-auth")

	if first == second {
		t.Fatalf("expected distinct session names for different paths with the same leaf, got %q", first)
	}
	if !strings.HasPrefix(first, "feature-auth-") || !strings.HasPrefix(second, "feature-auth-") {
		t.Fatalf("expected readable leaf prefixes, got %q and %q", first, second)
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
	sessionName := actions.WorktreeSessionName(worktreePath)
	if got := launch.Cmd.Args; len(got) != 6 || got[0] != "sh" || got[1] != "-c" || got[3] != "wtui" || got[4] != sessionName || got[5] != worktreePath {
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
	want := []string{"zellij", "action", "switch-session", actions.WorktreeSessionName(worktreePath), "--cwd", worktreePath}
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

func TestOpenTerminal_InteractiveLaunchRequiresCallerTTY(t *testing.T) {
	dir := t.TempDir()
	tmuxPath := filepath.Join(dir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TMUX", "")
	t.Setenv("ZELLIJ", "")

	worktreePath := filepath.Join(t.TempDir(), "feature")
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		t.Fatal(err)
	}

	err := actions.OpenTerminal(worktreePath)
	if err == nil {
		t.Fatal("expected interactive terminal launch to return an error")
	}
	if !strings.Contains(err.Error(), "requires an interactive TTY") {
		t.Fatalf("expected interactive TTY error, got %v", err)
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
