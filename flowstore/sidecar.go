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
const sidecarSchemaVersion = 1

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
	return sidecar, true
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
func reconcileSidecar(root string, storedVersion int64, migrated bool) error {
	existing, present := readSidecar(root)
	switch {
	case migrated:
		return writeSidecar(root, storedVersion, sidecarProvenanceMigrated, newGenerationID())
	case !present:
		// Creation writes no sidecar, so a never-migrated root legitimately has
		// none. Inventing one here would claim a migration that never happened.
		return nil
	case existing.PhysicalVersion == storedVersion:
		return nil
	default:
		generation := existing.GenerationID
		if generation == "" {
			generation = newGenerationID()
		}
		return writeSidecar(root, storedVersion, sidecarProvenanceReconstructed, generation)
	}
}

func writeSidecar(root string, storedVersion int64, provenance, generation string) error {
	sidecar := databaseSidecar{
		SchemaVersion:       sidecarSchemaVersion,
		GenerationID:        generation,
		PhysicalVersion:     storedVersion,
		MinReaderGeneration: storedVersion,
		MinWriterGeneration: storedVersion,
		MigratedBy: sidecarMigrator{
			BuildVersion: version.Version(),
			Commit:       version.Commit(),
			At:           time.Now().UTC().Format(time.RFC3339),
		},
		Provenance: provenance,
	}
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return fmt.Errorf("encode flow database sidecar: %w", err)
	}
	// Atomic: temp file, fsync, rename. A torn sidecar would read as absent,
	// which is safe, but a rename keeps even that from being observable.
	if err := artifacts.WriteFileAtomic(sidecarPath(root), append(data, '\n')); err != nil {
		return fmt.Errorf("write flow database sidecar: %w", err)
	}
	return nil
}

// newGenerationID identifies one migration event. Failure to read the system
// CSPRNG is not worth failing a migration over — the ID names backup files and
// is never a security boundary — so it degrades to a timestamp.
func newGenerationID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(raw[:])
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
