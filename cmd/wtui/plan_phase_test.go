package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPlanPhaseSetThenListShowsPhase(t *testing.T) {
	root := t.TempDir()
	mustRun(t, []string{"wtui", "plan", "save", "--title", "Phased", "--plan-id", "phased", "--state-root", root}, "body")

	err := run([]string{"wtui", "plan", "phase", "set",
		"--plan-id", "phased", "--phase-id", "p1", "--title", "Tracer bullet", "--status", "completed", "--order", "1", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err != nil {
		t.Fatalf("plan phase set error = %v", err)
	}

	var stdout bytes.Buffer
	if err := run([]string{"wtui", "plan", "list", "--state-root", root, "--json"},
		noScanDeps(t, runDeps{stdout: &stdout})); err != nil {
		t.Fatalf("plan list error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{`"phase_id":"p1"`, `"title":"Tracer bullet"`, `"status":"completed"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %s:\n%s", want, out)
		}
	}
}

func TestRunPlanPhaseSetRequiresIDs(t *testing.T) {
	root := t.TempDir()
	mustRun(t, []string{"wtui", "plan", "save", "--title", "P", "--plan-id", "p", "--state-root", root}, "body")
	err := run([]string{"wtui", "plan", "phase", "set", "--plan-id", "p", "--title", "X", "--state-root", root},
		noScanDeps(t, runDeps{stdout: &bytes.Buffer{}}))
	if err == nil {
		t.Fatal("expected error when --phase-id missing")
	}
}
