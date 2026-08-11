package flowstore

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const bootstrapLockFilename = ".approach.db.bootstrap.lock"

type cutoverState struct {
	database  bool
	legacy    bool
	tombstone bool
	stage     bool
}

func newSQLiteStoreBackend(root string, lockTimeout time.Duration, configuredPresets []Preset) (*sqliteBackend, error) {
	canonicalRoot, err := secureCanonicalRoot(root)
	if err != nil {
		return nil, err
	}
	release, err := artifacts.AcquireFileLockNoFollow(
		filepath.Join(canonicalRoot, bootstrapLockFilename), "flow database bootstrap lock", lockTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("bootstrap flow database: %w", err)
	}
	defer release()

	state, err := inspectCutoverState(canonicalRoot)
	if err != nil {
		return nil, err
	}
	if state.database {
		if err := validateAuthoritativeDatabase(filepath.Join(canonicalRoot, databaseFilename)); err != nil {
			return nil, err
		}
		backend, err := openSQLiteBackend(canonicalRoot, lockTimeout)
		if err != nil {
			return nil, err
		}
		if _, err := presetRegistry(configuredPresets); err != nil {
			_ = backend.db.Close()
			return nil, err
		}
		return backend, nil
	}

	presets, err := presetRegistry(configuredPresets)
	if err != nil {
		return nil, err
	}
	if err := completeCutover(canonicalRoot, state, presets); err != nil {
		return nil, err
	}
	backend, err := openSQLiteBackend(canonicalRoot, lockTimeout)
	if err != nil {
		return nil, err
	}
	return backend, nil
}

func secureCanonicalRoot(root string) (string, error) {
	if err := os.MkdirAll(root, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("create flow store root: %w", err)
	}
	if err := os.Chmod(root, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("secure flow store root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve flow store root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect flow store root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("flow store root must resolve to a directory")
	}
	if err := os.Chmod(canonical, artifacts.DirPerm); err != nil {
		return "", fmt.Errorf("secure resolved flow store root: %w", err)
	}
	info, err = os.Stat(canonical)
	if err != nil || info.Mode().Perm() != artifacts.DirPerm {
		return "", fmt.Errorf("flow store root permissions are not 0700")
	}
	return canonical, nil
}

func inspectCutoverState(root string) (cutoverState, error) {
	database, err := inspectReservedRegular(filepath.Join(root, databaseFilename))
	if err != nil {
		return cutoverState{}, err
	}
	if database {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if _, err := inspectReservedRegular(filepath.Join(root, databaseFilename) + suffix); err != nil {
				return cutoverState{}, err
			}
		}
		return cutoverState{database: true}, nil
	}

	legacy, err := inspectReservedDirectory(filepath.Join(root, "flows"))
	if err != nil {
		return cutoverState{}, err
	}
	tombstone, err := inspectReservedDirectory(filepath.Join(root, "flows.legacy"))
	if err != nil {
		return cutoverState{}, err
	}
	stage, err := inspectReservedRegular(filepath.Join(root, stageFilename))
	if err != nil {
		return cutoverState{}, err
	}
	for _, base := range []string{databaseFilename, stageFilename} {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if _, err := inspectReservedRegular(filepath.Join(root, base) + suffix); err != nil {
				return cutoverState{}, err
			}
		}
	}
	return cutoverState{legacy: legacy, tombstone: tombstone, stage: stage}, nil
}

func inspectReservedRegular(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect reserved path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("reserved path %q must be a real regular file", path)
	}
	return true, nil
}

func inspectReservedDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect reserved path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("reserved path %q must be a real directory", path)
	}
	return true, nil
}

func completeCutover(root string, state cutoverState, presets map[string]Preset) error {
	stagePath := filepath.Join(root, stageFilename)
	legacyPath := filepath.Join(root, "flows")
	tombstonePath := filepath.Join(root, "flows.legacy")
	databasePath := filepath.Join(root, databaseFilename)

	if state.legacy && state.tombstone {
		return fmt.Errorf("flow database cutover conflict: both flows and flows.legacy exist")
	}
	if !state.legacy && state.tombstone && !state.stage {
		return fmt.Errorf("flow database cutover is incomplete: flows.legacy exists without a staged database")
	}

	if !state.legacy && state.stage {
		var expected []FlowRecord
		var err error
		if state.tombstone {
			expected, err = readAndCanonicalizeLegacy(root, "flows.legacy", presets)
			if err != nil {
				return err
			}
		}
		if err := validateStagedDatabase(stagePath, expected); err != nil {
			return fmt.Errorf("validate interrupted staged flow database: %w", err)
		}
		if err := os.Rename(stagePath, databasePath); err != nil {
			return fmt.Errorf("promote interrupted staged flow database: %w", err)
		}
		if err := syncDirectory(root); err != nil {
			return fmt.Errorf("sync promoted flow database directory: %w", err)
		}
		return nil
	}

	if err := cleanupReservedSQLiteFiles(root); err != nil {
		return err
	}
	var records []FlowRecord
	var err error
	if state.legacy {
		records, err = readAndCanonicalizeLegacy(root, "flows", presets)
		if err != nil {
			return err
		}
	}
	if err := buildStagedDatabase(stagePath, records); err != nil {
		return err
	}
	if err := validateStagedDatabase(stagePath, records); err != nil {
		return err
	}
	if !state.legacy {
		if err := os.Rename(stagePath, databasePath); err != nil {
			return fmt.Errorf("promote fresh flow database: %w", err)
		}
		if err := syncDirectory(root); err != nil {
			return fmt.Errorf("sync fresh flow database directory: %w", err)
		}
		return nil
	}

	if err := os.Rename(legacyPath, tombstonePath); err != nil {
		return fmt.Errorf("rename legacy flow source: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		return rollbackLegacyRename(root, legacyPath, tombstonePath, fmt.Errorf("sync legacy flow tombstone: %w", err))
	}
	if err := os.Rename(stagePath, databasePath); err != nil {
		return rollbackLegacyRename(root, legacyPath, tombstonePath, fmt.Errorf("promote migrated flow database: %w", err))
	}
	if err := syncDirectory(root); err != nil {
		return fmt.Errorf("sync migrated flow database directory: %w", err)
	}
	return nil
}

func rollbackLegacyRename(root, legacyPath, tombstonePath string, original error) error {
	if err := os.Rename(tombstonePath, legacyPath); err != nil {
		return fmt.Errorf("%v; additionally failed to restore legacy source: %w", original, err)
	}
	if err := syncDirectory(root); err != nil {
		return fmt.Errorf("%v; restored legacy source but directory sync failed: %w", original, err)
	}
	return original
}

func cleanupReservedSQLiteFiles(root string) error {
	for _, base := range []string{databaseFilename, stageFilename} {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			path := filepath.Join(root, base) + suffix
			exists, err := inspectReservedRegular(path)
			if err != nil {
				return err
			}
			if exists {
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("remove reserved SQLite artifact %q: %w", path, err)
				}
			}
		}
	}
	return nil
}

func readAndCanonicalizeLegacy(root, collection string, presets map[string]Preset) ([]FlowRecord, error) {
	dir := filepath.Join(root, collection)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read legacy flow source: %w", err)
	}
	var records []FlowRecord
	for _, entry := range entries {
		if !entry.IsDir() || validateFlowID(entry.Name()) != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if err != nil {
			continue
		}
		stored, ok := decodeLegacyFlow(entry.Name(), data)
		if !ok {
			continue
		}
		record := canonicalizeLegacyFlow(stored, presets)
		record.Phases = collapseAllDuplicatePhaseRows(record.Phases)
		if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved {
			record = normalizeRecordBase(record)
			record = normalizeReviewOutcomes(record)
		} else {
			record = normalizeRecord(record, true)
		}
		record.Status = DeriveStatus(record)
		if record.GraphRecovery.Status == GraphRecoveryPresetEdgesRestored {
			record.GraphRecovery.Status = ""
		}
		if _, _, err := encodeStoredFlow(record); err != nil {
			return nil, fmt.Errorf("canonicalize legacy flow %q: %w", record.FlowID, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func decodeLegacyFlow(flowID string, data []byte) (legacyStoredFlow, bool) {
	presence := rawDependsOnPresence(data)
	headlessPresent := rawFieldPresent(data, "headless")
	var record FlowRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return legacyStoredFlow{}, false
	}
	if record.FlowID != flowID || record.SchemaVersion != schemaVersion {
		return legacyStoredFlow{}, false
	}
	return legacyStoredFlow{record: record, dependsOnPresence: presence, headlessPresent: headlessPresent}, true
}

func collapseAllDuplicatePhaseRows(phases []FlowPhase) []FlowPhase {
	seen := map[string]bool{}
	for i := 0; i < len(phases); i++ {
		id := artifacts.NormalizePhaseID(phases[i].PhaseID)
		if seen[id] {
			continue
		}
		seen[id] = true
		phases = collapseDuplicatePhaseRows(phases, i)
	}
	return phases
}

func buildStagedDatabase(path string, records []FlowRecord) error {
	file, err := openExclusiveRegular(path)
	if err != nil {
		return fmt.Errorf("create staged flow database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged flow database placeholder: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"rw"}}))
	if err != nil {
		return fmt.Errorf("open staged flow database: %w", err)
	}
	closeDB := true
	defer func() {
		if closeDB {
			_ = db.Close()
		}
	}()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		return fmt.Errorf("configure staged flow database rollback journal: mode=%q err=%w", mode, err)
	}
	if err := initializeSQLiteSchema(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin legacy flow import: %w", err)
	}
	for _, record := range records {
		data, projection, err := encodeStoredFlow(record)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.Exec("INSERT INTO flows(flow_id, repo_path, status, updated_at, record) VALUES(?, ?, ?, ?, ?)",
			projection.flowID, projection.repoPath, projection.status, projection.updatedAt, data); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("import legacy flow %q: %w", record.FlowID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy flow import: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close staged flow database: %w", err)
	}
	closeDB = false
	if err := os.Chmod(path, artifacts.FilePerm); err != nil {
		return fmt.Errorf("secure staged flow database: %w", err)
	}
	file, err = os.Open(path)
	if err != nil {
		return fmt.Errorf("open staged flow database for sync: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync staged flow database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged flow database after sync: %w", err)
	}
	return nil
}

func openExclusiveRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(artifacts.FilePerm))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(artifacts.FilePerm); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validateStagedDatabase(path string, expected []FlowRecord) error {
	before, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	if err := requireNoSQLiteSidecars(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}, "immutable": {"1"}}))
	if err != nil {
		return err
	}
	if err := validateSQLiteSchema(db); err != nil {
		_ = db.Close()
		return err
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		_ = db.Close()
		return fmt.Errorf("staged flow database integrity check = %q, err=%v", integrity, err)
	}
	rows, err := db.Query("SELECT flow_id, repo_path, status, updated_at, record FROM flows ORDER BY flow_id")
	if err != nil {
		_ = db.Close()
		return err
	}
	var actual []FlowRecord
	for rows.Next() {
		var flowID, repoPath, status, updatedAt string
		var data []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &data); err != nil {
			_ = rows.Close()
			_ = db.Close()
			return err
		}
		stored, err := decodeStoredFlow(flowID, repoPath, status, updatedAt, data)
		if err != nil {
			_ = rows.Close()
			_ = db.Close()
			return err
		}
		actual = append(actual, stored.record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = db.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].FlowID < expected[j].FlowID })
	if len(actual) != len(expected) {
		return fmt.Errorf("staged flow count = %d, want %d", len(actual), len(expected))
	}
	for i := range expected {
		wantData, wantProjection, err := encodeStoredFlow(expected[i])
		if err != nil {
			return err
		}
		gotData, gotProjection, err := encodeStoredFlow(actual[i])
		if err != nil {
			return err
		}
		if wantProjection != gotProjection || !bytes.Equal(wantData, gotData) {
			return fmt.Errorf("staged flow %q does not match canonical legacy record", expected[i].FlowID)
		}
	}
	after, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("read-only staged database validation mutated %q", path)
	}
	return requireNoSQLiteSidecars(path)
}

type fingerprint struct {
	hash    [sha256.Size]byte
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func fileFingerprint(path string) (fingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fingerprint{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fingerprint{}, err
	}
	return fingerprint{hash: sha256.Sum256(data), size: info.Size(), mode: info.Mode(), modTime: info.ModTime()}, nil
}

func requireNoSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("staged flow database unexpectedly has sidecar %q", path+suffix)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func validateAuthoritativeDatabase(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		return fmt.Errorf("open authoritative flow database for validation: %w", err)
	}
	defer db.Close()
	if err := validateSQLiteSchema(db); err != nil {
		return fmt.Errorf("validate authoritative flow database: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil && runtime.GOOS == "windows" && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)) {
		return nil
	}
	return err
}
