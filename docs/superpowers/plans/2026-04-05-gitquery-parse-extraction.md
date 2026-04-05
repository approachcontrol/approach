# Gitquery Parse Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract pure parsing functions from `gitquery/gitquery.go` into `gitquery/parse.go` so parsing logic can be unit-tested with string literals instead of real git repos.

**Architecture:** Each public `List*` function in `gitquery.go` currently runs a git command and parses the output inline. We extract each parsing step into an exported function in `parse.go`, write unit tests first (TDD), then wire the existing functions to call the new parsers. Existing integration tests remain unchanged as a safety net.

**Tech Stack:** Go, standard library only

---

## File Structure

- **Create:** `gitquery/parse.go` — all exported pure parsing functions and the `WorktreeInfo` type
- **Create:** `gitquery/parse_test.go` — unit tests using string literals (`package gitquery_test`)
- **Modify:** `gitquery/gitquery.go` — replace inline parsing with calls to new functions, remove old private helpers

---

### Task 1: ParseCommitLog

Extract commit log parsing from `ListCommits` (gitquery.go:165-190).

**Files:**
- Create: `gitquery/parse.go`
- Create: `gitquery/parse_test.go`

- [ ] **Step 1: Write the failing test**

Create `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseCommitLog -v`
Expected: FAIL — `ParseCommitLog` not defined

- [ ] **Step 3: Write minimal implementation**

Create `gitquery/parse.go`:

```go
package gitquery

import "strings"

// ParseCommitLog parses the output of git log --format=%h%x00%an%x00%ar%x00%s.
func ParseCommitLog(text string) []Commit {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var commits []Commit
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) != 4 {
			continue
		}
		commits = append(commits, Commit{
			Hash:    parts[0],
			Author:  parts[1],
			Date:    parts[2],
			Subject: parts[3],
		})
	}
	return commits
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseCommitLog -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Wire ListCommits to use ParseCommitLog**

In `gitquery/gitquery.go`, replace the body of `ListCommits` (lines 165-190) with:

```go
func ListCommits(repoPath string) ([]Commit, error) {
	text, err := gitCmd(repoPath, "log", "--format=%h%x00%an%x00%ar%x00%s", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}
	return ParseCommitLog(text), nil
}
```

- [ ] **Step 6: Run all tests to verify nothing broke**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS (existing integration tests + new unit tests)

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseCommitLog from ListCommits"
```

---

### Task 2: ParseReflog

Extract reflog parsing from `ListReflog` (gitquery.go:193-218). Nearly identical structure to ParseCommitLog.

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseReflog -v`
Expected: FAIL — `ParseReflog` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// ParseReflog parses the output of git reflog --format=%h%x00%gd%x00%ar%x00%gs.
func ParseReflog(text string) []ReflogEntry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var entries []ReflogEntry
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\x00", 4)
		if len(parts) != 4 {
			continue
		}
		entries = append(entries, ReflogEntry{
			Hash:     parts[0],
			Selector: parts[1],
			Date:     parts[2],
			Subject:  parts[3],
		})
	}
	return entries
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseReflog -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Wire ListReflog to use ParseReflog**

In `gitquery/gitquery.go`, replace the body of `ListReflog` (lines 193-218) with:

```go
func ListReflog(repoPath string) ([]ReflogEntry, error) {
	text, err := gitCmd(repoPath, "reflog", "--format=%h%x00%gd%x00%ar%x00%gs", "-n", "50")
	if err != nil {
		return nil, fmt.Errorf("listing reflog: %w", err)
	}
	return ParseReflog(text), nil
}
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseReflog from ListReflog"
```

---

### Task 3: ParseStashList

Extract stash parsing from `ListStashes` (gitquery.go:244-272). Includes `stash@{N}` index extraction.

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseStashList -v`
Expected: FAIL — `ParseStashList` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// ParseStashList parses the output of git stash list --format=%gd%x00%ai%x00%s.
func ParseStashList(text string) []Stash {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var stashes []Stash
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		idxStr := strings.TrimPrefix(parts[0], "stash@{")
		idxStr = strings.TrimSuffix(idxStr, "}")
		idx, _ := strconv.Atoi(idxStr)
		stashes = append(stashes, Stash{
			Index:   idx,
			Date:    parts[1],
			Message: parts[2],
		})
	}
	return stashes
}
```

Note: Add `"strconv"` to the imports in `parse.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseStashList -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Wire ListStashes to use ParseStashList**

In `gitquery/gitquery.go`, replace the body of `ListStashes` (lines 244-272) with:

```go
func ListStashes(repoPath string) ([]Stash, error) {
	text, err := gitCmd(repoPath, "stash", "list", "--format=%gd%x00%ai%x00%s")
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %w", err)
	}
	return ParseStashList(text), nil
}
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseStashList from ListStashes"
```

---

### Task 4: ParseNumstat

Extract numstat parsing from `populateDirtyStatus` (gitquery.go:476-489) and `populateWorktreeDirtyStatus` (gitquery.go:152-161). This eliminates duplication between the two functions.

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseNumstat -v`
Expected: FAIL — `ParseNumstat` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// ParseNumstat parses the output of git diff --numstat.
// Returns total lines added and deleted. Binary files (shown as - -) contribute 0.
func ParseNumstat(text string) (int, int) {
	var added, deleted int
	for _, line := range splitLines(text) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		a, _ := strconv.Atoi(fields[0])
		d, _ := strconv.Atoi(fields[1])
		added += a
		deleted += d
	}
	return added, deleted
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseNumstat -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Wire populateDirtyStatus and populateWorktreeDirtyStatus**

In `gitquery/gitquery.go`, replace the numstat loop in `populateWorktreeDirtyStatus` (lines 148-161) with:

```go
	diffOut, err := gitCmd(wt.Path, "diff", "HEAD", "--numstat")
	if err != nil {
		return
	}
	wt.LinesAdded, wt.LinesDeleted = ParseNumstat(diffOut)
```

Replace the numstat loop in `populateDirtyStatus` (lines 476-492) with:

```go
		diffOut, err := gitCmd(path, "diff", "HEAD", "--numstat")
		if err != nil {
			continue
		}
		a, d := ParseNumstat(diffOut)
		b.LinesAdded += a
		b.LinesDeleted += d
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseNumstat, deduplicate dirty status parsing"
```

---

### Task 5: ParseAheadBehind

Extract ahead/behind parsing from `branchAheadBehind` (gitquery.go:439-453).

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseAheadBehind -v`
Expected: FAIL — `ParseAheadBehind` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// ParseAheadBehind parses the output of git rev-list --count --left-right.
func ParseAheadBehind(text string) (int, int) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) != 2 {
		return 0, 0
	}
	ahead, _ := strconv.Atoi(parts[0])
	behind, _ := strconv.Atoi(parts[1])
	return ahead, behind
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseAheadBehind -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Wire branchAheadBehind to use ParseAheadBehind**

In `gitquery/gitquery.go`, replace the body of `branchAheadBehind` (lines 439-453) with:

```go
func branchAheadBehind(repoPath, branchName, upstream string) (int, int, error) {
	out, err := gitCmd(repoPath, "rev-list", "--count", "--left-right", branchName+"..."+upstream)
	if err != nil {
		return 0, 0, err
	}
	ahead, behind := ParseAheadBehind(out)
	return ahead, behind, nil
}
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseAheadBehind from branchAheadBehind"
```

---

### Task 6: ParseBranchLine

Promote existing `parseBranchLine` (gitquery.go:423-437) to exported `ParseBranchLine`.

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseBranchLine -v`
Expected: FAIL — `ParseBranchLine` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// ParseBranchLine parses one line of git for-each-ref --format=%(refname:short)\t%(upstream)\t%(upstream:track).
// Returns the branch (with Name, HasUpstream, UpstreamGone populated) and the upstream ref string.
func ParseBranchLine(line string) (Branch, string) {
	parts := strings.SplitN(line, "\t", 3)
	b := Branch{Name: parts[0]}

	var upstream string
	if len(parts) > 1 && parts[1] != "" {
		b.HasUpstream = true
		upstream = parts[1]
		if len(parts) > 2 && strings.Contains(parts[2], "gone") {
			b.UpstreamGone = true
		}
	}

	return b, upstream
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseBranchLine -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Remove parseBranchLine, update caller**

In `gitquery/gitquery.go`:
- Delete the `parseBranchLine` function (lines 423-437)
- In `ListBranches` (line 301), change `parseBranchLine(line)` to `ParseBranchLine(line)`

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Promote parseBranchLine to exported ParseBranchLine"
```

---

### Task 7: ParseWorktreeList and WorktreeInfo

Combine `splitWorktreeBlocks` + `parseWorktreeBlock` into exported `ParseWorktreeList`. Export `WorktreeInfo` type.

**Files:**
- Modify: `gitquery/parse.go`
- Modify: `gitquery/parse_test.go`
- Modify: `gitquery/gitquery.go`

- [ ] **Step 1: Write the failing test**

Append to `gitquery/parse_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseWorktreeList -v`
Expected: FAIL — `ParseWorktreeList` not defined

- [ ] **Step 3: Write minimal implementation**

Append to `gitquery/parse.go`:

```go
// WorktreeInfo holds data parsed from one block of git worktree list --porcelain output.
type WorktreeInfo struct {
	Path     string
	Branch   string
	IsBare   bool
	Detached bool
}

// ParseWorktreeList parses the full output of git worktree list --porcelain
// into a slice of WorktreeInfo entries.
func ParseWorktreeList(output string) []WorktreeInfo {
	output = strings.TrimRight(output, "\n")
	if strings.TrimSpace(output) == "" {
		return nil
	}

	var result []WorktreeInfo
	var current []string
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			if len(current) > 0 {
				result = append(result, parseOneWorktreeBlock(strings.Join(current, "\n")))
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		result = append(result, parseOneWorktreeBlock(strings.Join(current, "\n")))
	}
	return result
}

func parseOneWorktreeBlock(block string) WorktreeInfo {
	var wt WorktreeInfo
	for _, line := range strings.Split(block, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wt.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			wt.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			wt.IsBare = true
		case line == "detached":
			wt.Detached = true
			wt.Branch = "(detached)"
		}
	}
	return wt
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -run TestParseWorktreeList -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Wire ListWorktrees and branchWorktreeMap, remove old helpers**

In `gitquery/gitquery.go`:

Replace the worktree parsing in `ListWorktrees` (lines 98-118) — change the loop that uses `splitWorktreeBlocks`/`parseWorktreeBlock` to use `ParseWorktreeList`:

```go
func ListWorktrees(repoPath string) ([]Worktree, error) {
	out, err := gitCmd(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	first := true
	for _, wt := range ParseWorktreeList(out) {
		if wt.IsBare {
			continue
		}

		w := Worktree{
			Path:     wt.Path,
			Detached: wt.Detached,
			IsMain:   first,
		}
		if wt.Detached {
			w.BranchName = ""
		} else {
			w.BranchName = wt.Branch
		}
		first = false
		worktrees = append(worktrees, w)
	}

	// Batch stale detection for all worktrees
	paths := make([]string, len(worktrees))
	for i := range worktrees {
		paths[i] = worktrees[i].Path
	}
	staleFlags := checkStale(paths)
	for i := range worktrees {
		worktrees[i].Stale = staleFlags[i]
		if !worktrees[i].Stale {
			populateWorktreeDirtyStatus(&worktrees[i])
		}
	}

	return worktrees, nil
}
```

Replace the worktree parsing in `branchWorktreeMap` (lines 399-421):

```go
func branchWorktreeMap(repoPath string) (map[string][]string, []string, error) {
	out, err := gitCmd(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, err
	}

	m := make(map[string][]string)
	var detachedPaths []string
	for _, wt := range ParseWorktreeList(out) {
		if wt.IsBare {
			continue
		}
		if wt.Detached {
			detachedPaths = append(detachedPaths, wt.Path)
			continue
		}
		if wt.Branch != "" {
			m[wt.Branch] = append(m[wt.Branch], wt.Path)
		}
	}
	return m, detachedPaths, nil
}
```

Delete the old `worktreeInfo` type (lines 354-359), `splitWorktreeBlocks` (lines 361-378), and `parseWorktreeBlock` (lines 380-396).

- [ ] **Step 6: Run all tests**

Run: `cd /Users/brian/dev/wtui && go test ./gitquery/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add gitquery/parse.go gitquery/parse_test.go gitquery/gitquery.go
git commit -m "Extract ParseWorktreeList, export WorktreeInfo type"
```

---

### Task 8: Final cleanup and format check

Verify no dead code remains, formatting is clean, and all tests pass.

**Files:**
- Modify: `gitquery/gitquery.go` (if needed)

- [ ] **Step 1: Run gofmt**

Run: `cd /Users/brian/dev/wtui && gofmt -l .`
Expected: no output (all files formatted)

If any files listed, run: `gofmt -w gitquery/parse.go gitquery/gitquery.go`

- [ ] **Step 2: Run full test suite**

Run: `cd /Users/brian/dev/wtui && make test`
Expected: All PASS

- [ ] **Step 3: Verify no dead code**

Check that `gitquery.go` no longer contains:
- `worktreeInfo` type
- `splitWorktreeBlocks` function
- `parseWorktreeBlock` function
- `parseBranchLine` function
- Inline numstat parsing loops (in `populateDirtyStatus` and `populateWorktreeDirtyStatus`)
- Inline commit/reflog/stash parsing loops

Run: `cd /Users/brian/dev/wtui && grep -n 'func splitWorktreeBlocks\|func parseWorktreeBlock\|func parseBranchLine\|type worktreeInfo' gitquery/gitquery.go`
Expected: no output

- [ ] **Step 4: Commit if any formatting fixes were needed**

```bash
git add -A
git commit -m "Format and clean up after parse extraction"
```

Only commit if there are changes. Skip if clean.
