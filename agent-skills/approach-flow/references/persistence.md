# Persistence contract

Use this contract for every Approach read or write.

## Resolve the launched binary

Approach may pin the executable that launched the agent. Honor the pin and stop
if it is unusable; falling back could write with an incompatible build.

```bash
if [ -n "${APPROACH_EXECUTABLE:-}" ] && [ ! -x "$APPROACH_EXECUTABLE" ]; then
  printf 'Approach persistence failure: APPROACH_EXECUTABLE is not executable: %s\n' "$APPROACH_EXECUTABLE" >&2
  exit 1
fi
APPROACH_BIN="${APPROACH_EXECUTABLE:-${APPROACH_BIN:-approach}}"
```

Every runnable example uses the same self-resolving command word. Preserve it
when adapting a command; never replace it with bare `approach` in a launched
workflow.

## IDs and state root

Commands launched by Approach default these flags from the environment:

- `--flow-id` from `APPROACH_FLOW_ID`
- `--phase-id` from `APPROACH_FLOW_PHASE_ID`
- `--plan-id` from `APPROACH_PLAN_ID`
- plan `--phase-id` from `APPROACH_PLAN_PHASE_ID`, then the Flow phase

Flow and plan commands share root resolution. A launch normally supplies
`APPROACH_FLOW_STATE_ROOT`, so omit `--state-root`. For ad hoc work, use the
same explicit absolute `--state-root` on every related command.

## Verify and report

After a write, use the corresponding read command where one exists. Capture
the failing command, its error, and the state that was not persisted. Do not
silently retry against another binary or root, and do not advance later
workflow state as if the write succeeded.

If work itself is blocked, persist that workflow state when possible. If the
persistence command also fails, report both the work blocker and the
persistence failure.

## Control endpoint and deferred writes

`APPROACH_CONTROL_ENDPOINT` and `APPROACH_CONTROL_TOKEN` are the launch's
registration with the running Approach; the `flow` CLI reads them itself to
route writes through the launcher, and you never pass or print them.

One exit-zero case is a **deferred** success, not a failure. When the launcher
that started you is not reachable and this build cannot open the flow database,
a `flow` write may print `spooled: control endpoint unreachable and this build
cannot open the flow database; the request will be applied on the next approach
start` on stderr, print no JSON, and exit 0. The request is on disk and will be
applied, exactly once, when Approach next starts. Report it as deferred ("phase
completion recorded for replay"), do not retry it, and do not describe the
phase as already advanced. Only `phase set`, `phase complete`, `phase block`,
`phase needs-attention`, `plan set`, `issue set`, `pr set`, and `merge set` can
be deferred; `phase restart`, `phase add-child`, and `phase agent set` cannot
and exit non-zero instead, and `flow read` and `flow list` never spool — a read
either returns data or exits non-zero. One non-zero exit from those three is not
a plain failure: `was sent to control endpoint ... but no response arrived; the
controller may already have applied it` means the write may have landed and was
deliberately not run twice. Run `flow read` and act on what the record shows
rather than retrying blindly.
