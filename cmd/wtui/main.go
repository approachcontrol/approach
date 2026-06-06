package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/internal/version"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/sessions"
)

func main() {
	if err := run(os.Args, runDeps{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type runDeps struct {
	loadConfig   func() (config.Config, error)
	getenv       func(string) string
	scan         func(scanner.ScanOptions) ([]scanner.Repo, error)
	startProgram func([]scanner.Repo, config.Config) error
	stdin        io.Reader
	stdout       io.Writer
}

func run(args []string, deps runDeps) error {
	deps = fillRunDeps(deps)
	if len(args) > 1 && args[1] == "session-hook" {
		return runSessionHook(args, deps)
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	versionFlag := flags.Bool("version", false, "print version and exit")
	flags.BoolVar(versionFlag, "v", false, "print version and exit")
	if err := flags.Parse(args[1:]); err != nil {
		return err
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

	if err := deps.startProgram(repos, cfg); err != nil {
		return fmt.Errorf("error: %w", err)
	}
	return nil
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
	if deps.scan == nil {
		deps.scan = scanner.Scan
	}
	if deps.startProgram == nil {
		deps.startProgram = startProgram
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
			"WTUI_BRANCH":             deps.getenv("WTUI_BRANCH"),
			"WTUI_COMMIT":             deps.getenv("WTUI_COMMIT"),
			"WTUI_SESSION_STATE_ROOT": deps.getenv("WTUI_SESSION_STATE_ROOT"),
		},
	})
	return err
}

func startProgram(repos []scanner.Repo, cfg config.Config) error {
	sessionStore, err := sessions.NewStore(sessions.StoreOptions{
		Root:               cfg.Sessions.Root,
		CopyRawTranscripts: cfg.Sessions.CopyRawTranscripts,
	})
	if err != nil {
		return err
	}
	p := tea.NewProgram(model.NewWithOptions(repos, model.Options{
		AgentCommand:     cfg.Agent.Command,
		SessionStateRoot: sessionStore.Root(),
		ListSessions:     sessionStore.List,
		ReadTranscript:   sessionStore.ReadTranscript,
		FinalizeAgentSession: func(ctx actions.AgentLaunchContext) error {
			return sessionStore.MarkLaunchEnded(ctx.LaunchID, time.Now().UTC())
		},
		BootstrapHookForRepo: bootstrapHookResolver(cfg),
		RunBootstrapHook:     actions.RunBootstrapHook,
		SaveAgentCommand: func(command string) error {
			return config.SaveAgentCommand(command)
		},
	}), tea.WithAltScreen())
	_, err = p.Run()
	return err
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
