package flowstore

// PhaseIsActionable reports whether a phase is one the Flow is currently
// sitting on: ready, running, needs_attention, or blocked. It is deliberately
// broader than "executing" — blocked and needs-attention phases are actionable
// because they are where a human or agent must intervene next.
func PhaseIsActionable(phase FlowPhase) bool {
	switch phase.Status {
	case PhaseReady, PhaseRunning, PhaseNeedsAttention, PhaseBlocked:
		return true
	default:
		return false
	}
}

// NextActionablePhase returns the first actionable phase in graph order
// (OrderedPhases), reporting false when the Flow has none.
func NextActionablePhase(record FlowRecord) (FlowPhase, bool) {
	for _, phase := range OrderedPhases(record.Phases) {
		if PhaseIsActionable(phase) {
			return phase, true
		}
	}
	return FlowPhase{}, false
}
