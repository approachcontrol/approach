package flowstore_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestEpicProgressionPersistsPerCanonicalRepositoryAndEpic(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	times := []time.Time{
		time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 14, 12, 1, 0, 0, time.UTC),
		time.Date(2026, 8, 14, 12, 2, 0, 0, time.UTC),
	}
	next := 0
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time {
		value := times[next]
		next++
		return value
	}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	key := flowstore.EpicProgressionKey{RepoPath: repo + string(filepath.Separator) + ".", EpicID: " epic-1 "}
	if _, found, err := store.ReadEpicProgression(key); err != nil || found {
		t.Fatalf("ReadEpicProgression(absent) = found %t, err %v; want false, nil", found, err)
	}

	enabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatalf("SetEpicProgression(true) error = %v", err)
	}
	if enabled.RepoPath != filepath.Clean(repo) || enabled.EpicID != "epic-1" || !enabled.Enabled || enabled.Halt != nil {
		t.Fatalf("enabled progression = %#v", enabled)
	}
	if !enabled.CreatedAt.Equal(times[0]) || !enabled.UpdatedAt.Equal(times[0]) {
		t.Fatalf("enabled timestamps = %s / %s, want %s", enabled.CreatedAt, enabled.UpdatedAt, times[0])
	}

	redundant, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatalf("SetEpicProgression(redundant true) error = %v", err)
	}
	if !redundant.UpdatedAt.Equal(enabled.UpdatedAt) {
		t.Fatalf("redundant updated_at = %s, want unchanged %s", redundant.UpdatedAt, enabled.UpdatedAt)
	}

	disabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatalf("SetEpicProgression(false) error = %v", err)
	}
	if disabled.Enabled || disabled.Halt != nil || !disabled.CreatedAt.Equal(times[0]) || !disabled.UpdatedAt.Equal(times[1]) {
		t.Fatalf("disabled progression = %#v", disabled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("reopen NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, found, err := reopened.ReadEpicProgression(flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic-1"})
	if err != nil || !found {
		t.Fatalf("ReadEpicProgression(reopened) = found %t, err %v", found, err)
	}
	if got.Enabled || !got.CreatedAt.Equal(times[0]) || !got.UpdatedAt.Equal(times[1]) {
		t.Fatalf("reopened progression = %#v", got)
	}
}

func TestEnableEpicProgressionRequiresExactPreparedPendingFlow(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "worktree")
	stamp := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return stamp }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	link := flowstore.BeadLink{ID: "epic-1.1", EpicID: "epic-1"}
	flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "child-flow", Title: "Child", Instructions: "Test.", RepoPath: repo, Bead: link,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{FlowID: flow.FlowID, WorktreePath: worktree, Branch: "flow/child"}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic-1"}
	if _, _, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID, Key: key, Bead: flowstore.BeadLink{ID: "wrong", EpicID: "epic-1"},
	}); err == nil {
		t.Fatal("EnableEpicProgressionForPreparedFlow(wrong link) succeeded")
	}
	if _, found, err := store.ReadEpicProgression(key); err != nil || found {
		t.Fatalf("progression after rejected enable = found %t, err %v", found, err)
	}

	progression, authoritative, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID, Key: key, Bead: link,
	})
	if err != nil {
		t.Fatalf("EnableEpicProgressionForPreparedFlow() error = %v", err)
	}
	if !progression.Enabled || authoritative.PreparedAt == nil || authoritative.Status != flowstore.StatusPending || authoritative.Bead != link {
		t.Fatalf("enable result = progression %#v, flow %#v", progression, authoritative)
	}
}
