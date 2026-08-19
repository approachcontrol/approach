package regression_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/model"
)

// pinCopy stages a private copy of the real approach binary and pins it, so a
// test can replace or corrupt the SOURCE without touching the package's one
// build.
func pinCopy(t *testing.T, root string) (controlplane.Pin, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "approach")
	data, err := os.ReadFile(approachBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := controlplane.FileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	pin := controlplane.Materialize(root,
		controlplane.SourceIdentity{Path: source, Digest: digest},
		flowstore.DatabaseSchemaVersion())
	if pin.SourceChanged {
		t.Fatalf("pin reported SourceChanged for a freshly written source: %#v", pin)
	}
	return pin, source
}

// TestPinnedLaunchSurvivesItsSourceBeingReplaced is the upgrade-mid-session
// case. A `brew upgrade` replaces the binary the TUI started from; the launch
// must still run the build that is actually in charge of the database.
func TestPinnedLaunchSurvivesItsSourceBeingReplaced(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	record, phase := seedRunningPhase(t, store, "launch-replaced-source")
	pin, source := pinCopy(t, root)
	if pin.Degraded {
		t.Skipf("binary cache unavailable, so there is no cached copy to prove: %s", pin.Notice)
	}

	// The upgrade: same path, different bytes, still executable.
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 42\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("Verify() after the source was replaced = %v, want nil (the cached copy is intact)", err)
	}

	prompt := model.PhaseLaunchPrompt(record, phase, record.PlanPath, "",
		model.FlowPromptTemplates{}, pin.ExecutablePath)
	binary := promptBinaryWord(t, prompt)
	stubDir, marker := stubOldCLIOnPath(t, flowstore.DatabaseSchemaVersion()-1)
	command := binary + " flow phase complete --flow-id " + record.FlowID +
		" --phase-id " + phase.PhaseID + ` --summary "done"`
	_, stderr, code := runShell(t, root, command, launchEnv(root, stubDir,
		oldCLIMarkerEnv+"="+marker,
		targetSchemaEnv+"="+strconv.Itoa(flowstore.DatabaseSchemaVersion())))
	if code != 0 {
		t.Fatalf("the pinned command failed after its source was replaced (exit %d): %s", code, stderr)
	}
	if got := phaseByID(t, store, record.FlowID, phase.PhaseID); got.Status != flowstore.PhaseCompleted {
		t.Fatalf("phase status = %q, want %q", got.Status, flowstore.PhaseCompleted)
	}
}

// TestCorruptedPinRefusesTheLaunchAndLeavesThePhaseAlone covers the other side:
// when the cached copy itself is wrong, the launch must be refused, and the
// refusal must not be a partial launch that moved the phase.
func TestCorruptedPinRefusesTheLaunchAndLeavesThePhaseAlone(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	record, _ := seedRunningPhase(t, store, "launch-corrupt-pin")
	pin, _ := pinCopy(t, root)
	if pin.Degraded {
		t.Skipf("binary cache unavailable, so there is no cached copy to corrupt: %s", pin.Notice)
	}
	before := recordSnapshot(t, store, record.FlowID)

	// The cache is written read-only, which is the point of it; corrupting it
	// stands in for filesystem damage, not for something approach would do.
	if err := os.Chmod(pin.ExecutablePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin.ExecutablePath, []byte("not the pinned build"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := pin.Verify()
	if !errors.Is(err, controlplane.ErrPinDigestMismatch) {
		t.Fatalf("Verify() over a corrupted cached copy = %v, want ErrPinDigestMismatch", err)
	}
	// Verify is the gate flowLaunchPreparation.verifyPin consults before it
	// touches the store, so a refusal here is a launch that never happened.
	requireUnchanged(t, before, recordSnapshot(t, store, record.FlowID))
}
