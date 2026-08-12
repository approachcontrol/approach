package actions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmuxModeContext() AgentLaunchContext {
	ctx := planAgentContext()
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

func TestRepoTmuxAgentLaunchUnavailableWithoutTmux(t *testing.T) {
	_, err := repoTmuxAgentLaunch(tmuxModeContext(), fakeLookPath())
	if !errors.Is(err, ErrRepoTmuxUnavailable) {
		t.Fatalf("expected ErrRepoTmuxUnavailable, got %v", err)
	}
}

func TestRepoTmuxAgentLaunchRejectsNonCLIAgents(t *testing.T) {
	ctx := tmuxModeContext()
	ctx.Command = "codex-app"
	if _, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux")); err == nil {
		t.Fatal("expected codex-app to be rejected by the tmux route")
	}
}

func TestRepoTmuxAgentLaunchCreatesOrReusesRepoSession(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext()
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
	if len(args) < 4 || args[0] != "sh" || args[1] != "-c" {
		t.Fatalf("expected sh -c script, got %#v", args)
	}
	script := args[2]
	for _, want := range []string{
		`tmux has-session -t "=$session"`,
		`tmux new-window -t "=$session" -n "$window" -c "$dir" "$cmd"`,
		`tmux new-session -d -s "$session" -n "$window" -c "$dir" "$cmd"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("tmux script missing %q:\n%s", want, script)
		}
	}
	// The duplicate-session retry: new-window appears both as the reuse branch
	// and as the fallback after a lost creation race.
	if got := strings.Count(script, "tmux new-window"); got != 2 {
		t.Fatalf("expected two new-window invocations (reuse + race retry), got %d:\n%s", got, script)
	}
	wantArgs := []string{spec.SessionName, spec.WindowName, ctx.WorktreePath}
	if !strings.HasPrefix(strings.Join(args[4:], "\x00"), strings.Join(wantArgs, "\x00")) {
		t.Fatalf("script args = %#v, want prefix %#v", args[4:], wantArgs)
	}
}

func TestRepoTmuxAgentLaunchStripsMultiplexerEnv(t *testing.T) {
	putAgentOnPath(t, "codex")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("ZELLIJ", "0")
	ctx := tmuxModeContext()
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
	ctx := tmuxModeContext()
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
	first := tmuxModeContext()
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
	cmd := RepoTmuxHasSessionCommand("/repo")
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

func TestRepoTmuxAgentLaunchCleanupRemovesScript(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext()
	ctx.WorktreePath = t.TempDir()

	spec, err := repoTmuxAgentLaunch(ctx, fakeLookPath("tmux"))
	if err != nil {
		t.Fatalf("repoTmuxAgentLaunch returned error: %v", err)
	}
	spec.Launch.Cleanup()

	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "approach-agent-*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected cleanup to remove the launch script, found %#v", matches)
	}
}
