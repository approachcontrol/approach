package flowstore

import (
	"time"

	"github.com/approachcontrol/approach/planstore"
)

// planPhaseSyncer opens a writer for the plan store that mirrors completed Flow
// phases into their linked plan. It is a two-phase interface on purpose: the
// Store's beforeLinkedPlanPhaseSync test hook must fire *after* the plan store
// is opened and *before* the plan write, so opening and writing cannot collapse
// into a single call.
type planPhaseSyncer interface {
	// open is called once per sync attempt, not once per Store, so a plan store
	// that fails to open surfaces as a sync failure (which trips the
	// needs_attention compensation) rather than as a Store construction failure.
	open() (planPhaseWriter, error)
}

// planPhaseWriter marks one linked plan phase completed.
type planPhaseWriter interface {
	markPhaseCompleted(planID, phaseID string) error
}

// nestedPlanLockTimeout bounds the plan lock wait taken INSIDE the Flow write
// transaction. It must stay well under the Flow busy timeout (defaultLockTimeout,
// 5s), and that gap is the whole point.
//
// planstore's mutation lock is a single GLOBAL file lock across every plan and
// every process, and it is acquired while this process holds SQLite's
// database-wide write lock. At planstore's own 5s default the two budgets are
// equal, so a Flow writer parked on the plan lock is guaranteed to burn another
// Flow writer's entire busy timeout — measured: an unrelated Flow with no linked
// plan at all failing outright with "database is locked". Capping the nested
// wait at 1s means the outer writer always releases with ~4s of the other
// writer's budget left, and the plan-lock failure degrades into the
// needs_attention compensation that already exists for exactly this case.
const nestedPlanLockTimeout = time.Second

// planstoreSyncer is the production syncer: plans are files under the same
// artifact root as flows.
type planstoreSyncer struct{ root string }

func (s planstoreSyncer) open() (planPhaseWriter, error) {
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root:        s.root,
		LockTimeout: nestedPlanLockTimeout,
	})
	if err != nil {
		return nil, err
	}
	return planstorePhaseWriter{store: store}, nil
}

type planstorePhaseWriter struct{ store *planstore.Store }

func (w planstorePhaseWriter) markPhaseCompleted(planID, phaseID string) error {
	return w.store.SetPhaseStatus(planID, phaseID, "completed")
}
