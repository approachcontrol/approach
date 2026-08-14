package model

import (
	"errors"
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

func TestEpicProgressionToggleReconcilesRuntimeBaselineBeforeReservationRelease(t *testing.T) {
	target := beadExpansionTarget{repoPath: "/repo", epicID: "epic"}
	key := epicProgressionBaselineKey(target.repoPath, target.epicID)
	oldFlow := flowstore.FlowRecord{FlowID: "old-flow"}
	newFlow := flowstore.FlowRecord{FlowID: "new-flow"}
	for _, tt := range []struct {
		name        string
		msg         epicProgressionToggleResultMsg
		wantFlowID  string
		wantPresent bool
	}{
		{
			name: "confirmed enable installs baseline",
			msg: epicProgressionToggleResultMsg{
				target: target, flow: newFlow, known: true, enabled: true, baselineDisposition: epicProgressionBaselineReplace,
			},
			wantFlowID:  newFlow.FlowID,
			wantPresent: true,
		},
		{
			name: "confirmed disable removes baseline",
			msg: epicProgressionToggleResultMsg{
				target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
			},
		},
		{
			name: "unknown outcome preserves possible live baseline",
			msg: epicProgressionToggleResultMsg{
				target: target, known: false,
			},
			wantFlowID:  oldFlow.FlowID,
			wantPresent: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			baselines := map[string]flowstore.FlowRecord{key: oldFlow}
			m := Model{
				beadExpansion:            beadExpansionSnapshot{target: beadExpansionTarget{token: 99}},
				epicProgressionBaselines: baselines,
				flowPreparationAdmission: true,
			}
			released := false
			tt.msg.release = func() {
				released = true
				flow, found := baselines[key]
				if found != tt.wantPresent || flow.FlowID != tt.wantFlowID {
					t.Fatalf("baseline at release = %#v, %t; want Flow %q, present %t", flow, found, tt.wantFlowID, tt.wantPresent)
				}
			}
			next, _ := m.handleEpicProgressionToggleResult(tt.msg)
			if !released || next.flowPreparationAdmission {
				t.Fatalf("release called = %t, admission retained = %t", released, next.flowPreparationAdmission)
			}
		})
	}
}

func TestDisableEpicProgressionConfirmedActivePreservesRuntimeBaseline(t *testing.T) {
	target := beadExpansionTarget{repoPath: "/repo", epicID: "epic"}
	key := epicProgressionBaselineKey(target.repoPath, target.epicID)
	baseline := flowstore.FlowRecord{FlowID: "prepared-flow"}
	m := Model{
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{}, errors.New("commit failed")
		},
		readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: key.RepoPath, EpicID: key.EpicID, Enabled: true}, true, nil
		},
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: baseline},
		flowPreparationAdmission: true,
		beadExpansion:            beadExpansionSnapshot{target: beadExpansionTarget{token: 99}},
	}
	msg, ok := m.disableEpicProgressionCmd(target)().(epicProgressionToggleResultMsg)
	if !ok {
		t.Fatal("disable command returned unexpected message")
	}
	next, _ := m.handleEpicProgressionToggleResult(msg)
	if got := next.epicProgressionBaselines[key]; got.FlowID != baseline.FlowID {
		t.Fatalf("confirmed-active disable replaced baseline with %#v", got)
	}
}
