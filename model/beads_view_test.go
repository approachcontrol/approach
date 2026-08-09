package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
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

func TestKeyFiveReentersRememberedBeadsSubview(t *testing.T) {
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
			if m.Mode() != ui.ModeBeadsOpen {
				t.Fatalf("first key 5 mode = %v, want ModeBeadsOpen", m.Mode())
			}
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			before := listRequests(m)
			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if m.Mode() != tt.mode || cmd != nil {
				t.Fatalf("active key 5 changed Beads state: mode=%v cmd=%T, want %v and nil", m.Mode(), cmd, tt.mode)
			}
			assertListRequestsUnchanged(t, before, m)

			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			before = listRequests(m)
			m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if m.Mode() != tt.mode {
				t.Fatalf("re-entry mode = %v, want remembered %v", m.Mode(), tt.mode)
			}
			if cmd == nil {
				t.Fatal("re-entry returned nil fetch command")
			}
			assertOnlyListRequestChanged(t, before, m, tt.mode)
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
				assertBeadsModePending(t, m, tt.mode, "")
			}

			m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, true, []beadsquery.Bead{{ID: "bd-current"}})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
			if m.ListRequest(tt.mode) == bravoRequest {
				t.Fatal("F5 did not supersede the active Beads request")
			}
			for _, available := range []bool{true, false} {
				m = applyBeadsResultFor(t, m, tt.mode, "/dev/bravo", bravoRequest, available, []beadsquery.Bead{{ID: "bd-stale-request"}})
				assertBeadsModePending(t, m, tt.mode, "bd-current")
			}
		})
	}
}

func TestBeadsSubviewFetchPreservesOnlyTargetStateWhilePending(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-open"}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "bd-ready"}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsBlocked, true, []beadsquery.Bead{{ID: "bd-blocked"}})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := m.BeadsReady(); cmd == nil || !m.BeadsPending(ui.ModeBeadsReady) || len(got) != 1 || got[0].ID != "bd-ready" || m.BeadsAvailable(ui.ModeBeadsReady) {
		t.Fatalf("Ready target did not retain rows in loading state: rows=%#v available=%v pending=%v", got, m.BeadsAvailable(ui.ModeBeadsReady), m.BeadsPending(ui.ModeBeadsReady))
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

func assertBeadsModePending(t *testing.T, m model.Model, mode ui.Mode, wantID string) {
	t.Helper()
	got := m.Beads(mode)
	if wantID == "" {
		if len(got) != 0 || m.BeadsAvailable(mode) || !m.BeadsPending(mode) {
			t.Fatalf("Beads mode %v state = rows %#v available=%v pending=%v, want cleared pending", mode, got, m.BeadsAvailable(mode), m.BeadsPending(mode))
		}
		return
	}
	if len(got) != 1 || got[0].ID != wantID || m.BeadsAvailable(mode) || !m.BeadsPending(mode) {
		t.Fatalf("Beads mode %v state = rows %#v available=%v pending=%v, want retained %q pending", mode, got, m.BeadsAvailable(mode), m.BeadsPending(mode), wantID)
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
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-bravo" || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("stale result changed retained pending Beads state: rows=%#v available=%v pending=%v", got, m.BeadsOpenAvailable(), m.BeadsOpenPending())
	}
}

func TestBeadsSubviews_RightPaneSlashFiltersEachSubview(t *testing.T) {
	for _, tt := range []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'r', mode: ui.ModeBeadsReady, query: "needle-id"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "needle title"},
		{key: 'o', mode: ui.ModeBeadsOpen, query: "alice"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "needle-id"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "alice"},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-other", Title: "Other", Assignee: "bob"},
				{ID: "needle-id", Title: "Needle title", Assignee: "alice"},
			})

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			if cmd != nil || !m.SearchActive() {
				t.Fatalf("right-pane slash = cmd %T active %v, want nil/true", cmd, m.SearchActive())
			}
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.query)})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.SearchActive() || m.ItemSearch() != tt.query {
				t.Fatalf("filter lifecycle = active %v query %q, want false/%q", m.SearchActive(), m.ItemSearch(), tt.query)
			}
			if got := m.Beads(tt.mode); len(got) != 1 || got[0].ID != "needle-id" {
				t.Fatalf("filtered Beads = %#v, want needle row", got)
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
	if got := m.BeadsOpen(); len(got) != 1 || got[0].ID != "bd-old" || m.BeadsOpenAvailable() || !m.BeadsOpenPending() {
		t.Fatalf("F5 pending state: rows=%#v available=%v pending=%v, want retained pending state", got, m.BeadsOpenAvailable(), m.BeadsOpenPending())
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

func TestBeadsSubviews_ArrowsCycleWithWrapAndFetchOnlyTarget(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fromKey   rune
		direction tea.KeyType
		want      ui.Mode
	}{
		{name: "ready left wraps closed", fromKey: 'r', direction: tea.KeyLeft, want: ui.ModeBeadsClosed},
		{name: "ready right", fromKey: 'r', direction: tea.KeyRight, want: ui.ModeBeadsBlocked},
		{name: "blocked left", fromKey: 'b', direction: tea.KeyLeft, want: ui.ModeBeadsReady},
		{name: "blocked right", fromKey: 'b', direction: tea.KeyRight, want: ui.ModeBeadsOpen},
		{name: "open left", fromKey: 'o', direction: tea.KeyLeft, want: ui.ModeBeadsBlocked},
		{name: "open right", fromKey: 'o', direction: tea.KeyRight, want: ui.ModeBeadsInProgress},
		{name: "in-progress left", fromKey: 'i', direction: tea.KeyLeft, want: ui.ModeBeadsOpen},
		{name: "in-progress right", fromKey: 'i', direction: tea.KeyRight, want: ui.ModeBeadsClosed},
		{name: "closed left", fromKey: 'c', direction: tea.KeyLeft, want: ui.ModeBeadsInProgress},
		{name: "closed right wraps ready", fromKey: 'c', direction: tea.KeyRight, want: ui.ModeBeadsReady},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.fromKey != 'o' {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.fromKey}})
			}
			before := listRequests(m)

			m, cmd := update(m, tea.KeyMsg{Type: tt.direction})

			if m.Mode() != tt.want || m.ActivePane() != 1 {
				t.Fatalf("arrow mode/pane = %v/%d, want %v/1", m.Mode(), m.ActivePane(), tt.want)
			}
			if cmd == nil {
				t.Fatal("arrow switch returned nil fetch command")
			}
			assertOnlyListRequestChanged(t, before, m, tt.want)
		})
	}
}

func TestTopLevelArrowFromFlowsEntersRememberedBeadsSubview(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	before := listRequests(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.Mode() != ui.ModeBeadsClosed || cmd == nil {
		t.Fatalf("right from Flows = mode %v cmd %T, want remembered Closed fetch", m.Mode(), cmd)
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeBeadsClosed)
}

func TestBeadsStartupFetchesOnlyConfiguredSubview(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode ui.Mode
	}{
		{name: "ready", mode: ui.ModeBeadsReady},
		{name: "blocked", mode: ui.ModeBeadsBlocked},
		{name: "open", mode: ui.ModeBeadsOpen},
		{name: "in-progress", mode: ui.ModeBeadsInProgress},
		{name: "closed", mode: ui.ModeBeadsClosed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			query := func(name string) func(string) ([]beadsquery.Bead, error) {
				return func(repoPath string) ([]beadsquery.Bead, error) {
					calls = append(calls, name+":"+repoPath)
					return []beadsquery.Bead{{ID: "bd-" + name}}, nil
				}
			}
			m := model.NewWithOptions(testRepos(), model.Options{
				StartupMode:         tt.mode,
				ListReadyBeads:      query("ready"),
				ListBlockedBeads:    query("blocked"),
				ListOpenBeads:       query("open"),
				ListInProgressBeads: query("in-progress"),
				ListClosedBeads:     query("closed"),
			})
			if m.Mode() != tt.mode {
				t.Fatalf("startup mode = %v, want %v", m.Mode(), tt.mode)
			}
			if !m.BeadsPending(tt.mode) {
				t.Fatalf("configured mode %v is not pending before its startup result", tt.mode)
			}
			view := m.View()
			if want := "loading " + tt.name + " beads"; !strings.Contains(view, want) {
				t.Fatalf("startup view does not show %q before the result:\n%s", want, view)
			}
			if strings.Contains(view, "beads not configured") {
				t.Fatalf("startup view reports Beads unavailable while the query is pending:\n%s", view)
			}

			cmd := m.Init()
			if cmd == nil {
				t.Fatal("Init returned nil command")
			}
			batch, ok := cmd().(tea.BatchMsg)
			if !ok || len(batch) < 1 {
				t.Fatalf("Init command = %T with %d entries, want fetch batch", batch, len(batch))
			}
			if len(calls) != 0 {
				t.Fatalf("Beads query ran before fetch command execution: %v", calls)
			}
			msg := batch[0]()
			wantCall := tt.name + ":/dev/alpha"
			if len(calls) != 1 || calls[0] != wantCall {
				t.Fatalf("startup query calls = %v, want [%s]", calls, wantCall)
			}
			if got := beadsResultMode(msg); got != tt.mode {
				t.Fatalf("startup fetch result mode = %v for %T, want %v", got, msg, tt.mode)
			}

			m, _ = update(m, msg)
			for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
				beads := m.Beads(mode)
				if mode == tt.mode {
					if len(beads) != 1 || beads[0].ID != "bd-"+tt.name || !m.BeadsAvailable(mode) || m.BeadsPending(mode) {
						t.Fatalf("configured mode %v state = %#v available=%v pending=%v", mode, beads, m.BeadsAvailable(mode), m.BeadsPending(mode))
					}
					continue
				}
				if len(beads) != 0 || m.BeadsAvailable(mode) {
					t.Fatalf("non-configured mode %v state = %#v available=%v, want untouched", mode, beads, m.BeadsAvailable(mode))
				}
			}
		})
	}
}

func TestBeadsStartupWithoutRepoDoesNotRemainPendingOrQuery(t *testing.T) {
	calls := 0
	m := model.NewWithOptions(nil, model.Options{
		StartupMode: ui.ModeBeadsReady,
		ListReadyBeads: func(string) ([]beadsquery.Bead, error) {
			calls++
			return nil, nil
		},
	})
	if m.Mode() != ui.ModeBeadsReady {
		t.Fatalf("startup mode = %v, want Ready", m.Mode())
	}
	if m.BeadsPending(ui.ModeBeadsReady) {
		t.Fatal("Ready is pending even though no repository is available to query")
	}
	if cmd := model.FetchForModeForTest(m); cmd != nil {
		t.Fatalf("Beads startup without a repository returned fetch command %T", cmd)
	}
	if calls != 0 {
		t.Fatalf("Ready query calls = %d, want 0 without a repository", calls)
	}
}

func TestBeadsStartupDefaultSeedsStickySubview(t *testing.T) {
	for _, mode := range []ui.Mode{
		ui.ModeBeadsReady,
		ui.ModeBeadsBlocked,
		ui.ModeBeadsOpen,
		ui.ModeBeadsInProgress,
		ui.ModeBeadsClosed,
	} {
		t.Run(model.ViewChoiceLabel(mode), func(t *testing.T) {
			opts := beadQueryOptions()
			opts.StartupMode = mode
			m := inRightPane(model.NewWithOptions(testRepos(), opts))
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			before := listRequests(m)

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})

			if m.Mode() != mode {
				t.Fatalf("Beads re-entry mode = %v, want configured %v", m.Mode(), mode)
			}
			if cmd == nil {
				t.Fatal("Beads re-entry returned nil fetch command")
			}
			assertOnlyListRequestChanged(t, before, m, mode)
		})
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

func TestBeadsSubviews_RestoreIndependentPaneStateAcrossSwitchPaths(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	openRows := []beadsquery.Bead{
		{ID: "open-0", Title: "Open zero"},
		{ID: "open-1", Title: "Open one"},
		{ID: "open-2", Title: "Open two"},
		{ID: "open-3", Title: "Open three"},
	}
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m = setBeadsQuery(t, m, "open")
	for range 3 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	readyRows := []beadsquery.Bead{
		{ID: "ready-0", Title: "Ready zero"},
		{ID: "ready-1", Title: "Ready one"},
		{ID: "ready-2", Title: "Ready two"},
		{ID: "ready-3", Title: "Ready three"},
	}
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, readyRows)
	m = setBeadsQuery(t, m, "ready")
	for range 2 {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	assertBeadsPaneState(t, m, ui.ModeBeadsOpen, "open", 4, 3, 2)
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	assertBeadsPaneState(t, m, ui.ModeBeadsOpen, "open", 4, 3, 2)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft}) // Blocked.
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft}) // Ready.
	assertBeadsPaneState(t, m, ui.ModeBeadsReady, "ready", 4, 2, 1)
}

func setBeadsQuery(t *testing.T, m model.Model, query string) model.Model {
	t.Helper()
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

func assertBeadsPaneState(t *testing.T, m model.Model, mode ui.Mode, query string, rows, selected, scroll int) {
	t.Helper()
	if m.Mode() != mode || m.ItemSearch() != query || len(m.Beads(mode)) != rows ||
		m.BeadsSelected(mode) != selected || m.BeadsScroll(mode) != scroll {
		t.Fatalf("Beads pane %v = mode %v query %q rows %d selected %d scroll %d, want query %q rows %d selected %d scroll %d",
			mode, m.Mode(), m.ItemSearch(), len(m.Beads(mode)), m.BeadsSelected(mode), m.BeadsScroll(mode),
			query, rows, selected, scroll)
	}
}

func TestBeadsSubviews_SameRepoRefetchRetainsThenRefiltersAndClamps(t *testing.T) {
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
			m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
			if tt.mode != ui.ModeBeadsOpen {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			}
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-old-0", Title: "Needle zero"},
				{ID: "bd-old-1", Title: "Needle one"},
				{ID: "bd-old-2", Title: "Needle two"},
				{ID: "bd-old-3", Title: "Needle three"},
			})
			m = setBeadsQuery(t, m, "needle")
			for range 3 {
				m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
			}

			m, cmd := update(m, tea.KeyMsg{Type: tea.KeyF5})
			if cmd == nil || !m.BeadsPending(tt.mode) || m.BeadsAvailable(tt.mode) {
				t.Fatalf("F5 state = cmd %T pending %v available %v", cmd, m.BeadsPending(tt.mode), m.BeadsAvailable(tt.mode))
			}
			assertBeadsPaneState(t, m, tt.mode, "needle", 4, 3, 2)
			if view := m.View(); !strings.Contains(view, "loading ") || strings.Contains(view, "bd-old-3") {
				t.Fatalf("pending view exposed retained rows instead of loading state:\n%s", view)
			}

			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{
				{ID: "bd-new-0", Title: "Needle new zero"},
				{ID: "bd-new-1", Title: "Needle new one"},
			})
			assertBeadsPaneState(t, m, tt.mode, "needle", 2, 1, 0)
			if !m.BeadsAvailable(tt.mode) || m.BeadsPending(tt.mode) {
				t.Fatalf("short replacement availability/pending = %v/%v, want true/false", m.BeadsAvailable(tt.mode), m.BeadsPending(tt.mode))
			}

			m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
			m = applyBeadsResult(t, m, tt.mode, true, []beadsquery.Bead{{ID: "bd-other", Title: "Other"}})
			assertBeadsPaneState(t, m, tt.mode, "needle", 0, 0, 0)
			if !m.BeadsAvailable(tt.mode) || m.BeadsPending(tt.mode) {
				t.Fatalf("zero-match replacement availability/pending = %v/%v, want true/false", m.BeadsAvailable(tt.mode), m.BeadsPending(tt.mode))
			}
		})
	}
}

func TestBeadsRepoChangeClearsEveryPaneButRetainsQueries(t *testing.T) {
	subviews := []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'o', mode: ui.ModeBeadsOpen, query: "open-query"},
		{key: 'r', mode: ui.ModeBeadsReady, query: "ready-query"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "blocked-query"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "progress-query"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "closed-query"},
	}
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 1})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	for _, subview := range subviews {
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview.key}})
		}
		m = applyBeadsResult(t, m, subview.mode, true, []beadsquery.Bead{
			{ID: subview.query + "-0", Title: subview.query},
			{ID: subview.query + "-1", Title: subview.query},
		})
		m = setBeadsQuery(t, m, subview.query)
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	before := listRequests(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil || m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("repo change = cmd %T mode %v, want Closed fetch", cmd, m.Mode())
	}
	for _, subview := range subviews {
		if m.ListRequest(subview.mode) == before[subview.mode] {
			t.Fatalf("repo change did not invalidate %v request", subview.mode)
		}
		if len(m.Beads(subview.mode)) != 0 || m.BeadsAvailable(subview.mode) ||
			m.BeadsSelected(subview.mode) != 0 || m.BeadsScroll(subview.mode) != 0 {
			t.Fatalf("repo change left state in %v: rows=%#v available=%v selected=%d scroll=%d",
				subview.mode, m.Beads(subview.mode), m.BeadsAvailable(subview.mode), m.BeadsSelected(subview.mode), m.BeadsScroll(subview.mode))
		}
		wantPending := subview.mode == ui.ModeBeadsClosed
		if m.BeadsPending(subview.mode) != wantPending {
			t.Fatalf("repo change pending(%v) = %v, want %v", subview.mode, m.BeadsPending(subview.mode), wantPending)
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	for i := len(subviews) - 1; i >= 0; i-- {
		subview := subviews[i]
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview.key}})
		}
		if got := m.ItemSearch(); got != subview.query {
			t.Fatalf("repo change query for %v = %q, want %q", subview.mode, got, subview.query)
		}
	}
}

func TestBeadsUnavailableResultClearsOnlyCurrentPaneAndRetainsQuery(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inRightPane(model.NewWithOptions(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 1})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	openRows := []beadsquery.Bead{{ID: "open-0", Title: "Open"}, {ID: "open-1", Title: "Open"}}
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, openRows)
	m = setBeadsQuery(t, m, "open")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, true, []beadsquery.Bead{{ID: "ready-0", Title: "Ready"}, {ID: "ready-1", Title: "Ready"}})
	m = setBeadsQuery(t, m, "ready")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	m = applyBeadsResult(t, m, ui.ModeBeadsReady, false, []beadsquery.Bead{{ID: "ignored"}})

	assertBeadsPaneState(t, m, ui.ModeBeadsReady, "ready", 0, 0, 0)
	if m.BeadsAvailable(ui.ModeBeadsReady) || m.BeadsPending(ui.ModeBeadsReady) {
		t.Fatalf("unavailable Ready state = available %v pending %v, want false/false", m.BeadsAvailable(ui.ModeBeadsReady), m.BeadsPending(ui.ModeBeadsReady))
	}
	if got := m.BeadsOpen(); len(got) != 2 || got[1].ID != "open-1" || !m.BeadsAvailable(ui.ModeBeadsOpen) || m.BeadsSelected(ui.ModeBeadsOpen) != 1 || m.BeadsScroll(ui.ModeBeadsOpen) != 1 {
		t.Fatalf("unavailable Ready result changed inactive Open pane: rows=%#v available=%v selected=%d scroll=%d",
			got, m.BeadsAvailable(ui.ModeBeadsOpen), m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen))
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if got := m.ItemSearch(); got != "open" {
		t.Fatalf("inactive Open query = %q after return, want open", got)
	}
}

// A pending query hides the retained rows, so the pane must report loading even
// when the retained rows no longer match the subview's filter. Otherwise a
// refetch answers "no results" about rows it is in the middle of replacing.
func TestBeadsPendingViewReportsLoadingUnderZeroMatchQuery(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inRightPane(model.NewWithOptions(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{{ID: "bd-0", Title: "Alpha"}})
	m = setBeadsQuery(t, m, "zzz")
	if view := m.View(); !strings.Contains(view, "No bead results for zzz") {
		t.Fatalf("settled zero-match view did not report the filter verdict:\n%s", view)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	if !m.BeadsPending(ui.ModeBeadsOpen) {
		t.Fatal("f5 did not leave Open pending")
	}
	view := m.View()
	if !strings.Contains(view, "loading open beads") || strings.Contains(view, "No bead results") {
		t.Fatalf("pending zero-match view did not report loading:\n%s", view)
	}
}

// Changing repositories while Active Flows overlays the content pane still
// switches which repository Beads describes, so no Beads pane may keep the old
// repository's rows or cursor once the surface is dismissed.
func TestBeadsRepoChangeUnderActiveFlowsClearsPaneState(t *testing.T) {
	subviews := []struct {
		key   rune
		mode  ui.Mode
		query string
	}{
		{key: 'o', mode: ui.ModeBeadsOpen, query: "open"},
		{key: 'r', mode: ui.ModeBeadsReady, query: "ready"},
		{key: 'b', mode: ui.ModeBeadsBlocked, query: "blocked"},
		{key: 'i', mode: ui.ModeBeadsInProgress, query: "progress"},
		{key: 'c', mode: ui.ModeBeadsClosed, query: "closed"},
	}
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	opts.ListFlows = func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return nil, nil }
	m := inRightPane(model.NewWithOptions(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	for _, subview := range subviews {
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview.key}})
		}
		m = applyBeadsResult(t, m, subview.mode, true, []beadsquery.Bead{
			{ID: subview.query + "-0", Title: subview.query},
			{ID: subview.query + "-1", Title: subview.query},
			{ID: subview.query + "-2", Title: subview.query},
			{ID: subview.query + "-3", Title: subview.query},
		})
		m = setBeadsQuery(t, m, subview.query)
		for range 3 {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
		}
	}

	before := listRequests(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	for _, subview := range subviews {
		if m.ListRequest(subview.mode) == before[subview.mode] {
			t.Fatalf("Active Flows repo change did not invalidate %v request", subview.mode)
		}
		if len(m.Beads(subview.mode)) != 0 || m.BeadsAvailable(subview.mode) || m.BeadsPending(subview.mode) ||
			m.BeadsSelected(subview.mode) != 0 || m.BeadsScroll(subview.mode) != 0 {
			t.Fatalf("Active Flows repo change left state in %v: rows=%#v available=%v pending=%v selected=%d scroll=%d",
				subview.mode, m.Beads(subview.mode), m.BeadsAvailable(subview.mode), m.BeadsPending(subview.mode),
				m.BeadsSelected(subview.mode), m.BeadsScroll(subview.mode))
		}
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	if m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("active flows toggle returned to mode %v, want remembered Beads Closed", m.Mode())
	}
	for i := len(subviews) - 1; i >= 0; i-- {
		subview := subviews[i]
		if m.Mode() != subview.mode {
			m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{subview.key}})
		}
		assertBeadsPaneState(t, m, subview.mode, subview.query, 0, 0, 0)
	}
}

// While a subview is pending its rows are hidden behind the loading message, so
// cursor keys must not silently move a selection the user cannot see.
func TestBeadsCursorIsInertWhilePending(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inRightPane(model.NewWithOptions(testRepos(), opts))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 2})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	wantSelected, wantScroll := m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen)

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyF5})
	for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyDown, tea.KeyUp} {
		m, _ = update(m, tea.KeyMsg{Type: key})
	}
	if got, gotScroll := m.BeadsSelected(ui.ModeBeadsOpen), m.BeadsScroll(ui.ModeBeadsOpen); got != wantSelected || gotScroll != wantScroll {
		t.Fatalf("cursor moved behind the loading placeholder: selected %d scroll %d, want %d/%d", got, gotScroll, wantSelected, wantScroll)
	}

	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-0"}, {ID: "bd-1"}, {ID: "bd-2"},
	})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.BeadsSelected(ui.ModeBeadsOpen); got != wantSelected+1 {
		t.Fatalf("cursor stayed inert after the result landed: selected %d, want %d", got, wantSelected+1)
	}
}

// Arrow switching is one of the ways a subview becomes the remembered one, so
// key 5 must re-enter a subview that was only ever reached by arrow.
func TestBeadsArrowSwitchIsRememberedForKeyFiveReentry(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRight}) // Open -> In-progress.
	if m.Mode() != ui.ModeBeadsInProgress {
		t.Fatalf("arrow mode = %v, want Beads In-progress", m.Mode())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	before := listRequests(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if m.Mode() != ui.ModeBeadsInProgress || cmd == nil {
		t.Fatalf("key 5 re-entry = mode %v cmd %T, want arrow-remembered In-progress fetch", m.Mode(), cmd)
	}
	assertOnlyListRequestChanged(t, before, m, ui.ModeBeadsInProgress)
}

// The remembered subview is view state, not repo-owned data, so a repo change
// must not send Beads back to Open.
func TestBeadsRememberedSubviewSurvivesRepoChange(t *testing.T) {
	opts := beadQueryOptions()
	opts.ScanRepos = func() ([]scanner.Repo, error) { return testRepos(), nil }
	m := inRightPane(model.NewWithOptions(testRepos(), opts))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	if m.Mode() != ui.ModeBeadsClosed {
		t.Fatalf("key 5 after repo change = %v, want remembered Beads Closed", m.Mode())
	}
}

// esc is the primary way a filter is undone, and it must restore the subview's
// full row set rather than only leaving search-edit mode.
func TestBeadsFilterClearsWithEsc(t *testing.T) {
	m := inRightPane(model.NewWithOptions(testRepos(), beadQueryOptions()))
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: ui.BeadsContentOverhead + 3})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = applyBeadsResult(t, m, ui.ModeBeadsOpen, true, []beadsquery.Bead{
		{ID: "bd-needle", Title: "Needle"},
		{ID: "bd-other", Title: "Other"},
	})
	m = setBeadsQuery(t, m, "needle")
	if got := m.Beads(ui.ModeBeadsOpen); len(got) != 1 || got[0].ID != "bd-needle" {
		t.Fatalf("filtered Open rows = %#v, want the needle row", got)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.SearchActive() || m.ItemSearch() != "" {
		t.Fatalf("esc left filter state: active %v query %q", m.SearchActive(), m.ItemSearch())
	}
	if got := m.Beads(ui.ModeBeadsOpen); len(got) != 2 {
		t.Fatalf("esc did not restore the unfiltered Open rows: %#v", got)
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
