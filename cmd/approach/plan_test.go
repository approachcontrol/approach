package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/internal/testgit"
	"github.com/approachcontrol/approach/planstore"
	"github.com/approachcontrol/approach/scanner"
)

func noScanDeps(t *testing.T, deps runDeps) runDeps {
	t.Helper()
	deps.scan = func(scanner.ScanOptions) ([]scanner.Repo, error) {
		t.Fatal("scan should not run for plan subcommand")
		return nil, nil
	}
	deps.startProgram = func([]scanner.Repo, config.Config) error {
		t.Fatal("program should not start for plan subcommand")
		return nil
	}
	if deps.loadConfig == nil {
		deps.loadConfig = func() (config.Config, error) { return config.Config{}, nil }
	}
	if deps.getenv == nil {
		deps.getenv = func(string) string { return "" }
	}
	return deps
}

func TestRunPlanHelpPrintsUsageAndExamples(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for plan help")
			return config.Config{}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	requireContainsAll(t, stdout.String(), []string{
		"Usage: approach plan <save|list|read|status|phase> [flags]",
		"approach plan save --title",
		"approach plan read --plan-id",
		"approach plan status set --plan-id",
		"approach plan phase set --plan-id",
	})
}

func TestRunPlanSaveHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for plan save help")
			return config.Config{}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if strings.Contains(stdout.String(), "flag: help requested") {
		t.Fatalf("help output should not contain flag error:\n%s", stdout.String())
	}
	requireContainsAll(t, stdout.String(), []string{
		"Usage: approach plan save [flags]",
		"--title TITLE",
		"--file PATH",
		"--state-root PATH",
		"--json",
	})
}

func TestRunPlanLeafHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{
			name: "list",
			args: []string{"approach", "plan", "list", "--help"},
			wants: []string{
				"Usage: approach plan list [flags]",
				"--json",
				"--repo-path PATH",
			},
		},
		{
			name: "read",
			args: []string{"approach", "plan", "read", "--help"},
			wants: []string{
				"Usage: approach plan read [flags]",
				"--plan-id PLAN_ID",
				"approach plan read --plan-id",
			},
		},
		{
			name: "phase set",
			args: []string{"approach", "plan", "phase", "set", "--help"},
			wants: []string{
				"Usage: approach plan phase set [flags]",
				"--plan-id PLAN_ID",
				"--phase-id PHASE_ID",
			},
		},
		{
			name: "status set",
			args: []string{"approach", "plan", "status", "set", "--help"},
			wants: []string{
				"Usage: approach plan status set [flags]",
				"--plan-id PLAN_ID",
				"--status STATUS",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run for plan leaf help")
					return config.Config{}, nil
				},
				stdout: &stdout,
			}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			if strings.Contains(stdout.String(), "flag: help requested") {
				t.Fatalf("help output should not contain flag error:\n%s", stdout.String())
			}
			requireContainsAll(t, stdout.String(), tc.wants)
		})
	}
}

func TestRunPlanSaveAllowsHelpAsFlagValue(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{
		"approach", "plan", "save",
		"--title", "help",
		"--plan-id", "help-title",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run with explicit state root")
			return config.Config{}, nil
		},
		stdin:  strings.NewReader("body"),
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "help-title" {
		t.Fatalf("expected saved plan id, got %q", stdout.String())
	}
	record := readPlanRecord(t, root, "help-title")
	if record.Title != "help" {
		t.Fatalf("title = %q, want help", record.Title)
	}
}

func TestRunPlanUnknownSubcommandSuggestsNearbyCommand(t *testing.T) {
	err := run([]string{"approach", "plan", "reed"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for unknown plan subcommand")
			return config.Config{}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	requireContainsAll(t, err.Error(), []string{
		`unknown command "reed"; did you mean "read"?`,
		"Usage: approach plan <save|list|read|status|phase> [flags]",
	})
}

func TestRunPlanPhaseUnknownSubcommandSuggestsSet(t *testing.T) {
	err := run([]string{"approach", "plan", "phase", "sete"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for unknown plan phase subcommand")
			return config.Config{}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	requireContainsAll(t, err.Error(), []string{
		`unknown command "sete"; did you mean "set"?`,
		"Usage: approach plan phase set [flags]",
	})
}

func TestRunPlanSaveFromStdinPrintsPlanID(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--title", "My Plan", "--plan-id", "my-plan", "--state-root", root, "--status", "draft"},
		noScanDeps(t, runDeps{
			stdin:  strings.NewReader("# My Plan\n\nbody\n"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "my-plan" {
		t.Fatalf("expected plan_id output, got %q", stdout.String())
	}
	meta := filepath.Join(root, "plans", "my-plan", "meta.json")
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("expected meta.json at %s: %v", meta, err)
	}
	md, err := os.ReadFile(filepath.Join(root, "plans", "my-plan", "plan.md"))
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	if string(md) != "# My Plan\n\nbody\n" {
		t.Fatalf("plan.md mismatch: %q", md)
	}
}

func TestRunPlanSaveJSONPrintsPersistedPlanMetadata(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{
		"approach", "plan", "save",
		"--title", "Structured Plan",
		"--plan-id", "structured-plan",
		"--status", "approved",
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		stdin:  strings.NewReader("# Structured Plan\n"),
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var result planCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	wantPath := filepath.Join(root, "plans", "structured-plan", "plan.md")
	if result.PlanID != "structured-plan" || result.Title != "Structured Plan" || result.Status != "approved" || result.PlanPath != wantPath {
		t.Fatalf("result = %#v, want persisted metadata and path %q", result, wantPath)
	}
}

func TestRunPlanReadJSONDefaultsPlanIDFromLaunchEnvironment(t *testing.T) {
	root := t.TempDir()
	var saveOutput bytes.Buffer
	if err := run([]string{
		"approach", "plan", "save",
		"--title", "Launch Plan",
		"--plan-id", "launch-plan",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		stdin:  strings.NewReader("# Launch Plan\n"),
		stdout: &saveOutput,
	})); err != nil {
		t.Fatalf("save returned error: %v", err)
	}

	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "read", "--json", "--state-root", root}, noScanDeps(t, runDeps{
		getenv: func(key string) string {
			if key == "APPROACH_PLAN_ID" {
				return "launch-plan"
			}
			return ""
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("read returned error: %v", err)
	}

	var result planCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if result.PlanID != "launch-plan" || result.Markdown != "# Launch Plan\n" || result.PlanPath != filepath.Join(root, "plans", "launch-plan", "plan.md") {
		t.Fatalf("result = %#v, want launch plan metadata, Markdown, and path", result)
	}
}

func TestRunPlanStatusSetPreservesPlanContentAndPhases(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	if err := run([]string{
		"approach", "plan", "save",
		"--title", "Lifecycle Plan",
		"--plan-id", "lifecycle-plan",
		"--status", "approved",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdin: strings.NewReader("# Lifecycle Plan\n"), stdout: &output})); err != nil {
		t.Fatalf("save returned error: %v", err)
	}
	if err := run([]string{
		"approach", "plan", "phase", "set",
		"--plan-id", "lifecycle-plan",
		"--phase-id", "implementation",
		"--title", "Implementation",
		"--status", "pending",
		"--order", "1",
		"--state-root", root,
	}, noScanDeps(t, runDeps{})); err != nil {
		t.Fatalf("phase set returned error: %v", err)
	}

	output.Reset()
	err := run([]string{
		"approach", "plan", "status", "set",
		"--status", "in_progress",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		getenv: func(key string) string {
			if key == "APPROACH_PLAN_ID" {
				return "lifecycle-plan"
			}
			return ""
		},
		stdout: &output,
	}))
	if err != nil {
		t.Fatalf("status set returned error: %v", err)
	}

	var result planCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output.String())
	}
	if result.Status != "in_progress" || result.Title != "Lifecycle Plan" || len(result.Phases) != 1 || result.Phases[0].PhaseID != "implementation" {
		t.Fatalf("result = %#v, want updated status with existing metadata", result)
	}
	markdown, err := os.ReadFile(filepath.Join(root, "plans", "lifecycle-plan", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(markdown) != "# Lifecycle Plan\n" {
		t.Fatalf("Markdown changed to %q", markdown)
	}
}

func TestRunPlanSaveFromFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plan-input.md")
	if err := os.WriteFile(file, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--title", "File Plan", "--plan-id", "file-plan", "--file", file, "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	md, err := os.ReadFile(filepath.Join(root, "plans", "file-plan", "plan.md"))
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	if string(md) != "from file" {
		t.Fatalf("plan.md mismatch: %q", md)
	}
}

func TestRunPlanSaveStateRootPrecedence(t *testing.T) {
	flowRoot := t.TempDir()
	planRoot := t.TempDir()
	sessionRoot := t.TempDir()
	configRoot := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p"},
		noScanDeps(t, runDeps{
			loadConfig: func() (config.Config, error) {
				return config.Config{Sessions: config.SessionsConfig{Root: configRoot}}, nil
			},
			getenv: func(key string) string {
				switch key {
				case "APPROACH_FLOW_STATE_ROOT":
					return flowRoot
				case "APPROACH_PLAN_STATE_ROOT":
					return planRoot
				case "APPROACH_SESSION_STATE_ROOT":
					return sessionRoot
				}
				return ""
			},
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(flowRoot, "plans", "p", "meta.json")); err != nil {
		t.Fatalf("expected plan under APPROACH_FLOW_STATE_ROOT: %v", err)
	}
	if _, err := os.Stat(filepath.Join(planRoot, "plans", "p", "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("plan should not be under plan root when Flow root is set")
	}
	if _, err := os.Stat(filepath.Join(sessionRoot, "plans", "p", "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("plan should not be under session root")
	}
}

func TestRunPlanSaveSessionRootFallback(t *testing.T) {
	sessionRoot := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p"},
		noScanDeps(t, runDeps{
			getenv: func(key string) string {
				if key == "APPROACH_SESSION_STATE_ROOT" {
					return sessionRoot
				}
				return ""
			},
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionRoot, "plans", "p", "meta.json")); err != nil {
		t.Fatalf("expected plan under APPROACH_SESSION_STATE_ROOT: %v", err)
	}
}

func TestRunPlanSaveFillsMetadataFromEnv(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", root},
		noScanDeps(t, runDeps{
			getenv: func(key string) string {
				switch key {
				case "APPROACH_AGENT":
					return "claude"
				case "APPROACH_LAUNCH_ID":
					return "launch-9"
				case "APPROACH_REPO_PATH":
					return "/repo"
				case "APPROACH_WORKTREE_PATH":
					return "/repo/wt"
				case "APPROACH_BRANCH":
					return "feature/env"
				case "APPROACH_COMMIT":
					return "deadbeef"
				}
				return ""
			},
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "plans", "p", "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	for _, want := range []string{
		`"provider": "claude"`,
		`"launch_id": "launch-9"`,
		`"repo_path": "/repo"`,
		`"worktree_path": "/repo/wt"`,
		`"branch": "feature/env"`,
		`"commit": "deadbeef"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("meta.json missing %s:\n%s", want, data)
		}
	}
}

func TestRunPlanSaveFromLinkedWorktreeUsesRootRepoPath(t *testing.T) {
	stateRoot := t.TempDir()
	repoDir, worktreeDir := makeLinkedWorktree(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return worktreeDir, nil },
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.RepoPath != repoDir {
		t.Fatalf("repo_path = %q, want root repo %q", record.RepoPath, repoDir)
	}
	if record.WorktreePath != worktreeDir {
		t.Fatalf("worktree_path = %q, want linked worktree %q", record.WorktreePath, worktreeDir)
	}
	if record.Branch != "feature/plan" {
		t.Fatalf("branch = %q, want feature/plan", record.Branch)
	}
	if record.Commit == "" {
		t.Fatal("expected commit metadata from linked worktree")
	}
}

func TestRunPlanSaveNormalizesLinkedWorktreeRepoPathFromEnv(t *testing.T) {
	stateRoot := t.TempDir()
	repoDir, worktreeDir := makeLinkedWorktree(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getenv: func(key string) string {
				switch key {
				case "APPROACH_REPO_PATH", "APPROACH_WORKTREE_PATH":
					return worktreeDir
				}
				return ""
			},
			getwd:  func() (string, error) { return worktreeDir, nil },
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.RepoPath != repoDir {
		t.Fatalf("repo_path = %q, want root repo %q", record.RepoPath, repoDir)
	}
	if record.WorktreePath != worktreeDir {
		t.Fatalf("worktree_path = %q, want linked worktree %q", record.WorktreePath, worktreeDir)
	}
}

func TestRunPlanSaveExplicitRepoPathDoesNotUseUnrelatedCWDMetadata(t *testing.T) {
	stateRoot := t.TempDir()
	_, worktreeDir := makeLinkedWorktree(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", stateRoot, "--repo-path", "/repo"},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return worktreeDir, nil },
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.RepoPath != "/repo" {
		t.Fatalf("repo_path = %q, want explicit /repo", record.RepoPath)
	}
	if record.WorktreePath != "" || record.Branch != "" || record.Commit != "" {
		t.Fatalf("explicit unrelated repo should not use cwd metadata: %#v", record)
	}
}

func TestRunPlanSaveUpdatePreservesExistingGitMetadataWhenOmitted(t *testing.T) {
	stateRoot := t.TempDir()
	repoDir, worktreeDir := makeLinkedWorktree(t)
	otherRepoDir, otherWorktreeDir := makeLinkedWorktree(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "Original", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return worktreeDir, nil },
			stdin:  strings.NewReader("first body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("first run returned error: %v", err)
	}

	stdout.Reset()
	err = run([]string{"approach", "plan", "save", "--title", "Updated", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return otherWorktreeDir, nil },
			stdin:  strings.NewReader("second body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("second run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.Title != "Updated" {
		t.Fatalf("title = %q, want Updated", record.Title)
	}
	if record.RepoPath != repoDir || record.WorktreePath != worktreeDir {
		t.Fatalf("git metadata should stay on original checkout, got repo=%q worktree=%q; want repo=%q worktree=%q",
			record.RepoPath, record.WorktreePath, repoDir, worktreeDir)
	}
	if record.RepoPath == otherRepoDir || record.WorktreePath == otherWorktreeDir {
		t.Fatalf("git metadata was overwritten by update cwd: %#v", record)
	}
	md, err := os.ReadFile(filepath.Join(stateRoot, "plans", "p", "plan.md"))
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	if string(md) != "second body" {
		t.Fatalf("plan.md = %q, want second body", md)
	}
}

func TestRunPlanSaveUpdateWithBranchOnlyPreservesOmittedGitMetadata(t *testing.T) {
	stateRoot := t.TempDir()
	repoDir, worktreeDir := makeLinkedWorktree(t)
	otherRepoDir, otherWorktreeDir := makeLinkedWorktree(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "Original", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return worktreeDir, nil },
			stdin:  strings.NewReader("first body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("first run returned error: %v", err)
	}
	original := readPlanRecord(t, stateRoot, "p")

	stdout.Reset()
	err = run([]string{"approach", "plan", "save", "--title", "Updated", "--plan-id", "p", "--state-root", stateRoot, "--branch", "manual-branch"},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return otherWorktreeDir, nil },
			stdin:  strings.NewReader("second body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("second run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.Branch != "manual-branch" {
		t.Fatalf("branch = %q, want manual-branch", record.Branch)
	}
	if record.RepoPath != repoDir || record.WorktreePath != worktreeDir || record.Commit != original.Commit {
		t.Fatalf("omitted git metadata should stay original, got repo=%q worktree=%q commit=%q; want repo=%q worktree=%q commit=%q",
			record.RepoPath, record.WorktreePath, record.Commit, repoDir, worktreeDir, original.Commit)
	}
	if record.RepoPath == otherRepoDir || record.WorktreePath == otherWorktreeDir {
		t.Fatalf("omitted git metadata was overwritten by update cwd: %#v", record)
	}
}

func TestRunPlanSaveFromBareRepoUsesBareRepoPath(t *testing.T) {
	stateRoot := t.TempDir()
	bareDir, commit := makeBareRepo(t)
	var stdout bytes.Buffer

	err := run([]string{"approach", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", stateRoot},
		noScanDeps(t, runDeps{
			getwd:  func() (string, error) { return bareDir, nil },
			stdin:  strings.NewReader("body"),
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	record := readPlanRecord(t, stateRoot, "p")
	if record.RepoPath != bareDir {
		t.Fatalf("repo_path = %q, want bare repo %q", record.RepoPath, bareDir)
	}
	if record.WorktreePath != "" {
		t.Fatalf("worktree_path = %q, want empty for bare repo", record.WorktreePath)
	}
	if record.Branch != "main" {
		t.Fatalf("branch = %q, want main", record.Branch)
	}
	if record.Commit != commit {
		t.Fatalf("commit = %q, want %q", record.Commit, commit)
	}
}

func TestRunPlanListJSON(t *testing.T) {
	root := t.TempDir()
	mustRun(t, []string{"approach", "plan", "save", "--title", "Alpha", "--plan-id", "alpha", "--state-root", root, "--repo-path", "/repo"}, "alpha body")
	mustRun(t, []string{"approach", "plan", "save", "--title", "Beta", "--plan-id", "beta", "--state-root", root, "--repo-path", "/other"}, "beta body")

	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "list", "--repo-path", "/repo", "--state-root", root, "--json"},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("output is not JSON array: %v\n%s", err, stdout.String())
	}
	if len(records) != 1 || records[0]["plan_id"] != "alpha" {
		t.Fatalf("expected only alpha for /repo, got %#v", records)
	}
}

func TestRunPlanListRequiresJSON(t *testing.T) {
	root := t.TempDir()
	err := run([]string{"approach", "plan", "list", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error requiring --json")
	}
	if !strings.Contains(err.Error(), "json") {
		t.Fatalf("expected --json requirement error, got %q", err)
	}
}

func TestRunPlanReadPrintsMarkdownOnly(t *testing.T) {
	root := t.TempDir()
	mustRun(t, []string{"approach", "plan", "save", "--title", "Readable", "--plan-id", "readable", "--state-root", root}, "# Readable\n\nbody\n")

	var stdout bytes.Buffer
	err := run([]string{"approach", "plan", "read", "--plan-id", "readable", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.String() != "# Readable\n\nbody\n" {
		t.Fatalf("read output mismatch: %q", stdout.String())
	}
}

func TestRunPlanSaveRequiresTitle(t *testing.T) {
	err := run([]string{"approach", "plan", "save", "--state-root", t.TempDir()},
		noScanDeps(t, runDeps{stdin: strings.NewReader("body"), stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error requiring --title")
	}
}

func mustRun(t *testing.T, args []string, stdin string) {
	t.Helper()
	err := run(args, noScanDeps(t, runDeps{
		stdin:  strings.NewReader(stdin),
		stdout: &bytes.Buffer{},
	}))
	if err != nil {
		t.Fatalf("run(%v) error = %v", args, err)
	}
}

func makeLinkedWorktree(t *testing.T) (repoDir, worktreeDir string) {
	t.Helper()
	root := t.TempDir()
	repoDir = filepath.Join(root, "project")
	worktreeDir = filepath.Join(root, "project-worktrees", "feature-plan")
	mustGit(t, root, "init", repoDir)
	testgit.ConfigureRepo(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", "README.md")
	mustGit(t, repoDir, "commit", "-m", "initial")
	mustGit(t, repoDir, "worktree", "add", "-b", "feature/plan", worktreeDir)
	return mustRealPath(t, repoDir), mustRealPath(t, worktreeDir)
}

func makeBareRepo(t *testing.T) (bareDir, commit string) {
	t.Helper()
	root := t.TempDir()
	repoDir := filepath.Join(root, "project")
	bareDir = filepath.Join(root, "project.git")
	mustGit(t, root, "init", repoDir)
	testgit.ConfigureRepo(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", "README.md")
	mustGit(t, repoDir, "commit", "-m", "initial")
	mustGit(t, repoDir, "branch", "-M", "main")
	mustGit(t, root, "clone", "--bare", repoDir, bareDir)
	return mustRealPath(t, bareDir), gitOutput(t, bareDir, "rev-parse", "HEAD")
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	testgit.Run(t, dir, args...)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return testgit.Output(t, dir, args...)
}

func readPlanRecord(t *testing.T, root, planID string) planstore.PlanRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "plans", planID, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var record planstore.PlanRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode meta.json: %v\n%s", err, data)
	}
	return record
}

func mustRealPath(t *testing.T, path string) string {
	t.Helper()
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve real path %q: %v", path, err)
	}
	return realPath
}
