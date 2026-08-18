package launchcontrol

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

// serveTestController registers a launch on a listening controller.
func serveTestController(t *testing.T, store *flowstore.Store, root string, reg Registration) (*Controller, Endpoint) {
	t.Helper()
	c := newTestController(t, store, root)
	if err := c.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	endpoint, err := c.Register(reg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if endpoint.Path == "" || len(endpoint.Token) != 64 {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	return c, endpoint
}

func TestControllerServesRegisteredLaunchAndLogsWrites(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Served")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	c, endpoint := serveTestController(t, store, root, Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1", Kind: "phase"})
	var events []AppliedEvent
	c.SetAppliedNotifier(func(e AppliedEvent) { events = append(events, e) })

	client := Client{Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "plan"}
	req, _ := NewRequest(VerbFlowRead, ReadPayload{})
	resp, err := client.Call(req)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if record := decodeResult[flowstore.FlowRecord](t, resp); record.FlowID != created.FlowID {
		t.Fatalf("read = %#v", record)
	}
	req, _ = NewRequest(VerbPhaseComplete, PhaseActionPayload{Summary: "done"})
	resp, err = client.Call(req)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	result := decodeResult[PhaseActionResult](t, resp)
	if result.UpdatedPhase.Status != flowstore.PhaseCompleted {
		t.Fatalf("complete = %#v", result.UpdatedPhase)
	}
	log, _ := OpenLog(root, "launch-1")
	requests, _ := log.Requests()
	if len(requests) != 1 || requests[0].Verb != VerbPhaseComplete || requests[0].WrittenBy != WrittenByController || !requests[0].Replayable || requests[0].Observed.Status != flowstore.PhaseRunning {
		t.Fatalf("logged requests = %#v", requests)
	}
	applied, ok, _ := log.Applied()
	if !ok || applied.AppliedSeq != 1 || applied.Status != flowstore.PhaseCompleted || applied.Result != ResultApplied {
		t.Fatalf("applied = %#v", applied)
	}
	if len(events) != 1 || events[0].LaunchID != "launch-1" || events[0].PhaseID != "plan" {
		t.Fatalf("events = %#v", events)
	}
	info, ok, _ := log.Launch()
	if !ok || info.FlowID != created.FlowID || info.PhaseID != "plan" || info.Kind != "phase" || info.TokenSHA256 != tokenDigest(endpoint.Token) {
		t.Fatalf("launch.json = %#v", info)
	}
	if strings.Contains(string(mustReadFile(t, filepath.Join(log.Dir(), "launch.json"))), endpoint.Token) {
		t.Fatal("token persisted in clear")
	}
	// A refused write still advances the marker so replay never retries it.
	req, _ = NewRequest(VerbPhaseSet, PhaseSetPayload{Status: flowstore.PhaseSkipped})
	resp, err = client.Call(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !resp.Refused {
		t.Fatalf("skipped without notes = %#v", resp)
	}
	applied, _, _ = log.Applied()
	if applied.AppliedSeq != 2 || applied.Result != ResultRefused || applied.Status != flowstore.PhaseCompleted {
		t.Fatalf("applied after refusal = %#v", applied)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %#v", pending)
	}
	if info, err := os.Stat(endpoint.Path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket = %v %v", info, err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestControllerRefusesIdentityMismatches(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Identity")
	other := createFlow(t, store, "Other")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	_, endpoint := serveTestController(t, store, root, Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"})
	good := Client{Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "plan"}
	cases := map[string]Client{
		"token":        {Endpoint: endpoint.Path, Token: strings.Repeat("0", 64), LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "plan"},
		"flow":         {Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: other.FlowID, PhaseID: "plan"},
		"phase":        {Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "implementation"},
		"unregistered": {Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-9", FlowID: created.FlowID, PhaseID: "plan"},
	}
	for name, client := range cases {
		req, _ := NewRequest(VerbPhaseComplete, PhaseActionPayload{})
		resp, err := client.Call(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.OK || !resp.Refused || resp.Error != "launch identity mismatch" {
			t.Fatalf("%s response = %#v", name, resp)
		}
	}
	log, _ := OpenLog(root, "launch-1")
	if requests, _ := log.Requests(); len(requests) != 0 {
		t.Fatalf("mismatches were logged: %#v", requests)
	}
	if phaseOf(t, store, created.FlowID, "plan").Status != flowstore.PhaseRunning {
		t.Fatal("phase changed by a refused request")
	}
	// Reads on other phases of the same Flow are fine; the direct verbs are not proxied.
	req, _ := NewRequest(VerbFlowRead, ReadPayload{})
	if resp, err := good.Call(req); err != nil || !resp.OK {
		t.Fatalf("good read = %#v %v", resp, err)
	}
	req, _ = NewRequest(VerbPhaseReset, PhaseResetPayload{})
	if resp, err := good.Call(req); err != nil || resp.OK || !resp.Refused || !strings.Contains(resp.Error, "not proxied") {
		t.Fatalf("direct verb over socket = %#v %v", resp, err)
	}
}

func TestControllerUnownedLaunchAcceptsAnyPhaseAndMarksUnowned(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Unowned")
	_, endpoint := serveTestController(t, store, root, Registration{FlowID: created.FlowID, LaunchID: "launch-auto", Kind: "autofix"})
	client := Client{Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-auto", FlowID: created.FlowID}
	req, _ := NewRequest(VerbPhaseComplete, PhaseActionPayload{})
	req.PhaseID = "plan"
	resp, err := client.Call(req)
	if err != nil || !resp.OK {
		t.Fatalf("unowned complete = %#v %v", resp, err)
	}
	log, _ := OpenLog(root, "launch-auto")
	requests, _ := log.Requests()
	if len(requests) != 1 || !requests[0].Unowned || requests[0].PhaseID != "plan" {
		t.Fatalf("requests = %#v", requests)
	}
	if _, ok, _ := log.Baseline(); ok {
		t.Fatal("unowned launch has a baseline")
	}
}

func TestControllerRestartReloadsRegistrationsAndReplaysPending(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Restart")
	launchPhase(t, store, created.FlowID, "plan", "launch-1")
	first, endpoint := serveTestController(t, store, root, Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"})
	// Simulate a spooled request that the first controller never applied.
	log, _ := OpenLog(root, "launch-1")
	if err := log.WriteBaseline(Baseline{BaselineStatus: flowstore.PhaseRunning}); err != nil {
		t.Fatal(err)
	}
	req := mustRequest(t, VerbPhaseComplete, created.FlowID, "plan", "launch-1", PhaseActionPayload{Summary: "spooled"})
	if _, err := log.Append(mustEnvelope(t, req, WrittenBySpool)); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Leave a stale socket file behind, as a crashed TUI would.
	stale := filepath.Join(filepath.Dir(endpoint.Path), "deadbeef.sock")
	l, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()

	second := newTestController(t, store, root)
	report, err := second.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if report.Replayed != 1 {
		t.Fatalf("report = %#v, want one replayed request", report)
	}
	if phase := phaseOf(t, store, created.FlowID, "plan"); phase.Status != flowstore.PhaseCompleted || phase.Summary != "spooled" {
		t.Fatalf("plan after recover = %#v", phase)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket not reaped: %v", err)
	}
	if err := second.Listen(); err != nil {
		t.Fatalf("second Listen: %v", err)
	}
	if second.SocketPath() != endpoint.Path {
		t.Fatalf("socket path changed across restart: %s vs %s", second.SocketPath(), endpoint.Path)
	}
	client := Client{Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "plan"}
	readReq, _ := NewRequest(VerbFlowRead, ReadPayload{})
	resp, err := client.Call(readReq)
	if err != nil || !resp.OK {
		t.Fatalf("old token after restart = %#v %v", resp, err)
	}
}

func TestListenReportsLiveOwnerAndReplacesDeadSocket(t *testing.T) {
	store, root := newTestStore(t)
	first := newTestController(t, store, root)
	if err := first.Listen(); err != nil {
		t.Fatal(err)
	}
	second := newTestController(t, store, root)
	if err := second.Listen(); !errors.Is(err, ErrEndpointOwned) {
		t.Fatalf("second Listen error = %v, want ErrEndpointOwned", err)
	}
	if endpoint, err := second.Register(Registration{FlowID: "f", LaunchID: "launch-2"}); err != nil || endpoint.Path != "" {
		t.Fatalf("Register without a socket = %#v %v", endpoint, err)
	}
	path := first.SocketPath()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// A dead socket file at the path is replaced.
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if err := second.Listen(); err != nil {
		t.Fatalf("Listen over dead socket: %v", err)
	}
	if socketAlive(path) != true {
		t.Fatal("second controller is not accepting")
	}
	ReapStale(filepath.Dir(path))
	if !socketAlive(path) {
		t.Fatal("live socket was reaped")
	}
}

func TestSocketPathFallsBackWhenOverCapAndNeverTruncates(t *testing.T) {
	root := t.TempDir()
	original := socketDirCandidates
	t.Cleanup(func() { socketDirCandidates = original })
	long := filepath.Join(t.TempDir(), strings.Repeat("d", maxSocketPathLen()))
	short := filepath.Join(shortTempDir(t), "s")
	socketDirCandidates = func() []string { return []string{long, short} }
	path, ok := SocketPath(root)
	if !ok || !strings.HasPrefix(path, short) {
		t.Fatalf("SocketPath = %q %v, want fallback under %s", path, ok, short)
	}
	socketDirCandidates = func() []string { return []string{long} }
	if path, ok := SocketPath(root); ok || path != "" {
		t.Fatalf("SocketPath over cap = %q %v", path, ok)
	}
	if name := SocketName(root); len(name) != len("deadbeef.sock") || SocketName(root+"-other") == name {
		t.Fatalf("SocketName = %q", name)
	}
}

func TestServerRefusesOversizeFrameAndClientClassifiesTransportFailure(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Frames")
	_, endpoint := serveTestController(t, store, root, Registration{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"})
	conn, err := net.Dial("unix", endpoint.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	huge := strings.Repeat("x", maxFrameBytes+10)
	// The server stops reading once the cap is passed, so the tail of the write
	// may fail; what matters is that it answers with a refusal or drops the
	// connection, and keeps serving afterwards.
	_, _ = conn.Write([]byte(huge))
	var resp Response
	if err := readFrame(conn, &resp); err == nil && (resp.OK || !resp.Refused) {
		t.Fatalf("oversize frame response = %#v", resp)
	}
	good := Client{Endpoint: endpoint.Path, Token: endpoint.Token, LaunchID: "launch-1", FlowID: created.FlowID, PhaseID: "plan"}
	okReq, _ := NewRequest(VerbFlowRead, ReadPayload{})
	if resp, err := good.Call(okReq); err != nil || !resp.OK {
		t.Fatalf("server did not survive the oversize frame: %#v %v", resp, err)
	}
	client := Client{Endpoint: filepath.Join(t.TempDir(), "missing.sock"), Token: "t", LaunchID: "launch-1", FlowID: created.FlowID}
	req, _ := NewRequest(VerbFlowRead, ReadPayload{})
	if _, err := client.Call(req); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("missing endpoint error = %v, want ErrUnreachable", err)
	}
	// A listener that closes without answering is unreachable, not refused.
	silent := filepath.Join(shortTempDir(t), "silent.sock")
	l, err := net.Listen("unix", silent)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	client.Endpoint = silent
	client.Timeout = 2 * time.Second
	if _, err := client.Call(req); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("silent endpoint error = %v, want ErrUnreachable", err)
	}
}

func TestRecordBaselineWritesStatusForExactlyThatLaunch(t *testing.T) {
	store, root := newTestStore(t)
	created := createFlow(t, store, "Baseline")
	next := RecordBaseline(root, store.AddPhaseLaunchID)
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-1")
	baseline, ok, err := log.Baseline()
	if err != nil || !ok || baseline.BaselineStatus != flowstore.PhaseRunning || baseline.ObservedUpdatedAt.IsZero() {
		t.Fatalf("baseline = %#v %v %v", baseline, ok, err)
	}
	completePhase(t, store, created.FlowID, "plan", "")
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: "plan", LaunchID: "launch-2", Resume: true}); err != nil {
		t.Fatal(err)
	}
	log2, _ := OpenLog(root, "launch-2")
	baseline, _, _ = log2.Baseline()
	if baseline.BaselineStatus != flowstore.PhaseCompleted {
		t.Fatalf("resume baseline = %#v", baseline)
	}
	if _, err := next(flowstore.PhaseLaunchUpdate{FlowID: created.FlowID, PhaseID: "nope", LaunchID: "launch-3"}); err == nil {
		t.Fatal("expected failure")
	}
	if log3, _ := OpenLog(root, "launch-3"); log3.Exists() {
		t.Fatal("failed publication wrote a baseline")
	}
	if RecordBaseline("", store.AddPhaseLaunchID) == nil {
		t.Fatal("blank root returned nil")
	}
}

func TestRecordLaunchExitWritesExitJSON(t *testing.T) {
	root := t.TempDir()
	ended := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	if err := RecordLaunchExit(root, "flow-1", "Plan", "launch-1", 137, true, ended); err != nil {
		t.Fatal(err)
	}
	log, _ := OpenLog(root, "launch-1")
	exit, ok, err := log.Exit()
	if err != nil || !ok || exit.ExitCode != 137 || !exit.Signaled || exit.PhaseID != "plan" || !exit.EndedAt.Equal(ended) || exit.Source != "lease_runner" {
		t.Fatalf("exit = %#v %v %v", exit, ok, err)
	}
}

func TestNotifyBeforeSetAppliedNotifierNeitherPanicsNorBlocks(t *testing.T) {
	store, root := newTestStore(t)
	c := newTestController(t, store, root)
	done := make(chan struct{})
	go func() {
		c.notify(AppliedEvent{FlowID: "f", PhaseID: "plan", LaunchID: "launch-1"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notify blocked without a notifier")
	}
	got := make(chan AppliedEvent, 1)
	c.SetAppliedNotifier(func(e AppliedEvent) { got <- e })
	c.notify(AppliedEvent{LaunchID: "launch-2"})
	if e := <-got; e.LaunchID != "launch-2" {
		t.Fatalf("event = %#v", e)
	}
}
