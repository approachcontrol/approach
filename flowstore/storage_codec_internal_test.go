package flowstore

import (
	"strings"
	"testing"
	"time"
)

func TestSQLiteStorageCodecPersistsOnlyUnresolvedGraphRecovery(t *testing.T) {
	stamp := time.Date(1, time.February, 3, 4, 5, 6, 7, time.FixedZone("offset", 3600))
	record := FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        "codec-flow",
		Title:         "Codec",
		Status:        StatusPending,
		RepoPath:      "/tmp/../tmp/repo",
		Headless:      true,
		Phases:        []FlowPhase{},
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
		GraphRecovery: GraphRecoveryState{Status: GraphRecoveryMissingEdgesUnresolved},
	}

	encoded, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatalf("encodeStoredFlow() error = %v", err)
	}
	if !strings.HasSuffix(string(encoded), "\n  }\n}") || !strings.Contains(string(encoded), `"status": "missing_edges_unresolved"`) {
		t.Fatalf("encoded storage JSON does not end with graph_recovery: %s", encoded)
	}
	if strings.HasSuffix(string(encoded), "\n") {
		t.Fatalf("encoded storage JSON has a trailing newline: %q", encoded)
	}
	if projection.repoPath != "/tmp/repo" {
		t.Fatalf("repo projection = %q, want cleaned path", projection.repoPath)
	}
	if projection.updatedAt != "0001-02-03T03:05:06.000000007Z" {
		t.Fatalf("updated projection = %q", projection.updatedAt)
	}

	decoded, err := decodeStoredFlow(projection.flowID, projection.repoPath, projection.status, projection.updatedAt, encoded)
	if err != nil {
		t.Fatalf("decodeStoredFlow() error = %v", err)
	}
	if decoded.record.GraphRecovery.Status != GraphRecoveryMissingEdgesUnresolved {
		t.Fatalf("graph recovery = %q", decoded.record.GraphRecovery.Status)
	}

	record.GraphRecovery.Status = GraphRecoveryPresetEdgesRestored
	encoded, _, err = encodeStoredFlow(record)
	if err != nil {
		t.Fatalf("encodeStoredFlow(recovered) error = %v", err)
	}
	if strings.Contains(string(encoded), "graph_recovery") {
		t.Fatalf("recovered storage JSON unexpectedly persists graph_recovery: %s", encoded)
	}
}

func TestSQLiteStorageCodecRejectsOutOfRangeUpdatedTime(t *testing.T) {
	record := FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        "bad-time",
		Status:        StatusPending,
		RepoPath:      "/tmp/repo",
		Phases:        []FlowPhase{},
		CreatedAt:     time.Time{},
		UpdatedAt:     time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if _, _, err := encodeStoredFlow(record); err == nil {
		t.Fatal("encodeStoredFlow() error = nil for year 10000")
	}
}

func TestSQLiteStorageCodecAcceptsSupportedYearBoundaries(t *testing.T) {
	for _, year := range []int{0, 1, 9999} {
		t.Run(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).String(), func(t *testing.T) {
			stamp := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			record := FlowRecord{
				SchemaVersion: schemaVersion, FlowID: "boundary", Status: StatusPending,
				RepoPath: "/tmp/repo", Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
			}
			if _, _, err := encodeStoredFlow(record); err != nil {
				t.Fatalf("encodeStoredFlow(year %d) error = %v", year, err)
			}
		})
	}
	for _, year := range []int{-1, 10000} {
		stamp := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		record := FlowRecord{
			SchemaVersion: schemaVersion, FlowID: "out-of-range", Status: StatusPending,
			RepoPath: "/tmp/repo", Phases: []FlowPhase{}, CreatedAt: stamp, UpdatedAt: stamp,
		}
		if _, _, err := encodeStoredFlow(record); err == nil {
			t.Fatalf("encodeStoredFlow(year %d) error = nil", year)
		}
	}
}
