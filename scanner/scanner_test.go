package scanner_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/scanner"
)

func makeRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func makeBareRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "config"), []byte("[core]\n\tbare = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = hermeticGitEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func hermeticGitEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_CONFIG_") || strings.HasPrefix(entry, "GIT_TEMPLATE_DIR=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func makeCommittedGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "init")
	runGit(t, path, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath="+t.TempDir(),
		"commit", "-m", "initial",
	)
}

func addLinkedWorktree(t *testing.T, repoDir, worktreeDir, branch string) {
	t.Helper()
	runGit(t, repoDir, "branch", branch)
	runGit(t, repoDir, "worktree", "add", worktreeDir, branch)
}

func assertOnlyRepo(t *testing.T, repos []scanner.Repo, want scanner.Repo) {
	t.Helper()
	if len(repos) != 1 {
		t.Fatalf("expected only repo %+v, got %+v", want, repos)
	}
	if repos[0].Path != want.Path {
		t.Fatalf("expected Path %q, got %q", want.Path, repos[0].Path)
	}
	if repos[0].DisplayName != want.DisplayName {
		t.Fatalf("expected DisplayName %q, got %q", want.DisplayName, repos[0].DisplayName)
	}
	if repos[0].IsBare != want.IsBare {
		t.Fatalf("expected IsBare %t, got %t", want.IsBare, repos[0].IsBare)
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
	if repos[0].IsBare {
		t.Error("expected normal repo IsBare=false")
	}
}

func TestScan_DiscoversTopLevelBareRepo(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "project.git")
	makeBareRepo(t, repoDir)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("expected Path %q, got %q", repoDir, repos[0].Path)
	}
	if repos[0].DisplayName != "project.git" {
		t.Errorf("expected DisplayName %q, got %q", "project.git", repos[0].DisplayName)
	}
	if !repos[0].IsBare {
		t.Error("expected bare repo IsBare=true")
	}
}

func TestScan_DiscoversNestedBareRepo(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "org", "project.git")
	makeBareRepo(t, repoDir)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].DisplayName != "org/project.git" {
		t.Errorf("expected DisplayName %q, got %q", "org/project.git", repos[0].DisplayName)
	}
	if !repos[0].IsBare {
		t.Error("expected nested bare repo IsBare=true")
	}
}

func TestScan_BareRepoCarriesIsBare(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "real.git")
	if err := exec.Command("git", "init", "--bare", repoDir).Run(); err != nil {
		t.Fatalf("git init --bare failed: %v", err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if !repos[0].IsBare {
		t.Fatalf("expected real bare repo IsBare=true, got %+v", repos[0])
	}
}

func TestScan_DoesNotTreatRandomGitLikeDirectoryAsBare(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project.git")
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("not a ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected no repos, got %+v", repos)
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
	if repos[0].IsBare {
		t.Error("expected git-file worktree IsBare=false")
	}
}

func TestScan_ExcludesTopLevelLinkedWorktreeCheckout(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "wtui")
	worktreeDir := filepath.Join(root, "wtui-bootstrap-hooks")

	makeCommittedGitRepo(t, repoDir)
	addLinkedWorktree(t, repoDir, worktreeDir, "bootstrap-hooks")

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root, MaxDepth: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "wtui", IsBare: false})
}

func TestScan_ExcludesLinkedWorktreeCheckoutWithRelativeRoot(t *testing.T) {
	parent := t.TempDir()
	rootName := "scan-root"
	root := filepath.Join(parent, rootName)
	repoDir := filepath.Join(root, "wtui")
	worktreeDir := filepath.Join(root, "wtui-bootstrap-hooks")

	makeCommittedGitRepo(t, repoDir)
	addLinkedWorktree(t, repoDir, worktreeDir, "bootstrap-hooks")

	t.Chdir(parent)
	repos, err := scanner.Scan(scanner.ScanOptions{Root: rootName, MaxDepth: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolvedRoot, err := filepath.Abs(rootName)
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: filepath.Join(resolvedRoot, "wtui"), DisplayName: "wtui", IsBare: false})
}

func TestScan_ExcludesNestedLinkedWorktreeCheckout(t *testing.T) {
	root := t.TempDir()
	orgDir := filepath.Join(root, "org")
	repoDir := filepath.Join(orgDir, "wtui")
	worktreeDir := filepath.Join(orgDir, "wtui-bootstrap-hooks")

	makeCommittedGitRepo(t, repoDir)
	addLinkedWorktree(t, repoDir, worktreeDir, "bootstrap-hooks")

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "org/wtui", IsBare: false})
}

func TestScan_GitFileNonWorktreeRepoDiscovered(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "separate")
	gitDir := filepath.Join(root, "external-git-dir")

	runGit(t, root, "init", "--separate-git-dir", gitDir, repoDir)

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "separate", IsBare: false})
}

func TestScan_WorktreesShapedGitFileNonWorktreeRepoDiscovered(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "odd-shape")
	adminDir := filepath.Join(root, "external", "worktrees", "odd-shape")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ../external/worktrees/odd-shape\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(repoDir, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../not-the-owner\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "odd-shape", IsBare: false})
}

func TestScan_WorktreesShapedGitFileWithoutCommonGitDirDiscovered(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "odd-shape")
	adminDir := filepath.Join(root, "external", "worktrees", "odd-shape")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ../external/worktrees/odd-shape\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(repoDir, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "odd-shape", IsBare: false})
}

func TestScan_WorktreesShapedGitFileInvalidCommonHeadDiscovered(t *testing.T) {
	root := t.TempDir()
	commonDir := filepath.Join(t.TempDir(), "external")
	adminDir := filepath.Join(commonDir, "worktrees", "odd-shape")
	repoDir := filepath.Join(root, "odd-shape")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(commonDir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(commonDir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "HEAD"), []byte("not a ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(repoDir, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "odd-shape", IsBare: false})
}

func TestScan_WorktreesShapedGitFileSHA256DetachedCommonHeadExcluded(t *testing.T) {
	root := t.TempDir()
	commonDir := filepath.Join(t.TempDir(), "external")
	adminDir := filepath.Join(commonDir, "worktrees", "sha256-worktree")
	repoDir := filepath.Join(root, "sha256-worktree")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(commonDir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(commonDir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "HEAD"), []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(repoDir, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected linked SHA-256 worktree to be excluded, got %+v", repos)
	}
}

func TestScan_WorktreesShapedGitFileWrongAdminGitdirDiscovered(t *testing.T) {
	root := t.TempDir()
	commonDir := filepath.Join(t.TempDir(), "external")
	adminDir := filepath.Join(commonDir, "worktrees", "odd-shape")
	repoDir := filepath.Join(root, "odd-shape")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBareRepo(t, commonDir)
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(root, "other", ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "odd-shape", IsBare: false})
}

func TestScan_MalformedGitFileRepoDiscovered(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "malformed")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("not gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOnlyRepo(t, repos, scanner.Repo{Path: repoDir, DisplayName: "malformed", IsBare: false})
}
