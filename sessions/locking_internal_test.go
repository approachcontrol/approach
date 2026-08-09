package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

type sessionLockAttempt struct {
	path  string
	label string
}

func TestIndependentStoresSerializeUpsertAndMarkLaunchEndedAtSessionLock(t *testing.T) {
	root := t.TempDir()
	store1, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	store2, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	started := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	if err := store1.Upsert(SessionRecord{
		Provider:   ProviderCodex,
		SessionID:  "shared-session",
		LaunchID:   "launch-1",
		Status:     "last_seen",
		StartedAt:  started,
		LastSeenAt: started.Add(time.Minute),
		Summary:    "old summary",
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}

	lockDir := filepath.Join(root, "sessions", ".locks")
	lockPath := filepath.Join(lockDir, "codex-"+safeSessionDirName("shared-session")+".lock")
	wantLabel := fmt.Sprintf("session lock %q/%q", ProviderCodex, "shared-session")
	release, err := artifacts.AcquireFileLock(lockPath, "test session lock", time.Second)
	if err != nil {
		t.Fatalf("hold session lock: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	attempts := make(chan sessionLockAttempt, 2)
	observe := func(path, label string, timeout time.Duration) (func(), error) {
		attempts <- sessionLockAttempt{path: path, label: label}
		return artifacts.AcquireFileLock(path, label, timeout)
	}
	store1.acquireFileLock = observe
	store2.acquireFileLock = observe
	results := make(chan error, 2)
	go func() {
		results <- store1.Upsert(SessionRecord{
			Provider:   ProviderCodex,
			SessionID:  "shared-session",
			LaunchID:   "launch-1",
			Status:     "last_seen",
			StartedAt:  started.Add(-time.Hour),
			LastSeenAt: started.Add(2 * time.Minute),
			Summary:    "new summary",
		})
	}()
	go func() {
		results <- store2.MarkLaunchEnded("launch-1", started.Add(3*time.Minute))
	}()

	for i := 0; i < 2; i++ {
		select {
		case attempt := <-attempts:
			if attempt.path != lockPath || attempt.label != wantLabel {
				release()
				released = true
				t.Fatalf("lock attempt = %#v, want path %q label %q", attempt, lockPath, wantLabel)
			}
		case <-time.After(time.Second):
			release()
			released = true
			t.Fatal("timed out waiting for both session writers at the lock boundary")
		}
	}
	release()
	released = true
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent session mutation error = %v", err)
		}
	}

	got, ok, err := store1.readMetadata(ProviderCodex, "shared-session")
	if err != nil || !ok {
		t.Fatalf("readMetadata() = %#v, %v, %v; want record", got, ok, err)
	}
	if got.Summary != "new summary" || got.Status != "ended" {
		t.Fatalf("merged summary/status = %q/%q", got.Summary, got.Status)
	}
	if !got.StartedAt.Equal(started.Add(-time.Hour)) || !got.EndedAt.Equal(started.Add(3*time.Minute)) || !got.LastSeenAt.Equal(started.Add(3*time.Minute)) {
		t.Fatalf("merged timestamps = started %v ended %v last-seen %v", got.StartedAt, got.EndedAt, got.LastSeenAt)
	}
	assertSessionLockMode(t, lockDir, 0o700)
	assertSessionLockMode(t, lockPath, 0o600)
}

func assertSessionLockMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
