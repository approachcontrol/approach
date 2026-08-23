package launchcontrol

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/planstore"
)

func TestExecuteReadAndListMatchStore(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Read Me")

	resp, err := Execute(store, mustRequest(t, VerbFlowRead, created.FlowID, "", "", ReadPayload{}))
	if err != nil {
		t.Fatal(err)
	}
	got := decodeResult[flowstore.FlowRecord](t, resp)
	want, _ := store.Read(created.FlowID)
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("read result differs from store.Read:\n%s\n%s", gotJSON, wantJSON)
	}

	resp, err = Execute(store, mustRequest(t, VerbFlowList, "", "", "", ListPayload{RepoPath: created.RepoPath}))
	if err != nil {
		t.Fatal(err)
	}
	list := decodeResult[[]flowstore.FlowRecord](t, resp)
	if len(list) != 1 || list[0].FlowID != created.FlowID {
		t.Fatalf("list = %#v", list)
	}
	resp, _ = Execute(store, mustRequest(t, VerbFlowList, "", "", "", ListPayload{RepoPath: "/nowhere"}))
	if string(resp.Result) != "[]" {
		t.Fatalf("empty list result = %s, want []", resp.Result)
	}
	resp, _ = Execute(store, mustRequest(t, VerbFlowRead, "missing-flow", "", "", ReadPayload{}))
	if resp.OK || !resp.Refused || !flowstoreNotFoundText(resp.Error) {
		t.Fatalf("missing flow response = %#v", resp)
	}
}

func flowstoreNotFoundText(text string) bool { return strings.Contains(text, "not found") }

func TestExecutePhaseSetRefusesReadyWithoutTouchingStore(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Set Ready")
	resp, err := Execute(store, mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "", PhaseSetPayload{Status: string(flowstore.PhaseReady)}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !resp.Refused || !strings.Contains(resp.Error, "readiness is derived") {
		t.Fatalf("response = %#v", resp)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseReady {
		t.Fatal("plan phase changed")
	}
}

func TestExecutePhaseSetMatchesStoreSetPhase(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Set Phase")
	resp, err := Execute(store, mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "", PhaseSetPayload{Status: string(flowstore.PhaseCompleted), Summary: "Saved plan"}))
	if err != nil {
		t.Fatal(err)
	}
	record := decodeResult[flowstore.FlowRecord](t, resp)
	phase, _ := PhaseByID(record, "plan")
	if phase.Status != flowstore.PhaseCompleted || phase.Summary != "Saved plan" {
		t.Fatalf("phase = %#v", phase)
	}
	if next, _ := PhaseByID(record, "plan-review"); next.Status != flowstore.PhaseReady {
		t.Fatalf("plan-review = %#v", next)
	}
}

func TestExecutePhaseSetCannotCreateReconciliationStamp(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "No forged reconciliation")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	req := mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-1", PhaseSetPayload{})
	req.Payload = json.RawMessage(`{"status":"needs_attention","outcome":"phase_result_missing","notes":"phase_result_missing: forged","reconciliation":{"reason":"phase_result_missing","launch_id":"launch-1"}}`)
	resp, err := Execute(store, req)
	if err != nil || !resp.OK {
		t.Fatalf("phase.set = %#v, %v", resp, err)
	}
	phase := phaseOf(t, store, created.FlowID, "plan")
	if phase.Reconciliation != nil {
		t.Fatalf("agent-facing phase.set created reconciliation stamp %#v", phase.Reconciliation)
	}
}

func TestExecutePhaseActionsDefaultOutcomesByKindAndPrintNextPhase(t *testing.T) {
	store, root := newTestStore(t)
	created, err := store.Create(flowstore.FlowRecord{Title: "Actions", Instructions: "x", RepoPath: filepath.Join(root, "repo"), Branch: "flow/actions"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Execute(store, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "", PhaseActionPayload{Summary: "Saved"}))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeResult[PhaseActionResult](t, resp)
	if result.FlowID != created.FlowID || result.UpdatedPhase.PhaseID != "plan" || result.UpdatedPhase.Status != flowstore.PhaseCompleted || result.UpdatedPhase.Outcome != "" {
		t.Fatalf("plan result = %#v", result.UpdatedPhase)
	}
	if result.NextPhase == nil || result.NextPhase.PhaseID != "plan-review" || result.NextPhase.Status != string(flowstore.PhaseReady) ||
		!reflect.DeepEqual(result.NextPhase.AllowedStatuses, flowstore.AllowedNextPhaseStatuses(string(flowstore.PhaseReady))) {
		t.Fatalf("next phase = %#v", result.NextPhase)
	}
	// plan-review kind fills approved on complete.
	resp, _ = Execute(store, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan-review", "", PhaseActionPayload{}))
	result = decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Outcome != flowstore.OutcomeApproved {
		t.Fatalf("plan-review outcome = %q, want approved", result.UpdatedPhase.Outcome)
	}
	// block on plan-review defaults to blocked; needs-attention to changes_requested.
	other := createFlow(t, store, "Actions Two")
	completePhase(t, store, other.FlowID, "plan", "")
	resp, _ = Execute(store, mustRequest(t, VerbPhaseNeedsAttention, other.FlowID, "plan-review", "", PhaseActionPayload{Notes: "revise"}))
	result = decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Status != flowstore.PhaseNeedsAttention || result.UpdatedPhase.Outcome != flowstore.OutcomeChangesRequested {
		t.Fatalf("needs-attention plan-review = %#v", result.UpdatedPhase)
	}
	// autoreview kind fills passed on complete.
	for _, phaseID := range []string{"implementation", "review-loop", "pr-creation"} {
		completePhase(t, store, created.FlowID, phaseID, "")
	}
	if _, err := store.SetPR(flowstore.PRUpdate{FlowID: created.FlowID, Provider: "github", Number: 1, URL: "https://github.com/o/r/pull/1", HeadBranch: "flow/actions", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if got := phaseOf(t, store, created.FlowID, "autoreview").Status; got != flowstore.PhaseReady {
		t.Fatalf("autoreview status = %q, want ready", got)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPhaseComplete, created.FlowID, "autoreview", "", PhaseActionPayload{}))
	result = decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Outcome != "passed" {
		t.Fatalf("autoreview complete outcome = %q, want passed", result.UpdatedPhase.Outcome)
	}
}

func TestExecuteRestartRequiresRecoverableStateAndFillsNote(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Restart")
	resp, _ := Execute(store, mustRequest(t, VerbPhaseRestart, created.FlowID, "plan", "", PhaseRestartPayload{}))
	if resp.OK || !resp.Refused {
		t.Fatalf("restart of a ready phase = %#v", resp)
	}
	if _, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseBlocked, Notes: "wait"}); err != nil {
		t.Fatal(err)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPhaseRestart, created.FlowID, "plan", "", PhaseRestartPayload{}))
	result := decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Status != flowstore.PhaseRunning || result.UpdatedPhase.Notes != "Rerunning Plan after addressing prior findings." {
		t.Fatalf("restart result = %#v", result.UpdatedPhase)
	}
}

func TestExecuteFlowLevelVerbsMatchStore(t *testing.T) {
	store, root := newTestStore(t)
	created, err := store.Create(flowstore.FlowRecord{Title: "Flow Level", Instructions: "x", RepoPath: filepath.Join(root, "repo"), Branch: "flow/x"})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Save(planstore.PlanRecord{PlanID: "plan-1", Title: "Linked", Status: "approved", Markdown: "# Plan\n"}); err != nil {
		t.Fatal(err)
	}
	resp, _ := Execute(store, mustRequest(t, VerbPlanSet, created.FlowID, "", "", PlanSetPayload{PlanID: "plan-1"}))
	record := decodeResult[flowstore.FlowRecord](t, resp)
	if record.PlanID != "plan-1" || record.PlanPath == "" {
		t.Fatalf("plan set = %#v", record)
	}
	resp, _ = Execute(store, mustRequest(t, VerbIssueSet, created.FlowID, "", "", IssueSetPayload{Provider: "github", Number: 12, URL: "https://github.com/o/r/issues/12"}))
	record = decodeResult[flowstore.FlowRecord](t, resp)
	if record.Issue.Number != 12 {
		t.Fatalf("issue set = %#v", record.Issue)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPRSet, created.FlowID, "", "", PRSetPayload{Provider: "github", Number: 7, URL: "https://github.com/o/r/pull/7", Head: "flow/x", Base: "main"}))
	record = decodeResult[flowstore.FlowRecord](t, resp)
	if record.PR.Number != 7 || record.PR.HeadBranch != "flow/x" {
		t.Fatalf("pr set = %#v", record.PR)
	}
	resp, _ = Execute(store, mustRequest(t, VerbMergeSet, created.FlowID, "", "", MergeSetPayload{Status: "merged", Commit: "abc123", MergedAt: "2026-06-09T12:00:00Z"}))
	if resp.OK || !strings.Contains(resp.Error, "requires completed merge phase") {
		t.Fatalf("merge before merge phase = %#v", resp)
	}
	resp, _ = Execute(store, mustRequest(t, VerbMergeSet, created.FlowID, "", "", MergeSetPayload{Status: "merged", Commit: "abc123"}))
	if resp.OK || !strings.Contains(resp.Error, "requires --merged-at") {
		t.Fatalf("merge without merged-at = %#v", resp)
	}
}

func TestExecuteAddChildAndAgentSet(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Children")
	resp, _ := Execute(store, mustRequest(t, VerbPhaseAddChild, created.FlowID, "", "", AddChildPayload{ParentPhaseID: "implementation", PhaseID: "api", Title: "API work", Order: 1}))
	record := decodeResult[flowstore.FlowRecord](t, resp)
	if child, ok := PhaseByID(record, "api"); !ok || child.ParentPhaseID != "implementation" {
		t.Fatalf("child = %#v ok=%v", child, ok)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPhaseAddChild, created.FlowID, "", "", AddChildPayload{ParentPhaseID: "implementation", PhaseID: "api", Title: "API work", Order: 0}))
	if resp.OK || !strings.Contains(resp.Error, "positive --order") {
		t.Fatalf("order 0 = %#v", resp)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPhaseAgentSet, created.FlowID, "implementation", "", AgentSetPayload{Agent: "claude", Model: "claude-opus-5"}))
	record = decodeResult[flowstore.FlowRecord](t, resp)
	if phase, _ := PhaseByID(record, "implementation"); phase.Agent != "claude" || phase.Model != "claude-opus-5" {
		t.Fatalf("agent set = %#v", phase)
	}
	resp, _ = Execute(store, mustRequest(t, VerbPhaseAgentSet, created.FlowID, "implementation", "", AgentSetPayload{Clear: true}))
	record = decodeResult[flowstore.FlowRecord](t, resp)
	if phase, _ := PhaseByID(record, "implementation"); phase.Agent != "" {
		t.Fatalf("agent clear = %#v", phase)
	}
}

func TestExecuteRefusesDirectVerbsAndUnknownVerbs(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := Execute(store, Request{Verb: VerbFlowCreate, FlowID: "x"}); !IsNotExecutable(err) {
		t.Fatalf("flow.create error = %v, want not executable", err)
	}
	resp, err := Execute(store, Request{Verb: "phase.explode", FlowID: "x"})
	if err != nil || resp.OK || !resp.Refused {
		t.Fatalf("unknown verb = %#v, %v", resp, err)
	}
	created := createFlow(t, store, "Reset")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	resp, err = Execute(store, mustRequest(t, VerbPhaseReset, created.FlowID, "plan", "", PhaseResetPayload{}))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("phase.reset = %#v", resp)
	}
}

func TestExecutePhaseRecoverUsesObservedIdentity(t *testing.T) {
	store, _ := newTestStore(t)
	created := createFlow(t, store, "Recover")
	launchPhase(t, store, created.FlowID, "plan", "launch-stale")
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{FlowID: created.FlowID, PhaseID: "plan", Session: flowstore.Session{Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended"}}); err != nil {
		t.Fatal(err)
	}
	demoted, err := store.DemoteReconciledPhase(flowstore.ReconciliationDemotionUpdate{PhaseUpdate: flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled"}, Reason: flowstore.OutcomePhaseResultMissing, LaunchID: "launch-stale"})
	if err != nil {
		t.Fatal(err)
	}
	phase, _ := PhaseByID(demoted, "plan")
	resp, err := Execute(store, mustRequest(t, VerbPhaseRecover, created.FlowID, "plan", "", PhaseRecoverPayload{ExpectedStatus: string(phase.Status), ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}))
	if err != nil || !resp.OK {
		t.Fatalf("phase.recover = %#v, %v", resp, err)
	}
	result := decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Status != flowstore.PhaseReady || len(result.UpdatedPhase.LaunchIDs) != 0 {
		t.Fatalf("recovered phase = %#v", result.UpdatedPhase)
	}
	resp, err = Execute(store, mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-stale", PhaseActionPayload{Summary: "late stale result"}))
	if err != nil || resp.OK || !resp.Refused || !strings.Contains(resp.Error, "was recovered") {
		t.Fatalf("late recovered-launch completion = %#v, %v", resp, err)
	}
	if phase := phaseOf(t, store, created.FlowID, "plan"); phase.Status != flowstore.PhaseReady {
		t.Fatalf("late completion changed recovered phase = %#v", phase)
	}
	resp, err = Execute(store, mustRequest(t, VerbPhaseRecover, created.FlowID, "plan", "", PhaseRecoverPayload{ExpectedStatus: string(phase.Status), ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}))
	if err != nil || resp.OK || !resp.Refused {
		t.Fatalf("stale phase.recover = %#v, %v", resp, err)
	}
}

// Calling executeVerb bypasses Execute's validation and proves the store rejects
// each revoked capability inside the transaction that performs the mutation.
func TestExecuteVerbAtomicallyRefusesWritesFromRecoveredLaunch(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Recovered capabilities")
	launchPhase(t, store, created.FlowID, "plan", "launch-stale")
	if _, err := store.AttachSession(flowstore.SessionAttachUpdate{
		FlowID:  created.FlowID,
		PhaseID: "plan",
		Session: flowstore.Session{
			Provider: "codex", SessionID: "session-stale", LaunchID: "launch-stale", Status: "ended",
		},
	}); err != nil {
		t.Fatal(err)
	}
	demoted, err := store.DemoteReconciledPhase(flowstore.ReconciliationDemotionUpdate{
		PhaseUpdate: flowstore.PhaseUpdate{
			FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention,
			Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled",
		},
		Reason: flowstore.OutcomePhaseResultMissing, LaunchID: "launch-stale",
	})
	if err != nil {
		t.Fatal(err)
	}
	phase, _ := PhaseByID(demoted, "plan")
	if _, err := store.RecoverReconciledPhase(flowstore.PhaseRecoveryUpdate{
		FlowID: created.FlowID, PhaseID: "plan", ExpectedStatus: phase.Status,
		ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}

	// Flow-level requests can present a phase other than the launch's registered
	// phase. Revocation follows the launch ID, not this client-controlled value.
	req := mustRequest(t, VerbIssueSet, created.FlowID, "implementation", "launch-stale", IssueSetPayload{
		Provider: "github", Number: 99, URL: "https://github.com/o/r/issues/99",
	})
	if _, _, err := executeVerb(store, req); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch issue write error = %v, want recovered-launch refusal", err)
	}
	record, err := store.Read(created.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Issue.Number != 0 {
		t.Fatalf("recovered launch changed issue metadata = %#v", record.Issue)
	}

	agentReq := mustRequest(t, VerbPhaseAgentSet, created.FlowID, "plan", "launch-stale", AgentSetPayload{
		Agent: "codex",
	})
	if _, _, err := executeVerb(store, agentReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch agent-settings write error = %v, want recovered-launch refusal", err)
	}
	if phase := phaseOf(t, store, created.FlowID, "plan"); phase.Agent != "" || phase.Model != "" {
		t.Fatalf("recovered launch changed phase agent settings = %#v", phase.AgentSettings())
	}

	plans, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Save(planstore.PlanRecord{PlanID: "recovered-plan", Title: "Recovered", Markdown: "# Plan\n"}); err != nil {
		t.Fatal(err)
	}
	planReq := mustRequest(t, VerbPlanSet, created.FlowID, "plan", "launch-stale", PlanSetPayload{PlanID: "recovered-plan"})
	if _, _, err := executeVerb(store, planReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch plan-link write error = %v, want recovered-launch refusal", err)
	}
	record, err = store.Read(created.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	if record.PlanID != "" {
		t.Fatalf("recovered launch changed plan link = %q", record.PlanID)
	}

	prReq := mustRequest(t, VerbPRSet, created.FlowID, "plan", "launch-stale", PRSetPayload{
		Provider: "github", Number: 400, URL: "https://github.com/o/r/pull/400", Head: "flow/recovered", Base: "main",
	})
	if _, _, err := executeVerb(store, prReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch PR write error = %v, want recovered-launch refusal", err)
	}
	mergeReq := mustRequest(t, VerbMergeSet, created.FlowID, "plan", "launch-stale", MergeSetPayload{Status: flowstore.MergeBlocked})
	if _, _, err := executeVerb(store, mergeReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch merge write error = %v, want recovered-launch refusal", err)
	}
	restartReq := mustRequest(t, VerbPhaseRestart, created.FlowID, "plan", "launch-stale", PhaseRestartPayload{Notes: "retry"})
	if _, _, err := executeVerb(store, restartReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch restart error = %v, want recovered-launch refusal", err)
	}
	resetReq := mustRequest(t, VerbPhaseReset, created.FlowID, "plan", "launch-stale", PhaseResetPayload{})
	if _, _, err := executeVerb(store, resetReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch reset error = %v, want recovered-launch refusal", err)
	}
	recoverReq := mustRequest(t, VerbPhaseRecover, created.FlowID, "plan", "launch-stale", PhaseRecoverPayload{
		ExpectedStatus: string(phase.Status), ExpectedOutcome: phase.Outcome,
		ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt,
	})
	if _, _, err := executeVerb(store, recoverReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch recover error = %v, want recovered-launch refusal", err)
	}
	childReq := mustRequest(t, VerbPhaseAddChild, created.FlowID, "plan", "launch-stale", AddChildPayload{
		ParentPhaseID: "implementation", PhaseID: "late-child", Title: "Late child", Order: 1,
	})
	if _, _, err := executeVerb(store, childReq); err == nil || !strings.Contains(err.Error(), "was recovered") {
		t.Fatalf("recovered launch child-phase write error = %v, want recovered-launch refusal", err)
	}
	if record, err := store.Read(created.FlowID); err != nil {
		t.Fatal(err)
	} else if _, ok := PhaseByID(record, "late-child"); ok {
		t.Fatal("recovered launch added a child phase")
	}
	phaseWrites := []Request{
		mustRequest(t, VerbPhaseSet, created.FlowID, "plan", "launch-stale", PhaseSetPayload{Status: string(flowstore.PhaseCompleted)}),
		mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-stale", PhaseActionPayload{}),
		mustRequest(t, VerbPhaseBlock, created.FlowID, "plan", "launch-stale", PhaseActionPayload{Notes: "blocked"}),
		mustRequest(t, VerbPhaseNeedsAttention, created.FlowID, "plan", "launch-stale", PhaseActionPayload{Notes: "attention"}),
	}
	for _, req := range phaseWrites {
		if _, _, err := executeVerb(store, req); err == nil || !strings.Contains(err.Error(), "was recovered") {
			t.Fatalf("recovered launch %s error = %v, want recovered-launch refusal", req.Verb, err)
		}
	}
}
