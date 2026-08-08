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
func DefaultRoot() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "approach", "sessions", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve artifact state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", "approach", "sessions", "v1"), nil
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
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultCollisionTries
	}
	kind := opts.Kind
	if kind == "" {
		kind = "artifact"
	}
	base := opts.Now.UTC().Format("20060102T150405Z") + "-" + Slug(opts.Title, opts.FallbackSlug)
	candidate := base
	for i := 2; i < opts.MaxAttempts; i++ {
		_, err := os.Stat(RecordDir(opts.Root, opts.Collection, candidate))
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check %s id collision: %w", kind, err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("could not allocate a unique %s id for %q after %d attempts", kind, opts.Title, opts.MaxAttempts)
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
