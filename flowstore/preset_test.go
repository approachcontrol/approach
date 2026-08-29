package flowstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

func TestCreateDefaultSeedGolden(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record, err := store.Create(FlowRecord{
		FlowID:       "golden-default-seed",
		Title:        "Golden Default Seed",
		Instructions: "capture the default phase seed",
		RepoPath:     "/repo",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, _, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatalf("encodeStoredFlow() error = %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "default_seed.golden.json"))
	if err != nil {
		t.Fatalf("ReadFile(golden) error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("default seed metadata differed from golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDefaultPresetMatchesSeed(t *testing.T) {
	preset := DefaultPreset()
	if preset.Name != "default" {
		t.Fatalf("DefaultPreset().Name = %q, want default", preset.Name)
	}
	got := preset.Phases
	want := []PhaseSpec{
		{ID: "plan", Title: "Plan", Kind: KindPlan, DependsOn: []string{}},
		{ID: "plan-review", Title: "Plan Review", Kind: KindPlanReview, DependsOn: []string{"plan"}},
		{ID: "implementation", Title: "Implementation", Kind: KindImplementation, DependsOn: []string{"plan-review"}},
		{ID: "review-loop", Title: "Review loop", Kind: KindReviewLoop, DependsOn: []string{"implementation"}},
		{ID: "pr-creation", Title: "PR creation", Kind: KindPRCreation, DependsOn: []string{"review-loop"}},
		{ID: "autoreview", Title: "Autoreview", Kind: KindAutoreview, DependsOn: []string{"pr-creation"}},
		{ID: "merge", Title: "Merge", Kind: KindMerge, DependsOn: []string{"autoreview"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultPreset().Phases = %#v, want %#v", got, want)
	}
}

func TestCreateWithPresetSeedsNonLinearFlow(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	planStore, err := planstore.NewStore(planstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("plan NewStore() error = %v", err)
	}
	if _, err := planStore.Save(planstore.PlanRecord{
		PlanID:   "plan-1",
		Title:    "Linked Plan",
		Markdown: "Draft and publish.",
		Status:   "in_progress",
		RepoPath: repoPath,
		Phases: []planstore.PlanPhase{{
			PhaseID: "Draft",
			Title:   "Draft",
			Status:  "in_progress",
			Order:   3,
		}},
	}); err != nil {
		t.Fatalf("plan Save() error = %v", err)
	}
	preset := Preset{
		Name: "research-publish",
		Phases: []PhaseSpec{
			{ID: "research", Title: "Research", Kind: KindPlan, DependsOn: []string{}},
			{ID: "design-review", Title: "Design Review", Kind: KindPlanReview, DependsOn: []string{"research"}},
			{ID: "draft", Title: "Draft", Kind: KindImplementation, DependsOn: []string{"design-review"}},
			{ID: "benchmark", Title: "Benchmark", Kind: KindReviewLoop, DependsOn: []string{"design-review"}},
			{ID: "publish", Title: "Publish", Kind: KindPRCreation, DependsOn: []string{"draft", "benchmark"}},
			{ID: "ship", Title: "Ship", Kind: KindMerge, DependsOn: []string{"publish"}},
		},
	}
	record, err := store.CreateWithOptions(FlowRecord{
		FlowID:       "nonlinear-preset",
		Title:        "Nonlinear Preset",
		Instructions: "drive a preset graph",
		RepoPath:     repoPath,
		Branch:       "flow/nonlinear-preset",
	}, CreateOptions{Preset: &preset})
	if err != nil {
		t.Fatalf("CreateWithOptions() error = %v", err)
	}
	record, err = store.SetPlanLink(PlanLinkUpdate{FlowID: record.FlowID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("SetPlanLink() error = %v", err)
	}
	if got := phaseByIDForPresetTest(t, record, "research").Status; got != PhaseReady {
		t.Fatalf("research status = %q, want ready", got)
	}
	if got := phaseByIDForPresetTest(t, record, "draft").Status; got != PhasePending {
		t.Fatalf("draft status = %q, want pending", got)
	}

	record = mustSetPhaseForPresetTest(t, store, record, PhaseUpdate{FlowID: record.FlowID, PhaseID: "research", Status: PhaseCompleted})
	if got := phaseByIDForPresetTest(t, record, "design-review").Status; got != PhaseReady {
		t.Fatalf("design-review status = %q, want ready", got)
	}
	record = mustSetPhaseForPresetTest(t, store, record, PhaseUpdate{FlowID: record.FlowID, PhaseID: "design-review", Status: PhaseCompleted, Outcome: OutcomeApproved})
	if got := phaseByIDForPresetTest(t, record, "draft").Status; got != PhaseReady {
		t.Fatalf("draft status = %q, want ready after review approval", got)
	}
	if got := phaseByIDForPresetTest(t, record, "benchmark").Status; got != PhaseReady {
		t.Fatalf("benchmark status = %q, want ready after review approval", got)
	}
	record = mustSetPhaseForPresetTest(t, store, record, PhaseUpdate{FlowID: record.FlowID, PhaseID: "draft", Status: PhaseCompleted})
	if got := phaseByIDForPresetTest(t, record, "publish").Status; got != PhasePending {
		t.Fatalf("publish status = %q, want pending until benchmark completes", got)
	}
	plans, err := planStore.List(planstore.PlanFilter{RepoPath: repoPath})
	if err != nil {
		t.Fatalf("plan List() error = %v", err)
	}
	if got := planPhaseStatusByIDForPresetTest(t, plans[0], "draft"); got != "completed" {
		t.Fatalf("linked plan draft status = %q, want completed", got)
	}
	record = mustSetPhaseForPresetTest(t, store, record, PhaseUpdate{FlowID: record.FlowID, PhaseID: "benchmark", Status: PhaseCompleted})
	if got := phaseByIDForPresetTest(t, record, "publish").Status; got != PhaseReady {
		t.Fatalf("publish status = %q, want ready after fan-in", got)
	}
	record = mustSetPhaseForPresetTest(t, store, record, PhaseUpdate{FlowID: record.FlowID, PhaseID: "publish", Status: PhaseCompleted})
	if got := phaseByIDForPresetTest(t, record, "ship").Status; got != PhasePending {
		t.Fatalf("ship status = %q, want pending until PR target is recorded", got)
	}
	record, err = store.SetPR(PRUpdate{
		FlowID:     record.FlowID,
		Provider:   "github",
		Number:     42,
		URL:        "https://github.com/approachcontrol/approach/pull/42",
		HeadBranch: "flow/nonlinear-preset",
		BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("SetPR() error = %v", err)
	}
	if got := phaseByIDForPresetTest(t, record, "ship").Status; got != PhaseReady {
		t.Fatalf("ship status = %q, want ready after PR target", got)
	}
}

func TestCreateWithOptionsRejectsPresetWithDeclaredPhases(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	preset := DefaultPreset()
	_, err = store.CreateWithOptions(FlowRecord{
		FlowID:       "ambiguous-preset",
		Title:        "Ambiguous Preset",
		Instructions: "do not silently ignore preset options",
		RepoPath:     filepath.Join(root, "repo"),
		Phases: []FlowPhase{{
			PhaseID: "custom",
			Title:   "Custom",
			Status:  PhasePending,
			Order:   1,
		}},
	}, CreateOptions{Preset: &preset})
	if err == nil || !strings.Contains(err.Error(), "preset cannot be used with declared phases") {
		t.Fatalf("CreateWithOptions() error = %v, want preset/declared phases rejection", err)
	}
}

func TestPresetValidation(t *testing.T) {
	valid := Preset{
		Name: "valid",
		Phases: []PhaseSpec{
			{ID: "start", Title: "Start", Kind: KindPlan, DependsOn: []string{}},
			{ID: "finish", Title: "Finish", Kind: KindMerge, DependsOn: []string{"start"}},
		},
	}
	tests := []struct {
		name   string
		preset Preset
		want   string
	}{
		{
			name:   "empty name",
			preset: Preset{Phases: valid.Phases},
			want:   "preset name is required",
		},
		{
			name: "empty phase id",
			preset: Preset{
				Name:   "bad",
				Phases: []PhaseSpec{{Title: "Missing ID"}},
			},
			want: "phase 1 id is required",
		},
		{
			name: "empty title",
			preset: Preset{
				Name:   "bad",
				Phases: []PhaseSpec{{ID: "start"}},
			},
			want: `phase "start" title is required`,
		},
		{
			name: "duplicate phase IDs",
			preset: Preset{
				Name: "bad",
				Phases: []PhaseSpec{
					{ID: "Start", Title: "Start"},
					{ID: "start", Title: "Duplicate"},
				},
			},
			want: `duplicate phase id "start"`,
		},
		{
			name: "invalid phase id",
			preset: Preset{
				Name:   "bad",
				Phases: []PhaseSpec{{ID: "bad/id", Title: "Bad ID"}},
			},
			want: `invalid phase id "bad/id"`,
		},
		{
			name: "unknown kind",
			preset: Preset{
				Name:   "bad",
				Phases: []PhaseSpec{{ID: "review", Title: "Review", Kind: "plan_reveiw"}},
			},
			want: `unknown phase kind "plan_reveiw"`,
		},
		{
			name: "child kind",
			preset: Preset{
				Name:   "bad",
				Phases: []PhaseSpec{{ID: "child", Title: "Child", Kind: KindImplementationChild}},
			},
			want: "implementation_child cannot be used in a preset",
		},
		{
			name: "two merge phases",
			preset: Preset{
				Name: "bad",
				Phases: []PhaseSpec{
					{ID: "ship", Title: "Ship", Kind: KindMerge},
					{ID: "release", Title: "Release", Kind: KindMerge},
				},
			},
			want: "at most one merge phase",
		},
		{
			name: "cycle",
			preset: Preset{
				Name: "bad",
				Phases: []PhaseSpec{
					{ID: "a", Title: "A", DependsOn: []string{"b"}},
					{ID: "b", Title: "B", DependsOn: []string{"a"}},
				},
			},
			want: "phase graph contains a cycle",
		},
		{
			name: "unknown dependency",
			preset: Preset{
				Name: "bad",
				Phases: []PhaseSpec{
					{ID: "start", Title: "Start"},
					{ID: "finish", Title: "Finish", DependsOn: []string{"missing"}},
				},
			},
			want: `phase "finish": unknown dependency "missing"`,
		},
		{
			name: "duplicate dependency",
			preset: Preset{
				Name: "bad",
				Phases: []PhaseSpec{
					{ID: "start", Title: "Start"},
					{ID: "finish", Title: "Finish", DependsOn: []string{"start", "Start"}},
				},
			},
			want: `phase "finish" has duplicate dependency "Start"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePreset(tc.preset)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validatePreset() error = %v, want %q", err, tc.want)
			}
		})
	}
	if err := validatePreset(valid); err != nil {
		t.Fatalf("validatePreset(valid) error = %v", err)
	}
}

func TestPresetValidationUnknownKindMessageOmitsChildKind(t *testing.T) {
	err := validatePreset(Preset{
		Name:   "bad",
		Phases: []PhaseSpec{{ID: "review", Title: "Review", Kind: "bogus"}},
	})
	if err == nil {
		t.Fatal("validatePreset() error = nil, want unknown kind")
	}
	if strings.Contains(err.Error(), string(KindImplementationChild)) {
		t.Fatalf("validatePreset() error = %q, should not list child-only kind", err)
	}
}

func TestReadDoesNotHealCanonicalUnknownKindRecord(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	record := FlowRecord{
		SchemaVersion: 1,
		FlowID:        "unknown-kind-record",
		Title:         "Unknown Kind",
		Instructions:  "load old hand-authored records",
		Status:        StatusPending,
		RepoPath:      filepath.Join(root, "repo"),
		Merge:         Merge{Status: MergePending},
		AutoMode:      true,
		Phases: []FlowPhase{
			{PhaseID: "mystery", Title: "Mystery", Kind: "not_a_real_kind", DependsOn: []string{}, Status: PhaseCompleted, Order: 1, CreatedAt: now, UpdatedAt: now},
			{PhaseID: "next", Title: "Next", DependsOn: []string{"mystery"}, Status: PhasePending, Order: 2, CreatedAt: now, UpdatedAt: now},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.write(record); err != nil {
		t.Fatalf("write test flow: %v", err)
	}

	read, err := store.Read(record.FlowID)
	if err != nil {
		t.Fatalf("Read() with unknown kind error = %v", err)
	}
	if got := phaseByIDForPresetTest(t, read, "next").Status; got != PhasePending {
		t.Fatalf("next status = %q, want stored pending status without runtime healing", got)
	}
}

func mustSetPhaseForPresetTest(t *testing.T, store *Store, record FlowRecord, update PhaseUpdate) FlowRecord {
	t.Helper()
	updated, err := store.SetPhase(update)
	if err != nil {
		t.Fatalf("SetPhase(%s %s) error = %v", update.PhaseID, update.Status, err)
	}
	return updated
}

func phaseByIDForPresetTest(t *testing.T, record FlowRecord, phaseID string) FlowPhase {
	t.Helper()
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	t.Fatalf("phase %q not found in %#v", phaseID, record.Phases)
	return FlowPhase{}
}

func planPhaseStatusByIDForPresetTest(t *testing.T, record planstore.PlanRecord, phaseID string) string {
	t.Helper()
	for _, phase := range record.Phases {
		if strings.EqualFold(phase.PhaseID, phaseID) {
			return phase.Status
		}
	}
	t.Fatalf("plan phase %q not found in %#v", phaseID, record.Phases)
	return ""
}
