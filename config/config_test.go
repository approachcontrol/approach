package config_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/config"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

func TestConfigMutationReportsBoundedLockTimeout(t *testing.T) {
	configHome := t.TempDir()
	path := filepath.Join(configHome, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	getenv := func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return configHome
		}
		return ""
	}
	release, err := artifacts.AcquireFileLock(path+".lock", "test config lock", time.Second)
	if err != nil {
		t.Fatalf("hold config lock: %v", err)
	}
	defer release()
	err = config.SaveAgentCommand("codex", config.WithGetenv(getenv), config.WithLockTimeout(10*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for config lock") {
		t.Fatalf("SaveAgentCommand() error = %v, want bounded config lock timeout", err)
	}
}

func TestLoadFrom_AllowsMissingConfig(t *testing.T) {
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("LoadFrom returned error for missing config: %v", err)
	}

	if cfg.Scan.Root != "" {
		t.Fatalf("expected empty scan root, got %q", cfg.Scan.Root)
	}
	if cfg.Launch.Backend != config.LaunchBackendEmbedded {
		t.Fatalf("expected embedded launch backend by default, got %q", cfg.Launch.Backend)
	}
}

func TestLoadFrom_ParsesLaunchBackend(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "omitted", body: "[launch]\nprefer_multiplexer = true\n", want: config.LaunchBackendEmbedded},
		{name: "empty", body: "[launch]\nbackend = \"\"\n", want: config.LaunchBackendEmbedded},
		{name: "embedded", body: "[launch]\nbackend = \"embedded\"\n", want: config.LaunchBackendEmbedded},
		{name: "tmux", body: "[launch]\nbackend = \"tmux\"\n", want: config.LaunchBackendTmux},
		{name: "normalized", body: "[launch]\nbackend = \"  TMUX \"\n", want: config.LaunchBackendTmux},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, err := config.LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom returned error: %v", err)
			}
			if cfg.Launch.Backend != tt.want {
				t.Fatalf("expected launch backend %q, got %q", tt.want, cfg.Launch.Backend)
			}
		})
	}
}

func TestLoadFrom_RejectsUnknownLaunchBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[launch]\nbackend = \"zellij\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadFrom(path)
	if err == nil {
		t.Fatal("expected unknown launch backend error")
	}
	if !strings.Contains(err.Error(), "launch.backend") {
		t.Fatalf("expected error to mention launch.backend, got %q", err.Error())
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

[ui]
default_view = 8

[agent]
command = "codex"
plan_prompt = "Implement {title} from {plan_path}"
codex_model = " GPT-5.5 "
claude_model = "claude-fable-5"
cursor_model = " Composer-2.5 "
codex_reasoning_effort = " HIGH "
claude_reasoning_effort = "max"

[flow_prompts]
plan = "Plan only: {instructions}"
implementation = "Implement {plan_path} in {worktree_path}"
autoreview = "Review {pr_url} and ship fixes"

[flow]
preset = "research"

[[flow.presets]]
name = "research"

[[flow.presets.phases]]
id = "research"
title = "Research"
kind = "plan"

[[flow.presets.phases]]
id = "build"
title = "Build"
kind = " implementation "
depends_on = ["research"]

[sessions]
root = "~/state/approach/sessions"
copy_raw_transcripts = false

[bootstrap]
timeout_seconds = 180

[[bootstrap.hooks]]
repo_path = "~/approach"
script = ".approach/bootstrap"

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
	if cfg.Agent.CodexModel != "gpt-5.5" {
		t.Fatalf("expected normalized codex model gpt-5.5, got %q", cfg.Agent.CodexModel)
	}
	if cfg.Agent.ClaudeModel != "claude-fable-5" {
		t.Fatalf("expected claude model claude-fable-5, got %q", cfg.Agent.ClaudeModel)
	}
	if cfg.Agent.CursorModel != "composer-2.5" {
		t.Fatalf("expected normalized cursor model composer-2.5, got %q", cfg.Agent.CursorModel)
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
	if cfg.Flow.Preset != "research" {
		t.Fatalf("expected flow preset research, got %q", cfg.Flow.Preset)
	}
	if len(cfg.Flow.Presets) != 1 {
		t.Fatalf("expected one flow preset, got %#v", cfg.Flow.Presets)
	}
	preset := cfg.Flow.Presets[0]
	if preset.Name != "research" {
		t.Fatalf("expected normalized preset name research, got %q", preset.Name)
	}
	if len(preset.Phases) != 2 {
		t.Fatalf("expected two preset phases, got %#v", preset.Phases)
	}
	if preset.Phases[1].Kind != flowstore.KindImplementation {
		t.Fatalf("expected normalized phase kind %q, got %q", flowstore.KindImplementation, preset.Phases[1].Kind)
	}
	if got := preset.Phases[1].DependsOn; len(got) != 1 || got[0] != "research" {
		t.Fatalf("expected build to depend on research, got %#v", got)
	}
	if cfg.Sessions.Root != filepath.Join(home, "state", "approach", "sessions") {
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
	if cfg.Bootstrap.Hooks[0].RepoPath != filepath.Join(home, "approach") {
		t.Fatalf("expected expanded repo path, got %q", cfg.Bootstrap.Hooks[0].RepoPath)
	}
	if cfg.Bootstrap.Hooks[0].Script != ".approach/bootstrap" {
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
repo_path = "/dev/approach"
script = ".approach/bootstrap"
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

func TestLoadFrom_ParsesFlowDefaultPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[flow]\npreset = \"default\"\nunknown = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Flow.Preset != "default" {
		t.Fatalf("expected default flow preset, got %q", cfg.Flow.Preset)
	}
}

func TestLoadFrom_RejectsInvalidFlowConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "wrong preset type",
			body: "[flow]\npreset = 3\n",
			want: "toml",
		},
		{
			name: "undefined default preset",
			body: "[flow]\npreset = \"missing\"\n",
			want: `flow.preset "missing" is not a defined preset`,
		},
		{
			name: "invalid graph",
			body: `
[[flow.presets]]
name = "research"

[[flow.presets.phases]]
id = "a"
title = "A"
depends_on = ["b"]

[[flow.presets.phases]]
id = "b"
title = "B"
depends_on = ["a"]
`,
			want: `flow.presets["research"]:`,
		},
		{
			name: "duplicate preset name",
			body: `
[[flow.presets]]
name = " Research "

[[flow.presets.phases]]
id = "a"
title = "A"

[[flow.presets]]
name = "research"

[[flow.presets.phases]]
id = "b"
title = "B"
`,
			want: `duplicate preset name "research"`,
		},
		{
			name: "reserved default preset",
			body: `
[[flow.presets]]
name = " default "

[[flow.presets.phases]]
id = "a"
title = "A"
`,
			want: `preset name "default" is reserved`,
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
				t.Fatal("expected invalid flow config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to contain %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestLoadFrom_IgnoresLegacyDefaultViewAssignments(t *testing.T) {
	for _, body := range []string{"[ui]\ndefault_view = 0\n", "[ui]\ndefault_view = 99\n", "[ui]\ndefault_view = \"flows\"\n", "[ui]\ndefault_view = 8.5\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFrom(path); err != nil {
			t.Fatalf("LoadFrom legacy default_view returned error: %v", err)
		}
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
	if err := os.WriteFile(path, []byte("[sessions]\nroot = \".approach-sessions\"\n"), 0o644); err != nil {
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

func TestLoadFrom_AllowsUnknownBootstrapFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[bootstrap]\ntimeout_seconds = 120\ntimeout = 300\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Bootstrap.TimeoutSeconds != 120 {
		t.Fatalf("expected known bootstrap timeout to parse, got %d", cfg.Bootstrap.TimeoutSeconds)
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
			body: "[[bootstrap.hooks]]\nscript = \".approach/bootstrap\"\n",
			want: "repo_path",
		},
		{
			name: "blank repo path",
			body: "[[bootstrap.hooks]]\nrepo_path = \"   \"\nscript = \".approach/bootstrap\"\n",
			want: "repo_path",
		},
		{
			name: "missing script",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/approach\"\n",
			want: "script",
		},
		{
			name: "blank script",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/approach\"\nscript = \"   \"\n",
			want: "script",
		},
		{
			name: "negative section timeout",
			body: "[bootstrap]\ntimeout_seconds = -1\n",
			want: "timeout_seconds",
		},
		{
			name: "negative hook timeout",
			body: "[[bootstrap.hooks]]\nrepo_path = \"/dev/approach\"\nscript = \".approach/bootstrap\"\ntimeout_seconds = -1\n",
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

	path := filepath.Join(xdg, "approach", "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "claude" {
		t.Fatalf("expected saved agent claude, got %q", cfg.Agent.Command)
	}
	for checkedPath, want := range map[string]os.FileMode{
		filepath.Dir(path): 0o700,
		path:               0o600,
	} {
		info, err := os.Stat(checkedPath)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", checkedPath, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", checkedPath, got, want)
		}
	}
}

func TestLoadFrom_NormalizesStoredCodexAppAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \" CoDeX-App \"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("expected retired agent to normalize to codex, got %q", cfg.Agent.Command)
	}
}

func TestLoadFrom_AcceptsCodexMinimalReasoningEffort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncodex_reasoning_effort = \" minimal \"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.CodexReasoningEffort != "minimal" {
		t.Fatalf("expected normalized codex reasoning effort minimal, got %q", cfg.Agent.CodexReasoningEffort)
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

func TestLoadFrom_RejectsInvalidModels(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "codex claude model",
			body: "[agent]\ncodex_model = \"claude-sonnet-5\"\n",
			want: "unsupported model",
		},
		{
			name: "claude unknown",
			body: "[agent]\nclaude_model = \"turbo\"\n",
			want: "unsupported model",
		},
		{
			name: "cursor unknown",
			body: "[agent]\ncursor_model = \"turbo\"\n",
			want: "unsupported model",
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
				t.Fatal("expected invalid model error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to mention %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func TestSaveAgentCommand_RepairsPermissiveConfigOnNoOp(t *testing.T) {
	configHome := t.TempDir()
	configDir := filepath.Join(configHome, "approach")
	path := filepath.Join(configDir, "config.toml")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAgentCommand("codex", config.WithGetenv(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return configHome
		}
		return ""
	})); err != nil {
		t.Fatalf("SaveAgentCommand() error = %v", err)
	}
	for checkedPath, want := range map[string]os.FileMode{configDir: 0o700, path: 0o600} {
		info, err := os.Stat(checkedPath)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", checkedPath, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", checkedPath, got, want)
		}
	}
}

func TestSaveAgentCommand_RejectsCodexAppWithoutWriting(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			xdg := t.TempDir()
			path := filepath.Join(xdg, "approach", "config.toml")
			before := []byte("# preserved\n[agent]\ncommand = \"claude\"\n")
			if existing {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, before, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			err := config.SaveAgentCommand("codex-app",
				config.WithGetenv(func(key string) string {
					if key == "XDG_CONFIG_HOME" {
						return xdg
					}
					return ""
				}),
				config.WithHomeDir(func() (string, error) { return t.TempDir(), nil }),
			)
			want := `unsupported agent "codex-app"; choose codex, claude, or cursor-agent`
			if err == nil || err.Error() != want {
				t.Fatalf("SaveAgentCommand() error = %v, want %q", err, want)
			}
			after, readErr := os.ReadFile(path)
			if existing {
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(after, before) {
					t.Fatalf("config changed after rejected write:\n%s", after)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("missing config read error = %v, want not-exist", readErr)
			}
		})
	}
}

func TestSaveAgentCommand_PreservesExistingParsedSettings(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
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
	path := filepath.Join(xdg, "approach", "config.toml")
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

func TestSaveAgentCommand_UpdatesSymlinkTarget(t *testing.T) {
	xdg := t.TempDir()
	managed := filepath.Join(t.TempDir(), "managed-config.toml")
	if err := os.WriteFile(managed, []byte("[agent]\ncommand = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managed, path); err != nil {
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

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config.toml should remain a symlink")
	}
	raw, err := os.ReadFile(managed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `command = "claude"`) {
		t.Fatalf("managed target = %q, want updated command", raw)
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

	path := filepath.Join(xdg, "approach", "config.toml")
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
	path := filepath.Join(xdg, "approach", "config.toml")
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

func TestSaveAgentModel_CreatesMissingConfig(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentModel("codex", "gpt-5.5",
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
		t.Fatalf("SaveAgentModel returned error: %v", err)
	}

	path := filepath.Join(xdg, "approach", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `codex_model = "gpt-5.5"`) {
		t.Fatalf("expected codex model in saved config, got:\n%s", raw)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.CodexModel != "gpt-5.5" {
		t.Fatalf("expected saved codex model gpt-5.5, got %q", cfg.Agent.CodexModel)
	}
}

func TestSaveAgentModel_UpdatesExistingAgentSection(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "# keep me\n[agent]\n# keep agent note\ncommand = \"claude\"\nclaude_model = \"claude-opus-4-8\"\n\n[scan]\nroot = \"/src\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SaveAgentModel("claude", "claude-sonnet-5",
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
		t.Fatalf("SaveAgentModel returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# keep me", "# keep agent note", `command = "claude"`, `claude_model = "claude-sonnet-5"`, `root = "/src"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected saved config to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "codex_model") {
		t.Fatalf("claude save should not add codex model key, got:\n%s", text)
	}
}

func TestSaveAgentModel_PersistsEmptyAsDefault(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentModel("claude", "",
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
		t.Fatalf("SaveAgentModel returned error: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(xdg, "approach", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `claude_model = "default"`) {
		t.Fatalf("expected default claude model in saved config, got:\n%s", raw)
	}
}

func TestSaveAgentModel_RejectsUnsupportedModel(t *testing.T) {
	err := config.SaveAgentModel("codex", "claude-sonnet-5",
		config.WithGetenv(func(string) string { return t.TempDir() }),
		config.WithHomeDir(func() (string, error) { return t.TempDir(), nil }),
	)
	if err == nil {
		t.Fatal("expected unsupported model error")
	}
	if !strings.Contains(err.Error(), "unsupported model") {
		t.Fatalf("expected unsupported model error, got %q", err.Error())
	}
}

func TestSaveAgentModel_PersistsCursorModel(t *testing.T) {
	xdg := t.TempDir()
	err := config.SaveAgentModel("cursor-agent", "composer-2.5",
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
		t.Fatalf("SaveAgentModel returned error: %v", err)
	}

	path := filepath.Join(xdg, "approach", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `cursor_model = "composer-2.5"`) {
		t.Fatalf("expected cursor model in saved config, got:\n%s", raw)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.CursorModel != "composer-2.5" {
		t.Fatalf("expected saved cursor model composer-2.5, got %q", cfg.Agent.CursorModel)
	}
}

func TestSavePromptTemplate_RoundTripsEscapedMultilineTemplates(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	value := "Line 1\nLine 2 with \"quotes\" and \\ slash\tend\x01"

	err := config.SavePromptTemplate("flow_prompts", "plan", value,
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
		t.Fatalf("SavePromptTemplate returned error: %v", err)
	}

	path := filepath.Join(xdg, "approach", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `[flow_prompts]`) || !strings.Contains(text, `plan = "Line 1\nLine 2 with \"quotes\" and \\ slash\tend\u0001"`) {
		t.Fatalf("expected escaped single-line flow prompt, got:\n%s", text)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.FlowPrompts.Plan != value {
		t.Fatalf("saved flow prompt = %q, want %q", cfg.FlowPrompts.Plan, value)
	}
}

func TestSavePromptTemplate_ReplacesExistingMultilineStringAssignment(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "[flow_prompts]\nplan = \"\"\"old line 1\nold line 2\"\"\"\nimplementation = \"keep implementation\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SavePromptTemplate("flow_prompts", "plan", "new prompt",
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
		t.Fatalf("SavePromptTemplate returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "old line") || strings.Contains(text, `"""`) {
		t.Fatalf("expected old multiline prompt assignment fully replaced, got:\n%s", text)
	}
	if !strings.Contains(text, `plan = "new prompt"`) || !strings.Contains(text, `implementation = "keep implementation"`) {
		t.Fatalf("expected new prompt and sibling assignment preserved, got:\n%s", text)
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.FlowPrompts.Plan != "new prompt" || cfg.FlowPrompts.Implementation != "keep implementation" {
		t.Fatalf("loaded flow prompts = %#v", cfg.FlowPrompts)
	}
}

func TestSavePromptTemplate_SkipsOtherMultilineStringBodiesBeforeTarget(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "[flow_prompts]\nplan = \"\"\"\nDocument an example:\nimplementation = \"not the real assignment\"\n\"\"\"\nimplementation = \"old implementation\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.SavePromptTemplate("flow_prompts", "implementation", "new implementation",
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
		t.Fatalf("SavePromptTemplate returned error: %v", err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if !strings.Contains(cfg.FlowPrompts.Plan, `implementation = "not the real assignment"`) {
		t.Fatalf("plan prompt body was not preserved: %q", cfg.FlowPrompts.Plan)
	}
	if cfg.FlowPrompts.Implementation != "new implementation" {
		t.Fatalf("implementation prompt = %q, want new implementation", cfg.FlowPrompts.Implementation)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `implementation = "not the real assignment"`) ||
		!strings.Contains(text, `implementation = "new implementation"`) ||
		strings.Contains(text, `implementation = "old implementation"`) {
		t.Fatalf("expected multiline body preserved and real assignment replaced, got:\n%s", text)
	}
}

func TestResetPromptTemplate_RemovesOnlySelectedAssignment(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "# keep me\n[agent]\nplan_prompt = \"custom plan\"\ncommand = \"codex\"\n\n[flow_prompts]\nplan = \"custom flow\"\nimplementation = \"keep implementation\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.ResetPromptTemplate("flow_prompts", "plan",
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
		t.Fatalf("ResetPromptTemplate returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# keep me", `plan_prompt = "custom plan"`, `command = "codex"`, `implementation = "keep implementation"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected reset config to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, `plan = "custom flow"`) {
		t.Fatalf("expected flow plan prompt removed, got:\n%s", text)
	}
}

func TestResetPromptTemplate_RemovesExistingMultilineStringAssignment(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "[flow_prompts]\nplan = '''old line 1\nold line 2'''\nimplementation = \"keep implementation\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.ResetPromptTemplate("flow_prompts", "plan",
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
		t.Fatalf("ResetPromptTemplate returned error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "old line") || strings.Contains(text, `'''`) || strings.Contains(text, "plan =") {
		t.Fatalf("expected old multiline prompt assignment fully removed, got:\n%s", text)
	}
	if !strings.Contains(text, `implementation = "keep implementation"`) {
		t.Fatalf("expected sibling assignment preserved, got:\n%s", text)
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.FlowPrompts.Plan != "" || cfg.FlowPrompts.Implementation != "keep implementation" {
		t.Fatalf("loaded flow prompts = %#v", cfg.FlowPrompts)
	}
}

func TestResetPromptTemplate_SkipsOtherMultilineStringBodiesBeforeTarget(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := "[flow_prompts]\nplan = '''\nDocument an example:\nimplementation = \"not the real assignment\"\n'''\nimplementation = \"old implementation\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.ResetPromptTemplate("flow_prompts", "implementation",
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
		t.Fatalf("ResetPromptTemplate returned error: %v", err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if !strings.Contains(cfg.FlowPrompts.Plan, `implementation = "not the real assignment"`) {
		t.Fatalf("plan prompt body was not preserved: %q", cfg.FlowPrompts.Plan)
	}
	if cfg.FlowPrompts.Implementation != "" {
		t.Fatalf("implementation prompt = %q, want reset default", cfg.FlowPrompts.Implementation)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `implementation = "not the real assignment"`) ||
		strings.Contains(text, `implementation = "old implementation"`) {
		t.Fatalf("expected multiline body preserved and real assignment removed, got:\n%s", text)
	}
}

func TestResetPromptTemplate_MissingConfigDoesNotCreateFile(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()

	err := config.ResetPromptTemplate("agent", "plan_prompt",
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
		t.Fatalf("ResetPromptTemplate returned error: %v", err)
	}

	path := filepath.Join(xdg, "approach", "config.toml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reset missing config should not create %s, stat err=%v", path, err)
	}
}

func TestSaveAgentCommand_UpdatesExistingFallbackConfig(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	homePath := filepath.Join(home, ".config", "approach", "config.toml")
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

	if _, err := os.Stat(filepath.Join(xdg, "approach", "config.toml")); !os.IsNotExist(err) {
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

func TestLoadFrom_AllowsUnknownAgentFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \"codex\"\nfuture_model = \"gpt-next\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.Command != "codex" {
		t.Fatalf("expected known agent command to parse, got %q", cfg.Agent.Command)
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

func TestLoadFrom_AllowsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[scan]\nmax_depth = 1\nroto = \"~/src\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Scan.MaxDepth != 1 {
		t.Fatalf("expected known scan max_depth to parse, got %d", cfg.Scan.MaxDepth)
	}
}

func TestLoadFrom_AllowsUnknownSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[future]\nenabled = true\n[scan]\nmax_depth = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Scan.MaxDepth != 1 {
		t.Fatalf("expected known scan max_depth to parse, got %d", cfg.Scan.MaxDepth)
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
	xdgConfig := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(xdgConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xdgConfig, []byte("[scan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	homeConfig := filepath.Join(home, ".config", "approach", "config.toml")
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
	homeConfig := filepath.Join(home, ".config", "approach", "config.toml")
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

	if path != filepath.Join("/xdg", "approach", "config.toml") {
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

	if path != filepath.Join("/home/user", ".config", "approach", "config.toml") {
		t.Fatalf("unexpected config path %q", path)
	}
}
