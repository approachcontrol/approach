package gitquery

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner is the seam between query orchestration and the git CLI.
type Runner interface {
	// Run executes git and returns stdout. On failure, git stderr is folded
	// into the error, matching the historical gitCmd contract.
	Run(dir string, args ...string) (string, error)
	// Ok executes git only for its exit code. Predicate commands such as
	// merge-base --is-ancestor use nil as the affirmative answer.
	Ok(dir string, args ...string) error
}

type execRunner struct{}

var defaultRunner Runner = execRunner{}

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

func (execRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Output captures stderr into (*exec.ExitError).Stderr when cmd.Stderr is
	// nil, but its error string is only "exit status N". Fold the git stderr
	// diagnostic into the returned error while keeping stdout clean for parsing.
	out, err := cmd.Output()
	if err != nil {
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

func (execRunner) Ok(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
