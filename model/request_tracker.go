package model

import "github.com/approachcontrol/approach/ui"

// listFetchState is the in-flight list/diff/create request cluster on Model.
type listFetchState struct {
	diffRequestSeq            uint64
	activeViewRequest         uint64
	activeViewKind            FetchKind
	activeViewMode            ui.Mode
	listRequestSeq            uint64
	worktreeSessionRequestSeq uint64
	activeWorktreeSessionReq  uint64
	inlineWorktreeSessionRepo string
	inlineWorktreeSessionPath string
	pendingInlineSessionRepo  string
	pendingInlineSessionPath  string
	pendingInlineSessionList  uint64
	worktreeCreateReq         requestTracker
	repoCreateReq             requestTracker
	flowCreateReq             requestTracker
	readyBeadFlowCreateReq    requestTracker
	repoRefreshSeq            uint64
	activeRepoRefresh         uint64
	pendingRepoSelection      string
	listRequests              [listRequestSlots]uint64
	listErrors                [listRequestSlots]string
	visibleRepoFetchSeq       uint64
	visibleRepoFetch          visibleRepoFetchState
}

// requestTracker is a single in-flight request ID. next issues the next ID and
// makes it current; clear drops the current ID after it lands; invalidate
// makes any in-flight ID stale without issuing a replacement.
type requestTracker struct {
	seq     uint64
	current uint64
}

func (t *requestTracker) next() uint64 {
	t.seq++
	t.current = t.seq
	return t.current
}

func (t requestTracker) isCurrent(id uint64) bool {
	return id == t.current
}

func (t *requestTracker) clear(id uint64) {
	if id != 0 && id == t.current {
		t.current = 0
	}
}

func (t *requestTracker) invalidate() {
	t.seq++
	t.current = 0
}
