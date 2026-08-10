package model

import "strings"

type flowLaunchSource string

const (
	flowLaunchSourceCreatePhase   flowLaunchSource = "create-phase"
	flowLaunchSourcePhase         flowLaunchSource = "phase"
	flowLaunchSourceAutoPhase     flowLaunchSource = "auto-phase"
	flowLaunchSourcePhaseResume   flowLaunchSource = "phase-resume"
	flowLaunchSourceRepair        flowLaunchSource = "repair"
	flowLaunchSourceSessionResume flowLaunchSource = "session-resume"
	flowLaunchSourceWorktreeAgent flowLaunchSource = "worktree-agent"
)

type flowLaunchLease struct {
	Token          string
	Source         flowLaunchSource
	HandoffPending bool
	FailurePending bool
}

func (m Model) acquireFlowLaunchLease(flowID, token string, source flowLaunchSource) (Model, bool) {
	flowID = strings.TrimSpace(flowID)
	token = strings.TrimSpace(token)
	if flowID == "" || token == "" || source == "" {
		return m, false
	}
	if _, occupied := m.flowLaunchLeases[flowID]; occupied {
		return m, false
	}
	leases := make(map[string]flowLaunchLease, len(m.flowLaunchLeases)+1)
	for id, lease := range m.flowLaunchLeases {
		leases[id] = lease
	}
	leases[flowID] = flowLaunchLease{Token: token, Source: source}
	m.flowLaunchLeases = leases
	return m, true
}

func (m Model) flowLaunchLease(flowID string) (flowLaunchLease, bool) {
	lease, ok := m.flowLaunchLeases[strings.TrimSpace(flowID)]
	return lease, ok
}

func (m Model) matchingFlowLaunchLease(flowID, token string, source flowLaunchSource) bool {
	lease, ok := m.flowLaunchLease(flowID)
	return ok && lease.Token == strings.TrimSpace(token) && lease.Source == source && !lease.HandoffPending && !lease.FailurePending
}

func (m Model) beginExternalFlowHandoff(flowID, token string, source flowLaunchSource) (Model, bool) {
	flowID = strings.TrimSpace(flowID)
	token = strings.TrimSpace(token)
	lease, ok := m.flowLaunchLeases[flowID]
	if !ok || token == "" || lease.Token != token || lease.Source != source || lease.HandoffPending || lease.FailurePending {
		return m, false
	}
	leases := make(map[string]flowLaunchLease, len(m.flowLaunchLeases))
	for id, existing := range m.flowLaunchLeases {
		leases[id] = existing
	}
	lease.HandoffPending = true
	leases[flowID] = lease
	m.flowLaunchLeases = leases
	return m, true
}

func (m Model) beginFlowLaunchFailure(flowID, token string) (Model, bool) {
	flowID = strings.TrimSpace(flowID)
	token = strings.TrimSpace(token)
	lease, ok := m.flowLaunchLeases[flowID]
	if !ok || token == "" || lease.Token != token || lease.FailurePending {
		return m, false
	}
	leases := make(map[string]flowLaunchLease, len(m.flowLaunchLeases))
	for id, existing := range m.flowLaunchLeases {
		leases[id] = existing
	}
	lease.FailurePending = true
	leases[flowID] = lease
	m.flowLaunchLeases = leases
	return m, true
}

func (m Model) flowLaunchLeaseOccupied(flowID string) bool {
	_, ok := m.flowLaunchLease(flowID)
	return ok
}

func (m Model) releaseFlowLaunchLease(flowID, token string) Model {
	flowID = strings.TrimSpace(flowID)
	token = strings.TrimSpace(token)
	lease, ok := m.flowLaunchLeases[flowID]
	if !ok || token == "" || lease.Token != token {
		return m
	}
	leases := make(map[string]flowLaunchLease, len(m.flowLaunchLeases)-1)
	for id, existing := range m.flowLaunchLeases {
		if id != flowID {
			leases[id] = existing
		}
	}
	if len(leases) == 0 {
		leases = nil
	}
	m.flowLaunchLeases = leases
	return m
}
