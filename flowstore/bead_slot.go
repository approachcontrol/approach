package flowstore

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBeadFlowActive is the sentinel every duplicate Bead-linked creation
// refusal wraps.
var ErrBeadFlowActive = errors.New("bead already has an active flow")

// BeadFlowActiveError names the Flow that already holds the repository+Bead
// slot so callers can surface it instead of creating a second Flow.
type BeadFlowActiveError struct {
	RepoPath string
	BeadID   string
	Existing FlowRecord
}

func (e *BeadFlowActiveError) Error() string {
	return fmt.Sprintf("flow %q already tracks bead %q in %s", e.Existing.FlowID, e.BeadID, e.RepoPath)
}

func (e *BeadFlowActiveError) Unwrap() error { return ErrBeadFlowActive }

// IsBeadFlowActive reports the duplicate refusal.
func IsBeadFlowActive(err error) bool {
	return errors.Is(err, ErrBeadFlowActive)
}

// ActiveBeadFlow returns the Flow named by a duplicate refusal.
func ActiveBeadFlow(err error) (FlowRecord, bool) {
	var conflict *BeadFlowActiveError
	if errors.As(err, &conflict) {
		return conflict.Existing, true
	}
	return FlowRecord{}, false
}

// BeadFlowSlotOccupied reports whether record holds its repository+Bead slot.
//
// A Flow occupies the slot for its Bead unless it reached a terminal status:
// completed, merged, abandoned, and closed release it, because a follow-up Flow
// for the same Bead is then legitimate. Everything else — including blocked and
// needs_attention — holds it, because those are the states where a human is
// meant to intervene on the existing Flow rather than fork a second one.
//
// DeriveStatus returns exactly eight values, so the switch below is total.
func BeadFlowSlotOccupied(record FlowRecord) bool {
	if strings.TrimSpace(record.Bead.ID) == "" {
		return false
	}
	switch DeriveStatus(record) {
	case StatusClosed, StatusAbandoned, StatusMerged, StatusCompleted:
		return false
	default:
		return true
	}
}
