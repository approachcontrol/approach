package version

import (
	"fmt"
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

func String() string {
	version, commit, date := resolvedValues()

	return fmt.Sprintf(
		"wtui %s (%s) built %s",
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
