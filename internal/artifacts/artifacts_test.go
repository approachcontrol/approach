package artifacts_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/internal/artifacts"
)

func TestEnsureCollectionSecuresRootAndCollection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")

	if err := artifacts.EnsureCollection(root, "plans"); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}

	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "plans"), 0o700)

	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("chmod root: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "plans"), 0o755); err != nil {
		t.Fatalf("chmod collection: %v", err)
	}
	if err := artifacts.EnsureCollection(root, "plans"); err != nil {
		t.Fatalf("EnsureCollection() second call error = %v", err)
	}
	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "plans"), 0o700)
}

func TestEnsureRecordDirSecuresRecordDirectory(t *testing.T) {
	root := t.TempDir()
	if err := artifacts.EnsureCollection(root, "flows"); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}

	dir, err := artifacts.EnsureRecordDir(root, "flows", "flow-1")
	if err != nil {
		t.Fatalf("EnsureRecordDir() error = %v", err)
	}

	want := filepath.Join(root, "flows", "flow-1")
	if dir != want {
		t.Fatalf("EnsureRecordDir() = %q, want %q", dir, want)
	}
	assertMode(t, dir, 0o700)

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod record dir: %v", err)
	}
	if _, err := artifacts.EnsureRecordDir(root, "flows", "flow-1"); err != nil {
		t.Fatalf("EnsureRecordDir() second call error = %v", err)
	}
	assertMode(t, dir, 0o700)
}

func TestWriteFileAtomicWrites0600AndReplacesContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")

	if err := artifacts.WriteFileAtomic(path, []byte("first")); err != nil {
		t.Fatalf("WriteFileAtomic(first) error = %v", err)
	}
	assertFile(t, path, "first")
	assertMode(t, path, 0o600)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	if err := artifacts.WriteFileAtomic(path, []byte("second")); err != nil {
		t.Fatalf("WriteFileAtomic(second) error = %v", err)
	}
	assertFile(t, path, "second")
	assertMode(t, path, 0o600)
}

func TestWriteFileAtomicFromReaderCleansTempOnReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raw.jsonl")

	err := artifacts.WriteFileAtomicFromReader(path, io.MultiReader(strings.NewReader("partial"), errReader{}))
	if err == nil {
		t.Fatal("WriteFileAtomicFromReader() error = nil, want reader error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after failed write, stat err = %v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if globErr != nil {
		t.Fatalf("glob temp files: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files were not cleaned up: %#v", matches)
	}
}

func TestSafeIDRejectsPathSegments(t *testing.T) {
	valid := []string{"plan-1", "20260608T014447Z-deepen", "a.b_c-2"}
	for _, id := range valid {
		if !artifacts.IsSafeID(id) {
			t.Fatalf("IsSafeID(%q) = false, want true", id)
		}
	}

	invalid := []string{"", ".", "..", "../escape", "nested/id", "-leading", strings.Repeat("a", 129)}
	for _, id := range invalid {
		if artifacts.IsSafeID(id) {
			t.Fatalf("IsSafeID(%q) = true, want false", id)
		}
	}
}

func TestAllocateTimestampedIDSlugFallbackAndCollisionSuffix(t *testing.T) {
	root := t.TempDir()
	if err := artifacts.EnsureCollection(root, "plans"); err != nil {
		t.Fatalf("EnsureCollection() error = %v", err)
	}
	now := time.Date(2026, 6, 8, 1, 44, 47, 0, time.UTC)

	first, err := artifacts.AllocateTimestampedID(artifacts.IDOptions{
		Root:         root,
		Collection:   "plans",
		Title:        "!!!",
		FallbackSlug: "plan",
		Kind:         "plan",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("AllocateTimestampedID(first) error = %v", err)
	}
	if first != "20260608T014447Z-plan" {
		t.Fatalf("first id = %q, want fallback slug", first)
	}
	if _, err := artifacts.EnsureRecordDir(root, "plans", first); err != nil {
		t.Fatalf("create first record dir: %v", err)
	}

	second, err := artifacts.AllocateTimestampedID(artifacts.IDOptions{
		Root:         root,
		Collection:   "plans",
		Title:        "!!!",
		FallbackSlug: "plan",
		Kind:         "plan",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("AllocateTimestampedID(second) error = %v", err)
	}
	if second != "20260608T014447Z-plan-2" {
		t.Fatalf("second id = %q, want collision suffix", second)
	}
}

func TestAcquireFileLockIsBoundedAndReusable(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "session.lock")
	release, err := artifacts.AcquireFileLock(lockPath, "session lock session-1", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("AcquireFileLock(first) error = %v", err)
	}
	defer release()
	assertMode(t, lockPath, 0o600)

	started := time.Now()
	_, err = artifacts.AcquireFileLock(lockPath, "session lock session-1", 15*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "session lock session-1") {
		t.Fatalf("AcquireFileLock(contended) error = %v, want descriptive timeout", err)
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond {
		t.Fatalf("contended lock returned after %v, want bounded retry wait", elapsed)
	}

	release()
	release = func() {}
	releaseAgain, err := artifacts.AcquireFileLock(lockPath, "session lock session-1", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("AcquireFileLock(after release) error = %v", err)
	}
	releaseAgain()
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, data, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
