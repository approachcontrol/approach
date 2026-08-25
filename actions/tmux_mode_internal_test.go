package actions

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/internal/flowlease"
)

func tmuxModeContext(t *testing.T) AgentLaunchContext {
	t.Helper()
	ctx := planAgentContext()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := flowlease.ResolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx.SessionStateRoot = canonical
	ctx.RepoPath = "/repo"
	ctx.WorktreePath = "/repo/worktree"
	ctx.FlowID = "flow-1"
	ctx.FlowPhaseID = "phase-1"
	ctx.FlowPhaseKind = "implementation"
	ctx.FlowLaunchTracked = true
	return ctx
}

func TestRepoAgentSessionNameIsRepoScopedAndPrefixed(t *testing.T) {
	repo := t.TempDir()
	worktree := filepath.Join(repo, "worktree")

	name := RepoAgentSessionName(repo)
	if !strings.HasPrefix(name, "approach-") {
		t.Fatalf("expected approach- prefix, got %q", name)
	}
	if name == RepoAgentSessionName(worktree) {
		t.Fatal("expected repo and worktree session names to differ")
	}
	if name != RepoAgentSessionName(repo) {
		t.Fatal("expected repo session name to be stable")
	}
	// Default-backend external launches keep using the unprefixed per-worktree
	// names on the same default server; the two must never collide.
	if name == WorktreeSessionName(repo) {
		t.Fatalf("expected repo agent session name to differ from worktree session name, got %q", name)
	}
}

func TestRepoAgentSessionNamePreservesUnicodeForPrivateTransport(t *testing.T) {
	name := RepoAgentSessionName("/repos/café-东京")
	if _, err := flowlease.ExactWindowTarget(name, "implementation-deadbeef-12345678"); err != nil {
		t.Fatalf("RepoAgentSessionName() = %q is incompatible with the private tmux transport: %v", name, err)
	}
	if !strings.Contains(name, "café-东京") {
		t.Fatalf("RepoAgentSessionName() = %q, want established Unicode identity preserved", name)
	}
}

// TestRepoAgentSessionNameHasNoTmuxTargetSeparators pins the substitution that
// keeps dotted repo directories addressable. tmux reads "." and ":" in a target
// as session.pane and session:window, so a session named after
// github.io-style directory would exist but be unreachable: has-session and
// attach both fail with "can't find pane", and the attach command printed in
// every launch status fails when the user pastes it.
func TestRepoAgentSessionNameHasNoTmuxTargetSeparators(t *testing.T) {
	dotted := filepath.Join(t.TempDir(), "foo.github.io")
	if err := os.MkdirAll(dotted, 0o755); err != nil {
		t.Fatal(err)
	}

	name := RepoAgentSessionName(dotted)
	if strings.ContainsAny(name, ".:") {
		t.Fatalf("session name = %q, want no tmux target separators", name)
	}
	if !strings.Contains(name, "foo-github-io") {
		t.Fatalf("session name = %q, want the directory still recognizable", name)
	}
	// The hash keeps the substitution from merging two distinct repos.
	sibling := filepath.Join(filepath.Dir(dotted), "foo-github-io")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if name == RepoAgentSessionName(sibling) {
		t.Fatalf("foo.github.io and foo-github-io must not share session %q", name)
	}
}

func TestRepoTmuxAgentLaunchUnavailableWithoutTmux(t *testing.T) {
	_, err := repoTmuxAgentLaunch(tmuxModeContext(t), fakeLookPath())
	if !errors.Is(err, ErrRepoTmuxUnavailable) {
		t.Fatalf("expected ErrRepoTmuxUnavailable, got %v", err)
	}
}

func TestRepoTmuxAgentLaunchAcceptsCursorAgent(t *testing.T) {
	putAgentOnPath(t, "cursor-agent")
	t.Setenv("HOME", t.TempDir())
	ctx := tmuxModeContext(t)
	ctx.Command = "cursor-agent"
	ctx.WorktreePath = t.TempDir()

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()
	if spec.SessionName != RepoAgentSessionName("/repo") {
		t.Fatalf("session name = %q, want %q", spec.SessionName, RepoAgentSessionName("/repo"))
	}
}

func TestRepoTmuxAgentLaunchRejectsUnsupportedAgent(t *testing.T) {
	ctx := tmuxModeContext(t)
	ctx.Command = "gemini"
	_, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err == nil || !strings.Contains(err.Error(), "supports only CLI agents") {
		t.Fatalf("error = %v, want CLI-agent rejection", err)
	}
}

func TestRepoTmuxAgentLaunchCreatesOrReusesRepoSession(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	if spec.SessionName != RepoAgentSessionName("/repo") {
		t.Fatalf("session name = %q, want %q", spec.SessionName, RepoAgentSessionName("/repo"))
	}
	if spec.Launch.Interactive {
		t.Fatal("tmux-mode launch must not take over the TTY")
	}
	if !spec.Launch.Detached {
		t.Fatal("tmux-mode launch must be detached so hooks own completion")
	}
	if spec.AttachCommand != "tmux attach -t "+shellQuote(spec.SessionName) {
		t.Fatalf("attach command = %q", spec.AttachCommand)
	}
	if !strings.HasPrefix(spec.WindowName, "implementation-") {
		t.Fatalf("window name = %q, want an implementation- prefix", spec.WindowName)
	}

	args := spec.Launch.Cmd.Args
	if len(args) < 2 || args[1] != flowlease.TmuxSpawnCommand {
		t.Fatalf("expected private Flow tmux parent command, got %#v", args)
	}
	joined := strings.Join(args, "\x00")
	for _, want := range []string{spec.SessionName, spec.WindowName, ctx.WorktreePath, ctx.FlowID, ctx.FlowPhaseID, ctx.LaunchID} {
		if !strings.Contains(joined, want) {
			t.Fatalf("private spawn args = %#v, want %q", args, want)
		}
	}
}

func TestRepoTmuxAgentLaunchSupportsCursor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	putAgentOnPath(t, "cursor-agent")
	ctx := tmuxModeContext(t)
	ctx.Command = "cursor-agent"
	ctx.WorktreePath = t.TempDir()
	ctx.FlowLaunchTracked = false

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	script, err := os.ReadFile(launchScriptPathFromArgs(t, spec.Launch.Cmd.Args))
	if err != nil {
		t.Fatalf("read launch script: %v", err)
	}
	if !strings.Contains(string(script), "cursor-agent") {
		t.Fatalf("launch script does not run cursor-agent:\n%s", script)
	}
}

// tmuxStub puts a fake tmux on PATH that records every invocation and fails the
// subcommands named in fail. It lets the launch script's branching be tested for
// what it does rather than for what its source text contains.
func tmuxStub(t *testing.T, fail ...string) (dir string, invocations func() []string) {
	t.Helper()
	dir = t.TempDir()
	log := filepath.Join(dir, "invocations")
	failing := strings.Join(fail, " ")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(log) + "\nfor bad in " + failing + "; do\n" +
		"  if [ \"$1\" = \"$bad\" ]; then\n    echo \"tmux: $1 refused\" >&2\n    exit 1\n  fi\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write tmux stub: %v", err)
	}
	return dir, func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

func runLaunchScript(t *testing.T, stubDir string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", repoTmuxLaunchScript, "approach", "sess", "win", stubDir, "agent")
	cmd.Env = append(os.Environ(), "PATH="+stubDir)
	var stderr boundedBuffer
	stderr.limit = repoTmuxStderrLimit
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// TestRepoTmuxLaunchScriptCreatesReusesAndRetries executes the script against a
// stub tmux. Both orderings race against a concurrent launch into the same repo,
// and the retry each race needs is the other command — behavior that only shows
// up when the script actually runs.
func TestRepoTmuxLaunchScriptCreatesReusesAndRetries(t *testing.T) {
	tests := []struct {
		name string
		fail []string
		want []string
	}{
		{
			// The session exists, so the launch is a window in it and the script
			// must never reach new-session.
			name: "reuses an existing session",
			fail: nil,
			want: []string{"has-session -t =sess", "new-window -d -t =sess: -n win -c DIR agent"},
		},
		{
			name: "creates the session when absent",
			fail: []string{"has-session"},
			want: []string{"has-session -t =sess", "new-session -d -s sess -n win -c DIR agent"},
		},
		{
			// The session died between the probe and new-window: retry as a
			// creation rather than failing the launch.
			name: "retries as new-session when the session dies after the probe",
			fail: []string{"new-window"},
			want: []string{
				"has-session -t =sess",
				"new-window -d -t =sess: -n win -c DIR agent",
				"new-session -d -s sess -n win -c DIR agent",
			},
		},
		{
			// A concurrent launch won the creation race: retry as a window in
			// the session that launch just made.
			name: "retries as new-window when creation loses the race",
			fail: []string{"has-session", "new-session"},
			want: []string{
				"has-session -t =sess",
				"new-session -d -s sess -n win -c DIR agent",
				"new-window -d -t =sess: -n win -c DIR agent",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubDir, invocations := tmuxStub(t, tc.fail...)
			_, err := runLaunchScript(t, stubDir)
			if err != nil {
				t.Fatalf("launch script failed: %v", err)
			}
			var want []string
			for _, line := range tc.want {
				want = append(want, strings.ReplaceAll(line, "DIR", stubDir))
			}
			if got := invocations(); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("tmux invocations = %#v, want %#v", got, want)
			}
		})
	}
}

// TestRepoTmuxLaunchScriptReportsTheFinalFailure keeps a failed spawn
// diagnosable. Every attempt but the last suppresses stderr, so exactly one
// message reaches the caller; without it the launch reduces to "exit status 1",
// which is what a Flow persists into the phase's needs_attention note.
func TestRepoTmuxLaunchScriptReportsTheFinalFailure(t *testing.T) {
	stubDir, _ := tmuxStub(t, "has-session", "new-session", "new-window")
	stderr, err := runLaunchScript(t, stubDir)
	if err == nil {
		t.Fatal("expected the launch script to fail when every tmux attempt fails")
	}
	if strings.Count(stderr, "refused") != 1 {
		t.Fatalf("expected exactly the final attempt's stderr, got %q", stderr)
	}
	if !strings.Contains(stderr, "new-window refused") {
		t.Fatalf("expected the final new-window's message, got %q", stderr)
	}
}

// TestRepoTmuxAgentLaunchCapturesSpawnStderr pins the wiring that carries that
// message out of the transport: without ErrorDetail the script's stderr goes to
// /dev/null and every tmux failure reads as a bare exit status.
func TestRepoTmuxAgentLaunchCapturesSpawnStderr(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.FlowLaunchTracked = false

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	if spec.Launch.ErrorDetail == nil {
		t.Fatal("a tmux launch must expose its spawn diagnostic")
	}
	if spec.Launch.Cmd.Stderr == nil {
		t.Fatal("a tmux launch must capture stderr, or the diagnostic is always empty")
	}
	stubDir, _ := tmuxStub(t, "has-session", "new-session", "new-window")
	spec.Launch.Cmd.Env = append(spec.Launch.Cmd.Env, "PATH="+stubDir)
	if err := spec.Launch.Cmd.Run(); err == nil {
		t.Fatal("expected the stubbed launch to fail")
	}
	if detail := spec.Launch.ErrorDetail(); !strings.Contains(detail, "refused") {
		t.Fatalf("ErrorDetail() = %q, want the failing tmux invocation's message", detail)
	}
}

// TestBoundedBufferTruncates keeps a runaway diagnostic out of a persisted Flow
// note while still reporting a full write, so the child never sees a short write.
func TestBoundedBufferTruncates(t *testing.T) {
	buf := &boundedBuffer{limit: 8}
	n, err := buf.Write([]byte("abcdefghij"))
	if err != nil || n != 10 {
		t.Fatalf("Write = (%d, %v), want (10, nil)", n, err)
	}
	if _, err := buf.Write([]byte("klm")); err != nil {
		t.Fatalf("Write after limit returned %v", err)
	}
	if got := buf.String(); got != "abcdefgh" {
		t.Fatalf("String() = %q, want %q", got, "abcdefgh")
	}
}

func TestRepoTmuxAgentLaunchStripsMultiplexerEnv(t *testing.T) {
	putAgentOnPath(t, "codex")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("ZELLIJ", "0")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	for _, entry := range spec.Launch.Cmd.Env {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "ZELLIJ=") {
			t.Fatalf("tmux client env must not carry %q", entry)
		}
	}
	script := agentLaunchScript(t)
	if strings.Contains(script, "export TMUX=") || strings.Contains(script, "export ZELLIJ=") {
		t.Fatalf("agent script must not re-export multiplexer env:\n%s", script)
	}
}

func TestRepoTmuxAgentLaunchDeliversPromptInArgv(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.InitialPrompt = "Implement the plan."

	// An embedded tracked-phase launch would prefill the prompt into the dock
	// instead of argv. The tmux window has no dock, so argv is the only
	// delivery that works and ShouldPrefillEmbeddedPrompt must stay false.
	embedded := ctx
	embedded.Embedded = true
	if !ShouldPrefillEmbeddedPrompt(embedded) {
		t.Fatal("expected the embedded equivalent of this context to prefill; the assertion below is otherwise vacuous")
	}

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	script := agentLaunchScript(t)
	requireScriptContains(t, script, shellQuote("Implement the plan."))
	requireScriptContains(t, script, "export APPROACH_FLOW_ID="+shellQuote("flow-1"))
	requireScriptContains(t, script, "cd "+shellQuote(ctx.WorktreePath))
}

func TestRepoTmuxAgentLaunchWindowNamesAreUniquePerLaunch(t *testing.T) {
	putAgentOnPath(t, "codex")
	first := tmuxModeContext(t)
	first.WorktreePath = t.TempDir()
	first.LaunchID = "approach-1700000000000000000-aabbccddeeff"
	second := first
	second.LaunchID = "approach-1700000000000000001-112233445566"

	firstSpec, err := repoTmuxAgentLaunch(first, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer firstSpec.Launch.Cleanup()
	secondSpec, err := repoTmuxAgentLaunch(second, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer secondSpec.Launch.Cleanup()

	if firstSpec.SessionName != secondSpec.SessionName {
		t.Fatal("expected both launches to share the repo session")
	}
	if firstSpec.WindowName == secondSpec.WindowName {
		t.Fatalf("expected distinct window names, got %q twice", firstSpec.WindowName)
	}
}

func TestRepoTmuxTrackedLaunchNamesStayUniqueWhenLaunchSuffixesCollide(t *testing.T) {
	putAgentOnPath(t, "codex")
	first := tmuxModeContext(t)
	first.WorktreePath = t.TempDir()
	first.LaunchID = "approach-1700000000000000000-aabbccdd"
	second := first
	second.LaunchID = "another-1700000000000000001-aabbccdd"

	firstSpec, err := repoTmuxAgentLaunch(first, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("first launch error: %v", err)
	}
	defer firstSpec.Launch.Cleanup()
	secondSpec, err := repoTmuxAgentLaunch(second, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("second launch error: %v", err)
	}
	defer secondSpec.Launch.Cleanup()

	if firstSpec.WindowName == secondSpec.WindowName {
		t.Fatalf("collision-bearing tracked windows both = %q", firstSpec.WindowName)
	}
	for _, name := range []string{firstSpec.WindowName, secondSpec.WindowName} {
		if !strings.HasSuffix(name, "aabbccdd") {
			t.Fatalf("window %q no longer preserves the launch-ID suffix probe", name)
		}
	}
}

func TestRepoTmuxTrackedLaunchRejectsMissingLeaseIdentity(t *testing.T) {
	putAgentOnPath(t, "codex")
	tests := []struct {
		name   string
		mutate func(*AgentLaunchContext)
	}{
		{name: "flow id", mutate: func(ctx *AgentLaunchContext) { ctx.FlowID = "" }},
		{name: "phase id", mutate: func(ctx *AgentLaunchContext) { ctx.FlowPhaseID = "" }},
		{name: "launch id", mutate: func(ctx *AgentLaunchContext) { ctx.LaunchID = "../bad" }},
		{name: "state root", mutate: func(ctx *AgentLaunchContext) { ctx.SessionStateRoot = "relative" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tmuxModeContext(t)
			ctx.WorktreePath = t.TempDir()
			tc.mutate(&ctx)
			if _, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux")); err == nil {
				t.Fatal("tracked launch error = nil, want hard lease validation failure")
			}
		})
	}
}

func TestRepoTmuxTrackedLaunchRejectsIncompatibleRoles(t *testing.T) {
	putAgentOnPath(t, "codex")
	tests := []struct {
		name   string
		mutate func(*AgentLaunchContext)
	}{
		{name: "auto launch", mutate: func(ctx *AgentLaunchContext) { ctx.FlowAutoLaunch = true }},
		{name: "headless", mutate: func(ctx *AgentLaunchContext) { ctx.Headless = true }},
		{name: "repair", mutate: func(ctx *AgentLaunchContext) { ctx.FlowRepair = true }},
		{name: "generic Flow agent", mutate: func(ctx *AgentLaunchContext) { ctx.FlowAgent = true }},
		{name: "saved session resume", mutate: func(ctx *AgentLaunchContext) { ctx.FlowSavedSessionResume = true }},
		{name: "autofix", mutate: func(ctx *AgentLaunchContext) { ctx.FlowAutofix = true }},
		{name: "autofix PR", mutate: func(ctx *AgentLaunchContext) { ctx.FlowAutofixPRNumber = 42 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tmuxModeContext(t)
			ctx.WorktreePath = t.TempDir()
			tc.mutate(&ctx)
			if _, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux")); err == nil ||
				!strings.Contains(err.Error(), "invalid tracked Flow tmux launch role") {
				t.Fatalf("tracked launch error = %v, want incompatible role rejection", err)
			}
		})
	}
}

func TestRepoTmuxTrackedLaunchRejectsSelfExecutableResolutionFailure(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	_, err := repoTmuxAgentLaunchWithExecutable(ctx, fakeLookPath("tmux"), func() (string, error) {
		return "", errors.New("executable unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "executable unavailable") {
		t.Fatalf("launch error = %v, want executable resolution failure", err)
	}
}

// TestRepoTmuxTrackedLaunchPrefersPinnedExecutable is the upgrade-safety
// contract for the private lease helpers. os.Executable names the mutable
// installation path; after brew/cask replace that file, a long-lived TUI still
// holds a verified pin in ctx.Executable. Both __flow-tmux-spawn and
// __flow-lease-run must exec that pin, or a protocol-incompatible replacement
// build can hang the handoff.
func TestRepoTmuxTrackedLaunchPrefersPinnedExecutable(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.Executable = "/state/approach/sessions/v1/bin/approach-abc123"
	fallbackCalled := false
	spec, err := repoTmuxAgentLaunchWithExecutable(ctx, fakeLookPath("tmux"), func() (string, error) {
		fallbackCalled = true
		return "/opt/homebrew/bin/approach", nil
	})
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunchWithExecutable returned error: %v", err)
	}
	defer spec.Launch.Cleanup()
	if fallbackCalled {
		t.Fatal("pinned launch must not resolve os.Executable")
	}
	requireTrackedLeaseHelpersUseExecutable(t, spec, ctx.Executable)
}

func TestRepoTmuxTrackedLaunchFallsBackToRunningBinaryWhenUnpinned(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	const running = "/tmp/approach-running-unpinned"
	spec, err := repoTmuxAgentLaunchWithExecutable(ctx, fakeLookPath("tmux"), func() (string, error) {
		return running, nil
	})
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunchWithExecutable returned error: %v", err)
	}
	defer spec.Launch.Cleanup()
	requireTrackedLeaseHelpersUseExecutable(t, spec, running)
}

func TestRepoTmuxTrackedLaunchRejectsRelativePinnedExecutable(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.Executable = "approach-abc123"
	_, err := repoTmuxAgentLaunchWithExecutable(ctx, fakeLookPath("tmux"), func() (string, error) {
		t.Fatal("relative pin must not fall back to os.Executable")
		return "/opt/homebrew/bin/approach", nil
	})
	if err == nil || !strings.Contains(err.Error(), "pinned Approach executable path must be absolute") {
		t.Fatalf("launch error = %v, want absolute pinned-path rejection", err)
	}
}

func requireTrackedLeaseHelpersUseExecutable(t *testing.T, spec RepoTmuxAgentSpec, want string) {
	t.Helper()
	args := spec.Launch.Cmd.Args
	if len(args) < 2 || args[0] != want || args[1] != flowlease.TmuxSpawnCommand {
		t.Fatalf("spawn argv = %#v, want %s %s ...", args, want, flowlease.TmuxSpawnCommand)
	}
	scriptPath := privateArgValue(t, args, "--script")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read lease-run script: %v", err)
	}
	wantLease := shellQuote(want) + " " + shellQuote(flowlease.LeaseRunCommand)
	if !strings.Contains(string(script), wantLease) {
		t.Fatalf("lease-run script missing %q:\n%s", wantLease, script)
	}
}

// TestRepoTmuxWindowNameNeverEmpty pins the fallback chain. tmux names an
// unnamed window after its command, so an empty -n would silently rename the
// window to the launch script's path.
func TestRepoTmuxWindowNameNeverEmpty(t *testing.T) {
	putAgentOnPath(t, "codex")
	tests := []struct {
		name          string
		phaseKind     string
		launchID      string
		wantPrefix    string
		wantNoTrailer bool
	}{
		{name: "kind and launch id", phaseKind: "implementation", launchID: "approach-1700000000000000000-aabbccddeeff", wantPrefix: "implementation-"},
		// No phase kind: a worktree launch falls back to the agent name.
		{name: "no phase kind", launchID: "approach-1700000000000000000-aabbccddeeff", wantPrefix: "codex-"},
		// Nothing sanitizable in the launch ID, so the name carries no suffix.
		{name: "unusable launch id", phaseKind: "review", launchID: "...", wantPrefix: "review", wantNoTrailer: true},
		{name: "no launch id", phaseKind: "review", wantPrefix: "review", wantNoTrailer: true},
		{name: "unicode phase kind", phaseKind: "café", launchID: "...", wantPrefix: "café", wantNoTrailer: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tmuxModeContext(t)
			ctx.WorktreePath = t.TempDir()
			ctx.FlowPhaseKind = tc.phaseKind
			ctx.LaunchID = tc.launchID
			ctx.FlowLaunchTracked = false

			spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
			if err != nil {
				t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
			}
			defer spec.Launch.Cleanup()

			if spec.WindowName == "" {
				t.Fatal("window name must never be empty")
			}
			if !strings.HasPrefix(spec.WindowName, tc.wantPrefix) {
				t.Fatalf("window name = %q, want prefix %q", spec.WindowName, tc.wantPrefix)
			}
			if tc.wantNoTrailer && spec.WindowName != tc.wantPrefix {
				t.Fatalf("window name = %q, want exactly %q with no dangling separator", spec.WindowName, tc.wantPrefix)
			}
		})
	}
}

func TestRepoTmuxAttachExistingShellCommandDoesNotCreate(t *testing.T) {
	cmd := RepoTmuxAttachExistingShellCommand("approach-repo-0000")
	if strings.Contains(cmd, "new-session") {
		t.Fatalf("attach-existing must never create a session, got %q", cmd)
	}
	if !strings.Contains(cmd, shellQuote("=approach-repo-0000")) {
		t.Fatalf("expected an exact-match target, got %q", cmd)
	}
}

func TestRepoTmuxHasSessionCommandProbesDefaultServerExactly(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	cmd := tmuxProbeCommand(context.Background(), repoTmuxHasSessionArgs("/repo")...)
	want := []string{"tmux", "has-session", "-t", "=" + RepoAgentSessionName("/repo")}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	// An isolated -L socket would hide these sessions from the user's own
	// `tmux ls`, which is the point of tmux mode.
	for _, arg := range cmd.Args {
		if arg == "-L" || arg == "-f" {
			t.Fatalf("probe must use the default tmux server, got %#v", cmd.Args)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatal("probe must not inherit TMUX")
		}
	}
}

func TestRepoTmuxListWindowsCommandProbesDefaultServerExactly(t *testing.T) {
	cmd := tmuxProbeCommand(context.Background(), repoTmuxListWindowsArgs("/repo")...)
	want := []string{"tmux", "list-windows", "-t", "=" + RepoAgentSessionName("/repo") + ":", "-F", "#{window_name} #{pane_dead}"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	for _, arg := range cmd.Args {
		if arg == "-L" || arg == "-f" {
			t.Fatalf("probe must use the default tmux server, got %#v", cmd.Args)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatal("probe must not inherit TMUX")
		}
	}
}

// TestLaunchWindowRunningInListingIgnoresDeadWindows is the probe's parsing
// contract. `remain-on-exit on` keeps a finished window listed with its name
// intact, and reading that as a live agent would refuse reset and resume for a
// phase whose agent exited long ago — with no way out but killing the window by
// hand. Real `list-windows` output for that case is the second row below.
func TestLaunchWindowRunningInListingIgnoresDeadWindows(t *testing.T) {
	suffixes := []string{"aabbccdd"}
	tests := []struct {
		name    string
		listing string
		want    bool
	}{
		{name: "no windows", listing: ""},
		{name: "running window", listing: "implementation-aabbccdd 0\n", want: true},
		{name: "retained dead window", listing: "implementation-aabbccdd 1\n"},
		{
			name:    "another launch is running",
			listing: "implementation-11223344 0\nreview-aabbccdd 1\n",
		},
		{
			name:    "this launch runs alongside a dead one",
			listing: "implementation-11223344 1\nimplementation-aabbccdd 0\n",
			want:    true,
		},
		// A missing or unparsable liveness field must not invent a live window.
		{name: "name only", listing: "implementation-aabbccdd\n"},
		{name: "unparsable field", listing: "implementation-aabbccdd ?\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := launchWindowRunningInListing(tc.listing, suffixes); got != tc.want {
				t.Fatalf("launchWindowRunningInListing(%q) = %t, want %t", tc.listing, got, tc.want)
			}
		})
	}
}

// TestLaunchWindowRunningInListingMatchesAnyOfAPhasesLaunches covers what the
// multi-launch probe exists for: an earlier launch's window can outlive a later
// one that already exited, so asking about the newest launch alone would report
// no live agent while one is still running.
func TestLaunchWindowRunningInListingMatchesAnyOfAPhasesLaunches(t *testing.T) {
	const listing = "implementation-11111111 0\nimplementation-22222222 1\n"
	if !launchWindowRunningInListing(listing, []string{"22222222", "11111111"}) {
		t.Fatal("a live window for an earlier launch must still count as a live agent")
	}
	if launchWindowRunningInListing(listing, []string{"22222222"}) {
		t.Fatal("the newest launch alone is dead here")
	}
	// An empty launch ID sanitizes away rather than widening the match to every
	// window; otherwise a phase with one blank launch ID would never be resettable.
	if launchWindowRunningInListing(listing, repoTmuxLaunchSuffixes([]string{"", "   "})) {
		t.Fatal("blank launch IDs must not match any window")
	}
}

func TestTmuxLaunchProbeConfirmsAbsenceOnlyForMissingSessionOrServer(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{stderr: "can't find session: approach-alpha-1234", want: true},
		{stderr: "no server running on /tmp/tmux-501/default", want: true},
		{stderr: "error connecting to /tmp/tmux-501/default (Permission denied)"},
		{stderr: "probe timed out"},
		{stderr: ""},
	}
	for _, tc := range tests {
		if got := tmuxLaunchProbeConfirmsAbsence(tc.stderr); got != tc.want {
			t.Fatalf("tmuxLaunchProbeConfirmsAbsence(%q) = %t, want %t", tc.stderr, got, tc.want)
		}
	}
}

// TestRepoTmuxLaunchWindowLiveMatchesTheLaunchsOwnWindow pins the pairing the
// liveness probe depends on: the window name a launch gets must be matchable
// back to that launch ID, and must not match a different launch's window.
func TestRepoTmuxLaunchWindowLiveMatchesTheLaunchsOwnWindow(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.LaunchID = "approach-1700000000000000000-aabbccddeeff"

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	defer spec.Launch.Cleanup()

	suffix := repoTmuxLaunchSuffix(ctx.LaunchID)
	if suffix == "" {
		t.Fatal("expected a matchable launch suffix")
	}
	if !strings.HasSuffix(spec.WindowName, suffix) {
		t.Fatalf("window %q must end with the launch suffix %q, or the probe cannot match it", spec.WindowName, suffix)
	}
	if other := repoTmuxLaunchSuffix("approach-1700000000000000001-112233445566"); strings.HasSuffix(spec.WindowName, other) {
		t.Fatalf("window %q must not match another launch's suffix %q", spec.WindowName, other)
	}
	// No suffix to match on means the probe cannot claim a window is live.
	if RepoTmuxLaunchWindowLive("/repo", "...") {
		t.Fatal("an unmatchable launch ID must not report a live window")
	}
}

func TestRepoTmuxAgentLaunchCleanupRemovesScript(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}

	// Asserted on this spec's own script path: globbing all of TempDir would
	// fail on any concurrent test's or stale process's script.
	script := launchScriptPathFromArgs(t, spec.Launch.Cmd.Args)
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("launch script should exist before cleanup: %v", err)
	}
	spec.Launch.Cleanup()
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatalf("cleanup should have removed %s, stat err = %v", script, err)
	}
}

func TestRepoTmuxTrackedLaunchCleanupReportsCancellationFailure(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	handoffDir := privateArgValue(t, spec.Launch.Cmd.Args, "--handoff")
	if err := os.Mkdir(filepath.Dir(handoffDir), 0o700); err != nil {
		t.Fatalf("Mkdir(handoff collection) error = %v", err)
	}
	if err := os.Mkdir(handoffDir, 0o700); err != nil {
		t.Fatalf("Mkdir(handoff attempt) error = %v", err)
	}
	stubDir, _ := tmuxStub(t, "kill-window")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	spec.Launch.Cleanup()
	if detail := spec.Launch.ErrorDetail(); !strings.Contains(detail, "cancel exact tmux window") {
		t.Fatalf("ErrorDetail() = %q, want cancellation failure", detail)
	}
}

func privateArgValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	t.Fatalf("private argument %s not found in %#v", name, args)
	return ""
}

// launchScriptPathFromArgs pulls the self-deleting launch script's path out of
// the tmux command the spec runs.
func launchScriptPathFromArgs(t *testing.T, args []string) string {
	t.Helper()
	pattern := regexp.MustCompile(`/[^\s'"]*approach-agent-[^\s'"]*\.sh`)
	for _, arg := range args {
		if match := pattern.FindString(arg); match != "" {
			return match
		}
	}
	t.Fatalf("no launch script path in %#v", args)
	return ""
}

func TestRepoTmuxListClientsCommandProbesDefaultServerExactly(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	cmd := tmuxProbeCommand(context.Background(), repoTmuxListClientsArgs("/repo")...)
	want := []string{"tmux", "list-clients", "-t", "=" + RepoAgentSessionName("/repo"), "-F", "#{client_name}"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	for _, arg := range cmd.Args {
		if arg == "-L" || arg == "-f" {
			t.Fatalf("probe must use the default tmux server, got %#v", cmd.Args)
		}
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatal("probe must not inherit TMUX")
		}
	}
}

// TestSessionAttachedInListingReadsClientRows is the parse half of the attach
// probe. Blank output is tmux's answer for a session nobody is watching, and
// reading it as attached would suppress the terminal window forever.
func TestSessionAttachedInListingReadsClientRows(t *testing.T) {
	tests := []struct {
		name    string
		listing string
		want    bool
	}{
		{name: "no clients", listing: ""},
		{name: "blank lines only", listing: "\n   \n"},
		{name: "one client", listing: "/dev/ttys004\n", want: true},
		{name: "several clients", listing: "/dev/ttys004\n/dev/ttys007\n", want: true},
		{name: "leading blank line", listing: "\n/dev/ttys004\n", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionAttachedInListing(tc.listing); got != tc.want {
				t.Fatalf("sessionAttachedInListing(%q) = %t, want %t", tc.listing, got, tc.want)
			}
		})
	}
}

func TestInsideMultiplexerReadsTmuxAndZellij(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "bare shell", env: map[string]string{}},
		{name: "empty vars", env: map[string]string{"TMUX": "", "ZELLIJ": ""}},
		{name: "inside tmux", env: map[string]string{"TMUX": "/tmp/tmux-1000/default,123,0"}, want: true},
		{name: "inside zellij", env: map[string]string{"ZELLIJ": "0"}, want: true},
		{name: "inside both", env: map[string]string{"TMUX": "/tmp/x,1,0", "ZELLIJ": "0"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := insideMultiplexer(func(key string) string { return tc.env[key] })
			if got != tc.want {
				t.Fatalf("insideMultiplexer(%v) = %t, want %t", tc.env, got, tc.want)
			}
		})
	}
}

// TestAgentLaunchClearsEmbeddedForTheExternalWindow is the external-terminal
// twin of TestRepoTmuxAgentLaunchDeliversPromptInArgv. An external window is
// not an embedded slot either: there is no dock to prefill and no alt screen to
// suppress, so the seam clears Embedded and both rules fall out of that.
func TestAgentLaunchClearsEmbeddedForTheExternalWindow(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext(t)
	ctx.WorktreePath = t.TempDir()
	ctx.InitialPrompt = "Implement the plan."
	ctx.Embedded = true

	// Without the clear this context prefills instead of putting the prompt on
	// argv, so the assertions below would be vacuous.
	if !ShouldPrefillEmbeddedPrompt(ctx) {
		t.Fatal("expected this context to prefill while Embedded; the assertions below are otherwise vacuous")
	}

	launch, err := agentLaunchWithOptions(ctx, "linux", fakeGetenv(map[string]string{"TERMINAL": "alacritty"}), fakeLookPath("alacritty"), LaunchOptions{})
	if err != nil {
		t.Fatalf("agentLaunchWithOptions returned error: %v", err)
	}
	defer launch.Cleanup()

	script := agentLaunchScript(t)
	requireScriptContains(t, script, shellQuote("Implement the plan."))
	if strings.Contains(script, "--no-alt-screen") {
		t.Fatalf("an external window has an alt screen; the launch must not suppress it:\n%s", script)
	}
}
