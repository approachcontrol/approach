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

// A predecessor stage that the resume path advances is a real migration — it
// even writes a backup — but it promotes through completeCutover rather than
// the authoritative-database branch that normally stamps provenance. Without an
// explicit reconcile it gets no sidecar at all, and a later open reads that
// absence as the legitimate never-migrated state and leaves it forever.
func TestAResumedPredecessorStageStampsProvenance(t *testing.T) {
	root := t.TempDir()
	db := createParentReleaseV5DatabaseAt(t, filepath.Join(root, stageFilename))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("stage resume: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	sidecar, ok := readSidecar(root)
	if !ok {
		t.Fatal("a resumed predecessor stage wrote no provenance sidecar")
	}
	if sidecar.Provenance != sidecarProvenanceMigrated {
		t.Fatalf("provenance = %q, want %q", sidecar.Provenance, sidecarProvenanceMigrated)
	}
	if sidecar.PhysicalVersion != databaseSchemaVersion {
		t.Fatalf("physical_version = %d, want %d", sidecar.PhysicalVersion, databaseSchemaVersion)
	}
	if sidecar.GenerationID == "" {
		t.Fatal("the sidecar carries no generation id to trace its backup to")
	}
}

// The other half: a stage the previous run had already built at this build's
// schema is resumed CREATION, not migration. Stamping it would claim a
// migration that never happened.
func TestAResumedCurrentStageStampsNoProvenance(t *testing.T) {
	root := t.TempDir()
	built, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := built.Close(); err != nil {
		t.Fatal(err)
	}
	// Recreate the interrupted-cutover shape: a current-schema stage and no
	// authoritative database.
	if err := os.Rename(filepath.Join(root, databaseFilename), filepath.Join(root, stageFilename)); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(filepath.Join(root, databaseFilename) + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("stage resume: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readSidecar(root); ok {
		t.Fatal("a resumed current-schema stage claimed a migration that never happened")
	}
}

// The sidecar's generation ID goes straight into a backup FILENAME, so a value
// carrying a path separator makes every later migration fail at the rename —
// reported as a backup failure suggesting the disk is full. A hand-edited or
// externally damaged sidecar must not be able to permanently block the upgrade
// of a healthy database.
func TestAnInvalidSidecarIsTreatedAsAbsentAndDoesNotBlockMigration(t *testing.T) {
	for name, body := range map[string]string{
		"separator in generation": `{"schema_version":1,"generation_id":"bad/name","physical_version":5}`,
		"non-hex generation":      `{"schema_version":1,"generation_id":"zzzzzzzzzzzzzzzz","physical_version":5}`,
		"short generation":        `{"schema_version":1,"generation_id":"abc","physical_version":5}`,
		"unknown schema version":  `{"schema_version":99,"generation_id":"feedfacefeedface","physical_version":5}`,
		"not an object":           `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			root := schemaFiveRoot(t)
			if err := os.WriteFile(sidecarPath(root), []byte(body), artifacts.FilePerm); err != nil {
				t.Fatal(err)
			}
			if _, ok := readSidecar(root); ok {
				t.Fatal("an uninterpretable sidecar was accepted as provenance")
			}

			store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
			if err != nil {
				t.Fatalf("a damaged sidecar blocked the migration: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			// The migration ran and replaced the unusable provenance with real
			// provenance, rather than preserving the bad generation id.
			sidecar, ok := readSidecar(root)
			if !ok {
				t.Fatal("the migration left no usable sidecar behind")
			}
			if sidecar.Provenance != sidecarProvenanceMigrated {
				t.Fatalf("provenance = %q, want %q", sidecar.Provenance, sidecarProvenanceMigrated)
			}
			if len(sidecar.GenerationID) != 2*generationIDBytes {
				t.Fatalf("generation_id = %q, want a freshly generated one", sidecar.GenerationID)
			}
			// And the backup it wrote is named with the nogen fallback, since the
			// unusable generation was correctly not carried into a filename.
			names := backupNames(t, filepath.Join(root, backupDirName))
			if len(names) != 1 || !strings.HasSuffix(names[0], "-nogen.db") {
				t.Fatalf("backups = %v, want one falling back to nogen", names)
			}
		})
	}
}
