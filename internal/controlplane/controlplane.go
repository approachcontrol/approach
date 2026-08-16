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

// Resolve returns the Pin for the running binary, materializing a cached copy
// under root when it can.
//
// schemaVersion is passed in rather than read from flowstore so this package
// stays free of the store's dependency graph; callers pass
// flowstore.DatabaseSchemaVersion().
//
// Resolve only fails when the running binary itself cannot be resolved or
// hashed. Every cache failure degrades instead.
func Resolve(root string, schemaVersion int) (Pin, error) {
	source, err := resolveSourcePath()
	if err != nil {
		return Pin{}, err
	}
	digest, err := fileDigest(source)
	if err != nil {
		return Pin{}, fmt.Errorf("hash approach executable %s: %w", source, err)
	}
	pin := Pin{
		ExecutablePath: source,
		SourcePath:     source,
		Version:        version.Version(),
		Commit:         version.Commit(),
		SchemaVersion:  schemaVersion,
		Digest:         digest,
	}
	cached, err := materialize(root, source, digest)
	if err != nil {
		pin.Degraded = true
		pin.Notice = fmt.Sprintf(
			"Launch binary cache unavailable (%v); launching %s directly, which an upgrade can replace mid-session.",
			err, source)
		return pin, nil
	}
	pin.ExecutablePath = cached
	return pin, nil
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
	cacheDir := filepath.Join(canonicalRoot, cacheDirName)
	if err := os.MkdirAll(cacheDir, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("create launch binary cache: %w", err)
	}
	if err := os.Chmod(cacheDir, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("secure launch binary cache: %w", err)
	}
	target := filepath.Join(cacheDir, cachedBinaryName(digest))
	if reusable(target, digest) {
		sweepCache(cacheDir, target)
		return target, nil
	}
	if err := copyExecutable(source, target); err != nil {
		return "", err
	}
	sweepCache(cacheDir, target)
	return target, nil
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

func copyExecutable(source, target string) error {
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
	if _, err := io.Copy(temp, in); err != nil {
		_ = temp.Close()
		return fmt.Errorf("copy approach executable: %w", err)
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
	for i, candidate := range candidates {
		if i < retainedBinaries-1 {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, candidate.name))
	}
}

// pinnedDigests returns the digest name prefixes live launches still point at.
func pinnedDigests(pinsDir string) map[string]bool {
	pinned := map[string]bool{}
	entries, err := os.ReadDir(pinsDir)
	if err != nil {
		return pinned
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pinsDir, entry.Name()))
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
