package model_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
)

func ensureWorktreeFlowRecord() flowstore.FlowRecord {
	return flowstore.FlowRecord{
		FlowID:   "flow-1",
		Title:    "Add Flow Mode",
		RepoPath: "/dev/alpha",
		BaseRef:  "main",
		Phases: []flowstore.FlowPhase{
			{PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseReady, Order: 1},
		},
	}
}

func TestFlowStarterEnsureWorktreeMirrorsPrepareFlowOrderAndArguments(t *testing.T) {
	var calls []string
	var createArgs []string
	var startUpdate flowstore.StartMetadataUpdate
	var bootstrapCtx actions.BootstrapContext
	record := ensureWorktreeFlowRecord()
	persisted := record
	persisted.WorktreePath = "/dev/alpha-worktrees/flow-add-flow-mode"
	persisted.Branch = "flow/add-flow-mode"
	persisted.Commit = "abc123"

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error) {
			calls = append(calls, "create-worktree")
			createArgs = []string{repoPath, title, baseRef}
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
			}, nil
		},
		ResolveCommit: func(worktreePath string) string {
			calls = append(calls, "resolve-commit")
			if worktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" {
				t.Fatalf("ResolveCommit(%q)", worktreePath)
			}
			return "abc123"
		},
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-start")
			startUpdate = update
			return persisted, nil
		},
		BootstrapHookForRepo: func(repoPath string) (actions.BootstrapHook, bool) {
			if repoPath != "/dev/alpha" {
				t.Fatalf("BootstrapHookForRepo(%q)", repoPath)
			}
			return actions.BootstrapHook{Script: ".approach/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, _ actions.BootstrapHook) error {
			calls = append(calls, "bootstrap")
			bootstrapCtx = ctx
			return nil
		},
		SetPhase: func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
			calls = append(calls, "set-phase")
			return flowstore.FlowRecord{}, nil
		},
	})

	got, err := starter.EnsureWorktree(record)
	if err != nil {
		t.Fatalf("EnsureWorktree() error = %v", err)
	}
	if strings.Join(calls, ",") != "create-worktree,resolve-commit,set-start,bootstrap" {
		t.Fatalf("call order = %#v", calls)
	}
	if strings.Join(createArgs, ",") != "/dev/alpha,Add Flow Mode,main" {
		t.Fatalf("CreateWorktree args = %#v, want the record's repo path, title, and base ref", createArgs)
	}
	if startUpdate.FlowID != "flow-1" ||
		startUpdate.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		startUpdate.Branch != "flow/add-flow-mode" ||
		startUpdate.BaseRef != "main" ||
		startUpdate.Commit != "abc123" {
		t.Fatalf("start metadata update = %#v", startUpdate)
	}
	if bootstrapCtx.RepoPath != "/dev/alpha" ||
		bootstrapCtx.WorktreePath != "/dev/alpha-worktrees/flow-add-flow-mode" ||
		bootstrapCtx.Ref != "flow/add-flow-mode" ||
		bootstrapCtx.Kind != actions.WorktreeCreateFlow {
		t.Fatalf("bootstrap context = %#v", bootstrapCtx)
	}
	// The read stage sets event.Record from this and the prepare stage looks the
	// phase up in it, so a phase-less record would strand the launch.
	if len(got.Phases) != 1 || got.Phases[0].PhaseID != "plan" {
		t.Fatalf("ensured record phases = %#v, want the complete persisted record", got.Phases)
	}
	if got.WorktreePath != persisted.WorktreePath || got.Branch != persisted.Branch || got.Commit != persisted.Commit {
		t.Fatalf("ensured record = %#v, want the persisted start metadata", got)
	}
}

func TestFlowStarterEnsureWorktreePassesThroughWhenWorktreeExists(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.WorktreePath = "/dev/alpha-worktrees/flow-add-flow-mode"

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("EnsureWorktree() must not create a second worktree")
			return actions.FlowWorktreeCreateResult{}, nil
		},
		SetStartMetadata: func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("EnsureWorktree() must not rewrite start metadata for an existing worktree")
			return flowstore.FlowRecord{}, nil
		},
	})

	got, err := starter.EnsureWorktree(record)
	if err != nil {
		t.Fatalf("EnsureWorktree() error = %v", err)
	}
	if got.WorktreePath != record.WorktreePath {
		t.Fatalf("ensured record = %#v, want the record returned unchanged", got)
	}
}

// Blocking phases is the lifecycle's job, behind its attempt fence. EnsureWorktree
// only reports.
func TestFlowStarterEnsureWorktreeReportsFailuresWithoutBlockingPhases(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options model.FlowStarterOptions
		want    string
	}{
		{
			name: "worktree creation",
			options: model.FlowStarterOptions{
				CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
					return actions.FlowWorktreeCreateResult{}, errors.New("branch already checked out")
				},
			},
			want: "branch already checked out",
		},
		{
			name: "start metadata",
			options: model.FlowStarterOptions{
				CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
					return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-one", Branch: "flow/one"}, nil
				},
				SetStartMetadata: func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{}, errors.New("flow store locked")
				},
			},
			// The path is named because nothing records it: the retry allocates
			// flow-one-2 rather than adopting this directory, so a bare store
			// error would leave it unattributable.
			want: "worktree not recorded: /dev/alpha-worktrees/flow-one: flow store locked",
		},
		{
			name: "bootstrap hook",
			options: model.FlowStarterOptions{
				CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
					return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-one", Branch: "flow/one"}, nil
				},
				SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
					return flowstore.FlowRecord{FlowID: update.FlowID, WorktreePath: update.WorktreePath}, nil
				},
				BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
					return actions.BootstrapHook{Script: ".approach/bootstrap"}, true
				},
				RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
					return errors.New("missing env file")
				},
			},
			want: "bootstrap hook failed: missing env file",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			phaseUpdates := 0
			opts := tt.options
			opts.SetPhase = func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error) {
				phaseUpdates++
				return flowstore.FlowRecord{}, nil
			}
			starter := model.NewFlowStarter(opts)

			_, err := starter.EnsureWorktree(ensureWorktreeFlowRecord())
			if err == nil || err.Error() != tt.want {
				t.Fatalf("EnsureWorktree() error = %v, want %q", err, tt.want)
			}
			if phaseUpdates != 0 {
				t.Fatalf("phase updates = %d, want EnsureWorktree to leave blocking to the caller", phaseUpdates)
			}
		})
	}
}

// CreateFlowWorktree derives the branch from the title, so a title-less record
// surfaces its error rather than getting a synthesized slug.
func TestFlowStarterEnsureWorktreeSurfacesMissingTitle(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.Title = ""
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(_, title, _ string) (actions.FlowWorktreeCreateResult, error) {
			if title != "" {
				t.Fatalf("CreateWorktree title = %q, want the record's empty title verbatim", title)
			}
			return actions.FlowWorktreeCreateResult{}, errors.New("flow title is required")
		},
	})

	if _, err := starter.EnsureWorktree(record); err == nil || err.Error() != "flow title is required" {
		t.Fatalf("EnsureWorktree() error = %v, want the create-worktree error surfaced", err)
	}
}

// A --branch recorded by `approach flow create` without a --worktree-path is
// still a promise: prompts render it as the push target and `flow pr set --head`
// validates against it. Allocating flow/<slug> beside it would leave the record
// naming a branch no agent ever touches.
func TestFlowStarterEnsureWorktreeAttachesTheRecordedBranch(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.Branch = "feature/login"
	var attachArgs []string
	var startUpdate flowstore.StartMetadataUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		AttachWorktree: func(repoPath, branch string) (actions.FlowWorktreeCreateResult, error) {
			attachArgs = []string{repoPath, branch}
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/feature-login",
				Branch:       "feature/login",
			}, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("EnsureWorktree() must not allocate a second branch beside the recorded one")
			return actions.FlowWorktreeCreateResult{}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			startUpdate = update
			updated := record
			updated.WorktreePath = update.WorktreePath
			updated.Branch = update.Branch
			updated.Commit = update.Commit
			return updated, nil
		},
	})

	got, err := starter.EnsureWorktree(record)
	if err != nil {
		t.Fatalf("EnsureWorktree() error = %v", err)
	}
	if strings.Join(attachArgs, ",") != "/dev/alpha,feature/login" {
		t.Fatalf("AttachWorktree args = %#v, want the record's repo path and branch", attachArgs)
	}
	if startUpdate.Branch != "feature/login" || got.Branch != "feature/login" {
		t.Fatalf("branch = update %q record %q, want the recorded branch preserved", startUpdate.Branch, got.Branch)
	}
	if got.WorktreePath != "/dev/alpha-worktrees/feature-login" {
		t.Fatalf("worktree path = %q, want the attached worktree", got.WorktreePath)
	}
}

// A recorded branch that resolves to nothing is the one case the store cannot
// tell apart on its own, and the only one where a fresh flow/<slug> may replace
// the recorded name.
func TestFlowStarterEnsureWorktreeCreatesAFreshPairWhenTheRecordedBranchIsGone(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.Branch = "deleted-long-ago"
	var startUpdate flowstore.StartMetadataUpdate

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		AttachWorktree: func(_, branch string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, fmt.Errorf("%s: %w", branch, actions.ErrFlowBranchMissing)
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
			}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			startUpdate = update
			updated := record
			updated.WorktreePath = update.WorktreePath
			updated.Branch = update.Branch
			return updated, nil
		},
	})

	got, err := starter.EnsureWorktree(record)
	if err != nil {
		t.Fatalf("EnsureWorktree() error = %v", err)
	}
	if startUpdate.Branch != "flow/add-flow-mode" || got.Branch != "flow/add-flow-mode" {
		t.Fatalf("branch = update %q record %q, want the created branch", startUpdate.Branch, got.Branch)
	}
}

// A blank Branch is no branch at all, so the attach seam is never consulted for
// one. Trimming here rather than inside the seam keeps `git worktree add` from
// ever seeing a whitespace ref.
func TestFlowStarterEnsureWorktreeIgnoresAWhitespaceOnlyBranch(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.Branch = "   "
	created := false

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		AttachWorktree: func(string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("EnsureWorktree() must not attach a whitespace-only branch")
			return actions.FlowWorktreeCreateResult{}, nil
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			created = true
			return actions.FlowWorktreeCreateResult{
				WorktreePath: "/dev/alpha-worktrees/flow-add-flow-mode",
				Branch:       "flow/add-flow-mode",
			}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			updated := record
			updated.WorktreePath = update.WorktreePath
			updated.Branch = update.Branch
			return updated, nil
		},
	})

	if _, err := starter.EnsureWorktree(record); err != nil {
		t.Fatalf("EnsureWorktree() error = %v", err)
	}
	if !created {
		t.Fatal("EnsureWorktree() did not fall through to CreateWorktree for a blank branch")
	}
}

// Only ErrFlowBranchMissing means "the recorded branch is not real". Any other
// attach failure — the branch is checked out elsewhere, git is unhappy — must
// surface rather than silently becoming a second branch.
func TestFlowStarterEnsureWorktreeSurfacesNonMissingAttachFailures(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.Branch = "feature/login"

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		AttachWorktree: func(string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{}, errors.New("branch is already checked out")
		},
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("EnsureWorktree() must not fall back to a fresh branch on an unrelated attach failure")
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	if _, err := starter.EnsureWorktree(record); err == nil || err.Error() != "branch is already checked out" {
		t.Fatalf("EnsureWorktree() error = %v, want the attach error surfaced", err)
	}
}

// The launch call site guards this too, but `git -C ""` would run in Approach's
// own working directory, so the seam refuses on its own account.
func TestFlowStarterEnsureWorktreeRefusesARecordWithNoRepository(t *testing.T) {
	record := ensureWorktreeFlowRecord()
	record.RepoPath = ""

	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			t.Fatal("EnsureWorktree() must not run git without a repository path")
			return actions.FlowWorktreeCreateResult{}, nil
		},
	})

	if _, err := starter.EnsureWorktree(record); err == nil || err.Error() != "flow has no repository of its own" {
		t.Fatalf("EnsureWorktree() error = %v, want a refusal naming the missing repository", err)
	}
}

// The bootstrap failure is the one that persists before it fails, so the record
// it returns must carry the worktree the store now holds. Callers phrase their
// refusal from exactly this.
func TestFlowStarterEnsureWorktreeReturnsThePersistedRecordAfterABootstrapFailure(t *testing.T) {
	starter := model.NewFlowStarter(model.FlowStarterOptions{
		CreateWorktree: func(string, string, string) (actions.FlowWorktreeCreateResult, error) {
			return actions.FlowWorktreeCreateResult{WorktreePath: "/dev/alpha-worktrees/flow-one", Branch: "flow/one"}, nil
		},
		ResolveCommit: func(string) string { return "abc123" },
		SetStartMetadata: func(update flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{
				FlowID:       update.FlowID,
				WorktreePath: update.WorktreePath,
				Branch:       update.Branch,
				Commit:       update.Commit,
			}, nil
		},
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".approach/bootstrap"}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("missing env file")
		},
	})

	got, err := starter.EnsureWorktree(ensureWorktreeFlowRecord())
	if err == nil {
		t.Fatal("EnsureWorktree() error = nil, want the bootstrap failure reported")
	}
	if got.WorktreePath != "/dev/alpha-worktrees/flow-one" || got.Branch != "flow/one" {
		t.Fatalf("record = %#v, want the persisted worktree carried alongside the error", got)
	}
}
