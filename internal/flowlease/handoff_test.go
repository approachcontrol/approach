package flowlease

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestHandoffRecordsAreImmutableAndIdentityBound(t *testing.T) {
	root := secureTempRoot(t)
	attempt := handoffAttempt{
		Root:       root,
		FlowID:     "flow-1",
		PhaseID:    "implementation",
		LaunchID:   "launch-1",
		Nonce:      "0123456789abcdef",
		HandoffDir: filepath.Join(root, handoffCollection, "launch-1-deadbeef"),
	}
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordReady, ""); err != nil {
		t.Fatalf("publish ready error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordReady, ""); !errors.Is(err, errRecordExists) {
		t.Fatalf("publish duplicate ready error = %v, want errRecordExists", err)
	}
	if _, err := readHandoffRecord(attempt, recordReady); err != nil {
		t.Fatalf("read ready error = %v", err)
	}

	if err := publishHandoffRecord(attempt, recordDecision, decisionCommit); err != nil {
		t.Fatalf("publish commit error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionAbort); !errors.Is(err, errRecordExists) {
		t.Fatalf("publish competing abort error = %v, want errRecordExists", err)
	}
	record, err := readHandoffRecord(attempt, recordDecision)
	if err != nil {
		t.Fatalf("read decision error = %v", err)
	}
	if record.Outcome != decisionCommit {
		t.Fatalf("decision outcome = %q, want %q", record.Outcome, decisionCommit)
	}
	if err := publishHandoffFailure(attempt, "agent start failed"); err != nil {
		t.Fatalf("publish failure error = %v", err)
	}
	if err := publishHandoffFailure(attempt, "replacement failure"); !errors.Is(err, errRecordExists) {
		t.Fatalf("publish duplicate failure error = %v, want errRecordExists", err)
	}
	failure, err := readHandoffRecord(attempt, recordFailure)
	if err != nil {
		t.Fatalf("read failure error = %v", err)
	}
	if failure.Detail != "agent start failed" {
		t.Fatalf("failure detail = %q, want immutable original", failure.Detail)
	}

	wrong := attempt
	wrong.Nonce = "wrong-nonce"
	if _, err := readHandoffRecord(wrong, recordReady); err == nil {
		t.Fatal("read ready with wrong nonce error = nil, want error")
	}

	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
	}
	if _, err := os.Lstat(attempt.HandoffDir); !os.IsNotExist(err) {
		t.Fatalf("Lstat(cleaned handoff) error = %v, want not exist", err)
	}
}

func TestHandoffRecordIsInvisibleUntilVerifiedPublication(t *testing.T) {
	root := secureTempRoot(t)
	attempt := handoffAttempt{
		Root:       root,
		FlowID:     "flow-1",
		PhaseID:    "implementation",
		LaunchID:   "launch-1",
		Nonce:      "0123456789abcdef",
		HandoffDir: filepath.Join(root, handoffCollection, "launch-1-deadbeef"),
	}
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}

	staged := make(chan struct{})
	publish := make(chan struct{})
	errCh := make(chan error, 1)
	var once sync.Once
	go func() {
		errCh <- publishHandoffRecordWithHook(attempt, recordStarted, "", func() {
			once.Do(func() { close(staged) })
			<-publish
		})
	}()
	<-staged
	if _, err := readHandoffRecord(attempt, recordStarted); !isMissingRecord(err) {
		t.Fatalf("read staged started error = %v, want not published", err)
	}
	close(publish)
	if err := <-errCh; err != nil {
		t.Fatalf("publish started error = %v", err)
	}
	if _, err := readHandoffRecord(attempt, recordStarted); err != nil {
		t.Fatalf("read published started error = %v", err)
	}
}

func TestStartedRecordIsReadBackBeforeLaunchConfirmation(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	startedPath := filepath.Join(attempt.HandoffDir, recordStarted)
	err := publishAndReadStarted(attempt, func() {
		if chmodErr := os.Chmod(startedPath, 0o400); chmodErr != nil {
			t.Fatalf("Chmod(started) error = %v", chmodErr)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("publishAndReadStarted() error = %v, want secure readback failure", err)
	}
	if err := os.Chmod(startedPath, 0o600); err != nil {
		t.Fatalf("restore started permissions error = %v", err)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
	}
}

func TestTmuxCreateTimeoutDoesNotRetrySameWindowName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		results  []error
		wantRuns int
	}{
		{
			name:     "existing session window timeout",
			results:  []error{nil, errTmuxCommandTimeout},
			wantRuns: 2,
		},
		{
			name:     "new session timeout",
			results:  []error{errors.New("session absent"), errTmuxCommandTimeout},
			wantRuns: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := testPrivateSpec(t)
			runs := 0
			err := runTmuxWindowCreate(spec, func(...string) error {
				result := tc.results[runs]
				runs++
				return result
			})
			if !errors.Is(err, errTmuxCommandTimeout) {
				t.Fatalf("runTmuxWindowCreate() error = %v, want timeout", err)
			}
			if runs != tc.wantRuns {
				t.Fatalf("tmux command runs = %d, want %d without a duplicate mutation", runs, tc.wantRuns)
			}
		})
	}
}

func TestRunnerHoldsLeaseUntilAgentExits(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	marker := filepath.Join(t.TempDir(), "agent-started")
	stop := filepath.Join(t.TempDir(), "agent-stop")
	agentArgv := []string{"/bin/sh", "-c", "printf started > \"$1\"; while [ ! -e \"$2\" ]; do sleep 0.01; done", "runner-test", marker, stop}
	argv, err := LeaseRunArgv("/absolute/approach", spec, agentArgv)
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard) }()

	if _, err := waitForRecord(attempt, recordReady, spec.DecisionDeadline); err != nil {
		t.Fatalf("wait ready error = %v", err)
	}
	if state, err := Inspect(spec.Root, spec.FlowID); err != nil || state != Held {
		t.Fatalf("Inspect(ready) = %v, %v, want Held", state, err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionCommit); err != nil {
		t.Fatalf("publish commit error = %v", err)
	}
	if _, err := waitForRecord(attempt, recordStarted, spec.StartedDeadline); err != nil {
		t.Fatalf("wait started error = %v", err)
	}
	waitForPath(t, marker)
	if err := os.WriteFile(stop, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(stop) error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("RunLeaseRunner() error = %v", err)
	}
	if state, err := Inspect(spec.Root, spec.FlowID); err != nil || state != Free {
		t.Fatalf("Inspect(exited) = %v, %v, want Free", state, err)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
	}
}

func TestChildProcessGroupUsesPaneForegroundPTY(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	attrs := childProcessGroupAttributes(tty)
	if attrs == nil || !attrs.Foreground || !attrs.Setpgid {
		t.Fatalf("child process attributes = %#v, want a foreground process group", attrs)
	}
	if attrs.Ctty != int(tty.Fd()) {
		t.Fatalf("child controlling terminal = %d, want fd %d", attrs.Ctty, tty.Fd())
	}
}

func TestChildProcessGroupCanReadPanePTY(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcessGroupPTYHelper$")
	cmd.Env = append(os.Environ(), "APPROACH_FLOWLEASE_PTY_HELPER=1")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start() error = %v", err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = ptmx.Close()
	}()
	if _, err := io.WriteString(ptmx, "hello\n"); err != nil {
		t.Fatalf("write pane PTY error = %v", err)
	}
	outputCh := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(ptmx)
		outputCh <- output
	}()
	select {
	case output := <-outputCh:
		if !strings.Contains(string(output), "agent-read:hello") {
			t.Fatalf("pane output = %q, want foreground child read marker", output)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("foreground child did not read from the pane PTY")
	}
}

func TestChildProcessGroupPTYHelper(t *testing.T) {
	if os.Getenv("APPROACH_FLOWLEASE_PTY_HELPER") != "1" {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", `IFS= read -r line; printf 'agent-read:%s\n' "$line"`)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = childProcessGroupAttributes(os.Stdin)
	if err := cmd.Run(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestLeaseRunnerStartFailureIsPublishedToParent(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/definitely/missing-flow-agent"})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard) }()
	if _, err := waitForRecordOrFailure(attempt, recordReady, spec.DecisionDeadline); err != nil {
		t.Fatalf("wait ready error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionCommit); err != nil {
		t.Fatalf("publish commit error = %v", err)
	}
	if _, err := waitForRecordOrFailure(attempt, recordStarted, spec.StartedDeadline); err == nil ||
		!strings.Contains(err.Error(), "start tracked Flow agent") ||
		!strings.Contains(err.Error(), "/definitely/missing-flow-agent") {
		t.Fatalf("parent failure = %v, want bounded runner start diagnostic", err)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "start tracked Flow agent") {
		t.Fatalf("RunLeaseRunner() error = %v, want start failure", err)
	}
}

func TestLeaseRunnerAcquireFailureIsPublishedBeforeReady(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	lease, err := Acquire(spec.Root, spec.FlowID)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lease.Close()
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/bin/true"})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard) }()
	if _, err := waitForRecordOrFailure(attempt, recordReady, spec.DecisionDeadline); err == nil ||
		!strings.Contains(err.Error(), "acquire tracked Flow lease") {
		t.Fatalf("parent failure = %v, want lease-acquisition diagnostic before ready", err)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "acquire tracked Flow lease") {
		t.Fatalf("RunLeaseRunner() error = %v, want acquisition failure", err)
	}
}

func TestTerminateProcessGroupWaitsForDescendantsAfterLeaderExits(t *testing.T) {
	childPIDPath := filepath.Join(t.TempDir(), "child-pid")
	cmd := exec.Command("/bin/sh", "-c", `
trap 'exit 0' TERM
(trap '' TERM; while :; do sleep 1; done) &
printf '%d\n' "$!" > "$1"
while :; do sleep 1; done
`, "flowlease-group-test", childPIDPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	waitForPath(t, childPIDPath)
	data, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatalf("ReadFile(child PID) error = %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child PID %q: %v", data, err)
	}

	if err := terminateProcessGroup(pid, waitCh); err != nil {
		t.Fatalf("terminateProcessGroup() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant PID %d still exists after process-group cleanup returned", childPID)
}

func TestRunnerAbortNeverStartsAgent(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-start")
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/bin/sh", "-c", "touch \"$1\"", "runner-test", marker})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard) }()
	if _, err := waitForRecord(attempt, recordReady, spec.DecisionDeadline); err != nil {
		t.Fatalf("wait ready error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionAbort); err != nil {
		t.Fatalf("publish abort error = %v", err)
	}
	if err := <-errCh; err == nil {
		t.Fatal("RunLeaseRunner(abort) error = nil, want error")
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("Lstat(agent marker) error = %v, want not exist", err)
	}
	if state, err := Inspect(spec.Root, spec.FlowID); err != nil || state != Free {
		t.Fatalf("Inspect(aborted) = %v, %v, want Free", state, err)
	}
}

func TestExactWindowTargetRejectsTmuxParsingCharacters(t *testing.T) {
	target, err := ExactWindowTarget("approach-repo-1234", "implementation-deadbeef-12345678")
	if err != nil {
		t.Fatalf("ExactWindowTarget() error = %v", err)
	}
	if target != "=approach-repo-1234:=implementation-deadbeef-12345678" {
		t.Fatalf("target = %q", target)
	}
	for _, value := range []string{"prefix:other", "prefix.other", "prefix;other", "=marker", "line\nbreak"} {
		if _, err := ExactWindowTarget(value, "window"); err == nil {
			t.Fatalf("ExactWindowTarget(%q, window) error = nil, want rejection", value)
		}
		if _, err := ExactWindowTarget("session", value); err == nil {
			t.Fatalf("ExactWindowTarget(session, %q) error = nil, want rejection", value)
		}
	}
}

func TestHandoffRejectsReusedOrUnsafeAttemptDirectory(t *testing.T) {
	root := secureTempRoot(t)
	attempt := handoffAttempt{
		Root:       root,
		FlowID:     "flow-1",
		PhaseID:    "implementation",
		LaunchID:   "launch-1",
		Nonce:      "0123456789abcdef",
		HandoffDir: filepath.Join(root, handoffCollection, "launch-1-deadbeef"),
	}
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	if err := createHandoff(attempt); err == nil {
		t.Fatal("createHandoff(reuse) error = nil, want error")
	}

	unsafe := attempt
	unsafe.HandoffDir = filepath.Join(root, handoffCollection, "..", "escape")
	if err := createHandoff(unsafe); err == nil {
		t.Fatal("createHandoff(escape) error = nil, want error")
	}
}

func secureTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	canonical, err := ResolveRoot(root)
	if err != nil {
		t.Fatalf("ResolveRoot() error = %v", err)
	}
	return canonical
}

func testPrivateSpec(t *testing.T) PrivateSpec {
	t.Helper()
	root := secureTempRoot(t)
	now := time.Now()
	return PrivateSpec{
		SessionName:      "approach-repo-1234",
		WindowName:       "implementation-deadbeef-12345678",
		CWD:              root,
		ScriptPath:       "/absolute/launch-script",
		Root:             root,
		FlowID:           "flow-1",
		PhaseID:          "implementation",
		LaunchID:         "launch-1",
		HandoffDir:       filepath.Join(root, handoffCollection, "launch-1-deadbeef"),
		Nonce:            "0123456789abcdef",
		DecisionDeadline: now.Add(2 * time.Second),
		StartedDeadline:  now.Add(4 * time.Second),
		CleanupDeadline:  now.Add(6 * time.Second),
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
