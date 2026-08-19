package regression_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
	_ "modernc.org/sqlite"
)

// newRoot returns a scratch state root. Never the default artifact root: a dev
// build migrating that is itself one of the incidents this suite tests for.
func newRoot(t *testing.T) string {
	t.Helper()
	// os.MkdirTemp under /tmp rather than t.TempDir(): the launch controller
	// binds a unix socket under the root on some paths, and macOS t.TempDir()
	// names exceed the sockaddr limit.
	dir, err := os.MkdirTemp("/tmp", "apr-reg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func openStore(t *testing.T, root string) *flowstore.Store {
	t.Helper()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Role: flowstore.RoleMigrator})
	if err != nil {
		t.Fatalf("NewStore(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seedRunningPhase creates a Flow and marks its first phase running under
// launchID, which is the state a launched agent's `flow phase complete` acts on.
func seedRunningPhase(t *testing.T, store *flowstore.Store, launchID string) (flowstore.FlowRecord, flowstore.FlowPhase) {
	t.Helper()
	created, err := store.Create(flowstore.FlowRecord{
		Title:        "Incident regression",
		Instructions: "Exercise the shipped command line.",
		RepoPath:     filepath.Join(t.TempDir(), "repo"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created.Phases) == 0 {
		t.Fatalf("a created Flow has no phases: %#v", created)
	}
	phase := created.Phases[0]
	updated, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{
		FlowID: created.FlowID, PhaseID: phase.PhaseID, LaunchID: launchID,
	})
	if err != nil {
		t.Fatalf("AddPhaseLaunchID: %v", err)
	}
	for _, candidate := range updated.Phases {
		if candidate.PhaseID == phase.PhaseID {
			return updated, candidate
		}
	}
	t.Fatalf("phase %s vanished", phase.PhaseID)
	return flowstore.FlowRecord{}, flowstore.FlowPhase{}
}

func phaseByID(t *testing.T, store *flowstore.Store, flowID, phaseID string) flowstore.FlowPhase {
	t.Helper()
	record, err := store.Read(flowID)
	if err != nil {
		t.Fatalf("Read(%s): %v", flowID, err)
	}
	for _, phase := range record.Phases {
		if phase.PhaseID == phaseID {
			return phase
		}
	}
	t.Fatalf("phase %s not in %s", phaseID, flowID)
	return flowstore.FlowPhase{}
}

// promptBinaryWord extracts the command word a generated prompt tells the agent
// to run approach as.
//
// Taken from the prompt's own text rather than reconstructed, because that is
// the entire point of this package: a test that rebuilt the command word would
// keep passing while the shipped prompt named something else. Every generated
// phase prompt spells at least one persistence command as
// `<bin> <approach subcommand>`, so the word is whatever immediately precedes
// the earliest such subcommand — with its quoting preserved, since the unpinned
// form is a quoted shell expansion and the pinned form may be a quoted path.
func promptBinaryWord(t *testing.T, prompt string) string {
	t.Helper()
	best := -1
	for _, marker := range []string{" flow phase ", " flow plan ", " flow issue ", " plan save", " flow ", " db "} {
		if at := strings.Index(prompt, marker); at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	if best < 0 {
		t.Fatalf("generated prompt names no approach command:\n%s", prompt)
	}
	if word := commandWordEndingAt(prompt, best); word != "" {
		return word
	}
	t.Fatalf("generated prompt names an approach command with no command word:\n%s", prompt)
	return ""
}

// commandWordEndingAt walks left from end to the start of the shell word that
// ends there, honouring one level of quoting so `"${APPROACH_EXECUTABLE:-...}"`
// comes back whole rather than truncated at its first space.
func commandWordEndingAt(text string, end int) string {
	if end <= 0 {
		return ""
	}
	if quote := text[end-1]; quote == '"' || quote == '\'' {
		start := strings.LastIndexByte(text[:end-1], quote)
		if start < 0 {
			return ""
		}
		return text[start:end]
	}
	start := strings.LastIndexAny(text[:end], " \t\n`")
	return strings.TrimSpace(text[start+1 : end])
}

// runShell runs one command line the way an agent would: through a shell, with
// the launch environment, so parameter expansion and PATH lookup are the real
// ones rather than an exec.Command reconstruction of them.
func runShell(t *testing.T, dir, command string, env []string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code = 0
	var exitErr *exec.ExitError
	if err != nil {
		if !asExitError(err, &exitErr) {
			t.Fatalf("run %q: %v", command, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func asExitError(err error, target **exec.ExitError) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		*target = exitErr
		return true
	}
	return false
}

// launchEnv is the environment a launched agent inherits, minus anything the
// ambient shell would contribute. Built from scratch rather than from os.Environ
// so an APPROACH_* variable exported into the developer's own shell cannot
// decide a test's answer.
func launchEnv(root, pathDirs string, extra ...string) []string {
	env := []string{
		"PATH=" + pathDirs,
		"HOME=" + root,
		"APPROACH_FLOW_STATE_ROOT=" + root,
		"APPROACH_PLAN_STATE_ROOT=" + root,
		"APPROACH_SESSION_STATE_ROOT=" + root,
	}
	return append(env, extra...)
}

// stampUserVersion rewrites a database's schema stamp. It is how this package
// stages a database from a build that does not exist: a real newer approach
// cannot be built here, and the stamp is the only thing the compatibility
// decisions key on.
func stampUserVersion(t *testing.T, root string, version int64) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "approach.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatalf("stamp user_version = %d: %v", version, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func decodeJSON[T any](t *testing.T, data string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
	return value
}
