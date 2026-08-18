package sessions_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/flowlease"
	"github.com/approachcontrol/approach/internal/launchcontrol"
	"github.com/approachcontrol/approach/sessions"
)

func newRunningFlowLaunch(t *testing.T, root, launchID string) (*flowstore.Store, flowstore.FlowRecord) {
	t.Helper()
	flowStore, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = flowStore.Close() })
	flow, err := flowStore.Create(flowstore.FlowRecord{
		FlowID:       "flow-1",
		Title:        "Session End",
		Instructions: "Plan the work",
		RepoPath:     filepath.Join(root, "repo"),
		WorktreePath: filepath.Join(root, "repo-worktrees", "flow-1"),
		Branch:       "flow/session-end",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	next := launchcontrol.RecordBaseline(root, flowStore.AddPhaseLaunchID)
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: flow.FlowID, PhaseID: "plan", LaunchID: launchID}); err != nil {
		t.Fatalf("AddPhaseLaunchID() error = %v", err)
	}
	return flowStore, flow
}

func sessionEndPayload(worktreePath string) []byte {
	return []byte(`{
		"session_id": "claude-flow-1",
		"cwd": ` + quoteJSON(worktreePath) + `,
		"hook_event_name": "SessionEnd",
		"timestamp": "2026-06-06T14:10:00Z"
	}`)
}

func TestIngestHookSessionEndDoesNotDemoteAStillRunningPhase(t *testing.T) {
	root := t.TempDir()
	flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	result, err := sessions.IngestHookWithWarnings(sessions.ProviderClaude, bytes.NewReader(sessionEndPayload(flow.WorktreePath)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		},
	})
	if err != nil {
		t.Fatalf("IngestHookWithWarnings() error = %v", err)
	}
	if result.Record.Status != "ended" {
		t.Fatalf("record status = %q, want ended", result.Record.Status)
	}
	read, err := flowStore.Read(flow.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	phase := flowPhaseByID(t, read, "plan")
	if phase.Status != flowstore.PhaseRunning {
		t.Fatalf("fresh SessionEnd demoted a live phase: %#v", phase)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" {
		t.Fatalf("session still attached: %#v", phase.Sessions)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "marked needs_attention") || strings.Contains(warning, "could not reconcile") {
			t.Fatalf("unexpected warning: %q", warning)
		}
	}
}

func TestIngestHookCodexStopDoesNotDemoteAStillRunningPhase(t *testing.T) {
	root := t.TempDir()
	flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "codex-flow-1",
		"cwd": `+quoteJSON(flow.WorktreePath)+`,
		"hook_event_name": "Stop",
		"timestamp": "2026-06-06T14:10:00Z"
	}`)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		},
	}); err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}
	read, _ := flowStore.Read(flow.FlowID)
	if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseRunning {
		t.Fatalf("Codex Stop demoted a live phase: %#v", phase)
	}
}

func TestIngestHookSessionEndDoesNotDemoteWhileFlowLeaseIsHeld(t *testing.T) {
	root := t.TempDir()
	flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	lease, err := flowlease.Acquire(root, flow.FlowID)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lease.Close()
	result, err := sessions.IngestHookWithWarnings(sessions.ProviderClaude, bytes.NewReader(sessionEndPayload(flow.WorktreePath)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		},
	})
	if err != nil {
		t.Fatalf("IngestHookWithWarnings() error = %v", err)
	}
	read, _ := flowStore.Read(flow.FlowID)
	if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseRunning {
		t.Fatalf("held lease did not veto: %#v", phase)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "could not reconcile") {
			t.Fatalf("unexpected failure warning: %q", warning)
		}
	}
}

func TestIngestHookLastSeenDoesNotReconcile(t *testing.T) {
	root := t.TempDir()
	flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	if _, err := sessions.IngestHook(sessions.ProviderCodex, bytes.NewReader([]byte(`{
		"session_id": "codex-flow-1",
		"cwd": `+quoteJSON(flow.WorktreePath)+`,
		"timestamp": "2026-06-06T14:10:00Z"
	}`)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		},
	}); err != nil {
		t.Fatalf("IngestHook() error = %v", err)
	}
	read, _ := flowStore.Read(flow.FlowID)
	if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseRunning {
		t.Fatalf("last_seen hook demoted the phase: %#v", phase)
	}
}

// A per-turn stop hook is not exit evidence. Codex fires Stop after every turn
// while the CLI stays open, Cursor's stop hook is the same shape, and Claude's
// SessionEnd fires on /clear with the agent still alive: none may demote a
// running phase at hook time, even though each records the session as ended,
// and no lease is held on the default embedded route to veto them.
func TestIngestHookPerTurnStopsDoNotDemoteRunningPhase(t *testing.T) {
	cases := []struct {
		name     string
		provider sessions.Provider
		payload  string
	}{
		{name: "codex Stop", provider: sessions.ProviderCodex, payload: `{
			"session_id": "codex-flow-1",
			"cwd": %s,
			"hook_event_name": "Stop",
			"timestamp": "2026-06-06T14:10:00Z"
		}`},
		{name: "cursor stop", provider: sessions.ProviderCursor, payload: `{
			"conversation_id": "cursor-flow-1",
			"cwd": %s,
			"hook_event_name": "stop",
			"timestamp": "2026-06-06T14:10:00Z"
		}`},
		{name: "claude clear", provider: sessions.ProviderClaude, payload: `{
			"session_id": "claude-flow-1",
			"cwd": %s,
			"hook_event_name": "SessionEnd",
			"reason": "clear",
			"timestamp": "2026-06-06T14:10:00Z"
		}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
			payload := strings.Replace(tc.payload, "%s", quoteJSON(flow.WorktreePath), 1)
			result, err := sessions.IngestHookWithWarnings(tc.provider, bytes.NewReader([]byte(payload)), sessions.IngestOptions{
				Env: map[string]string{
					"APPROACH_LAUNCH_ID":          "launch-flow-1",
					"APPROACH_SESSION_STATE_ROOT": root,
					"APPROACH_FLOW_STATE_ROOT":    root,
					"APPROACH_FLOW_ID":            flow.FlowID,
					"APPROACH_FLOW_PHASE_ID":      "plan",
				},
			})
			if err != nil {
				t.Fatalf("IngestHookWithWarnings() error = %v", err)
			}
			if result.Record.Status != "ended" {
				t.Fatalf("record status = %q, want ended", result.Record.Status)
			}
			read, _ := flowStore.Read(flow.FlowID)
			if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseRunning {
				t.Fatalf("per-turn hook demoted the phase: %#v", phase)
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "marked") || strings.Contains(warning, "could not reconcile") {
					t.Fatalf("unexpected reconciliation warning: %q", warning)
				}
			}
		})
	}
}

// The hook keeps the provider's end reason on the record so the sweep's
// liveness probe can tell a Claude /clear (the agent lives on) from an exit.
func TestIngestHookRecordsTheSessionEndReason(t *testing.T) {
	root := t.TempDir()
	_, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	result, err := sessions.IngestHookWithWarnings(sessions.ProviderClaude, bytes.NewReader([]byte(`{
		"session_id": "claude-flow-1",
		"cwd": `+quoteJSON(flow.WorktreePath)+`,
		"hook_event_name": "SessionEnd",
		"reason": "clear",
		"timestamp": "2026-06-06T14:10:00Z"
	}`)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
		},
	})
	if err != nil {
		t.Fatalf("IngestHookWithWarnings() error = %v", err)
	}
	if result.Record.Status != "ended" || result.Record.EndReason != "clear" {
		t.Fatalf("record = %#v, want ended with reason clear", result.Record)
	}
	store, err := sessions.NewStore(sessions.StoreOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.List(sessions.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].EndReason != "clear" {
		t.Fatalf("stored records = %#v, want the end reason persisted", records)
	}
}

// A per-turn hook is not exit evidence however old its timestamp: a Codex
// Stop, Cursor stop, or Claude /clear payload that carries an ended_at past
// the session-end grace still may not demote a live launch's phase.
func TestIngestHookPerTurnStopsWithOldTimestampsDoNotDemote(t *testing.T) {
	cases := []struct {
		name     string
		provider sessions.Provider
		payload  string
	}{
		{name: "codex Stop", provider: sessions.ProviderCodex, payload: `{
			"session_id": "codex-flow-1",
			"cwd": %s,
			"hook_event_name": "Stop",
			"ended_at": "2020-01-01T00:00:00Z",
			"timestamp": "2020-01-01T00:00:00Z"
		}`},
		{name: "claude clear", provider: sessions.ProviderClaude, payload: `{
			"session_id": "claude-flow-1",
			"cwd": %s,
			"hook_event_name": "SessionEnd",
			"reason": "clear",
			"ended_at": "2020-01-01T00:00:00Z",
			"timestamp": "2020-01-01T00:00:00Z"
		}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
			payload := strings.Replace(tc.payload, "%s", quoteJSON(flow.WorktreePath), 1)
			result, err := sessions.IngestHookWithWarnings(tc.provider, bytes.NewReader([]byte(payload)), sessions.IngestOptions{
				Env: map[string]string{
					"APPROACH_LAUNCH_ID":          "launch-flow-1",
					"APPROACH_SESSION_STATE_ROOT": root,
					"APPROACH_FLOW_STATE_ROOT":    root,
					"APPROACH_FLOW_ID":            flow.FlowID,
					"APPROACH_FLOW_PHASE_ID":      "plan",
				},
			})
			if err != nil {
				t.Fatalf("IngestHookWithWarnings() error = %v", err)
			}
			if result.Record.Status != "ended" {
				t.Fatalf("record status = %q, want ended", result.Record.Status)
			}
			read, _ := flowStore.Read(flow.FlowID)
			if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseRunning {
				t.Fatalf("old per-turn hook demoted the phase: %#v", phase)
			}
		})
	}
	// A Claude SessionEnd that is a real end and is past the grace does demote.
	root := t.TempDir()
	flowStore, flow := newRunningFlowLaunch(t, root, "launch-flow-1")
	if _, err := sessions.IngestHookWithWarnings(sessions.ProviderClaude, bytes.NewReader([]byte(`{
		"session_id": "claude-flow-1",
		"cwd": `+quoteJSON(flow.WorktreePath)+`,
		"hook_event_name": "SessionEnd",
		"reason": "prompt_input_exit",
		"ended_at": "2020-01-01T00:00:00Z",
		"timestamp": "2020-01-01T00:00:00Z"
	}`)), sessions.IngestOptions{
		Env: map[string]string{
			"APPROACH_LAUNCH_ID":          "launch-flow-1",
			"APPROACH_SESSION_STATE_ROOT": root,
			"APPROACH_FLOW_STATE_ROOT":    root,
			"APPROACH_FLOW_ID":            flow.FlowID,
			"APPROACH_FLOW_PHASE_ID":      "plan",
		},
	}); err != nil {
		t.Fatalf("IngestHookWithWarnings() error = %v", err)
	}
	read, _ := flowStore.Read(flow.FlowID)
	if phase := flowPhaseByID(t, read, "plan"); phase.Status != flowstore.PhaseNeedsAttention {
		t.Fatalf("aged real SessionEnd did not demote: %#v", phase)
	}
}
