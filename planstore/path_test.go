package planstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brian-bell/wtui/planstore"
)

func TestMarkdownPathReturnsPlanMarkdownPath(t *testing.T) {
	root := t.TempDir()

	got, err := planstore.MarkdownPath(root, "plan-1")
	if err != nil {
		t.Fatalf("MarkdownPath() error = %v", err)
	}

	want := filepath.Join(root, "plans", "plan-1", "plan.md")
	if got != want {
		t.Fatalf("MarkdownPath() = %q, want %q", got, want)
	}
}

func TestMarkdownPathRejectsInvalidPlanID(t *testing.T) {
	root := t.TempDir()

	if _, err := planstore.MarkdownPath(root, "../plan"); err == nil {
		t.Fatal("expected invalid plan ID error")
	}
}

func TestMarkdownPathRejectsRelativeRoot(t *testing.T) {
	if _, err := planstore.MarkdownPath("relative/root", "plan-1"); err == nil {
		t.Fatal("expected relative root error")
	}
}

func TestMarkdownPathAllowsAbsoluteRootWithSpaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state root")

	got, err := planstore.MarkdownPath(root, "plan-1")
	if err != nil {
		t.Fatalf("MarkdownPath() error = %v", err)
	}

	want := filepath.Join(root, "plans", "plan-1", "plan.md")
	if got != want {
		t.Fatalf("MarkdownPath() = %q, want %q", got, want)
	}
}

func TestMarkdownPathDefaultsEmptyRoot(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	got, err := planstore.MarkdownPath("", "plan-1")
	if err != nil {
		t.Fatalf("MarkdownPath() error = %v", err)
	}

	want := filepath.Join(stateHome, "wtui", "sessions", "v1", "plans", "plan-1", "plan.md")
	if got != want {
		t.Fatalf("MarkdownPath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("MarkdownPath() should not create or read plan file, stat err = %v", err)
	}
}
