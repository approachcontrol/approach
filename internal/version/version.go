package version

import "fmt"

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	defaultDate    = "unknown"
)

var (
	Version = defaultVersion
	Commit  = defaultCommit
	Date    = defaultDate
)

func String() string {
	return fmt.Sprintf(
		"wtui %s (%s) built %s",
		valueOrDefault(Version, defaultVersion),
		valueOrDefault(Commit, defaultCommit),
		valueOrDefault(Date, defaultDate),
	)
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
