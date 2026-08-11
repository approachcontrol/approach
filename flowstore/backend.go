package flowstore

import "time"

type storedFlow struct {
	record FlowRecord
}

// backend is the Flow storage seam. Implementations own record durability,
// database-wide writer serialization, filtering, ordering, and record encoding.
//
// They own no DOMAIN rules — no phase validation, no normalization, no
// readiness derivation, no status derivation. Admissibility is still theirs:
// see the ID and schema-version clauses on get.
type backend interface {
	// get and list do not acquire the writer. get uses ok=false only for an
	// absent record; corruption and storage failures are returned as errors.
	//
	// Stored-id mismatches, unsupported schema versions, malformed JSON, and
	// projection disagreements are corruption errors, never misses.
	get(flowID string) (storedFlow, bool, error)

	// list applies the filter and returns records ordered by updated_at DESC,
	// flow_id ASC. Any malformed authoritative row fails the whole call.
	list(filter FlowFilter) ([]storedFlow, error)

	// delete acquires the same critical section as update, then removes the
	// record. It returns flowNotFoundError(flowID) when the record is absent.
	delete(flowID string) error

	// update runs mutate inside the database writer section and returns what
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
	//      compensation and *then* return the sync error. The transaction is
	//      therefore committed even when mutate returns an error.
	//   3. mutate's record AND error are returned VERBATIM — never wrapped,
	//      never replaced, never zeroed. The two callers differ on purpose:
	//      SetPhase returns a zero record beside its sync error while
	//      MarkManualMerge returns the compensated record beside the same kind
	//      of error, and errors.Is against errFlowNotFound / errAutoLaunchOutdated
	//      plus the literal error substrings asserted throughout store_test.go
	//      all depend on this clause.
	//   4. sess.save may be called zero, one, or many times.
	//   5. Writer acquisition happens before mutate. Updates are serialized
	//      database-wide and readers remain available under WAL.
	//
	// A commit failure is the sole exception to clause 3: durability is then
	// unknown, so update returns a zero record and a classifying storage error.
	update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error)

	// allocateID returns an id no existing record is using, derived from title
	// and now. It is called OUTSIDE the critical section, before update, so two
	// concurrent Creates can still race for an id exactly as they do today.
	allocateID(title string, now time.Time) (string, error)
}

// flowSession is the handle a mutate closure uses inside the critical section.
//
// It is named a session because callback errors are caller outcomes rather than
// rollback signals. The SQLite implementation still uses one transaction and
// commits all successful saves after the callback returns.
type flowSession interface {
	// get reads the record as it stands inside the section, including anything
	// an earlier save in this same session wrote. It must not re-acquire the
	// section.
	get() (storedFlow, bool, error)

	// exists reports whether the record is present without decoding it, so
	// CreateWithOptions can detect an id collision it would otherwise clobber.
	exists() (bool, error)

	// save persists one record inside the section without re-acquiring a writer.
	save(record FlowRecord) error
}
