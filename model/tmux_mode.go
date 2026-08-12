package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
)

// tmuxFallbackNote is appended to a launch status when tmux mode wanted the
// tmux route and could not take it. It is set only for that one case: the
// embedded backend and the headless design exclusion are not fallbacks.
const tmuxFallbackNote = "tmux unavailable — launched in embedded terminal"

// tmuxExternalFallbackNote is the same fallback reported by the launch paths
// whose default-backend route opens an external terminal instead.
const tmuxExternalFallbackNote = "tmux unavailable — launched in a terminal session"

func normalizeLaunchBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case config.LaunchBackendTmux:
		return config.LaunchBackendTmux
	default:
		return config.LaunchBackendEmbedded
	}
}

// tmuxLaunchBackend reports whether the user opted into tmux mode. It says
// nothing about whether tmux is actually installed.
func (m Model) tmuxLaunchBackend() bool {
	return m.launchBackend == config.LaunchBackendTmux
}

func (m Model) tmuxAvailable() bool {
	if m.tmuxLaunchAvailable == nil {
		return actions.TmuxAvailable()
	}
	return m.tmuxLaunchAvailable()
}

// tmuxRouteEligible answers whether this launch belongs in a per-repo tmux
// window: tmux mode is on, the agent is a CLI agent, and the launch is
// interactive. Headless launches stay embedded — `claude --print` buffers all
// output until completion, so a self-closing tmux window would show nothing and
// then discard it; the TUI-side renderer is the only readable surface for them.
func tmuxRouteEligible(command string, headless bool) bool {
	switch agent.Normalize(command) {
	case agent.CommandCodex, agent.CommandClaude:
		return !headless
	default:
		return false
	}
}

// tmuxLaunchRoute reports whether this launch takes the tmux route, and whether
// declining it is a fallback the user should be told about. wantsTmux is true
// exactly when the tmux route was eligible but tmux is missing.
func (m Model) tmuxLaunchRoute(command string, headless bool) (route bool, fellBack bool) {
	if !m.tmuxLaunchBackend() || !tmuxRouteEligible(command, headless) {
		return false, false
	}
	if !m.tmuxAvailable() {
		return false, true
	}
	return true, false
}

// tmuxLaunchStatus names the window and session a launch landed in and gives
// the exact command to reach it from the user's own terminal.
func tmuxLaunchStatus(spec actions.RepoTmuxAgentSpec) string {
	return "Launched " + spec.WindowName + " in tmux session " + spec.SessionName + " — " + spec.AttachCommand
}

// withFallbackNote appends a fallback note to a status message.
func withFallbackNote(status, note string) string {
	if strings.TrimSpace(note) == "" {
		return status
	}
	if strings.TrimSpace(status) == "" {
		return note
	}
	return status + " (" + note + ")"
}

func (m Model) buildRepoTmuxAgentLaunch(ctx actions.AgentLaunchContext) (actions.RepoTmuxAgentSpec, error) {
	if m.launchRepoTmuxAgent == nil {
		return actions.RepoTmuxAgentLaunch(ctx)
	}
	return m.launchRepoTmuxAgent(ctx)
}

// launchAgentInRepoTmuxSession is the single spawn every tmux-mode route ends
// at. The window is not an embedded slot: the result is detached, provider
// hooks own completion, and the caller's reservation covers only the spawn.
func (m Model) launchAgentInRepoTmuxSession(ctx actions.AgentLaunchContext, release func()) (Model, tea.Cmd) {
	// The context may arrive with Embedded set by a pipeline that hardcodes it.
	// Clearing it is what routes InitialPrompt into argv instead of a dock
	// prefill that will never happen.
	ctx.Embedded = false
	spec, err := m.buildRepoTmuxAgentLaunch(ctx)
	if err != nil {
		releaseFlowLaunchReservation(release)
		return m.startFlowLaunchFailure(ctx, err.Error())
	}
	return m.runAgentLaunchWithStatus(ctx, spec.Launch, release, tmuxLaunchStatus(spec))
}

// launchAgentForBackend routes the launch paths that open an external terminal
// on the default backend: worktree `a`, new-worktree-with-agent, and plans-mode
// implement. In tmux mode those run as windows in the repo's tmux session
// instead; without tmux they keep today's external behavior and say so.
func (m Model) launchAgentForBackend(ctx actions.AgentLaunchContext, release func()) (Model, tea.Cmd) {
	route, fellBack := m.tmuxLaunchRoute(ctx.Command, ctx.Headless)
	if route {
		return m.launchAgentInRepoTmuxSession(ctx, release)
	}
	launchedStatus := ""
	if fellBack {
		launchedStatus = withFallbackNote(agentLaunchedStatus(ctx.Command), tmuxExternalFallbackNote)
	}
	return m.launchAgentWithContextStatus(ctx, release, launchedStatus)
}

// tmuxRepoSessionExists probes the default tmux server for a repo's session.
func (m Model) tmuxRepoSessionExists(repoPath string) bool {
	if strings.TrimSpace(repoPath) == "" {
		return false
	}
	if m.repoTmuxSessionExists == nil {
		return actions.RepoTmuxSessionExists(repoPath)
	}
	return m.repoTmuxSessionExists(repoPath)
}

// tmuxModeAttachAvailable reports whether the T affordance is worth offering.
// It reads a value resolved once at startup rather than probing: this is called
// on every render, and neither the backend nor tmux's presence on PATH changes
// within a session in a way worth a per-frame PATH scan.
func (m Model) tmuxModeAttachAvailable() bool {
	return m.tmuxAttachHint
}

// handleAttachRepoTmuxSession opens the configured external terminal attached
// to the selected repo's agent session. A missing session is an error status,
// never a silently created empty session.
func (m Model) handleAttachRepoTmuxSession() (tea.Model, tea.Cmd) {
	if !m.tmuxLaunchBackend() {
		return m.setStatus(statusOther, "Attaching requires [launch].backend = \"tmux\""), nil
	}
	if !m.tmuxAvailable() {
		return m.setStatus(statusOther, "tmux is not installed"), nil
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return m.setStatus(statusOther, "Select a repository first"), nil
	}
	sessionName := actions.RepoAgentSessionName(repoPath)
	if !m.tmuxRepoSessionExists(repoPath) {
		return m.setStatus(statusOther, "No tmux session "+sessionName+" to attach to"), nil
	}
	if m.launchDetachedTerminal == nil {
		return m.setStatus(statusOther, "No external terminal is configured for attaching"), nil
	}
	launch, err := m.launchDetachedTerminal(actions.RepoTmuxAttachExistingShellCommand(sessionName), repoPath)
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	m = m.setStatus(statusOther, "Attaching to tmux session "+sessionName)
	if launch.Interactive {
		return m, tea.ExecProcess(launch.Cmd, func(err error) tea.Msg {
			if err != nil {
				return TerminalResultMsg{Err: err.Error()}
			}
			return TerminalResultMsg{}
		})
	}
	return m, func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			return TerminalResultMsg{Err: err.Error()}
		}
		return TerminalResultMsg{}
	}
}
