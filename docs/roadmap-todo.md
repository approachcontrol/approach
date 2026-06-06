# wtui Roadmap Todo

This checklist is derived from `docs/product-roadmap.md`. Use it as the
operational tracker for checking off roadmap work as it lands.

## Phase 1: Error Surfacing and Config Foundation

Goal: make failures visible and establish the configuration layer future
features can build on.

- [x] P0 - Git command error surfacing: surface async list/diff fetch failures in the status bar so empty panes are not confused with command failures.
- [x] P0 - Config file foundation: add `~/.config/wtui/config.toml` and load it early enough to support future scan, editor, terminal, provider, and launch settings.
- [x] P1 - Empty-state clarity: distinguish "nothing to show" from "command failed" and "repo has no matching filtered results."
- [x] P1 - Stale async result clarity: keep ignoring outdated async results, but make current command failures obvious when they belong to the selected repo/mode.

## Phase 2: Worktree Lifecycle Depth

Goal: round out lifecycle operations while keeping the destructive boundary
clear.

- [x] P1 - PR-based worktree creation: enter a PR number or URL, fetch the head branch, and create a worktree for review.
- [ ] P1 - Branch creation from branches pane: complement worktree creation and make branch-first workflows smoother.
- [ ] P1 - Coding-agent launch actions: launch Codex or Claude Code from a selected worktree or worktree branch, optionally after creating a new worktree or branch.
- [ ] P2 - Worktree move/rename: wrap `git worktree move` for users who need to reorganize generated paths.
- [ ] P2 - Bare repo support: detect bare repositories and support worktree creation/listing correctly for central bare repo workflows.
- [ ] P2 - Bootstrap hooks: support an optional per-repo setup script after worktree creation for env files, dependency links, or setup commands.

## Phase 3: Multi-Repo Operations

Goal: make wtui more than a per-repo switcher while preserving safety and
clarity.

- [ ] P1 - Fetch all visible repos: extend the current fetch action across filtered repos with clear progress and failure reporting.
- [ ] P1 - Prune stale worktrees across visible repos: require destructive mode and a confirmation summary.
- [ ] P2 - Batch selection: enable scoped multi-repo actions without applying commands to the whole workspace accidentally.
- [ ] P2 - Merged branch cleanup queue: group safe cleanup candidates across repos using the existing merged-branch indicators.
- [ ] P2 - Operation log: provide a compact way to review successes, failures, and skipped items for multi-repo commands.

## Phase 4: External Signals

Goal: add context that helps users decide which worktree needs attention.

- [ ] P2 - CI status per branch/worktree: show pass, fail, and running indicators for active branches.
- [ ] P2 - GitHub/GitLab provider config: support PR creation, PR detection, and CI lookup without hardcoding one provider.
- [ ] P3 - Worktree notes, labels, or tags: help users manage many parallel branches, especially for review or agent-driven work.
- [ ] P3 - AI-agent session awareness: detect active coding-agent sessions in worktrees when reliable signals exist.
- [ ] P3 - Desktop notifications: notify on long-running fetch, pull, or batch completion after the operation model is mature.

## Phase 5: Release and Community Hygiene

Goal: make the project easier to release, verify, and maintain once the product
surface is stronger.

- [ ] P1 - First tagged release validation: exercise a real `v0.1.0` tag and verify GitHub artifacts, checksums, Homebrew cask, and `go install`.
- [ ] P1 - README/demo recording: show scan, filter, create worktree, diff, terminal launch, and cleanup.
- [ ] P2 - Release notes and changelog habit: establish a predictable user-facing cadence for fixes and feature drops.
- [ ] P2 - Issue templates and contribution notes: capture repo layout, OS, git version, terminal/multiplexer, and reproduction steps.

## Phase 6: Configuration and Personal Workflow

Goal: let users adapt wtui without changing source or environment wrappers.

- [ ] P1 - Multiple scan roots: support developers who split work across `~/dev`, client folders, and sandbox folders.
- [ ] P1 - Custom editor command: support Cursor, Zed, Neovim terminals, or arbitrary commands through config/env instead of hardcoding `code`.
- [ ] P2 - Scan-depth/exclude controls: preserve zero-config defaults while letting large workspaces avoid slow or noisy directories.
- [ ] P3 - Persisted UI defaults: remember default mode and possibly last selected repo without surprising statefulness.

## Distribution Follow-Ups

- [ ] P0 - Homebrew cask: keep this as the primary macOS install path.
- [ ] P0 - GitHub Releases: verify artifacts and checksums on every release.
- [ ] P0 - `go install`: keep documented as the source-friendly fallback.
- [ ] P2 - AUR: consider after the first release proves stable.
- [ ] P2 - Nix: consider a flake or nixpkgs path after release cadence settles.
- [ ] P3 - Scoop/Windows: add only after validating terminal/editor behavior on Windows.

## Launch Follow-Ups

- [ ] Record a concrete workflow demo before launch.
- [ ] Prepare a Hacker News "Show HN" post.
- [ ] Prepare r/commandline, r/git, and r/golang posts with a concise workflow example.
- [ ] Write a short blog post about multi-repo worktree state.
- [ ] Submit to terminal tool directories after the release is installable.
