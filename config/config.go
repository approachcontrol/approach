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
	UI          UIConfig         `toml:"ui"`
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

// UIConfig stores user-interface preferences.
type UIConfig struct {
	DefaultView *int `toml:"default_view"`
}

// AgentConfig stores the user's preferred interactive coding agent.
type AgentConfig struct {
	Command               string `toml:"command"`
	PlanPrompt            string `toml:"plan_prompt"`
	CodexReasoningEffort  string `toml:"codex_reasoning_effort"`
	ClaudeReasoningEffort string `toml:"claude_reasoning_effort"`
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

	if cfg.UI.DefaultView != nil {
		if err := validateDefaultView(*cfg.UI.DefaultView); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	if cfg.Agent.Command != "" {
		cfg.Agent.Command = agent.Normalize(cfg.Agent.Command)
		if err := agent.Validate(cfg.Agent.Command); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if cfg.Agent.CodexReasoningEffort != "" {
		cfg.Agent.CodexReasoningEffort = agent.NormalizeReasoningEffort(cfg.Agent.CodexReasoningEffort)
		if err := agent.ValidateReasoningEffort(agent.CommandCodex, cfg.Agent.CodexReasoningEffort); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if cfg.Agent.ClaudeReasoningEffort != "" {
		cfg.Agent.ClaudeReasoningEffort = agent.NormalizeReasoningEffort(cfg.Agent.ClaudeReasoningEffort)
		if err := agent.ValidateReasoningEffort(agent.CommandClaude, cfg.Agent.ClaudeReasoningEffort); err != nil {
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

func validateDefaultView(view int) error {
	if view < 1 || view > 9 {
		return fmt.Errorf("ui.default_view must be between 1 and 9")
	}
	return nil
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

// SaveAgentReasoningEffort persists the selected provider-specific reasoning
// effort to wtui's default config file, creating the config directory when
// needed. An empty effort is saved as "default".
func SaveAgentReasoningEffort(command, effort string, options ...Option) error {
	command = agent.Normalize(command)
	if command != agent.CommandCodex && command != agent.CommandClaude {
		if err := agent.Validate(command); err != nil {
			return err
		}
		return fmt.Errorf("reasoning effort is configurable only for codex or claude")
	}
	effort = agent.NormalizeReasoningEffort(effort)
	if effort == "" {
		effort = agent.ReasoningEffortDefault
	}
	if err := agent.ValidateReasoningEffort(command, effort); err != nil {
		return err
	}

	opts := defaultOptions(options...)
	path, err := writableDefaultPath(opts)
	if err != nil {
		return err
	}
	return saveAgentReasoningEffortTo(path, command, effort, options...)
}

// SaveDefaultView persists the startup default view number to wtui's default
// config file, creating the config directory when needed.
func SaveDefaultView(view int, options ...Option) error {
	if err := validateDefaultView(view); err != nil {
		return err
	}

	opts := defaultOptions(options...)
	path, err := writableDefaultPath(opts)
	if err != nil {
		return err
	}
	return saveDefaultViewTo(path, view, options...)
}

// SavePromptTemplate persists a configurable prompt template to wtui's default
// config file. plan_prompt is stored under [agent]; Flow phase prompt keys are
// stored under [flow_prompts].
func SavePromptTemplate(section, key, value string, options ...Option) error {
	section, key, err := normalizePromptTemplateTarget(section, key)
	if err != nil {
		return err
	}

	opts := defaultOptions(options...)
	path, err := writableDefaultPath(opts)
	if err != nil {
		return err
	}
	return savePromptTemplateTo(path, section, key, value, options...)
}

// ResetPromptTemplate removes a configurable prompt template override from
// wtui's default config file. Missing assignments are treated as already reset.
func ResetPromptTemplate(section, key string, options ...Option) error {
	section, key, err := normalizePromptTemplateTarget(section, key)
	if err != nil {
		return err
	}

	opts := defaultOptions(options...)
	path, err := writableDefaultPath(opts)
	if err != nil {
		return err
	}
	return resetPromptTemplateTo(path, section, key, options...)
}

func normalizePromptTemplateTarget(section, key string) (string, string, error) {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	switch key {
	case "plan_prompt":
		if section != "" && section != "agent" {
			return "", "", fmt.Errorf("prompt template %s belongs to [agent]", key)
		}
		return "agent", key, nil
	case "plan", "plan_review", "implementation", "review_loop", "pr_creation", "autoreview", "merge", "generic":
		if section != "" && section != "flow_prompts" {
			return "", "", fmt.Errorf("prompt template %s belongs to [flow_prompts]", key)
		}
		return "flow_prompts", key, nil
	default:
		return "", "", fmt.Errorf("unsupported prompt template key %q", key)
	}
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
	return saveAgentConfigTo(path, options, func(data []byte) []byte {
		return patchAgentCommand(data, command)
	})
}

func saveAgentReasoningEffortTo(path, command, effort string, options ...Option) error {
	key := "codex_reasoning_effort"
	if command == agent.CommandClaude {
		key = "claude_reasoning_effort"
	}
	return saveAgentConfigTo(path, options, func(data []byte) []byte {
		return patchAgentReasoningEffort(data, key, effort)
	})
}

func saveDefaultViewTo(path string, view int, options ...Option) error {
	return saveAgentConfigTo(path, options, func(data []byte) []byte {
		return patchSectionAssignment(data, "ui", "default_view", fmt.Sprintf("default_view = %d\n", view))
	})
}

func savePromptTemplateTo(path, section, key, value string, options ...Option) error {
	return saveAgentConfigTo(path, options, func(data []byte) []byte {
		return patchSectionAssignment(data, section, key, fmt.Sprintf("%s = %s\n", key, escapeTOMLString(value)))
	})
}

func resetPromptTemplateTo(path, section, key string, options ...Option) error {
	opts := defaultOptions(options...)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if _, err := parseConfigData(path, data, opts); err != nil {
		return err
	}

	patched := removeSectionAssignment(data, section, key)
	if bytes.Equal(patched, data) {
		return nil
	}
	if err := os.WriteFile(path, patched, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func saveAgentConfigTo(path string, options []Option, patch func([]byte) []byte) error {
	opts := defaultOptions(options...)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read config %s: %w", path, err)
		}
	} else if _, err := parseConfigData(path, data, opts); err != nil {
		return err
	}

	data = patch(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func patchAgentCommand(data []byte, command string) []byte {
	return patchSectionAssignment(data, "agent", "command", agentCommandLine(command))
}

func patchAgentReasoningEffort(data []byte, key, effort string) []byte {
	return patchSectionAssignment(data, "agent", key, agentReasoningEffortLine(key, effort))
}

func patchSectionAssignment(data []byte, section, key, assignmentLine string) []byte {
	if len(data) == 0 {
		return []byte("[" + section + "]\n" + assignmentLine)
	}

	lines := strings.SplitAfter(string(data), "\n")
	inSection := false
	sectionHeader := -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if header, ok := tableHeaderName(trimmed); ok {
			if inSection {
				return []byte(strings.Join(insertLine(lines, i, assignmentLine), ""))
			}
			inSection = header == section
			if inSection {
				sectionHeader = i
			}
			continue
		}
		if inSection && isSectionAssignment(line, key) {
			end := sectionAssignmentEnd(lines, i)
			lines[i] = replaceSectionAssignment(line, assignmentLine)
			if end > i+1 {
				lines = append(lines[:i+1], lines[end:]...)
			}
			return []byte(strings.Join(lines, ""))
		}
		if inSection {
			if end := sectionAssignmentEnd(lines, i); end > i+1 {
				i = end - 1
			}
		}
	}

	if inSection {
		return []byte(strings.Join(insertLine(lines, sectionHeader+1, assignmentLine), ""))
	}

	text := string(data)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if strings.TrimSpace(text) != "" && !strings.HasSuffix(text, "\n\n") {
		text += "\n"
	}
	return []byte(text + "[" + section + "]\n" + assignmentLine)
}

func removeSectionAssignment(data []byte, section, key string) []byte {
	if len(data) == 0 {
		return data
	}

	lines := strings.SplitAfter(string(data), "\n")
	inSection := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if header, ok := tableHeaderName(trimmed); ok {
			if inSection {
				return data
			}
			inSection = header == section
			continue
		}
		if inSection && isSectionAssignment(line, key) {
			end := sectionAssignmentEnd(lines, i)
			lines = append(lines[:i], lines[end:]...)
			return []byte(strings.Join(lines, ""))
		}
		if inSection {
			if end := sectionAssignmentEnd(lines, i); end > i+1 {
				i = end - 1
			}
		}
	}
	return data
}

func tableHeaderName(line string) (string, bool) {
	if strings.HasPrefix(line, "[[") {
		end := strings.Index(line, "]]")
		if end == -1 {
			return "", false
		}
		tail := strings.TrimSpace(line[end+2:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", false
		}
		name := strings.TrimSpace(line[2:end])
		if name == "" {
			return "", false
		}
		return "[[" + name + "]]", true
	}
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	end := strings.Index(line, "]")
	if end == -1 {
		return "", false
	}
	tail := strings.TrimSpace(line[end+1:])
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return "", false
	}
	name := strings.TrimSpace(line[1:end])
	if name == "" {
		return "", false
	}
	return name, true
}

func isSectionAssignment(line, key string) bool {
	eq := strings.Index(line, "=")
	if eq == -1 {
		return false
	}
	return strings.TrimSpace(line[:eq]) == key
}

func replaceSectionAssignment(line, assignmentLine string) string {
	body := strings.TrimRight(line, "\r\n")
	ending := line[len(body):]
	indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	return indent + strings.TrimSuffix(assignmentLine, "\n") + ending
}

func sectionAssignmentEnd(lines []string, start int) int {
	if start < 0 || start >= len(lines) {
		return start + 1
	}
	line := strings.TrimRight(lines[start], "\r\n")
	eq := strings.Index(line, "=")
	if eq == -1 {
		return start + 1
	}
	value := strings.TrimSpace(line[eq+1:])
	delim, ok := multilineStringDelimiter(value)
	if !ok {
		return start + 1
	}
	if strings.Contains(value[len(delim):], delim) {
		return start + 1
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], delim) {
			return i + 1
		}
	}
	return len(lines)
}

func multilineStringDelimiter(value string) (string, bool) {
	switch {
	case strings.HasPrefix(value, `"""`):
		return `"""`, true
	case strings.HasPrefix(value, `'''`):
		return `'''`, true
	default:
		return "", false
	}
}

func agentCommandLine(command string) string {
	return fmt.Sprintf("command = %q\n", command)
}

func agentReasoningEffortLine(key, effort string) string {
	return fmt.Sprintf("%s = %q\n", key, effort)
}

func escapeTOMLString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
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
