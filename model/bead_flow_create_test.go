package model_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

// A rescan-driven repo change never routes through handleRepoSelectionChanged,
// so it must still release the Ready create token instead of stranding `f`.
func TestBeadsReadyCreateFlowRescanRepoChangeDoesNotStrandShortcut(t *testing.T) {
	createCalls := 0
	var readyRepos []string
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{
				{Path: "/dev/bravo", DisplayName: "bravo"},
				{Path: "/dev/charlie", DisplayName: "charlie"},
			}, nil
		},
		ListReadyBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			readyRepos = append(readyRepos, repoPath)
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createCalls++
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())

	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if createCmd == nil {
		t.Fatal("Ready f returned nil create command")
	}
	stale := createCmd().(model.ReadyBeadFlowCreatedMsg)
	if createCalls != 1 {
		t.Fatalf("CreateFlow calls = %d, want 1", createCalls)
	}

	m, refreshCmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	if refreshCmd == nil {
		t.Fatal("f5 returned nil refresh command")
	}
	m, afterRefresh := update(m, repoRefreshResultFromBatch(t, immediateTestCommandMessages(refreshCmd)))
	if afterRefresh != nil {
		_ = immediateTestCommandMessages(afterRefresh)
	}
	if len(readyRepos) == 0 || readyRepos[len(readyRepos)-1] != "/dev/bravo" {
		t.Fatalf("rescan did not move the Ready query to the new repo: %#v", readyRepos)
	}

	m, staleCmd := update(m, stale)
	if staleCmd != nil || strings.Contains(m.TransientError(), "Created flow") {
		t.Fatalf("stale completion changed UI: status=%q cmd=%T", m.TransientError(), staleCmd)
	}

	m = applyBeadsResultFor(t, m, ui.ModeBeadsReady, "/dev/bravo", m.ListRequest(ui.ModeBeadsReady), true,
		[]beadsquery.Bead{{ID: "bd-2", Title: "Two"}})
	_, retryCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if retryCmd == nil {
		t.Fatal("rescan-driven repo change stranded Ready Flow creation")
	}
}

func TestBeadsReadyCreateFlowRequiresUsableBeadIDAndToleratesEmptyTitle(t *testing.T) {
	newReadyModel := func(t *testing.T, beads []beadsquery.Bead, create func(model.FlowStartRequest) (model.FlowStartResult, error)) model.Model {
		t.Helper()
		m := inBeadsPane(newTestModel(testRepos(), model.Options{
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			ListOpenBeads:  func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlow:     create,
		}))
		m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		return applyBeadsResult(t, m, ui.ModeBeadsReady, true, beads)
	}

	for _, tt := range []struct {
		name string
		bead beadsquery.Bead
	}{
		{name: "empty id", bead: beadsquery.Bead{ID: "", Title: "No identifier"}},
		{name: "whitespace id", bead: beadsquery.Bead{ID: "   ", Title: "No identifier"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			createCalls := 0
			m := newReadyModel(t, []beadsquery.Bead{tt.bead}, func(model.FlowStartRequest) (model.FlowStartResult, error) {
				createCalls++
				return model.FlowStartResult{}, nil
			})
			if strings.Contains(ansi.Strip(m.View()), "new flow") {
				t.Fatalf("unusable Bead ID advertised the Flow shortcut:\n%s", ansi.Strip(m.View()))
			}
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if cmd != nil {
				t.Fatalf("unusable Bead ID returned create command %T", cmd)
			}
			if createCalls != 0 {
				t.Fatalf("CreateFlow calls = %d, want 0", createCalls)
			}
		})
	}

	t.Run("empty title preserves the exact mapping", func(t *testing.T) {
		var createdRequest model.FlowStartRequest
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "   "}}, func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createdRequest = req
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		})
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if cmd == nil {
			t.Fatal("titleless Bead returned nil create command")
		}
		m, _ = update(m, cmd())
		if createdRequest.Title != "bd-1: " {
			t.Fatalf("Flow title = %q, want %q", createdRequest.Title, "bd-1: ")
		}
		if got := m.TransientError(); got != "Created flow: bd-1:" {
			t.Fatalf("status = %q", got)
		}
	})
}

func TestBeadsReadyCreateFlowAdaptersAreIndependentWhenInjectedAlone(t *testing.T) {
	for _, injected := range []string{"CreateFlow", "StartFlowPlan"} {
		t.Run(injected, func(t *testing.T) {
			testRoot := t.TempDir()
			repoPath := filepath.Join(testRoot, "repo")
			if err := os.MkdirAll(repoPath, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "-b", "main"},
				{"config", "user.email", "test@example.com"},
				{"config", "user.name", "Test User"},
			} {
				cmd := exec.Command("git", args...)
				cmd.Dir = repoPath
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v failed: %v\n%s", args, err, output)
				}
			}
			if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "fixture"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = repoPath
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v failed: %v\n%s", args, err, output)
				}
			}

			var calls []string
			opts := model.Options{
				AgentCommand:     "codex",
				SessionStateRoot: filepath.Join(testRoot, "state"),
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
				},
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			}
			switch injected {
			case "CreateFlow":
				opts.CreateFlow = func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					calls = append(calls, "create")
					return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "custom-create", RepoPath: req.RepoPath, Title: req.Title}}, nil
				}
			case "StartFlowPlan":
				opts.StartFlowPlan = func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					calls = append(calls, "start")
					return model.FlowStartResult{
						Flow:          flowstore.FlowRecord{FlowID: "custom-start", RepoPath: req.RepoPath, Title: req.Title},
						LaunchSkipped: true,
					}, nil
				}
			}
			repos := []scanner.Repo{{Path: repoPath, DisplayName: "repo"}}

			ready := inBeadsPane(newTestModel(repos, opts))
			ready, _ = update(ready, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			ready, _ = update(ready, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			ready, readyCmd := update(ready, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			if readyCmd == nil {
				t.Fatal("entering Ready returned nil query command")
			}
			ready, _ = update(ready, readyCmd())
			_, recordCmd := update(ready, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if recordCmd == nil {
				t.Fatal("Ready f returned nil create command")
			}
			if msg := recordCmd(); func() bool {
				_, ok := msg.(model.ReadyBeadFlowCreatedMsg)
				return ok
			}() == false {
				t.Fatalf("Ready command returned %T, want ReadyBeadFlowCreatedMsg", msg)
			}
			wantCalls := ""
			if injected == "CreateFlow" {
				wantCalls = "create"
			}
			if got := strings.Join(calls, ","); got != wantCalls {
				t.Fatalf("calls after Ready path = %q, want %q", got, wantCalls)
			}

			parked := inBeadsPane(newTestModel(repos, opts))
			parked, _ = update(parked, tea.KeyMsg{Type: tea.KeyTab})
			parked, _ = update(parked, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			_, parkedCmd := submitNewFlowPromptsWithCreateOptions(t, parked, "Parked "+injected, "Plan later", "main", true, false)
			if parkedCmd == nil {
				t.Fatal("Plan Now off returned nil command")
			}
			if msg := parkedCmd(); func() bool {
				_, failed := msg.(model.FlowCreateFailedMsg)
				return failed
			}() {
				t.Fatalf("Plan Now off failed: %#v", msg)
			}
			if injected == "CreateFlow" {
				wantCalls = "create,create"
			}
			if got := strings.Join(calls, ","); got != wantCalls {
				t.Fatalf("calls after parked path = %q, want %q", got, wantCalls)
			}

			planned := inBeadsPane(newTestModel(repos, opts))
			planned, _ = update(planned, tea.KeyMsg{Type: tea.KeyTab})
			planned, _ = update(planned, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			_, plannedCmd := submitNewFlowPrompts(t, planned, "Planned "+injected, "Plan now", "main")
			if plannedCmd == nil {
				t.Fatal("Plan Now returned nil command")
			}
			if msg := plannedCmd(); func() bool {
				_, failed := msg.(model.FlowCreateFailedMsg)
				return failed
			}() {
				t.Fatalf("Plan Now failed: %#v", msg)
			}
			if injected == "StartFlowPlan" {
				wantCalls = "start"
			}
			if got := strings.Join(calls, ","); got != wantCalls {
				t.Fatalf("calls after Plan Now path = %q, want %q", got, wantCalls)
			}
		})
	}
}

func TestBeadsReadyCreateFlowFailureWithoutDetailReportsFallback(t *testing.T) {
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	created := createCmd().(model.ReadyBeadFlowCreatedMsg)

	m, _ = update(m, model.ReadyBeadFlowCreateFailedMsg{
		RepoPath: created.RepoPath,
		Title:    created.Title,
		Err:      "   ",
		Request:  created.Request,
	})
	if got := m.TransientError(); got != "Unable to create flow" {
		t.Fatalf("detail-free failure status = %q, want %q", got, "Unable to create flow")
	}
}

func TestBeadsReadyStartFlowFailureSurfacesPersistedFlowAndReleasesReservation(t *testing.T) {
	releases := 0
	listCalls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
			listCalls++
			return nil, nil
		},
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{
				Flow:          flowstore.FlowRecord{FlowID: "flow-persisted", RepoPath: req.RepoPath, Title: req.Title},
				LaunchRelease: func() { releases++ },
			}, errors.New("launch bookkeeping failed")
		},
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 30})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	failed, ok := startCmd().(model.ReadyBeadFlowCreateFailedMsg)
	if !ok {
		t.Fatalf("Ready F returned %T, want ReadyBeadFlowCreateFailedMsg", failed)
	}
	if failed.FlowID != "flow-persisted" || releases != 1 {
		t.Fatalf("Ready F failure = %#v, reservation releases = %d", failed, releases)
	}
	m, refreshCmd := update(m, failed)
	_ = immediateTestCommandMessages(refreshCmd)
	if got := m.TransientError(); !strings.Contains(got, "launch bookkeeping failed") || !strings.Contains(got, "flow-persisted") {
		t.Fatalf("failure status does not identify persisted Flow: %q", got)
	}
	if listCalls != 1 {
		t.Fatalf("visible Flow refresh calls = %d, want 1", listCalls)
	}
}

func TestBeadsReadyCreateFlowPreparesSelectedVisibleBeadWithoutLaunch(t *testing.T) {
	readyCalls := 0
	showCalls := 0
	createCalls := 0
	var createdRequest model.FlowStartRequest
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeModel:           "claude-opus-5",
		ClaudeReasoningEffort: "high",
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
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createCalls++
			createdRequest = req
			return model.FlowStartResult{Flow: flowstore.FlowRecord{
				FlowID:       "flow-selected",
				Title:        req.Title,
				Instructions: req.Instructions,
				RepoPath:     req.RepoPath,
			}}, nil
		},
		StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Ready shortcut called StartFlowPlan")
			return model.FlowStartResult{}, nil
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
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
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
		t.Fatalf("CreateFlow calls = %d, want 1", createCalls)
	}
	if readyCalls != 1 || showCalls != 0 {
		t.Fatalf("Beads calls after f = Ready %d Show %d, want 1 and 0", readyCalls, showCalls)
	}
	if createdRequest.Title != "bd-selected: Selected work" {
		t.Fatalf("Flow title = %q", createdRequest.Title)
	}
	wantInstructions := "Use Bead bd-selected as the durable source of requirements. Read it with `bd show bd-selected` before planning or implementation."
	if createdRequest.Instructions != wantInstructions {
		t.Fatalf("Flow instructions = %q, want %q", createdRequest.Instructions, wantInstructions)
	}
	if createdRequest.RepoPath != "/dev/alpha" || createdRequest.BaseRef != "" ||
		createdRequest.AgentCommand != "claude" || createdRequest.Model != "claude-opus-5" || createdRequest.ReasoningEffort != "high" ||
		createdRequest.SessionStateRoot != "" || createdRequest.FlowPromptTemplates != (model.FlowPromptTemplates{}) ||
		createdRequest.FlowPromptTemplatesProvided ||
		createdRequest.PlanPhaseID != "" || createdRequest.PlanPhaseTitle != "" || createdRequest.PlanPhaseStatus != "" ||
		createdRequest.Headless != nil {
		t.Fatalf("Ready create request did not preserve phase settings without launch-only metadata: %#v", createdRequest)
	}
	if got := m.TransientError(); got != "Created flow: bd-selected: Selected work" {
		t.Fatalf("status = %q", got)
	}
	if m.Mode() != ui.ModeBeadsReady {
		t.Fatalf("mode = %v, want Ready", m.Mode())
	}
}

func TestBeadsReadyFlowRequestsCarryNormalizedSelectedBeadLink(t *testing.T) {
	for _, key := range []rune{'f', 'F'} {
		for _, tt := range []struct {
			name   string
			parent string
			want   flowstore.BeadLink
		}{
			{name: "known epic", parent: "  epic-parent  ", want: flowstore.BeadLink{ID: "bead-child", EpicID: "epic-parent"}},
			{name: "unknown epic", parent: "  ", want: flowstore.BeadLink{ID: "bead-child"}},
		} {
			t.Run(string(key)+"/"+tt.name, func(t *testing.T) {
				var captured model.FlowStartRequest
				showCalls := 0
				opts := model.Options{
					AgentCommand:   "codex",
					ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
					ListOpenBeads:  func(string) ([]beadsquery.Bead, error) { return nil, nil },
					ShowBead: func(string, string) (string, error) {
						showCalls++
						return "", nil
					},
					CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
						captured = req
						return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-f", RepoPath: req.RepoPath}}, nil
					},
					StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
						captured = req
						return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-F", RepoPath: req.RepoPath}, LaunchSkipped: true}, nil
					},
				}
				m := inBeadsPane(newTestModel(testRepos(), opts))
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "  bead-child  ", Parent: tt.parent, Title: "Child"}})

				_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
				if cmd == nil {
					t.Fatalf("Ready %c returned nil command", key)
				}
				_ = cmd()
				if captured.Bead != tt.want {
					t.Fatalf("Ready %c request Bead = %#v, want %#v", key, captured.Bead, tt.want)
				}
				if showCalls != 0 {
					t.Fatalf("Ready %c called ShowBead %d times", key, showCalls)
				}
			})
		}
	}
}

func TestBeadsReadyCreateFlowRejectsInvalidAndDuplicateRequests(t *testing.T) {
	newReadyModel := func(t *testing.T, beads []beadsquery.Bead, available bool, create func(model.FlowStartRequest) (model.FlowStartResult, error)) model.Model {
		t.Helper()
		m := inBeadsPane(newTestModel(testRepos(), model.Options{
			AgentCommand:   "codex",
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			ListOpenBeads:  func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlow:     create,
			FetchRepo:      func(string) error { return nil },
		}))
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		return applyBeadsResult(t, m, ui.ModeBeadsReady, available, beads)
	}

	t.Run("invalid contexts", func(t *testing.T) {
		createCalls := 0
		create := func(model.FlowStartRequest) (model.FlowStartResult, error) {
			createCalls++
			return model.FlowStartResult{}, nil
		}
		assertNoCreate := func(t *testing.T, m model.Model) {
			t.Helper()
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			if cmd != nil {
				t.Fatalf("invalid Ready context returned command %T", cmd)
			}
			if createCalls != 0 {
				t.Fatalf("CreateFlow calls = %d, want 0", createCalls)
			}
		}

		// Left-pane focus must keep `f` on the repo-fetch binding, not merely
		// avoid Flow creation.
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
		_, leftCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if leftCmd == nil {
			t.Fatal("left-pane f lost the repo fetch binding")
		}
		fetched := false
		for _, msg := range immediateTestCommandMessages(leftCmd) {
			if _, ok := msg.(model.VisibleRepoFetchResultMsg); ok {
				fetched = true
			}
		}
		if !fetched {
			t.Fatal("left-pane f did not run the visible repo fetch")
		}
		if createCalls != 0 {
			t.Fatalf("CreateFlow calls = %d, want 0", createCalls)
		}

		for _, subview := range []rune{'b', 'o', 'i', 'c'} {
			m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview}})
			assertNoCreate(t, m)
		}

		m = inBeadsPane(newTestModel(testRepos(), model.Options{
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			ListOpenBeads:  func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlow:     create,
		}))
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		assertNoCreate(t, m) // loading
		assertNoCreate(t, newReadyModel(t, nil, false, create))
		assertNoCreate(t, newReadyModel(t, nil, true, create))
		m = newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, create)
		m = setBeadsQuery(t, m, "no-match")
		assertNoCreate(t, m)
	})

	t.Run("duplicate in flight", func(t *testing.T) {
		createCalls := 0
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			createCalls++
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		})
		m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		m, duplicate := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if first == nil || duplicate != nil {
			t.Fatalf("create commands = first %T duplicate %T, want non-nil then nil", first, duplicate)
		}
		msg := first()
		m, _ = update(m, msg)
		if createCalls != 1 {
			t.Fatalf("CreateFlow calls = %d, want 1", createCalls)
		}
		_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if retry == nil {
			t.Fatal("accepted completion did not release Ready create")
		}
	})

	t.Run("mixed actions share admission", func(t *testing.T) {
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		})
		m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		_, mixed := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
		if first == nil || mixed != nil {
			t.Fatalf("mixed create commands = first %T second %T, want non-nil then nil", first, mixed)
		}
	})

	t.Run("failure releases request", func(t *testing.T) {
		m := newReadyModel(t, []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, true, func(model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-persisted"}}, errors.New("worktree unavailable")
		})
		m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		failed := cmd().(model.ReadyBeadFlowCreateFailedMsg)
		m, _ = update(m, failed)
		if got := m.TransientError(); got != "Flow flow-persisted was created, but preparation failed: worktree unavailable" {
			t.Fatalf("failure status = %q", got)
		}
		_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
		if retry == nil {
			t.Fatal("accepted failure did not release Ready create")
		}
	})
}

func TestBeadsReadyStartKeyOwnershipConsumesUnavailableAndEarlyInput(t *testing.T) {
	startCalls := 0
	newReady := func(command string) model.Model {
		m := inBeadsPane(newTestModel(testRepos(), model.Options{
			AgentCommand: command,
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
				return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
			},
			ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
				return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
			},
			StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
				startCalls++
				return model.FlowStartResult{}, nil
			},
		}))
		m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m, _ = update(m, readyCmd())
		return m
	}

	t.Run("missing agent", func(t *testing.T) {
		m := newReady("   ")
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "new flow") || strings.Contains(view, "new flow + start") || strings.Contains(view, "pull") {
			t.Fatalf("missing-agent Ready shortcuts are wrong:\n%s", view)
		}
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
		if cmd != nil || startCalls != 0 {
			t.Fatalf("owned missing-agent F returned %T and started %d times", cmd, startCalls)
		}
	})

	for _, tt := range []struct {
		name string
		open func(model.Model) model.Model
	}{
		{name: "search", open: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			return m
		}},
		{name: "picker", open: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
			return m
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.open(newReady("codex"))
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
			if cmd != nil || startCalls != 0 {
				t.Fatalf("%s F returned %T and started %d times", tt.name, cmd, startCalls)
			}
		})
	}

	t.Run("focused embedded terminal", func(t *testing.T) {
		fakeTerm := &fakeEmbeddedTerminal{state: "running"}
		startCalls := 0
		m := inBeadsPane(newTestModel(testRepos(), model.Options{
			AgentCommand: "codex",
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
				return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
			},
			ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			StartFlowPlan: func(model.FlowStartRequest) (model.FlowStartResult, error) {
				startCalls++
				return model.FlowStartResult{}, nil
			},
			StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
				return fakeTerm, nil
			},
		}))
		m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 30})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		m, _ = update(m, readyCmd())
		m, _ = update(m, model.FlowEmbeddedLaunchRequestedMsg{LaunchContext: actions.AgentLaunchContext{
			Command: "codex", FlowID: "flow-existing", FlowPhaseID: "implementation",
		}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
		if cmd != nil || startCalls != 0 {
			t.Fatalf("terminal-focused F returned %T and started %d Ready flows", cmd, startCalls)
		}
		if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "F" {
			t.Fatalf("terminal-focused F writes = %#v, want forwarded input", fakeTerm.writes)
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
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
				},
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
				ListFlows:     func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil },
				CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					createCalls++
					return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
				},
			}))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			m, _ = update(m, readyCmd())
			m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			stale := createCmd().(model.ReadyBeadFlowCreatedMsg)
			if createCalls != 1 {
				t.Fatalf("initial CreateFlow calls = %d, want 1", createCalls)
			}

			if tt.activeFlows {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
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

func TestBeadsReadyCreateFlowCompletionRefreshesVisibleFlowSurface(t *testing.T) {
	for _, tt := range []struct {
		name        string
		keys        []tea.KeyMsg
		height      int
		wantRefresh int
	}{
		{name: "beads ready with background flows visible", height: 30, wantRefresh: 1},
		{name: "selected repo flows", height: 30, keys: []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyRunes, Runes: []rune{'3'}}}, wantRefresh: 1},
		{name: "active flows", height: 30, keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}}, wantRefresh: 1},
		{name: "other surface with background flows hidden", height: 20, keys: []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'1'}}}, wantRefresh: 0},
	} {
		for _, failed := range []bool{false, true} {
			outcome := "success"
			if failed {
				outcome = "failure"
			}
			t.Run(tt.name+"/"+outcome, func(t *testing.T) {
				listCalls := 0
				m := inBeadsPane(newTestModel(testRepos(), model.Options{
					ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
						return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
					},
					ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
					ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						listCalls++
						return nil, nil
					},
					CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
						if failed {
							return model.FlowStartResult{}, errors.New("persist failed")
						}
						return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
					},
				}))
				m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: tt.height})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
				m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				m, _ = update(m, readyCmd())
				m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
				completion := createCmd()
				for _, key := range tt.keys {
					m, _ = update(m, key)
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
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}, {ID: "bd-2", Title: "Two"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
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
		m := inBeadsPane(newTestModel(testRepos(), model.Options{
			ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
				return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
			},
			ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
			CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
				return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title}}, nil
			},
		}))
		m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
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
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
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
		{name: "search active", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			return m
		}},
		{name: "agent picker open", transform: func(m model.Model) model.Model {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
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

func TestBeadsReadyCreateFlowPickerConsumesFWithoutCreating(t *testing.T) {
	createCalls := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			createCalls++
			return model.FlowStartResult{}, nil
		},
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if cmd != nil {
		_ = immediateTestCommandMessages(cmd)
	}
	if createCalls != 0 {
		t.Fatalf("CreateFlow calls = %d, want 0", createCalls)
	}
}

func TestBeadsReadyCreateFlowProductionWiringCreatesWorktreeWithStartMetadata(t *testing.T) {
	root := t.TempDir()
	preset := flowstore.Preset{
		Name: "ready-bead",
		Phases: []flowstore.PhaseSpec{
			{ID: "research", Title: "Research", Kind: flowstore.KindPlan},
			{ID: "execute", Title: "Execute", Kind: flowstore.KindImplementation, DependsOn: []string{"research"}},
			{ID: "deliver", Title: "Deliver", Kind: flowstore.KindPRCreation, DependsOn: []string{"execute"}},
		},
	}
	m, repoPath := setupModelRepoWithOptions(t, model.Options{
		SessionStateRoot:      root,
		AgentCommand:          "claude",
		ClaudeModel:           "claude-opus-5",
		ClaudeReasoningEffort: "high",
		FlowPresets:           []flowstore.Preset{preset},
		FlowPreset:            &preset,
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Parent: "epic-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	})
	m = inBeadsPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	_, createCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	created, ok := createCmd().(model.ReadyBeadFlowCreatedMsg)
	if !ok {
		t.Fatalf("Ready create command did not return ReadyBeadFlowCreatedMsg")
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: []flowstore.Preset{preset}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read(%q) error = %v", created.FlowID, err)
	}
	if record.PresetName != "ready-bead" || record.Title != "bd-1: One" || record.RepoPath != repoPath {
		t.Fatalf("persisted Flow identity = %#v", record)
	}
	if record.Bead != (flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}) {
		t.Fatalf("persisted Flow Bead = %#v", record.Bead)
	}
	wantWorktree := filepath.Join(filepath.Dir(repoPath), filepath.Base(repoPath)+"-worktrees", "flow-bd-1-one")
	if record.WorktreePath != wantWorktree || record.Branch != "flow/bd-1-one" {
		t.Fatalf("persisted Flow worktree = %q branch = %q, want %q and %q", record.WorktreePath, record.Branch, wantWorktree, "flow/bd-1-one")
	}
	if info, err := os.Stat(record.WorktreePath); err != nil || !info.IsDir() {
		t.Fatalf("Flow worktree missing on disk: %v", err)
	}
	if head := gitOut(t, repoPath, "rev-parse", "HEAD"); record.Commit != head {
		t.Fatalf("persisted Flow commit = %q, want repo HEAD %q", record.Commit, head)
	}
	if record.BaseRef != "" ||
		record.PlanID != "" || record.PlanPath != "" || record.Issue != (flowstore.Issue{}) || record.PR != (flowstore.PullRequest{}) {
		t.Fatalf("persisted Flow has base-ref/plan/GitHub metadata: %#v", record)
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
		wantAgent := (flowstore.PhaseAgentSettings{Agent: "claude", Model: "claude-opus-5", ReasoningEffort: "high"})
		if got := phase.AgentSettings(); got != wantAgent {
			t.Fatalf("phase %d agent settings = %#v, want %#v", i, got, wantAgent)
		}
		if len(phase.LaunchIDs) != 0 || len(phase.Sessions) != 0 || phase.Status == flowstore.PhaseRunning ||
			phase.Status == flowstore.PhaseNeedsAttention || phase.Status == flowstore.PhaseBlocked {
			t.Fatalf("phase %d has launch/session/startup-failure state: %#v", i, phase)
		}
	}
}

func TestBeadsReadyStartFlowProductionWiringLaunchesFirstActionablePhase(t *testing.T) {
	root := t.TempDir()
	preset := flowstore.Preset{
		Name: "ready-bead-start",
		Phases: []flowstore.PhaseSpec{
			{ID: "research", Title: "Research", Kind: flowstore.KindPlan},
			{ID: "execute", Title: "Execute", Kind: flowstore.KindImplementation, DependsOn: []string{"research"}},
		},
	}
	newStore := func() *flowstore.Store {
		t.Helper()
		store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Presets: []flowstore.Preset{preset}})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		return store
	}
	m, repoPath := setupModelRepoWithOptions(t, model.Options{
		SessionStateRoot:     root,
		AgentCommand:         "codex",
		CodexModel:           "gpt-5.5",
		CodexReasoningEffort: "high",
		FlowPresets:          []flowstore.Preset{preset},
		FlowPreset:           &preset,
		FlowPromptTemplates:  model.FlowPromptTemplates{Plan: "Ready plan {flow_id}: {instructions}"},
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Parent: "epic-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ReserveFlowLaunch: func(flowID string) (flowstore.FlowRecord, func(), error) {
			return newStore().ReserveAgentLaunch(flowID)
		},
	})
	m = inBeadsPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	_, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if startCmd == nil {
		t.Fatal("Ready F returned nil start command")
	}
	raw := startCmd()
	handoff, ok := raw.(model.FlowEmbeddedLaunchRequestedMsg)
	if !ok {
		t.Fatalf("Ready F returned %T, want FlowEmbeddedLaunchRequestedMsg", raw)
	}
	if handoff.LaunchRelease == nil {
		t.Fatal("production Ready F did not carry its launch reservation")
	}
	ctx := handoff.LaunchContext
	if handoff.ReadyBeadRequest == 0 || handoff.Request != 0 ||
		ctx.RepoPath != repoPath || ctx.FlowPhaseID != "research" || ctx.PlanPhaseID != "research" ||
		ctx.FlowPhaseKind != flowstore.KindPlan || !ctx.Embedded || !ctx.FlowLaunchTracked {
		t.Fatalf("production Ready F handoff = %#v", handoff)
	}
	if ctx.Command != "codex" || ctx.Model != "gpt-5.5" || ctx.ReasoningEffort != "high" || !ctx.Headless {
		t.Fatalf("production Ready F launch settings = %#v", ctx)
	}
	if !strings.Contains(ctx.InitialPrompt, "Ready plan "+ctx.FlowID) || !strings.Contains(ctx.InitialPrompt, "bd-1") {
		t.Fatalf("production Ready F prompt = %q", ctx.InitialPrompt)
	}
	record, err := newStore().Read(ctx.FlowID)
	if err != nil {
		t.Fatalf("Read(%q) error = %v", ctx.FlowID, err)
	}
	if record.Bead != (flowstore.BeadLink{ID: "bd-1", EpicID: "epic-1"}) {
		t.Fatalf("production Ready F persisted Bead = %#v", record.Bead)
	}
	phase := phaseByID(record, "research")
	if phase.Status != flowstore.PhaseRunning || len(phase.LaunchIDs) != 1 || phase.LaunchIDs[0] != ctx.LaunchID {
		t.Fatalf("production Ready F phase = %#v", phase)
	}
	wantAgent := flowstore.PhaseAgentSettings{Agent: "codex", Model: "gpt-5.5", ReasoningEffort: "high"}
	if got := phase.AgentSettings(); got != wantAgent {
		t.Fatalf("production Ready F phase agent settings = %#v, want %#v", got, wantAgent)
	}

	handoff.LaunchRelease()
	_, releaseAgain, err := newStore().ReserveAgentLaunch(ctx.FlowID)
	if err != nil {
		t.Fatalf("Ready F reservation was not released: %v", err)
	}
	releaseAgain()
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

func TestBeadsReadyStartFlowCapturesLaunchSettings(t *testing.T) {
	wantTemplates := model.FlowPromptTemplates{Plan: "ready plan {flow_id}"}
	var startedRequest model.FlowStartRequest
	m := inBeadsPane(model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:          "claude",
		ClaudeModel:           "claude-opus-5",
		ClaudeReasoningEffort: "high",
		CodexModel:            "gpt-5.5",
		CodexReasoningEffort:  "medium",
		SessionStateRoot:      "/state/approach/sessions/v1",
		FlowPromptTemplates:   wantTemplates,
		ListReadyBeads:        func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListOpenBeads:         func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(model.FlowStartRequest) (model.FlowStartResult, error) {
			t.Fatal("Ready F called CreateFlow instead of StartFlowPlan")
			return model.FlowStartResult{}, nil
		},
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			startedRequest = req
			return model.FlowStartResult{
				Flow:          flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title},
				LaunchSkipped: true,
			}, nil
		},
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-1", Title: "One"}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if cmd == nil {
		t.Fatal("Ready Bead Flow start returned nil command")
	}
	if msg := cmd(); func() bool { _, ok := msg.(model.ReadyBeadFlowCreatedMsg); return ok }() == false {
		t.Fatalf("Ready F returned %T, want ReadyBeadFlowCreatedMsg for skipped launch", msg)
	}
	if startedRequest.RepoPath != "/dev/alpha" || startedRequest.Title != "bd-1: One" {
		t.Fatalf("Ready F identity = %#v", startedRequest)
	}
	wantInstructions := "Use Bead bd-1 as the durable source of requirements. Read it with `bd show bd-1` before planning or implementation."
	if startedRequest.Instructions != wantInstructions || startedRequest.BaseRef != "" {
		t.Fatalf("Ready F content = %#v", startedRequest)
	}
	if startedRequest.AgentCommand != "claude" || startedRequest.Model != "claude-opus-5" || startedRequest.ReasoningEffort != "high" {
		t.Fatalf("Ready F agent settings = %#v", startedRequest)
	}
	if startedRequest.SessionStateRoot != "/state/approach/sessions/v1" ||
		startedRequest.FlowPromptTemplates != wantTemplates || !startedRequest.FlowPromptTemplatesProvided {
		t.Fatalf("Ready F launch snapshot = %#v", startedRequest)
	}
	if startedRequest.Headless == nil || !*startedRequest.Headless {
		t.Fatalf("Ready F Headless = %#v, want explicit true", startedRequest.Headless)
	}
	if startedRequest.PlanPhaseID != "" || startedRequest.PlanPhaseTitle != "" || startedRequest.PlanPhaseStatus != "" {
		t.Fatalf("Ready F hard-coded initial phase: %#v", startedRequest)
	}
}

func TestBeadsReadyStartFlowRoutesCreationTimeLaunchByAgent(t *testing.T) {
	for _, tt := range []struct {
		name          string
		command       string
		launchBackend string
		wantEmbedded  bool
	}{
		{name: "codex embedded", command: "codex", wantEmbedded: true},
		{name: "claude stays embedded in tmux mode", command: "claude", launchBackend: "tmux", wantEmbedded: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				AgentCommand:  tt.command,
				LaunchBackend: tt.launchBackend,
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
				},
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					return model.FlowStartResult{
						Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title},
						LaunchContext: actions.AgentLaunchContext{
							Command:      req.AgentCommand,
							LaunchID:     "launch-1",
							RepoPath:     req.RepoPath,
							WorktreePath: "/dev/alpha-worktrees/flow-1",
							FlowID:       "flow-1",
							FlowPhaseID:  "plan",
							Headless:     true,
						},
						LaunchRelease: func() {},
					}, nil
				},
			}))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			m, _ = update(m, readyCmd())

			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
			if cmd == nil {
				t.Fatal("Ready F returned nil command")
			}
			switch msg := cmd().(type) {
			case model.FlowEmbeddedLaunchRequestedMsg:
				if !tt.wantEmbedded || msg.ReadyBeadRequest == 0 || msg.Request != 0 || !msg.LaunchContext.Embedded || !msg.LaunchContext.FlowLaunchTracked {
					t.Fatalf("embedded Ready handoff = %#v", msg)
				}
			case model.PlanLaunchRequestedMsg:
				if tt.wantEmbedded || msg.ReadyBeadRequest == 0 || msg.Request != 0 || msg.LaunchContext.Embedded || msg.LaunchContext.FlowLaunchTracked {
					t.Fatalf("external Ready handoff = %#v", msg)
				}
			default:
				t.Fatalf("Ready F returned %T", msg)
			}
		})
	}
}

func TestBeadsReadyStartFlowHandoffTransfersAdmissionAndRejectsStaleRepo(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "accepted"
		if stale {
			name = "stale repository"
		}
		t.Run(name, func(t *testing.T) {
			releases := 0
			embeddedStarts := 0
			var phaseUpdates []flowstore.PhaseUpdate
			m := inBeadsPane(newTestModel(testRepos(), model.Options{
				AgentCommand: "codex",
				ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
					return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
				},
				ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
				StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
					return model.FlowStartResult{
						Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title},
						LaunchContext: actions.AgentLaunchContext{
							Command: "codex", LaunchID: "launch-1", RepoPath: req.RepoPath,
							WorktreePath: "/dev/alpha-worktrees/flow-1", FlowID: "flow-1", FlowPhaseID: "plan", Headless: true,
						},
						LaunchRelease: func() { releases++ },
					}, nil
				},
				StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
					embeddedStarts++
					return &fakeEmbeddedTerminal{state: "running"}, nil
				},
				SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
					phaseUpdates = append(phaseUpdates, update)
					return flowstore.FlowRecord{FlowID: update.FlowID}, nil
				},
			}))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			m, _ = update(m, readyCmd())
			m, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
			handoff := startCmd().(model.FlowEmbeddedLaunchRequestedMsg)

			if stale {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlR})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			}
			var handoffCmd tea.Cmd
			m, handoffCmd = update(m, handoff)
			if stale {
				if handoffCmd == nil {
					t.Fatal("stale Ready handoff returned no phase recovery command")
				}
				if releases != 0 {
					t.Fatalf("reservation released before stale phase recovery: %d", releases)
				}
				m, _ = update(m, handoffCmd())
			}
			if releases != 1 {
				t.Fatalf("reservation releases = %d, want 1", releases)
			}
			wantStarts := 1
			if stale {
				wantStarts = 0
			}
			if embeddedStarts != wantStarts {
				t.Fatalf("embedded starts = %d, want %d", embeddedStarts, wantStarts)
			}
			if stale {
				if len(phaseUpdates) != 1 {
					t.Fatalf("stale Ready phase updates = %#v, want one recovery update", phaseUpdates)
				}
				got := phaseUpdates[0]
				if got.FlowID != "flow-1" || got.PhaseID != "plan" || got.Status != flowstore.PhaseNeedsAttention ||
					!strings.Contains(got.Notes, "canceled") {
					t.Fatalf("stale Ready phase update = %#v", got)
				}
			}
			if !stale {
				_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
				if retry == nil {
					t.Fatal("accepted Ready handoff did not transfer admission ownership")
				}
			}
		})
	}
}

// The Ready pane keeps focus across the whole `F` handoff, so the post-launch
// refresh cannot ask whether Flows is focused; it has to ask the same question
// the create-only completion asks — is any Flow surface displayed?
func TestBeadsReadyStartFlowRefreshesVisibleFlowSurface(t *testing.T) {
	for _, tt := range []struct {
		name        string
		keys        []tea.KeyMsg
		height      int
		wantRefresh int
	}{
		{name: "beads ready with background flows visible", height: 30, wantRefresh: 1},
		{name: "selected repo flows", height: 30, keys: []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyRunes, Runes: []rune{'3'}}}, wantRefresh: 1},
		{name: "active flows", height: 30, keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}}, wantRefresh: 1},
		{name: "other surface with background flows hidden", height: 20, keys: []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'1'}}}, wantRefresh: 0},
	} {
		// `codex` and `claude` route the handoff to the embedded branch; any
		// other command routes it to the external one. A failed external launch
		// adds the failure-persistence hop, which refreshes a second time.
		for _, route := range []struct {
			name        string
			command     string
			launchErr   bool
			perRefresh  int
			wantStatus  string
			wantPersist bool
		}{
			{name: "embedded", command: "codex", perRefresh: 1},
			{name: "external", command: "aider", perRefresh: 1},
			{name: "external launch failure", command: "aider", launchErr: true, perRefresh: 2, wantPersist: true, wantStatus: "launch failed"},
		} {
			t.Run(tt.name+"/"+route.name, func(t *testing.T) {
				persisted := 0
				listCalls := 0
				m := inBeadsPane(newTestModel(testRepos(), model.Options{
					AgentCommand: route.command,
					ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
						return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
					},
					ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
					ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) {
						listCalls++
						return nil, nil
					},
					StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
						return model.FlowStartResult{
							Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title},
							LaunchContext: actions.AgentLaunchContext{
								Command: req.AgentCommand, LaunchID: "launch-1", RepoPath: req.RepoPath,
								WorktreePath: "/dev/alpha-worktrees/flow-1", FlowID: "flow-1", FlowPhaseID: "plan", Headless: true,
							},
							LaunchRelease: func() {},
						}, nil
					},
					StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
						return &fakeEmbeddedTerminal{state: "running"}, nil
					},
					LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
						if route.launchErr {
							return actions.TerminalLaunchSpec{}, errors.New("launch failed")
						}
						return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
					},
					SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
						persisted++
						return flowstore.FlowRecord{FlowID: update.FlowID}, nil
					},
				}))
				m, _ = update(m, tea.WindowSizeMsg{Width: 140, Height: tt.height})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
				m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				m, _ = update(m, readyCmd())
				m, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
				if startCmd == nil {
					t.Fatal("Ready F returned nil command")
				}
				handoff := startCmd()
				for _, key := range tt.keys {
					m, _ = update(m, key)
				}
				m, cmd := update(m, handoff)
				drain := func(cmd tea.Cmd) []tea.Msg {
					if cmd == nil {
						return nil
					}
					return immediateTestCommandMessages(cmd)
				}
				for _, msg := range drain(cmd) {
					if msg == nil {
						continue
					}
					// The launch-failure persist hop is the second half of this
					// path; its refresh has to land on the visible surface too.
					var followUp tea.Cmd
					m, followUp = update(m, msg)
					_ = drain(followUp)
				}
				// Guard the route table itself: without the persisted phase
				// update, the failure route would silently stop covering the
				// failure-persistence refresh.
				if route.wantPersist != (persisted > 0) {
					t.Fatalf("phase persists = %d, want persisted=%v", persisted, route.wantPersist)
				}
				if route.wantStatus != "" && !strings.Contains(m.TransientError(), route.wantStatus) {
					t.Fatalf("status = %q, want it to contain %q", m.TransientError(), route.wantStatus)
				}
				if want := tt.wantRefresh * route.perRefresh; listCalls != want {
					t.Fatalf("ListFlows calls = %d, want %d", listCalls, want)
				}
			})
		}
	}
}

func TestBeadsReadyStartFlowRejectsDualRequestIdentityWithoutClearingAdmission(t *testing.T) {
	releases := 0
	starts := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{
				Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: req.RepoPath, Title: req.Title},
				LaunchContext: actions.AgentLaunchContext{
					Command: "codex", LaunchID: "launch-1", RepoPath: req.RepoPath,
					WorktreePath: "/dev/alpha-worktrees/flow-1", FlowID: "flow-1", FlowPhaseID: "plan", Headless: true,
				},
				LaunchRelease: func() { releases++ },
			}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			starts++
			return &fakeEmbeddedTerminal{state: "running"}, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	handoff := startCmd().(model.FlowEmbeddedLaunchRequestedMsg)
	handoff.Request = handoff.ReadyBeadRequest
	m, _ = update(m, handoff)
	if releases != 1 || starts != 0 {
		t.Fatalf("dual-identity handoff released %d times and started %d agents", releases, starts)
	}
	_, retry := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if retry != nil {
		t.Fatalf("malformed handoff cleared active Ready admission: %T", retry)
	}
}

func TestBeadsReadyStartFlowLateLaunchFailureDoesNotClearNewerRequest(t *testing.T) {
	releases := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		AgentCommand: "codex",
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			return []beadsquery.Bead{{ID: "bd-1", Title: "One"}}, nil
		},
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		CreateFlow: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{Flow: flowstore.FlowRecord{FlowID: "flow-newer", RepoPath: req.RepoPath, Title: req.Title}}, nil
		},
		StartFlowPlan: func(req model.FlowStartRequest) (model.FlowStartResult, error) {
			return model.FlowStartResult{
				Flow: flowstore.FlowRecord{FlowID: "flow-started", RepoPath: req.RepoPath, Title: req.Title},
				LaunchContext: actions.AgentLaunchContext{
					Command: "codex", LaunchID: "launch-old", RepoPath: req.RepoPath,
					WorktreePath: "/dev/alpha-worktrees/flow-started", FlowID: "flow-started",
					FlowPhaseID: "plan", FlowPhaseKind: flowstore.KindPlan, Headless: true,
				},
				LaunchRelease: func() { releases++ },
			}, nil
		},
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return nil, errors.New("embedded open failed")
		},
		SetFlowPhase: func(update flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{FlowID: update.FlowID}, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m, readyCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m, _ = update(m, readyCmd())
	m, startCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	handoff := startCmd().(model.FlowEmbeddedLaunchRequestedMsg)
	m, failureCmd := update(m, handoff)
	if failureCmd == nil || releases != 1 {
		t.Fatalf("failed launch returned %T and released reservation %d times", failureCmd, releases)
	}
	m, newerCmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if newerCmd == nil {
		t.Fatal("accepted handoff did not allow a newer Ready request")
	}
	m, _ = update(m, failureCmd())
	_, duplicate := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	if duplicate != nil {
		t.Fatalf("late launch failure cleared newer Ready admission: %T", duplicate)
	}
}
