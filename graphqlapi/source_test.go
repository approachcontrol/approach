package graphqlapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/scanner"
)

func staticRepos(repos ...scanner.Repo) RepoSource {
	return func() ([]scanner.Repo, error) { return repos, nil }
}

func staticFlows(records ...flowstore.FlowRecord) FlowSource {
	return func() ([]flowstore.FlowRecord, error) { return records, nil }
}

func repoPaths(repos []*Repo) []string {
	out := make([]string, 0, len(repos))
	for _, repo := range repos {
		out = append(out, repo.Path)
	}
	return out
}

func flowIDs(flows []*Flow) []string {
	out := make([]string, 0, len(flows))
	for _, flow := range flows {
		out = append(out, flow.Record.FlowID)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestBuildSnapshotKeepsStoreFlowOrder(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		staticFlows(
			flowstore.FlowRecord{FlowID: "newest", RepoPath: "/repos/alpha"},
			flowstore.FlowRecord{FlowID: "middle", RepoPath: "/repos/alpha"},
			flowstore.FlowRecord{FlowID: "oldest", RepoPath: "/repos/alpha"},
		),
		nil,
	)
	if got, want := flowIDs(snap.Flows()), []string{"newest", "middle", "oldest"}; !equalStrings(got, want) {
		t.Fatalf("Flows() = %v, want %v", got, want)
	}
	if got, want := flowIDs(snap.FlowsForRepo("/repos/alpha")), []string{"newest", "middle", "oldest"}; !equalStrings(got, want) {
		t.Fatalf("FlowsForRepo() = %v, want %v", got, want)
	}
}

func TestBuildSnapshotGroupsFlowsByRepo(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(
			scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"},
			scanner.Repo{Path: "/repos/beta", DisplayName: "beta"},
		),
		staticFlows(
			flowstore.FlowRecord{FlowID: "a1", RepoPath: "/repos/alpha"},
			flowstore.FlowRecord{FlowID: "b1", RepoPath: "/repos/beta"},
			flowstore.FlowRecord{FlowID: "a2", RepoPath: "/repos/alpha"},
		),
		nil,
	)
	if got, want := flowIDs(snap.FlowsForRepo("/repos/alpha")), []string{"a1", "a2"}; !equalStrings(got, want) {
		t.Fatalf("FlowsForRepo(alpha) = %v, want %v", got, want)
	}
	if got, want := flowIDs(snap.FlowsForRepo("/repos/beta")), []string{"b1"}; !equalStrings(got, want) {
		t.Fatalf("FlowsForRepo(beta) = %v, want %v", got, want)
	}
	if got := snap.FlowsForRepo("/repos/missing"); len(got) != 0 {
		t.Fatalf("FlowsForRepo(missing) = %v, want empty", flowIDs(got))
	}
}

func TestBuildSnapshotSynthesizesRepoOutsideScanRoot(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha", IsBare: true}),
		staticFlows(flowstore.FlowRecord{FlowID: "f1", RepoPath: "/elsewhere/outsider"}),
		nil,
	)
	repo, ok := snap.Repo("/elsewhere/outsider")
	if !ok {
		t.Fatalf("Repo(/elsewhere/outsider) not found; snapshot has %v", repoPaths(snap.Repos()))
	}
	if repo.DisplayName != "outsider" {
		t.Errorf("DisplayName = %q, want %q", repo.DisplayName, "outsider")
	}
	if repo.InScanRoot {
		t.Errorf("InScanRoot = true, want false")
	}
	if repo.IsBare {
		t.Errorf("IsBare = true, want false")
	}

	scanned, ok := snap.Repo("/repos/alpha")
	if !ok {
		t.Fatalf("Repo(/repos/alpha) not found")
	}
	if !scanned.InScanRoot || !scanned.IsBare {
		t.Errorf("scanned repo = %+v, want InScanRoot and IsBare true", scanned)
	}
}

func TestBuildSnapshotNormalizesFlowRepoPathOntoScannedRepo(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		staticFlows(
			flowstore.FlowRecord{FlowID: "trailing", RepoPath: "/repos/alpha/"},
			flowstore.FlowRecord{FlowID: "dotdot", RepoPath: "/repos/beta/../alpha"},
		),
		nil,
	)
	if got, want := len(snap.Repos()), 1; got != want {
		t.Fatalf("len(Repos()) = %d (%v), want %d", got, repoPaths(snap.Repos()), want)
	}
	if got, want := flowIDs(snap.FlowsForRepo("/repos/alpha")), []string{"trailing", "dotdot"}; !equalStrings(got, want) {
		t.Fatalf("FlowsForRepo(alpha) = %v, want %v", got, want)
	}
	for _, flow := range snap.Flows() {
		if flow.RepoPath != "/repos/alpha" {
			t.Errorf("flow %q RepoPath = %q, want %q", flow.Record.FlowID, flow.RepoPath, "/repos/alpha")
		}
	}
}

func TestSnapshotRepoResolvesUnnormalizedArgument(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		staticFlows(),
		nil,
	)
	for _, id := range []string{"/repos/alpha/", "/repos/alpha/.", "  /repos/alpha  ", "/repos/beta/../alpha"} {
		if _, ok := snap.Repo(id); !ok {
			t.Errorf("Repo(%q) not found, want the scanned repo", id)
		}
	}
	if _, ok := snap.Repo(""); ok {
		t.Errorf("Repo(\"\") found a repo, want none")
	}
}

func TestSnapshotRepoResolvesRelativeArgumentAgainstWorkingDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: filepath.Join(wd, "here"), DisplayName: "here"}),
		staticFlows(),
		nil,
	)
	if _, ok := snap.Repo("here"); !ok {
		t.Fatalf("Repo(%q) not found, want the scanned repo", "here")
	}
}

func TestBuildSnapshotUnionOrderingIsDeterministic(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/dev/parent/child", DisplayName: "parent/child"}),
		staticFlows(
			flowstore.FlowRecord{FlowID: "f2", RepoPath: "/other/zeta/child"},
			flowstore.FlowRecord{FlowID: "f1", RepoPath: "/other/alpha/child"},
		),
		nil,
	)
	// "child" sorts before "parent/child"; the two synthesized "child"
	// entries tie on display name and break on path.
	want := []string{"/other/alpha/child", "/other/zeta/child", "/dev/parent/child"}
	if got := repoPaths(snap.Repos()); !equalStrings(got, want) {
		t.Fatalf("Repos() = %v, want %v", got, want)
	}
}

func TestBuildSnapshotSortsCaseInsensitively(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(
			scanner.Repo{Path: "/repos/Zulu", DisplayName: "Zulu"},
			scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"},
			scanner.Repo{Path: "/repos/Bravo", DisplayName: "Bravo"},
		),
		staticFlows(),
		nil,
	)
	want := []string{"/repos/alpha", "/repos/Bravo", "/repos/Zulu"}
	if got := repoPaths(snap.Repos()); !equalStrings(got, want) {
		t.Fatalf("Repos() = %v, want %v", got, want)
	}
}

func TestBuildSnapshotSkipsRecordWithEmptyRepoPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	snap := buildSnapshot(
		staticRepos(),
		staticFlows(
			flowstore.FlowRecord{FlowID: "phantom", RepoPath: ""},
			flowstore.FlowRecord{FlowID: "blank", RepoPath: "   "},
		),
		nil,
	)
	if got := len(snap.Repos()); got != 0 {
		t.Fatalf("Repos() = %v, want empty (no CWD phantom)", repoPaths(snap.Repos()))
	}
	if _, ok := snap.Repo(wd); ok {
		t.Fatalf("Repo(cwd) found a synthesized repo at the working directory")
	}
	if got := len(snap.Flows()); got != 0 {
		t.Fatalf("Flows() = %v, want empty", flowIDs(snap.Flows()))
	}
	if _, ok := snap.Flow("phantom"); ok {
		t.Fatalf("Flow(phantom) resolved; a record with no repo path must be skipped")
	}
}

func TestBuildSnapshotDegradesOnScanError(t *testing.T) {
	var logged []string
	snap := buildSnapshot(
		func() ([]scanner.Repo, error) {
			return nil, errors.New("open /nope/dev: no such file or directory")
		},
		staticFlows(flowstore.FlowRecord{FlowID: "f1", RepoPath: "/repos/alpha"}),
		func(format string, args ...any) {
			logged = append(logged, format)
		},
	)
	if snap.err != nil {
		t.Fatalf("snapshot err = %v, want nil (scan failures degrade)", snap.err)
	}
	if got, want := repoPaths(snap.Repos()), []string{"/repos/alpha"}; !equalStrings(got, want) {
		t.Fatalf("Repos() = %v, want %v", got, want)
	}
	if repo, ok := snap.Repo("/repos/alpha"); !ok || repo.InScanRoot {
		t.Fatalf("Repo(alpha) = %+v, %v; want a synthesized entry", repo, ok)
	}
	if got, want := flowIDs(snap.Flows()), []string{"f1"}; !equalStrings(got, want) {
		t.Fatalf("Flows() = %v, want %v", got, want)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "repository scan failed") {
		t.Fatalf("logged = %v, want one repository-scan warning", logged)
	}
}

func TestBuildSnapshotFlowErrorIsSanitized(t *testing.T) {
	var logged []string
	snap := buildSnapshot(
		staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"}),
		func() ([]flowstore.FlowRecord, error) {
			return nil, errors.New("list flows: open /tmp/secret-root/flows: permission denied")
		},
		func(format string, args ...any) {
			logged = append(logged, format)
		},
	)
	if !errors.Is(snap.err, errStateUnavailable) {
		t.Fatalf("snapshot err = %v, want errStateUnavailable", snap.err)
	}
	if strings.Contains(snap.err.Error(), "secret-root") || strings.Contains(snap.err.Error(), "permission denied") {
		t.Fatalf("snapshot err leaked detail: %v", snap.err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "reading flow records failed") {
		t.Fatalf("logged = %v, want one flow-read failure", logged)
	}
}

func TestBuildSnapshotEmptySources(t *testing.T) {
	snap := buildSnapshot(staticRepos(), staticFlows(), nil)
	if len(snap.Repos()) != 0 || len(snap.Flows()) != 0 {
		t.Fatalf("empty sources produced repos=%v flows=%v", repoPaths(snap.Repos()), flowIDs(snap.Flows()))
	}
	if snap.err != nil {
		t.Fatalf("snapshot err = %v, want nil", snap.err)
	}
	if _, ok := snap.Flow("nope"); ok {
		t.Fatalf("Flow(nope) resolved against an empty snapshot")
	}

	nilSources := buildSnapshot(nil, nil, nil)
	if len(nilSources.Repos()) != 0 || len(nilSources.Flows()) != 0 || nilSources.err != nil {
		t.Fatalf("nil sources produced repos=%v flows=%v err=%v",
			repoPaths(nilSources.Repos()), flowIDs(nilSources.Flows()), nilSources.err)
	}
}

func TestBuildSnapshotDeduplicatesScannedPaths(t *testing.T) {
	snap := buildSnapshot(
		staticRepos(
			scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"},
			scanner.Repo{Path: "/repos/alpha/", DisplayName: "alpha-dup"},
		),
		staticFlows(),
		nil,
	)
	if got, want := len(snap.Repos()), 1; got != want {
		t.Fatalf("len(Repos()) = %d, want %d", got, want)
	}
	if repo, _ := snap.Repo("/repos/alpha"); repo.DisplayName != "alpha" {
		t.Fatalf("DisplayName = %q, want the first scanned entry %q", repo.DisplayName, "alpha")
	}
}

func TestBuildSnapshotFlowLookupPrefersFirstRecordForDuplicateID(t *testing.T) {
	now := time.Now().UTC()
	snap := buildSnapshot(
		staticRepos(),
		staticFlows(
			flowstore.FlowRecord{FlowID: "dupe", RepoPath: "/repos/alpha", UpdatedAt: now},
			flowstore.FlowRecord{FlowID: "dupe", RepoPath: "/repos/beta", UpdatedAt: now.Add(-time.Hour)},
		),
		nil,
	)
	flow, ok := snap.Flow("dupe")
	if !ok {
		t.Fatalf("Flow(dupe) not found")
	}
	if flow.RepoPath != "/repos/alpha" {
		t.Fatalf("Flow(dupe).RepoPath = %q, want the first (newest) record's %q", flow.RepoPath, "/repos/alpha")
	}
}

// fullyPopulatedSources fills every optional field on every type, so the
// drift guard sees a width for each one and byte-budget tests have realistic
// free text to measure.
func fullyPopulatedSources() (RepoSource, FlowSource, func(string, ...any)) {
	created := fixtureTime(0)
	merged := fixtureTime(time.Hour)
	return staticRepos(scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha", IsBare: true}),
		staticFlows(flowstore.FlowRecord{
			FlowID: "flow-alpha-1", Title: "Alpha one", Instructions: "do the thing",
			Status: flowstore.StatusInProgress, RepoPath: "/repos/alpha",
			WorktreePath: "/repos/alpha-worktrees/one", Branch: "flow/one", BaseRef: "main",
			Commit: "abc123", PresetName: "default", PlanID: "plan-1", AutoMode: true,
			Issue: flowstore.Issue{Provider: "github", Number: 7, URL: "https://example.test/i/7"},
			PR: flowstore.PullRequest{
				Provider: "github", Number: 9, URL: "https://example.test/p/9",
				HeadBranch: "flow/one", BaseBranch: "main", Status: "open",
			},
			Merge: flowstore.Merge{Status: flowstore.MergeMerged, Commit: "def456", MergedAt: &merged},
			Phases: []flowstore.FlowPhase{
				{
					PhaseID: "plan", Title: "Plan", Kind: flowstore.KindPlan, Status: flowstore.PhaseCompleted,
					Order: 1, Outcome: "approved", Notes: "some notes", Summary: "a summary",
					CreatedAt: created, UpdatedAt: created,
				},
				{
					PhaseID: "impl", ParentPhaseID: "plan", Title: "Implement", Kind: flowstore.KindImplementation,
					Status: flowstore.PhaseReady, Order: 2, DependsOn: []string{"plan"},
					CreatedAt: created, UpdatedAt: created,
				},
			},
			CreatedAt: created, UpdatedAt: created,
		}),
		nil
}

func TestSnapshotBounds(t *testing.T) {
	created := fixtureTime(0)
	// Distinct, non-trivial cardinalities so a bound wired to the wrong field
	// cannot pass by coincidence.
	repos := staticRepos(
		scanner.Repo{Path: "/repos/alpha", DisplayName: "alpha"},
		scanner.Repo{Path: "/repos/beta", DisplayName: "beta"},
	)
	records := []flowstore.FlowRecord{
		{
			FlowID: "a1", Title: "a1", RepoPath: "/repos/alpha", Instructions: strings.Repeat("x", 900),
			Phases: []flowstore.FlowPhase{
				{PhaseID: "p1", Title: "p1", DependsOn: []string{"a", "b", "c"}, CreatedAt: created, UpdatedAt: created},
			},
			CreatedAt: created, UpdatedAt: created,
		},
		{FlowID: "a2", Title: "a2", RepoPath: "/repos/alpha", CreatedAt: created, UpdatedAt: created},
		{FlowID: "a3", Title: "a3", RepoPath: "/repos/alpha", CreatedAt: created, UpdatedAt: created},
		{FlowID: "b1", Title: "b1", RepoPath: "/repos/beta", CreatedAt: created, UpdatedAt: created},
	}
	bounds := buildSnapshot(repos, staticFlows(records...), nil).bounds()

	for name, got := range map[string][2]int{
		"repos":             {bounds.repos, 2},
		"flows":             {bounds.flows, 4},
		"flowsPerRepo":      {bounds.flowsPerRepo, 3},
		"phasesPerFlow":     {bounds.phasesPerFlow, 1},
		"dependsOnPerPhase": {bounds.dependsOnPerPhase, 3},
	} {
		if got[0] != got[1] {
			t.Errorf("bounds.%s = %d, want %d", name, got[0], got[1])
		}
	}
	// Widths are the *encoded* width, so they include the surrounding quotes.
	if got := bounds.valueBytes("Flow", "instructions"); got != 902 {
		t.Errorf("valueBytes(Flow.instructions) = %d, want 902", got)
	}
	if got, want := bounds.valueBytes("Repo", "path"), int64(len(`"/repos/alpha"`)); got != want {
		t.Errorf("valueBytes(Repo.path) = %d, want %d", got, want)
	}
}

func TestJSONStringBytesMatchesTheEncoder(t *testing.T) {
	// The byte budget is only an upper bound if the measured width is never
	// below what the encoder emits. encoding/json expands <, >, & and control
	// bytes to a six-byte \uXXXX, which is a 6x undercount for the markdown
	// that lands in Flow.instructions.
	for _, value := range []string{
		"", "plain text", `"quoted"`, `back\slash`,
		"<script>", "a & b", "less < greater >",
		"tab\tnewline\ncarriage\r", "\x00\x01\x1f",
		"backspace\bformfeed\f", "delete\x7f",
		"héllo wörld", "日本語のテキスト", "emoji 🚀",
		"\u2028\u2029", "\xff\xfe invalid utf8",
		strings.Repeat("<&>", 100),
	} {
		assertEncodedWidth(t, value)
	}

	// jsonStringBytes counts the encoder's rules instead of calling it, so that
	// measuring an unbounded Flow.instructions does not allocate an escaped copy
	// of it on every request. That is only sound while the two agree exactly —
	// and only a *sweep* proves it, since a corpus can only miss a byte quietly.
	for b := 0; b < 256; b++ {
		assertEncodedWidth(t, string([]byte{byte(b)}))
		assertEncodedWidth(t, "pad"+string([]byte{byte(b)})+"pad")
	}
	for r := rune(0); r < 0x3000; r++ {
		assertEncodedWidth(t, string(r))
	}
}

func assertEncodedWidth(t *testing.T, value string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%q) error = %v", value, err)
	}
	if got := jsonStringBytes(value); got != len(encoded) {
		t.Errorf("jsonStringBytes(%q) = %d, want %d (the encoder's own width)", value, got, len(encoded))
	}
}

func TestJSONStringBytesDoesNotAllocate(t *testing.T) {
	// bounds() measures every stored string on every request, whether or not the
	// query selects it. Marshalling to measure allocated up to six times the
	// value per call, before any budget had been consulted, across up to
	// MaxInFlight requests at once.
	value := strings.Repeat("<a & b> héllo\n", 4096)
	if allocs := testing.AllocsPerRun(50, func() { jsonStringBytes(value) }); allocs != 0 {
		t.Errorf("jsonStringBytes allocated %v times per call, want 0", allocs)
	}
}

func TestSnapshotBoundsCountPhasesAsResolved(t *testing.T) {
	// Flow.phases resolves flowstore.OrderedPhases, which re-appends a
	// parent's children once per top-level row sharing that parent's id — the
	// pre-normalization duplicate shape. len(Record.Phases) is therefore not
	// an upper bound, and the cost limit is only sound if bounds() measures
	// what the resolver actually returns.
	created := fixtureTime(0)
	phases := []flowstore.FlowPhase{}
	for i := 0; i < 20; i++ {
		phases = append(phases, flowstore.FlowPhase{PhaseID: "plan", Title: "Plan", Order: i, CreatedAt: created, UpdatedAt: created})
	}
	for i := 0; i < 20; i++ {
		phases = append(phases, flowstore.FlowPhase{
			PhaseID: fmt.Sprintf("child-%d", i), ParentPhaseID: "plan", Title: "Child",
			Order: i, CreatedAt: created, UpdatedAt: created,
		})
	}
	resolved := len(flowstore.OrderedPhases(phases))
	if resolved <= len(phases) {
		t.Fatalf("fixture resolves %d phases from %d inputs; it no longer expands, so it proves nothing", resolved, len(phases))
	}

	bounds := buildSnapshot(nil, staticFlows(flowstore.FlowRecord{
		FlowID: "f1", Title: "f1", RepoPath: "/repos/alpha", Phases: phases,
		CreatedAt: created, UpdatedAt: created,
	}), nil).bounds()
	if bounds.phasesPerFlow < resolved {
		t.Fatalf("bounds.phasesPerFlow = %d, want >= %d (the number Flow.phases actually resolves)",
			bounds.phasesPerFlow, resolved)
	}
}
