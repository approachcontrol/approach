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
	"strings"
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
	// `Authorization: Bearer <token>` or `X-Approach-Token`. When it is empty
	// the handler instead enforces a loopback Host allowlist.
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
}

type server struct {
	schema         graphql.Schema
	repos          RepoSource
	flows          FlowSource
	token          string
	logger         io.Writer
	requestTimeout time.Duration
	slots          chan struct{}
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
	}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := s.route(w, r)
	// Method, path, status, duration only. Never the Authorization or
	// X-Approach-Token value, and never the query body.
	s.logf("%s %s %d %s", r.Method, r.URL.Path, status, time.Since(started))
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
	if err := inspectDocument(request.Query); err != nil {
		return writeGraphQLError(w, http.StatusBadRequest, err.Error())
	}

	snap, outcome := s.acquireSnapshot(r.Context())
	switch outcome {
	case snapshotBusy:
		return writeGraphQLError(w, http.StatusServiceUnavailable, "server busy")
	case snapshotTimedOut:
		return writeGraphQLError(w, http.StatusGatewayTimeout, "request timed out")
	}

	result := graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  request.Query,
		VariableValues: request.Variables,
		OperationName:  request.OperationName,
		Context:        withSnapshot(r.Context(), snap),
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
// and waits for it against the request timeout.
//
// On the timeout path the slot stays held until the scan finishes and the
// goroutine releases it — never released by the handler. Releasing on handler
// return would let a client that fires and abandons requests spawn unbounded
// concurrent orphaned scans, with the 503 never firing: the semaphore and the
// timeout would cancel each other out.
//
// scanner.Scan and flowstore.List take no context, so the scan itself cannot
// be interrupted; the timeout only bounds how long a client waits, and the
// semaphore is the real backpressure control.
func (s *server) acquireSnapshot(ctx context.Context) (*snapshot, snapshotOutcome) {
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, snapshotBusy
	}

	built := make(chan *snapshot, 1)
	go func() {
		defer func() { <-s.slots }()
		built <- buildSnapshot(s.repos, s.flows, s.logf)
	}()

	timer := time.NewTimer(s.requestTimeout)
	defer timer.Stop()
	select {
	case snap := <-built:
		return snap, snapshotReady
	case <-timer.C:
		return nil, snapshotTimedOut
	case <-ctx.Done():
		return nil, snapshotTimedOut
	}
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

// isLoopbackHost accepts localhost, 127.0.0.1, and [::1] on any port.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	name := host
	if hostPart, _, err := net.SplitHostPort(host); err == nil {
		name = hostPart
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	switch strings.ToLower(name) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) int {
	// No Access-Control-* header is ever emitted, on any status.
	w.Header().Set("Content-Type", "application/json")
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
