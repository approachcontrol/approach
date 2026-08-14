package flowstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStorageCodecPersistsBeadLinkStates(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	base := FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        "bead-codec",
		Title:         "Bead codec",
		Status:        StatusPending,
		RepoPath:      "/tmp/repo",
		Headless:      true,
		Phases:        []FlowPhase{},
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
	for _, tt := range []struct {
		name         string
		bead         BeadLink
		wantFragment string
		wantAbsent   string
	}{
		{name: "linked child", bead: BeadLink{ID: "child", EpicID: "epic"}, wantFragment: "\"bead\": {\n    \"id\": \"child\",\n    \"epic_id\": \"epic\"\n  }"},
		{name: "child without epic", bead: BeadLink{ID: "child"}, wantFragment: "\"bead\": {\n    \"id\": \"child\"\n  }", wantAbsent: `"epic_id"`},
		{name: "unlinked", wantAbsent: `"bead"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := base
			record.Bead = tt.bead
			encoded, projection, err := encodeStoredFlow(record)
			if err != nil {
				t.Fatalf("encodeStoredFlow() error = %v", err)
			}
			if tt.wantFragment != "" && !strings.Contains(string(encoded), tt.wantFragment) {
				t.Fatalf("encoded storage JSON =\n%s\nwant fragment:\n%s", encoded, tt.wantFragment)
			}
			if tt.wantAbsent != "" && strings.Contains(string(encoded), tt.wantAbsent) {
				t.Fatalf("encoded storage JSON unexpectedly contains %s:\n%s", tt.wantAbsent, encoded)
			}
			decoded, err := decodeStoredFlow(projection.flowID, projection.repoPath, projection.status, projection.updatedAt, projection.beadID, projection.epicID, encoded)
			if err != nil {
				t.Fatalf("decodeStoredFlow() error = %v", err)
			}
			if decoded.record.Bead != tt.bead {
				t.Fatalf("decoded Bead = %#v, want %#v", decoded.record.Bead, tt.bead)
			}
		})
	}
}

func TestSQLiteStorageCodecRejectsOrphanEpicID(t *testing.T) {
	record := populatedFlowRecord()
	record.Bead = BeadLink{EpicID: "epic"}
	if _, _, err := encodeStoredFlow(record); err == nil || !strings.Contains(err.Error(), "epic") {
		t.Fatalf("encodeStoredFlow() error = %v, want orphan epic rejection", err)
	}

	dto := storageDTOFromRecord(populatedFlowRecord())
	dto.Bead = BeadLink{EpicID: "epic"}
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	projection := flowProjection{flowID: dto.FlowID, repoPath: dto.RepoPath, status: dto.Status}
	projection.updatedAt, err = formatStorageTime(dto.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStoredFlow(projection.flowID, projection.repoPath, projection.status, projection.updatedAt, "", "epic", data); err == nil || !strings.Contains(err.Error(), "epic") {
		t.Fatalf("decodeStoredFlow() error = %v, want orphan epic rejection", err)
	}
}

func TestSQLiteStorageCodecRejectsPresentBeadWithoutID(t *testing.T) {
	dto := storageDTOFromRecord(populatedFlowRecord())
	dto.Bead = BeadLink{}
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"issue":`, `"bead":{},"issue":`, 1))
	updatedAt, err := formatStorageTime(dto.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStoredFlow(dto.FlowID, dto.RepoPath, dto.Status, updatedAt, "", "", data); err == nil || !strings.Contains(err.Error(), "requires a bead id") {
		t.Fatalf("decodeStoredFlow() error = %v, want present Bead without ID rejection", err)
	}
}

func TestSQLiteStorageCodecDecodesPreBeadRecordAsUnlinked(t *testing.T) {
	data := []byte(`{"schema_version":1,"flow_id":"pre-bead","title":"Legacy","instructions":"","status":"pending","repo_path":"/tmp/repo","headless":true,"phases":[],"created_at":"2026-08-14T12:00:00Z","updated_at":"2026-08-14T12:00:00Z"}`)
	decoded, err := decodeStoredFlow("pre-bead", "/tmp/repo", StatusPending, "2026-08-14T12:00:00.000000000Z", "", "", data)
	if err != nil {
		t.Fatalf("decodeStoredFlow() error = %v", err)
	}
	if decoded.record.Bead != (BeadLink{}) {
		t.Fatalf("decoded Bead = %#v, want zero value", decoded.record.Bead)
	}
	if decoded.record.SchemaVersion != schemaVersion {
		t.Fatalf("schema version = %d, want %d", decoded.record.SchemaVersion, schemaVersion)
	}
}

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

	decoded, err := decodeStoredFlow(projection.flowID, projection.repoPath, projection.status, projection.updatedAt, projection.beadID, projection.epicID, encoded)
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
