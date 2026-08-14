package flowstore_test

import (
	"errors"
	"path/filepath"
	"strings"
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

func TestPreparationFinalizerFailureLeavesReceiptAbsentAndConsumesCapability(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "bootstrap-failed", Title: "Bootstrap failed", Instructions: "Test.", RepoPath: repo,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/bootstrap-failed",
	}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	bootstrapErr := errors.New("bootstrap failed")
	if _, err := finalizer.Finalize(func() error { return bootstrapErr }); !errors.Is(err, bootstrapErr) || !flowstore.IsPreparationIncomplete(err) {
		t.Fatalf("Finalize() error = %v, want bootstrap and incomplete classifications", err)
	}
	authoritative, err := store.Read(created.FlowID)
	if err != nil || authoritative.PreparedAt != nil {
		t.Fatalf("Read() after callback failure = receipt %v, err %v", authoritative.PreparedAt, err)
	}
	if _, err := finalizer.Finalize(nil); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("second Finalize() error = %v, want consumed capability", err)
	}
}

func TestPreparationFinalizerRejectsReplacementFlowGenerationBeforeCallback(t *testing.T) {
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := filepath.Join(t.TempDir(), "repo")
	created, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "reused-id", Title: "Original", Instructions: "Test.", RepoPath: repo,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(created.FlowID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	replacement, err := store.Create(flowstore.FlowRecord{
		FlowID: created.FlowID, Title: "Replacement", Instructions: "Do not certify.", RepoPath: repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: replacement.FlowID, WorktreePath: filepath.Join(t.TempDir(), "replacement"), Branch: "flow/replacement",
	}); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	if _, err := finalizer.Finalize(func() error { callbacks++; return nil }); !flowstore.IsPreparationIncomplete(err) || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("Finalize() error = %v, want incomplete generation refusal", err)
	}
	if callbacks != 0 {
		t.Fatalf("stale finalizer ran %d callbacks, want 0", callbacks)
	}
	authoritative, err := store.Read(replacement.FlowID)
	if err != nil || authoritative.PreparedAt != nil || authoritative.Title != replacement.Title {
		t.Fatalf("replacement after stale finalizer = %#v, err %v", authoritative, err)
	}
}

func TestPreparationReceiptProtectsStartIdentityButAllowsUnrelatedMetadata(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "worktree")
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "identity-protected", Title: "Protected", Instructions: "Test.", RepoPath: repo,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: worktree, Branch: "flow/protected",
	}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: worktree + string(filepath.Separator) + ".", Branch: "flow/protected", PlanID: "plan-1",
	}); err != nil {
		t.Fatalf("identical identity plus unrelated metadata error = %v", err)
	}
	for _, update := range []flowstore.StartMetadataUpdate{
		{FlowID: created.FlowID, WorktreePath: filepath.Join(t.TempDir(), "other-worktree")},
		{FlowID: created.FlowID, Branch: "flow/replaced"},
	} {
		if _, err := store.SetStartMetadata(update); err == nil || !strings.Contains(err.Error(), "preparation receipt protects") {
			t.Fatalf("SetStartMetadata(%#v) error = %v, want protected identity rejection", update, err)
		}
	}
	authoritative, err := store.Read(created.FlowID)
	if err != nil || authoritative.WorktreePath != worktree || authoritative.Branch != "flow/protected" || authoritative.PlanID != "plan-1" {
		t.Fatalf("protected identity after updates = %#v, err %v", authoritative, err)
	}
}

func TestPreparationFinalizerRejectsClosedAndAlreadyRunningFlows(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*testing.T, *flowstore.Store, flowstore.FlowRecord)
	}{
		{
			name: "closed",
			mutate: func(t *testing.T, store *flowstore.Store, flow flowstore.FlowRecord) {
				t.Helper()
				if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: flow.FlowID, Reason: "closed before finalization"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "already running",
			mutate: func(t *testing.T, store *flowstore.Store, flow flowstore.FlowRecord) {
				t.Helper()
				phases := flowstore.OrderedPhases(flow.Phases)
				if len(phases) == 0 {
					t.Fatal("prepared Flow has no launchable phase")
				}
				if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: flow.FlowID, PhaseID: phases[0].PhaseID, LaunchID: "premature-launch"}); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
				FlowID: "invalid-finalization", Title: "Invalid", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
			}, flowstore.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
				FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/invalid-finalization",
			}); err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, store, flow)
			if finalized, err := finalizer.Finalize(nil); !flowstore.IsPreparationIncomplete(err) || finalized.PreparedAt != nil {
				t.Fatalf("Finalize() = receipt %v, err %v; want confirmed incomplete refusal", finalized.PreparedAt, err)
			}
			authoritative, err := store.Read(flow.FlowID)
			if err != nil || authoritative.PreparedAt != nil {
				t.Fatalf("authoritative receipt = %v, err %v", authoritative.PreparedAt, err)
			}
		})
	}
}

func TestPreparedFlowMutationsClampRegressingClock(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "clock-regression", Title: "Clock regression", Instructions: "Keep timestamps monotonic.", RepoPath: "/repo",
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: "/worktrees/clock-regression", Branch: "flow/clock-regression",
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(-time.Hour)
	prepared, err := finalizer.Finalize(nil)
	if err != nil {
		t.Fatalf("Finalize() with regressing clock error = %v", err)
	}
	if prepared.PreparedAt == nil || prepared.PreparedAt.Before(prepared.CreatedAt) || prepared.UpdatedAt.Before(*prepared.PreparedAt) {
		t.Fatalf("prepared timestamps = created %s receipt %v updated %s", prepared.CreatedAt, prepared.PreparedAt, prepared.UpdatedAt)
	}

	autoMode, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: created.FlowID, Enabled: false})
	if err != nil {
		t.Fatalf("SetAutoMode() with regressing clock error = %v", err)
	}
	phaseID := flowstore.OrderedPhases(autoMode.Phases)[0].PhaseID
	running, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: created.FlowID, PhaseID: phaseID, LaunchID: "launch-clock-regression",
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() with regressing clock error = %v", err)
	}
	attention, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID: created.FlowID, PhaseID: phaseID, Status: flowstore.PhaseNeedsAttention, Notes: "clock moved backward",
	})
	if err != nil {
		t.Fatalf("SetPhase() with regressing clock error = %v", err)
	}
	for name, record := range map[string]flowstore.FlowRecord{
		"auto mode": autoMode,
		"launch":    running,
		"phase":     attention,
	} {
		if record.PreparedAt == nil || record.UpdatedAt.Before(*record.PreparedAt) {
			t.Fatalf("%s timestamps = receipt %v updated %s", name, record.PreparedAt, record.UpdatedAt)
		}
	}
	if _, err := store.Read(created.FlowID); err != nil {
		t.Fatalf("Read() after regressing-clock mutations error = %v", err)
	}
}
