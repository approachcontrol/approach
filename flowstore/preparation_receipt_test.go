package flowstore_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestPreparationReceiptCanOnlyBeMintedBySuccessfulOneShotFinalizer(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	stamp := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return stamp }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	forged := stamp.Add(-time.Hour)
	ordinary, err := store.Create(flowstore.FlowRecord{
		FlowID: "ordinary", Title: "Ordinary", Instructions: "Test.", RepoPath: repo,
		PreparedAt: &forged,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if ordinary.PreparedAt != nil {
		t.Fatalf("ordinary Create retained forged receipt %s", ordinary.PreparedAt)
	}

	prepared, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "prepared", Title: "Prepared", Instructions: "Test.", RepoPath: repo,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if prepared.PreparedAt != nil || finalizer == nil {
		t.Fatalf("CreatePreparation() = receipt %v, finalizer %v", prepared.PreparedAt, finalizer)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: prepared.FlowID, WorktreePath: worktree, Branch: "flow/prepared",
	}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	callbacks := 0
	finalized, err := finalizer.Finalize(func() error {
		callbacks++
		return nil
	})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if finalized.PreparedAt == nil || !finalized.PreparedAt.Equal(stamp) || !finalized.UpdatedAt.Equal(stamp) {
		t.Fatalf("finalized receipt = %v, updated = %s; want %s", finalized.PreparedAt, finalized.UpdatedAt, stamp)
	}
	if callbacks != 1 {
		t.Fatalf("bootstrap callbacks = %d, want 1", callbacks)
	}
	if _, err := finalizer.Finalize(func() error { callbacks++; return nil }); err == nil {
		t.Fatal("second Finalize() succeeded")
	}
	if callbacks != 1 {
		t.Fatalf("second Finalize ran callback; callbacks = %d", callbacks)
	}
}
