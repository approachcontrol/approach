package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brian-bell/wtui/config"
	"github.com/brian-bell/wtui/flowstore"
	"github.com/brian-bell/wtui/planstore"
)

func TestRunFlowCreatePrintsJSONRecord(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	instructionsFile := filepath.Join(root, "instructions.md")
	if err := os.WriteFile(instructionsFile, []byte("Build the thing.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := run([]string{
		"wtui", "flow", "create",
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
		len(record.Phases) != 7 {
		t.Fatalf("unexpected flow record: %#v", record)
	}
	if _, err := os.Stat(filepath.Join(root, "flows", record.FlowID, "meta.json")); err != nil {
		t.Fatalf("expected persisted flow metadata: %v", err)
	}
}

func TestRunFlowListJSONFiltersByRepo(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	bravo := filepath.Join(root, "bravo")
	mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Alpha", "--instructions", "alpha", "--repo-path", alpha, "--json", "--state-root", root})
	mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Bravo", "--instructions", "bravo", "--repo-path", bravo, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{"wtui", "flow", "list", "--repo-path", alpha, "--json", "--state-root", root},
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
	err := run([]string{"wtui", "flow", "list", "--state-root", t.TempDir()},
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
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Readable", "--instructions", "read it", "--repo-path", repoPath, "--json", "--state-root", root})

	var stdout bytes.Buffer
	err := run([]string{"wtui", "flow", "read", "--flow-id", created.FlowID, "--state-root", root},
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
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Plan Link", "--instructions", "plan it", "--repo-path", repoPath, "--json", "--state-root", root})
	savePlanArtifact(t, root, "plan-1")

	var linkedAt string
	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		args := []string{
			"wtui", "flow", "plan", "set",
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
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Plan Link Validation", "--instructions", "plan it", "--repo-path", repoPath, "--json", "--state-root", root})
	savePlanArtifact(t, root, "plan-1")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing plan id",
			args: []string{"wtui", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-path", filepath.Join(root, "plans", "plan-1", "plan.md"), "--state-root", root},
			want: "requires --plan-id",
		},
		{
			name: "missing plan",
			args: []string{"wtui", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "missing-plan", "--state-root", root},
			want: `plan "missing-plan" not found`,
		},
		{
			name: "relative plan path",
			args: []string{"wtui", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "plan-1", "--plan-path", "plans/plan-1/plan.md", "--state-root", root},
			want: "flow plan path must be absolute",
		},
		{
			name: "mismatched plan path",
			args: []string{"wtui", "flow", "plan", "set", "--flow-id", created.FlowID, "--plan-id", "plan-1", "--plan-path", filepath.Join(root, "plans", "other", "plan.md"), "--state-root", root},
			want: "does not match plan",
		},
		{
			name: "missing flow",
			args: []string{"wtui", "flow", "plan", "set", "--flow-id", "missing-flow", "--plan-id", "plan-1", "--plan-path", filepath.Join(root, "plans", "plan-1", "plan.md"), "--state-root", root},
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
			created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", tc.name, "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

			args := []string{
				"wtui", "flow", "phase", "set",
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

func TestRunFlowPhaseSetRestartsBlockedPhaseWithNotes(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Restart Blocked", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	err := run([]string{
		"wtui", "flow", "phase", "set",
		"--flow-id", created.FlowID,
		"--phase-id", "plan",
		"--status", flowstore.PhaseBlocked,
		"--state-root", root,
	}, noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err != nil {
		t.Fatalf("set blocked returned error: %v", err)
	}

	err = run([]string{
		"wtui", "flow", "phase", "set",
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
		"wtui", "flow", "phase", "set",
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
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Reject Status", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "ready", status: flowstore.PhaseReady, want: "cannot set phase status to ready"},
		{name: "bogus", status: "done", want: "unsupported agent-facing phase status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run([]string{
				"wtui", "flow", "phase", "set",
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
	created := mustRunFlow(t, []string{"wtui", "flow", "create", "--title", "Reject Skip", "--instructions", "phase it", "--repo-path", repoPath, "--json", "--state-root", root})

	err := run([]string{
		"wtui", "flow", "phase", "set",
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
	err := run([]string{"wtui", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", repoPath, "--json"},
		noScanDeps(t, runDeps{
			loadConfig: func() (config.Config, error) {
				return config.Config{Sessions: config.SessionsConfig{Root: configRoot}}, nil
			},
			getenv: func(key string) string {
				switch key {
				case "WTUI_FLOW_STATE_ROOT":
					return flowRoot
				case "WTUI_PLAN_STATE_ROOT":
					return planRoot
				case "WTUI_SESSION_STATE_ROOT":
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
	if _, err := os.Stat(filepath.Join(flowRoot, "flows", record.FlowID, "meta.json")); err != nil {
		t.Fatalf("expected flow under WTUI_FLOW_STATE_ROOT: %v", err)
	}
	if _, err := os.Stat(filepath.Join(planRoot, "flows", record.FlowID, "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("flow should not be under plan root")
	}
}

func TestRunFlowCreateFallsBackToPlanThenSessionRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		envKey  string
		rootKey string
	}{
		{name: "plan root", envKey: "WTUI_PLAN_STATE_ROOT", rootKey: "plan"},
		{name: "session root", envKey: "WTUI_SESSION_STATE_ROOT", rootKey: "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := map[string]string{
				"plan":    t.TempDir(),
				"session": t.TempDir(),
			}
			repoPath := filepath.Join(t.TempDir(), "repo")
			var stdout bytes.Buffer
			err := run([]string{"wtui", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", repoPath, "--json"},
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
			if _, err := os.Stat(filepath.Join(roots[tc.rootKey], "flows", record.FlowID, "meta.json")); err != nil {
				t.Fatalf("expected flow under %s: %v", tc.envKey, err)
			}
		})
	}
}

func TestRunFlowCreateRequiresJSON(t *testing.T) {
	err := run([]string{"wtui", "flow", "create", "--title", "P", "--instructions", "i", "--repo-path", "/repo", "--state-root", t.TempDir()},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error requiring --json")
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
