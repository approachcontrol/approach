package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
)

func TestHyperlink(t *testing.T) {
	tests := []struct {
		name   string
		label  string
		target string
		want   string
	}{
		{name: "empty label", target: "https://example.com"},
		{name: "empty target", label: "plain", want: "plain"},
		{name: "unicode label", label: "PR 界", target: "https://example.com/pull/8", want: ansi.SetHyperlink("https://example.com/pull/8") + "PR 界" + ansi.ResetHyperlink()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hyperlink(tt.label, tt.target); got != tt.want {
				t.Fatalf("hyperlink(%q, %q) = %q, want %q", tt.label, tt.target, got, tt.want)
			}
		})
	}
}

func TestHyperlinkTargets(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "trusted PR", got: prHyperlinkTarget("https://github.com/approachcontrol/approach/pull/8?view=files#diff-a"), want: "https://github.com/approachcontrol/approach/pull/8?view=files#diff-a"},
		{name: "reject non-HTTP PR", got: prHyperlinkTarget("javascript:alert(1)")},
		{name: "reject relative PR", got: prHyperlinkTarget("github.com/approach/pull/8")},
		{name: "absolute file", got: fileHyperlinkTarget("/tmp/space # percent%/界"), want: "file:///tmp/space%20%23%20percent%25/%E7%95%8C"},
		{name: "reject relative file", got: fileHyperlinkTarget("relative/path")},
		{name: "repo-qualified Bead", got: beadHyperlinkTarget("/tmp/repo #1", "approach-5e7/child"), want: "bead://open?id=approach-5e7%2Fchild&repo=%2Ftmp%2Frepo+%231"},
		{name: "missing Bead repo", got: beadHyperlinkTarget("", "approach-5e7")},
		{name: "missing Bead ID", got: beadHyperlinkTarget("/tmp/repo", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("target = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestHyperlinkClosesBeforeAdjacentTextAndHasZeroWidth(t *testing.T) {
	label := "PR 界"
	linked := hyperlink(label, "https://example.com/pull/8")
	row := linked + "  next"
	if !strings.Contains(row, label+ansi.ResetHyperlink()+"  next") {
		t.Fatalf("link does not close before adjacent text: %q", row)
	}
	if got := ansi.Strip(row); got != label+"  next" {
		t.Fatalf("stripped row = %q", got)
	}
	if got, want := ansi.StringWidth(row), ansi.StringWidth(label+"  next"); got != want {
		t.Fatalf("row width = %d, want %d", got, want)
	}
}

func TestHyperlinkColumnClosesBeforePadding(t *testing.T) {
	const target = "https://example.com/pull/8"
	cell := hyperlinkColumn("#8", target, 8, lipgloss.NewStyle())
	if !strings.Contains(cell, "#8"+ansi.ResetHyperlink()+strings.Repeat(" ", 6)) {
		t.Fatalf("column link includes padding: %q", cell)
	}
	if got := ansi.StringWidth(cell); got != 8 {
		t.Fatalf("column width = %d, want 8", got)
	}
}

func TestRenderFlowHyperlinksPreserveTextAndWidth(t *testing.T) {
	const (
		repoPath     = "/tmp/approach repo"
		worktreePath = "/tmp/approach worktrees/feature"
		prURL        = "https://github.com/approachcontrol/approach/pull/88"
	)
	record := flowstore.FlowRecord{
		FlowID: "flow-1", Title: "Linked Flow", Status: flowstore.StatusInProgress,
		RepoPath: repoPath, WorktreePath: worktreePath, Branch: "feature/links",
		PR: flowstore.PullRequest{Number: 88, URL: prURL},
	}
	for _, selected := range []int{-1, 0} {
		rows := renderFlowPane([]flowstore.FlowRecord{record}, selected, 0, 240, 3, "", "", nil, true, map[string]string{repoPath: "approach"})
		row := rows[1]
		for _, target := range []string{fileHyperlinkTarget(repoPath), fileHyperlinkTarget(worktreePath), prURL} {
			if !strings.Contains(row, ansi.SetHyperlink(target)) {
				t.Fatalf("selected=%d row missing target %q: %q", selected, target, row)
			}
		}
		plain := ansi.Strip(row)
		for _, label := range []string{"approach", "feature/links", "#88", "Linked Flow"} {
			if !strings.Contains(plain, label) {
				t.Fatalf("selected=%d stripped row missing %q: %q", selected, label, plain)
			}
		}
		if got := ansi.StringWidth(row); got > 240 {
			t.Fatalf("selected=%d width = %d, want <= 240", selected, got)
		}
	}

	linked := renderFlowPane([]flowstore.FlowRecord{record}, -1, 0, 108, 3, "", "", nil, false, nil)[1]
	baseline := record
	baseline.PR.URL = ""
	plain := renderFlowPane([]flowstore.FlowRecord{baseline}, -1, 0, 108, 3, "", "", nil, false, nil)[1]
	if got, want := ansi.Strip(linked), ansi.Strip(plain); got != want {
		t.Fatalf("truncation changed visible text: got %q want %q", got, want)
	}
	if ansi.StringWidth(linked) != ansi.StringWidth(plain) || ansi.StringWidth(linked) > 108 {
		t.Fatalf("truncated linked width = %d, plain width = %d", ansi.StringWidth(linked), ansi.StringWidth(plain))
	}
	start := strings.Index(linked, ansi.SetHyperlink(prURL))
	if start < 0 || !strings.Contains(linked[start:], ansi.ResetHyperlink()) {
		t.Fatalf("truncated PR link is not explicitly closed: %q", linked)
	}
}

func TestRenderAuthoritativePathAndBeadTargets(t *testing.T) {
	const repoPath = "/tmp/repo with space"
	assertTarget := func(name, row, target string) {
		t.Helper()
		if !strings.Contains(row, ansi.SetHyperlink(target)) || !strings.Contains(row, ansi.ResetHyperlink()) {
			t.Fatalf("%s row missing closed target %q: %q", name, target, row)
		}
	}

	repoRow := renderRepoList([]scanner.Repo{{Path: repoPath, DisplayName: "repo"}}, 0, 0, 40, 1, "", nil)[0]
	assertTarget("repo", repoRow, fileHyperlinkTarget(repoPath))

	worktreePath := repoPath + "-worktrees/feature"
	worktreeRow := renderWorktreePane([]gitquery.Worktree{{Path: worktreePath, BranchName: "feature"}}, 0, 0, 80, 1)[0]
	assertTarget("worktree", worktreeRow, fileHyperlinkTarget(worktreePath))

	branchRow := renderBranchPaneSelected([]gitquery.BranchRow{{Branch: gitquery.Branch{Name: "feature", IsWorktree: true}, WorktreePath: worktreePath}}, 0, 0, 80, 1, repoPath)[0]
	assertTarget("branch", branchRow, fileHyperlinkTarget(worktreePath))

	sessionRows := renderSessionPane([]sessions.SessionRecord{{Provider: sessions.ProviderCodex, Branch: "feature", WorktreePath: worktreePath, Status: "ended"}}, 0, 0, 100, 2)
	assertTarget("session", sessionRows[1], fileHyperlinkTarget(worktreePath))

	expansion := BeadExpansion{EpicID: "approach-epic", State: BeadExpansionLoaded, ReadinessKnown: true, Children: []beadsquery.Bead{{ID: "approach-child", Title: "Child"}}}
	beadRows := renderBeadsPane([]beadsquery.Bead{{ID: "approach-epic", Priority: 2, Title: "Epic", IssueType: "epic"}}, 0, 0, 80, 3, expansion, repoPath)
	assertTarget("top-level Bead", beadRows[0], beadHyperlinkTarget(repoPath, "approach-epic"))
	assertTarget("child Bead", beadRows[1], beadHyperlinkTarget(repoPath, "approach-child"))
	if !strings.Contains(beadRows[0], ansi.ResetHyperlink()+"  P2") {
		t.Fatalf("top-level Bead link includes adjacent fields: %q", beadRows[0])
	}
	narrowBead := renderBeadsPane([]beadsquery.Bead{{ID: "approach-epic", Title: "Epic"}}, -1, 0, 10, 1, BeadExpansion{}, repoPath)[0]
	if !strings.Contains(narrowBead, ansi.ResetHyperlink()) || ansi.StringWidth(narrowBead) > 10 {
		t.Fatalf("truncated Bead link is not closed within width: %q", narrowBead)
	}
}

func TestRenderPRBabysitterLinksPRAndBeadTargets(t *testing.T) {
	const (
		repoPath = "/tmp/approach"
		prURL    = "https://github.com/approachcontrol/approach/pull/88"
	)
	row := PRBabysitterRow{
		Flow: flowstore.FlowRecord{FlowID: "flow-1", RepoPath: repoPath, PR: flowstore.PullRequest{Number: 88, URL: prURL}},
		Repo: "approach", Title: "PR #88", BeadID: "approach-5e7", Mergeability: "mergeable", Checks: "passing",
	}
	for _, selected := range []int{-1, 0} {
		line := renderPRBabysitterPane([]PRBabysitterRow{row}, selected, 0, 120, 3, "", "", nil)[1]
		for _, target := range []string{fileHyperlinkTarget(repoPath), prURL, beadHyperlinkTarget(repoPath, row.BeadID)} {
			if !strings.Contains(line, ansi.SetHyperlink(target)) {
				t.Fatalf("selected=%d row missing target %q: %q", selected, target, line)
			}
		}
		if got := ansi.Strip(line); !strings.Contains(got, "PR #88") || !strings.Contains(got, row.BeadID) {
			t.Fatalf("selected=%d visible text changed: %q", selected, got)
		}
	}
}

func TestEmbeddedTerminalFramePreservesClosedHyperlinkAtPaneEdge(t *testing.T) {
	const target = "https://example.com/pull/8"
	linked := hyperlink("abcdefgh", target)
	for _, width := range []int{20, 8} {
		lines := renderEmbeddedTerminalPane([]EmbeddedTerminalTab{{Number: 1, Provider: "sh", Active: true}}, []string{linked}, false, false, width, 4)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, ansi.SetHyperlink(target)) || !strings.Contains(joined, ansi.ResetHyperlink()) {
			t.Fatalf("width %d frame dropped or leaked hyperlink: %q", width, joined)
		}
		for _, line := range lines {
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("width %d framed line width = %d: %q", width, got, line)
			}
		}
	}
}
