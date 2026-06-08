package actions

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func planAgentContext() AgentLaunchContext {
	return AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/wtui/sessions/v1",
		PlanID:           "plan-1",
		PlanPath:         "/state/wtui/sessions/v1/plans/plan-1/plan.md",
		InitialPrompt:    "Read the plan and begin implementation.",
	}
}

func putAgentOnPath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake agent executable: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func agentLaunchScript(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "wtui-agent-*.sh"))
	if err != nil {
		t.Fatalf("glob agent launch script: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one agent launch script in %s, got %d: %#v", os.TempDir(), len(matches), matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read agent launch script: %v", err)
	}
	return string(data)
}

func requireScriptContains(t *testing.T, script, want string) {
	t.Helper()
	if !strings.Contains(script, want) {
		t.Fatalf("agent launch script missing %q", want)
	}
}

func TestAgentLaunch_InsideTmuxRunsAgentInSession(t *testing.T) {
	putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})
	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("inside-tmux agent launch should be detached (non-interactive)")
	}
	joined := strings.Join(launch.Cmd.Args, "\x00")
	if !strings.HasPrefix(joined, "sh\x00-c\x00") {
		t.Fatalf("expected sh -c tmux script, got %#v", launch.Cmd.Args)
	}
	// The agent command, plan environment, and prompt must survive the hop
	// into the tmux session.
	script := agentLaunchScript(t)
	for _, want := range []string{
		"codex",
		"--config",
		"session-hook --provider codex",
		"WTUI_PLAN_ID='plan-1'",
		"WTUI_PLAN_PATH='/state/wtui/sessions/v1/plans/plan-1/plan.md'",
		"Read the plan and begin implementation.",
	} {
		requireScriptContains(t, script, want)
	}
}

func TestAgentLaunch_InsideZellijRunsAgentInSession(t *testing.T) {
	putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"ZELLIJ": "0"})
	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("zellij"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("inside-zellij agent launch should be detached (non-interactive)")
	}
	args := launch.Cmd.Args
	if len(args) < 6 || args[0] != "zellij" || args[1] != "run" || args[2] != "--cwd" || args[3] != "/repo/worktree" {
		t.Fatalf("unexpected zellij run args: %#v", args)
	}
	script := agentLaunchScript(t)
	for _, want := range []string{"codex", "WTUI_PLAN_ID='plan-1'", "Read the plan and begin implementation."} {
		requireScriptContains(t, script, want)
	}
}

func TestAgentLaunch_DarwinExternalTerminalRunsAgent(t *testing.T) {
	putAgentOnPath(t, "codex")
	launch, err := agentLaunch(planAgentContext(), "darwin", fakeGetenv(nil), fakeLookPath("osascript", "open"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("darwin external Terminal agent launch should be detached")
	}
	if launch.Cmd.Args[0] != "osascript" {
		t.Fatalf("expected osascript transport, got %#v", launch.Cmd.Args)
	}
	script := agentLaunchScript(t)
	for _, want := range []string{"cd '/repo/worktree'", "codex", "Read the plan and begin implementation."} {
		requireScriptContains(t, script, want)
	}
}

func TestAgentLaunch_TerminalEnvRunsAgentWithDashE(t *testing.T) {
	putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"TERMINAL": "alacritty"})
	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("alacritty"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("TERMINAL agent launch should be detached")
	}
	args := launch.Cmd.Args
	if args[0] != "alacritty" {
		t.Fatalf("expected alacritty transport, got %#v", args)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "-e\x00sh\x00-c\x00") {
		t.Fatalf("expected -e sh -c invocation, got %#v", args)
	}
	if !strings.Contains(joined, "wtui-agent-") {
		t.Fatalf("expected agent script path in TERMINAL launch, got %#v", args)
	}
	if launch.Cmd.Dir != "/repo/worktree" {
		t.Fatalf("expected launch dir /repo/worktree, got %q", launch.Cmd.Dir)
	}
	requireScriptContains(t, agentLaunchScript(t), "codex")
}

func TestAgentLaunch_OutsideTmuxUsesTTYButIsDetachedForFinalization(t *testing.T) {
	putAgentOnPath(t, "codex")
	launch, err := agentLaunch(planAgentContext(), "linux", fakeGetenv(nil), fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("outside-tmux tmux launch should use the current TTY")
	}
	if !launch.Detached {
		t.Fatal("outside-tmux tmux launch should be detached for session finalization")
	}
}

func TestAgentLaunch_TerminalEnvDarwinUnsupportedReturnsError(t *testing.T) {
	putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"TERMINAL": "MacTerminalApp"})
	_, err := agentLaunch(planAgentContext(), "darwin", env, fakeLookPath("open"))
	if err == nil {
		t.Fatal("expected error when a GUI-only TERMINAL cannot run an agent command")
	}
}

func TestAgentLaunch_ShellFallbackIsInteractive(t *testing.T) {
	putAgentOnPath(t, "codex")
	shell := tempExecutableShell(t)
	env := fakeGetenv(map[string]string{"SHELL": shell})
	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath())
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("shell fallback agent launch should be interactive (hands over the TTY)")
	}
	joined := strings.Join(launch.Cmd.Args, "\x00")
	if !strings.Contains(joined, "wtui-agent-") {
		t.Fatalf("expected agent script path in shell fallback, got %#v", launch.Cmd.Args)
	}
	requireScriptContains(t, agentLaunchScript(t), "codex")
}

func TestAgentLaunch_WorkingDirControlsCommandDirKeepsWorktreeMetadata(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := planAgentContext()
	ctx.WorkingDir = "/repo/worktree/subdir"
	ctx.ResumeSessionID = "codex-session-1"
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})
	if _, err := agentLaunch(ctx, "linux", env, fakeLookPath("tmux")); err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	script := agentLaunchScript(t)
	requireScriptContains(t, script, "cd '/repo/worktree/subdir'")
	requireScriptContains(t, script, "WTUI_WORKTREE_PATH='/repo/worktree'")
	requireScriptContains(t, script, "'resume' 'codex-session-1'")
}

func TestAgentLaunch_UsesResolvedAgentExecutablePath(t *testing.T) {
	agentPath := putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})
	if _, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("tmux")); err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	script := agentLaunchScript(t)
	if !strings.Contains(script, shellQuote(agentPath)) {
		t.Fatalf("expected detached launch to use resolved agent path %q", agentPath)
	}
}

func TestAgentLaunch_PropagatesInheritedAgentEnvironment(t *testing.T) {
	putAgentOnPath(t, "codex")
	t.Setenv("OPENAI_API_KEY", "secret-from-wtui-process")
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})

	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	joined := strings.Join(launch.Cmd.Args, "\x00")
	if strings.Contains(joined, "secret-from-wtui-process") {
		t.Fatalf("secret leaked into transport argv: %#v", launch.Cmd.Args)
	}
	script := agentLaunchScript(t)
	if !strings.Contains(script, "export OPENAI_API_KEY='secret-from-wtui-process'") {
		t.Fatal("expected detached launch script to propagate inherited agent env")
	}
}

func TestAgentLaunch_RejectsMissingAgentExecutableBeforeTransport(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})

	_, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("tmux"))
	if err == nil {
		t.Fatal("expected missing agent executable error")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("expected error to mention codex, got %v", err)
	}
}

func TestAgentLaunch_CleansScriptWhenTransportSelectionFails(t *testing.T) {
	putAgentOnPath(t, "codex")
	env := fakeGetenv(map[string]string{"TERMINAL": "ghostterm"})

	_, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath())
	if err == nil {
		t.Fatal("expected terminal selection error")
	}
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "wtui-agent-*.sh"))
	if err != nil {
		t.Fatalf("glob agent launch script: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected failed launch to clean agent script, got %#v", matches)
	}
}

func TestTerminalCommand_ShellCommandResistsInjection(t *testing.T) {
	// Execution-based proof of quoting. The payloads use $(...) command
	// substitution (not trailing `;`/`&&`, which `exec` would swallow) and
	// contain no single quotes (so they cannot accidentally self-quote): if any
	// untrusted value escaped its quotes, the substitution runs during shell
	// expansion and creates a marker file. If quoting is removed (e.g.
	// shellQuote becomes the identity function) this test fails — verified by
	// mutation testing. With correct quoting, only the legitimate command runs.
	tmp := t.TempDir()
	tc, err := newTerminalCommand(tmp, []string{
		// Attempts to break out of the `export KEY=VAL` token.
		`WTUI_BRANCH=x$(touch pwned_env)`,
	}, []string{
		// argv[0] is the legitimate command; the trailing arg attempts injection.
		"touch", "ran", `$(touch pwned_arg)`,
	}, "")
	if err != nil {
		t.Fatalf("newTerminalCommand returned error: %v", err)
	}

	cmd := exec.Command("sh", "-c", tc.shellCommand())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered command failed: %v\noutput: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "ran")); err != nil {
		t.Fatalf("legitimate command did not run: %v", err)
	}
	for _, marker := range []string{"pwned_env", "pwned_arg"} {
		if _, err := os.Stat(filepath.Join(tmp, marker)); err == nil {
			t.Fatalf("injection succeeded: %q was created (quoting failed)", marker)
		}
	}
	if _, err := os.Stat(tc.scriptPath); !os.IsNotExist(err) {
		t.Fatalf("agent script was not removed after launch, stat err=%v", err)
	}
}

func TestAgentLaunch_OsascriptEscapesShellCommand(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := planAgentContext()
	// Adversarial prompt that tries to break out of the AppleScript string. It
	// contains no single quotes, so correct shellQuote wraps it verbatim in
	// single quotes; the assertion below checks for that exact single-quoted
	// token (a fixed literal, NOT recomputed via shellQuote) so the test fails
	// if quoting is removed.
	const prompt = `"; do shell script "touch /tmp/PWNED"; echo "`
	ctx.InitialPrompt = prompt
	launch, err := agentLaunch(ctx, "darwin", fakeGetenv(nil), fakeLookPath("osascript", "open"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	if launch.Cmd.Args[0] != "osascript" {
		t.Fatalf("expected osascript transport, got %#v", launch.Cmd.Args)
	}

	const prefix = `tell application "Terminal" to do script `
	var doScript string
	for _, arg := range launch.Cmd.Args {
		if strings.HasPrefix(arg, prefix) {
			doScript = strings.TrimPrefix(arg, prefix)
		}
	}
	if doScript == "" {
		t.Fatalf("no do-script argument found in %#v", launch.Cmd.Args)
	}
	// Must be a well-formed quoted string: if %q escaping of the prompt's quotes
	// broke, Unquote fails.
	inner, err := strconv.Unquote(doScript)
	if err != nil {
		t.Fatalf("do-script payload is not a valid quoted string (escaping broke): %q", doScript)
	}
	if strings.Contains(inner, prompt) {
		t.Fatalf("prompt leaked into AppleScript command: %q", inner)
	}
	script := agentLaunchScript(t)
	// The launch script must carry the prompt inside literal single quotes. Fixed
	// expectation, not recomputed via shellQuote.
	if !strings.Contains(script, `'`+prompt+`'`) {
		t.Fatal("expected prompt single-quoted inside the launch script")
	}
}

func TestAgentLaunch_SessionNameIsUniquePerLaunchAndDistinctFromTerminal(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := planAgentContext()
	ctx.LaunchID = "launch-aaa"
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/s"})

	first, err := agentLaunch(ctx, "linux", env, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}
	ctx.LaunchID = "launch-bbb"
	second, err := agentLaunch(ctx, "linux", env, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	// The session name is the 5th arg (sh -c <script> wtui <session> <cmd>).
	firstSession := first.Cmd.Args[4]
	secondSession := second.Cmd.Args[4]
	if firstSession == secondSession {
		t.Fatalf("expected distinct session names per launch, both = %q", firstSession)
	}
	// It must differ from the plain `t` terminal session for the same worktree,
	// so launching an agent never collides with a shell session opened by `t`.
	if termSession := WorktreeSessionName(ctx.WorktreePath); firstSession == termSession {
		t.Fatalf("agent session %q must not equal the `t` terminal session %q", firstSession, termSession)
	}
	// It should still be rooted at the recognizable worktree session name.
	if !strings.HasPrefix(firstSession, WorktreeSessionName(ctx.WorktreePath)) {
		t.Fatalf("expected agent session rooted at worktree name, got %q", firstSession)
	}
}

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
	}, "darwin", fakeGetenv(nil), fakeLookPath())
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
	if !reflect.DeepEqual(launch.Cmd.Args[:1], []string{"open"}) {
		t.Fatalf("unexpected codex-app args: %#v", launch.Cmd.Args)
	}
	gotURL, err := url.Parse(launch.Cmd.Args[1])
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	if got := gotURL.Query().Get("path"); got != "/repo/work tree+plus" {
		t.Fatalf("path query = %q, want worktree path", got)
	}
	prompt := gotURL.Query().Get("prompt")
	for _, want := range []string{
		"Read the plan & begin + ship.",
		"WTUI_WORKTREE_PATH=" + shellQuote("/repo/work tree+plus"),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "WTUI_AGENT") {
		t.Fatalf("prompt should not include agent metadata:\n%s", prompt)
	}
	if strings.Contains(prompt, "inherited-launch") {
		t.Fatalf("prompt leaked inherited WTUI_LAUNCH_ID:\n%s", prompt)
	}
}

func TestCodexAppLaunchUsesWorkingDirForNewThreadPath(t *testing.T) {
	launch, err := agentLaunch(AgentLaunchContext{
		Command:      "codex-app",
		WorktreePath: "/repo/worktree",
		WorkingDir:   "/repo/worktree/subdir",
	}, "darwin", fakeGetenv(nil), fakeLookPath())
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
		FlowID:           "flow-1",
		FlowPhaseID:      "plan",
		InitialPrompt:    "Read the plan and begin implementation.",
	}, "darwin", fakeGetenv(nil), fakeLookPath())
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
		"WTUI_WORKTREE_PATH=" + shellQuote("/repo/work'tree$(bad)"),
		"WTUI_SESSION_STATE_ROOT=" + shellQuote("/state/wtui/sessions/v1"),
		"WTUI_PLAN_STATE_ROOT=" + shellQuote("/state/wtui/sessions/v1"),
		"WTUI_FLOW_STATE_ROOT=" + shellQuote("/state/wtui/sessions/v1"),
		"WTUI_PLAN_ID=" + shellQuote("plan-1"),
		"WTUI_PLAN_PATH=" + shellQuote("/state/wtui/sessions/v1/plans/plan-1/plan.md"),
		"WTUI_PLAN_PHASE_ID=" + shellQuote("phase-1"),
		"WTUI_FLOW_ID=" + shellQuote("flow-1"),
		"WTUI_FLOW_PHASE_ID=" + shellQuote("plan"),
		"--state-root " + shellQuote("/state/wtui/sessions/v1"),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{
		"WTUI_REPO_PATH",
		"WTUI_BRANCH",
		"WTUI_COMMIT",
		"WTUI_PLAN_PHASE_TITLE",
		"WTUI_PLAN_PHASE_STATUS",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("prompt should not include %s:\n%s", unwanted, prompt)
		}
	}
}

func TestCodexAppFlowLaunchUsesRepoProjectPath(t *testing.T) {
	launch, err := agentLaunch(AgentLaunchContext{
		Command:       "codex-app",
		RepoPath:      "/repo",
		WorktreePath:  "/repo-worktrees/flow-add-flow-mode",
		FlowID:        "flow-1",
		FlowPhaseID:   "plan",
		InitialPrompt: "Use wtui-flow.",
	}, "darwin", fakeGetenv(nil), fakeLookPath())
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	gotURL, err := url.Parse(launch.Cmd.Args[1])
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	if got := gotURL.Query().Get("path"); got != "/repo" {
		t.Fatalf("path query = %q, want repo project path", got)
	}
	prompt := gotURL.Query().Get("prompt")
	if !strings.Contains(prompt, "WTUI_WORKTREE_PATH="+shellQuote("/repo-worktrees/flow-add-flow-mode")) {
		t.Fatalf("prompt should still carry worktree metadata:\n%s", prompt)
	}
}

func TestCodexAppFlowImplementationLaunchUsesWorkingDirPath(t *testing.T) {
	launch, err := agentLaunch(AgentLaunchContext{
		Command:       "codex-app",
		RepoPath:      "/repo",
		WorktreePath:  "/repo-worktrees/flow-implementation",
		WorkingDir:    "/repo-worktrees/flow-implementation",
		FlowID:        "flow-1",
		FlowPhaseID:   "implementation",
		InitialPrompt: "Use wtui-flow.",
	}, "darwin", fakeGetenv(nil), fakeLookPath())
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	gotURL, err := url.Parse(launch.Cmd.Args[1])
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	if got := gotURL.Query().Get("path"); got != "/repo-worktrees/flow-implementation" {
		t.Fatalf("path query = %q, want flow worktree", got)
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
	}, "darwin", fakeGetenv(nil), fakeLookPath())
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "codex://threads/9a0c8d4e-1111-2222-3333-abcdefabcdef"}) {
		t.Fatalf("unexpected codex-app resume args: %#v", launch.Cmd.Args)
	}
	assertNoWTUIEnv(t, launch.Cmd.Environ())
}

func TestCodexAppLaunchRejectsMissingNewThreadPath(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app"}, "darwin", fakeGetenv(nil), fakeLookPath())
	if err == nil {
		t.Fatal("expected missing path error")
	}
	if !strings.Contains(err.Error(), "requires a worktree path or working directory") {
		t.Fatalf("unexpected missing path error: %v", err)
	}
}

func TestCodexAppLaunchRejectsRelativeNewThreadPath(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app", WorktreePath: "relative/path"}, "darwin", fakeGetenv(nil), fakeLookPath())
	if err == nil {
		t.Fatal("expected relative path error")
	}
	if !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("unexpected relative path error: %v", err)
	}
}

func TestCodexAppLaunchRejectsUnsupportedPlatform(t *testing.T) {
	_, err := agentLaunch(AgentLaunchContext{Command: "codex-app", WorktreePath: "/repo/worktree"}, "linux", fakeGetenv(nil), fakeLookPath())
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
