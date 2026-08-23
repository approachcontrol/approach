package launchcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

// PhaseActionResult is what `flow phase complete|block|needs-attention|
// restart|reset|recover` print: the updated phase, the next actionable phase, and the
// whole record. It lives here so the proxied and direct paths print one shape.
type PhaseActionResult struct {
	FlowID       string               `json:"flow_id"`
	FlowStatus   string               `json:"flow_status"`
	UpdatedPhase flowstore.FlowPhase  `json:"updated_phase"`
	NextPhase    *PhaseActionState    `json:"next_phase,omitempty"`
	Flow         flowstore.FlowRecord `json:"flow"`
}

// PhaseActionState is the next-phase hint carried by PhaseActionResult.
type PhaseActionState struct {
	PhaseID         string   `json:"phase_id"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	AllowedStatuses []string `json:"allowed_statuses,omitempty"`
}

// errNotExecutable marks a verb Execute does not implement: the two
// ClassDirect verbs, which the CLI runs itself, and unknown verbs.
var errNotExecutable = errors.New("verb is not executable through the launch controller")

// IsNotExecutable reports whether err means Execute has no implementation
// for the request's verb.
func IsNotExecutable(err error) bool {
	return errors.Is(err, errNotExecutable)
}

// phaseAction is the fixed part of one `flow phase <action>` leaf.
type phaseAction struct {
	command        string
	status         string
	defaultOutcome string
}

var phaseActions = map[Verb]phaseAction{
	VerbPhaseComplete:       {command: "complete", status: string(flowstore.PhaseCompleted), defaultOutcome: flowstore.OutcomeApproved},
	VerbPhaseBlock:          {command: "block", status: string(flowstore.PhaseBlocked), defaultOutcome: flowstore.OutcomeBlocked},
	VerbPhaseNeedsAttention: {command: "needs-attention", status: string(flowstore.PhaseNeedsAttention), defaultOutcome: flowstore.OutcomeChangesRequested},
}

// Validate performs the request checks that need no store: identity fields,
// payload shape, and the agent-facing rules the CLI enforced before it ever
// opened a database. Execute calls it first, so a request that fails here is
// refused identically on the proxied and the direct path. Errors are worded
// exactly as the CLI's own, because that is what agents and tests read.
func Validate(req Request) error {
	if req.SchemaVersion != 0 && req.SchemaVersion != ProtocolSchemaVersion {
		return fmt.Errorf("unsupported launch control schema version %d", req.SchemaVersion)
	}
	if _, ok := verbTable[req.Verb]; !ok {
		return fmt.Errorf("unknown launch control verb %q", req.Verb)
	}
	if req.Verb != VerbFlowList && strings.TrimSpace(req.FlowID) == "" {
		return fmt.Errorf("%s requires --flow-id", verbLabel(req.Verb))
	}
	switch req.Verb {
	case VerbFlowRead, VerbFlowList, VerbFlowCreate:
		return nil
	case VerbPhaseSet:
		var payload PhaseSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase set requires --phase-id")
		}
		if payload.Status == "" {
			return fmt.Errorf("flow phase set requires --status")
		}
		if payload.Status == string(flowstore.PhaseReady) {
			return fmt.Errorf("cannot set phase status to ready; readiness is derived")
		}
		if !slices.Contains(flowstore.AgentSettablePhaseStatuses(), payload.Status) {
			return fmt.Errorf("unsupported agent-facing phase status %q; valid statuses: %s",
				payload.Status, strings.Join(flowstore.AgentSettablePhaseStatuses(), ", "))
		}
		return nil
	case VerbPhaseComplete, VerbPhaseBlock, VerbPhaseNeedsAttention:
		var payload PhaseActionPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase %s requires --phase-id", phaseActions[req.Verb].command)
		}
		return nil
	case VerbPhaseRestart:
		var payload PhaseRestartPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase restart requires --phase-id")
		}
		return nil
	case VerbPhaseReset:
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase reset requires --phase-id")
		}
		return nil
	case VerbPhaseRecover:
		var payload PhaseRecoverPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase recover requires --phase-id")
		}
		if payload.ExpectedStatus == "" || payload.ExpectedOutcome == "" || payload.ExpectedLaunchID == "" || payload.ExpectedUpdatedAt.IsZero() {
			return errors.New("flow phase recover requires a complete observed phase snapshot")
		}
		return nil
	case VerbPhaseAddChild:
		var payload AddChildPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if payload.PhaseID == "" {
			return fmt.Errorf("flow phase add-child requires --phase-id")
		}
		if strings.TrimSpace(payload.Title) == "" {
			return fmt.Errorf("flow phase add-child requires --title")
		}
		if payload.Order < 1 {
			return fmt.Errorf("flow phase add-child requires positive --order")
		}
		return nil
	case VerbPhaseAgentSet:
		var payload AgentSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(req.PhaseID) == "" {
			return fmt.Errorf("flow phase agent set requires --phase-id")
		}
		if payload.Clear {
			if payload.Agent != "" || payload.Model != "" || payload.ReasoningEffort != "" {
				return fmt.Errorf("flow phase agent set --clear cannot be combined with --agent, --model, or --reasoning-effort")
			}
		} else if strings.TrimSpace(payload.Agent) == "" {
			return fmt.Errorf("flow phase agent set requires --agent or --clear")
		}
		return nil
	case VerbPlanSet:
		var payload PlanSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if payload.PlanID == "" {
			return fmt.Errorf("flow plan set requires --plan-id")
		}
		return nil
	case VerbIssueSet:
		var payload IssueSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if payload.Number <= 0 {
			return fmt.Errorf("flow issue set requires positive --number")
		}
		if payload.URL == "" {
			return fmt.Errorf("flow issue set requires --url")
		}
		return nil
	case VerbPRSet:
		var payload PRSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if payload.Number <= 0 {
			return fmt.Errorf("flow pr set requires positive --number")
		}
		if payload.URL == "" {
			return fmt.Errorf("flow pr set requires --url")
		}
		if payload.Head == "" {
			return fmt.Errorf("flow pr set requires --head")
		}
		if payload.Base == "" {
			return fmt.Errorf("flow pr set requires --base")
		}
		return nil
	case VerbMergeSet:
		var payload MergeSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return err
		}
		if payload.Status == "" {
			return fmt.Errorf("flow merge set requires --status")
		}
		if payload.Status == flowstore.MergeMerged {
			if strings.TrimSpace(payload.Commit) == "" {
				return fmt.Errorf("flow merge set --status merged requires --commit")
			}
			if strings.TrimSpace(payload.MergedAt) == "" {
				return fmt.Errorf("flow merge set --status merged requires --merged-at")
			}
			if _, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.MergedAt)); err != nil {
				return fmt.Errorf("invalid --merged-at: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("unknown launch control verb %q", req.Verb)
}

func verbLabel(verb Verb) string {
	switch verb {
	case VerbFlowRead:
		return "flow read"
	case VerbFlowList:
		return "flow list"
	case VerbFlowCreate:
		return "flow create"
	case VerbPhaseSet:
		return "flow phase set"
	case VerbPhaseComplete, VerbPhaseBlock, VerbPhaseNeedsAttention:
		return "flow phase " + phaseActions[verb].command
	case VerbPhaseRestart:
		return "flow phase restart"
	case VerbPhaseReset:
		return "flow phase reset"
	case VerbPhaseRecover:
		return "flow phase recover"
	case VerbPhaseAddChild:
		return "flow phase add-child"
	case VerbPhaseAgentSet:
		return "flow phase agent set"
	case VerbPlanSet:
		return "flow plan set"
	case VerbIssueSet:
		return "flow issue set"
	case VerbPRSet:
		return "flow pr set"
	case VerbMergeSet:
		return "flow merge set"
	}
	return string(verb)
}

// Execute runs one request against store. It is the single implementation of
// every proxied verb: the controller calls it for socket requests, the CLI
// calls it on the direct path, and replay calls it for spooled requests, so
// the three cannot drift.
//
// A validation or store refusal comes back as Response{Refused: true}; that
// is a final answer for the caller and the request will not succeed on
// retry. A returned error is a programming or transport-class failure — an
// unknown verb, an unencodable result — and the request is not answered.
func Execute(store *flowstore.Store, req Request) (Response, error) {
	if err := Validate(req); err != nil {
		return refuse(err), nil
	}
	if class := verbTable[req.Verb]; class == ClassDirect && req.Verb == VerbFlowCreate {
		return Response{}, fmt.Errorf("%s: %w", req.Verb, errNotExecutable)
	}
	if store == nil {
		return Response{}, errors.New("launch control executor requires a store")
	}
	result, warning, err := executeVerb(store, req)
	if err != nil {
		return refuse(err), nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return Response{}, fmt.Errorf("encode %s result: %w", req.Verb, err)
	}
	return Response{SchemaVersion: ProtocolSchemaVersion, OK: true, Result: data, Warning: warning}, nil
}

func refuse(err error) Response {
	return Response{SchemaVersion: ProtocolSchemaVersion, Refused: true, Error: err.Error()}
}

func executeVerb(store *flowstore.Store, req Request) (any, string, error) {
	switch req.Verb {
	case VerbFlowRead:
		record, err := store.Read(req.FlowID)
		return record, "", err
	case VerbFlowList:
		var payload ListPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		records, err := store.List(flowstore.FlowFilter{RepoPath: payload.RepoPath})
		_, partial := flowstore.AsPartialList(err)
		if err != nil && !partial {
			return nil, "", err
		}
		if records == nil {
			records = []flowstore.FlowRecord{}
		}
		warning := ""
		if partial {
			warning = err.Error()
		}
		return records, warning, nil
	case VerbPhaseSet:
		var payload PhaseSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.SetPhase(flowstore.PhaseUpdate{
			FlowID:  req.FlowID,
			PhaseID: req.PhaseID,
			Status:  flowstore.PhaseStatus(payload.Status),
			Outcome: payload.Outcome,
			Notes:   payload.Notes,
			Summary: payload.Summary,
			Fence:   flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbPhaseComplete, VerbPhaseBlock, VerbPhaseNeedsAttention:
		var payload PhaseActionPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		action := phaseActions[req.Verb]
		outcome := strings.TrimSpace(payload.Outcome)
		if outcome == "" {
			record, err := store.Read(req.FlowID)
			if err != nil {
				return nil, "", err
			}
			phase, ok := PhaseByID(record, req.PhaseID)
			if !ok {
				return nil, "", fmt.Errorf("phase %q not found in flow %q", req.PhaseID, req.FlowID)
			}
			outcome = defaultPhaseActionOutcome(string(flowstore.SemanticKind(phase)), action)
		}
		record, err := store.SetPhase(flowstore.PhaseUpdate{
			FlowID:  req.FlowID,
			PhaseID: req.PhaseID,
			Status:  flowstore.PhaseStatus(action.status),
			Outcome: outcome,
			Notes:   payload.Notes,
			Summary: payload.Summary,
			Fence:   flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		if err != nil {
			return nil, "", err
		}
		return phaseActionResult(record, req.PhaseID)
	case VerbPhaseRestart:
		var payload PhaseRestartPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		note := strings.TrimSpace(payload.Notes)
		if note == "" {
			note = fmt.Sprintf("Rerunning %s after addressing prior findings.", DefaultPhaseTitle(req.PhaseID))
		}
		record, err := store.RestartPhase(flowstore.PhaseRestartUpdate{
			FlowID: req.FlowID, PhaseID: req.PhaseID, Notes: note,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		if err != nil {
			return nil, "", err
		}
		return phaseActionResult(record, req.PhaseID)
	case VerbPhaseReset:
		record, err := store.ResetRecoverableRunningPhase(flowstore.PhaseResetUpdate{
			FlowID: req.FlowID, PhaseID: req.PhaseID,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		if err != nil {
			return nil, "", err
		}
		return phaseActionResult(record, req.PhaseID)
	case VerbPhaseRecover:
		var payload PhaseRecoverPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{
			FlowID: req.FlowID, PhaseID: req.PhaseID,
			ExpectedStatus: flowstore.PhaseStatus(payload.ExpectedStatus), ExpectedOutcome: payload.ExpectedOutcome,
			ExpectedLaunchID: payload.ExpectedLaunchID, ExpectedUpdatedAt: payload.ExpectedUpdatedAt,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		if err != nil {
			return nil, "", err
		}
		return phaseActionResult(record, req.PhaseID)
	case VerbPhaseAddChild:
		var payload AddChildPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.AddChildPhase(flowstore.ChildPhaseUpdate{
			FlowID:        req.FlowID,
			ParentPhaseID: payload.ParentPhaseID,
			PhaseID:       payload.PhaseID,
			Title:         payload.Title,
			Order:         payload.Order,
			Fence:         flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbPhaseAgentSet:
		var payload AgentSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		settings := flowstore.PhaseAgentSettings{}
		if !payload.Clear {
			settings = flowstore.PhaseAgentSettings{Agent: payload.Agent, Model: payload.Model, ReasoningEffort: payload.ReasoningEffort}
		}
		record, err := store.SetPhaseAgentSettings(flowstore.PhaseAgentSettingsUpdate{
			FlowID: req.FlowID, PhaseID: req.PhaseID, Settings: settings,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbPlanSet:
		var payload PlanSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.SetPlanLink(flowstore.PlanLinkUpdate{
			FlowID: req.FlowID, PlanID: payload.PlanID, PlanPath: payload.PlanPath,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbIssueSet:
		var payload IssueSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.SetIssue(flowstore.IssueUpdate{
			FlowID: req.FlowID, Provider: payload.Provider, Number: payload.Number, URL: payload.URL,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbPRSet:
		var payload PRSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		record, err := store.SetPR(flowstore.PRUpdate{
			FlowID:     req.FlowID,
			Provider:   payload.Provider,
			Number:     payload.Number,
			URL:        payload.URL,
			HeadBranch: payload.Head,
			BaseBranch: payload.Base,
			Status:     payload.Status,
			Fence:      flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	case VerbMergeSet:
		var payload MergeSetPayload
		if err := decodePayload(req, &payload); err != nil {
			return nil, "", err
		}
		var mergedAt time.Time
		if payload.Status == flowstore.MergeMerged {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.MergedAt))
			if err != nil {
				return nil, "", fmt.Errorf("invalid --merged-at: %w", err)
			}
			mergedAt = parsed
		}
		record, err := store.SetMerge(flowstore.MergeUpdate{
			FlowID: req.FlowID, Status: payload.Status, Commit: payload.Commit, MergedAt: mergedAt,
			Fence: flowstore.PhaseLaunchFence{LaunchID: req.LaunchID},
		})
		return record, "", err
	}
	return nil, "", fmt.Errorf("%s: %w", req.Verb, errNotExecutable)
}

func phaseActionResult(record flowstore.FlowRecord, phaseID string) (any, string, error) {
	updated, ok := PhaseByID(record, phaseID)
	if !ok {
		return nil, "", fmt.Errorf("phase %q not found in updated flow %q", phaseID, record.FlowID)
	}
	return PhaseActionResult{
		FlowID:       record.FlowID,
		FlowStatus:   record.Status,
		UpdatedPhase: updated,
		NextPhase:    NextPhaseActionState(record, updated),
		Flow:         record,
	}, "", nil
}

func defaultPhaseActionOutcome(kind string, action phaseAction) string {
	switch kind {
	case string(flowstore.KindPlanReview):
		return action.defaultOutcome
	case string(flowstore.KindAutoreview):
		switch action.status {
		case string(flowstore.PhaseCompleted):
			return "passed"
		case string(flowstore.PhaseNeedsAttention):
			return "needs_attention"
		case string(flowstore.PhaseBlocked):
			return flowstore.OutcomeBlocked
		}
	}
	return ""
}

// NextPhaseActionState is the "what now" hint an action prints: the same
// phase while it still expects work, otherwise the next actionable phase.
func NextPhaseActionState(record flowstore.FlowRecord, updated flowstore.FlowPhase) *PhaseActionState {
	if flowstore.PhaseIsActionable(updated) && updated.Status != flowstore.PhaseCompleted && updated.Status != flowstore.PhaseSkipped {
		return newPhaseActionState(updated)
	}
	if phase, ok := flowstore.NextActionablePhase(record); ok {
		return newPhaseActionState(phase)
	}
	return nil
}

func newPhaseActionState(phase flowstore.FlowPhase) *PhaseActionState {
	return &PhaseActionState{
		PhaseID:         phase.PhaseID,
		Title:           phase.Title,
		Status:          string(phase.Status),
		AllowedStatuses: flowstore.AllowedNextPhaseStatuses(string(phase.Status)),
	}
}

// PhaseByID finds a phase by normalized ID.
func PhaseByID(record flowstore.FlowRecord, phaseID string) (flowstore.FlowPhase, bool) {
	normalized := artifacts.NormalizePhaseID(phaseID)
	for _, phase := range record.Phases {
		if artifacts.NormalizePhaseID(phase.PhaseID) == normalized {
			return phase, true
		}
	}
	return flowstore.FlowPhase{}, false
}

// DefaultPhaseTitle renders a phase ID as a title for generated notes.
func DefaultPhaseTitle(phaseID string) string {
	normalized := artifacts.NormalizePhaseID(phaseID)
	if normalized == "" {
		return "phase"
	}
	parts := strings.Fields(strings.ReplaceAll(normalized, "-", " "))
	for i, part := range parts {
		if part == "pr" {
			parts[i] = "PR"
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
