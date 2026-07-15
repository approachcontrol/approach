package sessions

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

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
