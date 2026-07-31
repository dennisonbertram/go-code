# Issue #1058: Restore the TUI waiting-conversation overlay

## Context

- Governing issue: https://github.com/dennisonbertram/go-code/issues/1058
- Problem: the TUI SSE decoder drops the envelope's top-level `run_id`, while
  the `run.waiting_for_user` model handler expects that ID inside `payload`.
- User impact: a correctly paused AskUserQuestion run is invisible and cannot
  be continued through the TUI.
- Constraints: repair the existing bridge/model seam only; preserve the public
  SSE and input APIs, reconnect behavior, authentication, and other clients.

## Scope

- In scope: canonical run-ID retention in `SSEEventMsg`, the waiting handler,
  a production-shape SSE-to-visible-overlay-to-answer-to-continuation
  regression, exact wait call/generation correlation for asynchronous pending
  fetches, replay/reconnect compatibility, and durable logs.
- Out of scope: PR #1055's server ordering work, native GUI, web, ACP,
  approvals, callbacks, cron execution, provider/model/tool changes, and
  completion claims for epics #1000 or #1010.

## Documentation Contract

- Feature status: implemented and locally verified; review promotion pending.
- Public docs affected: none; the intended AskUserQuestion behavior is already
  documented.
- Implementation notes: update engineering, observational, and system logs
  with red/green/full evidence.

## Test Plan (TDD)

- First red: drive a real top-level-`run_id` waiting envelope through
  `StartSSEBridge`, require GET `/input`, visible options, Enter submission,
  `run.resumed`, and later assistant output.
- Replay control: exercise the same decoder with a resume event ID and prove
  the canonical run ID survives without a payload copy.
- Concurrency regressions: hold a real pending GET open across `run.resumed`
  and across a newer same-run waiting call; require the late result to be
  discarded in both cases.
- Green/race: focused AskUser/SSE tests, complete TUI package normal and race,
  then `./scripts/test-regression.sh`.
- Real path: localhost SSE/input smoke through the production bridge/model and
  inspect the view, answer request, and continued transcript.

## Cross-Surface Impact Map

See `2026-07-31-issue-1058-tui-waiting-overlay-impact-map.md`.

## Implementation Checklist

- [x] Contract-complete bug issue created.
- [x] Current architecture and open PR ownership searched.
- [x] Production-shape failure independently reproduced.
- [x] Cross-surface impact map completed.
- [x] Write and observe the expected failing acceptance regression.
- [x] Retain and consume the envelope run ID once.
- [x] Keep replay/reconnect and adjacent event decoding green.
- [x] Update durable logs with final evidence.
- [x] Pass focused normal/race and full regression.
- [x] Commit, push, open PR #1061 with `Closes #1058`, and verify hosted checks.
- [x] Observe deterministic red regressions for resume and supersession races.
- [x] Correlate pending success/error messages to exact wait call/generation.
- [x] Rerun focused/package normal and race plus full foreground regression.
- [ ] Push the review fix, resolve/reply to comment 3687057509, and request
  Codex re-review at the new exact head.

## Risks and Mitigations

- Risk: introducing a second run-ID source or changing payloads.
  Mitigation: add `RunID` to the internal decoded message and leave `Raw` as
  the untouched event payload.
- Risk: reconnect replays fetch the prompt twice.
  Mitigation: pin exact event delivery/fetch counts and existing
  `Last-Event-ID` behavior.
- Risk: an asynchronous GET completes after resume or after a newer wait.
  Mitigation: model-owned generations plus exact run/call checks gate both
  pending successes and failures before they can mutate overlay state.
- Risk: synthetic tests continue encoding the wrong wire shape.
  Mitigation: the acceptance fixture starts at the actual SSE envelope.
