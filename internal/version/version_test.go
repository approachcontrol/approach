package version

import "testing"

func TestString(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

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
			Version, Commit, Date = tt.version, tt.commit, tt.date

			if got := String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
