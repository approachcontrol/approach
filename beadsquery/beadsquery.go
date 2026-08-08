package beadsquery

import "fmt"

// Bead is the display data for one Beads issue.
type Bead struct {
	ID       string
	Priority int
	Title    string
	Assignee string
}

// ListOpen returns the selected repository's open beads.
func ListOpen(repoPath string) ([]Bead, error) {
	return defaultQuery().ListOpen(repoPath)
}

// ListOpen returns the selected repository's open beads.
func (q *Querier) ListOpen(repoPath string) ([]Bead, error) {
	out, err := q.runner.Run(repoPath, "list", "-s", "open", "--json")
	if err != nil {
		return nil, fmt.Errorf("listing open beads: %w", err)
	}
	return ParseOpen(out)
}
