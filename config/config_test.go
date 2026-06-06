package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brian-bell/wtui/config"
)

func TestLoadFrom_AllowsMissingConfig(t *testing.T) {
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadFrom returned error for missing config: %v", err)
	}

	if cfg.Scan.Root != "" {
		t.Fatalf("expected empty scan root, got %q", cfg.Scan.Root)
	}
}

func TestLoadFrom_ParsesConfigSections(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[scan]
root = "~/src"
max_depth = 1

[editor]
command = "code"

[terminal]
command = "wezterm start"

[provider]
name = "github"

[launch]
prefer_multiplexer = true

[agent]
command = "codex"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path, config.WithHomeDir(func() (string, error) {
		return home, nil
	}))
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}

	if cfg.Scan.Root != filepath.Join(home, "src") {
		t.Fatalf("expected expanded scan root, got %q", cfg.Scan.Root)
	}
	if cfg.Scan.MaxDepth != 1 {
		t.Fatalf("expected max depth 1, got %d", cfg.Scan.MaxDepth)
	}
	if cfg.Editor.Command != "code" {
		t.Fatalf("expected editor command code, got %q", cfg.Editor.Command)
	}
	if cfg.Terminal.Command != "wezterm start" {
		t.Fatalf("expected terminal command, got %q", cfg.Terminal.Command)
	}
	if cfg.Provider.Name != "github" {
		t.Fatalf("expected provider github, got %q", cfg.Provider.Name)
	}
	if !cfg.Launch.PreferMultiplexer {
		t.Fatal("expected launch prefer_multiplexer to parse true")
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("expected agent command codex, got %q", cfg.Agent.Command)
	}
}

func TestSaveAgentCommand_CreatesMissingConfig(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentCommand("claude",
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return t.TempDir(), nil
		}),
	)
	if err != nil {
		t.Fatalf("SaveAgentCommand returned error: %v", err)
	}

	path := filepath.Join(xdg, "wtui", "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "claude" {
		t.Fatalf("expected saved agent claude, got %q", cfg.Agent.Command)
	}
}

func TestSaveAgentCommand_PreservesExistingParsedSettings(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[scan]\nroot = \"/src\"\nmax_depth = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SaveAgentCommand("codex",
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return t.TempDir(), nil
		}),
	)
	if err != nil {
		t.Fatalf("SaveAgentCommand returned error: %v", err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Scan.Root != "/src" || cfg.Scan.MaxDepth != 1 {
		t.Fatalf("expected scan settings preserved, got root=%q depth=%d", cfg.Scan.Root, cfg.Scan.MaxDepth)
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("expected saved agent codex, got %q", cfg.Agent.Command)
	}
}

func TestSaveAgentCommand_UpdatesExistingFallbackConfig(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	homePath := filepath.Join(home, ".config", "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(homePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homePath, []byte("[scan]\nroot = \"/home-src\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SaveAgentCommand("claude",
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return home, nil
		}),
	)
	if err != nil {
		t.Fatalf("SaveAgentCommand returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(xdg, "wtui", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected missing XDG config to stay missing, stat err=%v", err)
	}
	cfg, err := config.LoadFrom(homePath)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Scan.Root != "/home-src" || cfg.Agent.Command != "claude" {
		t.Fatalf("expected fallback config preserved and updated, got root=%q agent=%q", cfg.Scan.Root, cfg.Agent.Command)
	}
}

func TestLoadFrom_RejectsUnknownAgentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncmd = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected unknown agent field error")
	}
	if !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected strict decoder error, got %q", err.Error())
	}
}

func TestSaveAgentCommand_RejectsUnsupportedCommand(t *testing.T) {
	err := config.SaveAgentCommand("vim",
		config.WithGetenv(func(string) string { return t.TempDir() }),
		config.WithHomeDir(func() (string, error) { return t.TempDir(), nil }),
	)
	if err == nil {
		t.Fatal("expected unsupported command error")
	}
	if !strings.Contains(err.Error(), "unsupported agent") {
		t.Fatalf("expected unsupported agent error, got %q", err.Error())
	}
}

func TestLoadFrom_ReportsMalformedConfigWithPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected malformed config error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("expected error to include path %q, got %q", path, err.Error())
	}
}

func TestLoadFrom_RejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan]\nroto = \"~/src\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("expected error to include path %q, got %q", path, err.Error())
	}
	if !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected strict decoder error, got %q", err.Error())
	}
}

func TestLoadFrom_ReportsUnreadableConfigWithPath(t *testing.T) {
	path := t.TempDir()

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected unreadable config error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("expected error to include path %q, got %q", path, err.Error())
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected read config error, got %q", err.Error())
	}
}

func TestLoadFrom_RejectsNegativeMaxDepth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan]\nmax_depth = -1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected negative max_depth error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("expected error to include path %q, got %q", path, err.Error())
	}
	if !strings.Contains(err.Error(), "max_depth") {
		t.Fatalf("expected error to mention max_depth, got %q", err.Error())
	}
}

func TestLoad_StopsAtMalformedXDGConfig(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	xdgConfig := filepath.Join(xdg, "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(xdgConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xdgConfig, []byte("[scan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	homeConfig := filepath.Join(home, ".config", "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeConfig, []byte("[scan]\nroot = \"/home-config\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.Load(
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return home, nil
		}),
	)
	if err == nil {
		t.Fatal("expected malformed XDG config error")
	}
	if !strings.Contains(err.Error(), xdgConfig) {
		t.Fatalf("expected error to include XDG config path %q, got %q", xdgConfig, err.Error())
	}
}

func TestLoad_FallsBackToHomeConfigWhenXDGConfigIsMissing(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	homeConfig := filepath.Join(home, ".config", "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeConfig, []byte("[scan]\nroot = \"/home-config\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return home, nil
		}),
	)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Scan.Root != "/home-config" {
		t.Fatalf("expected home config fallback, got root %q", cfg.Scan.Root)
	}
}

func TestDefaultPath_UsesXDGConfigHome(t *testing.T) {
	path, err := config.DefaultPath(
		config.WithGetenv(func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/xdg"
			}
			return ""
		}),
		config.WithHomeDir(func() (string, error) {
			return "/home/user", nil
		}),
	)
	if err != nil {
		t.Fatalf("DefaultPath returned error: %v", err)
	}

	if path != filepath.Join("/xdg", "wtui", "config.toml") {
		t.Fatalf("unexpected config path %q", path)
	}
}

func TestDefaultPath_FallsBackToHomeConfig(t *testing.T) {
	path, err := config.DefaultPath(
		config.WithGetenv(func(string) string { return "" }),
		config.WithHomeDir(func() (string, error) {
			return "/home/user", nil
		}),
	)
	if err != nil {
		t.Fatalf("DefaultPath returned error: %v", err)
	}

	if path != filepath.Join("/home/user", ".config", "wtui", "config.toml") {
		t.Fatalf("unexpected config path %q", path)
	}
}
