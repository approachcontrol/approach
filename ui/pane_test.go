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

func TestStackedContentLayout(t *testing.T) {
	tests := []struct {
		name       string
		rows       int
		focus      Pane
		remembered Pane
		wantSplit  bool
		wantTop    int
		wantBottom int
	}{
		{name: "even split", rows: 20, focus: PaneTop, remembered: PaneTop, wantSplit: true, wantTop: 10, wantBottom: 10},
		{name: "odd split gives top extra", rows: 19, focus: PaneBottom, remembered: PaneBottom, wantSplit: true, wantTop: 10, wantBottom: 9},
		{name: "one below threshold degrades to focus", rows: 18, focus: PaneBottom, remembered: PaneTop, wantBottom: 18},
		{name: "repo focus uses remembered top", rows: 18, focus: PaneRepos, remembered: PaneTop, wantTop: 18},
		{name: "repo focus uses remembered bottom", rows: 18, focus: PaneRepos, remembered: PaneBottom, wantBottom: 18},
		{name: "zero rows", focus: PaneRepos, remembered: PaneTop},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StackedContentLayout(tt.rows, tt.focus, tt.remembered)
			if got.Split != tt.wantSplit || got.TopRows != tt.wantTop || got.BottomRows != tt.wantBottom {
				t.Fatalf("layout = split %t top %d bottom %d, want split %t top %d bottom %d", got.Split, got.TopRows, got.BottomRows, tt.wantSplit, tt.wantTop, tt.wantBottom)
			}
			if got.TopRows+got.BottomRows != max(tt.rows, 0) {
				t.Fatalf("allocations %d+%d do not consume %d rows", got.TopRows, got.BottomRows, tt.rows)
			}
		})
	}
}
