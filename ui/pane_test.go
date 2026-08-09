package ui

import "testing"

func TestPaneForMode(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
		want Pane
		ok   bool
	}{
		{name: "worktrees", mode: ModeWorktrees, want: PaneTop, ok: true},
		{name: "branches", mode: ModeBranches, want: PaneTop, ok: true},
		{name: "stashes", mode: ModeStashes, want: PaneTop, ok: true},
		{name: "history", mode: ModeHistory, want: PaneTop, ok: true},
		{name: "reflog", mode: ModeReflog, want: PaneTop, ok: true},
		{name: "sessions", mode: ModeSessions, want: PaneBottom, ok: true},
		{name: "plans", mode: ModePlans, want: PaneBottom, ok: true},
		{name: "flows", mode: ModeFlows, want: PaneBottom, ok: true},
		{name: "active flows takeover", mode: ModeActiveFlows},
		{name: "beads ready", mode: ModeBeadsReady, want: PaneTop, ok: true},
		{name: "beads blocked", mode: ModeBeadsBlocked, want: PaneTop, ok: true},
		{name: "beads open", mode: ModeBeadsOpen, want: PaneTop, ok: true},
		{name: "beads in progress", mode: ModeBeadsInProgress, want: PaneTop, ok: true},
		{name: "beads closed", mode: ModeBeadsClosed, want: PaneTop, ok: true},
		{name: "negative", mode: Mode(-1)},
		{name: "zero", mode: 0},
		{name: "after valid range", mode: ModeBeadsClosed + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PaneForMode(tt.mode)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("PaneForMode(%d) = (%d, %t), want (%d, %t)", tt.mode, got, ok, tt.want, tt.ok)
			}
		})
	}
}
