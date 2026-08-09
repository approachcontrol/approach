package beadsquery_test

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
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

func TestQuerierListOpenClassifiesOnlyKnownAbsenceFailures(t *testing.T) {
	t.Parallel()

	missingExecutable := &exec.Error{Name: "bd", Err: exec.ErrNotFound}
	missingProject := errors.New("Error: cannot use -C directory /repo: no beads project found: exit status 1")
	missingDatabase := errors.New("Error: no beads database found.\nHint: run 'bd init' to create a database in the current directory")
	normalizedMissingDatabase := errors.New("NO   BEADS\tDATABASE FOUND")
	genericFailure := errors.New("bd failed: permission denied")
	outputLimit := errors.New("bd output exceeded 33554432-byte limit")
	tests := []struct {
		name          string
		out           string
		err           error
		notConfigured bool
		wantDetail    string
	}{
		{name: "missing executable", err: missingExecutable, notConfigured: true},
		{name: "missing project wrapped by runner", err: missingProject, notConfigured: true},
		{name: "missing database with hint", err: missingDatabase, notConfigured: true},
		{name: "missing database normalized", err: normalizedMissingDatabase, notConfigured: true},
		{name: "generic nonzero", err: genericFailure, wantDetail: "permission denied"},
		{name: "output limit", err: outputLimit, wantDetail: "output exceeded"},
		{name: "garbage output", out: "not-json", wantDetail: "parsing open beads"},
		{name: "lexical extension", err: errors.New("no beads project foundish"), wantDetail: "foundish"},
		{name: "incidental mention", err: errors.New("help says no beads database found means setup is incomplete"), wantDetail: "help says"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{out: tt.out, err: tt.err}
			got, err := beadsquery.NewQuerier(runner).ListOpen("/repo")
			if errors.Is(err, beadsquery.ErrNotConfigured) != tt.notConfigured {
				t.Fatalf("errors.Is(ListOpen() error, ErrNotConfigured) = %v, want %v: %v", errors.Is(err, beadsquery.ErrNotConfigured), tt.notConfigured, err)
			}
			if got != nil {
				t.Fatalf("ListOpen() = %#v, want no partial data", got)
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatalf("ListOpen() error = %v, want original cause %v", err, tt.err)
			}
			if tt.wantDetail != "" && !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("ListOpen() error = %q, want detail %q", err, tt.wantDetail)
			}
			if tt.err == missingExecutable {
				if !errors.Is(err, exec.ErrNotFound) {
					t.Fatalf("ListOpen() error = %v, want exec.ErrNotFound", err)
				}
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

func TestQuerierListClosedUsesBoundedNewestFirstReadonlyQuery(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `[{"id":"bd-1","priority":1,"title":"Closed","closed_at":"2026-08-08T20:00:00Z"}]`}
	_, err := beadsquery.NewQuerier(runner).ListClosed("/repo")
	if err != nil {
		t.Fatalf("ListClosed() error = %v", err)
	}
	wantArgs := []string{"list", "-s", "closed", "--json", "--limit", "100", "--sort", "closed", "--reverse", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
}

func TestQuerierCountClosedUsesReadonlyNoActivityStats(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{out: `{"summary":{"closed_issues":1432}}`}
	got, err := beadsquery.NewQuerier(runner).CountClosed("/repo")
	if err != nil {
		t.Fatalf("CountClosed() error = %v", err)
	}
	wantArgs := []string{"stats", "--json", "--no-activity", "--readonly"}
	if runner.calls != 1 || runner.dir != "/repo" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner call = (%d, %q, %#v), want (1, %q, %#v)", runner.calls, runner.dir, runner.args, "/repo", wantArgs)
	}
	if got != 1432 {
		t.Fatalf("CountClosed() = %d, want 1432", got)
	}
}

func TestQuerierCountClosedPreservesRunnerFailureCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("bd stats failed")
	runner := &fakeRunner{out: `{"summary":{"closed_issues":1432}}`, err: cause}
	got, err := beadsquery.NewQuerier(runner).CountClosed("/repo")
	if !errors.Is(err, cause) {
		t.Fatalf("CountClosed() error = %v, want wrapped cause %v", err, cause)
	}
	if got != 0 {
		t.Fatalf("CountClosed() = %d, want no guessed count", got)
	}
}

func TestQuerierCountClosedUsesSharedAbsenceClassification(t *testing.T) {
	t.Parallel()

	cause := errors.New("Error: no beads database found.\nHint: run 'bd init'")
	got, err := beadsquery.NewQuerier(&fakeRunner{err: cause}).CountClosed("/repo")
	if !errors.Is(err, beadsquery.ErrNotConfigured) {
		t.Fatalf("CountClosed() error = %v, want ErrNotConfigured", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("CountClosed() error = %v, want wrapped cause %v", err, cause)
	}
	if got != 0 {
		t.Fatalf("CountClosed() = %d, want no guessed count", got)
	}
}

func TestEveryQueryUsesSharedAbsenceClassification(t *testing.T) {
	t.Parallel()

	queries := []struct {
		name string
		run  func(*beadsquery.Querier) ([]beadsquery.Bead, error)
	}{
		{name: "ready", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListReady("/repo") }},
		{name: "blocked", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListBlocked("/repo") }},
		{name: "open", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListOpen("/repo") }},
		{name: "in-progress", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListInProgress("/repo") }},
		{name: "closed", run: func(q *beadsquery.Querier) ([]beadsquery.Bead, error) { return q.ListClosed("/repo") }},
	}
	for _, query := range queries {
		t.Run(query.name, func(t *testing.T) {
			for _, failure := range []struct {
				name          string
				cause         error
				notConfigured bool
			}{
				{name: "absence", cause: errors.New("Error: no beads database found.\nHint: run 'bd init'"), notConfigured: true},
				{name: "configured error", cause: errors.New("bd failed")},
			} {
				t.Run(failure.name, func(t *testing.T) {
					got, err := query.run(beadsquery.NewQuerier(&fakeRunner{err: failure.cause}))
					if errors.Is(err, beadsquery.ErrNotConfigured) != failure.notConfigured {
						t.Fatalf("query error classification = %v, want %v: %v", errors.Is(err, beadsquery.ErrNotConfigured), failure.notConfigured, err)
					}
					if !errors.Is(err, failure.cause) {
						t.Fatalf("query error = %v, want wrapped cause %v", err, failure.cause)
					}
					if got != nil {
						t.Fatalf("query result = %#v, want no partial data", got)
					}
				})
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
