# Plan: pin plan-mode exit semantics across approval modes (epic #819, slice 1)

## Context

- Problem: plan-exit approval currently always routes through the approval broker regardless of
  `ToolApprovalMode`, but nothing pins that behavior. A future change could gate plan exit on
  approval mode (e.g. letting `full_auto` bypass it) with no test failing.
- User impact: operators rely on plan-exit approval as a hard checkpoint; `full_auto` must never
  bypass it. Denial must return the run to plan mode with operator feedback; approval must
  deactivate plan mode. Nil broker and broker timeout must be defined outcomes.
- Constraints: semantics-pinning tests only; no feature work (later slices of #819). Minimal diff.

## Scope

- In scope: new `internal/harness/plan_mode_semantics_test.go`; doc comments in
  `internal/harness/plan_mode.go` documenting the pinned semantics.
- Out of scope: model-facing guidance, approach options, TUI work, website docs (slices 2-5).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (doc comments only)
- Spec docs to update before code: none
- Implementation notes to add after code: doc comments in `internal/harness/plan_mode.go`

## Test Plan (TDD)

- New failing tests to add first (semantics pin — verified to fail under a deliberate mutation
  that gates plan exit on approval mode, then to pass on real code):
  - `TestPlanModeExitApprovalBlocksInEveryApprovalMode` — table over
    `ToolApprovalModeFullAuto` / `ToolApprovalModePermissions` / `ToolApprovalModeAll`:
    `awaitPlanApproval` blocks on the broker (run waits, `plan.approval_required` emitted) in all
    modes; approve → `PlanModeInactive` + `plan.approval_granted` + run completes.
  - `TestPlanModeExitDenyReentersActiveWithFeedback` — table over all modes: deny →
    `PlanModeActive` + `plan.approval_denied` + "operator requested changes" user message appended;
    run continues and can be approved on the next attempt.
  - `TestPlanModeExitNilBrokerFailsRun` — nil broker → explicit run failure
    (`plan mode requires an approval broker`).
  - `TestPlanModeExitBrokerTimeoutFailsRun` — broker timeout → run fails (defined outcome).
- Existing tests to update: none.
- Regression tests required: these are the regression guardrail.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Write failing tests first (mutation-verified).
- [x] Document pinned semantics in `plan_mode.go` doc comments.
- [x] Run `go test ./internal/harness/ -run PlanMode -count=1` green.
- [x] gofmt + go vet clean; run touched-package tests.
- [x] Commit, push `epic/819-plan-mode`, open PR (no merge).

## Risks and Mitigations

- Risk: pinning tests pass trivially because behavior already exists.
- Mitigation: mutation check — temporarily gate exit on approval mode and confirm the full_auto
  case fails; revert mutation.
