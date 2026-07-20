# Plan: epic #820 slice 1 — steer client plumbing (steerRunCmd + harnesscli steer)

## Context

- Problem: the server exposes `POST /v1/runs/{id}/steer` (202/404/409/429 contract,
  `internal/server/http_runs.go` `handleRunSteer`) but no client path exists — the TUI
  never calls it and the one-shot CLI has no `steer` subcommand.
- User impact: users cannot inject corrective input into a running turn from any
  go-code client (kimi-code parity gap).
- Constraints: implement ONLY slice 1 of #820 (client plumbing). No keybinding, no
  SSE `steering.received` rendering, no local echo — those are slices 2–4.
  Strict TDD per `docs/runbooks/testing.md`. Server-side steering semantics are a
  fixed contract.

## Scope

- In scope:
  - `cmd/harnesscli/tui/api.go`: `steerRunCmd(baseURL, runID, prompt, apiKey) tea.Cmd`
    mirroring `cancelRunCmd`; client-side rejection of empty/whitespace prompts.
  - `cmd/harnesscli/tui/messages.go`: `SteerAcceptedMsg` + `SteerErrorMsg` (Kind:
    `not_found`, `run_not_active`, `steering_buffer_full`, `invalid_prompt`, `http`,
    `transport`).
  - `cmd/harnesscli/runctl.go`: `runSteer(args)` — `harnesscli steer <run-id> <prompt>`,
    `-base-url` flag mirroring `runCancel` (runCancel has no `-api-key` flag; "consistent
    with runCancel" wins over the epic's parenthetical).
  - `cmd/harnesscli/auth.go` `dispatch`: route `steer`.
- Out of scope: slices 2–5 (SSE rendering, ctrl+g binding, local echo, e2e); any
  server/harness change; help-dialog/keybinding docs (slice 3).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none in this slice (CLI help text is the usage error output;
  `website/docs/cli/tui.md` keybinding table belongs to slice 3).
- Spec docs to update before code: none — server contract already documented in
  `docs/implementation/issue-6-mid-run-steering.md`.
- Implementation notes to add after code: engineering-log entry per repo convention.

## Test Plan (TDD)

- New failing tests to add first:
  - `cmd/harnesscli/tui/steer_command_test.go`: POST path + JSON body assertion,
    202 → `SteerAcceptedMsg`, 404/409/429 → `SteerErrorMsg` kinds, empty prompt
    rejected client-side without HTTP call, API key header sent.
  - `cmd/harnesscli/runctl_test.go` (append, mirroring runCancel tests): happy path
    202 + confirmation output, missing-args usage error, whitespace prompt rejected
    without HTTP, 404/409/429 exit 1 with clear message, dispatch routing.
  - `cmd/harnesscli/tui/api_auth_test.go`: add `steerRunCmd` to the auth audit table.
- Existing tests to update: none.
- Regression tests required: `go test ./cmd/harnesscli/... -count=1` green.

## Cross-Surface Impact Map

- Not a provider/model flow change — no impact map required. Surfaces touched:
  TUI API client + one-shot CLI only.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above).
- [x] Write failing tests first, watch them fail.
- [x] Implement `steerRunCmd` + message types.
- [x] Implement `runSteer` + dispatch routing.
- [x] gofmt + go vet clean.
- [x] `go test ./cmd/harnesscli/... -count=1` green.
- [x] Engineering-log entry; `docs/plans/INDEX.md` updated for the new plan page.
- [ ] Commit, push `epic/820-tui-steering`, open PR (do NOT merge).

## Risks and Mitigations

- Risk: epic parenthetical mentions an `-api-key` flag that `runCancel` does not have.
- Mitigation: mirror `runCancel` exactly (`-base-url` only); note the deviation in the
  PR body so slice reviewers can decide.
