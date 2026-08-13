package flowstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

func TestPlanstoreSyncerResolvePlanLinkCollapsesInvalidMetadataToNotFound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata []byte
	}{
		{name: "missing metadata"},
		{name: "malformed metadata", metadata: []byte("{")},
		{name: "mismatched metadata id", metadata: []byte(`{"plan_id":"some-other-plan"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.metadata != nil {
				writePlanLinkMetadataFixture(t, root, "plan-1", tc.metadata)
			}

			_, err := (planstoreSyncer{root: root, lockTimeout: time.Second}).resolvePlanLink("plan-1", "")
			if err == nil || err.Error() != `plan "plan-1" not found` {
				t.Fatalf("resolvePlanLink() error = %v, want exact not-found error", err)
			}
		})
	}
}

func TestPlanstoreSyncerResolvePlanLinkValidatesBeforeOpeningStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	resolver := planstoreSyncer{root: root, lockTimeout: time.Second}
	for _, tc := range []struct {
		name         string
		planID       string
		suppliedPath string
		want         string
	}{
		{
			name:         "plan id before supplied path",
			planID:       "../bad",
			suppliedPath: "relative/plan.md",
			want:         `invalid plan id "../bad"`,
		},
		{
			name:         "relative supplied path",
			planID:       "plan-1",
			suppliedPath: "relative/plan.md",
			want:         "flow plan path must be absolute: relative/plan.md",
		},
		{
			name:         "mismatched supplied path",
			planID:       "plan-1",
			suppliedPath: filepath.Join(t.TempDir(), "plans", "plan-1", "plan.md"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolver.resolvePlanLink(tc.planID, tc.suppliedPath)
			want := tc.want
			if want == "" {
				want = fmt.Sprintf("flow plan path %q does not match plan %q path %q", filepath.Clean(tc.suppliedPath), tc.planID, filepath.Join(root, "plans", tc.planID, "plan.md"))
			}
			if err == nil || err.Error() != want {
				t.Fatalf("resolvePlanLink() error = %v, want %q", err, want)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("resolver opened plan store before validation; root stat error = %v", err)
			}
		})
	}
}

func TestPlanstoreSyncerResolvePlanLinkReportsMissingMarkdownBody(t *testing.T) {
	root := t.TempDir()
	metadata, err := json.Marshal(planstore.PlanRecord{
		SchemaVersion: 1,
		PlanID:        "plan-1",
		Title:         "Missing Markdown",
		Status:        "approved",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	writePlanLinkMetadataFixture(t, root, "plan-1", metadata)

	_, err = (planstoreSyncer{root: root, lockTimeout: time.Second}).resolvePlanLink("plan-1", "")
	if err == nil || !strings.Contains(err.Error(), "read plan:") || !strings.Contains(err.Error(), filepath.Join("plans", "plan-1", "plan.md")) {
		t.Fatalf("resolvePlanLink() error = %v, want wrapped missing plan.md read error", err)
	}
}

func TestPlanstoreSyncerResolvePlanLinkPreservesConfiguredSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real-root")
	configuredRoot := filepath.Join(parent, "configured-root")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("Mkdir(real root) error = %v", err)
	}
	if err := os.Symlink(realRoot, configuredRoot); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	flowStore, err := NewStore(StoreOptions{Root: configuredRoot})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = flowStore.Close() })
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: configuredRoot})
	if err != nil {
		t.Fatalf("planstore.NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Symlink-root plan",
		Markdown: "Keep the configured-root spelling.",
		Status:   "approved",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	record, err := flowStore.Create(FlowRecord{
		Title:        "Symlink-root Flow",
		Instructions: "Persist the configured plan path.",
		RepoPath:     filepath.Join(parent, "repo"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	configuredPlanPath := filepath.Join(configuredRoot, "plans", "plan-1", "plan.md")
	linked, err := flowStore.SetPlanLink(PlanLinkUpdate{
		FlowID:   record.FlowID,
		PlanID:   "plan-1",
		PlanPath: configuredPlanPath,
	})
	if err != nil {
		t.Fatalf("SetPlanLink(configured path) error = %v", err)
	}
	if linked.PlanPath != configuredPlanPath {
		t.Fatalf("PlanPath = %q, want configured-root path %q", linked.PlanPath, configuredPlanPath)
	}

	canonicalPlanPath := filepath.Join(realRoot, "plans", "plan-1", "plan.md")
	got, err := flowStore.SetPlanLink(PlanLinkUpdate{
		FlowID:   record.FlowID,
		PlanID:   "plan-1",
		PlanPath: canonicalPlanPath,
	})
	wantErr := fmt.Sprintf("flow plan path %q does not match plan %q path %q", canonicalPlanPath, "plan-1", configuredPlanPath)
	if err == nil || err.Error() != wantErr {
		t.Fatalf("SetPlanLink(canonical path) error = %v, want %q", err, wantErr)
	}
	if !reflect.DeepEqual(got, FlowRecord{}) {
		t.Fatalf("SetPlanLink(canonical path) record = %#v, want zero record", got)
	}
	persisted, err := flowStore.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !reflect.DeepEqual(persisted, linked) {
		t.Fatalf("mismatched path changed record:\n got: %#v\nwant: %#v", persisted, linked)
	}
}

func writePlanLinkMetadataFixture(t *testing.T, root, planID string, metadata []byte) {
	t.Helper()
	dir := filepath.Join(root, "plans", planID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), metadata, 0o600); err != nil {
		t.Fatalf("WriteFile(meta.json) error = %v", err)
	}
}
