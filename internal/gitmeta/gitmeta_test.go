package gitmeta_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/approachcontrol/approach/internal/gitmeta"
	"github.com/approachcontrol/approach/internal/testgit"
)

func TestResolvePlainRepoPathEqualsToplevel(t *testing.T) {
	repoPath := t.TempDir()
	testgit.Run(t, repoPath, "init")
	testgit.ConfigureRepo(t, repoPath)
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	testgit.Run(t, repoPath, "add", "README.md")
	testgit.Run(t, repoPath, "commit", "-m", "initial")

	info, err := gitmeta.Resolve(repoPath)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	toplevel := testgit.Output(t, repoPath, "rev-parse", "--show-toplevel")
	if info.RepoPath != toplevel {
		t.Fatalf("RepoPath = %q, want toplevel %q", info.RepoPath, toplevel)
	}
}

func TestResolveLinkedWorktreeRepoPathIsParentOfGitCommonDir(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo-worktrees", "feature")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("create repo dir: %v", err)
	}
	testgit.Run(t, repoPath, "init")
	testgit.ConfigureRepo(t, repoPath)
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	testgit.Run(t, repoPath, "add", "README.md")
	testgit.Run(t, repoPath, "commit", "-m", "initial")
	testgit.Run(t, repoPath, "worktree", "add", "-b", "feature/gitmeta", worktreePath, "HEAD")

	info, err := gitmeta.Resolve(worktreePath)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	commonDir := testgit.Output(t, worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	wantRepo := filepath.Dir(commonDir)
	if filepath.Base(commonDir) != ".git" {
		t.Fatalf("common dir = %q, want a .git directory", commonDir)
	}
	if info.RepoPath != wantRepo {
		t.Fatalf("RepoPath = %q, want parent of common dir %q", info.RepoPath, wantRepo)
	}
	if !info.Linked {
		t.Fatal("Linked = false, want true for a linked worktree")
	}
}

func TestResolveBareRepoPathIsCommonDir(t *testing.T) {
	root := t.TempDir()
	barePath := filepath.Join(root, "repo.git")
	testgit.Run(t, root, "init", "--bare", barePath)

	info, err := gitmeta.Resolve(barePath)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	commonDir := testgit.Output(t, barePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if info.RepoPath != filepath.Clean(commonDir) {
		t.Fatalf("RepoPath = %q, want common dir %q", info.RepoPath, commonDir)
	}
	if info.WorktreePath != "" {
		t.Fatalf("WorktreePath = %q, want empty for a bare repo", info.WorktreePath)
	}
}

func TestResolveWorktreeOfBareRepoUsesBareCommonDir(t *testing.T) {
	root := t.TempDir()
	bareRepoPath := filepath.Join(root, "container", ".git")
	worktreePath := filepath.Join(root, "worktrees", "feature")
	if err := os.MkdirAll(filepath.Dir(bareRepoPath), 0o755); err != nil {
		t.Fatalf("create bare parent dir: %v", err)
	}
	testgit.Run(t, root, "init", "--bare", bareRepoPath)
	testgit.Run(t, bareRepoPath, "worktree", "add", "-b", "feature/gitmeta", worktreePath)

	info, err := gitmeta.Resolve(worktreePath)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	commonDir := testgit.Output(t, worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if info.RepoPath != filepath.Clean(commonDir) {
		t.Fatalf("RepoPath = %q, want bare common dir %q", info.RepoPath, commonDir)
	}
	toplevel := testgit.Output(t, worktreePath, "rev-parse", "--show-toplevel")
	if info.WorktreePath != toplevel {
		t.Fatalf("WorktreePath = %q, want toplevel %q", info.WorktreePath, toplevel)
	}
}

func TestResolveNonRepoReturnsZeroInfoWithoutError(t *testing.T) {
	cwd := t.TempDir()

	info, err := gitmeta.Resolve(cwd)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil", err)
	}
	if info != (gitmeta.Info{}) {
		t.Fatalf("Resolve() = %#v, want zero Info", info)
	}
}
