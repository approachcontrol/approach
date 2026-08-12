package model

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
)

// tmuxFallbackNote is reported when tmux mode wanted the tmux route and could
// not take it, on the paths whose default-backend route is certainly the
// embedded terminal. It is set only for that one case: the embedded backend and
// the headless design exclusion are not fallbacks.
const tmuxFallbackNote = "tmux unavailable — launched in embedded terminal"

// tmuxUnavailableNote is the same fallback for paths that cannot name where the
// launch landed. The external route already says "in a terminal session" in the
// status it decorates, and a resume can land embedded or external depending on
// whether the agent supports an embedded slot, so neither may claim one.
const tmuxUnavailableNote = "tmux unavailable"

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
// window: the agent is a CLI agent, the launch is interactive, and it is not a
// Flow repair. It says nothing about the backend or tmux's presence.
//
// Headless launches stay embedded — `claude --print` buffers all output until
// completion, so a self-closing tmux window would show nothing and then discard
// it; the TUI-side renderer is the only readable surface for them. Repair stays
// embedded because a repair session is phase-untracked and its
// obstruction/recovery contract assumes the embedded slot. Repair reaches this
// predicate through no call site today, so the guard is here to keep the rule
// in the predicate that names itself the eligibility rule rather than resting on
// call-site topology.
func tmuxRouteEligible(ctx actions.AgentLaunchContext) bool {
	if ctx.Headless || ctx.FlowRepair {
		return false
	}
	switch agent.Normalize(ctx.Command) {
	case agent.CommandCodex, agent.CommandClaude:
		return true
	default:
		return false
	}
}

// tmuxLaunchRouteFor is the one implementation of the routing rule. It reports
// whether this launch takes the tmux route, and whether declining it is a
// fallback the user should be told about; fellBack is true exactly when the
// tmux route was eligible but tmux is missing.
//
// It takes the backend and the probe rather than reading them off a receiver
// because two callers hold their own copies: Model reads config's value live,
// while a FlowPhaseLauncher snapshots both at admission so a lifecycle attempt
// decides against what it was admitted with. A nil probe means the real PATH.
func tmuxLaunchRouteFor(backend string, available func() bool, ctx actions.AgentLaunchContext) (route bool, fellBack bool) {
	if normalizeLaunchBackend(backend) != config.LaunchBackendTmux || !tmuxRouteEligible(ctx) {
		return false, false
	}
	if available == nil {
		available = actions.TmuxAvailable
	}
	if !available() {
		return false, true
	}
	return true, false
}

func (m Model) tmuxLaunchRoute(ctx actions.AgentLaunchContext) (route bool, fellBack bool) {
	return tmuxLaunchRouteFor(m.launchBackend, m.tmuxLaunchAvailable, ctx)
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
	// Sessions captured outside Approach can carry no repo path, and the session
	// name would then be keyed on the worktree — a session T never finds, since T
	// asks about the selected repo. Fill it in so "one session per repo" holds.
	if strings.TrimSpace(ctx.RepoPath) == "" {
		if repoPath, ok := m.currentRepoPath(); ok {
			ctx.RepoPath = repoPath
		}
	}
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
	route, fellBack := m.tmuxLaunchRoute(ctx)
	if route {
		return m.launchAgentInRepoTmuxSession(ctx, release)
	}
	launchedStatus := ""
	if fellBack {
		launchedStatus = withFallbackNote(agentLaunchedStatus(ctx.Command), tmuxUnavailableNote)
	}
	return m.launchAgentWithContextStatus(ctx, release, launchedStatus)
}

// tmuxPhaseLaunchWindowLive reports whether any of these launches still has an
// open tmux window.
//
// It replaces what the embedded dock's running slot does for reset, repeat
// resume, and repair: in tmux mode there is no slot, and persisted session state
// alone cannot answer the question — a Claude window records nothing until it
// exits, and a Codex window records an `ended` session after its first turn, so a
// phase with a live agent reads as recoverable either way.
//
// This runs a tmux subprocess, so it belongs only on the one-shot user-initiated
// choke points (the reset confirmation, resume admission, and repair admission)
// and never in a predicate the renderer evaluates. It is advisory in one
// direction only: false can mean "probe failed", so it never asserts an agent is
// gone.
func (m Model) tmuxPhaseLaunchWindowLive(repoPath string, launchIDs []string) bool {
	if !m.tmuxLaunchBackend() || strings.TrimSpace(repoPath) == "" || len(launchIDs) == 0 {
		return false
	}
	if !m.tmuxAvailable() {
		return false
	}
	if m.repoTmuxLaunchWindowLive == nil {
		return actions.RepoTmuxLaunchWindowLive(repoPath, launchIDs...)
	}
	return m.repoTmuxLaunchWindowLive(repoPath, launchIDs...)
}

// tmuxPhaseAgentStillRunning is tmuxPhaseLaunchWindowLive for one phase of a
// record, resolving both the repo and the launches a window could belong to.
//
// Every launch the phase made is checked, not only the newest: an earlier
// launch's window can outlive a later one that already exited, and one
// `list-windows` answers for all of them anyway.
func (m Model) tmuxPhaseAgentStillRunning(record flowstore.FlowRecord, phase flowstore.FlowPhase, fallbackRepoPath string) bool {
	return m.tmuxPhaseLaunchWindowLive(m.tmuxProbeRepoPath(record, fallbackRepoPath), phase.LaunchIDs)
}

// tmuxFlowAgentStillRunning is tmuxPhaseAgentStillRunning for a whole record.
// Repair needs it because a Flow-level obstruction names no phase, and a repair
// agent must not start while any of the Flow's phases still has a live window.
func (m Model) tmuxFlowAgentStillRunning(record flowstore.FlowRecord, fallbackRepoPath string) bool {
	var launchIDs []string
	for _, phase := range record.Phases {
		launchIDs = append(launchIDs, phase.LaunchIDs...)
	}
	return m.tmuxPhaseLaunchWindowLive(m.tmuxProbeRepoPath(record, fallbackRepoPath), launchIDs)
}

// tmuxProbeRepoPath resolves which repo's session to probe. It has to agree with
// how the launch resolved its own session name, or the probe would ask about a
// session the window was never opened in.
//
// repoTmuxAgentLaunch keys on the context's RepoPath, falling back to its
// WorktreePath and then the command's directory. Only the first of those is
// reachable in practice — every Flow launch context gets a non-empty RepoPath
// from preflight — so this resolves the record's repo, then the selected one,
// and only then the caller's offer. Consulting the caller's fallback earlier
// would probe a worktree-keyed session for a window opened in the repo's.
func (m Model) tmuxProbeRepoPath(record flowstore.FlowRecord, fallbackRepoPath string) string {
	repoPath := strings.TrimSpace(record.RepoPath)
	if repoPath == "" {
		repoPath, _ = m.currentRepoPath()
	}
	if strings.TrimSpace(repoPath) == "" {
		repoPath = strings.TrimSpace(fallbackRepoPath)
	}
	return repoPath
}

// tmuxAttachRepoPath resolves whose session T attaches to. The active flows
// surface lists Flows from every repo, so the selected Flow's own repo wins
// there; attaching to the left pane's selection would open an unrelated repo's
// agents, or report the selected Flow's live session as missing.
func (m Model) tmuxAttachRepoPath() (string, bool) {
	if m.flowSurfaceVisible() {
		if record, ok := m.selectedFlow(); ok {
			if repoPath := strings.TrimSpace(record.RepoPath); repoPath != "" {
				return repoPath, true
			}
		}
	}
	return m.currentRepoPath()
}

// The live-window refusals. Reset and resume act on one phase; repair is
// Flow-wide and can be armed by an obstruction that names no phase at all, so
// naming a phase there would point at the wrong scope.
const (
	tmuxPhaseLiveWindowRefusal = "Flow phase still has an agent running in tmux"
	tmuxFlowLiveWindowRefusal  = "Flow still has an agent running in tmux"
)

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
	repoPath, ok := m.tmuxAttachRepoPath()
	if !ok {
		return m.setStatus(statusOther, "Select a repository first"), nil
	}
	sessionName := actions.RepoAgentSessionName(repoPath)
	if !m.tmuxRepoSessionExists(repoPath) {
		return m.setStatus(statusOther, "No tmux session "+sessionName+" to attach to"), nil
	}
	// Defaulted in NewWithOptions, so this only fires for a test seam that
	// clears it. The real "no terminal configured" failure comes back as an
	// error from the call below, which names TERMINAL and [terminal].command.
	if m.launchDetachedTerminal == nil {
		return m.setStatus(statusOther, "No external terminal is available for attaching"), nil
	}
	launch, err := m.launchDetachedTerminal(actions.RepoTmuxAttachExistingShellCommand(sessionName), repoPath)
	if err != nil {
		return m.setStatus(statusOther, err.Error()), nil
	}
	// The attach command comes from the shared terminal seam, which inherits the
	// environment. A TUI running inside tmux would otherwise hand its own TMUX to
	// the client and be told it cannot nest.
	actions.StripMultiplexerEnv(launch.Cmd)
	m = m.setStatus(statusOther, "Attaching to tmux session "+sessionName)
	// Always detached: the external terminal opens alongside the TUI rather than
	// taking over its TTY, which is why attaching does not suspend approach.
	return m, func() tea.Msg {
		if err := launch.Cmd.Run(); err != nil {
			return TerminalResultMsg{Err: err.Error()}
		}
		return TerminalResultMsg{}
	}
}
