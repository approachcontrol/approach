package beadsquery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type beadJSON struct {
	ID        *string `json:"id"`
	Priority  *int    `json:"priority"`
	Title     *string `json:"title"`
	Assignee  *string `json:"assignee"`
	IssueType *string `json:"issue_type"`
	Parent    *string `json:"parent"`
	ClosedAt  *string `json:"closed_at"`
}

type parsedBead struct {
	bead     Bead
	closedAt time.Time
}

// ParseClosedCount decodes the aggregate closed issue count from bd stats JSON.
func ParseClosedCount(text string) (int, error) {
	var stats struct {
		Summary *struct {
			ClosedIssues *int `json:"closed_issues"`
		} `json:"summary"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		return 0, fmt.Errorf("parsing closed bead count: %w", err)
	}
	if stats.Summary == nil && len(stats.Data) > 0 {
		if err := json.Unmarshal(stats.Data, &stats); err != nil {
			return 0, fmt.Errorf("parsing closed bead count: %w", err)
		}
	}
	if stats.Summary == nil || stats.Summary.ClosedIssues == nil {
		return 0, fmt.Errorf("parsing closed bead count: missing summary.closed_issues")
	}
	if *stats.Summary.ClosedIssues < 0 {
		return 0, fmt.Errorf("parsing closed bead count: summary.closed_issues is negative")
	}
	return *stats.Summary.ClosedIssues, nil
}

// ParseOpen decodes bd list JSON and returns beads ordered by priority then ID.
func ParseOpen(text string) ([]Bead, error) {
	return parsePriorityBeads(text, "open")
}

// ParseReady decodes bd ready JSON and returns beads ordered by priority then ID.
func ParseReady(text string) ([]Bead, error) {
	return parsePriorityBeads(text, "ready")
}

// ParseChildren decodes bd children JSON and returns direct children ordered
// by priority, natural ID, and finally raw ID for equal-natural identifiers.
func ParseChildren(text string) ([]Bead, error) {
	records, err := parseBeadRecords(text, "children", false)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].bead.Priority != records[j].bead.Priority {
			return records[i].bead.Priority < records[j].bead.Priority
		}
		if order := naturalCompareIDs(records[i].bead.ID, records[j].bead.ID); order != 0 {
			return order < 0
		}
		return records[i].bead.ID < records[j].bead.ID
	})
	return beadsFromParsed(records), nil
}

// ParseBlocked decodes bd blocked-list JSON and returns beads ordered by priority then ID.
func ParseBlocked(text string) ([]Bead, error) {
	return parsePriorityBeads(text, "blocked")
}

// ParseInProgress decodes bd in-progress-list JSON and returns beads ordered by priority then ID.
func ParseInProgress(text string) ([]Bead, error) {
	return parsePriorityBeads(text, "in-progress")
}

// ParseClosed decodes bd closed-list JSON and returns beads ordered by newest
// close time, then natural ID.
func ParseClosed(text string) ([]Bead, error) {
	records, err := parseBeadRecords(text, "closed", true)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].closedAt.Equal(records[j].closedAt) {
			return records[i].closedAt.After(records[j].closedAt)
		}
		if order := naturalCompareIDs(records[i].bead.ID, records[j].bead.ID); order != 0 {
			return order < 0
		}
		return records[i].bead.ID < records[j].bead.ID
	})
	return beadsFromParsed(records), nil
}

func parsePriorityBeads(text, kind string) ([]Bead, error) {
	records, err := parseBeadRecords(text, kind, false)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].bead.Priority != records[j].bead.Priority {
			return records[i].bead.Priority < records[j].bead.Priority
		}
		if order := naturalCompareIDs(records[i].bead.ID, records[j].bead.ID); order != 0 {
			return order < 0
		}
		return records[i].bead.ID < records[j].bead.ID
	})
	return beadsFromParsed(records), nil
}

func parseBeadRecords(text, kind string, requireClosedAt bool) ([]parsedBead, error) {
	prefix := "parsing " + kind + " beads"
	payload := []byte(text)
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("%s: %w", prefix, err)
		}
		if len(envelope.Data) == 0 {
			return nil, fmt.Errorf("%s: expected envelope data array", prefix)
		}
		payload = envelope.Data
	}

	var records []*beadJSON
	if err := json.Unmarshal(payload, &records); err != nil {
		return nil, fmt.Errorf("%s: %w", prefix, err)
	}
	if records == nil {
		return nil, fmt.Errorf("%s: expected a JSON array", prefix)
	}

	beads := make([]parsedBead, len(records))
	for i, record := range records {
		if record == nil {
			return nil, fmt.Errorf("%s: bead %d is null", prefix, i)
		}
		if record.ID == nil || strings.TrimSpace(*record.ID) == "" {
			return nil, fmt.Errorf("%s: bead %d has no id", prefix, i)
		}
		if record.Priority == nil {
			return nil, fmt.Errorf("%s: bead %d has no priority", prefix, i)
		}
		if record.Title == nil || strings.TrimSpace(*record.Title) == "" {
			return nil, fmt.Errorf("%s: bead %d has no title", prefix, i)
		}
		beads[i].bead = Bead{ID: *record.ID, Priority: *record.Priority, Title: *record.Title}
		if record.Assignee != nil {
			beads[i].bead.Assignee = *record.Assignee
		}
		if record.IssueType != nil {
			beads[i].bead.IssueType = *record.IssueType
		}
		if record.Parent != nil {
			beads[i].bead.Parent = *record.Parent
		}
		if requireClosedAt {
			if record.ClosedAt == nil || strings.TrimSpace(*record.ClosedAt) == "" {
				return nil, fmt.Errorf("%s: bead %d has no closed_at", prefix, i)
			}
			closedAt, err := time.Parse(time.RFC3339, *record.ClosedAt)
			if err != nil {
				return nil, fmt.Errorf("%s: bead %d has invalid closed_at: %w", prefix, i, err)
			}
			beads[i].closedAt = closedAt
		}
	}
	return beads, nil
}

func beadsFromParsed(records []parsedBead) []Bead {
	beads := make([]Bead, len(records))
	for i := range records {
		beads[i] = records[i].bead
	}
	return beads
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
