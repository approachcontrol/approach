package actions

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fakeLookPath(available ...string) lookPathFunc {
	found := map[string]bool{}
	for _, name := range available {
		found[name] = true
	}
	return func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func fakeGetenv(values map[string]string) getenvFunc {
	return func(key string) string {
		return values[key]
	}
}

func assertSpec(t *testing.T, got commandSpec, name string, args []string) {
	t.Helper()
	if got.name != name {
		t.Fatalf("expected command %q, got %q", name, got.name)
	}
	if !reflect.DeepEqual(got.args, args) {
		t.Fatalf("expected args %#v, got %#v", args, got.args)
	}
}

func TestSelectClipboardCommand_DarwinUsesPbcopy(t *testing.T) {
	spec, err := selectClipboardCommand("darwin", fakeLookPath("pbcopy"))
	if err != nil {
		t.Fatalf("selectClipboardCommand returned error: %v", err)
	}
	assertSpec(t, spec, "pbcopy", nil)
}

func TestSelectClipboardCommand_LinuxPrefersWaylandThenX11(t *testing.T) {
	tests := []struct {
		name      string
		available []string
		wantName  string
		wantArgs  []string
	}{
		{
			name:      "prefers wl-copy",
			available: []string{"wl-copy", "xclip", "xsel"},
			wantName:  "wl-copy",
		},
		{
			name:      "prefers xclip over xsel",
			available: []string{"xclip", "xsel"},
			wantName:  "xclip",
			wantArgs:  []string{"-selection", "clipboard"},
		},
		{
			name:      "falls back to xsel",
			available: []string{"xsel"},
			wantName:  "xsel",
			wantArgs:  []string{"--clipboard", "--input"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := selectClipboardCommand("linux", fakeLookPath(tt.available...))
			if err != nil {
				t.Fatalf("selectClipboardCommand returned error: %v", err)
			}
			assertSpec(t, spec, tt.wantName, tt.wantArgs)
		})
	}
}

func TestSelectClipboardCommand_LinuxReportsMissingTools(t *testing.T) {
	_, err := selectClipboardCommand("linux", fakeLookPath())
	if err == nil {
		t.Fatal("expected missing clipboard command error")
	}
	for _, want := range []string{"wl-copy", "xclip", "xsel"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}

func TestTerminalLaunch_UsesMultiplexerBeforeTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{
		"TMUX":     "/tmp/tmux.sock",
		"TERMINAL": "alacritty",
	})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("tmux", "alacritty"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("inside-tmux launch should be non-interactive")
	}
	if got := launch.Cmd.Args; len(got) != 6 || got[0] != "sh" || got[1] != "-c" || got[3] != "wtui" || got[5] != "/repo" {
		t.Fatalf("unexpected tmux launch args: %#v", got)
	}
}

func TestTerminalLaunch_UsesZellijWhenActive(t *testing.T) {
	env := fakeGetenv(map[string]string{"ZELLIJ": "0"})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("zellij"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	want := []string{"zellij", "action", "switch-session", WorktreeSessionName("/repo"), "--cwd", "/repo"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("unexpected zellij launch args: got %#v want %#v", launch.Cmd.Args, want)
	}
}

func TestTerminalLaunch_HonorsTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "wezterm start"})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("wezterm"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if launch.Interactive {
		t.Fatal("TERMINAL launch should not require the caller TTY")
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"wezterm", "start"}) {
		t.Fatalf("unexpected TERMINAL args: %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected TERMINAL launch dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunch_DarwinFallsBackToTerminalApp(t *testing.T) {
	launch, err := terminalLaunch("/repo", "darwin", fakeGetenv(nil), fakeLookPath("open"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "-a", "Terminal", "/repo"}) {
		t.Fatalf("unexpected macOS fallback args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunch_DarwinFallsBackToOpenAppWhenTerminalMissing(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "wezterm start"})
	launch, err := terminalLaunch("/repo", "darwin", env, fakeLookPath("open"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "-a", "wezterm", "/repo"}) {
		t.Fatalf("unexpected macOS TERMINAL fallback args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunch_LinuxUsesShellFallbackEvenWhenXDGOpenExists(t *testing.T) {
	env := fakeGetenv(map[string]string{"SHELL": "/bin/zsh"})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("xdg-open"))
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("shell fallback should require the caller TTY")
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"/bin/zsh"}) {
		t.Fatalf("unexpected shell fallback args: %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected shell launch dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunch_LinuxUsesShellFallback(t *testing.T) {
	env := fakeGetenv(map[string]string{"SHELL": "/bin/zsh"})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath())
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("shell fallback should require the caller TTY")
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"/bin/zsh"}) {
		t.Fatalf("unexpected shell fallback args: %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected shell launch dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunch_ReportsMissingTerminalCommand(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "ghostterm"})
	_, err := terminalLaunch("/repo", "linux", env, fakeLookPath())
	if err == nil {
		t.Fatal("expected missing TERMINAL command error")
	}
	for _, want := range []string{"TERMINAL", "ghostterm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}
