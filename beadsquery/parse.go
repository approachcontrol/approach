package beadsquery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	payload := []byte(text)
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("parsing open beads: %w", err)
		}
		if len(envelope.Data) == 0 {
			return nil, fmt.Errorf("parsing open beads: expected envelope data array")
		}
		payload = envelope.Data
	}

	var records []*openBeadJSON
	if err := json.Unmarshal(payload, &records); err != nil {
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
		return naturalCompareIDs(beads[i].ID, beads[j].ID) < 0
	})
	return beads, nil
}

func naturalCompareIDs(a, b string) int {
	aSegments := splitIDSegments(a)
	bSegments := splitIDSegments(b)
	for i := 0; i < len(aSegments) && i < len(bSegments); i++ {
		if aSegments[i] == bSegments[i] {
			continue
		}
		aNumber, aErr := strconv.Atoi(aSegments[i])
		bNumber, bErr := strconv.Atoi(bSegments[i])
		if aErr == nil && bErr == nil {
			switch {
			case aNumber < bNumber:
				return -1
			case aNumber > bNumber:
				return 1
			}
			continue
		}
		if aSegments[i] < bSegments[i] {
			return -1
		}
		return 1
	}
	switch {
	case len(aSegments) < len(bSegments):
		return -1
	case len(aSegments) > len(bSegments):
		return 1
	default:
		return 0
	}
}

func splitIDSegments(id string) []string {
	var segments []string
	start := 0
	for i, r := range id {
		if r != '.' && r != '-' {
			continue
		}
		if i > start {
			segments = append(segments, id[start:i])
		}
		start = i + 1
	}
	if start < len(id) {
		segments = append(segments, id[start:])
	}
	return segments
}
