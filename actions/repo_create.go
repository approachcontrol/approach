package actions

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoVisibility is the GitHub visibility requested for a new repository.
type RepoVisibility string

const (
	RepoVisibilityPublic  RepoVisibility = "public"
	RepoVisibilityPrivate RepoVisibility = "private"
)

// RepoCreateOptions describes a local-first repository creation request.
type RepoCreateOptions struct {
	Root              string
	Name              string
	CreateGitHub      bool
	Visibility        RepoVisibility
	RemoteOnlyRetry   bool
	ExistingLocalPath string
}

// RepoCreateResult reports what was created and whether a failed GitHub step
// can be retried against the already-created local path.
type RepoCreateResult struct {
	DestinationPath   string
	LocalCreated      bool
	GitHubCreated     bool
	PartialSuccess    bool
	RetryAllowed      bool
	ExistingLocalPath string
}

type repoCreateRunner interface {
	Run(name string, args ...string) error
}

type execRepoCreateRunner struct{}

func (execRepoCreateRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return errors.New(msg)
	}
	return nil
}

// CreateRepo creates a local git repository and optionally creates/wires a
// GitHub repository through gh.
func CreateRepo(opts RepoCreateOptions) (RepoCreateResult, error) {
	return createRepoWithRunner(opts, execRepoCreateRunner{})
}

func createRepoWithRunner(opts RepoCreateOptions, runner repoCreateRunner) (RepoCreateResult, error) {
	if runner == nil {
		runner = execRepoCreateRunner{}
	}
	root, name, err := validateRepoCreateRootAndName(opts.Root, opts.Name)
	if err != nil {
		return RepoCreateResult{}, err
	}
	destination := filepath.Join(root, name)
	if err := ensureDirectRepoChild(root, name, destination); err != nil {
		return RepoCreateResult{}, err
	}

	if opts.RemoteOnlyRetry {
		return createRepoRemoteOnlyRetry(opts, runner, root, name, destination)
	}

	if _, err := os.Stat(destination); err == nil {
		return RepoCreateResult{}, fmt.Errorf("repo destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return RepoCreateResult{}, err
	}

	result := RepoCreateResult{DestinationPath: destination}
	if err := os.Mkdir(destination, 0o755); err != nil {
		return result, err
	}
	if err := runner.Run("git", "init", destination); err != nil {
		if removeErr := os.RemoveAll(destination); removeErr != nil {
			return result, fmt.Errorf("git init: %w; cleanup failed: %v", err, removeErr)
		}
		return result, fmt.Errorf("git init: %w", err)
	}
	result.LocalCreated = true

	if !opts.CreateGitHub {
		return result, nil
	}
	if err := runGitHubRepoCreate(runner, name, destination, opts.Visibility); err != nil {
		result.PartialSuccess = true
		result.RetryAllowed = true
		result.ExistingLocalPath = destination
		return result, fmt.Errorf("create GitHub repo: %w", err)
	}
	result.GitHubCreated = true
	return result, nil
}

func createRepoRemoteOnlyRetry(opts RepoCreateOptions, runner repoCreateRunner, root, name, destination string) (RepoCreateResult, error) {
	existing, err := validateRepoCreateRetryPath(root, name, opts.ExistingLocalPath)
	if err != nil {
		return RepoCreateResult{}, err
	}
	result := RepoCreateResult{DestinationPath: existing}
	if existing != destination {
		return result, fmt.Errorf("retry path must match repo destination: %s", destination)
	}
	if !opts.CreateGitHub {
		return result, fmt.Errorf("remote-only retry requires GitHub creation")
	}
	if err := runGitHubRepoCreate(runner, name, existing, opts.Visibility); err != nil {
		result.PartialSuccess = true
		result.RetryAllowed = true
		result.ExistingLocalPath = existing
		return result, fmt.Errorf("create GitHub repo: %w", err)
	}
	result.GitHubCreated = true
	return result, nil
}

func validateRepoCreateRootAndName(root, name string) (string, string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", fmt.Errorf("repo creation root cannot be empty")
	}
	if !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("repo creation root must be absolute")
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("repo creation root does not exist: %s", root)
		}
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("repo creation root is not a directory: %s", root)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("repo name cannot be empty")
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", "", fmt.Errorf("repo name cannot contain path separators")
	}
	if filepath.IsAbs(name) {
		return "", "", fmt.Errorf("repo name cannot be an absolute path")
	}
	if name == "." || name == ".." {
		return "", "", fmt.Errorf("repo name cannot be %q", name)
	}
	if strings.HasPrefix(name, "-") {
		return "", "", fmt.Errorf("repo name cannot start with '-'")
	}
	if strings.HasSuffix(name, "-worktrees") {
		return "", "", fmt.Errorf("repo name cannot end with '-worktrees'")
	}
	if filepath.Clean(name) != name {
		return "", "", fmt.Errorf("repo name must be a single path segment")
	}
	return root, name, nil
}

func validateRepoCreateRetryPath(root, name, existingLocalPath string) (string, error) {
	existingLocalPath = strings.TrimSpace(existingLocalPath)
	if existingLocalPath == "" {
		return "", fmt.Errorf("retry local path cannot be empty")
	}
	if !filepath.IsAbs(existingLocalPath) {
		return "", fmt.Errorf("retry local path must be absolute")
	}
	existingLocalPath = filepath.Clean(existingLocalPath)
	if err := ensureDirectRepoChild(root, name, existingLocalPath); err != nil {
		return "", err
	}
	info, err := os.Stat(existingLocalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("retry local path does not exist: %s", existingLocalPath)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("retry local path is not a directory: %s", existingLocalPath)
	}
	return existingLocalPath, nil
}

func ensureDirectRepoChild(root, name, destination string) error {
	destination = filepath.Clean(destination)
	if filepath.Dir(destination) != root || filepath.Base(destination) != name {
		return fmt.Errorf("repo destination must be a direct child of %s", root)
	}
	return nil
}

func runGitHubRepoCreate(runner repoCreateRunner, name, path string, visibility RepoVisibility) error {
	visibilityFlag := "--public"
	if visibility == RepoVisibilityPrivate {
		visibilityFlag = "--private"
	}
	return runner.Run("gh", "repo", "create", name, visibilityFlag, "--source", path, "--remote", "origin")
}
