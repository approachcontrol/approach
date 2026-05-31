package actions

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type commandSpec struct {
	name string
	args []string
	dir  string
}

type lookPathFunc func(string) (string, error)
type getenvFunc func(string) string

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

// CopyToClipboard copies text to the system clipboard.
func CopyToClipboard(text string) error {
	spec, err := selectClipboardCommand(runtime.GOOS, exec.LookPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(spec.name, spec.args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// OpenTerminal opens a new terminal at the given path.
func OpenTerminal(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	spec, err := selectTerminalCommand(runtime.GOOS, path, os.Getenv, exec.LookPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(spec.name, spec.args...)
	cmd.Dir = spec.dir
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// OpenVSCode opens VSCode at the given path.
func OpenVSCode(path string) error {
	return exec.Command("code", path).Run()
}

func selectClipboardCommand(goos string, lookPath lookPathFunc) (commandSpec, error) {
	if goos == "darwin" {
		return selectRequiredCommand("pbcopy", nil, lookPath, "clipboard command pbcopy not found")
	}

	if goos == "linux" {
		candidates := []commandSpec{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
		for _, candidate := range candidates {
			if _, err := lookPath(candidate.name); err == nil {
				return candidate, nil
			}
		}
		return commandSpec{}, fmt.Errorf("no supported clipboard command installed; install wl-copy, xclip, or xsel")
	}

	return commandSpec{}, fmt.Errorf("clipboard copy is not supported on %s", goos)
}

func selectTerminalCommand(goos, path string, getenv getenvFunc, lookPath lookPathFunc) (commandSpec, error) {
	if getenv("TMUX") != "" {
		if _, err := lookPath("tmux"); err == nil {
			return commandSpec{name: "tmux", args: []string{"new-window", "-c", path}}, nil
		}
	}

	if getenv("ZELLIJ") != "" {
		if _, err := lookPath("zellij"); err == nil {
			return commandSpec{name: "zellij", args: []string{"action", "new-pane", "--cwd", path}}, nil
		}
	}

	if terminal := strings.TrimSpace(getenv("TERMINAL")); terminal != "" {
		return terminalCommand(goos, terminal, path, lookPath)
	}

	if goos == "darwin" {
		return commandSpec{name: "open", args: []string{"-a", "Terminal", path}}, nil
	}

	if goos == "linux" {
		if _, err := lookPath("xdg-open"); err == nil {
			return commandSpec{name: "xdg-open", args: []string{path}}, nil
		}
		if shell := strings.TrimSpace(getenv("SHELL")); shell != "" {
			return commandSpec{name: shell, dir: path}, nil
		}
		return commandSpec{}, fmt.Errorf("no terminal launcher found; set TERMINAL or install xdg-open")
	}

	return commandSpec{}, fmt.Errorf("terminal launch is not supported on %s", goos)
}

func terminalCommand(goos, terminal, path string, lookPath lookPathFunc) (commandSpec, error) {
	fields := strings.Fields(terminal)
	if len(fields) == 0 {
		return commandSpec{}, fmt.Errorf("TERMINAL is empty")
	}
	name := fields[0]
	args := fields[1:]

	if _, err := lookPath(name); err == nil {
		return commandSpec{name: name, args: args, dir: path}, nil
	}

	if goos == "darwin" {
		return commandSpec{name: "open", args: []string{"-a", terminal, path}}, nil
	}

	return commandSpec{}, fmt.Errorf("TERMINAL is set to %q, but that command was not found", terminal)
}

func selectRequiredCommand(name string, args []string, lookPath lookPathFunc, missing string) (commandSpec, error) {
	if _, err := lookPath(name); err != nil {
		return commandSpec{}, fmt.Errorf("%s", missing)
	}
	return commandSpec{name: name, args: args}, nil
}
