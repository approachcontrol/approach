package launchcontrol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenLogRefusesUnsafeLaunchIDs(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"", "..", "a/b", "launch id", "../x"} {
		if _, err := OpenLog(root, id); err == nil {
			t.Errorf("OpenLog(%q) accepted", id)
		}
	}
	if _, err := OpenLog("relative", "launch-1"); err == nil {
		t.Error("relative root accepted")
	}
}

func TestLogRefusesSymlinkedLaunchDirectory(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(LaunchesDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(LaunchesDir(root), "launch-1")); err != nil {
		t.Fatal(err)
	}
	log, err := OpenLog(root, "launch-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := log.WriteLaunch(LaunchInfo{FlowID: "f"}); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("WriteLaunch through symlink error = %v", err)
	}
	if _, err := log.Lock(time.Second); err == nil {
		t.Fatal("Lock through symlink succeeded")
	}
}

func TestLogAppendAllocatesMonotonicSequenceAcrossLogsAndGoroutines(t *testing.T) {
	root := t.TempDir()
	first, _ := OpenLog(root, "launch-1")
	second, _ := OpenLog(root, "launch-1")
	var wg sync.WaitGroup
	seqs := make(chan int, 40)
	for _, log := range []*Log{first, second} {
		wg.Add(1)
		go func(log *Log) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				unlock, err := log.Lock(5 * time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				seq, err := log.Append(RequestEnvelope{RequestID: NewRequestID(), FlowID: "f", PhaseID: "plan", Verb: VerbPhaseComplete, Replayable: true, WrittenBy: WrittenByDirect})
				unlock()
				if err != nil {
					t.Error(err)
					return
				}
				seqs <- seq
			}
		}(log)
	}
	wg.Wait()
	close(seqs)
	seen := make(map[int]bool)
	for seq := range seqs {
		if seen[seq] {
			t.Fatalf("sequence %d allocated twice", seq)
		}
		seen[seq] = true
	}
	if len(seen) != 40 {
		t.Fatalf("allocated %d sequences, want 40", len(seen))
	}
	for i := 1; i <= 40; i++ {
		if !seen[i] {
			t.Fatalf("sequence %d missing", i)
		}
	}
	all, err := first.Requests()
	if err != nil || len(all) != 40 {
		t.Fatalf("Requests() = %d, %v", len(all), err)
	}
	for i, env := range all {
		if env.Seq != i+1 {
			t.Fatalf("Requests()[%d].Seq = %d", i, env.Seq)
		}
	}
}

func TestLogAppendIsDurableBeforeAck(t *testing.T) {
	root := t.TempDir()
	log, _ := OpenLog(root, "launch-1")
	original := ackHook
	ackHook = func() error { return errors.New("crash before ack") }
	t.Cleanup(func() { ackHook = original })
	seq, err := log.Append(RequestEnvelope{RequestID: "req-1", FlowID: "f", PhaseID: "plan", Verb: VerbPhaseComplete, Replayable: true, WrittenBy: WrittenBySpool})
	if err == nil || seq != 1 {
		t.Fatalf("Append = %d, %v; want seq 1 and the ack error", seq, err)
	}
	pending, err := log.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RequestID != "req-1" || pending[0].Seq != 1 || pending[0].LaunchID != "launch-1" || pending[0].SchemaVersion != LogSchemaVersion {
		t.Fatalf("Pending() = %#v", pending)
	}
	if err := log.WriteApplied(AppliedState{AppliedSeq: 1, Status: "completed", Result: ResultApplied}); err != nil {
		t.Fatal(err)
	}
	pending, _ = log.Pending()
	if len(pending) != 0 {
		t.Fatalf("Pending() after applied = %#v", pending)
	}
	applied, ok, err := log.Applied()
	if err != nil || !ok || applied.AppliedSeq != 1 || applied.Status != "completed" || applied.AppliedAt.IsZero() {
		t.Fatalf("Applied() = %#v, %v, %v", applied, ok, err)
	}
	if info, err := os.Stat(filepath.Join(log.Dir(), "requests", "000001-req-1.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("request file = %v, %v", info, err)
	}
	if info, err := os.Stat(log.Dir()); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("launch dir = %v, %v", info, err)
	}
}

func TestLogAppendReusesMatchingRequestID(t *testing.T) {
	log, _ := OpenLog(t.TempDir(), "launch-1")
	env := RequestEnvelope{
		RequestID: "req-1", FlowID: "flow-1", PhaseID: "plan",
		Verb: VerbPhaseComplete, Replayable: true,
		Payload: json.RawMessage(`{"summary":"done"}`), WrittenBy: WrittenByController,
	}
	first, err := log.Append(env)
	if err != nil {
		t.Fatal(err)
	}
	env.WrittenBy = WrittenByDirect
	second, err := log.Append(env)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != first {
		t.Fatalf("Append sequences = %d, %d; want 1, 1", first, second)
	}
	requests, err := log.Requests()
	if err != nil || len(requests) != 1 {
		t.Fatalf("Requests() = %#v, %v", requests, err)
	}
}

func TestLogAppendRefusesRequestIDCollision(t *testing.T) {
	log, _ := OpenLog(t.TempDir(), "launch-1")
	base := RequestEnvelope{
		RequestID: "req-1", FlowID: "flow-1", PhaseID: "plan",
		Verb: VerbPhaseSet, Replayable: true,
		Payload: json.RawMessage(`{"status":"completed"}`), WrittenBy: WrittenByController,
	}
	if _, err := log.Append(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RequestEnvelope){
		"flow":    func(env *RequestEnvelope) { env.FlowID = "flow-2" },
		"phase":   func(env *RequestEnvelope) { env.PhaseID = "review" },
		"verb":    func(env *RequestEnvelope) { env.Verb = VerbPhaseComplete },
		"payload": func(env *RequestEnvelope) { env.Payload = json.RawMessage(`{"status":"blocked"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			collision := base
			mutate(&collision)
			if _, err := log.Append(collision); !errors.Is(err, ErrRequestIDCollision) {
				t.Fatalf("Append collision error = %v", err)
			}
		})
	}
	requests, _ := log.Requests()
	if len(requests) != 1 {
		t.Fatalf("Requests() count = %d, want 1", len(requests))
	}
}

func TestLogResponseRoundTrip(t *testing.T) {
	log, _ := OpenLog(t.TempDir(), "launch-1")
	for name, want := range map[string]Response{
		"success": {SchemaVersion: ProtocolSchemaVersion, OK: true, Result: json.RawMessage(`{"status":"completed"}`)},
		"refused": {SchemaVersion: ProtocolSchemaVersion, Refused: true, Error: "transition refused"},
		"warning": {SchemaVersion: ProtocolSchemaVersion, OK: true, Result: json.RawMessage(`[]`), Warning: "partial result"},
	} {
		t.Run(name, func(t *testing.T) {
			requestID := "req-" + name
			if _, err := log.Append(RequestEnvelope{RequestID: requestID, FlowID: "flow-1", Verb: VerbPhaseSet}); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := log.Response(requestID); err != nil || ok {
				t.Fatalf("Response before write = ok %v, err %v", ok, err)
			}
			if err := log.WriteResponse(requestID, want); err != nil {
				t.Fatal(err)
			}
			got, ok, err := log.Response(requestID)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if err != nil || !ok || string(gotJSON) != string(wantJSON) {
				t.Fatalf("Response() = %#v, %v, %v; want %#v", got, ok, err, want)
			}
		})
	}
}

func TestLogAppendRefusesUnsafeRequestID(t *testing.T) {
	root := t.TempDir()
	log, _ := OpenLog(root, "launch-1")
	if _, err := log.Append(RequestEnvelope{RequestID: "../escape", Verb: VerbPhaseSet}); err == nil {
		t.Fatal("unsafe request id accepted")
	}
}

func TestLogRetentionAgeIgnoresSequenceLock(t *testing.T) {
	root := t.TempDir()
	log, _ := OpenLog(root, "launch-1")
	if err := log.WriteLaunch(LaunchInfo{FlowID: "f", PhaseID: "plan"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-20 * 24 * time.Hour)
	for _, name := range []string{"launch.json"} {
		if err := os.Chtimes(filepath.Join(log.Dir(), name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	before, err := log.AgeForRetention()
	if err != nil {
		t.Fatal(err)
	}
	if !before.Before(time.Now().Add(-19 * 24 * time.Hour)) {
		t.Fatalf("age = %v, want ~20 days ago", before)
	}
	unlock, err := log.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	after, err := log.AgeForRetention()
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("taking the lock changed the retention age from %v to %v", before, after)
	}
	if err := log.WriteExit(ExitRecord{FlowID: "f", ExitCode: 3, EndedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	renewed, _ := log.AgeForRetention()
	if !renewed.After(before) {
		t.Fatal("exit.json did not renew the retention age")
	}
	exit, ok, err := log.Exit()
	if err != nil || !ok || exit.ExitCode != 3 || exit.LaunchID != "launch-1" {
		t.Fatalf("Exit() = %#v %v %v", exit, ok, err)
	}
}

func TestLogRejectedAppendsBatches(t *testing.T) {
	root := t.TempDir()
	log, _ := OpenLog(root, "launch-1")
	if err := log.AppendRejected(RejectedBatch{Reason: ReasonPhaseResultStale, IntendedStatus: "completed", ObservedStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := log.AppendRejected(RejectedBatch{Reason: ReasonRequestInvalid, Error: "nope"}); err != nil {
		t.Fatal(err)
	}
	rejected, ok, err := log.Rejected()
	if err != nil || !ok || len(rejected.Batches) != 2 || rejected.Batches[1].Reason != ReasonRequestInvalid || rejected.Batches[0].RejectedAt.IsZero() {
		t.Fatalf("Rejected() = %#v %v %v", rejected, ok, err)
	}
	if err := log.WriteNotice([]string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if text, ok := log.Notice(); !ok || text != "one\ntwo\n" {
		t.Fatalf("Notice() = %q %v", text, ok)
	}
	ids, err := ListLaunchIDs(root)
	if err != nil || len(ids) != 1 || ids[0] != "launch-1" {
		t.Fatalf("ListLaunchIDs = %v %v", ids, err)
	}
}

// A launch directory written by a newer build is left alone: every reader
// refuses a file whose schema_version is above this build's, so replay never
// applies a request it cannot fully understand, and the directory stays
// pending for a controller that can.
func TestLogReadersRefuseFilesFromANewerSchema(t *testing.T) {
	root := t.TempDir()
	log, err := OpenLog(root, "launch-1")
	if err != nil {
		t.Fatal(err)
	}
	req := mustRequest(t, VerbPhaseComplete, "flow-1", "plan", "launch-1", PhaseActionPayload{})
	if _, err := log.Append(mustEnvelope(t, req, WrittenBySpool)); err != nil {
		t.Fatal(err)
	}
	if err := log.WriteLaunch(LaunchInfo{FlowID: "flow-1", PhaseID: "plan"}); err != nil {
		t.Fatal(err)
	}
	if err := log.WriteBaseline(Baseline{BaselineStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	// Bump the schema of each file in turn, as a newer build would have.
	bump := func(path string) {
		t.Helper()
		var raw map[string]any
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		raw["schema_version"] = LogSchemaVersion + 1
		data, _ = json.Marshal(raw)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, _ := log.requestFiles()
	requestPath := filepath.Join(log.Dir(), requestsDir, names[0])
	bump(requestPath)
	if _, err := log.Requests(); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Requests over a newer envelope = %v, want a newer-build refusal", err)
	}
	if _, err := log.Pending(); err == nil {
		t.Fatal("Pending over a newer envelope succeeded")
	}
	// Restore the request; the sidecars refuse the same way.
	if err := os.Remove(requestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(mustEnvelope(t, req, WrittenBySpool)); err != nil {
		t.Fatal(err)
	}
	bump(filepath.Join(log.Dir(), launchFile))
	if _, _, err := log.Launch(); err == nil {
		t.Fatal("Launch over a newer launch.json succeeded")
	}
	bump(filepath.Join(log.Dir(), baselineFile))
	if _, _, err := log.Baseline(); err == nil {
		t.Fatal("Baseline over a newer baseline.json succeeded")
	}
	// The controller leaves such a launch pending rather than replaying it.
	store, storeRoot := newTestStore(t)
	_ = storeRoot
	c, err := New(Options{Root: root, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if report := c.Sweep(); report.Replayed != 0 || report.Reconciled != 0 {
		t.Fatalf("sweep replayed a newer-schema launch: %#v", report)
	}
}
