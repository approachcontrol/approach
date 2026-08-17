// Package artifacts centralizes filesystem mechanics shared by approach artifact
// stores. Domain stores still own their JSON schema, validation, and listing
// semantics.
package artifacts

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/approachcontrol/approach/internal/version"
)

const (
	DirPerm  os.FileMode = 0o700
	FilePerm os.FileMode = 0o600
)

const (
	maxSlugLength         = 48
	defaultCollisionTries = 1000
)

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// IDOptions configures timestamped artifact ID allocation.
type IDOptions struct {
	Root         string
	Collection   string
	Title        string
	FallbackSlug string
	Kind         string
	Now          time.Time
	MaxAttempts  int
}

// DefaultRoot returns the shared approach artifact root used by sessions, plans,
// and flows.
//
// A development build defaults to its own root (`approach-dev`) so it cannot
// silently migrate the database a released build owns. Only the *default*
// changes: an explicit --state-root, the APPROACH_*_STATE_ROOT variables, and
// [sessions].root are untouched, so Flow-launched agents — which always receive
// an explicit root — are unaffected.
//
// The blast radius is wider than Flows. artifacts.DefaultRoot backs the session
// store, the plan store, and the flow store alike, so a development build moves
// its session history and plan list along with its Flow list. That is the
// correct outcome — development state should be isolated as a unit rather than
// split across two roots — but it is a user-visible change, not just "an empty
// Flow list on first run".
func DefaultRoot() (string, error) {
	if version.IsDevelopment() {
		return rootUnder("approach-dev")
	}
	return ReleaseDefaultRoot()
}

// ReleaseDefaultRoot is the root a released build defaults to, whatever this
// build is. It exists so the migration guard can test for "this development
// build is about to migrate the release-owned database" by exact path equality
// rather than by a broader rule that would also fire in every temp directory.
func ReleaseDefaultRoot() (string, error) {
	return rootUnder("approach")
}

func rootUnder(namespace string) (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, namespace, "sessions", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve artifact state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", namespace, "sessions", "v1"), nil
}

// SecureCanonicalRoot creates root when absent, forces it to 0700, resolves it
// through symlinks, and returns the canonical path only when the resolved
// directory is genuinely owner-only. label names the root in every error so a
// caller's diagnostics read the way its own messages always did.
//
// Every consumer that stores executable or authoritative state under an
// approach root shares this check: the flow database is written through it, and
// the pinned launch binary is executed out of it.
func SecureCanonicalRoot(root, label string) (string, error) {
	if err := os.MkdirAll(root, DirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", label, err)
	}
	if err := os.Chmod(root, DirPerm); err != nil {
		return "", fmt.Errorf("secure %s: %w", label, err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s must resolve to a directory", label)
	}
	if err := os.Chmod(canonical, DirPerm); err != nil {
		return "", fmt.Errorf("secure resolved %s: %w", label, err)
	}
	info, err = os.Stat(canonical)
	if err != nil || info.Mode().Perm() != DirPerm {
		return "", fmt.Errorf("%s permissions are not 0700", label)
	}
	return canonical, nil
}

// ResolveCanonicalRoot resolves root through symlinks and returns the canonical
// path without creating, chmod'ing, or asserting anything about it.
//
// It exists for read-only opens. SecureCanonicalRoot's two chmods succeed on a
// user-owned 0755 or 0500 directory, so calling it from a reader would silently
// tighten the root to 0700 — repairing the very directory state a diagnostic is
// supposed to report, and giving `approach serve` a different effect on the same
// directory depending on how its root was spelled. A reader reports the mode it
// found; only a writer or a migrator repairs it.
func ResolveCanonicalRoot(root, label string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s must resolve to a directory", label)
	}
	return canonical, nil
}

// RequireAbsoluteRoot returns the same root when it is absolute.
func RequireAbsoluteRoot(root, storeName string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s store root must be absolute: %s", storeName, root)
	}
	return root, nil
}

// EnsureCollection creates and secures root/<collection>.
func EnsureCollection(root, collection string) error {
	if err := os.MkdirAll(CollectionDir(root, collection), DirPerm); err != nil {
		return err
	}
	if err := os.Chmod(root, DirPerm); err != nil {
		return err
	}
	if err := os.Chmod(CollectionDir(root, collection), DirPerm); err != nil {
		return err
	}
	return nil
}

// EnsureRecordDir creates and secures root/<collection>/<id>.
func EnsureRecordDir(root, collection, id string) (string, error) {
	if !IsSafeID(id) {
		return "", fmt.Errorf("invalid artifact id %q", id)
	}
	dir := RecordDir(root, collection, id)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, DirPerm); err != nil {
		return "", err
	}
	return dir, nil
}

// CollectionDir returns root/<collection>.
func CollectionDir(root, collection string) string {
	return filepath.Join(root, collection)
}

// RecordDir returns root/<collection>/<id>.
func RecordDir(root, collection, id string) string {
	return filepath.Join(CollectionDir(root, collection), id)
}

// WriteFileAtomic replaces path with data using a temporary sibling file.
func WriteFileAtomic(path string, data []byte) error {
	return WriteFileAtomicFromReader(path, bytes.NewReader(data))
}

// WriteFileAtomicFromReader streams input to path using a temporary sibling
// file, then renames it into place with restrictive permissions.
func WriteFileAtomicFromReader(path string, input io.Reader) error {
	return WriteFileAtomicFunc(path, func(output io.Writer) error {
		_, err := io.Copy(output, input)
		return err
	})
}

// WriteFileAtomicFunc lets callers generate file contents into an atomic
// temporary file without buffering the whole artifact in memory.
func WriteFileAtomicFunc(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := write(temp); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(FilePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// IsSafeID reports whether id can be used as one artifact path segment.
func IsSafeID(id string) bool {
	return safeIDPattern.MatchString(id) && id != "." && id != ".."
}

// NormalizePhaseID canonicalizes a phase identifier so superficially different
// spellings of the same logical phase (case or surrounding whitespace) compare
// equal and upsert in place instead of duplicating rows.
func NormalizePhaseID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// AllocateTimestampedID returns a timestamp+slug ID that does not already have
// a record directory in the configured collection.
func AllocateTimestampedID(opts IDOptions) (string, error) {
	kind := opts.Kind
	if kind == "" {
		kind = "artifact"
	}
	for _, candidate := range TimestampedIDCandidates(opts) {
		_, err := os.Stat(RecordDir(opts.Root, opts.Collection, candidate))
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check %s id collision: %w", kind, err)
		}
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultCollisionTries
	}
	return "", fmt.Errorf("could not allocate a unique %s id for %q after %d attempts", kind, opts.Title, opts.MaxAttempts)
}

// TimestampedIDCandidates returns the exact candidate sequence used by
// AllocateTimestampedID. Storage backends use it to preserve file-allocation
// naming and exhaustion behavior while checking uniqueness in their own
// authority.
func TimestampedIDCandidates(opts IDOptions) []string {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultCollisionTries
	}
	base := opts.Now.UTC().Format("20060102T150405Z") + "-" + Slug(opts.Title, opts.FallbackSlug)
	candidates := make([]string, 0, opts.MaxAttempts-1)
	candidate := base
	for i := 2; i < opts.MaxAttempts; i++ {
		candidates = append(candidates, candidate)
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidates
}

// Slug lowercases text, keeps [a-z0-9-], collapses separator runs, trims
// boundary dashes, caps length, and falls back when nothing usable remains.
func Slug(text, fallback string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxSlugLength {
		out = strings.Trim(out[:maxSlugLength], "-")
	}
	if out == "" {
		return fallback
	}
	return out
}
