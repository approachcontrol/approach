package model

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
)

func TestFlowLaunchLeaseIsTokenOwned(t *testing.T) {
	m := Model{}
	var ok bool
	m, ok = m.acquireFlowLaunchLease("flow-1", "token-a", flowLaunchSourcePhase)
	if !ok {
		t.Fatal("first acquire failed")
	}
	if _, ok := m.acquireFlowLaunchLease("flow-1", "token-b", flowLaunchSourceWorktreeAgent); ok {
		t.Fatal("occupied Flow accepted a second lease")
	}
	m = m.releaseFlowLaunchLease("flow-1", "token-b")
	if lease, ok := m.flowLaunchLease("flow-1"); !ok || lease.Token != "token-a" || lease.Source != flowLaunchSourcePhase {
		t.Fatalf("mismatched release changed lease: %#v, %v", lease, ok)
	}
	m = m.releaseFlowLaunchLease("flow-1", "token-a")
	if _, ok := m.flowLaunchLease("flow-1"); ok {
		t.Fatal("matching release retained lease")
	}
}

func TestExternalFlowLaunchLeaseHeldUntilHandoffResult(t *testing.T) {
	m := NewWithOptions(nil, Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease("flow-1", "launch-1", flowLaunchSourcePhase)
	if !acquired {
		t.Fatal("failed to acquire initial Flow launch lease")
	}

	nextModel, cmd := m.Update(PlanLaunchRequestedMsg{
		LaunchContext: actions.AgentLaunchContext{
			Command:     "codex-app",
			FlowID:      "flow-1",
			FlowPhaseID: "plan",
			LaunchID:    "launch-1",
		},
		FlowLeaseSource: flowLaunchSourcePhase,
		FlowLeaseID:     "flow-1",
		FlowLeaseToken:  "launch-1",
	})
	next := nextModel.(Model)
	if cmd == nil {
		t.Fatal("external Flow launch returned nil handoff command")
	}
	if !next.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("external Flow launch released its lease before the handoff command ran")
	}

	result := cmd()
	afterModel, _ := next.Update(result)
	after := afterModel.(Model)
	if after.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("successful external handoff result retained the Flow launch lease")
	}
}

func TestExternalFlowLaunchFailureHoldsLeaseUntilFailurePersistence(t *testing.T) {
	var phaseUpdate flowstore.PhaseUpdate
	m := NewWithOptions(nil, Options{
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{Cmd: exec.Command("false")}, nil
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdate = update
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	})
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease("flow-1", "launch-1", flowLaunchSourcePhase)
	if !acquired {
		t.Fatal("failed to acquire initial Flow launch lease")
	}

	nextModel, launchCmd := m.Update(PlanLaunchRequestedMsg{
		LaunchContext: actions.AgentLaunchContext{
			Command:     "codex-app",
			FlowID:      "flow-1",
			FlowPhaseID: "plan",
			LaunchID:    "launch-1",
		},
		FlowLeaseSource: flowLaunchSourcePhase,
		FlowLeaseID:     "flow-1",
		FlowLeaseToken:  "launch-1",
	})
	next := nextModel.(Model)
	result := launchCmd()

	failingModel, persistCmd := next.Update(result)
	failing := failingModel.(Model)
	if persistCmd == nil {
		t.Fatal("failed external handoff returned nil failure-persistence command")
	}
	if !failing.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("failed external handoff released its lease before failure persistence")
	}

	persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, persistCmd)
	finalModel, _ := failing.Update(persisted)
	final := finalModel.(Model)
	if final.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("persisted external handoff failure retained the Flow launch lease")
	}
	if phaseUpdate.FlowID != "flow-1" || phaseUpdate.PhaseID != "plan" || phaseUpdate.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("external handoff failure phase update = %#v", phaseUpdate)
	}
}

func TestEmbeddedFlowLaunchFailureHoldsLeaseUntilFailurePersistence(t *testing.T) {
	m := NewWithOptions(nil, Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return nil, errors.New("pty unavailable")
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	})
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease("flow-1", "launch-1", flowLaunchSourcePhase)
	if !acquired {
		t.Fatal("failed to acquire initial Flow launch lease")
	}

	nextModel, persistCmd := m.Update(FlowEmbeddedLaunchRequestedMsg{
		LaunchContext: actions.AgentLaunchContext{
			Command:     "codex",
			FlowID:      "flow-1",
			FlowPhaseID: "plan",
			LaunchID:    "launch-1",
		},
		FlowLeaseSource: flowLaunchSourcePhase,
		FlowLeaseID:     "flow-1",
		FlowLeaseToken:  "launch-1",
	})
	next := nextModel.(Model)
	if persistCmd == nil {
		t.Fatal("embedded startup failure returned nil failure-persistence command")
	}
	if !next.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("embedded startup failure released its lease before failure persistence")
	}

	persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, persistCmd)
	finalModel, _ := next.Update(persisted)
	final := finalModel.(Model)
	if final.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("persisted embedded startup failure retained the Flow launch lease")
	}
}

func TestEmbeddedFlowPhaseResumeFailureHoldsLeaseUntilFailurePersistence(t *testing.T) {
	ctx := actions.AgentLaunchContext{
		Command: "codex", FlowID: "flow-1", FlowPhaseID: "implementation", LaunchID: "launch-1", FlowLaunchTracked: true,
	}
	key, ok := newFlowPhaseResumeKey(ctx.FlowID, ctx.FlowPhaseID)
	if !ok {
		t.Fatal("failed to build Flow phase resume key")
	}
	m := NewWithOptions(nil, Options{
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return nil, errors.New("pty unavailable")
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	})
	m.pendingFlowPhaseResumes = map[flowPhaseResumeKey]string{key: ctx.LaunchID}
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease(ctx.FlowID, ctx.LaunchID, flowLaunchSourcePhaseResume)
	if !acquired {
		t.Fatal("failed to acquire Flow phase resume lease")
	}

	next, persistCmd := m.handleFlowPhaseResumePersisted(flowPhaseResumePersistedMsg{
		LaunchContext: ctx,
		Flow: flowstore.FlowRecord{FlowID: ctx.FlowID, Phases: []flowstore.FlowPhase{{
			PhaseID: ctx.FlowPhaseID, Status: flowstore.PhaseRunning,
		}}},
	})
	if persistCmd == nil {
		t.Fatal("phase-resume startup failure returned nil failure-persistence command")
	}
	if !next.flowLaunchLeaseOccupied(ctx.FlowID) {
		t.Fatal("phase-resume startup failure released its lease before failure persistence")
	}

	persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, persistCmd)
	finalModel, _ := next.Update(persisted)
	final := finalModel.(Model)
	if final.flowLaunchLeaseOccupied(ctx.FlowID) {
		t.Fatal("persisted phase-resume startup failure retained the Flow launch lease")
	}
}

func TestStaleExternalFlowLaunchResultCannotMutateNewerLease(t *testing.T) {
	phaseUpdates := 0
	m := NewWithOptions(nil, Options{
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates++
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	})
	var acquired bool
	m, acquired = m.acquireFlowLaunchLease("flow-1", "launch-b", flowLaunchSourcePhase)
	if !acquired {
		t.Fatal("failed to acquire newer Flow launch lease")
	}

	next, cmd := m.handleAgentResultAfterFinalization(AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{
			Command: "codex-app", FlowID: "flow-1", FlowPhaseID: "plan", LaunchID: "launch-a",
		},
		Err:      "stale handoff failed",
		Detached: true,
	}, nil)
	if cmd != nil {
		t.Fatal("stale external Flow result scheduled phase failure persistence")
	}
	if phaseUpdates != 0 {
		t.Fatalf("stale external Flow result persisted %d phase updates", phaseUpdates)
	}
	lease, ok := next.flowLaunchLease("flow-1")
	if !ok || lease.Token != "launch-b" {
		t.Fatalf("newer Flow lease = %#v, present %v", lease, ok)
	}
}
