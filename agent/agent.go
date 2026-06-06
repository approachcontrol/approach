package agent

import (
	"fmt"
	"strings"
)

const (
	CommandCodex  = "codex"
	CommandClaude = "claude"
)

func Normalize(command string) string {
	return strings.ToLower(strings.TrimSpace(command))
}

func Supported(command string) bool {
	switch Normalize(command) {
	case CommandCodex, CommandClaude:
		return true
	default:
		return false
	}
}

func Validate(command string) error {
	if Normalize(command) == "" {
		return fmt.Errorf("agent is not set")
	}
	if !Supported(command) {
		return fmt.Errorf("unsupported agent %q; choose codex or claude", command)
	}
	return nil
}
