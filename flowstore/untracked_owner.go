package flowstore

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type UntrackedOwnerState string

const (
	UntrackedOwnerReserved UntrackedOwnerState = "reserved"
	UntrackedOwnerLive     UntrackedOwnerState = "live"
	UntrackedOwnerEnded    UntrackedOwnerState = "ended"
)

type UntrackedOwnerRole string

const (
	UntrackedOwnerWorktreeAgent UntrackedOwnerRole = "worktree_agent"
	UntrackedOwnerAutofix       UntrackedOwnerRole = "autofix"
	UntrackedOwnerRepair        UntrackedOwnerRole = "repair"
)

type UntrackedTransportKind string

const (
	UntrackedTransportLauncher     UntrackedTransportKind = "launcher_process"
	UntrackedTransportRepoTmux     UntrackedTransportKind = "repo_tmux"
	UntrackedTransportEmbeddedTmux UntrackedTransportKind = "embedded_tmux"
	UntrackedTransportDirect       UntrackedTransportKind = "direct_embedded"
)

// UntrackedOwnerTransport identifies the exact transport whose liveness proves
// whether an owner may be reclaimed. Only the fields used by Kind are set.
type UntrackedOwnerTransport struct {
	Kind         UntrackedTransportKind `json:"kind"`
	Socket       string                 `json:"socket,omitempty"`
	Session      string                 `json:"session,omitempty"`
	Window       string                 `json:"window,omitempty"`
	PID          int                    `json:"pid,omitempty"`
	ProcessToken string                 `json:"process_token,omitempty"`
}

// UntrackedOwner is the durable phase-untracked worktree owner. Ended owners
// remain as fenced lifecycle history until the next claim replaces them.
type UntrackedOwner struct {
	LaunchID      string                  `json:"launch_id"`
	Role          UntrackedOwnerRole      `json:"role"`
	State         UntrackedOwnerState     `json:"state"`
	Transport     UntrackedOwnerTransport `json:"transport,omitzero"`
	LauncherPID   int                     `json:"launcher_pid,omitempty"`
	LauncherToken string                  `json:"launcher_token,omitempty"`
	ReservedAt    time.Time               `json:"reserved_at"`
	ActivatedAt   time.Time               `json:"activated_at,omitempty"`
	EndedAt       time.Time               `json:"ended_at,omitempty"`
}

type UntrackedOwnerClaim struct {
	FlowID string
	Owner  UntrackedOwner
}

type UntrackedOwnerReplacement struct {
	FlowID           string
	ExpectedLaunchID string
	Owner            UntrackedOwner
}

type UntrackedOwnerActivation struct {
	FlowID    string
	LaunchID  string
	Transport UntrackedOwnerTransport
}

type UntrackedOwnerRelease struct {
	FlowID   string
	LaunchID string
}

func cloneUntrackedOwner(owner *UntrackedOwner) *UntrackedOwner {
	if owner == nil {
		return nil
	}
	cloned := *owner
	return &cloned
}

func validateUntrackedOwnerIdentity(owner UntrackedOwner) error {
	if strings.TrimSpace(owner.LaunchID) == "" {
		return errors.New("phase-untracked owner launch ID is required")
	}
	switch owner.Role {
	case UntrackedOwnerWorktreeAgent, UntrackedOwnerAutofix, UntrackedOwnerRepair:
		return nil
	default:
		return fmt.Errorf("invalid phase-untracked owner role %q", owner.Role)
	}
}

func (s *Store) ClaimUntrackedOwner(update UntrackedOwnerClaim) (FlowRecord, error) {
	if err := validateUntrackedOwnerIdentity(update.Owner); err != nil {
		return FlowRecord{}, err
	}
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, fmt.Errorf("%w: %s", ErrFlowClosed, record.FlowID)
		}
		if owner := record.UntrackedOwner; owner != nil && owner.State != UntrackedOwnerEnded {
			return FlowRecord{}, fmt.Errorf("%w: %s owns %s", ErrFlowUntrackedOwned, owner.LaunchID, record.FlowID)
		}
		owner := update.Owner
		owner.LaunchID = strings.TrimSpace(owner.LaunchID)
		owner.State = UntrackedOwnerReserved
		owner.Transport = update.Owner.Transport
		if owner.LauncherPID <= 0 && owner.Transport.Kind == UntrackedTransportLauncher {
			owner.LauncherPID = owner.Transport.PID
			owner.LauncherToken = owner.Transport.ProcessToken
		}
		owner.ReservedAt, owner.ActivatedAt, owner.EndedAt = now, time.Time{}, time.Time{}
		record.UntrackedOwner = &owner
		return record, nil
	})
}

// ReplaceUntrackedOwner reclaims a proven-dead active owner and installs a new
// reservation in the same writer transaction.
func (s *Store) ReplaceUntrackedOwner(update UntrackedOwnerReplacement) (FlowRecord, error) {
	if err := validateUntrackedOwnerIdentity(update.Owner); err != nil {
		return FlowRecord{}, err
	}
	expected := strings.TrimSpace(update.ExpectedLaunchID)
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		if FlowClosed(record) {
			return FlowRecord{}, fmt.Errorf("%w: %s", ErrFlowClosed, record.FlowID)
		}
		if record.UntrackedOwner == nil || record.UntrackedOwner.LaunchID != expected {
			return FlowRecord{}, fmt.Errorf("%w: expected %s", ErrUntrackedOwnerChanged, expected)
		}
		owner := update.Owner
		owner.LaunchID = strings.TrimSpace(owner.LaunchID)
		owner.State, owner.Transport = UntrackedOwnerReserved, update.Owner.Transport
		if owner.LauncherPID <= 0 && owner.Transport.Kind == UntrackedTransportLauncher {
			owner.LauncherPID = owner.Transport.PID
			owner.LauncherToken = owner.Transport.ProcessToken
		}
		owner.ReservedAt, owner.ActivatedAt, owner.EndedAt = now, time.Time{}, time.Time{}
		record.UntrackedOwner = &owner
		return record, nil
	})
}

// PrepareUntrackedOwnerTransport records the exact post-spawn identity while
// the launcher reservation still owns admission. A failed activation can then
// remain fail-closed on the real transport instead of only the launcher PID.
func (s *Store) PrepareUntrackedOwnerTransport(update UntrackedOwnerActivation) (FlowRecord, error) {
	expected := strings.TrimSpace(update.LaunchID)
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, _ time.Time) (FlowRecord, error) {
		owner := record.UntrackedOwner
		if owner == nil || owner.LaunchID != expected || owner.State == UntrackedOwnerEnded {
			return FlowRecord{}, fmt.Errorf("%w: expected %s", ErrUntrackedOwnerChanged, expected)
		}
		if owner.State == UntrackedOwnerLive && owner.Transport != update.Transport {
			return FlowRecord{}, fmt.Errorf("%w: transport changed for %s", ErrUntrackedOwnerChanged, expected)
		}
		owner.Transport = update.Transport
		return record, nil
	})
}

func (s *Store) ActivateUntrackedOwner(update UntrackedOwnerActivation) (FlowRecord, error) {
	expected := strings.TrimSpace(update.LaunchID)
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		owner := record.UntrackedOwner
		if owner == nil || owner.LaunchID != expected || owner.State == UntrackedOwnerEnded {
			return FlowRecord{}, fmt.Errorf("%w: expected %s", ErrUntrackedOwnerChanged, expected)
		}
		if owner.State == UntrackedOwnerLive {
			if owner.Transport != update.Transport {
				return FlowRecord{}, fmt.Errorf("%w: transport changed for %s", ErrUntrackedOwnerChanged, expected)
			}
			return record, nil
		}
		owner.State, owner.Transport, owner.ActivatedAt = UntrackedOwnerLive, update.Transport, now
		return record, nil
	})
}

func (s *Store) ReleaseUntrackedOwner(update UntrackedOwnerRelease) (FlowRecord, error) {
	expected := strings.TrimSpace(update.LaunchID)
	return s.updateFlowMetadataOnly(update.FlowID, func(record FlowRecord, now time.Time) (FlowRecord, error) {
		owner := record.UntrackedOwner
		if owner == nil || owner.LaunchID != expected {
			return FlowRecord{}, fmt.Errorf("%w: expected %s", ErrUntrackedOwnerChanged, expected)
		}
		if owner.State == UntrackedOwnerEnded {
			return record, nil
		}
		owner.State, owner.EndedAt = UntrackedOwnerEnded, now
		return record, nil
	})
}
