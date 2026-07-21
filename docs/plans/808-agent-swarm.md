# Plan: agent_swarm epic #808 — Slices 1–4

## Context

- Problem: go-code can delegate single sub-agents but has no orchestration that
  fans one prompt template out over many items into concurrent subagents.
  Epic #808 adds an `agent_swarm` capability matching kimi-code's AgentSwarm.
- User impact: the model must loop `start_subagent`/`wait_subagent` by hand,
  which is slow, error-prone, and burns turns.
- Constraints: reuse `internal/subagents` Manager + `tools.SubagentManager`
  (`InlineManager`); strict TDD. Slices 1–3 merged via PRs #839, #867, #899.
  This PR covers ONLY Slice 4 (TUI live swarm progress panel).

## Scope

- In scope (Slice 4):
  - Track the current run's `agent_swarm` tool call from SSE events
    (`tool.call.started` → items from arguments; `tool.call.completed` →
    exact member report) in a `swarmTracker` on the TUI model.
  - Live grouped panel in the viewport: summary line (launched/completed
    counts + cap 128) and per-member pending/running/completed/failed rows
    with the item label, refreshed in place on each poll.
  - Poll loop: while a swarm is active, re-fetch `/v1/subagents` every 1s
    (`swarmPollTickMsg`); stop on completion or when all members terminal.
    `RemoteSubagent` gains `created_at` (already sent by the server) for
    creation-window member matching; NO server changes.
  - `formatSubagentsLines` groups swarm members in the `/subagents` listing
    (swarm section first, then the regular entries); no-swarm output
    unchanged.
- Out of scope: server endpoint changes, multi-run swarm tracking,
  historical swarm persistence.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (TUI behavior only)
- Spec docs to update before code: none
- Implementation notes to add after code: engineering-log entry per slice.

## Test Plan (TDD)

- New failing tests to add first:
  - internal (`package tui`): `formatSwarmPanelLines` multi-status rendering
    (summary + per-member rows + cap), creation-window matching (old
    subagents excluded, unmatched items pending), exact members after report,
    `/subagents` grouping in `formatSubagentsLines`, no-swarm regression,
    report parsing on completion, poll tick start/stop.
  - external (`package tui_test`): SSE flow — agent_swarm started renders
    the live panel, poll updates per-member status, completion renders exact
    statuses and stops updating.
- Existing tests to update: none.
- Regression tests required: existing TUI suites stay green unmodified.

## Cross-Surface Impact Map

- None — TUI-only; no provider/model flow, config, or server API changes.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Refactor while tests remain green.
- [x] Update docs, status ledgers, and indexes.
- [x] Update engineering log.
- [x] Run full test suite for touched packages + regression.

## Risks and Mitigations

- Risk: mid-swarm member↔item matching is heuristic (creation window +
  schedule order); concurrent unrelated subagent creation could misattribute.
- Mitigation: single-run tracking only (agent_swarm is sole-call and blocks
  the parent run, so at most one swarm is active per run); the aggregated
  report at completion replaces heuristics with exact member IDs.
- Risk: live panel line offsets going stale as other viewport content moves.
- Mitigation: panel renders only while the parent run is blocked inside the
  tool call (no interleaved tool cards); replacement is clamped; block
  freezes at completion.
