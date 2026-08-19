package regression_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// TestDevBuildRefusesToMigrateTheReleaseDefaultRoot runs the real
// `approach db migrate` process against a faked release default root.
//
// Through the process, not the Go guard: the guard's own test proves the
// condition, and this proves the command an operator actually types honours it
// — including that the acknowledgement it names is reachable from a shell.
func TestDevBuildRefusesToMigrateTheReleaseDefaultRoot(t *testing.T) {
	// Staged away from the fake default root and moved in, because building it
	// AT that path would be refused by the same guard under test.
	staging := newRoot(t)
	store := openStore(t, staging)
	seedRunningPhase(t, store, "launch-dev-root")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stampUserVersion(t, staging, int64(flowstore.DatabaseSchemaVersion())-1)

	stateHome := newRoot(t)
	releaseRoot := filepath.Join(stateHome, "approach", "sessions", "v1")
	if err := os.MkdirAll(filepath.Dir(releaseRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, releaseRoot); err != nil {
		t.Fatal(err)
	}

	home := newRoot(t)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_STATE_HOME=" + stateHome,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
	}
	// --state-root names the release root explicitly: a DEVELOPMENT build's own
	// default root is approach-dev/, so without this the command would migrate
	// a different directory and prove nothing.
	stdout, stderr, code := runShell(t, home,
		shellQuote(approachBinary)+" db migrate --state-root "+shellQuote(releaseRoot), env)
	if code == 0 {
		t.Fatalf("a development build migrated the release default root:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "--allow-dev-live-migration") {
		t.Fatalf("stderr = %q, want the acknowledgement flag named", stderr)
	}
	// The refusal must leave the database exactly as it was: a half-migrated
	// release root is strictly worse than an unmigrated one.
	report := inspectJSON(t, releaseRoot)
	if report.UserVersion == nil || *report.UserVersion != int64(flowstore.DatabaseSchemaVersion())-1 {
		t.Fatalf("user_version after a refused migration = %v", report.UserVersion)
	}
}

// TestInspectAgainstAFutureSchemaTellsTheOperatorToUpgrade runs the real
// binary against a database stamped one generation ahead of it. `db inspect`
// is the command an operator reaches for when nothing else answers, so it must
// exit 0, emit parseable JSON, and name the one correct action.
func TestInspectAgainstAFutureSchemaTellsTheOperatorToUpgrade(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stampUserVersion(t, root, int64(flowstore.DatabaseSchemaVersion())+1)

	report := inspectJSON(t, root)
	if report.UserVersion == nil || *report.UserVersion != int64(flowstore.DatabaseSchemaVersion())+1 {
		t.Fatalf("user_version = %v", report.UserVersion)
	}
	if report.NextAction == nil || !strings.Contains(*report.NextAction, "upgrade approach") {
		t.Fatalf("next_action = %v, want the upgrade advice", report.NextAction)
	}
	if strings.Contains(strings.ToLower(derefOr(report.NextAction)), "restore") {
		t.Fatalf("next_action = %q offers restore advice for a newer build", *report.NextAction)
	}
}

func derefOr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// inspectJSON runs the real `approach db inspect --json` and decodes it. It
// asserts exit 0 on the way past: `db inspect` never refuses, and a diagnostic
// that fails on the database it exists to diagnose is worse than none.
func inspectJSON(t *testing.T, root string) flowstore.InspectReport {
	t.Helper()
	home := newRoot(t)
	stdout, stderr, code := runShell(t, home,
		shellQuote(approachBinary)+" db inspect --json --state-root "+shellQuote(root),
		[]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config")})
	if code != 0 {
		t.Fatalf("db inspect exited %d: %s", code, stderr)
	}
	return decodeJSON[flowstore.InspectReport](t, stdout)
}

// TestAgentExitWithoutResultLandsNeedsAttention drives the real reconcile entry
// point: an agent that exited cleanly and persisted nothing must leave the
// phase visibly stuck rather than silently running forever.
func TestAgentExitWithoutResultLandsNeedsAttention(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	record, phase := seedLaunchWithBaseline(t, store, root, "launch-clean-exit")

	if err := launchcontrol.RecordLaunchExit(root, record.FlowID, phase.PhaseID,
		"launch-clean-exit", 0, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	controller, err := launchcontrol.New(launchcontrol.Options{Root: root, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if report := controller.Sweep(); report.Reconciled != 1 {
		t.Fatalf("sweep = %#v, want exactly one reconciled launch", report)
	}
	got := phaseByID(t, store, record.FlowID, phase.PhaseID)
	if got.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("phase status = %q, want %q", got.Status, flowstore.PhaseNeedsAttention)
	}
	if !strings.Contains(got.Notes, "exit code 0") {
		t.Fatalf("notes = %q, want the exit named", got.Notes)
	}
}

// TestControllerCrashBetweenAcceptAndApplyAppliesExactlyOnce is the durability
// claim the request log exists for. The assertion is on the applied SEQUENCE,
// not the final status: a double-apply reaches the same status and would be
// invisible to a status-only check.
func TestControllerCrashBetweenAcceptAndApplyAppliesExactlyOnce(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	record, phase := seedLaunchWithBaseline(t, store, root, "launch-crash")

	// The crash: the request was accepted and durably logged, and the process
	// died before applying it. Nothing wrote applied.json.
	req, err := launchcontrol.NewRequest(launchcontrol.VerbPhaseComplete,
		launchcontrol.PhaseActionPayload{Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	req.FlowID, req.PhaseID, req.LaunchID = record.FlowID, phase.PhaseID, "launch-crash"
	log, err := launchcontrol.OpenLog(root, "launch-crash")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := log.Append(launchcontrol.RequestEnvelope{
		RequestID:  req.RequestID,
		LaunchID:   req.LaunchID,
		FlowID:     req.FlowID,
		PhaseID:    req.PhaseID,
		Verb:       req.Verb,
		Replayable: launchcontrol.Replayable(req.Verb),
		Payload:    req.Payload,
		WrittenBy:  "controller",
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied, ok, _ := log.Applied(); ok {
		t.Fatalf("applied.json exists before the restart: %#v", applied)
	}

	// The restart.
	controller, err := launchcontrol.New(launchcontrol.Options{Root: root, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if _, err := controller.Recover(); err != nil {
		t.Fatal(err)
	}
	applied, ok, err := log.Applied()
	if err != nil || !ok {
		t.Fatalf("applied.json missing after recovery: ok=%t err=%v", ok, err)
	}
	if applied.AppliedSeq != seq {
		t.Fatalf("applied_seq = %d, want %d", applied.AppliedSeq, seq)
	}
	after := phaseByID(t, store, record.FlowID, phase.PhaseID)
	if after.Status != flowstore.PhaseCompleted {
		t.Fatalf("phase status = %q, want %q", after.Status, flowstore.PhaseCompleted)
	}

	// A second restart must be a no-op. This is the half a status assertion
	// cannot see: re-applying would leave the same status behind a second
	// write.
	before := recordSnapshot(t, store, record.FlowID)
	if _, err := controller.Recover(); err != nil {
		t.Fatal(err)
	}
	requireUnchanged(t, before, recordSnapshot(t, store, record.FlowID))
	replayed, _, err := log.Applied()
	if err != nil {
		t.Fatal(err)
	}
	if replayed.AppliedSeq != seq {
		t.Fatalf("applied_seq after a second recovery = %d, want %d", replayed.AppliedSeq, seq)
	}
	if pending, err := log.Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("pending after recovery = %d (err %v), want none", len(pending), err)
	}
}

// seedLaunchWithBaseline is seedRunningPhase plus the launch-directory state a
// controller needs to reason about the launch: baseline.json and launch.json.
func seedLaunchWithBaseline(t *testing.T, store *flowstore.Store, root, launchID string) (flowstore.FlowRecord, flowstore.FlowPhase) {
	t.Helper()
	created, err := store.Create(flowstore.FlowRecord{
		Title:        "Incident regression",
		Instructions: "Exercise the controller's crash path.",
		RepoPath:     filepath.Join(t.TempDir(), "repo"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	phaseID := created.Phases[0].PhaseID
	next := launchcontrol.RecordBaseline(root, store.AddPhaseLaunchID)
	updated, err := next(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: phaseID, LaunchID: launchID})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID: %v", err)
	}
	log, err := launchcontrol.OpenLog(root, launchID)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.WriteLaunch(launchcontrol.LaunchInfo{
		FlowID: created.FlowID, PhaseID: phaseID, Kind: "phase",
	}); err != nil {
		t.Fatal(err)
	}
	for _, phase := range updated.Phases {
		if phase.PhaseID == phaseID {
			return updated, phase
		}
	}
	t.Fatalf("phase %s vanished", phaseID)
	return flowstore.FlowRecord{}, flowstore.FlowPhase{}
}
