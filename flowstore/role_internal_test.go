package flowstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

func schemaFiveRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	db := createParentReleaseV5Database(t, root)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func stampUserVersion(t *testing.T, root string, version int64) {
	t.Helper()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec("PRAGMA user_version = " + itoaTest(version)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func itoaTest(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// canonical resolves the symlinked temp root the store reports back.
func canonical(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func fileDigest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNonMigratorRolesRefuseAPredecessorSchemaRoot(t *testing.T) {
	for _, role := range []Role{RoleReader, RoleWriter} {
		t.Run(role.String(), func(t *testing.T) {
			root := schemaFiveRoot(t)
			_, err := NewStore(StoreOptions{Root: root, Role: role})
			if err == nil {
				t.Fatalf("expected %s to refuse a schema-5 root", role)
			}
			want := "flow database schema 5 needs migration to " + itoaTest(int64(databaseSchemaVersion)) + "; run 'approach db migrate'" +
				" (this process opened the store as " + role.String() + " and will not migrate)"
			if err.Error() != want {
				t.Fatalf("refusal = %q, want %q", err.Error(), want)
			}
			if !errors.Is(err, errRoleRefusedMigration) {
				t.Fatalf("refusal is not errRoleRefusedMigration: %v", err)
			}
			// The role refusal must never inherit the rebuild advice: telling an
			// operator to move aside a database that is merely unmigrated is the
			// same disaster the newer-build suppression avoids.
			if strings.Contains(err.Error(), "move") {
				t.Fatalf("refusal offers rebuild advice: %q", err)
			}
			// It is also not the validator's message, which names migration but
			// not the role and offers no command.
			if strings.Contains(err.Error(), "requires bootstrap migration to") {
				t.Fatalf("refusal came from validateSQLiteSchema: %q", err)
			}
		})
	}
}

func TestARefusedReaderOpenLeavesThePredecessorDatabaseUntouched(t *testing.T) {
	root := schemaFiveRoot(t)
	path := filepath.Join(root, databaseFilename)
	before := fileDigest(t, path)
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(StoreOptions{Root: root, Role: RoleReader}); err == nil {
		t.Fatal("expected a refusal")
	}
	if version, err := readUserVersionRO(path); err != nil || version != 5 {
		t.Fatalf("user_version = %d (err %v), want 5", version, err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if afterInfo.Mode() != beforeInfo.Mode() {
		t.Fatalf("file mode changed from %v to %v", beforeInfo.Mode(), afterInfo.Mode())
	}
	if string(fileDigest(t, path)) != string(before) {
		t.Fatal("refused reader open modified the database file")
	}
}

func TestTheZeroValueRoleStillMigratesPredecessorSchemas(t *testing.T) {
	for name, seed := range map[string]func(*testing.T, string){
		"v4": func(t *testing.T, root string) {
			db := createParentReleaseV4Database(t, root)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		},
		"v5": func(t *testing.T, root string) {
			db := createParentReleaseV5Database(t, root)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			seed(t, root)
			store, err := NewStore(StoreOptions{Root: root})
			if err != nil {
				t.Fatalf("bare NewStore must keep migrating: %v", err)
			}
			defer func() { _ = store.Close() }()
			if version, err := readUserVersionRO(filepath.Join(root, databaseFilename)); err != nil || version != databaseSchemaVersion {
				t.Fatalf("user_version = %d (err %v), want %d", version, err, databaseSchemaVersion)
			}
		})
	}
}

func TestEveryRoleRefusesADatabaseFromANewerBuild(t *testing.T) {
	for _, role := range []Role{RoleReader, RoleWriter, RoleMigrator} {
		t.Run(role.String(), func(t *testing.T) {
			root := t.TempDir()
			stampUserVersion(t, root, 99)
			_, err := NewStore(StoreOptions{Root: root, Role: role})
			if err == nil {
				t.Fatalf("expected %s to refuse a schema-99 root", role)
			}
			if !errors.Is(err, errDatabaseFromNewerBuild) {
				t.Fatalf("refusal is not errDatabaseFromNewerBuild: %v", err)
			}
			for _, fragment := range []string{
				"flow database was written by a newer version of approach",
				"database schema 99 needs an approach build supporting flow database schema 99 or newer",
				"upgrade approach",
			} {
				if !strings.Contains(err.Error(), fragment) {
					t.Fatalf("refusal %q is missing %q", err, fragment)
				}
			}
			if strings.Contains(err.Error(), "move") {
				t.Fatalf("refusal offers rebuild advice: %q", err)
			}
		})
	}
}

func TestTheCompatibilityRefusalNamesTheLaunchingBinary(t *testing.T) {
	t.Setenv("APPROACH_EXECUTABLE", "/pinned/bin/approach")
	t.Setenv("APPROACH_BUILD_VERSION", "v0.13.0")
	root := t.TempDir()
	stampUserVersion(t, root, 99)
	_, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "but /pinned/bin/approach is build v0.13.0 (schema "+itoaTest(int64(databaseSchemaVersion))+")") {
		t.Fatalf("refusal %q does not name the launching binary", err)
	}
}

func TestAReaderRefusesAPredecessorRootInAReadOnlyDirectory(t *testing.T) {
	root := schemaFiveRoot(t)
	// The bootstrap flock runs before the version read and opens O_CREAT, so a
	// 0500 root without a pre-existing lock file fails at the lock instead. That
	// is the documented read-only-mount behaviour, not a defect; Inspect is the
	// lock-free path. Pre-create the lock file so this test reaches the gate.
	lock, err := os.OpenFile(filepath.Join(root, bootstrapLockFilename), os.O_CREATE|os.O_RDWR, artifacts.FilePerm)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, artifacts.DirPerm) })

	_, err = NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !errors.Is(err, errRoleRefusedMigration) {
		t.Fatalf("refusal is not the role refusal: %v", err)
	}
}

func TestAReaderReportsALooseRootModeWithoutRepairingIt(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "implicit root"
		if explicit {
			name = "explicit root"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o755); err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(StoreOptions{Root: root, Role: RoleReader, RootExplicit: explicit})
			if err != nil {
				t.Fatalf("a loose root must warn, not refuse: %v", err)
			}
			defer func() { _ = store.Close() }()
			diagnostics := store.OpenDiagnostics()
			if diagnostics.DirectoryMode != 0o755 {
				t.Fatalf("DirectoryMode = %04o, want 0755", diagnostics.DirectoryMode)
			}
			if len(diagnostics.Warnings) != 1 || !strings.Contains(diagnostics.Warnings[0], "0755") {
				t.Fatalf("warnings = %v, want one naming 0755", diagnostics.Warnings)
			}
			// The assertion that pins "readers never call SecureCanonicalRoot".
			// Without it the chmod would make the two above pass by repairing
			// the fixture.
			info, err := os.Stat(root)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("reader tightened the root to %04o", info.Mode().Perm())
			}
		})
	}
}

func TestAWriterStillTightensALooseRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != artifacts.DirPerm {
		t.Fatalf("writer left the root at %04o, want 0700", info.Mode().Perm())
	}
}

func TestAReaderReportsADeleteModeDatabase(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reverted, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	backend := reverted.backend.(*sqliteBackend)
	var mode string
	if err := backend.db.QueryRow("PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := reverted.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	diagnostics := reader.OpenDiagnostics()
	if diagnostics.JournalMode != "delete" {
		t.Fatalf("JournalMode = %q, want %q", diagnostics.JournalMode, "delete")
	}
	if len(diagnostics.Warnings) == 0 {
		t.Fatal("a DELETE-mode database must warn")
	}
}

func TestAReaderWillNotCreateAnExplicitlyNamedMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "typo")
	_, err := NewStore(StoreOptions{Root: root, Role: RoleReader, RootExplicit: true})
	if err == nil {
		t.Fatal("expected an error for a missing explicit root")
	}
	if err.Error() != "state root "+root+" does not exist" {
		t.Fatalf("error = %q", err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatal("the reader created the root it was told did not exist")
	}
}

func TestAReaderCreatesAMissingNonExplicitRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "default")
	store, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatalf("a config or default root must be created: %v", err)
	}
	defer func() { _ = store.Close() }()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != artifacts.DirPerm {
		t.Fatalf("created root mode = %04o, want 0700", info.Mode().Perm())
	}
}

func TestAReaderDoesNotDiscardAStagedDatabase(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Both files: discardObsoleteStage runs inside the state.database branch,
	// so a stage-only fixture would go to completeCutover and prove nothing.
	stagePath := filepath.Join(root, stageFilename)
	if err := os.WriteFile(stagePath, []byte("staged"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}

	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := os.Stat(stagePath); err != nil {
		t.Fatalf("the reader removed the staged database: %v", err)
	}

	writer, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := os.Stat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("a writer must still discard the obsolete stage: %v", err)
	}
}

func TestCreationStaysAvailableToEveryRole(t *testing.T) {
	for _, role := range []Role{RoleReader, RoleWriter, RoleMigrator} {
		t.Run(role.String(), func(t *testing.T) {
			root := t.TempDir()
			store, err := NewStore(StoreOptions{Root: root, Role: role})
			if err != nil {
				t.Fatalf("creation must stay open to %s: %v", role, err)
			}
			defer func() { _ = store.Close() }()
			if _, err := store.List(FlowFilter{}); err != nil {
				t.Fatalf("read after creation: %v", err)
			}
			path := filepath.Join(root, databaseFilename)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// The create path always opens writer-privileged first, so the
			// database is WAL and 0600 whichever role asked for it.
			if info.Mode().Perm() != artifacts.FilePerm {
				t.Fatalf("created database mode = %04o, want %04o", info.Mode().Perm(), artifacts.FilePerm)
			}
			backend := store.backend.(*sqliteBackend)
			var journalMode string
			if err := backend.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
				t.Fatal(err)
			}
			if !strings.EqualFold(journalMode, "wal") {
				t.Fatalf("created database journal mode = %q, want wal", journalMode)
			}
			// ...and the handle the Store returns is still read-only for a
			// reader, so neither half of the two-open sequence can be dropped.
			if role == RoleReader {
				if err := store.write(FlowRecord{SchemaVersion: schemaVersion, FlowID: "created", Title: "t", RepoPath: root}); !errors.Is(err, errReaderWrite) {
					t.Fatalf("reader handle accepted a write: %v", err)
				}
			}
		})
	}
}

func TestNonMigratorRolesRefuseALegacyCorpus(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "flows"), artifacts.DirPerm); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	want := "legacy flow corpus at " + filepath.Join(canonical(t, root), "flows") +
		" must be imported by 'approach db migrate' (this process opened the store as writer and will not migrate)"
	if err.Error() != want {
		t.Fatalf("refusal = %q, want %q", err, want)
	}
	if !errors.Is(err, errRoleRefusedMigration) {
		t.Fatalf("refusal is not errRoleRefusedMigration: %v", err)
	}
}

func TestNonMigratorRolesRefuseAnInterruptedCutover(t *testing.T) {
	root := t.TempDir()
	stagePath := filepath.Join(root, stageFilename)
	if err := os.WriteFile(stagePath, []byte("staged"), artifacts.FilePerm); err != nil {
		t.Fatal(err)
	}
	_, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	want := "interrupted flow database cutover at " + filepath.Join(canonical(t, root), stageFilename) +
		" must be resumed by 'approach db migrate' (this process opened the store as writer and will not migrate)"
	if err.Error() != want {
		t.Fatalf("refusal = %q, want %q", err, want)
	}
}

// The cutover path used to invert the steady-state order: refuseDevLiveCreation
// ran before the role gate, so a writer or reader against a release-owned root
// that still had flows/ or a stage got the acknowledgeable development-live
// refusal. Setting the variable and retrying still hit the role refusal.
func TestNonMigratorRolesRefuseCutoverAheadOfDevLiveOnTheReleaseRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
		want  func(string, Role) string
	}{
		{
			name: "legacy corpus",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "flows"), artifacts.DirPerm); err != nil {
					t.Fatal(err)
				}
			},
			want: func(root string, role Role) string {
				return "legacy flow corpus at " + filepath.Join(root, "flows") +
					" must be imported by 'approach db migrate' (this process opened the store as " +
					role.String() + " and will not migrate)"
			},
		},
		{
			name: "interrupted cutover",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, stageFilename), []byte("staged"), artifacts.FilePerm); err != nil {
					t.Fatal(err)
				}
			},
			want: func(root string, role Role) string {
				return "interrupted flow database cutover at " + filepath.Join(root, stageFilename) +
					" must be resumed by 'approach db migrate' (this process opened the store as " +
					role.String() + " and will not migrate)"
			},
		},
	} {
		for _, role := range []Role{RoleReader, RoleWriter} {
			t.Run(tc.name+"/"+role.String(), func(t *testing.T) {
				stateHome := t.TempDir()
				t.Setenv("XDG_STATE_HOME", stateHome)
				root := filepath.Join(stateHome, "approach", "sessions", "v1")
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				tc.setup(t, root)
				stubDevelopmentBuild(t, true)
				_, err := NewStore(StoreOptions{Root: root, Role: role})
				if err == nil {
					t.Fatal("expected a refusal")
				}
				if errors.Is(err, errDevLiveMigrationRefused) {
					t.Fatalf("got the acknowledgeable development-live refusal: %v", err)
				}
				if !errors.Is(err, errRoleRefusedMigration) {
					t.Fatalf("refusal is not errRoleRefusedMigration: %v", err)
				}
				want := tc.want(canonical(t, root), role)
				if err.Error() != want {
					t.Fatalf("refusal = %q, want %q", err.Error(), want)
				}
				if strings.Contains(err.Error(), "APPROACH_ALLOW_DEV_LIVE_MIGRATION") {
					t.Fatalf("role refusal named the acknowledgement a non-migrator cannot use: %v", err)
				}
			})
		}
	}
}

func TestAReaderStoreRefusesWritesInGo(t *testing.T) {
	root := t.TempDir()
	seed, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	created, err := seed.Create(FlowRecord{
		SchemaVersion: schemaVersion, Title: "Seed", Instructions: "Seed.", RepoPath: filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, databaseFilename)
	before := fileDigest(t, path)
	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	writes := map[string]func() error{
		"SetPhase": func() error {
			_, err := reader.SetPhase(PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: PhaseRunning})
			return err
		},
		"Create": func() error {
			_, err := reader.Create(FlowRecord{
				SchemaVersion: schemaVersion, Title: "Nope", Instructions: "Nope.", RepoPath: filepath.Join(root, "repo"),
			})
			return err
		},
		"Delete": func() error { return reader.Delete(created.FlowID) },
		"EnableEpicProgressionForPreparedFlow": func() error {
			_, _, err := reader.EnableEpicProgressionForPreparedFlow(PreparedEpicProgressionUpdate{
				FlowID: created.FlowID,
				Key:    EpicProgressionKey{RepoPath: filepath.Join(root, "repo"), EpicID: "epic"},
				Bead:   BeadLink{ID: "epic.1", EpicID: "epic"},
			})
			return err
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			err := write()
			if !errors.Is(err, errReaderWrite) {
				t.Fatalf("%s error = %v, want the reader write guard", name, err)
			}
			if strings.Contains(err.Error(), "readonly database") {
				t.Fatalf("%s surfaced the raw SQLite error: %v", name, err)
			}
		})
	}
	if string(fileDigest(t, path)) != string(before) {
		t.Fatal("a refused write changed the database file")
	}
}

func TestAReaderStoreStillServesReads(t *testing.T) {
	root := t.TempDir()
	seed, err := NewStore(StoreOptions{Root: root, Role: RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	created, err := seed.Create(FlowRecord{
		SchemaVersion: schemaVersion, Title: "Seed", Instructions: "Seed.", RepoPath: filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := reader.Read(created.FlowID); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := reader.List(FlowFilter{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	// The `approach serve` regression guard: a decorator around the backend
	// interface would have broken this type assertion.
	if _, _, err := reader.ReadEpicProgression(EpicProgressionKey{
		RepoPath: filepath.Join(root, "repo"), EpicID: "epic",
	}); err != nil {
		t.Fatalf("ReadEpicProgression: %v", err)
	}
}

func TestReaderDiagnosticsAreCleanOnASecuredRoot(t *testing.T) {
	root := t.TempDir()
	writer, err := NewStore(StoreOptions{Root: root, Role: RoleWriter, LockTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewStore(StoreOptions{Root: root, Role: RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	diagnostics := reader.OpenDiagnostics()
	if diagnostics.DirectoryMode != artifacts.DirPerm {
		t.Fatalf("DirectoryMode = %04o, want 0700", diagnostics.DirectoryMode)
	}
	if diagnostics.JournalMode != "wal" {
		t.Fatalf("JournalMode = %q, want wal", diagnostics.JournalMode)
	}
	if len(diagnostics.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", diagnostics.Warnings)
	}
}

// The version probe can fail for reasons that have nothing to do with the
// database's shape — another process holding it past the probe's busy timeout
// is the obvious one. Falling through to the migration helper on that error
// would let a `flow list`, `serve`, or session hook that merely lost a race
// migrate a predecessor database, which is the mixed-build incident this unit
// exists to prevent. The helper has no role check of its own, so the refusal
// has to hold here.
func TestANonMigratorNeverMigratesWhenTheVersionProbeFails(t *testing.T) {
	for _, role := range []Role{RoleReader, RoleWriter} {
		t.Run(role.String(), func(t *testing.T) {
			root := schemaFiveRoot(t)
			databasePath := filepath.Join(root, databaseFilename)

			// Stand in for a probe that could not answer. The role gate treats an
			// unreadable version as "nothing proved", and the question is what it
			// does next.
			original := readUserVersion
			readUserVersion = func(string) (int64, error) {
				return 0, errors.New("database is locked")
			}
			t.Cleanup(func() { readUserVersion = original })

			if _, err := NewStore(StoreOptions{Root: root, Role: role}); err == nil {
				t.Fatalf("%s opened a predecessor database whose version could not be read", role)
			}
			if version, err := readUserVersionRO(databasePath); err != nil || version != 5 {
				t.Fatalf("%s advanced user_version to %d (%v), want it left at 5", role, version, err)
			}
			if names := backupNames(t, filepath.Join(root, backupDirName)); len(names) != 0 {
				t.Fatalf("%s entered the migration helper and wrote backups: %v", role, names)
			}
		})
	}
}
