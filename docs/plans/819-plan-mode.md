# Plan: pin plan-mode exit semantics across approval modes (epic #819, slice 1)

Slice 1 shipped (PR #827), slice 2 shipped (PR #858), slice 3 shipped (PR #890). Slice 2-4 plans
are appended below.

## Slice 4: approach-option selection in the plan-approval overlay (branch `epic/819-plan-mode-s4`)

- Problem: the TUI plan-approval overlay ignores the `options` slice 3 put in
  `plan.approval_required`; the operator cannot pick an approach.
- Design decisions:
  - `planApprovalState` gains `options []planApproachOption` + `selectedIdx`;
    `planApproachOption{id,label,description}` mirrors the wire shape.
  - With options present, ↑/↓/j/k moves the selection cursor (askuser `▶ ` idiom) instead of
    scrolling; without options the overlay scrolls and renders exactly as before (regression
    guard test).
  - `enter`/`a` approves with the highlighted option ID; `d` requests changes (never posts an
    option). `approveToolCmd` takes a variadic option ID (`{"option": id}` body) so the two
    existing tool-approval call sites compile unchanged; `toolApprovalDecisionCmd` builds the
    JSON body only when an option is present.
  - `model.go` parses `options` from the `plan.approval_required` payload; a fresh no-option
    payload clears any stale options.
- In scope: `cmd/harnesscli/tui/plan_approval.go`, `approval.go`, `model.go`;
  `plan_approval_options_test.go`.
- Out of scope: website docs (slice 5).
- TDD (written first, watched fail to compile — unknown `options`/`selectedIdx`/
  `planApproachOption`):
  - `TestPlanApprovalOverlayRendersOptionsAndMovesCursor` — options render, cursor moves/clamps,
    scroll untouched in options mode.
  - `TestPlanApprovalApprovePostsSelectedOption` — enter and a post `{"option":"b"}` to
    `/v1/runs/{id}/approve`.
  - `TestPlanApprovalDenyIgnoresOptions` — d posts to `/deny` with no option.
  - `TestPlanApprovalNoOptionsKeepsScrollAndPlainApprove` — regression: render/scroll/plain
    approve unchanged.
  - `TestPlanApprovalRequiredEventParsesOptions` — SSE payload parsing, stale-option clearing.
- Checklist:
  - [x] Write failing tests first.
  - [x] Implement option selection in plan overlay.
  - [x] `go test ./cmd/harnesscli/tui/ -run Plan -count=1` green.
  - [x] Full `go test ./cmd/harnesscli/... -count=1`, gofmt/vet, regression (`[regression] PASS`,
    coverage 84.4%), commit, push, PR #901.

## Slice 3: approach options in plan-exit approval (branch `epic/819-plan-mode-s3`)

- Problem: the agent cannot attach 1-3 approach options to a plan exit, and the operator cannot
  approve with a chosen option; `plan.approval_required` carries only `{tool, plan}` and the
  approve/deny API is binary.
- Design decisions:
  - Option shape: `{id, label, description}` with positional IDs `a`/`b`/`c` (matches the epic's
    `{"option":"b"}` curl example).
  - Extraction convention (from slice 2's guidance): the plan's trailing `## Approaches` section;
    numbered (`1.`/`2)`) or bullet (`-`/`*`) items; label/description split on ` — `, ` – `,
    `: `, or ` - `; markdown bold stripped. Exactly 1-3 valid items required — anything else is
    treated as a no-option plan (behavior identical to pre-slice-3).
  - Broker contract: `ApprovalRequest.Options` / `PendingApproval.Options`;
    `Ask` returns `(approved, selectedOption, err)`; new `ApproveWithOption(runID, option)`;
    `Approve(runID)` unchanged (delegates with `""`), so all existing callers compile.
  - Validation lives at the HTTP edge (`POST /v1/runs/{id}/approve`): unknown/absent option ID
    falls back to plain approve; `awaitPlanApproval` also only echoes IDs matching a presented
    option, so a direct broker call with a bogus ID degrades the same way.
  - Checkpoint broker: options persist in the approval record's `Questions` field (unused for
    `KindApproval`, no schema change); selection returns via
    `checkpoints.Service.ApproveWithPayload` (new) → resume payload `{"option": id}`.
  - Relay: on approve-with-option the step engine appends a user message
    (`The operator approved the plan and selected approach "..." (x). Follow that approach.`)
    before completing, so continuations see the choice; `plan.approval_granted` carries
    `option`/`option_label`.
- In scope: `internal/harness/approval_broker.go`, `plan_mode.go`, `checkpoint_brokers.go`,
  `runner_step_engine.go` (3 call sites), `internal/checkpoints/service.go`,
  `internal/server/http_runs.go`; tests below.
- Out of scope: TUI option selection (slice 4), website docs (slice 5).
- TDD (written first, watched fail to compile — undefined `PlanApproachOption`,
  `parsePlanApproaches`, `ApproveWithOption`, `PendingApproval.Options`):
  - `internal/harness/plan_mode_options_test.go`: `TestParsePlanApproaches` (7-case table),
    `TestPlanApprovalRequiredCarriesOptions` (event payload + pending), 
    `TestPlanApprovalRequiredOmitsOptionsWithoutApproaches` (regression guard),
    `TestPlanExitApproveWithOptionRoundTrip` (broker→Ask→granted→transcript relay),
    `TestInMemoryApprovalBrokerOptionsRoundTrip`.
  - `internal/harness/checkpoint_broker_test.go`: `TestCheckpointApprovalBrokerOptionsRoundTrip`.
  - `internal/checkpoints/service_test.go`: `TestServiceApproveWithPayloadWakesWaiterWithPayload`.
  - `internal/server/http_plan_mode_test.go`: `TestHTTPPlanExitApproveWithOption`,
    `TestHTTPPlanExitApproveWithInvalidOptionFallsBack`.
  - Existing `Ask` call sites in `approval_broker_test.go` / `checkpoint_broker_test.go` updated
    for the new 3-value return.
- Checklist:
  - [x] Write failing tests first.
  - [x] Implement options end-to-end.
  - [x] Targeted tests green on fresh `origin/main` base.
  - [x] Plan file, full package tests, gofmt/vet, regression (`[regression] PASS`, coverage 84.2%), commit, push, PR #890.

## Slice 2: tell the model it is in plan mode (branch `epic/819-plan-mode-s2`)

- Problem: nothing in the prompt or tool surface tells the model it is in plan mode, that only
  the plan file is writable, or that it should present approaches on exit. The model discovers
  `plan_mode_denied` only by tripping it.
- In scope: `Runner.planModePromptBlock(runID)` in `internal/harness/plan_mode.go` (guidance
  block naming the resolved plan file, read-only rule, present-the-plan instruction, 1-3
  approaches convention under `## Approaches`); injected as a trailing system message via
  `buildTurnMessages` (new `planModeGuidance` param) whenever `planMode == PlanModeActive`;
  denial-feedback message factored into `Runner.planModeDenialFeedback(runID)` and extended to
  name the plan file.
- Out of scope: approach options end-to-end, TUI, website docs (slices 3-5).
- Note: tests use the in-package `capturingProvider`, not `internal/fakeprovider` — fakeprovider
  imports this package, an import cycle for in-package tests; capturingProvider records the same
  outgoing requests.
- TDD: `internal/harness/plan_mode_prompt_test.go` (written first, watched fail):
  - `TestPlanModeGuidanceInjectedIntoOutgoingMessages` — block present in every outgoing request
    of a plan-mode run; names `.harness/plan.md`, `plan_mode_denied`, `## Approaches`.
  - `TestPlanModeGuidanceNamesCustomPlanFile` — custom `PlanFile` named, default absent.
  - `TestPlanModeGuidanceAbsentWhenPlanModeDisabled` — regression guard: no block in normal runs.
  - `TestPlanModeDenialFeedbackNamesPlanFile` — denial feedback names the plan file.
- Checklist:
  - [x] Write failing tests first.
  - [x] Implement guidance injection + denial message extension.
  - [x] `go test ./internal/harness/ -run TestPlanMode -count=1` green.
  - [ ] Full package tests + gofmt/vet; commit, push, open PR.

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
