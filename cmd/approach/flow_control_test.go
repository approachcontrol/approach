package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// controlEnv builds the getenv a launched agent sees: the registration and,
// as the launcher exports them, the state roots — the endpoint serves exactly
// that root.
func controlEnv(root, endpoint, token, launchID, flowID, phaseID string) func(string) string {
	values := map[string]string{
		"APPROACH_CONTROL_ENDPOINT":   endpoint,
		"APPROACH_CONTROL_TOKEN":      token,
		"APPROACH_LAUNCH_ID":          launchID,
		"APPROACH_FLOW_ID":            flowID,
		"APPROACH_FLOW_PHASE_ID":      phaseID,
		"APPROACH_FLOW_STATE_ROOT":    root,
		"APPROACH_PLAN_STATE_ROOT":    root,
		"APPROACH_SESSION_STATE_ROOT": root,
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

	// A proxied leaf never loads config or opens a database: the direct path
	// needs both, so a fatal loadConfig proves the request went to the socket.
	deps := func(stdout, stderr *bytes.Buffer) runDeps {
		return noScanDeps(t, runDeps{
			stdout: stdout, stderr: stderr,
			getenv: controlEnv(controllerRoot, endpoint.Path, endpoint.Token, "launch-1", created.FlowID, "plan"),
			loadConfig: func() (config.Config, error) {
				t.Fatal("loadConfig must not run for a proxied leaf")
				return config.Config{}, nil
			},
		})
	}
	var stdout, stderr bytes.Buffer
	// No --state-root: the launch's exported root is the endpoint's root.
	if err := run([]string{"approach", "flow", "read", "--flow-id", created.FlowID}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("proxied read: %v (%s)", err, stderr.String())
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || read.FlowID != created.FlowID {
		t.Fatalf("read output = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	// An explicit --state-root naming the same root is proxied too.
	if err := run([]string{"approach", "flow", "phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "via socket", "--state-root", controllerRoot}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("proxied complete: %v (%s)", err, stderr.String())
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.UpdatedPhase.Status != flowstore.PhaseCompleted || result.UpdatedPhase.Summary != "via socket" {
		t.Fatalf("complete output = %s (%v)", stdout.String(), err)
	}
	log, _ := launchcontrol.OpenLog(controllerRoot, "launch-1")
	applied, ok, _ := log.Applied()
	if !ok || applied.AppliedSeq != 1 || applied.Status != string(flowstore.PhaseCompleted) {
		t.Fatalf("applied = %#v", applied)
	}
	// A refused write is final: exit non-zero, nothing spooled.
	stdout.Reset()
	err := run([]string{"approach", "flow", "phase", "set", "--flow-id", created.FlowID, "--phase-id", "plan", "--status", "skipped"}, deps(&stdout, &stderr))
	if err == nil || !strings.Contains(err.Error(), "skipped phase requires notes") {
		t.Fatalf("refused set error = %v", err)
	}
}

// The endpoint serves exactly the launch's root. A command that names another
// root — a scratch root under test, say — is never proxied into the
// launcher's database; it opens the root it named, directly, as before.
func TestFlowLeavesWithAnotherStateRootBypassTheEndpoint(t *testing.T) {
	controllerRoot := t.TempDir()
	repoPath := filepath.Join(controllerRoot, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Proxied", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", controllerRoot})
	recordLaunch(t, controllerRoot, created.FlowID, "plan", "launch-1")
	_, endpoint := controllerFor(t, controllerRoot, created.FlowID, "plan", "launch-1")
	scratch := t.TempDir()
	other := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Scratch", "--instructions", "x", "--repo-path", filepath.Join(scratch, "repo"), "--json", "--state-root", scratch})
	getenv := controlEnv(controllerRoot, endpoint.Path, endpoint.Token, "launch-1", created.FlowID, "plan")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "flow", "read", "--flow-id", other.FlowID, "--state-root", scratch}, noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: getenv})); err != nil {
		t.Fatalf("scratch read: %v (%s)", err, stderr.String())
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || read.FlowID != other.FlowID {
		t.Fatalf("scratch read output = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	if err := run([]string{"approach", "flow", "phase", "set", "--flow-id", other.FlowID, "--phase-id", "plan", "--status", "completed", "--summary", "scratch only", "--state-root", scratch}, noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: getenv})); err != nil {
		t.Fatalf("scratch write: %v (%s)", err, stderr.String())
	}
	// The launcher's database and launch log are untouched.
	live, err := flowstore.NewStore(flowstore.StoreOptions{Root: controllerRoot, Role: flowstore.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	record, err := live.Read(created.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if phase := phaseByID(record, "plan"); phase.Status != flowstore.PhaseRunning || phase.Summary != "" {
		t.Fatalf("live phase written through the endpoint: %#v", phase)
	}
	if _, err := live.Read(other.FlowID); err == nil {
		t.Fatal("scratch flow appeared in the live database")
	}
	log, _ := launchcontrol.OpenLog(controllerRoot, "launch-1")
	if requests, _ := log.Requests(); len(requests) != 0 {
		t.Fatalf("scratch write was logged against the launch: %#v", requests)
	}
	// Same root, spelled through a symlink, is still the endpoint's root.
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(controllerRoot, link); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", link}, noScanDeps(t, runDeps{
		stdout: &stdout, stderr: &stderr, getenv: getenv,
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig must not run for a proxied leaf")
			return config.Config{}, nil
		},
	})); err != nil {
		t.Fatalf("symlinked-root read: %v (%s)", err, stderr.String())
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
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
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
	if len(requests) != 1 || requests[0].WrittenBy != launchcontrol.WrittenByDirect || !ok || applied.AppliedSeq != 1 || applied.Status != string(flowstore.PhaseCompleted) {
		t.Fatalf("log = %#v applied = %#v", requests, applied)
	}
}

func TestPhaseRecoverFallsBackDirectlyWhenEndpointCannotBeReached(t *testing.T) {
	root := t.TempDir()
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Recover fallback", "--instructions", "x", "--repo-path", filepath.Join(root, "repo"), "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-stale")
	store := mustFlowStore(t, root)
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{FlowID: created.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-ended", LaunchID: "launch-stale", Status: "ended"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DemoteReconciledPhase(flowstore.ReconciliationDemotionUpdate{PhaseUpdate: flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled"}, Reason: flowstore.OutcomePhaseResultMissing, LaunchID: "launch-stale"}); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(t.TempDir(), "dead.sock")
	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "flow", "phase", "recover", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root}, noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-stale", created.FlowID, "plan")}))
	if err != nil {
		t.Fatalf("recover fallback: %v (%s)", err, stderr.String())
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.UpdatedPhase.Status != flowstore.PhaseReady {
		t.Fatalf("output = %s (%v)", stdout.String(), err)
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
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
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
	if len(pending) != 1 || pending[0].WrittenBy != launchcontrol.WrittenBySpool || pending[0].Verb != launchcontrol.VerbPhaseComplete || pending[0].Observed.Status != string(flowstore.PhaseRunning) {
		t.Fatalf("pending = %#v", pending)
	}
	after, _ := os.ReadFile(filepath.Join(root, "approach.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("incompatible database was written")
	}
	// Non-replayable writes and reads cannot spool.
	err = run([]string{"approach", "flow", "phase", "restart", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err == nil || !strings.HasPrefix(err.Error(), "cannot be deferred: phase.restart is not replayable; control endpoint "+dead+" unreachable and the flow database could not be opened: ") {
		t.Fatalf("restart error = %v", err)
	}
	stdout.Reset()
	err = run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
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
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", "", "")}))
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
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", record.FlowID, "plan")}))
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
				noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, tc.phaseEnv)}))
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
			noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
		if err != nil || !strings.Contains(stderr.String(), flowSpooledMessage) {
			t.Fatalf("%v: exit = %v stderr = %q, want spooled", args, err, stderr.String())
		}
	}
	if pending, _ := log.Pending(); len(pending) != 2 {
		t.Fatalf("pending = %#v, want the two spooled requests", pending)
	}
}

// A spooled request is applied by replay against the launch's baseline, and
// replay rejects a launch that has none. RecordBaseline is best-effort, so a
// launch can lack baseline.json; spooling for it would be a deferred success
// that never lands. It exits non-zero instead and leaves nothing pending.
func TestSpoolRefusesLaunchWithoutABaseline(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "No Baseline", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	// Launched, but without the baseline decorator.
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
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
	var stdout, stderr bytes.Buffer
	err = run([]string{"approach", "flow", "phase", "complete", "--flow-id", created.FlowID, "--phase-id", "plan", "--summary", "later", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: controlEnv(root, dead, "tok", "launch-1", created.FlowID, "plan")}))
	if err == nil || !strings.HasPrefix(err.Error(), "cannot be deferred: ") || !strings.Contains(err.Error(), "baseline") {
		t.Fatalf("exit = %v, want a cannot-be-deferred error naming the baseline", err)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "spooled:") {
		t.Fatalf("stdout = %q stderr = %q", stdout.String(), stderr.String())
	}
	log, _ := launchcontrol.OpenLog(root, "launch-1")
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want nothing spooled", pending)
	}
}

// An endpoint that takes the request and then closes without answering may
// or may not have applied it. A replayable write falls back as for any
// unreachable endpoint (its duplicate is a same-status no-op), but a
// non-replayable one — phase restart, add-child, agent set — is not executed
// again on a guess: the CLI exits non-zero saying the outcome is
// indeterminate, so the agent verifies rather than retries.
func TestNonReplayableWriteIsNotRetriedAfterALostResponse(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Lost", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-1")
	mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseBlocked, flowstore.OutcomeBlocked, "", "waiting")
	silent := filepath.Join(shortSocketDir(t), "silent.sock")
	l, err := net.Listen("unix", silent)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			// Read the whole request, then hang up without a response.
			_, _ = io.ReadAll(c)
			_ = c.Close()
		}
	}()
	getenv := controlEnv(root, silent, "tok", "launch-1", created.FlowID, "plan")
	var stdout, stderr bytes.Buffer
	err = run([]string{"approach", "flow", "phase", "restart", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: getenv}))
	if err == nil || !strings.Contains(err.Error(), "may already have applied") {
		t.Fatalf("restart after a lost response = %v, want an indeterminate-outcome error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no JSON", stdout.String())
	}
	if phase := phaseByID(mustRunFlow(t, []string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root}), "plan"); phase.Status != flowstore.PhaseBlocked {
		t.Fatalf("restart was executed on a guess: %#v", phase)
	}
	// A replayable write still falls back to the logged direct path.
	stdout.Reset()
	if err := run([]string{"approach", "flow", "phase", "set", "--flow-id", created.FlowID, "--phase-id", "plan", "--status", "running", "--notes", "resumed", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: getenv})); err != nil {
		t.Fatalf("replayable write after a lost response = %v (%s)", err, stderr.String())
	}
	if phase := phaseByID(mustRunFlow(t, []string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root}), "plan"); phase.Status != flowstore.PhaseRunning {
		t.Fatalf("replayable write did not fall back: %#v", phase)
	}
	store := mustFlowStore(t, root)
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{FlowID: created.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-ended", LaunchID: "launch-1", Status: "ended"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DemoteReconciledPhase(flowstore.ReconciliationDemotionUpdate{PhaseUpdate: flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled"}, Reason: flowstore.OutcomePhaseResultMissing, LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err = run([]string{"approach", "flow", "phase", "recover", "--flow-id", created.FlowID, "--phase-id", "plan", "--state-root", root}, noScanDeps(t, runDeps{stdout: &stdout, stderr: &stderr, getenv: getenv}))
	if err == nil || !strings.Contains(err.Error(), "may already have applied") {
		t.Fatalf("recover after a lost response = %v, want an indeterminate-outcome error", err)
	}
	if phase := phaseByID(mustRunFlow(t, []string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root}), "plan"); phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != flowstore.OutcomePhaseResultMissing {
		t.Fatalf("recover was executed on a guess: %#v", phase)
	}
}

// shortSocketDir is a temp dir under /tmp: t.TempDir() paths on macOS exceed
// the sockaddr limit, and a socket that cannot bind proves nothing.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "approach-flow-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// add-child names no phase of its own; the client stamps the launch's own
// phase onto it, so the controller authorizes it for the launch that owns
// the parent phase — the one the skill tells to run it.
func TestProxiedAddChildIsAuthorizedForTheOwningLaunch(t *testing.T) {
	root := t.TempDir()
	created := mustRunFlowReadyForImplementation(t, root, "Children", "flow/children")
	recordLaunch(t, root, created.FlowID, "implementation", "launch-impl")
	_, endpoint := controllerFor(t, root, created.FlowID, "implementation", "launch-impl")
	var stdout, stderr bytes.Buffer
	deps := noScanDeps(t, runDeps{
		stdout: &stdout, stderr: &stderr,
		getenv: controlEnv(root, endpoint.Path, endpoint.Token, "launch-impl", created.FlowID, "implementation"),
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig must not run for a proxied leaf")
			return config.Config{}, nil
		},
	})
	if err := run([]string{"approach", "flow", "phase", "add-child", "--flow-id", created.FlowID, "--parent-phase-id", "implementation", "--phase-id", "implementation-api", "--title", "API", "--order", "1"}, deps); err != nil {
		t.Fatalf("proxied add-child: %v (%s)", err, stderr.String())
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("add-child output = %s (%v)", stdout.String(), err)
	}
	if child := phaseByID(record, "implementation-api"); child.PhaseID == "" || child.ParentPhaseID != "implementation" {
		t.Fatalf("child not added: %#v", record.Phases)
	}
}

// `flow plan save` is the command the bundled skill tells a launched agent to
// run, and it is a composite of `flow read` and `flow plan set`. Both halves go
// through the controller, so the composite stays exactly as available as its
// parts after a newer TUI has migrated the state root out of this build's
// reach.
func TestFlowPlanSaveProxiesThroughEndpoint(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Plan Save", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, created.FlowID, "plan", "launch-1")
	_, endpoint := controllerFor(t, root, created.FlowID, "plan", "launch-1")
	planFile := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(planFile, []byte("# Plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Both halves proxied means neither opens the database, and the direct
	// path needs config to do that — so a fatal loadConfig is the proof. The
	// plan artifact itself is a local file under the named state root, which
	// needs no config either.
	var stdout, stderr bytes.Buffer
	err := run([]string{"approach", "flow", "plan", "save", "--flow-id", created.FlowID, "--title", "Implementation plan", "--file", planFile, "--state-root", root},
		noScanDeps(t, runDeps{
			stdout: &stdout, stderr: &stderr,
			getenv: controlEnv(root, endpoint.Path, endpoint.Token, "launch-1", created.FlowID, "plan"),
			loadConfig: func() (config.Config, error) {
				t.Fatal("flow plan save opened the database directly instead of proxying")
				return config.Config{}, nil
			},
		}))
	if err != nil {
		t.Fatalf("proxied plan save: %v (%s)", err, stderr.String())
	}
	var saved flowPlanSaveResult
	if err := json.Unmarshal(stdout.Bytes(), &saved); err != nil {
		t.Fatalf("output = %s (%v)", stdout.String(), err)
	}
	if saved.PlanID == "" || !saved.Linked || saved.FlowID != created.FlowID {
		t.Fatalf("saved = %#v", saved)
	}
	if _, err := os.Lstat(saved.PlanPath); err != nil {
		t.Fatalf("plan markdown is not on disk: %v", err)
	}
	// The link really landed in the Flow the controller serves.
	linked := mustRunFlow(t, []string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root})
	if linked.PlanID != saved.PlanID {
		t.Fatalf("linked plan = %q, want %q", linked.PlanID, saved.PlanID)
	}
}

// A launched agent may deliberately update ANOTHER Flow in the same root — the
// bundled skill says so, and `flow plan save` documents what an omitted
// --plan-id means there. The controller authorizes exactly the Flow its launch
// registered, and its refusal is final, so such a command must open the store
// itself rather than be proxied into a refusal.
func TestFlowLeavesTargetingAnotherFlowBypassTheEndpoint(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	launched := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Launched", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	other := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Other", "--instructions", "x", "--repo-path", repoPath, "--json", "--state-root", root})
	recordLaunch(t, root, launched.FlowID, "plan", "launch-1")
	_, endpoint := controllerFor(t, root, launched.FlowID, "plan", "launch-1")
	deps := func(stdout, stderr *bytes.Buffer) runDeps {
		return noScanDeps(t, runDeps{
			stdout: stdout, stderr: stderr,
			getenv: controlEnv(root, endpoint.Path, endpoint.Token, "launch-1", launched.FlowID, "plan"),
		})
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"approach", "flow", "read", "--flow-id", other.FlowID}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("read of another Flow: %v (%s)", err, stderr.String())
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil || read.FlowID != other.FlowID {
		t.Fatalf("read output = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	// A write lands too, and is not logged against this launch.
	if err := run([]string{"approach", "flow", "phase", "complete", "--flow-id", other.FlowID, "--phase-id", "plan", "--summary", "cross flow"}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("write to another Flow: %v (%s)", err, stderr.String())
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.UpdatedPhase.Status != flowstore.PhaseCompleted {
		t.Fatalf("write output = %s (%v)", stdout.String(), err)
	}
	log, _ := launchcontrol.OpenLog(root, "launch-1")
	if requests, _ := log.Requests(); len(requests) != 0 {
		t.Fatalf("another Flow's write was logged against this launch: %#v", requests)
	}
	// The launch's own Flow is still proxied.
	stdout.Reset()
	if err := run([]string{"approach", "flow", "read", "--flow-id", launched.FlowID}, deps(&stdout, &stderr)); err != nil {
		t.Fatalf("read of the launch's own Flow: %v (%s)", err, stderr.String())
	}
}
