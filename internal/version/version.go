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
	Version = defaultVersion
	Commit  = defaultCommit
	Date    = defaultDate

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
	version := valueOrDefault(Version, defaultVersion)
	commit := valueOrDefault(Commit, defaultCommit)
	date := valueOrDefault(Date, defaultDate)

	if version != defaultVersion || commit != defaultCommit || date != defaultDate {
		return version, commit, date
	}

	info, ok := readBuildInfo()
	if !ok || info == nil {
		return version, commit, date
	}

	if isUsefulModuleVersion(info.Main.Version) {
		version = info.Main.Version
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			commit = valueOrDefault(setting.Value, commit)
		case "vcs.time":
			date = valueOrDefault(setting.Value, date)
		}
	}

	return version, commit, date
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
