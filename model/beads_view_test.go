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

func TestBeadsSubviewLettersSwitchAndStartMatchingDeferredQuery(t *testing.T) {
	tests := []struct {
		key  rune
		mode ui.Mode
		name string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, name: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, name: "blocked"},
		{key: 'o', mode: ui.ModeBeadsOpen, name: "open"},
		{key: 'i', mode: ui.ModeBeadsInProgress, name: "in-progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, name: "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := ""
			query := func(name string) func(string) ([]beadsquery.Bead, error) {
				return func(string) ([]beadsquery.Bead, error) {
					called = name
					return []beadsquery.Bead{{ID: "bd-" + name, Priority: 1, Title: name}}, nil
				}
			}
			m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
				ListReadyBeads:      query("ready"),
				ListBlockedBeads:    query("blocked"),
				ListOpenBeads:       query("open"),
				ListInProgressBeads: query("in-progress"),
				ListClosedBeads:     query("closed"),
			}))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode == ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
			}
			before := m.ListRequest(tt.mode)

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			if m.Mode() != tt.mode {
				t.Fatalf("mode = %v, want %v", m.Mode(), tt.mode)
			}
			if cmd == nil {
				t.Fatal("letter switch returned nil query command")
			}
			if m.ListRequest(tt.mode) == before {
				t.Fatalf("request token for %v did not advance", tt.mode)
			}
			if called != "" {
				t.Fatalf("query ran before command execution: %q", called)
			}
			msg := cmd()
			if called != tt.name {
				t.Fatalf("query = %q, want %q", called, tt.name)
			}
			if got := beadsResultMode(msg); got != tt.mode {
				t.Fatalf("result mode = %v for %T, want %v", got, msg, tt.mode)
			}
		})
	}
}

func beadsResultMode(msg tea.Msg) ui.Mode {
	switch msg.(type) {
	case model.BeadsReadyResultMsg:
		return ui.ModeBeadsReady
	case model.BeadsBlockedResultMsg:
		return ui.ModeBeadsBlocked
	case model.BeadsOpenResultMsg:
		return ui.ModeBeadsOpen
	case model.BeadsInProgressResultMsg:
		return ui.ModeBeadsInProgress
	case model.BeadsClosedResultMsg:
		return ui.ModeBeadsClosed
	default:
		return 0
	}
}

func TestBeadsSubviewActiveLetterIsHandledNoOp(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			before := m.ListRequest(tt.mode)
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			if m.Mode() != tt.mode || cmd != nil || m.ListRequest(tt.mode) != before {
				t.Fatalf("active letter changed state: mode=%v cmd=%T request=%d, want mode=%v nil %d", m.Mode(), cmd, m.ListRequest(tt.mode), tt.mode, before)
			}
		})
	}
}

func TestBeadsSubviewLettersAreScopedOutsideBeads(t *testing.T) {
	for _, key := range []rune{'r', 'b', 'o', 'i', 'c'} {
		t.Run(string(key), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			if ui.IsBeadsMode(m.Mode()) {
				t.Fatalf("key %q entered Beads outside the Beads group: mode=%v", key, m.Mode())
			}
		})
	}
}

func TestKeyFiveAlwaysTargetsOpenWithinBeads(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			before := m.ListRequest(ui.ModeBeadsOpen)
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if m.Mode() != ui.ModeBeadsOpen {
				t.Fatalf("mode = %v, want ModeBeadsOpen", m.Mode())
			}
			if tt.mode == ui.ModeBeadsOpen {
				if cmd != nil || m.ListRequest(ui.ModeBeadsOpen) != before {
					t.Fatalf("active key 5 changed Open: cmd=%T request=%d want nil/%d", cmd, m.ListRequest(ui.ModeBeadsOpen), before)
				}
			} else if cmd == nil || m.ListRequest(ui.ModeBeadsOpen) == before {
				t.Fatalf("key 5 from %v did not start Open fetch: cmd=%T request=%d before=%d", tt.mode, cmd, m.ListRequest(ui.ModeBeadsOpen), before)
			}
		})
	}
}

func TestReadyAndOpenKeepIndependentResultsWithSharedID(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-shared", Title: "Ready copy"}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-shared", Title: "Open copy"}})

	if got := m.BeadsReady(); len(got) != 1 || got[0].Title != "Ready copy" || !m.BeadsAvailable(ui.ModeBeadsReady) {
		t.Fatalf("Ready state = %#v available=%v, want independent shared result", got, m.BeadsAvailable(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 1 || got[0].Title != "Open copy" || !m.BeadsAvailable(ui.ModeBeadsOpen) {
		t.Fatalf("Open state = %#v available=%v, want independent shared result", got, m.BeadsAvailable(ui.ModeBeadsOpen))
	}
}

func TestBeadsResultForPriorSubviewAfterLetterSwitchIsRejected(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sourceKey rune
		source    ui.Mode
		targetKey rune
		target    ui.Mode
	}{
		{name: "ready", sourceKey: 'r', source: ui.ModeBeadsReady, targetKey: 'b', target: ui.ModeBeadsBlocked},
		{name: "blocked", sourceKey: 'b', source: ui.ModeBeadsBlocked, targetKey: 'o', target: ui.ModeBeadsOpen},
		{name: "open", sourceKey: 'o', source: ui.ModeBeadsOpen, targetKey: 'i', target: ui.ModeBeadsInProgress},
		{name: "in-progress", sourceKey: 'i', source: ui.ModeBeadsInProgress, targetKey: 'c', target: ui.ModeBeadsClosed},
		{name: "closed", sourceKey: 'c', source: ui.ModeBeadsClosed, targetKey: 'r', target: ui.ModeBeadsReady},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.source != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.sourceKey}})
			}
			sourceRequest := m.ListRequest(tt.source)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.targetKey}})

			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.source, "/dev/alpha", sourceRequest, available, []beadsquery.Bead{{ID: "bd-stale", Title: "Stale"}})
				if len(m.Beads(tt.source)) != 0 || m.BeadsAvailable(tt.source) || !m.BeadsPending(tt.target) {
					t.Fatalf("prior-mode result changed state: source=%#v available=%v targetPending=%v", m.Beads(tt.source), m.BeadsAvailable(tt.source), m.BeadsPending(tt.target))
				}
			}
		})
	}
}

func TestBeadsSubviewStaleRepoAndSupersededRequestResultsAreRejectedPerMode(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
	}{
		{key: 'r', mode: ui.ModeBeadsReady},
		{key: 'b', mode: ui.ModeBeadsBlocked},
		{key: 'o', mode: ui.ModeBeadsOpen},
		{key: 'i', mode: ui.ModeBeadsInProgress},
		{key: 'c', mode: ui.ModeBeadsClosed},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			opts := beadQueryOptions()
			opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
			m := inRightPane(model.NewWithOptions(testRepos(), opts))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			alphaRequest := m.ListRequest(tt.mode)
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			bravoRequest := m.ListRequest(tt.mode)

			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.mode, "/dev/alpha", alphaRequest, available, []beadsquery.Bead{{ID: "bd-stale-repo"}})
				assertBeadsModePending(t, m, tt.mode)
			}

			m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, true, []beadsquery.Bead{{ID: "bd-current"}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
			if m.ListRequest(tt.mode) == bravoRequest {
				t.Fatal("F5 did not supersede the active Beads request")
			}
			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, available, []beadsquery.Bead{{ID: "bd-stale-request"}})
				assertBeadsModePending(t, m, tt.mode)
			}
		})
	}
}

func TestBeadsSubviewFetchClearsOnlyTargetState(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-open"}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-ready"}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsBlocked, true, []beadsquery.Bead{{ID: "bd-blocked"}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil || !m.BeadsPending(ui.ModeBeadsReady) || len(m.BeadsReady()) != 0 || m.BeadsAvailable(ui.ModeBeadsReady) {
		t.Fatalf("Ready target did not enter cleared loading state: rows=%#v available=%v pending=%v", m.BeadsReady(), m.BeadsAvailable(ui.ModeBeadsReady), m.BeadsPending(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-open" || !m.BeadsAvailable(ui.ModeBeadsOpen) {
		t.Fatalf("Ready fetch changed Open state: %#v available=%v", got, m.BeadsAvailable(ui.ModeBeadsOpen))
	}
	if got := m.BeadsBlocked(); len(got) != 1 || got[0].ID != "bd-blocked" || !m.BeadsAvailable(ui.ModeBeadsBlocked) {
		t.Fatalf("Ready fetch changed Blocked state: %#v available=%v", got, m.BeadsAvailable(ui.ModeBeadsBlocked))
	}
}

func beadQueryOptions() model.Options {
	empty := func(string) ([]beadsquery.Bead, error) { return nil, nil }
	return model.Options{
		ListReadyBeads: empty, ListBlockedBeads: empty, ListOpenBeads: empty,
		ListInProgressBeads: empty, ListClosedBeads: empty,
	}
}

func applyBeadsResult(t *testing.T, m model.Model, mode ui.Mode, available bool, beads []beadsquery.Bead) model.Model {
	t.Helper()
	return applyBeadsResultFor(t, m, mode, "/dev/alpha", m.ListRequest(mode), available, beads)
}

func applyBeadsResultFor(t *testing.T, m model.Model, mode ui.Mode, repoPath string, request uint64, available bool, beads []beadsquery.Bead) model.Model {
	t.Helper()
	var msg tea.Msg
	switch mode {
	case ui.ModeBeadsReady:
		msg = model.BeadsReadyResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsBlocked:
		msg = model.BeadsBlockedResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsOpen:
		msg = model.BeadsOpenResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsInProgress:
		msg = model.BeadsInProgressResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	case ui.ModeBeadsClosed:
		msg = model.BeadsClosedResultMsg{RepoPath: repoPath, ListRequest: request, Available: available, Beads: beads}
	default:
		t.Fatalf("unsupported Beads mode %v", mode)
	}
	next, _ := update(m, msg)
	return next
}

func assertBeadsModePending(t *testing.T, m model.Model, mode ui.Mode) {
	t.Helper()
	if got := m.Beads(mode); len(got) != 0 || m.BeadsAvailable(mode) || !m.BeadsPending(mode) {
		t.Fatalf("Beads mode %v state = rows %#v available=%v pending=%v, want cleared pending", mode, got, m.BeadsAvailable(mode), m.BeadsPending(mode))
	}
}

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
	if !m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = false before asynchronous result")
	}
	if view := m.View(); !strings.Contains(view, "loading open beads") || strings.Contains(view, "beads not configured") {
		t.Fatalf("pending Beads fetch did not render a neutral loading state:\n%s", view)
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
	if m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = true after successful query")
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
	if m.BeadsOpenPending() {
		t.Fatal("BeadsOpenPending() = true after unavailable result")
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
	if len(m.BeadsOpen()) != 0 || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("repo change Beads state: rows=%#v available=%v pending=%v, want empty pending state", m.BeadsOpen(), m.BeadsOpenAvailable(), m.BeadsOpenPending())
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
		assertBeadsOpenPending(t, m)
	}
}

func assertCurrentBravoBead(t *testing.T, m model.Model) {
	t.Helper()
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-bravo" || !m.BeadsOpenAvailable() {
		t.Fatalf("stale result changed current Beads state: rows=%#v available=%v", got, m.BeadsOpenAvailable())
	}
}

func assertBeadsOpenPending(t *testing.T, m model.Model) {
	t.Helper()
	if got := m.BeadsOpen(); len(got) != 0 || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("stale result changed pending Beads state: rows=%#v available=%v pending=%v", got, m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
}

func TestBeadsSubviews_RightPaneSlashIsSilentNoOp(t *testing.T) {
	for _, key := range []rune{'r', 'b', 'o', 'i', 'c'} {
		t.Run(string(key), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if key != 'o' {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			}

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			if cmd != nil {
				t.Fatalf("right-pane slash command = %T, want nil", cmd)
			}
			if m.SearchActive() || m.ItemSearch() != "" {
				t.Fatalf("right-pane slash exposed deferred Beads filter: active=%v query=%q", m.SearchActive(), m.ItemSearch())
			}
		})
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
	m, _ = update(m, model.BeadsOpenResultMsg{
		RepoPath:    "/dev/alpha",
		ListRequest: m.ListRequest(ui.ModeBeadsOpen),
		Available:   true,
		Beads:       []beadsquery.Bead{{ID: "bd-old", Priority: 1, Title: "Old result"}},
	})
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
	if len(m.BeadsOpen()) != 0 || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("F5 pending state: rows=%#v available=%v pending=%v, want cleared pending state", m.BeadsOpen(), m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
	if view := m.View(); !strings.Contains(view, "loading open beads") || strings.Contains(view, "bd-old") {
		t.Fatalf("F5 did not replace stale rows with loading state:\n%s", view)
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

func TestBeadsSubviews_AreExcludedFromHorizontalCycle(t *testing.T) {
	for _, subview := range []rune{'r', 'b', 'o', 'i', 'c'} {
		t.Run(string(subview), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if subview != 'o' {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview}})
			}
			want := m.Mode()
			for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight} {
				var cmd tea.Cmd
				m, cmd = update(m, tea.KeyMsg{Type: key})
				if m.Mode() != want || cmd != nil {
					t.Fatalf("arrow %v changed Beads view: mode=%v cmd=%T want=%v", key, m.Mode(), cmd, want)
				}
			}
		})
	}

	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight})
	if ui.IsBeadsMode(m.Mode()) {
		t.Fatal("top-level arrow cycle entered deferred Beads stop")
	}
}

func TestBeadsOpen_DoesNotExtendFrozenStartupVocabulary(t *testing.T) {
	if got := len(model.ViewChoices()); got != 9 {
		t.Fatalf("ViewChoices length = %d, want frozen 9", got)
	}
	for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
		if _, ok := model.ViewNumber(mode); ok {
			t.Fatalf("ViewNumber(%v) unexpectedly extended frozen vocabulary", mode)
		}
	}
	if _, ok := model.ModeForViewNumber(10); ok {
		t.Fatal("ModeForViewNumber(10) unexpectedly enabled deferred Beads startup")
	}
	for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
		m := model.NewWithOptions(testRepos(), model.Options{StartupMode: mode})
		if m.Mode() != ui.ModeWorktrees {
			t.Fatalf("StartupMode %v produced mode %v, want existing fallback ModeWorktrees", mode, m.Mode())
		}
	}
}

func TestBeadsOpen_CursorUsesGroupedContentHeightAndScroll(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), model.Options{
		ListOpenBeads: func(string) ([]beadsquery.Bead, error) { return nil, nil },
	}))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
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

func TestBeadsSubviews_ModelViewUsesExactQuietStates(t *testing.T) {
	for _, tt := range []struct {
		key  rune
		mode ui.Mode
		name string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, name: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, name: "blocked"},
		{key: 'o', mode: ui.ModeBeadsOpen, name: "open"},
		{key: 'i', mode: ui.ModeBeadsInProgress, name: "in-progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, name: "closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			if view := m.View(); !strings.Contains(view, "loading "+tt.name+" beads") || strings.Contains(view, "beads not configured") {
				t.Fatalf("pending view has wrong quiet state:\n%s", view)
			}
			m = applyBeadsResult(t, m, tt.mode, true, nil)
			if view := m.View(); !strings.Contains(view, "no "+tt.name+" beads") || strings.Contains(view, "beads not configured") {
				t.Fatalf("empty view has wrong quiet state:\n%s", view)
			}
			m = applyBeadsResult(t, m, tt.mode, false, []beadsquery.Bead{{ID: "bd-ignored"}})
			if view := m.View(); !strings.Contains(view, "beads not configured") || strings.Contains(view, "no "+tt.name+" beads") {
				t.Fatalf("unavailable view has wrong quiet state:\n%s", view)
			}
			if m.TransientError() != "" {
				t.Fatalf("unavailable state exposed status %q", m.TransientError())
			}
		})
	}
}
