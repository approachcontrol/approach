package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ValidateTranscriptPath canonicalizes an existing provider transcript and
// rejects paths outside that provider's configured transcript root.
//
// Hook payloads name arbitrary paths, so canonicalization (symlink
// resolution plus containment against the provider root — Codex
// $CODEX_HOME/sessions, Claude $CLAUDE_CONFIG_DIR/projects, and Cursor's chats
// or per-project agent-transcripts directory) is what keeps a crafted payload
// from making Approach ingest files outside those roots.
func ValidateTranscriptPath(provider Provider, path string, env map[string]string) (string, error) {
	canonicalPath, _, err := validateTranscriptPath(provider, path, env)
	return canonicalPath, err
}

func validateTranscriptPath(provider Provider, path string, env map[string]string) (string, os.FileInfo, error) {
	if path == "" {
		return "", nil, nil
	}
	if !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("provider transcript path must be absolute: %q", path)
	}
	roots, err := providerTranscriptRoots(provider, path, env)
	if err != nil {
		return "", nil, err
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return "", nil, fmt.Errorf("%s transcript root must be absolute: %q", provider, root)
		}
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve provider transcript %q: %w", path, err)
	}
	contained := false
	for _, root := range roots {
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if pathWithinRoot(canonicalRoot, canonicalPath) {
			contained = true
			break
		}
	}
	if !contained {
		return "", nil, fmt.Errorf("provider transcript %q is outside expected %s transcript roots", path, provider)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return "", nil, fmt.Errorf("stat provider transcript %q: %w", canonicalPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("provider transcript %q is not a regular file", canonicalPath)
	}
	return canonicalPath, info, nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func openValidatedTranscript(provider Provider, path string, env map[string]string) (*os.File, string, error) {
	canonicalPath, validatedInfo, err := validateTranscriptPath(provider, path, env)
	if err != nil {
		return nil, "", err
	}
	file, err := openValidatedTranscriptFile(canonicalPath, validatedInfo)
	if err != nil {
		return nil, "", err
	}
	return file, canonicalPath, nil
}

func openValidatedTranscriptFile(path string, validatedInfo os.FileInfo) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open validated provider transcript %q: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat open provider transcript %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("provider transcript %q is not a regular file", path)
	}
	if validatedInfo == nil || !os.SameFile(validatedInfo, info) {
		file.Close()
		return nil, fmt.Errorf("provider transcript %q changed after validation", path)
	}
	return file, nil
}

func providerTranscriptRoots(provider Provider, path string, env map[string]string) ([]string, error) {
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
			return []string{filepath.Join(codexHome, "sessions")}, nil
		}
		if home == "" {
			return nil, fmt.Errorf("resolve Codex transcript root: HOME is unset")
		}
		return []string{filepath.Join(home, ".codex", "sessions")}, nil
	case ProviderClaude:
		if claudeConfigDir := value("CLAUDE_CONFIG_DIR"); claudeConfigDir != "" {
			return []string{filepath.Join(claudeConfigDir, "projects")}, nil
		}
		if home == "" {
			return nil, fmt.Errorf("resolve Claude transcript root: HOME is unset")
		}
		return []string{filepath.Join(home, ".claude", "projects")}, nil
	case ProviderCursor:
		if home == "" {
			return nil, fmt.Errorf("resolve Cursor transcript root: HOME is unset")
		}
		cursorRoot := filepath.Join(home, ".cursor")
		if canonicalCursorRoot, err := filepath.EvalSymlinks(cursorRoot); err == nil {
			cursorRoot = canonicalCursorRoot
		}
		candidatePath := path
		if canonicalPath, err := filepath.EvalSymlinks(path); err == nil {
			candidatePath = canonicalPath
		}
		roots := []string{filepath.Join(cursorRoot, "chats")}
		projectsRoot := filepath.Join(cursorRoot, "projects")
		if pathWithinRoot(projectsRoot, candidatePath) {
			rel, err := filepath.Rel(projectsRoot, candidatePath)
			if err == nil {
				parts := strings.Split(rel, string(filepath.Separator))
				if len(parts) >= 3 && parts[0] != "" && parts[1] == "agent-transcripts" {
					roots = append(roots, filepath.Join(projectsRoot, parts[0], "agent-transcripts"))
				}
			}
		}
		return roots, nil
	default:
		return nil, fmt.Errorf("unsupported session provider %q", provider)
	}
}
