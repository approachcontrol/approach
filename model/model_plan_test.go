package model_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/ui"
)

func TestModel_Key7SwitchesToPlansAndFetches(t *testing.T) {
	var gotFilter planstore.PlanFilter
	want := []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Persist plans", RepoPath: "/dev/alpha", Branch: "main", Status: "draft"},
	}
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			gotFilter = filter
			return want, nil
		},
	})
	m = inRightPane(m)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	if m.Mode() != ui.ModePlans {
		t.Fatalf("mode = %d, want plans", m.Mode())
	}
	if cmd == nil {
		t.Fatal("expected plans fetch command")
	}
	if gotFilter.RepoPath != "" {
		t.Fatalf("plan lister ran before command execution: %#v", gotFilter)
	}
	msg, ok := cmd().(model.PlanResultMsg)
	if !ok {
		t.Fatalf("expected PlanResultMsg, got %T", msg)
	}
	m, _ = update(m, msg)

	if gotFilter.RepoPath != "/dev/alpha" {
		t.Fatalf("RepoPath filter = %q, want /dev/alpha", gotFilter.RepoPath)
	}
	got := m.Plans()
	if len(got) != 1 || got[0].PlanID != "plan-1" {
		t.Fatalf("Plans() = %#v, want %#v", got, want)
	}
}

func TestModel_ChangingRepoRefetchesPlansMode(t *testing.T) {
	var filters []planstore.PlanFilter
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(filter planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			filters = append(filters, filter)
			return []planstore.PlanRecord{{PlanID: filepath.Base(filter.RepoPath), RepoPath: filter.RepoPath, Title: "T", Status: "draft"}}, nil
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	if cmd == nil {
		t.Fatal("expected initial plans fetch")
	}
	m, _ = update(m, cmd())
	if got := m.Plans(); len(got) != 1 || got[0].RepoPath != "/dev/alpha" {
		t.Fatalf("initial Plans() = %#v", got)
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatalf("expected nil cmd switching to repo pane, got %T", cmd)
	}
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("expected plans refetch after repo change")
	}
	if got := m.Plans(); len(got) != 0 {
		t.Fatalf("expected plans cleared before refetch, got %#v", got)
	}
	m, _ = update(m, cmd())
	if got := m.Plans(); len(got) != 1 || got[0].RepoPath != "/dev/bravo" {
		t.Fatalf("refetched Plans() = %#v", got)
	}
	if len(filters) != 2 || filters[0].RepoPath != "/dev/alpha" || filters[1].RepoPath != "/dev/bravo" {
		t.Fatalf("plan filters = %#v", filters)
	}
}

func TestModel_StalePlanResultIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(planstore.PlanFilter) ([]planstore.PlanRecord, error) { return nil, nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})

	// A result with a stale (zero) list request must be ignored.
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "stale", RepoPath: "/dev/alpha", Title: "T", Status: "draft"},
	}, ListRequest: 999999})
	if got := m.Plans(); len(got) != 0 {
		t.Fatalf("stale plan result should be ignored, got %#v", got)
	}
}

func TestModel_PlanListErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListPlans: func(planstore.PlanFilter) ([]planstore.PlanRecord, error) {
			return nil, errors.New("plans unavailable")
		},
	})
	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	if cmd == nil {
		t.Fatal("expected plans fetch command")
	}
	m, _ = update(m, cmd())
	if got := m.View(); !strings.Contains(got, "failed to load plans") {
		t.Fatalf("expected plan load error in view:\n%s", got)
	}
}

func TestModel_IKeyLaunchesAgentFromSelectedPlan(t *testing.T) {
	var got actions.AgentLaunchContext
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			got = ctx
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{{
		PlanID:       "plan-1",
		Title:        "Implement plans",
		Status:       "approved",
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/plans",
	}}, ListRequest: m.ListRequest(ui.ModePlans)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	if got.Command != "codex" ||
		got.RepoPath != "/dev/alpha" ||
		got.WorktreePath != "/dev/alpha-worktrees/plans" ||
		got.SessionStateRoot != "/state/wtui/sessions/v1" ||
		got.PlanID != "plan-1" ||
		got.PlanPath != "/state/wtui/sessions/v1/plans/plan-1/plan.md" {
		t.Fatalf("unexpected launch context: %#v", got)
	}
	if got.LaunchID == "" {
		t.Fatalf("expected launch ID in context: %#v", got)
	}
	prompt := strings.ToLower(got.InitialPrompt)
	for _, want := range []string{"implement plans", "plan-1", "/state/wtui/sessions/v1/plans/plan-1/plan.md", "read the plan file", "begin implementation"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("initial prompt missing %q: %q", want, got.InitialPrompt)
		}
	}
}

func TestModel_IKeyNoOpsWithNoSelectedPlan(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{AgentCommand: "codex"})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd with no selected plan, got %T", cmd)
	}
}

func TestModel_IKeyWithNoSelectedAgentShowsStatus(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd without selected agent, got %T", cmd)
	}
	if !strings.Contains(m.View(), "Press A to choose") {
		t.Fatal("expected unset-agent status")
	}
}

func TestModel_IKeyLaunchPathFallsBackFromPlanRepoToSelectedRepo(t *testing.T) {
	tests := []struct {
		name     string
		plan     planstore.PlanRecord
		wantRepo string
		wantPath string
	}{
		{
			name:     "plan repo",
			plan:     planstore.PlanRecord{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/plan-repo"},
			wantRepo: "/dev/plan-repo",
			wantPath: "/dev/plan-repo",
		},
		{
			name:     "selected repo",
			plan:     planstore.PlanRecord{PlanID: "plan-1", Title: "Implement plans", Status: "approved"},
			wantRepo: "/dev/alpha",
			wantPath: "/dev/alpha",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got actions.AgentLaunchContext
			m := model.NewWithOptions(testRepos(), model.Options{
				AgentCommand:     "codex",
				PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
				LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
					got = ctx
					return actions.TerminalLaunchSpec{Cmd: exec.Command("true"), Interactive: true}, nil
				},
			})
			m = inRightPane(m)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
			m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{tc.plan}, ListRequest: m.ListRequest(ui.ModePlans)})

			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
			if cmd == nil {
				t.Fatal("expected launch command")
			}
			if got.RepoPath != tc.wantRepo || got.WorktreePath != tc.wantPath {
				t.Fatalf("launch context repo/path = %q/%q, want %q/%q", got.RepoPath, got.WorktreePath, tc.wantRepo, tc.wantPath)
			}
		})
	}
}

func TestModel_IKeyPlanPathResolverErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "", errors.New("bad plan path") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd on resolver error, got %T", cmd)
	}
	if !strings.Contains(m.View(), "bad plan path") {
		t.Fatal("expected resolver error in status")
	}
}

func TestModel_IKeyMissingPlanLaunchPathShowsStatus(t *testing.T) {
	m := model.NewWithOptions(nil, model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd for missing launch path, got %T", cmd)
	}
	if !strings.Contains(m.View(), "Cannot determine launch path for this plan") {
		t.Fatal("expected missing launch path status")
	}
}

func TestModel_IKeyLaunchErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		AgentCommand:     "codex",
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		LaunchAgent: func(actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchSpec{}, errors.New("agent unavailable")
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd on launch error, got %T", cmd)
	}
	if !strings.Contains(m.View(), "agent unavailable") {
		t.Fatal("expected launch error in status")
	}
}

func TestModel_YKeyCopiesSelectedPlanMarkdownPath(t *testing.T) {
	var copied string
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(planID string) (string, error) {
			if planID != "plan-1" {
				t.Fatalf("resolver planID = %q, want plan-1", planID)
			}
			return "/state/wtui/sessions/v1/plans/plan-1/plan.md", nil
		},
		CopyToClipboard: func(text string) error {
			copied = text
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	if msg := cmd(); msg != (model.ClipboardResultMsg{}) {
		t.Fatalf("copy command msg = %#v", msg)
	}
	if copied != "/state/wtui/sessions/v1/plans/plan-1/plan.md" {
		t.Fatalf("copied = %q", copied)
	}
}

func TestModel_YKeyUsesDefaultPlanMarkdownPathResolver(t *testing.T) {
	root := t.TempDir()
	var copied string
	m := model.NewWithOptions(testRepos(), model.Options{
		SessionStateRoot: root,
		CopyToClipboard: func(text string) error {
			copied = text
			return nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	_ = cmd()
	want := filepath.Join(root, "plans", "plan-1", "plan.md")
	if copied != want {
		t.Fatalf("copied = %q, want %q", copied, want)
	}
}

func TestModel_YKeyNoOpsWithNoSelectedPlan(t *testing.T) {
	m := model.New(testRepos())
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})

	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd with no selected plan, got %T", cmd)
	}
}

func TestModel_YKeyPlanPathResolverErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(string) (string, error) { return "", errors.New("cannot resolve plan path") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatalf("expected nil cmd on resolver error, got %T", cmd)
	}
	if !strings.Contains(m.View(), "cannot resolve plan path") {
		t.Fatal("expected resolver error in status")
	}
}

func TestModel_YKeyPlanClipboardErrorShowsStatus(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		PlanMarkdownPath: func(string) (string, error) { return "/state/plans/plan-1/plan.md", nil },
		CopyToClipboard:  func(string) error { return errors.New("clipboard unavailable") },
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}})
	m, _ = update(m, model.PlanResultMsg{RepoPath: "/dev/alpha", Plans: []planstore.PlanRecord{
		{PlanID: "plan-1", Title: "Implement plans", Status: "approved", RepoPath: "/dev/alpha"},
	}, ListRequest: m.ListRequest(ui.ModePlans)})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected copy command")
	}
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "clipboard unavailable") {
		t.Fatal("expected clipboard error in status")
	}
}

func TestModel_YKeyHistoryAndReflogCopyUseInjectedClipboard(t *testing.T) {
	t.Run("history", func(t *testing.T) {
		var copied string
		m := model.NewWithOptions(testRepos(), model.Options{
			CopyToClipboard: func(text string) error {
				copied = text
				return nil
			},
		})
		m = inRightPane(m)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
		m, _ = update(m, model.CommitResultMsg{RepoPath: "/dev/alpha", Commits: testCommits()[:1], ListRequest: m.ListRequest(ui.ModeHistory)})

		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd == nil {
			t.Fatal("expected copy command")
		}
		_ = cmd()
		if copied != testCommits()[0].Hash {
			t.Fatalf("copied = %q, want %q", copied, testCommits()[0].Hash)
		}
	})
	t.Run("reflog", func(t *testing.T) {
		var copied string
		m := model.NewWithOptions(testRepos(), model.Options{
			CopyToClipboard: func(text string) error {
				copied = text
				return nil
			},
		})
		m = inRightPane(m)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
		m, _ = update(m, model.ReflogResultMsg{RepoPath: "/dev/alpha", Reflogs: testReflogs()[:1], ListRequest: m.ListRequest(ui.ModeReflog)})

		_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd == nil {
			t.Fatal("expected copy command")
		}
		_ = cmd()
		if copied != testReflogs()[0].Hash {
			t.Fatalf("copied = %q, want %q", copied, testReflogs()[0].Hash)
		}
	})
}
