package planstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

type planLockAttempt struct {
	path  string
	label string
}

func TestIndependentStoresSerializeSaveAndSetPhaseAtMutationLock(t *testing.T) {
	root := t.TempDir()
	store1, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	store2, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	if _, err := store1.Save(PlanRecord{
		PlanID: "shared", Title: "Original title", Summary: "Original summary", Markdown: "Original body", Status: "draft",
		Phases: []PlanPhase{{PhaseID: "plan", Title: "Plan", Status: "completed", Order: 1}},
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}

	lockDir := filepath.Join(root, "plans", ".locks")
	lockPath := filepath.Join(lockDir, "mutations.lock")
	release, err := artifacts.AcquireFileLock(lockPath, "test plan mutation lock", time.Second)
	if err != nil {
		t.Fatalf("hold plan mutation lock: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	attempts := make(chan planLockAttempt, 2)
	observe := func(path, label string, timeout time.Duration) (func(), error) {
		attempts <- planLockAttempt{path: path, label: label}
		return artifacts.AcquireFileLock(path, label, timeout)
	}
	store1.acquireFileLock = observe
	store2.acquireFileLock = observe
	results := make(chan error, 2)
	go func() {
		_, err := store1.Save(PlanRecord{
			PlanID: "shared", Title: "Concurrent title", Summary: "Concurrent summary", Markdown: "Concurrent body", Status: "approved",
			// Phases is intentionally omitted so Save preserves any SetPhase update
			// regardless of which waiter acquires the mutation lock first.
		})
		results <- err
	}()
	go func() {
		results <- store2.SetPhase("shared", PlanPhase{
			PhaseID: "implementation", Title: "Implementation", Status: "in_progress", Order: 3,
		})
	}()

	waitForPlanLockAttempts(t, attempts, 2, lockPath)
	release()
	released = true
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent plan mutation error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent plan mutations to finish")
		}
	}

	got, err := store1.ReadMetadata("shared")
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if got.Title != "Concurrent title" || got.Summary != "Concurrent summary" || got.Status != "approved" {
		t.Fatalf("concurrent plan metadata was not preserved: %#v", got)
	}
	if len(got.Phases) != 2 || got.Phases[0].PhaseID != "plan" || got.Phases[1] != (PlanPhase{PhaseID: "implementation", Title: "Implementation", Status: "in_progress", Order: 3}) {
		t.Fatalf("concurrent phases = %#v, want plan plus implementation", got.Phases)
	}
	if body, err := store1.ReadPlan("shared"); err != nil || body != "Concurrent body" {
		t.Fatalf("ReadPlan() = %q, %v; want concurrent body", body, err)
	}
	assertPlanLockMode(t, lockDir, 0o700)
	assertPlanLockMode(t, lockPath, 0o600)
}

func TestSetPhaseStatusRereadsAfterMutationLockAcquisition(t *testing.T) {
	root := t.TempDir()
	saveStore, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(save) error = %v", err)
	}
	statusStore, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(status) error = %v", err)
	}
	if _, err := saveStore.Save(PlanRecord{
		PlanID:   "shared-status",
		Title:    "Original title",
		Summary:  "Original summary",
		Markdown: "Original body",
		Status:   "draft",
		Phases: []PlanPhase{
			{PhaseID: "Implementation", Title: "Original implementation", Status: "in_progress", Order: 3},
			{PhaseID: "unrelated", Title: "Unrelated", Status: "blocked", Order: 4},
		},
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}

	lockPath := filepath.Join(root, "plans", ".locks", "mutations.lock")
	release, err := artifacts.AcquireFileLock(lockPath, "test plan mutation lock", time.Second)
	if err != nil {
		t.Fatalf("hold plan mutation lock: %v", err)
	}
	released := false
	allowStatusAcquire := make(chan struct{})
	statusAllowed := false
	t.Cleanup(func() {
		if !released {
			release()
		}
		if !statusAllowed {
			close(allowStatusAcquire)
		}
	})

	saveAttempts := make(chan planLockAttempt, 1)
	statusAttempts := make(chan planLockAttempt, 1)
	saveStore.acquireFileLock = func(path, label string, timeout time.Duration) (func(), error) {
		saveAttempts <- planLockAttempt{path: path, label: label}
		return artifacts.AcquireFileLock(path, label, timeout)
	}
	statusStore.acquireFileLock = func(path, label string, timeout time.Duration) (func(), error) {
		statusAttempts <- planLockAttempt{path: path, label: label}
		<-allowStatusAcquire
		return artifacts.AcquireFileLock(path, label, timeout)
	}

	saveResult := make(chan error, 1)
	go func() {
		_, err := saveStore.Save(PlanRecord{
			PlanID:   "shared-status",
			Title:    "Concurrent title",
			Summary:  "Concurrent summary",
			Markdown: "Concurrent body",
			Status:   "approved",
			// Phases is intentionally omitted so the under-lock merge preserves
			// the status-only update after this Save completes.
		})
		saveResult <- err
	}()
	statusResult := make(chan error, 1)
	go func() {
		statusResult <- statusStore.SetPhaseStatus("shared-status", " implementation ", "completed")
	}()

	waitForPlanLockAttempts(t, saveAttempts, 1, lockPath)
	waitForPlanLockAttempts(t, statusAttempts, 1, lockPath)
	release()
	released = true
	select {
	case err := <-saveResult:
		if err != nil {
			t.Fatalf("concurrent Save() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent Save to finish")
	}
	close(allowStatusAcquire)
	statusAllowed = true
	select {
	case err := <-statusResult:
		if err != nil {
			t.Fatalf("SetPhaseStatus() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for status-only plan mutation to finish")
	}

	got, err := saveStore.ReadMetadata("shared-status")
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if got.Title != "Concurrent title" || got.Summary != "Concurrent summary" || got.Status != "approved" {
		t.Fatalf("status-only mutation overwrote concurrent plan metadata: %#v", got)
	}
	wantPhases := []PlanPhase{
		{PhaseID: "Implementation", Title: "Original implementation", Status: "completed", Order: 3},
		{PhaseID: "unrelated", Title: "Unrelated", Status: "blocked", Order: 4},
	}
	if len(got.Phases) != len(wantPhases) {
		t.Fatalf("phases = %#v, want %#v", got.Phases, wantPhases)
	}
	for i := range wantPhases {
		if got.Phases[i] != wantPhases[i] {
			t.Fatalf("phase[%d] = %#v, want %#v", i, got.Phases[i], wantPhases[i])
		}
	}
	if body, err := saveStore.ReadPlan("shared-status"); err != nil || body != "Concurrent body" {
		t.Fatalf("ReadPlan() = %q, %v; want concurrent body", body, err)
	}
}

func TestIndependentStoresSerializeGeneratedIDAllocationAtMutationLock(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC)
	store1, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return now }, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	store2, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return now }, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	lockPath := filepath.Join(root, "plans", ".locks", "mutations.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), artifacts.DirPerm); err != nil {
		t.Fatalf("create plan lock directory: %v", err)
	}
	release, err := artifacts.AcquireFileLock(lockPath, "test plan mutation lock", time.Second)
	if err != nil {
		t.Fatalf("hold plan mutation lock: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	attempts := make(chan planLockAttempt, 2)
	observe := func(path, label string, timeout time.Duration) (func(), error) {
		attempts <- planLockAttempt{path: path, label: label}
		return artifacts.AcquireFileLock(path, label, timeout)
	}
	store1.acquireFileLock = observe
	store2.acquireFileLock = observe
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for _, store := range []*Store{store1, store2} {
		go func(store *Store) {
			id, err := store.Save(PlanRecord{Title: "Same title", Markdown: "body"})
			results <- result{id: id, err: err}
		}(store)
	}

	waitForPlanLockAttempts(t, attempts, 2, lockPath)
	release()
	released = true
	ids := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("generated Save() error = %v", result.err)
			}
			ids[result.id] = true
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for generated-ID plan saves to finish")
		}
	}
	if len(ids) != 2 {
		t.Fatalf("generated plan IDs = %#v, want two unique IDs", ids)
	}
}

func TestPlanMutationLockTimeoutNamesContestedResource(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	lockPath := filepath.Join(root, "plans", ".locks", "mutations.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), artifacts.DirPerm); err != nil {
		t.Fatalf("create plan lock directory: %v", err)
	}
	release, err := artifacts.AcquireFileLock(lockPath, "test plan mutation lock", time.Second)
	if err != nil {
		t.Fatalf("hold plan mutation lock: %v", err)
	}
	defer release()

	_, err = store.Save(PlanRecord{Title: "Blocked", Markdown: "body"})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "plan mutation lock") {
		t.Fatalf("Save() error = %v, want bounded plan mutation lock timeout", err)
	}
}

func waitForPlanLockAttempts(t *testing.T, attempts <-chan planLockAttempt, count int, wantPath string) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case attempt := <-attempts:
			if attempt.path != wantPath || attempt.label != "plan mutation lock" {
				t.Fatalf("lock attempt = %#v, want path %q label %q", attempt, wantPath, "plan mutation lock")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for plan writers at the mutation lock boundary")
		}
	}
}

func assertPlanLockMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
