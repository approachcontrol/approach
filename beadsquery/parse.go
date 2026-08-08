package beadsquery

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ParseOpen decodes bd list JSON and returns beads ordered by priority then ID.
func ParseOpen(text string) ([]Bead, error) {
	var beads []Bead
	if err := json.Unmarshal([]byte(text), &beads); err != nil {
		return nil, fmt.Errorf("parsing open beads: %w", err)
	}
	sort.Slice(beads, func(i, j int) bool {
		if beads[i].Priority != beads[j].Priority {
			return beads[i].Priority < beads[j].Priority
		}
		return beads[i].ID < beads[j].ID
	})
	return beads, nil
}
