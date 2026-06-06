package model_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/actions"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
	"github.com/brian-bell/wtui/ui"
)

// These tests back the Model with a real temporary git repository so the fetch
// and diff commands actually execute git and return their success messages with
// real payloads. They cover the success path that the fake-path navigation tests
// deliberately cannot (those only assert synchronous dispatch). This matches the
// project convention of testing against real git repos rather than mocks.

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupModelRepo creates a real git repo with one committed file and returns a
// Model whose single (selected) repo points at it.
func setupModelRepo(t *testing.T) (model.Model, string) {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (e.g. macOS /tmp -> /private/tmp) so the repo path
	// matches the canonical paths git reports for worktrees.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	writeFile(t, dir, "README.md", "hello\n")
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "initial commit")
	m := model.New([]scanner.Repo{{Path: dir, DisplayName: filepath.Base(dir)}})
	return m, dir
}

func setupModelRepoWithOptions(t *testing.T, opts model.Options) (model.Model, string) {
	t.Helper()
	_, dir := setupModelRepo(t)
	m := model.NewWithOptions([]scanner.Repo{{Path: dir, DisplayName: filepath.Base(dir)}}, opts)
	return m, dir
}

func setupModelPullRequestRepoWithOptions(t *testing.T, opts model.Options) (model.Model, string) {
	t.Helper()
	dir := t.TempDir()
	upstream := filepath.Join(dir, "upstream")
	origin := filepath.Join(dir, "origin.git")
	local := filepath.Join(dir, "local")
	mustGit(t, dir, "init", upstream)
	mustGit(t, upstream, "config", "user.email", "test@test.com")
	mustGit(t, upstream, "config", "user.name", "Test")
	writeFile(t, upstream, "README.md", "hello\n")
	mustGit(t, upstream, "add", "README.md")
	mustGit(t, upstream, "commit", "-m", "initial commit")
	mustGit(t, dir, "init", "--bare", origin)
	mustGit(t, upstream, "remote", "add", "origin", origin)
	mustGit(t, upstream, "push", "-u", "origin", "HEAD")
	mustGit(t, upstream, "checkout", "-b", "pr-head")
	writeFile(t, upstream, "pr.txt", "review me\n")
	mustGit(t, upstream, "add", "pr.txt")
	mustGit(t, upstream, "commit", "-m", "pr change")
	mustGit(t, upstream, "push", "origin", "HEAD:refs/pull/123/head")
	mustGit(t, dir, "clone", origin, local)
	if resolved, err := filepath.EvalSymlinks(local); err == nil {
		local = resolved
	}
	mustGit(t, local, "config", "user.email", "test@test.com")
	mustGit(t, local, "config", "user.name", "Test")
	m := model.NewWithOptions([]scanner.Repo{{Path: local, DisplayName: filepath.Base(local)}}, opts)
	return m, local
}

// TestModel_ModeFetchesProduceResultsAgainstRealRepo verifies that each mode's
// fetch command, run against a real repo, returns its success result message
// (not an error). This is the success-path counterpart to the fake-path
// dispatch tests that only assert a command was returned.
func TestModel_ModeFetchesProduceResultsAgainstRealRepo(t *testing.T) {
	t.Run("worktrees via Init", func(t *testing.T) {
		m, _ := setupModelRepo(t)
		msg := m.Init()()
		if _, ok := msg.(model.WorktreeResultMsg); !ok {
			t.Fatalf("expected WorktreeResultMsg, got %T: %v", msg, msg)
		}
	})

	cases := []struct {
		name string
		key  rune
		want func(tea.Msg) bool
	}{
		{"branches", '2', func(m tea.Msg) bool { _, ok := m.(model.BranchResultMsg); return ok }},
		{"stashes", '3', func(m tea.Msg) bool { _, ok := m.(model.StashResultMsg); return ok }},
		{"history", '4', func(m tea.Msg) bool { _, ok := m.(model.CommitResultMsg); return ok }},
		{"reflog", '5', func(m tea.Msg) bool { _, ok := m.(model.ReflogResultMsg); return ok }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := setupModelRepo(t)
			m = inRightPane(m)
			_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			if cmd == nil {
				t.Fatalf("expected fetch cmd for %s, got nil", tc.name)
			}
			msg := cmd()
			if !tc.want(msg) {
				t.Fatalf("expected success result for %s, got %T: %v", tc.name, msg, msg)
			}
		})
	}
}

func TestModel_WorktreeDiffPayloadAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	writeFile(t, dir, "README.md", "hello\nchanged\n")

	m = inRightPane(m)
	m, _ = update(m, m.Init()()) // load real worktrees (root is dirty)

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayWorktreeDiff {
		t.Fatalf("expected OverlayWorktreeDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchWorktreeDiff cmd, got nil")
	}
	res, ok := cmd().(model.WorktreeDiffResultMsg)
	if !ok {
		t.Fatalf("expected WorktreeDiffResultMsg, got %T", cmd())
	}
	if !strings.Contains(res.Diff, "README.md") {
		t.Errorf("expected diff to mention changed file, got %q", res.Diff)
	}
}

func TestModel_BranchDiffPayloadAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	branch := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	writeFile(t, dir, "README.md", "hello\nchanged\n")

	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}) // branches
	if cmd == nil {
		t.Fatal("expected fetchBranches cmd, got nil")
	}
	m, _ = update(m, cmd()) // load real branches (root branch dirty)

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayBranchDiff {
		t.Fatalf("expected OverlayBranchDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchBranchDiff cmd, got nil")
	}
	res, ok := cmd().(model.BranchDiffResultMsg)
	if !ok {
		t.Fatalf("expected BranchDiffResultMsg, got %T", cmd())
	}
	if res.BranchName != branch {
		t.Errorf("expected branch name %q, got %q", branch, res.BranchName)
	}
	if !strings.Contains(res.Diff, "README.md") {
		t.Errorf("expected diff to mention changed file, got %q", res.Diff)
	}
}

func TestModel_CreateBranchFromSelectedBranchAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	initial := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	mustGit(t, dir, "checkout", "-b", "base")
	writeFile(t, dir, "base.txt", "base\n")
	mustGit(t, dir, "add", "base.txt")
	mustGit(t, dir, "commit", "-m", "base commit")
	baseHead := gitOut(t, dir, "rev-parse", "base")
	mustGit(t, dir, "checkout", initial)
	mustGit(t, dir, "tag", "base", initial)

	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd == nil {
		t.Fatal("expected branches fetch cmd")
	}
	m, _ = update(m, cmd())
	baseIndex := -1
	for i, row := range m.Rows() {
		if row.Branch.Name == "base" || row.Branch.Name == "heads/base" {
			baseIndex = i
			break
		}
	}
	if baseIndex == -1 {
		t.Fatalf("expected base branch in rows: %+v", m.Rows())
	}
	for i := 0; i < baseIndex; i++ {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/from-base")})
	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create branch cmd")
	}
	msg := cmd()
	if _, ok := msg.(model.BranchCreatedMsg); !ok {
		t.Fatalf("expected BranchCreatedMsg, got %T", msg)
	}
	got := gitOut(t, dir, "rev-parse", "feature/from-base")
	if got != baseHead {
		t.Fatalf("expected feature/from-base at base %s, got %s", baseHead, got)
	}
	if current := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); current != initial {
		t.Fatalf("expected current branch to remain %s, got %s", initial, current)
	}
	if m.Mode() != ui.ModeBranches {
		t.Fatalf("expected mode branches, got %d", m.Mode())
	}
}

func TestModel_MoveWorktreeAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	root := filepath.Dir(dir)
	oldPath := filepath.Join(root, "repo-worktrees", "feat")
	newPath := filepath.Join(root, "repo-worktrees", "feat-renamed")
	mustGit(t, dir, "worktree", "add", oldPath, "-b", "feat")

	m = inRightPane(m)
	m, _ = update(m, m.Init()())
	if len(m.Worktrees()) != 2 {
		t.Fatalf("expected root and linked worktree, got %+v", m.Worktrees())
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feat-renamed")})
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected move command")
	}
	moved, ok := cmd().(model.WorktreeMovedMsg)
	if !ok {
		t.Fatalf("expected WorktreeMovedMsg, got %T", cmd())
	}
	if moved.OldPath != oldPath || moved.NewPath != newPath {
		t.Fatalf("unexpected move message: %+v", moved)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old worktree path removed, stat err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected new worktree path to exist: %v", err)
	}

	m, cmd = update(m, moved)
	if cmd == nil {
		t.Fatal("expected refresh command after move")
	}
	m, _ = update(m, cmd())
	if got := m.Worktrees()[m.WorktreeSelected()].Path; got != newPath {
		t.Fatalf("expected moved worktree selected at %q, got %q", newPath, got)
	}
}

func TestModel_StashDiffPayloadAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	writeFile(t, dir, "README.md", "hello\nstashed\n")
	mustGit(t, dir, "stash")

	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) // stashes
	if cmd == nil {
		t.Fatal("expected fetchStashes cmd, got nil")
	}
	m, _ = update(m, cmd()) // load real stashes

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayStashDiff {
		t.Fatalf("expected OverlayStashDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchStashDiff cmd, got nil")
	}
	res, ok := cmd().(model.StashDiffResultMsg)
	if !ok {
		t.Fatalf("expected StashDiffResultMsg, got %T", cmd())
	}
	if !strings.Contains(res.Diff, "stashed") {
		t.Errorf("expected stash diff to contain change, got %q", res.Diff)
	}
}

func TestModel_CommitDiffPayloadAgainstRealRepo(t *testing.T) {
	m, _ := setupModelRepo(t)

	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}}) // history
	if cmd == nil {
		t.Fatal("expected fetchCommits cmd, got nil")
	}
	m, _ = update(m, cmd()) // load real commits

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayCommitDiff {
		t.Fatalf("expected OverlayCommitDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchCommitDiff cmd, got nil")
	}
	res, ok := cmd().(model.CommitDiffResultMsg)
	if !ok {
		t.Fatalf("expected CommitDiffResultMsg, got %T", cmd())
	}
	if !strings.Contains(res.Diff, "initial commit") {
		t.Errorf("expected commit diff to contain commit message, got %q", res.Diff)
	}
}

func TestModel_ReflogDiffPayloadAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	// A second commit gives the latest reflog entry a parent to diff against.
	writeFile(t, dir, "README.md", "hello\nsecond\n")
	mustGit(t, dir, "commit", "-am", "second commit")

	m = inRightPane(m)
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}}) // reflog
	if cmd == nil {
		t.Fatal("expected fetchReflog cmd, got nil")
	}
	m, _ = update(m, cmd()) // load real reflog entries

	m, cmd = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Overlay() != ui.OverlayReflogDiff {
		t.Fatalf("expected OverlayReflogDiff, got %d", m.Overlay())
	}
	if cmd == nil {
		t.Fatal("expected fetchReflogDiff cmd, got nil")
	}
	res, ok := cmd().(model.ReflogDiffResultMsg)
	if !ok {
		t.Fatalf("expected ReflogDiffResultMsg, got %T", cmd())
	}
	if !strings.Contains(res.Diff, "README.md") {
		t.Errorf("expected reflog diff to mention changed file, got %q", res.Diff)
	}
}

func TestModel_AgentLaunchAgainstRealRepo(t *testing.T) {
	var launchedPath string
	m, dir := setupModelRepoWithOptions(t, model.Options{
		AgentCommand: "codex",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchedPath = ctx.WorktreePath
			cmd := exec.Command("pwd")
			cmd.Dir = ctx.WorktreePath
			return actions.TerminalLaunchSpec{Cmd: cmd}, nil
		},
	})

	m = inRightPane(m)
	m, _ = update(m, m.Init()())
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected agent launch command")
	}
	msg := cmd()
	if got, ok := msg.(model.AgentResultMsg); !ok || got.Err != "" {
		t.Fatalf("expected successful AgentResultMsg, got %T: %#v", msg, msg)
	}
	if launchedPath != dir {
		t.Fatalf("expected launch from repo worktree %q, got %q", dir, launchedPath)
	}
}

func TestModel_CreateThenAgentLaunchAgainstRealRepo(t *testing.T) {
	var launchedPath string
	m, dir := setupModelRepoWithOptions(t, model.Options{
		AgentCommand: "claude",
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launchedPath = ctx.WorktreePath
			cmd := exec.Command("pwd")
			cmd.Dir = ctx.WorktreePath
			return actions.TerminalLaunchSpec{Cmd: cmd}, nil
		},
	})

	m = inRightPane(m)
	m, _ = update(m, m.Init()())
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("agent-smoke")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected create worktree command")
	}
	msg := createCmd()
	created, ok := msg.(model.WorktreeCreatedMsg)
	if !ok {
		t.Fatalf("expected WorktreeCreatedMsg, got %T", msg)
	}
	if !created.LaunchAgent {
		t.Fatal("expected created message to request agent launch")
	}

	_, batchCmd := update(m, created)
	if batchCmd == nil {
		t.Fatal("expected create+launch batch command")
	}
	var agentResult model.AgentResultMsg
	switch msg := batchCmd().(type) {
	case tea.BatchMsg:
		for _, batched := range msg {
			if got, ok := batched().(model.AgentResultMsg); ok {
				agentResult = got
			}
		}
	case model.AgentResultMsg:
		agentResult = msg
	default:
		t.Fatalf("expected BatchMsg or AgentResultMsg, got %T", msg)
	}
	if agentResult.Err != "" {
		t.Fatalf("expected successful agent launch, got %q", agentResult.Err)
	}

	want := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-worktrees", "agent-smoke")
	if launchedPath != want {
		t.Fatalf("expected launch from created worktree %q, got %q", want, launchedPath)
	}
}

func TestModel_CreateWorktreeRunsBootstrapHookAgainstRealRepo(t *testing.T) {
	var gotCtx actions.BootstrapContext
	var gotHook actions.BootstrapHook
	var m model.Model
	var dir string
	m, dir = setupModelRepoWithOptions(t, model.Options{
		BootstrapHookForRepo: func(repoPath string) (actions.BootstrapHook, bool) {
			if repoPath != dir {
				t.Fatalf("expected hook lookup for %q, got %q", dir, repoPath)
			}
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 7}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, hook actions.BootstrapHook) error {
			gotCtx = ctx
			gotHook = hook
			return nil
		},
	})

	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("bootstrap-smoke")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected create worktree command")
	}
	msg := createCmd()
	created, ok := msg.(model.WorktreeCreatedMsg)
	if !ok {
		t.Fatalf("expected WorktreeCreatedMsg, got %T", msg)
	}
	if !created.BootstrapRan {
		t.Fatal("expected created message to record bootstrap run")
	}
	wantPath := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-worktrees", "bootstrap-smoke")
	if gotCtx.RepoPath != dir || gotCtx.WorktreePath != wantPath || gotCtx.Ref != "bootstrap-smoke" || gotCtx.Kind != actions.WorktreeCreateGeneric {
		t.Fatalf("unexpected bootstrap context: %#v", gotCtx)
	}
	if gotHook.Script != ".wtui/bootstrap" || gotHook.TimeoutSeconds != 7 {
		t.Fatalf("unexpected bootstrap hook: %#v", gotHook)
	}
}

func TestModel_CreateWorktreeWithoutHookPreservesCreatedMessageAgainstRealRepo(t *testing.T) {
	m, _ := setupModelRepo(t)
	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("no-hook")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected create worktree command")
	}
	msg := createCmd()
	created, ok := msg.(model.WorktreeCreatedMsg)
	if !ok {
		t.Fatalf("expected WorktreeCreatedMsg, got %T", msg)
	}
	if created.BootstrapRan {
		t.Fatal("expected bootstrap to be skipped when no hook is configured")
	}
}

func TestModel_BootstrapFailureRefreshesWorktreesAndShowsStatus(t *testing.T) {
	m := inBranchesMode(model.New(testRepos()))
	m, cmd := update(m, model.WorktreeBootstrapFailedMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/feat",
		Err:          "setup failed",
	})
	if m.Mode() != ui.ModeWorktrees {
		t.Fatalf("expected mode worktrees after bootstrap failure, got %d", m.Mode())
	}
	if m.Overlay() != ui.OverlayNone {
		t.Fatalf("expected input modal to stay closed, got overlay %d", m.Overlay())
	}
	if !strings.Contains(m.TransientError(), "bootstrap hook failed: setup failed") {
		t.Fatalf("expected bootstrap error status, got %q", m.TransientError())
	}
	if cmd == nil {
		t.Fatal("expected worktree refresh command")
	}
}

func TestModel_StaleBootstrapFailureIsIgnored(t *testing.T) {
	m := model.New(testRepos())
	m, cmd := update(m, model.WorktreeBootstrapFailedMsg{
		RepoPath:     "/dev/bravo",
		WorktreePath: "/dev/bravo-worktrees/feat",
		Err:          "setup failed",
	})
	if cmd != nil {
		t.Fatal("expected stale bootstrap failure to be ignored")
	}
	if m.TransientError() != "" {
		t.Fatalf("expected no status for stale bootstrap failure, got %q", m.TransientError())
	}
}

func TestModel_StaleBootstrapFailureRequestIsIgnoredAfterNewerSubmit(t *testing.T) {
	m := inWorktreesMode(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("old")})
	m, firstCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new")})
	m, secondCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if firstCmd == nil || secondCmd == nil {
		t.Fatal("expected create commands")
	}

	m, cmd := update(m, model.WorktreeBootstrapFailedMsg{
		RepoPath:     "/dev/alpha",
		WorktreePath: "/dev/alpha-worktrees/old",
		Err:          "old setup failed",
		Request:      1,
	})
	if cmd != nil {
		t.Fatal("expected stale bootstrap failure request to be ignored")
	}
	if m.TransientError() != "" {
		t.Fatalf("expected no status for stale request, got %q", m.TransientError())
	}
}

func TestModel_CancelledPromptDoesNotSupersedeInFlightCreate(t *testing.T) {
	m := inWorktreesMode(model.New(testRepos()))
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("old")})
	m, firstCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if firstCmd == nil {
		t.Fatal("expected create command")
	}
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEsc})

	msg := firstCmd()
	m, _ = update(m, msg)
	if m.Overlay() != ui.OverlayWorktreeInput {
		t.Fatalf("expected in-flight create failure to reopen input, got %d", m.Overlay())
	}
	if m.WorktreeInput() != "old" {
		t.Fatalf("expected original input restored, got %q", m.WorktreeInput())
	}
}

func TestModel_PullRequestWorktreeBootstrapFailureAgainstRealRepo(t *testing.T) {
	var m model.Model
	var dir string
	m, dir = setupModelPullRequestRepoWithOptions(t, model.Options{
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 5}, true
		},
		RunBootstrapHook: func(ctx actions.BootstrapContext, hook actions.BootstrapHook) error {
			if ctx.Kind != actions.WorktreeCreatePullRequest || ctx.Ref != "123" || ctx.RepoPath != dir {
				t.Fatalf("unexpected PR bootstrap context: %#v", ctx)
			}
			return errors.New("pr setup failed")
		},
	})

	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("123")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if createCmd == nil {
		t.Fatal("expected create PR worktree command")
	}
	msg := createCmd()
	failed, ok := msg.(model.WorktreeBootstrapFailedMsg)
	if !ok {
		t.Fatalf("expected WorktreeBootstrapFailedMsg, got %T", msg)
	}
	if failed.WorktreePath == "" || !strings.Contains(failed.Err, "pr setup failed") {
		t.Fatalf("unexpected bootstrap failure message: %#v", failed)
	}
}

func TestModel_CreateThenAgentLaunchWaitsForBootstrapHook(t *testing.T) {
	var launchedPath string
	hookRan := false
	m, dir := setupModelRepoWithOptions(t, model.Options{
		AgentCommand: "codex",
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 5}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			hookRan = true
			return nil
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			if !hookRan {
				t.Fatal("agent launched before bootstrap hook completed")
			}
			launchedPath = ctx.WorktreePath
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})

	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("agent-bootstrap")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := createCmd()
	created, ok := msg.(model.WorktreeCreatedMsg)
	if !ok {
		t.Fatalf("expected WorktreeCreatedMsg, got %T", msg)
	}
	if !created.LaunchAgent || !created.BootstrapRan {
		t.Fatalf("expected launch + bootstrap metadata, got %#v", created)
	}
	_, batchCmd := update(m, created)
	if batchCmd == nil {
		t.Fatal("expected launch command after successful bootstrap")
	}
	want := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-worktrees", "agent-bootstrap")
	if launchedPath != want {
		t.Fatalf("expected launch from created worktree %q, got %q", want, launchedPath)
	}
	_ = batchCmd()
}

func TestModel_CreateThenAgentLaunchSkipsAgentWhenBootstrapFails(t *testing.T) {
	launched := false
	m, _ := setupModelRepoWithOptions(t, model.Options{
		AgentCommand: "codex",
		BootstrapHookForRepo: func(string) (actions.BootstrapHook, bool) {
			return actions.BootstrapHook{Script: ".wtui/bootstrap", TimeoutSeconds: 5}, true
		},
		RunBootstrapHook: func(actions.BootstrapContext, actions.BootstrapHook) error {
			return errors.New("setup failed")
		},
		LaunchAgent: func(ctx actions.AgentLaunchContext) (actions.TerminalLaunchSpec, error) {
			launched = true
			return actions.TerminalLaunchSpec{Cmd: exec.Command("true")}, nil
		},
	})

	m = inRightPane(m)
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("agent-bootstrap-fail")})
	m, createCmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	msg := createCmd()
	failed, ok := msg.(model.WorktreeBootstrapFailedMsg)
	if !ok {
		t.Fatalf("expected WorktreeBootstrapFailedMsg, got %T", msg)
	}
	if !failed.LaunchAgent {
		t.Fatal("expected bootstrap failure to preserve original launch intent")
	}
	_, refreshCmd := update(m, failed)
	if refreshCmd == nil {
		t.Fatal("expected refresh command after bootstrap failure")
	}
	if launched {
		t.Fatal("agent should not launch when bootstrap fails")
	}
}

// TestModel_CombinedCleanupForceDeleteSucceedsAgainstRealRepo drives the combined
// cleanup flow to the point where a normal branch delete fails (unmerged branch)
// but the force delete succeeds, and verifies the threaded WorktreeDeleteCompletedMsg
// is returned (not BranchDeletedMsg).
func TestModel_CombinedCleanupForceDeleteSucceedsAgainstRealRepo(t *testing.T) {
	m, dir := setupModelRepo(t)
	base := gitOut(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	// Create an unmerged branch "feat" that is not checked out, so `git branch -d`
	// fails but `git branch -D` succeeds.
	mustGit(t, dir, "checkout", "-b", "feat")
	writeFile(t, dir, "feat.txt", "feature\n")
	mustGit(t, dir, "add", "feat.txt")
	mustGit(t, dir, "commit", "-m", "feat work")
	mustGit(t, dir, "checkout", base)

	m = inWorktreesMode(m)
	m, _ = update(m, m.Init()()) // load real worktrees

	// Simulate the "feat" worktree having been removed → "Also delete branch?".
	m, _ = update(m, model.WorktreeRemovedMsg{RepoPath: dir, BranchName: "feat"})
	if m.Overlay() != ui.OverlayConfirm {
		t.Fatalf("expected branch confirm overlay, got %d", m.Overlay())
	}

	// Confirm branch deletion → `git branch -d feat` fails (unmerged) → DeleteFailedMsg.
	_, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected branch delete cmd, got nil")
	}
	deleteFailed, ok := cmd().(model.DeleteFailedMsg)
	if !ok {
		t.Fatalf("expected DeleteFailedMsg for unmerged branch, got %T", cmd())
	}
	m, _ = update(m, deleteFailed)
	if !m.ConfirmForce() {
		t.Fatal("expected force-delete confirm to be shown")
	}

	// Confirm force delete → `git branch -D feat` succeeds → WorktreeDeleteCompletedMsg.
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected force delete cmd, got nil")
	}
	if _, ok := cmd().(model.WorktreeDeleteCompletedMsg); !ok {
		t.Fatalf("expected WorktreeDeleteCompletedMsg after force delete, got %T", cmd())
	}
	if out := gitOut(t, dir, "branch", "--list", "feat"); out != "" {
		t.Errorf("expected feat branch to be deleted, got %q", out)
	}
}
