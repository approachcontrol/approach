package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/controlplane"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/internal/launchcontrol"
	"github.com/approachcontrol/approach/internal/version"
	"github.com/approachcontrol/approach/model"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
)

func main() {
	if err := run(os.Args, runDeps{}); err != nil {
		var processExit flowlease.ProcessExitError
		if errors.As(err, &processExit) {
			os.Exit(processExit.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type runDeps struct {
	loadConfig              func() (config.Config, error)
	getenv                  func(string) string
	getwd                   func() (string, error)
	scan                    func(scanner.ScanOptions) ([]scanner.Repo, error)
	startProgram            func([]scanner.Repo, config.Config) error
	startProgramWithOptions func([]scanner.Repo, startProgramOptions) error
	stdin                   io.Reader
	stdout                  io.Writer
	stderr                  io.Writer
	listen                  func(network, address string) (net.Listener, error)
}

type startProgramOptions struct {
	Config         config.Config
	ScanRepos      func() ([]scanner.Repo, error)
	RepoCreateRoot string
	// AllowDevLiveMigration acknowledges that this development build may advance
	// the schema of the database a released build owns. It is off by default and
	// has no effect on a release build or on any root but the release default.
	AllowDevLiveMigration bool
	// LaunchSource is the running binary, hashed before the repository scan so
	// an upgrade during that scan cannot make the pin name a different build. A
	// zero value means "capture it now", which is what a test constructing this
	// struct directly gets.
	LaunchSource controlplane.SourceIdentity
}

func run(args []string, deps runDeps) error {
	deps = fillRunDeps(deps)
	if len(args) > 1 && args[1] == flowlease.TmuxSpawnCommand {
		return flowlease.RunTmuxSpawn(args[2:], deps.stderr)
	}
	if len(args) > 1 && args[1] == flowlease.LeaseRunCommand {
		return flowlease.RunLeaseRunner(args[2:], deps.stdin, deps.stdout, deps.stderr, func(exit flowlease.LaunchExit) {
			// exit.json is the sweep's authoritative exit evidence for a
			// tracked tmux launch. Best effort: the runner's job is the lease
			// and the exit status, and a failed write must not change either.
			if err := launchcontrol.RecordLaunchExit(exit.Root, exit.FlowID, exit.PhaseID, exit.LaunchID, exit.Code, exit.Signaled, exit.EndedAt); err != nil {
				fmt.Fprintf(deps.stderr, "approach: record launch exit: %v\n", err)
			}
		})
	}
	if len(args) == 2 && isHelpArg(args[1]) {
		printMainHelp(deps.stdout)
		return nil
	}
	if len(args) > 1 && args[1] == "session-hook" {
		return runSessionHook(args, deps)
	}
	if len(args) > 1 && args[1] == "plan" {
		return runPlan(args, deps)
	}
	if len(args) > 1 && args[1] == "flow" {
		return runFlow(args, deps)
	}
	if len(args) > 1 && args[1] == "serve" {
		return runServe(args, deps)
	}
	if len(args) > 1 && args[1] == "db" {
		return runDB(args, deps)
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	versionFlag := flags.Bool("version", false, "print version and exit")
	flags.BoolVar(versionFlag, "v", false, "print version and exit")
	allowDevLiveMigration := flags.Bool("allow-dev-live-migration", false,
		"let this development build migrate the release artifact root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return unknownCommandError(flags.Arg(0), []string{"plan", "flow", "serve", "db", "session-hook"}, mainHelpText)
	}

	if *versionFlag {
		fmt.Fprintln(deps.stdout, version.String())
		return nil
	}

	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	root := cfg.Scan.Root
	if envRoot := deps.getenv("WORKTREE_ROOT"); envRoot != "" {
		root = envRoot
	}
	repoCreateRoot, err := scanner.ResolveRoot(root)
	if err != nil {
		return fmt.Errorf("error resolving scan root: %w", err)
	}

	// Before the scan, deliberately. Resolve used to hash the running binary
	// once the state root was known, which put a full repository walk inside the
	// window where a `brew upgrade` could replace the file the pin then named —
	// and the pin would still carry THIS process's version and schema, so the
	// mismatch would be silent. Hashing needs no root, so it does not have to
	// wait for one. The copy is materialized later, in startProgram, after the
	// Flow store's dev-root guard has had its say about that root.
	launchSource, err := controlplane.CaptureSource()
	if err != nil {
		return err
	}

	repos, err := deps.scan(scanner.ScanOptions{
		Root:     root,
		MaxDepth: cfg.Scan.MaxDepth,
	})
	if err != nil {
		return fmt.Errorf("error scanning repos: %w", err)
	}

	scanOptions := scanner.ScanOptions{
		Root:     root,
		MaxDepth: cfg.Scan.MaxDepth,
	}
	if err := deps.startProgramWithOptions(repos, startProgramOptions{
		Config:                cfg,
		RepoCreateRoot:        repoCreateRoot,
		LaunchSource:          launchSource,
		AllowDevLiveMigration: *allowDevLiveMigration || truthyEnv(deps.getenv("APPROACH_ALLOW_DEV_LIVE_MIGRATION")),
		ScanRepos: func() ([]scanner.Repo, error) {
			return deps.scan(scanOptions)
		},
	}); err != nil {
		return fmt.Errorf("error: %w", err)
	}
	return nil
}

// truthyEnv accepts the spellings a shell script is likely to use for an
// acknowledgement that must be given deliberately. Anything else, including an
// unset variable, means "not acknowledged".
func truthyEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func printMainHelp(w io.Writer) {
	io.WriteString(w, mainHelpText)
}

const mainHelpText = `Usage: approach [--version] [command]

Launch the worktree TUI, or use a command to persist agent artifacts.

Commands:
  plan          Save, list, read, and update saved plans.
  flow          Create, inspect, and update Flow records.
  serve         Serve the read-only GraphQL API over HTTP.
  db            Inspect and migrate the flow database.
  session-hook  Capture Claude, Codex, or Cursor session hook payloads.

Flags:
  --version, -v  Print version and exit.
  --help, -h     Print this help and exit.
  --allow-dev-live-migration
                 Let this development build migrate the release artifact root
                 (also APPROACH_ALLOW_DEV_LIVE_MIGRATION=1). A development build
                 otherwise defaults to its own root and refuses to advance the
                 schema of the database a released approach owns.

Examples:
  approach
  approach plan --help
  approach flow --help
  approach serve --help
  approach db inspect --json
  approach session-hook --provider codex
`

func unknownCommandError(got string, valid []string, usage string) error {
	if suggestion := nearestCommand(got, valid); suggestion != "" {
		return fmt.Errorf("unknown command %q; did you mean %q?\n\n%s", got, suggestion, usage)
	}
	return fmt.Errorf("unknown command %q\n\n%s", got, usage)
}

func nearestCommand(got string, valid []string) string {
	best := ""
	bestDistance := 3
	for _, candidate := range valid {
		distance := editDistance(got, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				curr[j-1]+1,
				prev[j]+1,
				prev[j-1]+cost,
			)
		}
		prev = curr
	}
	return prev[len(b)]
}

func minInt(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func parseCommandFlags(flags *flag.FlagSet, args []string) (bool, error) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func fillRunDeps(deps runDeps) runDeps {
	if deps.loadConfig == nil {
		deps.loadConfig = func() (config.Config, error) {
			return config.Load()
		}
	}
	if deps.getenv == nil {
		deps.getenv = os.Getenv
	}
	if deps.getwd == nil {
		deps.getwd = os.Getwd
	}
	if deps.scan == nil {
		deps.scan = scanner.Scan
	}
	if deps.startProgramWithOptions == nil {
		if deps.startProgram != nil {
			deps.startProgramWithOptions = func(repos []scanner.Repo, opts startProgramOptions) error {
				return deps.startProgram(repos, opts.Config)
			}
		} else {
			deps.startProgramWithOptions = startProgram
		}
	}
	if deps.stdin == nil {
		deps.stdin = os.Stdin
	}
	if deps.stdout == nil {
		deps.stdout = os.Stdout
	}
	if deps.stderr == nil {
		deps.stderr = os.Stderr
	}
	if deps.listen == nil {
		deps.listen = net.Listen
	}
	return deps
}

func runSessionHook(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("session-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	providerFlag := flags.String("provider", "", "session provider")
	stateRoot := flags.String("state-root", "", "session state root")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	provider := sessions.Provider(*providerFlag)
	switch provider {
	case sessions.ProviderClaude, sessions.ProviderCodex, sessions.ProviderCursor:
	default:
		return fmt.Errorf("unsupported session provider %q", *providerFlag)
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	root := *stateRoot
	explicitRoot := root != ""
	if root == "" {
		if envRoot := deps.getenv("APPROACH_SESSION_STATE_ROOT"); envRoot != "" {
			root, explicitRoot = envRoot, true
		}
	}
	if root == "" {
		root = cfg.Sessions.Root
	}
	result, err := sessions.IngestHookWithWarnings(provider, deps.stdin, sessions.IngestOptions{
		StateRoot:          root,
		StateRootExplicit:  explicitRoot,
		CopyRawTranscripts: cfg.Sessions.CopyRawTranscripts,
		FlowPresets:        cfg.Flow.Presets,
		Env: map[string]string{
			"HOME":                        deps.getenv("HOME"),
			"CODEX_HOME":                  deps.getenv("CODEX_HOME"),
			"CLAUDE_CONFIG_DIR":           deps.getenv("CLAUDE_CONFIG_DIR"),
			"CURSOR_TRANSCRIPT_PATH":      deps.getenv("CURSOR_TRANSCRIPT_PATH"),
			"APPROACH_LAUNCH_ID":          deps.getenv("APPROACH_LAUNCH_ID"),
			"APPROACH_REPO_PATH":          deps.getenv("APPROACH_REPO_PATH"),
			"APPROACH_WORKTREE_PATH":      deps.getenv("APPROACH_WORKTREE_PATH"),
			"APPROACH_PLAN_ID":            deps.getenv("APPROACH_PLAN_ID"),
			"APPROACH_PLAN_PATH":          deps.getenv("APPROACH_PLAN_PATH"),
			"APPROACH_PLAN_STATE_ROOT":    deps.getenv("APPROACH_PLAN_STATE_ROOT"),
			"APPROACH_FLOW_ID":            deps.getenv("APPROACH_FLOW_ID"),
			"APPROACH_FLOW_PHASE_ID":      deps.getenv("APPROACH_FLOW_PHASE_ID"),
			"APPROACH_FLOW_STATE_ROOT":    deps.getenv("APPROACH_FLOW_STATE_ROOT"),
			"APPROACH_BRANCH":             deps.getenv("APPROACH_BRANCH"),
			"APPROACH_COMMIT":             deps.getenv("APPROACH_COMMIT"),
			"APPROACH_SESSION_STATE_ROOT": deps.getenv("APPROACH_SESSION_STATE_ROOT"),
		},
	})
	// Keep-alive, never release. This hook is the only signal a detached agent
	// emits — the TUI releases claims in FinalizeAgentSession but deliberately
	// skips that for detached launches — but "a hook fired" does not mean "the
	// agent is done", and releasing on a hook that is not end-of-life would
	// unpin a live agent still bound to the path baked into its argv. Codex
	// wires Stop, which fires once per TURN. Claude wires SessionEnd, which also
	// fires on /clear while the process keeps running. Neither is a reliable
	// death certificate, and a provider added later would be one more guess.
	//
	// So what the hook actually attests is "this launch was alive just now",
	// which is exactly what a claim's freshness should track: it restamps the
	// claim, and retirement is left to FinalizeAgentSession or to expiry. The
	// cost is that a detached launch holds its digest until pinClaimMaxAge after
	// its last sign of life; that is bounded disk, against the unbounded
	// alternative of evicting a binary a running agent still has to exec.
	//
	// Best effort and after ingest: the session record is what this command
	// exists to write, and retention hygiene may never cost one.
	if launchID := deps.getenv("APPROACH_LAUNCH_ID"); launchID != "" && root != "" {
		_ = controlplane.RefreshPin(root, launchID)
	}
	// Warnings on stderr, exit code unchanged. agent-skills/approach-flow tells
	// every agent that a non-zero exit from an `approach` command is a
	// persistence failure, and a schema-compatibility notice is not one — the
	// session record this command exists to write was still captured.
	for _, warning := range result.Warnings {
		fmt.Fprintf(deps.stderr, "approach: %s\n", warning)
	}
	return err
}

func startProgram(repos []scanner.Repo, opts startProgramOptions) error {
	cfg := opts.Config
	artifactRoot := runtimeArtifactRoot(cfg)
	sessionStore, err := sessions.NewStore(sessions.StoreOptions{
		Root:               artifactRoot,
		CopyRawTranscripts: cfg.Sessions.CopyRawTranscripts,
	})
	if err != nil {
		return err
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: sessionStore.Root()})
	if err != nil {
		return err
	}
	// The owners lease is taken BEFORE the store opens and kept for the whole
	// process, and the store is told to exclude it. Not released and
	// reacquired around the migration: that gap is exactly as racy as the
	// shared-to-exclusive upgrade it would be imitating, and a concurrent
	// `approach db migrate` could take the lease inside it and migrate
	// underneath the handle this process never closes.
	ownerLease := acquireDatabaseOwnerLease(sessionStore.Root())
	// The one migrator in the process. TUI startup and `approach db migrate`
	// are the only surfaces that may advance the schema.
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:                  sessionStore.Root(),
		Role:                  flowstore.RoleMigrator,
		Presets:               cfg.Flow.Presets,
		AllowDevLiveMigration: opts.AllowDevLiveMigration,
		OwnerNonce:            ownerLease.Nonce(),
	})
	if err != nil {
		// Released on the failure path only. On the success path the store
		// stays open for the process lifetime (see the note below), so the
		// lease has to outlive this function exactly as the store does.
		_ = ownerLease.Release()
		return err
	}
	// One store for the whole process: it is handed to the model below so the
	// TUI's Flow mutators write through this same pool.
	//
	// Deliberately NOT closed after p.Run(). Bubble Tea does not wait for command
	// goroutines — it cannot cancel them, so it leaks them — and main returns
	// immediately after this, so a Flow mutation still in flight at quit races
	// process exit either way. Closing does not make it land; it only converts
	// that race into a guaranteed loss ("sql: database is closed", with the
	// failure message delivered to a dead program). What closing would buy is an
	// exit-time WAL checkpoint on a process that is exiting anyway, and the WAL is
	// durable and recovered on the next open. Genuinely fixing the last-write
	// window means draining in-flight mutations before returning, not closing.
	// Materialize the pin only now, after the Flow store opened. The binary cache
	// lives in this root and writing it is a mutation of it, so an unacknowledged
	// development build pointed at the release default has to fail on the store's
	// dev-root guard BEFORE it copies a dev binary in and runs cache retention
	// there — a command that reports it refused to touch the release database
	// must not have left a dev executable behind on the way to saying so.
	//
	// The binary's identity was captured back in run(), before the repository
	// scan, precisely so this ordering costs nothing: os.Executable returns a
	// mutable pathname, and hashing late would put every step above inside the
	// window where an upgrade could swap the file the pin names. Nothing can make
	// that check atomic with process start — Go offers no portable handle on the
	// running image — so the capture happens as early as possible and the copy as
	// late as it is safe to.
	//
	// A cache problem degrades to the running binary and says so through the
	// notice below. A mid-startup replacement is not a cache problem: the
	// captured digest no longer describes the file at the source path, so a
	// degraded pin would fail every Verify. Restart is the only consistent pin.
	launchSource := opts.LaunchSource
	if strings.TrimSpace(launchSource.Path) == "" {
		if launchSource, err = controlplane.CaptureSource(); err != nil {
			return err
		}
	}
	pin := controlplane.Materialize(sessionStore.Root(), launchSource, flowstore.DatabaseSchemaVersion())
	if pin.SourceChanged {
		return fmt.Errorf("%s", pin.Notice)
	}

	// The launch controller owns this root's launch directories and, when it
	// can bind the root's socket, serves the agents' `approach flow` writes
	// through the one store above. Recovery — replaying what earlier launches
	// spooled and reconciling launches that exited without a result — runs
	// before the first render; its failures are logged, never fatal, because a
	// broken launch directory must not take the TUI down. A live listener on
	// the socket means another TUI owns this root: this process runs without an
	// endpoint and its launches take the direct path.
	controller, err := launchcontrol.New(launchcontrol.Options{
		Root:     sessionStore.Root(),
		Store:    flowStore,
		Liveness: sessionLivenessProbe(sessionStore),
		Log:      os.Stderr,
	})
	if err != nil {
		return err
	}
	if _, err := controller.Recover(); err != nil {
		fmt.Fprintf(os.Stderr, "approach: launch control recovery: %v\n", err)
	}
	if err := controller.Listen(); err != nil {
		if !errors.Is(err, launchcontrol.ErrEndpointOwned) && !errors.Is(err, launchcontrol.ErrNoSocketPath) {
			fmt.Fprintf(os.Stderr, "approach: launch control endpoint unavailable: %v\n", err)
		}
	}
	defer func() { _ = controller.Close() }()

	modelOpts := modelOptionsFromConfig(cfg, opts.ScanRepos, sessionStore, planStore, flowStore)
	modelOpts.RepoCreateRoot = opts.RepoCreateRoot
	modelOpts.LaunchPin = pin
	modelOpts.LaunchPinNotice = controlplane.PathMismatchNotice(pin, nil)
	modelOpts.LaunchControl = controller
	modelOpts.ReconcileLaunchExit = func(flowID, phaseID, launchID string, ev launchcontrol.ExitEvidence) error {
		_, err := controller.Reconcile(flowID, phaseID, launchID, ev)
		return err
	}
	modelOpts.SweepLaunches = func() { controller.Sweep() }
	// Bubble Tea normally exits immediately on SIGINT/SIGTERM without waiting
	// for command goroutines. Let the model defer those messages while its
	// private tmux helper still carries an authoritative Flow reservation.
	p := tea.NewProgram(
		model.NewWithOptions(repos, modelOpts),
		tea.WithAltScreen(),
		tea.WithFilter(model.DeferQuitDuringFlowLaunch),
	)
	controller.SetAppliedNotifier(func(event launchcontrol.AppliedEvent) {
		p.Send(model.FlowControlAppliedMsg{FlowID: event.FlowID, PhaseID: event.PhaseID, LaunchID: event.LaunchID})
	})
	_, err = p.Run()
	_ = controlplane.ReleaseProcessPin(sessionStore.Root())
	return err
}

// sessionLivenessProbe answers the launch controller's "did this launch's
// agent process end" from the session store. Status is authoritative over
// EndedAt; several records for one launch count as ended only when all of
// them are, and the launch's end is its latest record's end.
//
// An `ended` record is exit evidence only when the provider's hook means the
// process ended. Codex records `ended` after every turn (its Stop hook fires
// while the CLI stays open) and Cursor's stop hook is the same shape, so for
// those providers an ended record is a turn boundary: the launch is known but
// never reported ended, and its phase is left to authoritative evidence — the
// embedded terminal's exit or the lease runner's exit.json. Claude's
// SessionEnd-backed records count, except a `/clear`: that ends the session
// with the process alive on a new one that has no record until it ends, so a
// launch whose latest end is a `clear` is continued, not ended.
func sessionLivenessProbe(store *sessions.Store) launchcontrol.LivenessProbe {
	return func(launchID string) (launchcontrol.LaunchLiveness, error) {
		records, err := store.List(sessions.SessionFilter{})
		if err != nil {
			return launchcontrol.LaunchLiveness{}, err
		}
		liveness := launchcontrol.LaunchLiveness{Ended: true}
		var latest sessions.SessionRecord
		for _, record := range records {
			if strings.TrimSpace(record.LaunchID) != launchID {
				continue
			}
			liveness.RecordKnown = true
			if sessions.IsActive(record.Status, record.EndedAt) || !sessionEndIsProcessEnd(record.Provider) {
				liveness.Ended = false
				continue
			}
			if record.EndedAt.After(latest.EndedAt) {
				latest = record
			}
		}
		if !liveness.RecordKnown || sessionEndContinues(latest) {
			liveness.Ended = false
		}
		if liveness.Ended {
			liveness.EndedAt = latest.EndedAt
		}
		return liveness, nil
	}
}

// sessionEndIsProcessEnd reports whether an `ended` session record from
// provider can mean its agent process ended rather than a turn.
func sessionEndIsProcessEnd(provider sessions.Provider) bool {
	return provider == sessions.ProviderClaude
}

// sessionEndContinues reports an ended record whose end left the agent
// process alive: Claude's `/clear`.
func sessionEndContinues(record sessions.SessionRecord) bool {
	return record.Provider == sessions.ProviderClaude && strings.TrimSpace(record.EndReason) == "clear"
}

func modelOptionsFromConfig(cfg config.Config, scanRepos func() ([]scanner.Repo, error), sessionStore *sessions.Store, planStore *planstore.Store, flowStore *flowstore.Store) model.Options {
	launchOpts := actions.LaunchOptions{TerminalCommand: cfg.Terminal.Command}
	flowPreset, _ := resolveConfiguredFlowPreset(cfg, "")
	return model.Options{
		AgentCommand:          cfg.Agent.Command,
		CodexModel:            cfg.Agent.CodexModel,
		ClaudeModel:           cfg.Agent.ClaudeModel,
		CursorModel:           cfg.Agent.CursorModel,
		CodexReasoningEffort:  cfg.Agent.CodexReasoningEffort,
		ClaudeReasoningEffort: cfg.Agent.ClaudeReasoningEffort,
		PlanPromptTemplate:    cfg.Agent.PlanPrompt,
		FlowPresets:           cfg.Flow.Presets,
		FlowPreset:            flowPreset,
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan:           cfg.FlowPrompts.Plan,
			PlanReview:     cfg.FlowPrompts.PlanReview,
			Implementation: cfg.FlowPrompts.Implementation,
			ReviewLoop:     cfg.FlowPrompts.ReviewLoop,
			PRCreation:     cfg.FlowPrompts.PRCreation,
			Autoreview:     cfg.FlowPrompts.Autoreview,
			Merge:          cfg.FlowPrompts.Merge,
			Generic:        cfg.FlowPrompts.Generic,
			Autofix:        cfg.FlowPrompts.Autofix,
		},
		ScanRepos:        scanRepos,
		SessionStateRoot: sessionStore.Root(),
		ListSessions:     sessionStore.List,
		ReadSession:      sessionStore.Read,
		ReadTranscript:   sessionStore.ReadTranscript,
		ListPlans:        planStore.List,
		ListFlows:        flowStore.List,
		FlowStore:        flowStore,
		ReadPlan:         planStore.ReadPlan,
		LaunchTerminal: func(path string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchWithOptions(path, launchOpts)
		},
		LaunchDetachedTerminal: func(targetShellCommand, cwd string) (actions.TerminalLaunchSpec, error) {
			return actions.DetachedTerminalLaunch(targetShellCommand, cwd, launchOpts)
		},
		EditFile: func(path string) (actions.TerminalLaunchSpec, error) {
			return actions.EditFileWithOptions(path, actions.EditorOptions{EditorCommand: cfg.Editor.Command})
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.AgentLaunchWithOptions(ctx, launchOpts)
		},
		LaunchBackend: cfg.Launch.Backend,
		FinalizeAgentSession: func(ctx actions.AgentLaunchContext) error {
			endedAt := time.Now().UTC()
			// The launch is over, so its claim on a cached binary is too. Best
			// effort: an orphaned claim only costs one retained copy, while
			// failing finalization over it would lose the session record.
			_ = controlplane.ReleasePin(sessionStore.Root(), ctx.LaunchID)
			if err := sessionStore.MarkLaunchEnded(ctx.LaunchID, endedAt); err != nil {
				return err
			}
			if ctx.FlowID == "" || ctx.FlowPhaseID == "" {
				return nil
			}
			_, err := flowStore.MarkPhaseLaunchEnded(flowstore.PhaseLaunchEndUpdate{
				FlowID:   ctx.FlowID,
				PhaseID:  ctx.FlowPhaseID,
				LaunchID: ctx.LaunchID,
				EndedAt:  endedAt,
			})
			return err
		},
		BootstrapHookForRepo: bootstrapHookResolver(cfg),
		RunBootstrapHook:     actions.RunBootstrapHook,
		SaveAgentCommand: func(command string) error {
			return config.SaveAgentCommand(command)
		},
		SaveAgentReasoningEffort: func(command, effort string) error {
			return config.SaveAgentReasoningEffort(command, effort)
		},
		SaveAgentModel: func(command, model string) error {
			return config.SaveAgentModel(command, model)
		},
		SavePromptTemplate: func(section, key, value string) error {
			return config.SavePromptTemplate(section, key, value)
		},
		ResetPromptTemplate: func(section, key string) error {
			return config.ResetPromptTemplate(section, key)
		},
	}
}

func runtimeArtifactRoot(cfg config.Config) string {
	if envRoot := os.Getenv("APPROACH_FLOW_STATE_ROOT"); envRoot != "" {
		return envRoot
	}
	if envRoot := os.Getenv("APPROACH_PLAN_STATE_ROOT"); envRoot != "" {
		return envRoot
	}
	if envRoot := os.Getenv("APPROACH_SESSION_STATE_ROOT"); envRoot != "" {
		return envRoot
	}
	return cfg.Sessions.Root
}

func bootstrapHookResolver(cfg config.Config) func(string) (actions.BootstrapHook, bool) {
	hooks := make(map[string]actions.BootstrapHook, len(cfg.Bootstrap.Hooks))
	for _, hook := range cfg.Bootstrap.Hooks {
		timeout := hook.TimeoutSeconds
		if timeout == 0 {
			timeout = cfg.Bootstrap.TimeoutSeconds
		}
		if timeout == 0 {
			timeout = 120
		}
		hooks[filepath.Clean(hook.RepoPath)] = actions.BootstrapHook{
			Script:         hook.Script,
			TimeoutSeconds: timeout,
		}
	}
	return func(repoPath string) (actions.BootstrapHook, bool) {
		hook, ok := hooks[filepath.Clean(repoPath)]
		return hook, ok
	}
}
