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
	wantArgs := []string{"list", "-s", "open", "--json"}
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
