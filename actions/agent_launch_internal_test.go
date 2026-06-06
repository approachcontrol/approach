package actions

import (
	"encoding/json"
	"testing"
)

func TestClaudeSessionHookSettingsEncodesJSONString(t *testing.T) {
	hookCommand := "/tmp/wtui\a\v session-hook --provider claude"

	settings := claudeSessionHookSettings(hookCommand)

	var decoded struct {
		Hooks struct {
			SessionEnd []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionEnd"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &decoded); err != nil {
		t.Fatalf("settings is not valid JSON: %v\n%s", err, settings)
	}
	if got := decoded.Hooks.SessionEnd[0].Hooks[0].Command; got != hookCommand {
		t.Fatalf("command = %q, want %q", got, hookCommand)
	}
}
