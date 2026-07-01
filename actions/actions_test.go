package actions_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func TestMoveWorktree(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
	newPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat-renamed")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")

	got, err := actions.MoveWorktree(repoPath, worktreePath, newPath)
	if err != nil {
		t.Fatalf("MoveWorktree returned error: %v", err)
	}
	if got != newPath {
		t.Fatalf("expected returned path %q, got %q", newPath, got)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected old worktree path to be gone, stat err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected new worktree path to exist: %v", err)
	}
	out := runOutput(t, repoPath, "git", "worktree", "list", "--porcelain")
	if !strings.Contains(out, "worktree "+newPath) {
		t.Fatalf("expected worktree list to contain new path, got:\n%s", out)
	}
	if strings.Contains(out, "worktree "+worktreePath+"\n") {
		t.Fatalf("expected worktree list not to contain old path, got:\n%s", out)
	}
	branch := runOutput(t, newPath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "feat" {
		t.Fatalf("expected moved worktree to remain on feat, got %q", branch)
	}
}

func TestMoveWorktree_DirtyWorktreeMovesWithLocalChanges(t *testing.T) {
	repoPath := setupRepo(t)
	worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
	newPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat-renamed")
	mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
	if err := os.WriteFile(filepath.Join(worktreePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := actions.MoveWorktree(repoPath, worktreePath, newPath)
	if err != nil {
		t.Fatalf("MoveWorktree returned error for dirty worktree: %v", err)
	}
	if got != newPath {
		t.Fatalf("expected returned path %q, got %q", newPath, got)
	}
	if contents, err := os.ReadFile(filepath.Join(newPath, "dirty.txt")); err != nil {
		t.Fatalf("expected dirty file to move with worktree: %v", err)
	} else if string(contents) != "dirty" {
		t.Fatalf("expected dirty contents preserved, got %q", contents)
	}
}

func TestMoveWorktree_ResolvesDestinations(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantPath    func(root string) string
		wantParents bool
	}{
		{
			name:  "bare relative destination",
			input: "feat-renamed",
			wantPath: func(root string) string {
				return filepath.Join(root, "repo-worktrees", "feat-renamed")
			},
		},
		{
			name:  "nested relative destination creates parents",
			input: filepath.Join("team", "feat-renamed"),
			wantPath: func(root string) string {
				return filepath.Join(root, "repo-worktrees", "team", "feat-renamed")
			},
			wantParents: true,
		},
		{
			name:  "absolute destination",
			input: "ABS",
			wantPath: func(root string) string {
				return filepath.Join(root, "elsewhere", "feat-renamed")
			},
			wantParents: true,
		},
		{
			name:  "path with spaces",
			input: "feat renamed",
			wantPath: func(root string) string {
				return filepath.Join(root, "repo-worktrees", "feat renamed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoPath := setupRepo(t)
			root := filepath.Dir(repoPath)
			worktreePath := filepath.Join(root, "repo-worktrees", "feat")
			mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
			input := tt.input
			wantPath := tt.wantPath(root)
			if input == "ABS" {
				input = wantPath
			}

			got, err := actions.MoveWorktree(repoPath, worktreePath, input)
			if err != nil {
				t.Fatalf("MoveWorktree returned error: %v", err)
			}
			if got != wantPath {
				t.Fatalf("expected resolved path %q, got %q", wantPath, got)
			}
			if _, err := os.Stat(wantPath); err != nil {
				t.Fatalf("expected destination to exist: %v", err)
			}
			if tt.wantParents {
				if _, err := os.Stat(filepath.Dir(wantPath)); err != nil {
					t.Fatalf("expected destination parent to exist: %v", err)
				}
			}
		})
	}
}

func TestMoveWorktree_Failures(t *testing.T) {
	t.Run("empty destination", func(t *testing.T) {
		if _, err := actions.MoveWorktree("/repo", "/repo-wt", ""); err == nil {
			t.Fatal("expected empty destination error")
		}
	})

	t.Run("whitespace destination", func(t *testing.T) {
		if _, err := actions.MoveWorktree("/repo", "/repo-wt", "   "); err == nil {
			t.Fatal("expected whitespace destination error")
		}
	})

	t.Run("empty worktree path", func(t *testing.T) {
		if _, err := actions.MoveWorktree("/repo", "", "new"); err == nil {
			t.Fatal("expected empty worktree path error")
		}
	})

	t.Run("same destination", func(t *testing.T) {
		repoPath := setupRepo(t)
		worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
		mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
		if _, err := actions.MoveWorktree(repoPath, worktreePath, worktreePath); err == nil {
			t.Fatal("expected same destination error")
		}
	})

	t.Run("destination already exists", func(t *testing.T) {
		repoPath := setupRepo(t)
		worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
		newPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "existing")
		mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
		if err := os.MkdirAll(newPath, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := actions.MoveWorktree(repoPath, worktreePath, newPath); err == nil {
			t.Fatal("expected existing destination error")
		}
	})

	t.Run("locked worktree", func(t *testing.T) {
		repoPath := setupRepo(t)
		worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
		newPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat-renamed")
		mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
		mustRun(t, repoPath, "git", "worktree", "lock", "--reason", "busy", worktreePath)
		if _, err := actions.MoveWorktree(repoPath, worktreePath, newPath); err == nil {
			t.Fatal("expected locked worktree error")
		} else if strings.Contains(err.Error(), "exit status") {
			t.Fatalf("expected clean git error, got %q", err.Error())
		}
	})

	t.Run("failed move cleans created parent directories", func(t *testing.T) {
		repoPath := setupRepo(t)
		worktreePath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "feat")
		newPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "team", "feat-renamed")
		mustRun(t, repoPath, "git", "worktree", "add", worktreePath, "-b", "feat")
		mustRun(t, repoPath, "git", "worktree", "lock", "--reason", "busy", worktreePath)
		if _, err := actions.MoveWorktree(repoPath, worktreePath, newPath); err == nil {
			t.Fatal("expected locked worktree error")
		}
		if _, err := os.Stat(filepath.Dir(newPath)); !os.IsNotExist(err) {
			t.Fatalf("expected created destination parent to be cleaned up, stat err=%v", err)
		}
		if _, err := os.Stat(filepath.Dir(worktreePath)); err != nil {
			t.Fatalf("expected existing worktree parent to remain: %v", err)
		}
	})

	t.Run("main worktree", func(t *testing.T) {
		repoPath := setupRepo(t)
		newPath := filepath.Join(filepath.Dir(repoPath), "repo-renamed")
		if _, err := actions.MoveWorktree(repoPath, repoPath, newPath); err == nil {
			t.Fatal("expected main worktree error")
		} else if strings.Contains(err.Error(), "exit status") {
			t.Fatalf("expected clean git error, got %q", err.Error())
		}
	})
}

func setupRepo(t *testing.T) (repoPath string) {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
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

func setupBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed")
	barePath := filepath.Join(dir, "project.git")

	mustRun(t, dir, "git", "init", seedPath)
	mustRun(t, seedPath, "git", "checkout", "-b", "main")
	mustRun(t, seedPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, seedPath, "git", "config", "user.name", "Test")
	mustRun(t, seedPath, "git", "commit", "--allow-empty", "-m", "init")
	mustRun(t, dir, "git", "init", "--bare", barePath)
	mustRun(t, seedPath, "git", "remote", "add", "origin", barePath)
	mustRun(t, seedPath, "git", "push", "-u", "origin", "main")
	mustRun(t, barePath, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	return barePath
}

func configureGitHubOrigin(t *testing.T, localPath, owner, repo string) {
	t.Helper()
	originPath := filepath.Join(filepath.Dir(localPath), "origin.git")
	githubURL := "https://github.com/" + owner + "/" + repo + ".git"
	configureOriginRewrite(t, localPath, githubURL, originPath)
}

func configureOriginRewrite(t *testing.T, localPath, remoteURL, originPath string) {
	t.Helper()
	mustRun(t, localPath, "git", "remote", "set-url", "origin", remoteURL)
	mustRun(t, localPath, "git", "config", "url."+originPath+".insteadOf", remoteURL)
}

func configureCustomGitHubOrigin(t *testing.T, localPath, remoteURL string) {
	t.Helper()
	originPath := filepath.Join(filepath.Dir(localPath), "origin.git")
	configureOriginRewrite(t, localPath, remoteURL, originPath)
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
	err := actions.Fetch("/nonexistent")
	if err == nil {
		t.Fatal("expected Fetch to fail for nonexistent path")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected clean git error without exit status, got %q", err.Error())
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
	err := actions.Pull("/nonexistent")
	if err == nil {
		t.Fatal("expected Pull to fail for nonexistent path")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected clean git error without exit status, got %q", err.Error())
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

func TestDefaultWorktreePath_BareRepoStripsDotGit(t *testing.T) {
	repoPath := setupBareRepo(t)

	path := actions.DefaultWorktreePath(repoPath, "feature/new")
	expected := filepath.Join(filepath.Dir(repoPath), "project-worktrees", "feature-new")
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

func TestCreateWorktree_FromBareRepoExistingBranch(t *testing.T) {
	repoPath := setupBareRepo(t)

	worktreePath, err := actions.CreateWorktree(repoPath, "main")
	if err != nil {
		t.Fatalf("CreateWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(repoPath), "project-worktrees", "main")
	if worktreePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, worktreePath)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree directory to exist: %v", err)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "main" {
		t.Fatalf("expected checked out branch main, got %q", got)
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

func TestCreateWorktree_FromBareRepoNewBranch(t *testing.T) {
	repoPath := setupBareRepo(t)

	worktreePath, err := actions.CreateWorktree(repoPath, "feature/new")
	if err != nil {
		t.Fatalf("CreateWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(repoPath), "project-worktrees", "feature-new")
	if worktreePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, worktreePath)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "feature/new" {
		t.Fatalf("expected checked out branch feature/new, got %q", got)
	}
	out := runOutput(t, repoPath, "git", "branch", "--list", "feature/new")
	if !strings.Contains(out, "feature/new") {
		t.Fatal("expected new branch to exist in bare repo")
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

func TestCreateFlowWorktree_AllocatesPairedBranchAndPath(t *testing.T) {
	repoPath := setupRepo(t)
	baseCommit := runOutput(t, repoPath, "git", "rev-parse", "HEAD")

	result, err := actions.CreateFlowWorktree(repoPath, "Add Flow Mode", "HEAD")
	if err != nil {
		t.Fatalf("CreateFlowWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "flow-add-flow-mode")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if result.Branch != "flow/add-flow-mode" {
		t.Fatalf("branch = %q, want flow/add-flow-mode", result.Branch)
	}
	if got := runOutput(t, result.WorktreePath, "git", "branch", "--show-current"); got != result.Branch {
		t.Fatalf("worktree branch = %q, want %q", got, result.Branch)
	}
	if got := runOutput(t, result.WorktreePath, "git", "rev-parse", "HEAD"); got != baseCommit {
		t.Fatalf("worktree commit = %q, want %q", got, baseCommit)
	}
}

func TestCreateFlowWorktree_IncrementsBranchAndPathTogetherOnCollision(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "branch", "flow/add-flow-mode")
	if err := os.MkdirAll(filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "flow-add-flow-mode-2"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := actions.CreateFlowWorktree(repoPath, "Add Flow Mode", "")
	if err != nil {
		t.Fatalf("CreateFlowWorktree returned error: %v", err)
	}

	if result.Branch != "flow/add-flow-mode-3" {
		t.Fatalf("branch = %q, want flow/add-flow-mode-3", result.Branch)
	}
	expectedPath := filepath.Join(filepath.Dir(repoPath), "repo-worktrees", "flow-add-flow-mode-3")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if got := runOutput(t, result.WorktreePath, "git", "branch", "--show-current"); got != result.Branch {
		t.Fatalf("worktree branch = %q, want %q", got, result.Branch)
	}
}

func TestCreatePullRequestWorktree_FromNumber(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "pr change")
	wantHead := runOutput(t, upstreamPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	worktreePath, err := actions.CreatePullRequestWorktree(localPath, "123")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(filepath.Dir(localPath), "local-worktrees", "pr-123")
	if worktreePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, worktreePath)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree directory to exist: %v", err)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
	if got := runOutput(t, worktreePath, "git", "rev-parse", "HEAD"); got != wantHead {
		t.Fatalf("expected worktree HEAD %s, got %s", wantHead, got)
	}
}

func TestCreatePullRequestWorktree_FromBareRepoNumber(t *testing.T) {
	dir := t.TempDir()
	upstreamPath := filepath.Join(dir, "upstream")
	originPath := filepath.Join(dir, "origin.git")
	localBarePath := filepath.Join(dir, "project.git")

	mustRun(t, dir, "git", "init", upstreamPath)
	mustRun(t, upstreamPath, "git", "checkout", "-b", "main")
	mustRun(t, upstreamPath, "git", "config", "user.email", "test@test.com")
	mustRun(t, upstreamPath, "git", "config", "user.name", "Test")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "init")
	mustRun(t, dir, "git", "init", "--bare", originPath)
	mustRun(t, upstreamPath, "git", "remote", "add", "origin", originPath)
	mustRun(t, upstreamPath, "git", "push", "-u", "origin", "main")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "pr change")
	wantHead := runOutput(t, upstreamPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")
	mustRun(t, dir, "git", "clone", "--bare", originPath, localBarePath)

	worktreePath, err := actions.CreatePullRequestWorktree(localBarePath, "123")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}

	expectedPath := filepath.Join(dir, "project-worktrees", "pr-123")
	if worktreePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, worktreePath)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
	if got := runOutput(t, worktreePath, "git", "rev-parse", "HEAD"); got != wantHead {
		t.Fatalf("expected worktree HEAD %s, got %s", wantHead, got)
	}
}

func TestCreatePullRequestWorktree_FromGitHubURL(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	configureGitHubOrigin(t, localPath, "acme", "project")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "pr url change")
	wantHead := runOutput(t, upstreamPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	worktreePath, err := actions.CreatePullRequestWorktree(localPath, "https://github.com/acme/project/pull/123")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}

	if got := filepath.Base(worktreePath); got != "pr-123" {
		t.Fatalf("expected worktree leaf pr-123, got %q", got)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
	if got := runOutput(t, worktreePath, "git", "rev-parse", "HEAD"); got != wantHead {
		t.Fatalf("expected worktree HEAD %s, got %s", wantHead, got)
	}
}

func TestCreatePullRequestWorktree_FromGitHubURLSubpage(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	configureGitHubOrigin(t, localPath, "acme", "project")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "pr files change")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	worktreePath, err := actions.CreatePullRequestWorktree(localPath, "https://github.com/acme/project/pull/123/files")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
}

func TestCreatePullRequestWorktree_AcceptsHashPrefixedNumber(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "hash pr change")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	worktreePath, err := actions.CreatePullRequestWorktree(localPath, "#123")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
}

func TestCreatePullRequestWorktree_AcceptsSchemeLessGitHubURL(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	configureGitHubOrigin(t, localPath, "acme", "project")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "schemeless pr change")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	worktreePath, err := actions.CreatePullRequestWorktree(localPath, "github.com/acme/project/pull/123")
	if err != nil {
		t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
	}
	if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
		t.Fatalf("expected branch pr-123, got %q", got)
	}
}

func TestCreatePullRequestWorktree_GitHubURLMustMatchOrigin(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	configureGitHubOrigin(t, localPath, "acme", "project")
	before := runOutput(t, localPath, "git", "rev-parse", "HEAD")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "wrong repo pr")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	_, err := actions.CreatePullRequestWorktree(localPath, "https://github.com/other/project/pull/123")
	if err == nil {
		t.Fatal("expected mismatched GitHub URL to fail")
	}
	if !strings.Contains(err.Error(), "does not match origin") {
		t.Fatalf("expected origin mismatch error, got %v", err)
	}
	if got := runOutput(t, localPath, "git", "rev-parse", "--verify", "HEAD"); got != before {
		t.Fatalf("expected no git mutation before fetch, got HEAD %s want %s", got, before)
	}
}

func TestCreatePullRequestWorktree_GitHubOriginURLShapes(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string
		prURL     string
	}{
		{
			name:      "https trailing slash",
			remoteURL: "https://github.com/acme/project.git/",
			prURL:     "https://github.com/acme/project/pull/123",
		},
		{
			name:      "ssh URL",
			remoteURL: "ssh://git@github.com/acme/project.git",
			prURL:     "https://github.com/acme/project/pull/123",
		},
		{
			name:      "scp style",
			remoteURL: "git@github.com:acme/project.git",
			prURL:     "https://github.com/acme/project/pull/123",
		},
		{
			name:      "case insensitive",
			remoteURL: "https://github.com/Acme/Project.git",
			prURL:     "https://github.com/acme/project/pull/123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			localPath, upstreamPath, _ := setupRemoteRepo(t)
			configureCustomGitHubOrigin(t, localPath, tc.remoteURL)
			mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "origin shape pr")
			mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

			worktreePath, err := actions.CreatePullRequestWorktree(localPath, tc.prURL)
			if err != nil {
				t.Fatalf("CreatePullRequestWorktree returned error: %v", err)
			}
			if got := runOutput(t, worktreePath, "git", "branch", "--show-current"); got != "pr-123" {
				t.Fatalf("expected branch pr-123, got %q", got)
			}
		})
	}
}

func TestCreatePullRequestWorktree_ExistingReviewBranchFailsBeforeFetch(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	mustRun(t, localPath, "git", "branch", "pr-123")
	before := runOutput(t, localPath, "git", "rev-parse", "pr-123")
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "new pr head")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")

	_, err := actions.CreatePullRequestWorktree(localPath, "123")
	if err == nil {
		t.Fatal("expected existing review branch to fail")
	}
	if !strings.Contains(err.Error(), "branch pr-123 already exists") {
		t.Fatalf("expected existing branch error, got %v", err)
	}
	if got := runOutput(t, localPath, "git", "rev-parse", "pr-123"); got != before {
		t.Fatalf("expected existing branch not to move, got %s want %s", got, before)
	}
}

func TestCreatePullRequestWorktree_CleansFetchedBranchWhenWorktreeAddFails(t *testing.T) {
	localPath, upstreamPath, _ := setupRemoteRepo(t)
	mustRun(t, upstreamPath, "git", "commit", "--allow-empty", "-m", "cleanup pr branch")
	mustRun(t, upstreamPath, "git", "push", "origin", "HEAD:refs/pull/123/head")
	worktreeParent := filepath.Join(filepath.Dir(localPath), "local-worktrees")
	if err := os.MkdirAll(worktreeParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(worktreeParent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(worktreeParent, 0o755)
	})

	_, err := actions.CreatePullRequestWorktree(localPath, "123")
	if err == nil {
		t.Fatal("expected worktree add to fail")
	}
	if out := runOutput(t, localPath, "git", "branch", "--list", "pr-123"); out != "" {
		t.Fatalf("expected fetched review branch to be cleaned up, got %q", out)
	}
}

func TestCreatePullRequestWorktree_InvalidInputFailsBeforeGit(t *testing.T) {
	repoPath := setupRepo(t)
	tests := []string{
		"",
		"  ",
		"--upload-pack=/tmp/nope",
		"0",
		"abc",
		"https://gitlab.com/acme/project/-/merge_requests/123",
		"https://github.com/acme/project/pull/",
		"https://github.com/acme/project/pull/abc",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			worktreePath, err := actions.CreatePullRequestWorktree(repoPath, input)
			if err == nil {
				t.Fatal("expected error for invalid PR input")
			}
			if worktreePath != "" {
				t.Fatalf("expected no worktree path, got %q", worktreePath)
			}
			if strings.Contains(err.Error(), "exit status") {
				t.Fatalf("expected validation error without exit status, got %q", err.Error())
			}
		})
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

func TestRunBootstrapHook_RunsExecutableScriptInWorktree(t *testing.T) {
	dir := t.TempDir()
	repoPath := filepath.Join(dir, "repo")
	worktreePath := filepath.Join(dir, "worktree")
	for _, key := range []string{
		"WTUI_REPO_PATH",
		"WTUI_WORKTREE_PATH",
		"WTUI_WORKTREE_REF",
		"WTUI_WORKTREE_CREATE_KIND",
	} {
		t.Setenv(key, "inherited-"+key)
	}
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(worktreePath, ".wtui", "bootstrap")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
pwd > cwd.txt
printf "%s
%s
%s
%s
" "$WTUI_REPO_PATH" "$WTUI_WORKTREE_PATH" "$WTUI_WORKTREE_REF" "$WTUI_WORKTREE_CREATE_KIND" > env.txt
env | awk -F= '/^WTUI_REPO_PATH=|^WTUI_WORKTREE_PATH=|^WTUI_WORKTREE_REF=|^WTUI_WORKTREE_CREATE_KIND=/ { count[$1]++ } END { for (key in count) print key "=" count[key] }' | sort > env-counts.txt
`), 0o755); err != nil {
		t.Fatal(err)
	}

	err := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Ref:          "feature/one",
		Kind:         actions.WorktreeCreateGeneric,
	}, actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("RunBootstrapHook returned error: %v", err)
	}

	cwd, err := os.ReadFile(filepath.Join(worktreePath, "cwd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	physicalWorktreePath, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(cwd)) != physicalWorktreePath {
		t.Fatalf("expected hook cwd %q, got %q", physicalWorktreePath, strings.TrimSpace(string(cwd)))
	}
	env, err := os.ReadFile(filepath.Join(worktreePath, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{repoPath, worktreePath, "feature/one", "generic"}, "\n")
	if strings.TrimSpace(string(env)) != want {
		t.Fatalf("unexpected hook env:\n%s", env)
	}
	counts, err := os.ReadFile(filepath.Join(worktreePath, "env-counts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := strings.Join([]string{
		"WTUI_REPO_PATH=1",
		"WTUI_WORKTREE_CREATE_KIND=1",
		"WTUI_WORKTREE_PATH=1",
		"WTUI_WORKTREE_REF=1",
	}, "\n")
	if strings.TrimSpace(string(counts)) != wantCounts {
		t.Fatalf("unexpected hook env counts:\n%s", counts)
	}
}

func TestRunBootstrapHook_AbsoluteScript(t *testing.T) {
	dir := t.TempDir()
	worktreePath := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "bootstrap")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ntouch absolute-ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     filepath.Join(dir, "repo"),
		WorktreePath: worktreePath,
		Ref:          "123",
		Kind:         actions.WorktreeCreatePullRequest,
	}, actions.BootstrapHook{Script: scriptPath, TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("RunBootstrapHook returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "absolute-ran")); err != nil {
		t.Fatalf("expected absolute hook to run in worktree: %v", err)
	}
}

func TestRunBootstrapHook_ReportsScriptValidationErrors(t *testing.T) {
	worktreePath := t.TempDir()
	missingErr := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		Ref:          "feat",
		Kind:         actions.WorktreeCreateGeneric,
	}, actions.BootstrapHook{Script: ".wtui/missing", TimeoutSeconds: 5})
	if missingErr == nil || !strings.Contains(missingErr.Error(), "bootstrap hook not found") {
		t.Fatalf("expected missing hook error, got %v", missingErr)
	}

	scriptPath := filepath.Join(worktreePath, "bootstrap")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	execErr := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		Ref:          "feat",
		Kind:         actions.WorktreeCreateGeneric,
	}, actions.BootstrapHook{Script: "bootstrap", TimeoutSeconds: 5})
	if execErr == nil || !strings.Contains(execErr.Error(), "bootstrap hook is not executable") {
		t.Fatalf("expected executable-bit error, got %v", execErr)
	}
}

func TestRunBootstrapHook_FailurePrefersScriptOutput(t *testing.T) {
	worktreePath := t.TempDir()
	scriptPath := filepath.Join(worktreePath, "bootstrap")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho first\necho useful failure >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		Ref:          "feat",
		Kind:         actions.WorktreeCreateGeneric,
	}, actions.BootstrapHook{Script: "bootstrap", TimeoutSeconds: 5})
	if err == nil {
		t.Fatal("expected hook failure")
	}
	if !strings.Contains(err.Error(), "useful failure") {
		t.Fatalf("expected script output, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), scriptPath) {
		t.Fatalf("expected script path in error, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected clean output without exit status, got %q", err.Error())
	}
}

func TestRunBootstrapHook_Timeout(t *testing.T) {
	worktreePath := t.TempDir()
	scriptPath := filepath.Join(worktreePath, "bootstrap")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := actions.RunBootstrapHook(actions.BootstrapContext{
		RepoPath:     "/repo",
		WorktreePath: worktreePath,
		Ref:          "feat",
		Kind:         actions.WorktreeCreateGeneric,
	}, actions.BootstrapHook{Script: "bootstrap", TimeoutSeconds: 1})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("expected timeout error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), scriptPath) {
		t.Fatalf("expected script path in timeout error, got %q", err.Error())
	}
}

func TestNormalizePullRequestWorktreeRef(t *testing.T) {
	for _, input := range []string{"123", "#123", "https://github.com/acme/project/pull/123/files"} {
		t.Run(input, func(t *testing.T) {
			ref, err := actions.NormalizePullRequestWorktreeRef(input)
			if err != nil {
				t.Fatalf("NormalizePullRequestWorktreeRef returned error: %v", err)
			}
			if ref != "123" {
				t.Fatalf("expected normalized ref 123, got %q", ref)
			}
		})
	}
}

func TestCreateBranch_FromHEAD(t *testing.T) {
	repoPath := setupRepo(t)
	head := runOutput(t, repoPath, "git", "rev-parse", "HEAD")
	initial := runOutput(t, repoPath, "git", "branch", "--show-current")

	if err := actions.CreateBranch(repoPath, "feature/one", ""); err != nil {
		t.Fatalf("CreateBranch returned error: %v", err)
	}

	got := runOutput(t, repoPath, "git", "rev-parse", "feature/one")
	if got != head {
		t.Fatalf("expected feature/one at HEAD %s, got %s", head, got)
	}
	current := runOutput(t, repoPath, "git", "branch", "--show-current")
	if current != initial {
		t.Fatalf("expected current branch to remain %s, got %q", initial, current)
	}
}

func TestCreateBranch_FromStartPoint(t *testing.T) {
	repoPath := setupRepo(t)
	initial := runOutput(t, repoPath, "git", "branch", "--show-current")
	mustRun(t, repoPath, "git", "branch", "base")
	mustRun(t, repoPath, "git", "checkout", "base")
	mustRun(t, repoPath, "git", "commit", "--allow-empty", "-m", "base change")
	base := runOutput(t, repoPath, "git", "rev-parse", "base")
	mustRun(t, repoPath, "git", "checkout", initial)

	if err := actions.CreateBranch(repoPath, "feature/from-base", "base"); err != nil {
		t.Fatalf("CreateBranch returned error: %v", err)
	}

	got := runOutput(t, repoPath, "git", "rev-parse", "feature/from-base")
	if got != base {
		t.Fatalf("expected feature/from-base at base %s, got %s", base, got)
	}
	current := runOutput(t, repoPath, "git", "branch", "--show-current")
	if current != initial {
		t.Fatalf("expected current branch to remain %s, got %q", initial, current)
	}
}

func TestCreateBranch_InvalidInputFails(t *testing.T) {
	repoPath := setupRepo(t)
	for _, input := range []string{"", "  ", "--bad"} {
		t.Run(input, func(t *testing.T) {
			if err := actions.CreateBranch(repoPath, input, ""); err == nil {
				t.Fatal("expected invalid branch name to fail")
			}
		})
	}
}

func TestCreateBranch_GitValidationErrorsAreReadable(t *testing.T) {
	repoPath := setupRepo(t)
	mustRun(t, repoPath, "git", "branch", "existing")

	for _, input := range []string{"existing", "bad name"} {
		t.Run(input, func(t *testing.T) {
			err := actions.CreateBranch(repoPath, input, "")
			if err == nil {
				t.Fatal("expected branch creation to fail")
			}
			if strings.Contains(err.Error(), "exit status") {
				t.Fatalf("expected clean git error without exit status, got %q", err.Error())
			}
		})
	}
}

func TestCreateBranch_StartPointStartingWithDashIsTreatedAsRef(t *testing.T) {
	repoPath := setupRepo(t)
	err := actions.CreateBranch(repoPath, "feature/from-dash", "--bad")
	if err == nil {
		t.Fatal("expected invalid start point to fail")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected clean git error without exit status, got %q", err.Error())
	}
	if out := runOutput(t, repoPath, "git", "branch", "--list", "feature/from-dash"); out != "" {
		t.Fatalf("expected branch not to be created, got %q", out)
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

func TestTerminalLaunch_TmuxRequiresInteractiveTTY(t *testing.T) {
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

	launch, err := actions.TerminalLaunch(worktreePath)
	if err != nil {
		t.Fatalf("TerminalLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("expected tmux launch outside a multiplexer to be interactive")
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

func TestAgentCommand_BuildsSupportedCommands(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:      command,
				LaunchID:     "launch-1",
				RepoPath:     "/repo",
				WorktreePath: "/repo/worktree",
				Branch:       "main",
				Commit:       "abcdef",
			})
			if err != nil {
				t.Fatalf("AgentCommand returned error: %v", err)
			}
			if cmd.Dir != "/repo/worktree" {
				t.Fatalf("expected command dir /repo/worktree, got %q", cmd.Dir)
			}
			if len(cmd.Args) == 0 || cmd.Args[0] != command {
				t.Fatalf("expected command args to start with %q, got %#v", command, cmd.Args)
			}
		})
	}
}

func TestAgentCommandAddsSessionMetadataEnvironment(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_AGENT":              "codex",
		"WTUI_LAUNCH_ID":          "launch-1",
		"WTUI_REPO_PATH":          "/repo",
		"WTUI_WORKTREE_PATH":      "/repo/worktree",
		"WTUI_BRANCH":             "main",
		"WTUI_COMMIT":             "abcdef",
		"WTUI_SESSION_STATE_ROOT": "/state/wtui/sessions/v1",
		"WTUI_PLAN_STATE_ROOT":    "/state/wtui/sessions/v1",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestAgentCommandAddsFlowEnvironment(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "flow/add-flow-mode",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanID:           "plan-1",
		PlanPath:         "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		PlanPhaseID:      "plan",
		FlowID:           "flow-1",
		FlowPhaseID:      "plan",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_FLOW_ID":         "flow-1",
		"WTUI_FLOW_PHASE_ID":   "plan",
		"WTUI_FLOW_STATE_ROOT": "/state/wtui/sessions/v1",
		"WTUI_PLAN_ID":         "plan-1",
		"WTUI_PLAN_PHASE_ID":   "plan",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestAgentCommandBuildsHeadlessCodexExecCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "flow/add-flow-mode",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		FlowID:           "flow-1",
		FlowPhaseID:      "implementation",
		Embedded:         true,
		Headless:         true,
		InitialPrompt:    "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	if len(args) != 5 {
		t.Fatalf("args = %#v, want command plus hook config, exec, and prompt", args)
	}
	if args[0] != "codex" || args[1] != "--config" || args[3] != "exec" || args[4] != "Implement this phase." {
		t.Fatalf("unexpected headless codex args: %#v", args)
	}
	if strings.Contains(strings.Join(args, "\x00"), "json") {
		t.Fatalf("headless codex args should stay human-readable, got %#v", args)
	}
	if !strings.Contains(args[2], "session-hook --provider codex") {
		t.Fatalf("expected codex hook config in args, got %#v", args)
	}
	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_AGENT":              "codex",
		"WTUI_FLOW_ID":            "flow-1",
		"WTUI_FLOW_PHASE_ID":      "implementation",
		"WTUI_FLOW_STATE_ROOT":    "/state/wtui/sessions/v1",
		"WTUI_WORKTREE_PATH":      "/repo/worktree",
		"WTUI_COMMIT":             "abcdef",
		"WTUI_SESSION_STATE_ROOT": "/state/wtui/sessions/v1",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestAgentCommandBuildsHeadlessClaudePrintCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "flow/add-flow-mode",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		FlowID:           "flow-1",
		FlowPhaseID:      "implementation",
		Embedded:         true,
		Headless:         true,
		InitialPrompt:    "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	// Headless claude streams stream-json so the embedded panel can render
	// readable progress; --verbose is mandatory alongside it.
	want := []string{"claude", "--print", "--settings", args[3], "--verbose", "--output-format", "stream-json", "--include-partial-messages", "Implement this phase."}
	if !slices.Equal(args, want) {
		t.Fatalf("headless claude args = %#v, want %#v", args, want)
	}
	if !strings.Contains(args[3], "session-hook --provider claude") {
		t.Fatalf("expected claude hook settings in args, got %#v", args)
	}
	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_AGENT":              "claude",
		"WTUI_FLOW_ID":            "flow-1",
		"WTUI_FLOW_PHASE_ID":      "implementation",
		"WTUI_FLOW_STATE_ROOT":    "/state/wtui/sessions/v1",
		"WTUI_WORKTREE_PATH":      "/repo/worktree",
		"WTUI_COMMIT":             "abcdef",
		"WTUI_SESSION_STATE_ROOT": "/state/wtui/sessions/v1",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestUsesStreamJSONOutput(t *testing.T) {
	cases := []struct {
		name string
		ctx  actions.AgentLaunchContext
		want bool
	}{
		{
			name: "embedded headless claude renders stream-json",
			ctx:  actions.AgentLaunchContext{Command: "claude", Embedded: true, Headless: true},
			want: true,
		},
		{
			name: "interactive claude does not",
			ctx:  actions.AgentLaunchContext{Command: "claude", Embedded: true, Headless: false},
			want: false,
		},
		{
			name: "headless codex does not",
			ctx:  actions.AgentLaunchContext{Command: "codex", Embedded: true, Headless: true},
			want: false,
		},
		{
			name: "non-embedded headless claude does not",
			ctx:  actions.AgentLaunchContext{Command: "claude", Embedded: false, Headless: true},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actions.UsesStreamJSONOutput(tc.ctx); got != tc.want {
				t.Fatalf("UsesStreamJSONOutput = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStreamJSONArgsMatchPredicate(t *testing.T) {
	// The renderer is attached iff UsesStreamJSONOutput is true, so the launch
	// args must carry stream-json under exactly the same condition. A mismatch
	// would dump raw JSON into the panel (or render plain text as events).
	for _, command := range []string{"claude", "codex"} {
		for _, embedded := range []bool{true, false} {
			for _, headless := range []bool{true, false} {
				for _, resume := range []string{"", "sess-1"} {
					for _, effort := range []string{"", "high"} {
						ctx := actions.AgentLaunchContext{
							Command:         command,
							Embedded:        embedded,
							Headless:        headless,
							ResumeSessionID: resume,
							ReasoningEffort: effort,
							WorktreePath:    "/repo/worktree",
						}
						cmd, err := actions.AgentCommand(ctx)
						if err != nil {
							// Rejected combinations (headless+resume,
							// resume+effort, unsupported effort) produce no args,
							// so they cannot mismatch the renderer.
							continue
						}
						hasStreamJSON := slices.Contains(cmd.Args, "stream-json")
						if want := actions.UsesStreamJSONOutput(ctx); hasStreamJSON != want {
							t.Fatalf("command=%s embedded=%v headless=%v resume=%q effort=%q: args stream-json=%v, UsesStreamJSONOutput=%v (must match); args=%#v",
								command, embedded, headless, resume, effort, hasStreamJSON, want, cmd.Args)
						}
					}
				}
			}
		}
	}
}

func TestAgentCommandCodexAddsReasoningEffortConfig(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		WorktreePath:     "/repo/worktree",
		SessionStateRoot: "/state/wtui/sessions/v1",
		FlowID:           "flow-1",
		FlowPhaseID:      "implementation",
		Embedded:         true,
		Headless:         true,
		ReasoningEffort:  "xhigh",
		InitialPrompt:    "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	effortIndex := slices.Index(args, "model_reasoning_effort=xhigh")
	execIndex := slices.Index(args, "exec")
	if effortIndex == -1 || effortIndex == 0 || args[effortIndex-1] != "--config" {
		t.Fatalf("expected codex reasoning effort config pair, got %#v", args)
	}
	if execIndex == -1 || effortIndex > execIndex {
		t.Fatalf("expected codex effort config before exec, got %#v", args)
	}
	if args[len(args)-1] != "Implement this phase." {
		t.Fatalf("expected prompt to remain final arg, got %#v", args)
	}
}

func TestAgentCommandClaudeAddsReasoningEffortArg(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		LaunchID:         "launch-1",
		WorktreePath:     "/repo/worktree",
		SessionStateRoot: "/state/wtui/sessions/v1",
		FlowID:           "flow-1",
		FlowPhaseID:      "implementation",
		Embedded:         true,
		Headless:         true,
		ReasoningEffort:  "max",
		InitialPrompt:    "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	effortFlagIndex := slices.Index(args, "--effort")
	if effortFlagIndex == -1 || effortFlagIndex+1 >= len(args) || args[effortFlagIndex+1] != "max" {
		t.Fatalf("expected claude --effort max arg pair, got %#v", args)
	}
	if args[len(args)-1] != "Implement this phase." {
		t.Fatalf("expected prompt to remain final arg, got %#v", args)
	}
}

func TestAgentCommandDefaultReasoningEffortOmitsProviderArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		effort  string
	}{
		{name: "codex empty", command: "codex", effort: ""},
		{name: "codex default", command: "codex", effort: "default"},
		{name: "claude default", command: "claude", effort: " default "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:         tt.command,
				WorktreePath:    "/repo/worktree",
				ReasoningEffort: tt.effort,
				InitialPrompt:   "Implement this phase.",
			})
			if err != nil {
				t.Fatalf("AgentCommand returned error: %v", err)
			}
			if strings.Contains(strings.Join(cmd.Args, "\x00"), "model_reasoning_effort") {
				t.Fatalf("default codex effort should not add config args, got %#v", cmd.Args)
			}
			if slices.Contains(cmd.Args, "--effort") {
				t.Fatalf("default claude effort should not add --effort, got %#v", cmd.Args)
			}
		})
	}
}

func TestAgentCommandRejectsUnsupportedReasoningEffort(t *testing.T) {
	_, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:         "codex",
		WorktreePath:    "/repo/worktree",
		ReasoningEffort: "max",
	})
	if err == nil {
		t.Fatal("expected unsupported reasoning effort error")
	}
	if !strings.Contains(err.Error(), "unsupported reasoning effort") {
		t.Fatalf("expected unsupported reasoning effort error, got %q", err.Error())
	}
}

func TestAgentCommandRejectsResumeWithReasoningEffort(t *testing.T) {
	_, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		WorktreePath:     "/repo/worktree",
		ResumeSessionID:  "session-1",
		ReasoningEffort:  "high",
		SessionStateRoot: "/state/wtui/sessions/v1",
	})
	if err == nil {
		t.Fatal("expected resume reasoning effort error")
	}
	if !strings.Contains(err.Error(), "reasoning effort") || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("expected resume reasoning effort error, got %q", err.Error())
	}
}

func TestAgentCommandCodexAddsModelBeforeExec(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:       "codex",
		WorktreePath:  "/repo/worktree",
		Embedded:      true,
		Headless:      true,
		Model:         "gpt-5.5",
		InitialPrompt: "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	modelIndex := slices.Index(args, "--model")
	execIndex := slices.Index(args, "exec")
	if modelIndex == -1 || modelIndex+1 >= len(args) || args[modelIndex+1] != "gpt-5.5" {
		t.Fatalf("expected codex --model gpt-5.5 pair, got %#v", args)
	}
	if execIndex == -1 || modelIndex > execIndex {
		t.Fatalf("expected codex model before exec, got %#v", args)
	}
	if args[len(args)-1] != "Implement this phase." {
		t.Fatalf("expected prompt to remain final arg, got %#v", args)
	}
}

func TestAgentCommandCodexInteractiveAddsModelAsGlobalFlag(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:       "codex",
		WorktreePath:  "/repo/worktree",
		Model:         " GPT-5.5 ",
		InitialPrompt: "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	modelIndex := slices.Index(args, "--model")
	if modelIndex == -1 || modelIndex+1 >= len(args) || args[modelIndex+1] != "gpt-5.5" {
		t.Fatalf("expected codex --model gpt-5.5 pair, got %#v", args)
	}
	if args[len(args)-1] != "Implement this phase." {
		t.Fatalf("expected prompt to remain final arg, got %#v", args)
	}
}

func TestAgentCommandClaudeAddsModelArg(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:       "claude",
		WorktreePath:  "/repo/worktree",
		Embedded:      true,
		Headless:      true,
		Model:         "claude-sonnet-5",
		InitialPrompt: "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	modelFlagIndex := slices.Index(args, "--model")
	printIndex := slices.Index(args, "--print")
	streamIndex := slices.Index(args, "stream-json")
	if modelFlagIndex == -1 || modelFlagIndex+1 >= len(args) || args[modelFlagIndex+1] != "claude-sonnet-5" {
		t.Fatalf("expected claude --model claude-sonnet-5 pair, got %#v", args)
	}
	if printIndex == -1 || streamIndex == -1 {
		t.Fatalf("expected headless stream-json args to remain, got %#v", args)
	}
	if args[len(args)-1] != "Implement this phase." {
		t.Fatalf("expected prompt to remain final arg, got %#v", args)
	}
}

func TestAgentCommandDefaultModelOmitsProviderArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		model   string
	}{
		{name: "codex empty", command: "codex", model: ""},
		{name: "codex default", command: "codex", model: "default"},
		{name: "claude default", command: "claude", model: " default "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:       tt.command,
				WorktreePath:  "/repo/worktree",
				Model:         tt.model,
				InitialPrompt: "Implement this phase.",
			})
			if err != nil {
				t.Fatalf("AgentCommand returned error: %v", err)
			}
			if slices.Contains(cmd.Args, "--model") {
				t.Fatalf("default model should not add --model, got %#v", cmd.Args)
			}
		})
	}
}

func TestAgentCommandRejectsUnsupportedModel(t *testing.T) {
	_, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:      "codex",
		WorktreePath: "/repo/worktree",
		Model:        "claude-sonnet-5",
	})
	if err == nil {
		t.Fatal("expected unsupported model error")
	}
	if !strings.Contains(err.Error(), "unsupported model") {
		t.Fatalf("expected unsupported model error, got %q", err.Error())
	}
}

func TestAgentCommandRejectsResumeWithModel(t *testing.T) {
	_, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		WorktreePath:     "/repo/worktree",
		ResumeSessionID:  "session-1",
		Model:            "claude-opus-4-8",
		SessionStateRoot: "/state/wtui/sessions/v1",
	})
	if err == nil {
		t.Fatal("expected resume model error")
	}
	if !strings.Contains(err.Error(), "model") || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("expected resume model error, got %q", err.Error())
	}
}

func TestAgentLaunchRejectsCodexAppModelOverride(t *testing.T) {
	_, err := actions.AgentLaunch(actions.AgentLaunchContext{
		Command:      "codex-app",
		WorktreePath: "/repo/worktree",
		Model:        "gpt-5.5",
	})
	if err == nil {
		t.Fatal("expected codex-app model error")
	}
	if !strings.Contains(err.Error(), "model") || !strings.Contains(err.Error(), "codex-app") {
		t.Fatalf("expected codex-app model error, got %q", err.Error())
	}
}

func TestAgentCommandBuildsEmbeddedInteractiveCodexCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:           "codex",
		LaunchID:          "launch-1",
		RepoPath:          "/repo",
		WorktreePath:      "/repo/worktree",
		SessionStateRoot:  "/state/wtui/sessions/v1",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		Embedded:          true,
		InitialPrompt:     "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	if len(args) != 4 {
		t.Fatalf("args = %#v, want command plus no-alt-screen and hook config", args)
	}
	if args[0] != "codex" || args[1] != "--no-alt-screen" || args[2] != "--config" {
		t.Fatalf("unexpected embedded interactive codex args: %#v", args)
	}
	if slices.Contains(args, "Implement this phase.") {
		t.Fatalf("tracked embedded interactive Flow codex args should not include prompt, got %#v", args)
	}
	if slices.Contains(args, "exec") {
		t.Fatalf("embedded interactive codex args should not include exec, got %#v", args)
	}
	if !strings.Contains(args[3], "session-hook --provider codex") {
		t.Fatalf("expected codex hook config in args, got %#v", args)
	}
}

func TestAgentCommandBuildsEmbeddedInteractiveClaudeCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:           "claude",
		LaunchID:          "launch-1",
		RepoPath:          "/repo",
		WorktreePath:      "/repo/worktree",
		SessionStateRoot:  "/state/wtui/sessions/v1",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		Embedded:          true,
		InitialPrompt:     "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	if len(args) != 3 {
		t.Fatalf("args = %#v, want command plus hook settings", args)
	}
	if args[0] != "claude" || args[1] != "--settings" {
		t.Fatalf("unexpected embedded interactive claude args: %#v", args)
	}
	if slices.Contains(args, "Implement this phase.") {
		t.Fatalf("tracked embedded interactive Flow claude args should not include prompt, got %#v", args)
	}
	if slices.Contains(args, "--print") {
		t.Fatalf("embedded interactive claude args should not include --print, got %#v", args)
	}
	if !strings.Contains(args[2], "session-hook --provider claude") {
		t.Fatalf("expected claude hook settings in args, got %#v", args)
	}
}

func TestAgentCommandEmbeddedInteractiveUntrackedFlowKeepsPromptArg(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		SessionStateRoot: "/state/wtui/sessions/v1",
		FlowID:           "flow-1",
		FlowPhaseID:      "implementation",
		Embedded:         true,
		InitialPrompt:    "Implement this phase.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != "Implement this phase." {
		t.Fatalf("last arg = %q, want prompt", got)
	}
}

func TestShouldPrefillEmbeddedPrompt(t *testing.T) {
	base := actions.AgentLaunchContext{
		Command:           "codex",
		WorktreePath:      "/repo/worktree",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		Embedded:          true,
		InitialPrompt:     "Implement this phase.",
	}
	if !actions.ShouldPrefillEmbeddedPrompt(base) {
		t.Fatalf("ShouldPrefillEmbeddedPrompt(%#v) = false, want true", base)
	}

	cases := []struct {
		name string
		mut  func(*actions.AgentLaunchContext)
	}{
		{name: "codex app", mut: func(ctx *actions.AgentLaunchContext) { ctx.Command = "codex-app" }},
		{name: "not embedded", mut: func(ctx *actions.AgentLaunchContext) { ctx.Embedded = false }},
		{name: "headless", mut: func(ctx *actions.AgentLaunchContext) { ctx.Headless = true }},
		{name: "resume", mut: func(ctx *actions.AgentLaunchContext) { ctx.ResumeSessionID = "session-1" }},
		{name: "empty prompt", mut: func(ctx *actions.AgentLaunchContext) { ctx.InitialPrompt = "" }},
		{name: "missing flow", mut: func(ctx *actions.AgentLaunchContext) { ctx.FlowID = "" }},
		{name: "missing phase", mut: func(ctx *actions.AgentLaunchContext) { ctx.FlowPhaseID = "" }},
		{name: "untracked", mut: func(ctx *actions.AgentLaunchContext) { ctx.FlowLaunchTracked = false }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := base
			tt.mut(&ctx)
			if actions.ShouldPrefillEmbeddedPrompt(ctx) {
				t.Fatalf("ShouldPrefillEmbeddedPrompt(%#v) = true, want false", ctx)
			}
		})
	}
}

func TestAgentCommandCodexAddsPlanEnvironmentAndPrompt(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanID:           "plan-1",
		PlanPath:         "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		InitialPrompt:    "Read the plan and begin implementation.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	env := envMap(cmd.Env)
	if env["WTUI_PLAN_ID"] != "plan-1" {
		t.Fatalf("WTUI_PLAN_ID = %q, want plan-1", env["WTUI_PLAN_ID"])
	}
	if env["WTUI_PLAN_PATH"] != "/state/wtui/sessions/v1/plans/plan-1/plan.md" {
		t.Fatalf("WTUI_PLAN_PATH = %q", env["WTUI_PLAN_PATH"])
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != "Read the plan and begin implementation." {
		t.Fatalf("final arg = %q, want initial prompt; args=%#v", got, cmd.Args)
	}
}

func TestAgentCommandAddsPlanPhaseEnvironment(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:         "codex",
		WorktreePath:    "/repo/worktree",
		PlanID:          "plan-1",
		PlanPath:        "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		PlanPhaseID:     "p2",
		PlanPhaseTitle:  "CLI subcommands",
		PlanPhaseStatus: "pending",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_PLAN_PHASE_ID":     "p2",
		"WTUI_PLAN_PHASE_TITLE":  "CLI subcommands",
		"WTUI_PLAN_PHASE_STATUS": "pending",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestAgentCommandReplacesInheritedWTUIEnvironment(t *testing.T) {
	t.Setenv("CUSTOM_KEEP", "still-here")
	for _, key := range []string{
		"WTUI_AGENT",
		"WTUI_LAUNCH_ID",
		"WTUI_REPO_PATH",
		"WTUI_WORKTREE_PATH",
		"WTUI_BRANCH",
		"WTUI_COMMIT",
		"WTUI_SESSION_STATE_ROOT",
		"WTUI_PLAN_STATE_ROOT",
		"WTUI_PLAN_ID",
		"WTUI_PLAN_PATH",
		"WTUI_PLAN_PHASE_ID",
		"WTUI_PLAN_PHASE_TITLE",
		"WTUI_PLAN_PHASE_STATUS",
	} {
		t.Setenv(key, "inherited-"+key)
	}

	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-2",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "ctx-commit",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanID:           "plan-2",
		PlanPath:         "/state/wtui/sessions/v1/plans/plan-2/plan.md",
		PlanPhaseID:      "phase-2",
		PlanPhaseTitle:   "Phase two",
		PlanPhaseStatus:  "pending",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	for key, want := range map[string]string{
		"WTUI_AGENT":              "codex",
		"WTUI_LAUNCH_ID":          "launch-2",
		"WTUI_REPO_PATH":          "/repo",
		"WTUI_WORKTREE_PATH":      "/repo/worktree",
		"WTUI_BRANCH":             "main",
		"WTUI_COMMIT":             "ctx-commit",
		"WTUI_SESSION_STATE_ROOT": "/state/wtui/sessions/v1",
		"WTUI_PLAN_STATE_ROOT":    "/state/wtui/sessions/v1",
		"WTUI_PLAN_ID":            "plan-2",
		"WTUI_PLAN_PATH":          "/state/wtui/sessions/v1/plans/plan-2/plan.md",
		"WTUI_PLAN_PHASE_ID":      "phase-2",
		"WTUI_PLAN_PHASE_TITLE":   "Phase two",
		"WTUI_PLAN_PHASE_STATUS":  "pending",
	} {
		got, count := envEntryValue(cmd.Env, key)
		if got != want || count != 1 {
			t.Fatalf("%s appears %d time(s) with value %q, want exactly one %q in env %#v", key, count, got, want, cmd.Env)
		}
	}
	if got := envMap(cmd.Env)["CUSTOM_KEEP"]; got != "still-here" {
		t.Fatalf("CUSTOM_KEEP = %q, want unrelated env preserved in %#v", got, cmd.Env)
	}
}

func TestAgentCommandClaudeAddsPromptAsFinalArg(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:       "claude",
		LaunchID:      "launch-1",
		RepoPath:      "/repo",
		WorktreePath:  "/repo/worktree",
		PlanID:        "plan-1",
		PlanPath:      "/state/plans/plan-1/plan.md",
		InitialPrompt: "Read the plan and begin implementation.",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	env := envMap(cmd.Env)
	if env["WTUI_PLAN_ID"] != "plan-1" || env["WTUI_PLAN_PATH"] != "/state/plans/plan-1/plan.md" {
		t.Fatalf("plan env not exported: %#v", env)
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != "Read the plan and begin implementation." {
		t.Fatalf("final arg = %q, want initial prompt; args=%#v", got, cmd.Args)
	}
}

func TestAgentCommandEmptyPromptLeavesProviderArgsUnchanged(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			withoutPlan, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:      command,
				LaunchID:     "launch-1",
				RepoPath:     "/repo",
				WorktreePath: "/repo/worktree",
			})
			if err != nil {
				t.Fatalf("AgentCommand without prompt returned error: %v", err)
			}
			withEmptyPrompt, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:       command,
				LaunchID:      "launch-1",
				RepoPath:      "/repo",
				WorktreePath:  "/repo/worktree",
				InitialPrompt: "",
			})
			if err != nil {
				t.Fatalf("AgentCommand with empty prompt returned error: %v", err)
			}
			if strings.Join(withEmptyPrompt.Args, "\x00") != strings.Join(withoutPlan.Args, "\x00") {
				t.Fatalf("empty prompt changed args:\nwithout=%#v\nwith=%#v", withoutPlan.Args, withEmptyPrompt.Args)
			}
		})
	}
}

func TestAgentCommandResolvesMissingCommitFromWorktree(t *testing.T) {
	repoPath := setupRepo(t)
	wantCommit := strings.TrimSpace(runOutput(t, repoPath, "git", "rev-parse", "HEAD"))

	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:      "codex",
		LaunchID:     "launch-1",
		RepoPath:     repoPath,
		WorktreePath: repoPath,
		Branch:       "main",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	if got := envMap(cmd.Env)["WTUI_COMMIT"]; got != wantCommit {
		t.Fatalf("WTUI_COMMIT = %q, want %q", got, wantCommit)
	}
}

func TestAgentCommandResolvesMissingCommitFromWorkingDir(t *testing.T) {
	repoPath := setupRepo(t)
	wantCommit := strings.TrimSpace(runOutput(t, repoPath, "git", "rev-parse", "HEAD"))

	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:         "codex",
		LaunchID:        "launch-1",
		RepoPath:        repoPath,
		WorkingDir:      repoPath,
		ResumeSessionID: "codex-session-1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	if got := envMap(cmd.Env)["WTUI_COMMIT"]; got != wantCommit {
		t.Fatalf("WTUI_COMMIT = %q, want %q", got, wantCommit)
	}
}

func TestAgentCommandWiresCodexSessionHook(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{
		"--config",
		"hooks.Stop",
		"session-hook --provider codex",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected codex launch args to contain %q, got %#v", want, cmd.Args)
		}
	}
}

func TestAgentCommandBuildsCodexResumeCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		ResumeSessionID:  "codex-session-1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	if cmd.Dir != "/repo/worktree" {
		t.Fatalf("command dir = %q, want /repo/worktree", cmd.Dir)
	}
	args := cmd.Args
	if len(args) != 5 {
		t.Fatalf("args = %#v, want command plus --config hook, resume, and id", args)
	}
	if args[0] != "codex" || args[1] != "--config" || args[3] != "resume" || args[4] != "codex-session-1" {
		t.Fatalf("unexpected codex resume args: %#v", args)
	}
	if !strings.Contains(args[2], "session-hook --provider codex") {
		t.Fatalf("expected codex hook config in args, got %#v", args)
	}

	env := envMap(cmd.Env)
	for key, want := range map[string]string{
		"WTUI_AGENT":              "codex",
		"WTUI_LAUNCH_ID":          "launch-1",
		"WTUI_REPO_PATH":          "/repo",
		"WTUI_WORKTREE_PATH":      "/repo/worktree",
		"WTUI_BRANCH":             "main",
		"WTUI_COMMIT":             "abcdef",
		"WTUI_SESSION_STATE_ROOT": "/state/wtui/sessions/v1",
		"WTUI_PLAN_STATE_ROOT":    "/state/wtui/sessions/v1",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q in env %#v", key, env[key], want, cmd.Env)
		}
	}
}

func TestAgentCommandWiresClaudeSessionHook(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{
		"--settings",
		"SessionEnd",
		"session-hook --provider claude",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected claude launch args to contain %q, got %#v", want, cmd.Args)
		}
	}
}

func TestAgentCommandEmbeddedCodexDisablesAltScreen(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:         "codex",
		WorktreePath:    "/repo/worktree",
		ResumeSessionID: "codex-session-1",
		Embedded:        true,
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	if len(cmd.Args) < 3 || cmd.Args[0] != "codex" || cmd.Args[1] != "--no-alt-screen" || cmd.Args[2] != "--config" {
		t.Fatalf("embedded codex args = %#v, want --no-alt-screen immediately after binary", cmd.Args)
	}

	nonEmbedded, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:      "codex",
		WorktreePath: "/repo/worktree",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	if slices.Contains(nonEmbedded.Args, "--no-alt-screen") {
		t.Fatalf("non-embedded codex args = %#v, should not include --no-alt-screen", nonEmbedded.Args)
	}

	claude, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:      "claude",
		WorktreePath: "/repo/worktree",
		Embedded:     true,
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	if slices.Contains(claude.Args, "--no-alt-screen") {
		t.Fatalf("embedded claude args = %#v, should not include codex flag", claude.Args)
	}
}

func TestAgentCommandBuildsClaudeResumeCommand(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "claude",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "docs",
		Commit:           "123456",
		SessionStateRoot: "/state/wtui/sessions/v1",
		ResumeSessionID:  "claude-session-1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	args := cmd.Args
	if len(args) != 5 {
		t.Fatalf("args = %#v, want command plus --settings hook, --resume, and id", args)
	}
	if args[0] != "claude" || args[1] != "--settings" || args[3] != "--resume" || args[4] != "claude-session-1" {
		t.Fatalf("unexpected claude resume args: %#v", args)
	}
	if !strings.Contains(args[2], "session-hook --provider claude") {
		t.Fatalf("expected claude hook settings in args, got %#v", args)
	}
}

func TestAgentCommandRejectsBlankResumeSessionID(t *testing.T) {
	for _, command := range []string{"claude", "codex"} {
		t.Run(command, func(t *testing.T) {
			_, err := actions.AgentCommand(actions.AgentLaunchContext{
				Command:         command,
				WorktreePath:    "/repo/worktree",
				ResumeSessionID: "   ",
			})
			if err == nil {
				t.Fatal("AgentCommand() error = nil, want blank resume session ID rejected")
			}
			if !strings.Contains(err.Error(), "session ID") {
				t.Fatalf("AgentCommand() error = %v, want mention of session ID", err)
			}
		})
	}
}

func TestAgentCommandTrimsResumeSessionID(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:         "claude",
		WorktreePath:    "/repo/worktree",
		ResumeSessionID: " claude-session-1 ",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	args := cmd.Args
	if args[len(args)-2] != "--resume" || args[len(args)-1] != "claude-session-1" {
		t.Fatalf("unexpected resume args: %#v", args)
	}
}

func TestAgentCommandResumeWorkingDirDoesNotOverwriteWorktreeMetadata(t *testing.T) {
	cmd, err := actions.AgentCommand(actions.AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		WorkingDir:       "/repo/worktree/subdir",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		ResumeSessionID:  "codex-session-1",
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}

	if cmd.Dir != "/repo/worktree/subdir" {
		t.Fatalf("command dir = %q, want /repo/worktree/subdir", cmd.Dir)
	}
	if got := envMap(cmd.Env)["WTUI_WORKTREE_PATH"]; got != "/repo/worktree" {
		t.Fatalf("WTUI_WORKTREE_PATH = %q, want /repo/worktree", got)
	}
}

func TestAgentCommand_RejectsMissingOrUnsupportedCommand(t *testing.T) {
	for _, command := range []string{"", "vim"} {
		t.Run(command, func(t *testing.T) {
			if _, err := actions.AgentCommand(actions.AgentLaunchContext{Command: command, WorktreePath: "/repo/worktree"}); err == nil {
				t.Fatal("expected AgentCommand error")
			}
		})
	}
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func envEntryValue(env []string, wantKey string) (string, int) {
	var value string
	var count int
	for _, entry := range env {
		key, entryValue, ok := strings.Cut(entry, "=")
		if ok && key == wantKey {
			value = entryValue
			count++
		}
	}
	return value, count
}

func TestAgentLaunch_RejectsMissingOrUnsupportedCommand(t *testing.T) {
	for _, command := range []string{"", "vim"} {
		t.Run(command, func(t *testing.T) {
			if _, err := actions.AgentLaunch(actions.AgentLaunchContext{Command: command, WorktreePath: "/repo/worktree"}); err == nil {
				t.Fatal("expected AgentLaunch error")
			}
		})
	}
}

func TestAgentLaunchResumeRejectsMissingOrUnsupportedCommand(t *testing.T) {
	for _, command := range []string{"", "vim"} {
		t.Run(command, func(t *testing.T) {
			if _, err := actions.AgentLaunch(actions.AgentLaunchContext{
				Command:         command,
				WorktreePath:    "/repo/worktree",
				ResumeSessionID: "session-1",
			}); err == nil {
				t.Fatal("expected AgentLaunch resume error")
			}
		})
	}
}
