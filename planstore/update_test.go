package planstore_test

import (
	"testing"
	"time"

	"github.com/brian-bell/wtui/planstore"
)

func TestStoreReadPlanReturnsMarkdown(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "readable",
		Title:    "Readable",
		Markdown: "# Readable\n\nbody\n",
		Status:   "draft",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	markdown, err := store.ReadPlan("readable")
	if err != nil {
		t.Fatalf("ReadPlan() error = %v", err)
	}
	if markdown != "# Readable\n\nbody\n" {
		t.Fatalf("ReadPlan() = %q", markdown)
	}
}

func TestStoreSaveUpdatesMarkdownAndMergesMetadata(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	created := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:    "merge",
		Title:     "First",
		Summary:   "first summary",
		Markdown:  "first body",
		Status:    "draft",
		Source:    "manual",
		Provider:  "claude",
		LaunchID:  "launch-1",
		RepoPath:  "/repo",
		Branch:    "feature/x",
		CreatedAt: created,
		UpdatedAt: created,
		Phases: []planstore.PlanPhase{
			{PhaseID: "p1", Title: "Phase 1", Status: "pending", Order: 1},
		},
	}); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}

	// Update: new markdown/title/status, no summary, no repo/session fields,
	// no phases. Markdown/title/status replace; the rest are preserved.
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "merge",
		Title:    "Second",
		Markdown: "second body",
		Status:   "in_progress",
	}); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() returned %d records, want 1", len(records))
	}
	got := records[0]
	if got.Title != "Second" || got.Markdown != "second body" || got.Status != "in_progress" {
		t.Fatalf("update did not replace markdown/title/status: %#v", got)
	}
	if got.Summary != "first summary" {
		t.Fatalf("summary should be preserved when omitted, got %q", got.Summary)
	}
	if got.Provider != "claude" || got.LaunchID != "launch-1" || got.RepoPath != "/repo" || got.Branch != "feature/x" {
		t.Fatalf("repo/session fields should be preserved when omitted: %#v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("created_at should be preserved, got %s", got.CreatedAt)
	}
	if got.UpdatedAt.Equal(created) || got.UpdatedAt.Before(created) {
		t.Fatalf("updated_at should advance, got %s", got.UpdatedAt)
	}
	if len(got.Phases) != 1 || got.Phases[0].PhaseID != "p1" {
		t.Fatalf("phases should be preserved when omitted: %#v", got.Phases)
	}
}

func TestStoreSaveUpdatePreservesStatusWhenOmitted(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID: "p", Title: "T", Markdown: "body", Status: "in_progress",
	}); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	// Revise the body only, no --status: the prior status must be preserved.
	if _, err := store.Save(planstore.PlanRecord{
		PlanID: "p", Title: "T", Markdown: "new body",
	}); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	records, _ := store.List(planstore.PlanFilter{})
	if got := records[0].Status; got != "in_progress" {
		t.Fatalf("status should be preserved when omitted, got %q", got)
	}

	// A supplied status still wins.
	if _, err := store.Save(planstore.PlanRecord{
		PlanID: "p", Title: "T", Markdown: "new body", Status: "completed",
	}); err != nil {
		t.Fatalf("third Save() error = %v", err)
	}
	records, _ = store.List(planstore.PlanFilter{})
	if got := records[0].Status; got != "completed" {
		t.Fatalf("supplied status should win, got %q", got)
	}
}

func TestStoreSaveUpdatesSummaryAndFieldsWhenSupplied(t *testing.T) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "fields",
		Title:    "Title",
		Summary:  "old summary",
		Markdown: "body",
		Status:   "draft",
		Branch:   "old-branch",
	}); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   "fields",
		Title:    "Title",
		Summary:  "new summary",
		Markdown: "body",
		Status:   "draft",
		Branch:   "new-branch",
	}); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	records, err := store.List(planstore.PlanFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	got := records[0]
	if got.Summary != "new summary" {
		t.Fatalf("summary should update when supplied, got %q", got.Summary)
	}
	if got.Branch != "new-branch" {
		t.Fatalf("branch should update when supplied, got %q", got.Branch)
	}
}
