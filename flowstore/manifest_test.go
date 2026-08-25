package flowstore

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// TestManifestCoversCurrentSchemaVersion is the gate that fires on the next
// databaseSchemaVersion bump. A bump with no manifest entry is exactly the
// incident this unit exists to prevent, so it fails here rather than in the
// field.
func TestManifestCoversCurrentSchemaVersion(t *testing.T) {
	entry, ok := manifestEntry(databaseSchemaVersion)
	if !ok {
		t.Fatalf("flowstore/schema_manifest.json has no entry for schema %d;"+
			" add one (see docs/release.md's schema-bump checklist)", databaseSchemaVersion)
	}
	if entry.FirstCompatibleRelease == "" {
		t.Errorf("schema %d declares no first_compatible_release", databaseSchemaVersion)
	}
	if entry.ReleaseNotes == "" {
		t.Errorf("schema %d declares no release_notes", databaseSchemaVersion)
	}
}

// TestManifestPredecessorsMatchMigrator pins the declaration to the code. A
// predecessor accepted by migrateAuthoritativeDatabase but undeclared is an
// untested migration path shipped silently.
func TestManifestPredecessorsMatchMigrator(t *testing.T) {
	entry, ok := manifestEntry(databaseSchemaVersion)
	if !ok {
		t.Fatalf("no manifest entry for schema %d", databaseSchemaVersion)
	}
	declared := slices.Clone(entry.MigrationTestedPredecessors)
	accepted := slices.Clone(supportedPredecessorVersions)
	slices.Sort(declared)
	slices.Sort(accepted)
	if !slices.Equal(declared, accepted) {
		t.Fatalf("manifest declares predecessors %v for schema %d, the migrator accepts %v",
			declared, databaseSchemaVersion, accepted)
	}
}

func TestManifestStructuralInvariants(t *testing.T) {
	entries := manifestEntries()
	if len(entries) == 0 {
		t.Fatal("manifest declares no schemas")
	}
	seen := map[int64]bool{}
	for i, entry := range entries {
		if seen[entry.PhysicalVersion] {
			t.Errorf("duplicate physical_version %d", entry.PhysicalVersion)
		}
		seen[entry.PhysicalVersion] = true
		if i > 0 && entry.PhysicalVersion <= entries[i-1].PhysicalVersion {
			t.Errorf("physical_version %d is not above its predecessor %d; entries must ascend",
				entry.PhysicalVersion, entries[i-1].PhysicalVersion)
		}
		if entry.PhysicalVersion > databaseSchemaVersion {
			t.Errorf("manifest declares schema %d, above this build's %d",
				entry.PhysicalVersion, databaseSchemaVersion)
		}
		if entry.MinReaderGeneration > entry.PhysicalVersion {
			t.Errorf("schema %d declares min_reader_generation %d above itself",
				entry.PhysicalVersion, entry.MinReaderGeneration)
		}
		if entry.MinWriterGeneration > entry.PhysicalVersion {
			t.Errorf("schema %d declares min_writer_generation %d above itself",
				entry.PhysicalVersion, entry.MinWriterGeneration)
		}
		for _, predecessor := range entry.MigrationTestedPredecessors {
			if predecessor >= entry.PhysicalVersion {
				t.Errorf("schema %d declares predecessor %d, which is not below it",
					entry.PhysicalVersion, predecessor)
			}
		}
	}
}

// TestManifestDeclaredPredecessorsMigrate is the executing half of the gate:
// every predecessor the manifest declares for the current schema is actually
// migrated here, through the real NewStore path. A declared predecessor with no
// fixture fails rather than being taken on the manifest's word.
func TestManifestDeclaredPredecessorsMigrate(t *testing.T) {
	entry, ok := manifestEntry(databaseSchemaVersion)
	if !ok {
		t.Fatalf("no manifest entry for schema %d", databaseSchemaVersion)
	}
	for _, predecessor := range entry.MigrationTestedPredecessors {
		t.Run(fmt.Sprintf("v%d", predecessor), func(t *testing.T) {
			root := t.TempDir()
			db := createPredecessorDatabase(t, root, predecessor)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
			if err != nil {
				t.Fatalf("migrating a v%d database: %v", predecessor, err)
			}
			defer store.Close()
			backend := store.backend.(*sqliteBackend)
			var got int64
			if err := backend.db.QueryRow("PRAGMA user_version").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != databaseSchemaVersion {
				t.Fatalf("user_version after migrating v%d = %d, want %d",
					predecessor, got, databaseSchemaVersion)
			}
			if err := validateSQLiteSchema(backend.db); err != nil {
				t.Fatalf("migrated v%d database is not the current shape: %v", predecessor, err)
			}
		})
	}
}

// createPredecessorDatabase builds a database in the exact published shape of
// one predecessor generation. It is keyed by version so the manifest's
// predecessor list drives which fixtures must exist.
func createPredecessorDatabase(t *testing.T, root string, version int64) *sql.DB {
	t.Helper()
	path := filepath.Join(root, databaseFilename)
	switch version {
	case 4:
		return createParentReleaseV4DatabaseAt(t, path)
	case 5:
		return createParentReleaseV5DatabaseAt(t, path)
	case 6:
		return createParentReleaseV6DatabaseAt(t, path)
	case 7:
		return createParentReleaseV7DatabaseAt(t, path)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rwc"}}))
	if err != nil {
		t.Fatal(err)
	}
	indexes := `
CREATE INDEX idx_flows_updated ON flows(updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_repo_updated ON flows(repo_path, updated_at DESC, flow_id ASC);
CREATE INDEX idx_flows_status_updated ON flows(status, updated_at DESC, flow_id ASC);
`
	var schema string
	switch version {
	case 0, 1:
		schema = flowTableSchemaV1 + `;` + indexes
	case 2:
		schema = flowTableSchemaV2 + `;` + indexes + flowBeadCompatibilityTrigger + `;`
	case 3:
		schema = flowTableSchemaV3 + `;` + indexes +
			epicProgressionTableSchema + `;` +
			flowBeadCompatibilityTrigger + `;` +
			flowPreparedCompatibilityTrigger + `;`
	default:
		_ = db.Close()
		t.Fatalf("manifest declares predecessor v%d but no fixture builds that shape;"+
			" add one here alongside the manifest entry", version)
	}
	if _, err := db.Exec(schema + fmt.Sprintf("PRAGMA user_version = %d;", version)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return db
}

// TestSidecarGenerationsComeFromTheManifest proves the sidecar's compatibility
// floors are declared rather than assumed. They are equal to the physical
// version for every schema shipped so far, so only the manifest lookup itself
// distinguishes the two implementations.
func TestSidecarGenerationsComeFromTheManifest(t *testing.T) {
	root := t.TempDir()
	if err := writeSidecar(root, databaseSchemaVersion, sidecarProvenanceMigrated, newGenerationID(), migrationOutcome{}); err != nil {
		t.Fatal(err)
	}
	sidecar, ok := readSidecar(root)
	if !ok {
		t.Fatal("sidecar written but not readable")
	}
	entry, ok := manifestEntry(databaseSchemaVersion)
	if !ok {
		t.Fatalf("no manifest entry for schema %d", databaseSchemaVersion)
	}
	if sidecar.MinReaderGeneration != entry.MinReaderGeneration {
		t.Errorf("sidecar min_reader_generation = %d, manifest declares %d",
			sidecar.MinReaderGeneration, entry.MinReaderGeneration)
	}
	if sidecar.MinWriterGeneration != entry.MinWriterGeneration {
		t.Errorf("sidecar min_writer_generation = %d, manifest declares %d",
			sidecar.MinWriterGeneration, entry.MinWriterGeneration)
	}
}

// TestInspectReportsFirstCompatibleRelease covers the only actionable half of
// the manifest: an operator can act on a release string, not on a generation
// number.
func TestInspectReportsFirstCompatibleRelease(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.FirstCompatibleRelease == nil {
		t.Fatal("inspect reported no first_compatible_release for a current-schema database")
	}
	entry, _ := manifestEntry(databaseSchemaVersion)
	if *report.FirstCompatibleRelease != entry.FirstCompatibleRelease {
		t.Fatalf("first_compatible_release = %q, manifest declares %q",
			*report.FirstCompatibleRelease, entry.FirstCompatibleRelease)
	}
}
