package flowstore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func closedGraphRecord(t *testing.T) flowstore.FlowRecord {
	t.Helper()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	record := flowstore.FlowRecord{
		FlowID:       "20260101T000000Z-closed-graph",
		Title:        "Closed graph",
		Instructions: "closed",
		RepoPath:     "/repo",
		CreatedAt:    now,
		UpdatedAt:    now,
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation,
			Status: flowstore.PhaseReady, Order: 1,
		}},
	}
	if !flowstore.PhaseLaunchEligible(record, 0) {
		t.Fatalf("fixture is not launchable before closing: %#v", record)
	}
	record.Closed = flowstore.Closure{Reason: "closed for the test", ClosedAt: &now}
	return record
}

func TestPhaseLaunchEligibleRefusesClosedFlow(t *testing.T) {
	record := closedGraphRecord(t)
	if flowstore.PhaseLaunchEligible(record, 0) {
		t.Fatal("PhaseLaunchEligible(closed) = true, want false")
	}
}

func TestFirstLaunchablePhaseRefusesClosedFlow(t *testing.T) {
	record := closedGraphRecord(t)
	if phase, idx, ok := flowstore.FirstLaunchablePhase(record); ok {
		t.Fatalf("FirstLaunchablePhase(closed) = (%q, %d, true); want no phase", phase.PhaseID, idx)
	}
}

// A close can land while an auto-launch command is already in flight. The
// store-side gate must refuse the launch ID rather than record work on a Flow
// the user just closed.
func TestStoreAddPhaseLaunchIDRejectsAutoLaunchOnClosedFlow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := graphRecord(root, "20260607T120000Z-closed-auto-launch", []flowstore.FlowPhase{
		graphPhase(now, "step-1", flowstore.PhaseReady, []string{}),
	})
	closedAt := now.Add(time.Minute)
	record.Closed = flowstore.Closure{Reason: "closed mid-flight", ClosedAt: &closedAt}
	record.Status = flowstore.StatusClosed
	writeFreshFlowRecordForTest(t, root, record)

	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID:     record.FlowID,
		PhaseID:    "step-1",
		LaunchID:   "launch-after-close",
		AutoLaunch: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("AddPhaseLaunchID(closed) error = %v, want not eligible", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if launchIDs := phaseByID(t, read, "step-1").LaunchIDs; len(launchIDs) != 0 {
		t.Fatalf("closed Flow recorded launch IDs %#v", launchIDs)
	}
}
