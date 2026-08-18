package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidateTranscriptPathCursorAcceptsChatsAndProjects(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home}
	chatsPath := filepath.Join(home, ".cursor", "chats", "store.db")
	projectsPath := filepath.Join(home, ".cursor", "projects", "repo", "agent-transcripts", "chat.db")
	siblingPath := filepath.Join(home, ".cursor", "hooks.json")
	for _, path := range []string{chatsPath, projectsPath, siblingPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("opaque"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	for _, path := range []string{chatsPath, projectsPath} {
		canonical, err := ValidateTranscriptPath(ProviderCursor, path, env)
		if err != nil {
			t.Fatalf("ValidateTranscriptPath(%q) error = %v", path, err)
		}
		want, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("canonicalize %q: %v", path, err)
		}
		if canonical != want {
			t.Fatalf("ValidateTranscriptPath(%q) = %q, want %q", path, canonical, want)
		}
	}

	if _, err := ValidateTranscriptPath(ProviderCursor, siblingPath, env); err == nil || !strings.Contains(err.Error(), "outside expected") {
		t.Fatalf("ValidateTranscriptPath(%q) error = %v, want out-of-root rejection", siblingPath, err)
	}
}

func TestValidateTranscriptPathCursorAcceptsProjectsWhenChatsMissing(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "projects", "repo", "agent-transcripts", "chat.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create projects transcript: %v", err)
	}
	if err := os.WriteFile(path, []byte("opaque"), 0o600); err != nil {
		t.Fatalf("write projects transcript: %v", err)
	}

	if _, err := ValidateTranscriptPath(ProviderCursor, path, map[string]string{"HOME": home}); err != nil {
		t.Fatalf("ValidateTranscriptPath() error = %v, want projects path accepted without chats root", err)
	}
}

func TestOpenValidatedTranscriptFileRejectsReplacementAfterValidation(t *testing.T) {
	root := t.TempDir()
	validatedPath := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(validatedPath, []byte("validated\n"), 0o600); err != nil {
		t.Fatalf("write validated transcript: %v", err)
	}
	validatedInfo, err := os.Stat(validatedPath)
	if err != nil {
		t.Fatalf("stat validated transcript: %v", err)
	}

	outsidePath := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outsidePath, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write outside transcript: %v", err)
	}
	if err := os.Remove(validatedPath); err != nil {
		t.Fatalf("remove validated transcript: %v", err)
	}
	if err := os.Symlink(outsidePath, validatedPath); err != nil {
		t.Fatalf("replace transcript with symlink: %v", err)
	}

	file, err := openValidatedTranscriptFile(validatedPath, validatedInfo)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("openValidatedTranscriptFile() error = nil, want replacement rejection")
	}
}

func TestOpenValidatedTranscriptFileDoesNotBlockOnFIFOReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte("validated\n"), 0o600); err != nil {
		t.Fatalf("write validated transcript: %v", err)
	}
	validatedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat validated transcript: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove validated transcript: %v", err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("replace transcript with FIFO: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		file, err := openValidatedTranscriptFile(path, validatedInfo)
		if file != nil {
			file.Close()
		}
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("openValidatedTranscriptFile() error = nil, want FIFO rejection")
		}
	case <-time.After(time.Second):
		t.Fatal("openValidatedTranscriptFile() blocked on FIFO replacement")
	}
}
