package planstore_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

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

func TestStoreDoesNotPublishMetadataWhenMarkdownWriteFails(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planDir := filepath.Join(root, "plans", "failed-markdown")
	if err := os.MkdirAll(filepath.Join(planDir, "plan.md"), 0o700); err != nil {
		t.Fatalf("create blocking plan.md directory: %v", err)
	}

	_, err = store.Save(planstore.PlanRecord{
		PlanID:   "failed-markdown",
		Title:    "Failed Markdown",
		Markdown: "new body",
		Status:   "draft",
	})
	if err == nil {
		t.Fatal("Save() error = nil, want Markdown write failure")
	}
	if _, readErr := store.ReadMetadata("failed-markdown"); readErr == nil {
		t.Fatal("ReadMetadata() error = nil, metadata was published before Markdown")
	}
}

func TestStoreKeepsExistingMetadataWhenMarkdownWriteFails(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "Before",
		Markdown: "old body",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}
	markdownPath := filepath.Join(root, "plans", "existing-plan", "plan.md")
	if err := os.Remove(markdownPath); err != nil {
		t.Fatalf("remove plan.md: %v", err)
	}
	if err := os.Mkdir(markdownPath, 0o700); err != nil {
		t.Fatalf("create blocking plan.md directory: %v", err)
	}

	_, err = store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "After",
		Markdown: "new body",
		Status:   "completed",
	})
	if err == nil {
		t.Fatal("update Save() error = nil, want Markdown write failure")
	}
	metadata, readErr := store.ReadMetadata("existing-plan")
	if readErr != nil {
		t.Fatalf("ReadMetadata() error = %v", readErr)
	}
	if metadata.Title != "Before" || metadata.Status != "draft" {
		t.Fatalf("ReadMetadata() = %#v, want unchanged existing metadata", metadata)
	}
}

func TestStoreRemovesNewPlanBodyWhenMetadataWriteFails(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planDir := filepath.Join(root, "plans", "new-plan")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(planDir, "meta.json"), 0o700); err != nil {
		t.Fatalf("create blocking meta.json directory: %v", err)
	}

	_, err = store.Save(planstore.PlanRecord{
		PlanID:   "new-plan",
		Title:    "New",
		Markdown: "new body",
		Status:   "draft",
	})
	if err == nil {
		t.Fatal("Save() error = nil, want metadata write failure")
	}
	if _, statErr := os.Stat(filepath.Join(planDir, "plan.md")); !os.IsNotExist(statErr) {
		t.Fatalf("unpublished plan.md still present: %v", statErr)
	}
}

func TestStoreRestoresPlanBodyWhenMetadataWriteFails(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "Before",
		Markdown: "old body",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}
	metaPath := filepath.Join(root, "plans", "existing-plan", "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove meta.json: %v", err)
	}
	if err := os.Mkdir(metaPath, 0o700); err != nil {
		t.Fatalf("create blocking meta.json directory: %v", err)
	}

	_, err = store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "After",
		Markdown: "new body",
		Status:   "completed",
	})
	if err == nil {
		t.Fatal("update Save() error = nil, want metadata write failure")
	}
	got, readErr := os.ReadFile(filepath.Join(root, "plans", "existing-plan", "plan.md"))
	if readErr != nil {
		t.Fatalf("read plan.md: %v", readErr)
	}
	if string(got) != "old body" {
		t.Fatalf("plan.md = %q, want restored previous body", got)
	}
}

func TestStoreFailsClosedOnUnreadablePreviousPlanBody(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "Before",
		Markdown: "old body",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}
	bodyPath := filepath.Join(root, "plans", "existing-plan", "plan.md")
	if err := os.Remove(bodyPath); err != nil {
		t.Fatalf("remove plan.md: %v", err)
	}
	if err := os.Mkdir(bodyPath, 0o700); err != nil {
		t.Fatalf("block plan.md: %v", err)
	}

	_, err = store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "After",
		Markdown: "new body",
		Status:   "completed",
	})
	if err == nil {
		t.Fatal("update Save() error = nil, want unreadable previous plan body")
	}
	info, statErr := os.Stat(bodyPath)
	if statErr != nil {
		t.Fatalf("previous plan.md was removed: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("previous plan.md = file, want blocking directory left in place")
	}
	metadata, readErr := store.ReadMetadata("existing-plan")
	if readErr != nil {
		t.Fatalf("ReadMetadata() error = %v", readErr)
	}
	if metadata.Title != "Before" || metadata.Status != "draft" {
		t.Fatalf("ReadMetadata() = %#v, want unchanged existing metadata", metadata)
	}
}

func TestStoreKeepsUnreadablePlanBodyOnMetadataOnlyUpdate(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "existing-plan",
		Title:    "Before",
		Markdown: "old body",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("seed Save() error = %v", err)
	}
	bodyPath := filepath.Join(root, "plans", "existing-plan", "plan.md")
	if err := os.Remove(bodyPath); err != nil {
		t.Fatalf("remove plan.md: %v", err)
	}
	if err := os.Mkdir(bodyPath, 0o700); err != nil {
		t.Fatalf("block plan.md: %v", err)
	}

	updated, err := store.SetStatus("existing-plan", "approved")
	if err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}
	if updated.Status != "approved" {
		t.Fatalf("SetStatus() status = %q, want approved", updated.Status)
	}
	info, statErr := os.Stat(bodyPath)
	if statErr != nil {
		t.Fatalf("previous plan.md was removed: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("previous plan.md = file, want blocking directory left in place")
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
