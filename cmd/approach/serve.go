package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/graphqlapi"
	"github.com/approachcontrol/approach/scanner"
)

const defaultServeAddr = "127.0.0.1:8787"

const serveShutdownTimeout = 10 * time.Second

// runServe handles `approach serve`. It installs a signal context and hands
// off; tests drive runServeContext directly with a cancellable context so no
// signals are sent to the test process.
func runServe(args []string, deps runDeps) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runServeContext(ctx, args, deps)
}

// runServeContext serves the read-only GraphQL API until ctx is cancelled. It
// returns only after http.Server.Shutdown has completed.
func runServeContext(ctx context.Context, args []string, deps runDeps) error {
	if len(args) == 3 && isHelpArg(args[2]) {
		printServeHelp(deps.stdout)
		return nil
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printServeHelp(deps.stdout) }
	addrFlag := flags.String("addr", "", "bind address (default 127.0.0.1:8787)")
	tokenFlag := flags.String("token", "", "shared API token")
	stateRoot := flags.String("state-root", "", "artifact state root")
	scanRootFlag := flags.String("scan-root", "", "repository scan root")
	if handled, err := parseCommandFlags(flags, args[2:]); handled || err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q\n\n%s", flags.Arg(0), serveHelpText)
	}

	addr := firstNonEmpty(*addrFlag, deps.getenv("APPROACH_API_ADDR"), defaultServeAddr)
	token := firstNonEmpty(*tokenFlag, deps.getenv("APPROACH_API_TOKEN"))
	loopback, err := isLoopbackBindAddr(addr)
	if err != nil {
		return err
	}
	// Fail before the listener opens: an unauthenticated listener on a
	// non-loopback interface must never exist, even briefly.
	if !loopback && token == "" {
		return fmt.Errorf("a token is required for the non-loopback bind address %q; pass --token or set APPROACH_API_TOKEN", addr)
	}

	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	// Presets must reach the store: they are consulted on the read path to
	// restore missing depends_on edges, so a store built without them would
	// report different dependsOn, status, and currentPhase than the CLI.
	store, err := newFlowStoreWithConfig(*stateRoot, cfg, deps)
	if err != nil {
		return fmt.Errorf("error opening flow store: %w", err)
	}

	scanRoot, err := scanner.ResolveRoot(firstNonEmpty(*scanRootFlag, deps.getenv("WORKTREE_ROOT"), cfg.Scan.Root))
	if err != nil {
		return fmt.Errorf("error resolving scan root: %w", err)
	}
	scanOptions := scanner.ScanOptions{Root: scanRoot, MaxDepth: cfg.Scan.MaxDepth}

	handler, err := graphqlapi.NewServer(graphqlapi.ServerOptions{
		Repos: func() ([]scanner.Repo, error) { return deps.scan(scanOptions) },
		Flows: func() ([]flowstore.FlowRecord, error) { return store.List(flowstore.FlowFilter{}) },
		Token: token,
		// The Logger seam keeps request logging off stdout, where the
		// resolved address is printed for scripts to read.
		Logger: deps.stderr,
	})
	if err != nil {
		return err
	}

	listener, err := deps.listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("error listening on %s: %w", addr, err)
	}
	fmt.Fprintf(deps.stdout, "approach serve listening on http://%s%s\n", listener.Addr().String(), graphqlapi.GraphQLPath)

	return serveUntilDone(ctx, newServeHTTPServer(handler), listener)
}

// newServeHTTPServer applies the transport timeouts. It is separated from
// listener creation so the durations are assertable without opening a port.
func newServeHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func serveUntilDone(ctx context.Context, server *http.Server, listener net.Listener) error {
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}

// isLoopbackBindAddr reports whether addr binds only the loopback interface.
// A wildcard host (":8787") is not loopback.
func isLoopbackBindAddr(addr string) (bool, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("invalid bind address %q: %w", addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return false, fmt.Errorf("invalid bind address %q: missing port", addr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false, nil
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, nil
	}
	return ip.IsLoopback(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func printServeHelp(w io.Writer) {
	io.WriteString(w, serveHelpText)
}

const serveHelpText = `Usage: approach serve [flags]

Serve the read-only GraphQL API over HTTP. POST queries to /graphql; GET
/healthz for liveness. No mutations exist in the schema.

Flags:
  --addr ADDRESS     Bind address (default 127.0.0.1:8787, or APPROACH_API_ADDR).
                     Use 127.0.0.1:0 to pick a free port; the resolved address
                     is printed on startup.
  --token TOKEN      Shared token (or APPROACH_API_TOKEN). Optional on a
                     loopback bind, required on any other bind address.
  --state-root PATH  Override the artifact state root.
  --scan-root PATH   Override the repository scan root (or WORKTREE_ROOT).
  --help, -h         Print this help and exit.

Examples:
  approach serve
  approach serve --addr 127.0.0.1:0
  APPROACH_API_TOKEN=secret approach serve --addr 0.0.0.0:8787
  curl -s -H 'Content-Type: application/json' \
    -d '{"query":"{ repos { id displayName } }"}' http://127.0.0.1:8787/graphql

See docs/graphql-api.md for the schema, the HTTP status contract, and the
hardening rules.
`
