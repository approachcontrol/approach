package model

import (
	"fmt"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// The S11 ∪ S13 phase-session rule and the four scopes consumers apply it at.
// Both halves are scoped identically — only sessions whose launch ID belongs to
// the phase count — because a wider rule would let one crashed agent make the
// Flow permanently unlaunchable.

// occupancySessionPhase builds the phase these tables run against: it owns one
// launch ID and nothing else.
func occupancySessionPhase() flowstore.FlowPhase {
	return flowstore.FlowPhase{
		PhaseID:   "implementation",
		Title:     "Implementation",
		Status:    flowstore.PhaseReady,
		Order:     1,
		LaunchIDs: []string{occupancyPhaseLaunchID},
	}
}

func mirroredSession(provider, sessionID, launchID, status string) flowstore.Session {
	return flowstore.Session{Provider: provider, SessionID: sessionID, LaunchID: launchID, Status: status}
}

func storedSession(provider, sessionID, launchID, status string) sessions.SessionRecord {
	return sessions.SessionRecord{
		Provider:  sessions.Provider(provider),
		SessionID: sessionID,
		LaunchID:  launchID,
		Status:    status,
	}
}

// TestFlowLaunchPhaseSessionOccupiedUnionRule runs one table across both halves
// of the union: every row is asserted with the session in the phase mirror only,
// in the store listing only, and in both. A rule that stopped reading one half
// would fail exactly one of those three.
func TestFlowLaunchPhaseSessionOccupiedUnionRule(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		sessionID string
		launchID  string
		status    string
		want      bool
	}{
		{
			name:      "live session on an owned launch",
			provider:  occupancySessionProvider,
			sessionID: occupancyPhaseSessionID,
			launchID:  occupancyPhaseLaunchID,
			status:    "active",
			want:      true,
		},
		{
			// Launch IDs are trimmed on both sides before the set lookup.
			name:      "the launch ID is trimmed on the session",
			provider:  occupancySessionProvider,
			sessionID: occupancyPhaseSessionID,
			launchID:  "  " + occupancyPhaseLaunchID + "  ",
			status:    "active",
			want:      true,
		},
		{
			name:      "a launch the phase does not own is not occupancy",
			provider:  occupancySessionProvider,
			sessionID: occupancyPhaseSessionID,
			launchID:  "someone-elses-launch",
			status:    "active",
		},
		{
			name:      "an unattributed session is not occupancy",
			provider:  occupancySessionProvider,
			sessionID: occupancyPhaseSessionID,
			launchID:  "",
			status:    "active",
		},
		{
			// The ID-less session is skipped before the launch lookup, so this
			// is an absence test rather than a comparison.
			name:      "a session with no ID is not occupancy",
			provider:  occupancySessionProvider,
			sessionID: "   ",
			launchID:  occupancyPhaseLaunchID,
			status:    "active",
		},
		{
			name:      "an ended session is not occupancy",
			provider:  occupancySessionProvider,
			sessionID: occupancyPhaseSessionID,
			launchID:  occupancyPhaseLaunchID,
			status:    "ended",
		},
		{
			// Provider is not part of the occupancy rule at all; only the
			// resume exemption reads it.
			name:      "a foreign provider on an owned launch still occupies",
			provider:  "claude",
			sessionID: occupancyPhaseSessionID,
			launchID:  occupancyPhaseLaunchID,
			status:    "active",
			want:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mirror := occupancySessionPhase()
			mirror.Sessions = []flowstore.Session{mirroredSession(tc.provider, tc.sessionID, tc.launchID, tc.status)}
			store := []sessions.SessionRecord{storedSession(tc.provider, tc.sessionID, tc.launchID, tc.status)}

			if got := phaseHasMatchingLiveSession(mirror); got != tc.want {
				t.Fatalf("phaseHasMatchingLiveSession (mirror half) = %v, want %v", got, tc.want)
			}
			if got := flowLaunchPhaseSessionOccupied(mirror, nil); got != tc.want {
				t.Fatalf("occupied (mirror only) = %v, want %v", got, tc.want)
			}
			if got := flowLaunchPhaseSessionOccupied(occupancySessionPhase(), store); got != tc.want {
				t.Fatalf("occupied (store only) = %v, want %v", got, tc.want)
			}
			if got := flowLaunchPhaseSessionOccupied(mirror, store); got != tc.want {
				t.Fatalf("occupied (both halves) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFlowLaunchPhaseSessionOccupiedIgnoresABlankOwnedLaunch pins that a phase
// whose launch list is entirely whitespace owns nothing, so no session can be
// attributed to it.
func TestFlowLaunchPhaseSessionOccupiedIgnoresABlankOwnedLaunch(t *testing.T) {
	phase := occupancySessionPhase()
	phase.LaunchIDs = []string{"   ", ""}
	phase.Sessions = []flowstore.Session{mirroredSession(occupancySessionProvider, occupancyPhaseSessionID, "", "active")}
	store := []sessions.SessionRecord{storedSession(occupancySessionProvider, occupancyStoreSessionID, "  ", "active")}

	if flowLaunchPhaseSessionOccupied(phase, store) {
		t.Fatal("a phase that owns no launch cannot be occupied by a session")
	}
}

// TestFlowSessionIdentityMatches pins the exemption's comparison rule: providers
// compare normalized, session IDs compare byte-exact, and the zero identity —
// which every caller but resume passes — matches nothing.
func TestFlowSessionIdentityMatches(t *testing.T) {
	target := flowSessionIdentity{Provider: occupancySessionProvider, SessionID: occupancyPhaseSessionID}
	tests := []struct {
		name      string
		id        flowSessionIdentity
		provider  string
		sessionID string
		want      bool
	}{
		{name: "exact pair", id: target, provider: occupancySessionProvider, sessionID: occupancyPhaseSessionID, want: true},
		{
			name: "provider spelled differently still matches", id: target,
			provider: "Codex", sessionID: occupancyPhaseSessionID, want: true,
		},
		{
			// Two providers can hand out the same session ID, so exempting by ID
			// alone would exempt a live competing agent.
			name: "same session ID from another provider does not match", id: target,
			provider: "claude", sessionID: occupancyPhaseSessionID,
		},
		{
			// Session IDs are the identity both stores enforce byte-exact, so
			// IDs differing only by whitespace are two distinct agents.
			name: "session ID differing by whitespace does not match", id: target,
			provider: occupancySessionProvider, sessionID: " " + occupancyPhaseSessionID,
		},
		{
			name: "the zero identity matches nothing", id: flowSessionIdentity{},
			provider: occupancySessionProvider, sessionID: occupancyPhaseSessionID,
		},
		{
			name: "an identity with a whitespace-only ID matches nothing",
			id:   flowSessionIdentity{Provider: occupancySessionProvider, SessionID: "  "},
			// The same whitespace the identity carries, so only the blank-ID
			// guard can be what refuses the match.
			provider: occupancySessionProvider, sessionID: "  ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.matches(tc.provider, tc.sessionID); got != tc.want {
				t.Fatalf("matches(%q, %q) = %v, want %v", tc.provider, tc.sessionID, got, tc.want)
			}
		})
	}
}

// TestPhaseSessionOccupancyExemptionAppliesToBothHalves is D6. Resume is the
// only caller that passes an identity, and the target session appears in the
// store listing as well as in the phase's own mirror, so the exemption has to
// reach both or resume would refuse itself.
func TestPhaseSessionOccupancyExemptionAppliesToBothHalves(t *testing.T) {
	tests := []struct {
		name string
		skip flowSessionIdentity
		// wantOccupied is what the rule reports with the target session live in
		// both halves and nothing else present.
		wantOccupied bool
	}{
		{
			name: "the exact target is exempt",
			skip: flowSessionIdentity{Provider: occupancySessionProvider, SessionID: occupancyPhaseSessionID},
		},
		{
			name: "a differently spelled provider is still the target",
			skip: flowSessionIdentity{Provider: "CODEX", SessionID: occupancyPhaseSessionID},
		},
		{
			name:         "the same session ID from another provider is not exempt",
			skip:         flowSessionIdentity{Provider: "claude", SessionID: occupancyPhaseSessionID},
			wantOccupied: true,
		},
		{
			name:         "a whitespace-differing session ID is not exempt",
			skip:         flowSessionIdentity{Provider: occupancySessionProvider, SessionID: occupancyPhaseSessionID + " "},
			wantOccupied: true,
		},
		{
			name:         "the zero identity exempts nothing",
			skip:         flowSessionIdentity{},
			wantOccupied: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase := occupancySessionPhase()
			phase.Sessions = []flowstore.Session{
				mirroredSession(occupancySessionProvider, occupancyPhaseSessionID, occupancyPhaseLaunchID, "active"),
			}
			store := []sessions.SessionRecord{
				storedSession(occupancySessionProvider, occupancyPhaseSessionID, occupancyPhaseLaunchID, "active"),
			}

			if got := phaseHasMatchingLiveSessionExcept(phase, tc.skip); got != tc.wantOccupied {
				t.Fatalf("mirror half = %v, want %v", got, tc.wantOccupied)
			}
			if got := flowLaunchPhaseSessionOccupiedExcept(occupancySessionPhase(), store, tc.skip); got != tc.wantOccupied {
				t.Fatalf("store half = %v, want %v", got, tc.wantOccupied)
			}
			if got := flowLaunchPhaseSessionOccupiedExcept(phase, store, tc.skip); got != tc.wantOccupied {
				t.Fatalf("union = %v, want %v", got, tc.wantOccupied)
			}
		})
	}
}

// TestPhaseSessionExemptionStillCountsACompetingSession pins the other half of
// D6: exempting resume's own target must not exempt a second live agent on the
// same phase.
func TestPhaseSessionExemptionStillCountsACompetingSession(t *testing.T) {
	skip := flowSessionIdentity{Provider: occupancySessionProvider, SessionID: occupancyPhaseSessionID}
	phase := occupancySessionPhase()
	phase.Sessions = []flowstore.Session{
		mirroredSession(occupancySessionProvider, occupancyPhaseSessionID, occupancyPhaseLaunchID, "active"),
		mirroredSession("claude", "competing-session", occupancyPhaseLaunchID, "active"),
	}
	if !phaseHasMatchingLiveSessionExcept(phase, skip) {
		t.Fatal("a competing live session on the same phase still occupies it")
	}

	store := []sessions.SessionRecord{
		storedSession(occupancySessionProvider, occupancyPhaseSessionID, occupancyPhaseLaunchID, "active"),
		storedSession("claude", "competing-session", occupancyPhaseLaunchID, "active"),
	}
	if !flowLaunchPhaseSessionOccupiedExcept(occupancySessionPhase(), store, skip) {
		t.Fatal("a competing live record in the store still occupies the phase")
	}
}

// TestFlowRepairPhaseSessionOccupiedReportsTheFirstOrderedPhase is repair's
// scope: Flow-wide, and iterated through flowstore.OrderedPhases, because the
// refusal names the phase and has to name the same one on every press.
//
// What "ordered" means here is worth pinning precisely, because it is not what
// the name suggests: OrderedPhases is stable over top-level phases and only
// groups children under their parent, so the Order field does not re-sort them.
// A Flow with two occupied top-level phases therefore reports the one declared
// first, whatever its Order says.
func TestFlowRepairPhaseSessionOccupiedReportsTheFirstOrderedPhase(t *testing.T) {
	occupied := func(phaseID, parentID string, order int) flowstore.FlowPhase {
		phase := flowstore.FlowPhase{
			PhaseID:       phaseID,
			ParentPhaseID: parentID,
			Status:        flowstore.PhaseReady,
			Order:         order,
			LaunchIDs:     []string{phaseID + "-launch"},
		}
		phase.Sessions = []flowstore.Session{
			mirroredSession(occupancySessionProvider, phaseID+"-session", phaseID+"-launch", "active"),
		}
		return phase
	}

	t.Run("top-level phases keep their declared order", func(t *testing.T) {
		record := occupancyFlowRecord()
		record.Phases = []flowstore.FlowPhase{
			occupied("review-loop", "", 3),
			occupied("implementation", "", 2),
			{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1},
		}
		phase, ok := flowRepairPhaseSessionOccupied(record, nil)
		if !ok {
			t.Fatal("a Flow with two session-occupied phases is occupied")
		}
		if phase.PhaseID != "review-loop" {
			t.Fatalf("phase = %q, want the first declared phase — Order does not re-sort top-level phases", phase.PhaseID)
		}
	})

	t.Run("a child is walked with its parent, not where it was declared", func(t *testing.T) {
		record := occupancyFlowRecord()
		// The child is declared last but belongs to the first phase, so
		// OrderedPhases lifts it ahead of the second top-level phase.
		record.Phases = []flowstore.FlowPhase{
			{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1},
			occupied("review-loop", "", 3),
			occupied("plan-review", "plan", 2),
		}
		phase, ok := flowRepairPhaseSessionOccupied(record, nil)
		if !ok {
			t.Fatal("a Flow with two session-occupied phases is occupied")
		}
		if phase.PhaseID != "plan-review" {
			t.Fatalf("phase = %q, want the child walked directly after its parent", phase.PhaseID)
		}
	})

	t.Run("no session-attached phase is not occupancy", func(t *testing.T) {
		if _, ok := flowRepairPhaseSessionOccupied(occupancyFlowRecord(), nil); ok {
			t.Fatal("a Flow with no session-attached phase is not occupied")
		}
	})
}

// TestFlowRepairPhaseSessionOccupiedUnionsTheStoreListing pins that repair's
// Flow-scoped rule carries the same union as the phase-scoped one: a live record
// the mirror never saw still occupies the Flow.
func TestFlowRepairPhaseSessionOccupiedUnionsTheStoreListing(t *testing.T) {
	record := occupancyFlowRecord()
	record.Phases = []flowstore.FlowPhase{{
		PhaseID:   "implementation",
		Status:    flowstore.PhaseReady,
		Order:     1,
		LaunchIDs: []string{occupancyStoreLaunchID},
	}}
	store := []sessions.SessionRecord{
		storedSession(occupancySessionProvider, occupancyStoreSessionID, occupancyStoreLaunchID, "active"),
	}

	if _, ok := flowRepairPhaseSessionOccupied(record, nil); ok {
		t.Fatal("the mirror alone shows nothing here")
	}
	phase, ok := flowRepairPhaseSessionOccupied(record, store)
	if !ok || phase.PhaseID != "implementation" {
		t.Fatalf("phase = %q, ok = %v, want the store listing to occupy the phase", phase.PhaseID, ok)
	}
}

// TestGenericFlowRuntimeOccupancyReasonOrdersSessionBeforeRunning pins two facts
// about the worktree agent's S10 ∨ S11 rung that a migration would silently
// change: within one phase the session reason wins over the running reason, and
// the loop walks record.Phases as declared rather than in phase order.
func TestGenericFlowRuntimeOccupancyReasonOrdersSessionBeforeRunning(t *testing.T) {
	sessionPhase := func(phaseID string, order int, status flowstore.PhaseStatus) flowstore.FlowPhase {
		phase := flowstore.FlowPhase{
			PhaseID:   phaseID,
			Status:    status,
			Order:     order,
			LaunchIDs: []string{phaseID + "-launch"},
		}
		phase.Sessions = []flowstore.Session{
			mirroredSession(occupancySessionProvider, phaseID+"-session", phaseID+"-launch", "active"),
		}
		return phase
	}

	t.Run("no occupancy reports nothing", func(t *testing.T) {
		if reason := genericFlowRuntimeOccupancyReason(occupancyFlowRecord()); reason != "" {
			t.Fatalf("reason = %q, want empty", reason)
		}
	})

	t.Run("a running phase is named", func(t *testing.T) {
		record := withOccupancyRunningPhase(occupancyFlowRecord())
		want := "a running phase implementation already occupies this Flow"
		if reason := genericFlowRuntimeOccupancyReason(record); reason != want {
			t.Fatalf("reason = %q, want %q", reason, want)
		}
	})

	t.Run("the session reason wins within one phase", func(t *testing.T) {
		record := occupancyFlowRecord()
		record.Phases = []flowstore.FlowPhase{sessionPhase("implementation", 1, flowstore.PhaseRunning)}
		want := "an active persisted session on phase implementation already occupies this Flow"
		if reason := genericFlowRuntimeOccupancyReason(record); reason != want {
			t.Fatalf("reason = %q, want %q", reason, want)
		}
	})

	t.Run("iteration follows the record's declared phase order", func(t *testing.T) {
		record := occupancyFlowRecord()
		// review-loop is declared first but ordered last. The rule walks the
		// slice, so it is the phase that gets named.
		record.Phases = []flowstore.FlowPhase{
			sessionPhase("review-loop", 3, flowstore.PhaseReady),
			sessionPhase("implementation", 1, flowstore.PhaseReady),
		}
		want := "an active persisted session on phase review-loop already occupies this Flow"
		if reason := genericFlowRuntimeOccupancyReason(record); reason != want {
			t.Fatalf("reason = %q, want %q — the loop is not order-normalized", reason, want)
		}
	})

	t.Run("a later phase's session outranks an earlier phase's running status", func(t *testing.T) {
		// Both reasons exist, in different phases. The earlier phase is reached
		// first, so its running reason is the one reported — the session-first
		// rule is per phase, not per record.
		record := occupancyFlowRecord()
		record.Phases = []flowstore.FlowPhase{
			{PhaseID: "implementation", Status: flowstore.PhaseRunning, Order: 1},
			sessionPhase("review-loop", 2, flowstore.PhaseReady),
		}
		want := fmt.Sprintf("a running phase %s already occupies this Flow", "implementation")
		if reason := genericFlowRuntimeOccupancyReason(record); reason != want {
			t.Fatalf("reason = %q, want %q", reason, want)
		}
	})
}

// TestActiveFlowSessionIsWholeFlowScoped pins S13's whole-Flow variant, which
// the worktree agent and create both read: it counts any live record in the
// listing, with no launch-ID attribution at all.
func TestActiveFlowSessionIsWholeFlowScoped(t *testing.T) {
	tests := []struct {
		name    string
		records []sessions.SessionRecord
		want    bool
	}{
		{name: "no records"},
		{
			name:    "a live record with no launch attribution still counts",
			records: []sessions.SessionRecord{storedSession(occupancySessionProvider, occupancyStoreSessionID, "", "active")},
			want:    true,
		},
		{
			name:    "an ended record does not count",
			records: []sessions.SessionRecord{storedSession(occupancySessionProvider, occupancyStoreSessionID, "", "ended")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := activeFlowSession(tc.records); got != tc.want {
				t.Fatalf("activeFlowSession = %v, want %v", got, tc.want)
			}
		})
	}
}
