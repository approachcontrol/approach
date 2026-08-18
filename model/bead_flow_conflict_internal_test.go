package model

import (
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestConflictStatusNamesBeadStatusAndFlow(t *testing.T) {
	closedAt := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)

	for _, tt := range []struct {
		name   string
		record flowstore.FlowRecord
		remedy bool
		want   string
	}{
		{
			name: "in progress with remedy",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "approach-cwk"},
				Phases: []flowstore.FlowPhase{
					{PhaseID: "plan", Status: flowstore.PhaseCompleted},
					{PhaseID: "implementation", Status: flowstore.PhaseRunning},
				},
			},
			remedy: true,
			want:   "Bead approach-cwk already has a in progress flow 20260816T025735Z-approach-cwk: close it from the Flows view with C",
		},
		{
			name: "needs attention is humanized",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "approach-cwk"},
				Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseNeedsAttention}},
			},
			remedy: true,
			want:   "Bead approach-cwk already has a needs attention flow 20260816T025735Z-approach-cwk: close it from the Flows view with C",
		},
		{
			name: "blocked without remedy",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "approach-cwk"},
				Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseBlocked}},
			},
			want: "Bead approach-cwk already has a blocked flow 20260816T025735Z-approach-cwk",
		},
		{
			name: "pending without remedy",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "approach-cwk"},
				Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhasePending}},
			},
			want: "Bead approach-cwk already has a pending flow 20260816T025735Z-approach-cwk",
		},
		{
			name: "untrimmed stored bead id renders trimmed",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "  approach-cwk  "},
				Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhasePending}},
			},
			remedy: true,
			want:   "Bead approach-cwk already has a pending flow 20260816T025735Z-approach-cwk: close it from the Flows view with C",
		},
		{
			name: "closed flow still renders its derived status",
			record: flowstore.FlowRecord{
				FlowID: "20260816T025735Z-approach-cwk",
				Bead:   flowstore.BeadLink{ID: "approach-cwk"},
				Closed: flowstore.Closure{Reason: "done", ClosedAt: &closedAt},
			},
			want: "Bead approach-cwk already has a closed flow 20260816T025735Z-approach-cwk",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := conflictStatus(tt.record, tt.remedy); got != tt.want {
				t.Fatalf("conflictStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
