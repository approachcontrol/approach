package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/embeddedterm"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/sessions"
)

func TestResolveUntrackedOwnerReleasesOnlyOnProvenTransportDeath(t *testing.T) {
	tests := []struct {
		name      string
		transport flowstore.UntrackedOwnerTransport
		repo      actions.TransportLiveness
		embedded  actions.TransportLiveness
		process   bool
		identity  string
		state     flowstore.UntrackedOwnerState
		launcher  int
		wantEnded bool
	}{
		{name: "repo live", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "owner"}, repo: actions.TransportLivenessLive},
		{name: "repo unknown", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "owner"}, repo: actions.TransportLivenessUnknown},
		{name: "repo dead", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "owner"}, repo: actions.TransportLivenessDead, wantEnded: true},
		{name: "embedded live", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportEmbeddedTmux, Socket: "socket", Session: "owner"}, embedded: actions.TransportLivenessLive},
		{name: "embedded unknown", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportEmbeddedTmux, Socket: "socket", Session: "owner"}, embedded: actions.TransportLivenessUnknown},
		{name: "embedded dead", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportEmbeddedTmux, Socket: "socket", Session: "owner"}, embedded: actions.TransportLivenessDead, wantEnded: true},
		{name: "reserved launcher live", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportLauncher, PID: 42, ProcessToken: "birth-a"}, process: true, identity: "birth-a"},
		{name: "reserved launcher reused", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportLauncher, PID: 42, ProcessToken: "birth-a"}, process: true, identity: "birth-b", wantEnded: true},
		{name: "live PID with unavailable identity is unknown", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportLauncher, PID: 42, ProcessToken: "birth-a"}, process: true},
		{name: "reserved launcher dead", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportLauncher, PID: 42, ProcessToken: "birth-a"}, wantEnded: true},
		{name: "reserved pending tmux keeps launcher fence", state: flowstore.UntrackedOwnerReserved, launcher: 42, process: true, identity: "birth-a", transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "pending"}, repo: actions.TransportLivenessDead},
		{name: "reserved pending tmux keeps launcher fence when identity is unavailable", state: flowstore.UntrackedOwnerReserved, launcher: 42, process: true, transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "pending"}, repo: actions.TransportLivenessDead},
		{name: "reserved pending tmux falls back to exact window", state: flowstore.UntrackedOwnerReserved, launcher: 42, transport: flowstore.UntrackedOwnerTransport{Kind: flowstore.UntrackedTransportRepoTmux, Session: "repo", Window: "pending"}, repo: actions.TransportLivenessLive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.state
			if state == "" {
				state = flowstore.UntrackedOwnerLive
			}
			record := flowstore.FlowRecord{FlowID: "flow-1", UntrackedOwner: &flowstore.UntrackedOwner{
				LaunchID: "launch-1", Role: flowstore.UntrackedOwnerRepair,
				State: state, Transport: tc.transport, LauncherPID: tc.launcher, LauncherToken: "birth-a",
			}}
			releases := 0
			sessionReads := 0
			m := NewWithOptions(nil, Options{
				ListFlows: func(flowstore.FlowFilter) ([]flowstore.FlowRecord, error) { return []flowstore.FlowRecord{record}, nil },
				ReadFlow:  func(string) (flowstore.FlowRecord, error) { return record, nil },
				ListSessions: func(sessions.SessionFilter) ([]sessions.SessionRecord, error) {
					sessionReads++
					return []sessions.SessionRecord{{LaunchID: "launch-1", Status: "ended"}}, nil
				},
				ClaimUntrackedOwner:    func(flowstore.UntrackedOwnerClaim) (flowstore.FlowRecord, error) { return record, nil },
				ActivateUntrackedOwner: func(flowstore.UntrackedOwnerActivation) (flowstore.FlowRecord, error) { return record, nil },
				ReleaseUntrackedOwner: func(flowstore.UntrackedOwnerRelease) (flowstore.FlowRecord, error) {
					releases++
					ended := record
					owner := *record.UntrackedOwner
					owner.State = flowstore.UntrackedOwnerEnded
					ended.UntrackedOwner = &owner
					return ended, nil
				},
				ProbeRepoTmuxOwner:     func(string, string) actions.TransportLiveness { return tc.repo },
				ProbeEmbeddedTmuxOwner: func(string, string) actions.TransportLiveness { return tc.embedded },
				ProcessIdentity: func(int) (string, bool) {
					return tc.identity, tc.process
				},
			})
			got, err := m.launchSeams.ResolveUntrackedOwner(record)
			if err != nil {
				t.Fatal(err)
			}
			if ended := got.UntrackedOwner.State == flowstore.UntrackedOwnerEnded; ended != tc.wantEnded {
				t.Fatalf("ended = %t, want %t", ended, tc.wantEnded)
			}
			if releases != boolInt(tc.wantEnded) {
				t.Fatalf("releases = %d, want %d", releases, boolInt(tc.wantEnded))
			}
			if sessionReads != 0 {
				t.Fatalf("provider session records are not process-exit evidence, reads = %d", sessionReads)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestDurableOwnerReleaseFailureIsRetried(t *testing.T) {
	calls := 0
	m := Model{launchSeams: flowLaunchSeams{ReleaseUntrackedOwner: func(update flowstore.UntrackedOwnerRelease) (flowstore.FlowRecord, error) {
		calls++
		if update.FlowID != "flow-1" || update.LaunchID != "launch-1" {
			t.Fatalf("release = %#v", update)
		}
		if calls < 3 {
			return flowstore.FlowRecord{}, errors.New("store busy")
		}
		return flowstore.FlowRecord{FlowID: update.FlowID}, nil
	}}}
	if err := m.releaseDurableUntrackedOwner("flow-1", "launch-1"); err == nil {
		t.Fatal("initial release error = nil")
	}
	if got := m.pendingUntrackedOwnerReleases["flow-1"]; got != "launch-1" {
		t.Fatalf("pending release = %q", got)
	}
	scheduled, tickCmd := m.scheduleDurableUntrackedOwnerReleaseRetry()
	if tickCmd == nil || calls != 1 {
		t.Fatalf("schedule performed store I/O: tick=%v calls=%d", tickCmd != nil, calls)
	}
	m = scheduled
	result := m.durableUntrackedOwnerReleaseRetryCmd()().(untrackedOwnerReleaseRetryResultMsg)
	m = m.handleDurableUntrackedOwnerReleaseRetryResult(result)
	if got := m.pendingUntrackedOwnerReleases["flow-1"]; got != "launch-1" {
		t.Fatalf("pending release after failed retry = %q", got)
	}
	if m.untrackedOwnerReleaseRetryDelay != 2*time.Second {
		t.Fatalf("retry delay = %v, want 2s backoff", m.untrackedOwnerReleaseRetryDelay)
	}
	result = m.durableUntrackedOwnerReleaseRetryCmd()().(untrackedOwnerReleaseRetryResultMsg)
	m = m.handleDurableUntrackedOwnerReleaseRetryResult(result)
	if _, pending := m.pendingUntrackedOwnerReleases["flow-1"]; pending || calls != 3 {
		t.Fatalf("pending=%v calls=%d, want successful third attempt", pending, calls)
	}

	m.pendingUntrackedOwnerReleases["flow-1"] = "launch-new"
	m = m.handleDurableUntrackedOwnerReleaseRetryResult(untrackedOwnerReleaseRetryResultMsg{Results: []untrackedOwnerReleaseRetryResult{{
		FlowID: "flow-1", LaunchID: "launch-old", Err: flowstore.ErrUntrackedOwnerChanged,
	}}})
	if got := m.pendingUntrackedOwnerReleases["flow-1"]; got != "launch-new" {
		t.Fatalf("stale result deleted newer pending release: %q", got)
	}
}

type ownerIdentityRuntime struct {
	state   embeddedterm.State
	pid     int
	socket  string
	session string
}

func (r *ownerIdentityRuntime) VisibleLines(int, int) []string { return nil }
func (r *ownerIdentityRuntime) Write(p []byte) (int, error)    { return len(p), nil }
func (r *ownerIdentityRuntime) Resize(int, int) error          { return nil }
func (r *ownerIdentityRuntime) Terminate() error               { r.state = embeddedterm.StateTerminated; return nil }
func (r *ownerIdentityRuntime) Wait(context.Context) error     { return nil }
func (r *ownerIdentityRuntime) State() embeddedterm.State      { return r.state }
func (r *ownerIdentityRuntime) ProcessID() int                 { return r.pid }
func (r *ownerIdentityRuntime) SocketName() string             { return r.socket }
func (r *ownerIdentityRuntime) SessionName() string            { return r.session }

func TestRealEmbeddedTerminalReportsDurableTransportIdentity(t *testing.T) {
	tmuxRuntime := &ownerIdentityRuntime{state: embeddedterm.StateRunning, socket: "socket-1", session: "session-1"}
	got := (realEmbeddedTerminal{term: tmuxRuntime}).untrackedOwnerTransport()
	if got.Kind != flowstore.UntrackedTransportEmbeddedTmux || got.Socket != "socket-1" || got.Session != "session-1" {
		t.Fatalf("tmux transport = %#v", got)
	}

	directRuntime := &processOnlyRuntime{state: embeddedterm.StateRunning, pid: 4343}
	got = (realEmbeddedTerminal{term: directRuntime}).untrackedOwnerTransport()
	if got.Kind != flowstore.UntrackedTransportDirect || got.PID != 4343 {
		t.Fatalf("direct transport = %#v", got)
	}
}

type processOnlyRuntime struct {
	state embeddedterm.State
	pid   int
}

func (r *processOnlyRuntime) VisibleLines(int, int) []string { return nil }
func (r *processOnlyRuntime) Write(p []byte) (int, error)    { return len(p), nil }
func (r *processOnlyRuntime) Resize(int, int) error          { return nil }
func (r *processOnlyRuntime) Terminate() error               { r.state = embeddedterm.StateTerminated; return nil }
func (r *processOnlyRuntime) Wait(context.Context) error     { return nil }
func (r *processOnlyRuntime) State() embeddedterm.State      { return r.state }
func (r *processOnlyRuntime) ProcessID() int                 { return r.pid }

func TestPrefillTerminationReleasesDurableOwner(t *testing.T) {
	runtime := &ownerIdentityRuntime{state: embeddedterm.StateTerminated}
	releases := 0
	m := Model{
		embeddedTerminalState: embeddedTerminalState{embeddedTerminals: []embeddedTerminalSlot{{
			ID: 1, FlowID: "flow-1", LaunchID: "launch-1", Terminal: realEmbeddedTerminal{term: runtime},
		}}},
		launchSeams: flowLaunchSeams{ReleaseUntrackedOwner: func(update flowstore.UntrackedOwnerRelease) (flowstore.FlowRecord, error) {
			releases++
			if update.FlowID != "flow-1" || update.LaunchID != "launch-1" {
				return flowstore.FlowRecord{}, errors.New("wrong owner")
			}
			return flowstore.FlowRecord{}, nil
		}},
	}
	m = m.dismissEmbeddedTerminalForReason(1, embeddedTerminalRemovalPrefillFailure)
	if releases != 1 || len(m.embeddedTerminals) != 0 {
		t.Fatalf("releases=%d terminals=%d", releases, len(m.embeddedTerminals))
	}
}
