package model

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

type createLaunchHarness struct {
	record         flowstore.FlowRecord
	allocatedID    string
	order          []string
	contexts       []actions.AgentLaunchContext
	phaseUpdates   []flowstore.PhaseUpdate
	sessions       []sessions.SessionRecord
	addErr         error
	addPersists    bool
	createErr      error
	reserveErr     error
	worktreeErr    error
	bootstrapErr   error
	metadataErr    error
	metadataMutate func(*flowstore.FlowRecord)
	phaseErrs      map[string]error
	readErrs       map[int]error
	readMutate     func(*flowstore.FlowRecord)
	readCalls      int
	terminalErr    error
	terminal       EmbeddedTerminal
	releases       int
}

func TestCreateFlowLaunchCustomPhasePersistenceRereadsAuthoritativeReservationRecord(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	created, err := store.Create(flowstore.FlowRecord{
		RepoPath: "/dev/alpha", Title: "Custom persistence", Instructions: "Write the plan.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Phases) == 0 {
		t.Fatal("test Flow has no startup phases")
	}

	m := NewWithOptions(nil, Options{
		FlowStore: store,
		AddFlowPhaseLaunchID: func(update flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return store.AddPhaseLaunchID(update)
		},
	})
	record, release, err := m.launchSeams.ReserveLaunch(created.FlowID)
	if release != nil {
		defer release()
	}
	if err != nil {
		t.Fatal(err)
	}
	if record.FlowID != created.FlowID || len(record.Phases) != len(created.Phases) {
		t.Fatalf("create reservation record = %#v, want authoritative created Flow", record)
	}
}

func TestCreateFlowLaunchEmbeddedSuccessRefreshesVisibleUnfocusedFlowPane(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseRunning}})
	m := h.model(t)
	m.bottomMode = ui.ModeFlows
	m.contentPane = ui.PaneTop
	m.activePane = ui.PaneTop
	if m.flowSurfaceVisible() || !m.flowRefreshSurfaceVisible() {
		t.Fatalf("test surface predicates: focused=%v refresh-visible=%v", m.flowSurfaceVisible(), m.flowRefreshSurfaceVisible())
	}

	attempt := flowLaunchAttempt{
		Token: "launch-create-1", Kind: flowLaunchKindCreatePhase, FlowID: h.record.FlowID,
		Create: flowLaunchCreateRequest{Request: 1, RepoPath: h.record.RepoPath},
	}
	m.activeFlowCreate = attempt.Create.Request
	m = m.withFlowLaunchAttempt(attempt)
	beforeRequest := m.ListRequest(ui.ModeFlows)
	next, _ := m.installFlowLaunchEmbedded(attempt, flowLaunchEventMsg{
		Context: actions.AgentLaunchContext{
			Command: "codex", FlowID: h.record.FlowID, FlowPhaseID: "plan", LaunchID: attempt.Token, InitialPrompt: "Write the plan.",
		},
		Record: h.record,
	})
	if next.flowSurfaceVisible() {
		t.Fatal("test launch unexpectedly focused the Flow pane before prefill completed")
	}
	if next.ListRequest(ui.ModeFlows) == beforeRequest {
		t.Fatal("successful create launch did not refresh the visible unfocused Flow pane")
	}
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
			if h.terminal != nil {
				return h.terminal, nil
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
			if record.FlowID != h.record.FlowID || record.RepoPath != h.record.RepoPath || opts.Headless == nil || *opts.Headless != h.record.Headless {
				t.Fatalf("exact create = %#v, %#v", record, opts)
			}
			created := h.record
			created.Title, created.Instructions, created.BaseRef = record.Title, record.Instructions, record.BaseRef
			if h.createErr != nil {
				return flowstore.FlowRecord{}, h.createErr
			}
			h.record = created
			return created, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			h.order = append(h.order, "reserve:"+flowID)
			if h.reserveErr != nil {
				return flowstore.FlowRecord{}, nil, h.reserveErr
			}
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
			if h.readMutate != nil {
				h.readMutate(&h.record)
			}
			return h.record, nil
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "metadata")
			if h.metadataErr != nil {
				return flowstore.FlowRecord{}, h.metadataErr
			}
			h.record.WorktreePath, h.record.Branch, h.record.Commit = update.WorktreePath, update.Branch, update.Commit
			if h.metadataMutate != nil {
				h.metadataMutate(&h.record)
			}
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

type createPrefillFailureTerminal struct {
	flowPhaseLaunchTestTerminal
	terminateErr error
	terminates   int
}

func (t *createPrefillFailureTerminal) Write([]byte) (int, error) {
	return 0, errors.New("prefill write failed")
}

func (t *createPrefillFailureTerminal) Terminate() error {
	t.terminates++
	return t.terminateErr
}

func (h *createLaunchHarness) start(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := h.admit(t, m)
	return drainCreateLaunch(t, m, cmd)
}

func (h *createLaunchHarness) admit(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	var request uint64
	m, request = m.nextFlowCreateRequest()
	create := flowLaunchCreateRequest{
		Request: request, RepoPath: "/dev/alpha", Title: "New Flow", Instructions: "Write the plan.", BaseRef: "main", Headless: h.record.Headless,
	}
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindCreatePhase, Origin: flowLaunchOriginNewFlow, Create: create})
	if !admitted || cmd == nil {
		t.Fatalf("create admission = %v, cmd=%v, status=%q", admitted, cmd != nil, next.status.Text)
	}
	return next, cmd
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

func advanceCreateLaunchToStage(t *testing.T, m Model, cmd tea.Cmd, want flowLaunchStage) (Model, flowLaunchEventMsg) {
	t.Helper()
	for i := 0; cmd != nil && i < 20; i++ {
		msg, ok := cmd().(flowLaunchEventMsg)
		if !ok {
			t.Fatalf("create command returned a non-event before stage %v", want)
		}
		if msg.Stage == want {
			return m, msg
		}
		m, cmd = m.handleFlowLaunchEvent(msg)
	}
	t.Fatalf("create launch did not reach stage %v", want)
	return m, flowLaunchEventMsg{}
}

func startInteractiveCreateUntilPrefill(t *testing.T, h *createLaunchHarness) (Model, embeddedPromptPrefillResultMsg) {
	t.Helper()
	h.record.Headless = false
	m, cmd := h.admit(t, h.model(t))
	var prefill embeddedPromptPrefillResultMsg
	for i := 0; cmd != nil && i < 20; i++ {
		raw := cmd()
		switch msg := raw.(type) {
		case flowLaunchEventMsg:
			m, cmd = m.handleFlowLaunchEvent(msg)
		case tea.BatchMsg:
			for _, child := range msg {
				if child == nil {
					continue
				}
				if result, ok := child().(embeddedPromptPrefillResultMsg); ok {
					prefill = result
					break
				}
			}
			cmd = nil
		default:
			t.Fatalf("create command returned %T before prefill", raw)
		}
	}
	if prefill.ID == 0 || prefill.Create == nil {
		t.Fatalf("prefill result = %#v, want create origin", prefill)
	}
	return m, prefill
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

func TestCreateFlowLaunchCreateAndReservationErrorsDoNotAdoptOrRecoverUnprovenRows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*createLaunchHarness)
		want      string
	}{
		{name: "create durability ambiguous", configure: func(h *createLaunchHarness) { h.createErr = errors.New("commit result unknown") }, want: "create flow "},
		{name: "reservation refused", configure: func(h *createLaunchHarness) { h.reserveErr = errors.New("flow closed") }, want: "reserve launch: flow closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			tc.configure(h)
			m := h.start(t, h.model(t))
			joined := strings.Join(h.order, ",")
			if strings.Contains(joined, "worktree") || strings.Contains(joined, "reread:") || strings.Contains(joined, "metadata") || len(h.phaseUpdates) != 0 {
				t.Fatalf("unproven row crossed into recovery: order=%#v updates=%#v", h.order, h.phaseUpdates)
			}
			if !strings.Contains(m.status.Text, tc.want) || m.activeFlowCreate != 0 || h.releases != 0 {
				t.Fatalf("failure result: status=%q active=%d releases=%d", m.status.Text, m.activeFlowCreate, h.releases)
			}
		})
	}
}

func TestCreateFlowLaunchRejectsRecordClaimedBeforeTrackedReservation(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, cmd := h.admit(t, h.model(t))
	m, written := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)
	m, reserveCmd := m.handleFlowLaunchEvent(written)
	if reserveCmd == nil {
		t.Fatal("proven create did not request tracked reservation")
	}

	// Model another process winning the gap after exact create: it creates the
	// worktree, stamps a launch ID, and releases its tracked reservation first.
	h.record.WorktreePath = "/dev/alpha-worktrees/winner"
	h.record.Branch = "flow/winner"
	h.record.Commit = "winner-commit"
	h.record.Phases[0].Status = flowstore.PhaseRunning
	h.record.Phases[0].LaunchIDs = []string{"winner-launch"}
	reserved := reserveCmd().(flowLaunchEventMsg)
	m, cmd = m.handleFlowLaunchEvent(reserved)
	m = drainCreateLaunch(t, m, cmd)

	joined := strings.Join(h.order, ",")
	for _, forbidden := range []string{"worktree", "launch-id:", "metadata", "terminal", "block:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("losing create attempt performed %q after contention: %#v", forbidden, h.order)
		}
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "claimed by another launch") {
		t.Fatalf("contention result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchRejectsSameIDReplacementBeforeTrackedReservation(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.CreatedAt = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	m, cmd := h.admit(t, h.model(t))
	m, written := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)
	m, reserveCmd := m.handleFlowLaunchEvent(written)
	if reserveCmd == nil {
		t.Fatal("proven create did not request tracked reservation")
	}

	// A delete/recreate can preserve the exact ID and immutable form fields; the
	// creation timestamp is the generation marker that distinguishes the row.
	h.record.CreatedAt = h.record.CreatedAt.Add(time.Second)
	reserved := reserveCmd().(flowLaunchEventMsg)
	m, cmd = m.handleFlowLaunchEvent(reserved)
	m = drainCreateLaunch(t, m, cmd)

	if strings.Contains(strings.Join(h.order, ","), "worktree") || h.releases != 1 || m.activeFlowCreate != 0 {
		t.Fatalf("replacement result: order=%#v releases=%d active=%d", h.order, h.releases, m.activeFlowCreate)
	}
}

func TestCreateFlowLaunchRevalidatesLaunchProofAfterMetadata(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.metadataMutate = func(record *flowstore.FlowRecord) {
		record.Phases[0].Status = flowstore.PhaseBlocked
		record.Phases[0].LaunchIDs = nil
	}
	m := h.start(t, h.model(t))

	if len(h.contexts) != 0 || strings.Contains(strings.Join(h.order, ","), "terminal") {
		t.Fatalf("metadata proof drift spawned an agent: contexts=%#v order=%#v", h.contexts, h.order)
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "launch proof changed before embedded install") {
		t.Fatalf("metadata proof result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchRejectsSameIDReplacementAtPostBootstrapReread(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.CreatedAt = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	h.readMutate = func(record *flowstore.FlowRecord) {
		record.CreatedAt = record.CreatedAt.Add(time.Second)
	}
	m := h.start(t, h.model(t))

	joined := strings.Join(h.order, ",")
	for _, forbidden := range []string{"launch-id:", "metadata", "terminal", "block:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("replacement reread performed %q: %#v", forbidden, h.order)
		}
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("replacement reread result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchRejectsGenerationLossDuringLaunchIDProofReread(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.CreatedAt = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	h.addErr = errors.New("write failed")
	h.readMutate = func(record *flowstore.FlowRecord) {
		if h.readCalls == 2 {
			record.CreatedAt = record.CreatedAt.Add(time.Second)
		}
	}
	m := h.start(t, h.model(t))

	joined := strings.Join(h.order, ",")
	for _, forbidden := range []string{"metadata", "terminal", "block:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("proof reread generation loss performed %q: %#v", forbidden, h.order)
		}
	}
	if h.releases != 1 || m.activeFlowCreate != 0 || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("proof reread generation result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchCancellationDoesNotRecoverAfterGenerationLoss(t *testing.T) {
	for _, stage := range []flowLaunchStage{flowLaunchStageCreateBootstrap, flowLaunchStageCreateLaunchID} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			h.record.CreatedAt = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
			if stage == flowLaunchStageCreateLaunchID {
				h.addErr = errors.New("write failed")
			}
			h.readMutate = func(record *flowstore.FlowRecord) {
				wantCall := 1
				if stage == flowLaunchStageCreateLaunchID {
					wantCall = 2
				}
				if h.readCalls == wantCall {
					record.CreatedAt = record.CreatedAt.Add(time.Second)
				}
			}

			m, cmd := h.admit(t, h.model(t))
			m, event := advanceCreateLaunchToStage(t, m, cmd, stage)
			if !event.GenerationLost {
				t.Fatal("test event did not lose its Flow generation")
			}
			m.flowCreateSeq++
			m.activeFlowCreate = m.flowCreateSeq
			m = m.setStatusNow(statusOther, "newer creation status")
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			if len(h.phaseUpdates) != 0 || strings.Contains(strings.Join(h.order, ","), "metadata") {
				t.Fatalf("stale generation loss recovered replacement: order=%#v updates=%#v", h.order, h.phaseUpdates)
			}
			if h.releases != 1 || m.status.Text != "newer creation status" || m.activeFlowCreate == 0 {
				t.Fatalf("stale generation cleanup: releases=%d status=%q active=%d", h.releases, m.status.Text, m.activeFlowCreate)
			}
		})
	}
}

func TestCreateFlowLaunchParkedMetadataRejectsGenerationLoss(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhasePending}})
	h.record.CreatedAt = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	h.metadataMutate = func(record *flowstore.FlowRecord) {
		record.CreatedAt = record.CreatedAt.Add(time.Second)
	}
	m := h.start(t, h.model(t))

	if h.releases != 1 || m.activeFlowCreate != 0 || strings.Contains(m.status.Text, "Created flow") || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("parked generation result: releases=%d active=%d status=%q", h.releases, m.activeFlowCreate, m.status.Text)
	}
}

func TestCreateFlowLaunchCancellationMatrixPreservesNewerPresentation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stage        flowLaunchStage
		wantMetadata bool
		wantBlocks   int
		wantRelease  int
	}{
		{name: "after create", stage: flowLaunchStageCreateWritten},
		{name: "after reservation", stage: flowLaunchStageCreateReserved, wantBlocks: 2, wantRelease: 1},
		{name: "after worktree", stage: flowLaunchStageCreateWorktree, wantMetadata: true, wantBlocks: 2, wantRelease: 1},
		{name: "after bootstrap", stage: flowLaunchStageCreateBootstrap, wantMetadata: true, wantBlocks: 2, wantRelease: 1},
		{name: "after launch id", stage: flowLaunchStageCreateLaunchID, wantMetadata: true, wantBlocks: 2, wantRelease: 1},
		{name: "after metadata", stage: flowLaunchStageCreateMetadata, wantMetadata: true, wantBlocks: 1, wantRelease: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			m, cmd := h.admit(t, h.model(t))
			m, event := advanceCreateLaunchToStage(t, m, cmd, tc.stage)
			m.flowCreateSeq++
			m.activeFlowCreate = m.flowCreateSeq
			m = m.setStatusNow(statusOther, "newer creation status")
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			metadata := false
			for _, call := range h.order {
				metadata = metadata || call == "metadata"
			}
			if metadata != tc.wantMetadata || len(h.phaseUpdates) != tc.wantBlocks || h.releases != tc.wantRelease {
				t.Fatalf("cancellation result: order=%#v updates=%#v releases=%d", h.order, h.phaseUpdates, h.releases)
			}
			if m.status.Text != "newer creation status" || m.activeFlowCreate == 0 || len(h.contexts) != 0 {
				t.Fatalf("newer presentation changed: status=%q active=%d contexts=%#v", m.status.Text, m.activeFlowCreate, h.contexts)
			}
		})
	}
}

func TestCreateFlowLaunchAllocatedIDRefusesRetainedFlowSlots(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "repair"}[repair], func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			m := h.model(t)
			m.embeddedTerminals = []embeddedTerminalSlot{{ID: 1, Scope: embeddedTerminalScopeFlow, FlowID: h.record.FlowID, FlowRepair: repair, Terminal: flowPhaseLaunchTestTerminal{state: "running"}}}
			m, cmd := h.admit(t, m)
			allocated := cmd().(flowLaunchEventMsg)
			m, cmd = m.handleFlowLaunchEvent(allocated)
			if cmd != nil || m.activeFlowCreate != 0 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
				t.Fatalf("retained slot admission: cmd=%T active=%d attempt=%v", cmd, m.activeFlowCreate, m.flowLaunchAttemptOccupied(h.record.FlowID))
			}
			if !reflect.DeepEqual(h.order, []string{"allocate"}) {
				t.Fatalf("retained slot crossed admission: %#v", h.order)
			}
		})
	}
}

func TestCreateFlowLaunchParkedMetadataFailureDoesNotMutatePhases(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhasePending}})
	h.metadataErr = errors.New("disk full")
	m := h.start(t, h.model(t))
	if len(h.phaseUpdates) != 0 || len(h.contexts) != 0 || h.releases != 1 {
		t.Fatalf("parked metadata recovery: updates=%#v contexts=%#v releases=%d", h.phaseUpdates, h.contexts, h.releases)
	}
	if !strings.Contains(m.status.Text, "record start metadata: disk full") || m.activeFlowCreate != 0 {
		t.Fatalf("parked metadata status=%q active=%d", m.status.Text, m.activeFlowCreate)
	}
}

func TestCreateFlowLaunchInteractivePrefillKeepsOriginFenceUntilResult(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, prefill := startInteractiveCreateUntilPrefill(t, h)
	if m.activeFlowCreate != prefill.Create.Request {
		t.Fatalf("active create request = %d, want pending prefill request %d", m.activeFlowCreate, prefill.Create.Request)
	}
	if len(m.embeddedTerminals) != 1 || !m.embeddedTerminals[0].PrefillPending {
		t.Fatalf("pending terminals = %#v", m.embeddedTerminals)
	}

	// A repo change invalidates the original create request. Its delayed prefill
	// result must recover the phase without activating the old terminal or
	// replacing presentation for the newly selected repo.
	m.flowCreateSeq++
	m.activeFlowCreate = m.flowCreateSeq
	m = m.setStatusNow(statusOther, "new repo status")
	next, persistCmd := m.Update(prefill)
	m = next.(Model)
	if persistCmd == nil {
		t.Fatal("stale create prefill failure should persist phase recovery")
	}
	if m.activeTerminalNum != 0 || m.terminalFocus != terminalFocusList {
		t.Fatalf("stale prefill activated terminal: active=%d focus=%v", m.activeTerminalNum, m.terminalFocus)
	}
	if len(m.embeddedTerminals) != 0 {
		t.Fatalf("stale successful prefill left a hidden terminal: %#v", m.embeddedTerminals)
	}
	persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, persistCmd)
	settled, _ := m.Update(persisted)
	m = settled.(Model)
	if m.status.Text != "new repo status" {
		t.Fatalf("stale prefill status = %q, want newer presentation preserved", m.status.Text)
	}
	if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("stale prefill recovery = %#v", h.phaseUpdates)
	}
}

func TestCreateFlowLaunchInteractivePrefillSuccessCompletesRequestAndFocuses(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, prefill := startInteractiveCreateUntilPrefill(t, h)
	next, _ := m.Update(prefill)
	m = next.(Model)
	if m.activeFlowCreate != 0 || len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].PrefillPending {
		t.Fatalf("successful prefill result: active=%d terminals=%#v", m.activeFlowCreate, m.embeddedTerminals)
	}
	if m.activeTerminalNum != 1 {
		t.Fatalf("successful prefill active terminal = %d, want 1", m.activeTerminalNum)
	}
}

func TestCreateFlowLaunchMissingPrefillSlotRecoversPhaseAndCompletesRequest(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, prefill := startInteractiveCreateUntilPrefill(t, h)
	m = m.dismissEmbeddedTerminalForReason(prefill.ID, embeddedTerminalRemovalUserClose)
	next, cmd := m.Update(prefill)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing prefill slot should persist phase recovery")
	}
	persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, cmd)
	settled, _ := m.Update(persisted)
	m = settled.(Model)
	if m.activeFlowCreate != 0 || len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("missing-slot recovery: active=%d updates=%#v", m.activeFlowCreate, h.phaseUpdates)
	}
	if !strings.Contains(m.status.Text, "embedded terminal closed before prompt prefill completed") {
		t.Fatalf("missing-slot status = %q", m.status.Text)
	}
	if !strings.HasPrefix(m.status.Text, "Flow "+h.record.FlowID+": ") {
		t.Fatalf("missing-slot status = %q, want exact Flow ID prefix", m.status.Text)
	}
}

func TestCreateFlowLaunchPrefillFailureDoesNotMutatePhaseWhenAnotherAttemptWinsReservation(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, prefill := startInteractiveCreateUntilPrefill(t, h)
	newer := flowLaunchAttempt{Token: "newer-token", Kind: flowLaunchKindManualPhase, FlowID: h.record.FlowID}
	var ok bool
	m, ok = m.reserveFlowLaunchAttempt(newer, flowLaunchStateReading)
	if !ok {
		t.Fatal("test could not install competing exact-Flow attempt")
	}
	prefill.Err = errors.New("prefill failed")
	m, cmd := m.handleFlowLaunchPrefillFailure(prefill)
	if cmd != nil {
		t.Fatalf("old prefill failure returned command %T, want fenced cleanup only", cmd)
	}
	if m.activeFlowCreate != 0 {
		t.Fatalf("active create request = %d, want cleared", m.activeFlowCreate)
	}
	if len(h.phaseUpdates) != 0 {
		t.Fatalf("old prefill failure mutated phase owned by winner: %#v", h.phaseUpdates)
	}
	attempt, exists := m.flowLaunchAttempt(h.record.FlowID)
	if !exists || attempt.Token != newer.Token || attempt.State != flowLaunchStateReading {
		t.Fatalf("competing attempt changed: %#v exists=%v", attempt, exists)
	}
}

func TestCreateFlowLaunchPrefillTerminationFailureRetainsNondetachableOccupancy(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			term := &createPrefillFailureTerminal{
				flowPhaseLaunchTestTerminal: flowPhaseLaunchTestTerminal{state: "running"},
				terminateErr:                errors.New("terminate failed"),
			}
			h.terminal = term
			m, prefill := startInteractiveCreateUntilPrefill(t, h)
			if prefill.Err == nil || !prefill.RetainTerminal || term.terminates != 1 {
				t.Fatalf("prefill termination result = %#v terminates=%d", prefill, term.terminates)
			}
			if stale {
				m.flowCreateSeq++
				m.activeFlowCreate = m.flowCreateSeq
				m = m.setStatusNow(statusOther, "newer status")
			}
			next, cmd := m.Update(prefill)
			m = next.(Model)
			if cmd == nil || len(m.embeddedTerminals) != 1 {
				t.Fatalf("retained prefill failure: cmd=%T terminals=%#v", cmd, m.embeddedTerminals)
			}
			slot := m.embeddedTerminals[0]
			if slot.PrefillPending || slot.DetachPolicy != embeddedTerminalDetachNever || term.terminates != 1 {
				t.Fatalf("retained slot = %#v terminates=%d", slot, term.terminates)
			}
			if stale && (m.activeTerminalNum != 0 || m.terminalFocus != terminalFocusList) {
				t.Fatalf("stale retention changed focus: active=%d focus=%v", m.activeTerminalNum, m.terminalFocus)
			}
			persisted := commandMessageOfType[flowLaunchFailurePersistedMsg](t, cmd)
			settled, _ := m.Update(persisted)
			m = settled.(Model)
			if len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
				t.Fatalf("retained recovery = %#v", h.phaseUpdates)
			}
			if stale && m.status.Text != "newer status" {
				t.Fatalf("stale retained status = %q", m.status.Text)
			}
		})
	}
}
