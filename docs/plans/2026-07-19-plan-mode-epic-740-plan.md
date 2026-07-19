# Plan: enforced plan mode — Epic #740

## Context

- Problem: the TUI plan toggle is cosmetic and edits are not constrained.
- User impact: operators need an auditable read-only planning phase before implementation.
- Constraints: reuse the existing tool policy, permission-rule matcher, approval broker, and conversation store.

## Scope

- In scope: #764 state/request plumbing; #765 policy gate; #766 broker approval; #767 SQLite persistence; #768 CLI/TUI request plumbing; #769 TUI approval preview.
- Out of scope: #567 and any workspace-boundary redesign.

## Documentation Contract

- Feature status: in implementation
- Public docs affected: operator runbook and CLAUDE.md policy notes.
- Spec docs to update before code: this plan and plan index.
- Implementation notes to add after code: engineering and system logs.

## Test Plan (TDD)

- New failing tests to add first: request/state lifecycle, real wrapped-tool denial, broker HTTP approve/deny, SQLite restart persistence, CLI/TUI serialization, and overlay key/scroll behavior.
- Existing tests to update: TUI plan-mode and command/API snapshots.
- Regression tests required: non-plan policy and normal run HTTP paths.

## Cross-Surface Impact Map

- Config: None; plan mode is a per-run request.
- Server API: `RunRequest.PlanMode` is accepted by the existing run endpoint; existing approval routes are reused.
- TUI state: ctrl+o is serialized with the next run and plan approval events use the existing decision transport.
- Regression tests: harness tools, runner, SQLite, server HTTP, CLI, and TUI coverage.

## Implementation Checklist

- [ ] #764 state machine and request plumbing
- [ ] #765 central edit gate
- [ ] #766 approval transition
- [ ] #767 plan persistence
- [ ] #768 CLI/TUI start plumbing
- [ ] #769 TUI approval view
- [ ] Docs, full regression, merge origin/main, push and PR

## Risks and Mitigations

- Risk: static registry wrappers cannot see per-run state. Mitigation: inject live run plan state into the real tool execution context.
- Risk: a mutation has no path. Mitigation: fail closed while plan mode is active.
