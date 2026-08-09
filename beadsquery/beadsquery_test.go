package beadsquery_test

import (
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/approachcontrol/approach/beadsquery"
)

type fakeRunner struct {
	out   string
	err   error
	dir   string
	args  []string
	calls int
}

func TestQuerierListOpenPreservesRunnerFailureCauses(t *testing.T) {
	t.Parallel()

	missingDatabase := errors.New("no beads database found")
	genericFailure := errors.New("bd failed")
	tests := []struct {
		name string
		err  error
		is   error
	}{
		{name: "missing executable", err: &exec.Error{Name: "bd", Err: exec.ErrNotFound}, is: exec.ErrNotFound},
		{name: "missing database", err: missingDatabase, is: missingDatabase},
		{name: "generic nonzero", err: genericFailure, is: genericFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{err: tt.err}
			got, err := beadsquery.NewQuerier(runner).ListOpen("/repo")
			if !errors.Is(err, tt.is) {
				t.Fatalf("ListOpen() error = %v, want cause %v", err, tt.is)
			}
			if got != nil {
				t.Fatalf("ListOpen() = %#v, want no partial data", got)
			}
			if tt.name == "missing executable" {
				var execErr *exec.Error
				if !errors.As(err, &execErr) {
					t.Fatalf("ListOpen() error = %v, want *exec.Error", err)
				}
			}
		})
	}
}

func (r *fakeRunner) Run(dir string, args ...string) (string, error) {
	r.calls++
	r.dir = dir
	r.args = append([]string(nil), args...)
	return r.out, r.err
}

func TestQuerierShowUsesReadonlyRunnerAndPreservesOutput(t *testing.T) {
	t.Parallel()

	const want = "\x1b[1mbead-42\x1b[0m\n\n  body with trailing space \n\n"
	runner := &fakeRunner{out: want}

	got, err := beadsquery.NewQuerier(runner).Show("/selected/repo", "bead-42")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if runner.calls != 1 || runner.dir != "/selected/repo" {
		t.Fatalf("runner call = (%d, %q), want (1, %q)", runner.calls, runner.dir, "/selected/repo")
	}
	wantArgs := []string{"show", "bead-42", "--readonly"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
	if got != want {
		t.Fatalf("Show() = %q, want byte-for-byte output %q", got, want)
	}
}

func TestQuerierShowPreservesRunnerFailureCauses(t *testing.T) {
	t.Parallel()

	cause := errors.New("bd show failed")
	got, err := beadsquery.NewQuerier(&fakeRunner{out: "partial", err: cause}).Show("/repo", "bead-42")
	if !errors.Is(err, cause) {
		t.Fatalf("Show() error = %v, want wrapped cause %v", err, cause)
	}
	if got != "" {
		t.Fatalf("Show() = %q, want no partial output", got)
	}
}

func TestShowUsesDefaultRunner(t *testing.T) {
	runner := &fakeRunner{out: "raw detail\n"}
	previous := beadsquery.DefaultRunner
	beadsquery.DefaultRunner = runner
	t.Cleanup(func() { beadsquery.DefaultRunner = previous })

	got, err := beadsquery.Show("/repo", "bead-default")
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	wantArgs := []string{"show", "bead-default", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
	if got != "raw detail\n" {
		t.Fatalf("Show() = %q, want raw output", got)
	}
}

func TestQuerierListOpenUsesRunnerAndReturnsSortedBeads(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[
		{"id":"bd-2","priority":2,"title":"Second"},
		{"id":"bd-1","priority":1,"title":"First","assignee":"alice"}
	]`}

	got, err := beadsquery.NewQuerier(runner).ListOpen("/repo")
	if err != nil {
		t.Fatalf("ListOpen() error = %v", err)
	}
	if runner.calls != 1 || runner.dir != "/repo" {
		t.Fatalf("runner call = (%d, %q), want (1, %q)", runner.calls, runner.dir, "/repo")
	}
	wantArgs := []string{"list", "-s", "open", "--json", "--limit", "0", "--readonly"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, wantArgs)
	}
	want := []beadsquery.Bead{
		{ID: "bd-1", Priority: 1, Title: "First", Assignee: "alice"},
		{ID: "bd-2", Priority: 2, Title: "Second"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListOpen() = %#v, want %#v", got, want)
	}
}

func TestQuerierListReadyUsesDependencyGraphQuery(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-1","priority":1,"title":"Ready"}]`}
	got, err := beadsquery.NewQuerier(runner).ListReady("/repo")
	if err != nil {
		t.Fatalf("ListReady() error = %v", err)
	}
	wantArgs := []string{"ready", "--json", "--limit", "0", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
	if len(got) != 1 || got[0].ID != "bd-1" {
		t.Fatalf("ListReady() = %#v, want ready bead", got)
	}
}

func TestQuerierListBlockedUsesUncappedReadonlyStatusQuery(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-1","priority":1,"title":"Blocked"}]`}
	_, err := beadsquery.NewQuerier(runner).ListBlocked("/repo")
	if err != nil {
		t.Fatalf("ListBlocked() error = %v", err)
	}
	wantArgs := []string{"list", "-s", "blocked", "--json", "--limit", "0", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
}

func TestQuerierListInProgressUsesUncappedReadonlyStatusQuery(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-1","priority":1,"title":"In progress"}]`}
	_, err := beadsquery.NewQuerier(runner).ListInProgress("/repo")
	if err != nil {
		t.Fatalf("ListInProgress() error = %v", err)
	}
	wantArgs := []string{"list", "-s", "in_progress", "--json", "--limit", "0", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
}

func TestQuerierListClosedUsesUncappedReadonlyCloseTimeQuery(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-1","priority":1,"title":"Closed","closed_at":"2026-08-08T20:00:00Z"}]`}
	_, err := beadsquery.NewQuerier(runner).ListClosed("/repo")
	if err != nil {
		t.Fatalf("ListClosed() error = %v", err)
	}
	wantArgs := []string{"list", "-s", "closed", "--json", "--limit", "0", "--sort", "closed", "--reverse", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
}

func TestNewQueriesPreserveRunnerFailureCauses(t *testing.T) {
	t.Parallel()

	cause := errors.New("bd failed")
	queries := []struct {
		name string
		run  func(*beadsquery.Querier) ([]beadsquery.Bead, error)
	}{
		{name: "ready", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListReady("/repo") }},
		{name: "blocked", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListBlocked("/repo") }},
		{name: "in-progress", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListInProgress("/repo") }},
		{name: "closed", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListClosed("/repo") }},
	}
	for _, query := range queries {
		query := query
		t.Run(query.name, func(t *testing.T) {
			runner := &fakeRunner{err: cause}
			got, err := query.run(beadsquery.NewQuerier(runner))
			if !errors.Is(err, cause) {
				t.Fatalf("query error = %v, want wrapped cause %v", err, cause)
			}
			if got != nil {
				t.Fatalf("query result = %#v, want no partial data", got)
			}
		})
	}
}

func TestReadyAndOpenReturnTheSameBeadIndependently(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-shared","priority":1,"title":"Shared"}]`}
	querier := beadsquery.NewQuerier(runner)
	ready, err := querier.ListReady("/repo")
	if err != nil {
		t.Fatalf("ListReady() error = %v", err)
	}
	open, err := querier.ListOpen("/repo")
	if err != nil {
		t.Fatalf("ListOpen() error = %v", err)
	}
	if len(ready) != 1 || len(open) != 1 || ready[0].ID != "bd-shared" || open[0].ID != "bd-shared" {
		t.Fatalf("ready/open = %#v / %#v, want independent shared bead", ready, open)
	}
	ready[0].Title = "changed ready copy"
	if open[0].Title != "Shared" {
		t.Fatalf("mutating Ready result changed Open result: %#v", open)
	}
}
