package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Repo represents a discovered git repository.
type Repo struct {
	Path        string
	DisplayName string
}

// ScanOptions configures the scanner.
type ScanOptions struct {
	Root string
	// MaxDepth controls how many directory levels below Root are scanned.
	// Only values 0-2 are meaningful: 0 defaults to 2, 1 scans only the
	// immediate children of Root, and 2 also scans one level deeper. Values
	// greater than 2 behave the same as 2 (the scan never recurses further).
	MaxDepth int
}

// Scan discovers git repositories under the configured root.
// Returns repos sorted alphabetically by DisplayName.
func Scan(opts ScanOptions) ([]Repo, error) {
	root := opts.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, "dev")
	}

	maxDepth := opts.MaxDepth
	if maxDepth == 0 {
		maxDepth = 2
	}

	var repos []Repo

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), "-worktrees") {
			continue
		}

		path := filepath.Join(root, entry.Name())

		if isRepo(path) {
			repos = append(repos, Repo{
				Path:        path,
				DisplayName: entry.Name(),
			})
			continue
		}

		if maxDepth >= 2 {
			subEntries, err := os.ReadDir(path)
			if err != nil {
				continue
			}
			for _, sub := range subEntries {
				if !sub.IsDir() {
					continue
				}
				if strings.HasSuffix(sub.Name(), "-worktrees") {
					continue
				}
				subPath := filepath.Join(path, sub.Name())
				if isRepo(subPath) {
					repos = append(repos, Repo{
						Path:        subPath,
						DisplayName: entry.Name() + "/" + sub.Name(),
					})
				}
			}
		}
	}

	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].DisplayName) < strings.ToLower(repos[j].DisplayName)
	})

	return repos, nil
}

func isRepo(path string) bool {
	// A repo has a ".git" entry that is either a directory (normal repo) or a
	// regular file (a git worktree/submodule pointer). Bare repos (which have no
	// ".git" entry at all) are intentionally not detected here.
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}
