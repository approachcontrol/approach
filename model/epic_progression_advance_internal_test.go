package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
)

func progressionAdvanceFlow(flowID, repoPath, childID, epicID, status string) flowstore.FlowRecord {
	stamp := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	return flowstore.FlowRecord{
		FlowID: flowID, RepoPath: repoPath, Title: childID, Status: status,
		Bead: flowstore.BeadLink{ID: childID, EpicID: epicID}, PreparedAt: &stamp,
	}
}

func TestEpicProgressionEnableIgnoresAlreadyInFlightTerminalSnapshot(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	pending := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(pending)
	terminal.Status = flowstore.StatusCompleted

	m := Model{autoAdvanceRequestSeq: 1, autoAdvanceInFlight: 1}
	m, _ = m.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target:              beadExpansionTarget{repoPath: repo, epicID: epic},
		flow:                pending,
		known:               true,
		enabled:             true,
		baselineDisposition: epicProgressionBaselineReplace,
	})

	next, _ := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	if next.activeEpicProgressionAdvance.Request != 0 || next.flowPreparationAdmission {
		t.Fatalf("stale request scheduled progression: active=%#v admission=%t", next.activeEpicProgressionAdvance, next.flowPreparationAdmission)
	}
	if got := next.epicProgressionBaselines[key]; got.Status != flowstore.StatusPending {
		t.Fatalf("baseline = %#v, want the enable-time pending record", got)
	}
}

func TestEpicProgressionAdvanceUsesReadyOrderAndSkipsExactLinkedChildren(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	linked := progressionAdvanceFlow("linked-a", repo, "epic.a", epic, flowstore.StatusCompleted)
	successor := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	var requests []FlowStartRequest
	beadQueries := 0
	releases := 0
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			beadQueries++
			return []beadsquery.Bead{{ID: "epic.a", Title: "A"}, {ID: "epic.b", Title: "B"}}, nil
		},
		listReadyBeads: func(string) ([]beadsquery.Bead, error) {
			beadQueries++
			return []beadsquery.Bead{{ID: "epic.a", Title: "A"}, {ID: "epic.b", Title: "B"}}, nil
		},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{terminal, linked}, nil
		},
		createFlow: func(request FlowStartRequest) (FlowStartResult, error) {
			requests = append(requests, request)
			return FlowStartResult{Flow: successor}, nil
		},
		reserveEpicSuccessor: func(string) (func(), error) {
			return func() { releases++ }, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorAccepted, Flow: successor}, nil
		},
	}

	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	var result epicProgressionAdvanceResultMsg
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if candidate, ok := msg.(epicProgressionAdvanceResultMsg); ok {
			result = candidate
		}
	}
	if result.request == 0 {
		t.Fatal("success-terminal edge did not schedule epic progression")
	}
	if len(requests) != 1 || requests[0].Bead != (flowstore.BeadLink{ID: "epic.b", EpicID: epic}) ||
		!strings.Contains(requests[0].Instructions, "bd show epic.b") {
		t.Fatalf("FlowStartRequest = %#v", requests)
	}
	if beadQueries != 2 {
		t.Fatalf("Beads queries = %d, want direct children and ready", beadQueries)
	}
	next, _ = updateFlowRefreshTest(next, result)
	if got := next.epicProgressionBaselines[key]; got.FlowID != successor.FlowID {
		t.Fatalf("baseline = %#v, want accepted successor", got)
	}
	if releases != 1 || next.flowPreparationAdmission {
		t.Fatalf("reservation releases = %d, admission = %t", releases, next.flowPreparationAdmission)
	}
}

func TestEpicProgressionAdvanceRetriesOwnedSuccessorBeforeLaterReadyChild(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusMerged
	owned := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	createCalls, beadQueries, reserves := 0, 0, 0
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			beadQueries++
			return []beadsquery.Bead{{ID: "epic.b"}, {ID: "epic.c"}}, nil
		},
		listReadyBeads: func(string) ([]beadsquery.Bead, error) {
			beadQueries++
			return []beadsquery.Bead{{ID: "epic.b"}, {ID: "epic.c"}}, nil
		},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{terminal}, nil
		},
		createFlow: func(FlowStartRequest) (FlowStartResult, error) {
			createCalls++
			return FlowStartResult{Flow: owned}, errors.New("finalize failed")
		},
		reserveEpicSuccessor: func(string) (func(), error) {
			reserves++
			if reserves == 1 {
				return nil, errors.New("busy")
			}
			return func() {}, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorAccepted, Flow: owned}, nil
		},
	}
	m.launchSeams.ReadFlow = func(string) (flowstore.FlowRecord, error) { return owned, nil }

	next, firstCmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	first := epicProgressionAdvanceMessage(t, firstCmd)
	next, _ = updateFlowRefreshTest(next, first)
	if got := next.epicProgressionOwnedSuccessors[key]; got.FlowID != owned.FlowID {
		t.Fatalf("owned successor = %#v", got)
	}

	next.autoAdvanceInFlight = 2
	next, secondCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 2})
	second := epicProgressionAdvanceMessage(t, secondCmd)
	next, _ = updateFlowRefreshTest(next, second)
	if createCalls != 1 || beadQueries != 2 || reserves != 2 {
		t.Fatalf("create=%d beadQueries=%d reserves=%d; owned retry queried or created past successor", createCalls, beadQueries, reserves)
	}
	if got := next.epicProgressionBaselines[key]; got.FlowID != owned.FlowID {
		t.Fatalf("retry baseline = %#v", got)
	}
}

func TestEpicProgressionAdvanceRotatesRetryingEpics(t *testing.T) {
	repo := "/repo"
	firstEpic, secondEpic := "epic-a", "epic-b"
	firstKey := epicProgressionBaselineKey(repo, firstEpic)
	secondKey := epicProgressionBaselineKey(repo, secondEpic)
	firstSource := progressionAdvanceFlow("flow-a", repo, "epic-a.1", firstEpic, flowstore.StatusPending)
	secondSource := progressionAdvanceFlow("flow-b", repo, "epic-b.1", secondEpic, flowstore.StatusPending)
	firstTerminal := cloneFlowRecord(firstSource)
	firstTerminal.Status = flowstore.StatusCompleted
	secondTerminal := cloneFlowRecord(secondSource)
	secondTerminal.Status = flowstore.StatusCompleted

	m := Model{
		autoAdvanceInFlight: 1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{
			firstKey:  firstSource,
			secondKey: secondSource,
		},
		readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{}, false, errors.New("store unavailable for " + key.EpicID)
		},
	}
	terminalFlows := []flowstore.FlowRecord{firstTerminal, secondTerminal}

	next, firstCmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: terminalFlows, Request: 1})
	first := epicProgressionAdvanceMessage(t, firstCmd)
	if first.epicKey != firstKey || first.disposition != epicProgressionAdvanceRetryable {
		t.Fatalf("first advance = key %q disposition %v, want retryable %q", first.epicKey, first.disposition, firstKey)
	}
	next, _ = updateFlowRefreshTest(next, first)

	next.autoAdvanceInFlight = 2
	_, secondCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: terminalFlows, Request: 2})
	second := epicProgressionAdvanceMessage(t, secondCmd)
	if second.epicKey != secondKey {
		t.Fatalf("second advance key = %q, want rotation to %q", second.epicKey, secondKey)
	}
}

func TestEpicProgressionRuntimeBaselineLifecycle(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	nonSuccess := cloneFlowRecord(source)
	nonSuccess.Title = "refreshed"
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted

	t.Run("startup terminal snapshot does not catch up", func(t *testing.T) {
		m := Model{autoAdvanceInFlight: 1}
		next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
		for _, msg := range immediateFlowRefreshMessages(cmd) {
			if _, ok := msg.(epicProgressionAdvanceResultMsg); ok {
				t.Fatal("terminal startup snapshot scheduled progression")
			}
		}
		if len(next.epicProgressionBaselines) != 0 {
			t.Fatalf("startup reconstructed baselines: %#v", next.epicProgressionBaselines)
		}
	})

	t.Run("non terminal observation refreshes exact baseline", func(t *testing.T) {
		m := Model{autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source}}
		next, _ := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{nonSuccess}, Request: 1})
		if next.epicProgressionBaselines[key].Title != "refreshed" {
			t.Fatalf("baseline = %#v", next.epicProgressionBaselines[key])
		}
	})

	for _, tt := range []struct {
		name string
		msg  AutoAdvanceResultMsg
	}{
		{name: "failed poll", msg: AutoAdvanceResultMsg{Err: "unavailable", Request: 1}},
		{name: "degraded poll", msg: AutoAdvanceResultMsg{Degradation: &flowstore.PartialListError{}, Request: 1}},
		{name: "missing tracked flow", msg: AutoAdvanceResultMsg{Flows: nil, Request: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source}}
			next, _ := updateFlowRefreshTest(m, tt.msg)
			if next.epicProgressionBaselines[key].FlowID != source.FlowID {
				t.Fatalf("baseline changed: %#v", next.epicProgressionBaselines)
			}
		})
	}
}

func TestEpicProgressionAdvanceChecksAuthoritativeStateBeforeSideEffects(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	for _, tt := range []struct {
		name         string
		progression  flowstore.EpicProgression
		found        bool
		err          error
		wantBaseline bool
	}{
		{name: "absent", found: false},
		{name: "disabled", found: true, progression: flowstore.EpicProgression{RepoPath: repo, EpicID: epic}},
		{name: "halted", found: true, progression: flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &flowstore.EpicProgressionHalt{ChildBeadID: "epic.a", Status: flowstore.StatusBlocked, Message: "halted"}}},
		{name: "unreadable", err: errors.New("corrupt row"), wantBaseline: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sideEffects := 0
			m := Model{
				autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return tt.progression, tt.found, tt.err
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { sideEffects++; return nil, nil },
				listReadyBeads:    func(string) ([]beadsquery.Bead, error) { sideEffects++; return nil, nil },
				listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { sideEffects++; return nil, nil },
				createFlow:        func(FlowStartRequest) (FlowStartResult, error) { sideEffects++; return FlowStartResult{}, nil },
			}
			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
			result := epicProgressionAdvanceMessage(t, cmd)
			next, _ = updateFlowRefreshTest(next, result)
			if sideEffects != 0 {
				t.Fatalf("side effects before authoritative state = %d", sideEffects)
			}
			_, present := next.epicProgressionBaselines[key]
			if present != tt.wantBaseline {
				t.Fatalf("baseline present = %t, want %t", present, tt.wantBaseline)
			}
		})
	}
}

func TestEpicProgressionOwnedObstructionForbidsBeadQueriesAndLaterSelection(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	for _, state := range []string{"missing-receipt", "blocked", "closed"} {
		t.Run(state, func(t *testing.T) {
			ownedFlow := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
			switch state {
			case "missing-receipt":
				ownedFlow.PreparedAt = nil
			case "blocked":
				ownedFlow.Status = flowstore.StatusBlocked
			case "closed":
				stamp := time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC)
				ownedFlow.Closed = flowstore.Closure{Reason: "closed", ClosedAt: &stamp}
			}
			beadQueries := 0
			reserves := 0
			reconciles := 0
			m := Model{
				autoAdvanceInFlight:      1,
				epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
				epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
					key: {SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: ownedFlow.FlowID},
				},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
					beadQueries++
					return []beadsquery.Bead{{ID: "epic.c"}}, nil
				},
				listReadyBeads: func(string) ([]beadsquery.Bead, error) { beadQueries++; return []beadsquery.Bead{{ID: "epic.c"}}, nil },
				setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
					t.Fatal("owned obstruction attempted exhaustion")
					return flowstore.EpicProgression{}, nil
				},
				reserveEpicSuccessor: func(string) (func(), error) {
					reserves++
					return func() {}, nil
				},
				reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
					reconciles++
					return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorOwnedObstruction, Flow: ownedFlow}, nil
				},
			}
			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
			result := epicProgressionAdvanceMessage(t, cmd)
			next, _ = updateFlowRefreshTest(next, result)
			if beadQueries != 0 || reserves != 1 || reconciles != 1 || !strings.Contains(next.status.Text, "blocks auto-progression") {
				t.Fatalf("queries=%d reserves=%d reconciles=%d status=%q", beadQueries, reserves, reconciles, next.status.Text)
			}
			if got := next.epicProgressionOwnedSuccessors[key]; got.FlowID != ownedFlow.FlowID {
				t.Fatalf("obstruction ownership = %#v", got)
			}
		})
	}
}

func TestEpicProgressionAuthoritativeInactiveWinsOverOwnedFlowCondition(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	ownedFlow := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusBlocked)
	owned := epicProgressionOwnedSuccessor{SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: ownedFlow.FlowID}
	releases := 0
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
			key: owned,
		},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		reserveEpicSuccessor: func(string) (func(), error) {
			return func() { releases++ }, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorInactive}, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			t.Fatal("owned successor queried Beads")
			return nil, nil
		},
	}

	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	next, _ = updateFlowRefreshTest(next, result)
	if _, present := next.epicProgressionBaselines[key]; present {
		t.Fatal("authoritative inactive result retained source baseline")
	}
	if _, present := next.epicProgressionOwnedSuccessors[key]; present {
		t.Fatal("authoritative inactive result retained owned successor")
	}
	if releases != 1 {
		t.Fatalf("reservation releases = %d, want 1", releases)
	}
}

func TestEpicProgressionAdvanceRefreshesVisibleFlowSurfaceFromPersistedOwnedID(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	owned := epicProgressionOwnedSuccessor{SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: "flow-b"}
	const ownerToken = 7
	listCalls := 0
	m := Model{
		activeFlowSurface:        true,
		flowPreparationAdmission: true,
		flowPreparationOwner:     flowPreparationOwner{Kind: flowPreparationEpicAdvance, Token: ownerToken},
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		activeEpicProgressionAdvance: epicProgressionAdvanceRequest{
			Request: 1, OwnerToken: ownerToken, EpicKey: key, SourceFlowID: source.FlowID,
		},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return nil, nil
		},
	}
	next, cmd := m.handleEpicProgressionAdvanceResult(epicProgressionAdvanceResultMsg{
		request: 1, ownerToken: ownerToken, epicKey: key, sourceFlowID: source.FlowID,
		disposition: epicProgressionAdvanceInactive, owned: owned, hasOwned: true,
	})
	_ = epicProgressionTestCommandMessages(cmd)
	if listCalls != 1 {
		t.Fatalf("Flow refresh calls = %d, want 1 for persisted owned successor", listCalls)
	}
	if _, present := next.epicProgressionBaselines[key]; present {
		t.Fatal("inactive result retained source baseline")
	}
}

func TestEpicProgressionExhaustionReconciliationMatrix(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	for _, tt := range []struct {
		name         string
		setErr       error
		readFound    bool
		readEnabled  bool
		readErr      error
		wantBaseline bool
		wantStatus   string
	}{
		{name: "successful disable", wantStatus: "no ready children remain"},
		{name: "write error reread absent", setErr: errors.New("write"), readFound: false, wantStatus: "no ready children remain"},
		{name: "write error reread off", setErr: errors.New("write"), readFound: true, wantStatus: "no ready children remain"},
		{name: "write error reread enabled", setErr: errors.New("write"), readFound: true, readEnabled: true, wantBaseline: true, wantStatus: "Could not disable"},
		{name: "write error reread error", setErr: errors.New("write"), readErr: errors.New("read"), wantStatus: "Could not confirm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			m := Model{
				autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					reads++
					if reads == 1 {
						return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
					}
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: tt.readEnabled}, tt.readFound, tt.readErr
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return nil, nil },
				listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return nil, nil },
				listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
				setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic}, tt.setErr
				},
			}
			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
			result := epicProgressionAdvanceMessage(t, cmd)
			next, _ = updateFlowRefreshTest(next, result)
			_, present := next.epicProgressionBaselines[key]
			if present != tt.wantBaseline || !strings.Contains(next.status.Text, tt.wantStatus) {
				t.Fatalf("baseline=%t status=%q", present, next.status.Text)
			}
		})
	}
}

func TestEpicProgressionPreparationAdmissionIsSingleFlightAndStaleResultFenced(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	successor := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	createCalls := 0
	releases := 0
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{terminal}, nil
		},
		createFlow: func(FlowStartRequest) (FlowStartResult, error) {
			createCalls++
			return FlowStartResult{Flow: successor}, nil
		},
		reserveEpicSuccessor: func(string) (func(), error) {
			return func() { releases++ }, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorAccepted, Flow: successor}, nil
		},
	}

	next, firstCmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	if !next.flowPreparationAdmission {
		t.Fatal("first poll did not retain preparation admission while command was active")
	}
	next.autoAdvanceInFlight = 2
	next, secondCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 2})
	for _, msg := range immediateFlowRefreshMessages(secondCmd) {
		if _, ok := msg.(epicProgressionAdvanceResultMsg); ok {
			t.Fatal("second poll admitted a concurrent progression preparation")
		}
	}
	if createCalls != 0 {
		t.Fatalf("create calls before first command ran = %d", createCalls)
	}

	stale := epicProgressionAdvanceMessage(t, firstCmd)
	// Simulate a newer owner before replaying the delayed completion. The stale
	// result may release its reservation, but cannot install its successor or
	// release the newer admission.
	next.flowPreparationSeq++
	next.flowPreparationOwner = flowPreparationOwner{Kind: flowPreparationEpicAdvance, Token: next.flowPreparationSeq}
	next.flowPreparationAdmission = true
	next.activeEpicProgressionAdvance = epicProgressionAdvanceRequest{
		Request: stale.request + 1, OwnerToken: next.flowPreparationSeq, EpicKey: key, SourceFlowID: source.FlowID,
	}
	next, _ = updateFlowRefreshTest(next, stale)
	if got := next.epicProgressionBaselines[key]; got.FlowID != source.FlowID {
		t.Fatalf("stale result replaced baseline with %#v", got)
	}
	if !next.flowPreparationAdmission || next.flowPreparationOwner.Token != next.flowPreparationSeq {
		t.Fatalf("stale result released newer owner: %#v", next.flowPreparationOwner)
	}
	if releases != 1 {
		t.Fatalf("stale reservation releases = %d, want 1", releases)
	}
}

func TestEpicProgressionExactLinkScopeDoesNotSuppressOtherRepositoryEpicOrNoncanonicalLink(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	successor := progressionAdvanceFlow("flow-new-a", repo, "epic.a", epic, flowstore.StatusPending)
	var request FlowStartRequest
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.a"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.a", Title: "A"}}, nil },
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			otherEpic := progressionAdvanceFlow("other-epic", repo, "epic.a", "another-epic", flowstore.StatusPending)
			otherRepo := progressionAdvanceFlow("other-repo", "/elsewhere", "epic.a", epic, flowstore.StatusPending)
			noncanonicalChild := progressionAdvanceFlow("noncanonical-child", repo, "epic.a ", epic, flowstore.StatusPending)
			noncanonicalEpic := progressionAdvanceFlow("noncanonical-epic", repo, "epic.a", " "+epic, flowstore.StatusPending)
			return []flowstore.FlowRecord{otherEpic, otherRepo, noncanonicalChild, noncanonicalEpic}, nil
		},
		createFlow: func(candidate FlowStartRequest) (FlowStartResult, error) {
			request = candidate
			return FlowStartResult{Flow: successor}, nil
		},
		reserveEpicSuccessor: func(string) (func(), error) {
			return func() {}, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorAccepted, Flow: successor}, nil
		},
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic}, nil
		},
	}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	next, _ = updateFlowRefreshTest(next, result)
	if request.Bead != (flowstore.BeadLink{ID: "epic.a", EpicID: epic}) || next.epicProgressionBaselines[key].FlowID != successor.FlowID {
		t.Fatalf("request=%#v baseline=%#v", request, next.epicProgressionBaselines[key])
	}
}

func TestEpicProgressionPreparationFailureWithoutFlowIDRetriesFreshSelection(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	queries, creates := 0, 0
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			queries++
			return []beadsquery.Bead{{ID: "epic.b"}}, nil
		},
		listReadyBeads: func(string) ([]beadsquery.Bead, error) { queries++; return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listFlows:      func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
		createFlow: func(FlowStartRequest) (FlowStartResult, error) {
			creates++
			return FlowStartResult{}, errors.New("prepare failed")
		},
	}
	for request := uint64(1); request <= 2; request++ {
		m.autoAdvanceInFlight = request
		next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: request})
		result := epicProgressionAdvanceMessage(t, cmd)
		m, _ = updateFlowRefreshTest(next, result)
	}
	if queries != 4 || creates != 2 || len(m.epicProgressionOwnedSuccessors) != 0 {
		t.Fatalf("queries=%d creates=%d owned=%#v", queries, creates, m.epicProgressionOwnedSuccessors)
	}
}

func TestEpicProgressionExhaustionReportsAllReadyChildrenAlreadyLinked(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	linked := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	creates := 0
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return []flowstore.FlowRecord{linked}, nil },
		createFlow:        func(FlowStartRequest) (FlowStartResult, error) { creates++; return FlowStartResult{}, nil },
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic}, nil
		},
	}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	next, _ = updateFlowRefreshTest(next, result)
	if creates != 0 || !strings.Contains(next.status.Text, "every ready child already has a Flow") {
		t.Fatalf("creates=%d status=%q", creates, next.status.Text)
	}
	if _, present := next.epicProgressionBaselines[key]; present {
		t.Fatal("linked-only exhaustion retained baseline")
	}
}

func TestEpicProgressionReconciliationFailureRetriesOwnedFlowWithoutReselection(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	ownedFlow := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	beadQueries, reconciles, releases := 0, 0, 0
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
			key: {SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: ownedFlow.FlowID},
		},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { beadQueries++; return nil, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { beadQueries++; return nil, nil },
		reserveEpicSuccessor: func(string) (func(), error) {
			return func() { releases++ }, nil
		},
		reconcileEpicSuccessor: func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error) {
			reconciles++
			if reconciles == 1 {
				return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorRetryable}, errors.New("store busy")
			}
			return flowstore.EpicProgressionSuccessorResult{Outcome: flowstore.EpicProgressionSuccessorAccepted, Flow: ownedFlow}, nil
		},
	}
	m.launchSeams.ReadFlow = func(string) (flowstore.FlowRecord, error) { return ownedFlow, nil }
	for request := uint64(1); request <= 2; request++ {
		m.autoAdvanceInFlight = request
		next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: request})
		result := epicProgressionAdvanceMessage(t, cmd)
		m, _ = updateFlowRefreshTest(next, result)
	}
	if beadQueries != 0 || reconciles != 2 || releases != 2 {
		t.Fatalf("queries=%d reconciles=%d releases=%d", beadQueries, reconciles, releases)
	}
	if m.epicProgressionBaselines[key].FlowID != ownedFlow.FlowID || len(m.epicProgressionOwnedSuccessors) != 0 {
		t.Fatalf("baseline=%#v owned=%#v", m.epicProgressionBaselines[key], m.epicProgressionOwnedSuccessors)
	}
}

func epicProgressionAdvanceMessage(t *testing.T, cmd tea.Cmd) epicProgressionAdvanceResultMsg {
	t.Helper()
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if result, ok := msg.(epicProgressionAdvanceResultMsg); ok {
			return result
		}
	}
	t.Fatal("command returned no epic progression result")
	return epicProgressionAdvanceResultMsg{}
}
