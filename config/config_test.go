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
plan_prompt = "Implement {title} from {plan_path}"
codex_reasoning_effort = " HIGH "
claude_reasoning_effort = "max"

[flow_prompts]
plan = "Plan only: {instructions}"
implementation = "Implement {plan_path} in {worktree_path}"
autoreview = "Review {pr_url} and ship fixes"

[sessions]
root = "~/state/wtui/sessions"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 180

[[bootstrap.hooks]]
repo_path = "~/wtui"
script = ".wtui/bootstrap"

[[bootstrap.hooks]]
repo_path = "/dev/client-api/"
script = "~/bin/bootstrap-client-api"
timeout_seconds = 300
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
	if cfg.Agent.PlanPrompt != "Implement {title} from {plan_path}" {
		t.Fatalf("expected agent plan prompt to parse, got %q", cfg.Agent.PlanPrompt)
	}
	if cfg.Agent.CodexReasoningEffort != "high" {
		t.Fatalf("expected normalized codex reasoning effort high, got %q", cfg.Agent.CodexReasoningEffort)
	}
	if cfg.Agent.ClaudeReasoningEffort != "max" {
		t.Fatalf("expected claude reasoning effort max, got %q", cfg.Agent.ClaudeReasoningEffort)
	}
	if cfg.FlowPrompts.Plan != "Plan only: {instructions}" ||
		cfg.FlowPrompts.Implementation != "Implement {plan_path} in {worktree_path}" ||
		cfg.FlowPrompts.Autoreview != "Review {pr_url} and ship fixes" {
		t.Fatalf("expected flow prompt templates to parse, got %#v", cfg.FlowPrompts)
	}
	if cfg.Sessions.Root != filepath.Join(home, "state", "wtui", "sessions") {
		t.Fatalf("expected expanded sessions root, got %q", cfg.Sessions.Root)
	}
	if cfg.Sessions.CopyRawTranscripts {
		t.Fatal("expected sessions copy_raw_transcripts false")
	}
	if cfg.Bootstrap.TimeoutSeconds != 180 {
		t.Fatalf("expected bootstrap timeout 180, got %d", cfg.Bootstrap.TimeoutSeconds)
	}
	if len(cfg.Bootstrap.Hooks) != 2 {
		t.Fatalf("expected 2 bootstrap hooks, got %d", len(cfg.Bootstrap.Hooks))
	}
	if cfg.Bootstrap.Hooks[0].RepoPath != filepath.Join(home, "wtui") {
		t.Fatalf("expected expanded repo path, got %q", cfg.Bootstrap.Hooks[0].RepoPath)
	}
	if cfg.Bootstrap.Hooks[0].Script != ".wtui/bootstrap" {
		t.Fatalf("expected relative script preserved, got %q", cfg.Bootstrap.Hooks[0].Script)
	}
	if cfg.Bootstrap.Hooks[0].TimeoutSeconds != 0 {
		t.Fatalf("expected hook timeout override omitted, got %d", cfg.Bootstrap.Hooks[0].TimeoutSeconds)
	}
	if cfg.Bootstrap.Hooks[1].RepoPath != filepath.Clean("/dev/client-api/") {
		t.Fatalf("expected cleaned repo path, got %q", cfg.Bootstrap.Hooks[1].RepoPath)
	}
	if cfg.Bootstrap.Hooks[1].Script != filepath.Join(home, "bin", "bootstrap-client-api") {
		t.Fatalf("expected expanded script path, got %q", cfg.Bootstrap.Hooks[1].Script)
	}
	if cfg.Bootstrap.Hooks[1].TimeoutSeconds != 300 {
		t.Fatalf("expected per-hook timeout 300, got %d", cfg.Bootstrap.Hooks[1].TimeoutSeconds)
	}
}

func TestLoadFrom_DefaultsBootstrapTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[bootstrap]

[[bootstrap.hooks]]
repo_path = "/dev/wtui"
script = ".wtui/bootstrap"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}

	if cfg.Bootstrap.TimeoutSeconds != 120 {
		t.Fatalf("expected default bootstrap timeout 120, got %d", cfg.Bootstrap.TimeoutSeconds)
	}
}

func TestLoadFrom_DefaultsSessionsCopyRawTranscriptsOff(t *testing.T) {
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Sessions.CopyRawTranscripts {
		t.Fatal("expected sessions copy_raw_transcripts to default false")
	}
}

func TestLoadFrom_ParsesSessionsCopyRawTranscriptsOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[sessions]\ncopy_raw_transcripts = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if !cfg.Sessions.CopyRawTranscripts {
		t.Fatal("expected explicit copy_raw_transcripts true to parse")
	}
}

func TestLoadFromRejectsRelativeSessionsRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[sessions]\nroot = \".wtui-sessions\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected relative sessions root error")
	}
	if !strings.Contains(err.Error(), "sessions.root must be absolute") {
		t.Fatalf("expected sessions.root absolute error, got %q", err)
	}
}

func TestLoadFrom_RejectsUnknownBootstrapFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[bootstrap]\ntimeout = 120\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected unknown bootstrap field error")
	}
	if !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected strict decoder error, got %q", err.Error())
	}
}

func TestLoadFrom_RejectsInvalidBootstrapHooks(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing repo path",
			body: "[[bootstrap.hooks]]\nscript = \".wtui/bootstrap\"\n",
			want: "repo_path",
		},
		{
			name: "blank repo path",
			body: "[[bootstrap.hooks]]\nrepo_path = \"   \"\nscript = \".wtui/bootstrap\"\n",
			want: "repo_path",
		},
		{
			name: "missing script",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/wtui\"\n",
			want: "script",
		},
		{
			name: "blank script",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/wtui\"\nscript = \"   \"\n",
			want: "script",
		},
		{
			name: "negative section timeout",
			body: "[bootstrap]\ntimeout_seconds = -1\n",
			want: "timeout_seconds",
		},
		{
			name: "negative hook timeout",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/wtui\"\nscript = \".wtui/bootstrap\"\ntimeout_seconds = -1\n",
			want: "timeout_seconds",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := config.LoadFrom(path)
			if err == nil {
				t.Fatal("expected invalid bootstrap config error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error to mention %q, got %q", tc.want, err.Error())
			}
		})
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

func TestLoadFrom_AcceptsCodexAppAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \" CoDeX-App \"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "codex-app" {
		t.Fatalf("expected normalized agent codex-app, got %q", cfg.Agent.Command)
	}
}

func TestLoadFrom_RejectsInvalidReasoningEfforts(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "codex max",
			body: "[agent]\ncodex_reasoning_effort = \"max\"\n",
			want: "unsupported reasoning effort",
		},
		{
			name: "claude unknown",
			body: "[agent]\nclaude_reasoning_effort = \"turbo\"\n",
			want: "unsupported reasoning effort",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := config.LoadFrom(path)
			if err == nil {
				t.Fatal("expected invalid reasoning effort error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to mention %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestSaveAgentCommand_WritesCodexApp(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentCommand("codex-app",
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `command = "codex-app"`) {
		t.Fatalf("expected codex-app command in saved config, got:\n%s", raw)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "codex-app" {
		t.Fatalf("expected saved agent codex-app, got %q", cfg.Agent.Command)
	}
}

func TestSaveAgentCommand_PreservesExistingParsedSettings(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	path := filepath.Join(xdg, "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# keep me\n[scan]\nroot = \"~/src\"\nmax_depth = 1\n"), 0o644); err != nil {
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
			return home, nil
		}),
	)
	if err != nil {
		t.Fatalf("SaveAgentCommand returned error: %v", err)
	}

	cfg, err := config.LoadFrom(path, config.WithHomeDir(func() (string, error) {
		return home, nil
	}))
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Scan.Root != filepath.Join(home, "src") || cfg.Scan.MaxDepth != 1 {
		t.Fatalf("expected scan settings preserved, got root=%q depth=%d", cfg.Scan.Root, cfg.Scan.MaxDepth)
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("expected saved agent codex, got %q", cfg.Agent.Command)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# keep me", `root = "~/src"`, "[agent]", `command = "codex"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected saved config to contain %q, got:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"[editor]", "[terminal]", "[provider]", "[launch]"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("saved config should not add zero-value section %q, got:\n%s", unwanted, text)
		}
	}
}

func TestSaveAgentCommand_UpdatesExistingAgentSection(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \"codex\"\ncodex_reasoning_effort = \"high\"\n"), 0o644); err != nil {
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
			return t.TempDir(), nil
		}),
	)
	if err != nil {
		t.Fatalf("SaveAgentCommand returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Count(text, "[agent]") != 1 {
		t.Fatalf("expected one agent section, got:\n%s", text)
	}
	if !strings.Contains(text, `command = "claude"`) {
		t.Fatalf("expected updated agent command, got:\n%s", text)
	}
	if !strings.Contains(text, `codex_reasoning_effort = "high"`) {
		t.Fatalf("expected saved agent command to preserve reasoning effort, got:\n%s", text)
	}
}

func TestSaveAgentReasoningEffort_CreatesMissingConfig(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentReasoningEffort("codex", "high",
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
		t.Fatalf("SaveAgentReasoningEffort returned error: %v", err)
	}

	path := filepath.Join(xdg, "wtui", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `codex_reasoning_effort = "high"`) {
		t.Fatalf("expected codex reasoning effort in saved config, got:\n%s", raw)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.CodexReasoningEffort != "high" {
		t.Fatalf("expected saved codex effort high, got %q", cfg.Agent.CodexReasoningEffort)
	}
}

func TestSaveAgentReasoningEffort_UpdatesExistingAgentSection(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "wtui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "# keep me\n[agent]\n# keep agent note\ncommand = \"claude\"\nclaude_reasoning_effort = \"low\"\n\n[scan]\nroot = \"/src\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SaveAgentReasoningEffort("claude", "max",
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
		t.Fatalf("SaveAgentReasoningEffort returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# keep me", "# keep agent note", `command = "claude"`, `claude_reasoning_effort = "max"`, `root = "/src"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected saved config to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "codex_reasoning_effort") {
		t.Fatalf("claude save should not add codex effort key, got:\n%s", text)
	}
}

func TestSaveAgentReasoningEffort_RejectsUnsupportedEffort(t *testing.T) {
	err := config.SaveAgentReasoningEffort("codex", "max",
		config.WithGetenv(func(string) string { return t.TempDir() }),
		config.WithHomeDir(func() (string, error) { return t.TempDir(), nil }),
	)
	if err == nil {
		t.Fatal("expected unsupported effort error")
	}
	if !strings.Contains(err.Error(), "unsupported reasoning effort") {
		t.Fatalf("expected unsupported effort error, got %q", err.Error())
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
