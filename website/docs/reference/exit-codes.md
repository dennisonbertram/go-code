---
title: "Exit Codes"
sidebar_label: "Exit Codes"
sidebar_position: 9
---

import { Callout } from '@site/src/components/ui';

This page is the **exit-code contract for headless `harnesscli` usage** — the one-shot streaming mode (`harnesscli -prompt ...`) and the streaming `continue` subcommand. It exists so shell scripts and CI pipelines can branch on a run's outcome via `$?` instead of parsing stdout.

<Callout type="warning">
**Contract status.** This page ratifies the target contract (tracking issue: epic #823, part of the kimi-code parity program #803). The implementation lands in later slices of that epic — see the [current vs. contracted behavior](#current-vs-contracted-behavior) table below for exactly which process outcomes change and which are already in effect. Until the implementation slices merge, treat the **Current** column as ground truth.
</Callout>

The contract adopts [kimi-code](https://github.com/MoonshotAI/kimi-code)'s headless codes where semantics align: kimi's `kimi -p` exits `0` when a goal completes, `3` when it blocks, `6` when it pauses, and non-zero on turn failure. Aligning on the same numbers means scripts written against one CLI keep working against the other.

---

## The contract

Applies to: `harnesscli -prompt ...` (default streaming run mode) and `harnesscli continue <run-id> <prompt>` (streaming). For everything else, see [command coverage](#command-coverage).

| Exit code | Meaning | Trigger |
|---|---|---|
| `0` | Success | Terminal event `run.completed` — the run finished. |
| `1` | Client-side error | Bad flags, missing prompt, connection/HTTP failure, stream transport error. Also the defensive default for an unknown or empty terminal event type, so a scripting caller never mistakes an unrecognized outcome for success. |
| `2` | Run failed | Terminal event `run.failed` — a turn failed server-side (satisfies kimi's "non-zero on turn failure"). |
| `3` | Blocked | The run cannot proceed without input it will never get headlessly: `run.waiting_for_user`, `tool.approval_required`, or `plan.approval_required` observed while stdin is **not** a terminal. See [blocked runs](#blocked-runs-exit-3). |
| `6` | Paused / cancelled | Terminal event `run.cancelled`. Work is interrupted but resumable via `harnesscli continue <run-id> <prompt>`. |
| `130` | Interrupted | SIGINT/SIGTERM while streaming (`128 + SIGINT`, the conventional shell code). The CLI best-effort cancels the still-executing server-side run before exiting. |

### kimi-code alignment rationale

- `0` for a completed run/goal is identical in both CLIs.
- `3` (blocked) and `6` (paused) reuse kimi's exact codes for the same semantics: a headless caller can distinguish "needs a human" (`3`) from "stopped but resumable" (`6`) without reading any output.
- `2` for `run.failed` satisfies kimi's "non-zero on turn failure" guarantee while staying distinct from `1` (the failure is server-side, not a client usage or transport problem), so `if [ $? -eq 1 ]` retry-the-invocation logic keeps its current meaning.
- `1` and `130` are go-code's current behavior and are unchanged; `130` is the standard shell convention both CLIs follow.

---

## Run terminal events

The terminal event set is exactly three event types — `run.completed`, `run.failed`, `run.cancelled` — as defined by `IsTerminalEvent` (`internal/harness/events.go:472`). After a terminal event the SSE stream ends; the exit code is derived from which terminal event arrived:

| Terminal event | Source constant | Exit code |
|---|---|---|
| `run.completed` | `EventRunCompleted` (`internal/harness/events.go:20`) | `0` |
| `run.failed` | `EventRunFailed` (`internal/harness/events.go:21`) | `2` |
| `run.cancelled` | `EventRunCancelled` (`internal/harness/events.go:34`) | `6` |

Two non-terminal events deserve explicit call-outs because they change how a script should interpret the terminal event that follows:

<Callout type="info">
**`max_turns.exhausted` is not terminal.** `EventMaxTurnsExhausted` (`internal/harness/events.go:384`) is emitted when an agent exhausts its turn budget, and the run then terminates with `run.failed` (`reason=max_turns_exhausted`). It carries no exit code of its own — it surfaces through the subsequent terminal event's code, which is `2`.
</Callout>

<Callout type="info">
**Cost-limit runs exit `0`.** `run.cost_limit_reached` (`EventRunCostLimitReached`, `internal/harness/events.go:27`) terminates the run with `run.completed` — **not** `run.failed` — when the `max_cost_usd` ceiling is hit. The process therefore exits `0` even though the run stopped early. Scripts that treat "hit the cost ceiling" as a failure must watch for the `run.cost_limit_reached` event line on stdout; the exit code alone does not distinguish it from an ordinary completion.
</Callout>

---

## Blocked runs (exit 3)

A run is **blocked** when it cannot make progress without input a headless caller will never provide. Two families of signals indicate this:

| Signal | Source constant | Run status while blocked | Kind |
|---|---|---|---|
| `run.waiting_for_user` | `EventRunWaitingForUser` (`internal/harness/events.go:22`) | `waiting_for_user` (`RunStatusWaitingForUser`, `internal/harness/types.go:337`) | Question-blocked: the run invoked `ask_user_question`. |
| `tool.approval_required` | `EventToolApprovalRequired` (`internal/harness/events.go:69`) | `waiting_for_approval` (`RunStatusWaitingForApproval`, `internal/harness/types.go:338`) | Approval-blocked: a tool call needs operator approval. |
| `plan.approval_required` | `EventPlanApprovalRequired` (`internal/harness/events.go:83`) | `waiting_for_approval` (`internal/harness/types.go:338`) | Approval-blocked: a plan needs operator approval. |

There is no dedicated "waiting-for-approval" run event — the approval-required events are the signal, and the run status transitions to `waiting_for_approval`.

Contracted behavior when a blocked signal is observed in one-shot or streaming `continue` mode:

- **stdin is not a terminal** (piped/redirected, the CI case): the CLI prints the blocked reason and run ID to **stderr**, stops streaming, and exits `3`. The server-side run is left intact — no auto-cancel — so an operator can resume it later with `harnesscli continue <run-id> <prompt>` or by answering via `POST /v1/runs/{id}/input` (questions) or `/approve` / `/deny` (approvals).
- **stdin is a terminal**: behavior is unchanged — the stream stays open and no exit-3 shortcut is taken. Interactive answer wiring in the streaming loop is a separate epic's scope; that epic must preserve exit `3` for non-interactive stdin.

The terminal check uses the same `term.IsTerminal` test the `--tui` path already uses (`cmd/harnesscli/main.go:517`).

---

## Command coverage

| Command | Covered by this contract? | Exit codes |
|---|---|---|
| `harnesscli -prompt ...` (streaming run mode) | **Yes** | `0`, `1`, `2`, `3`, `6`, `130` per the table above. |
| `harnesscli continue <run-id> <prompt>` (streaming, the default) | **Yes** | Same mapping as the one-shot path. |
| `harnesscli continue -no-stream ...` | No (never observes events) | Prints `run_id=<id>` and exits `0`; `1` on client error. |
| `list`, `status` / `show`, `cancel`, `replay`, `search` | No (non-streaming) | Unchanged: `0` on success, `1` on error. This contract documents but does not change them. |
| `--tui` | No | Interactive TUI exit behavior is out of scope; the contract covers headless/streaming mode only. |

---

## Goal statuses (reserved codes)

Goals (`internal/goals/goals.go:25-30`) have their own lifecycle statuses: `pending`, `running`, `verifying`, `completed`, `failed`, `cancelled`. The contract maps them onto the same codes so a future goal-scoped headless surface inherits the semantics without renumbering:

| Goal status | Exit code | Notes |
|---|---|---|
| `completed` | `0` | Goal verified complete. |
| `failed` | `2` | Goal verification or execution failed. |
| `cancelled` | `6` | Goal cancelled; resumable. |
| (blocked — future) | `3` | **Reserved.** No blocked goal status exists today; a goal waiting on user input maps here when one is added. |
| (paused — future) | `6` | **Reserved.** Paused goals share the cancelled/resumable code, matching kimi's pause semantics. |

<Callout type="warning">
This goal mapping is **documentation-only in this epic**. There is no goal-scoped headless surface to implement it against today: no `/v1/goals` HTTP route and no goal CLI subcommand exist — `internal/goals` is reachable only through the in-run `goals` tool. A future goal epic must add the runtime mapping and its tests when it introduces that surface; until then no runtime goal exit codes are guaranteed.
</Callout>

---

## Current vs. contracted behavior

This table is the reviewer-facing diff of process outcomes. Rows marked **unchanged** are already in effect; rows marked **changes** land with the implementation slices of epic #823.

| Outcome | Current exit code | Contracted exit code | Status |
|---|---|---|---|
| `run.completed` (success) | `0` | `0` | Unchanged |
| Client/usage/transport error | `1` | `1` | Unchanged |
| SIGINT/SIGTERM interrupt | `130` | `130` | Unchanged |
| `run.failed` | `0` | `2` | **Changes** — today every terminal event exits `0` (`cmd/harnesscli/main.go:219-220`, `cmd/harnesscli/runctl.go:324-330`) |
| `run.cancelled` | `0` | `6` | **Changes** — same unconditional-`0` paths |
| Blocked, stdin non-interactive | (streams forever) | `3` | **Changes** — today the CLI waits on the SSE stream indefinitely |

### stdout contract is unchanged

Only the process exit code changes. The existing stdout lines — `run_id=<id>` when the run is created and `terminal_event=<event_type>` at the end of the stream — are preserved exactly, so existing log parsers keep working. Exit-code assertions and stdout parsing compose: a script may still read `terminal_event=run.failed` while also getting `2` from `$?`.

### Wrapper propagation

The `go-code` wrapper script needs no changes and propagates the code unchanged: in `scripts/go-code.sh` the `harnesscli` invocation is the last command of `main` for both `prompt` and `cli` modes (`scripts/go-code.sh:393`, `:399`), and `main "$@"` is the script's final line (`:404`), so the `harnesscli` exit status becomes the wrapper's own exit status (the `stop_server` EXIT trap does not override it). `go-code prompt "..." ; echo $?` therefore yields the same code as calling `harnesscli` directly.

---

## Traceability

Every code in the contract traces to an existing event constant, run status, or current CLI behavior:

| Exit code | Traces to |
|---|---|
| `0` | `EventRunCompleted` (`internal/harness/events.go:20`); current `run()` return at `cmd/harnesscli/main.go:220` |
| `1` | Current usage/transport error returns (`cmd/harnesscli/main.go:164`, `:176`, `:183`, `:211`, `:236`; `cmd/harnesscli/runctl.go`) |
| `2` | `EventRunFailed` (`internal/harness/events.go:21`) |
| `3` | `EventRunWaitingForUser` (`internal/harness/events.go:22`), `EventToolApprovalRequired` (`internal/harness/events.go:69`), `EventPlanApprovalRequired` (`internal/harness/events.go:83`); statuses `RunStatusWaitingForUser` / `RunStatusWaitingForApproval` (`internal/harness/types.go:337-338`) |
| `6` | `EventRunCancelled` (`internal/harness/events.go:34`) |
| `130` | Current `handleStreamError` interrupt path (`cmd/harnesscli/main.go:233`) |

---

## Next steps

- **CLI commands** — See the [harnesscli Reference](/docs/cli/harnesscli) for the streaming run mode and subcommand details.
- **Events** — See the [Event Catalog](/docs/reference/events-catalog) for every SSE event type and payload, including the terminal events this contract maps.
- **Flags** — See the [CLI Flag Reference](/docs/reference/cli-flags) for `-prompt`, `-no-stream`, and the rest of the flag surface.
