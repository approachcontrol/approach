package flowstore_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func newBeadSlotStore(t *testing.T, root string) *flowstore.Store {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func beadSlotCreate(t *testing.T, store *flowstore.Store, repoPath, beadID string, phases []flowstore.FlowPhase) flowstore.FlowRecord {
	t.Helper()
	created, err := store.Create(flowstore.FlowRecord{
		Title:        "Bead slot",
		Instructions: "Occupy the slot.",
		RepoPath:     repoPath,
		Bead:         flowstore.BeadLink{ID: beadID},
		Phases:       phases,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

func TestCreateRefusesSecondFlowForSameRepositoryAndBead(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store := newBeadSlotStore(t, root)

	first := beadSlotCreate(t, store, repoPath, "approach-cwk", nil)

	_, err := store.Create(flowstore.FlowRecord{
		Title: "Duplicate", Instructions: "Should be refused.", RepoPath: repoPath,
		Bead: flowstore.BeadLink{ID: "approach-cwk"},
	})
	if !flowstore.IsBeadFlowActive(err) {
		t.Fatalf("Create() error = %v, want IsBeadFlowActive", err)
	}
	conflict, ok := flowstore.ActiveBeadFlow(err)
	if !ok {
		t.Fatalf("ActiveBeadFlow() ok = false, want the existing Flow")
	}
	if conflict.FlowID != first.FlowID {
		t.Fatalf("ActiveBeadFlow().FlowID = %q, want %q", conflict.FlowID, first.FlowID)
	}
	if !strings.Contains(err.Error(), first.FlowID) || !strings.Contains(err.Error(), "approach-cwk") {
		t.Fatalf("error %q does not name the existing Flow and Bead", err)
	}

	listed, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].FlowID != first.FlowID {
		t.Fatalf("List() = %d flows, want exactly the first one", len(listed))
	}
}

func TestCreatePreparationRefusesDuplicateBeadWithoutWritingAnOrphan(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store := newBeadSlotStore(t, root)

	first := beadSlotCreate(t, store, repoPath, "approach-cwk", nil)

	allocated, err := store.AllocateID("Duplicate")
	if err != nil {
		t.Fatalf("AllocateID() error = %v", err)
	}
	_, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: allocated, Title: "Duplicate", Instructions: "Should be refused.",
		RepoPath: repoPath, Bead: flowstore.BeadLink{ID: "approach-cwk"},
	}, flowstore.CreateOptions{})
	if !flowstore.IsBeadFlowActive(err) {
		t.Fatalf("CreatePreparation() error = %v, want IsBeadFlowActive", err)
	}
	if finalizer != nil {
		t.Fatal("CreatePreparation() returned a finalizer for a refused create")
	}
	conflict, ok := flowstore.ActiveBeadFlow(err)
	if !ok || conflict.FlowID != first.FlowID {
		t.Fatalf("ActiveBeadFlow() = (%q, %v), want (%q, true)", conflict.FlowID, ok, first.FlowID)
	}

	if _, err := store.Read(allocated); !flowstore.IsNotFound(err) {
		t.Fatalf("Read(%q) error = %v, want not found; a refused preparation must leave no row", allocated, err)
	}
	listed, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() = %d flows, want 1", len(listed))
	}
}

func TestCreateAllowsSecondFlowWhenTheFirstIsTerminal(t *testing.T) {
	completedPhases := []flowstore.FlowPhase{
		{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted},
	}

	for _, tt := range []struct {
		name       string
		phases     []flowstore.FlowPhase
		retire     func(t *testing.T, store *flowstore.Store, flowID string)
		wantStatus string
	}{
		{
			name:   "closed",
			phases: nil,
			retire: func(t *testing.T, store *flowstore.Store, flowID string) {
				t.Helper()
				if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: flowID, Reason: "retired"}); err != nil {
					t.Fatalf("CloseFlow() error = %v", err)
				}
			},
			wantStatus: flowstore.StatusClosed,
		},
		{
			name:       "all phases completed",
			phases:     completedPhases,
			wantStatus: flowstore.StatusCompleted,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			repoPath := filepath.Join(root, "repo")
			store := newBeadSlotStore(t, root)

			first := beadSlotCreate(t, store, repoPath, "approach-cwk", tt.phases)
			if tt.retire != nil {
				tt.retire(t, store, first.FlowID)
			}
			read, err := store.Read(first.FlowID)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if got := flowstore.DeriveStatus(read); got != tt.wantStatus {
				t.Fatalf("DeriveStatus() = %q, want %q", got, tt.wantStatus)
			}

			second, err := store.Create(flowstore.FlowRecord{
				Title: "Follow-up", Instructions: "Legitimate follow-up.", RepoPath: repoPath,
				Bead: flowstore.BeadLink{ID: "approach-cwk"},
			})
			if err != nil {
				t.Fatalf("Create() error = %v, want success once the first Flow is %s", err, tt.wantStatus)
			}
			if second.FlowID == first.FlowID {
				t.Fatal("Create() reused the retired Flow ID")
			}
		})
	}
}

func TestCreateRefusesWhenTheExistingFlowIsBlockedOrNeedsAttention(t *testing.T) {
	for _, tt := range []struct {
		name       string
		phaseState flowstore.PhaseStatus
		wantStatus string
	}{
		{name: "blocked", phaseState: flowstore.PhaseBlocked, wantStatus: flowstore.StatusBlocked},
		{name: "needs attention", phaseState: flowstore.PhaseNeedsAttention, wantStatus: flowstore.StatusNeedsAttention},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			repoPath := filepath.Join(root, "repo")
			store := newBeadSlotStore(t, root)

			first := beadSlotCreate(t, store, repoPath, "approach-cwk", []flowstore.FlowPhase{
				{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: tt.phaseState, Notes: "needs a human"},
			})
			read, err := store.Read(first.FlowID)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if got := flowstore.DeriveStatus(read); got != tt.wantStatus {
				t.Fatalf("DeriveStatus() = %q, want %q", got, tt.wantStatus)
			}

			_, err = store.Create(flowstore.FlowRecord{
				Title: "Duplicate", Instructions: "Should be refused.", RepoPath: repoPath,
				Bead: flowstore.BeadLink{ID: "approach-cwk"},
			})
			if !flowstore.IsBeadFlowActive(err) {
				t.Fatalf("Create() error = %v, want IsBeadFlowActive for a %s Flow", err, tt.wantStatus)
			}
		})
	}
}

func TestCreateAllowsDifferentBeadOrDifferentRepository(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	otherRepo := filepath.Join(root, "other")
	store := newBeadSlotStore(t, root)

	beadSlotCreate(t, store, repoPath, "approach-cwk", nil)

	if _, err := store.Create(flowstore.FlowRecord{
		Title: "Other bead", Instructions: "Different bead.", RepoPath: repoPath,
		Bead: flowstore.BeadLink{ID: "approach-other"},
	}); err != nil {
		t.Fatalf("Create() with a different bead error = %v, want success", err)
	}
	if _, err := store.Create(flowstore.FlowRecord{
		Title: "Other repo", Instructions: "Different repository.", RepoPath: otherRepo,
		Bead: flowstore.BeadLink{ID: "approach-cwk"},
	}); err != nil {
		t.Fatalf("Create() in a different repository error = %v, want success", err)
	}

	// A request path differing only by a trailing separator is the same
	// repository: the guard must clean it exactly as the write side does.
	_, err := store.Create(flowstore.FlowRecord{
		Title: "Unclean request", Instructions: "Should be refused.", RepoPath: repoPath + string(filepath.Separator),
		Bead: flowstore.BeadLink{ID: "approach-cwk"},
	})
	if !flowstore.IsBeadFlowActive(err) {
		t.Fatalf("Create() with a trailing separator error = %v, want IsBeadFlowActive", err)
	}
}

func TestCreateNeverRefusesUnlinkedFlows(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	store := newBeadSlotStore(t, root)

	for i := 0; i < 3; i++ {
		if _, err := store.Create(flowstore.FlowRecord{
			Title: "Unlinked", Instructions: "No bead.", RepoPath: repoPath,
		}); err != nil {
			t.Fatalf("Create() unlinked flow %d error = %v", i, err)
		}
	}
	listed, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("List() = %d flows, want 3", len(listed))
	}
}

func TestBeadSlotRefusalSurvivesStoreRestart(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	first := beadSlotCreate(t, store, repoPath, "approach-cwk", nil)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := newBeadSlotStore(t, root)
	read, err := reopened.Read(first.FlowID)
	if err != nil {
		t.Fatalf("Read() after reopen error = %v", err)
	}
	if read.Bead.ID != "approach-cwk" {
		t.Fatalf("Read().Bead.ID = %q, want %q", read.Bead.ID, "approach-cwk")
	}
	listed, err := reopened.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() after reopen error = %v", err)
	}
	if len(listed) != 1 || listed[0].Bead.ID != "approach-cwk" {
		t.Fatalf("List() after reopen = %#v, want one Bead-linked Flow", listed)
	}

	_, err = reopened.Create(flowstore.FlowRecord{
		Title: "Duplicate", Instructions: "Should be refused.", RepoPath: repoPath,
		Bead: flowstore.BeadLink{ID: "approach-cwk"},
	})
	if !flowstore.IsBeadFlowActive(err) {
		t.Fatalf("Create() after reopen error = %v, want IsBeadFlowActive", err)
	}
	conflict, ok := flowstore.ActiveBeadFlow(err)
	if !ok || conflict.FlowID != first.FlowID {
		t.Fatalf("ActiveBeadFlow() = (%q, %v), want (%q, true)", conflict.FlowID, ok, first.FlowID)
	}
}
