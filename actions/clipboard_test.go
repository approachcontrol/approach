package actions

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

type recordingWriteCloser struct {
	bytes.Buffer
	closeErr error
}

func (w *recordingWriteCloser) Close() error { return w.closeErr }

func TestBuildOSC52Sequence(t *testing.T) {
	text := "copy 雪"
	payload := base64.StdEncoding.EncodeToString([]byte(text))

	tests := []struct {
		name string
		tmux bool
		want string
	}{
		{
			name: "plain terminal",
			want: "\x1b]52;c;" + payload + "\x1b\\",
		},
		{
			name: "tmux passthrough",
			tmux: true,
			want: "\x1bPtmux;\x1b\x1b]52;c;" + payload + "\x1b\x1b\\\x1b\\",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildOSC52Sequence(text, 100_000, tt.tmux)
			if err != nil {
				t.Fatalf("buildOSC52Sequence returned error: %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("sequence = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCopyToClipboardWithOptions_ForcedOSC52BypassesSystemClipboard(t *testing.T) {
	terminal := &recordingWriteCloser{}
	lookedUp := false
	deps := clipboardDeps{
		goos: "linux",
		lookPath: func(string) (string, error) {
			lookedUp = true
			return "", errors.New("unexpected lookup")
		},
		getenv: func(string) string { return "" },
		runSystem: func(commandSpec, string) error {
			t.Fatal("system clipboard command should not run")
			return nil
		},
		openTerminal: func() (io.WriteCloser, error) { return terminal, nil },
	}

	err := copyToClipboardWithOptions("hello", ClipboardOptions{
		Method:               ClipboardMethodOSC52,
		OSC52MaxPayloadBytes: 100_000,
	}, deps)
	if err != nil {
		t.Fatalf("copyToClipboardWithOptions returned error: %v", err)
	}
	if lookedUp {
		t.Fatal("forced OSC 52 should bypass clipboard command lookup")
	}
	want, err := buildOSC52Sequence("hello", 100_000, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(terminal.Bytes(), want) {
		t.Fatalf("terminal write = %q, want %q", terminal.Bytes(), want)
	}
}

func TestCopyToClipboardWithOptions_SelectsTransport(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		available    []string
		runErr       error
		wantSystem   string
		wantTerminal bool
		wantError    string
	}{
		{
			name:       "auto prefers native command",
			method:     ClipboardMethodAuto,
			available:  []string{"wl-copy", "xclip"},
			wantSystem: "wl-copy",
		},
		{
			name:       "system uses native command",
			method:     ClipboardMethodSystem,
			available:  []string{"xsel"},
			wantSystem: "xsel",
		},
		{
			name:         "auto falls back when commands are unavailable",
			method:       ClipboardMethodAuto,
			wantTerminal: true,
		},
		{
			name:      "auto reports native execution failure",
			method:    ClipboardMethodAuto,
			available: []string{"xclip"},
			runErr:    errors.New("exit status 1"),
			wantError: "run clipboard command xclip",
		},
		{
			name:      "system requires a native command",
			method:    ClipboardMethodSystem,
			wantError: "wl-copy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal := &recordingWriteCloser{}
			var ranSystem string
			openedTerminal := false
			deps := clipboardDeps{
				goos:     "linux",
				lookPath: fakeLookPath(tt.available...),
				getenv:   func(string) string { return "" },
				runSystem: func(spec commandSpec, text string) error {
					ranSystem = spec.name
					if text != "hello" {
						t.Fatalf("system clipboard input = %q, want hello", text)
					}
					return tt.runErr
				},
				openTerminal: func() (io.WriteCloser, error) {
					openedTerminal = true
					return terminal, nil
				},
			}

			err := copyToClipboardWithOptions("hello", ClipboardOptions{
				Method:               tt.method,
				OSC52MaxPayloadBytes: 100_000,
			}, deps)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want text %q", err, tt.wantError)
				}
			} else if err != nil {
				t.Fatalf("copyToClipboardWithOptions returned error: %v", err)
			}
			if ranSystem != tt.wantSystem && !(tt.runErr != nil && ranSystem == "xclip") {
				t.Fatalf("system command = %q, want %q", ranSystem, tt.wantSystem)
			}
			if openedTerminal != tt.wantTerminal {
				t.Fatalf("opened terminal = %v, want %v", openedTerminal, tt.wantTerminal)
			}
		})
	}
}

func TestBuildOSC52SequencePayloadLimit(t *testing.T) {
	text := "abc"
	encodedSize := base64.StdEncoding.EncodedLen(len(text))

	if _, err := buildOSC52Sequence(text, encodedSize, false); err != nil {
		t.Fatalf("payload at boundary returned error: %v", err)
	}
	if _, err := buildOSC52Sequence(text, 0, false); err != nil {
		t.Fatalf("unlimited payload returned error: %v", err)
	}
	_, err := buildOSC52Sequence(text, encodedSize-1, false)
	if err == nil {
		t.Fatal("oversized payload should fail")
	}
	for _, want := range []string{"4 bytes", "limit is 3 bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want text %q", err, want)
		}
	}
}

func TestCopyToClipboardWithOptions_RejectsOversizedPayloadBeforeTerminalWrite(t *testing.T) {
	openedTerminal := false
	deps := clipboardDeps{
		goos:     "linux",
		lookPath: fakeLookPath(),
		getenv:   func(string) string { return "" },
		runSystem: func(commandSpec, string) error {
			t.Fatal("system command should not run")
			return nil
		},
		openTerminal: func() (io.WriteCloser, error) {
			openedTerminal = true
			return &recordingWriteCloser{}, nil
		},
	}

	err := copyToClipboardWithOptions("abc", ClipboardOptions{
		Method:               ClipboardMethodOSC52,
		OSC52MaxPayloadBytes: 3,
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "4 bytes") {
		t.Fatalf("error = %v, want encoded payload size", err)
	}
	if openedTerminal {
		t.Fatal("oversized payload should fail before opening the terminal")
	}
}

type failingWriteCloser struct {
	n        int
	writeErr error
	closeErr error
}

func (w failingWriteCloser) Write([]byte) (int, error) { return w.n, w.writeErr }
func (w failingWriteCloser) Close() error              { return w.closeErr }

func TestCopyToClipboardWithOptions_ReportsTerminalFailures(t *testing.T) {
	tests := []struct {
		name      string
		openErr   error
		writer    io.WriteCloser
		wantError string
	}{
		{name: "open", openErr: errors.New("no tty"), wantError: "open controlling terminal"},
		{name: "short write", writer: failingWriteCloser{n: 1}, wantError: "short write"},
		{name: "write", writer: failingWriteCloser{writeErr: errors.New("broken pipe")}, wantError: "broken pipe"},
		{name: "close", writer: failingWriteCloser{closeErr: errors.New("close failed")}, wantError: "close failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := clipboardDeps{
				goos:     "linux",
				lookPath: fakeLookPath(),
				getenv:   func(string) string { return "" },
				runSystem: func(commandSpec, string) error {
					t.Fatal("system command should not run")
					return nil
				},
				openTerminal: func() (io.WriteCloser, error) {
					return tt.writer, tt.openErr
				},
			}
			err := copyToClipboardWithOptions("hello", ClipboardOptions{
				Method:               ClipboardMethodOSC52,
				OSC52MaxPayloadBytes: 100_000,
			}, deps)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want text %q", err, tt.wantError)
			}
		})
	}
}
