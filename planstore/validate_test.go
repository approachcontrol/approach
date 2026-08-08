package planstore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

func TestStoreRejectsEmptyTitleAndContent(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if _, err := store.Save(planstore.PlanRecord{PlanID: "x", Markdown: "body", Status: "draft"}); err == nil {
		t.Fatal("Save() with empty title: error = nil, want title required")
	} else if !strings.Contains(err.Error(), "title") {
		t.Fatalf("Save() error = %q, want title validation", err)
	}

	if _, err := store.Save(planstore.PlanRecord{PlanID: "x", Title: "T", Status: "draft"}); err == nil {
		t.Fatal("Save() with empty markdown: error = nil, want content required")
	} else if !strings.Contains(err.Error(), "content") && !strings.Contains(err.Error(), "markdown") {
		t.Fatalf("Save() error = %q, want content validation", err)
	}
}

func TestStoreRejectsInvalidStatus(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{PlanID: "x", Title: "T", Markdown: "b", Status: "bogus"}); err == nil {
		t.Fatal("Save() with invalid status: error = nil")
	} else if !strings.Contains(err.Error(), "status") {
		t.Fatalf("Save() error = %q, want status validation", err)
	}

	for _, status := range []string{"draft", "approved", "in_progress", "completed", "blocked", "superseded"} {
		if _, err := store.Save(planstore.PlanRecord{PlanID: "ok-" + status, Title: "T", Markdown: "b", Status: status}); err != nil {
			t.Fatalf("Save() rejected valid status %q: %v", status, err)
		}
	}
}

func TestStoreRejectsInvalidPlanID(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	for _, id := range []string{".", "..", "../escape", "a/b", "a b", ".hidden"} {
		if _, err := store.Save(planstore.PlanRecord{PlanID: id, Title: "T", Markdown: "b", Status: "draft"}); err == nil {
			t.Fatalf("Save() with plan id %q: error = nil, want rejection", id)
		}
	}
}

func TestStoreRejectsInvalidPlanIDOnReadAndPhaseUpdate(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	escapedDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(escapedDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{"schema_version":1,"plan_id":"../outside","title":"Escaped","status":"draft"}`
	if err := os.WriteFile(filepath.Join(escapedDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(escapedDir, "plan.md"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}

	if got, err := store.ReadPlan("../outside"); err == nil {
		t.Fatalf("ReadPlan() with escaped id returned %q, want error", got)
	} else if !strings.Contains(err.Error(), "invalid plan id") {
		t.Fatalf("ReadPlan() error = %q, want invalid plan id", err)
	}

	err = store.SetPhase("../outside", planstore.PlanPhase{PhaseID: "p1", Title: "Escaped phase", Status: "pending", Order: 1})
	if err == nil {
		t.Fatal("SetPhase() with escaped id: error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "invalid plan id") {
		t.Fatalf("SetPhase() error = %q, want invalid plan id", err)
	}
	data, err := os.ReadFile(filepath.Join(escapedDir, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta) error = %v", err)
	}
	if strings.Contains(string(data), "Escaped phase") {
		t.Fatalf("SetPhase() updated escaped metadata: %s", data)
	}
}

func TestStoreRejectsMismatchedMetadataPlanIDOnPhaseUpdate(t *testing.T) {
	root := t.TempDir()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	dir := filepath.Join(root, "plans", "safe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{"schema_version":1,"plan_id":"../outside","title":"Corrupt","status":"draft"}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("body"), 0o600); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}

	err = store.SetPhase("safe", planstore.PlanPhase{PhaseID: "p1", Title: "Phase", Status: "pending", Order: 1})
	if err == nil {
		t.Fatal("SetPhase() with mismatched metadata plan id: error = nil, want rejection")
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
		t.Fatalf("SetPhase() created escaped directory, stat err = %v", err)
	}
}

func TestStoreGeneratesIDFromTitleAndTimestamp(t *testing.T) {
	fixed := time.Date(2026, 6, 6, 14, 30, 15, 0, time.UTC)
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in approach!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	want := "20260606T143015Z-persist-plans-in-approach"
	if id != want {
		t.Fatalf("generated id = %q, want %q", id, want)
	}

	// Same title at the same timestamp collides → suffix -2, then -3.
	id2, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in approach!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	if id2 != want+"-2" {
		t.Fatalf("collision id = %q, want %q", id2, want+"-2")
	}
	id3, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in approach!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("third Save() error = %v", err)
	}
	if id3 != want+"-3" {
		t.Fatalf("collision id = %q, want %q", id3, want+"-3")
	}
}

func TestStoreGeneratesFallbackSlugForEmptyTitleSlug(t *testing.T) {
	fixed := time.Date(2026, 6, 6, 14, 30, 15, 0, time.UTC)
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	id, err := store.Save(planstore.PlanRecord{Title: "!!!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if id != "20260606T143015Z-plan" {
		t.Fatalf("fallback slug id = %q, want ...-plan", id)
	}
}
