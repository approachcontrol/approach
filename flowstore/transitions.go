package flowstore

// phaseTransitions is the canonical Flow phase transition table. Keys are
// current phase statuses; values are the agent-settable statuses reachable
// from that state, in canonical order. Readiness (pending -> ready) is derived
// by wtui and never settable, so it does not appear as a target here.
var phaseTransitions = map[string][]string{
	PhasePending:        {PhaseSkipped},
	PhaseReady:          {PhaseRunning, PhaseNeedsAttention, PhaseCompleted, PhaseBlocked, PhaseSkipped},
	PhaseRunning:        {PhaseNeedsAttention, PhaseCompleted, PhaseBlocked, PhaseSkipped},
	PhaseNeedsAttention: {PhaseRunning, PhaseSkipped},
	PhaseBlocked:        {PhaseRunning, PhaseSkipped},
	PhaseCompleted:      {PhaseRunning},
	PhaseSkipped:        {PhaseRunning},
}

// agentSettablePhaseStatuses lists every status agents may pass to phase
// updates, in canonical order. Derived statuses (pending, ready) are excluded.
var agentSettablePhaseStatuses = []string{
	PhaseRunning,
	PhaseNeedsAttention,
	PhaseCompleted,
	PhaseBlocked,
	PhaseSkipped,
}

// AgentSettablePhaseStatuses returns the canonical list of statuses agents may
// set on a Flow phase, in canonical order.
func AgentSettablePhaseStatuses() []string {
	out := make([]string, len(agentSettablePhaseStatuses))
	copy(out, agentSettablePhaseStatuses)
	return out
}

// AllowedNextPhaseStatuses returns the canonical agent-settable statuses a
// phase may transition to from current, in canonical order. It returns nil for
// unknown statuses. Same-status updates are idempotent no-ops handled
// separately and are not listed.
func AllowedNextPhaseStatuses(current string) []string {
	next, ok := phaseTransitions[current]
	if !ok {
		return nil
	}
	out := make([]string, len(next))
	copy(out, next)
	return out
}
