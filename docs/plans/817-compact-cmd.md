# Plan: epic #817 — /compact preserve-instruction + compaction summary

Parent epic: #817 (`/compact [instruction]` + visible compaction summary), part of #803.

- Slice 1 (`feat(harness): accept a preserve-instruction in CompactRun and the compact endpoint`): **implemented**, merged via PR #836.
- Slice 2 (`feat(harness): return the compaction summary from the compact endpoint`): **in implementation** (branch `epic/817-compact-cmd-s2`).

## Slice 1 (done)

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

- Feature status: `implemented` (merged via PR #836)
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

---

## Slice 2 (in implementation): return the compaction summary from the compact endpoint

### Context

- Problem: `CompactRunResult` and the compact endpoint report only `messages_removed` — clients (TUI slice 4) cannot render what was distilled.
- Constraints: additive response fields only (`ok`/`messages_removed` unchanged); no SSE changes; strip mode returns an empty summary; existing `internal/server/http_compact_test.go` and `http_context_compact_test.go` pass unmodified.

### Scope

- In scope:
  - `CompactRunResult.Mode` (resolved mode) and `CompactRunResult.Summary` (`internal/harness/runner.go`).
  - `compactMessagesHTTP` returns the summary produced by `compactSummarizeHTTP`/`compactHybridHTTP` (previously discarded); strip and no-op paths return "".
  - `autoCompactMessages` discards the summary — behavior unchanged.
  - Endpoint response gains `mode` and `summary` keys (`internal/server/http_runs.go`).
  - Mechanical signature updates at existing `compactMessagesHTTP` test call sites.
- Out of scope: slices 3–4 (TUI command, transcript block); auto-compact SSE payloads; persisting summaries.

### Test Plan (TDD)

- New failing tests first:
  - `internal/harness/runner_compact_summary_test.go`: summarize returns the scripted summary + mode; hybrid returns the summary; strip (and default-mode-resolves-to-strip) returns empty summary with `MessagesRemoved > 0` and the resolved mode.
  - `internal/server/http_run_compact_instruction_test.go`: summarize endpoint returns `{ok, messages_removed>0, mode:"summarize", summary:"..."}`; strip endpoint returns `mode:"strip"`, `summary` key present and empty.
- Existing tests to update: `compactMessagesHTTP` call sites in `runner_context_compact_test.go` (mechanical, 3-value return).
- Regression: all pre-existing harness/server compaction tests pass.

### Implementation Checklist

- [x] Write failing tests first (watched red: runner build failure on missing fields; server missing `mode`/`summary` keys).
- [x] Implement minimal code changes.
- [ ] gofmt + go vet clean.
- [ ] `go test ./internal/harness/ ./internal/server/ -count=1` green.
- [ ] Commit, push `epic/817-compact-cmd-s2`, open PR (no merge).
