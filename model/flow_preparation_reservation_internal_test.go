package model

import (
	"strings"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func TestNewWithOptionsCustomLaunchPersistenceDoesNotAuthorizeDefaultPreparation(t *testing.T) {
	repoPath := t.TempDir()
	m := NewWithOptions(nil, Options{
		SessionStateRoot: t.TempDir(),
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			t.Fatal("preparation reached custom launch persistence")
			return flowstore.FlowRecord{}, nil
		},
	})

	_, err := m.createFlow(FlowStartRequest{
		RepoPath: repoPath, Title: "Requires a real reservation", Instructions: "exercise preparation admission",
	})
	if err == nil || !strings.Contains(err.Error(), "missing authoritative ReserveLaunch") {
		t.Fatalf("PrepareFlow() error = %v, want missing authoritative reservation", err)
	}
}

func TestNewWithOptionsFlowStoreAuthorizesProgressionRecoveryWithCustomLaunchPersistence(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()
	created, _, err := store.CreatePreparation(flowstore.FlowRecord{
		Title: "Recover claimed child", Instructions: "repeat the durable claim", RepoPath: t.TempDir(),
		ProgressionClaim: true,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	m := NewWithOptions(nil, Options{
		FlowStore: store,
		AddFlowPhaseLaunchID: func(flowstore.PhaseLaunchUpdate) (flowstore.FlowRecord, error) {
			return flowstore.FlowRecord{}, nil
		},
	})

	reserved, release, err := m.reserveFlowPreparation(created.FlowID)
	if err != nil {
		t.Fatalf("reserveFlowPreparation() error = %v", err)
	}
	defer release()
	if reserved.FlowID != created.FlowID || reserved.PreparationGeneration != created.PreparationGeneration || !reserved.ProgressionClaim {
		t.Fatalf("reserveFlowPreparation() = %#v, want authoritative marked generation %#v", reserved, created)
	}
}
