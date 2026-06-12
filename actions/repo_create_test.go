package actions

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedCreateRepoCommand struct {
	Name string
	Args []string
}

type fakeCreateRepoRunner struct {
	commands []recordedCreateRepoCommand
	failName string
	failErr  error
}

func (r *fakeCreateRepoRunner) Run(name string, args ...string) error {
	r.commands = append(r.commands, recordedCreateRepoCommand{Name: name, Args: append([]string(nil), args...)})
	if name == r.failName {
		if r.failErr != nil {
			return r.failErr
		}
		return errors.New("command failed")
	}
	return nil
}

func TestCreateRepoRejectsInvalidNames(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		repo string
	}{
		{name: "empty", repo: ""},
		{name: "whitespace", repo: "   "},
		{name: "slash", repo: "team/repo"},
		{name: "backslash", repo: `team\repo`},
		{name: "absolute path", repo: filepath.Join(string(filepath.Separator), "tmp", "repo")},
		{name: "dot", repo: "."},
		{name: "dot dot", repo: ".."},
		{name: "leading dash", repo: "-repo"},
		{name: "reserved worktrees suffix", repo: "app-worktrees"},
		{name: "existing destination", repo: "existing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateRepo(RepoCreateOptions{
				Root:         root,
				Name:         tt.repo,
				CreateGitHub: false,
			})
			if err == nil {
				t.Fatalf("expected %q to be rejected", tt.repo)
			}
		})
	}
}

func TestCreateRepoValidatesRootAndDestination(t *testing.T) {
	t.Run("empty root", func(t *testing.T) {
		if _, err := CreateRepo(RepoCreateOptions{Name: "repo"}); err == nil {
			t.Fatal("expected empty root error")
		}
	})

	t.Run("relative root", func(t *testing.T) {
		if _, err := CreateRepo(RepoCreateOptions{Root: "relative", Name: "repo"}); err == nil {
			t.Fatal("expected relative root error")
		}
	})

	t.Run("missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		if _, err := CreateRepo(RepoCreateOptions{Root: root, Name: "repo"}); err == nil {
			t.Fatal("expected missing root error")
		}
	})

	t.Run("cleans root and returns direct child destination", func(t *testing.T) {
		root := t.TempDir()
		cleanRoot := filepath.Clean(root)
		result, err := CreateRepo(RepoCreateOptions{
			Root:         filepath.Join(root, "."),
			Name:         "repo",
			CreateGitHub: false,
		})
		if err != nil {
			t.Fatalf("CreateRepo returned error: %v", err)
		}
		want := filepath.Join(cleanRoot, "repo")
		if result.DestinationPath != want {
			t.Fatalf("expected destination %q, got %q", want, result.DestinationPath)
		}
		if filepath.Dir(result.DestinationPath) != cleanRoot {
			t.Fatalf("expected destination to be a direct child of %q, got %q", cleanRoot, result.DestinationPath)
		}
	})
}

func TestCreateRepoLocalOnlyInitializesGitRepository(t *testing.T) {
	root := t.TempDir()

	result, err := CreateRepo(RepoCreateOptions{
		Root:         root,
		Name:         "project",
		CreateGitHub: false,
	})
	if err != nil {
		t.Fatalf("CreateRepo returned error: %v", err)
	}

	wantPath := filepath.Join(root, "project")
	if result.DestinationPath != wantPath {
		t.Fatalf("expected destination %q, got %q", wantPath, result.DestinationPath)
	}
	if !result.LocalCreated {
		t.Fatal("expected local creation to run")
	}
	if result.GitHubCreated {
		t.Fatal("did not expect GitHub creation for local-only repo")
	}
	if result.PartialSuccess || result.RetryAllowed || result.ExistingLocalPath != "" {
		t.Fatalf("did not expect retry metadata for local-only success: %+v", result)
	}

	cmd := exec.Command("git", "-C", wantPath, "rev-parse", "--git-dir")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected initialized git repo: %v\n%s", err, out)
	}
}

func TestCreateRepoGitInitFailureCleansDestination(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCreateRepoRunner{failName: "git", failErr: errors.New("git unavailable")}

	result, err := createRepoWithRunner(RepoCreateOptions{
		Root:         root,
		Name:         "project",
		CreateGitHub: false,
	}, runner)
	if err == nil {
		t.Fatal("expected git init failure")
	}
	dest := filepath.Join(root, "project")
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed git init to remove %q, stat err = %v", dest, statErr)
	}
	if result.LocalCreated || result.PartialSuccess || result.RetryAllowed {
		t.Fatalf("git init failure should not look retryable, got %+v", result)
	}
}

func TestCreateRepoCreatesGitHubRepoAfterLocalInit(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCreateRepoRunner{}

	result, err := createRepoWithRunner(RepoCreateOptions{
		Root:         root,
		Name:         "project",
		CreateGitHub: true,
		Visibility:   RepoVisibilityPrivate,
	}, runner)
	if err != nil {
		t.Fatalf("createRepoWithRunner returned error: %v", err)
	}

	dest := filepath.Join(root, "project")
	want := []recordedCreateRepoCommand{
		{Name: "git", Args: []string{"init", dest}},
		{Name: "gh", Args: []string{"repo", "create", "project", "--private", "--source", dest, "--remote", "origin"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant: %#v\n got: %#v", want, runner.commands)
	}
	if !result.LocalCreated || !result.GitHubCreated {
		t.Fatalf("expected local and GitHub creation success, got %+v", result)
	}
}

func TestCreateRepoDefaultsGitHubVisibilityToPublic(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCreateRepoRunner{}

	_, err := createRepoWithRunner(RepoCreateOptions{
		Root:         root,
		Name:         "project",
		CreateGitHub: true,
	}, runner)
	if err != nil {
		t.Fatalf("createRepoWithRunner returned error: %v", err)
	}

	if len(runner.commands) != 2 {
		t.Fatalf("expected two commands, got %#v", runner.commands)
	}
	got := strings.Join(runner.commands[1].Args, " ")
	if !strings.Contains(got, "--public") {
		t.Fatalf("expected gh command to use --public by default, got %q", got)
	}
}

func TestCreateRepoGitHubFailureReturnsPartialSuccess(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCreateRepoRunner{failName: "gh", failErr: errors.New("gh auth required")}

	result, err := createRepoWithRunner(RepoCreateOptions{
		Root:         root,
		Name:         "project",
		CreateGitHub: true,
		Visibility:   RepoVisibilityPublic,
	}, runner)
	if err == nil {
		t.Fatal("expected GitHub failure")
	}
	dest := filepath.Join(root, "project")
	if _, statErr := os.Stat(dest); statErr != nil {
		t.Fatalf("expected local repo directory to remain after GitHub failure: %v", statErr)
	}
	if !result.LocalCreated || result.GitHubCreated || !result.PartialSuccess || !result.RetryAllowed {
		t.Fatalf("expected retryable partial success, got %+v", result)
	}
	if result.ExistingLocalPath != dest {
		t.Fatalf("expected retry path %q, got %q", dest, result.ExistingLocalPath)
	}
}

func TestCreateRepoRemoteOnlyRetrySkipsLocalInit(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "project")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeCreateRepoRunner{}

	result, err := createRepoWithRunner(RepoCreateOptions{
		Root:              root,
		Name:              "project",
		CreateGitHub:      true,
		Visibility:        RepoVisibilityPrivate,
		RemoteOnlyRetry:   true,
		ExistingLocalPath: existing,
	}, runner)
	if err != nil {
		t.Fatalf("createRepoWithRunner returned error: %v", err)
	}

	want := []recordedCreateRepoCommand{
		{Name: "gh", Args: []string{"repo", "create", "project", "--private", "--source", existing, "--remote", "origin"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant: %#v\n got: %#v", want, runner.commands)
	}
	if result.LocalCreated {
		t.Fatalf("remote-only retry should not re-run local creation: %+v", result)
	}
	if !result.GitHubCreated {
		t.Fatalf("expected GitHub creation success: %+v", result)
	}
}

func TestCreateRepoRemoteOnlyRetryRequiresGitHubCreation(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "project")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeCreateRepoRunner{}

	result, err := createRepoWithRunner(RepoCreateOptions{
		Root:              root,
		Name:              "project",
		CreateGitHub:      false,
		RemoteOnlyRetry:   true,
		ExistingLocalPath: existing,
	}, runner)
	if err == nil {
		t.Fatal("expected remote-only retry without GitHub creation to fail")
	}
	if result.GitHubCreated {
		t.Fatalf("retry without GitHub creation should not report success: %+v", result)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("retry validation should not run commands, got %#v", runner.commands)
	}
}

func TestCreateRepoRemoteOnlyRetryValidatesExistingLocalPath(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "project")
	if err := os.Mkdir(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		repoName string
		path     string
	}{
		{name: "empty", repoName: "project", path: ""},
		{name: "relative", repoName: "project", path: "project"},
		{name: "missing", repoName: "project", path: filepath.Join(root, "missing")},
		{name: "outside root", repoName: "project", path: outside},
		{name: "name mismatch", repoName: "other", path: valid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := createRepoWithRunner(RepoCreateOptions{
				Root:              root,
				Name:              tt.repoName,
				CreateGitHub:      true,
				RemoteOnlyRetry:   true,
				ExistingLocalPath: tt.path,
			}, &fakeCreateRepoRunner{})
			if err == nil {
				t.Fatal("expected retry validation error")
			}
		})
	}
}
