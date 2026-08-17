package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
)

func flowFromPayload(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no data object: %#v", payload)
	}
	flow, ok := data["flow"].(map[string]any)
	if !ok {
		t.Fatalf("data has no flow object: %#v", data)
	}
	return flow
}

func phaseByIDFromPayload(t *testing.T, flow map[string]any, phaseID string) map[string]any {
	t.Helper()
	phases, ok := flow["phases"].([]any)
	if !ok {
		t.Fatalf("flow has no phases array: %#v", flow)
	}
	for _, entry := range phases {
		phase := entry.(map[string]any)
		if phase["id"] == phaseID {
			return phase
		}
	}
	t.Fatalf("phase %q not found in %#v", phaseID, phases)
	return nil
}

// TestServeReflectsLiveStoreMutations covers AC 5: each request re-reads disk,
// so a change written by another process shows up without a restart.
func TestServeReflectsLiveStoreMutations(t *testing.T) {
	root := t.TempDir()
	repoPath := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	created, err := store.Create(flowstore.FlowRecord{
		Title:        "Live state",
		Instructions: "Watch it change",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0", "--state-root", root},
		serveDeps(t, t.TempDir(), nil))
	query := `{ flow(id: "` + created.FlowID + `") { status updatedAt currentPhase { id status } phases { id status } } }`

	status, payload := harness.query(t, query)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	before := flowFromPayload(t, payload)
	if got := phaseByIDFromPayload(t, before, "plan")["status"]; got != flowstore.PhaseReady {
		t.Fatalf("plan status = %v, want %q", got, flowstore.PhaseReady)
	}
	if current := before["currentPhase"].(map[string]any); current["id"] != "plan" {
		t.Fatalf("currentPhase = %#v, want plan", current)
	}
	beforeUpdated, err := time.Parse(time.RFC3339, before["updatedAt"].(string))
	if err != nil {
		t.Fatalf("updatedAt %v is not RFC3339: %v", before["updatedAt"], err)
	}

	// Mutate through the store, in this process, while the server is running.
	time.Sleep(2 * time.Millisecond)
	if _, err := store.SetPhase(flowstore.PhaseUpdate{
		FlowID:  created.FlowID,
		PhaseID: "plan",
		Status:  flowstore.PhaseRunning,
	}); err != nil {
		t.Fatalf("SetPhase() error = %v", err)
	}

	status, payload = harness.query(t, query)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	after := flowFromPayload(t, payload)
	if got := phaseByIDFromPayload(t, after, "plan")["status"]; got != flowstore.PhaseRunning {
		t.Fatalf("plan status = %v, want %q without a restart", got, flowstore.PhaseRunning)
	}
	if got := after["status"]; got != flowstore.StatusInProgress {
		t.Fatalf("flow status = %v, want %q", got, flowstore.StatusInProgress)
	}
	afterUpdated, err := time.Parse(time.RFC3339, after["updatedAt"].(string))
	if err != nil {
		t.Fatalf("updatedAt %v is not RFC3339: %v", after["updatedAt"], err)
	}
	if !afterUpdated.After(beforeUpdated) {
		t.Fatalf("updatedAt did not move: %s then %s", beforeUpdated, afterUpdated)
	}
}

// TestServeSurfacesBeadLinkAndEpicProgression proves the progression read seam
// is wired to the same store `serve` lists Flows from. The GraphQL package
// tests the projection against a fake source; only this one shows a row written
// through flowstore reaching a client.
func TestServeSurfacesBeadLinkAndEpicProgression(t *testing.T) {
	root := t.TempDir()
	repoPath := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	linked, err := store.Create(flowstore.FlowRecord{
		Title:        "Linked to an epic child",
		Instructions: "Work the child Bead",
		RepoPath:     repoPath,
		Bead:         flowstore.BeadLink{ID: "approach-y7g.7", EpicID: "approach-y7g"},
	})
	if err != nil {
		t.Fatalf("Create(linked) error = %v", err)
	}
	unlinked, err := store.Create(flowstore.FlowRecord{
		Title:        "No Bead at all",
		Instructions: "Nothing to track",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create(unlinked) error = %v", err)
	}
	if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{
		Key:     flowstore.EpicProgressionKey{RepoPath: repoPath, EpicID: "approach-y7g"},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SetEpicProgression() error = %v", err)
	}

	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0", "--state-root", root},
		serveDeps(t, t.TempDir(), nil))
	query := func(flowID string) map[string]any {
		t.Helper()
		status, payload := harness.query(t,
			`{ flow(id: "`+flowID+`") { bead { id epicId } epicProgression { enabled done halt { childBeadId } } } }`)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%v)", status, payload)
		}
		return flowFromPayload(t, payload)
	}

	flow := query(linked.FlowID)
	bead, ok := flow["bead"].(map[string]any)
	if !ok {
		t.Fatalf("bead = %#v, want the persisted link", flow["bead"])
	}
	if bead["id"] != "approach-y7g.7" || bead["epicId"] != "approach-y7g" {
		t.Errorf("bead = %#v, want the child and epic ids written at creation", bead)
	}
	progression, ok := flow["epicProgression"].(map[string]any)
	if !ok {
		t.Fatalf("epicProgression = %#v, want the row written through the store", flow["epicProgression"])
	}
	if progression["enabled"] != true || progression["done"] != false || progression["halt"] != nil {
		t.Errorf("epicProgression = %#v, want enabled, not done, not halted", progression)
	}

	// A Flow with no Bead reads back as linked to nothing, with no progression
	// invented for it.
	bare := query(unlinked.FlowID)
	if bare["bead"] != nil || bare["epicProgression"] != nil {
		t.Errorf("unlinked flow = %#v, want null bead and epicProgression", bare)
	}
}

// TestServeReportsPresetRecoveredEdges proves serve reads what the migration
// recovered. Presets are consulted during the legacy import to restore
// depends_on edges missing from legacy records, so a root migrated without them
// reports different dependsOn — and therefore different derived status and
// currentPhase — than one migrated with them.
//
// serve itself opens as a RoleReader and will not import a legacy corpus at
// all, so the migration is done up front by the migrator, which is what
// `approach db migrate` now owns.
func TestServeReportsPresetRecoveredEdges(t *testing.T) {
	preset := twoStepPresetForServeTest()
	flowID := "20260607T120000Z-serve-preset-recovery"

	// A store without presets cannot restore the missing edges. If this ever
	// stops differing, the assertion below proves nothing. Migration is
	// one-time per root, so the control needs a root of its own.
	bareRoot := t.TempDir()
	writeLegacyTwoStepFlow(t, bareRoot, flowID)
	bare, err := flowstore.NewStore(flowstore.StoreOptions{Root: bareRoot})
	if err != nil {
		t.Fatalf("NewStore(bare) error = %v", err)
	}
	bareRecords, err := bare.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List(bare) error = %v", err)
	}
	if got := dependsOnFor(t, bareRecords, flowID, "verify"); len(got) != 0 {
		t.Fatalf("preset-less store dependsOn = %v, want empty; the fixture no longer isolates presets", got)
	}

	root := t.TempDir()
	writeLegacyTwoStepFlow(t, root, flowID)
	cfg := config.Config{
		Sessions: config.SessionsConfig{Root: root},
		Flow:     config.FlowConfig{Presets: []flowstore.Preset{preset}},
	}
	migrateLegacyCorpusForCLITest(t, root, []flowstore.Preset{preset})
	wantDependsOn := []string{"build"}

	deps := serveDeps(t, root, nil)
	deps.loadConfig = func() (config.Config, error) { return cfg, nil }
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"}, deps)

	status, payload := harness.query(t, `{ flow(id: "`+flowID+`") { phases { id dependsOn status } currentPhase { id } } }`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	flow := flowFromPayload(t, payload)
	verify := phaseByIDFromPayload(t, flow, "verify")
	got := make([]string, 0, 1)
	for _, entry := range verify["dependsOn"].([]any) {
		got = append(got, entry.(string))
	}
	if len(got) != len(wantDependsOn) || got[0] != wantDependsOn[0] {
		t.Fatalf("GraphQL dependsOn = %v, want %v (the store was built without presets)", got, wantDependsOn)
	}
	if verify["status"] != flowstore.PhasePending {
		t.Fatalf("verify status = %v, want %q; a preset-less store would derive ready", verify["status"], flowstore.PhasePending)
	}
	if current := flow["currentPhase"].(map[string]any); current["id"] != "build" {
		t.Fatalf("currentPhase = %#v, want build", current)
	}
}

func dependsOnFor(t *testing.T, records []flowstore.FlowRecord, flowID, phaseID string) []string {
	t.Helper()
	for _, record := range records {
		if record.FlowID != flowID {
			continue
		}
		for _, phase := range record.Phases {
			if phase.PhaseID == phaseID {
				return phase.DependsOn
			}
		}
		t.Fatalf("phase %q not found in flow %q", phaseID, flowID)
	}
	t.Fatalf("flow %q not found", flowID)
	return nil
}

func twoStepPresetForServeTest() flowstore.Preset {
	return flowstore.Preset{
		Name: "twostep",
		Phases: []flowstore.PhaseSpec{
			{ID: "build", Title: "Build", Kind: flowstore.KindImplementation, DependsOn: []string{}},
			{ID: "verify", Title: "Verify", Kind: flowstore.KindReviewLoop, DependsOn: []string{"build"}},
		},
	}
}

// writeLegacyTwoStepFlow lays down a legacy on-disk record whose phases carry
// no depends_on, simulating a record written before edges were stored. Only
// migrate-on-open can recover those edges, and only from a matching preset.
func writeLegacyTwoStepFlow(t *testing.T, root, flowID string) {
	t.Helper()
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Preset parity",
  "instructions": "Restore my edges",
  "status": "in_progress",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "preset_name": "twostep",
  "phases": [
    {"phase_id": "build", "title": "Build", "kind": "implementation", "status": "pending", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "verify", "title": "Verify", "kind": "review_loop", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
