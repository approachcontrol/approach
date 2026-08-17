// Package testgit isolates git CLI invocations used by tests.
//
// Temp repos inherit the runner's user/system config and can trigger
// auto-gc or background maintenance. Under parallel `go test ./...`
// that has failed CI with "fatal: unable to read tree" mid-commit.
package testgit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Env returns a process environment that ignores user/system git config
// and drops ambient repo selectors such as GIT_DIR.
func Env() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_CONFIG_") ||
			strings.HasPrefix(entry, "GIT_TEMPLATE_DIR=") ||
			strings.HasPrefix(entry, "GIT_DIR=") ||
			strings.HasPrefix(entry, "GIT_WORK_TREE=") ||
			strings.HasPrefix(entry, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(entry, "GIT_OBJECT_DIRECTORY=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

// Command builds a git invocation in dir with auto-gc and signing disabled.
func Command(dir string, args ...string) *exec.Cmd {
	gitArgs := append([]string{
		"-c", "gc.auto=0",
		"-c", "gc.autoDetach=false",
		"-c", "maintenance.auto=false",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = dir
	cmd.Env = Env()
	return cmd
}

// Run executes git in dir and returns combined output, failing the test on error.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := Command(dir, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// Output executes git in dir and returns stdout, failing the test on error.
func Output(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := Command(dir, args...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// ConfigureRepo sets a local identity and disables maintenance so later
// production git invocations against the same temp repo stay quiet.
func ConfigureRepo(t testing.TB, dir string) {
	t.Helper()
	Run(t, dir, "config", "user.email", "test@test.com")
	Run(t, dir, "config", "user.name", "Test")
	Run(t, dir, "config", "gc.auto", "0")
	Run(t, dir, "config", "gc.autoDetach", "false")
	Run(t, dir, "config", "maintenance.auto", "false")
}
