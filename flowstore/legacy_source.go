package flowstore

import "encoding/json"

// legacyStoredFlow is used only while importing historical meta.json files.
// Runtime SQLite records carry no raw-field-presence archaeology.
type legacyStoredFlow struct {
	record            FlowRecord
	dependsOnPresence []rawDependsOnState
	headlessPresent   bool
}

func rawDependsOnPresence(data []byte) []rawDependsOnState {
	var raw struct {
		Phases []map[string]json.RawMessage `json:"phases"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	presence := make([]rawDependsOnState, len(raw.Phases))
	for i, phase := range raw.Phases {
		_, ok := phase["depends_on"]
		presence[i] = rawDependsOnState{Present: ok}
	}
	return presence
}

func rawFieldPresent(data []byte, field string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw[field]
	return ok
}
