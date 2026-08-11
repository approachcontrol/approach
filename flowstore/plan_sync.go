package flowstore

import (
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

// planstoreSyncer is the production syncer: plans are files under the same
// artifact root as flows.
type planstoreSyncer struct{ root string }

func (s planstoreSyncer) open() (planPhaseWriter, error) {
	store, err := planstore.NewStore(planstore.StoreOptions{Root: s.root})
	if err != nil {
		return nil, err
	}
	return planstorePhaseWriter{store: store}, nil
}

type planstorePhaseWriter struct{ store *planstore.Store }

func (w planstorePhaseWriter) markPhaseCompleted(planID, phaseID string) error {
	return w.store.SetPhaseStatus(planID, phaseID, "completed")
}
