package model

import (
	"errors"
	"reflect"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func TestFlowFetchCarriesTypedPartialResultInsteadOfFatalError(t *testing.T) {
	healthy := flowForRefreshTest("healthy")
	partial := testPartialList("corrupt")
	m := NewWithOptions(flowRefreshTestRepos(), Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{healthy}, partial
		},
	})

	msg := flowResultFromCommand(t, m.Init())
	if len(msg.Flows) != 1 || msg.Flows[0].FlowID != healthy.FlowID {
		t.Fatalf("FlowResultMsg.Flows = %#v, want healthy Flow", msg.Flows)
	}
	if msg.Degradation != partial {
		t.Fatalf("FlowResultMsg.Degradation = %#v, want typed diagnostic %#v", msg.Degradation, partial)
	}
}

func TestRepositoryFlowDegradationFollowsAcceptedResultFreshness(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo/a"}, {Path: "/repo/b"}})
	m.width, m.height = 160, 26
	request := m.currentListRequest(ui.ModeFlows)
	partial := testPartialList("id1", "id2", "id3", "id4")

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath: "/repo/a", Flows: []flowstore.FlowRecord{{FlowID: "healthy", RepoPath: "/repo/a"}},
		Degradation: partial, ListRequest: request,
	})
	if got, want := m.flowDegradationWarning(ui.ModeFlows), "Skipped 4 unreadable Flows (id1, id2, id3, …); run approach flow list --json"; got != want {
		t.Fatalf("repository degradation warning = %q, want %q", got, want)
	}
	if got := m.currentListError(ui.ModeFlows); got != "" {
		t.Fatalf("partial result reused fatal list error = %q", got)
	}

	m, freshRequest := m.nextListFetchRequest(ui.ModeFlows)
	if got := m.flowDegradationWarning(ui.ModeFlows); got == "" {
		t.Fatal("starting same-repository request cleared degradation warning")
	}
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{RepoPath: "/repo/a", ListRequest: request})
	if got := m.flowDegradationWarning(ui.ModeFlows); got == "" {
		t.Fatal("stale clean result cleared degradation warning")
	}
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{RepoPath: "/repo/a", ListRequest: freshRequest})
	if got := m.flowDegradationWarning(ui.ModeFlows); got != "" {
		t.Fatalf("current clean result left degradation warning %q", got)
	}
}

func TestFlowDegradationWarningsAreIndependentAcrossRepositoryAndActiveSurfaces(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo/a"}, {Path: "/repo/b"}})
	repoRequest := m.currentListRequest(ui.ModeFlows)
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath: "/repo/a", Degradation: testPartialList("repo-bad"), ListRequest: repoRequest,
	})

	m = modelWithModeForTest(m, ui.ModeActiveFlows)
	m, activeRequest := m.nextListFetchRequest(ui.ModeActiveFlows)
	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{
		Degradation: testPartialList("global-bad"), ListRequest: activeRequest,
	})
	if got := m.flowDegradationWarning(ui.ModeFlows); got == "" {
		t.Fatal("repository warning missing before repository switch")
	}
	if got := m.flowDegradationWarning(ui.ModeActiveFlows); got == "" {
		t.Fatal("Active Flows warning missing before repository switch")
	}

	next, _ := m.moveRepoSelection(1)
	m = next.(Model)
	if got := m.flowDegradationWarning(ui.ModeFlows); got != "" {
		t.Fatalf("repository switch retained old repository warning %q", got)
	}
	if got := m.flowDegradationWarning(ui.ModeActiveFlows); got == "" {
		t.Fatal("repository switch cleared global Active Flows warning")
	}

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath: "/repo/a", Degradation: testPartialList("old-a"), ListRequest: repoRequest,
	})
	if got := m.flowDegradationWarning(ui.ModeFlows); got != "" {
		t.Fatalf("old repository result reinstalled warning %q", got)
	}

	m, freshActiveRequest := m.nextListFetchRequest(ui.ModeActiveFlows)
	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{ListRequest: activeRequest})
	if got := m.flowDegradationWarning(ui.ModeActiveFlows); got == "" {
		t.Fatal("stale clean Active Flows result cleared degradation warning")
	}
	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{ListRequest: freshActiveRequest})
	if got := m.flowDegradationWarning(ui.ModeActiveFlows); got != "" {
		t.Fatalf("current clean Active Flows result left warning %q", got)
	}
}

func TestFlowDegradationWarningCoexistsWithFatalCachedRefreshWarning(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo/a"}})
	request := m.currentListRequest(ui.ModeFlows)
	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath: "/repo/a", Flows: []flowstore.FlowRecord{{FlowID: "healthy", RepoPath: "/repo/a"}},
		Degradation: testPartialList("bad"), ListRequest: request,
	})
	m, _ = updateFlowRefreshTest(m, FetchErrorMsg{
		RepoPath: "/repo/a", Pane: "flows", Err: "failed to load flows: database unavailable",
		Kind: FetchList, Mode: ui.ModeFlows, ListRequest: request,
	})
	if got := m.flowDegradationWarning(ui.ModeFlows); got == "" {
		t.Fatal("fatal refresh cleared prior degradation warning")
	}
	if got := m.currentListError(ui.ModeFlows); got == "" {
		t.Fatal("fatal refresh did not retain cached-refresh warning state")
	}
	if got := m.paneFlowDegradationWarningRows(ui.ModeFlows) + m.paneCachedWarningRows(ui.ModeFlows); got != 2 {
		t.Fatalf("warning rows = %d, want two independent rows", got)
	}
}

func TestFlowDegradationWarningReflowsSelectionAndScrollGeometry(t *testing.T) {
	m := New([]scanner.Repo{{Path: "/repo/a"}})
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.width, m.height = 160, 26
	m.activePane, m.contentPane = ui.PaneBottom, ui.PaneBottom
	records := make([]flowstore.FlowRecord, 20)
	for i := range records {
		records[i] = flowstore.FlowRecord{FlowID: string(rune('a' + i)), RepoPath: "/repo/a"}
	}
	m.flows = m.flows.SetItems(records)
	cleanRows := m.paneContentHeight(ui.ModeFlows)
	m.flows = m.flows.Move(len(records)-1, cleanRows, m.contentWidth())

	m = m.setFlowDegradation(ui.ModeFlows, "/repo/a", testPartialList("bad"))
	if got := m.paneContentHeight(ui.ModeFlows); got != cleanRows-1 {
		t.Fatalf("degraded pane rows = %d, want %d", got, cleanRows-1)
	}
	if got, want := m.flows.Scroll(), len(records)-(cleanRows-1); got != want {
		t.Fatalf("degraded pane scroll = %d, want %d", got, want)
	}

	m = m.setCurrentListError(ui.ModeFlows, "refresh failed")
	if got := m.paneContentHeight(ui.ModeFlows); got != cleanRows-2 {
		t.Fatalf("two-warning pane rows = %d, want %d", got, cleanRows-2)
	}
	m = m.setFlowDegradation(ui.ModeFlows, "/repo/a", nil)
	if got := m.paneContentHeight(ui.ModeFlows); got != cleanRows-1 {
		t.Fatalf("cached-only pane rows = %d, want %d", got, cleanRows-1)
	}
}

func TestAutoAdvancePartialPollRetainsCompleteSnapshotAndDoesNotLaunch(t *testing.T) {
	baseline := autoAdvanceTestFlow("flow-1", "/repo/a", true, map[string]string{
		"plan": flowstore.PhaseRunning,
	})
	incomplete := autoAdvanceTestFlow("flow-1", "/repo/a", true, map[string]string{
		"plan":           flowstore.PhaseCompleted,
		"plan-review":    flowstore.PhaseCompleted,
		"implementation": flowstore.PhaseReady,
	})
	partial := testPartialList("missing-flow")
	var launches int
	m := NewWithOptions([]scanner.Repo{{Path: "/repo/a"}}, Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{incomplete}, partial
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			launches++
			return incomplete, nil
		},
	})
	m.autoAdvanceSnapshot = cloneFlowRecords([]flowstore.FlowRecord{baseline})

	fetched, ok := m.fetchAutoAdvanceFlows(1)().(AutoAdvanceResultMsg)
	if !ok || fetched.Degradation != partial {
		t.Fatalf("auto poll result = %#v, want typed partial diagnostic", fetched)
	}
	m.autoAdvanceInFlight = 1
	before := cloneFlowRecords(m.autoAdvanceSnapshot)
	beforeStatus := m.status
	m, _ = updateFlowRefreshTest(m, fetched)
	if !reflect.DeepEqual(m.autoAdvanceSnapshot, before) {
		t.Fatalf("partial poll changed baseline:\n got: %#v\nwant: %#v", m.autoAdvanceSnapshot, before)
	}
	if launches != 0 {
		t.Fatalf("partial poll launched %d phases, want none", launches)
	}
	if !reflect.DeepEqual(m.status, beforeStatus) {
		t.Fatalf("partial poll changed status from %#v to %#v", beforeStatus, m.status)
	}
}

func TestPartialDisplayResultsDoNotSeedAutoAdvanceSnapshot(t *testing.T) {
	baseline := autoAdvanceTestFlow("baseline", "/repo/a", true, map[string]string{"plan": flowstore.PhaseRunning})
	partialFlow := autoAdvanceTestFlow("partial", "/repo/a", true, map[string]string{"plan": flowstore.PhaseCompleted})
	m := New([]scanner.Repo{{Path: "/repo/a"}})
	m.autoAdvanceSnapshot = cloneFlowRecords([]flowstore.FlowRecord{baseline})
	want := cloneFlowRecords(m.autoAdvanceSnapshot)

	m, _ = updateFlowRefreshTest(m, FlowResultMsg{
		RepoPath: "/repo/a", Flows: []flowstore.FlowRecord{partialFlow},
		Degradation: testPartialList("repo-bad"), ListRequest: m.currentListRequest(ui.ModeFlows),
	})
	if !reflect.DeepEqual(m.autoAdvanceSnapshot, want) {
		t.Fatalf("partial repository result changed baseline:\n got: %#v\nwant: %#v", m.autoAdvanceSnapshot, want)
	}

	m = modelWithModeForTest(m, ui.ModeActiveFlows)
	m, request := m.nextListFetchRequest(ui.ModeActiveFlows)
	m, _ = updateFlowRefreshTest(m, ActiveFlowResultMsg{
		Flows: []flowstore.FlowRecord{partialFlow}, Degradation: testPartialList("global-bad"), ListRequest: request,
	})
	if !reflect.DeepEqual(m.autoAdvanceSnapshot, want) {
		t.Fatalf("partial Active Flows result changed baseline:\n got: %#v\nwant: %#v", m.autoAdvanceSnapshot, want)
	}
}

func testPartialList(ids ...string) *flowstore.PartialListError {
	partial := &flowstore.PartialListError{Entries: make([]flowstore.PartialListEntry, len(ids))}
	for i, id := range ids {
		partial.Entries[i] = flowstore.PartialListEntry{FlowID: id, Cause: errors.New("unreadable " + id)}
	}
	return partial
}
