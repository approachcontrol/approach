package launchcontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func newTestStore(t *testing.T) (*flowstore.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, root
}

func createFlow(t *testing.T, store *flowstore.Store, title string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.Create(flowstore.FlowRecord{
		Title:        title,
		Instructions: "test flow",
		RepoPath:     filepath.Join(t.TempDir(), "repo"),
	})
	if err != nil {
		t.Fatalf("Create(%q): %v", title, err)
	}
	return record
}

// launchPhase records a launch against a phase, which marks it running.
func launchPhase(t *testing.T, store *flowstore.Store, flowID, phaseID, launchID string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: flowID, PhaseID: phaseID, LaunchID: launchID})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID(%s/%s/%s): %v", flowID, phaseID, launchID, err)
	}
	return record
}

func completePhase(t *testing.T, store *flowstore.Store, flowID, phaseID, outcome string) flowstore.FlowRecord {
	t.Helper()
	record, err := store.SetPhase(flowstore.PhaseUpdate{FlowID: flowID, PhaseID: phaseID, Status: flowstore.PhaseCompleted, Outcome: outcome})
	if err != nil {
		t.Fatalf("SetPhase(%s/%s completed): %v", flowID, phaseID, err)
	}
	return record
}

func phaseOf(t *testing.T, store *flowstore.Store, flowID, phaseID string) flowstore.FlowPhase {
	t.Helper()
	record, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read(%s): %v", flowID, err)
	}
	phase, ok := PhaseByID(record, phaseID)
	if !ok {
		t.Fatalf("phase %s not in %s", phaseID, flowID)
	}
	return phase
}

func mustRequest(t *testing.T, verb Verb, flowID, phaseID, launchID string, payload any) Request {
	t.Helper()
	req, err := NewRequest(verb, payload)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", verb, err)
	}
	req.FlowID = flowID
	req.PhaseID = phaseID
	req.LaunchID = launchID
	return req
}

func mustEnvelope(t *testing.T, req Request, writtenBy string) RequestEnvelope {
	t.Helper()
	return RequestEnvelope{
		RequestID:  req.RequestID,
		LaunchID:   req.LaunchID,
		FlowID:     req.FlowID,
		PhaseID:    req.PhaseID,
		Verb:       req.Verb,
		Replayable: Replayable(req.Verb),
		Payload:    req.Payload,
		WrittenBy:  writtenBy,
	}
}

func decodeResult[T any](t *testing.T, resp Response) T {
	t.Helper()
	var value T
	if !resp.OK {
		t.Fatalf("response not OK: refused=%v error=%q", resp.Refused, resp.Error)
	}
	if err := json.Unmarshal(resp.Result, &value); err != nil {
		t.Fatalf("decode result: %v\n%s", err, resp.Result)
	}
	return value
}

func newTestController(t *testing.T, store *flowstore.Store, root string, opts ...func(*Options)) *Controller {
	t.Helper()
	options := Options{Root: root, Store: store, Now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(&options)
	}
	c, err := New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// shortTempDir is a temp dir under /tmp: t.TempDir() paths on macOS exceed
// the sockaddr limit, and a socket that cannot bind proves nothing.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
