# Plan: agent_swarm epic #808 — Slices 1–3

## Context

- Problem: go-code can delegate single sub-agents but has no orchestration that
  fans one prompt template out over many items into concurrent subagents.
  Epic #808 adds an `agent_swarm` capability matching kimi-code's AgentSwarm.
- User impact: the model must loop `start_subagent`/`wait_subagent` by hand,
  which is slow, error-prone, and burns turns.
- Constraints: reuse `internal/subagents` Manager + `tools.SubagentManager`
  (`InlineManager`); strict TDD. Slice 1 (core `Swarm`) merged via PR #839,
  Slice 2 (resume) via PR #867. This PR covers ONLY Slice 3 (the deferred
  tool, policy integration, and sole-call rule).

## Scope

- In scope (Slice 3):
  - Mirror types + `SwarmRunner` interface + `AgentSwarmToolName` const in
    `internal/harness/tools` (import-cycle-safe, same pattern as
    `SubagentManager`); adapter `NewToolSwarmRunner` in `internal/subagents`.
  - New `internal/harness/tools/deferred/agent_swarm.go`: `TierDeferred`,
    `ActionExecute`, `Mutating: true`; params `prompt_template`, `items`,
    `resume_agent_ids` + profile/model overrides; returns the report via
    `MarshalToolResult`.
  - Description `internal/harness/tools/descriptions/agent_swarm.md`
    (sole-call rule, 128 cap, 5→+1/700ms ramp, env cap, resume semantics).
  - Registration in `internal/harness/tools_default.go` via new
    `AgentSwarmRunner` option; wired in `cmd/harnessd/runtime_container.go`.
  - Member exclusion: per-run `DeniedTools` plumbed `tools.SubagentRequest` →
    `subagents.Request` → `harness.RunRequest` → runState; enforced in
    `filteredToolsForRun` (never offered) and in the step-engine call gate
    (call blocked). The swarm sets it on every member.
  - Runner sole-call rule in `runner_step_engine.go`: a response containing
    `agent_swarm` plus any other call executes the (first) swarm call and
    rejects the extras with a corrective error naming the rule.
- Out of scope: TUI panel (Slice 4), new server endpoints.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: `internal/harness/tools/descriptions/agent_swarm.md`
  (tool contract, model-facing)
- Spec docs to update before code: none
- Implementation notes to add after code: engineering-log entry per slice.

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/harness/tools/deferred/agent_swarm_test.go`: definition shape
    (ActionExecute/Mutating/TierDeferred/required params), arg parsing and
    validation errors, profile resolution + override mapping, resume mapping,
    report marshaling, runner-error propagation, nil-runner error.
  - `internal/subagents/swarm_tool_runner_test.go`: adapter maps
    tools.SwarmRequest ↔ subagents types both directions.
  - `internal/harness/runner_swarm_test.go`: sole-call rule (swarm+extra →
    corrective error, swarm alone → ok); DeniedTools gate (call blocked,
    definitions filtered even when activated); approval flow surfaces
    agent_swarm as mutating ActionExecute under destructive policy.
  - `internal/subagents/swarm_e2e_test.go`: fakeprovider full-stack session —
    find_tool activation, one `agent_swarm` call, 4 member runs with expanded
    prompts, one aggregated tool result, run completes.
- Existing tests to update: none.
- Regression tests required: existing harness/deferred/subagents suites stay
  green.

## Cross-Surface Impact Map

- None — additive tool + in-process enforcement; no provider/model flow,
  config, or server API changes (TUI panel is Slice 4).

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Refactor while tests remain green.
- [x] Update docs, status ledgers, and indexes.
- [x] Update engineering log.
- [x] Run full test suite for touched packages + regression.

## Risks and Mitigations

- Risk: import cycle between harness/deferred and subagents.
- Mitigation: mirror types in `tools` + adapter in `subagents`, exactly the
  existing `SubagentManager`/`InlineManager` pattern.
- Risk: sole-call gate breaking parallel tool execution for normal calls.
- Mitigation: gate only engages when a response contains `agent_swarm`;
  all other responses flow unchanged; regression suite validates.
