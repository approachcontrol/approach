package flowlease

import (
	"errors"
	"fmt"
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

func TestHandoffNormalizesPermissionsMaskedByUmask(t *testing.T) {
	root := secureTempRoot(t)
	attempt := handoffAttempt{
		Root:       root,
		FlowID:     "flow-1",
		PhaseID:    "implementation",
		LaunchID:   "launch-1",
		Nonce:      "0123456789abcdef",
		HandoffDir: filepath.Join(root, handoffCollection, "launch-1-deadbeef"),
	}
	previous := syscall.Umask(0o777)
	t.Cleanup(func() { syscall.Umask(previous) })

	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordReady, ""); err != nil {
		t.Fatalf("publishHandoffRecord() error = %v", err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(root, handoffCollection):         0o700,
		attempt.HandoffDir:                             0o700,
		filepath.Join(attempt.HandoffDir, recordReady): 0o600,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%s) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s permissions = %04o, want %04o", path, got, want)
		}
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

func TestStartedConfirmationIsPublishedOnlyAfterRunnerReadback(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	startedPublished := make(chan struct{})
	allowReadback := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- publishAndReadStarted(attempt, func() {
			close(startedPublished)
			<-allowReadback
		})
	}()
	<-startedPublished
	if _, err := readHandoffRecord(attempt, recordStarted); err != nil {
		t.Fatalf("read started error = %v", err)
	}
	if _, err := readHandoffRecord(attempt, recordStartedConfirmed); !isMissingRecord(err) {
		t.Fatalf("confirmation before readback error = %v, want missing", err)
	}
	close(allowReadback)
	if err := <-errCh; err != nil {
		t.Fatalf("publishAndReadStarted() error = %v", err)
	}
	if _, err := readHandoffRecord(attempt, recordStartedConfirmed); err != nil {
		t.Fatalf("read started confirmation error = %v", err)
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

func TestCancelExactWaitsForLeaseReleaseAfterTmuxError(t *testing.T) {
	spec := testPrivateSpec(t)
	spec.CleanupDeadline = time.Now().Add(time.Second)
	inspectCalls := 0
	err := cancelExactAttemptWith(spec,
		func(string, string) error { return errors.New("tmux client failed") },
		func(string, string) (LeaseState, error) {
			inspectCalls++
			if inspectCalls < 3 {
				return Held, nil
			}
			return Free, nil
		},
	)
	if inspectCalls != 3 {
		t.Fatalf("lease inspections = %d, want polling through release", inspectCalls)
	}
	if err == nil || !strings.Contains(err.Error(), "cancel exact tmux window") {
		t.Fatalf("CancelExact() error = %v, want preserved tmux failure", err)
	}
}

func TestCancelExactDoesNotTreatMissingTmuxAsAbsentTarget(t *testing.T) {
	spec := testPrivateSpec(t)
	err := cancelExactAttemptWith(spec,
		func(string, string) error {
			return &exec.Error{Name: "tmux", Err: exec.ErrNotFound}
		},
		func(string, string) (LeaseState, error) { return Free, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("cancelExactAttemptWith() error = %v, want missing executable failure", err)
	}
}

func TestRunTmuxSpawnRoutesConfirmedCleanupFailureThroughCancellation(t *testing.T) {
	spec := testPrivateSpec(t)
	cleanupErr := errors.New("cleanup denied")
	cancelCalled := false
	runnerErr := make(chan error, 1)
	ops := tmuxSpawnOps{
		start: func(spec PrivateSpec, _ io.Writer) error {
			go func() {
				attempt := spec.attempt()
				if err := publishHandoffRecord(attempt, recordReady, ""); err != nil {
					runnerErr <- err
					return
				}
				decision, err := waitForRecord(attempt, recordDecision, spec.DecisionDeadline)
				if err != nil {
					runnerErr <- err
					return
				}
				if decision.Outcome != decisionCommit {
					runnerErr <- fmt.Errorf("decision = %q, want commit", decision.Outcome)
					return
				}
				if err := publishAndReadStarted(attempt, nil); err != nil {
					runnerErr <- err
					return
				}
				runnerErr <- nil
			}()
			return nil
		},
		cleanup: func(handoffAttempt) error { return cleanupErr },
		cancel: func(PrivateSpec) error {
			cancelCalled = true
			return nil
		},
	}
	err := runTmuxSpawn(spec, io.Discard, ops)
	if runnerResult := <-runnerErr; runnerResult != nil {
		t.Fatalf("runner protocol error = %v", runnerResult)
	}
	if !cancelCalled {
		t.Fatal("confirmed cleanup failure did not cancel the exact attempt")
	}
	if err == nil || !strings.Contains(err.Error(), "uncertain committed Flow launch cleanup") || !errors.Is(err, cleanupErr) {
		t.Fatalf("runTmuxSpawn() error = %v, want uncertain committed cleanup failure", err)
	}
	if cleanupErr := cleanupHandoff(spec.attempt()); cleanupErr != nil {
		t.Fatalf("cleanupHandoff(test teardown) error = %v", cleanupErr)
	}
}

func TestRunTmuxSpawnCompletesConfirmedRunnerHandshake(t *testing.T) {
	spec := testPrivateSpec(t)
	prepareRunnerLaunchScript(t, &spec, []string{"/usr/bin/true"})
	installFakeTmux(t, true)
	if err := RunTmuxSpawn(privateFlags(spec), io.Discard); err != nil {
		t.Fatalf("RunTmuxSpawn() error = %v", err)
	}
	if _, err := os.Lstat(spec.HandoffDir); !os.IsNotExist(err) {
		t.Fatalf("Lstat(completed handoff) error = %v, want not exist", err)
	}
	assertFlowLeaseFree(t, spec)
}

func TestRunTmuxSpawnAbortsWhenRunnerNeverBecomesReady(t *testing.T) {
	spec := testPrivateSpec(t)
	now := time.Now()
	spec.DecisionDeadline = now.Add(100 * time.Millisecond)
	spec.StartedDeadline = now.Add(time.Second)
	spec.CleanupDeadline = now.Add(2 * time.Second)
	prepareRunnerLaunchScript(t, &spec, []string{"/usr/bin/true"})
	installFakeTmux(t, false)
	err := RunTmuxSpawn(privateFlags(spec), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wait for Flow lease readiness") {
		t.Fatalf("RunTmuxSpawn() error = %v, want bounded readiness failure", err)
	}
	if _, statErr := os.Lstat(spec.HandoffDir); !os.IsNotExist(statErr) {
		t.Fatalf("Lstat(aborted handoff) error = %v, want not exist", statErr)
	}
	assertFlowLeaseFree(t, spec)
}

func TestRunTmuxSpawnReportsCommittedRunnerStartFailure(t *testing.T) {
	spec := testPrivateSpec(t)
	prepareRunnerLaunchScript(t, &spec, []string{"/definitely/missing-flow-agent"})
	installFakeTmux(t, true)
	err := RunTmuxSpawn(privateFlags(spec), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "uncertain committed Flow launch") ||
		!strings.Contains(err.Error(), "start tracked Flow agent") {
		t.Fatalf("RunTmuxSpawn() error = %v, want committed runner start failure", err)
	}
	if _, statErr := os.Lstat(spec.HandoffDir); !os.IsNotExist(statErr) {
		t.Fatalf("Lstat(failed handoff) error = %v, want not exist", statErr)
	}
	assertFlowLeaseFree(t, spec)
}

func TestRunTmuxSpawnRejectsUnconfirmedRunnerStart(t *testing.T) {
	spec := testPrivateSpec(t)
	now := time.Now()
	spec.DecisionDeadline = now.Add(time.Second)
	spec.StartedDeadline = now.Add(1300 * time.Millisecond)
	spec.CleanupDeadline = now.Add(2 * time.Second)
	prepareRunnerLaunchScript(t, &spec, []string{"/usr/bin/true"})
	t.Setenv("APPROACH_FLOWLEASE_SPAWN_BEHAVIOR", "unconfirmed")
	installFakeTmux(t, true)
	err := RunTmuxSpawn(privateFlags(spec), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "uncertain committed Flow launch confirmation") {
		t.Fatalf("RunTmuxSpawn() error = %v, want missing confirmation failure", err)
	}
	if _, statErr := os.Lstat(spec.HandoffDir); !os.IsNotExist(statErr) {
		t.Fatalf("Lstat(unconfirmed handoff) error = %v, want not exist", statErr)
	}
	assertFlowLeaseFree(t, spec)
}

func TestRunTmuxSpawnRunnerHelper(t *testing.T) {
	if os.Getenv("APPROACH_FLOWLEASE_SPAWN_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	args := os.Args[separator+1:]
	if os.Getenv("APPROACH_FLOWLEASE_SPAWN_BEHAVIOR") == "unconfirmed" {
		if err := runUnconfirmedSpawnHelper(args); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	if err := RunLeaseRunner(args, nil, io.Discard, io.Discard, nil); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func runUnconfirmedSpawnHelper(args []string) error {
	spec, _, err := parsePrivateArgs(args, true)
	if err != nil {
		return err
	}
	if err := validatePrivateSpec(spec, false); err != nil {
		return err
	}
	lease, err := Acquire(spec.Root, spec.FlowID)
	if err != nil {
		return err
	}
	defer lease.Close()
	attempt := spec.attempt()
	if err := publishHandoffRecord(attempt, recordReady, ""); err != nil {
		return err
	}
	decision, err := waitForRecord(attempt, recordDecision, spec.DecisionDeadline)
	if err != nil {
		return err
	}
	if decision.Outcome != decisionCommit {
		return fmt.Errorf("decision = %q, want commit", decision.Outcome)
	}
	return publishHandoffRecord(attempt, recordStarted, "")
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
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, nil) }()

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
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, nil) }()
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
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, nil) }()
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

func TestDuplicateLeaseRunnerDoesNotPublishSharedFailure(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordRunnerClaim, ""); err != nil {
		t.Fatalf("publish runner claim error = %v", err)
	}
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/usr/bin/true"})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	err = RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate Flow lease runner claim") {
		t.Fatalf("RunLeaseRunner() error = %v, want duplicate claim rejection", err)
	}
	if _, failureErr := readHandoffRecord(attempt, recordFailure); !isMissingRecord(failureErr) {
		t.Fatalf("duplicate failure record error = %v, want missing", failureErr)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
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

func TestSuperviseProcessGroupWaitsForDescendantsAfterLeaderExits(t *testing.T) {
	childPIDPath := filepath.Join(t.TempDir(), "child-pid")
	cmd := exec.Command("/bin/sh", "-c", `
(trap '' HUP; while :; do sleep 1; done) &
printf '%d\n' "$!" > "$1"
exit 0
`, "flowlease-supervise-test", childPIDPath)
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

	if err := superviseProcessGroup(pid, waitCh, make(chan os.Signal)); err != nil {
		t.Fatalf("superviseProcessGroup() error = %v", err)
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("descendant PID %d still exists after supervision returned", childPID)
	}
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
	go func() { errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, nil) }()
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

func TestExistingHandoffRejectsReplacedCollectionSymlink(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	collection := filepath.Join(spec.Root, handoffCollection)
	relocated := filepath.Join(spec.Root, "relocated-handoffs")
	if err := os.Rename(collection, relocated); err != nil {
		t.Fatalf("Rename(handoff collection) error = %v", err)
	}
	if err := os.Symlink(relocated, collection); err != nil {
		t.Fatalf("Symlink(handoff collection) error = %v", err)
	}
	if err := validateExistingHandoff(attempt); err == nil {
		t.Fatal("validateExistingHandoff() error = nil, want collection symlink rejection")
	}
	if err := os.Remove(collection); err != nil {
		t.Fatalf("Remove(collection symlink) error = %v", err)
	}
	if err := os.Rename(relocated, collection); err != nil {
		t.Fatalf("restore handoff collection error = %v", err)
	}
	if err := cleanupHandoff(attempt); err != nil {
		t.Fatalf("cleanupHandoff() error = %v", err)
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

func prepareRunnerLaunchScript(t *testing.T, spec *PrivateSpec, agentArgv []string) {
	t.Helper()
	spec.ScriptPath = filepath.Join(t.TempDir(), "flow-launch.sh")
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("Abs(test binary) error = %v", err)
	}
	args := []string{binary, "-test.run=^TestRunTmuxSpawnRunnerHelper$", "--"}
	args = append(args, privateFlags(*spec)...)
	args = append(args, "--")
	args = append(args, agentArgv...)
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	body := "#!/bin/sh\nAPPROACH_FLOWLEASE_SPAWN_HELPER=1 exec " + strings.Join(quoted, " ") + "\n"
	if err := os.WriteFile(spec.ScriptPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(runner launch script) error = %v", err)
	}
}

func installFakeTmux(t *testing.T, launch bool) {
	t.Helper()
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	body := `#!/bin/sh
case "$1" in
  has-session) exit 1 ;;
  new-session|new-window)
    if [ "${APPROACH_FLOWLEASE_FAKE_TMUX_LAUNCH:-}" = "1" ]; then
      for last do :; done
      nohup /bin/sh -c "$last" </dev/null >/dev/null 2>&1 &
    fi
    exit 0
    ;;
  kill-window) exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(tmuxPath, []byte(body), 0o700); err != nil {
		t.Fatalf("WriteFile(fake tmux) error = %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if launch {
		t.Setenv("APPROACH_FLOWLEASE_FAKE_TMUX_LAUNCH", "1")
	} else {
		t.Setenv("APPROACH_FLOWLEASE_FAKE_TMUX_LAUNCH", "")
	}
}

func assertFlowLeaseFree(t *testing.T, spec PrivateSpec) {
	t.Helper()
	state, err := Inspect(spec.Root, spec.FlowID)
	if err != nil || state != Free {
		t.Fatalf("Inspect(%s) = %v, %v, want Free", spec.FlowID, state, err)
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

func TestLeaseRunnerReportsExitExactlyOnceAfterAgentEnds(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/bin/sh", "-c", "exit 7"})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	exits := make(chan LaunchExit, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, func(exit LaunchExit) {
			// The lease is still held while the exit is reported.
			if state, err := Inspect(spec.Root, spec.FlowID); err != nil || state != Held {
				t.Errorf("Inspect(onExit) = %v, %v, want Held", state, err)
			}
			exits <- exit
		})
	}()
	if _, err := waitForRecord(attempt, recordReady, spec.DecisionDeadline); err != nil {
		t.Fatalf("wait ready error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionCommit); err != nil {
		t.Fatalf("publish commit error = %v", err)
	}
	err = <-errCh
	var processExit ProcessExitError
	if !errors.As(err, &processExit) || processExit.Code != 7 {
		t.Fatalf("RunLeaseRunner() error = %v, want exit status 7", err)
	}
	select {
	case exit := <-exits:
		if exit.Code != 7 || exit.Signaled || exit.LaunchID != spec.LaunchID || exit.FlowID != spec.FlowID || exit.PhaseID != spec.PhaseID || exit.Root != spec.Root || exit.EndedAt.IsZero() {
			t.Fatalf("LaunchExit = %#v", exit)
		}
	default:
		t.Fatal("onExit was not called")
	}
	select {
	case exit := <-exits:
		t.Fatalf("onExit called twice: %#v", exit)
	default:
	}
	_ = cleanupHandoff(attempt)
}

func TestLeaseRunnerDoesNotReportExitWhenAbortedBeforeStart(t *testing.T) {
	spec := testPrivateSpec(t)
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		t.Fatalf("createHandoff() error = %v", err)
	}
	argv, err := LeaseRunArgv("/absolute/approach", spec, []string{"/bin/sh", "-c", "exit 0"})
	if err != nil {
		t.Fatalf("LeaseRunArgv() error = %v", err)
	}
	called := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunLeaseRunner(argv[2:], nil, io.Discard, io.Discard, func(LaunchExit) { called <- struct{}{} })
	}()
	if _, err := waitForRecord(attempt, recordReady, spec.DecisionDeadline); err != nil {
		t.Fatalf("wait ready error = %v", err)
	}
	if err := publishHandoffRecord(attempt, recordDecision, decisionAbort); err != nil {
		t.Fatalf("publish abort error = %v", err)
	}
	if err := <-errCh; err == nil {
		t.Fatal("RunLeaseRunner(abort) error = nil, want error")
	}
	select {
	case <-called:
		t.Fatal("onExit called for a runner that never started its agent")
	default:
	}
}
