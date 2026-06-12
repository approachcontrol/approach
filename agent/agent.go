package agent

import (
	"fmt"
	"strings"
)

const (
	CommandCodex    = "codex"
	CommandCodexApp = "codex-app"
	CommandClaude   = "claude"
)

const (
	ReasoningEffortDefault = "default"
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)

func Normalize(command string) string {
	return strings.ToLower(strings.TrimSpace(command))
}

func Supported(command string) bool {
	switch Normalize(command) {
	case CommandCodex, CommandCodexApp, CommandClaude:
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
		return fmt.Errorf("unsupported agent %q; choose codex, codex-app, or claude", command)
	}
	return nil
}

func NormalizeReasoningEffort(effort string) string {
	return strings.ToLower(strings.TrimSpace(effort))
}

func ReasoningEffortChoices(command string) []string {
	switch Normalize(command) {
	case CommandCodex:
		return []string{ReasoningEffortDefault, ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh}
	case CommandClaude:
		return []string{ReasoningEffortDefault, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax}
	case CommandCodexApp:
		return []string{ReasoningEffortDefault}
	default:
		return nil
	}
}

func ValidateReasoningEffort(command, effort string) error {
	command = Normalize(command)
	if err := Validate(command); err != nil {
		return err
	}
	effort = NormalizeReasoningEffort(effort)
	if effort == "" {
		return nil
	}
	for _, choice := range ReasoningEffortChoices(command) {
		if effort == choice {
			return nil
		}
	}
	return fmt.Errorf("unsupported reasoning effort %q for %s", effort, command)
}
