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

// Close closes beadID in repoPath through bd close, recording reason when it is
// non-blank. bd close is idempotent: closing an already-closed Bead succeeds, so
// a retried caller does not have to probe status first.
//
// The reason precedes the -- separator deliberately. bd treats every argument
// after -- as positional, so a trailing --reason is resolved as a second issue
// ID and the call fails.
func Close(repoPath, beadID, reason string) error {
	return defaultMutator().Close(repoPath, beadID, reason)
}

// Close closes beadID in repoPath through bd close, recording reason when it is
// non-blank.
func (m *Mutator) Close(repoPath, beadID, reason string) error {
	repoPath, beadID, err := canonicalMutationInput("closing bead", repoPath, beadID)
	if err != nil {
		return err
	}
	args := make([]string, 0, 4)
	args = append(args, "close")
	if reason = strings.TrimSpace(reason); reason != "" {
		args = append(args, "--reason="+reason)
	}
	args = append(args, "--", beadID)
	if _, err := m.runner.Run(repoPath, args...); err != nil {
		return fmt.Errorf("closing bead %s: %w", beadID, err)
	}
	return nil
}

func canonicalClaimInput(repoPath, beadID string) (string, string, error) {
	return canonicalMutationInput("claiming bead", repoPath, beadID)
}

func canonicalMutationInput(action, repoPath, beadID string) (string, string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", "", fmt.Errorf("%s: repository path is blank", action)
	}
	if !filepath.IsAbs(repoPath) {
		return "", "", fmt.Errorf("%s: repository path must be absolute", action)
	}
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return "", "", fmt.Errorf("%s: bead ID is blank", action)
	}
	return filepath.Clean(repoPath), beadID, nil
}
