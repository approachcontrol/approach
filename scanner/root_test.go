package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRootCleansExplicitRoot(t *testing.T) {
	root := t.TempDir()

	got, err := ResolveRoot(filepath.Join(root, "."))
	if err != nil {
		t.Fatalf("ResolveRoot returned error: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("ResolveRoot = %q, want %q", got, filepath.Clean(root))
	}
}

func TestResolveRootConvertsRelativeExplicitRootToAbsolute(t *testing.T) {
	cwd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if err := os.Mkdir("repos", 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRoot(filepath.Join(".", "repos"))
	if err != nil {
		t.Fatalf("ResolveRoot returned error: %v", err)
	}
	want, err := filepath.Abs("repos")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveRoot = %q, want %q", got, want)
	}
}

func TestResolveRootDefaultsToHomeDev(t *testing.T) {
	home := t.TempDir()
	got, err := resolveRoot("", func() (string, error) {
		return home, nil
	})
	if err != nil {
		t.Fatalf("resolveRoot returned error: %v", err)
	}
	want := filepath.Join(home, "dev")
	if got != want {
		t.Fatalf("resolveRoot = %q, want %q", got, want)
	}
}

func TestResolveRootSurfacesHomeDirectoryFailure(t *testing.T) {
	wantErr := errors.New("home unavailable")
	_, err := resolveRoot("", func() (string, error) {
		return "", wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("resolveRoot error = %v, want %v", err, wantErr)
	}
}

func TestScanPreservesExplicitRelativeRoot(t *testing.T) {
	cwd := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if err := os.MkdirAll(filepath.Join("repos", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repos, err := Scan(ScanOptions{Root: "repos"})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	wantPath := filepath.Join("repos", "app")
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %+v", repos)
	}
	if repos[0].Path != wantPath {
		t.Fatalf("repo path = %q, want %q", repos[0].Path, wantPath)
	}
	if repos[0].DisplayName != "app" {
		t.Fatalf("repo display name = %q, want app", repos[0].DisplayName)
	}
}
