package flowoccupancy

import (
	"errors"
	"testing"

	"github.com/approachcontrol/approach/actions"
)

// The skeleton must never report a Flow free. Until approach-x0r.3 reads the
// sources, a caller migrated early has to fail closed and fail loudly.
func TestQueryFailsClosedWhileUnimplemented(t *testing.T) {
	verdict := New(Sources{}).Query(Query{
		FlowID:  "flow-1",
		Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageAdmission},
	})

	if !verdict.Occupied() {
		t.Fatalf("Occupied() = false, want true while Query is unimplemented")
	}
	if !errors.Is(verdict.Err(), ErrUnimplemented) {
		t.Fatalf("Err() = %v, want ErrUnimplemented", verdict.Err())
	}
}

// Free() stays the way callers and tests express "nothing holds it", and must
// not drift into occupancy alongside the fail-closed stub.
func TestFreeIsNotOccupied(t *testing.T) {
	if Free().Occupied() {
		t.Fatal("Free().Occupied() = true, want false")
	}
	if Free().Err() != nil {
		t.Fatalf("Free().Err() = %v, want nil", Free().Err())
	}
}

// A corrupt or future Holder must not be readable as a free Flow. Naming
// HolderNone explicitly keeps the default branch for values this package does
// not know, the way Stage.String() already does.
func TestHolderStringSeparatesNoneFromUnknown(t *testing.T) {
	if got := HolderNone.String(); got != "none" {
		t.Fatalf("HolderNone.String() = %q, want %q", got, "none")
	}
	if got := (HolderHeadlessWrite + 1).String(); got != "unknown" {
		t.Fatalf("out-of-range Holder.String() = %q, want %q", got, "unknown")
	}
	if got := Holder(-1).String(); got != "unknown" {
		t.Fatalf("negative Holder.String() = %q, want %q", got, "unknown")
	}
}
