package model

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func babysitterFlow(id, repo string, phaseStatus, mergeStatus string) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID: id, RepoPath: repo, Title: id, Status: flowstore.StatusInProgress,
		PR:    flowstore.PullRequest{Provider: "github", Number: 42, URL: "https://github.com/acme/project/pull/42", HeadBranch: "flow/feature", BaseBranch: "main"},
		Issue: flowstore.Issue{Provider: "github", Number: 7, URL: "https://github.com/acme/project/issues/7"},
		Merge: flowstore.Merge{Status: mergeStatus},
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseCompleted},
			{PhaseID: "merge", Kind: flowstore.KindMerge, Status: phaseStatus, DependsOn: []string{"implementation"}},
		},
	}
}

func TestTakeoverTransitionMatrixPreservesOneOriginalSnapshot(t *testing.T) {
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{})
	m.topMode = ui.ModeBeadsReady
	m.bottomMode = ui.ModePlans
	m.activePane = ui.PaneRepos
	m.contentPane = ui.PaneBottom

	entered, prCmd := m.handlePRBabysitterToggle()
	if prCmd == nil || entered.focusedMode() != ui.ModePRBabysitter || entered.activePane != ui.PaneRepos {
		t.Fatalf("PR entry = mode %d pane %d cmd %T", entered.focusedMode(), entered.activePane, prCmd)
	}
	entered.activePane = ui.PaneBottom
	switched, activeCmd := entered.handleActiveFlowsToggle()
	if activeCmd == nil || switched.focusedMode() != ui.ModeActiveFlows || switched.activePane != ui.PaneBottom {
		t.Fatalf("cross-switch = mode %d pane %d cmd %T", switched.focusedMode(), switched.activePane, activeCmd)
	}
	closed, _ := switched.handleActiveFlowsToggle()
	if closed.takeoverVisible() || closed.activePane != ui.PaneRepos || closed.contentPane != ui.PaneBottom || closed.topMode != ui.ModeBeadsReady || closed.bottomMode != ui.ModePlans {
		t.Fatalf("close did not restore original snapshot: takeover=%v pane=%d content=%d top=%d bottom=%d", closed.takeoverVisible(), closed.activePane, closed.contentPane, closed.topMode, closed.bottomMode)
	}

	contentEntry := m
	contentEntry.activePane = ui.PaneTop
	contentEntry.contentPane = ui.PaneTop
	contentEntry, _ = contentEntry.handleActiveFlowsToggle()
	contentEntry, crossCmd := contentEntry.handlePRBabysitterToggle()
	if crossCmd == nil || contentEntry.focusedMode() != ui.ModePRBabysitter || contentEntry.activePane != ui.PaneTop {
		t.Fatalf("Active-to-PR switch lost content focus: mode=%d pane=%d", contentEntry.focusedMode(), contentEntry.activePane)
	}
	contentEntry, _ = contentEntry.handlePRBabysitterToggle()
	if contentEntry.activePane != ui.PaneTop || contentEntry.contentPane != ui.PaneTop {
		t.Fatalf("PR close restored pane/content %d/%d, want top/top", contentEntry.activePane, contentEntry.contentPane)
	}
}

func TestPRBabysitterRefreshFiltersOrdersAndReplacesPerRowFailures(t *testing.T) {
	records := []flowstore.FlowRecord{
		babysitterFlow("flow-b", "/dev/beta", flowstore.PhaseReady, flowstore.MergePending),
		babysitterFlow("flow-a", "/dev/alpha", flowstore.PhaseBlocked, flowstore.MergeBlocked),
		{FlowID: "ineligible", RepoPath: "/dev/alpha"},
	}
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}, {Path: "/dev/beta"}}, Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return records, nil },
		LookupPRStatus: func(_ context.Context, _ int, _ string) (actions.PullRequestStatus, error) {
			return actions.PullRequestStatus{}, errors.New("lookup failed")
		},
	})
	m.activePane = ui.PaneBottom
	m.contentPane = ui.PaneBottom
	m, cmd := m.handlePRBabysitterToggle()
	msg, ok := cmd().(PRBabysitterResultMsg)
	if !ok {
		t.Fatalf("command returned %T", cmd())
	}
	if len(msg.Flows) != 2 || msg.Flows[0].FlowID != "flow-b" || msg.Flows[1].FlowID != "flow-a" {
		t.Fatalf("stable eligible order = %#v", msg.Flows)
	}
	m, poll := m.handlePRBabysitterResult(msg)
	if poll == nil || len(m.prBabysitterRecords) != 2 {
		t.Fatalf("accepted refresh rows=%d poll=%T", len(m.prBabysitterRecords), poll)
	}
	for _, record := range m.prBabysitterRecords {
		if got := m.prBabysitterStatuses[record.FlowID]; got != unknownPullRequestStatus() {
			t.Fatalf("status[%s] = %#v, want unknown columns", record.FlowID, got)
		}
	}
}

func TestPRBabysitterTotalFailureRetainsCacheAndSkipsGitHub(t *testing.T) {
	var lookups atomic.Int32
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}}, Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return nil, errors.New("store unavailable")
		},
		LookupPRStatus: func(context.Context, int, string) (actions.PullRequestStatus, error) {
			lookups.Add(1)
			return actions.PullRequestStatus{}, nil
		},
	})
	cached := babysitterFlow("flow-1", "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)
	m = m.setTakeover(takeoverPRBabysitter)
	m.prBabysitterRecords = []flowstore.FlowRecord{cached}
	m.prBabysitterStatuses = map[string]actions.PullRequestStatus{cached.FlowID: {Mergeability: actions.PRMergeable, Checks: actions.PRChecksPassing}}
	m = m.syncPRBabysitterFromCache()
	m, cmd := m.startPRBabysitterRefresh()
	msg := cmd().(PRBabysitterResultMsg)
	m, poll := m.handlePRBabysitterResult(msg)
	if poll == nil || lookups.Load() != 0 || len(m.prBabysitterRecords) != 1 || m.prBabysitterStatuses[cached.FlowID].Checks != actions.PRChecksPassing {
		t.Fatalf("total failure changed cache or queried GitHub: lookups=%d rows=%d statuses=%v", lookups.Load(), len(m.prBabysitterRecords), m.prBabysitterStatuses)
	}
	if m.currentListError(ui.ModePRBabysitter) == "" {
		t.Fatal("total failure did not retain a cached-data warning")
	}
}

func TestPRBabysitterAcceptsPartialListAndRejectsSupersededResult(t *testing.T) {
	record := babysitterFlow("flow-1", "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)
	partial := &flowstore.PartialListError{Entries: []flowstore.PartialListEntry{{FlowID: "broken", Cause: errors.New("bad JSON")}}}
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}}, Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{record}, partial
		},
		LookupPRStatus: func(context.Context, int, string) (actions.PullRequestStatus, error) {
			return actions.PullRequestStatus{Mergeability: actions.PRMergeable, Checks: actions.PRChecksPassing}, nil
		},
	})
	m = m.setTakeover(takeoverPRBabysitter)
	m, firstCmd := m.startPRBabysitterRefresh()
	first := firstCmd().(PRBabysitterResultMsg)
	m, secondCmd := m.startPRBabysitterRefresh()
	second := secondCmd().(PRBabysitterResultMsg)
	if first.ListRequest == second.ListRequest {
		t.Fatal("superseding refresh reused request token")
	}
	stale, stalePoll := m.handlePRBabysitterResult(first)
	if stalePoll != nil || len(stale.prBabysitterRecords) != 0 {
		t.Fatalf("stale result mutated rows or scheduled poll: rows=%d poll=%T", len(stale.prBabysitterRecords), stalePoll)
	}
	m, poll := m.handlePRBabysitterResult(second)
	if poll == nil || len(m.prBabysitterRecords) != 1 || m.flowDegradationWarning(ui.ModePRBabysitter) == "" {
		t.Fatalf("partial result rows=%d warning=%q poll=%T", len(m.prBabysitterRecords), m.flowDegradationWarning(ui.ModePRBabysitter), poll)
	}
}

func TestPRBabysitterPreservesSelectionByFlowIDAcrossAcceptedRefresh(t *testing.T) {
	first := babysitterFlow("flow-1", "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)
	second := babysitterFlow("flow-2", "/dev/beta", flowstore.PhaseReady, flowstore.MergePending)
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}, {Path: "/dev/beta"}}, Options{})
	m = m.setTakeover(takeoverPRBabysitter)
	m.activePane = ui.PaneBottom
	m.contentPane = ui.PaneBottom
	m.prBabysitterRecords = []flowstore.FlowRecord{first, second}
	m = m.syncPRBabysitterFromCache()
	m.prBabysitterFlows = m.prBabysitterFlows.SelectFunc(func(record flowstore.FlowRecord) bool { return record.FlowID == second.FlowID })
	m, request := m.nextListFetchRequest(ui.ModePRBabysitter)
	m, _ = m.handlePRBabysitterResult(PRBabysitterResultMsg{
		Flows: []flowstore.FlowRecord{second, first}, Statuses: map[string]actions.PullRequestStatus{
			first.FlowID: unknownPullRequestStatus(), second.FlowID: unknownPullRequestStatus(),
		}, ListRequest: request,
	})
	selected, ok := m.prBabysitterFlows.Selected()
	if !ok || selected.FlowID != second.FlowID {
		t.Fatalf("selected Flow = %#v, want %s", selected, second.FlowID)
	}
}

func TestPRBabysitterRefreshCapsConcurrencyAndCancelsOnExit(t *testing.T) {
	records := make([]flowstore.FlowRecord, 8)
	for i := range records {
		records[i] = babysitterFlow(string(rune('a'+i)), "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)
	}
	var active atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	lookup := func(ctx context.Context, _ int, _ string) (actions.PullRequestStatus, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return actions.PullRequestStatus{}, ctx.Err()
		case <-release:
			return actions.PullRequestStatus{Mergeability: actions.PRMergeable, Checks: actions.PRChecksPassing}, nil
		}
	}
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}}, Options{
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return records, nil }, LookupPRStatus: lookup,
	})
	m = m.setTakeover(takeoverPRBabysitter)
	m, cmd := m.startPRBabysitterRefresh()
	done := make(chan struct{})
	go func() { _ = cmd(); close(done) }()
	for range prBabysitterWorkers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	if maximum.Load() != prBabysitterWorkers {
		t.Fatalf("maximum concurrency = %d, want %d", maximum.Load(), prBabysitterWorkers)
	}
	m, _ = m.handlePRBabysitterToggle()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("exit did not cancel in-flight lookups")
	}
}

func TestPRBabysitterMutationRemovesIneligibleRowImmediately(t *testing.T) {
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}}, Options{})
	m = m.setTakeover(takeoverPRBabysitter)
	record := babysitterFlow("flow-1", "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)
	m.prBabysitterRecords = []flowstore.FlowRecord{record}
	m.prBabysitterStatuses = map[string]actions.PullRequestStatus{record.FlowID: unknownPullRequestStatus()}
	m = m.syncPRBabysitterFromCache()
	merged := record
	merged.Merge.Status = flowstore.MergeMerged
	m = m.replaceFlowRecord(merged, flowMutationWholeRecord, nil)
	if len(m.prBabysitterRecords) != 0 || len(m.prBabysitterStatuses) != 0 || m.prBabysitterFlows.Len() != 0 {
		t.Fatalf("merged mutation retained row/status: rows=%v statuses=%v pane=%d", m.prBabysitterRecords, m.prBabysitterStatuses, m.prBabysitterFlows.Len())
	}
}

func TestPRBabysitterReusesFlowActionsWithoutWideningBlockedMergeGates(t *testing.T) {
	var opened []string
	var copied string
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha"}}, Options{
		OpenURL: func(target string) error {
			opened = append(opened, target)
			return nil
		},
		CopyToClipboard: func(value string) error {
			copied = value
			return nil
		},
	})
	m.activePane = ui.PaneBottom
	m.contentPane = ui.PaneBottom
	m = m.setTakeover(takeoverPRBabysitter)
	record := babysitterFlow("flow-1", "/dev/alpha", flowstore.PhaseBlocked, flowstore.MergeBlocked)
	record.WorktreePath = "/dev/alpha-worktrees/flow-1"
	m.prBabysitterRecords = []flowstore.FlowRecord{record}
	m = m.syncPRBabysitterFromCache()

	for _, key := range []string{"p", "i", "c"} {
		_, cmd := m.handleActiveFlowSurfaceKey(key)
		if cmd == nil {
			t.Fatalf("%s action returned nil command", key)
		}
		_ = cmd()
	}
	if len(opened) != 2 || opened[0] != record.PR.URL || opened[1] != record.Issue.URL || copied != record.FlowID {
		t.Fatalf("shared actions opened=%v copied=%q", opened, copied)
	}
	if next, cmd := m.handleActiveFlowSurfaceKey("m"); cmd != nil || next.(Model).modal.IsOpen() {
		t.Fatalf("blocked babysitter row acquired m: cmd=%T modal=%v", cmd, next.(Model).modal.IsOpen())
	}
	if next, cmd := m.handleActiveFlowSurfaceKey("U"); cmd != nil || next.(Model).modal.IsOpen() {
		t.Fatalf("blocked babysitter row acquired U: cmd=%T modal=%v", cmd, next.(Model).modal.IsOpen())
	}
	if next, cmd := m.handleActiveFlowSurfaceKey("C"); cmd != nil || !next.(Model).modal.IsOpen() {
		t.Fatalf("C did not reuse Flow close modal: cmd=%T modal=%v", cmd, next.(Model).modal.IsOpen())
	}
}

func TestLookupPRStatusesReturnsStableResultsWithoutDataRaces(t *testing.T) {
	records := []flowstore.FlowRecord{babysitterFlow("one", "/dev/alpha", flowstore.PhaseReady, flowstore.MergePending)}
	var mu sync.Mutex
	called := false
	statuses := lookupPRStatuses(context.Background(), records, func(context.Context, int, string) (actions.PullRequestStatus, error) {
		mu.Lock()
		called = true
		mu.Unlock()
		return actions.PullRequestStatus{Mergeability: actions.PRMergeable, Checks: actions.PRChecksPassing}, nil
	})
	mu.Lock()
	defer mu.Unlock()
	if !called || statuses["one"].Checks != actions.PRChecksPassing {
		t.Fatalf("lookup result = %v called=%v", statuses, called)
	}
}
