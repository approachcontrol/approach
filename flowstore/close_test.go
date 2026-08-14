package flowstore_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func closeTestRecord(now time.Time) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "20260101T000000Z-close-me",
		Title:        "Close me",
		Instructions: "close it",
		RepoPath:     "/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Status: flowstore.PhaseCompleted, Order: 1},
			{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 2},
		},
	}
}

func TestDeriveStatusClosed(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	closedAt := now.Add(time.Hour)

	base := closeTestRecord(now)
	base.Closed = flowstore.Closure{Reason: "superseded by #42", ClosedAt: &closedAt}
	if got := flowstore.DeriveStatus(base); got != flowstore.StatusClosed {
		t.Fatalf("DeriveStatus(closed) = %q, want closed", got)
	}

	merged := base
	merged.Merge.Status = flowstore.MergeMerged
	if got := flowstore.DeriveStatus(merged); got != flowstore.StatusClosed {
		t.Fatalf("DeriveStatus(closed+merged) = %q, want closed", got)
	}

	blocked := closeTestRecord(now)
	blocked.Closed = flowstore.Closure{Reason: "dead end", ClosedAt: &closedAt}
	blocked.Phases[1].Status = flowstore.PhaseBlocked
	if got := flowstore.DeriveStatus(blocked); got != flowstore.StatusClosed {
		t.Fatalf("DeriveStatus(closed+blocked phase) = %q, want closed", got)
	}

	abandoned := base
	abandoned.Status = flowstore.StatusAbandoned
	if got := flowstore.DeriveStatus(abandoned); got != flowstore.StatusClosed {
		t.Fatalf("DeriveStatus(closed+abandoned) = %q, want closed", got)
	}
}

func TestDeriveStatusIgnoresReasonWithoutTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	record := closeTestRecord(now)
	record.Closed = flowstore.Closure{Reason: "reason without a timestamp"}
	if got := flowstore.DeriveStatus(record); got != flowstore.StatusInProgress {
		t.Fatalf("DeriveStatus(reason only) = %q, want in_progress", got)
	}
}

func TestFlowClosedKeysOnTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	record := closeTestRecord(now)
	if flowstore.FlowClosed(record) {
		t.Fatal("FlowClosed(open record) = true, want false")
	}
	record.Closed = flowstore.Closure{Reason: "only a reason"}
	if flowstore.FlowClosed(record) {
		t.Fatal("FlowClosed(reason without timestamp) = true, want false")
	}
	record.Closed.ClosedAt = &now
	if !flowstore.FlowClosed(record) {
		t.Fatal("FlowClosed(closed record) = false, want true")
	}
}

func newCloseTestStore(t *testing.T, now *time.Time) (*flowstore.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store, root
}

func createCloseTestFlow(t *testing.T, store *flowstore.Store, root string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Closable Flow",
		Instructions: "Close this Flow.",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return record
}

func TestCloseFlowPersistsReasonAndTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)

	now = now.Add(time.Minute)
	closed, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "  superseded by #42  "})
	if err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}
	if closed.Closed.Reason != "superseded by #42" {
		t.Fatalf("Closed.Reason = %q, want trimmed reason", closed.Closed.Reason)
	}
	if closed.Closed.ClosedAt == nil || !closed.Closed.ClosedAt.Equal(now) {
		t.Fatalf("Closed.ClosedAt = %v, want %s", closed.Closed.ClosedAt, now)
	}
	if !closed.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %s, want %s", closed.UpdatedAt, now)
	}
	if closed.Status != flowstore.StatusClosed {
		t.Fatalf("Status = %q, want closed", closed.Status)
	}
	if len(closed.Phases) != len(created.Phases) {
		t.Fatalf("phase count = %d, want %d", len(closed.Phases), len(created.Phases))
	}
	for i := range closed.Phases {
		if closed.Phases[i].Status != created.Phases[i].Status {
			t.Fatalf("phase %q status = %q, want unchanged %q",
				closed.Phases[i].PhaseID, closed.Phases[i].Status, created.Phases[i].Status)
		}
	}

	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Closed.Reason != "superseded by #42" || read.Closed.ClosedAt == nil || !read.Closed.ClosedAt.Equal(now) {
		t.Fatalf("round-tripped closure = %#v, want reason and timestamp", read.Closed)
	}
	if read.Status != flowstore.StatusClosed {
		t.Fatalf("round-tripped Status = %q, want closed", read.Status)
	}
}

func TestCloseFlowRejectsBlankReason(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)

	for _, reason := range []string{"", "   "} {
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: reason}); err == nil {
			t.Fatalf("CloseFlow(%q) error = nil, want a rejection", reason)
		} else if !strings.Contains(err.Error(), "reason") {
			t.Fatalf("CloseFlow(%q) error = %v, want it to mention the reason", reason, err)
		}
		read, err := store.Read(created.FlowID)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if flowstore.FlowClosed(read) {
			t.Fatalf("CloseFlow(%q) closed the record anyway: %#v", reason, read.Closed)
		}
	}
}

func TestCloseFlowRejectsAlreadyClosedFlow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)

	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "first"}); err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}
	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "second"}); err == nil {
		t.Fatal("CloseFlow(already closed) error = nil, want a rejection")
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Closed.Reason != "first" {
		t.Fatalf("Closed.Reason = %q, want the original reason preserved", read.Closed.Reason)
	}
}

func TestCloseFlowRejectsMergedFlow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := mustCreateManualMergeFlow(t, store, root, true, true)

	if _, err := store.MarkManualMerge(flowstore.ManualMergeUpdate{
		FlowID:   created.FlowID,
		PRNumber: 116,
		PRURL:    "https://github.com/approachcontrol/approach/pull/116",
		Commit:   "abc123",
		MergedAt: now,
	}); err != nil {
		t.Fatalf("MarkManualMerge() error = %v", err)
	}
	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "no longer needed"}); err == nil {
		t.Fatal("CloseFlow(merged) error = nil, want a rejection")
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Status != flowstore.StatusMerged {
		t.Fatalf("Status = %q, want merged", read.Status)
	}
}

func TestCloseFlowRejectsAbandonedFlow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	record := closeTestRecord(now)
	record.RepoPath = filepath.Join(root, "repo")
	record.Status = flowstore.StatusAbandoned
	created, err := store.Create(record)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != flowstore.StatusAbandoned {
		t.Fatalf("created status = %q, want abandoned", created.Status)
	}

	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "no longer needed"}); err == nil || !strings.Contains(err.Error(), "abandoned") {
		t.Fatalf("CloseFlow(abandoned) error = %v, want abandoned rejection", err)
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Status != flowstore.StatusAbandoned || flowstore.FlowClosed(read) {
		t.Fatalf("rejected close changed abandoned Flow: %#v", read)
	}
}

func TestCloseFlowUnknownFlow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, _ := newCloseTestStore(t, &now)

	_, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: "20260101T000000Z-missing", Reason: "gone"})
	if err == nil {
		t.Fatal("CloseFlow(unknown) error = nil, want not found")
	}
	if !flowstore.IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false, want true", err)
	}
}

func TestReopenFlowClearsClosure(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)
	priorStatus := created.Status

	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "pausing this"}); err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}

	now = now.Add(time.Hour)
	reopened, err := store.ReopenFlow(created.FlowID)
	if err != nil {
		t.Fatalf("ReopenFlow() error = %v", err)
	}
	if reopened.Closed.Reason != "" || reopened.Closed.ClosedAt != nil {
		t.Fatalf("reopened closure = %#v, want cleared", reopened.Closed)
	}
	if reopened.Status != priorStatus {
		t.Fatalf("reopened Status = %q, want the prior derived status %q", reopened.Status, priorStatus)
	}
	if !reopened.UpdatedAt.Equal(now) {
		t.Fatalf("reopened UpdatedAt = %s, want %s", reopened.UpdatedAt, now)
	}

	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if flowstore.FlowClosed(read) {
		t.Fatalf("round-tripped record still closed: %#v", read.Closed)
	}
}

func TestReopenFlowRejectsOpenFlow(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)

	if _, err := store.ReopenFlow(created.FlowID); err == nil {
		t.Fatal("ReopenFlow(open) error = nil, want a rejection")
	}
}

func TestCloseFlowStatusProjectionMatchesRecord(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)

	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "projection check"}); err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatalf("open approach.db: %v", err)
	}
	defer db.Close()
	var status string
	if err := db.QueryRow("SELECT status FROM flows WHERE flow_id = ?", created.FlowID).Scan(&status); err != nil {
		t.Fatalf("read status projection: %v", err)
	}
	if status != flowstore.StatusClosed {
		t.Fatalf("status projection = %q, want closed", status)
	}
}

func TestCreateRejectsIncompleteClosureMetadata(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name    string
		closure flowstore.Closure
		want    string
	}{
		{
			name:    "timestamp without reason",
			closure: flowstore.Closure{ClosedAt: &now},
			want:    "reason",
		},
		{
			name:    "whitespace reason with timestamp",
			closure: flowstore.Closure{Reason: "   ", ClosedAt: &now},
			want:    "reason",
		},
		{
			name:    "reason without timestamp",
			closure: flowstore.Closure{Reason: "missing timestamp"},
			want:    "timestamp",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, root := newCloseTestStore(t, &now)
			_, err := store.Create(flowstore.FlowRecord{
				Title:        "Malformed closure",
				Instructions: "must be rejected",
				RepoPath:     filepath.Join(root, "repo"),
				Closed:       tc.closure,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestReadRejectsPersistedClosureWithoutReason(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store, root := newCloseTestStore(t, &now)
	created := createCloseTestFlow(t, store, root)
	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: created.FlowID, Reason: "valid before corruption"}); err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	data := readSQLiteRecordForTest(t, root, created.FlowID)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal stored record: %v", err)
	}
	closed, ok := payload["closed"].(map[string]any)
	if !ok {
		t.Fatalf("stored closed payload = %#v, want object", payload["closed"])
	}
	closed["reason"] = "   "
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal malformed record: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatalf("open approach.db: %v", err)
	}
	if _, err := db.Exec("UPDATE flows SET record = ? WHERE flow_id = ?", data, created.FlowID); err != nil {
		db.Close()
		t.Fatalf("corrupt stored record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close approach.db: %v", err)
	}

	store, err = flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, err = store.Read(created.FlowID)
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("Read() error = %v, want malformed closure reason rejection", err)
	}
}

func TestClosedFlowRejectsTerminalMutationsAtStoreBoundary(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	t.Run("manual launch", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Manual launch", Instructions: "reject stale launch", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady, Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed while launch was pending"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "stale-manual"})
		assertClosedMutationRejected(t, err)
		assertNoPhaseLaunchID(t, store, record.FlowID, "implementation")
	})

	t.Run("resume", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Resume", Instructions: "reject stale resume", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseCompleted, Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before resume"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "stale-resume", Resume: true})
		assertClosedMutationRejected(t, err)
		assertNoPhaseLaunchID(t, store, record.FlowID, "implementation")
	})

	t.Run("phase reset", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Reset", Instructions: "reject stale reset", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"stale-launch"}, Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before reset"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: "implementation"})
		assertClosedMutationRejected(t, err)
		read, readErr := store.Read(record.FlowID)
		if readErr != nil {
			t.Fatalf("Read() error = %v", readErr)
		}
		phase := phaseByID(t, read, "implementation")
		if phase.Status != flowstore.PhaseRunning || len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != "stale-launch" {
			t.Fatalf("rejected reset changed phase: %#v", phase)
		}
	})

	t.Run("auto mode arming", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record := createCloseTestFlow(t, store, root)
		if _, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: record.FlowID, Enabled: false}); err != nil {
			t.Fatalf("SetAutoMode(false) error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before stale toggle"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err := store.SetAutoMode(flowstore.AutoModeUpdate{FlowID: record.FlowID, Enabled: true})
		assertClosedMutationRejected(t, err)
		read, readErr := store.Read(record.FlowID)
		if readErr != nil {
			t.Fatalf("Read() error = %v", readErr)
		}
		if read.AutoMode {
			t.Fatal("rejected auto-mode arm enabled auto mode")
		}
	})

	t.Run("manual merge", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record := mustCreateManualMergeFlow(t, store, root, true, true)
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before merge verification returned"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err := store.MarkManualMerge(flowstore.ManualMergeUpdate{
			FlowID: record.FlowID, PRNumber: 116,
			PRURL:  "https://github.com/approachcontrol/approach/pull/116",
			Commit: "abc123", MergedAt: now,
		})
		assertClosedMutationRejected(t, err)
	})

	t.Run("merge metadata", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record := mustCreateManualMergeFlow(t, store, root, true, true)
		var err error
		record, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "merge", Status: flowstore.PhaseCompleted})
		if err != nil {
			t.Fatalf("SetPhase(merge completed) error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before merge metadata returned"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.SetMerge(flowstore.MergeUpdate{FlowID: record.FlowID, Status: flowstore.MergeMerged, Commit: "abc123", MergedAt: now})
		assertClosedMutationRejected(t, err)
	})
}

func assertClosedMutationRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("mutation error = %v, want closed Flow rejection", err)
	}
}

func assertNoPhaseLaunchID(t *testing.T, store *flowstore.Store, flowID, phaseID string) {
	t.Helper()
	read, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if launchIDs := phaseByID(t, read, phaseID).LaunchIDs; len(launchIDs) != 0 {
		t.Fatalf("rejected mutation recorded launch IDs %#v", launchIDs)
	}
}

func TestClosedFlowRejectsRunningPhaseTransitions(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	t.Run("restart", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Restart", Instructions: "reject restart", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseBlocked, Notes: "blocked", Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before restart"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.RestartPhase(flowstore.PhaseRestartUpdate{FlowID: record.FlowID, PhaseID: "implementation", Notes: "retry"})
		assertClosedMutationRejected(t, err)
	})

	t.Run("set running", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Set running", Instructions: "reject running transition", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseNeedsAttention, Notes: "review", Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before agent retry"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		_, err = store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "implementation", Status: flowstore.PhaseRunning, Notes: "retry"})
		assertClosedMutationRejected(t, err)
	})

	t.Run("running agent may finish", func(t *testing.T) {
		store, root := newCloseTestStore(t, &now)
		record, err := store.Create(flowstore.FlowRecord{
			Title: "Finish", Instructions: "allow running agent result", RepoPath: filepath.Join(root, "repo"),
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Order: 1}},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed while agent finishes"}); err != nil {
			t.Fatalf("CloseFlow() error = %v", err)
		}
		updated, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: record.FlowID, PhaseID: "implementation", Status: flowstore.PhaseCompleted, Summary: "finished after close"})
		if err != nil {
			t.Fatalf("SetPhase(completed) error = %v", err)
		}
		if updated.Status != flowstore.StatusClosed || phaseByID(t, updated, "implementation").Status != flowstore.PhaseCompleted {
			t.Fatalf("completed agent result = %#v, want closed Flow with completed phase", updated)
		}
	})
}

func TestRepairLaunchReservationSerializesWithClose(t *testing.T) {
	root := t.TempDir()
	launchStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(launch) error = %v", err)
	}
	closeStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(close) error = %v", err)
	}
	record := createCloseTestFlow(t, launchStore, root)

	reserved, release, err := launchStore.ReserveRepairLaunch(record.FlowID)
	if err != nil {
		t.Fatalf("ReserveRepairLaunch() error = %v", err)
	}
	if reserved.FlowID != record.FlowID || flowstore.FlowClosed(reserved) {
		t.Fatalf("reserved record = %#v, want current open Flow", reserved)
	}

	var once sync.Once
	defer once.Do(release)
	result := make(chan error, 1)
	go func() {
		_, err := closeStore.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "close raced repair"})
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("CloseFlow() returned before repair launch reservation released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("CloseFlow() after release error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseFlow() did not proceed after repair reservation release")
	}

	if _, _, err := launchStore.ReserveRepairLaunch(record.FlowID); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ReserveRepairLaunch(closed) error = %v, want closed rejection", err)
	}
}

func TestAgentLaunchReservationSerializesWithClose(t *testing.T) {
	root := t.TempDir()
	resumeStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(resume) error = %v", err)
	}
	closeStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(close) error = %v", err)
	}
	record := createCloseTestFlow(t, resumeStore, root)

	reserved, release, err := resumeStore.ReserveAgentLaunch(record.FlowID)
	if err != nil {
		t.Fatalf("ReserveAgentLaunch() error = %v", err)
	}
	if reserved.FlowID != record.FlowID || flowstore.FlowClosed(reserved) {
		t.Fatalf("reserved record = %#v, want current open Flow", reserved)
	}

	var once sync.Once
	defer once.Do(release)
	result := make(chan error, 1)
	go func() {
		_, err := closeStore.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "close raced resume"})
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("CloseFlow() returned before resume launch reservation released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("CloseFlow() after release error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseFlow() did not proceed after resume reservation release")
	}

	if _, _, err := resumeStore.ReserveAgentLaunch(record.FlowID); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ReserveAgentLaunch(closed) error = %v, want closed rejection", err)
	}
}

func TestEpicProgressionSuccessorReservationAllowsClosedAndMissingFlows(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := createCloseTestFlow(t, store, root)
	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "closed before reconciliation"}); err != nil {
		t.Fatalf("CloseFlow() error = %v", err)
	}

	for _, flowID := range []string{record.FlowID, "missing-successor"} {
		release, err := store.ReserveEpicProgressionSuccessor(flowID)
		if err != nil {
			t.Fatalf("ReserveEpicProgressionSuccessor(%q) error = %v", flowID, err)
		}
		if release == nil {
			t.Fatalf("ReserveEpicProgressionSuccessor(%q) returned nil release", flowID)
		}
		release()
	}
}

func TestAgentLaunchReservationSerializesWithDeleteAndSameIDRecreate(t *testing.T) {
	root := t.TempDir()
	launchStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(launch) error = %v", err)
	}
	deleteStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore(delete) error = %v", err)
	}
	record := createCloseTestFlow(t, launchStore, root)

	_, release, err := launchStore.ReserveAgentLaunch(record.FlowID)
	if err != nil {
		t.Fatalf("ReserveAgentLaunch() error = %v", err)
	}
	var once sync.Once
	defer once.Do(release)
	result := make(chan error, 1)
	go func() { result <- deleteStore.Delete(record.FlowID) }()
	select {
	case err := <-result:
		t.Fatalf("Delete() returned before launch reservation released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	once.Do(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Delete() after release error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Delete() did not proceed after launch reservation released")
	}
	if _, err := deleteStore.Create(record); err != nil {
		t.Fatalf("same-ID recreate after serialized delete error = %v", err)
	}
}
