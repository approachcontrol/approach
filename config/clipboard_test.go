package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/config"
)

func TestLoadFromClipboardConfig(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantMethod string
		wantLimit  int
		wantError  string
	}{
		{name: "omitted defaults", wantMethod: config.ClipboardMethodAuto, wantLimit: 100_000},
		{name: "auto", body: "[clipboard]\nmethod = \"auto\"\n", wantMethod: config.ClipboardMethodAuto, wantLimit: 100_000},
		{name: "system normalized", body: "[clipboard]\nmethod = \"  SYSTEM  \"\n", wantMethod: config.ClipboardMethodSystem, wantLimit: 100_000},
		{name: "osc52", body: "[clipboard]\nmethod = \"osc52\"\n", wantMethod: config.ClipboardMethodOSC52, wantLimit: 100_000},
		{name: "zero is unlimited", body: "[clipboard]\nosc52_max_payload_bytes = 0\n", wantMethod: config.ClipboardMethodAuto, wantLimit: 0},
		{name: "custom limit", body: "[clipboard]\nosc52_max_payload_bytes = 4096\n", wantMethod: config.ClipboardMethodAuto, wantLimit: 4096},
		{name: "invalid method", body: "[clipboard]\nmethod = \"terminal\"\n", wantError: "clipboard.method"},
		{name: "negative limit", body: "[clipboard]\nosc52_max_payload_bytes = -1\n", wantError: "clipboard.osc52_max_payload_bytes must be >= 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if tt.body != "" {
				if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.LoadFrom(path)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("LoadFrom error = %v, want text %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFrom returned error: %v", err)
			}
			if cfg.Clipboard.Method != tt.wantMethod || cfg.Clipboard.OSC52MaxPayloadBytes != tt.wantLimit {
				t.Fatalf("clipboard config = %#v, want method %q limit %d", cfg.Clipboard, tt.wantMethod, tt.wantLimit)
			}
		})
	}
}
