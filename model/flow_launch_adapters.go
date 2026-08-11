package model

import (
	"strings"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// flowLaunchSeams is the authoritative boundary the launch lifecycle reads and
// writes through. It is built once in NewWithOptions and stored on the Model so
// the lifecycle never reaches for a pane snapshot or a package-level store.
type flowLaunchSeams struct {
	ReadFlow         func(flowID string) (flowstore.FlowRecord, error)
	ListFlowSessions func(flowID string) ([]sessions.SessionRecord, error)
	AddPhaseLaunchID func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	SetPhase         func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	PlanMarkdownPath func(planID string) (string, error)
	ReadPlan         func(planID string) (string, error)
	NewLaunchID      func() string
}

// newFlowLaunchSeams wires the lifecycle onto the already-resolved Model seams.
// ListFlowSessions filters client-side because sessions.SessionFilter has no
// FlowID field; adding one would be a public API change in another package.
func newFlowLaunchSeams(
	readFlow func(string) (flowstore.FlowRecord, error),
	listSessions func(sessions.SessionFilter) ([]sessions.SessionRecord, error),
	addPhaseLaunchID func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error),
	setPhase func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error),
	planMarkdownPath func(string) (string, error),
	readPlan func(string) (string, error),
) flowLaunchSeams {
	return flowLaunchSeams{
		ReadFlow: readFlow,
		ListFlowSessions: func(flowID string) ([]sessions.SessionRecord, error) {
			flowID = strings.TrimSpace(flowID)
			if flowID == "" || listSessions == nil {
				return nil, nil
			}
			all, err := listSessions(sessions.SessionFilter{})
			if err != nil {
				return nil, err
			}
			var out []sessions.SessionRecord
			for _, record := range all {
				if strings.TrimSpace(record.FlowID) == flowID {
					out = append(out, record)
				}
			}
			return out, nil
		},
		AddPhaseLaunchID: addPhaseLaunchID,
		SetPhase:         setPhase,
		PlanMarkdownPath: planMarkdownPath,
		ReadPlan:         readPlan,
		NewLaunchID:      newLaunchID,
	}
}
