package actions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/google/shlex"
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

const (
	ClipboardMethodAuto   = "auto"
	ClipboardMethodSystem = "system"
	ClipboardMethodOSC52  = "osc52"

	DefaultOSC52MaxPayloadBytes = 100_000
)

// ClipboardOptions selects the clipboard transport and bounds encoded OSC 52 payloads.
type ClipboardOptions struct {
	Method               string
	OSC52MaxPayloadBytes int
}

type clipboardDeps struct {
	goos         string
	lookPath     lookPathFunc
	getenv       getenvFunc
	runSystem    func(commandSpec, string) error
	openTerminal func() (io.WriteCloser, error)
}

// CommandRunner executes a command and returns stdout, stderr, and the command error.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, []byte, error)
}

// ContextCommandRunner executes a command whose process is cancelled with ctx.
type ContextCommandRunner interface {
	RunContext(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

type execCommandRunner struct{}

type execContextCommandRunner struct{}

func (execCommandRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command(name, args...)
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout, exitErr.Stderr, err
	}
	return stdout, nil, err
}

func (execContextCommandRunner) RunContext(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout, exitErr.Stderr, err
	}
	return stdout, nil, err
}

// BootstrapHook configures a script to run after a worktree is created.
type BootstrapHook struct {
	Script         string
	TimeoutSeconds int
}

// worktreeDirPerm is the world-readable directory mode for worktree parents
// and newly created local repos. It is intentionally distinct from
// artifacts.DirPerm (0700).
const worktreeDirPerm os.FileMode = 0o755

// WorktreeCreateKind identifies which create flow produced the worktree.
type WorktreeCreateKind int

const (
	WorktreeCreateGeneric WorktreeCreateKind = iota
	WorktreeCreatePullRequest
	WorktreeCreateFlow
)

func (k WorktreeCreateKind) String() string {
	switch k {
	case WorktreeCreatePullRequest:
		return "pull_request"
	case WorktreeCreateFlow:
		return "flow"
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

// PullRequestMerge is the verified GitHub merge metadata for a PR.
type PullRequestMerge struct {
	Commit   string
	MergedAt time.Time
}

const (
	PRStatusUnknown = "unknown"
	PRMergeable     = "mergeable"
	PRConflicting   = "conflicting"
	PRChecksPassing = "passing"
	PRChecksFailing = "failing"
	PRChecksPending = "pending"
)

// PullRequestStatus is the live GitHub status projected in the PR babysitter.
type PullRequestStatus struct {
	Mergeability string
	Checks       string
}

// ErrWorktreePruneFailed reports that git removed the worktree but failed to
// prune stale admin references. Callers should treat the worktree as gone.
var ErrWorktreePruneFailed = errors.New("worktree removed but prune failed")

func pruneAfterWorktreeRemove(repoPath string) error {
	if err := runGit(repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("%w: %w", ErrWorktreePruneFailed, err)
	}
	return nil
}

// RemoveWorktree runs `git worktree remove` for the given worktree path,
// then prunes stale references to ensure the worktree no longer appears
// in listings.
func RemoveWorktree(repoPath, worktreePath string) error {
	if err := runGit(repoPath, "worktree", "remove", worktreePath); err != nil {
		return err
	}
	return pruneAfterWorktreeRemove(repoPath)
}

// ForceRemoveWorktree runs `git worktree remove --force`, then prunes
// stale references.
func ForceRemoveWorktree(repoPath, worktreePath string) error {
	if err := runGit(repoPath, "worktree", "remove", "--force", worktreePath); err != nil {
		return err
	}
	return pruneAfterWorktreeRemove(repoPath)
}

// PruneWorktree runs `git worktree prune` to remove stale admin references.
func PruneWorktree(repoPath string) error {
	return runGit(repoPath, "worktree", "prune")
}

// UnlockWorktree runs `git worktree unlock` for the given worktree path.
func UnlockWorktree(repoPath, worktreePath string) error {
	return runGit(repoPath, "worktree", "unlock", worktreePath)
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
	if err := os.MkdirAll(parent, worktreeDirPerm); err != nil {
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
		return gitCommandError{message: msg, cause: err}
	}
	return nil
}

type gitCommandError struct {
	message string
	cause   error
}

func (e gitCommandError) Error() string { return e.message }
func (e gitCommandError) Unwrap() error { return e.cause }

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
		envVar{key: "APPROACH_REPO_PATH", value: ctx.RepoPath},
		envVar{key: "APPROACH_WORKTREE_PATH", value: ctx.WorktreePath},
		envVar{key: "APPROACH_WORKTREE_REF", value: ctx.Ref},
		envVar{key: "APPROACH_WORKTREE_CREATE_KIND", value: ctx.Kind.String()},
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
	if err := os.MkdirAll(filepath.Dir(worktreePath), worktreeDirPerm); err != nil {
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

// FlowWorktreeCreateResult describes the branch/worktree allocated for a Flow.
type FlowWorktreeCreateResult struct {
	WorktreePath string
	Branch       string
}

// MainWorktreePath resolves the primary worktree for the repository containing
// path. Git always lists the primary worktree first, including when path is a
// linked worktree.
func MainWorktreePath(path string) (string, error) {
	out, err := exec.Command("git", "-C", path, "worktree", "list", "--porcelain", "-z").Output()
	nulDelimited := err == nil
	if err != nil {
		// Git added -z support to `worktree list` after the project's minimum
		// supported version. Retry the legacy format so ordinary paths continue
		// to work there; modern Git keeps the unambiguous path representation.
		out, err = exec.Command("git", "-C", path, "worktree", "list", "--porcelain").Output()
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if exitErr, ok := err.(*exec.ExitError); ok {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				msg = stderr
			}
		}
		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", err
	}
	delimiter := "\x00"
	if !nulDelimited {
		delimiter = "\n"
	}
	for _, field := range strings.Split(string(out), delimiter) {
		if !nulDelimited {
			field = strings.Trim(field, "\r\n")
		}
		mainPath, ok := strings.CutPrefix(field, "worktree ")
		if !ok {
			continue
		}
		if !nulDelimited {
			// -z prints the path raw; the legacy format applies git's C-style
			// quoting to any path with a newline, quote, tab, or non-ASCII
			// byte in it, and reading that literally would reject a path git
			// reported perfectly well.
			unquoted, err := unquoteGitPath(mainPath)
			if err != nil {
				return "", err
			}
			mainPath = unquoted
		}
		if !filepath.IsAbs(mainPath) {
			return "", fmt.Errorf("git returned non-absolute main worktree path %q", mainPath)
		}
		return filepath.Clean(mainPath), nil
	}
	return "", fmt.Errorf("git worktree list returned no primary worktree")
}

// unquoteGitPath decodes one pathname from git's legacy porcelain output.
//
// An unquoted token is already the path. A quoted one uses C escapes, and the
// octal form git emits for non-ASCII bytes (`\303\251`) is why this is written
// out rather than handed to strconv.Unquote, which rejects it. The bytes are
// assembled as bytes, never as runes: a path is a byte string, and decoding a
// two-byte UTF-8 escape into one rune would produce a name that does not exist.
func unquoteGitPath(path string) (string, error) {
	if !strings.HasPrefix(path, `"`) {
		return path, nil
	}
	if len(path) < 2 || !strings.HasSuffix(path, `"`) {
		return "", fmt.Errorf("git returned an unterminated quoted worktree path %s", path)
	}
	body := path[1 : len(path)-1]
	var decoded []byte
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			decoded = append(decoded, body[i])
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("git returned a quoted worktree path ending in an escape: %s", path)
		}
		switch body[i] {
		case 'a':
			decoded = append(decoded, '\a')
		case 'b':
			decoded = append(decoded, '\b')
		case 'f':
			decoded = append(decoded, '\f')
		case 'n':
			decoded = append(decoded, '\n')
		case 'r':
			decoded = append(decoded, '\r')
		case 't':
			decoded = append(decoded, '\t')
		case 'v':
			decoded = append(decoded, '\v')
		case '\\', '"':
			decoded = append(decoded, body[i])
		case '0', '1', '2', '3', '4', '5', '6', '7':
			if i+2 >= len(body) {
				return "", fmt.Errorf("git returned a truncated octal escape in worktree path %s", path)
			}
			value, err := strconv.ParseUint(body[i:i+3], 8, 8)
			if err != nil {
				return "", fmt.Errorf("git returned an undecodable octal escape in worktree path %s: %w", path, err)
			}
			decoded = append(decoded, byte(value))
			i += 2
		default:
			return "", fmt.Errorf("git returned an unrecognized escape %q in worktree path %s", body[i], path)
		}
	}
	return string(decoded), nil
}

// CreateFlowWorktree creates a deterministic Flow branch/worktree pair:
// flow/<slug> at <repo>-worktrees/flow-<slug>. Branch and path suffixes move
// together on collision so the pair remains easy to recognize.
func CreateFlowWorktree(repoPath, title, baseRef string) (FlowWorktreeCreateResult, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return FlowWorktreeCreateResult{}, fmt.Errorf("flow title cannot be empty")
	}
	baseRef = strings.TrimSpace(baseRef)
	slug := artifacts.Slug(title, "flow")
	for i := 1; i < 1000; i++ {
		suffix := ""
		if i > 1 {
			suffix = fmt.Sprintf("-%d", i)
		}
		branch := "flow/" + slug + suffix
		worktreePath := filepath.Join(filepath.Dir(repoPath), repoWorktreeDirName(repoPath), "flow-"+slug+suffix)
		if flowBranchOrPathExists(repoPath, branch, worktreePath) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(worktreePath), worktreeDirPerm); err != nil {
			return FlowWorktreeCreateResult{}, err
		}
		args := []string{"-C", repoPath, "worktree", "add", "-b", branch, worktreePath}
		if baseRef != "" {
			args = append(args, baseRef)
		}
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if isFlowWorktreeCollisionError(msg) {
				continue
			}
			if msg == "" {
				return FlowWorktreeCreateResult{}, err
			}
			return FlowWorktreeCreateResult{}, fmt.Errorf("%s: %w", msg, err)
		}
		return FlowWorktreeCreateResult{WorktreePath: worktreePath, Branch: branch}, nil
	}
	return FlowWorktreeCreateResult{}, fmt.Errorf("could not allocate a unique flow worktree for %q after %d attempts", title, 999)
}

// ErrFlowBranchMissing reports that the branch a Flow record names does not
// exist in its repository, so there is nothing to attach a worktree to. It is a
// sentinel rather than a message because the caller's next move — allocate a
// fresh flow/<slug> pair instead — depends on this case and no other.
var ErrFlowBranchMissing = errors.New("branch does not exist")

// AttachFlowWorktree gives an existing local branch a worktree at the
// conventional sibling path. It is the counterpart to CreateFlowWorktree for a
// Flow that already records the branch it means to run on: inventing a second
// branch there would strand the recorded one.
func AttachFlowWorktree(repoPath, branch string) (FlowWorktreeCreateResult, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return FlowWorktreeCreateResult{}, fmt.Errorf("branch cannot be empty")
	}
	if strings.HasPrefix(branch, "-") {
		return FlowWorktreeCreateResult{}, fmt.Errorf("branch cannot start with -: %q", branch)
	}
	// A local branch, not any commit-ish: `git worktree add <path> v1.0.0`
	// succeeds on a tag, a remote-tracking ref, or a raw SHA and leaves a
	// detached HEAD, which would let an agent commit onto nothing while the
	// record kept naming a branch that never moved.
	if !localBranchExists(repoPath, branch) {
		if refExists(repoPath, branch) {
			return FlowWorktreeCreateResult{}, fmt.Errorf("%s is not a local branch", branch)
		}
		return FlowWorktreeCreateResult{}, fmt.Errorf("%s: %w", branch, ErrFlowBranchMissing)
	}
	// A branch that already has a worktree of its own is where the Flow belongs.
	// Git will not check it out twice, so refusing here would leave the Flow
	// permanently unlaunchable for the most ordinary reason a record names a
	// branch. The main worktree is never adopted — running there is the
	// isolation break this whole path exists to prevent — so it falls through to
	// the loud refusal `git worktree add` produces on its own.
	if existing := linkedWorktreeForBranch(repoPath, branch); existing != "" {
		return FlowWorktreeCreateResult{WorktreePath: existing, Branch: branch}, nil
	}
	basePath := DefaultWorktreePath(repoPath, branch)
	for i := 1; i < 1000; i++ {
		worktreePath := basePath
		if i > 1 {
			worktreePath = fmt.Sprintf("%s-%d", basePath, i)
		}
		if _, err := os.Stat(worktreePath); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(worktreePath), worktreeDirPerm); err != nil {
			return FlowWorktreeCreateResult{}, err
		}
		out, err := exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, branch).CombinedOutput()
		if err == nil {
			return FlowWorktreeCreateResult{WorktreePath: worktreePath, Branch: branch}, nil
		}
		msg := strings.TrimSpace(string(out))
		// Only the path may move. A branch already checked out somewhere else
		// fails loudly instead: adopting that worktree is how a Flow ends up
		// running in the primary checkout, which is the whole bug being fixed.
		if isFlowWorktreePathCollisionError(msg) {
			continue
		}
		if msg == "" {
			return FlowWorktreeCreateResult{}, err
		}
		return FlowWorktreeCreateResult{}, fmt.Errorf("%s: %w", msg, err)
	}
	return FlowWorktreeCreateResult{}, fmt.Errorf("could not allocate a worktree for branch %q after %d attempts", branch, 999)
}

func localBranchExists(repoPath, branch string) bool {
	return exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// linkedWorktreeForBranch returns the linked worktree that already has branch
// checked out, or "" when there is none. Git lists the main worktree first, and
// a match there answers "" precisely so the caller does not adopt the user's
// primary checkout.
func linkedWorktreeForBranch(repoPath, branch string) string {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}
	want := "branch refs/heads/" + branch
	mainPath := ""
	current := ""
	// The match is not answered at the branch line: git emits `prunable` after
	// it, so a candidate has to survive to the end of its own record before it
	// can be adopted.
	candidate := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			if candidate != "" {
				return candidate
			}
			current = path
			if mainPath == "" {
				mainPath = path
			}
			continue
		}
		if candidate != "" && (line == "prunable" || strings.HasPrefix(line, "prunable ")) {
			// git keeps listing a worktree whose gitdir link is gone — a
			// directory the user deleted, or one whose `.git` file alone went
			// missing — and marks the registration prunable. Adopting either
			// would hand an agent a directory that is not a worktree; dropping
			// the candidate instead reaches git's own already-used-by refusal,
			// which is the one that says to prune.
			candidate = ""
			continue
		}
		if line == want {
			if current == "" || current == mainPath {
				return ""
			}
			if info, err := os.Stat(current); err != nil || !info.IsDir() {
				return ""
			}
			candidate = current
		}
	}
	return candidate
}

func flowBranchOrPathExists(repoPath, branch, worktreePath string) bool {
	if refExists(repoPath, branch) {
		return true
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return true
	}
	return false
}

func isFlowWorktreeCollisionError(msg string) bool {
	return isFlowWorktreePathCollisionError(msg) ||
		strings.Contains(strings.ToLower(msg), "is already checked out")
}

// isFlowWorktreePathCollisionError separates the collisions a different path
// resolves from the ones only a different branch would. For CreateFlowWorktree
// that includes a directory the user deleted without pruning, since the branch
// moves with the path; for AttachFlowWorktree the branch is fixed, so a stale
// registration reappears at the next suffix as an already-used-by refusal that
// only `git worktree prune` clears.
func isFlowWorktreePathCollisionError(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "missing but already registered")
}

func repoWorktreeDirName(repoPath string) string {
	base := filepath.Base(repoPath)
	if isBareRepo(repoPath) {
		base = strings.TrimSuffix(base, ".git")
	}
	return base + "-worktrees"
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
	if err := os.MkdirAll(filepath.Dir(worktreePath), worktreeDirPerm); err != nil {
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

// NormalizePullRequestWorktreeRef returns the stable PR ref value approach exposes
// to post-create integrations.
func NormalizePullRequestWorktreeRef(input string) (string, error) {
	pr, err := parsePullRequestInput(input)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(pr.Number), nil
}

// LookupGitHubPRMerge returns verified merge metadata for a GitHub PR.
func LookupGitHubPRMerge(number int, prURL string) (PullRequestMerge, error) {
	return LookupGitHubPRMergeWithRunner(number, prURL, execCommandRunner{})
}

// LookupGitHubPRMergeWithRunner returns verified merge metadata using runner.
func LookupGitHubPRMergeWithRunner(number int, prURL string, runner CommandRunner) (PullRequestMerge, error) {
	if number <= 0 {
		return PullRequestMerge{}, fmt.Errorf("PR number must be positive")
	}
	pr, err := parsePullRequestInput(prURL)
	if err != nil || pr.Owner == "" || pr.Repo == "" {
		return PullRequestMerge{}, fmt.Errorf("manual merge lookup requires GitHub PR URL with owner and repo")
	}
	if pr.Number != number {
		return PullRequestMerge{}, fmt.Errorf("PR URL number %d must match PR number %d", pr.Number, number)
	}
	stdout, stderr, err := runner.Run("gh", "pr", "view", strconv.Itoa(number), "--repo", pr.Owner+"/"+pr.Repo, "--json", "state,mergedAt,mergeCommit")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return PullRequestMerge{}, fmt.Errorf("gh executable not found")
		}
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = err.Error()
		}
		return PullRequestMerge{}, fmt.Errorf("gh pr view failed: %s", detail)
	}
	var parsed struct {
		State       string `json:"state"`
		MergedAt    string `json:"mergedAt"`
		MergeCommit *struct {
			OID string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		return PullRequestMerge{}, fmt.Errorf("parse gh PR JSON: %w", err)
	}
	if parsed.State != "MERGED" {
		return PullRequestMerge{}, fmt.Errorf("GitHub PR #%d is %s, not MERGED", number, parsed.State)
	}
	if parsed.MergeCommit == nil || strings.TrimSpace(parsed.MergeCommit.OID) == "" {
		return PullRequestMerge{}, fmt.Errorf("GitHub PR #%d is missing merge commit", number)
	}
	mergedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(parsed.MergedAt))
	if err != nil {
		return PullRequestMerge{}, fmt.Errorf("invalid mergedAt for GitHub PR #%d: %w", number, err)
	}
	return PullRequestMerge{Commit: strings.TrimSpace(parsed.MergeCommit.OID), MergedAt: mergedAt}, nil
}

// LookupGitHubPRStatus returns live mergeability and checks for a GitHub PR.
func LookupGitHubPRStatus(ctx context.Context, number int, prURL string) (PullRequestStatus, error) {
	return LookupGitHubPRStatusWithRunner(ctx, number, prURL, execContextCommandRunner{})
}

// LookupGitHubPRStatusWithRunner returns live PR status using runner.
func LookupGitHubPRStatusWithRunner(ctx context.Context, number int, prURL string, runner ContextCommandRunner) (PullRequestStatus, error) {
	unknown := PullRequestStatus{Mergeability: PRStatusUnknown, Checks: PRStatusUnknown}
	if number <= 0 {
		return unknown, fmt.Errorf("PR number must be positive")
	}
	pr, err := parsePullRequestInput(prURL)
	if err != nil || pr.Owner == "" || pr.Repo == "" {
		return unknown, fmt.Errorf("PR status lookup requires GitHub PR URL with owner and repo")
	}
	if pr.Number != number {
		return unknown, fmt.Errorf("PR URL number %d must match PR number %d", pr.Number, number)
	}
	stdout, stderr, err := runner.RunContext(ctx, "gh", "pr", "view", strconv.Itoa(number), "--repo", pr.Owner+"/"+pr.Repo, "--json", "mergeable,statusCheckRollup")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return unknown, fmt.Errorf("gh executable not found")
		}
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = err.Error()
		}
		return unknown, fmt.Errorf("gh pr view failed: %s", detail)
	}
	var parsed struct {
		Mergeable         string            `json:"mergeable"`
		StatusCheckRollup []json.RawMessage `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		return unknown, fmt.Errorf("parse gh PR status JSON: %w", err)
	}
	return PullRequestStatus{
		Mergeability: classifyPRMergeability(parsed.Mergeable),
		Checks:       aggregatePRChecks(parsed.StatusCheckRollup),
	}, nil
}

func classifyPRMergeability(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "MERGEABLE":
		return PRMergeable
	case "CONFLICTING":
		return PRConflicting
	default:
		return PRStatusUnknown
	}
}

func aggregatePRChecks(rollup []json.RawMessage) string {
	if len(rollup) == 0 {
		return PRStatusUnknown
	}
	best := PRChecksPassing
	for _, member := range rollup {
		value := classifyPRCheck(member)
		if prCheckRank(value) > prCheckRank(best) {
			best = value
		}
	}
	return best
}

func prCheckRank(value string) int {
	switch value {
	case PRChecksFailing:
		return 4
	case PRChecksPending:
		return 3
	case PRStatusUnknown:
		return 2
	case PRChecksPassing:
		return 1
	default:
		return 2
	}
}

func classifyPRCheck(raw json.RawMessage) string {
	var member struct {
		TypeName   string `json:"__typename"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(raw, &member); err != nil {
		return PRStatusUnknown
	}
	switch member.TypeName {
	case "CheckRun":
		switch strings.ToUpper(strings.TrimSpace(member.Status)) {
		case "CREATED", "QUEUED", "IN_PROGRESS", "PENDING", "REQUESTED", "WAITING":
			return PRChecksPending
		case "COMPLETED":
			switch strings.ToUpper(strings.TrimSpace(member.Conclusion)) {
			case "ACTION_REQUIRED", "CANCELLED", "FAILURE", "STALE", "STARTUP_FAILURE", "TIMED_OUT":
				return PRChecksFailing
			case "SUCCESS", "NEUTRAL", "SKIPPED":
				return PRChecksPassing
			default:
				return PRStatusUnknown
			}
		default:
			return PRStatusUnknown
		}
	case "StatusContext":
		switch strings.ToUpper(strings.TrimSpace(member.State)) {
		case "ERROR", "FAILURE":
			return PRChecksFailing
		case "EXPECTED", "PENDING":
			return PRChecksPending
		case "SUCCESS":
			return PRChecksPassing
		default:
			return PRStatusUnknown
		}
	default:
		return PRStatusUnknown
	}
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

// CopyToClipboard copies text using the native clipboard when available and
// otherwise falls back to OSC 52.
func CopyToClipboard(text string) error {
	return CopyToClipboardWithOptions(text, ClipboardOptions{
		Method:               ClipboardMethodAuto,
		OSC52MaxPayloadBytes: DefaultOSC52MaxPayloadBytes,
	})
}

// CopyToClipboardWithOptions copies text using the configured clipboard transport.
func CopyToClipboardWithOptions(text string, opts ClipboardOptions) error {
	return copyToClipboardWithOptions(text, opts, clipboardDeps{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		runSystem: func(spec commandSpec, text string) error {
			cmd := exec.Command(spec.name, spec.args...)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		},
		openTerminal: func() (io.WriteCloser, error) {
			return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		},
	})
}

func copyToClipboardWithOptions(text string, opts ClipboardOptions, deps clipboardDeps) error {
	method := opts.Method
	if method == "" {
		method = ClipboardMethodAuto
	}
	if method == ClipboardMethodSystem || method == ClipboardMethodAuto {
		spec, err := selectClipboardCommand(deps.goos, deps.lookPath)
		if err == nil {
			if err := deps.runSystem(spec, text); err != nil {
				return fmt.Errorf("run clipboard command %s: %w", spec.name, err)
			}
			return nil
		}
		if method == ClipboardMethodSystem {
			return err
		}
	}
	if method != ClipboardMethodOSC52 && method != ClipboardMethodAuto {
		return fmt.Errorf("unknown clipboard method %q", opts.Method)
	}

	sequence, err := buildOSC52Sequence(text, opts.OSC52MaxPayloadBytes, deps.getenv("TMUX") != "")
	if err != nil {
		return err
	}
	terminal, err := deps.openTerminal()
	if err != nil {
		return fmt.Errorf("open controlling terminal for OSC 52: %w", err)
	}
	n, writeErr := terminal.Write(sequence)
	if writeErr == nil && n != len(sequence) {
		writeErr = io.ErrShortWrite
	}
	closeErr := terminal.Close()
	if writeErr != nil {
		writeErr = fmt.Errorf("write OSC 52 sequence to controlling terminal: %w", writeErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close controlling terminal after OSC 52 write: %w", closeErr)
	}
	return errors.Join(writeErr, closeErr)
}

func buildOSC52Sequence(text string, maxPayloadBytes int, tmux bool) ([]byte, error) {
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	if maxPayloadBytes > 0 && len(payload) > maxPayloadBytes {
		return nil, fmt.Errorf("OSC 52 encoded payload is %d bytes, limit is %d bytes", len(payload), maxPayloadBytes)
	}

	sequence := "\x1b]52;c;" + payload + "\x1b\\"
	if !tmux {
		return []byte(sequence), nil
	}
	sequence = strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b")
	return []byte("\x1bPtmux;" + sequence + "\x1b\\"), nil
}

// OpenURL opens an absolute http(s) URL in the system browser.
func OpenURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	spec, err := selectBrowserCommand(runtime.GOOS, exec.LookPath)
	if err != nil {
		return err
	}
	args := append([]string(nil), spec.args...)
	args = append(args, rawURL)
	return exec.Command(spec.name, args...).Run()
}

// PageText builds an interactive pager command for read-only text views.
func PageText(body string) (TerminalLaunchSpec, error) {
	return pageText(body, exec.LookPath)
}

func pageText(body string, lookPath lookPathFunc) (TerminalLaunchSpec, error) {
	if _, err := lookPath("less"); err != nil {
		return TerminalLaunchSpec{}, err
	}
	cmd := exec.Command("less", "-R")
	cmd.Stdin = strings.NewReader(body)
	return TerminalLaunchSpec{Cmd: cmd, Interactive: true}, nil
}

// EditorOptions customizes how editable files are opened.
type EditorOptions struct {
	EditorCommand string
}

// EditFile builds an interactive editor command for path.
func EditFile(path string) (TerminalLaunchSpec, error) {
	return EditFileWithOptions(path, EditorOptions{})
}

// EditFileWithOptions is EditFile with configurable editor selection.
func EditFileWithOptions(path string, opts EditorOptions) (TerminalLaunchSpec, error) {
	return editFileWithOptions(path, os.Getenv, exec.LookPath, opts)
}

func editFileWithOptions(path string, getenv getenvFunc, lookPath lookPathFunc, opts EditorOptions) (TerminalLaunchSpec, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return TerminalLaunchSpec{}, fmt.Errorf("editor path cannot be empty")
	}
	source := "[editor].command"
	editor := strings.TrimSpace(opts.EditorCommand)
	if editor == "" {
		source = "EDITOR"
		editor = strings.TrimSpace(getenv("EDITOR"))
	}
	if editor == "" {
		return TerminalLaunchSpec{}, fmt.Errorf("no editor configured; set [editor].command or EDITOR")
	}
	fields, err := shlex.Split(editor)
	if err != nil {
		return TerminalLaunchSpec{}, fmt.Errorf("parse %s: %w", source, err)
	}
	if len(fields) == 0 || strings.TrimSpace(fields[0]) == "" {
		return TerminalLaunchSpec{}, fmt.Errorf("%s is empty", source)
	}
	if !commandExists(fields[0], lookPath) {
		return TerminalLaunchSpec{}, fmt.Errorf("%s is set to %q, but that command was not found", source, editor)
	}
	args := append([]string(nil), fields[1:]...)
	args = append(args, path)
	return TerminalLaunchSpec{Cmd: exec.Command(fields[0], args...), Interactive: true}, nil
}

// OpenVSCode opens VSCode at the given path.
func OpenVSCode(path string) error {
	return exec.Command("code", path).Run()
}

// TerminalLaunchSpec describes how approach should open an external process for a worktree.
// Interactive commands should be run with Bubble Tea's ExecProcess so the TUI
// releases the current terminal until the process exits.
type TerminalLaunchSpec struct {
	Cmd         *exec.Cmd
	Interactive bool
	// Detached means the command hands the agent off to another terminal or
	// multiplexer session; provider hooks own completed-session metadata.
	Detached bool
	Cleanup  func()
	// ErrorDetail returns a transport-captured diagnostic for a failed run, or
	// "" when there is none. A transport that spawns through a wrapper script
	// sets it so the failure reads as more than the script's exit status; nil
	// leaves the process error as the whole message. Only read after the
	// command has exited.
	ErrorDetail func() string
}

// ErrEmbeddedTmuxUnavailable tells callers they can use the direct embedded PTY
// path because tmux is not installed.
var ErrEmbeddedTmuxUnavailable = errors.New("tmux is not available for embedded terminal detach")

// UntrackedOwnerReleaseCommand is the internal process-exit callback used by
// phase-untracked Flow agents. It is not part of the interactive CLI surface.
const UntrackedOwnerReleaseCommand = "untracked-owner-release"

// EmbeddedTmuxAgentSpec describes a CLI agent launch that runs inside a tmux
// session while approach embeds only an attached tmux client.
type EmbeddedTmuxAgentSpec struct {
	SocketName         string
	SessionName        string
	ScriptPath         string
	StatusPath         string
	DetachTarget       string
	HasSessionCommand  *exec.Cmd
	NewSessionCommand  *exec.Cmd
	AttachCommand      *exec.Cmd
	KillSessionCommand *exec.Cmd
	Cleanup            func()
}

// LaunchOptions customizes external terminal transports without changing
// multiplexer/session selection.
type LaunchOptions struct {
	TerminalCommand string
}

// AgentLaunchContext carries metadata Approach knows at launch time so provider
// hooks can associate later session records with the selected repo/worktree.
type AgentLaunchContext struct {
	Command           string
	LaunchID          string
	RepoPath          string
	WorktreePath      string
	WorkingDir        string
	Branch            string
	Commit            string
	SessionStateRoot  string
	ResumeSessionID   string
	PlanID            string
	PlanPath          string
	PlanPhaseID       string
	PlanPhaseTitle    string
	PlanPhaseStatus   string
	FlowID            string
	FlowPhaseID       string
	FlowPhaseKind     string
	FlowLaunchTracked bool
	FlowAutoLaunch    bool
	// FlowRepair marks an untracked Flow-scoped repair session. Repair launches
	// deliberately carry no phase ID so provider hooks retain Flow
	// discoverability without attaching the session to a phase attempt.
	FlowRepair bool
	// FlowAgent marks the generic untracked Flow-scoped worktree agent started by
	// s. Its retained terminal is intentionally nondetachable.
	// Like repair it carries no phase ID, so provider hooks keep Flow
	// discoverability without attaching the session to a phase attempt.
	FlowAgent bool
	// FlowSavedSessionResume marks a phase-untracked resume whose authoritative
	// saved session belongs to a Flow. It is embedded-only and preserves the raw
	// provider session ID byte-for-byte.
	FlowSavedSessionResume bool
	// FlowAutofix marks the distinct prompted, PR-gated untracked agent started
	// by U. It shares prompt delivery mechanics with FlowAgent but not policy.
	FlowAutofix bool
	// FlowAutofixPRNumber is display-only typed metadata for the PR-gated
	// FlowAutofix role. It is not exported to providers.
	FlowAutofixPRNumber int
	// FlowPhaseTerminal records that the persisted phase kept a terminal
	// status (completed, skipped) when the launch was recorded, so launch
	// failures must not regress the phase to needs_attention.
	FlowPhaseTerminal bool
	Embedded          bool
	Headless          bool
	Model             string
	ReasoningEffort   string
	// InitialPrompt is canonical launch metadata. It is delivered either as a
	// provider argv or by embedded PTY prefill, depending on launch mode.
	InitialPrompt string
	// Executable pins the approach binary this launch's agent must invoke,
	// exported as APPROACH_EXECUTABLE and baked into the provider session hook.
	// It is the launching build, not whatever `approach` ambient PATH resolves:
	// a phase launched by a schema-N build and reported by a schema-(N-1) CLI
	// cannot persist its result at all. Empty means "no pin" and leaves agents
	// on PATH, which is the pre-pin behaviour and still correct for a manually
	// started session.
	Executable string
	// BuildVersion and DBSchemaVersion describe the pinned build, exported as
	// APPROACH_BUILD_VERSION and APPROACH_DB_SCHEMA so an agent-side
	// compatibility refusal can name both binaries rather than one integer.
	BuildVersion    string
	DBSchemaVersion int
	// ControlEndpoint and ControlToken are the launch's registration with the
	// TUI's launch controller, exported as APPROACH_CONTROL_ENDPOINT and
	// APPROACH_CONTROL_TOKEN only when set. With them the agent's `approach
	// flow` writes are proxied over the per-root socket and logged before they
	// are acknowledged; without them the CLI opens the store directly, which is
	// the pre-controller behaviour. The token is per launch and lives only in
	// the agent's environment and the controller's memory.
	ControlEndpoint string
	ControlToken    string
}

// AgentLaunch builds a supported coding-agent command for ctx and wraps it in a
// terminal/multiplexer transport so the agent runs in its own
// window/session—matching the behavior of the `t` shortcut—instead of taking
// over the approach TTY. Detached transports leave the approach TUI usable; only
// transports that genuinely need the current TTY are returned as interactive.
func AgentLaunch(ctx AgentLaunchContext) (TerminalLaunchSpec, error) {
	return AgentLaunchWithOptions(ctx, LaunchOptions{})
}

// AgentLaunchWithOptions is AgentLaunch with configurable terminal transport
// selection.
func AgentLaunchWithOptions(ctx AgentLaunchContext, opts LaunchOptions) (TerminalLaunchSpec, error) {
	return agentLaunchWithOptions(ctx, runtime.GOOS, os.Getenv, exec.LookPath, opts)
}

func agentLaunch(ctx AgentLaunchContext, goos string, getenv getenvFunc, lookPath lookPathFunc) (TerminalLaunchSpec, error) {
	return agentLaunchWithOptions(ctx, goos, getenv, lookPath, LaunchOptions{})
}

func agentLaunchWithOptions(ctx AgentLaunchContext, goos string, getenv getenvFunc, lookPath lookPathFunc, opts LaunchOptions) (TerminalLaunchSpec, error) {
	command := agent.Normalize(ctx.Command)
	if err := agent.Validate(command); err != nil {
		return TerminalLaunchSpec{}, err
	}

	// An external terminal window is not an embedded slot: there is no dock to
	// prefill, so the initial prompt has to reach the agent as argv, and the
	// window has an alt screen of its own to leave alone. Both follow from
	// Embedded being false, exactly as they do in RepoTmuxAgentLaunch.
	ctx.Embedded = false
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
	termCommand, err := newTerminalCommand(cmd.Dir, cmd.Env, argv, agentSessionName(sessionSource, ctx.LaunchID))
	if err != nil {
		return TerminalLaunchSpec{}, err
	}
	configureUntrackedOwnerRelease(termCommand, ctx)
	launch, err := terminalLaunchWithOptions(cmd.Dir, goos, getenv, lookPath, termCommand, opts)
	if err != nil {
		termCommand.cleanup()
		return TerminalLaunchSpec{}, err
	}
	launch.Cleanup = termCommand.cleanup
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
	return sanitizeIdentifierPart(s, "")
}

func sanitizeIdentifierPart(s, fallback string) string {
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
	name := strings.Trim(b.String(), ".-")
	if name == "" {
		return fallback
	}
	return name
}

// AgentCommand builds the direct command for launching a supported coding agent
// in ctx, including provider hook args, resume args, the trailing prompt, the
// working directory, and the APPROACH_* environment overrides. It does not wrap the
// command in a terminal transport; AgentLaunch does that.
func AgentCommand(ctx AgentLaunchContext) (*exec.Cmd, error) {
	cmd, _, err := agentCommandSpec(ctx)
	return cmd, err
}

// EmbeddedTmuxAgentCommand builds the tmux lifecycle commands for a detachable
// embedded CLI agent launch. It does not start tmux.
func EmbeddedTmuxAgentCommand(ctx AgentLaunchContext) (EmbeddedTmuxAgentSpec, error) {
	return embeddedTmuxAgentCommand(ctx, exec.LookPath)
}

func embeddedTmuxAgentCommand(ctx AgentLaunchContext, lookPath lookPathFunc) (EmbeddedTmuxAgentSpec, error) {
	if !commandExists("tmux", lookPath) {
		return EmbeddedTmuxAgentSpec{}, ErrEmbeddedTmuxUnavailable
	}
	ctx.Embedded = true
	cmd, _, err := agentCommandSpec(ctx)
	if err != nil {
		return EmbeddedTmuxAgentSpec{}, err
	}
	sessionSource := ctx.WorktreePath
	if sessionSource == "" {
		sessionSource = cmd.Dir
	}
	argv, err := resolvedCommandArgv(cmd)
	if err != nil {
		return EmbeddedTmuxAgentSpec{}, err
	}
	sessionName := agentSessionName(sessionSource, ctx.LaunchID)
	agentEnv := envWithoutKeys(cmd.Env, "TMUX", "ZELLIJ")
	termCommand, err := newTerminalCommandWithStatus(cmd.Dir, agentEnv, argv, sessionName)
	if err != nil {
		return EmbeddedTmuxAgentSpec{}, err
	}
	configureUntrackedOwnerRelease(termCommand, ctx)
	tmuxEnv := envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
	socketName := tmuxSocketName(sessionName)
	tmuxArgs := isolatedTmuxArgs(socketName)
	spec := EmbeddedTmuxAgentSpec{
		SocketName:         socketName,
		SessionName:        sessionName,
		ScriptPath:         termCommand.scriptPath,
		StatusPath:         termCommand.statusPath,
		DetachTarget:       tmuxDetachTarget(socketName, sessionName),
		HasSessionCommand:  exec.Command("tmux", append(tmuxArgs, "has-session", "-t", sessionName)...),
		NewSessionCommand:  exec.Command("tmux", append(append([]string{}, tmuxArgs...), tmuxNewSessionArgs(sessionName, cmd.Dir, termCommand.shellCommand())...)...),
		AttachCommand:      tmuxAttachStatusCommand(socketName, sessionName, termCommand.statusPath),
		KillSessionCommand: exec.Command("tmux", append(tmuxArgs, "kill-session", "-t", sessionName)...),
		Cleanup:            termCommand.cleanup,
	}
	spec.HasSessionCommand.Env = tmuxEnv
	spec.NewSessionCommand.Env = tmuxEnv
	spec.AttachCommand.Env = tmuxEnv
	spec.KillSessionCommand.Env = tmuxEnv
	return spec, nil
}

func isolatedTmuxArgs(socketName string) []string {
	return []string{"-f", "/dev/null", "-L", socketName}
}

func tmuxSocketName(sessionName string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionName))
	return fmt.Sprintf("approach-agent-%08x", h.Sum32())
}

func tmuxDetachTarget(socketName, sessionName string) string {
	return "env -u TMUX tmux -f /dev/null -L " + shellQuote(socketName) + " attach-session -t " + shellQuote(sessionName)
}

func tmuxNewSessionArgs(sessionName, dir, shellCommand string) []string {
	return []string{
		"start-server",
		";", "set-option", "-g", "prefix", "None",
		";", "unbind-key", "C-b",
		";", "set-option", "-g", "status", "off",
		";", "new-session", "-d", "-s", sessionName, "-c", dir, shellCommand,
	}
}

func tmuxAttachStatusCommand(socketName, sessionName, statusPath string) *exec.Cmd {
	script := strings.TrimSpace(`
tmux -f /dev/null -L "$1" attach-session -t "$2"
tmux_status=$?
if [ -r "$3" ]; then
	IFS= read -r agent_status < "$3"
	rm -f "$3"
	case "$agent_status" in
		""|*[!0-9]*) exit "$tmux_status" ;;
		*) exit "$agent_status" ;;
	esac
fi
exit "$tmux_status"
`)
	return exec.Command("/bin/sh", "-c", script, "approach", socketName, sessionName, statusPath)
}

// UsesStreamJSONOutput reports whether an embedded launch emits claude
// stream-json that approach must render into readable terminal lines. Headless
// Claude and Cursor print modes stream stream-json; codex and interactive
// launches render their own output directly.
func UsesStreamJSONOutput(ctx AgentLaunchContext) bool {
	command := agent.Normalize(ctx.Command)
	return (command == agent.CommandClaude || command == agent.CommandCursor) && ctx.Embedded && ctx.Headless
}

// ShouldPrefillEmbeddedPrompt reports whether an embedded Flow launch fills the
// dock with its prompt instead of passing it as argv. Which launches prefill is
// the role's answer; the rest of the conjuncts are transport and payload — the
// three providers with a dock, the interactive embedded slot, and a prompt that
// is actually there to place.
//
// The role has to be well formed as well as prefilling: a context that mixes
// markers — a repair carrying a phase, a phase-attached launch that declared
// itself untracked — is not the launch its role names, and the four
// hand-written predicates this replaced each refused those shapes by failing a
// conjunct. That refusal is now stated once, on the role.
func ShouldPrefillEmbeddedPrompt(ctx AgentLaunchContext) bool {
	command := agent.Normalize(ctx.Command)
	role := FlowLaunchRoleOf(ctx)
	return (command == agent.CommandCodex || command == agent.CommandClaude || command == agent.CommandCursor) &&
		ctx.Embedded &&
		!ctx.Headless &&
		ctx.ResumeSessionID == "" &&
		ctx.InitialPrompt != "" &&
		role.Prefills() &&
		validateFlowLaunchRole(ctx, role) == nil
}

func agentCommandSpec(ctx AgentLaunchContext) (*exec.Cmd, []envVar, error) {
	command := agent.Normalize(ctx.Command)
	if err := agent.Validate(command); err != nil {
		return nil, nil, err
	}
	if command == agent.CommandCursor {
		if err := ensureCursorSessionHook(os.Getenv("HOME")); err != nil {
			return nil, nil, err
		}
	}
	resumeSessionID, err := resumeSessionIDForContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Headless && resumeSessionID != "" {
		return nil, nil, fmt.Errorf("headless agent launch does not support session resume")
	}
	reasoningEffort := agent.NormalizeReasoningEffort(ctx.ReasoningEffort)
	if reasoningEffort != "" && reasoningEffort != agent.ReasoningEffortDefault {
		if err := agent.ValidateReasoningEffort(command, reasoningEffort); err != nil {
			return nil, nil, err
		}
		if resumeSessionID != "" {
			return nil, nil, fmt.Errorf("reasoning effort cannot be set for session resume")
		}
	}
	model := agent.NormalizeModel(ctx.Model)
	if model != "" && model != agent.ModelDefault {
		if err := agent.ValidateModel(command, model); err != nil {
			return nil, nil, err
		}
		if resumeSessionID != "" {
			return nil, nil, fmt.Errorf("model cannot be set for session resume")
		}
	}
	args := agentLaunchArgs(command, resumeSessionID, ctx.Headless, model, reasoningEffort, agentLaunchArgsOptions{
		embedded:   ctx.Embedded,
		streamJSON: UsesStreamJSONOutput(ctx),
		executable: ctx.Executable,
	})
	if ctx.InitialPrompt != "" && !ShouldPrefillEmbeddedPrompt(ctx) {
		args = append(args, ctx.InitialPrompt)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = ctx.WorktreePath
	if ctx.WorkingDir != "" {
		cmd.Dir = ctx.WorkingDir
	}
	commit := ctx.Commit
	if !ctx.FlowSavedSessionResume {
		if resolved := ResolveWorktreeCommit(cmd.Dir); resolved != "" {
			commit = resolved
		}
	}
	overrides := []envVar{
		{key: "APPROACH_AGENT", value: command},
		{key: "APPROACH_LAUNCH_ID", value: ctx.LaunchID},
		{key: "APPROACH_REPO_PATH", value: ctx.RepoPath},
		{key: "APPROACH_WORKTREE_PATH", value: ctx.WorktreePath},
		{key: "APPROACH_BRANCH", value: ctx.Branch},
		{key: "APPROACH_COMMIT", value: commit},
		{key: "APPROACH_SESSION_STATE_ROOT", value: ctx.SessionStateRoot},
		{key: "APPROACH_PLAN_STATE_ROOT", value: ctx.SessionStateRoot},
		{key: "APPROACH_FLOW_STATE_ROOT", value: ctx.SessionStateRoot},
		{key: "APPROACH_PLAN_ID", value: ctx.PlanID},
		{key: "APPROACH_PLAN_PATH", value: ctx.PlanPath},
		{key: "APPROACH_PLAN_PHASE_ID", value: ctx.PlanPhaseID},
		{key: "APPROACH_PLAN_PHASE_TITLE", value: ctx.PlanPhaseTitle},
		{key: "APPROACH_PLAN_PHASE_STATUS", value: ctx.PlanPhaseStatus},
		{key: "APPROACH_FLOW_ID", value: ctx.FlowID},
		{key: "APPROACH_FLOW_PHASE_ID", value: ctx.FlowPhaseID},
		{key: "APPROACH_EXECUTABLE", value: ctx.Executable},
		{key: "APPROACH_BUILD_VERSION", value: ctx.BuildVersion},
		{key: "APPROACH_DB_SCHEMA", value: schemaVersionEnvValue(ctx.DBSchemaVersion)},
	}
	// Only when non-empty. An empty endpoint would read as "an endpoint that
	// is unreachable" and send every write down the fallback path; absent, the
	// CLI opens the store directly with no detour. The parent's own value, if
	// this TUI was itself started inside a launched agent, is stripped either
	// way so a stale endpoint is never inherited.
	controlKeys := []string{"APPROACH_CONTROL_ENDPOINT", "APPROACH_CONTROL_TOKEN"}
	if ctx.ControlEndpoint != "" && ctx.ControlToken != "" {
		overrides = append(overrides,
			envVar{key: "APPROACH_CONTROL_ENDPOINT", value: ctx.ControlEndpoint},
			envVar{key: "APPROACH_CONTROL_TOKEN", value: ctx.ControlToken},
		)
		controlKeys = nil
	}
	cmd.Env = envWithoutKeys(envWithOverrides(overrides...), controlKeys...)
	return cmd, overrides, nil
}

func resumeSessionIDForContext(ctx AgentLaunchContext) (string, error) {
	if !ctx.FlowSavedSessionResume {
		return resumeSessionIDForLaunch(ctx.ResumeSessionID)
	}
	// The marker claims the role; the classifier and the marker rows say whether
	// the context is a well-formed instance of it. What is left here is
	// transport and payload: this role exists only as an interactive embedded
	// resume of a session that is actually named, and it carries no prompt.
	if err := validateFlowLaunchRole(ctx, RoleSavedSessionResume); err != nil ||
		!ctx.Embedded ||
		ctx.Headless ||
		ctx.InitialPrompt != "" ||
		strings.TrimSpace(ctx.ResumeSessionID) == "" {
		return "", fmt.Errorf("invalid Flow saved-session resume role")
	}
	return ctx.ResumeSessionID, nil
}

// resumeSessionIDForLaunch trims a resume session ID and rejects resume
// requests whose session ID is blank, so a launch can never silently start a
// fresh session (or run `--resume ""`) when the caller asked for a resume.
func resumeSessionIDForLaunch(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if raw != "" && trimmed == "" {
		return "", fmt.Errorf("resume requires a non-blank session ID")
	}
	return trimmed, nil
}

func envWithoutKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[key] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, found := drop[key]; found {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
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

// agentLaunchArgsOptions carries the embedded-launch flags that shape an agent's
// argv beyond the resume/effort basics.
type agentLaunchArgsOptions struct {
	// embedded is true for an embedded-terminal launch (vs an external one).
	embedded bool
	// streamJSON is true when approach will render the agent's stream-json output;
	// it must equal UsesStreamJSONOutput for the same context so the args and
	// the renderer stay in lockstep (raw JSON in the panel otherwise).
	streamJSON bool
	// executable is the pinned approach binary for this launch. It is baked
	// into the provider session-hook argv so a detached agent's SessionEnd hook
	// runs the same build that launched it. Empty falls back to the running
	// binary.
	executable string
}

func agentLaunchArgs(command, resumeSessionID string, headless bool, model string, reasoningEffort string, opts agentLaunchArgsOptions) []string {
	switch command {
	case "codex":
		return codexLaunchArgs(resumeSessionID, headless, model, reasoningEffort, opts)
	case "claude":
		return claudeLaunchArgs(resumeSessionID, headless, model, reasoningEffort, opts)
	case agent.CommandCursor:
		return cursorLaunchArgs(resumeSessionID, headless, model, opts)
	default:
		return nil
	}
}

func codexLaunchArgs(resumeSessionID string, headless bool, model string, reasoningEffort string, opts agentLaunchArgsOptions) []string {
	hookCommand := approachSessionHookCommand("codex", opts.executable)
	hookConfig := "hooks.Stop=[{hooks=[{type=\"command\", command=" + strconv.Quote(hookCommand) + ", timeout=30, statusMessage=\"Saving approach session\"}]}]"
	args := []string{"--config", hookConfig}
	if model != "" && model != agent.ModelDefault {
		args = append(args, "--model", model)
	}
	if reasoningEffort != "" && reasoningEffort != agent.ReasoningEffortDefault {
		args = append(args, "--config", "model_reasoning_effort="+reasoningEffort)
	}
	if opts.embedded && !headless {
		args = slices.Insert(args, 0, "--no-alt-screen")
	}
	if headless {
		args = append(args, "exec")
	}
	if resumeSessionID != "" {
		args = append(args, "resume", resumeSessionID)
	}
	return args
}

func claudeLaunchArgs(resumeSessionID string, headless bool, model string, reasoningEffort string, opts agentLaunchArgsOptions) []string {
	hookCommand := approachSessionHookCommand("claude", opts.executable)
	settings := claudeSessionHookSettings(hookCommand)
	args := []string{"--settings", settings}
	if model != "" && model != agent.ModelDefault {
		args = append(args, "--model", model)
	}
	if reasoningEffort != "" && reasoningEffort != agent.ReasoningEffortDefault {
		args = append(args, "--effort", reasoningEffort)
	}
	if headless {
		args = slices.Insert(args, 0, "--print")
		// claude --print buffers plain-text output until completion, so an
		// embedded headless launch would show a blank panel for the whole
		// run. Stream stream-json (which requires --verbose) so approach can
		// render readable progress as events arrive;
		// --include-partial-messages adds token-by-token deltas so text and
		// tool calls stream in rather than appearing only when each block
		// completes. opts.streamJSON is UsesStreamJSONOutput for this
		// context, so these args appear iff the renderer is attached.
		if opts.streamJSON {
			args = append(args, "--verbose", "--output-format", "stream-json", "--include-partial-messages")
		}
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	return args
}

func cursorLaunchArgs(resumeSessionID string, headless bool, model string, opts agentLaunchArgsOptions) []string {
	var args []string
	if model != "" && model != agent.ModelDefault {
		args = append(args, "--model", model)
	}
	if headless {
		args = append(args, "-p", "--force", "--trust")
		// cursor-agent --print text format waits for the final answer, so
		// an embedded headless launch would show a blank panel for the
		// whole run. Stream stream-json with partial deltas so approach
		// can render readable progress as events arrive. opts.streamJSON
		// is UsesStreamJSONOutput for this context, so these args appear
		// iff the renderer is attached.
		if opts.streamJSON {
			args = append(args, "--output-format", "stream-json", "--stream-partial-output")
		}
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	return args
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

// approachSessionHookCommand builds the provider hook argv. It takes the pinned
// executable rather than calling os.Executable itself so the hook and
// APPROACH_EXECUTABLE can never name different builds; an empty pin keeps the
// previous running-binary behaviour for untracked launches. The executable is
// path data crossing a shell boundary and must remain shellQuote'd. Provider is
// a closed value supplied only by agentLaunchArgs, never untrusted input.
func approachSessionHookCommand(provider, executable string) string {
	executable = approachCommandExecutable(executable)
	return shellQuote(executable) + " session-hook --provider " + provider
}

func approachCommandExecutable(executable string) string {
	if strings.TrimSpace(executable) != "" {
		return executable
	}
	resolved, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return resolved
}

// schemaVersionEnvValue renders a schema version for the environment. Zero means
// "no pin was supplied" and exports empty rather than a literal 0, which an
// agent-side check would otherwise read as a real schema generation.
func schemaVersionEnvValue(version int) string {
	if version <= 0 {
		return ""
	}
	return strconv.Itoa(version)
}

// terminalCommand describes an inner process (such as a coding agent) that a
// terminal transport should run inside the worktree session. The actual command
// lives in an owner-readable script so inherited secrets are not serialized into
// tmux/zellij/osascript/terminal argv.
type terminalCommand struct {
	scriptPath  string
	statusPath  string
	exitCommand string
	// session overrides the tmux/Zellij session name for this launch. When
	// empty, the transport falls back to WorktreeSessionName(path).
	session string
}

func newTerminalCommand(dir string, env []string, argv []string, session string) (*terminalCommand, error) {
	return newTerminalCommandWithStatusPath(dir, env, argv, session, "")
}

func newTerminalCommandWithStatus(dir string, env []string, argv []string, session string) (*terminalCommand, error) {
	statusFile, err := os.CreateTemp("", "approach-agent-status-*.txt")
	if err != nil {
		return nil, err
	}
	statusPath := statusFile.Name()
	if err := statusFile.Close(); err != nil {
		_ = os.Remove(statusPath)
		return nil, err
	}
	return newTerminalCommandWithStatusPath(dir, env, argv, session, statusPath)
}

func newTerminalCommandWithStatusPath(dir string, env []string, argv []string, session, statusPath string) (*terminalCommand, error) {
	return newTerminalCommandWithArgvBuilder(dir, env, session, statusPath, func(string) ([]string, error) {
		return argv, nil
	})
}

func newTerminalCommandWithArgvBuilder(
	dir string,
	env []string,
	session string,
	statusPath string,
	buildArgv func(scriptPath string) ([]string, error),
) (*terminalCommand, error) {
	scriptPath, err := writeTerminalCommandScriptWithArgvBuilder(dir, env, statusPath, buildArgv)
	if err != nil {
		if statusPath != "" {
			_ = os.Remove(statusPath)
		}
		return nil, err
	}
	return &terminalCommand{scriptPath: scriptPath, statusPath: statusPath, session: session}, nil
}

// shellCommand renders only the safe transport command. The script it calls
// contains the quoted environment, cwd, and argv, then deletes itself before
// exec'ing the agent.
func (c *terminalCommand) shellCommand() string {
	if strings.TrimSpace(c.exitCommand) != "" {
		return "sh " + shellQuote(c.scriptPath) + "; status=$?; " + c.exitCommand + "; exit \"$status\""
	}
	return "exec sh " + shellQuote(c.scriptPath)
}

func configureUntrackedOwnerRelease(command *terminalCommand, ctx AgentLaunchContext) {
	if command == nil || strings.TrimSpace(ctx.FlowID) == "" || strings.TrimSpace(ctx.LaunchID) == "" {
		return
	}
	switch FlowLaunchRoleOf(ctx) {
	case RoleRepair, RoleAutofix, RoleWorktreeAgent:
		command.exitCommand = shellQuote(approachCommandExecutable(ctx.Executable)) + " " + UntrackedOwnerReleaseCommand +
			" --state-root " + shellQuote(ctx.SessionStateRoot) +
			" --flow-id " + shellQuote(ctx.FlowID) + " --launch-id " + shellQuote(ctx.LaunchID)
	}
}

func (c *terminalCommand) cleanup() {
	if c != nil && c.scriptPath != "" {
		_ = os.Remove(c.scriptPath)
	}
	if c != nil && c.statusPath != "" {
		_ = os.Remove(c.statusPath)
	}
}

func writeTerminalCommandScript(dir string, env []string, argv []string, statusPath string) (string, error) {
	return writeTerminalCommandScriptWithArgvBuilder(dir, env, statusPath, func(string) ([]string, error) {
		return argv, nil
	})
}

func writeTerminalCommandScriptWithArgvBuilder(
	dir string,
	env []string,
	statusPath string,
	buildArgv func(scriptPath string) ([]string, error),
) (string, error) {

	file, err := os.CreateTemp("", "approach-agent-*.sh")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	argv, err := buildArgv(path)
	if err != nil {
		cleanup()
		return "", err
	}
	if len(argv) == 0 {
		cleanup()
		return "", fmt.Errorf("agent command has no argv")
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
	if statusPath == "" {
		b.WriteString("exec")
	} else {
		b.WriteString("set +e\n")
	}
	for _, arg := range argv {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	b.WriteByte('\n')
	if statusPath != "" {
		b.WriteString("status=$?\n")
		b.WriteString("if [ -e ")
		b.WriteString(shellQuote(statusPath))
		b.WriteString(" ]; then\n")
		b.WriteString("printf '%s\\n' \"$status\" > ")
		b.WriteString(shellQuote(statusPath))
		b.WriteByte('\n')
		b.WriteString("fi\n")
		b.WriteString("exit \"$status\"\n")
	}

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
//   - outside a multiplexer: prefer $TERMINAL, configured terminal, tmux/Zellij, then a platform/shell fallback
func TerminalLaunch(path string) (TerminalLaunchSpec, error) {
	return TerminalLaunchWithOptions(path, LaunchOptions{})
}

// TerminalLaunchWithOptions is TerminalLaunch with configurable terminal
// transport selection.
func TerminalLaunchWithOptions(path string, opts LaunchOptions) (TerminalLaunchSpec, error) {
	return terminalLaunchWithOptions(path, runtime.GOOS, os.Getenv, exec.LookPath, nil, opts)
}

// DetachedTerminalLaunch builds a non-interactive handoff command that opens an
// external terminal and runs targetShellCommand. It intentionally ignores active
// or installed multiplexers; the target command already attaches to the
// detached tmux-backed embedded terminal.
func DetachedTerminalLaunch(targetShellCommand, cwd string, opts LaunchOptions) (TerminalLaunchSpec, error) {
	return detachedTerminalLaunch(targetShellCommand, cwd, runtime.GOOS, os.Getenv, exec.LookPath, opts)
}

func detachedTerminalLaunch(targetShellCommand, cwd, goos string, getenv getenvFunc, lookPath lookPathFunc, opts LaunchOptions) (TerminalLaunchSpec, error) {
	targetShellCommand = strings.TrimSpace(targetShellCommand)
	if targetShellCommand == "" {
		return TerminalLaunchSpec{}, fmt.Errorf("detached terminal handoff command cannot be empty")
	}
	preference := selectTerminalPreference(getenv("TERMINAL"), opts.TerminalCommand, lookPath)
	if preference.kind != terminalPreferenceNone {
		launch, err := detachedTerminalLaunchFromPreference(goos, cwd, lookPath, preference, targetShellCommand)
		if err != nil {
			return TerminalLaunchSpec{}, err
		}
		launch.Detached = true
		return launch, nil
	}
	if goos == "darwin" && commandExists("osascript", lookPath) {
		return TerminalLaunchSpec{
			Cmd:      macOSTerminalScriptCommand("Terminal", detachedTerminalShellCommand(targetShellCommand, cwd)),
			Detached: true,
		}, nil
	}
	return TerminalLaunchSpec{}, fmt.Errorf("external terminal required for detached handoff; set TERMINAL or [terminal].command")
}

// terminalLaunch chooses a transport for path. When command is nil it opens a
// plain shell/terminal session (the `t` shortcut). When command is non-nil it
// runs that command inside the chosen session instead.
func terminalLaunch(path, goos string, getenv getenvFunc, lookPath lookPathFunc, command *terminalCommand) (TerminalLaunchSpec, error) {
	return terminalLaunchWithOptions(path, goos, getenv, lookPath, command, LaunchOptions{})
}

func terminalLaunchWithOptions(path, goos string, getenv getenvFunc, lookPath lookPathFunc, command *terminalCommand, opts LaunchOptions) (TerminalLaunchSpec, error) {
	sessionName := WorktreeSessionName(path)
	if command != nil && command.session != "" {
		sessionName = command.session
	}
	preference := selectTerminalPreference(getenv("TERMINAL"), opts.TerminalCommand, lookPath)

	switch {
	case getenv("ZELLIJ") != "" && commandExists("zellij", lookPath):
		if command != nil {
			// switch-session cannot carry a command, so run the agent in a new
			// pane of the current Zellij session; approach keeps running in its pane.
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
	}

	if preference.kind != terminalPreferenceNone {
		return terminalLaunchFromPreference(goos, path, lookPath, preference, command)
	}

	switch {
	case commandExists("tmux", lookPath):
		if goos == "darwin" {
			launch, err := macOSScriptLaunch(path, lookPath, preference, tmuxAttachCommand(sessionName, path, command), command != nil)
			if err == nil {
				return launch, nil
			}
			if preference.kind != terminalPreferenceNone {
				return TerminalLaunchSpec{}, err
			}
		}
		return TerminalLaunchSpec{
			Cmd:         tmuxNewSessionCommand(sessionName, path, command),
			Interactive: true,
			Detached:    command != nil,
		}, nil
	case commandExists("zellij", lookPath):
		if goos == "darwin" {
			launch, err := macOSScriptLaunch(path, lookPath, preference, zellijAttachCommand(sessionName, path, command), command != nil)
			if err == nil {
				return launch, nil
			}
			if preference.kind != terminalPreferenceNone {
				return TerminalLaunchSpec{}, err
			}
		}
		return TerminalLaunchSpec{
			Cmd:         zellijAttachLocalCommand(sessionName, path, command),
			Interactive: true,
		}, nil
	}

	if goos == "darwin" && commandExists("open", lookPath) {
		if command != nil {
			if !commandExists("osascript", lookPath) {
				return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: osascript is required to run a command in Terminal")
			}
			return TerminalLaunchSpec{
				Cmd:      macOSTerminalScriptCommand("Terminal", command.shellCommand()),
				Detached: true,
			}, nil
		}
		return TerminalLaunchSpec{
			Cmd: macOSTerminalOpenCommand("Terminal", path),
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

type terminalPreferenceKind int

const (
	terminalPreferenceNone terminalPreferenceKind = iota
	terminalPreferenceGUIApp
	terminalPreferenceCLICommand
	terminalPreferenceUnsupportedGUIApp
)

type terminalPreference struct {
	source string
	raw    string
	kind   terminalPreferenceKind
	app    string
	argv   []string
	reason string
}

func selectTerminalPreference(envTerminal, configuredTerminal string, lookPath lookPathFunc) terminalPreference {
	if terminal := strings.TrimSpace(envTerminal); terminal != "" {
		return parseTerminalPreference("TERMINAL", terminal, lookPath)
	}
	if terminal := strings.TrimSpace(configuredTerminal); terminal != "" {
		return parseTerminalPreference("[terminal].command", terminal, lookPath)
	}
	return terminalPreference{kind: terminalPreferenceNone}
}

func parseTerminalPreference(source, terminal string, lookPath lookPathFunc) terminalPreference {
	fields := strings.Fields(terminal)
	if len(fields) == 0 {
		return terminalPreference{source: source, raw: terminal, kind: terminalPreferenceNone}
	}
	if app, ok := normalizeMacOSGUIAppAlias(fields[0]); ok {
		if len(fields) > 1 {
			return terminalPreference{
				source: source,
				raw:    terminal,
				kind:   terminalPreferenceUnsupportedGUIApp,
				app:    fields[0],
				reason: fmt.Sprintf("%s %q uses supported macOS GUI app %q with unsupported arguments", source, terminal, fields[0]),
			}
		}
		return terminalPreference{source: source, raw: terminal, kind: terminalPreferenceGUIApp, app: app}
	}
	if commandExists(fields[0], lookPath) {
		return terminalPreference{source: source, raw: terminal, kind: terminalPreferenceCLICommand, argv: fields}
	}
	return terminalPreference{source: source, raw: terminal, kind: terminalPreferenceUnsupportedGUIApp, app: fields[0]}
}

func normalizeMacOSGUIAppAlias(value string) (string, bool) {
	if value == "Ghostty" || value == "Ghostty.app" {
		return "Ghostty", true
	}
	switch strings.ToLower(value) {
	case "terminal", "terminal.app":
		return "Terminal", true
	case "iterm", "iterm.app", "iterm2", "iterm2.app":
		return "iTerm", true
	default:
		return "", false
	}
}

func terminalLaunchFromPreference(goos, path string, lookPath lookPathFunc, pref terminalPreference, command *terminalCommand) (TerminalLaunchSpec, error) {
	switch pref.kind {
	case terminalPreferenceCLICommand:
		return cliTerminalLaunch(pref.argv, path, command)
	case terminalPreferenceGUIApp:
		if goos != "darwin" {
			return TerminalLaunchSpec{}, missingTerminalCommandError(pref)
		}
		if command != nil {
			if !commandExists("osascript", lookPath) {
				if pref.app == "Ghostty" {
					return TerminalLaunchSpec{}, ghosttyAppleScriptDependencyError("launch agent")
				}
				return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: osascript is required to run a command in %s", pref.app)
			}
			return TerminalLaunchSpec{
				Cmd:      macOSTerminalScriptCommand(pref.app, command.shellCommand()),
				Detached: true,
			}, nil
		}
		if pref.app == "Terminal" && !commandExists("open", lookPath) {
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch terminal: open is required to open Terminal")
		}
		if pref.app != "Terminal" && !commandExists("osascript", lookPath) {
			if pref.app == "Ghostty" {
				return TerminalLaunchSpec{}, ghosttyAppleScriptDependencyError("launch terminal")
			}
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch terminal: osascript is required to open %s", pref.app)
		}
		return TerminalLaunchSpec{Cmd: macOSTerminalOpenCommand(pref.app, path)}, nil
	case terminalPreferenceUnsupportedGUIApp:
		if pref.reason != "" {
			return TerminalLaunchSpec{}, fmt.Errorf("%s", pref.reason)
		}
		if command != nil {
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: %s %q must be a supported macOS terminal app or a command on PATH that accepts -e", pref.source, pref.raw)
		}
		if goos == "darwin" {
			if !commandExists("open", lookPath) {
				return TerminalLaunchSpec{}, fmt.Errorf("cannot launch terminal: open is required to open %s", pref.app)
			}
			return TerminalLaunchSpec{Cmd: exec.Command("open", "-a", pref.app, path)}, nil
		}
		return TerminalLaunchSpec{}, missingTerminalCommandError(pref)
	default:
		return TerminalLaunchSpec{}, fmt.Errorf("%s is empty", pref.source)
	}
}

func detachedTerminalLaunchFromPreference(goos, cwd string, lookPath lookPathFunc, pref terminalPreference, shellCommand string) (TerminalLaunchSpec, error) {
	switch pref.kind {
	case terminalPreferenceCLICommand:
		cmd, err := cliTerminalLaunchForShell(pref.argv, cwd, shellCommand)
		if err != nil {
			return TerminalLaunchSpec{}, err
		}
		return TerminalLaunchSpec{Cmd: cmd}, nil
	case terminalPreferenceGUIApp:
		if goos != "darwin" {
			return TerminalLaunchSpec{}, missingTerminalCommandError(pref)
		}
		if !commandExists("osascript", lookPath) {
			if pref.app == "Ghostty" {
				return TerminalLaunchSpec{}, ghosttyAppleScriptDependencyError("launch detached terminal handoff")
			}
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch detached terminal handoff: osascript is required to run a command in %s", pref.app)
		}
		return TerminalLaunchSpec{Cmd: macOSTerminalScriptCommand(pref.app, detachedTerminalShellCommand(shellCommand, cwd))}, nil
	case terminalPreferenceUnsupportedGUIApp:
		if pref.reason != "" {
			return TerminalLaunchSpec{}, fmt.Errorf("%s", pref.reason)
		}
		return TerminalLaunchSpec{}, missingTerminalCommandError(pref)
	default:
		return TerminalLaunchSpec{}, fmt.Errorf("%s is empty", pref.source)
	}
}

func detachedTerminalShellCommand(shellCommand, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return shellCommand
	}
	return "cd " + shellQuote(cwd) + " && " + shellCommand
}

func cliTerminalLaunch(argv []string, path string, command *terminalCommand) (TerminalLaunchSpec, error) {
	if len(argv) == 0 {
		return TerminalLaunchSpec{}, fmt.Errorf("terminal command is empty")
	}
	args := append([]string(nil), argv[1:]...)
	if command != nil {
		args = append(args, "-e", "sh", "-c", command.shellCommand())
	}
	cmd := exec.Command(argv[0], args...)
	cmd.Dir = path
	return TerminalLaunchSpec{Cmd: cmd, Detached: command != nil}, nil
}

func macOSScriptLaunch(path string, lookPath lookPathFunc, pref terminalPreference, shellCommand string, detached bool) (TerminalLaunchSpec, error) {
	switch pref.kind {
	case terminalPreferenceNone:
		if !commandExists("osascript", lookPath) {
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: osascript is required to run a command in Terminal")
		}
		return TerminalLaunchSpec{Cmd: macOSTerminalScriptCommand("Terminal", shellCommand), Detached: detached}, nil
	case terminalPreferenceGUIApp:
		if !commandExists("osascript", lookPath) {
			if pref.app == "Ghostty" {
				return TerminalLaunchSpec{}, ghosttyAppleScriptDependencyError("launch agent")
			}
			return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: osascript is required to run a command in %s", pref.app)
		}
		return TerminalLaunchSpec{Cmd: macOSTerminalScriptCommand(pref.app, shellCommand), Detached: detached}, nil
	case terminalPreferenceCLICommand:
		cmd, err := cliTerminalLaunchForShell(pref.argv, path, shellCommand)
		if err != nil {
			return TerminalLaunchSpec{}, err
		}
		return TerminalLaunchSpec{Cmd: cmd, Detached: detached}, nil
	case terminalPreferenceUnsupportedGUIApp:
		if pref.reason != "" {
			return TerminalLaunchSpec{}, fmt.Errorf("%s", pref.reason)
		}
		return TerminalLaunchSpec{}, fmt.Errorf("cannot launch agent: %s %q must be a supported macOS terminal app or a command on PATH that accepts -e", pref.source, pref.raw)
	default:
		return TerminalLaunchSpec{}, fmt.Errorf("terminal preference is invalid")
	}
}

func cliTerminalLaunchForShell(argv []string, path, shellCommand string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("terminal command is empty")
	}
	args := append([]string(nil), argv[1:]...)
	args = append(args, "-e", "sh", "-c", shellCommand)
	cmd := exec.Command(argv[0], args...)
	cmd.Dir = path
	return cmd, nil
}

func missingTerminalCommandError(pref terminalPreference) error {
	if pref.source == "TERMINAL" {
		return fmt.Errorf("TERMINAL is set to %q, but that command was not found", pref.raw)
	}
	return fmt.Errorf("%s is set to %q, but that command was not found", pref.source, pref.raw)
}

func ghosttyAppleScriptDependencyError(action string) error {
	return fmt.Errorf("cannot %s: osascript and macOS Automation permission are required to control Ghostty; configure command = %q to use the CLI fallback", action, "ghostty")
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

func validateBrowserURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("browser URL cannot be empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse browser URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("browser URL must be an absolute http(s) URL")
	}
	return nil
}

func selectBrowserCommand(goos string, lookPath lookPathFunc) (commandSpec, error) {
	switch goos {
	case "darwin":
		if !commandExists("open", lookPath) {
			return commandSpec{}, errors.New("browser command open not found")
		}
		return commandSpec{name: "open"}, nil
	case "linux":
		if !commandExists("xdg-open", lookPath) {
			return commandSpec{}, errors.New("browser command xdg-open not found")
		}
		return commandSpec{name: "xdg-open"}, nil
	default:
		return commandSpec{}, fmt.Errorf("browser opening is not supported on %s", goos)
	}
}

// WorktreeSessionName returns a tmux/Zellij-safe session name derived from
// the worktree directory name plus a stable path fingerprint.
func WorktreeSessionName(path string) string {
	cleanPath := filepath.Clean(path)
	hashPath := cleanPath
	if absPath, err := filepath.Abs(cleanPath); err == nil {
		hashPath = absPath
	}

	name := sanitizeIdentifierPart(filepath.Base(cleanPath), "worktree")

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
	if strings.HasPrefix(strings.ToLower(rawURL), "github.com/") ||
		strings.HasPrefix(strings.ToLower(rawURL), "www.github.com/") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return pullRequestInput{}, fmt.Errorf("invalid PR number or URL: %q", input)
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" || host == "www.github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "pull" {
			if number, ok := parsePositiveInt(parts[3]); ok {
				return pullRequestInput{Number: number, Owner: parts[0], Repo: strings.TrimSuffix(parts[1], ".git")}, nil
			}
		}
		return pullRequestInput{}, fmt.Errorf("invalid GitHub PR URL: %q", input)
	}
	return pullRequestInput{}, fmt.Errorf("unsupported PR URL host: %s", u.Hostname())
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
		return exec.Command("sh", "-c", tmuxSwitchRunScript, "approach", sessionName, path, command.shellCommand())
	}
	return exec.Command("sh", "-c", tmuxSwitchScript, "approach", sessionName, path)
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

func macOSTerminalOpenCommand(app, path string) *exec.Cmd {
	if app == "iTerm" || app == "Ghostty" {
		return macOSTerminalScriptCommand(app, "cd "+shellQuote(path)+" && exec ${SHELL:-/bin/sh}")
	}
	return exec.Command("open", "-a", "Terminal", path)
}

// macOSTerminalScriptCommand opens a supported macOS GUI terminal running
// shellCommand. shellCommand is embedded via Go %q, which escapes the
// AppleScript string; untrusted values inside it are already single-quoted by
// shellQuote, so they cannot break out of either the AppleScript string or the
// shell. Exotic control characters (bell, vtab) are assumed absent from
// paths/branches/prompts; they would be mangled by the AppleScript layer but
// cannot inject.
func macOSTerminalScriptCommand(app, shellCommand string) *exec.Cmd {
	if app == "iTerm" {
		return exec.Command(
			"osascript",
			"-e", `tell application "iTerm"`,
			"-e", `activate`,
			"-e", `set newWindow to (create window with default profile)`,
			"-e", fmt.Sprintf(`tell current session of newWindow to write text %q`, shellCommand),
			"-e", `end tell`,
		)
	}
	if app == "Ghostty" {
		return exec.Command(
			"osascript",
			"-e", `tell application "Ghostty"`,
			"-e", `activate`,
			"-e", `set newWindow to new window`,
			"-e", `set targetTerminal to focused terminal of selected tab of newWindow`,
			"-e", fmt.Sprintf(`input text %q to targetTerminal`, shellCommand),
			"-e", `send key "enter" to targetTerminal`,
			"-e", `end tell`,
		)
	}
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
