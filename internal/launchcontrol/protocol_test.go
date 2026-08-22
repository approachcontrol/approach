package launchcontrol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVerbTableClassifiesEveryVerbOnce(t *testing.T) {
	want := map[Verb]VerbClass{
		VerbFlowRead: ClassProxiedNonReplayable, VerbFlowList: ClassProxiedNonReplayable,
		VerbPhaseSet: ClassProxiedReplayable, VerbPhaseComplete: ClassProxiedReplayable,
		VerbPhaseBlock: ClassProxiedReplayable, VerbPhaseNeedsAttention: ClassProxiedReplayable,
		VerbPlanSet: ClassProxiedReplayable, VerbIssueSet: ClassProxiedReplayable,
		VerbPRSet: ClassProxiedReplayable, VerbMergeSet: ClassProxiedReplayable,
		VerbPhaseRestart: ClassProxiedNonReplayable, VerbPhaseAddChild: ClassProxiedNonReplayable,
		VerbPhaseAgentSet: ClassProxiedNonReplayable,
		VerbPhaseRecover:  ClassProxiedNonReplayable,
		VerbFlowCreate:    ClassDirect, VerbPhaseReset: ClassDirect,
	}
	if len(AllVerbs()) != len(want) {
		t.Fatalf("AllVerbs() = %v, want %d verbs", AllVerbs(), len(want))
	}
	for verb, class := range want {
		got, ok := Classify(verb)
		if !ok || got != class {
			t.Errorf("Classify(%s) = %v,%v want %v", verb, got, ok, class)
		}
		if Replayable(verb) != (class == ClassProxiedReplayable) {
			t.Errorf("Replayable(%s) = %v", verb, Replayable(verb))
		}
	}
	if _, ok := Classify("phase.explode"); ok {
		t.Fatal("unknown verb classified")
	}
	if !IsRead(VerbFlowRead) || !IsRead(VerbFlowList) || IsRead(VerbPhaseSet) {
		t.Fatal("IsRead is wrong")
	}
}

func TestPayloadsRoundTripThroughJSON(t *testing.T) {
	payloads := []any{
		PhaseSetPayload{Status: "completed", Outcome: "approved", Summary: "s", Notes: "n"},
		PhaseActionPayload{Outcome: "approved", Summary: "s", Notes: "n"},
		PhaseRestartPayload{Notes: "again"},
		PhaseRecoverPayload{ExpectedStatus: "needs_attention", ExpectedOutcome: "phase_result_missing", ExpectedLaunchID: "l", ExpectedUpdatedAt: time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)},
		AddChildPayload{ParentPhaseID: "implementation", PhaseID: "api", Title: "API", Order: 2},
		AgentSetPayload{Agent: "claude", Model: "m", ReasoningEffort: "high"},
		AgentSetPayload{Clear: true},
		PlanSetPayload{PlanID: "p", PlanPath: "/p.md"},
		IssueSetPayload{Provider: "github", Number: 3, URL: "https://github.com/o/r/issues/3"},
		PRSetPayload{Provider: "github", Number: 4, URL: "https://github.com/o/r/pull/4", Head: "h", Base: "main", Status: "open"},
		MergeSetPayload{Status: "merged", Commit: "abc", MergedAt: "2026-06-09T12:00:00Z"},
		ListPayload{RepoPath: "/repo"},
	}
	for _, payload := range payloads {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		back := reflect.New(reflect.TypeOf(payload)).Interface()
		if err := json.Unmarshal(data, back); err != nil {
			t.Fatal(err)
		}
		if got := reflect.ValueOf(back).Elem().Interface(); !reflect.DeepEqual(got, payload) {
			t.Errorf("%T round trip = %#v, want %#v", payload, got, payload)
		}
	}
}

func TestRequestRoundTripKeepsPayloadAndDropsNothing(t *testing.T) {
	req, err := NewRequest(VerbPhaseComplete, PhaseActionPayload{Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	req.FlowID, req.PhaseID, req.LaunchID, req.Token = "f", "plan", "l", "tok"
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back Request
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Verb != VerbPhaseComplete || back.FlowID != "f" || back.PhaseID != "plan" || back.LaunchID != "l" || back.Token != "tok" || back.SchemaVersion != ProtocolSchemaVersion {
		t.Fatalf("round trip = %#v", back)
	}
	if len(back.RequestID) != 32 {
		t.Fatalf("request id = %q, want 32 hex chars", back.RequestID)
	}
	var payload PhaseActionPayload
	if err := json.Unmarshal(back.Payload, &payload); err != nil || payload.Summary != "done" {
		t.Fatalf("payload = %#v (%v)", payload, err)
	}
}

func TestValidateRejectsUnknownVerbAndMissingIdentity(t *testing.T) {
	if err := Validate(Request{Verb: "phase.explode", FlowID: "f"}); err == nil || !strings.Contains(err.Error(), "unknown launch control verb") {
		t.Fatalf("unknown verb error = %v", err)
	}
	if err := Validate(Request{Verb: VerbPhaseSet, PhaseID: "plan"}); err == nil || !strings.Contains(err.Error(), "requires --flow-id") {
		t.Fatalf("missing flow error = %v", err)
	}
	if err := Validate(Request{Verb: VerbFlowList}); err != nil {
		t.Fatalf("list without flow id = %v", err)
	}
	if err := Validate(Request{Verb: VerbPhaseSet, FlowID: "f", PhaseID: "plan", Payload: []byte(`{"status":"ready"}`)}); err == nil || !strings.Contains(err.Error(), "readiness is derived") {
		t.Fatalf("ready error = %v", err)
	}
	if err := Validate(Request{Verb: VerbPhaseSet, FlowID: "f", PhaseID: "plan", Payload: []byte(`{"status":"paused"}`)}); err == nil || !strings.Contains(err.Error(), "unsupported agent-facing phase status") {
		t.Fatalf("unsupported status error = %v", err)
	}
	if err := Validate(Request{Verb: VerbMergeSet, FlowID: "f", Payload: []byte(`{"status":"merged","commit":"abc","merged_at":"yesterday"}`)}); err == nil || !strings.Contains(err.Error(), "invalid --merged-at") {
		t.Fatalf("merged-at error = %v", err)
	}
	if err := Validate(Request{Verb: VerbPhaseAgentSet, FlowID: "f", PhaseID: "plan", Payload: []byte(`{"clear":true,"agent":"claude"}`)}); err == nil || !strings.Contains(err.Error(), "--clear cannot be combined") {
		t.Fatalf("clear error = %v", err)
	}
}
