package model

import (
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestRejectEpicProgressionCandidateOrdersTerminalAndPreparationClasses(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC)
	base := flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusPending, PreparedAt: &stamp}
	tests := []struct {
		name string
		flow flowstore.FlowRecord
		want string
	}{
		{name: "adoptable pending", flow: base, want: ""},
		{name: "incomplete", flow: func() flowstore.FlowRecord { f := base; f.PreparedAt = nil; return f }(), want: "preparation is incomplete"},
		{name: "running", flow: func() flowstore.FlowRecord { f := base; f.Status = flowstore.StatusInProgress; return f }(), want: "is in_progress"},
		{name: "failed", flow: func() flowstore.FlowRecord { f := base; f.Status = flowstore.StatusBlocked; return f }(), want: "is blocked"},
		{name: "success", flow: func() flowstore.FlowRecord { f := base; f.Status = flowstore.StatusCompleted; return f }(), want: "is completed"},
		{name: "unknown", flow: func() flowstore.FlowRecord { f := base; f.Status = "future"; return f }(), want: "is future"},
		{name: "closure wins", flow: func() flowstore.FlowRecord {
			f := base
			f.Status = flowstore.StatusBlocked
			f.Closed = flowstore.Closure{Reason: "done", ClosedAt: &stamp}
			return f
		}(), want: "is closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rejectEpicProgressionCandidate(tt.flow)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("classification = %q, want substring %q", got, tt.want)
			}
		})
	}
}
