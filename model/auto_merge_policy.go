package model

import (
	"fmt"
	"sync"

	"github.com/approachcontrol/approach/flowstore"
)

// autoMergePolicyGate linearizes successful global setting changes with the
// final launch-ID write. Bubble Tea commands run concurrently with Update, so
// copying the setting into a launch intent is not enough to revoke work that
// was admitted before the config save completed.
type autoMergePolicyGate struct {
	mu         sync.Mutex
	enabled    bool
	generation uint64
}

type globalAutoMergeWrite struct {
	inFlight bool
	value    bool
	desired  bool
	request  uint64
}

func newAutoMergePolicyGate(enabled bool) *autoMergePolicyGate {
	return &autoMergePolicyGate{enabled: enabled}
}

func (gate *autoMergePolicyGate) snapshot() (bool, uint64) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.enabled, gate.generation
}

func (gate *autoMergePolicyGate) save(enabled bool, persist func(bool) error) error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if err := persist(enabled); err != nil {
		return err
	}
	if gate.enabled == enabled {
		return nil
	}
	gate.enabled = enabled
	gate.generation++
	return nil
}

func (gate *autoMergePolicyGate) addGlobalSnapshotLaunch(
	generation uint64,
	add func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error),
	update flowstore.PhaseLaunchUpdate,
) (flowstore.FlowRecord, error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if !gate.enabled || gate.generation != generation {
		return flowstore.FlowRecord{}, fmt.Errorf("global auto-merge policy changed: %w", flowstore.ErrAutoLaunchOutdated)
	}
	return add(update)
}
