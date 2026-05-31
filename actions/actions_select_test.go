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

func assertSpec(t *testing.T, got commandSpec, name string, args []string, dir string) {
	t.Helper()
	if got.name != name {
		t.Fatalf("expected command %q, got %q", name, got.name)
	}
	if !reflect.DeepEqual(got.args, args) {
		t.Fatalf("expected args %#v, got %#v", args, got.args)
	}
	if got.dir != dir {
		t.Fatalf("expected dir %q, got %q", dir, got.dir)
	}
}

func TestSelectClipboardCommand_DarwinUsesPbcopy(t *testing.T) {
	spec, err := selectClipboardCommand("darwin", fakeLookPath("pbcopy"))
	if err != nil {
		t.Fatalf("selectClipboardCommand returned error: %v", err)
	}
	assertSpec(t, spec, "pbcopy", nil, "")
}

func TestSelectClipboardCommand_LinuxPrefersWaylandThenX11(t *testing.T) {
	spec, err := selectClipboardCommand("linux", fakeLookPath("wl-copy", "xclip", "xsel"))
	if err != nil {
		t.Fatalf("selectClipboardCommand returned error: %v", err)
	}
	assertSpec(t, spec, "wl-copy", nil, "")

	spec, err = selectClipboardCommand("linux", fakeLookPath("xclip", "xsel"))
	if err != nil {
		t.Fatalf("selectClipboardCommand returned error: %v", err)
	}
	assertSpec(t, spec, "xclip", []string{"-selection", "clipboard"}, "")

	spec, err = selectClipboardCommand("linux", fakeLookPath("xsel"))
	if err != nil {
		t.Fatalf("selectClipboardCommand returned error: %v", err)
	}
	assertSpec(t, spec, "xsel", []string{"--clipboard", "--input"}, "")
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

func TestSelectTerminalCommand_UsesMultiplexerBeforeTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{
		"TMUX":     "/tmp/tmux.sock",
		"TERMINAL": "alacritty",
	})
	spec, err := selectTerminalCommand("linux", "/repo", env, fakeLookPath("tmux", "alacritty"))
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "tmux", []string{"new-window", "-c", "/repo"}, "")
}

func TestSelectTerminalCommand_UsesZellijWhenActive(t *testing.T) {
	env := fakeGetenv(map[string]string{"ZELLIJ": "0"})
	spec, err := selectTerminalCommand("linux", "/repo", env, fakeLookPath("zellij"))
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "zellij", []string{"action", "new-pane", "--cwd", "/repo"}, "")
}

func TestSelectTerminalCommand_HonorsTerminal(t *testing.T) {
	env := fakeGetenv(map[string]string{"TERMINAL": "wezterm start"})
	spec, err := selectTerminalCommand("linux", "/repo", env, fakeLookPath("wezterm"))
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "wezterm", []string{"start"}, "/repo")
}

func TestSelectTerminalCommand_DarwinFallsBackToTerminalApp(t *testing.T) {
	spec, err := selectTerminalCommand("darwin", "/repo", fakeGetenv(nil), fakeLookPath())
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "open", []string{"-a", "Terminal", "/repo"}, "")
}

func TestSelectTerminalCommand_LinuxUsesXDGOpenFallback(t *testing.T) {
	spec, err := selectTerminalCommand("linux", "/repo", fakeGetenv(nil), fakeLookPath("xdg-open"))
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "xdg-open", []string{"/repo"}, "")
}

func TestSelectTerminalCommand_LinuxUsesShellFallback(t *testing.T) {
	env := fakeGetenv(map[string]string{"SHELL": "/bin/zsh"})
	spec, err := selectTerminalCommand("linux", "/repo", env, fakeLookPath())
	if err != nil {
		t.Fatalf("selectTerminalCommand returned error: %v", err)
	}
	assertSpec(t, spec, "/bin/zsh", nil, "/repo")
}

func TestSelectTerminalCommand_LinuxReportsMissingLauncher(t *testing.T) {
	_, err := selectTerminalCommand("linux", "/repo", fakeGetenv(nil), fakeLookPath())
	if err == nil {
		t.Fatal("expected missing terminal launcher error")
	}
	for _, want := range []string{"TERMINAL", "xdg-open"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}
