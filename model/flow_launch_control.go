package model

import (
	"context"
	"errors"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/internal/launchcontrol"
)

// launchSweepInterval is how often the model asks the launch controller to
// sweep launch directories for exit evidence. Detached agents that exit
// without a result are reconciled within one interval while the TUI is up.
const launchSweepInterval = 30 * time.Second

// FlowControlAppliedMsg is sent by the process entry point when the launch
// controller applied a request or reconciled a launch, so the Flow surface
// refreshes now rather than on its next tick.
type FlowControlAppliedMsg struct {
	FlowID   string
	PhaseID  string
	LaunchID string
}

type launchSweepTickMsg struct {
	Generation uint64
}

type launchSweepDoneMsg struct {
	Generation uint64
}

// launchExitReconcileDoneMsg reports a reconciliation the model requested for
// an exited embedded terminal.
type launchExitReconcileDoneMsg struct {
	FlowID   string
	PhaseID  string
	LaunchID string
	Err      error
}

func (m Model) launchSweepTickCmd() tea.Cmd {
	if m.sweepLaunches == nil {
		return nil
	}
	generation := m.launchSweepTickGen
	return tea.Tick(launchSweepInterval, func(time.Time) tea.Msg {
		return launchSweepTickMsg{Generation: generation}
	})
}

// startLaunchSweep runs the controller's sweep as a command, off the render
// path, then re-arms the tick once it is done so sweeps never overlap.
func (m Model) startLaunchSweep() (Model, tea.Cmd) {
	if m.sweepLaunches == nil {
		return m, nil
	}
	m.launchSweepTickGen++
	generation := m.launchSweepTickGen
	sweep := m.sweepLaunches
	return m, func() tea.Msg {
		sweep()
		return launchSweepDoneMsg{Generation: generation}
	}
}

func (m Model) handleLaunchSweepDone(msg launchSweepDoneMsg) (Model, tea.Cmd) {
	if msg.Generation != m.launchSweepTickGen {
		return m, nil
	}
	return m, m.launchSweepTickCmd()
}

// reconcileExitedFlowEmbeddedTerminals emits one reconciliation command per
// Flow terminal that exited on its own — not one the user terminated — for a
// launch that carries a Flow, phase, and launch ID. Each slot is reconciled
// once; the controller decides whether the phase still needs attention, and
// it records the exit durably (exit.json) before anything that can fail, so a
// reconciliation that does not finish is retried by the sweep, not by a slot
// that has since been dismissed.
func (m Model) reconcileExitedFlowEmbeddedTerminals() (Model, []tea.Cmd) {
	if m.reconcileLaunchExit == nil {
		return m, nil
	}
	var cmds []tea.Cmd
	for i, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow || slot.Terminal == nil || slot.ExitReconciled {
			continue
		}
		if slot.FlowID == "" || slot.FlowPhaseID == "" || slot.LaunchID == "" {
			continue
		}
		state := slot.Terminal.State()
		if state != "exited" && state != "failed" {
			continue
		}
		m.embeddedTerminals[i].ExitReconciled = true
		cmds = append(cmds, m.reconcileLaunchExitCmd(slot))
	}
	return m, cmds
}

func (m Model) reconcileLaunchExitCmd(slot embeddedTerminalSlot) tea.Cmd {
	reconcile := m.reconcileLaunchExit
	terminal := slot.Terminal
	flowID, phaseID, launchID := slot.FlowID, slot.FlowPhaseID, slot.LaunchID
	return func() tea.Msg {
		ev := launchcontrol.ExitEvidence{Source: launchcontrol.SourceTerminalExit, EndedAt: time.Now().UTC()}
		ev.Code, ev.CodeKnown = embeddedTerminalExitCode(terminal)
		err := reconcile(flowID, phaseID, launchID, ev)
		return launchExitReconcileDoneMsg{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID, Err: err}
	}
}

// embeddedTerminalExitCode reads the exit status of a terminal that has
// already ended. Wait returns at once for an ended terminal; the timeout is a
// guard against a state that says exited before done is closed.
func embeddedTerminalExitCode(terminal EmbeddedTerminal) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := terminal.Wait(ctx)
	if err == nil {
		return 0, true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 0, false
	}
	return 0, false
}

func (m Model) handleLaunchExitReconcileDone(msg launchExitReconcileDoneMsg) (Model, tea.Cmd) {
	if msg.Err != nil {
		m = m.setStatus(statusOther, "Flow launch reconciliation failed: "+msg.Err.Error())
	}
	return m.startFlowSurfaceRefreshFetch()
}

// reconcileInteractiveLaunchExitCmd reports the end of an interactive
// (TTY-handover) tracked phase launch. Detached launches never reach here;
// their exit is the sweep's or a hook's to observe.
func (m Model) reconcileInteractiveLaunchExitCmd(ctx actions.AgentLaunchContext) tea.Cmd {
	if m.reconcileLaunchExit == nil || !ctx.FlowLaunchTracked {
		return nil
	}
	if ctx.FlowID == "" || ctx.FlowPhaseID == "" || ctx.LaunchID == "" {
		return nil
	}
	reconcile := m.reconcileLaunchExit
	flowID, phaseID, launchID := ctx.FlowID, ctx.FlowPhaseID, ctx.LaunchID
	return func() tea.Msg {
		err := reconcile(flowID, phaseID, launchID, launchcontrol.ExitEvidence{
			Source: launchcontrol.SourceTerminalExit, Code: 0, CodeKnown: true, EndedAt: time.Now().UTC(),
		})
		return launchExitReconcileDoneMsg{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID, Err: err}
	}
}
