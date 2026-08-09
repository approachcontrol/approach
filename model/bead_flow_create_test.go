package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/ui"
)

func TestBeadsReadyCreateFlowPersistsSelectedVisibleBeadRecordOnly(t *testing.T) {
	readyCalls := 0
	showCalls := 0
	createCalls := 0
	var createdInput flowstore.FlowRecord
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListReadyBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			readyCalls++
			if repoPath != "/dev/alpha" {
				t.Fatalf("Ready repo path = %q, want /dev/alpha", repoPath)
			}
			return []beadsquery.Bead{
				{ID: "bd-hidden", Title: "Other work"},
				{ID: "  bd-selected  ", Title: "  Selected work  "},
			}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ShowBead: func(string, string) (string, error) {
			showCalls++
			return "", nil
		},
		CreateFlowRecord: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			createCalls++
			createdInput = record
			record.FlowID = "flow-selected"
			return record, nil
		},
		CreateFlow: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Ready shortcut called legacy CreateFlow")
			return model.FlowStartResult{}, nil
		},
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Ready shortcut called StartFlowPlan")
			return model.FlowStartResult{}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			t.Fatal("Ready shortcut looked up a bootstrap hook")
			return actions.BootstrapHook{}, false
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			t.Fatal("Ready shortcut ran a bootstrap hook")
			return nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			t.Fatal("Ready shortcut launched an external agent")
			return actions.TerminalLaunchSpec{}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			t.Fatal("Ready shortcut launched an embedded agent")
			return nil, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if readyCmd == nil {
		t.Fatal("entering Ready returned nil query command")
	}
	m, _ = update(m, readyCmd())
	m = setBeadsQuery(t, m, "Selected")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd == nil {
		t.Fatal("Ready f returned nil create command")
	}
	msg := cmd()
	if _, ok := msg.(model.ReadyBeadFlowCreatedMsg); !ok {
		t.Fatalf("Ready create command returned %T, want ReadyBeadFlowCreatedMsg", msg)
	}
	m, _ = update(m, msg)

	if createCalls != 1 {
		t.Fatalf("CreateFlowRecord calls = %d, want 1", createCalls)
	}
	if readyCalls != 1 || showCalls != 0 {
		t.Fatalf("Beads calls after f = Ready %d Show %d, want 1 and 0", readyCalls, showCalls)
	}
	if createdInput.Title != "bd-selected: Selected work" {
		t.Fatalf("Flow title = %q", createdInput.Title)
	}
	wantInstructions := "Use Bead bd-selected as the durable source of requirements. Read it with `bd show bd-selected` before planning or implementation."
	if createdInput.Instructions != wantInstructions {
		t.Fatalf("Flow instructions = %q, want %q", createdInput.Instructions, wantInstructions)
	}
	if createdInput.RepoPath != "/dev/alpha" || createdInput.WorktreePath != "" || createdInput.Branch != "" ||
		createdInput.BaseRef != "" || createdInput.Commit != "" || createdInput.PlanID != "" || createdInput.PlanPath != "" ||
		createdInput.Issue != (flowstore.Issue{}) || createdInput.PR != (flowstore.PullRequest{}) || len(createdInput.Phases) != 0 {
		t.Fatalf("record-only Flow input = %#v", createdInput)
	}
	if got := m.TransientError(); got != "Created flow: bd-selected: Selected work" {
		t.Fatalf("status = %q", got)
	}
	if m.Mode() != ui.ModeBeadsReady {
		t.Fatalf("mode = %v, want Ready", m.Mode())
	}
}

func TestBeadsReadyCreateFlowRejectsInvalidAndDuplicateRequests(t *testing.T) {
	newReadyModel := func(t *testing.T, beads []beadsquery.Bead, available bool, create func(flowstore.FlowRecord) (flowstore.FlowRecord, error)) model.Model {
		t.Helper()
		m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
			ListReadyBeads:   func(string) ([]beadsquery.Bead, error) { return nil, nil },
			ListOpenBeads:    func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlowRecord: create,
			FetchRepo:        func(string) error { return nil },
		}))
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		return applyBeadsResult(t, m, ui.ModeBeadsReady, available, beads)
	}

	t.Run("invalid contexts", func(t *testing.T) {
		createCalls := 0
		create := func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			createCalls++
			return record, nil
		}
		assertNoCreate := func(t *testing.T, m model.Model, allowExistingCommand bool) {
			t.Helper()
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if cmd != nil && !allowExistingCommand {
				t.Fatalf("invalid Ready context returned command %T", cmd)
			}
			if cmd != nil {
				_ = immediateTestCommandMessages(cmd)
			}
			if createCalls != 0 {
				t.Fatalf("CreateFlowRecord calls = %d, want 0", createCalls)
			}
		}

		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
		assertNoCreate(t, m, true)

		for _, subview := range []rune{'b', 'o', 'i', 'c'} {
			m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview}})
			assertNoCreate(t, m, false)
		}

		m = inRightPane(model.NewWithOptions(testRepos(), model.Options{
			ListReadyBeads:   func(string) ([]beadsquery.Bead, error) { return nil, nil },
			ListOpenBeads:    func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlowRecord: create,
		}))
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		assertNoCreate(t, m, false) // loading
		assertNoCreate(t, newReadyModel(t, nil, false, create), false)
		assertNoCreate(t, newReadyModel(t, nil, true, create), false)
		m = newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
		m = setBeadsQuery(t, m, "no-match")
		assertNoCreate(t, m, false)
	})

	t.Run("duplicate in flight", func(t *testing.T) {
		createCalls := 0
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			createCalls++
			record.FlowID = "flow-1"
			return record, nil
		})
		m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		m, duplicate := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if first == nil || duplicate != nil {
			t.Fatalf("create commands = first %T duplicate %T, want non-nil then nil", first, duplicate)
		}
		msg := first()
		m, _ = update(m, msg)
		if createCalls != 1 {
			t.Fatalf("CreateFlowRecord calls = %d, want 1", createCalls)
		}
		_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if retry == nil {
			t.Fatal("accepted completion did not release Ready create")
		}
	})

	t.Run("failure releases request", func(t *testing.T) {
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("flow store unavailable")
		})
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		failed := cmd().(model.ReadyBeadFlowCreateFailedMsg)
		m, _ = update(m, failed)
		if got := m.TransientError(); got != "flow store unavailable" {
			t.Fatalf("failure status = %q", got)
		}
		_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if retry == nil {
			t.Fatal("accepted failure did not release Ready create")
		}
	})
}

func TestBeadsReadyCreateFlowRepoRoundTripInvalidatesStaleCompletion(t *testing.T) {
	for _, tt := range []struct {
		name        string
		activeFlows bool
	}{
		{name: "selected repo surface"},
		{name: "active flows surface", activeFlows: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			createCalls := 0
			m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
				},
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
				ListFlows:     func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
				CreateFlowRecord: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
					createCalls++
					record.FlowID = "flow-1"
					return record, nil
				},
			}))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			m, _ = update(m, readyCmd())
			m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			stale := createCmd().(model.ReadyBeadFlowCreatedMsg)
			if createCalls != 1 {
				t.Fatalf("initial CreateFlowRecord calls = %d, want 1", createCalls)
			}

			if tt.activeFlows {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
			m, staleCmd := update(m, stale)
			if staleCmd != nil || m.TransientError() != "" {
				t.Fatalf("stale completion changed UI: status=%q cmd=%T", m.TransientError(), staleCmd)
			}
			m, staleCmd = update(m, model.ReadyBeadFlowCreateFailedMsg{
				RepoPath: stale.RepoPath,
				Title:    stale.Title,
				Err:      "stale failure",
				Request:  stale.Request,
			})
			if staleCmd != nil || m.TransientError() != "" {
				t.Fatalf("stale failure changed UI: status=%q cmd=%T", m.TransientError(), staleCmd)
			}

			if tt.activeFlows {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
			} else {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			}
			m = applyBeadsResultFor(t, m, ui.ModeBeadsReady, "/dev/alpha", m.ListRequest(ui.ModeBeadsReady), true, []beadsquery.Bead{{ID: "bd-1", Title: "One"}})
			_, retryCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if retryCmd == nil {
				t.Fatal("repo round trip stranded Ready Flow creation")
			}
		})
	}
}

func TestBeadsReadyCreateFlowCompletionRefreshesOnlyVisibleFlowSurface(t *testing.T) {
	for _, tt := range []struct {
		name        string
		key         tea.KeyMsg
		wantRefresh int
	}{
		{name: "beads ready", key: tea.KeyMsg{}, wantRefresh: 0},
		{name: "selected repo flows", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}}, wantRefresh: 1},
		{name: "active flows", key: tea.KeyMsg{Type: tea.KeyCtrlA}, wantRefresh: 1},
		{name: "other surface", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}, wantRefresh: 0},
	} {
		for _, failed := range []bool{false, true} {
			outcome := "success"
			if failed {
				outcome = "failure"
			}
			t.Run(tt.name+"/"+outcome, func(t *testing.T) {
				listCalls := 0
				m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
					ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
						return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
					},
					ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
					ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						listCalls++
						return nil, nil
					},
					CreateFlowRecord: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
						if failed {
							return flowstore.FlowRecord{}, errors.New("persist failed")
						}
						record.FlowID = "flow-1"
						return record, nil
					},
				}))
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
				m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				m, _ = update(m, readyCmd())
				m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
				completion := createCmd()
				if tt.key.Type != 0 {
					m, _ = update(m, tt.key)
				}
				m, cmd := update(m, completion)
				_ = immediateTestCommandMessages(cmd)
				if listCalls != tt.wantRefresh {
					t.Fatalf("ListFlows calls = %d, want %d", listCalls, tt.wantRefresh)
				}
				wantStatus := "Created flow: bd-1: One"
				if failed {
					wantStatus = "persist failed"
				}
				if got := m.TransientError(); got != wantStatus {
					t.Fatalf("status = %q, want %q", got, wantStatus)
				}
			})
		}
	}
}

func TestBeadsReadyCreateFlowSameRepoFilterAndCursorChangesKeepRequestCurrent(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}, {ID: "bd-2", Title: "Two"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlowRecord: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			record.FlowID = "flow-1"
			return record, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m = setBeadsQuery(t, m, "Two")
	m, _ = update(m, createCmd())
	if got := m.TransientError(); got != "Created flow: bd-1: One" {
		t.Fatalf("completion after same-repo selection changes = %q", got)
	}
}

func TestBeadsReadyFlowCreateShortcutMatchesExecutablePredicate(t *testing.T) {
	newModel := func(t *testing.T) model.Model {
		t.Helper()
		m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
				return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
			},
			ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlowRecord: func(record flowstore.FlowRecord) (flowstore.FlowRecord, error) {
				record.FlowID = "flow-1"
				return record, nil
			},
		}))
		m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
		m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m, _ = update(m, readyCmd())
		return m
	}

	tests := []struct {
		name      string
		transform func(model.Model) model.Model
		want      bool
	}{
		{name: "available", transform: func(m model.Model) model.Model { return m }, want: true},
		{name: "left focused", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			return m
		}},
		{name: "non ready", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
			return m
		}},
		{name: "loading", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			return m
		}},
		{name: "unavailable", transform: func(m model.Model) model.Model {
			return applyBeadsResult(t, m, ui.ModeBeadsReady, false, nil)
		}},
		{name: "empty", transform: func(m model.Model) model.Model {
			return applyBeadsResult(t, m, ui.ModeBeadsReady, true, nil)
		}},
		{name: "filtered empty", transform: func(m model.Model) model.Model {
			return setBeadsQuery(t, m, "no-match")
		}},
		{name: "creating", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.transform(newModel(t))
			got := strings.Contains(ansi.Strip(m.View()), "new flow")
			if got != tt.want {
				t.Fatalf("new flow shortcut visible = %v, want %v:\n%s", got, tt.want, ansi.Strip(m.View()))
			}
		})
	}
}

func TestBeadsReadyCreateFlowProductionWiringPersistsConfiguredPresetWithoutStartMetadata(t *testing.T) {
	root := t.TempDir()
	preset := flowstore.Preset{
		Name: "ready-bead",
		Phases: []flowstore.PhaseSpec{
			{ID: "research", Title: "Research", Kind: flowstore.KindPlan},
			{ID: "execute", Title: "Execute", Kind: flowstore.KindImplementation, DependsOn: []string{"research"}},
			{ID: "deliver", Title: "Deliver", Kind: flowstore.KindPRCreation, DependsOn: []string{"execute"}},
		},
	}
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: root,
		FlowPresets:      []flowstore.Preset{preset},
		FlowPreset:       &preset,
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	_, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	created := createCmd().(model.ReadyBeadFlowCreatedMsg)

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: []flowstore.Preset{preset}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read(%q) error = %v", created.FlowID, err)
	}
	if record.PresetName != "ready-bead" || record.Title != "bd-1: One" || record.RepoPath != "/dev/alpha" {
		t.Fatalf("persisted Flow identity = %#v", record)
	}
	if record.WorktreePath != "" || record.Branch != "" || record.BaseRef != "" || record.Commit != "" ||
		record.PlanID != "" || record.PlanPath != "" || record.Issue != (flowstore.Issue{}) || record.PR != (flowstore.PullRequest{}) {
		t.Fatalf("persisted Flow has start/plan/link metadata: %#v", record)
	}
	if !record.AutoMode || record.Merge.Status != flowstore.MergePending {
		t.Fatalf("creation defaults = auto %v merge %#v", record.AutoMode, record.Merge)
	}
	phases := flowstore.OrderedPhases(record.Phases)
	if len(phases) != len(preset.Phases) {
		t.Fatalf("persisted phases = %#v", phases)
	}
	for i, spec := range preset.Phases {
		phase := phases[i]
		wantStatus := flowstore.PhasePending
		if i == 0 {
			wantStatus = flowstore.PhaseReady
		}
		if phase.PhaseID != spec.ID || phase.Title != spec.Title || phase.Kind != spec.Kind || phase.Status != wantStatus ||
			!equalStrings(phase.DependsOn, spec.DependsOn) {
			t.Fatalf("phase %d = %#v, want spec %#v status %q", i, phase, spec, wantStatus)
		}
		if len(phase.LaunchIDs) != 0 || len(phase.Sessions) != 0 || phase.Status == flowstore.PhaseRunning ||
			phase.Status == flowstore.PhaseNeedsAttention || phase.Status == flowstore.PhaseBlocked {
			t.Fatalf("phase %d has launch/session/startup-failure state: %#v", i, phase)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
