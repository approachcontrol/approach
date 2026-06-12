package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

func repoCreateKey(input string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(input)}
}

func TestModel_LeftPaneNOpensRepoCreationForm(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{RepoCreateRoot: "/dev"})

	m, cmd := update(m, repoCreateKey("n"))
	if cmd != nil {
		t.Fatal("opening repo form should not return command")
	}
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("Overlay() = %d, want repo form", m.Overlay())
	}
	if !strings.Contains(m.View(), "New repo") || !strings.Contains(m.View(), "Create GitHub repo") {
		t.Fatalf("repo creation form missing expected fields:\n%s", m.View())
	}
}

func TestModel_LeftPaneNReportsUnavailableWithoutRepoCreateRoot(t *testing.T) {
	m := model.New(testRepos())

	m, cmd := update(m, repoCreateKey("n"))
	if cmd != nil {
		t.Fatal("unavailable repo creation should not return command")
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("Overlay() = %d, want none", m.Overlay())
	}
	if got := m.TransientError(); got != "repo creation unavailable: scan root is not configured" {
		t.Fatalf("TransientError() = %q", got)
	}
}

func TestModel_RightPaneNStillOpensWorktreeInput(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{RepoCreateRoot: "/dev"})
	m = inRightPane(m)

	m, _ = update(m, repoCreateKey("n"))
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("Overlay() = %d, want worktree input", m.Overlay())
	}
}

func TestModel_RepoCreateSubmitUsesDefaults(t *testing.T) {
	var got actions.RepoCreateOptions
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(opts actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			got = opts
			return actions.RepoCreateResult{DestinationPath: "/dev/project", LocalCreated: true, GitHubCreated: true}, nil
		},
	})

	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create repo command")
	}
	msg := cmd()
	if _, ok := msg.(model.RepoCreatedMsg); !ok {
		t.Fatalf("create command returned %T %[1]v, want RepoCreatedMsg", msg)
	}
	if got.Root != "/dev" || got.Name != "project" || !got.CreateGitHub || got.Visibility != actions.RepoVisibilityPublic {
		t.Fatalf("CreateRepo options = %#v, want root/name/default GitHub public", got)
	}
}

func TestModel_RepoCreateSubmitSupportsUncheckedGitHubAndPrivateVisibility(t *testing.T) {
	var got actions.RepoCreateOptions
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(opts actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			got = opts
			return actions.RepoCreateResult{DestinationPath: "/dev/project", LocalCreated: true}, nil
		},
	})

	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeySpace})
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create repo command")
	}
	_ = cmd()
	if got.CreateGitHub {
		t.Fatalf("CreateGitHub = true, want false: %#v", got)
	}
	if got.Visibility != actions.RepoVisibilityPrivate {
		t.Fatalf("Visibility = %q, want private", got.Visibility)
	}
}

func TestModel_RepoCreateValidationStaysInForm(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{RepoCreateRoot: "/dev"})

	m, _ = update(m, repoCreateKey("n"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("invalid form should not return command")
	}
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("Overlay() = %d, want form", m.Overlay())
	}
	if !strings.Contains(m.View(), "repo name cannot be empty") {
		t.Fatalf("expected validation error in form:\n%s", m.View())
	}
}

func TestModel_RepoCreatedRefreshesAndSelectsNewRepo(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			return actions.RepoCreateResult{DestinationPath: "/dev/project", LocalCreated: true, GitHubCreated: true}, nil
		},
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{
				{Path: "/dev/alpha", DisplayName: "alpha"},
				{Path: "/dev/project", DisplayName: "project"},
			}, nil
		},
	})
	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, refreshCmd := update(m, cmd())
	if refreshCmd == nil {
		t.Fatal("repo creation success should start repo refresh")
	}
	refreshMsg := repoRefreshResultFromBatch(t, runBatchCmd(t, refreshCmd))
	m, _ = update(m, refreshMsg)

	if got := m.Selected(); got != 1 {
		t.Fatalf("Selected() = %d, want new repo index 1", got)
	}
	if got := m.TransientError(); got != "Created repo project" {
		t.Fatalf("TransientError() = %q, want success status", got)
	}
}

func TestModel_RepoCreateFailureReopensFormWithPreviousValues(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			return actions.RepoCreateResult{}, errors.New("permission denied")
		},
	})

	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())

	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("Overlay() = %d, want form", m.Overlay())
	}
	view := m.View()
	if !strings.Contains(view, "project") || !strings.Contains(view, "permission denied") {
		t.Fatalf("failed create should reopen form with previous values and error:\n%s", view)
	}
}

func TestModel_RepoCreatePartialFailureRetriesGitHubOnly(t *testing.T) {
	var calls []actions.RepoCreateOptions
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(opts actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			calls = append(calls, opts)
			if len(calls) == 1 {
				return actions.RepoCreateResult{
					DestinationPath:   "/dev/project",
					LocalCreated:      true,
					PartialSuccess:    true,
					RetryAllowed:      true,
					ExistingLocalPath: "/dev/project",
				}, errors.New("gh auth required")
			}
			return actions.RepoCreateResult{DestinationPath: "/dev/project", GitHubCreated: true}, nil
		},
		ScanRepos: func() ([]scanner.Repo, error) {
			return []scanner.Repo{{Path: "/dev/project", DisplayName: "project"}}, nil
		},
	})

	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlayForm || !strings.Contains(m.View(), "Local repo created; GitHub/origin setup failed") {
		t.Fatalf("partial failure should reopen retry form:\n%s", m.View())
	}

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected retry create command")
	}
	m, refreshCmd := update(m, cmd())
	if refreshCmd == nil {
		t.Fatal("successful retry should refresh repos")
	}

	if len(calls) != 2 {
		t.Fatalf("CreateRepo calls = %d, want 2", len(calls))
	}
	if !calls[1].RemoteOnlyRetry || calls[1].ExistingLocalPath != "/dev/project" {
		t.Fatalf("retry options = %#v, want remote-only retry against existing path", calls[1])
	}
}

func TestModel_RepoCreateRetryKeepsStateWhenNameChanges(t *testing.T) {
	var calls []actions.RepoCreateOptions
	m := model.NewWithOptions(testRepos(), model.Options{
		RepoCreateRoot: "/dev",
		CreateRepo: func(opts actions.RepoCreateOptions) (actions.RepoCreateResult, error) {
			calls = append(calls, opts)
			return actions.RepoCreateResult{
				DestinationPath:   "/dev/project",
				LocalCreated:      true,
				PartialSuccess:    true,
				RetryAllowed:      true,
				ExistingLocalPath: "/dev/project",
			}, errors.New("gh auth required")
		},
	})

	m, _ = update(m, repoCreateKey("n"))
	m, _ = update(m, repoCreateKey("project"))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, cmd())
	if m.Overlay() != ui.OverlayForm {
		t.Fatalf("Overlay() = %d, want retry form", m.Overlay())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, repoCreateKey("other"))
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("changed retry name should fail validation before running create command")
	}
	if len(calls) != 1 {
		t.Fatalf("CreateRepo calls = %d, want original call only", len(calls))
	}
	if !strings.Contains(m.View(), "repo name must remain project when retrying GitHub setup") {
		t.Fatalf("retry validation should preserve form state:\n%s", m.View())
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = update(m, repoCreateKey("project"))
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected retry command after restoring original name")
	}
	_ = cmd()
	if len(calls) != 2 {
		t.Fatalf("CreateRepo calls = %d, want retry call", len(calls))
	}
	if !calls[1].RemoteOnlyRetry || calls[1].ExistingLocalPath != "/dev/project" {
		t.Fatalf("retry options = %#v, want preserved retry path", calls[1])
	}
}

func TestModel_StaleRepoCreateResultsAreIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{RepoCreateRoot: "/dev"})

	m, cmd := update(m, model.RepoCreatedMsg{Request: 99, Result: actions.RepoCreateResult{DestinationPath: "/dev/project"}})
	if cmd != nil {
		t.Fatal("stale repo create result should not refresh")
	}
	if m.TransientError() != "" {
		t.Fatalf("stale repo create result should not set status, got %q", m.TransientError())
	}
}
