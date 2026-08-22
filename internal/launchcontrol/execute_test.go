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
	demoted, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: created.FlowID, PhaseID: "plan", Status: flowstore.PhaseNeedsAttention, Outcome: flowstore.OutcomePhaseResultMissing, Notes: "reconciled"})
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
	resp, err = Execute(store, mustRequest(t, VerbPhaseRecover, created.FlowID, "plan", "", PhaseRecoverPayload{ExpectedStatus: string(phase.Status), ExpectedOutcome: phase.Outcome, ExpectedLaunchID: "launch-stale", ExpectedUpdatedAt: phase.UpdatedAt}))
	if err != nil || resp.OK || !resp.Refused {
		t.Fatalf("stale phase.recover = %#v, %v", resp, err)
	}
}
