# Plan: headless exit-code contract — epic #823

## Slice status

- Slice 1 (docs contract): **merged** (PR #824, branch `epic/823-exit-codes`).
- Slice 2 (map terminal events to exit codes): **merged** (PR #861, branch `epic/823-exit-codes-s2`).
- Slice 3 (exit 3 on blocked headless runs): **merged** (PR #882, branch `epic/823-exit-codes-s3`).
- Slice 4 (e2e assertions + per-command docs): **this branch** (`epic/823-exit-codes-s4`), branched from `origin/main` after the Slice 3 merge.

## Slice 4 scope

- New `test/e2e/exit_codes_test.go`: builds the real `harnesscli` binary once (`go build`) and drives it over real HTTP+SSE against the in-process harnessd (`newTestServer` patterns from `helpers_test.go`) backed by `internal/fakeprovider` — content turn → exit 0 (`run.completed`); `ExhaustError` → exit 2 (`run.failed`); `Hang` turn + `POST /v1/runs/{id}/cancel` mid-run → exit 6 (`run.cancelled`); plus an unreachable-server client-error pin (exit 1). Assertions are on the process exit code (`$?`), not stdout text.
- Wrapper propagation: `TestGoCodeScriptPropagatesHarnessCLIExitCode` in `cmd/harnesscli/go_code_script_test.go` — stubbed `harnesscli` exits 2/6/3 in prompt and cli modes, with and without the wrapper-started-server `EXIT` trap armed; the script's exit code must equal the stub's, and the stub must actually have run.
- Docs: per-command exit-code table in `website/docs/cli/harnesscli.md` (streaming mode + non-streaming 0/1 note), headless-scripting `$?` section in `website/docs/cli/go-code-wrapper.md`, exit-code note + `-no-stream` clarification in `website/docs/reference/cli-flags.md` — all linking the contract page.
- Out of scope per the slice spec: blocked (exit 3) e2e — the CLI has no permissions flag, so approval-blocked runs cannot be driven through `harnesscli`; blocked coverage stays with the Slice 3 unit tests. No code changes.

## Slice 4 test plan

- The e2e assertions are the tests; they validate already-merged Slice 2/3 behavior over the real binary (a red result would mean the contract is broken, not that a test needs adjusting).
- `go test ./test/e2e/... -count=1` and `go test ./cmd/harnesscli/... -count=1` green; `go test ./internal/harness/... -count=1` green; gofmt/vet clean; `npm run build` green for the edited docs pages.

## Slice 3 record (completed)

- `cmd/harnesscli/exitcodes.go`: `isBlockedEvent` (run.waiting_for_user, tool.approval_required, plan.approval_required) and `blockedEventReason` (question-blocked vs approval-blocked wording). The contract's blocked signals, in the same single-source-of-truth file as the code table.
- `cmd/harnesscli/main.go`: `runBlockedError`; `processSSEBlock` returns it when a blocked signal arrives while `stdinIsTerminal()` is false (interactive stdin: stream unchanged, stays open — ask-user wiring is the other epic's scope); `run()` maps it to `exitBlocked` after `reportRunBlocked` prints the run ID, reason, and `harnesscli continue <run-id>` resume command to stderr. Terminal detection reuses the existing injectable `stdinIsTerminal` double (`cmd/harnesscli/plugins.go:107`); no new TTY dependency.
- `cmd/harnesscli/runctl.go` `runContinue()`: same blocked mapping (contract applies to streaming continue).
- Server-side run never auto-cancelled on the blocked path; blocked event line still printed to stdout (every-event-printed contract preserved).
- Docs: contract page blocked section + status table now describe implemented behavior; engineering-log entry.

## Slice 3 test plan (TDD)

- Failing-first (`cmd/harnesscli/exitcodes_test.go`): all three blocked signals × (non-interactive stdin → exit 3, stderr names run ID + reason + event + resume command, no cancel POST reaches the server, blocked event line still on stdout; simulated TTY → stream continues and the subsequent terminal event's code is returned); `runContinue()` blocked → 3; unit tables for `isBlockedEvent` / `blockedEventReason`.
- Servers hold the SSE stream open after the blocked signal so a regression to "stream forever" hangs the test, not just fails an assertion.
- Regression guard: slice-2 exit-code tests and all existing `cmd/harnesscli` tests unchanged and green.
- Verification: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1`, gofmt, go vet.

## Slice 2 record (completed)

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
