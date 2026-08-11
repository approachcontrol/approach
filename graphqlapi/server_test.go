package graphqlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testSources() (RepoSource, FlowSource) {
	created := fixtureTime(0)
	return staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		staticFlows(
			flowstore.FlowRecord{
				FlowID: "flow-alpha-1", Title: "Alpha one", RepoPath: "/repos/alpha",
				Status: flowstore.StatusInProgress, Merge: flowstore.Merge{Status: flowstore.MergePending},
				Phases:    []flowstore.FlowPhase{{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady}},
				CreatedAt: created, UpdatedAt: created,
			},
			flowstore.FlowRecord{
				FlowID: "flow-alpha-2", Title: "Alpha two", RepoPath: "/repos/alpha",
				Status: flowstore.StatusPending, Merge: flowstore.Merge{Status: flowstore.MergePending},
				CreatedAt: created, UpdatedAt: created,
			},
		)
}

func newTestServer(t *testing.T, opts ServerOptions) http.Handler {
	t.Helper()
	if opts.Repos == nil && opts.Flows == nil {
		opts.Repos, opts.Flows = testSources()
	}
	handler, err := NewServer(opts)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return handler
}

func newGraphQLRequest(body string, mutate ...func(*http.Request)) *http.Request {
	request := httptest.NewRequest(http.MethodPost, GraphQLPath, strings.NewReader(body))
	request.Host = "127.0.0.1:8787"
	request.Header.Set("Content-Type", "application/json")
	for _, apply := range mutate {
		apply(request)
	}
	return request
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func queryBody(t *testing.T, query string) string {
	t.Helper()
	encoded, err := json.Marshal(graphQLRequest{Query: query})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(encoded)
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response body %q is not JSON: %v", recorder.Body.String(), err)
	}
	return payload
}

func TestServerMethodAndPathRouting(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, GraphQLPath, http.StatusMethodNotAllowed},
		{http.MethodPut, GraphQLPath, http.StatusMethodNotAllowed},
		{http.MethodDelete, GraphQLPath, http.StatusMethodNotAllowed},
		{http.MethodPost, HealthPath, http.StatusMethodNotAllowed},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodGet, "/graphiql", http.StatusNotFound},
		{http.MethodGet, HealthPath, http.StatusOK},
	}
	for _, testCase := range cases {
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		request.Host = "127.0.0.1:8787"
		recorder := serve(handler, request)
		if recorder.Code != testCase.want {
			t.Errorf("%s %s = %d, want %d", testCase.method, testCase.path, recorder.Code, testCase.want)
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s %s Content-Type = %q, want application/json", testCase.method, testCase.path, got)
		}
	}
}

func TestServerHealthzBody(t *testing.T) {
	handler := newTestServer(t, ServerOptions{Token: "secret"})
	request := httptest.NewRequest(http.MethodGet, HealthPath, nil)
	request.Host = "example.test"
	recorder := serve(handler, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200 even with a token configured", recorder.Code)
	}
	if got, want := decodeBody(t, recorder)["status"], "ok"; got != want {
		t.Fatalf("status = %v, want %q", got, want)
	}
}

func TestServerContentTypeChecks(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	body := queryBody(t, `{ repos { id } }`)

	recorder := serve(handler, newGraphQLRequest(body, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
	}))
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d, want 415", recorder.Code)
	}

	recorder = serve(handler, newGraphQLRequest(body, func(r *http.Request) {
		r.Header.Del("Content-Type")
	}))
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("missing content-type = %d, want 415", recorder.Code)
	}

	recorder = serve(handler, newGraphQLRequest(body, func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json; charset=utf-8")
	}))
	if recorder.Code != http.StatusOK {
		t.Errorf("application/json; charset=utf-8 = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerHostAllowlistWithoutToken(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	body := queryBody(t, `{ repos { id } }`)

	// Every host here is one cmd/approach/serve.go would let bind without a
	// token, so rejecting any of them would make that address unreachable.
	for _, host := range []string{
		"localhost:8787", "127.0.0.1:8787", "[::1]:8787", "localhost", "127.0.0.1",
		"127.0.0.2:8787", "127.1.2.3", "[0:0:0:0:0:0:0:1]:8787", "LOCALHOST:8787",
	} {
		recorder := serve(handler, newGraphQLRequest(body, func(r *http.Request) { r.Host = host }))
		if recorder.Code != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, recorder.Code)
		}
	}
	// A rebound Host is a name, never an IP literal, so a name that merely
	// reads like a loopback address is still rejected.
	for _, host := range []string{
		"evil.test", "approach.trycloudflare.com", "192.168.1.5:8787", "",
		"127.0.0.1.evil.test", "0.0.0.0:8787", "10.0.0.1",
	} {
		recorder := serve(handler, newGraphQLRequest(body, func(r *http.Request) { r.Host = host }))
		if recorder.Code != http.StatusForbidden {
			t.Errorf("Host %q = %d, want 403", host, recorder.Code)
		}
	}
}

func TestServerHostNotCheckedWhenTokenConfigured(t *testing.T) {
	handler := newTestServer(t, ServerOptions{Token: "secret"})
	body := queryBody(t, `{ repos { id } }`)
	recorder := serve(handler, newGraphQLRequest(body, func(r *http.Request) {
		r.Host = "approach.trycloudflare.com"
		r.Header.Set("Authorization", "Bearer secret")
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("tunnel Host with a valid token = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerAuth(t *testing.T) {
	handler := newTestServer(t, ServerOptions{Token: "s3cret"})
	body := queryBody(t, `{ repos { id } }`)
	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"missing", func(*http.Request) {}, http.StatusUnauthorized},
		{"wrong bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"wrong header token", func(r *http.Request) { r.Header.Set("X-Approach-Token", "nope") }, http.StatusUnauthorized},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic s3cret") }, http.StatusUnauthorized},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") }, http.StatusOK},
		{"bearer lowercase scheme", func(r *http.Request) { r.Header.Set("Authorization", "bearer s3cret") }, http.StatusOK},
		{"approach token header", func(r *http.Request) { r.Header.Set("X-Approach-Token", "s3cret") }, http.StatusOK},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := serve(handler, newGraphQLRequest(body, testCase.mutate))
			if recorder.Code != testCase.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.want, recorder.Body.String())
			}
		})
	}

	open := newTestServer(t, ServerOptions{})
	if recorder := serve(open, newGraphQLRequest(body)); recorder.Code != http.StatusOK {
		t.Fatalf("loopback without a configured token = %d, want 200", recorder.Code)
	}
}

func TestServerBodyChecks(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{"query": `, http.StatusBadRequest},
		{"batched", `[{"query":"{ repos { id } }"}]`, http.StatusBadRequest},
		{"empty query", `{"query":""}`, http.StatusBadRequest},
		{"whitespace query", `{"query":"   "}`, http.StatusBadRequest},
		{"absent query", `{}`, http.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := serve(handler, newGraphQLRequest(testCase.body))
			if recorder.Code != testCase.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.want, recorder.Body.String())
			}
			payload := decodeBody(t, recorder)
			if _, hasData := payload["data"]; hasData {
				t.Errorf("transport failure body has a data key: %s", recorder.Body.String())
			}
			if _, hasErrors := payload["errors"]; !hasErrors {
				t.Errorf("transport failure body has no errors key: %s", recorder.Body.String())
			}
		})
	}
}

func TestServerRejectsOversizedBody(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	padding := strings.Repeat("x", MaxRequestBytes+1024)
	body := queryBody(t, `{ repos { id } } # `+padding)
	recorder := serve(handler, newGraphQLRequest(body))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerRejectsNonQueryOperations(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	for _, query := range []string{`mutation { nope }`, `subscription { flows { id } }`} {
		recorder := serve(handler, newGraphQLRequest(queryBody(t, query)))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400", query, recorder.Code)
		}
	}
}

func TestServerLimitsReturnBadRequest(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	cases := map[string]string{
		"too deep":       nestedQuery(maxQueryDepth + 1),
		"deep fragment":  "{ repos { ...Deep } }\nfragment Deep on Repo " + nestedQuery(maxQueryDepth),
		"cyclic":         "{ repos { ...A } }\nfragment A on Repo { flows { ...B } }\nfragment B on Flow { repo { ...A } }",
		"doubling chain": doublingChainQuery(40),
		"node cap":       "{ ...Body }\nfragment Body on Query { " + strings.Repeat("f ", maxQueryNodes+1) + "}",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- serve(handler, newGraphQLRequest(queryBody(t, query))) }()
			select {
			case recorder := <-done:
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("handler did not return; the limit walk is unbounded")
			}
		})
	}
}

func doublingChainQuery(levels int) string {
	var builder strings.Builder
	builder.WriteString("{ ...F0 }\n")
	for level := 0; level < levels-1; level++ {
		fmt.Fprintf(&builder, "fragment F%d on Query { ...F%d ...F%d }\n", level, level+1, level+1)
	}
	fmt.Fprintf(&builder, "fragment F%d on Query { leaf }\n", levels-1)
	return builder.String()
}

func TestServerAllowsIntrospection(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	query := `{ __schema { types { fields { type {
		ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType { ofType { name } } } } } } } } }
	} } } } }`
	if measured := measure(t, query).rawDepth; measured <= maxQueryDepth {
		t.Fatalf("introspection fixture rawDepth = %d, want > %d", measured, maxQueryDepth)
	}
	recorder := serve(handler, newGraphQLRequest(queryBody(t, query)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("introspection = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerGraphQLErrorsAreTwoHundred(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	recorder := serve(handler, newGraphQLRequest(queryBody(t, `{ repos { id `)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("syntax error = %d, want 200", recorder.Code)
	}
	payload := decodeBody(t, recorder)
	if payload["data"] != nil {
		t.Errorf("data = %#v, want null", payload["data"])
	}
	errorList, ok := payload["errors"].([]any)
	if !ok || len(errorList) == 0 {
		t.Fatalf("errors = %#v, want a populated array", payload["errors"])
	}
}

func TestServerVariablesAndOperationName(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})

	body, err := json.Marshal(graphQLRequest{
		Query:     `query One($id: ID!) { flow(id: $id) { id title } }`,
		Variables: map[string]any{"id": "flow-alpha-2"},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	recorder := serve(handler, newGraphQLRequest(string(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("variables query = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	data := decodeBody(t, recorder)["data"].(map[string]any)
	flow := data["flow"].(map[string]any)
	if flow["id"] != "flow-alpha-2" {
		t.Fatalf("flow.id = %v, want %q", flow["id"], "flow-alpha-2")
	}

	body, err = json.Marshal(graphQLRequest{
		Query:         `query First { flow(id: "flow-alpha-1") { id } } query Second { flow(id: "flow-alpha-2") { id } }`,
		OperationName: "Second",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	recorder = serve(handler, newGraphQLRequest(string(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("operationName query = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	data = decodeBody(t, recorder)["data"].(map[string]any)
	flow = data["flow"].(map[string]any)
	if flow["id"] != "flow-alpha-2" {
		t.Fatalf("operationName selected %v, want flow-alpha-2", flow["id"])
	}
}

func TestServerTimeoutHoldsSlotUntilScanFinishes(t *testing.T) {
	release := make(chan struct{})
	_, flows := testSources()
	handler := newTestServer(t, ServerOptions{
		Repos: func() ([]scanner.Repo, error) {
			<-release
			return []scanner.Repo{{Path: "/repos/alpha", DisplayName: "alpha"}}, nil
		},
		Flows:          flows,
		RequestTimeout: 20 * time.Millisecond,
		MaxInFlight:    1,
	})
	body := queryBody(t, `{ repos { id } }`)

	if recorder := serve(handler, newGraphQLRequest(body)); recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("blocked request = %d, want 504 (body %s)", recorder.Code, recorder.Body.String())
	}
	// The slot is still held by the orphaned scan, so the next request is
	// rejected rather than starting a second concurrent scan.
	if recorder := serve(handler, newGraphQLRequest(body)); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request = %d, want 503 (body %s)", recorder.Code, recorder.Body.String())
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		recorder := serve(handler, newGraphQLRequest(body))
		if recorder.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never freed after the scan finished; last status %d", recorder.Code)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServerNeverEmitsCORSHeaders(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	body := queryBody(t, `{ repos { id } }`)
	recorders := []*httptest.ResponseRecorder{
		serve(handler, newGraphQLRequest(body)),
		serve(handler, newGraphQLRequest(body, func(r *http.Request) { r.Host = "evil.test" })),
		serve(handler, httptest.NewRequest(http.MethodOptions, GraphQLPath, nil)),
	}
	for _, recorder := range recorders {
		for name := range recorder.Header() {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				t.Errorf("status %d emitted CORS header %q", recorder.Code, name)
			}
		}
	}
}

func TestServerLogHygiene(t *testing.T) {
	logger := &syncBuffer{}
	_, flows := testSources()
	handler := newTestServer(t, ServerOptions{
		Repos:  staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		Flows:  flows,
		Token:  "super-secret-token",
		Logger: logger,
	})
	query := `{ flow(id: "flow-alpha-1") { title instructions } }`
	recorder := serve(handler, newGraphQLRequest(queryBody(t, query), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer super-secret-token")
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}

	logged := logger.String()
	if strings.Contains(logged, "super-secret-token") {
		t.Errorf("log leaked the token: %s", logged)
	}
	for _, fragment := range []string{"flow-alpha-1", "instructions", "{ flow("} {
		if strings.Contains(logged, fragment) {
			t.Errorf("log leaked query content %q: %s", fragment, logged)
		}
	}
	pattern := regexp.MustCompile(`POST ` + regexp.QuoteMeta(GraphQLPath) + ` 200 \S+`)
	if !pattern.MatchString(logged) {
		t.Errorf("log = %q, want a method/path/status/duration line", logged)
	}
}

func TestServerSanitizesStoreFailure(t *testing.T) {
	logger := &syncBuffer{}
	secret := "/tmp/approach-secret-root/flows"
	handler := newTestServer(t, ServerOptions{
		Repos: staticRepos(),
		Flows: func() ([]flowstore.FlowRecord, error) {
			return nil, fmt.Errorf("list flows: open %s: permission denied", secret)
		},
		Logger: logger,
	})
	recorder := serve(handler, newGraphQLRequest(queryBody(t, `{ flows { id } }`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, errStateUnavailable.Error()) {
		t.Errorf("body = %s, want the sanitized message", body)
	}
	for _, leak := range []string{secret, "permission denied"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaked %q: %s", leak, body)
		}
	}
	if !strings.Contains(logger.String(), secret) {
		t.Errorf("log = %q, want the unsanitized detail server-side", logger.String())
	}
}

func TestServerRejectsResultAmplification(t *testing.T) {
	// A tiny query that passes every structural limit and resolves flows^N
	// values, because Repo.flows <-> Flow.repo is a cycle in the type graph.
	// Only the snapshot-aware cost limit stands in front of it.
	// One repo with thirty flows is enough: unbounded, this exact shape
	// resolves 30^5 flows and serializes to hundreds of megabytes.
	created := fixtureTime(0)
	flows := make([]flowstore.FlowRecord, 0, 30)
	for f := 0; f < 30; f++ {
		flows = append(flows, flowstore.FlowRecord{
			FlowID: fmt.Sprintf("flow-%d", f), Title: "t", RepoPath: "/repos/alpha",
			Status: flowstore.StatusPending, Merge: flowstore.Merge{Status: flowstore.MergePending},
			CreatedAt: created, UpdatedAt: created,
		})
	}
	handler := newTestServer(t, ServerOptions{
		Repos: staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		Flows: staticFlows(flows...),
	})

	query := cycleQuery(5)
	measured := measure(t, query)
	if measured.depth > maxQueryDepth || measured.nodes > maxQueryNodes {
		t.Fatalf("fixture depth=%d nodes=%d trips a structural limit; it no longer proves the cost limit is load-bearing",
			measured.depth, measured.nodes)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(handler, newGraphQLRequest(queryBody(t, query))) }()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (response was %d bytes)", recorder.Code, recorder.Body.Len())
		}
		if !strings.Contains(recorder.Body.String(), errQueryTooExpensive.Error()) {
			t.Errorf("body = %s, want the cost-limit message", recorder.Body.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("handler did not return; the query was executed instead of rejected")
	}
}

func TestServerAllowsAmplifyingShapeOnASmallSnapshot(t *testing.T) {
	// The cost limit must reject a shape only because of what this snapshot
	// holds. Against the two-flow fixture the same nesting is cheap and has to
	// keep working, or the limit is just a disguised depth cap.
	handler := newTestServer(t, ServerOptions{})
	recorder := serve(handler, newGraphQLRequest(queryBody(t, cycleQuery(3))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if payload := decodeBody(t, recorder); payload["data"] == nil {
		t.Errorf("data = nil, want a result (errors %v)", payload["errors"])
	}
}

func TestServerDefaultsMatchDocumentedLimits(t *testing.T) {
	// Every number in the hardening table of docs/graphql-api.md. Each is
	// either overridden by other tests or only asserted indirectly, so without
	// this they drift out of the doc silently.
	for name, got := range map[string][2]int{
		"defaultMaxInFlight":    {defaultMaxInFlight, 8},
		"MaxRequestBytes":       {MaxRequestBytes, 64 << 10},
		"maxQueryDepth":         {maxQueryDepth, 12},
		"maxIntrospectionDepth": {maxIntrospectionDepth, 20},
		"maxIntrospectionNodes": {maxIntrospectionNodes, 400},
		"maxQueryNodes":         {maxQueryNodes, 2000},
		"maxWalkBudget":         {maxWalkBudget, 20000},
		"maxQueryCost":          {maxQueryCost, 500_000},
		"maxResponseBytes":      {maxResponseBytes, 16 << 20},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %d, want %d (docs/graphql-api.md)", name, got[0], got[1])
		}
	}
	if defaultRequestTimeout != 20*time.Second {
		t.Errorf("defaultRequestTimeout = %s, want 20s (docs/graphql-api.md)", defaultRequestTimeout)
	}
	handler, err := NewServer(ServerOptions{})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	built, ok := handler.(*server)
	if !ok {
		t.Fatalf("NewServer() returned %T, want *server", handler)
	}
	if got := cap(built.slots); got != defaultMaxInFlight {
		t.Errorf("in-flight capacity = %d, want %d", got, defaultMaxInFlight)
	}
	if built.requestTimeout != defaultRequestTimeout {
		t.Errorf("requestTimeout = %s, want %s", built.requestTimeout, defaultRequestTimeout)
	}
}

func TestServerSetsResponseHardeningHeaders(t *testing.T) {
	handler := newTestServer(t, ServerOptions{})
	for name, request := range map[string]*http.Request{
		"query":  newGraphQLRequest(queryBody(t, "{ repos { id } }")),
		"error":  newGraphQLRequest("not json"),
		"health": httptest.NewRequest(http.MethodGet, HealthPath, nil),
	} {
		t.Run(name, func(t *testing.T) {
			header := serve(handler, request).Header()
			if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestServerRejectsByteAmplificationFromUnboundedText(t *testing.T) {
	// Value count alone is a bad proxy for response size: Flow.instructions is
	// unbounded agent text, so this query resolves only a few thousand values
	// but serializes to hundreds of megabytes once Repo.flows <-> Flow.repo
	// re-entry repeats each one. Only the byte budget sees it.
	created := fixtureTime(0)
	instructions := strings.Repeat("x", 8<<10)
	repos := make([]scanner.Repo, 0, 10)
	flows := make([]flowstore.FlowRecord, 0, 1000)
	for r := 0; r < 10; r++ {
		path := fmt.Sprintf("/repos/r%d", r)
		repos = append(repos, scanner.Repo{Path: path, DisplayName: fmt.Sprintf("r%d", r)})
		for f := 0; f < 100; f++ {
			flows = append(flows, flowstore.FlowRecord{
				FlowID: fmt.Sprintf("flow-%d-%d", r, f), Title: "t", RepoPath: path, Instructions: instructions,
				Status: flowstore.StatusPending, Merge: flowstore.Merge{Status: flowstore.MergePending},
				CreatedAt: created, UpdatedAt: created,
			})
		}
	}
	handler := newTestServer(t, ServerOptions{Repos: staticRepos(repos...), Flows: staticFlows(flows...)})

	query := `{ repos { flows { repo { flows { instructions } } } } }`
	if measured := measure(t, query); measured.depth > maxQueryDepth || measured.nodes > maxQueryNodes {
		t.Fatalf("fixture depth=%d nodes=%d trips a structural limit", measured.depth, measured.nodes)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(handler, newGraphQLRequest(queryBody(t, query))) }()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (response was %d bytes)", recorder.Code, recorder.Body.Len())
		}
		if !strings.Contains(recorder.Body.String(), errResponseTooLarge.Error()) {
			t.Errorf("body = %s, want the response-size message", recorder.Body.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("handler did not return; the query was executed instead of rejected")
	}

	// The same data without the re-entry is just the data, and must still be
	// served: the limit targets amplification, not size.
	recorder := serve(handler, newGraphQLRequest(queryBody(t, `{ flows { id } }`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plain flow list = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerHoldsInFlightSlotAcrossExecution(t *testing.T) {
	// The slot must cover execution, not just snapshot construction:
	// execution is where the cost is, so a slot released after the snapshot
	// would cap the cheap phase and leave the expensive one unbounded.
	executing := make(chan struct{})
	proceed := make(chan struct{})
	handler := newTestServer(t, ServerOptions{MaxInFlight: 1, beforeExecute: func() {
		select {
		case executing <- struct{}{}:
			<-proceed
		default: // later requests run unimpeded
		}
	}})

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- serve(handler, newGraphQLRequest(queryBody(t, "{ repos { id } }"))) }()
	select {
	case <-executing:
	case <-time.After(10 * time.Second):
		t.Fatalf("first request never reached execution")
	}

	second := serve(handler, newGraphQLRequest(queryBody(t, "{ repos { id } }")))
	if second.Code != http.StatusServiceUnavailable {
		t.Errorf("concurrent request during execution = %d, want 503 (the slot was released too early)", second.Code)
	}

	close(proceed)
	if recorder := <-first; recorder.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", recorder.Code)
	}
	// And the slot comes back once the handler returns.
	if third := serve(handler, newGraphQLRequest(queryBody(t, "{ repos { id } }"))); third.Code != http.StatusOK {
		t.Errorf("request after completion = %d, want 200 (the slot was never released)", third.Code)
	}
}

func TestServerExecutionIgnoresClientCancellation(t *testing.T) {
	// graphql.Do returns on a cancelled context but leaves its execution
	// goroutine running to completion, so honoring a client disconnect would
	// accumulate orphaned executions that no slot accounts for. Execution runs
	// on an uncancellable context instead.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := newTestServer(t, ServerOptions{beforeExecute: cancel})

	request := newGraphQLRequest(queryBody(t, "{ repos { id displayName } }")).WithContext(ctx)
	recorder := serve(handler, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	payload := decodeBody(t, recorder)
	if payload["errors"] != nil {
		t.Fatalf("errors = %v, want none (execution inherited the cancellation)", payload["errors"])
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["repos"] == nil {
		t.Fatalf("data = %#v, want a resolved repos list", payload["data"])
	}
}

// bigSnapshotServer builds a handler over repos x flows flows, each carrying
// phases phases, for the amplification tests.
func bigSnapshotServer(t *testing.T, repoCount, flowsPerRepo, phaseCount int) http.Handler {
	t.Helper()
	created := fixtureTime(0)
	repos := make([]scanner.Repo, 0, repoCount)
	flows := make([]flowstore.FlowRecord, 0, repoCount*flowsPerRepo)
	phases := make([]flowstore.FlowPhase, 0, phaseCount)
	for p := 0; p < phaseCount; p++ {
		phases = append(phases, flowstore.FlowPhase{
			PhaseID: fmt.Sprintf("p%d", p), Title: "Phase", Kind: flowstore.KindPlan,
			Status: flowstore.PhasePending, Order: p, CreatedAt: created, UpdatedAt: created,
		})
	}
	for r := 0; r < repoCount; r++ {
		path := fmt.Sprintf("/repos/r%d", r)
		repos = append(repos, scanner.Repo{Path: path, DisplayName: fmt.Sprintf("r%d", r)})
		for f := 0; f < flowsPerRepo; f++ {
			flows = append(flows, flowstore.FlowRecord{
				FlowID: fmt.Sprintf("flow-%d-%d", r, f), Title: "t", RepoPath: path,
				Status: flowstore.StatusPending, Merge: flowstore.Merge{Status: flowstore.MergePending},
				Phases: phases, CreatedAt: created, UpdatedAt: created,
			})
		}
	}
	return newTestServer(t, ServerOptions{Repos: staticRepos(repos...), Flows: staticFlows(flows...)})
}

func assertRejected(t *testing.T, handler http.Handler, query string, want error) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(handler, newGraphQLRequest(queryBody(t, query))) }()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (response was %d bytes)", recorder.Code, recorder.Body.Len())
		}
		if !strings.Contains(recorder.Body.String(), want.Error()) {
			t.Errorf("body = %s, want %q", recorder.Body.String(), want)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("handler did not return; the query was executed instead of rejected")
	}
}

func TestServerChargesAliasBytes(t *testing.T) {
	// graphql-go writes the *alias* as the response key, and an alias is
	// client-chosen and unbounded. Charging the field name instead leaves a
	// 60 KiB alias under a list traversal free, which is a gigabyte-scale
	// response from a single request that fits the body cap.
	handler := bigSnapshotServer(t, 1, 100, 100)
	alias := strings.Repeat("a", 55<<10)
	query := fmt.Sprintf("{ repos { flows { phases { %s: id } } } }", alias)
	if len(queryBody(t, query)) > MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, which the body cap would have rejected first", len(queryBody(t, query)))
	}
	if measured := measure(t, query); measured.depth > maxQueryDepth || measured.nodes > maxQueryNodes {
		t.Fatalf("fixture depth=%d nodes=%d trips a structural limit", measured.depth, measured.nodes)
	}
	assertRejected(t, handler, query, errResponseTooLarge)

	// A short alias over the same shape stays served.
	recorder := serve(handler, newGraphQLRequest(queryBody(t, "{ repos { flows { phases { x: id } } } }")))
	if recorder.Code != http.StatusOK {
		t.Fatalf("short alias = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerChargesAliasOfAnEmptyList(t *testing.T) {
	// A list field's response key is written once per parent object even when
	// the list resolves to nothing, so charging the key inside the cardinality
	// multiplier makes the whole field free on a snapshot whose lists are
	// empty. A store with many repos and no Flows yet is the ordinary shape of
	// a fresh install, and there `"<55 KiB alias>":[]` per repo is tens of
	// megabytes of response from a request that fits the 64 KiB body cap.
	handler := bigSnapshotServer(t, 400, 0, 0)
	alias := strings.Repeat("a", 55<<10)
	query := fmt.Sprintf("{ repos { %s: flows { id } } }", alias)
	if len(queryBody(t, query)) > MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, which the body cap would have rejected first", len(queryBody(t, query)))
	}
	if measured := measure(t, query); measured.depth > maxQueryDepth || measured.nodes > maxQueryNodes {
		t.Fatalf("fixture depth=%d nodes=%d trips a structural limit", measured.depth, measured.nodes)
	}
	assertRejected(t, handler, query, errResponseTooLarge)

	// The same shape with an ordinary key stays served: it is the alias width,
	// not the empty list, that has to cost something.
	recorder := serve(handler, newGraphQLRequest(queryBody(t, "{ repos { flows { id } } }")))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unaliased empty list = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerChargesTypename(t *testing.T) {
	// __typename looks like introspection but resolves once per *snapshot
	// object*, not against the fixed schema, so it puts data-proportional
	// values and bytes on the wire and cannot share the introspection cost
	// exemption that __schema and __type get.
	handler := bigSnapshotServer(t, 1, 100, 100)
	var builder strings.Builder
	builder.WriteString("{ repos { flows { phases { ")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&builder, "a%d: __typename ", i)
	}
	builder.WriteString("} } } }")

	recorder := serve(handler, newGraphQLRequest(queryBody(t, builder.String())))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (response was %d bytes)", recorder.Code, recorder.Body.Len())
	}

	// __schema, by contrast, stays exempt however large the snapshot is.
	recorder = serve(handler, newGraphQLRequest(queryBody(t, `{ __schema { types { name fields { name } } } }`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("introspection = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestServerChargesJSONEscapedWidth(t *testing.T) {
	// encoding/json expands <, >, & and control bytes to a six-byte \uXXXX.
	// Measuring len() instead of the encoded width would put the real ceiling
	// at six times the documented one, on exactly the markdown that lands in
	// Flow.instructions.
	created := fixtureTime(0)
	// Each "<a & b>" is 7 bytes of markdown that the encoder writes as 22.
	// len() therefore reads this as 6.3 MB — comfortably inside the budget —
	// while the response really is 19.8 MB.
	instructions := strings.Repeat("<a & b>", 900_000)
	if len(instructions) >= maxResponseBytes {
		t.Fatalf("fixture is %d unescaped bytes; a len()-based bound would reject it too, so it proves nothing", len(instructions))
	}
	handler := newTestServer(t, ServerOptions{
		Repos: staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		Flows: staticFlows(flowstore.FlowRecord{
			FlowID: "flow-0", Title: "t", RepoPath: "/repos/alpha", Instructions: instructions,
			Status: flowstore.StatusPending, Merge: flowstore.Merge{Status: flowstore.MergePending},
			CreatedAt: created, UpdatedAt: created,
		}),
	})
	assertRejected(t, handler, `{ flow(id: "flow-0") { instructions } }`, errResponseTooLarge)
}
