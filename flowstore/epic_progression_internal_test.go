package flowstore

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEpicProgressionCodecRejectsInvalidHaltStates(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	validHalt := &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "child blocked"}
	base := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      "/repo",
		EpicID:        "epic",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
	tests := []struct {
		name   string
		record EpicProgression
		want   string
	}{
		{name: "enabled with halt", record: func() EpicProgression { r := base; r.Enabled = true; r.Halt = validHalt; return r }(), want: "cannot be halted"},
		{name: "enabled and done", record: func() EpicProgression { r := base; r.Enabled = true; r.Done = true; return r }(), want: "cannot be done"},
		{name: "done with halt", record: func() EpicProgression { r := base; r.Done = true; r.Halt = validHalt; return r }(), want: "cannot be halted"},
		{name: "incomplete halt", record: func() EpicProgression {
			r := base
			r.Halt = &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked}
			return r
		}(), want: "incomplete"},
		{name: "unknown halt", record: func() EpicProgression {
			r := base
			r.Halt = &EpicProgressionHalt{ChildBeadID: "epic.1", Status: "paused", Message: "unknown"}
			return r
		}(), want: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := encodeEpicProgression(tt.record); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("encodeEpicProgression() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEpicProgressionCodecRoundTripsEveryDurableState(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	for _, record := range []EpicProgression{
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "active", Enabled: true, CreatedAt: stamp, UpdatedAt: stamp},
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "off", CreatedAt: stamp, UpdatedAt: stamp},
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "done", Done: true, CreatedAt: stamp, UpdatedAt: stamp},
		{SchemaVersion: 1, RepoPath: "/repo", EpicID: "halted", Halt: &EpicProgressionHalt{ChildBeadID: "halted.1", Status: StatusNeedsAttention, Message: "needs attention"}, CreatedAt: stamp, UpdatedAt: stamp},
	} {
		t.Run(record.EpicID, func(t *testing.T) {
			data, updatedAt, err := encodeEpicProgression(record)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeEpicProgression(record.RepoPath, record.EpicID, boolToSQLite(record.Enabled), updatedAt, data)
			if err != nil || !reflect.DeepEqual(got, record) {
				t.Fatalf("round trip = %#v, err %v; want %#v", got, err, record)
			}
		})
	}
}

func TestEpicProgressionSerializedWriterOrdersDoneAndManualOff(t *testing.T) {
	for _, tt := range []struct {
		name       string
		firstDone  bool
		queuedDone bool
		wantErr    bool
		wantDone   bool
	}{
		{name: "manual off commits before queued done", firstDone: false, queuedDone: true, wantErr: true},
		{name: "done commits before queued manual off", firstDone: true, queuedDone: false},
		{name: "done commits before queued redundant done", firstDone: true, queuedDone: true, wantDone: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			stamp := time.Date(2026, 8, 14, 21, 0, 0, 0, time.UTC)
			first, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return stamp }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			second, err := NewStore(StoreOptions{Root: root, Now: func() time.Time { return stamp.Add(2 * time.Minute) }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
			active, err := first.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}

			backend := first.backend.(*sqliteBackend)
			tx, err := backend.beginTx(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			committedFirst := active
			committedFirst.Enabled = false
			committedFirst.Done = tt.firstDone
			committedFirst.UpdatedAt = stamp.Add(time.Minute)
			data, updatedAt, err := encodeEpicProgression(committedFirst)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("UPDATE epic_progressions SET enabled=0, updated_at=?, record=? WHERE repo_path=? AND epic_id=?", updatedAt, data, key.RepoPath, key.EpicID); err != nil {
				t.Fatal(err)
			}

			secondBackend := second.backend.(*sqliteBackend)
			originalBegin := secondBackend.beginTx
			started := make(chan struct{})
			secondBackend.beginTx = func(ctx context.Context) (sqliteTransaction, error) {
				close(started)
				return originalBegin(ctx)
			}
			type writeResult struct {
				progression EpicProgression
				err         error
			}
			resultCh := make(chan writeResult, 1)
			go func() {
				progression, err := second.SetEpicProgression(EpicProgressionUpdate{Key: key, Done: tt.queuedDone})
				resultCh <- writeResult{progression: progression, err: err}
			}()
			<-started
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			result := <-resultCh
			if (result.err != nil) != tt.wantErr {
				t.Fatalf("queued write error = %v, want error %t", result.err, tt.wantErr)
			}
			got, found, err := first.ReadEpicProgression(key)
			if err != nil || !found || got.Enabled || got.Done != tt.wantDone {
				t.Fatalf("final progression = %#v, found %t, err %v", got, found, err)
			}
			if tt.queuedDone && !tt.wantErr && !got.UpdatedAt.Equal(committedFirst.UpdatedAt) {
				t.Fatalf("redundant done updated timestamp from %s to %s", committedFirst.UpdatedAt, got.UpdatedAt)
			}
			if tt.wantErr && !reflect.DeepEqual(got, committedFirst) {
				t.Fatalf("rejected queued done changed row: got %#v, want %#v", got, committedFirst)
			}
		})
	}
}

func TestEpicProgressionDoneRejectsAuthoritativeHaltAndSuccessorTreatsDoneInactive(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return stamp.Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
	halted := EpicProgression{SchemaVersion: 1, RepoPath: key.RepoPath, EpicID: key.EpicID, Halt: &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "blocked"}, CreatedAt: stamp, UpdatedAt: stamp}
	data, updatedAt, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.db.Exec("INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)", key.RepoPath, key.EpicID, updatedAt, data); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Done: true}); err == nil {
		t.Fatal("halted -> done succeeded")
	}
	got, found, err := store.ReadEpicProgression(key)
	if err != nil || !found || !reflect.DeepEqual(got, halted) {
		t.Fatalf("halted row after rejected done = %#v, found %t, err %v", got, found, err)
	}

	active, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil || !active.Enabled || active.Done || active.Halt != nil {
		t.Fatalf("enable halted = %#v, err %v", active, err)
	}
	done, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Done: true})
	if err != nil || !done.Done {
		t.Fatalf("active -> done = %#v, err %v", done, err)
	}
	result, err := store.ReconcileEpicProgressionSuccessor(EpicProgressionSuccessorUpdate{
		FlowID: "absent-flow", Key: key, Bead: BeadLink{ID: "epic.2", EpicID: key.EpicID},
	})
	if err != nil || result.Outcome != EpicProgressionSuccessorInactive || !result.Progression.Done {
		t.Fatalf("done successor reconciliation = %#v, err %v", result, err)
	}
}

func TestEpicProgressionStickyHaltRetentionAndClearing(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
	halted := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      key.RepoPath,
		EpicID:        key.EpicID,
		Halt:          &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusNeedsAttention, Message: "needs a decision"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	data, updatedAt, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)`,
		key.RepoPath, key.EpicID, updatedAt, data); err != nil {
		t.Fatal(err)
	}
	disabled, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Halt == nil || *disabled.Halt != *halted.Halt || !disabled.UpdatedAt.Equal(halted.UpdatedAt) {
		t.Fatalf("disabled halted progression = %#v, want sticky no-op", disabled)
	}
	now = now.Add(time.Minute)
	enabled, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || enabled.Halt != nil || !enabled.UpdatedAt.Equal(now) {
		t.Fatalf("enabled progression = %#v, want cleared halt", enabled)
	}
}

func TestReconcileEpicProgressionSuccessorHaltedPrecedesFlowState(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := EpicProgressionKey{RepoPath: "/repo", EpicID: "epic"}
	halted := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      key.RepoPath,
		EpicID:        key.EpicID,
		Halt:          &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "blocked"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	data, updatedAt, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	backend := store.backend.(*sqliteBackend)
	if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, 0, ?, ?)`,
		key.RepoPath, key.EpicID, updatedAt, data); err != nil {
		t.Fatal(err)
	}

	link := BeadLink{ID: "epic.2", EpicID: key.EpicID}
	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "prepared", RepoPath: key.RepoPath, Title: "Prepared", Instructions: "Test.", Bead: link,
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/prepared",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}

	for _, flowID := range []string{"absent", flow.FlowID} {
		result, err := store.ReconcileEpicProgressionSuccessor(EpicProgressionSuccessorUpdate{
			FlowID: flowID, Key: key, Bead: link,
		})
		if err != nil || result.Outcome != EpicProgressionSuccessorInactive {
			t.Fatalf("flow %q result = %#v, err %v; want inactive", flowID, result, err)
		}
	}
}

func TestReadEpicProgressionReportsCorruptRowsAsErrors(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	valid := EpicProgression{
		SchemaVersion: epicProgressionSchemaVersion,
		RepoPath:      "/repo",
		EpicID:        "epic",
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
	validData, validUpdatedAt, err := encodeEpicProgression(valid)
	if err != nil {
		t.Fatal(err)
	}
	missingEnabled := []byte(strings.Replace(string(validData), "  \"enabled\": false,\n", "", 1))
	nullEnabled := []byte(strings.Replace(string(validData), "\"enabled\": false", "\"enabled\": null", 1))
	missingDone := []byte(strings.Replace(string(validData), "  \"done\": false,\n", "", 1))
	nullDone := []byte(strings.Replace(string(validData), "\"done\": false", "\"done\": null", 1))
	wrongTypeDone := []byte(strings.Replace(string(validData), "\"done\": false", "\"done\": \"false\"", 1))
	duplicateDone := []byte(strings.Replace(string(validData), "\"done\": false", "\"done\": false,\n  \"done\": true", 1))
	caseVariantDone := []byte(strings.Replace(string(validData), "\"done\": false", "\"done\": false,\n  \"Done\": false", 1))
	caseVariantEnabled := []byte(strings.Replace(string(validData), "\"enabled\": false", "\"Enabled\": false", 1))
	halted := valid
	halted.Halt = &EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusBlocked, Message: "blocked"}
	haltedData, _, err := encodeEpicProgression(halted)
	if err != nil {
		t.Fatal(err)
	}
	duplicateNestedHalt := []byte(strings.Replace(string(haltedData), "\"child_bead_id\": \"epic.1\"", "\"child_bead_id\": \"epic.1\",\n    \"child_bead_id\": \"epic.1\"", 1))
	caseVariantNestedHalt := []byte(strings.Replace(string(haltedData), "\"status\": \"blocked\"", "\"status\": \"blocked\",\n    \"Status\": \"blocked\"", 1))
	for _, tt := range []struct {
		name      string
		enabled   int
		updatedAt string
		data      []byte
	}{
		{name: "malformed blob", enabled: 0, updatedAt: validUpdatedAt, data: []byte("{")},
		{name: "missing enabled", enabled: 0, updatedAt: validUpdatedAt, data: missingEnabled},
		{name: "null enabled", enabled: 0, updatedAt: validUpdatedAt, data: nullEnabled},
		{name: "missing done", enabled: 0, updatedAt: validUpdatedAt, data: missingDone},
		{name: "null done", enabled: 0, updatedAt: validUpdatedAt, data: nullDone},
		{name: "wrong type done", enabled: 0, updatedAt: validUpdatedAt, data: wrongTypeDone},
		{name: "duplicate done", enabled: 0, updatedAt: validUpdatedAt, data: duplicateDone},
		{name: "case-variant done", enabled: 0, updatedAt: validUpdatedAt, data: caseVariantDone},
		{name: "case-variant enabled", enabled: 0, updatedAt: validUpdatedAt, data: caseVariantEnabled},
		{name: "duplicate nested halt field", enabled: 0, updatedAt: validUpdatedAt, data: duplicateNestedHalt},
		{name: "case-variant nested halt field", enabled: 0, updatedAt: validUpdatedAt, data: caseVariantNestedHalt},
		{name: "projection mismatch", enabled: 1, updatedAt: validUpdatedAt, data: validData},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			backend := store.backend.(*sqliteBackend)
			if _, err := backend.db.Exec("DROP TRIGGER guard_epic_progression_done_insert; DROP TRIGGER guard_epic_progression_done_record_update"); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.db.Exec(`INSERT INTO epic_progressions(repo_path, epic_id, enabled, updated_at, record) VALUES(?, ?, ?, ?, ?)`,
				valid.RepoPath, valid.EpicID, tt.enabled, tt.updatedAt, tt.data); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.ReadEpicProgression(EpicProgressionKey{RepoPath: valid.RepoPath, EpicID: valid.EpicID}); err == nil || found {
				t.Fatalf("ReadEpicProgression() = found %t, err %v; want corruption error", found, err)
			}
		})
	}
}

func TestHaltEpicProgressionWritesDisabledProjectionAndStaysInactive(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Root: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := filepath.Join(t.TempDir(), "repo")
	key := EpicProgressionKey{RepoPath: repo, EpicID: "epic"}
	link := BeadLink{ID: "epic.2", EpicID: key.EpicID}
	flow, finalizer, err := store.CreatePreparation(FlowRecord{
		FlowID: "prepared", RepoPath: repo, Title: "Prepared", Instructions: "Test.", Bead: link,
	}, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(StartMetadataUpdate{
		FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/prepared",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}

	if _, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	halt := EpicProgressionHalt{ChildBeadID: "epic.1", Status: StatusNeedsAttention, Message: "child Flow flow-1 halted auto-progression"}
	halted, err := store.HaltEpicProgression(EpicProgressionHaltUpdate{Key: key, Halt: halt})
	if err != nil {
		t.Fatal(err)
	}

	backend := store.backend.(*sqliteBackend)
	var enabled int
	if err := backend.db.QueryRow(
		"SELECT enabled FROM epic_progressions WHERE repo_path = ? AND epic_id = ?", key.RepoPath, key.EpicID,
	).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 {
		t.Fatalf("enabled projection after halt = %d, want 0", enabled)
	}

	result, err := store.ReconcileEpicProgressionSuccessor(EpicProgressionSuccessorUpdate{FlowID: flow.FlowID, Key: key, Bead: link})
	if err != nil || result.Outcome != EpicProgressionSuccessorInactive {
		t.Fatalf("halted successor reconciliation = %#v, err %v; want inactive", result, err)
	}

	now = now.Add(time.Minute)
	sticky, err := store.SetEpicProgression(EpicProgressionUpdate{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if sticky.Halt == nil || *sticky.Halt != halt || !sticky.UpdatedAt.Equal(halted.UpdatedAt) {
		t.Fatalf("explicit off after halt = %#v, want sticky no-op on %#v", sticky, halted)
	}

	now = now.Add(time.Minute)
	cleared, _, err := store.EnableEpicProgressionForPreparedFlow(PreparedEpicProgressionUpdate{FlowID: flow.FlowID, Key: key, Bead: link})
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.Enabled || cleared.Halt != nil || !cleared.UpdatedAt.Equal(now) {
		t.Fatalf("prepared enable after halt = %#v, want cleared halt", cleared)
	}
}
