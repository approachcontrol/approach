package testgit

import (
	"strings"
	"testing"
)

func TestEnvDropsAmbientGitSelectors(t *testing.T) {
	t.Setenv("GIT_DIR", "/tmp/other.git")
	t.Setenv("GIT_WORK_TREE", "/tmp/other")
	t.Setenv("GIT_INDEX_FILE", "/tmp/index")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/tmp/objects")
	t.Setenv("GIT_TEMPLATE_DIR", "/tmp/template")
	t.Setenv("GIT_CONFIG_GLOBAL", "/tmp/gitconfig")
	t.Setenv("GIT_CONFIG_COUNT", "1")

	env := Env()
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_DIR=") ||
			strings.HasPrefix(entry, "GIT_WORK_TREE=") ||
			strings.HasPrefix(entry, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(entry, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(entry, "GIT_TEMPLATE_DIR=") ||
			strings.HasPrefix(entry, "GIT_CONFIG_") {
			t.Fatalf("ambient git selector leaked into test env: %s", entry)
		}
	}
	if !containsEnv(env, "GIT_CONFIG_GLOBAL=/dev/null") || !containsEnv(env, "GIT_CONFIG_NOSYSTEM=1") || !containsEnv(env, "GIT_OPTIONAL_LOCKS=0") {
		t.Fatalf("expected hermetic git config overrides, got %v", env)
	}
}

func TestCommandAndConfigureRepoDisableMaintenance(t *testing.T) {
	dir := t.TempDir()
	Run(t, dir, "init", "-b", "main")
	ConfigureRepo(t, dir)
	if got := Output(t, dir, "config", "--get", "gc.auto"); got != "0" {
		t.Fatalf("gc.auto = %q, want 0", got)
	}
	if got := Output(t, dir, "config", "--get", "maintenance.auto"); got != "false" {
		t.Fatalf("maintenance.auto = %q, want false", got)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
