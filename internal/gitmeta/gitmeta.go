// Package gitmeta resolves worktree, repository, branch, and commit from a
// working directory by asking git. Callers apply the result only to empty
// fields; git errors stay inside this package.
package gitmeta

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info is the git identity of a working directory. Zero value means the
// directory is not a repository, or git could not be asked.
type Info struct {
	WorktreePath string
	RepoPath     string
	Branch       string
	Commit       string
	Linked       bool
}

// Resolve asks git for the worktree, repository, branch, and commit at cwd.
// A non-repository or unusable cwd returns a zero Info and a nil error so
// callers can keep filling fields from other sources.
func Resolve(cwd string) (Info, error) {
	if cwd == "" {
		return Info{}, nil
	}
	worktreePath := ""
	if out, err := gitOutput(cwd, "rev-parse", "--show-toplevel"); err == nil {
		worktreePath = out
	}
	gitCommonDir := ""
	if out, err := gitOutput(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		gitCommonDir = out
	}
	gitDir := ""
	if out, err := gitOutput(cwd, "rev-parse", "--path-format=absolute", "--git-dir"); err == nil {
		gitDir = out
	}
	isBare := false
	if out, err := gitOutput(cwd, "rev-parse", "--is-bare-repository"); err == nil {
		isBare = out == "true"
	}
	commonDirIsBare := false
	if gitCommonDir != "" {
		if out, err := gitOutput(gitCommonDir, "rev-parse", "--is-bare-repository"); err == nil {
			commonDirIsBare = out == "true"
		}
	}
	repoPath := repoPathFromGitMetadata(worktreePath, gitDir, gitCommonDir, isBare, commonDirIsBare)
	info := Info{
		Linked: isLinkedWorktreeGitDir(gitDir, gitCommonDir),
	}
	if repoPath != "" {
		info.RepoPath = repoPath
	} else if worktreePath != "" {
		info.RepoPath = worktreePath
	}
	info.WorktreePath = worktreePath
	if out, err := gitOutput(cwd, "branch", "--show-current"); err == nil {
		info.Branch = out
	}
	if out, err := gitOutput(cwd, "rev-parse", "HEAD"); err == nil {
		info.Commit = out
	}
	return info, nil
}

func repoPathFromGitMetadata(worktreePath, gitDir, commonDir string, isBare, commonDirIsBare bool) string {
	if isBare {
		if commonDir != "" {
			return filepath.Clean(commonDir)
		}
		if gitDir == "" {
			return ""
		}
		return filepath.Clean(gitDir)
	}
	if commonDir != "" && gitDir != "" && isLinkedWorktreeGitDir(gitDir, commonDir) {
		if commonDirIsBare {
			return filepath.Clean(commonDir)
		}
		if filepath.Base(filepath.Clean(commonDir)) != ".git" {
			return worktreePath
		}
		return repoPathFromGitCommonDir(commonDir)
	}
	if worktreePath != "" {
		return worktreePath
	}
	if commonDir != "" {
		return repoPathFromGitCommonDir(commonDir)
	}
	if gitDir == "" {
		return ""
	}
	return repoPathFromGitCommonDir(gitDir)
}

func isLinkedWorktreeGitDir(gitDir, commonDir string) bool {
	rel, err := filepath.Rel(filepath.Join(filepath.Clean(commonDir), "worktrees"), filepath.Clean(gitDir))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func repoPathFromGitCommonDir(commonDir string) string {
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir
}

func gitOutput(cwd string, args ...string) (string, error) {
	cleaned, err := plausibleGitCWD(cwd)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", append([]string{"-C", cleaned}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func plausibleGitCWD(cwd string) (string, error) {
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "." || !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("git cwd must be an absolute path")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("inspect git cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git cwd %q is not a directory", cwd)
	}
	return cwd, nil
}
