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
		PreparedAt: &forged, PreparationNonce: "forged",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if ordinary.PreparedAt != nil || ordinary.PreparationNonce != "" {
		t.Fatalf("ordinary Create retained forged preparation state: receipt %v, nonce %q", ordinary.PreparedAt, ordinary.PreparationNonce)
	}

	prepared, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "prepared", Title: "Prepared", Instructions: "Test.", RepoPath: repo,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if prepared.PreparedAt != nil || prepared.PreparationNonce == "" || finalizer == nil {
		t.Fatalf("CreatePreparation() = receipt %v, nonce %q, finalizer %v", prepared.PreparedAt, prepared.PreparationNonce, finalizer)
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

func TestPreparationFinalizerCompensatesOnlyItsExactReceiptlessGeneration(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "compensated", Title: "Compensated", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	compensated, err := finalizer.Compensate("creation canceled after repository changed")
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	phase, _, ok := flowstore.FirstLaunchablePhase(compensated)
	if ok {
		t.Fatalf("Compensate() left launchable phase %#v", phase)
	}
	actionable, ok := flowstore.NextActionablePhase(compensated)
	if !ok || actionable.Status != flowstore.PhaseBlocked || !strings.Contains(actionable.Notes, "creation canceled") {
		t.Fatalf("Compensate() actionable phase = %#v, %t", actionable, ok)
	}
	if _, err := finalizer.Finalize(nil); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("Finalize() after Compensate() error = %v, want consumed capability", err)
	}

	stale, staleFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "reused", Title: "Stale", Instructions: "Stale.", RepoPath: filepath.Join(t.TempDir(), "stale-repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(stale.FlowID); err != nil {
		t.Fatal(err)
	}
	replacement, replacementFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: stale.FlowID, Title: stale.Title, Instructions: stale.Instructions, RepoPath: stale.RepoPath,
		BaseRef: stale.BaseRef, Bead: stale.Bead, CreatedAt: stale.CreatedAt,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleFinalizer.Compensate("must not touch replacement"); err == nil {
		t.Fatal("stale finalizer compensated a same-ID replacement")
	}
	authoritative, err := store.Read(replacement.FlowID)
	if err != nil || authoritative.Title != replacement.Title || !authoritative.CreatedAt.Equal(stale.CreatedAt) {
		t.Fatalf("same-ID replacement = %#v, %v", authoritative, err)
	}
	if _, _, ok := flowstore.FirstLaunchablePhase(authoritative); !ok {
		t.Fatalf("old finalizer changed same-ID replacement: %#v", authoritative)
	}
	if _, err := replacementFinalizer.Compensate("test cleanup"); err != nil {
		t.Fatal(err)
	}

	claimed, claimedFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "claimed", Title: "Claimed", Instructions: "Claimed.", RepoPath: filepath.Join(t.TempDir(), "claimed-repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	launchable, _, ok := flowstore.FirstLaunchablePhase(claimed)
	if !ok {
		t.Fatal("claimed preparation has no launchable phase")
	}
	claimed, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: claimed.FlowID, PhaseID: launchable.PhaseID, LaunchID: "claim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claimedFinalizer.Compensate("must not overwrite claimant"); err == nil {
		t.Fatal("Compensate() overwrote a claimed preparation")
	}
	authoritative, err = store.Read(claimed.FlowID)
	if err != nil || authoritative.Phases[0].Status != flowstore.PhaseRunning || len(authoritative.Phases[0].LaunchIDs) != 1 {
		t.Fatalf("claimed preparation after Compensate() = %#v, %v", authoritative, err)
	}

	provisioned, provisionedFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "provisioned", Title: "Provisioned", Instructions: "Provisioned.", RepoPath: filepath.Join(t.TempDir(), "provisioned-repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: provisioned.FlowID, WorktreePath: filepath.Join(t.TempDir(), "provisioned-worktree"), Branch: "flow/provisioned",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionedFinalizer.Compensate("must not overwrite provisioner"); err == nil || !strings.Contains(err.Error(), "claimed") {
		t.Fatalf("Compensate() provisioned Flow error = %v", err)
	}
	authoritative, err = store.Read(provisioned.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := flowstore.FirstLaunchablePhase(authoritative); !ok {
		t.Fatalf("Compensate() blocked a provisioned Flow: %#v", authoritative)
	}

	baseClaimed, baseClaimedFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "base-claimed", Title: "Base claimed", Instructions: "Claimed.", RepoPath: filepath.Join(t.TempDir(), "base-claimed-repo"),
		BaseRef: "main",
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: baseClaimed.FlowID, BaseRef: "release",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := baseClaimedFinalizer.Compensate("must not overwrite base-ref claimant"); err == nil || !strings.Contains(err.Error(), "claimed") {
		t.Fatalf("Compensate() base-ref claim error = %v", err)
	}
	authoritative, err = store.Read(baseClaimed.FlowID)
	if err != nil || authoritative.BaseRef != "release" {
		t.Fatalf("base-ref claimed preparation = %#v, %v", authoritative, err)
	}
	if _, _, ok := flowstore.FirstLaunchablePhase(authoritative); !ok {
		t.Fatalf("Compensate() blocked a base-ref claimed Flow: %#v", authoritative)
	}
}

func TestPreparationFinalizerSnapshotIsolatedFromReturnedRecordMutation(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "snapshot", Title: "Snapshot", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Phases) == 0 {
		t.Fatal("created preparation has no phases")
	}
	created.Phases[0].Status = flowstore.PhaseRunning
	created.Phases[0].LaunchIDs = append(created.Phases[0].LaunchIDs, "local-mutation")
	created.Phases[0].Sessions = append(created.Phases[0].Sessions, flowstore.Session{Provider: "codex", SessionID: "local"})

	compensated, err := finalizer.Compensate("creation canceled")
	if err != nil {
		t.Fatalf("Compensate() after returned-record mutation error = %v", err)
	}
	if _, _, ok := flowstore.FirstLaunchablePhase(compensated); ok {
		t.Fatalf("Compensate() left launchable roots after returned-record mutation: %#v", compensated)
	}
}

func TestPreparationFinalizerDoesNotReconcileForeignGenerationReceipt(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	stale, staleFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "finalize-reused", Title: "Same", Instructions: "Same.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(stale.FlowID); err != nil {
		t.Fatal(err)
	}
	replacement, replacementFinalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: stale.FlowID, Title: stale.Title, Instructions: stale.Instructions, RepoPath: stale.RepoPath,
		BaseRef: stale.BaseRef, Bead: stale.Bead, CreatedAt: stale.CreatedAt,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: replacement.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/replacement",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := replacementFinalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}

	foreign, err := staleFinalizer.Finalize(nil)
	if err == nil || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("stale Finalize() = %#v, %v", foreign, err)
	}
	if foreign.PreparedAt == nil || foreign.PreparationNonce == stale.PreparationNonce {
		t.Fatalf("stale Finalize() did not return the foreign authoritative generation: %#v", foreign)
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
