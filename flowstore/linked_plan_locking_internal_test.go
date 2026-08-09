package flowstore

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

func TestLinkedPlanPhaseSyncPreservesConcurrentPlanSave(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	flowStore, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("planstore.NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "linked-plan",
		Title:    "Original title",
		Summary:  "Original summary",
		Markdown: "Original body",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{
			{PhaseID: "Implementation", Title: "Original implementation", Status: "in_progress", Order: 3},
			{PhaseID: "existing", Title: "Existing phase", Status: "pending", Order: 4},
		},
	}); err != nil {
		t.Fatalf("seed plan Save() error = %v", err)
	}

	record, err := flowStore.Create(FlowRecord{
		Title:        "Linked plan locking",
		Instructions: "Preserve a concurrent plan save.",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	record, err = flowStore.SetPlanLink(PlanLinkUpdate{FlowID: record.FlowID, PlanID: "linked-plan"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	for _, update := range []PhaseUpdate{
		{FlowID: record.FlowID, PhaseID: "plan", Status: PhaseCompleted},
		{FlowID: record.FlowID, PhaseID: "plan-review", Status: PhaseCompleted, Outcome: OutcomeApproved},
		{FlowID: record.FlowID, PhaseID: "implementation", Status: PhaseRunning},
	} {
		record, err = flowStore.SetPhase(update)
		if err != nil {
			t.Fatalf("SetPhase(%s %s) error = %v", update.PhaseID, update.Status, err)
		}
	}

	syncReached := make(chan struct{}, 1)
	resumeSync := make(chan struct{})
	flowStore.beforeLinkedPlanPhaseSync = func(planID, phaseID string) {
		if planID != "linked-plan" || phaseID != "implementation" {
			t.Errorf("linked sync boundary = %q/%q, want linked-plan/implementation", planID, phaseID)
		}
		syncReached <- struct{}{}
		<-resumeSync
	}
	completion := make(chan error, 1)
	go func() {
		_, err := flowStore.SetPhase(PhaseUpdate{
			FlowID:  record.FlowID,
			PhaseID: "implementation",
			Status:  PhaseCompleted,
			Summary: "Implementation finished.",
		})
		completion <- err
	}()

	select {
	case <-syncReached:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for linked plan sync boundary")
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "linked-plan",
		Title:    "Concurrent title",
		Summary:  "Concurrent summary",
		Markdown: "Concurrent body",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{
			{PhaseID: "implementation", Title: "Concurrent implementation", Status: "in_progress", Order: 9},
			{PhaseID: "unrelated", Title: "Concurrent unrelated phase", Status: "blocked", Order: 10},
		},
	}); err != nil {
		close(resumeSync)
		t.Fatalf("concurrent plan Save() error = %v", err)
	}
	close(resumeSync)
	if err := <-completion; err != nil {
		t.Fatalf("SetPhase(implementation completed) error = %v", err)
	}

	got, err := planStore.ReadMetadata("linked-plan")
	if err != nil {
		t.Fatalf("ReadMetadata() error = %v", err)
	}
	if got.Title != "Concurrent title" || got.Summary != "Concurrent summary" {
		t.Fatalf("concurrent plan metadata was overwritten: %#v", got)
	}
	if len(got.Phases) != 2 {
		t.Fatalf("plan phases = %#v, want two concurrent phases", got.Phases)
	}
	if phase := got.Phases[0]; phase.PhaseID != "implementation" || phase.Title != "Concurrent implementation" || phase.Status != "completed" || phase.Order != 9 {
		t.Fatalf("target phase = %#v, want status-only completion", phase)
	}
	if phase := got.Phases[1]; phase.PhaseID != "unrelated" || phase.Title != "Concurrent unrelated phase" || phase.Status != "blocked" || phase.Order != 10 {
		t.Fatalf("unrelated phase changed: %#v", phase)
	}
	body, err := planStore.ReadPlan("linked-plan")
	if err != nil {
		t.Fatalf("ReadPlan() error = %v", err)
	}
	if body != "Concurrent body" {
		t.Fatalf("plan body = %q, want concurrent body", body)
	}
}
