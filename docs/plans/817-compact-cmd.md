# Plan: epic #817 — /compact preserve-instruction + compaction summary

Parent epic: #817 (`/compact [instruction]` + visible compaction summary), part of #803.

- Slice 1 (`feat(harness): accept a preserve-instruction in CompactRun and the compact endpoint`): **implemented**, merged via PR #836.
- Slice 2 (`feat(harness): return the compaction summary from the compact endpoint`): **implemented**, merged via PR #860.
- Slice 3 (`feat(harnesscli): /compact [instruction] slash command`): **implemented**, merged via PR #884.
- Slice 4 (`feat(tui): compaction summary block in the transcript with ctrl+o toggle`): **in implementation** (branch `epic/817-compact-cmd-s4`).

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

---

## Slice 3 (in implementation): /compact [instruction] slash command

### Context

- Problem: users cannot trigger compaction from the TUI; only the raw HTTP endpoint exists (slices 1–2).
- Constraints: `/compact` always requests `hybrid` mode (strip stays raw-API-only); requires an active run; help and slash completion are registry-driven so the registry entry covers both; the registry completeness/builtin tests (`cmd_parser_test.go`) and the auth-coverage table (`api_auth_test.go`) are the snapshots the epic mandates updating.

### Scope

- In scope:
  - `compact` entry in `builtinCommandEntries()` (`cmd/harnesscli/tui/cmd_parser.go`).
  - `executeCompactCommand` beside `executeCancelCommand` (`cmd/harnesscli/tui/model.go`): active-run guard, args joined verbatim into the instruction, fires `compactRunCmd`; `CompactResultMsg` Update case reports "Compacted context — N messages removed" / "compact failed: ...".
  - `compactRunCmd` (`cmd/harnesscli/tui/api.go`): POST `{"mode":"hybrid","instruction":...}` to `/v1/runs/{id}/compact` via `newHarnessRequest`; decodes `{ok,messages_removed,mode,summary}` into `CompactResultMsg` (`messages.go`) — `Mode`/`Summary` carried for slice 4.
  - Snapshot updates: `TestTUI041_BuiltinCommandsRegistered` + `TestTUI364_RegistryCompleteness` lists; `api_auth_test.go` audit comment + table case; `TestBuildCommandRegistry_SlashCompleteShowsCommands` first-window list (`compact` takes a first-window slot, pushing `dashboard` below the fold).
- Out of scope: slice 4 (transcript block, ctrl+o, auto_compact SSE); mode selection flag; one-shot CLI UX.

### Test Plan (TDD)

- New failing tests first (`cmd/harnesscli/tui/compact_command_test.go`):
  - no active run → usage/active-run status, zero HTTP requests.
  - active run → POST hybrid + instruction joined verbatim; success status reports messages removed; msg carries mode/summary.
  - bare `/compact` → empty instruction sent.
  - 409 → "compact failed" status.
  - registry/help/slash-complete visibility.
- Updated snapshot tests: registry lists + auth table (watched red before implementation).
- Regression: `go test ./cmd/harnesscli/... -count=1`.

### Implementation Checklist

- [x] Write failing tests first (watched red: build failures on undefined `executeCompactCommand`/`compactRunCmd`/`CompactResultMsg` + registry list failures).
- [x] Implement minimal code changes.
- [ ] gofmt + go vet clean.
- [ ] `go test ./cmd/harnesscli/... -count=1` green.
- [ ] Commit, push `epic/817-compact-cmd-s3`, open PR (no merge).

---

## Slice 4 (in implementation): compaction summary block in the transcript with ctrl+o toggle

### Context

- Problem: compactions are invisible — slice 3's `/compact` shows only a transient status line, and `auto_compact.*` SSE events are unhandled in the TUI dispatch.
- Constraints: ctrl+o is already dual-purpose (active-tool expansion, then idle plan-mode toggle at `model.go` `keys.ExpandTool`); the compaction toggle must slot between them, never rebind. Block rendering reuses the tool-card in-place update pattern (tracked line offsets + `ReplaceLineRange` + start shifting), not the tooluse component itself (it is tool-call-specific).

### Scope

- In scope:
  - `cmd/harnesscli/tui/compaction_block.go` (new): `compactionBlock` (title/details/expanded/lineStart/lineCount), collapsed `▸` / expanded `▾` + `⎿` detail rendering with width-wrapped details, model helpers `addCompactionBlock` / `findCompactionBlock` / `updateCompactionBlock` (shifts `toolLineStarts` and later blocks on line-count delta) / `toggleLatestCompactionBlock` / `clearCompactionBlocks`.
  - `model.go`: `CompactResultMsg` success renders a collapsed block (`Compacted context — N messages removed`, details = mode + summary); SSE dispatch handles `auto_compact.started` (in-progress block, remembered via `pendingAutoCompactID`) and `auto_compact.completed` (updates the pending block in place, or appends when missed; error payload renders a failure title); ctrl+o chain gains the block toggle between active-tool and plan-mode; `clearCompactionBlocks()` called at every viewport rebuild (`resetTranscriptView`, `ClearMsg`, `executeNewSessionCommand`, `SessionPickerSelectedMsg`); help entries updated.
  - `keys.go`: ExpandTool help text mentions compaction.
- Out of scope: persisting blocks to the store / resumed sessions; toggling older (non-latest) blocks; changing auto-compact behavior itself.

### Test Plan (TDD)

- New failing tests first (`cmd/harnesscli/tui/compaction_block_test.go`):
  - manual result renders collapsed block (title visible, summary hidden, slice-3 status line preserved).
  - ctrl+o expands then collapses; with a block present ctrl+o does NOT toggle plan mode.
  - auto_compact started→completed renders one block updated in place with before/after tokens; completed-without-started appends.
  - precedence regression: active tool + block → ctrl+o expands the tool, block stays collapsed.
  - plan-mode idle toggle regression: covered by existing `planmode_test.go` (unmodified, must stay green).
- Regression: `go test ./cmd/harnesscli/... -count=1`.

### Implementation Checklist

- [x] Write failing tests first (watched red: `m.compactionBlocks undefined`).
- [x] Implement minimal code changes.
- [ ] gofmt + go vet clean.
- [ ] `go test ./cmd/harnesscli/... -count=1` green.
- [ ] Commit, push `epic/817-compact-cmd-s4`, open PR (no merge).
