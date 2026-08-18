# Agent Sessions and Hooks

How Approach captures, stores, and resumes coding-agent sessions. The sessions
view UI is documented in `docs/tui-guide.md`; the `[sessions]` config keys and
state-root precedence are in `docs/config.md`.

## Automatic Wiring

CLI agents launched from Approach (worktree `a`/`N`, Flow `g`/`s`/`R`/`U`, or
session resume `r`) are wired automatically: Approach passes Claude Code or Codex a session-end
hook that calls the current Approach binary, and it exports `APPROACH_*` metadata
so hook records can be associated with the repo, worktree, branch, and launch.
The exported metadata includes `APPROACH_AGENT`, `APPROACH_LAUNCH_ID`,
`APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_BRANCH`,
`APPROACH_COMMIT`, the three `APPROACH_*_STATE_ROOT` roots, and plan or flow
IDs, paths, and phase fields when available.

The generic Flow-worktree agent started by `s` exports the exact Flow ID but an
empty Flow phase ID. It is therefore discoverable with the Flow while remaining
phase-untracked: hook ingestion persists the session record but cannot attach it
to any phase's launch or session history. Its retained embedded terminal is the
Flow's in-process owner and cannot be detached; it remains occupancy after the
agent exits until the slot is dismissed. `U` autofix is a separate prompted
intent whose prompt comes from `[flow_prompts].autofix`; its headless/tmux
routing remains unchanged.

All Flow-associated launches enter the Model's one Flow launch lifecycle before
any reservation, Flow write, or process start. That boundary changes no storage
ownership: provider hooks and the session store still own capture and transcript
metadata, the Flow store still owns phase launch/session mirrors, and the
embedded terminal still owns the live process. The lifecycle only coordinates
their ordering and exact-Flow occupancy.

## Manual Hook Setup

For agent sessions that are not launched by Approach, configure Claude Code,
Codex, or Cursor CLI hooks to call Approach:

```bash
approach session-hook --provider claude
approach session-hook --provider codex
approach session-hook --provider cursor-agent
```

For local testing, use `--state-root /tmp/approach-sessions-test`.

Codex may ask you to review and trust the injected hook with `/hooks` before
it runs it. After trust is recorded for the unchanged hook command, later
Approach-launched Codex sessions can save normally.

Claude Code hook example:

```json
{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "approach session-hook --provider claude"
          }
        ]
      }
    ]
  }
}
```

Codex hook example:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "approach session-hook --provider codex"
          }
        ]
      }
    ]
  }
}
```

Cursor CLI has no per-launch hook flag. Approach-launched `cursor-agent`
sessions merge a managed `stop` hook into `~/.cursor/hooks.json` (preserving
other user hooks). The hook no-ops unless `APPROACH_LAUNCH_ID` is set, then
runs `session-hook --provider cursor-agent`. Cursor `stop` payloads use
`conversation_id` as the session ID and record the session as ended.

For Codex hook payloads with `hook_event_name = "Stop"`, Approach records the
session as ended. Claude hook ingestion also records ended sessions, using the
payload end time when present and the current time as a fallback. Every other
hook event records `last_seen`, which is the status a session keeps until
something ends it.

An agent Approach launched itself is also finalized when its terminal exits:
`FinalizeAgentSession` marks the launch ended in the session store and, when
the launch has a phase ID, mirrors that into the Flow phase. Nothing runs it if
Approach is killed first, so the
launch's session stays live indefinitely and blocks its Flow phase. `x` on that
phase in the TUI makes the same call by hand after a confirmation — see
`docs/tui-guide.md` — which is why a released session is indistinguishable from
a cleanly exited one: it is the same write.

A `SessionEnd`-class hook (Claude and Cursor always; Codex on `Stop`) for a
tracked phase launch also reports the end to the launch controller (see
"Launch directories" below): anything the launch spooled is replayed first.
That record is not a death certificate — Codex `Stop` fires per turn and
Claude's `SessionEnd` also fires on `/clear` while the agent keeps running —
so a still-`running` phase is not demoted from the hook alone. The sweep
treats an ended Claude session as evidence only after ten minutes and only
when its end was not a `/clear` — never a Codex or Cursor one, whose `ended`
records are per-turn — and a held Flow lease vetoes demotion either way. The hook reports what it did as a stderr
warning and still exits 0.

`session-hook` loads the normal Approach config, so `[sessions].root` and
`copy_raw_transcripts` apply to hook ingestion. `--state-root` overrides the
configured sessions root for one hook invocation. Raw provider transcript
copies are off by default; set `copy_raw_transcripts = true` to also preserve
provider-native `raw.jsonl` alongside normalized transcript events.

## Storage and Security

Session data is stored under the user state directory by default:
`$XDG_STATE_HOME/approach/sessions/v1`, or
`~/.local/state/approach/sessions/v1` when `XDG_STATE_HOME` is unset. A build
whose version is not a published release tag substitutes `approach-dev` for
`approach` in that path (see `docs/config.md`). Sessions,
plans, and Flows share this artifact root; the full precedence chain
(`--state-root`, `APPROACH_*_STATE_ROOT` variables, `[sessions].root`) is in
`docs/config.md`.

Transcripts may contain secrets or private prompts; Approach keeps them outside
repositories and uses restrictive file permissions for created session files.
Provider session IDs are stored in hashed directory names instead of raw path
components. Hook payloads without a usable session ID are rejected at capture
time, so no unusable session records are stored.

## Launch Directories and Controller-Owned Phase Results

Every Flow-scoped launch has a directory `<root>/launches/<launch-id>/`
(`0700`, files `0600`) beside — deliberately not inside — `bin/pins/`: pins are
content-addressed binary retention, launch directories are a time-based
mutation log, and the two fail for different reasons. The files:

- `launch.json` — identity (`flow_id`, `phase_id`, `kind`: phase, autofix,
  repair, generic, resume) and the SHA-256 of the launch's control token,
  written at registration. The token itself lives only in the agent's
  environment and the launcher's memory.
- `baseline.json` — the phase status `AddPhaseLaunchID` returned for this
  launch (`running` for a fresh launch, the terminal status for a resume of a
  completed phase). Replay compares against it. `observed_updated_at` is
  diagnostic only.
- `requests/<seq:06d>-<request-id>.json` — every write the launch made through
  the `flow` CLI, appended durably (fsync + directory sync) **before** it is
  acknowledged, by the controller (`written_by: controller`), by the CLI's
  direct fallback (`direct`), or by the CLI's spool path (`spool`).
- `applied.json` — the log's high-water mark and the phase status after the
  last applied request; `result` is `applied`, `refused`, `reconciled`, or
  `rejected`.
- `rejected.json` — replay rejections with `reason` `phase_result_stale`,
  `request_invalid`, or `baseline_missing`, and the intended and observed
  statuses.
- `exit.json` — the sweep's authoritative exit evidence: written by the tmux
  lease runner after the agent's whole process group is gone, and by the
  TUI's reconciliation for an embedded or interactive terminal's exit before
  it takes any lock or touches the store, so a reconciliation that fails
  part-way is retried by the sweep rather than lost. `code_unknown` marks a
  record whose writer saw the exit but not its status.
- `FLOW-REPLAY-NOTICE.txt` — advisory, same shape as the migration notice.
- `.seq.lock` — the per-launch flock every appender and replayer holds. Its
  owner line is rewritten on each acquisition, so retention ignores it.

The TUI hosts one Unix socket per state root
(`$TMPDIR/approach-<uid>/<8 hex of sha256(root)>.sock`, falling back to
`/tmp/approach-<uid>/`, never truncated to fit; a sibling `.lock` is the
endpoint's ownership flock, held for the listener's lifetime so that probing a
socket for liveness and replacing a dead one are one step) and exports
`APPROACH_CONTROL_ENDPOINT` and `APPROACH_CONTROL_TOKEN` to every registered
launch. With them, `approach flow` writes are proxied over the socket and
applied through the TUI's one store — which is how an agent whose `approach`
on PATH is a schema-(N-1) build completes a phase a schema-N TUI launched.
When the socket does not answer, reads open the database read-only or exit
non-zero (a read never exits 0 without data); `phase restart`, `add-child`,
and `agent set` open the database or exit non-zero (`cannot be deferred`);
and the replayable writes open the database under the same log discipline or,
when this build must not touch that database at all, spool the request and
exit 0 with a fixed `spooled:` message — but only a request a replay can
apply: one from a launch that owns a phase, for that phase or for the Flow.
A launch without a phase (repair, autofix, generic) or a write to another
phase exits non-zero (`cannot be deferred`) instead, because replay would
reject it and the deferred success would never land. A restarted TUI re-binds the same
socket path, reloads registrations from `launch.json`, replays every pending
request exactly once — under the latest-launch gate, so a launch that no
longer owns its phase never writes — and only then listens.

Reconciliation demotes a phase only on positive exit evidence: an embedded
terminal's exit, an interactive launch handing the TTY back, the lease
Reconciliation demotes a phase only on positive exit evidence: an embedded
terminal's exit, an interactive launch handing the TTY back, the lease
runner's `exit.json`, or a Claude session record the store says ended more
than ten minutes ago for a reason other than `/clear` — and never while the
Flow lease is held. A `SessionEnd` hook replays first but is not itself exit
evidence; it does keep the provider's `reason` on the record (`end_reason`),
and a launch whose latest end is a `clear` is treated as continued, because
the agent lives on in a new session that has no record until it ends. Codex
and Cursor records say `ended` after every turn, so they are never exit
evidence either: a Codex or Cursor launch that exits without a result is
demoted only by its embedded terminal's exit or the lease runner's
`exit.json`.
The write is `needs_attention` with the reason as the outcome
(`phase_result_missing` or `phase_result_stale`); on a plan-review kind it is
`blocked`/`blocked` with the reason leading the notes, the convention that
kind already uses for "the agent did not run". Every such note ends with the
recovery command: `approach flow phase set --flow-id <id> --phase-id <id>
--status running --notes "<reason>"`. An operator who runs it without
relaunching keeps that launch as the latest one, so a later stale replay will
demote it again — loud over silent. Launch directories with nothing pending
are retired 14 days after their last state change; a directory with a pending
request or a held lock is never removed. Two TUIs on one root: the second
finds the socket owned and runs without an endpoint, so its launches take the
direct path.

Flow records share this root but use the `0600` SQLite database `approach.db`;
the `0700` root contains its WAL/SHM sidecars. Legacy `flows/` records migrate
once and are left unchanged in place; `flows/` is retained, not moved. Session transcripts and
saved plans remain ordinary restrictive files.

Hook transcript paths must resolve to regular files inside the provider-owned
transcript root. Codex uses `$CODEX_HOME/sessions` (default
`$HOME/.codex/sessions`); Claude uses `$CLAUDE_CONFIG_DIR/projects` (default
`$HOME/.claude/projects`); Cursor CLI uses `$HOME/.cursor/chats`. Set those
existing provider environment variables when the provider itself uses a custom
home or config directory. Relative
paths, paths outside the expected root, and symlink escapes are rejected
before Approach creates session artifacts. Cursor transcripts are opaque
SQLite stores, so Approach records the validated path and does not normalize
them into JSONL.

## Resume Semantics

Resume uses the stored provider session ID: Codex resumes with
`codex ... resume <session-id>`, Claude Code with
`claude ... --resume <session-id>`, and Cursor CLI with
`cursor-agent --resume <session-id>`, preserving the same hook and metadata
wiring as fresh launches. Initially non-Flow resume session IDs retain their
existing behavior: they are trimmed and whitespace-only IDs are rejected, so a
resume command never carries a blank ID. Approach runs the
resume command from the recorded session `cwd` when present, falling back to
the captured worktree path, while preserving the stored worktree metadata for
subsequent hooks. Sessions missing a provider session ID cannot be resumed;
Approach reports this in the status line instead of starting a fresh provider
session.

A record cached with a Flow association takes the same lifecycle route from all
three sources: Sessions-pane `r`, the embedded dock's global session picker,
and Enter on an inline worktree session. The cached Flow is only the initial
reservation hint. Approach first reads the exact provider plus raw session ID
and verifies the decoded key. In tmux mode it also refuses if the launch window
recorded by that refreshed row is still live, even when the row says `ended`.
If the refreshed record moved from Flow A to Flow B, ownership transfers to B
before B is read; if it is now non-Flow, ownership is released and the refreshed
record returns to that source's existing non-Flow route. A missing B is treated
the same way because saved sessions can outlive deleted Flow records; other read
failures still refuse. Protected preparation re-reads the exact session under
B's launch reservation and refuses if its Flow association moved again or its
final launch window is live. This preserves final repo, worktree, `cwd`, branch,
commit, plan path/ID, provider, and raw session ID instead of launching from the
cached row.

An authoritatively Flow-associated CLI resume is interactive and embedded-only,
regardless of tmux mode or the Flow's headless preference. It exports the
authoritative Flow ID with an empty phase ID, records no phase launch ID or
phase status, schedules no prompt prefill, and passes the nonblank raw session
ID byte-for-byte to the provider. The retained terminal cannot detach and
continues to occupy that exact Flow after the process exits until the user
dismisses it. Unsupported embedded startup is a failure; it never falls back
to an external or tmux launch.

A Flow phase resume (`r` in the Flows surface) additionally goes through the
Flow launch lifecycle: it targets the exact Flow and phase, re-reads the
persisted record before writing, and records the resume as a fresh launch on
that phase. The provider session it resumes is the one the read stage
validated, never re-derived from the record the write returns. Occupancy is
Flow-scoped for CLI resumes and matches every other launch kind: a non-repair
embedded terminal on that Flow — on any phase and in any state — refuses the
resume out loud until it is closed, detached, or dismissed, while a repair
terminal and another lifecycle attempt — a manual launch, an auto launch, or a
repair — refuse it silently.
