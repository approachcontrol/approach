// Package regression_test is the cross-package incident suite: it exists to
// fail on the schema-N controller / schema-(N-1) CLI incident, and its
// neighbours, rather than letting a user find them.
//
// Everything here is a _test.go file. There is no library to import, and there
// must not be: the value of this package is exactly what a unit test cannot
// reach — a REAL second binary, and the command line a generated prompt
// actually tells an agent to run. The mechanisms below are each already
// covered in isolation by their own packages' tests; every one of those passed
// throughout the incident, because none of them ran the shipped command line.
package regression_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// approachBinary is the real `approach` under test, built once for the whole
// package. Nothing can stand in for it: a Go function call would exercise this
// process's build of every package, and the incident was a mismatch BETWEEN
// builds.
var approachBinary string

// oldCLIMarkerEnv names the file the stub "old CLI" touches when it is invoked.
// Its absence is the assertion that matters: a pinned launch must never reach
// the `approach` that happens to be first on PATH.
const (
	oldCLIMarkerEnv  = "APPROACH_REGRESSION_OLD_CLI_MARKER"
	oldCLIHelperEnv  = "APPROACH_REGRESSION_OLD_CLI"
	oldCLISchemaEnv  = "APPROACH_REGRESSION_OLD_CLI_SCHEMA"
	targetSchemaEnv  = "APPROACH_DB_SCHEMA"
	helperTestFilter = "^TestHelperOldCLI$"
)

func TestMain(m *testing.M) {
	flag.Parse()
	// The helper re-exec lands here before any test runs. It must not build a
	// binary, and it must not run the suite.
	if os.Getenv(oldCLIHelperEnv) != "" {
		os.Exit(runOldCLIHelper())
	}
	if testing.Short() {
		// -short skips the whole package rather than each test: the cost this
		// escape hatch exists for is the `go build` below, which happens once
		// whether one test runs or all of them.
		fmt.Fprintln(os.Stderr, "regression: skipped under -short (it builds cmd/approach)")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "approach-regression-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "regression: temp dir: %v\n", err)
		os.Exit(1)
	}
	approachBinary = filepath.Join(dir, "approach")
	build := exec.Command("go", "build", "-o", approachBinary, "github.com/approachcontrol/approach/cmd/approach")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "regression: build cmd/approach: %v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestHelperOldCLI is the stub older `approach`. It is a test function reached
// by re-execing this test binary — the standard helper-process pattern —
// rather than a second `go build`, which would double the package's cost to
// simulate a build whose only interesting behaviour is one refusal.
//
// It is never run as part of the suite: without oldCLIHelperEnv it skips, and
// with it TestMain exits before m.Run.
func TestHelperOldCLI(t *testing.T) {
	t.Skip("helper process; reached only through the PATH shim")
}

// runOldCLIHelper is the stub's whole behaviour: record that it was reached,
// then refuse anything stamped newer than the schema it claims, in the wording
// a real older build uses.
func runOldCLIHelper() int {
	if marker := os.Getenv(oldCLIMarkerEnv); marker != "" {
		// Append rather than truncate, so two invocations are distinguishable
		// from one. The test asserts the file does not exist at all, and this
		// keeps a debugging read of it honest.
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			fmt.Fprintf(file, "%s\n", strings.Join(os.Args[1:], " "))
			_ = file.Close()
		}
	}
	claimed, claimedOK := atoi(os.Getenv(oldCLISchemaEnv))
	target, targetOK := atoi(os.Getenv(targetSchemaEnv))
	if claimedOK && targetOK && target > claimed {
		fmt.Fprintf(os.Stderr,
			"flow database was written by a newer version of approach: database schema %d"+
				" needs an approach build supporting flow database schema %d or newer,"+
				" but this is the stub old CLI (schema %d); upgrade approach\n",
			target, target, claimed)
		return 1
	}
	fmt.Fprintln(os.Stderr, "stub old CLI: refusing to touch a flow database")
	return 1
}

// stubOldCLIOnPath installs the stub under the name `approach` in a fresh
// directory and returns that directory and the marker path.
//
// A shell shim naming this test binary, rather than a copy of it: the helper
// pattern needs os.Args[0] plus an env switch, and the file on PATH has to be
// called `approach` for a bare `approach` in a command line to find it.
func stubOldCLIOnPath(t *testing.T, claimedSchema int) (dir, marker string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub old CLI is a POSIX shell shim")
	}
	dir = t.TempDir()
	marker = filepath.Join(dir, "old-cli-was-invoked")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
%s=1 %s=%d exec %s -test.run='%s' -- "$@"
`, oldCLIHelperEnv, oldCLISchemaEnv, claimedSchema, shellQuote(self), helperTestFilter)
	path := filepath.Join(dir, "approach")
	if err := os.WriteFile(path, []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, marker
}

func atoi(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	return parsed, err == nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
