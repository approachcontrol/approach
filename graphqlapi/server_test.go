package graphqlapi

import (
	"bytes"
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

	for _, host := range []string{"localhost:8787", "127.0.0.1:8787", "[::1]:8787", "localhost", "127.0.0.1"} {
		recorder := serve(handler, newGraphQLRequest(body, func(r *http.Request) { r.Host = host }))
		if recorder.Code != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, recorder.Code)
		}
	}
	for _, host := range []string{"evil.test", "approach.trycloudflare.com", "192.168.1.5:8787", ""} {
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
