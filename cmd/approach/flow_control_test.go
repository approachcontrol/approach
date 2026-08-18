package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// controlEnv builds the getenv a launched agent sees.
func controlEnv(endpoint, token, launchID, flowID, phaseID string) func(string) string {
	values := map[string]string{
		"APPROACH_CONTROL_ENDPOINT": endpoint,
		"APPROACH_CONTROL_TOKEN":    token,
		"APPROACH_LAUNCH_ID":        launchID,
		"APPROACH_FLOW_ID":          flowID,
		"APPROACH_FLOW_PHASE_ID":    phaseID,
	}
	return func(key string) string { return values[key] }
}

// controllerFor serves a controller over the given root's store and registers
// one launch.
func controllerFor(t *testing.T, root, flowID, phaseID, launchID string) (*launchcontrol.Controller, launchcontrol.Endpoint) {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctrl, err := launchcontrol.New(launchcontrol.Options{Root: root, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })
	if err := ctrl.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	endpoint, err := ctrl.Register(launchcontrol.Registration{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID, Kind: "phase"})
	if err != nil {
		t.Fatal(err)
	}
	return ctrl, endpoint
}

func recordLaunch(t *testing.T, root, flowID, phaseID, launchID string) {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	next := launchcontrol.RecordBaseline(root, store.AddPhaseLaunchID)
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID}); err != nil {
		t.Fatal(err)
	}
}

func TestFlowLeavesProxyThroughEndpointWithoutOpeningTheDatabase(t *testing.T) {
	controllerRoot := t.TempDir()
	repoPath := filepath.Join(controllerRoot, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Proxied", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", controllerRoot})
	recordLaunch(t, controllerRoot, created.FlowID, "plan", "launch-1")
	_, endpoint := controllerFor(t, controllerRoot, created.FlowID, "plan", "launch-1")

	// The agent's own view of the state root is garbage: a regular file where
	// approach.db should be. Any direct open would fail loudly.
	agentRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentRoot, "approach.db"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := func(stdout, stderr *bytes.Buffer) runDeps {
		return noScanDeps(t, runDeps{
			stdout: stdout, stderr: stderr,
			getenv: controlEnv(endpoint.Path, endpoint.Token, "launch-1", created.FlowID, "plan"),
			loadConfig: func() (config.Config, error) {
				t.Fatal("loadConfig must not run for a proxied leaf")
				return config.Config{}, nil
			},
		})
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", agentRoot}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("proxied read: %v (%s)", err, stderr.String())
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || read.FlowID != created.FlowID {
		t.Fatalf("read output = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	if err := run([]string{"approach", "flow", "phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "via socket", "--state-root", agentRoot}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("proxied complete: %v (%s)", err, stderr.String())
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.UpdatedPhase.Status != flowstore.PhaseCompleted || result.UpdatedPhase.Summary != "via socket" {
		t.Fatalf("complete output = %s (%v)", stdout.String(), err)
	}
	log, _ := launchcontrol.OpenLog(controllerRoot, "launch-1")
	applied, ok, _ := log.Applied()
	if !ok || applied.AppliedSeq != 1 || applied.Status != flowstore.PhaseCompleted {
		t.Fatalf("applied = %#v", applied)
	}
	if data, _ := os.ReadFile(filepath.Join(agentRoot, "approach.db")); string(data) != "not a database" {
		t.Fatal("agent-side database was touched")
	}
	// A refused write is final: exit non-zero, nothing spooled.
	stdout.Reset()
	err := run([]string{"approach", "flow", "phase", "set", "--flow-id", created.FlowID, "--phase-id", "plan", "--status", "skipped", "--state-root", agentRoot}, deps(&stdout, &stderr))
	if err == nil || !strings.Contains(err.Error(), "skipped phase requires notes") {
		t.Fatalf("refused set error = %v", err)
	}
}

func TestReplayableWriteFallsBackToLoggedDirectOpenWhenEndpointUnreachable(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Fallback", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-1")
	dead := filepath.Join(t.TempDir(), "dead.sock")
	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "flow", "phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err != nil {
		t.Fatalf("fallback complete: %v (%s)", err, stderr.String())
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.UpdatedPhase.Status != flowstore.PhaseCompleted {
		t.Fatalf("output = %s (%v)", stdout.String(), err)
	}
	log, _ := launchcontrol.OpenLog(root, "launch-1")
	requests, _ := log.Requests()
	applied, ok, _ := log.Applied()
	if len(requests) != 1 || requests[0].WrittenBy != launchcontrol.WrittenByDirect || !ok || applied.AppliedSeq != 1 || applied.Status != flowstore.PhaseCompleted {
		t.Fatalf("log = %#v applied = %#v", requests, applied)
	}
}

func TestReplayableWriteSpoolsWhenEndpointUnreachableAndDatabaseIncompatible(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Spool", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-1")
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "approach.db"))
	dead := filepath.Join(t.TempDir(), "dead.sock")
	var stdout, stderr bytes.Buffer
	err = run([]string{"approach", "flow", "phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "later", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err != nil {
		t.Fatalf("spooled complete exit = %v, want 0", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no JSON for a spooled write", stdout.String())
	}
	if !strings.Contains(stderr.String(), flowSpooledMessage) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	log, _ := launchcontrol.OpenLog(root, "launch-1")
	pending, _ := log.Pending()
	if len(pending) != 1 || pending[0].WrittenBy != launchcontrol.WrittenBySpool || pending[0].Verb != launchcontrol.VerbPhaseComplete || pending[0].Observed.Status != flowstore.PhaseRunning {
		t.Fatalf("pending = %#v", pending)
	}
	after, _ := os.ReadFile(filepath.Join(root, "approach.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("incompatible database was written")
	}
	// Non-replayable writes and reads cannot spool.
	err = run([]string{"approach", "flow", "phase", "restart", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err == nil || !strings.HasPrefix(err.Error(), "cannot be deferred: phase.restart is not replayable; control endpoint "+dead+" unreachable and the flow database could not be opened: ") {
		t.Fatalf("restart error = %v", err)
	}
	stdout.Reset()
	err = run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err == nil || !strings.HasPrefix(err.Error(), "control endpoint "+dead+" unreachable; direct read failed: ") {
		t.Fatalf("read error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("read printed %q on failure", stdout.String())
	}
	if pending, _ := log.Pending(); len(pending) != 1 {
		t.Fatalf("non-replayable verbs were spooled: %#v", pending)
	}
}

func TestFlowCreateIgnoresEndpointAndOpensDirectly(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	dead := filepath.Join(t.TempDir(), "dead.sock")
	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "flow", "create", "--title", "Direct", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", "", "")}))
	if err != nil {
		t.Fatalf("create with endpoint: %v", err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil || record.FlowID == "" {
		t.Fatalf("output = %s (%v)", stdout.String(), err)
	}
	// phase reset is direct too.
	recordLaunch(t, root, record.FlowID, "plan", "launch-1")
	stdout.Reset()
	err = run([]string{"approach", "flow", "phase", "reset", "--flow-id", record.FlowID, "--phase-id", "plan", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", record.FlowID, "plan")}))
	if err != nil {
		t.Fatalf("reset with endpoint: %v", err)
	}
}

// Only a request a replay can apply may be spooled: replay rejects every
// request of an unowned launch (repair, autofix, generic) as baseline_missing,
// and gates an owned launch's requests on its own phase. Reporting those as
// deferred success would promise an apply that never comes; they exit
// non-zero like any other non-deferrable write and leave nothing pending.
func TestSpoolRefusesRequestsNoReplayCanApply(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Spool Owner", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-1")
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(t.TempDir(), "dead.sock")
	cases := []struct {
		name     string
		phaseEnv string
		args     []string
	}{
		{name: "unowned launch", phaseEnv: "", args: []string{"phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "later"}},
		{name: "another phase", phaseEnv: "plan", args: []string{"phase", "set", "--flow-id", created.FlowID, "--phase-id", "implementation", "--status", "completed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(append([]string{"approach", "flow"}, append(tc.args, "--state-root", root)...),
				noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, tc.phaseEnv)}))
			if err == nil || !strings.HasPrefix(err.Error(), "cannot be deferred: ") {
				t.Fatalf("exit = %v, want a cannot-be-deferred error", err)
			}
			if stdout.Len() != 0 || strings.Contains(stderr.String(), "spooled:") {
				t.Fatalf("stdout = %q stderr = %q, want neither JSON nor a spooled message", stdout.String(), stderr.String())
			}
		})
	}
	log, _ := launchcontrol.OpenLog(root, "launch-1")
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want nothing spooled", pending)
	}
	// The launch's own phase and a Flow-level verb still spool.
	for _, args := range [][]string{
		{"phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "later"},
		{"plan", "set", "--flow-id", created.FlowID, "--plan-id", "plan-9"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(append([]string{"approach", "flow"}, append(args, "--state-root", root)...),
			noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(dead, "tok", "launch-1", created.FlowID, "plan")}))
		if err != nil || !strings.Contains(stderr.String(), flowSpooledMessage) {
			t.Fatalf("%v: exit = %v stderr = %q, want spooled", args, err, stderr.String())
		}
	}
	if pending, _ := log.Pending(); len(pending) != 2 {
		t.Fatalf("pending = %#v, want the two spooled requests", pending)
	}
}
