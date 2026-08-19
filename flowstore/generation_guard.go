package flowstore

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrDatabaseGenerationChanged marks a handle whose database was replaced or
// migrated underneath it.
//
// Distinct from every other write failure because the remedy is distinct: the
// request was fine, the database is fine, and this PROCESS is holding a handle
// on state that no longer exists. Retrying cannot help, and a caller that
// treated it as a transient error would loop.
var ErrDatabaseGenerationChanged = errors.New("flow database changed generation underneath this handle")

// sidecarGenerationThrottle bounds how often a write re-reads the sidecar.
//
// A deliberate trade. Reading it on every mutation costs a stat and a read per
// write; the window this leaves open is bounded by one second of a restore
// that already refuses while any live holder exists, and by the user_version
// check below, which is a same-connection pragma and runs unthrottled on every
// write. Revalidate is the escape hatch for a caller that needs the exact
// answer now.
const sidecarGenerationThrottle = time.Second

// generationGuard is the live-handle half of the compatibility story. Every
// other check in this package runs at OPEN; this one runs while the handle is
// held, which is the only place a migration by another process can be observed.
type generationGuard struct {
	mu sync.Mutex
	// openUserVersion and openGeneration are what this handle observed when it
	// opened. They are the comparison baseline and are never updated: a handle
	// that adopted the new values would silently start writing to a database it
	// never validated.
	openUserVersion int64
	openGeneration  string
	lastSidecarRead time.Time
	// failure is sticky. Once the database underneath a handle has changed,
	// every later operation through it is refused: the alternative is a caller
	// that retries and, on the retry, finds the throttle window has not
	// elapsed and the pragma answers the new version, and writes anyway.
	failure error
}

// guardWrite is the one choke point every write passes through. It answers
// "may this handle still write" — first by role, then by generation.
func (b *sqliteBackend) guardWrite() error {
	if err := b.requireWriter(); err != nil {
		return err
	}
	return b.checkGeneration(false)
}

// checkGeneration compares the database under this handle against what the
// handle opened. force skips the sidecar throttle; Revalidate passes it.
func (b *sqliteBackend) checkGeneration(force bool) error {
	b.guard.mu.Lock()
	defer b.guard.mu.Unlock()
	if b.guard.failure != nil {
		return b.guard.failure
	}
	// user_version first and always: it is a pragma answered out of the file
	// header on a connection this handle already owns, so it costs no
	// filesystem call of its own and catches every migration.
	current, err := b.readUserVersion()
	if err != nil {
		// A version that cannot be read is not evidence the database changed.
		// Failing here would turn a transient BUSY into a permanently poisoned
		// handle, which is the opposite of what a sticky failure is for.
		return nil
	}
	if current != b.guard.openUserVersion {
		b.guard.failure = fmt.Errorf("%w: opened at flow database schema %d, now %d;"+
			" another process migrated this database. Restart approach",
			ErrDatabaseGenerationChanged, b.guard.openUserVersion, current)
		return b.guard.failure
	}
	// The sidecar generation catches what user_version cannot: a restore that
	// put a DIFFERENT database at the same schema.
	now := b.generationClock()
	if !force && now.Sub(b.guard.lastSidecarRead) < sidecarGenerationThrottle {
		return nil
	}
	b.guard.lastSidecarRead = now
	generation := b.readGeneration()
	// An absent generation is not a change: a never-migrated root has no
	// sidecar at all, which is the ordinary state and must not poison a handle.
	if generation == "" || generation == b.guard.openGeneration {
		return nil
	}
	b.guard.failure = fmt.Errorf("%w: opened at generation %s, now %s;"+
		" this database was restored or replaced. Restart approach",
		ErrDatabaseGenerationChanged, generationOrNone(b.guard.openGeneration), generation)
	return b.guard.failure
}

func generationOrNone(generation string) string {
	if generation == "" {
		return "none"
	}
	return generation
}

// Revalidate forces the exact answer the throttled check only approximates. A
// caller about to do something it cannot undo — and `db restore` itself —
// should ask rather than wait out the window.
func (s *Store) Revalidate() error {
	backend, ok := s.backend.(*sqliteBackend)
	if !ok {
		return nil
	}
	return backend.checkGeneration(true)
}
