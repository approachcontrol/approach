package flowstore

import (
	"testing"
	"time"
)

func TestBeadFlowSlotOccupiedCoversEveryDerivedStatus(t *testing.T) {
	closedAt := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
	mergedAt := closedAt

	for _, tt := range []struct {
		name       string
		record     FlowRecord
		wantStatus string
		want       bool
	}{
		{
			name:       "pending holds the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Phases: []FlowPhase{{PhaseID: "plan", Status: PhasePending}}},
			wantStatus: StatusPending,
			want:       true,
		},
		{
			name: "in progress holds the slot",
			record: FlowRecord{Bead: BeadLink{ID: "child"}, Phases: []FlowPhase{
				{PhaseID: "plan", Status: PhaseCompleted},
				{PhaseID: "implementation", Status: PhaseRunning},
			}},
			wantStatus: StatusInProgress,
			want:       true,
		},
		{
			name:       "needs attention holds the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Phases: []FlowPhase{{PhaseID: "plan", Status: PhaseNeedsAttention}}},
			wantStatus: StatusNeedsAttention,
			want:       true,
		},
		{
			name:       "blocked holds the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Phases: []FlowPhase{{PhaseID: "plan", Status: PhaseBlocked}}},
			wantStatus: StatusBlocked,
			want:       true,
		},
		{
			name:       "completed releases the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Phases: []FlowPhase{{PhaseID: "plan", Status: PhaseCompleted}}},
			wantStatus: StatusCompleted,
			want:       false,
		},
		{
			name:       "merged releases the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Merge: Merge{Status: MergeMerged, Commit: "abc", MergedAt: &mergedAt}},
			wantStatus: StatusMerged,
			want:       false,
		},
		{
			name:       "abandoned releases the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Status: StatusAbandoned},
			wantStatus: StatusAbandoned,
			want:       false,
		},
		{
			name:       "closed releases the slot",
			record:     FlowRecord{Bead: BeadLink{ID: "child"}, Closed: Closure{Reason: "done", ClosedAt: &closedAt}},
			wantStatus: StatusClosed,
			want:       false,
		},
		{
			name: "closed wins over all phases complete and releases the slot",
			record: FlowRecord{
				Bead:   BeadLink{ID: "child"},
				Closed: Closure{Reason: "done", ClosedAt: &closedAt},
				Phases: []FlowPhase{{PhaseID: "plan", Status: PhaseCompleted}},
			},
			wantStatus: StatusClosed,
			want:       false,
		},
		{
			name:       "empty bead id never occupies a slot",
			record:     FlowRecord{Phases: []FlowPhase{{PhaseID: "plan", Status: PhasePending}}},
			wantStatus: StatusPending,
			want:       false,
		},
		{
			name:       "whitespace-only bead id never occupies a slot",
			record:     FlowRecord{Bead: BeadLink{ID: "   "}, Phases: []FlowPhase{{PhaseID: "plan", Status: PhasePending}}},
			wantStatus: StatusPending,
			want:       false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveStatus(tt.record); got != tt.wantStatus {
				t.Fatalf("DeriveStatus() = %q, want %q (fixture does not exercise the intended status)", got, tt.wantStatus)
			}
			if got := BeadFlowSlotOccupied(tt.record); got != tt.want {
				t.Fatalf("BeadFlowSlotOccupied() = %v, want %v", got, tt.want)
			}
		})
	}
}
