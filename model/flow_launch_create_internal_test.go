package model

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
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
	record                         flowstore.FlowRecord
	allocatedID                    string
	order                          []string
	contexts                       []actions.AgentLaunchContext
	phaseUpdates                   []flowstore.PhaseUpdate
	sessions                       []sessions.SessionRecord
	addErr                         error
	addPersists                    bool
	createErr                      error
	reserveErr                     error
	reserveReleaseOnError          bool
	reserveMutate                  func(*flowstore.FlowRecord)
	worktreeErr                    error
	bootstrapErr                   error
	preparationErr                 error
	bootstrapCheck                 func(flowstore.FlowRecord) error
	metadataErr                    error
	metadataPersists               bool
	metadataMutate                 func(*flowstore.FlowRecord)
	phaseErrs                      map[string]error
	phaseErrRemaining              map[string]int
	phasePersistErrs               map[string]error
	readErrs                       map[int]error
	readMutate                     func(*flowstore.FlowRecord)
	readCalls                      int
	terminalErr                    error
	terminal                       EmbeddedTerminal
	releases                       int
	releasesAtCompensate           int
	compensateIncompleteRemaining  int
	compensateReservationRemaining int
}

type createLaunchPreparationFinalizer struct {
	h     *createLaunchHarness
	nonce string
}

func (f createLaunchPreparationFinalizer) Finalize(callback func() error) (flowstore.FlowRecord, error) {
	if f.h.record.PreparationNonce != f.nonce {
		return f.h.record, errors.New("preparation generation changed")
	}
	if callback != nil {
		if err := callback(); err != nil {
			return f.h.record, errors.Join(flowstore.ErrPreparationIncomplete, err)
		}
	}
	if f.h.preparationErr != nil {
		return f.h.record, f.h.preparationErr
	}
	f.h.order = append(f.h.order, "finalize")
	stamp := time.Date(2026, time.August, 14, 12, 1, 0, 0, time.UTC)
	f.h.record.PreparedAt = &stamp
	return f.h.record, nil
}

func (f createLaunchPreparationFinalizer) CompensateUnderReservation(notes string) (flowstore.FlowRecord, error) {
	return f.Compensate(notes)
}

func (f createLaunchPreparationFinalizer) Compensate(notes string) (flowstore.FlowRecord, error) {
	if f.h.record.PreparationNonce != f.nonce {
		return f.h.record, errors.New("preparation generation changed")
	}
	f.h.releasesAtCompensate = f.h.releases
	f.h.order = append(f.h.order, "compensate")
	if f.h.compensateReservationRemaining > 0 {
		f.h.compensateReservationRemaining--
		return f.h.record, errors.Join(flowstore.ErrPreparationReservation, errors.New("timed out waiting for Flow launch/close lock"))
	}
	if f.h.compensateIncompleteRemaining > 0 {
		f.h.compensateIncompleteRemaining--
		return f.h.record, flowstore.ErrPreparationIncomplete
	}
	for _, root := range launchablePhases(f.h.record) {
		update := blockedPhaseUpdate(f.h.record.FlowID, root, notes)
		f.h.phaseUpdates = append(f.h.phaseUpdates, update)
		for i := range f.h.record.Phases {
			if f.h.record.Phases[i].PhaseID == root.PhaseID {
				f.h.record.Phases[i].Status = update.Status
				f.h.record.Phases[i].Notes = update.Notes
				f.h.record.Phases[i].Outcome = update.Outcome
			}
		}
	}
	return f.h.record, nil
}

func TestBeadsReadyStartRoutesCreatePhaseBeforeSideEffects(t *testing.T) {
	m := newModelForTest(nil, Options{
		AgentCommand: "codex",
	})
	cmd := m.requestReadyBeadFlowLaunch(
		"/dev/alpha",
		"bd-1: First",
		"Read bd-1.",
		flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"},
		1,
	)
	if cmd == nil {
		t.Fatal("Ready F returned no command")
	}
	msg, ok := cmd().(flowLaunchCreateRequestedMsg)
	if !ok {
		t.Fatalf("Ready F did not route a createPhase lifecycle request")
	}
	if msg.Create.Presentation != (flowLaunchCreatePresentation{Origin: flowLaunchOriginReadyBead, Request: 1}) ||
		msg.Create.RepoPath != "/dev/alpha" || msg.Create.Bead != (flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}) || !msg.Create.Headless {
		t.Fatalf("Ready F create request = %#v", msg.Create)
	}
	if msg.Settings.Command != "codex" {
		t.Fatalf("Ready F settings command = %q, want codex", msg.Settings.Command)
	}
}

func TestCreateFlowLaunchSourceSnapshotsSettingsBeforeQueuedRequestIsHandled(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m := h.model(t)
	m.codexModel = "gpt-5.5"
	m.codexReasoningEffort = "high"
	m.sessionStateRoot = "/state/request"
	m.flowPromptTemplates.Plan = "REQUEST TEMPLATE: {flow_title}"
	var request uint64
	m, request = m.nextFlowCreateRequest()
	cmd := tagFlowCreateRequest(m.createFlowAndLaunchPlanForRepo("/dev/alpha", "New Flow", "Write the plan.", "main", true), request)

	// A settings result already queued ahead of the create request belongs to
	// the next launch, not the request the user submitted under codex.
	m.agentCommand = "claude"
	m.codexModel = "gpt-5.6-sol"
	m.codexReasoningEffort = "low"
	m.sessionStateRoot = "/state/later"
	m.flowPromptTemplates.Plan = "LATER TEMPLATE"
	updated, launchCmd := m.Update(cmd())
	m = updated.(Model)
	m = drainCreateLaunch(t, m, launchCmd)

	if len(h.contexts) != 1 || h.contexts[0].Command != "codex" || h.contexts[0].Model != "gpt-5.5" ||
		h.contexts[0].ReasoningEffort != "high" || h.contexts[0].SessionStateRoot != "/state/request" ||
		!strings.Contains(h.contexts[0].InitialPrompt, "REQUEST TEMPLATE") {
		t.Fatalf("launch contexts = %#v, status=%q order=%#v, want submitting Model's codex snapshot", h.contexts, m.status.Text, h.order)
	}
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

	m := newModelForTest(nil, Options{
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
		Create: flowLaunchCreateRequest{Presentation: flowLaunchCreatePresentation{Origin: flowLaunchOriginNewFlow, Request: 1}, RepoPath: h.record.RepoPath},
	}
	m.flowCreateReq.current = attempt.Create.Presentation.Request
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
	m := newModelForTest([]scanner.Repo{{Path: "/dev/alpha", DisplayName: "alpha"}}, Options{
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
	create := func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, error) {
		h.order = append(h.order, "create:"+record.FlowID)
		if record.FlowID != h.record.FlowID || record.RepoPath != h.record.RepoPath || record.Bead != h.record.Bead || opts.Headless == nil || *opts.Headless != h.record.Headless {
			t.Fatalf("exact create = %#v, %#v", record, opts)
		}
		created := h.record
		created.Title, created.Instructions, created.BaseRef = record.Title, record.Instructions, record.BaseRef
		if h.createErr != nil {
			return flowstore.FlowRecord{}, h.createErr
		}
		h.record = created
		return created, nil
	}
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
		CreateFlow: create,
		CreatePreparation: func(record flowstore.FlowRecord, opts flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error) {
			created, err := create(record, opts)
			if err != nil {
				return flowstore.FlowRecord{}, nil, err
			}
			return created, createLaunchPreparationFinalizer{h: h, nonce: created.PreparationNonce}, nil
		},
		ReserveLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			h.order = append(h.order, "reserve:"+flowID)
			release := func() { h.releases++ }
			if h.reserveErr != nil {
				if h.reserveReleaseOnError {
					return flowstore.FlowRecord{}, release, h.reserveErr
				}
				return flowstore.FlowRecord{}, nil, h.reserveErr
			}
			record := h.record
			if h.reserveMutate != nil {
				h.reserveMutate(&record)
			}
			return record, release, nil
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
			if h.bootstrapCheck != nil {
				if err := h.bootstrapCheck(h.record); err != nil {
					return err
				}
			}
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
			if h.metadataErr != nil && !h.metadataPersists {
				return flowstore.FlowRecord{}, h.metadataErr
			}
			h.record.WorktreePath, h.record.Branch, h.record.Commit = update.WorktreePath, update.Branch, update.Commit
			if h.metadataMutate != nil {
				h.metadataMutate(&h.record)
			}
			if h.metadataErr != nil {
				return flowstore.FlowRecord{}, h.metadataErr
			}
			return h.record, nil
		},
		SetPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			h.order = append(h.order, "block:"+update.PhaseID)
			h.phaseUpdates = append(h.phaseUpdates, update)
			if remaining := h.phaseErrRemaining[update.PhaseID]; remaining > 0 {
				h.phaseErrRemaining[update.PhaseID]--
				return flowstore.FlowRecord{}, errors.New("database busy")
			}
			if err := h.phaseErrs[update.PhaseID]; err != nil {
				return flowstore.FlowRecord{}, err
			}
			if persistErr := h.phasePersistErrs[update.PhaseID]; persistErr != nil {
				for i := range h.record.Phases {
					if h.record.Phases[i].PhaseID == update.PhaseID {
						h.record.Phases[i].Status = update.Status
						h.record.Phases[i].Notes = update.Notes
						h.record.Phases[i].Outcome = update.Outcome
					}
				}
				return flowstore.FlowRecord{}, persistErr
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
	return h.admitSource(t, m, flowLaunchOriginNewFlow)
}

func (h *createLaunchHarness) admitSource(t *testing.T, m Model, origin flowLaunchOrigin) (Model, tea.Cmd) {
	t.Helper()
	var request uint64
	switch origin {
	case flowLaunchOriginNewFlow:
		m, request = m.nextFlowCreateRequest()
	case flowLaunchOriginReadyBead:
		m, request = m.nextReadyBeadFlowCreateRequest()
	default:
		t.Fatalf("unsupported create origin %v", origin)
	}
	create := flowLaunchCreateRequest{
		Presentation: flowLaunchCreatePresentation{Origin: origin, Request: request},
		RepoPath:     "/dev/alpha", Title: "New Flow", Instructions: "Write the plan.", Bead: h.record.Bead, BaseRef: "main", Headless: h.record.Headless,
	}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(""))
	next, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindCreatePhase, Origin: origin, Create: create, Settings: settings})
	if !admitted || cmd == nil {
		t.Fatalf("create admission = %v, cmd=%v, status=%q", admitted, cmd != nil, next.status.Text)
	}
	return next, cmd
}

func TestCreateFlowLaunchReadyOriginPersistsBeadAndUsesEmbeddedOnlyHandoff(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.record.Headless = true
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m = drainCreateLaunch(t, m, cmd)

	if len(h.contexts) != 1 || !h.contexts[0].Embedded || !h.contexts[0].Headless {
		t.Fatalf("Ready launch contexts = %#v", h.contexts)
	}
	if m.readyBeadFlowCreateReq.current != 0 || m.flowCreateReq.current != 0 || h.releases != 1 {
		t.Fatalf("Ready ownership: ready=%d new=%d releases=%d", m.readyBeadFlowCreateReq.current, m.flowCreateReq.current, h.releases)
	}
}

func TestCreateFlowLaunchReadyOriginFinalizesPreparationBeforeLaunchID(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}

	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m = drainCreateLaunch(t, m, cmd)

	if h.record.PreparedAt == nil {
		t.Fatalf("Ready launch Flow has no preparation receipt: %#v", h.record)
	}
	joined := strings.Join(h.order, ",")
	finalizeAt, launchAt := strings.Index(joined, "finalize"), strings.Index(joined, "launch-id:")
	if finalizeAt < 0 || launchAt < 0 || finalizeAt > launchAt {
		t.Fatalf("Ready launch order = %#v, want preparation finalization before launch ID", h.order)
	}
}

func TestCreateFlowLaunchReadyFreshnessIsSourceAwareAtEveryBoundary(t *testing.T) {
	stages := []flowLaunchStage{
		flowLaunchStageCreateSessionsRead,
		flowLaunchStageCreateWritten,
		flowLaunchStageCreateReserved,
		flowLaunchStageCreateWorktree,
		flowLaunchStageCreateBootstrap,
		flowLaunchStageCreateLaunchID,
		flowLaunchStageCreateMetadata,
	}
	for _, stage := range stages {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
			m, event := advanceCreateLaunchToStage(t, m, cmd, stage)
			m.readyBeadFlowCreateReq.next()
			m, newFlowRequest := m.nextFlowCreateRequest()
			m = m.setStatusNow(statusOther, "newer presentation")
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			if m.readyBeadFlowCreateReq.current == 0 || m.flowCreateReq.current != newFlowRequest || m.status.Text != "newer presentation" {
				t.Fatalf("source fence at %v: ready=%d new=%d status=%q", stage, m.readyBeadFlowCreateReq.current, m.flowCreateReq.current, m.status.Text)
			}
		})
	}
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
	h.bootstrapCheck = func(record flowstore.FlowRecord) error {
		if record.WorktreePath != "/dev/alpha-worktrees/flow-new" || record.Branch != "flow/new" || record.Commit != "abc123" {
			return fmt.Errorf("bootstrap observed start metadata %#v", record)
		}
		return nil
	}
	m := h.start(t, h.model(t))

	want := []string{
		"allocate", "sessions:" + h.record.FlowID, "create:" + h.record.FlowID, "reserve:" + h.record.FlowID,
		"worktree", "commit", "metadata", "bootstrap", "reread:" + h.record.FlowID,
		"launch-id:" + h.record.FlowID + ":plan:launch-create-1", "terminal",
	}
	if !reflect.DeepEqual(h.order, want) {
		t.Fatalf("create order = %#v, want %#v", h.order, want)
	}
	if len(h.contexts) != 1 {
		t.Fatalf("launch contexts = %#v", h.contexts)
	}
	ctx := h.contexts[0]
	// Embedded and FlowLaunchTracked are the builder's, not install's: this
	// asserts they survive the whole create route to the terminal unrewritten.
	if ctx.FlowID != h.record.FlowID || ctx.LaunchID != "launch-create-1" || ctx.FlowPhaseID != "plan" ||
		ctx.WorktreePath != "/dev/alpha-worktrees/flow-new" || ctx.Commit != "abc123" || !ctx.Embedded || !ctx.FlowLaunchTracked {
		t.Fatalf("launch context = %#v", ctx)
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || len(m.embeddedTerminals) != 1 {
		t.Fatalf("handoff ownership: releases=%d active=%d terminals=%d", h.releases, m.flowCreateReq.current, len(m.embeddedTerminals))
	}
}

func TestCreateFlowLaunchParksWithoutLaunchID(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "merge", Kind: flowstore.KindMerge, Status: flowstore.PhasePending}})
	m := h.start(t, h.model(t))
	if strings.Contains(strings.Join(h.order, ","), "launch-id:") || len(h.contexts) != 0 {
		t.Fatalf("parked Flow launched: order=%#v contexts=%#v", h.order, h.contexts)
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "Created flow") {
		t.Fatalf("parked result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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

	t.Run("unreadable joins block failure after metadata", func(t *testing.T) {
		h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
		h.addErr = errors.New("write failed")
		h.readErrs = map[int]error{2: errors.New("database busy")}
		h.phaseErrs = map[string]error{"plan": errors.New("database busy")}
		m := h.start(t, h.model(t))

		want := "Flow " + h.record.FlowID + ": AddPhaseLaunchID: write failed; reread flow after AddPhaseLaunchID: database busy; block phase plan: database busy"
		if m.status.Text != want {
			t.Fatalf("joined recovery status = %q, want %q", m.status.Text, want)
		}
		if len(h.phaseUpdates) != readyPreparationCompensationAttemptLimit || h.releases != 1 {
			t.Fatalf("unreadable recovery: updates=%#v releases=%d, want %d retried root-block writes", h.phaseUpdates, h.releases, readyPreparationCompensationAttemptLimit)
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

			if tc.name == "bootstrap" {
				if len(h.phaseUpdates) != 2 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[1].PhaseID != "implementation" {
					t.Fatalf("captured-root recovery = %#v", h.phaseUpdates)
				}
			} else if len(h.phaseUpdates) != 2 || h.phaseUpdates[0].PhaseID != "plan" || h.phaseUpdates[1].PhaseID != "implementation" {
				t.Fatalf("metadata failure did not fence captured roots: %#v", h.phaseUpdates)
			}
			if len(h.contexts) != 0 || h.releases != 1 || !strings.Contains(m.status.Text, tc.wantOperation) {
				t.Fatalf("recovery result: contexts=%#v releases=%d status=%q", h.contexts, h.releases, m.status.Text)
			}
			if tc.name == "metadata" {
				joined := strings.Join(h.order, ",")
				if strings.Contains(joined, "bootstrap") || strings.Contains(joined, "launch-id:") {
					t.Fatalf("metadata failure crossed into bootstrap or launch persistence: %#v", h.order)
				}
				if !strings.Contains(m.status.Text, "/dev/alpha-worktrees/flow-new") {
					t.Fatalf("metadata recovery status omitted surviving worktree: %q", m.status.Text)
				}
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

func TestCreateFlowLaunchRefusesEveryPreexistingSessionAssociationBeforeCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session sessions.SessionRecord
		status  string
	}{
		{name: "live", session: sessions.SessionRecord{SessionID: "s1", FlowID: "20260814T120000Z-new-flow", Status: "active"}, status: "active session"},
		{name: "ended", session: sessions.SessionRecord{SessionID: "s1", FlowID: "20260814T120000Z-new-flow", Status: "ended", EndedAt: time.Now()}, status: "saved session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
			h.sessions = []sessions.SessionRecord{tc.session}
			m := h.start(t, h.model(t))
			created := strings.Contains(strings.Join(h.order, ","), "create:"+h.record.FlowID)
			if created {
				t.Fatalf("preexisting %s crossed exact-ID creation: order=%#v", tc.name, h.order)
			}
			if m.flowCreateReq.current != 0 || h.releases != 0 || !strings.Contains(m.status.Text, tc.status) {
				t.Fatalf("%s refusal: status=%q active=%d releases=%d", tc.name, m.status.Text, m.flowCreateReq.current, h.releases)
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
	if m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "invalid ID") {
		t.Fatalf("invalid allocation result: active=%d status=%q", m.flowCreateReq.current, m.status.Text)
	}
}

func TestCreateFlowLaunchPreservesRequestTimeAgentSnapshotAcrossAllocation(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m := h.model(t)
	var request uint64
	m, request = m.nextFlowCreateRequest()
	create := flowLaunchCreateRequest{Presentation: flowLaunchCreatePresentation{Origin: flowLaunchOriginNewFlow, Request: request}, RepoPath: "/dev/alpha", Title: "New Flow", Instructions: "Write the plan.", BaseRef: "main", Headless: true}
	settings := snapshotFlowLaunchAgentSettings(m.flowLaunchLauncher(""))
	m, cmd, admitted := m.requestFlowLaunch(flowLaunchIntent{Kind: flowLaunchKindCreatePhase, Create: create, Settings: settings})
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
			if !strings.Contains(m.status.Text, tc.want) || m.flowCreateReq.current != 0 || h.releases != 0 {
				t.Fatalf("failure result: status=%q active=%d releases=%d", m.status.Text, m.flowCreateReq.current, h.releases)
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
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "claimed by another launch") {
		t.Fatalf("contention result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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

	if strings.Contains(strings.Join(h.order, ","), "worktree") || h.releases != 1 || m.flowCreateReq.current != 0 {
		t.Fatalf("replacement result: order=%#v releases=%d active=%d", h.order, h.releases, m.flowCreateReq.current)
	}
}

func TestCreateFlowLaunchRejectsPreparationNonceReplacementBeforeProvisioning(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.record.PreparationNonce = "old-generation"
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, written := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)
	m, reserveCmd := m.handleFlowLaunchEvent(written)
	if reserveCmd == nil {
		t.Fatal("proven preparation did not request tracked reservation")
	}

	h.record.PreparationNonce = "replacement-generation"
	reserved := reserveCmd().(flowLaunchEventMsg)
	m, cmd = m.handleFlowLaunchEvent(reserved)
	m = drainCreateLaunch(t, m, cmd)

	if slices.Contains(h.order, "worktree") || len(h.phaseUpdates) != 0 || h.releases != 1 {
		t.Fatalf("nonce replacement result: order=%#v updates=%#v releases=%d", h.order, h.phaseUpdates, h.releases)
	}
	if !strings.Contains(m.status.Text, "claimed by another launch") {
		t.Fatalf("nonce replacement status = %q", m.status.Text)
	}
	if slices.Contains(h.order, "compensate") {
		t.Fatalf("nonce replacement was already classified as claimed but still compensated: %#v", h.order)
	}
}

func TestCreateFlowLaunchRevalidatesStartupRootAfterMetadata(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.metadataMutate = func(record *flowstore.FlowRecord) {
		record.Phases[0].Status = flowstore.PhaseBlocked
		record.Phases[0].LaunchIDs = nil
	}
	m := h.start(t, h.model(t))

	if len(h.contexts) != 0 || strings.Contains(strings.Join(h.order, ","), "terminal") {
		t.Fatalf("metadata proof drift spawned an agent: contexts=%#v order=%#v", h.contexts, h.order)
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "validate startup root: no longer launchable") {
		t.Fatalf("metadata proof result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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
	for _, forbidden := range []string{"launch-id:", "terminal", "block:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("replacement reread performed %q: %#v", forbidden, h.order)
		}
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("replacement reread result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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
	for _, forbidden := range []string{"terminal", "block:"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("proof reread generation loss performed %q: %#v", forbidden, h.order)
		}
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("proof reread generation result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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
			m.flowCreateReq.next()
			m = m.setStatusNow(statusOther, "newer creation status")
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			if len(h.phaseUpdates) != 0 {
				t.Fatalf("stale generation loss recovered replacement: order=%#v updates=%#v", h.order, h.phaseUpdates)
			}
			if h.releases != 1 || m.status.Text != "newer creation status" || m.flowCreateReq.current == 0 {
				t.Fatalf("stale generation cleanup: releases=%d status=%q active=%d", h.releases, m.status.Text, m.flowCreateReq.current)
			}
		})
	}
}

func TestCreateFlowLaunchStaleMetadataFailureFencesSurvivingWorktree(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
	})
	h.metadataErr = errors.New("database busy")
	m, cmd := h.admit(t, h.model(t))
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateMetadata)
	if event.Err == "" {
		t.Fatal("test metadata event unexpectedly succeeded")
	}

	m.flowCreateReq.next()
	m = m.setStatusNow(statusOther, "newer creation status")
	m, cmd = m.handleFlowLaunchEvent(event)
	m = drainCreateLaunch(t, m, cmd)

	if len(h.phaseUpdates) != 2 {
		t.Fatalf("stale metadata recovery updates = %#v, want both captured roots fenced", h.phaseUpdates)
	}
	for _, update := range h.phaseUpdates {
		if !strings.Contains(update.Notes, "/dev/alpha-worktrees/flow-new") {
			t.Fatalf("stale metadata recovery omitted surviving worktree: %#v", update)
		}
	}
	if h.releases != 1 || m.status.Text != "newer creation status" || m.flowCreateReq.current == 0 {
		t.Fatalf("stale metadata cleanup: releases=%d status=%q active=%d", h.releases, m.status.Text, m.flowCreateReq.current)
	}
}

func TestCreateFlowLaunchStaleUnknownPreparationDoesNotCompensate(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.preparationErr = errors.Join(flowstore.ErrPreparationUnknown, errors.New("database busy"))
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateBootstrap)
	if !event.PreparationUnknown {
		t.Fatalf("test preparation event = %#v, want unknown outcome", event)
	}

	m.readyBeadFlowCreateReq.next()
	m = m.setStatusNow(statusOther, "newer Ready status")
	m, cmd = m.handleFlowLaunchEvent(event)
	m = drainCreateLaunch(t, m, cmd)

	if len(h.phaseUpdates) != 0 {
		t.Fatalf("stale unknown preparation compensated possibly durable receipt: %#v", h.phaseUpdates)
	}
	if h.releases != 1 || m.status.Text != "newer Ready status" || m.readyBeadFlowCreateReq.current == 0 {
		t.Fatalf("stale unknown cleanup: releases=%d status=%q active=%d", h.releases, m.status.Text, m.readyBeadFlowCreateReq.current)
	}
}

func TestCreateFlowLaunchStaleReadyWriteCompensatesLaunchableRoots(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
	})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)

	m = m.invalidateReadyBeadFlowCreateRequest()
	m, cmd = m.handleFlowLaunchEvent(event)
	m = drainCreateLaunch(t, m, cmd)

	if len(h.phaseUpdates) != 2 {
		t.Fatalf("stale Ready compensation updates = %#v, want every launchable root blocked", h.phaseUpdates)
	}
	if slices.Contains(h.order, "finalize") || slices.Contains(h.order, "worktree") {
		t.Fatalf("stale Ready compensation crossed into preparation side effects: %#v", h.order)
	}
	if m.flowPreparationAdmission || m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatal("stale Ready compensation stranded lifecycle ownership")
	}
}

func TestCreateFlowLaunchReadyReservationFailureCompensatesLaunchableRoots(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
			h.reserveErr = errors.New("database busy")
			h.reserveReleaseOnError = true

			m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
			if stale {
				var event flowLaunchEventMsg
				m, event = advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateReserved)
				m.readyBeadFlowCreateReq.next()
				m = m.setStatusNow(statusOther, "newer Ready status")
				m, cmd = m.handleFlowLaunchEvent(event)
			}
			m = drainCreateLaunch(t, m, cmd)

			if len(h.phaseUpdates) != 2 {
				t.Fatalf("Ready reservation compensation updates = %#v, want every launchable root blocked", h.phaseUpdates)
			}
			if slices.Contains(h.order, "finalize") || slices.Contains(h.order, "worktree") {
				t.Fatalf("Ready reservation compensation crossed into preparation side effects: %#v", h.order)
			}
			if stale && m.status.Text != "newer Ready status" {
				t.Fatalf("stale Ready reservation compensation status = %q", m.status.Text)
			}
			if !stale && !strings.Contains(m.status.Text, "reserve launch: database busy") {
				t.Fatalf("Ready reservation compensation status = %q", m.status.Text)
			}
			if m.flowPreparationAdmission || m.flowLaunchAttemptOccupied(h.record.FlowID) {
				t.Fatal("Ready reservation compensation stranded lifecycle ownership")
			}
			if h.releases != 1 {
				t.Fatalf("Ready reservation error releases = %d, want exactly one", h.releases)
			}
		})
	}
}

func TestCreateFlowLaunchReadyWrongReservationIdentityCompensatesCreatedFlow(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
			h.reserveMutate = func(record *flowstore.FlowRecord) { record.FlowID = "foreign-flow" }

			m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
			if stale {
				var event flowLaunchEventMsg
				m, event = advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateReserved)
				m.readyBeadFlowCreateReq.next()
				m = m.setStatusNow(statusOther, "newer Ready status")
				m, cmd = m.handleFlowLaunchEvent(event)
			}
			m = drainCreateLaunch(t, m, cmd)

			if len(h.phaseUpdates) != 2 || h.releases != 1 {
				t.Fatalf("wrong-ID compensation updates = %#v, releases = %d", h.phaseUpdates, h.releases)
			}
			if slices.Contains(h.order, "worktree") || slices.Contains(h.order, "finalize") {
				t.Fatalf("wrong-ID compensation crossed into preparation side effects: %#v", h.order)
			}
			if stale && m.status.Text != "newer Ready status" {
				t.Fatalf("stale wrong-ID compensation status = %q", m.status.Text)
			}
			if !stale && !strings.Contains(m.status.Text, "foreign-flow") {
				t.Fatalf("wrong-ID compensation status = %q", m.status.Text)
			}
		})
	}
}

func TestCreateFlowLaunchReadyPreMetadataExitsUsePreparationFinalizer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stage       flowLaunchStage
		worktreeErr error
		metadataErr error
		stale       bool
	}{
		{name: "stale after reservation", stage: flowLaunchStageCreateReserved, stale: true},
		{name: "worktree failure", stage: flowLaunchStageCreateWorktree, worktreeErr: errors.New("worktree failed")},
		{name: "stale after worktree", stage: flowLaunchStageCreateWorktree, stale: true},
		{name: "metadata failure", stage: flowLaunchStageCreateMetadata, metadataErr: errors.New("disk full")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
			h.worktreeErr = tc.worktreeErr
			h.metadataErr = tc.metadataErr
			m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
			m, event := advanceCreateLaunchToStage(t, m, cmd, tc.stage)
			if tc.stale {
				m = m.invalidateReadyBeadFlowCreateRequest()
			}
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			if !slices.Contains(h.order, "compensate") {
				t.Fatalf("pre-metadata exit bypassed preparation finalizer: %#v", h.order)
			}
			for _, call := range h.order {
				if strings.HasPrefix(call, "block:") {
					t.Fatalf("pre-metadata exit used unfenced SetPhase recovery: %#v", h.order)
				}
			}
			if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
				t.Fatalf("pre-metadata cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
			}
		})
	}
}

func TestCreateFlowLaunchReadyCompensationRetriesWhenReservationTimesOut(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
	})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.reserveErr = errors.New("database busy")
	h.compensateReservationRemaining = 1
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m = drainCreateLaunch(t, m, cmd)

	compensateCalls := 0
	for _, call := range h.order {
		if call == "compensate" {
			compensateCalls++
		}
	}
	if compensateCalls != 2 {
		t.Fatalf("Ready compensation calls = %d, want a retry after reservation timeout: %#v", compensateCalls, h.order)
	}
	if len(h.phaseUpdates) == 0 || h.phaseUpdates[0].Status != flowstore.PhaseBlocked {
		t.Fatalf("retry did not block startup roots: %#v", h.phaseUpdates)
	}
	if m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatal("reservation-timeout retry stranded lifecycle ownership")
	}
}

func TestCreateFlowLaunchReadyMetadataAcknowledgementContinuesWhenWriteLanded(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.metadataErr = errors.New("commit acknowledgement failed")
	h.metadataPersists = true
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m = drainCreateLaunch(t, m, cmd)

	if slices.Contains(h.order, "compensate") {
		t.Fatalf("landed metadata acknowledgement compensated the preparation: %#v", h.order)
	}
	if !slices.Contains(h.order, "finalize") || h.record.PreparedAt == nil {
		t.Fatalf("landed metadata acknowledgement did not continue finalization: %#v receipt=%v", h.order, h.record.PreparedAt)
	}
	if len(h.contexts) != 1 || h.releases != 1 {
		t.Fatalf("landed metadata acknowledgement result: contexts=%#v releases=%d", h.contexts, h.releases)
	}
}

func TestCreateFlowLaunchReadyMetadataUnreadDoesNotBypassFinalizer(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.metadataErr = errors.New("commit acknowledgement failed")
	h.readErrs = map[int]error{
		1: errors.New("database busy"),
		2: errors.New("database busy"),
		3: errors.New("database busy"),
	}
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m = drainCreateLaunch(t, m, cmd)

	if slices.Contains(h.order, "compensate") {
		t.Fatalf("unreadable metadata reread compensated a possibly landed write: %#v", h.order)
	}
	for _, call := range h.order {
		if strings.HasPrefix(call, "block:") {
			t.Fatalf("unreadable metadata reread used unfenced SetPhase recovery: %#v", h.order)
		}
	}
	if slices.Contains(h.order, "finalize") || h.record.PreparedAt != nil {
		t.Fatalf("unreadable metadata reread continued finalization: %#v", h.order)
	}
	if !strings.Contains(m.status.Text, "record start metadata: commit acknowledgement failed") || !strings.Contains(m.status.Text, "reread start metadata: database busy") {
		t.Fatalf("unreadable metadata status = %q", m.status.Text)
	}
	if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatalf("unreadable metadata cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
	}
}

func TestCreateFlowLaunchRecoveryDoesNotRetryAlreadyBlockedRoots(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
	})
	h.bootstrapErr = errors.New("hook failed")
	h.phasePersistErrs = map[string]error{"plan": errors.New("commit acknowledgement failed")}
	m := h.start(t, h.model(t))

	planBlocks := 0
	for _, update := range h.phaseUpdates {
		if update.PhaseID == "plan" {
			planBlocks++
		}
	}
	if planBlocks != 1 {
		t.Fatalf("durable plan block was retried: %#v", h.phaseUpdates)
	}
	if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatalf("durable root-block cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
	}
}

func TestCreateFlowLaunchRecoveryRetriesFailedBlockAfterLandedLaunchID(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.addErr = errors.New("write failed")
	h.addPersists = true
	h.phaseErrRemaining = map[string]int{"plan": 1}
	m := h.start(t, h.model(t))

	planBlocks := 0
	for _, update := range h.phaseUpdates {
		if update.PhaseID == "plan" {
			planBlocks++
		}
	}
	if planBlocks != 2 {
		t.Fatalf("running launch-ID root was not retried: %#v", h.phaseUpdates)
	}
	if h.releases != 1 || !strings.Contains(m.status.Text, "AddPhaseLaunchID: write failed") {
		t.Fatalf("landed launch-ID recovery: releases=%d status=%q", h.releases, m.status.Text)
	}
}

func TestCreateFlowLaunchRecoveryRetriesFailedRootBlocks(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
		{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
	})
	h.bootstrapErr = errors.New("hook failed")
	h.phaseErrRemaining = map[string]int{"plan": 1}
	m := h.start(t, h.model(t))

	planBlocks := 0
	implementationBlocks := 0
	for _, update := range h.phaseUpdates {
		switch update.PhaseID {
		case "plan":
			planBlocks++
		case "implementation":
			implementationBlocks++
		}
	}
	if planBlocks != 2 || implementationBlocks != 1 {
		t.Fatalf("root-block retries = %#v, want plan retried once and implementation blocked on the first pass", h.phaseUpdates)
	}
	if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatalf("root-block retry cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
	}
	if !strings.Contains(m.status.Text, "run bootstrap: hook failed") {
		t.Fatalf("root-block retry status = %q", m.status.Text)
	}
}

func TestCreateFlowLaunchReadyCompensationRetriesWhenWritesDoNotLand(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{
		{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
	})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.worktreeErr = errors.New("worktree failed")
	h.compensateIncompleteRemaining = 1
	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWorktree)
	m, cmd = m.handleFlowLaunchEvent(event)
	m = drainCreateLaunch(t, m, cmd)

	compensateCalls := 0
	for _, call := range h.order {
		if call == "compensate" {
			compensateCalls++
		}
	}
	if compensateCalls != 2 {
		t.Fatalf("Ready compensation calls = %d, want a retry after the unlanded write: %#v", compensateCalls, h.order)
	}
	if len(h.phaseUpdates) == 0 || h.phaseUpdates[0].Status != flowstore.PhaseBlocked {
		t.Fatalf("retry did not block startup roots: %#v", h.phaseUpdates)
	}
	if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatalf("retry cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
	}
}

func TestCreateFlowLaunchReadyWorktreeFailureHoldsReservationThroughCompensation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stage       flowLaunchStage
		worktreeErr error
		stale       bool
	}{
		{name: "worktree failure", stage: flowLaunchStageCreateWorktree, worktreeErr: errors.New("worktree failed")},
		{name: "stale after reservation", stage: flowLaunchStageCreateReserved, stale: true},
		{name: "stale after worktree", stage: flowLaunchStageCreateWorktree, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
			h.worktreeErr = tc.worktreeErr
			m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
			m, event := advanceCreateLaunchToStage(t, m, cmd, tc.stage)
			if tc.stale {
				m = m.invalidateReadyBeadFlowCreateRequest()
			}
			m, cmd = m.handleFlowLaunchEvent(event)
			m = drainCreateLaunch(t, m, cmd)

			if !slices.Contains(h.order, "compensate") {
				t.Fatalf("Ready compensation skipped: %#v", h.order)
			}
			if h.releasesAtCompensate != 0 {
				t.Fatalf("releases at Compensate = %d, want the launch reservation held through compensation", h.releasesAtCompensate)
			}
			if h.releases != 1 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
				t.Fatalf("Ready compensation cleanup releases=%d occupied=%t", h.releases, m.flowLaunchAttemptOccupied(h.record.FlowID))
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

	if h.releases != 1 || m.flowCreateReq.current != 0 || strings.Contains(m.status.Text, "Created flow") || !strings.Contains(m.status.Text, "flow generation changed") {
		t.Fatalf("parked generation result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
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
		{name: "after metadata", stage: flowLaunchStageCreateMetadata, wantMetadata: true, wantBlocks: 2, wantRelease: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCreateLaunchHarness([]flowstore.FlowPhase{
				{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady},
				{PhaseID: "implementation", Kind: flowstore.KindImplementation, Status: flowstore.PhaseReady},
			})
			m, cmd := h.admit(t, h.model(t))
			m, event := advanceCreateLaunchToStage(t, m, cmd, tc.stage)
			m.flowCreateReq.next()
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
			if tc.stage == flowLaunchStageCreateWorktree && h.record.Commit != "abc123" {
				t.Fatalf("stale post-worktree recovery commit = %q, want abc123", h.record.Commit)
			}
			if m.status.Text != "newer creation status" || m.flowCreateReq.current == 0 || len(h.contexts) != 0 {
				t.Fatalf("newer presentation changed: status=%q active=%d contexts=%#v", m.status.Text, m.flowCreateReq.current, h.contexts)
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
			if cmd != nil || m.flowCreateReq.current != 0 || m.flowLaunchAttemptOccupied(h.record.FlowID) {
				t.Fatalf("retained slot admission: cmd=%T active=%d attempt=%v", cmd, m.flowCreateReq.current, m.flowLaunchAttemptOccupied(h.record.FlowID))
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
	if !strings.Contains(m.status.Text, "record start metadata: disk full") || m.flowCreateReq.current != 0 {
		t.Fatalf("parked metadata status=%q active=%d", m.status.Text, m.flowCreateReq.current)
	}
}

func TestCreateFlowLaunchInteractivePrefillKeepsOriginFenceUntilResult(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, prefill := startInteractiveCreateUntilPrefill(t, h)
	if m.flowCreateReq.current != prefill.Create.Presentation.Request {
		t.Fatalf("active create request = %d, want pending prefill request %d", m.flowCreateReq.current, prefill.Create.Presentation.Request)
	}
	if len(m.embeddedTerminals) != 1 || !m.embeddedTerminals[0].PrefillPending {
		t.Fatalf("pending terminals = %#v", m.embeddedTerminals)
	}

	// A repo change invalidates the original create request. Its delayed prefill
	// result must recover the phase without activating the old terminal or
	// replacing presentation for the newly selected repo.
	m.flowCreateReq.next()
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
	if m.flowCreateReq.current != 0 || len(m.embeddedTerminals) != 1 || m.embeddedTerminals[0].PrefillPending {
		t.Fatalf("successful prefill result: active=%d terminals=%#v", m.flowCreateReq.current, m.embeddedTerminals)
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
	if m.flowCreateReq.current != 0 || len(h.phaseUpdates) != 1 || h.phaseUpdates[0].Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("missing-slot recovery: active=%d updates=%#v", m.flowCreateReq.current, h.phaseUpdates)
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
	if m.flowCreateReq.current != 0 {
		t.Fatalf("active create request = %d, want cleared", m.flowCreateReq.current)
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
				m.flowCreateReq.next()
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

// TestCreateFlowLaunchReadyOriginRefusesDuplicateBeadFlow pins the manual `F`
// path. The store refuses before anything is written, so nothing needs
// compensating — but finishCreateBeforeWrite does not refresh the Flow surface
// the way finishCreateAfterWrite does, so the conflict branch must chain that
// fetch itself.
func TestCreateFlowLaunchReadyOriginRefusesDuplicateBeadFlow(t *testing.T) {
	existing := flowstore.FlowRecord{
		FlowID: "20260816T025735Z-bd-1",
		Bead:   flowstore.BeadLink{ID: "bd-1"},
		Phases: []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseNeedsAttention}},
	}
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.createErr = &flowstore.BeadFlowActiveError{RepoPath: "/dev/alpha", BeadID: "bd-1", Existing: existing}

	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, written := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)
	if written.BeadFlowConflict.FlowID != existing.FlowID {
		t.Fatalf("event BeadFlowConflict.FlowID = %q, want %q", written.BeadFlowConflict.FlowID, existing.FlowID)
	}
	surfaceVisible := m.flowRefreshSurfaceVisible()
	next, followUp := m.handleFlowLaunchEvent(written)

	want := "Bead bd-1 already has a needs attention flow 20260816T025735Z-bd-1: close it from the Flows view with C"
	if next.status.Text != want {
		t.Fatalf("status = %q, want %q", next.status.Text, want)
	}
	if slices.Contains(h.order, "worktree") || len(h.phaseUpdates) != 0 || h.releases != 0 {
		t.Fatalf("refused create provisioned: order=%#v updates=%#v releases=%d", h.order, h.phaseUpdates, h.releases)
	}
	if next.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatal("refused create did not release the launch attempt")
	}
	if next.flowPreparationAdmission {
		t.Fatal("refused create did not release the Ready admission")
	}
	if !surfaceVisible {
		t.Fatal("harness did not have the Flow surface visible; the refresh assertion below would be vacuous")
	}
	if followUp == nil {
		t.Fatal("refused create did not issue the Flow surface fetch")
	}
}

// TestCreateFlowLaunchReadyOriginSurfacesUnreadableBeadRefusal pins the second
// refusal shape on the `F` path. No decodable Flow can be named, so the store's
// own text stands — but the write still never happened, so the attempt and the
// Ready admission must be released exactly as for the readable refusal.
func TestCreateFlowLaunchReadyOriginSurfacesUnreadableBeadRefusal(t *testing.T) {
	refusal := &flowstore.BeadFlowUnreadableError{
		RepoPath: "/dev/alpha", BeadID: "bd-1", FlowID: "20260816T025735Z-bd-1",
		Err: errors.New("unsupported schema version 9999"),
	}
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	h.record.Bead = flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}
	h.createErr = refusal

	m, cmd := h.admitSource(t, h.model(t), flowLaunchOriginReadyBead)
	m, written := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateWritten)
	if !written.BeadFlowRefused {
		t.Fatal("event did not mark the unreadable refusal; it would report a created Flow")
	}
	if written.BeadFlowConflict.FlowID != "" {
		t.Fatalf("event BeadFlowConflict.FlowID = %q, want empty", written.BeadFlowConflict.FlowID)
	}
	next, followUp := m.handleFlowLaunchEvent(written)

	if next.status.Text != refusal.Error() {
		t.Fatalf("status = %q, want the store refusal %q", next.status.Text, refusal.Error())
	}
	if strings.Contains(next.status.Text, "create flow ") {
		t.Fatalf("status = %q, but nothing was created", next.status.Text)
	}
	if slices.Contains(h.order, "worktree") || len(h.phaseUpdates) != 0 || h.releases != 0 {
		t.Fatalf("refused create provisioned: order=%#v updates=%#v releases=%d", h.order, h.phaseUpdates, h.releases)
	}
	if next.flowLaunchAttemptOccupied(h.record.FlowID) {
		t.Fatal("refused create did not release the launch attempt")
	}
	if next.flowPreparationAdmission {
		t.Fatal("refused create did not release the Ready admission")
	}
	if followUp == nil {
		t.Fatal("refused create did not issue the Flow surface fetch")
	}
}

// TestCreateFlowLaunchPreInstallFailureIgnoresTrackedFlags pins the assumption
// the createPhase builder's final transport flags rest on: the one failure
// window between building a createPhase context and installing it
// (failCreateFlowLaunchEmbedded) persists the same phase update whether or not
// that context carries FlowLaunchTracked and Embedded. flowLaunchFailureUpdate
// consults FlowLaunchTracked only for a resume, and a createPhase context
// carries no ResumeSessionID.
func TestCreateFlowLaunchPreInstallFailureIgnoresTrackedFlags(t *testing.T) {
	m := newModelForTest(nil, Options{AgentCommand: "codex"})
	base := launchContextCreatePhaseContext(launchContextCreatePhaseTarget())
	if base.ResumeSessionID != "" {
		t.Fatalf("createPhase context carries a resume session: %#v", base)
	}
	const errText = "launch proof changed before embedded install"
	want, ok := m.flowLaunchFailureUpdate(base, errText)
	if !ok || want.Status != flowstore.PhaseNeedsAttention || !strings.HasPrefix(want.Notes, "Agent launch failed") {
		t.Fatalf("baseline failure update = %#v, ok=%v", want, ok)
	}
	if want.Fence.LaunchID != base.LaunchID {
		t.Fatalf("baseline failure fence = %q, want launch ID %q", want.Fence.LaunchID, base.LaunchID)
	}
	for _, flags := range []struct {
		embedded bool
		tracked  bool
	}{{false, false}, {false, true}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("embedded=%v/tracked=%v", flags.embedded, flags.tracked), func(t *testing.T) {
			ctx := base
			ctx.Embedded, ctx.FlowLaunchTracked = flags.embedded, flags.tracked
			got, ok := m.flowLaunchFailureUpdate(ctx, errText)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("failure update = %#v (ok=%v), want %#v", got, ok, want)
			}
		})
	}
}

// TestCreateFlowLaunchProofChangeBeforeInstallBlocksPhase drives the same
// window end to end: the launch ID is written, but the phase is no longer
// running by the time the create stage reads it back, so the launch fails
// before install and regresses the phase it had claimed.
func TestCreateFlowLaunchProofChangeBeforeInstallBlocksPhase(t *testing.T) {
	h := newCreateLaunchHarness([]flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}})
	m, cmd := h.admit(t, h.model(t))
	m, event := advanceCreateLaunchToStage(t, m, cmd, flowLaunchStageCreateLaunchID)
	// The proof is present; only the status moved out from under it.
	event.Record.Phases = slices.Clone(event.Record.Phases)
	event.Record.Phases[0].Status = flowstore.PhaseBlocked
	m, cmd = m.handleFlowLaunchEvent(event)
	m = drainCreateLaunch(t, m, cmd)

	if strings.Contains(strings.Join(h.order, ","), "terminal") || len(h.contexts) != 0 {
		t.Fatalf("proof change installed a terminal: order=%#v contexts=%#v", h.order, h.contexts)
	}
	if len(h.phaseUpdates) != 1 {
		t.Fatalf("phase updates = %#v, want one", h.phaseUpdates)
	}
	update := h.phaseUpdates[0]
	if update.PhaseID != "plan" || update.Status != flowstore.PhaseNeedsAttention ||
		!strings.HasPrefix(update.Notes, "Agent launch failed") ||
		!strings.Contains(update.Notes, "launch proof changed before embedded install") {
		t.Fatalf("phase update = %#v", update)
	}
	if h.releases != 1 || m.flowCreateReq.current != 0 || !strings.Contains(m.status.Text, "launch proof changed before embedded install") {
		t.Fatalf("proof change result: releases=%d active=%d status=%q", h.releases, m.flowCreateReq.current, m.status.Text)
	}
}
