package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

type configLockAttempt struct {
	path  string
	label string
}

func TestAllConfigMutationFamiliesSerializeAtOneTransactionLock(t *testing.T) {
	configHome := t.TempDir()
	path := filepath.Join(configHome, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	seed := "# preserve top-level comment\n" +
		"[scan]\n" +
		"root = \"/preserve/root\"\n" +
		"unrelated = \"keep me\"\n\n" +
		"[agent]\n" +
		"plan_prompt = \"remove me\"\n" +
		"custom_assignment = \"preserve me\"\n" +
		"# preserve agent comment\n\n" +
		"[flow_prompts]\n" +
		"generic = \"preserve generic\"\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	getenv := func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return configHome
		}
		return ""
	}
	options := []Option{WithGetenv(getenv), WithLockTimeout(time.Second)}
	lockPath := path + ".lock"
	wantLabel := fmt.Sprintf("config lock %q", path)
	release, err := artifacts.AcquireFileLock(lockPath, "test config lock", time.Second)
	if err != nil {
		t.Fatalf("hold config lock: %v", err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			release()
		}
	})

	previousAcquire := acquireConfigFileLock
	attempts := make(chan configLockAttempt, 6)
	acquireConfigFileLock = func(path, label string, timeout time.Duration) (func(), error) {
		attempts <- configLockAttempt{path: path, label: label}
		return artifacts.AcquireFileLock(path, label, timeout)
	}
	t.Cleanup(func() { acquireConfigFileLock = previousAcquire })
	results := make(chan error, 6)
	go func() { results <- SaveAgentCommand("codex", options...) }()
	go func() { results <- SaveAgentModel("codex", "gpt-5.5", options...) }()
	go func() { results <- SaveAgentReasoningEffort("claude", "max", options...) }()
	go func() { results <- SaveFlowAutoMerge(true, options...) }()
	go func() {
		results <- SavePromptTemplate("flow_prompts", "implementation", "new implementation prompt", options...)
	}()
	go func() { results <- ResetPromptTemplate("agent", "plan_prompt", options...) }()

	for i := 0; i < 6; i++ {
		select {
		case attempt := <-attempts:
			if attempt.path != lockPath || attempt.label != wantLabel {
				release()
				released = true
				t.Fatalf("lock attempt = %#v, want path %q label %q", attempt, lockPath, wantLabel)
			}
		case <-time.After(time.Second):
			release()
			released = true
			t.Fatal("timed out waiting for all config writers at the lock boundary")
		}
	}
	release()
	released = true
	for i := 0; i < 6; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent config mutation error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent config mutations to finish")
		}
	}

	cfg, err := LoadFrom(path, options...)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.Agent.Command != "codex" || cfg.Agent.CodexModel != "gpt-5.5" || cfg.Agent.ClaudeReasoningEffort != "max" {
		t.Fatalf("agent mutations missing: %#v", cfg.Agent)
	}
	if cfg.FlowPrompts.Implementation != "new implementation prompt" || cfg.FlowPrompts.Generic != "preserve generic" {
		t.Fatalf("flow prompts = %#v", cfg.FlowPrompts)
	}
	if cfg.Agent.PlanPrompt != "" {
		t.Fatalf("plan prompt = %q, want reset", cfg.Agent.PlanPrompt)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	text := string(data)
	for _, preserved := range []string{
		"# preserve top-level comment\n",
		"root = \"/preserve/root\"\n",
		"unrelated = \"keep me\"\n",
		"custom_assignment = \"preserve me\"\n",
		"# preserve agent comment\n",
		"generic = \"preserve generic\"\n",
	} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("config lost unrelated text %q:\n%s", preserved, text)
		}
	}
	if strings.Contains(text, "plan_prompt") {
		t.Fatalf("config retained reset target:\n%s", text)
	}
	assertConfigLockMode(t, filepath.Dir(path), 0o700)
	assertConfigLockMode(t, path, 0o600)
	assertConfigLockMode(t, lockPath, 0o600)
}

func assertConfigLockMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
