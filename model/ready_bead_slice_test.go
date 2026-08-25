package model_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/ui"
)

func sliceEpicBeads() []beadsquery.Bead {
	return []beadsquery.Bead{{ID: "bd-epic", Title: "Epic one", IssueType: "epic"}}
}

// newSliceEpicModel puts a settled Ready Beads selection in the focused content
// pane. It pins the embedded launch backend on purpose: the tmux route builds
// its spec through LaunchRepoTmuxAgent and never touches Options.LaunchAgent,
// so a tmux-backed model would bypass every capture seam below and leave the
// assertions vacuously green.
func newSliceEpicModel(t *testing.T, opts model.Options, beads []beadsquery.Bead) model.Model {
	t.Helper()
	opts.LaunchBackend = config.LaunchBackendEmbedded
	return newSliceEpicModelWithOptions(t, opts, beads)
}

func newSliceEpicModelWithOptions(t *testing.T, opts model.Options, beads []beadsquery.Bead) model.Model {
	t.Helper()
	if opts.ListReadyBeads == nil {
		opts.ListReadyBeads = func(string) ([]beadsquery.Bead, error) { return nil, nil }
	}
	if opts.ListOpenBeads == nil {
		opts.ListOpenBeads = func(string) ([]beadsquery.Bead, error) { return nil, nil }
	}
	m := inBeadsPane(newTestModel(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	return applyBeadsResult(t, m, ui.ModeBeadsReady, true, beads)
}

func sliceEpicHintShown(m model.Model) bool {
	return strings.Contains(ansi.Strip(viewContent(m)), "slice epic")
}

func pressSliceEpic(m model.Model) (model.Model, tea.Cmd) {
	return update(m, tea.KeyPressMsg{Text: string([]rune{'S'})})
}

// captureSliceLaunch presses S and keeps the launch command undelivered, so the
// caller decides when the AgentResultMsg that releases the fence arrives.
func captureSliceLaunch(t *testing.T, m model.Model) (model.Model, tea.Cmd) {
	t.Helper()
	next, cmd := pressSliceEpic(m)
	if cmd == nil {
		t.Fatal("S returned no launch command")
	}
	return next, cmd
}

func TestSliceEpic_AdvertisesAndLaunchesOneAgentInSelectedRepo(t *testing.T) {
	var contexts []actions.AgentLaunchContext
	claimed := 0
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		ClaimBead: func(string, string) error {
			claimed++
			return errors.New("must not be called")
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			contexts = append(contexts, ctx)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	if !sliceEpicHintShown(m) {
		t.Fatalf("settled Ready epic selection did not advertise the slice shortcut:\n%s", ansi.Strip(viewContent(m)))
	}

	m, cmd := captureSliceLaunch(t, m)
	m, _ = applyTestCommand(m, cmd)

	if len(contexts) != 1 {
		t.Fatalf("captured %d launch contexts, want exactly 1", len(contexts))
	}
	if contexts[0].WorkingDir != "/dev/alpha" {
		t.Fatalf("launch WorkingDir = %q, want %q", contexts[0].WorkingDir, "/dev/alpha")
	}
	if contexts[0].RepoPath != "/dev/alpha" || contexts[0].WorktreePath != "/dev/alpha" {
		t.Fatalf("launch repo/worktree = %q/%q, want the repository root", contexts[0].RepoPath, contexts[0].WorktreePath)
	}
	if claimed != 0 {
		t.Fatalf("slice launch performed %d tracker writes, want 0", claimed)
	}
}

func TestSliceEpic_PromptCarriesEveryContractClause(t *testing.T) {
	var got actions.AgentLaunchContext
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	m, cmd := captureSliceLaunch(t, m)
	_, _ = applyTestCommand(m, cmd)

	prompt := got.InitialPrompt
	clauses := []struct {
		name string
		want string
	}{
		{"skill name", "slice-issues"},
		{"epic ID", "bd-epic"},
		{"repository path", "/dev/alpha"},
		{"bd show", "bd show -- bd-epic"},
		{"tracer bullet slices", "tracer-bullet vertical slices"},
		{"HITL or AFK", "HITL or AFK"},
		{"dependencies", "blocked by"},
		{"acceptance criteria", "acceptance criteria"},
		{"user stories", "user stories"},
		{"wait for approval", "Wait for my"},
		{"approved children", "create only the approved child Beads"},
		{"dependency order", "in dependency order"},
		{"parent untouched", "Never modify or close the parent epic bd-epic"},
	}
	for _, clause := range clauses {
		if !strings.Contains(prompt, clause.want) {
			t.Fatalf("prompt is missing the %s clause %q:\n%s", clause.name, clause.want, prompt)
		}
	}
}

func TestSliceEpic_NotOwnedOutsideSettledReadyEpicSelection(t *testing.T) {
	launches := 0
	opts := func() model.Options {
		return model.Options{
			AgentCommand: "codex",
			LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
				launches++
				return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
			},
		}
	}

	tests := []struct {
		name  string
		build func(t *testing.T) model.Model
	}{
		{"ordinary task row", func(t *testing.T) model.Model {
			return newSliceEpicModel(t, opts(), []beadsquery.Bead{{ID: "bd-task", Title: "Task", IssueType: "task"}})
		}},
		{"empty selection", func(t *testing.T) model.Model {
			return newSliceEpicModel(t, opts(), nil)
		}},
		{"blank bead ID", func(t *testing.T) model.Model {
			return newSliceEpicModel(t, opts(), []beadsquery.Bead{{ID: "   ", Title: "Blank", IssueType: "epic"}})
		}},
		{"unavailable subview", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			return applyBeadsResult(t, m, ui.ModeBeadsReady, false, sliceEpicBeads())
		}},
		// Leaving and re-entering Ready refetches it: the epic row is retained
		// but the subview is pending, so the selection is not settled.
		{"pending subview", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'b'})})
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
			if !m.BeadsPending(ui.ModeBeadsReady) || len(m.Beads(ui.ModeBeadsReady)) != 1 {
				t.Fatalf("setup did not leave a retained row in a pending Ready subview: pending=%v rows=%#v",
					m.BeadsPending(ui.ModeBeadsReady), m.Beads(ui.ModeBeadsReady))
			}
			return m
		}},
		{"error subview", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			return applyBeadsResultWithError(t, m, ui.ModeBeadsReady, "bd failed")
		}},
		{"filter with zero matches", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
			for _, r := range "zzzz" {
				m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{r})})
			}
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			return m
		}},
		{"active search", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'/'})})
			return m
		}},
		{"repo pane focused", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			if m.ActivePane() != ui.PaneRepos {
				t.Fatalf("setup did not focus the repo pane: %v", m.ActivePane())
			}
			return m
		}},
		{"open modal", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'A'})})
			if !model.ModalOpenForTest(m) {
				t.Fatal("setup did not open a modal")
			}
			return m
		}},
		// The Active Flows takeover makes focusedMode report ModeActiveFlows
		// even though the Ready rows are still loaded underneath it.
		{"active flows takeover", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			if m.Mode() != ui.ModeActiveFlows {
				t.Fatalf("setup did not open the Active Flows takeover: %v", m.Mode())
			}
			return m
		}},
		// Ready stays live in the top pane while the bottom pane is focused;
		// focusedMode returns the bottom mode, so S is not owned.
		{"bottom pane focused", func(t *testing.T) model.Model {
			m := newSliceEpicModel(t, opts(), sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			if m.ActivePane() != ui.PaneBottom {
				t.Fatalf("setup did not focus the bottom pane: %v", m.ActivePane())
			}
			if m.Mode() == ui.ModeBeadsReady {
				t.Fatalf("setup left Ready focused: %v", m.Mode())
			}
			if len(m.Beads(ui.ModeBeadsReady)) != 1 {
				t.Fatalf("setup dropped the live Ready rows: %#v", m.Beads(ui.ModeBeadsReady))
			}
			return m
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launches = 0
			m := tt.build(t)
			if sliceEpicHintShown(m) {
				t.Fatalf("slice shortcut advertised in an unowned context:\n%s", ansi.Strip(viewContent(m)))
			}
			before := m.TransientError()
			next, cmd := pressSliceEpic(m)
			if cmd != nil {
				next, _ = applyTestCommand(next, cmd)
			}
			if launches != 0 {
				t.Fatalf("S launched %d agents in an unowned context, want 0", launches)
			}
			if next.TransientError() != before {
				t.Fatalf("S changed the status in an unowned context: %q, want %q", next.TransientError(), before)
			}
		})
	}
}

func TestSliceEpic_NotOwnedWhileEmbeddedTerminalInputIsFocused(t *testing.T) {
	fakeTerm := &fakeEmbeddedTerminal{state: "running"}
	launches := 0
	m := inBeadsPane(newTestModel(testRepos(), model.Options{
		AgentCommand:   "codex",
		LaunchBackend:  config.LaunchBackendEmbedded,
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
		ListOpenBeads:  func(string) ([]beadsquery.Bead, error) { return nil, nil },
		StartEmbeddedTerminal: func(actions.AgentLaunchContext, int, int) (model.EmbeddedTerminal, error) {
			return fakeTerm, nil
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launches++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'2'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'o'})})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'r'})})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, sliceEpicBeads())
	m, _ = update(m, flowTerminalOpenRequest{LaunchContext: actions.AgentLaunchContext{
		Command: "codex", FlowID: "flow-existing", FlowPhaseID: "implementation",
	}})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{'i'})})

	if sliceEpicHintShown(m) {
		t.Fatal("slice shortcut advertised while the embedded terminal input was focused")
	}
	_, cmd := pressSliceEpic(m)
	if cmd != nil || launches != 0 {
		t.Fatalf("terminal-focused S returned %T and launched %d agents", cmd, launches)
	}
	if len(fakeTerm.writes) != 1 || fakeTerm.writes[0] != "S" {
		t.Fatalf("terminal-focused S writes = %#v, want forwarded input", fakeTerm.writes)
	}
}

func TestSliceEpic_NotOwnedInNonReadyBeadsSubviews(t *testing.T) {
	subviews := []struct {
		name string
		key  rune
		mode ui.Mode
	}{
		{"blocked", 'b', ui.ModeBeadsBlocked},
		{"open", 'o', ui.ModeBeadsOpen},
		{"in progress", 'i', ui.ModeBeadsInProgress},
		{"closed", 'c', ui.ModeBeadsClosed},
	}
	for _, sub := range subviews {
		t.Run(sub.name, func(t *testing.T) {
			launches := 0
			m := newSliceEpicModel(t, model.Options{
				AgentCommand: "codex",
				LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					launches++
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
				},
			}, sliceEpicBeads())
			m, _ = update(m, tea.KeyPressMsg{Text: string([]rune{sub.key})})
			m = applyBeadsResult(t, m, sub.mode, true, sliceEpicBeads())

			if sliceEpicHintShown(m) {
				t.Fatalf("slice shortcut advertised in the %s subview:\n%s", sub.name, ansi.Strip(viewContent(m)))
			}
			next, cmd := pressSliceEpic(m)
			if cmd != nil {
				_, _ = applyTestCommand(next, cmd)
			}
			if launches != 0 {
				t.Fatalf("S launched %d agents from the %s subview, want 0", launches, sub.name)
			}
		})
	}
}

func TestSliceEpic_RefusesWithoutConfiguredAgentAndKeepsAdvertising(t *testing.T) {
	launches := 0
	claimed := 0
	m := newSliceEpicModel(t, model.Options{
		ClaimBead: func(string, string) error {
			claimed++
			return errors.New("must not be called")
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launches++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	if !sliceEpicHintShown(m) {
		t.Fatal("slice shortcut should stay advertised without a configured agent so the refusal can teach the fix")
	}
	want := "Press A to choose " + ui.AgentInputPlaceholder + " before launching an agent"
	// The refusal returns only the status-expiry tick; draining it would let the
	// 3s lifetime clear the status this test is about.
	m, _ = pressSliceEpic(m)
	if launches != 0 {
		t.Fatalf("S launched %d agents with no configured agent, want 0", launches)
	}
	if m.TransientError() != want {
		t.Fatalf("status = %q, want %q", m.TransientError(), want)
	}
	if !sliceEpicHintShown(m) {
		t.Fatal("the refusal left an admission held; the shortcut stopped being advertised")
	}
	if model.FlowPreparationAdmissionForTest(m) || model.SliceEpicLaunchInFlightForTest(m) {
		t.Fatal("the agent refusal left a preparation admission or launch record behind")
	}

	// A second press must refuse on the agent again, not on a wedged fence.
	m, _ = pressSliceEpic(m)
	if m.TransientError() != want {
		t.Fatalf("second refusal status = %q, want %q", m.TransientError(), want)
	}
	if claimed != 0 {
		t.Fatalf("agent refusal performed %d tracker writes, want 0", claimed)
	}
}

// sliceEpicPin writes a real binary and returns the pin that verifies against
// it, so a test can invalidate the pin afterwards by replacing the file.
func sliceEpicPin(t *testing.T) (string, controlplane.Pin) {
	t.Helper()
	pinned := filepath.Join(t.TempDir(), "approach")
	if err := os.WriteFile(pinned, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write pin: %v", err)
	}
	digest, err := controlplane.FileDigest(pinned)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return pinned, controlplane.Pin{
		ExecutablePath: pinned,
		Digest:         digest,
		Version:        "v0.10.3",
		SchemaVersion:  6,
	}
}

// The launched agent runs `bd` and `approach` through the provider session hook,
// so S has to bake in the launching build like every other launch kind rather
// than letting the agent resolve `approach` off an ambient PATH.
func TestSliceEpic_LaunchContextCarriesTheLaunchingBuild(t *testing.T) {
	pinned, pin := sliceEpicPin(t)
	var got actions.AgentLaunchContext
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		LaunchPin:    pin,
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	m, cmd := captureSliceLaunch(t, m)
	applyTestCommand(m, cmd)

	if got.Executable != pinned {
		t.Fatalf("launch Executable = %q, want the pinned binary %q", got.Executable, pinned)
	}
	if got.BuildVersion != "v0.10.3" || got.DBSchemaVersion != 6 {
		t.Fatalf("launch build stamp = %q/%d, want v0.10.3/6", got.BuildVersion, got.DBSchemaVersion)
	}
}

// An upgrade can replace the pinned binary underneath a long-lived TUI. S must
// refuse before it reserves the shared preparation, so the refusal leaves the
// fence exactly as it found it and a later press can still launch.
func TestSliceEpic_RefusesAnUnverifiedPinWithoutHoldingTheFence(t *testing.T) {
	pinned, pin := sliceEpicPin(t)
	launches := 0
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		LaunchPin:    pin,
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launches++
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	if err := os.WriteFile(pinned, []byte("#!/bin/sh\n# replaced\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("replace pin: %v", err)
	}

	m, _ = pressSliceEpic(m)
	if launches != 0 {
		t.Fatalf("S launched %d agents on a replaced pin, want 0", launches)
	}
	if !strings.Contains(m.TransientError(), "Launch refused") {
		t.Fatalf("status %q does not refuse the replaced pin", m.TransientError())
	}
	if model.FlowPreparationAdmissionForTest(m) || model.SliceEpicLaunchInFlightForTest(m) {
		t.Fatal("the pin refusal left a preparation admission or launch record behind")
	}
	if !sliceEpicHintShown(m) {
		t.Fatal("the pin refusal stopped the shortcut being advertised")
	}
}

func TestSliceEpic_FenceAdmitsOneLaunchAtATime(t *testing.T) {
	var contexts []actions.AgentLaunchContext
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			contexts = append(contexts, ctx)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	m, cmd := captureSliceLaunch(t, m)
	if len(contexts) != 1 {
		t.Fatalf("captured %d contexts after the first press, want 1", len(contexts))
	}
	if contexts[0].InitialPrompt == "" {
		t.Fatal("captured launch carried no prompt")
	}

	// The fence is held while the launch command has not resolved yet, and the
	// shared preparation admission suppresses f/F/a with it.
	view := ansi.Strip(viewContent(m))
	if strings.Contains(view, "slice epic") {
		t.Fatalf("slice shortcut stayed advertised while its own launch was in flight:\n%s", view)
	}
	if strings.Contains(view, "new flow") {
		t.Fatalf("f/F stayed advertised while the shared preparation admission was held:\n%s", view)
	}

	held, _ := pressSliceEpic(m)
	if len(contexts) != 1 {
		t.Fatalf("second press launched again: %d contexts, want 1", len(contexts))
	}
	if held.TransientError() != "A slice epic launch is already in flight" {
		t.Fatalf("in-flight status = %q", held.TransientError())
	}

	// A foreign result must not release this fence.
	foreign, _ := update(held, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{Command: "codex", LaunchID: "approach-foreign"},
		Detached:      true,
	})
	if sliceEpicHintShown(foreign) {
		t.Fatal("a foreign launch result released the slice fence")
	}

	// An unrelated launch's failure must not release it either.
	failed, _ := update(foreign, model.AgentResultMsg{
		LaunchContext: actions.AgentLaunchContext{Command: "codex", LaunchID: "approach-foreign"},
		Detached:      true,
		Err:           "unrelated launch failed",
	})
	if sliceEpicHintShown(failed) {
		t.Fatal("an unrelated launch failure released the slice fence")
	}

	// The launch's own result releases it and names the recorded epic.
	released, _ := applyTestCommand(failed, cmd)
	if !sliceEpicHintShown(released) {
		t.Fatal("the launch's own result did not release the slice fence")
	}
	if want := "Launched codex to slice bd-epic"; released.TransientError() != want {
		t.Fatalf("detached slice status = %q, want %q", released.TransientError(), want)
	}
}

func TestSliceEpic_HeldFenceSurvivesSelectionAndRepoChanges(t *testing.T) {
	var contexts []actions.AgentLaunchContext
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			contexts = append(contexts, ctx)
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, []beadsquery.Bead{
		{ID: "bd-epic", Title: "Epic one", IssueType: "epic"},
		{ID: "bd-other", Title: "Epic two", IssueType: "epic"},
	})

	m, cmd := captureSliceLaunch(t, m)
	if !strings.Contains(contexts[0].InitialPrompt, "bd-epic") {
		t.Fatalf("first launch did not capture the pressed epic: %q", contexts[0].InitialPrompt)
	}

	// Move the selection, then switch repositories. Neither releases the fence.
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if model.SliceEpicLaunchInFlightForTest(m) != true {
		t.Fatal("a selection or repository change released the in-flight slice fence")
	}

	// The stale result still reports against the epic it was launched for.
	m, _ = applyTestCommand(m, cmd)
	if want := "Launched codex to slice bd-epic"; m.TransientError() != want {
		t.Fatalf("stale result status = %q, want %q", m.TransientError(), want)
	}
	if len(contexts) != 1 {
		t.Fatalf("captured %d contexts, want exactly 1", len(contexts))
	}
}

func TestSliceEpic_LaunchContextCarriesConfiguredAgentAndSkipsFlowLifecycle(t *testing.T) {
	var got actions.AgentLaunchContext
	m := newSliceEpicModel(t, model.Options{
		AgentCommand:         "codex",
		CodexModel:           "gpt-5-codex",
		CodexReasoningEffort: "high",
		SessionStateRoot:     "/state/sessions",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	m, cmd := captureSliceLaunch(t, m)
	_, _ = applyTestCommand(m, cmd)

	if got.Command != "codex" {
		t.Fatalf("launch command = %q, want codex", got.Command)
	}
	if got.Model != "gpt-5-codex" || got.ReasoningEffort != "high" {
		t.Fatalf("launch settings = %q/%q, want gpt-5-codex/high", got.Model, got.ReasoningEffort)
	}
	if got.SessionStateRoot != "/state/sessions" {
		t.Fatalf("launch SessionStateRoot = %q", got.SessionStateRoot)
	}
	if strings.TrimSpace(got.LaunchID) == "" {
		t.Fatal("launch carried no launch ID")
	}
	if model.FlowLaunchContextRequiresLifecycleForTest(got) {
		t.Fatal("slice launch context claims the Flow launch lifecycle")
	}
}

func TestSliceEpic_LaunchFailureReleasesFenceAndAllowsRetry(t *testing.T) {
	launches := 0
	claimed := 0
	m := newSliceEpicModel(t, model.Options{
		AgentCommand: "codex",
		ClaimBead: func(string, string) error {
			claimed++
			return errors.New("must not be called")
		},
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launches++
			if launches == 1 {
				return actions.TerminalLaunchSpec{}, errors.New("terminal unavailable")
			}
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	}, sliceEpicBeads())

	// launchAgent runs synchronously inside the key handler, so the failure has
	// already landed; the returned command is only the status-expiry tick.
	m, _ = pressSliceEpic(m)
	if m.TransientError() != "terminal unavailable" {
		t.Fatalf("failure status = %q, want %q", m.TransientError(), "terminal unavailable")
	}
	if model.FlowPreparationAdmissionForTest(m) || model.SliceEpicLaunchInFlightForTest(m) {
		t.Fatal("a failed launch left the slice fence held")
	}
	if !sliceEpicHintShown(m) {
		t.Fatal("a failed launch stopped advertising the slice shortcut")
	}

	m, retry := pressSliceEpic(m)
	if retry == nil {
		t.Fatal("retry after a failed launch produced no command")
	}
	_, _ = applyTestCommand(m, retry)
	if launches != 2 {
		t.Fatalf("retry produced %d total launches, want 2", launches)
	}
	if claimed != 0 {
		t.Fatalf("failed launch performed %d tracker writes, want 0", claimed)
	}
}

func TestSliceEpic_TmuxRouteKeepsAttachStatusAndReleasesFence(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true}
	m := newSliceEpicModelWithOptions(t, spy.options(config.LaunchBackendTmux), sliceEpicBeads())

	m, cmd := captureSliceLaunch(t, m)
	if len(spy.tmuxContexts) != 1 {
		t.Fatalf("tmux route captured %d contexts, want 1", len(spy.tmuxContexts))
	}
	if !strings.Contains(spy.tmuxContexts[0].InitialPrompt, "bd-epic") {
		t.Fatalf("tmux launch prompt lost the epic ID: %q", spy.tmuxContexts[0].InitialPrompt)
	}
	if len(spy.externalContexts) != 0 {
		t.Fatalf("tmux route also used the external transport %d times", len(spy.externalContexts))
	}

	m, _ = applyTestCommand(m, cmd)
	want := "Launched codex-abcd1234 in tmux session approach-alpha-0001 — tmux attach -t 'approach-alpha-0001'"
	if m.TransientError() != want {
		t.Fatalf("tmux status = %q, want the attach command %q", m.TransientError(), want)
	}
	if model.SliceEpicLaunchInFlightForTest(m) {
		t.Fatal("the tmux launch result did not release the slice fence")
	}
}

func TestSliceEpic_TmuxBuildFailureReleasesFence(t *testing.T) {
	spy := &tmuxModeSpy{tmuxAvailable: true, tmuxErr: errors.New("tmux window refused")}
	m := newSliceEpicModelWithOptions(t, spy.options(config.LaunchBackendTmux), sliceEpicBeads())

	m, _ = pressSliceEpic(m)
	if m.TransientError() != "tmux window refused" {
		t.Fatalf("tmux failure status = %q", m.TransientError())
	}
	if model.FlowPreparationAdmissionForTest(m) || model.SliceEpicLaunchInFlightForTest(m) {
		t.Fatal("a failed tmux spec build left the slice fence held")
	}
}
