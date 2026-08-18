package flowstore

import (
	"errors"
	"testing"
)

func TestIsSchemaCompatibilityRefusalNamesExactlyTheTwoBuildPairingRefusals(t *testing.T) {
	for name, err := range map[string]error{
		"newer build":            refuseIncompatibleBuild(9, 9),
		"role refused migration": refuseRoleMigration(5, RoleWriter),
		"role legacy import":     refuseRoleLegacyImport("/root/flows", RoleReader),
		"role stage resume":      refuseRoleStageResume("/root/approach.db.migrating", RoleWriter),
	} {
		if !IsSchemaCompatibilityRefusal(err) {
			t.Errorf("%s: IsSchemaCompatibilityRefusal = false for %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"nil":           nil,
		"plain":         errors.New("database is locked"),
		"not found":     flowNotFoundError("flow-1"),
		"reader write":  errReaderWrite,
		"dev migration": errDevLiveMigrationRefused,
	} {
		if IsSchemaCompatibilityRefusal(err) {
			t.Errorf("%s: IsSchemaCompatibilityRefusal = true for %v", name, err)
		}
	}
}
