# Plan: agent_swarm epic #808 — Slice 1: swarm orchestrator

## Context

- Problem: go-code can delegate single sub-agents but has no orchestration that
  fans one prompt template out over many items into concurrent subagents.
  Epic #808 adds an `agent_swarm` capability matching kimi-code's AgentSwarm.
- User impact: the model must loop `start_subagent`/`wait_subagent` by hand,
  which is slow, error-prone, and burns turns.
- Constraints: reuse `internal/subagents` Manager + `tools.SubagentManager`
  (`InlineManager`); strict TDD; this PR covers ONLY Slice 1 (core `Swarm`
  type in `internal/subagents`). Slices 2–4 (resume, deferred tool, TUI) are
  out of scope here.

## Scope

- In scope:
  - `internal/subagents/swarm.go`: `SwarmRequest` (PromptTemplate, Items,
    ResumeAgentIDs rejected until Slice 2, profile/model overrides),
    validation, `Swarm.Run(ctx) (SwarmReport, error)`.
  - Concurrency scheduler: 5 immediate starts, +1 every 700ms, cap read once
    from `HARNESS_SWARM_MAX_CONCURRENCY` (default 128, clamped to 128).
  - Caller context cancellation cancels every started member; per-member
    errors are captured in the report and never abort the cohort.
  - `SwarmReport` with per-member ID, item, status, output/error in
    deterministic item order.
- Out of scope: resume_agent_ids delivery (Slice 2), `agent_swarm` tool and
  runner sole-call rule (Slice 3), TUI panel (Slice 4), new server endpoints.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (no public surface in this slice)
- Spec docs to update before code: none
- Implementation notes to add after code: engineering-log entry per slice.

## Test Plan (TDD)

- New failing tests to add first (`internal/subagents/swarm_test.go`):
  - table-driven validation: missing `{{item}}` placeholder, empty items,
    >128 items, duplicate expanded prompts, resume_agent_ids rejected
  - ramp timing with injected manual ticker: 5 in-flight before first tick,
    +1 per tick
  - env cap: `HARNESS_SWARM_MAX_CONCURRENCY` honored; resolver default/clamp
  - cancellation: parent cancel cancels all started members, unstarted
    members reported cancelled, all reach terminal state
  - aggregated report shape/order; per-member failure does not abort cohort
  - acceptance: real `Manager` + `InlineManager` fan-out completes
- Existing tests to update: none
- Regression tests required: existing `internal/subagents` tests stay green
  unmodified.

## Cross-Surface Impact Map

- None — in-process orchestration only; no provider/model flow, config,
  server API, or TUI changes in this slice.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first.
- [x] Implement minimal code changes (`swarm.go`).
- [x] Refactor while tests remain green.
- [x] Update docs, status ledgers, and indexes.
- [x] Update engineering log.
- [x] Run full test suite for touched packages.

## Risks and Mitigations

- Risk: ramp timing tests flaking on wall-clock sleeps.
- Mitigation: inject a manual ticker seam; wall-clock only for short
  stability assertions with generous margins.
- Risk: goroutine leak / hang when a member never terminates after cancel.
- Mitigation: swarm issues `Cancel` for every started member and relies on
  the documented `tools.SubagentManager` contract (cancel drives members to
  a terminal state); the swarm-scoped context is always stopped on return.
