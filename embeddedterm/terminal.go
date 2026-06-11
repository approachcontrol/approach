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
	defaultScrollbackLines  = 5000
	finalOutputDrainTimeout = 200 * time.Millisecond
	terminateWaitTimeout    = 2 * time.Second
)

var newEmulatorFromPipes = bubbleemulator.NewFromPipes

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
	outputReader := newTerminalOutputReader(ptmx, ptmx, width, defaultScrollbackLines)
	emu, err := newEmulatorFromPipes(width, height, outputReader, ptmx)
	if err != nil {
		_ = ptmx.Close()
		outputReader.close()
		_ = terminateProcessGroup(cmd)
		_ = waitForCommandExit(cmd, terminateWaitTimeout)
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
	state := t.state
	outputReader := t.outputReader
	t.mu.Unlock()
	if emu == nil {
		return make([]string, height)
	}
	lines := emu.GetScreen().Rows
	if state == StateExited || state == StateFailed || state == StateTerminated {
		if outputReader != nil {
			if retained := outputReader.retainedRows(); len(retained) > len(lines) {
				lines = retained
			}
		}
	}
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

type terminalOutputReader struct {
	reader        io.Reader
	writer        io.Writer
	done          chan struct{}
	once          sync.Once
	retained      *vt.SafeEmulator
	filterPending []byte
	outputPending []byte
	mu            sync.Mutex
}

func newTerminalOutputReader(reader io.Reader, writer io.Writer, width, maxLines int) *terminalOutputReader {
	width, maxLines = normalizeSize(width, maxLines)
	r := &terminalOutputReader{
		reader:   reader,
		writer:   writer,
		done:     make(chan struct{}),
		retained: vt.NewSafeEmulator(width, maxLines),
	}
	return r
}

func (r *terminalOutputReader) Read(p []byte) (int, error) {
	for {
		if n := r.copyOutputPending(p); n > 0 {
			return n, nil
		}
		n, err := r.reader.Read(p)
		if n > 0 {
			filtered, queries := r.filterForBubbleterm(p[:n], err != nil)
			if len(filtered) > 0 {
				r.recordRetained(filtered)
			}
			r.writeResponses(r.terminalResponses(queries))
			if len(filtered) > 0 {
				return r.copyFilteredOutput(p, filtered), nil
			}
		}
		if err != nil {
			r.close()
			return 0, err
		}
	}
}

func (r *terminalOutputReader) copyOutputPending(p []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.outputPending) == 0 {
		return 0
	}
	n := copy(p, r.outputPending)
	r.outputPending = append(r.outputPending[:0], r.outputPending[n:]...)
	return n
}

func (r *terminalOutputReader) copyFilteredOutput(p, filtered []byte) int {
	n := copy(p, filtered)
	if n < len(filtered) {
		r.mu.Lock()
		r.outputPending = append(r.outputPending[:0], filtered[n:]...)
		r.mu.Unlock()
	}
	return n
}

func (r *terminalOutputReader) recordRetained(p []byte) {
	if r.retained != nil {
		_, _ = r.retained.Write(p)
	}
}

func (r *terminalOutputReader) filterForBubbleterm(p []byte, final bool) ([]byte, []terminalResponseRequest) {
	r.mu.Lock()
	input := append(r.filterPending, p...)
	r.filterPending = nil
	r.mu.Unlock()
	filtered, pending, queries := stripTerminalResponseRequests(input, final)
	if len(pending) > 0 {
		r.mu.Lock()
		r.filterPending = append(r.filterPending[:0], pending...)
		r.mu.Unlock()
	}
	return filtered, queries
}

func (r *terminalOutputReader) terminalResponses(queries []terminalResponseRequest) []byte {
	if len(queries) == 0 {
		return nil
	}
	var responses strings.Builder
	for _, query := range queries {
		switch query.kind {
		case terminalResponseCPR:
			x, y := 0, 0
			if r.retained != nil {
				pos := r.retained.CursorPosition()
				x, y = pos.X, pos.Y
			}
			responses.WriteString(ansi.CursorPositionReport(y+1, x+1))
		case terminalResponseStatus:
			responses.WriteString(ansi.DeviceStatusReport(ansi.DECStatusReport(0)))
		case terminalResponsePrimaryDA:
			responses.WriteString(ansi.PrimaryDeviceAttributes(62, 1, 6, 22))
		case terminalResponseSecondaryDA:
			responses.WriteString(ansi.SecondaryDeviceAttributes(1, 10, 0))
		case terminalResponseKittyKeyboard:
			responses.WriteString("\x1b[?0u")
		case terminalResponseForeground:
			responses.WriteString(ansi.SetForegroundColor(ansi.XRGBColor{Color: ansi.White}.String()))
		case terminalResponseBackground:
			responses.WriteString(ansi.SetBackgroundColor(ansi.XRGBColor{Color: ansi.Black}.String()))
		case terminalResponseCursorColor:
			responses.WriteString(ansi.SetCursorColor(ansi.XRGBColor{Color: ansi.White}.String()))
		}
	}
	return []byte(responses.String())
}

func (r *terminalOutputReader) writeResponses(responses []byte) {
	if len(responses) == 0 || r.writer == nil {
		return
	}
	_, _ = r.writer.Write(responses)
}

func (r *terminalOutputReader) retainedRows() []string {
	if r.retained == nil {
		return nil
	}
	return trimBlankTerminalRows(splitTerminalRows(r.retained.Render()))
}

func (r *terminalOutputReader) close() {
	r.once.Do(func() {
		if r.retained != nil {
			_ = r.retained.Close()
		}
		close(r.done)
	})
}

type terminalResponseKind int

const (
	terminalResponseCPR terminalResponseKind = iota
	terminalResponseStatus
	terminalResponsePrimaryDA
	terminalResponseSecondaryDA
	terminalResponseKittyKeyboard
	terminalResponseForeground
	terminalResponseBackground
	terminalResponseCursorColor
)

type terminalResponseRequest struct {
	kind terminalResponseKind
}

func stripTerminalResponseRequests(input []byte, final bool) ([]byte, []byte, []terminalResponseRequest) {
	var out []byte
	var queries []terminalResponseRequest
	for i := 0; i < len(input); {
		if input[i] != 0x1b {
			out = append(out, input[i])
			i++
			continue
		}
		if i+1 >= len(input) {
			if final {
				out = append(out, input[i])
				i++
				continue
			}
			return out, append([]byte(nil), input[i:]...), queries
		}
		switch input[i+1] {
		case '[':
			end := findCSIEnd(input, i+2)
			if end == -1 {
				if final {
					out = append(out, input[i:]...)
					return out, nil, queries
				}
				return out, append([]byte(nil), input[i:]...), queries
			}
			seq := input[i : end+1]
			if query, ok := terminalResponseCSI(seq); ok {
				queries = append(queries, query)
			} else {
				out = append(out, seq...)
			}
			i = end + 1
		case ']':
			end := findOSCEnd(input, i+2)
			if end == -1 {
				if final {
					out = append(out, input[i:]...)
					return out, nil, queries
				}
				return out, append([]byte(nil), input[i:]...), queries
			}
			seq := input[i:end]
			if query, ok := terminalResponseOSC(seq); ok {
				queries = append(queries, query)
			} else {
				out = append(out, input[i:end]...)
			}
			i = end
		default:
			out = append(out, input[i], input[i+1])
			i += 2
		}
	}
	return out, nil, queries
}

func findCSIEnd(input []byte, start int) int {
	for i := start; i < len(input); i++ {
		if input[i] >= 0x40 && input[i] <= 0x7e {
			return i
		}
	}
	return -1
}

func terminalResponseCSI(seq []byte) (terminalResponseRequest, bool) {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return terminalResponseRequest{}, false
	}
	body := string(seq[2:])
	final := body[len(body)-1]
	params := body[:len(body)-1]
	switch final {
	case 'c':
		switch params {
		case "", "0":
			return terminalResponseRequest{kind: terminalResponsePrimaryDA}, true
		case ">":
			return terminalResponseRequest{kind: terminalResponseSecondaryDA}, true
		default:
			return terminalResponseRequest{}, false
		}
	case 'n':
		switch params {
		case "5":
			return terminalResponseRequest{kind: terminalResponseStatus}, true
		case "6", "?6":
			return terminalResponseRequest{kind: terminalResponseCPR}, true
		default:
			return terminalResponseRequest{}, false
		}
	case 'p':
		return terminalResponseRequest{}, strings.HasSuffix(params, "$")
	case 'u':
		if params == "?" {
			return terminalResponseRequest{kind: terminalResponseKittyKeyboard}, true
		}
		return terminalResponseRequest{}, false
	default:
		return terminalResponseRequest{}, false
	}
}

func findOSCEnd(input []byte, start int) int {
	for i := start; i < len(input); i++ {
		switch input[i] {
		case 0x07:
			return i + 1
		case 0x1b:
			if i+1 < len(input) && input[i+1] == '\\' {
				return i + 2
			}
		}
	}
	return -1
}

func terminalResponseOSC(seq []byte) (terminalResponseRequest, bool) {
	if len(seq) < 5 || seq[0] != 0x1b || seq[1] != ']' {
		return terminalResponseRequest{}, false
	}
	body := string(seq[2:])
	body = strings.TrimSuffix(strings.TrimSuffix(body, "\x1b\\"), "\x07")
	switch body {
	case "10;?":
		return terminalResponseRequest{kind: terminalResponseForeground}, true
	case "11;?":
		return terminalResponseRequest{kind: terminalResponseBackground}, true
	case "12;?":
		return terminalResponseRequest{kind: terminalResponseCursorColor}, true
	default:
		return terminalResponseRequest{}, false
	}
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
