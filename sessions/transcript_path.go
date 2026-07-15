package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateTranscriptPath canonicalizes an existing provider transcript and
// rejects paths outside that provider's configured transcript root.
func ValidateTranscriptPath(provider Provider, path string, env map[string]string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("provider transcript path must be absolute: %q", path)
	}
	root, err := providerTranscriptRoot(provider, env)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s transcript root must be absolute: %q", provider, root)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s transcript root %q: %w", provider, root, err)
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve provider transcript %q: %w", path, err)
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalPath)
	if err != nil {
		return "", fmt.Errorf("check provider transcript containment: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("provider transcript %q is outside expected %s root %q", path, provider, canonicalRoot)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return "", fmt.Errorf("stat provider transcript %q: %w", canonicalPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("provider transcript %q is not a regular file", canonicalPath)
	}
	return canonicalPath, nil
}

func providerTranscriptRoot(provider Provider, env map[string]string) (string, error) {
	value := func(key string) string {
		if env != nil {
			if value, ok := env[key]; ok {
				return value
			}
		}
		return os.Getenv(key)
	}
	home := value("HOME")
	switch provider {
	case ProviderCodex:
		if codexHome := value("CODEX_HOME"); codexHome != "" {
			return filepath.Join(codexHome, "sessions"), nil
		}
		if home == "" {
			return "", fmt.Errorf("resolve Codex transcript root: HOME is unset")
		}
		return filepath.Join(home, ".codex", "sessions"), nil
	case ProviderClaude:
		if claudeConfigDir := value("CLAUDE_CONFIG_DIR"); claudeConfigDir != "" {
			return filepath.Join(claudeConfigDir, "projects"), nil
		}
		if home == "" {
			return "", fmt.Errorf("resolve Claude transcript root: HOME is unset")
		}
		return filepath.Join(home, ".claude", "projects"), nil
	default:
		return "", fmt.Errorf("unsupported session provider %q", provider)
	}
}
