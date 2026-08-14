package flowstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAsPartialListRequiresStandaloneValidDiagnostic(t *testing.T) {
	cause := errors.New("unreadable row")
	partial := &PartialListError{Entries: []PartialListEntry{{FlowID: "bad", Cause: cause}}}

	for _, err := range []error{partial, fmt.Errorf("list flows: %w", partial)} {
		got, ok := AsPartialList(err)
		if !ok || got != partial {
			t.Fatalf("AsPartialList(%v) = (%#v, %t), want original partial diagnostic", err, got, ok)
		}
	}

	fatal := errors.New("database unavailable")
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "joined fatal", err: errors.Join(partial, fatal)},
		{name: "wrapped joined fatal", err: fmt.Errorf("list flows: %w", errors.Join(partial, fatal))},
		{name: "empty", err: &PartialListError{}},
		{name: "missing flow ID", err: &PartialListError{Entries: []PartialListEntry{{Cause: cause}}}},
		{name: "missing cause", err: &PartialListError{Entries: []PartialListEntry{{FlowID: "bad"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := AsPartialList(test.err); ok || got != nil {
				t.Fatalf("AsPartialList(%v) = (%#v, %t), want fatal classification", test.err, got, ok)
			}
		})
	}
}

func TestSQLiteListReturnsHealthyRowsWithPartialDiagnostic(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	repo := filepath.Join(root, "repo")
	otherRepo := filepath.Join(root, "other")

	insertListTestRecord(t, backend, listTestRecord("healthy-new", repo, 4))
	insertListTestRecord(t, backend, listTestRecord("healthy-old", repo, 1))
	insertListTestRaw(t, backend, "future", repo, StatusPending, listTestTime(5),
		[]byte(`{"schema_version":99,"flow_id":"future"}`))
	insertListTestRaw(t, backend, "malformed", repo, StatusPending, listTestTime(3), []byte("{"))
	insertListTestRaw(t, backend, "excluded", otherRepo, StatusPending, listTestTime(6), []byte("{"))

	records, err := store.List(FlowFilter{RepoPath: repo})
	if got, want := flowIDs(records), []string{"healthy-new", "healthy-old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List() records = %v, want %v", got, want)
	}
	partial, ok := AsPartialList(err)
	if !ok {
		t.Fatalf("AsPartialList(%v) = false, want typed partial diagnostic", err)
	}
	if got, want := partialFlowIDs(partial), []string{"future", "malformed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("partial flow IDs = %v, want %v", got, want)
	}
	if got, want := err.Error(), `skipped 2 unreadable Flows: future: flow "future" has unsupported schema version 99; malformed: decode flow "malformed" record: unexpected end of JSON input`; got != want {
		t.Fatalf("List() error = %q, want %q", got, want)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("errors.As(%v, *json.SyntaxError) = false, want aggregate cause traversal", err)
	}

	if _, readErr := store.Read("malformed"); readErr == nil || IsNotFound(readErr) || !strings.Contains(readErr.Error(), "decode flow") {
		t.Fatalf("Read(malformed) error = %v, want strict non-not-found corruption", readErr)
	}
}

func TestSQLiteListAllCorruptReturnsNonNilEmptyPartialResult(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	repo := filepath.Join(root, "repo")

	insertListTestRaw(t, backend, "future", repo, StatusPending, listTestTime(2),
		[]byte(`{"schema_version":99,"flow_id":"future"}`))
	insertListTestRaw(t, backend, "malformed", repo, StatusPending, listTestTime(1), []byte("{"))

	records, err := store.List(FlowFilter{RepoPath: repo})
	if records == nil || len(records) != 0 {
		t.Fatalf("List() records = %#v, want non-nil empty result", records)
	}
	partial, ok := AsPartialList(err)
	if !ok {
		t.Fatalf("AsPartialList(%v) = false, want typed partial diagnostic", err)
	}
	if got, want := partialFlowIDs(partial), []string{"future", "malformed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("partial flow IDs = %v, want %v", got, want)
	}
}

func TestSQLiteListFilterExcludesCorruptRowsFromDiagnostic(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	backend := store.backend.(*sqliteBackend)
	includedRepo := filepath.Join(root, "included")
	excludedRepo := filepath.Join(root, "excluded")

	insertListTestRecord(t, backend, listTestRecord("healthy", includedRepo, 1))
	insertListTestRaw(t, backend, "malformed", excludedRepo, StatusPending, listTestTime(2), []byte("{"))

	records, err := store.List(FlowFilter{RepoPath: includedRepo})
	if err != nil {
		t.Fatalf("List() error = %v, want corrupt row outside filter ignored", err)
	}
	if got, want := flowIDs(records), []string{"healthy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List() records = %v, want %v", got, want)
	}
}

func TestStoreListDiscardsRowsForFatalListFailures(t *testing.T) {
	fatal := errors.New("injected fatal list failure")
	for _, test := range []struct {
		name string
		rows *fakeFlowListRows
		fail bool
	}{
		{name: "query", fail: true},
		{name: "scan", rows: &fakeFlowListRows{rows: []fakeFlowListRow{
			fakeListRowForRecord(t, listTestRecord("healthy", "/repo", 3)),
			{flowID: "corrupt", repoPath: "/repo", status: StatusPending, updatedAt: listTestTimeString(t, 2), record: []byte("{")},
			{scanErr: fatal},
		}}},
		{name: "iteration", rows: &fakeFlowListRows{
			rows: []fakeFlowListRow{
				fakeListRowForRecord(t, listTestRecord("healthy", "/repo", 3)),
				{flowID: "corrupt", repoPath: "/repo", status: StatusPending, updatedAt: listTestTimeString(t, 2), record: []byte("{")},
			},
			iterateErr: fatal,
		}},
		{name: "close", rows: &fakeFlowListRows{
			rows: []fakeFlowListRow{
				fakeListRowForRecord(t, listTestRecord("healthy", "/repo", 3)),
				{flowID: "corrupt", repoPath: "/repo", status: StatusPending, updatedAt: listTestTimeString(t, 2), record: []byte("{")},
			},
			closeErr: fatal,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			backend := store.backend.(*sqliteBackend)
			backend.queryListRows = func(string, ...any) (flowListRows, error) {
				if test.fail {
					return nil, fatal
				}
				return test.rows, nil
			}

			records, err := store.List(FlowFilter{})
			if records != nil {
				t.Fatalf("List() records = %#v, want nil beside fatal error", records)
			}
			if !errors.Is(err, fatal) {
				t.Fatalf("List() error = %v, want injected fatal cause", err)
			}
			if _, partial := AsPartialList(err); partial {
				t.Fatalf("List() error = %v, must not classify fatal iterator failure as partial", err)
			}
		})
	}
}

func listTestRecord(flowID, repo string, second int) FlowRecord {
	stamp := listTestTime(second)
	return FlowRecord{
		SchemaVersion: schemaVersion,
		FlowID:        flowID,
		Title:         flowID,
		Instructions:  "test",
		Status:        StatusPending,
		RepoPath:      repo,
		Phases:        []FlowPhase{},
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
}

func listTestTime(second int) time.Time {
	return time.Date(2026, 8, 14, 0, 0, second, 0, time.UTC)
}

func insertListTestRecord(t *testing.T, backend *sqliteBackend, record FlowRecord) {
	t.Helper()
	data, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatalf("encodeStoredFlow(%q) error = %v", record.FlowID, err)
	}
	insertListTestRaw(t, backend, projection.flowID, projection.repoPath, projection.status, record.UpdatedAt, data)
}

func insertListTestRaw(t *testing.T, backend *sqliteBackend, flowID, repo, status string, updatedAt time.Time, data []byte) {
	t.Helper()
	projectionTime, err := formatStorageTime(updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.db.Exec(`
INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)`,
		flowID, filepath.Clean(repo), status, projectionTime, data); err != nil {
		t.Fatalf("insert flow %q: %v", flowID, err)
	}
}

func flowIDs(records []FlowRecord) []string {
	ids := make([]string, len(records))
	for i := range records {
		ids[i] = records[i].FlowID
	}
	return ids
}

func partialFlowIDs(partial *PartialListError) []string {
	ids := make([]string, len(partial.Entries))
	for i := range partial.Entries {
		ids[i] = partial.Entries[i].FlowID
	}
	return ids
}

type fakeFlowListRow struct {
	flowID    string
	repoPath  string
	status    string
	updatedAt string
	record    []byte
	scanErr   error
}

type fakeFlowListRows struct {
	rows       []fakeFlowListRow
	next       int
	iterateErr error
	closeErr   error
}

func (r *fakeFlowListRows) Next() bool {
	if r.next >= len(r.rows) {
		return false
	}
	r.next++
	return true
}

func (r *fakeFlowListRows) Scan(dest ...any) error {
	row := r.rows[r.next-1]
	if row.scanErr != nil {
		return row.scanErr
	}
	*dest[0].(*string) = row.flowID
	*dest[1].(*string) = row.repoPath
	*dest[2].(*string) = row.status
	*dest[3].(*string) = row.updatedAt
	*dest[4].(*[]byte) = row.record
	return nil
}

func (r *fakeFlowListRows) Err() error   { return r.iterateErr }
func (r *fakeFlowListRows) Close() error { return r.closeErr }

func fakeListRowForRecord(t *testing.T, record FlowRecord) fakeFlowListRow {
	t.Helper()
	data, projection, err := encodeStoredFlow(record)
	if err != nil {
		t.Fatalf("encodeStoredFlow(%q) error = %v", record.FlowID, err)
	}
	return fakeFlowListRow{
		flowID:    projection.flowID,
		repoPath:  projection.repoPath,
		status:    projection.status,
		updatedAt: projection.updatedAt,
		record:    data,
	}
}

func listTestTimeString(t *testing.T, second int) string {
	t.Helper()
	value, err := formatStorageTime(listTestTime(second))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
