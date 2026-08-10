package model_test

import (
	"errors"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

var beadDetailSubviews = []struct {
	name string
	key  rune
	mode ui.Mode
}{
	{name: "ready", key: 'r', mode: ui.ModeBeadsReady},
	{name: "blocked", key: 'b', mode: ui.ModeBeadsBlocked},
	{name: "open", key: 'o', mode: ui.ModeBeadsOpen},
	{name: "in-progress", key: 'i', mode: ui.ModeBeadsInProgress},
	{name: "closed", key: 'c', mode: ui.ModeBeadsClosed},
}

func TestBeadDetailEnterPagesVisibleFilteredSelectionInEverySubview(t *testing.T) {
	for _, tt := range beadDetailSubviews {
		t.Run(tt.name, func(t *testing.T) {
			showCalls := 0
			showRepo := ""
			showID := ""
			paged := []string{}
			opts := beadQueryOptions()
			opts.ShowBead = func(repoPath, beadID string) (string, error) {
				showCalls++
				showRepo = repoPath
				showID = beadID
				return "\x1b[1mraw detail\x1b[0m\n\n", nil
			}
			opts.PageText = func(body string) (actions.TerminalLaunchSpec, error) {
				paged = append(paged, body)
				return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
			}

			m := settledBeadDetailModel(t, opts, tt.mode, tt.key, []beadsquery.Bead{
				{ID: "bd-hidden", Title: "Unrelated"},
				{ID: "bd-selected", Title: "Needle"},
			})
			m = setBeadsQuery(t, m, "needle")

			m, fetchCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if fetchCmd == nil {
				t.Fatal("enter returned nil detail command")
			}
			if showCalls != 0 || len(paged) != 0 {
				t.Fatalf("enter performed work eagerly: show calls=%d paged=%#v", showCalls, paged)
			}

			msg := fetchCmd()
			if showCalls != 1 || showRepo != "/dev/alpha" || showID != "bd-selected" {
				t.Fatalf("ShowBead call = (%d, %q, %q), want (1, %q, %q)", showCalls, showRepo, showID, "/dev/alpha", "bd-selected")
			}
			result, ok := msg.(model.BeadDetailResultMsg)
			if !ok {
				t.Fatalf("detail command returned %T, want BeadDetailResultMsg", msg)
			}
			if result.RepoPath != "/dev/alpha" || result.Mode != tt.mode || result.BeadID != "bd-selected" || result.DiffRequest == 0 || result.Body != "\x1b[1mraw detail\x1b[0m\n\n" {
				t.Fatalf("detail result = %#v, want captured repo/mode/bead/request and raw body", result)
			}

			m, pagerCmd := update(m, result)
			if len(paged) != 1 || paged[0] != "\x1b[1mraw detail\x1b[0m\n\n" {
				t.Fatalf("PageText bodies = %#v, want exact raw detail", paged)
			}
			if pagerCmd == nil {
				t.Fatal("matching detail result returned nil pager launch command")
			}
			if _, ok := pagerCmd().(model.TerminalResultMsg); !ok {
				t.Fatalf("pager command result has unexpected type")
			}
		})
	}
}

func TestBeadDetailEnterRequiresVisibleSettledSelection(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, model.Options) model.Model
	}{
		{
			name: "empty settled pane",
			setup: func(t *testing.T, opts model.Options) model.Model {
				return settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', nil)
			},
		},
		{
			name: "settled zero-match filter",
			setup: func(t *testing.T, opts model.Options) model.Model {
				m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', []beadsquery.Bead{{ID: "bd-one", Title: "One"}})
				return setBeadsQuery(t, m, "does-not-match")
			},
		},
		{
			name: "pending pane with retained hidden selection",
			setup: func(t *testing.T, opts model.Options) model.Model {
				opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
				m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', []beadsquery.Bead{{ID: "bd-zero"}, {ID: "bd-retained"}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
				if !m.BeadsPending(ui.ModeBeadsOpen) || m.BeadsSelected(ui.ModeBeadsOpen) != 1 {
					t.Fatalf("pending setup lost retained selection: pending=%v selected=%d", m.BeadsPending(ui.ModeBeadsOpen), m.BeadsSelected(ui.ModeBeadsOpen))
				}
				return m
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			showCalls := 0
			opts := beadQueryOptions()
			opts.ShowBead = func(string, string) (string, error) {
				showCalls++
				return "unexpected", nil
			}
			m := tt.setup(t, opts)

			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil || showCalls != 0 {
				t.Fatalf("enter = cmd %T show calls %d, want inert", cmd, showCalls)
			}
		})
	}
}

func TestBeadDetailRefreshInvalidatesResultAfterSameBeadSettles(t *testing.T) {
	paged := []string{}
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	opts.ShowBead = func(string, string) (string, error) { return "stale detail", nil }
	opts.PageText = func(body string) (actions.TerminalLaunchSpec, error) {
		paged = append(paged, body)
		return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
	}
	m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', []beadsquery.Bead{{ID: "bd-same"}})

	m, detailCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if detailCmd == nil {
		t.Fatal("enter returned nil detail command")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	if !m.BeadsPending(ui.ModeBeadsOpen) {
		t.Fatal("f5 did not mark Beads Open pending")
	}
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-same"}})

	result, ok := detailCmd().(model.BeadDetailResultMsg)
	if !ok {
		t.Fatalf("detail command returned unexpected message")
	}
	m, pagerCmd := update(m, result)
	if pagerCmd != nil || len(paged) != 0 {
		t.Fatalf("pre-refresh detail was accepted after refetch settled: cmd=%T paged=%#v", pagerCmd, paged)
	}
	if got := m.TransientError(); got != "" {
		t.Fatalf("stale success changed status to %q", got)
	}
}

func TestBeadDetailStaleSuccessesAreIgnored(t *testing.T) {
	for _, tt := range beadDetailStaleTransitions() {
		t.Run(tt.name, func(t *testing.T) {
			paged := []string{}
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			opts.ShowBead = func(string, string) (string, error) { return "stale body", nil }
			opts.PageText = func(body string) (actions.TerminalLaunchSpec, error) {
				paged = append(paged, body)
				return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
			}
			m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', beadDetailRows())
			m, detailCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if detailCmd == nil {
				t.Fatal("enter returned nil detail command")
			}
			m = tt.apply(t, m)

			result, ok := detailCmd().(model.BeadDetailResultMsg)
			if !ok {
				t.Fatalf("detail command returned unexpected message")
			}
			m, pagerCmd := update(m, result)
			if pagerCmd != nil || len(paged) != 0 {
				t.Fatalf("stale success had an effect: cmd=%T paged=%#v", pagerCmd, paged)
			}
			if got := m.TransientError(); got != "" {
				t.Fatalf("stale success changed status to %q", got)
			}
		})
	}
}

func TestBeadDetailStaleErrorsAreIgnored(t *testing.T) {
	for _, tt := range beadDetailStaleTransitions() {
		t.Run(tt.name, func(t *testing.T) {
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			opts.ShowBead = func(string, string) (string, error) { return "", errors.New("stale failure") }
			m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', beadDetailRows())
			m, detailCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if detailCmd == nil {
				t.Fatal("enter returned nil detail command")
			}
			m = tt.apply(t, m)
			m, _ = update(m, model.TerminalResultMsg{Err: "preserve status"})
			before := m.TransientError()

			errMsg, ok := detailCmd().(model.FetchErrorMsg)
			if !ok {
				t.Fatalf("detail command returned unexpected error message")
			}
			m, cmd := update(m, errMsg)
			if cmd != nil || m.TransientError() != before {
				t.Fatalf("stale error had an effect: cmd=%T status=%q, want unchanged %q", cmd, m.TransientError(), before)
			}
		})
	}
}

func TestBeadDetailCurrentErrorIsDisplayedWithCapturedIdentity(t *testing.T) {
	opts := beadQueryOptions()
	opts.ShowBead = func(string, string) (string, error) { return "", errors.New("detail unavailable") }
	m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', beadDetailRows())
	m, detailCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})

	errMsg, ok := detailCmd().(model.FetchErrorMsg)
	if !ok {
		t.Fatalf("detail command returned unexpected message")
	}
	if errMsg.RepoPath != "/dev/alpha" || errMsg.Pane != "bead detail" || errMsg.Kind != model.FetchBeadDetail ||
		errMsg.Mode != ui.ModeBeadsOpen || errMsg.DiffRequest == 0 || errMsg.BeadID != "bd-original" || errMsg.Err != "failed to load bead detail: detail unavailable" {
		t.Fatalf("detail error = %#v, want captured repo/pane/kind/mode/request/bead and wrapped text", errMsg)
	}
	m, _ = update(m, errMsg)
	if got := m.TransientError(); got != "failed to load bead detail: detail unavailable" {
		t.Fatalf("current detail error status = %q", got)
	}
}

func TestBeadDetailCursorMayMoveAwayAndBackBeforeCompletion(t *testing.T) {
	paged := []string{}
	opts := beadQueryOptions()
	opts.ShowBead = func(string, string) (string, error) { return "current body", nil }
	opts.PageText = func(body string) (actions.TerminalLaunchSpec, error) {
		paged = append(paged, body)
		return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
	}
	m := settledBeadDetailModel(t, opts, ui.ModeBeadsOpen, 'o', beadDetailRows())
	m, detailCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})

	result := detailCmd().(model.BeadDetailResultMsg)
	_, pagerCmd := update(m, result)
	if pagerCmd == nil || len(paged) != 1 || paged[0] != "current body" {
		t.Fatalf("returned selection did not accept latest request: cmd=%T paged=%#v", pagerCmd, paged)
	}
}

type beadDetailStaleTransition struct {
	name  string
	apply func(*testing.T, model.Model) model.Model
}

func beadDetailStaleTransitions() []beadDetailStaleTransition {
	return []beadDetailStaleTransition{
		{
			name: "repeated enter on same bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
				return m
			},
		},
		{
			name: "cursor moves to different bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
				return m
			},
		},
		{
			name: "filter selects different bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				return setBeadsQuery(t, m, "other")
			},
		},
		{
			name: "filter has no selected bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				return setBeadsQuery(t, m, "missing")
			},
		},
		{
			name: "switches subview containing same id",
			apply: func(t *testing.T, m model.Model) model.Model {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				return applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-original"}})
			},
		},
		{
			name: "switches away and back to settled same bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
				return applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-original"}})
			},
		},
		{
			name: "changes repository",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
				return m
			},
		},
		{
			name: "changes repository away and back to settled same bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyUp})
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
				return applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-original"}})
			},
		},
		{
			name: "starts refresh and hides retained rows",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
				if !m.BeadsPending(ui.ModeBeadsOpen) {
					t.Fatal("refresh did not leave Beads pending")
				}
				return m
			},
		},
		{
			name: "refresh settles again on same bead",
			apply: func(t *testing.T, m model.Model) model.Model {
				t.Helper()
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
				return applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-original"}})
			},
		},
	}
}

func beadDetailRows() []beadsquery.Bead {
	return []beadsquery.Bead{
		{ID: "bd-original", Title: "Original"},
		{ID: "bd-other", Title: "Other"},
	}
}

func settledBeadDetailModel(t *testing.T, opts model.Options, mode ui.Mode, key rune, beads []beadsquery.Bead) model.Model {
	t.Helper()
	m := model.NewWithOptions(testRepos(), opts)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if mode != ui.ModeBeadsReady {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	}
	return applyBeadsResult(t, m, mode, true, beads)
}
