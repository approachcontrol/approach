package embeddedterm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateExited     State = "exited"
	StateFailed     State = "failed"
	StateTerminated State = "terminated"
)

type StartRequest struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Width   int
	Height  int
}

// OutputTransform rewrites child output before it reaches the terminal
// emulator. p is a chunk of (query-filtered) child output and final is true on
// the read loop's last call (child EOF). Implementations may buffer partial
// input across calls and must flush their remainder when final is true.
type OutputTransform interface {
	Transform(p []byte, final bool) []byte
}

// StartOptions carries optional behavior for StartCommandWithOptions.
type StartOptions struct {
	// Transform, when non-nil, post-processes child output (e.g. translating
	// claude stream-json into readable lines) before it is rendered.
	Transform OutputTransform
}

const (
	defaultScrollbackLines  = 5000
	finalOutputDrainTimeout = 200 * time.Millisecond
	terminateWaitTimeout    = 2 * time.Second
	maxPendingSequenceBytes = 16
)

var newEmulator = func(width, height int) (*vt.SafeEmulator, error) {
	return vt.NewSafeEmulator(width, height), nil
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func IsUnsupported(err error) bool {
	return errors.Is(err, pty.ErrUnsupported)
}

func (m *Manager) Start(ctx context.Context, req StartRequest) (*Terminal, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, fmt.Errorf("embedded terminal command is required")
	}
	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	return m.StartCommand(ctx, cmd, req.Width, req.Height)
}

func (m *Manager) StartCommand(ctx context.Context, cmd *exec.Cmd, width, height int) (*Terminal, error) {
	return m.StartCommandWithOptions(ctx, cmd, width, height, StartOptions{})
}

// StartCommandWithOptions is StartCommand with optional behavior such as an
// output transform applied to child output before rendering.
func (m *Manager) StartCommandWithOptions(ctx context.Context, cmd *exec.Cmd, width, height int, opts StartOptions) (*Terminal, error) {
	if cmd == nil {
		return nil, fmt.Errorf("embedded terminal command is required")
	}
	width, height = normalizeSize(width, height)
	ensureTerminalEnv(cmd)
	configureProcessGroup(cmd)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, err
	}
	emu, err := newEmulator(width, height)
	if err != nil {
		_ = ptmx.Close()
		_ = terminateProcessGroup(cmd)
		_ = waitForCommandExit(cmd, terminateWaitTimeout)
		return nil, err
	}
	emu.SetScrollbackSize(defaultScrollbackLines)
	t := &Terminal{
		cmd:       cmd,
		pty:       ptmx,
		emulator:  emu,
		transform: opts.Transform,
		state:     StateRunning,
		done:      make(chan struct{}),
		readDone:  make(chan struct{}),
		drainDone: make(chan struct{}),
	}
	go t.readLoop(ptmx, emu)
	go t.drainResponses(ptmx, emu)
	go t.waitLoop()
	return t, nil
}

type Terminal struct {
	mu          sync.Mutex
	emuMu       sync.Mutex
	cmd         *exec.Cmd
	pty         *os.File
	emulator    *vt.SafeEmulator
	transform   OutputTransform
	state       State
	err         error
	terminating bool
	done        chan struct{}
	readDone    chan struct{}
	drainDone   chan struct{}
	finalRows   []string
}

func (t *Terminal) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Terminal) VisibleLines(width, height int) []string {
	width, height = normalizeSize(width, height)
	t.mu.Lock()
	emu := t.emulator
	state := t.state
	finalRows := append([]string(nil), t.finalRows...)
	t.mu.Unlock()

	var lines []string
	if state == StateExited || state == StateFailed || state == StateTerminated {
		lines = finalRows
	} else if emu != nil {
		t.emuMu.Lock()
		lines = splitTerminalRows(emu.Render())
		t.emuMu.Unlock()
	}
	if len(lines) == 0 {
		return blankTerminalLines(width, height)
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := blankTerminalLines(width, height)
	start := height - len(lines)
	for i, line := range lines {
		out[start+i] = fitTerminalLine(line, width)
	}
	return out
}

func blankTerminalLines(width, height int) []string {
	out := make([]string, height)
	for i := range out {
		out[i] = fitTerminalLine("", width)
	}
	return out
}

func (t *Terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	ptmx := t.pty
	t.mu.Unlock()
	if ptmx == nil {
		return 0, os.ErrClosed
	}
	n, err := ptmx.Write(p)
	if err != nil && isClosedPTYError(err) {
		return n, os.ErrClosed
	}
	return n, err
}

func (t *Terminal) Resize(width, height int) error {
	width, height = normalizeSize(width, height)
	t.mu.Lock()
	state := t.state
	ptmx := t.pty
	emu := t.emulator
	if state == StateExited || state == StateFailed || state == StateTerminated {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	if ptmx == nil {
		return nil
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)}); err != nil {
		if isClosedPTYError(err) {
			return nil
		}
		return err
	}
	if emu != nil {
		t.emuMu.Lock()
		emu.Resize(width, height)
		t.emuMu.Unlock()
	}
	return nil
}

func (t *Terminal) Terminate() error {
	t.mu.Lock()
	state := t.state
	t.mu.Unlock()
	if state == StateExited || state == StateFailed || state == StateTerminated {
		return nil
	}
	if t.cmd.Process == nil {
		return nil
	}
	t.mu.Lock()
	t.terminating = true
	ptmx := t.pty
	t.mu.Unlock()
	err := terminateProcessGroup(t.cmd)
	if ptmx != nil {
		_ = t.closePTY()
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminateWaitTimeout)
	defer cancel()
	if err := t.Wait(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	return nil
}

func (t *Terminal) Close() error {
	if err := t.closePTY(); err != nil {
		return err
	}
	t.unblockEmulatorWrites()
	t.waitForReadDone()
	t.saveFinalRows(t.snapshotRows())
	t.shutdownEmulator()
	t.waitForTerminalIO()
	return nil
}

func (t *Terminal) closePTY() error {
	t.mu.Lock()
	ptmx := t.pty
	t.pty = nil
	t.mu.Unlock()
	if ptmx == nil {
		return nil
	}
	if err := ptmx.Close(); err != nil && !isClosedPTYError(err) {
		return err
	}
	return nil
}

func (t *Terminal) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		t.mu.Lock()
		err := t.err
		t.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Terminal) waitLoop() {
	err := t.cmd.Wait()
	if !t.waitForReadDone() {
		_ = t.closePTY()
		t.unblockEmulatorWrites()
		<-t.readDone
	}
	finalRows := t.snapshotRows()
	_ = t.closePTY()
	t.shutdownEmulator()
	t.waitForTerminalIO()
	t.mu.Lock()
	t.err = err
	if len(finalRows) > 0 || len(t.finalRows) == 0 {
		t.finalRows = finalRows
	}
	switch {
	case err == nil:
		t.state = StateExited
	case t.terminating:
		t.state = StateTerminated
	default:
		t.state = StateFailed
	}
	t.mu.Unlock()
	close(t.done)
}

func (t *Terminal) readLoop(ptmx *os.File, emu *vt.SafeEmulator) {
	defer close(t.readDone)
	filter := terminalQueryFilter{writer: ptmx}
	buf := make([]byte, 4096)
	for {
		n, err := ptmx.Read(buf)
		// Run the final read (n==0 with EOF) through the pipeline too so a
		// buffering transform can flush its trailing partial line.
		if n > 0 || err != nil {
			final := err != nil
			filtered := filter.Filter(buf[:n], final)
			if t.transform != nil {
				filtered = t.transform.Transform(filtered, final)
			}
			if len(filtered) > 0 {
				t.emuMu.Lock()
				_, writeErr := emu.Write(filtered)
				t.emuMu.Unlock()
				if writeErr != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) drainResponses(ptmx *os.File, emu *vt.SafeEmulator) {
	defer close(t.drainDone)
	_, _ = io.Copy(ptmx, emu)
}

func closeEmulatorResponses(emu *vt.SafeEmulator) {
	type pipeCloser interface {
		CloseWithError(error) error
	}
	if closer, ok := emu.InputPipe().(pipeCloser); ok {
		_ = closer.CloseWithError(io.EOF)
	}
}

func (t *Terminal) unblockEmulatorWrites() {
	t.mu.Lock()
	emu := t.emulator
	t.mu.Unlock()
	if emu != nil {
		closeEmulatorResponses(emu)
	}
}

func (t *Terminal) shutdownEmulator() {
	t.mu.Lock()
	emu := t.emulator
	t.emulator = nil
	t.mu.Unlock()
	if emu == nil {
		return
	}
	closeEmulatorResponses(emu)
}

func (t *Terminal) snapshotRows() []string {
	t.mu.Lock()
	emu := t.emulator
	t.mu.Unlock()
	if emu == nil {
		return nil
	}
	t.emuMu.Lock()
	defer t.emuMu.Unlock()
	rows := make([]string, 0, emu.ScrollbackLen()+emu.Height())
	if scrollback := emu.Scrollback(); scrollback != nil {
		for _, line := range scrollback.Lines() {
			rows = append(rows, line.Render())
		}
	}
	rows = append(rows, splitTerminalRows(emu.Render())...)
	return trimBlankTerminalRows(rows)
}

func (t *Terminal) saveFinalRows(rows []string) {
	if len(rows) == 0 {
		return
	}
	t.mu.Lock()
	t.finalRows = rows
	t.mu.Unlock()
}

func ensureTerminalEnv(cmd *exec.Cmd) {
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	for i, env := range cmd.Env {
		if strings.HasPrefix(env, "TERM=") {
			if env == "TERM=" || env == "TERM=dumb" {
				cmd.Env[i] = "TERM=xterm-256color"
			}
			return
		}
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
}

func fitTerminalLine(line string, width int) string {
	line = ansi.Truncate(line, width, "")
	if padding := width - ansi.StringWidth(line); padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	return line
}

func splitTerminalRows(rendered string) []string {
	rows := strings.Split(rendered, "\n")
	if len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func trimBlankTerminalRows(rows []string) []string {
	for len(rows) > 0 && strings.TrimSpace(ansi.Strip(rows[len(rows)-1])) == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

func (t *Terminal) waitForTerminalIO() {
	t.waitForReadDone()
	t.waitForDrainDone()
}

func (t *Terminal) waitForReadDone() bool {
	select {
	case <-t.readDone:
		return true
	case <-time.After(finalOutputDrainTimeout):
		return false
	}
}

func (t *Terminal) waitForDrainDone() bool {
	select {
	case <-t.drainDone:
		return true
	case <-time.After(finalOutputDrainTimeout):
		return false
	}
}

func waitForCommandExit(cmd *exec.Cmd, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

type terminalQueryFilter struct {
	writer  io.Writer
	pending []byte
}

func (f *terminalQueryFilter) Filter(p []byte, final bool) []byte {
	queries := []struct {
		sequence string
		response string
	}{
		{sequence: "\x1b[?u", response: "\x1b[?0u"},
		// This vt version answers DSR-5 with DEC private syntax; approach needs
		// the ANSI status form so children do not block waiting for ESC[0n.
		{sequence: "\x1b[5n", response: "\x1b[0n"},
	}
	input := append(append([]byte(nil), f.pending...), p...)
	f.pending = nil
	var out []byte
	for i := 0; i < len(input); {
		if input[i] != 0x1b {
			out = append(out, input[i])
			i++
			continue
		}
		remaining := input[i:]
		if query, ok := completeTerminalFilterQuery(remaining, queries); ok {
			_, _ = io.WriteString(f.writer, query.response)
			i += len(query.sequence)
			continue
		}
		if !final && partialTerminalFilterQuery(remaining, queries) {
			f.pending = append(f.pending[:0], remaining...)
			break
		}
		if len(f.pending) > maxPendingSequenceBytes {
			out = append(out, f.pending...)
			f.pending = nil
		}
		out = append(out, input[i])
		i++
	}
	if len(f.pending) > maxPendingSequenceBytes {
		out = append(out, f.pending...)
		f.pending = nil
	}
	return out
}

func completeTerminalFilterQuery(input []byte, queries []struct {
	sequence string
	response string
}) (struct {
	sequence string
	response string
}, bool) {
	for _, query := range queries {
		if len(input) >= len(query.sequence) && string(input[:len(query.sequence)]) == query.sequence {
			return query, true
		}
	}
	return struct {
		sequence string
		response string
	}{}, false
}

func partialTerminalFilterQuery(input []byte, queries []struct {
	sequence string
	response string
}) bool {
	for _, query := range queries {
		if len(input) < len(query.sequence) && strings.HasPrefix(query.sequence, string(input)) {
			return true
		}
	}
	return false
}

func normalizeSize(width, height int) (int, int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

func isClosedPTYError(err error) bool {
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "file already closed") || strings.Contains(text, "bad file descriptor")
}
