# wtui Product Roadmap

## Vision

wtui is a multi-repository command center for git worktree workflows. Its job is
to answer "what is happening across my repos and worktrees?" quickly, then make
the common next action safe: inspect, open, fetch, pull, create, clean up, or
hand off to another tool.

The product should stay narrower than a full git client. wtui wins by being the
dashboard and launcher for worktree-heavy development, not by reimplementing
lazygit.

## Current State

The codebase has moved past the original pre-launch roadmap. These capabilities
are already implemented and should be treated as the baseline product:

- Multi-repo scanning under `WORKTREE_ROOT` or `~/dev`, with configurable one-
  or two-level depth, excluding sibling `*-worktrees` directories.
- Eight right-pane modes: worktrees, branches, stashes, history, reflog,
  captured agent sessions, saved agent plans, and task-centric Flows.
- Read-only default posture with explicit destructive mode (`D`) before delete,
  drop, or prune actions are exposed.
- Worktree creation from an existing branch, tag, ref, or new branch name using
  `n`, with new checkouts placed under `<repo>-worktrees/<ref>`.
- Branch creation from the branches pane using `n`, creating from the selected
  branch without checking out or creating a worktree.
- Worktree deletion followed by an optional associated branch deletion prompt.
- Stale worktree pruning, with locked worktrees protected from delete/prune and
  unlock exposed as a separate `u` action.
- Dirty, stale, locked, upstream, ahead/behind, unpushed commit, and
  merged-into-cleanup indicators.
- Fuzzy filtering for both repos and the active item list.
- `less -R` paging for dirty worktree, dirty worktree branch, stash, commit,
  and reflog diffs.
- Fetch and pull actions from eligible worktree/branch contexts.
- tmux/Zellij-aware terminal launching, `$TERMINAL` fallback, macOS Terminal
  fallback, and shell fallback.
- VS Code launching via `code`.
- Coding-agent launches for Codex and Claude Code, including create-then-launch
  from the worktrees view.
- Agent-session capture and browsing for Claude Code and Codex hook payloads,
  with inline worktree-scoped session lists, normalized transcript paging,
  full sessions-view embedded PTY resumes for CLI Codex/Claude sessions,
  `codex-app` deep-link resumes, raw session ID copying, and user-state storage
  outside repos.
- Saved agent plans persisted through the `wtui plan` CLI and browsed in the
  plans pane with per-phase progress, phase expansion, plan-path copying, and
  plan Markdown editing plus editable launch instructions for whole-plan or
  selected-phase agent launch.
- Flow records persisted through the `wtui flow` CLI and started from the
  default flows pane with new-flow creation, ready-phase agent launch, derived
  status, phase progress, linked plan metadata, PR metadata, repo-scoped
  filtering, linked-plan body opening, expanded-phase session resume, and
  recoverable partial-state labels for missing worktree metadata, pending
  session attachment, launch/session mismatch, missing provider session IDs,
  and missing Autoreview PR metadata.
- Cross-platform hash copy for history and reflog entries.
- GoReleaser release configuration, GitHub release workflow, release snapshot
  CI job, version metadata, and Homebrew cask publishing.

## Positioning

| Tool type | Strength | wtui distinction |
|-----------|----------|------------------|
| Full git TUIs | Rich commit, staging, rebase, push, and branch workflows | wtui stays focused on multi-repo worktree visibility and safe launcher actions |
| Single-repo worktree tools | Deep lifecycle management inside one repository | wtui scans the workspace and puts all repos in one dashboard |
| CLI worktree helpers | Fast scripted operations | wtui makes state visible before mutation and gates destructive actions |

The core message is now:

> wtui is a terminal dashboard for managing git worktrees across all your repos,
> with safe cleanup, quick creation, and launcher actions for real development
> sessions.

## Roadmap

### Phase 1: Error Surfacing and Config Foundation

Goal: make failures visible and establish the configuration layer future
features can build on.

| Item | Why it matters | Priority |
|------|----------------|----------|
| Git command error surfacing | Several async list/diff fetchers currently discard git errors. Surface failures in the status bar so empty panes are not confused with command failures. | P0 |
| Config file foundation | Add `~/.config/wtui/config.toml` and load it early enough to support future scan, editor, terminal, provider, and launch settings. | P0 |
| Empty-state clarity | Distinguish "nothing to show" from "command failed" and "repo has no matching filtered results." | P1 |
| Stale async result clarity | Keep ignoring outdated async results, but make current command failures obvious when they belong to the selected repo/mode. | P1 |

### Phase 2: Worktree Lifecycle Depth

Goal: round out lifecycle operations while keeping the destructive boundary
clear.

| Item | Why it matters | Priority |
|------|----------------|----------|
| PR-based worktree creation | Enter a PR number or URL, fetch the head branch, and create a worktree for review. This is a high-value worktree workflow. | P1 |
| Branch creation from branches pane | Complements worktree creation and makes branch-first workflows smoother. | P1 |
| Worktree move/rename | Wrap `git worktree move` for users who need to reorganize generated paths. | P2 |
| Bare repo support | Detect bare repositories and support worktree creation/listing correctly for users with central bare repo workflows. | P2 |
| Bootstrap hooks | Optional per-repo setup script after worktree creation for copying env files, linking dependencies, or running setup commands. | P2 |

### Phase 3: Multi-Repo Operations

Goal: make wtui more than a per-repo switcher while preserving safety and
clarity.

| Item | Why it matters | Priority |
|------|----------------|----------|
| Fetch all visible repos | A natural extension of the current fetch action and repo filtering. Must report progress and failures clearly. | P1 |
| Prune stale worktrees across visible repos | Useful cleanup operation, but should require destructive mode and a confirmation summary. | P1 |
| Batch selection | Enables scoped multi-repo actions without applying commands to the whole workspace accidentally. | P2 |
| Merged branch cleanup queue | The code already marks merged branches. A queue view could group safe cleanup candidates across repos. | P2 |
| Operation log | Multi-repo commands need a compact way to review successes, failures, and skipped items. | P2 |

### Phase 4: External Signals

Goal: add context that helps users decide which worktree needs attention.

| Item | Why it matters | Priority |
|------|----------------|----------|
| CI status per branch/worktree | Pass/fail/running indicators would make the dashboard more actionable for active branches. | P2 |
| GitHub/GitLab provider config | Needed for PR creation, PR detection, and CI lookup without hardcoding one provider. | P2 |
| Worktree notes, labels, or tags | Useful once users have many parallel branches, especially for review or agent-driven work. | P3 |
| Agent session follow-ups | Add open-native-transcript actions and richer status when provider signals are reliable. | P3 |
| Flow follow-ups | Add transcript shortcuts from expanded phase rows, manual recovery/retry actions for partial Flow states, and richer phase/session status once provider signals are reliable. | P3 |
| Desktop notifications | Notify on long-running fetch/pull/batch completion only after the operation model is mature. | P3 |

### Phase 5: Release and Community Hygiene

Goal: make the project easier to release, verify, and maintain once the product
surface is stronger.

| Item | Why it matters | Priority |
|------|----------------|----------|
| First tagged release validation | The release pipeline exists; exercise a real `v0.1.0` tag and verify GitHub artifacts, checksums, Homebrew cask, and `go install`. | P1 |
| README/demo recording | A short terminal recording should show scan, filter, create worktree, diff, terminal launch, and cleanup. | P1 |
| Release notes and changelog habit | GoReleaser can generate release notes, but the project needs a predictable user-facing cadence for fixes and feature drops. | P2 |
| Issue templates and contribution notes | Helps shape incoming reports around repo layout, OS, git version, terminal/multiplexer, and reproduction steps. | P2 |

### Phase 6: Configuration and Personal Workflow

Goal: let users adapt wtui without changing source or environment wrappers.

| Item | Why it matters | Priority |
|------|----------------|----------|
| Multiple scan roots | Current scanning accepts one root. Many developers split work across `~/dev`, client folders, and sandbox folders. | P1 |
| Custom code-open command | `c` is currently hardcoded to `code`. Support Cursor, Zed, Neovim terminals, or arbitrary commands through config/env. | P1 |
| Additional scan exclude controls | Preserve zero-config scan-depth defaults while letting large workspaces avoid noisy directories beyond sibling `*-worktrees`. | P2 |
| Persisted UI defaults | Remember default mode and possibly last selected repo without making the app feel stateful in surprising ways. | P3 |

## Distribution Strategy

The critical distribution path is now mostly implemented:

1. GitHub Releases via GoReleaser for macOS and Linux on amd64/arm64.
2. Homebrew cask published to `brian-bell/homebrew-tap` for macOS users.
3. `go install github.com/brian-bell/wtui/cmd/wtui@latest` support through the
   module path and tagged releases.

Next distribution work should be ordered by user demand:

| Channel | Priority | Notes |
|---------|----------|-------|
| Homebrew cask | P0 | Keep this as the primary macOS install path. |
| GitHub Releases | P0 | Verify artifacts and checksums on every release. |
| `go install` | P0 | Keep documented as the source-friendly fallback. |
| AUR | P2 | Good fit for terminal-heavy Linux users once the first release proves stable. |
| Nix | P2 | Consider a flake or nixpkgs path after release cadence settles. |
| Scoop/Windows | P3 | Current GoReleaser config does not build Windows artifacts; add only after validating terminal/editor behavior on Windows. |

## Growth Plan

Launch should wait until the first release has been verified and the README has
a short demo recording. The best launch story is concrete and workflow-focused:

- "Manage git worktrees across all your repos from one terminal UI."
- "Create a worktree, inspect dirty state, open a tmux/Zellij session, and clean
  it up safely."
- "Read-only by default, destructive actions only after an explicit mode switch."

Useful launch surfaces:

- Hacker News "Show HN" post with the recording.
- r/commandline, r/git, and r/golang posts with a concise workflow example.
- A short blog post about why multi-repo worktree state is different from
  single-repo git management.
- Terminal tool directories after the release is installable.

## Scope Discipline

wtui should keep saying no to general git-client work unless it directly serves
worktree management across repositories. Commit creation, staging, rebasing,
conflict resolution, and push workflows belong in dedicated git clients. wtui
should make those workflows easier to reach by opening the right worktree in the
right terminal, editor, or companion tool.

Good roadmap questions:

- Does this help users understand worktree state across repos?
- Does this make common worktree lifecycle actions safer or faster?
- Does this preserve the read-only default and destructive-mode boundary?
- Can this be represented clearly in the two-pane dashboard without turning wtui
  into a full git client?
