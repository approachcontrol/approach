package model_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
