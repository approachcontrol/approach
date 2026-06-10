package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brian-bell/wtui/agent"
	"github.com/pelletier/go-toml/v2"
)

type getenvFunc func(string) string
type homeDirFunc func() (string, error)

// Config is wtui's parsed configuration file.
type Config struct {
	Scan        ScanConfig       `toml:"scan"`
	Editor      EditorConfig     `toml:"editor"`
	Terminal    TerminalConfig   `toml:"terminal"`
	Provider    ProviderConfig   `toml:"provider"`
	Launch      LaunchConfig     `toml:"launch"`
	Agent       AgentConfig      `toml:"agent"`
	FlowPrompts FlowPromptConfig `toml:"flow_prompts"`
	Sessions    SessionsConfig   `toml:"sessions"`
	Bootstrap   BootstrapConfig  `toml:"bootstrap"`
}

// ScanConfig configures repository discovery.
type ScanConfig struct {
	Root     string `toml:"root"`
	MaxDepth int    `toml:"max_depth"`
}

// EditorConfig is parsed now so editor behavior can be wired in later.
type EditorConfig struct {
	Command string `toml:"command"`
}

// TerminalConfig is parsed now so terminal behavior can be wired in later.
type TerminalConfig struct {
	Command string `toml:"command"`
}

// ProviderConfig is parsed now so provider-specific behavior can be wired in later.
type ProviderConfig struct {
	Name string `toml:"name"`
}

// LaunchConfig is parsed now so launch behavior can be wired in later.
type LaunchConfig struct {
	PreferMultiplexer bool `toml:"prefer_multiplexer"`
}

// AgentConfig stores the user's preferred interactive coding agent.
type AgentConfig struct {
	Command    string `toml:"command"`
	PlanPrompt string `toml:"plan_prompt"`
}

// FlowPromptConfig stores optional launch prompt templates for Flow phases.
type FlowPromptConfig struct {
	Plan           string `toml:"plan"`
	PlanReview     string `toml:"plan_review"`
	Implementation string `toml:"implementation"`
	ReviewLoop     string `toml:"review_loop"`
	PRCreation     string `toml:"pr_creation"`
	Autoreview     string `toml:"autoreview"`
	Merge          string `toml:"merge"`
	Generic        string `toml:"generic"`
}

// SessionsConfig controls agent-session capture storage.
type SessionsConfig struct {
	Root               string `toml:"root"`
	CopyRawTranscripts bool   `toml:"copy_raw_transcripts"`
}

// BootstrapConfig configures optional scripts that run after worktree creation.
type BootstrapConfig struct {
	TimeoutSeconds int                   `toml:"timeout_seconds"`
	Hooks          []BootstrapHookConfig `toml:"hooks"`
}

// BootstrapHookConfig maps one repository to its bootstrap script.
type BootstrapHookConfig struct {
	RepoPath       string `toml:"repo_path"`
	Script         string `toml:"script"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

type loadOptions struct {
	getenv  getenvFunc
	homeDir homeDirFunc
}

// Option customizes config loading. It is primarily useful in tests.
type Option func(*loadOptions)

// WithGetenv overrides environment lookup during config loading.
func WithGetenv(getenv func(string) string) Option {
	return func(opts *loadOptions) {
		opts.getenv = getenv
	}
}

// WithHomeDir overrides home directory lookup during config loading.
func WithHomeDir(homeDir func() (string, error)) Option {
	return func(opts *loadOptions) {
		opts.homeDir = homeDir
	}
}

// Load reads wtui's default config file.
func Load(options ...Option) (Config, error) {
	opts := defaultOptions(options...)
	paths, err := defaultPaths(opts)
	if err != nil {
		return Config{}, err
	}
	for _, path := range paths {
		cfg, found, err := loadPath(path, opts)
		if err != nil {
			return Config{}, err
		}
		if found {
			return cfg, nil
		}
	}
	return defaultConfig(), nil
}

// LoadFrom reads a config file from path. Missing files are allowed and return
// the default empty config.
func LoadFrom(path string, options ...Option) (Config, error) {
	opts := defaultOptions(options...)
	cfg, _, err := loadPath(path, opts)
	return cfg, err
}

func loadPath(path string, opts loadOptions) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), false, nil
		}
		return Config{}, false, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg, err := parseConfigData(path, data, opts)
	if err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func parseConfigData(path string, data []byte, opts loadOptions) (Config, error) {
	cfg := defaultConfig()
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Scan.MaxDepth < 0 {
		return Config{}, fmt.Errorf("parse config %s: scan.max_depth must be >= 0", path)
	}

	if cfg.Scan.Root != "" {
		root, err := expandHome(cfg.Scan.Root, opts.homeDir)
		if err != nil {
			return Config{}, fmt.Errorf("expand scan root in config %s: %w", path, err)
		}
		cfg.Scan.Root = root
	}

	if cfg.Agent.Command != "" {
		cfg.Agent.Command = agent.Normalize(cfg.Agent.Command)
		if err := agent.Validate(cfg.Agent.Command); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	if cfg.Sessions.Root != "" {
		root, err := expandHome(cfg.Sessions.Root, opts.homeDir)
		if err != nil {
			return Config{}, fmt.Errorf("expand sessions root in config %s: %w", path, err)
		}
		if !filepath.IsAbs(root) {
			return Config{}, fmt.Errorf("parse config %s: sessions.root must be absolute or start with ~", path)
		}
		cfg.Sessions.Root = root
	}

	if err := normalizeBootstrapConfig(path, &cfg.Bootstrap, opts); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultConfig() Config {
	return Config{}
}

func normalizeBootstrapConfig(path string, cfg *BootstrapConfig, opts loadOptions) error {
	if cfg.TimeoutSeconds < 0 {
		return fmt.Errorf("parse config %s: bootstrap.timeout_seconds must be >= 0", path)
	}
	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = 120
	}

	for i := range cfg.Hooks {
		hook := &cfg.Hooks[i]
		hook.RepoPath = strings.TrimSpace(hook.RepoPath)
		hook.Script = strings.TrimSpace(hook.Script)
		if hook.RepoPath == "" {
			return fmt.Errorf("parse config %s: bootstrap.hooks[%d].repo_path is required", path, i)
		}
		if hook.Script == "" {
			return fmt.Errorf("parse config %s: bootstrap.hooks[%d].script is required", path, i)
		}
		if hook.TimeoutSeconds < 0 {
			return fmt.Errorf("parse config %s: bootstrap.hooks[%d].timeout_seconds must be >= 0", path, i)
		}

		repoPath, err := expandHome(hook.RepoPath, opts.homeDir)
		if err != nil {
			return fmt.Errorf("expand bootstrap repo_path in config %s: %w", path, err)
		}
		hook.RepoPath = filepath.Clean(repoPath)

		script, err := expandHome(hook.Script, opts.homeDir)
		if err != nil {
			return fmt.Errorf("expand bootstrap script in config %s: %w", path, err)
		}
		hook.Script = script
	}
	return nil
}

// DefaultPath returns the default config path:
// $XDG_CONFIG_HOME/wtui/config.toml, or ~/.config/wtui/config.toml.
func DefaultPath(options ...Option) (string, error) {
	opts := defaultOptions(options...)
	paths, err := defaultPaths(opts)
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

// SaveAgentCommand persists the selected coding agent to wtui's default config
// file, creating the config directory when needed.
func SaveAgentCommand(command string, options ...Option) error {
	command = agent.Normalize(command)
	if err := agent.Validate(command); err != nil {
		return err
	}

	opts := defaultOptions(options...)
	path, err := writableDefaultPath(opts)
	if err != nil {
		return err
	}
	return saveAgentCommandTo(path, command, options...)
}

func writableDefaultPath(opts loadOptions) (string, error) {
	paths, err := defaultPaths(opts)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return path, nil
		}
	}
	return paths[0], nil
}

func saveAgentCommandTo(path, command string, options ...Option) error {
	opts := defaultOptions(options...)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config %s: %w", path, err)
		}
	} else if _, err := parseConfigData(path, data, opts); err != nil {
		return err
	}

	data = patchAgentCommand(data, command)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func patchAgentCommand(data []byte, command string) []byte {
	if len(data) == 0 {
		return []byte("[agent]\n" + agentCommandLine(command))
	}

	lines := strings.SplitAfter(string(data), "\n")
	inAgent := false
	agentHeader := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if isTableHeader(trimmed) {
			if inAgent {
				return []byte(strings.Join(insertLine(lines, i, agentCommandLine(command)), ""))
			}
			inAgent = trimmed == "[agent]"
			if inAgent {
				agentHeader = i
			}
			continue
		}
		if inAgent && isCommandAssignment(line) {
			lines[i] = replaceCommandAssignment(line, command)
			return []byte(strings.Join(lines, ""))
		}
	}

	if inAgent {
		return []byte(strings.Join(insertLine(lines, agentHeader+1, agentCommandLine(command)), ""))
	}

	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if strings.TrimSpace(text) != "" && !strings.HasSuffix(text, "\n\n") {
		text += "\n"
	}
	return []byte(text + "[agent]\n" + agentCommandLine(command))
}

func isTableHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

func isCommandAssignment(line string) bool {
	eq := strings.Index(line, "=")
	if eq == -1 {
		return false
	}
	return strings.TrimSpace(line[:eq]) == "command"
}

func replaceCommandAssignment(line, command string) string {
	body := strings.TrimRight(line, "\r\n")
	ending := line[len(body):]
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	return indent + strings.TrimSuffix(agentCommandLine(command), "\n") + ending
}

func agentCommandLine(command string) string {
	return fmt.Sprintf("command = %q\n", command)
}

func insertLine(lines []string, index int, line string) []string {
	if index > 0 && !strings.HasSuffix(lines[index-1], "\n") {
		lines[index-1] += "\n"
	}
	lines = append(lines, "")
	copy(lines[index+1:], lines[index:])
	lines[index] = line
	return lines
}

func defaultPaths(opts loadOptions) ([]string, error) {
	var paths []string
	if xdg := opts.getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "wtui", "config.toml"))
	}

	home, err := opts.homeDir()
	if err != nil {
		if len(paths) > 0 {
			return paths, nil
		}
		return nil, err
	}
	homePath := filepath.Join(home, ".config", "wtui", "config.toml")
	if len(paths) == 0 || paths[len(paths)-1] != homePath {
		paths = append(paths, homePath)
	}
	return paths, nil
}

func defaultOptions(options ...Option) loadOptions {
	opts := loadOptions{
		getenv:  os.Getenv,
		homeDir: os.UserHomeDir,
	}
	for _, option := range options {
		option(&opts)
	}
	return opts
}

func expandHome(path string, homeDir homeDirFunc) (string, error) {
	switch {
	case path == "~":
		return homeDir()
	case strings.HasPrefix(path, "~/"):
		home, err := homeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	default:
		return path, nil
	}
}
