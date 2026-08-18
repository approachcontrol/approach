package agentskills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApproachFlowIsCanonicalUnifiedSkill(t *testing.T) {
	root := filepath.Join(repoRoot(t), "agent-skills")
	entry := readFile(t, filepath.Join(root, "approach-flow", "SKILL.md"))

	requireContainsAll(t, "unified trigger metadata", entry, []string{
		"name: approach-flow",
		"active Approach Flow phase",
		"create a persisted Approach Flow",
		"persist or update an Approach plan",
	})
	for _, reference := range []string{"active-phase.md", "create-flow.md", "plans.md", "persistence.md"} {
		if !strings.Contains(entry, "references/"+reference) {
			t.Fatalf("canonical skill does not route to %s", reference)
		}
		if _, err := os.Stat(filepath.Join(root, "approach-flow", "references", reference)); err != nil {
			t.Fatalf("canonical skill reference %s is unavailable: %v", reference, err)
		}
	}

	combined := entry
	for _, reference := range []string{"active-phase.md", "create-flow.md", "plans.md", "persistence.md"} {
		combined += "\n" + readFile(t, filepath.Join(root, "approach-flow", "references", reference))
	}
	requireContainsAll(t, "unified CLI workflow", combined, []string{
		"approach flow read",
		"approach flow create --prepare-worktree",
		"approach flow plan save",
		"approach plan save --json",
		"approach plan status set",
		"approach plan read --json",
		"approach flow phase complete",
		"APPROACH_EXECUTABLE",
		"persistence failure",
	})
	// The spooled contract: a deferred write is exit 0 with a fixed message,
	// reported as deferred and never retried.
	requireContainsAll(t, "spooled writes", combined, []string{
		"spooled: control endpoint unreachable and this build cannot open the flow database; the request will be applied on the next approach start",
		"do not retry",
		"APPROACH_CONTROL_ENDPOINT",
		"APPROACH_CONTROL_TOKEN",
		"never spool",
	})
}

func TestApproachFlowHasCanonicalInterfaceMetadata(t *testing.T) {
	metadata := readFile(t, filepath.Join(repoRoot(t), "agent-skills", "approach-flow", "agents", "openai.yaml"))
	requireContainsAll(t, "canonical skill interface", metadata, []string{
		`display_name: "Approach Flow"`,
		`short_description: "Persist Approach Flows and plans"`,
		`default_prompt: "Use $approach-flow`,
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireContainsAll(t *testing.T, label, haystack string, needles []string) {
	t.Helper()
	normalized := strings.Join(strings.Fields(haystack), " ")
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) && !strings.Contains(normalized, needle) {
			t.Fatalf("%s missing %q", label, needle)
		}
	}
}
