package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

type injectedCommitMode int

const (
	injectedCommitAbsent injectedCommitMode = iota
	injectedCommitDurable
	injectedCommitUnreadable
)

type injectedCommitTx struct {
	sqliteTransaction
	mode         injectedCommitMode
	db           *sql.DB
	afterDurable func()
}

func (tx *injectedCommitTx) Commit() error {
	switch tx.mode {
	case injectedCommitDurable:
		if err := tx.sqliteTransaction.Commit(); err != nil {
			return err
		}
		if tx.afterDurable != nil {
			tx.afterDurable()
		}
	case injectedCommitUnreadable:
		if err := tx.sqliteTransaction.Rollback(); err != nil {
			return err
		}
		if err := tx.db.Close(); err != nil {
			return err
		}
	}
	return errors.New("injected commit acknowledgement failure")
}

func injectNextCommitOutcome(store *Store, mode injectedCommitMode) {
	backend := store.backend.(*sqliteBackend)
	original := backend.beginTx
	backend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
		tx, err := original(ctx)
		if err != nil {
			return nil, err
		}
		backend.beginTx = original
		return &injectedCommitTx{sqliteTransaction: tx, mode: mode, db: backend.db}, nil
	}
}

func TestPreparationReceiptReconcilesCommitAcknowledgementFailures(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mode        injectedCommitMode
		wantReceipt bool
		wantUnknown bool
	}{
		{name: "confirmed absent", mode: injectedCommitAbsent},
		{name: "confirmed durable", mode: injectedCommitDurable, wantReceipt: true},
		{name: "readback unavailable", mode: injectedCommitUnreadable, wantUnknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			flow, finalizer, err := store.CreatePreparation(FlowRecord{
				FlowID: "receipt-outcome", Title: "Receipt", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
			}, CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetStartMetadata(StartMetadataUpdate{
				FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/receipt-outcome",
			}); err != nil {
				t.Fatal(err)
			}
			injectNextCommitOutcome(store, tt.mode)
			finalized, err := finalizer.Finalize(nil)
			if tt.wantReceipt {
				if err != nil || finalized.PreparedAt == nil {
					t.Fatalf("Finalize() = receipt %v, err %v; want confirmed durable success", finalized.PreparedAt, err)
				}
				return
			}
			if tt.wantUnknown {
				if !IsPreparationUnknown(err) {
					t.Fatalf("Finalize() error = %v, want unknown classification", err)
				}
				return
			}
			if !IsPreparationIncomplete(err) || finalized.PreparedAt != nil {
				t.Fatalf("Finalize() = receipt %v, err %v; want confirmed incomplete", finalized.PreparedAt, err)
			}
		})
	}
}

func TestPreparationCreateReconcilesCommitAcknowledgementFailures(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mode        injectedCommitMode
		wantCreated bool
		wantUnknown bool
	}{
		{name: "confirmed absent", mode: injectedCommitAbsent},
		{name: "confirmed durable", mode: injectedCommitDurable, wantCreated: true},
		{name: "readback unavailable", mode: injectedCommitUnreadable, wantUnknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			injectNextCommitOutcome(store, tt.mode)
			flow, finalizer, err := store.CreatePreparation(FlowRecord{
				FlowID: "create-outcome", Title: "Create", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
			}, CreateOptions{})
			if tt.wantUnknown {
				if !IsPreparationUnknown(err) {
					t.Fatalf("CreatePreparation() error = %v, want unknown classification", err)
				}
				return
			}
			if !tt.wantCreated {
				if err == nil || finalizer != nil || flow.FlowID != "" {
					t.Fatalf("CreatePreparation() = %#v, %v, %v; want confirmed absent error", flow, finalizer, err)
				}
				if _, readErr := store.Read("create-outcome"); !IsNotFound(readErr) {
					t.Fatalf("Read() after absent insert error = %v, want not found", readErr)
				}
				return
			}
			if err != nil || finalizer == nil || flow.FlowID != "create-outcome" || flow.PreparationNonce == "" {
				t.Fatalf("CreatePreparation() = %#v, %v, %v; want reconciled durable preparation", flow, finalizer, err)
			}
			compensated, compensateErr := finalizer.Compensate("test cleanup")
			if compensateErr != nil {
				t.Fatalf("reconciled finalizer Compensate() error = %v", compensateErr)
			}
			if _, _, ok := FirstLaunchablePhase(compensated); ok {
				t.Fatalf("reconciled finalizer left launchable preparation: %#v", compensated)
			}
		})
	}
}

func TestPreparationCreateDoesNotAdoptAClaimMadeAfterAmbiguousCommit(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	original := backend.beginTx
	backend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
		tx, beginErr := original(ctx)
		if beginErr != nil {
			return nil, beginErr
		}
		backend.beginTx = original
		return &injectedCommitTx{
			sqliteTransaction: tx,
			mode:              injectedCommitDurable,
			db:                backend.db,
			afterDurable: func() {
				if _, metadataErr := store.SetStartMetadata(StartMetadataUpdate{
					FlowID: "create-claimed", BaseRef: "release",
				}); metadataErr != nil {
					t.Errorf("SetStartMetadata() during acknowledgement failure = %v", metadataErr)
				}
			},
		}, nil
	}

	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "create-claimed", Title: "Claimed", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, CreateOptions{})
	if err == nil || finalizer != nil || flow.FlowID != "" {
		t.Fatalf("CreatePreparation() = %#v, %v, %v; want ambiguous insert error without adopting claimant", flow, finalizer, err)
	}
	authoritative, readErr := store.Read("create-claimed")
	if readErr != nil || authoritative.BaseRef != "release" {
		t.Fatalf("claimed preparation = %#v, %v", authoritative, readErr)
	}
}

func TestPreparationCreateDoesNotAdoptPhaseStateClaimAfterAmbiguousCommit(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	original := backend.beginTx
	backend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
		tx, beginErr := original(ctx)
		if beginErr != nil {
			return nil, beginErr
		}
		backend.beginTx = original
		return &injectedCommitTx{
			sqliteTransaction: tx,
			mode:              injectedCommitDurable,
			db:                backend.db,
			afterDurable: func() {
				if _, phaseErr := store.SetPhase(PhaseUpdate{
					FlowID: "create-phase-claimed", PhaseID: "plan", Status: PhaseBlocked, Notes: "claimed elsewhere",
				}); phaseErr != nil {
					t.Errorf("SetPhase() during acknowledgement failure = %v", phaseErr)
				}
			},
		}, nil
	}

	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "create-phase-claimed", Title: "Claimed", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, CreateOptions{})
	if err == nil || finalizer != nil || flow.FlowID != "" {
		t.Fatalf("CreatePreparation() = %#v, %v, %v; want ambiguous insert error without adopting phase claim", flow, finalizer, err)
	}
	authoritative, readErr := store.Read("create-phase-claimed")
	if readErr != nil || authoritative.Phases[0].Status != PhaseBlocked {
		t.Fatalf("phase-claimed preparation = %#v, %v", authoritative, readErr)
	}
}

func TestPreparationCompensationRetriesAbsentCommitForPendingCustomRoot(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "pending-root", Title: "Pending root", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
		Phases: []FlowPhase{{
			PhaseID: "custom", Title: "Custom", Kind: KindImplementation, Status: PhasePending, DependsOn: []string{},
		}},
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if flow.Phases[0].Status != PhasePending {
		t.Fatalf("declared root status = %q, want persisted pending precondition", flow.Phases[0].Status)
	}
	injectNextCommitOutcome(store, injectedCommitAbsent)
	compensated, err := finalizer.Compensate("creation canceled")
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	if compensated.Phases[0].Status != PhaseBlocked {
		t.Fatalf("Compensate() phase = %#v, want blocked after retry", compensated.Phases[0])
	}
}

func TestPreparationCompensationReconcilesCommitAcknowledgementFailures(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mode        injectedCommitMode
		wantUnknown bool
	}{
		{name: "confirmed absent retries", mode: injectedCommitAbsent},
		{name: "confirmed durable", mode: injectedCommitDurable},
		{name: "readback unavailable", mode: injectedCommitUnreadable, wantUnknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			flow, finalizer, err := store.CreatePreparation(FlowRecord{
				FlowID: "compensation-outcome", Title: "Compensation", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
			}, CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			injectNextCommitOutcome(store, tt.mode)
			compensated, err := finalizer.Compensate("creation canceled")
			if tt.wantUnknown {
				if !IsPreparationUnknown(err) {
					t.Fatalf("Compensate() error = %v, want unknown classification", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compensate() error = %v", err)
			}
			if compensated.FlowID != flow.FlowID {
				t.Fatalf("Compensate() flow = %q, want %q", compensated.FlowID, flow.FlowID)
			}
			if _, _, ok := FirstLaunchablePhase(compensated); ok {
				t.Fatalf("Compensate() left a launchable root after commit acknowledgement failure: %#v", compensated)
			}
		})
	}
}

func TestPreparedEpicProgressionReturnsTransactionFlowOnUnknownCommit(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mode        injectedCommitMode
		wantEnabled bool
	}{
		{name: "confirmed absent", mode: injectedCommitAbsent},
		{name: "confirmed durable", mode: injectedCommitDurable, wantEnabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			repo := filepath.Join(t.TempDir(), "repo")
			link := BeadLink{ID: "epic.1", EpicID: "epic"}
			flow, finalizer, err := store.CreatePreparation(FlowRecord{
				FlowID: "atomic-outcome", Title: "Atomic", Instructions: "Test.", RepoPath: repo, Bead: link,
			}, CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetStartMetadata(StartMetadataUpdate{
				FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/atomic", PlanID: "fresh-plan",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := finalizer.Finalize(nil); err != nil {
				t.Fatal(err)
			}
			injectNextCommitOutcome(store, tt.mode)
			_, transactionFlow, err := store.EnableEpicProgressionForPreparedFlow(PreparedEpicProgressionUpdate{
				FlowID: flow.FlowID, Key: EpicProgressionKey{RepoPath: repo, EpicID: "epic"}, Bead: link,
			})
			if !IsPreparedEpicProgressionCommitUnknown(err) {
				t.Fatalf("EnableEpicProgressionForPreparedFlow() error = %v, want commit-unknown classification", err)
			}
			if transactionFlow.FlowID != flow.FlowID || transactionFlow.PlanID != "fresh-plan" || transactionFlow.PreparedAt == nil {
				t.Fatalf("transaction Flow = %#v, want authoritative prepared snapshot", transactionFlow)
			}
			progression, found, readErr := store.ReadEpicProgression(EpicProgressionKey{RepoPath: repo, EpicID: "epic"})
			if readErr != nil || (found && progression.Enabled) != tt.wantEnabled {
				t.Fatalf("readback = %#v, found %t, err %v; want enabled %t", progression, found, readErr, tt.wantEnabled)
			}
		})
	}
}
