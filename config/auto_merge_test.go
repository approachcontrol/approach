package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/config"
)

func TestFlowAutoMergeDefaultsOffAndLoadsExplicitValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.LoadFrom(path)
	if err != nil || cfg.Flow.AutoMerge {
		t.Fatalf("missing config = %#v, %v; want auto-merge off", cfg.Flow, err)
	}
	if err := os.WriteFile(path, []byte("[flow]\nauto_merge = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadFrom(path)
	if err != nil || !cfg.Flow.AutoMerge {
		t.Fatalf("explicit config = %#v, %v; want auto-merge on", cfg.Flow, err)
	}
}

func TestSaveFlowAutoMergePreservesOtherSettings(t *testing.T) {
	xdg := t.TempDir()
	path := filepath.Join(xdg, "approach", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[agent]\ncommand = \"claude\"\n\n[flow]\npreset = \"default\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := []config.Option{config.WithGetenv(func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	})}
	if err := config.SaveFlowAutoMerge(true, opts...); err != nil {
		t.Fatalf("SaveFlowAutoMerge() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"command = \"claude\"", "preset = \"default\"", "auto_merge = true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
}
