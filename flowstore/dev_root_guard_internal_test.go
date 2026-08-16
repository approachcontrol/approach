package flowstore

import (
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

// The guard's condition is exact path equality and nothing broader. A rule
// phrased as "any root that is not the dev default" would fire in every
// t.TempDir() — and because IsDevelopment is true under `go test`, it would
// break every existing migration test in this package.
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
