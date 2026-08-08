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
