package flowownership

import "strings"

// Slot is the runtime-only part of an embedded terminal that affects Flow
// ownership. Terminal objects, presentation state, and launch settings stay in
// the caller.
type Slot struct {
	FlowID             string
	Flow               bool
	Repair             bool
	WorktreeAgent      bool
	SavedSessionResume bool
	PrefillPending     bool
	FailureRetained    bool
	TerminalPresent    bool
	TerminalRunning    bool
	TerminalExited     bool
}

func (slot Slot) HoldsFlow(flowID string) bool {
	return strings.TrimSpace(flowID) != "" && slot.Flow && slot.FlowID == flowID && slot.TerminalPresent
}

func (slot Slot) HoldsNonRepairFlow(flowID string) bool {
	return slot.HoldsFlow(flowID) && !slot.Repair
}

func (slot Slot) HoldsRepair(flowID string) bool {
	flowID = strings.TrimSpace(flowID)
	return flowID != "" && slot.Flow && slot.FlowID == flowID && slot.Repair
}

func (slot Slot) AutoCloses() bool {
	return slot.Flow && slot.TerminalPresent && slot.TerminalExited && !slot.SavedSessionResume
}

// DetachDropsOwnership reports whether removing this slot would discard a
// deliberately retained in-process owner.
func (slot Slot) DetachDropsOwnership() bool {
	return slot.Flow && (slot.SavedSessionResume || slot.FailureRetained)
}
