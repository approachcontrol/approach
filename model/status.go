package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/ui"
)

const statusLifetime = 3 * time.Second

type StatusExpiredMsg struct {
	Seq uint64
}

type StatusFadeMsg struct {
	Seq  uint64
	Step int
}

type statusTimerFactory interface {
	Expire(seq uint64, delay time.Duration) tea.Cmd
	Fade(seq uint64, step int, delay time.Duration) tea.Cmd
}

type productionStatusTimer struct{}

func (productionStatusTimer) Expire(seq uint64, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return StatusExpiredMsg{Seq: seq}
	})
}

func (productionStatusTimer) Fade(seq uint64, step int, delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return StatusFadeMsg{Seq: seq, Step: step}
	})
}

func (m Model) currentStatusTimer() statusTimerFactory {
	if m.statusTimer != nil {
		return m.statusTimer
	}
	return productionStatusTimer{}
}

func (m Model) nextStatusSeq() Model {
	m.statusSeq++
	return m
}

func (m Model) setStatusNow(source statusSource, text string) Model {
	m = m.nextStatusSeq()
	m.status = statusError{Text: text, Source: source, Seq: m.statusSeq}
	return m
}

func (m Model) setStatus(source statusSource, text string) Model {
	m = m.setStatusNow(source, text)
	return m.queueStatusExpiry(m.status.Seq)
}

func (m Model) setFetchStatus(msg FetchErrorMsg) Model {
	m = m.nextStatusSeq()
	m.status = statusError{Text: msg.Err, Source: statusFetch, Seq: m.statusSeq, FetchKind: msg.Kind, Mode: msg.Mode}
	return m.queueStatusExpiry(m.status.Seq)
}

func (m Model) setVisibleRepoFetchSummaryStatus(text string) Model {
	m = m.setStatusNow(statusVisibleRepoFetchSummary, text)
	seq := m.status.Seq
	timer := m.currentStatusTimer()
	return m.queueStatusCmds(
		timer.Fade(seq, 1, time.Second),
		timer.Fade(seq, 2, 2*time.Second),
		timer.Expire(seq, statusLifetime),
	)
}

// autoAdvanceStatusRank orders the two kinds of message that share
// statusFlowAutoAdvance. Both are 3 s transients and either may be the newer
// one, so recency cannot separate them.
//
// A transition — needs_attention, merge-ready — is reported on its edge and
// never again; miss it and the user is never told. Launch reporting is emitted
// once per launch attempt and there is always another attempt. So transitions
// outrank launch reporting, while equal ranks still replace each other, which
// is what keeps a launch on one Flow from being hidden by an announcement about
// another that is merely still on screen.
//
// The rank is compared against whatever is live, not against the poll that set
// it, and that is a deliberate divergence from the pre-lifecycle drain. There,
// a transition and a launch message from the same poll were one ordered slice
// and only the last survived, so a transition outranked a launch message from
// its own poll and no other. Here a transition also suppresses a queued
// announcement raised within its 3 s lifetime, and since admission disarms the
// drain that announcement is not retried — the launch still happens and still
// shows in the Flows list, but it is never announced. Scoping the guard to a
// poll generation would remove that, at the cost of threading the generation
// through the intent and both event hops. Losing an announcement is worth less
// than the alert it would otherwise erase, so the coarser rule stands.
// Launch failures are unaffected: both failure paths re-arm the drain, so they
// re-report on the next poll and appear as soon as the transition expires.
type autoAdvanceStatusRank int

const (
	autoAdvanceRankLaunch autoAdvanceStatusRank = iota
	autoAdvanceRankTransition
)

// setAutoAdvanceStatus reports a transition the poll observed. It yields to any
// live status from another source and outranks everything within its own.
func (m Model) setAutoAdvanceStatus(text string) (Model, tea.Cmd) {
	return m.setRankedAutoAdvanceStatus(autoAdvanceRankTransition, text)
}

// setAutoAdvanceLaunchStatus reports what one launch attempt did — queued, or
// failed. It differs from setAutoAdvanceStatus only in yielding to a live
// transition.
//
// Without the rank these two would be ordered by arrival, and the migration
// inverted that: both messages were once set before autoAdvanceStatusEvents ran
// and were overwritten by the poll's transitions, and both now land a hop after
// them. Ordering by arrival alone would let a routine launch on one Flow erase a
// needs_attention raised by another, which is the only prompt the user gets
// that a Flow wants a human.
func (m Model) setAutoAdvanceLaunchStatus(text string) (Model, tea.Cmd) {
	return m.setRankedAutoAdvanceStatus(autoAdvanceRankLaunch, text)
}

func (m Model) setRankedAutoAdvanceStatus(rank autoAdvanceStatusRank, text string) (Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	if m.status.isSet() && m.status.Source != statusFlowAutoAdvance {
		return m, nil
	}
	if m.status.isSet() && m.status.AutoRank > rank {
		return m, nil
	}
	m = m.setStatusNow(statusFlowAutoAdvance, text)
	m.status.AutoRank = rank
	return m, m.currentStatusTimer().Expire(m.status.Seq, statusLifetime)
}

func (m Model) queueStatusExpiry(seq uint64) Model {
	return m.queueStatusCmds(m.currentStatusTimer().Expire(seq, statusLifetime))
}

func (m Model) queueStatusCmds(cmds ...tea.Cmd) Model {
	for _, cmd := range cmds {
		if cmd != nil {
			m.pendingStatusCmds = append(m.pendingStatusCmds, cmd)
		}
	}
	return m
}

func (m Model) drainStatusCmds(cmd tea.Cmd) (Model, tea.Cmd) {
	if len(m.pendingStatusCmds) == 0 {
		return m, cmd
	}
	cmds := append([]tea.Cmd{cmd}, m.pendingStatusCmds...)
	m.pendingStatusCmds = nil
	return m, batchNonNil(cmds...)
}

func (m Model) clearStatus(source statusSource) Model {
	if m.status.isSet() && m.status.Source == source {
		m.status = statusError{}
		m = m.nextStatusSeq()
	}
	return m
}

func (m Model) clearFetchListStatus(mode ui.Mode) Model {
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList && m.status.Mode == mode {
		m.status = statusError{}
		m = m.nextStatusSeq()
	}
	return m
}

func (m Model) clearAnyStatus() Model {
	if m.status.isSet() {
		m.status = statusError{}
		m = m.nextStatusSeq()
	}
	return m
}

func (m Model) handleStatusExpired(msg StatusExpiredMsg) Model {
	if msg.Seq == 0 || msg.Seq != m.status.Seq {
		return m
	}
	if m.status.Source == statusFetch && m.status.FetchKind == FetchList {
		m.status.Text = ""
		m.status.FadeStep = 0
		m = m.nextStatusSeq()
		m.status.Seq = m.statusSeq
		return m
	}
	m.status = statusError{}
	m = m.nextStatusSeq()
	return m
}

func (m Model) handleStatusFade(msg StatusFadeMsg) Model {
	if msg.Seq == 0 || msg.Seq != m.status.Seq {
		return m
	}
	m.status.FadeStep = msg.Step
	return m
}

func (s statusError) isSet() bool {
	return s.Source != statusNone || s.Text != ""
}
