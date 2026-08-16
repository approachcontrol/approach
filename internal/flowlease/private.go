package flowlease

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const (
	TmuxSpawnCommand = "__flow-tmux-spawn"
	LeaseRunCommand  = "__flow-lease-run"

	protocolPollInterval = 10 * time.Millisecond
	tmuxCommandTimeout   = 2 * time.Second
	gracefulStopTimeout  = 2 * time.Second
)

var errTmuxCommandTimeout = errors.New("tmux command timed out with uncertain server state")

// PrivateSpec contains the identity and bounded protocol inputs shared by the
// parent-side tmux spawner and the lease-owning runner.
type PrivateSpec struct {
	SessionName      string
	WindowName       string
	CWD              string
	ScriptPath       string
	Root             string
	FlowID           string
	PhaseID          string
	LaunchID         string
	HandoffDir       string
	Nonce            string
	DecisionDeadline time.Time
	StartedDeadline  time.Time
	CleanupDeadline  time.Time
}

// NewHandoffPath returns the exact per-attempt directory path the private
// parent will create exclusively beneath the canonical artifact root.
func NewHandoffPath(root, launchID, suffix string) (string, error) {
	canonical, err := ResolveRoot(root)
	if err != nil {
		return "", err
	}
	if canonical != filepath.Clean(root) {
		return "", errors.New("Flow launch handoff root must already be canonical")
	}
	if !artifacts.IsSafeID(launchID) || !artifacts.IsSafeID(suffix) {
		return "", errors.New("Flow launch handoff requires safe launch and random IDs")
	}
	return filepath.Join(canonical, handoffCollection, launchID+"-"+suffix), nil
}

// SpawnArgv builds the exact private parent command argv.
func SpawnArgv(executable string, spec PrivateSpec) ([]string, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("private Flow tmux executable must be absolute")
	}
	if err := validatePrivateSpec(spec, true); err != nil {
		return nil, err
	}
	return append([]string{executable, TmuxSpawnCommand}, privateFlags(spec)...), nil
}

// LeaseRunArgv builds the exact private runner argv followed by the untouched
// original agent argv.
func LeaseRunArgv(executable string, spec PrivateSpec, agentArgv []string) ([]string, error) {
	if !filepath.IsAbs(executable) {
		return nil, errors.New("private Flow lease executable must be absolute")
	}
	if err := validatePrivateSpec(spec, false); err != nil {
		return nil, err
	}
	if len(agentArgv) == 0 || strings.TrimSpace(agentArgv[0]) == "" {
		return nil, errors.New("private Flow lease runner requires agent argv")
	}
	argv := append([]string{executable, LeaseRunCommand}, privateFlags(spec)...)
	argv = append(argv, "--")
	argv = append(argv, agentArgv...)
	return argv, nil
}

func privateFlags(spec PrivateSpec) []string {
	return []string{
		"--session", spec.SessionName,
		"--window", spec.WindowName,
		"--cwd", spec.CWD,
		"--script", spec.ScriptPath,
		"--root", spec.Root,
		"--flow", spec.FlowID,
		"--phase", spec.PhaseID,
		"--launch", spec.LaunchID,
		"--handoff", spec.HandoffDir,
		"--nonce", spec.Nonce,
		"--decision-deadline", spec.DecisionDeadline.UTC().Format(time.RFC3339Nano),
		"--started-deadline", spec.StartedDeadline.UTC().Format(time.RFC3339Nano),
		"--cleanup-deadline", spec.CleanupDeadline.UTC().Format(time.RFC3339Nano),
	}
}

func validatePrivateSpec(spec PrivateSpec, requireScript bool) error {
	if !validTmuxComponent(spec.SessionName) || !validTmuxComponent(spec.WindowName) {
		return errors.New("private Flow tmux session and window names must use the generated-name alphabet")
	}
	if !filepath.IsAbs(spec.CWD) {
		return errors.New("private Flow tmux cwd must be absolute")
	}
	if requireScript && !filepath.IsAbs(spec.ScriptPath) {
		return errors.New("private Flow tmux script path must be absolute")
	}
	canonical, err := ResolveRoot(spec.Root)
	if err != nil {
		return err
	}
	if canonical != filepath.Clean(spec.Root) {
		return errors.New("private Flow tmux root must already be canonical")
	}
	for label, value := range map[string]string{
		"Flow ID": spec.FlowID, "phase ID": spec.PhaseID,
		"launch ID": spec.LaunchID, "nonce": spec.Nonce,
	} {
		if !artifacts.IsSafeID(value) {
			return fmt.Errorf("invalid private Flow tmux %s %q", label, value)
		}
	}
	attempt := spec.attempt()
	if _, err := validateHandoffAttempt(attempt); err != nil {
		return err
	}
	if spec.DecisionDeadline.IsZero() || spec.StartedDeadline.IsZero() || spec.CleanupDeadline.IsZero() ||
		spec.StartedDeadline.Before(spec.DecisionDeadline) || spec.CleanupDeadline.Before(spec.StartedDeadline) {
		return errors.New("private Flow tmux deadlines are missing or out of order")
	}
	return nil
}

func validTmuxComponent(value string) bool {
	if value == "" || strings.ContainsAny(value, ":.;=$\x00\r\n") {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// ExactWindowTarget builds tmux's exact-component target and rejects every
// separator or marker that could re-enable prefix/target parsing.
func ExactWindowTarget(sessionName, windowName string) (string, error) {
	if !validTmuxComponent(sessionName) || !validTmuxComponent(windowName) {
		return "", errors.New("refusing unsafe exact tmux target component")
	}
	return "=" + sessionName + ":=" + windowName, nil
}

// CancelExact cancels only spec's full generated window and then proves the
// Flow lease is free. It is safe to call after the private parent already
// performed the same cleanup.
func CancelExact(spec PrivateSpec) error {
	if err := validatePrivateSpec(spec, false); err != nil {
		return err
	}
	return cancelExactAttempt(spec)
}

func (spec PrivateSpec) attempt() handoffAttempt {
	return handoffAttempt{
		Root: spec.Root, FlowID: spec.FlowID, PhaseID: spec.PhaseID,
		LaunchID: spec.LaunchID, Nonce: spec.Nonce, HandoffDir: spec.HandoffDir,
	}
}

// RunTmuxSpawn runs the private parent handshake. It returns only after a
// matching started record or terminal cleanup evidence.
func RunTmuxSpawn(args []string, stderr io.Writer) error {
	spec, rest, err := parsePrivateArgs(args, false)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New("private Flow tmux spawn rejects positional arguments")
	}
	if err := validatePrivateSpec(spec, true); err != nil {
		return err
	}
	return runTmuxSpawn(spec, stderr, tmuxSpawnOps{
		start: startTmuxWindow, cleanup: cleanupHandoff, cancel: cancelExactAttempt,
	})
}

type tmuxSpawnOps struct {
	start   func(PrivateSpec, io.Writer) error
	cleanup func(handoffAttempt) error
	cancel  func(PrivateSpec) error
}

func runTmuxSpawn(spec PrivateSpec, stderr io.Writer, ops tmuxSpawnOps) error {
	attempt := spec.attempt()
	if err := createHandoff(attempt); err != nil {
		return err
	}
	committed := false
	terminal := false
	defer func() {
		if terminal {
			_ = ops.cleanup(attempt)
		}
	}()

	if err := ops.start(spec, stderr); err != nil {
		terminal = true
		return abortAndCancel(spec, false, fmt.Errorf("start tmux window: %w", err), ops.cancel)
	}
	if _, err := waitForRecordOrFailure(attempt, recordReady, spec.DecisionDeadline); err != nil {
		terminal = true
		return abortAndCancel(spec, false, fmt.Errorf("wait for Flow lease readiness: %w", err), ops.cancel)
	}
	if !time.Now().Before(spec.DecisionDeadline) {
		terminal = true
		return abortAndCancel(spec, false, errors.New("Flow lease readiness arrived after the decision deadline"), ops.cancel)
	}
	outcome, err := publishOrReadDecision(attempt, decisionCommit)
	if err != nil {
		terminal = true
		return abortAndCancel(spec, false, fmt.Errorf("commit Flow lease handoff: %w", err), ops.cancel)
	}
	if outcome != decisionCommit {
		terminal = true
		return abortAndCancel(spec, false, errors.New("Flow lease handoff was aborted before commit"), ops.cancel)
	}
	committed = true
	if _, err := waitForRecordOrFailure(attempt, recordStarted, spec.StartedDeadline); err != nil {
		terminal = true
		return abortAndCancel(spec, committed, fmt.Errorf("uncertain committed Flow launch: %w", err), ops.cancel)
	}
	if _, err := waitForRecordOrFailure(attempt, recordStartedConfirmed, spec.StartedDeadline); err != nil {
		terminal = true
		return abortAndCancel(spec, committed, fmt.Errorf("uncertain committed Flow launch confirmation: %w", err), ops.cancel)
	}
	if !time.Now().Before(spec.StartedDeadline) {
		terminal = true
		return abortAndCancel(spec, committed, errors.New("uncertain committed Flow launch: started confirmation arrived after its deadline"), ops.cancel)
	}
	terminal = true
	if err := ops.cleanup(attempt); err != nil {
		return abortAndCancel(spec, committed, fmt.Errorf("clean confirmed Flow launch handoff: %w", err), ops.cancel)
	}
	return nil
}

// RunLeaseRunner owns the Flow lease while supervising the original agent.
func RunLeaseRunner(args []string, stdin io.Reader, stdout, stderr io.Writer) (retErr error) {
	spec, agentArgv, err := parsePrivateArgs(args, true)
	if err != nil {
		return err
	}
	if err := validatePrivateSpec(spec, false); err != nil {
		return err
	}
	if len(agentArgv) == 0 {
		return errors.New("private Flow lease runner requires argv after --")
	}
	attempt := spec.attempt()
	if err := validateExistingHandoff(attempt); err != nil {
		return err
	}
	for _, kind := range []string{recordReady, recordDecision, recordStarted, recordStartedConfirmed, recordFailure} {
		if err := requireHandoffRecordAbsent(attempt, kind); err != nil {
			return err
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	reportFailure := true
	defer func() {
		if retErr != nil && reportFailure {
			retErr = reportLeaseRunnerFailure(spec, attempt, retErr, signals)
		}
	}()
	if !time.Now().Before(spec.DecisionDeadline) {
		return errors.New("Flow lease runner decision deadline already expired")
	}
	lease, err := Acquire(spec.Root, spec.FlowID)
	if err != nil {
		if _, readyErr := readHandoffRecord(attempt, recordReady); readyErr == nil {
			reportFailure = false
		}
		return fmt.Errorf("acquire tracked Flow lease: %w", err)
	}
	defer lease.Close()
	if err := publishHandoffRecord(attempt, recordReady, ""); err != nil {
		if errors.Is(err, errRecordExists) {
			reportFailure = false
		}
		return fmt.Errorf("publish Flow lease readiness: %w", err)
	}
	decision, err := waitForRecord(attempt, recordDecision, spec.DecisionDeadline)
	if err != nil {
		reportFailure = false
		_ = cleanupHandoff(attempt)
		return fmt.Errorf("wait for Flow lease decision: %w", err)
	}
	if decision.Outcome != decisionCommit {
		reportFailure = false
		_ = cleanupHandoff(attempt)
		return errors.New("Flow lease runner received abort decision")
	}
	if !time.Now().Before(spec.DecisionDeadline) {
		return errors.New("Flow lease runner received commit after its decision deadline")
	}
	if !time.Now().Before(spec.StartedDeadline) {
		return errors.New("Flow lease runner start deadline expired")
	}
	select {
	case sig := <-signals:
		return fmt.Errorf("Flow lease runner canceled before agent start: %s", sig)
	default:
	}

	cmd := exec.Command(agentArgv[0], agentArgv[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = childProcessGroupAttributes(stdin)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tracked Flow agent: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	if err := publishAndReadStarted(attempt, nil); err != nil {
		cause := fmt.Errorf("confirm started tracked Flow agent: %w", err)
		if failureErr := publishHandoffFailure(attempt, cause.Error()); failureErr != nil {
			_ = terminateProcessGroup(cmd.Process.Pid, waitCh)
			return errors.Join(cause, fmt.Errorf("publish Flow lease runner failure: %w", failureErr))
		}
		reportFailure = false
		if cleanupErr := terminateProcessGroup(cmd.Process.Pid, waitCh); cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("terminate unconfirmed tracked Flow agent: %w", cleanupErr))
		}
		return awaitLeaseRunnerFailureConsumption(spec, attempt, cause, signals)
	}
	// publishHandoffRecord verifies the durable payload before its atomic final
	// link. Once the parent can observe started, no runner read remains for the
	// parent's successful cleanup to race.
	reportFailure = false
	return superviseProcessGroup(cmd.Process.Pid, waitCh, signals)
}

func publishAndReadStarted(attempt handoffAttempt, afterPublish func()) error {
	if err := publishHandoffRecord(attempt, recordStarted, ""); err != nil {
		return err
	}
	if afterPublish != nil {
		afterPublish()
	}
	if _, err := readHandoffRecord(attempt, recordStarted); err != nil {
		return fmt.Errorf("read back started tracked Flow agent: %w", err)
	}
	return publishHandoffRecord(attempt, recordStartedConfirmed, "")
}

func childProcessGroupAttributes(stdin io.Reader) *syscall.SysProcAttr {
	attrs := &syscall.SysProcAttr{Setpgid: true}
	if tty, ok := stdin.(*os.File); ok {
		attrs.Foreground = true
		attrs.Ctty = int(tty.Fd())
	}
	return attrs
}

func reportLeaseRunnerFailure(spec PrivateSpec, attempt handoffAttempt, cause error, signals <-chan os.Signal) error {
	if err := publishHandoffFailure(attempt, cause.Error()); err != nil {
		return errors.Join(cause, fmt.Errorf("publish Flow lease runner failure: %w", err))
	}
	return awaitLeaseRunnerFailureConsumption(spec, attempt, cause, signals)
}

func awaitLeaseRunnerFailureConsumption(spec PrivateSpec, attempt handoffAttempt, cause error, signals <-chan os.Signal) error {
	poll := time.NewTicker(protocolPollInterval)
	defer poll.Stop()
	for {
		if _, err := os.Lstat(attempt.HandoffDir); os.IsNotExist(err) {
			return cause
		}
		if !time.Now().Before(spec.CleanupDeadline) {
			if err := cleanupHandoff(attempt); err != nil {
				return errors.Join(cause, fmt.Errorf("clean expired Flow lease runner failure: %w", err))
			}
			return cause
		}
		select {
		case <-signals:
			return cause
		case <-poll.C:
		}
	}
}

func requireHandoffRecordAbsent(attempt handoffAttempt, kind string) error {
	path := filepath.Join(attempt.HandoffDir, kind)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("unexpected pre-existing Flow launch handoff %s", kind)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect pre-start Flow launch handoff %s: %w", kind, err)
	}
	return nil
}

// ProcessExitError carries the child-compatible exit status to main.
type ProcessExitError struct{ Code int }

func (e ProcessExitError) Error() string {
	return fmt.Sprintf("tracked Flow agent exited with status %d", e.Code)
}

func superviseProcessGroup(pid int, waitCh <-chan error, signals <-chan os.Signal) error {
	for {
		select {
		case err := <-waitCh:
			return stopProcessGroupAfterLeaderExit(pid, err)
		case sig := <-signals:
			sysSig, ok := sig.(syscall.Signal)
			if !ok {
				continue
			}
			return stopProcessGroup(pid, waitCh, sysSig)
		}
	}
}

func terminateProcessGroup(pid int, waitCh <-chan error) error {
	return stopProcessGroup(pid, waitCh, syscall.SIGTERM)
}

// stopProcessGroup retains the caller's lease until every member of the
// agent's process group is gone. Reaping the group leader is not sufficient:
// a descendant may ignore the graceful signal and keep using the worktree.
// After the grace period, keep the supervisor alive and the lease fail-closed
// while SIGKILL takes effect rather than returning on the leader's status.
func stopProcessGroup(pid int, waitCh <-chan error, gracefulSignal syscall.Signal) error {
	return stopProcessGroupWithLeader(pid, waitCh, gracefulSignal, false, nil)
}

func stopProcessGroupAfterLeaderExit(pid int, leaderErr error) error {
	return stopProcessGroupWithLeader(pid, nil, syscall.SIGTERM, true, leaderErr)
}

func stopProcessGroupWithLeader(
	pid int,
	waitCh <-chan error,
	gracefulSignal syscall.Signal,
	leaderExited bool,
	leaderErr error,
) error {
	_ = syscall.Kill(-pid, gracefulSignal)
	grace := time.NewTimer(gracefulStopTimeout)
	defer grace.Stop()
	poll := time.NewTicker(protocolPollInterval)
	defer poll.Stop()

	for {
		if leaderExited && !processGroupExists(pid) {
			return childExitError(leaderErr)
		}
		select {
		case err := <-waitCh:
			leaderErr = err
			leaderExited = true
		case <-grace.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-poll.C:
		}
	}
}

func processGroupExists(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func childExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return ProcessExitError{Code: exitErr.ExitCode()}
	}
	if status.Signaled() {
		return ProcessExitError{Code: 128 + int(status.Signal())}
	}
	return ProcessExitError{Code: status.ExitStatus()}
}

func waitForRecord(attempt handoffAttempt, kind string, deadline time.Time) (handoffRecord, error) {
	for {
		record, err := readHandoffRecord(attempt, kind)
		if err == nil {
			return record, nil
		}
		if !isMissingRecord(err) {
			return handoffRecord{}, err
		}
		if !time.Now().Before(deadline) {
			return handoffRecord{}, fmt.Errorf("timed out waiting for %s", kind)
		}
		time.Sleep(protocolPollInterval)
	}
}

func waitForRecordOrFailure(attempt handoffAttempt, kind string, deadline time.Time) (handoffRecord, error) {
	for {
		failure, failureErr := readHandoffRecord(attempt, recordFailure)
		if failureErr == nil {
			return handoffRecord{}, errors.New("Flow lease runner failed: " + failure.Detail)
		}
		if !isMissingRecord(failureErr) {
			return handoffRecord{}, failureErr
		}
		record, err := readHandoffRecord(attempt, kind)
		if err == nil {
			return record, nil
		}
		if !isMissingRecord(err) {
			return handoffRecord{}, err
		}
		if !time.Now().Before(deadline) {
			return handoffRecord{}, fmt.Errorf("timed out waiting for %s", kind)
		}
		time.Sleep(protocolPollInterval)
	}
}

func isMissingRecord(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT)
}

func publishOrReadDecision(attempt handoffAttempt, outcome string) (string, error) {
	if err := publishHandoffRecord(attempt, recordDecision, outcome); err == nil {
		return outcome, nil
	} else if !errors.Is(err, errRecordExists) {
		return "", err
	}
	record, err := readHandoffRecord(attempt, recordDecision)
	if err != nil {
		return "", err
	}
	return record.Outcome, nil
}

func abortAndCancel(spec PrivateSpec, committed bool, cause error, cancel func(PrivateSpec) error) error {
	attempt := spec.attempt()
	outcome, decisionErr := publishOrReadDecision(attempt, decisionAbort)
	if decisionErr != nil && !committed {
		cause = errors.Join(cause, fmt.Errorf("publish abort decision: %w", decisionErr))
	}
	if outcome == decisionCommit {
		committed = true
	}
	cancelErr := cancel(spec)
	if cancelErr != nil {
		cause = errors.Join(cause, cancelErr)
	}
	if committed {
		return fmt.Errorf("uncertain committed Flow launch cleanup: %w", cause)
	}
	return cause
}

func cancelExactAttempt(spec PrivateSpec) error {
	return cancelExactAttemptWith(spec, killExactWindow, Inspect)
}

func cancelExactAttemptWith(
	spec PrivateSpec,
	kill func(sessionName, windowName string) error,
	inspect func(root, flowID string) (LeaseState, error),
) error {
	killErr := kill(spec.SessionName, spec.WindowName)
	for {
		state, inspectErr := inspect(spec.Root, spec.FlowID)
		if inspectErr != nil {
			verifyErr := fmt.Errorf("verify Flow lease release after cancellation: %w", inspectErr)
			if killErr != nil {
				return errors.Join(fmt.Errorf("cancel exact tmux window: %w", killErr), verifyErr)
			}
			return verifyErr
		}
		if state == Free {
			if killErr != nil && !tmuxTargetAbsent(killErr) {
				return fmt.Errorf("cancel exact tmux window (Flow lease release verified): %w", killErr)
			}
			return nil
		}
		if !time.Now().Before(spec.CleanupDeadline) {
			releaseErr := errors.New("Flow lease remains held after exact tmux cancellation")
			if killErr != nil {
				return errors.Join(fmt.Errorf("cancel exact tmux window: %w", killErr), releaseErr)
			}
			return releaseErr
		}
		time.Sleep(protocolPollInterval)
	}
}

func killExactWindow(sessionName, windowName string) error {
	target, err := ExactWindowTarget(sessionName, windowName)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "kill-window", "-t", target)
	cmd.Env = envWithoutMultiplexers(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func tmuxTargetAbsent(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "can't find") || strings.Contains(text, "no server running") || strings.Contains(text, "not found")
}

func startTmuxWindow(spec PrivateSpec, stderr io.Writer) error {
	if err := inspectLaunchScript(spec.ScriptPath); err != nil {
		return err
	}
	run := func(args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxCommandTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tmux", args...)
		cmd.Env = envWithoutMultiplexers(os.Environ())
		cmd.Stderr = stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			return fmt.Errorf("%w: tmux %s", errTmuxCommandTimeout, args[0])
		}
		return err
	}
	return runTmuxWindowCreate(spec, run)
}

func runTmuxWindowCreate(spec PrivateSpec, run func(...string) error) error {
	hasSessionErr := run("has-session", "-t", "="+spec.SessionName)
	if hasSessionErr == nil {
		if err := run("new-window", "-d", "-t", "="+spec.SessionName+":", "-n", spec.WindowName, "-c", spec.CWD,
			"exec sh "+shellQuote(spec.ScriptPath)); err == nil {
			return nil
		} else if errors.Is(err, errTmuxCommandTimeout) {
			return err
		}
	} else if errors.Is(hasSessionErr, errTmuxCommandTimeout) {
		return hasSessionErr
	}
	command := "exec sh " + shellQuote(spec.ScriptPath)
	if err := run("new-session", "-d", "-s", spec.SessionName, "-n", spec.WindowName, "-c", spec.CWD, command); err == nil {
		return nil
	} else if errors.Is(err, errTmuxCommandTimeout) {
		return err
	}
	return run("new-window", "-d", "-t", "="+spec.SessionName+":", "-n", spec.WindowName, "-c", spec.CWD, command)
}

func inspectLaunchScript(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("Flow tmux launch script must be absolute")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open Flow tmux launch script: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect Flow tmux launch script: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("Flow tmux launch script must be an owner-readable regular file")
	}
	return nil
}

func parsePrivateArgs(args []string, wantAgent bool) (PrivateSpec, []string, error) {
	values := make(map[string]string)
	rest := []string(nil)
	for i := 0; i < len(args); {
		if args[i] == "--" {
			if !wantAgent {
				return PrivateSpec{}, nil, errors.New("private Flow tmux spawn rejects --")
			}
			rest = append([]string(nil), args[i+1:]...)
			i = len(args)
			break
		}
		name := strings.TrimPrefix(args[i], "--")
		if name == args[i] || i+1 >= len(args) {
			return PrivateSpec{}, nil, fmt.Errorf("invalid private Flow tmux argument %q", args[i])
		}
		if _, exists := values[name]; exists {
			return PrivateSpec{}, nil, fmt.Errorf("duplicate private Flow tmux argument --%s", name)
		}
		values[name] = args[i+1]
		i += 2
	}
	want := []string{"session", "window", "cwd", "script", "root", "flow", "phase", "launch", "handoff", "nonce", "decision-deadline", "started-deadline", "cleanup-deadline"}
	if len(values) != len(want) {
		return PrivateSpec{}, nil, errors.New("private Flow tmux arguments are missing or unexpected")
	}
	for _, name := range want {
		if _, ok := values[name]; !ok {
			return PrivateSpec{}, nil, fmt.Errorf("missing private Flow tmux argument --%s", name)
		}
	}
	parseTime := func(name string) (time.Time, error) {
		value, err := time.Parse(time.RFC3339Nano, values[name])
		if err != nil {
			return time.Time{}, fmt.Errorf("parse --%s: %w", name, err)
		}
		return value, nil
	}
	decision, err := parseTime("decision-deadline")
	if err != nil {
		return PrivateSpec{}, nil, err
	}
	started, err := parseTime("started-deadline")
	if err != nil {
		return PrivateSpec{}, nil, err
	}
	cleanup, err := parseTime("cleanup-deadline")
	if err != nil {
		return PrivateSpec{}, nil, err
	}
	return PrivateSpec{
		SessionName: values["session"], WindowName: values["window"], CWD: values["cwd"],
		ScriptPath: values["script"], Root: values["root"], FlowID: values["flow"],
		PhaseID: values["phase"], LaunchID: values["launch"], HandoffDir: values["handoff"],
		Nonce: values["nonce"], DecisionDeadline: decision, StartedDeadline: started, CleanupDeadline: cleanup,
	}, rest, nil
}

func envWithoutMultiplexers(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" && key != "ZELLIJ" {
			out = append(out, entry)
		}
	}
	return out
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
