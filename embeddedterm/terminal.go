package embeddedterm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	defaultScrollbackLines  = 5000
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
	configureProcessGroup(cmd)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, err
	}
	t := &Terminal{
		cmd:      cmd,
		pty:      ptmx,
		screen:   newScreenBuffer(width, height, defaultScrollbackLines),
		state:    StateRunning,
		done:     make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go t.readLoop()
	go t.waitLoop()
	return t, nil
}

type Terminal struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	pty         *os.File
	screen      *screenBuffer
	state       State
	err         error
	terminating bool
	done        chan struct{}
	readDone    chan struct{}
}

func (t *Terminal) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Terminal) VisibleLines(width, height int, viewport Viewport) []string {
	width, height = normalizeSize(width, height)
	t.mu.Lock()
	lines := t.screen.VisibleLines(width, height)
	t.mu.Unlock()
	out := make([]string, height)
	copy(out[height-len(lines):], lines)
	return out
}

func (t *Terminal) Write(p []byte) (int, error) {
	return t.pty.Write(p)
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
	if t.screen != nil {
		t.screen.Resize(width, height)
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
	if ptmx != nil {
		_ = ptmx.Close()
	}
	if err := terminateProcessGroup(t.cmd); err != nil {
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
	if t.pty == nil {
		return nil
	}
	return t.pty.Close()
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

func (t *Terminal) readLoop() {
	defer close(t.readDone)
	var tmp [4096]byte
	for {
		n, err := t.pty.Read(tmp[:])
		if n > 0 {
			t.mu.Lock()
			t.screen.Write(tmp[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) waitLoop() {
	err := t.cmd.Wait()
	select {
	case <-t.readDone:
	case <-time.After(finalOutputDrainTimeout):
		_ = t.Close()
		<-t.readDone
	}
	_ = t.Close()
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
