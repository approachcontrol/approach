package flowstore

import "time"

// storedFlow is one decoded record plus the raw-encoding hints the read path
// still needs for depends_on archaeology.
//
// legacyEncoding is what makes the zero value safe. Only a backend that
// persists records as the historical JSON documents — where a phase object
// could omit depends_on entirely, and the record itself could omit headless —
// sets it, and only such a backend is asked for dependsOnPresence or
// headlessPresent. Every other backend leaves those fields alone and is read as
// "depends_on present on every phase, headless present": the neutral answer,
// because the archaeology exists to date legacy files, not to run on stores
// that have real depends_on and headless columns.
type storedFlow struct {
	record FlowRecord

	// legacyEncoding marks a record that came from an encoding where
	// depends_on could be absent. When false, dependsOnPresence is ignored.
	//
	// A backend that MIGRATES legacy records inherits this obligation: it must
	// persist "depends_on was absent for this phase" as real state, set this
	// flag, and rebuild dependsOnPresence below. Dropping the distinction on
	// migration silently rewrites those records' edges on the next read.
	legacyEncoding bool

	// dependsOnPresence is positional against record.Phases and is read only
	// when legacyEncoding is true. len(dependsOnPresence) MUST equal
	// len(record.Phases), in the same order the backend returns the phases —
	// natural for a JSON document, a trap for anything reconstructing phases
	// with ORDER BY. Entries past the end are read as absent.
	dependsOnPresence []rawDependsOnState

	// headlessPresent reports whether the stored encoding carried a headless
	// field at all, and is read only when legacyEncoding is true. Records
	// written before headless became per-flow have none, and those default to
	// headless on read rather than to Go's zero value.
	headlessPresent bool
}

// dependsOnHints returns the positional presence slice hydrate should use. A
// non-legacy backend gets "present everywhere" rather than the nil slice, which
// would otherwise read as "depends_on absent everywhere" and drive every read
// into graph recovery.
func (s storedFlow) dependsOnHints() []rawDependsOnState {
	if s.legacyEncoding {
		return s.dependsOnPresence
	}
	hints := make([]rawDependsOnState, len(s.record.Phases))
	for i := range hints {
		hints[i] = rawDependsOnState{Present: true}
	}
	return hints
}

// backend is the per-flow storage seam. Implementations own record durability,
// the per-flow critical section, and record encoding.
//
// They own no DOMAIN rules — no phase validation, no normalization, no
// readiness derivation, no status derivation. Admissibility is still theirs:
// see the ID and schema-version clauses on get.
type backend interface {
	// get and list acquire no lock, matching today's Read/List.
	//
	// get reports ANY read failure as a miss: a record that is absent, corrupt,
	// or unreadable is all one outcome. That is the file store's historical
	// behavior, preserved here deliberately. It is a poor fit for a networked or
	// transactional store, where an I/O error would be indistinguishable from
	// data loss; revisit the signature when the second backend lands rather than
	// widening it blind.
	//
	// Two admissibility rules are load-bearing and MUST be enforced here,
	// because nothing above the seam repeats them:
	//   - a record whose stored flow id does not match flowID is a miss;
	//   - a record whose stored schema version is not schemaVersion is a miss,
	//     which is what makes records written by a future version invisible
	//     rather than half-decoded. A plain row fetch that skips this check
	//     silently changes Read and List.
	get(flowID string) (storedFlow, bool)

	// list returns every readable record, skipping unreadable ones rather than
	// failing the call. Order is load-bearing: Store.List sorts STABLY on
	// UpdatedAt, so records with equal timestamps keep the order returned here.
	// Implementations must therefore return a stable, insertion-consistent
	// order; reordering between calls silently reorders ties for the caller.
	list() ([]storedFlow, error)

	// delete acquires the same critical section as update, then removes the
	// record. It returns flowNotFoundError(flowID) when the record is absent.
	delete(flowID string) error

	// update runs mutate inside the per-flow critical section and returns what
	// mutate returned.
	//
	// CONTRACT — binding on every implementation:
	//   1. mutate is invoked EXACTLY ONCE. Implementations MUST NOT retry it,
	//      not even on a spurious lock or transient store error, because the
	//      closures are not idempotent: CreateWithOptions rewrites depends_on
	//      through the caller's shared phase array, so a second pass sees the
	//      first pass's edges and takes a different validation branch. Retry
	//      the acquisition if you must, but never a mutate that has begun.
	//   2. Every sess.save performed by mutate is DURABLE once update returns,
	//      REGARDLESS of whether mutate returned an error. A non-nil error from
	//      mutate is a caller-visible outcome, NOT a rollback signal. SetPhase
	//      and MarkManualMerge rely on this: they persist needs_attention
	//      compensation and *then* return the sync error. This is why the
	//      handle below is a session and not a transaction — see flowSession.
	//   3. mutate's record AND error are returned VERBATIM — never wrapped,
	//      never replaced, never zeroed. The two callers differ on purpose:
	//      SetPhase returns a zero record beside its sync error while
	//      MarkManualMerge returns the compensated record beside the same kind
	//      of error, and errors.Is against errFlowNotFound / errAutoLaunchOutdated
	//      plus the literal error substrings asserted throughout store_test.go
	//      all depend on this clause.
	//   4. sess.save may be called zero, one, or many times.
	//   5. Concurrent updates to the SAME flowID are mutually exclusive; the
	//      whole point of the call is that mutate runs alone. Updates to
	//      different flow IDs must not block each other.
	update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error)

	// allocateID returns an id no existing record is using, derived from title
	// and now. It is called OUTSIDE the critical section, before update, so two
	// concurrent Creates can still race for an id exactly as they do today.
	allocateID(title string, now time.Time) (string, error)
}

// flowSession is the handle a mutate closure uses inside the critical section.
//
// It is deliberately NOT called a transaction. Clause 2 on update requires
// every save to be durable even when the closure then fails, so an
// implementation may never wrap the closure in a rollback-on-error
// transaction; the most it can do is commit each save as it happens. Naming
// this flowTx would promise atomicity the contract forbids.
type flowSession interface {
	// get reads the record as it stands inside the section, including anything
	// an earlier save in this same session wrote. It must not re-acquire the
	// section.
	get() (storedFlow, bool)

	// exists reports whether the record is present without decoding it, so
	// CreateWithOptions can detect an id collision it would otherwise clobber.
	exists() (bool, error)

	// save persists one record inside the section. It must not re-acquire the
	// section either: fileBackend's flock is not re-entrant, and a second
	// acquire would block until it timed out.
	save(record FlowRecord) error
}
