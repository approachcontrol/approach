package planstore_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/planstore"
)

func TestStoreMutationsSerializeAndPreserveConcurrentChanges(t *testing.T) {
	root := t.TempDir()
	store1, err := planstore.NewStore(planstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	store2, err := planstore.NewStore(planstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(second) error = %v", err)
	}
	if _, err := store1.Save(planstore.PlanRecord{PlanID: "shared", Title: "Shared", Markdown: "old"}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}

	lockPath := filepath.Join(root, "plans", ".locks", "mutations.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	release, err := artifacts.AcquireFileLock(lockPath, "test plan mutation lock", time.Second)
	if err != nil {
		t.Fatalf("hold plan mutation lock: %v", err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := store1.Save(planstore.PlanRecord{PlanID: "shared", Title: "Shared updated", Markdown: "new", Summary: "saved concurrently"})
		results <- err
	}()
	go func() {
		results <- store2.SetPhase("shared", planstore.PlanPhase{PhaseID: "implementation", Title: "Implementation", Status: "in_progress"})
	}()
	select {
	case err := <-results:
		release()
		t.Fatalf("plan mutation completed while lock was held: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent plan mutation error = %v", err)
		}
	}
	got, err := store1.ReadMetadata("shared")
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if got.Summary != "saved concurrently" || len(got.Phases) != 1 || got.Phases[0].PhaseID != "implementation" {
		t.Fatalf("concurrent plan changes were not merged: %#v", got)
	}
}

func TestStoreConcurrentGeneratedIDSavesAreUnique(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	stores := make([]*planstore.Store, 2)
	for i := range stores {
		store, err := planstore.NewStore(planstore.StoreOptions{Root: root, Now: func() time.Time { return now }, LockTimeout: time.Second})
		if err != nil {
			t.Fatalf("NewStore(%d) error = %v", i, err)
		}
		stores[i] = store
	}
	start := make(chan struct{})
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func(store *planstore.Store) {
			defer wg.Done()
			<-start
			id, err := store.Save(planstore.PlanRecord{Title: "Same title", Markdown: "body"})
			ids <- id
			errs <- err
		}(store)
	}
	close(start)
	wg.Wait()
	close(ids)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("generated Save() error = %v", err)
		}
	}
	got := make(map[string]bool)
	for id := range ids {
		got[id] = true
	}
	if len(got) != 2 {
		t.Fatalf("generated plan IDs = %#v, want two unique IDs", got)
	}
}

func TestStoreListSkipsCorruptAndNonDirEntries(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{PlanID: "good", Title: "Good", Markdown: "b", Status: "draft"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// A plan dir with corrupt metadata must not hide other plans.
	badDir := filepath.Join(root, "plans", "bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A stray non-directory entry under plans/ must be ignored.
	if err := os.WriteFile(filepath.Join(root, "plans", "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].PlanID != "good" {
		t.Fatalf("List() = %#v, want only good", records)
	}
}

func TestStoreReadMetadataReturnsPlanMetadataAndReportsCorruptMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "readable",
		Title:    "Readable",
		Markdown: "# Readable\n",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	read, err := store.ReadMetadata("readable")
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if read.PlanID != "readable" || read.Markdown != "" {
		t.Fatalf("ReadMetadata() = %#v", read)
	}

	badDir := filepath.Join(root, "plans", "bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadMetadata("bad"); err == nil {
		t.Fatal("ReadMetadata(corrupt) error = nil")
	}
}

func TestStoreSavesAndListsPlansByRepoPath(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo", "feature")

	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	planID, err := store.Save(planstore.PlanRecord{
		PlanID:       "plan-tracer",
		Title:        "Persist plans",
		Summary:      "Add saved plans",
		Markdown:     "# Persist plans\n\nDo the thing.\n",
		Status:       "draft",
		Source:       "manual",
		Provider:     "claude",
		LaunchID:     "launch-1",
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Branch:       "feature/plans",
		Commit:       "abcdef123456",
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if planID != "plan-tracer" {
		t.Fatalf("Save() returned id %q, want plan-tracer", planID)
	}

	records, err := store.List(planstore.PlanFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1: %#v", len(records), records)
	}

	got := records[0]
	if got.PlanID != "plan-tracer" ||
		got.Title != "Persist plans" ||
		got.Summary != "Add saved plans" ||
		got.Markdown != "# Persist plans\n\nDo the thing.\n" ||
		got.Status != "draft" ||
		got.Source != "manual" ||
		got.Provider != "claude" ||
		got.LaunchID != "launch-1" ||
		got.RepoPath != repoPath ||
		got.WorktreePath != worktreePath ||
		got.Branch != "feature/plans" ||
		got.Commit != "abcdef123456" {
		t.Fatalf("record did not round-trip: %#v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("expected created/updated timestamps to be set: %#v", got)
	}

	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "plans"), 0o700)
	assertMode(t, filepath.Join(root, "plans", "plan-tracer"), 0o700)
	assertMode(t, filepath.Join(root, "plans", "plan-tracer", "meta.json"), 0o600)
	assertMode(t, filepath.Join(root, "plans", "plan-tracer", "plan.md"), 0o600)
}

func TestStoreListSortsByUpdatedAtDescending(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	older := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:    "older",
		Title:     "Older",
		Markdown:  "old",
		Status:    "draft",
		RepoPath:  repoPath,
		UpdatedAt: older,
		CreatedAt: older,
	}); err != nil {
		t.Fatalf("Save(older) error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:    "newer",
		Title:     "Newer",
		Markdown:  "new",
		Status:    "draft",
		RepoPath:  repoPath,
		UpdatedAt: newer,
		CreatedAt: newer,
	}); err != nil {
		t.Fatalf("Save(newer) error = %v", err)
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("List() returned %d records, want 2", len(records))
	}
	if records[0].PlanID != "newer" || records[1].PlanID != "older" {
		t.Fatalf("List() not sorted by updated_at desc: %#v", records)
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
