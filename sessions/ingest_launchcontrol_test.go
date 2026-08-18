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

func TestIngestHookSessionEndDemotesRunningPhaseLeftWithoutAResult(t *testing.T) {
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
	if phase.Status != flowstore.PhaseNeedsAttention || phase.Outcome != launchcontrol.ReasonPhaseResultMissing {
		t.Fatalf("plan phase = %#v", phase)
	}
	if !strings.Contains(phase.Notes, "exited (session_end, exit code unknown)") {
		t.Fatalf("notes = %q", phase.Notes)
	}
	if len(phase.Sessions) != 1 || phase.Sessions[0].Status != "ended" {
		t.Fatalf("session still attached: %#v", phase.Sessions)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "marked needs_attention") {
		t.Fatalf("warnings = %v", result.Warnings)
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
