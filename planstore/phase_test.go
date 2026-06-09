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

func TestSetPhaseUpsertsNormalizedPhaseIDVariants(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	savePlan(t, store, "phased")

	if err := store.SetPhase("phased", planstore.PlanPhase{PhaseID: "phase-1", Title: "Phase 1", Status: "in_progress", Order: 1}); err != nil {
		t.Fatalf("SetPhase(phase-1) error = %v", err)
	}
	// Completion commands that vary only by case or surrounding whitespace must
	// update the same logical phase, not create a second row.
	for _, variant := range []string{"Phase-1", " phase-1 ", "PHASE-1"} {
		if err := store.SetPhase("phased", planstore.PlanPhase{PhaseID: variant, Title: "Phase 1", Status: "completed", Order: 1}); err != nil {
			t.Fatalf("SetPhase(%q) error = %v", variant, err)
		}
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := records[0]
	if len(got.Phases) != 1 {
		t.Fatalf("variant phase ids duplicated rows: %#v", got.Phases)
	}
	if got.Phases[0].PhaseID != "phase-1" {
		t.Fatalf("stored phase id not normalized: %q", got.Phases[0].PhaseID)
	}
	if got.Phases[0].Status != "completed" {
		t.Fatalf("phase not updated in place: %#v", got.Phases[0])
	}
}

func TestSetPhaseCollapsesExistingDuplicateRows(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// Records written before phase-id normalization may already hold duplicate
	// rows for one logical phase; the next update must repair them.
	if _, err := store.Save(planstore.PlanRecord{
		PlanID: "dupes", Title: "T", Markdown: "b", Status: "draft",
		Phases: []planstore.PlanPhase{
			{PhaseID: "Phase-1", Title: "Phase 1", Status: "in_progress", Order: 1},
			{PhaseID: "phase-1", Title: "Phase 1", Status: "pending", Order: 1},
			{PhaseID: "phase-2", Title: "Phase 2", Status: "pending", Order: 2},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.SetPhase("dupes", planstore.PlanPhase{PhaseID: "phase-1", Title: "Phase 1", Status: "completed", Order: 1}); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := records[0]
	if len(got.Phases) != 2 {
		t.Fatalf("duplicate rows not collapsed: %#v", got.Phases)
	}
	if got.Phases[0].PhaseID != "phase-1" || got.Phases[0].Status != "completed" {
		t.Fatalf("logical phase not updated in place: %#v", got.Phases[0])
	}
	if got.Phases[1].PhaseID != "phase-2" {
		t.Fatalf("unrelated phase disturbed: %#v", got.Phases[1])
	}
}

func TestSetPhasePreservesMarkdownBody(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	const body = "# Plan\n\nDistinctive body that must survive a metadata-only update.\n"
	if _, err := store.Save(planstore.PlanRecord{PlanID: "keep-body", Title: "T", Markdown: body, Status: "draft"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.SetPhase("keep-body", planstore.PlanPhase{PhaseID: "a", Title: "First", Status: "completed", Order: 1}); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}

	got, err := store.ReadPlan("keep-body")
	if err != nil {
		t.Fatalf("ReadPlan() error = %v", err)
	}
	if got != body {
		t.Fatalf("SetPhase blanked or altered plan.md:\n got = %q\nwant = %q", got, body)
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
