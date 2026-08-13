package model

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/actions"
	"github.com/approachcontrol/approach/agent"
	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/gitquery"
	"github.com/approachcontrol/approach/scanner"
	"github.com/approachcontrol/approach/sessions"
	"github.com/approachcontrol/approach/ui"
)

// --- Fetch commands ---

const visibleRepoFetchFailureNameLimit = 3

func (m Model) startFetchForMode() (Model, tea.Cmd) {
	if m.activeFlowSurfaceVisible() {
		return m.startActiveFlowsFetchWithRefreshTick()
	}
	return m.startFetchMode(m.focusedMode())
}

// fetchStoredModes starts the already-tokened repository-scoped requests for
// both stable content panes. It is used by Init, where the model cannot retain
// newly allocated request tokens after returning the command.
func (m Model) fetchStoredModes() tea.Cmd {
	return batchNonNil(
		m.fetchMode(m.topMode, m.currentListRequest(m.topMode)),
		m.fetchMode(m.bottomMode, m.currentListRequest(m.bottomMode)),
	)
}

// startStoredModeFetches allocates independent request tokens and starts one
// repository-scoped request for each stable pane.
func (m Model) startStoredModeFetches() (Model, tea.Cmd) {
	if _, ok := m.currentRepoPath(); !ok {
		return m, nil
	}
	var topCmd, bottomCmd tea.Cmd
	m, topCmd = m.startFetchMode(m.topMode)
	m, bottomCmd = m.startFetchMode(m.bottomMode)
	return m, batchNonNil(topCmd, bottomCmd)
}

func (m Model) startGlobalRefresh() (Model, tea.Cmd) {
	if m.scanRepos == nil {
		m = m.setStatus(statusOther, "refresh unavailable")
		return m, nil
	}

	m.repoRefreshSeq++
	request := m.repoRefreshSeq
	m.activeRepoRefresh = request

	scanRepos := m.scanRepos
	scanCmd := func() tea.Msg {
		repos, err := scanRepos()
		if err != nil {
			return RepoRefreshFailedMsg{Request: request, Err: err.Error()}
		}
		return RepoRefreshResultMsg{Request: request, Repos: repos}
	}

	cmds := []tea.Cmd{scanCmd}
	if repoPath, ok := m.currentRepoPath(); ok {
		inlineWorktreePath := ""
		if m.topMode == ui.ModeWorktrees && m.inlineWorktreeSessionPath != "" {
			inlineWorktreePath = m.inlineWorktreeSessionPath
		}
		var storedCmd tea.Cmd
		m, storedCmd = m.startStoredModeFetches()
		if inlineWorktreePath != "" && storedCmd != nil {
			m.pendingInlineSessionRepo = repoPath
			m.pendingInlineSessionPath = inlineWorktreePath
			m.pendingInlineSessionList = m.currentListRequest(ui.ModeWorktrees)
		}
		if storedCmd != nil {
			cmds = append(cmds, storedCmd)
		}
	}
	if m.activeFlowSurfaceVisible() {
		var activeCmd tea.Cmd
		m, activeCmd = m.startActiveFlowsFetchWithRefreshTick()
		if activeCmd != nil {
			cmds = append(cmds, activeCmd)
		}
	}
	if len(cmds) == 1 {
		return m, scanCmd
	}
	return m, tea.Batch(cmds...)
}

func (m Model) fetchForMode() tea.Cmd {
	if m.activeFlowSurfaceVisible() {
		return m.fetchActiveFlows(m.currentListRequest(ui.ModeActiveFlows))
	}
	mode := m.activeContentFetchMode()
	return m.fetchMode(mode, m.currentListRequest(mode))
}

func (m Model) currentListRequest(mode ui.Mode) uint64 {
	if int(mode) < 0 || int(mode) >= len(m.listRequests) {
		return 0
	}
	return m.listRequests[int(mode)]
}

func (m Model) nextListFetchRequest(mode ui.Mode) (Model, uint64) {
	m.listRequestSeq++
	request := m.listRequestSeq
	if int(mode) >= 0 && int(mode) < len(m.listRequests) {
		m.listRequests[int(mode)] = request
		m = m.setCurrentListError(mode, "")
	}
	return m, request
}

func (m Model) currentListError(mode ui.Mode) string {
	if int(mode) < 0 || int(mode) >= len(m.listErrors) {
		return ""
	}
	return m.listErrors[int(mode)]
}

func (m Model) setCurrentListError(mode ui.Mode, detail string) Model {
	if int(mode) < 0 || int(mode) >= len(m.listErrors) {
		return m
	}
	if m.listErrors[int(mode)] == detail {
		return m
	}
	warningRows := m.paneCachedWarningRows(mode)
	m.listErrors[int(mode)] = detail
	if m.paneCachedWarningRows(mode) != warningRows {
		// Showing or clearing the cached-data banner resizes the list, so the
		// pane has to re-clamp its scroll against the new viewport.
		m = m.reflowMode(mode)
	}
	return m
}

func (m Model) reflowMode(mode ui.Mode) Model {
	switch mode {
	case ui.ModeActiveFlows:
		return m.reflowActiveFlows()
	case ui.ModeFlows:
		return m.reflowFlows()
	case ui.ModeWorktrees:
		return m.reflowWorktrees()
	case ui.ModeBranches:
		return m.reflowBranches()
	case ui.ModeStashes:
		return m.reflowStashes()
	case ui.ModeHistory:
		return m.reflowCommits()
	case ui.ModeReflog:
		return m.reflowReflogs()
	case ui.ModeSessions:
		return m.reflowSessions()
	case ui.ModePlans:
		return m.reflowPlans()
	default:
		if ui.IsBeadsMode(mode) {
			return m.reflowBeads(mode)
		}
		return m
	}
}

func (m Model) invalidateListRequests() Model {
	m.listRequestSeq++
	for i := range m.listRequests {
		m.listRequests[i] = m.listRequestSeq
		m = m.setCurrentListError(ui.Mode(i), "")
	}
	return m
}

func (m Model) invalidateStoredListRequests() Model {
	m.listRequestSeq++
	for mode := ui.ModeWorktrees; mode <= ui.ModeBeadsClosed; mode++ {
		if mode == ui.ModeActiveFlows {
			continue
		}
		m.listRequests[int(mode)] = m.listRequestSeq
		m = m.setCurrentListError(mode, "")
	}
	return m
}

func (m Model) invalidateBeadsListRequests() Model {
	m.listRequestSeq++
	for mode := ui.ModeBeadsReady; mode <= ui.ModeBeadsClosed; mode++ {
		m.listRequests[int(mode)] = m.listRequestSeq
	}
	return m
}

func (m Model) nextWorktreeSessionRequest(repoPath, worktreePath string) (Model, uint64) {
	m.worktreeSessionRequestSeq++
	m.activeWorktreeSessionReq = m.worktreeSessionRequestSeq
	m.inlineWorktreeSessionRepo = repoPath
	m.inlineWorktreeSessionPath = worktreePath
	m.worktreeSessions = newSessionPane()
	return m, m.activeWorktreeSessionReq
}

func (m Model) isCurrentWorktreeSessionRequest(msg WorktreeSessionResultMsg) bool {
	return msg.Request != 0 &&
		msg.Request == m.activeWorktreeSessionReq &&
		msg.RepoPath == m.inlineWorktreeSessionRepo &&
		msg.WorktreePath == m.inlineWorktreeSessionPath
}

func (m Model) pendingInlineSessionRefresh(repoPath string, listRequest uint64) (string, bool) {
	if m.pendingInlineSessionRepo != repoPath || m.pendingInlineSessionList != listRequest {
		return "", false
	}
	return m.pendingInlineSessionPath, m.pendingInlineSessionPath != ""
}

func (m Model) nextWorktreeCreateRequest() (Model, uint64) {
	m.worktreeCreateSeq++
	m.activeWorktreeCreate = m.worktreeCreateSeq
	return m, m.activeWorktreeCreate
}

func (m Model) isCurrentWorktreeCreateRequest(request uint64) bool {
	return request == m.activeWorktreeCreate
}

func (m Model) clearWorktreeCreateRequest(request uint64) Model {
	if request != 0 && request == m.activeWorktreeCreate {
		m.activeWorktreeCreate = 0
	}
	return m
}

func (m Model) nextRepoCreateRequest() (Model, uint64) {
	m.repoCreateSeq++
	m.activeRepoCreate = m.repoCreateSeq
	return m, m.activeRepoCreate
}

func (m Model) isCurrentRepoCreateRequest(request uint64) bool {
	return request != 0 && request == m.activeRepoCreate
}

func (m Model) clearRepoCreateRequest(request uint64) Model {
	if request != 0 && request == m.activeRepoCreate {
		m.activeRepoCreate = 0
	}
	return m
}

func (m Model) nextFlowCreateRequest() (Model, uint64) {
	m.flowCreateSeq++
	m.activeFlowCreate = m.flowCreateSeq
	return m, m.activeFlowCreate
}

func (m Model) isCurrentFlowCreateRequest(request uint64) bool {
	return request == m.activeFlowCreate
}

func (m Model) clearFlowCreateRequest(request uint64) Model {
	if request != 0 && request == m.activeFlowCreate {
		m.activeFlowCreate = 0
	}
	return m
}

func (m Model) nextReadyBeadFlowCreateRequest() (Model, uint64) {
	m.readyBeadFlowCreateSeq++
	m.activeReadyBeadFlowCreate = m.readyBeadFlowCreateSeq
	return m, m.activeReadyBeadFlowCreate
}

func (m Model) isCurrentReadyBeadFlowCreateRequest(request uint64) bool {
	return request != 0 && request == m.activeReadyBeadFlowCreate
}

func (m Model) clearReadyBeadFlowCreateRequest(request uint64) Model {
	if m.isCurrentReadyBeadFlowCreateRequest(request) {
		m.activeReadyBeadFlowCreate = 0
	}
	return m
}

func (m Model) invalidateReadyBeadFlowCreateRequest() Model {
	m.readyBeadFlowCreateSeq++
	m.activeReadyBeadFlowCreate = 0
	return m
}

// acceptCreationTimeFlowLaunch validates the mutually exclusive request
// namespaces used by Plan Now and Ready-Bead F before either launch path may
// spawn. A valid Ready handoff clears its admission token here; from that point
// the held Flow reservation and launch lifecycle own success or failure.
func (m Model) acceptCreationTimeFlowLaunch(repoPath string, flowCreateRequest, readyBeadRequest uint64, release func()) (Model, bool) {
	if flowCreateRequest != 0 && readyBeadRequest != 0 {
		releaseFlowLaunchReservation(release)
		return m, false
	}
	if flowCreateRequest != 0 {
		if !m.isCurrentRepo(repoPath) || !m.isCurrentFlowCreateRequest(flowCreateRequest) {
			releaseFlowLaunchReservation(release)
			return m, false
		}
		return m.clearFlowCreateRequest(flowCreateRequest), true
	}
	if readyBeadRequest != 0 {
		if !m.isCurrentReadyBeadFlowCreateRequest(readyBeadRequest) {
			releaseFlowLaunchReservation(release)
			return m, false
		}
		m = m.clearReadyBeadFlowCreateRequest(readyBeadRequest)
		if !m.isCurrentRepo(repoPath) {
			releaseFlowLaunchReservation(release)
			return m, false
		}
	}
	return m, true
}

func (m Model) startFetchVisibleRepos() (Model, tea.Cmd) {
	if m.visibleRepoFetch.Request != 0 {
		return m, nil
	}

	repos := m.filteredRepos()
	if len(repos) == 0 {
		m.visibleRepoFetch = visibleRepoFetchState{}
		m = m.setStatus(statusGitMutation, "No visible repos to fetch")
		return m, nil
	}

	m.visibleRepoFetchSeq++
	request := m.visibleRepoFetchSeq
	capturedPaths := make(map[string]struct{}, len(repos))
	cmds := make([]tea.Cmd, 0, len(repos))
	for _, repo := range repos {
		repo := repo
		capturedPaths[repo.Path] = struct{}{}
		cmds = append(cmds, func() tea.Msg {
			errText := ""
			if err := m.fetchRepo(repo.Path); err != nil {
				errText = fmt.Sprintf("fetch failed: %v", err)
			}
			return VisibleRepoFetchResultMsg{
				Request:     request,
				RepoPath:    repo.Path,
				DisplayName: repo.DisplayName,
				Err:         errText,
			}
		})
	}
	m.visibleRepoFetch = visibleRepoFetchState{
		Request:       request,
		Total:         len(repos),
		CapturedPaths: capturedPaths,
	}
	return m, tea.Batch(cmds...)
}

func (m Model) fetchWorktreeSessions(worktreePath string, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || worktreePath == "" {
		return nil
	}
	return func() tea.Msg {
		records, err := m.listSessions(sessions.SessionFilter{RepoPath: repoPath, WorktreePath: worktreePath})
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "worktree sessions",
				Err:          fmt.Sprintf("failed to load worktree sessions: %v", err),
				Kind:         FetchList,
				Mode:         ui.ModeWorktrees,
				ListRequest:  request,
				WorktreePath: worktreePath,
			}
		}
		return WorktreeSessionResultMsg{RepoPath: repoPath, WorktreePath: worktreePath, Sessions: records, Request: request}
	}
}

func (m Model) canFetch() bool {
	if m.activePane == ui.PaneRepos {
		return false
	}
	_, _, ok := m.fetchTargetPath()
	return ok
}

func (m Model) canFetchVisibleRepos() bool {
	return m.activePane == ui.PaneRepos && len(m.filteredRepos()) > 0
}

func (m Model) canCreateRepo() bool {
	return m.activePane == ui.PaneRepos && strings.TrimSpace(m.repoCreateRoot) != ""
}

func (m Model) visibleRepoFetchProgressText() string {
	return fmt.Sprintf("Fetching %d/%d visible %s...", m.visibleRepoFetch.Completed, m.visibleRepoFetch.Total, visibleRepoNoun(m.visibleRepoFetch.Total))
}

func (m Model) visibleRepoFetchFinalStatusText() string {
	total := m.visibleRepoFetch.Total
	if m.visibleRepoFetch.FailureCount == 0 {
		return fmt.Sprintf("Fetched %d visible %s", total, visibleRepoNoun(total))
	}
	failed := strings.Join(m.visibleRepoFetch.FailureNames, ", ")
	remaining := m.visibleRepoFetch.FailureCount - len(m.visibleRepoFetch.FailureNames)
	if remaining > 0 {
		failed = fmt.Sprintf("%s +%d more", failed, remaining)
	}
	return fmt.Sprintf("Fetched %d/%d visible %s; failed: %s", m.visibleRepoFetch.Successes, total, visibleRepoNoun(total), failed)
}

func visibleRepoNoun(count int) string {
	if count == 1 {
		return "repo"
	}
	return "repos"
}

func (m Model) replaceReposPreservingVisibleSelection(repos []scanner.Repo, previousPath string) (Model, bool, bool) {
	m.repos = m.repos.SetItems(repos)
	visible := m.filteredRepos()
	if len(visible) == 0 {
		return m.reflowRepos(), previousPath != "", false
	}
	if previousPath != "" {
		for _, repo := range visible {
			if repo.Path == previousPath {
				m = m.selectFilteredRepo(previousPath)
				return m, false, true
			}
		}
	}
	currentPath, _ := m.currentRepoPath()
	return m.reflowRepos(), previousPath != currentPath, true
}

func (m Model) canPull() bool {
	if m.activePane == ui.PaneRepos {
		return false
	}
	_, _, ok := m.pullTargetPath()
	return ok
}

func (m Model) fetchTargetPath() (string, string, bool) {
	return m.gitTargetPath(false)
}

func (m Model) pullTargetPath() (string, string, bool) {
	return m.gitTargetPath(true)
}

// gitTargetPath returns the repo path and the working directory to run a fetch
// or pull against. When forPull is true the ui.ModeBranches case additionally
// requires the selected branch to have a checked-out worktree, since a bare
// branch has no working tree to pull into.
func (m Model) gitTargetPath(forPull bool) (string, string, bool) {
	repo, ok := m.currentRepo()
	if !ok {
		return "", "", false
	}
	repoPath := repo.Path
	switch m.focusedMode() {
	case ui.ModeWorktrees:
		wt, ok := m.selectedWorktree()
		if !ok {
			if forPull && repo.IsBare {
				return "", "", false
			}
			return repoPath, repoPath, true
		}
		if wt.Stale {
			return "", "", false
		}
		return repoPath, wt.Path, true
	case ui.ModeBranches:
		row, ok := m.selectedRow()
		if !ok {
			if forPull && repo.IsBare {
				return "", "", false
			}
			return repoPath, repoPath, true
		}
		if row.Stale {
			return "", "", false
		}
		if row.WorktreePath != "" {
			return repoPath, row.WorktreePath, true
		}
		if forPull {
			return "", "", false
		}
		return repoPath, repoPath, true
	default:
		return "", "", false
	}
}

func (m Model) createWorktree(input string, launchAgent bool, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreateWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), LaunchAgent: launchAgent, Request: request}
		}
		return m.finishWorktreeCreate(repoPath, worktreePath, input, actions.WorktreeCreateGeneric, launchAgent, request)
	}
}

func (m Model) repoCreate(opts actions.RepoCreateOptions, request uint64) tea.Cmd {
	return func() tea.Msg {
		result, err := m.createRepo(opts)
		if err != nil {
			return RepoCreateFailedMsg{
				Input:        opts.Name,
				CreateGitHub: opts.CreateGitHub,
				Visibility:   opts.Visibility,
				Result:       result,
				Err:          err.Error(),
				Request:      request,
			}
		}
		return RepoCreatedMsg{Name: opts.Name, Result: result, Request: request}
	}
}

func (m Model) createBranch(input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return m.createBranchCommand(repoPath, input, m.selectedBranchStartPoint())
}

func (m Model) createBranchFromStartPoint(input, startPoint string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return m.createBranchCommand(repoPath, input, startPoint)
}

func (m Model) createBranchCommand(repoPath, input, startPoint string) tea.Cmd {
	return func() tea.Msg {
		if err := actions.CreateBranch(repoPath, input, startPoint); err != nil {
			return BranchCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), StartPoint: startPoint}
		}
		return BranchCreatedMsg{RepoPath: repoPath, Name: input}
	}
}

func (m Model) selectedBranchStartPoint() string {
	if m.focusedMode() != ui.ModeBranches {
		return ""
	}
	row, ok := m.selectedRow()
	if !ok || row.Branch.Name == "(detached)" {
		return ""
	}
	if row.Branch.FullRef != "" {
		return row.Branch.FullRef
	}
	return "refs/heads/" + row.Branch.Name
}

func (m Model) createPullRequestWorktree(input string, request uint64) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		worktreePath, err := actions.CreatePullRequestWorktree(repoPath, input)
		if err != nil {
			return WorktreeCreateFailedMsg{RepoPath: repoPath, Input: input, Err: err.Error(), Kind: WorktreeCreatePullRequest, Request: request}
		}
		ref, err := actions.NormalizePullRequestWorktreeRef(input)
		if err != nil {
			ref = input
		}
		return m.finishWorktreeCreate(repoPath, worktreePath, ref, actions.WorktreeCreatePullRequest, false, request)
	}
}

func (m Model) createFlowAndLaunchPlan(title, instructions, baseRef string, headless bool) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return m.createFlowAndLaunchPlanForRepo(repoPath, title, instructions, baseRef, headless)
}

func (m Model) createFlowAndLaunchPlanForRepo(repoPath, title, instructions, baseRef string, headless bool) tea.Cmd {
	command, model, reasoningEffort := m.flowLaunchAgentSettings()
	return func() tea.Msg {
		result, err := m.startFlowPlan(FlowStartRequest{
			RepoPath:                    repoPath,
			Title:                       title,
			Instructions:                instructions,
			BaseRef:                     baseRef,
			AgentCommand:                command,
			Model:                       model,
			ReasoningEffort:             reasoningEffort,
			SessionStateRoot:            m.sessionStateRoot,
			FlowPromptTemplates:         m.flowPromptTemplates,
			FlowPromptTemplatesProvided: true,
			Headless:                    flowHeadlessPointer(headless),
		})
		if err != nil {
			releaseFlowLaunchReservation(result.LaunchRelease)
			return FlowCreateFailedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Err: err.Error()}
		}
		if result.LaunchSkipped {
			releaseFlowLaunchReservation(result.LaunchRelease)
			return FlowCreatedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title}
		}
		return flowPlanLaunchMessage(result.LaunchContext, result.LaunchRelease)
	}
}

func (m Model) createFlowForRepo(repoPath, title, instructions, baseRef string, headless bool) tea.Cmd {
	command, model, reasoningEffort := m.flowLaunchAgentSettings()
	return func() tea.Msg {
		result, err := m.createFlow(FlowStartRequest{
			RepoPath:        repoPath,
			Title:           title,
			Instructions:    instructions,
			BaseRef:         baseRef,
			AgentCommand:    command,
			Model:           model,
			ReasoningEffort: reasoningEffort,
			Headless:        flowHeadlessPointer(headless),
		})
		if err != nil {
			return FlowCreateFailedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Err: err.Error()}
		}
		return FlowCreatedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title}
	}
}

func (m Model) createReadyBeadFlow(repoPath, title, instructions string, request uint64, intent readyBeadFlowIntent) tea.Cmd {
	command, launchModel, reasoningEffort := m.flowLaunchAgentSettings()
	if intent == readyBeadFlowCreateOnly {
		return func() tea.Msg {
			result, err := m.createFlow(FlowStartRequest{
				RepoPath:        repoPath,
				Title:           title,
				Instructions:    instructions,
				AgentCommand:    command,
				Model:           launchModel,
				ReasoningEffort: reasoningEffort,
			})
			if err != nil {
				return ReadyBeadFlowCreateFailedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Err: err.Error(), Request: request}
			}
			return ReadyBeadFlowCreatedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Request: request}
		}
	}
	return func() tea.Msg {
		result, err := m.startFlowPlan(FlowStartRequest{
			RepoPath:                    repoPath,
			Title:                       title,
			Instructions:                instructions,
			AgentCommand:                command,
			Model:                       launchModel,
			ReasoningEffort:             reasoningEffort,
			SessionStateRoot:            m.sessionStateRoot,
			FlowPromptTemplates:         m.flowPromptTemplates,
			FlowPromptTemplatesProvided: true,
			Headless:                    flowHeadlessPointer(true),
		})
		if err != nil {
			releaseFlowLaunchReservation(result.LaunchRelease)
			return ReadyBeadFlowCreateFailedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Err: err.Error(), Request: request}
		}
		if result.LaunchSkipped {
			releaseFlowLaunchReservation(result.LaunchRelease)
			return ReadyBeadFlowCreatedMsg{RepoPath: repoPath, FlowID: result.Flow.FlowID, Title: title, Request: request}
		}
		return readyBeadFlowPlanLaunchMessage(result.LaunchContext, result.LaunchRelease, request)
	}
}

func readyBeadFlowPlanLaunchMessage(ctx actions.AgentLaunchContext, release func(), request uint64) tea.Msg {
	msg := flowPlanLaunchMessage(ctx, release)
	switch msg := msg.(type) {
	case FlowEmbeddedLaunchRequestedMsg:
		msg.ReadyBeadRequest = request
		return msg
	case PlanLaunchRequestedMsg:
		msg.ReadyBeadRequest = request
		return msg
	default:
		return msg
	}
}

// flowPlanLaunchMessage carries the reservation StartPlan is still holding into
// the handler that spawns, which owns releasing it. A non-nil release also
// tells the embedded path the spawn is already reserved, so it must not take
// the same lock a second time.
func flowPlanLaunchMessage(ctx actions.AgentLaunchContext, release func()) tea.Msg {
	switch agent.Normalize(ctx.Command) {
	case agent.CommandCodex, agent.CommandClaude:
		ctx.Embedded = true
		ctx.FlowLaunchTracked = true
		return FlowEmbeddedLaunchRequestedMsg{LaunchContext: ctx, LaunchRelease: release}
	default:
		return PlanLaunchRequestedMsg{LaunchContext: ctx, LaunchRelease: release}
	}
}

func flowHeadlessPointer(value bool) *bool {
	return &value
}

func (m Model) deleteFlowCommand(repoPath, flowID, title string) tea.Cmd {
	return func() tea.Msg {
		if err := m.deleteFlow(flowID); err != nil {
			return FlowDeleteFailedMsg{
				RepoPath: repoPath,
				FlowID:   flowID,
				Title:    title,
				Err:      err.Error(),
				NotFound: flowstore.IsNotFound(err),
			}
		}
		return FlowDeletedMsg{RepoPath: repoPath, FlowID: flowID, Title: title}
	}
}

func (m Model) moveWorktree(oldPath, input string) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		newPath, err := actions.MoveWorktree(repoPath, oldPath, input)
		if err != nil {
			return WorktreeMoveFailedMsg{RepoPath: repoPath, OldPath: oldPath, Input: input, Err: err.Error()}
		}
		return WorktreeMovedMsg{RepoPath: repoPath, OldPath: oldPath, NewPath: newPath}
	}
}

func (m Model) finishWorktreeCreate(repoPath, worktreePath, ref string, kind actions.WorktreeCreateKind, launchAgent bool, request uint64) tea.Msg {
	branch := createdWorktreeBranch(worktreePath, ref, kind)
	hook, ok := m.bootstrapHookForRepo(repoPath)
	if !ok {
		return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Branch: branch, LaunchAgent: launchAgent, Request: request}
	}
	ctx := actions.BootstrapContext{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Ref:          ref,
		Kind:         kind,
	}
	if err := m.runBootstrapHook(ctx, hook); err != nil {
		return WorktreeBootstrapFailedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Err: err.Error(), LaunchAgent: launchAgent, Request: request}
	}
	return WorktreeCreatedMsg{RepoPath: repoPath, WorktreePath: worktreePath, Branch: branch, LaunchAgent: launchAgent, BootstrapRan: true, Request: request}
}

func createdWorktreeBranch(worktreePath, ref string, kind actions.WorktreeCreateKind) string {
	if branch, err := gitquery.CurrentBranch(worktreePath); err == nil {
		return branch
	}
	if kind == actions.WorktreeCreatePullRequest {
		return "pr-" + ref
	}
	return ""
}

func (m Model) fetchBranchDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	row, ok := m.selectedRow()
	if !ok || !row.Branch.Dirty || !row.Branch.IsWorktree {
		return nil
	}
	worktreePath := row.WorktreePath
	if worktreePath == "" {
		worktreePath = repoPath
	}
	branchName := row.Branch.Name
	diffRequest := m.activeViewRequest

	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "branch diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchBranchDiff,
				Mode:         ui.ModeBranches,
				DiffRequest:  diffRequest,
				BranchName:   branchName,
				WorktreePath: worktreePath,
			}
		}
		return BranchDiffResultMsg{
			RepoPath:     repoPath,
			BranchName:   branchName,
			WorktreePath: worktreePath,
			DiffRequest:  diffRequest,
			Diff:         diff,
		}
	}
}

func (m Model) fetchBeadDetail() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	bead, ok := m.selectedVisibleBead()
	if !ok {
		return nil
	}
	mode := m.focusedMode()
	beadID := bead.ID
	diffRequest := m.activeViewRequest
	showBead := m.showBead
	return func() tea.Msg {
		body, err := showBead(repoPath, beadID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "bead detail",
				Err:         fmt.Sprintf("failed to load bead detail: %v", err),
				Kind:        FetchBeadDetail,
				Mode:        mode,
				DiffRequest: diffRequest,
				BeadID:      beadID,
			}
		}
		return BeadDetailResultMsg{
			RepoPath:    repoPath,
			Mode:        mode,
			BeadID:      beadID,
			DiffRequest: diffRequest,
			Body:        body,
		}
	}
}

func (m Model) fetchWorktreeDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	wt, ok := m.selectedWorktree()
	if !ok || !wt.Dirty || wt.Stale {
		return nil
	}
	worktreePath := wt.Path
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		diff, err := gitquery.BranchDiff(worktreePath)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "worktree diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchWorktreeDiff,
				Mode:         ui.ModeWorktrees,
				DiffRequest:  diffRequest,
				WorktreePath: worktreePath,
			}
		}
		return WorktreeDiffResultMsg{
			RepoPath:     repoPath,
			WorktreePath: worktreePath,
			DiffRequest:  diffRequest,
			Diff:         diff,
		}
	}
}

func (m Model) fetchStashDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	stash, ok := m.selectedStash()
	if !ok {
		return nil
	}
	index := stash.Index
	stashDate := stash.Date
	stashMessage := stash.Message
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		diff, err := gitquery.StashDiff(repoPath, index)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:     repoPath,
				Pane:         "stash diff",
				Err:          fmt.Sprintf("failed to load diff: %v", err),
				Kind:         FetchStashDiff,
				Mode:         ui.ModeStashes,
				DiffRequest:  diffRequest,
				StashIndex:   index,
				StashDate:    stashDate,
				StashMessage: stashMessage,
			}
		}
		return StashDiffResultMsg{
			RepoPath:    repoPath,
			Index:       index,
			Date:        stashDate,
			Message:     stashMessage,
			DiffRequest: diffRequest,
			Diff:        diff,
		}
	}
}

func (m Model) fetchReflogDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	entry, ok := m.selectedReflog()
	if !ok {
		return nil
	}
	hash := entry.Hash
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		diff, err := gitquery.ReflogDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "reflog diff",
				Err:         fmt.Sprintf("failed to load diff: %v", err),
				Kind:        FetchReflogDiff,
				Mode:        ui.ModeReflog,
				DiffRequest: diffRequest,
				Hash:        hash,
			}
		}
		return ReflogDiffResultMsg{RepoPath: repoPath, Hash: hash, DiffRequest: diffRequest, Diff: diff}
	}
}

func (m Model) fetchSessionTranscript() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	record, ok := m.selectedSession()
	if !ok {
		return nil
	}
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		events, err := m.readTranscript(record.Provider, record.SessionID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "session transcript",
				Err:         fmt.Sprintf("failed to load transcript: %v", err),
				Kind:        FetchSessionTranscript,
				Mode:        ui.ModeSessions,
				DiffRequest: diffRequest,
				Provider:    record.Provider,
				SessionID:   record.SessionID,
			}
		}
		return SessionTranscriptResultMsg{
			RepoPath:    repoPath,
			Provider:    record.Provider,
			SessionID:   record.SessionID,
			DiffRequest: diffRequest,
			Transcript:  formatTranscript(events),
		}
	}
}

func (m Model) fetchPlanText() tea.Cmd {
	record, ok := m.selectedPlan()
	if !ok {
		return nil
	}
	return m.fetchPlanTextByID(record.PlanID, ui.ModePlans)
}

func (m Model) fetchPlanTextByID(planID string, mode ui.Mode) tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok || planID == "" {
		return nil
	}
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		body, err := m.readPlan(planID)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "plan",
				Err:         fmt.Sprintf("failed to load plan: %v", err),
				Kind:        FetchPlanText,
				Mode:        mode,
				DiffRequest: diffRequest,
				PlanID:      planID,
			}
		}
		return PlanReadResultMsg{
			RepoPath:    repoPath,
			PlanID:      planID,
			Mode:        mode,
			DiffRequest: diffRequest,
			Text:        body,
		}
	}
}

func formatTranscript(events []sessions.TranscriptEvent) string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		prefix := event.Role
		if event.Kind != "" && event.Kind != "message" {
			prefix += " " + event.Kind
		}
		lines = append(lines, fmt.Sprintf("%s: %s", prefix, event.Text))
	}
	return strings.Join(lines, "\n")
}

func (m Model) fetchCommitDiff() tea.Cmd {
	repoPath, ok := m.currentRepoPath()
	if !ok {
		return nil
	}
	commit, ok := m.selectedCommit()
	if !ok {
		return nil
	}
	hash := commit.Hash
	diffRequest := m.activeViewRequest
	return func() tea.Msg {
		diff, err := gitquery.CommitDiff(repoPath, hash)
		if err != nil {
			return FetchErrorMsg{
				RepoPath:    repoPath,
				Pane:        "commit diff",
				Err:         fmt.Sprintf("failed to load diff: %v", err),
				Kind:        FetchCommitDiff,
				Mode:        ui.ModeHistory,
				DiffRequest: diffRequest,
				Hash:        hash,
			}
		}
		return CommitDiffResultMsg{RepoPath: repoPath, Hash: hash, DiffRequest: diffRequest, Diff: diff}
	}
}
