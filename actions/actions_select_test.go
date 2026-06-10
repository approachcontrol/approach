package actions

import (
	"errors"
	"os"
	"path/filepath"
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

func tempExecutableShell(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
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
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("tmux", "alacritty"), nil)
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
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("zellij"), nil)
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
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("wezterm"), nil)
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

func TestEditFileWithOptionsUsesConfiguredEditorBeforeEditorEnv(t *testing.T) {
	launch, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(map[string]string{
		"EDITOR": "vim",
	}), fakeLookPath("code", "vim"), EditorOptions{
		EditorCommand: "code --wait",
	})
	if err != nil {
		t.Fatalf("editFileWithOptions returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("edit launch should be interactive")
	}
	want := []string{"code", "--wait", "/state/plans/plan-1/plan.md"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("editor args = %#v, want %#v", launch.Cmd.Args, want)
	}
}

func TestEditFileWithOptionsFallsBackToEditorEnv(t *testing.T) {
	launch, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(map[string]string{
		"EDITOR": "vim",
	}), fakeLookPath("vim"), EditorOptions{})
	if err != nil {
		t.Fatalf("editFileWithOptions returned error: %v", err)
	}
	want := []string{"vim", "/state/plans/plan-1/plan.md"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("editor args = %#v, want %#v", launch.Cmd.Args, want)
	}
}

func TestEditFileWithOptionsParsesQuotedEditorArgs(t *testing.T) {
	launch, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(nil), fakeLookPath("vim"), EditorOptions{
		EditorCommand: `vim -c "set ft=markdown"`,
	})
	if err != nil {
		t.Fatalf("editFileWithOptions returned error: %v", err)
	}
	want := []string{"vim", "-c", "set ft=markdown", "/state/plans/plan-1/plan.md"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("editor args = %#v, want %#v", launch.Cmd.Args, want)
	}
}

func TestEditFileWithOptionsPreservesEmptyQuotedEditorArg(t *testing.T) {
	launch, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(nil), fakeLookPath("emacsclient"), EditorOptions{
		EditorCommand: `emacsclient -a "" -c`,
	})
	if err != nil {
		t.Fatalf("editFileWithOptions returned error: %v", err)
	}
	want := []string{"emacsclient", "-a", "", "-c", "/state/plans/plan-1/plan.md"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("editor args = %#v, want %#v", launch.Cmd.Args, want)
	}
}

func TestEditFileWithOptionsWhitespaceConfiguredEditorFallsBackToEditorEnv(t *testing.T) {
	launch, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(map[string]string{
		"EDITOR": "vim",
	}), fakeLookPath("vim"), EditorOptions{EditorCommand: " \t "})
	if err != nil {
		t.Fatalf("editFileWithOptions returned error: %v", err)
	}
	want := []string{"vim", "/state/plans/plan-1/plan.md"}
	if !reflect.DeepEqual(launch.Cmd.Args, want) {
		t.Fatalf("editor args = %#v, want %#v", launch.Cmd.Args, want)
	}
}

func TestEditFileWithOptionsReportsCommentOnlyEditorAsEmpty(t *testing.T) {
	_, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(nil), fakeLookPath(), EditorOptions{
		EditorCommand: "# disabled",
	})
	if err == nil {
		t.Fatal("expected empty editor error")
	}
	if !strings.Contains(err.Error(), "[editor].command is empty") {
		t.Fatalf("error = %v, want empty editor error", err)
	}
}

func TestEditFileWithOptionsReportsQuotedEmptyCommandAsEmpty(t *testing.T) {
	_, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(map[string]string{
		"EDITOR": `""`,
	}), fakeLookPath(), EditorOptions{})
	if err == nil {
		t.Fatal("expected empty editor error")
	}
	if !strings.Contains(err.Error(), "EDITOR is empty") {
		t.Fatalf("error = %v, want empty editor error", err)
	}
}

func TestEditFileWithOptionsReportsMissingEditor(t *testing.T) {
	_, err := editFileWithOptions("/state/plans/plan-1/plan.md", fakeGetenv(nil), fakeLookPath(), EditorOptions{})
	if err == nil {
		t.Fatal("expected missing editor error")
	}
	for _, want := range []string{"[editor].command", "EDITOR"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing editor error should contain %q, got %v", want, err)
		}
	}
}

func TestTerminalLaunch_DarwinFallsBackToTerminalApp(t *testing.T) {
	launch, err := terminalLaunch("/repo", "darwin", fakeGetenv(nil), fakeLookPath("open"), nil)
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "-a", "Terminal", "/repo"}) {
		t.Fatalf("unexpected macOS fallback args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_DarwinConfiguredITermOpensWorktree(t *testing.T) {
	launch, err := terminalLaunchWithOptions("/repo", "darwin", fakeGetenv(nil), fakeLookPath("osascript"), nil, LaunchOptions{
		TerminalCommand: "iTerm",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if launch.Cmd.Args[0] != "osascript" {
		t.Fatalf("expected iTerm AppleScript transport, got %#v", launch.Cmd.Args)
	}
	joined := strings.Join(launch.Cmd.Args, "\n")
	for _, want := range []string{`tell application "iTerm"`, "activate", "set newWindow to (create window with default profile)", "write text", "cd '/repo' && exec ${SHELL:-/bin/sh}"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected iTerm launch args to contain %q, got %#v", want, launch.Cmd.Args)
		}
	}
	if strings.Contains(joined, "current session of current window") {
		t.Fatalf("iTerm launch should not write into the user's current session: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_DarwinTerminalAppNormalizesToFallback(t *testing.T) {
	launch, err := terminalLaunchWithOptions("/repo", "darwin", fakeGetenv(nil), fakeLookPath("open"), nil, LaunchOptions{
		TerminalCommand: "Terminal.app",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "-a", "Terminal", "/repo"}) {
		t.Fatalf("unexpected macOS Terminal.app args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_ActiveTmuxWinsOverConfiguredTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{"TMUX": "/tmp/tmux.sock"})
	launch, err := terminalLaunchWithOptions("/repo", "darwin", env, fakeLookPath("tmux", "osascript"), nil, LaunchOptions{
		TerminalCommand: "iTerm",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if launch.Cmd.Args[0] != "sh" || !strings.Contains(strings.Join(launch.Cmd.Args, " "), "tmux") {
		t.Fatalf("expected active tmux transport, got %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_DarwinOutsideTmuxUsesConfiguredITermWrapper(t *testing.T) {
	launch, err := terminalLaunchWithOptions("/repo", "darwin", fakeGetenv(nil), fakeLookPath("tmux", "osascript"), nil, LaunchOptions{
		TerminalCommand: "iTerm.app",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	joined := strings.Join(launch.Cmd.Args, "\n")
	for _, want := range []string{`tell application "iTerm"`, "write text", "cd '/repo' && exec ${SHELL:-/bin/sh}"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected configured iTerm launch to contain %q, got %#v", want, launch.Cmd.Args)
		}
	}
	if strings.Contains(joined, "tmux new-session -A -s") {
		t.Fatalf("configured iTerm should win over installed tmux outside tmux, got %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_DarwinOutsideTmuxUsesConfiguredCLIWrapper(t *testing.T) {
	launch, err := terminalLaunchWithOptions("/repo", "darwin", fakeGetenv(nil), fakeLookPath("tmux", "wezterm"), nil, LaunchOptions{
		TerminalCommand: "wezterm start",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"wezterm", "start"}) {
		t.Fatalf("expected configured CLI to win over installed tmux, got %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected CLI wrapper dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunchWithOptions_TerminalEnvWinsOverInstalledTmuxOutsideTmux(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "alacritty"})
	launch, err := terminalLaunchWithOptions("/repo", "linux", env, fakeLookPath("tmux", "alacritty"), nil, LaunchOptions{
		TerminalCommand: "wezterm start",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"alacritty"}) {
		t.Fatalf("expected TERMINAL to win over installed tmux, got %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_TerminalEnvWinsOverConfiguredTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "alacritty"})
	launch, err := terminalLaunchWithOptions("/repo", "linux", env, fakeLookPath("alacritty", "wezterm"), nil, LaunchOptions{
		TerminalCommand: "wezterm start",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"alacritty"}) {
		t.Fatalf("expected TERMINAL to win, got %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_ConfiguredCLIUsesConfiguredArgs(t *testing.T) {
	launch, err := terminalLaunchWithOptions("/repo", "linux", fakeGetenv(nil), fakeLookPath("wezterm"), nil, LaunchOptions{
		TerminalCommand: "wezterm start --cwd .",
	})
	if err != nil {
		t.Fatalf("terminalLaunchWithOptions returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"wezterm", "start", "--cwd", "."}) {
		t.Fatalf("unexpected configured CLI args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunchWithOptions_RejectsSupportedGUIAliasWithArgs(t *testing.T) {
	_, err := terminalLaunchWithOptions("/repo", "darwin", fakeGetenv(nil), fakeLookPath("osascript"), nil, LaunchOptions{
		TerminalCommand: "iTerm --new-window",
	})
	if err == nil {
		t.Fatal("expected supported GUI alias with args to be rejected")
	}
	for _, want := range []string{"[terminal].command", "iTerm --new-window", "unsupported arguments"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}

func TestTerminalLaunch_DarwinFallsBackToOpenAppWhenTerminalMissing(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "wezterm start"})
	launch, err := terminalLaunch("/repo", "darwin", env, fakeLookPath("open"), nil)
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{"open", "-a", "wezterm", "/repo"}) {
		t.Fatalf("unexpected macOS TERMINAL fallback args: %#v", launch.Cmd.Args)
	}
}

func TestTerminalLaunch_LinuxUsesShellFallbackEvenWhenXDGOpenExists(t *testing.T) {
	shell := tempExecutableShell(t)
	env := fakeGetenv(map[string]string{"SHELL": shell})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath("xdg-open"), nil)
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("shell fallback should require the caller TTY")
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{shell}) {
		t.Fatalf("unexpected shell fallback args: %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected shell launch dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunch_LinuxUsesShellFallback(t *testing.T) {
	shell := tempExecutableShell(t)
	env := fakeGetenv(map[string]string{"SHELL": shell})
	launch, err := terminalLaunch("/repo", "linux", env, fakeLookPath(), nil)
	if err != nil {
		t.Fatalf("terminalLaunch returned error: %v", err)
	}
	if !launch.Interactive {
		t.Fatal("shell fallback should require the caller TTY")
	}
	if !reflect.DeepEqual(launch.Cmd.Args, []string{shell}) {
		t.Fatalf("unexpected shell fallback args: %#v", launch.Cmd.Args)
	}
	if launch.Cmd.Dir != "/repo" {
		t.Fatalf("expected shell launch dir /repo, got %q", launch.Cmd.Dir)
	}
}

func TestTerminalLaunch_ReportsMissingTerminalCommand(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "ghostterm"})
	_, err := terminalLaunch("/repo", "linux", env, fakeLookPath(), nil)
	if err == nil {
		t.Fatal("expected missing TERMINAL command error")
	}
	for _, want := range []string{"TERMINAL", "ghostterm"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}
