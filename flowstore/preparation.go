package flowstore

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrPreparationIncomplete means authoritative state confirms that no
	// protected preparation receipt was persisted.
	ErrPreparationIncomplete = errors.New("flow preparation is incomplete")
	// ErrPreparationUnknown means a failed persistence attempt could not be
	// reconciled by an authoritative read.
	ErrPreparationUnknown = errors.New("flow preparation outcome is unknown")
)

// PreparationFinalizer is a Flow-bound one-shot capability. The concrete type
// is private so callers cannot construct a capability for an arbitrary Flow ID.
type PreparationFinalizer interface {
	Finalize(func() error) (FlowRecord, error)
}

type preparationFinalizer struct {
	mu        sync.Mutex
	store     *Store
	flowID    string
	createdAt time.Time
	consumed  bool
}

// CreatePreparation creates an ordinary receipt-less Flow and returns the sole
// capability that may stamp its preparation receipt.
func (s *Store) CreatePreparation(record FlowRecord, opts CreateOptions) (FlowRecord, PreparationFinalizer, error) {
	created, err := s.CreateWithOptions(record, opts)
	if err != nil {
		return FlowRecord{}, nil, err
	}
	return created, &preparationFinalizer{store: s, flowID: created.FlowID, createdAt: created.CreatedAt}, nil
}

// Finalize runs bootstrap exactly once and stamps the receipt only after the
// callback succeeds. A commit error is reconciled with an authoritative read:
// a visible receipt is success, a visible nil receipt is incomplete, and an
// unreadable result is unknown.
func (f *preparationFinalizer) Finalize(bootstrap func() error) (FlowRecord, error) {
	if f == nil || f.store == nil || strings.TrimSpace(f.flowID) == "" || f.createdAt.IsZero() {
		return FlowRecord{}, errors.New("invalid flow preparation finalizer")
	}
	f.mu.Lock()
	if f.consumed {
		f.mu.Unlock()
		return FlowRecord{}, errors.New("flow preparation finalizer was already consumed")
	}
	f.consumed = true
	f.mu.Unlock()
	current, err := f.store.Read(f.flowID)
	if err != nil {
		return FlowRecord{}, errors.Join(ErrPreparationUnknown, fmt.Errorf("read flow generation before preparation: %w", err))
	}
	if !current.CreatedAt.Equal(f.createdAt) {
		return current, errors.Join(ErrPreparationIncomplete, fmt.Errorf("flow %q generation changed before preparation finalization", f.flowID))
	}

	if bootstrap != nil {
		if err := bootstrap(); err != nil {
			return FlowRecord{}, errors.Join(ErrPreparationIncomplete, err)
		}
	}
	record, err := f.store.backend.update(f.flowID, func(sess flowSession) (FlowRecord, error) {
		stored, found, err := sess.get()
		if err != nil {
			return FlowRecord{}, err
		}
		if !found {
			return FlowRecord{}, flowNotFoundError(f.flowID)
		}
		record := stored.record
		if !record.CreatedAt.Equal(f.createdAt) {
			return FlowRecord{}, fmt.Errorf("flow %q generation changed before preparation finalization", record.FlowID)
		}
		if record.PreparedAt != nil {
			return FlowRecord{}, fmt.Errorf("flow %q already has a preparation receipt", record.FlowID)
		}
		if FlowClosed(record) {
			return FlowRecord{}, closedFlowMutationError(record, "finalize preparation for")
		}
		if DeriveStatus(record) != StatusPending {
			return FlowRecord{}, fmt.Errorf("flow %q must be pending to finalize preparation", record.FlowID)
		}
		if strings.TrimSpace(record.WorktreePath) == "" || !filepath.IsAbs(record.WorktreePath) {
			return FlowRecord{}, fmt.Errorf("flow %q requires an absolute worktree before preparation can finalize", record.FlowID)
		}
		if strings.TrimSpace(record.Branch) == "" {
			return FlowRecord{}, fmt.Errorf("flow %q requires a branch before preparation can finalize", record.FlowID)
		}
		stamp := flowMutationTime(record, f.store.now())
		record.PreparedAt = &stamp
		record.UpdatedAt = stamp
		if err := f.store.saveSession(sess, record); err != nil {
			return FlowRecord{}, err
		}
		return record, nil
	})
	if err == nil {
		return record, nil
	}

	authoritative, readErr := f.store.Read(f.flowID)
	if readErr != nil {
		return FlowRecord{}, errors.Join(ErrPreparationUnknown, err, fmt.Errorf("read preparation receipt: %w", readErr))
	}
	if !authoritative.CreatedAt.Equal(f.createdAt) {
		return authoritative, errors.Join(ErrPreparationIncomplete, err, fmt.Errorf("flow %q generation changed before preparation finalization", f.flowID))
	}
	if authoritative.PreparedAt != nil {
		return authoritative, nil
	}
	return authoritative, errors.Join(ErrPreparationIncomplete, err)
}

// IsPreparationIncomplete reports a confirmed receipt-less outcome.
func IsPreparationIncomplete(err error) bool { return errors.Is(err, ErrPreparationIncomplete) }

// IsPreparationUnknown reports a persistence outcome that could not be read
// authoritatively after a failed receipt write.
func IsPreparationUnknown(err error) bool { return errors.Is(err, ErrPreparationUnknown) }
