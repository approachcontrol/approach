package gitquery

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultGitTimeout = 30 * time.Second
const gitWaitDelay = 2 * time.Second

// Runner is the seam between query orchestration and the git CLI.
type Runner interface {
	// Run executes git and returns stdout. On failure, git stderr is folded
	// into the error, matching the historical gitCmd contract.
	Run(dir string, args ...string) (string, error)
	// Predicate executes a Git predicate. Exit 0 is true, exit 1 is false, and
	// execution failures remain distinguishable from an ordinary false result.
	Predicate(dir string, args ...string) (bool, error)
}

type execRunner struct {
	timeout time.Duration
}

var defaultRunner Runner = execRunner{timeout: defaultGitTimeout}

// DefaultRunner is used by package-level query functions. Override it only for
// process-wide integration hooks; tests should prefer NewQuerier with a fake.
var DefaultRunner Runner = defaultRunner

// Querier orchestrates git queries over an injected Runner.
type Querier struct {
	git Runner
}

// NewQuerier constructs a Querier over r. A nil runner uses the built-in git
// executable adapter.
func NewQuerier(r Runner) *Querier {
	if r == nil {
		r = defaultRunner
	}
	return &Querier{git: r}
}

func defaultQuery() *Querier {
	return NewQuerier(DefaultRunner)
}

func (r execRunner) Run(dir string, args ...string) (string, error) {
	timeout := r.commandTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	// Output captures stderr into (*exec.ExitError).Stderr when cmd.Stderr is
	// nil, but its error string is only "exit status N". Fold the git stderr
	// diagnostic into the returned error while keeping stdout clean for parsing.
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s timed out after %s: %w", gitOperation(args), timeout, ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
				return "", fmt.Errorf("%s: %w", msg, err)
			}
		}
		return "", err
	}
	return string(out), nil
}

func (r execRunner) Predicate(dir string, args ...string) (bool, error) {
	timeout := r.commandTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return false, fmt.Errorf("git %s timed out after %s: %w", gitOperation(args), timeout, ctx.Err())
	}
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (r execRunner) commandTimeout() time.Duration {
	if r.timeout <= 0 {
		return defaultGitTimeout
	}
	return r.timeout
}

func gitOperation(args []string) string {
	if len(args) == 0 {
		return "command"
	}
	return args[0]
}
