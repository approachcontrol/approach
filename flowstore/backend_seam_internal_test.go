package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	_ backend         = (*sqliteBackend)(nil)
	_ flowSession     = sqliteSession{}
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

	got, err := store.SetPhase(PhaseUpdate{FlowID: record.FlowID, PhaseID: "plan", Status: PhaseCompleted})
	if err == nil {
		t.Fatal("SetPhase() error = nil, want sync failure")
	}
	if !strings.Contains(err.Error(), "sync linked plan phase: plan write exploded") {
		t.Fatalf("SetPhase() error = %v, want sync linked plan phase wrapping", err)
	}
	// Half of the asymmetry pinned by clause 3 on backend.update: SetPhase
	// discards the record on a sync failure even though the compensation was
	// persisted. TestManualMergeSyncFailureReturnsCompensatedRecord pins the
	// other half. Nothing else in the suite looks at this return value, so
	// without this assertion the seam could start returning the record here and
	// stay green.
	if got.FlowID != "" {
		t.Fatalf("SetPhase() record = %+v, want the zero record beside the error", got)
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

// TestManualMergeSyncFailureReturnsCompensatedRecord pins the other half of the
// clause 3 asymmetry: unlike SetPhase, MarkManualMerge hands back the
// compensated record beside the sync error, with the PR and merge status rolled
// back to what they were before the merge was recorded.
func TestManualMergeSyncFailureReturnsCompensatedRecord(t *testing.T) {
	syncer := &fakePlanSyncer{writeErr: errors.New("plan write exploded")}
	store, record := newMergeReadySeamFlow(t, syncer)

	mergedAt := time.Date(2026, 5, 6, 9, 0, 0, 0, time.UTC)
	got, err := store.MarkManualMerge(ManualMergeUpdate{
		FlowID:   record.FlowID,
		PRNumber: record.PR.Number,
		PRURL:    record.PR.URL,
		Commit:   "deadbeef",
		MergedAt: mergedAt,
	})
	if err == nil {
		t.Fatal("MarkManualMerge() error = nil, want sync failure")
	}
	if got.FlowID != record.FlowID {
		t.Fatalf("MarkManualMerge() record = %+v, want the compensated record beside the error", got)
	}
	if got.PR.Status == MergeMerged {
		t.Fatalf("PR status = %q, want the pre-merge status restored", got.PR.Status)
	}
	if got.Merge.Status != MergePending {
		t.Fatalf("merge status = %q, want %q", got.Merge.Status, MergePending)
	}
	assertPhaseNeedsAttention(t, store, record.FlowID, "merge")
}

// TestBackendSaveDoesNotTakeTheFlowLock pins the invariant that keeps every
// in-section write from deadlocking: Store.saveSession runs inside the closure
// backend.update is holding the lock for, and routes through backend.save, so
// save must never acquire that lock itself. The flock is not re-entrant, so a
// second acquire would block until it timed out.
func TestBackendSaveDoesNotTakeTheFlowLock(t *testing.T) {
	store, record := newSeamTestFlow(t, &fakePlanSyncer{})

	done := make(chan error, 1)
	go func() {
		_, err := store.backend.update(record.FlowID, func(sess flowSession) (FlowRecord, error) {
			stored, ok, err := sess.get()
			if err != nil {
				return FlowRecord{}, err
			}
			if !ok {
				return FlowRecord{}, errors.New("seeded record missing")
			}
			// The production path: saveSession -> sess.save -> backend.save,
			// all while update holds the critical section.
			return stored.record, store.saveSession(sess, stored.record)
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("save inside update error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("save inside update deadlocked on the flow lock")
	}
	if _, err := store.Read(record.FlowID); err != nil {
		t.Fatalf("Read() after in-section save error = %v", err)
	}
}

func TestSQLiteBackendSatisfiesContract(t *testing.T) {
	testBackendContract(t, func(t *testing.T) backend {
		t.Helper()
		store, err := NewStore(StoreOptions{Root: t.TempDir(), LockTimeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		b := store.backend.(*sqliteBackend)
		t.Cleanup(func() { _ = b.db.Close() })
		return store.backend
	})
}

func TestSQLiteUpdateCommitFailureTakesPrecedence(t *testing.T) {
	commitErr := errors.New("commit exploded")
	rollbackErr := errors.New("rollback also exploded")
	tx := &failingCommitTransaction{commitErr: commitErr, rollbackErr: rollbackErr}
	b := &sqliteBackend{beginTx: func(context.Context) (sqliteTransaction, error) { return tx, nil }}
	callbackErr := errors.New("domain callback failed")
	want := FlowRecord{FlowID: "commit-failure", Title: "must be zeroed"}

	got, err := b.update("commit-failure", func(flowSession) (FlowRecord, error) {
		return want, callbackErr
	})
	if !reflect.DeepEqual(got, FlowRecord{}) {
		t.Fatalf("update() record = %#v, want zero because durability is unknown", got)
	}
	if !errors.Is(err, commitErr) || errors.Is(err, callbackErr) {
		t.Fatalf("update() error = %v, want commit classification only", err)
	}
	for _, context := range []string{"callback error: domain callback failed", "rollback error: rollback also exploded"} {
		if !strings.Contains(err.Error(), context) {
			t.Fatalf("update() error = %v, want context %q", err, context)
		}
	}
	if !tx.rollbackCalled {
		t.Fatal("commit failure did not attempt rollback")
	}
	if IsNotFound(err) || IsAutoLaunchOutdated(err) {
		t.Fatalf("commit failure was misclassified as a domain error: %v", err)
	}
}

func TestSQLiteUpdateBeginFailureDoesNotInvokeCallback(t *testing.T) {
	beginErr := errors.New("begin exploded")
	b := &sqliteBackend{beginTx: func(context.Context) (sqliteTransaction, error) { return nil, beginErr }}
	calls := 0
	_, err := b.update("begin-failure", func(flowSession) (FlowRecord, error) {
		calls++
		return FlowRecord{}, nil
	})
	if !errors.Is(err, beginErr) || calls != 0 {
		t.Fatalf("update() error/calls = %v/%d, want begin error and zero callback calls", err, calls)
	}
}

func TestSQLitePrimaryKeyArbitratesAutomaticIDRaceWithoutRetry(t *testing.T) {
	store, err := NewStore(StoreOptions{Root: t.TempDir(), LockTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	b := store.backend.(*sqliteBackend)
	t.Cleanup(func() { _ = b.db.Close() })
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	firstID, err := b.allocateID("Racing Flow", now)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := b.allocateID("Racing Flow", now)
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("pre-write allocations = %q/%q, want the race candidate to match", firstID, secondID)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	callbackCalls := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(worker int) {
			<-start
			calls := 0
			_, err := b.update(firstID, func(sess flowSession) (FlowRecord, error) {
				calls++
				exists, err := sess.exists()
				if err != nil {
					return FlowRecord{}, err
				}
				if exists {
					return FlowRecord{}, fmt.Errorf("flow %q already exists", firstID)
				}
				record := FlowRecord{
					SchemaVersion: schemaVersion, FlowID: firstID, Title: fmt.Sprintf("worker-%d", worker),
					Status: StatusPending, RepoPath: "/tmp/repo", Phases: []FlowPhase{}, CreatedAt: now, UpdatedAt: now,
				}
				return record, sess.save(record)
			})
			callbackCalls <- calls
			results <- err
		}(i)
	}
	close(start)
	successes, collisions := 0, 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else if strings.Contains(err.Error(), "already exists") {
			collisions++
		} else {
			t.Fatalf("race error = %v", err)
		}
		if calls := <-callbackCalls; calls != 1 {
			t.Fatalf("worker callback calls = %d, want exactly one", calls)
		}
	}
	if successes != 1 || collisions != 1 {
		t.Fatalf("race results = %d successes/%d collisions, want 1/1", successes, collisions)
	}
}

type failingCommitTransaction struct {
	commitErr      error
	rollbackErr    error
	rollbackCalled bool
}

func (t *failingCommitTransaction) QueryRow(string, ...any) *sql.Row { return nil }
func (t *failingCommitTransaction) Exec(string, ...any) (sql.Result, error) {
	return nil, errors.New("unexpected Exec")
}
func (t *failingCommitTransaction) Commit() error { return t.commitErr }
func (t *failingCommitTransaction) Rollback() error {
	t.rollbackCalled = true
	return t.rollbackErr
}

// testBackendContract asserts the clauses documented on the backend interface.
// It is implementation-agnostic on purpose: it touches no file paths and makes
// no assumption about encoding.
func testBackendContract(t *testing.T, newBackend func(t *testing.T) backend) {
	t.Helper()

	// save goes through update because the seam exposes no lock-free write:
	// flowSession.save is the only way in.
	save := func(t *testing.T, b backend, record FlowRecord) {
		t.Helper()
		if _, err := b.update(record.FlowID, func(sess flowSession) (FlowRecord, error) {
			return record, sess.save(record)
		}); err != nil {
			t.Fatalf("save(%q) error = %v", record.FlowID, err)
		}
	}

	seed := func(t *testing.T, b backend, flowID string) FlowRecord {
		t.Helper()
		record := FlowRecord{
			SchemaVersion: schemaVersion,
			FlowID:        flowID,
			Title:         "Contract",
			Status:        StatusPending,
			RepoPath:      "/tmp/repo",
			Phases:        defaultPhases(time.Unix(0, 0).UTC(), time.Unix(0, 0).UTC()),
			CreatedAt:     time.Unix(0, 0).UTC(),
			UpdatedAt:     time.Unix(0, 0).UTC(),
		}
		save(t, b, record)
		return record
	}

	t.Run("get distinguishes absence from invalid input", func(t *testing.T) {
		b := newBackend(t)
		if _, ok, err := b.get("absent-flow"); err != nil || ok {
			t.Fatal("get() on an absent flow = true, want a miss")
		}
		if _, _, err := b.get("../escape"); err == nil {
			t.Fatal("get() on an invalid id error = nil")
		}
	})

	// The schema-version filter is what makes a record written by a future
	// version invisible instead of half-decoded. It lives below the seam and
	// nothing above repeats it, so every backend owes it.
	t.Run("get rejects a foreign schema version", func(t *testing.T) {
		b := newBackend(t)
		record := seed(t, b, "future-schema")
		record.SchemaVersion = schemaVersion + 1
		save(t, b, record)
		if _, _, err := b.get("future-schema"); err == nil {
			t.Fatal("get() on a future schema version error = nil")
		}
		if _, err := b.list(FlowFilter{}); err == nil {
			t.Fatal("list() error = nil for a foreign schema version")
		}
	})

	t.Run("save then get round-trips", func(t *testing.T) {
		b := newBackend(t)
		want := seed(t, b, "round-trip")
		got, ok, err := b.get("round-trip")
		if err != nil {
			t.Fatalf("get() error = %v", err)
		}
		if !ok {
			t.Fatal("get() = false, want the saved record")
		}
		if got.record.FlowID != want.FlowID || got.record.Title != want.Title {
			t.Fatalf("get() record = %+v, want %+v", got.record, want)
		}
		if len(got.record.Phases) != len(want.Phases) {
			t.Fatalf("get() phases = %d, want %d", len(got.record.Phases), len(want.Phases))
		}
	})

	t.Run("clause 1: mutate is invoked exactly once on success", func(t *testing.T) {
		b := newBackend(t)
		seed(t, b, "once")
		calls := 0
		if _, err := b.update("once", func(sess flowSession) (FlowRecord, error) {
			calls++
			return FlowRecord{}, nil
		}); err != nil {
			t.Fatalf("update() error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("mutate invocations = %d, want exactly 1", calls)
		}
	})

	// A backend that retries would only ever retry a FAILING mutate, so the
	// success case above cannot catch it. This is the case that matters: the
	// Create closure rewrites depends_on through the caller's shared phase
	// array, so a second pass takes a different validation branch.
	t.Run("clause 1: a failing mutate is not retried", func(t *testing.T) {
		b := newBackend(t)
		seed(t, b, "no-retry")
		sentinel := errors.New("mutate refused")
		calls := 0
		_, err := b.update("no-retry", func(sess flowSession) (FlowRecord, error) {
			calls++
			return FlowRecord{}, sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("update() error = %v, want %v", err, sentinel)
		}
		if calls != 1 {
			t.Fatalf("mutate invocations on failure = %d, want exactly 1 (no retry)", calls)
		}
	})

	t.Run("session get observes saves made in the same session", func(t *testing.T) {
		b := newBackend(t)
		record := seed(t, b, "in-section")
		if _, err := b.update("in-section", func(sess flowSession) (FlowRecord, error) {
			// Every production closure opens with sess.get, so it must neither
			// block on the section update is already holding nor return a stale
			// snapshot.
			before, ok, err := sess.get()
			if err != nil {
				t.Fatalf("sess.get() error = %v", err)
			}
			if !ok {
				t.Fatal("sess.get() = false at the top of the section")
			}
			if before.record.Title != record.Title {
				t.Fatalf("sess.get() title = %q, want %q", before.record.Title, record.Title)
			}
			next := record
			next.Title = "Rewritten in section"
			if err := sess.save(next); err != nil {
				t.Fatalf("sess.save() error = %v", err)
			}
			after, ok, err := sess.get()
			if err != nil {
				t.Fatalf("sess.get() error = %v", err)
			}
			if !ok {
				t.Fatal("sess.get() = false after an in-section save")
			}
			if after.record.Title != "Rewritten in section" {
				t.Fatalf("sess.get() title = %q, want the in-section save to be visible", after.record.Title)
			}
			return after.record, nil
		}); err != nil {
			t.Fatalf("update() error = %v", err)
		}
	})

	t.Run("clause 5: updates to one flow are mutually exclusive", func(t *testing.T) {
		b := newBackend(t)
		seed(t, b, "contended")
		var mu sync.Mutex
		inside, overlaps := 0, 0
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = b.update("contended", func(sess flowSession) (FlowRecord, error) {
					mu.Lock()
					inside++
					if inside > 1 {
						overlaps++
					}
					mu.Unlock()
					// Wide enough that unsynchronized sections would overlap.
					time.Sleep(20 * time.Millisecond)
					mu.Lock()
					inside--
					mu.Unlock()
					return FlowRecord{}, nil
				})
			}()
		}
		wg.Wait()
		if overlaps != 0 {
			t.Fatalf("observed %d overlapping critical sections, want 0", overlaps)
		}
	})

	t.Run("clause 5: writer acquisition serializes different flows before callback", func(t *testing.T) {
		b := newBackend(t)
		seed(t, b, "alpha-flow")
		seed(t, b, "beta-flow")
		release := make(chan struct{})
		holding := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := b.update("alpha-flow", func(sess flowSession) (FlowRecord, error) {
				close(holding)
				<-release
				return FlowRecord{}, nil
			})
			done <- err
		}()
		<-holding
		second := make(chan error, 1)
		secondCalls := 0
		go func() {
			_, err := b.update("beta-flow", func(sess flowSession) (FlowRecord, error) {
				secondCalls++
				return FlowRecord{}, nil
			})
			second <- err
		}()
		select {
		case err := <-second:
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
				t.Fatalf("contended update error = %v, want SQLite locked/busy failure", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("contended update did not honor the bounded busy timeout")
		}
		if secondCalls != 0 {
			t.Fatalf("contended callback calls = %d, want 0 because acquisition failed first", secondCalls)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("update() error = %v", err)
		}
	})

	t.Run("clause 2: saves survive a mutate error", func(t *testing.T) {
		b := newBackend(t)
		record := seed(t, b, "durable")
		sentinel := errors.New("mutate refused")
		_, err := b.update("durable", func(sess flowSession) (FlowRecord, error) {
			saved := record
			saved.Title = "Durable after error"
			if err := sess.save(saved); err != nil {
				return FlowRecord{}, err
			}
			return FlowRecord{}, sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("update() error = %v, want %v", err, sentinel)
		}
		got, ok, err := b.get("durable")
		if err != nil {
			t.Fatalf("get() error = %v", err)
		}
		if !ok {
			t.Fatal("get() = false, want the record the failed mutate saved")
		}
		if got.record.Title != "Durable after error" {
			t.Fatalf("title = %q, want the save to survive the mutate error", got.record.Title)
		}
	})

	t.Run("clause 3: record and error come back verbatim", func(t *testing.T) {
		b := newBackend(t)
		seed(t, b, "verbatim")
		sentinel := errors.New("verbatim sentinel")
		want := FlowRecord{FlowID: "verbatim", Title: "Marker"}
		got, err := b.update("verbatim", func(sess flowSession) (FlowRecord, error) {
			return want, sentinel
		})
		if !errors.Is(err, sentinel) || err.Error() != sentinel.Error() {
			t.Fatalf("update() error = %v, want %v unwrapped and unrewritten", err, sentinel)
		}
		if got.FlowID != want.FlowID || got.Title != want.Title {
			t.Fatalf("update() record = %+v, want %+v returned beside the error", got, want)
		}
	})

	t.Run("clause 4: save may be called zero or many times", func(t *testing.T) {
		b := newBackend(t)
		record := seed(t, b, "many")
		if _, err := b.update("many", func(sess flowSession) (FlowRecord, error) {
			return FlowRecord{}, nil
		}); err != nil {
			t.Fatalf("update() with zero saves error = %v", err)
		}
		if _, err := b.update("many", func(sess flowSession) (FlowRecord, error) {
			for i, title := range []string{"first", "second", "third"} {
				saved := record
				saved.Title = title
				if err := sess.save(saved); err != nil {
					t.Fatalf("save %d error = %v", i, err)
				}
			}
			return FlowRecord{}, nil
		}); err != nil {
			t.Fatalf("update() with three saves error = %v", err)
		}
		got, ok, err := b.get("many")
		if err != nil {
			t.Fatalf("get() error = %v", err)
		}
		if !ok {
			t.Fatal("get() = false, want the last saved record")
		}
		if got.record.Title != "third" {
			t.Fatalf("title = %q, want the last save to win", got.record.Title)
		}
	})

	t.Run("session exists reflects the record", func(t *testing.T) {
		b := newBackend(t)
		if _, err := b.update("later", func(sess flowSession) (FlowRecord, error) {
			if exists, err := sess.exists(); err != nil {
				t.Fatalf("exists() error = %v", err)
			} else if exists {
				t.Fatal("exists() = true before any save")
			}
			return FlowRecord{}, nil
		}); err != nil {
			t.Fatalf("update() error = %v", err)
		}
		seed(t, b, "later")
		if _, err := b.update("later", func(sess flowSession) (FlowRecord, error) {
			if exists, err := sess.exists(); err != nil {
				t.Fatalf("exists() error = %v", err)
			} else if !exists {
				t.Fatal("exists() = false after a save")
			}
			return FlowRecord{}, nil
		}); err != nil {
			t.Fatalf("update() error = %v", err)
		}
	})

	t.Run("delete reports a missing record as not found", func(t *testing.T) {
		b := newBackend(t)
		err := b.delete("absent-flow")
		if !errors.Is(err, errFlowNotFound) {
			t.Fatalf("delete() error = %v, want errFlowNotFound", err)
		}
		seed(t, b, "doomed")
		if err := b.delete("doomed"); err != nil {
			t.Fatalf("delete() error = %v", err)
		}
		if _, ok, err := b.get("doomed"); err != nil || ok {
			t.Fatal("get() = true after delete, want a miss")
		}
	})

	t.Run("list returns every saved record", func(t *testing.T) {
		b := newBackend(t)
		if got, err := b.list(FlowFilter{}); err != nil {
			t.Fatalf("list() error = %v", err)
		} else if len(got) != 0 {
			t.Fatalf("list() = %d records on an empty store, want 0", len(got))
		}
		seed(t, b, "alpha")
		seed(t, b, "beta")
		got, err := b.list(FlowFilter{})
		if err != nil {
			t.Fatalf("list() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("list() = %d records, want 2", len(got))
		}
		seen := map[string]bool{}
		for _, flow := range got {
			seen[flow.record.FlowID] = true
		}
		if !seen["alpha"] || !seen["beta"] {
			t.Fatalf("list() ids = %v, want alpha and beta", seen)
		}
	})

	// Store.List sorts stably on UpdatedAt, so records with equal timestamps
	// come back in whatever order list() produced. That makes list()'s order a
	// contract, not an implementation detail: it must at least be stable across
	// calls, or equal-timestamp ties would shuffle between reads.
	t.Run("list order is stable across calls", func(t *testing.T) {
		b := newBackend(t)
		for _, id := range []string{"tie-a", "tie-b", "tie-c", "tie-d"} {
			seed(t, b, id)
		}
		first, err := b.list(FlowFilter{})
		if err != nil {
			t.Fatalf("list() error = %v", err)
		}
		order := func(flows []storedFlow) []string {
			ids := make([]string, 0, len(flows))
			for _, flow := range flows {
				ids = append(ids, flow.record.FlowID)
			}
			return ids
		}
		want := order(first)
		for i := 0; i < 3; i++ {
			again, err := b.list(FlowFilter{})
			if err != nil {
				t.Fatalf("list() error = %v", err)
			}
			got := order(again)
			if len(got) != len(want) {
				t.Fatalf("list() = %d records, want %d", len(got), len(want))
			}
			for j := range want {
				if got[j] != want[j] {
					t.Fatalf("list() order = %v, want %v (equal-UpdatedAt ties must not shuffle)", got, want)
				}
			}
		}
	})

	t.Run("allocateID returns a usable distinct id", func(t *testing.T) {
		b := newBackend(t)
		now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
		first, err := b.allocateID("Contract Flow", now)
		if err != nil {
			t.Fatalf("allocateID() error = %v", err)
		}
		if err := validateFlowID(first); err != nil {
			t.Fatalf("allocateID() returned an id the store rejects: %v", err)
		}
		seed(t, b, first)
		second, err := b.allocateID("Contract Flow", now)
		if err != nil {
			t.Fatalf("allocateID() error = %v", err)
		}
		if second == first {
			t.Fatalf("allocateID() = %q twice, want a distinct id once the first is taken", second)
		}
	})
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

// newMergeReadySeamFlow drives a Flow all the way to a ready merge phase with a
// verified PR and a linked plan, which is the only state from which
// MarkManualMerge reaches the linked-plan sync.
func newMergeReadySeamFlow(t *testing.T, syncer planPhaseSyncer) (*Store, FlowRecord) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(FlowRecord{
		Title:        "Merge seam",
		Instructions: "Drive a Flow to the merge phase.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID:       record.FlowID,
		Branch:       "feat/seam",
		WorktreePath: filepath.Join(root, "wt"),
		BaseRef:      "main",
	}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	prURL := "https://github.com/o/r/pull/42"
	if _, err := store.SetPR(PRUpdate{
		FlowID: record.FlowID, Provider: "github", Number: 42,
		URL: prURL, HeadBranch: "feat/seam", BaseBranch: "main", Status: "open",
	}); err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	for _, step := range []struct{ id, outcome string }{
		{"plan", ""},
		{"plan-review", "approved"},
		{"implementation", ""},
		{"review-loop", "approved"},
		{"pr-creation", ""},
		{"autoreview", "approved"},
	} {
		if _, err := store.SetPhase(PhaseUpdate{
			FlowID: record.FlowID, PhaseID: step.id, Status: PhaseCompleted, Outcome: step.outcome,
		}); err != nil {
			t.Fatalf("SetPhase(%s) error = %v", step.id, err)
		}
	}
	// Linked last, and the syncer injected last, so only the merge phase's sync
	// goes through the fake.
	record, err = store.SetStartMetadata(StartMetadataUpdate{FlowID: record.FlowID, PlanID: "seam-plan"})
	if err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	store.planSync = syncer
	return store, record
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
