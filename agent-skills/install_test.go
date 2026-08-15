package agentskills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runInstall invokes install.sh with the given arguments and returns its
// combined output. It fails the test if the exit status does not match wantOK.
func runInstall(t *testing.T, wantOK bool, args ...string) string {
	t.Helper()
	script := filepath.Join(repoRoot(t), "agent-skills", "install.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	out, err := cmd.CombinedOutput()
	if wantOK && err != nil {
		t.Fatalf("install.sh %v failed: %v\n%s", args, err, out)
	}
	if !wantOK && err == nil {
		t.Fatalf("install.sh %v unexpectedly succeeded\n%s", args, out)
	}
	return string(out)
}

func skillSource(t *testing.T, skill string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "agent-skills", skill)
}

// requireLink asserts that dest is a symlink resolving to the in-repo skill.
func requireLink(t *testing.T, dest, skill string) {
	t.Helper()
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("lstat %s: %v", dest, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", dest)
	}
	got, err := filepath.EvalSymlinks(dest)
	if err != nil {
		t.Fatalf("resolve %s: %v", dest, err)
	}
	want, err := filepath.EvalSymlinks(skillSource(t, skill))
	if err != nil {
		t.Fatalf("resolve source %s: %v", skill, err)
	}
	if got != want {
		t.Fatalf("%s resolves to %s, want %s", dest, got, want)
	}
}

func TestInstallLinksAllSkills(t *testing.T) {
	target := t.TempDir()
	runInstall(t, true, "--target", target)

	for _, skill := range []string{"approach-flow", "approach-flow-create", "approach-plan-persist"} {
		requireLink(t, filepath.Join(target, skill), skill)
		if _, err := os.Stat(filepath.Join(target, skill, "SKILL.md")); err != nil {
			t.Fatalf("SKILL.md not readable through link for %s: %v", skill, err)
		}
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	target := t.TempDir()
	runInstall(t, true, "--target", target)
	out := runInstall(t, true, "--target", target)

	if !strings.Contains(out, "already linked") {
		t.Fatalf("second run did not report existing links:\n%s", out)
	}
	if strings.Contains(out, "relink") {
		t.Fatalf("second run needlessly relinked:\n%s", out)
	}
	requireLink(t, filepath.Join(target, "approach-flow"), "approach-flow")
}

// A checkout that moves leaves dangling symlinks behind. Those are ours to
// repair without --force.
func TestInstallRepairsDanglingSymlink(t *testing.T) {
	target := t.TempDir()
	dest := filepath.Join(target, "approach-flow")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone", "approach-flow"), dest); err != nil {
		t.Fatalf("seed dangling symlink: %v", err)
	}

	out := runInstall(t, true, "--target", target)
	if !strings.Contains(out, "relink") {
		t.Fatalf("expected a relink for the dangling symlink:\n%s", out)
	}
	requireLink(t, dest, "approach-flow")
}

func TestInstallSkipsRealDirectoryWithoutForce(t *testing.T) {
	target := t.TempDir()
	dest := filepath.Join(target, "approach-flow")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("seed directory: %v", err)
	}
	marker := filepath.Join(dest, "local.md")
	if err := os.WriteFile(marker, []byte("hand written"), 0o644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	out := runInstall(t, false, "--target", target)
	if !strings.Contains(out, "use --force") {
		t.Fatalf("expected a skip explaining --force:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("existing directory was clobbered: %v", err)
	}
	// The other skills still install.
	requireLink(t, filepath.Join(target, "approach-plan-persist"), "approach-plan-persist")

	out = runInstall(t, true, "--target", target, "--force")
	if !strings.Contains(out, "replace") {
		t.Fatalf("expected --force to replace the directory:\n%s", out)
	}
	requireLink(t, dest, "approach-flow")
}

func TestInstallDryRunTouchesNothing(t *testing.T) {
	target := t.TempDir()
	out := runInstall(t, true, "--target", target, "--dry-run")

	if !strings.Contains(out, "dry run") {
		t.Fatalf("expected dry-run summary:\n%s", out)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry run created %d entries in %s", len(entries), target)
	}
}

func TestInstallCopyMode(t *testing.T) {
	target := t.TempDir()
	runInstall(t, true, "--target", target, "--copy")

	dest := filepath.Join(target, "approach-flow")
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("lstat %s: %v", dest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("--copy produced a symlink at %s", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("copied skill missing SKILL.md: %v", err)
	}
}

func TestInstallRejectsUnknownOption(t *testing.T) {
	out := runInstall(t, false, "--nope")
	if !strings.Contains(out, "Unknown option") {
		t.Fatalf("expected unknown-option error:\n%s", out)
	}
}
