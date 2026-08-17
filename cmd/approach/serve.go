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
		return fmt.Errorf("a token is required for the non-loopback bind address %q; set APPROACH_API_TOKEN (preferred: --token puts the token in argv, where any local account can read it)", addr)
	}

	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	// Presets must reach the store: they are consulted on the read path to
	// restore missing depends_on edges, so a store built without them would
	// report different dependsOn, status, and currentPhase than the CLI.
	// Read-only by role as well as by construction: serve only ever calls List
	// and ReadEpicProgression, and it must not migrate, discard a staged
	// database, or tighten the mode of a state root it merely reads.
	store, err := newFlowStoreWithConfig(*stateRoot, cfg, deps, flowstore.RoleReader)
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
		// Read-only by construction: the store's read API is the only epic
		// progression seam the API gets, so serving can observe progression
		// state but never enable, disable, or halt it.
		EpicProgressions: store.ReadEpicProgression,
		Token:            token,
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
	// The check above classified a *string*. A hostname resolves through
	// /etc/hosts and the configured resolver, so "localhost" is a claim about a
	// name, not about an interface: a resolver that maps it to a LAN address
	// would hand out an unauthenticated listener there, and the handler's own
	// allowlist accepts `Host: localhost` from anywhere. Verify what actually
	// got bound, and close it before serving a single byte if it is not
	// loopback after all.
	bound := listener.Addr()
	if token == "" && !isLoopbackAddr(bound) {
		listener.Close()
		return fmt.Errorf("a token is required: bind address %q resolved to the non-loopback listen address %q; set APPROACH_API_TOKEN", addr, bound)
	}
	fmt.Fprintf(deps.stdout, "approach serve listening on http://%s%s\n", bound.String(), graphqlapi.GraphQLPath)
	if !isLoopbackAddr(bound) {
		// The token gates access but does not protect the wire: this server
		// speaks plaintext HTTP and has no TLS configuration, so a bind that
		// leaves the machine puts the token and every Flow record in front of
		// anyone on the path. Say so once, where the operator will see it.
		fmt.Fprintf(deps.stderr, "approach serve: warning: %s is not loopback and this server speaks plaintext HTTP. "+
			"The API token and every Flow record cross the network in the clear. "+
			"Front it with TLS — a tunnel or a reverse proxy — rather than exposing this port directly.\n", bound)
	}

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

// isLoopbackAddr reports whether a listener really bound the loopback
// interface. It is the check that decides whether a token-free server is
// allowed to serve, because it looks at the resolved address rather than at
// the string the operator typed.
func isLoopbackAddr(addr net.Addr) bool {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP != nil && tcp.IP.IsLoopback()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackBindAddr reports whether addr binds only the loopback interface.
// A wildcard host (":8787") is not loopback. It runs before the listener
// opens, so a non-loopback *literal* never gets bound unauthenticated even
// briefly; a hostname is only settled afterwards, by isLoopbackAddr.
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

// firstNonEmpty returns the first value with non-space content, trimmed.
// Trimming the returned value matters: the token is compared against a trimmed
// header, so a padded APPROACH_API_TOKEN would otherwise 401 every request
// with no diagnostic.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
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
  --token TOKEN      Shared token. Optional on a loopback bind, required on any
                     other bind address. Prefer APPROACH_API_TOKEN: a flag puts
                     the token in argv and in shell history, where any local
                     account can read it.
  --state-root PATH  Override the artifact state root.
  --scan-root PATH   Override the repository scan root (or WORKTREE_ROOT).
  --help, -h         Print this help and exit.

There is no TLS: this server speaks plaintext HTTP. A non-loopback bind puts
the token and every Flow record on the wire in the clear, so front it with a
tunnel or reverse proxy rather than exposing the port directly. A loopback bind
is machine-local, not user-local — set a token on a shared machine.

Examples:
  approach serve
  approach serve --addr 127.0.0.1:0
  APPROACH_API_TOKEN=secret approach serve --addr 0.0.0.0:8787
  curl -s -H 'Content-Type: application/json' \
    -d '{"query":"{ repos { id displayName } }"}' http://127.0.0.1:8787/graphql

See docs/graphql-api.md for the schema, the HTTP status contract, and the
hardening rules.
`
