package flowstore_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/approachcontrol/approach/flowstore"
)

func TestUntrackedOwnerLifecycleIsIdentityFenced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Role: flowstore.RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := mustCreateFlow(t, store, "durable untracked owner")

	claimed, err := store.ClaimUntrackedOwner(flowstore.UntrackedOwnerClaim{
		FlowID: record.FlowID,
		Owner:  flowstore.UntrackedOwner{LaunchID: "launch-1", Role: flowstore.UntrackedOwnerWorktreeAgent},
	})
	if err != nil {
		t.Fatalf("ClaimUntrackedOwner: %v", err)
	}
	if got := claimed.UntrackedOwner; got == nil || got.LaunchID != "launch-1" || got.State != flowstore.UntrackedOwnerReserved || got.ReservedAt.IsZero() {
		t.Fatalf("claimed owner = %#v", got)
	}
	if _, err := store.ClaimUntrackedOwner(flowstore.UntrackedOwnerClaim{FlowID: record.FlowID, Owner: flowstore.UntrackedOwner{LaunchID: "launch-2", Role: flowstore.UntrackedOwnerRepair}}); !errors.Is(err, flowstore.ErrFlowUntrackedOwned) {
		t.Fatalf("competing claim error = %v", err)
	}

	activated, err := store.ActivateUntrackedOwner(flowstore.UntrackedOwnerActivation{
		FlowID: record.FlowID, LaunchID: "launch-1",
		Transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "approach-repo", Window: "launch-1"},
	})
	if err != nil {
		t.Fatalf("ActivateUntrackedOwner: %v", err)
	}
	if got := activated.UntrackedOwner; got == nil || got.State != flowstore.UntrackedOwnerLive || got.ActivatedAt.IsZero() || got.Transport.Window != "launch-1" {
		t.Fatalf("activated owner = %#v", got)
	}
	if _, err := store.ReleaseUntrackedOwner(flowstore.UntrackedOwnerRelease{FlowID: record.FlowID, LaunchID: "stale"}); !errors.Is(err, flowstore.ErrUntrackedOwnerChanged) {
		t.Fatalf("stale release error = %v", err)
	}
	ended, err := store.ReleaseUntrackedOwner(flowstore.UntrackedOwnerRelease{FlowID: record.FlowID, LaunchID: "launch-1"})
	if err != nil {
		t.Fatalf("ReleaseUntrackedOwner: %v", err)
	}
	if got := ended.UntrackedOwner; got == nil || got.State != flowstore.UntrackedOwnerEnded || got.EndedAt.IsZero() {
		t.Fatalf("ended owner = %#v", got)
	}
}

func TestUntrackedOwnerReplacementAndConcurrentClaimsFenceByLaunch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Role: flowstore.RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Role: flowstore.RoleWriter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	record := mustCreateFlow(t, first, "concurrent durable owner")

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, store := range []*flowstore.Store{first, second} {
		wg.Add(1)
		go func(i int, store *flowstore.Store) {
			defer wg.Done()
			_, err := store.ClaimUntrackedOwner(flowstore.UntrackedOwnerClaim{FlowID: record.FlowID, Owner: flowstore.UntrackedOwner{LaunchID: []string{"launch-a", "launch-b"}[i], Role: flowstore.UntrackedOwnerAutofix}})
			errs <- err
		}(i, store)
	}
	wg.Wait()
	close(errs)
	var successes, occupied int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, flowstore.ErrFlowUntrackedOwned):
			occupied++
		default:
			t.Fatalf("claim error = %v", err)
		}
	}
	if successes != 1 || occupied != 1 {
		t.Fatalf("successes=%d occupied=%d", successes, occupied)
	}
	current, err := first.Read(record.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	winner := current.UntrackedOwner.LaunchID
	replaced, err := second.ReplaceUntrackedOwner(flowstore.UntrackedOwnerReplacement{
		FlowID: record.FlowID, ExpectedLaunchID: winner,
		Owner: flowstore.UntrackedOwner{LaunchID: "launch-new", Role: flowstore.UntrackedOwnerRepair},
	})
	if err != nil {
		t.Fatalf("ReplaceUntrackedOwner: %v", err)
	}
	if replaced.UntrackedOwner.LaunchID != "launch-new" {
		t.Fatalf("replacement = %#v", replaced.UntrackedOwner)
	}
	if _, err := first.ReleaseUntrackedOwner(flowstore.UntrackedOwnerRelease{FlowID: record.FlowID, LaunchID: winner}); !errors.Is(err, flowstore.ErrUntrackedOwnerChanged) {
		t.Fatalf("late winner release error = %v", err)
	}
}
