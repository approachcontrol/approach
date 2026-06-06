package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/internal/version"
	"github.com/brian-bell/wtui/scanner"
)

func TestRun_VersionBypassesConfigAndScan(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"wtui", "--version"}, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for --version")
			return config.Config{}, nil
		},
		scan: func(scanner.ScanOptions) ([]scanner.Repo, error) {
			t.Fatal("scan should not run for --version")
			return nil, nil
		},
		startProgram: func([]scanner.Repo, config.Config) error {
			t.Fatal("program should not start for --version")
			return nil
		},
		stdout: &stdout,
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != version.String() {
		t.Fatalf("expected version output %q, got %q", version.String(), stdout.String())
	}
}

func TestRun_LoadsConfigBeforeScanning(t *testing.T) {
	var got scanner.ScanOptions
	err := run([]string{"wtui"}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Scan: config.ScanConfig{
					Root:     "/from/config",
					MaxDepth: 1,
				},
			}, nil
		},
		getenv: func(string) string { return "" },
		scan: func(opts scanner.ScanOptions) ([]scanner.Repo, error) {
			got = opts
			return []scanner.Repo{{Path: "/repo", DisplayName: "repo"}}, nil
		},
		startProgram: func([]scanner.Repo, config.Config) error { return nil },
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got.Root != "/from/config" {
		t.Fatalf("expected config scan root, got %q", got.Root)
	}
	if got.MaxDepth != 1 {
		t.Fatalf("expected config max depth 1, got %d", got.MaxDepth)
	}
}

func TestRun_WorktreeRootEnvOverridesConfigRoot(t *testing.T) {
	var got scanner.ScanOptions
	err := run([]string{"wtui"}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Scan: config.ScanConfig{Root: "/from/config", MaxDepth: 1},
			}, nil
		},
		getenv: func(key string) string {
			if key == "WORKTREE_ROOT" {
				return "/from/env"
			}
			return ""
		},
		scan: func(opts scanner.ScanOptions) ([]scanner.Repo, error) {
			got = opts
			return nil, nil
		},
		startProgram: func([]scanner.Repo, config.Config) error { return nil },
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	if got.Root != "/from/env" {
		t.Fatalf("expected WORKTREE_ROOT to override config root, got %q", got.Root)
	}
	if got.MaxDepth != 1 {
		t.Fatalf("expected config max depth to remain 1, got %d", got.MaxDepth)
	}
}

func TestRun_ConfigErrorStopsBeforeScan(t *testing.T) {
	scanned := false
	err := run([]string{"wtui"}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, errors.New("bad config")
		},
		getenv: func(string) string { return "" },
		scan: func(scanner.ScanOptions) ([]scanner.Repo, error) {
			scanned = true
			return nil, nil
		},
		startProgram: func([]scanner.Repo, config.Config) error { return nil },
	})
	if err == nil {
		t.Fatal("expected config error")
	}
	if scanned {
		t.Fatal("scan should not run when config fails")
	}
}

func TestRun_PassesConfigToProgram(t *testing.T) {
	var got config.Config
	err := run([]string{"wtui"}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{Agent: config.AgentConfig{Command: "codex"}}, nil
		},
		getenv: func(string) string { return "" },
		scan: func(scanner.ScanOptions) ([]scanner.Repo, error) {
			return []scanner.Repo{{Path: "/repo", DisplayName: "repo"}}, nil
		},
		startProgram: func(_ []scanner.Repo, cfg config.Config) error {
			got = cfg
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got.Agent.Command != "codex" {
		t.Fatalf("expected agent config passed to program, got %q", got.Agent.Command)
	}
}

func TestBootstrapHookResolverMatchesConfiguredRepoPath(t *testing.T) {
	cfg := config.Config{
		Bootstrap: config.BootstrapConfig{
			TimeoutSeconds: 120,
			Hooks: []config.BootstrapHookConfig{
				{RepoPath: filepath.Clean("/dev/wtui/"), Script: ".wtui/bootstrap"},
				{RepoPath: "/dev/client-api", Script: "/bin/bootstrap-client-api", TimeoutSeconds: 300},
			},
		},
	}
	resolve := bootstrapHookResolver(cfg)

	hook, ok := resolve("/dev/wtui")
	if !ok {
		t.Fatal("expected hook for configured repo")
	}
	if hook != (actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 120}) {
		t.Fatalf("unexpected hook: %#v", hook)
	}

	hook, ok = resolve("/dev/client-api")
	if !ok {
		t.Fatal("expected hook for second configured repo")
	}
	if hook.TimeoutSeconds != 300 {
		t.Fatalf("expected per-hook timeout override 300, got %d", hook.TimeoutSeconds)
	}
}

func TestBootstrapHookResolverDoesNotMatchDifferentRepoPath(t *testing.T) {
	resolve := bootstrapHookResolver(config.Config{
		Bootstrap: config.BootstrapConfig{
			TimeoutSeconds: 120,
			Hooks: []config.BootstrapHookConfig{
				{RepoPath: "/dev/wtui", Script: ".wtui/bootstrap"},
			},
		},
	})

	if _, ok := resolve("/dev/wtui-other"); ok {
		t.Fatal("expected non-matching repo to have no hook")
	}
}
