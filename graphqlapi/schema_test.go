package graphqlapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/graphql-go/graphql"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
)

func executeQuery(t *testing.T, snap *snapshot, query string) *graphql.Result {
	t.Helper()
	schema, err := newSchema()
	if err != nil {
		t.Fatalf("newSchema() error = %v", err)
	}
	return graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: query,
		Context:       withSnapshot(context.Background(), snap),
	})
}

func mustSucceed(t *testing.T, result *graphql.Result) map[string]any {
	t.Helper()
	if len(result.Errors) > 0 {
		t.Fatalf("query errors = %v", result.Errors)
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("result data = %#v, want an object", result.Data)
	}
	return data
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(encoded)
}

func fixtureTime(offset time.Duration) time.Time {
	return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC).Add(offset)
}

func fixtureSnapshot() *snapshot {
	created := fixtureTime(0)
	updated := fixtureTime(time.Hour)
	return buildSnapshot(
		staticRepos(
			scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"},
			scanner.Repo{Path: "/repos/beta", DisplayName: "beta", IsBare: true},
		),
		staticFlows(
			flowstore.FlowRecord{
				FlowID:       "flow-alpha-1",
				Title:        "Ship the API",
				Instructions: "Build it",
				Status:       flowstore.StatusInProgress,
				RepoPath:     "/repos/alpha",
				WorktreePath: "/repos/alpha-worktrees/flow",
				Branch:       "flow/api",
				BaseRef:      "main",
				Commit:       "abc123",
				PresetName:   "default",
				PlanID:       "plan-1",
				AutoMode:     true,
				Issue:        flowstore.Issue{Provider: "github", Number: 12, URL: "https://example.test/issues/12"},
				PR:           flowstore.PullRequest{Provider: "github", Number: 34, URL: "https://example.test/pull/34", HeadBranch: "flow/api", BaseBranch: "main", Status: "open"},
				Merge:        flowstore.Merge{Status: flowstore.MergePending},
				Phases: []flowstore.FlowPhase{
					{
						PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan,
						Status: flowstore.PhaseCompleted, Order: 1,
						DependsOn: []string{}, Summary: "Planned",
						CreatedAt: created, UpdatedAt: updated,
					},
					{
						PhaseID: "implementation", Title: "Implementation", Kind: flowstore.KindImplementation,
						Status: flowstore.PhaseRunning, Order: 2,
						DependsOn: []string{"plan"}, Notes: "In flight",
						CreatedAt: created, UpdatedAt: updated,
					},
					{
						PhaseID: "impl-child", ParentPhaseID: "implementation", Title: "Child",
						Kind: flowstore.KindImplementationChild, Status: flowstore.PhasePending, Order: 1,
						CreatedAt: created, UpdatedAt: updated,
					},
				},
				CreatedAt: created,
				UpdatedAt: updated,
			},
			flowstore.FlowRecord{
				FlowID:    "flow-outside",
				Title:     "Outside the scan root",
				Status:    flowstore.StatusPending,
				RepoPath:  "/elsewhere/outsider",
				Merge:     flowstore.Merge{Status: flowstore.MergePending},
				Phases:    []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted, Order: 1}},
				CreatedAt: created,
				UpdatedAt: created,
			},
		),
		nil,
	)
}

func TestSchemaReposQuery(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{ repos { id path displayName isBare inScanRoot } }`)
	data := mustSucceed(t, result)
	want := `[{"displayName":"alpha","id":"/repos/alpha","inScanRoot":true,"isBare":false,"path":"/repos/alpha"},` +
		`{"displayName":"beta","id":"/repos/beta","inScanRoot":true,"isBare":true,"path":"/repos/beta"},` +
		`{"displayName":"outsider","id":"/elsewhere/outsider","inScanRoot":false,"isBare":false,"path":"/elsewhere/outsider"}]`
	if got := jsonOf(t, data["repos"]); got != want {
		t.Fatalf("repos = %s, want %s", got, want)
	}
}

func TestSchemaRepoByID(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{ repo(id: "/repos/alpha/") { id displayName } }`)
	data := mustSucceed(t, result)
	if got, want := jsonOf(t, data["repo"]), `{"displayName":"alpha","id":"/repos/alpha"}`; got != want {
		t.Fatalf("repo = %s, want %s", got, want)
	}
}

func TestSchemaUnknownIDsResolveToNull(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{ repo(id: "/nope") { id } flow(id: "nope") { id } }`)
	data := mustSucceed(t, result)
	if data["repo"] != nil {
		t.Errorf("repo = %#v, want null", data["repo"])
	}
	if data["flow"] != nil {
		t.Errorf("flow = %#v, want null", data["flow"])
	}
}

func TestSchemaFlowsAndFlowByID(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{
		all: flows { id }
		alpha: flows(repoId: "/repos/alpha") { id }
		none: flows(repoId: "/repos/beta") { id }
		one: flow(id: "flow-alpha-1") { id title status repoPath }
	}`)
	data := mustSucceed(t, result)
	if got, want := jsonOf(t, data["all"]), `[{"id":"flow-alpha-1"},{"id":"flow-outside"}]`; got != want {
		t.Errorf("flows = %s, want %s", got, want)
	}
	if got, want := jsonOf(t, data["alpha"]), `[{"id":"flow-alpha-1"}]`; got != want {
		t.Errorf("flows(repoId: alpha) = %s, want %s", got, want)
	}
	if got, want := jsonOf(t, data["none"]), `[]`; got != want {
		t.Errorf("flows(repoId: beta) = %s, want %s", got, want)
	}
	want := `{"id":"flow-alpha-1","repoPath":"/repos/alpha","status":"in_progress","title":"Ship the API"}`
	if got := jsonOf(t, data["one"]); got != want {
		t.Errorf("flow = %s, want %s", got, want)
	}
}

func TestSchemaTraversalBothDirections(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{
		repos { id flows { id repo { id } } }
		flow(id: "flow-outside") { repo { id displayName inScanRoot flows { id } } }
	}`)
	data := mustSucceed(t, result)
	want := `[{"flows":[{"id":"flow-alpha-1","repo":{"id":"/repos/alpha"}}],"id":"/repos/alpha"},` +
		`{"flows":[],"id":"/repos/beta"},` +
		`{"flows":[{"id":"flow-outside","repo":{"id":"/elsewhere/outsider"}}],"id":"/elsewhere/outsider"}]`
	if got := jsonOf(t, data["repos"]); got != want {
		t.Errorf("repos = %s, want %s", got, want)
	}
	wantFlow := `{"repo":{"displayName":"outsider","flows":[{"id":"flow-outside"}],"id":"/elsewhere/outsider","inScanRoot":false}}`
	if got := jsonOf(t, data["flow"]); got != wantFlow {
		t.Errorf("flow = %s, want %s", got, wantFlow)
	}
}

func TestSchemaPhaseFields(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{
		flow(id: "flow-alpha-1") {
			phases { id parentPhaseId title kind status order dependsOn outcome notes summary createdAt updatedAt }
		}
	}`)
	data := mustSucceed(t, result)
	want := `{"phases":[` +
		`{"createdAt":"2026-08-11T12:00:00Z","dependsOn":[],"id":"plan","kind":"plan","notes":null,"order":1,"outcome":null,"parentPhaseId":null,"status":"completed","summary":"Planned","title":"Plan","updatedAt":"2026-08-11T13:00:00Z"},` +
		`{"createdAt":"2026-08-11T12:00:00Z","dependsOn":["plan"],"id":"implementation","kind":"implementation","notes":"In flight","order":2,"outcome":null,"parentPhaseId":null,"status":"running","summary":null,"title":"Implementation","updatedAt":"2026-08-11T13:00:00Z"},` +
		`{"createdAt":"2026-08-11T12:00:00Z","dependsOn":[],"id":"impl-child","kind":"implementation_child","notes":null,"order":1,"outcome":null,"parentPhaseId":"implementation","status":"pending","summary":null,"title":"Child","updatedAt":"2026-08-11T13:00:00Z"}]}`
	if got := jsonOf(t, data["flow"]); got != want {
		t.Fatalf("flow = %s\nwant %s", got, want)
	}
}

func TestSchemaCurrentPhase(t *testing.T) {
	base := fixtureTime(0)
	cases := []struct {
		name    string
		phases  []flowstore.FlowPhase
		want    string
		wantNil bool
	}{
		{
			name: "running",
			phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: base, UpdatedAt: base},
				{PhaseID: "implementation", Status: flowstore.PhaseRunning, Order: 2, CreatedAt: base, UpdatedAt: base},
			},
			want: "implementation",
		},
		{
			name: "blocked counts as current",
			phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: base, UpdatedAt: base},
				{PhaseID: "implementation", Status: flowstore.PhaseBlocked, Order: 2, CreatedAt: base, UpdatedAt: base},
				{PhaseID: "merge", Status: flowstore.PhaseReady, Order: 3, CreatedAt: base, UpdatedAt: base},
			},
			want: "implementation",
		},
		{
			name: "needs attention counts as current",
			phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Order: 1, CreatedAt: base, UpdatedAt: base},
			},
			want: "plan",
		},
		{
			name: "none actionable",
			phases: []flowstore.FlowPhase{
				{PhaseID: "plan", Status: flowstore.PhaseCompleted, Order: 1, CreatedAt: base, UpdatedAt: base},
				{PhaseID: "merge", Status: flowstore.PhasePending, Order: 2, CreatedAt: base, UpdatedAt: base},
			},
			wantNil: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			snap := buildSnapshot(staticRepos(), staticFlows(flowstore.FlowRecord{
				FlowID:   "f1",
				RepoPath: "/repos/alpha",
				Merge:    flowstore.Merge{Status: flowstore.MergePending},
				Phases:   testCase.phases,
			}), nil)
			result := executeQuery(t, snap, `{ flow(id: "f1") { currentPhase { id status } } }`)
			data := mustSucceed(t, result)
			flow := data["flow"].(map[string]any)
			if testCase.wantNil {
				if flow["currentPhase"] != nil {
					t.Fatalf("currentPhase = %#v, want null", flow["currentPhase"])
				}
				return
			}
			phase, ok := flow["currentPhase"].(map[string]any)
			if !ok {
				t.Fatalf("currentPhase = %#v, want an object", flow["currentPhase"])
			}
			if phase["id"] != testCase.want {
				t.Fatalf("currentPhase.id = %v, want %q", phase["id"], testCase.want)
			}
		})
	}
}

func TestSchemaNullabilityMatrix(t *testing.T) {
	mergedAt := fixtureTime(2 * time.Hour)
	snap := buildSnapshot(staticRepos(), staticFlows(
		flowstore.FlowRecord{
			FlowID:   "empty",
			RepoPath: "/repos/alpha",
			Merge:    flowstore.Merge{Status: flowstore.MergePending},
			Phases:   []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseReady}},
		},
		flowstore.FlowRecord{
			FlowID:   "populated",
			RepoPath: "/repos/alpha",
			Issue:    flowstore.Issue{Provider: "github", Number: 7, URL: "https://example.test/i/7"},
			PR:       flowstore.PullRequest{Provider: "github", Number: 9, URL: "https://example.test/p/9", HeadBranch: "head", BaseBranch: "base", Status: "merged"},
			Merge:    flowstore.Merge{Status: flowstore.MergeMerged, Commit: "deadbeef", MergedAt: &mergedAt},
			Phases:   []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseCompleted}},
		},
		flowstore.FlowRecord{
			FlowID:   "merge-commit-only",
			RepoPath: "/repos/alpha",
			Merge:    flowstore.Merge{Status: flowstore.MergePending, Commit: "cafe"},
			Phases:   []flowstore.FlowPhase{{PhaseID: "plan", Status: flowstore.PhaseCompleted}},
		},
	), nil)

	result := executeQuery(t, snap, `{
		empty: flow(id: "empty") {
			instructions worktreePath branch baseRef commit presetName planId
			issue { provider } pullRequest { provider } merge { status }
			phases { dependsOn outcome }
		}
		populated: flow(id: "populated") {
			issue { provider number url }
			pullRequest { provider number url headBranch baseBranch status }
			merge { status commit mergedAt }
		}
		mergeCommitOnly: flow(id: "merge-commit-only") { merge { status commit mergedAt } }
	}`)
	data := mustSucceed(t, result)

	wantEmpty := `{"baseRef":null,"branch":null,"commit":null,"instructions":null,"issue":null,"merge":null,` +
		`"phases":[{"dependsOn":[],"outcome":null}],"planId":null,"presetName":null,"pullRequest":null,"worktreePath":null}`
	if got := jsonOf(t, data["empty"]); got != wantEmpty {
		t.Errorf("empty flow = %s\nwant %s", got, wantEmpty)
	}

	wantPopulated := `{"issue":{"number":7,"provider":"github","url":"https://example.test/i/7"},` +
		`"merge":{"commit":"deadbeef","mergedAt":"2026-08-11T14:00:00Z","status":"merged"},` +
		`"pullRequest":{"baseBranch":"base","headBranch":"head","number":9,"provider":"github","status":"merged","url":"https://example.test/p/9"}}`
	if got := jsonOf(t, data["populated"]); got != wantPopulated {
		t.Errorf("populated flow = %s\nwant %s", got, wantPopulated)
	}

	wantCommitOnly := `{"merge":{"commit":"cafe","mergedAt":null,"status":"pending"}}`
	if got := jsonOf(t, data["mergeCommitOnly"]); got != wantCommitOnly {
		t.Errorf("merge-commit-only flow = %s\nwant %s", got, wantCommitOnly)
	}
	if strings.Contains(jsonOf(t, data), "0001-01-01") {
		t.Errorf("response serialized a zero time: %s", jsonOf(t, data))
	}
}

func TestSchemaDateTimeIsRFC3339(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{ flow(id: "flow-alpha-1") { createdAt updatedAt } }`)
	data := mustSucceed(t, result)
	flow := data["flow"].(map[string]any)
	for field, want := range map[string]string{
		"createdAt": "2026-08-11T12:00:00Z",
		"updatedAt": "2026-08-11T13:00:00Z",
	} {
		got, ok := flow[field].(string)
		if !ok {
			t.Fatalf("%s = %#v, want a string", field, flow[field])
		}
		if got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
		if _, err := time.Parse(time.RFC3339, got); err != nil {
			t.Errorf("%s = %q is not RFC3339: %v", field, got, err)
		}
	}
}

func TestSchemaInvalidQueryReturnsErrorsWithNullData(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{ repos { id `)
	if len(result.Errors) == 0 {
		t.Fatalf("errors = none, want a parse error")
	}
	if result.Data != nil {
		t.Fatalf("data = %#v, want null", result.Data)
	}
}

func TestSchemaHidesSensitiveFieldsAndHasNoMutations(t *testing.T) {
	result := executeQuery(t, fixtureSnapshot(), `{
		__schema {
			mutationType { name }
			subscriptionType { name }
			types { name fields { name } }
		}
	}`)
	data := mustSucceed(t, result)
	schemaInfo := data["__schema"].(map[string]any)
	if schemaInfo["mutationType"] != nil {
		t.Errorf("mutationType = %#v, want null", schemaInfo["mutationType"])
	}
	if schemaInfo["subscriptionType"] != nil {
		t.Errorf("subscriptionType = %#v, want null", schemaInfo["subscriptionType"])
	}

	forbidden := map[string]bool{
		"sessions": true, "launchIds": true, "launch_ids": true,
		"transcript": true, "transcriptPath": true,
		"planPath": true, "schemaVersion": true,
	}
	for _, entry := range schemaInfo["types"].([]any) {
		typeInfo := entry.(map[string]any)
		fields, _ := typeInfo["fields"].([]any)
		for _, fieldEntry := range fields {
			name := fieldEntry.(map[string]any)["name"].(string)
			if forbidden[name] {
				t.Errorf("type %v exposes forbidden field %q", typeInfo["name"], name)
			}
		}
	}
}

func TestSchemaSanitizesFlowSourceFailure(t *testing.T) {
	secretPath := "/tmp/approach-secret-root/flows"
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		func() ([]flowstore.FlowRecord, error) {
			return nil, errors.New("list flows: open " + secretPath + ": permission denied")
		},
		nil,
	)
	result := executeQuery(t, snap, `{ flows { id } repos { id } }`)
	if len(result.Errors) == 0 {
		t.Fatalf("errors = none, want a sanitized failure")
	}
	encoded := jsonOf(t, result)
	if !strings.Contains(encoded, errStateUnavailable.Error()) {
		t.Errorf("response = %s, want it to contain %q", encoded, errStateUnavailable.Error())
	}
	for _, leak := range []string{secretPath, "permission denied", "approach-secret-root"} {
		if strings.Contains(encoded, leak) {
			t.Errorf("response leaked %q: %s", leak, encoded)
		}
	}
}

func TestSchemaMissingSnapshotIsSanitized(t *testing.T) {
	schema, err := newSchema()
	if err != nil {
		t.Fatalf("newSchema() error = %v", err)
	}
	result := graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: `{ repos { id } }`,
		Context:       context.Background(),
	})
	if len(result.Errors) == 0 {
		t.Fatalf("errors = none, want a sanitized failure")
	}
	if !strings.Contains(result.Errors[0].Message, errStateUnavailable.Error()) {
		t.Fatalf("error = %q, want %q", result.Errors[0].Message, errStateUnavailable.Error())
	}
}
