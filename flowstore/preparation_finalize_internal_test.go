package flowstore

import (
	"errors"
	"path/filepath"
	"testing"
)

type failNthGetBackend struct {
	backend
	failOn int
	hits   int
	err    error
}

func (b *failNthGetBackend) get(flowID string) (storedFlow, bool, error) {
	b.hits++
	if b.hits == b.failOn {
		return storedFlow{}, false, b.err
	}
	return b.backend.get(flowID)
}

func TestFinalizeRetainsCapabilityWhenIdentityReadFails(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "identity-read", Title: "Identity read", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/identity-read",
	}); err != nil {
		t.Fatal(err)
	}

	store.backend = &failNthGetBackend{backend: store.backend, failOn: 1, err: errors.New("database busy")}
	if _, err := finalizer.Finalize(nil); err == nil || IsPreparationUnknown(err) {
		t.Fatalf("Finalize() error = %v, want a retryable pre-write read failure, not unknown", err)
	}

	store.backend = store.backend.(*failNthGetBackend).backend
	finalized, err := finalizer.Finalize(nil)
	if err != nil || finalized.PreparedAt == nil {
		t.Fatalf("retry Finalize() = receipt %v, err %v; want the finalizer retained after the identity-read failure", finalized.PreparedAt, err)
	}
}
