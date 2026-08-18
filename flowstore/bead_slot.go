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

// ErrBeadFlowUnreadable is the sentinel for a refusal caused by a row that
// claims the requested Bead but whose stored record cannot be decoded.
var ErrBeadFlowUnreadable = errors.New("bead may have an unreadable flow")

// BeadFlowUnreadableError names the Flow whose record could not be decoded, so
// the caller can point a human at the row that needs repair.
//
// Such a row cannot report a derived status, so the guard cannot tell whether
// it still holds the Bead slot. Creating anyway is the worse answer: it is
// precisely how a duplicate Flow and a duplicate worktree get made. Unreadable
// rows for other Beads remain ignorable, so this never becomes a
// whole-repository outage.
type BeadFlowUnreadableError struct {
	RepoPath string
	BeadID   string
	FlowID   string
	Err      error
}

func (e *BeadFlowUnreadableError) Error() string {
	return fmt.Sprintf("flow %q may still track bead %q in %s, but its stored record is unreadable: %v", e.FlowID, e.BeadID, e.RepoPath, e.Err)
}

func (e *BeadFlowUnreadableError) Unwrap() error { return ErrBeadFlowUnreadable }

// IsBeadFlowActive reports the duplicate refusal.
func IsBeadFlowActive(err error) bool {
	return errors.Is(err, ErrBeadFlowActive)
}

// IsBeadFlowUnreadable reports the unreadable-candidate refusal.
func IsBeadFlowUnreadable(err error) bool {
	return errors.Is(err, ErrBeadFlowUnreadable)
}

// IsBeadFlowRefusal reports whether the Bead-slot guard refused a creation, in
// either of its two forms. Both refusals are raised before anything is written,
// so a caller can use this to distinguish "nothing was created" from a Flow
// that exists but failed later preparation.
func IsBeadFlowRefusal(err error) bool {
	return IsBeadFlowActive(err) || IsBeadFlowUnreadable(err)
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
