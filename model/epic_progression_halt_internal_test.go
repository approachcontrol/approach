package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

func epicProgressionHaltMessage(t *testing.T, cmd tea.Cmd) epicProgressionHaltResultMsg {
	t.Helper()
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if result, ok := msg.(epicProgressionHaltResultMsg); ok {
			return result
		}
	}
	t.Fatal("command returned no epic progression halt result")
	return epicProgressionHaltResultMsg{}
}

func progressionHaltClosed(record flowstore.FlowRecord) flowstore.FlowRecord {
	closedAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	record.Closed.ClosedAt = &closedAt
	record.Closed.Reason = "done with it"
	// CloseFlow persists DeriveStatus, which reports closure before anything
	// phase state could derive.
	record.Status = flowstore.StatusClosed
	return record
}

// TestEpicProgressionFailureTerminalHaltsAndDropsTracking covers AC 1 and AC 2:
// the tracked baseline's failure-terminal edge persists the halt tuple, names
// the child and its state, and drops every runtime tracking entry so no further
// child Flow can be created.
func TestEpicProgressionFailureTerminalHaltsAndDropsTracking(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	for _, tt := range []struct {
		name     string
		observed func(flowstore.FlowRecord) flowstore.FlowRecord
		want     string
	}{
		{
			name:     "blocked",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusBlocked; return r },
			want:     flowstore.StatusBlocked,
		},
		{
			name:     "needs attention",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusNeedsAttention; return r },
			want:     flowstore.StatusNeedsAttention,
		},
		{
			// No live code path assigns abandoned; the row pins the mapping, not
			// a reachable transition.
			name:     "abandoned",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusAbandoned; return r },
			want:     flowstore.StatusAbandoned,
		},
		{
			name:     "untrimmed status",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = " blocked "; return r },
			want:     flowstore.StatusBlocked,
		},
		{
			name:     "closed",
			observed: progressionHaltClosed,
			want:     flowstore.StatusClosed,
		},
		{
			// A completed child that the user then closes reads back as closed,
			// so it halts rather than advancing.
			name: "completed then closed",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord {
				r.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseCompleted}}
				return progressionHaltClosed(r)
			},
			want: flowstore.StatusClosed,
		},
		{
			name: "derived blocked phase",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord {
				r.Status = ""
				r.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseBlocked}}
				return r
			},
			want: flowstore.StatusBlocked,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
			observed := tt.observed(cloneFlowRecord(source))
			var updates []flowstore.EpicProgressionHaltUpdate
			sideEffects := 0
			m := Model{
				epicProgressionBaselines:               map[string]epicProgressionBaseline{key: progressionBaselineForTest(source)},
				epicProgressionBaselineMinimumRequests: map[string]uint64{key: 1},
				epicProgressionOwnedSuccessors: map[string]epicProgressionOwnedSuccessor{
					key: {SourceFlowID: source.FlowID, ChildID: "epic.b", FlowID: "flow-b"},
				},
				haltEpicProgression: func(update flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
					updates = append(updates, update)
					halt := update.Halt
					return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &halt}, nil
				},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					sideEffects++
					return flowstore.EpicProgression{}, false, nil
				},
				listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { sideEffects++; return nil, nil },
				listReadyBeads:    func(string) ([]beadsquery.Bead, error) { sideEffects++; return nil, nil },
				createFlow:        func(FlowStartRequest) (FlowStartResult, error) { sideEffects++; return FlowStartResult{}, nil }, autoAdvanceState: autoAdvanceState{
					autoAdvanceInFlight:   1,
					autoAdvanceRequestSeq: 1}}

			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 1})
			if !next.flowPreparationAdmission || next.flowPreparationOwner.Kind != flowPreparationEpicHalt {
				t.Fatalf("halt admission = %t, owner %#v", next.flowPreparationAdmission, next.flowPreparationOwner)
			}
			if got := next.epicProgressionBaselines[key]; got.FlowID != source.FlowID || got.Status != flowstore.StatusPending {
				t.Fatalf("halt branch refreshed the baseline: %#v", got)
			}
			result := epicProgressionHaltMessage(t, cmd)
			if result.disposition != epicProgressionHaltPersisted {
				t.Fatalf("disposition = %v, want persisted", result.disposition)
			}
			if sideEffects != 0 {
				t.Fatalf("halt performed %d Beads/Flow/progression-read side effects", sideEffects)
			}
			wantHalt := flowstore.EpicProgressionHalt{
				ChildBeadID: "epic.a",
				Status:      tt.want,
				Message:     "child Flow flow-a halted auto-progression",
			}
			wantActivation := progressionBaselineForTest(source).Activation
			if len(updates) != 1 || updates[0].Key != (flowstore.EpicProgressionKey{RepoPath: repo, EpicID: epic}) || updates[0].Halt != wantHalt || !updates[0].ExpectedActivation.Equal(wantActivation) {
				t.Fatalf("halt updates = %#v, want one %#v", updates, wantHalt)
			}

			next, _ = updateFlowRefreshTest(next, result)
			if _, present := next.epicProgressionBaselines[key]; present {
				t.Fatalf("halt retained baseline: %#v", next.epicProgressionBaselines)
			}
			if _, present := next.epicProgressionBaselineMinimumRequests[key]; present {
				t.Fatal("halt retained the baseline minimum request marker")
			}
			if _, present := next.epicProgressionOwnedSuccessors[key]; present {
				t.Fatal("halt retained the owned successor")
			}
			if next.flowPreparationAdmission {
				t.Fatal("halt result retained preparation admission")
			}
			wantStatus := "Auto-progression halted for epic epic: child epic.a is " + tt.want
			if next.status.Text != wantStatus {
				t.Fatalf("status = %q, want %q", next.status.Text, wantStatus)
			}

			next.autoAdvanceInFlight = 2
			_, secondCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 2})
			for _, msg := range immediateFlowRefreshMessages(secondCmd) {
				if _, ok := msg.(epicProgressionHaltResultMsg); ok {
					t.Fatal("a later poll halted the same untracked child again")
				}
			}
			if len(updates) != 1 {
				t.Fatalf("halt updates after untracked poll = %#v", updates)
			}
		})
	}
}

// TestEpicProgressionSuccessAndNonTerminalObservationsDoNotHalt keeps the halt
// edge disjoint from the advance edge and from the ordinary baseline refresh.
func TestEpicProgressionSuccessAndNonTerminalObservationsDoNotHalt(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	for _, tt := range []struct {
		name        string
		observed    func(flowstore.FlowRecord) flowstore.FlowRecord
		wantAdvance bool
	}{
		{
			name:        "completed",
			observed:    func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusCompleted; return r },
			wantAdvance: true,
		},
		{
			name:        "merged",
			observed:    func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusMerged; return r },
			wantAdvance: true,
		},
		{
			// Skipping a phase is a deliberate user choice that still leaves the
			// Flow completed, so an all-skipped child advances rather than halts.
			name: "all phases skipped",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord {
				r.Status = ""
				r.Phases = []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseSkipped}}
				return r
			},
			wantAdvance: true,
		},
		{
			name:     "pending",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Title = "refreshed"; return r },
		},
		{
			name:     "in progress",
			observed: func(r flowstore.FlowRecord) flowstore.FlowRecord { r.Status = flowstore.StatusInProgress; return r },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
			observed := tt.observed(cloneFlowRecord(source))
			m := Model{
				epicProgressionBaselines: map[string]epicProgressionBaseline{key: progressionBaselineForTest(source)},
				haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
					t.Fatal("non-failure observation halted progression")
					return flowstore.EpicProgression{}, nil
				},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return flowstore.EpicProgression{}, false, nil
				}, autoAdvanceState: autoAdvanceState{
					autoAdvanceInFlight: 1}}
			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 1})
			advanced := false
			for _, msg := range immediateFlowRefreshMessages(cmd) {
				switch msg.(type) {
				case epicProgressionAdvanceResultMsg:
					advanced = true
				case epicProgressionHaltResultMsg:
					t.Fatal("non-failure observation scheduled a halt")
				}
			}
			if advanced != tt.wantAdvance {
				t.Fatalf("advance scheduled = %t, want %t", advanced, tt.wantAdvance)
			}
			if tt.wantAdvance {
				return
			}
			if got := next.epicProgressionBaselines[key]; got.Status != observed.Status || got.Title != observed.Title {
				t.Fatalf("non-terminal observation did not refresh the baseline: %#v", got)
			}
		})
	}
}

// TestEpicProgressionHaltPersistenceFailureUsesAuthoritativeState covers
// Design 5: a still-active epic keeps its baseline so the edge refires, and an
// already-inactive epic drops it.
func TestEpicProgressionHaltPersistenceFailureUsesAuthoritativeState(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	for _, tt := range []struct {
		name            string
		progression     flowstore.EpicProgression
		found           bool
		readErr         error
		wantDisposition epicProgressionHaltDisposition
		wantBaseline    bool
	}{
		{
			name:            "still active",
			progression:     flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true},
			found:           true,
			wantDisposition: epicProgressionHaltRetryable,
			wantBaseline:    true,
		},
		{
			name:            "new activation",
			progression:     flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Enabled: true, UpdatedAt: time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)},
			found:           true,
			wantDisposition: epicProgressionHaltInactive,
		},
		{
			name:            "unreadable",
			readErr:         errors.New("corrupt row"),
			wantDisposition: epicProgressionHaltRetryable,
			wantBaseline:    true,
		},
		{
			name:            "absent",
			wantDisposition: epicProgressionHaltInactive,
		},
		{
			// A halted row is a halt whatever the write reported, so this
			// announces the retained cause rather than the inactive line. The
			// dedicated durable-halt test pins that message.
			name:            "already halted",
			found:           true,
			progression:     flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &flowstore.EpicProgressionHalt{ChildBeadID: "epic.a", Status: flowstore.StatusBlocked, Message: "halted"}},
			wantDisposition: epicProgressionHaltPersisted,
		},
		{
			name:            "done",
			found:           true,
			progression:     flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Done: true},
			wantDisposition: epicProgressionHaltInactive,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
			baseline := progressionBaselineForTest(source)
			if tt.name == "still active" {
				tt.progression.UpdatedAt = baseline.Activation
			}
			observed := cloneFlowRecord(source)
			observed.Status = flowstore.StatusBlocked
			halts := 0
			m := Model{
				epicProgressionBaselines: map[string]epicProgressionBaseline{key: baseline},
				haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
					halts++
					return flowstore.EpicProgression{}, errors.New("write failed")
				},
				readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
					return tt.progression, tt.found, tt.readErr
				}, autoAdvanceState: autoAdvanceState{
					autoAdvanceInFlight: 1}}
			next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 1})
			result := epicProgressionHaltMessage(t, cmd)
			if result.disposition != tt.wantDisposition {
				t.Fatalf("disposition = %v, want %v", result.disposition, tt.wantDisposition)
			}
			next, _ = updateFlowRefreshTest(next, result)
			if _, present := next.epicProgressionBaselines[key]; present != tt.wantBaseline {
				t.Fatalf("baseline present = %t, want %t", present, tt.wantBaseline)
			}
			if next.flowPreparationAdmission {
				t.Fatal("failed halt retained preparation admission")
			}
			if !tt.wantBaseline {
				return
			}
			next.autoAdvanceInFlight = 2
			_, retryCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 2})
			if retry := epicProgressionHaltMessage(t, retryCmd); retry.request == 0 {
				t.Fatal("retained baseline did not refire the halt edge")
			}
			if halts != 2 {
				t.Fatalf("halt attempts = %d, want a refire", halts)
			}
		})
	}
}

func TestEpicProgressionHaltActivationMismatchStopsWithoutAdoptingFreshState(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	baseline := progressionBaselineForTest(source)
	reads := 0
	m := Model{
		epicProgressionBaselines: map[string]epicProgressionBaseline{key: baseline},
		haltEpicProgression: func(update flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
			if !update.ExpectedActivation.Equal(baseline.Activation) {
				t.Fatalf("expected activation = %s, want %s", update.ExpectedActivation, baseline.Activation)
			}
			return flowstore.EpicProgression{}, flowstore.ErrEpicProgressionActivationChanged
		},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			reads++
			return flowstore.EpicProgression{Enabled: true, UpdatedAt: baseline.Activation.Add(time.Minute)}, true, nil
		},
	}
	request := epicProgressionHaltRequest{Request: 1, EpicKey: key, SourceFlowID: source.FlowID}
	m.activeEpicProgressionHalt = request
	m.flowPreparationAdmission = true
	msg := epicProgressionHaltMessage(t, m.haltEpicProgressionCauseCmd(request, repo, epic, baseline.Activation, flowstore.EpicProgressionHalt{
		ChildBeadID: "epic.a", Status: flowstore.StatusBlocked, Message: "blocked",
	}))
	if msg.disposition != epicProgressionHaltInactive || reads != 0 {
		t.Fatalf("stale halt = disposition %v, reads %d; want inactive without readback", msg.disposition, reads)
	}
	next, _ := updateFlowRefreshTest(m, msg)
	if _, tracked := next.epicProgressionBaselines[key]; tracked {
		t.Fatalf("stale halt retained old baseline: %#v", next.epicProgressionBaselines)
	}
}

// TestEpicProgressionHaltSharesPreparationAdmissionAndRotates covers Design 4:
// the halt cannot interleave with an in-flight advance, and a halt that keeps
// failing cannot starve the other tracked epics.
func TestEpicProgressionHaltSharesPreparationAdmissionAndRotates(t *testing.T) {
	repo := "/repo"
	firstEpic, secondEpic := "epic-a", "epic-b"
	firstKey := epicProgressionBaselineKey(repo, firstEpic)
	secondKey := epicProgressionBaselineKey(repo, secondEpic)
	firstSource := progressionAdvanceFlow("flow-a", repo, "epic-a.1", firstEpic, flowstore.StatusPending)
	secondSource := progressionAdvanceFlow("flow-b", repo, "epic-b.1", secondEpic, flowstore.StatusPending)
	firstBlocked := cloneFlowRecord(firstSource)
	firstBlocked.Status = flowstore.StatusBlocked
	secondBlocked := cloneFlowRecord(secondSource)
	secondBlocked.Status = flowstore.StatusBlocked
	blocked := []flowstore.FlowRecord{firstBlocked, secondBlocked}

	t.Run("held admission defers without losing the edge", func(t *testing.T) {
		m := Model{
			epicProgressionBaselines: map[string]epicProgressionBaseline{firstKey: progressionBaselineForTest(firstSource)},
			flowPreparationAdmission: true,
			flowPreparationSeq:       7,
			flowPreparationOwner:     flowPreparationOwner{Kind: flowPreparationEpicAdvance, Token: 7},
			haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
				return flowstore.EpicProgression{RepoPath: repo, EpicID: firstEpic}, nil
			}, autoAdvanceState: autoAdvanceState{
				autoAdvanceInFlight: 1}}
		next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{firstBlocked}, Request: 1})
		for _, msg := range immediateFlowRefreshMessages(cmd) {
			if _, ok := msg.(epicProgressionHaltResultMsg); ok {
				t.Fatal("halt ran while another preparation held admission")
			}
		}
		if got := next.epicProgressionBaselines[firstKey]; got.FlowID != firstSource.FlowID {
			t.Fatalf("deferred halt lost the baseline: %#v", next.epicProgressionBaselines)
		}
		next = next.releaseFlowPreparation(flowPreparationEpicAdvance, 7)
		next.autoAdvanceInFlight = 2
		_, retryCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{firstBlocked}, Request: 2})
		if retry := epicProgressionHaltMessage(t, retryCmd); retry.epicKey != firstKey {
			t.Fatalf("deferred halt did not refire: %#v", retry)
		}
	})

	t.Run("failing halt rotates to the next tracked epic", func(t *testing.T) {
		m := Model{
			epicProgressionBaselines: map[string]epicProgressionBaseline{
				firstKey: progressionBaselineForTest(firstSource), secondKey: progressionBaselineForTest(secondSource),
			},
			haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
				return flowstore.EpicProgression{}, errors.New("write failed")
			},
			readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
				return flowstore.EpicProgression{RepoPath: key.RepoPath, EpicID: key.EpicID, Enabled: true}, true, nil
			}, autoAdvanceState: autoAdvanceState{
				autoAdvanceInFlight: 1}}
		next, firstCmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: blocked, Request: 1})
		first := epicProgressionHaltMessage(t, firstCmd)
		if first.epicKey != firstKey {
			t.Fatalf("first halt key = %q, want %q", first.epicKey, firstKey)
		}
		next, _ = updateFlowRefreshTest(next, first)
		next.autoAdvanceInFlight = 2
		_, secondCmd := updateFlowRefreshTest(next, AutoAdvanceResultMsg{Flows: blocked, Request: 2})
		second := epicProgressionHaltMessage(t, secondCmd)
		if second.epicKey != secondKey {
			t.Fatalf("second halt key = %q, want rotation to %q", second.epicKey, secondKey)
		}
	})
}

// TestEpicProgressionStaleHaltResultIsFencedAndReleasesAdmission pins the
// four-part staleness guard and the unconditional admission release.
func TestEpicProgressionStaleHaltResultIsFencedAndReleasesAdmission(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	blocked := cloneFlowRecord(source)
	blocked.Status = flowstore.StatusBlocked
	m := Model{
		epicProgressionBaselines: map[string]epicProgressionBaseline{key: progressionBaselineForTest(source)},
		haltEpicProgression: func(update flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
			halt := update.Halt
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &halt}, nil
		}, autoAdvanceState: autoAdvanceState{
			autoAdvanceInFlight: 1}}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{blocked}, Request: 1})
	result := epicProgressionHaltMessage(t, cmd)
	if result.ownerToken == 0 {
		t.Fatal("halt acquired a zero preparation token")
	}
	stale := result
	stale.sourceFlowID = "flow-other"

	fenced, _ := updateFlowRefreshTest(next, stale)
	if got := fenced.epicProgressionBaselines[key]; got.FlowID != source.FlowID {
		t.Fatalf("stale halt result dropped the baseline: %#v", fenced.epicProgressionBaselines)
	}
	if fenced.status.Text != "" {
		t.Fatalf("stale halt result announced %q", fenced.status.Text)
	}
	if fenced.flowPreparationAdmission {
		t.Fatal("stale halt result retained preparation admission")
	}
}

// TestBeadProgressionReadOwnsHaltDetail pins Design 7: the authoritative read
// is the only writer of the halt line, and it clears the line whenever the
// record it read is not halted.
func TestBeadProgressionReadOwnsHaltDetail(t *testing.T) {
	index, _ := beadSubviewIndex(ui.ModeBeadsOpen)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	base := Model{
		topMode: ui.ModeBeadsOpen,
		beads:   newBeadSubviews(),
		beadExpansion: beadExpansionSnapshot{
			target:     target,
			projection: ui.BeadExpansion{EpicID: "epic", State: ui.BeadExpansionLoaded, ReadinessKnown: true},
		},
	}
	base.beads[index].available = true
	base.beads[index].repoPath = "/repo"
	base.beads[index].pane = base.beads[index].pane.SetItems([]beadsquery.Bead{{ID: "epic", IssueType: "epic"}})

	halted := flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Halt: &flowstore.EpicProgressionHalt{
		ChildBeadID: "epic.1", Status: flowstore.StatusBlocked, Message: "child Flow flow-a halted auto-progression",
	}}
	next := base.handleBeadProgressionResult(beadProgressionResultMsg{target: target, progression: halted, found: true})
	if got := next.beadExpansion.projection; got.ProgressionHaltDetail != "epic.1 is blocked" || got.ProgressionEnabled || !got.ProgressionKnown {
		t.Fatalf("halted projection = %#v", got)
	}

	failed := next.handleBeadProgressionResult(beadProgressionResultMsg{target: target, err: errors.New("read failed")})
	if failed.beadExpansion.projection.ProgressionHaltDetail != "" {
		t.Fatalf("read failure left a stale halt line: %#v", failed.beadExpansion.projection)
	}

	active := flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Enabled: true}
	enabled := next.handleBeadProgressionResult(beadProgressionResultMsg{target: target, progression: active, found: true})
	if got := enabled.beadExpansion.projection; got.ProgressionHaltDetail != "" || !got.ProgressionEnabled {
		t.Fatalf("re-enabled projection = %#v", got)
	}
}

// TestEpicProgressionToggleReconcilesHaltDetail covers the recovery frame: an
// enable failure never claims the epic is un-halted, and an enable success
// clears the halt line in the same frame that sets enabled.
func TestEpicProgressionToggleReconcilesHaltDetail(t *testing.T) {
	index, _ := beadSubviewIndex(ui.ModeBeadsOpen)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	base := Model{
		topMode: ui.ModeBeadsOpen,
		beads:   newBeadSubviews(),
		beadExpansion: beadExpansionSnapshot{
			target: target,
			projection: ui.BeadExpansion{
				EpicID: "epic", State: ui.BeadExpansionLoaded, ReadinessKnown: true,
				ProgressionKnown: true, ProgressionHaltDetail: "epic.1 is blocked",
			},
		},
		readEpicProgression: func(key flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			return flowstore.EpicProgression{RepoPath: key.RepoPath, EpicID: key.EpicID}, true, nil
		},
	}
	base.beads[index].available = true
	base.beads[index].repoPath = "/repo"
	base.beads[index].pane = base.beads[index].pane.SetItems([]beadsquery.Bead{{ID: "epic", IssueType: "epic"}})

	refused, cmd := base.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
		status: "Flow flow-a is blocked; auto-progression remains off",
	})
	if got := refused.beadExpansion.projection; got.ProgressionHaltDetail != "epic.1 is blocked" || got.ProgressionEnabled {
		t.Fatalf("refused enable rewrote the halt line: %#v", got)
	}
	reread := false
	for _, msg := range immediateFlowRefreshMessages(cmd) {
		if _, ok := msg.(beadProgressionResultMsg); ok {
			reread = true
		}
	}
	if !reread {
		t.Fatal("refused enable did not schedule the authoritative progression re-read")
	}

	recovered, _ := base.handleEpicProgressionToggleResult(epicProgressionToggleResultMsg{
		target: target, known: true, enabled: true, baselineDisposition: epicProgressionBaselineReplace,
		status: "Enabled auto-progression for epic epic",
	})
	if got := recovered.beadExpansion.projection; got.ProgressionHaltDetail != "" || !got.ProgressionEnabled {
		t.Fatalf("enable success rendered a halted, enabled epic: %#v", got)
	}
}

// TestEnableEpicProgressionAfterHaltResumesFromTheNextReadyChild covers AC 3:
// re-enabling needs no new code, but it is refused while the failed child's
// Flow is still open and resumes from the next ready child once it is resolved.
func TestEnableEpicProgressionAfterHaltResumesFromTheNextReadyChild(t *testing.T) {
	stamp := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	target := beadExpansionTarget{token: 1, repoPath: "/repo", mode: ui.ModeBeadsOpen, epicID: "epic"}
	blocked := flowstore.FlowRecord{
		FlowID: "flow-a", RepoPath: "/repo", Status: flowstore.StatusBlocked, PreparedAt: &stamp,
		Bead: flowstore.BeadLink{ID: "epic.1", EpicID: "epic"}, ProgressionClaim: true,
	}
	projection := ui.BeadExpansion{
		Children: []beadsquery.Bead{{ID: "epic.1", Title: "First"}, {ID: "epic.2", Title: "Second"}},
		ReadyIDs: map[string]bool{"epic.2": true},
	}
	children := []beadsquery.Bead{{ID: "epic.1", Title: "First"}, {ID: "epic.2", Title: "Second"}}

	t.Run("refuses while the failed child stays open", func(t *testing.T) {
		m := Model{
			listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
				return []flowstore.FlowRecord{blocked}, nil
			},
			listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return children, nil },
			listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.2"}}, nil },
			claimBead:         func(string, string) error { return nil },
			reserveFlowPreparation: func(string) (flowstore.FlowRecord, func(), error) {
				return blocked, func() {}, nil
			},
			createFlow: func(FlowStartRequest) (FlowStartResult, error) {
				t.Fatal("refused re-enable prepared a Flow")
				return FlowStartResult{}, nil
			},
		}
		msg, ok := m.enableEpicProgressionCmd(target, projection)().(epicProgressionToggleResultMsg)
		if !ok || msg.enabled || !strings.Contains(msg.status, "Flow flow-a is blocked; auto-progression remains off") {
			t.Fatalf("re-enable while blocked = %#v", msg)
		}
	})

	t.Run("resumes from the next ready child once resolved", func(t *testing.T) {
		closed := blocked
		closed.Closed.ClosedAt = &stamp
		closed.Closed.Reason = "resolved by hand"
		closed.Status = flowstore.StatusClosed
		successor := flowstore.FlowRecord{
			FlowID: "flow-b", RepoPath: "/repo", Status: flowstore.StatusPending, PreparedAt: &stamp,
			Bead: flowstore.BeadLink{ID: "epic.2", EpicID: "epic"},
		}
		var prepared []flowstore.BeadLink
		m := Model{
			listFlows:         func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return []flowstore.FlowRecord{closed}, nil },
			listChildrenBeads: func(string, string) ([]beadsquery.Bead, error) { return children, nil },
			listReadyBeads:    func(string) ([]beadsquery.Bead, error) { return []beadsquery.Bead{{ID: "epic.2"}}, nil },
			claimBead:         func(string, string) error { return nil },
			createFlow: func(request FlowStartRequest) (FlowStartResult, error) {
				prepared = append(prepared, request.Bead)
				if request.AfterFlowPersisted != nil {
					if err := request.AfterFlowPersisted(); err != nil {
						return FlowStartResult{}, err
					}
				}
				return FlowStartResult{Flow: successor}, nil
			},
			reserveFlowPreparation: func(string) (flowstore.FlowRecord, func(), error) {
				return successor, func() {}, nil
			},
			enableEpicProgression: func(update flowstore.PreparedEpicProgressionUpdate) (flowstore.EpicProgression, flowstore.FlowRecord, error) {
				return flowstore.EpicProgression{RepoPath: "/repo", EpicID: "epic", Enabled: true}, successor, nil
			},
		}
		msg, ok := m.enableEpicProgressionCmd(target, projection)().(epicProgressionToggleResultMsg)
		if !ok || !msg.enabled || msg.baselineDisposition != epicProgressionBaselineReplace {
			t.Fatalf("re-enable after resolution = %#v", msg)
		}
		if len(prepared) != 1 || prepared[0] != (flowstore.BeadLink{ID: "epic.2", EpicID: "epic"}) {
			t.Fatalf("prepared child links = %#v, want the next ready child", prepared)
		}
	})
}

// TestEpicProgressionHaltAnnouncesTheRetainedCause pins the first-cause
// guarantee at the surface: HaltEpicProgression is sticky, so when another
// process already halted the epic for a different child, the notification must
// report the tuple the store retained rather than this attempt's observation.
func TestEpicProgressionHaltAnnouncesTheRetainedCause(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	blocked := cloneFlowRecord(source)
	blocked.Status = flowstore.StatusBlocked
	retained := flowstore.EpicProgressionHalt{
		ChildBeadID: "epic.z",
		Status:      flowstore.StatusClosed,
		Message:     "child Flow flow-z halted auto-progression",
	}
	m := Model{
		epicProgressionBaselines: map[string]epicProgressionBaseline{key: progressionBaselineForTest(source)},
		haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
			first := retained
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &first}, nil
		}, autoAdvanceState: autoAdvanceState{
			autoAdvanceInFlight: 1}}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{blocked}, Request: 1})
	result := epicProgressionHaltMessage(t, cmd)
	if result.disposition != epicProgressionHaltPersisted {
		t.Fatalf("disposition = %v, want persisted", result.disposition)
	}
	next, _ = updateFlowRefreshTest(next, result)
	want := "Auto-progression halted for epic epic: child epic.z is " + flowstore.StatusClosed
	if next.status.Text != want {
		t.Fatalf("status = %q, want %q", next.status.Text, want)
	}
}

// TestEpicProgressionHaltWriteErrorReportsADurableHalt covers the write that
// reports an error after the row became durable. The authoritative read is the
// tiebreaker, and a halted row must announce its retained cause rather than the
// generic inactive line, exactly as the successful write does.
func TestEpicProgressionHaltWriteErrorReportsADurableHalt(t *testing.T) {
	repo, epic := "/repo", "epic"
	key := epicProgressionBaselineKey(repo, epic)
	source := progressionAdvanceFlow("flow-a", repo, "epic.a", epic, flowstore.StatusPending)
	observed := cloneFlowRecord(source)
	observed.Status = flowstore.StatusBlocked
	durable := flowstore.EpicProgressionHalt{
		ChildBeadID: "epic.z",
		Status:      flowstore.StatusClosed,
		Message:     "child Flow flow-z halted auto-progression",
	}
	m := Model{
		epicProgressionBaselines: map[string]epicProgressionBaseline{key: progressionBaselineForTest(source)},
		haltEpicProgression: func(flowstore.EpicProgressionHaltUpdate) (flowstore.EpicProgression, error) {
			return flowstore.EpicProgression{}, errors.New("commit failed")
		},
		readEpicProgression: func(flowstore.EpicProgressionKey) (flowstore.EpicProgression, bool, error) {
			halt := durable
			return flowstore.EpicProgression{RepoPath: repo, EpicID: epic, Halt: &halt}, true, nil
		}, autoAdvanceState: autoAdvanceState{
			autoAdvanceInFlight: 1}}
	next, cmd := updateFlowRefreshTest(m, AutoAdvanceResultMsg{Flows: []flowstore.FlowRecord{observed}, Request: 1})
	result := epicProgressionHaltMessage(t, cmd)
	if result.disposition != epicProgressionHaltPersisted {
		t.Fatalf("disposition = %v, want persisted", result.disposition)
	}
	next, _ = updateFlowRefreshTest(next, result)
	if _, present := next.epicProgressionBaselines[key]; present {
		t.Fatalf("durable halt retained the baseline: %#v", next.epicProgressionBaselines)
	}
	want := "Auto-progression halted for epic epic: child epic.z is " + flowstore.StatusClosed
	if next.status.Text != want {
		t.Fatalf("status = %q, want %q", next.status.Text, want)
	}
}
