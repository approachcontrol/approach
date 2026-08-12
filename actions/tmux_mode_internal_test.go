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
	if args[2] != repoTmuxLaunchScript {
		t.Fatal("the launch must run the shared tmux script")
	}
	wantArgs := []string{spec.SessionName, spec.WindowName, ctx.WorktreePath}
	if !strings.HasPrefix(strings.Join(args[4:], "\x00"), strings.Join(wantArgs, "\x00")) {
		t.Fatalf("script args = %#v, want prefix %#v", args[4:], wantArgs)
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
	ctx := tmuxModeContext()
	ctx.WorktreePath = t.TempDir()

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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tmuxModeContext()
			ctx.WorktreePath = t.TempDir()
			ctx.FlowPhaseKind = tc.phaseKind
			ctx.LaunchID = tc.launchID

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

// TestRepoTmuxLaunchWindowLiveMatchesTheLaunchsOwnWindow pins the pairing the
// liveness probe depends on: the window name a launch gets must be matchable
// back to that launch ID, and must not match a different launch's window.
func TestRepoTmuxLaunchWindowLiveMatchesTheLaunchsOwnWindow(t *testing.T) {
	putAgentOnPath(t, "codex")
	ctx := tmuxModeContext()
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
	ctx := tmuxModeContext()
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
