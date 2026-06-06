package planstore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/planstore"
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

func TestStoreGeneratesIDFromTitleAndTimestamp(t *testing.T) {
	fixed := time.Date(2026, 6, 6, 14, 30, 15, 0, time.UTC)
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in wtui!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	want := "20260606T143015Z-persist-plans-in-wtui"
	if id != want {
		t.Fatalf("generated id = %q, want %q", id, want)
	}

	// Same title at the same timestamp collides → suffix -2, then -3.
	id2, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in wtui!", Markdown: "b", Status: "draft"})
	if err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	if id2 != want+"-2" {
		t.Fatalf("collision id = %q, want %q", id2, want+"-2")
	}
	id3, err := store.Save(planstore.PlanRecord{Title: "Persist Plans in wtui!", Markdown: "b", Status: "draft"})
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
