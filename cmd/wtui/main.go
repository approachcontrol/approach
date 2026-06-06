package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/internal/version"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
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
	stdout       io.Writer
}

func run(args []string, deps runDeps) error {
	deps = fillRunDeps(deps)

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
	if deps.stdout == nil {
		deps.stdout = os.Stdout
	}
	return deps
}

func startProgram(repos []scanner.Repo, cfg config.Config) error {
	p := tea.NewProgram(model.NewWithOptions(repos, model.Options{
		AgentCommand: cfg.Agent.Command,
		SaveAgentCommand: func(command string) error {
			return config.SaveAgentCommand(command)
		},
	}), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
