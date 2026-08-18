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
	beadQueries := 0
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		agentCommand:             "codex",
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
	}

	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	if result.disposition != epicProgressionAdvanceSelected || result.owned.ChildID != "epic.b" {
		t.Fatalf("advance result = %#v, want the first unlinked ready child selected", result)
	}
	if beadQueries != 2 {
		t.Fatalf("Beads queries = %d, want direct children and ready", beadQueries)
	}
	next, selectedCmd := updateFlowRefreshTest(next, result)
	create := epicProgressionCreateRequest(t, selectedCmd).Create
	if create.Presentation != (flowLaunchCreatePresentation{Origin: flowLaunchOriginEpicProgression, Request: result.request}) ||
		create.Bead != (flowstore.BeadLink{ID: "epic.b", EpicID: epic}) || create.RepoPath != repo ||
		create.Title != "epic.b: B" || !strings.Contains(create.Instructions, "bd show epic.b") || !create.Headless {
		t.Fatalf("create request = %#v", create)
	}
	// Selection is only the first half of one attempt: the admission stays held
	// for the create pipeline that launches the child.
	if !next.flowPreparationAdmission || next.activeEpicProgressionAdvance.Request != result.request {
		t.Fatalf("selection released admission: admission=%t active=%#v", next.flowPreparationAdmission, next.activeEpicProgressionAdvance)
	}
	if got := next.epicProgressionBaselines[key]; got.FlowID != source.FlowID {
		t.Fatalf("selection replaced the baseline: %#v", got)
	}
}

func TestEpicProgressionAdvanceSkipsAnEpicWhoseChildIsAlreadyInFlight(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	m := Model{
		autoAdvanceInFlight:      1,
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
			key: {SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: "flow-b"},
		},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			t.Fatal("advance ran for an epic with a child already in flight")
			return flowstore.EpicProgression{}, false, nil
		},
	}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if _, ok := msg.(epicProgressionAdvanceResultMsg); ok {
			t.Fatal("owned in-flight child did not fence the advance edge")
		}
	}
	if next.flowPreparationAdmission {
		t.Fatal("fenced advance took preparation admission")
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
		{name: "done", found: true, progression: flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Done: true}},
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
		readDone     bool
		readHalt     bool
		readErr      error
		wantBaseline bool
		wantStatus   string
	}{
		{name: "successful disable", wantStatus: "no ready children remain"},
		{name: "write error reread done", setErr: errors.New("write"), readFound: true, readDone: true, wantStatus: "no ready children remain"},
		{name: "write error reread absent", setErr: errors.New("write"), readFound: false, wantStatus: "no longer active"},
		{name: "write error reread off", setErr: errors.New("write"), readFound: true, wantStatus: "no longer active"},
		{name: "write error reread halted", setErr: errors.New("write"), readFound: true, readHalt: true, wantStatus: "no longer active"},
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
					progression := flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: tt.readEnabled, Done: tt.readDone}
					if tt.readHalt {
						progression.Halt = &flowstore.EpicProgressionHalt{ChildBeadID: "epic.a", Status: flowstore.StatusBlocked, Message: "blocked"}
					}
					return progression, tt.readFound, tt.readErr
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return nil, nil },
				listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return nil, nil },
				listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
				setEpicProgression: func(update flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
					if update.Enabled || !update.Done {
						t.Fatalf("exhaustion update = %#v, want enabled=false done=true", update)
					}
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
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		agentCommand: "codex",
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{terminal}, nil
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

	stale := epicProgressionAdvanceMessage(t, firstCmd)
	// Simulate a newer owner before replaying the delayed selection. The stale
	// result may not submit a create request or release the newer admission.
	next.flowPreparationSeq++
	next.flowPreparationOwner = flowPreparationOwner{Kind: flowPreparationEpicAdvance, Token: next.flowPreparationSeq}
	next.flowPreparationAdmission = true
	newer := epicProgressionAdvanceRequest{
		Request: stale.request + 1, OwnerToken: next.flowPreparationSeq, EpicKey: key, SourceFlowID: source.FlowID,
	}
	next.activeEpicProgressionAdvance = newer
	next, staleCmd := updateFlowRefreshTest(next, stale)
	for _, msg := range immediateFlowRefreshMessages(staleCmd) {
		if _, ok := msg.(flowLaunchCreateRequestedMsg); ok {
			t.Fatal("stale selection submitted a create request")
		}
	}
	if !next.flowPreparationAdmission || next.activeEpicProgressionAdvance != newer {
		t.Fatalf("stale result disturbed the newer owner: admission=%t active=%#v", next.flowPreparationAdmission, next.activeEpicProgressionAdvance)
	}
	if got := next.epicProgressionBaselines[key]; got.FlowID != source.FlowID {
		t.Fatalf("stale result replaced baseline with %#v", got)
	}
}

// TestEpicProgressionSelectionScopeDoesNotSuppressOtherRepositoryLink pins the
// one dimension of the selection filter that repository scoping still governs.
//
// This test used to assert that a same-repository Flow under a different epic,
// and a Flow with an untrimmed Bead ID, also failed to suppress selection.
// Those cases are now deliberately suppressed: the filter is keyed on
// (repository, trimmed Bead ID, slot occupancy) to match the store's creation
// guard, and selecting a child the guard would refuse is an unbounded
// create-refuse loop. See TestEpicProgressionSelectionMatchesBeadSlotGuard for
// the cases that moved.
func TestEpicProgressionSelectionScopeDoesNotSuppressOtherRepositoryLink(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		agentCommand: "codex",
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.a"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.a", Title: "A"}}, nil },
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			otherRepo := progressionAdvanceFlow("other-repo", "/elsewhere", "epic.a", epic, flowstore.StatusPending)
			return []flowstore.FlowRecord{otherRepo}, nil
		},
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			t.Fatal("noncanonical or foreign links exhausted the epic")
			return flowstore.EpicProgression{}, nil
		},
	}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	next, selectedCmd := updateFlowRefreshTest(next, result)
	create := epicProgressionCreateRequest(t, selectedCmd).Create
	if create.Bead != (flowstore.BeadLink{ID: "epic.a", EpicID: epic}) {
		t.Fatalf("create request = %#v", create)
	}
	_ = key
	_ = next
}

func TestEpicProgressionExhaustionReportsAllReadyChildrenAlreadyLinked(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	linked := progressionAdvanceFlow("flow-b", repo, "epic.b", epic, flowstore.StatusPending)
	m := Model{
		autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.b"}}, nil },
		listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return []flowstore.FlowRecord{linked}, nil },
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic}, nil
		},
	}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
	result := epicProgressionAdvanceMessage(t, cmd)
	if result.disposition == epicProgressionAdvanceSelected {
		t.Fatalf("linked-only epic selected child %q", result.owned.ChildID)
	}
	next, _ = updateFlowRefreshTest(next, result)
	if !strings.Contains(next.status.Text, "every ready child already has a Flow") {
		t.Fatalf("status=%q", next.status.Text)
	}
	if _, present := next.epicProgressionBaselines[key]; present {
		t.Fatal("linked-only exhaustion retained baseline")
	}
}

func epicProgressionCreateRequest(t *testing.T, cmd tea.Cmd) flowLaunchCreateRequestedMsg {
	t.Helper()
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if request, ok := msg.(flowLaunchCreateRequestedMsg); ok {
			return request
		}
	}
	t.Fatal("selected advance submitted no create-then-launch request")
	return flowLaunchCreateRequestedMsg{}
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

// progressionAdvanceFlow sets Status but no Phases, and DeriveStatus ignores
// FlowRecord.Status except for abandoned — so every fixture it builds derives
// pending and occupies its Bead slot. Tests that need a genuinely terminal Flow
// must say so structurally, which is what this helper is for.
func progressionAdvanceTerminalFlow(flowID, repoPath, childID, epicID string, closed bool) flowstore.FlowRecord {
	record := progressionAdvanceFlow(flowID, repoPath, childID, epicID, flowstore.StatusCompleted)
	stamp := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	if closed {
		record.Closed = flowstore.Closure{Reason: "retired", ClosedAt: &stamp}
		return record
	}
	record.Merge = flowstore.Merge{Status: flowstore.MergeMerged, Commit: "abc1234", MergedAt: &stamp}
	return record
}

// TestEpicProgressionSelectionMatchesBeadSlotGuard pins the selection filter to
// the store's creation guard in every dimension the guard uses. A child the
// filter selects but the guard refuses is an unbounded select-create-refuse
// loop, and a child the filter suppresses but the guard would permit is a
// stalled epic. Advance only selects, so the disposition is the assertion:
// selected means the create pipeline will be handed this child, exhausted means
// the filter suppressed every ready one.
func TestEpicProgressionSelectionMatchesBeadSlotGuard(t *testing.T) {
	repo, epic := "/repo", "epic"

	for _, tt := range []struct {
		name         string
		existing     func() flowstore.FlowRecord
		wantSelected bool
	}{
		{
			name: "non-terminal flow under a different epic suppresses selection",
			existing: func() flowstore.FlowRecord {
				return progressionAdvanceFlow("other-epic", repo, "epic.a", "another-epic", flowstore.StatusPending)
			},
			wantSelected: false,
		},
		{
			name: "closed flow under a different epic still permits selection",
			existing: func() flowstore.FlowRecord {
				return progressionAdvanceTerminalFlow("closed-other-epic", repo, "epic.a", "another-epic", true)
			},
			wantSelected: true,
		},
		{
			name: "merged flow under a different epic still permits selection",
			existing: func() flowstore.FlowRecord {
				return progressionAdvanceTerminalFlow("merged-other-epic", repo, "epic.a", "another-epic", false)
			},
			wantSelected: true,
		},
		{
			name: "untrimmed stored bead id suppresses selection",
			existing: func() flowstore.FlowRecord {
				return progressionAdvanceFlow("noncanonical-child", repo, "epic.a ", "another-epic", flowstore.StatusPending)
			},
			wantSelected: false,
		},
		{
			name: "flow in a different repository does not suppress selection",
			existing: func() flowstore.FlowRecord {
				return progressionAdvanceFlow("other-repo", "/elsewhere", "epic.a", epic, flowstore.StatusPending)
			},
			wantSelected: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key := epicProgressionBaselineKey(repo, epic)
			source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
			terminal := cloneFlowRecord(source)
			terminal.Status = flowstore.StatusCompleted
			existing := tt.existing()
			m := Model{
				autoAdvanceInFlight: 1, epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "epic.a"}}, nil
				},
				listReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "epic.a", Title: "A"}}, nil
				},
				listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{existing}, nil
				},
				setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic}, nil
				},
			}
			_, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: 1})
			result := epicProgressionAdvanceMessage(t, cmd)
			if tt.wantSelected {
				if result.disposition != epicProgressionAdvanceSelected {
					t.Fatalf("disposition = %v, want selected (status=%q)", result.disposition, result.status)
				}
				if result.owned.ChildID != "epic.a" {
					t.Fatalf("selected child = %q, want epic.a", result.owned.ChildID)
				}
				return
			}
			if result.disposition != epicProgressionAdvanceExhausted {
				t.Fatalf("disposition = %v, want exhausted when every ready child is filtered (status=%q)", result.disposition, result.status)
			}
			if result.owned.ChildID != "" {
				t.Fatalf("selected child = %q, want none", result.owned.ChildID)
			}
		})
	}
}

// TestEpicProgressionBeadSlotRefusalConvergesAcrossPasses drives three passes.
// A test that only checks pass 2 cannot distinguish a fix from a longer
// livelock: a filter that keeps re-selecting an occupied child would hand the
// create pipeline the same doomed child on every tick forever. Advance itself
// no longer creates, so convergence is a property of selection alone.
func TestEpicProgressionBeadSlotRefusalConvergesAcrossPasses(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	terminal := cloneFlowRecord(source)
	terminal.Status = flowstore.StatusCompleted
	// The winner carries a different EpicID on purpose: that is the case the
	// old exact-link pre-filter missed and the guard now catches.
	winner := progressionAdvanceFlow("winner", repo, "epic.b", "another-epic", flowstore.StatusPending)

	// A pass that schedules no progression at all is termination, which is the
	// outcome this test is really after; scheduled is what distinguishes it
	// from a pass that ran and selected again.
	type pass struct {
		scheduled   bool
		disposition epicProgressionAdvanceDisposition
	}
	passes := []pass{}
	m := Model{
		epicProgressionBaselines: map[string]flowstore.FlowRecord{key: source},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true}, true, nil
		},
		listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "epic.b"}}, nil
		},
		listReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "epic.b", Title: "B"}}, nil
		},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{winner}, nil
		},
		setEpicProgression: func(flowstore.EpicProgressionUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Done: true}, nil
		},
	}
	for request := uint64(1); request <= 3; request++ {
		m.autoAdvanceInFlight = request
		next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{terminal}, Request: request})
		current := pass{}
		for _, msg := range immediateFlowRefreshMessages(cmd) {
			if result, ok := msg.(epicProgressionAdvanceResultMsg); ok {
				current = pass{scheduled: true, disposition: result.disposition}
				next, _ = updateFlowRefreshTest(next, result)
				break
			}
		}
		passes = append(passes, current)
		m = next
	}

	// Pass 1 must already terminate: the winner is in the very first snapshot,
	// so the only ready child is filtered out and none remains to select.
	if !passes[0].scheduled || passes[0].disposition != epicProgressionAdvanceExhausted {
		t.Fatalf("pass 1 = %#v, want a scheduled exhausted", passes[0])
	}
	for index, current := range passes[1:] {
		if current.scheduled && current.disposition == epicProgressionAdvanceSelected {
			t.Fatalf("pass %d = %#v, want termination rather than another selection", index+2, current)
		}
	}
}
