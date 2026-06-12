package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/internal/version"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/planstore"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
	"github.com/brian-bell/wtui/ui"
)

func main() {
	if err := run(os.Args, runDeps{}); err != nil {
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
}

type startProgramOptions struct {
	Config    config.Config
	ScanRepos func() ([]scanner.Repo, error)
}

func run(args []string, deps runDeps) error {
	deps = fillRunDeps(deps)
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

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	versionFlag := flags.Bool("version", false, "print version and exit")
	flags.BoolVar(versionFlag, "v", false, "print version and exit")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return unknownCommandError(flags.Arg(0), []string{"plan", "flow", "session-hook"}, mainHelpText)
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
		Config: cfg,
		ScanRepos: func() ([]scanner.Repo, error) {
			return deps.scan(scanOptions)
		},
	}); err != nil {
		return fmt.Errorf("error: %w", err)
	}
	return nil
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func printMainHelp(w io.Writer) {
	io.WriteString(w, mainHelpText)
}

const mainHelpText = `Usage: wtui [--version] [command]

Launch the worktree TUI, or use a command to persist agent artifacts.

Commands:
  plan          Save, list, read, and update saved plans.
  flow          Create, inspect, and update Flow records.
  session-hook  Capture Claude or Codex session hook payloads.

Flags:
  --version, -v  Print version and exit.
  --help, -h     Print this help and exit.

Examples:
  wtui
  wtui plan --help
  wtui flow --help
  wtui session-hook --provider codex
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
	case sessions.ProviderClaude, sessions.ProviderCodex:
	default:
		return fmt.Errorf("unsupported session provider %q", *providerFlag)
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	root := *stateRoot
	if root == "" {
		root = deps.getenv("WTUI_SESSION_STATE_ROOT")
	}
	if root == "" {
		root = cfg.Sessions.Root
	}
	_, err = sessions.IngestHook(provider, deps.stdin, sessions.IngestOptions{
		StateRoot:          root,
		CopyRawTranscripts: cfg.Sessions.CopyRawTranscripts,
		Env: map[string]string{
			"WTUI_LAUNCH_ID":          deps.getenv("WTUI_LAUNCH_ID"),
			"WTUI_REPO_PATH":          deps.getenv("WTUI_REPO_PATH"),
			"WTUI_WORKTREE_PATH":      deps.getenv("WTUI_WORKTREE_PATH"),
			"WTUI_PLAN_ID":            deps.getenv("WTUI_PLAN_ID"),
			"WTUI_PLAN_PATH":          deps.getenv("WTUI_PLAN_PATH"),
			"WTUI_PLAN_STATE_ROOT":    deps.getenv("WTUI_PLAN_STATE_ROOT"),
			"WTUI_FLOW_ID":            deps.getenv("WTUI_FLOW_ID"),
			"WTUI_FLOW_PHASE_ID":      deps.getenv("WTUI_FLOW_PHASE_ID"),
			"WTUI_FLOW_STATE_ROOT":    deps.getenv("WTUI_FLOW_STATE_ROOT"),
			"WTUI_BRANCH":             deps.getenv("WTUI_BRANCH"),
			"WTUI_COMMIT":             deps.getenv("WTUI_COMMIT"),
			"WTUI_SESSION_STATE_ROOT": deps.getenv("WTUI_SESSION_STATE_ROOT"),
		},
	})
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
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: sessionStore.Root()})
	if err != nil {
		return err
	}
	p := tea.NewProgram(model.NewWithOptions(repos, modelOptionsFromConfig(cfg, opts.ScanRepos, sessionStore, planStore, flowStore)), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func modelOptionsFromConfig(cfg config.Config, scanRepos func() ([]scanner.Repo, error), sessionStore *sessions.Store, planStore *planstore.Store, flowStore *flowstore.Store) model.Options {
	launchOpts := actions.LaunchOptions{TerminalCommand: cfg.Terminal.Command}
	return model.Options{
		AgentCommand:          cfg.Agent.Command,
		CodexReasoningEffort:  cfg.Agent.CodexReasoningEffort,
		ClaudeReasoningEffort: cfg.Agent.ClaudeReasoningEffort,
		StartupMode:           ui.ModeFlows,
		PlanPromptTemplate:    cfg.Agent.PlanPrompt,
		FlowPromptTemplates: model.FlowPromptTemplates{
			Plan:           cfg.FlowPrompts.Plan,
			PlanReview:     cfg.FlowPrompts.PlanReview,
			Implementation: cfg.FlowPrompts.Implementation,
			ReviewLoop:     cfg.FlowPrompts.ReviewLoop,
			PRCreation:     cfg.FlowPrompts.PRCreation,
			Autoreview:     cfg.FlowPrompts.Autoreview,
			Merge:          cfg.FlowPrompts.Merge,
			Generic:        cfg.FlowPrompts.Generic,
		},
		ScanRepos:        scanRepos,
		SessionStateRoot: sessionStore.Root(),
		ListSessions:     sessionStore.List,
		ReadTranscript:   sessionStore.ReadTranscript,
		ListPlans:        planStore.List,
		ListFlows:        flowStore.List,
		ReadPlan:         planStore.ReadPlan,
		LaunchTerminal: func(path string) (actions.TerminalLaunchSpec, error) {
			return actions.TerminalLaunchWithOptions(path, launchOpts)
		},
		EditFile: func(path string) (actions.TerminalLaunchSpec, error) {
			return actions.EditFileWithOptions(path, actions.EditorOptions{EditorCommand: cfg.Editor.Command})
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			return actions.AgentLaunchWithOptions(ctx, launchOpts)
		},
		FinalizeAgentSession: func(ctx actions.AgentLaunchContext) error {
			return sessionStore.MarkLaunchEnded(ctx.LaunchID, time.Now().UTC())
		},
		BootstrapHookForRepo: bootstrapHookResolver(cfg),
		RunBootstrapHook:     actions.RunBootstrapHook,
		SaveAgentCommand: func(command string) error {
			return config.SaveAgentCommand(command)
		},
		SaveAgentReasoningEffort: func(command, effort string) error {
			return config.SaveAgentReasoningEffort(command, effort)
		},
	}
}

func runtimeArtifactRoot(cfg config.Config) string {
	if envRoot := os.Getenv("WTUI_FLOW_STATE_ROOT"); envRoot != "" {
		return envRoot
	}
	if envRoot := os.Getenv("WTUI_PLAN_STATE_ROOT"); envRoot != "" {
		return envRoot
	}
	if envRoot := os.Getenv("WTUI_SESSION_STATE_ROOT"); envRoot != "" {
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
