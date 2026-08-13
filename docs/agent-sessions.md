# Agent Sessions and Hooks

How Approach captures, stores, and resumes coding-agent sessions. The sessions
view UI is documented in `docs/tui-guide.md`; the `[sessions]` config keys and
state-root precedence are in `docs/config.md`.

## Automatic Wiring

CLI agents launched from Approach (worktree `a`/`N`, Flow `g`, or session resume
`r`) are wired automatically: Approach passes Claude Code or Codex a session-end
hook that calls the current Approach binary, and it exports `APPROACH_*` metadata
so hook records can be associated with the repo, worktree, branch, and launch.
The exported metadata includes `APPROACH_AGENT`, `APPROACH_LAUNCH_ID`,
`APPROACH_REPO_PATH`, `APPROACH_WORKTREE_PATH`, `APPROACH_BRANCH`,
`APPROACH_COMMIT`, the three `APPROACH_*_STATE_ROOT` roots, and plan or flow
IDs, paths, and phase fields when available.

`codex-app` opens via macOS deep link instead; Approach scrubs inherited
`APPROACH_*` from `open` and includes prompt-only launch metadata with copyable
`--state-root` command examples. New `codex-app` threads use the repo path for
Codex App project identity when Approach knows it, while the selected worktree
remains available in the prompt metadata.

## Manual Hook Setup

For agent sessions that are not launched by Approach, configure Claude Code or
Codex hooks to call Approach:

```bash
approach session-hook --provider claude
approach session-hook --provider codex
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

For Codex hook payloads with `hook_event_name = "Stop"`, Approach records the
session as ended. Claude hook ingestion also records ended sessions, using the
payload end time when present and the current time as a fallback.

`session-hook` loads the normal Approach config, so `[sessions].root` and
`copy_raw_transcripts` apply to hook ingestion. `--state-root` overrides the
configured sessions root for one hook invocation. Raw provider transcript
copies are off by default; set `copy_raw_transcripts = true` to also preserve
provider-native `raw.jsonl` alongside normalized transcript events.

## Storage and Security

Session data is stored under the user state directory by default:
`$XDG_STATE_HOME/approach/sessions/v1`, or
`~/.local/state/approach/sessions/v1` when `XDG_STATE_HOME` is unset. Sessions,
plans, and Flows share this artifact root; the full precedence chain
(`--state-root`, `APPROACH_*_STATE_ROOT` variables, `[sessions].root`) is in
`docs/config.md`.

Transcripts may contain secrets or private prompts; Approach keeps them outside
repositories and uses restrictive file permissions for created session files.
Provider session IDs are stored in hashed directory names instead of raw path
components. Hook payloads without a usable session ID are rejected at capture
time, so no unusable session records are stored.

Flow records share this root but use the `0600` SQLite database `approach.db`;
the `0700` root contains its WAL/SHM sidecars. Legacy `flows/` records migrate
once and are left unchanged in place; `flows/` is retained, not moved. Session transcripts and
saved plans remain ordinary restrictive files.

Hook transcript paths must resolve to regular files inside the provider-owned
transcript root. Codex uses `$CODEX_HOME/sessions` (default
`$HOME/.codex/sessions`); Claude uses `$CLAUDE_CONFIG_DIR/projects` (default
`$HOME/.claude/projects`). Set those existing provider environment variables
when the provider itself uses a custom home or config directory. Relative
paths, paths outside the expected root, and symlink escapes are rejected
before Approach creates session artifacts.

## Resume Semantics

Resume uses the stored provider session ID: Codex resumes with
`codex ... resume <session-id>` and Claude Code with
`claude ... --resume <session-id>`, preserving the same hook and metadata
wiring as fresh launches. Resume session IDs are trimmed and whitespace-only
IDs are rejected, so a resume command never carries a blank ID. Approach runs the
resume command from the recorded session `cwd` when present, falling back to
the captured worktree path, while preserving the stored worktree metadata for
subsequent hooks.
`codex-app` resumes use the existing macOS deep-link path rather than an
embedded terminal. Sessions missing a provider session ID cannot be resumed;
Approach reports this in the status line instead of starting a fresh provider
session.

A Flow phase resume (`r` in the Flows surface) additionally goes through the
Flow launch lifecycle: it targets the exact Flow and phase, re-reads the
persisted record before writing, and records the resume as a fresh launch on
that phase. The provider session it resumes is the one the read stage
validated, never re-derived from the record the write returns. Occupancy is
Flow-scoped for CLI resumes and matches every other launch kind: a non-repair
embedded terminal on that Flow — on any phase and in any state — refuses the
resume out loud until it is closed, detached, or dismissed, while a repair
terminal and another lifecycle attempt — a manual launch, an auto launch, or a
repair — refuse it silently. `codex-app` navigation is exempt from all of it.
