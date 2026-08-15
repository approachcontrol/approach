package flowstore_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/approachcontrol/approach/flowstore"
)

func TestReconcileEpicProgressionSuccessorClassifiesAuthoritativeState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		flow    string
		outcome flowstore.EpicProgressionSuccessorOutcome
	}{
		{name: "accepted", flow: "prepared", outcome: flowstore.EpicProgressionSuccessorAccepted},
		{name: "missing flow", flow: "absent", outcome: flowstore.EpicProgressionSuccessorReleased},
		{name: "changed link", flow: "wrong-link", outcome: flowstore.EpicProgressionSuccessorReleased},
		{name: "missing receipt", flow: "incomplete", outcome: flowstore.EpicProgressionSuccessorOwnedObstruction},
		{name: "closed", flow: "closed", outcome: flowstore.EpicProgressionSuccessorOwnedObstruction},
		{name: "non pending", flow: "running", outcome: flowstore.EpicProgressionSuccessorOwnedObstruction},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			repo := filepath.Join(t.TempDir(), "repo")
			key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic"}
			link := flowstore.BeadLink{ID: "epic.2", EpicID: "epic"}
			if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			flowID := "successor"
			if tt.flow != "absent" {
				storedLink := link
				if tt.flow == "wrong-link" {
					storedLink.ID = "epic.other"
				}
				flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
					FlowID: flowID, Title: "Successor", Instructions: "Test.", RepoPath: repo, Bead: storedLink,
				}, flowstore.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
					FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/successor",
				}); err != nil {
					t.Fatal(err)
				}
				if tt.flow != "incomplete" {
					flow, err = finalizer.Finalize(nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch tt.flow {
				case "closed":
					if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: flowID, Reason: "closed"}); err != nil {
						t.Fatal(err)
					}
				case "running":
					phases := flowstore.OrderedPhases(flow.Phases)
					if len(phases) == 0 {
						t.Fatal("prepared flow has no phase")
					}
					if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: flowID, PhaseID: phases[0].PhaseID, LaunchID: "launch"}); err != nil {
						t.Fatal(err)
					}
				}
			}

			result, err := store.ReconcileEpicProgressionSuccessor(flowstore.EpicProgressionSuccessorUpdate{
				FlowID: flowID, Key: key, Bead: link,
			})
			if err != nil {
				t.Fatalf("ReconcileEpicProgressionSuccessor() error = %v", err)
			}
			if result.Outcome != tt.outcome {
				t.Fatalf("outcome = %q, want %q (flow = %#v)", result.Outcome, tt.outcome, result.Flow)
			}
		})
	}
}

func TestReconcileEpicProgressionSuccessorValidationFailureIsRetryable(t *testing.T) {
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	result, err := store.ReconcileEpicProgressionSuccessor(flowstore.EpicProgressionSuccessorUpdate{})
	if err == nil || result.Outcome != flowstore.EpicProgressionSuccessorRetryable {
		t.Fatalf("result = %#v, err %v; want retryable validation failure", result, err)
	}
}

func TestReconcileEpicProgressionSuccessorInactivePrecedesEveryFlowCondition(t *testing.T) {
	for _, progressionState := range []string{"absent", "disabled"} {
		for _, flowCondition := range []string{"absent", "wrong-link", "incomplete", "closed", "running", "prepared"} {
			t.Run(fmt.Sprintf("%s/%s", progressionState, flowCondition), func(t *testing.T) {
				store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				repo := filepath.Join(t.TempDir(), "repo")
				key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic"}
				link := flowstore.BeadLink{ID: "epic.2", EpicID: "epic"}
				if progressionState != "absent" {
					if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false}); err != nil {
						t.Fatal(err)
					}
				}
				flowID := "successor-" + flowCondition
				if flowCondition != "absent" {
					storedLink := link
					if flowCondition == "wrong-link" {
						storedLink.ID = "epic.other"
					}
					flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
						FlowID: flowID, Title: "Inactive successor", Instructions: "Test.", RepoPath: repo, Bead: storedLink,
					}, flowstore.CreateOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
						FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/inactive-successor",
					}); err != nil {
						t.Fatal(err)
					}
					if flowCondition != "incomplete" {
						flow, err = finalizer.Finalize(nil)
						if err != nil {
							t.Fatal(err)
						}
					}
					switch flowCondition {
					case "closed":
						if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: flowID, Reason: "closed"}); err != nil {
							t.Fatal(err)
						}
					case "running":
						phase := flowstore.OrderedPhases(flow.Phases)[0]
						if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: flowID, PhaseID: phase.PhaseID, LaunchID: "launch"}); err != nil {
							t.Fatal(err)
						}
					}
				}
				// The inactive progression read must win over every simultaneous
				// authoritative Flow condition above.
				result, err := store.ReconcileEpicProgressionSuccessor(flowstore.EpicProgressionSuccessorUpdate{
					FlowID: flowID, Key: key, Bead: link,
				})
				if err != nil || result.Outcome != flowstore.EpicProgressionSuccessorInactive {
					t.Fatalf("result = %#v, err %v; want inactive", result, err)
				}
				persisted, found, err := store.ReadEpicProgression(key)
				if err != nil || found != (progressionState != "absent") || (found && persisted.Enabled) {
					t.Fatalf("inactive state changed = %#v, found %t, err %v", persisted, found, err)
				}
			})
		}
	}
}

func TestEpicProgressionPersistsPerCanonicalRepositoryAndEpic(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	times := []time.Time{
		time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 14, 12, 1, 0, 0, time.UTC),
		time.Date(2026, 8, 14, 12, 2, 0, 0, time.UTC),
	}
	next := 0
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time {
		value := times[next]
		next++
		return value
	}})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	key := flowstore.EpicProgressionKey{RepoPath: repo + string(filepath.Separator) + ".", EpicID: " epic-1 "}
	if _, found, err := store.ReadEpicProgression(key); err != nil || found {
		t.Fatalf("ReadEpicProgression(absent) = found %t, err %v; want false, nil", found, err)
	}

	enabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatalf("SetEpicProgression(true) error = %v", err)
	}
	if enabled.RepoPath != filepath.Clean(repo) || enabled.EpicID != "epic-1" || !enabled.Enabled || enabled.Halt != nil {
		t.Fatalf("enabled progression = %#v", enabled)
	}
	if !enabled.CreatedAt.Equal(times[0]) || !enabled.UpdatedAt.Equal(times[0]) {
		t.Fatalf("enabled timestamps = %s / %s, want %s", enabled.CreatedAt, enabled.UpdatedAt, times[0])
	}

	redundant, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatalf("SetEpicProgression(redundant true) error = %v", err)
	}
	if !redundant.UpdatedAt.Equal(enabled.UpdatedAt) {
		t.Fatalf("redundant updated_at = %s, want unchanged %s", redundant.UpdatedAt, enabled.UpdatedAt)
	}

	disabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatalf("SetEpicProgression(false) error = %v", err)
	}
	if disabled.Enabled || disabled.Halt != nil || !disabled.CreatedAt.Equal(times[0]) || !disabled.UpdatedAt.Equal(times[1]) {
		t.Fatalf("disabled progression = %#v", disabled)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := flowstore.NewStore(flowstore.StoreOptions{Root: root})
	if err != nil {
		t.Fatalf("reopen NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, found, err := reopened.ReadEpicProgression(flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic-1"})
	if err != nil || !found {
		t.Fatalf("ReadEpicProgression(reopened) = found %t, err %v", found, err)
	}
	if got.Enabled || !got.CreatedAt.Equal(times[0]) || !got.UpdatedAt.Equal(times[1]) {
		t.Fatalf("reopened progression = %#v", got)
	}
}

func TestEpicProgressionDoneTransitionsRequireAuthoritativeActiveState(t *testing.T) {
	for _, initial := range []string{"absent", "off", "halted"} {
		t.Run(initial, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			key := flowstore.EpicProgressionKey{RepoPath: filepath.Join(t.TempDir(), "repo"), EpicID: "epic"}
			if initial == "off" {
				if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key}); err != nil {
					t.Fatal(err)
				}
			}
			if initial == "halted" {
				// Halt creation is not public yet; an ordinary off row still proves
				// that non-active authoritative state cannot be promoted to done.
				if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key}); err != nil {
					t.Fatal(err)
				}
			}
			before, foundBefore, readErr := store.ReadEpicProgression(key)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Done: true}); err == nil {
				t.Fatalf("SetEpicProgression(done) from %s succeeded", initial)
			}
			after, foundAfter, readErr := store.ReadEpicProgression(key)
			if readErr != nil || foundAfter != foundBefore || after != before {
				t.Fatalf("rejected done changed row: before=%#v/%t after=%#v/%t err=%v", before, foundBefore, after, foundAfter, readErr)
			}
		})
	}

	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := flowstore.EpicProgressionKey{RepoPath: filepath.Join(t.TempDir(), "repo"), EpicID: "epic"}
	active, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	done, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Done: true})
	if err != nil || done.Enabled || !done.Done || done.Halt != nil {
		t.Fatalf("active -> done = %#v, err %v", done, err)
	}
	redundant, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Done: true})
	if err != nil || !redundant.UpdatedAt.Equal(done.UpdatedAt) {
		t.Fatalf("done -> done = %#v, err %v", redundant, err)
	}
	off, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key})
	if err != nil || off.Done || off.Enabled || !off.CreatedAt.Equal(active.CreatedAt) {
		t.Fatalf("done -> manual off = %#v, err %v", off, err)
	}
}

func TestEnableEpicProgressionRequiresExactPreparedPendingFlow(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "worktree")
	stamp := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	store, err := flowstore.NewStore(flowstore.StoreOptions{Root: root, Now: func() time.Time { return stamp }})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	link := flowstore.BeadLink{ID: "epic-1.1", EpicID: "epic-1"}
	flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "child-flow", Title: "Child", Instructions: "Test.", RepoPath: repo, Bead: link,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatalf("CreatePreparation() error = %v", err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{FlowID: flow.FlowID, WorktreePath: worktree, Branch: "flow/child"}); err != nil {
		t.Fatalf("SetStartMetadata() error = %v", err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic-1"}
	if _, _, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID,
		Key:    flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "other-epic"},
		Bead:   link,
	}); err == nil {
		t.Fatal("EnableEpicProgressionForPreparedFlow(mismatched key epic) succeeded")
	}
	if _, found, err := store.ReadEpicProgression(flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "other-epic"}); err != nil || found {
		t.Fatalf("unrelated progression after rejected enable = found %t, err %v", found, err)
	}
	if _, _, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID, Key: key, Bead: flowstore.BeadLink{ID: "wrong", EpicID: "epic-1"},
	}); err == nil {
		t.Fatal("EnableEpicProgressionForPreparedFlow(wrong link) succeeded")
	}
	if _, found, err := store.ReadEpicProgression(key); err != nil || found {
		t.Fatalf("progression after rejected enable = found %t, err %v", found, err)
	}

	progression, authoritative, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID, Key: key, Bead: link,
	})
	if err != nil {
		t.Fatalf("EnableEpicProgressionForPreparedFlow() error = %v", err)
	}
	if !progression.Enabled || authoritative.PreparedAt == nil || authoritative.Status != flowstore.StatusPending || authoritative.Bead != link {
		t.Fatalf("enable result = progression %#v, flow %#v", progression, authoritative)
	}
}

func TestEnableEpicProgressionRefusesIncompleteClosedAndRunningFlows(t *testing.T) {
	for _, state := range []string{"incomplete", "closed", "running"} {
		t.Run(state, func(t *testing.T) {
			store, err := flowstore.NewStore(flowstore.StoreOptions{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			repo := filepath.Join(t.TempDir(), "repo")
			link := flowstore.BeadLink{ID: "epic.1", EpicID: "epic"}
			flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
				FlowID: "state-refusal", Title: "State", Instructions: "Test.", RepoPath: repo, Bead: link,
			}, flowstore.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
				FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/state-refusal",
			}); err != nil {
				t.Fatal(err)
			}
			if state != "incomplete" {
				flow, err = finalizer.Finalize(nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case "closed":
				if _, err := store.CloseFlow(flowstore.ClosureUpdate{FlowID: flow.FlowID, Reason: "closed before enable"}); err != nil {
					t.Fatal(err)
				}
			case "running":
				phases := flowstore.OrderedPhases(flow.Phases)
				if len(phases) == 0 {
					t.Fatal("prepared Flow has no phase")
				}
				if _, err := store.AddPhaseLaunchID(flowstore.PhaseLaunchUpdate{FlowID: flow.FlowID, PhaseID: phases[0].PhaseID, LaunchID: "launch-1"}); err != nil {
					t.Fatal(err)
				}
			}
			key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic"}
			if _, _, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
				FlowID: flow.FlowID, Key: key, Bead: link,
			}); err == nil {
				t.Fatalf("EnableEpicProgressionForPreparedFlow() accepted %s Flow", state)
			}
			if _, found, err := store.ReadEpicProgression(key); err != nil || found {
				t.Fatalf("progression after %s refusal = found %t, err %v", state, found, err)
			}
		})
	}
}

func TestEpicProgressionUpdatesClampRegressingClock(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	repo := filepath.Join(t.TempDir(), "repo")
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root: t.TempDir(),
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	link := flowstore.BeadLink{ID: "epic.1", EpicID: "epic"}
	flow, finalizer, err := store.CreatePreparation(flowstore.FlowRecord{
		FlowID: "progression-clock", Title: "Progression clock", Instructions: "Keep timestamps monotonic.", RepoPath: repo, Bead: link,
	}, flowstore.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStartMetadata(flowstore.StartMetadataUpdate{
		FlowID: flow.FlowID, WorktreePath: filepath.Join(t.TempDir(), "worktree"), Branch: "flow/progression-clock",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := finalizer.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	key := flowstore.EpicProgressionKey{RepoPath: repo, EpicID: "epic"}
	initial, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	enabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.UpdatedAt.After(initial.UpdatedAt) {
		t.Fatalf("enabled updated_at = %s, want after %s", enabled.UpdatedAt, initial.UpdatedAt)
	}

	now = now.Add(-time.Minute)
	disabled, err := store.SetEpicProgression(flowstore.EpicProgressionUpdate{Key: key, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.UpdatedAt.Before(enabled.UpdatedAt) {
		t.Fatalf("disabled updated_at regressed from %s to %s", enabled.UpdatedAt, disabled.UpdatedAt)
	}
	now = now.Add(-time.Minute)
	atomicallyEnabled, _, err := store.EnableEpicProgressionForPreparedFlow(flowstore.PreparedEpicProgressionUpdate{
		FlowID: flow.FlowID, Key: key, Bead: link,
	})
	if err != nil {
		t.Fatal(err)
	}
	if atomicallyEnabled.UpdatedAt.Before(disabled.UpdatedAt) {
		t.Fatalf("atomic enable updated_at regressed from %s to %s", disabled.UpdatedAt, atomicallyEnabled.UpdatedAt)
	}
}
