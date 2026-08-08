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

func (m Model) setAutoAdvanceStatus(text string) (Model, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return m, nil
	}
	if m.status.isSet() && m.status.Source != statusFlowAutoAdvance {
		return m, nil
	}
	m = m.setStatusNow(statusFlowAutoAdvance, text)
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
