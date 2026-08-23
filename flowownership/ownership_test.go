package flowownership

import "testing"

type ownershipPayload struct {
	kind string
}

func TestOwnershipFencesExactIdentityTransitionsAndRelease(t *testing.T) {
	owners := Ownership[ownershipPayload, string]{}
	owners, ok := owners.Reserve("flow-1", "token-1", StateReserved, ownershipPayload{kind: "manual"}, nil)
	if !ok {
		t.Fatal("Reserve() = false, want true")
	}
	if _, ok := owners.Reserve("flow-1", "token-2", StateReserved, ownershipPayload{}, nil); ok {
		t.Fatal("duplicate Flow reservation succeeded")
	}
	if owners.Occupied("flow") || owners.Occupied("flow-10") || owners.Occupied("") {
		t.Fatal("prefix-like or blank Flow ID reported occupied")
	}
	if _, ok := owners.Transition("flow-1", "token-1", StatePreparing, StateReading); ok {
		t.Fatal("transition from wrong state succeeded")
	}
	if _, ok := owners.Transition("flow-1", "stale", StateReserved, StateReading); ok {
		t.Fatal("transition with stale token succeeded")
	}
	owners, ok = owners.Transition("flow-1", "token-1", StateReserved, StateReading)
	if !ok {
		t.Fatal("matching transition failed")
	}
	if stale, released := owners.Release("flow-1", "stale"); released || !stale.Occupied("flow-1") {
		t.Fatal("stale release changed ownership")
	}
	owners, ok = owners.Release("flow-1", "token-1")
	if !ok || owners.Occupied("flow-1") {
		t.Fatal("matching release did not free ownership")
	}
	if _, ok := owners.Reserve("flow-1", "token-1", StateReserved, ownershipPayload{}, nil); !ok {
		t.Fatal("released token could not be reused")
	}
}

func TestOwnershipTransfersAndReleasesSavedSessionAtomically(t *testing.T) {
	owners := Ownership[ownershipPayload, string]{}
	key := "codex/session-1"
	owners, ok := owners.Reserve("provisional", "token-1", StateReadingSession, ownershipPayload{}, &key)
	if !ok || !owners.SessionOccupied(key) {
		t.Fatal("saved-session reservation was not installed in both indexes")
	}
	if _, ok := owners.Reserve("other", "token-2", StateReadingSession, ownershipPayload{}, &key); ok {
		t.Fatal("duplicate saved-session reservation succeeded")
	}
	if _, ok := owners.TransferSession("provisional", "stale", key, "flow-1", StateReadingSession, StateReading); ok {
		t.Fatal("stale transfer succeeded")
	}
	if _, ok := owners.TransferSession("provisional", "token-1", key, "flow-1", StatePreparing, StateReading); ok {
		t.Fatal("wrong-state transfer succeeded")
	}
	owners, ok = owners.TransferSession("provisional", "token-1", key, "flow-1", StateReadingSession, StateReading)
	if !ok || owners.Occupied("provisional") || !owners.Occupied("flow-1") {
		t.Fatal("saved-session transfer did not move Flow ownership")
	}
	owner, ok := owners.SessionOwner(key)
	if !ok || owner.FlowID() != "flow-1" || owner.Token() != "token-1" {
		t.Fatalf("SessionOwner() = (%q, %q, %v)", owner.FlowID(), owner.Token(), ok)
	}
	owners, ok = owners.Release("flow-1", "token-1")
	if !ok || owners.SessionOccupied(key) || owners.Occupied("flow-1") {
		t.Fatal("release did not remove both ownership indexes")
	}
}
