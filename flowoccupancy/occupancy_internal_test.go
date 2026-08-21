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
