package gitquery_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/gitquery"
)

func quietRefMissingError(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit 1").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("quietRefMissingError() = %v, want exit status 1", err)
	}
	return err
}

type fakeReply struct {
	out string
	err error
}

type fakeRunner struct {
	t                 *testing.T
	run               map[string]fakeReply
	ok                map[string]error
	predicateFailures map[string]error
	calls             []string
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	key := fakeKey(dir, args...)
	f.calls = append(f.calls, "run "+key)
	reply, ok := f.run[key]
	if !ok {
		f.t.Fatalf("unexpected Run call: %s", key)
	}
	return reply.out, reply.err
}

func (f *fakeRunner) Predicate(dir string, args ...string) (bool, error) {
	key := fakeKey(dir, args...)
	f.calls = append(f.calls, "ok "+key)
	if err, ok := f.predicateFailures[key]; ok {
		return false, err
	}
	err, ok := f.ok[key]
	if !ok {
		f.t.Fatalf("unexpected Ok call: %s", key)
	}
	return err == nil, nil
}

func fakeKey(dir string, args ...string) string {
	return dir + " | " + strings.Join(args, " ")
}

func fakeCallContains(calls []string, needle string) bool {
	for _, call := range calls {
		if strings.Contains(call, needle) {
			return true
		}
	}
	return false
}

func TestQuerierListBranches_AheadBehindFailureLeavesCountsZero(t *testing.T) {
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\trefs/remotes/origin/feature\t[ahead 3]\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey("/repo", "rev-list", "--count", "--left-right", "feature...refs/remotes/origin/feature"): {
				err: errors.New("fatal: bad revision"),
			},
			fakeKey("/repo", "status", "--porcelain"): {},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): errors.New("not merged"),
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok {
		t.Fatalf("AsPartialQuery(%v) = false, want non-fatal enrichment diagnostic", err)
	}
	if len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "ahead/behind" || partial.Warnings[0].Subject != "feature" {
		t.Fatalf("partial warnings = %#v, want feature ahead/behind failure", partial.Warnings)
	}

	feature := findBranch(branches, "feature")
	if feature == nil {
		t.Fatal("branch feature not found")
	}
	if feature.Ahead != 0 || feature.Behind != 0 {
		t.Fatalf("ahead/behind should remain zero after best-effort failure, got %+v", *feature)
	}
	if len(feature.Unpushed) != 0 {
		t.Fatalf("unpushed commits should not be read when ahead count is unavailable, got %v", feature.Unpushed)
	}
	if fakeCallContains(f.calls, "log --oneline") {
		t.Fatalf("log --oneline should not run after ahead/behind failure; calls: %v", f.calls)
	}
}

func TestQuerierListBranches_ForEachRefFailureIsFatal(t *testing.T) {
	wantErr := errors.New("fatal: not a git repository")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				err: wantErr,
			},
		},
		ok: map[string]error{},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected for-each-ref error, got %v", err)
	}
	if branches != nil {
		t.Fatalf("expected no branches on fatal error, got %+v", branches)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected query to stop after fatal for-each-ref, calls: %v", f.calls)
	}
}

func TestQuerierListBranches_UnpushedCommitsOnlyReadForAheadBranches(t *testing.T) {
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "behind\trefs/remotes/origin/behind\t[behind 1]\nfeature\trefs/remotes/origin/feature\t[ahead 2]\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey("/repo", "rev-list", "--count", "--left-right", "behind...refs/remotes/origin/behind"): {
				out: "0 1\n",
			},
			fakeKey("/repo", "rev-list", "--count", "--left-right", "feature...refs/remotes/origin/feature"): {
				out: "2 0\n",
			},
			fakeKey("/repo", "log", "--oneline", "refs/remotes/origin/feature..feature"): {
				out: "abc123 local change\n789def another change\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "behind", "main"):  errors.New("not merged"),
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): errors.New("not merged"),
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	feature := findBranch(branches, "feature")
	if feature == nil {
		t.Fatal("branch feature not found")
	}
	if feature.Ahead != 2 || feature.Behind != 0 {
		t.Fatalf("unexpected feature ahead/behind: %+v", *feature)
	}
	if len(feature.Unpushed) != 2 {
		t.Fatalf("expected two unpushed commits, got %v", feature.Unpushed)
	}
	if fakeCallContains(f.calls, "refs/remotes/origin/behind..behind") {
		t.Fatalf("behind-only branch should not read unpushed log; calls: %v", f.calls)
	}
}

func TestQuerierListBranches_ReportsUnpushedCommitFailure(t *testing.T) {
	wantErr := errors.New("cannot read commit graph")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\trefs/remotes/origin/feature\t[ahead 2]\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey("/repo", "rev-list", "--count", "--left-right", "feature...refs/remotes/origin/feature"): {out: "2 0\n"},
			fakeKey("/repo", "log", "--oneline", "refs/remotes/origin/feature..feature"):                     {err: wantErr},
			fakeKey("/repo", "status", "--porcelain"):                                                        {},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): errors.New("not merged"),
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	feature := findBranch(branches, "feature")
	if feature == nil || len(feature.Unpushed) != 0 {
		t.Fatalf("feature = %#v, want retained row without unpushed details", feature)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok || len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "unpushed commits" || !errors.Is(partial.Warnings[0].Cause, wantErr) {
		t.Fatalf("partial = %#v, err = %v, want unpushed-commit failure", partial, err)
	}
}

func TestQuerierListBranches_DirtyStatusFailureIsBestEffort(t *testing.T) {
	featurePath := t.TempDir()
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\t\t\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n\nworktree " + featurePath + "\nHEAD def456\nbranch refs/heads/feature\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
			fakeKey(featurePath, "status", "--porcelain"): {
				err: errors.New("fatal: cannot read index"),
			},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): errors.New("not merged"),
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok {
		t.Fatalf("AsPartialQuery(%v) = false, want non-fatal enrichment diagnostic", err)
	}
	if len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "dirty status" || partial.Warnings[0].Subject != featurePath {
		t.Fatalf("partial warnings = %#v, want feature dirty-status failure", partial.Warnings)
	}

	feature := findBranch(branches, "feature")
	if feature == nil {
		t.Fatal("branch feature not found")
	}
	if feature.Dirty || feature.FilesChanged != 0 || feature.LinesAdded != 0 || feature.LinesDeleted != 0 {
		t.Fatalf("dirty status failure should leave counts empty, got %+v", *feature)
	}
	if fakeCallContains(f.calls, "diff HEAD --numstat") {
		t.Fatalf("diff should not run when status fails; calls: %v", f.calls)
	}
}

func TestQuerierListBranches_OkProbeMarksMergedBranches(t *testing.T) {
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\t\t\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): nil,
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}

	feature := findBranch(branches, "feature")
	if feature == nil {
		t.Fatal("branch feature not found")
	}
	if !feature.Merged || feature.MergedInto != "main" {
		t.Fatalf("expected feature to be marked merged into main, got %+v", *feature)
	}
	if !fakeCallContains(f.calls, "ok /repo | merge-base --is-ancestor feature main") {
		t.Fatalf("expected merge-base probe call, calls: %v", f.calls)
	}
}

func TestQuerierListBranches_ReportsMergedPredicateFailure(t *testing.T) {
	wantErr := errors.New("merge-base unavailable")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\t\t\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
		},
		predicateFailures: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): wantErr,
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	feature := findBranch(branches, "feature")
	if feature == nil || feature.Merged {
		t.Fatalf("feature = %#v, want retained unmerged row", feature)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok || len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "merged status" || !errors.Is(partial.Warnings[0].Cause, wantErr) {
		t.Fatalf("partial = %#v, err = %v, want merged-status failure", partial, err)
	}
}

func TestQuerierListBranches_MissingOriginHEADIsNotAFailure(t *testing.T) {
	featurePath := t.TempDir()
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "dev\t\t\nfeature\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree " + featurePath + "\nHEAD abc123\nbranch refs/heads/feature\n",
			},
			fakeKey("/repo", "branch", "--show-current"): {},
			fakeKey("/repo", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"): {
				err: quietRefMissingError(t),
			},
			fakeKey(featurePath, "status", "--porcelain"): {},
		},
		ok: map[string]error{},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	if err != nil {
		t.Fatalf("ListBranches() error = %v, want no warning for a missing origin HEAD", err)
	}
	if len(branches) != 2 {
		t.Fatalf("ListBranches() = %#v, want retained rows", branches)
	}
}

func TestQuerierListBranches_ReportsRootAndDefaultBranchFallbackFailures(t *testing.T) {
	wantRootErr := errors.New("cannot identify current branch")
	wantDefaultErr := errors.New("origin HEAD is corrupt")
	featurePath := t.TempDir()
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "dev\t\t\nfeature\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree " + featurePath + "\nHEAD abc123\nbranch refs/heads/feature\n",
			},
			fakeKey("/repo", "branch", "--show-current"):                                       {err: wantRootErr},
			fakeKey("/repo", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"): {err: wantDefaultErr},
			fakeKey(featurePath, "status", "--porcelain"):                                      {},
		},
		ok: map[string]error{},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	if len(branches) != 2 {
		t.Fatalf("ListBranches() = %#v, want retained rows", branches)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok || len(partial.Warnings) != 2 {
		t.Fatalf("partial = %#v, err = %v, want two fallback warnings", partial, err)
	}
	if partial.Warnings[0].Operation != "root branch" || !errors.Is(partial.Warnings[0].Cause, wantRootErr) ||
		partial.Warnings[1].Operation != "default branch" || !errors.Is(partial.Warnings[1].Cause, wantDefaultErr) {
		t.Fatalf("partial warnings = %#v", partial.Warnings)
	}
}

func TestQuerierListWorktrees_UsesInjectedRunnerForDirtyStatus(t *testing.T) {
	mainPath := t.TempDir()
	dirtyPath := t.TempDir()
	stalePath := filepath.Join(t.TempDir(), "missing")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey(mainPath, "worktree", "list", "--porcelain"): {
				out: "worktree " + mainPath + "\nHEAD abc123\nbranch refs/heads/main\n\nworktree " + dirtyPath + "\nHEAD def456\nbranch refs/heads/feature\n\nworktree " + stalePath + "\nHEAD 789abc\nbranch refs/heads/stale\n",
			},
			fakeKey(mainPath, "status", "--porcelain"): {},
			fakeKey(dirtyPath, "status", "--porcelain"): {
				out: " M f.txt\n?? new.txt\n",
			},
			fakeKey(dirtyPath, "diff", "HEAD", "--numstat"): {
				out: "3\t1\tf.txt\n-\t-\tbinary.dat\n",
			},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees(mainPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(worktrees) != 3 {
		t.Fatalf("expected three worktrees, got %+v", worktrees)
	}
	if !worktrees[0].IsMain || worktrees[0].Path != mainPath {
		t.Fatalf("expected main worktree first, got %+v", worktrees[0])
	}
	if !worktrees[1].Dirty || worktrees[1].FilesChanged != 2 || worktrees[1].LinesAdded != 3 || worktrees[1].LinesDeleted != 1 {
		t.Fatalf("expected dirty counts on feature worktree, got %+v", worktrees[1])
	}
	if !worktrees[2].Stale {
		t.Fatalf("expected missing worktree to be stale, got %+v", worktrees[2])
	}
	if fakeCallContains(f.calls, stalePath+" | status --porcelain") {
		t.Fatalf("stale worktree should not read dirty status; calls: %v", f.calls)
	}
}

func TestQuerierListWorktrees_KeepsDirtyFlagWhenDiffHEADFails(t *testing.T) {
	worktreePath := t.TempDir()
	wantErr := errors.New("fatal: bad revision 'HEAD'")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree " + worktreePath + "\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey(worktreePath, "status", "--porcelain"): {
				out: "?? new.txt\n",
			},
			fakeKey(worktreePath, "diff", "HEAD", "--numstat"): {err: wantErr},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/repo")
	if len(worktrees) != 1 || !worktrees[0].Dirty || worktrees[0].FilesChanged != 1 {
		t.Fatalf("ListWorktrees() = %#v, want dirty row with file count", worktrees)
	}
	if worktrees[0].LinesAdded != 0 || worktrees[0].LinesDeleted != 0 {
		t.Fatalf("line counts = +%d/-%d, want zeros when numstat fails", worktrees[0].LinesAdded, worktrees[0].LinesDeleted)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok || len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "dirty status" || !errors.Is(partial.Warnings[0].Cause, wantErr) {
		t.Fatalf("partial = %#v, err = %v, want dirty-status warning", partial, err)
	}
}

func TestQuerierListBranches_KeepsDirtyFlagWhenDiffHEADFails(t *testing.T) {
	featurePath := t.TempDir()
	wantErr := errors.New("fatal: bad revision 'HEAD'")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "feature\t\t\nmain\t\t\n",
			},
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n\nworktree " + featurePath + "\nHEAD def456\nbranch refs/heads/feature\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
			fakeKey(featurePath, "status", "--porcelain"): {
				out: "?? new.txt\n",
			},
			fakeKey(featurePath, "diff", "HEAD", "--numstat"): {err: wantErr},
		},
		ok: map[string]error{
			fakeKey("/repo", "merge-base", "--is-ancestor", "feature", "main"): errors.New("not merged"),
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches("/repo")
	feature := findBranch(branches, "feature")
	if feature == nil || !feature.Dirty || feature.FilesChanged != 1 {
		t.Fatalf("feature = %#v, want dirty row with file count", feature)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok || len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "dirty status" || !errors.Is(partial.Warnings[0].Cause, wantErr) {
		t.Fatalf("partial = %#v, err = %v, want dirty-status warning", partial, err)
	}
}

func TestQuerierListWorktrees_ReturnsRowsWithDirtyStatusDiagnostic(t *testing.T) {
	worktreePath := t.TempDir()
	wantErr := errors.New("fatal: cannot read index")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				out: "worktree " + worktreePath + "\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey(worktreePath, "status", "--porcelain"): {err: wantErr},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/repo")
	if len(worktrees) != 1 || worktrees[0].Path != worktreePath {
		t.Fatalf("ListWorktrees() = %#v, want usable worktree row", worktrees)
	}
	partial, ok := gitquery.AsPartialQuery(err)
	if !ok {
		t.Fatalf("AsPartialQuery(%v) = false, want non-fatal enrichment diagnostic", err)
	}
	if len(partial.Warnings) != 1 || partial.Warnings[0].Operation != "dirty status" || partial.Warnings[0].Subject != worktreePath || !errors.Is(partial.Warnings[0].Cause, wantErr) {
		t.Fatalf("partial warnings = %#v, want worktree dirty-status failure", partial.Warnings)
	}
}

func TestQuerierListWorktrees_SkipsBareRootWithoutRootLabel(t *testing.T) {
	worktreePath := t.TempDir()
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/dev/project.git", "worktree", "list", "--porcelain"): {
				out: "worktree /dev/project.git\nbare\n\nworktree " + worktreePath + "\nHEAD abc123\nbranch refs/heads/feature\n",
			},
			fakeKey(worktreePath, "status", "--porcelain"): {},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/dev/project.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("expected one attached worktree, got %+v", worktrees)
	}
	if worktrees[0].Path != worktreePath {
		t.Fatalf("expected worktree path %q, got %+v", worktreePath, worktrees[0])
	}
	if worktrees[0].BranchName != "feature" {
		t.Fatalf("expected branch feature, got %+v", worktrees[0])
	}
	if worktrees[0].IsMain {
		t.Fatalf("attached worktree from bare repo should not be marked root: %+v", worktrees[0])
	}
	if fakeCallContains(f.calls, "/dev/project.git | status --porcelain") {
		t.Fatalf("bare root should not be checked for dirty status; calls: %v", f.calls)
	}
}

func TestQuerierListWorktrees_BareRepoWithNoCheckoutsReturnsEmpty(t *testing.T) {
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/dev/project.git", "worktree", "list", "--porcelain"): {
				out: "worktree /dev/project.git\nbare\n",
			},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/dev/project.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(worktrees) != 0 {
		t.Fatalf("expected no worktrees, got %+v", worktrees)
	}
}

func TestQuerierListWorktrees_RootLabelAllowsCleanedRepoPath(t *testing.T) {
	mainPath := t.TempDir()
	repoPath := mainPath + string(filepath.Separator)
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey(repoPath, "worktree", "list", "--porcelain"): {
				out: "worktree " + mainPath + "\nHEAD abc123\nbranch refs/heads/main\n",
			},
			fakeKey(mainPath, "status", "--porcelain"): {},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees(repoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("expected one worktree, got %+v", worktrees)
	}
	if !worktrees[0].IsMain {
		t.Fatalf("expected cleaned repo path to match root worktree, got %+v", worktrees[0])
	}
}

func TestQuerierListBranches_RootBranchAllowsCleanedRepoPath(t *testing.T) {
	repoPath := "/repo/"
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey(repoPath, "for-each-ref", "--format=%(refname:short)\t%(refname)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
				out: "dev\trefs/heads/dev\t\t\nfeature\trefs/heads/feature\t\t\n",
			},
			fakeKey(repoPath, "worktree", "list", "--porcelain"): {
				out: "worktree /repo\nHEAD abc123\nbranch refs/heads/dev\n",
			},
			fakeKey("/repo", "status", "--porcelain"): {},
		},
		ok: map[string]error{
			fakeKey(repoPath, "merge-base", "--is-ancestor", "feature", "dev"): nil,
		},
	}

	branches, err := gitquery.NewQuerier(f).ListBranches(repoPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dev := findBranch(branches, "dev")
	if dev == nil {
		t.Fatal("branch dev not found")
	}
	if dev.Merged {
		t.Fatalf("root worktree branch should not be marked merged, got %+v", *dev)
	}
	feature := findBranch(branches, "feature")
	if feature == nil {
		t.Fatal("branch feature not found")
	}
	if !feature.Merged || feature.MergedInto != "dev" {
		t.Fatalf("expected feature merged into root branch dev, got %+v", *feature)
	}
}

func TestQuerierListWorktrees_WorktreeListFailureIsFatal(t *testing.T) {
	wantErr := errors.New("fatal: bad worktree metadata")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
				err: wantErr,
			},
		},
	}

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/repo")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected worktree list error, got %v", err)
	}
	if worktrees != nil {
		t.Fatalf("expected no worktrees on fatal error, got %+v", worktrees)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected query to stop after fatal worktree list, calls: %v", f.calls)
	}
}
