package gitquery_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/gitquery"
)

type fakeReply struct {
	out string
	err error
}

type fakeRunner struct {
	t     *testing.T
	run   map[string]fakeReply
	ok    map[string]error
	calls []string
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

func (f *fakeRunner) Ok(dir string, args ...string) error {
	key := fakeKey(dir, args...)
	f.calls = append(f.calls, "ok "+key)
	err, ok := f.ok[key]
	if !ok {
		f.t.Fatalf("unexpected Ok call: %s", key)
	}
	return err
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
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
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
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
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
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
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
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
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

func TestQuerierListBranches_DirtyStatusFailureIsBestEffort(t *testing.T) {
	featurePath := t.TempDir()
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
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
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
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
			fakeKey("/repo", "for-each-ref", "--format=%(refname:short)\t%(upstream)\t%(upstream:track)", "refs/heads/"): {
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

func TestQuerierListWorktrees_UsesInjectedRunnerForDirtyStatus(t *testing.T) {
	mainPath := t.TempDir()
	dirtyPath := t.TempDir()
	stalePath := filepath.Join(t.TempDir(), "missing")
	f := &fakeRunner{
		t: t,
		run: map[string]fakeReply{
			fakeKey("/repo", "worktree", "list", "--porcelain"): {
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

	worktrees, err := gitquery.NewQuerier(f).ListWorktrees("/repo")
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
