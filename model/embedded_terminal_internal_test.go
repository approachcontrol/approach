package model

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model/modal"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

type internalFakeEmbeddedTerminal struct {
	state string
	lines []string
}

// repairEmbeddedLaunchTestModel builds the Model and the reserved repair
// attempt the launch lifecycle holds when it reaches the install stage, which
// is where a repair terminal is opened now that R submits an intent.
func repairEmbeddedLaunchTestModel(ctx actions.AgentLaunchContext) (Model, flowLaunchAttempt) {
	record := repairClassificationRecord(flowstore.FlowPhase{
		PhaseID: "implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseBlocked,
	})
	record.FlowID = ctx.FlowID
	m := modelWithModeForTest(Model{height: 24, activePane: ui.PaneBottom}, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	attempt := flowLaunchAttempt{
		Token:  ctx.LaunchID,
		Kind:   flowLaunchKindRepair,
		FlowID: ctx.FlowID,
		State:  flowLaunchStatePreparing,
		// handleFlowLaunchEvent deliberately never marks a repair attempt: repair
		// runs no AddPhaseLaunchID, so there is no persisted phase for a failure
		// to correct. Leaving this false is what production produces, and
		// failFlowLaunch's repair branch does not read it either way.
		MutatedPhase: false,
	}
	m, _ = m.reserveFlowLaunchAttempt(attempt, flowLaunchStatePreparing)
	return m, attempt
}

// installRepairLaunch runs the install stage for a prepared repair, the hop
// used by the lifecycle after the obsolete direct-launch message was removed.
func installRepairLaunch(m Model, attempt flowLaunchAttempt, ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
	return m.installFlowLaunchEmbedded(attempt, flowLaunchEventMsg{
		Token:   ctx.LaunchID,
		Kind:    flowLaunchKindRepair,
		From:    flowLaunchStatePreparing,
		FlowID:  ctx.FlowID,
		Stage:   flowLaunchStagePrepared,
		Context: ctx,
		Route:   flowLaunchRouteEmbedded,
	})
}

// openFlowEmbeddedTerminalForTest exercises runtime-only slot, prefill, focus,
// and tick mechanics without reintroducing a production Flow launch route.
func openFlowEmbeddedTerminalForTest(m Model, ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
	ctx.Embedded = true
	needsTick := !m.hasRunningEmbeddedTerminal()
	next, opened, err, prefillCmd := m.openFlowEmbeddedTerminal(ctx)
	if err != nil || !opened {
		return next, nil
	}
	if prefillCmd == nil {
		next = next.updateFlowTerminalFocusAfterLaunch(ctx)
	}
	var tickCmd tea.Cmd
	if needsTick {
		next, tickCmd = next.startEmbeddedTerminalTick()
	}
	return next, batchNonNil(prefillCmd, tickCmd)
}

func TestFlowRepairTerminalSlotPreservesRepairIdentityBeforePrefill(t *testing.T) {
	ctx := actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "repair-launch",
		FlowID:        "flow-1",
		FlowRepair:    true,
		Embedded:      true,
		InitialPrompt: "Repair it",
	}
	m, attempt := repairEmbeddedLaunchTestModel(ctx)
	m.startEmbeddedTerminal = func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
		return internalFakeEmbeddedTerminal{}, nil
	}
	next, cmd := installRepairLaunch(m, attempt, ctx)
	if cmd == nil {
		t.Fatal("interactive repair should return a prefill command")
	}
	if _, held := next.flowLaunchAttempt(ctx.FlowID); held {
		t.Fatal("an installed repair slot owns the Flow; its attempt must be released")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %#v, want one repair slot", next.embeddedTerminals)
	}
	slot := next.embeddedTerminals[0]
	if !slot.FlowRepair || slot.FlowPhaseID != "" || slot.Identity != "repair" || !slot.PrefillPending {
		t.Fatalf("repair slot = %#v, want explicit pending repair identity", slot)
	}
	prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, cmd)
	afterModel, _ := next.Update(prefillCmd())
	after := afterModel.(Model)
	if after.embeddedTerminals[0].PrefillPending || after.terminalFocus != terminalFocusTerminal {
		t.Fatalf("successful interactive repair prefill did not activate and focus slot: %#v", after.embeddedTerminals[0])
	}
}

type repairPrefillFailureTerminal struct {
	internalFakeEmbeddedTerminal
	terminated bool
}

func (t *repairPrefillFailureTerminal) Write([]byte) (int, error) {
	return 0, errors.New("prefill write failed")
}

func (t *repairPrefillFailureTerminal) Terminate() error {
	t.terminated = true
	t.state = "terminated"
	return nil
}

func TestFlowRepairLaunchFailuresNeverMutatePhaseOrAllowAutoDrain(t *testing.T) {
	ctx := actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "repair-launch",
		FlowID:        "flow-1",
		FlowRepair:    true,
		Embedded:      true,
		InitialPrompt: "Repair it",
	}

	t.Run("startup", func(t *testing.T) {
		phaseUpdates := 0
		m, attempt := repairEmbeddedLaunchTestModel(ctx)
		m.setFlowPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates++
			return flowstore.FlowRecord{}, nil
		}
		m.launchSeams.SetPhase = m.setFlowPhase
		m.startEmbeddedTerminal = func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return nil, errors.New("startup failed")
		}
		next, _ := installRepairLaunch(m, attempt, ctx)
		if phaseUpdates != 0 || len(next.pendingRepairAutoDrainFlowIDs) != 0 || !strings.Contains(next.visibleStatusText(), "startup failed") {
			t.Fatalf("startup failure: updates=%d markers=%#v status=%q", phaseUpdates, next.pendingRepairAutoDrainFlowIDs, next.visibleStatusText())
		}
		if _, held := next.flowLaunchAttempt(ctx.FlowID); held {
			t.Fatal("a failed repair install must release the Flow, not strand it")
		}
	})

	t.Run("capacity", func(t *testing.T) {
		phaseUpdates := 0
		m, attempt := repairEmbeddedLaunchTestModel(ctx)
		m.setFlowPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates++
			return flowstore.FlowRecord{}, nil
		}
		m.launchSeams.SetPhase = m.setFlowPhase
		for i := 1; i <= 9; i++ {
			m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{Number: i, ID: embeddedTerminalID(i), Terminal: internalFakeEmbeddedTerminal{}})
		}
		next, _ := installRepairLaunch(m, attempt, ctx)
		if phaseUpdates != 0 || len(next.pendingRepairAutoDrainFlowIDs) != 0 || !strings.Contains(next.visibleStatusText(), "Maximum embedded terminals") {
			t.Fatalf("capacity failure: updates=%d markers=%#v status=%q", phaseUpdates, next.pendingRepairAutoDrainFlowIDs, next.visibleStatusText())
		}
	})

	t.Run("prefill", func(t *testing.T) {
		phaseUpdates := 0
		term := &repairPrefillFailureTerminal{}
		m, attempt := repairEmbeddedLaunchTestModel(ctx)
		m.setFlowPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseUpdates++
			return flowstore.FlowRecord{}, nil
		}
		m.launchSeams.SetPhase = m.setFlowPhase
		m.startEmbeddedTerminal = func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return term, nil
		}
		next, cmd := installRepairLaunch(m, attempt, ctx)
		prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, cmd)
		afterModel, _ := next.Update(prefillCmd())
		after := afterModel.(Model)
		marker, suppressed := after.pendingRepairAutoDrainFlowIDs[ctx.FlowID]
		if !term.terminated || len(after.embeddedTerminals) != 0 || phaseUpdates != 0 || !suppressed || marker.CleanExit {
			t.Fatalf("prefill failure: terminated=%v slots=%#v updates=%d markers=%#v", term.terminated, after.embeddedTerminals, phaseUpdates, after.pendingRepairAutoDrainFlowIDs)
		}
		if !strings.Contains(after.visibleStatusText(), "prefill write failed") {
			t.Fatalf("prefill failure status = %q", after.visibleStatusText())
		}
	})
}

func (t internalFakeEmbeddedTerminal) VisibleLines(int, int) []string {
	return append([]string(nil), t.lines...)
}
func (t internalFakeEmbeddedTerminal) Write(p []byte) (int, error) { return len(p), nil }
func (t internalFakeEmbeddedTerminal) Resize(int, int) error       { return nil }
func (t internalFakeEmbeddedTerminal) Terminate() error            { return nil }
func (t internalFakeEmbeddedTerminal) Wait(context.Context) error  { return nil }
func (t internalFakeEmbeddedTerminal) State() string {
	if t.state == "" {
		return "running"
	}
	return t.state
}

func TestFlowEmbeddedTerminalIdentityAutofix(t *testing.T) {
	valid := actions.AgentLaunchContext{
		FlowID:                 "flow-1",
		WorktreePath:           "/dev/alpha/worktrees/flow-1",
		FlowAutofix:            true,
		FlowAutofixPRNumber:    116,
		Embedded:               true,
		InitialPrompt:          "autofix pr #116",
		FlowLaunchTracked:      false,
		FlowPhaseID:            "",
		FlowRepair:             false,
		FlowAgent:              false,
		FlowSavedSessionResume: false,
	}

	for _, tc := range []struct {
		name   string
		mutate func(*actions.AgentLaunchContext)
		want   string
	}{
		{name: "valid", want: "autofix"},
		{name: "repair precedence", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowRepair = true
		}, want: "repair"},
		{name: "generic agent precedence", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowAgent = true
		}, want: "agent"},
		{name: "saved session precedence", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowSavedSessionResume = true
			ctx.ResumeSessionID = "abcdefgh12345678"
		}, want: "session abcdefgh"},
		{name: "tracked phase precedence", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowPhaseID = "implementation"
			ctx.FlowLaunchTracked = true
		}, want: "implementation"},
		{name: "untracked phase identity", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowPhaseID = "review-loop"
		}, want: "review-loop"},
		{name: "zero PR number", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowAutofixPRNumber = 0
		}, want: "flow-1"},
		{name: "negative PR number", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowAutofixPRNumber = -1
		}, want: "flow-1"},
		{name: "PR metadata without autofix role", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowAutofix = false
		}, want: "flow-1"},
		{name: "whitespace Flow ID", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowID = " \t "
		}, want: "flow-1"},
		{name: "whitespace phase ID", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowPhaseID = " \t "
		}, want: "flow-1"},
		{name: "not embedded", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.Embedded = false
		}, want: "flow-1"},
		{name: "headless", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.Headless = true
		}, want: "autofix pr 116"},
		{name: "tracked without phase", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowLaunchTracked = true
		}, want: "flow-1"},
		{name: "malformed scope falls back to worktree", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowID = ""
			ctx.WorktreePath = "/dev/alpha/worktrees/fallback"
		}, want: "fallback"},
		{name: "malformed scope falls back to flow", mutate: func(ctx *actions.AgentLaunchContext) {
			ctx.FlowID = ""
			ctx.WorktreePath = ""
		}, want: "flow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := valid
			if tc.mutate != nil {
				tc.mutate(&ctx)
			}
			if got := flowEmbeddedTerminalIdentity(ctx); got != tc.want {
				t.Fatalf("flowEmbeddedTerminalIdentity(%#v) = %q, want %q", ctx, got, tc.want)
			}
		})
	}
}

func TestEmbeddedTerminalNumbersAreGlobalAcrossScopes(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		}}}

	var opened bool
	var err error
	m, opened, err, _ = m.openEmbeddedTerminalWithLabel(actions.AgentLaunchContext{}, embeddedTerminalScopeSession, "codex", "session", "", "", 80, 20)
	if err != nil || !opened {
		t.Fatalf("open session terminal: opened=%v err=%v", opened, err)
	}
	m, opened, err, _ = m.openEmbeddedTerminalWithLabel(actions.AgentLaunchContext{}, embeddedTerminalScopeFlow, "claude", "implementation", "flow-1", "implementation", 80, 20)
	if err != nil || !opened {
		t.Fatalf("open Flow terminal: opened=%v err=%v", opened, err)
	}

	if len(m.embeddedTerminals) != 2 {
		t.Fatalf("embedded terminals = %#v, want two slots", m.embeddedTerminals)
	}
	if got := []int{m.embeddedTerminals[0].Number, m.embeddedTerminals[1].Number}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("terminal numbers = %v, want global numbers [1 2]", got)
	}
}

func TestDismissEmbeddedTerminalRenumbersGloballyAndPreservesActiveSlot(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		activeTerminalNum: 3,
		embeddedTerminals: []embeddedTerminalSlot{
			{Number: 1, Scope: embeddedTerminalScopeSession, Identity: "session-one", ID: 1, Terminal: internalFakeEmbeddedTerminal{}},
			{Number: 2, Scope: embeddedTerminalScopeFlow, Identity: "implementation", ID: 2, Terminal: internalFakeEmbeddedTerminal{}},
			{Number: 3, Scope: embeddedTerminalScopeSession, Identity: "session-two", ID: 3, Terminal: internalFakeEmbeddedTerminal{}},
		}}}

	m = m.dismissEmbeddedTerminal(2)

	if got := []int{m.embeddedTerminals[0].Number, m.embeddedTerminals[1].Number}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("terminal numbers after dismiss = %v, want [1 2]", got)
	}
	active, _, ok := m.activeTerminal()
	if !ok || active.ID != 3 || active.Number != 2 {
		t.Fatalf("active terminal after dismiss = %#v, %v; want surviving ID 3 renumbered to 2", active, ok)
	}
}

func TestEmbeddedTerminalPresentationUsesOneActiveTerminalAcrossScopes(t *testing.T) {
	m := Model{
		height: 24, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			activeTerminalNum:   2,
			embeddedTerminals: []embeddedTerminalSlot{
				{Number: 1, Scope: embeddedTerminalScopeSession, Identity: "session", ID: 1, Terminal: internalFakeEmbeddedTerminal{lines: []string{"session output"}}},
				{Number: 2, Scope: embeddedTerminalScopeFlow, Identity: "implementation", ID: 2, Terminal: internalFakeEmbeddedTerminal{lines: []string{"flow output"}}},
			}}}

	tabs := m.embeddedTerminalTabs()
	if len(tabs) != 2 || tabs[0].Active || !tabs[1].Active {
		t.Fatalf("unified tabs = %#v, want only Flow terminal 2 active", tabs)
	}
	if lines := m.embeddedTerminalLines(); len(lines) != 1 || lines[0] != "flow output" {
		t.Fatalf("unified lines = %#v, want active Flow output", lines)
	}
}

func TestFirstAndLastEmbeddedTerminalReflowSessionSelection(t *testing.T) {
	records := make([]sessions.SessionRecord, 14)
	for i := range records {
		records[i] = sessions.SessionRecord{SessionID: fmt.Sprintf("session-%02d", i)}
	}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		width:       160,
		height:      20 + ui.TerminalChipRows,
		sessions:    newSessionPane().SetItems(records).Move(13, 14, 80), embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
				return internalFakeEmbeddedTerminal{}, nil
			}}}
	if got := m.sessions.Scroll(); got != 0 {
		t.Fatalf("session scroll before opening terminal = %d, want 0", got)
	}

	next, opened, err, _ := m.openEmbeddedTerminalWithLabel(actions.AgentLaunchContext{}, embeddedTerminalScopeSession, "codex", "session", "", "", 80, 20)
	if err != nil || !opened {
		t.Fatalf("open first terminal: opened=%v err=%v", opened, err)
	}
	if selected, scroll, height := next.sessions.SelectedIndex(), next.sessions.Scroll(), next.sessionContentHeight(); selected < scroll || selected >= scroll+height {
		t.Fatalf("selected session %d is outside dock viewport [%d, %d)", selected, scroll, scroll+height)
	}

	next = next.dismissEmbeddedTerminal(next.embeddedTerminals[0].ID)
	if selected, scroll, height := next.sessions.SelectedIndex(), next.sessions.Scroll(), next.sessionContentHeight(); selected < scroll || selected >= scroll+height {
		t.Fatalf("selected session %d is outside restored stacked viewport [%d, %d)", selected, scroll, scroll+height)
	}
}

func TestRepoContentHeightAccountsForTerminalDockState(t *testing.T) {
	m := Model{height: 24, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true}}
	if got := m.repoContentHeight(); got != 20 {
		t.Fatalf("no-terminals repo height = %d, want 20 (chip row reserved)", got)
	}
	m.embeddedTerminals = []embeddedTerminalSlot{{Number: 1, Scope: embeddedTerminalScopeSession, Terminal: internalFakeEmbeddedTerminal{}}}
	if got := m.repoContentHeight(); got != 17 {
		t.Fatalf("expanded-dock repo height = %d, want 17", got)
	}
	m.terminalDockVisible = false
	if got := m.repoContentHeight(); got != 20 {
		t.Fatalf("collapsed-dock repo height = %d, want 20", got)
	}
}

func TestEmbeddedTerminalSpansFullAppWidth(t *testing.T) {
	m := Model{width: 120, height: 30}
	if got := m.embeddedTerminalOuterWidth(); got != 120 {
		t.Fatalf("terminal outer width = %d, want full app width 120", got)
	}
}

func TestEmbeddedTerminalAllocationIsModeIndependent(t *testing.T) {
	base := Model{height: 24, topMode: ui.ModeWorktrees, bottomMode: ui.ModeSessions, contentPane: ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true, embeddedTerminals: []embeddedTerminalSlot{{Terminal: internalFakeEmbeddedTerminal{}}}}}
	activeFlows := Model{height: 24, topMode: ui.ModeWorktrees, bottomMode: ui.ModeFlows, contentPane: ui.PaneTop, activeFlowSurface: true, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true, embeddedTerminals: []embeddedTerminalSlot{{Terminal: internalFakeEmbeddedTerminal{}}}}}
	if base.embeddedTerminalDockAllocation() != activeFlows.embeddedTerminalDockAllocation() {
		t.Fatalf("terminal allocation differs by mode: %#v vs %#v", base.embeddedTerminalDockAllocation(), activeFlows.embeddedTerminalDockAllocation())
	}
}

func TestResponsiveTerminalAllocationControlsModelVisibility(t *testing.T) {
	m := Model{
		height:      23,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{},
			}}}}
	allocation := m.embeddedTerminalDockAllocation()
	if allocation.State != ui.EmbeddedTerminalDockCollapsed || allocation.SharedOuterRows != 21 {
		t.Fatalf("height 23 allocation = %#v", allocation)
	}
	next := m.cyclePaneFocusForward()
	if next.terminalFocus == terminalFocusTerminal {
		t.Fatal("auto-collapsed terminal became focus eligible")
	}

	m.height = 24
	if allocation := m.embeddedTerminalDockAllocation(); allocation.State != ui.EmbeddedTerminalDockExpanded || allocation.RenderPTYRows != 1 {
		t.Fatalf("height 24 allocation = %#v", allocation)
	}
}

func TestWindowResizeEvacuatesAutoCollapsedTerminalFocus(t *testing.T) {
	m := Model{
		width:       120,
		height:      24,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible:  true,
			terminalFocus:        terminalFocusTerminal,
			terminalPrefixActive: true,
			activeTerminalNum:    1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{},
			}}}}
	nextModel, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 23})
	next := nextModel.(Model)
	if !next.terminalDockVisible || next.activePane != ui.PaneBottom || next.contentPane != ui.PaneBottom || next.terminalFocus != terminalFocusList || next.terminalPrefixActive {
		t.Fatalf("collapsed resize state = requested=%v active=%d remembered=%d focus=%d prefix=%v", next.terminalDockVisible, next.activePane, next.contentPane, next.terminalFocus, next.terminalPrefixActive)
	}
}

func TestFirstTerminalStartupUsesProspectiveRequestedState(t *testing.T) {
	newModel := func(started *[2]int) Model {
		return Model{
			width:       180,
			height:      65,
			topMode:     ui.ModeWorktrees,
			bottomMode:  ui.ModeFlows,
			contentPane: ui.PaneBottom,
			activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
				terminalDockVisible: false,
				startEmbeddedTerminal: func(_ actions.AgentLaunchContext, width, height int) (EmbeddedTerminal, error) {
					*started = [2]int{width, height}
					return internalFakeEmbeddedTerminal{}, nil
				}}}
	}

	t.Run("interactive session requests expansion", func(t *testing.T) {
		var started [2]int
		next, opened, err := newModel(&started).openEmbeddedTerminal(actions.AgentLaunchContext{}, sessions.SessionRecord{Provider: sessions.ProviderCodex})
		if err != nil || !opened || started[1] != 19 || !next.terminalDockVisible {
			t.Fatalf("session startup: opened=%v err=%v size=%v requested=%v", opened, err, started, next.terminalDockVisible)
		}
	})

	t.Run("interactive Flow requests expansion", func(t *testing.T) {
		var started [2]int
		next, opened, err, _ := newModel(&started).openFlowEmbeddedTerminal(actions.AgentLaunchContext{Command: "codex"})
		if err != nil || !opened || started[1] != 19 || !next.terminalDockVisible {
			t.Fatalf("interactive Flow startup: opened=%v err=%v size=%v requested=%v", opened, err, started, next.terminalDockVisible)
		}
	})

	for _, tt := range []struct {
		name string
		ctx  actions.AgentLaunchContext
	}{
		{name: "headless Flow preserves hidden request", ctx: actions.AgentLaunchContext{Command: "codex", Headless: true}},
		{name: "auto Flow preserves hidden request", ctx: actions.AgentLaunchContext{Command: "codex", FlowAutoLaunch: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var started [2]int
			next, opened, err, _ := newModel(&started).openFlowEmbeddedTerminal(tt.ctx)
			if err != nil || !opened || started[1] != 1 || next.terminalDockVisible {
				t.Fatalf("background Flow startup: opened=%v err=%v size=%v requested=%v", opened, err, started, next.terminalDockVisible)
			}
		})
	}
}

func TestInteractiveFlowPrefillImmediatelyResizesExistingHiddenTerminals(t *testing.T) {
	newModel := func(existing *internalFakeDetachableEmbeddedTerminal, started EmbeddedTerminal) Model {
		return Model{
			width:       180,
			height:      65,
			topMode:     ui.ModeWorktrees,
			bottomMode:  ui.ModeFlows,
			contentPane: ui.PaneBottom,
			activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
				terminalDockVisible:    false,
				activeTerminalNum:      1,
				nextEmbeddedTerminalID: 1,
				embeddedTerminals: []embeddedTerminalSlot{{
					Number: 1, ID: 1, Terminal: existing,
				}},
				startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
					return started, nil
				}}}
	}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		Embedded:          true,
		InitialPrompt:     "Implement it",
	}
	wantResize := [2]int{ui.EmbeddedTerminalPTYWidth(180), ui.ResolveEmbeddedTerminalDock(65, true).BackendPTYRows}

	t.Run("while prefill is pending", func(t *testing.T) {
		existing := &internalFakeDetachableEmbeddedTerminal{}
		pending := newDelayedPrefillFakeEmbeddedTerminal()
		t.Cleanup(pending.release)
		next, opened, err, prefillCmd := newModel(existing, pending).openFlowEmbeddedTerminal(ctx)
		if err != nil || !opened || prefillCmd == nil {
			t.Fatalf("interactive launch: opened=%v err=%v prefill=%T", opened, err, prefillCmd)
		}
		if !next.terminalDockVisible || len(existing.resizes) != 1 || existing.resizes[0] != wantResize {
			t.Fatalf("pending prefill state: visible=%v existing resizes=%#v, want [%v]", next.terminalDockVisible, existing.resizes, wantResize)
		}
	})

	t.Run("after prefill failure", func(t *testing.T) {
		existing := &internalFakeDetachableEmbeddedTerminal{}
		failing := &repairPrefillFailureTerminal{}
		next, opened, err, prefillCmd := newModel(existing, failing).openFlowEmbeddedTerminal(ctx)
		if err != nil || !opened || prefillCmd == nil {
			t.Fatalf("interactive launch: opened=%v err=%v prefill=%T", opened, err, prefillCmd)
		}
		result := prefillCmd()
		afterModel, _ := next.Update(result)
		after := afterModel.(Model)
		if len(after.embeddedTerminals) != 1 || after.embeddedTerminals[0].Number != 1 {
			t.Fatalf("prefill failure terminals = %#v, want surviving original", after.embeddedTerminals)
		}
		if !after.terminalDockVisible || len(existing.resizes) != 1 || existing.resizes[0] != wantResize {
			t.Fatalf("failed prefill state: visible=%v existing resizes=%#v, want [%v]", after.terminalDockVisible, existing.resizes, wantResize)
		}
	})
}

func TestAutoCollapsedRequestedPreferenceRestoresOnlyWhileStillOpen(t *testing.T) {
	base := Model{
		width:       120,
		height:      23,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{},
			}}}}

	grownModel, _ := base.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	grown := grownModel.(Model)
	if !grown.terminalDockVisible || !grown.terminalEffectivelyExpanded() {
		t.Fatalf("requested-open dock did not restore after growth: requested=%v allocation=%#v", grown.terminalDockVisible, grown.embeddedTerminalDockAllocation())
	}

	hiddenModel, _ := base.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	hidden := hiddenModel.(Model)
	if hidden.terminalDockVisible {
		t.Fatal("ctrl+t while auto-collapsed did not store a manual hide")
	}
	hiddenModel, _ = hidden.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	hidden = hiddenModel.(Model)
	if hidden.terminalDockVisible || hidden.terminalEffectivelyExpanded() {
		t.Fatalf("manually hidden dock restored after growth: requested=%v allocation=%#v", hidden.terminalDockVisible, hidden.embeddedTerminalDockAllocation())
	}
}

func TestContentHeightForModeAccountsForTerminalDockState(t *testing.T) {
	tests := []struct {
		name          string
		mode          ui.Mode
		wantNoDock    int
		wantExpanded  int
		wantCollapsed int
	}{
		{name: "worktrees", mode: ui.ModeWorktrees, wantNoDock: 7, wantExpanded: 6, wantCollapsed: 7},
		{name: "branches", mode: ui.ModeBranches, wantNoDock: 7, wantExpanded: 6, wantCollapsed: 7},
		{name: "stashes", mode: ui.ModeStashes, wantNoDock: 7, wantExpanded: 6, wantCollapsed: 7},
		{name: "history", mode: ui.ModeHistory, wantNoDock: 7, wantExpanded: 6, wantCollapsed: 7},
		{name: "reflog", mode: ui.ModeReflog, wantNoDock: 7, wantExpanded: 6, wantCollapsed: 7},
		{name: "sessions", mode: ui.ModeSessions, wantNoDock: 7, wantExpanded: 5, wantCollapsed: 7},
		{name: "plans", mode: ui.ModePlans, wantNoDock: 7, wantExpanded: 5, wantCollapsed: 7},
		{name: "flows", mode: ui.ModeFlows, wantNoDock: 7, wantExpanded: 5, wantCollapsed: 7},
		{name: "active flows", mode: ui.ModeActiveFlows, wantNoDock: 18, wantExpanded: 15, wantCollapsed: 18},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := modelWithModeForTest(Model{height: 24, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true}}, tt.mode)
			if got := m.contentHeightForMode(); got != tt.wantNoDock {
				t.Fatalf("no-dock content height = %d, want %d", got, tt.wantNoDock)
			}
			m.embeddedTerminals = []embeddedTerminalSlot{{Number: 1, Scope: embeddedTerminalScopeSession, Terminal: internalFakeEmbeddedTerminal{}}}
			if got := m.contentHeightForMode(); got != tt.wantExpanded {
				t.Fatalf("expanded content height = %d, want %d", got, tt.wantExpanded)
			}
			m.terminalDockVisible = false
			if got := m.contentHeightForMode(); got != tt.wantCollapsed {
				t.Fatalf("collapsed content height = %d, want %d", got, tt.wantCollapsed)
			}
		})
	}
}

func TestCyclePaneFocusIncludesExpandedTerminalDockInEveryMode(t *testing.T) {
	for _, mode := range []ui.Mode{ui.ModeWorktrees, ui.ModeSessions, ui.ModePlans, ui.ModeFlows} {
		t.Run(fmt.Sprintf("mode_%d", mode), func(t *testing.T) {
			m := modelWithModeForTest(Model{
				height:     24,
				activePane: ui.PaneRepos, embeddedTerminalState: embeddedTerminalState{
					terminalFocus:       terminalFocusList,
					terminalDockVisible: true,
					activeTerminalNum:   1,
					embeddedTerminals: []embeddedTerminalSlot{{
						Number: 1, Scope: embeddedTerminalScopeSession, ID: 1, Terminal: internalFakeEmbeddedTerminal{},
					}}}}, mode)

			m = m.cyclePaneFocusForward()
			if m.activePane == ui.PaneRepos || m.terminalFocus != terminalFocusList {
				t.Fatalf("repo -> list focus = pane %d focus %d, want pane 1/list", m.activePane, m.terminalFocus)
			}
			m = m.cyclePaneFocusForward()
			if m.activePane != ui.PaneBottom || m.terminalFocus != terminalFocusList {
				t.Fatalf("top -> bottom focus = pane %d focus %d, want bottom/list", m.activePane, m.terminalFocus)
			}
			m = m.cyclePaneFocusForward()
			if m.activePane == ui.PaneRepos || m.terminalFocus != terminalFocusTerminal || !m.terminalPrefixActive {
				t.Fatalf("bottom -> terminal focus = pane %d focus %d prefix %v, want content/terminal/command", m.activePane, m.terminalFocus, m.terminalPrefixActive)
			}
			m = m.cyclePaneFocusForward()
			if m.activePane != ui.PaneRepos || m.terminalFocus != terminalFocusList || m.terminalPrefixActive {
				t.Fatalf("terminal -> repo focus = pane %d focus %d prefix %v, want pane 0/list/input", m.activePane, m.terminalFocus, m.terminalPrefixActive)
			}
		})
	}
}

func TestCyclePaneFocusSkipsCollapsedOrEmptyTerminalDock(t *testing.T) {
	for _, tt := range []struct {
		name      string
		terminals []embeddedTerminalSlot
		visible   bool
	}{
		{name: "collapsed", terminals: []embeddedTerminalSlot{{Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{}}}},
		{name: "empty", visible: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{topMode: ui.ModeWorktrees, bottomMode: ui.ModePlans, contentPane: ui.PaneBottom, activePane: ui.PaneRepos, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: tt.visible, embeddedTerminals: tt.terminals}}
			m = m.cyclePaneFocusForward()
			m = m.cyclePaneFocusForward()
			m = m.cyclePaneFocusForward()
			if m.activePane != ui.PaneRepos || m.terminalFocus != terminalFocusList {
				t.Fatalf("third tab = pane %d focus %d, want repo/list", m.activePane, m.terminalFocus)
			}
		})
	}
}

func TestCtrlTTogglesTerminalDockOutsideInputMode(t *testing.T) {
	for _, tt := range []struct {
		name         string
		activePane   ui.Pane
		focus        terminalFocus
		prefixActive bool
	}{
		{name: "repo pane", activePane: ui.PaneRepos, focus: terminalFocusList},
		{name: "middle list", activePane: ui.PaneTop, focus: terminalFocusList},
		{name: "terminal command mode", activePane: ui.PaneTop, focus: terminalFocusTerminal, prefixActive: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			term := &internalFakeDetachableEmbeddedTerminal{}
			m := Model{
				topMode:     ui.ModeWorktrees,
				bottomMode:  ui.ModePlans,
				contentPane: ui.PaneBottom,
				width:       160,
				height:      24,
				activePane:  tt.activePane, embeddedTerminalState: embeddedTerminalState{
					terminalFocus:        tt.focus,
					terminalPrefixActive: tt.prefixActive,
					terminalDockVisible:  true,
					activeTerminalNum:    1,
					embeddedTerminals: []embeddedTerminalSlot{{
						Number: 1, ID: 1, Scope: embeddedTerminalScopeSession, Terminal: term,
					}}}}

			nextModel, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
			next := nextModel.(Model)
			if cmd != nil {
				t.Fatalf("ctrl+t returned command %T, want nil", cmd)
			}
			if next.terminalDockVisible {
				t.Fatal("ctrl+t should collapse the expanded dock")
			}
			if tt.focus == terminalFocusTerminal && (next.activePane == ui.PaneRepos || next.terminalFocus != terminalFocusList || next.terminalPrefixActive) {
				t.Fatalf("collapse from terminal focus = pane %d focus %d prefix %v, want middle list/input", next.activePane, next.terminalFocus, next.terminalPrefixActive)
			}
			if len(term.resizes) != 1 || term.resizes[0][1] != 1 {
				t.Fatalf("collapse backend resize = %#v, want one-row PTY", term.resizes)
			}
		})
	}
}

func TestCtrlTInTerminalInputModePassesThroughToPTY(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		height:      24,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModePlans,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusTerminal,
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeSession, Terminal: term,
			}}}}

	nextModel, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	next := nextModel.(Model)
	if cmd != nil || !next.terminalDockVisible {
		t.Fatalf("ctrl+t input passthrough changed dock or returned command: visible=%v cmd=%T", next.terminalDockVisible, cmd)
	}
	if len(term.writes) != 1 || string(term.writes[0]) != "\x14" {
		t.Fatalf("ctrl+t writes = %#v, want PTY byte 0x14", term.writes)
	}
}

func TestTerminalCommandTogglesDockFromInputMode(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		height:      24,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusTerminal,
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeFlow, Terminal: term,
			}}}}

	nextModel, _ := m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	next := nextModel.(Model)
	if !next.terminalPrefixActive {
		t.Fatal("ctrl+] should enter terminal command mode")
	}
	nextModel, cmd := next.Update(tea.KeyPressMsg{Text: string([]rune{'t'})})
	next = nextModel.(Model)
	if cmd != nil || next.terminalDockVisible || next.terminalFocus != terminalFocusList || next.terminalPrefixActive {
		t.Fatalf("ctrl+] t result = visible %v focus %d prefix %v cmd %T, want collapsed list focus", next.terminalDockVisible, next.terminalFocus, next.terminalPrefixActive, cmd)
	}
	if len(term.writes) != 0 {
		t.Fatalf("ctrl+] t should not write to PTY: %#v", term.writes)
	}
}

func TestShowingTerminalDockResizesLiveTerminals(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneTop,
		width:       160,
		height:      24,
		activePane:  ui.PaneTop, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusList,
			terminalDockVisible: false,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeSession, Terminal: term,
			}}}}

	nextModel, _ := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	next := nextModel.(Model)
	if !next.terminalDockVisible {
		t.Fatal("ctrl+t should expand the collapsed dock")
	}
	want := [2]int{next.embeddedTerminalWidth(), next.embeddedTerminalContentHeight()}
	if len(term.resizes) != 1 || term.resizes[0] != want {
		t.Fatalf("show resize calls = %#v, want [%v]", term.resizes, want)
	}
}

func TestCtrlTWithNoTerminalsReportsStatusAndSearchDoesNotToggle(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true}}
	nextModel, _ := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	next := nextModel.(Model)
	if !next.terminalDockVisible || !strings.Contains(next.status.Text, "No embedded terminals") {
		t.Fatalf("no-terminal ctrl+t = visible %v status %q", next.terminalDockVisible, next.status.Text)
	}

	next = Model{
		topMode:      ui.ModeWorktrees,
		bottomMode:   ui.ModeSessions,
		contentPane:  ui.PaneBottom,
		activePane:   ui.PaneBottom,
		searchActive: true, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{},
			}}}}
	nextModel, _ = next.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	next = nextModel.(Model)
	if !next.terminalDockVisible {
		t.Fatal("ctrl+t should not toggle while list search is active")
	}
}

func terminalPickerTestModel(listSessions func(sessions.SessionFilter) ([]sessions.SessionRecord, error)) Model {
	return Model{
		height:       24,
		topMode:      ui.ModeWorktrees,
		bottomMode:   ui.ModeFlows,
		contentPane:  ui.PaneBottom,
		activePane:   ui.PaneBottom,
		repos:        newRepoPane().SetItems([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}),
		listSessions: listSessions, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:        terminalFocusTerminal,
			terminalPrefixActive: true,
			terminalDockVisible:  true,
			activeTerminalNum:    1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeFlow, Terminal: internalFakeEmbeddedTerminal{},
			}}}}
}

func TestTerminalCommandSessionPickerLoadsStoredSessionsOnDemand(t *testing.T) {
	stored := []sessions.SessionRecord{{Provider: sessions.ProviderCodex, SessionID: "session-1", RepoPath: "/dev/alpha", Branch: "feature/dock"}}
	base := terminalPickerTestModel(func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
		if filter.RepoPath != "/dev/alpha" {
			return nil, nil
		}
		return stored, nil
	})

	next, cmd, consumed := base.handleFocusedEmbeddedTerminalKey(tea.KeyPressMsg{Text: string([]rune{'l'})})
	if !consumed || cmd == nil || next.modal.IsOpen() {
		t.Fatalf("empty cache should defer to on-demand load: consumed %v cmd %v modal %v", consumed, cmd, next.modal.IsOpen())
	}
	if strings.Contains(next.status.Text, "No saved sessions available") {
		t.Fatalf("empty cache should not report missing sessions before loading, got %q", next.status.Text)
	}
	msg, ok := cmd().(embeddedSessionPickerLoadedMsg)
	if !ok || msg.RepoPath != "/dev/alpha" || len(msg.Records) != 1 || msg.Err != nil {
		t.Fatalf("picker load message = %#v", msg)
	}
	loaded, _ := next.handleEmbeddedSessionPickerLoaded(msg)
	if !loaded.modal.IsOpen() || loaded.modal.View().Kind != modal.Select {
		t.Fatalf("on-demand load should open the picker, modal %#v", loaded.modal.View())
	}

	base.sessions = newSessionPane().SetItems(stored)
	next, cmd, consumed = base.handleFocusedEmbeddedTerminalKey(tea.KeyPressMsg{Text: string([]rune{'l'})})
	if !consumed || cmd != nil || !next.modal.IsOpen() || next.modal.View().Kind != modal.Select {
		t.Fatalf("warm cache picker result = consumed %v cmd %v modal %#v", consumed, cmd, next.modal.View())
	}
}

func TestTerminalCommandSessionPickerLoadHandlesEmptyErrorAndStaleResults(t *testing.T) {
	base := terminalPickerTestModel(nil)

	next, _ := base.handleEmbeddedSessionPickerLoaded(embeddedSessionPickerLoadedMsg{RepoPath: "/dev/alpha"})
	if next.modal.IsOpen() || !strings.Contains(next.status.Text, "No saved sessions available") {
		t.Fatalf("empty store result = modal %v status %q", next.modal.IsOpen(), next.status.Text)
	}

	next, _ = base.handleEmbeddedSessionPickerLoaded(embeddedSessionPickerLoadedMsg{RepoPath: "/dev/alpha", Err: errors.New("store unreadable")})
	if next.modal.IsOpen() || !strings.Contains(next.status.Text, "failed to load sessions") || !strings.Contains(next.status.Text, "store unreadable") {
		t.Fatalf("load error result = modal %v status %q", next.modal.IsOpen(), next.status.Text)
	}

	stale := embeddedSessionPickerLoadedMsg{RepoPath: "/dev/bravo", Records: []sessions.SessionRecord{{SessionID: "session-1"}}}
	next, _ = base.handleEmbeddedSessionPickerLoaded(stale)
	if next.modal.IsOpen() || next.status.Text != "" {
		t.Fatalf("stale repo result should be ignored: modal %v status %q", next.modal.IsOpen(), next.status.Text)
	}
}

func TestSessionResumeExpandsCollapsedDockAndFocusesTerminalInInputMode(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		width:       160,
		height:      24,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusList,
			terminalDockVisible: false,
			startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
				return term, nil
			}}}
	next, _ := m.resumeSessionInEmbeddedTerminal(actions.AgentLaunchContext{}, sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1"}, nil)
	if !next.terminalDockVisible || next.activePane == ui.PaneRepos || next.terminalFocus != terminalFocusTerminal || next.terminalPrefixActive {
		t.Fatalf("session resume state = visible %v pane %d focus %d prefix %v, want expanded terminal input", next.terminalDockVisible, next.activePane, next.terminalFocus, next.terminalPrefixActive)
	}
	if len(term.resizes) != 1 {
		t.Fatalf("session resume resize calls = %#v, want one resize after expanding dock", term.resizes)
	}
}

func TestInteractiveFlowLaunchFocusExpandsCollapsedDock(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		width:       160,
		height:      24,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:       terminalFocusList,
			terminalDockVisible: false,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeFlow, Terminal: term,
			}}}}

	next := m.updateFlowTerminalFocusAfterLaunch(actions.AgentLaunchContext{})
	if !next.terminalDockVisible || next.activePane == ui.PaneRepos || next.terminalFocus != terminalFocusTerminal || next.terminalPrefixActive {
		t.Fatalf("interactive Flow focus = visible %v pane %d focus %d prefix %v, want expanded terminal input", next.terminalDockVisible, next.activePane, next.terminalFocus, next.terminalPrefixActive)
	}
	if len(term.resizes) != 1 {
		t.Fatalf("interactive Flow resize calls = %#v, want one resize after expanding dock", term.resizes)
	}
}

func TestGitAndNonGitModeSwitchesKeepTerminalDockSize(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		lastGitMode: ui.ModeWorktrees,
		width:       160,
		height:      24,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalDockVisible: true,
			activeTerminalNum:   1,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number: 1, ID: 1, Scope: embeddedTerminalScopeSession, Terminal: term,
			}}}}

	m.activePane = ui.PaneTop
	m.contentPane = ui.PaneTop
	next, _, handled := m.switchModeFromKey("1")
	if !handled || next.focusedMode() != ui.ModeWorktrees {
		t.Fatalf("switch to Git = handled %v mode %d, want worktrees", handled, next.focusedMode())
	}
	if next.embeddedTerminalContentHeight() != m.embeddedTerminalContentHeight() || next.embeddedTerminalWidth() != m.embeddedTerminalWidth() {
		t.Fatal("terminal dimensions should be mode-independent")
	}
	if len(term.resizes) != 0 {
		t.Fatalf("Git switch should not resize the mode-independent terminal, got %#v", term.resizes)
	}

	next.activePane = ui.PaneBottom
	next.contentPane = ui.PaneBottom
	next, _, handled = next.switchModeFromKey("1")
	if !handled || next.focusedMode() != ui.ModeSessions {
		t.Fatalf("switch from Git = handled %v mode %d, want sessions", handled, next.focusedMode())
	}
	if len(term.resizes) != 0 {
		t.Fatalf("non-Git switch should not resize the mode-independent terminal, got %#v", term.resizes)
	}
}

func TestDismissActiveTerminalWithOnlyPendingSurvivorsReturnsToListFocus(t *testing.T) {
	m := Model{
		activePane: ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:        terminalFocusTerminal,
			terminalPrefixActive: true,
			activeTerminalNum:    1,
			embeddedTerminals: []embeddedTerminalSlot{
				{Number: 1, ID: 1, Terminal: internalFakeEmbeddedTerminal{}},
				{Number: 2, ID: 2, PrefillPending: true, Terminal: internalFakeEmbeddedTerminal{}},
			}}}

	next := m.dismissEmbeddedTerminal(1)
	if next.activeTerminalNum != 0 || next.terminalFocus != terminalFocusList || next.terminalPrefixActive {
		t.Fatalf("pending survivor state = active %d focus %d prefix %v, want list focus with no active terminal", next.activeTerminalNum, next.terminalFocus, next.terminalPrefixActive)
	}
}

func TestNumberedModeKeysRemainAvailableWithSessionTerminalListFocused(t *testing.T) {
	for _, tt := range []struct {
		key      rune
		wantMode ui.Mode
	}{
		{key: '1', wantMode: ui.ModeSessions},
		{key: '2', wantMode: ui.ModePlans},
		{key: '3', wantMode: ui.ModeFlows},
		{key: '4', wantMode: ui.ModeSessions},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := Model{
				topMode:     ui.ModeWorktrees,
				bottomMode:  ui.ModeSessions,
				contentPane: ui.PaneBottom,
				lastGitMode: ui.ModeWorktrees,
				activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
					terminalFocus:       terminalFocusList,
					terminalDockVisible: true,
					activeTerminalNum:   1,
					embeddedTerminals: []embeddedTerminalSlot{{
						Number: 1, ID: 1, Scope: embeddedTerminalScopeSession, Terminal: internalFakeEmbeddedTerminal{},
					}}}}
			nextModel, _ := m.Update(tea.KeyPressMsg{Text: string([]rune{tt.key})})
			next := nextModel.(Model)
			if next.focusedMode() != tt.wantMode {
				t.Fatalf("key %q mode = %d, want %d", tt.key, next.focusedMode(), tt.wantMode)
			}
		})
	}

	m := Model{topMode: ui.ModeWorktrees, bottomMode: ui.ModeSessions, contentPane: ui.PaneBottom, activePane: ui.PaneRepos, embeddedTerminalState: embeddedTerminalState{terminalDockVisible: true, embeddedTerminals: []embeddedTerminalSlot{{Number: 1, Terminal: internalFakeEmbeddedTerminal{}}}}}
	nextModel, _ := m.Update(tea.KeyPressMsg{Text: string([]rune{'3'})})
	if next := nextModel.(Model); next.focusedMode() != ui.ModeSessions {
		t.Fatalf("repo-pane numbered key changed mode to %d, want sessions", next.focusedMode())
	}
}

type internalFakeDetachableEmbeddedTerminal struct {
	internalFakeEmbeddedTerminal
	target   string
	detached bool
	writes   [][]byte
	resizes  [][2]int
}

func (t *internalFakeDetachableEmbeddedTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (t *internalFakeDetachableEmbeddedTerminal) Detach() error {
	t.detached = true
	t.state = "exited"
	return nil
}

func (t *internalFakeDetachableEmbeddedTerminal) DetachTarget() string { return t.target }

func (t *internalFakeDetachableEmbeddedTerminal) Resize(width, height int) error {
	t.resizes = append(t.resizes, [2]int{width, height})
	return nil
}

type prefillReadyFakeEmbeddedTerminal struct {
	lines       [][]string
	visibleHits int
	writes      []string
}

func (t *prefillReadyFakeEmbeddedTerminal) VisibleLines(int, int) []string {
	t.visibleHits++
	if len(t.lines) == 0 {
		return nil
	}
	index := t.visibleHits - 1
	if index >= len(t.lines) {
		index = len(t.lines) - 1
	}
	return t.lines[index]
}

func (t *prefillReadyFakeEmbeddedTerminal) Write(p []byte) (int, error) {
	t.writes = append(t.writes, string(p))
	return len(p), nil
}

func (t *prefillReadyFakeEmbeddedTerminal) Resize(int, int) error      { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) Terminate() error           { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) Wait(context.Context) error { return nil }
func (t *prefillReadyFakeEmbeddedTerminal) State() string              { return "running" }

type delayedPrefillFakeEmbeddedTerminal struct {
	mu               sync.Mutex
	probeStarted     chan struct{}
	releaseReady     chan struct{}
	commandCompleted chan struct{}
	probeOnce        sync.Once
	releaseOnce      sync.Once
	completeOnce     sync.Once
	visibleHits      int
	writes           []string
}

func newDelayedPrefillFakeEmbeddedTerminal() *delayedPrefillFakeEmbeddedTerminal {
	return &delayedPrefillFakeEmbeddedTerminal{
		probeStarted:     make(chan struct{}),
		releaseReady:     make(chan struct{}),
		commandCompleted: make(chan struct{}),
	}
}

func (t *delayedPrefillFakeEmbeddedTerminal) VisibleLines(int, int) []string {
	t.mu.Lock()
	t.visibleHits++
	t.mu.Unlock()
	t.probeOnce.Do(func() { close(t.probeStarted) })
	<-t.releaseReady
	return []string{"Codex ready"}
}

func (t *delayedPrefillFakeEmbeddedTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.writes = append(t.writes, string(p))
	t.mu.Unlock()
	t.completeOnce.Do(func() { close(t.commandCompleted) })
	return len(p), nil
}

func (t *delayedPrefillFakeEmbeddedTerminal) Resize(int, int) error      { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) Terminate() error           { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) Wait(context.Context) error { return nil }
func (t *delayedPrefillFakeEmbeddedTerminal) State() string              { return "running" }

func (t *delayedPrefillFakeEmbeddedTerminal) release() {
	t.releaseOnce.Do(func() { close(t.releaseReady) })
}

func (t *delayedPrefillFakeEmbeddedTerminal) snapshot() (int, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visibleHits, append([]string(nil), t.writes...)
}

func TestPrefillEmbeddedPromptWaitsForVisibleTerminalOutput(t *testing.T) {
	originalInterval := embeddedPromptPrefillPollInterval
	embeddedPromptPrefillPollInterval = 0
	t.Cleanup(func() {
		embeddedPromptPrefillPollInterval = originalInterval
	})

	term := &prefillReadyFakeEmbeddedTerminal{
		lines: [][]string{
			{"", "   "},
			{"Codex ready", ""},
		},
	}

	err := prefillEmbeddedPromptIfNeeded(term, actions.AgentLaunchContext{
		Command:           "codex",
		Embedded:          true,
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build it",
	})
	if err != nil {
		t.Fatalf("prefill returned error: %v", err)
	}

	if term.visibleHits != 2 {
		t.Fatalf("visible calls = %d, want wait until second ready frame", term.visibleHits)
	}
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	if len(term.writes) != 1 || term.writes[0] != wantWrite {
		t.Fatalf("writes = %#v, want %q", term.writes, wantWrite)
	}
}

func TestFlowEmbeddedInteractivePrefillRunsAfterUpdateAndActivatesByStableID(t *testing.T) {
	term := newDelayedPrefillFakeEmbeddedTerminal()
	t.Cleanup(term.release)
	startCalls := 0
	m := Model{
		height:      24,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom,
		// Seeded so the stable IDs handed out below (41, 42) cannot coincide
		// with the Flow terminal numbers the same slots receive (1, 2). In a
		// zero-valued model both counters start at 1, and activation keyed on
		// slot.Number would satisfy every assertion in this test.
		embeddedTerminalState: embeddedTerminalState{
			nextEmbeddedTerminalID: 40,
			terminalFocus:          terminalFocusList,
			startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
				startCalls++
				return term, nil
			}}}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build it",
	}

	type updateResult struct {
		model tea.Model
		cmd   tea.Cmd
	}
	updateDone := make(chan updateResult, 1)
	go func() {
		next, cmd := openFlowEmbeddedTerminalForTest(m, ctx)
		updateDone <- updateResult{model: next, cmd: cmd}
	}()

	deadlockGuard := time.After(5 * time.Second)
	var result updateResult
	select {
	case result = <-updateDone:
	case <-term.probeStarted:
		term.release()
		select {
		case <-updateDone:
		case <-deadlockGuard:
			t.Fatal("Update did not return after releasing unexpected synchronous readiness probe")
		}
		t.Fatal("Update started terminal readiness work before returning its command")
	case <-deadlockGuard:
		term.release()
		t.Fatal("Update did not return before the deadlock guard expired")
	}

	next := result.model.(Model)
	cmd := result.cmd
	if cmd == nil {
		t.Fatal("interactive Flow launch returned nil command, want asynchronous prefill command")
	}
	select {
	case <-term.probeStarted:
		t.Fatal("Update started terminal readiness work before its command was invoked")
	default:
	}
	visibleHits, writes := term.snapshot()
	if visibleHits != 0 || len(writes) != 0 {
		t.Fatalf("Update performed prefill work: visible calls=%d writes=%#v", visibleHits, writes)
	}
	if startCalls != 1 {
		t.Fatalf("terminal starts = %d, want exactly 1", startCalls)
	}
	if len(next.embeddedTerminals) != 1 || next.embeddedTerminals[0].ID == 0 || !next.embeddedTerminals[0].PrefillPending {
		t.Fatalf("pending terminal slots = %#v, want one PrefillPending slot with stable ID", next.embeddedTerminals)
	}
	stableID := next.embeddedTerminals[0].ID
	if int(stableID) == next.embeddedTerminals[0].Number {
		t.Fatalf("stable ID %d collides with Flow terminal number %d; seed nextEmbeddedTerminalID so activation cannot key on the number", stableID, next.embeddedTerminals[0].Number)
	}
	if next.activeTerminalNum != 0 || next.terminalFocus != terminalFocusList {
		t.Fatalf("pending terminal stole focus: active=%d focus=%v", next.activeTerminalNum, next.terminalFocus)
	}
	if number, ok := next.nextEmbeddedTerminalNumber(); !ok || number != 2 {
		t.Fatalf("next Flow terminal number = %d, %v; want reserved slot 1 and next number 2", number, ok)
	}

	prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, cmd)
	prefillResult := make(chan tea.Msg, 1)
	go func() {
		prefillResult <- prefillCmd()
	}()

	select {
	case <-term.probeStarted:
	case <-deadlockGuard:
		t.Fatal("prefill command did not begin its readiness probe")
	}
	select {
	case msg := <-prefillResult:
		t.Fatalf("prefill command returned while readiness was withheld: %T", msg)
	default:
	}

	beforeReadyModel, tabCmd := next.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	beforeReady := beforeReadyModel.(Model)
	beforeReadyModel, keyCmd := beforeReady.Update(tea.KeyPressMsg{Text: string([]rune{'z'})})
	beforeReady = beforeReadyModel.(Model)
	_, writes = term.snapshot()
	if tabCmd != nil || keyCmd != nil || len(writes) != 0 || beforeReady.activePane != ui.PaneRepos || beforeReady.terminalFocus != terminalFocusList {
		t.Fatalf("pending terminal accepted focus/input before prefill: tab cmd=%T key cmd=%T writes=%#v pane=%d focus=%v", tabCmd, keyCmd, writes, beforeReady.activePane, beforeReady.terminalFocus)
	}

	// A lone pending slot cannot separate activation by stable ID from
	// activation of slot 0 or Flow terminal number 1. Open a second pending
	// slot and let its prefill finish first, so only ID-keyed activation can
	// satisfy the assertions below.
	secondTerm := newDelayedPrefillFakeEmbeddedTerminal()
	t.Cleanup(secondTerm.release)
	next.startEmbeddedTerminal = func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
		startCalls++
		return secondTerm, nil
	}
	var secondCmd tea.Cmd
	next, secondCmd = openFlowEmbeddedTerminalForTest(next, ctx)
	if startCalls != 2 || len(next.embeddedTerminals) != 2 {
		t.Fatalf("second interactive launch starts=%d slots=%#v, want a second pending slot", startCalls, next.embeddedTerminals)
	}
	secondID := next.embeddedTerminals[1].ID
	if secondID == 0 || secondID == stableID || next.embeddedTerminals[1].Number != 2 || !next.embeddedTerminals[1].PrefillPending {
		t.Fatalf("second pending slot = %#v, want distinct stable ID on Flow terminal number 2", next.embeddedTerminals[1])
	}
	if int(secondID) == next.embeddedTerminals[1].Number || int(stableID) == next.embeddedTerminals[1].Number || int(secondID) == next.embeddedTerminals[0].Number {
		t.Fatalf("stable IDs (%d, %d) overlap Flow terminal numbers (%d, %d); activation keyed on the number would pass", stableID, secondID, next.embeddedTerminals[0].Number, next.embeddedTerminals[1].Number)
	}
	if next.embeddedTerminals[0].ID != stableID || !next.embeddedTerminals[0].PrefillPending || next.activeTerminalNum != 0 || next.terminalFocus != terminalFocusList {
		t.Fatalf("second launch disturbed the first pending slot: slots=%#v active=%d focus=%v", next.embeddedTerminals, next.activeTerminalNum, next.terminalFocus)
	}

	// The second prefill completes while the first is still withheld, so its
	// result arrives out of order relative to slot creation.
	secondTerm.release()
	secondMsg := commandMessageOfType[embeddedPromptPrefillResultMsg](t, secondCmd)
	if secondMsg.ID != secondID || secondMsg.Err != nil {
		t.Fatalf("second prefill result = %#v, want successful result for stable terminal ID %d", secondMsg, secondID)
	}
	outOfOrderModel, _ := next.Update(secondMsg)
	outOfOrder := outOfOrderModel.(Model)
	if outOfOrder.activeTerminalNum != 2 || outOfOrder.terminalFocus != terminalFocusTerminal {
		t.Fatalf("out-of-order prefill activated the wrong terminal: active=%d focus=%v", outOfOrder.activeTerminalNum, outOfOrder.terminalFocus)
	}
	if outOfOrder.embeddedTerminals[1].ID != secondID || outOfOrder.embeddedTerminals[1].PrefillPending || !outOfOrder.embeddedTerminals[0].PrefillPending {
		t.Fatalf("out-of-order prefill cleared the wrong pending slot: slots=%#v", outOfOrder.embeddedTerminals)
	}
	if _, secondWrites := secondTerm.snapshot(); len(secondWrites) != 1 || secondWrites[0] != embeddedPromptPasteStart+"Build it"+embeddedPromptPasteEnd {
		t.Fatalf("second prefill writes = %#v, want exactly one prompt paste", secondWrites)
	}

	term.release()
	var rawMsg tea.Msg
	select {
	case rawMsg = <-prefillResult:
	case <-deadlockGuard:
		t.Fatal("prefill command did not return after readiness was released")
	}
	select {
	case <-term.commandCompleted:
	default:
		t.Fatal("prefill command returned before completing its terminal write")
	}
	msg, ok := rawMsg.(embeddedPromptPrefillResultMsg)
	if !ok {
		t.Fatalf("prefill command returned %T, want embeddedPromptPrefillResultMsg", rawMsg)
	}
	if msg.ID != stableID || msg.Err != nil {
		t.Fatalf("prefill result = %#v, want successful result for stable terminal ID %d", msg, stableID)
	}
	if outOfOrder.activeTerminalNum != 2 || !outOfOrder.embeddedTerminals[0].PrefillPending {
		t.Fatalf("prefill command activated terminal before result Update: active=%d pending=%v", outOfOrder.activeTerminalNum, outOfOrder.embeddedTerminals[0].PrefillPending)
	}

	activatedModel, _ := outOfOrder.Update(msg)
	activated := activatedModel.(Model)
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	visibleHits, writes = term.snapshot()
	if startCalls != 2 || visibleHits != 1 || len(writes) != 1 || writes[0] != wantWrite {
		t.Fatalf("async prefill starts=%d calls=%d writes=%#v, want two starts, one probe, and %q", startCalls, visibleHits, writes, wantWrite)
	}
	if len(activated.embeddedTerminals) != 2 || activated.embeddedTerminals[0].ID != stableID || activated.embeddedTerminals[0].PrefillPending || activated.activeTerminalNum != 1 || activated.terminalFocus != terminalFocusTerminal {
		t.Fatalf("late prefill did not activate its own stable terminal: slots=%#v active=%d focus=%v", activated.embeddedTerminals, activated.activeTerminalNum, activated.terminalFocus)
	}
	if activated.embeddedTerminals[1].ID != secondID || activated.embeddedTerminals[1].PrefillPending {
		t.Fatalf("late prefill disturbed the already-activated slot: slots=%#v", activated.embeddedTerminals)
	}
}

func TestFlowEmbeddedInteractivePrefillWritesAfterReadinessTimeout(t *testing.T) {
	originalTimeout := embeddedPromptPrefillTimeout
	originalInterval := embeddedPromptPrefillPollInterval
	embeddedPromptPrefillTimeout = time.Nanosecond
	embeddedPromptPrefillPollInterval = 0
	t.Cleanup(func() {
		embeddedPromptPrefillTimeout = originalTimeout
		embeddedPromptPrefillPollInterval = originalInterval
	})

	term := &prefillReadyFakeEmbeddedTerminal{lines: [][]string{{"", "   "}}}
	startCalls := 0
	m := Model{
		height:      24,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus: terminalFocusList,
			startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
				startCalls++
				return term, nil
			}}}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		FlowID:            "flow-1",
		FlowPhaseID:       "implementation",
		FlowLaunchTracked: true,
		InitialPrompt:     "Build \x1b[31mit\x1b[0m",
	}

	next, parentCmd := openFlowEmbeddedTerminalForTest(m, ctx)
	prefillCmd := isolatedFlowPrefillCommandFromLaunchBatch(t, parentCmd)
	rawMsg := prefillCmd()
	msg, ok := rawMsg.(embeddedPromptPrefillResultMsg)
	if !ok {
		t.Fatalf("prefill command returned %T, want embeddedPromptPrefillResultMsg", rawMsg)
	}
	if msg.Err != nil {
		t.Fatalf("readiness timeout returned launch error: %v", msg.Err)
	}
	if startCalls != 1 || len(next.embeddedTerminals) != 1 || !next.embeddedTerminals[0].PrefillPending || next.activeTerminalNum != 0 || next.terminalFocus != terminalFocusList {
		t.Fatalf("timeout prefill activated before result Update: starts=%d slots=%#v active=%d focus=%v", startCalls, next.embeddedTerminals, next.activeTerminalNum, next.terminalFocus)
	}
	wantWrite := embeddedPromptPasteStart + "Build it" + embeddedPromptPasteEnd
	if term.visibleHits == 0 || len(term.writes) != 1 || term.writes[0] != wantWrite {
		t.Fatalf("timeout prefill calls=%d writes=%#v, want readiness probes and %q", term.visibleHits, term.writes, wantWrite)
	}

	activatedModel, _ := next.Update(msg)
	activated := activatedModel.(Model)
	if len(activated.embeddedTerminals) != 1 || activated.embeddedTerminals[0].PrefillPending || activated.activeTerminalNum != 1 || activated.terminalFocus != terminalFocusTerminal {
		t.Fatalf("successful timeout prefill did not activate terminal on result Update: slots=%#v active=%d focus=%v", activated.embeddedTerminals, activated.activeTerminalNum, activated.terminalFocus)
	}
}

func TestStaleEmbeddedPromptPrefillResultIsIgnoredAfterSlotRemoval(t *testing.T) {
	phaseMutationRan := false
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom,
		setFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			phaseMutationRan = true
			return flowstore.FlowRecord{}, nil
		}, embeddedTerminalState: embeddedTerminalState{
			terminalFocus: terminalFocusList}}

	nextModel, cmd := m.Update(embeddedPromptPrefillResultMsg{
		ID:            42,
		LaunchContext: actions.AgentLaunchContext{FlowID: "flow-1", FlowPhaseID: "implementation"},
		Err:           errors.New("late failure"),
	})
	next := nextModel.(Model)
	if cmd != nil || phaseMutationRan || next.activeTerminalNum != 0 || next.terminalFocus != terminalFocusList {
		t.Fatalf("stale prefill result changed model: cmd=%T mutation=%v active=%d focus=%v", cmd, phaseMutationRan, next.activeTerminalNum, next.terminalFocus)
	}
}

func commandMessageOfType[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	if typed, ok := msg.(T); ok {
		return typed
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if typed, ok := child().(T); ok {
				return typed
			}
		}
	}
	t.Fatalf("command returned %T, want message %T", msg, zero)
	return zero
}

// isolatedFlowPrefillCommandFromLaunchBatch extracts the prefill command from
// the single-level batch returned by a minimal Flow model with no refresh cmd.
func isolatedFlowPrefillCommandFromLaunchBatch(t *testing.T, parentCmd tea.Cmd) tea.Cmd {
	t.Helper()
	if parentCmd == nil {
		t.Fatal("interactive Flow launch returned nil parent command")
	}
	raw := parentCmd()
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		t.Fatalf("interactive Flow launch parent command returned %T, want tea.BatchMsg", raw)
	}
	if len(batch) != 2 || batch[0] == nil || batch[1] == nil {
		t.Fatalf("interactive Flow launch batch = %#v, want prefill and repaint commands", batch)
	}
	return batch[0]
}

func internalFlowsModel(records ...flowstore.FlowRecord) Model {
	return Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom,
		repos: newRepoPane().SetItems([]scanner.Repo{
			{Path: "/dev/alpha", DisplayName: "alpha"},
		}),
		flows: newFlowPane().SetItems(records),
	}
}

func TestDefaultEmbeddedTerminalStarterUsesTmuxWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	writeInternalFakeExecutable(t, dir, "codex", "#!/bin/sh\n/bin/sleep 30\n")
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("APPROACH_TMUX_LOG", logPath)
	writeInternalFakeExecutable(t, dir, "tmux", `#!/bin/sh
echo "$@" >> "$APPROACH_TMUX_LOG"
for arg in "$@"; do
  case "$arg" in
    has-session) exit 1 ;;
    new-session)
      rm -f "$TMPDIR"/approach-agent-*.sh
      exit 0
      ;;
    attach-session) /bin/sleep 30 ;;
    kill-session) exit 0 ;;
  esac
done
exit 0
	`)
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		Embedded:      true,
		InitialPrompt: "run",
	}, 20, 3)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("default starter returned %T, want detachable tmux-backed terminal", term)
	}
	if target := detachable.DetachTarget(); !strings.Contains(target, "env -u TMUX tmux -f /dev/null -L 'approach-agent-") || !strings.Contains(target, "attach-session -t") || !strings.Contains(target, "agent-launch-1") {
		t.Fatalf("detach target = %q, want reattach command for per-launch agent tmux session", target)
	}
	waitInternalFileContains(t, logPath, "attach-session")
	if err := detachable.Detach(); err != nil {
		t.Fatalf("Detach returned error: %v", err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read tmux log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{"has-session", "new-session", "attach-session"} {
		if !strings.Contains(log, want) {
			t.Fatalf("tmux log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "kill-session") {
		t.Fatalf("detach should not kill the tmux session:\n%s", log)
	}
}

func TestDefaultEmbeddedTerminalStarterFallsBackWhenTmuxUnavailable(t *testing.T) {
	dir := t.TempDir()
	writeInternalFakeExecutable(t, dir, "codex", "#!/bin/sh\n/bin/sleep 30\n")
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "codex",
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		Embedded:      true,
		InitialPrompt: "run",
	}, 20, 3)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("default starter returned %T, want model-facing detach method", term)
	}
	if err := detachable.Detach(); !errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
		t.Fatalf("Detach error = %v, want unavailable sentinel", err)
	}
}

func TestDefaultEmbeddedTerminalStarterRendersHeadlessClaudeStreamJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	// A stream-json assistant text event; the renderer must turn it into a
	// readable line, never raw JSON.
	writeInternalFakeExecutable(t, dir, "claude", "#!/bin/sh\n"+
		`printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"hello from claude"}]}}'`+"\n")
	// tmux is available but must NOT be used for headless claude.
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("APPROACH_TMUX_LOG", logPath)
	writeInternalFakeExecutable(t, dir, "tmux", "#!/bin/sh\necho \"$@\" >> \"$APPROACH_TMUX_LOG\"\nexit 0\n")
	t.Setenv("PATH", dir)

	term, err := defaultEmbeddedTerminalStarter(actions.AgentLaunchContext{
		Command:       "claude",
		Headless:      true,
		LaunchID:      "launch-1",
		WorktreePath:  dir,
		Embedded:      true,
		InitialPrompt: "run",
	}, 40, 6)
	if err != nil {
		t.Fatalf("defaultEmbeddedTerminalStarter returned error: %v", err)
	}
	defer term.Terminate()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := term.Wait(ctx); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	got := strings.Join(term.VisibleLines(40, 6), "\n")
	if !strings.Contains(got, "hello from claude") {
		t.Fatalf("headless claude stream-json not rendered, got:\n%s", got)
	}
	if strings.Contains(got, `"type":`) {
		t.Fatalf("raw stream-json leaked into the panel, got:\n%s", got)
	}
	// Headless claude bypasses tmux: it runs inline and is not detachable.
	detachable, ok := term.(detachableEmbeddedTerminal)
	if !ok {
		t.Fatalf("starter returned %T, want model-facing detach method", term)
	}
	if err := detachable.Detach(); !errors.Is(err, errEmbeddedTerminalDetachUnavailable) {
		t.Fatalf("headless claude Detach error = %v, want unavailable sentinel", err)
	}
	if body, _ := os.ReadFile(logPath); strings.TrimSpace(string(body)) != "" {
		t.Fatalf("tmux must not be invoked for headless claude, log:\n%s", body)
	}
}

func writeInternalFakeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake %s executable: %v", name, err)
	}
}

func waitInternalFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		body, _ := os.ReadFile(path)
		if strings.Contains(string(body), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	body, _ := os.ReadFile(path)
	t.Fatalf("%s did not contain %q:\n%s", path, want, body)
}

func TestActiveTerminalRepoPathsIncludesOnlyRunningOwnedTerminals(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		embeddedTerminals: []embeddedTerminalSlot{
			{RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{}},
			{RepoPath: "/dev/beta", Terminal: internalFakeEmbeddedTerminal{state: "exited"}},
			{RepoPath: "/dev/gamma", Terminal: internalFakeEmbeddedTerminal{state: "starting"}},
			{RepoPath: "", Terminal: internalFakeEmbeddedTerminal{}},
			{RepoPath: "/dev/delta", Terminal: nil},
		}}}

	active := m.activeTerminalRepoPaths()

	for _, repoPath := range []string{"/dev/alpha", "/dev/gamma"} {
		if !active[repoPath] {
			t.Fatalf("active terminal repo paths missing %s: %#v", repoPath, active)
		}
	}
	for _, repoPath := range []string{"/dev/beta", "", "/dev/delta"} {
		if active[repoPath] {
			t.Fatalf("active terminal repo paths unexpectedly includes %q: %#v", repoPath, active)
		}
	}
}

func TestActiveTerminalRepoPathsClearsWhenLastRunningSlotIsDismissed(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		embeddedTerminals: []embeddedTerminalSlot{
			{ID: 1, Number: 1, Scope: embeddedTerminalScopeSession, RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{state: "exited"}},
			{ID: 2, Number: 2, Scope: embeddedTerminalScopeSession, RepoPath: "/dev/alpha", Terminal: internalFakeEmbeddedTerminal{}},
		}}}

	if !m.activeTerminalRepoPaths()["/dev/alpha"] {
		t.Fatalf("repo should remain active while one matching slot is running")
	}

	m = m.dismissEmbeddedTerminal(2)

	if m.activeTerminalRepoPaths()["/dev/alpha"] {
		t.Fatalf("repo should stop being active after last running slot is dismissed")
	}
}

func TestOpenEmbeddedTerminalStoresSessionRepoPath(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		}}}

	next, opened, err := m.openEmbeddedTerminal(actions.AgentLaunchContext{
		RepoPath:     "/dev/alpha/",
		WorktreePath: "/dev/alpha-worktrees/feature",
		WorkingDir:   "/dev/alpha-worktrees/feature/subdir",
	}, sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1"})
	if err != nil {
		t.Fatalf("open embedded terminal returned error: %v", err)
	}
	if !opened {
		t.Fatal("open embedded terminal should open a slot")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminal count = %d, want 1", len(next.embeddedTerminals))
	}
	if got := next.embeddedTerminals[0].RepoPath; got != "/dev/alpha" {
		t.Fatalf("slot repo path = %q, want cleaned repo path %q", got, "/dev/alpha")
	}
	if got := next.embeddedTerminals[0].WorktreePath; got != "/dev/alpha-worktrees/feature" {
		t.Fatalf("slot worktree path = %q, want cleaned worktree path", got)
	}
	if got := next.embeddedTerminals[0].WorkingDir; got != "/dev/alpha-worktrees/feature/subdir" {
		t.Fatalf("slot working dir = %q, want cleaned working dir", got)
	}
}

func TestOpenFlowEmbeddedTerminalStoresFlowRepoPath(t *testing.T) {
	m := Model{embeddedTerminalState: embeddedTerminalState{
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		}}}

	next, opened, err, _ := m.openFlowEmbeddedTerminal(actions.AgentLaunchContext{
		Command:       "codex",
		RepoPath:      "/dev/alpha",
		WorktreePath:  "/dev/alpha-worktrees/feature",
		FlowID:        "flow-1",
		FlowPhaseID:   "implementation",
		InitialPrompt: "Build it",
	})
	if err != nil {
		t.Fatalf("open Flow embedded terminal returned error: %v", err)
	}
	if !opened {
		t.Fatal("open Flow embedded terminal should open a slot")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminal count = %d, want 1", len(next.embeddedTerminals))
	}
	if got := next.embeddedTerminals[0].RepoPath; got != "/dev/alpha" {
		t.Fatalf("slot repo path = %q, want repo path %q", got, "/dev/alpha")
	}
}

func TestViewMarksRepoWithRunningTerminalByCleanRepoPath(t *testing.T) {
	m := Model{
		width:       80,
		height:      12,
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneTop,
		activePane:  ui.PaneRepos,
		repos: newRepoPane().SetItems([]scanner.Repo{
			{Path: "/dev/alpha", DisplayName: "alpha"},
			{Path: "/dev/alpha-worktrees/feature", DisplayName: "feature"},
		}), embeddedTerminalState: embeddedTerminalState{
			embeddedTerminals: []embeddedTerminalSlot{
				{
					RepoPath: "/dev/alpha/",
					Terminal: internalFakeEmbeddedTerminal{},
				},
			}}}

	view := ansi.Strip(viewContent(m))

	if !strings.Contains(view, " > ● alpha") {
		t.Fatalf("view should mark repo with running terminal:\n%s", view)
	}
	if !strings.Contains(view, "     feature") {
		t.Fatalf("view should reserve inactive marker spacing for worktree row without marking it:\n%s", view)
	}
	if strings.Contains(view, "● feature") {
		t.Fatalf("view should not mark worktree path as active repo:\n%s", view)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowSelectsNewestMatchingTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   3,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{state: "starting"},
		},
	}
	m.flows = m.flows.Move(1, 20, 80)

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeTerminalNum != 3 {
		t.Fatalf("active Flow terminal = %d, want newest matching terminal 3", m.activeTerminalNum)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowPreservesActiveTerminalWhenSelectedFlowHasNoMatch(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{state: "exited"},
		},
	}
	m.flows = m.flows.Move(1, 20, 80)

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1", m.activeTerminalNum)
	}
}

func TestSyncActiveFlowTerminalToSelectedFlowPreservesCurrentMatchingTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
	)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.syncActiveFlowTerminalToSelectedFlow()

	if m.activeTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want current matching terminal 1", m.activeTerminalNum)
	}
}

func TestMoveCursorSyncsActiveFlowTerminalToSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.moveCursor(1)

	if m.activeTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeTerminalNum)
	}
	if m.terminalFocus != terminalFocusList {
		t.Fatalf("flow focus = %d, want list focus", m.terminalFocus)
	}
	if m.terminalPrefixActive {
		t.Fatal("list navigation should not enable terminal command mode")
	}
}

func TestFlowFilterSyncsActiveTerminalToSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Alpha"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Bravo"},
	)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	m = m.setSearchActive(true)

	next, _ := m.handleSearchKey(tea.KeyPressMsg{Text: string([]rune("Bravo"))})
	m = next.(Model)

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeTerminalNum)
	}
	if m.terminalFocus != terminalFocusList {
		t.Fatalf("flow focus = %d, want list focus", m.terminalFocus)
	}
}

func TestMoveSelectedFlowPhaseSyncsTerminalWhenCrossingToNextFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{
			FlowID:   "flow-1",
			RepoPath: "/dev/alpha",
			Title:    "Flow one",
			Phases: []flowstore.FlowPhase{
				{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseRunning},
			},
		},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.expandedFlowID = "flow-1"
	m.selectedFlowPhaseID = "implementation"
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:      1,
			Scope:       embeddedTerminalScopeFlow,
			FlowID:      "flow-1",
			FlowPhaseID: "implementation",
			Terminal:    internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}

	m = m.moveCursor(1)

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeTerminalNum)
	}
}

func TestHandleFlowResultSyncsTerminalAfterPreservingSelectedFlow(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 42
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want selected Flow terminal 2", m.activeTerminalNum)
	}
}

func TestHandleFlowResultPreservesActiveTerminalWhileTerminalFocused(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.activeTerminalNum = 2
	m.terminalFocus = terminalFocusTerminal
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 44
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one updated"},
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-1" {
		t.Fatalf("selected Flow = %q, want flow-1", got)
	}
	if m.activeTerminalNum != 2 {
		t.Fatalf("active Flow terminal = %d, want explicitly selected terminal 2", m.activeTerminalNum)
	}
}

func TestHandleFlowResultOffFlowsModeDoesNotRetargetActiveTerminal(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.topMode = ui.ModeWorktrees
	m.contentPane = ui.PaneTop
	m.activeFlowSurface = false
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
		{
			Number:   2,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-2",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 45
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two updated"},
			{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-2" {
		t.Fatalf("selected Flow = %q, want flow-2", got)
	}
	if m.activeTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1 off flows mode", m.activeTerminalNum)
	}
}

func TestHandleFlowResultPreservesActiveTerminalWhenClampedSelectionHasNoMatch(t *testing.T) {
	m := internalFlowsModel(
		flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/dev/alpha", Title: "Flow one"},
		flowstore.FlowRecord{FlowID: "flow-2", RepoPath: "/dev/alpha", Title: "Flow two"},
	)
	m.flows = m.flows.Move(1, 20, 80)
	m.activeTerminalNum = 1
	m.embeddedTerminals = []embeddedTerminalSlot{
		{
			Number:   1,
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   "flow-1",
			Terminal: internalFakeEmbeddedTerminal{},
		},
	}
	const request = 43
	m.listRequests[int(ui.ModeFlows)] = request

	m, _ = m.handleFlowResult(FlowResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Flows: []flowstore.FlowRecord{
			{FlowID: "flow-3", RepoPath: "/dev/alpha", Title: "Flow three"},
		},
	})

	if got := m.selectedFlowID(); got != "flow-3" {
		t.Fatalf("selected Flow = %q, want clamped flow-3", got)
	}
	if m.activeTerminalNum != 1 {
		t.Fatalf("active Flow terminal = %d, want unchanged terminal 1", m.activeTerminalNum)
	}
}

func TestDismissActiveFlowTerminalKeepsUnifiedDockFocusedOnSurvivor(t *testing.T) {
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    2,
			terminalFocus:        terminalFocusTerminal,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{
				{
					Number:   1,
					Scope:    embeddedTerminalScopeSession,
					Provider: "codex",
					Identity: "session",
					Terminal: internalFakeEmbeddedTerminal{},
					ID:       1,
				},
				{
					Number:      2,
					Scope:       embeddedTerminalScopeFlow,
					Provider:    "codex",
					Identity:    "implementation",
					FlowID:      "flow-1",
					FlowPhaseID: "implementation",
					Terminal:    internalFakeEmbeddedTerminal{state: "exited"},
					ID:          2,
				},
			}}}

	m = m.dismissEmbeddedTerminal(2)

	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Scope != embeddedTerminalScopeSession {
		t.Fatalf("remaining terminals = %#v, want only session terminal", m.embeddedTerminals)
	}
	if m.activeTerminalNum != 1 {
		t.Fatalf("active session terminal = %d, want 1", m.activeTerminalNum)
	}
	if m.terminalFocus != terminalFocusTerminal {
		t.Fatalf("terminal focus = %d, want terminal", m.terminalFocus)
	}
	if !m.terminalPrefixActive {
		t.Fatal("terminal command state should remain active on the surviving session terminal")
	}
}

func TestDismissLastFlowTerminalPreservesSessionCommandState(t *testing.T) {
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    1,
			terminalFocus:        terminalFocusTerminal,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{
				{
					Number:   1,
					Scope:    embeddedTerminalScopeSession,
					Provider: "codex",
					Identity: "session",
					Terminal: internalFakeEmbeddedTerminal{},
					ID:       1,
				},
				{
					Number:      2,
					Scope:       embeddedTerminalScopeFlow,
					Provider:    "codex",
					Identity:    "implementation",
					FlowID:      "flow-1",
					FlowPhaseID: "implementation",
					Terminal:    internalFakeEmbeddedTerminal{state: "exited"},
					ID:          2,
				},
			}}}

	m = m.dismissEmbeddedTerminal(2)

	if len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].Scope != embeddedTerminalScopeSession {
		t.Fatalf("remaining terminals = %#v, want only session terminal", m.embeddedTerminals)
	}
	if m.activeTerminalNum != 1 {
		t.Fatalf("active session terminal = %d, want 1", m.activeTerminalNum)
	}
	if !m.terminalPrefixActive {
		t.Fatal("session terminal command state should survive background Flow terminal dismissal")
	}
}

func TestSessionTerminalPrefixDDetachesActiveTerminal(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	var gotTarget, gotCWD string
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		launchDetachedTerminal: func(target, cwd string) (actions.TerminalLaunchSpec, error) {
			gotTarget = target
			gotCWD = cwd
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		finalizeAgentSession: func(actions.AgentLaunchContext) error {
			t.Fatal("detach should not finalize the agent session")
			return nil
		}, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    1,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:       1,
				Scope:        embeddedTerminalScopeSession,
				Provider:     "codex",
				Identity:     "session",
				RepoPath:     "/dev/repo",
				WorktreePath: "/dev/worktree",
				WorkingDir:   "/dev/worktree/subdir",
				Terminal:     term,
				ID:           1,
			}}}}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if gotTarget != "approach-agent-session" {
		t.Fatalf("handoff target = %q, want approach-agent-session", gotTarget)
	}
	if gotCWD != "/dev/worktree/subdir" {
		t.Fatalf("handoff cwd = %q, want /dev/worktree/subdir", gotCWD)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
	if !strings.Contains(next.status.Text, "opening terminal: approach-agent-session") {
		t.Fatalf("status = %q, want opening-terminal detach target", next.status.Text)
	}
	msg, ok := cmd().(EmbeddedTerminalDetachHandoffResultMsg)
	if !ok {
		t.Fatalf("handoff command message = %#v, want EmbeddedTerminalDetachHandoffResultMsg", msg)
	}
	if msg.Target != "approach-agent-session" || msg.Err != "" {
		t.Fatalf("handoff message = %#v, want successful target", msg)
	}
	updated, _ := next.Update(msg)
	next = updated.(Model)
	if !strings.Contains(next.status.Text, "Detached embedded terminal and opened terminal: approach-agent-session") {
		t.Fatalf("status = %q, want opened-terminal detach target", next.status.Text)
	}
}

func TestFlowTerminalPrefixDDetachesActiveTerminalAndRenumbers(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-flow-agent"}
	var gotTarget, gotCWD string
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom,
		modal:       modal.OpenConfirm(embeddedTerminalTerminatePrompt, nil),
		launchDetachedTerminal: func(target, cwd string) (actions.TerminalLaunchSpec, error) {
			gotTarget = target
			gotCWD = cwd
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
		finalizeAgentSession: func(actions.AgentLaunchContext) error {
			t.Fatal("detach should not finalize the agent session")
			return nil
		}, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:        terminalFocusTerminal,
			activeTerminalNum:    1,
			terminalPrefixActive: true,
			terminalConfirmID:    1,
			embeddedTerminals: []embeddedTerminalSlot{
				{
					Number:       1,
					Scope:        embeddedTerminalScopeFlow,
					Provider:     "codex",
					Identity:     "implementation",
					RepoPath:     "/dev/repo",
					WorktreePath: "/dev/worktree",
					FlowID:       "flow-1",
					FlowPhaseID:  "implementation",
					Terminal:     term,
					ID:           1,
				},
				{
					Number:      2,
					Scope:       embeddedTerminalScopeFlow,
					Provider:    "claude",
					Identity:    "review",
					FlowID:      "flow-1",
					FlowPhaseID: "review",
					Terminal:    internalFakeEmbeddedTerminal{},
					ID:          2,
				},
				{
					Number:   3,
					Scope:    embeddedTerminalScopeSession,
					Provider: "codex",
					Identity: "session",
					Terminal: internalFakeEmbeddedTerminal{},
					ID:       3,
				},
			}}}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeFlow)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 2 {
		t.Fatalf("embedded terminals = %#v, want detached Flow terminal removed only", next.embeddedTerminals)
	}
	if next.embeddedTerminals[0].Scope != embeddedTerminalScopeFlow || next.embeddedTerminals[0].Number != 1 {
		t.Fatalf("remaining Flow terminal not renumbered to 1: %#v", next.embeddedTerminals)
	}
	if next.embeddedTerminals[1].Scope != embeddedTerminalScopeSession || next.embeddedTerminals[1].Number != 2 {
		t.Fatalf("session terminal should be preserved: %#v", next.embeddedTerminals)
	}
	if next.terminalConfirmID != 0 || next.modal.View().Kind != modal.None {
		t.Fatalf("stale confirmation was not cleared: confirm=%d modal=%#v", next.terminalConfirmID, next.modal.View())
	}
	if activity := next.flowTerminalActivity(); len(activity) != 1 || activity[0].PhaseID != "review" {
		t.Fatalf("flow terminal activity = %#v, want only remaining Flow terminal", activity)
	}
	if gotTarget != "approach-flow-agent" {
		t.Fatalf("handoff target = %q, want approach-flow-agent", gotTarget)
	}
	if gotCWD != "/dev/worktree" {
		t.Fatalf("handoff cwd = %q, want /dev/worktree", gotCWD)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
}

func TestTerminalPrefixDReportsHandoffConstructionFailureAfterDetach(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		launchDetachedTerminal: func(string, string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("no external terminal")
		}, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    1,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: term,
				ID:       1,
			}}}}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if !term.detached {
		t.Fatal("terminal was not detached")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if cmd != nil {
		t.Fatal("construction failure should not return a handoff command")
	}
	if !strings.Contains(next.status.Text, "Detached embedded terminal, but failed to open terminal: no external terminal") {
		t.Fatalf("status = %q, want detach-success handoff failure", next.status.Text)
	}
}

func TestTerminalPrefixDReportsHandoffRunFailureAfterDetach(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-agent-session"}
	cleaned := false
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom,
		launchDetachedTerminal: func(string, string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{
				Cmd:     exec.Command("sh", "-c", "exit 7"),
				Cleanup: func() { cleaned = true },
			}, nil
		}, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    1,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: term,
				ID:       1,
			}}}}

	next, cmd, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatalf("embedded terminals = %#v, want detached terminal dismissed", next.embeddedTerminals)
	}
	if cmd == nil {
		t.Fatal("detach should return a handoff command")
	}
	msg, ok := cmd().(EmbeddedTerminalDetachHandoffResultMsg)
	if !ok {
		t.Fatalf("handoff command message = %#v, want EmbeddedTerminalDetachHandoffResultMsg", msg)
	}
	if msg.Err == "" {
		t.Fatalf("handoff message = %#v, want run error", msg)
	}
	if !cleaned {
		t.Fatal("handoff cleanup should run when handoff command fails")
	}
	updated, _ := next.Update(msg)
	next = updated.(Model)
	if !strings.Contains(next.status.Text, "Detached embedded terminal, but failed to open terminal:") {
		t.Fatalf("status = %q, want detach-success handoff failure", next.status.Text)
	}
}

func TestTerminalPrefixDReportsUnavailableForDirectPTY(t *testing.T) {
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeSessions,
		contentPane: ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			activeTerminalNum:    1,
			terminalPrefixActive: true,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:   1,
				Scope:    embeddedTerminalScopeSession,
				Provider: "codex",
				Identity: "session",
				Terminal: internalFakeEmbeddedTerminal{},
				ID:       1,
			}}}}

	next, _, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeSession)

	if !consumed {
		t.Fatal("detach key should be consumed")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("non-detachable terminal should remain, got %#v", next.embeddedTerminals)
	}
	if !strings.Contains(next.status.Text, "Detach unavailable") {
		t.Fatalf("status = %q, want unavailable message", next.status.Text)
	}
}

func TestFlowTerminalInputModeDPassesThrough(t *testing.T) {
	term := &internalFakeDetachableEmbeddedTerminal{target: "approach-flow-agent"}
	m := Model{
		topMode:     ui.ModeWorktrees,
		bottomMode:  ui.ModeFlows,
		contentPane: ui.PaneBottom,
		activePane:  ui.PaneBottom, embeddedTerminalState: embeddedTerminalState{
			terminalFocus:        terminalFocusTerminal,
			activeTerminalNum:    1,
			terminalPrefixActive: false,
			embeddedTerminals: []embeddedTerminalSlot{{
				Number:   1,
				Scope:    embeddedTerminalScopeFlow,
				Provider: "codex",
				Identity: "implementation",
				Terminal: term,
				ID:       1,
			}}}}

	next, _, consumed := m.handleEmbeddedTerminalKeyForScope(tea.KeyPressMsg{Text: string([]rune("d"))}, embeddedTerminalScopeFlow)

	if !consumed {
		t.Fatal("terminal input key should be consumed")
	}
	if term.detached {
		t.Fatal("input-mode d should not detach")
	}
	if len(term.writes) != 1 || string(term.writes[0]) != "d" {
		t.Fatalf("writes = %#v, want d passed through", term.writes)
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("terminal should remain, got %#v", next.embeddedTerminals)
	}
}

// TestDockLaunchesReachTheStarterEmbedded is the regression pin for deleting
// the two ctx.Embedded = true forces this file used to carry. Every context
// that reaches the dock has to describe the dock itself, because nothing below
// startEmbeddedTerminal can tell that it did not: a context that arrives false
// renders a full-screen agent badly and fails nothing.
func TestDockLaunchesReachTheStarterEmbedded(t *testing.T) {
	t.Run("Flow phase launch", func(t *testing.T) {
		h := newManualLaunchHarness(t, manualLaunchFlowRecord())

		m := h.launch(h.model())

		if len(h.launchContexts) != 1 {
			t.Fatalf("embedded launches = %d, want 1; status=%q", len(h.launchContexts), m.status.Text)
		}
		if !h.launchContexts[0].Embedded {
			t.Fatal("a Flow dock launch must reach the starter embedded")
		}
	})

	t.Run("non-Flow session resume", func(t *testing.T) {
		var started []actions.AgentLaunchContext
		m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{
			AgentCommand: "codex",
			StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
				started = append(started, ctx)
				return internalFakeEmbeddedTerminal{state: "running"}, nil
			},
		})
		record := sessions.SessionRecord{
			Provider:     sessions.ProviderCodex,
			SessionID:    "session-1",
			RepoPath:     "/dev/alpha",
			WorktreePath: "/dev/alpha",
			CWD:          "/dev/alpha",
		}

		ctx, release, ok, next := m.sessionResumeLaunchContext(record)
		if !ok {
			t.Fatalf("resume was refused, status = %q", next.status.Text)
		}
		next, _ = next.resumeSessionForBackend(ctx, record, release)

		if len(started) != 1 {
			t.Fatalf("embedded starts = %d, want the resume", len(started))
		}
		if !started[0].Embedded {
			t.Fatal("a non-Flow dock resume must reach the starter embedded")
		}
		if !next.hasRunningEmbeddedTerminal() {
			t.Fatal("the resume must install a dock slot")
		}
	})
}

// TestEmbeddedTerminalNeverForcesEmbedded encodes approach-hyl.15's first
// acceptance criterion as a build-time rule: the embedded open is a consumer of
// the transport bit, not its author. Every launch context arrives here already
// describing its own transport, so a write in this file would be a context
// being repaired after the fact — exactly the drift the seam work removed.
func TestEmbeddedTerminalNeverForcesEmbedded(t *testing.T) {
	const name = "embedded_terminal.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, target := range assign.Lhs {
			selector, ok := target.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Embedded" {
				continue
			}
			t.Errorf("%s: %s assigns Embedded; the transport bit belongs to the context's builder or to the transport that clears it",
				fset.Position(assign.Pos()), name)
		}
		return true
	})
}
