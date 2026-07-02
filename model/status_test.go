package model

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type recordedStatusTimerEvent struct {
	kind  string
	seq   uint64
	step  int
	delay time.Duration
}

type recordingStatusTimer struct {
	events *[]recordedStatusTimerEvent
}

func (r recordingStatusTimer) Expire(seq uint64, delay time.Duration) tea.Cmd {
	*r.events = append(*r.events, recordedStatusTimerEvent{kind: "expire", seq: seq, delay: delay})
	return func() tea.Msg {
		return StatusExpiredMsg{Seq: seq}
	}
}

func (r recordingStatusTimer) Fade(seq uint64, step int, delay time.Duration) tea.Cmd {
	*r.events = append(*r.events, recordedStatusTimerEvent{kind: "fade", seq: seq, step: step, delay: delay})
	return func() tea.Msg {
		return StatusFadeMsg{Seq: seq, Step: step}
	}
}

func TestStatusTransientExpiryUsesSequence(t *testing.T) {
	var events []recordedStatusTimerEvent
	m := New(nil)
	m.statusTimer = recordingStatusTimer{events: &events}

	m = m.setStatus(statusOther, "failed")
	if m.status.Seq == 0 {
		t.Fatal("status sequence should be non-zero")
	}
	if len(m.pendingStatusCmds) != 1 {
		t.Fatalf("pending status commands = %d, want 1", len(m.pendingStatusCmds))
	}
	if len(events) != 1 || events[0].kind != "expire" || events[0].delay != statusLifetime {
		t.Fatalf("timer events = %#v, want one 3s expiry", events)
	}

	m = m.handleStatusExpired(StatusExpiredMsg{Seq: m.status.Seq + 1})
	if m.status.Text != "failed" {
		t.Fatalf("stale expiry cleared status: %#v", m.status)
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: m.status.Seq})
	if m.status.Text != "" {
		t.Fatalf("matching expiry left status: %#v", m.status)
	}
}

func TestStatusRepeatedTextUsesSequenceForStaleness(t *testing.T) {
	m := New(nil)
	m = m.setStatus(statusOther, "same")
	firstSeq := m.status.Seq
	m = m.setStatus(statusOther, "same")
	secondSeq := m.status.Seq

	if firstSeq == secondSeq {
		t.Fatal("repeated text should still create a new sequence")
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: firstSeq})
	if m.status.Text != "same" || m.status.Seq != secondSeq {
		t.Fatalf("first expiry cleared newer identical status: %#v", m.status)
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: secondSeq})
	if m.status.Text != "" {
		t.Fatalf("second expiry did not clear status: %#v", m.status)
	}
}

func TestVisibleRepoSummaryStatusSchedulesFadeAndExpiry(t *testing.T) {
	var events []recordedStatusTimerEvent
	m := New(nil)
	m.statusTimer = recordingStatusTimer{events: &events}

	m = m.setVisibleRepoFetchSummaryStatus("Fetched 3 visible repos")
	if m.status.Source != statusVisibleRepoFetchSummary {
		t.Fatalf("status source = %v, want visible repo summary", m.status.Source)
	}
	if len(events) != 3 {
		t.Fatalf("timer events = %#v, want 3", events)
	}
	want := []recordedStatusTimerEvent{
		{kind: "fade", seq: m.status.Seq, step: 1, delay: time.Second},
		{kind: "fade", seq: m.status.Seq, step: 2, delay: 2 * time.Second},
		{kind: "expire", seq: m.status.Seq, delay: statusLifetime},
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %#v, want %#v", i, events[i], want[i])
		}
	}

	m = m.handleStatusFade(StatusFadeMsg{Seq: m.status.Seq, Step: 1})
	if m.status.FadeStep != 1 {
		t.Fatalf("fade step = %d, want 1", m.status.FadeStep)
	}
}

func TestAutoAdvanceStatusDoesNotStompForegroundOrAdvanceSequence(t *testing.T) {
	m := New(nil)
	m = m.setStatus(statusOther, "keep")
	seq := m.statusSeq

	next, cmd := m.setAutoAdvanceStatus("Flow Alpha: ready to merge")
	if cmd != nil {
		t.Fatal("ignored auto-advance status should not schedule expiry")
	}
	if next.status.Text != "keep" || next.statusSeq != seq {
		t.Fatalf("status = %#v seq=%d, want foreground preserved at seq %d", next.status, next.statusSeq, seq)
	}
}

func TestOrdinaryStatusExpiresWhileVisibleFetchProgressOwnsDisplay(t *testing.T) {
	m := New(nil)
	m.visibleRepoFetch = visibleRepoFetchState{Request: 3, Total: 2, Completed: 0}
	m = m.setStatus(statusOther, "hidden")
	seq := m.status.Seq

	if got := m.visibleStatusText(); got != "Fetching 0/2 visible repos..." {
		t.Fatalf("visible status = %q, want fetch progress", got)
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: seq})
	if m.status.Text != "" {
		t.Fatalf("hidden status after expiry = %#v, want cleared", m.status)
	}
	if got := m.visibleStatusText(); got != "Fetching 0/2 visible repos..." {
		t.Fatalf("visible status after hidden expiry = %q, want fetch progress", got)
	}
}

func TestForegroundStatusReplacesAutoAdvanceStatus(t *testing.T) {
	m := New(nil)
	var cmd tea.Cmd
	m, cmd = m.setAutoAdvanceStatus("Flow Alpha: ready to merge")
	if cmd == nil || m.status.Source != statusFlowAutoAdvance {
		t.Fatalf("auto status = %#v cmd=%v, want auto status and expiry", m.status, cmd)
	}

	m = m.setStatus(statusOther, "foreground")
	if m.status.Source != statusOther || m.status.Text != "foreground" {
		t.Fatalf("status = %#v, want foreground replacement", m.status)
	}
}

func TestSourceSpecificClearDoesNotClearNewerDifferentSource(t *testing.T) {
	m := New(nil)
	m = m.setStatus(statusGitMutation, "git failed")
	m = m.setStatus(statusOther, "foreground")
	m = m.clearStatus(statusGitMutation)

	if m.status.Source != statusOther || m.status.Text != "foreground" {
		t.Fatalf("status = %#v, want newer foreground preserved", m.status)
	}
}

func TestSourceSpecificClearEmptyStatusDoesNotAdvanceSequence(t *testing.T) {
	m := New(nil)
	seq := m.statusSeq

	m = m.clearStatus(statusGitMutation)

	if m.statusSeq != seq {
		t.Fatalf("status sequence = %d, want unchanged %d", m.statusSeq, seq)
	}
}

func TestFetchStatusPreservesMetadataAndExpires(t *testing.T) {
	m := New(nil)
	m = m.setFetchStatus(FetchErrorMsg{Err: "fetch failed", Kind: FetchList, Mode: 2})
	seq := m.status.Seq

	if m.status.Source != statusFetch || m.status.FetchKind != FetchList || m.status.Mode != 2 {
		t.Fatalf("fetch status = %#v, want metadata preserved", m.status)
	}
	m = m.handleStatusExpired(StatusExpiredMsg{Seq: seq})
	if m.status.Text != "" {
		t.Fatalf("fetch status after expiry = %#v, want cleared", m.status)
	}
}

func TestStatusCommandDrainsFromUpdate(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(OpenURLResultMsg{Label: "Opened PR #1"})
	if cmd == nil {
		t.Fatal("status-setting update should return expiry command")
	}
	modelNext := next.(Model)
	if modelNext.status.Text != "Opened PR #1" {
		t.Fatalf("status = %#v, want Opened PR #1", modelNext.status)
	}
}
