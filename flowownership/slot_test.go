package flowownership

import "testing"

func TestSlotRetentionPolicy(t *testing.T) {
	tests := []struct {
		name          string
		slot          Slot
		wantHeld      bool
		wantRepair    bool
		wantAutoClose bool
		wantNoDetach  bool
	}{
		{name: "running Flow", slot: Slot{FlowID: "flow-1", Flow: true, TerminalPresent: true, TerminalRunning: true}, wantHeld: true},
		{name: "prefill pending", slot: Slot{FlowID: "flow-1", Flow: true, TerminalPresent: true, PrefillPending: true}, wantHeld: true},
		{name: "terminal-less repair", slot: Slot{FlowID: "flow-1", Flow: true, Repair: true}, wantRepair: true},
		{name: "exited ordinary Flow", slot: Slot{FlowID: "flow-1", Flow: true, TerminalPresent: true, TerminalExited: true}, wantHeld: true, wantAutoClose: true},
		{name: "exited worktree agent", slot: Slot{FlowID: "flow-1", Flow: true, TerminalPresent: true, TerminalExited: true, WorktreeAgent: true}, wantHeld: true, wantNoDetach: true},
		{name: "exited saved-session resume", slot: Slot{FlowID: "flow-1", Flow: true, TerminalPresent: true, TerminalExited: true, SavedSessionResume: true}, wantHeld: true, wantNoDetach: true},
		{name: "generic session", slot: Slot{TerminalPresent: true, TerminalRunning: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.slot.HoldsFlow("flow-1"); got != tc.wantHeld {
				t.Fatalf("HoldsFlow() = %v, want %v", got, tc.wantHeld)
			}
			if got := tc.slot.HoldsRepair("flow-1"); got != tc.wantRepair {
				t.Fatalf("HoldsRepair() = %v, want %v", got, tc.wantRepair)
			}
			if got := tc.slot.AutoCloses(); got != tc.wantAutoClose {
				t.Fatalf("AutoCloses() = %v, want %v", got, tc.wantAutoClose)
			}
			if got := tc.slot.DetachDropsOwnership(); got != tc.wantNoDetach {
				t.Fatalf("DetachDropsOwnership() = %v, want %v", got, tc.wantNoDetach)
			}
		})
	}
}

func TestSlotIdentityIsExact(t *testing.T) {
	slot := Slot{FlowID: "flow-10", Flow: true, TerminalPresent: true}
	if slot.HoldsFlow("flow-1") || slot.HoldsRepair("flow-1") || slot.HoldsFlow("") {
		t.Fatal("blank or prefix-like Flow ID matched slot ownership")
	}
}
