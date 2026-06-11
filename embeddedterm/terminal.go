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
	"github.com/creack/pty"
	bubbleemulator "github.com/taigrr/bubbleterm/emulator"
)

type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateExited     State = "exited"
	StateFailed     State = "failed"
	StateTerminated State = "terminated"
)

type ViewportMode string

const ViewportLive ViewportMode = "live"

type Viewport struct {
	Mode         ViewportMode
	ScrollOffset int
}

type StartRequest struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Width   int
	Height  int
}

const (
	finalOutputDrainTimeout = 200 * time.Millisecond
	terminateWaitTimeout    = 2 * time.Second
)

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
	outputReader := newTerminalOutputReader(ptmx)
	emu, err := bubbleemulator.NewFromPipes(width, height, outputReader, ptmx)
	if err != nil {
		_ = ptmx.Close()
		return nil, err
	}
	t := &Terminal{
		cmd:          cmd,
		pty:          ptmx,
		emulator:     emu,
		outputReader: outputReader,
		state:        StateRunning,
		done:         make(chan struct{}),
	}
	go t.waitLoop()
	return t, nil
}

type Terminal struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	pty          *os.File
	emulator     *bubbleemulator.Emulator
	outputReader *terminalOutputReader
	state        State
	err          error
	terminating  bool
	done         chan struct{}
}

func (t *Terminal) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Terminal) VisibleLines(width, height int, viewport Viewport) []string {
	width, height = normalizeSize(width, height)
	t.mu.Lock()
	emu := t.emulator
	t.mu.Unlock()
	if emu == nil {
		return make([]string, height)
	}
	lines := emu.GetScreen().Rows
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := make([]string, height)
	start := height - len(lines)
	for i, line := range lines {
		out[start+i] = fitTerminalLine(line, width)
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
	if state == StateExited || state == StateFailed || state == StateTerminated {
		t.mu.Unlock()
		return nil
	}
	emu := t.emulator
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
		if err := emu.Resize(width, height); err != nil && !isClosedPTYError(err) {
			return err
		}
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
		_ = ptmx.Close()
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
	t.mu.Lock()
	ptmx := t.pty
	emu := t.emulator
	outputReader := t.outputReader
	t.pty = nil
	t.emulator = nil
	t.mu.Unlock()
	if emu != nil {
		_ = emu.Close()
	}
	if ptmx == nil {
		return nil
	}
	if err := ptmx.Close(); err != nil && !isClosedPTYError(err) {
		return err
	}
	if outputReader != nil {
		outputReader.close()
	}
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
	time.Sleep(finalOutputDrainTimeout)
	_ = t.closePTY()
	t.waitForOutputReader()
	t.mu.Lock()
	t.err = err
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

func (t *Terminal) waitForOutputReader() {
	t.mu.Lock()
	outputReader := t.outputReader
	t.mu.Unlock()
	if outputReader == nil {
		return
	}
	select {
	case <-outputReader.done:
	case <-time.After(finalOutputDrainTimeout):
	}
}

type terminalOutputReader struct {
	reader io.Reader
	done   chan struct{}
	once   sync.Once
}

func newTerminalOutputReader(reader io.Reader) *terminalOutputReader {
	return &terminalOutputReader{
		reader: reader,
		done:   make(chan struct{}),
	}
}

func (r *terminalOutputReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil {
		r.close()
	}
	return n, err
}

func (r *terminalOutputReader) close() {
	r.once.Do(func() {
		close(r.done)
	})
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
