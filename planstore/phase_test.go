package planstore_test

import (
	"strings"
	"testing"

	"github.com/brian-bell/wtui/planstore"
)

func savePlan(t *testing.T, store *planstore.Store, id string) {
	t.Helper()
	if _, err := store.Save(planstore.PlanRecord{PlanID: id, Title: "T", Markdown: "b", Status: "draft"}); err != nil {
		t.Fatalf("Save(%s) error = %v", id, err)
	}
}

func TestSetPhaseCreatesAndUpdatesOrderedPhase(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	savePlan(t, store, "phased")

	if err := store.SetPhase("phased", planstore.PlanPhase{PhaseID: "b", Title: "Second", Status: "pending", Order: 2}); err != nil {
		t.Fatalf("SetPhase(b) error = %v", err)
	}
	if err := store.SetPhase("phased", planstore.PlanPhase{PhaseID: "a", Title: "First", Status: "pending", Order: 1}); err != nil {
		t.Fatalf("SetPhase(a) error = %v", err)
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := records[0]
	if len(got.Phases) != 2 {
		t.Fatalf("want 2 phases, got %#v", got.Phases)
	}
	if got.Phases[0].PhaseID != "a" || got.Phases[1].PhaseID != "b" {
		t.Fatalf("phases not ordered by Order: %#v", got.Phases)
	}

	// Update existing phase b in place.
	if err := store.SetPhase("phased", planstore.PlanPhase{PhaseID: "b", Title: "Second updated", Status: "completed", Order: 2}); err != nil {
		t.Fatalf("SetPhase(b update) error = %v", err)
	}
	records, _ = store.List(planstore.PlanFilter{})
	got = records[0]
	if len(got.Phases) != 2 {
		t.Fatalf("update should not add a phase: %#v", got.Phases)
	}
	if got.Phases[1].Title != "Second updated" || got.Phases[1].Status != "completed" {
		t.Fatalf("phase b not updated: %#v", got.Phases[1])
	}
}

func TestSetPhaseRejectsInvalidStatusAndMissingPlan(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	savePlan(t, store, "p")

	if err := store.SetPhase("p", planstore.PlanPhase{PhaseID: "x", Title: "X", Status: "bogus", Order: 1}); err == nil {
		t.Fatal("SetPhase() invalid status: error = nil")
	} else if !strings.Contains(err.Error(), "status") {
		t.Fatalf("SetPhase() error = %q, want status validation", err)
	}

	if err := store.SetPhase("missing", planstore.PlanPhase{PhaseID: "x", Title: "X", Status: "pending", Order: 1}); err == nil {
		t.Fatal("SetPhase() missing plan: error = nil")
	}

	for _, status := range []string{"pending", "in_progress", "completed", "blocked", "skipped"} {
		if err := store.SetPhase("p", planstore.PlanPhase{PhaseID: "ph-" + status, Title: "T", Status: status, Order: 1}); err != nil {
			t.Fatalf("SetPhase() rejected valid status %q: %v", status, err)
		}
	}
}
