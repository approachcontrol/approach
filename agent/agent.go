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

const (
	ModelDefault       = "default"
	ModelGPT55         = "gpt-5.5"
	ModelGPT56Sol      = "gpt-5.6-sol"
	ModelClaudeOpus48  = "claude-opus-4-8"
	ModelClaudeOpus5   = "claude-opus-5"
	ModelClaudeSonnet5 = "claude-sonnet-5"
	ModelClaudeFable5  = "claude-fable-5"
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

func NormalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
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

func ModelChoices(command string) []string {
	switch Normalize(command) {
	case CommandCodex:
		return []string{ModelDefault, ModelGPT55, ModelGPT56Sol}
	case CommandClaude:
		return []string{ModelDefault, ModelClaudeOpus48, ModelClaudeOpus5, ModelClaudeSonnet5, ModelClaudeFable5}
	case CommandCodexApp:
		return []string{ModelDefault}
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

func ValidateModel(command, model string) error {
	command = Normalize(command)
	if err := Validate(command); err != nil {
		return err
	}
	model = NormalizeModel(model)
	if model == "" {
		return nil
	}
	for _, choice := range ModelChoices(command) {
		if model == choice {
			return nil
		}
	}
	return fmt.Errorf("unsupported model %q for %s", model, command)
}
