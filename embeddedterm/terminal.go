package embeddedterm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

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

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
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
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, err
	}
	t := &Terminal{
		cmd:   cmd,
		pty:   ptmx,
		state: StateRunning,
		done:  make(chan error, 1),
	}
	go t.readLoop()
	go t.waitLoop()
	return t, nil
}

type Terminal struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	pty         *os.File
	buf         bytes.Buffer
	state       State
	err         error
	terminating bool
	done        chan error
}

func (t *Terminal) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Terminal) VisibleLines(width, height int, viewport Viewport) []string {
	width, height = normalizeSize(width, height)
	t.mu.Lock()
	text := t.buf.String()
	t.mu.Unlock()
	lines := screenLines(text, width)
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := make([]string, height)
	copy(out[height-len(lines):], lines)
	return out
}

func (t *Terminal) Write(p []byte) (int, error) {
	return t.pty.Write(p)
}

func (t *Terminal) Resize(width, height int) error {
	width, height = normalizeSize(width, height)
	return pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
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
	t.mu.Unlock()
	if err := t.cmd.Process.Kill(); err != nil {
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
	case err := <-t.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Terminal) readLoop() {
	var tmp [4096]byte
	for {
		n, err := t.pty.Read(tmp[:])
		if n > 0 {
			t.mu.Lock()
			_, _ = t.buf.Write(tmp[:n])
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *Terminal) waitLoop() {
	err := t.cmd.Wait()
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
	t.done <- err
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

func screenLines(text string, width int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "")
	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			lines = append(lines, "")
			continue
		}
		for len(line) > width {
			lines = append(lines, line[:width])
			line = line[width:]
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
