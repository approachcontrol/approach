package flowstore

import (
	"reflect"
	"testing"
	"time"
)

// populatedFlowRecord returns a record with every field set to a non-zero value,
// so the round-trip test below fails if any converter drops one. Adding a field
// to FlowRecord without adding it here fails the test's own zero-value check.
func populatedFlowRecord() FlowRecord {
	created := time.Date(2024, 3, 4, 5, 6, 7, 8, time.UTC)
	updated := created.Add(time.Hour)
	mergedAt := updated.Add(time.Minute)
	closedAt := updated.Add(2 * time.Minute)
	return FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        "20240304-050607-parity",
		Title:         "parity",
		Instructions:  "instructions",
		Status:        StatusInProgress,
		RepoPath:      "/tmp/repo",
		WorktreePath:  "/tmp/worktree",
		Branch:        "feature",
		BaseRef:       "main",
		Commit:        "abc123",
		PresetName:    "custom",
		PlanID:        "plan-1",
		PlanPath:      "/tmp/plan.md",
		Issue:         Issue{Provider: "github", Number: 7, URL: "https://github.com/o/r/issues/7"},
		PR:            PullRequest{Provider: "github", Number: 9, URL: "https://github.com/o/r/pull/9", HeadBranch: "feature", BaseBranch: "main", Status: "open"},
		Merge:         Merge{Status: MergeMerged, Commit: "def456", MergedAt: &mergedAt},
		Closed:        Closure{Reason: "superseded", ClosedAt: &closedAt},
		AutoMode:      true,
		Headless:      true,
		Phases: []FlowPhase{{
			PhaseID: "implementation", ParentPhaseID: "plan", Title: "Implementation",
			Kind: KindImplementation, DependsOn: []string{"plan"}, Status: PhaseRunning,
			Order: 1, Outcome: OutcomeApproved, Notes: "notes", Summary: "summary",
			LaunchIDs: []string{"launch-1"},
			Sessions:  []Session{{Provider: "claude", SessionID: "s1", LaunchID: "launch-1", Status: "ended", StartedAt: created, EndedAt: updated, TranscriptPath: "/tmp/t.jsonl"}},
			CreatedAt: created, UpdatedAt: updated,
		}},
		CreatedAt:     created,
		UpdatedAt:     updated,
		GraphRecovery: GraphRecoveryState{Status: GraphRecoveryMissingEdgesUnresolved},
	}
}

// storedFlowDTO is a hand-maintained mirror of FlowRecord with hand-written
// converters in both directions. Nothing else in the package notices when the
// two drift: preset_test.go's golden file is generated through the DTO, so both
// sides of that comparison lose a forgotten field identically and the golden
// still passes. Drift is silent data loss — a new FlowRecord field is never
// persisted, and because decodeStoredFlow does not use DisallowUnknownFields, a
// field dropped from the DTO discards the persisted value on the next save.
//
// These tests are the guard. If one fails, update storedFlowDTO,
// storageDTOFromRecord, and storedFlowDTO.record together.

// jsonTagNames returns the JSON field names a struct persists, skipping fields
// tagged `json:"-"`.
func jsonTagNames(t *testing.T, value any) map[string]string {
	t.Helper()
	typ := reflect.TypeOf(value)
	if typ.Kind() != reflect.Struct {
		t.Fatalf("jsonTagNames requires a struct, got %s", typ.Kind())
	}
	names := map[string]string{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := tag
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				name = tag[:j]
				break
			}
		}
		if name == "" {
			name = field.Name
		}
		names[name] = field.Type.String()
	}
	return names
}

func TestStorageDTOCoversEveryFlowRecordField(t *testing.T) {
	recordFields := jsonTagNames(t, FlowRecord{})
	dtoFields := jsonTagNames(t, storedFlowDTO{})

	// graph_recovery is storage-only: FlowRecord carries GraphRecovery as
	// `json:"-"` and the DTO persists the unresolved marker explicitly.
	delete(dtoFields, "graph_recovery")

	for name, recordType := range recordFields {
		dtoType, ok := dtoFields[name]
		if !ok {
			t.Errorf("FlowRecord field %q has no storedFlowDTO counterpart; it would never be persisted", name)
			continue
		}
		if dtoType != recordType {
			t.Errorf("field %q: FlowRecord type %s, storedFlowDTO type %s", name, recordType, dtoType)
		}
		delete(dtoFields, name)
	}
	for name := range dtoFields {
		t.Errorf("storedFlowDTO field %q has no FlowRecord counterpart; decoding would silently drop it", name)
	}
}

// TestStorageDTORoundTripsEveryPopulatedField catches the case the tag-set test
// cannot: a field present in both structs that one of the hand-written
// converters forgets to copy. Every non-zero field must survive the round trip.
func TestStorageDTORoundTripsEveryPopulatedField(t *testing.T) {
	original := populatedFlowRecord()
	round := storageDTOFromRecord(original).record()

	originalValue := reflect.ValueOf(original)
	roundValue := reflect.ValueOf(round)
	typ := originalValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		want := originalValue.Field(i).Interface()
		got := roundValue.Field(i).Interface()
		if field.Tag.Get("json") == "-" && field.Name != "GraphRecovery" {
			continue
		}
		if originalValue.Field(i).IsZero() {
			t.Errorf("populatedFlowRecord leaves %s zero, so the round trip does not exercise it", field.Name)
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("field %s did not survive the DTO round trip: got %#v, want %#v", field.Name, got, want)
		}
	}
}
