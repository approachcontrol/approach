package flowstore_test

import (
	"database/sql"
	"path/filepath"
	"strings"
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
