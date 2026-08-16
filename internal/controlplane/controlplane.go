// Package controlplane pins the approach binary that launched a Flow agent.
//
// # Why a checksum-named cache rather than a path pin
//
// An agent that resolves `approach` from ambient PATH can be a different build
// from the TUI that launched it. That is the incident this package exists to
// prevent: the launcher migrated the database to a newer schema, the agent
// resolved an older release from PATH, and its phase result could not be
// persisted at all.
//
// The obvious fix — record the launcher's resolved path and re-hash it before
// each launch — is not merely weaker here, it is actively worse. Homebrew ships
// approach as a cask binary symlinked into /opt/homebrew/bin, so resolving
// symlinks pins a versioned Caskroom path that `brew upgrade` deletes. A path
// pin would then fail Verify permanently and refuse every launch for the life of
// the TUI process: a total launch outage traded for a rare wrong-binary launch.
//
// So Resolve copies the running binary into <root>/bin/approach-<digest>, and a
// launch runs the copy. A copy cannot be replaced underneath us, and its name is
// its content, so Verify is a re-hash rather than a trust decision. The residual
// TOCTOU window between Verify and exec is accepted and stated rather than
// papered over.
//
// # Trust
//
// The TUI executes a file out of the state root, so that root's trust
// properties become load-bearing. Resolve secures the root exactly the way the
// flow store does (artifacts.SecureCanonicalRoot), the cache directory is 0700,
// cached binaries are 0500, and a cached copy whose digest does not match its
// name is never used. A world-writable state root was already a full compromise
// — it holds the database the TUI writes — so this widens the consequence, not
// the precondition.
//
// # Degradation
//
// A caching failure must never block work. When the cache cannot be created
// (read-only or full state root), Resolve falls back to the resolved source path
// and returns a Pin whose Notice names the degradation; callers surface it
// through their status channel and keep launching.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/version"
)

const (
	// cacheDirName is the runtime binary cache, relative to the state root.
	cacheDirName = "bin"
	// pinsDirName holds one file per live launch naming the digest that launch's
	// argv still points at. Retention refuses to evict a pinned digest.
	pinsDirName = "pins"
	// cachedBinaryPrefix names a cached copy. The remainder is the digest
	// prefix, so the file name is the content.
	cachedBinaryPrefix = "approach-"
	// digestNameLength is how much of the hex digest lands in the file name.
	// 64 bits of prefix is far past the point where a collision is a realistic
	// concern for a handful of local builds.
	digestNameLength = 16
	// retainedBinaries bounds the cache by count. Copies of a ~30 MB binary are
	// not free, but neither is losing the session hook of a detached agent.
	retainedBinaries = 3

	cachedBinaryPerm os.FileMode = 0o500
)

// pinClaimMaxAge bounds how long a claim can exempt a cached binary from
// retention, measured from the last time anything reported the launch alive.
//
// It is the only *automatic* retirement path for a detached launch — an
// operator releasing the phase session still runs FinalizeAgentSession, which
// releases. Otherwise the TUI deliberately skips finalization for detached
// launches, and the provider session hook restamps rather than releases because
// no provider hook here is a reliable death certificate (Codex's Stop is
// per-turn; Claude's SessionEnd also fires on /clear). So a detached launch
// holds its digest until this long after its last hook — a real, bounded disk
// cost of roughly one cached binary per distinct build launched inside the
// window, accepted against evicting a binary a live agent still has to exec.
//
// A month rather than a week because the failure it guards is silent: expire a
// claim early and a long-lived session loses the hook that captures it. Nothing
// re-derives that.
const pinClaimMaxAge = 30 * 24 * time.Hour

// timeNow is time.Now, replaced in tests so claim expiry can be exercised
// without sleeping.
var timeNow = time.Now

// resolveExecutable is os.Executable, replaced in tests so they can stand in a
// disposable "running binary" and delete or replace it.
var resolveExecutable = os.Executable

// Pin identifies the approach binary a launch must invoke.
type Pin struct {
	// ExecutablePath is what a launch runs: the cached copy, or the resolved
	// source path when Degraded.
	ExecutablePath string
	// SourcePath is the resolved location of the running binary.
	SourcePath string
	// Version and Commit describe the pinned build.
	Version string
	Commit  string
	// SchemaVersion is the flow database schema this build writes.
	SchemaVersion int
	// Digest is the full hex SHA-256 of the pinned binary.
	Digest string
	// Degraded reports that the cache could not be created and ExecutablePath
	// is the source path, which an upgrade can replace underneath the process.
	Degraded bool
	// Notice names the degradation for the caller's status channel. It is empty
	// unless Degraded.
	Notice string
}

// Errors Verify returns. They are typed because the caller's refusal message
// differs by cause: a missing copy is a wiped state root, a digest mismatch is
// tampering or an upgrade under a degraded pin.
var (
	ErrPinMissing        = errors.New("pinned approach executable is missing")
	ErrPinNotExecutable  = errors.New("pinned approach executable is not executable")
	ErrPinDigestMismatch = errors.New("pinned approach executable does not match its recorded digest")
)

// SourceIdentity is the running binary's resolved path and content digest.
//
// It is a separate value, and CaptureSource a separate call, because the two
// halves of pinning have very different timing needs. Hashing answers "which
// build is running", and that answer decays: os.Executable returns a *pathname*,
// Homebrew ships approach as a cask binary symlinked into /opt/homebrew/bin, and
// an upgrade landing before the hash would have this process pin a build that is
// not the one running. Materializing needs the state root, which is not known
// until config is loaded and repositories are scanned. Splitting them lets the
// hash run at the earliest moment the process can manage while the copy waits
// for the root.
type SourceIdentity struct {
	Path   string
	Digest string
}

// CaptureSource resolves and hashes the running binary. It takes no root, so it
// can be called before anything slow — a repository scan, a config load, a
// database migration — has had a chance to overlap an upgrade. Nothing here can
// make the answer atomic with process start; Go exposes no portable handle on
// the running image. Calling it early is the whole mitigation.
func CaptureSource() (SourceIdentity, error) {
	path, err := resolveSourcePath()
	if err != nil {
		return SourceIdentity{}, err
	}
	digest, err := fileDigest(path)
	if err != nil {
		return SourceIdentity{}, fmt.Errorf("hash approach executable %s: %w", path, err)
	}
	return SourceIdentity{Path: path, Digest: digest}, nil
}

// Materialize returns the Pin for an already-captured source, copying it into
// the cache under root when it can. It cannot fail: every cache problem degrades
// to the source path with a Notice, because a caching failure must never block
// work.
//
// schemaVersion is passed in rather than read from flowstore so this package
// stays free of the store's dependency graph; callers pass
// flowstore.DatabaseSchemaVersion().
func Materialize(root string, source SourceIdentity, schemaVersion int) Pin {
	pin := Pin{
		ExecutablePath: source.Path,
		SourcePath:     source.Path,
		Version:        version.Version(),
		Commit:         version.Commit(),
		SchemaVersion:  schemaVersion,
		Digest:         source.Digest,
	}
	cached, err := materialize(root, source.Path, source.Digest)
	if err != nil {
		pin.Degraded = true
		pin.Notice = fmt.Sprintf(
			"Launch binary cache unavailable (%v); launching %s directly, which an upgrade can replace mid-session.",
			err, source.Path)
		return pin
	}
	pin.ExecutablePath = cached
	return pin
}

// Resolve captures and materializes in one step, for callers that have the root
// as early as they have anything. It only fails when the running binary itself
// cannot be resolved or hashed.
func Resolve(root string, schemaVersion int) (Pin, error) {
	source, err := CaptureSource()
	if err != nil {
		return Pin{}, err
	}
	return Materialize(root, source, schemaVersion), nil
}

// Verify re-checks the pinned binary immediately before a launch. A degraded
// pin verifies its source, which is exactly the check that catches an upgrade
// replacing the binary underneath a long-lived TUI.
func (p Pin) Verify() error {
	if strings.TrimSpace(p.ExecutablePath) == "" {
		return fmt.Errorf("%w: no path recorded", ErrPinMissing)
	}
	info, err := os.Stat(p.ExecutablePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrPinMissing, p.ExecutablePath)
		}
		return fmt.Errorf("inspect pinned approach executable %s: %w", p.ExecutablePath, err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrPinMissing, p.ExecutablePath)
	}
	if info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("%w: %s has mode %v", ErrPinNotExecutable, p.ExecutablePath, info.Mode().Perm())
	}
	digest, err := fileDigest(p.ExecutablePath)
	if err != nil {
		return fmt.Errorf("hash pinned approach executable %s: %w", p.ExecutablePath, err)
	}
	if digest != p.Digest {
		return fmt.Errorf("%w: %s", ErrPinDigestMismatch, p.ExecutablePath)
	}
	return nil
}

// PathMismatchNotice reports, without blocking anything, that the `approach` a
// launched agent would find on ambient PATH is a different build from the pin.
// It is context for a confusing session, not a gate: the pin already guarantees
// which binary the agent runs. An empty string means "nothing worth saying".
//
// lookPath is exec.LookPath when nil.
func PathMismatchNotice(pin Pin, lookPath func(string) (string, error)) string {
	if pin.Notice != "" {
		return pin.Notice
	}
	if strings.TrimSpace(pin.ExecutablePath) == "" {
		return ""
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	resolved, err := lookPath("approach")
	if err != nil || strings.TrimSpace(resolved) == "" {
		return ""
	}
	if resolved == pin.ExecutablePath || resolved == pin.SourcePath {
		return ""
	}
	if digest, err := fileDigest(resolved); err == nil && digest == pin.Digest {
		return ""
	}
	return "approach on PATH (" + resolved + ") is a different build from the one launching agents; launches use the pinned binary."
}

// RetainPin records that launchID's argv points at digest, so retention will not
// evict it. The launch's session-hook command bakes the executable path into the
// agent's argv, so a detached agent that outlives a few rebuilds would otherwise
// silently lose its session capture.
func RetainPin(root, launchID, digest string) error {
	if !artifacts.IsSafeID(launchID) {
		return fmt.Errorf("invalid launch id %q", launchID)
	}
	dir := filepath.Join(root, cacheDirName, pinsDirName)
	if err := os.MkdirAll(dir, artifacts.DirPerm); err != nil {
		return fmt.Errorf("create launch pin directory: %w", err)
	}
	return artifacts.WriteFileAtomic(filepath.Join(dir, launchID), []byte(digest+"\n"))
}

// RefreshPin restamps launchID's claim so expiry measures time since the
// launch was last known alive rather than time since it started. A missing claim
// is success: there is nothing to keep alive, and a provider whose hook fires
// for an untracked launch must not be turned into an error.
//
// This exists because not every provider hook means "the agent is done". Codex
// wires Stop, which fires once per TURN, so treating that hook as end-of-life
// would drop the claim while the agent is still live and still bound to the
// pinned path baked into its argv — and retention could then evict the binary
// out from under it, which is the whole failure this package prevents.
func RefreshPin(root, launchID string) error {
	if !artifacts.IsSafeID(launchID) {
		return fmt.Errorf("invalid launch id %q", launchID)
	}
	path := filepath.Join(root, cacheDirName, pinsDirName, launchID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect launch pin: %w", err)
	}
	now := timeNow()
	if err := os.Chtimes(path, now, now); err != nil {
		return fmt.Errorf("refresh launch pin: %w", err)
	}
	return nil
}

// ReleasePin drops launchID's claim on its digest. A missing claim is success:
// releasing twice, or releasing a launch that never pinned, is not an error.
func ReleasePin(root, launchID string) error {
	if !artifacts.IsSafeID(launchID) {
		return fmt.Errorf("invalid launch id %q", launchID)
	}
	err := os.Remove(filepath.Join(root, cacheDirName, pinsDirName, launchID))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release launch pin: %w", err)
	}
	return nil
}

func resolveSourcePath() (string, error) {
	executable, err := resolveExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve approach executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		// A binary behind a broken symlink is still worth pinning by its own
		// path; the digest check is what makes the pin trustworthy.
		return executable, nil
	}
	return resolved, nil
}

// materialize copies source into the cache under its content-addressed name and
// returns the cached path. An existing copy is reused only when its digest still
// matches its name.
func materialize(root, source, digest string) (string, error) {
	canonicalRoot, err := artifacts.SecureCanonicalRoot(root, "approach state root")
	if err != nil {
		return "", err
	}
	cacheDir, err := secureCacheDir(canonicalRoot)
	if err != nil {
		return "", err
	}
	target := filepath.Join(cacheDir, cachedBinaryName(digest))
	if !reusable(target, digest) {
		if err := copyExecutable(source, target, digest); err != nil {
			return "", err
		}
	}
	if err := requireRunnable(target); err != nil {
		return "", err
	}
	sweepCache(cacheDir, target)
	return target, nil
}

// probeTimeout bounds the runnability probe. It is generous for `--version` on a
// local binary and still short enough that a pathological filesystem cannot hang
// startup.
const probeTimeout = 10 * time.Second

// requireRunnable checks that this process can actually execute the cached copy,
// by executing it.
//
// A mode bit is not that check. The state root is an ordinary directory the user
// chose, and a `noexec` mount happily holds a 0500 file that execve refuses — so
// every permission test in this package would pass while the only thing the
// cache exists for, running the binary, fails. Nothing notices until an agent is
// already working: the provider hook and the agent's own `approach` commands are
// what use this path, so the phase is running by the time persistence dies.
//
// Failure is not fatal, because the caller treats a materialization error as a
// reason to degrade: the pin falls back to the source path, which this process
// is executing and therefore demonstrably can, and the Notice says why. That is
// the same trade the rest of the cache makes — never block work over a cache.
//
// `--version` is the probe because it is the one subcommand guaranteed to touch
// no state: it prints and exits before any root, database, or config is opened.
func requireRunnable(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// Inherit nothing. The probe must not pick up an APPROACH_* variable that
	// would send it at a real state root, and it has no reason to read stdin.
	cmd.Env = []string{}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cached approach executable %s is not runnable: %w", path, err)
	}
	return nil
}

// secureCacheDir creates <root>/bin and returns it only when it is a real
// owner-only directory. The Lstat is the point: MkdirAll succeeds on an existing
// symlink to a directory and Chmod follows it, so without this the package doc's
// "the cache directory is 0700" would be a claim about a directory somewhere
// else entirely — one an attacker chose, holding files this process then
// executes. root is already canonical, so only the final component is at issue.
func secureCacheDir(root string) (string, error) {
	cacheDir := filepath.Join(root, cacheDirName)
	if err := os.MkdirAll(cacheDir, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("create launch binary cache: %w", err)
	}
	info, err := os.Lstat(cacheDir)
	if err != nil {
		return "", fmt.Errorf("inspect launch binary cache: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("launch binary cache %q must be a real directory", cacheDir)
	}
	if err := os.Chmod(cacheDir, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("secure launch binary cache: %w", err)
	}
	// Re-stat rather than trust the chmod, the way SecureCanonicalRoot does. The
	// package doc states 0700 as a property of this directory; on a filesystem
	// that silently ignores mode bits that would otherwise be an assertion the
	// code never checks.
	info, err = os.Lstat(cacheDir)
	if err != nil {
		return "", fmt.Errorf("re-inspect launch binary cache: %w", err)
	}
	if info.Mode().Perm() != artifacts.DirPerm {
		return "", fmt.Errorf("launch binary cache %q permissions are not 0700", cacheDir)
	}
	return cacheDir, nil
}

// reusable reports whether an existing cached copy can be used as-is. The digest
// re-check is the point: the file name is a claim, not a guarantee.
func reusable(target, digest string) bool {
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
		return false
	}
	actual, err := fileDigest(target)
	return err == nil && actual == digest
}

// copyExecutable stages source into the cache and publishes it under its
// content-addressed name, but only once the bytes it actually read hash to
// expected.
//
// That check is the point, not a formality. The digest was captured before the
// repository scan and the copy happens after it, precisely because the path is
// mutable — so the window this split exists to survive is the same window in
// which `source` can be a *different file* by the time it is read. Without the
// comparison, a replacement would be copied under the old build's digest name
// and then executed by the runnability probe, which is the one place this
// package runs an unverified binary. Pin.Verify would refuse launches later, but
// later is after the exec.
func copyExecutable(source, target, expected string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open approach executable %s: %w", source, err)
	}
	defer in.Close()

	dir := filepath.Dir(target)
	temp, err := os.CreateTemp(dir, ".tmp-approach-*")
	if err != nil {
		return fmt.Errorf("stage cached approach executable: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), in); err != nil {
		_ = temp.Close()
		return fmt.Errorf("copy approach executable: %w", err)
	}
	if staged := hex.EncodeToString(hash.Sum(nil)); staged != expected {
		_ = temp.Close()
		return fmt.Errorf(
			"approach executable %s changed while it was being cached (expected %s, read %s); it was probably upgraded mid-startup",
			source, expected[:digestNameLength], staged[:digestNameLength])
	}
	if err := temp.Chmod(cachedBinaryPerm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure cached approach executable: %w", err)
	}
	// fsync before rename: a cached binary that is visible but not durable would
	// survive a crash as a truncated file that still carries a valid-looking
	// name, and the digest check would then refuse every launch.
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("flush cached approach executable: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close cached approach executable: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("publish cached approach executable: %w", err)
	}
	return nil
}

// sweepCache bounds the cache by count, newest first, never evicting the copy
// this launch just resolved and never evicting a digest a live launch still
// points at. A sweep failure is not reported: retention is hygiene, and failing
// a launch over it would trade a bounded disk cost for an outage.
func sweepCache(cacheDir, keep string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	pinned := pinnedDigests(filepath.Join(cacheDir, pinsDirName))
	type cached struct {
		name    string
		modTime int64
	}
	candidates := make([]cached, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), cachedBinaryPrefix) {
			continue
		}
		if filepath.Join(cacheDir, entry.Name()) == keep {
			continue
		}
		if pinned[strings.TrimPrefix(entry.Name(), cachedBinaryPrefix)] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, cached{name: entry.Name(), modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime != candidates[j].modTime {
			return candidates[i].modTime > candidates[j].modTime
		}
		return candidates[i].name < candidates[j].name
	})
	// keep counts against the budget, so only retainedBinaries-1 others survive.
	pinsDir := filepath.Join(cacheDir, pinsDirName)
	for i, candidate := range candidates {
		if i < retainedBinaries-1 {
			continue
		}
		// Re-read the claims immediately before the unlink rather than trusting
		// the snapshot taken above. Sweeps are not serialized against launches
		// in other processes, and everything between that snapshot and here —
		// reading the whole directory, stat-ing each entry, sorting — is time a
		// peer can use to claim this very digest for a launch whose argv already
		// names it. Re-reading cannot make the check atomic, but it shrinks the
		// window from "the length of a sweep" to "two syscalls", which is the
		// difference between a race a busy machine loses regularly and one it
		// effectively never does. Full cross-process serialization of claim,
		// refresh, release, and sweep is a lock this package does not have yet.
		if pinnedDigests(pinsDir)[strings.TrimPrefix(candidate.name, cachedBinaryPrefix)] {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, candidate.name))
	}
}

// pinnedDigests returns the digest name prefixes live launches still point at,
// and removes claims that have aged out. A claim older than pinClaimMaxAge
// belongs to a launch whose release never ran — a killed TUI, an agent whose
// provider hook never fired — and honouring it forever would let the pins
// directory and the binary cache both grow without bound.
func pinnedDigests(pinsDir string) map[string]bool {
	pinned := map[string]bool{}
	entries, err := os.ReadDir(pinsDir)
	if err != nil {
		return pinned
	}
	cutoff := timeNow().Add(-pinClaimMaxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(pinsDir, entry.Name())
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			// Re-stat before removing. ReadDir's entry.Info is a snapshot, and a
			// RefreshPin in another process between that snapshot and here means
			// the claim is alive — deleting it would then let the same sweep
			// evict a binary a running agent still has to exec, which is the one
			// outcome this whole mechanism exists to prevent. Best effort
			// otherwise: a claim that cannot be removed is honoured for another
			// sweep, which costs one retained copy and never a launch.
			if fresh, statErr := os.Stat(path); statErr == nil && fresh.ModTime().Before(cutoff) {
				if os.Remove(path) == nil {
					continue
				}
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		digest := strings.TrimSpace(string(data))
		if len(digest) >= digestNameLength {
			pinned[digest[:digestNameLength]] = true
		}
	}
	return pinned
}

func cachedBinaryName(digest string) string {
	if len(digest) > digestNameLength {
		digest = digest[:digestNameLength]
	}
	return cachedBinaryPrefix + digest
}

// FileDigest is the SHA-256 a Pin's Digest records. It is exported so callers
// can compare an arbitrary binary — the `approach` a launched agent would find
// on PATH, say — against a pin without duplicating the hash.
func FileDigest(path string) (string, error) {
	return fileDigest(path)
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
