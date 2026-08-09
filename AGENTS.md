# Agent Entry Point for Approach

Routing only. Substance lives in the linked docs; anything duplicated here drifts.
CLAUDE.md is a symlink to this file — edit here.

## Read First

- Commands, quality gate, dev workflow, safety: `docs/agent-operations.md`
- Architecture, package map, invariants: `docs/architecture.md`
- Overview, install, quick start: `README.md`
- TUI behavior and key bindings: `docs/tui-guide.md`
- Agent session hooks and storage: `docs/agent-sessions.md`
- Config reference: `docs/config.md`
- Flow phase semantics: `docs/flow-phases.md`
- Beads task tracking: `.agents/skills/beads/SKILL.md` (run `bd prime` for live session context)
- Flow/plan skill recipes: `agent-skills/approach-flow/SKILL.md`, `agent-skills/approach-flow-create/SKILL.md`, `agent-skills/approach-plan-persist/SKILL.md`

## Non-negotiables

Branch first — never commit or push directly to main. Everything else (TDD, quality gate, Flow/plan persistence, transcript safety) is in `docs/agent-operations.md`.

## Conflict Rule

If this file conflicts with a linked doc, trust the linked doc and remove the duplicate here.
