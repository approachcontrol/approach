package flowstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/approachcontrol/approach/internal/artifacts"
	"github.com/approachcontrol/approach/internal/version"
)

// sidecarFilename holds the migration provenance beside the database.
//
// A file, never a table: validateSQLiteSchemaObjects compares the schema object
// set for exact equality, so a bookkeeping table would make every database this
// build touched unreadable by the previous one.
const sidecarFilename = "approach.db.meta.json"

// sidecarSchemaVersion versions the sidecar's own JSON, independently of the
// database schema it describes.
//
// 2 adds the append-only history array. The bump is safe in exactly one
// direction, and only because of how the predecessor behaves: a build that
// knows only version 1 fails validSidecar on a v2 sidecar and reports it
// ABSENT, and reconcileSidecar's !present branch returns without rewriting, so
// an older build neither believes nor destroys the newer record. That property
// is pinned by TestAV2SidecarReadsAsAbsentThroughTheV1ValidationPath rather
// than assumed.
const sidecarSchemaVersion = 2

// sidecarHistoryLimit caps the append-only history. A long-lived root migrated
// on every release would otherwise grow this file without bound, and the
// entries an operator needs are always the newest ones.
const sidecarHistoryLimit = 100

// generationIDBytes is the width of a generation ID before hex encoding. It is
// named because validSidecar checks the encoded length against it, and the
// timestamp fallback in newGenerationID has to keep matching.
const generationIDBytes = 8

const (
	sidecarProvenanceMigrated = "migrated"
	// sidecarProvenanceReconstructed marks a sidecar rebuilt from user_version
	// after the two disagreed. user_version is authoritative in that comparison,
	// always, and the disagreement itself never feeds a compatibility decision.
	sidecarProvenanceReconstructed = "reconstructed"
)

type sidecarMigrator struct {
	BuildVersion string `json:"build_version"`
	Commit       string `json:"commit"`
	At           string `json:"at"`
}

// sidecarHistoryEntry is one migration or repair, recorded append-only.
//
// FromVersion and BackupPath are pointers because a reconstruction genuinely
// does not know them: the sidecar was rebuilt from user_version after a crash
// between commit and sidecar write, and the version it came FROM is gone. Null
// says that; a zero would claim the database was migrated from the unstamped
// original.
type sidecarHistoryEntry struct {
	At           string  `json:"at"`
	BuildVersion string  `json:"build_version,omitempty"`
	Commit       string  `json:"commit,omitempty"`
	FromVersion  *int64  `json:"from_version"`
	ToVersion    int64   `json:"to_version"`
	BackupPath   *string `json:"backup_path"`
	GenerationID string  `json:"generation_id"`
	Provenance   string  `json:"provenance"`
}

type databaseSidecar struct {
	SchemaVersion int64 `json:"schema_version"`
	// GenerationID identifies one migration event. It names backup files so a
	// backup can be traced to the migration that produced it.
	GenerationID string `json:"generation_id"`
	// PhysicalVersion mirrors user_version, which stays authoritative. This is
	// a cache, and a cache that disagrees is repaired, not believed.
	PhysicalVersion int64 `json:"physical_version"`
	// MinReaderGeneration and MinWriterGeneration are what a compatibility
	// refusal would name once a manifest maps generations to releases.
	MinReaderGeneration int64           `json:"min_reader_generation"`
	MinWriterGeneration int64           `json:"min_writer_generation"`
	MigratedBy          sidecarMigrator `json:"migrated_by"`
	Provenance          string          `json:"provenance"`
	// History is every migration and repair this root has recorded, oldest
	// first. Append-only: an entry is never edited, only trimmed off the front
	// once the cap is reached.
	History []sidecarHistoryEntry `json:"history,omitempty"`
	// HistoryTruncated reports that older entries were trimmed, so a reader
	// never mistakes a capped history for a complete one.
	HistoryTruncated bool `json:"history_truncated,omitempty"`
}

func sidecarPath(root string) string {
	return filepath.Join(root, sidecarFilename)
}

// readSidecar returns the sidecar and whether one exists. A missing sidecar is
// a normal state, not an error: creation writes none, so every root that has
// never migrated has none either. An unreadable or malformed one is reported as
// absent for the same reason a disagreeing one is never believed — it cannot be
// allowed to drive a decision.
func readSidecar(root string) (databaseSidecar, bool) {
	data, err := os.ReadFile(sidecarPath(root))
	if err != nil {
		return databaseSidecar{}, false
	}
	var sidecar databaseSidecar
	if err := json.Unmarshal(data, &sidecar); err != nil {
		return databaseSidecar{}, false
	}
	// Parsing as JSON is not the same as being a sidecar. Absent is the safe
	// reading of anything this build cannot interpret, and it is what the doc
	// above promises — without this, "malformed is reported as absent" covered
	// only syntax.
	if !validSidecar(sidecar) {
		return databaseSidecar{}, false
	}
	return sidecar, true
}

// validSidecar rejects a sidecar whose fields this build cannot use.
//
// The generation ID is the load-bearing one: sidecarGenerationOrNoGen puts it
// straight into a backup FILENAME, so a value carrying a separator makes every
// later migration build a destination under a directory that does not exist and
// fail at the rename — reported as a backup failure suggesting the disk is
// full. That is a hand-edited or externally damaged sidecar permanently
// blocking the upgrade of a perfectly healthy database, which is precisely
// backwards for a file the package documents as a cache that is never believed.
//
// newGenerationID emits exactly 16 lowercase hex characters, so that is the
// whole grammar. Empty is allowed: callers already treat it as "no generation".
func validSidecar(sidecar databaseSidecar) bool {
	return validSidecarForVersion(sidecar, sidecarSchemaVersion)
}

// validSidecarForVersion is validSidecar with the accepted version made
// explicit, so a test can restate the PREDECESSOR's rule and prove an older
// build reports a v2 sidecar as absent rather than acting on it.
//
// This build accepts its own version and every earlier one it can still read:
// a v1 sidecar is a v2 sidecar without a history, and treating one as absent
// would drop the generation_id that names every backup already on disk.
func validSidecarForVersion(sidecar databaseSidecar, accepted int64) bool {
	if sidecar.SchemaVersion < 1 || sidecar.SchemaVersion > accepted {
		return false
	}
	if sidecar.GenerationID == "" {
		return true
	}
	if len(sidecar.GenerationID) != 2*generationIDBytes {
		return false
	}
	_, err := hex.DecodeString(sidecar.GenerationID)
	return err == nil
}

// sidecarDisagrees reports a sidecar whose physical version is not the one
// stamped in the database. Absent is not stale: there is nothing to disagree.
func sidecarDisagrees(root string, storedVersion int64) bool {
	sidecar, ok := readSidecar(root)
	return ok && sidecar.PhysicalVersion != storedVersion
}

// reconcileSidecar writes or repairs the sidecar. Only a RoleMigrator calls it,
// and only while the bootstrap lease is held: repairing is a write to the root
// under a lock, and this unit's premise is that one role owns those.
//
// One call site covers both cases because they are the same decision made on
// different evidence — a migration just ran, or the database was already
// current and the cached copy drifted. The repair case is unreachable from
// inside migrateAuthoritativeDatabase, whose early return fires first, which is
// why this sits outside it.
func reconcileSidecar(root string, storedVersion int64, outcome migrationOutcome) error {
	existing, present := readSidecar(root)
	switch {
	case outcome.Migrated:
		return writeSidecar(root, storedVersion, sidecarProvenanceMigrated, newGenerationID(), outcome)
	case !present:
		// Creation writes no sidecar, so a never-migrated root legitimately has
		// none. Inventing one here would claim a migration that never happened.
		return nil
	case existing.PhysicalVersion == storedVersion:
		// Already agreed. An upgrade of the sidecar's OWN version is deliberately
		// not forced here: rewriting a v1 sidecar that says the right thing would
		// make every open of a shared root a write, and the next real migration
		// carries the upgrade for free.
		return nil
	default:
		// user_version moved and the sidecar did not: a crash between the
		// migration's commit and its sidecar write. The generation is kept
		// because it names the backups already on disk.
		generation := existing.GenerationID
		if generation == "" {
			generation = newGenerationID()
		}
		return writeSidecar(root, storedVersion, sidecarProvenanceReconstructed, generation,
			migrationOutcome{Reconstructed: true})
	}
}

func writeSidecar(root string, storedVersion int64, provenance, generation string, outcome migrationOutcome) error {
	// Declared, not assumed. The two floors were hardcoded to storedVersion
	// while every schema shipped so far had them equal; taking them from the
	// manifest is behaviour-preserving today and is what makes the NEXT bump
	// able to say "this version is readable by an older build" without editing
	// this function.
	minReader, minWriter := manifestGenerations(storedVersion)
	at := time.Now().UTC().Format(time.RFC3339)
	sidecar := databaseSidecar{
		SchemaVersion:       sidecarSchemaVersion,
		GenerationID:        generation,
		PhysicalVersion:     storedVersion,
		MinReaderGeneration: minReader,
		MinWriterGeneration: minWriter,
		MigratedBy: sidecarMigrator{
			BuildVersion: version.Version(),
			Commit:       version.Commit(),
			At:           at,
		},
		Provenance: provenance,
	}
	// Read through the EXISTING sidecar's history rather than starting fresh:
	// this file is the only durable record of what has been done to this
	// database, and a rewrite that dropped it would erase the provenance the
	// backups depend on.
	existing, _ := readSidecar(root)
	history := append(existing.History, sidecarHistoryEntry{
		At:           at,
		BuildVersion: version.Version(),
		Commit:       version.Commit(),
		FromVersion:  outcome.fromVersionOrNil(),
		ToVersion:    storedVersion,
		BackupPath:   outcome.backupPathOrNil(),
		GenerationID: generation,
		Provenance:   provenance,
	})
	trimmed, truncated := trimSidecarHistory(history)
	sidecar.History = trimmed
	sidecar.HistoryTruncated = existing.HistoryTruncated || truncated
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return fmt.Errorf("encode flow database sidecar: %w", err)
	}
	// Atomic AND durable: temp file, fsync, rename, fsync the directory. A torn
	// sidecar would read as absent, which is safe, but a rename keeps even that
	// from being observable.
	if err := writeSidecarDurably(root, append(data, '\n')); err != nil {
		return fmt.Errorf("write flow database sidecar: %w", err)
	}
	return nil
}

// trimSidecarHistory caps the history at sidecarHistoryLimit, oldest first out,
// and reports whether anything was dropped.
func trimSidecarHistory(history []sidecarHistoryEntry) ([]sidecarHistoryEntry, bool) {
	if len(history) <= sidecarHistoryLimit {
		return history, false
	}
	return history[len(history)-sidecarHistoryLimit:], true
}

// sidecarDurabilityProbe is a no-op seam naming each durability step as it
// completes. fsync has no effect a test can read back — the loss it prevents
// only appears after a power cut — and an unverified durability claim is
// exactly what this file used to make.
var sidecarDurabilityProbe = func(step string) {}

// writeSidecarDurably replaces the sidecar so that, once it returns, both the
// contents and the directory entry naming them have reached the disk.
//
// Deliberately not artifacts.WriteFileAtomic: that closes and renames without
// syncing either, so after a power loss the migrated database — committed and
// synced by SQLite — can survive while its provenance does not. A missing
// sidecar is read as the legitimate never-migrated state, so nothing later
// reconstructs it, and the migration record is gone for good.
func writeSidecarDurably(root string, data []byte) error {
	path := sidecarPath(root)
	temp, err := os.CreateTemp(root, ".approach.db.meta-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(artifacts.FilePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	sidecarDurabilityProbe("file-sync")
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	sidecarDurabilityProbe("rename")
	// After the rename, never before: syncing the directory first would persist
	// an entry that does not exist yet and leave the one that matters unsynced.
	if err := syncDirectory(root); err != nil {
		return err
	}
	sidecarDurabilityProbe("dir-sync")
	return nil
}

// newGenerationID identifies one migration event. Failure to read the system
// CSPRNG is not worth failing a migration over — the ID names backup files and
// is never a security boundary — so it degrades to a timestamp.
func newGenerationID() string {
	var raw [generationIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// sidecarCompatibilityNotice reports a sidecar declaring a compatibility floor
// above what this build implements. Empty when there is nothing to say.
//
// This is the case user_version cannot express: a newer build migrated the
// database to a physical version this one still opens, and declared in the
// sidecar that reading it needs a later generation anyway. The role gate keys
// on user_version and legitimately admits the open, so without this the
// operator sees no signal at all until something behaves strangely.
func sidecarCompatibilityNotice(root string) string {
	sidecar, ok := readSidecar(root)
	if !ok {
		return ""
	}
	required := sidecar.MinReaderGeneration
	if sidecar.MinWriterGeneration > required {
		required = sidecar.MinWriterGeneration
	}
	if required <= databaseSchemaVersion {
		return ""
	}
	return fmt.Sprintf("flow database at %s declares a minimum reader generation of %d,"+
		" above this build's %d; it was migrated by approach %s. Upgrade approach%s.",
		root, required, databaseSchemaVersion, sidecarBuildOrUnknown(sidecar),
		manifestReleaseSuffix(required))
}

func sidecarBuildOrUnknown(sidecar databaseSidecar) string {
	if sidecar.MigratedBy.BuildVersion == "" {
		return "(build unrecorded)"
	}
	return sidecar.MigratedBy.BuildVersion
}

// sidecarGenerationOrNoGen names the generation a backup filename carries.
// The very first migration of any root runs with no sidecar to draw one from,
// and that is the ordinary case rather than an edge one.
func sidecarGenerationOrNoGen(root string) string {
	sidecar, ok := readSidecar(root)
	if !ok || sidecar.GenerationID == "" {
		return "nogen"
	}
	return sidecar.GenerationID
}
