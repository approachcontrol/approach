package actions

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/approachcontrol/approach/agent"
)

// repoTmuxSessionPrefix keeps tmux-mode sessions disjoint from the per-worktree
// WorktreeSessionName sessions that default-backend external launches create on
// the same default server.
const repoTmuxSessionPrefix = "approach-"

// repoTmuxWindowIDLen bounds how much of the launch ID the window name carries.
// The launch ID's trailing random hex is what makes windows unique inside a
// session; the leading timestamp adds nothing but width.
const repoTmuxWindowIDLen = 8

// ErrRepoTmuxUnavailable reports that tmux mode cannot run this launch because
// tmux is not installed. Callers fall back to their default-backend route.
var ErrRepoTmuxUnavailable = errors.New("tmux is not available for tmux launch mode")

// RepoTmuxAgentSpec is a CLI agent launch that runs as a window in the repo's
// tmux session on the user's default tmux server.
type RepoTmuxAgentSpec struct {
	SessionName string
	WindowName  string
	// AttachCommand is the command to show the user so they can reach the
	// session from their own terminal.
	AttachCommand string
	Launch        TerminalLaunchSpec
}

// TmuxAvailable reports whether tmux mode can run launches right now.
func TmuxAvailable() bool {
	return commandExists("tmux", exec.LookPath)
}

// RepoAgentSessionName returns the tmux session name that holds every agent
// window for a repo. It is keyed on the repo, not the worktree, so all of a
// repo's Flows share one session.
func RepoAgentSessionName(repoPath string) string {
	return repoTmuxSessionPrefix + WorktreeSessionName(repoPath)
}

// RepoTmuxAttachCommand is the attach command shown to the user in status text.
func RepoTmuxAttachCommand(sessionName string) string {
	return "tmux attach -t " + shellQuote(sessionName)
}

// RepoTmuxAttachExistingShellCommand attaches to an existing session and fails
// when it is absent. It deliberately avoids `new-session -A`, which would
// create the session the caller is trying to report as missing.
func RepoTmuxAttachExistingShellCommand(sessionName string) string {
	return "tmux attach-session -t " + shellQuote(tmuxExactTarget(sessionName))
}

// RepoTmuxHasSessionCommand probes the default server for a repo's agent
// session. TMUX/ZELLIJ are stripped so a TUI running inside a multiplexer still
// asks the default server rather than its enclosing one.
func RepoTmuxHasSessionCommand(repoPath string) *exec.Cmd {
	cmd := exec.Command("tmux", "has-session", "-t", tmuxExactTarget(RepoAgentSessionName(repoPath)))
	cmd.Env = envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
	return cmd
}

// RepoTmuxSessionExists reports whether a repo's agent session is alive.
func RepoTmuxSessionExists(repoPath string) bool {
	if !TmuxAvailable() {
		return false
	}
	return RepoTmuxHasSessionCommand(repoPath).Run() == nil
}

// repoTmuxLaunchScript creates the repo session on demand and runs the agent in
// a new window of it. The has-session probe and new-session are not atomic, so
// a "duplicate session" loss to a near-simultaneous launch into the same repo
// retries as new-window rather than failing.
const repoTmuxLaunchScript = `
session=$1
window=$2
dir=$3
cmd=$4
if tmux has-session -t "=$session" 2>/dev/null; then
	exec tmux new-window -t "=$session" -n "$window" -c "$dir" "$cmd"
fi
if tmux new-session -d -s "$session" -n "$window" -c "$dir" "$cmd" 2>/dev/null; then
	exit 0
fi
exec tmux new-window -t "=$session" -n "$window" -c "$dir" "$cmd"
`

// RepoTmuxAgentLaunch builds a CLI agent launch that runs in the repo's tmux
// session. The agent itself runs from the same self-deleting script every other
// transport uses, so cwd, APPROACH_* exports, and provider hook wiring are
// identical to an embedded or external launch.
func RepoTmuxAgentLaunch(ctx AgentLaunchContext) (RepoTmuxAgentSpec, error) {
	return repoTmuxAgentLaunch(ctx, exec.LookPath)
}

func repoTmuxAgentLaunch(ctx AgentLaunchContext, lookPath lookPathFunc) (RepoTmuxAgentSpec, error) {
	if !commandExists("tmux", lookPath) {
		return RepoTmuxAgentSpec{}, ErrRepoTmuxUnavailable
	}
	command := agent.Normalize(ctx.Command)
	if command != agent.CommandCodex && command != agent.CommandClaude {
		return RepoTmuxAgentSpec{}, errors.New("tmux launch mode supports only CLI agents")
	}
	// The window is not an embedded slot: there is no dock to prefill, so the
	// initial prompt has to reach the agent as argv, and no stream-json
	// rendering applies. Both follow from Embedded being false.
	ctx.Embedded = false
	cmd, _, err := agentCommandSpec(ctx)
	if err != nil {
		return RepoTmuxAgentSpec{}, err
	}
	argv, err := resolvedCommandArgv(cmd)
	if err != nil {
		return RepoTmuxAgentSpec{}, err
	}
	sessionSource := ctx.RepoPath
	if sessionSource == "" {
		sessionSource = ctx.WorktreePath
	}
	if sessionSource == "" {
		sessionSource = cmd.Dir
	}
	sessionName := RepoAgentSessionName(sessionSource)
	windowName := repoTmuxWindowName(ctx)
	agentEnv := envWithoutKeys(cmd.Env, "TMUX", "ZELLIJ")
	termCommand, err := newTerminalCommand(cmd.Dir, agentEnv, argv, sessionName)
	if err != nil {
		return RepoTmuxAgentSpec{}, err
	}
	tmuxCmd := exec.Command("sh", "-c", repoTmuxLaunchScript, "approach", sessionName, windowName, cmd.Dir, termCommand.shellCommand())
	tmuxCmd.Env = envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
	return RepoTmuxAgentSpec{
		SessionName:   sessionName,
		WindowName:    windowName,
		AttachCommand: RepoTmuxAttachCommand(sessionName),
		Launch: TerminalLaunchSpec{
			Cmd: tmuxCmd,
			// The tmux command returns as soon as the window exists; the agent
			// keeps running there and provider hooks own its completion.
			Detached: true,
			Cleanup:  termCommand.cleanup,
		},
	}, nil
}

// repoTmuxWindowName labels the window by what it is running, suffixed with
// enough of the launch ID to stay unique inside the shared repo session.
func repoTmuxWindowName(ctx AgentLaunchContext) string {
	name := sanitizeSessionSuffix(ctx.FlowPhaseKind)
	if name == "" {
		name = sanitizeSessionSuffix(agent.Normalize(ctx.Command))
	}
	if name == "" {
		name = "agent"
	}
	if suffix := repoTmuxLaunchSuffix(ctx.LaunchID); suffix != "" {
		name += "-" + suffix
	}
	return name
}

func repoTmuxLaunchSuffix(launchID string) string {
	suffix := sanitizeSessionSuffix(launchID)
	if len(suffix) > repoTmuxWindowIDLen {
		suffix = suffix[len(suffix)-repoTmuxWindowIDLen:]
	}
	return strings.Trim(suffix, ".-")
}

// tmuxExactTarget pins a target to one exact session name so tmux's prefix
// matching cannot resolve it to a different session.
func tmuxExactTarget(sessionName string) string {
	return "=" + sessionName
}
