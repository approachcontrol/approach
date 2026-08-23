package flowstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestAutoMergeOverrideRoundTripsThreeStates(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{Title: "Policy", Instructions: "Test it", RepoPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if record.AutoMerge != nil || flowstore.EffectiveAutoMerge(record, false) || !flowstore.EffectiveAutoMerge(record, true) {
		t.Fatalf("new record policy = %v", record.AutoMerge)
	}
	for _, want := range []*bool{boolPtr(true), boolPtr(false), nil} {
		updated, err := store.SetAutoMerge(flowstore.AutoMergeUpdate{FlowID: record.FlowID, Enabled: want})
		if err != nil {
			t.Fatalf("SetAutoMerge(%v) error = %v", want, err)
		}
		read, err := store.Read(record.FlowID)
		if err != nil {
			t.Fatal(err)
		}
		if !sameBoolPointer(updated.AutoMerge, want) || !sameBoolPointer(read.AutoMerge, want) {
			t.Fatalf("override after %v = updated %v, read %v", want, updated.AutoMerge, read.AutoMerge)
		}
	}
}

func TestAutoMergeCannotBeEnabledOnClosedFlow(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{Title: "Closed", Instructions: "Stay closed", RepoPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: record.FlowID, Reason: "finished elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAutoMerge(flowstore.AutoMergeUpdate{FlowID: record.FlowID, Enabled: boolPtr(true)}); !flowstore.IsFlowClosed(err) {
		t.Fatalf("SetAutoMerge(true) error = %v, want closed Flow refusal", err)
	}
	if _, err := store.SetAutoMerge(flowstore.AutoMergeUpdate{FlowID: record.FlowID, Enabled: boolPtr(false)}); err != nil {
		t.Fatalf("SetAutoMerge(false) cleanup error = %v", err)
	}
}

func TestAutoMergeLaunchWriteRechecksPersistedOverride(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title: "Policy race", Instructions: "Keep the final write authoritative", RepoPath: t.TempDir(),
		Phases: []flowstore.FlowPhase{
			{PhaseID: "review", Title: "Review", Kind: flowstore.KindAutoreview, Status: flowstore.PhaseCompleted, DependsOn: []string{}, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "land", Title: "Land", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, DependsOn: []string{"review"}, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAutoMerge(flowstore.AutoMergeUpdate{FlowID: record.FlowID, Enabled: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	_, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: record.FlowID, PhaseID: "land", LaunchID: "stale-launch",
		AutoLaunch: true, AutoMerge: true, GlobalAutoMerge: true,
	})
	if !errors.Is(err, flowstore.ErrAutoLaunchOutdated) {
		t.Fatalf("AddPhaseLaunchID() error = %v, want ErrAutoLaunchOutdated", err)
	}
	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range read.Phases {
		if phase.PhaseID == "land" && (phase.Status != flowstore.PhaseReady || len(phase.LaunchIDs) != 0) {
			t.Fatalf("stale auto-merge mutated phase: %#v", phase)
		}
	}
}

func TestAutoMergeLaunchWriteAcceptsCustomMergeKindID(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title: "Custom merge", Instructions: "Launch land", RepoPath: t.TempDir(),
		Phases: []flowstore.FlowPhase{{PhaseID: "land", Title: "Land", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, DependsOn: []string{}, Order: 1, CreatedAt: now, UpdatedAt: now}},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: record.FlowID, PhaseID: "land", LaunchID: "merge-launch",
		AutoLaunch: true, AutoMerge: true, GlobalAutoMerge: true,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	if len(updated.Phases) != 1 || updated.Phases[0].Status != flowstore.PhaseRunning || len(updated.Phases[0].LaunchIDs) != 1 {
		t.Fatalf("updated phase = %#v", updated.Phases)
	}
}

func boolPtr(value bool) *bool { return &value }

func sameBoolPointer(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
