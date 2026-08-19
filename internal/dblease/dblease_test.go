package dblease_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/dblease"
)

func identity(t *testing.T, build string) dblease.Identity {
	t.Helper()
	return dblease.Identity{
		BuildVersion:  build,
		Commit:        "deadbee",
		Executable:    "/usr/local/bin/approach",
		SchemaVersion: 6,
		StartedAt:     time.Now().UTC(),
	}
}

func TestTwoLiveHoldersAreBothReported(t *testing.T) {
	root := t.TempDir()
	first, err := dblease.Acquire(root, identity(t, "v0.10.3"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := dblease.Acquire(root, identity(t, "v0.10.4"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()

	live, reaped, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 0 {
		t.Fatalf("reaped %v while both holders are live", reaped)
	}
	if len(live) != 2 {
		t.Fatalf("live = %#v, want two holders", live)
	}
	builds := map[string]bool{}
	for _, record := range live {
		if record.PID != os.Getpid() {
			t.Fatalf("record %#v does not name this process (%d)", record, os.Getpid())
		}
		builds[record.BuildVersion] = true
	}
	if !builds["v0.10.3"] || !builds["v0.10.4"] {
		t.Fatalf("builds = %v, want both", builds)
	}
}

func TestExcludeOmitsTheCallersOwnHolder(t *testing.T) {
	root := t.TempDir()
	self, err := dblease.Acquire(root, identity(t, "self"))
	if err != nil {
		t.Fatal(err)
	}
	defer self.Release()
	other, err := dblease.Acquire(root, identity(t, "other"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()

	live, _, err := dblease.Scan(root, self.Nonce())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].BuildVersion != "other" {
		t.Fatalf("live = %#v, want only the other holder", live)
	}
}

// TestAHolderKilledWithSIGKILLIsReaped is why the lock is exclusive and
// per-holder: a dead process releases its flock, so liveness needs no PID
// heuristic and a crash leaves nothing behind to block a migration forever.
func TestAHolderKilledWithSIGKILLIsReaped(t *testing.T) {
	root := t.TempDir()
	cmd := holderProcess(t, root, "victim")
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	live, reaped, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("live = %#v after the holder was killed", live)
	}
	if len(reaped) != 1 {
		t.Fatalf("reaped = %v, want the killed holder's file", reaped)
	}
	entries, err := os.ReadDir(dblease.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("reaped holder's file survives: %s", entry.Name())
		}
	}
}

// TestAHolderCreatedDuringAScanIsNeverReaped drives the create/rename window
// through the probe seam. Write-then-lock would let this scan's non-blocking
// exclusive acquire succeed, conclude the holder is dead, and unlink the file
// underneath a live process.
func TestAHolderCreatedDuringAScanIsNeverReaped(t *testing.T) {
	root := t.TempDir()
	var scanLive []dblease.Record
	var scanReaped []string
	restore := dblease.SetAcquisitionProbe(func(step string) {
		if step != dblease.ProbeLocked {
			return
		}
		// Mid-acquire: the temp file exists and is locked, and the rename has
		// not happened. A scan here must see nothing and destroy nothing.
		live, reaped, err := dblease.Scan(root)
		if err != nil {
			t.Errorf("scan during acquire: %v", err)
		}
		scanLive, scanReaped = live, reaped
	})
	defer restore()

	holder, err := dblease.Acquire(root, identity(t, "racing"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if len(scanLive) != 0 || len(scanReaped) != 0 {
		t.Fatalf("a scan inside the create window saw live=%#v reaped=%v", scanLive, scanReaped)
	}
	// And the holder that raced the scan is still there and still live.
	live, _, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].BuildVersion != "racing" {
		t.Fatalf("live after the race = %#v", live)
	}
}

// TestScanIgnoresTemporaryFiles is the same invariant from the other side: a
// scan that unlinked a temp file would make the holder's rename fail ENOENT.
func TestScanIgnoresTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(dblease.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(dblease.Dir(root), ".tmp-abc123.json")
	if err := os.WriteFile(temp, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	live, reaped, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 || len(reaped) != 0 {
		t.Fatalf("a temp file was reported: live=%#v reaped=%v", live, reaped)
	}
	if _, err := os.Lstat(temp); err != nil {
		t.Fatalf("a temp file was unlinked by a scan: %v", err)
	}
}

func TestReleaseIsIdempotentAndRemovesTheHolder(t *testing.T) {
	root := t.TempDir()
	holder, err := dblease.Acquire(root, identity(t, "released"))
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Release(); err != nil {
		t.Fatalf("second Release = %v, want nil", err)
	}
	live, _, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("live = %#v after Release", live)
	}
}

func TestScanOverAnAbsentDirectoryIsEmpty(t *testing.T) {
	live, reaped, err := dblease.Scan(t.TempDir())
	if err != nil {
		t.Fatalf("scanning a root with no owners directory = %v, want nil", err)
	}
	if len(live) != 0 || len(reaped) != 0 {
		t.Fatalf("live=%#v reaped=%v", live, reaped)
	}
}

func TestAcquireOnAnUnwritableRootFailsWithANamedError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0o700) }()
	_, err := dblease.Acquire(root, identity(t, "denied"))
	if err == nil {
		t.Fatal("Acquire on an unwritable root succeeded")
	}
	if !errors.Is(err, dblease.ErrOwnersDirectory) {
		t.Fatalf("err = %v, want one wrapping ErrOwnersDirectory", err)
	}
}

// holderProcess starts a second process holding a lease and waits until its
// record is visible, so the caller can kill it and scan deterministically.
func holderProcess(t *testing.T, root, build string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestHelperHoldsALease$")
	cmd.Env = append(os.Environ(),
		"APPROACH_DBLEASE_HELPER=1",
		"APPROACH_DBLEASE_ROOT="+root,
		"APPROACH_DBLEASE_BUILD="+build,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		live, _, err := dblease.Scan(root)
		if err == nil && len(live) == 1 {
			return cmd
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the helper holder never published a record")
	return nil
}

// TestHelperHoldsALease is the second process. It takes a lease and blocks
// until it is killed, which is the only way to observe a lock surviving a
// process rather than a goroutine.
func TestHelperHoldsALease(t *testing.T) {
	if os.Getenv("APPROACH_DBLEASE_HELPER") == "" {
		t.Skip("helper process")
	}
	holder, err := dblease.Acquire(os.Getenv("APPROACH_DBLEASE_ROOT"), dblease.Identity{
		BuildVersion:  os.Getenv("APPROACH_DBLEASE_BUILD"),
		Executable:    "/usr/local/bin/approach",
		SchemaVersion: 6,
		StartedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = holder
	select {}
}

// A holder whose lock is held is LIVE, whatever its file says. Damaged metadata
// must not turn that proof into "no holder": a migration or a restore would
// then run underneath a process that never closes its handle.
func TestALiveHolderWithADamagedRecordIsStillReported(t *testing.T) {
	root := t.TempDir()
	holder, err := dblease.Acquire(root, identity(t, "v0.10.3"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	entries, err := os.ReadDir(dblease.Dir(root))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v (%v)", entries, err)
	}
	path := filepath.Join(dblease.Dir(root), entries[0].Name())
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	live, reaped, err := dblease.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 0 {
		t.Fatalf("reaped %v while the holder is live", reaped)
	}
	if len(live) != 1 {
		t.Fatalf("live = %#v, want the damaged holder reported", live)
	}
	if live[0].Unreadable == "" {
		t.Fatalf("record %#v does not say its metadata is unreadable", live[0])
	}
	if !strings.Contains(live[0].Describe(), path) {
		t.Fatalf("describe = %q does not name the holder file", live[0].Describe())
	}
	// Schema 0 is older than any real one, so it blocks a migration too.
	if live[0].SchemaVersion != 0 {
		t.Fatalf("schema = %d, want 0 for a record nothing could be read from", live[0].SchemaVersion)
	}
}
