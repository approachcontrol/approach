package model

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

// openDescriptorCount counts the descriptors this process holds by probing each
// number with F_GETFD. /proc is unavailable on macOS and /dev/fd entries there
// are not symlinks, so neither can be enumerated; the "open /dev/null and read
// its number" trick does not work either, because it reports the lowest free
// descriptor and a hole low in the table hides all growth above it.
func openDescriptorCount() int {
	const probeLimit = 2048
	open := 0
	for fd := 0; fd < probeLimit; fd++ {
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0); errno == 0 {
			open++
		}
	}
	return open
}

// seedFlow creates one Flow through a Store the test owns and closes, so the
// only Flow-database handles left behind belong to the Model's fallback store.
func seedFlow(t *testing.T, root string) flowstore.FlowRecord {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "reuse",
		Instructions: "prove the fallback store is shared",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return record
}

// A flowstore.Store owns a pooled SQLite handle for its whole life. The fallback
// mutators in NewWithOptions used to build a fresh Store per operation, which
// was free when the backend was plain files and leaks roughly two descriptors
// per Flow write now that it is SQLite. A long-running TUI driving auto-advance
// phase launches would exhaust the process's descriptors and then fail at
// everything, not just Flows.
//
// The tolerance is deliberately loose: the point is to separate "bounded" from
// "grows with every write", not to pin an exact number. Per-operation stores
// would add roughly two descriptors per iteration, far outside it.
func TestFlowStoreIsReusedAcrossFallbackMutations(t *testing.T) {
	const writes = 50
	const tolerance = 8

	root := t.TempDir()
	record := seedFlow(t, root)
	m := newModelForTest(nil, Options{SessionStateRoot: root})

	// The first write opens the Model's memoized store lazily; measure after it
	// so the baseline already includes the shared store's own descriptors.
	if _, err := m.setFlowAutoMode(flowstore.AutoModeUpdate{FlowID: record.FlowID, Enabled: true}); err != nil {
		t.Fatalf("setFlowAutoMode() error = %v", err)
	}
	baseline := openDescriptorCount()

	for i := 0; i < writes; i++ {
		if _, err := m.setFlowAutoMode(flowstore.AutoModeUpdate{
			FlowID:  record.FlowID,
			Enabled: i%2 == 0,
		}); err != nil {
			t.Fatalf("setFlowAutoMode(%d) error = %v", i, err)
		}
	}

	if grew := openDescriptorCount() - baseline; grew > tolerance {
		t.Fatalf("open descriptors grew by %d across %d fallback writes (tolerance %d); "+
			"the fallback mutators are building a flowstore.Store per operation instead of sharing one",
			grew, writes, tolerance)
	}
}

// The memoized store must be shared between distinct fallback mutators, not one
// per closure.
//
// Repeating ONE mutator cannot show this: a per-closure cache is still bounded,
// so it would sit inside any sane tolerance and the test would pass under the
// exact bug it is named for. The signal is the number of DISTINCT mutators
// exercised after the baseline — each one that opens its own Store costs
// another handle, while a shared store costs nothing.
func TestFlowStoreIsSharedBetweenDistinctFallbackMutators(t *testing.T) {
	// Measured: each additional Store that has served a write costs ~2-3
	// descriptors, so the five distinct mutators below cost ~12 if they do not
	// share. A shared store costs ~0. 6 sits clear of both.
	const tolerance = 6

	root := t.TempDir()
	record := seedFlow(t, root)
	m := newModelForTest(nil, Options{SessionStateRoot: root})

	// One mutator first, so the baseline already carries the shared store.
	if _, err := m.setFlowAutoMode(flowstore.AutoModeUpdate{FlowID: record.FlowID, Enabled: true}); err != nil {
		t.Fatalf("setFlowAutoMode() error = %v", err)
	}
	baseline := openDescriptorCount()

	// Every one of these reaches newFlowStore through a different closure. Errors
	// are tolerated on purpose: a rejected update still had to resolve a Store to
	// reach its validation, which is the only thing under test here.
	phaseID := record.Phases[0].PhaseID
	if _, err := m.setFlowHeadless(flowstore.HeadlessUpdate{FlowID: record.FlowID, Enabled: false}); err != nil {
		t.Fatalf("setFlowHeadless() error = %v", err)
	}
	if _, err := m.setFlowPhase(flowstore.PhaseUpdate{
		FlowID: record.FlowID, PhaseID: phaseID, Status: flowstore.PhaseRunning,
	}); err != nil {
		t.Fatalf("setFlowPhase() error = %v", err)
	}
	_, _ = m.addFlowPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: record.FlowID, PhaseID: phaseID, LaunchID: "launch-1",
	})
	_, _ = m.resetFlowPhase(flowstore.PhaseResetUpdate{FlowID: record.FlowID, PhaseID: phaseID})
	_, _ = m.markFlowManualMerge(flowstore.ManualMergeUpdate{FlowID: record.FlowID})

	if grew := openDescriptorCount() - baseline; grew > tolerance {
		t.Fatalf("open descriptors grew by %d across 5 distinct fallback mutators (tolerance %d); "+
			"each closure is memoizing its own flowstore.Store instead of sharing one",
			grew, tolerance)
	}
}

// An injected store must be used verbatim: the TUI process already opened one,
// and a second pool against the same approach.db would contend for SQLite's
// single writer through nothing but busy_timeout.
func TestInjectedFlowStoreIsUsedByFallbackMutators(t *testing.T) {
	root := t.TempDir()
	record := seedFlow(t, root)

	injected, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer injected.Close()

	m := newModelForTest(nil, Options{SessionStateRoot: root, FlowStore: injected})
	if _, err := m.setFlowAutoMode(flowstore.AutoModeUpdate{FlowID: record.FlowID, Enabled: false}); err != nil {
		t.Fatalf("setFlowAutoMode() error = %v", err)
	}

	// Closing the injected store must disable the mutators, which is only true if
	// they went through it rather than opening their own.
	if err := injected.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := m.setFlowHeadless(flowstore.HeadlessUpdate{FlowID: record.FlowID, Enabled: false}); err == nil {
		t.Fatal("setFlowHeadless() error = nil after closing the injected store; " +
			"the mutators opened their own Store instead of using Options.FlowStore")
	}
}
