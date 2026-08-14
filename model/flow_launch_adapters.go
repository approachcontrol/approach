package model

import (
	"fmt"
	"os"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

// flowLaunchSeams is the authoritative boundary the launch lifecycle reads and
// writes through. It is built once in NewWithOptions and stored on the Model so
// the lifecycle never reaches for a pane snapshot or a package-level store.
type flowLaunchSeams struct {
	AllocateFlowID           func(title string) (string, error)
	CreateFlow               func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	ReadFlow                 func(flowID string) (flowstore.FlowRecord, error)
	ReadSession              func(sessions.Provider, string) (sessions.SessionRecord, error)
	ListFlowSessions         func(flowID string) ([]sessions.SessionRecord, error)
	ReserveLaunch            func(flowID string) (flowstore.FlowRecord, func(), error)
	CreateWorktree           func(repoPath, title, baseRef string) (actions.FlowWorktreeCreateResult, error)
	ResolveCommit            func(worktreePath string) string
	BootstrapHookForRepo     func(repoPath string) (actions.BootstrapHook, bool)
	RunBootstrapHook         func(actions.BootstrapContext, actions.BootstrapHook) error
	AddPhaseLaunchID         func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error)
	SetStartMetadata         func(flowstore.StartMetadataUpdate) (flowstore.FlowRecord, error)
	SetPhase                 func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error)
	PlanMarkdownPath         func(planID string) (string, error)
	ReadPlan                 func(planID string) (string, error)
	InspectWorktreeDirectory func(path string) error
	NewLaunchID              func() string
}

// newFlowLaunchSeams wires the lifecycle onto the already-resolved Model seams.
// ListFlowSessions filters client-side because sessions.SessionFilter has no
// FlowID field; adding one would be a public API change in another package.
func newFlowLaunchSeams(
	readFlow func(string) (flowstore.FlowRecord, error),
	readSession func(sessions.Provider, string) (sessions.SessionRecord, error),
	listSessions func(sessions.SessionFilter) ([]sessions.SessionRecord, error),
	addPhaseLaunchID func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error),
	setPhase func(flowstore.PhaseUpdate) (flowstore.FlowRecord, error),
	planMarkdownPath func(string) (string, error),
	readPlan func(string) (string, error),
) flowLaunchSeams {
	return flowLaunchSeams{
		ReadFlow:    readFlow,
		ReadSession: readSession,
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
		AddPhaseLaunchID:         addPhaseLaunchID,
		SetPhase:                 setPhase,
		PlanMarkdownPath:         planMarkdownPath,
		ReadPlan:                 readPlan,
		InspectWorktreeDirectory: inspectWorktreeDirectory,
		NewLaunchID:              newLaunchID,
	}
}

func (seams flowLaunchSeams) inspectWorktreeDirectory(path string) error {
	if seams.InspectWorktreeDirectory != nil {
		return seams.InspectWorktreeDirectory(path)
	}
	return inspectWorktreeDirectory(path)
}

func inspectWorktreeDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}
