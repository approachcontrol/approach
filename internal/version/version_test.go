package version

import (
	"runtime/debug"
	"testing"
)

func TestString(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
		readBuildInfo = originalReadBuildInfo
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return nil, false
	}

	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		want    string
	}{
		{
			name:    "defaults",
			version: "dev",
			commit:  "unknown",
			date:    "unknown",
			want:    "approach dev (unknown) built unknown",
		},
		{
			name:    "release build",
			version: "v0.1.0",
			commit:  "abc1234",
			date:    "2026-04-19T10:20:30Z",
			want:    "approach v0.1.0 (abc1234) built 2026-04-19T10:20:30Z",
		},
		{
			name:    "empty values fall back",
			version: "",
			commit:  "",
			date:    "",
			want:    "approach dev (unknown) built unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, commit, date = tt.version, tt.commit, tt.date

			if got := String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStringFallsBackToBuildInfoWhenLdflagsDefault(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
		readBuildInfo = originalReadBuildInfo
	})

	version, commit, date = defaultVersion, defaultCommit, defaultDate
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{
				Path:    "github.com/approachcontrol/approach",
				Version: "v0.1.0",
			},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-04-19T10:20:30Z"},
			},
		}, true
	}

	want := "approach v0.1.0 (abcdef1234567890) built 2026-04-19T10:20:30Z"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestStringPrefersLdflagsOverBuildInfo(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
		readBuildInfo = originalReadBuildInfo
	})

	version, commit, date = "v0.2.0", "ldflags-commit", "2026-05-10T12:00:00Z"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{
				Path:    "github.com/approachcontrol/approach",
				Version: "v0.1.0",
			},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "build-info-commit"},
				{Key: "vcs.time", Value: "2026-04-19T10:20:30Z"},
			},
		}, true
	}

	want := "approach v0.2.0 (ldflags-commit) built 2026-05-10T12:00:00Z"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestVersionAndCommitAccessors(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
		readBuildInfo = originalReadBuildInfo
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }

	version, commit, date = "v0.10.3", "abc1234", "2026-08-16T00:00:00Z"
	if got := Version(); got != "v0.10.3" {
		t.Fatalf("Version() = %q, want %q", got, "v0.10.3")
	}
	if got := Commit(); got != "abc1234" {
		t.Fatalf("Commit() = %q, want %q", got, "abc1234")
	}
	if got := String(); got != "approach v0.10.3 (abc1234) built 2026-08-16T00:00:00Z" {
		t.Fatalf("String() = %q", got)
	}
}

func TestIsDevelopment(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		version, commit, date = originalVersion, originalCommit, originalDate
		readBuildInfo = originalReadBuildInfo
	})
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		version string
		want    bool
	}{
		{version: "dev", want: true},
		{version: "", want: true},
		{version: "v0.10.3", want: false},
		{version: "v1.0.0", want: false},
		{version: "0.10.3", want: true},
		{version: "v0.10.3-next", want: true},
		{version: "v0.0.0-20260101120000-abcdef123456", want: true},
		{version: "(devel)", want: true},
	}
	for _, test := range tests {
		version, commit, date = test.version, "abc1234", "2026-08-16T00:00:00Z"
		if got := IsDevelopment(); got != test.want {
			t.Fatalf("IsDevelopment() for %q = %v, want %v", test.version, got, test.want)
		}
	}
}
