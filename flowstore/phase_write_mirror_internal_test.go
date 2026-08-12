package flowstore

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestFlowPhaseForWriteCarriesEveryFlowPhaseField enforces the invariant
// documented on flowPhaseForWrite: it is a hand-maintained mirror with no
// compile-time link to FlowPhase, so a field added to FlowPhase alone is
// silently erased on the unresolved-graph write path.
//
// The check is deliberately value-based rather than tag-based. Comparing struct
// tags would prove the two declarations match while still passing when a new
// field is declared in both and then forgotten in flowPhaseForWriteFrom — which
// is the same silent erasure. Filling every FlowPhase field with a distinct
// non-zero value and comparing the marshaled objects covers the declaration and
// the copy at once, and reads the real JSON rather than inferring it from tags,
// so embedded and json:"-" fields cannot produce a false verdict.
func TestFlowPhaseForWriteCarriesEveryFlowPhaseField(t *testing.T) {
	phase := filledFlowPhase(t)
	sourceKeys, sourceValues := marshaledObject(t, phase)
	mirrorKeys, mirrorValues := marshaledObject(t, flowPhaseForWriteFrom(phase, false))

	if !reflect.DeepEqual(sourceKeys, mirrorKeys) {
		t.Fatalf("JSON keys differ; flowPhaseForWrite must mirror FlowPhase in name and order:\n source = %v\n mirror = %v",
			sourceKeys, mirrorKeys)
	}
	for key, want := range sourceValues {
		got, ok := mirrorValues[key]
		if !ok {
			t.Fatalf("flowPhaseForWrite drops %q", key)
		}
		if string(got) != string(want) {
			t.Fatalf("flowPhaseForWriteFrom does not copy %q: wrote %s, FlowPhase has %s", key, got, want)
		}
	}
}

// filledFlowPhase returns a FlowPhase with every field set to a distinct
// non-zero value, so an uncopied field shows up as a missing key or a zero
// value rather than matching by accident. A field of an unhandled type fails
// loudly instead of being left zero and silently escaping the comparison.
func filledFlowPhase(t *testing.T) FlowPhase {
	t.Helper()
	var phase FlowPhase
	value := reflect.ValueOf(&phase).Elem()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		name := value.Type().Field(i).Name
		switch field.Interface().(type) {
		case string:
			field.SetString("value-" + name)
		case int:
			field.SetInt(int64(i + 1))
		case []string:
			field.Set(reflect.ValueOf([]string{"value-" + name}))
		case []Session:
			field.Set(reflect.ValueOf([]Session{{Provider: "value-" + name}}))
		case time.Time:
			field.Set(reflect.ValueOf(base.Add(time.Duration(i) * time.Hour)))
		default:
			t.Fatalf("FlowPhase field %s has unhandled type %s; extend filledFlowPhase so the mirror guard keeps covering it",
				name, field.Type())
		}
	}
	return phase
}

// marshaledObject returns the top-level JSON keys of v in encounter order plus
// the raw value of each, read from the marshaled bytes rather than from tags.
func marshaledObject(t *testing.T, v any) ([]string, map[string]json.RawMessage) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal(%T) error = %v", v, err)
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("Unmarshal(%T) error = %v\n%s", v, err, data)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("reading opening brace of %T: %v", v, err)
	}
	keys := make([]string, 0, len(values))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("reading key of %T: %v", v, err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatalf("expected string key in %T, got %T", v, token)
		}
		keys = append(keys, key)
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			t.Fatalf("reading value of %q in %T: %v", key, v, err)
		}
	}
	return keys, values
}
