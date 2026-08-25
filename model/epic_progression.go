package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/beadsquery"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/ui"
)

type epicProgressionToggleResultMsg struct {
	target               beadExpansionTarget
	progression          flowstore.EpicProgression
	flow                 flowstore.FlowRecord
	baselineDisposition  epicProgressionBaselineDisposition
	enabled              bool
	known                bool
	status               string
	presentStatusOnStale bool
	release              func()
	preparationKind      flowPreparationKind
	preparationToken     uint64
}

type epicProgressionBaselineDisposition uint8

const (
	epicProgressionBaselinePreserve epicProgressionBaselineDisposition = iota
	epicProgressionBaselineReplace
	epicProgressionBaselineRemove
)

type flowPreparationKind uint8

const (
	flowPreparationNone flowPreparationKind = iota
	flowPreparationReadyBead
	flowPreparationEpicToggle
	flowPreparationEpicAdvance
	flowPreparationEpicHalt
	// flowPreparationSliceEpic holds across a whole agent spawn rather than a
	// store round trip, so it is the longest-lived sharer of this admission.
	flowPreparationSliceEpic
)

type flowPreparationOwner struct {
	Kind  flowPreparationKind
	Token uint64
}

type epicProgressionOwnedSuccessor struct {
	SourceFlowID string
	ChildID      string
	FlowID       string
}

type epicProgressionBaseline struct {
	flowstore.FlowRecord
	Activation time.Time
}

type epicProgressionAdvanceRequest struct {
	Request      uint64
	OwnerToken   uint64
	EpicKey      string
	SourceFlowID string
}

type epicProgressionAdvanceDisposition uint8

const (
	epicProgressionAdvanceRetryable epicProgressionAdvanceDisposition = iota
	epicProgressionAdvanceInactive
	epicProgressionAdvanceExhausted
	epicProgressionAdvanceExhaustedActive
	epicProgressionAdvanceExhaustedUnknown
	// epicProgressionAdvanceSelected carries a chosen child that has no Flow
	// yet. It is the only disposition that keeps the advance's preparation
	// admission held: the create-then-launch intent it produces runs under the
	// same admission, and the create pipeline releases it.
	epicProgressionAdvanceSelected
)

type epicProgressionAdvanceResultMsg struct {
	request      uint64
	ownerToken   uint64
	epicKey      string
	repoPath     string
	epicID       string
	sourceFlowID string
	activation   time.Time
	disposition  epicProgressionAdvanceDisposition
	// owned and childTitle are selection-only: the chosen child and its Bead
	// title, carried to the create-then-launch intent that follows.
	owned       epicProgressionOwnedSuccessor
	childTitle  string
	degradation *flowstore.PartialListError
	status      string
}

type epicProgressionHaltRequest struct {
	Request      uint64
	OwnerToken   uint64
	EpicKey      string
	SourceFlowID string
}

type epicProgressionHaltDisposition uint8

const (
	epicProgressionHaltRetryable epicProgressionHaltDisposition = iota
	epicProgressionHaltPersisted
	epicProgressionHaltInactive
)

type epicProgressionHaltResultMsg struct {
	request      uint64
	ownerToken   uint64
	epicKey      string
	repoPath     string
	epicID       string
	sourceFlowID string
	disposition  epicProgressionHaltDisposition
	status       string
}

func (m Model) acquireFlowPreparation(kind flowPreparationKind) (Model, uint64, bool) {
	if kind == flowPreparationNone || m.flowPreparationAdmission {
		return m, 0, false
	}
	m.flowPreparationSeq++
	if m.flowPreparationSeq == 0 {
		m.flowPreparationSeq++
	}
	m.flowPreparationAdmission = true
	m.flowPreparationOwner = flowPreparationOwner{Kind: kind, Token: m.flowPreparationSeq}
	return m, m.flowPreparationSeq, true
}

func (m Model) flowPreparationMatches(kind flowPreparationKind, token uint64) bool {
	if !m.flowPreparationAdmission {
		return false
	}
	// Zero-token state exists only in focused legacy tests that construct a
	// Model directly. Production acquisitions always allocate a non-zero token.
	if token == 0 {
		return m.flowPreparationOwner.Token == 0
	}
	return m.flowPreparationOwner == (flowPreparationOwner{Kind: kind, Token: token})
}

func (m Model) releaseFlowPreparation(kind flowPreparationKind, token uint64) Model {
	if !m.flowPreparationMatches(kind, token) {
		return m
	}
	m.flowPreparationAdmission = false
	m.flowPreparationOwner = flowPreparationOwner{}
	return m
}

func (m Model) startEpicProgressionAdvance(epicKey string, baseline epicProgressionBaseline, observed flowstore.FlowRecord) (Model, tea.Cmd) {
	var ownerToken uint64
	var admitted bool
	m, ownerToken, admitted = m.acquireFlowPreparation(flowPreparationEpicAdvance)
	if !admitted {
		return m, nil
	}
	m.epicProgressionAdvanceSeq++
	request := epicProgressionAdvanceRequest{
		Request: m.epicProgressionAdvanceSeq, OwnerToken: ownerToken,
		EpicKey: epicKey, SourceFlowID: baseline.FlowID,
	}
	m.activeEpicProgressionAdvance = request
	return m, m.advanceEpicProgressionCmd(request, baseline, observed)
}

// advanceEpicProgressionCmd selects the next child and stops there. Everything
// that writes — the child Flow, its worktree, its preparation receipt, its
// successor reconciliation and its first phase — belongs to the create-phase
// launch pipeline the selection is handed to, so the whole advance is one
// admitted launch attempt rather than a preparation plus a later start.
func (m Model) advanceEpicProgressionCmd(request epicProgressionAdvanceRequest, baseline epicProgressionBaseline, observed flowstore.FlowRecord) tea.Cmd {
	readProgression := m.readEpicProgression
	closeBead := m.closeBead
	listChildren := m.listChildrenBeads
	listReady := m.listReadyBeads
	listFlows := m.listFlows
	setProgression := m.setEpicProgression
	repoPath := filepath.Clean(baseline.RepoPath)
	epicID := strings.TrimSpace(baseline.Bead.EpicID)
	var degradation *flowstore.PartialListError
	result := func(disposition epicProgressionAdvanceDisposition, status string) epicProgressionAdvanceResultMsg {
		return epicProgressionAdvanceResultMsg{
			request: request.Request, ownerToken: request.OwnerToken, epicKey: request.EpicKey,
			repoPath: repoPath, epicID: epicID, sourceFlowID: request.SourceFlowID, activation: baseline.Activation,
			disposition: disposition, degradation: degradation, status: status,
		}
	}
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: repoPath, EpicID: epicID}
		progression, found, err := readProgression(key)
		if err != nil {
			return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", epicID, err))
		}
		if !found || !progression.Enabled || progression.Done || progression.Halt != nil ||
			(!progression.UpdatedAt.IsZero() && !progression.UpdatedAt.Equal(baseline.Activation)) {
			return result(epicProgressionAdvanceInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
		}

		// Closing the merged child's Bead must precede the readiness query, not
		// follow it. Progression claims a child on enable but nothing else ever
		// releases it, so a still-claimed parent keeps its dependent siblings out
		// of `bd ready`; a close that lands afterwards selects against a stale
		// snapshot and the empty intersection reports the epic complete.
		//
		// Only a recorded merge qualifies. A child that merely reached completed
		// has nothing on the base branch, and closing its Bead would retire work
		// that never landed.
		if beadID, reason, ok := progressionMergedChildClose(observed); ok && closeBead != nil {
			// bd close is idempotent, which is what makes this safe on a
			// level-triggered edge: the success branch refires on every poll
			// until an advance actually completes.
			if err := closeBead(repoPath, beadID, reason); err != nil {
				return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not close Bead %s for merged Flow %s: %v", beadID, observed.FlowID, err))
			}
		}

		var candidate epicProgressionOwnedSuccessor
		var childTitle string
		children, childrenErr := listChildren(repoPath, epicID)
		if childrenErr != nil {
			return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not list children for epic %s: %v", epicID, childrenErr))
		}
		ready, readyErr := listReady(repoPath)
		if readyErr != nil {
			return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not list ready children for epic %s: %v", epicID, readyErr))
		}
		flows, flowsErr := listFlows(flowstore.FlowFilter{RepoPath: repoPath})
		if partial, ok := flowstore.AsPartialList(flowsErr); ok {
			degradation = partial
		} else if flowsErr != nil {
			return result(epicProgressionAdvanceRetryable, fmt.Sprintf("Could not check existing child Flows for epic %s: %v", epicID, flowsErr))
		}
		direct := make(map[string]struct{}, len(children))
		for _, child := range children {
			if id := strings.TrimSpace(child.ID); id != "" {
				direct[id] = struct{}{}
			}
		}
		// This filter must agree with the store's creation guard in every
		// dimension the guard uses — repository, TRIMMED Bead ID, and slot
		// occupancy — or a child the filter selects gets refused by the guard
		// on every pass. Selection sets no backoff and nothing here sets Halt,
		// so a disagreement is an unbounded 1 Hz select-create-refuse loop
		// rather than a cosmetic mismatch.
		//
		// Hence flowstore.BeadFlowSlotOccupied itself rather than a
		// hand-rolled status test: a child whose only Flow is closed or merged
		// is still selected, and the guard still permits the create. The epic
		// disjunct preserves today's behaviour for a child whose epic-linked
		// Flow has since completed — progression treats that child as done,
		// and this fix is not the place to revisit that.
		//
		// A refusal that still gets through is a genuine race: another process
		// committed the Flow between listFlows and the create. It surfaces in
		// the shared launch-create pipeline, which names it, and the next
		// pass's snapshot contains the winner, so this filter skips the child
		// and selection moves on (or the exhausted path fires).
		linkedChildren := make(map[string]struct{})
		for _, flow := range flows {
			if filepath.Clean(flow.RepoPath) != repoPath || strings.TrimSpace(flow.Bead.ID) == "" {
				continue
			}
			if flow.Bead.EpicID == epicID || flowstore.BeadFlowSlotOccupied(flow) {
				linkedChildren[strings.TrimSpace(flow.Bead.ID)] = struct{}{}
			}
		}
		intersection, linked := 0, 0
		for _, readyChild := range ready {
			childID := strings.TrimSpace(readyChild.ID)
			if _, ok := direct[childID]; !ok {
				continue
			}
			intersection++
			if _, ok := linkedChildren[childID]; ok {
				linked++
				continue
			}
			candidate = epicProgressionOwnedSuccessor{SourceFlowID: request.SourceFlowID, ChildID: childID}
			childTitle = strings.TrimSpace(readyChild.Title)
			break
		}
		if candidate.ChildID == "" {
			// A child held by a Flow under another epic counts as exhausted
			// rather than retryable, deliberately. Progression already
			// terminates whenever no ready child is available to it — a
			// blocked child reaches this branch the same way — and the
			// alternative is an unbounded poll: nothing here sets a backoff,
			// so a manual Flow left open on a child would keep this closure
			// shelling out to `bd` every advance tick for as long as it lives.
			// Termination is visible (the status below says so) and
			// re-enabling auto-progression is one key away, whereas the poll
			// has no exit the user can see.
			status := fmt.Sprintf("Auto-progression complete for epic %s; no ready children remain", epicID)
			if intersection > 0 && linked == intersection {
				status = fmt.Sprintf("Auto-progression complete for epic %s; every ready child already has a Flow", epicID)
			}
			_, doneErr := setProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false, Done: true, ExpectedActivation: baseline.Activation})
			if doneErr == nil {
				return result(epicProgressionAdvanceExhausted, status)
			}
			if errors.Is(doneErr, flowstore.ErrEpicProgressionActivationChanged) {
				return result(epicProgressionAdvanceInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
			}
			authoritative, stillFound, readErr := readProgression(key)
			if readErr != nil {
				return result(epicProgressionAdvanceExhaustedUnknown, fmt.Sprintf("Could not confirm auto-progression completion for epic %s: %v", epicID, readErr))
			}
			if stillFound && authoritative.Done && !authoritative.Enabled && authoritative.Halt == nil {
				return result(epicProgressionAdvanceExhausted, status)
			}
			if !stillFound || !authoritative.Enabled || authoritative.Halt != nil {
				return result(epicProgressionAdvanceInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
			}
			return result(epicProgressionAdvanceExhaustedActive, fmt.Sprintf("Could not disable exhausted auto-progression for epic %s: %v", epicID, doneErr))
		}

		selected := result(epicProgressionAdvanceSelected, "")
		selected.owned = candidate
		selected.childTitle = childTitle
		return selected
	}
}

// startEpicProgressionHalt shares the single-flight preparation admission with
// advance and the epic toggle, so a halt can never interleave with either. It
// creates no Flow, queries no Beads, and takes no Flow reservation.
// progressionMergedChildClose reports the Bead a merged child Flow retires, and
// the reason recorded against it. It keys on the durable merge record rather
// than on the derived status so that a hand-edited status cannot close a Bead
// whose PR never landed.
func progressionMergedChildClose(observed flowstore.FlowRecord) (string, string, bool) {
	if observed.Merge.Status != flowstore.MergeMerged {
		return "", "", false
	}
	beadID := strings.TrimSpace(observed.Bead.ID)
	if beadID == "" {
		return "", "", false
	}
	reason := fmt.Sprintf("Merged by Approach Flow %s", observed.FlowID)
	if observed.PR.Number > 0 {
		reason = fmt.Sprintf("Merged by Approach Flow %s (PR #%d)", observed.FlowID, observed.PR.Number)
	}
	if commit := strings.TrimSpace(observed.Merge.Commit); commit != "" {
		reason += ": " + commit
	}
	return beadID, reason, true
}

func (m Model) startEpicProgressionHalt(epicKey string, baseline epicProgressionBaseline, observed flowstore.FlowRecord) (Model, tea.Cmd) {
	var ownerToken uint64
	var admitted bool
	m, ownerToken, admitted = m.acquireFlowPreparation(flowPreparationEpicHalt)
	if !admitted {
		return m, nil
	}
	m.epicProgressionHaltSeq++
	request := epicProgressionHaltRequest{
		Request: m.epicProgressionHaltSeq, OwnerToken: ownerToken,
		EpicKey: epicKey, SourceFlowID: baseline.FlowID,
	}
	m.activeEpicProgressionHalt = request
	// The web viewer renders the halt message verbatim beside the child and its
	// state, so the message carries the one identifier neither surface derives.
	halt := flowstore.EpicProgressionHalt{
		ChildBeadID: strings.TrimSpace(observed.Bead.ID),
		Status:      epicProgressionResolvedStatus(observed),
		Message:     fmt.Sprintf("child Flow %s halted auto-progression", strings.TrimSpace(observed.FlowID)),
	}
	return m, m.haltEpicProgressionCauseCmd(request, filepath.Clean(observed.RepoPath), strings.TrimSpace(observed.Bead.EpicID), baseline.Activation, halt)
}

// haltEpicProgressionCauseCmd persists one already-composed halt tuple. Both
// halt edges — a failed child and a child that could not launch at all — share
// it, so neither can invent its own announcement or its own retry rules.
func (m Model) haltEpicProgressionCauseCmd(request epicProgressionHaltRequest, repoPath, epicID string, activation time.Time, halt flowstore.EpicProgressionHalt) tea.Cmd {
	haltProgression := m.haltEpicProgression
	readProgression := m.readEpicProgression
	result := func(disposition epicProgressionHaltDisposition, status string) epicProgressionHaltResultMsg {
		return epicProgressionHaltResultMsg{
			request: request.Request, ownerToken: request.OwnerToken, epicKey: request.EpicKey,
			repoPath: repoPath, epicID: epicID, sourceFlowID: request.SourceFlowID,
			disposition: disposition, status: status,
		}
	}
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: repoPath, EpicID: epicID}
		persisted, err := haltProgression(flowstore.EpicProgressionHaltUpdate{Key: key, Halt: halt, ExpectedActivation: activation})
		if err == nil {
			// Halt is sticky, so an already-halted row keeps its first cause and
			// this attempt's tuple is discarded. Announce what the store retained,
			// never what this poll observed.
			cause := halt
			if persisted.Halt != nil {
				cause = *persisted.Halt
			}
			return result(epicProgressionHaltPersisted, epicProgressionHaltStatus(epicID, cause))
		}
		if errors.Is(err, flowstore.ErrEpicProgressionActivationChanged) {
			return result(epicProgressionHaltInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
		}
		authoritative, found, readErr := readProgression(key)
		if readErr != nil {
			return result(epicProgressionHaltRetryable, fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", epicID, readErr))
		}
		if found && authoritative.Halt != nil {
			// A write can report an error after the row became durable. The read
			// is the tiebreaker: a halted row is a halt, announced from its own
			// tuple rather than the generic inactive line.
			return result(epicProgressionHaltPersisted, epicProgressionHaltStatus(epicID, *authoritative.Halt))
		}
		if found && authoritative.Enabled && !authoritative.Done && authoritative.UpdatedAt.Equal(activation) {
			return result(epicProgressionHaltRetryable, fmt.Sprintf("Could not halt auto-progression for epic %s: %v", epicID, err))
		}
		return result(epicProgressionHaltInactive, fmt.Sprintf("Auto-progression for epic %s is no longer active", epicID))
	}
}

// handleEpicProgressionHaltResult drops the epic's runtime tracking once the
// halt is durable. That teardown is what mechanically guarantees no further
// child Flow is created until the user re-enables progression.
func (m Model) handleEpicProgressionHaltResult(msg epicProgressionHaltResultMsg) (Model, tea.Cmd) {
	active := m.activeEpicProgressionHalt
	baseline, baselineFound := m.epicProgressionBaselines[msg.epicKey]
	current := msg.request != 0 && active.Request == msg.request && active.OwnerToken == msg.ownerToken &&
		active.EpicKey == msg.epicKey && active.SourceFlowID == msg.sourceFlowID &&
		baselineFound && baseline.FlowID == msg.sourceFlowID &&
		m.flowPreparationMatches(flowPreparationEpicHalt, msg.ownerToken)
	// Releasing on every path is free and cannot steal: releaseFlowPreparation
	// no-ops unless this exact kind and token still own admission.
	m = m.releaseFlowPreparation(flowPreparationEpicHalt, msg.ownerToken)
	if !current {
		return m, nil
	}
	m.activeEpicProgressionHalt = epicProgressionHaltRequest{}
	switch msg.disposition {
	case epicProgressionHaltPersisted, epicProgressionHaltInactive:
		delete(m.epicProgressionBaselines, msg.epicKey)
		delete(m.epicProgressionBaselineMinimumRequests, msg.epicKey)
		delete(m.epicProgressionOwnedSuccessors, msg.epicKey)
	}
	var statusCmd tea.Cmd
	if strings.TrimSpace(msg.status) != "" {
		// A halt is the class of alert the transition rank exists to protect: it
		// is announced once, and then the baseline is gone.
		m, statusCmd = m.setAutoAdvanceStatus(msg.status)
	}
	var progressionRefreshCmd tea.Cmd
	target := m.beadExpansion.target
	if target.token != 0 && filepath.Clean(target.repoPath) == msg.repoPath && strings.TrimSpace(target.epicID) == msg.epicID {
		progressionRefreshCmd = m.readEpicProgressionCmd(target)
	}
	return m, batchNonNil(statusCmd, progressionRefreshCmd)
}

func rejectOwnedEpicProgressionSuccessor(flow flowstore.FlowRecord) string {
	flowID := strings.TrimSpace(flow.FlowID)
	if flow.PreparedAt == nil {
		return fmt.Sprintf("Owned successor Flow %s is missing its preparation receipt and blocks auto-progression", flowID)
	}
	if flowstore.FlowClosed(flow) {
		return fmt.Sprintf("Owned successor Flow %s is closed and blocks auto-progression", flowID)
	}
	status := strings.TrimSpace(flow.Status)
	if status == "" {
		status = flowstore.DeriveStatus(flow)
	}
	if status != flowstore.StatusPending {
		return fmt.Sprintf("Owned successor Flow %s is %s and blocks auto-progression", flowID, status)
	}
	return ""
}

func (m Model) handleEpicProgressionAdvanceResult(msg epicProgressionAdvanceResultMsg) (Model, tea.Cmd) {
	active := m.activeEpicProgressionAdvance
	baseline, baselineFound := m.epicProgressionBaselines[msg.epicKey]
	current := msg.request != 0 && active.Request == msg.request && active.OwnerToken == msg.ownerToken &&
		active.EpicKey == msg.epicKey && active.SourceFlowID == msg.sourceFlowID &&
		baselineFound && baseline.FlowID == msg.sourceFlowID &&
		m.flowPreparationMatches(flowPreparationEpicAdvance, msg.ownerToken)
	if !current {
		return m, nil
	}
	if msg.degradation != nil {
		m = m.setFlowDegradation(ui.ModeFlows, msg.repoPath, msg.degradation)
	}
	if msg.disposition == epicProgressionAdvanceSelected {
		// The advance is not over: it continues as one create-then-launch
		// attempt that keeps this request's preparation admission until the
		// create pipeline releases it.
		return m.requestEpicProgressionChildLaunch(msg)
	}
	switch msg.disposition {
	case epicProgressionAdvanceInactive, epicProgressionAdvanceExhausted, epicProgressionAdvanceExhaustedUnknown:
		delete(m.epicProgressionBaselines, msg.epicKey)
		delete(m.epicProgressionBaselineMinimumRequests, msg.epicKey)
		delete(m.epicProgressionOwnedSuccessors, msg.epicKey)
	}
	m.activeEpicProgressionAdvance = epicProgressionAdvanceRequest{}
	m = m.releaseFlowPreparation(flowPreparationEpicAdvance, msg.ownerToken)
	var statusCmd tea.Cmd
	if strings.TrimSpace(msg.status) != "" {
		m, statusCmd = m.setAutoAdvanceLaunchStatus(msg.status)
	}
	var progressionRefreshCmd tea.Cmd
	target := m.beadExpansion.target
	if target.token != 0 && filepath.Clean(target.repoPath) == msg.repoPath && strings.TrimSpace(target.epicID) == msg.epicID {
		progressionRefreshCmd = m.readEpicProgressionCmd(target)
	}
	return m, batchNonNil(statusCmd, progressionRefreshCmd)
}

func (m Model) epicProgressionKeysOwned() bool {
	terminalInputFocused := m.terminalEffectivelyExpanded() && m.activePane != ui.PaneRepos && m.terminalFocus == terminalFocusTerminal && m.hasActiveEmbeddedTerminal()
	if m.modal.IsOpen() || m.searchActive || terminalInputFocused || !m.contentListInputEligible() || !ui.IsBeadsMode(m.focusedMode()) {
		return false
	}
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return false
	}
	bead, ok := m.selectedVisibleBead()
	if !ok || !isEpicBead(bead) {
		return false
	}
	target := m.beadExpansion.target
	return target.token != 0 && target.repoPath == repoPath && target.mode == m.focusedMode() && target.epicID == bead.ID
}

func (m Model) canDisableEpicProgression() bool {
	projection := m.beadExpansion.projection
	return m.epicProgressionKeysOwned() && !m.flowPreparationAdmission && projection.ProgressionKnown && projection.ProgressionEnabled
}

func (m Model) canEnableEpicProgression() bool {
	projection := m.beadExpansion.projection
	return m.epicProgressionKeysOwned() && !m.flowPreparationAdmission && projection.ProgressionKnown && !projection.ProgressionEnabled &&
		projection.State == ui.BeadExpansionLoaded && projection.ReadinessKnown
}

func (m Model) handleToggleEpicProgression() (Model, tea.Cmd) {
	if !m.canDisableEpicProgression() && !m.canEnableEpicProgression() {
		return m, nil
	}
	target := m.beadExpansion.target
	projection := cloneBeadExpansion(m.beadExpansion.projection)
	var token uint64
	var admitted bool
	m, token, admitted = m.acquireFlowPreparation(flowPreparationEpicToggle)
	if !admitted {
		return m, nil
	}
	ownedCmd := func(cmd tea.Cmd) tea.Cmd {
		return func() tea.Msg {
			msg := cmd()
			if result, ok := msg.(epicProgressionToggleResultMsg); ok {
				result.preparationKind = flowPreparationEpicToggle
				result.preparationToken = token
				return result
			}
			return msg
		}
	}
	if projection.ProgressionEnabled {
		return m, ownedCmd(m.disableEpicProgressionCmd(target))
	}
	return m, ownedCmd(m.enableEpicProgressionCmd(target, projection))
}

func (m Model) disableEpicProgressionCmd(target beadExpansionTarget) tea.Cmd {
	setProgression := m.setEpicProgression
	readProgression := m.readEpicProgression
	return func() tea.Msg {
		key := flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID}
		progression, err := setProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false, Done: false})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Disabled auto-progression for epic %s", target.epicID)}
		}
		authoritative, found, readErr := readProgression(key)
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, known: false,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr)}
		}
		if !found {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Auto-progression for epic %s is off; disable outcome could not be confirmed", target.epicID)}
		}
		if authoritative.Done && !authoritative.Enabled && authoritative.Halt == nil {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Auto-progression for epic %s completed before disable could be confirmed", target.epicID)}
		}
		if authoritative.Halt != nil && !authoritative.Enabled {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Auto-progression for epic %s is halted; disable outcome could not be confirmed", target.epicID)}
		}
		if !authoritative.Enabled {
			return epicProgressionToggleResultMsg{target: target, progression: authoritative, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("Auto-progression for epic %s is off; disable outcome could not be confirmed", target.epicID)}
		}
		return epicProgressionToggleResultMsg{target: target, progression: authoritative, enabled: true, known: true,
			status: fmt.Sprintf("Could not disable auto-progression for epic %s: %v", target.epicID, err)}
	}
}

func (m Model) enableEpicProgressionCmd(target beadExpansionTarget, projection ui.BeadExpansion) tea.Cmd {
	listFlows := m.listFlows
	listChildren := m.listChildrenBeads
	listReady := m.listReadyBeads
	claimBead := m.claimBead
	createFlow := m.createFlow
	reserveFlow := m.reserveFlowPreparation
	enableProgression := m.enableEpicProgression
	readProgression := m.readEpicProgression
	readFlow := m.launchSeams.ReadFlow
	command, launchModel, reasoningEffort := m.flowLaunchAgentSettings()
	preferences := m.agentPreferences()
	return func() tea.Msg {
		flows, err := listFlows(flowstore.FlowFilter{RepoPath: target.repoPath})
		if err != nil {
			return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Could not check existing child Flows: %v", err)}
		}
		directChildren, err := listChildren(target.repoPath, target.epicID)
		if err != nil {
			return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Could not refresh direct children for epic %s; auto-progression remains off: %v", target.epicID, err)}
		}
		childID, childTitle := epicProgressionRecoveryChild(target, directChildren, flows)
		recovered := childID != ""
		if !recovered {
			for _, child := range projection.Children {
				if projection.ReadyIDs[child.ID] {
					childID = strings.TrimSpace(child.ID)
					childTitle = strings.TrimSpace(child.Title)
					break
				}
			}
		}
		if childID == "" {
			return epicProgressionToggleResultMsg{target: target, known: true,
				baselineDisposition: epicProgressionBaselineRemove,
				status:              fmt.Sprintf("No ready child for epic %s; auto-progression remains off", target.epicID)}
		}
		ignoredConsumed := !recovered && hasConsumedProgressionMarker(target, flows)
		if ignoredConsumed {
			if detail := confirmConsumedProgressionMarkers(target, childID, flows, readFlow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: detail}
			}
		}
		link := flowstore.BeadLink{ID: childID, EpicID: target.epicID}
		matches := make([]flowstore.FlowRecord, 0, 1)
		for _, flow := range flows {
			if filepath.Clean(flow.RepoPath) == filepath.Clean(target.repoPath) && flow.Bead == link {
				matches = append(matches, flow)
			}
		}
		if len(matches) > 1 {
			return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
				status: fmt.Sprintf("Multiple Flows exist for child %s; auto-progression remains off", childID)}
		}
		var flow flowstore.FlowRecord
		var authoritative flowstore.FlowRecord
		var release func()
		claimed := false
		if len(matches) == 1 {
			flow = matches[0]
			if flow.ProgressionClaim {
				reserved, reservedRelease, err := reserveFlow(flow.FlowID)
				if err != nil {
					return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not reserve Flow %s before claim recovery; auto-progression remains off: %v", flow.FlowID, err)}
				}
				release = reservedRelease
				// A migrated pre-receipt marked Flow carries no
				// PreparationGeneration or nonce at all, so two matching empty
				// identities are not evidence of drift on their own. Prefer
				// the nonce whenever either record carries one; otherwise
				// compare the legacy generation. Any inequality, including an
				// empty identity turning nonempty, still proves a same-ID
				// replacement and must be rejected.
				if reserved.FlowID != flow.FlowID || filepath.Clean(reserved.RepoPath) != filepath.Clean(target.repoPath) || reserved.Bead != link ||
					!reserved.ProgressionClaim || !flowstore.SamePreparationIdentity(reserved, flow) {
					return epicProgressionToggleResultMsg{target: target, flow: reserved, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Flow %s changed before claim recovery; auto-progression remains off", flow.FlowID)}
				}
				flow = reserved
				authoritative = reserved
				children, err := listChildren(target.repoPath, target.epicID)
				if err != nil {
					return epicProgressionToggleResultMsg{target: target, flow: flow, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not revalidate child %s before claim recovery; auto-progression remains off: %v", childID, err)}
				}
				direct := false
				for _, child := range children {
					if strings.TrimSpace(child.ID) == childID {
						direct = true
						childTitle = strings.TrimSpace(child.Title)
						break
					}
				}
				if !direct {
					return epicProgressionToggleResultMsg{target: target, flow: flow, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Child %s is no longer a direct child of epic %s; auto-progression remains off", childID, target.epicID)}
				}
				if err := claimBead(target.repoPath, childID); err != nil {
					return epicProgressionToggleResultMsg{target: target, flow: flow, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not reconcile claim for child %s; auto-progression remains off: %v", childID, err), presentStatusOnStale: true}
				}
				claimed = true
			}
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: detail, presentStatusOnStale: claimed}
			}
		} else {
			children, err := listChildren(target.repoPath, target.epicID)
			if err != nil {
				return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Could not revalidate child %s before claim; auto-progression remains off: %v", childID, err)}
			}
			ready, err := listReady(target.repoPath)
			if err != nil {
				return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Could not revalidate child %s before claim; auto-progression remains off: %v", childID, err)}
			}
			direct := false
			for _, child := range children {
				if strings.TrimSpace(child.ID) == childID {
					direct = true
					childTitle = strings.TrimSpace(child.Title)
					break
				}
			}
			readyNow := false
			for _, bead := range ready {
				if strings.TrimSpace(bead.ID) == childID {
					readyNow = true
					break
				}
			}
			if !direct || !readyNow {
				return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Child %s is no longer a ready direct child of epic %s; auto-progression remains off", childID, target.epicID)}
			}
			title := childID + ": " + childTitle
			instructions := fmt.Sprintf("Use Bead %s as the durable source of requirements. Read it with `bd show -- %s` before planning or implementation.", childID, childID)
			claimAttempted := false
			var claimErr error
			var siblingMarkerDetail string
			result, createErr := createFlow(FlowStartRequest{
				RepoPath: target.repoPath, Title: title, Instructions: instructions, Bead: link,
				AgentCommand: command, Model: launchModel, ReasoningEffort: reasoningEffort,
				AgentPreferences: preferences, AgentPreferencesProvided: true,
				AfterFlowPersisted: func() error {
					if ignoredConsumed {
						if detail := confirmConsumedProgressionMarkersNow(target, childID, listFlows, readFlow); detail != "" {
							siblingMarkerDetail = detail
							return fmt.Errorf("%s", detail)
						}
					}
					claimAttempted = true
					claimErr = claimBead(target.repoPath, childID)
					claimed = claimErr == nil
					return claimErr
				},
			})
			flow = result.Flow
			if siblingMarkerDetail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: siblingMarkerDetail}
			}
			// The pre-check above matches flow.Bead == link exactly, so a Flow
			// for this Bead under a different (or empty) epic slips past it and
			// reaches the guard. A newer-schema row for the same Bead can also
			// land between listFlows and create. Without this branch either
			// refusal would surface through the !claimAttempted arm as a
			// Bead-admission message — AfterFlowPersisted never runs on a
			// guard refusal — which describes the wrong failure. Enabling
			// still declines; the user is just told why. The unreadable
			// variant names no decodable Flow, so it surfaces the store's own
			// text, which already names the row to repair.
			if flowstore.IsBeadFlowRefusal(createErr) {
				status := createErr.Error()
				if existing, conflict := flowstore.ActiveBeadFlow(createErr); conflict {
					status = conflictStatus(existing, false)
				}
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: status}
			}
			if claimErr != nil {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Could not claim child %s; auto-progression remains off: %v", childID, createErr), presentStatusOnStale: true}
			}
			if !claimAttempted {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Could not admit child %s before Flow preparation; auto-progression remains off: %v", childID, createErr)}
			}
			if createErr != nil {
				if strings.TrimSpace(flow.FlowID) == "" {
					return epicProgressionToggleResultMsg{target: target, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not prepare Flow for child %s: %v", childID, createErr), presentStatusOnStale: claimed}
				}
				authoritative, readErr := readFlow(flow.FlowID)
				if readErr != nil {
					return epicProgressionToggleResultMsg{target: target, flow: flow, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Could not confirm preparation for Flow %s; auto-progression state is unknown", flow.FlowID), presentStatusOnStale: claimed}
				}
				if authoritative.PreparedAt == nil {
					return epicProgressionToggleResultMsg{target: target, flow: authoritative, known: true, baselineDisposition: epicProgressionBaselineRemove,
						status: fmt.Sprintf("Flow %s exists but preparation is incomplete; auto-progression remains off", flow.FlowID), presentStatusOnStale: claimed}
				}
				flow = authoritative
			}
			if detail := rejectEpicProgressionCandidate(flow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: detail, presentStatusOnStale: claimed}
			}
		}

		if ignoredConsumed {
			if detail := confirmConsumedProgressionMarkersNow(target, childID, listFlows, readFlow); detail != "" {
				return epicProgressionToggleResultMsg{target: target, flow: flow, release: release, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: detail, presentStatusOnStale: claimed}
			}
		}
		if release == nil {
			var err error
			authoritative, release, err = reserveFlow(flow.FlowID)
			if err != nil {
				return epicProgressionToggleResultMsg{target: target, flow: flow, known: true, baselineDisposition: epicProgressionBaselineRemove,
					status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", flow.FlowID, err), presentStatusOnStale: claimed}
			}
		}
		progression, enabledFlow, err := enableProgression(flowstore.PreparedEpicProgressionUpdate{
			FlowID: authoritative.FlowID,
			Key:    flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID},
			Bead:   link,
		})
		if err == nil {
			return epicProgressionToggleResultMsg{target: target, progression: progression, flow: enabledFlow,
				baselineDisposition: epicProgressionBaselineReplace, enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, enabledFlow.FlowID), presentStatusOnStale: claimed}
		}
		resultFlow := enabledFlow
		if strings.TrimSpace(resultFlow.FlowID) == "" {
			resultFlow = authoritative
		}
		confirmed, found, readErr := readProgression(flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID})
		if readErr != nil {
			return epicProgressionToggleResultMsg{target: target, flow: resultFlow, release: release,
				status: fmt.Sprintf("Could not confirm auto-progression state for epic %s: %v", target.epicID, readErr), presentStatusOnStale: claimed}
		}
		if found && confirmed.Enabled && !confirmed.Done {
			if !flowstore.IsPreparedEpicProgressionCommitUnknown(err) {
				return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
					enabled: true, known: true, release: release,
					status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", resultFlow.FlowID, err), presentStatusOnStale: claimed}
			}
			return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
				baselineDisposition: epicProgressionBaselineReplace, enabled: true, known: true, release: release,
				status: fmt.Sprintf("Enabled auto-progression for epic %s; Flow %s is prepared", target.epicID, resultFlow.FlowID), presentStatusOnStale: claimed}
		}
		return epicProgressionToggleResultMsg{target: target, progression: confirmed, flow: resultFlow,
			baselineDisposition: epicProgressionBaselineRemove, known: true, release: release,
			status: fmt.Sprintf("Flow %s was prepared, but enabling auto-progression failed: %v", resultFlow.FlowID, err), presentStatusOnStale: claimed}
	}
}

func epicProgressionRecoveryChild(target beadExpansionTarget, children []beadsquery.Bead, flows []flowstore.FlowRecord) (string, string) {
	for _, child := range children {
		childID := strings.TrimSpace(child.ID)
		if childID == "" {
			continue
		}
		link := flowstore.BeadLink{ID: childID, EpicID: target.epicID}
		for _, flow := range flows {
			if filepath.Clean(flow.RepoPath) == filepath.Clean(target.repoPath) && flow.Bead == link && flow.ProgressionClaim && epicProgressionRetryRelevant(flow) {
				return childID, strings.TrimSpace(child.Title)
			}
		}
	}
	return "", ""
}

// epicProgressionRetryRelevant reports whether a marked Flow should still be
// treated as its Bead's recovery candidate. Closed Flows are ignored, and so
// are consumed successful terminals (`completed`/`merged`) that remain
// deliberately open: those markers have already done their job, and treating
// them as retryable would make off/on recovery refuse enablement instead of
// selecting a later Ready sibling. A marked Flow that is running, blocked,
// abandoned, or otherwise mid-flight must still surface here so
// rejectEpicProgressionCandidate can refuse enablement against it. Filtering
// those out by status would make recovery silently fall through to a Ready
// sibling, claiming and enabling progression against it while stranding the
// mid-flight Flow's claim as an unrecoverable orphan.
func epicProgressionRetryRelevant(flow flowstore.FlowRecord) bool {
	return !flowstore.FlowClosed(flow) && !epicProgressionConsumedMarker(flow)
}

func epicProgressionConsumedMarker(flow flowstore.FlowRecord) bool {
	if flowstore.FlowClosed(flow) || !flow.ProgressionClaim {
		return false
	}
	status := strings.TrimSpace(flow.Status)
	if status == "" {
		status = flowstore.DeriveStatus(flow)
	}
	return status == flowstore.StatusCompleted || status == flowstore.StatusMerged
}

func hasConsumedProgressionMarker(target beadExpansionTarget, flows []flowstore.FlowRecord) bool {
	for _, flow := range flows {
		if filepath.Clean(flow.RepoPath) == filepath.Clean(target.repoPath) && flow.Bead.EpicID == target.epicID && epicProgressionConsumedMarker(flow) {
			return true
		}
	}
	return false
}

func confirmConsumedProgressionMarkers(target beadExpansionTarget, exceptChildID string, flows []flowstore.FlowRecord, readFlow func(string) (flowstore.FlowRecord, error)) string {
	return confirmProgressionMarkersStillConsumed(target, exceptChildID, flows, readFlow)
}

func confirmConsumedProgressionMarkersNow(target beadExpansionTarget, exceptChildID string, listFlows func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error), readFlow func(string) (flowstore.FlowRecord, error)) string {
	if listFlows == nil {
		return "Could not refresh child Flows before sibling claim; auto-progression remains off"
	}
	flows, err := listFlows(flowstore.FlowFilter{RepoPath: target.repoPath})
	if err != nil {
		return fmt.Sprintf("Could not refresh child Flows before sibling claim; auto-progression remains off: %v", err)
	}
	return confirmProgressionMarkersStillConsumed(target, exceptChildID, flows, readFlow)
}

func confirmProgressionMarkersStillConsumed(target beadExpansionTarget, exceptChildID string, flows []flowstore.FlowRecord, readFlow func(string) (flowstore.FlowRecord, error)) string {
	for _, flow := range flows {
		if filepath.Clean(flow.RepoPath) != filepath.Clean(target.repoPath) || flow.Bead.EpicID != target.epicID || flow.Bead.ID == exceptChildID || !flow.ProgressionClaim {
			continue
		}
		if readFlow == nil {
			return fmt.Sprintf("Could not confirm consumed claim marker on Flow %s; auto-progression remains off", flow.FlowID)
		}
		fresh, err := readFlow(flow.FlowID)
		if err != nil {
			return fmt.Sprintf("Could not confirm consumed claim marker on Flow %s; auto-progression remains off: %v", flow.FlowID, err)
		}
		if epicProgressionRetryRelevant(fresh) {
			return fmt.Sprintf("Flow %s changed before sibling claim; auto-progression remains off", flow.FlowID)
		}
	}
	return ""
}

func rejectEpicProgressionCandidate(flow flowstore.FlowRecord) string {
	flowID := strings.TrimSpace(flow.FlowID)
	if flowstore.FlowClosed(flow) {
		return fmt.Sprintf("Flow %s is closed; auto-progression remains off", flowID)
	}
	status := strings.TrimSpace(flow.Status)
	if status == "" {
		status = flowstore.DeriveStatus(flow)
	}
	switch status {
	case flowstore.StatusNeedsAttention, flowstore.StatusBlocked, flowstore.StatusAbandoned,
		flowstore.StatusCompleted, flowstore.StatusMerged, flowstore.StatusInProgress:
		return fmt.Sprintf("Flow %s is %s; auto-progression remains off", flowID, status)
	case flowstore.StatusPending:
		if flow.PreparedAt == nil {
			return fmt.Sprintf("Flow %s exists but preparation is incomplete; auto-progression remains off", flowID)
		}
		return ""
	default:
		return fmt.Sprintf("Flow %s is %s; auto-progression remains off", flowID, status)
	}
}

func (m Model) handleEpicProgressionToggleResult(msg epicProgressionToggleResultMsg) (Model, tea.Cmd) {
	key := epicProgressionBaselineKey(msg.target.repoPath, msg.target.epicID)
	currentOwner := (msg.preparationToken == 0 && m.flowPreparationOwner.Token == 0) ||
		m.flowPreparationMatches(msg.preparationKind, msg.preparationToken)
	if currentOwner {
		switch msg.baselineDisposition {
		case epicProgressionBaselineReplace:
			if m.epicProgressionBaselines == nil {
				m.epicProgressionBaselines = make(map[string]epicProgressionBaseline)
			}
			if m.epicProgressionBaselineMinimumRequests == nil {
				m.epicProgressionBaselineMinimumRequests = make(map[string]uint64)
			}
			m.epicProgressionBaselines[key] = epicProgressionBaseline{FlowRecord: msg.flow, Activation: msg.progression.UpdatedAt}
			m.epicProgressionBaselineMinimumRequests[key] = m.autoAdvanceRequestSeq + 1
			delete(m.epicProgressionOwnedSuccessors, key)
		case epicProgressionBaselineRemove:
			delete(m.epicProgressionBaselines, key)
			delete(m.epicProgressionBaselineMinimumRequests, key)
			delete(m.epicProgressionOwnedSuccessors, key)
		}
	}
	if msg.release != nil {
		msg.release()
	}
	m = m.releaseFlowPreparation(msg.preparationKind, msg.preparationToken)
	if !currentOwner {
		var flowRefreshCmd, progressionRefreshCmd tea.Cmd
		if m.flowRefreshSurfaceVisible() && msg.flow.FlowID != "" {
			m, flowRefreshCmd = m.startFlowSurfaceFetch()
		}
		current := m.beadExpansion.target
		if current.token != 0 && filepath.Clean(current.repoPath) == filepath.Clean(msg.target.repoPath) && current.epicID == msg.target.epicID {
			progressionRefreshCmd = m.readEpicProgressionCmd(current)
		}
		return m, batchNonNil(flowRefreshCmd, progressionRefreshCmd)
	}
	var flowRefreshCmd tea.Cmd
	if m.flowRefreshSurfaceVisible() && msg.flow.FlowID != "" {
		m, flowRefreshCmd = m.startFlowSurfaceFetch()
	}
	if msg.target != m.beadExpansion.target {
		if msg.presentStatusOnStale {
			m = m.setStatus(statusOther, msg.status)
		}
		var progressionRefreshCmd tea.Cmd
		current := m.beadExpansion.target
		if current.token != 0 && current.repoPath == msg.target.repoPath && current.epicID == msg.target.epicID {
			progressionRefreshCmd = m.readEpicProgressionCmd(current)
		}
		return m, batchNonNil(flowRefreshCmd, progressionRefreshCmd)
	}
	projection := cloneBeadExpansion(m.beadExpansion.projection)
	projection.ProgressionKnown = msg.known
	projection.ProgressionEnabled = msg.known && msg.enabled
	if !msg.known {
		projection.ProgressionDetail = msg.status
	} else {
		projection.ProgressionDetail = ""
	}
	// Enabled progression can never be halted, so a confirmed enable clears the
	// halt line in the same frame rather than waiting for the re-read below.
	// Every other branch leaves it to the authoritative read: the enable-failure
	// branches report known state without reading progression at all, and
	// deriving halt state from them would render a halted epic as not-halted.
	if msg.known && msg.enabled {
		projection.ProgressionHaltDetail = ""
	}
	m.beadExpansion.projection = projection
	m = m.reflowBeadExpansionPane()
	m = m.setStatus(statusOther, msg.status)
	var progressionRefreshCmd tea.Cmd
	if current := m.beadExpansion.target; current.token != 0 {
		progressionRefreshCmd = m.readEpicProgressionCmd(current)
	}
	return m, batchNonNil(flowRefreshCmd, progressionRefreshCmd)
}

func (m Model) readEpicProgressionCmd(target beadExpansionTarget) tea.Cmd {
	readProgression := m.readEpicProgression
	return func() tea.Msg {
		progression, found, err := readProgression(flowstore.EpicProgressionKey{RepoPath: target.repoPath, EpicID: target.epicID})
		return beadProgressionResultMsg{target: target, progression: progression, found: found, err: err}
	}
}

func epicProgressionBaselineKey(repoPath, epicID string) string {
	return filepath.Clean(repoPath) + "\x00" + strings.TrimSpace(epicID)
}

// epicProgressionHaltStatus formats the one halt notification, always from the
// tuple the store owns, so every path reports the cause that is actually
// durable.
func epicProgressionHaltStatus(epicID string, cause flowstore.EpicProgressionHalt) string {
	return fmt.Sprintf("Auto-progression halted for epic %s: child %s is %s", epicID, cause.ChildBeadID, cause.Status)
}
