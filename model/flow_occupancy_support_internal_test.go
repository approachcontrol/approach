package model

import (
	"errors"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
)

// This file is the harness for approach-x0r.2's characterization suite. The
// suite pins today's Flow occupancy behavior — including the parts that look
// wrong — so every later slice of approach-x0r is provably behavior-preserving.
// docs/flow-occupancy-matrix.md is the frozen survey these tests are keyed to;
// its S-numbers appear throughout.
//
// The one design constraint that matters: the acceptance criterion the suite is
// most likely to miss is "simultaneous sources, not one at a time". Every source
// therefore has exactly one installer, so a case naming three sources composes
// three installers in one line and costs no more to write than a single-source
// case.

// occupancySource names one of the survey's representations of "something is
// already working in this Flow". The attempt sources are split by kind because
// the ordered ladders (§4.1, §4.3) rank on the kind, not on the attempt.
type occupancySource int

const (
	srcLeaseHeld occupancySource = iota + 1
	srcLeaseError
	srcAttemptManualPhase
	srcAttemptAutoPhase
	srcAttemptPhaseResume
	srcAttemptRepair
	srcAttemptAutofix
	srcAttemptWorktreeAgent
	srcFlowTerminal
	srcRepairSlot
	srcRunningPhase
	srcPhaseSession
	srcSessionMirror
	srcStoreSession
	srcHeadlessWrite
	srcTmuxAutofixWindow
	srcDrainMarker
)

func (source occupancySource) String() string {
	switch source {
	case srcLeaseHeld:
		return "leaseHeld"
	case srcLeaseError:
		return "leaseError"
	case srcAttemptManualPhase:
		return "attemptManualPhase"
	case srcAttemptAutoPhase:
		return "attemptAutoPhase"
	case srcAttemptPhaseResume:
		return "attemptPhaseResume"
	case srcAttemptRepair:
		return "attemptRepair"
	case srcAttemptAutofix:
		return "attemptAutofix"
	case srcAttemptWorktreeAgent:
		return "attemptWorktreeAgent"
	case srcFlowTerminal:
		return "flowTerminal"
	case srcRepairSlot:
		return "repairSlot"
	case srcRunningPhase:
		return "runningPhase"
	case srcPhaseSession:
		return "phaseSession"
	case srcSessionMirror:
		return "sessionMirror"
	case srcStoreSession:
		return "storeSession"
	case srcHeadlessWrite:
		return "headlessWrite"
	case srcTmuxAutofixWindow:
		return "tmuxAutofixWindow"
	case srcDrainMarker:
		return "drainMarker"
	default:
		return "unknown"
	}
}

// The fixed identifiers the installers write. Tests assert against these rather
// than re-deriving them, so a case that installs a source and a case that
// asserts on it cannot drift apart.
const (
	occupancyLeaseErrText      = "flow-leases directory is unsafe"
	occupancyPhaseLaunchID     = "phase-launch-1"
	occupancyStoreLaunchID     = "store-launch-1"
	occupancyAutofixTmuxLaunch = "autofix-tmux-1"
	occupancySessionProvider   = "codex"
	occupancyPhaseSessionID    = "phase-session-1"
	occupancyStoreSessionID    = "store-session-1"
)

func occupancyLeaseErr() error {
	return errors.New(occupancyLeaseErrText)
}

// occupancyFixture holds everything a case needs to interrogate one Flow: the
// Model with the sources installed, the record the sources were installed
// against, and the authoritative session listing the store-scoped predicates
// take as an argument rather than reading from the Model.
type occupancyFixture struct {
	t *testing.T
	h *manualLaunchHarness

	m      Model
	record flowstore.FlowRecord
	stored []sessions.SessionRecord
}

// occupancyFlowRecord is the launchable Flow: one ready implementation phase,
// which is what `g` and the manual admission need. It is deliberately not
// repairable — a Flow with a launchable phase never is (flow_repair.go:43-47) —
// so repair cases use occupancyRepairFlowRecord instead.
func occupancyFlowRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Flow one",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha",
		Branch:       "flow/one",
		Commit:       "abc123",
		Status:       flowstore.StatusInProgress,
		UpdatedAt:    time.Now(),
		Phases: []flowstore.FlowPhase{{
			PhaseID: "implementation",
			Title:   "Implementation",
			Kind:    flowstore.KindImplementation,
			Status:  flowstore.PhaseReady,
			Order:   1,
		}},
	}
}

// occupancyRepairFlowRecord is the repairable Flow: a blocked phase with no
// launchable successor, which is the shape flowRepairObstructionForRecord
// admits.
func occupancyRepairFlowRecord() flowstore.FlowRecord {
	record := occupancyFlowRecord()
	record.Phases = []flowstore.FlowPhase{{
		PhaseID: "implementation",
		Title:   "Implementation",
		Kind:    flowstore.KindImplementation,
		Status:  flowstore.PhaseBlocked,
		Outcome: flowstore.OutcomeBlocked,
		Notes:   "persisted metadata is inconsistent",
		Order:   1,
	}}
	return record
}

func newOccupancyFixture(t *testing.T, sources ...occupancySource) occupancyFixture {
	t.Helper()
	return newOccupancyFixtureFor(t, occupancyFlowRecord(), sources...)
}

// newOccupancyFixtureFor installs every named source against one record. The
// two-stage shape is forced by the Model's construction: the tmux launch
// backend is read out of Options when the Model is built, while every other
// source is either a closure the seam calls per probe or a plain Model field.
//
// The record is selected in the Flows pane because cachedFlowRecord and
// cachedGenericWorktreeAgentRecord both resolve through the selection.
func newOccupancyFixtureFor(t *testing.T, record flowstore.FlowRecord, sources ...occupancySource) occupancyFixture {
	t.Helper()
	f := occupancyFixture{t: t, record: record}
	f.h = newManualLaunchHarness(t, record)
	// A usable inspector reporting Free is the unoccupied baseline; the lease
	// sources below replace its answer rather than its existence.
	f.h.leaseState = flowlease.Free

	for _, source := range sources {
		f.configure(source)
	}
	// The record may have been rewritten by a record-shaped source, and both the
	// pane and the read seam have to see the same one.
	f.h.record = f.record
	f.m = f.h.modelWith([]scanner.Repo{{Path: f.record.RepoPath, DisplayName: "alpha"}}, f.h.options())
	for _, source := range sources {
		f.m = f.install(source, f.m)
	}
	f.h.sessionRecords = f.stored
	return f
}

// configure applies the half of a source that has to exist before the Model is
// built, and rewrites the record for the sources that are properties of it.
func (f *occupancyFixture) configure(source occupancySource) {
	switch source {
	case srcLeaseHeld:
		f.h.leaseState = flowlease.Held
	case srcLeaseError:
		f.h.leaseErr = occupancyLeaseErr()
	case srcRunningPhase:
		f.record = withOccupancyRunningPhase(f.record)
	case srcPhaseSession:
		f.record = withOccupancyPhaseSession(f.record)
	case srcStoreSession:
		f.record = withOccupancyStoreLaunchID(f.record)
		f.stored = append(f.stored, occupancyStoreSessionRecord(f.record.FlowID))
	case srcTmuxAutofixWindow:
		f.h.launchBackend = config.LaunchBackendTmux
		f.h.tmuxAvailable = true
		f.h.tmuxWindowLiveLaunchID = occupancyAutofixTmuxLaunch
	}
}

// install applies the Model-shaped half of a source. Every installer reuses the
// production writer for its state — reserveFlowLaunchAttempt rather than a raw
// map write, withFlowAutofixTmuxLaunch rather than a registry poke — so a case
// can never install a state the code itself cannot produce.
func (f occupancyFixture) install(source occupancySource, m Model) Model {
	f.t.Helper()
	switch source {
	case srcAttemptManualPhase:
		return f.reserve(m, flowLaunchKindManualPhase)
	case srcAttemptAutoPhase:
		return f.reserve(m, flowLaunchKindAutoPhase)
	case srcAttemptPhaseResume:
		return f.reserve(m, flowLaunchKindPhaseResume)
	case srcAttemptRepair:
		return f.reserve(m, flowLaunchKindRepair)
	case srcAttemptAutofix:
		return f.reserve(m, flowLaunchKindAutofix)
	case srcAttemptWorktreeAgent:
		return f.reserve(m, flowLaunchKindWorktreeAgent)
	case srcFlowTerminal:
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Scope:    embeddedTerminalScopeFlow,
			FlowID:   f.record.FlowID,
			Terminal: flowPhaseLaunchTestTerminal{state: "running"},
		})
		return m
	case srcRepairSlot:
		// Terminal stays nil on purpose: S6 and S7 overlap rather than nest, and
		// a terminal-less repair slot is the state that tells them apart.
		m.embeddedTerminals = append(m.embeddedTerminals, embeddedTerminalSlot{
			Scope:      embeddedTerminalScopeFlow,
			FlowID:     f.record.FlowID,
			FlowRepair: true,
		})
		return m
	case srcSessionMirror:
		return m.withOccupancySessionMirror(f.record.FlowID)
	case srcHeadlessWrite:
		return m.markFlowHeadlessWritePending(pendingFlowHeadlessWrite{
			flowID:   f.record.FlowID,
			repoPath: f.record.RepoPath,
			enabled:  true,
		})
	case srcTmuxAutofixWindow:
		return m.withFlowAutofixTmuxLaunch(f.record.FlowID, occupancyAutofixTmuxLaunch)
	case srcDrainMarker:
		return m.setRepairAutoDrainMarker(f.record.FlowID, true)
	}
	return m
}

func (f occupancyFixture) reserve(m Model, kind flowLaunchKind) Model {
	f.t.Helper()
	next, ok := m.reserveFlowLaunchAttempt(flowLaunchAttempt{
		Token:   "occupancy-token",
		Kind:    kind,
		FlowID:  f.record.FlowID,
		PhaseID: f.record.Phases[0].PhaseID,
	}, flowLaunchStateReading)
	if !ok {
		f.t.Fatalf("reserving a %v attempt should succeed on an unreserved Flow", kind)
	}
	return next
}

// phase is the record's first phase, which is the one every phase-scoped
// installer writes to.
func (f occupancyFixture) phase() flowstore.FlowPhase {
	return f.record.Phases[0]
}

// flowID is the exact persisted ID the installers keyed on.
func (f occupancyFixture) flowID() string {
	return f.record.FlowID
}

func withOccupancyRunningPhase(record flowstore.FlowRecord) flowstore.FlowRecord {
	phases := append([]flowstore.FlowPhase(nil), record.Phases...)
	phases[0].Status = flowstore.PhaseRunning
	record.Phases = phases
	return record
}

// withOccupancyPhaseSession installs S11: a live session in the phase's own
// mirror whose launch ID the phase owns. Both halves are required — an
// unmatched launch ID is skipped by phaseHasMatchingLiveSessionExcept.
func withOccupancyPhaseSession(record flowstore.FlowRecord) flowstore.FlowRecord {
	phases := append([]flowstore.FlowPhase(nil), record.Phases...)
	phases[0].LaunchIDs = append(append([]string(nil), phases[0].LaunchIDs...), occupancyPhaseLaunchID)
	phases[0].Sessions = append(append([]flowstore.Session(nil), phases[0].Sessions...), flowstore.Session{
		Provider:  occupancySessionProvider,
		SessionID: occupancyPhaseSessionID,
		LaunchID:  occupancyPhaseLaunchID,
		Status:    "active",
	})
	record.Phases = phases
	return record
}

// withOccupancyStoreLaunchID gives the phase the launch ID the store-listing
// source's session claims, which is what scopes S13 to this phase.
func withOccupancyStoreLaunchID(record flowstore.FlowRecord) flowstore.FlowRecord {
	phases := append([]flowstore.FlowPhase(nil), record.Phases...)
	phases[0].LaunchIDs = append(append([]string(nil), phases[0].LaunchIDs...), occupancyStoreLaunchID)
	record.Phases = phases
	return record
}

func occupancyStoreSessionRecord(flowID string) sessions.SessionRecord {
	return sessions.SessionRecord{
		Provider:  occupancySessionProvider,
		SessionID: occupancyStoreSessionID,
		LaunchID:  occupancyStoreLaunchID,
		FlowID:    flowID,
		Status:    "active",
	}
}

// withOccupancySessionMirror installs S12, the display mirror the worktree
// agent is the only family to read.
func (m Model) withOccupancySessionMirror(flowID string) Model {
	m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{
		Provider:  occupancySessionProvider,
		SessionID: "mirror-session-1",
		FlowID:    flowID,
		Status:    "active",
	}})
	return m
}

// TestOccupancyFixtureInstallersDriveTheirOwnLeaf proves every installer in
// isolation. Without this, a later table asserting "three sources at once" could
// pass because one of the three never installed anything.
func TestOccupancyFixtureInstallersDriveTheirOwnLeaf(t *testing.T) {
	tests := []struct {
		source occupancySource
		// leaf is the single predicate this installer exists to flip.
		leaf func(occupancyFixture) bool
	}{
		{srcLeaseHeld, func(f occupancyFixture) bool {
			occupied, err := f.m.trackedFlowLeaseOccupied(f.flowID())
			return occupied && err == nil
		}},
		{srcLeaseError, func(f occupancyFixture) bool {
			_, err := f.m.trackedFlowLeaseOccupied(f.flowID())
			return err != nil
		}},
		{srcAttemptManualPhase, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindManualPhase
		}},
		{srcAttemptAutoPhase, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindAutoPhase
		}},
		{srcAttemptPhaseResume, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindPhaseResume
		}},
		{srcAttemptRepair, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindRepair
		}},
		{srcAttemptAutofix, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindAutofix
		}},
		{srcAttemptWorktreeAgent, func(f occupancyFixture) bool {
			return f.m.flowLaunchAttemptKind(f.flowID()) == flowLaunchKindWorktreeAgent
		}},
		{srcFlowTerminal, func(f occupancyFixture) bool {
			return f.m.hasFlowEmbeddedTerminalForFlow(f.flowID())
		}},
		{srcRepairSlot, func(f occupancyFixture) bool {
			return f.m.hasFlowRepairEmbeddedTerminalForFlow(f.flowID())
		}},
		{srcRunningPhase, func(f occupancyFixture) bool {
			return f.phase().Status == flowstore.PhaseRunning
		}},
		{srcPhaseSession, func(f occupancyFixture) bool {
			return phaseHasMatchingLiveSession(f.phase())
		}},
		{srcSessionMirror, func(f occupancyFixture) bool {
			return f.m.hasKnownActiveFlowSession(f.flowID())
		}},
		{srcStoreSession, func(f occupancyFixture) bool {
			return flowLaunchPhaseSessionOccupied(f.phase(), f.stored)
		}},
		{srcHeadlessWrite, func(f occupancyFixture) bool {
			return f.m.flowHeadlessWritePending(f.flowID())
		}},
		{srcTmuxAutofixWindow, func(f occupancyFixture) bool {
			return f.m.tmuxAutofixAgentStillRunning(f.record, f.record.RepoPath)
		}},
		{srcDrainMarker, func(f occupancyFixture) bool {
			return f.m.hasPendingRepairAutoDrainMarker(f.flowID())
		}},
	}
	for _, tc := range tests {
		t.Run(tc.source.String(), func(t *testing.T) {
			if tc.leaf(newOccupancyFixture(t)) {
				t.Fatal("the baseline fixture must not already satisfy this leaf")
			}
			if !tc.leaf(newOccupancyFixture(t, tc.source)) {
				t.Fatalf("installing %v must flip its own leaf predicate", tc.source)
			}
		})
	}
}

// TestOccupancyFixtureComposesSimultaneousSources is the harness's stopping
// condition: the five-source state the bead calls out has to cost one line.
func TestOccupancyFixtureComposesSimultaneousSources(t *testing.T) {
	f := newOccupancyFixture(t, srcLeaseHeld, srcAttemptManualPhase, srcFlowTerminal, srcRunningPhase, srcPhaseSession)

	if occupied, err := f.m.trackedFlowLeaseOccupied(f.flowID()); err != nil || !occupied {
		t.Fatalf("lease occupied = %v, err = %v, want true and nil", occupied, err)
	}
	if !f.m.flowLaunchAttemptOccupied(f.flowID()) {
		t.Fatal("the attempt map should hold the Flow")
	}
	if !f.m.hasFlowEmbeddedTerminalForFlow(f.flowID()) {
		t.Fatal("the Flow terminal slot should be installed")
	}
	if f.phase().Status != flowstore.PhaseRunning {
		t.Fatal("the record's phase should be running")
	}
	if !phaseHasMatchingLiveSession(f.phase()) {
		t.Fatal("the phase should carry a live mirrored session")
	}
}
