# Spec: Beads View

Status: forward-looking full-v1 draft, partially implemented. The Open tracer,
five visible query/header subviews, sticky group re-entry, arrow navigation, and
per-subview filter/cursor preservation with refetch clamping are shipped. Detail
paging, error classification, Closed cap/count, and `default_view` 10–14 remain
deferred. Companion docs: `architecture.md`
(package map, invariants), `config.md` (config vocabulary), `README.md` (current
key bindings).

> **Scope:** The Solution, User Stories, and Implementation Decisions below
> describe the target full-v1 design. For current shipped behavior, use the
> status above and the linked README/config documentation; any capability named
> as deferred above remains future work even when it appears below.

## Problem Statement

Approach users working in repos tracked with beads (the `bd` dependency-aware
issue tracker) have no visibility into issue state from inside the TUI. Deciding
what to work on next — which beads are ready, which are blocked, what is already
in flight — requires leaving Approach and running `bd` commands in a terminal,
per repo. The TUI already answers "what is the state of this repo's worktrees,
branches, sessions, plans, and Flows"; issue state is the missing piece of that
picture.

## Solution

A new top-level Beads view, structured as a grouped view exactly like the Git
group: keyboard `5` enters Beads at its last-used subview, and five letter-keyed
subviews — `r` ready, `b` blocked, `o` open, `i` in-progress, `c` closed — show
the selected repo's beads one status at a time. Data comes from the `bd` CLI in
JSON mode, fetched asynchronously when the user enters the group or moves the
repo cursor. Repos without beads show a calm "beads not configured" message;
configured repos where `bd` fails show a real error. The view is read-only in
v1, with `enter` paging a bead's detail through the pager.

## User Stories

1. As an Approach user, I want a Beads view for the selected repo, so that I can see issue state without leaving the TUI.
2. As an Approach user, I want a Ready subview showing beads whose blockers are all resolved, so that I can pick actionable work immediately.
3. As an Approach user, I want a Blocked subview, so that I can see what is stuck and why work isn't flowing.
4. As an Approach user, I want an Open subview equivalent to `bd list -s open`, so that I see the complete open backlog including beads that also appear in Ready.
5. As an Approach user, I want an In-Progress subview, so that I can see what is already being worked on before starting something new.
6. As an Approach user, I want a Closed subview of recently closed beads, so that I can confirm recent work landed.
7. As an Approach user, I want keyboard `5` to enter Beads at the subview I last used, so that the group behaves like the Git group I already know.
8. As an Approach user, I want letter keys `r`/`b`/`o`/`i`/`c` to switch beads subviews while the group is active, so that navigation matches the Git group's letter-key pattern.
9. As an Approach user, I want arrow keys to wrap within the beads subviews while inside the group, so that cycling feels the same as cycling git subviews.
10. As an Approach user, I want Beads to join the top-level arrow cycle as a fifth view, so that I can reach it without memorizing its number.
11. As an Approach user, I want each beads subview to remember its cursor position across switches, so that returning to a subview doesn't lose my place.
12. As an Approach user, I want a per-subview pane filter, so that I can narrow long lists the same way I filter git panes.
13. As an Approach user, I want cursors and filters clamped sensibly when a refetch changes the list, so that the view never points at a row that no longer exists.
14. As an Approach user, I want a header row labelling the five subviews with their letters, so that I can see where I am and where I can go, like the Git group header.
15. As an Approach user, I want a repo without a beads database to show "beads not configured", so that absence reads as a normal state rather than an error.
16. As an Approach user without the `bd` binary installed, I want every repo's Beads view to show "beads not configured", so that a missing tool doesn't render as breakage.
17. As an Approach user, I want a configured repo where `bd` fails to show an error message with detail, so that real breakage (corrupt database, `bd` regression) is never dressed up as absence.
18. As an Approach user, I want bead rows to show id, priority, and title (plus assignee when set), so that I can triage at a glance.
19. As an Approach user, I want subviews sorted by priority then id — and Closed by most recent close — so that the most important or most recent beads surface first.
20. As an Approach user, I want the Closed subview capped at the newest N with the truncation visible in the header (e.g. "100 of 1432"), so that mature repos stay fast and the cap is never mistaken for the full history.
21. As an Approach user, I want empty subviews to show a quiet message (e.g. "no ready beads"), so that empty and broken are visually distinct.
22. As an Approach user, I want bead data fetched asynchronously on group entry and repo-cursor change, so that the UI never blocks on a `bd` subprocess.
23. As an Approach user, I want stale-result protection on bead fetches, so that moving quickly across repos never shows one repo's beads under another repo's name.
24. As an Approach user, I want `enter` on a bead to page its full detail (`bd show`) through the pager, so that I can read descriptions and dependencies without leaving the TUI.
25. As an Approach user, I want the Beads view to be read-only, so that browsing can never mutate the tracker by accident.
26. As an Approach user, I want `default_view` numbers 10–14 pinning each beads subview, so that I can start the app directly in any of them, mirroring the git subview numbers.
27. As an Approach user, I want the frozen `default_view` vocabulary (1–9) untouched, so that existing configs keep their meaning.
28. As an Approach user, I want a manual way to refetch beads via the app's existing refresh affordance, so that I can pull in changes an agent made without bouncing the repo cursor.
29. As an agent operator, I want issue state visible next to Sessions, Plans, and Flows, so that I can see what my agents should pick up next and what they have finished.

## Implementation Decisions

- Beads is a second grouped view modeled on the Git group: one top-level entry
  key (`5`), a letter-labelled subview row in the right-pane header, letter keys
  scoped to the active group, last-used-subview memory, arrow wrapping within
  the group, and per-subview cursor/filter preservation. The Git group is the
  behavioral reference for every navigation question not answered here.
- Five new modes (beads ready / blocked / open / in-progress / closed) join the
  mode enum and the per-mode list-fetch descriptor table. The header rendering
  reuses the grouped-selector mechanics (top-level row plus letter row), with
  the extra row's height taken out of the list height as the git panes do.
- Data access is a new beads query package shaped like the git query package: a
  small `Runner` seam that executes `bd` with `--json`, wrapped by a querier;
  parsing is pure functions over captured JSON. Nothing outside this package
  invokes `bd`.
- Ready comes from `bd ready` — the dependency-graph readiness computation stays
  in `bd`, never reimplemented. Open is literally `bd list -s open`, so ready
  beads intentionally appear in both Ready and Open. Blocked, in-progress, and
  closed are plain status queries.
- The Closed query is capped at the newest 100 (a constant in v1). The header
  shows "100 of `<total>`"; the total comes from a separate cheap count source
  (e.g. `bd stats` JSON), resolved at implementation time.
- Configured/not-configured/error classification: missing `bd` binary or a repo
  without a beads database → "beads not configured"; a configured repo where
  `bd` exits nonzero or emits unparseable output → an error state carrying
  detail. The classification is decided in the beads query package and rendered
  by the ui layer.
- `bd` runs in the repo root path from the scanner. `bd` handles its own
  worktree redirection, and the repo scan already excludes worktree checkouts.
- Fetches are asynchronous with the existing request-token stale-result
  protection; fetch triggers are group entry and repo-cursor change, plus the
  app's existing manual refresh affordance. No polling.
- `enter` on a bead pages `bd show <id>` output through the pager with
  stale-result protection, the same pattern as the app's other read-only
  detail views.
- Config: `default_view` gains frozen numbers 10 (ready), 11 (blocked),
  12 (open), 13 (in-progress), 14 (closed), parallel to the git subviews'
  1–5. Numbers 1–9 keep their existing frozen meanings.
- Row format: `<id>  P<n>  <title>`, appending assignee when set. Sections sort
  by priority then id; Closed sorts by most recent close first.

## Testing Decisions

- Good tests here assert external behavior at established seams — what the user
  sees and what messages flow through the update loop — never internal state
  shapes or private helpers.
- Beads query package: pure parse functions tested against captured `bd --json`
  fixture output (including malformed output), and the querier tested through a
  fake `Runner` covering success, nonzero exit, missing binary, and
  missing-database cases to pin the configured/not-configured/error
  classification. Prior art: the git query package's parse tests and fake-runner
  querier tests.
- Model: tests drive the Bubble Tea update loop with key events and typed bead
  result messages — group entry on `5`, letter-key switching, last-used-subview
  memory, arrow wrapping inside the group, top-level cycle membership, cursor
  and filter preservation with clamping, stale-result rejection, `default_view`
  10–14 startup routing, and pager launch on `enter`. Prior art: the git mode
  and list-fetch model tests.
- ui: stateless render tests from a `RenderParams` snapshot — beads header
  letter row, row formatting, the "100 of N" closed header, empty-subview
  messages, and not-configured vs error rendering. Prior art: the git header
  and ui render tests.

## Out of Scope

- Any mutation of beads (create, start, close, reopen, edit, dependency
  changes). The view is strictly read-only in v1.
- Background polling or auto-refresh while the view is visible.
- Reading beads storage files directly, or any fallback path that bypasses the
  `bd` CLI.
- A cross-repo aggregate beads view; the view is always scoped to the selected
  repo.
- Configurability of the closed cap, row format, or sort order.
- Dependency-graph visualization beyond what `bd show` prints in the detail
  pager.
- Initializing beads for a repo from inside the TUI.

## Further Notes

- The letter keys `r` and `b` collide with git subview letters only in name,
  not in behavior: grouped-view letters are scoped to the active group, so no
  global binding changes are needed.
- The Ready ⊂ Open duplication is a deliberate product decision (Open is the
  complete status list), not an implementation accident; don't "fix" it.
- The closed-total count source (`bd stats` vs an uncapped count query) is the
  one open implementation detail; whichever is chosen, the capped list query
  must stay bounded.
- If beads later gains statuses beyond the five shown (e.g. deferred), unknown
  statuses should be ignored by parsing rather than failing, mirroring how the
  config layer ignores unknown keys for version compatibility.
