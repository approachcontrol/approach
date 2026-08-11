package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

func TestFlowWorktreeAgentStartsFromParentFlowWithoutPhaseTracking(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), "flow ")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatalf("Mkdir(%q) error = %v", worktree, err)
	}
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/repo",
		WorktreePath: worktree,
		Branch:       "flow/one",
		Commit:       "abc123",
		PlanID:       "plan-1",
		PlanPath:     "/state/plan.md",
		Phases:       []flowstore.FlowPhase{{PhaseID: "implementation", Status: flowstore.PhaseCompleted}},
	}
	var filter sessions.SessionFilter
	var launched actions.AgentLaunchContext
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		AgentCommand: "codex",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{record}, nil
		},
		ListSessions: func(got sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			filter = got
			return nil, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			launched = ctx
			return internalFakeEmbeddedTerminal{}, nil
		},
	})
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activePane = ui.PaneBottom
	m.expandedFlowID = record.FlowID
	m.selectedFlowPhaseID = "implementation"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	next := nextModel.(Model)
	if cmd == nil {
		t.Fatal("s did not queue Flow worktree preflight")
	}
	if _, ok := next.flowLaunchLease(record.FlowID); !ok {
		t.Fatal("s did not synchronously acquire Flow lease")
	}
	msg := cmd()
	if filter.FlowID != record.FlowID {
		t.Fatalf("session filter FlowID = %q, want %q", filter.FlowID, record.FlowID)
	}
	nextModel, _ = next.Update(msg)
	next = nextModel.(Model)
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want 1", len(next.embeddedTerminals))
	}
	slot := next.embeddedTerminals[0]
	if !slot.FlowWorktreeAgent || !slot.FlowDetachBlocked || slot.FlowID != record.FlowID || slot.FlowPhaseID != "" || slot.FlowLaunchTracked || slot.FlowRepair {
		t.Fatalf("generic Flow slot metadata = %#v", slot)
	}
	if !launched.FlowWorktreeAgent || launched.FlowID != record.FlowID || launched.FlowPhaseID != "" || launched.FlowLaunchTracked || launched.FlowRepair || launched.InitialPrompt != "" || launched.WorktreePath != worktree {
		t.Fatalf("launch context = %#v", launched)
	}
	if _, ok := next.flowLaunchLease(record.FlowID); ok {
		t.Fatal("lease was not transferred to retained terminal")
	}
}

func TestFlowWorktreeAgentUsesRefreshedFlowMetadata(t *testing.T) {
	initial := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/repo",
		WorktreePath: t.TempDir(),
		Branch:       "flow/initial",
		Commit:       "initial",
	}
	updated := initial
	updated.WorktreePath = t.TempDir()
	updated.Branch = "flow/updated"
	updated.Commit = "updated"
	var launched actions.AgentLaunchContext
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		AgentCommand: "codex",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{updated}, nil
		},
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return nil, nil
		},
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			launched = ctx
			return internalFakeEmbeddedTerminal{}, nil
		},
	})
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{initial})
	m.activePane = ui.PaneBottom

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	next := nextModel.(Model)
	if cmd == nil {
		t.Fatal("s did not queue Flow worktree preflight")
	}
	nextModel, _ = next.Update(cmd())
	next = nextModel.(Model)
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want 1", len(next.embeddedTerminals))
	}
	if launched.WorktreePath != updated.WorktreePath || launched.Branch != updated.Branch || launched.Commit != updated.Commit {
		t.Fatalf("launch context = %#v, want final Flow metadata %#v", launched, updated)
	}
}

func TestFlowWorktreeAgentRejectsActivePersistedSessionAndReleasesLease(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/repo", WorktreePath: worktree}
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		AgentCommand: "claude",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{record}, nil
		},
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{{FlowID: record.FlowID, Status: "active"}}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			t.Fatal("active session must block terminal startup")
			return nil, nil
		},
	})
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activePane = ui.PaneBottom
	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	next := nextModel.(Model)
	nextModel, _ = next.Update(cmd())
	next = nextModel.(Model)
	if _, ok := next.flowLaunchLease(record.FlowID); ok {
		t.Fatal("rejected preflight retained lease")
	}
}

func TestFlowWorktreeAgentPreflightReadsSessionsAfterFlowRefresh(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/repo", WorktreePath: worktree}
	flowRefreshed := false
	started := false
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{
		AgentCommand: "claude",
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			flowRefreshed = true
			return []flowstore.FlowRecord{record}, nil
		},
		ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if !flowRefreshed {
				return nil, nil
			}
			return []sessions.SessionRecord{{FlowID: record.FlowID, Status: "active"}}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{}, nil
		},
	})
	m = modelWithModeForTest(m, ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activePane = ui.PaneBottom

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("worktree-agent launch did not queue preflight")
	}
	nextModel, _ = nextModel.(Model).Update(cmd())
	next := nextModel.(Model)

	if started || len(next.embeddedTerminals) != 0 {
		t.Fatal("session persisted during Flow refresh did not block terminal startup")
	}
	if next.flowLaunchLeaseOccupied(record.FlowID) {
		t.Fatal("late session occupancy rejection retained the Flow lease")
	}
}

func TestFlowWorktreeAgentReadinessUsesKnownActiveSessionSnapshot(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{FlowID: "flow-1", RepoPath: "/repo", WorktreePath: worktree}
	m := modelWithModeForTest(NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{AgentCommand: "codex"}), ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activePane = ui.PaneBottom
	m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{FlowID: record.FlowID, Status: "active"}})
	if m.selectedFlowWorktreeAgentReady() {
		t.Fatal("known active saved session should hide the worktree-agent shortcut")
	}
	m.sessions = m.sessions.SetItems([]sessions.SessionRecord{{FlowID: record.FlowID, Status: "ended"}})
	if !m.selectedFlowWorktreeAgentReady() {
		t.Fatal("known ended saved session should not hide the worktree-agent shortcut")
	}
}

func TestFlowWorktreeAgentReadinessUsesCachedWorktreeMetadata(t *testing.T) {
	record := flowstore.FlowRecord{
		FlowID:       "flow-1",
		RepoPath:     "/repo",
		WorktreePath: filepath.Join(t.TempDir(), "not-created"),
	}
	m := modelWithModeForTest(NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{AgentCommand: "codex"}), ui.ModeFlows)
	m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
	m.activePane = ui.PaneBottom

	if !m.selectedFlowWorktreeAgentReady() {
		t.Fatal("non-empty cached worktree metadata should expose the worktree-agent shortcut")
	}
}

func TestFlowDetachBlockedSlotRejectsBeforeCallingTerminal(t *testing.T) {
	terminal := &internalFakeDetachableEmbeddedTerminal{target: "tmux attach -t flow"}
	m := Model{
		activeTerminalNum: 1,
		embeddedTerminals: []embeddedTerminalSlot{{
			Number:            1,
			ID:                1,
			FlowID:            "flow-1",
			FlowWorktreeAgent: true,
			FlowDetachBlocked: true,
			Terminal:          terminal,
		}},
	}
	next, cmd := m.handleEmbeddedTerminalDetachPrefix()
	if cmd != nil {
		t.Fatal("detach-blocked slot returned a handoff command")
	}
	if terminal.detached {
		t.Fatal("detach-blocked slot called Detach")
	}
	if len(next.embeddedTerminals) != 1 {
		t.Fatal("detach-blocked slot was removed")
	}
}

func TestTrackedFlowDetachRequiresFreshRunningPhase(t *testing.T) {
	for _, status := range []string{
		flowstore.PhaseCompleted,
		flowstore.PhaseSkipped,
		flowstore.PhaseBlocked,
		flowstore.PhaseNeedsAttention,
	} {
		t.Run(status, func(t *testing.T) {
			terminal := &internalFakeDetachableEmbeddedTerminal{target: "tmux attach -t flow"}
			m := Model{
				activeTerminalNum: 1,
				listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
					return []flowstore.FlowRecord{{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Status: status}}}}, nil
				},
				embeddedTerminals: []embeddedTerminalSlot{{
					Number:            1,
					ID:                1,
					FlowID:            "flow-1",
					FlowPhaseID:       "implementation",
					FlowLaunchTracked: true,
					Terminal:          terminal,
				}},
			}
			next, cmd := m.handleEmbeddedTerminalDetachPrefix()
			if cmd == nil || terminal.detached {
				t.Fatalf("detach preflight cmd/detached = %v/%v, want cmd/false", cmd != nil, terminal.detached)
			}
			next, _ = next.handleEmbeddedTerminalDetachValidated(cmd().(embeddedTerminalDetachValidatedMsg))
			if terminal.detached {
				t.Fatalf("phase status %s allowed detach", status)
			}
		})
	}
}

func TestTrackedFlowDetachRevalidatesPersistedPhaseAtDetachBoundary(t *testing.T) {
	terminal := &internalFakeDetachableEmbeddedTerminal{target: "tmux attach -t flow"}
	reads := 0
	m := Model{
		activeTerminalNum: 1,
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			reads++
			status := flowstore.PhaseRunning
			if reads > 1 {
				status = flowstore.PhaseCompleted
			}
			return []flowstore.FlowRecord{{
				FlowID: "flow-1",
				Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Status: status}},
			}}, nil
		},
		embeddedTerminals: []embeddedTerminalSlot{{
			Number: 1, ID: 1, FlowID: "flow-1", FlowPhaseID: "implementation", FlowLaunchTracked: true, Terminal: terminal,
		}},
	}

	next, cmd := m.handleEmbeddedTerminalDetachPrefix()
	if cmd == nil {
		t.Fatal("detach did not queue initial phase validation")
	}
	msg := cmd().(embeddedTerminalDetachValidatedMsg)
	if reads != 1 || msg.Err != nil {
		t.Fatalf("initial validation reads/error = %d/%v, want 1/nil", reads, msg.Err)
	}
	next, _ = next.handleEmbeddedTerminalDetachValidated(msg)
	if reads != 2 {
		t.Fatalf("persisted Flow reads = %d, want revalidation at detach boundary", reads)
	}
	if terminal.detached || len(next.embeddedTerminals) != 1 {
		t.Fatalf("phase completed after preflight detached/slots = %v/%d, want false/1", terminal.detached, len(next.embeddedTerminals))
	}
}

func TestTrackedFlowDetachSucceedsAfterFreshRunningPhase(t *testing.T) {
	terminal := &internalFakeDetachableEmbeddedTerminal{target: "tmux attach -t flow"}
	m := Model{
		activeTerminalNum: 1,
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{PhaseID: "implementation", Status: flowstore.PhaseRunning}}}}, nil
		},
		launchDetachedTerminal: func(string, string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, nil
		},
		embeddedTerminals: []embeddedTerminalSlot{{
			Number: 1, ID: 1, FlowID: "flow-1", FlowPhaseID: "implementation", FlowLaunchTracked: true, Terminal: terminal,
		}},
	}
	next, cmd := m.handleEmbeddedTerminalDetachPrefix()
	if cmd == nil || terminal.detached {
		t.Fatalf("detach preflight cmd/detached = %v/%v, want cmd/false", cmd != nil, terminal.detached)
	}
	next, _ = next.handleEmbeddedTerminalDetachValidated(cmd().(embeddedTerminalDetachValidatedMsg))
	if !terminal.detached || len(next.embeddedTerminals) != 0 {
		t.Fatalf("validated detach detached/slots = %v/%d, want true/0", terminal.detached, len(next.embeddedTerminals))
	}
}

func TestFlowAssociatedSavedSessionResumeRetainsFlowOccupancy(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", FlowID: "flow-1", CWD: t.TempDir(),
	}
	var launched actions.AgentLaunchContext
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-1", Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseCompleted}}}}, nil
		},
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{record}, nil
		},
		startEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			launched = ctx
			return internalFakeEmbeddedTerminal{}, nil
		},
	}
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok || ctx.FlowID != "flow-1" || ctx.FlowPhaseID != "" {
		t.Fatalf("resume context = %#v, ok %v", ctx, ok)
	}
	next, cmd := next.resumeSessionInEmbeddedTerminal(ctx, record)
	if cmd == nil || !next.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("Flow session resume did not reserve and queue preflight")
	}
	next, _ = next.handleFlowSessionResumePreflight(cmd().(flowSessionResumePreflightMsg))
	if len(next.embeddedTerminals) != 1 {
		t.Fatalf("embedded terminals = %d, want 1", len(next.embeddedTerminals))
	}
	slot := next.embeddedTerminals[0]
	if slot.Scope != embeddedTerminalScopeSession || slot.FlowID != "flow-1" || !slot.FlowDetachBlocked || slot.FlowPhaseID != "" {
		t.Fatalf("Flow-associated resumed slot = %#v", slot)
	}
	if launched.FlowID != "flow-1" || launched.FlowLaunchTracked || launched.FlowPhaseID != "" {
		t.Fatalf("resumed launch context = %#v", launched)
	}
}

func TestFlowAssociatedSavedSessionResumeReadsFlowAfterSessionScan(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", FlowID: "flow-1", CWD: t.TempDir(),
	}
	flowExists := true
	started := false
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			if !flowExists {
				return nil, nil
			}
			return []flowstore.FlowRecord{{FlowID: "flow-1"}}, nil
		},
		listSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if filter.Provider == record.Provider {
				return []sessions.SessionRecord{record}, nil
			}
			flowExists = false
			return []sessions.SessionRecord{record}, nil
		},
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{}, nil
		},
	}
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		t.Fatal("selected record did not produce a resume context")
	}
	next, cmd := next.resumeSessionInEmbeddedTerminal(ctx, record)
	if cmd == nil || !next.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("Flow session resume did not reserve and queue preflight")
	}
	next, _ = next.handleFlowSessionResumePreflight(cmd().(flowSessionResumePreflightMsg))
	if started {
		t.Fatal("session resume started after the Flow was deleted during preflight")
	}
	if next.flowLaunchLeaseOccupied("flow-1") {
		t.Fatal("deleted-Flow resume rejection retained the Flow lease")
	}
}

func TestSavedSessionResumeRefreshesFlowAssociationBeforeRouting(t *testing.T) {
	cached := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", CWD: t.TempDir(),
	}
	current := cached
	current.FlowID = "flow-1"
	var launched actions.AgentLaunchContext
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: current.FlowID}}, nil
		},
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{current}, nil
		},
		startEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			launched = ctx
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	next, refreshCmd := m.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: cached, OK: true})
	if refreshCmd == nil {
		t.Fatal("saved-session selection did not queue an authoritative record refresh")
	}
	if len(next.embeddedTerminals) != 0 {
		t.Fatal("saved-session selection launched before refreshing Flow association")
	}

	refreshedModel, flowPreflightCmd := next.Update(refreshCmd())
	refreshed := refreshedModel.(Model)
	if flowPreflightCmd == nil || !refreshed.flowLaunchLeaseOccupied(current.FlowID) {
		t.Fatal("refreshed Flow-associated session did not reserve and queue Flow preflight")
	}
	finalModel, _ := refreshed.Update(flowPreflightCmd())
	final := finalModel.(Model)
	if len(final.embeddedTerminals) != 1 || final.embeddedTerminals[0].FlowID != current.FlowID {
		t.Fatalf("refreshed Flow-associated terminal slots = %#v", final.embeddedTerminals)
	}
	if launched.FlowID != current.FlowID || launched.FlowPhaseID != "" || launched.FlowLaunchTracked {
		t.Fatalf("refreshed Flow-associated launch context = %#v", launched)
	}
}

func TestSavedSessionResumeValidatesLaunchPathAfterAuthoritativeRefresh(t *testing.T) {
	cached := sessions.SessionRecord{Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended"}
	current := cached
	current.CWD = t.TempDir()
	refreshes := 0
	m := Model{
		agentCommand: "codex",
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			refreshes++
			return []sessions.SessionRecord{current}, nil
		},
		startEmbeddedTerminal: func(_ actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	pending, refreshCmd := m.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: cached, OK: true})
	if refreshCmd == nil || refreshes != 0 {
		t.Fatalf("cached missing path refresh = cmd %T calls %d", refreshCmd, refreshes)
	}
	finalModel, _ := pending.Update(refreshCmd())
	final := finalModel.(Model)
	if refreshes != 1 || len(final.embeddedTerminals) != 1 {
		t.Fatalf("authoritatively refreshed resume = calls %d slots %#v", refreshes, final.embeddedTerminals)
	}
}

func TestSavedSessionResumePreservesExactSessionIdentityAcrossRefreshes(t *testing.T) {
	exact := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: " session-1 ", Status: "ended", FlowID: "flow-exact", LaunchID: "launch-exact", CWD: t.TempDir(),
	}
	aliased := exact
	aliased.SessionID = "session-1"
	aliased.FlowID = "flow-aliased"
	aliased.LaunchID = "launch-aliased"
	m := Model{
		agentCommand: "codex",
		listSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if filter.FlowID == exact.FlowID {
				return []sessions.SessionRecord{exact}, nil
			}
			if filter.Provider != "" {
				return []sessions.SessionRecord{aliased, exact}, nil
			}
			return nil, nil
		},
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: exact.FlowID}}, nil
		},
	}

	if savedSessionResumeKey(exact) == savedSessionResumeKey(aliased) {
		t.Fatal("resume keys aliased distinct exact session IDs")
	}
	_, refreshCmd := m.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: exact, OK: true})
	if refreshCmd == nil {
		t.Fatal("saved-session resume did not queue authoritative refresh")
	}
	refreshed := refreshCmd().(savedSessionResumeRefreshedMsg)
	if !refreshed.Found || refreshed.Record.SessionID != exact.SessionID || refreshed.Record.FlowID != exact.FlowID {
		t.Fatalf("authoritative refresh selected %#v, want exact record %#v", refreshed.Record, exact)
	}

	ctx := actions.AgentLaunchContext{FlowID: exact.FlowID, LaunchID: "resume-token"}
	preflight := m.flowSessionResumePreflightCmd(ctx, exact)().(flowSessionResumePreflightMsg)
	if preflight.Err != nil || !preflight.CurrentRecordFound ||
		preflight.CurrentRecord.SessionID != exact.SessionID || preflight.CurrentRecord.LaunchID != exact.LaunchID {
		t.Fatalf("second authoritative refresh = %#v, want exact session identity", preflight)
	}
}

func TestSavedSessionResumeRejectsNoncanonicalFlowIdentityBeforeReservation(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", FlowID: "flow-1 ", CWD: t.TempDir(),
	}
	m := Model{
		agentCommand: "codex",
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			t.Fatal("noncanonical Flow identity reached authoritative refresh")
			return nil, nil
		},
	}

	next, cmd := m.resumeSavedSession(record, savedSessionResumeEmbedded)
	if cmd != nil {
		t.Fatalf("noncanonical Flow identity queued resume command %T", cmd)
	}
	if next.flowLaunchLeaseOccupied("flow-1") || next.flowLaunchLeaseOccupied(record.FlowID) {
		t.Fatal("noncanonical Flow identity acquired a launch lease")
	}
	if !strings.Contains(next.visibleStatusText(), "noncanonical Flow ID") {
		t.Fatalf("noncanonical Flow identity status = %q", next.visibleStatusText())
	}
}

func TestFlowAssociatedSavedSessionResumeReservesLeaseAndFencesRefreshReplay(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", CWD: t.TempDir(), FlowID: "flow-1",
	}
	var launched actions.AgentLaunchContext
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: record.FlowID}}, nil
		},
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{record}, nil
		},
		startEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			launched = ctx
			return internalFakeEmbeddedTerminal{}, nil
		},
	}

	reserved, refreshCmd := m.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: record, OK: true})
	lease, ok := reserved.flowLaunchLease(record.FlowID)
	if !ok || lease.Source != flowLaunchSourceSessionResume || lease.Token == "" {
		t.Fatalf("saved-session refresh lease = %#v, present %v", lease, ok)
	}
	refreshResult := refreshCmd()
	refreshedModel, preflightCmd := reserved.Update(refreshResult)
	refreshed := refreshedModel.(Model)
	if preflightCmd == nil {
		t.Fatal("owned refresh did not queue Flow preflight")
	}
	replayedModel, replayCmd := refreshed.Update(refreshResult)
	if replayCmd != nil {
		t.Fatal("replayed saved-session refresh queued another launch")
	}
	if replayed := replayedModel.(Model); !replayed.matchingFlowLaunchLease(record.FlowID, lease.Token, flowLaunchSourceSessionResume) {
		t.Fatal("replayed refresh disturbed the owned Flow lease")
	}

	finalModel, _ := refreshed.Update(preflightCmd())
	final := finalModel.(Model)
	if len(final.embeddedTerminals) != 1 || launched.LaunchID != lease.Token {
		t.Fatalf("owned saved-session launch = slots %#v context %#v", final.embeddedTerminals, launched)
	}
}

func TestPendingSavedSessionResumeRejectsSupersedingRequestWithoutStrandingLease(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", Status: "ended", CWD: t.TempDir(), FlowID: "flow-1",
	}
	m := Model{
		agentCommand: "codex",
		listSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			return []sessions.SessionRecord{record}, nil
		},
	}

	reserved, refreshCmd := m.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: record, OK: true})
	lease, ok := reserved.flowLaunchLease(record.FlowID)
	if !ok {
		t.Fatal("first saved-session resume did not reserve its Flow")
	}
	duplicateRecord := record
	duplicateRecord.FlowID = ""
	afterDuplicate, _ := reserved.handleEmbeddedSessionPickerSelected(embeddedSessionPickerSelectedMsg{Record: duplicateRecord, OK: true})
	if got := afterDuplicate.pendingSavedSessionResumes[savedSessionResumeKey(record)]; got != lease.Token {
		t.Fatalf("pending saved-session token = %q, want original %q", got, lease.Token)
	}
	if !afterDuplicate.matchingFlowLaunchLease(record.FlowID, lease.Token, flowLaunchSourceSessionResume) {
		t.Fatal("superseding saved-session request stranded the original Flow lease")
	}

	refreshedModel, preflightCmd := afterDuplicate.Update(refreshCmd())
	refreshed := refreshedModel.(Model)
	if preflightCmd == nil || !refreshed.matchingFlowLaunchLease(record.FlowID, lease.Token, flowLaunchSourceSessionResume) {
		t.Fatal("original saved-session request could not continue after duplicate rejection")
	}
}

func TestFlowAssociatedSavedSessionResumeRejectsRecordMovedToAnotherFlow(t *testing.T) {
	selected := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", LaunchID: "launch-a", Status: "ended", FlowID: "flow-a", CWD: t.TempDir(),
	}
	current := selected
	current.FlowID = "flow-b"
	current.LaunchID = "launch-b"
	started := false
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: "flow-a"}, {FlowID: "flow-b"}}, nil
		},
		listSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if filter.Provider == sessions.ProviderCodex {
				return []sessions.SessionRecord{current}, nil
			}
			return nil, nil
		},
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{}, nil
		},
	}
	ctx, ok, next := m.sessionResumeLaunchContext(selected)
	if !ok {
		t.Fatal("selected record did not produce a resume context")
	}
	next, cmd := next.resumeSessionInEmbeddedTerminal(ctx, selected)
	if cmd == nil || !next.flowLaunchLeaseOccupied("flow-a") {
		t.Fatal("stale selection did not reserve and queue preflight")
	}
	next, _ = next.handleFlowSessionResumePreflight(cmd().(flowSessionResumePreflightMsg))
	if started {
		t.Fatal("session moved to another Flow but resume still started")
	}
	if next.flowLaunchLeaseOccupied("flow-a") {
		t.Fatal("stale resume rejection retained the old Flow lease")
	}
}

func TestFlowAssociatedSavedSessionResumeRejectsRecordMovedBetweenAuthoritativeReads(t *testing.T) {
	record := sessions.SessionRecord{
		Provider: sessions.ProviderCodex, SessionID: "session-1", LaunchID: "launch-a", Status: "ended", FlowID: "flow-a", CWD: t.TempDir(),
	}
	started := false
	m := Model{
		agentCommand: "codex",
		listFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			return []flowstore.FlowRecord{{FlowID: record.FlowID}}, nil
		},
		listSessions: func(filter sessions.SessionFilter) ([]sessions.SessionRecord, error) {
			if filter.Provider == record.Provider {
				return []sessions.SessionRecord{record}, nil
			}
			if filter.FlowID == record.FlowID {
				return nil, nil
			}
			return nil, nil
		},
		startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
			started = true
			return internalFakeEmbeddedTerminal{}, nil
		},
	}
	ctx, ok, next := m.sessionResumeLaunchContext(record)
	if !ok {
		t.Fatal("selected record did not produce a resume context")
	}
	next, cmd := next.resumeSessionInEmbeddedTerminal(ctx, record)
	if cmd == nil || !next.flowLaunchLeaseOccupied(record.FlowID) {
		t.Fatal("saved-session resume did not reserve and queue preflight")
	}
	next, _ = next.handleFlowSessionResumePreflight(cmd().(flowSessionResumePreflightMsg))
	if started {
		t.Fatal("session absent from final Flow snapshot but resume still started")
	}
	if next.flowLaunchLeaseOccupied(record.FlowID) {
		t.Fatal("stale final-snapshot rejection retained the Flow lease")
	}
}

func TestTrackedPhaseResumeDetachPolicyUsesPersistedPhaseDurability(t *testing.T) {
	for _, tt := range []struct {
		name          string
		phaseTerminal bool
		wantBlocked   bool
	}{
		{name: "running phase uses fresh validation"},
		{name: "terminal phase is blocked", phaseTerminal: true, wantBlocked: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{startEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (EmbeddedTerminal, error) {
				return internalFakeEmbeddedTerminal{}, nil
			}}
			ctx := actions.AgentLaunchContext{
				Command: "codex", LaunchID: "launch-1", FlowID: "flow-1", FlowPhaseID: "implementation",
				ResumeSessionID: "session-1", FlowLaunchTracked: true, FlowPhaseTerminal: tt.phaseTerminal,
			}
			next, opened, err, _ := m.openFlowEmbeddedTerminal(ctx)
			if err != nil || !opened {
				t.Fatalf("openFlowEmbeddedTerminal() opened/error = %v/%v", opened, err)
			}
			if got := next.embeddedTerminals[0].FlowDetachBlocked; got != tt.wantBlocked {
				t.Fatalf("FlowDetachBlocked = %v, want %v", got, tt.wantBlocked)
			}
		})
	}
}

func TestFlowWorktreeAgentUsesActiveFlowsSurface(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{FlowID: "flow-active", RepoPath: "/repo", WorktreePath: worktree}
	m := NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{AgentCommand: "codex"})
	m.activeFlowSurface = true
	m.activePane = ui.PaneBottom
	m.activeFlows = m.activeFlows.SetItems([]flowstore.FlowRecord{record})
	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("Active Flows s did not queue a worktree-agent preflight")
	}
	if !nextModel.(Model).flowLaunchLeaseOccupied(record.FlowID) {
		t.Fatal("Active Flows s did not reserve the selected Flow")
	}
}

func TestFlowWorktreeAndPhaseLaunchesSerializeInBothOrders(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID: "flow-1", RepoPath: "/repo", WorktreePath: worktree,
		Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}},
	}
	newModel := func() Model {
		m := modelWithModeForTest(NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{AgentCommand: "codex"}), ui.ModeFlows)
		m.activePane = ui.PaneBottom
		m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
		return m
	}

	m := newModel()
	nextModel, worktreeCmd := m.handleStartSelectedFlowWorktreeAgent()
	m = nextModel.(Model)
	nextModel, phaseCmd := m.handleLaunchNextFlowPhase()
	if worktreeCmd == nil || phaseCmd != nil {
		t.Fatalf("s then g commands = %v/%v, want command/nil", worktreeCmd != nil, phaseCmd != nil)
	}
	if lease, _ := nextModel.(Model).flowLaunchLease(record.FlowID); lease.Source != flowLaunchSourceWorktreeAgent {
		t.Fatalf("s then g lease = %#v", lease)
	}

	m = newModel()
	nextModel, phaseCmd = m.handleLaunchNextFlowPhase()
	m = nextModel.(Model)
	nextModel, worktreeCmd = m.handleStartSelectedFlowWorktreeAgent()
	if phaseCmd == nil || worktreeCmd != nil {
		t.Fatalf("g then s commands = %v/%v, want command/nil", phaseCmd != nil, worktreeCmd != nil)
	}
	if lease, _ := nextModel.(Model).flowLaunchLease(record.FlowID); lease.Source != flowLaunchSourcePhase {
		t.Fatalf("g then s lease = %#v", lease)
	}
}

func TestFlowWorktreeAgentSerializesAgainstEveryLaunchEntrypointInBothOrders(t *testing.T) {
	worktree := t.TempDir()
	record := flowstore.FlowRecord{
		FlowID: "flow-1", RepoPath: "/repo", WorktreePath: worktree,
		Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}},
	}
	newModel := func() Model {
		m := modelWithModeForTest(NewWithOptions([]scanner.Repo{{Path: "/repo"}}, Options{AgentCommand: "codex"}), ui.ModeFlows)
		m.activePane = ui.PaneBottom
		m.flows = m.flows.SetItems([]flowstore.FlowRecord{record})
		return m
	}

	for _, tt := range []struct {
		name   string
		source flowLaunchSource
	}{
		{name: "AutoMode", source: flowLaunchSourceAutoPhase},
		{name: "phase r", source: flowLaunchSourcePhaseResume},
		{name: "repair R", source: flowLaunchSourceRepair},
		{name: "Sessions pane resume", source: flowLaunchSourceSessionResume},
		{name: "session picker resume", source: flowLaunchSourceSessionResume},
		{name: "inline session resume", source: flowLaunchSourceSessionResume},
		{name: "Plan Now after partial Flow publication", source: flowLaunchSourceCreatePhase},
	} {
		t.Run(tt.name+" then worktree agent", func(t *testing.T) {
			m, acquired := newModel().acquireFlowLaunchLease(record.FlowID, "competing-token", tt.source)
			if !acquired {
				t.Fatal("competing entrypoint did not acquire its lease")
			}
			nextModel, cmd := m.handleStartSelectedFlowWorktreeAgent()
			if cmd != nil {
				t.Fatal("worktree agent started while a competing launch owned the Flow")
			}
			if lease, ok := nextModel.(Model).flowLaunchLease(record.FlowID); !ok || lease.Source != tt.source || lease.Token != "competing-token" {
				t.Fatalf("competing lease changed = %#v, present %v", lease, ok)
			}
		})

		t.Run("worktree agent then "+tt.name, func(t *testing.T) {
			nextModel, cmd := newModel().handleStartSelectedFlowWorktreeAgent()
			if cmd == nil {
				t.Fatal("worktree agent did not queue its preflight")
			}
			m := nextModel.(Model)
			if _, acquired := m.acquireFlowLaunchLease(record.FlowID, "competing-token", tt.source); acquired {
				t.Fatal("competing entrypoint acquired a Flow already reserved by the worktree agent")
			}
			if lease, ok := m.flowLaunchLease(record.FlowID); !ok || lease.Source != flowLaunchSourceWorktreeAgent || lease.Token == "" {
				t.Fatalf("worktree lease changed = %#v, present %v", lease, ok)
			}
		})

		t.Run(tt.name+" stale token and failure cleanup", func(t *testing.T) {
			m, acquired := newModel().acquireFlowLaunchLease(record.FlowID, "current-token", tt.source)
			if !acquired {
				t.Fatal("entrypoint did not acquire its lease")
			}
			m = m.releaseFlowLaunchLease(record.FlowID, "stale-token")
			if !m.matchingFlowLaunchLease(record.FlowID, "current-token", tt.source) {
				t.Fatal("stale completion released or replaced the current lease")
			}
			m = m.releaseFlowLaunchLease(record.FlowID, "current-token")
			if m.flowLaunchLeaseOccupied(record.FlowID) {
				t.Fatal("owned failure cleanup retained the Flow lease")
			}
		})
	}
}
