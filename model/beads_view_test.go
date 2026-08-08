package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

func TestBeadsOpen_KeyFiveStartsDeferredFetchForSelectedRepo(t *testing.T) {
	calls := 0
	queriedRepo := ""
	m := model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			calls++
			queriedRepo = repoPath
			return []beadsquery.Bead{{ID: "bd-1", Priority: 1, Title: "First"}}, nil
		},
	})
	m = inRightPane(m)
	beforeRequest := m.ListRequest(ui.ModeBeadsOpen)
	if beforeRequest == 0 {
		t.Fatal("new model has zero Beads Open request token")
	}

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if m.Mode() != ui.ModeBeadsOpen {
		t.Fatalf("mode = %v, want ModeBeadsOpen", m.Mode())
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == beforeRequest {
		t.Fatalf("Beads Open request = %d, want advancement from %d", request, beforeRequest)
	}
	if cmd == nil {
		t.Fatal("key 5 returned nil fetch command")
	}
	if calls != 0 {
		t.Fatalf("ListOpenBeads ran before command execution: %d call(s)", calls)
	}

	msg := cmd()
	if calls != 1 || queriedRepo != "/dev/alpha" {
		t.Fatalf("ListOpenBeads call = (%d, %q), want (1, %q)", calls, queriedRepo, "/dev/alpha")
	}
	result, ok := msg.(model.BeadsOpenResultMsg)
	if !ok {
		t.Fatalf("fetch message = %T, want BeadsOpenResultMsg", msg)
	}
	if result.RepoPath != "/dev/alpha" || !result.Available || result.ListRequest != m.ListRequest(ui.ModeBeadsOpen) {
		t.Fatalf("fetch result = %#v, want current available alpha result", result)
	}
}

func TestBeadsOpen_QueryFailureProducesUnavailableResult(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) {
			return nil, errors.New("bd exploded")
		},
	}))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	rawMsg := cmd()
	msg, ok := rawMsg.(model.BeadsOpenResultMsg)
	if !ok {
		t.Fatalf("fetch message = %T, want BeadsOpenResultMsg", rawMsg)
	}
	if msg.Available || len(msg.Beads) != 0 {
		t.Fatalf("failed query result = %#v, want unavailable without rows", msg)
	}
	if got := m.TransientError(); got != "" {
		t.Fatalf("TransientError() = %q before result application, want empty", got)
	}
}

func TestBeadsOpen_CurrentSuccessReplacesPaneAndMarksAvailable(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-1", Priority: 1, Title: "First"},
			{ID: "bd-2", Priority: 2, Title: "Second"},
		},
	})

	if got := m.BeadsOpen(); len(got) != 2 || got[1].ID != "bd-2" {
		t.Fatalf("BeadsOpen() = %#v, want current result", got)
	}
	if !m.BeadsOpenAvailable() {
		t.Fatal("BeadsOpenAvailable() = false after successful query")
	}
}

func TestBeadsOpen_CurrentUnavailableClearsPaneWithoutFetchStatus(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	request := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: request,
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-old", Title: "Old"}},
	})

	m, _ = update(m, model.BeadsOpenResultMsg{RepoPath: "/dev/alpha", ListRequest: request})

	if got := m.BeadsOpen(); len(got) != 0 {
		t.Fatalf("BeadsOpen() = %#v, want cleared pane", got)
	}
	if m.BeadsOpenAvailable() {
		t.Fatal("BeadsOpenAvailable() = true after unavailable result")
	}
	if got := m.TransientError(); got != "" {
		t.Fatalf("TransientError() = %q, want blanket UI state without fetch error", got)
	}
}

func TestBeadsOpen_RepoCursorChangeClearsAvailabilityAndRefetches(t *testing.T) {
	queriedRepo := ""
	m := model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			queriedRepo = repoPath
			return nil, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-alpha", Title: "Alpha"}},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	before := m.ListRequest(ui.ModeBeadsOpen)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("repo cursor change returned nil Beads fetch command")
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == before {
		t.Fatalf("Beads request = %d, want advancement from %d", request, before)
	}
	if len(m.BeadsOpen()) != 0 || m.BeadsOpenAvailable() {
		t.Fatalf("repo change kept Beads state: rows=%#v available=%v", m.BeadsOpen(), m.BeadsOpenAvailable())
	}
	if queriedRepo != "" {
		t.Fatalf("ListOpenBeads ran before command execution for %q", queriedRepo)
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(model.BeadsOpenResultMsg)
	if !ok {
		t.Fatalf("repo change fetch = %T, want BeadsOpenResultMsg", rawMsg)
	}
	if queriedRepo != "/dev/bravo" || msg.RepoPath != "/dev/bravo" {
		t.Fatalf("repo change queried %q and returned %q, want /dev/bravo", queriedRepo, msg.RepoPath)
	}
}

func TestBeadsOpen_StaleSuccessAndUnavailableResultsAreIgnored(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) { return testRepos(), nil },
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) {
			return nil, nil
		},
	})
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	alphaRequest := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	bravoRequest := m.ListRequest(ui.ModeBeadsOpen)
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/bravo",
		ListRequest: bravoRequest,
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-bravo", Title: "Bravo"}},
	})

	staleRows := []beadsquery.Bead{{ID: "bd-stale", Title: "Stale"}}
	for _, stale := range []model.BeadsOpenResultMsg{
		{RepoPath: "/dev/alpha", ListRequest: alphaRequest, Available: true, Beads: staleRows},
		{RepoPath: "/dev/alpha", ListRequest: alphaRequest},
	} {
		m, _ = update(m, stale)
		assertCurrentBravoBead(t, m)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.ListRequest(ui.ModeBeadsOpen) == bravoRequest {
		t.Fatal("F5 did not supersede Beads Open request")
	}
	for _, stale := range []model.BeadsOpenResultMsg{
		{RepoPath: "/dev/bravo", ListRequest: bravoRequest, Available: true, Beads: staleRows},
		{RepoPath: "/dev/bravo", ListRequest: bravoRequest},
	} {
		m, _ = update(m, stale)
		assertCurrentBravoBead(t, m)
	}
}

func assertCurrentBravoBead(t *testing.T, m model.Model) {
	t.Helper()
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-bravo" || !m.BeadsOpenAvailable() {
		t.Fatalf("stale result changed current Beads state: rows=%#v available=%v", got, m.BeadsOpenAvailable())
	}
}

func TestBeadsOpen_RightPaneSlashIsSilentNoOp(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	if cmd != nil {
		t.Fatalf("right-pane slash command = %T, want nil", cmd)
	}
	if m.SearchActive() || m.ItemSearch() != "" {
		t.Fatalf("right-pane slash exposed deferred Beads filter: active=%v query=%q", m.SearchActive(), m.ItemSearch())
	}
}

func TestBeadsOpen_F5RefetchesSelectedRepo(t *testing.T) {
	var queried []string
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ScanRepos: func() ([]scanner.Repo, error) { return testRepos(), nil },
		ListOpenBeads: func(repoPath string) ([]beadsquery.Bead, error) {
			queried = append(queried, repoPath)
			return nil, nil
		},
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	before := m.ListRequest(ui.ModeBeadsOpen)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
	if cmd == nil {
		t.Fatal("F5 returned nil command")
	}
	if request := m.ListRequest(ui.ModeBeadsOpen); request == before {
		t.Fatalf("Beads request = %d, want advancement from %d", request, before)
	}
	if len(queried) != 0 {
		t.Fatalf("ListOpenBeads ran before refresh command execution: %v", queried)
	}
	msgs := runBatchCmd(t, cmd)
	if len(queried) != 1 || queried[0] != "/dev/alpha" {
		t.Fatalf("F5 queried repos = %v, want [/dev/alpha]", queried)
	}
	if !hasListFetchForMode(msgs, ui.ModeBeadsOpen, m.ListRequest(ui.ModeBeadsOpen)) {
		t.Fatalf("F5 messages = %#v, want current Beads Open result", msgs)
	}
}

func TestBeadsOpen_ModelViewExposesRowsSelectionAndAvailability(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 16})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-1", Priority: 1, Title: "First"},
			{ID: "bd-2", Priority: 2, Title: "Second", Assignee: "alice"},
		},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()
	if !strings.Contains(view, "   bd-1  P1  First") || !strings.Contains(view, " > bd-2  P2  Second  alice") {
		t.Fatalf("model view did not expose Beads pane selection:\n%s", view)
	}
}

func TestBeadsOpen_KeyFiveIsInertWhileRepoPaneFocused(t *testing.T) {
	m := model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	})
	before := m.ListRequest(ui.ModeBeadsOpen)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	if m.Mode() != ui.ModeWorktrees || cmd != nil || m.ListRequest(ui.ModeBeadsOpen) != before {
		t.Fatalf("left-pane key 5 changed state: mode=%v cmd=%T request=%d want mode=%v request=%d", m.Mode(), cmd, m.ListRequest(ui.ModeBeadsOpen), ui.ModeWorktrees, before)
	}
}

func TestBeadsOpen_IsExcludedFromHorizontalCycle(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight} {
		var cmd tea.Cmd
		m, cmd = update(m, tea.KeyMsg{Type: key})
		if m.Mode() != ui.ModeBeadsOpen || cmd != nil {
			t.Fatalf("arrow %v changed Beads tracer view: mode=%v cmd=%T", key, m.Mode(), cmd)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() == ui.ModeBeadsOpen {
		t.Fatal("top-level arrow cycle entered deferred Beads stop")
	}
}

func TestBeadsOpen_DoesNotExtendFrozenStartupVocabulary(t *testing.T) {
	if got := len(model.ViewChoices()); got != 9 {
		t.Fatalf("ViewChoices length = %d, want frozen 9", got)
	}
	if _, ok := model.ViewNumber(ui.ModeBeadsOpen); ok {
		t.Fatal("ViewNumber(ModeBeadsOpen) unexpectedly extended frozen vocabulary")
	}
	if _, ok := model.ModeForViewNumber(10); ok {
		t.Fatal("ModeForViewNumber(10) unexpectedly enabled deferred Beads startup")
	}
	m := model.NewWithOptions(testRepos(), model.Options{StartupMode: ui.ModeBeadsOpen})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("StartupMode ModeBeadsOpen produced mode %v, want existing fallback ModeWorktrees", m.Mode())
	}
}

func TestBeadsOpen_CursorUsesNormalContentHeightAndScroll(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BranchContentOverhead + 2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads: []beadsquery.Bead{
			{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"}, {ID: "bd-3"},
		},
	})
	for range 3 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	if m.BeadsOpenSelected() != 3 {
		t.Fatalf("BeadsOpenSelected() = %d, want 3", m.BeadsOpenSelected())
	}
	if scroll := m.BeadsOpenScroll(); scroll == 0 || m.BeadsOpenSelected() >= scroll+2 {
		t.Fatalf("Beads selection %d not visible in height-2 viewport at scroll %d", m.BeadsOpenSelected(), scroll)
	}
	if !strings.Contains(m.View(), "> bd-3") {
		t.Fatalf("selected Bead did not render after scrolling:\n%s", m.View())
	}
}
