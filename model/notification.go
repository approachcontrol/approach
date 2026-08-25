package model

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/internal/artifacts"
)

const notificationMessageMaxRunes = 180

type notificationEvent struct {
	subject string
	context string
	result  string
	joiner  string
}

type tmuxNotificationWatch struct {
	LaunchID string
	RepoPath string
	Provider string
}

func flowPhaseNotification(phaseTitle, flowTitle, result string) notificationEvent {
	return notificationEvent{subject: phaseTitle, context: flowTitle, result: result, joiner: "for"}
}

func flowPhaseNotificationEvents(previous, current []flowstore.FlowRecord) []notificationEvent {
	previousFlows := make(map[string]flowstore.FlowRecord, len(previous))
	for _, record := range previous {
		if record.FlowID != "" {
			previousFlows[record.FlowID] = record
		}
	}
	type keyedEvent struct {
		key   string
		event notificationEvent
	}
	var found []keyedEvent
	for _, record := range current {
		prior, ok := previousFlows[record.FlowID]
		if !ok {
			continue
		}
		priorPhases := make(map[string]flowstore.FlowPhase, len(prior.Phases))
		for _, phase := range prior.Phases {
			if id := artifacts.NormalizePhaseID(phase.PhaseID); id != "" {
				priorPhases[id] = phase
			}
		}
		for _, phase := range record.Phases {
			phaseID := artifacts.NormalizePhaseID(phase.PhaseID)
			priorPhase, ok := priorPhases[phaseID]
			if !ok || priorPhase.Status == phase.Status {
				continue
			}
			result, ok := notificationPhaseResult(phase.Status)
			if !ok {
				continue
			}
			phaseTitle := strings.TrimSpace(phase.Title)
			if phaseTitle == "" {
				phaseTitle = phase.PhaseID
			}
			flowTitle := strings.TrimSpace(record.Title)
			if flowTitle == "" {
				flowTitle = record.FlowID
			}
			found = append(found, keyedEvent{
				key:   record.FlowID + "\x00" + phaseID,
				event: flowPhaseNotification(phaseTitle, flowTitle, result),
			})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].key < found[j].key })
	events := make([]notificationEvent, len(found))
	for i := range found {
		events[i] = found[i].event
	}
	return events
}

func notificationPhaseResult(status flowstore.PhaseStatus) (string, bool) {
	switch status {
	case flowstore.PhaseCompleted:
		return "completed", true
	case flowstore.PhaseBlocked:
		return "blocked", true
	case flowstore.PhaseNeedsAttention:
		return "needs attention", true
	default:
		return "", false
	}
}

func agentExitNotification(provider, repoPath, state string, code int, codeKnown bool) notificationEvent {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "agent"
	}
	result := "finished"
	if state == "failed" || codeKnown && code != 0 {
		result = "failed"
		if codeKnown && code != 0 {
			result = fmt.Sprintf("failed (exit %d)", code)
		}
	}
	repoName := "repository"
	if strings.TrimSpace(repoPath) != "" {
		repoName = filepath.Base(filepath.Clean(repoPath))
	}
	return notificationEvent{
		subject: provider + " session",
		context: repoName,
		result:  result,
		joiner:  "in",
	}
}

func (m Model) notificationCmd(event notificationEvent) tea.Cmd {
	if !m.notificationsEnabled {
		return nil
	}
	sequence := osc9NotificationSequence(notificationMessage(event))
	if sequence == "" {
		return nil
	}
	if m.insideTmux != nil && m.insideTmux() {
		sequence = tmuxPassthroughSequence(sequence)
	}
	return tea.Raw(sequence)
}

func notificationMessage(event notificationEvent) string {
	parts := []string{strings.TrimSpace(event.subject), strings.TrimSpace(event.result)}
	if context := strings.TrimSpace(event.context); context != "" {
		parts = append(parts, strings.TrimSpace(event.joiner), context)
	}
	body := strings.TrimSpace(strings.Join(parts, " "))
	if body == "" {
		return ""
	}
	return "Approach: " + body
}

func (m Model) collectEmbeddedExitNotifications() (Model, []tea.Cmd) {
	if !m.notificationsEnabled {
		return m, nil
	}
	var cmds []tea.Cmd
	for i, slot := range m.embeddedTerminals {
		if slot.Terminal == nil || slot.NotificationReported {
			continue
		}
		state := slot.Terminal.State()
		if state != "exited" && state != "failed" {
			continue
		}
		code, known := embeddedTerminalExitCode(slot.Terminal)
		m.embeddedTerminals[i].NotificationReported = true
		cmds = append(cmds, m.notificationCmd(agentExitNotification(slot.Provider, slot.RepoPath, state, code, known)))
	}
	return m, cmds
}

func (m Model) withTmuxNotificationWatch(ctx actions.AgentLaunchContext) Model {
	if !m.notificationsEnabled || strings.TrimSpace(ctx.LaunchID) == "" || strings.TrimSpace(ctx.RepoPath) == "" {
		return m
	}
	watches := make(map[string]tmuxNotificationWatch, len(m.tmuxNotificationWatches)+1)
	for id, watch := range m.tmuxNotificationWatches {
		watches[id] = watch
	}
	watches[ctx.LaunchID] = tmuxNotificationWatch{
		LaunchID: ctx.LaunchID,
		RepoPath: ctx.RepoPath,
		Provider: ctx.Command,
	}
	m.tmuxNotificationWatches = watches
	return m
}

func (m Model) withoutTmuxNotificationWatches(exited []tmuxNotificationWatch) Model {
	if len(exited) == 0 {
		return m
	}
	watches := make(map[string]tmuxNotificationWatch, len(m.tmuxNotificationWatches))
	for id, watch := range m.tmuxNotificationWatches {
		watches[id] = watch
	}
	for _, watch := range exited {
		if current, ok := watches[watch.LaunchID]; ok && current == watch {
			delete(watches, watch.LaunchID)
		}
	}
	m.tmuxNotificationWatches = watches
	return m
}

func markTmuxAgentResult(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if result, ok := msg.(AgentResultMsg); ok {
			result.Tmux = true
			return result
		}
		return msg
	}
}

func osc9NotificationSequence(message string) string {
	message = sanitizeNotificationText(message)
	if message == "" {
		return ""
	}
	return "\x1b]9;" + message + "\x07"
}

func tmuxPassthroughSequence(sequence string) string {
	sequence = strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + sequence + "\x1b\\"
}

func sanitizeNotificationText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > notificationMessageMaxRunes {
		text = string(runes[:notificationMessageMaxRunes])
	}
	return text
}
