package actions

import (
	"context"
	"encoding/json"
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
	"sync"
	"time"
	"unicode"

	"github.com/brian-bell/wtui/agent"
)

type commandSpec struct {
	name string
	args []string
}

type envVar struct {
	key   string
	value string
}

type lookPathFunc func(string) (string, error)
type getenvFunc func(string) string

// BootstrapHook configures a script to run after a worktree is created.
type BootstrapHook struct {
	Script         string
	TimeoutSeconds int
}

// WorktreeCreateKind identifies which create flow produced the worktree.
type WorktreeCreateKind int

const (
	WorktreeCreateGeneric WorktreeCreateKind = iota
	WorktreeCreatePullRequest
)

func (k WorktreeCreateKind) String() string {
	switch k {
	case WorktreeCreatePullRequest:
		return "pull_request"
	default:
		return "generic"
	}
}

// BootstrapContext describes the worktree creation that triggered a hook.
type BootstrapContext struct {
	RepoPath     string
	WorktreePath string
	Ref          string
	Kind         WorktreeCreateKind
}

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

// MoveWorktree runs `git worktree move` for a linked worktree and returns the
// resolved destination path on success.
func MoveWorktree(repoPath, worktreePath, destination string) (string, error) {
	finalPath, err := resolveWorktreeMoveDestination(worktreePath, destination)
	if err != nil {
		return "", err
	}
	if filepath.Clean(worktreePath) == finalPath {
		return "", fmt.Errorf("worktree destination must be different")
	}
	if _, err := os.Stat(finalPath); err == nil {
		return "", fmt.Errorf("worktree destination already exists: %s", finalPath)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	cleanupParent, err := createWorktreeMoveDestinationParent(finalPath)
	if err != nil {
		return "", err
	}
	if err := runGit(repoPath, "worktree", "move", "--", worktreePath, finalPath); err != nil {
		cleanupParent()
		return "", err
	}
	return finalPath, nil
}

func createWorktreeMoveDestinationParent(finalPath string) (func(), error) {
	parent := filepath.Dir(finalPath)
	var created []string
	for dir := parent; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		} else if os.IsNotExist(err) {
			created = append(created, dir)
		} else {
			return nil, err
		}
		if next := filepath.Dir(dir); next == dir {
			break
		}
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	return func() {
		for _, dir := range created {
			_ = os.Remove(dir)
		}
	}, nil
}

func resolveWorktreeMoveDestination(worktreePath, input string) (string, error) {
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return "", fmt.Errorf("worktree path cannot be empty")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("worktree destination cannot be empty")
	}
	if filepath.IsAbs(input) {
		return filepath.Clean(input), nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(worktreePath), input)), nil
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

// RunBootstrapHook executes a configured bootstrap script directly, with the
// created worktree as its working directory.
func RunBootstrapHook(ctx BootstrapContext, hook BootstrapHook) error {
	scriptPath := hook.Script
	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(ctx.WorktreePath, scriptPath)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("bootstrap hook not found: %s", scriptPath)
		}
		return fmt.Errorf("stat bootstrap hook %s: %w", scriptPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("bootstrap hook is not a regular file: %s", scriptPath)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("bootstrap hook is not executable: %s", scriptPath)
	}

	timeout := hook.TimeoutSeconds
	if timeout == 0 {
		timeout = 120
	}
	if timeout < 0 {
		return fmt.Errorf("bootstrap hook timeout must be >= 0")
	}
	commandCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, scriptPath)
	cmd.Dir = ctx.WorktreePath
	cmd.Env = envWithOverrides(
		envVar{key: "WTUI_REPO_PATH", value: ctx.RepoPath},
		envVar{key: "WTUI_WORKTREE_PATH", value: ctx.WorktreePath},
		envVar{key: "WTUI_WORKTREE_REF", value: ctx.Ref},
		envVar{key: "WTUI_WORKTREE_CREATE_KIND", value: ctx.Kind.String()},
	)
	output := newTailBuffer(4096)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	msg := output.String()
	if commandCtx.Err() == context.DeadlineExceeded {
		if msg != "" {
			return fmt.Errorf("bootstrap hook %s timed out after %ds: %s", scriptPath, timeout, msg)
		}
		return fmt.Errorf("bootstrap hook %s timed out after %ds", scriptPath, timeout)
	}
	if err != nil {
		if msg == "" {
			return fmt.Errorf("bootstrap hook %s failed: %w", scriptPath, err)
		}
		return fmt.Errorf("bootstrap hook %s failed: %s", scriptPath, msg)
	}
	return nil
}

type tailBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.truncated = true
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.limit:]...)
		if idx := strings.IndexByte(string(b.buf), '\n'); idx >= 0 && idx+1 < len(b.buf) {
			b.buf = append([]byte(nil), b.buf[idx+1:]...)
		}
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := strings.TrimSpace(string(b.buf))
	if msg == "" {
		return ""
	}
	if b.truncated {
		return "... " + msg
	}
	return msg
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

// NormalizePullRequestWorktreeRef returns the stable PR ref value wtui exposes
// to post-create integrations.
func NormalizePullRequestWorktreeRef(input string) (string, error) {
	pr, err := parsePullRequestInput(input)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(pr.Number), nil
}

// DefaultWorktreePath returns the conventional sibling path used for new
// worktrees: <repo>-worktrees/<branch-or-tag>.
func DefaultWorktreePath(repoPath, ref string) string {
	base := filepath.Base(repoPath)
	if isBareRepo(repoPath) {
		base = strings.TrimSuffix(base, ".git")
	}
	parent := filepath.Dir(repoPath)
	return filepath.Join(parent, base+"-worktrees", sanitizePathPart(ref))
}

func isBareRepo(repoPath string) bool {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--is-bare-repository").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
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
	// Detached means the command hands the agent off to another terminal or
	// multiplexer session; provider hooks own completed-session metadata.
	Detached bool
	Cleanup  func()
}

// AgentLaunchContext carries metadata wtui knows at launch time so provider
// hooks can associate later session records with the selected repo/worktree.
type AgentLaunchContext struct {
	Command          string
	LaunchID         string
	RepoPath         string
	WorktreePath     string
	WorkingDir       string
	Branch           string
	Commit           string
	SessionStateRoot string
	ResumeSessionID  string
	PlanID           string
	PlanPath         string
	PlanPhaseID      string
	PlanPhaseTitle   string
	PlanPhaseStatus  string
	// InitialPrompt is appended as the trailing positional prompt argument.
	InitialPrompt string
}

// AgentLaunch builds a supported coding-agent command for ctx and wraps it in a
// terminal/multiplexer transport so the agent runs in its own
// window/session—matching the behavior of the `t` shortcut—instead of taking
// over the wtui TTY. Detached transports leave the wtui TUI usable; only
// transports that genuinely need the current TTY are returned as interactive.
func AgentLaunch(ctx AgentLaunchContext) (TerminalLaunchSpec, error) {
	return agentLaunch(ctx, runtime.GOOS, os.Getenv, exec.LookPath)
}

func agentLaunch(ctx AgentLaunchContext, goos string, getenv getenvFunc, lookPath lookPathFunc) (TerminalLaunchSpec, error) {
	cmd, _, err := agentCommandSpec(ctx)
	if err != nil {
		return TerminalLaunchSpec{}, err
	}
	// Name the session after the worktree root (not WorkingDir, which may be a
	// subdir on resume) so it is recognizable, and suffix it with the launch id
	// so each launch gets its own session. A pre-existing same-named session
	// (e.g. one opened by `t`) would otherwise cause tmux to drop the agent
	// command and only switch to the old shell session.
	sessionSource := ctx.WorktreePath
	if sessionSource == "" {
		sessionSource = cmd.Dir
	}
	argv, err := resolvedCommandArgv(cmd)
	if err != nil {
		return TerminalLaunchSpec{}, err
	}
	command, err := newTerminalCommand(cmd.Dir, cmd.Env, argv, agentSessionName(sessionSource, ctx.LaunchID))
	if err != nil {
		return TerminalLaunchSpec{}, err
	}
	launch, err := terminalLaunch(cmd.Dir, goos, getenv, lookPath, command)
	if err != nil {
		command.cleanup()
		return TerminalLaunchSpec{}, err
	}
	launch.Cleanup = command.cleanup
	return launch, nil
}

func resolvedCommandArgv(cmd *exec.Cmd) ([]string, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	if len(cmd.Args) == 0 {
		return nil, fmt.Errorf("agent command has no argv")
	}
	argv := append([]string(nil), cmd.Args...)
	if cmd.Path != "" {
		argv[0] = cmd.Path
	}
	return argv, nil
}

// agentSessionName derives a tmux/Zellij session name for an agent launch. It is
// rooted at the worktree, always carries an "-agent" segment so it can never
// equal the plain `t` terminal session for the same worktree, and is suffixed
// with the launch id so each launch gets its own session (a pre-existing
// same-named session would otherwise cause tmux to drop the agent command). If
// launchID is empty the name still differs from the `t` session via "-agent".
func agentSessionName(worktreePath, launchID string) string {
	name := WorktreeSessionName(worktreePath) + "-agent"
	if suffix := sanitizeSessionSuffix(launchID); suffix != "" {
		name += "-" + suffix
	}
	return name
}

func sanitizeSessionSuffix(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), ".-")
}

// AgentCommand builds the direct command for launching a supported coding agent
// in ctx, including provider hook args, resume args, the trailing prompt, the
// working directory, and the WTUI_* environment overrides. It does not wrap the
// command in a terminal transport; AgentLaunch does that.
func AgentCommand(ctx AgentLaunchContext) (*exec.Cmd, error) {
	cmd, _, err := agentCommandSpec(ctx)
	return cmd, err
}

func agentCommandSpec(ctx AgentLaunchContext) (*exec.Cmd, []envVar, error) {
	command := agent.Normalize(ctx.Command)
	if err := agent.Validate(command); err != nil {
		return nil, nil, err
	}
	args := agentLaunchArgs(command, ctx.ResumeSessionID)
	if ctx.InitialPrompt != "" {
		args = append(args, ctx.InitialPrompt)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = ctx.WorktreePath
	if ctx.WorkingDir != "" {
		cmd.Dir = ctx.WorkingDir
	}
	commit := ResolveWorktreeCommit(cmd.Dir)
	if commit == "" {
		commit = ctx.Commit
	}
	overrides := []envVar{
		{key: "WTUI_AGENT", value: command},
		{key: "WTUI_LAUNCH_ID", value: ctx.LaunchID},
		{key: "WTUI_REPO_PATH", value: ctx.RepoPath},
		{key: "WTUI_WORKTREE_PATH", value: ctx.WorktreePath},
		{key: "WTUI_BRANCH", value: ctx.Branch},
		{key: "WTUI_COMMIT", value: commit},
		{key: "WTUI_SESSION_STATE_ROOT", value: ctx.SessionStateRoot},
		{key: "WTUI_PLAN_STATE_ROOT", value: ctx.SessionStateRoot},
		{key: "WTUI_PLAN_ID", value: ctx.PlanID},
		{key: "WTUI_PLAN_PATH", value: ctx.PlanPath},
		{key: "WTUI_PLAN_PHASE_ID", value: ctx.PlanPhaseID},
		{key: "WTUI_PLAN_PHASE_TITLE", value: ctx.PlanPhaseTitle},
		{key: "WTUI_PLAN_PHASE_STATUS", value: ctx.PlanPhaseStatus},
	}
	cmd.Env = envWithOverrides(overrides...)
	return cmd, overrides, nil
}

func envWithOverrides(overrides ...envVar) []string {
	overrideKeys := make(map[string]struct{}, len(overrides))
	for _, item := range overrides {
		overrideKeys[item.key] = struct{}{}
	}

	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, found := overrideKeys[key]; found {
				continue
			}
		}
		env = append(env, entry)
	}
	for _, item := range overrides {
		env = append(env, item.key+"="+item.value)
	}
	return env
}

// ResolveWorktreeCommit returns HEAD for path, or "" when path is not a git
// worktree. Launching agents should not fail just because metadata is missing.
func ResolveWorktreeCommit(path string) string {
	if path == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func agentLaunchArgs(command, resumeSessionID string) []string {
	switch command {
	case "codex":
		hookCommand := wtuiSessionHookCommand("codex")
		hookConfig := "hooks.Stop=[{hooks=[{type=\"command\", command=" + strconv.Quote(hookCommand) + ", timeout=30, statusMessage=\"Saving wtui session\"}]}]"
		args := []string{"--config", hookConfig}
		if resumeSessionID != "" {
			args = append(args, "resume", resumeSessionID)
		}
		return args
	case "claude":
		hookCommand := wtuiSessionHookCommand("claude")
		settings := claudeSessionHookSettings(hookCommand)
		args := []string{"--settings", settings}
		if resumeSessionID != "" {
			args = append(args, "--resume", resumeSessionID)
		}
		return args
	default:
		return nil
	}
}

func claudeSessionHookSettings(hookCommand string) string {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionEnd": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": hookCommand,
						},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(settings)
	return string(data)
}

func wtuiSessionHookCommand(provider string) string {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	return shellQuote(executable) + " session-hook --provider " + provider
}

// terminalCommand describes an inner process (such as a coding agent) that a
// terminal transport should run inside the worktree session. The actual command
// lives in an owner-readable script so inherited secrets are not serialized into
// tmux/zellij/osascript/terminal argv.
type terminalCommand struct {
	scriptPath string
	// session overrides the tmux/Zellij session name for this launch. When
	// empty, the transport falls back to WorktreeSessionName(path).
	session string
}

func newTerminalCommand(dir string, env []string, argv []string, session string) (*terminalCommand, error) {
	scriptPath, err := writeTerminalCommandScript(dir, env, argv)
	if err != nil {
		return nil, err
	}
	return &terminalCommand{scriptPath: scriptPath, session: session}, nil
}

// shellCommand renders only the safe transport command. The script it calls
// contains the quoted environment, cwd, and argv, then deletes itself before
// exec'ing the agent.
func (c *terminalCommand) shellCommand() string {
	return "exec sh " + shellQuote(c.scriptPath)
}

func (c *terminalCommand) cleanup() {
	if c != nil && c.scriptPath != "" {
		_ = os.Remove(c.scriptPath)
	}
}

func writeTerminalCommandScript(dir string, env []string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("agent command has no argv")
	}

	file, err := os.CreateTemp("", "wtui-agent-*.sh")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("script_path=$0\n")
	b.WriteString("cleanup() { rm -f \"$script_path\"; }\n")
	b.WriteString("trap cleanup EXIT HUP INT TERM\n")
	b.WriteString("cd ")
	b.WriteString(shellQuote(dir))
	b.WriteString(" || exit\n")
	for _, entry := range env {
		if line, ok := shellExportLine(entry); ok {
			b.WriteString(line)
		}
	}
	b.WriteString("cleanup\n")
	b.WriteString("trap - EXIT HUP INT TERM\n")
	b.WriteString("exec")
	for _, arg := range argv {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	b.WriteByte('\n')

	if _, err := file.WriteString(b.String()); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func shellExportLine(entry string) (string, bool) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok || !isShellIdentifier(key) {
		return "", false
	}
	return "export " + key + "=" + shellQuote(value) + "\n", true
}

func isShellIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// TerminalLaunch returns a command that opens or switches to a multiplexer
// session for path. It adapts to the current environment:
//   - inside Zellij: switch to a Zellij session with the worktree name
//   - inside tmux: create the tmux session if needed, then switch-client
//   - outside a multiplexer: prefer tmux, Zellij, $TERMINAL, then a platform/shell fallback
func TerminalLaunch(path string) (TerminalLaunchSpec, error) {
	return terminalLaunch(path, runtime.GOOS, os.Getenv, exec.LookPath, nil)
}

// terminalLaunch chooses a transport for path. When command is nil it opens a
// plain shell/terminal session (the `t` shortcut). When command is non-nil it
// runs that command inside the chosen session instead.
func terminalLaunch(path, goos string, getenv getenvFunc, lookPath lookPathFunc, command *terminalCommand) (TerminalLaunchSpec, error) {
	sessionName := WorktreeSessionName(path)
	if command != nil && command.session != "" {
		sessionName = command.session
	}

	switch {
	case getenv("ZELLIJ") != "" && commandExists("zellij", lookPath):
		if command != nil {
			// switch-session cannot carry a command, so run the agent in a new
			// pane of the current Zellij session; wtui keeps running in its pane.
			return TerminalLaunchSpec{
				Cmd:      exec.Command("zellij", "run", "--cwd", path, "--", "sh", "-c", command.shellCommand()),
				Detached: true,
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd: exec.Command("zellij", "action", "switch-session", sessionName, "--cwd", path),
		}, nil
	case getenv("TMUX") != "" && commandExists("tmux", lookPath):
		return TerminalLaunchSpec{
			Cmd:      tmuxSwitchCommand(sessionName, path, command),
			Detached: command != nil,
		}, nil
	case commandExists("tmux", lookPath):
		if goos == "darwin" && commandExists("osascript", lookPath) {
			return TerminalLaunchSpec{
				Cmd:      externalTerminalCommand(tmuxAttachCommand(sessionName, path, command)),
				Detached: command != nil,
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd:         tmuxNewSessionCommand(sessionName, path, command),
			Interactive: true,
			Detached:    command != nil,
		}, nil
	case commandExists("zellij", lookPath):
		if goos == "darwin" && commandExists("osascript", lookPath) {
			return TerminalLaunchSpec{
				Cmd:      externalTerminalCommand(zellijAttachCommand(sessionName, path, command)),
				Detached: command != nil,
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd:         zellijAttachLocalCommand(sessionName, path, command),
			Interactive: true,
		}, nil
	}

	if terminal := strings.TrimSpace(getenv("TERMINAL")); terminal != "" {
		return terminalLaunchFromEnv(goos, terminal, path, lookPath, command)
	}

	if goos == "darwin" && commandExists("open", lookPath) {
		if command != nil {
			if !commandExists("osascript", lookPath) {
				return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: osascript is required to run a command in Terminal")
			}
			return TerminalLaunchSpec{
				Cmd:      externalTerminalCommand(command.shellCommand()),
				Detached: true,
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd: exec.Command("open", "-a", "Terminal", path),
		}, nil
	}

	// $SHELL comes from the user's own environment, so we trust its intent;
	// we still validate it points at a runnable executable before exec'ing it
	// and fall back to /bin/sh when it is empty or invalid.
	shell := resolveShell(strings.TrimSpace(getenv("SHELL")), lookPath)
	if command != nil {
		// No detached transport is available, so hand over the current TTY and
		// run the agent directly. This is the only interactive agent path.
		return TerminalLaunchSpec{
			Cmd:         exec.Command(shell, "-c", command.shellCommand()),
			Interactive: true,
		}, nil
	}
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

func terminalLaunchFromEnv(goos, terminal, path string, lookPath lookPathFunc, command *terminalCommand) (TerminalLaunchSpec, error) {
	fields := strings.Fields(terminal)
	if len(fields) == 0 {
		return TerminalLaunchSpec{}, fmt.Errorf("TERMINAL is empty")
	}

	name := fields[0]
	args := fields[1:]
	if commandExists(name, lookPath) {
		if command != nil {
			// Best-effort: most terminals accept `-e <command>` to run a
			// command instead of an interactive shell.
			args = append(append([]string{}, args...), "-e", "sh", "-c", command.shellCommand())
		}
		cmd := exec.Command(name, args...)
		cmd.Dir = path
		return TerminalLaunchSpec{Cmd: cmd, Detached: command != nil}, nil
	}

	if goos == "darwin" {
		if command != nil {
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: TERMINAL %q must be on PATH to run a command; run inside tmux/zellij or use a terminal that accepts -e", terminal)
		}
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
	input = strings.TrimPrefix(input, "#")
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

const tmuxSwitchRunScript = `
session=$1
path=$2
cmd=$3
tmux has-session -t "$session" 2>/dev/null || tmux new-session -d -s "$session" -c "$path" "$cmd"
tmux switch-client -t "$session"
`

// tmuxSwitchCommand creates (if needed) and switches to the worktree session
// from within an existing tmux client. With a command it runs the command in a
// freshly created session.
func tmuxSwitchCommand(sessionName, path string, command *terminalCommand) *exec.Cmd {
	if command != nil {
		return exec.Command("sh", "-c", tmuxSwitchRunScript, "wtui", sessionName, path, command.shellCommand())
	}
	return exec.Command("sh", "-c", tmuxSwitchScript, "wtui", sessionName, path)
}

// tmuxNewSessionCommand attaches to (creating if needed) the worktree session
// in the current terminal. With a command it runs the command on creation.
func tmuxNewSessionCommand(sessionName, path string, command *terminalCommand) *exec.Cmd {
	args := []string{"new-session", "-A", "-s", sessionName, "-c", path}
	if command != nil {
		args = append(args, command.shellCommand())
	}
	return exec.Command("tmux", args...)
}

// zellijAttachLocalCommand attaches to (creating if needed) the worktree
// session in the current terminal. With a command it hands the TTY to a shell
// running the agent directly, since Zellij cannot create a detached session
// running a command from outside an existing session.
func zellijAttachLocalCommand(sessionName, path string, command *terminalCommand) *exec.Cmd {
	if command != nil {
		return exec.Command("sh", "-c", command.shellCommand())
	}
	cmd := exec.Command("zellij", "attach", "--create", sessionName)
	cmd.Dir = path
	return cmd
}

func commandExists(name string, lookPath lookPathFunc) bool {
	_, err := lookPath(name)
	return err == nil
}

// externalTerminalCommand opens macOS Terminal running shellCommand. shellCommand
// is embedded via Go %q, which escapes the AppleScript string; untrusted values
// inside it are already single-quoted by shellQuote, so they cannot break out of
// either the AppleScript string or the shell. Exotic control characters (bell,
// vtab) are assumed absent from paths/branches/prompts; they would be mangled by
// the AppleScript layer but cannot inject.
func externalTerminalCommand(shellCommand string) *exec.Cmd {
	return exec.Command(
		"osascript",
		"-e", fmt.Sprintf(`tell application "Terminal" to do script %q`, shellCommand),
		"-e", `tell application "Terminal" to activate`,
	)
}

func tmuxAttachCommand(sessionName, path string, command *terminalCommand) string {
	cmd := "tmux new-session -A -s " + shellQuote(sessionName) + " -c " + shellQuote(path)
	if command != nil {
		cmd += " " + shellQuote(command.shellCommand())
	}
	return cmd
}

func zellijAttachCommand(sessionName, path string, command *terminalCommand) string {
	if command != nil {
		// A brand-new external terminal has no Zellij session to attach to, so
		// run the agent directly in the opened window.
		return command.shellCommand()
	}
	return "cd " + shellQuote(path) + " && zellij attach --create " + shellQuote(sessionName)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
