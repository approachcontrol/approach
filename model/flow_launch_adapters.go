package model

import (
	"fmt"
	"os"
	"strings"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowoccupancy"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/sessions"
)

// flowOccupancyLeaseInspector adapts the model's preserved injection seam to
// the occupancy module. Production inspection requires the shared artifact
// root; injected inspectors own their own root contract.
type flowOccupancyLeaseInspector struct {
	root     string
	injected bool
	inspect  func(root, flowID string) (flowlease.LeaseState, error)
}

var _ flowoccupancy.LeaseInspector = flowOccupancyLeaseInspector{}

func (adapter flowOccupancyLeaseInspector) FlowLeaseOccupied(flowID string) (bool, error) {
	if strings.TrimSpace(adapter.root) == "" && !adapter.injected {
		return false, fmt.Errorf("Flow lease artifact root is unavailable")
	}
	if adapter.inspect == nil {
		return false, fmt.Errorf("Flow lease inspector is unavailable")
	}
	state, err := adapter.inspect(adapter.root, flowID)
	if err != nil {
		return false, err
	}
	switch state {
	case flowlease.Free:
		return false, nil
	case flowlease.Held:
		return true, nil
	default:
		return false, fmt.Errorf("invalid Flow lease state %d", state)
	}
}

// flowOccupancyRuntime keeps the occupancy package on the lifecycle's existing
// model-side seams without exposing the Model's maps or terminal slots.
type flowOccupancyRuntime struct {
	model Model
}

var _ flowoccupancy.Runtime = flowOccupancyRuntime{}

func (runtime flowOccupancyRuntime) AttemptHolder(flowID string) (actions.FlowLaunchRole, bool) {
	attempt, ok := runtime.model.flowLaunchAttempt(flowID)
	if !ok {
		return actions.RoleNone, false
	}
	return flowLaunchRole(attempt.Kind), true
}

func (runtime flowOccupancyRuntime) HasFlowTerminal(flowID string) bool {
	return runtime.model.hasFlowEmbeddedTerminalForFlow(flowID)
}

func (runtime flowOccupancyRuntime) HasNonRepairFlowTerminal(flowID string) bool {
	for _, slot := range runtime.model.embeddedTerminals {
		if slot.Scope == embeddedTerminalScopeFlow && slot.FlowID == flowID && slot.Terminal != nil && !slot.FlowRepair {
			return true
		}
	}
	return false
}

func (runtime flowOccupancyRuntime) HasRepairTerminal(flowID string) bool {
	return runtime.model.hasFlowRepairEmbeddedTerminalForFlow(flowID)
}

func (runtime flowOccupancyRuntime) HeadlessWritePending(flowID string) bool {
	return runtime.model.flowHeadlessWritePending(flowID)
}

func (runtime flowOccupancyRuntime) RepairDrainPending(flowID string) bool {
	return runtime.model.hasPendingRepairAutoDrainMarker(flowID)
}

type flowOccupancyFlowCache struct {
	record flowstore.FlowRecord
}

var _ flowoccupancy.FlowCache = flowOccupancyFlowCache{}

func (cache flowOccupancyFlowCache) CachedFlow(flowID string) (flowstore.FlowRecord, bool) {
	flowID = strings.TrimSpace(flowID)
	return cache.record, flowID != "" && strings.TrimSpace(cache.record.FlowID) == flowID
}

type flowOccupancySessionCache struct {
	flowSessions     []sessions.SessionRecord
	worktreeSessions []sessions.SessionRecord
}

var _ flowoccupancy.SessionCache = flowOccupancySessionCache{}

func (cache flowOccupancySessionCache) ActiveFlowSessions(flowID string) []sessions.SessionRecord {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return nil
	}
	var active []sessions.SessionRecord
	for _, records := range [][]sessions.SessionRecord{cache.flowSessions, cache.worktreeSessions} {
		for _, record := range records {
			if strings.TrimSpace(record.FlowID) == flowID && sessions.IsActive(record.Status, record.EndedAt) {
				active = append(active, record)
			}
		}
	}
	return active
}

func flowLaunchRole(kind flowLaunchKind) actions.FlowLaunchRole {
	switch kind {
	case flowLaunchKindManualPhase, flowLaunchKindAutoPhase:
		return actions.RoleTrackedPhase
	case flowLaunchKindCreatePhase:
		return actions.RoleCreatePhase
	case flowLaunchKindPhaseResume:
		return actions.RolePhaseResume
	case flowLaunchKindRepair:
		return actions.RoleRepair
	case flowLaunchKindAutofix:
		return actions.RoleAutofix
	case flowLaunchKindWorktreeAgent:
		return actions.RoleWorktreeAgent
	case flowLaunchKindSavedSessionResume:
		return actions.RoleSavedSessionResume
	default:
		return actions.RoleNone
	}
}

func (m Model) createFlowAdmissionOccupancy(flowID string) flowoccupancy.Verdict {
	return flowoccupancy.New(flowoccupancy.Sources{
		Runtime: flowOccupancyRuntime{model: m},
	}).Query(flowoccupancy.Query{
		FlowID: flowID,
		Purpose: flowoccupancy.Purpose{
			Role:  actions.RoleCreatePhase,
			Stage: flowoccupancy.StageAdmission,
		},
	})
}

func (m Model) trackedPhaseOccupancy(flowID string, stage flowoccupancy.Stage) flowoccupancy.Verdict {
	if strings.TrimSpace(flowID) == "" {
		return flowoccupancy.Free()
	}
	return flowoccupancy.Evaluate(flowoccupancy.Sources{
		Lease: flowOccupancyLeaseInspector{
			root:     m.sessionStateRoot,
			injected: m.leaseInspectInjected,
			inspect:  m.inspectFlowLease,
		},
		Runtime: flowOccupancyRuntime{model: m},
	}, flowoccupancy.Query{
		FlowID: flowID,
		Purpose: flowoccupancy.Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: stage,
		},
	})
}

func (m Model) repairOccupancy(flowID string, stage flowoccupancy.Stage) flowoccupancy.Verdict {
	if strings.TrimSpace(flowID) == "" {
		return flowoccupancy.Free()
	}
	return flowoccupancy.Evaluate(flowoccupancy.Sources{
		Lease: flowOccupancyLeaseInspector{
			root:     m.sessionStateRoot,
			injected: m.leaseInspectInjected,
			inspect:  m.inspectFlowLease,
		},
		Runtime: flowOccupancyRuntime{model: m},
	}, flowoccupancy.Query{
		FlowID: flowID,
		Purpose: flowoccupancy.Purpose{
			Role:  actions.RoleRepair,
			Stage: stage,
		},
	})
}

func (m Model) trackedPhaseDrainAdvice(record flowstore.FlowRecord, phaseID string) flowoccupancy.Advisory {
	return flowoccupancy.EvaluateAdvisory(flowoccupancy.Sources{
		FlowCache: flowOccupancyFlowCache{record: record},
		Lease: flowOccupancyLeaseInspector{
			root:     m.sessionStateRoot,
			injected: m.leaseInspectInjected,
			inspect:  m.inspectFlowLease,
		},
		Runtime: flowOccupancyRuntime{model: m},
	}, flowoccupancy.Query{
		FlowID: record.FlowID,
		Purpose: flowoccupancy.Purpose{
			Role:  actions.RoleTrackedPhase,
			Stage: flowoccupancy.StageDrain,
		},
		PhaseID: phaseID,
	})
}

func (m Model) worktreeAgentFooterAdvice(record flowstore.FlowRecord) flowoccupancy.Advisory {
	return flowoccupancy.EvaluateAdvisory(flowoccupancy.Sources{
		FlowCache: flowOccupancyFlowCache{record: record},
		Cache: flowOccupancySessionCache{
			flowSessions:     m.sessions.Items(),
			worktreeSessions: m.worktreeSessions.Items(),
		},
		Lease: flowOccupancyLeaseInspector{
			root:     m.sessionStateRoot,
			injected: m.leaseInspectInjected,
			inspect:  m.inspectFlowLease,
		},
		Runtime: flowOccupancyRuntime{model: m},
	}, flowoccupancy.Query{
		FlowID: record.FlowID,
		Purpose: flowoccupancy.Purpose{
			Role:  actions.RoleWorktreeAgent,
			Stage: flowoccupancy.StageFooter,
		},
	})
}

// flowLaunchSeams is the authoritative boundary the launch lifecycle reads and
// writes through. It is built once in NewWithOptions and stored on the Model so
// the lifecycle never reaches for a pane snapshot or a package-level store.
type flowLaunchSeams struct {
	AllocateFlowID           func(title string) (string, error)
	CreateFlow               func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, error)
	CreatePreparation        func(flowstore.FlowRecord, flowstore.CreateOptions) (flowstore.FlowRecord, flowstore.PreparationFinalizer, error)
	ReadFlow                 func(flowID string) (flowstore.FlowRecord, error)
	ReadSession              func(sessions.Provider, string) (sessions.SessionRecord, error)
	ListFlowSessions         func(flowID string) ([]sessions.SessionRecord, error)
	ReserveLaunch            func(flowID string) (flowstore.FlowRecord, func(), error)
	EnsureWorktree           func(flowstore.FlowRecord) (flowstore.FlowRecord, error)
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
	// ReconcileEpicSuccessor is epic-progression-only. The create pipeline
	// calls it under the launch/close reservation it already holds, so it must
	// never be a seam that takes that reservation for itself.
	ReconcileEpicSuccessor func(flowstore.EpicProgressionSuccessorUpdate) (flowstore.EpicProgressionSuccessorResult, error)
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
