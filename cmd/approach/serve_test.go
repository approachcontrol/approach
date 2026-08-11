package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serveDeps returns deps that never touch the real environment, config, or
// default artifact root.
func serveDeps(t *testing.T, stateRoot string, env map[string]string) runDeps {
	t.Helper()
	return runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{Sessions: config.SessionsConfig{Root: stateRoot}}, nil
		},
		getenv: func(key string) string { return env[key] },
		scan: func(scanner.ScanOptions) ([]scanner.Repo, error) {
			return nil, nil
		},
		startProgram: func([]scanner.Repo, config.Config) error {
			t.Fatal("program should not start for serve")
			return nil
		},
		stdout: io.Discard,
		stderr: io.Discard,
	}
}

// seedFlow writes one Flow record into root and returns its id.
func seedFlow(t *testing.T, root, title, repoPath string) string {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		Title:        title,
		Instructions: "Do the thing",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return record.FlowID
}

type serveHarness struct {
	baseURL  string
	stdout   *lockedBuffer
	stderr   *lockedBuffer
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
	stopErr  error
}

// startServe runs runServeContext on a real ephemeral port and waits until the
// listener is open.
func startServe(t *testing.T, args []string, deps runDeps) *serveHarness {
	t.Helper()
	stdout := &lockedBuffer{}
	stderr := &lockedBuffer{}
	deps.stdout = stdout
	deps.stderr = stderr

	var mu sync.Mutex
	var addr string
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			mu.Lock()
			addr = listener.Addr().String()
			mu.Unlock()
		}
		return listener, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServeContext(ctx, args, deps) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		resolved := addr
		mu.Unlock()
		if resolved != "" {
			harness := &serveHarness{
				baseURL: "http://" + resolved,
				stdout:  stdout,
				stderr:  stderr,
				cancel:  cancel,
				done:    done,
			}
			t.Cleanup(func() { harness.stop() })
			return harness
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("runServeContext returned before listening: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start listening")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// stop cancels the serve context and waits for runServeContext to return. It
// is safe to call twice: t.Cleanup always calls it, and tests that assert on
// the shutdown result call it explicitly first.
func (h *serveHarness) stop() error {
	h.stopOnce.Do(func() {
		h.cancel()
		select {
		case h.stopErr = <-h.done:
		case <-time.After(10 * time.Second):
			h.stopErr = errors.New("runServeContext did not return after cancellation")
		}
	})
	return h.stopErr
}

func (h *serveHarness) query(t *testing.T, query string, mutate ...func(*http.Request)) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, h.baseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for _, apply := range mutate {
		apply(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}
	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("body %q is not JSON: %v", raw, err)
		}
	}
	return response.StatusCode, payload
}

func flowTitles(t *testing.T, payload map[string]any) []string {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no data object: %#v", payload)
	}
	flows, ok := data["flows"].([]any)
	if !ok {
		t.Fatalf("data has no flows array: %#v", data)
	}
	titles := make([]string, 0, len(flows))
	for _, entry := range flows {
		titles = append(titles, entry.(map[string]any)["title"].(string))
	}
	return titles
}

func TestRunServeHelpPrintsUsageAndExamples(t *testing.T) {
	var stdout bytes.Buffer
	deps := serveDeps(t, t.TempDir(), nil)
	deps.stdout = &stdout
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("listen should not run for --help")
		return nil, nil
	}
	if err := runServeContext(context.Background(), []string{"approach", "serve", "--help"}, deps); err != nil {
		t.Fatalf("runServeContext() error = %v", err)
	}
	requireContainsAll(t, stdout.String(), []string{
		"Usage: approach serve [flags]",
		"--addr ADDRESS",
		"--token TOKEN",
		"--state-root PATH",
		"--scan-root PATH",
		"/healthz",
		"docs/graphql-api.md",
	})
}

func TestRunServeRejectsUnknownFlagAndArgument(t *testing.T) {
	deps := serveDeps(t, t.TempDir(), nil)
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("listen should not run for a bad invocation")
		return nil, nil
	}
	if err := runServeContext(context.Background(), []string{"approach", "serve", "--nope"}, deps); err == nil {
		t.Fatalf("runServeContext(--nope) error = nil, want a flag error")
	}
	err := runServeContext(context.Background(), []string{"approach", "serve", "extra"}, deps)
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "extra"`) {
		t.Fatalf("runServeContext(extra) error = %v, want an unexpected-argument error", err)
	}
}

func TestRunServeRejectsInvalidAddr(t *testing.T) {
	deps := serveDeps(t, t.TempDir(), nil)
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("listen should not run for an invalid address")
		return nil, nil
	}
	for _, addr := range []string{"127.0.0.1", "not-an-address", "127.0.0.1:"} {
		err := runServeContext(context.Background(), []string{"approach", "serve", "--addr", addr}, deps)
		if err == nil || !strings.Contains(err.Error(), "invalid bind address") {
			t.Errorf("runServeContext(--addr %q) error = %v, want an invalid-address error", addr, err)
		}
	}
}

func TestRunServeRequiresTokenForNonLoopbackBindBeforeListening(t *testing.T) {
	deps := serveDeps(t, t.TempDir(), nil)
	deps.loadConfig = func() (config.Config, error) {
		t.Fatal("config should not load before the token check")
		return config.Config{}, nil
	}
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("listener must not open without a token on a non-loopback bind")
		return nil, nil
	}
	for _, addr := range []string{"0.0.0.0:8787", ":8787", "192.168.1.5:8787"} {
		err := runServeContext(context.Background(), []string{"approach", "serve", "--addr", addr}, deps)
		if err == nil || !strings.Contains(err.Error(), "token is required") {
			t.Errorf("runServeContext(--addr %q) error = %v, want a token-required error", addr, err)
		}
	}
}

func TestRunServeAllowsLoopbackBindWithoutToken(t *testing.T) {
	root := t.TempDir()
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"}, serveDeps(t, root, nil))
	status, payload := harness.query(t, `{ flows { title } }`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
}

func TestRunServeAddrPrecedence(t *testing.T) {
	var requested []string
	deps := serveDeps(t, t.TempDir(), map[string]string{"APPROACH_API_ADDR": "127.0.0.1:9101"})
	deps.listen = func(network, address string) (net.Listener, error) {
		requested = append(requested, address)
		return nil, errors.New("listen refused by test")
	}

	if err := runServeContext(context.Background(), []string{"approach", "serve", "--addr", "127.0.0.1:9102"}, deps); err == nil {
		t.Fatalf("runServeContext() error = nil, want the injected listen error")
	}
	if err := runServeContext(context.Background(), []string{"approach", "serve"}, deps); err == nil {
		t.Fatalf("runServeContext() error = nil, want the injected listen error")
	}

	noEnv := serveDeps(t, t.TempDir(), nil)
	noEnv.listen = deps.listen
	if err := runServeContext(context.Background(), []string{"approach", "serve"}, noEnv); err == nil {
		t.Fatalf("runServeContext() error = nil, want the injected listen error")
	}

	want := []string{"127.0.0.1:9102", "127.0.0.1:9101", defaultServeAddr}
	if strings.Join(requested, ",") != strings.Join(want, ",") {
		t.Fatalf("listen addresses = %v, want %v", requested, want)
	}
}

func TestRunServeTokenPrecedence(t *testing.T) {
	root := t.TempDir()
	deps := serveDeps(t, root, map[string]string{"APPROACH_API_TOKEN": "env-token"})
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0", "--token", "flag-token"}, deps)

	status, _ := harness.query(t, `{ flows { title } }`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer flag-token")
	})
	if status != http.StatusOK {
		t.Errorf("flag token status = %d, want 200", status)
	}
	status, _ = harness.query(t, `{ flows { title } }`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer env-token")
	})
	if status != http.StatusUnauthorized {
		t.Errorf("env token status = %d, want 401 (--token wins)", status)
	}

	envOnly := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"}, serveDeps(t, root, map[string]string{
		"APPROACH_API_TOKEN": "env-token",
	}))
	status, _ = envOnly.query(t, `{ flows { title } }`, func(r *http.Request) {
		r.Header.Set("X-Approach-Token", "env-token")
	})
	if status != http.StatusOK {
		t.Errorf("env-only token status = %d, want 200", status)
	}
	status, _ = envOnly.query(t, `{ flows { title } }`)
	if status != http.StatusUnauthorized {
		t.Errorf("missing token status = %d, want 401", status)
	}
}

func TestRunServeStateRootPrecedence(t *testing.T) {
	flagRoot := t.TempDir()
	flowEnvRoot := t.TempDir()
	planEnvRoot := t.TempDir()
	sessionEnvRoot := t.TempDir()
	configRoot := t.TempDir()
	repoPath := t.TempDir()

	seedFlow(t, flagRoot, "from-flag", repoPath)
	seedFlow(t, flowEnvRoot, "from-flow-env", repoPath)
	seedFlow(t, planEnvRoot, "from-plan-env", repoPath)
	seedFlow(t, sessionEnvRoot, "from-session-env", repoPath)
	seedFlow(t, configRoot, "from-config", repoPath)

	fullEnv := map[string]string{
		"APPROACH_FLOW_STATE_ROOT":    flowEnvRoot,
		"APPROACH_PLAN_STATE_ROOT":    planEnvRoot,
		"APPROACH_SESSION_STATE_ROOT": sessionEnvRoot,
	}
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"flag wins", []string{"--state-root", flagRoot}, fullEnv, "from-flag"},
		{"flow env", nil, fullEnv, "from-flow-env"},
		{"plan env", nil, map[string]string{
			"APPROACH_PLAN_STATE_ROOT":    planEnvRoot,
			"APPROACH_SESSION_STATE_ROOT": sessionEnvRoot,
		}, "from-plan-env"},
		{"session env", nil, map[string]string{"APPROACH_SESSION_STATE_ROOT": sessionEnvRoot}, "from-session-env"},
		{"config", nil, nil, "from-config"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			args := append([]string{"approach", "serve", "--addr", "127.0.0.1:0"}, testCase.args...)
			harness := startServe(t, args, serveDeps(t, configRoot, testCase.env))
			status, payload := harness.query(t, `{ flows { title } }`)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%v)", status, payload)
			}
			titles := flowTitles(t, payload)
			if len(titles) != 1 || titles[0] != testCase.want {
				t.Fatalf("flows = %v, want [%s]", titles, testCase.want)
			}
		})
	}
}

func TestRunServeScanRootPrecedence(t *testing.T) {
	flagRoot := t.TempDir()
	envRoot := t.TempDir()
	configRoot := t.TempDir()

	var mu sync.Mutex
	var seen []scanner.ScanOptions
	run := func(args []string, env map[string]string, cfgRoot string) {
		deps := serveDeps(t, t.TempDir(), env)
		deps.loadConfig = func() (config.Config, error) {
			return config.Config{
				Sessions: config.SessionsConfig{Root: t.TempDir()},
				Scan:     config.ScanConfig{Root: cfgRoot, MaxDepth: 1},
			}, nil
		}
		deps.scan = func(opts scanner.ScanOptions) ([]scanner.Repo, error) {
			mu.Lock()
			seen = append(seen, opts)
			mu.Unlock()
			return nil, nil
		}
		harness := startServe(t, append([]string{"approach", "serve", "--addr", "127.0.0.1:0"}, args...), deps)
		if status, payload := harness.query(t, `{ repos { id } }`); status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (%v)", status, payload)
		}
	}

	run([]string{"--scan-root", flagRoot}, map[string]string{"WORKTREE_ROOT": envRoot}, configRoot)
	run(nil, map[string]string{"WORKTREE_ROOT": envRoot}, configRoot)
	run(nil, nil, configRoot)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("scan calls = %d, want 3", len(seen))
	}
	for index, want := range []string{flagRoot, envRoot, configRoot} {
		if seen[index].Root != want {
			t.Errorf("scan[%d].Root = %q, want %q", index, seen[index].Root, want)
		}
		if seen[index].MaxDepth != 1 {
			t.Errorf("scan[%d].MaxDepth = %d, want 1 (from [scan].max_depth)", index, seen[index].MaxDepth)
		}
	}
}

func TestNewServeHTTPServerTimeouts(t *testing.T) {
	server := newServeHTTPServer(http.NotFoundHandler())
	cases := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"ReadHeaderTimeout": {server.ReadHeaderTimeout, 5 * time.Second},
		"ReadTimeout":       {server.ReadTimeout, 15 * time.Second},
		"WriteTimeout":      {server.WriteTimeout, 30 * time.Second},
		"IdleTimeout":       {server.IdleTimeout, 60 * time.Second},
	}
	for name, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("%s = %s, want %s", name, testCase.got, testCase.want)
		}
	}
	if server.Handler == nil {
		t.Errorf("Handler = nil, want the GraphQL handler")
	}
}

func TestRunServePrintsResolvedAddress(t *testing.T) {
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"}, serveDeps(t, t.TempDir(), nil))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(harness.stdout.String(), "listening on") {
		if time.Now().After(deadline) {
			t.Fatalf("stdout = %q, want the resolved address", harness.stdout.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
	printed := harness.stdout.String()
	if strings.Contains(printed, "127.0.0.1:0") {
		t.Errorf("stdout printed the requested port, not the resolved one: %q", printed)
	}
	if !strings.Contains(printed, strings.TrimPrefix(harness.baseURL, "http://")) {
		t.Errorf("stdout = %q, want it to contain %q", printed, harness.baseURL)
	}
	if !strings.Contains(printed, "/graphql") {
		t.Errorf("stdout = %q, want the endpoint path", printed)
	}
}

func TestRunServeRoundTripAndShutdown(t *testing.T) {
	root := t.TempDir()
	repoPath := t.TempDir()
	flowID := seedFlow(t, root, "Round trip", repoPath)

	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0", "--state-root", root},
		serveDeps(t, t.TempDir(), nil))

	status, payload := harness.query(t, `{ flows { id title repo { id inScanRoot } currentPhase { id status } } }`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	flows := payload["data"].(map[string]any)["flows"].([]any)
	if len(flows) != 1 {
		t.Fatalf("flows = %v, want one record", flows)
	}
	flow := flows[0].(map[string]any)
	if flow["id"] != flowID {
		t.Errorf("flow.id = %v, want %q", flow["id"], flowID)
	}
	if repo := flow["repo"].(map[string]any); repo["id"] != repoPath {
		t.Errorf("flow.repo.id = %v, want %q", repo["id"], repoPath)
	}

	response, err := http.Get(harness.baseURL + "/healthz")
	if err != nil {
		t.Fatalf("healthz error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d, want 200", response.StatusCode)
	}

	if err := harness.stop(); err != nil {
		t.Fatalf("runServeContext() error = %v, want a clean return after cancellation", err)
	}
}

func TestRunServeTrimsConfiguredToken(t *testing.T) {
	// The token is compared against a trimmed header, so a padded env value
	// that stayed padded would 401 every request with no diagnostic anywhere.
	root := t.TempDir()
	harness := startServe(t, []string{"approach", "serve", "--addr", "127.0.0.1:0"},
		serveDeps(t, root, map[string]string{"APPROACH_API_TOKEN": "  padded-token\n"}))

	status, _ := harness.query(t, `{ flows { title } }`, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer padded-token")
	})
	if status != http.StatusOK {
		t.Errorf("padded env token status = %d, want 200", status)
	}
	status, _ = harness.query(t, `{ flows { title } }`)
	if status != http.StatusUnauthorized {
		t.Errorf("missing token status = %d, want 401 (a padded token must still be a token)", status)
	}
}
