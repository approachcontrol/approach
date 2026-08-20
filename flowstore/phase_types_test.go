package flowstore_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestFlowPhaseJSONKeepsStatusAndKindStrings(t *testing.T) {
	now := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	phase := flowstore.FlowPhase{
		PhaseID:   "implementation",
		Title:     "Implementation",
		Kind:      flowstore.KindImplementation,
		Status:    flowstore.PhaseReady,
		Order:     1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(phase)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	encoded := string(data)
	if !strings.Contains(encoded, `"status":"ready"`) {
		t.Fatalf("JSON status = %s, want \"status\":\"ready\"", encoded)
	}
	if !strings.Contains(encoded, `"kind":"implementation"`) {
		t.Fatalf("JSON kind = %s, want \"kind\":\"implementation\"", encoded)
	}

	var decoded flowstore.FlowPhase
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Status != flowstore.PhaseReady {
		t.Fatalf("decoded status = %q, want %q", decoded.Status, flowstore.PhaseReady)
	}
	if decoded.Kind != flowstore.KindImplementation {
		t.Fatalf("decoded kind = %q, want %q", decoded.Kind, flowstore.KindImplementation)
	}
}
