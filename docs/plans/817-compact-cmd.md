# Plan: epic #817 slice 1 — preserve-instruction in CompactRun and the compact endpoint

Parent epic: #817 (`/compact [instruction]` + visible compaction summary), part of #803.
This plan covers ONLY slice 1: `feat(harness): accept a preserve-instruction in CompactRun and the compact endpoint`.

## Context

- Problem: the manual compact endpoint and `Runner.CompactRun` accept only `{mode, keep_last}` — callers cannot steer what the summarizer keeps. kimi-code's `/compact [instruction]` steers the summarizer with a free-text hint.
- User impact: users compacting long sessions cannot protect critical context (e.g. "keep the SQL schema") from being distilled away.
- Constraints: auto-compaction (`autoCompactMessages`) must remain byte-identical to today (no instruction); strip mode ignores the instruction; minimal diff scoped to slice 1 — no summary return value, no TUI, no SSE changes (later slices).

## Scope

- In scope:
  - `CompactRunRequest.Instruction` (`internal/harness/runner.go`).
  - `instruction` JSON field on `POST /v1/runs/{id}/compact` decode struct (`internal/server/http_runs.go`), plumbed to `CompactRun`.
  - Instruction appended to the summarization prompt (as `Preserve especially: <instruction>`) in summarize/hybrid modes via the runner-backed summarizer.
  - Behavior tests first (strict TDD).
- Out of scope: slices 2–4 (summary in response, TUI `/compact`, transcript block); changing auto-compact thresholds/modes/fallback; changing the `tools.MessageSummarizer` interface (the instruction is carried by the runner-side summarizer wrapper, so the tools layer is untouched); `POST /v1/conversations/{id}/compact`.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (internal API additive field only).
- Spec docs to update before code: none.
- Implementation notes to add after code: none beyond this plan + folder index.

## Test Plan (TDD)

- New failing tests to add first (`internal/harness/runner_compact_instruction_test.go`):
  - summarize mode: instruction text appears in the provider-visible summarization prompt (capturing provider).
  - hybrid mode: instruction text appears in the summarization prompt.
  - strip mode: instruction never reaches the provider (no summarization call at all).
  - empty instruction: summarization prompt is byte-identical to today's fixed prompt (no-op).
  - auto-compact path: summarization prompt contains no preserve-instruction marker.
- New server test (`internal/server/http_run_compact_instruction_test.go`):
  - `POST /v1/runs/{id}/compact` with `{"mode":"summarize","instruction":"..."}` returns 200 and the instruction reaches the summarizer prompt.
- Existing tests to update: none (additive only).
- Regression tests required: existing `internal/server/http_compact_test.go`, `http_context_compact_test.go`, `internal/harness` compaction tests must pass unmodified.

## Cross-Surface Impact Map

- Config: None — no new config knob; instruction is per-request.
- Server API: additive optional `instruction` field on the compact request body; response shape unchanged.
- TUI state: None — slice 3 owns TUI.
- Regression tests: listed above.

## Implementation Checklist

- [x] Define acceptance criteria in tests (per epic slice 1).
- [ ] Write failing tests first.
- [ ] Implement minimal code changes.
- [ ] gofmt + go vet clean.
- [ ] `go test ./internal/harness/ ./internal/server/ -count=1` green.
- [ ] Update docs/plans/INDEX.md.
- [ ] Commit, push `epic/817-compact-cmd`, open PR (no merge).

## Risks and Mitigations

- Risk: changing the shared `MessageSummarizer` interface ripples into the tools layer.
  - Mitigation: carry the instruction on the runner-side `runnerMessageSummarizer` wrapper; interface untouched.
- Risk: empty/whitespace instruction altering today's prompt.
  - Mitigation: trim; when empty use the exact existing prompt string; pinned by the no-op test.
