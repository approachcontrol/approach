package flowstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var (
	// ErrPreparationIncomplete means authoritative state confirms that no
	// protected preparation receipt was persisted.
	ErrPreparationIncomplete = errors.New("flow preparation is incomplete")
	// ErrPreparationUnknown means a failed persistence attempt could not be
	// reconciled by an authoritative read.
	ErrPreparationUnknown = errors.New("flow preparation outcome is unknown")
	// ErrPreparationStale means the Flow ID now names a different preparation
	// generation, so callers must not compensate the returned replacement.
	ErrPreparationStale = errors.New("flow preparation generation is stale")
)

// PreparationFinalizer is a Flow-bound one-shot capability. The concrete type
// is private so callers cannot construct a capability for an arbitrary Flow ID.
type PreparationFinalizer interface {
	Finalize(func() error) (FlowRecord, error)
	Compensate(notes string) (FlowRecord, error)
	// CompensateUnderReservation is Compensate for a caller that already holds
	// this Flow's launch/close reservation. Compensate acquires that reservation
	// itself and must not be used while one is already held.
	CompensateUnderReservation(notes string) (FlowRecord, error)
}

type preparationFinalizer struct {
	mu       sync.Mutex
	store    *Store
	flowID   string
	nonce    string
	created  FlowRecord
	consumed bool
}

// CreatePreparation creates an ordinary receipt-less Flow and returns the sole
// capability that may stamp its preparation receipt.
func (s *Store) CreatePreparation(record FlowRecord, opts CreateOptions) (FlowRecord, PreparationFinalizer, error) {
	if strings.TrimSpace(record.FlowID) == "" {
		flowID, err := s.AllocateID(record.Title)
		if err != nil {
			return FlowRecord{}, nil, err
		}
		record.FlowID = flowID
	}
	nonce, err := newPreparationNonce()
	if err != nil {
		return FlowRecord{}, nil, err
	}
	created, err := s.createWithOptions(record, opts, nonce)
	if err == nil {
		return created, newPreparationFinalizer(s, created, nonce), nil
	}

	// The INSERT may be durable even when its commit acknowledgement fails.
	// Recover the capability only when an authoritative read proves that the
	// locally generated nonce owns the requested ID.
	authoritative, readErr := s.Read(record.FlowID)
	if readErr != nil {
		if IsNotFound(readErr) {
			return FlowRecord{}, nil, err
		}
		return FlowRecord{}, nil, errors.Join(ErrPreparationUnknown, err, fmt.Errorf("read preparation create: %w", readErr))
	}
	if authoritative.PreparationNonce != nonce || preparationCreateStateClaimed(record, authoritative) {
		return FlowRecord{}, nil, err
	}
	return authoritative, newPreparationFinalizer(s, authoritative, nonce), nil
}

func preparationCreateStateClaimed(requested, authoritative FlowRecord) bool {
	if authoritative.PreparedAt != nil || FlowClosed(authoritative) ||
		authoritative.Status != StatusPending || DeriveStatus(authoritative) != StatusPending {
		return true
	}
	for _, pair := range [][2]string{
		{requested.WorktreePath, authoritative.WorktreePath},
		{requested.Branch, authoritative.Branch},
		{requested.BaseRef, authoritative.BaseRef},
		{requested.Commit, authoritative.Commit},
		{requested.PlanID, authoritative.PlanID},
		{requested.PlanPath, authoritative.PlanPath},
	} {
		if strings.TrimSpace(pair[0]) != strings.TrimSpace(pair[1]) {
			return true
		}
	}
	requestedPhases := make(map[string]FlowPhase, len(requested.Phases))
	for _, phase := range requested.Phases {
		requestedPhases[phase.PhaseID] = phase
	}
	for _, phase := range authoritative.Phases {
		requestedPhase := requestedPhases[phase.PhaseID]
		if !slices.Equal(requestedPhase.LaunchIDs, phase.LaunchIDs) || !slices.Equal(requestedPhase.Sessions, phase.Sessions) {
			return true
		}
	}
	return false
}

func newPreparationFinalizer(store *Store, created FlowRecord, nonce string) PreparationFinalizer {
	return &preparationFinalizer{
		store:   store,
		flowID:  created.FlowID,
		nonce:   nonce,
		created: clonePreparationSnapshot(created),
	}
}

func clonePreparationSnapshot(record FlowRecord) FlowRecord {
	record.Phases = append([]FlowPhase(nil), record.Phases...)
	for i := range record.Phases {
		record.Phases[i].DependsOn = append([]string(nil), record.Phases[i].DependsOn...)
		record.Phases[i].LaunchIDs = append([]string(nil), record.Phases[i].LaunchIDs...)
		record.Phases[i].Sessions = append([]Session(nil), record.Phases[i].Sessions...)
	}
	return record
}

// Finalize runs bootstrap exactly once and stamps the receipt only after the
// callback succeeds. A commit error is reconciled with an authoritative read:
// a visible receipt is success, a visible nil receipt is incomplete, and an
// unreadable result is unknown. A failed identity read happens before any
// receipt write, so it is not unknown and leaves this capability retryable.
func (f *preparationFinalizer) Finalize(bootstrap func() error) (FlowRecord, error) {
	current, err := f.store.Read(f.flowID)
	if err != nil {
		return FlowRecord{}, fmt.Errorf("read flow generation before preparation: %w", err)
	}
	if current.PreparationNonce != f.nonce {
		return current, errors.Join(ErrPreparationStale, fmt.Errorf("flow %q generation changed before preparation finalization", f.flowID))
	}
	if err := f.consume(); err != nil {
		return FlowRecord{}, err
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
		if record.PreparationNonce != f.nonce {
			return FlowRecord{}, fmt.Errorf("flow %q preparation generation changed", record.FlowID)
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
	if authoritative.PreparationNonce != f.nonce {
		return authoritative, errors.Join(ErrPreparationStale, err, fmt.Errorf("flow %q generation changed before preparation finalization", f.flowID))
	}
	if authoritative.PreparedAt != nil {
		return authoritative, nil
	}
	return authoritative, errors.Join(ErrPreparationIncomplete, err)
}

// Compensate atomically turns every currently launchable root of this exact
// receipt-less preparation into a blocked phase. It acquires the same
// launch/close reservation as agent spawn before consuming this capability, then
// revalidates the generation and pending state inside the writer transaction so
// a stale creator cannot overwrite a claimant or a same-ID replacement. A
// reservation timeout, or two writer attempts that reconcile as unlanded,
// leaves the finalizer usable for a later retry. Callers that already hold that
// reservation must use CompensateUnderReservation instead.
func (f *preparationFinalizer) Compensate(notes string) (FlowRecord, error) {
	notes, err := f.compensationNotes(notes)
	if err != nil {
		return FlowRecord{}, err
	}
	release, err := f.store.acquireLaunchCloseLock(f.flowID)
	if err != nil {
		return FlowRecord{}, err
	}
	defer release()
	if err := f.consume(); err != nil {
		return FlowRecord{}, err
	}
	return f.compensateUnderReservation(notes)
}

// CompensateUnderReservation is Compensate without taking a second launch/close
// reservation. The caller must already hold that fence for this Flow.
func (f *preparationFinalizer) CompensateUnderReservation(notes string) (FlowRecord, error) {
	notes, err := f.compensationNotes(notes)
	if err != nil {
		return FlowRecord{}, err
	}
	if err := f.consume(); err != nil {
		return FlowRecord{}, err
	}
	return f.compensateUnderReservation(notes)
}

func (f *preparationFinalizer) compensationNotes(notes string) (string, error) {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return "", errors.New("flow preparation compensation requires notes")
	}
	return notes, nil
}

func (f *preparationFinalizer) compensateUnderReservation(notes string) (FlowRecord, error) {
	record, updateErr, mutationErr := f.compensateOnce(notes)
	if updateErr == nil || mutationErr != nil {
		return record, updateErr
	}
	authoritative, visible, reconcileErr := f.reconcileCompensation(notes, updateErr)
	if reconcileErr != nil || visible {
		return authoritative, reconcileErr
	}

	// The acknowledgement failed and the authoritative read confirmed that the
	// first transaction did not land. Retry once while this finalizer still owns
	// the launch/close reservation; the nonce and claim checks run again inside
	// the new writer transaction.
	record, updateErr, mutationErr = f.compensateOnce(notes)
	if updateErr == nil || mutationErr != nil {
		return record, updateErr
	}
	authoritative, visible, reconcileErr = f.reconcileCompensation(notes, updateErr)
	if reconcileErr != nil || visible {
		return authoritative, reconcileErr
	}
	f.restore()
	return authoritative, errors.Join(ErrPreparationIncomplete, updateErr)
}

func (f *preparationFinalizer) compensateOnce(notes string) (FlowRecord, error, error) {
	var mutationErr error
	record, updateErr := f.store.backend.update(f.flowID, func(sess flowSession) (FlowRecord, error) {
		var record FlowRecord
		record, mutationErr = f.compensateSession(sess, notes)
		return record, mutationErr
	})
	return record, updateErr, mutationErr
}

func (f *preparationFinalizer) compensateSession(sess flowSession, notes string) (FlowRecord, error) {
	stored, found, err := sess.get()
	if err != nil {
		return FlowRecord{}, err
	}
	if !found {
		return FlowRecord{}, flowNotFoundError(f.flowID)
	}
	record := stored.record
	if record.PreparationNonce != f.nonce {
		return FlowRecord{}, fmt.Errorf("flow %q preparation generation changed", f.flowID)
	}
	if record.PreparedAt != nil {
		return FlowRecord{}, fmt.Errorf("flow %q already has a preparation receipt", f.flowID)
	}
	if preparationStateClaimed(f.created, record) {
		return FlowRecord{}, fmt.Errorf("flow %q preparation was claimed before compensation", f.flowID)
	}
	if FlowClosed(record) {
		return FlowRecord{}, closedFlowMutationError(record, "compensate preparation for")
	}
	if DeriveStatus(record) != StatusPending {
		return FlowRecord{}, fmt.Errorf("flow %q must be pending to compensate preparation", f.flowID)
	}
	if err := validatePhaseGraphResolved(record); err != nil {
		return FlowRecord{}, err
	}

	now := flowMutationTime(record, f.store.now())
	record = refreshPhaseReadiness(record, now)
	changed := false
	for i := range record.Phases {
		if !phaseLaunchEligibleAtIndex(record, i) {
			continue
		}
		phase := record.Phases[i]
		phase.Status = PhaseBlocked
		phase.Notes = notes
		if SemanticKind(phase) == KindPlanReview {
			phase.Outcome = OutcomeBlocked
		}
		phase.UpdatedAt = now
		record.Phases[i] = phase
		changed = true
	}
	if !changed {
		return record, nil
	}
	record.UpdatedAt = now
	record = refreshPhaseReadiness(record, now)
	record.Status = DeriveStatus(record)
	if err := f.store.saveSession(sess, record); err != nil {
		return FlowRecord{}, err
	}
	return record, nil
}

func (f *preparationFinalizer) reconcileCompensation(notes string, updateErr error) (FlowRecord, bool, error) {
	authoritative, readErr := f.store.Read(f.flowID)
	if readErr != nil {
		return FlowRecord{}, false, errors.Join(ErrPreparationUnknown, updateErr, fmt.Errorf("read preparation compensation: %w", readErr))
	}
	if authoritative.PreparationNonce != f.nonce || authoritative.PreparedAt != nil {
		return authoritative, false, updateErr
	}
	if preparationCompensationVisible(f.created, authoritative, notes) {
		return authoritative, true, nil
	}
	if preparationStateClaimed(f.created, authoritative) {
		return authoritative, false, updateErr
	}
	return authoritative, false, nil
}

func preparationCompensationVisible(created, current FlowRecord, notes string) bool {
	created = refreshPhaseReadiness(clonePreparationSnapshot(created), created.UpdatedAt)
	current = refreshPhaseReadiness(clonePreparationSnapshot(current), current.UpdatedAt)
	currentPhases := make(map[string]FlowPhase, len(current.Phases))
	for _, phase := range current.Phases {
		currentPhases[phase.PhaseID] = phase
	}
	for i, initial := range created.Phases {
		if !phaseLaunchEligibleAtIndex(created, i) {
			continue
		}
		phase, ok := currentPhases[initial.PhaseID]
		if !ok || phase.Status != PhaseBlocked || phase.Notes != notes {
			return false
		}
		if SemanticKind(phase) == KindPlanReview && phase.Outcome != OutcomeBlocked {
			return false
		}
	}
	for i := range current.Phases {
		if phaseLaunchEligibleAtIndex(current, i) {
			return false
		}
	}
	return true
}

func (f *preparationFinalizer) consume() error {
	if f == nil || f.store == nil || strings.TrimSpace(f.flowID) == "" || strings.TrimSpace(f.nonce) == "" {
		return errors.New("invalid flow preparation finalizer")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumed {
		return errors.New("flow preparation finalizer was already consumed")
	}
	f.consumed = true
	return nil
}

func (f *preparationFinalizer) restore() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumed = false
}

// PreparationLaunchBlocked reports that a nonce-bearing preparation has not
// yet stamped its receipt, so another process must not launch or persist a
// launch ID. Migrated records without a nonce stay launchable.
func PreparationLaunchBlocked(record FlowRecord) bool {
	return strings.TrimSpace(record.PreparationNonce) != "" && record.PreparedAt == nil
}

// SamePreparationIdentity reports whether two records name the same
// receipt-less preparation generation. Prefer the storage-only nonce whenever
// either record carries one. Compare the legacy PreparationGeneration field
// only for migrated records that have no nonce.
func SamePreparationIdentity(a, b FlowRecord) bool {
	aNonce := strings.TrimSpace(a.PreparationNonce)
	bNonce := strings.TrimSpace(b.PreparationNonce)
	if aNonce != "" || bNonce != "" {
		return aNonce == bNonce
	}
	return a.PreparationGeneration == b.PreparationGeneration
}

func newPreparationNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate flow preparation nonce: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func preparationStateClaimed(created, current FlowRecord) bool {
	for _, pair := range [][2]string{
		{created.WorktreePath, current.WorktreePath},
		{created.Branch, current.Branch},
		{created.BaseRef, current.BaseRef},
		{created.Commit, current.Commit},
		{created.PlanID, current.PlanID},
		{created.PlanPath, current.PlanPath},
	} {
		if strings.TrimSpace(pair[0]) != strings.TrimSpace(pair[1]) {
			return true
		}
	}
	if len(created.Phases) != len(current.Phases) {
		return true
	}
	createdPhases := make(map[string]FlowPhase, len(created.Phases))
	for _, phase := range created.Phases {
		createdPhases[phase.PhaseID] = phase
	}
	for _, phase := range current.Phases {
		initial, ok := createdPhases[phase.PhaseID]
		if !ok || phase.Status != initial.Status ||
			!slices.Equal(phase.LaunchIDs, initial.LaunchIDs) || !slices.Equal(phase.Sessions, initial.Sessions) {
			return true
		}
	}
	return false
}

// IsPreparationIncomplete reports a confirmed receipt-less outcome.
func IsPreparationIncomplete(err error) bool { return errors.Is(err, ErrPreparationIncomplete) }

// IsPreparationUnknown reports a persistence outcome that could not be read
// authoritatively after a failed receipt write.
func IsPreparationUnknown(err error) bool { return errors.Is(err, ErrPreparationUnknown) }

// IsPreparationStale reports that the Flow ID now names another preparation
// generation and is therefore unsafe to mutate as compensation.
func IsPreparationStale(err error) bool { return errors.Is(err, ErrPreparationStale) }
