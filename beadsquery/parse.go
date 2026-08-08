package beadsquery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type openBeadJSON struct {
	ID       *string `json:"id"`
	Priority *int    `json:"priority"`
	Title    *string `json:"title"`
	Assignee *string `json:"assignee"`
}

// ParseOpen decodes bd list JSON and returns beads ordered by priority then ID.
func ParseOpen(text string) ([]Bead, error) {
	var records []*openBeadJSON
	if err := json.Unmarshal([]byte(text), &records); err != nil {
		return nil, fmt.Errorf("parsing open beads: %w", err)
	}
	if records == nil {
		return nil, fmt.Errorf("parsing open beads: expected a JSON array")
	}

	beads := make([]Bead, len(records))
	for i, record := range records {
		if record == nil {
			return nil, fmt.Errorf("parsing open beads: bead %d is null", i)
		}
		if record.ID == nil || strings.TrimSpace(*record.ID) == "" {
			return nil, fmt.Errorf("parsing open beads: bead %d has no id", i)
		}
		if record.Priority == nil {
			return nil, fmt.Errorf("parsing open beads: bead %d has no priority", i)
		}
		if record.Title == nil || strings.TrimSpace(*record.Title) == "" {
			return nil, fmt.Errorf("parsing open beads: bead %d has no title", i)
		}
		beads[i] = Bead{ID: *record.ID, Priority: *record.Priority, Title: *record.Title}
		if record.Assignee != nil {
			beads[i].Assignee = *record.Assignee
		}
	}
	sort.Slice(beads, func(i, j int) bool {
		if beads[i].Priority != beads[j].Priority {
			return beads[i].Priority < beads[j].Priority
		}
		return beads[i].ID < beads[j].ID
	})
	return beads, nil
}
