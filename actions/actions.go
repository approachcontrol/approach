package actions

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/brian-bell/wtui/agent"
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
		return errors.New(msg)
	}
	return nil
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
		// Keep the human-readable git stderr at the front while preserving
		// the original *exec.ExitError so callers can still inspect it.
		return "", fmt.Errorf("%s: %w", msg, err)
	}
	return worktreePath, nil
}

// CreateBranch creates a new branch without checking it out. When startPoint is
// empty, git creates the branch at HEAD.
func CreateBranch(repoPath, name, startPoint string) error {
	name = strings.TrimSpace(name)
	startPoint = strings.TrimSpace(startPoint)
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name cannot start with -: %q", name)
	}

	args := []string{"branch", "--", name}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	return runGit(repoPath, args...)
}

// CreatePullRequestWorktree fetches a pull request head into a local review
// branch, then creates a worktree for that branch.
func CreatePullRequestWorktree(repoPath, input string) (string, error) {
	pr, err := parsePullRequestInput(input)
	if err != nil {
		return "", err
	}
	if err := validatePullRequestRepo(repoPath, pr); err != nil {
		return "", err
	}

	branch := fmt.Sprintf("pr-%d", pr.Number)
	worktreePath := DefaultWorktreePath(repoPath, branch)
	if refExists(repoPath, branch) {
		return "", fmt.Errorf("branch %s already exists", branch)
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree path already exists: %s", worktreePath)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	refspec := fmt.Sprintf("refs/pull/%d/head:refs/heads/%s", pr.Number, branch)
	if err := runGit(repoPath, "fetch", "origin", refspec); err != nil {
		return "", fmt.Errorf("fetching PR #%d: %w", pr.Number, err)
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		if cleanupErr := runGit(repoPath, "branch", "-D", branch); cleanupErr != nil {
			return "", fmt.Errorf("%w; also failed to delete branch %s: %v", err, branch, cleanupErr)
		}
		return "", err
	}
	if err := runGit(repoPath, "worktree", "add", worktreePath, branch); err != nil {
		if cleanupErr := runGit(repoPath, "branch", "-D", branch); cleanupErr != nil {
			return "", fmt.Errorf("%w; also failed to delete branch %s: %v", err, branch, cleanupErr)
		}
		return "", err
	}
	return worktreePath, nil
}

// ValidatePullRequestWorktreeInput checks whether input is a supported PR
// number or URL for repoPath.
func ValidatePullRequestWorktreeInput(repoPath, input string) error {
	pr, err := parsePullRequestInput(input)
	if err != nil {
		return err
	}
	return validatePullRequestRepo(repoPath, pr)
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
	return runGit(repoPath, "branch", "-d", name)
}

// ForceDeleteBranch runs `git branch -D`.
func ForceDeleteBranch(repoPath, name string) error {
	return runGit(repoPath, "branch", "-D", name)
}

// DropStash runs `git stash drop stash@{N}`.
func DropStash(repoPath string, index int) error {
	ref := fmt.Sprintf("stash@{%d}", index)
	return runGit(repoPath, "stash", "drop", ref)
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

// OpenVSCode opens VSCode at the given path.
func OpenVSCode(path string) error {
	return exec.Command("code", path).Run()
}

// TerminalLaunchSpec describes how wtui should open an external process for a worktree.
// Interactive commands should be run with Bubble Tea's ExecProcess so the TUI
// releases the current terminal until the process exits.
type TerminalLaunchSpec struct {
	Cmd         *exec.Cmd
	Interactive bool
}

// AgentLaunch returns a safe, direct command for launching a supported coding
// agent in path.
func AgentLaunch(path, command string) (TerminalLaunchSpec, error) {
	command = agent.Normalize(command)
	if err := agent.Validate(command); err != nil {
		return TerminalLaunchSpec{}, err
	}
	cmd := exec.Command(command)
	cmd.Dir = path
	return TerminalLaunchSpec{Cmd: cmd, Interactive: true}, nil
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

	// $SHELL comes from the user's own environment, so we trust its intent;
	// we still validate it points at a runnable executable before exec'ing it
	// and fall back to /bin/sh when it is empty or invalid.
	shell := resolveShell(strings.TrimSpace(getenv("SHELL")), lookPath)
	cmd := exec.Command(shell)
	cmd.Dir = path
	return TerminalLaunchSpec{Cmd: cmd, Interactive: true}, nil
}

// resolveShell returns a runnable shell path. It accepts shell only if it is a
// regular file with an executable bit, or resolves via lookPath; otherwise it
// falls back to /bin/sh.
func resolveShell(shell string, lookPath lookPathFunc) string {
	const fallback = "/bin/sh"
	if shell == "" {
		return fallback
	}
	if info, err := os.Stat(shell); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
		return shell
	}
	if _, err := lookPath(shell); err == nil {
		return shell
	}
	return fallback
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

	h := fnv.New32a()
	_, _ = h.Write([]byte(hashPath))
	return fmt.Sprintf("%s-%08x", name, h.Sum32())
}

func refExists(repoPath, ref string) bool {
	if strings.HasPrefix(ref, "-") {
		return false
	}
	return exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run() == nil
}

type pullRequestInput struct {
	Number int
	Owner  string
	Repo   string
}

func parsePullRequestInput(input string) (pullRequestInput, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return pullRequestInput{}, fmt.Errorf("PR number or URL cannot be empty")
	}
	if strings.HasPrefix(input, "-") {
		return pullRequestInput{}, fmt.Errorf("PR number or URL cannot start with -: %q", input)
	}
	if strings.HasPrefix(input, "#") {
		input = strings.TrimPrefix(input, "#")
	}
	if number, ok := parsePositiveInt(input); ok {
		return pullRequestInput{Number: number}, nil
	}

	rawURL := input
	if strings.HasPrefix(rawURL, "github.com/") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return pullRequestInput{}, fmt.Errorf("invalid PR number or URL: %q", input)
	}
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "pull" {
			if number, ok := parsePositiveInt(parts[3]); ok {
				return pullRequestInput{Number: number, Owner: parts[0], Repo: strings.TrimSuffix(parts[1], ".git")}, nil
			}
		}
		return pullRequestInput{}, fmt.Errorf("invalid GitHub PR URL: %q", input)
	}
	return pullRequestInput{}, fmt.Errorf("unsupported PR URL host: %s", u.Host)
}

func parsePositiveInt(s string) (int, bool) {
	number, err := strconv.Atoi(s)
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

func validatePullRequestRepo(repoPath string, pr pullRequestInput) error {
	if pr.Owner == "" || pr.Repo == "" {
		return nil
	}
	owner, repo, ok := originGitHubRepo(repoPath)
	if !ok {
		return fmt.Errorf("cannot verify GitHub PR URL because origin is not a GitHub repository")
	}
	if !strings.EqualFold(owner, pr.Owner) || !strings.EqualFold(repo, pr.Repo) {
		return fmt.Errorf("PR URL repository %s/%s does not match origin %s/%s", pr.Owner, pr.Repo, owner, repo)
	}
	return nil
}

func originGitHubRepo(repoPath string) (owner, repo string, ok bool) {
	out, err := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", "", false
	}
	return parseGitHubRemote(strings.TrimSpace(string(out)))
}

func parseGitHubRemote(remote string) (owner, repo string, ok bool) {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@github.com:") {
		path := strings.TrimSuffix(strings.Trim(strings.TrimPrefix(remote, "git@github.com:"), "/"), ".git")
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
		return "", "", false
	}
	u, err := url.Parse(remote)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], strings.TrimSuffix(parts[1], ".git"), true
	}
	return "", "", false
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
