package flowstore

import (
	"slices"
	"strings"
	"testing"
)

func TestSQLiteSchemaObjectsAcceptEquivalentObjectsInAnyOrder(t *testing.T) {
	objectsByVersion := map[int64][]string{
		1: {
			"index:idx_flows_repo_updated:flows",
			"index:idx_flows_status_updated:flows",
			"index:idx_flows_updated:flows",
			"table:flows:flows",
		},
		2: {
			"index:idx_flows_repo_updated:flows",
			"index:idx_flows_status_updated:flows",
			"index:idx_flows_updated:flows",
			"table:flows:flows",
			"trigger:guard_linked_flow_record_update:flows",
		},
	}
	objectsByVersion[3] = append(slices.Clone(objectsByVersion[2]),
		"table:epic_progressions:epic_progressions",
		"trigger:guard_prepared_flow_record_update:flows",
	)
	objectsByVersion[4] = append(slices.Clone(objectsByVersion[3]),
		"trigger:guard_epic_progression_done_insert:epic_progressions",
		"trigger:guard_epic_progression_done_record_update:epic_progressions",
	)
	objectsByVersion[5] = append(slices.Clone(objectsByVersion[4]),
		"trigger:guard_progression_claim_record_update:flows",
	)
	objectsByVersion[6] = append(slices.Clone(objectsByVersion[5]),
		"trigger:guard_preparation_nonce_update:flows",
	)
	objectsByVersion[7] = append(slices.Clone(objectsByVersion[6]),
		"trigger:guard_recovered_launch_state_update:flows",
	)
	objectsByVersion[8] = append(slices.Clone(objectsByVersion[7]),
		"trigger:guard_untracked_owner_delete:flows",
		"trigger:guard_untracked_owner_update:flows",
	)

	for version := int64(1); version <= databaseSchemaVersion; version++ {
		objects := slices.Clone(objectsByVersion[version])
		slices.Reverse(objects)
		if err := validateSQLiteSchemaObjectSet(objects, version); err != nil {
			t.Errorf("validateSQLiteSchemaObjectSet(version %d) error = %v, want nil for equivalent objects in reverse order", version, err)
		}
	}
}

func TestSQLiteSchemaObjectsRequireAnExactSet(t *testing.T) {
	objects := []string{
		"trigger:guard_recovered_launch_state_update:flows",
		"trigger:guard_preparation_nonce_update:flows",
		"trigger:guard_progression_claim_record_update:flows",
		"trigger:guard_prepared_flow_record_update:flows",
		"trigger:guard_linked_flow_record_update:flows",
		"trigger:guard_epic_progression_done_record_update:epic_progressions",
		"trigger:guard_epic_progression_done_insert:epic_progressions",
		"trigger:guard_untracked_owner_delete:flows",
		"trigger:guard_untracked_owner_update:flows",
		"table:flows:flows",
		"table:epic_progressions:epic_progressions",
		"index:idx_flows_updated:flows",
		"index:idx_flows_status_updated:flows",
		"index:idx_flows_repo_updated:flows",
	}

	for _, tt := range []struct {
		name    string
		objects []string
	}{
		{name: "missing", objects: slices.Clone(objects[1:])},
		{name: "extra", objects: append(slices.Clone(objects), "table:unexpected:unexpected")},
		{name: "changed", objects: append(slices.Clone(objects[1:]), "trigger:changed:flows")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLiteSchemaObjectSet(tt.objects, databaseSchemaVersion)
			if err == nil || !strings.Contains(err.Error(), "incompatible schema objects") {
				t.Fatalf("validateSQLiteSchemaObjectSet() error = %v, want incompatible schema objects", err)
			}
		})
	}
}
