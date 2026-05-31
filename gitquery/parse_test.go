package gitquery_test

import (
	"testing"

	"github.com/brian-bell/wtui/gitquery"
)

func TestParseCommitLog_ParsesMultipleCommits(t *testing.T) {
	input := "abc1234\x00Alice\x002 hours ago\x00Add feature\nabc5678\x00Bob\x003 days ago\x00Fix bug\n"

	commits := gitquery.ParseCommitLog(input)

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].Hash != "abc1234" {
		t.Errorf("expected Hash %q, got %q", "abc1234", commits[0].Hash)
	}
	if commits[0].Author != "Alice" {
		t.Errorf("expected Author %q, got %q", "Alice", commits[0].Author)
	}
	if commits[0].Date != "2 hours ago" {
		t.Errorf("expected Date %q, got %q", "2 hours ago", commits[0].Date)
	}
	if commits[0].Subject != "Add feature" {
		t.Errorf("expected Subject %q, got %q", "Add feature", commits[0].Subject)
	}
	if commits[1].Hash != "abc5678" {
		t.Errorf("expected Hash %q, got %q", "abc5678", commits[1].Hash)
	}
}

func TestParseCommitLog_EmptyInput(t *testing.T) {
	if commits := gitquery.ParseCommitLog(""); commits != nil {
		t.Errorf("expected nil, got %v", commits)
	}
	if commits := gitquery.ParseCommitLog("  \n"); commits != nil {
		t.Errorf("expected nil for whitespace, got %v", commits)
	}
}

func TestParseCommitLog_MalformedLineSkipped(t *testing.T) {
	input := "abc1234\x00Alice\x002 hours ago\x00Add feature\ngarbage line\nabc5678\x00Bob\x003 days ago\x00Fix bug\n"

	commits := gitquery.ParseCommitLog(input)

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits (malformed skipped), got %d", len(commits))
	}
}

func TestParseReflog_ParsesMultipleEntries(t *testing.T) {
	input := "abc1234\x00HEAD@{0}\x002 hours ago\x00commit: Add feature\nabc5678\x00HEAD@{1}\x003 days ago\x00checkout: moving from main to feat\n"

	entries := gitquery.ParseReflog(input)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hash != "abc1234" {
		t.Errorf("expected Hash %q, got %q", "abc1234", entries[0].Hash)
	}
	if entries[0].Selector != "HEAD@{0}" {
		t.Errorf("expected Selector %q, got %q", "HEAD@{0}", entries[0].Selector)
	}
	if entries[0].Date != "2 hours ago" {
		t.Errorf("expected Date %q, got %q", "2 hours ago", entries[0].Date)
	}
	if entries[0].Subject != "commit: Add feature" {
		t.Errorf("expected Subject %q, got %q", "commit: Add feature", entries[0].Subject)
	}
}

func TestParseReflog_EmptyInput(t *testing.T) {
	if entries := gitquery.ParseReflog(""); entries != nil {
		t.Errorf("expected nil, got %v", entries)
	}
}

func TestParseReflog_MalformedLineSkipped(t *testing.T) {
	input := "abc1234\x00HEAD@{0}\x002 hours ago\x00commit: Add feature\nbadline\n"

	entries := gitquery.ParseReflog(input)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (malformed skipped), got %d", len(entries))
	}
}

func TestParseStashList_ParsesMultipleStashes(t *testing.T) {
	input := "stash@{0}\x002024-01-15 10:30:00 -0500\x00WIP on main: abc1234 some work\nstash@{1}\x002024-01-14 09:00:00 -0500\x00On main: save progress\n"

	stashes := gitquery.ParseStashList(input)

	if len(stashes) != 2 {
		t.Fatalf("expected 2 stashes, got %d", len(stashes))
	}
	if stashes[0].Index != 0 {
		t.Errorf("expected Index 0, got %d", stashes[0].Index)
	}
	if stashes[0].Message != "WIP on main: abc1234 some work" {
		t.Errorf("expected Message %q, got %q", "WIP on main: abc1234 some work", stashes[0].Message)
	}
	if stashes[1].Index != 1 {
		t.Errorf("expected Index 1, got %d", stashes[1].Index)
	}
	if stashes[1].Date != "2024-01-14 09:00:00 -0500" {
		t.Errorf("expected Date %q, got %q", "2024-01-14 09:00:00 -0500", stashes[1].Date)
	}
}

func TestParseStashList_EmptyInput(t *testing.T) {
	if stashes := gitquery.ParseStashList(""); stashes != nil {
		t.Errorf("expected nil, got %v", stashes)
	}
}

func TestParseStashList_MalformedLineSkipped(t *testing.T) {
	input := "stash@{0}\x002024-01-15 10:30:00 -0500\x00WIP\nbroken\n"

	stashes := gitquery.ParseStashList(input)

	if len(stashes) != 1 {
		t.Fatalf("expected 1 stash (malformed skipped), got %d", len(stashes))
	}
}

func TestParseNumstat_ParsesAddedDeleted(t *testing.T) {
	input := "3\t1\tfile.go\n10\t5\tother.go\n"

	added, deleted := gitquery.ParseNumstat(input)

	if added != 13 {
		t.Errorf("expected added 13, got %d", added)
	}
	if deleted != 6 {
		t.Errorf("expected deleted 6, got %d", deleted)
	}
}

func TestParseNumstat_EmptyInput(t *testing.T) {
	added, deleted := gitquery.ParseNumstat("")
	if added != 0 || deleted != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", added, deleted)
	}
}

func TestParseNumstat_BinaryFilesIgnored(t *testing.T) {
	input := "3\t1\ttext.go\n-\t-\tbinary.png\n"

	added, deleted := gitquery.ParseNumstat(input)

	if added != 3 {
		t.Errorf("expected added 3, got %d", added)
	}
	if deleted != 1 {
		t.Errorf("expected deleted 1, got %d", deleted)
	}
}

func TestParseNumstat_WhitespaceOnlyInput(t *testing.T) {
	added, deleted := gitquery.ParseNumstat("  \n  \n")
	if added != 0 || deleted != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", added, deleted)
	}
}

func TestParseNumstat_MalformedLineSkipped(t *testing.T) {
	input := "3\t1\tfile.go\nbadline\n2\t0\tother.go\n"

	added, deleted := gitquery.ParseNumstat(input)

	if added != 5 {
		t.Errorf("expected added 5, got %d", added)
	}
	if deleted != 1 {
		t.Errorf("expected deleted 1, got %d", deleted)
	}
}

func TestParseAheadBehind_ParsesCounts(t *testing.T) {
	ahead, behind := gitquery.ParseAheadBehind("3\t2\n")
	if ahead != 3 {
		t.Errorf("expected ahead 3, got %d", ahead)
	}
	if behind != 2 {
		t.Errorf("expected behind 2, got %d", behind)
	}
}

func TestParseAheadBehind_ZeroCounts(t *testing.T) {
	ahead, behind := gitquery.ParseAheadBehind("0\t0\n")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", ahead, behind)
	}
}

func TestParseAheadBehind_EmptyInput(t *testing.T) {
	ahead, behind := gitquery.ParseAheadBehind("")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", ahead, behind)
	}
}

func TestParseAheadBehind_MalformedInput(t *testing.T) {
	ahead, behind := gitquery.ParseAheadBehind("notanumber")
	if ahead != 0 || behind != 0 {
		t.Errorf("expected (0, 0), got (%d, %d)", ahead, behind)
	}
}

func TestParseBranchLine_WithUpstream(t *testing.T) {
	line := "feature\trefs/remotes/origin/feature\t"

	b, upstream := gitquery.ParseBranchLine(line)

	if b.Name != "feature" {
		t.Errorf("expected Name %q, got %q", "feature", b.Name)
	}
	if !b.HasUpstream {
		t.Error("expected HasUpstream = true")
	}
	if b.UpstreamGone {
		t.Error("expected UpstreamGone = false")
	}
	if upstream != "refs/remotes/origin/feature" {
		t.Errorf("expected upstream %q, got %q", "refs/remotes/origin/feature", upstream)
	}
}

func TestParseBranchLine_UpstreamGone(t *testing.T) {
	line := "old-feature\trefs/remotes/origin/old-feature\t[gone]"

	b, _ := gitquery.ParseBranchLine(line)

	if !b.HasUpstream {
		t.Error("expected HasUpstream = true")
	}
	if !b.UpstreamGone {
		t.Error("expected UpstreamGone = true")
	}
}

func TestParseBranchLine_NoUpstream(t *testing.T) {
	line := "local-only\t\t"

	b, upstream := gitquery.ParseBranchLine(line)

	if b.Name != "local-only" {
		t.Errorf("expected Name %q, got %q", "local-only", b.Name)
	}
	if b.HasUpstream {
		t.Error("expected HasUpstream = false")
	}
	if upstream != "" {
		t.Errorf("expected empty upstream, got %q", upstream)
	}
}

func TestParseBranchLine_EmptyInput(t *testing.T) {
	b, upstream := gitquery.ParseBranchLine("")

	if b.Name != "" {
		t.Errorf("expected empty Name, got %q", b.Name)
	}
	if b.HasUpstream {
		t.Error("expected HasUpstream = false")
	}
	if upstream != "" {
		t.Errorf("expected empty upstream, got %q", upstream)
	}
}

func TestParseBranchLine_NameOnly(t *testing.T) {
	line := "main"

	b, _ := gitquery.ParseBranchLine(line)

	if b.Name != "main" {
		t.Errorf("expected Name %q, got %q", "main", b.Name)
	}
	if b.HasUpstream {
		t.Error("expected HasUpstream = false")
	}
}

func TestParseWorktreeList_ParsesMultipleWorktrees(t *testing.T) {
	input := "worktree /home/user/project\nbranch refs/heads/main\n\nworktree /home/user/project-feature\nbranch refs/heads/feature\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(infos))
	}
	if infos[0].Path != "/home/user/project" {
		t.Errorf("expected Path %q, got %q", "/home/user/project", infos[0].Path)
	}
	if infos[0].Branch != "main" {
		t.Errorf("expected Branch %q, got %q", "main", infos[0].Branch)
	}
	if infos[0].IsBare || infos[0].Detached {
		t.Error("expected IsBare=false, Detached=false")
	}
	if infos[1].Path != "/home/user/project-feature" {
		t.Errorf("expected Path %q, got %q", "/home/user/project-feature", infos[1].Path)
	}
	if infos[1].Branch != "feature" {
		t.Errorf("expected Branch %q, got %q", "feature", infos[1].Branch)
	}
}

func TestParseWorktreeList_BareWorktree(t *testing.T) {
	input := "worktree /home/user/project.git\nbare\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(infos))
	}
	if !infos[0].IsBare {
		t.Error("expected IsBare = true")
	}
}

func TestParseWorktreeList_DetachedWorktree(t *testing.T) {
	input := "worktree /home/user/project-detached\ndetached\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(infos))
	}
	if !infos[0].Detached {
		t.Error("expected Detached = true")
	}
	if infos[0].Branch != "(detached)" {
		t.Errorf("expected Branch %q, got %q", "(detached)", infos[0].Branch)
	}
}

func TestParseWorktreeList_BareLockedLine(t *testing.T) {
	input := "worktree /home/user/project\nbranch refs/heads/main\nlocked\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(infos))
	}
	if !infos[0].Locked {
		t.Error("expected Locked = true")
	}
	if infos[0].LockReason != "" {
		t.Errorf("expected empty LockReason, got %q", infos[0].LockReason)
	}
}

func TestParseWorktreeList_LockedLineWithReason(t *testing.T) {
	input := "worktree /home/user/project\nbranch refs/heads/main\nlocked on external drive\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(infos))
	}
	if !infos[0].Locked {
		t.Error("expected Locked = true")
	}
	if infos[0].LockReason != "on external drive" {
		t.Errorf("expected LockReason %q, got %q", "on external drive", infos[0].LockReason)
	}
}

func TestParseWorktreeList_EmptyInput(t *testing.T) {
	if infos := gitquery.ParseWorktreeList(""); infos != nil {
		t.Errorf("expected nil, got %v", infos)
	}
}

func TestParseWorktreeList_MixedTypes(t *testing.T) {
	input := "worktree /home/user/repo.git\nbare\n\nworktree /home/user/repo\nbranch refs/heads/main\n\nworktree /home/user/repo-detached\ndetached\n\n"

	infos := gitquery.ParseWorktreeList(input)

	if len(infos) != 3 {
		t.Fatalf("expected 3 worktrees, got %d", len(infos))
	}
	if !infos[0].IsBare {
		t.Error("expected first to be bare")
	}
	if infos[1].Branch != "main" {
		t.Errorf("expected second branch %q, got %q", "main", infos[1].Branch)
	}
	if !infos[2].Detached {
		t.Error("expected third to be detached")
	}
}
