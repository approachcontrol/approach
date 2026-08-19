// Package dblease publishes "this process holds the flow database open, at
// this build" in a form a migrator can read without racing it.
//
// It is named apart from internal/flowlease, which is the Flow-scoped launch
// lease and is unrelated: this one is about a database file and the builds
// holding it.
//
// The mechanism is one file per holder under <root>/.approach.db.owners/, each
// holding an EXCLUSIVE flock on its own file for the holder's lifetime. One
// file per holder is what makes an exclusive lock the right primitive — there
// is nothing to share — and it is what lets a scanner detect a dead holder for
// free: a process that died released its lock, so a non-blocking exclusive
// acquire that SUCCEEDS proves the holder is gone. No PID-liveness heuristic,
// which cannot distinguish a stale PID from a recycled one.
package dblease

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

// ownersDirName is the holder directory under the state root. Dot-prefixed for
// the same reason the bootstrap lock is: it is machinery, not artifacts.
const ownersDirName = ".approach.db.owners"

// tempPrefix marks a holder file that is not yet complete. Scan skips these,
// and MUST: unlinking one between its create and its rename would make the
// holder's rename fail ENOENT.
const tempPrefix = ".tmp-"

// ErrOwnersDirectory marks a failure to use the owners directory at all — a
// read-only root, a permissions problem, a file where the directory should be.
// Named so callers can degrade rather than crash: a lease that cannot be taken
// is a missing safety net, not a reason to refuse to start.
var ErrOwnersDirectory = errors.New("flow database owners directory unusable")

// Identity is what a holder publishes about itself. Every field exists to be
// printed in a refusal: an operator told only "something holds this database"
// has nothing to close.
type Identity struct {
	BuildVersion string `json:"build_version"`
	Commit       string `json:"commit,omitempty"`
	Executable   string `json:"executable,omitempty"`
	// SchemaVersion is the flow database schema this build writes. A migrator
	// compares it against the version it would advance TO.
	SchemaVersion int       `json:"schema_version"`
	StartedAt     time.Time `json:"started_at"`
}

// Record is one holder as a scanner sees it: its Identity plus the PID and the
// file it came from.
type Record struct {
	Identity
	PID   int    `json:"pid"`
	Nonce string `json:"nonce"`
	// Path is the holder file, for diagnostics only. A refusal names the PID
	// and the build, which is what an operator can act on.
	Path string `json:"-"`
}

// Holder is a live lease. It owns an open file descriptor for its lifetime;
// closing it is what releases the lock, so the descriptor is deliberately never
// handed out.
type Holder struct {
	path     string
	nonce    string
	file     *os.File
	released bool
}

// Nonce identifies this holder among a process's holders, so a migrator can
// exclude ITSELF from its own scan without excluding every holder that happens
// to share its PID.
func (h *Holder) Nonce() string {
	if h == nil {
		return ""
	}
	return h.nonce
}

// Probe steps, named so a test can assert the acquisition ORDER as a property
// rather than read it out of a comment.
const (
	// ProbeLocked fires after the temp file is written, fsynced, and locked,
	// and before the rename that publishes it.
	ProbeLocked = "lease-locked"
	// ProbePublished fires after the rename and the directory fsync.
	ProbePublished = "lease-published"
	// ProbeBootstrapLock is recorded by flowstore when it takes the bootstrap
	// lock, so the total order (lease first, bootstrap lock second) is testable.
	ProbeBootstrapLock = "bootstrap-lock"
)

// acquisitionProbe is a no-op seam recording lease and bootstrap-lock steps.
var acquisitionProbe = func(step string) {}

// SetAcquisitionProbe installs a probe and returns a function restoring the
// previous one. Exported because the ordering property it exists to prove is
// cross-package: the lease is here and the bootstrap lock is in flowstore.
func SetAcquisitionProbe(probe func(step string)) func() {
	previous := acquisitionProbe
	if probe == nil {
		probe = func(string) {}
	}
	acquisitionProbe = probe
	return func() { acquisitionProbe = previous }
}

// Probe records a step against the installed probe. flowstore calls it for
// ProbeBootstrapLock.
func Probe(step string) { acquisitionProbe(step) }

// Dir is the owners directory under root.
func Dir(root string) string {
	return filepath.Join(root, ownersDirName)
}

// Acquire publishes this process as a holder and takes the lock that proves it
// is alive.
//
// The ordering is create-locked-then-rename, and it is the whole design. Both
// alternatives are wrong:
//
//   - lock-then-write (what artifacts.acquireOpenedFileLock does, correctly, for
//     a file whose CONTENT nobody reads) lets a probing migrator open the final
//     name and read an empty file, so a live holder is reported with no PID and
//     no build — precisely the fields the refusal exists to print;
//   - write-then-lock lets a probing migrator's non-blocking exclusive acquire
//     SUCCEED on a file whose owner has not locked it yet, conclude the holder
//     is dead, and unlink it. The holder then locks an unlinked inode and is
//     permanently invisible to every later scan.
//
// Under create-locked-then-rename, a file under its final name is already
// complete and already locked, and is never rewritten — which is what makes
// Scan's unlocked read of a live holder's record safe.
func Acquire(root string, id Identity) (*Holder, error) {
	dir := Dir(root)
	if err := os.MkdirAll(dir, artifacts.DirPerm); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOwnersDirectory, err)
	}
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	record := Record{Identity: id, PID: os.Getpid(), Nonce: nonce}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode flow database owner record: %w", err)
	}
	tempPath := filepath.Join(dir, tempPrefix+nonce+".json")
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, artifacts.FilePerm)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOwnersDirectory, err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		cleanup()
		return nil, fmt.Errorf("write flow database owner record: %w", err)
	}
	// Synced before the lock and before the rename: a holder file that is
	// visible under its final name but whose contents did not reach the disk
	// would be read as a corrupt record by the very scan it exists to inform.
	if err := file.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync flow database owner record: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		cleanup()
		return nil, fmt.Errorf("lock flow database owner record: %w", err)
	}
	acquisitionProbe(ProbeLocked)
	finalPath := filepath.Join(dir, fmt.Sprintf("%d-%s.json", record.PID, nonce))
	if err := os.Rename(tempPath, finalPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("publish flow database owner record: %w", err)
	}
	// The rename's directory entry is what every scanner looks for; without
	// this fsync a crash can lose the publication while the process goes on
	// believing it is registered.
	if err := syncDirectory(dir); err != nil {
		_ = file.Close()
		_ = os.Remove(finalPath)
		return nil, fmt.Errorf("sync flow database owners directory: %w", err)
	}
	acquisitionProbe(ProbePublished)
	return &Holder{path: finalPath, nonce: nonce, file: file}, nil
}

// Release drops the lease. Idempotent, and a missing file is success: a holder
// reaped by a scan that (correctly) judged this process dead is not an error
// this process can do anything about.
func (h *Holder) Release() error {
	if h == nil || h.released {
		return nil
	}
	h.released = true
	// Unlink before unlocking: between the unlock and the remove, a scanner
	// could acquire the lock, decide the holder is dead, and remove the file
	// itself — after which this remove would race a NEW holder's rename onto
	// the same name. Removing first closes that window, and closing the file
	// releases the lock as a side effect.
	removeErr := os.Remove(h.path)
	if removeErr != nil && !os.IsNotExist(removeErr) {
		_ = h.file.Close()
		return fmt.Errorf("remove flow database owner record: %w", removeErr)
	}
	if err := h.file.Close(); err != nil {
		return fmt.Errorf("release flow database owner record: %w", err)
	}
	return nil
}

// Scan reports the live holders under root and reaps the dead ones.
//
// exclude names nonces the caller owns. A migrator passes its OWN holder's
// nonce rather than filtering on PID: one process can legitimately hold several
// leases, and a PID filter would hide a second one.
//
// A missing owners directory is not an error. Every root that predates this
// package has none, and a lease nobody has taken is exactly zero holders.
func Scan(root string, exclude ...string) (live []Record, reaped []string, err error) {
	dir := Dir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("%w: %v", ErrOwnersDirectory, err)
	}
	excluded := make(map[string]bool, len(exclude))
	for _, nonce := range exclude {
		if nonce != "" {
			excluded[nonce] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		// Temp files are skipped, never probed and never unlinked: one belongs
		// to a holder mid-Acquire, and removing it would break the rename that
		// is about to publish it.
		if entry.IsDir() || strings.HasPrefix(name, tempPrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		record, dead, probeErr := probeHolder(path)
		if probeErr != nil {
			// An unreadable holder file is evidence of nothing. It is neither
			// reported as live — which would block a migration on a file
			// nobody can attribute — nor unlinked, which would destroy the one
			// artifact a human could inspect.
			continue
		}
		if dead {
			if err := os.Remove(path); err == nil || os.IsNotExist(err) {
				reaped = append(reaped, name)
			}
			continue
		}
		if excluded[record.Nonce] {
			continue
		}
		record.Path = path
		live = append(live, record)
	}
	return live, reaped, nil
}

// probeHolder decides whether one holder file names a live process.
//
// The flock IS the liveness check: an exclusive non-blocking acquire that
// succeeds proves nothing holds the file, and the only thing that ever holds it
// is its owner for its whole lifetime. EWOULDBLOCK means a live holder, whose
// record is then read WITHOUT the lock — safe precisely because
// create-locked-then-rename means a file under its final name is complete and
// is never rewritten.
func probeHolder(path string) (Record, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return Record{}, false, err
	}
	defer func() { _ = file.Close() }()
	lockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if lockErr == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return Record{}, true, nil
	}
	if lockErr != syscall.EWOULDBLOCK && lockErr != syscall.EAGAIN {
		return Record{}, false, lockErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, false, err
	}
	if record.PID <= 0 {
		return Record{}, false, fmt.Errorf("owner record %s names no pid", path)
	}
	return record, false, nil
}

// Describe renders one holder the way a refusal names it. Split out so the
// migrator's plural message and any diagnostic spell a holder identically.
func (r Record) Describe() string {
	build := r.BuildVersion
	if strings.TrimSpace(build) == "" {
		build = "unknown build"
	}
	described := fmt.Sprintf("pid %d (%s, flow database schema %d)", r.PID, build, r.SchemaVersion)
	if strings.TrimSpace(r.Executable) != "" {
		described += " at " + r.Executable
	}
	return described
}

// newNonce disambiguates holders within one process and names the temp file.
// A CSPRNG read failing is not worth failing a lease over — the nonce is not a
// security boundary — so it degrades to the clock, which is unique enough for a
// name that only has to differ from this process's other holders.
func newNonce() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UTC().UnixNano()), nil
	}
	return hex.EncodeToString(raw[:]), nil
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}
