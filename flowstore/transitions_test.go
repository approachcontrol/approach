package flowstore_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func TestAllowedNextPhaseStatusesCanonicalTable(t *testing.T) {
	for _, tc := range []struct {
		current string
		want    []string
	}{
		{flowstore.PhasePending, []string{flowstore.PhaseSkipped}},
		{flowstore.PhaseReady, []string{
			flowstore.PhaseRunning,
			flowstore.PhaseNeedsAttention,
			flowstore.PhaseCompleted,
			flowstore.PhaseBlocked,
			flowstore.PhaseSkipped,
		}},
		{flowstore.PhaseRunning, []string{
			flowstore.PhaseNeedsAttention,
			flowstore.PhaseCompleted,
			flowstore.PhaseBlocked,
			flowstore.PhaseSkipped,
		}},
		{flowstore.PhaseNeedsAttention, []string{flowstore.PhaseRunning, flowstore.PhaseSkipped}},
		{flowstore.PhaseBlocked, []string{flowstore.PhaseRunning, flowstore.PhaseSkipped}},
		{flowstore.PhaseCompleted, []string{flowstore.PhaseRunning}},
		{flowstore.PhaseSkipped, []string{flowstore.PhaseRunning}},
		{"unknown", nil},
	} {
		t.Run(tc.current, func(t *testing.T) {
			got := flowstore.AllowedNextPhaseStatuses(tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AllowedNextPhaseStatuses(%q) = %#v, want %#v", tc.current, got, tc.want)
			}
		})
	}
}

func TestStoreReadinessDerivationByGraphShape(t *testing.T) {
	root := t.TempDir()
	writeFlowMeta := func(t *testing.T, flowID, phasesJSON string) {
		t.Helper()
		flowDir := filepath.Join(root, "flows", flowID)
		if err := os.MkdirAll(flowDir, 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Graph",
  "instructions": "readiness derivation contract",
  "status": "pending",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "phases": [` + phasesJSON + `],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
		if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	phaseJSON := func(phaseID, kind, status string, order int) string {
		return `{"phase_id": "` + phaseID + `", "title": "` + phaseID + `", "kind": "` + kind + `",
  "status": "` + status + `", "order": ` + strconv.Itoa(order) + `,
  "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}`
	}
	writeFlowMeta(t, "20260101T000000Z-standard",
		phaseJSON("plan", "plan", "pending", 1)+","+phaseJSON("plan-review", "plan_review", "pending", 2))
	writeFlowMeta(t, "20260101T000000Z-custom",
		phaseJSON("research", "research", "running", 1)+","+phaseJSON("publish", "publish", "pending", 2))

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	standard, err := store.Read("20260101T000000Z-standard")
	if err != nil {
		t.Fatalf("Read(standard) error = %v", err)
	}
	if got := standard.Phases[0].Status; got != flowstore.PhaseReady {
		t.Fatalf("standard graph first phase status = %q, want ready (self-healed on read)", got)
	}

	custom, err := store.Read("20260101T000000Z-custom")
	if err != nil {
		t.Fatalf("Read(custom) error = %v", err)
	}
	if got := custom.Phases[1].Status; got != flowstore.PhasePending {
		t.Fatalf("custom graph second phase status = %q, want pending (stored statuses preserved on read)", got)
	}

	// Store mutations re-derive readiness for every graph shape.
	custom, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  "20260101T000000Z-custom",
		PhaseID: "research",
		Status:  flowstore.PhaseCompleted,
	})
	if err != nil {
		t.Fatalf("SetPhase(custom research completed) error = %v", err)
	}
	if got := custom.Phases[1].Status; got != flowstore.PhaseReady {
		t.Fatalf("custom graph second phase status after write = %q, want ready (readiness derived on mutation)", got)
	}
}

func TestStoreSetPhaseInvalidTransitionListsAllowedStatuses(t *testing.T) {
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        "Transition errors",
		Instructions: "list allowed next statuses",
		RepoPath:     filepath.Join(root, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan-review",
		Status:  flowstore.PhaseCompleted,
		Outcome: flowstore.OutcomeApproved,
	})
	want := "invalid phase transition pending -> completed; allowed from pending: skipped"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("SetPhase() error = %v, want it to contain %q", err, want)
	}

	record, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseNeedsAttention,
		Notes:   "Plan needs another pass.",
	})
	if err != nil {
		t.Fatalf("SetPhase(plan needs_attention) error = %v", err)
	}
	_, err = store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  record.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseCompleted,
	})
	want = "invalid phase transition needs_attention -> completed; allowed from needs_attention: running, skipped; restart with --status running --notes before completing"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("SetPhase() error = %v, want it to contain %q", err, want)
	}
}
