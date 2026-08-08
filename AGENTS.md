# Agent Entry Point for approach

Intentionally short. Don't copy build, UI, config, Flow, or storage detail here; it drifts when repeated across entrypoints. Add substance to the linked docs.

## Read First

- Commands, quality gates, dev workflow: `docs/agent-operations.md`
- Architecture, package map, invariants: `docs/architecture.md`
- User-facing behavior and key bindings: `README.md`
- Config, saved plans, Flows, and hooks: `docs/config.md`
- Flow phase semantics: `docs/flow-phases.md`
- Agent Flow recipes: `agent-skills/approach-flow/SKILL.md`, `agent-skills/approach-flow-create/SKILL.md`, `agent-skills/approach-plan-persist/SKILL.md`

## Ground Rules (know these immediately)

- Use TDD unless told otherwise or there's a strong reason not to.
- Pull latest from main before starting, unless given a different base.
- Never commit or push directly to main; branch first.
- Persist Flow and plan progress through the `approach flow` / `approach plan` CLIs, never by editing store files by hand.
- Transcripts may contain secrets — keep them under user state, never in a repo.

## Quality Gate

CI requires all three clean before merge:

```bash
gofmt -l .          # must print nothing
make test           # all tests pass
make build          # builds bin/approach
```

## Conflict Rule

If this file conflicts with a linked source, trust the linked source and fix this file by removing the duplicate.
