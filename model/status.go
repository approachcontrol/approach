package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/ui"
)

// The transient-status schedule: two fade steps and then expiry. Options may
// override it; tests collapse it so draining a command batch never waits on a
// status timer.
const (
	defaultStatusFadeStep1 = time.Second
	defaultStatusFadeStep2 = 2 * time.Second
	defaultStatusLifetime  = 3 * time.Second
)

// StatusTimings overrides the transient-status fade and expiry schedule. A zero
// field keeps that step at its production delay.
type StatusTimings struct {
	FadeStep1 time.Duration
	FadeStep2 time.Duration
	Lifetime  time.Duration
}

// statusTimings is this Model's schedule, with unset steps filled in from the
// production defaults so a directly constructed Model behaves as before. Read
// the schedule through here rather than off the field.
func (m Model) statusTimings() StatusTimings {
	timings := m.statusSchedule
	if timings.FadeStep1 <= 0 {
		timings.FadeStep1 = defaultStatusFadeStep1
	}
	if timings.FadeStep2 <= 0 {
		timings.FadeStep2 = defaultStatusFadeStep2
	}
	if timings.Lifetime <= 0 {
		timings.Lifetime = defaultStatusLifetime
	}
	return timings
}

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
	timings := m.statusTimings()
	return m.queueStatusCmds(
		timer.Fade(seq, 1, timings.FadeStep1),
		timer.Fade(seq, 2, timings.FadeStep2),
		timer.Expire(seq, timings.Lifetime),
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
// Queued and failed deliberately share one rank rather than splitting into two.
// Ranking failure above queued would invert the rule the rest of this enum is
// built on — a message reported once outranks one that will be back — because
// admission disarms the drain, so a queued announcement is never retried, while
// both failure paths re-arm and re-report every poll. The cost of sharing is
// bounded and self-clearing: an off-repo failure batched alongside a queued
// announcement for a different Flow can land first and be replaced, and the
// re-armed failure replaces it again about a second later. The cost of
// splitting is not: a Flow failing every second would suppress the only
// announcement another Flow ever gets.
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
	return m, m.currentStatusTimer().Expire(m.status.Seq, m.statusTimings().Lifetime)
}

func (m Model) queueStatusExpiry(seq uint64) Model {
	return m.queueStatusCmds(m.currentStatusTimer().Expire(seq, m.statusTimings().Lifetime))
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
