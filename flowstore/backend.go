package flowstore

import "time"

// storedFlow is one decoded record plus the raw-encoding hints the read path
// still needs for depends_on archaeology.
//
// dependsOnPresence is positional against record.Phases. A backend that does
// not decode JSON must still return a slice of len(record.Phases) with
// Present: true in every element: a nil slice is NOT neutral, it reads as
// "depends_on absent everywhere" and drives every read into graph recovery.
//
// headlessPresent reports whether the stored encoding carried a headless field
// at all. Records written before headless became per-flow carry none, and the
// read path defaults those to headless. A backend that does not decode JSON
// must report true, or every record reads back as headless.
type storedFlow struct {
	record            FlowRecord
	dependsOnPresence []rawDependsOnState
	headlessPresent   bool
}

// backend is the per-flow storage seam. Implementations own record durability,
// the per-flow critical section, and record encoding. They own no domain rules:
// no validation, no normalization, no readiness derivation.
type backend interface {
	// get and list acquire no lock, matching today's Read/List. Any read
	// failure is reported as a miss.
	get(flowID string) (storedFlow, bool)
	list() ([]storedFlow, error)

	// save persists one record WITHOUT acquiring the critical section. The
	// caller must already hold it, or must not need it. Used only by
	// Store.write, which exists only for test seeding.
	save(record FlowRecord) error

	// delete acquires the same critical section as update, then removes the
	// record. It returns flowNotFoundError(flowID) when the record is absent.
	delete(flowID string) error

	// update runs mutate inside the per-flow critical section.
	//
	// CONTRACT — binding on every implementation:
	//   1. Every tx.save performed by mutate is DURABLE once update returns,
	//      REGARDLESS of whether mutate returned an error. A non-nil error from
	//      mutate is a caller-visible outcome, NOT a rollback signal. A backend
	//      with transactional rollback MUST commit before propagating. SetPhase
	//      and MarkManualMerge rely on this: they persist needs_attention
	//      compensation and *then* return the sync error.
	//   2. mutate's error is returned VERBATIM — never wrapped, never replaced.
	//      errors.Is against errFlowNotFound / errAutoLaunchOutdated and the
	//      literal error substrings asserted throughout store_test.go both
	//      depend on this.
	//   3. tx.save may be called zero, one, or many times.
	update(flowID string, mutate func(tx flowTx) error) error

	// allocateID is called OUTSIDE the critical section, before update. Its now
	// argument must be a fresh clock read taken at the call site, because
	// Create reads the clock twice and the existing tests count those reads.
	allocateID(title string, now time.Time) (string, error)
}

// flowTx is the handle a mutate closure uses inside the critical section.
type flowTx interface {
	get() (storedFlow, bool)
	exists() (bool, error)
	save(record FlowRecord) error
}
