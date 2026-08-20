package actions

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestPageTextBuildsInteractiveLessCommand(t *testing.T) {
	launch, err := pageText("diff --git a/f.txt\n+added\n", fakeLookPath("less"))
	if err != nil {
		t.Fatalf("pageText returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("expected page text launch to be interactive")
	}
	if launch.Cmd == nil {
		t.Fatal("expected command")
	}
	wantArgs := []string{"less", "-R"}
	if !reflect.DeepEqual(launch.Cmd.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", launch.Cmd.Args, wantArgs)
	}
	gotBody, err := io.ReadAll(launch.Cmd.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(gotBody) != "diff --git a/f.txt\n+added\n" {
		t.Fatalf("stdin = %q", string(gotBody))
	}
}

func planAgentContext() AgentLaunchContext {
	return AgentLaunchContext{
		Command:          "codex",
		LaunchID:         "launch-1",
		RepoPath:         "/repo",
		WorktreePath:     "/repo/worktree",
		Branch:           "main",
		Commit:           "abcdef",
		SessionStateRoot: "/state/approach/sessions/v1",
		PlanID:           "plan-1",
		PlanPath:         "/state/approach/sessions/v1/plans/plan-1/plan.md",
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
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "approach-agent-*.sh"))
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
		"APPROACH_PLAN_ID='plan-1'",
		"APPROACH_PLAN_PATH='/state/approach/sessions/v1/plans/plan-1/plan.md'",
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
	for _, want := range []string{"codex", "APPROACH_PLAN_ID='plan-1'", "Read the plan and begin implementation."} {
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

func TestAgentLaunchWithOptions_DarwinConfiguredITermRunsGeneratedScript(t *testing.T) {
	putAgentOnPath(t, "codex")
	launch, err := agentLaunchWithOptions(planAgentContext(), "darwin", fakeGetenv(nil), fakeLookPath("osascript"), LaunchOptions{
		TerminalCommand: "iTerm2.app",
	})
	if err != nil {
		t.Fatalf("agentLaunchWithOptions returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("iTerm agent launch should be detached")
	}
	if launch.Cleanup == nil {
		t.Fatal("expected cleanup to be wired")
	}
	joined := strings.Join(launch.Cmd.Args, "\n")
	for _, want := range []string{`tell application "iTerm"`, "activate", "write text", "exec sh '/"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected iTerm agent args to contain %q, got %#v", want, launch.Cmd.Args)
		}
	}
	for _, unwanted := range []string{"Read the plan and begin implementation.", "APPROACH_PLAN_ID", "codex --config"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("agent details leaked into AppleScript args: %q in %#v", unwanted, launch.Cmd.Args)
		}
	}
	requireScriptContains(t, agentLaunchScript(t), "Read the plan and begin implementation.")
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
	if !strings.Contains(joined, "approach-agent-") {
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

func TestAgentLaunchWithOptions_DarwinUnsupportedConfiguredGUIReturnsError(t *testing.T) {
	putAgentOnPath(t, "codex")
	_, err := agentLaunchWithOptions(planAgentContext(), "darwin", fakeGetenv(nil), fakeLookPath("open"), LaunchOptions{
		TerminalCommand: "GhostTerminal",
	})
	if err == nil {
		t.Fatal("expected unsupported configured GUI error")
	}
	for _, want := range []string{"[terminal].command", "GhostTerminal", "supported macOS terminal app"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}

func TestAgentLaunchWithOptions_NonDarwinMissingConfiguredCommandNamesConfig(t *testing.T) {
	putAgentOnPath(t, "codex")
	_, err := agentLaunchWithOptions(planAgentContext(), "linux", fakeGetenv(nil), fakeLookPath(), LaunchOptions{
		TerminalCommand: "ghostterm",
	})
	if err == nil {
		t.Fatal("expected missing configured terminal error")
	}
	for _, want := range []string{"[terminal].command", "ghostterm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
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
	if !strings.Contains(joined, "approach-agent-") {
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
	requireScriptContains(t, script, "APPROACH_WORKTREE_PATH='/repo/worktree'")
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
	t.Setenv("OPENAI_API_KEY", "secret-from-approach-process")
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})

	launch, err := agentLaunch(planAgentContext(), "linux", env, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("agentLaunch returned error: %v", err)
	}

	joined := strings.Join(launch.Cmd.Args, "\x00")
	if strings.Contains(joined, "secret-from-approach-process") {
		t.Fatalf("secret leaked into transport argv: %#v", launch.Cmd.Args)
	}
	script := agentLaunchScript(t)
	if !strings.Contains(script, "export OPENAI_API_KEY='secret-from-approach-process'") {
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
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "approach-agent-*.sh"))
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
		`APPROACH_BRANCH=x$(touch pwned_env)`,
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

func TestAgentLaunchWithOptions_ITermOsascriptEscapesShellCommand(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := planAgentContext()
	const prompt = `"; do shell script "touch /tmp/PWNED"; echo "`
	ctx.InitialPrompt = prompt
	launch, err := agentLaunchWithOptions(ctx, "darwin", fakeGetenv(nil), fakeLookPath("osascript"), LaunchOptions{
		TerminalCommand: "iTerm",
	})
	if err != nil {
		t.Fatalf("agentLaunchWithOptions returned error: %v", err)
	}
	if launch.Cmd.Args[0] != "osascript" {
		t.Fatalf("expected osascript transport, got %#v", launch.Cmd.Args)
	}

	const prefix = `tell current session of newWindow to write text `
	var writeText string
	for _, arg := range launch.Cmd.Args {
		if strings.HasPrefix(arg, prefix) {
			writeText = strings.TrimPrefix(arg, prefix)
		}
	}
	if writeText == "" {
		t.Fatalf("no iTerm write-text argument found in %#v", launch.Cmd.Args)
	}
	if strings.Contains(strings.Join(launch.Cmd.Args, "\n"), "current session of current window") {
		t.Fatalf("iTerm agent launch should not write into the user's current session: %#v", launch.Cmd.Args)
	}
	inner, err := strconv.Unquote(writeText)
	if err != nil {
		t.Fatalf("write-text payload is not a valid quoted string (escaping broke): %q", writeText)
	}
	if strings.Contains(inner, prompt) {
		t.Fatalf("prompt leaked into AppleScript command: %q", inner)
	}
	script := agentLaunchScript(t)
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

	// The session name is the 5th arg (sh -c <script> approach <session> <cmd>).
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
	hookCommand := "/tmp/approach\a\v session-hook --provider claude"

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

func TestEmbeddedTmuxAgentCommandBuildsPrivateScriptTransport(t *testing.T) {
	putAgentOnPath(t, "codex")
	t.Setenv("TMUX", "/tmp/parent-tmux.sock")
	t.Setenv("ZELLIJ", "parent-zellij")
	ctx := planAgentContext()
	ctx.Embedded = true
	ctx.Headless = true
	ctx.LaunchID = "launch/tmux"

	spec, err := embeddedTmuxAgentCommand(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("embeddedTmuxAgentCommand returned error: %v", err)
	}
	defer spec.Cleanup()

	if want := WorktreeSessionName(ctx.WorktreePath) + "-agent-launch-tmux"; spec.SessionName != want {
		t.Fatalf("session name = %q, want per-launch agent session", spec.SessionName)
	}
	if spec.StatusPath == "" {
		t.Fatal("expected status path for tmux exit propagation")
	}
	socketName := spec.HasSessionCommand.Args[4]
	if !strings.HasPrefix(socketName, "approach-agent-") || len(socketName) != len("approach-agent-00000000") {
		t.Fatalf("socket name = %q, want short hashed approach-agent name", socketName)
	}
	if strings.Contains(socketName, spec.SessionName) {
		t.Fatalf("socket name %q should not embed full session name %q", socketName, spec.SessionName)
	}
	if got, want := spec.DetachTarget, "env -u TMUX tmux -f /dev/null -L "+shellQuote(socketName)+" attach-session -t "+shellQuote(spec.SessionName); got != want {
		t.Fatalf("detach target = %q, want %q", got, want)
	}
	if got, want := spec.HasSessionCommand.Args, []string{"tmux", "-f", "/dev/null", "-L", socketName, "has-session", "-t", spec.SessionName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("has-session args = %#v, want %#v", got, want)
	}
	if got, want := spec.AttachCommand.Args[:3], []string{"/bin/sh", "-c", "tmux -f /dev/null -L \"$1\" attach-session -t \"$2\"\ntmux_status=$?\nif [ -r \"$3\" ]; then\n\tIFS= read -r agent_status < \"$3\"\n\trm -f \"$3\"\n\tcase \"$agent_status\" in\n\t\t\"\"|*[!0-9]*) exit \"$tmux_status\" ;;\n\t\t*) exit \"$agent_status\" ;;\n\tesac\nfi\nexit \"$tmux_status\""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attach args prefix = %#v, want %#v", got, want)
	}
	if got, want := spec.AttachCommand.Args[3:], []string{"approach", socketName, spec.SessionName, spec.StatusPath}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attach wrapper args = %#v, want %#v", got, want)
	}
	if envValue(spec.AttachCommand.Env, "TMUX") != "" {
		t.Fatalf("attach command inherited TMUX: %#v", spec.AttachCommand.Env)
	}
	wantNewSession := []string{
		"tmux", "-f", "/dev/null", "-L", socketName,
		"start-server",
		";", "set-option", "-g", "prefix", "None",
		";", "unbind-key", "C-b",
		";", "set-option", "-g", "status", "off",
		";", "new-session", "-d", "-s", spec.SessionName, "-c", "/repo/worktree", "exec sh " + shellQuote(spec.ScriptPath),
	}
	if got := spec.NewSessionCommand.Args; !reflect.DeepEqual(got, wantNewSession) {
		t.Fatalf("new-session args = %#v, want %#v", got, wantNewSession)
	}
	if got, want := spec.KillSessionCommand.Args, []string{"tmux", "-f", "/dev/null", "-L", socketName, "kill-session", "-t", spec.SessionName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kill-session args = %#v, want %#v", got, want)
	}

	script := agentLaunchScript(t)
	for _, want := range []string{
		"cd '/repo/worktree' || exit",
		"APPROACH_LAUNCH_ID='launch/tmux'",
		"APPROACH_PLAN_ID='plan-1'",
		"codex",
		"exec",
		"Read the plan and begin implementation.",
		"if [ -e " + shellQuote(spec.StatusPath) + " ]; then",
		"printf '%s\\n' \"$status\" > " + shellQuote(spec.StatusPath),
	} {
		requireScriptContains(t, script, want)
	}
	for _, blocked := range []string{"export TMUX=", "export ZELLIJ="} {
		if strings.Contains(script, blocked) {
			t.Fatalf("agent launch script should not inherit parent multiplexer env %q:\n%s", blocked, script)
		}
	}
	spec.Cleanup()
	if _, err := os.Stat(spec.ScriptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("script cleanup error = %v, want removed", err)
	}
	if _, err := os.Stat(spec.StatusPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status cleanup error = %v, want removed", err)
	}
}

func TestEmbeddedTmuxAgentCommandReportsMissingTmux(t *testing.T) {
	putAgentOnPath(t, "codex")
	_, err := embeddedTmuxAgentCommand(planAgentContext(), fakeLookPath())
	if !errors.Is(err, ErrEmbeddedTmuxUnavailable) {
		t.Fatalf("error = %v, want ErrEmbeddedTmuxUnavailable", err)
	}
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}

func TestAgentCommandSpecExportsPinnedControlPlaneEnvironment(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := planAgentContext()
	ctx.Executable = "/state/approach/sessions/v1/bin/approach-abc123"
	ctx.BuildVersion = "v0.10.3"
	ctx.DBSchemaVersion = 6

	_, overrides, err := agentCommandSpec(ctx)
	if err != nil {
		t.Fatalf("agentCommandSpec: %v", err)
	}
	env := map[string]string{}
	for _, override := range overrides {
		env[override.key] = override.value
	}
	want := map[string]string{
		"APPROACH_EXECUTABLE":    "/state/approach/sessions/v1/bin/approach-abc123",
		"APPROACH_BUILD_VERSION": "v0.10.3",
		"APPROACH_DB_SCHEMA":     "6",
	}
	for key, value := range want {
		if env[key] != value {
			t.Fatalf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestAgentCommandSpecOmitsUnsetControlPlaneEnvironment(t *testing.T) {
	putAgentOnPath(t, "codex")

	_, overrides, err := agentCommandSpec(planAgentContext())
	if err != nil {
		t.Fatalf("agentCommandSpec: %v", err)
	}
	for _, override := range overrides {
		switch override.key {
		case "APPROACH_EXECUTABLE", "APPROACH_BUILD_VERSION":
			if override.value != "" {
				t.Fatalf("%s = %q, want empty when no pin was supplied", override.key, override.value)
			}
		case "APPROACH_DB_SCHEMA":
			if override.value != "" {
				t.Fatalf("APPROACH_DB_SCHEMA = %q, want empty when no pin was supplied", override.value)
			}
		}
	}
}

// The session hook is baked into the agent's argv, so it must name the same
// binary APPROACH_EXECUTABLE does. A drift between the two is exactly the
// mixed-build split this pin exists to close.
func TestSessionHookCommandMatchesExportedExecutable(t *testing.T) {
	for _, command := range []string{"codex", "claude"} {
		t.Run(command, func(t *testing.T) {
			putAgentOnPath(t, command)
			ctx := planAgentContext()
			ctx.Command = command
			ctx.Executable = "/state/approach/sessions/v1/bin/approach-abc123"

			cmd, overrides, err := agentCommandSpec(ctx)
			if err != nil {
				t.Fatalf("agentCommandSpec: %v", err)
			}
			exported := ""
			for _, override := range overrides {
				if override.key == "APPROACH_EXECUTABLE" {
					exported = override.value
				}
			}
			if exported != ctx.Executable {
				t.Fatalf("APPROACH_EXECUTABLE = %q, want %q", exported, ctx.Executable)
			}
			argv := strings.Join(cmd.Args, " ")
			if !strings.Contains(argv, shellQuote(exported)+" session-hook") {
				t.Fatalf("argv does not embed the pinned session hook %q:\n%s", exported, argv)
			}
		})
	}
}

func TestSessionHookCommandFallsBackToRunningBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if got := approachSessionHookCommand("claude", ""); got != shellQuote(executable)+" session-hook --provider claude" {
		t.Fatalf("hook command = %q", got)
	}
}

func TestSessionHookCommandTreatsExecutablePathAsOneShellWord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("session hooks use a POSIX shell command")
	}
	tmp := t.TempDir()
	executable := filepath.Join(tmp, "approach $(touch pwned_hook) 'quoted'")
	argsPath := filepath.Join(tmp, "hook-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", approachSessionHookCommand("claude", executable))
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("session hook command failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "pwned_hook")); !os.IsNotExist(err) {
		t.Fatalf("command substitution escaped executable quoting: %v", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), "session-hook\n--provider\nclaude\n"; got != want {
		t.Fatalf("hook argv = %q, want %q", got, want)
	}
}

// The control endpoint is exported only when a launch was registered. Empty
// fields must leave the variables absent (not empty), because the CLI reads
// "set" as "proxy or fall back" and "absent" as "open the store directly".
func TestAgentCommandSpecExportsControlEndpointOnlyWhenRegistered(t *testing.T) {
	putAgentOnPath(t, "codex")
	t.Setenv("APPROACH_CONTROL_ENDPOINT", "/stale/parent.sock")
	t.Setenv("APPROACH_CONTROL_TOKEN", "stale")

	cmd, overrides, err := agentCommandSpec(planAgentContext())
	if err != nil {
		t.Fatalf("agentCommandSpec: %v", err)
	}
	for _, override := range overrides {
		if override.key == "APPROACH_CONTROL_ENDPOINT" || override.key == "APPROACH_CONTROL_TOKEN" {
			t.Fatalf("%s exported without a registration", override.key)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "APPROACH_CONTROL_ENDPOINT=") || strings.HasPrefix(entry, "APPROACH_CONTROL_TOKEN=") {
			t.Fatalf("stale parent value leaked into the agent environment: %s", entry)
		}
	}

	ctx := planAgentContext()
	ctx.ControlEndpoint = "/tmp/approach-501/deadbeef.sock"
	ctx.ControlToken = "0123456789abcdef"
	cmd, overrides, err = agentCommandSpec(ctx)
	if err != nil {
		t.Fatalf("agentCommandSpec: %v", err)
	}
	env := map[string]string{}
	for _, override := range overrides {
		env[override.key] = override.value
	}
	if env["APPROACH_CONTROL_ENDPOINT"] != ctx.ControlEndpoint || env["APPROACH_CONTROL_TOKEN"] != ctx.ControlToken {
		t.Fatalf("control env = %q / %q", env["APPROACH_CONTROL_ENDPOINT"], env["APPROACH_CONTROL_TOKEN"])
	}
	found := 0
	for _, entry := range cmd.Env {
		if entry == "APPROACH_CONTROL_ENDPOINT="+ctx.ControlEndpoint || entry == "APPROACH_CONTROL_TOKEN="+ctx.ControlToken {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("cmd.Env carries %d control entries, want 2", found)
	}
}
