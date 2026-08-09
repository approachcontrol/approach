package beadsquery_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/approachcontrol/approach/beadsquery"
)

func TestParseOpenSortsByPriorityThenID(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/list_open.json")
	if err != nil {
		t.Fatal(err)
	}

	got, err := beadsquery.ParseOpen(string(input))
	if err != nil {
		t.Fatalf("ParseOpen() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-123", Priority: 1, Title: "Fix cache", Assignee: "alice"},
		{ID: "bd-125", Priority: 1, Title: "Polish cache"},
		{ID: "bd-124", Priority: 2, Title: "Document cache"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseOpen() = %#v, want %#v", got, want)
	}
}

func TestParseOpenSortsCounterIDsNaturally(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseOpen(`[
		{"id":"bd-10","priority":1,"title":"Tenth"},
		{"id":"bd-2","priority":1,"title":"Second"},
		{"id":"bd-1","priority":1,"title":"First"}
	]`)
	if err != nil {
		t.Fatalf("ParseOpen() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-1", Priority: 1, Title: "First"},
		{ID: "bd-2", Priority: 1, Title: "Second"},
		{ID: "bd-10", Priority: 1, Title: "Tenth"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseOpen() = %#v, want %#v", got, want)
	}
}

func TestParseOpenEmptyList(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseOpen("[]")
	if err != nil {
		t.Fatalf("ParseOpen() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParseOpen() = %#v, want no beads", got)
	}
}

func TestParseOpenAcceptsJSONEnvelope(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseOpen(`{
		"schema_version": 1,
		"data": [
			{"id":"bd-2","priority":2,"title":"Second"},
			{"id":"bd-1","priority":1,"title":"First","assignee":"alice"}
		]
	}`)
	if err != nil {
		t.Fatalf("ParseOpen() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-1", Priority: 1, Title: "First", Assignee: "alice"},
		{ID: "bd-2", Priority: 2, Title: "Second"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseOpen() = %#v, want %#v", got, want)
	}
}

func TestParseOpenRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseOpen(`[{"id":"bd-123"}`)
	if err == nil {
		t.Fatal("ParseOpen() error = nil, want malformed JSON error")
	}
	if got != nil {
		t.Fatalf("ParseOpen() = %#v, want no partial data", got)
	}
}

func TestParseOpenRejectsStructurallyInvalidJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "null list", input: `null`},
		{name: "missing envelope data", input: `{"schema_version":1}`},
		{name: "null envelope data", input: `{"schema_version":1,"data":null}`},
		{name: "null bead", input: `[null]`},
		{name: "missing id", input: `[{"priority":1,"title":"Missing ID"}]`},
		{name: "missing priority", input: `[{"id":"bd-1","title":"Missing priority"}]`},
		{name: "missing title", input: `[{"id":"bd-1","priority":1}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := beadsquery.ParseOpen(tt.input)
			if err == nil {
				t.Fatalf("ParseOpen(%s) error = nil, want structural validation error", tt.input)
			}
			if got != nil {
				t.Fatalf("ParseOpen(%s) = %#v, want no partial data", tt.input, got)
			}
		})
	}
}

func TestParseOpenTreatsMissingNullAndEmptyAssigneesAsAbsent(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseOpen(`[
		{"id":"bd-1","priority":1,"title":"Missing"},
		{"id":"bd-2","priority":1,"title":"Null","assignee":null},
		{"id":"bd-3","priority":1,"title":"Empty","assignee":""}
	]`)
	if err != nil {
		t.Fatalf("ParseOpen() error = %v", err)
	}
	for _, bead := range got {
		if bead.Assignee != "" {
			t.Fatalf("ParseOpen() bead %q assignee = %q, want absent", bead.ID, bead.Assignee)
		}
	}
}

func TestParseReadySortsCapturedRowsByPriorityThenNaturalID(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/ready.json")
	if err != nil {
		t.Fatal(err)
	}

	got, err := beadsquery.ParseReady(string(input))
	if err != nil {
		t.Fatalf("ParseReady() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-1", Priority: 0, Title: "Highest priority ready bead"},
		{ID: "bd-2", Priority: 1, Title: "Second ready bead", Assignee: "alice"},
		{ID: "bd-10", Priority: 1, Title: "Tenth ready bead"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseReady() = %#v, want %#v", got, want)
	}
}

func TestParseBlockedAcceptsCapturedEnvelopeAndSortsRows(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/list_blocked.json")
	if err != nil {
		t.Fatal(err)
	}

	got, err := beadsquery.ParseBlocked(string(input))
	if err != nil {
		t.Fatalf("ParseBlocked() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-3", Priority: 1, Title: "Higher priority blocker", Assignee: "bob"},
		{ID: "bd-11", Priority: 2, Title: "Lower priority blocker"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseBlocked() = %#v, want %#v", got, want)
	}
}

func TestParseInProgressSortsCapturedRowsByPriorityThenNaturalID(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/list_in_progress.json")
	if err != nil {
		t.Fatal(err)
	}

	got, err := beadsquery.ParseInProgress(string(input))
	if err != nil {
		t.Fatalf("ParseInProgress() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-4", Priority: 1, Title: "First in-progress bead"},
		{ID: "bd-20", Priority: 1, Title: "Later in-progress bead", Assignee: "carol"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseInProgress() = %#v, want %#v", got, want)
	}
}

func TestParseClosedSortsCapturedRowsByDescendingCloseTimeThenNaturalID(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/list_closed.json")
	if err != nil {
		t.Fatal(err)
	}

	got, err := beadsquery.ParseClosed(string(input))
	if err != nil {
		t.Fatalf("ParseClosed() error = %v", err)
	}
	want := []beadsquery.Bead{
		{ID: "bd-1", Priority: 0, Title: "Newest closed bead"},
		{ID: "bd-2", Priority: 3, Title: "Second equal-time closed bead"},
		{ID: "bd-10", Priority: 1, Title: "Tenth equal-time closed bead", Assignee: "dana"},
		{ID: "bd-3", Priority: 2, Title: "Oldest closed bead"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseClosed() = %#v, want %#v", got, want)
	}
}

func TestNewBeadParsersRejectStructurallyInvalidDisplayRows(t *testing.T) {
	t.Parallel()

	parsers := []struct {
		name  string
		parse func(string) ([]beadsquery.Bead, error)
	}{
		{name: "ready", parse: beadsquery.ParseReady},
		{name: "blocked", parse: beadsquery.ParseBlocked},
		{name: "in-progress", parse: beadsquery.ParseInProgress},
		{name: "closed", parse: beadsquery.ParseClosed},
	}
	inputs := []struct {
		name  string
		input string
	}{
		{name: "null list", input: `null`},
		{name: "missing envelope data", input: `{"schema_version":1}`},
		{name: "null envelope data", input: `{"schema_version":1,"data":null}`},
		{name: "null bead", input: `[null]`},
		{name: "missing id", input: `[{"priority":1,"title":"Missing ID"}]`},
		{name: "blank id", input: `[{"id":"  ","priority":1,"title":"Blank ID"}]`},
		{name: "missing priority", input: `[{"id":"bd-1","title":"Missing priority"}]`},
		{name: "missing title", input: `[{"id":"bd-1","priority":1}]`},
		{name: "blank title", input: `[{"id":"bd-1","priority":1,"title":"  "}]`},
	}

	for _, parser := range parsers {
		parser := parser
		for _, input := range inputs {
			input := input
			t.Run(parser.name+"/"+input.name, func(t *testing.T) {
				t.Parallel()
				got, err := parser.parse(input.input)
				if err == nil {
					t.Fatalf("parse(%s) error = nil, want structural validation error", input.input)
				}
				if got != nil {
					t.Fatalf("parse(%s) = %#v, want no partial data", input.input, got)
				}
			})
		}
	}
}

func TestParseClosedRequiresValidRFC3339ClosedAtWithoutPartialData(t *testing.T) {
	t.Parallel()

	inputs := []struct {
		name     string
		closedAt string
	}{
		{name: "missing", closedAt: ""},
		{name: "null", closedAt: `,"closed_at":null`},
		{name: "blank", closedAt: `,"closed_at":"  "`},
		{name: "malformed", closedAt: `,"closed_at":"yesterday"`},
	}
	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			input := `[{"id":"bd-1","priority":1,"title":"First","closed_at":"2026-08-08T20:00:00Z"},` +
				`{"id":"bd-2","priority":1,"title":"Second"` + tt.closedAt + `}]`
			got, err := beadsquery.ParseClosed(input)
			if err == nil {
				t.Fatalf("ParseClosed(%s) error = nil, want closed_at validation error", input)
			}
			if got != nil {
				t.Fatalf("ParseClosed(%s) = %#v, want no partial data", input, got)
			}
		})
	}
}

func TestParseClosedUsesRawIDAsFinalEqualTimeTieBreak(t *testing.T) {
	t.Parallel()

	got, err := beadsquery.ParseClosed(`[
		{"id":"bd-1","priority":1,"title":"Plain","closed_at":"2026-08-08T20:00:00Z"},
		{"id":"bd-01","priority":1,"title":"Padded","closed_at":"2026-08-08T20:00:00Z"}
	]`)
	if err != nil {
		t.Fatalf("ParseClosed() error = %v", err)
	}
	if got[0].ID != "bd-01" || got[1].ID != "bd-1" {
		t.Fatalf("ParseClosed() IDs = [%q %q], want deterministic raw-ID order", got[0].ID, got[1].ID)
	}
}
