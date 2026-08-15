package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
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
		wantOwned   bool
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
			wantOwned:   true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			baselines := map[string]flowstore.FlowRecord{key: oldFlow}
			owned := map[string]epicProgressionOwnedSuccessor{key: {SourceFlowID: oldFlow.FlowID, ChildID: "epic.2", FlowID: "owned-flow"}}
			m := Model{
				beadExpansion:                  beadExpansionSnapshot{target: beadExpansionTarget{token: 99}},
				epicProgressionBaselines:       baselines,
				epicProgressionOwnedSuccessors: owned,
				flowPreparationAdmission:       true,
			}
			released := false
			tt.msg.release = func() {
				released = true
				flow, found := baselines[key]
				if found != tt.wantPresent || flow.FlowID != tt.wantFlowID {
					t.Fatalf("baseline at release = %#v, %t; want Flow %q, present %t", flow, found, tt.wantFlowID, tt.wantPresent)
				}
				_, ownedPresent := owned[key]
				if ownedPresent != tt.wantOwned {
					t.Fatalf("owned successor at release = %t, want %t", ownedPresent, tt.wantOwned)
				}
			}
			next, _ := m.handleEpicProgressionToggleResult(tt.msg)
			if !released || next.flowPreparationAdmission {
				t.Fatalf("release called = %t, admission retained = %t", released, next.flowPreparationAdmission)
			}
			_, ownedPresent := next.epicProgressionOwnedSuccessors[key]
			if ownedPresent != tt.wantOwned {
				t.Fatalf("owned successor present = %t, want %t", ownedPresent, tt.wantOwned)
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
		epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
			key: {SourceFlowID: baseline.FlowID, ChildID: "epic.2", FlowID: "owned-flow"},
		},
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
	if got := next.epicProgressionOwnedSuccessors[key]; got.FlowID != "owned-flow" {
		t.Fatalf("confirmed-active disable replaced ownership with %#v", got)
	}
}

func TestDisableEpicProgressionReconciliationMatrix(t *testing.T) {
	target := beadExpansionTarget{repoPath: "/repo", epicID: "epic"}
	writeErr := errors.New("write failed")
	for _, tt := range []struct {
		name            string
		setErr          error
		found           bool
		read            flowstore.EpicProgression
		readErr         error
		wantKnown       bool
		wantEnabled     bool
		wantDisposition epicProgressionBaselineDisposition
		wantStatus      string
	}{
		{name: "success", found: true, wantKnown: true, wantDisposition: epicProgressionBaselineRemove, wantStatus: "Disabled auto-progression"},
		{name: "error reread absent", setErr: writeErr, wantKnown: true, wantDisposition: epicProgressionBaselineRemove, wantStatus: "is off; disable outcome could not be confirmed"},
		{name: "error reread normal off", setErr: writeErr, found: true, read: flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic"}, wantKnown: true, wantDisposition: epicProgressionBaselineRemove, wantStatus: "is off; disable outcome could not be confirmed"},
		{name: "error reread halted", setErr: writeErr, found: true, read: flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Halt: &flowstore.EpicProgressionHalt{ChildBeadID: "epic.1", Status: flowstore.StatusBlocked, Message: "blocked"}}, wantKnown: true, wantDisposition: epicProgressionBaselineRemove, wantStatus: "is halted; disable outcome could not be confirmed"},
		{name: "error reread done", setErr: writeErr, found: true, read: flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Done: true}, wantKnown: true, wantDisposition: epicProgressionBaselineRemove, wantStatus: "completed before disable could be confirmed"},
		{name: "error reread active", setErr: writeErr, found: true, read: flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Enabled: true}, wantKnown: true, wantEnabled: true, wantStatus: "Could not disable"},
		{name: "error reread error", setErr: writeErr, readErr: errors.New("read failed"), wantStatus: "Could not confirm auto-progression state"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				setEpicProgression: func(update flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
					if update.Enabled || update.Done {
						t.Fatalf("manual disable update = %#v, want enabled=false done=false", update)
					}
					return flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic"}, tt.setErr
				},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return tt.read, tt.found, tt.readErr
				},
			}
			msg, ok := m.disableEpicProgressionCmd(target)().(epicProgressionToggleResultMsg)
			if !ok {
				t.Fatal("disable command returned unexpected message")
			}
			if msg.known != tt.wantKnown || msg.enabled != tt.wantEnabled || msg.baselineDisposition != tt.wantDisposition || !strings.Contains(msg.status, tt.wantStatus) {
				t.Fatalf("disable result = %#v", msg)
			}
			if tt.read.Done && strings.Contains(msg.status, "Disabled auto-progression") {
				t.Fatalf("done reread claimed direct disable: %q", msg.status)
			}
		})
	}
}

func TestEnableEpicProgressionRevalidationFailureDoesNotInstallIneligibleBaseline(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)
	prepared := flowstore.FlowRecord{
		FlowID: "flow-child", RepoPath: "/repo", Status: flowstore.StatusPending,
		Bead: flowstore.BeadLink{ID: "epic.1", EpicID: "epic"}, PreparedAt: &stamp,
	}
	running := prepared
	running.Status = flowstore.StatusInProgress
	m := Model{
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{prepared}, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "epic.1", Title: "Child"}}, nil
		},
		reserveFlowPreparation: func(string) (flowstore.FlowRecord, func(), error) {
			return prepared, func() {}, nil
		},
		enableEpicProgression: func(flowstore.PreparedEpicProgressionUpdate) (flowstore.EpicProgression, flowstore.FlowRecord, error) {
			return flowstore.EpicProgression{}, running, errors.New("Flow is already running")
		},
		readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: key.RepoPath, EpicID: key.EpicID, Enabled: true}, true, nil
		},
	}
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	msg, ok := m.enableEpicProgressionCmd(target, ui.BeadExpansion{
		Children: []beadsquery.Bead{{ID: "epic.1", Title: "Child"}},
		ReadyIDs: map[string]bool{"epic.1": true},
	})().(epicProgressionToggleResultMsg)
	if !ok {
		t.Fatal("enable command returned unexpected message")
	}
	if !msg.known || !msg.enabled || msg.baselineDisposition != epicProgressionBaselinePreserve {
		t.Fatalf("revalidation result = %#v, want known active with preserved baseline", msg)
	}
	if !strings.Contains(msg.status, "enabling auto-progression failed") {
		t.Fatalf("revalidation status = %q, want failure", msg.status)
	}
}

func TestStaleEpicProgressionToggleReconcilesNewerSelectionOfSameEpic(t *testing.T) {
	stale := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	current := stale
	current.token = 2
	reads := 0
	m := Model{
		beadExpansion:            beadExpansionSnapshot{target: current},
		flowPreparationAdmission: true,
		readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			reads++
			return flowstore.EpicProgression{RepoPath: key.RepoPath, EpicID: key.EpicID, Enabled: true}, true, nil
		},
	}
	next, cmd := m.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target: stale, known: true, enabled: true,
	})
	messages := epicProgressionTestCommandMessages(cmd)
	if reads != 1 || len(messages) != 1 {
		t.Fatalf("stale same-epic reconciliation = %d reads, %d messages; want 1/1", reads, len(messages))
	}
	msg, ok := messages[0].(beadProgressionResultMsg)
	if !ok || msg.target != current || !msg.found || !msg.progression.Enabled {
		t.Fatalf("stale same-epic reconciliation message = %#v", messages[0])
	}
	if next.flowPreparationAdmission {
		t.Fatal("stale toggle retained preparation admission")
	}
}

func TestStaleEpicProgressionToggleRefreshesVisibleFlowSurface(t *testing.T) {
	listCalls := 0
	m := Model{
		activeFlowSurface:        true,
		flowPreparationAdmission: true,
		beadExpansion:            beadExpansionSnapshot{target: beadExpansionTarget{token: 2, repoPath: "/other", epicID: "other"}},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return nil, nil
		},
	}
	next, cmd := m.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target: beadExpansionTarget{token: 1, repoPath: "/repo", epicID: "epic"},
		flow:   flowstore.FlowRecord{FlowID: "prepared-flow"},
	})
	_ = epicProgressionTestCommandMessages(cmd)
	if listCalls != 1 {
		t.Fatalf("stale toggle Flow refresh calls = %d, want 1", listCalls)
	}
	if next.flowPreparationAdmission {
		t.Fatal("stale toggle retained preparation admission")
	}
}

func TestStaleEpicProgressionToggleOwnerCannotMutateNewerRuntimeState(t *testing.T) {
	key := epicProgressionBaselineKey("/repo", "epic")
	baseline := flowstore.FlowRecord{FlowID: "current-flow"}
	owned := epicProgressionOwnedSuccessor{SourceFlowID: baseline.FlowID, ChildID: "epic.2", FlowID: "owned-flow"}
	released := 0
	m := Model{
		flowPreparationAdmission: true,
		flowPreparationOwner:     flowPreparationOwner{Kind: flowPreparationEpicToggle, Token: 2},
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: baseline},
		epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
			key: owned,
		},
	}
	next, _ := m.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target:              beadExpansionTarget{repoPath: "/repo", epicID: "epic"},
		baselineDisposition: epicProgressionBaselineRemove,
		status:              "stale status",
		release:             func() { released++ },
		preparationKind:     flowPreparationEpicToggle,
		preparationToken:    1,
	})
	if released != 1 || next.flowPreparationOwner.Token != 2 || !next.flowPreparationAdmission {
		t.Fatalf("release=%d owner=%#v admission=%t", released, next.flowPreparationOwner, next.flowPreparationAdmission)
	}
	if next.epicProgressionBaselines[key].FlowID != baseline.FlowID || next.epicProgressionOwnedSuccessors[key] != owned {
		t.Fatalf("stale result changed baseline/ownership: %#v %#v", next.epicProgressionBaselines, next.epicProgressionOwnedSuccessors)
	}
	if next.status.Text != "" {
		t.Fatalf("stale result surfaced status %q", next.status.Text)
	}
}

func epicProgressionTestCommandMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var messages []tea.Msg
	for _, nested := range batch {
		messages = append(messages, epicProgressionTestCommandMessages(nested)...)
	}
	return messages
}
