# wtui Product Roadmap

## Vision

wtui is the **multi-repository command center for git worktree workflows**. Where other tools manage worktrees within a single repo, wtui provides a dashboard across all repositories in a developer's workspace — with a safety-first design that prevents accidental destruction.

### Current Differentiators

- **Multi-repo scanning** — Discovers all git repos under a root directory; no per-repo setup required
- **Five integrated modes** — Worktrees, branches, stashes, history, and reflog in one view (no competitor matches this breadth)
- **Read-only by default** — Destructive mode (`Shift+D`) is an explicit opt-in safety gate unique to wtui
- **Zero configuration** — Single env var (`WORKTREE_ROOT`), no config files, no setup ceremony
- **Charmbracelet stack** — Same Go + Bubble Tea foundation as lazygit, the most popular git TUI

---

## Competitive Landscape

| Tool | Language | Stars | Strengths | Gaps |
|------|----------|-------|-----------|------|
| **lazygit** | Go | ~37k | Full git TUI; worktree panel since 2023 | Worktrees are secondary; no multi-repo view |
| **LazyWorktree** | Go/BubbleTea | ~190 | CI status, AI agent monitoring, tmux support | Single-repo focus; no stash/history/reflog modes |
| **Worktrunk** | Rust | ~4k | Build cache sharing, lifecycle hooks, LLM commit messages | CLI-only (no TUI); single-repo |
| **gwq** | Go | ~380 | Global worktree discovery, fuzzy finder | No branch/stash/history views |
| **wtui** | Go/BubbleTea | — | Multi-repo dashboard, 5 modes, safety gate | No worktree creation, no distribution, no config file |

### Key Takeaway

The worktree tool space is fragmenting into single-repo power tools (LazyWorktree, Worktrunk) vs. git-everything TUIs (lazygit). wtui occupies a unique niche as a **multi-repo worktree dashboard** — the tool you open to get an overview of all your projects and their worktree states. The roadmap should deepen this positioning rather than chase feature parity with single-repo tools.

---

## Feature Roadmap

### Phase 1: Distribution & Discovery

*Get the tool into developers' hands. Zero features matter without distribution.*

| Item | Description | Priority |
|------|-------------|----------|
| **GoReleaser + GitHub Releases** | `.goreleaser.yml` with cross-platform builds, changelogs, checksums. GitHub Actions workflow on `v*` tag push. | P0 |
| **`go install` support** | Ensure `go install github.com/brian-bell/wtui/cmd/wtui@latest` works. Requires proper module path and tagged releases. | P0 |
| **Homebrew tap** | Create `homebrew-wtui` repo. GoReleaser auto-publishes formula on release. `brew install brian-bell/tap/wtui`. | P0 |
| **README terminal recording** | Create a GIF or VHS recording showing the core workflow. The README is the primary marketing surface. | P0 |
| **AUR package** | GoReleaser native PKGBUILD generation. Arch users are disproportionately TUI power users. | P1 |
| **Nix flake** | GoReleaser NUR publishing or standalone flake.nix. Growing developer audience. | P2 |
| **Scoop bucket** | GoReleaser JSON manifest for Windows. | P2 |

### Phase 2: Core Feature Gaps

*Close the most-requested gaps that block adoption.*

| Item | Description | Priority |
|------|-------------|----------|
| **Worktree creation** | `n` key to create a new worktree from a branch, tag, or new branch name. This is the single biggest gap vs. every competitor. | P0 |
| **Combined cleanup** | When deleting a worktree, offer to also delete the associated branch in one operation. Eliminates the most common pain point. | P0 |
| **Fuzzy finder** | Filter repos (left pane) and items (right pane) with `/` to type-ahead search. Essential for developers with 20+ repos. | P1 |
| **Config file** | `~/.config/wtui/config.toml` (or similar) for persistent settings: default mode, custom scan paths, excluded repos, editor command. | P1 |
| **Custom editor command** | Replace hardcoded `code` with a configurable `$EDITOR_CMD` (or config value). Support VS Code, Cursor, Zed, Neovim, etc. | P1 |
| **Branch creation** | Create a new branch from the branches pane. Natural complement to worktree creation. | P2 |
| **Worktree rename/move** | Relocate a worktree directory (`git worktree move`). | P3 |

### Phase 3: Developer Workflow Integration

*Embed wtui into real development workflows.*

| Item | Description | Priority |
|------|-------------|----------|
| **Tmux/multiplexer integration** | `t` key creates (or attaches to) a tmux session named after the worktree, rather than opening a bare Terminal window. Detect tmux/zellij and adapt. | P1 |
| **Bootstrap scripts** | On worktree creation, optionally run a per-repo `.wtui-setup` script (copy .env, symlink node_modules, install deps). Solves the #1 worktree pain point. | P1 |
| **PR-based worktree creation** | Enter a PR number or URL → fetch the branch → create a worktree. Streamlines code review workflows. | P2 |
| **Bare repo workflow** | Detect bare repos and handle them correctly (remote tracking, worktree creation from remote branches). | P2 |
| **Notification on long operations** | Desktop notification when a background git operation completes (e.g., large fetch). | P3 |

### Phase 4: AI-Era & Advanced Features

*Position wtui for the AI-assisted development wave driving worktree adoption.*

| Item | Description | Priority |
|------|-------------|----------|
| **AI agent awareness** | Detect active Claude Code / Cursor / Copilot agent sessions in worktrees. Show status indicator (running/idle/complete). | P1 |
| **CI status per worktree** | Query GitHub Actions (or GitLab CI) status for the branch in each worktree. Show pass/fail/running indicator inline. | P1 |
| **Worktree metadata** | Attach descriptions, tags, or color labels to worktrees. Persisted in `.git/wtui-meta` or similar. Helps organize multi-agent workflows. | P2 |
| **Auto-cleanup policy** | Configurable rules to flag or auto-remove worktrees older than N days, or whose branches have been merged. | P2 |
| **Multi-repo operations** | Select multiple repos and run batch operations (fetch all, prune all stale worktrees). | P3 |
| **Remote worktree dashboard** | SSH into remote machines to list their worktree states. For developers running agents on cloud VMs. | P3 |

---

## Distribution Strategy

### Tier 1 — Launch Essentials (before any marketing)

1. **GoReleaser + GitHub Actions**: Tag-triggered pipeline producing binaries for linux/darwin/windows on amd64/arm64. This is the foundation everything else builds on.
2. **`go install`**: Works automatically with proper module path and semver tags.
3. **Homebrew tap**: `homebrew-wtui` repo with GoReleaser auto-publish. Single command install for macOS/Linux.
4. **README with terminal recording**: Use [VHS](https://github.com/charmbracelet/vhs) (Charm's tape-to-GIF tool) or asciinema. Show: launch → browse repos → switch modes → view diff → create worktree.

### Tier 2 — Expand Reach (after initial traction)

5. **AUR package**: GoReleaser PKGBUILD. Arch users are heavy TUI adopters.
6. **Nix flake / NUR**: Nix community is growing rapidly among developer tool enthusiasts.
7. **Scoop bucket**: Windows developers using Git + terminal workflows.
8. **awesome-tuis list**: Low bar, submit a PR immediately.
9. **Terminal Trove**: Submit at terminaltrove.com for inclusion in their directory.

### Tier 3 — Graduate (after 500+ stars)

10. **Homebrew core**: Requires external submitter, notability proof, and Linux+macOS builds.
11. **awesome-go**: Requires 5+ months history, semver releases, 80%+ test coverage, Go Report Card A grade.
12. **nixpkgs PR**: Official inclusion in the Nix package repository.

---

## Growth & Marketing

### Launch Sequence

Coordinate these within a 48-hour window to maximize GitHub Trending impact (~30-40 stars in 1-2 hours triggers it):

1. **Hacker News "Show HN"** — Post on a weekday morning (Pacific time). Title: "Show HN: wtui – A terminal UI for managing git worktrees across all your repos." Lead with the GIF.
2. **Reddit** — Post to r/commandline (~500k), r/git (~100k), and r/golang (~200k) with GIF demos. Engage every comment.
3. **Dev.to / Hashnode** — Write a "Why I built a worktree dashboard" post explaining the multi-repo problem.

### Sustained Growth

- **Release consistently** — Charm's analysis found that "consistent releases" was the #1 factor in sustained growth. Even small releases keep the project visible in feeds.
- **Beginner-friendly issues** — Label issues with `good first issue`. Lazygit's contributor pipeline grew from this.
- **`FUNDING.yml`** — Set up GitHub Sponsors. Even $0/month, the button signals the project is serious.
- **Changelog** — GoReleaser auto-generates changelogs from conventional commits. Users follow projects with visible momentum.

### Content Ideas

| Topic | Channel | Hook |
|-------|---------|------|
| "Git worktrees are the new tabs" | HN, Reddit | Tie to the AI agent parallel development trend |
| "I manage 30 repos with one TUI" | Dev.to | Concrete workflow walkthrough |
| "The read-only TUI: why wtui defaults to safe" | Blog | Unique angle no competitor has |
| "Worktrees + Claude Code: parallel AI development" | Twitter/X | Ride the AI coding wave |

---

## Long-Term Considerations

### Jujutsu (jj) Compatibility
Jujutsu is a next-generation VCS with native multi-workspace support that may eventually supersede git worktrees. Monitor adoption. If jj reaches critical mass, consider adding a `jj workspace` mode alongside git worktrees.

### Scope Discipline
wtui's strength is being a **dashboard**, not a full git client. Resist the pull to reimplement lazygit. The right question for any feature is: "Does this help a developer manage worktrees across multiple repos?" If the answer involves general git operations (commit, push, rebase), it belongs in lazygit, not here.

### Plugin/Extension System
As feature requests grow, consider a plugin architecture (Lua scripts or Go plugins) rather than building every integration natively. This keeps the core lean while enabling community-driven features like CI providers, editor integrations, and notification systems.
