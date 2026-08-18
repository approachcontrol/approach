package model

import (
	"strings"
	"testing"
)

// sliceEpicFencedModel holds the slice admission the way production does: an
// acquisition through acquireFlowPreparation, which allocates a non-zero token.
// Setting flowPreparationAdmission directly would leave a zero token, and
// flowPreparationMatches skips the kind check for those, so every release
// assertion below would pass against any owner.
func sliceEpicFencedModel(t *testing.T) (Model, uint64) {
	t.Helper()
	m, token, admitted := Model{}.acquireFlowPreparation(flowPreparationSliceEpic)
	if !admitted || token == 0 {
		t.Fatalf("acquireFlowPreparation(slice) admitted=%v token=%d", admitted, token)
	}
	return m.recordSliceEpicLaunch("launch-1", token, "/dev/alpha", "bd-epic"), token
}

func TestSliceEpicFence_ReleasesOnlyOnItsOwnLaunchID(t *testing.T) {
	m, _ := sliceEpicFencedModel(t)
	if !m.sliceEpicLaunchInFlight() {
		t.Fatal("recorded slice launch is not reported in flight")
	}

	for _, id := range []string{"", "   ", "launch-2"} {
		if next := m.releaseSliceEpicLaunch(id); !next.sliceEpicLaunchInFlight() {
			t.Fatalf("launch ID %q released a fence it does not own", id)
		}
	}

	released := m.releaseSliceEpicLaunch("launch-1")
	if released.sliceEpicLaunchInFlight() {
		t.Fatal("the owning launch ID did not release the fence")
	}
	if released.flowPreparationAdmission || released.flowPreparationOwner != (flowPreparationOwner{}) {
		t.Fatalf("release left admission=%v owner=%#v", released.flowPreparationAdmission, released.flowPreparationOwner)
	}
	if released.sliceEpicLaunch != (sliceEpicLaunchRecord{}) {
		t.Fatalf("release left the launch record %#v", released.sliceEpicLaunch)
	}
	// Idempotent: the failure funnel and the result funnel both call it.
	if again := released.releaseSliceEpicLaunch("launch-1"); again.flowPreparationAdmission {
		t.Fatal("a second release re-took the admission")
	}
}

func TestSliceEpicFence_NotInFlightForOtherPreparationOwners(t *testing.T) {
	m, _, admitted := Model{}.acquireFlowPreparation(flowPreparationReadyBead)
	if !admitted {
		t.Fatal("ready-bead preparation was not admitted")
	}
	if m.sliceEpicLaunchInFlight() {
		t.Fatal("a ready-bead preparation reported a slice launch in flight")
	}
	if m.canSliceSelectedEpic() {
		t.Fatal("the slice shortcut stayed available while another preparation held the admission")
	}
}

func TestSliceEpicPrompt_NamesRepositoryAndEpicEverywhereItMatters(t *testing.T) {
	prompt := sliceEpicPrompt("/dev/alpha", "bd-epic")
	if strings.Count(prompt, "bd-epic") < 4 {
		t.Fatalf("prompt names the epic %d times, want it in every clause:\n%s", strings.Count(prompt, "bd-epic"), prompt)
	}
	if !strings.Contains(prompt, "/dev/alpha") {
		t.Fatalf("prompt does not name the repository:\n%s", prompt)
	}
}
