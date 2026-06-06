package gitquery

import (
	"strconv"
	"strings"
)

// ParseBranchLine parses one line of git for-each-ref output for a local branch.
// Returns the branch (with Name, HasUpstream, UpstreamGone populated) and the upstream ref string.
func ParseBranchLine(line string) (Branch, string) {
	parts := strings.SplitN(line, "\t", 4)
	b := Branch{Name: parts[0]}

	upstreamIndex := 1
	trackIndex := 2
	if len(parts) > 1 && strings.HasPrefix(parts[1], "refs/heads/") {
		b.FullRef = parts[1]
		upstreamIndex = 2
		trackIndex = 3
	}

	var upstream string
	if len(parts) > upstreamIndex && parts[upstreamIndex] != "" {
		b.HasUpstream = true
		upstream = parts[upstreamIndex]
		if len(parts) > trackIndex && strings.Contains(parts[trackIndex], "gone") {
			b.UpstreamGone = true
		}
	}

	return b, upstream
}

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

// WorktreeInfo holds data parsed from one block of git worktree list --porcelain output.
type WorktreeInfo struct {
	Path       string
	Commit     string
	Branch     string
	IsBare     bool
	Detached   bool
	Locked     bool
	LockReason string
}

// ParseWorktreeList parses the full output of git worktree list --porcelain
// into a slice of WorktreeInfo entries.
func ParseWorktreeList(output string) []WorktreeInfo {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return nil
	}

	var result []WorktreeInfo
	var current []string
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			if len(current) > 0 {
				result = append(result, parseOneWorktreeBlock(current))
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		result = append(result, parseOneWorktreeBlock(current))
	}
	return result
}

func parseOneWorktreeBlock(block []string) WorktreeInfo {
	var wt WorktreeInfo
	for _, line := range block {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wt.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			wt.Commit = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			wt.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			wt.IsBare = true
		case line == "detached":
			wt.Detached = true
			wt.Branch = "(detached)"
		case line == "locked":
			wt.Locked = true
		case strings.HasPrefix(line, "locked "):
			wt.Locked = true
			wt.LockReason = strings.TrimPrefix(line, "locked ")
		}
	}
	return wt
}

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
