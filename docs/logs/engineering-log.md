# Engineering Log

## 2026-07-21 (ACP Server Mode — Epic #806, Slice 3)

- Prompt turns now stream live output to the editor: `assistant.message.delta` -> `agent_message_chunk`, `assistant.thinking.delta` -> `agent_thought_chunk` (payload field `content`), `tool.call.started` -> `tool_call` (`status: in_progress`, `title` = tool name, `kind` via a tool-name table), `tool.call.completed` -> `tool_call_update` (`completed`, or `failed` when the payload carries `error`; output included as content). `toolCallId` is the harness `call_id`, stable across start/complete.
- `RunsClient.WaitTerminal` generalized to `WatchRun(ctx, runID, onEvent)`; oversized SSE lines (cap now a test-shrinkable var) are drained and their event skipped with a logged warning instead of corrupting the stream.
- Backpressure discipline: one bounded (256) queue per turn with a single writer goroutine, so the SSE reader never blocks on a slow editor. Coalescing/drops trigger only on a FULL queue — same-kind deltas merge into the tail, other deltas drop (counted + logged), lifecycle updates evict buffered deltas but are never dropped. (First cut coalesced whenever the writer lagged, which made healthy streams lose chunk granularity and broke the golden ordering test; coalescing is now strictly an anti-overflow mechanism.)
- The prompt handler closes the queue at the terminal event and drains it fully before writing the `session/prompt` response, per the spec's updates-before-result rule.
- Validation: strict TDD (red: `undefined: runEvent`/`translateRunEvent`). Golden acceptance: scripted client observes two message chunks, a thought chunk, `tool_call`, `tool_call_update` (stable `toolCallId`) in exact order before the `end_turn` result. `go test ./internal/acp/... -count=1` green.

## 2026-07-21 (Agent Swarm — Epic #808, Slice 3)

- Added the deferred `agent_swarm` tool
  (`internal/harness/tools/deferred/agent_swarm.go`): `TierDeferred`,
  `ActionExecute`, `Mutating: true`, params `prompt_template`/`items`/
  `resume_agent_ids` plus profile/model overrides resolved exactly like
  `start_subagent`; returns the aggregated report via `MarshalToolResult`.
- Import-cycle design: `harness` and `deferred` cannot import `subagents`
  (subagents imports harness). Mirror types (`SwarmRequest`/`SwarmReport`) +
  `SwarmRunner` interface + `AgentSwarmToolName` const live in
  `internal/harness/tools` (same pattern as `SubagentManager`); the adapter
  `subagents.NewToolSwarmRunner` maps both directions; wiring happens in
  `cmd/harnessd/runtime_container.go` next to the InlineManager.
- Sole-call rule in `runner_step_engine.go`: a response containing
  `agent_swarm` plus any other call executes the first swarm call and rejects
  every other call with a corrective error naming the rule (a second
  `agent_swarm` call is rejected too).
- Nested-swarm prevention: new per-run `DeniedTools` denylist plumbed
  `tools.SubagentRequest` -> `subagents.Request` -> `harness.RunRequest` ->
  runState (carried over on continuation). `filteredToolsForRun` never offers
  denied tools (even when activated) and the step-engine call gate blocks
  them outright. The swarm sets `DeniedTools=[agent_swarm]` on every member.
- Approval flow: no new code needed — the existing destructive-policy path
  consults `Registry.IsMutating`, and the tool declares `Mutating: true`;
  a test proves the call pauses for approval and runs after approval.
- Description file `descriptions/agent_swarm.md` documents the sole-call
  rule, the 128 cap, the 5->+1/700ms ramp, the env cap, and resume semantics.
- TDD: tests landed first across four files (deferred tool contract, adapter
  mapping, runner sole-call/denied/approval gates, fakeprovider full-stack
  e2e showing a 4-item swarm and one aggregated tool result); all failed on
  undefined symbols before implementation.
- Validation: package tests + `-race` green for subagents, harness, tools,
  deferred, harnessd; full regression suite (see PR body).

## 2026-07-20 (Image Attachments Through Run Plumbing — Epic #818 Slice 3)

- Added `harness.ContentBlock{Type, MediaType, Data}` (base64), additive
  `Message.Blocks` and `RunRequest.Attachments` (text-only messages and
  callers unchanged; `Message.Clone` deep-copies Blocks).
- `Runner.StartRun` validates attachments (type `image`, media type
  `image/png`|`image/jpeg`, non-empty valid base64) and enforces the
  server-side modality gate: the effective model+provider is resolved via
  the provider registry's catalog and known text-only models are rejected
  with an actionable error; unknown models/modalities (nil registry,
  discovered models) are allowed. HTTP callers get a synchronous 400
  `invalid_request` through the existing `handlePostRun` error path.
- `runner.execute` builds the user message with the prompt text plus Blocks;
  blocks reach the provider `CompletionRequest` (asserted via
  `fakeprovider.LastRequest()`). Snapshot/history stays text-only
  (documented limitation: blocks do not persist across continuation runs).
- TUI send path: pending chips are base64-encoded into the `POST /v1/runs`
  body on submit and consumed (state + temp dirs); an encode failure aborts
  the submit, restores the text, and keeps the chips. `startRunCmd` now
  surfaces the server's error body so the 400 modality message reaches the
  status bar.
- Verified against a tmux `harnessd` on the real catalog: image-capable
  `gpt-4.1` POST → 202; text-only `claude-sonnet-4-6` → 400 naming model +
  provider; malformed base64 → 400 naming the attachment index.

## 2026-07-21 (`/undo` TUI Command — Epic #805, Slice 3)

- Added `/undo [n]` to the TUI (`cmd/harnesscli/tui`): registry entry next to `/clear` (`cmd_parser.go`), executor `executeUndoCommand` (`model.go`), API call `undoConversationCmd` (`api.go`), and result type `UndoResultMsg` (`messages.go`). Help dialog and slash completion pick the command up automatically via `buildHelpDialog` over the registry — no static lists.
- Behavior contract: bare `/undo` removes the last prompt, `/undo 3` the last three. Malformed counts (`abc`, `0`, negatives) and extra args are command errors — a usage status line, zero HTTP. `/undo` refuses with no conversation and while a run is active (an in-flight run's terminal persistence would rewrite the store and silently clobber the undo).
- Viewport refresh: on success the command POSTs `{"count": n}` to the Slice 2 route, then GETs the trimmed history in the same `tea.Cmd`; the `UndoResultMsg` case clears the view (`resetTranscriptView`, extracted from `/clear`) and re-renders (`appendConversationMessages`, extracted from `ConversationHistoryMsg`) so the removed tail bubbles disappear immediately. The `is_meta` undo-boundary marker is never rendered (only `user`/non-empty `assistant` roles render, matching resume).
- On 409 the compaction-boundary explanation renders inline in the viewport (`✗ Undo refused: …` plus a one-line hint) and the existing view is left intact; other failures land in the status bar without touching viewport or transcript.
- Bug found by the tmux smoke (issue #895): the undo truncated the store but `GET {id}/messages` kept serving the runner's stale in-memory conversation mirror, so the TUI refetch rebuilt the viewport with the removed messages. Fix: new `Runner.DropConversationCache` evicts the mirror entry (ownership records kept for cross-tenant validation; safe for active runs since run state lives in `r.runs`), called by `handleUndoConversation` after a successful `UndoPrompts`. Regression tests: `TestRunner_DropConversationCacheFallsBackToStore` (harness) and `TestUndoConversationEndpoint_RefreshesInMemoryMirror` (server, two real runs then undo then GET).
- Validation: failing-first tests in `undo_command_test.go` (13 tests: API success/conflict/error/network, executor default/numeric/parse-error/no-conversation/run-active paths, model-level viewport rebuild, conflict inline render, registry dispatch); `TestTUI364_RegistryCompleteness` allowlist and `harnessAuthCases()` auth-header table updated for the new surface. `go test ./cmd/harnesscli/tui/ -run Undo -count=1` green. tmux smoke vs `harnessd` (fake provider + conversation DB): 3 prompts, `/undo 2` → viewport shows only the first prompt and its response; `GET {id}/messages` serves `user, assistant, is_meta marker` ("removed 2 prompt(s)").

## 2026-07-21 (Issue #886 runBlockedError.Error Coverage Fix)

- Symptom: post-merge regression gate (`scripts/test-regression.sh`) failed after PR #882 landed: `coveragegate` flagged `(*runBlockedError).Error` (cmd/harnesscli/main.go) at 0.0% — `functions with zero coverage detected`.
- Cause: PR #882 added `runBlockedError` as a sentinel detected via `errors.As` in `run()`/`runContinue()`; its blocked-path tests assert exit code and stderr content but never invoke `Error()`, and the PR's validation ran package tests, not the full gate. Same failure shape as #875.
- Fix (test-only): added `TestRunBlockedErrorMessage` in `cmd/harnesscli/exitcodes_test.go` — pins both sentinel contracts across all three blocked signals: `Error()` names the blocked event type, and `errors.As` detection survives wrapping. Function now reports 100%; no production code changed.

## 2026-07-20 (OS-Service Install for harnessd — Epic #807)

- Shipped `harnesscli service install|uninstall|start|stop|status` for end users: user-level launchd agent on macOS (`~/Library/LaunchAgents/com.gocode.harnessd.plist`, `RunAtLoad`+`KeepAlive`) and `systemd --user` unit on Linux (`~/.config/systemd/user/harnessd.service`, `Restart=on-failure`, `WantedBy=default.target`). Slice 1 (PR #826): install/uninstall + pure unit generators with `--binary`/`--addr`/`--log-dir`/`--dry-run`; addr resolution reuses the daemon's own stack (`HARNESS_ADDR` env or `~/.harness/config.toml`, default `:8080`) exported into the unit environment. Slice 2 (PR #866): lifecycle commands over launchctl/systemctl behind an injectable runner; `status` distinguishes not-installed / installed-not-running / running-healthy / running-unreachable via a `GET /healthz` probe.
- Slice 3 (this change, docs-only): new "OS Service Install" section in `docs/runbooks/distribution.md` (commands, per-OS unit paths, log locations, flags, troubleshooting incl. `launchctl print gui/$(id -u)/com.gocode.harnessd` and `journalctl --user -u harnessd`, lingering note, scope guardrails); README gains an end-user pointer and the tmux guidance is explicitly re-scoped to repository dev agents.
- Validation: slices 1–2 landed under strict TDD (`go test ./cmd/harnesscli/ -run Service -count=1` — 32 tests) plus real launchd end-to-end on macOS (install → start → healthy status → stop → uninstall; `plutil -lint` OK). Slice 3 verified every documented flag/path/command against `cmd/harnesscli/service.go` and live `-h` output; no code changed.

## 2026-07-19 (Plugin Zip + GitHub Archive Install Sources — Epic #821 Slice 3)

- `internal/plugins.NormalizeSource` now detects zip sources: `.zip` suffix on remote URLs and local files, and GitHub `/archive/` URLs even without the suffix (`Source.Zip`). Local zip files are non-remote (trusted by default); zip URLs are remote (untrusted, install-time confirmation from slice 2 applies).
- `Installer.Stage` fetches zips with `net/http` (remote) or from disk (local) and extracts with stdlib `archive/zip` into the existing staging dir; `rejectSymlinks`, `LoadBundle` validation, and atomic promote are unchanged. Every entry name is validated before any write (no absolute paths, no `..` elements, no backslashes), symlink entries are rejected, and a single shared top-level directory (the GitHub archive convention) is stripped so the bundle root lands at the staging dir. Fetch, corrupt-zip, and bad-entry errors name the source.
- TDD: failing-first tests cover the detection matrix (zip URL / GitHub archive ± suffix / git URL / shorthand / local zip / local dir / local non-zip file), local and GitHub-style single-root installs, `..`/absolute/backslash/symlink rejection with no residue, and corrupt/404 sources naming the source; CLI regression proves `plugin install --yes <httptest zip URL>` end-to-end.

## 2026-07-20 (Shell Mode Slice 3 — Epic #811)

- After a foreground shell-mode command exits, the next agent prompt now
  carries a `<shell-command command="..." exit-code="...">` block with
  CDATA-wrapped output (same wrapping pattern as @-mention expansion:
  `xmlAttrEscape` + `cdataSafe`, so command text cannot break the block).
- The block is single-use (cleared on injection), prepended to `expandedValue`
  before `startRunCmd`; the display bubble and transcript keep the user's
  original text. Output gets a second, prompt-side head+tail truncation at
  10KB (rune-aligned) on top of the executor's 30KB capture cap.
- Only commands that exited on their own are captured (success or non-zero
  exit); interrupted/timed-out commands are excluded — the user killed them
  deliberately, so partial output is not context-worthy.
- Validation: `go test ./cmd/harnesscli/... -count=1` green (27 packages);
  gofmt/vet clean on touched files.

## 2026-07-20 (Headless Exit Codes — Epic #823, Slice 3)

- A headless run that blocks on input it will never receive no longer streams forever: `processSSEBlock` (`cmd/harnesscli/main.go`) now classifies `run.waiting_for_user`, `tool.approval_required`, and `plan.approval_required` via `isBlockedEvent` (`cmd/harnesscli/exitcodes.go`), and when stdin is non-interactive the stream loop returns a `runBlockedError`. `run()` and streaming `runContinue()` map it to exit 3 (`exitBlocked`) with a stderr message naming the run ID, the reason, and the `harnesscli continue <run-id>` resume command.
- The server-side run is never auto-cancelled on the blocked path (resumable by design); the blocked event line is still printed to stdout before exit, preserving the every-event-is-printed contract.
- Terminal detection reuses the package's existing injectable `stdinIsTerminal` double (`cmd/harnesscli/plugins.go:107`) — no new TTY dependency, tests stub it. Interactive stdin behavior is unchanged: blocked signals do not abort the stream (interactive answer wiring remains the ask-user epic's scope, per the epic's cross-epic constraint).
- TDD: failing-first tests cover all three blocked signals × (non-interactive → exit 3 + stderr content + no cancel POST; simulated TTY → stream continues to the terminal event's code), plus `runContinue()` blocked → 3 and unit tables for `isBlockedEvent`/`blockedEventReason`.
- Validation: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1` green; gofmt/vet clean.

## 2026-07-20 (Mid-Turn Steering — Epic #820, Slice 3)

- `ctrl+g` now steers the active run with the input-box content: new `Steer` binding in `cmd/harnesscli/tui/keys.go` (ctrl+s stays copy; ctrl+r stays reserved for future history search — re-grepped unbound), included in `ShortHelp`/`FullHelp` and the `buildHelpDialog` key list.
- `cmd/harnesscli/tui/model.go` KeyMsg handler: gated on `runActive && RunID != "" && TrimSpace(input) != ""` → clears the input (`input.Clear()`, cursor/history-pos safe), sets "Steering sent", fires slice 1's `steerRunCmd`; ungated presses are status hints only ("No active run to steer" / "Type a message to steer into the run"), never errors, never HTTP. New `SteerErrorMsg` case maps kinds via `steerErrorStatusText` (409 → "run already finished", 429 → "steering buffer full — try again shortly", 404 → "run not found"); `SteerAcceptedMsg` is consumed as a documented no-op for slice 4 to hook.
- `website/docs/cli/tui.md`: `Ctrl+G` keybinding row + a "Mid-turn steering" section (step-boundary injection, buffer-of-10 limit, `steered ⟂` marker, `harnesscli steer` one-shot).
- Strict TDD: `steer_key_test.go` drives `tea.KeyCtrlG` through the model against an httptest daemon (POST path/body observed, input cleared, `RunActive()` true, `cancelRun` not called), plus idle/empty-input no-HTTP hints and the error-kind mapping table. The 3s per-test cost is the existing `statusTickCmd(3s)` driven synchronously by the shared `runCmd` helper — same trade the ctrl+c/cancel tests already make.
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok; regression guards (`keys_test.go`, `escape_test.go`, `cancel_test.go`, `ctrlc_server_cancel_test.go`, `clipboard_test.go`, `sse_events_test.go`) green; `go test ./internal/server/ ./internal/harness/ -count=1` ok; gofmt/vet clean.

## 2026-07-20 (Issue #875 Shell-Mode Test-Seam Coverage Fix)

- Symptom: post-merge regression gate (`scripts/test-regression.sh`) failed after PR #870 landed: `coveragegate` flagged `(*Model).ShellCommandRunning` and `(*Model).WithShellExecTimeout` (cmd/harnesscli/tui/model.go) at 0.0%.
- Cause: PR #870 added both exported test seams but its `shellmode_exec_test.go` detects the running state via `ActiveToolCallStatus()` and never overrides the 120s default timeout, so the seams were dead code; the PR's validation ran only `go test ./cmd/harnesscli/...`, not the full gate.
- Fix (test-only): added `TestShellMode_CommandTimesOut` driving a real timeout kill through the executor — `WithShellExecTimeout(100ms)` + `sleep 999` — asserting the running flag transitions (`ShellCommandRunning` false→true→false), the timed-out error card, and prompt kill. Both functions now report 100%; the timeout finalization path (`timed out after …` card) is behaviorally pinned for the first time.

## 2026-07-20 (ACP Server Mode — Epic #806, Slice 2)

- `internal/acp` sessions over the runs API: `session/new` (unique `sess_<hex>` ids, cwd/mcpServers accepted but not acted on), `session/prompt` (content-block text extraction — text blocks joined, `resource_link` contributes its URI, empty extraction -> `-32602`), `session/cancel` notification -> `POST /v1/runs/{id}/cancel`. One ACP session maps to one run; a second prompt on a used session errors `-32603` (multi-turn is a later epic).
- New stdlib `RunsClient` (`client.go`): bounded client for `POST /v1/runs` and cancel, no-timeout client for the SSE subscription; `WaitTerminal` scans `GET /v1/runs/{id}/events` to a terminal event, tracking `run.cost_limit_reached` as a flag (it is non-terminal — the run then completes).
- Stop reasons: `run.completed` -> `end_turn`, cost-limit + completed -> `max_turn_requests`, `run.failed` -> `refusal`, `run.cancelled` -> `cancelled`.
- Dispatch is now concurrent: `session/prompt` holds its response open until the run terminates, so handlers run in goroutines or a mid-turn `session/cancel` could never be read. Writes stay mutex-serialized; `Serve` drains in-flight handlers at EOF. The slice-1 ordering test was updated to correlate pipelined responses by id (JSON-RPC clients never relied on order).
- `harness acp -server` flag; resolution flag > `loadConfig().Server` > `http://localhost:8080`; Bearer key from `loadConfig()`.
- Validation: strict TDD (failing tests first: `undefined: NewRunsClient`). `go test ./internal/acp/ -count=1` and `-race` green, incl. scripted-pipe flows — cancel mid-run issues the cancel POST and the prompt answers `cancelled`; concurrent sessions stay isolated; blocked-handler concurrency proof.

## 2026-07-20 (Agent Swarm — Epic #808, Slice 2)

- Extended `internal/subagents/swarm.go` with `resume_agent_ids`: entry `i`
  pairs with `items[i]`, and the item's expanded prompt is delivered to the
  existing subagent through the same messaging path `message_subagent` uses
  (`Manager.Get` to resolve the run ID, then `RunSteerer.SteerRun`).
- Validation rejects unknown IDs, duplicate IDs, more resume IDs than items,
  and active-incompatible statuses (only `running`/`waiting_for_user` accept
  steered messages, matching `SteerRun`'s contract). All checks run before
  any member launches, so a bad request starts nothing.
- Resumed members are scheduled first in the ramp, count against the same
  concurrency allowance, and are cancelled through the manager on parent
  cancellation like any started member.
- Report order stays deterministic: non-resumed item members in item order,
  then resumed members in resume order; resumed entries carry `Resumed: true`
  and their subagent ID from the start. Steer failures land in the report
  per member and never abort the cohort.
- The swarm takes the steerer via a new `WithSwarmSteerer` option; slice 3
  will wire it to the runner-backed steerer alongside the `agent_swarm` tool.
- TDD: resume behavior tests landed first (failed on undefined
  `WithSwarmSteerer`), covering the happy path, status compatibility table,
  unknown-ID/duplicate/overflow rejection, scheduling order with cap 1,
  report marking, steer-failure capture, and cancellation of resumed members.
- Validation: `go test ./internal/subagents/... -count=1` and `-race` green;
  new tests repeated 5x without flakes; full regression suite (see PR body).

## 2026-07-20 (`/undo` HTTP Route — Epic #805, Slice 2)

- Added `POST /v1/conversations/{id}/undo` (`internal/server/http_conversations.go`), routed next to `compact` with the same guards: POST-only (405 otherwise), `runs:write` scope (403), and `blockConversationCrossTenant` (cross-tenant 404, verified non-mutating).
- Handler `handleUndoConversation` delegates to Slice 1's `ConversationStore.UndoPrompts`. Body accepts `{"count": N}` (absent → default 1) or `{"to_step": S}`; empty body is treated as all-defaults. `to_step` is resolved to a count by `undoCountForStep`, which rejects steps that are negative, beyond history, or pointing at a non-prompt (non-`user` or `is_meta`) message with 400 before the store is called.
- Error mapping: `ErrUndoCrossesCompaction` → 409 `undo_crosses_compaction`; `ErrUndoCountOutOfRange` → 400; unknown conversation → 404; missing store → 501. Success returns `{"undone": true, "removed_from_step": S, "remaining_messages": M}` where M counts the persisted messages including the `is_meta` undo-boundary marker.
- Boundary semantics are store-enforced (Slice 1): the target prompt must sit strictly above the most recent `is_compact_summary` message; anything at or below the boundary is refused and the conversation is left untouched. This holds for both `count` and `to_step` forms.
- In-memory caveat, same as the existing compact route: the mutation is store-only, so `GET {id}/messages` on a conversation still resident in the runner's memory shows the pre-undo snapshot until the run ends or the daemon restarts (the store fallback then serves the truncated history). The TUI slice refetches after undo.
- Validation: failing-first tests in `internal/server/http_undo_test.go` (10 endpoint behavior tests) and `internal/server/http_undo_tenant_test.go` (cross-tenant 404 + no-mutation, `runs:read`-only 403); `go test ./internal/server/ -run 'Undo|TenantIsolation' -count=1` green. tmux smoke against `harnessd` (fake provider, `HARNESS_CONVERSATION_DB` set): `POST .../undo {"count":1}` → 200 `{"undone":true,"removed_from_step":2,"remaining_messages":3}`; after restart, `GET .../messages` serves the truncated history with the marker.

## 2026-07-19 (Unified /tasks Panel — Epic #814, Slice 2)

- Background bash jobs are now enumerable and killable daemon-wide. `JobManager.List` (`internal/harness/tools/bash_manager.go` unexported `list` + exported wrapper in `job_manager_exports.go`) returns `JobInfo` snapshots (id, command, working dir, started-at, tenant, running, exit code, timed-out) with a `Status()` of `running`/`exited`/`timed_out`; `runBackground` captures the originating run's tenant from `RunMetadataFromContext`.
- New `harness.JobTracker` (`internal/harness/job_tracker.go`): per-registry job managers register via the new `DefaultRegistryOptions.JobTracker` (unregister on registry shutdown), so the main registry, per-run provisioned-workspace registries (runner.go), and subagent worktree registries are all covered. Task IDs are namespaced `jm<N>:job_<n>` because managers number jobs from `job_1` independently.
- Server: `GET /v1/tasks` unions `bash_job` entries (label = command, cancel action while running) with the same tenant filtering as callbacks; new `POST /v1/jobs/{id}/kill` (runs:write, 404 unknown/cross-tenant, 501 unconfigured) reuses `JobManager.Kill`.
- harnessd: one `JobTracker` created in `main.go`, threaded via `baseRegistryOptions` and `runtime_container`/`bootstrap_helpers` into `ServerOptions.JobTracker`.
- Validation: failing-first tests — `bash_manager_list_test.go` (7 tests incl. race-checked concurrency), `job_tracker_test.go` (6 tests incl. registry-wiring integration through the real `bash` tool and `job_output`), and 8 new `http_tasks_test.go` tests. `go test ./internal/server/ ./internal/harness/ ./internal/harness/tools/ ./cmd/harnessd/ -count=1` all pass; the pre-existing flaky `TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly` passed in this run.
## 2026-07-20 (Headless Exit Codes — Epic #823, Slice 2)

- `harnesscli -prompt ...` and streaming `harnesscli continue` no longer exit 0 for every terminal run state: the terminal event now maps through `exitCodeForTerminalEvent` (`cmd/harnesscli/exitcodes.go`) — `run.completed` → 0, `run.failed` → 2, `run.cancelled` → 6, unknown/empty → 1 (defensive non-zero default). stdout is unchanged (`run_id=` / `terminal_event=` lines preserved); only the process exit code changed.
- New `cmd/harnesscli/exitcodes.go` holds all six contract codes (`exitSuccess`/`exitClientError`/`exitRunFailed`/`exitBlocked`/`exitCancelled`/`exitInterrupted`) as the single source of truth; literal `1`/`130` returns in `run()` and `handleStreamError()` were replaced with the named constants. `exitBlocked` (3) is wired in Slice 3.
- TDD: failing-first tests in `cmd/harnesscli/exitcodes_test.go` — mapping table (all terminal events + non-terminal/unknown/empty), contract-value pins, and `httptest` SSE end-to-end assertions that `run()`/`runContinue()` return 0/2/6 for completed/failed/cancelled streams while preserving the stdout lines; `-no-stream` proven to exit 0 without opening the event stream.
- Validation: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1` green; gofmt/vet clean; no existing test relied on the old failed/cancelled-exits-0 behavior.

## 2026-07-19 (Plugin Trust CLI + Install-Time Confirmation — Epic #821 Slice 2)

- Split `internal/plugins.Installer.Install` into `Stage` (fetch, symlink reject, manifest validation into a private `.install-*` staging dir) and `StagedBundle.Promote`/`Discard`, so the CLI can review declared surfaces between validation and promotion. `Install` keeps its one-step contract as Stage+Promote.
- Added `harnesscli plugin trust <name>` / `plugin untrust <name>` over `StateStore.SetTrusted`, making trust reachable for remote bundles for the first time; `plugin list` now appends an `untrusted — commands/hooks/MCP inactive` hint to untrusted entries.
- Remote installs now print the manifest's declared executable surfaces and require confirmation before promotion: interactive y/N on a terminal, `--yes`/`-y` for scripts, refusal with a `--yes` hint otherwise (no stdin deadlock in pipelines). Declined installs leave no files and no state record.
- `plugin update` re-stages from the recorded source, and for remote bundles re-prints surfaces and re-requires confirmation only when the declared surfaces changed; unchanged remote updates skip confirmation and preserve trust (pinned by tests).
- TDD: failing-first tests cover Stage/Promote/Discard residue discipline, trust/untrust round-trip with `plugins.TrustedBundles` gating proof, declined/non-TTY/`--yes`/prompt-accept install paths, and update trust preservation on both unchanged and changed surfaces. Remote fixtures use local `file://` git remotes (no network).

## 2026-07-20 (Mid-Turn Steering — Epic #820, Slice 2)

- The TUI no longer drops `steering.received` SSE events: `cmd/harnesscli/tui/model.go`'s dispatch switch gained a `case "steering.received"` that parses the fixed `{"message": "..."}` payload (harness `drainSteering` contract) and appends a user bubble + transcript entry (role `user`) via a new `appendSteeringMarker` helper.
- Both viewport and transcript/export carry a `steered ⟂ ` marker prefix so steered input reads distinctly from a typed prompt; the helper is the rendering slice 4's local echo will reuse. Malformed/empty payloads are dropped without panic; `m.lastEventID` bookkeeping is untouched (the case sits after ID tracking in the existing switch).
- Strict TDD: `cmd/harnesscli/tui/steer_events_test.go` (package `tui_test`, `sse_events_test.go` pattern) drives scripted `SSEEventMsg`s — marker+message in viewport, exactly one role-`user` transcript entry, distinction from a typed prompt, five malformed-payload shapes (no panic, no marker, transcript unchanged, run stays active).
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok (incl. `sse_events_test.go`, `escape_test.go`, `cancel_test.go`, `ctrlc_server_cancel_test.go`, `clipboard_test.go`, `keys_test.go` guards); `go test ./internal/server/ ./internal/harness/ -count=1` ok; gofmt/vet clean.

## 2026-07-20 (Shell Mode Slice 2 — Epic #811)

- Shell-mode submit now executes the command locally in the TUI process
  (`sh -c`, 120s default timeout) and streams combined stdout/stderr into a
  tool-style `shell` card, replacing the slice-1 stub. The executor
  (`cmd/harnesscli/tui/shellexec.go`) is fully async: a pump goroutine feeds
  output/done messages on a buffered channel that the model polls with a
  tea.Cmd, so `Update()` never blocks.
- Output is bounded twice: live deltas stop after 30KB, and the final done
  message carries a 30KB head+tail buffer (same strategy as bash plugins), so
  flood commands like `yes` stay memory-safe.
- Esc and Ctrl-C kill the whole process group (`Setpgid` + group SIGKILL +
  `WaitDelay`, mirroring `internal/harness/tools/exec_group_unix.go` #786);
  the done message then finalizes the card as interrupted. Cards reuse the
  existing `handleToolStart`/`handleToolChunk`/`handleToolResult`/
  `handleToolError` pipeline; `extractToolCommand` now covers `shell`.
- Known limitation: the tooluse `ErrorView` renders only `ErrorText`, so
  failed commands report `exit status N` plus the bounded output as reflowed
  error text (same behavior as agent bash errors today).
- Validation: `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean
  on touched files.

## 2026-07-19 (Ctrl-V Image Paste + Chips — Epic #818 Slice 2)

- Wired the slice-1 clipboard reader into the TUI: `ctrl+v` (new
  `KeyMap.PasteImage`, also listed in the help dialog) runs a modality
  pre-flight, then reads the clipboard image asynchronously; success appends
  an `[image #N]` chip row above the input prompt, failure maps the typed
  errors (`ErrClipboardNoImage`/`ErrClipboardHeadless`/`ErrClipboardUnsupported`)
  to inline status messages.
- `inputarea` owns chip state (`Attachment{Path, MediaType}`): Backspace on an
  empty buffer removes the latest chip and deletes its temp directory
  (`removeAttachmentFiles` seam); chips survive text submit (pending until
  slice 3 sends them).
- Modality pre-flight: `GET /v1/models` now returns the catalog `modalities`
  (additive, both registry and catalog branches); the TUI keeps the fetched
  list (`m.serverModels`) and rejects the paste before any subprocess when
  the effective model is known text-only. Unknown modalities (offline, older
  server, OpenRouter-sourced list) allow the paste — slice 3's server gate
  enforces at send time.
- Bug found and fixed during implementation (regression test added):
  `WindowSizeMsg` re-creates the input component, which dropped pending
  chips; attachments are now carried across the re-create via
  `inputarea.Model.WithAttachments` (`TestPasteImageChipsSurviveWindowResize`).
- Verified on macOS against the real clipboard (image set then restored):
  paste → `[image #1]` + temp dir; Backspace → chip gone + temp dir deleted;
  re-paste → `[image #1]`; text prompt submits with the chip pending.

## 2026-07-20 (Issue #854 TUI Subscription Credential Import)

- Replaced the stale `/keys` startup hint based on nonexistent `KIMI_SUBSCRIPTION_AUTH` with synchronous, local-only reads of both harness-owned Codex and Kimi credential stores. The TUI stores only a non-secret availability marker.
- Added bodyless `POST /v1/providers/{codex-subscription,kimi-subscription}/import-subscription`. It reuses the existing vendor-file import functions and the exact daemon-bootstrap token-source construction, then replaces the live registry source so `GET /v1/providers` becomes configured without restarting `harnessd`.
- Added `/keys` `i` import action for subscription rows only. Successful imports refetch the provider catalog; errors show the server's actionable remediation rather than an HTTP/stack trace. API-key rows ignore `i`.
- Regression coverage uses temporary HOME vendor fixtures only. It proves Codex and Kimi import-to-live-registry transitions, absent-login guidance, route scoping, bodyless TUI requests, and provider-list refresh behavior. No token values are logged or included in the endpoint contract.

## 2026-07-19 (Coverage-Gate Fix — `internal/acp` Zero-Coverage Functions)

- Post-merge regression gate (`scripts/test-regression.sh`) failed after epic #806 slice 1 landed: `coveragegate` flagged `(*Conn).drainLine` (conn.go) and `(*rpcError).Error` (jsonrpc.go) at 0.0%.
- Cause: the existing oversized-line tests only exercised the path where the over-cap fragment already contains the newline (no drain needed); the drain path (fragment ends mid-line, `ErrBufferFull`) and the `error`-interface method were never called.
- Fix (test-only): added a `ReadLine` subtest that shrinks `maxMessageSize` below the bufio buffer size so the crossing fragment lacks the terminator (covers `drainLine` and stream realignment), plus a direct `rpcError.Error` test. Both functions now report 100%.

## 2026-07-19 (Plugin Home Decision + Manifest v1 Contract — Epic #821 Slice 1)

- Extended `docs/design/installable-plugin-bundles.md` into the stable v1 authoring contract: full `plugin.json` field reference with validation rules, install layout (`<name>/<version>` under `$HARNESS_GLOBAL_DIR/plugins`, default `~/.go-harness/plugins`), and the enabled-vs-trusted gating table grounded in the current loader wiring.
- Decided the single plugin home: `~/.go-harness/plugins` is the bundle home; `~/.config/harnesscli/plugins/*.json` is legacy-but-supported with a documented migration path.
- Added a TUI startup warning (`legacyPluginsDirWarning` in `cmd/harnesscli/tui/plugin_loader.go`, wired in `model.go`) when the legacy dir contains JSON plugins, pointing at the bundle format; startup status wording changed from "had errors loading" to "plugin warning(s) at startup" since warnings now include a non-error deprecation notice.
- TDD: failing-first tests cover the warning surface (non-empty/missing/empty/JSON-free dirs) and that legacy JSON plugins still register as working slash commands while the warning surfaces.
- Validation: `go test ./cmd/harnesscli/tui/ -run 'TestLegacyPluginsDir|TestNoLegacyPluginsDir|TestLoadAndRegisterPlugins|TestWithPluginsDir' -count=1` and the full touched-package runs below are green.

## 2026-07-19 (ACP Server Mode — Epic #806, Slice 1)

- Added `internal/acp`: a stdlib-only (`encoding/json`) newline-delimited
  JSON-RPC 2.0 transport for the Agent Client Protocol — framed `Conn`
  (partial lines, multiple messages per read, 16 MiB message cap with stream
  realignment, goroutine-safe writer), envelope types, and spec error codes
  (`-32700` parse, `-32600` invalid request, `-32601` method not found,
  `-32602` invalid params).
- `initialize` handshake negotiates protocol version (agent supports v1 only,
  always replies 1 per spec) and returns agent capabilities:
  `loadSession: false`, text-only `promptCapabilities`, `agentInfo`, empty
  `authMethods`. Notifications and client→agent responses never get replies;
  diagnostics go to a separate writer so stdout stays a pure protocol channel.
- Wired `harness acp` (`runACP` in `cmd/harnesscli/acp.go`, dispatch case in
  `cmd/harnesscli/auth.go`) serving the handshake over stdin/stdout.
- Distinct from the pre-existing SDK-based `internal/harnessacp` /
  `cmd/harness-acp` adapter (epic #746): this package is the epic-#806
  stdlib-only implementation; session methods land in slices 2–4.
- Bug found by TDD oversized-line test: `ReadLine` drained one line too many
  when an over-cap message's newline arrived in the same read fragment;
  fixed by only draining when the terminator is still unconsumed. Covered by
  `TestConnReadLine/oversized_line...` and
  `TestServerOversizedMessageRejectedStreamStaysAligned`.
- Validation: `go test ./internal/acp/... -count=1` (also `-race`) and
  `go test ./cmd/harnesscli/... -count=1` green; acceptance
  `printf '...initialize...' | harness acp` prints a single JSON-RPC result
  with capabilities and exits 0.
## 2026-07-19 (Unified /tasks Panel — Epic #814, Slice 1)

- Added `GET /v1/tasks` (`internal/server/http_tasks.go`): a read-scoped union endpoint returning subagents, cron jobs, and pending delayed callbacks as one `Task` DTO (`id`, `type`, `status`, `label`, `started_at`, `age_seconds`, `actions`). Unconfigured sources are skipped, so an empty daemon returns `{"tasks": []}`; a failing source fails the request rather than silently dropping entries.
- Added `CallbackManager.ListAll` (`internal/harness/tools/delayed_callback.go`) for cross-conversation enumeration of pending callbacks; fired/canceled callbacks stay excluded, matching `List` semantics.
- Tenant scoping reuses the existing per-source helpers verbatim (`filterSubagentsByTenant`, `filterCronJobsByTenant`) and mirrors the cron exact-match shape for callbacks; auth matches `/v1/subagents` and `/v1/cron/jobs` (`runs:read`).
- Wired the daemon's `*tools.CallbackManager` into `server.ServerOptions.CallbackLister` through `cmd/harnessd` (`main.go` → `runtime_container.go` → `bootstrap_helpers.go`).
- Validation: failing-first tests in `internal/server/http_tasks_test.go` (7 handler tests) and `TestCallbackManagerListAll`; `go test ./internal/server/ ./cmd/harnessd/ -count=1` pass. `go test ./internal/harness/tools/ -count=1` has one pre-existing failure (`TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly`) that fails identically on main (a439dc9f) and is unrelated to this slice.

## 2026-07-19 (Agent Swarm — Epic #808, Slice 1)

- Added `internal/subagents/swarm.go`: a `Swarm` orchestrator that fans one
  `prompt_template` (with a required `{{item}}` placeholder) over 1–128 items
  into concurrent subagents started through the existing
  `tools.SubagentManager` (`InlineManager`).
- Validation rejects missing placeholders, empty/oversized item lists,
  duplicate expanded prompts (compared trimmed, since the manager trims), and
  `resume_agent_ids` (reserved for Slice 2).
- Concurrency ramps kimi-code style: 5 members start immediately, then +1
  in-flight allowance every 700ms, capped by `HARNESS_SWARM_MAX_CONCURRENCY`
  (read once at construction; default 128, clamped to 128).
- Caller context cancellation cancels every started member via the manager
  (members finishing Start after the sweep self-cancel, closing the race);
  unstarted members are reported cancelled. Member failures land in the
  aggregated `SwarmReport` (deterministic item order) and never abort the
  cohort.
- TDD: behavior tests landed first (initially failing on undefined symbols),
  covering validation, ramp timing via an injected ticker, env cap,
  cancellation propagation, per-member failure capture, and an acceptance
  test through a real `Manager` + `InlineManager`.
- Coverage-gate lesson: the zero-coverage-function gate caught an unused
  speculative option (`WithSwarmRamp`) after the first full regression run.
  The epic fixes the ramp at 5/+1-per-700ms with only the env cap as a knob,
  so the option was removed (dead code) rather than padded with a test for
  behavior nothing calls. Keep slice API surface to what the epic specifies.
- Validation: `go test ./internal/subagents/... -count=1` and `-race` green;
  new tests repeated 5x without flakes.
- Note: epic-level docs (`agent_swarm` tool description, swarm design doc)
  land with their owning slices (3+); no pre-existing subagent design doc
  exists to update in this slice.

## 2026-07-19 (Mid-Turn Steering — Epic #820, Slice 1)

- Added the client plumbing for the existing `POST /v1/runs/{id}/steer` route, strict TDD:
  - `cmd/harnesscli/tui/api.go`: `steerRunCmd(baseURL, runID, prompt, apiKey)` mirroring `cancelRunCmd`; routes through `newHarnessRequest` so harnessd auth is preserved (pinned by the `api_auth_test.go` audit table + static routing regression).
  - `cmd/harnesscli/tui/messages.go`: `SteerAcceptedMsg` (202) and `SteerErrorMsg` with stable `Kind` strings (`not_found`, `run_not_active`, `steering_buffer_full`, `invalid_prompt`, `http`, `transport`) for slice 3's status-bar mapping.
  - `cmd/harnesscli/runctl.go` + `auth.go` dispatch: `harnesscli steer <run-id> <prompt>` one-shot subcommand mirroring `runCancel` (`-base-url` only — the epic's `-api-key` parenthetical does not match `runCancel`, which has no such flag; noted in the PR).
  - Empty/whitespace prompts are rejected client-side in both paths before any HTTP request (the server would 400).
- Live-daemon smoke: `harnessd` with `HARNESS_PROVIDER=fake` + a scripted bash turn held active via `HARNESS_TOOL_APPROVAL_MODE=all`; `harnesscli steer <id> "focus on X"` printed `Run <id> steering accepted` (exit 0); unknown run → `not found` (exit 1); finished run → `not active` (exit 1).
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok; `go test ./internal/server/ ./internal/harness/ -count=1` ok; `go vet ./cmd/harnesscli/...` clean; gofmt clean on touched files (repo-wide gofmt drift on unrelated files pre-exists on main).

## 2026-07-19 (Clipboard Image Reader — Epic #818 Slice 1)

- Added `ReadImageFromClipboard` in `cmd/harnesscli/tui/clipboard_image.go`:
  reads a PNG off the system clipboard into an `os.MkdirTemp` file and returns
  `ClipboardImage{Path, MediaType}` with typed sentinel errors
  (`ErrClipboardHeadless`, `ErrClipboardUnsupported`, `ErrClipboardNoImage`).
- Platform matrix: macOS via `osascript` (pbpaste cannot read image flavors —
  its `-Prefer` accepts only txt/rtf/ps — so the `PNGf` class is read as a
  `«data PNGf<hex>»` record and hex-decoded in-process); Linux via `wl-paste`
  or `xclip`; anything else returns `ErrClipboardUnsupported`. Headless mode
  (`IsHeadless()`) short-circuits before any subprocess.
- Strict TDD: 13 failing-first tests cover the happy paths (exact PNG bytes in
  the temp file), no-image/tool-missing/malformed-payload errors, and the
  no-subprocess headless guarantee, using package-level exec seams
  (`clipboardImageGOOS`/`clipboardImageLookPath`/`clipboardImageOutput`).
- Verified on macOS against the real clipboard (image set then restored):
  reader produced a valid PNG temp file via the unfaked code path.

## 2026-07-19 (Shell Mode Slice 1 — Epic #811)

- Added shell-mode input state to the `harnesscli` TUI: `!` on an empty input
  (typed or pasted) enters shell mode; the input area renders a `!` prompt
  marker inside a violet rounded border; Backspace/Esc on an empty shell-mode
  input exits; submit is a stub status message (execution lands in slice 2).
- Root `Model` owns the `shellMode` flag and re-applies it to the re-created
  input component on every `WindowSizeMsg`; the inputarea component owns only
  rendering state (`SetShellMode`), keeping mode transitions in one place.
- Esc with a non-empty shell input clears the text but stays in shell mode —
  exit happens only on an already-empty input, matching kimi-code.
- Validation: `go test ./cmd/harnesscli/tui/ -count=1` and
  `go test ./cmd/harnesscli/tui/components/inputarea/... -count=1` pass.

## 2026-07-19 (ACP Server Mode — Epic #746)

- Added `cmd/harness-acp` and `internal/harnessacp`, using pinned
  `github.com/coder/acp-go-sdk v0.13.5` (compatible with Go 1.25) for stdio
  JSON-RPC lifecycle handling.
- The adapter keeps harnessd as the only execution path: ACP sessions map to
  stable conversations; prompt/cancel/approve/deny use existing run routes;
  the shared `HarnessClient` now exposes parsed SSE streaming for adapters.
- ACP updates project assistant message/thought deltas, tool lifecycle events,
  approval requests, and todo plan updates. The key-free fake HTTP/SSE ACP
  prompt-turn test covers request-to-terminal update flow.
- Validation before PR: targeted ACP and harness MCP package tests, then the
  repository formatting, vet, and regression gates.

## 2026-07-19 (Enforced Plan Mode — Epic #740)

- Added per-run plan state, central policy-wrapper gating, broker-backed plan-exit approval, SQLite plan persistence, CLI/TUI request plumbing, and a scrollable TUI approval preview. Mutations with absent or non-matching paths fail closed while planning.

## 2026-07-19 (Session Rewind — Epic #739)

- Added SQLite pre-image points, non-fatal runner capture, hash-checked restore/truncation, HTTP list/restore routes, and explicit TUI confirmation. Oversized files are skipped rather than stored.

## 2026-07-19 (Reliability Epic #644 Reconciliation)

- Reconciled the 2026-06-24 15-slice long-session reliability plan against the supplied `origin/main` baseline. The code and deterministic regressions for T03–T15, plus the original T01/T02 behavior, were already present on the baseline (principally from prior harness/TUI integration work), so they were not duplicated or falsely represented as new failing-first commits.
- Closed the two remaining plan-level correctness gaps with failing-first regressions:
  - T01: completed run states are retained until their terminal event has actually persisted, preventing a transient store failure from silently dropping the only in-memory terminal history.
  - T02: every event-store append now receives a five-second bounded context, preventing a wedged store from occupying a run goroutine indefinitely while preserving the existing lock-free terminal fanout path.
- Validation: focused T01/T02 tests passed under `-race`; full `go test ./... -race`, `go vet ./...`, and `./scripts/test-regression.sh` passed (`coveragegate: PASS`, 84.4% total, zero zero-coverage functions).

## 2026-07-19 (Multi-run TUI Dashboard — Epic #738)

- Implemented the six dashboard slices (#742, #745, #749, #753, #757, #762) as TUI-only changes: authenticated `/v1/runs` polling, grouped overlay navigation, `/dashboard`/`Ctrl+D`, one cancellable peek SSE bridge, selected-run steer/cancel, and isolated new-run dispatch.
- Added focused failing-first dashboard tests for list loading, grouped navigation, command/key opening, peek close lifecycle, control routing, and dispatch. No server route or dependency changes.
- Validation: `go test ./cmd/harnesscli/...` passes. Repository-wide formatting gate still reports pre-existing drift and a syntax-invalid training exercise; see final verification status.

## 2026-06-28 (Config-Driven Lifecycle Hooks — Epic #737)

- Implemented epic #737 and all six child issues (#741, #744, #750, #755, #759, #763) in worktree branch `codex/config-hooks-epic-737`, one commit per slice, strict TDD throughout.
- New package `internal/hooks`:
  - Hook-file schema + loader with strict JSON decoding (unknown fields rejected), structured per-file skip records, deterministic ordering, and user/project source classification.
  - Command + HTTP adapters implementing the four existing `internal/harness` hook interfaces unchanged; JSON wire types defined once in `wire.go` and shared by both adapters (pinned by golden tests).
  - Content-hash trust store (`~/.harness/hooks-trust.json`) gating project-level hook files; user-global files trusted implicitly; atomic temp+rename writes; corrupt/missing store fails closed (empty).
  - `Build` (def → adapter routed by event) and `Summary` (startup-computed listing, non-nil empty slices so JSON marshals `[]`).
- `internal/config`: `[hooks]` TOML section (`enabled`, `dirs`) following the existing rawLayer pointer-merge pattern.
- `cmd/harnessd`: `registerConfigDrivenHooks` appends adapters to existing `RunnerConfig` hook slices after compiled-in plugins; structured startup logs per loaded/skipped hook; summary flows through `runtime_container` → `buildServerOptions` → `ServerOptions.HooksSummary`.
- `internal/server`: `GET /v1/hooks` serves the startup summary (read scope); never re-derives per request.
- `cmd/harnesscli`: `hooks trust|revoke|list` maintenance subcommand; TUI `/hooks` command rendering the server listing (loaded table + skipped section + empty state) through the existing registry/API-client/viewport paths.
- `internal/harness/runner.go` (additive only): `duration_ms` on `tool_hook.completed`/`tool_hook.failed` events, matching the message-hook observability contract. No interface-signature changes anywhere.
- Bugs found during implementation (each got a permanent regression guard):
  - **Parallel test file collision**: table-driven command-adapter subtests shared one `hook.sh` path in one temp dir, so parallel subtests overwrote each other's scripts and every case saw the same script. Symptom: incoherent failures (deny results on allow cases). Cause: shared mutable file across `t.Parallel()` subtests. Fix: per-subtest `t.TempDir()` — the fixed table structure is the regression guard.
  - **httptest timeout test hung 30s**: the server handler blocked on `r.Context().Done()` but the Go server only cancels the request context on client disconnect after the handler has consumed the request body. Symptom: package suite took 30s. Cause: handler never read the body, so disconnect went undetected and the `time.After(30s)` backstop fired. Fix: consume the body first, then block on `r.Context().Done()`; suite back to ~1s.
  - **Timeout-kill test flake under race/parallel load**: the orphan assertion checked `kill(pid, 0)` once immediately after the kill; the background grandchild is reparented to init and reaped asynchronously, so a single instantaneous check raced reaping. Fix: poll for process death with a 5s deadline — assertion strength unchanged (processes must die), timing tolerance added.
  - **Same test, second flake mode (found by the fast PR gate)**: with a 1s hook timeout, full-suite CPU contention could fire the timeout before the just-exec'd script wrote its pid files — the orphan assertion then failed on missing files. Root cause: the test's pid discovery assumed script startup < hook timeout. Deterministic redesign: the hook runs in a goroutine, the test waits for pid files to appear (4s budget) before the 5s hook timeout fires, then asserts the timeout error and polls for process death; under pathological startup latency it degrades (with a `t.Logf`) to the timeout-error assertion only. Verified with `go test -race -count=3 ./internal/hooks/` under concurrent CPU load.
  - **Linux ETXTBSY (found by PR CI, invisible on macOS)**: `TestCommandHook_PostToolUse/empty_stdout_is_no_modification` failed in CI with `fork/exec .../post-empty.sh: text file busy` — the known overlayfs/Linux pattern of exec'ing a file written milliseconds earlier. Fix: all script-exec test sites (unit, integration, server e2e) run scripts through `/bin/sh <script>` — reading a just-written file never hits ETXTBSY. Adapter behavior unchanged (production hooks are exec'd directly; the window only exists for just-written files). PR CI then passed on both jobs.
- Observability: adapters log structured failure fields (`hook_name`, `event`, `tool_name`/`url`, `duration_ms`, `exit_code`/`status_code`, `error`) through the runner's `harness.Logger`; every exec emits existing `tool_hook.*`/`hook.*` SSE events with hook name, decision, and `duration_ms` — recon confirmed no new SSE event types were needed for config-hook deny attribution (documented in plugins.md).
- Docs: `docs/design/plugins.md` gained the full "Config-driven hooks" chapter (schema, discovery, command + HTTP wire protocols, message events, trust model, runtime semantics, end-to-end example); CLAUDE.md gained the Lifecycle Hooks HTTP API section; `docs/ux-paths.md` slash-command table gained `/hooks`; plans/design indexes updated.
- Note: no dedicated TOML config-reference doc exists (grepped docs/ for `conclusion_watcher` — only investigations/plans matched); the `[hooks]` section is documented in plugins.md instead, per the #741 fallback instruction.
- Validation:
  - Red phase per slice: new tests failed to compile/run before implementation (undefined `Load`, `NewCommandHook`, `NewHTTPHook`, `LoadTrustStore`, `Build`, `registerConfigDrivenHooks`, `loadHooksCmd`).
  - Green phase per slice: `go test ./internal/hooks/ ./internal/config/ ./cmd/harnessd/ ./internal/server/ ./cmd/harnesscli/...` all pass.
  - `go test -race -count=5 ./internal/hooks/` passes consecutively (post flake-fix).
  - Fast PR gate `go test ./internal/... ./cmd/...`: 95 packages ok, exit 0.
  - `./scripts/test-regression.sh`: PASS, `coveragegate: PASS (total=84.4%, min=80.0%, zero-functions=0)`.
  - PR #784 CI: both `test` jobs pass on the final head (`09569df8`).
  - `gofmt -l` clean on all touched files; `go vet` clean on all touched packages. (Pre-existing repo-wide gofmt drift on untouched files verified identical on `main`.)

## 2026-06-26 (Reliability T01 Memory Retention)

- Implemented reliability plan slice T01 locally:
  - Added bounded in-memory retention for terminal run states with default cap 32.
  - Added bounded in-memory conversation mirror retention with default cap 256.
  - Terminal runs with active subscribers are kept until the subscriber cancels; subscriber cancellation re-runs pruning.
- Added failing-first coverage in `internal/harness/runner_prune_test.go` for completed-run pruning, subscriber-protected terminal runs, and conversation mirror pruning.
- Validation:
  - Red phase: `go test ./internal/harness -run 'TestRunnerPrune' -count=1` failed to build because retention config fields did not exist.
  - `go test ./internal/harness -run 'TestRunnerPrune' -count=1`
  - `go test ./internal/harness -race -run 'TestRunnerPrune|TestRecorderGoroutine_DoneClosedAfterRun' -count=1`
  - `go test ./internal/harness -race -count=1`

## 2026-06-26 (Regression Coverage Gate Cleanup)

- Fixed the current `./scripts/test-regression.sh` coveragegate blocker without weakening the gate.
- Added meaningful zero-coverage tests across:
  - `cmd/harnessd/mcp_runner_adapter.go`
  - checkpoint service/store helpers
  - Docker fallback execution
  - replay tool dispatch lookup
  - callback manager construction
  - checkpoint approval denial
  - workspace path permission detection
  - deferred goal tool actions
  - networks/workflow/workflows stores and helpers
  - SQLite working-memory deletion
- Fixed two race/baseline issues surfaced by the regression run:
  - workflow subscriber cancellation can no longer close a channel while `emit` is sending;
  - the recorder goroutine test now holds the provider until `recorderDone` is observable.
- Validation:
  - `go run ./cmd/coveragegate -coverprofile=coverage.out -min-total=80.0` passed with total 84.5% and zero zero-coverage functions.
  - `./scripts/test-regression.sh` passed end to end.

## 2026-06-26 (Reliability T03 Empty Response Exhaustion)

- Implemented reliability plan slice T03 locally:
  - Empty-response retry exhaustion now fails the run with `max_empty_responses` instead of silently completing with empty output.
  - Retryable empty responses no longer consume outer step budget, so a run with `MaxSteps=1` can recover after retryable empty responses.
- Added failing-first coverage in `internal/harness/runner_empty_response_test.go` for both exhaustion failure and retry budget preservation.
- Validation:
  - `go test ./internal/harness -run 'TestEmptyResponseRetry_MaxRetriesExhausted|TestEmptyResponseRetry_DoesNotConsumeStepBudget' -count=1`
  - `go test ./...`
  - `go test ./... -race`
- Regression gate note:
  - `./scripts/test-regression.sh` still fails in the coverage gate because pre-existing zero-coverage functions remain outside this slice, including `cmd/harnessd/mcp_runner_adapter.go` and workflow/checkpoint store functions. Total coverage is above threshold at 83.7%, and the new daily TUI handlers are covered.

## 2026-06-26 (TUI-First Daily Harness Command Slice)

- Added first-pass daily run-control commands for the personal TUI-first harness plan:
  - `harnesscli continue <run-id> <prompt>` starts a continuation and streams the new run's events.
  - `harnesscli replay <run-id-or-rollout-path>` posts to the replay endpoint and prints formatted JSON.
  - `harnesscli search <query>` filters persisted run metadata locally.
  - `harnesscli runs` and `harnesscli show` alias the existing list/status behavior.
- Updated `scripts/go-code.sh` so installed `go-code` exposes `runs`, `show`, `cancel`, `continue`, `replay`, and `search` directly.
- Registered the remaining daily TUI slash-command entry points (`/attach`, `/runs`, `/replay`, `/resume`, `/doctor`) while preserving existing `/sessions`, `/search`, and `@path` file expansion behavior.
- Added bare run-ID replay resolution: `POST /v1/runs/replay` can now resolve `run_...` to `<RolloutDir>/*/<run_id>.jsonl` when a rollout directory is configured.
- Added shared Conductor repository settings for setup/build and concurrent workspace daemon runs.
- Reconciled stale `docs/context/known-issues.md` continuation-tool-filter status.
- Validation:
  - `go test ./cmd/harnesscli ./internal/server -run 'TestRunContinue|TestRunReplay|TestRunSearch|TestDispatch_DailyAliases|TestGoCodeScriptRoutesDailyCommands|TestHandleRunReplay_SimulateResolvesBareRunID|TestTUI041_BuiltinCommandsRegistered' -count=1`
  - `go test ./cmd/harnesscli ./cmd/harnesscli/tui ./internal/server -count=1`
  - `go test ./...`
  - `go test ./... -race`

## 2026-06-26 (Adapter-First Terminal-Bench Eval Harness)

- Hardened the Terminal-Bench runner and adapter.
  - `scripts/run-terminal-bench.sh` now performs preflight checks for dataset, Python, Docker daemon, tmux, Terminal-Bench command resolution, provider/key configuration, fake-provider turns, and target arch.
  - The runner now builds linux/amd64 or linux/arm64 `harnessd` and `harnesscli` once per campaign and passes the binary directory to the adapter through `HARNESS_BENCH_BINARY_DIR`.
  - The runner now passes explicit Terminal-Bench flags for model, custom agent import path, dataset path, output path, concurrency, attempts, and global timeouts.
- Added `scripts/terminal_bench_artifacts.py`.
  - Merges Terminal-Bench oracle output with adapter-produced `benchmark_result.json`.
  - Validates merged rows against `benchmarks/comparison/result.schema.json`.
  - Writes merged `results.jsonl`, `summary.json`, `run-env.json`, and an actionable `report.md`.
  - Classifies failed tasks as `oracle_fail`, `agent_timeout`, `harness_error`, `provider_error`, `tool_contract_error`, `workspace_error`, or `infra_error`.
- Updated the Terminal-Bench adapter to write per-trial `benchmark_result.json`, `harness_telemetry.json`, and `harnessd.log`, and to support key-free fake-provider mode.
- Extended the benchmark result schema with external Terminal-Bench `parser_results` and derived failure classification fields.
- Added `scripts/test_terminal_bench_artifacts.py` and wired it into the fast GitHub workflow.
- Stabilized `TestWorkerPool_RunQueuedEventEmitted` for race-mode regression runs by using the same longer wait as the adjacent queued-transition test and releasing held provider channels through cleanup-safe helpers.
- Validation:
  - `python3 scripts/test_terminal_bench_artifacts.py`
  - `python3 -m py_compile scripts/terminal_bench_artifacts.py scripts/test_terminal_bench_artifacts.py benchmarks/terminal_bench/agent.py`
  - `bash -n scripts/run-terminal-bench.sh scripts/build-bench-images.sh`
  - `git diff --check`
  - `go test ./internal/... ./cmd/...`
  - `go test ./internal/harness -race -run TestWorkerPool_RunQueuedEventEmitted -count=1`
  - `go test ./internal/harness -race -count=1`
- Full regression:
  - `scripts/test-regression.sh` was run in tmux.
  - First run failed in `go test ./... -race` on `internal/harness TestWorkerPool_RunQueuedEventEmitted`; the test was fixed and the package now passes under race.
  - Second run passed `go test ./...` and `go test ./... -race`, then failed at `coveragegate` despite 83.9% total statement coverage because existing zero-covered functions remain across packages such as `cmd/harnessd`, `internal/checkpoints`, `internal/workflows`, and `internal/workingmemory`.
- 2026-06-27 follow-up:
  - Added focused coverage tests for the remaining zero-covered functions across `cmd/harnessd`, checkpoints, cloud scheduler, replay, harness brokers/tools, networks, workflows, and working memory.
  - Stabilized `internal/harness TestWorktreePartialProvisionFailure_NoOrphan` under race mode by replacing the racy chmod watcher setup with a deterministic committed-directory blocker and bounded git setup.
  - `scripts/test-regression.sh` now passes in tmux with `coveragegate: PASS (total=84.6%, min=80.0%, zero-functions=0)`.
  - Refreshed Terminal-Bench CLI behavior from Context7, changed runner liveness from unsupported `--version` to `--help`, recorded the package version through Python metadata, and fixed empty extra-arg handling under `set -u`.
  - Fixed real-smoke adapter blockers discovered during live runs.
    - `cmd/harnesscli` now ignores SSE comment/heartbeat blocks such as `: ping` instead of failing with `invalid sse block`.
    - `benchmarks/terminal_bench/agent.py` now copies provider credentials through a private container env file instead of embedding them in Terminal-Bench `commands.txt`.
    - The adapter fetches run records, summaries, and harness logs through raw Docker `exec_run` output instead of parsing tmux-wrapped pane text.
    - The adapter sets `HARNESS_PRICING_CATALOG_PATH` to the copied repo catalog path for models that have catalog pricing.
  - Ran the accepted real-provider smoke campaign at `.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42`.
    - Provenance recorded: git SHA `89b5064fba6b17423029db4a41ac02fb8857d350`, provider `openai`, model `gpt-5-mini`, Terminal-Bench `0.2.18`, dataset hash `31b29122bfa16205e6a66967fc444f5d46924a8ed9f39167cb27fc1e676d5457`, concurrency `1`, attempts `1`, timeouts `1800/300`.
    - Result: 7/7 tasks passed with per-task `benchmark_result.json`, `harness_telemetry.json`, `harnessd.log`, command logs, pane logs, raw `results.json`, merged `results.jsonl`, `summary.json`, `run-env.json`, and `report.md`.
    - Secret check: the accepted artifact directory has zero files matching raw OpenAI key patterns.
  - Promoted `benchmarks/terminal_bench/baseline.json` from the accepted real-provider campaign. Cost is explicit but unpriced: `total_cost_usd=0.0`, `cost_status=unpriced_model`, because `catalog/pricing.json` does not yet include `gpt-5-mini`.

## 2026-06-26 (Issue #649 Completed Run Retention)

- Implemented reliability slice T01 from `docs/plans/2026-06-24-harness-reliability-plan.md` for issue `#649`.
- Added bounded in-memory retention for terminal run states:
  - `RunnerConfig.MaxCompletedRetention` defaults to 32.
  - completed, failed, and cancelled runs are eligible for pruning only when a durable run `Store` is configured, after terminal handling, and when no subscribers remain attached.
  - subscriber cancellation re-runs pruning so terminal runs held for streaming clients can be released after the stream detaches.
- Added bounded in-memory conversation mirror retention:
  - `RunnerConfig.MaxConversationRetention` defaults to 256.
  - `r.conversations`, `r.conversationOwners`, and conversation recency metadata evict together.
  - persistent `ConversationStore` history remains the fallback for pruned conversation mirrors.
- Added regressions in `internal/harness/runner_prune_test.go` covering completed-run pruning, active-subscriber retention, and persistent-store fallback for evicted conversation mirrors.
- Red phase:
  - `go test ./internal/harness -run TestRunner_Prune -count=1` failed to build because the retention config fields did not exist.
- Verification:
  - `go test ./internal/harness -run TestRunner_Prune -count=1`
  - `go test ./internal/harness -count=1`
  - `go test ./internal/server -run TestWorkerPoolLoad -count=1`
  - `go test ./internal/harness/... -race -count=1`
- Regression status:
  - `./scripts/test-regression.sh` passed the `go test ./...` and `go test ./... -race` phases.
  - `./scripts/test-regression.sh` failed at the coverage-gate phase because existing functions outside this slice still report `0.0%` coverage; total statement coverage was `83.9%`.

## 2026-05-05 (GitHub Pages User Repositioning)

- Recentered the go-code GitHub Pages copy around the developer visitor.
- Shifted the page from runtime/API-first positioning to the user problem: getting visible, steerable coding help inside a real repository.
- Added concrete use cases for failing tests, codebase orientation, and careful refactors.
- Reframed trust language around local-first work, visible tools, bounded runs, and recoverable context.
- Validation:
  - `python3` HTML parser sanity check for `docs/site/index.html`
  - Local browser preview at `http://127.0.0.1:4188/` with desktop and mobile viewport screenshots

## 2026-05-03 (Repository Rename and Public README Cleanup)

- Renamed the GitHub repository and public project surface from `go-agent-harness` to `go-code`.
- Reworked the top-level README for first-time browsers with a watercolor hero, quick start, install modes, repository map, HTTP surface summary, testing commands, and documentation links.
- Updated the GitHub Pages landing page and distribution runbook to use the new repository URL and Pages URL.
- Added `docs/assets/` for public documentation media and removed tracked root-level scratch files that made the repository look less presentable.
- Validation:
  - `file docs/assets/go-code-watercolor-hero.png`
  - `git diff --check`
  - `bash -n scripts/install.sh scripts/go-code.sh`
  - `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/pages.yml")'`

## 2026-05-03 (Repository Hygiene Cleanup)

- Removed tracked local/generated state that should not be part of the repository:
  - `.coord/`
  - `.codex-worktrees/`
  - benchmark `jobs/`
  - Python `__pycache__/` bytecode
  - root `code-reviews/` output
  - scratch `sol/`
- Moved root-level training exercise folders into `playground/training/` so the top-level tree reads like a product repository.
- Isolated incomplete `playground/examples/` and `playground/exercises/` behind their own module boundaries so `cd playground && go test ./...` covers the stable playground baseline without treating every scratch exercise as product code.
- Stabilized the moved persistent-trie training test by replacing random word generation with deterministic unique words, avoiding false "future word" failures from duplicate random samples.
- Tightened `.gitignore` to keep coordination state, generated job output, Python bytecode, and scratch training outputs from being recommitted.
- Validation:
  - `git diff --check`
  - `go test ./cmd/harnesscli/... -count=1`
  - `go test ./internal/quality/repostructure -count=1`
  - `go test ./... -count=1`
  - `cd playground && go test ./... -count=1`

## 2026-05-01 (User-Local Installer and Workspace-Aware TUI)

- Added a sudo-free local installer for distribution testing.
  - Added `scripts/install.sh`, defaulting to `~/.local/bin` via `~/.local`, with `--prefix`, `--bin-dir`, `--data-dir`, `--system`, `--add-to-path`, `--no-build`, `--uninstall`, and `--dry-run`.
  - Installer now copies runtime `prompts/` and `catalog/` assets into a sibling `share/go-code` directory so installed commands do not depend on the repo as the current working directory.
  - Updated `Makefile` so `make install` delegates to the user-local installer instead of copying directly into `/usr/local/bin`.
  - Updated `scripts/go-code.sh` to discover installed runtime assets and point missing-command hints at the installer.
- Made installed TUI launches preserve the caller's project workspace.
  - `harnesscli --tui -workspace <path>` now carries the workspace into `tui.TUIConfig`.
  - TUI run creation now includes `workspace_path`, matching single-shot prompt mode.
  - Added regressions for CLI workspace request payloads, TUI config workspace propagation, and TUI start-run workspace payloads.
- Validation:
  - `bash -n scripts/install.sh scripts/go-code.sh`
  - `scripts/install.sh --dry-run --no-build --prefix "$PWD/.tmp/install-dry-run"`
  - `GOCACHE=/tmp/go-build go test ./cmd/harnesscli ./cmd/harnesscli/tui -run 'TestRunCreatesAndStreamsToCompletion|TestNewTUIConfigIncludesWorkspace|TestRunTUIRequiresTerminal|TestStartRunCmdIncludesWorkspacePath' -count=1`
  - `GOCACHE=/tmp/go-build scripts/install.sh --prefix "$PWD/.tmp/install-verify"`
  - `.tmp/install-verify/bin/go-code --help`
  - `HOME=$(mktemp -d) GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... -count=1`

## 2026-05-01 (Distribution Docs and GitHub Pages)

- Added public distribution documentation for Go Agent Harness.
  - `docs/runbooks/distribution.md` now documents the current source installer, installed command contract, GitHub Pages setup, release archive layout, future installer download mode, Homebrew tap direction, single-binary simplification path, and release checklist.
  - `README.md` now points daily users at `./scripts/install.sh --add-to-path`, `go-code`, and the distribution docs.
- Added a GitHub Pages-ready static site.
  - `docs/site/index.html` and `docs/site/styles.css` provide a single-page install and product overview for Go Agent Harness.
  - `docs/site/INDEX.md` indexes the site source folder.
  - `.github/workflows/pages.yml` publishes `docs/site` through GitHub Actions on pushes to `main` that touch the site or workflow.
- Updated documentation indexes:
  - `docs/INDEX.md`
  - `docs/runbooks/INDEX.md`
- Validation:
  - `curl -I http://127.0.0.1:4180/` against a temporary tmux-served `docs/site` static server
  - `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/pages.yml"); puts "yaml ok"'`
  - `git diff --check -- README.md docs/INDEX.md docs/runbooks/INDEX.md docs/runbooks/distribution.md docs/logs/engineering-log.md docs/logs/long-term-thinking-log.md .github/workflows/pages.yml`
  - `perl -ne 'print "$ARGV:$.: trailing whitespace\n" if /[ \t]$/; close ARGV if eof' docs/site/INDEX.md docs/site/index.html docs/site/styles.css`

- 2026-04-29: Fixed issue `#557` by making the container workspace provision success test use a unique, readable workspace ID per invocation instead of reusing `test-provision`.
  - Added `containerWorkspaceTestID(...)` in `internal/workspace/container_test.go`, combining a readable sanitized prefix with nanoseconds and an atomic sequence.
  - Updated `TestContainerWorkspace_Provision_Success` to register `t.Cleanup` with `Destroy(...)` after provisioning attempts so normal failures clean up the test container.
  - Added regressions proving:
    - generated test IDs are unique per call and keep the `test-provision-` prefix
    - Docker container name conflicts are not treated as skippable environment failures
  - Verification:
    - red phase: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run TestContainerWorkspace_Provision_TestIDUniquePerCall -count=1` failed to build because `containerWorkspaceTestID` did not exist
    - green phase: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run 'TestContainerWorkspace_Provision_(TestIDUniquePerCall|ConflictIsNotSkipped)' -count=1`
    - acceptance rerun: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run TestContainerWorkspace_Provision_Success -count=2 -v` passed with both runs skipped because this sandbox cannot bind `:0`.
    - follow-up hardening: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness/tools/core -count=1` passed after making `TestGitDiffTool_MaxBytes` create its own dirty Git fixture instead of depending on this checkout having a diff.
  - Local environment blockers:
    - `go test ./internal/workspace -count=1` is blocked by sandbox network restrictions: `TestGetFreePort` cannot bind `:0`, and unrelated Hetzner `httptest` tests cannot listen on `[::1]:0`.
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh` is blocked by the same sandbox restriction across unrelated packages that use `httptest.NewServer`, `127.0.0.1:0`, or `[::1]:0`.
    - tmux session creation is blocked in this sandbox: `error connecting to /private/tmp/tmux-501/default (Operation not permitted)`.
    - GitHub CLI issue/PR access is blocked by `error connecting to api.github.com`.

- 2026-04-13: Added an autoresearch-style testing loop with a dedicated prompt-profile and target-driven run scripts.
  - Added `prompts/models/autoresearch.md` and wired it into `prompts/catalog.yaml` so the harness has a reusable testing-oriented prompt profile.
  - Added `scripts/autoresearch-run.sh` for one-shot autoresearch runs and `scripts/autoresearch-loop.sh` for cycling through coverage-gap-driven targets with per-run markdown reports under `.tmp/autoresearch/`.
  - Adjusted both runners to send `max_steps=50` by default and exposed `--max-steps` / `HARNESS_AUTORESEARCH_MAX_STEPS` overrides for future tuning.
  - Documented the workflow in `docs/runbooks/testing.md`, added the plan at `docs/plans/2026-04-13-autoresearch-testing-plan.md`, and updated the plans index and active-plan tracker.
  - Added prompt-profile resolution coverage in `internal/systemprompt/catalog_test.go` and refreshed the fixture catalog in `internal/systemprompt/testhelpers_test.go`.
  - Verification:
    - `bash -n scripts/autoresearch-run.sh scripts/autoresearch-loop.sh`
    - `go test ./internal/systemprompt`
    - `go test ./internal/systemprompt ./cmd/harnesscli`

- 2026-04-05: Added documentation-first orchestration guardrails and landed the stage-1 `harnessd` runtime-container extraction.
  - Added the umbrella orchestration program plan plus five stage specs under `docs/plans/`, with explicit feature statuses so planned checkpoints/workflows/memory/networks stay out of public docs until implemented.
  - Tightened `docs/runbooks/testing.md`, `docs/runbooks/documentation-maintenance.md`, and `docs/plans/PLAN_TEMPLATE.md` so large architecture work now requires characterization before refactors, failing tests before new behavior, permanent regression tests for discovered bugs, and status-aligned docs.
  - Extracted `cmd/harnessd/runtime_container.go` so:
    - `runMCPStdio(...)` delegates stdio assembly to `buildMCPStdioRuntime(...)`
    - `runWithSignals(...)` delegates runner/subagent/server assembly to `buildHTTPRuntime(...)`
  - Added direct tests in `cmd/harnessd/runtime_container_test.go` for the new MCP and HTTP runtime assembly helpers, including callback-runner and lazy-summarizer binding.
  - Verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run 'TestBuild(MCPStdioRuntimeCreatesCatalogAndServer|HTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer)' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server ./internal/subagents ./cmd/harnessd -count=1`

- 2026-04-01: Moved sandbox enforcement to the live tool execution boundary so per-run and continuation permissions now control bash/job behavior.
  - Added tool-context sandbox propagation in the runner step engine instead of relying on the registry startup sandbox.
  - Updated `JobManager` foreground/background execution to prefer the per-call sandbox from `context.Context`, while preserving the manager-level sandbox as a fallback default for non-run callers.
  - Added regressions proving:
    - start-run sandbox overrides can loosen a stricter registry default
    - continuation sandbox overrides can change behavior mid-conversation
    - direct `JobManager` calls respect context sandbox overrides for both foreground and background commands
  - Corrected the `SandboxScopeLocal` comment in `internal/harness/types.go` so the documented trust boundary matches the current enforcement model.
  - Verification:
    - `go test ./internal/harness/tools`
    - `go test ./internal/harness/tools/core`
    - `go test ./internal/harness`
    - `go test ./internal/server`

- 2026-03-29: Restored a green repo-wide test baseline after the structure cleanup.
  - Fixed `tmp/training-pubsub/broker.go` so active subscribers get retry-based delivery before a publish is counted as dropped, while lag accounting still works for genuinely full subscribers.
  - Simplified `tmp/training-skiplist/skiplist.go` to use a single RW lock for correctness under concurrent insert/search/delete paths.
  - Reworked `tmp/training-regex/regex.go` and `training-regex/regex.go` so `Regexp.Match(...)` uses AST-based full-string matching semantics that satisfy the current training tests.
  - Fixed `training-trie/trie.go` so `Delete(...)` returns whether a word was actually deleted instead of whether the root should be pruned.
  - Fixed `training-trie/trie_test.go` to remove a deadlocking parent/subtest `t.Parallel()` pattern from the stress test.
  - Verification:
    - `go test ./tmp/training-pubsub ./tmp/training-skiplist`
    - `go test ./tmp/training-regex ./training-regex`
    - `go test ./training-trie`
    - `go test ./...`

- 2026-03-28: Cleaned up the repository boundary between product code and experimental code.
  - Moved the ad hoc root-level Go snippets into `playground/examples/` and `playground/exercises/`.
  - Added `playground/go.mod` so example-code failures no longer break product-module verification.
  - Added `internal/quality/repostructure/root_layout_test.go` to prevent Go source from drifting back into the repo root and to enforce the separate-module boundary for `playground/`.
  - Removed the tracked root-level `trainerd` binary and ignored it going forward.
  - Updated the top-level `README.md` and added `playground/README.md` so the new structure is explicit to contributors.

- 2026-03-25: Split GitHub test gating so pull requests run a fast `go test ./internal/... ./cmd/...` workflow while the full `./scripts/test-regression.sh` suite runs on `main`, nightly schedule, and manual dispatch.
  - Updated `.github/workflows/test-regression.yml` to remove the PR trigger and add nightly/manual entrypoints.
  - Added `.github/workflows/test-fast.yml` as the lightweight PR gate.
  - Updated `docs/runbooks/testing.md` to document the new CI split and when the full regression suite still applies.

## 2026-03-25 (Issue #425 Step Engine Extraction)

- Added a dedicated internal step-engine abstraction in `internal/harness/runner_step_engine.go` and reduced `Runner.runStepEngine(...)` in `internal/harness/runner.go` to a thin delegator.
- Preserved the existing step-loop behavior by moving the full provider/hook/tool/accounting/compaction/steering path intact instead of redesigning the contract.
- Added focused characterization coverage in `internal/harness/runner_step_engine_test.go` for the step-boundary steering contract on the second step.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunnerStepLoop_SteeringDrainBeforeTurnRequest|TestSteerRun_BasicInjection|TestSteerRun_MultipleMessages|TestStepStartedEventHasTimestamp' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`

## 2026-03-25 (Issue #426 Bootstrap Wiring)

- Extracted focused `harnessd` bootstrap helpers in `cmd/harnessd/bootstrap_helpers.go` for:
  - catalog/pricing/provider-registry bootstrap
  - cron bootstrap
  - persistence + conversation-cleaner bootstrap
  - trigger/webhook adapter bootstrap
  - HTTP server option assembly
- Slimmed `cmd/harnessd/main.go` so `runWithSignals(...)` delegates those wiring concerns instead of inlining each subsystem's setup.
- Added direct failing-first coverage in `cmd/harnessd/bootstrap_helpers_test.go` for:
  - workspace catalog fallback and model API lookup behavior
  - secret-driven trigger validator/adapter registration
  - server option forwarding of the extracted runtime dependencies
  - persistence bootstrap setup and failure cleanup behavior
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run 'TestBuild(CatalogBootstrapFallsBackToWorkspaceCatalog|TriggerRuntimeHonorsSecrets|ServerOptionsForwardsBootstrapRuntime|PersistenceBootstrapInitializesStoresAndCleaner|PersistenceBootstrapClosesRunStoreWhenConversationSetupFails)' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -race -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build COVERPROFILE_PATH=$PWD/.tmp/issue-426-coverage.out ./scripts/test-regression.sh`
- Regression status:
  - `cmd/harnessd` package tests and race tests passed after the extraction.
  - The repo-wide regression script is blocked locally by unrelated existing transcript-export tests that attempt to write under `~/Library/Caches`, which this sandbox forbids:
    - `cmd/harnesscli/tui: TestExportCommandWritesOutsideWorkingDirectory`
    - `cmd/harnesscli/tui/components/transcriptexport: TestTUI059_ExportDefaultOutputDirCreatesFileOutsideWorkingDirectory`
  - The issue-`#426` change itself did not introduce a package-level failure outside that pre-existing sandbox-specific blocker.

## 2026-03-25 (Issue #422 Run Persistence Ownership)

- Added focused HTTP persistence-ownership regressions in `internal/server/http_persistence_ownership_test.go` to prove that:
  - `POST /v1/runs` persists exactly once when a shared store is configured
  - external-trigger `start` persists exactly once
  - external-trigger `continue` persists exactly once for the new run record
- Confirmed the red state first:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'Test(PostRunPersistsExactlyOnce|ExternalTriggerStartPersistsExactlyOnce|ExternalTriggerContinuePersistsExactlyOnce)' -count=1`
  - failed because each new run hit `CreateRun` twice
- Consolidated ownership by removing duplicate transport-layer `CreateRun` calls from:
  - `internal/server/http.go`
  - `internal/server/http_external_trigger.go`
- Updated `internal/server/http_test.go` so the existing store-backed run test uses a shared runner/server store and reflects runner-owned persistence explicitly.
- Baseline observation before changes:
  - `go test ./...` still fails in `go-agent-harness/training-regex` (`TestQuest`, `TestAlt`, `TestGroup`, `TestAnchors`, `TestEmptyString`, `TestEdgeCases`), which is unrelated pre-existing test debt outside this issue’s scope.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'Test(PostRunPersistsExactlyOnce|ExternalTriggerStartPersistsExactlyOnce|ExternalTriggerContinuePersistsExactlyOnce|HarnessRunToStore)' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness`

## 2026-03-25 (Issue #430 Allowed-Tools Fallback Integrity)

- Preserved `allowed_tools` restrictions on prompt-based fallback execution paths by adding an optional constrained runner entrypoint and using it in:
  - `internal/server/http_agents.go` for `/v1/agents` prompt execution and skill-lister fallback execution
  - `internal/harness/tools/skill.go` for flat-catalog fork fallback execution
  - `internal/harness/tools/core/skill.go` for core skill fork fallback execution
- Implemented `Runner.RunPromptWithAllowedTools(...)` in `internal/harness/runner.go` so fallback execution can start a plain sub-run while still forwarding `RunRequest.AllowedTools`.
- Added regression coverage for:
  - `/v1/agents` prompt path preserving `allowed_tools`
  - `/v1/agents` skill fallback preserving `allowed_tools`
  - flat skill fallback preserving `allowed_tools`
  - core skill fallback preserving `allowed_tools`
  - runner-level forwarding of `RunPromptWithAllowedTools(...)`
- Verification:
  - baseline before edits: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core`
  - failing-first regressions:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillFallbackPreservesAllowedTools|TestFlatSkillForkBasicRunPromptPreservesAllowedTools|TestSkillTool_Handler_ForkWithBasicRunnerPreservesAllowedTools' -count=1`
  - focused green verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_(PromptPreservesAllowedTools|SkillFallbackPreservesAllowedTools)|TestFlatSkillForkBasicRunPromptPreservesAllowedTools|TestSkillTool_Handler_ForkWithBasicRunnerPreservesAllowedTools|TestRunPrompt(ReturnsOutput|WithAllowedTools_ForwardsAllowedTools|_RespectsContextCancellation)' -count=1`
  - relevant package verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core`
  - repo regression gate:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
    - local package-test phase passed cleanly
    - local race phase produced repeated macOS linker warnings (`malformed LC_DYSYMTAB`) and did not yield a clean final exit inside the tmux wrapper before handoff, so final mergeability will be confirmed from PR CI

## 2026-03-25 (Issue #427 HTTP Feature Decomposition)

- Extracted the run transport slice from [`internal/server/http.go`](/Users/dennisonbertram/.codex/worktrees/ade2/go-agent-harness/.codex-worktrees/issue-427-http-feature-decomposition/go-agent-harness/internal/server/http.go) into [`internal/server/http_runs.go`](/Users/dennisonbertram/.codex/worktrees/ade2/go-agent-harness/.codex-worktrees/issue-427-http-feature-decomposition/go-agent-harness/internal/server/http_runs.go):
  - route registration helper for `/v1/runs`
  - run collection dispatch and run-by-id dispatch
  - run creation/listing, run SSE/events, approval, input, continuation, context, compaction, and cancellation transport handlers
- Extracted the conversation transport slice from [`internal/server/http.go`](/Users/dennisonbertram/.codex/worktrees/ade2/go-agent-harness/.codex-worktrees/issue-427-http-feature-decomposition/go-agent-harness/internal/server/http.go) into [`internal/server/http_conversations.go`](/Users/dennisonbertram/.codex/worktrees/ade2/go-agent-harness/.codex-worktrees/issue-427-http-feature-decomposition/go-agent-harness/internal/server/http_conversations.go):
  - route registration helper for `/v1/conversations/`
  - conversation dispatch, search/export/compact/cleanup handlers
  - list/delete conversation handlers
- Kept `buildMux()` behavior-identical while replacing the inline route wiring for runs/conversations with small registration helpers so `http.go` reads more like server assembly than server implementation.
- Added a focused `internal/profiles/profile_test.go` regression for `ListProfileSummaries(...)` so the branch still satisfies the repo zero-coverage gate after the extraction.
- Verification:
  - baseline before extraction:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -count=1`
  - post-extraction:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -count=1`
  - persistence-regression guard after rebasing onto `main`:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'TestPostRunPersistsExactlyOnce|TestExternalTriggerStartPersistsExactlyOnce|TestExternalTriggerContinuePersistsExactlyOnce' -count=1`
  - profile coverage regression:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/profiles -run TestListProfileSummariesDeduplicatesByTierPriority -count=1`
  - repo regression rerun:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
    - blocked by the pre-existing `internal/harness` race test `TestRecorderGoroutine_RaceWithConcurrentEmit`, which reproduces in isolation without any `internal/harness` changes in this PR

## 2026-03-25 (Issue #429 Forked Child-Run Failure Propagation)

- Reproduced the bug with new failing regressions on all three affected caller surfaces:
  - `internal/server/http_agents_test.go`
  - `internal/harness/tools/skill_test.go`
  - `internal/harness/tools/core/skill_test.go`
- Added `internal/harness/tools/fork_result.go` with a small shared helper so tool-layer callers can treat `ForkResult.Error` as terminal child-run failure information.
- Updated:
  - `internal/server/http_agents.go` so `/v1/agents` returns `execution_error` instead of HTTP 200 when a forked skill completes with `result.Error`.
  - `internal/harness/tools/skill.go` so flat-catalog forked skills do not emit `status: completed` for failed child runs.
  - `internal/harness/tools/core/skill.go` so core skill-tool fork execution follows the same failure contract.
- Verification:
  - baseline before changes:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core`
  - red phase:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillForkResultErrorReturns500|TestFlatSkillForkForkedAgentRunnerResultError|TestSkillTool_Handler_ForkResultError'`
  - green phase:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillForkResultErrorReturns500|TestFlatSkillForkForkedAgentRunnerResultError|TestSkillTool_Handler_ForkResultError'`
## 2026-03-25 (Issue #431 Startup Cleaner Cancellation)

- Reproduced the `go vet` startup-leak warning in `cmd/harnessd/main.go` where `convCleanerCancel` was only reached from the normal shutdown path after the conversation cleaner had already been started.
- Added a deterministic regression seam in `cmd/harnessd/main.go` so tests can supply a fake conversation cleaner without mutating package globals across parallel test runs.
- Added `TestStartupFailureCancelsConversationCleaner` in `cmd/harnessd/main_test.go`:
  - starts the cleaner
  - forces a startup failure with a bound port
  - asserts the cleaner context is cancelled before `runWithSignals(...)` returns
- Tightened `runWithSignals(...)` cleanup so the cleaner cancel function is always deferred once startup begins, which preserves the existing clean-shutdown path while also covering early startup exits.
- Followed up on the PR CI failure in `internal/training`:
  - the temporary Git repositories created in tests were still using Git's default branch name, while the regression helper and tests expect `main`
  - updated `initGitRepo(...)` to rename the freshly created branch to `main` after the initial commit so the regression suite behaves the same in CI, worktrees, and local runs
- Followed up on the repo-wide coverage gate exposed by CI:
  - added direct coverage for `newEmptyCommandRegistry()` in `cmd/harnesscli/tui`
  - added direct coverage for `tooluse.New(...)`
  - added direct coverage for `ListProfileSummaries()` tier precedence via explicit project/user dirs plus built-in fallback
- Verification:
  - `go test ./cmd/harnessd -run TestStartupFailureCancelsConversationCleaner -count=1`
  - `go test ./cmd/harnessd -count=1`
  - `go vet ./internal/... ./cmd/...`
  - `go test ./internal/training -count=1`
  - `go test ./cmd/harnesscli/tui -run TestNewEmptyCommandRegistryStartsEmpty -count=1`
  - `go test ./cmd/harnesscli/tui/components/tooluse -run TestNewInitializesIdentityFields -count=1`
- `go test ./internal/profiles -run TestListProfileSummariesPrefersHigherPriorityDirs -count=1`

## 2026-03-25 (Issue #421 Config Runtime Contract)

- Centralized `cmd/harnessd` runner wiring behind `buildRunnerConfig(...)` so merged `config.Config` is the authoritative source for projected runner behavior instead of scattered field assignment in `runWithSignals(...)`.
- Projected the full currently-supported `auto_compact` and `forensics` surfaces into `harness.RunnerConfig`, including:
  - `enabled`, `mode`, `threshold`, `keep_last`, `model_context_window`
  - `trace_tool_decisions`, `detect_anti_patterns`, `trace_hook_mutations`
  - `capture_request_envelope`, `snapshot_memory_snippet`
  - `error_chain_enabled`, `error_context_depth`, `capture_reasoning`
  - `cost_anomaly_detection_enabled`, `cost_anomaly_step_multiplier`
  - `audit_trail_enabled`, `context_window_snapshot_enabled`, `context_window_warning_threshold`, `causal_graph_enabled`, `rollout_dir`
- Preserved the existing runtime-only dependencies and behavior around prompt engine, ask-user broker, role models, MCP registry wiring, and the legacy rollout-dir env override by folding that override back into the resolved config before building `RunnerConfig`.
- Added failing-first regression coverage in `cmd/harnessd/main_test.go` for:
  - projection of all supported `auto_compact` and `forensics` fields
  - preservation of existing runtime dependencies when using the new builder seam
- Verification:
  - Baseline before edits: `go test ./cmd/harnessd ./internal/config`
  - Red first: `go test ./cmd/harnessd -run 'TestBuildRunnerConfig(Project|Preserves)' -count=1`
  - Green after fix:
    - `go test ./cmd/harnessd -run 'TestBuildRunnerConfig(Project|Preserves)' -count=1`
    - `go test ./cmd/harnessd -count=1`
    - `go test ./internal/config -count=1`
  - Repo regression gate: `./scripts/test-regression.sh` launched in `tmux` (`issue421-regression`); final status recorded after completion.
  - `go test ./internal/profiles -run TestListProfileSummariesPrefersHigherPriorityDirs -count=1`

## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Reproduced the subrun cancellation leak in `internal/harness/runner.go`: `waitForTerminalResult(...)` returned on parent `ctx.Done()` without cancelling the spawned child run, leaving it in `running` status.
- Added regression coverage in:
  - `internal/harness/runner_orchestration_test.go`
    - `TestRunPrompt_CancelsChildRunOnContextCancellation`
    - `TestRunForkedSkill_CancelsChildRunOnContextCancellation`
  - `internal/server/http_agents_test.go`
    - `TestAgentsEndpoint_TimeoutCancelsSpawnedRun`
- Implemented a minimal runner fix:
  - `waitForTerminalResult(...)` now checks whether the child run already reached a terminal state before treating parent cancellation as authoritative.
  - if the child run is still active when the parent context ends, the runner now calls `CancelRun(runID)` before returning the parent cancellation error.
- Verification:
  - baseline before changes: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server`
  - red step: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunPrompt_CancelsChildRunOnContextCancellation|TestRunForkedSkill_CancelsChildRunOnContextCancellation'`
  - green focused step: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server -run 'TestRunPrompt_CancelsChildRunOnContextCancellation|TestRunForkedSkill_CancelsChildRunOnContextCancellation|TestAgentsEndpoint_Timeout(Exceeded_Returns408|CancelsSpawnedRun)'`
  - package verification: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server`
## 2026-03-25 (Issue #423 Runner Preflight Extraction)

- Extracted the `Runner.execute()` setup path into a focused `runPreflight(...)` helper in [`internal/harness/runner.go`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/internal/harness/runner.go):
  - profile-driven workspace isolation fallback
  - workspace provisioning and cleanup registration
  - workspace-path system-prompt re-resolution
  - provider/model setup and prompt events
  - conversation preloading and per-run MCP registry setup
- Added direct seam-level regression coverage in [`internal/harness/runner_preflight_test.go`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/internal/harness/runner_preflight_test.go) for:
  - profile isolation fallback when `workspace_type` is unset
  - `workspace.provision_failed` emission on provisioning errors
  - prompt re-resolution against the provisioned workspace path
  - per-run scoped MCP registry creation
- Updated the plan/intent trail for the issue:
  - [`docs/plans/2026-03-25-issue-423-runner-preflight-plan.md`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/docs/plans/2026-03-25-issue-423-runner-preflight-plan.md)
  - [`docs/plans/active-plan.md`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/docs/plans/active-plan.md)
  - [`docs/plans/INDEX.md`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/docs/plans/INDEX.md)
  - [`docs/logs/long-term-thinking-log.md`](/Users/dennisonbertram/.codex/worktrees/a321/go-agent-harness/docs/logs/long-term-thinking-log.md)
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunPreflight_' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -race -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -race -run TestWorkerPool_QueuedTransitionsToRunning -count=5`
- Regression status:
  - the repo-wide `./scripts/test-regression.sh` run reached the multi-package race phase cleanly but hit a timeout in `TestWorkerPool_QueuedTransitionsToRunning` during the full-package `go test ./internal/... ./cmd/... -race` invocation.
  - that worker-pool race timeout did not reproduce in isolated reruns, so it currently looks like an unrelated pre-existing/full-suite flake rather than a deterministic issue-`#423` regression.

## 2026-03-25 (Issue #424 Event Journal Extraction)

- Extracted the runner event append/store/recorder path into a focused internal helper in `internal/harness/runner_event_journal.go`.
- Kept `Runner.emit()` as the orchestration wrapper while moving payload enrichment, terminal sealing, redaction handling, recorder capture, and event construction behind the new helper boundary.
- Added direct regression coverage in `internal/harness/runner_event_journal_test.go` for the terminal-ordering contract:
  - terminal events must be appended to the store before subscribers observe them as durable.
- Preserved the existing send-under-lock behavior for non-terminal subscriber fanout so the extraction stays race-clean with concurrent `Subscribe(...)/cancel()` behavior.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test -race ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build COVERPROFILE_PATH=$PWD/.tmp/issue-424-coverage.out ./scripts/test-regression.sh` launched in `tmux` as `issue-424-regression`; the package-test phase passed and the repo-wide race phase advanced deep into `internal/...` and `cmd/...`, but the local sandbox run stopped making visible progress after repeated macOS linker warnings (`malformed LC_DYSYMTAB`). Final mergeability should be confirmed from PR CI.

## 2026-03-25 (Harness Review Bug Tickets)

- Reviewed the harness runtime and transport paths with focus on cancellation propagation, forked-run failure reporting, tool-allowlist integrity, and bootstrap cleanup.
- Created four bug issues with implementation-ready agent prompts, explicit TDD requirements, and regression-test expectations:
  - `#428` Cancel timed-out subruns instead of leaving them running
  - `#429` Propagate forked child-run failures instead of reporting success
  - `#430` Preserve `allowed_tools` restrictions on agent and skill fallback paths
  - `#431` Close the conversation cleaner on `harnessd` startup failures
- Verification:
  - `gh issue create` created issues `#428` through `#431`
  - no runtime code changed in this pass

## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Claimed GitHub issue `#428` in a dedicated worktree branch: `codex/issue-428-subrun-cancel`.
- Confirmed the current wait path in `internal/harness/runner.go` returns the parent context error from `waitForTerminalResult(...)` without calling `CancelRun(runID)`, which matches the reported leak risk.
- Baseline verification before changes:
  - `GOCACHE=$PWD/.tmp/go-build TMPDIR=$PWD/.tmp/tmp go test ./internal/harness -run 'TestRunPrompt_RespectsContextCancellation|TestRunForkedSkill_ReturnsFailedForkResult|TestWaitForTerminalResult_(UsesTerminalHistory|ReturnsOnStreamClose)' -count=1`
  - `GOCACHE=$PWD/.tmp/go-build TMPDIR=$PWD/.tmp/tmp go test ./internal/server -run 'TestAgentsEndpoint_TimeoutExceeded_Returns408' -count=1`
- Next step: add failing regression tests for child-run cancellation on parent timeout/cancellation before implementing the minimal runner fix.

## 2026-03-25 (Architecture Review Backlog)

- Reviewed the harness architecture with focus on config authority, persistence ownership, and monolithic orchestration boundaries.
- Converted the review into a dependency-ordered GitHub issue stack with TDD-first implementation guidance and explicit regression-test expectations:
  - `#421` Make merged harness config the authoritative runtime contract
  - `#422` Consolidate run persistence ownership into the runner boundary
  - `#423` Extract runner preflight orchestration from `execute()`
  - `#424` Extract runner event journal and sink path from `runner.go`
  - `#425` Extract the core step engine from the runner monolith
  - `#426` Split `harnessd` bootstrap into modular app wiring
  - `#427` Continue decomposing `internal/server/http.go` by feature
- Execution order captured in the issue bodies:
  - Start with config contract and persistence ownership so runtime boundaries are explicit.
  - Then split the runner monolith in slices: preflight, event journal, step engine.
  - Run `harnessd` bootstrap decomposition and `internal/server` transport decomposition alongside or after the runner work as dependencies allow.
- Verification:
  - Created GitHub issues `#421` through `#427`
  - No runtime code changed in this pass

## 2026-03-25 (Backend OpenRouter Discovery)

- Added additive backend discovery support in `internal/provider/catalog/discovery.go`:
  - live OpenRouter fetch from `https://openrouter.ai/api/v1/models`
  - in-memory TTL caching
  - stale-cache fallback when a refresh fails
- Extended `internal/provider/catalog/registry.go` so runtime provider resolution and merged model listing can use cached live OpenRouter data while preserving static catalog metadata as the overlay authority.
- Updated `internal/server/http.go` so `GET /v1/models` serializes the merged registry view when a provider registry is configured.
- Wired `cmd/harnessd/main.go` to enable OpenRouter discovery automatically when the loaded model catalog includes an `openrouter` provider, without making startup perform a live fetch.
- Added focused regression coverage in:
  - `internal/provider/catalog/discovery_test.go`
  - `internal/provider/catalog/discovery_registry_test.go`
  - `internal/server/http_models_test.go`
  - updated `internal/harness/runner_test.go`
- Verification:
  - `go test ./internal/provider/catalog ./internal/server ./internal/harness ./cmd/harnessd`

## 2026-03-18 (Issue #316 Context Grid Coverage)

- Added direct package coverage for `cmd/harnesscli/tui/components/contextgrid` in `cmd/harnesscli/tui/components/contextgrid/model_test.go`:
  - default total-token fallback when `TotalTokens <= 0`
  - used-token clamping for negative and over-limit values
  - width fallback / max-width bar sizing
  - rendered header, counts, percentage text, and bar glyph assertions
- Tightened `cmd/harnesscli/tui/components/contextgrid/model.go` so the progress bar fits within the requested width after accounting for the surrounding brackets:
  - `barWidth` now uses `width - 2`
  - narrow widths clamp to at least one cell instead of forcing a 10-cell overflow
- Verification:
  - `TMPDIR=$PWD/.tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/components/contextgrid`
  - `TMPDIR=$PWD/.tmp GOCACHE=$PWD/.tmp/go-build go test -cover ./cmd/harnesscli/tui/components/contextgrid`
- Regression status:
  - package coverage for `cmd/harnesscli/tui/components/contextgrid` is now `93.1%`
  - full `./scripts/test-regression.sh` is blocked in this sandbox because many existing tests cannot bind local ports (`httptest.NewServer`, `listen tcp :0`, `127.0.0.1:0`) under the current environment; the failures are not isolated to the context-grid package

## 2026-03-18 (Issue #332 Runner Orchestration Coverage)

- Added direct orchestration regression tests in `internal/harness/runner_orchestration_test.go` for:
  - `SubmitInput` mapping broker validation failures to `ErrInvalidRunInput`
  - `SubmitInput` mapping missing pending-question submissions to `ErrNoPendingInput`
  - terminal-history and stream-closure wait semantics
  - failed `RunForkedSkill` terminal result mapping
- Refactored the shared wait logic in `internal/harness/runner.go` into `waitForTerminalResult(...)` so `RunPrompt` and `RunForkedSkill` keep the same behavior while the history/stream branches become directly testable.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestSubmitInput_MapsBrokerValidationFailure|TestSubmitInput_MapsMissingPendingQuestion|TestWaitForTerminalResult_UsesTerminalHistory|TestWaitForTerminalResult_ReturnsOnStreamClose|TestRunForkedSkill_ReturnsFailedForkResult|TestRunPrompt_ReturnsOutput|TestRunPrompt_RespectsContextCancellation'`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
- Regression status:
  - targeted harness tests and full `internal/harness` package tests passed.
  - the repo-wide regression script failed for unrelated environment/sandbox reasons: multiple packages panic or error when `httptest.NewServer`, `net.Listen`, or `listen tcp 127.0.0.1:0` attempt to bind a localhost port in this sandbox (examples include `internal/cron`, `internal/mcp`, `internal/observationalmemory`, `internal/server`, `cmd/harnesscli`, `cmd/harnesscli/tui`, `cmd/harnessd`, `cmd/cronsd`, and `internal/workspace`).
  - no issue-`#332` failure remained in the harness package after the new tests/refactor landed.

## 2026-03-18 (Ownership And Copy-Semantics Hardening)

- Added an explicit clone contract for mutable exported/state-storing harness types:
  - `internal/harness/types.go`
    - `ToolDefinition.Clone()` now deep-copies schema maps.
    - existing `Message.Clone()` remains the owner of `ToolCalls` copy semantics.
  - `internal/harness/clone.go`
    - centralized deep-copy helpers for payload maps, string slices, and message slices with preserved nil semantics.
- Hardened registry ownership boundaries in `internal/harness/registry.go`:
  - clone tool definitions on registration
  - clone definitions on `Definitions()`, `DefinitionsForRun()`, and `DeferredDefinitions()`
  - deep-copy MCP-discovered tool schemas before storing them
- Normalized remaining runner message snapshot reads onto `copyMessages(...)` in `internal/harness/runner.go` so internal readers stop using ad hoc shallow slice copies.
- Fixed nil/empty conversation semantics in `internal/harness/runner.go`:
  - persisted empty conversations are now distinguishable from missing conversations via store owner lookup
  - `copyMessages(...)` preserves non-nil empty slices instead of collapsing them to `nil`
- Added TDD coverage in `internal/harness/registry_test.go` for:
  - caller mutation after `Register(...)`
  - returned-definition mutation after `Definitions()` / `DefinitionsForRun()`
  - `ToolDefinition.Clone()` nil semantics
- Added the reusable checklist runbook and wired it into the planning flow:
  - `docs/runbooks/ownership-copy-semantics.md`
  - `docs/runbooks/INDEX.md`
  - `docs/plans/PLAN_TEMPLATE.md`
  - `docs/runbooks/worktree-flow.md`
- While running the repo regression gate, fixed two unrelated pre-existing blockers so the gate got further:
  - `cmd/harnesscli/tui/components/statspanel/model.go` plus three golden snapshots now anchor snapshot rendering to the latest fixture date instead of wall-clock time
  - `internal/subagents/manager.go` now synchronizes worktree auto-cleanup so `Get()` no longer races or reports cleanup complete before the filesystem destroy finishes
- Validation:
  - `go test ./internal/harness ./internal/subagents ./cmd/harnesscli/tui/components/statspanel`
  - `go test ./internal/subagents -run 'TestManagerCreateWorktreeSubagent(DestroyOnSuccess|Preserve)' -race`
  - `./scripts/test-regression.sh` executed via `tmux`
- Regression status:
  - repo-wide regression script still exits non-zero because the existing coverage gate reports many zero-coverage functions in unrelated packages (for example `cmd/forensics/main.go:18`, `cmd/harnesscli/main.go:347`, `cmd/harnesscli/tui/api.go:99`, `internal/config/config.go:511`, `internal/provider/openai/client.go:749`, `internal/subagents/manager.go:164`)
  - no new repo-wide behavioral test failure remained after the `statspanel` and `subagents` fixes above

## 2026-03-18 (Runner Concurrency Invariants)

- Made the runner's concurrency/lifecycle invariants explicit in `internal/harness/runner.go`:
  - `emit()` owns canonical event ordering.
  - `state.messages` is the single source of truth for run context.
  - payload ownership must stay isolated across caller/history/subscriber/recorder boundaries.
- Strengthened recorder behavior in `internal/harness/runner.go`:
  - `startRecorderGoroutine()` now buffers out-of-order arrivals and flushes JSONL in `Seq` order.
  - `recorder.drop_detected` markers now carry the dropped event's `Seq`, keeping the ledger position explicit if a drop is surfaced.
- Added invariant-focused regression coverage in `internal/harness/runner_forensics_test.go`:
  - `TestEventLedgerInvariant_JSONLMatchesInMemoryHistory`
- Reframed existing compaction tests in `internal/harness/runner_context_compact_test.go` around the `state.messages` source-of-truth contract.
- Verification:
  - `GOCACHE=/tmp/go-build-cache go test ./internal/harness -run 'TestEventLedgerInvariant_JSONLMatchesInMemoryHistory|TestCompactRunSurvivesConcurrentExecute|TestCompactRunAtStepBoundary|TestMessageExportMutationIsolation|TestAccountingStructPointerFieldIsolation'`
  - `GOCACHE=/tmp/go-build-cache go test -race ./internal/harness -run 'TestEventLedgerInvariant_JSONLMatchesInMemoryHistory|TestCompactRunSurvivesConcurrentExecute|TestCompactRunAtStepBoundary|TestMessageExportMutationIsolation|TestAccountingStructPointerFieldIsolation'`
  - Full repo regression suite not run in this pass.

## 2026-03-18 (Provider/Model Impact Map Guardrail)

- Added a new one-page planning artifact for provider/model flow work:
  - `docs/plans/IMPACT_MAP_TEMPLATE.md`
  - Requires explicit sections for config, server API, TUI state, and regression tests.
  - Makes blank headings an explicit warning; unaffected surfaces must be documented as `None` with rationale.
- Added a focused runbook:
  - `docs/runbooks/provider-model-impact-mapping.md`
  - Defines when the impact map is required and how to use it before implementation starts.
- Updated workflow entry points to surface the requirement early:
  - `AGENTS.md`
  - `docs/context/critical-context.md`
  - `docs/plans/PLAN_TEMPLATE.md`
  - `docs/runbooks/worktree-flow.md`
- Updated planning metadata:
  - `docs/plans/2026-03-18-provider-model-impact-map-guardrail-plan.md`
  - `docs/plans/active-plan.md`
  - `docs/plans/INDEX.md`
  - `docs/runbooks/INDEX.md`
- Verification:
  - Planned as doc cross-reference verification in this pass; no runtime code changed.

## 2026-03-06 (Issue #18 Head-Tail Buffer for Long Command Output)

- Added bounded head-tail output capture in `internal/harness/tools/head_tail_buffer.go`:
  - concurrency-safe writer that stores leading and trailing output bytes
  - explicit middle omission marker: `...[truncated output]...`
- Integrated bounded capture in command execution paths:
  - `internal/harness/tools/bash_manager.go` for foreground `bash` and background jobs (`job_output`)
  - `internal/harness/tools/common_exec.go` so command-backed helper tools also avoid unbounded output buffering
- TDD evidence (failing first, then green):
  - failing first: `GOCACHE=/tmp/go-build-cache go test ./internal/harness/tools -run TestJobManagerOutputHeadTailBuffer` (compile failure before implementation: missing `maxOutputBytes`)
  - passing after implementation:
    - `GOCACHE=/tmp/go-build-cache go test ./internal/harness/tools -run TestJobManagerOutputHeadTailBuffer`
    - `GOCACHE=/tmp/go-build-cache go test ./internal/harness -run TestBashToolOutputUsesHeadTailBuffer`
- Full regression gate:
  - executed via tmux: `GOCACHE=/tmp/go-build-cache ./scripts/test-regression.sh`
  - failed due unrelated pre-existing repo issues:
    - `cmd/harnesscli/main_prompt_test.go` references undefined `httpClient`
    - existing harness test failure: `TestApplyPatchToolAcceptsUnifiedPatchPayload`
- Commit/merge status:
  - blocked by required full regression gate failure (no commit/merge performed).

## 2026-03-05 (Provider Token Streaming)

- Added incremental provider-to-runner streaming contract in `internal/harness/types.go` via `CompletionRequest.Stream` and `CompletionDelta`.
- Updated runner execution to emit live SSE-visible delta events before turn completion:
  - `assistant.message.delta`
  - `tool.call.delta`
- Implemented OpenAI streaming chat completions assembly in `internal/provider/openai/client.go`:
  - sends `stream: true`
  - requests streamed usage via `stream_options.include_usage`
  - assembles assistant text and tool calls from chunked deltas
- Added TDD coverage:
  - streamed assistant/tool-call assembly in `internal/provider/openai/client_test.go`
  - runner delta event emission in `internal/harness/runner_test.go`
- Validation:
  - `go test ./internal/provider/openai` passed
  - targeted runner tests in `go test ./internal/harness -run 'TestRunner(EmitsAssistantMessageDeltaEvents|EmitsToolCallDeltaEventsBeforeExecution|ExecutesToolCallsAndPublishesEvents|FailsWhenProviderErrors|EmitsUsageDeltaAndPersistsTotals|FailedRunIncludesPartialUsageTotals)'` passed
- Note: full `go test ./internal/harness` is currently blocked by an unrelated existing failure in `TestApplyPatchToolAcceptsUnifiedPatchPayload`.

## 2026-03-05

### Architecture Decision: REST over GraphQL

**Decision**: Stick with REST for all API endpoints. Do not adopt GraphQL.

**Rationale**:
- The API is command-and-control for orchestrating agent runs, not a complex query interface
- Current surface is 6 endpoints with clean REST sub-resource patterns (`/runs/{id}/events`, `/runs/{id}/input`)
- SSE for event streaming is REST-native; GraphQL subscriptions (WebSocket-based) would add complexity for no benefit
- New endpoints (`/steer`, `/continue`) are imperative actions, not data mutations — REST verbs express this naturally
- Go stdlib makes REST trivial; GraphQL requires schema/codegen layer (gqlgen etc.) that's overkill here
- No client needs complex field selection, cross-resource queries, or varied data shapes

**When to revisit**: If a dashboard or analytics layer needs to query across many runs with filters, pagination, and field selection — a read-heavy client with varied data needs. That would be a separate read API, not a replacement for the core run orchestration API.

### Issues Created

- [#1](https://github.com/dennisonbertram/go-agent-harness/issues/1) — Stream tool output incrementally during execution
- [#2](https://github.com/dennisonbertram/go-agent-harness/issues/2) — Audit SSE events for completeness and consistency
- [#3](https://github.com/dennisonbertram/go-agent-harness/issues/3) — Make max steps tunable per-run, default to unlimited
- [#4](https://github.com/dennisonbertram/go-agent-harness/issues/4) — Implement deferred (lazy-loaded) tools via ToolSearch meta-tool
- [#5](https://github.com/dennisonbertram/go-agent-harness/issues/5) — Add run continuation for multi-turn conversations
- [#6](https://github.com/dennisonbertram/go-agent-harness/issues/6) — Add mid-run steering for user guidance during execution

### Architecture Direction: Platform Backend (CLI + GUI)

Established that the harness is a **Go backend platform** supporting multiple frontends (CLI, web GUI, desktop app). Must work transparently in both local and remote modes — remote execution should feel like local, and vice versa.

Key architectural pieces identified:
- **Persistence layer** (#7) — foundational, everything else depends on it
- **Workspace abstraction** (#8) — transparent local/remote via `Workspace` interface + optional proxy agent on user's machine
- **Client auth** (#9) — API keys, tenant isolation, scoped permissions
- **Cost/safety controls** (#10) — cost ceilings, idle detection, spending limits (critical once max steps goes unlimited)
- **Multi-provider** (#11) — Anthropic alongside OpenAI, auto-detect from model name, prompt caching

### Codex CLI Architecture Study

Researched OpenAI Codex CLI (Rust, 65+ crates, Apache-2.0) for architectural patterns. Findings in `docs/research/codex-cli-architecture.md`. Created issues for the most impactful patterns:

- [#15](https://github.com/dennisonbertram/go-agent-harness/issues/15) — Two-axis permission model (sandbox × approval policy)
- [#16](https://github.com/dennisonbertram/go-agent-harness/issues/16) — JSONL rollout recorder for replay/fork/audit
- [#17](https://github.com/dennisonbertram/go-agent-harness/issues/17) — Conversation compaction for unlimited-step sessions
- [#18](https://github.com/dennisonbertram/go-agent-harness/issues/18) — Head-tail buffer for long process output
- [#19](https://github.com/dennisonbertram/go-agent-harness/issues/19) — Bidirectional MCP (client + server)
- [#20](https://github.com/dennisonbertram/go-agent-harness/issues/20) — Layered configuration cascade with cloud/team overrides

Skipped creating separate issues for Op/EventMsg protocol (already covered by SSE event audit #2 and the existing architecture) and Codex's skills/memories system (observational memory already covers this).

### Research

- Deferred tools design doc written to `docs/research/deferred-tools-design.md` — covers Claude Code's ToolSearch pattern, Go implementation strategy, token savings analysis (40-60%), and comparison of alternatives (intent filtering, tiered packs, description compression, dynamic pruning). Recommended approach: ToolSearch + tiered packs.

## 2026-03-04

- Initialized repository scaffold.
- Added operating policy (`AGENTS.md`) with strict TDD, worktree-first, and pre-commit testing requirements.
- Created docs structure with indexes, logs, context, plans, and runbooks.
- Added merge helper script: `scripts/verify-and-merge.sh`.
- Refactored `AGENTS.md` into a bootstrap reference map for faster onboarding.
- Added long-term thinking log (`docs/logs/long-term-thinking-log.md`) with command-intent and user-intent precedence.
- Added UX requirements doc (`docs/design/ux-requirements.md`).
- Added completed bootstrap plan/checklist (`docs/plans/2026-03-04-repo-bootstrap-plan.md`).
- Updated merge workflow to auto-push `main` in `scripts/verify-and-merge.sh`.
- Updated worktree runbook and AGENTS guidance to reflect process-guided enforcement (no hard gating yet).
- Added explicit response-clarity policy requiring `Task status: DONE` / `Task status: NOT DONE`.
- Updated agent completion and nightly-task docs to require status-first reporting.

## 2026-03-04 (OpenAI Harness POC)

- Added Go module and executable service entrypoint: `cmd/harnessd/main.go`.
- Implemented core harness runtime in `internal/harness/`:
  - Deterministic run loop with bounded steps.
  - Event history + live subscriber fanout.
  - In-memory run state with status/output/error tracking.
  - Tool registry with schema metadata and execution dispatch.
- Added default proof-of-concept tools:
  - `list_files` (workspace-scoped listing, recursive/non-recursive).
  - `read_file` (workspace-scoped reads with byte limit + truncation flag).
  - `run_go_test` (bounded timeout + restricted package pattern).
- Implemented OpenAI provider adapter in `internal/provider/openai/client.go` against `/v1/chat/completions` with function-tool schema mapping and tool-call parsing.
- Implemented HTTP server in `internal/server/http.go`:
  - `POST /v1/runs`
  - `GET /v1/runs/{runID}`
  - `GET /v1/runs/{runID}/events` (SSE)
  - `GET /healthz`
- Added tests first, then implemented to green:
  - `internal/harness/runner_test.go`
  - `internal/harness/tools_test.go`
  - `internal/provider/openai/client_test.go`
  - `internal/server/http_test.go`
- Updated README with setup, API contract, event taxonomy, and quick-start usage.

## 2026-03-04 (Toolset Update: read/write/edit/bash)

- Replaced default harness tool registrations in `internal/harness/tools_default.go`:
  - Removed `list_files`, `read_file`, `run_go_test`.
  - Added `read`, `write`, `edit`, `bash`.
- Implemented `write` with create/overwrite/append support and parent directory creation.
- Implemented `edit` with single/replace-all text replacement and explicit error when `old_text` is not found.
- Implemented `bash` command execution with timeout, workspace working directory confinement, and deny-list guardrails for dangerous commands.
- Rewrote `internal/harness/tools_test.go` with failing-first assertions for new tools and safety constraints.
- Ran full suite to confirm no behavior regressions outside toolset update.

## 2026-03-04 (Function Coverage Expansion)

- Added `cmd/harnessd/main_test.go` to cover entrypoint logic and env helpers:
  - `main` success/error exit behavior (via test hooks).
  - `run` delegation behavior.
  - `runWithSignals` missing key, provider failure, and graceful shutdown.
  - `getenvOrDefault` and `getenvIntOrDefault`.
- Refactored `cmd/harnessd/main.go` for testability while preserving runtime behavior:
  - Introduced `runMain`, `exitFunc`, and `runWithSignalsFunc` hooks.
  - Converted fatal exits in internal flow to returned errors handled in `main`.
- Expanded `internal/harness/runner_test.go` with failure-path coverage:
  - Provider error run failure path.
  - `failRun(nil)` default error path.
  - `mustJSON` marshal-failure fallback.
- Expanded `internal/server/http_test.go` with handler error/edge coverage:
  - `GET /healthz`.
  - method-not-allowed checks.
  - invalid JSON handling.
  - not-found run and event stream paths.
- Coverage verification:
  - `go test ./... -coverprofile=coverage.out`
  - `go tool cover -func=coverage.out`
  - Total statement coverage now `81.0%`.
  - All functions report non-zero coverage.

## 2026-03-05 (Regression Guardrails Automation)

- Added coverage-gate library and tests:
  - `internal/quality/coveragegate/gate.go`
  - `internal/quality/coveragegate/gate_test.go`
- Added coverage-gate CLI and tests:
  - `cmd/coveragegate/main.go`
  - `cmd/coveragegate/main_test.go`
- Added regression contract test for default tool interface:
  - `internal/harness/tools_contract_test.go` (asserts `bash`, `edit`, `read`, `write` contract).
- Added automated regression script:
  - `scripts/test-regression.sh`
  - Runs `go test`, `go test -race`, coverage profile generation, and coverage gate checks.
- Added CI workflow:
  - `.github/workflows/test-regression.yml`
  - Executes regression script on `pull_request` and `push` to `main`.
- Updated testing and worktree runbooks + README development commands to use regression script as default quality gate.
- Verified full regression suite passes locally with coverage gate result:
  - `coveragegate: PASS (total=81.1%, min=80.0%, zero-functions=0)`.

## 2026-03-05 (Hooks + Baseline Tools Expansion)

- Added hook contracts and runner integration in `internal/harness`:
  - New hook types/interfaces in `types.go` (`PreMessageHook`, `PostMessageHook`, `HookAction`, `HookFailureMode`).
  - Runner hook pipeline in `runner.go`:
    - Pre-message hooks executed before provider call.
    - Post-message hooks executed after provider call.
    - Hook events emitted: `hook.started`, `hook.completed`, `hook.failed`.
    - Blocking and mutation semantics with fail-open/fail-closed modes.
- Added hook-focused tests in `internal/harness/hooks_test.go`:
  - Mutation, blocking, fail-open, and fail-closed behavior for pre and post hooks.
- Expanded default toolset in `internal/harness/tools_default.go`:
  - Added baseline tools:
    - `ls`
    - `glob`
    - `grep`
    - `apply_patch`
    - `git_status`
    - `git_diff`
  - Kept existing tools:
    - `read`, `write`, `edit`, `bash`
- Expanded tool tests in `internal/harness/tools_test.go`:
  - New baseline tool behavior and validation/error branches.
  - Additional branch coverage for helper functions and command execution paths.
- Updated default tool contract test in `internal/harness/tools_contract_test.go`.
- Updated README to document hooks and expanded tool list.
- Validation:
  - `go test ./...` passed.
  - `./scripts/test-regression.sh` passed.
  - Coverage gate after changes: `PASS (total=80.8%, min=80.0%, zero-functions=0)`.
- Live OpenAI verification (local key, `gpt-5-nano`, tmux-hosted harness):
  - Confirmed successful run with `run.completed`.
  - Observed tool calls for `ls`, `write`, `apply_patch`, `grep`, `git_status`, `git_diff` in event stream.

## 2026-03-05 (Sample CLI Test Client)

- Added a new CLI client in `cmd/harnesscli/main.go` to test harness connectivity quickly from terminal.
- Implemented CLI flow:
  - Parse flags (`-base-url`, `-prompt`, `-model`, `-system-prompt`).
  - Create run via `POST /v1/runs`.
  - Stream and print lifecycle events from `GET /v1/runs/{id}/events`.
  - Stop on terminal events (`run.completed`, `run.failed`) with explicit terminal summary output.
- Added full TDD coverage in `cmd/harnesscli/main_test.go`:
  - `main` exit delegation.
  - Create-run payload contract validation.
  - SSE block parsing + event decode + terminal detection.
  - End-to-end CLI success path.
  - Non-2xx create/stream regression paths.
  - Invalid SSE data handling path.
- Validation:
  - `go test ./cmd/harnesscli`
  - `go test ./...`
  - `./scripts/test-regression.sh` (pass, coverage gate pass)
- Live OpenAI verification (local key, `gpt-5-nano`, tmux-hosted harness):
  - Ran CLI end-to-end with prompt to create `demo/live-cli-smoke.html`.
  - Observed real `bash`, `write`, and `ls` tool calls in stream.
  - Completed with `terminal_event=run.completed`.
- Added operator documentation:
  - `docs/runbooks/harnesscli-live-testing.md`
  - Includes tmux commands, variable map, expected outputs, known live-run issues, and troubleshooting.

## Entry Template

- Date:
- Task:
- Change summary:
- Tests added/updated:
- Bugs fixed:
- Regression tests added:
- Docs updated:

## 2026-03-05 (Modular Tooling Migration + Crush-Informed Expansion)

- Refactored tool implementation into modular package: `internal/harness/tools/`.
  - Added catalog-driven registration (`catalog.go`) and common shared utilities (`common_paths.go`, `common_exec.go`, `common_result.go`, `policy.go`).
  - Migrated and modularized existing tools (`read`, `write`, `edit`, `bash`, `ls`, `glob`, `grep`, `apply_patch`, `git_status`, `git_diff`).
- Added Phase 1/2/3 tool contracts and implementations with dependency-gated registration:
  - `job_output`, `job_kill`
  - `fetch`, `download`
  - `todos`
  - `lsp_diagnostics`, `lsp_references`, `lsp_restart`
  - `sourcegraph` (registered when endpoint configured)
  - `list_mcp_resources`, `read_mcp_resource`, dynamic `mcp_<server>_<tool>` (registered when MCP registry provided)
  - `agent`, `agentic_fetch`, `web_search`, `web_fetch` (registered when integrations provided)
- Added approval-mode seam and compatibility wiring:
  - New harness types for `ToolApprovalMode`, `ToolPolicy`, policy input/output.
  - Added `HARNESS_TOOL_APPROVAL_MODE` env wiring in `cmd/harnessd/main.go`.
  - Added `NewDefaultRegistryWithPolicy(...)` while preserving `NewDefaultRegistry(...)` compatibility.
- Updated runner tool execution context to include run ID for run-scoped tools (used by `todos`).
- Expanded test coverage heavily for modular package and compatibility wrappers:
  - `internal/harness/tools/catalog_test.go`
  - `internal/harness/tools/coverage_boost_test.go`
  - `internal/harness/tools/coverage_extra_test.go`
  - `internal/harness/tools_default_test.go`
  - Updated `internal/harness/tools_contract_test.go` expected tool surface.
  - Updated `cmd/harnessd/main_test.go` for approval-mode env parser.
- Fixed live OpenAI schema issue discovered during tmux smoke test:
  - Added explicit `items` schemas for array properties in `apply_patch.edits` and `todos.todos`.
- Validation:
  - `go test ./...` passed.
  - `./scripts/test-regression.sh` passed.
  - Coverage gate after migration: `PASS (total=80.0%, min=80.0%, zero-functions=0)`.
- Live OpenAI verification (tmux-hosted harness + `gpt-5-nano`):
  - Confirmed `run.completed` with real tool usage (`ls`, `write`, `read`) and generated file verification.

## 2026-03-05 (Claude-Compatible AskUserQuestion Tool)

- Added a new first-class `AskUserQuestion` tool in `internal/harness/tools/ask_user_question.go` with Claude-compatible schema and result payload (`questions` + `answers`).
- Added tool-side validation and answer normalization helpers:
  - 1-4 questions, 2-4 options per question.
  - required `question/header/options/multiSelect` fields.
  - unique question text and option labels.
  - multi-select answer normalization to comma-separated labels.
- Added broker interfaces and context helpers in `internal/harness/tools/types.go`:
  - `AskUserQuestionBroker`, `AskUserQuestionRequest`, `AskUserQuestionPending`.
  - `ContextKeyToolCallID` / `ToolCallIDFromContext`.
- Added in-memory broker implementation in `internal/harness/ask_user_broker.go`:
  - one pending question per run.
  - blocking wait in `Ask`.
  - typed timeout error path.
  - submission validation with invalid-input preservation.
- Updated tool catalog/default registry wiring:
  - `AskUserQuestion` now registers in default toolset.
  - new registry options support broker + timeout injection.
- Updated runner behavior:
  - new status `waiting_for_user`.
  - emits `run.waiting_for_user` and `run.resumed` events.
  - fails run immediately on typed AskUserQuestion timeout.
  - adds tool call id into tool execution context.
  - new runner methods for input API: `PendingInput` and `SubmitInput`.
- Updated HTTP server API in `internal/server/http.go`:
  - `GET /v1/runs/{runID}/input`
  - `POST /v1/runs/{runID}/input`
  - error contracts: `404` missing run, `409` no pending input, `400` invalid JSON/request.
- Updated runtime wiring in `cmd/harnessd/main.go`:
  - new env var `HARNESS_ASK_USER_TIMEOUT_SECONDS` (default `300`).
  - shared in-memory broker injected into both registry and runner.
- Added/updated tests:
  - `internal/harness/tools/ask_user_question_test.go`
  - `internal/harness/ask_user_broker_test.go`
  - `internal/harness/runner_test.go` (wait/resume and timeout paths)
  - `internal/server/http_test.go` (input endpoint lifecycle and error semantics)
  - `internal/harness/tools/catalog_test.go` and `internal/harness/tools_contract_test.go` (tool contract update)
  - `cmd/harnessd/main_test.go` (ask-user timeout env parsing)

## 2026-03-05 (Token Counting + Cost Tracking)

- Added additive accounting types in `internal/harness/types.go`:
  - `CompletionUsage` optional detail fields.
  - `CompletionCost`, `UsageStatus`, `CostStatus`.
  - Run-level totals: `RunUsageTotals`, `RunCostTotals`.
- Added pricing module in `internal/provider/pricing/`:
  - file-backed JSON catalog loader.
  - provider/model resolver with alias support.
  - unit tests for load/resolve/validation behavior.
- Extended OpenAI adapter (`internal/provider/openai/client.go`):
  - parses usage + detail fields.
  - normalizes missing usage to zero + `provider_unreported`.
  - computes cost from explicit response cost when present, otherwise resolver-driven pricing.
  - emits `unpriced_model` when pricing is unavailable.
- Extended runner accounting (`internal/harness/runner.go`):
  - per-turn accumulation of usage/cost totals.
  - new `usage.delta` event each model turn.
  - `run.completed` and `run.failed` now include usage/cost totals payloads.
  - run state includes persisted totals exposed by `GET /v1/runs/{id}`.
- Updated runtime context (`internal/systemprompt/runtime_context.go`):
  - replaced phase-1 cost placeholder with live token/cost fields.
  - default `cost_status: pending` before first completion.
- Wired pricing config in server startup (`cmd/harnessd/main.go`):
  - `HARNESS_PRICING_CATALOG_PATH` enables resolver-backed cost computation.
- Updated tests:
  - `internal/provider/openai/client_test.go`
  - `internal/provider/pricing/catalog_test.go`
  - `internal/harness/runner_test.go`
  - `internal/harness/runner_prompt_test.go`
  - `internal/systemprompt/engine_test.go`
  - `internal/server/http_test.go`
- Validation:
  - `go test ./...` passed.
  - `go test ./... -race` passed.
  - `./scripts/test-regression.sh` passed (`coveragegate: PASS`, total `80.1%`, zero-functions `0`).

## 2026-03-05 (Token/Cost Documentation Pass)

- Updated `README.md` to fully document:
  - `GET /v1/runs/{id}` usage/cost totals fields.
  - `usage.delta` payload contract.
  - missing-usage and missing-pricing behavior.
  - pricing catalog JSON format and configuration.
- Updated `docs/runbooks/harnesscli-live-testing.md`:
  - added `HARNESS_PRICING_CATALOG_PATH`.
  - documented expectation that `usage.delta` appears during runs.
- Updated `docs/design/system-prompt-architecture.md` heading/scope text to reflect OpenAI-first implementation status.
- Updated `docs/plans/INDEX.md` to mark token/cost plan as completed.

## 2026-03-05 (Optional Observational Memory: Local-First Foundation)

- Added new subsystem package: `internal/observationalmemory/`.
  - Core manager orchestration and state model (`manager.go`, `types.go`).
  - Model-backed observer + reflector implementations (`observer.go`, `reflector.go`).
  - Local per-scope coordinator (`coordinator.go`).
  - SQLite durable store with migration-safe schema (`store_sqlite.go`, migrations).
  - Postgres compile-ready stub for future activation (`store_postgres.go`).
- Added transcript/runtime context seams in tool layer:
  - `RunMetadata` and read-only `TranscriptReader` in `internal/harness/tools/types.go`.
- Added new tool: `observational_memory` in `internal/harness/tools/observational_memory.go`.
  - Actions: `enable`, `disable`, `status`, `export`, `review`, `reflect_now`.
- Wired tool catalog/default registry to include observational memory manager.
- Updated runner integration in `internal/harness/runner.go`:
  - Stores run transcript snapshots.
  - Injects `<observational-memory>` snippet before model turns when enabled.
  - Calls memory observe flow after each turn/tool cycle.
  - Emits memory lifecycle events (`memory.observe.*`, `memory.reflection.completed`).
  - Passes run metadata + transcript reader into tool execution context.
- Expanded run API metadata fields in `internal/harness/types.go`:
  - `tenant_id`, `conversation_id`, `agent_id` on `RunRequest` and `Run`.
- Updated server bootstrap in `cmd/harnessd/main.go`:
  - Added memory env config parsing and manager creation.
  - Wired shared manager into registry + runner.
- Added/updated tests for new surfaces:
  - `internal/harness/tools/observational_memory_test.go`
  - `internal/harness/runner_test.go` memory snippet/event coverage
  - Tool contract/catalog/default-registry expected tool list updates.
- Added architecture and runbook docs:
  - `docs/design/observational-memory-architecture.md`
  - `docs/runbooks/observational-memory.md`
- Updated roadmap/index/readme docs to include observational memory and configuration.

## 2026-03-05 (Modular System Prompt Subsystem)

- Added new prompt engine module in `internal/systemprompt/`:
  - `catalog.go`: YAML catalog loading/validation and prompt asset indexing.
  - `matcher.go`: deterministic model profile routing with fallback signaling.
  - `engine.go`: static prompt composition for base/intent/model/extensions/custom layers.
  - `runtime_context.go`: per-turn ephemeral runtime context formatter.
  - `types.go`, `errors.go`, `validation.go` for subsystem contracts.
- Added file-driven prompt assets under `prompts/`:
  - `catalog.yaml`
  - `base/main.md`
  - `intents/{general,code_review,frontend_design}.md`
  - `models/{default,openai_gpt5}.md`
  - starter behavior/talent extensions.
- Expanded run request model in `internal/harness/types.go`:
  - `agent_intent`, `task_context`, `prompt_profile`, `prompt_extensions`.
  - reserved `skills` field retained for forward compatibility and ignored in phase 1.
- Updated runner integration in `internal/harness/runner.go`:
  - resolve prompt context at `StartRun`.
  - preserve `system_prompt` override bypass behavior.
  - rebuild provider messages each turn using static prompt + ephemeral runtime context + transcript.
  - emit `prompt.resolved` and `prompt.warning` events.
  - keep runtime context non-persistent in transcript state.
- Updated server bootstrap in `cmd/harnessd/main.go`:
  - startup loads prompt engine from `HARNESS_PROMPTS_DIR` (with default auto-discovery).
  - added `HARNESS_DEFAULT_AGENT_INTENT` config.
  - startup fails fast on invalid prompt catalog/files.
- Updated CLI in `cmd/harnesscli/main.go`:
  - new flags for intent/profile/extensions (`-agent-intent`, `-task-context`, `-prompt-profile`, `-prompt-behavior`, `-prompt-talent`, `-prompt-custom`).
- Added/updated tests:
  - `internal/systemprompt/{catalog,matcher,engine}_test.go`
  - `internal/harness/runner_prompt_test.go`
  - `internal/server/http_prompt_test.go`
  - `cmd/harnesscli/main_prompt_test.go`
- Validation:
  - Focused suites passed: `go test ./internal/systemprompt ./internal/harness ./internal/server ./cmd/harnesscli ./cmd/harnessd`.

## 2026-03-06 (Terminal Bench Periodic Smoke Suite)

- Added a private Terminal Bench integration under `benchmarks/terminal_bench/`.
- Added custom benchmark agent bridge in `benchmarks/terminal_bench/agent.py`:
  - Copies the current repository into each task container.
  - Builds `harnessd` and `harnesscli` inside the container.
  - Starts the harness in tmux and drives tasks through the real HTTP API.
- Added three stable smoke tasks:
  - `go-retry-schedule-fix`
  - `staging-deploy-docs`
  - `incident-summary-shell`
- Added local runner script:
  - `scripts/run-terminal-bench.sh`
  - Uses `tb` when installed or falls back to `uv tool run terminal-bench`.
- Added scheduled workflow:
  - `.github/workflows/terminal-bench-periodic.yml`
  - Runs nightly and on manual dispatch, then uploads benchmark artifacts.
- Added operator documentation:
  - `docs/runbooks/terminal-bench-periodic-suite.md`
- Updated README, nightly tasks, plan tracker, and indexes to reflect the new benchmark path.
- Validation:
  - Not run in this change set.

## 2026-03-25 (HTTP Catalog Route Group Follow-up)

- Continued issue #427 after `origin/main` absorbed the earlier run/conversation extraction, leaving the catalog transport responsibilities inline in `internal/server/http.go`.
- Extracted the remaining catalog/provider/summarize HTTP transport into `internal/server/http_catalog.go` and updated mux wiring to register the catalog route group from one seam.
- Added route-group regression coverage in `internal/server/http_route_groups_test.go` to lock the `/v1/models`, `/v1/providers`, and `/v1/summarize` registration behavior to the extracted helper.
- Validation:
  - `go test ./internal/server -run 'TestRegister(Run|Conversation|Catalog)Routes' -count=1`

## 2026-04-05 (Stages 2-5 Orchestration Runtime)

- Added persistent checkpoint subsystem in `internal/checkpoints/`:
  - SQLite + memory stores
  - waiter/notify service
  - checkpoint-backed approval and ask-user brokers
  - HTTP routes for `GET /v1/checkpoints/{id}` and `POST /v1/checkpoints/{id}/resume`
- Added workflow runtime in `internal/workflows/`:
  - YAML-backed definitions
  - `tool`, `run`, `checkpoint`, and `branch` step execution
  - persisted workflow runs, step states, and workflow event streams
  - HTTP routes for `/v1/workflows*` and `/v1/workflow-runs*`
- Added explicit working memory in `internal/workingmemory/`:
  - SQLite + memory stores
  - core `working_memory` tool
  - runner prompt injection ahead of observational-memory snippets
- Added network compiler/runtime in `internal/networks/`:
  - YAML-backed network definitions
  - workflow-backed sequential role execution
  - HTTP routes for `/v1/networks*`
- Wired `cmd/harnessd` to:
  - open shared SQLite-backed checkpoint/workflow/working-memory stores
  - load workflow and network definitions from `HARNESS_WORKFLOWS_DIR` / `HARNESS_NETWORKS_DIR`
  - use checkpoint-backed approval/input brokers in the live runner
- Added failing-first tests for each new stage plus broader integration coverage in:
  - `internal/checkpoints/service_test.go`
  - `internal/harness/checkpoint_broker_test.go`
  - `internal/workflows/engine_test.go`
  - `internal/workingmemory/store_test.go`
  - `internal/harness/runner_working_memory_test.go`
  - `internal/networks/engine_test.go`
  - `internal/server/http_checkpoints_test.go`
  - `internal/server/http_workflows_test.go`
  - `internal/server/http_networks_test.go`
- Validation:
  - `go test ./internal/checkpoints ./internal/workflows ./internal/networks ./internal/workingmemory ./internal/harness ./internal/harness/tools/core ./internal/server ./cmd/harnessd -count=1`
- Fixed a shutdown bookkeeping race in `internal/symphd/dispatcher.go` where `Shutdown(...)` could return after semaphore drain but before deferred cleanup removed entries from `d.running`.
  - Added `TestDispatcher_ShutdownWaitsForRunningCleanup` in `internal/symphd/dispatcher_test.go` as the failing-first regression for the race.
  - Updated `Shutdown(...)` to:
    - release any partially acquired semaphore slots on context cancellation
    - wait for `d.running` to drain to zero before returning
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/symphd -run 'TestDispatcher_(Shutdown|ShutdownWaitsForRunningCleanup)' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/symphd -count=1`
- Reduced `-race` test-suite timeouts in API-key-heavy packages without changing production hashing behavior.
  - Added low-cost test-only API-key helpers in:
    - `internal/store/apikey_test_helpers_test.go`
    - `internal/server/apikey_test_helpers_test.go`
  - Swapped the slow `store.GenerateAPIKey(...)` test call sites in:
    - `internal/store/apikeys_test.go`
    - `internal/server/auth_scope_test.go`
    - `internal/server/auth_test.go`
    - `internal/server/http_auth_test.go`
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/store -race -run TestAPIKey_SQLite -count=1 -timeout 2m`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -race -count=1 -timeout 5m`
- Replaced the shell-output fixture in `internal/cron/executor_test.go` with a faster `awk` generator so truncation coverage stays stable under heavier regression-suite load.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/cron -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/cron -race -count=1`

## 2026-04-08 (Repo-Wide Regression Cleanup Follow-up)

- Fixed transcript export default-path selection in `cmd/harnesscli/tui/components/transcriptexport/export.go`.
  - `DefaultOutputDir()` now probes the cache, home, and temp candidates and returns the first writable absolute directory instead of assuming the cache path is usable.
  - Added `TestSelectRuntimeSafeOutputDirSkipsUnwritableCandidates` in `cmd/harnesscli/tui/components/transcriptexport/export_internal_test.go` to lock the fallback behavior when the preferred directory is not writable.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/components/transcriptexport -run 'TestTUI059_(ExportDefaultOutputDirCreatesFileOutsideWorkingDirectory|ExportDefaultOutputDir)|TestSelectRuntimeSafeOutputDirSkipsUnwritableCandidates' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui -run TestExportCommandWritesOutsideWorkingDirectory -count=1`
- Hardened rollout integration timing in `internal/rollout/integration_test.go`.
  - Replaced the fixed post-terminal sleep with polling for a terminal JSONL event so the test matches the recorder's asynchronous flush semantics under full-suite load.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/rollout -run TestRunnerRollout_RunProducesJSONL -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/rollout -count=1`
- Repo-wide verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run TestMatrix_ConclusionWatcherEnabledWithEvaluator -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/... -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/... ./cmd/... -count=1`
  - `git diff --check`

## 2026-06-26 (Reliability T02 Terminal Event Fanout)

- Moved terminal event store append and subscriber fanout out of the runner mutex while preserving append-before-subscriber-observe ordering.
- Added a subscriber send/close guard for terminal fanout so cancellation cannot race a captured terminal subscriber channel.
- Added `TestTerminalStoreAppendDoesNotBlockRunnerQueries`, which blocks terminal event persistence and verifies unrelated run queries still return.
- Updated the terminal ordering test to exercise the out-of-lock publish path directly.
- Validation:
  - `go test ./internal/harness -run 'TestTerminalStoreAppendDoesNotBlockRunnerQueries|TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification' -count=1`
  - `go test ./internal/harness -race -run 'TestTerminalStoreAppendDoesNotBlockRunnerQueries|TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification|TestRunnerPrune|TestRecorderGoroutine_DoneClosedAfterRun' -count=1`

## 2026-06-26 (Reliability T04 Background Bash Shutdown)

- Bound background bash jobs to the tool execution context instead of `context.Background()`, so run cancellation terminates background jobs.
- Added `JobManager.Shutdown(ctx)` to cancel tracked jobs, wait for their `cmd.Wait` goroutines, and clear the jobs map.
- Added registry shutdown hooks and wired default registries to shut down their bash job manager.
- Updated `Runner.Shutdown` to invoke shutdown hooks once for the base registry and any per-run workspace registries after active runs are cancelled or drained.
- Added failing-first coverage for run-context cancellation, job-manager shutdown cleanup, and runner-level registry shutdown invocation.
- Validation:
  - `go test ./internal/harness/tools -run 'TestRunBackgroundCancelsWithRunContext|TestJobManagerShutdownCancelsAndClearsJobs' -count=1`
  - `go test ./internal/harness -run TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns -count=1`
  - `go test ./internal/harness/tools ./internal/harness -race -run 'TestRunBackgroundCancelsWithRunContext|TestJobManagerShutdownCancelsAndClearsJobs|TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns|TestRunnerShutdownStopsPoolDispatcher|TestRunnerShutdownIdempotent' -count=1`

## 2026-06-26 (Reliability T05 Scoped MCP Shutdown)

- Added a shutdown sweep that closes scoped per-run MCP registries for all live run states after shutdown cancellation or normal drain.
- Made `closeScopedMCP` atomically detach `state.scopedMCPRegistry` before closing so re-entry is a no-op and closed registries are not retained in memory.
- Added an `execute()` defer safety net so scoped MCP registries are closed even when execution exits outside the normal terminal helpers.
- Added `TestRunnerShutdownClosesWedgedScopedMCPRegistry`, which attaches an already-connected scoped MCP registry to a wedged run and verifies shutdown closes and clears it.
- Validation:
  - `go test ./internal/harness -run TestRunnerShutdownClosesWedgedScopedMCPRegistry -count=1`
  - `go test ./internal/harness -race -run 'TestRunnerShutdownClosesWedgedScopedMCPRegistry|TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns|TestScopedMCPRegistry_Close|TestRunPreflight_BuildsScopedMCPRegistry|TestStartRun_MCPServers' -count=1`

## 2026-06-26 (Reliability T06 Shared Audit Buckets)

- Added shared audit writer buckets keyed by UTC date so same-day runs append through one runner-owned writer instead of one writer per run.
- Changed terminal audit cleanup to detach run state from the shared writer; buckets are closed once during `Runner.Shutdown`.
- Added `TestAuditTrail_ActiveRunsShareDateBucketWriter`, which keeps two same-day runs active and verifies both point at the same audit writer.
- Preserved existing audit persistence and hash-chain behavior with the writer's internal mutex and file-lock chain resume.
- Validation:
  - `go test ./internal/harness -run 'TestAuditTrail_ActiveRunsShareDateBucketWriter|TestAuditTrail_HashChainValid|TestAuditTrail_RunStarted_WrittenOnEnable|TestAuditTrail_RunCompleted_Written|TestTerminalSealing_AuditWriterWithRolloutDirClosesOnTerminal|TestTerminalSealing_AuditWriterFailedRunClosesOnTerminal' -count=1`
  - `go test ./internal/harness ./internal/forensics/audittrail -race -run 'TestAuditTrail_|TestTerminalSealing_AuditWriter|TestAuditWriter_(ConcurrentWrites|HashChain|HashChainIntegrity|CloseIdempotent|WriteAfterClose)' -count=1`

## 2026-06-26 (Reliability T07 Pool Dispatcher Recovery)

- Wrapped each bounded-pool dispatcher iteration with panic recovery so one bad queued item cannot kill the dispatcher goroutine.
- On dispatcher panic, the runner now releases the acquired worker token, marks the affected queued run failed, decrements its inflight count, logs the panic, and continues dispatching later queued items.
- Added a deterministic `poolDispatchHook` test seam and `TestPoolDispatcherRecoverKeepsDispatchAlive`, which queues work behind a held worker, panics one queued item, and verifies later items still complete and shutdown does not hang.
- Validation:
  - `go test ./internal/harness -run TestPoolDispatcherRecoverKeepsDispatchAlive -count=1`
  - `go test ./internal/harness -race -run 'TestPoolDispatcherRecoverKeepsDispatchAlive|TestRunnerShutdownDrainsBufferedQueue|TestRunnerShutdownStopsPoolDispatcher|TestPanicInProviderEmitsRunFailed|TestPanicInToolHandlerEmitsRunFailed' -count=1`

## 2026-06-26 (Reliability T08 Container Workspace Cleanup)

- Added a small Docker-client interface seam so container lifecycle cleanup can be tested without a live Docker daemon.
- `Provision` now records the workspace path as soon as the directory is created and force-destroys partial resources on create/start/inspect/config-write failures.
- `Destroy` now uses its own bounded background context for stop/remove, force-removes the container, and removes the workspace directory after successful cleanup.
- Added fake-client coverage for start-failure cleanup, workspace directory removal, and destroy behavior when the caller context is already cancelled.
- Validation:
  - `go test ./internal/workspace -run 'TestContainerWorkspace_(ProvisionStartErrorCleansContainerAndWorkspaceDir|DestroyRemovesWorkspaceDir|DestroyUsesForceContextWhenCallerContextCancelled)' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T09 VM Post-Create Cleanup)

- `HetznerProvider.Create` now best-effort deletes the created server with a bounded background context when polling, timeout, disappearance, or caller cancellation happens after `Server.Create` succeeds.
- `VMWorkspace.Provision` now stores `vmID` immediately after provider create succeeds and before post-create setup, so caller cleanup can delete the VM if later setup fails.
- Added an HTTP-backed Hetzner regression for delete-after-poll-error and a VMWorkspace regression that simulates post-create failure and verifies `Destroy` deletes the retained VM ID.
- Validation:
  - `go test ./internal/workspace -run 'TestHetznerProvider_CreateDeletesServerAfterPollingError|TestVMWorkspace_ProvisionKeepsVMIDOnPostCreateError' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T10 Worktree Serialization)

- Added a `runGitCommand` seam and per-repo mutex so `git worktree add`, `git worktree remove`, `git branch -D`, and `git worktree prune` are serialized by repository path.
- `Destroy` now runs `git worktree prune` even when worktree removal returns an error.
- `Pool.Close` now prunes each distinct worktree repository path once after destroying live workspaces.
- Added focused coverage for same-repo add serialization, prune-after-remove-error, and distinct repo pruning from pool close.
- Validation:
  - `go test ./internal/workspace -run 'TestWorktreeWorkspace_(ProvisionSerializesWorktreeAddPerRepo|DestroyPrunesAfterRemoveError)|TestPoolClosePrunesEachDistinctWorktreeRepoOnce' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T11 Bash Streaming Long Lines)

- Replaced the foreground bash streaming `bufio.Scanner` with a draining `bufio.Reader` loop.
- The streamer now caps each emitted line at `defaultMaxStreamLineBytes` while continuing to drain the rest of an overlong line, preventing subprocess pipe blockage.
- Added result metadata for stream truncation: `stream_truncated`, `max_line_bytes`, and `stream_error`.
- Added a regression that streams a 4 MiB single line and verifies the command returns promptly without timing out.
- Validation:
  - `go test ./internal/harness/tools -run TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly -count=1`
  - `go test ./internal/harness/tools -count=1`
  - `go test ./internal/harness/tools -race -count=1`

## 2026-06-26 (Reliability T12 Cron Tenant Isolation)

- Added `tenant_id` ownership to cron job types, create requests, the HTTP cron client, the embedded cron adapter, and the SQLite cron store.
- `POST /v1/cron/jobs` now stamps jobs from the authenticated tenant context; list/get/update/delete/pause/resume only expose jobs for that tenant and return `404 not_found` on cross-tenant access.
- Cron by-ID handlers now distinguish typed job-not-found errors from backend failures, so real store/client errors return `500 internal_error`.
- Added SQLite migration coverage for legacy `cron_jobs` tables without `tenant_id`, plus persistence coverage for create/get/update/list and missing-delete not-found behavior.
- Updated `TestWorkerPoolLoad` to set `MaxCompletedRetention: totalRuns`; after T01, the default terminal-run retention is 32 and the load test starts 50 runs, so the test must opt into retention for its final GET assertions.
- Validation:
  - `go test ./internal/server -run 'TestCron(GetJob_Returns500ForBackendError|Jobs_AreTenantIsolated)' -count=1` failed before implementation because `tools.CronJob.TenantID` did not exist.
  - `go test ./internal/server ./internal/cron -run 'TestCron(GetJob_Returns500ForBackendError|Jobs_AreTenantIsolated)|Test(CreateJob_PreservesTenantID|Migrate_AddsTenantIDToExistingCronJobs|DeleteJob_NotFound)' -count=1`
  - `go test ./internal/server ./internal/cron ./cmd/harnessd -count=1`
  - `go test ./internal/server ./internal/cron ./cmd/harnessd -race -count=1`

## 2026-06-26 (Reliability T13 Server Hardening)

- Added a top-level server hardening wrapper that applies `http.MaxBytesReader` to request bodies and `http.TimeoutHandler` to non-streaming requests.
- Streaming-style routes whose final path segment is `events`, `stream`, or `wait` bypass the timeout wrapper so SSE and blocking wait endpoints keep their own `r.Context().Done()` behavior.
- `POST /v1/runs` now maps `http.MaxBytesError` to `413 request_too_large` instead of reporting malformed JSON after the body limit is exceeded.
- `buildHTTPRuntime` now constructs the daemon `http.Server` with `ReadTimeout: 60s`, `ReadHeaderTimeout: 10s`, `IdleTimeout: 120s`, and `MaxHeaderBytes: 1 MiB`.
- Added focused coverage for oversized request-body reads, non-streaming timeout behavior, streaming timeout bypass, and daemon server settings.
- Validation:
  - `go test ./internal/server ./cmd/harnessd -run 'Test(PostRunRejectsOversizedBodyWithoutReadingAll|HardenedHandlerTimesOutNonStreamingRequests|HardenedHandlerDoesNotTimeoutSSERequests)|TestBuildHTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer' -count=1`
  - `go test ./internal/server ./cmd/harnessd -count=1`
  - `go test ./internal/server ./cmd/harnessd -race -count=1`

## 2026-06-26 (Reliability T14 Replay Drift Gate)

- Added a small semaphore around `detect_drift:true` replay simulation so drift detection returns `503 replay_busy` instead of constructing additional throwaway replay runners when capacity is saturated.
- Added `ReplayDriftConcurrency` to `ServerOptions`; values <= 0 use the default of 2 concurrent drift detections.
- Added a drift-runner factory seam so tests can prove a saturated gate fails before `runDriftDetection` constructs a replay runner.
- Validation:
  - `go test ./internal/server -run TestHandleRunReplay_DetectDriftReturns503WhenSemaphoreFull -count=1`
  - `go test ./internal/server -run 'TestHandleRunReplay|TestReplaySimulate' -count=1`
  - `go test ./internal/server -count=1`
  - `go test ./internal/server -race -count=1`

## 2026-06-26 (Reliability T15 Registry Hot-Swap Safety)

- Added per-tool in-flight tracking in `Registry.Execute`; hot reloads now wait for old matching handlers to return before replacing tools with the same source tag.
- MCP tools registered via `RegisterMCPTools` now carry an `mcp_server:<name>` tag and retained server metadata.
- `ReplaceByTag` rebuilds `mcpServerTools` from surviving and replacement tools after the swap, so `UnregisterMCPServer` removes the current MCP-owned tools instead of stale names.
- Added regressions for MCP ownership rebuild after replacement and waiting for an in-flight handler before hot-swap completion.
- Validation:
  - `go test ./internal/harness -run 'TestRegistry_ReplaceByTag(RebuildsMCPServerTools|WaitsForInFlightExecution)' -count=1`
  - `go test ./internal/harness -count=1`
  - `go test ./internal/harness -race -count=1`

## 2026-06-27 (TUI Daily Loop, Workflow Recaps, Self-Improvement Command)

- Replaced TUI run-control guidance-only commands with HTTP-backed `/runs`, `/cancel`, `/replay`, and `/resume` actions. `/resume` expands `@path` attachments and emits `RunStartedMsg` so the existing SSE/session path continues the run.
- Added TUI run-list snapshots at 80x24, 120x40, and 200x50, plus focused tests for `/model` issue coverage, command routing, and run-control endpoint behavior.
- Added deterministic workflow recaps to terminal run state and durable run storage. Recaps include goal, changed files, tests run, failure cause, fix pattern, useful commands, and a next continuation prompt.
- Extended `harnesscli search`/`go-code search` to match recap content and `show` to print recap details when present.
- Added `harnesscli improve` and `go-code improve`, exposing the existing autoresearch loop as a first-class command with `--dry-run` planning and `--score-only` repo-native checks.
- Validation:
  - `go test ./cmd/harnesscli/tui ./cmd/harnesscli/tui/components/modelswitcher -run 'TestRunControl_|TestTUI_DailyHarnessCommandsSetGuidance|TestTUI041_BuiltinCommandsRegistered|TestTUI364_RegistryCompleteness|TestTUI573_|TestIssue57|TestModelSearch' -count=1`
  - `go test ./internal/store ./internal/harness ./cmd/harnesscli -run 'TestMemoryStore/UpdateRun_PersistsWorkflowRecap|TestSQLiteStore/UpdateRun_PersistsWorkflowRecap|TestRunnerStore_CompletedRunPersistsWorkflowRecap|TestRunSearch_(FiltersRunMetadata|MatchesWorkflowRecap)' -count=1`
  - `go test ./cmd/harnesscli -run 'TestGoCodeScriptRoutesDailyCommands|TestRunImproveDryRunPrintsSelfImprovementPlan|TestDispatchRoutesImprove' -count=1`

## 2026-06-28 (Go Relay PR #689 Review Repair)

- Resolved PR #689 merge conflicts against current `origin/main` while preserving the Go Relay server option and routes plus main's server-hardening fields.
- Fixed Relay worker HTTP tenant isolation: list/register now derive tenant scope from the authenticated API key, and get/update/delete/heartbeat hide cross-tenant workers as `404`.
- Made placement routing enforce required capability inventory, repo URL, browser, Docker, secret, memory, MCP, tool, and output-surface constraints before scoring workers.
- Wired `HARNESS_RELAY_DB` through `harnessd` persistence/bootstrap/runtime so the daemon can enable `/v1/relay/workers` with a real SQLite worker store.
- Fixed operator run-summary capability redaction by sanitizing with the selected worker's actual location type.
- Validation:
  - `go test ./internal/server -run 'TestRelayWorkersUseAuthenticatedTenant' -count=1` failed before implementation and passes after the tenant fix.
  - `go test ./internal/relay -run 'TestPlacementRequiresCapabilityInventory|TestPlacementRejectsCapabilityRequirementsWithoutCapabilityStore|TestOperatorRunSummaryRedactsNonLocalCapabilityPack' -count=1` failed before implementation and passes after the routing/redaction fixes.
  - `go test ./cmd/harnessd -run 'TestBuild(ServerOptionsForwardsBootstrapRuntime|PersistenceBootstrapInitializesStoresAndCleaner|HTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer)' -count=1` failed before implementation and passes after the runtime wiring fix.
  - `go test ./internal/relay -count=1`
  - `go test ./internal/server -count=1`
  - `go test ./cmd/harnessd -count=1`

## 2026-07-18 (Issue #787 Hybrid Compaction Orphan Tool Messages)

- Symptom: after `compact_history` in `hybrid` mode dropped a large tool result but kept a small one from the same assistant turn, the resulting transcript had `tool` messages with a `tool_call_id` whose parent assistant message carried no `tool_calls` — rejected by OpenAI/Anthropic with a 400 on the next request.
- Cause: `compactHybrid` (both duplicated copies: `internal/harness/tools/compact_history.go`, `internal/harness/tools/core/compact_history.go`) rebuilt an `assistant_tool` turn's assistant message with only `Index/Role/Content`, dropping `ToolCalls`, while keeping small tool results verbatim. Both existing test suites used fixtures without `ToolCalls`, so they encoded the bug.
- Fix: partition each turn's tool results into kept (<=500 estimated tokens) and removed (>500); rebuild the assistant message with `ToolCalls` filtered to exactly the ids whose results survived, emitting it when it has non-empty trimmed content or at least one surviving tool call, followed by the kept results. Orphan tool turns (no assistant parent) fold kept results into the removed set instead of emitting unpairable tool messages. Applied identically in both copies (verified logic-identical modulo `tools.` package prefixes); a later tier dedups these files.
- Regression tests: `TestCompactHistoryTool_HybridModePreservesToolCallPairing` (`internal/harness/tools/compact_history_test.go`) and `TestCompactHistoryTool_Core_HybridModePreservesToolCallPairing` (`internal/harness/tools/core/compact_history_test.go`), enforcing the two-way pairing invariant (every assistant `tool_calls` id has a following tool result; every tool result id appears in a preceding assistant `tool_calls`).
- Validation:
  - Red phase: `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'HybridModePreservesToolCallPairing' -count=1` failed pre-fix (`parent assistant tool_calls ids exactly [call_small], got []`; `orphan tool result: tool_call_id "call_small" has no preceding assistant tool_calls entry`).
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'HybridModePreservesToolCallPairing' -count=1` (green)
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'Compact|ParseTurns|FindCompactionBounds|EstimateTextTokens|EstimateTranscriptTokens|TranscriptMsgsToMaps' -count=1` (all pre-existing compact tests stay green; no-ToolCalls fixtures produce identical output)

## 2026-07-18 (Issue #786 Bash Timeout/Kill Orphans Grandchildren)

- Symptom: `bash -lc 'sleep 300 &'` (or any command that backgrounds a child) with a 30s timeout returned only after ~300s, and `job_kill` left the backgrounded grandchildren running.
- Cause: all three spawn sites (`runForeground`/`runBackground` in `internal/harness/tools/bash_manager.go`, `runCommandOnce` in `internal/harness/tools/common_exec.go`) used `exec.CommandContext` with no `SysProcAttr.Setpgid` and no `WaitDelay`, so on timeout/`job_kill` Go SIGKILLed only the direct `bash` child; grandchildren survived and held the stdout/stderr pipes open, so `cmd.Wait()` blocked until they exited.
- Fix: new `configureGroupKill` (`exec_group_unix.go`, `//go:build unix`): `Setpgid` + a `Cancel` override that SIGKILLs the whole process group (ESRCH tolerated) + `WaitDelay = 2s`, matching the proven pattern in `tools/script/loader.go`; `exec_group_other.go` keeps non-unix behavior unchanged. Wired into all three spawn sites. `kill()` needed no change — `job.cancel()` routes through the overridden `Cancel`. Contract preservation: in all three exit-code branches, an error wrapping `exec.ErrWaitDelay` with an exited `ProcessState` recovers the real exit code, so a normally-exiting `bash -lc 'sleep 5 &'` still reports its exit code instead of -1.
- Regression tests (`internal/harness/tools/groupkill_unix_test.go`): `TestRunForegroundTimeoutKillsProcessGroup`, `TestJobKillKillsBackgroundJobGroup`, `TestRunCommandOnceTimeoutKillsProcessGroup` — assert prompt return after timeout/kill and poll `kill(pid, 0)` for ESRCH on the grandchild.
- Validation:
  - Red phase: pre-fix, `go test ./internal/harness/tools/ -run 'TestRunForegroundTimeoutKillsProcessGroup|TestJobKillKillsBackgroundJobGroup|TestRunCommandOnceTimeoutKillsProcessGroup' -count=1` failed — foreground and runCommandOnce each took ~10s instead of ~1s, and the job-kill grandchild was still alive after 3s.
  - `go test ./internal/harness/tools/ -run 'TestRunForegroundTimeoutKillsProcessGroup|TestJobKillKillsBackgroundJobGroup|TestRunCommandOnceTimeoutKillsProcessGroup' -count=1` (green, ~1s each)
  - `go test ./internal/harness/tools/... -count=1` (incl. `TestRunCommand_TimeoutReturnsNilError`, `TestRunCommand_ExternalSignalKillRetriesThenErrors`, streaming tests — all stay green)
  - `go test ./internal/harness/tools/ -race -count=1` (green)

## 2026-07-18 (Issue #785 Linux bwrap Sandbox Shared Host PID/IPC Namespaces)

- Symptom: on Linux, commands run under `SandboxScopeWorkspace`/`SandboxScopeLocal` (bubblewrap) could signal every same-UID host process and read host `/proc/<pid>/environ` (including API keys); darwin's seatbelt profile already restricts signals to self, so this was a cross-platform parity gap.
- Cause: `buildSandboxedCommand` in `internal/harness/tools/sandbox_linux.go` passed only `--unshare-net`; no `--unshare-pid`/`--unshare-ipc`/`--new-session`.
- Fix: insert `--unshare-pid`, `--unshare-ipc`, `--new-session` into the bwrap args right after `--unshare-net`, before the scope branch, so both Workspace and Local scopes get them. `--as-pid-1` intentionally not added (bwrap runs its own minimal PID 1 that reaps zombies); `--die-with-parent` unchanged.
- Regression tests (`internal/harness/tools/sandbox_linux_test.go`, `//go:build linux`): `TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC` (fake `bwrap` on PATH; asserts the argv for both scopes) and `TestSandboxLinuxPIDNamespaceHidesHostProcesses` (OS-level: host canary must be unsignalable and its `/proc/<pid>/environ` unreadable from inside the sandbox; skips when bwrap/user namespaces are unusable).
- Validation:
  - Runtime RED/GREEN requires Linux; this change was authored on macOS, so the linux-tagged files were verified with `GOOS=linux go build ./internal/harness/tools/` and `GOOS=linux go vet ./internal/harness/tools/` (both pass). Pre-fix, the argv assertions fail (flags absent) and the OS-level probe prints `CAN_SIGNAL_HOST`/`ENVIRON_READABLE`; run `go test ./internal/harness/tools/ -run 'TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC|TestSandboxLinuxPIDNamespaceHidesHostProcesses' -count=1 -v` on a Linux host with bwrap for the full red/green cycle.
  - `go test ./internal/harness/tools/... -count=1` (darwin host, green)

## 2026-07-18 (Issue #796 Coverage Gate Red on subagentRunnerHandoff Wrappers)

- Symptom: `./scripts/test-regression.sh` failed at its coverage gate on main: all 8 `subagentRunnerHandoff` methods in `cmd/harnessd/runtime_container.go` (`StartRun`, `GetRun`, `Subscribe`, `CancelRun`, `RunPrompt`, `RunPromptWithAllowedTools`, `SteerRun`, `ParentRunID`) reported 0.0% coverage.
- Cause: PR #795 introduced the handoff (an initialization-cycle breaker that forwards `subagents.RunEngine`/`htools.ConstrainedAgentRunner`/`htools.RunSteerer` calls to a `*harness.Runner` installed later via `setRunner`) without any unit test exercising the wrappers.
- Fix: new `cmd/harnessd/runtime_container_handoff_test.go` builds a real `*harness.Runner` over the exported scriptable `fakeprovider` (single content reply, `ExhaustRepeatLast`), wires it into `&subagentRunnerHandoff{}` via `setRunner` exactly like `buildHTTPRuntime`, and asserts delegation behavior for all 8 methods — not just calls for coverage points: `StartRun` registers the run on the underlying runner; `GetRun` returns the runner's record (ok=false for unknown IDs); `Subscribe` on a completed run replays non-empty history with a live channel and working cancel func (error for unknown IDs); `CancelRun` surfaces `ErrRunNotFound` for unknown runs and is a nil no-op on terminal runs; `RunPrompt`/`RunPromptWithAllowedTools` return the scripted provider content (nil and named-tool filters); `SteerRun` surfaces `ErrRunNotFound`/`ErrRunNotActive`/blank-message validation matching the runner; `ParentRunID` returns ("parent-1", true) for a run spawned with `ParentContextHandoff`, ("", false) for whitespace-only, missing handoff, and unknown runs. Waits poll `GetRun` with a 5s deadline (10ms interval); no sleeps beyond that.
- Validation:
  - `go test ./cmd/harnessd -count=1 -run 'TestSubagentRunnerHandoff' -v` (5 tests green)
  - `go test ./cmd/harnessd -count=1 -race -run 'TestSubagentRunnerHandoff'` (green)
  - `go test ./cmd/harnessd -coverprofile=/tmp/hd-cover.out -count=1 && go tool cover -func=/tmp/hd-cover.out | grep runtime_container.go` — all 8 wrappers (and `setRunner`) at 100.0%, package total 84.5%.

## 2026-07-18 (Issue #788 Recipe Steps Bypass Approval/Policy)

- Symptom: under `ApprovalModePermissions`/`ApprovalModeAll`, one approval of `run_recipe` silently expanded into N unapproved steps — a recipe whose `bash` step was denied by policy executed it anyway (observed: `touch <ws>/pwned` ran with `exit_code:0`).
- Cause: the recipe `HandlerMap` was built by copying raw `Handler` values BEFORE the `ApplyPolicy` wrap loops in both registration paths: `internal/harness/tools_default.go` (recipe block ahead of the wrap loops) and `internal/harness/tools/catalog.go` (`buildHandlerMap(tools)` before the `applyPolicy` loop). `recipe.Executor` then invoked the captured pre-policy handlers. `applyPolicy` reports a denial as marshaled JSON (`permission_denied`) with a nil Go error, so a denied step does not abort the recipe — the fix had to prevent execution, not just surface the denial.
- Fix: moved the recipe registration block after the policy wrap loops in both files so the handler map snapshots post-wrap handlers; wrapped the recipe tool itself individually (`ApplyPolicy(recipeTool.Definition, ..., recipeTool.Handler)` before appending — same pattern as `connect_mcp`/`find_tool`). Side effect: recipe-addressable membership expands to tools registered after the old block position (script/workflow/deploy/deep-git/subagent/goals) — additive only, and all are policy-wrapped.
- Regression tests: `TestRunRecipeTool_PolicyAppliesToSteps` + `TestRunRecipeTool_PolicyAllowsSteps` (`internal/harness/tools/recipe_tool_test.go`; deny-bash policy, allow-all control, direct-bash sanity assertion proving the machinery) and `TestDefaultRegistry_RecipeStepsRespectPolicy` + `TestDefaultRegistry_RecipeStepsAllowedByPolicy` (`internal/harness/tools_default_test.go`; same shape via `NewDefaultRegistryWithOptions`).
- Docs: `internal/harness/tools/descriptions/run_recipe.md` now states each recipe step is subject to the same approval-mode and policy checks as a direct tool invocation, and that a denied step does not execute.
- Validation:
  - Red phase: pre-fix, both deny-policy tests failed — recipe output lacked `permission_denied` (step output showed `exit_code:0`) and the `pwned` marker file existed on disk.
  - `go test ./internal/harness/tools/ -run 'TestRunRecipeTool' -count=1` (green)
  - `go test ./internal/harness/ -run 'TestDefaultRegistry' -count=1` (green)
  - `go test ./internal/harness/... -count=1` (green)

## 2026-07-18 (Issue #789 Git Option Injection via Unvalidated Refs)

- Symptom: user-controlled revision arguments were appended bare to git argv ahead of `--`, so git parsed values like `--output=/abs/path` as options — an arbitrary file write from read-classified tools (`git_diff`, `git_blame_context`, `git_diff_range`). Verified empirically: `git diff --output=<p>` creates `<p>` even in a non-repository directory (exit 129), `git blame --porcelain --output=<p> -- f` creates `<p>`, and `git diff --stat "--output=<p>..HEAD"` creates `<p>..HEAD`.
- Cause: no validation at the four ref-to-argv sites: `internal/harness/tools/git_diff.go` (`args.Target`), `internal/harness/tools/core/git.go` (`args.Target`), `internal/harness/tools/deferred/git_deep.go` (`args.Rev` in blame; `args.From`+`args.To` glued into `from..to` in diff_range). `runCommand` returns a nil Go error for non-zero exits, so the injection surfaced as a normal tool result.
- Fix: new `internal/harness/tools/git_refs.go` exporting `ValidateGitRef` — rejects any ref beginning with `-` (legitimate refs: branches, tags, SHAs, `HEAD~2`, `a..b`/`a...b` ranges never do; git refnames cannot either). Applied after default assignment and before argv append at all four sites (`tools.` prefix in core/deferred; no import cycle since both already import package `tools`). The glued `--since=`/`--grep=` and `-S <query>` sites were left alone (option name is fixed, value position is safe). Rejected alternatives: `git check-ref-format`/`rev-parse --verify` (one exec per call, rejects unresolvable-but-valid refs, no range support) and `--end-of-options` (git >= 2.20, repo pins no minimum, delicate argv placement).
- Regression tests: `TestValidateGitRef_RejectsOptionLikeRefs`/`_AcceptsLegitRefs` (table-driven, `internal/harness/tools/git_refs_test.go`); `TestGitDiffTool_RejectsOptionLikeTarget` in both `internal/harness/tools/git_diff_test.go` and `internal/harness/tools/core/git_test.go` (error contains `must not begin with '-'`, injected file not created; no repo needed — validation precedes exec); `TestGitDiffTool_AcceptsLegitTargets` (`HEAD~1`, branch, `<sha1>..<sha2>` over a 2-commit repo); `TestGitBlameContextTool_RejectsOptionLikeRev`, `TestGitDiffRangeTool_RejectsOptionLikeFrom`, `TestGitDiffRangeTool_RejectsOptionLikeTo`, `TestGitBlameContextTool_AcceptsLegitRev`, `TestGitDiffRangeTool_AcceptsSHARange` (`internal/harness/tools/deferred/git_deep_test.go`).
- Docs: constraint clause added to the relevant args in `descriptions/git_diff.md` (target), `descriptions/git_blame_context.md` (rev), `descriptions/git_diff_range.md` (from/to); line-1 directive phrasing preserved (`TestToolDescriptionsContainBehavioralDirectives` stays green).
- Validation:
  - Red phase: pre-fix, all five reject tests failed with `expected error for option-like ..., got nil` and the injected marker files were created on disk (unit test failed to compile: `undefined: ValidateGitRef`).
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ ./internal/harness/tools/deferred/ -run 'ValidateGitRef|OptionLike|LegitTargets|LegitRev|SHARange' -count=1` (green)
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ ./internal/harness/tools/deferred/ ./internal/harness/tools/descriptions/ -count=1` (green)

## 2026-07-18 (Issue #790 Deploy workspace Arg Accepts Any Absolute Path)

- Symptom: the `deploy` tool's `workspace` argument overrode the workspace root with any raw absolute path; `railway up`/`fly deploy` then package and upload that directory — arbitrary host-directory exfiltration under the default FullAuto approval mode. The pre-existing `TestDeployTool_WorkspaceOverride` blessed the behavior (detect against a directory outside the workspace succeeded).
- Cause: `internal/harness/tools/deferred/deploy.go` set `wsDir := args.Workspace` verbatim. `DeployTool` receives no sandbox scope, and the empty default scope makes `ConfineWorkspacePath` a no-op, so the confinement had to be unconditional.
- Fix: replaced the raw override with `tools.ResolveWorkspacePath(workspaceRoot, args.Workspace)` followed by `tools.ConfineWorkspacePath(tools.SandboxScopeWorkspace, workspaceRoot, nil, abs)`, placed before the `detect` branch so all four actions (deploy/status/logs/detect) are covered. Deliberate behavior change: relative `workspace` values now resolve against the workspace root (previously they were used raw, i.e. relative to the process CWD). Absolute paths outside the root fail with `deploy workspace: sandbox violation: path ... escapes the allowed workspace root ...`; `../` traversal fails inside `ResolveWorkspacePath` with `... escapes workspace`.
- Regression tests (`internal/harness/tools/deferred/deploy_test.go`, replacing `TestDeployTool_WorkspaceOverride`): `TestDeployTool_WorkspaceOverride_OutsideRejected` (absolute path outside root rejected with `escapes the allowed workspace root`), `TestDeployTool_WorkspaceOverride_InsideAllowed` (absolute subdir with `railway.json` detects `railway`), `TestDeployTool_WorkspaceOverride_RelativeInsideAllowed` (`workspace: "app"` resolves against root), `TestDeployTool_WorkspaceOverride_TraversalRejected` (`../sibling` rejected).
- Docs: `descriptions/deploy.md` and the JSON-schema description of the `workspace` property now state the path is relative to the workspace root (absolute paths must lie inside it), defaults to the workspace root, and outside paths are rejected.
- Validation:
  - Red phase (verified by stashing the fix and re-running the final tests): `OutsideRejected` failed with `expected error for workspace outside the workspace root, got nil`; `RelativeInsideAllowed` failed with `detect platform: no platform config found in app` (relative used raw); `TraversalRejected` failed with error text `no platform config found in ../sibling` instead of `escapes workspace`.
  - `go test ./internal/harness/tools/deferred/ -run 'TestDeployTool_WorkspaceOverride' -count=1` (green)
  - `go test ./internal/harness/tools/deferred/ ./internal/harness/tools/descriptions/ -count=1` (green)
# 2026-07-19 — Installable plugin bundles (Epic #748)

- Added validated, versioned installable bundles with explicit enabled versus trusted lifecycle state, CLI/TUI management, marketplace indexes, and runtime reuse of the existing skills, profiles, MCP, and hooks paths.
- Remote installs default untrusted; hook and MCP execution are unreachable until explicit trust.

## 2026-07-20 (Issue #846 Subscription-Auth Foundation)

- Added the internal `provider.TokenSource` contract and `StaticToken` adapter, keeping static-key client construction compatible.
- Extended the OpenAI-compatible client with request-time bearer lookup and copied static extra headers at both chat-completions and responses request sites. Authorization is applied after extra headers so an extra-header map cannot override it; errors identify only the credential operation, never its value.
- Added `internal/provider/tokencache`: a provider-neutral mutex-single-flighted refresh cache. It reuses credentials outside a configurable expiry margin; if a refresh within that margin fails while the current credential remains valid, it returns the still-valid cache entry. Refresh transport, OAuth details, and persistence deliberately remain follow-on-provider responsibilities.
- Added registry `SetTokenSource`: token sources satisfy configuration, evict cached clients on replacement, and reach the typed four-argument `catalog.ClientFactory`. `SetClientFactory` continues accepting existing three-argument static factories as a source-compatible bridge.
- TDD validation: provider token-source, OpenAI dynamic-auth/header/static-header regression, token-cache concurrency/failure-policy, and registry propagation/eviction tests were all red before their implementations and green afterward. `go test ./internal/provider/... ./internal/harness/...` passed.

## 2026-07-20 (Epic #849 Live Model Discovery)

- Generalized the catalog's OpenRouter-only cache into provider-agnostic live model discovery.
- OpenRouter, OpenAI, Anthropic, and DeepSeek now have five-minute cached listings when configured; failures retain stale cached results when present and otherwise leave the static catalog untouched.
- Live listings add models while curated catalog metadata remains authoritative on matching IDs.
## 2026-07-20 (Issue #848 Kimi Code Subscription Authentication)

- Added a separate `kimi-subscription` provider that derives its model list from the existing metered `kimi` entry, preserving the metered path unchanged.
- `harnesscli auth kimi login` reads the vendor credential only and stores a `0600` go-code-owned copy; status and logout never print a credential and logout never affects the vendor CLI.
- Refresh uses a 30-second margin for the real 900-second TTL. Fake OAuth/API integration coverage proves a forced near-expiry refresh, rotated persistence, dynamic bearer authorization, and all `X-Kimi-Client-*` headers.
- Live endpoint caveat: a single unauthenticated `OPTIONS https://auth.kimi.com/api/oauth/token` returned `405 Allow: POST`; no authenticated live refresh or completion was performed. The form/body and OpenAI-compatible wire contract are convention-based and must be manually verified.

## 2026-07-20 — Codex ChatGPT-Subscription Authentication (Epic #847)

- Added `internal/provider/codex`: read-only vendor credential import, a `0600` harness-owned credential store, JWT expiry parsing, OAuth refresh, and a `tokencache`-backed token source that persists refreshes only to `~/.harness/subscription-auth/codex.json`.
- Added `codex-subscription` as a structurally mirrored `openai` catalog provider. A token-source-required catalog flag distinguishes this remote subscription route from anonymous local optional-key providers, so absence remains unconfigured and never probes the ChatGPT backend.
- Existing OpenAI-compatible request code now supports the Codex backend's no-`/v1` endpoint path and applies `chatgpt-account-id` with the dynamic bearer credential. `HARNESS_PROVIDER=codex-subscription` selects it deterministically when imported credentials are present.
- Added `harnesscli auth codex login|status|logout`; `/keys` renders the read-only ChatGPT subscription connection state rather than offering API-key entry.
- Coverage includes OAuth request/error sanitization, import permissions/read-only behavior, catalog mirroring, bootstrap wiring, CLI lifecycle, TUI/server status, fake HTTPS request plus forced mid-session expiry refresh, and a grep-based no-token-logging guard.

## 2026-07-19 (Epic #815 Slice 1 Config Reload Field Classification)

- Change: new `internal/config/reload.go` — the single authoritative classification of every `Config` field as hot-swappable (takes effect on live reload for subsequent runs) or restart-only (wired once at startup, reported but never applied), plus the pure `ReloadDiff(old, new Config) ReloadReport` function later slices (runner swap, `POST /v1/config/reload`, SIGHUP, TUI `/reload`) build on.
- Classification rationale (grounded in `cmd/harnessd/main.go` consumption): restart-only is exactly `addr` (listen socket bound once), `memory.db_driver`/`memory.db_dsn`/`memory.sqlite_path` (persistence handles opened once), and `mcp_servers` (server processes and tool registry wired once). Everything else — model, max_steps, cost ceiling, memory toggles/thresholds/LLM knobs, auto_compact, forensics, conclusion_watcher, hooks, cron timing — flows into per-run `RunnerConfig` or runtime policy and is hot-swappable.
- Design: table-driven (`reloadFields` slice with path/class/equality probe per field) so report order is deterministic; `ReloadClassification()` exposes a copy for docs/validation; `ReloadReport` carries `Applied` + `RestartRequired` with `Changed()`/`NeedsRestart()` helpers. No behavior change to `Load`, `Defaults`, or `Resolve`.
- Tests (TDD, written first and verified red as compile errors): model-only change hot-swappable; `addr` restart-only; memory split (`db_driver` restart-only vs `enabled` swappable); identical configs empty report; `mcp_servers` map change restart-only; slice field (`hooks.dirs`) detection; mixed-change determinism; reflection-based exhaustiveness guard failing any future `Config` field added without classification.
- Validation: `go test ./internal/config/... -count=1` (green, 9 new tests); `gofmt`/`go vet` clean; every `reload.go` function at 100% statement coverage.
- Learning: the regression coverage gate rejects any zero-coverage function repo-wide — the first `test-regression.sh` run failed solely on the untested `ReloadClass.String()` helper. Fixed by adding `TestReloadClassString` (including the defensive unknown-class branch) before re-running; the gate failure was self-inflicted, not a baseline issue.

## 2026-07-19 (Epic #810 Slice 1: Theme Token Schema and JSON Loader)

- Change: new `cmd/harnesscli/tui/themes.go` adds a 17-token color schema (`TokenSet`, camelCase JSON keys aligned with kimi-code: primary, accent, text, textStrong, textDim, textMuted, border, borderFocus, success, warning, error, diffAdd, diffRemove, diffHunk, roleUser, shellMode, codeBackground) plus `LoadTheme(dir, name)`, `ListThemes(dir)`, `DefaultThemesDir()` (`~/.config/harnesscli/themes`), and built-in base themes `default-dark`/`default-light`.
- Design: token values are a string (`#rgb`/`#rrggbb` hex or ANSI-256 number, applied to both backgrounds) or an adaptive `{"light","dark"}` object. Resolution overlays each explicitly-set, valid token onto a copy of `DefaultTheme()` — omitted, empty, or unparseable values fall back per token and per side to the base palette (`tokenBaseColors`, pinned to theme.go's current colors by `TestThemesLoad_BasePaletteDerivation`). `theme.go` itself is untouched, so default rendering is byte-identical when no theme file exists; the full `go test ./cmd/harnesscli/tui/...` suite (including golden snapshots) passes unchanged.
- Fallback semantics: missing theme file or built-in name returns `DefaultTheme()` with no error; malformed JSON returns the base palette plus an error (callers keep the current/default theme and can surface the message); invalid token shapes (numbers, arrays) and invalid colors (`"not-a-color"`, 5-digit hex, ANSI > 255) fall back without failing the load. Unsafe names (`../x`, separators, empty) are rejected with an error.
- Mapping: every one of the 24 `Theme` style fields is bound to exactly one token (`applyToken`); `borderFocus`/`shellMode` are parsed and resolved but intentionally unbound — reserved for component-level styling in slice 2. `TestThemesLoad_TokenMappingCoversEveryThemeField` reflects over the struct so adding a `Theme` field without a binding fails the test.
- Validation: strict TDD — 16 behavior tests written first in `cmd/harnesscli/tui/themes_test.go` (red: compile error, undefined `tui.LoadTheme` et al.), then implementation to green. `go test ./cmd/harnesscli/tui/... -count=1` green (25 packages); `gofmt`/`go vet` clean.
- Deferred (later slices of #810, not this commit): threading resolved themes through components (slice 2), `/theme` picker (slice 3), config persistence (slice 4), website docs + example theme file (slice 5).

## 2026-07-19 (Epic #815 Slice 2 Runner ApplyConfig)

- Change: `Runner` can now swap its `RunnerConfig` at runtime via `ApplyConfig(RunnerConfig)` (`internal/harness/runner.go`). Runs started after the swap observe the new config (model, max steps, auto-compact, forensics knobs); in-flight runs keep the snapshot captured at their creation and are completely undisturbed. `NewRunner` signature and behavior unchanged.
- Design: `config` is guarded by a new leaf-level `configMu` (never held while acquiring `r.mu`; `ApplyConfig`/`snapshotConfig` touch no other lock). `runState` gains an immutable `*RunnerConfig` captured in `StartRun`/`ContinueRunWithOptions` (a continuation is a new run and gets a fresh snapshot); nil only for test-constructed runStates, where `configForRun` falls back to the runner's current config — preserving pre-change behavior for the ~19 direct `runState` literals in tests. Every one of the ~198 `r.config.X` read sites across `runner.go`, `runner_step_engine.go`, `runner_event_journal.go`, `plan_mode.go`, `permission_rules.go` now reads a per-function snapshot (`rc := r.configForRun(runID)` for run-scoped code, `rc := r.snapshotConfig()` otherwise); grep verifies zero unsynchronized reads remain. `NewRunner`'s zero-value defaulting was factored into `normalizeRunnerConfig` shared with `ApplyConfig`. Worker-pool sizing stays construction-time only.
- Boundary semantics (documented): snapshot isolation covers everything from run creation onward; a `StartRun` that overlaps an `ApplyConfig` call is itself "starting", so either side of the swap is legitimate for it. Mid-run per-step reads (auto-compact check in the step engine, hook application, emit/redaction path, error-chain, forensics flags) all come from the run's snapshot.
- Bug found and fixed during the slice: the auto-compact/manual-`CompactRun` summarizer resolved its model from the live config mid-run (`summarizeMessagesWithModelAndInstruction`). Added `summarizeWithConfig` + `runnerMessageSummarizer.rc` so run-scoped compaction (`autoCompactMessages`, `CompactRun`) resolves the summarizer model from the run's snapshot; the dead `newMessageSummarizerWithInstruction` constructor was removed (replaced by `newMessageSummarizerWithConfig`). Exported `SummarizeMessages`/`SummarizeMessagesWithModel` keep live-config resolution (no run context).
- Locking subtlety: `eventJournal.prepareLocked` runs under `r.mu`, so it reads `state.config` directly (falling back to `snapshotConfig`) instead of calling `configForRun` (which RLocks `r.mu` and would self-deadlock).
- Tests (TDD, red first as compile errors): `internal/harness/runner_apply_config_test.go` — new runs observe applied model (`CompletionRequest.Model` recorded by a gating provider) and applied `MaxSteps` (run dies at the new limit, provider call count == 2); in-flight isolation (config applied while a run is blocked in its first LLM call: no `auto_compact.started` for the in-flight run on its post-apply steps, original model used for all its calls; a post-apply run compacts and uses the new model); `ApplyConfig` normalization matches `NewRunner` defaults; concurrent apply+start hammer for `-race`.
- Validation: `go test ./internal/harness/ -run TestApplyConfig -count=1 -v` (5/5 green); `go test -race ./internal/harness/... -count=1` green (7 packages); `gofmt`/`go vet` clean; `GOCACHE=/tmp/go-build ./scripts/test-regression.sh` PASS (`coveragegate: PASS (total=84.0%, min=80.0%, zero-functions=0)`).

## 2026-07-19 (Epic #810 Slice 2: Thread Resolved Theme Through Components)

- Change: the resolved `Theme` now actually flows into the five component paths named by the epic. Each component gained a `Styles` struct + `DefaultStyles()` + optional injection (`statusbar.SetStyles`, `diffview.View.Styles`/`Model.Styles`, `spinner.WithStyles` (immutable), `messagebubble` `Styles` on `Model`/`UserBubble`/`AssistantBubble`, `tooluse.DiffStyles` pass-through to its embedded diffview). New `cmd/harnesscli/tui/theme_components.go` derives component styles from a `Theme`; `Model.SetTheme` stores the theme and re-distributes via `applyThemeToComponents` (foundation for slice-3 hot reload), called from `New`, after `WindowSizeMsg` statusbar re-creation, and after spinner re-creation on `RunStartedMsg`. Ephemeral components are styled at the two render funnels: `renderMessageBubble` and `appendToolUseView` (single injection point covers all four tool-card construction sites). The approval overlay (`approval.go`), previously unstyled, now renders chrome in the border-token color, the tool name in primary, and the action line in warning — the one deliberate default-appearance change in this slice.
- Zero-drift mappings (verified by `TestDefaultTheme_ComponentsMatchLegacyRendering` and unchanged component suites): statusbar Dim/Bold/Warn ← DimStyle/BoldStyle/WarningStyle (#FFAF00 == legacy); diffview Add/Remove/Hunk ← diff* styles, dashed border ← DimStyle (consistent with slice 1's SeparatorStyle ← textDim — the rule is a separator, not a box border); spinner ← DimStyle (faint); messagebubble keeps bg 237/fg 252/dot 15 defaults and takes colors only from tokens unset by default (roleUser → user fg, accent → assistant dot, textStrong → title fg), so default rendering is byte-identical.
- Validation: strict TDD — component tests (`styles_theme_test.go` in statusbar/diffview/messagebubble/spinner) and model-level `theme_redistribution_test.go` written first (red: undefined `Styles`/`SetStyles`/`WithStyles`/`SetTheme`), then implementation to green. Color assertions force `termenv.TrueColor` with save/restore (component tests previously only stripped ANSI; default renderer emits no color in non-TTY test runs). One test-expectation fix during green phase: `#E05252` renders as rgb(224,81,81) through termenv, not the hand-computed 224,82,82 — pinned actual output. One test-harness fix: `appendToolUseView` requires `ensureToolStateMaps()` first (nil-map panic in the test, not in product code paths).
- Deferred: `/theme` picker (slice 3), persistence (slice 4), docs + example theme (slice 5); tooluse collapsed/expanded chrome, plan-approval overlay, inputarea, and model.go overlay styles (colors 220/62 etc.) remain hardcoded — outside the epic's named component set.

## 2026-07-19 (Epic #810 Slice 3: /theme Picker With Live Apply and Directory Re-scan)

- Change: new `cmd/harnesscli/tui/components/themepicker/` (modeled on `profilepicker`: value-semantics `New(entries).Open()` state machine, `ThemeSelectedMsg`, rounded-border view with built-in tags and an `(active)` marker). `/theme` registered in `cmd_parser.go` beside `/profiles`; `executeThemeCommand` re-scans `ListThemes(dir)` and rebuilds the picker on every open (sessions-picker pattern), so theme files dropped into `~/.config/harnesscli/themes/` while the TUI runs appear without restart. Model gains `themeName` (default `"default-dark"`), a `themesDir` test seam (empty = `DefaultThemesDir()`), and the `"theme"` overlay kind wired at the same four sites as `"profiles"` (esc close, Enter route, catch-all key route, render switch). Selection resolves via the slice-1 `LoadTheme` and applies live via slice-2 `SetTheme`; loader failure keeps the current theme and sets an error status message (`Theme load failed: ... — keeping <name>`). Persistence deliberately excluded (slice 4).
- Validation: strict TDD — themepicker component tests (navigation wrap, Enter emits selection, Esc closes, SetEntries resets, view lists names/tags/active marker, empty state) and model-level `theme_picker_test.go` (registration, open lists built-ins + files, re-scan picks up a file added after first open, Enter+ThemeSelectedMsg applies live with statusbar restyle proof, malformed theme keeps current + error status, Esc closes without changes) written first (red: undefined `ThemeEntry`/`executeThemeCommand`/`themesDir`), then implementation to green.
- Deferred: config persistence of the selection (slice 4), website docs + example theme (slice 5).

## 2026-07-19 (Epic #815 Slice 3 POST /v1/config/reload)

- Change: `POST /v1/config/reload` (admin scope, same as `PUT /v1/providers/{name}/key`) triggers a live daemon config reload. New `internal/server/http_config.go` (`ConfigReloadFunc`, handler: 501 unwired / 400 with the load error on invalid config / 200 `{applied, restart_required}`), wired in `buildMux` and `ServerOptions.ConfigReload`. `cmd/harnessd/config_reload.go` adds `configReloader`: mutex-serialized load → `config.ReloadDiff` against last-known-good → full `RunnerConfig` reassembly → `Runner.ApplyConfig`; invalid TOML leaves the previous config active.
- Key design decision: reload must reproduce the FULL startup runner-config assembly, not bare `buildRunnerConfig` — `ApplyConfig` replaces hook slices wholesale, so a bare rebuild would silently wipe config-driven hooks, trusted plugin hooks, and the conclusion watcher on every reload. Extracted `assembleRunnerConfig` (buildRunnerConfig + ProfileRunStore + S3 uploader + conclusion watcher + config-driven hooks + trusted plugin hooks) and `loadHarnessConfig` (Load → Resolve → applyProfileDefaults → MaxSteps 0→8 daemon default) — both now shared by startup and reload so the two paths cannot drift. Existing `cmd/harnessd` tests pass unchanged (characterization for the extraction).
- Wiring: `httpRuntimeOptions.configReloader` → `buildHTTPRuntime` binds the created runner (`subagentRunnerHandoff` precedent) → `serverBootstrapOptions` → `ServerOptions.ConfigReload` via `configReloadFunc` adapter. Also added exported `Runner.Config()` (public read counterpart of `ApplyConfig`).
- Scope: memory-manager-resident knobs (`memory.*` LLM/threshold values) live in the `om.Manager` built once at startup and are not rebuilt by this slice (documented limitation; they remain classified hot-swappable in the Slice 1 table for a future manager-rebuild slice). `GET /v1/hooks` keeps serving the startup-computed summary.
- Tests (TDD, red first as undefined `ConfigReload`/`configReloader` compile errors): `internal/server/http_config_test.go` (model edit in temp file → 200 + applied list + next run uses new model; invalid TOML → 400 with error text + last-known-good retained; `addr` edit → restart-only warning; unwired → 501; GET → 405); `auth_scope_test.go` extended (read_only/write → 403, admin → 200); `cmd/harnessd/config_reload_test.go` (apply, invalid-keeps-good, restart-only + advancing diff base, hook slices survive reload without wipe/duplication and reflect `hooks.enabled=false`, concurrent reloads serialize, `buildServerOptions` wiring both branches).
- Validation: `go test ./internal/server/... ./cmd/harnessd/... -count=1` green; `go test ./internal/harness/ -count=1` green; `go test -race ./cmd/harnessd/ -run TestReloader -count=1` green; `gofmt`/`go vet` clean.
