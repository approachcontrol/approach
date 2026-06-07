package actions

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeSessionHookSettingsEncodesJSONString(t *testing.T) {
	hookCommand := "/tmp/wtui\a\v session-hook --provider claude"

	settings := claudeSessionHookSettings(hookCommand)

	var decoded struct {
		Hooks struct {
			SessionEnd []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionEnd"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &decoded); err != nil {
		t.Fatalf("settings is not valid JSON: %v\n%s", err, settings)
	}
	if got := decoded.Hooks.SessionEnd[0].Hooks[0].Command; got != hookCommand {
		t.Fatalf("command = %q, want %q", got, hookCommand)
	}
}

func TestCodexAppLaunchOpensNewThreadDeepLink(t *testing.T) {
	t.Setenv("WTUI_LAUNCH_ID", "inherited-launch")
	launch, err := agentLaunch(AgentLaunchContext{
		Command:       "codex-app",
		WorktreePath:  "/repo/work tree+plus",
		InitialPrompt: "Read the plan & begin + ship.",
	}, "darwin")
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("expected codex-app launch to be non-interactive")
	}
	if launch.Cmd.Dir != "" {
		t.Fatalf("expected open command to have no working dir, got %q", launch.Cmd.Dir)
	}
	assertNoWTUIEnv(t, launch.Cmd.Environ())
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "codex://threads/new?path=%2Frepo%2Fwork%20tree%2Bplus&prompt=Read%20the%20plan%20%26%20begin%20%2B%20ship."}) {
		t.Fatalf("unexpected codex-app args: %#v", launch.Cmd.Args)
	}
}

func TestCodexAppLaunchUsesWorkingDirForNewThreadPath(t *testing.T) {
	launch, err := agentLaunch(AgentLaunchContext{
		Command:      "codex-app",
		WorktreePath: "/repo/worktree",
		WorkingDir:   "/repo/worktree/subdir",
	}, "darwin")
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	gotURL, err := url.Parse(launch.Cmd.Args[1])
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	if got := gotURL.Query().Get("path"); got != "/repo/worktree/subdir" {
		t.Fatalf("path query = %q, want working dir", got)
	}
	if got := gotURL.Query().Get("prompt"); got != "" {
		t.Fatalf("prompt query = %q, want empty", got)
	}
}

func TestCodexAppLaunchPromptIncludesWTUIMetadata(t *testing.T) {
	t.Setenv("WTUI_PLAN_STATE_ROOT", "/inherited/state")
	launch, err := agentLaunch(AgentLaunchContext{
		Command:          "codex-app",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/work'tree$(bad)",
		Branch:           "feature/$(echo pwned)",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanID:           "plan-1",
		PlanPath:         "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		PlanPhaseID:      "phase-1",
		PlanPhaseTitle:   "Resolve conflicts",
		PlanPhaseStatus:  "in_progress",
		InitialPrompt:    "Read the plan and begin implementation.",
	}, "darwin")
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	assertNoWTUIEnv(t, launch.Cmd.Environ())

	gotURL, err := url.Parse(launch.Cmd.Args[1])
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	prompt := gotURL.Query().Get("prompt")
	for _, want := range []string{
		"Read the plan and begin implementation.",
		"WTUI_LAUNCH_ID=" + shellQuote("launch-1"),
		"WTUI_REPO_PATH=" + shellQuote("/repo"),
		"WTUI_WORKTREE_PATH=" + shellQuote("/repo/work'tree$(bad)"),
		"WTUI_BRANCH=" + shellQuote("feature/$(echo pwned)"),
		"WTUI_SESSION_STATE_ROOT=" + shellQuote("/state/wtui/sessions/v1"),
		"WTUI_PLAN_STATE_ROOT=" + shellQuote("/state/wtui/sessions/v1"),
		"WTUI_PLAN_ID=" + shellQuote("plan-1"),
		"WTUI_PLAN_PATH=" + shellQuote("/state/wtui/sessions/v1/plans/plan-1/plan.md"),
		"WTUI_PLAN_PHASE_ID=" + shellQuote("phase-1"),
		"WTUI_PLAN_PHASE_TITLE=" + shellQuote("Resolve conflicts"),
		"WTUI_PLAN_PHASE_STATUS=" + shellQuote("in_progress"),
		"--state-root " + shellQuote("/state/wtui/sessions/v1"),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestCodexAppLaunchOpensResumeDeepLink(t *testing.T) {
	t.Setenv("WTUI_SESSION_STATE_ROOT", "/inherited/state")
	launch, err := agentLaunch(AgentLaunchContext{
		Command:          "codex-app",
		WorktreePath:     "/repo/worktree",
		InitialPrompt:    "ignored for resume",
		ResumeSessionID:  "9a0c8d4e-1111-2222-3333-abcdefabcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
	}, "darwin")
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "codex://threads/9a0c8d4e-1111-2222-3333-abcdefabcdef"}) {
		t.Fatalf("unexpected codex-app resume args: %#v", launch.Cmd.Args)
	}
	assertNoWTUIEnv(t, launch.Cmd.Environ())
}

func TestCodexAppLaunchRejectsMissingNewThreadPath(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app"}, "darwin")
	if err == nil {
		t.Fatal("expected missing path error")
	}
	if !strings.Contains(err.Error(), "requires a worktree path or working directory") {
		t.Fatalf("unexpected missing path error: %v", err)
	}
}

func TestCodexAppLaunchRejectsRelativeNewThreadPath(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app", WorktreePath: "relative/path"}, "darwin")
	if err == nil {
		t.Fatal("expected relative path error")
	}
	if !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("unexpected relative path error: %v", err)
	}
}

func TestCodexAppLaunchRejectsUnsupportedPlatform(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app", WorktreePath: "/repo/worktree"}, "linux")
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("unexpected unsupported platform error: %v", err)
	}
}

func assertNoWTUIEnv(t *testing.T, env []string) {
	t.Helper()
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(key, "WTUI_") {
			t.Fatalf("expected codex-app open command to scrub WTUI env, found %q in %#v", key, env)
		}
	}
}
