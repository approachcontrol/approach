package actions_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/approachcontrol/approach/actions"
)

type fakeContextCommandRunner struct {
	stdout []byte
	stderr []byte
	err    error
	name   string
	args   []string
}

func (r *fakeContextCommandRunner) RunContext(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	return r.stdout, r.stderr, r.err
}

func TestLookupGitHubPRStatusWithRunner(t *testing.T) {
	runner := &fakeContextCommandRunner{stdout: []byte(`{
		"mergeable":"MERGEABLE",
		"statusCheckRollup":[
			{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},
			{"__typename":"StatusContext","state":"SUCCESS"}
		]
	}`)}
	got, err := actions.LookupGitHubPRStatusWithRunner(context.Background(), 42, "https://github.com/acme/project/pull/42", runner)
	if err != nil {
		t.Fatalf("LookupGitHubPRStatusWithRunner() error = %v", err)
	}
	want := actions.PullRequestStatus{Mergeability: actions.PRMergeable, Checks: actions.PRChecksPassing}
	if got != want {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
	if runner.name != "gh" || !reflect.DeepEqual(runner.args, []string{"pr", "view", "42", "--repo", "acme/project", "--json", "mergeable,statusCheckRollup"}) {
		t.Fatalf("command = %q %v", runner.name, runner.args)
	}
}

func TestLookupGitHubPRStatusClassifiesMergeabilityIndependently(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{value: "MERGEABLE", want: actions.PRMergeable},
		{value: "CONFLICTING", want: actions.PRConflicting},
		{value: "UNKNOWN", want: actions.PRStatusUnknown},
		{value: "FUTURE_VALUE", want: actions.PRStatusUnknown},
		{value: "", want: actions.PRStatusUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			runner := &fakeContextCommandRunner{stdout: []byte(`{"mergeable":"` + tc.value + `","statusCheckRollup":[{"__typename":"StatusContext","state":"SUCCESS"}]}`)}
			got, err := actions.LookupGitHubPRStatusWithRunner(context.Background(), 42, "https://github.com/acme/project/pull/42", runner)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mergeability != tc.want || got.Checks != actions.PRChecksPassing {
				t.Fatalf("status = %#v, want mergeability %q and passing checks", got, tc.want)
			}
		})
	}
}

func TestLookupGitHubPRStatusClassifiesCheckRollup(t *testing.T) {
	tests := []struct {
		name   string
		member string
		want   string
	}{
		{name: "created", member: `{"__typename":"CheckRun","status":"CREATED"}`, want: actions.PRChecksPending},
		{name: "queued", member: `{"__typename":"CheckRun","status":"QUEUED"}`, want: actions.PRChecksPending},
		{name: "in progress", member: `{"__typename":"CheckRun","status":"IN_PROGRESS"}`, want: actions.PRChecksPending},
		{name: "pending check", member: `{"__typename":"CheckRun","status":"PENDING"}`, want: actions.PRChecksPending},
		{name: "requested", member: `{"__typename":"CheckRun","status":"REQUESTED"}`, want: actions.PRChecksPending},
		{name: "waiting", member: `{"__typename":"CheckRun","status":"WAITING"}`, want: actions.PRChecksPending},
		{name: "action required", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"ACTION_REQUIRED"}`, want: actions.PRChecksFailing},
		{name: "cancelled", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"CANCELLED"}`, want: actions.PRChecksFailing},
		{name: "failure", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}`, want: actions.PRChecksFailing},
		{name: "stale", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"STALE"}`, want: actions.PRChecksFailing},
		{name: "startup failure", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"STARTUP_FAILURE"}`, want: actions.PRChecksFailing},
		{name: "timed out", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"TIMED_OUT"}`, want: actions.PRChecksFailing},
		{name: "success", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}`, want: actions.PRChecksPassing},
		{name: "neutral", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"NEUTRAL"}`, want: actions.PRChecksPassing},
		{name: "skipped", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SKIPPED"}`, want: actions.PRChecksPassing},
		{name: "context error", member: `{"__typename":"StatusContext","state":"ERROR"}`, want: actions.PRChecksFailing},
		{name: "context failure", member: `{"__typename":"StatusContext","state":"FAILURE"}`, want: actions.PRChecksFailing},
		{name: "context expected", member: `{"__typename":"StatusContext","state":"EXPECTED"}`, want: actions.PRChecksPending},
		{name: "context pending", member: `{"__typename":"StatusContext","state":"PENDING"}`, want: actions.PRChecksPending},
		{name: "context success", member: `{"__typename":"StatusContext","state":"SUCCESS"}`, want: actions.PRChecksPassing},
		{name: "incomplete completed", member: `{"__typename":"CheckRun","status":"COMPLETED"}`, want: actions.PRStatusUnknown},
		{name: "unknown check status", member: `{"__typename":"CheckRun","status":"FUTURE"}`, want: actions.PRStatusUnknown},
		{name: "unknown conclusion", member: `{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FUTURE"}`, want: actions.PRStatusUnknown},
		{name: "unknown context", member: `{"__typename":"StatusContext","state":"FUTURE"}`, want: actions.PRStatusUnknown},
		{name: "malformed member", member: `7`, want: actions.PRStatusUnknown},
		{name: "unknown member", member: `{"__typename":"FutureType"}`, want: actions.PRStatusUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeContextCommandRunner{stdout: []byte(`{"mergeable":"MERGEABLE","statusCheckRollup":[` + tc.member + `]}`)}
			got, err := actions.LookupGitHubPRStatusWithRunner(context.Background(), 42, "https://github.com/acme/project/pull/42", runner)
			if err != nil {
				t.Fatal(err)
			}
			if got.Checks != tc.want {
				t.Fatalf("checks = %q, want %q", got.Checks, tc.want)
			}
		})
	}
}

func TestLookupGitHubPRStatusAggregatesChecksByPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		rollup string
		want   string
	}{
		{name: "empty", rollup: `[]`, want: actions.PRStatusUnknown},
		{name: "nil", rollup: `null`, want: actions.PRStatusUnknown},
		{name: "passing and unknown", rollup: `[{"__typename":"StatusContext","state":"SUCCESS"},{"__typename":"FutureType"}]`, want: actions.PRStatusUnknown},
		{name: "pending wins unknown", rollup: `[{"__typename":"FutureType"},{"__typename":"StatusContext","state":"PENDING"}]`, want: actions.PRChecksPending},
		{name: "failing wins pending", rollup: `[{"__typename":"StatusContext","state":"PENDING"},{"__typename":"StatusContext","state":"FAILURE"}]`, want: actions.PRChecksFailing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeContextCommandRunner{stdout: []byte(`{"mergeable":"MERGEABLE","statusCheckRollup":` + tc.rollup + `}`)}
			got, err := actions.LookupGitHubPRStatusWithRunner(context.Background(), 42, "https://github.com/acme/project/pull/42", runner)
			if err != nil {
				t.Fatal(err)
			}
			if got.Checks != tc.want {
				t.Fatalf("checks = %q, want %q", got.Checks, tc.want)
			}
		})
	}
}

func TestLookupGitHubPRStatusFailuresReturnUnknownColumns(t *testing.T) {
	tests := []struct {
		name   string
		number int
		url    string
		runner *fakeContextCommandRunner
	}{
		{name: "bad number", number: 0, url: "https://github.com/acme/project/pull/42", runner: &fakeContextCommandRunner{}},
		{name: "bad URL", number: 42, url: "https://example.com/acme/project/pull/42", runner: &fakeContextCommandRunner{}},
		{name: "mismatched number", number: 42, url: "https://github.com/acme/project/pull/41", runner: &fakeContextCommandRunner{}},
		{name: "missing command", number: 42, url: "https://github.com/acme/project/pull/42", runner: &fakeContextCommandRunner{err: exec.ErrNotFound}},
		{name: "nonzero", number: 42, url: "https://github.com/acme/project/pull/42", runner: &fakeContextCommandRunner{stderr: []byte("boom"), err: errors.New("exit 1")}},
		{name: "malformed top JSON", number: 42, url: "https://github.com/acme/project/pull/42", runner: &fakeContextCommandRunner{stdout: []byte(`{`)}},
	}
	want := actions.PullRequestStatus{Mergeability: actions.PRStatusUnknown, Checks: actions.PRStatusUnknown}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := actions.LookupGitHubPRStatusWithRunner(context.Background(), tc.number, tc.url, tc.runner)
			if err == nil {
				t.Fatal("error = nil")
			}
			if got != want {
				t.Fatalf("status = %#v, want %#v", got, want)
			}
		})
	}
}
