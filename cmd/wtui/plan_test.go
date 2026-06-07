package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/scanner"
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

func TestRunPlanSaveFromStdinPrintsPlanID(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "save", "--title", "My Plan", "--plan-id", "my-plan", "--state-root", root, "--status", "draft"},
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

func TestRunPlanSaveFromFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plan-input.md")
	if err := os.WriteFile(file, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "save", "--title", "File Plan", "--plan-id", "file-plan", "--file", file, "--state-root", root},
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
	planRoot := t.TempDir()
	sessionRoot := t.TempDir()
	configRoot := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "save", "--title", "P", "--plan-id", "p"},
		noScanDeps(t, runDeps{
			loadConfig: func() (config.Config, error) {
				return config.Config{Sessions: config.SessionsConfig{Root: configRoot}}, nil
			},
			getenv: func(key string) string {
				switch key {
				case "WTUI_PLAN_STATE_ROOT":
					return planRoot
				case "WTUI_SESSION_STATE_ROOT":
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
	if _, err := os.Stat(filepath.Join(planRoot, "plans", "p", "meta.json")); err != nil {
		t.Fatalf("expected plan under WTUI_PLAN_STATE_ROOT: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionRoot, "plans", "p", "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("plan should not be under session root")
	}
}

func TestRunPlanSaveSessionRootFallback(t *testing.T) {
	sessionRoot := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "save", "--title", "P", "--plan-id", "p"},
		noScanDeps(t, runDeps{
			getenv: func(key string) string {
				if key == "WTUI_SESSION_STATE_ROOT" {
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
		t.Fatalf("expected plan under WTUI_SESSION_STATE_ROOT: %v", err)
	}
}

func TestRunPlanSaveFillsMetadataFromEnv(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", root},
		noScanDeps(t, runDeps{
			getenv: func(key string) string {
				switch key {
				case "WTUI_AGENT":
					return "claude"
				case "WTUI_LAUNCH_ID":
					return "launch-9"
				case "WTUI_REPO_PATH":
					return "/repo"
				case "WTUI_WORKTREE_PATH":
					return "/repo/wt"
				case "WTUI_BRANCH":
					return "feature/env"
				case "WTUI_COMMIT":
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

func TestRunPlanListJSON(t *testing.T) {
	root := t.TempDir()
	mustRun(t, []string{"wtui", "plan", "save", "--title", "Alpha", "--plan-id", "alpha", "--state-root", root, "--repo-path", "/repo"}, "alpha body")
	mustRun(t, []string{"wtui", "plan", "save", "--title", "Beta", "--plan-id", "beta", "--state-root", root, "--repo-path", "/other"}, "beta body")

	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "list", "--repo-path", "/repo", "--state-root", root, "--json"},
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
	err := run([]string{"wtui", "plan", "list", "--state-root", root},
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
	mustRun(t, []string{"wtui", "plan", "save", "--title", "Readable", "--plan-id", "readable", "--state-root", root}, "# Readable\n\nbody\n")

	var stdout bytes.Buffer
	err := run([]string{"wtui", "plan", "read", "--plan-id", "readable", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if stdout.String() != "# Readable\n\nbody\n" {
		t.Fatalf("read output mismatch: %q", stdout.String())
	}
}

func TestRunPlanSaveRequiresTitle(t *testing.T) {
	err := run([]string{"wtui", "plan", "save", "--state-root", t.TempDir()},
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
