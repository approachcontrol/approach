package flowstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

func readSidecarFile(t *testing.T, root string) databaseSidecar {
	t.Helper()
	data, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sidecar databaseSidecar
	if err := json.Unmarshal(data, &sidecar); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	return sidecar
}

func TestARealMigrationWritesTheSidecar(t *testing.T) {
	root := schemaFiveRoot(t)
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	sidecar := readSidecarFile(t, root)
	if sidecar.SchemaVersion != sidecarSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", sidecar.SchemaVersion, sidecarSchemaVersion)
	}
	if sidecar.PhysicalVersion != databaseSchemaVersion {
		t.Fatalf("physical_version = %d, want %d", sidecar.PhysicalVersion, databaseSchemaVersion)
	}
	if sidecar.MinReaderGeneration != databaseSchemaVersion || sidecar.MinWriterGeneration != databaseSchemaVersion {
		t.Fatalf("generations = %d/%d, want %d", sidecar.MinReaderGeneration, sidecar.MinWriterGeneration, databaseSchemaVersion)
	}
	if sidecar.Provenance != sidecarProvenanceMigrated {
		t.Fatalf("provenance = %q, want %q", sidecar.Provenance, sidecarProvenanceMigrated)
	}
	if sidecar.GenerationID == "" || sidecar.MigratedBy.At == "" || sidecar.MigratedBy.BuildVersion == "" {
		t.Fatalf("sidecar is missing provenance detail: %+v", sidecar)
	}
}

func TestAFreshlyCreatedRootHasNoSidecar(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := os.Stat(sidecarPath(root)); !os.IsNotExist(err) {
		t.Fatalf("creation wrote a sidecar: %v", err)
	}
	if store.OpenDiagnostics().SidecarStale {
		t.Fatal("a missing sidecar must not read as stale")
	}
}

// A sidecar that disagrees with user_version is repaired from user_version,
// never the reverse — and by the migrator alone.
func TestAMigratorRepairsADisagreeingSidecarAndAWriterOnlyReportsIt(t *testing.T) {
	root := t.TempDir()
	created, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	stale := databaseSidecar{
		SchemaVersion:       sidecarSchemaVersion,
		GenerationID:        "deadbeefdeadbeef",
		PhysicalVersion:     databaseSchemaVersion - 1,
		MinReaderGeneration: databaseSchemaVersion - 1,
		MinWriterGeneration: databaseSchemaVersion - 1,
		Provenance:          sidecarProvenanceMigrated,
	}
	data, err := json.MarshalIndent(stale, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath(root), data, artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}

	// The writer sees the drift, says so, and changes nothing: repairing is a
	// write to the root under a lock, and one role owns those.
	writer, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	if !writer.OpenDiagnostics().SidecarStale {
		t.Fatal("the writer did not report the stale sidecar")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the writer rewrote the sidecar")
	}

	migrator, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if !migrator.OpenDiagnostics().SidecarStale {
		t.Fatal("the migrator did not report what it found before repairing it")
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	repaired := readSidecarFile(t, root)
	if repaired.PhysicalVersion != databaseSchemaVersion {
		t.Fatalf("physical_version = %d, want %d", repaired.PhysicalVersion, databaseSchemaVersion)
	}
	if repaired.Provenance != sidecarProvenanceReconstructed {
		t.Fatalf("provenance = %q, want %q", repaired.Provenance, sidecarProvenanceReconstructed)
	}
	if repaired.GenerationID != "deadbeefdeadbeef" {
		t.Fatalf("a repair invented a new generation ID: %q", repaired.GenerationID)
	}
	// The disagreement never fed a compatibility decision: the open succeeded
	// rather than refusing on the sidecar's claimed generation.
	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatalf("a repaired root must open cleanly: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestADisagreeingSidecarNeverDrivesACompatibilityRefusal(t *testing.T) {
	root := t.TempDir()
	created, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	future := databaseSidecar{
		SchemaVersion:       sidecarSchemaVersion,
		GenerationID:        "aaaaaaaaaaaaaaaa",
		PhysicalVersion:     99,
		MinReaderGeneration: 99,
		MinWriterGeneration: 99,
		Provenance:          sidecarProvenanceMigrated,
	}
	data, err := json.MarshalIndent(future, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath(root), data, artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatalf("a sidecar claiming a future generation must not refuse the open: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if !reader.OpenDiagnostics().SidecarStale {
		t.Fatal("the disagreement was not reported")
	}
}

func TestBackupFilenameNamesTheMigratedFileAndGeneration(t *testing.T) {
	// The stage's real name, from the constant rather than a literal: the
	// staged file is approach.db.migrating and nothing in the tree is called
	// approach.db.stage.
	if !strings.HasPrefix(backupFilename(stageFilename, 4, time.Date(2026, 8, 16, 18, 8, 30, 0, time.UTC), "nogen"), stageFilename+"-v4-") {
		t.Fatalf("backup filename does not lead with the migrated file's name")
	}
	name := backupFilename(databaseFilename, 5, time.Date(2026, 8, 16, 18, 8, 30, 0, time.UTC), "abc123")
	if !strings.HasPrefix(name, databaseFilename+"-v5-") || !strings.HasSuffix(name, "-abc123.db") {
		t.Fatalf("backup filename = %q", name)
	}
}

func TestSidecarGenerationFallsBackToNoGen(t *testing.T) {
	root := t.TempDir()
	if got := sidecarGenerationOrNoGen(root); got != "nogen" {
		t.Fatalf("generation = %q, want nogen", got)
	}
	if err := writeSidecar(root, databaseSchemaVersion, sidecarProvenanceMigrated, "feedfacefeedface"); err != nil {
		t.Fatal(err)
	}
	if got := sidecarGenerationOrNoGen(root); got != "feedfacefeedface" {
		t.Fatalf("generation = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, sidecarFilename)); err != nil {
		t.Fatal(err)
	}
}
