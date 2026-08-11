package flowstore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	_ backend         = (*fileBackend)(nil)
	_ flowTx          = fileTx{}
	_ planPhaseSyncer = planstoreSyncer{}
	_ planPhaseWriter = planstorePhaseWriter{}
)

// fakePlanSyncer replaces the planstore collaborator so the sync boundary can
// be driven without a real plan artifact.
type fakePlanSyncer struct {
	openErr  error
	writeErr error
	opens    int
	calls    []string
}

func (f *fakePlanSyncer) open() (planPhaseWriter, error) {
	f.opens++
	if f.openErr != nil {
		return nil, f.openErr
	}
	return fakePlanWriter{parent: f}, nil
}

type fakePlanWriter struct{ parent *fakePlanSyncer }

func (w fakePlanWriter) markPhaseCompleted(planID, phaseID string) error {
	w.parent.calls = append(w.parent.calls, planID+"/"+phaseID)
	return w.parent.writeErr
}

// newSeamTestFlow creates a Flow with a linked plan id but without a plan
// artifact, so the injected syncer is the only thing the sync path touches.
func newSeamTestFlow(t *testing.T, syncer planPhaseSyncer) (*Store, FlowRecord) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.planSync = syncer
	record, err := store.Create(FlowRecord{
		Title:        "Seam",
		Instructions: "Exercise the storage seam.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = store.SetStartMetadata(StartMetadataUpdate{FlowID: record.FlowID, PlanID: "seam-plan"})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	return store, record
}

func TestInjectedPlanSyncerReceivesCompletedPhase(t *testing.T) {
	syncer := &fakePlanSyncer{}
	store, record, hookCalls := seamStoreWithHook(t, syncer)

	if _, err := store.SetPhase(PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: PhaseCompleted}); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}
	if got := syncer.calls; len(got) != 1 || got[0] != "seam-plan/plan" {
		t.Fatalf("syncer calls = %v, want [seam-plan/plan]", got)
	}
	if *hookCalls != 1 {
		t.Fatalf("beforeLinkedPlanPhaseSync calls = %d, want 1", *hookCalls)
	}
}

func TestInjectedPlanSyncerWriteFailureCompensates(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record, hookCalls := seamStoreWithHook(t, syncer)

	_, err := store.SetPhase(PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: PhaseCompleted})
	if err == nil {
		t.Fatal("SetPhase() error = nil, want sync failure")
	}
	if !strings.Contains(err.Error(), "sync linked plan phase: plan write exploded") {
		t.Fatalf("SetPhase() error = %v, want sync linked plan phase wrapping", err)
	}
	// The write path opens the plan store, fires the hook, and only then writes.
	if *hookCalls != 1 {
		t.Fatalf("beforeLinkedPlanPhaseSync calls = %d, want 1", *hookCalls)
	}
	assertPhaseNeedsAttention(t, store, record.FlowID, "plan")
}

func TestInjectedPlanSyncerOpenFailureCompensatesBeforeHook(t *testing.T) {
	syncer := &fakePlanSyncer{openErr: errors.New("plan store unavailable")}
	store, record, hookCalls := seamStoreWithHook(t, syncer)

	_, err := store.SetPhase(PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: PhaseCompleted})
	if err == nil {
		t.Fatal("SetPhase() error = nil, want open failure")
	}
	if !strings.Contains(err.Error(), "sync linked plan phase: plan store unavailable") {
		t.Fatalf("SetPhase() error = %v, want sync linked plan phase wrapping", err)
	}
	if syncer.opens != 1 {
		t.Fatalf("syncer opens = %d, want 1", syncer.opens)
	}
	// An open failure precedes the hook: the two-phase syncer interface exists
	// precisely so the hook keeps firing between open and write.
	if *hookCalls != 0 {
		t.Fatalf("beforeLinkedPlanPhaseSync calls = %d, want 0 on open failure", *hookCalls)
	}
	assertPhaseNeedsAttention(t, store, record.FlowID, "plan")
}

// TestBackendUpdateSavesRemainDurableAfterMutateError pins clause 1 of the
// backend.update contract: a non-nil error from mutate is an outcome, not a
// rollback signal. The needs_attention compensation in SetPhase and
// MarkManualMerge depends on it, so any future backend must satisfy it too.
func TestBackendUpdateSavesRemainDurableAfterMutateError(t *testing.T) {
	store, record := newSeamTestFlow(t, &fakePlanSyncer{})

	sentinel := errors.New("mutate refused")
	err := store.backend.update(record.FlowID, func(tx flowTx) error {
		stored, ok := tx.get()
		if !ok {
			t.Fatalf("tx.get() = false, want the seeded record")
		}
		saved := stored.record
		saved.Title = "Durable after error"
		if err := tx.save(saved); err != nil {
			t.Fatalf("tx.save() error = %v", err)
		}
		return sentinel
	})
	// Clause 2: the error is returned verbatim, never wrapped or replaced.
	if !errors.Is(err, sentinel) || err.Error() != sentinel.Error() {
		t.Fatalf("update() error = %v, want the mutate error verbatim", err)
	}
	got, readErr := store.Read(record.FlowID)
	if readErr != nil {
		t.Fatalf("Read() error = %v", readErr)
	}
	if got.Title != "Durable after error" {
		t.Fatalf("title = %q, want the save to survive the mutate error", got.Title)
	}
}

func TestBackendSaveDoesNotTakeTheFlowLock(t *testing.T) {
	store, record := newSeamTestFlow(t, &fakePlanSyncer{})

	// Store.write is reachable from inside a held critical section (and from
	// test seeding), so backend.save must never acquire the lock: the flock is
	// not re-entrant and a second acquire would block until it timed out.
	done := make(chan error, 1)
	go func() {
		done <- store.backend.update(record.FlowID, func(tx flowTx) error {
			return store.write(record)
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write inside update error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write inside update deadlocked on the flow lock")
	}
}

func seamStoreWithHook(t *testing.T, syncer *fakePlanSyncer) (*Store, FlowRecord, *int) {
	t.Helper()
	store, record := newSeamTestFlow(t, syncer)
	calls := 0
	store.beforeLinkedPlanPhaseSync = func(planID, phaseID string) {
		if planID != "seam-plan" || phaseID != "plan" {
			t.Errorf("hook boundary = %q/%q, want seam-plan/plan", planID, phaseID)
		}
		calls++
	}
	return store, record, &calls
}

func assertPhaseNeedsAttention(t *testing.T, store *Store, flowID, phaseID string) {
	t.Helper()
	got, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	index := phaseIndexByID(got.Phases, phaseID)
	if index < 0 {
		t.Fatalf("phase %q missing from %#v", phaseID, got.Phases)
	}
	phase := got.Phases[index]
	if phase.Status != PhaseNeedsAttention {
		t.Fatalf("phase %q status = %q, want needs_attention compensation persisted", phaseID, phase.Status)
	}
	if !strings.Contains(phase.Notes, "Linked plan phase sync failed") {
		t.Fatalf("phase %q notes = %q, want the sync failure note", phaseID, phase.Notes)
	}
}
