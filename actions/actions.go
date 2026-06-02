package actions

import (
	"fmt"
	"os/exec"
	"strings"
)

// RemoveWorktree runs `git worktree remove` for the given worktree path,
// then prunes stale references to ensure the worktree no longer appears
// in listings.
func RemoveWorktree(repoPath, worktreePath string) error {
	err := exec.Command("git", "-C", repoPath, "worktree", "remove", worktreePath).Run()
	if err == nil {
		_ = exec.Command("git", "-C", repoPath, "worktree", "prune").Run()
	}
	return err
}

// ForceRemoveWorktree runs `git worktree remove --force`, then prunes
// stale references.
func ForceRemoveWorktree(repoPath, worktreePath string) error {
	err := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath).Run()
	if err == nil {
		_ = exec.Command("git", "-C", repoPath, "worktree", "prune").Run()
	}
	return err
}

// PruneWorktree runs `git worktree prune` to remove stale admin references.
func PruneWorktree(repoPath string) error {
	return exec.Command("git", "-C", repoPath, "worktree", "prune").Run()
}

// UnlockWorktree runs `git worktree unlock` for the given worktree path.
func UnlockWorktree(repoPath, worktreePath string) error {
	return exec.Command("git", "-C", repoPath, "worktree", "unlock", worktreePath).Run()
}

// Fetch runs `git fetch --prune` for the given repo or worktree path.
func Fetch(path string) error {
	return runGit(path, "fetch", "--prune")
}

// Pull runs `git pull --ff-only` for the given repo or worktree path.
func Pull(path string) error {
	return runGit(path, "pull", "--ff-only")
}

func runGit(path string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

// DeleteBranch runs `git branch -d`.
func DeleteBranch(repoPath, name string) error {
	return exec.Command("git", "-C", repoPath, "branch", "-d", name).Run()
}

// ForceDeleteBranch runs `git branch -D`.
func ForceDeleteBranch(repoPath, name string) error {
	return exec.Command("git", "-C", repoPath, "branch", "-D", name).Run()
}

// DropStash runs `git stash drop stash@{N}`.
func DropStash(repoPath string, index int) error {
	ref := fmt.Sprintf("stash@{%d}", index)
	return exec.Command("git", "-C", repoPath, "stash", "drop", ref).Run()
}

// CopyToClipboard copies text to the system clipboard using pbcopy.
func CopyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// OpenTerminal opens a new Terminal window at the given path.
func OpenTerminal(path string) error {
	return exec.Command("open", "-a", "Terminal", path).Run()
}

// OpenVSCode opens VSCode at the given path.
func OpenVSCode(path string) error {
	return exec.Command("code", path).Run()
}
