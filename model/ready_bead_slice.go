package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/ui"
)

const sliceEpicInFlightStatus = "A slice epic launch is already in flight"

// sliceEpicLaunchRecord ties the held preparation admission to the one launch
// that owns it. AgentResultMsg carries no preparation token, so the LaunchID is
// what maps a returning result back to the fence it must release — and what
// keeps a result from a launch made before a repository or selection change
// from releasing a newer fence or reporting against a different epic.
type sliceEpicLaunchRecord struct {
	LaunchID string
	Token    uint64
	RepoPath string
	EpicID   string
}

// sliceEpicKeysOwned mirrors epicProgressionKeysOwned's modal/search/terminal
// preamble, which is the closest precedent because that action also requires
// epic-ness. It differs in two ways: it is Ready-only rather than any Beads
// subview (which also excludes the Active Flows takeover, since focusedMode
// returns ModeActiveFlows while it is open), and it needs no live inline
// expansion target.
func (m Model) sliceEpicKeysOwned() bool {
	terminalInputFocused := m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && m.hasActiveEmbeddedTerminal()
	if m.modal.IsOpen() || m.searchActive || terminalInputFocused ||
		!m.contentListInputEligible() || m.focusedMode() != ui.ModeBeadsReady {
		return false
	}
	if _, ok := m.currentRepoPath(); !ok {
		return false
	}
	bead, ok := m.selectedVisibleBead()
	// An empty ID would produce an unrunnable `bd show -- ` instruction, so the
	// action is not offered for rows that carry no usable Bead ID.
	if !ok || strings.TrimSpace(bead.ID) == "" {
		return false
	}
	return isEpicBead(bead)
}

// canSliceSelectedEpic gates the footer hint. It deliberately does not consult
// the configured agent: a refusal that names the fixing key teaches more than a
// hint that silently disappears, which is why an unconfigured agent still
// advertises S and refuses on press.
func (m Model) canSliceSelectedEpic() bool {
	return m.sliceEpicKeysOwned() && !m.flowPreparationAdmission
}

func (m Model) sliceEpicTarget() (string, string, bool) {
	if !m.sliceEpicKeysOwned() {
		return "", "", false
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return "", "", false
	}
	bead, ok := m.selectedVisibleBead()
	if !ok {
		return "", "", false
	}
	epicID := strings.TrimSpace(bead.ID)
	if epicID == "" {
		return "", "", false
	}
	return repoPath, epicID, true
}

// sliceEpicLaunchInFlight reports whether this action, rather than an f/F/a
// preparation, is what holds the shared admission. That distinction is what
// lets a refused press speak only about its own fence.
func (m Model) sliceEpicLaunchInFlight() bool {
	return m.flowPreparationAdmission &&
		m.flowPreparationOwner.Kind == flowPreparationSliceEpic &&
		strings.TrimSpace(m.sliceEpicLaunch.LaunchID) != ""
}

func (m Model) recordSliceEpicLaunch(launchID string, token uint64, repoPath, epicID string) Model {
	m.sliceEpicLaunch = sliceEpicLaunchRecord{
		LaunchID: strings.TrimSpace(launchID), Token: token, RepoPath: repoPath, EpicID: epicID,
	}
	return m
}

func (m Model) sliceEpicLaunchFor(launchID string) (sliceEpicLaunchRecord, bool) {
	id := strings.TrimSpace(launchID)
	if id == "" || m.sliceEpicLaunch.LaunchID != id {
		return sliceEpicLaunchRecord{}, false
	}
	return m.sliceEpicLaunch, true
}

// releaseSliceEpicLaunch is the only release path, used by both the result
// funnel and the launch-failure funnel, so the record and the admission can
// never drift apart. It no-ops on an empty or non-matching ID, which is what
// keeps every existing startFlowLaunchFailure caller byte-identical and stops
// an unrelated launch's failure from releasing a held slice fence.
func (m Model) releaseSliceEpicLaunch(launchID string) Model {
	record, ok := m.sliceEpicLaunchFor(launchID)
	if !ok {
		return m
	}
	m.sliceEpicLaunch = sliceEpicLaunchRecord{}
	return m.releaseFlowPreparation(flowPreparationSliceEpic, record.Token)
}

// sliceEpicPrompt is a built-in constant rather than a configurable template:
// the Bead calls this prompt the contract that activates slice-issues, and a
// user-editable version could be silently emptied.
//
// It diverges from the skill on two points on purpose. The skill's pre-approval
// presentation omits acceptance criteria (they appear only in the created
// sub-issue), and the skill tells the agent to ask which tracker to use. This
// prompt requires acceptance criteria up front and names Beads and the parent
// ID directly, so the agent never has to ask. It also restates the whole
// process, so a machine without the skill installed degrades rather than fails.
func sliceEpicPrompt(repoPath, epicID string) string {
	return fmt.Sprintf(`Use the slice-issues skill to break down Bead %[2]s in the repository at %[1]s.

Read the epic first with `+"`bd show -- %[2]s`"+`, then explore enough of the repository to propose tracer-bullet vertical slices.

Present the proposed slices with, for each one, its HITL or AFK classification, the slices it is blocked by, its acceptance criteria, and the user stories it covers. Wait for my approval and revise until I approve.

After approval, create only the approved child Beads under %[2]s, in dependency order. Never modify or close the parent epic %[2]s.`, repoPath, epicID)
}

// handleSliceSelectedEpic carries the single ownership gate for S; the key
// switch does not repeat it.
func (m Model) handleSliceSelectedEpic() (Model, tea.Cmd) {
	repoPath, epicID, ok := m.sliceEpicTarget()
	if !ok {
		return m, nil
	}
	// Refused before the preparation is reserved, so an unusable pin leaves the
	// shared fence exactly as it found it. The launched agent runs `bd` and
	// `approach` commands through the pinned binary just as every other launch
	// kind does.
	if refusal := refuseUnverifiedLaunchPin(m.launchPin); refusal != "" {
		return m.setStatus(statusOther, refusal), nil
	}
	next, token, admitted := m.acquireFlowPreparation(flowPreparationSliceEpic)
	if !admitted {
		if m.sliceEpicLaunchInFlight() {
			return m.setStatus(statusOther, sliceEpicInFlightStatus), nil
		}
		// An f/F/a preparation holds the shared bool. Those actions refuse
		// silently in the same situation, and S only ever speaks about its own
		// fence.
		return m, nil
	}
	m = next
	if agent.Normalize(m.agentCommand) == "" {
		m = m.releaseFlowPreparation(flowPreparationSliceEpic, token)
		return m.setStatus(statusOther, flowLaunchNoAgentCommandStatus), nil
	}
	// Everything the launch depends on is resolved here, synchronously, so no
	// later selection change can redirect it. The context deliberately carries
	// no Flow marker, which keeps flowLaunchContextRequiresLifecycle false and
	// this launch outside the sealed Flow launch lifecycle.
	launch := m.launchAgentSettings(m.agentCommand)
	ctx := applyLaunchPin(actions.AgentLaunchContext{
		Command:          m.agentCommand,
		Model:            launch.Model,
		ReasoningEffort:  launch.ReasoningEffort,
		LaunchID:         newLaunchID(),
		RepoPath:         repoPath,
		WorktreePath:     repoPath,
		WorkingDir:       repoPath,
		SessionStateRoot: m.sessionStateRoot,
		InitialPrompt:    sliceEpicPrompt(repoPath, epicID),
	}, m.launchPin)
	// Record before launching: launchAgentForBackend's failure paths release
	// through releaseSliceEpicLaunch synchronously.
	m = m.recordSliceEpicLaunch(ctx.LaunchID, token, repoPath, epicID)
	return m.launchAgentForBackend(ctx, nil)
}

func sliceEpicLaunchedStatus(command, epicID string) string {
	return "Launched " + command + " to slice " + epicID
}
