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
	IsBare      bool
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

		if isRepo, isBare := repoInfo(path); isRepo {
			repos = append(repos, Repo{
				Path:        path,
				DisplayName: entry.Name(),
				IsBare:      isBare,
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
				if isRepo, isBare := repoInfo(subPath); isRepo {
					repos = append(repos, Repo{
						Path:        subPath,
						DisplayName: entry.Name() + "/" + sub.Name(),
						IsBare:      isBare,
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

func repoInfo(path string) (isRepo bool, isBare bool) {
	// A normal checkout has a ".git" entry that is either a directory or a
	// regular file (a git worktree/submodule pointer).
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err == nil {
		return info.IsDir() || info.Mode().IsRegular(), false
	}
	if !os.IsNotExist(err) {
		return false, false
	}

	if !isRegularFile(filepath.Join(path, "HEAD")) ||
		!isDir(filepath.Join(path, "objects")) ||
		!isDir(filepath.Join(path, "refs")) ||
		!configHasCoreBare(filepath.Join(path, "config")) {
		return false, false
	}

	head, err := os.ReadFile(filepath.Join(path, "HEAD"))
	if err != nil {
		return false, false
	}
	if !looksLikeGitHEAD(strings.TrimSpace(string(head))) {
		return false, false
	}
	return true, true
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func looksLikeGitHEAD(head string) bool {
	if strings.HasPrefix(head, "ref: refs/") {
		return true
	}
	if len(head) != 40 {
		return false
	}
	for _, r := range head {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func configHasCoreBare(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inCore := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inCore = strings.EqualFold(strings.TrimSpace(strings.Trim(line, "[]")), "core")
			continue
		}
		if !inCore {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "bare") && strings.EqualFold(strings.TrimSpace(value), "true") {
			return true
		}
	}
	return false
}
