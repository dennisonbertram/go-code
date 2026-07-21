# Plan: epic #820 — mid-turn steering from the TUI (per-slice sections)

## Slice 1 — steer client plumbing (steerRunCmd + harnesscli steer)

### Context

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
- [x] Commit, push `epic/820-tui-steering`, open PR (do NOT merge). — merged as PR #841.

## Risks and Mitigations

- Risk: epic parenthetical mentions an `-api-key` flag that `runCancel` does not have.
- Mitigation: mirror `runCancel` exactly (`-base-url` only); note the deviation in the
  PR body so slice reviewers can decide.

---

## Slice 2 — render steering.received events in the transcript

## Context

- Problem: the TUI drops `steering.received` SSE events — there is no case for them in
  the dispatch switch — so a server-confirmed steering injection is invisible to the
  user even though the agent now sees the message.
- User impact: after steering (from any client — this TUI, the one-shot CLI, or a
  webhook), the transcript never shows the steered input, so the user cannot tell what
  the agent was told.
- Constraints: implement ONLY slice 2 of #820. No keybinding (slice 3), no local echo
  or dedupe (slice 4). Server event payload `{"message": "..."}` is a fixed contract
  (`internal/harness/runner.go` `drainSteering`). Strict TDD.

## Scope

- In scope:
  - `cmd/harnesscli/tui/model.go`: `case "steering.received"` in the SSE dispatch
    switch; parse `{"message": "..."}`; append a transcript entry (role `user`) and a
    user bubble, both carrying a `steered ⟂ ` marker prefix, via a small
    `appendSteeringMarker` helper (reused by slice 4's local echo). Malformed or empty
    payloads are ignored without panic. `m.lastEventID` bookkeeping untouched (the
    case sits inside the existing type switch, after ID tracking).
- Out of scope: slices 3–5; any server/harness change; help/keybinding docs.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (`website/docs/cli/tui.md` belongs to slice 3).
- Implementation notes to add after code: engineering-log entry.

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/tui/steer_events_test.go`, package
  `tui_test`, pattern from `sse_events_test.go`):
  - scripted `SSEEventMsg{EventType: "steering.received"}` → marker + message visible
    in rendered viewport; transcript gains a role `user` entry carrying the marker;
    run stays active.
  - marker is visually distinct from a normal user prompt (steered line carries
    `steered ⟂`; a typed prompt does not).
  - malformed payload (`not-json`, `{}`, whitespace-only message) → no panic, no
    marker, transcript unchanged.
- Regression tests required: `go test ./cmd/harnesscli/... -count=1` green, esp.
  `sse_events_test.go`, `escape_test.go`, `cancel_test.go`, `ctrlc_server_cancel_test.go`,
  `clipboard_test.go`, `keys_test.go`.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above).
- [x] Write failing tests first, watch them fail.
- [x] Implement `steering.received` case + `appendSteeringMarker`.
- [x] gofmt + go vet clean.
- [x] `go test ./cmd/harnesscli/... -count=1` green.
- [x] Engineering-log entry.
- [ ] Commit, push `epic/820-tui-steering-s2`, open PR (do NOT merge).

---

## Slice 3 — ctrl+g steers the active run with in-flight input

## Context

- Problem: slices 1–2 provide the client path (`steerRunCmd`) and the
  server-confirmed transcript marker, but the user has no way to trigger a steer
  from the TUI — kimi-code parity requires a single keypress that injects the
  input-box content into the running turn.
- User impact: without a binding, mid-turn correction requires cancelling the run
  or switching to the one-shot CLI.
- Constraints: implement ONLY slice 3 of #820. No local echo/dedupe (slice 4), no
  e2e (slice 5). `ctrl+s` stays copy; `ctrl+r` stays reserved for a future
  history-search binding. Re-grepped: `ctrl+g` is unbound anywhere under
  `cmd/harnesscli` (checked again on this branch). Strict TDD.

## Scope

- In scope:
  - `cmd/harnesscli/tui/keys.go`: `Steer key.Binding` on `ctrl+g` ("steer run"),
    included in `ShortHelp`/`FullHelp`.
  - `cmd/harnesscli/tui/model.go`: `key.Matches(msg, m.keys.Steer)` case gated on
    `m.runActive && m.RunID != "" && strings.TrimSpace(m.input.Value()) != ""` →
    fires `steerRunCmd`, clears the input, sets "Steering sent" status. Ungated
    press: status hint only ("No active run to steer" / "Type a message to steer
    into the run"), never an error. New `SteerErrorMsg` case mapping kinds to
    status text: `run_not_active` → "run already finished", `steering_buffer_full`
    → "steering buffer full — try again shortly", `not_found` → "run not found",
    others → "Steer failed: <err>". `SteerAcceptedMsg` consumed as a no-op
    (slice 4 hooks it for local echo).
  - Help: `buildHelpDialog` key list gains `ctrl+g`;
    `website/docs/cli/tui.md` keybinding table gains the `Ctrl+G` row.
- Out of scope: slices 4–5; any server/harness change; remapping `ctrl+s`.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: `website/docs/cli/tui.md` keybinding table (+ steering
  behavior note: step-boundary injection, buffer limit).
- Implementation notes to add after code: engineering-log entry.

## Test Plan (TDD)

- New failing tests (`cmd/harnesscli/tui/steer_key_test.go`, package `tui_test`):
  - ctrl+g during an active run with typed input → httptest server receives POST
    `/v1/runs/{id}/steer` with the input text (via the existing `runCmd` batch
    driver); input cleared; `RunActive()` still true; `cancelRun` NOT called;
    status shows "Steering sent".
  - ctrl+g while idle (no run) → no HTTP call, status hint.
  - ctrl+g with empty/whitespace input during a run → no HTTP call, status hint.
  - `SteerErrorMsg` kinds → status text (409/429/404/transport).
  - `SteerAcceptedMsg` → no crash, run still active.
- Regression guards: `keys_test.go`, `escape_test.go`, `cancel_test.go`,
  `ctrlc_server_cancel_test.go`, `clipboard_test.go`, `sse_events_test.go` stay
  green (esc cancel and ctrl+s copy unchanged).

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above).
- [x] Write failing tests first, watch them fail.
- [x] Implement binding + KeyMsg case + message mapping + help/docs.
- [x] gofmt + go vet clean.
- [x] `go test ./cmd/harnesscli/... -count=1` green.
- [x] Engineering-log entry.
- [ ] Commit, push `epic/820-tui-steering-s3`, open PR (do NOT merge).

---

## Slice 4 — immediate local echo for steered input with dedupe

## Context

- Problem: after slice 3, a TUI-originated steer only becomes visible at the next
  step boundary (when `steering.received` arrives — seconds later). The user gets
  no immediate confirmation their input went anywhere, and a naive immediate echo
  would double-render once the server confirmation lands.
- User impact: steered text must appear instantly on send, exactly once, with
  failures never leaving an orphan entry claiming a steer the server rejected.
- Constraints: implement ONLY slice 4. Server event contract fixed. Viewport
  supports only `ReplaceTailLines`/`ReplaceLineRange(start,...)` — no content
  search; assistant streaming re-renders via `ReplaceTailLines`, so position
  tracking for non-tail blocks is unsafe without hooking every append path
  (rejected as too invasive).

## Design (chosen after viewport exploration)

- Echo at keypress uses the FINAL slice-2 rendering (`steered ⟂ msg`) in the
  viewport — no in-place re-render is ever needed on confirm, so no fragile
  offset bookkeeping. Pending state lives in the transcript entry
  (`steered ⟂ msg (pending)`) and a small `pendingSteers` set
  (`{message, transcriptIdx}`) on the model.
- Dedupe at `steering.received`: payload matching a pending echo → strip the
  `(pending)` suffix from that transcript entry, consume the pending record,
  append nothing (exactly one marker). No match → slice-2 behavior (external
  steer, e.g. webhook).
- Failure (`SteerErrorMsg`): exact matching needs the prompt — add `Prompt` to
  `SteerErrorMsg` (slice-1 type, module-internal) and populate it in
  `steerRunCmd`. On failure: drop the pending record, delete the transcript
  entry (no orphan entry), and remove the viewport bubble ONLY when it is
  verifiably still at the tail — tracked by `steerEchoTail`, invalidated on any
  later `SSEEventMsg` (the only async append source during a run). Otherwise
  the bubble remains but the transcript (the durable/exported record) is clean
  and the status bar shows the failure.
- Pending state cleared wherever the transcript is reset (resetTranscriptView,
  new session, session switch). Leftover pendings at run end keep their
  `(pending)` mark — an honest record of an unconfirmed steer; out of scope.

## Scope

- In scope: `cmd/harnesscli/tui/model.go` (echo helper, pending set, key case,
  SSE dedupe, error cleanup, reset hooks), `messages.go` + `api.go`
  (`SteerErrorMsg.Prompt`).
- Out of scope: slice 5 (e2e); server/harness changes; viewport position
  tracking for non-tail in-place updates.

## Test Plan (TDD)

- New failing tests (`cmd/harnesscli/tui/steer_echo_test.go`, package `tui_test`):
  - echo visible immediately after ctrl+g, before any SSE event; transcript
    entry marked pending.
  - scripted `steering.received` with same content → exactly one marker in
    viewport, exactly one transcript entry, no longer pending.
  - `steering.received` with different content (external) → second marker/entry;
    external steer with no local echo → slice-2 behavior unchanged.
  - consumed dedupe: a second identical `steering.received` after confirmation
    appends a new marker (treated as external).
  - failure (409 and 429 via httptest, cmd driven end-to-end through Update) →
    transcript has no entry for the text (no orphan), viewport bubble removed
    (tail case), failure status shown.
- Regression: slice 1–3 steer suites + `go test ./cmd/harnesscli/... -count=1`.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above).
- [x] Write failing tests first, watch them fail.
- [x] Implement echo + dedupe + failure cleanup + `SteerErrorMsg.Prompt`.
- [x] gofmt + go vet clean.
- [x] `go test ./cmd/harnesscli/... -count=1` green.
- [x] Engineering-log entry.
- [ ] Commit, push `epic/820-tui-steering-s4`, open PR (do NOT merge).
