# Gitquery Parse Extraction

Separate pure parsing functions from git command execution in the `gitquery/` package.

## Goal

Extract parsing logic that is currently interleaved with `exec.Command` calls into exported pure functions. This enables fast unit tests with string literals while keeping existing integration tests unchanged.

## New file: `gitquery/parse.go`

### Exported parsers

| Function | Extracted from | Input format |
|----------|---------------|-------------|
| `ParseCommitLog(text string) []Commit` | `ListCommits` | NUL-delimited 4-field lines (`hash\0author\0date\0subject`) |
| `ParseReflog(text string) []ReflogEntry` | `ListReflog` | NUL-delimited 4-field lines (`hash\0selector\0date\0subject`) |
| `ParseStashList(text string) []Stash` | `ListStashes` | NUL-delimited 3-field lines (`stash@{N}\0date\0message`) |
| `ParseNumstat(text string) (int, int)` | `populateDirtyStatus` (x2) | Tab-separated `added\tdeleted\tfilename` lines |
| `ParseAheadBehind(text string) (int, int)` | `branchAheadBehind` | Space-separated `ahead behind` |
| `ParseBranchLine(line string) (Branch, string)` | `parseBranchLine` | Tab-separated `name\tupstream\ttrack` |
| `ParseWorktreeList(output string) []WorktreeInfo` | `splitWorktreeBlocks` + `parseWorktreeBlock` | `git worktree list --porcelain` output |

### New exported type

```go
type WorktreeInfo struct {
    Path     string
    Branch   string
    IsBare   bool
    Detached bool
}
```

Replaces the unexported `worktreeInfo`.

## Changes to `gitquery.go`

- `ListCommits` calls `ParseCommitLog`
- `ListReflog` calls `ParseReflog`
- `ListStashes` calls `ParseStashList`
- `populateDirtyStatus` and `populateWorktreeDirtyStatus` call `ParseNumstat`
- `branchAheadBehind` calls `ParseAheadBehind`
- `ListWorktrees` and `branchWorktreeMap` call `ParseWorktreeList`
- Remove `splitWorktreeBlocks`, `parseWorktreeBlock`, `parseBranchLine`, `worktreeInfo`

## New file: `gitquery/parse_test.go`

`package gitquery_test` with unit tests for each parser using string literals. Coverage:

- Happy path with realistic git output
- Empty input returns nil/zero
- Malformed lines skipped gracefully
- Edge cases: binary files in numstat (`-\t-\tfile`), gone upstream, detached/bare worktrees

## Unchanged

- `gitquery_test.go` — existing integration tests remain as-is
- Public API — `ListWorktrees`, `ListBranches`, `ListCommits`, etc. signatures unchanged
- All consumers of `gitquery` are unaffected
