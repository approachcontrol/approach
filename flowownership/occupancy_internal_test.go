package flowownership

import (
	"errors"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

type flowCacheFixture struct {
	record flowstore.FlowRecord
	found  bool
	calls  int
}

func (cache *flowCacheFixture) CachedFlow(flowID string) (flowstore.FlowRecord, bool) {
	cache.calls++
	if cache.record.FlowID != flowID {
		return flowstore.FlowRecord{}, false
	}
	return cache.record, cache.found
}

func TestAdvisoryCachedRunningPhaseDefers(t *testing.T) {
	cache := &flowCacheFixture{
		found: true,
		record: flowstore.FlowRecord{
			FlowID: "flow-1",
			Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Status: flowstore.PhaseRunning}},
		},
	}
	advice := New(Sources{
		FlowCache: cache,
		Lease:     &leaseFixture{t: t, wantFlowID: "flow-1"},
		Runtime:   runtimeFixture{},
	}).Advise(Query{
		FlowID: "flow-1",
		Purpose: Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: StageDrain,
		},
	})
	if !advice.Defer() {
		t.Fatal("Defer() = false, want cached running phase to defer")
	}
	if advice.verdict.Holder() != HolderRunningPhase || advice.verdict.PhaseID() != "implementation" {
		t.Fatalf("advice = (%v, %q), want running phase implementation", advice.verdict.Holder(), advice.verdict.PhaseID())
	}
	if advice.verdict.Err() != nil {
		t.Fatalf("Err() = %v, want nil", advice.verdict.Err())
	}
	if cache.calls != 1 {
		t.Fatalf("CachedFlow calls = %d, want 1", cache.calls)
	}
}

func TestDurableUntrackedOwnerDefersCachedAndAuthoritativeAdmissions(t *testing.T) {
	record := flowstore.FlowRecord{FlowID: "flow-1", UntrackedOwner: &flowstore.UntrackedOwner{LaunchID: "launch-1", Role: flowstore.UntrackedOwnerAutofix, State: flowstore.UntrackedOwnerLive}}
	cache := &flowCacheFixture{found: true, record: record}
	advice := New(Sources{FlowCache: cache, Cache: &sessionCacheFixture{wantFlowID: "flow-1"}, Lease: &leaseFixture{t: t, wantFlowID: "flow-1"}, Runtime: runtimeFixture{}}).Advise(Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleWorktreeAgent, Stage: StageFooter}})
	if advice.Holder() != HolderUntrackedOwner {
		t.Fatalf("cached holder=%v", advice.Holder())
	}
	reader := &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record}
	verdict := New(Sources{Flows: reader}).Query(Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageAuthoritative}, PhaseID: "implementation"})
	if verdict.Holder() != HolderUntrackedOwner {
		t.Fatalf("authoritative holder=%v err=%v", verdict.Holder(), verdict.Err())
	}
}

type sessionCacheFixture struct {
	records    []sessions.SessionRecord
	wantFlowID string
	calls      int
}

func (cache *sessionCacheFixture) ActiveFlowSessions(flowID string) []sessions.SessionRecord {
	cache.calls++
	if flowID != cache.wantFlowID {
		panic("unexpected Flow ID")
	}
	return cache.records
}

type panicFlowReader struct{}

func (panicFlowReader) ReadFlow(string) (flowstore.FlowRecord, error) {
	panic("advisory query reached Flow store")
}

type panicSessionStore struct{}

func (panicSessionStore) ListFlowSessions(string) ([]sessions.SessionRecord, error) {
	panic("advisory query reached session store")
}

type flowReaderFixture struct {
	t          *testing.T
	wantFlowID string
	record     flowstore.FlowRecord
	err        error
	calls      int
}

func (reader *flowReaderFixture) ReadFlow(flowID string) (flowstore.FlowRecord, error) {
	reader.calls++
	if reader.wantFlowID != "" && flowID != reader.wantFlowID {
		reader.t.Fatalf("ReadFlow() flow ID = %q, want %q", flowID, reader.wantFlowID)
	}
	return reader.record, reader.err
}

type sessionStoreFixture struct {
	t          *testing.T
	wantFlowID string
	records    []sessions.SessionRecord
	err        error
	calls      int
}

func (store *sessionStoreFixture) ListFlowSessions(flowID string) ([]sessions.SessionRecord, error) {
	store.calls++
	if store.wantFlowID != "" && flowID != store.wantFlowID {
		store.t.Fatalf("ListFlowSessions() flow ID = %q, want %q", flowID, store.wantFlowID)
	}
	return store.records, store.err
}

func TestTrackedPhaseAuthoritativeQueryFindsMirroredPhaseSession(t *testing.T) {
	flows := &flowReaderFixture{
		t:          t,
		wantFlowID: "flow-exact",
		record: flowstore.FlowRecord{
			FlowID: "flow-exact",
			Phases: []flowstore.FlowPhase{{
				PhaseID:   "implementation",
				LaunchIDs: []string{"launch-1"},
				Sessions: []flowstore.Session{{
					SessionID: "session-1",
					LaunchID:  "launch-1",
					Status:    "running",
				}},
			}},
		},
	}
	store := &sessionStoreFixture{t: t, wantFlowID: "flow-exact"}

	verdict := New(Sources{Flows: flows, Sessions: store}).Query(Query{
		FlowID: " flow-exact ",
		Purpose: Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: StageAuthoritative,
		},
		PhaseID: "implementation",
	})

	if verdict.Holder() != HolderPhaseSession || verdict.PhaseID() != "implementation" || verdict.Err() != nil {
		t.Fatalf("verdict = (%v, %q, %v), want phase session on implementation", verdict.Holder(), verdict.PhaseID(), verdict.Err())
	}
	if flows.calls != 1 || store.calls != 1 {
		t.Fatalf("source calls = (Flow %d, sessions %d), want (1, 1)", flows.calls, store.calls)
	}
}

func TestAuthoritativePhaseSessionEvaluation(t *testing.T) {
	ended := time.Now()
	base := flowstore.FlowRecord{
		FlowID: "flow-1",
		Phases: []flowstore.FlowPhase{{
			PhaseID:   "implementation",
			Status:    flowstore.PhaseReady,
			LaunchIDs: []string{" launch-1 "},
		}},
	}
	tests := []struct {
		name       string
		mutateFlow func(*flowstore.FlowRecord)
		records    []sessions.SessionRecord
		wantHolder Holder
	}{
		{
			name: "store-only live session",
			records: []sessions.SessionRecord{{
				FlowID: "flow-1", SessionID: "session-1", LaunchID: "launch-1", Status: "last_seen",
			}},
			wantHolder: HolderPhaseSession,
		},
		{
			name: "prefix-like Flow ID does not match",
			records: []sessions.SessionRecord{{
				FlowID: "flow-10", SessionID: "session-1", LaunchID: "launch-1", Status: "running",
			}},
		},
		{
			name: "another launch does not match",
			records: []sessions.SessionRecord{{
				FlowID: "flow-1", SessionID: "session-1", LaunchID: "launch-other", Status: "running",
			}},
		},
		{
			name: "ended session does not match",
			records: []sessions.SessionRecord{{
				FlowID: "flow-1", SessionID: "session-1", LaunchID: "launch-1", Status: "ended", EndedAt: ended,
			}},
		},
		{
			name: "mirror and store union",
			mutateFlow: func(record *flowstore.FlowRecord) {
				record.Phases[0].Sessions = []flowstore.Session{{
					SessionID: "session-1", LaunchID: "launch-1", Status: "running",
				}}
			},
			records: []sessions.SessionRecord{{
				FlowID: "flow-1", SessionID: "session-2", LaunchID: "launch-other", Status: "running",
			}},
			wantHolder: HolderPhaseSession,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			record := base
			record.Phases = append([]flowstore.FlowPhase(nil), base.Phases...)
			if tc.mutateFlow != nil {
				tc.mutateFlow(&record)
			}
			flows := &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record}
			store := &sessionStoreFixture{t: t, wantFlowID: "flow-1", records: tc.records}
			verdict := Evaluate(Sources{Flows: flows, Sessions: store}, Query{
				FlowID: " flow-1 ",
				Purpose: Purpose{
					Role:  actions.RoleTrackedPhase,
					Stage: StageAuthoritative,
				},
				PhaseID: "implementation",
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
			wantPhase := ""
			if tc.wantHolder == HolderPhaseSession {
				wantPhase = "implementation"
			}
			if verdict.PhaseID() != wantPhase || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %q, %v)", verdict.Holder(), verdict.PhaseID(), verdict.Err())
			}
			if flows.calls != 1 || store.calls != 1 {
				t.Fatalf("source calls = (Flow %d, sessions %d), want (1, 1)", flows.calls, store.calls)
			}
		})
	}
}

func TestRepairAuthoritativeOccupancy(t *testing.T) {
	tests := []struct {
		name       string
		running    bool
		live       bool
		wantHolder Holder
		wantPhase  string
	}{
		{name: "running phase is not occupancy"},
		{name: "live phase session", live: true, wantHolder: HolderPhaseSession, wantPhase: "review"},
		{name: "live phase session wins over running status", running: true, live: true, wantHolder: HolderPhaseSession, wantPhase: "review"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runningStatus := flowstore.PhaseReady
			if tc.running {
				runningStatus = flowstore.PhaseRunning
			}
			review := flowstore.FlowPhase{
				PhaseID:   "review",
				Status:    flowstore.PhaseReady,
				LaunchIDs: []string{"launch-review"},
			}
			if tc.live {
				review.Sessions = []flowstore.Session{{
					SessionID: "session-review", LaunchID: "launch-review", Status: "running",
				}}
			}
			record := flowstore.FlowRecord{
				FlowID: "flow-1",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Status: runningStatus},
					review,
				},
			}
			verdict := Evaluate(Sources{
				Flows:    &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record},
				Sessions: &sessionStoreFixture{t: t, wantFlowID: "flow-1"},
			}, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleRepair, Stage: StageAuthoritative},
			})
			if verdict.Holder() != tc.wantHolder || verdict.PhaseID() != tc.wantPhase || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %q, %v), want (%v, %q, nil)", verdict.Holder(), verdict.PhaseID(), verdict.Err(), tc.wantHolder, tc.wantPhase)
			}
		})
	}
}

func TestAutofixAuthoritativeOccupancy(t *testing.T) {
	tests := []struct {
		name       string
		running    bool
		live       bool
		terminal   bool
		wantHolder Holder
		wantPhase  string
	}{
		{name: "running phase is not occupancy"},
		{name: "live phase session", live: true, wantHolder: HolderPhaseSession, wantPhase: "review"},
		{name: "live phase session wins over running status", running: true, live: true, wantHolder: HolderPhaseSession, wantPhase: "review"},
		{name: "terminal phase session is not occupancy", live: true, terminal: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runningStatus := flowstore.PhaseReady
			if tc.running {
				runningStatus = flowstore.PhaseRunning
			}
			reviewStatus := flowstore.PhaseReady
			if tc.terminal {
				reviewStatus = flowstore.PhaseCompleted
			}
			review := flowstore.FlowPhase{
				PhaseID:   "review",
				Status:    reviewStatus,
				LaunchIDs: []string{"launch-review"},
			}
			if tc.live {
				review.Sessions = []flowstore.Session{{
					SessionID: "session-review", LaunchID: "launch-review", Status: "running",
				}}
			}
			record := flowstore.FlowRecord{
				FlowID: "flow-1",
				Phases: []flowstore.FlowPhase{
					{PhaseID: "implementation", Status: runningStatus},
					review,
				},
			}
			verdict := Evaluate(Sources{
				Flows:    &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record},
				Sessions: &sessionStoreFixture{t: t, wantFlowID: "flow-1"},
			}, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleAutofix, Stage: StageAuthoritative},
			})
			if verdict.Holder() != tc.wantHolder || verdict.PhaseID() != tc.wantPhase || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %q, %v), want (%v, %q, nil)", verdict.Holder(), verdict.PhaseID(), verdict.Err(), tc.wantHolder, tc.wantPhase)
			}
		})
	}
}

func TestWorktreeAgentAuthoritativeOccupancy(t *testing.T) {
	tests := []struct {
		name        string
		phaseStatus flowstore.PhaseStatus
		phaseLive   bool
		stored      []sessions.SessionRecord
		wantHolder  Holder
		wantPhase   string
	}{
		{name: "running phase", phaseStatus: flowstore.PhaseRunning, wantHolder: HolderRunningPhase, wantPhase: "implementation"},
		{name: "live phase session", phaseStatus: flowstore.PhaseReady, phaseLive: true, wantHolder: HolderPhaseSession, wantPhase: "implementation"},
		{name: "live phase session wins within running phase", phaseStatus: flowstore.PhaseRunning, phaseLive: true, wantHolder: HolderPhaseSession, wantPhase: "implementation"},
		{
			name: "exact Flow stored session wins",
			stored: []sessions.SessionRecord{
				{FlowID: "flow-10", SessionID: "prefix", Status: "running"},
				{FlowID: "flow-1", SessionID: "exact", Status: "running"},
			},
			phaseStatus: flowstore.PhaseRunning,
			phaseLive:   true,
			wantHolder:  HolderFlowSession,
		},
		{name: "terminal phase session remains occupancy", phaseStatus: flowstore.PhaseCompleted, phaseLive: true, wantHolder: HolderPhaseSession, wantPhase: "implementation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase := flowstore.FlowPhase{
				PhaseID:   "implementation",
				Status:    tc.phaseStatus,
				LaunchIDs: []string{"launch-1"},
			}
			if tc.phaseLive {
				phase.Sessions = []flowstore.Session{{SessionID: "phase-session", LaunchID: "launch-1", Status: "running"}}
			}
			record := flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{phase}}
			verdict := Evaluate(Sources{
				Flows:    &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record},
				Sessions: &sessionStoreFixture{t: t, wantFlowID: "flow-1", records: tc.stored},
			}, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleWorktreeAgent, Stage: StageAuthoritative},
			})
			if verdict.Holder() != tc.wantHolder || verdict.PhaseID() != tc.wantPhase || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %q, %v), want (%v, %q, nil)", verdict.Holder(), verdict.PhaseID(), verdict.Err(), tc.wantHolder, tc.wantPhase)
			}
		})
	}
}

func TestSavedSessionResumeAuthoritativeOccupancy(t *testing.T) {
	tests := []struct {
		name        string
		phaseStatus flowstore.PhaseStatus
		phaseLive   bool
		stored      []sessions.SessionRecord
		wantHolder  Holder
		wantPhase   string
	}{
		{name: "running phase", phaseStatus: flowstore.PhaseRunning, wantHolder: HolderRunningPhase, wantPhase: "implementation"},
		{name: "live phase session", phaseStatus: flowstore.PhaseReady, phaseLive: true, wantHolder: HolderPhaseSession, wantPhase: "implementation"},
		{name: "running status wins within live phase", phaseStatus: flowstore.PhaseRunning, phaseLive: true, wantHolder: HolderRunningPhase, wantPhase: "implementation"},
		{
			name:        "active exact Flow stored session",
			phaseStatus: flowstore.PhaseReady,
			stored: []sessions.SessionRecord{
				{FlowID: "flow-10", SessionID: "prefix", Status: "running"},
				{FlowID: "flow-1", SessionID: "exact", Status: "running"},
			},
			wantHolder: HolderFlowSession,
		},
		{
			name:        "phase occupancy precedes Flow stored session",
			phaseStatus: flowstore.PhaseRunning,
			stored:      []sessions.SessionRecord{{FlowID: "flow-1", SessionID: "exact", Status: "running"}},
			wantHolder:  HolderRunningPhase,
			wantPhase:   "implementation",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase := flowstore.FlowPhase{
				PhaseID:   "implementation",
				Status:    tc.phaseStatus,
				LaunchIDs: []string{"launch-1"},
			}
			if tc.phaseLive {
				phase.Sessions = []flowstore.Session{{SessionID: "phase-session", LaunchID: "launch-1", Status: "running"}}
			}
			record := flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{phase}}
			verdict := Evaluate(Sources{
				Flows:    &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record},
				Sessions: &sessionStoreFixture{t: t, wantFlowID: "flow-1", records: tc.stored},
			}, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleSavedSessionResume, Stage: StageAuthoritative},
			})
			if verdict.Holder() != tc.wantHolder || verdict.PhaseID() != tc.wantPhase || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %q, %v), want (%v, %q, nil)", verdict.Holder(), verdict.PhaseID(), verdict.Err(), tc.wantHolder, tc.wantPhase)
			}
		})
	}
}

func TestPhaseResumeAuthoritativeOccupancyRemainsPhaseScopedWithSessionExemption(t *testing.T) {
	target := flowstore.Session{
		Provider: "Codex", SessionID: "target", LaunchID: "launch-implementation", Status: "running",
	}
	record := flowstore.FlowRecord{
		FlowID: "flow-1",
		Phases: []flowstore.FlowPhase{
			{
				PhaseID:   "implementation",
				LaunchIDs: []string{"launch-implementation"},
				Sessions:  []flowstore.Session{target},
			},
			{
				PhaseID:   "review",
				LaunchIDs: []string{"launch-review"},
				Sessions: []flowstore.Session{{
					Provider: "claude", SessionID: "other-phase", LaunchID: "launch-review", Status: "running",
				}},
			},
		},
	}
	query := Query{
		FlowID:      "flow-1",
		Purpose:     Purpose{Role: actions.RolePhaseResume, Stage: StageAuthoritative},
		PhaseID:     "implementation",
		SkipSession: SessionIdentity{Provider: "codex", SessionID: "target"},
	}
	evaluate := func(record flowstore.FlowRecord) Verdict {
		return Evaluate(Sources{
			Flows:    &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record},
			Sessions: &sessionStoreFixture{t: t, wantFlowID: "flow-1"},
		}, query)
	}
	if verdict := evaluate(record); verdict.Occupied() || verdict.Err() != nil {
		t.Fatalf("exempt target plus other-phase session verdict = (%v, %q, %v), want free", verdict.Holder(), verdict.PhaseID(), verdict.Err())
	}
	record.Phases[0].Sessions = append(record.Phases[0].Sessions, flowstore.Session{
		Provider: "claude", SessionID: "competitor", LaunchID: "launch-implementation", Status: "running",
	})
	if verdict := evaluate(record); verdict.Holder() != HolderPhaseSession || verdict.PhaseID() != "implementation" || verdict.Err() != nil {
		t.Fatalf("same-phase competitor verdict = (%v, %q, %v), want phase session on implementation", verdict.Holder(), verdict.PhaseID(), verdict.Err())
	}
}

func TestReservedAuthoritativePurposesCheckLeaseBeforeStores(t *testing.T) {
	for _, role := range []actions.FlowLaunchRole{
		actions.RoleRepair,
		actions.RoleAutofix,
		actions.RoleWorktreeAgent,
		actions.RoleSavedSessionResume,
	} {
		t.Run(role.String(), func(t *testing.T) {
			verdict := Evaluate(Sources{
				Lease:    &leaseFixture{t: t, wantFlowID: "flow-1", occupied: true},
				Flows:    panicFlowReader{},
				Sessions: panicSessionStore{},
			}, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: role, Stage: StageReserved},
			})
			if verdict.Holder() != HolderPeerLease || verdict.Err() != nil {
				t.Fatalf("verdict = (%v, %v), want peer lease", verdict.Holder(), verdict.Err())
			}
		})
	}
}

func TestAutoAdvanceAuthoritativeRunningPhasePrecedesSession(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID: "flow-1",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "implementation", Status: flowstore.PhaseReady, LaunchIDs: []string{"launch-1"}},
			{PhaseID: "review-loop", Status: flowstore.PhaseRunning},
		},
	}
	flows := &flowReaderFixture{t: t, wantFlowID: "flow-1", record: record}
	store := &sessionStoreFixture{
		t: t, wantFlowID: "flow-1",
		records: []sessions.SessionRecord{{
			FlowID: "flow-1", SessionID: "session-1", LaunchID: "launch-1", Status: "running",
		}},
	}
	verdict := Evaluate(Sources{Flows: flows, Sessions: store}, Query{
		FlowID: "flow-1",
		Purpose: Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: StageAutoAdvance,
		},
		Freshness: FreshnessAuthoritative,
		PhaseID:   "implementation",
	})
	if verdict.Holder() != HolderRunningPhase || verdict.PhaseID() != "review-loop" || verdict.Err() != nil {
		t.Fatalf("verdict = (%v, %q, %v), want running review-loop", verdict.Holder(), verdict.PhaseID(), verdict.Err())
	}
	if flows.calls != 1 || store.calls != 0 {
		t.Fatalf("source calls = (Flow %d, sessions %d), want (1, 0)", flows.calls, store.calls)
	}
}

func TestAutoAdvanceAuthoritativeExcludesNormalizedCandidatePhase(t *testing.T) {
	flows := &flowReaderFixture{
		t: t, wantFlowID: "flow-1",
		record: flowstore.FlowRecord{
			FlowID: "flow-1",
			Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation", Status: flowstore.PhaseRunning,
			}},
		},
	}
	store := &sessionStoreFixture{t: t, wantFlowID: "flow-1"}
	verdict := Evaluate(Sources{Flows: flows, Sessions: store}, Query{
		FlowID: "flow-1",
		Purpose: Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: StageAutoAdvance,
		},
		Freshness: FreshnessAuthoritative,
		PhaseID:   "  Implementation  ",
	})
	if verdict.Occupied() || verdict.Err() != nil {
		t.Fatalf("verdict = (%v, %q, %v), want the normalized candidate excluded", verdict.Holder(), verdict.PhaseID(), verdict.Err())
	}
	if flows.calls != 1 || store.calls != 1 {
		t.Fatalf("source calls = (Flow %d, sessions %d), want (1, 1)", flows.calls, store.calls)
	}
}

func TestAuthoritativeQueriesFailClosedAtSourceBoundary(t *testing.T) {
	flowErr := errors.New("read Flow failed")
	sessionErr := errors.New("list sessions failed")
	baseRecord := flowstore.FlowRecord{FlowID: "flow-1"}
	tests := []struct {
		name       string
		sources    func(*testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture)
		wantErr    error
		wantFlows  int
		wantStores int
	}{
		{
			name: "missing Flow reader", wantErr: ErrMissingFlowStore,
			sources: func(t *testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture) {
				store := &sessionStoreFixture{t: t}
				return Sources{Sessions: store}, nil, store
			},
		},
		{
			name: "Flow read error", wantErr: flowErr, wantFlows: 1,
			sources: func(t *testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture) {
				flows := &flowReaderFixture{t: t, err: flowErr}
				store := &sessionStoreFixture{t: t}
				return Sources{Flows: flows, Sessions: store}, flows, store
			},
		},
		{
			name: "wrong Flow identity", wantErr: ErrInvalidQuery, wantFlows: 1,
			sources: func(t *testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture) {
				flows := &flowReaderFixture{t: t, record: flowstore.FlowRecord{FlowID: "flow-10"}}
				store := &sessionStoreFixture{t: t}
				return Sources{Flows: flows, Sessions: store}, flows, store
			},
		},
		{
			name: "missing session reader", wantErr: ErrMissingSessionStore, wantFlows: 1,
			sources: func(t *testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture) {
				flows := &flowReaderFixture{t: t, record: baseRecord}
				return Sources{Flows: flows}, flows, nil
			},
		},
		{
			name: "session read error", wantErr: sessionErr, wantFlows: 1, wantStores: 1,
			sources: func(t *testing.T) (Sources, *flowReaderFixture, *sessionStoreFixture) {
				flows := &flowReaderFixture{t: t, record: baseRecord}
				store := &sessionStoreFixture{t: t, err: sessionErr}
				return Sources{Flows: flows, Sessions: store}, flows, store
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sources, flows, store := tc.sources(t)
			verdict := Evaluate(sources, Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageAuthoritative},
				PhaseID: "implementation",
			})
			if !verdict.Occupied() || !errors.Is(verdict.Err(), tc.wantErr) {
				t.Fatalf("verdict = (%v, %v), want fail-closed errors.Is(_, %v)", verdict.Holder(), verdict.Err(), tc.wantErr)
			}
			if flows != nil && flows.calls != tc.wantFlows {
				t.Fatalf("Flow calls = %d, want %d", flows.calls, tc.wantFlows)
			}
			if store != nil && store.calls != tc.wantStores {
				t.Fatalf("session calls = %d, want %d", store.calls, tc.wantStores)
			}
		})
	}
}

type panicAgentProbe struct{}

func (panicAgentProbe) FlowAgentRunning(flowstore.FlowRecord, string) bool {
	panic("advisory query reached tmux probe")
}

func (panicAgentProbe) AutofixAgentRunning(flowstore.FlowRecord, string) bool {
	panic("advisory query reached tmux probe")
}

func TestAdvisoryCachedEvidence(t *testing.T) {
	ended := time.Now()
	phaseWithSession := flowstore.FlowPhase{
		PhaseID:   "implementation",
		LaunchIDs: []string{" launch-1 "},
		Sessions:  []flowstore.Session{{SessionID: "session-1", LaunchID: "launch-1", Status: "active"}},
	}
	tests := []struct {
		name       string
		purpose    Purpose
		phaseID    string
		flow       flowstore.FlowRecord
		sessions   []sessions.SessionRecord
		runtime    runtimeFixture
		wantHolder Holder
		wantPhase  string
	}{
		{
			name:       "drain phase session is launch scoped",
			purpose:    Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain},
			phaseID:    "implementation",
			flow:       flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{phaseWithSession}},
			wantHolder: HolderPhaseSession,
			wantPhase:  "implementation",
		},
		{
			name:    "drain ignores unmatched phase session",
			purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain},
			phaseID: "implementation",
			flow: flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation", LaunchIDs: []string{"launch-other"},
				Sessions: []flowstore.Session{{SessionID: "session-1", LaunchID: "launch-1", Status: "active"}},
			}}},
		},
		{
			name:    "drain ignores ended phase session",
			purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain},
			phaseID: "implementation",
			flow: flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{
				PhaseID: "implementation", LaunchIDs: []string{"launch-1"},
				Sessions: []flowstore.Session{{SessionID: "session-1", LaunchID: "launch-1", Status: "ended", EndedAt: ended}},
			}}},
		},
		{
			name:       "drain attempt outranks cached running phase and terminal",
			purpose:    Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain},
			flow:       flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Status: flowstore.PhaseRunning}}},
			runtime:    runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}, flows: map[string]bool{"flow-1": true}},
			wantHolder: HolderPhaseAttempt,
		},
		{
			name:       "worktree footer reads whole Flow session mirror",
			purpose:    Purpose{Role: actions.RoleWorktreeAgent, Stage: StageFooter},
			flow:       flowstore.FlowRecord{FlowID: "flow-1"},
			sessions:   []sessions.SessionRecord{{FlowID: " flow-1 ", SessionID: "session-1", Status: "active"}},
			wantHolder: HolderFlowSession,
		},
		{
			name:       "worktree runtime holder outranks both cached families",
			purpose:    Purpose{Role: actions.RoleWorktreeAgent, Stage: StageFooter},
			flow:       flowstore.FlowRecord{FlowID: "flow-1", Phases: []flowstore.FlowPhase{phaseWithSession}},
			sessions:   []sessions.SessionRecord{{FlowID: "flow-1", SessionID: "session-1", Status: "active"}},
			runtime:    runtimeFixture{flows: map[string]bool{"flow-1": true}},
			wantHolder: HolderFlowTerminal,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flowCache := &flowCacheFixture{record: tc.flow, found: true}
			sessionCache := &sessionCacheFixture{records: tc.sessions, wantFlowID: "flow-1"}
			advice := EvaluateAdvisory(Sources{
				Flows: panicFlowReader{}, FlowCache: flowCache,
				Sessions: panicSessionStore{}, Cache: sessionCache,
				Lease: &leaseFixture{t: t, wantFlowID: "flow-1"}, Runtime: tc.runtime,
				Probe: panicAgentProbe{},
			}, Query{FlowID: " flow-1 ", Purpose: tc.purpose, PhaseID: tc.phaseID})
			if advice.verdict.Holder() != tc.wantHolder || advice.verdict.PhaseID() != tc.wantPhase {
				t.Fatalf("advice = (%v, %q), want (%v, %q)", advice.verdict.Holder(), advice.verdict.PhaseID(), tc.wantHolder, tc.wantPhase)
			}
			if advice.Defer() != (tc.wantHolder != HolderNone) {
				t.Fatalf("Defer() = %v, want %v", advice.Defer(), tc.wantHolder != HolderNone)
			}
			if tc.purpose.Role == actions.RoleTrackedPhase && sessionCache.calls != 0 {
				t.Fatalf("session cache calls = %d, want drain to skip the whole-Flow session mirror", sessionCache.calls)
			}
		})
	}
}

func TestAdvisoryFailsClosedForInvalidQueriesAndMissingCaches(t *testing.T) {
	tests := []struct {
		name    string
		sources Sources
		query   Query
		wantErr error
	}{
		{name: "blank Flow ID", query: Query{Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain}}, wantErr: ErrInvalidQuery},
		{name: "authoritative purpose", query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageAuthoritative}}, wantErr: ErrInvalidQuery},
		{name: "missing Flow cache", sources: Sources{Lease: &leaseFixture{}, Runtime: runtimeFixture{}}, query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain}}, wantErr: ErrMissingFlowCache},
		{name: "missing session cache", sources: Sources{FlowCache: &flowCacheFixture{record: flowstore.FlowRecord{FlowID: "flow-1"}, found: true}, Lease: &leaseFixture{}, Runtime: runtimeFixture{}}, query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleWorktreeAgent, Stage: StageFooter}}, wantErr: ErrMissingSessionCache},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			advice := EvaluateAdvisory(tc.sources, tc.query)
			if !advice.Defer() {
				t.Fatal("Defer() = false, want fail-closed deferral")
			}
			if !errors.Is(advice.verdict.Err(), tc.wantErr) {
				t.Fatalf("Err() = %v, want errors.Is(_, %v)", advice.verdict.Err(), tc.wantErr)
			}
		})
	}
}

func TestPurposeValidRegistry(t *testing.T) {
	validStages := map[actions.FlowLaunchRole][]Stage{
		actions.RoleNone:               {StagePreview, StageFooter, StageSessionRelease},
		actions.RoleTrackedPhase:       {StagePreview, StageFooter, StageAdmission, StageAutoAdvance, StageAuthoritative, StageReserved, StageInstall, StageDrain, StageDrainControl},
		actions.RoleCreatePhase:        {StageAdmission, StageAuthoritative, StageInstall},
		actions.RolePhaseResume:        {StagePreview, StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleRepair:             {StagePreview, StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleAutofix:            {StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleWorktreeAgent:      {StageFooter, StageAdmission, StageAuthoritative, StageReserved, StageInstall},
		actions.RoleSavedSessionResume: {StageAdmission, StageAuthoritative, StageReserved, StageInstall},
	}
	want := make(map[Purpose]bool)
	for role, stages := range validStages {
		for _, stage := range stages {
			want[Purpose{Role: role, Stage: stage}] = true
		}
	}
	roles := []actions.FlowLaunchRole{
		actions.RoleNone, actions.RoleTrackedPhase, actions.RolePhaseResume,
		actions.RoleRepair, actions.RoleAutofix, actions.RoleWorktreeAgent,
		actions.RoleSavedSessionResume, actions.RoleCreatePhase,
	}
	for _, role := range roles {
		for stage := StageUnknown; stage <= StageSessionRelease; stage++ {
			purpose := Purpose{Role: role, Stage: stage}
			if got := purpose.Valid(); got != want[purpose] {
				t.Errorf("Purpose{%s, %s}.Valid() = %v, want %v", role, stage, got, want[purpose])
			}
		}
	}
	if len(purposeRegistry) != len(want) {
		t.Fatalf("purpose registry has %d rows, want %d", len(purposeRegistry), len(want))
	}
}

func TestQueryRejectsInvalidInputsAndMissingRuntime(t *testing.T) {
	tests := []struct {
		name  string
		query Query
		want  error
	}{
		{
			name:  "blank Flow ID",
			query: Query{FlowID: "   ", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}},
			want:  ErrInvalidQuery,
		},
		{
			name:  "phantom purpose",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleAutofix, Stage: StageDrain}},
			want:  ErrInvalidQuery,
		},
		{
			name:  "unsupported freshness",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StagePreview}, Freshness: FreshnessAuthoritative},
			want:  ErrInvalidQuery,
		},
		{
			name:  "unknown freshness",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}, Freshness: FreshnessAuthoritative + 1},
			want:  ErrInvalidQuery,
		},
		{
			name:  "required runtime adapter missing",
			query: Query{FlowID: "flow-1", Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission}},
			want:  ErrMissingRuntime,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{}).Query(tc.query)
			if !verdict.Occupied() {
				t.Fatal("Occupied() = false, want fail-closed occupancy")
			}
			if !errors.Is(verdict.Err(), tc.want) {
				t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.want)
			}
		})
	}
}

type runtimeFixture struct {
	attempts       map[string]actions.FlowLaunchRole
	flows          map[string]bool
	nonRepairFlows map[string]bool
	repairs        map[string]bool
	headless       map[string]bool
	repairDrains   map[string]bool
}

type leaseFixture struct {
	wantFlowID string
	occupied   bool
	err        error
	calls      int
	t          *testing.T
}

func (lease *leaseFixture) FlowLeaseOccupied(flowID string) (bool, error) {
	lease.calls++
	if lease.wantFlowID != "" && flowID != lease.wantFlowID {
		lease.t.Fatalf("FlowLeaseOccupied() flow ID = %q, want %q", flowID, lease.wantFlowID)
	}
	return lease.occupied, lease.err
}

func TestQueryAnswersFromLeaseSource(t *testing.T) {
	inspectErr := errors.New("unsafe flow-leases directory")
	tests := []struct {
		name       string
		lease      *leaseFixture
		wantHolder Holder
		wantErr    error
	}{
		{name: "free", lease: &leaseFixture{}, wantHolder: HolderNone},
		{name: "held", lease: &leaseFixture{occupied: true}, wantHolder: HolderPeerLease},
		{name: "inspection error", lease: &leaseFixture{err: inspectErr}, wantHolder: HolderLeaseUnreadable, wantErr: inspectErr},
		{name: "missing adapter", wantHolder: HolderLeaseUnreadable, wantErr: ErrMissingLease},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lease != nil {
				tc.lease.t = t
				tc.lease.wantFlowID = "flow-exact"
			}
			sources := Sources{FlowCache: &flowCacheFixture{}, Runtime: runtimeFixture{}}
			if tc.lease != nil {
				sources.Lease = tc.lease
			}
			verdict := New(sources).Query(Query{
				FlowID:  " flow-exact ",
				Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StagePreview},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
			if !errors.Is(verdict.Err(), tc.wantErr) {
				t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.wantErr)
			}
			if tc.lease != nil && tc.lease.calls != 1 {
				t.Fatalf("lease calls = %d, want 1", tc.lease.calls)
			}
		})
	}
}

func TestTrackedPhaseLeaseAndRuntimePrecedence(t *testing.T) {
	tests := []struct {
		name          string
		stage         Stage
		leaseOccupied bool
		runtime       runtimeFixture
		wantHolder    Holder
	}{
		{
			name:          "preview lease outranks attempt and terminal",
			stage:         StagePreview,
			leaseOccupied: true,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}},
			wantHolder:    HolderPeerLease,
		},
		{
			name:          "footer headless write outranks lease",
			stage:         StageFooter,
			leaseOccupied: true,
			runtime:       runtimeFixture{headless: map[string]bool{"flow-1": true}},
			wantHolder:    HolderHeadlessWrite,
		},
		{
			name:       "footer Flow terminal outranks attempt",
			stage:      StageFooter,
			runtime:    runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}},
			wantHolder: HolderFlowTerminal,
		},
		{
			name:       "footer non-repair Flow terminal outranks attempt and repair slot",
			stage:      StageFooter,
			runtime:    runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}, flows: map[string]bool{"flow-1": true}, nonRepairFlows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}},
			wantHolder: HolderFlowTerminal,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{
				FlowCache: &flowCacheFixture{},
				Lease:     &leaseFixture{occupied: tc.leaseOccupied, t: t, wantFlowID: "flow-1"},
				Runtime:   tc.runtime,
			}).Query(Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: tc.stage},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
		})
	}

	t.Run("footer headless write outranks unreadable lease", func(t *testing.T) {
		verdict := New(Sources{
			FlowCache: &flowCacheFixture{},
			Lease:     &leaseFixture{err: errors.New("lease unreadable"), t: t, wantFlowID: "flow-1"},
			Runtime:   runtimeFixture{headless: map[string]bool{"flow-1": true}},
		}).Query(Query{
			FlowID:  "flow-1",
			Purpose: Purpose{Role: actions.RoleTrackedPhase, Stage: StageFooter},
		})
		if verdict.Holder() != HolderHeadlessWrite || verdict.Err() != nil {
			t.Fatalf("verdict = (%v, %v), want headless write with nil error", verdict.Holder(), verdict.Err())
		}
	})
}

func TestRepairPurposeHolderPrecedence(t *testing.T) {
	leaseErr := errors.New("unsafe flow-leases directory")
	tests := []struct {
		name          string
		leaseOccupied bool
		leaseErr      error
		runtime       runtimeFixture
		wantPreview   Holder
		wantAdmission Holder
		wantFooter    Holder
	}{
		{name: "free"},
		{name: "repair attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}}, wantPreview: HolderRepairAttempt, wantAdmission: HolderRepairAttempt, wantFooter: HolderRepairAttempt},
		{name: "phase resume attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RolePhaseResume}}, wantPreview: HolderPhaseResumeAttempt, wantAdmission: HolderPhaseResumeAttempt, wantFooter: HolderPhaseResumeAttempt},
		{name: "phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}}, wantPreview: HolderPhaseAttempt, wantAdmission: HolderPhaseAttempt, wantFooter: HolderPhaseAttempt},
		{name: "other attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleAutofix}}, wantPreview: HolderOtherAttempt, wantAdmission: HolderOtherAttempt, wantFooter: HolderOtherAttempt},
		{name: "Flow terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, wantPreview: HolderFlowTerminal, wantAdmission: HolderFlowTerminal, wantFooter: HolderFlowTerminal},
		{name: "terminal-less repair slot", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, wantPreview: HolderRepairTerminal, wantAdmission: HolderRepairTerminal, wantFooter: HolderRepairTerminal},
		{name: "headless write", runtime: runtimeFixture{headless: map[string]bool{"flow-1": true}}, wantAdmission: HolderHeadlessWrite, wantFooter: HolderHeadlessWrite},
		{
			name:          "repair attempt wins every runtime source",
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderRepairAttempt,
			wantAdmission: HolderRepairAttempt,
			wantFooter:    HolderRepairAttempt,
		},
		{
			name:          "held lease wins every runtime source",
			leaseOccupied: true,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderPeerLease,
			wantAdmission: HolderPeerLease,
			wantFooter:    HolderPeerLease,
		},
		{
			name:          "unreadable lease wins every runtime source",
			leaseErr:      leaseErr,
			runtime:       runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}, headless: map[string]bool{"flow-1": true}},
			wantPreview:   HolderLeaseUnreadable,
			wantAdmission: HolderLeaseUnreadable,
			wantFooter:    HolderLeaseUnreadable,
		},
	}
	for _, tc := range tests {
		for _, stage := range []struct {
			name  string
			stage Stage
			want  Holder
		}{
			{name: "preview", stage: StagePreview, want: tc.wantPreview},
			{name: "admission", stage: StageAdmission, want: tc.wantAdmission},
			{name: "footer", stage: StageFooter, want: tc.wantFooter},
		} {
			t.Run(tc.name+"/"+stage.name, func(t *testing.T) {
				lease := &leaseFixture{occupied: tc.leaseOccupied, err: tc.leaseErr, t: t, wantFlowID: "flow-1"}
				verdict := New(Sources{FlowCache: &flowCacheFixture{}, Lease: lease, Runtime: tc.runtime}).Query(Query{
					FlowID:  "flow-1",
					Purpose: Purpose{Role: actions.RoleRepair, Stage: stage.stage},
				})
				if verdict.Holder() != stage.want {
					t.Fatalf("Holder() = %v, want %v", verdict.Holder(), stage.want)
				}
				if !errors.Is(verdict.Err(), tc.leaseErr) {
					t.Fatalf("Err() = %v, want errors.Is(_, %v)", verdict.Err(), tc.leaseErr)
				}
			})
		}
	}
}

func (runtime runtimeFixture) AttemptHolder(flowID string) (actions.FlowLaunchRole, bool) {
	role, ok := runtime.attempts[flowID]
	return role, ok
}

func (runtime runtimeFixture) HasFlowTerminal(flowID string) bool { return runtime.flows[flowID] }
func (runtime runtimeFixture) HasNonRepairFlowTerminal(flowID string) bool {
	if runtime.nonRepairFlows != nil {
		return runtime.nonRepairFlows[flowID]
	}
	return runtime.flows[flowID] && !runtime.repairs[flowID]
}
func (runtime runtimeFixture) HasRepairTerminal(flowID string) bool { return runtime.repairs[flowID] }
func (runtime runtimeFixture) HeadlessWritePending(flowID string) bool {
	return runtime.headless[flowID]
}
func (runtime runtimeFixture) RepairDrainPending(flowID string) bool {
	return runtime.repairDrains[flowID]
}

func TestQueryAnswersFromInProcessRuntime(t *testing.T) {
	tests := []struct {
		name    string
		runtime runtimeFixture
		flowID  string
		want    Holder
	}{
		{name: "free", runtime: runtimeFixture{}, flowID: "flow-1", want: HolderNone},
		{name: "exact Flow ID only", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-10": actions.RoleRepair}}, flowID: "flow-1", want: HolderNone},
		{name: "repair attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}}, flowID: "flow-1", want: HolderRepairAttempt},
		{name: "phase resume attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RolePhaseResume}}, flowID: "flow-1", want: HolderPhaseResumeAttempt},
		{name: "tracked phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleTrackedPhase}}, flowID: "flow-1", want: HolderPhaseAttempt},
		{name: "create phase attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleCreatePhase}}, flowID: "flow-1", want: HolderPhaseAttempt},
		{name: "other attempt", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleAutofix}}, flowID: "flow-1", want: HolderOtherAttempt},
		{name: "Flow terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderFlowTerminal},
		{name: "repair slot", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairTerminal},
		{name: "repair slot with live terminal", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairTerminal},
		{name: "attempt wins simultaneous sources", runtime: runtimeFixture{attempts: map[string]actions.FlowLaunchRole{"flow-1": actions.RoleRepair}, flows: map[string]bool{"flow-1": true}, repairs: map[string]bool{"flow-1": true}}, flowID: "flow-1", want: HolderRepairAttempt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{Runtime: tc.runtime}).Query(Query{
				FlowID:  tc.flowID,
				Purpose: Purpose{Role: actions.RoleCreatePhase, Stage: StageAdmission},
			})
			if verdict.Holder() != tc.want {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.want)
			}
			if verdict.Occupied() != (tc.want != HolderNone) {
				t.Fatalf("Occupied() = %v, want %v", verdict.Occupied(), tc.want != HolderNone)
			}
			if verdict.Err() != nil {
				t.Fatalf("Err() = %v, want nil", verdict.Err())
			}
		})
	}
}

func TestStageInstallReadsPerRoleTerminalSources(t *testing.T) {
	tests := []struct {
		name       string
		role       actions.FlowLaunchRole
		flow       bool
		repair     bool
		wantHolder Holder
	}{
		{name: "tracked phase ignores Flow terminal", role: actions.RoleTrackedPhase, flow: true, wantHolder: HolderNone},
		{name: "tracked phase reads repair terminal", role: actions.RoleTrackedPhase, repair: true, wantHolder: HolderRepairTerminal},
		{name: "create phase reads Flow terminal", role: actions.RoleCreatePhase, flow: true, wantHolder: HolderFlowTerminal},
		{name: "create phase reads repair terminal", role: actions.RoleCreatePhase, repair: true, wantHolder: HolderRepairTerminal},
		{name: "phase resume ignores Flow terminal", role: actions.RolePhaseResume, flow: true, wantHolder: HolderNone},
		{name: "phase resume reads repair terminal", role: actions.RolePhaseResume, repair: true, wantHolder: HolderRepairTerminal},
		{name: "repair reads Flow terminal", role: actions.RoleRepair, flow: true, wantHolder: HolderFlowTerminal},
		{name: "repair reads repair terminal", role: actions.RoleRepair, repair: true, wantHolder: HolderRepairTerminal},
		{name: "autofix ignores Flow terminal", role: actions.RoleAutofix, flow: true, wantHolder: HolderNone},
		{name: "autofix reads repair terminal", role: actions.RoleAutofix, repair: true, wantHolder: HolderRepairTerminal},
		{name: "worktree agent reads Flow terminal", role: actions.RoleWorktreeAgent, flow: true, wantHolder: HolderFlowTerminal},
		{name: "worktree agent reads repair terminal", role: actions.RoleWorktreeAgent, repair: true, wantHolder: HolderRepairTerminal},
		{name: "saved-session resume reads Flow terminal", role: actions.RoleSavedSessionResume, flow: true, wantHolder: HolderFlowTerminal},
		{name: "saved-session resume ignores repair terminal", role: actions.RoleSavedSessionResume, repair: true, wantHolder: HolderNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := New(Sources{Runtime: runtimeFixture{
				flows:   map[string]bool{"flow-1": tc.flow},
				repairs: map[string]bool{"flow-1": tc.repair},
			}}).Query(Query{
				FlowID:  "flow-1",
				Purpose: Purpose{Role: tc.role, Stage: StageInstall},
			})
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
			if verdict.Err() != nil {
				t.Fatalf("Err() = %v, want nil", verdict.Err())
			}
		})
	}
}

func TestAutofixAdmissionUsesWholeFlowAgentProbe(t *testing.T) {
	policy := purposeRegistry[Purpose{Role: actions.RoleAutofix, Stage: StageAdmission}]
	if policy.probe != probeFlowAgent {
		t.Fatalf("probe = %v, want whole-Flow agent probe %v", policy.probe, probeFlowAgent)
	}
}

func TestPhaseResumePreviewPreservesRepairSlotException(t *testing.T) {
	policy := purposeRegistry[Purpose{Role: actions.RolePhaseResume, Stage: StageFooter}]
	tests := []struct {
		name       string
		flow       bool
		repair     bool
		wantHolder Holder
	}{
		{name: "neither", wantHolder: HolderNone},
		{name: "Flow terminal only", flow: true, wantHolder: HolderFlowTerminal},
		{name: "repair slot only", repair: true, wantHolder: HolderNone},
		{name: "overlapping Flow terminal and repair slot", flow: true, repair: true, wantHolder: HolderNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := queryRuntime(policy.runtime, runtimeFixture{
				flows:   map[string]bool{"flow-1": tc.flow},
				repairs: map[string]bool{"flow-1": tc.repair},
			}, "flow-1")
			if verdict.Holder() != tc.wantHolder {
				t.Fatalf("Holder() = %v, want %v", verdict.Holder(), tc.wantHolder)
			}
		})
	}
}

func TestPreviewAndFooterKeepHeadlessWritePoliciesSeparate(t *testing.T) {
	for _, role := range []actions.FlowLaunchRole{actions.RoleTrackedPhase, actions.RoleRepair} {
		runtime := runtimeFixture{headless: map[string]bool{"flow-1": true}}
		preview := purposeRegistry[Purpose{Role: role, Stage: StagePreview}]
		if verdict := queryRuntime(preview.runtime, runtime, "flow-1"); verdict.Holder() != HolderNone {
			t.Errorf("%s preview holder = %v, want none", role, verdict.Holder())
		}
		footer := purposeRegistry[Purpose{Role: role, Stage: StageFooter}]
		if verdict := queryRuntime(footer.runtime, runtime, "flow-1"); verdict.Holder() != HolderHeadlessWrite {
			t.Errorf("%s footer holder = %v, want %v", role, verdict.Holder(), HolderHeadlessWrite)
		}
	}
}

func TestDrainGateAndControlKeepRepairStateSeparate(t *testing.T) {
	gate := purposeRegistry[Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrain}]
	control := purposeRegistry[Purpose{Role: actions.RoleTrackedPhase, Stage: StageDrainControl}]
	tests := []struct {
		name        string
		runtime     runtimeFixture
		wantGate    Holder
		wantControl Holder
	}{
		{name: "Flow terminal gates", runtime: runtimeFixture{flows: map[string]bool{"flow-1": true}}, wantGate: HolderFlowTerminal},
		{name: "repair slot controls", runtime: runtimeFixture{repairs: map[string]bool{"flow-1": true}}, wantControl: HolderRepairTerminal},
		{name: "repair marker controls", runtime: runtimeFixture{repairDrains: map[string]bool{"flow-1": true}}, wantControl: HolderRepairDrain},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if verdict := queryRuntime(gate.runtime, tc.runtime, "flow-1"); verdict.Holder() != tc.wantGate {
				t.Errorf("gate holder = %v, want %v", verdict.Holder(), tc.wantGate)
			}
			if verdict := queryRuntime(control.runtime, tc.runtime, "flow-1"); verdict.Holder() != tc.wantControl {
				t.Errorf("control holder = %v, want %v", verdict.Holder(), tc.wantControl)
			}
		})
	}
	if gate.sources&readSessionCache != 0 {
		t.Fatal("drain gate reads the session mirror, want cached Flow record only")
	}
}

func TestSessionReleaseFooterAndGestureKeepSourcesSeparate(t *testing.T) {
	footer := purposeRegistry[Purpose{Role: actions.RoleNone, Stage: StageFooter}]
	if footer.sources != readFlowCache || footer.runtime != 0 {
		t.Fatalf("footer policy = {sources:%v runtime:%v}, want cached Flow only", footer.sources, footer.runtime)
	}
	gesture := purposeRegistry[Purpose{Role: actions.RoleNone, Stage: StageSessionRelease}]
	wantSources := readRuntime | readFlowCache | readSessionStore
	if gesture.sources != wantSources {
		t.Fatalf("gesture sources = %v, want %v", gesture.sources, wantSources)
	}
	if gesture.runtime != readAttempt|readHeadlessWrite {
		t.Fatalf("gesture runtime = %v, want attempt and headless-write sources", gesture.runtime)
	}
	if freshness, ok := resolveFreshness(StageSessionRelease, FreshnessDefault); !ok || freshness != FreshnessAuthoritative {
		t.Fatalf("default freshness = (%v, %v), want (%v, true)", freshness, ok, FreshnessAuthoritative)
	}
}

// Free() stays the way callers and tests express "nothing holds it", and must
// not drift into occupancy alongside the fail-closed stub.
func TestFreeIsNotOccupied(t *testing.T) {
	if Free().Occupied() {
		t.Fatal("Free().Occupied() = true, want false")
	}
	if Free().Err() != nil {
		t.Fatalf("Free().Err() = %v, want nil", Free().Err())
	}
}

// A corrupt or future Holder must not be readable as a free Flow. Naming
// HolderNone explicitly keeps the default branch for values this package does
// not know, the way Stage.String() already does.
func TestHolderStringSeparatesNoneFromUnknown(t *testing.T) {
	if got := HolderNone.String(); got != "none" {
		t.Fatalf("HolderNone.String() = %q, want %q", got, "none")
	}
	if got := (HolderHeadlessWrite + 1).String(); got != "unknown" {
		t.Fatalf("out-of-range Holder.String() = %q, want %q", got, "unknown")
	}
	if got := Holder(-1).String(); got != "unknown" {
		t.Fatalf("negative Holder.String() = %q, want %q", got, "unknown")
	}
}
