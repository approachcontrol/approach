package model

import (
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func repairClassificationRecord(phases ...flowstore.FlowPhase) flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:       "flow-repair",
		Title:        "Repair me",
		RepoPath:     "/dev/approach",
		WorktreePath: "/dev/approach-worktrees/flow-repair",
		Branch:       "flow/repair",
		Status:       flowstore.StatusInProgress,
		Phases:       phases,
	}
}

func TestFlowRepairObstructionClassifiesStalledAndHealthyFlows(t *testing.T) {
	liveSession := flowstore.Session{SessionID: "live-session", LaunchID: "launch-1", Status: "last_seen"}
	endedSession := flowstore.Session{SessionID: "ended-session", LaunchID: "launch-1", Status: "ended"}
	validPR := flowstore.PullRequest{
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/repair",
		BaseBranch: "main",
	}

	tests := []struct {
		name       string
		record     flowstore.FlowRecord
		want       bool
		wantReason string
	}{
		{
			name:       "blocked",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseBlocked}),
			want:       true,
			wantReason: "blocked",
		},
		{
			name:       "needs attention",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseNeedsAttention}),
			want:       true,
			wantReason: "needs_attention",
		},
		{
			name:   "blocked phase with live session",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseBlocked, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{liveSession}}),
		},
		{
			name:   "needs attention phase with live session",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseNeedsAttention, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{liveSession}}),
		},
		{
			name: "missing PR metadata",
			record: repairClassificationRecord(
				flowstore.FlowPhase{PhaseID: "pr-creation", Kind: flowstore.KindPRCreation, Status: flowstore.PhaseCompleted},
				flowstore.FlowPhase{PhaseID: "autoreview", Kind: flowstore.KindAutoreview, Status: flowstore.PhasePending, DependsOn: []string{"pr-creation"}},
			),
			want:       true,
			wantReason: "missing PR",
		},
		{
			name:       "await session",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}}),
			want:       true,
			wantReason: flowstore.PhaseResetReasonAwaitSession,
		},
		{
			name:       "ended session",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{endedSession}}),
			want:       true,
			wantReason: flowstore.PhaseResetReasonEndedSession,
		},
		{
			name:       "session mismatch",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{{SessionID: "session-1", LaunchID: "other", Status: "ended"}}}),
			want:       true,
			wantReason: "session-mismatch",
		},
		{
			name:       "missing session ID",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{{LaunchID: "launch-1", Status: "last_seen"}}}),
			want:       true,
			wantReason: "missing-session-id",
		},
		{
			name:       "inconsistent running without launch",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning}),
			want:       true,
			wantReason: "no launch",
		},
		{
			name:       "otherwise gated graph",
			record:     repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhasePending, DependsOn: []string{"missing"}}),
			want:       true,
			wantReason: "no phase is launchable",
		},
		{
			name:   "ready phase",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseReady, DependsOn: []string{}}),
		},
		{
			name:   "healthy running session",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseRunning, LaunchIDs: []string{"launch-1"}, Sessions: []flowstore.Session{liveSession}}),
		},
		{
			name:   "completed",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseCompleted}),
		},
		{
			name: "merged",
			record: func() flowstore.FlowRecord {
				record := repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseBlocked})
				record.Merge.Status = flowstore.MergeMerged
				return record
			}(),
		},
		{
			name: "abandoned",
			record: func() flowstore.FlowRecord {
				record := repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseBlocked})
				record.Status = flowstore.StatusAbandoned
				return record
			}(),
		},
		{
			name:   "ready Merge boundary",
			record: repairClassificationRecord(flowstore.FlowPhase{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseReady, DependsOn: []string{}}),
		},
		{
			name: "manually mergeable",
			record: func() flowstore.FlowRecord {
				record := repairClassificationRecord(flowstore.FlowPhase{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhaseCompleted, DependsOn: []string{}})
				record.PR = validPR
				return record
			}(),
		},
		{
			name: "retryable autoreview remains manually launchable",
			record: func() flowstore.FlowRecord {
				record := repairClassificationRecord(flowstore.FlowPhase{PhaseID: "autoreview", Kind: flowstore.KindAutoreview, Status: flowstore.PhaseBlocked, DependsOn: []string{}})
				record.PR = validPR
				return record
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obstruction, got := flowRepairObstructionForRecord(tt.record)
			if got != tt.want {
				t.Fatalf("flowRepairObstructionForRecord() available = %v, want %v; obstruction=%#v", got, tt.want, obstruction)
			}
			if tt.wantReason != "" && !strings.Contains(obstruction.Description, tt.wantReason) {
				t.Fatalf("obstruction description = %q, want containing %q", obstruction.Description, tt.wantReason)
			}
		})
	}
}

func TestSelectedFlowRepairReadyUsesBothFlowSurfacesAndTerminalOccupancy(t *testing.T) {
	record := repairClassificationRecord(flowstore.FlowPhase{PhaseID: "implementation", Status: flowstore.PhaseBlocked})
	newModel := func(mode ui.Mode) Model {
		m := NewWithOptions([]scanner.Repo{{Path: "/dev/approach", DisplayName: "approach"}}, Options{StartupMode: mode})
		m.activePane = 1
		if mode == ui.ModeActiveFlows {
			m.activeFlows = m.activeFlows.SetItems([]flowstore.FlowRecord{record})
			m.expandedActiveFlowID = record.FlowID
			m.selectedActiveFlowPhaseID = "implementation"
		} else {
			m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
			m.expandedFlowID = record.FlowID
			m.selectedFlowPhaseID = "implementation"
		}
		return m
	}

	for _, mode := range []ui.Mode{ui.ModeFlows, ui.ModeActiveFlows} {
		m := newModel(mode)
		if !m.selectedFlowRepairReady() {
			t.Fatalf("selectedFlowRepairReady() = false in mode %v with expanded phase selected", mode)
		}
		for _, state := range []string{"starting", "running", "exited", "failed", "terminated"} {
			occupied := m
			occupied.embeddedTerminals = []embeddedTerminalSlot{{
				Scope:          embeddedTerminalScopeFlow,
				FlowID:         record.FlowID,
				Terminal:       &internalFakeEmbeddedTerminal{state: state},
				PrefillPending: state == "starting",
			}}
			if occupied.selectedFlowRepairReady() {
				t.Fatalf("selectedFlowRepairReady() = true in mode %v with retained %s Flow terminal", mode, state)
			}
		}
		// A detached/dismissed terminal has no slot and therefore no occupancy.
		if !m.selectedFlowRepairReady() {
			t.Fatalf("selectedFlowRepairReady() = false in mode %v after terminal removal", mode)
		}
	}
}

func TestFlowPhaseResumePersistenceDoesNotOpenAlongsideRepairTerminal(t *testing.T) {
	const (
		flowID   = "flow-1"
		phaseID  = "implementation"
		launchID = "resume-launch"
	)
	key := flowPhaseResumeKey{FlowID: flowID, PhaseID: phaseID}
	starts := 0
	var failureUpdate flowstore.PhaseUpdate
	m := Model{
		pendingFlowPhaseResumes: map[flowPhaseResumeKey]string{key: launchID},
		embeddedTerminals: []embeddedTerminalSlot{{
			Scope:      embeddedTerminalScopeFlow,
			FlowID:     flowID,
			FlowRepair: true,
			Terminal:   internalFakeEmbeddedTerminal{state: "running"},
		}},
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			starts++
			return internalFakeEmbeddedTerminal{state: "running"}, nil
		},
		setFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			failureUpdate = update
			return flowstore.FlowRecord{}, nil
		},
	}
	ctx := actions.AgentLaunchContext{
		Command:           "codex",
		LaunchID:          launchID,
		FlowID:            flowID,
		FlowPhaseID:       phaseID,
		ResumeSessionID:   "codex-session",
		FlowLaunchTracked: true,
		Embedded:          true,
	}
	next, cmd := m.handleFlowPhaseResumePersisted(flowPhaseResumePersistedMsg{
		LaunchContext: ctx,
		Flow: flowstore.FlowRecord{FlowID: flowID, Phases: []flowstore.FlowPhase{{
			PhaseID: phaseID,
			Status:  flowstore.PhaseRunning,
		}}},
	})
	if starts != 0 || len(next.embeddedTerminals) != 1 {
		t.Fatalf("resume opened alongside repair: starts=%d terminals=%d", starts, len(next.embeddedTerminals))
	}
	if cmd == nil {
		t.Fatal("occupied resume should persist launch-failure state")
	}
	_ = cmd()
	if failureUpdate.Status != flowstore.PhaseNeedsAttention || !strings.Contains(failureUpdate.Notes, "repair terminal") {
		t.Fatalf("failure update = %#v, want repair-terminal needs_attention guidance", failureUpdate)
	}
}
