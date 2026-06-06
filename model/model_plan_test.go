package model_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
