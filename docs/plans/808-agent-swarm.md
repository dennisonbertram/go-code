# Plan: agent_swarm epic #808 — Slices 1–2

## Context

- Problem: go-code can delegate single sub-agents but has no orchestration that
  fans one prompt template out over many items into concurrent subagents.
  Epic #808 adds an `agent_swarm` capability matching kimi-code's AgentSwarm.
- User impact: the model must loop `start_subagent`/`wait_subagent` by hand,
  which is slow, error-prone, and burns turns.
- Constraints: reuse `internal/subagents` Manager + `tools.SubagentManager`
  (`InlineManager`); strict TDD. Slice 1 (core `Swarm`) merged via PR #839.
  This PR covers ONLY Slice 2 (resume via `resume_agent_ids`).

## Scope

- In scope (Slice 2):
  - Extend `internal/subagents/swarm.go`: `ResumeAgentIDs[i]` pairs with
    `Items[i]` (first-K positional); resolve each ID upfront via
    `tools.SubagentManager.Get`; reject unknown IDs, duplicate IDs,
    more IDs than items, and active-incompatible statuses (only
    `running`/`waiting_for_user` accept steered messages).
  - Deliver the expanded prompt through the existing messaging path used by
    `message_subagent`: `tools.RunSteerer.SteerRun(runID, prompt)`.
  - Resumes are scheduled first in the ramp and count against the same
    concurrency allowance; parent cancellation cancels resumed members too.
  - Report order stays "items first, resumes later"; resumed members carry
    `Resumed: true` and their subagent ID from the start.
- Out of scope: `agent_swarm` deferred tool + runner sole-call rule
  (Slice 3), TUI panel (Slice 4), new server endpoints.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (no public surface in this slice)
- Spec docs to update before code: none
- Implementation notes to add after code: engineering-log entry per slice.

## Test Plan (TDD)

- New failing tests to add first (`internal/subagents/swarm_resume_test.go`):
  - resume happy path against a fake manager + fake steerer (steer carries
    the expanded prompt to the resolved run ID; mixed cohort completes)
  - unknown ID and active-incompatible status rejection (queued/completed/
    failed/cancelled rejected; running/waiting_for_user accepted)
  - duplicate resume ID rejection; more resume IDs than items rejection
  - scheduling order: resumes precede new items (deterministic with cap 1)
  - report marking: `Resumed` flag, ID, order (items first, resumes later)
  - steer failure captured per member without aborting the cohort
  - cancellation cancels resumed members as well as new members
  - missing steerer with non-empty resume_agent_ids rejected
- Existing tests to update: drop the "resume_agent_ids rejected" case from
  the slice-1 validation table (now supported).
- Regression tests required: all slice-1 swarm tests stay green.

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

- Risk: status check races the member's own lifecycle (subagent finishes
  between `Get` and `SteerRun`).
- Mitigation: `SteerRun` errors are captured per member in the report; the
  cohort is never aborted, matching slice-1 failure semantics.
- Risk: report order confusing callers when resumes consume the first items.
- Mitigation: deterministic documented order (non-resumed item members in
  item order, then resumed members in resume-ID order), each entry carrying
  its `item` and `resumed` marker.
