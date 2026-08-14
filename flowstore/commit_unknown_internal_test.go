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
	mode injectedCommitMode
	db   *sql.DB
}

func (tx *injectedCommitTx) Commit() error {
	switch tx.mode {
	case injectedCommitDurable:
		if err := tx.sqliteTransaction.Commit(); err != nil {
			return err
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
