package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/flowstore"
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

func TestRun_PassesRefreshScannerWithResolvedScanOptions(t *testing.T) {
	var startupScan scanner.ScanOptions
	var refreshScan scanner.ScanOptions
	scans := 0
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
			scans++
			if scans == 1 {
				startupScan = opts
			} else {
				refreshScan = opts
			}
			return []scanner.Repo{{Path: "/repo", DisplayName: "repo"}}, nil
		},
		startProgramWithOptions: func(_ []scanner.Repo, opts startProgramOptions) error {
			if opts.ScanRepos == nil {
				t.Fatal("expected refresh scanner")
			}
			_, err := opts.ScanRepos()
			return err
		},
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if scans != 2 {
		t.Fatalf("scan count = %d, want startup + refresh", scans)
	}
	if startupScan.Root != "/from/env" || refreshScan.Root != "/from/env" {
		t.Fatalf("scan roots startup=%q refresh=%q, want WORKTREE_ROOT", startupScan.Root, refreshScan.Root)
	}
	if startupScan.MaxDepth != 1 || refreshScan.MaxDepth != 1 {
		t.Fatalf("scan max depth startup=%d refresh=%d, want 1", startupScan.MaxDepth, refreshScan.MaxDepth)
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
			return config.Config{Agent: config.AgentConfig{
				Command:    "codex",
				PlanPrompt: "Implement {title}",
			}}, nil
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
	if got.Agent.PlanPrompt != "Implement {title}" {
		t.Fatalf("expected agent plan prompt passed to program, got %q", got.Agent.PlanPrompt)
	}
}

func TestRuntimeArtifactRootPrecedenceIncludesFlowRoot(t *testing.T) {
	cfg := config.Config{Sessions: config.SessionsConfig{Root: "/from/config"}}
	t.Setenv("WTUI_SESSION_STATE_ROOT", "/from/session")
	t.Setenv("WTUI_PLAN_STATE_ROOT", "/from/plan")
	t.Setenv("WTUI_FLOW_STATE_ROOT", "/from/flow")

	if got := runtimeArtifactRoot(cfg); got != "/from/flow" {
		t.Fatalf("artifact root = %q, want flow root", got)
	}
}

func TestRuntimeArtifactRootFallsBackThroughPlanSessionConfig(t *testing.T) {
	cfg := config.Config{Sessions: config.SessionsConfig{Root: "/from/config"}}
	t.Setenv("WTUI_FLOW_STATE_ROOT", "")
	t.Setenv("WTUI_SESSION_STATE_ROOT", "/from/session")
	t.Setenv("WTUI_PLAN_STATE_ROOT", "/from/plan")
	if got := runtimeArtifactRoot(cfg); got != "/from/plan" {
		t.Fatalf("artifact root = %q, want plan root", got)
	}

	t.Setenv("WTUI_PLAN_STATE_ROOT", "")
	if got := runtimeArtifactRoot(cfg); got != "/from/session" {
		t.Fatalf("artifact root = %q, want session root", got)
	}

	t.Setenv("WTUI_SESSION_STATE_ROOT", "")
	if got := runtimeArtifactRoot(cfg); got != "/from/config" {
		t.Fatalf("artifact root = %q, want config root", got)
	}
}

func TestRunSessionHookWritesSessionMetadata(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "claude.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"timestamp":"2026-06-06T14:01:00Z","role":"user","kind":"message","text":"Fix scanner tests"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	stdin := bytes.NewBufferString(`{
		"session_id": "claude-session-1",
		"cwd": "/repo/worktree",
		"transcript_path": ` + quoteJSON(transcriptPath) + `,
		"summary": "Fix scanner tests",
		"ended_at": "2026-06-06T14:45:00Z"
	}`)

	err := run([]string{"wtui", "session-hook", "--provider", "claude", "--state-root", root}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{Sessions: config.SessionsConfig{CopyRawTranscripts: true}}, nil
		},
		scan: func(scanner.ScanOptions) ([]scanner.Repo, error) {
			t.Fatal("scan should not run for session-hook")
			return nil, nil
		},
		stdin: stdin,
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	metaPath := singleSessionFile(t, root, "claude", "meta.json")
	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	for _, want := range []string{`"provider": "claude"`, `"session_id": "claude-session-1"`, `"status": "ended"`, `"summary": "Fix scanner tests"`} {
		if !strings.Contains(string(meta), want) {
			t.Fatalf("metadata missing %s:\n%s", want, meta)
		}
	}
}

func TestRunSessionHookPersistsPlanEnvironment(t *testing.T) {
	root := t.TempDir()
	planPath := filepath.Join(root, "plans", "plan-1", "plan.md")

	err := run([]string{"wtui", "session-hook", "--provider", "codex", "--state-root", root}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		getenv: func(key string) string {
			switch key {
			case "WTUI_PLAN_ID":
				return "plan-1"
			case "WTUI_PLAN_PATH":
				return planPath
			case "WTUI_FLOW_ID":
				return "flow-1"
			case "WTUI_FLOW_PHASE_ID":
				return "plan"
			case "WTUI_FLOW_STATE_ROOT":
				return root
			default:
				return ""
			}
		},
		stdin: strings.NewReader(`{"session_id":"codex-plan-1","cwd":"/repo/worktree"}`),
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	meta, err := os.ReadFile(singleSessionFile(t, root, "codex", "meta.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	for _, want := range []string{`"plan_id": "plan-1"`, `"plan_path": ` + quoteJSON(planPath), `"flow_id": "flow-1"`, `"flow_phase_id": "plan"`} {
		if !strings.Contains(string(meta), want) {
			t.Fatalf("metadata missing %s:\n%s", want, meta)
		}
	}
}

func TestRunSessionHookAttachesFlowFromPlanStateRoot(t *testing.T) {
	planRoot := t.TempDir()
	sessionRoot := t.TempDir()
	repoPath := filepath.Join(planRoot, "repo")
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: planRoot})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Plan Root Flow",
		Instructions: "attach the session",
		RepoPath:     repoPath,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = run([]string{"wtui", "session-hook", "--provider", "codex", "--state-root", sessionRoot}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		getenv: func(key string) string {
			switch key {
			case "WTUI_PLAN_STATE_ROOT":
				return planRoot
			case "WTUI_FLOW_ID":
				return flow.FlowID
			case "WTUI_FLOW_PHASE_ID":
				return "plan"
			default:
				return ""
			}
		},
		stdin: strings.NewReader(`{"session_id":"codex-flow-plan-root","cwd":"/repo/worktree"}`),
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	read, err := flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(read.Phases) == 0 || len(read.Phases[0].Sessions) != 1 {
		t.Fatalf("attached sessions = %#v, want one on first phase", read.Phases)
	}
	if got := read.Phases[0].Sessions[0].SessionID; got != "codex-flow-plan-root" {
		t.Fatalf("attached session ID = %q, want codex-flow-plan-root", got)
	}
}

func TestRunSessionHookRejectsMalformedJSON(t *testing.T) {
	err := run([]string{"wtui", "session-hook", "--provider", "codex", "--state-root", t.TempDir()}, runDeps{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		stdin:      strings.NewReader(`{"session_id":`),
	})
	if err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if !strings.Contains(err.Error(), "parse hook payload") {
		t.Fatalf("expected parse hook payload error, got %q", err)
	}
}

func TestRunSessionHookRejectsUnsupportedProvider(t *testing.T) {
	err := run([]string{"wtui", "session-hook", "--provider", "other", "--state-root", t.TempDir()}, runDeps{
		stdin: strings.NewReader(`{}`),
	})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if !strings.Contains(err.Error(), "unsupported session provider") {
		t.Fatalf("expected unsupported provider error, got %q", err)
	}
}

func TestRunSessionHookHonorsCopyRawTranscriptConfig(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "codex.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"role":"user","kind":"message","text":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	err := run([]string{"wtui", "session-hook", "--provider", "codex", "--state-root", root}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{Sessions: config.SessionsConfig{CopyRawTranscripts: false}}, nil
		},
		stdin: strings.NewReader(`{
			"session_id": "codex-session-1",
			"cwd": "/repo/worktree",
			"transcript_path": ` + quoteJSON(transcriptPath) + `
		}`),
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "sessions", "codex", "*", "raw.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("expected no copied raw transcript, matches=%#v err=%v", matches, err)
	}
}

func TestRunSessionHookEnvStateRootOverridesConfig(t *testing.T) {
	configRoot := t.TempDir()
	envRoot := t.TempDir()
	err := run([]string{"wtui", "session-hook", "--provider", "codex"}, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{Sessions: config.SessionsConfig{Root: configRoot}}, nil
		},
		getenv: func(key string) string {
			if key == "WTUI_SESSION_STATE_ROOT" {
				return envRoot
			}
			return ""
		},
		stdin: strings.NewReader(`{"session_id":"codex-env-root","cwd":"/repo/worktree"}`),
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(envRoot, "sessions", "codex", "*", "meta.json")); err != nil || len(matches) != 1 {
		t.Fatalf("expected metadata under env root, matches=%#v err=%v", matches, err)
	}
	if matches, err := filepath.Glob(filepath.Join(configRoot, "sessions", "codex", "*", "meta.json")); err != nil || len(matches) != 0 {
		t.Fatalf("expected no metadata under config root, matches=%#v err=%v", matches, err)
	}
}

func singleSessionFile(t *testing.T, root, provider, name string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "sessions", provider, "*", name))
	if err != nil {
		t.Fatalf("glob session file: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("glob returned %d matches, want 1: %#v", len(matches), matches)
	}
	return matches[0]
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
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
