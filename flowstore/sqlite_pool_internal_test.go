package flowstore

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Under WAL a read must never wait on a writer. That holds inside SQLite, but
// it can be undone in the pool: with _txlock=immediate a writer issues BEGIN
// IMMEDIATE at BeginTx and then holds its pooled connection for the whole
// busy_timeout while it waits its turn. Cap MaxOpenConns near the number of
// concurrent writers and parked writers own every connection, so a read queues
// in database/sql instead — measured at 2.74s behind four parked writers with a
// cap of 4, versus ~1ms uncapped. The TUI's Flows pane is exactly that read.
func TestReadsDoNotQueueBehindParkedWriters(t *testing.T) {
	const parkedWriters = 6
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	if _, err := store.Create(FlowRecord{Title: "read me", Instructions: "i", RepoPath: "/tmp/repo"}); err != nil {
		t.Fatal(err)
	}

	backend := store.backend.(*sqliteBackend)

	// Hold the SQLite write lock with a transaction nothing else can preempt, so
	// every writer below parks in BEGIN IMMEDIATE holding a pooled connection.
	blocker, err := backend.beginTx(context.Background())
	if err != nil {
		t.Fatalf("beginTx() error = %v", err)
	}
	if _, err := blocker.Exec("CREATE TABLE IF NOT EXISTS writer_lock_probe(id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	var wg sync.WaitGroup
	parked := make(chan struct{}, parkedWriters)
	for i := 0; i < parkedWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parked <- struct{}{}
			// Expected to fail on the busy timeout; the point is that it occupies a
			// connection while it waits.
			_, _ = store.SetAutoMode(AutoModeUpdate{FlowID: "missing-flow", Enabled: true})
		}()
	}
	for i := 0; i < parkedWriters; i++ {
		<-parked
	}
	// Wait for the writers to ACTUALLY check out connections, rather than sleeping
	// and hoping. A bare sleep turns a loaded machine into a false negative: the
	// read would run against an empty pool and the test would pass while testing
	// nothing. InUse counts the blocker plus every parked writer.
	deadline := time.Now().Add(2 * time.Second)
	for backend.db.Stats().InUse < parkedWriters+1 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d connections were checked out; the writers never parked, so this "+
				"test would not have exercised pool starvation",
				backend.db.Stats().InUse, parkedWriters+1)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The goroutine only reports; every assertion happens on the test goroutine.
	// Calling t.Errorf here could race a t.Fatalf below that has already run the
	// deferred Close, and would also skip the rollback that releases the parked
	// writers.
	type readResult struct {
		elapsed time.Duration
		err     error
	}
	readDone := make(chan readResult, 1)
	go func() {
		start := time.Now()
		_, err := store.List(FlowFilter{})
		readDone <- readResult{elapsed: time.Since(start), err: err}
	}()

	var result readResult
	timedOut := false
	select {
	case result = <-readDone:
	case <-time.After(3 * time.Second):
		timedOut = true
	}

	// Release the writers before asserting, so a failure still unwinds cleanly.
	_ = blocker.Rollback()
	wg.Wait()

	switch {
	case timedOut:
		t.Fatalf("List() did not complete behind %d parked writers; readers are starved by the "+
			"connection pool", parkedWriters)
	case result.err != nil:
		t.Fatalf("List() error = %v", result.err)
	case result.elapsed > time.Second:
		// Generous: the failure mode is a multi-second stall, and a healthy read
		// here is sub-millisecond.
		t.Fatalf("List() took %s behind %d parked writers; readers are queueing for pooled "+
			"connections. Do not cap MaxOpenConns near the writer count.", result.elapsed, parkedWriters)
	}
}

// A panicking mutate must not abandon a BEGIN IMMEDIATE. If it did, the
// connection would keep the database-wide write lock and every later write
// would fail with SQLITE_BUSY while reads kept working — a wedged writer with
// no obvious cause. backend_sqlite.go's deferred Rollback is what prevents it.
func TestPanickingMutateLeavesTheWriterUsable(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root, LockTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	record, err := store.Create(FlowRecord{Title: "survivor", Instructions: "i", RepoPath: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}

	backend := store.backend.(*sqliteBackend)
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("expected the panic to propagate out of update")
			}
		}()
		_, _ = backend.update(record.FlowID, func(flowSession) (FlowRecord, error) {
			panic("mutate exploded")
		})
	}()

	// The writer must still be available to everyone else.
	for i := 0; i < 4; i++ {
		if _, err := store.SetAutoMode(AutoModeUpdate{FlowID: record.FlowID, Enabled: i%2 == 0}); err != nil {
			t.Fatalf("SetAutoMode() after a panicking mutate error = %v; the abandoned transaction "+
				"is still holding the database-wide write lock", err)
		}
	}
}
