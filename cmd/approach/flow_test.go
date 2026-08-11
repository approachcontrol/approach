package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/planstore"
)

func TestRunFlowHelpPrintsUsageAndExamples(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for flow help")
			return config.Config{}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	requireContainsAll(t, stdout.String(), []string{
		"Usage: approach flow <create|list|read|phase|plan|issue|pr|merge> [flags]",
		"approach flow read --flow-id",
		"approach flow phase complete --flow-id",
		"approach flow phase block --flow-id",
		"approach flow phase needs-attention --flow-id",
		"approach flow phase restart --flow-id",
		"approach flow phase reset --flow-id",
		"approach flow phase set --flow-id",
		"approach flow issue set --flow-id",
		"approach flow pr set --flow-id",
		"approach flow merge set --flow-id",
	})
}

func TestRunFlowPhaseHelpPrintsUsageAndExamples(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "phase", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for flow phase help")
			return config.Config{}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	requireContainsAll(t, stdout.String(), []string{
		"Usage: approach flow phase <set|complete|block|needs-attention|restart|reset|add-child> [flags]",
		"approach flow phase set --flow-id",
		"approach flow phase complete --flow-id",
		"approach flow phase block --flow-id",
		"approach flow phase needs-attention --flow-id",
		"approach flow phase restart --flow-id",
		"approach flow phase reset --flow-id",
		"--status completed",
		"approach flow phase add-child --flow-id",
	})
}

func TestRunFlowPhaseActionHelpPrintsExamplesWithoutLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{
			name: "complete",
			args: []string{"approach", "flow", "phase", "complete", "--help"},
			wants: []string{
				"Usage: approach flow phase complete [flags]",
				"--flow-id FLOW_ID",
				"--phase-id PHASE_ID",
				"--outcome OUTCOME",
				`approach flow phase complete --flow-id "$FLOW_ID" --phase-id plan`,
			},
		},
		{
			name: "block",
			args: []string{"approach", "flow", "phase", "block", "--help"},
			wants: []string{
				"Usage: approach flow phase block [flags]",
				"--flow-id FLOW_ID",
				"--phase-id PHASE_ID",
				"--notes TEXT",
				`approach flow phase block --flow-id "$FLOW_ID" --phase-id implementation --notes "Waiting on review"`,
			},
		},
		{
			name: "needs-attention",
			args: []string{"approach", "flow", "phase", "needs-attention", "--help"},
			wants: []string{
				"Usage: approach flow phase needs-attention [flags]",
				"--flow-id FLOW_ID",
				"--phase-id PHASE_ID",
				"--notes TEXT",
				`approach flow phase needs-attention --flow-id "$FLOW_ID" --phase-id plan-review --outcome changes_requested`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run for flow phase action help")
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

func TestRunFlowPhaseSetHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "phase", "set", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for flow phase set help")
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
		"Usage: approach flow phase set [flags]",
		"--flow-id FLOW_ID",
		"--phase-id PHASE_ID",
		"--status STATUS",
	})
}

func TestRunFlowPRSetHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "pr", "set", "--help"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for flow pr set help")
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
		"Usage: approach flow pr set [flags]",
		"--number N",
		"--url URL",
		"--head BRANCH",
	})
}

func TestRunFlowIssueHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "issue", args: []string{"approach", "flow", "issue", "--help"}},
		{name: "issue set", args: []string{"approach", "flow", "issue", "set", "--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run for flow issue help")
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
				"Usage: approach flow issue set [flags]",
				"--number N",
				"--url URL",
				"approach flow issue set --flow-id",
			})
		})
	}
}

func TestRunFlowLeafHelpPrintsUsageWithoutLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		wants []string
	}{
		{
			name: "create",
			args: []string{"approach", "flow", "create", "--help"},
			wants: []string{
				"Usage: approach flow create [flags]",
				"--title TITLE",
				"--instructions TEXT",
			},
		},
		{
			name: "list",
			args: []string{"approach", "flow", "list", "--help"},
			wants: []string{
				"Usage: approach flow list [flags]",
				"--json",
				"--repo-path PATH",
			},
		},
		{
			name: "read",
			args: []string{"approach", "flow", "read", "--help"},
			wants: []string{
				"Usage: approach flow read [flags]",
				"--flow-id FLOW_ID",
				"approach flow read --flow-id",
			},
		},
		{
			name: "phase add-child",
			args: []string{"approach", "flow", "phase", "add-child", "--help"},
			wants: []string{
				"Usage: approach flow phase add-child [flags]",
				"--phase-id PHASE_ID",
				"--order N",
			},
		},
		{
			name: "phase reset",
			args: []string{"approach", "flow", "phase", "reset", "--help"},
			wants: []string{
				"Usage: approach flow phase reset [flags]",
				"--flow-id FLOW_ID",
				"--phase-id PHASE_ID",
				"approach flow phase reset --flow-id",
			},
		},
		{
			name: "plan set",
			args: []string{"approach", "flow", "plan", "set", "--help"},
			wants: []string{
				"Usage: approach flow plan set [flags]",
				"--flow-id FLOW_ID",
				"--plan-id PLAN_ID",
			},
		},
		{
			name: "merge set",
			args: []string{"approach", "flow", "merge", "set", "--help"},
			wants: []string{
				"Usage: approach flow merge set [flags]",
				"--status STATUS",
				"--merged-at RFC3339_TIMESTAMP",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run for flow leaf help")
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

func TestRunFlowUnknownSubcommandSuggestsNearbyCommand(t *testing.T) {
	err := run([]string{"approach", "flow", "phaze"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for unknown flow subcommand")
			return config.Config{}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	requireContainsAll(t, err.Error(), []string{
		`unknown command "phaze"; did you mean "phase"?`,
		"Usage: approach flow <create|list|read|phase|plan|issue|pr|merge> [flags]",
		"issue set",
	})
}

func TestRunFlowPhaseUnknownSubcommandSuggestsNearbyCommand(t *testing.T) {
	err := run([]string{"approach", "flow", "phase", "ste"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for unknown flow phase subcommand")
			return config.Config{}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	requireContainsAll(t, err.Error(), []string{
		`unknown command "ste"; did you mean "set"?`,
		"Usage: approach flow phase <set|complete|block|needs-attention|restart|reset|add-child> [flags]",
	})
}

func TestRunFlowPhaseUnknownSubcommandSuggestsReset(t *testing.T) {
	err := run([]string{"approach", "flow", "phase", "rese"}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			t.Fatal("loadConfig should not run for unknown flow phase subcommand")
			return config.Config{}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown subcommand error")
	}
	requireContainsAll(t, err.Error(), []string{
		`unknown command "rese"; did you mean "reset"?`,
		"reset",
	})
}

func TestRunFlowNestedSetSubcommandsSuggestSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "plan", args: []string{"approach", "flow", "plan", "sete"}},
		{name: "issue", args: []string{"approach", "flow", "issue", "sete"}},
		{name: "pr", args: []string{"approach", "flow", "pr", "sete"}},
		{name: "merge", args: []string{"approach", "flow", "merge", "sete"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run for unknown flow nested subcommand")
					return config.Config{}, nil
				},
				stdout: &bytes.Buffer{},
			}))
			if err == nil {
				t.Fatal("expected unknown subcommand error")
			}
			requireContainsAll(t, err.Error(), []string{
				`unknown command "sete"; did you mean "set"?`,
				"Usage: approach flow",
			})
		})
	}
}

func TestRunFlowCreatePrintsJSONRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	instructionsFile := filepath.Join(root, "instructions.md")
	if err := os.WriteFile(instructionsFile, []byte("Build the thing.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "create",
		"--title", "Add Flow Mode",
		"--instructions-file", instructionsFile,
		"--repo-path", repoPath,
		"--worktree-path", filepath.Join(root, "repo-worktrees", "flow-add-flow-mode"),
		"--branch", "flow/add-flow-mode",
		"--base-ref", "main",
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if record.FlowID == "" ||
		record.Title != "Add Flow Mode" ||
		record.Instructions != "Build the thing.\n" ||
		record.RepoPath != repoPath ||
		record.WorktreePath != filepath.Join(root, "repo-worktrees", "flow-add-flow-mode") ||
		record.Branch != "flow/add-flow-mode" ||
		record.BaseRef != "main" ||
		record.Status != flowstore.StatusPending ||
		!record.AutoMode ||
		!record.Headless ||
		len(record.Phases) != 7 {
		t.Fatalf("unexpected flow record: %#v", record)
	}
	if _, err := os.Stat(filepath.Join(root, "approach.db")); err != nil {
		t.Fatalf("expected persisted Flow database: %v", err)
	}
	if _, err := mustFlowStore(t, root).Read(record.FlowID); err != nil {
		t.Fatalf("expected persisted Flow record: %v", err)
	}
}

func TestRunFlowReadWithExplicitStateRootUsesRequestedRoot(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Readable", "--instructions", "read it", "--repo-path", repoPath, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
		noScanDeps(t, runDeps{
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), created.FlowID) {
		t.Fatalf("flow read output missing flow ID %q: %s", created.FlowID, stdout.String())
	}
}

func TestRunFlowListJSONFiltersByRepo(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	bravo := filepath.Join(root, "bravo")
	mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Alpha", "--instructions", "alpha", "--repo-path", alpha, "--json", "--state-root", root})
	mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Bravo", "--instructions", "bravo", "--repo-path", bravo, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "list", "--repo-path", alpha, "--json", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var records []flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("output is not JSON array: %v\n%s", err, stdout.String())
	}
	if len(records) != 1 || records[0].Title != "Alpha" || records[0].RepoPath != alpha {
		t.Fatalf("expected only Alpha for repo %s, got %#v", alpha, records)
	}
}

func TestRunFlowListRequiresJSON(t *testing.T) {
	err := run([]string{"approach", "flow", "list", "--state-root", t.TempDir()},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error requiring --json")
	}
	if !strings.Contains(err.Error(), "json") {
		t.Fatalf("expected --json requirement error, got %q", err)
	}
}

func TestRunFlowReadPrintsJSONRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Readable", "--instructions", "read it", "--repo-path", repoPath, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if read.FlowID != created.FlowID || read.Title != "Readable" || read.RepoPath != repoPath {
		t.Fatalf("read record mismatch: %#v", read)
	}
}

func TestRunFlowPlanSetLinksPlanArtifact(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	planPath := filepath.Join(root, "plans", "plan-1", "plan.md")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Plan Link", "--instructions", "plan it", "--repo-path", repoPath, "--json", "--state-root", root})
	savePlanArtifact(t, root, "plan-1")

	var linkedAt string
	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		args := []string{
			"approach", "flow", "plan", "set",
			"--flow-id", created.FlowID,
			"--plan-id", "plan-1",
			"--state-root", root,
		}
		if i == 1 {
			args = append(args, "--plan-path", planPath)
		}
		err := run(args, noScanDeps(t, runDeps{stdout: &stdout}))
		if err != nil {
			t.Fatalf("run returned error on attempt %d: %v", i+1, err)
		}
		var updated flowstore.FlowRecord
		if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
			t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
		}
		if updated.PlanID != "plan-1" || updated.PlanPath != planPath {
			t.Fatalf("linked plan = (%q, %q), want plan-1 and %q", updated.PlanID, updated.PlanPath, planPath)
		}
		if i == 0 {
			linkedAt = updated.UpdatedAt.Format(time.RFC3339Nano)
		} else if got := updated.UpdatedAt.Format(time.RFC3339Nano); got != linkedAt {
			t.Fatalf("idempotent retry changed UpdatedAt from %s to %s", linkedAt, got)
		}
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.PlanID != "plan-1" || read.PlanPath != planPath {
		t.Fatalf("persisted linked plan = (%q, %q), want plan-1 and %q", read.PlanID, read.PlanPath, planPath)
	}
	if got := read.UpdatedAt.Format(time.RFC3339Nano); got != linkedAt {
		t.Fatalf("persisted UpdatedAt = %s, want idempotent retry to preserve %s", got, linkedAt)
	}
}

func TestRunFlowPlanSetValidatesInputsAndKeepsRecordUnchanged(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Plan Link Validation", "--instructions", "plan it", "--repo-path", repoPath, "--json", "--state-root", root})
	savePlanArtifact(t, root, "plan-1")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing plan id",
			args: []string{"approach", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-path", filepath.Join(root, "plans", "plan-1", "plan.md"), "--state-root", root},
			want: "requires --plan-id",
		},
		{
			name: "missing plan",
			args: []string{"approach", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "missing-plan", "--state-root", root},
			want: `plan "missing-plan" not found`,
		},
		{
			name: "relative plan path",
			args: []string{"approach", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "plan-1", "--plan-path", "plans/plan-1/plan.md", "--state-root", root},
			want: "flow plan path must be absolute",
		},
		{
			name: "mismatched plan path",
			args: []string{"approach", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "plan-1", "--plan-path", filepath.Join(root, "plans", "other", "plan.md"), "--state-root", root},
			want: "does not match plan",
		},
		{
			name: "missing flow",
			args: []string{"approach", "flow", "plan", "set", "--flow-id", "missing-flow", "--plan-id", "plan-1", "--plan-path", filepath.Join(root, "plans", "plan-1", "plan.md"), "--state-root", root},
			want: `flow "missing-flow" not found`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.PlanID != "" || read.PlanPath != "" {
		t.Fatalf("rejected plan link should not mutate record: %#v", read)
	}
}

func TestRunFlowPRSetPrintsJSONRecordAndUngatesAutoreview(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "PR metadata",
		"--instructions", "open a pull request",
		"--repo-path", repoPath,
		"--branch", "flow/pr-metadata",
		"--json",
		"--state-root", root,
	})
	for _, phaseID := range []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation"} {
		outcome := ""
		if phaseID == "plan-review" {
			outcome = flowstore.OutcomeApproved
		}
		mustSetFlowPhase(t, root, created.FlowID, phaseID, flowstore.PhaseCompleted, outcome, "", "")
	}

	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "pr", "set",
		"--flow-id", created.FlowID,
		"--provider", "github",
		"--number", "115",
		"--url", "https://github.com/approachcontrol/approach/pull/115",
		"--head", "flow/pr-metadata",
		"--base", "main",
		"--status", "open",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var updated flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if updated.PR.Provider != "github" ||
		updated.PR.Number != 115 ||
		updated.PR.URL != "https://github.com/approachcontrol/approach/pull/115" ||
		updated.PR.HeadBranch != "flow/pr-metadata" ||
		updated.PR.BaseBranch != "main" ||
		updated.PR.Status != "open" {
		t.Fatalf("PR metadata = %#v", updated.PR)
	}
	if got := phaseByID(updated, "autoreview").Status; got != flowstore.PhaseReady {
		t.Fatalf("autoreview status = %q, want ready", got)
	}
}

func TestRunFlowPRSetValidatesRequiredInputs(t *testing.T) {
	root := t.TempDir()
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "PR validation",
		"--instructions", "validate input",
		"--repo-path", filepath.Join(root, "repo"),
		"--branch", "flow/pr-validation",
		"--json",
		"--state-root", root,
	})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing number",
			args: []string{"approach", "flow", "pr", "set", "--flow-id", created.FlowID, "--provider", "github", "--url", "https://github.com/approachcontrol/approach/pull/115", "--head", "flow/pr-validation", "--base", "main", "--state-root", root},
			want: "requires positive --number",
		},
		{
			name: "missing url",
			args: []string{"approach", "flow", "pr", "set", "--flow-id", created.FlowID, "--provider", "github", "--number", "115", "--head", "flow/pr-validation", "--base", "main", "--state-root", root},
			want: "requires --url",
		},
		{
			name: "branch mismatch",
			args: []string{"approach", "flow", "pr", "set", "--flow-id", created.FlowID, "--provider", "github", "--number", "115", "--url", "https://github.com/approachcontrol/approach/pull/115", "--head", "feature/other", "--base", "main", "--state-root", root},
			want: "must match flow branch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunFlowIssueSetPrintsJSONRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "Issue metadata",
		"--instructions", "link an issue",
		"--repo-path", repoPath,
		"--branch", "flow/issue-metadata",
		"--json",
		"--state-root", root,
	})

	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "issue", "set",
		"--flow-id", created.FlowID,
		"--provider", "github",
		"--number", "123",
		"--url", "https://github.com/approachcontrol/approach/issues/123",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var updated flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if updated.Issue.Provider != "github" ||
		updated.Issue.Number != 123 ||
		updated.Issue.URL != "https://github.com/approachcontrol/approach/issues/123" {
		t.Fatalf("Issue metadata = %#v", updated.Issue)
	}

	stdout.Reset()
	err = run([]string{"approach", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
		noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("flow read returned error: %v", err)
	}
	var read flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &read); err != nil {
		t.Fatalf("read output is not JSON record: %v\n%s", err, stdout.String())
	}
	if read.Issue != updated.Issue {
		t.Fatalf("read issue = %#v, want %#v", read.Issue, updated.Issue)
	}
}

func TestRunFlowIssueSetValidatesRequiredInputs(t *testing.T) {
	root := t.TempDir()
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "Issue validation",
		"--instructions", "validate input",
		"--repo-path", filepath.Join(root, "repo"),
		"--branch", "flow/issue-validation",
		"--json",
		"--state-root", root,
	})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing flow id",
			args: []string{"approach", "flow", "issue", "set", "--provider", "github", "--number", "123", "--url", "https://github.com/approachcontrol/approach/issues/123", "--state-root", root},
			want: "requires --flow-id",
		},
		{
			name: "missing number",
			args: []string{"approach", "flow", "issue", "set", "--flow-id", created.FlowID, "--provider", "github", "--url", "https://github.com/approachcontrol/approach/issues/123", "--state-root", root},
			want: "requires positive --number",
		},
		{
			name: "missing url",
			args: []string{"approach", "flow", "issue", "set", "--flow-id", created.FlowID, "--provider", "github", "--number", "123", "--state-root", root},
			want: "requires --url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunFlowMergeSetPrintsJSONRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "Merge metadata",
		"--instructions", "merge deliberately",
		"--repo-path", repoPath,
		"--branch", "flow/merge-metadata",
		"--json",
		"--state-root", root,
	})
	for _, phaseID := range []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation"} {
		outcome := ""
		if phaseID == "plan-review" {
			outcome = flowstore.OutcomeApproved
		}
		mustSetFlowPhase(t, root, created.FlowID, phaseID, flowstore.PhaseCompleted, outcome, "", "")
	}
	mustRunFlow(t, []string{
		"approach", "flow", "pr", "set",
		"--flow-id", created.FlowID,
		"--provider", "github",
		"--number", "116",
		"--url", "https://github.com/approachcontrol/approach/pull/116",
		"--head", "flow/merge-metadata",
		"--base", "main",
		"--status", "open",
		"--state-root", root,
	})
	mustSetFlowPhase(t, root, created.FlowID, "autoreview", flowstore.PhaseCompleted, "passed", "", "")
	mustSetFlowPhase(t, root, created.FlowID, "merge", flowstore.PhaseCompleted, "merged", "", "")

	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "merge", "set",
		"--flow-id", created.FlowID,
		"--status", "merged",
		"--commit", "0123456789abcdef",
		"--merged-at", "2026-06-08T15:04:05Z",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var updated flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if updated.Status != flowstore.StatusMerged ||
		updated.Merge.Status != flowstore.MergeMerged ||
		updated.Merge.Commit != "0123456789abcdef" ||
		updated.Merge.MergedAt == nil ||
		updated.Merge.MergedAt.Format(time.RFC3339) != "2026-06-08T15:04:05Z" {
		t.Fatalf("updated merge record = %#v", updated)
	}
}

func TestRunFlowMergeSetValidatesInputs(t *testing.T) {
	root := t.TempDir()
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", "Merge validation",
		"--instructions", "validate merge input",
		"--repo-path", filepath.Join(root, "repo"),
		"--branch", "flow/merge-validation",
		"--json",
		"--state-root", root,
	})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing status",
			args: []string{"approach", "flow", "merge", "set", "--flow-id", created.FlowID, "--commit", "abc123", "--merged-at", "2026-06-08T15:04:05Z", "--state-root", root},
			want: "requires --status",
		},
		{
			name: "missing commit",
			args: []string{"approach", "flow", "merge", "set", "--flow-id", created.FlowID, "--status", flowstore.MergeMerged, "--merged-at", "2026-06-08T15:04:05Z", "--state-root", root},
			want: "requires --commit",
		},
		{
			name: "missing merged at",
			args: []string{"approach", "flow", "merge", "set", "--flow-id", created.FlowID, "--status", flowstore.MergeMerged, "--commit", "abc123", "--state-root", root},
			want: "requires --merged-at",
		},
		{
			name: "bad merged at",
			args: []string{"approach", "flow", "merge", "set", "--flow-id", created.FlowID, "--status", flowstore.MergeMerged, "--commit", "abc123", "--merged-at", "not-a-time", "--state-root", root},
			want: "invalid --merged-at",
		},
		{
			name: "missing flow",
			args: []string{"approach", "flow", "merge", "set", "--flow-id", "missing-flow", "--status", flowstore.MergeMerged, "--commit", "abc123", "--merged-at", "2026-06-08T15:04:05Z", "--state-root", root},
			want: `flow "missing-flow" not found`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunFlowPhaseSetUpdatesAgentFacingStatus(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name     string
		status   string
		notes    string
		wantFlow string
	}{
		{name: "running", status: flowstore.PhaseRunning, wantFlow: flowstore.StatusInProgress},
		{name: "completed", status: flowstore.PhaseCompleted, wantFlow: flowstore.StatusInProgress},
		{name: "needs attention", status: flowstore.PhaseNeedsAttention, wantFlow: flowstore.StatusNeedsAttention},
		{name: "blocked", status: flowstore.PhaseBlocked, wantFlow: flowstore.StatusBlocked},
		{name: "skipped", status: flowstore.PhaseSkipped, notes: "Existing plan is approved.", wantFlow: flowstore.StatusInProgress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := filepath.Join(root, "repo-"+tc.name)
			created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

			args := []string{
				"approach", "flow", "phase", "set",
				"--flow-id", created.FlowID,
				"--phase-id", "plan",
				"--status", tc.status,
				"--summary", "Phase updated.",
				"--state-root", root,
			}
			if tc.notes != "" {
				args = append(args, "--notes", tc.notes)
			}
			var stdout bytes.Buffer
			err := run(args, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			var updated flowstore.FlowRecord
			if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
				t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
			}
			if updated.Phases[0].Status != tc.status || updated.Phases[0].Summary != "Phase updated." {
				t.Fatalf("updated phase = %#v", updated.Phases[0])
			}
			if tc.notes != "" && updated.Phases[0].Notes != tc.notes {
				t.Fatalf("phase notes = %q, want %q", updated.Phases[0].Notes, tc.notes)
			}
			if updated.Status != tc.wantFlow {
				t.Fatalf("flow status = %q, want %q", updated.Status, tc.wantFlow)
			}
		})
	}
}

func TestRunFlowPhaseSetImplementationOutcomesAfterApprovedReview(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name             string
		status           string
		wantFlow         string
		wantReviewStatus string
	}{
		{name: "completed", status: flowstore.PhaseCompleted, wantFlow: flowstore.StatusInProgress, wantReviewStatus: flowstore.PhaseReady},
		{name: "needs attention", status: flowstore.PhaseNeedsAttention, wantFlow: flowstore.StatusNeedsAttention, wantReviewStatus: flowstore.PhasePending},
		{name: "blocked", status: flowstore.PhaseBlocked, wantFlow: flowstore.StatusBlocked, wantReviewStatus: flowstore.PhasePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := filepath.Join(root, "repo-implementation-"+strings.ReplaceAll(tc.name, " ", "-"))
			created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})
			mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseCompleted, "", "", "")
			mustSetFlowPhase(t, root, created.FlowID, "plan-review", flowstore.PhaseCompleted, "approved", "", "")

			var stdout bytes.Buffer
			err := run([]string{
				"approach", "flow", "phase", "set",
				"--flow-id", created.FlowID,
				"--phase-id", "implementation",
				"--status", tc.status,
				"--summary", "Implementation updated.",
				"--state-root", root,
			}, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			var updated flowstore.FlowRecord
			if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
				t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
			}
			if updated.Status != tc.wantFlow {
				t.Fatalf("flow status = %q, want %q", updated.Status, tc.wantFlow)
			}
			if phaseByID(updated, "implementation").Status != tc.status {
				t.Fatalf("implementation phase = %#v", phaseByID(updated, "implementation"))
			}
			if phaseByID(updated, "review-loop").Status != tc.wantReviewStatus {
				t.Fatalf("review-loop status = %q, want %q", phaseByID(updated, "review-loop").Status, tc.wantReviewStatus)
			}
		})
	}
}

func TestRunFlowPhaseActionCompletePrintsNextActionablePhase(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Action Complete", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "phase", "complete",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--summary", "Saved the implementation plan.",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
	}
	if result.FlowID != created.FlowID || result.FlowStatus != flowstore.StatusInProgress {
		t.Fatalf("result flow state = %#v", result)
	}
	if result.UpdatedPhase.PhaseID != "plan" ||
		result.UpdatedPhase.Status != flowstore.PhaseCompleted ||
		result.UpdatedPhase.Summary != "Saved the implementation plan." {
		t.Fatalf("updated phase = %#v", result.UpdatedPhase)
	}
	if result.NextPhase == nil {
		t.Fatal("next phase is nil, want plan-review")
	}
	if result.NextPhase.PhaseID != "plan-review" || result.NextPhase.Status != flowstore.PhaseReady {
		t.Fatalf("next phase = %#v, want ready plan-review", result.NextPhase)
	}
	if strings.Join(result.NextPhase.AllowedStatuses, ",") != strings.Join(flowstore.AllowedNextPhaseStatuses(flowstore.PhaseReady), ",") {
		t.Fatalf("next phase allowed statuses = %#v", result.NextPhase.AllowedStatuses)
	}
	if phaseByID(result.Flow, "plan-review").Status != flowstore.PhaseReady {
		t.Fatalf("embedded flow plan-review = %#v", phaseByID(result.Flow, "plan-review"))
	}
}

func TestRunFlowPhaseActionsMapToCanonicalStatuses(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name       string
		command    string
		wantStatus string
		notes      string
	}{
		{name: "block with notes", command: "block", wantStatus: flowstore.PhaseBlocked, notes: "Waiting on reviewer input."},
		{name: "block without notes", command: "block", wantStatus: flowstore.PhaseBlocked},
		{name: "needs attention with notes", command: "needs-attention", wantStatus: flowstore.PhaseNeedsAttention, notes: "Revise the generated plan."},
		{name: "needs attention without notes", command: "needs-attention", wantStatus: flowstore.PhaseNeedsAttention},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := filepath.Join(root, "repo-"+strings.ReplaceAll(tc.name, " ", "-"))
			created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

			args := []string{
				"approach", "flow", "phase", tc.command,
				"--flow-id", created.FlowID,
				"--phase-id", "plan",
				"--state-root", root,
			}
			if tc.notes != "" {
				args = append(args, "--notes", tc.notes)
			}
			var stdout bytes.Buffer
			err := run(args, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			var result flowPhaseActionResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
			}
			if result.UpdatedPhase.Status != tc.wantStatus || result.UpdatedPhase.Notes != tc.notes {
				t.Fatalf("updated phase = %#v", result.UpdatedPhase)
			}
			if result.NextPhase == nil || result.NextPhase.PhaseID != "plan" || result.NextPhase.Status != tc.wantStatus {
				t.Fatalf("next phase = %#v, want current phase still actionable", result.NextPhase)
			}
			if strings.Join(result.NextPhase.AllowedStatuses, ",") != strings.Join(flowstore.AllowedNextPhaseStatuses(tc.wantStatus), ",") {
				t.Fatalf("next phase allowed statuses = %#v", result.NextPhase.AllowedStatuses)
			}
		})
	}
}

func TestRunFlowPhaseActionsDefaultPlanReviewOutcomes(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name        string
		command     string
		outcome     string
		wantStatus  string
		wantOutcome string
		notes       string
	}{
		{name: "complete", command: "complete", wantStatus: flowstore.PhaseCompleted, wantOutcome: flowstore.OutcomeApproved},
		{name: "complete with concerns", command: "complete", outcome: flowstore.OutcomeApprovedWithConcerns, wantStatus: flowstore.PhaseCompleted, wantOutcome: flowstore.OutcomeApprovedWithConcerns, notes: "Proceed, but keep rollout staged."},
		{name: "block", command: "block", wantStatus: flowstore.PhaseBlocked, wantOutcome: flowstore.OutcomeBlocked, notes: "Waiting on a product decision."},
		{name: "needs attention", command: "needs-attention", wantStatus: flowstore.PhaseNeedsAttention, wantOutcome: flowstore.OutcomeChangesRequested, notes: "Revise the plan."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := filepath.Join(root, "repo-plan-review-"+strings.ReplaceAll(tc.name, " ", "-"))
			created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})
			mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseCompleted, "", "", "")

			args := []string{
				"approach", "flow", "phase", tc.command,
				"--flow-id", created.FlowID,
				"--phase-id", "plan-review",
				"--state-root", root,
			}
			if tc.outcome != "" {
				args = append(args, "--outcome", tc.outcome)
			}
			if tc.notes != "" {
				args = append(args, "--notes", tc.notes)
			}
			var stdout bytes.Buffer
			err := run(args, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			var result flowPhaseActionResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
			}
			review := result.UpdatedPhase
			if review.Status != tc.wantStatus || review.Outcome != tc.wantOutcome {
				t.Fatalf("plan-review = %#v, want status %q outcome %q", review, tc.wantStatus, tc.wantOutcome)
			}
		})
	}
}

func TestRunFlowPhaseActionsDefaultOutcomesByKind(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(flowstore.FlowRecord{
		FlowID:       "custom-action-outcomes",
		Title:        "Custom Action Outcomes",
		Instructions: "default outcomes by kind",
		RepoPath:     filepath.Join(root, "repo"),
		Branch:       "flow/custom-action-outcomes",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "design-review", Title: "Design Review", Kind: flowstore.KindPlanReview, DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "second-review", Title: "Second Review", Kind: flowstore.KindAutoreview, DependsOn: []string{}, Status: flowstore.PhaseReady, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var stdout bytes.Buffer
	err = run([]string{
		"approach", "flow", "phase", "complete",
		"--flow-id", record.FlowID,
		"--phase-id", "design-review",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run plan-review-kind action returned error: %v", err)
	}
	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
	}
	if result.UpdatedPhase.Outcome != flowstore.OutcomeApproved {
		t.Fatalf("design-review outcome = %q, want approved", result.UpdatedPhase.Outcome)
	}

	stdout.Reset()
	err = run([]string{
		"approach", "flow", "phase", "complete",
		"--flow-id", record.FlowID,
		"--phase-id", "second-review",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run autoreview-kind action returned error: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
	}
	if result.UpdatedPhase.Outcome != "passed" {
		t.Fatalf("second-review outcome = %q, want passed", result.UpdatedPhase.Outcome)
	}
}

func TestRunFlowPhaseActionsDefaultAutoreviewOutcomes(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name        string
		command     string
		notes       string
		wantStatus  string
		wantOutcome string
	}{
		{name: "complete", command: "complete", wantStatus: flowstore.PhaseCompleted, wantOutcome: "passed"},
		{name: "needs attention", command: "needs-attention", notes: "Follow-up concern remains.", wantStatus: flowstore.PhaseNeedsAttention, wantOutcome: "needs_attention"},
		{name: "block", command: "block", notes: "Autoreview cannot inspect the PR.", wantStatus: flowstore.PhaseBlocked, wantOutcome: flowstore.OutcomeBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			branch := "flow/autoreview-" + strings.ReplaceAll(tc.name, " ", "-")
			created := mustRunFlowReadyForAutoreview(t, root, tc.name, branch)

			args := []string{
				"approach", "flow", "phase", tc.command,
				"--flow-id", created.FlowID,
				"--phase-id", "autoreview",
				"--state-root", root,
			}
			if tc.notes != "" {
				args = append(args, "--notes", tc.notes)
			}
			var stdout bytes.Buffer
			err := run(args, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			var result flowPhaseActionResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
			}
			autoreview := result.UpdatedPhase
			if autoreview.Status != tc.wantStatus || autoreview.Outcome != tc.wantOutcome {
				t.Fatalf("autoreview = %#v, want status %q outcome %q", autoreview, tc.wantStatus, tc.wantOutcome)
			}
		})
	}
}

func TestRunFlowPhaseRestartRerunsAttentionAndBlockedPhases(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name         string
		startStatus  string
		startOutcome string
		startNotes   string
	}{
		{
			name:         "needs attention",
			startStatus:  flowstore.PhaseNeedsAttention,
			startOutcome: "needs_attention",
			startNotes:   "Follow-up concern remains.",
		},
		{
			name:         "blocked",
			startStatus:  flowstore.PhaseBlocked,
			startOutcome: flowstore.OutcomeBlocked,
			startNotes:   "Autoreview was blocked.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			branch := "flow/restart-" + strings.ReplaceAll(tc.name, " ", "-")
			created := mustRunFlowReadyForAutoreview(t, root, tc.name, branch)
			mustSetFlowPhase(t, root, created.FlowID, "autoreview", tc.startStatus, tc.startOutcome, tc.startNotes, "")

			var stdout bytes.Buffer
			err := run([]string{
				"approach", "flow", "phase", "restart",
				"--flow-id", created.FlowID,
				"--phase-id", "autoreview",
				"--state-root", root,
			}, noScanDeps(t, runDeps{stdout: &stdout}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}

			var result flowPhaseActionResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
			}
			autoreview := result.UpdatedPhase
			if autoreview.Status != flowstore.PhaseRunning || autoreview.Outcome != "" {
				t.Fatalf("autoreview = %#v, want running with cleared outcome", autoreview)
			}
			if !strings.Contains(autoreview.Notes, "Rerunning Autoreview after addressing prior findings.") {
				t.Fatalf("autoreview notes = %q, want default rerun note", autoreview.Notes)
			}
		})
	}
}

func TestRunFlowPhaseRestartRejectsNonRecoveryStates(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name        string
		prepare     func(t *testing.T) flowstore.FlowRecord
		wantCurrent string
	}{
		{
			name: "pending",
			prepare: func(t *testing.T) flowstore.FlowRecord {
				return mustRunFlow(t, []string{
					"approach", "flow", "create",
					"--title", "pending restart",
					"--instructions", "phase it",
					"--repo-path", filepath.Join(root, "repo-pending"),
					"--json",
					"--state-root", root,
				})
			},
			wantCurrent: "pending",
		},
		{
			name: "ready",
			prepare: func(t *testing.T) flowstore.FlowRecord {
				return mustRunFlowReadyForAutoreview(t, root, "ready restart", "flow/restart-ready")
			},
			wantCurrent: "ready",
		},
		{
			name: "completed",
			prepare: func(t *testing.T) flowstore.FlowRecord {
				record := mustRunFlowReadyForAutoreview(t, root, "completed restart", "flow/restart-completed")
				return mustSetFlowPhase(t, root, record.FlowID, "autoreview", flowstore.PhaseCompleted, "passed", "", "")
			},
			wantCurrent: "completed",
		},
		{
			name: "skipped",
			prepare: func(t *testing.T) flowstore.FlowRecord {
				record := mustRunFlowReadyForAutoreview(t, root, "skipped restart", "flow/restart-skipped")
				return mustSetFlowPhase(t, root, record.FlowID, "autoreview", flowstore.PhaseSkipped, "", "", "Autoreview intentionally skipped.")
			},
			wantCurrent: "skipped",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := tc.prepare(t)
			err := run([]string{
				"approach", "flow", "phase", "restart",
				"--flow-id", record.FlowID,
				"--phase-id", "autoreview",
				"--state-root", root,
			}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil {
				t.Fatal("restart returned nil error for non-recovery state")
			}
			for _, want := range []string{
				"flow phase restart requires current status needs_attention or blocked",
				"autoreview is " + tc.wantCurrent,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("restart error = %q, want %q", err.Error(), want)
				}
			}
		})
	}
}

func TestRunFlowPhaseResetReturnsEndedSessionPhaseToReady(t *testing.T) {
	root := t.TempDir()
	record := mustRunFlowReadyForImplementation(t, root, "reset ended", "flow/reset-ended")
	store := mustFlowStore(t, root)
	var err error
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-old"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(old) error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{Provider: "codex", SessionID: "session-old", LaunchID: "launch-old", Status: "ended"},
	})
	if err != nil {
		t.Fatalf("AttachSession(old) error = %v", err)
	}
	record, err = store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-ended"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(ended) error = %v", err)
	}
	record, err = store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  record.FlowID,
		PhaseID: "implementation",
		Session: flowstore.Session{Provider: "claude", SessionID: "session-ended", LaunchID: "launch-ended", Status: "ended"},
	})
	if err != nil {
		t.Fatalf("AttachSession(ended) error = %v", err)
	}

	var stdout bytes.Buffer
	err = run([]string{
		"approach", "flow", "phase", "reset",
		"--flow-id", record.FlowID,
		"--phase-id", "implementation",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
	}
	implementation := result.UpdatedPhase
	if implementation.Status != flowstore.PhaseReady {
		t.Fatalf("updated phase = %#v, want ready", implementation)
	}
	if len(implementation.LaunchIDs) != 1 || implementation.LaunchIDs[0] != "launch-old" {
		t.Fatalf("launch ids = %#v, want old launch only", implementation.LaunchIDs)
	}
	if len(implementation.Sessions) != 1 || implementation.Sessions[0].SessionID != "session-old" {
		t.Fatalf("sessions = %#v, want old session only", implementation.Sessions)
	}
	if result.NextPhase == nil || result.NextPhase.PhaseID != "implementation" || result.NextPhase.Status != flowstore.PhaseReady ||
		!slices.Contains(result.NextPhase.AllowedStatuses, flowstore.PhaseRunning) {
		t.Fatalf("next phase = %#v, want ready implementation with running allowed", result.NextPhase)
	}
}

func TestRunFlowPhaseResetReturnsAwaitingSessionPhaseToReady(t *testing.T) {
	root := t.TempDir()
	record := mustRunFlowReadyForImplementation(t, root, "reset awaiting", "flow/reset-awaiting")
	store := mustFlowStore(t, root)
	record, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-orphan"})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}

	var stdout bytes.Buffer
	err = run([]string{
		"approach", "flow", "phase", "reset",
		"--flow-id", record.FlowID,
		"--phase-id", "implementation",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var result flowPhaseActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON action result: %v\n%s", err, stdout.String())
	}
	if result.UpdatedPhase.Status != flowstore.PhaseReady || len(result.UpdatedPhase.LaunchIDs) != 0 {
		t.Fatalf("updated phase = %#v, want ready with orphan removed", result.UpdatedPhase)
	}
}

func TestRunFlowPhaseResetRejectsIneligiblePhasesWithoutChangingRecord(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) flowstore.FlowRecord
		want    string
	}{
		{
			name: "live latest session",
			prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) flowstore.FlowRecord {
				t.Helper()
				record, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-live"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID() error = %v", err)
				}
				record, err = store.AttachSession(flowstore.SessionAttachUpdate{
					FlowID:  record.FlowID,
					PhaseID: "implementation",
					Session: flowstore.Session{Provider: "codex", SessionID: "session-live", LaunchID: "launch-live", Status: "running"},
				})
				if err != nil {
					t.Fatalf("AttachSession() error = %v", err)
				}
				return record
			},
			want: "requires latest launch without a live attached session",
		},
		{
			name: "session mismatch",
			prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) flowstore.FlowRecord {
				t.Helper()
				record, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: record.FlowID, PhaseID: "implementation", LaunchID: "launch-current"})
				if err != nil {
					t.Fatalf("AddPhaseLaunchID() error = %v", err)
				}
				record.Phases = append([]flowstore.FlowPhase(nil), record.Phases...)
				for i := range record.Phases {
					if record.Phases[i].PhaseID == "implementation" {
						record.Phases[i].Sessions = []flowstore.Session{{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"}}
					}
				}
				if err := overwriteFlowRecord(t, root, record); err != nil {
					t.Fatalf("overwriteFlowRecord() error = %v", err)
				}
				return record
			},
			want: "requires attached sessions to match phase launch ids",
		},
		{
			name: "unsatisfied predecessors",
			prepare: func(t *testing.T, store *flowstore.Store, record flowstore.FlowRecord) flowstore.FlowRecord {
				t.Helper()
				record.Phases = []flowstore.FlowPhase{
					{PhaseID: "alpha", Title: "Alpha", DependsOn: []string{}, Status: flowstore.PhaseRunning, Order: 1},
					{PhaseID: "implementation", Title: "Implementation", DependsOn: []string{"alpha"}, Status: flowstore.PhaseRunning, Order: 2, LaunchIDs: []string{"launch-orphan"}},
				}
				if err := overwriteFlowRecord(t, root, record); err != nil {
					t.Fatalf("overwriteFlowRecord() error = %v", err)
				}
				return record
			},
			want: "requires satisfied predecessors",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title := "reset reject " + tc.name
			record := mustRunFlowReadyForImplementation(t, root, title, "flow/"+strings.ReplaceAll(title, " ", "-"))
			store := mustFlowStore(t, root)
			record = tc.prepare(t, store, record)
			before := phaseByID(record, "implementation")

			err := run([]string{
				"approach", "flow", "phase", "reset",
				"--flow-id", record.FlowID,
				"--phase-id", "implementation",
				"--state-root", root,
			}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
			read, err := store.Read(record.FlowID)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			after := phaseByID(read, "implementation")
			wantAfterStatus := before.Status
			if after.Status != wantAfterStatus || strings.Join(before.LaunchIDs, ",") != strings.Join(after.LaunchIDs, ",") || len(before.Sessions) != len(after.Sessions) {
				t.Fatalf("record changed after rejected reset: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestRunFlowPhaseActionRejectsInvalidTransitionAndKeepsRecordUnchanged(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Invalid Action", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	err := run([]string{
		"approach", "flow", "phase", "complete",
		"--flow-id", created.FlowID,
		"--phase-id", "plan-review",
		"--outcome", flowstore.OutcomeApproved,
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil || !strings.Contains(err.Error(), "invalid phase transition pending -> completed") {
		t.Fatalf("run error = %v, want invalid transition", err)
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if phaseByID(read, "plan-review").Status != flowstore.PhasePending {
		t.Fatalf("plan-review status after rejected action = %q, want pending", phaseByID(read, "plan-review").Status)
	}
}

func TestRunFlowPhaseActionsRejectInvalidPlanReviewOutcomes(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		command string
		outcome string
		notes   string
		want    string
	}{
		{
			name:    "complete changes requested",
			command: "complete",
			outcome: flowstore.OutcomeChangesRequested,
			notes:   "Revise the plan.",
			want:    "plan-review outcome changes_requested requires needs_attention status",
		},
		{
			name:    "complete approved with concerns missing notes",
			command: "complete",
			outcome: flowstore.OutcomeApprovedWithConcerns,
			want:    "plan-review approved_with_concerns requires notes",
		},
		{
			name:    "needs attention approved",
			command: "needs-attention",
			outcome: flowstore.OutcomeApproved,
			notes:   "Looks good.",
			want:    "plan-review outcome approved requires completed status",
		},
		{
			name:    "block approved",
			command: "block",
			outcome: flowstore.OutcomeApproved,
			notes:   "Waiting.",
			want:    "plan-review blocked requires outcome blocked",
		},
		{
			name:    "needs attention missing notes",
			command: "needs-attention",
			want:    "plan-review changes_requested requires notes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoPath := filepath.Join(root, "repo-invalid-plan-review-"+strings.ReplaceAll(tc.name, " ", "-"))
			created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})
			mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseCompleted, "", "", "")

			args := []string{
				"approach", "flow", "phase", tc.command,
				"--flow-id", created.FlowID,
				"--phase-id", "plan-review",
				"--state-root", root,
			}
			if tc.outcome != "" {
				args = append(args, "--outcome", tc.outcome)
			}
			if tc.notes != "" {
				args = append(args, "--notes", tc.notes)
			}
			err := run(args, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunFlowPhaseAddChildCreatesIdempotentImplementationChild(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Children", "--instructions", "split implementation", "--repo-path", repoPath, "--json", "--state-root", root})
	mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseCompleted, "", "", "")
	mustSetFlowPhase(t, root, created.FlowID, "plan-review", flowstore.PhaseCompleted, "approved", "", "")

	var firstUpdatedAt string
	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		err := run([]string{
			"approach", "flow", "phase", "add-child",
			"--flow-id", created.FlowID,
			"--parent-phase-id", "implementation",
			"--phase-id", "implementation-api",
			"--title", "API integration",
			"--order", "10",
			"--state-root", root,
		}, noScanDeps(t, runDeps{stdout: &stdout}))
		if err != nil {
			t.Fatalf("run returned error on attempt %d: %v", i+1, err)
		}
		var updated flowstore.FlowRecord
		if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
			t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
		}
		child := phaseByID(updated, "implementation-api")
		if child.ParentPhaseID != "implementation" ||
			child.Kind != "implementation_child" ||
			child.Title != "API integration" ||
			child.Order != 10 {
			t.Fatalf("child phase = %#v", child)
		}
		if i == 0 {
			firstUpdatedAt = updated.UpdatedAt.Format(time.RFC3339Nano)
		} else if got := updated.UpdatedAt.Format(time.RFC3339Nano); got != firstUpdatedAt {
			t.Fatalf("idempotent retry changed UpdatedAt from %s to %s", firstUpdatedAt, got)
		}
	}
}

func TestRunFlowPhaseSetRestartsBlockedPhaseWithNotes(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Restart Blocked", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	err := run([]string{
		"approach", "flow", "phase", "set",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--status", flowstore.PhaseBlocked,
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err != nil {
		t.Fatalf("set blocked returned error: %v", err)
	}

	err = run([]string{
		"approach", "flow", "phase", "set",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--status", flowstore.PhaseRunning,
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil || !strings.Contains(err.Error(), "restarting blocked phase requires notes") {
		t.Fatalf("restart without notes error = %v, want notes requirement", err)
	}

	var stdout bytes.Buffer
	err = run([]string{
		"approach", "flow", "phase", "set",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--status", flowstore.PhaseRunning,
		"--notes", "Unblocked after user confirmed scope.",
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &stdout}))
	if err != nil {
		t.Fatalf("restart with notes returned error: %v", err)
	}
	var updated flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &updated); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	if updated.Phases[0].Status != flowstore.PhaseRunning {
		t.Fatalf("phase status = %q, want running", updated.Phases[0].Status)
	}
	if updated.Phases[0].Notes != "Unblocked after user confirmed scope." {
		t.Fatalf("phase notes = %q", updated.Phases[0].Notes)
	}
}

func TestRunFlowPhaseSetRejectsUnsupportedStatuses(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Reject Status", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "ready", status: flowstore.PhaseReady, want: "cannot set phase status to ready"},
		{name: "bogus", status: "done", want: `unsupported agent-facing phase status "done"; valid statuses: running, needs_attention, completed, blocked, skipped`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run([]string{
				"approach", "flow", "phase", "set",
				"--flow-id", created.FlowID,
				"--phase-id", "plan",
				"--status", tc.status,
				"--state-root", root,
			}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunFlowPhaseSetRejectsSkippedWithoutNotes(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"approach", "flow", "create", "--title", "Reject Skip", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	err := run([]string{
		"approach", "flow", "phase", "set",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--status", flowstore.PhaseSkipped,
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil || !strings.Contains(err.Error(), "skipped phase requires notes") {
		t.Fatalf("run error = %v, want skipped notes error", err)
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	read, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Phases[0].Status != flowstore.PhaseReady {
		t.Fatalf("phase status after rejected skip = %q, want ready", read.Phases[0].Status)
	}
}

func TestRunFlowCreateStateRootPrecedence(t *testing.T) {
	flowRoot := t.TempDir()
	planRoot := t.TempDir()
	sessionRoot := t.TempDir()
	configRoot := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", repoPath, "--json"},
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
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(flowRoot, "approach.db")); err != nil {
		t.Fatalf("expected flow under APPROACH_FLOW_STATE_ROOT: %v", err)
	}
	if _, err := os.Stat(filepath.Join(planRoot, "approach.db")); !os.IsNotExist(err) {
		t.Fatalf("flow should not be under plan root")
	}
}

func TestRunFlowCreateFallsBackToPlanThenSessionRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		envKey  string
		rootKey string
	}{
		{name: "plan root", envKey: "APPROACH_PLAN_STATE_ROOT", rootKey: "plan"},
		{name: "session root", envKey: "APPROACH_SESSION_STATE_ROOT", rootKey: "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := map[string]string{
				"plan":    t.TempDir(),
				"session": t.TempDir(),
			}
			repoPath := filepath.Join(t.TempDir(), "repo")
			var stdout bytes.Buffer
			err := run([]string{"approach", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", repoPath, "--json"},
				noScanDeps(t, runDeps{
					getenv: func(key string) string {
						if key == tc.envKey {
							return roots[tc.rootKey]
						}
						return ""
					},
					stdout: &stdout,
				}))
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			var record flowstore.FlowRecord
			if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
			}
			if _, err := os.Stat(filepath.Join(roots[tc.rootKey], "approach.db")); err != nil {
				t.Fatalf("expected flow under %s: %v", tc.envKey, err)
			}
		})
	}
}

func TestRunFlowReadEnvRootLoadsConfigPresetsForRecovery(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-env-preset-recovery"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Env preset recovery",
  "instructions": "restore named preset edges",
  "status": "in_progress",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "read", "--flow-id", flowID},
		noScanDeps(t, runDeps{
			loadConfig: func() (config.Config, error) {
				return config.Config{
					Flow: config.FlowConfig{
						Presets: []flowstore.Preset{researchPresetForCLITest()},
					},
				}, nil
			},
			getenv: func(key string) string {
				if key == "APPROACH_FLOW_STATE_ROOT" {
					return root
				}
				return ""
			},
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if got := phaseByID(record, "draft").DependsOn; !slices.Equal(got, []string{"research"}) {
		t.Fatalf("draft DependsOn = %#v, want [research]", got)
	}
}

func TestRunFlowReadExplicitStateRootLoadsConfigPresetsForRecovery(t *testing.T) {
	root := t.TempDir()
	flowID := "20260607T120000Z-explicit-preset-recovery"
	flowDir := filepath.Join(root, "flows", flowID)
	if err := os.MkdirAll(flowDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	meta := `{
  "schema_version": 1,
  "flow_id": "` + flowID + `",
  "title": "Explicit preset recovery",
  "instructions": "restore named preset edges",
  "status": "in_progress",
  "repo_path": "` + filepath.Join(root, "repo") + `",
  "preset_name": "research",
  "phases": [
    {"phase_id": "research", "title": "Research", "kind": "plan", "status": "completed", "order": 1, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "draft", "title": "Draft", "kind": "implementation", "status": "pending", "order": 2, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"},
    {"phase_id": "publish", "title": "Publish", "kind": "merge", "status": "pending", "order": 3, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
  ],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(flowDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	err := run([]string{"approach", "flow", "read", "--flow-id", flowID, "--state-root", root},
		noScanDeps(t, runDeps{
			loadConfig: func() (config.Config, error) {
				return config.Config{
					Flow: config.FlowConfig{
						Presets: []flowstore.Preset{researchPresetForCLITest()},
					},
				}, nil
			},
			stdout: &stdout,
		}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if got := phaseByID(record, "draft").DependsOn; !slices.Equal(got, []string{"research"}) {
		t.Fatalf("draft DependsOn = %#v, want [research]", got)
	}
}

func TestRunFlowCreateWithPresetFlagSeedsCustomGraph(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")
	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "create",
		"--title", "Research",
		"--instructions", "phase it",
		"--repo-path", repoPath,
		"--preset", "research",
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Flow: config.FlowConfig{
					Presets: []flowstore.Preset{researchPresetForCLITest()},
				},
			}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if record.PresetName != "research" {
		t.Fatalf("preset name = %q, want research", record.PresetName)
	}
	if got := phaseIDs(record.Phases); !slices.Equal(got, []string{"research", "draft", "publish"}) {
		t.Fatalf("phase IDs = %#v", got)
	}
	if record.Phases[0].Status != flowstore.PhaseReady {
		t.Fatalf("root phase status = %q, want ready", record.Phases[0].Status)
	}
}

func TestRunFlowCreateUsesConfiguredDefaultPreset(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")
	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "create",
		"--title", "Research",
		"--instructions", "phase it",
		"--repo-path", repoPath,
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Flow: config.FlowConfig{
					Preset:  "research",
					Presets: []flowstore.Preset{researchPresetForCLITest()},
				},
			}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if record.PresetName != "research" {
		t.Fatalf("preset name = %q, want research", record.PresetName)
	}
	if got := phaseIDs(record.Phases); !slices.Equal(got, []string{"research", "draft", "publish"}) {
		t.Fatalf("phase IDs = %#v", got)
	}
}

func TestRunFlowCreateRejectsUnknownPreset(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")
	err := run([]string{
		"approach", "flow", "create",
		"--title", "Research",
		"--instructions", "phase it",
		"--repo-path", repoPath,
		"--preset", "missing",
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{
				Flow: config.FlowConfig{
					Presets: []flowstore.Preset{researchPresetForCLITest()},
				},
			}, nil
		},
		stdout: &bytes.Buffer{},
	}))
	if err == nil {
		t.Fatal("expected unknown preset error")
	}
	for _, want := range []string{`unknown flow preset "missing"`, "available presets: default, research"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %q", want, err.Error())
		}
	}
}

func TestRunFlowCreateWithoutPresetUsesDefault(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "repo")
	var stdout bytes.Buffer
	err := run([]string{
		"approach", "flow", "create",
		"--title", "Default",
		"--instructions", "phase it",
		"--repo-path", repoPath,
		"--json",
		"--state-root", root,
	}, noScanDeps(t, runDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, nil
		},
		stdout: &stdout,
	}))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	if record.PresetName != "" {
		t.Fatalf("preset name = %q, want empty for default preset", record.PresetName)
	}
	if got := phaseIDs(record.Phases); !slices.Equal(got, []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation", "autoreview", "merge"}) {
		t.Fatalf("phase IDs = %#v", got)
	}
}

func TestRunFlowCreateRequiresJSON(t *testing.T) {
	err := run([]string{"approach", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", "/repo", "--state-root", t.TempDir()},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error requiring --json")
	}
}

func researchPresetForCLITest() flowstore.Preset {
	return flowstore.Preset{
		Name: "research",
		Phases: []flowstore.PhaseSpec{
			{ID: "research", Title: "Research", Kind: flowstore.KindPlan, DependsOn: []string{}},
			{ID: "draft", Title: "Draft", Kind: flowstore.KindImplementation, DependsOn: []string{"research"}},
			{ID: "publish", Title: "Publish", Kind: flowstore.KindMerge, DependsOn: []string{"draft"}},
		},
	}
}

func phaseIDs(phases []flowstore.FlowPhase) []string {
	ids := make([]string, 0, len(phases))
	for _, phase := range phases {
		ids = append(ids, phase.PhaseID)
	}
	return ids
}

func TestRunFlowCreateValidatesRepoPathBeforeLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing repo path",
			args: []string{"approach", "flow", "create", "--title", "P", "--instructions", "i", "--json"},
			want: "requires --repo-path",
		},
		{
			name: "relative repo path",
			args: []string{"approach", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", "repo", "--json"},
			want: "requires absolute --repo-path",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, noScanDeps(t, runDeps{
				loadConfig: func() (config.Config, error) {
					t.Fatal("loadConfig should not run before repo path validation")
					return config.Config{}, nil
				},
				stdout: &bytes.Buffer{},
			}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run error = %v, want %q", err, tc.want)
			}
		})
	}
}

func mustRunFlow(t *testing.T, args []string) flowstore.FlowRecord {
	t.Helper()
	var stdout bytes.Buffer
	if err := run(args, noScanDeps(t, runDeps{stdout: &stdout})); err != nil {
		t.Fatalf("run(%v) error = %v", args, err)
	}
	var record flowstore.FlowRecord
	if err := json.Unmarshal(stdout.Bytes(), &record); err != nil {
		t.Fatalf("output is not JSON record: %v\n%s", err, stdout.String())
	}
	return record
}

func mustRunFlowReadyForImplementation(t *testing.T, root, title, branch string) flowstore.FlowRecord {
	t.Helper()
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", title,
		"--instructions", "phase it",
		"--repo-path", filepath.Join(root, "repo-"+strings.ReplaceAll(title, " ", "-")),
		"--branch", branch,
		"--json",
		"--state-root", root,
	})
	mustSetFlowPhase(t, root, created.FlowID, "plan", flowstore.PhaseCompleted, "", "", "")
	return mustSetFlowPhase(t, root, created.FlowID, "plan-review", flowstore.PhaseCompleted, flowstore.OutcomeApproved, "", "")
}

func mustRunFlowReadyForAutoreview(t *testing.T, root, title, branch string) flowstore.FlowRecord {
	t.Helper()
	created := mustRunFlow(t, []string{
		"approach", "flow", "create",
		"--title", title,
		"--instructions", "phase it",
		"--repo-path", filepath.Join(root, "repo-"+strings.ReplaceAll(title, " ", "-")),
		"--branch", branch,
		"--json",
		"--state-root", root,
	})
	for _, phaseID := range []string{"plan", "plan-review", "implementation", "review-loop", "pr-creation"} {
		outcome := ""
		if phaseID == "plan-review" {
			outcome = flowstore.OutcomeApproved
		}
		mustSetFlowPhase(t, root, created.FlowID, phaseID, flowstore.PhaseCompleted, outcome, "", "")
	}
	return mustRunFlow(t, []string{
		"approach", "flow", "pr", "set",
		"--flow-id", created.FlowID,
		"--provider", "github",
		"--number", "115",
		"--url", "https://github.com/approachcontrol/approach/pull/115",
		"--head", branch,
		"--base", "main",
		"--state-root", root,
	})
}

func mustSetFlowPhase(t *testing.T, root, flowID, phaseID, status, outcome, summary, notes string) flowstore.FlowRecord {
	t.Helper()
	args := []string{
		"approach", "flow", "phase", "set",
		"--flow-id", flowID,
		"--phase-id", phaseID,
		"--status", status,
		"--state-root", root,
	}
	if outcome != "" {
		args = append(args, "--outcome", outcome)
	}
	if summary != "" {
		args = append(args, "--summary", summary)
	}
	if notes != "" {
		args = append(args, "--notes", notes)
	}
	return mustRunFlow(t, args)
}

func mustFlowStore(t *testing.T, root string) *flowstore.Store {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func overwriteFlowRecord(t *testing.T, root string, record flowstore.FlowRecord) error {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	updatedAt := record.UpdatedAt.UTC()
	projectionTime := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%09dZ",
		updatedAt.Year(), updatedAt.Month(), updatedAt.Day(), updatedAt.Hour(), updatedAt.Minute(), updatedAt.Second(), updatedAt.Nanosecond())
	_, err = db.Exec(`
INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(flow_id) DO UPDATE SET repo_path=excluded.repo_path, status=excluded.status,
updated_at=excluded.updated_at, record=excluded.record`,
		record.FlowID, filepath.Clean(record.RepoPath), record.Status, projectionTime, data)
	return err
}

func phaseByID(record flowstore.FlowRecord, phaseID string) flowstore.FlowPhase {
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	return flowstore.FlowPhase{}
}

func savePlanArtifact(t *testing.T, root, planID string) {
	t.Helper()
	store, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewPlanStore() error = %v", err)
	}
	if _, err := store.Save(planstore.PlanRecord{
		PlanID:   planID,
		Title:    "Linked plan",
		Status:   "approved",
		Markdown: "# Linked plan\n",
	}); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
}
