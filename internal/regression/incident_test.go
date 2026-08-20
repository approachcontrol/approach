package regression_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
)

// TestPinnedPromptCompletesThePhaseWithAnOlderCLIFirstOnPath is the incident
// itself: a controller at schema N, an older `approach` first on PATH, and the
// command line the generated prompt hands the agent.
//
// Every mechanism this exercises has its own unit test, and all of them passed
// while the incident was live. What none of them ran was this: the real prompt
// text, resolved by a real shell, against a real second binary.
func TestPinnedPromptCompletesThePhaseWithAnOlderCLIFirstOnPath(t *testing.T) {
	root := newRoot(t)
	store := openStore(t, root)
	record, phase := seedRunningPhase(t, store, "launch-pinned")

	prompt := model.PhaseLaunchPrompt(record, phase, record.PlanPath, "",
		model.FlowPromptTemplates{}, approachBinary)
	binary := promptBinaryWord(t, prompt)

	stubDir, marker := stubOldCLIOnPath(t, flowstore.DatabaseSchemaVersion()-1)
	command := binary + " flow phase complete --flow-id " + record.FlowID +
		" --phase-id " + phase.PhaseID + ` --summary "done"`
	stdout, stderr, code := runShell(t, root, command, launchEnv(root, stubDir,
		oldCLIMarkerEnv+"="+marker,
		targetSchemaEnv+"="+strconv.Itoa(flowstore.DatabaseSchemaVersion())))
	if code != 0 {
		t.Fatalf("the shipped command line failed (exit %d)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if _, err := os.Lstat(marker); err == nil {
		invocations, _ := os.ReadFile(marker)
		t.Fatalf("the older approach on PATH was invoked by a pinned launch:\n%s", invocations)
	}
	if got := phaseByID(t, store, record.FlowID, phase.PhaseID); got.Status != flowstore.PhaseCompleted {
		t.Fatalf("phase status = %q, want %q", got.Status, flowstore.PhaseCompleted)
	}
}

// TestUnpinnedPromptFallsBackInTheDocumentedOrder proves the fallback the
// unpinned prompt spells is the one that actually resolves:
// APPROACH_EXECUTABLE, then APPROACH_BIN, then PATH. The order is load-bearing
// — APPROACH_BIN is an ordinary name a user's profile may already export — and
// only a real shell can settle it.
func TestUnpinnedPromptFallsBackInTheDocumentedOrder(t *testing.T) {
	stubDir, _ := stubOldCLIOnPath(t, flowstore.DatabaseSchemaVersion()-1)
	stubPath := filepath.Join(stubDir, "approach")

	for _, tc := range []struct {
		name      string
		env       []string
		wantStub  bool
		wantPhase flowstore.PhaseStatus
	}{
		{
			name:      "APPROACH_EXECUTABLE outranks an inherited APPROACH_BIN",
			env:       []string{"APPROACH_EXECUTABLE=" + approachBinary, "APPROACH_BIN=" + stubPath},
			wantPhase: flowstore.PhaseCompleted,
		},
		{
			name:      "APPROACH_BIN outranks PATH",
			env:       []string{"APPROACH_BIN=" + approachBinary},
			wantPhase: flowstore.PhaseCompleted,
		},
		{
			name:     "PATH is the last resort",
			env:      nil,
			wantStub: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRoot(t)
			store := openStore(t, root)
			record, phase := seedRunningPhase(t, store, "launch-unpinned")
			prompt := model.PhaseLaunchPrompt(record, phase, record.PlanPath, "",
				model.FlowPromptTemplates{}, "")
			binary := promptBinaryWord(t, prompt)

			marker := filepath.Join(t.TempDir(), "old-cli-was-invoked")
			command := binary + " flow phase complete --flow-id " + record.FlowID +
				" --phase-id " + phase.PhaseID + ` --summary "done"`
			env := launchEnv(root, stubDir,
				oldCLIMarkerEnv+"="+marker,
				targetSchemaEnv+"="+strconv.Itoa(flowstore.DatabaseSchemaVersion()))
			_, stderr, code := runShell(t, root, command, append(env, tc.env...))

			_, markerErr := os.Lstat(marker)
			if tc.wantStub {
				if markerErr != nil {
					t.Fatalf("PATH fallback did not reach the stub on PATH (exit %d): %s", code, stderr)
				}
				return
			}
			if markerErr == nil {
				t.Fatalf("the stub on PATH was reached despite a higher-priority override: %s", stderr)
			}
			if code != 0 {
				t.Fatalf("command failed (exit %d): %s", code, stderr)
			}
			if got := phaseByID(t, store, record.FlowID, phase.PhaseID); got.Status != tc.wantPhase {
				t.Fatalf("phase status = %q, want %q", got.Status, tc.wantPhase)
			}
		})
	}
}

// TestSchemaAheadOfTheCLIIsRefusedByTheOlderBuild is the other half of the
// incident: when the older build IS reached, it must refuse rather than write.
// The stub stands in for a build that cannot be compiled here, and this asserts
// the shape of the failure the suite above proves never happens on a pinned
// launch.
func TestSchemaAheadOfTheCLIIsRefusedByTheOlderBuild(t *testing.T) {
	root := newRoot(t)
	openStore(t, root)
	stubDir, marker := stubOldCLIOnPath(t, flowstore.DatabaseSchemaVersion()-1)
	_, stderr, code := runShell(t, root, "approach flow phase complete --flow-id x --phase-id y",
		launchEnv(root, stubDir,
			oldCLIMarkerEnv+"="+marker,
			targetSchemaEnv+"="+strconv.Itoa(flowstore.DatabaseSchemaVersion())))
	if code == 0 {
		t.Fatal("the older CLI accepted a database from a newer build")
	}
	if !strings.Contains(stderr, "written by a newer version of approach") {
		t.Fatalf("stderr = %q, want the newer-build refusal", stderr)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Fatal("the stub was never reached, so this test proves nothing")
	}
}

// recordSnapshot is the whole persisted record, for the assertions that a
// refused launch changed nothing. Comparing the status alone would pass while
// a refusal rewrote notes, timestamps, or launch IDs.
func recordSnapshot(t *testing.T, store *flowstore.Store, flowID string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read(%s): %v", flowID, err)
	}
	return record
}

func requireUnchanged(t *testing.T, before, after flowstore.FlowRecord) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the persisted record changed under a refused launch:\nbefore %#v\nafter  %#v", before, after)
	}
}
