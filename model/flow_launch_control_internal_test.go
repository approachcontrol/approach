package model

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/launchcontrol"
	"github.com/approachcontrol/approach/scanner"
)

type recordingRegistrar struct {
	registrations []launchcontrol.Registration
	err           error
	endpoint      launchcontrol.Endpoint
}

func (r *recordingRegistrar) Register(reg launchcontrol.Registration) (launchcontrol.Endpoint, error) {
	r.registrations = append(r.registrations, reg)
	if r.err != nil {
		return launchcontrol.Endpoint{}, r.err
	}
	return r.endpoint, nil
}

func TestTrackedPhaseLaunchRegistersAndCarriesControlEndpoint(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	registrar := &recordingRegistrar{endpoint: launchcontrol.Endpoint{Path: "/tmp/approach-1/abcd.sock", Token: "tok"}}
	opts := h.options()
	opts.LaunchControl = registrar
	h.launch(h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts))
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
	}
	ctx := h.launchContexts[0]
	if ctx.ControlEndpoint != registrar.endpoint.Path || ctx.ControlToken != "tok" {
		t.Fatalf("control env not stamped: %#v", ctx)
	}
	if len(registrar.registrations) != 1 {
		t.Fatalf("registrations = %#v", registrar.registrations)
	}
	reg := registrar.registrations[0]
	if reg.FlowID != record.FlowID || reg.PhaseID != "implementation" || reg.LaunchID != ctx.LaunchID || reg.Kind != "phase" {
		t.Fatalf("registration = %#v", reg)
	}
}

func TestGenericAgentLaunchWithoutFlowDoesNotRegister(t *testing.T) {
	registrar := &recordingRegistrar{endpoint: launchcontrol.Endpoint{Path: "/tmp/x.sock", Token: "tok"}}
	m := NewWithOptions(nil, Options{LaunchControl: registrar})
	ctx := applyLaunchStamp(actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-1", SessionStateRoot: "/state"}, m.launchStamp())
	if ctx.ControlEndpoint != "" || ctx.ControlToken != "" || len(registrar.registrations) != 0 {
		t.Fatalf("Flow-less launch registered: %#v %#v", ctx, registrar.registrations)
	}
	// A Flow-scoped launch without a phase (repair, autofix, generic agent)
	// registers as unowned.
	ctx = applyLaunchStamp(actions.AgentLaunchContext{Command: "codex", LaunchID: "launch-2", FlowID: "flow-1", FlowAutofix: true}, m.launchStamp())
	if ctx.ControlEndpoint == "" || len(registrar.registrations) != 1 || registrar.registrations[0].PhaseID != "" || registrar.registrations[0].Kind != "autofix" {
		t.Fatalf("autofix registration = %#v (%#v)", registrar.registrations, ctx)
	}
}

func TestFailingRegistrarLeavesControlFieldsEmptyAndLaunchProceeds(t *testing.T) {
	record := manualLaunchFlowRecord()
	h := newManualLaunchHarness(t, record)
	registrar := &recordingRegistrar{err: errors.New("no socket")}
	opts := h.options()
	opts.LaunchControl = registrar
	h.launch(h.modelWith([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, opts))
	if len(h.launchContexts) != 1 {
		t.Fatalf("embedded launches = %d, want 1", len(h.launchContexts))
	}
	if ctx := h.launchContexts[0]; ctx.ControlEndpoint != "" || ctx.ControlToken != "" {
		t.Fatalf("failed registration stamped: %#v", ctx)
	}
	if len(registrar.registrations) != 1 {
		t.Fatalf("registrations = %#v", registrar.registrations)
	}
}

// exitedTerminal is a Flow terminal that has ended with the given error.
type exitedTerminal struct {
	state string
	err   error
}

func (t exitedTerminal) VisibleLines(width, height int) []string { return nil }
func (t exitedTerminal) Write(p []byte) (int, error)             { return len(p), nil }
func (t exitedTerminal) Resize(width, height int) error          { return nil }
func (t exitedTerminal) Terminate() error                        { return nil }
func (t exitedTerminal) Wait(context.Context) error              { return t.err }
func (t exitedTerminal) State() string                           { return t.state }

func exitCodeError(code int) error {
	return exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
}

func TestEmbeddedTerminalExitReconcilesTrackedLaunchOnce(t *testing.T) {
	type call struct {
		flowID, phaseID, launchID string
		ev                        launchcontrol.ExitEvidence
	}
	var calls []call
	m := NewWithOptions(nil, Options{
		ReconcileLaunchExit: func(flowID, phaseID, launchID string, ev launchcontrol.ExitEvidence) error {
			calls = append(calls, call{flowID, phaseID, launchID, ev})
			return nil
		},
	})
	m.embeddedTerminals = []embeddedTerminalSlot{
		{Number: 1, ID: 1, Scope: embeddedTerminalScopeFlow, FlowID: "flow-1", FlowPhaseID: "plan", LaunchID: "launch-1", Terminal: exitedTerminal{state: "exited"}},
		{Number: 2, ID: 2, Scope: embeddedTerminalScopeFlow, FlowID: "flow-1", FlowPhaseID: "plan-review", LaunchID: "launch-2", Terminal: exitedTerminal{state: "failed", err: exitCodeError(3)}},
		{Number: 3, ID: 3, Scope: embeddedTerminalScopeFlow, FlowID: "flow-1", FlowPhaseID: "implementation", LaunchID: "launch-3", Terminal: exitedTerminal{state: "terminated"}},
		{Number: 4, ID: 4, Scope: embeddedTerminalScopeFlow, FlowID: "flow-1", LaunchID: "launch-4", FlowRepair: true, Terminal: exitedTerminal{state: "exited"}},
		{Number: 5, ID: 5, Scope: embeddedTerminalScopeSession, LaunchID: "launch-5", Terminal: exitedTerminal{state: "exited"}},
	}
	next, cmds := m.reconcileExitedFlowEmbeddedTerminals()
	if len(cmds) != 2 {
		t.Fatalf("reconcile commands = %d, want 2 (exited + failed tracked launches)", len(cmds))
	}
	for _, cmd := range cmds {
		if _, ok := cmd().(launchExitReconcileDoneMsg); !ok {
			t.Fatal("reconcile command returned the wrong message")
		}
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	byLaunch := map[string]call{}
	for _, c := range calls {
		byLaunch[c.launchID] = c
	}
	if c := byLaunch["launch-1"]; c.flowID != "flow-1" || c.phaseID != "plan" || c.ev.Source != launchcontrol.SourceTerminalExit || !c.ev.CodeKnown || c.ev.Code != 0 {
		t.Fatalf("launch-1 call = %#v", c)
	}
	if c := byLaunch["launch-2"]; c.ev.Code != 3 || !c.ev.CodeKnown {
		t.Fatalf("launch-2 call = %#v", c)
	}
	// Once per slot: a second pass finds nothing new.
	if _, again := next.reconcileExitedFlowEmbeddedTerminals(); len(again) != 0 {
		t.Fatalf("second pass emitted %d commands", len(again))
	}
}

func TestFlowControlAppliedMsgRefreshesTheFlowSurface(t *testing.T) {
	calls := 0
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			calls++
			return []flowstore.FlowRecord{flowForRefreshTest("flow-1")}, nil
		},
	})
	// Settle the startup fetch so the applied message is what starts the next one.
	m, _ = updateFlowRefreshTest(m, flowResultFromCommand(t, m.Init()))
	before := calls
	next, cmd := m.Update(FlowControlAppliedMsg{FlowID: "flow-1", PhaseID: "plan", LaunchID: "launch-1"})
	m = next.(Model)
	if cmd == nil || m.flowRefreshInFlight == 0 {
		t.Fatal("applied message started no fetch")
	}
	flowResultFromCommand(t, cmd)
	if calls <= before {
		t.Fatal("applied message did not list flows")
	}
}

func TestLaunchSweepTickRunsSweepOffTheRenderPathAndRearms(t *testing.T) {
	sweeps := 0
	m := NewWithOptions(nil, Options{SweepLaunches: func() { sweeps++ }})
	if m.launchSweepTickCmd() == nil {
		t.Fatal("no sweep tick with a sweep seam")
	}
	next, cmd := m.Update(launchSweepTickMsg{Generation: m.launchSweepTickGen})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("tick started no sweep")
	}
	msg := cmd()
	done, ok := msg.(launchSweepDoneMsg)
	if !ok || sweeps != 1 {
		t.Fatalf("sweep command = %#v, sweeps = %d", msg, sweeps)
	}
	if _, cmd = m.Update(done); cmd == nil {
		t.Fatal("sweep completion did not re-arm the tick")
	}
	if NewWithOptions(nil, Options{}).launchSweepTickCmd() != nil {
		t.Fatal("tick armed without a sweep seam")
	}
}
