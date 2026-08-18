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

Use `"$APPROACH_BIN"` wherever another reference writes `approach`.

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
