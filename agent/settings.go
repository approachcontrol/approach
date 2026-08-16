package agent

// Preferences is the full set of stored per-provider agent selections.
type Preferences struct {
	Command      string
	CodexModel   string
	ClaudeModel  string
	CursorModel  string
	CodexEffort  string
	ClaudeEffort string
}

// Settings is the single resolved selection for one launch or one phase.
type Settings struct {
	Command         string
	Model           string
	ReasoningEffort string
}

// Resolve picks the provider-specific model and reasoning effort for the
// selected command. The normalized command is echoed back for every input,
// including unsupported and empty ones, so callers can still report the
// command they were given; only the model and effort go empty for commands
// that carry no provider selection.
func Resolve(prefs Preferences) Settings {
	command := Normalize(prefs.Command)
	switch command {
	case CommandCodex:
		return Settings{
			Command:         command,
			Model:           prefs.CodexModel,
			ReasoningEffort: prefs.CodexEffort,
		}
	case CommandClaude:
		return Settings{
			Command:         command,
			Model:           prefs.ClaudeModel,
			ReasoningEffort: prefs.ClaudeEffort,
		}
	case CommandCursor:
		return Settings{
			Command:         command,
			Model:           prefs.CursorModel,
			ReasoningEffort: ReasoningEffortDefault,
		}
	default:
		return Settings{Command: command}
	}
}
