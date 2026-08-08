package beadsquery

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner is the seam between Beads query orchestration and the bd CLI.
type Runner interface {
	Run(dir string, args ...string) (string, error)
}

type execRunner struct{}

var defaultRunner Runner = execRunner{}

// DefaultRunner is used by package-level query functions. Tests should prefer
// an injected runner through NewQuerier.
var DefaultRunner Runner = defaultRunner

// Querier orchestrates Beads queries over an injected Runner.
type Querier struct {
	runner Runner
}

// NewQuerier constructs a Querier over r. A nil runner uses the built-in bd
// executable adapter.
func NewQuerier(r Runner) *Querier {
	if r == nil {
		r = defaultRunner
	}
	return &Querier{runner: r}
}

func defaultQuery() *Querier {
	return NewQuerier(DefaultRunner)
}

func (execRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("bd", args...)
	cmd.Dir = dir
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
