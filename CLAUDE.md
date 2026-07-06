# Claude Code Entry Point for wtui

Intentionally short. Don't add build, architecture, config, or Flow rules here — they drift when duplicated. `AGENTS.md` is the authoritative agent guide; this file only routes Claude Code to it and notes Claude-specific tooling.

## Read First

- Start here — ground rules, safety, quality gate: `AGENTS.md`
- Commands, workflow, safety-in-practice: `docs/agent-operations.md`
- Architecture, package map, invariants: `docs/architecture.md`
- User behavior, key bindings, CLI: `README.md`
- Config, saved plans, Flows, hooks: `docs/config.md`
- Flow phase semantics: `docs/flow-phases.md`

## Claude-Specific Notes

- Flow/plan work runs through the bundled skills: `agent-skills/wtui-flow/SKILL.md`, `agent-skills/wtui-flow-create/SKILL.md`, `agent-skills/wtui-plan-persist/SKILL.md`.

## Conflict Rule

If this file disagrees with `AGENTS.md` or any linked doc, trust the linked source and fix this file by removing the duplication.
