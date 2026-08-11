package graphqlapi

import (
	"errors"
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
