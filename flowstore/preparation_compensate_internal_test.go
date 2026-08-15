package flowstore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type sessionFaultBackend struct {
	backend
	getErrs  int
	saveErrs int
}

type sessionFault struct {
	flowSession
	getErrs  *int
	saveErrs *int
}

func (b *sessionFaultBackend) update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error) {
	return b.backend.update(flowID, func(sess flowSession) (FlowRecord, error) {
		return mutate(sessionFault{flowSession: sess, getErrs: &b.getErrs, saveErrs: &b.saveErrs})
	})
}

func (s sessionFault) get() (storedFlow, bool, error) {
	if s.getErrs != nil && *s.getErrs > 0 {
		*s.getErrs--
		return storedFlow{}, false, errors.New("injected session get failure")
	}
	return s.flowSession.get()
}

func (s sessionFault) save(record FlowRecord) error {
	if s.saveErrs != nil && *s.saveErrs > 0 {
		*s.saveErrs--
		return errors.New("injected session save failure")
	}
	return s.flowSession.save(record)
}

func TestPreparationCompensationRetainsCapabilityAfterInTransactionStorageFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		wrap func(*sessionFaultBackend)
	}{
		{name: "session get", wrap: func(b *sessionFaultBackend) { b.getErrs = 2 }},
		{name: "session save", wrap: func(b *sessionFaultBackend) { b.saveErrs = 2 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			created, finalizer, err := store.CreatePreparation(FlowRecord{
				FlowID: "storage-fault", Title: "Storage fault", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
			}, CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			fault := &sessionFaultBackend{backend: store.backend}
			tt.wrap(fault)
			store.backend = fault

			if _, err := finalizer.Compensate("creation canceled"); !IsPreparationIncomplete(err) {
				t.Fatalf("Compensate() error = %v, want incomplete so callers can retry after a storage mutation failure", err)
			}
			authoritative, err := store.Read(created.FlowID)
			if err != nil || authoritative.Phases[0].Status != PhaseReady {
				t.Fatalf("Read() after storage-fault Compensate() = %#v, %v; want unchanged ready root", authoritative, err)
			}

			compensated, err := finalizer.Compensate("creation canceled")
			if err != nil {
				t.Fatalf("retry Compensate() error = %v, want the finalizer retained after the in-transaction storage failure", err)
			}
			if compensated.Phases[0].Status != PhaseBlocked {
				t.Fatalf("retry Compensate() phase = %#v, want blocked", compensated.Phases[0])
			}
		})
	}
}

func TestPreparationCompensationDoesNotRestoreAfterSemanticClaim(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "claimed-storage", Title: "Claimed", Instructions: "Test.", RepoPath: filepath.Join(t.TempDir(), "repo"),
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID: created.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/claimed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Compensate("creation canceled"); err == nil {
		t.Fatal("Compensate() succeeded after a metadata claim")
	}
	if _, err := finalizer.Compensate("creation canceled"); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("retry Compensate() after semantic claim error = %v, want consumed capability", err)
	}
}
