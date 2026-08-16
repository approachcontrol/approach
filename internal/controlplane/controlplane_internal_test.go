package controlplane

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSchemaVersion = 6

// stubExecutable writes a fake "running binary" and points the package's
// executable resolver at it, so tests can replace or delete the source the way
// `brew upgrade` does without touching the real test binary.
func stubExecutable(t *testing.T, contents string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	path := filepath.Join(dir, "approach")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write stub executable: %v", err)
	}
	original := resolveExecutable
	t.Cleanup(func() { resolveExecutable = original })
	resolveExecutable = func() (string, error) { return path, nil }
	return path
}

func TestResolveMaterializesCachedCopy(t *testing.T) {
	source := stubExecutable(t, "binary-contents")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	pin, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pin.Degraded {
		t.Fatalf("Resolve degraded unexpectedly: %s", pin.Notice)
	}
	if pin.SourcePath != source {
		t.Fatalf("SourcePath = %q, want %q", pin.SourcePath, source)
	}
	if pin.ExecutablePath == source {
		t.Fatalf("ExecutablePath must be the cached copy, got the source %q", pin.ExecutablePath)
	}
	if filepath.Dir(pin.ExecutablePath) != filepath.Join(root, cacheDirName) {
		t.Fatalf("cached copy %q is not under %q", pin.ExecutablePath, filepath.Join(root, cacheDirName))
	}
	if !strings.HasSuffix(pin.ExecutablePath, pin.Digest[:digestNameLength]) {
		t.Fatalf("cached copy %q is not named for digest %q", pin.ExecutablePath, pin.Digest)
	}
	if pin.SchemaVersion != testSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", pin.SchemaVersion, testSchemaVersion)
	}
	data, err := os.ReadFile(pin.ExecutablePath)
	if err != nil {
		t.Fatalf("read cached copy: %v", err)
	}
	if string(data) != "binary-contents" {
		t.Fatalf("cached copy contents = %q", data)
	}
	info, err := os.Stat(pin.ExecutablePath)
	if err != nil {
		t.Fatalf("stat cached copy: %v", err)
	}
	if info.Mode().Perm() != cachedBinaryPerm {
		t.Fatalf("cached copy mode = %v, want %v", info.Mode().Perm(), cachedBinaryPerm)
	}
}

func TestResolveIsIdempotentForUnchangedSource(t *testing.T) {
	stubExecutable(t, "binary-contents")
	root := t.TempDir()

	first, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	firstInfo, err := os.Stat(first.ExecutablePath)
	if err != nil {
		t.Fatalf("stat first: %v", err)
	}
	second, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second.ExecutablePath != first.ExecutablePath {
		t.Fatalf("second Resolve produced %q, want %q", second.ExecutablePath, first.ExecutablePath)
	}
	secondInfo, err := os.Stat(second.ExecutablePath)
	if err != nil {
		t.Fatalf("stat second: %v", err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("unchanged source was recopied: mtime moved from %v to %v", firstInfo.ModTime(), secondInfo.ModTime())
	}
	entries := cachedBinaries(t, root)
	if len(entries) != 1 {
		t.Fatalf("cached binaries = %v, want exactly one", entries)
	}
}

func TestVerifyAcceptsCachedCopyAndRejectsTampering(t *testing.T) {
	stubExecutable(t, "binary-contents")
	root := t.TempDir()
	pin, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("Verify on a fresh pin: %v", err)
	}

	t.Run("replaced", func(t *testing.T) {
		replaced := pin
		if err := os.Chmod(replaced.ExecutablePath, 0o700); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if err := os.WriteFile(replaced.ExecutablePath, []byte("other-contents"), 0o500); err != nil {
			t.Fatalf("replace cached copy: %v", err)
		}
		err := replaced.Verify()
		if !errors.Is(err, ErrPinDigestMismatch) {
			t.Fatalf("Verify after replacement = %v, want ErrPinDigestMismatch", err)
		}
		if err := os.Chmod(replaced.ExecutablePath, 0o700); err != nil {
			t.Fatalf("chmod back: %v", err)
		}
		if err := os.WriteFile(replaced.ExecutablePath, []byte("binary-contents"), 0o500); err != nil {
			t.Fatalf("restore cached copy: %v", err)
		}
	})

	t.Run("not executable", func(t *testing.T) {
		if err := os.Chmod(pin.ExecutablePath, 0o400); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		defer os.Chmod(pin.ExecutablePath, cachedBinaryPerm)
		err := pin.Verify()
		if !errors.Is(err, ErrPinNotExecutable) {
			t.Fatalf("Verify on a non-executable copy = %v, want ErrPinNotExecutable", err)
		}
	})

	t.Run("removed", func(t *testing.T) {
		if err := os.Remove(pin.ExecutablePath); err != nil {
			t.Fatalf("remove cached copy: %v", err)
		}
		err := pin.Verify()
		if !errors.Is(err, ErrPinMissing) {
			t.Fatalf("Verify on a removed copy = %v, want ErrPinMissing", err)
		}
	})
}

func TestVerifySurvivesSourceDeletion(t *testing.T) {
	source := stubExecutable(t, "binary-contents")
	root := t.TempDir()
	pin, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// brew upgrade deletes the versioned Caskroom path a symlink pointed at.
	if err := os.Remove(source); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("Verify after the source was deleted: %v", err)
	}
}

func TestDegradedPinFollowsTheSource(t *testing.T) {
	source := stubExecutable(t, "binary-contents")
	pin := Pin{
		ExecutablePath: source,
		SourcePath:     source,
		Digest:         mustDigest(t, source),
		Degraded:       true,
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("Verify on a degraded pin: %v", err)
	}
	if err := os.WriteFile(source, []byte("upgraded"), 0o755); err != nil {
		t.Fatalf("replace source: %v", err)
	}
	if err := pin.Verify(); !errors.Is(err, ErrPinDigestMismatch) {
		t.Fatalf("Verify after the source was replaced = %v, want ErrPinDigestMismatch", err)
	}
}

func TestRetentionKeepsRecentAndNeverEvictsPinnedDigest(t *testing.T) {
	root := t.TempDir()
	digests := make([]string, 0, retainedBinaries+2)
	for i := range retainedBinaries + 2 {
		stubExecutable(t, "binary-contents-"+string(rune('a'+i)))
		pin, err := Resolve(root, testSchemaVersion)
		if err != nil {
			t.Fatalf("Resolve %d: %v", i, err)
		}
		digests = append(digests, pin.Digest)
		if i == 0 {
			// A detached agent still holds this launch's argv, so its digest
			// must outlive retention.
			if err := RetainPin(root, "launch-oldest", pin.Digest); err != nil {
				t.Fatalf("RetainPin: %v", err)
			}
		}
		// Cache names are content-addressed, so distinct mtimes are what order
		// retention. Sleep-free: stamp them explicitly.
		if err := os.Chtimes(pin.ExecutablePath, time.Time{}, time.Unix(int64(1_700_000_000+i), 0)); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	// One final Resolve so the sweep runs against the stamped mtimes.
	stubExecutable(t, "binary-contents-final")
	final, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("final Resolve: %v", err)
	}

	present := map[string]bool{}
	for _, name := range cachedBinaries(t, root) {
		present[name] = true
	}
	if !present[filepath.Base(final.ExecutablePath)] {
		t.Fatalf("the just-resolved binary was evicted: %v", present)
	}
	if !present[cachedBinaryName(digests[0])] {
		t.Fatalf("a pinned digest was evicted: %v", present)
	}
	if len(present) > retainedBinaries+1 {
		t.Fatalf("retention kept %d binaries, want at most %d (%d retained + 1 pinned)",
			len(present), retainedBinaries+1, retainedBinaries)
	}
	if present[cachedBinaryName(digests[1])] {
		t.Fatalf("the oldest unpinned digest survived retention: %v", present)
	}

	if err := ReleasePin(root, "launch-oldest"); err != nil {
		t.Fatalf("ReleasePin: %v", err)
	}
	stubExecutable(t, "binary-contents-after-release")
	if _, err := Resolve(root, testSchemaVersion); err != nil {
		t.Fatalf("Resolve after release: %v", err)
	}
	present = map[string]bool{}
	for _, name := range cachedBinaries(t, root) {
		present[name] = true
	}
	if present[cachedBinaryName(digests[0])] {
		t.Fatalf("a released digest survived retention: %v", present)
	}
}

// A claim whose launch never released — a killed TUI, an agent whose provider
// hook never fired — must not exempt its copy from retention forever, or
// retainedBinaries bounds nothing.
func TestRetentionExpiresAbandonedClaims(t *testing.T) {
	root := t.TempDir()
	stubExecutable(t, "binary-contents-stranded")
	stranded, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := RetainPin(root, "launch-abandoned", stranded.Digest); err != nil {
		t.Fatalf("RetainPin: %v", err)
	}
	claimPath := filepath.Join(root, cacheDirName, pinsDirName, "launch-abandoned")
	abandoned := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(claimPath, abandoned, abandoned); err != nil {
		t.Fatalf("chtimes claim: %v", err)
	}
	original := timeNow
	t.Cleanup(func() { timeNow = original })
	timeNow = func() time.Time { return abandoned.Add(pinClaimMaxAge + time.Hour) }

	// Fill the cache past the retention budget with newer copies.
	for i := range retainedBinaries + 1 {
		stubExecutable(t, "binary-contents-"+string(rune('a'+i)))
		if _, err := Resolve(root, testSchemaVersion); err != nil {
			t.Fatalf("Resolve %d: %v", i, err)
		}
	}

	for _, name := range cachedBinaries(t, root) {
		if name == cachedBinaryName(stranded.Digest) {
			t.Fatal("an abandoned claim kept its binary past retention")
		}
	}
	if _, err := os.Stat(claimPath); !os.IsNotExist(err) {
		t.Fatalf("stat expired claim = %v, want it removed", err)
	}
}

func TestResolveRefusesACacheDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	stubExecutable(t, "binary-contents")
	// MkdirAll succeeds on an existing symlink to a directory and Chmod follows
	// it, so without an Lstat the cache — and the binaries this process later
	// executes out of it — would be wherever the link points.
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(root, cacheDirName)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	pin, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve must degrade rather than fail: %v", err)
	}
	if !pin.Degraded {
		t.Fatalf("Resolve used a symlinked cache directory: %+v", pin)
	}
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Resolve wrote %d entries through the symlink", len(entries))
	}
}

func TestResolveDegradesWhenCacheCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	source := stubExecutable(t, "binary-contents")
	// The state root itself does not exist yet and its parent refuses writes,
	// which is what a read-only or full volume looks like from here.
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	pin, err := Resolve(root, testSchemaVersion)
	if err != nil {
		t.Fatalf("Resolve must degrade rather than fail: %v", err)
	}
	if !pin.Degraded {
		t.Fatal("Resolve did not report a degraded pin")
	}
	if pin.ExecutablePath != source {
		t.Fatalf("degraded ExecutablePath = %q, want the source %q", pin.ExecutablePath, source)
	}
	if strings.TrimSpace(pin.Notice) == "" {
		t.Fatal("a degraded pin must carry a notice naming the degradation")
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("Verify on a degraded pin: %v", err)
	}
}

func cachedBinaries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, cacheDirName))
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), cachedBinaryPrefix) {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func mustDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := fileDigest(path)
	if err != nil {
		t.Fatalf("digest %s: %v", path, err)
	}
	return digest
}

func TestPathMismatchNoticeOnlyFiresOnARealMismatch(t *testing.T) {
	pin := Pin{
		ExecutablePath: "/state/bin/approach-abc123",
		SourcePath:     "/opt/homebrew/bin/approach",
		Digest:         "abc123",
	}
	notFound := func(string) (string, error) { return "", os.ErrNotExist }

	if got := PathMismatchNotice(Pin{}, notFound); got != "" {
		t.Fatalf("unpinned launch produced a notice: %q", got)
	}
	if got := PathMismatchNotice(pin, notFound); got != "" {
		t.Fatalf("approach absent from PATH produced a notice: %q", got)
	}
	if got := PathMismatchNotice(pin, func(string) (string, error) { return pin.SourcePath, nil }); got != "" {
		t.Fatalf("PATH resolving to the pin source produced a notice: %q", got)
	}
	if got := PathMismatchNotice(pin, func(string) (string, error) { return pin.ExecutablePath, nil }); got != "" {
		t.Fatalf("PATH resolving to the cached copy produced a notice: %q", got)
	}

	// A different path holding the identical build is not a mismatch.
	source := stubExecutable(t, "binary-contents")
	same := Pin{ExecutablePath: "/state/bin/approach-abc123", Digest: mustDigest(t, source)}
	if got := PathMismatchNotice(same, func(string) (string, error) { return source, nil }); got != "" {
		t.Fatalf("PATH resolving to an identical build produced a notice: %q", got)
	}

	got := PathMismatchNotice(pin, func(string) (string, error) { return "/usr/local/bin/approach", nil })
	if !strings.Contains(got, "/usr/local/bin/approach") {
		t.Fatalf("mismatch notice = %q, want it to name the PATH binary", got)
	}

	degraded := pin
	degraded.Degraded = true
	degraded.Notice = "cache unavailable"
	if got := PathMismatchNotice(degraded, notFound); got != "cache unavailable" {
		t.Fatalf("degraded pin notice = %q, want the degradation to win", got)
	}
}
