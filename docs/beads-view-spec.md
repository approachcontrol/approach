# Spec: Beads View

Status: historical PR1 design record. The Open tracer,
five visible query/header subviews, sticky group re-entry, arrow navigation, and
per-subview filter/cursor preservation with refetch clamping, plus
configured/not-configured/error classification are shipped, as are the
newest-100 Closed cap with its plain/truncated header count, read-only detail
paging, and parked-Flow creation (record
plus worktree) from a settled Ready selection. Bead rows now also preserve the
optional `issue_type` and `parent` fields, mark epics, and expand the selected
epic with its direct children. Companion docs:
`architecture.md`
(package map, invariants), `config.md` (config vocabulary), `README.md` (current
key bindings).

> **Historical scope:** The Solution, User Stories, and Implementation Decisions
> below preserve the pre-stacked-pane requirements, including global key `5`,
> the old arrow cycle, and configurable startup-view stories. Those navigation
> and config details were intentionally superseded by the stacked-pane design.
> For current behavior, use `docs/tui-guide.md` and `docs/config.md`.

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
its Beads access and tracker state, with `enter` paging a bead's detail through
the pager. A settled Ready selection also exposes `f` to create an
Approach-owned parked Flow — record, branch, and worktree — without invoking
`bd` or mutating the Bead.

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
25. As an Approach user, I want Beads queries and tracker state to remain read-only, so that browsing or creating a Flow can never mutate the tracker by accident.
26. As an Approach user, I want `default_view` numbers 10–14 pinning each beads subview, so that I can start the app directly in any of them, mirroring the git subview numbers.
27. As an Approach user, I want the frozen `default_view` vocabulary (1–9) untouched, so that existing configs keep their meaning.
28. As an Approach user, I want a manual way to refetch beads via the app's existing refresh affordance, so that I can pull in changes an agent made without bouncing the repo cursor.
29. As an agent operator, I want issue state visible next to Sessions, Plans, and Flows, so that I can see what my agents should pick up next and what they have finished.
30. As an Approach user, I want `f` on a settled visible Ready selection to create a parked Flow with its worktree for that Bead, so that I can carry the durable requirement into the Flow workflow with isolation ready, without launching an agent or changing the Bead.

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
  small `Runner` seam wrapped by a querier. List queries execute `bd` with
  `--json` and use pure parsers over captured JSON; detail returns the raw
  human-readable output of `bd show <id> --readonly`. Nothing outside this
  package invokes `bd`.
- Ready comes from `bd ready` — the dependency-graph readiness computation stays
  in `bd`, never reimplemented. Open is literally `bd list -s open`, so ready
  beads intentionally appear in both Ready and Open. Blocked, in-progress, and
  closed are plain status queries.
- The Closed query is capped at the newest 100 (a constant in v1). The header
  shows the accepted unfiltered row count plainly when complete and
  "100 of `<total>`" when truncated. The bounded source is
  `bd list -s closed --json --limit 100 --sort closed --reverse --readonly`; the
  separate cheap total source is `bd stats --json --no-activity --readonly`,
  parsed from `summary.closed_issues` or the v1
  `data.summary.closed_issues` envelope. A failure from either source discards
  both rows and count.
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
- `enter` on a bead pages `bd show <id> --readonly` output through the pager with
  stale-result protection, the same pattern as the app's other read-only
  detail views.
- `f` is a Ready-only Flow-preparation action. It requires right-pane focus,
  an available settled filtered selection whose Bead has a non-empty trimmed
  ID, a selected repo, and no existing Ready Flow request. It creates a Flow
  titled `<trimmed bead ID>: <trimmed bead title>` with instructions directing
  agents to use `bd show <id>` as the durable source of requirements. Creation
  routes through the same Flow-starter prepare path as the Flows-pane `n` form
  with Plan Now off: the configured preset seeds the phase graph, the
  `flow/<slug>` branch and worktree are created from the repository's current
  HEAD (no base ref is supplied), start metadata is recorded, and the
  repository's bootstrap hook runs. It persists an independent Bead link from
  the selected row (`id` plus optional parent `epic_id`) but supplies no saved
  plan, GitHub Issue, PR, or launch/session metadata and never invokes agent
  launchers or `bd`.
- Ready Flow persistence has its own monotonically increasing request token.
  Duplicate keypresses are ignored while it is active. Cursor-driven repo
  changes invalidate it at the top of `handleRepoSelectionChanged`, before
  either the normal or Active-Flows reset branch; rescan-driven repo changes
  reach it through `resetRightPaneCursors`, which `handleRepoRefreshResult`
  uses on every selection-changing path. Completion handlers release the token
  before checking the repo, so no repo-change route can strand the shortcut.
  Accepted success or failure updates status and refreshes exactly the
  currently visible Flow surface, if any; Beads and other surfaces do not
  refresh.
- The resulting Flow is the same parked Flow the Flows-pane form produces, so
  phase launch runs in the Flow's isolated worktree. On worktree or bootstrap
  failure the persisted record's launchable phases are blocked with the failure
  noted; existing Flow rendering then labels the worktree-less record
  `missing-worktree` / `recover-worktree`. Phase launch never falls back to the
  repository root: it creates the missing worktree first, and refuses with a
  message when it cannot.
- Config: `default_view` gains frozen numbers 10 (ready), 11 (blocked),
  12 (open), 13 (in-progress), 14 (closed), parallel to the git subviews'
  1–5. Numbers 1–9 keep their existing frozen meanings.
- Row format: `<id>  P<n>  <title>`, appending assignee when set. Sections sort
  by priority then id; Closed sorts by most recent close first.
- `issue_type` and `parent` are optional display metadata. Older `bd` JSON that
  omits either field remains valid. An `issue_type` equal to `epic` after
  trimming and case folding adds an `[epic]` row marker in every subview.
- Selecting an epic starts one deferred, selection-scoped expansion request.
  Direct children come from exactly
  `bd children <parent-id> --json --readonly`; readiness comes from the same
  uncapped read-only Ready query used by the Ready subview. Ready direct
  children appear first in Ready's priority/natural-ID order. All remaining
  direct children follow in the children query's deterministic
  priority/natural-ID/raw-ID order. Only positively matched children receive a
  `[ready]` marker; absence from Ready does not imply a waiting status.
- The selected parent is followed by one loading, empty, or child-query-error
  row. A successful non-empty query renders child rows and, when readiness could
  not be loaded, one local warning after them. Child and state text uses the
  same terminal sanitization, width bounding, viewport padding, and visual-line
  scrolling as parent rows. A children failure or readiness warning never
  changes the parent list's configured/available state.
- Expansion state is one active snapshot keyed by repository, Beads subview,
  selected epic ID, and a monotonically increasing request token. Cursor or
  filter selection changes, subview switches, repo changes, refresh, and
  leaving Beads clear it synchronously. Results for an old snapshot are
  ignored. The stored top Beads pane owns expansion even while the bottom pane
  has focus; no polling or cross-subview child cache is added.

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
  messages, not-configured vs error rendering, and the Ready `f: new flow`
  hint only when its model predicate is executable. Prior art: the git header
  and ui render tests.

## Out of Scope

- Any mutation of beads (create, start, close, reopen, edit, dependency
  changes). Beads access and tracker state remain strictly read-only; creating
  an Approach-owned Flow artifact is not a Beads mutation.
- Background polling or auto-refresh while the view is visible.
- Reading beads storage files directly, or any fallback path that bypasses the
  `bd` CLI.
- A cross-repo aggregate beads view; the view is always scoped to the selected
  repo.
- Configurability of the closed cap, row format, or sort order.
- Dependency-graph visualization beyond the selected epic's direct-child list
  and positive Ready markers. Full dependency details remain in `bd show`.
- Initializing beads for a repo from inside the TUI.

## Further Notes

- The letter keys `r` and `b` collide with git subview letters only in name,
  not in behavior: grouped-view letters are scoped to the active group, so no
  global binding changes are needed.
- The Ready ⊂ Open duplication is a deliberate product decision (Open is the
  complete status list), not an implementation accident; don't "fix" it.
- The Closed list and stats calls are not an atomic snapshot. The header shows
  `<fetched> of <total>` only when `total > fetched`; otherwise it falls back to
  the plain fetched count so concurrent closes cannot produce `100 of 99`.
- If beads later gains statuses beyond the five shown (e.g. deferred), unknown
  statuses should be ignored by parsing rather than failing, mirroring how the
  config layer ignores unknown keys for version compatibility.
