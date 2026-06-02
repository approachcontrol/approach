package scanner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brian-bell/wtui/scanner"
)

func makeRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestScan_DiscoversTopLevelRepo(t *testing.T) {
	root := t.TempDir()

	repoDir := filepath.Join(root, "my-repo")
	makeRepo(t, repoDir)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].DisplayName != "my-repo" {
		t.Errorf("expected DisplayName %q, got %q", "my-repo", repos[0].DisplayName)
	}
	if repos[0].Path != repoDir {
		t.Errorf("expected Path %q, got %q", repoDir, repos[0].Path)
	}
}

func TestScan_ExcludesWorktreesDirs(t *testing.T) {
	root := t.TempDir()

	makeRepo(t, filepath.Join(root, "app"))
	makeRepo(t, filepath.Join(root, "app-worktrees"))

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].DisplayName != "app" {
		t.Errorf("expected %q, got %q", "app", repos[0].DisplayName)
	}
}

func TestScan_SkipsNonRepoDirs(t *testing.T) {
	root := t.TempDir()

	// A directory without .git — should be skipped
	os.MkdirAll(filepath.Join(root, "notes"), 0o755)
	// A file — should be skipped
	os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos, got %d", len(repos))
	}
}

func TestScan_DiscoversNestedRepos(t *testing.T) {
	root := t.TempDir()

	// org/ is not a repo, but org/project-a is
	os.MkdirAll(filepath.Join(root, "org"), 0o755)
	makeRepo(t, filepath.Join(root, "org", "project-a"))
	makeRepo(t, filepath.Join(root, "org", "project-b"))

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].DisplayName != "org/project-a" {
		t.Errorf("expected %q, got %q", "org/project-a", repos[0].DisplayName)
	}
	if repos[1].DisplayName != "org/project-b" {
		t.Errorf("expected %q, got %q", "org/project-b", repos[1].DisplayName)
	}
}

func TestScan_SortsAlphabetically(t *testing.T) {
	root := t.TempDir()

	makeRepo(t, filepath.Join(root, "zulu"))
	makeRepo(t, filepath.Join(root, "alpha"))
	makeRepo(t, filepath.Join(root, "mike"))

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(repos))
	}
	expected := []string{"alpha", "mike", "zulu"}
	for i, name := range expected {
		if repos[i].DisplayName != name {
			t.Errorf("position %d: expected %q, got %q", i, name, repos[i].DisplayName)
		}
	}
}

func TestScan_RespectsDepthLimit(t *testing.T) {
	root := t.TempDir()

	// 3 levels deep — should NOT be discovered
	makeRepo(t, filepath.Join(root, "a", "b", "c"))

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos, got %d", len(repos))
	}
}

func TestScan_GitFileWorktreeDiscovered(t *testing.T) {
	root := t.TempDir()

	// .git as a regular file (worktree/submodule pointer) should be discovered
	// as a repo, just like a normal .git directory.
	repoDir := filepath.Join(root, "wt-repo")
	os.MkdirAll(repoDir, 0o755)
	os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: /some/path"), 0o644)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (worktree pointer discovered), got %d", len(repos))
	}
	if repos[0].DisplayName != "wt-repo" {
		t.Fatalf("expected DisplayName %q, got %q", "wt-repo", repos[0].DisplayName)
	}
}
