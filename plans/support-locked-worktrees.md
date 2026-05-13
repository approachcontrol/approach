# Plan: Support Locked Worktrees

> Source PRD: https://github.com/brian-bell/wtui/issues/59

## Architectural decisions

Durable decisions that apply across all phases:

- **Modes and routes**: Locked worktree support lives in the existing worktrees mode (`1`). Branches, stashes, history, and reflog modes keep their current key semantics except that `u` is explicitly a no-op outside worktrees mode.
- **Schema**: No persistent schema changes are needed. Lock state is read from `git worktree list --porcelain` each time worktrees are fetched.
- **Key models**: A worktree has `Locked bool` and `LockReason string` fields alongside existing path, branch, stale, main, dirty, and diff summary fields.
- **Git command boundaries**: Listing continues to use `git worktree list --porcelain`. Unlocking uses `git -C <repoPath> worktree unlock <worktreePath>` and returns errors to the model rather than opening a force-retry flow.
- **State precedence**: When a worktree is both locked and stale, locked wins in the worktree pane and status-bar hint logic. The worktree is intentionally protected/offline, not presented as broken.
- **Destructive-mode policy**: Delete stays gated behind destructive mode. Unlock is not gated by destructive mode because it is reversible from any shell and is the explicit way to remove the lock safety rail.
- **Refresh policy**: Successful unlock triggers a worktree refetch for the originating repo and uses the existing stale-result protection pattern so late messages from another repo do not mutate the current view.

---

## Phase 1: Display Locked Worktrees

**User stories**: 1, 2, 3, 4

### What to build

Read lock state and optional lock reasons from git's worktree porcelain output, carry that state through the public worktree data model, and render locked worktrees with a distinct inline indicator in the worktrees pane. Locked and missing worktrees should be shown as locked rather than stale, and lock reasons should appear inline when available.

### Acceptance criteria

- [ ] A worktree with a bare `locked` porcelain line is returned with `Locked == true` and an empty lock reason.
- [ ] A worktree with a `locked <reason>` porcelain line is returned with `Locked == true` and the reason preserved.
- [ ] Worktrees without a lock line remain unlocked with an empty lock reason.
- [ ] Real locked worktrees returned by git include the correct lock state and reason in the app data model.
- [ ] The worktrees pane renders `🔒 locked` for locked worktrees.
- [ ] The worktrees pane includes the lock reason when one exists and truncates it to fit the pane.
- [ ] A locked + stale worktree renders as `🔒 locked`, not `✗ stale`.

---

## Phase 2: Block Locked Deletes

**User stories**: 5, 6

### What to build

Treat locked worktrees as delete-protected at both the interaction and presentation levels. In destructive mode, selecting a locked worktree should not advertise deletion and pressing `d` should do nothing.

### Acceptance criteria

- [ ] In worktrees mode with a locked worktree selected, the status bar does not show `d: delete`.
- [ ] Pressing `d` on a locked worktree in destructive mode leaves the overlay closed and performs no delete command.
- [ ] Pressing `d` on an unlocked, present, non-main worktree in destructive mode still opens the existing remove confirmation.
- [ ] Locked + stale worktrees suppress delete the same way locked present worktrees do.
- [ ] Main worktree delete protection remains unchanged and independent of lock state.

---

## Phase 3: Unlock Happy Path

**User stories**: 7, 8, 9, 10, 11, 12, 13

### What to build

Add the `u` shortcut for locked worktrees in worktrees mode. The shortcut should be discoverable in the status bar, run immediately without a confirmation dialog, work outside destructive mode, refresh the worktree list after success, and no-op when unlocking does not apply.

### Acceptance criteria

- [ ] With a locked worktree selected in worktrees mode, the status bar shows `u: unlock`.
- [ ] Pressing `u` on a locked worktree fires an unlock command without opening a confirmation dialog.
- [ ] Pressing `u` on a locked worktree works when destructive mode is off.
- [ ] Pressing `u` on a locked main worktree fires the unlock command.
- [ ] Pressing `u` on an unlocked worktree is a silent no-op.
- [ ] Pressing `u` outside worktrees mode is a silent no-op.
- [ ] After a successful unlock result for the current repo, the model refetches worktrees.
- [ ] If the selected repo changes before an unlock result arrives, the stale result is ignored.
- [ ] A real locked worktree can be unlocked through the action boundary and no longer appears locked in follow-up git porcelain output.

---

## Phase 4: Unlock Failure Feedback

**User stories**: 14

### What to build

Report unlock failures as transient status-bar errors instead of modal dialogs. The error should take precedence over normal hints briefly, then clear on the next successful action or navigation so the UI returns to normal.

### Acceptance criteria

- [ ] A failed unlock result stores a transient error message on the model.
- [ ] The status bar renders the transient error in the error style instead of normal hints.
- [ ] Unlock failure does not open a confirmation overlay or force-retry prompt.
- [ ] The transient error clears after a subsequent successful action or navigation.
- [ ] A stale unlock failure result for a non-current repo is ignored.
- [ ] The action behavior for trying to unlock an already-unlocked worktree is documented by a test and flows through the same transient-error path when invoked.

---

## Phase 5: Preserve Non-Destructive Interactions

**User stories**: 15, 16

### What to build

Verify that lock state only affects destructive removal and unlock behavior. Locked worktrees should otherwise behave like normal worktree rows for selection, scrolling, dirty diff viewing, terminal opening, and VS Code opening when their directory exists.

### Acceptance criteria

- [ ] Locked worktrees participate normally in up/down navigation and selection.
- [ ] A locked dirty worktree whose directory exists can still open its diff.
- [ ] `t` opens a terminal for a locked worktree whose directory exists.
- [ ] `c` opens VS Code for a locked worktree whose directory exists.
- [ ] Existing stale-worktree restrictions for diff, terminal, and VS Code remain unchanged.
- [ ] Branches mode remains unaffected by locked worktree support.
