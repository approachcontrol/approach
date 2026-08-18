package actions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	cursorSessionHookMarker = "session-hook --provider cursor-agent"
	cursorHooksFileName     = "hooks.json"
)

func cursorSessionHookCommand() string {
	return `sh -c 'if [ -z "${APPROACH_LAUNCH_ID:-}" ]; then exit 0; fi; exec "${APPROACH_EXECUTABLE:-approach}" session-hook --provider cursor-agent'`
}

func cursorHooksPath(home string) string {
	return filepath.Join(home, ".cursor", cursorHooksFileName)
}

// ensureCursorSessionHook merges Approach's managed Cursor stop hook into
// ~/.cursor/hooks.json without clobbering other user hooks. The hook no-ops
// unless APPROACH_LAUNCH_ID is set, so non-Approach Cursor sessions are
// untouched.
func ensureCursorSessionHook(home string) error {
	home = strings.TrimSpace(home)
	if home == "" {
		return fmt.Errorf("cursor-agent session hook requires HOME")
	}
	path := cursorHooksPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create cursor hooks dir: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read cursor hooks.json: %w", err)
	}
	merged, err := mergeCursorSessionHook(data, cursorSessionHookCommand())
	if err != nil {
		return err
	}
	if bytes.Equal(data, merged) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hooks.json-*.tmp")
	if err != nil {
		return fmt.Errorf("create cursor hooks temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write cursor hooks temp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod cursor hooks temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close cursor hooks temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install cursor hooks.json: %w", err)
	}
	return nil
}

func mergeCursorSessionHook(data []byte, command string) ([]byte, error) {
	root := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, fmt.Errorf("parse cursor hooks.json: %w", err)
		}
	}
	if _, ok := root["version"]; !ok {
		raw, err := json.Marshal(1)
		if err != nil {
			return nil, err
		}
		root["version"] = raw
	}
	hooks := map[string][]map[string]any{}
	if raw, ok := root["hooks"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse cursor hooks.json hooks: %w", err)
		}
	}
	stop := hooks["stop"]
	found := false
	for i, hook := range stop {
		cmd, _ := hook["command"].(string)
		if strings.Contains(cmd, cursorSessionHookMarker) {
			hook["command"] = command
			stop[i] = hook
			found = true
			break
		}
	}
	if !found {
		stop = append(stop, map[string]any{"command": command})
	}
	hooks["stop"] = stop
	raw, err := json.Marshal(hooks)
	if err != nil {
		return nil, err
	}
	root["hooks"] = raw
	merged, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	merged = append(merged, '\n')
	return merged, nil
}
