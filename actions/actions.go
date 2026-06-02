package actions

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

type commandSpec struct {
	name string
	args []string
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

// CreateWorktree creates a new worktree from an existing branch/tag/ref, or
// creates a new branch with that name from HEAD when the input does not resolve.
func CreateWorktree(repoPath, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("worktree ref cannot be empty")
	}
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("worktree ref cannot start with -: %q", ref)
	}

	worktreePath := DefaultWorktreePath(repoPath, ref)
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", err
	}

	args := []string{"-C", repoPath, "worktree", "add"}
	if refExists(repoPath, ref) {
		args = append(args, worktreePath, ref)
	} else {
		args = append(args, "-b", ref, worktreePath)
	}

	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%s", msg)
	}
	return worktreePath, nil
}

// DefaultWorktreePath returns the conventional sibling path used for new
// worktrees: <repo>-worktrees/<branch-or-tag>.
func DefaultWorktreePath(repoPath, ref string) string {
	base := filepath.Base(repoPath)
	parent := filepath.Dir(repoPath)
	return filepath.Join(parent, base+"-worktrees", sanitizePathPart(ref))
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

// OpenTerminal opens a non-interactive multiplexer-backed terminal command for
// the given path. Interactive launch specs need a caller-provided TTY; use
// TerminalLaunch directly with Bubble Tea's ExecProcess for those.
func OpenTerminal(path string) error {
	if info, err := os.Stat(path); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("terminal path is not a directory: %s", path)
	}
	launch, err := TerminalLaunch(path)
	if err != nil {
		return err
	}
	if launch.Interactive {
		return fmt.Errorf("terminal launch for %s requires an interactive TTY; use TerminalLaunch with ExecProcess", path)
	}
	return launch.Cmd.Run()
}

// OpenVSCode opens VSCode at the given path.
func OpenVSCode(path string) error {
	return exec.Command("code", path).Run()
}

// TerminalLaunchSpec describes how wtui should open a shell for a worktree.
// Interactive commands should be run with Bubble Tea's ExecProcess so the TUI
// releases the current terminal until the multiplexer exits.
type TerminalLaunchSpec struct {
	Cmd         *exec.Cmd
	Interactive bool
}

// TerminalLaunch returns a command that opens or switches to a multiplexer
// session for path. It adapts to the current environment:
//   - inside Zellij: switch to a Zellij session with the worktree name
//   - inside tmux: create the tmux session if needed, then switch-client
//   - outside a multiplexer: prefer tmux, Zellij, $TERMINAL, then a platform/shell fallback
func TerminalLaunch(path string) (TerminalLaunchSpec, error) {
	return terminalLaunch(path, runtime.GOOS, os.Getenv, exec.LookPath)
}

func terminalLaunch(path, goos string, getenv getenvFunc, lookPath lookPathFunc) (TerminalLaunchSpec, error) {
	sessionName := WorktreeSessionName(path)

	switch {
	case getenv("ZELLIJ") != "" && commandExists("zellij", lookPath):
		return TerminalLaunchSpec{
			Cmd: exec.Command("zellij", "action", "switch-session", sessionName, "--cwd", path),
		}, nil
	case getenv("TMUX") != "" && commandExists("tmux", lookPath):
		return TerminalLaunchSpec{
			Cmd: exec.Command("sh", "-c", tmuxSwitchScript, "wtui", sessionName, path),
		}, nil
	case commandExists("tmux", lookPath):
		if goos == "darwin" && commandExists("osascript", lookPath) {
			return TerminalLaunchSpec{
				Cmd: externalTerminalCommand(tmuxAttachCommand(sessionName, path)),
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd:         exec.Command("tmux", "new-session", "-A", "-s", sessionName, "-c", path),
			Interactive: true,
		}, nil
	case commandExists("zellij", lookPath):
		if goos == "darwin" && commandExists("osascript", lookPath) {
			return TerminalLaunchSpec{
				Cmd: externalTerminalCommand(zellijAttachCommand(sessionName, path)),
			}, nil
		}
		cmd := exec.Command("zellij", "attach", "--create", sessionName)
		cmd.Dir = path
		return TerminalLaunchSpec{Cmd: cmd, Interactive: true}, nil
	}

	if terminal := strings.TrimSpace(getenv("TERMINAL")); terminal != "" {
		return terminalLaunchFromEnv(goos, terminal, path, lookPath)
	}

	if goos == "darwin" && commandExists("open", lookPath) {
		return TerminalLaunchSpec{
			Cmd: exec.Command("open", "-a", "Terminal", path),
		}, nil
	}

	shell := strings.TrimSpace(getenv("SHELL"))
	if shell == "" {
		shell = "sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = path
	return TerminalLaunchSpec{Cmd: cmd, Interactive: true}, nil
}

func terminalLaunchFromEnv(goos, terminal, path string, lookPath lookPathFunc) (TerminalLaunchSpec, error) {
	fields := strings.Fields(terminal)
	if len(fields) == 0 {
		return TerminalLaunchSpec{}, fmt.Errorf("TERMINAL is empty")
	}

	name := fields[0]
	args := fields[1:]
	if commandExists(name, lookPath) {
		cmd := exec.Command(name, args...)
		cmd.Dir = path
		return TerminalLaunchSpec{Cmd: cmd}, nil
	}

	if goos == "darwin" {
		return TerminalLaunchSpec{
			Cmd: exec.Command("open", "-a", name, path),
		}, nil
	}

	return TerminalLaunchSpec{}, fmt.Errorf("TERMINAL is set to %q, but that command was not found", terminal)
}

func selectClipboardCommand(goos string, lookPath lookPathFunc) (commandSpec, error) {
	if goos == "darwin" {
		if !commandExists("pbcopy", lookPath) {
			return commandSpec{}, errors.New("clipboard command pbcopy not found")
		}
		return commandSpec{name: "pbcopy"}, nil
	}

	if goos == "linux" {
		candidates := []commandSpec{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
		for _, candidate := range candidates {
			if commandExists(candidate.name, lookPath) {
				return candidate, nil
			}
		}
		return commandSpec{}, fmt.Errorf("no supported clipboard command installed; install wl-copy, xclip, or xsel")
	}

	return commandSpec{}, fmt.Errorf("clipboard copy is not supported on %s", goos)
}

// WorktreeSessionName returns a tmux/Zellij-safe session name derived from
// the worktree directory name plus a stable path fingerprint.
func WorktreeSessionName(path string) string {
	cleanPath := filepath.Clean(path)
	hashPath := cleanPath
	if absPath, err := filepath.Abs(cleanPath); err == nil {
		hashPath = absPath
	}

	name := filepath.Base(cleanPath)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		allowed := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	name = strings.Trim(b.String(), ".-")
	if name == "" {
		name = "worktree"
	}

	sum := sha1.Sum([]byte(hashPath))
	return fmt.Sprintf("%s-%x", name, sum[:4])
}

func refExists(repoPath, ref string) bool {
	if strings.HasPrefix(ref, "-") {
		return false
	}
	return exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run() == nil
}

func sanitizePathPart(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "refs/heads/")
	s = strings.TrimPrefix(s, "refs/tags/")
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		" ", "-",
	)
	s = replacer.Replace(s)
	s = strings.Trim(s, ".-")
	if s == "" {
		return "worktree"
	}
	return s
}

const tmuxSwitchScript = `
session=$1
path=$2
tmux has-session -t "$session" 2>/dev/null || tmux new-session -d -s "$session" -c "$path"
tmux switch-client -t "$session"
`

func commandExists(name string, lookPath lookPathFunc) bool {
	_, err := lookPath(name)
	return err == nil
}

func externalTerminalCommand(shellCommand string) *exec.Cmd {
	return exec.Command(
		"osascript",
		"-e", fmt.Sprintf(`tell application "Terminal" to do script %q`, shellCommand),
		"-e", `tell application "Terminal" to activate`,
	)
}

func tmuxAttachCommand(sessionName, path string) string {
	return "tmux new-session -A -s " + shellQuote(sessionName) + " -c " + shellQuote(path)
}

func zellijAttachCommand(sessionName, path string) string {
	return "cd " + shellQuote(path) + " && zellij attach --create " + shellQuote(sessionName)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
