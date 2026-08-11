package main

import (
	"encoding/json"
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

// TestServePassesPresetsToStore proves cfg.Flow.Presets reaches the store.
// Presets are consulted on the read path to restore missing depends_on edges,
// so a store built without them reports different dependsOn — and therefore
// different derived status and currentPhase — than the CLI does.
func TestServePassesPresetsToStore(t *testing.T) {
	root := t.TempDir()
	repoPath := t.TempDir()
	preset := flowstore.Preset{
		Name: "twostep",
		Phases: []flowstore.PhaseSpec{
			{ID: "build", Title: "Build", Kind: flowstore.KindImplementation, DependsOn: []string{}},
			{ID: "verify", Title: "Verify", Kind: flowstore.KindReviewLoop, DependsOn: []string{"build"}},
		},
	}
	cfg := config.Config{
		Sessions: config.SessionsConfig{Root: root},
		Flow:     config.FlowConfig{Presets: []flowstore.Preset{preset}},
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: cfg.Flow.Presets})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	created, err := store.CreateWithOptions(flowstore.FlowRecord{
		Title:        "Preset parity",
		Instructions: "Restore my edges",
		RepoPath:     repoPath,
		PresetName:   preset.Name,
	}, flowstore.CreateOptions{Preset: &preset})
	if err != nil {
		t.Fatalf("CreateWithOptions() error = %v", err)
	}
	stripPersistedDependsOn(t, root, created.FlowID)

	// A store without presets cannot restore the stripped edges. If this ever
	// stops differing, the parity assertion below proves nothing.
	bare, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore(bare) error = %v", err)
	}
	bareRecords, err := bare.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List(bare) error = %v", err)
	}
	if got := dependsOnFor(t, bareRecords, created.FlowID, "verify"); len(got) != 0 {
		t.Fatalf("preset-less store dependsOn = %v, want empty; the fixture no longer isolates presets", got)
	}

	cliRecords, err := store.List(flowstore.FlowFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	wantDependsOn := dependsOnFor(t, cliRecords, created.FlowID, "verify")
	if len(wantDependsOn) != 1 || wantDependsOn[0] != "build" {
		t.Fatalf("CLI read path dependsOn = %v, want [build]", wantDependsOn)
	}

	deps := serveDeps(t, root, nil)
	deps.loadConfig = func() (config.Config, error) { return cfg, nil }
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"}, deps)

	status, payload := harness.query(t, `{ flow(id: "`+created.FlowID+`") { phases { id dependsOn status } currentPhase { id } } }`)
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

// stripPersistedDependsOn removes every depends_on key from a persisted
// record, simulating a record written before edges were stored.
func stripPersistedDependsOn(t *testing.T, root, flowID string) {
	t.Helper()
	path := filepath.Join(root, "flows", flowID, "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	phases, ok := record["phases"].([]any)
	if !ok {
		t.Fatalf("record has no phases array: %#v", record)
	}
	for _, entry := range phases {
		delete(entry.(map[string]any), "depends_on")
	}
	updated, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
