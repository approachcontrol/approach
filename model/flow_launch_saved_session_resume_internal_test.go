package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
)

func savedSessionLifecycleModel(opts Options) Model {
	return NewWithOptions([]scanner.Repo{{Path: "/repo", DisplayName: "repo"}}, opts)
}

func TestSavedSessionResumeTransfersAtoBAndInstallsUntrackedRetainedSlot(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-prefix", FlowID: "flow-a"}
	fresh := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-prefix", FlowID: "flow-b",
		RepoPath: "/repo", WorktreePath: "/repo/worktree", CWD: "/repo/worktree/subdir",
		Branch: "flow/b", Commit: "commit-b", PlanID: "plan-session", PlanPath: "/plans/session.md",
	}
	flow := flowstore.FlowRecord{FlowID: "flow-b", Status: flowstore.StatusInProgress}
	var started actions.AgentLaunchContext
	released := false
	addPhaseCalls := 0
	setPhaseCalls := 0
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(provider sessions.Provider, sessionID string) (sessions.SessionRecord, error) {
			if provider != cached.Provider || sessionID != cached.SessionID {
				t.Fatalf("ReadSession(%q, %q)", provider, sessionID)
			}
			return fresh, nil
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			if flowID != "flow-b" {
				t.Fatalf("ReadFlow(%q)", flowID)
			}
			return flow, nil
		},
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) { return nil, nil },
		ReserveFlowLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			if flowID != "flow-b" {
				t.Fatalf("ReserveFlowLaunch(%q)", flowID)
			}
			return flow, func() { released = true }, nil
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			addPhaseCalls++
			return flowstore.FlowRecord{}, nil
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			setPhaseCalls++
			return flowstore.FlowRecord{}, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			if released {
				t.Fatal("cross-process reservation released before slot installation")
			}
			started = ctx
			return internalFakeEmbeddedTerminal{state: "exited"}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "resume-token" }

	m, cmd := m.routeSavedSessionResume(cached, flowLaunchOriginSessionsPane)
	if cmd == nil {
		t.Fatal("Flow-associated resume was not admitted")
	}
	first := cmd().(flowLaunchEventMsg)
	m, cmd = m.handleFlowLaunchEvent(first)
	if cmd == nil {
		t.Fatal("session read did not schedule Flow read")
	}
	// The exact same delivery is stale after the synchronous A to B transfer.
	if replay, replayCmd := m.handleFlowLaunchEvent(first); replayCmd != nil || !replay.flowLaunchAttemptOccupied("flow-b") {
		t.Fatal("duplicate session-read delivery disturbed transferred ownership")
	}
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd == nil {
		t.Fatal("Flow read did not schedule protected preparation")
	}
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if !released {
		t.Fatal("cross-process reservation was not released after installation")
	}
	if addPhaseCalls != 0 || setPhaseCalls != 0 {
		t.Fatalf("saved resume mutated phase state: add=%d set=%d", addPhaseCalls, setPhaseCalls)
	}
	if started.Command != "codex" || started.ResumeSessionID != fresh.SessionID ||
		started.RepoPath != fresh.RepoPath || started.WorktreePath != fresh.WorktreePath || started.WorkingDir != fresh.CWD ||
		started.Branch != fresh.Branch || started.Commit != fresh.Commit || started.PlanID != fresh.PlanID || started.PlanPath != fresh.PlanPath ||
		started.FlowID != "flow-b" || started.FlowPhaseID != "" || started.FlowLaunchTracked || !started.FlowSavedSessionResume || !started.Embedded {
		t.Fatalf("started context = %#v", started)
	}
	if len(m.embeddedTerminals) != 1 || !m.embeddedTerminals[0].FlowSavedSessionResume ||
		m.embeddedTerminals[0].DetachPolicy != embeddedTerminalDetachNever {
		t.Fatalf("saved resume slot = %#v", m.embeddedTerminals)
	}
	if m.flowLaunchAttemptOccupied("flow-a") || m.flowLaunchAttemptOccupied("flow-b") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatalf("lifecycle ownership remained after slot handoff: %#v %#v", m.flowLaunchAttempts, m.flowLaunchSessionOwners)
	}
	if next := m.dismissExitedFlowEmbeddedTerminals(); len(next.embeddedTerminals) != 1 {
		t.Fatal("exited saved resume terminal auto-closed")
	}
}

func TestSavedSessionResumeRejectsDuplicateAndDestinationContention(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-a"}
	fresh := cached
	fresh.FlowID = "flow-b"
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return fresh, nil },
	})
	m.launchSeams.NewLaunchID = func() string { return "token-1" }
	m, first := m.routeSavedSessionResume(cached, flowLaunchOriginSessionsPane)
	if first == nil {
		t.Fatal("first resume not admitted")
	}
	if duplicate, cmd := m.routeSavedSessionResume(cached, flowLaunchOriginEmbeddedSessionPicker); cmd != nil || len(duplicate.flowLaunchSessionOwners) != 1 {
		t.Fatal("duplicate source was admitted")
	}
	m, _ = m.reserveFlowLaunchAttempt(flowLaunchAttempt{Token: "other", Kind: flowLaunchKindManualPhase, FlowID: "flow-b"}, flowLaunchStateReading)
	m, cmd := m.handleFlowLaunchEvent(first().(flowLaunchEventMsg))
	if cmd != nil || m.flowLaunchAttemptOccupied("flow-a") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatalf("destination contention stranded ownership: attempts=%#v sessions=%#v", m.flowLaunchAttempts, m.flowLaunchSessionOwners)
	}
}

func TestSavedSessionResumeAuthoritativeNonFlowUsesRefreshedEstablishedRoute(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-a"}
	fresh := cached
	fresh.FlowID = ""
	fresh.WorktreePath = "/repo/refreshed"
	var launched actions.AgentLaunchContext
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return fresh, nil },
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = ctx
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "token-1" }
	m, cmd := m.routeSavedSessionResume(cached, flowLaunchOriginInlineWorktreeSession)
	if cmd == nil {
		t.Fatal("session refresh was not scheduled")
	}
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd == nil {
		t.Fatal("authoritative non-Flow inline route did not launch externally")
	}
	if launched.WorktreePath != fresh.WorktreePath || launched.FlowID != "" || launched.ResumeSessionID != fresh.SessionID {
		t.Fatalf("non-Flow refreshed launch = %#v", launched)
	}
	if m.flowLaunchAttemptOccupied("flow-a") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatal("authoritative non-Flow route retained lifecycle ownership")
	}
}

func TestSavedSessionResumeMissingFlowUsesEstablishedRouteForEveryOrigin(t *testing.T) {
	for _, origin := range []flowLaunchOrigin{
		flowLaunchOriginSessionsPane,
		flowLaunchOriginEmbeddedSessionPicker,
		flowLaunchOriginInlineWorktreeSession,
	} {
		t.Run(originName(origin), func(t *testing.T) {
			fresh := sessions.SessionRecord{
				Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-missing",
				RepoPath: "/repo", WorktreePath: "/repo/worktree", CWD: "/repo/worktree/subdir",
			}
			var started actions.AgentLaunchContext
			m := savedSessionLifecycleModel(Options{
				ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return fresh, nil },
				ReadFlow: func(string) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{}, flowstore.ErrFlowNotFound
				},
				StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
					started = ctx
					return internalFakeEmbeddedTerminal{state: "running"}, nil
				},
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					started = ctx
					return actions.TerminalLaunchSpec{}, nil
				},
			})
			m.launchSeams.NewLaunchID = func() string { return "token" }
			m, cmd := m.routeSavedSessionResume(fresh, origin)
			m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
			m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
			if cmd == nil {
				t.Fatal("missing Flow did not delegate to the established route")
			}
			if started.ResumeSessionID != fresh.SessionID || started.WorkingDir != fresh.CWD || started.FlowID != "" {
				t.Fatalf("missing-Flow launch = %#v", started)
			}
			if m.flowLaunchAttemptOccupied(fresh.FlowID) || len(m.flowLaunchSessionOwners) != 0 {
				t.Fatal("missing-Flow delegation retained lifecycle ownership")
			}
		})
	}
}

func TestSavedSessionResumeFailuresAreStatusOnlyAndReleaseOwnership(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-a"}
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) {
			return sessions.SessionRecord{}, errors.New("read failed")
		},
		SetFlowPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("saved-session failure must not persist a phase")
			return flowstore.FlowRecord{}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "token-1" }
	m, cmd := m.routeSavedSessionResume(cached, flowLaunchOriginSessionsPane)
	m, followup := m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if followup != nil || m.status.Text != "read failed" || m.flowLaunchAttemptOccupied("flow-a") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatalf("failure result = status %q attempts %#v owners %#v", m.status.Text, m.flowLaunchAttempts, m.flowLaunchSessionOwners)
	}
}

func TestSavedSessionResumeRejectsAuthoritativeLiveTmuxWindow(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-a"}
	fresh := cached
	fresh.Status = "ended"
	fresh.LaunchID = "launch-live"
	fresh.RepoPath = "/repo"
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return fresh, nil },
	})
	m.launchBackend = "tmux"
	m.tmuxLaunchAvailable = func() bool { return true }
	m.repoTmuxLaunchWindowLive = func(repoPath string, launchIDs ...string) bool {
		if repoPath != fresh.RepoPath || len(launchIDs) != 1 || launchIDs[0] != fresh.LaunchID {
			t.Fatalf("tmux probe = %q %#v", repoPath, launchIDs)
		}
		return true
	}
	m.launchSeams.NewLaunchID = func() string { return "token-1" }
	m, cmd := m.routeSavedSessionResume(cached, flowLaunchOriginSessionsPane)
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd != nil || m.status.Text != tmuxSessionLiveWindowRefusal {
		t.Fatalf("live tmux refusal: cmd=%v status=%q", cmd != nil, m.status.Text)
	}
	if m.flowLaunchAttemptOccupied("flow-a") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatal("live tmux refusal retained lifecycle ownership")
	}
}

func TestSavedSessionResumeAllOriginsSubmitSameExactLifecycleIntent(t *testing.T) {
	for _, origin := range []flowLaunchOrigin{
		flowLaunchOriginSessionsPane,
		flowLaunchOriginEmbeddedSessionPicker,
		flowLaunchOriginInlineWorktreeSession,
	} {
		t.Run(originName(origin), func(t *testing.T) {
			record := sessions.SessionRecord{Provider: sessions.ProviderClaude, SessionID: "session-raw", FlowID: "flow-a"}
			m := savedSessionLifecycleModel(Options{
				ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return record, nil },
			})
			m.launchSeams.NewLaunchID = func() string { return "token" }
			m, cmd := m.routeSavedSessionResume(record, origin)
			if cmd == nil {
				t.Fatal("origin did not submit lifecycle intent")
			}
			first := cmd().(flowLaunchEventMsg)
			m, flowRead := m.handleFlowLaunchEvent(first)
			if flowRead == nil {
				t.Fatal("exact session read did not advance")
			}
			attempt, ok := m.flowLaunchAttempt("flow-a")
			if !ok || attempt.Kind != flowLaunchKindSavedSessionResume || attempt.Origin != origin ||
				attempt.SessionKey != (flowLaunchSavedSessionKey{Provider: record.Provider, SessionID: record.SessionID}) ||
				attempt.State != flowLaunchStateReading {
				t.Fatalf("attempt = %#v", attempt)
			}
			if replay, replayCmd := m.handleFlowLaunchEvent(first); replayCmd != nil || !replay.flowLaunchAttemptOccupied("flow-a") {
				t.Fatal("A to A duplicate session-read delivery disturbed ownership")
			}
		})
	}
}

func originName(origin flowLaunchOrigin) string {
	switch origin {
	case flowLaunchOriginSessionsPane:
		return "sessions"
	case flowLaunchOriginEmbeddedSessionPicker:
		return "picker"
	case flowLaunchOriginInlineWorktreeSession:
		return "inline"
	default:
		return "unknown"
	}
}

func TestValidateSavedSessionResumeFlowOccupancy(t *testing.T) {
	ended := sessions.SessionRecord{SessionID: "ended", Status: "ended"}
	closedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		record flowstore.FlowRecord
		stored []sessions.SessionRecord
		wantOK bool
	}{
		{"open empty", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}, []sessions.SessionRecord{ended}, true},
		{"wrong exact Flow", flowstore.FlowRecord{FlowID: "flow-10", Status: flowstore.StatusInProgress}, nil, false},
		{"closed", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusClosed, Closed: flowstore.Closure{Reason: "done", ClosedAt: &closedAt}}, nil, false},
		{"running phase", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress, Phases: []flowstore.FlowPhase{{Status: flowstore.PhaseRunning}}}, nil, false},
		{"live mirrored session", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress, Phases: []flowstore.FlowPhase{{LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{{SessionID: "live", LaunchID: "launch-1", Status: "last_seen"}}}}}, nil, false},
		{"unmatched mirrored session", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress, Phases: []flowstore.FlowPhase{{LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{{SessionID: "stray", LaunchID: "other", Status: "last_seen"}}}}}, nil, true},
		{"live stored session", flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}, []sessions.SessionRecord{{SessionID: "live", Status: "running"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSavedSessionResumeFlow("flow-1", tc.record, tc.stored)
			if (err == nil) != tc.wantOK {
				t.Fatalf("validate error = %v, wantOK %v", err, tc.wantOK)
			}
		})
	}
}

func TestStoredAndMirroredSessionsShareActivePredicate(t *testing.T) {
	endedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status  string
		endedAt time.Time
		want    bool
	}{
		{"", time.Time{}, true},
		{"", endedAt, false},
		{"ended", time.Time{}, false},
		{"last_seen", endedAt, true},
	} {
		stored := sessions.SessionRecord{Status: tc.status, EndedAt: tc.endedAt}
		mirrored := flowstore.Session{Status: tc.status, EndedAt: tc.endedAt}
		if got := sessions.IsActive(stored.Status, stored.EndedAt); got != tc.want {
			t.Fatalf("stored IsActive(%q, %v) = %v, want %v", tc.status, tc.endedAt, got, tc.want)
		}
		if got := sessions.IsActive(mirrored.Status, mirrored.EndedAt); got != tc.want {
			t.Fatalf("mirrored IsActive(%q, %v) = %v, want %v", tc.status, tc.endedAt, got, tc.want)
		}
	}
}

func TestSavedSessionResumeRevalidatesUnderReservation(t *testing.T) {
	session := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-1", WorktreePath: "/repo/worktree"}
	open := flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}
	closed := open
	closed.Status = flowstore.StatusClosed
	closedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	closed.Closed = flowstore.Closure{Reason: "done", ClosedAt: &closedAt}
	started := false
	released := false
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return session, nil },
		ReadFlow:    func(string) (flowstore.FlowRecord, error) { return open, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return nil, nil
		},
		ReserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return closed, func() { released = true }, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "token" }
	m, cmd := m.routeSavedSessionResume(session, flowLaunchOriginSessionsPane)
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd != nil || started || !released || !strings.Contains(m.status.Text, "closed") {
		t.Fatalf("revalidation result: cmd=%v started=%v released=%v status=%q", cmd != nil, started, released, m.status.Text)
	}
	if m.flowLaunchAttemptOccupied("flow-1") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatal("failed protected revalidation retained ownership")
	}
}

func TestSavedSessionResumeRevalidatesExactSessionUnderReservation(t *testing.T) {
	initial := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-1",
		WorktreePath: "/repo/worktree", Branch: "flow/one",
	}
	moved := initial
	moved.FlowID = "flow-2"
	moved.WorktreePath = "/repo/other-worktree"
	flow := flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}
	reads := 0
	started := false
	released := false
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(provider sessions.Provider, sessionID string) (sessions.SessionRecord, error) {
			if provider != initial.Provider || sessionID != initial.SessionID {
				t.Fatalf("ReadSession(%q, %q)", provider, sessionID)
			}
			reads++
			if reads == 1 {
				return initial, nil
			}
			return moved, nil
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) { return flow, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return nil, nil
		},
		ReserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flow, func() { released = true }, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "token" }
	m, cmd := m.routeSavedSessionResume(initial, flowLaunchOriginSessionsPane)
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd != nil || reads != 2 || started || !released || !strings.Contains(m.status.Text, "moved from Flow") {
		t.Fatalf("session revalidation result: cmd=%v reads=%d started=%v released=%v status=%q", cmd != nil, reads, started, released, m.status.Text)
	}
	if m.flowLaunchAttemptOccupied("flow-1") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatal("failed exact-session revalidation retained ownership")
	}
}

func TestSavedSessionResumeRechecksFinalTmuxLaunchUnderReservation(t *testing.T) {
	initial := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-1",
		RepoPath: "/repo", WorktreePath: "/repo/worktree", Status: "ended", LaunchID: "launch-old",
	}
	refreshed := initial
	refreshed.LaunchID = "launch-live"
	flow := flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}
	reads := 0
	probes := 0
	started := false
	released := false
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) {
			reads++
			if reads == 1 {
				return initial, nil
			}
			return refreshed, nil
		},
		ReadFlow: func(string) (flowstore.FlowRecord, error) { return flow, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return nil, nil
		},
		ReserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flow, func() { released = true }, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{state: "running"}, nil
		},
	})
	m.launchBackend = "tmux"
	m.tmuxLaunchAvailable = func() bool { return true }
	m.repoTmuxLaunchWindowLive = func(repoPath string, launchIDs ...string) bool {
		probes++
		if repoPath != initial.RepoPath || len(launchIDs) != 1 {
			t.Fatalf("tmux probe = %q %#v", repoPath, launchIDs)
		}
		return launchIDs[0] == refreshed.LaunchID
	}
	m.launchSeams.NewLaunchID = func() string { return "token" }
	m, cmd := m.routeSavedSessionResume(initial, flowLaunchOriginSessionsPane)
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd != nil || reads != 2 || probes != 2 || started || !released || m.status.Text != tmuxSessionLiveWindowRefusal {
		t.Fatalf("final tmux recheck: cmd=%v reads=%d probes=%d started=%v released=%v status=%q", cmd != nil, reads, probes, started, released, m.status.Text)
	}
	if m.flowLaunchAttemptOccupied("flow-1") || len(m.flowLaunchSessionOwners) != 0 {
		t.Fatal("final tmux refusal retained lifecycle ownership")
	}
}

func TestSavedSessionResumeEmbeddedStartFailureNeverFallsBackExternal(t *testing.T) {
	session := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", FlowID: "flow-1",
		WorktreePath: "/repo/worktree",
	}
	flow := flowstore.FlowRecord{FlowID: "flow-1", Status: flowstore.StatusInProgress}
	externalCalls := 0
	m := savedSessionLifecycleModel(Options{
		ReadSession: func(sessions.Provider, string) (sessions.SessionRecord, error) { return session, nil },
		ReadFlow:    func(string) (flowstore.FlowRecord, error) { return flow, nil },
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return nil, nil
		},
		ReserveFlowLaunch: func(string) (flowstore.FlowRecord, func(), error) {
			return flow, func() {}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			return nil, errors.New("embedded runtime unavailable")
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			externalCalls++
			return actions.TerminalLaunchSpec{}, nil
		},
	})
	m.launchSeams.NewLaunchID = func() string { return "token" }
	m, cmd := m.routeSavedSessionResume(session, flowLaunchOriginSessionsPane)
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	m, cmd = m.handleFlowLaunchEvent(cmd().(flowLaunchEventMsg))
	if cmd != nil || externalCalls != 0 || !strings.Contains(m.status.Text, "embedded runtime unavailable") {
		t.Fatalf("embedded-only failure: cmd=%v external=%d status=%q", cmd != nil, externalCalls, m.status.Text)
	}
}

func TestSavedSessionDestinationRetainedSlotRefusesTransfer(t *testing.T) {
	key := flowLaunchSavedSessionKey{Provider: sessions.ProviderCodex, SessionID: "session-1"}
	m := attemptTestModel()
	m, _ = m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token: "token", Kind: flowLaunchKindSavedSessionResume, FlowID: "flow-a", SessionKey: key,
	}, flowLaunchStateReadingSession)
	m.embeddedTerminals = []embeddedTerminalSlot{{
		Scope: embeddedTerminalScopeFlow, FlowID: "flow-b", PrefillPending: true,
		Terminal: internalFakeEmbeddedTerminal{state: "exited"}, FlowSavedSessionResume: true,
	}}
	if _, ok := m.transferSavedSessionFlowLaunchAttempt("flow-a", "token", key, "flow-b"); ok {
		t.Fatal("prefill-pending exited retained slot must occupy destination Flow")
	}
}
