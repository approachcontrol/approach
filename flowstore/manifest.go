package flowstore

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// schemaManifestJSON is the checked-in compatibility declaration, one entry per
// physical schema version this build knows about.
//
// Embedded rather than derived from the code because its whole job is to be a
// SEPARATE statement that a maintainer has to write down: a value computed from
// databaseSchemaVersion would move with the bump it exists to gate.
//
//go:embed schema_manifest.json
var schemaManifestJSON []byte

// schemaEntry declares what one physical schema version requires and which
// release first shipped it.
//
// Both a generation number and a release string, deliberately. The numbers are
// what writeSidecar stamps and what a compatibility refusal compares; the
// release string is the only one of the two an operator can act on.
type schemaEntry struct {
	PhysicalVersion     int64 `json:"physical_version"`
	MinReaderGeneration int64 `json:"min_reader_generation"`
	MinWriterGeneration int64 `json:"min_writer_generation"`
	// FirstCompatibleRelease is the earliest tagged release that opens a
	// database at this version. "unknown" where the mapping cannot be
	// reconstructed honestly — an absent tag is better than a fabricated one an
	// operator would go looking for.
	FirstCompatibleRelease string `json:"first_compatible_release"`
	// MigrationTestedPredecessors lists the versions whose migration INTO this
	// one is covered by an executing test. manifest_test.go proves the list
	// matches supportedPredecessorVersions and that each entry really migrates.
	MigrationTestedPredecessors []int64 `json:"migration_tested_predecessors"`
	ReleaseNotes                string  `json:"release_notes"`
}

type schemaManifest struct {
	ManifestVersion int64         `json:"manifest_version"`
	Schemas         []schemaEntry `json:"schemas"`
}

// loadManifest parses the embedded manifest once.
//
// sync.OnceValue rather than an init: a malformed checked-in manifest then
// fails the tests that read it rather than panicking at package load, which
// would take out every command in the binary — including `db inspect`, the one
// an operator reaches for when something is already wrong.
var loadManifest = sync.OnceValue(func() schemaManifest {
	var manifest schemaManifest
	if err := json.Unmarshal(schemaManifestJSON, &manifest); err != nil {
		// Unreachable in a build whose tests ran: manifestEntries is read by
		// the package's own gate. Reported as an empty manifest so the gate
		// fails with its own message instead of a panic here.
		return schemaManifest{}
	}
	return manifest
})

// manifestEntries returns the declared schemas in file order. Ascending order
// is an invariant the gate asserts rather than one this function imposes, so a
// mis-ordered file is reported instead of silently sorted.
func manifestEntries() []schemaEntry {
	return loadManifest().Schemas
}

// manifestEntry finds the declaration for one physical version. A miss is
// ordinary: a database from a newer build is by definition absent from this
// build's manifest, and that case has its own wording.
func manifestEntry(version int64) (schemaEntry, bool) {
	for _, entry := range manifestEntries() {
		if entry.PhysicalVersion == version {
			return entry, true
		}
	}
	return schemaEntry{}, false
}

// manifestGenerations reports the compatibility floors for a version, falling
// back to the version itself when the manifest does not declare it. The
// fallback is what every version stamped before this manifest existed already
// meant, so an undeclared version keeps today's behaviour rather than
// stamping a zero floor that would claim every build can open it.
func manifestGenerations(version int64) (minReader, minWriter int64) {
	if entry, ok := manifestEntry(version); ok {
		return entry.MinReaderGeneration, entry.MinWriterGeneration
	}
	return version, version
}

// manifestReleaseSuffix names the release an operator should install, when this
// build's manifest knows it. Empty otherwise — see refuseIncompatibleBuild.
func manifestReleaseSuffix(version int64) string {
	entry, ok := manifestEntry(version)
	if !ok || entry.FirstCompatibleRelease == "" || entry.FirstCompatibleRelease == "unknown" {
		return ""
	}
	return fmt.Sprintf(" (first shipped in %s)", entry.FirstCompatibleRelease)
}
