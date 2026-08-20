package model

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/embeddedterm"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

func NewRealEmbeddedTerminalForTest(term *embeddedterm.Terminal) EmbeddedTerminal {
	return realEmbeddedTerminal{term: term}
}

func SetSearchActiveForTest(m Model, active bool) Model {
	return m.setSearchActive(active)
}

func FormForTest(m Model) ui.FormView {
	return uiFormView(m.modal.View().Form)
}

func modelWithModeForTest(m Model, mode ui.Mode) Model {
	m.topMode, m.bottomMode, m.contentPane, m.activeFlowSurface = startupPaneState(mode)
	return m
}

func ActiveFlowCreateForTest(m Model) uint64 {
	return m.flowCreateReq.current
}

func ActiveFlowSelectedForTest(m Model) int {
	return m.activeFlows.SelectedIndex()
}

func ActiveFlowsForTest(m Model) []flowstore.FlowRecord {
	flows, _, _ := m.activeFlows.View()
	return flows
}

// ActiveFlowRecordsForTest exposes the cross-repository Flow cache, which
// outlives the visible Active Flows rows across repository selection changes.
func ActiveFlowRecordsForTest(m Model) []flowstore.FlowRecord {
	return m.activeFlowRecords
}

func SelectedActiveFlowPhaseIDForTest(m Model) string {
	return m.selectedActiveFlowPhaseID
}

func EmbeddedTerminalTickMsgForTest(m Model) any {
	return embeddedTerminalTickMsg{Generation: m.embeddedTerminalTickGen}
}

func HasRunningFlowEmbeddedTerminalForPhaseForTest(m Model, flowID, phaseID string) bool {
	return m.hasRunningFlowEmbeddedTerminalForPhase(flowID, phaseID)
}

func ClearFlowEmbeddedTerminalsForTest(m Model) Model {
	var kept []embeddedTerminalSlot
	for _, slot := range m.embeddedTerminals {
		if slot.Scope != embeddedTerminalScopeFlow {
			kept = append(kept, slot)
		}
	}
	m.embeddedTerminals = kept
	return m
}

func WithFlowWorktreeInspectionForTest(m Model, inspect func(string) error) Model {
	m.launchSeams.InspectWorktreeDirectory = inspect
	return m
}

func AutoAdvanceResultForTest(m Model, flows []flowstore.FlowRecord) (Model, tea.Cmd) {
	m.autoAdvanceInFlight = 1
	return m.handleAutoAdvanceResult(AutoAdvanceResultMsg{Flows: flows, Request: 1})
}

func AutoAdvanceLaunchCommandForTest(m Model, flows []flowstore.FlowRecord) (Model, tea.Cmd) {
	previous := cloneFlowRecords(m.autoAdvanceSnapshot)
	current := cloneFlowRecords(flows)
	m.autoAdvanceSnapshot = current
	var cmds []tea.Cmd
	var cmd tea.Cmd
	m, cmd, _ = m.prepareAutoFlowPhaseLaunch(previous, current)
	cmds = append(cmds, cmd)
	return m, batchNonNil(cmds...)
}

// AutoFlowLaunchForTest is one prepared AutoMode launch: the context its route
// carries, and the opaque lifecycle event that completes the launch when it is
// fed back through Update.
type AutoFlowLaunchForTest struct {
	Context  actions.AgentLaunchContext
	Embedded bool
	Prepared tea.Msg
}

// AutoFlowLaunchesForTest drives the AutoMode launch lifecycle from the advance
// poll's command to its prepared launches. Read events are applied through
// Update — the hop that re-arms the drain and emits the transient status —
// while the prepare hop is run without being applied, so a test observes the
// context and the persisted launch ID without opening a terminal.
func AutoFlowLaunchesForTest(m Model, cmd tea.Cmd) (Model, []AutoFlowLaunchForTest) {
	m, prepared := preparedAutoFlowLaunches(m, cmd)
	var launches []AutoFlowLaunchForTest
	for _, event := range prepared {
		if event.Err != "" || event.Skipped {
			continue
		}
		launches = append(launches, AutoFlowLaunchForTest{
			Context:  event.Context,
			Embedded: event.Route == flowLaunchRouteEmbedded,
			Prepared: event,
		})
	}
	return m, launches
}

func WithAutoAdvanceSnapshotForTest(m Model, flows []flowstore.FlowRecord) Model {
	m.autoAdvanceSnapshot = cloneFlowRecords(flows)
	return m
}

func FetchForModeForTest(m Model) tea.Cmd {
	return m.fetchForMode()
}

// FlowLaunchAttemptHeldForTest reports whether a launch lifecycle attempt owns
// this Flow. It is the successor to the repair pending map that external tests
// used to inspect directly.
func FlowLaunchAttemptHeldForTest(m Model, flowID string) bool {
	return m.flowLaunchAttemptOccupied(flowID)
}

func WithFlowLaunchEnsureWorktreeForTest(m Model, ensure func(flowstore.FlowRecord) (flowstore.FlowRecord, error)) Model {
	m.launchSeams.EnsureWorktree = ensure
	return m
}

func OpenFlowEmbeddedTerminalForTest(m Model, ctx actions.AgentLaunchContext) (Model, tea.Cmd) {
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
	if ctx.FlowID != "" && next.flowRefreshSurfaceVisible() {
		var fetchCmd tea.Cmd
		next, fetchCmd = next.startFlowSurfaceFetch()
		return next, batchNonNil(fetchCmd, prefillCmd, tickCmd)
	}
	return next, batchNonNil(prefillCmd, tickCmd)
}

func FlowPhaseDoneInstructionForTest() string {
	return flowPhaseDoneInstruction
}

func FlowPlanPromptForTest(record flowstore.FlowRecord, templates FlowPromptTemplates) string {
	return flowPlanPrompt(record, flowstore.FlowPhase{PhaseID: flowPlanPhaseID, Title: "Plan", Kind: flowstore.KindPlan}, templates, "")
}

func FlowPhasePromptForTest(record flowstore.FlowRecord, phase flowstore.FlowPhase, planPath, planBody string, templates FlowPromptTemplates) string {
	return flowPhasePrompt(record, phase, planPath, planBody, templates, "")
}

func FlowPreparationAdmissionForTest(m Model) bool {
	return m.flowPreparationAdmission
}

func SliceEpicLaunchInFlightForTest(m Model) bool {
	return m.sliceEpicLaunchInFlight()
}

func FlowLaunchContextRequiresLifecycleForTest(ctx actions.AgentLaunchContext) bool {
	return flowLaunchContextRequiresLifecycle(ctx)
}

func ModalOpenForTest(m Model) bool {
	return m.modal.IsOpen()
}
