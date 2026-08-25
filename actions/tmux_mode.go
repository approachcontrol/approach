package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/internal/flowlease"
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
// tmux is not installed. Callers probe availability before routing here, so
// reaching this is a launch failure, not a fallback: no caller inspects it.
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
//
// Dots and colons are replaced because tmux reads them as target separators:
// `-t "=approach-foo.github.io-1a2b3c4d"` parses as session `approach-foo`,
// pane `github` and fails with "can't find pane", which would silently break
// has-session, attach, and the attach command shown to the user. The trailing
// path hash WorktreeSessionName appends keeps the substitution collision-free.
func RepoAgentSessionName(repoPath string) string {
	name := strings.Map(func(r rune) rune {
		if r == '.' || r == ':' {
			return '-'
		}
		return r
	}, WorktreeSessionName(repoPath))
	return repoTmuxSessionPrefix + name
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

// StripMultiplexerEnv clears TMUX and ZELLIJ from a command's environment.
// Attaching is the one tmux-mode action whose command approach does not build
// itself — it goes through the shared external-terminal seam — and a terminal
// that inherits TMUX spawns a tmux client that refuses to nest, which is exactly
// the case for a user running approach inside tmux.
func StripMultiplexerEnv(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = envWithoutKeys(env, "TMUX", "ZELLIJ")
}

// repoTmuxProbeTimeout bounds the probes that run synchronously inside the TUI's
// update loop. A probe does block that loop for as long as it runs; the timeout
// caps how long a wedged tmux server can hold it rather than eliminating the
// stall, which is why callers are restricted to one-shot keystroke handlers. A
// probe that times out simply reports "no evidence".
const repoTmuxProbeTimeout = 2 * time.Second

// repoTmuxLiveWindowFormat pairs each window's name with whether its pane is
// dead. `remain-on-exit on` keeps a finished window listed with its name intact,
// so matching on the name alone would report a long-gone agent as live.
const repoTmuxLiveWindowFormat = "#{window_name} #{pane_dead}"

const (
	repoTmuxLaunchMarker       = "@approach_launch_id"
	repoTmuxLaunchMarkerFormat = "#{" + repoTmuxLaunchMarker + "} #{pane_dead}"
)

// tmuxProbeCommand builds a read-only tmux query. TMUX/ZELLIJ are stripped so a
// TUI running inside a multiplexer still asks the default server rather than its
// enclosing one.
func tmuxProbeCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Env = envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
	return cmd
}

// repoTmuxHasSessionArgs pins the exact-match target the session probe runs.
func repoTmuxHasSessionArgs(repoPath string) []string {
	return []string{"has-session", "-t", tmuxExactTarget(RepoAgentSessionName(repoPath))}
}

// RepoTmuxSessionExists reports whether a repo's agent session is alive.
func RepoTmuxSessionExists(repoPath string) bool {
	if !TmuxAvailable() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), repoTmuxProbeTimeout)
	defer cancel()
	return tmuxProbeCommand(ctx, repoTmuxHasSessionArgs(repoPath)...).Run() == nil
}

// repoTmuxClientFormat names one attached client per row. The session target
// already scopes the listing, so the row's content matters only as evidence
// that a row exists at all.
const repoTmuxClientFormat = "#{client_name}"

// repoTmuxListClientsArgs pins the exact-match target the attach probe runs.
func repoTmuxListClientsArgs(repoPath string) []string {
	return []string{"list-clients", "-t", tmuxExactTarget(RepoAgentSessionName(repoPath)), "-F", repoTmuxClientFormat}
}

// RepoTmuxSessionAttached reports whether a terminal is already watching a
// repo's agent session. tmux mode opens one terminal window per repo and adds
// tmux windows to it afterwards; this is what distinguishes the two cases, and
// it stays honest when the user closes that terminal or restarts the TUI, which
// an in-process flag alone cannot.
//
// False means "no evidence of an attached client" — tmux missing, session gone,
// probe failed or timed out — matching the existing probes' convention. Here
// that error direction costs at most one extra terminal window, never a lost
// agent, so it is the safe way to be wrong.
//
// It runs a tmux subprocess, so callers must keep it off the update loop.
func RepoTmuxSessionAttached(repoPath string) bool {
	if !TmuxAvailable() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), repoTmuxProbeTimeout)
	defer cancel()
	out, err := tmuxProbeCommand(ctx, repoTmuxListClientsArgs(repoPath)...).Output()
	if err != nil {
		return false
	}
	return sessionAttachedInListing(string(out))
}

// sessionAttachedInListing scans repoTmuxClientFormat output for at least one
// client. It is split out because "tmux printed nothing" is the case worth
// testing without a tmux server.
func sessionAttachedInListing(listing string) bool {
	for _, line := range strings.Split(listing, "\n") {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// InsideMultiplexer reports whether approach itself is running inside tmux or
// Zellij. tmux mode opens no terminal window there: the user already has a
// multiplexer in front of them, and a nested `tmux attach` refuses to run
// anyway. The model reads no environment of its own, so the read lives here.
func InsideMultiplexer() bool {
	return insideMultiplexer(os.Getenv)
}

// InsideTmux reports whether Approach's current renderer is hosted by tmux.
func InsideTmux() bool {
	return os.Getenv("TMUX") != ""
}

func insideMultiplexer(getenv func(string) string) bool {
	return getenv("TMUX") != "" || getenv("ZELLIJ") != ""
}

// repoTmuxListWindowsArgs lists a repo's agent session windows with the liveness
// field the launch probe needs.
func repoTmuxListWindowsArgs(repoPath string) []string {
	return []string{"list-windows", "-t", tmuxExactWindowTarget(RepoAgentSessionName(repoPath)), "-F", repoTmuxLiveWindowFormat}
}

func repoTmuxListLaunchMarkersArgs(repoPath string) []string {
	return []string{"list-windows", "-t", tmuxExactWindowTarget(RepoAgentSessionName(repoPath)), "-F", repoTmuxLaunchMarkerFormat}
}

// RepoTmuxLaunchWindowLive reports whether any of these launches still has a
// running window in the repo's agent session. It is the one liveness signal a
// tmux launch has: window names carry the launch ID's trailing hex, so a live
// window can be matched back to the launch that created it.
//
// It is variadic because one `list-windows` answers for every launch at once.
// Callers that own a whole phase or Flow should pass every launch ID it has
// rather than only the newest: an earlier launch's window can outlive a later
// one that already exited, and asking about the newest alone would miss it.
//
// False means "no evidence of a live window" — tmux missing, session gone, probe
// failed or timed out, the window's pane already dead, or launch IDs with
// nothing to match on. Callers must treat it as permission to proceed, never as
// proof the agent is gone, and must only call it on a user-initiated action: it
// runs a tmux subprocess.
func RepoTmuxLaunchWindowLive(repoPath string, launchIDs ...string) bool {
	suffixes := repoTmuxLaunchSuffixes(launchIDs)
	if len(suffixes) == 0 || !TmuxAvailable() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), repoTmuxProbeTimeout)
	defer cancel()
	out, err := tmuxProbeCommand(ctx, repoTmuxListWindowsArgs(repoPath)...).Output()
	if err != nil {
		return false
	}
	return launchWindowRunningInListing(string(out), suffixes)
}

// RepoTmuxLaunchWindowStatus reports whether any matching launch window is
// live and returns an error when tmux could not answer conclusively. Unlike the
// advisory boolean wrapper, a successful false result is safe to use as exit
// evidence.
func RepoTmuxLaunchWindowStatus(repoPath string, launchIDs ...string) (bool, error) {
	launchIDs = normalizedLaunchIDs(launchIDs)
	if len(launchIDs) == 0 {
		return false, errors.New("tmux launch probe requires a matchable launch ID")
	}
	if !TmuxAvailable() {
		return false, errors.New("tmux is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), repoTmuxProbeTimeout)
	defer cancel()
	out, err := tmuxProbeCommand(ctx, repoTmuxListLaunchMarkersArgs(repoPath)...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if tmuxLaunchProbeConfirmsAbsence(string(exitErr.Stderr)) {
				return false, nil
			}
		}
		return false, fmt.Errorf("probe tmux launch window: %w", err)
	}
	live, _ := launchMarkerRunningInListing(string(out), launchIDs)
	return live, nil
}

func tmuxLaunchProbeConfirmsAbsence(stderr string) bool {
	stderr = strings.TrimSpace(stderr)
	return strings.Contains(stderr, "can't find session:") || strings.Contains(stderr, "no server running on")
}

func normalizedLaunchIDs(launchIDs []string) []string {
	seen := make(map[string]struct{}, len(launchIDs))
	normalized := make([]string, 0, len(launchIDs))
	for _, launchID := range launchIDs {
		launchID = strings.TrimSpace(launchID)
		if launchID == "" {
			continue
		}
		if _, ok := seen[launchID]; ok {
			continue
		}
		seen[launchID] = struct{}{}
		normalized = append(normalized, launchID)
	}
	return normalized
}

func launchMarkerRunningInListing(listing string, launchIDs []string) (live, found bool) {
	wanted := make(map[string]struct{}, len(launchIDs))
	for _, launchID := range launchIDs {
		wanted[launchID] = struct{}{}
	}
	for _, line := range strings.Split(listing, "\n") {
		launchID, dead, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if _, ok := wanted[launchID]; !ok {
			continue
		}
		found = true
		if strings.TrimSpace(dead) == "0" {
			return true, true
		}
	}
	return false, found
}

// launchWindowRunningInListing scans repoTmuxLiveWindowFormat output for a live
// window belonging to any of these launches. It is split out because the
// dead-pane filter is the part worth testing without a tmux server.
func launchWindowRunningInListing(listing string, suffixes []string) bool {
	if len(suffixes) == 0 {
		return false
	}
	for _, line := range strings.Split(listing, "\n") {
		name, dead, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !matchesAnySuffix(name, suffixes) {
			continue
		}
		// A retained dead window is not a running agent. Anything other than a
		// clear "0" is treated as dead, so an unparsable field cannot invent one.
		if strings.TrimSpace(dead) == "0" {
			return true
		}
	}
	return false
}

func matchesAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// repoTmuxLaunchScript creates the repo session on demand and runs the agent in
// a new window of it. Neither ordering is atomic against a near-simultaneous
// launch into the same repo, so both races retry as the other command: losing
// the session to a concurrent launch retries as new-window, and a session that
// dies between the probe and new-window retries as new-session. Retried
// attempts suppress their stderr so only the last one can write; either way the
// caller sees the script's exit status, not its message.
//
// Every creation is detached (`-d`): without it new-window makes its window the
// session's current one, so a user attached to the repo session — the workflow T
// exists for — would be yanked off whatever agent they were watching every time
// the TUI launched another. A launch must not move anyone's client.
const repoTmuxLaunchScript = `
session=$1
window=$2
dir=$3
cmd=$4
launch_id=$5
mark_window() {
	if tmux set-option -w -t "=$session:=$window" @approach_launch_id "$launch_id"; then
		return 0
	fi
	tmux kill-window -t "=$session:=$window" 2>/dev/null || true
	return 1
}
if tmux has-session -t "=$session" 2>/dev/null; then
	if tmux new-window -d -t "=$session:" -n "$window" -c "$dir" "$cmd" 2>/dev/null; then
		mark_window
		exit $?
	fi
fi
if tmux new-session -d -s "$session" -n "$window" -c "$dir" "$cmd" 2>/dev/null; then
	mark_window
	exit $?
fi
if tmux new-window -d -t "=$session:" -n "$window" -c "$dir" "$cmd"; then
	mark_window
	exit $?
fi
exit 1
`

// RepoTmuxAgentLaunch builds a CLI agent launch that runs in the repo's tmux
// session. The agent itself runs from the same self-deleting script every other
// transport uses, so cwd, APPROACH_* exports, and provider hook wiring are
// identical to an embedded or external launch.
func RepoTmuxAgentLaunch(ctx AgentLaunchContext) (RepoTmuxAgentSpec, error) {
	return repoTmuxAgentLaunch(ctx, exec.LookPath)
}

func repoTmuxAgentLaunch(ctx AgentLaunchContext, lookPath lookPathFunc) (RepoTmuxAgentSpec, error) {
	return repoTmuxAgentLaunchWithExecutable(ctx, lookPath, os.Executable)
}

func repoTmuxAgentLaunchWithExecutable(ctx AgentLaunchContext, lookPath lookPathFunc, executable func() (string, error)) (RepoTmuxAgentSpec, error) {
	if !commandExists("tmux", lookPath) {
		return RepoTmuxAgentSpec{}, ErrRepoTmuxUnavailable
	}
	command := agent.Normalize(ctx.Command)
	if command != agent.CommandCodex && command != agent.CommandClaude && command != agent.CommandCursor {
		return RepoTmuxAgentSpec{}, errors.New("tmux launch mode supports only CLI agents")
	}
	// The role is classified once here and read once below, by the validation
	// that decides whether this launch may take the tracked route at all.
	//
	// The gate itself stays the marker rather than role.Tracked(): the marker is
	// the launch's claim on the Flow lease, and the classifier deliberately
	// names a phase-attached context a phase role even when it declared itself
	// untracked. Such a launch has always taken the plain window here, and
	// promoting it would hand it a lease it never asked for.
	role := FlowLaunchRoleOf(ctx)
	if ctx.FlowLaunchTracked {
		if err := validateTrackedRepoTmuxRole(ctx, role); err != nil {
			return RepoTmuxAgentSpec{}, err
		}
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
	// Validated above, so the marker and role.Tracked() agree by here.
	if ctx.FlowLaunchTracked {
		canonicalRoot, err := flowlease.ResolveRoot(ctx.SessionStateRoot)
		if err != nil {
			return RepoTmuxAgentSpec{}, fmt.Errorf("prepare tracked Flow lease: %w", err)
		}
		executablePath, err := resolveTrackedTmuxExecutable(ctx.Executable, executable)
		if err != nil {
			return RepoTmuxAgentSpec{}, err
		}
		handoffSuffix, err := randomHex(8)
		if err != nil {
			return RepoTmuxAgentSpec{}, fmt.Errorf("allocate Flow launch handoff suffix: %w", err)
		}
		nonce, err := randomHex(16)
		if err != nil {
			return RepoTmuxAgentSpec{}, fmt.Errorf("allocate Flow launch handoff nonce: %w", err)
		}
		handoffDir, err := flowlease.NewHandoffPath(canonicalRoot, ctx.LaunchID, handoffSuffix)
		if err != nil {
			return RepoTmuxAgentSpec{}, fmt.Errorf("prepare Flow launch handoff path: %w", err)
		}
		windowName = repoTmuxLeasedWindowName(ctx, handoffSuffix)
		now := time.Now()
		privateSpec := flowlease.PrivateSpec{
			SessionName:      sessionName,
			WindowName:       windowName,
			CWD:              cmd.Dir,
			Root:             canonicalRoot,
			FlowID:           ctx.FlowID,
			PhaseID:          ctx.FlowPhaseID,
			LaunchID:         ctx.LaunchID,
			HandoffDir:       handoffDir,
			Nonce:            nonce,
			DecisionDeadline: now.Add(10 * time.Second),
			StartedDeadline:  now.Add(20 * time.Second),
			CleanupDeadline:  now.Add(25 * time.Second),
		}
		termCommand, err := newTerminalCommandWithArgvBuilder(cmd.Dir, agentEnv, sessionName, "", func(scriptPath string) ([]string, error) {
			privateSpec.ScriptPath = scriptPath
			return flowlease.LeaseRunArgv(executablePath, privateSpec, argv)
		})
		if err != nil {
			return RepoTmuxAgentSpec{}, err
		}
		spawnArgv, err := flowlease.SpawnArgv(executablePath, privateSpec)
		if err != nil {
			termCommand.cleanup()
			return RepoTmuxAgentSpec{}, err
		}
		spawnCmd := exec.Command(spawnArgv[0], spawnArgv[1:]...)
		spawnCmd.Env = envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
		stderr := &boundedBuffer{limit: repoTmuxStderrLimit}
		cleanupDiagnostic := &boundedBuffer{limit: repoTmuxStderrLimit}
		spawnCmd.Stderr = stderr
		spawnCmd.WaitDelay = repoTmuxStderrDrainDelay
		cleanup := func() {
			termCommand.cleanup()
			if _, err := os.Lstat(privateSpec.HandoffDir); err == nil {
				if cancelErr := flowlease.CancelExact(privateSpec); cancelErr != nil {
					_, _ = fmt.Fprintf(cleanupDiagnostic, "cancel tracked Flow tmux launch: %v", cancelErr)
				}
			} else if !os.IsNotExist(err) {
				_, _ = fmt.Fprintf(cleanupDiagnostic, "inspect tracked Flow tmux handoff before cancellation: %v", err)
			}
		}
		errorDetail := func() string {
			spawnDetail := stderr.String()
			cleanupDetail := cleanupDiagnostic.String()
			if spawnDetail == "" {
				return cleanupDetail
			}
			if cleanupDetail == "" {
				return spawnDetail
			}
			return spawnDetail + "\n" + cleanupDetail
		}
		return RepoTmuxAgentSpec{
			SessionName: sessionName, WindowName: windowName,
			AttachCommand: RepoTmuxAttachCommand(sessionName),
			Launch: TerminalLaunchSpec{
				Cmd: spawnCmd, Detached: true, Cleanup: cleanup,
				ErrorDetail: errorDetail,
			},
		}, nil
	}
	// sessionName is passed for parity with the other transports' scripts; only
	// terminalLaunchWithOptions reads it back, and this path never calls that.
	termCommand, err := newTerminalCommand(cmd.Dir, agentEnv, argv, sessionName)
	if err != nil {
		return RepoTmuxAgentSpec{}, err
	}
	tmuxCmd := exec.Command("sh", "-c", repoTmuxLaunchScript, "approach", sessionName, windowName, cmd.Dir, termCommand.shellCommand(), ctx.LaunchID)
	tmuxCmd.Env = envWithoutKeys(os.Environ(), "TMUX", "ZELLIJ")
	// Without this the script's last attempt — the only one that does not
	// suppress stderr — writes to /dev/null and every tmux failure reduces to
	// "exit status 1". A Flow launch persists that string into the phase's
	// needs_attention note, so the durable record of the failure would carry no
	// diagnostic at all.
	stderr := &boundedBuffer{limit: repoTmuxStderrLimit}
	tmuxCmd.Stderr = stderr
	// Capturing stderr makes os/exec interpose a pipe, so Wait now also waits for
	// every process holding the write end to close it. tmux's server daemonizes
	// and drops inherited fds, but one that did not would hang Wait inside the
	// goroutine holding this Flow's cross-process launch reservation. WaitDelay
	// only applies once the command itself has exited, so it costs the spawn
	// nothing and bounds exactly that case.
	tmuxCmd.WaitDelay = repoTmuxStderrDrainDelay
	return RepoTmuxAgentSpec{
		SessionName:   sessionName,
		WindowName:    windowName,
		AttachCommand: RepoTmuxAttachCommand(sessionName),
		Launch: TerminalLaunchSpec{
			Cmd: tmuxCmd,
			// The tmux command returns as soon as the window exists; the agent
			// keeps running there and provider hooks own its completion.
			Detached:    true,
			Cleanup:     termCommand.cleanup,
			ErrorDetail: stderr.String,
		},
	}, nil
}

// validateTrackedRepoTmuxRole refuses every launch the tracked tmux route does
// not serve. The role and its marker rows answer which launches those are; what
// stays here is the transport and payload the role deliberately does not carry.
// Headless never routes to tmux, an auto launch has no terminal to attach to
// (F1), and a PR number belongs to autofix, which is not a tracked role.
func validateTrackedRepoTmuxRole(ctx AgentLaunchContext, role FlowLaunchRole) error {
	if !role.Tracked() ||
		validateFlowLaunchRole(ctx, role) != nil ||
		ctx.FlowAutoLaunch ||
		ctx.Headless ||
		ctx.FlowAutofixPRNumber != 0 {
		return errors.New("invalid tracked Flow tmux launch role")
	}
	return nil
}

func randomHex(byteCount int) (string, error) {
	data := make([]byte, byteCount)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

// resolveTrackedTmuxExecutable picks the binary both private lease helpers
// exec. A launch that already carries a verified pin must use that path:
// os.Executable still names the mutable installation even after an upgrade
// replaces it, and a replacement whose hidden handoff protocol differs would
// hang the tracked tmux launch. An empty pin keeps the previous running-binary
// behaviour for genuinely unpinned callers.
func resolveTrackedTmuxExecutable(pinned string, fallback func() (string, error)) (string, error) {
	if path := strings.TrimSpace(pinned); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("pinned Approach executable path must be absolute")
		}
		return path, nil
	}
	path, err := fallback()
	if err != nil {
		return "", fmt.Errorf("resolve current Approach executable: %w", err)
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("current Approach executable path must be absolute")
	}
	return path, nil
}

// repoTmuxStderrLimit bounds what a failed tmux invocation can push into a
// status line and a Flow's persisted needs_attention note.
const repoTmuxStderrLimit = 1024

// repoTmuxStderrDrainDelay bounds how long Wait may block on the captured stderr
// pipe after the launch command itself has exited.
const repoTmuxStderrDrainDelay = 2 * time.Second

// boundedBuffer collects at most limit bytes and discards the rest. Each
// instance has one writer and is read only after that writer finishes, so it
// needs no synchronization of its own.
type boundedBuffer struct {
	limit int
	buf   []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	// Report a full write regardless: a truncated diagnostic must not make the
	// child see a short write on stderr.
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return strings.TrimSpace(string(b.buf))
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

func repoTmuxLeasedWindowName(ctx AgentLaunchContext, handoffSuffix string) string {
	name := sanitizeSessionSuffix(ctx.FlowPhaseKind)
	if name == "" {
		name = sanitizeSessionSuffix(agent.Normalize(ctx.Command))
	}
	if name == "" {
		name = "agent"
	}
	name += "-" + strings.Trim(sanitizeSessionSuffix(handoffSuffix), ".-")
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

// repoTmuxLaunchSuffixes drops the launch IDs that sanitize to nothing, so an
// empty one can never widen a probe into matching every window.
func repoTmuxLaunchSuffixes(launchIDs []string) []string {
	suffixes := make([]string, 0, len(launchIDs))
	for _, launchID := range launchIDs {
		if suffix := repoTmuxLaunchSuffix(launchID); suffix != "" {
			suffixes = append(suffixes, suffix)
		}
	}
	return suffixes
}

// tmuxExactTarget pins a target to one exact session name so tmux's prefix
// matching cannot resolve it to a different session.
func tmuxExactTarget(sessionName string) string {
	return "=" + sessionName
}

// tmuxExactWindowTarget is tmuxExactTarget for the commands that take a
// target-window. The trailing colon keeps tmux from resolving a bare name
// against a window in whichever session the server considers current.
func tmuxExactWindowTarget(sessionName string) string {
	return tmuxExactTarget(sessionName) + ":"
}
