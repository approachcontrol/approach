package gitquery

import (
	"errors"
	"strings"
	"testing"
)

func TestPartialQueryDiagnosticIsBoundedAndTerminalSafe(t *testing.T) {
	cause := errors.New(strings.Repeat("x", 2000) + "\n\x1b]8;;https://example.invalid\a")
	warnings := queryWarnings{}
	for i := 0; i < maxQueryWarnings+5; i++ {
		warnings.add("dirty status", "/repo/worktree", cause)
	}
	partial := warnings.err().(*PartialQueryError)
	if len(partial.Warnings) != maxQueryWarnings {
		t.Fatalf("warnings = %d, want cap %d", len(partial.Warnings), maxQueryWarnings)
	}
	diagnostic := partial.Error()
	if len(diagnostic) > 800 {
		t.Fatalf("diagnostic length = %d, want bounded output", len(diagnostic))
	}
	if strings.ContainsAny(diagnostic, "\n\x1b\a") {
		t.Fatalf("diagnostic contains terminal control bytes: %q", diagnostic)
	}
}
