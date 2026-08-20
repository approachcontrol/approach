package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertRestoresTranscriptWhenMetadataWriteFails(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	providerDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldProvider := filepath.Join(providerDir, "old.jsonl")
	if err := os.WriteFile(oldProvider, []byte(`{"role":"user","kind":"message","text":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write old provider transcript: %v", err)
	}
	newProvider := filepath.Join(providerDir, "new.jsonl")
	if err := os.WriteFile(newProvider, []byte(`{"role":"user","kind":"message","text":"new"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write new provider transcript: %v", err)
	}
	store, err := NewStore(StoreOptions{Root: root, Env: map[string]string{"CODEX_HOME": codexHome}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "running",
		TranscriptPath: oldProvider,
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	transcriptPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "transcript.jsonl")
	metaPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "meta.json")
	store.beforeMetadataWrite = func() {
		if err := os.Remove(metaPath); err != nil {
			t.Fatalf("remove meta.json: %v", err)
		}
		if err := os.Mkdir(metaPath, 0o700); err != nil {
			t.Fatalf("block meta.json: %v", err)
		}
	}

	err = store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "ended",
		TranscriptPath: newProvider,
	})
	if err == nil {
		t.Fatal("update Upsert() error = nil, want metadata write failure")
	}
	got, readErr := os.ReadFile(transcriptPath)
	if readErr != nil {
		t.Fatalf("read restored transcript: %v", readErr)
	}
	if !strings.Contains(string(got), `"text":"old"`) || strings.Contains(string(got), `"text":"new"`) {
		t.Fatalf("transcript = %q, want restored previous body", got)
	}
}

func TestUpsertRemovesNewTranscriptWhenMetadataWriteFails(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	providerDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	providerTranscript := filepath.Join(providerDir, "new.jsonl")
	if err := os.WriteFile(providerTranscript, []byte(`{"role":"user","kind":"message","text":"new"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write provider transcript: %v", err)
	}
	store, err := NewStore(StoreOptions{Root: root, Env: map[string]string{"CODEX_HOME": codexHome}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	dir := store.sessionDir(ProviderCodex, "new-session")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store.beforeMetadataWrite = func() {
		if err := os.Mkdir(filepath.Join(dir, "meta.json"), 0o700); err != nil {
			t.Fatalf("block meta.json: %v", err)
		}
	}

	err = store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "new-session",
		Status:         "ended",
		TranscriptPath: providerTranscript,
	})
	if err == nil {
		t.Fatal("Upsert() error = nil, want metadata write failure")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "transcript.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("unpublished transcript still present: %v", statErr)
	}
}

func TestUpsertKeepsExistingTranscriptWhenMetadataOnlyWriteFails(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	providerDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldProvider := filepath.Join(providerDir, "old.jsonl")
	if err := os.WriteFile(oldProvider, []byte(`{"role":"user","kind":"message","text":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write old provider transcript: %v", err)
	}
	store, err := NewStore(StoreOptions{Root: root, Env: map[string]string{"CODEX_HOME": codexHome}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "running",
		TranscriptPath: oldProvider,
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	transcriptPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "transcript.jsonl")
	metaPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "meta.json")
	store.beforeMetadataWrite = func() {
		if err := os.Remove(metaPath); err != nil {
			t.Fatalf("remove meta.json: %v", err)
		}
		if err := os.Mkdir(metaPath, 0o700); err != nil {
			t.Fatalf("block meta.json: %v", err)
		}
	}

	err = store.Upsert(SessionRecord{
		Provider:  ProviderCodex,
		SessionID: "existing-session",
		Status:    "ended",
		Summary:   "metadata only",
	})
	if err == nil {
		t.Fatal("update Upsert() error = nil, want metadata write failure")
	}
	got, readErr := os.ReadFile(transcriptPath)
	if readErr != nil {
		t.Fatalf("existing transcript was removed: %v", readErr)
	}
	if !strings.Contains(string(got), `"text":"old"`) {
		t.Fatalf("transcript = %q, want existing body left in place", got)
	}
}

func TestUpsertRestoresRawCopyWhenNormalizationFails(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	providerDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldProvider := filepath.Join(providerDir, "old.jsonl")
	if err := os.WriteFile(oldProvider, []byte(`{"role":"user","kind":"message","text":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write old provider transcript: %v", err)
	}
	badProvider := filepath.Join(providerDir, "bad.jsonl")
	if err := os.WriteFile(badProvider, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("write invalid provider transcript: %v", err)
	}
	store, err := NewStore(StoreOptions{Root: root, CopyRawTranscripts: true, Env: map[string]string{"CODEX_HOME": codexHome}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "running",
		TranscriptPath: oldProvider,
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	rawPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "raw.jsonl")

	err = store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "ended",
		TranscriptPath: badProvider,
	})
	if err == nil {
		t.Fatal("update Upsert() error = nil, want transcript normalization failure")
	}
	got, readErr := os.ReadFile(rawPath)
	if readErr != nil {
		t.Fatalf("raw transcript was removed: %v", readErr)
	}
	if !strings.Contains(string(got), `"text":"old"`) {
		t.Fatalf("raw.jsonl = %q, want restored previous raw copy", got)
	}
	if _, statErr := os.Stat(rawPath + ".prev"); !os.IsNotExist(statErr) {
		t.Fatalf("unused raw backup still present: %v", statErr)
	}
	transcriptPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "transcript.jsonl")
	if _, statErr := os.Stat(transcriptPath + ".prev"); !os.IsNotExist(statErr) {
		t.Fatalf("unused transcript backup still present: %v", statErr)
	}
}

func TestUpsertFailsClosedOnUnreadablePreviousTranscript(t *testing.T) {
	root := t.TempDir()
	codexHome := t.TempDir()
	providerDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(providerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldProvider := filepath.Join(providerDir, "old.jsonl")
	if err := os.WriteFile(oldProvider, []byte(`{"role":"user","kind":"message","text":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write old provider transcript: %v", err)
	}
	newProvider := filepath.Join(providerDir, "new.jsonl")
	if err := os.WriteFile(newProvider, []byte(`{"role":"user","kind":"message","text":"new"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write new provider transcript: %v", err)
	}
	store, err := NewStore(StoreOptions{Root: root, Env: map[string]string{"CODEX_HOME": codexHome}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "running",
		TranscriptPath: oldProvider,
	}); err != nil {
		t.Fatalf("seed Upsert() error = %v", err)
	}
	transcriptPath := filepath.Join(store.sessionDir(ProviderCodex, "existing-session"), "transcript.jsonl")
	if err := os.Remove(transcriptPath); err != nil {
		t.Fatalf("remove transcript.jsonl: %v", err)
	}
	if err := os.Mkdir(transcriptPath, 0o700); err != nil {
		t.Fatalf("block transcript.jsonl: %v", err)
	}

	err = store.Upsert(SessionRecord{
		Provider:       ProviderCodex,
		SessionID:      "existing-session",
		Status:         "ended",
		TranscriptPath: newProvider,
	})
	if err == nil {
		t.Fatal("update Upsert() error = nil, want unreadable previous transcript")
	}
	info, statErr := os.Stat(transcriptPath)
	if statErr != nil {
		t.Fatalf("previous transcript path was removed: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("previous transcript path = file, want blocking directory left in place")
	}
}

func TestRollbackPayloadStagesIncludesCauseAndRollbackError(t *testing.T) {
	err := rollbackPayloadStages([]payloadStage{
		{rollback: func() error { return errors.New("restore failed") }},
	}, errors.New("raw stage failed"))
	if err == nil || !strings.Contains(err.Error(), "raw stage failed") || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("error = %v, want both cause and rollback failure", err)
	}
}

func TestCommitPayloadStagesContinuesAfterFirstError(t *testing.T) {
	second := false
	err := commitPayloadStages([]payloadStage{
		{commit: func() error { return errors.New("first") }},
		{commit: func() error { second = true; return nil }},
	})
	if err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("error = %v, want first commit error", err)
	}
	if !second {
		t.Fatal("second commit was not attempted")
	}
}
