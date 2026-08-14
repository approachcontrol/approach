package flowstore

import "time"

type storedFlow struct {
	record FlowRecord
}

type phaseAgentSettingsSave struct {
	PhaseIndex      int
	PhaseID         string
	Settings        PhaseAgentSettings
	PhaseUpdatedAt  time.Time
	RecordUpdatedAt time.Time
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
	// flow_id ASC. A decodeStoredFlow failure after a successful row scan omits
	// only that row and returns the healthy rows with a *PartialListError. Query,
	// scan, iteration, and close failures are fatal and return no usable rows.
	list(filter FlowFilter) ([]storedFlow, error)

	// delete removes the record, serialized against update by whatever mechanism
	// the implementation uses for its writer. The SQLite backend relies on
	// SQLite's own single-writer rule rather than opening an explicit
	// transaction. It returns flowNotFoundError(flowID) when the record is absent.
	delete(flowID string) error

	// update runs mutate inside the database writer section and returns what
	// mutate returned.
	//
	// CONTRACT — binding on every implementation:
	//   1. mutate is invoked AT MOST ONCE, and never more than once. An
	//      implementation that cannot enter the writer section at all (a failed
	//      BEGIN) returns without calling it. Implementations MUST NOT retry it,
	//      not even on a spurious lock or transient store error, because the
	//      closures are not idempotent: CreateWithOptions rewrites depends_on
	//      through the caller's shared phase array, so a second pass sees the
	//      first pass's edges and takes a different validation branch. Retry
	//      the acquisition if you must, but never a mutate that has begun.
	//   2. Every sess.save or sess.savePhaseAgentSettings performed by mutate is
	//      DURABLE once update returns, REGARDLESS of whether mutate returned an
	//      error. A non-nil error from mutate is a caller-visible outcome, NOT a
	//      rollback signal. The
	//      transaction is therefore committed even when mutate returns an error.
	//      This binds every implementation even though no production caller
	//      currently exercises it: the linked-plan compensation that used to
	//      rely on it now runs in a second update of its own. The contract
	//      subtests are what keep it honest.
	//   3. mutate's record AND error are returned VERBATIM — never wrapped,
	//      never replaced, never zeroed. errors.Is against errFlowNotFound /
	//      ErrAutoLaunchOutdated plus the literal error substrings asserted
	//      throughout store_test.go all depend on this clause.
	//   4. Session save operations may be called zero, one, or many times.
	//   5. Writer acquisition happens before mutate. Updates are serialized
	//      database-wide and readers remain available under WAL.
	//
	// A commit failure is the sole exception to clauses 2 and 3: durability is
	// then unknown, so update returns a zero record and a classifying storage
	// error, and no save is guaranteed to have landed.
	update(flowID string, mutate func(sess flowSession) (FlowRecord, error)) (FlowRecord, error)

	// allocateID returns an id no existing record is using, derived from title
	// and now. It is called OUTSIDE the critical section, before update, so two
	// concurrent Creates can still race for an id exactly as they do today.
	allocateID(title string, now time.Time) (string, error)

	// close releases whatever handles the implementation holds. Unlike the file
	// backend it replaced, the SQLite backend owns a pooled *sql.DB for the life
	// of the Store, so callers that build short-lived Stores need a way to give
	// the descriptors back. It is safe to call more than once.
	close() error
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

	// savePhaseAgentSettings patches only one raw phase object and the record
	// timestamp. Unlike save, it preserves key presence and raw values on every
	// unrelated legacy field.
	savePhaseAgentSettings(update phaseAgentSettingsSave) error
}
