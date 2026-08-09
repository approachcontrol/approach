package beadsquery

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotConfigured marks repositories where Beads cannot be queried because
// the bd executable or a Beads project/database is absent. Callers should use
// errors.Is rather than inspect the display text.
var ErrNotConfigured = errors.New("beads not configured")

type notConfiguredError struct {
	cause error
}

func (e notConfiguredError) Error() string { return e.cause.Error() }
func (e notConfiguredError) Unwrap() error { return e.cause }
func (e notConfiguredError) Is(target error) bool {
	return target == ErrNotConfigured
}

// Bead is the display data for one Beads issue.
type Bead struct {
	ID       string
	Priority int
	Title    string
	Assignee string
}

// ListReady returns the selected repository's dependency-graph-ready beads.
func ListReady(repoPath string) ([]Bead, error) {
	return defaultQuery().ListReady(repoPath)
}

// ListReady returns the selected repository's dependency-graph-ready beads.
func (q *Querier) ListReady(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "ready", "--json", "--limit", "0", "--readonly")
	if err != nil {
		return nil, queryError("ready", err)
	}
	return ParseReady(out)
}

// ListBlocked returns the selected repository's blocked beads.
func ListBlocked(repoPath string) ([]Bead, error) {
	return defaultQuery().ListBlocked(repoPath)
}

// ListBlocked returns the selected repository's blocked beads.
func (q *Querier) ListBlocked(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "blocked", "--json", "--limit", "0", "--readonly")
	if err != nil {
		return nil, queryError("blocked", err)
	}
	return ParseBlocked(out)
}

// ListInProgress returns the selected repository's in-progress beads.
func ListInProgress(repoPath string) ([]Bead, error) {
	return defaultQuery().ListInProgress(repoPath)
}

// ListInProgress returns the selected repository's in-progress beads.
func (q *Querier) ListInProgress(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "in_progress", "--json", "--limit", "0", "--readonly")
	if err != nil {
		return nil, queryError("in-progress", err)
	}
	return ParseInProgress(out)
}

// ListClosed returns the selected repository's closed beads newest-first.
func ListClosed(repoPath string) ([]Bead, error) {
	return defaultQuery().ListClosed(repoPath)
}

// ListClosed returns the selected repository's closed beads newest-first.
func (q *Querier) ListClosed(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "closed", "--json", "--limit", "0", "--sort", "closed", "--reverse", "--readonly")
	if err != nil {
		return nil, queryError("closed", err)
	}
	return ParseClosed(out)
}

// ListOpen returns the selected repository's open beads.
func ListOpen(repoPath string) ([]Bead, error) {
	return defaultQuery().ListOpen(repoPath)
}

// ListOpen returns the selected repository's open beads.
func (q *Querier) ListOpen(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "open", "--json", "--limit", "0", "--readonly")
	if err != nil {
		return nil, queryError("open", err)
	}
	return ParseOpen(out)
}

func queryError(kind string, err error) error {
	return fmt.Errorf("listing %s beads: %w", kind, classifyRunnerError(err))
}

func classifyRunnerError(err error) error {
	if errors.Is(err, exec.ErrNotFound) || hasAbsenceDiagnostic(err.Error()) {
		return notConfiguredError{cause: err}
	}
	return err
}

func hasAbsenceDiagnostic(message string) bool {
	segments := strings.FieldsFunc(message, func(r rune) bool {
		return r == ':' || r == '\n'
	})
	for _, segment := range segments {
		normalized := strings.ToLower(strings.Join(strings.Fields(segment), " "))
		// bd terminates its no-database diagnostic with a period before the
		// newline-delimited setup hint.
		normalized = strings.TrimSuffix(normalized, ".")
		if normalized == "no beads project found" || normalized == "no beads database found" {
			return true
		}
	}
	return false
}
