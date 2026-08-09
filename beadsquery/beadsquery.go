package beadsquery

import "fmt"

const closedLimit = 100

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
		return nil, fmt.Errorf("listing ready beads: %w", err)
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
		return nil, fmt.Errorf("listing blocked beads: %w", err)
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
		return nil, fmt.Errorf("listing in-progress beads: %w", err)
	}
	return ParseInProgress(out)
}

// ListClosed returns the selected repository's closed beads newest-first.
func ListClosed(repoPath string) ([]Bead, error) {
	return defaultQuery().ListClosed(repoPath)
}

// ListClosed returns the selected repository's closed beads newest-first.
func (q *Querier) ListClosed(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "closed", "--json", "--limit", fmt.Sprint(closedLimit), "--sort", "closed", "--readonly")
	if err != nil {
		return nil, fmt.Errorf("listing closed beads: %w", err)
	}
	return ParseClosed(out)
}

// CountClosed returns the selected repository's total number of closed beads.
func CountClosed(repoPath string) (int, error) {
	return defaultQuery().CountClosed(repoPath)
}

// CountClosed returns the selected repository's total number of closed beads.
func (q *Querier) CountClosed(repoPath string) (int, error) {
	out, err := q.runner.Run(repoPath, "stats", "--json", "--no-activity", "--readonly")
	if err != nil {
		return 0, fmt.Errorf("counting closed beads: %w", err)
	}
	return ParseClosedCount(out)
}

// ListOpen returns the selected repository's open beads.
func ListOpen(repoPath string) ([]Bead, error) {
	return defaultQuery().ListOpen(repoPath)
}

// ListOpen returns the selected repository's open beads.
func (q *Querier) ListOpen(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "open", "--json", "--limit", "0", "--readonly")
	if err != nil {
		return nil, fmt.Errorf("listing open beads: %w", err)
	}
	return ParseOpen(out)
}
