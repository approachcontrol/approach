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
	"strings"
	"syscall"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
)

const bootstrapLockFilename = ".approach.db.bootstrap.lock"

// migrationNoticeFilename records the cutover notice on disk. bootstrapWarnf
// writes to stderr, and the TUI takes the alternate screen within milliseconds
// of NewStore returning, so the stderr copy is realistically unreadable. The
// whole reversibility story of this design — "flows/ was left behind and you
// were told" — cannot rest on a message nobody sees.
const migrationNoticeFilename = "FLOW-MIGRATION-NOTICE.txt"

// bootstrapLockTimeout bounds the wait for the cutover lease. It is deliberately
// not the caller's per-operation lock timeout: migrating a large corpus can
// outlast the 5s default, and a second approach failing during a legitimate,
// progressing migration would look like a hang with no explanation.
const bootstrapLockTimeout = 2 * time.Minute

// bootstrapWarnf reports a non-fatal cutover notice. It exists because the
// migration deliberately leaves flows/ where it found it: a build without the
// SQLite backend keeps reading that directory, so the two views of the same
// state silently diverge and the operator has to be told. Tests override it.
var bootstrapWarnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "approach: "+format+"\n", args...)
}

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
		filepath.Join(canonicalRoot, bootstrapLockFilename),
		"flow database bootstrap lock (another approach process may be migrating this state root)",
		bootstrapLockTimeout,
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
			return nil, describeUnusableDatabase(canonicalRoot, err)
		}
		// A stage left behind by a crash that still managed to promote is
		// obsolete the moment approach.db exists. Drop it here: leaving it would
		// resurrect a stale corpus as the "interrupted" stage if the user ever
		// removed approach.db to start over.
		if err := discardObsoleteStage(canonicalRoot); err != nil {
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

// describeUnusableDatabase adds the recovery path to an authoritative-database
// failure. Leaving flows/ in place is the whole point of this cutover design,
// but an operator staring at "file is not a database" has no way to know their
// pre-migration records are still sitting next to the broken file.
func describeUnusableDatabase(root string, cause error) error {
	// A database from a newer build is not unusable, it is just ahead of this
	// binary. Attaching "re-migrate from flows/" to it would tell the operator to
	// roll a live corpus back to its migration-day snapshot, immediately after
	// correctly telling them to upgrade. Return it untouched.
	if errors.Is(cause, errDatabaseFromNewerBuild) {
		return cause
	}
	databasePath := filepath.Join(root, databaseFilename)
	legacyPath := filepath.Join(root, "flows")
	legacy, err := inspectReservedDirectory(legacyPath)
	if err != nil || !legacy {
		return fmt.Errorf("%w (move %q aside — keep it — and start approach again to rebuild an empty one)",
			cause, databasePath)
	}
	// This is NOT only reachable on an unreadable database. validateAuthoritative-
	// Database checks schema shape — columns and indexes — so a perfectly readable
	// database that is merely missing an index lands here with every post-migration
	// Flow intact and queryable. Re-migrating from flows/ would throw all of them
	// away. So the instruction gets the same bound as every other place it is
	// offered, and the message leads with the non-destructive option.
	return fmt.Errorf("%w (your pre-migration Flows are still in %q. Move %q aside — keep it, it holds"+
		" anything created since the migration, and may be repairable by hand. %s)",
		cause, legacyPath, databasePath, rollbackAdvice(databasePath, legacyPath))
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
		return fmt.Errorf("flow database cutover conflict: both flows and flows.legacy exist under %q; "+
			"keep whichever holds your Flows, move the other aside, and start approach again", root)
	}
	if !state.legacy && state.tombstone && !state.stage {
		// The tombstone is a byte-identical copy of the legacy source, so renaming
		// it back is lossless and lets the cutover run again from scratch.
		return fmt.Errorf("flow database cutover is incomplete: %q exists without a staged database; "+
			"your Flows are intact there — run `mv %q %q` and start approach again",
			tombstonePath, tombstonePath, legacyPath)
	}

	if state.stage {
		// Integrity and schema only — deliberately NOT a record-by-record diff
		// against the legacy source. The stage is fsynced before it is promoted,
		// so a validating stage is already known complete and durable;
		// re-deriving the expected set would add no safety and plenty of failure
		// surface, because canonicalizeLegacyFlow is preset-dependent. A user who
		// crashed mid-cutover and then edited a preset out of their config would
		// get a mismatch on every subsequent launch and could never start again.
		err := validateStagedDatabase(stagePath, nil, false)
		switch {
		case err == nil:
			if err := os.Rename(stagePath, databasePath); err != nil {
				return fmt.Errorf("promote interrupted staged flow database: %w", err)
			}
			if err := syncDirectory(root); err != nil {
				return fmt.Errorf("sync promoted flow database directory: %w", err)
			}
			reportResumedCutover(root, legacyPath, databasePath, state.legacy)
			return nil
		case !state.legacy && state.tombstone:
			// Only a root migrated by a build that renamed flows/ can reach this:
			// the stage is unusable and the tombstone is the sole surviving copy,
			// so refuse rather than rebuild from nothing.
			// Both steps, or the operator does the first and hits a second refusal:
			// removing the stage alone leaves a tombstone with no stage, which is
			// the "cutover is incomplete" branch above.
			return fmt.Errorf("validate interrupted staged flow database: %w"+
				" (the original records are still in %q. To redo the cutover from them, remove %q AND"+
				" run `mv %q %q`, then start approach again)",
				err, tombstonePath, stagePath, tombstonePath, legacyPath)
		default:
			// Either flows/ is still in place and authoritative, or there is no
			// legacy source at all. In both cases an unusable stage from an
			// interrupted run carries nothing that is not either still on disk or
			// already gone, so it is discarded and rebuilt below. Refusing here
			// instead would leave a fresh install that was interrupted during its
			// very first bootstrap permanently unable to start.
		}
	}

	if err := cleanupReservedSQLiteFiles(root); err != nil {
		return err
	}
	var imported legacyImport
	var err error
	if state.legacy {
		imported, err = readAndCanonicalizeLegacy(root, "flows", presets)
		if err != nil {
			return err
		}
	}
	records := imported.records
	if err := buildStagedDatabase(stagePath, records); err != nil {
		return err
	}
	if err := validateStagedDatabase(stagePath, records, true); err != nil {
		return err
	}
	// The legacy source is deliberately left where it is. Renaming it to
	// flows.legacy/ is invisible to this build but silently empties the Flow
	// list of any build still on the file store, including one launched by
	// accident against a shared state root. Coexistence plus a warning is the
	// reversible option while both backends are in play.
	if err := os.Rename(stagePath, databasePath); err != nil {
		return fmt.Errorf("promote flow database: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		return fmt.Errorf("sync flow database directory: %w", err)
	}
	if state.legacy {
		reportCutover(root, legacyPath, databasePath, imported)
	}
	return nil
}

// rollbackAdvice is the ONLY place "re-run the cutover" may be phrased.
//
// Re-running rebuilds approach.db from flows/, and flows/ is a snapshot frozen
// at migration time — nothing has written to it since. So the instruction is
// harmless in the minutes after a cutover, when the database holds nothing but
// what was just imported, and destroys every Flow created since at any later
// point. These strings are also written to a file that sits in the state root
// for months, which is exactly when they are most likely to be read and least
// likely to be safe.
//
// Centralizing it means no future paragraph can emit the instruction without
// the bound. TestEveryRollbackInstructionIsTimeBounded pins that.
func rollbackAdvice(databasePath, legacyPath string) string {
	return fmt.Sprintf("To re-run the cutover, move %q aside and start approach again; it will rebuild"+
		" from %q.\n"+
		"Do NOT do this later: %q is a snapshot frozen at migration time, not a live mirror — approach"+
		" has never written to it since — so this is safe ONLY while the database holds nothing beyond"+
		" what the migration imported. Once you have created or changed Flows here, re-running the"+
		" cutover discards every one of them.",
		databasePath, legacyPath, legacyPath)
}

// elideAfter bounds a list interpolated into a notice. These lists are written
// to stderr AND to a file, and a corpus with hundreds of stray directories would
// otherwise bury the instructions underneath them.
func elideAfter(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append(append([]string(nil), values[:limit]...),
		fmt.Sprintf("... and %d more", len(values)-limit))
}

// fenceRepairAdvice is the ONLY place the manual repair for a graph-fenced
// record may be phrased, for the same reason rollbackAdvice is centralized: the
// fence keys off the graph_recovery marker, not off the edges, so a recipe that
// names only the depends_on edit reads as complete and leaves the Flow blocked.
// Both edits, every time.
func fenceRepairAdvice(databasePath string) string {
	return fmt.Sprintf("Recreate those Flows, or repair each one directly in %q: set depends_on on its"+
		" phases AND delete the %q marker from its stored record — the fence keys off that marker, not"+
		" off the edges, so editing depends_on alone leaves the Flow blocked (see docs/config.md).",
		databasePath, GraphRecoveryMissingEdgesUnresolved)
}

// reportResumedCutover reports a promotion of a stage this process did not
// build. There is no import accounting to carry over, but the two facts that
// matter are still recoverable from the promoted database itself, and both are
// only actionable now: a crashed first migration that left Flows fenced would
// otherwise tell nobody, and a record added to flows/ after the stage was built
// (by falling back to an older file-store build) is dropped by the promotion.
func reportResumedCutover(root, legacyPath, databasePath string, legacyPresent bool) {
	promoted, unresolved, err := summarizePromotedDatabase(databasePath)
	if err == nil && !legacyPresent && len(unresolved) == 0 {
		// A fresh install whose very first bootstrap crashed after staging has no
		// legacy source and nothing fenced. Writing "promoted a staged Flow
		// database" into the state root would file an incident report for a
		// non-event on an install that never had any Flows.
		return
	}

	notice := fmt.Sprintf("promoted a staged Flow database into %q.", databasePath)
	switch {
	case err != nil:
		notice += fmt.Sprintf("\n\nCould not summarize the promoted records (%v);"+
			" check the Flow list yourself.", err)
	default:
		// Identity, not counts. A directory the migration would legitimately skip
		// — no meta.json, unreadable JSON — is not a dropped record, and raising a
		// false alarm next to a rollback instruction is how an operator ends up
		// discarding a database for no reason.
		if legacyPresent {
			missing, missingErr := legacyFlowIDsMissingFrom(legacyPath, promoted)
			switch {
			case missingErr != nil:
				notice += fmt.Sprintf("\n\nCould not compare the promoted records against %q (%v);"+
					" check the Flow list yourself.", legacyPath, missingErr)
			case len(missing) > 0:
				notice += fmt.Sprintf("\n\nThese Flow(s) are in %q but not in the promoted database:\n  - %s\n"+
					"The stage is promoted as built and deliberately not re-derived, so anything added to %q"+
					" after the interrupted run staged it was not imported.\n%s",
					legacyPath, strings.Join(elideAfter(missing, 20), "\n  - "), legacyPath,
					rollbackAdvice(databasePath, legacyPath))
			}
			// Matching by id cannot see an EDIT: a record whose meta.json was
			// rewritten after staging keeps its id, so it looks present. Say so
			// rather than let the list above read as the complete story.
			notice += fmt.Sprintf("\n\nThe resume promotes the stage exactly as it was built. Any Flow in"+
				" %q that was CHANGED after that stage was created also kept its pre-change form here.",
				legacyPath)
		}
		// Deliberately NOT gated on the legacy source still existing: this comes
		// from the promoted database, and an operator who already removed flows/
		// is the one most likely to never hear about it otherwise.
		if len(unresolved) > 0 {
			notice += fmt.Sprintf("\n\n%d Flow(s) were migrated with an unresolved phase graph, so phase"+
				" mutation on them is blocked:\n  - %s\n"+
				"Preset edge recovery runs only during migration.", len(unresolved),
				strings.Join(elideAfter(unresolved, 20), "\n  - "))
			if legacyPresent {
				notice += "\nRestore those presets first, then: " + rollbackAdvice(databasePath, legacyPath)
			} else {
				notice += fmt.Sprintf("\n%q is gone, so the cutover cannot be re-run. %s",
					legacyPath, fenceRepairAdvice(databasePath))
			}
		}
	}

	if legacyPresent {
		notice += fmt.Sprintf("\n\n%q was left in place and is no longer read."+
			" Builds without the SQLite flow backend still read it and will not see changes made here."+
			" Remove it yourself once every build you run is on the SQLite backend.", legacyPath)
	}

	bootstrapWarnf("%s", notice)
	writeMigrationNotice(root, notice)
}

// summarizePromotedDatabase reads back what was promoted. It deliberately does
// not re-canonicalize anything — that would reintroduce the preset dependence
// the resume path exists to avoid (see completeCutover).
func summarizePromotedDatabase(path string) (map[string]bool, []string, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT flow_id, repo_path, status, updated_at, record FROM flows ORDER BY flow_id")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	promoted := map[string]bool{}
	var unresolved []string
	for rows.Next() {
		var flowID, repoPath, status, updatedAt string
		var data []byte
		if err := rows.Scan(&flowID, &repoPath, &status, &updatedAt, &data); err != nil {
			return nil, nil, err
		}
		promoted[flowID] = true
		stored, err := decodeStoredFlow(flowID, repoPath, status, updatedAt, data)
		if err != nil {
			return nil, nil, err
		}
		if preset := fencingPresetName(stored.record); preset != "" {
			unresolved = append(unresolved, fmt.Sprintf("%s: preset %q", stored.record.FlowID, preset))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return promoted, unresolved, nil
}

// legacyFlowIDsMissingFrom names the legacy records the promotion did not
// import. It matches readAndCanonicalizeLegacy's candidate predicate AND its
// skip rules: a directory with no meta.json, or one whose meta.json the
// migration would refuse to decode, was never going to be imported and is not
// a dropped record. Reporting those as losses would raise a false alarm next
// to a rollback instruction, which is how an operator discards a database for
// nothing.
func legacyFlowIDsMissingFrom(dir string, promoted map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() || validateFlowID(entry.Name()) != nil || promoted[entry.Name()] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if err != nil {
			// A missing meta.json was never a record. Anything else — EACCES, EIO —
			// is environmental, and this is the one moment it can be reported: the
			// fresh path treats the same condition as fatal precisely because the
			// loss becomes permanent. Silently skipping it here would be the
			// opposite stance on identical evidence.
			if os.IsNotExist(err) {
				continue
			}
			missing = append(missing, fmt.Sprintf("%s (unreadable: %v)", entry.Name(), err))
			continue
		}
		if _, ok := decodeLegacyFlow(entry.Name(), data); !ok {
			continue
		}
		missing = append(missing, entry.Name())
	}
	return missing, nil
}

// reportCutover tells the operator what the one-way migration actually did.
// Every line here describes something that is only actionable now: once
// approach.db exists the legacy tree is never read again, so a record that was
// skipped or left graph-unresolved stays that way until the operator removes
// the database and re-runs the cutover.
func reportCutover(root, legacyPath, databasePath string, imported legacyImport) {
	notice := fmt.Sprintf("migrated %d of %d Flow records from %q into %q.",
		len(imported.records), imported.found, legacyPath, databasePath)
	if len(imported.skipped) > 0 {
		notice += fmt.Sprintf("\n\nSkipped %d unreadable record(s); their files are untouched under %q:\n  - %s",
			len(imported.skipped), legacyPath, strings.Join(elideAfter(imported.skipped, 20), "\n  - "))
	}
	if len(imported.unresolved) > 0 {
		// Edge recovery is preset-dependent and now runs exactly once, so unlike
		// the file store this does NOT heal on a later launch with the preset
		// restored. Re-running the cutover is the only real fix, and it is safe
		// ONLY right now, while the database holds nothing but what was just
		// migrated. Say both halves — this text also lands in a file that will
		// still be sitting in the state root months from now.
		notice += fmt.Sprintf("\n\n%d Flow(s) reference a preset that is not in your config, so their phase"+
			" dependencies could not be restored and phase mutation on them is blocked:\n  - %s\n"+
			"Preset edge recovery runs only during this migration. Restore those presets first, then: %s",
			len(imported.unresolved), strings.Join(elideAfter(imported.unresolved, 20), "\n  - "),
			rollbackAdvice(databasePath, legacyPath))
	}
	notice += fmt.Sprintf("\n\n%q was left in place and is no longer read."+
		" Builds without the SQLite flow backend still read it and will not see changes made here."+
		" Remove it yourself once every build you run is on the SQLite backend.", legacyPath)

	bootstrapWarnf("%s", notice)
	writeMigrationNotice(root, notice)
}

// writeMigrationNotice persists the cutover report next to the database. Best
// effort on purpose: failing to write an advisory file must never fail a
// migration that already succeeded and is already durable.
func writeMigrationNotice(root, notice string) {
	path := filepath.Join(root, migrationNoticeFilename)
	body := "approach Flow storage migration\n\n" + notice + "\n\nThis file is advisory. Delete it once you have read it.\n"
	// O_NOFOLLOW like every other write in this file: os.WriteFile would happily
	// follow a symlink planted at the notice path and write through it. The 0700
	// root makes that hard to reach, but nothing here should be the one primitive
	// that trusts its final path component.
	fd, err := syscall.Open(path,
		syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		uint32(artifacts.FilePerm))
	if err != nil {
		return
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(body); err != nil {
		return
	}
	_ = file.Chmod(artifacts.FilePerm)
}

// discardObsoleteStage drops a stage that approach.db has already made
// irrelevant. It runs on EVERY healthy startup, so it must not be able to
// refuse one: a stray directory or symlink at a stage path — a botched rsync, a
// backup tool, a curious user — is not a stage worth promoting and is left
// alone rather than turned into a startup failure. inspectReservedRegular's
// strictness belongs on the promotion path, where the file's contents become
// authoritative, not on a best-effort cleanup.
func discardObsoleteStage(root string) error {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		path := filepath.Join(root, stageFilename) + suffix
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect obsolete staged flow database %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove obsolete staged flow database %q: %w", path, err)
		}
	}
	return nil
}

// cleanupReservedSQLiteFiles clears the reserved SQLite paths before a fresh
// stage is built. It removes approach.db itself, so it MUST NOT be called on a
// root that already has an authoritative database — the guard below turns a
// future misuse into an error instead of silent data loss.
func cleanupReservedSQLiteFiles(root string) error {
	authoritative, err := inspectReservedRegular(filepath.Join(root, databaseFilename))
	if err != nil {
		return err
	}
	if authoritative {
		return fmt.Errorf("refusing to clear reserved SQLite artifacts: %q already exists",
			filepath.Join(root, databaseFilename))
	}
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

// legacyImport is the accounting for one cutover pass. found counts candidate
// record directories so a migration that imports 0 of 400 cannot look identical
// to one that imports 400 of 400.
type legacyImport struct {
	records    []FlowRecord
	found      int
	skipped    []string
	unresolved []string
}

func readAndCanonicalizeLegacy(root, collection string, presets map[string]Preset) (legacyImport, error) {
	dir := filepath.Join(root, collection)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return legacyImport{}, fmt.Errorf("read legacy flow source: %w", err)
	}
	var imported legacyImport
	for _, entry := range entries {
		if !entry.IsDir() || validateFlowID(entry.Name()) != nil {
			continue
		}
		imported.found++
		metaPath := filepath.Join(dir, entry.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			// A missing meta.json is a legitimately empty directory and was
			// invisible under the file store too. Anything else — EACCES from a
			// botched chown, EIO on a network state root — is environmental, and
			// skipping it would migrate a truncated corpus and then make the loss
			// permanent, because the legacy tree is never read again once
			// approach.db exists. Fail instead so the operator can fix the cause
			// and retry with the legacy source still authoritative.
			if os.IsNotExist(err) {
				imported.skipped = append(imported.skipped,
					fmt.Sprintf("%s: no meta.json", entry.Name()))
				continue
			}
			return legacyImport{}, fmt.Errorf("read legacy flow %q: %w (migration aborted so no Flow is lost; "+
				"fix the file's permissions or availability and start approach again)", entry.Name(), err)
		}
		stored, ok := decodeLegacyFlow(entry.Name(), data)
		if !ok {
			// Undecodable content was already invisible under the file store, so
			// dropping it changes nothing today and the bytes stay under flows/.
			// It does become permanent, though: repairing the JSON used to bring
			// the Flow back on the next launch and no longer does. That is what
			// makes reporting it here the last useful moment.
			imported.skipped = append(imported.skipped,
				fmt.Sprintf("%s: unreadable meta.json (invalid JSON, mismatched flow_id, or unsupported schema_version)",
					entry.Name()))
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
		if record.GraphRecovery.Status == GraphRecoveryMissingEdgesUnresolved && mutationFencedByPreset(record) {
			imported.unresolved = append(imported.unresolved,
				fmt.Sprintf("%s: preset %q", record.FlowID, normalizePresetName(record.PresetName)))
		}
		if _, _, err := encodeStoredFlow(record); err != nil {
			return legacyImport{}, fmt.Errorf("canonicalize legacy flow %q: %w (its stored timestamps are"+
				" outside the range this build can persist; fix it, or move %q aside — never delete it, it is"+
				" the only copy of that Flow — and start approach again)",
				record.FlowID, err, filepath.Join(dir, entry.Name(), "meta.json"))
		}
		imported.records = append(imported.records, record)
	}
	return imported, nil
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

// validateStagedDatabase checks the staged file's schema and integrity, and
// proves the read-only pass did not mutate it. When compareRecords is set it
// additionally diffs every row against expected; the fresh-build path wants
// that, the crash-resume path deliberately does not (see completeCutover).
func validateStagedDatabase(path string, expected []FlowRecord, compareRecords bool) error {
	before, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	// ORDER IS LOAD-BEARING: this sidecar check must stay ahead of the
	// immutable=1 open below. immutable tells SQLite the file cannot change, so
	// it skips hot-journal recovery entirely — a stage carrying a -journal from
	// an interrupted transaction would then validate as "ok" while its pages are
	// half-committed. Checking first is what makes the immutable open safe.
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
	if !compareRecords {
		after, err := fileFingerprint(path)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("read-only staged database validation mutated %q", path)
		}
		return requireNoSQLiteSidecars(path)
	}
	// Sort a copy: expected is the caller's live record slice, and a validation
	// step has no business reordering it.
	expected = append([]FlowRecord(nil), expected...)
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
