// Package beadsmutate contains the sanctioned Beads mutation boundary.
package beadsmutate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Runner is the seam between claim orchestration and the bd CLI.
type Runner interface {
	Run(dir string, args ...string) (string, error)
}

var defaultRunner Runner = execRunner{}

// DefaultRunner is used by package-level mutation functions. Tests should
// prefer an injected runner through NewMutator.
var DefaultRunner Runner = defaultRunner

// Mutator claims Beads through an injected Runner.
type Mutator struct {
	runner Runner
}

// NewMutator constructs a Mutator over r. A nil runner uses the built-in bd
// executable adapter.
func NewMutator(r Runner) *Mutator {
	if r == nil {
		r = defaultRunner
	}
	return &Mutator{runner: r}
}

func defaultMutator() *Mutator {
	return NewMutator(DefaultRunner)
}

// Claim atomically claims beadID in repoPath through bd update --claim.
func Claim(repoPath, beadID string) error {
	return defaultMutator().Claim(repoPath, beadID)
}

// Claim atomically claims beadID in repoPath through bd update --claim.
func (m *Mutator) Claim(repoPath, beadID string) error {
	repoPath, beadID, err := canonicalClaimInput(repoPath, beadID)
	if err != nil {
		return err
	}
	if _, err := m.runner.Run(repoPath, "update", "--claim", "--", beadID); err != nil {
		return fmt.Errorf("claiming bead %s: %w", beadID, err)
	}
	return nil
}

func canonicalClaimInput(repoPath, beadID string) (string, string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", "", fmt.Errorf("claiming bead: repository path is blank")
	}
	if !filepath.IsAbs(repoPath) {
		return "", "", fmt.Errorf("claiming bead: repository path must be absolute")
	}
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return "", "", fmt.Errorf("claiming bead: bead ID is blank")
	}
	return filepath.Clean(repoPath), beadID, nil
}
