package launchcontrol

import (
	"time"
)

const (
	// RetentionAge is how long a launch directory is kept after its last state
	// change. It exceeds every grace and every replay window by orders of
	// magnitude, so retention can never delete a directory the sweep still
	// needs.
	RetentionAge = 14 * 24 * time.Hour
	// retentionInterval spaces retention passes within a long-lived process.
	retentionInterval = 24 * time.Hour
	// retentionLockProbe is how long retention waits for a launch's lock; a
	// held lock means someone is working on the launch, and it is skipped.
	retentionLockProbe = 50 * time.Millisecond
)

// Retain deletes launch directories with no pending request whose retention
// age is older than RetentionAge. Age is checked before any lock is taken;
// a directory whose lock is held is left alone. Retired launches also lose
// their in-memory registration.
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
