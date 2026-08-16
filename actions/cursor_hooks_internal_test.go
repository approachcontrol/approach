package actions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCursorSessionHookCreatesManagedStopHook(t *testing.T) {
	merged, err := mergeCursorSessionHook(nil, cursorSessionHookCommand())
	if err != nil {
		t.Fatalf("mergeCursorSessionHook() error = %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(merged, &root); err != nil {
		t.Fatalf("unmarshal merged hooks: %v", err)
	}
	if root["version"] != float64(1) {
		t.Fatalf("version = %#v, want 1", root["version"])
	}
	hooks, _ := root["hooks"].(map[string]any)
	stop, _ := hooks["stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("stop hooks = %#v, want one managed hook", stop)
	}
	command, _ := stop[0].(map[string]any)["command"].(string)
	if !strings.Contains(command, cursorSessionHookMarker) {
		t.Fatalf("managed command %q does not contain marker %q", command, cursorSessionHookMarker)
	}
	if !strings.Contains(command, "APPROACH_LAUNCH_ID") || !strings.Contains(command, "APPROACH_EXECUTABLE") {
		t.Fatalf("managed command should filter on APPROACH_LAUNCH_ID and exec APPROACH_EXECUTABLE, got %q", command)
	}
}

func TestMergeCursorSessionHookPreservesUserHooksAndIsIdempotent(t *testing.T) {
	existing := []byte(`{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [{"command": "./audit.sh"}],
    "stop": [{"command": "./user-stop.sh"}]
  }
}
`)
	first, err := mergeCursorSessionHook(existing, cursorSessionHookCommand())
	if err != nil {
		t.Fatalf("first merge error = %v", err)
	}
	second, err := mergeCursorSessionHook(first, cursorSessionHookCommand())
	if err != nil {
		t.Fatalf("second merge error = %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(second, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	before, _ := hooks["beforeSubmitPrompt"].([]any)
	if len(before) != 1 {
		t.Fatalf("beforeSubmitPrompt hooks were dropped: %#v", before)
	}
	stop, _ := hooks["stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("stop hooks = %#v, want user hook plus one managed hook", stop)
	}
	user, _ := stop[0].(map[string]any)["command"].(string)
	if user != "./user-stop.sh" {
		t.Fatalf("user stop hook changed to %q", user)
	}
	managed, _ := stop[1].(map[string]any)["command"].(string)
	if !strings.Contains(managed, cursorSessionHookMarker) {
		t.Fatalf("managed hook missing: %#v", stop)
	}
}

func TestEnsureCursorSessionHookWritesIdempotently(t *testing.T) {
	home := t.TempDir()
	if err := ensureCursorSessionHook(home); err != nil {
		t.Fatalf("first ensureCursorSessionHook() error = %v", err)
	}
	path := cursorHooksPath(home)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("hooks.json mode = %o, want 0600", info.Mode().Perm())
	}
	if err := ensureCursorSessionHook(home); err != nil {
		t.Fatalf("second ensureCursorSessionHook() error = %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("second write changed hooks.json:\n%s\n---\n%s", first, second)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err != nil {
		t.Fatalf("expected .cursor dir: %v", err)
	}
}

func TestEnsureCursorSessionHookRequiresHome(t *testing.T) {
	if err := ensureCursorSessionHook(""); err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("ensureCursorSessionHook(\"\") error = %v, want HOME required", err)
	}
}

func TestAgentCommandWiresCursorSessionHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, err := AgentCommand(AgentLaunchContext{
		Command:      "cursor-agent",
		WorktreePath: "/repo/worktree",
		Embedded:     true,
	})
	if err != nil {
		t.Fatalf("AgentCommand returned error: %v", err)
	}
	raw, err := os.ReadFile(cursorHooksPath(home))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	if !strings.Contains(string(raw), cursorSessionHookMarker) {
		t.Fatalf("hooks.json missing managed stop hook:\n%s", raw)
	}
}
