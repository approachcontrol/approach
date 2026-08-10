package model

import "testing"

func TestFlowLaunchLeaseIsTokenOwned(t *testing.T) {
	m := Model{}
	var ok bool
	m, ok = m.acquireFlowLaunchLease("flow-1", "token-a", flowLaunchSourcePhase)
	if !ok {
		t.Fatal("first acquire failed")
	}
	if _, ok := m.acquireFlowLaunchLease("flow-1", "token-b", flowLaunchSourceWorktreeAgent); ok {
		t.Fatal("occupied Flow accepted a second lease")
	}
	m = m.releaseFlowLaunchLease("flow-1", "token-b")
	if lease, ok := m.flowLaunchLease("flow-1"); !ok || lease.Token != "token-a" || lease.Source != flowLaunchSourcePhase {
		t.Fatalf("mismatched release changed lease: %#v, %v", lease, ok)
	}
	m = m.releaseFlowLaunchLease("flow-1", "token-a")
	if _, ok := m.flowLaunchLease("flow-1"); ok {
		t.Fatal("matching release retained lease")
	}
}
