package flowstore

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDevelopmentBuild forces the build classification the guard keys on.
// Overriding it beats relying on the ambient answer: under `go test` the
// version ldflags are unset and every test binary already looks like a
// development build, so a test that wanted the release branch could not
// otherwise reach it.
func stubDevelopmentBuild(t *testing.T, development bool) {
	t.Helper()
	original := isDevelopmentBuild
	t.Cleanup(func() { isDevelopmentBuild = original })
	isDevelopmentBuild = func() bool { return development }
}

// releaseRootFixture stages a predecessor-schema database at the exact path a
// *released* build would default to, so the guard's path-equality condition is
// exercised rather than approximated.
func releaseRootFixture(t *testing.T) string {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create release root: %v", err)
	}
	db := createParentReleaseV4Database(t, root)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func storedSchemaVersion(t *testing.T, root string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(root, databaseFilename), map[string][]string{"mode": {"ro"}}))
	if err != nil {
		t.Fatalf("open stored database: %v", err)
	}
	defer db.Close()
	var version int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return version
}

func TestDevBuildRefusesToMigrateTheReleaseDefaultRoot(t *testing.T) {
	stubDevelopmentBuild(t, true)
	root := releaseRootFixture(t)

	_, err := NewStore(StoreOptions{Root: root})
	if err == nil {
		t.Fatal("a development build migrated the release-owned root without acknowledgement")
	}
	for _, want := range []string{"--allow-dev-live-migration", "development build", root} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	if got := storedSchemaVersion(t, root); got != 4 {
		t.Fatalf("refused migration still moved user_version to %d", got)
	}
}

func TestDevBuildMigratesTheReleaseDefaultRootWithAcknowledgement(t *testing.T) {
	stubDevelopmentBuild(t, true)
	root := releaseRootFixture(t)

	store, err := NewStore(StoreOptions{Root: root, AllowDevLiveMigration: true})
	if err != nil {
		t.Fatalf("acknowledged migration was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
	}
}

func TestDevBuildMigratesItsOwnDefaultRoot(t *testing.T) {
	stubDevelopmentBuild(t, true)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach-dev", "sessions", "v1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create dev root: %v", err)
	}
	db := createParentReleaseV4Database(t, root)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("development build refused its own root: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
	}
}

// The guard's condition is exact path equality and nothing broader: what it
// protects is the state a *released* build owns by default, which is one path.
// This is the residual gap, asserted deliberately rather than discovered later —
// an explicit shared root gets no protection, and isolating the development
// default is what keeps that exposure to configurations an operator chose.
func TestDevBuildMigratesAnArbitraryRoot(t *testing.T) {
	stubDevelopmentBuild(t, true)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	db := createParentReleaseV4Database(t, root)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("development build refused an arbitrary root: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
	}
}

// The cutover-resume path publishes its stage as approach.db in the same root,
// so the guard applies there too. It must REFUSE, not fall through to either
// branch that handles a genuinely damaged stage: the stage is intact, and the
// operator was just told how to satisfy the check.
//
// Both fixtures matter, because the two failure shapes differ: with no tombstone
// an unguarded fall-through discards the stage and rebuilds, and with a
// tombstone it instructs the operator to REMOVE the stage, which is the one path
// where the stage can be the only copy left. Since refuseDevLiveCreation now runs
// ahead of the stage branch, neither is reachable while that guard stands; the
// errDevLiveMigrationRefused sentinel inside the branch is kept as the fence that
// makes reordering safe, and this test is what would catch its removal together.
func TestDevBuildRefusesAnInterruptedCutoverAtTheReleaseDefaultRootWithoutDiscardingIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tombstone bool
	}{
		{name: "legacy source absent"},
		{name: "flows.legacy tombstone is the only other copy", tombstone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)
			root := filepath.Join(stateHome, "approach", "sessions", "v1")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatalf("create release root: %v", err)
			}
			if tc.tombstone {
				if err := os.MkdirAll(filepath.Join(root, "flows.legacy"), 0o700); err != nil {
					t.Fatalf("create tombstone: %v", err)
				}
			}
			stagePath := filepath.Join(root, stageFilename)
			db := createParentReleaseV4DatabaseAt(t, stagePath)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(stagePath)
			if err != nil {
				t.Fatalf("read stage: %v", err)
			}

			stubDevelopmentBuild(t, true)
			_, err = NewStore(StoreOptions{Root: root})
			if err == nil {
				t.Fatal("a development build promoted an interrupted cutover at the release-owned root")
			}
			if !strings.Contains(err.Error(), "--allow-dev-live-migration") {
				t.Fatalf("refusal %q does not name the acknowledgement", err)
			}
			// A policy refusal must never hand the operator a recipe that
			// destroys the very stage it just declined to promote.
			if strings.Contains(err.Error(), "remove") {
				t.Fatalf("refusal tells the operator to remove the intact stage: %v", err)
			}

			after, err := os.ReadFile(stagePath)
			if err != nil {
				t.Fatalf("the refused stage was discarded: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("the refused stage was rewritten")
			}
			if _, err := os.Lstat(filepath.Join(root, databaseFilename)); !os.IsNotExist(err) {
				t.Fatalf("stat approach.db = %v, want no database published", err)
			}

			// With the acknowledgement the same root promotes normally, so the
			// refusal is a gate the operator can pass and not a dead end.
			store, err := NewStore(StoreOptions{Root: root, AllowDevLiveMigration: true})
			if err != nil {
				t.Fatalf("acknowledged cutover resume was refused: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
				t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
			}
		})
	}
}

// Creating reaches the same end state as migrating — a database a released build
// cannot open — so the fresh-bootstrap and legacy-import path is guarded too.
// refuseDevLiveMigration cannot cover it: there is no stored version to compare.
func TestDevBuildRefusesToCreateADatabaseAtTheReleaseDefaultRoot(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(filepath.Join(root, "flows"), 0o700); err != nil {
		t.Fatalf("create release root: %v", err)
	}

	stubDevelopmentBuild(t, true)
	if _, err := NewStore(StoreOptions{Root: root}); err == nil {
		t.Fatal("a development build created a database at the release-owned root")
	} else if !strings.Contains(err.Error(), "--allow-dev-live-migration") {
		t.Fatalf("refusal %q does not name the acknowledgement", err)
	}
	// A refusal must not consume the legacy source or leave a partial database.
	if _, err := os.Stat(filepath.Join(root, databaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("stat approach.db after a refusal = %v, want none", err)
	}
	if _, err := os.Stat(filepath.Join(root, "flows")); err != nil {
		t.Fatalf("the legacy source was disturbed by a refusal: %v", err)
	}

	store, err := NewStore(StoreOptions{Root: root, AllowDevLiveMigration: true})
	if err != nil {
		t.Fatalf("acknowledged creation was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

// A release build is what the release root is for, and a development build on
// its own root is nobody else's business. Neither may be refused, or the guard
// has stopped being about drift and started being about mere inconvenience.
func TestFreshCreationIsAllowedWhereverItIsNotReleaseOwned(t *testing.T) {
	t.Run("release build at the release root", func(t *testing.T) {
		stateHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateHome)
		root := filepath.Join(stateHome, "approach", "sessions", "v1")
		stubDevelopmentBuild(t, false)
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("release build was refused its own root: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
	})
	t.Run("development build at the development root", func(t *testing.T) {
		stateHome := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateHome)
		root := filepath.Join(stateHome, "approach-dev", "sessions", "v1")
		stubDevelopmentBuild(t, true)
		store, err := NewStore(StoreOptions{Root: root})
		if err != nil {
			t.Fatalf("development build was refused its own root: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
	})
	t.Run("development build at an arbitrary root", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		stubDevelopmentBuild(t, true)
		store, err := NewStore(StoreOptions{Root: t.TempDir()})
		if err != nil {
			t.Fatalf("development build was refused an arbitrary root: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
	})
}

func TestReleaseBuildMigratesTheReleaseDefaultRoot(t *testing.T) {
	stubDevelopmentBuild(t, false)
	root := releaseRootFixture(t)

	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("release build was refused its own root: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
	}
}

// Reading an already-current release database from a development build is not a
// migration and stays allowed: the guard exists to stop a schema advance, not to
// fence development builds out of release state.
func TestDevBuildOpensACurrentReleaseDefaultRoot(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create release root: %v", err)
	}

	stubDevelopmentBuild(t, false)
	current, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("seed current release database: %v", err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}

	stubDevelopmentBuild(t, true)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("development build refused an already-current release root: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

// A stage already at THIS build's schema is exactly what a crash right after
// buildStagedDatabase leaves behind, and migrateAuthoritativeDatabase returns
// successfully for it before refuseDevLiveMigration ever runs — there is nothing
// to advance. Promotion is still the publication of a current-schema database at
// the release-owned root, which is the end state the fresh-creation path already
// refuses, so the guard has to sit ahead of the stage branch rather than inside
// the migration it may not perform.
func TestDevBuildRefusesToPromoteACurrentSchemaStageAtTheReleaseDefaultRoot(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	root := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create release root: %v", err)
	}
	stagePath := filepath.Join(root, stageFilename)
	if err := buildStagedDatabase(stagePath, nil); err != nil {
		t.Fatalf("build current-schema stage: %v", err)
	}

	stubDevelopmentBuild(t, true)
	if _, err := NewStore(StoreOptions{Root: root}); err == nil {
		t.Fatal("a development build promoted a current-schema stage at the release-owned root")
	} else if !strings.Contains(err.Error(), "--allow-dev-live-migration") {
		t.Fatalf("refusal %q does not name the acknowledgement", err)
	}
	if _, err := os.Lstat(filepath.Join(root, databaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("stat approach.db = %v, want no database published", err)
	}
	if _, err := os.Lstat(stagePath); err != nil {
		t.Fatalf("the refused stage was discarded: %v", err)
	}

	// The refusal is a gate the operator can pass, not a dead end.
	store, err := NewStore(StoreOptions{Root: root, AllowDevLiveMigration: true})
	if err != nil {
		t.Fatalf("acknowledged stage promotion was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := storedSchemaVersion(t, root); got != int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d, want %d", got, databaseSchemaVersion)
	}
}

// EvalSymlinks preserves the caller's spelling of every path component, so on
// the case-insensitive filesystem macOS ships by default a --state-root typed
// with different case names the same directory as the release default while
// comparing unequal as a string. The guard protects exactly one path, so a
// spelling that walks past it disarms it completely.
func TestDevBuildRefusesACaseVariantSpellingOfTheReleaseDefaultRoot(t *testing.T) {
	root := releaseRootFixture(t)
	variant := filepath.Join(filepath.Dir(filepath.Dir(root)), "SESSIONS", filepath.Base(root))
	// Checked before NewStore, which would otherwise CREATE this directory on a
	// case-sensitive filesystem and quietly test a different root than intended.
	if _, err := os.Stat(variant); err != nil {
		t.Skipf("case-sensitive filesystem: %q is a different directory (%v)", variant, err)
	}

	stubDevelopmentBuild(t, true)
	if _, err := NewStore(StoreOptions{Root: variant}); err == nil {
		t.Fatal("a development build migrated the release-owned root through a case-variant spelling")
	} else if !strings.Contains(err.Error(), "--allow-dev-live-migration") {
		t.Fatalf("refusal %q does not name the acknowledgement", err)
	}
	if got := storedSchemaVersion(t, root); got == int64(databaseSchemaVersion) {
		t.Fatalf("user_version = %d: the release database was migrated anyway", got)
	}
}
