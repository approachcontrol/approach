package flowstore

// write persists one hand-authored record through the seam, skipping every
// domain rule above it except flow-id validation and depends_on normalization.
//
// It is test-only on purpose. Production has no such path any more: after the
// seam extraction every write goes through backend.update, and the tests that
// need to plant a record in a shape the public API would never produce (legacy
// preset graphs, phases with no depends_on at all) seed it here instead.
//
// It takes the critical section like any other write, so it must not be called
// from inside one.
func (s *Store) write(record FlowRecord) error {
	if err := validateFlowID(record.FlowID); err != nil {
		return err
	}
	_, err := s.backend.update(record.FlowID, func(sess flowSession) (FlowRecord, error) {
		return record, s.saveSession(sess, record)
	})
	return err
}
