# Plan: headless exit-code contract — epic #823

## Slice status

- Slice 1 (docs contract): **merged** (PR #824, branch `epic/823-exit-codes`).
- Slice 2 (map terminal events to exit codes): **this branch** (`epic/823-exit-codes-s2`), branched from `origin/main` after the Slice 1 merge.
- Slices 3–4 (blocked detection; e2e + per-command docs): future branches.

## Slice 2 scope

- New `cmd/harnesscli/exitcodes.go`: constants `exitSuccess`(0) / `exitClientError`(1) / `exitRunFailed`(2) / `exitBlocked`(3) / `exitCancelled`(6) / `exitInterrupted`(130) and `exitCodeForTerminalEvent(harness.EventType) int`; unknown/empty/non-terminal events map to 1 (defensive non-zero default). `exitBlocked` is defined here and wired in Slice 3.
- `cmd/harnesscli/main.go` `run()`: terminal return becomes `exitCodeForTerminalEvent(...)`; literal `1` returns in `run()` and `1`/`130` in `handleStreamError()` replaced with the named constants (one source of truth). `terminal_event=` stdout line and interrupt-cancel behavior untouched.
- `cmd/harnesscli/runctl.go` `runContinue()`: same terminal-event mapping; `-no-stream` stays 0/1 and never opens the event stream.
- Docs: contract page status table updated (failed 0→2 and cancelled 0→6 now implemented; blocked→3 still slice 3); engineering-log entry.

## Slice 2 test plan (TDD)

- Failing-first (`cmd/harnesscli/exitcodes_test.go`): `exitCodeForTerminalEvent` table (all three terminal events + non-terminal/unknown/empty → 1); constant-value pins (0/1/2/3/6/130); `run()` against `httptest` SSE streams ending completed/failed/cancelled → 0/2/6 with `run_id=`/`terminal_event=` stdout preserved; `runContinue()` same; `-no-stream` exits 0 without requesting `/events`.
- Regression guard: existing happy-path tests (`TestRunCreatesAndStreamsToCompletion`, `TestRunContinue_PostsPromptAndStreamsNewRun`, …) must pass unchanged; nothing may rely on "failed run exits 0".
- Verification: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1`, gofmt, go vet.

## Slice 1 record (completed)

- Problem: `harnesscli -prompt ...` exited 0 for every terminal run state (including `run.failed` and `run.cancelled`), so shell scripts and CI could not branch on run outcomes without parsing stdout.
- Delivered: `website/docs/reference/exit-codes.md` contract page (full table, kimi 0/3/6 rationale, blocked signals, `max_turns.exhausted`/`run.cost_limit_reached` semantics, reserved goal mapping, current-vs-contracted table, traceability), cross-links from `website/docs/cli/harnesscli.md` and `website/docs/reference/events-catalog.md`, validated by `npm run build` (`onBrokenLinks: 'throw'`).

## Cross-Surface Impact Map

- None — CLI exit-path only; no provider/model flows, gateway routing, model catalogs, API-key management, or server/TUI provider plumbing touched.

## Risks and Mitigations

- Risk: a caller scripts against the old always-0 behavior.
- Mitigation: intentional breaking fix per the ratified contract; stdout lines unchanged so log parsers keep working; contract page and engineering log record the behavior change.
