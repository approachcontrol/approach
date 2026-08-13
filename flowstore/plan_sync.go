package flowstore

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/approachcontrol/approach/planstore"
)

// planLinkResolver validates a saved plan and returns the configured-root path
// that a Flow should persist for it.
type planLinkResolver interface {
	resolvePlanLink(planID, suppliedPath string) (string, error)
}

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
//
// lockTimeout is the Store's own resolved lock budget, carried here so the plan
// lock wait and the Flow writer wait are one knob. The sync no longer runs
// inside the Flow write transaction, so a long plan-lock wait costs only latency
// to the caller that needs the lock — nothing else is held. A SHORT budget is
// now the expensive side: it converts ordinary plan-lock contention into sync
// failures, and a sync failure demotes a legitimately completed phase to
// needs_attention, or for MarkManualMerge rolls merge metadata back and forces
// an operator RestartPhase before the merge can be recorded again. Prefer
// waiting. Plumbing the Store's budget rather than falling through to
// planstore's own 5s default also keeps a Store built with a deliberately short
// LockTimeout from silently inheriting a much longer plan budget.
type planstoreSyncer struct {
	root        string
	lockTimeout time.Duration
}

func (s planstoreSyncer) resolvePlanLink(planID, suppliedPath string) (string, error) {
	planPath, err := planstore.MarkdownPath(s.root, planID)
	if err != nil {
		return "", err
	}
	if supplied := strings.TrimSpace(suppliedPath); supplied != "" {
		if !filepath.IsAbs(supplied) {
			return "", fmt.Errorf("flow plan path must be absolute: %s", supplied)
		}
		if filepath.Clean(supplied) != planPath {
			return "", fmt.Errorf("flow plan path %q does not match plan %q path %q", filepath.Clean(supplied), planID, planPath)
		}
	}
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root:        s.root,
		LockTimeout: s.lockTimeout,
	})
	if err != nil {
		return "", err
	}
	if !store.HasPlan(planID) {
		return "", fmt.Errorf("plan %q not found", planID)
	}
	if _, err := store.ReadPlan(planID); err != nil {
		return "", err
	}
	return planPath, nil
}

func (s planstoreSyncer) open() (planPhaseWriter, error) {
	store, err := planstore.NewStore(planstore.StoreOptions{
		Root:        s.root,
		LockTimeout: s.lockTimeout,
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
