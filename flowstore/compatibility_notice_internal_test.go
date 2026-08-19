package flowstore

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestOpenWarnsWhenTheSidecarDeclaresAHigherReaderGeneration covers the
// compatibility notice Stage E asks for. It is a WARNING rather than a refusal
// because user_version — the authority — is one this build can open; the
// sidecar is the only evidence that a newer build declared a higher floor, and
// evidence that is never believed must not gate an open either.
func TestOpenWarnsWhenTheSidecarDeclaresAHigherReaderGeneration(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	writeSidecarWithGenerations(t, root, databaseSchemaVersion, databaseSchemaVersion+1, databaseSchemaVersion+1)

	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatalf("an open must still succeed: %v", err)
	}
	defer reopened.Close()
	warnings := strings.Join(reopened.OpenDiagnostics().Warnings, "\n")
	if !strings.Contains(warnings, "minimum reader generation") {
		t.Fatalf("warnings = %q, want one naming the minimum reader generation", warnings)
	}
}

func TestOpenIsSilentWhenTheSidecarDeclaresThisBuildsGeneration(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	writeSidecarWithGenerations(t, root, databaseSchemaVersion, databaseSchemaVersion, databaseSchemaVersion)

	reopened, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, warning := range reopened.OpenDiagnostics().Warnings {
		if strings.Contains(warning, "generation") {
			t.Fatalf("unexpected compatibility warning: %q", warning)
		}
	}
}

// TestInspectTellsAFutureSchemaOperatorToUpgrade pins the difference that
// matters when `db inspect` is the only command that still answers: a database
// from a newer build needs an upgrade, and the restore advice attached to a
// damaged one would talk an operator into rolling their corpus back.
func TestInspectTellsAFutureSchemaOperatorToUpgrade(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, Role: RoleMigrator})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stampUserVersion(t, root, databaseSchemaVersion+1)

	report, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Readable || report.Tier != TierOpen {
		t.Fatalf("a future schema must still report cleanly: tier=%s readable=%t", report.Tier, report.Readable)
	}
	if report.NextAction == nil {
		t.Fatal("no next_action for a database from a newer build")
	}
	if !strings.Contains(*report.NextAction, "upgrade approach") {
		t.Fatalf("next_action = %q, want the upgrade advice", *report.NextAction)
	}
	if strings.Contains(*report.NextAction, backupDirName) {
		t.Fatalf("next_action = %q, must not offer restore advice for a newer build", *report.NextAction)
	}
}

// writeSidecarWithGenerations stages a sidecar whose declared floors this build
// did not choose, which is what a newer build's sidecar looks like from here.
func writeSidecarWithGenerations(t *testing.T, root string, physical, minReader, minWriter int64) {
	t.Helper()
	sidecar := databaseSidecar{
		SchemaVersion:       sidecarSchemaVersion,
		GenerationID:        newGenerationID(),
		PhysicalVersion:     physical,
		MinReaderGeneration: minReader,
		MinWriterGeneration: minWriter,
		Provenance:          sidecarProvenanceMigrated,
	}
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath(root), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
