package model

import (
	"regexp"
	"testing"
)

func TestNewLaunchIDIncludesRandomSuffix(t *testing.T) {
	id := newLaunchID()
	if !regexp.MustCompile(`^approach-\d+-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("launch ID %q does not include the random suffix", id)
	}
}
