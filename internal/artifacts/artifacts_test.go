package artifacts_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/version"
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

func TestAcquireFileLockSerializesIndependentProcesses(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "cross-process.lock")
	release, err := artifacts.AcquireFileLock(lockPath, "cross-process store lock", time.Second)
	if err != nil {
		t.Fatalf("AcquireFileLock(parent) error = %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	runFileLockHelper(t, lockPath, "timeout")
	release()
	released = true
	runFileLockHelper(t, lockPath, "acquire")
}

func TestAcquireFileLockHelperProcess(t *testing.T) {
	if os.Getenv("APPROACH_TEST_FILE_LOCK_HELPER") != "1" {
		t.Skip("helper process only")
	}
	path := os.Getenv("APPROACH_TEST_FILE_LOCK_PATH")
	switch os.Getenv("APPROACH_TEST_FILE_LOCK_MODE") {
	case "timeout":
		_, err := artifacts.AcquireFileLock(path, "cross-process store lock", 20*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "cross-process store lock") {
			t.Fatalf("AcquireFileLock(contended helper) error = %v, want descriptive timeout", err)
		}
	case "acquire":
		release, err := artifacts.AcquireFileLock(path, "cross-process store lock", time.Second)
		if err != nil {
			t.Fatalf("AcquireFileLock(released helper) error = %v", err)
		}
		release()
		release()
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv("APPROACH_TEST_FILE_LOCK_MODE"))
	}
}

func runFileLockHelper(t *testing.T, path, mode string) {
	t.Helper()
	const helperGuard = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), helperGuard)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAcquireFileLockHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"APPROACH_TEST_FILE_LOCK_HELPER=1",
		"APPROACH_TEST_FILE_LOCK_PATH="+path,
		"APPROACH_TEST_FILE_LOCK_MODE="+mode,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("file lock helper (%s) exceeded %s guard: %v\n%s", mode, helperGuard, ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("file lock helper (%s) error = %v\n%s", mode, err, output)
	}
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

func TestDefaultRootIsolatesDevelopmentBuilds(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	// Under `go test` the ldflags vars are unset and debug.ReadBuildInfo reports
	// "(devel)", so this binary is always a development build.
	if !version.IsDevelopment() {
		t.Fatal("expected the test binary to look like a development build")
	}

	got, err := artifacts.DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot() error = %v", err)
	}
	want := filepath.Join(stateHome, "approach-dev", "sessions", "v1")
	if got != want {
		t.Fatalf("DefaultRoot() = %q, want %q", got, want)
	}

	release, err := artifacts.ReleaseDefaultRoot()
	if err != nil {
		t.Fatalf("ReleaseDefaultRoot() error = %v", err)
	}
	wantRelease := filepath.Join(stateHome, "approach", "sessions", "v1")
	if release != wantRelease {
		t.Fatalf("ReleaseDefaultRoot() = %q, want %q", release, wantRelease)
	}
	if got == release {
		t.Fatal("a development build must not default to the release-owned root")
	}
}

func TestReleaseDefaultRootFallsBackToHomeWithoutXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	release, err := artifacts.ReleaseDefaultRoot()
	if err != nil {
		t.Fatalf("ReleaseDefaultRoot() error = %v", err)
	}
	want := filepath.Join(home, ".local", "state", "approach", "sessions", "v1")
	if release != want {
		t.Fatalf("ReleaseDefaultRoot() = %q, want %q", release, want)
	}
}

// ResolveCanonicalRoot is the read-only half of SecureCanonicalRoot. The pair of
// assertions that matter are that it resolves symlinks like its sibling and that
// it leaves a loose directory exactly as it found it — a reader that repaired
// the mode would erase the state a diagnostic exists to report.
func TestResolveCanonicalRootReportsWithoutRepairing(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	canonical, err := artifacts.ResolveCanonicalRoot(link, "flow store root")
	if err != nil {
		t.Fatalf("ResolveCanonicalRoot() error = %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if canonical != resolvedTarget {
		t.Fatalf("canonical = %q, want %q", canonical, resolvedTarget)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %04o, want 0755 (ResolveCanonicalRoot must not chmod)", info.Mode().Perm())
	}
}

func TestResolveCanonicalRootDoesNotCreateAMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	if _, err := artifacts.ResolveCanonicalRoot(root, "flow store root"); err == nil {
		t.Fatal("ResolveCanonicalRoot() on a missing root returned no error")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("ResolveCanonicalRoot created the root: %v", err)
	}
}

func TestResolveCanonicalRootRejectsANonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := artifacts.ResolveCanonicalRoot(path, "flow store root")
	if err == nil || !strings.Contains(err.Error(), "must resolve to a directory") {
		t.Fatalf("error = %v, want a not-a-directory failure", err)
	}
}
