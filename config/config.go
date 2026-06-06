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
	Scan     ScanConfig     `toml:"scan"`
	Editor   EditorConfig   `toml:"editor"`
	Terminal TerminalConfig `toml:"terminal"`
	Provider ProviderConfig `toml:"provider"`
	Launch   LaunchConfig   `toml:"launch"`
	Agent    AgentConfig    `toml:"agent"`
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
	Command string `toml:"command"`
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
	return Config{}, nil
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
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Scan.MaxDepth < 0 {
		return Config{}, false, fmt.Errorf("parse config %s: scan.max_depth must be >= 0", path)
	}

	if cfg.Scan.Root != "" {
		root, err := expandHome(cfg.Scan.Root, opts.homeDir)
		if err != nil {
			return Config{}, false, fmt.Errorf("expand scan root in config %s: %w", path, err)
		}
		cfg.Scan.Root = root
	}

	if cfg.Agent.Command != "" {
		cfg.Agent.Command = agent.Normalize(cfg.Agent.Command)
		if err := agent.Validate(cfg.Agent.Command); err != nil {
			return Config{}, false, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	return cfg, true, nil
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
	cfg, err := LoadFrom(path, options...)
	if err != nil {
		return err
	}
	cfg.Agent.Command = command

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
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
