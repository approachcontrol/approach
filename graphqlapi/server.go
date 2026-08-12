package graphqlapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/graphql-go/graphql"
)

const (
	// GraphQLPath is the single query endpoint.
	GraphQLPath = "/graphql"
	// HealthPath is the unauthenticated liveness endpoint.
	HealthPath = "/healthz"

	// MaxRequestBytes bounds the request body.
	MaxRequestBytes = 64 << 10

	defaultRequestTimeout = 20 * time.Second
	defaultMaxInFlight    = 8
)

// ServerOptions configures the GraphQL HTTP handler.
type ServerOptions struct {
	// Repos and Flows are the read seams for one snapshot per request.
	Repos RepoSource
	Flows FlowSource

	// Token, when non-empty, is required on every /graphql request as either
	// `Authorization: Bearer <token>` or `X-Approach-Token`.
	//
	// When it is empty the handler falls back to a loopback Host allowlist.
	// That is an anti-DNS-rebinding measure for browsers, not an access
	// control — Host is client-supplied. Leaving Token empty is only safe on a
	// loopback bind, and enforcing that pairing is the caller's job;
	// cmd/approach/serve.go refuses to open a non-loopback listener without a
	// token.
	Token string

	// Logger receives one line per request plus sanitized failure detail. It
	// never receives the token or the query body.
	Logger io.Writer

	// RequestTimeout bounds how long a client waits for snapshot
	// construction. Zero means 20s. Package-level seam; not a CLI flag.
	RequestTimeout time.Duration

	// MaxInFlight caps concurrent snapshot construction. Zero means 8.
	// Package-level seam; not a CLI flag.
	MaxInFlight int

	// beforeExecute is a test seam, run after the in-flight slot is taken and
	// before graphql.Do. Two invariants — that the slot is held across
	// execution, and that execution ignores client cancellation — are only
	// observable from inside that window. Unexported so it is set at
	// construction and never written while handler goroutines read it.
	beforeExecute func()
}

type server struct {
	schema         graphql.Schema
	repos          RepoSource
	flows          FlowSource
	token          string
	logger         io.Writer
	requestTimeout time.Duration
	slots          chan struct{}
	beforeExecute  func()
}

// NewServer builds the read-only GraphQL handler.
func NewServer(opts ServerOptions) (http.Handler, error) {
	schema, err := newSchema()
	if err != nil {
		return nil, fmt.Errorf("build graphql schema: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = io.Discard
	}
	timeout := opts.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	inFlight := opts.MaxInFlight
	if inFlight <= 0 {
		inFlight = defaultMaxInFlight
	}
	return &server{
		schema:         schema,
		repos:          opts.Repos,
		flows:          opts.Flows,
		token:          opts.Token,
		logger:         logger,
		requestTimeout: timeout,
		slots:          make(chan struct{}, inFlight),
		beforeExecute:  opts.beforeExecute,
	}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := s.route(w, r)
	// Method, path, status, duration only. Never the Authorization or
	// X-Approach-Token value, and never the query body.
	s.logf("%s %s %d %s", r.Method, loggedPath(r.URL), status, time.Since(started))
}

// maxLoggedPathBytes caps the logged path. The routing table has two entries,
// so anything long is already a 404 and there is nothing to learn from the
// rest of it.
const maxLoggedPathBytes = 128

// loggedPath renders the request path as a quoted, length-capped literal.
//
// url.URL.Path is percent-*decoded*, so logging it raw lets any unauthenticated
// request — 404s are logged before handleGraphQL runs, so this needs no token
// and no allowed Host — put newlines and terminal escapes into stderr:
// `/x%0aapproach graphql: POST /graphql 200 1ms` forges a whole log line.
// EscapedPath leaves those bytes encoded and Quote escapes whatever it did not.
func loggedPath(u *url.URL) string {
	path := u.EscapedPath()
	if len(path) > maxLoggedPathBytes {
		return strconv.Quote(path[:maxLoggedPathBytes]) + "..."
	}
	return strconv.Quote(path)
}

func (s *server) route(w http.ResponseWriter, r *http.Request) int {
	switch r.URL.Path {
	case HealthPath:
		if r.Method != http.MethodGet {
			return writeGraphQLError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case GraphQLPath:
		if r.Method != http.MethodPost {
			return writeGraphQLError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return s.handleGraphQL(w, r)
	default:
		return writeGraphQLError(w, http.StatusNotFound, "not found")
	}
}

func (s *server) handleGraphQL(w http.ResponseWriter, r *http.Request) int {
	// The Host allowlist only matters while the token is optional: a
	// DNS-rebound browser cannot supply a bearer token it does not have.
	// Enforcing it unconditionally would 403 every request that arrives
	// through a tunnel, which is the recommended remote consumer shape.
	if s.token == "" {
		if !isLoopbackHost(r.Host) {
			return writeGraphQLError(w, http.StatusForbidden, "forbidden host")
		}
	} else if !s.authorized(r) {
		return writeGraphQLError(w, http.StatusUnauthorized, "unauthorized")
	}

	// Load-bearing beyond content negotiation: requiring application/json is
	// what closes CSRF against the default token-less loopback server. A
	// browser form or `no-cors` fetch can only send the three CORS-safelisted
	// content types, so demanding this one forces a preflight — which gets a
	// bare 405 with no Access-Control-* header and never reaches this handler.
	// Accepting application/graphql or a form encoding here would silently
	// open every local Flow record to any page the user visits.
	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		return writeGraphQLError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return writeGraphQLError(w, http.StatusRequestEntityTooLarge, "request body too large")
		}
		return writeGraphQLError(w, http.StatusBadRequest, "could not read request body")
	}
	if isBatchedRequest(body) {
		return writeGraphQLError(w, http.StatusBadRequest, "batched requests are not supported")
	}
	var request graphQLRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return writeGraphQLError(w, http.StatusBadRequest, "request body must be a JSON object")
	}
	if strings.TrimSpace(request.Query) == "" {
		return writeGraphQLError(w, http.StatusBadRequest, "query is required")
	}
	document := parseQuery(request.Query)
	if err := inspectDocument(document); err != nil {
		return writeGraphQLError(w, http.StatusBadRequest, err.Error())
	}

	snap, release, outcome := s.acquireSnapshot(r.Context())
	switch outcome {
	case snapshotBusy:
		return writeGraphQLError(w, http.StatusServiceUnavailable, "server busy")
	case snapshotTimedOut:
		return writeGraphQLError(w, http.StatusGatewayTimeout, "request timed out")
	}
	// The slot stays held across execution, not just snapshot construction:
	// execution is where the cost is, so releasing it here would cap the cheap
	// phase and leave the expensive one unbounded.
	defer release()

	// Only now is the cost limit decidable — it multiplies each list field by
	// its real cardinality in this snapshot.
	if err := inspectCost(&s.schema, document, snap.bounds()); err != nil {
		return writeGraphQLError(w, http.StatusBadRequest, err.Error())
	}

	if s.beforeExecute != nil {
		s.beforeExecute()
	}

	// Execution deliberately does not inherit the request's cancellation.
	// graphql.Do returns on a cancelled context but leaves its execution
	// goroutine running to completion, so honoring the disconnect would let a
	// client fire and abandon requests to build up orphaned executions that no
	// semaphore slot accounts for. Resolvers are pure in-memory reads over a
	// cost-bounded result, so running to completion is bounded work.
	result := graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  request.Query,
		VariableValues: request.Variables,
		OperationName:  request.OperationName,
		Context:        withSnapshot(context.WithoutCancel(r.Context()), snap),
	})
	// Parse, validation, and execution failures are GraphQL errors, not
	// transport errors: 200 with a populated errors array.
	return writeJSON(w, http.StatusOK, result)
}

type snapshotOutcome int

const (
	snapshotReady snapshotOutcome = iota
	snapshotBusy
	snapshotTimedOut
)

// acquireSnapshot takes an in-flight slot, builds the snapshot in a goroutine,
// and waits for it against the request timeout. On snapshotReady it returns a
// release func the caller must defer; on every other outcome release is nil
// and there is nothing for the caller to release.
//
// The slot is never released while work it accounts for is still running. On
// the timeout path a watcher goroutine holds it until the scan finishes, so it
// outlives the handler. Releasing on handler return would let a client that
// fires and abandons requests spawn unbounded concurrent orphaned scans, with
// the 503 never firing: the semaphore and the timeout would cancel each other
// out.
//
// scanner.Scan and flowstore.List take no context, so the scan itself cannot
// be interrupted; the timeout only bounds how long a client waits, and the
// semaphore is the real backpressure control.
func (s *server) acquireSnapshot(ctx context.Context) (*snapshot, func(), snapshotOutcome) {
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, nil, snapshotBusy
	}
	var once sync.Once
	release := func() { once.Do(func() { <-s.slots }) }

	// Buffered, so the builder never blocks on a handler that has given up and
	// exactly one of the two receives below consumes the result.
	built := make(chan *snapshot, 1)
	go func() { built <- buildSnapshot(s.repos, s.flows, s.logf) }()

	timer := time.NewTimer(s.requestTimeout)
	defer timer.Stop()
	select {
	case snap := <-built:
		return snap, release, snapshotReady
	case <-timer.C:
	case <-ctx.Done():
	}
	// Abandoned: hand the slot to a watcher that releases it once the scan
	// this slot is accounting for has actually finished.
	go func() {
		<-built
		release()
	}()
	return nil, nil, snapshotTimedOut
}

func (s *server) authorized(r *http.Request) bool {
	expected := []byte(s.token)
	if header := r.Header.Get("X-Approach-Token"); header != "" {
		if subtle.ConstantTimeCompare([]byte(header), expected) == 1 {
			return true
		}
	}
	authorization := r.Header.Get("Authorization")
	scheme, value, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(value)), expected) == 1
}

func (s *server) logf(format string, args ...any) {
	fmt.Fprintf(s.logger, "approach graphql: "+format+"\n", args...)
}

type graphQLRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

func isBatchedRequest(body []byte) bool {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	return strings.HasPrefix(trimmed, "[")
}

func hasJSONContentType(header string) bool {
	if header == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

// isLoopbackHost accepts localhost and any loopback IP literal, on any port.
//
// It applies net.IP.IsLoopback, the same rule cmd/approach/serve.go uses to
// decide a bind address may run without a token. Anything narrower makes an
// address that starts token-free — 127.0.0.2, or an expanded spelling of ::1 —
// 403 every request that reaches it. Widening the rule to the whole loopback
// range costs nothing against DNS rebinding either: rebinding resolves a
// hostname, and a rebound Host is a name, never an IP literal.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	name := host
	if hostPart, _, err := net.SplitHostPort(host); err == nil {
		name = hostPart
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, status int, payload any) int {
	// No Access-Control-* header is ever emitted, on any status.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Flow state is live and, over the tunnel the docs recommend, may pass
	// through caches that would otherwise be free to store it.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return status
	}
	return status
}

// writeGraphQLError emits a transport failure in the shape GraphQL clients
// already parse. The message is always a fixed string, never derived from
// request content.
func writeGraphQLError(w http.ResponseWriter, status int, message string) int {
	return writeJSON(w, status, map[string]any{
		"errors": []map[string]string{{"message": message}},
	})
}
