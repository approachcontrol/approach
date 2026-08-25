package launchcontrol

import (
	"errors"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

const (
	// RetentionAge is how long a launch directory is kept after its last state
	// change. It exceeds every replay window by orders of magnitude, so
	// retention can never delete a directory the sweep still needs.
	RetentionAge = 14 * 24 * time.Hour
	// retentionInterval spaces retention passes within a long-lived process.
	retentionInterval = 24 * time.Hour
	// retentionLockProbe is how long retention waits for a launch's lock; a
	// held lock means someone is working on the launch, and it is skipped.
	retentionLockProbe = 50 * time.Millisecond
)

// Retain deletes launch directories with no pending request whose retention
// age is older than RetentionAge and whose launch no longer owns a running
// phase — a launch that does may still have an agent, and its next result
// authenticates with the registration retirement would drop. Age is checked
// before any lock is taken; a directory whose lock is held is left alone.
// Retired launches also lose their in-memory registration.
func (c *Controller) Retain() (int, error) {
	now := c.now()
	c.mu.Lock()
	c.lastRetain = now
	c.mu.Unlock()
	ids, err := ListLaunchIDs(c.root)
	if err != nil {
		return 0, err
	}
	retired := 0
	for _, id := range ids {
		log, err := OpenLog(c.root, id)
		if err != nil {
			continue
		}
		age, err := log.AgeForRetention()
		if err != nil || now.Sub(age) < RetentionAge {
			continue
		}
		pending, err := log.Pending()
		if err != nil || len(pending) > 0 {
			continue
		}
		if c.ownsRunningPhase(log) {
			continue
		}
		lock := c.launchLock(id)
		if !lock.TryLock() {
			continue
		}
		unlock, err := log.Lock(retentionLockProbe)
		if err != nil {
			lock.Unlock()
			continue
		}
		pending, err = log.Pending()
		if err == nil && len(pending) == 0 {
			if err := log.Remove(); err == nil {
				retired++
				c.mu.Lock()
				delete(c.registrations, id)
				c.mu.Unlock()
			}
		}
		unlock()
		lock.Unlock()
	}
	return retired, nil
}

// ownsRunningPhase reports whether the launch is the latest launch of a phase
// that is still running.
//
// An ABSENT answer is no: a launch.json that says the launch owns no phase, a
// Flow that no longer exists, or a phase gone from the Flow are all definite.
// An UNREADABLE one is not an answer at all and counts as yes, so retirement
// skips it: a Flow the store could not read this once says nothing about
// whether an agent is still running under it, and retiring on that guess
// deletes the identity a live agent's next proxied result is authenticated
// against. Waiting for the next sweep costs a directory that lives a little
// longer.
func (c *Controller) ownsRunningPhase(log *Log) bool {
	info, ok, err := log.Launch()
	if err != nil {
		return true
	}
	if !ok || info.FlowID == "" || info.PhaseID == "" {
		return false
	}
	record, err := c.store.Read(info.FlowID)
	if err != nil {
		return !errors.Is(err, flowstore.ErrFlowNotFound)
	}
	phase, ok := PhaseByID(record, info.PhaseID)
	if !ok {
		return false
	}
	return phase.Status == flowstore.PhaseRunning && flowstore.LatestPhaseLaunchID(phase) == log.LaunchID()
}
