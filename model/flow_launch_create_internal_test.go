package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
)

type createLaunchHarness struct {
	record       flowstore.FlowRecord
	allocatedID  string
	order        []string
	contexts     []actions.AgentLaunchContext
	phaseUpdates []flowstore.PhaseUpdate
	sessions     []sessions.SessionRecord
	addErr       error
	addPersists  bool
	worktreeErr  error
	bootstrapErr error
	metadataErr  error
	phaseErrs    map[string]error
	readErrs     map[int]error
	readCalls    int
	terminalErr  error
	releases     int
}

func newCreateLaunchHarness(phases []flowstore.FlowPhase) *createLaunchHarness {
	return &createLaunchHarness{record: flowstore.FlowRecord{
		FlowID: "20260814T120000Z-new-flow", RepoPath: "/dev/alpha", Title: "New Flow",
		Instructions: "Write the plan.", BaseRef: "main", Headless: true, Phases: phases,
	}}
}

func (h *createLaunchHarness) model(t *testing.T) Model {
	t.Helper()
	m := NewWithOptions([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{
		AgentCommand: "codex", LaunchBackend: "tmux", TmuxLaunchAvailable: func() bool { return true },
		StartEmbeddedTerminal: func(ctx actions.AgentLaunchContext, _, _ int) (EmbeddedTerminal, error) {
			h.order = append(h.order, "terminal")
			h.contexts = append(h.contexts, ctx)
			if h.terminalErr != nil {
				return nil, h.terminalErr
			}
			return flowPhaseLaunchTestTerminal{state: "running"}, nil
		},
	})
	m.launchSeams = flowLaunchSeams{
		AllocateFlowID: func(title string) (string, error) {
			h.order = append(h.order, "allocate")
			if h.allocatedID != "" {
				return h.allocatedID, nil
			}
			return h.record.FlowID, nil
		},
		ListFlowSessions: func(flowID string) ([]sessions.SessionRecord, error) {
			h.order = append(h.order, "sessions:"+flowID)
			return h.sessions, nil
		},
		CreateFlow: func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "create:"+record.FlowID)
			if record.FlowID != h.record.FlowID || record.RepoPath != h.record.RepoPath || opts.Headless == nil || !*opts.Headless {
				t.Fatalf("exact create = %#v, %#v", record, opts)
			}
			created := h.record
			created.Title, created.Instructions, created.BaseRef = record.Title, record.Instructions, record.BaseRef
			h.record = created
			return created, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			h.order = append(h.order, "reserve:"+flowID)
			return h.record, func() { h.releases++ }, nil
		},
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			h.order = append(h.order, "worktree")
			if h.worktreeErr != nil {
				return actions.FlowWorktreeCreateResult{}, h.worktreeErr
			}
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-new", Branch: "flow/new"}, nil
		},
		ResolveCommit: func(string) string {
			h.order = append(h.order, "commit")
			return "abc123"
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: "true"}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			h.order = append(h.order, "bootstrap")
			return h.bootstrapErr
		},
		AddPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "launch-id:"+update.FlowID+":"+update.PhaseID+":"+update.LaunchID)
			if h.addErr != nil && !h.addPersists {
				return flowstore.FlowRecord{}, h.addErr
			}
			for i := range h.record.Phases {
				if h.record.Phases[i].PhaseID == update.PhaseID {
					h.record.Phases[i].Status = flowstore.PhaseRunning
					h.record.Phases[i].LaunchIDs = append(h.record.Phases[i].LaunchIDs, update.LaunchID)
				}
			}
			if h.addErr != nil {
				return flowstore.FlowRecord{}, h.addErr
			}
			return h.record, nil
		},
		ReadFlow: func(flowID string) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "reread:"+flowID)
			h.readCalls++
			if err := h.readErrs[h.readCalls]; err != nil {
				return flowstore.FlowRecord{}, err
			}
			return h.record, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "metadata")
			if h.metadataErr != nil {
				return flowstore.FlowRecord{}, h.metadataErr
			}
			h.record.WorktreePath, h.record.Branch, h.record.Commit = update.WorktreePath, update.Branch, update.Commit
			return h.record, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "block:"+update.PhaseID)
			h.phaseUpdates = append(h.phaseUpdates, update)
			if err := h.phaseErrs[update.PhaseID]; err != nil {
				return flowstore.FlowRecord{}, err
			}
			return h.record, nil
		},
		NewLaunchID: func() string { return "launch-create-1" },
	}
	return m
}

func (h *createLaunchHarness) start(t *testing.T, m Model) Model {
	t.Helper()
	var request uint64
	m, request = m.nextFlowCreateRequest()
	create := flowLaunchCreateRequest{
		Request: request, RepoPath: "/dev/alpha", Title: "New Flow", Instructions: "Write the plan.", BaseRef: "main", Headless: true,
	}
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindCreatePhase, Origin: flowLaunchOriginNewFlow, Create: create})
	if !admitted || cmd == nil {
		t.Fatalf("create admission = %v, cmd=%v, status=%q", admitted, cmd != nil, next.status.Text)
	}
	return drainCreateLaunch(t, next, cmd)
}

func drainCreateLaunch(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 20; i++ {
		switch msg := cmd().(type) {
		case flowLaunchEventMsg:
			m, cmd = m.handleFlowLaunchEvent(msg)
			if !m.flowLaunchAttemptOccupied(msg.FlowID) {
				return m
			}
		case flowLaunchFailurePersistedMsg:
			m, cmd = m.handleFlowLaunchFailurePersisted(msg)
		default:
			return m
		}
	}
	return m
}

func TestCreateFlowLaunchOwnsExactIdentityAndStartupOrdering(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m := h.start(t, h.model(t))

	want := []string{
		"allocate", "sessions:" + h.record.FlowID, "create:" + h.record.FlowID, "reserve:" + h.record.FlowID,
		"worktree", "commit", "bootstrap", "reread:" + h.record.FlowID,
		"launch-id:" + h.record.FlowID + ":plan:launch-create-1", "metadata", "terminal",
	}
	if !reflect.DeepEqual(h.order, want) {
		t.Fatalf("create order = %#v, want %#v", h.order, want)
	}
	if len(h.contexts) != 1 {
		t.Fatalf("launch contexts = %#v", h.contexts)
	}
	ctx := h.contexts[0]
	if ctx.FlowID != h.record.FlowID || ctx.LaunchID != "launch-create-1" || ctx.FlowPhaseID != "plan" ||
		ctx.WorktreePath != "/dev/alpha-worktrees/flow-new" || ctx.Commit != "abc123" || !ctx.Embedded || !ctx.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", ctx)
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || len(m.embeddedTerminals) != 1 {
		t.Fatalf("handoff ownership: releases=%d active=%d terminals=%d", h.releases, m.activeFlowCreate, len(m.embeddedTerminals))
	}
}

func TestCreateFlowLaunchParksWithoutLaunchID(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhasePending}})
	m := h.start(t, h.model(t))
	if strings.Contains(strings.Join(h.order, ","), "launch-id:") || len(h.contexts) != 0 {
		t.Fatalf("parked Flow launched: order=%#v contexts=%#v", h.order, h.contexts)
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "Created flow") {
		t.Fatalf("parked result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchAddFailureProvenAbsentRecordsMetadataWithoutBlockingRoots(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.addErr = errors.New("write failed")
	m := h.start(t, h.model(t))

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("proven-absent launch blocked roots: %#v", h.phaseUpdates)
	}
	if !strings.Contains(strings.Join(h.order, ","), "reread:"+h.record.FlowID) || !strings.Contains(strings.Join(h.order, ","), "metadata") {
		t.Fatalf("recovery order = %#v", h.order)
	}
	if h.releases != 1 || !strings.HasPrefix(m.status.Text, "Flow "+h.record.FlowID+": ") || !strings.Contains(m.status.Text, "AddPhaseLaunchID: write failed") {
		t.Fatalf("failure result: releases=%d status=%q", h.releases, m.status.Text)
	}
}

func TestCreateFlowLaunchAddFailureProofControlsRecovery(t *testing.T) {
	t.Run("present blocks every captured root", func(t *testing.T) {
		h := newCreateLaunchHarness([]flowstore.FlowPhase{
			{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
			{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
		})
		h.addErr = errors.New("write failed")
		h.addPersists = true
		m := h.start(t, h.model(t))

		if len(h.phaseUpdates) != 2 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[1].PhaseID != "implementation" {
			t.Fatalf("present-token recovery = %#v", h.phaseUpdates)
		}
		if len(h.contexts) != 0 || h.releases != 1 || !strings.Contains(m.status.Text, "AddPhaseLaunchID: write failed") {
			t.Fatalf("present-token result: contexts=%#v releases=%d status=%q", h.contexts, h.releases, m.status.Text)
		}
	})

	t.Run("unreadable joins independent recovery failures", func(t *testing.T) {
		h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
		h.addErr = errors.New("write failed")
		h.readErrs = map[int]error{2: errors.New("database busy")}
		h.metadataErr = errors.New("disk full")
		h.phaseErrs = map[string]error{"plan": errors.New("database busy")}
		m := h.start(t, h.model(t))

		want := "Flow " + h.record.FlowID + ": AddPhaseLaunchID: write failed; reread flow after AddPhaseLaunchID: database busy; record start metadata: disk full; block phase plan: database busy"
		if m.status.Text != want {
			t.Fatalf("joined recovery status = %q, want %q", m.status.Text, want)
		}
		if len(h.phaseUpdates) != 1 || h.releases != 1 {
			t.Fatalf("unreadable recovery: updates=%#v releases=%d", h.phaseUpdates, h.releases)
		}
	})
}

func TestCreateFlowLaunchBootstrapAndMetadataFailuresRecoverCapturedRoots(t *testing.T) {
	for _, tc := range []struct {
		name          string
		configure     func(*createLaunchHarness)
		wantOperation string
	}{
		{name: "bootstrap", configure: func(h *createLaunchHarness) { h.bootstrapErr = errors.New("hook failed") }, wantOperation: "run bootstrap: hook failed"},
		{name: "metadata", configure: func(h *createLaunchHarness) { h.metadataErr = errors.New("disk full") }, wantOperation: "record start metadata: disk full"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			tc.configure(h)
			m := h.start(t, h.model(t))

			if len(h.phaseUpdates) != 2 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[1].PhaseID != "implementation" {
				t.Fatalf("captured-root recovery = %#v", h.phaseUpdates)
			}
			if len(h.contexts) != 0 || h.releases != 1 || !strings.Contains(m.status.Text, tc.wantOperation) {
				t.Fatalf("recovery result: contexts=%#v releases=%d status=%q", h.contexts, h.releases, m.status.Text)
			}
		})
	}
}

func TestCreateFlowLaunchEmbeddedFailureUsesChosenPhaseRecovery(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
	})
	h.terminalErr = errors.New("terminal unavailable")
	m := h.start(t, h.model(t))

	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("embedded recovery = %#v; order=%#v status=%q", h.phaseUpdates, h.order, m.status.Text)
	}
	if h.releases != 1 || !strings.Contains(m.status.Text, "Flow "+h.record.FlowID+": terminal unavailable") {
		t.Fatalf("embedded failure result: releases=%d status=%q", h.releases, m.status.Text)
	}
}

func TestCreateFlowLaunchPlanReviewWithoutPlanRecoversMetadataAndBlocksWithOutcome(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "review", Title: "Plan Review", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady}})
	m := h.start(t, h.model(t))

	if len(h.phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v", h.phaseUpdates)
	}
	update := h.phaseUpdates[0]
	if update.PhaseID != "review" || update.Status != flowstore.PhaseBlocked || update.Outcome != flowstore.OutcomeBlocked {
		t.Fatalf("Plan Review recovery = %#v", update)
	}
	if !strings.Contains(strings.Join(h.order, ","), "metadata") || strings.Contains(strings.Join(h.order, ","), "launch-id:") {
		t.Fatalf("validation recovery order = %#v", h.order)
	}
	if h.releases != 1 || !strings.HasPrefix(m.status.Text, "Flow "+h.record.FlowID+": ") || !strings.Contains(m.status.Text, "Plan Review needs a linked plan") {
		t.Fatalf("failure result: releases=%d status=%q", h.releases, m.status.Text)
	}
}

func TestCreateFlowLaunchWorktreeFailureBlocksParallelRootsInCanonicalOrder(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "later", Title: "Later", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady, Order: 20},
		{PhaseID: "first", Title: "First", Kind: flowstore.KindPlanReview, Status: flowstore.PhaseReady, Order: 10},
	})
	h.record.PlanID = "plan-1"
	h.worktreeErr = errors.New("branch exists")
	m := h.start(t, h.model(t))
	if len(h.phaseUpdates) != 2 || h.phaseUpdates[0].PhaseID != "later" || h.phaseUpdates[1].PhaseID != "first" {
		t.Fatalf("parallel-root recovery = %#v", h.phaseUpdates)
	}
	if h.phaseUpdates[0].Outcome != "" || h.phaseUpdates[1].Outcome != flowstore.OutcomeBlocked {
		t.Fatalf("parallel-root outcomes = %#v", h.phaseUpdates)
	}
	if h.releases != 1 || !strings.HasPrefix(m.status.Text, "Flow "+h.record.FlowID+": ") || !strings.Contains(m.status.Text, "create worktree: branch exists") {
		t.Fatalf("worktree failure result: releases=%d status=%q", h.releases, m.status.Text)
	}
}

func TestCreateFlowLaunchRefusesOnlyLivePreexistingSessionsBeforeCreate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		session    sessions.SessionRecord
		wantCreate bool
	}{
		{name: "live", session: sessions.SessionRecord{SessionID: "s1", FlowID: "20260814T120000Z-new-flow", Status: "active"}},
		{name: "ended", session: sessions.SessionRecord{SessionID: "s1", FlowID: "20260814T120000Z-new-flow", Status: "ended", EndedAt: time.Now()}, wantCreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			h.sessions = []sessions.SessionRecord{tc.session}
			m := h.start(t, h.model(t))
			created := strings.Contains(strings.Join(h.order, ","), "create:"+h.record.FlowID)
			if created != tc.wantCreate {
				t.Fatalf("created = %v, want %v; order=%#v", created, tc.wantCreate, h.order)
			}
			if !tc.wantCreate && (m.activeFlowCreate != 0 || h.releases != 0) {
				t.Fatalf("live refusal ownership: active=%d releases=%d", m.activeFlowCreate, h.releases)
			}
		})
	}
}

func TestCreateFlowLaunchRejectsInvalidAllocatedIdentityBeforeSessionLookup(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.allocatedID = "../not-safe"
	m := h.start(t, h.model(t))
	if !reflect.DeepEqual(h.order, []string{"allocate"}) {
		t.Fatalf("invalid allocation crossed admission: %#v", h.order)
	}
	if m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "invalid ID") {
		t.Fatalf("invalid allocation result: active=%d status=%q", m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchPreservesRequestTimeAgentSnapshotAcrossAllocation(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m := h.model(t)
	var request uint64
	m, request = m.nextFlowCreateRequest()
	create := flowLaunchCreateRequest{Request: request, RepoPath: "/dev/alpha", Title: "New Flow", Instructions: "Write the plan.", BaseRef: "main", Headless: true}
	m, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindCreatePhase, Create: create})
	if !admitted || cmd == nil {
		t.Fatal("create intent was not admitted")
	}
	allocated := cmd().(flowLaunchEventMsg)
	// A settings edit after request-time allocation belongs to the next launch.
	m.agentCommand = "claude"
	m, cmd = m.handleFlowLaunchEvent(allocated)
	m = drainCreateLaunch(t, m, cmd)
	if len(h.contexts) != 1 || h.contexts[0].Command != "codex" {
		t.Fatalf("launch contexts = %#v, want request-time codex snapshot", h.contexts)
	}
}
