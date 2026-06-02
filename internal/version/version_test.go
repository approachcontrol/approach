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
			want:    "wtui dev (unknown) built unknown",
		},
		{
			name:    "release build",
			version: "v0.1.0",
			commit:  "abc1234",
			date:    "2026-04-19T10:20:30Z",
			want:    "wtui v0.1.0 (abc1234) built 2026-04-19T10:20:30Z",
		},
		{
			name:    "empty values fall back",
			version: "",
			commit:  "",
			date:    "",
			want:    "wtui dev (unknown) built unknown",
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
				Path:    "github.com/brian-bell/wtui",
				Version: "v0.1.0",
			},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-04-19T10:20:30Z"},
			},
		}, true
	}

	want := "wtui v0.1.0 (abcdef1234567890) built 2026-04-19T10:20:30Z"
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
				Path:    "github.com/brian-bell/wtui",
				Version: "v0.1.0",
			},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "build-info-commit"},
				{Key: "vcs.time", Value: "2026-04-19T10:20:30Z"},
			},
		}, true
	}

	want := "wtui v0.2.0 (ldflags-commit) built 2026-05-10T12:00:00Z"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
