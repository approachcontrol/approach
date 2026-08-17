package version

import (
	"fmt"
	"regexp"
	"runtime/debug"
)

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	defaultDate    = "unknown"
)

var (
	version = defaultVersion
	commit  = defaultCommit
	date    = defaultDate

	readBuildInfo = debug.ReadBuildInfo
)

// releaseVersionPattern matches the version strings goreleaser stamps for a
// real release (`.Tag`, e.g. v0.10.3). Snapshot builds, `make build` (which
// stamps the literal `dev`), and module pseudo-versions all fail it on purpose:
// anything that is not a published release tag is a development build.
//
// Admitting a prerelease suffix was considered, so that a tagged `v0.11.0-rc1`
// would keep the release artifact root rather than being exiled to
// `approach-dev`. It is rejected because Go module pseudo-versions have the same
// shape — `v0.0.0-20260101120000-abcdef123456` — so a suffix-tolerant pattern
// would classify every `go install` build as a release, which is the direction
// that loses data rather than the direction that inconveniences an RC user.
// Anything shipping RCs should stamp them explicitly instead. TestIsDevelopment
// pins both cases.
var releaseVersionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Version returns the resolved build version, e.g. "v0.10.3" or "dev".
func Version() string {
	resolved, _, _ := resolvedValues()
	return resolved
}

// Commit returns the resolved build commit, e.g. "abc1234" or "unknown".
func Commit() string {
	_, resolved, _ := resolvedValues()
	return resolved
}

// IsDevelopment reports whether this binary is a development build — anything
// whose resolved version is not a published release tag.
//
// Note for callers writing guards: under `go test` the ldflags vars are unset
// and debug.ReadBuildInfo reports "(devel)", so IsDevelopment is true in every
// test binary. A guard keyed on it must be scoped narrowly enough that the
// existing suite still exercises the non-development path.
func IsDevelopment() bool {
	return !releaseVersionPattern.MatchString(Version())
}

func String() string {
	version, commit, date := resolvedValues()

	return fmt.Sprintf(
		"approach %s (%s) built %s",
		version,
		commit,
		date,
	)
}

func resolvedValues() (string, string, string) {
	resolvedVersion := valueOrDefault(version, defaultVersion)
	resolvedCommit := valueOrDefault(commit, defaultCommit)
	resolvedDate := valueOrDefault(date, defaultDate)

	if resolvedVersion != defaultVersion || resolvedCommit != defaultCommit || resolvedDate != defaultDate {
		return resolvedVersion, resolvedCommit, resolvedDate
	}

	info, ok := readBuildInfo()
	if !ok || info == nil {
		return resolvedVersion, resolvedCommit, resolvedDate
	}

	if isUsefulModuleVersion(info.Main.Version) {
		resolvedVersion = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			resolvedCommit = valueOrDefault(setting.Value, resolvedCommit)
		case "vcs.time":
			resolvedDate = valueOrDefault(setting.Value, resolvedDate)
		}
	}

	return resolvedVersion, resolvedCommit, resolvedDate
}

func isUsefulModuleVersion(version string) bool {
	return version != "" && version != "(devel)"
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
