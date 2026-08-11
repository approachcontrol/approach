package flowstore

import "encoding/json"

// legacyStoredFlow is used only while importing historical meta.json files.
// Runtime SQLite records carry no raw-field-presence archaeology.
type legacyStoredFlow struct {
	record            FlowRecord
	dependsOnPresence []rawDependsOnState
	headlessPresent   bool
}

// rawFlowFields is a legacy meta.json object decoded once. Reads that must tell
// an absent field from its zero value need the undecoded form, and a record read
// needs two such answers; sharing one decode keeps an import at two parses per
// Flow rather than three.
type rawFlowFields map[string]json.RawMessage

func decodeRawFlowFields(data []byte) rawFlowFields {
	var fields rawFlowFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	return fields
}

// present reports whether the persisted record carried a top-level key at all,
// so the read path can tell a legacy record with no field from one that stored
// the zero value.
func (raw rawFlowFields) present(field string) bool {
	_, ok := raw[field]
	return ok
}

// dependsOnPresence records, positionally, whether each persisted phase object
// carried a depends_on key at all — the distinction between a legacy record
// with no edges and an edge-aware record with empty edges.
func (raw rawFlowFields) dependsOnPresence() []rawDependsOnState {
	var phases []map[string]json.RawMessage
	if encoded, ok := raw["phases"]; ok {
		if err := json.Unmarshal(encoded, &phases); err != nil {
			return nil
		}
	}
	presence := make([]rawDependsOnState, len(phases))
	for i, phase := range phases {
		_, ok := phase["depends_on"]
		presence[i] = rawDependsOnState{Present: ok}
	}
	return presence
}
