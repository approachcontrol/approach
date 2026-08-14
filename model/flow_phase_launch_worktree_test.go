package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
)

func worktreelessLaunchRecord() flowstore.FlowRecord {
	phase := flowstore.FlowPhase{PhaseID: "implementation", Title: "Implementation", Status: flowstore.PhaseReady}
	return flowstore.FlowRecord{
		FlowID:   "flow-1",
		Title:    "Flow one",
		RepoPath: "/dev/alpha",
		BaseRef:  "main",
		Phases:   []flowstore.FlowPhase{phase},
	}
}

// The bead's regression guard: a Flow with no worktree must never resolve to
// the repository root, which is where the agent would otherwise run.
func TestFlowPhaseLauncherPreflightDefersWorktreeForWorktreelessFlow(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{AgentCommand: "codex", NewLaunchID: func() string { return "launch-1" }}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if prepared.WorktreePath != "" {
		t.Fatalf("prepared worktree path = %q, want it deferred to EnsureLaunchWorktree", prepared.WorktreePath)
	}
	if prepared.RepoPath != "/dev/alpha" {
		t.Fatalf("prepared repo path = %q, want /dev/alpha", prepared.RepoPath)
	}
	if prepared.CreatedWorktree {
		t.Fatal("Preflight() must not report a created worktree; it is side-effect free")
	}
}

func TestFlowPhaseLauncherPreflightRefusesWithoutWorktreeOrRepoPath(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.RepoPath = ""
	launcher := model.FlowPhaseLauncher{AgentCommand: "codex"}

	_, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err == nil || err.Error() != "Cannot determine launch path for this flow" {
		t.Fatalf("Preflight() error = %v, want the undeterminable-launch-path refusal", err)
	}
}

func TestFlowPhaseLauncherPreflightKeepsExistingWorktreePath(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.WorktreePath = "/dev/alpha-worktrees/flow-one"
	launcher := model.FlowPhaseLauncher{AgentCommand: "codex", NewLaunchID: func() string { return "launch-1" }}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if prepared.WorktreePath != record.WorktreePath || prepared.RepoPath != record.RepoPath {
		t.Fatalf("prepared paths = repo %q worktree %q, want them unchanged", prepared.RepoPath, prepared.WorktreePath)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeInspectsExistingWorktreePath(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.WorktreePath = "/dev/alpha-worktrees/flow-one"
	ensureCalls := 0
	var inspectedPaths []string
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		InspectWorktreeDirectory: func(path string) error {
			inspectedPaths = append(inspectedPaths, path)
			return nil
		},
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			ensureCalls++
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err != nil {
		t.Fatalf("EnsureLaunchWorktree() error = %v", err)
	}
	if len(inspectedPaths) != 1 || inspectedPaths[0] != record.WorktreePath {
		t.Fatalf("inspected paths = %#v, want the recorded path", inspectedPaths)
	}
	if ensureCalls != 0 {
		t.Fatalf("ensure seam calls = %d, want 0 for a Flow that already has a worktree", ensureCalls)
	}
	if ensured.WorktreePath != record.WorktreePath || ensured.CreatedWorktree {
		t.Fatalf("ensured request = %#v, want it returned unchanged", ensured)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeUsesRealInspectorWhenNil(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.WorktreePath = t.TempDir()
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			t.Fatal("a non-empty recorded path must not be provisioned")
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err != nil {
		t.Fatalf("EnsureLaunchWorktree() error = %v", err)
	}
	if ensured.WorktreePath != record.WorktreePath || ensured.CreatedWorktree {
		t.Fatalf("ensured request = %#v, want the real directory returned unchanged", ensured)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeRefusesNonDirectoryRecordedPath(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.WorktreePath = "/dev/alpha-worktrees/not-a-directory"
	inspectionErr := errors.New("/dev/alpha-worktrees/not-a-directory is not a directory")
	var inspectedPaths []string
	ensureCalls := 0
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		InspectWorktreeDirectory: func(path string) error {
			inspectedPaths = append(inspectedPaths, path)
			return inspectionErr
		},
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			ensureCalls++
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	_, err = launcher.EnsureLaunchWorktree(prepared)
	want := `Recorded Flow worktree "/dev/alpha-worktrees/not-a-directory" is unusable: /dev/alpha-worktrees/not-a-directory is not a directory`
	if err == nil || err.Error() != want {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want %q", err, want)
	}
	var worktreeErr model.FlowPhaseLaunchWorktreeError
	if !errors.As(err, &worktreeErr) {
		t.Fatalf("EnsureLaunchWorktree() error type = %T, want FlowPhaseLaunchWorktreeError", err)
	}
	if worktreeErr.Transient || worktreeErr.Stale {
		t.Fatalf("worktree error classification = transient %v stale %v, want permanent", worktreeErr.Transient, worktreeErr.Stale)
	}
	if len(inspectedPaths) != 1 || inspectedPaths[0] != record.WorktreePath {
		t.Fatalf("inspected paths = %#v, want the recorded path", inspectedPaths)
	}
	if ensureCalls != 0 {
		t.Fatalf("ensure seam calls = %d, want 0 for a non-empty recorded path", ensureCalls)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeRefusesMissingRecordedPath(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.WorktreePath = "/dev/alpha-worktrees/missing-flow"
	inspectionErr := errors.New("stat /dev/alpha-worktrees/missing-flow: no such file or directory")
	var inspectedPaths []string
	ensureCalls := 0
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		InspectWorktreeDirectory: func(path string) error {
			inspectedPaths = append(inspectedPaths, path)
			return inspectionErr
		},
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			ensureCalls++
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	_, err = launcher.EnsureLaunchWorktree(prepared)
	want := `Recorded Flow worktree "/dev/alpha-worktrees/missing-flow" is unusable: stat /dev/alpha-worktrees/missing-flow: no such file or directory`
	if err == nil || err.Error() != want {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want %q", err, want)
	}
	var worktreeErr model.FlowPhaseLaunchWorktreeError
	if !errors.As(err, &worktreeErr) {
		t.Fatalf("EnsureLaunchWorktree() error type = %T, want FlowPhaseLaunchWorktreeError", err)
	}
	if worktreeErr.Transient || worktreeErr.Stale {
		t.Fatalf("worktree error classification = transient %v stale %v, want permanent", worktreeErr.Transient, worktreeErr.Stale)
	}
	if len(inspectedPaths) != 1 || inspectedPaths[0] != record.WorktreePath {
		t.Fatalf("inspected paths = %#v, want the recorded path", inspectedPaths)
	}
	if ensureCalls != 0 {
		t.Fatalf("ensure seam calls = %d, want 0 for a non-empty recorded path", ensureCalls)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeCreatesAndCarriesTheUpdatedRecord(t *testing.T) {
	record := worktreelessLaunchRecord()
	updated := record
	updated.WorktreePath = "/dev/alpha-worktrees/flow-one"
	updated.Branch = "flow/flow-one"
	updated.Commit = "abc123"
	var seen []flowstore.FlowRecord
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		InspectWorktreeDirectory: func(string) error {
			return nil
		},
		EnsureWorktree: func(got flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			seen = append(seen, got)
			return updated, nil
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return updated, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err != nil {
		t.Fatalf("EnsureLaunchWorktree() error = %v", err)
	}
	if len(seen) != 1 || seen[0].FlowID != "flow-1" {
		t.Fatalf("ensure seam calls = %#v, want exactly one for flow-1", seen)
	}
	if ensured.WorktreePath != updated.WorktreePath {
		t.Fatalf("ensured worktree path = %q, want %q", ensured.WorktreePath, updated.WorktreePath)
	}
	if ensured.Record.Branch != "flow/flow-one" || ensured.Record.Commit != "abc123" {
		t.Fatalf("ensured record = %#v, want the branch and commit the ensure seam persisted", ensured.Record)
	}
	if !ensured.CreatedWorktree {
		t.Fatal("ensured request must report that it created the worktree")
	}

	// The launch context is built from the carried record, not the pre-ensure one.
	result, err := launcher.Prepare(ensured)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Context.WorktreePath != updated.WorktreePath ||
		result.Context.Branch != "flow/flow-one" ||
		result.Context.Commit != "abc123" {
		t.Fatalf("launch context = %#v, want the ensured worktree, branch, and commit", result.Context)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeRefusesUnusableEnsuredPath(t *testing.T) {
	record := worktreelessLaunchRecord()
	updated := record
	updated.WorktreePath = "/dev/alpha-worktrees/disappeared"
	inspectionErr := errors.New("stat /dev/alpha-worktrees/disappeared: no such file or directory")
	var inspectedPaths []string
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			return updated, nil
		},
		InspectWorktreeDirectory: func(path string) error {
			inspectedPaths = append(inspectedPaths, path)
			return inspectionErr
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	want := `Recorded Flow worktree "/dev/alpha-worktrees/disappeared" is unusable: stat /dev/alpha-worktrees/disappeared: no such file or directory`
	if err == nil || err.Error() != want {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want %q", err, want)
	}
	var worktreeErr model.FlowPhaseLaunchWorktreeError
	if !errors.As(err, &worktreeErr) {
		t.Fatalf("EnsureLaunchWorktree() error type = %T, want FlowPhaseLaunchWorktreeError", err)
	}
	if worktreeErr.Transient || worktreeErr.Stale {
		t.Fatalf("worktree error classification = transient %v stale %v, want permanent", worktreeErr.Transient, worktreeErr.Stale)
	}
	if len(inspectedPaths) != 1 || inspectedPaths[0] != updated.WorktreePath {
		t.Fatalf("inspected paths = %#v, want the ensured path", inspectedPaths)
	}
	if ensured.WorktreePath != updated.WorktreePath || ensured.Record.WorktreePath != updated.WorktreePath || !ensured.CreatedWorktree {
		t.Fatalf("refused request = %#v, want the persisted ensured worktree carried forward", ensured)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeRefusesWithoutSeam(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{AgentCommand: "codex", NewLaunchID: func() string { return "launch-1" }}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err == nil || err.Error() != "Flow has no worktree and none can be created" {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want the nil-seam refusal", err)
	}
	var worktreeErr model.FlowPhaseLaunchWorktreeError
	if !errors.As(err, &worktreeErr) {
		t.Fatalf("EnsureLaunchWorktree() error type = %T, want FlowPhaseLaunchWorktreeError", err)
	}
	if ensured.WorktreePath != "" {
		t.Fatalf("refused request worktree path = %q, want it empty and never the repo path", ensured.WorktreePath)
	}
}

// Preflight resolves RepoPath through CurrentRepoPath, so a Flow with no repo of
// its own would otherwise get a worktree in whatever repo happens to be selected.
func TestFlowPhaseLauncherEnsureLaunchWorktreeNeverCreatesInTheSelectedRepo(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.RepoPath = ""
	calls := 0
	launcher := model.FlowPhaseLauncher{
		AgentCommand:    "codex",
		NewLaunchID:     func() string { return "launch-1" },
		CurrentRepoPath: func() (string, bool) { return "/dev/unrelated", true },
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			calls++
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if prepared.RepoPath != "/dev/unrelated" {
		t.Fatalf("prepared repo path = %q, want the selected repo", prepared.RepoPath)
	}
	if _, err := launcher.EnsureLaunchWorktree(prepared); err == nil ||
		err.Error() != "Flow has no worktree and no repository of its own" {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want the repo-less refusal", err)
	}
	if calls != 0 {
		t.Fatalf("ensure seam calls = %d, want 0 for a Flow with no repo of its own", calls)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeWrapsSeamFailure(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		EnsureWorktree: func(flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, errors.New("branch already checked out")
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	_, err = launcher.EnsureLaunchWorktree(prepared)
	if err == nil || err.Error() != "Worktree creation failed: branch already checked out" {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want the wrapped creation failure", err)
	}
}

// The bootstrap hook fails after the worktree exists and is recorded. Calling
// that a creation failure would send the user looking for a directory that is
// already there, and dropping the returned record would leave the caller
// holding one the store has moved past.
func TestFlowPhaseLauncherEnsureLaunchWorktreeReportsAPersistedWorktreeAsCreated(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		EnsureWorktree: func(got flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			started := got
			started.WorktreePath = "/dev/alpha-worktrees/flow-one"
			started.Branch = "flow/one"
			return started, errors.New("bootstrap hook failed: missing env file")
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err == nil ||
		err.Error() != "Worktree created but the launch was refused: bootstrap hook failed: missing env file" {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want the created-but-refused wording", err)
	}
	if ensured.WorktreePath != "/dev/alpha-worktrees/flow-one" ||
		ensured.Record.Branch != "flow/one" ||
		!ensured.CreatedWorktree {
		t.Fatalf("refused request = %#v, want the persisted worktree carried forward", ensured)
	}
}

func TestFlowPhaseLauncherEnsureLaunchWorktreeRejectsWorktreelessEnsureResult(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		EnsureWorktree: func(got flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			// The stub NewFlowStarter installs for a missing SetStartMetadata
			// returns exactly this: an ID and nothing else.
			return flowstore.FlowRecord{FlowID: got.FlowID}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err == nil || !strings.Contains(err.Error(), "Worktree creation failed") {
		t.Fatalf("EnsureLaunchWorktree() error = %v, want a loud failure for a worktree-less ensure result", err)
	}
	if ensured.WorktreePath != "" {
		t.Fatalf("refused request worktree path = %q, want it empty", ensured.WorktreePath)
	}
}

// A blank branch and commit in the ensured record reach the agent environment
// blank rather than falling back to the pre-ensure record's values.
func TestFlowPhaseLauncherEnsureLaunchWorktreeCarriesBlankBranchAndCommit(t *testing.T) {
	record := worktreelessLaunchRecord()
	record.Branch = "stale-branch"
	record.Commit = "stalecommit"
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		InspectWorktreeDirectory: func(string) error {
			return nil
		},
		EnsureWorktree: func(got flowstore.FlowRecord) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       got.FlowID,
				RepoPath:     got.RepoPath,
				WorktreePath: "/dev/alpha-worktrees/flow-one",
				Phases:       got.Phases,
			}, nil
		},
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	ensured, err := launcher.EnsureLaunchWorktree(prepared)
	if err != nil {
		t.Fatalf("EnsureLaunchWorktree() error = %v", err)
	}
	result, err := launcher.Prepare(ensured)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Context.Branch != "" || result.Context.Commit != "" {
		t.Fatalf("launch context = %#v, want the ensured record's blank branch and commit", result.Context)
	}
}

// The invariant is structural: even if a future caller forgets to ensure, an
// empty worktree path can never reach cmd.Dir.
func TestFlowPhaseLauncherPrepareRefusesEmptyWorktreePath(t *testing.T) {
	record := worktreelessLaunchRecord()
	launcher := model.FlowPhaseLauncher{
		AgentCommand: "codex",
		NewLaunchID:  func() string { return "launch-1" },
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("Prepare() must refuse before it persists a launch ID")
			return flowstore.FlowRecord{}, nil
		},
	}

	prepared, err := launcher.Preflight(model.FlowPhaseLaunchRequest{Record: record, Phase: record.Phases[0]})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if _, err := launcher.Prepare(prepared); err == nil ||
		err.Error() != "Flow has no worktree and none can be created" {
		t.Fatalf("Prepare() error = %v, want the missing-worktree refusal", err)
	}
}
