package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/ui"
)

type partialGitRunner struct {
	worktreePath string
	wantErr      error
}

func (r partialGitRunner) Run(dir string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch joined {
	case "worktree list --porcelain":
		return "worktree " + r.worktreePath + "\nHEAD abc123\nbranch refs/heads/main\n", nil
	case "status --porcelain":
		return "", r.wantErr
	default:
		return "", errors.New("unexpected Git query: " + joined)
	}
}

func (partialGitRunner) Predicate(string, ...string) (bool, error) { return false, nil }

func TestWorktreePartialQueryWarningTracksCurrentResultAndClearsOnCleanRefresh(t *testing.T) {
	m := newModelForTest([]scanner.Repo{{Path: "/repo", DisplayName: "repo"}}, Options{})
	request := m.currentListRequest(ui.ModeWorktrees)
	partial := &gitquery.PartialQueryError{Warnings: []gitquery.QueryWarning{{
		Operation: "dirty status",
		Subject:   "/repo/worktree",
		Cause:     errors.New("cannot read index"),
	}}}

	nextModel, _ := m.Update(WorktreeResultMsg{
		RepoPath:    "/repo",
		Worktrees:   []gitquery.Worktree{{Path: "/repo/worktree"}},
		ListRequest: request,
		Degradation: partial,
	})
	next := nextModel.(Model)
	if warning := next.gitDegradationWarning(ui.ModeWorktrees); !strings.Contains(warning, "cannot read index") {
		t.Fatalf("warning = %q, want partial Git diagnostic", warning)
	}

	cleanModel, _ := next.Update(WorktreeResultMsg{
		RepoPath:    "/repo",
		Worktrees:   []gitquery.Worktree{{Path: "/repo/worktree"}},
		ListRequest: request,
	})
	clean := cleanModel.(Model)
	if warning := clean.gitDegradationWarning(ui.ModeWorktrees); warning != "" {
		t.Fatalf("clean refresh retained warning %q", warning)
	}
}

func TestWorktreeFetchCarriesPartialQueryRowsInsteadOfFailing(t *testing.T) {
	oldRunner := gitquery.DefaultRunner
	t.Cleanup(func() { gitquery.DefaultRunner = oldRunner })
	wantErr := errors.New("cannot read index")
	gitquery.DefaultRunner = partialGitRunner{worktreePath: t.TempDir(), wantErr: wantErr}
	descriptor, ok := listFetchDescriptorForMode(ui.ModeWorktrees)
	if !ok {
		t.Fatal("worktree fetch descriptor unavailable")
	}

	message, err := descriptor.load(Model{}, "/repo", 7)
	if err != nil {
		t.Fatalf("load() error = %v, want usable partial result", err)
	}
	result, ok := message.(WorktreeResultMsg)
	if !ok || len(result.Worktrees) != 1 || result.Degradation == nil || !errors.Is(result.Degradation, wantErr) {
		t.Fatalf("load() message = %#v, want worktree row plus diagnostic", message)
	}
}
