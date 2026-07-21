# UX Path Catalog — go-code Harness (CLI / TUI / API)

Generated: 2026-07-12
Scope: `cmd/harnesscli` (CLI + BubbleTea TUI) and `internal/server` (harnessd HTTP+SSE API), grounded against commit `0de7f60` plus the just-fixed working-tree changes.
Method: read-only static analysis by five parallel research passes (CLI, TUI core engine, TUI overlays, API run/conversation lifecycle, API secondary surfaces). Every row cites file:line; test citations accompany OK rows.

Robustness legend:
- **OK** — handled and has real test coverage (test cited).
- **RISKY** — handled but untested, or only partially handled (the gap is stated).
- **BROKEN** — a real gap: hangs, panics, silently drops, wrong error/status, unreachable feature, tenant/auth bypass, resource leak.
- **UNKNOWN** — behavior not determinable from static reading (reason stated).

Out of scope as "just fixed" (NOT re-reported): SSE keepalive aborting CLI stream; `run.waiting_for_user` hanging non-TUI CLI; CLI/main.go Authorization header; auth-login key discard; TUI resize wiping transcript; TUI ctrl+c not cancelling server-side; stale SSE across runs; cross-tenant on the specific run/conversation/relay by-ID endpoints already patched; rollout_path traversal; unbounded webhook bodies (memory); O(n^2) tool-output streaming. Where a sibling of a fixed endpoint was found STILL leaking, it is reported as a new finding.

---

# Consolidated Findings (ranked, worst first)

## BROKEN

| # | Finding | Evidence |
|---|---------|----------|
| 1 | `POST /v1/conversations/cleanup` bulk-deletes EVERY tenant's old unpinned conversations (no tenant predicate) | `internal/server/http_conversations.go:305-340`; SQL `internal/harness/conversation_store_sqlite.go:472-490` |
| 2 | Checkpoints have no tenant concept at all — any tenant can read/resume another tenant's paused workflow with attacker payload | `internal/server/http_checkpoints.go:59-102`; `internal/checkpoints/store.go:27-43` |
| 3 | `GET /v1/conversations/search` (and `/v1/conversations/?q=`) full-text-leaks every tenant's message content | `internal/server/http_conversations.go:156-186`; interface `internal/harness/conversation_store.go:64-66`; SQL `conversation_store_sqlite.go:647-675` |
| 4 | `GET/PUT /v1/runs/{id}/todos` — only run sub-route missing `authorizeRun`; cross-tenant todo read/overwrite | `internal/server/http_runs.go:271-286` (vs 11 guarded siblings at 328,357,385,406,450,473,515,559,633,663,684); `http_todos.go:12-57` |
| 5 | `POST /v1/relay/workers` trusts `tenant_id` from body — register a worker under any victim tenant | `internal/server/http_relay_workers.go:120-198` (esp. 123,143-145,168) |
| 6 | Subagents have no `TenantID` — any tenant can list/get/wait/cancel/delete another tenant's subagent + output | `internal/subagents/manager.go:63-77`; `internal/server/http_subagents.go:15-185` |
| 7 | `POST /v1/external/trigger` `tenant_id` is caller-controlled JSON; one shared per-source secret → act under any tenant | `internal/server/http_external_trigger.go:49-54,107-216`; `internal/trigger/types.go:14`; `validator.go:15-36` |
| 8 | Legacy workflows, script-workflows, networks, cron: no `TenantID`, no `checkTenantOwnership` — globally visible/mutable across tenants | `http_workflows.go`, `http_script_workflows.go`, `http_networks.go`, `http_cron.go:211`; types lack tenant field |
| 9 | TUI tool-approval flow does not exist — `tool.approval_required` SSE has no handler; no `/approve`/`/deny` call anywhere; run sits blocked forever | `cmd/harnesscli/tui/model.go:1998-2086` (no case); `api.go` (no approve/deny); server emits it `internal/harness/runner.go:63-80` |
| 10 | TUI total input lockup: `run.waiting_for_user` then empty `questions:[]` leaves `askUser.active=true` forever, swallowing ALL keys incl. Ctrl+C | `model.go:2073-2082,2148,1226-1233`; `askuser.go:165-168` |
| 11 | `cmd/harnesscli/askuser.go` GET/POST `/v1/runs/{id}/input` never send Authorization; ask-user 401s on any auth-enabled server | `askuser.go:54,111` (bypass `newAuthedRequest` at `main.go:552-564`); server scope-gated `http.go:189`, `auth_scope_test.go:334-362` |
| 12 | Non-TUI CLI cannot answer a `MultiSelect:true` ask-user question — field decoded, never branched; always reads one line | `cmd/harnesscli/askuser.go:36,82-105` |
| 13 | 8 fully-built TUI overlay components are dead code (never imported): permissionprompt, permissionspanel, planoverlay, configpanel, interruptui, outputmode, costdisplay, spinner | components verified via repo-wide grep; dispatch table `cmd/harnesscli/tui/model.go:2398-2438` |
| 14 | Session resume is cosmetic — `/sessions` select clears transcript and claims history is "on the server" but never fetches it; `fetchSessionRunsCmd`/`SessionRunsFetchedMsg` unwired | `model.go:2320-2336`; `api.go:363-393`; `messages.go:218-224` |
| 15 | CLI SSE: mid-JSON truncation on a clean early close is classed `errInvalidSSEData` (no retry) though the same drop on a block boundary correctly reconnects | `cmd/harnesscli/main.go:341-354,370-373` (vs retry path proven `regression_findings_test.go:266`) |
| 16 | CLI SSE scanner hard-caps one line at 4MB; an oversized single event recurs identically across all 4 reconnects → permanent opaque "token too long" | `cmd/harnesscli/main.go:310,337-339` |
| 17 | github/slack/linear webhook oversized body returns 400 not 413 (don't unwrap `*http.MaxBytesError` like external_trigger does) | `internal/github/adapter.go:55-58`, `slack/adapter.go:48-51`, `linear/adapter.go:34-37` vs `http_external_trigger.go:38-46` |
| 18 | diffview / streamrenderer / tooluse error+stream paths do NO ANSI-strip or UTF-8 validation — raw escapes/binary corrupt the terminal; wrap math desyncs | `components/diffview/formatter.go`,`view.go`; `streamrenderer/model.go`,`tokenizer.go`; `tooluse/errorview.go` |
| 19 | TUI tool timer/view left frozen "running…" forever after Ctrl+C/Esc mid-tool-call (abort clears run state but not `toolTimers`/`toolViews`/`activeToolCallID`) | `model.go:1339-1350` |
| 20 | statusbar narrow-width segment-priority drop is dead code — `MaxWidth` pre-truncates before the fit check, so blunt mid-segment cut happens instead of graceful drop | `components/statusbar/model.go:89-108` (`_ = plainLen` at 96,98) |
| 21 | statusbar `truncate()` off-by-two (`runes[:max-1]+"..."` = max+2); masked only by outer `MaxWidth`, ±2 test tolerance hides it | `components/statusbar/model.go:144-150` |
| 22 | contextGrid `TotalTokens` never set → always the hardcoded 200k window; usage % systematically wrong for any non-200k model | `components/contextgrid/model.go:10`; never assigned in `model.go` |
| 23 | Transcript export failure discards the real error and shows generic "Export failed"; purpose-built `StatusModel.SetError` never constructed | `model.go:980-983,1974-1979`; `components/transcriptexport/model.go:26-76` |
| 24 | `ctrl+h`/`?` (keys.Help) never matched in Update() — falls through to literal text insertion; help only via `/help` | `cmd/harnesscli/tui/keys.go:69-72`; grep `keys.Help` in model.go = 0 |
| 25 | No Home/End jump-to-top/bottom binding — no fast nav on long transcripts | `cmd/harnesscli/tui/keys.go` (only Up/Down + PgUp/PgDn) |
| 26 | Nested tool-call render recurses with no max-depth/cycle guard; duplicate CallID re-add can silently drop grandchildren | `components/tooluse/nested.go` (Add/attachChild/Flatten/RenderTree; removeNodeFromSlice:102-112) |
| 27 | `a11y.A11yHints` labels defined for no-color/non-TTY mode but never consumed by any render path | `cmd/harnesscli/tui/a11y.go:5-19` |
| 28 | diff "No newline at end of file" marker path unverified (zero tests construct it) | `components/diffview/formatter.go:111-117` |
| 29 | contextgrid "not available" fallback string is dead code (`View()` never returns "") | `model.go:2408-2410` vs `components/contextgrid/model.go:28-71` |
| 30 | "Plan Mode" (keys.PlanMode on ctrl+o) is unreachable scaffolding — same key as ExpandTool wins; PlanApproved/Rejected msgs have no case | `keys.go:24,81-84,89-92`; `model.go:1359` |

## RISKY (high-signal subset; full detail in surface sections)

| # | Finding | Evidence |
|---|---------|----------|
| R1 | No body-size cap on authenticated core endpoints (`/v1/runs`, `/continue`, `/steer`, `/compact`, conversation `/compact`) — unbounded in-memory decode | `internal/server/http_runs.go:45,414,482,525`; `http_conversations.go:240` |
| R2 | `resolveRolloutPath` never checks the rollout file's tenant — replay/fork another tenant's run if its UUID leaks | `internal/server/http_replay.go:67-80,125-129` |
| R3 | `parsePositiveInt` has no overflow guard; long `limit`/`offset` can wrap negative → SQLite treats negative LIMIT as unlimited | `internal/server/http.go:370-379` |
| R4 | `DELETE /v1/conversations/{id}` doesn't guard an in-flight run; runner upsert likely resurrects the "deleted" conversation | `http_conversations.go:395-410`; `internal/harness/runner.go:2324` |
| R5 | Export of an existing-but-empty conversation is indistinguishable from 404 | `http_conversations.go:206-209` |
| R6 | `PUT /v1/providers/{name}/key` (admin, secret-setting) has zero test coverage | `internal/server/http_catalog.go:85-118` |
| R7 | `http_sourcegraph.go` (`POST /v1/search/code`) appears to have no test coverage at all | `internal/server/http_sourcegraph.go:21-116` |
| R8 | Linear webhook validator has no timestamp/replay-freshness check (Slack has ±300s) — captured payload replayable | `internal/trigger/validator.go:110-122` |
| R9 | `/healthz` always returns `{"status":"ok"}` — no dependency checks; can't detect degraded backend (`CronClient.Health` never called) | `internal/server/http.go:350-352` |
| R10 | github/slack/linear/external-trigger webhooks have no delivery-ID/nonce dedup — replayed `start` starts a second run | `http_github_webhook.go`; `internal/github/adapter.go` |
| R11 | Script-workflow start/resume silently swallow malformed `args` JSON (`_ = ...Decode`) instead of 400 | `http_script_workflows.go:89-98,159-163` |
| R12 | `POST /v1/subagents/{id}/wait` has no server-side max-duration cap; 408 cancel branch untested | `http_subagents.go:134-169` |
| R13 | MCP registry is process-global (no tenant field); no DELETE/disconnect endpoint; re-POST silently replaces | `internal/server/http_mcp.go:25-29,107-109` |
| R14 | Cross-tenant enforcement untested on `/events,/steer,/continue,/compact,/approve,/deny,/summary,/context,/input` (same authorizeRun call, no dedicated test); 429 buffer-full and 409 run-not-finished untested at HTTP level | `http_runs.go:357,385,406,450,473,515,559,663,684`; `http_conversations.go:225` |
| R15 | CLI Ctrl+C during streaming: no signal handling anywhere; kills process without cancelling server-side run | `cmd/harnesscli/*.go` (no `os/signal`); `main.go:210` prints run_id for manual recovery |
| R16 | CLI corrupt `~/.harness/config.json` silently swallowed → proceeds unauthenticated with no diagnostic | `cmd/harnesscli/main.go:560`; `auth.go:175-178` |
| R17 | CLI `--tui` silently discards `--prompt` and all prompt-extension flags; one-shot path never checks `NArg()` (trailing args/mistyped subcommand dropped) | `main.go:172-178,161-164` |
| R18 | CLI unbounded `io.ReadAll` on every response body (no LimitReader) | `main.go:238,302,505`; `runctl.go:72,147,203` |
| R19 | TUI whitespace-only prompt bypasses the empty guard (`==""` not TrimSpace) and submits a blank message | `components/inputarea/model.go:161-164`; `model.go:1842-1868` |
| R20 | TUI huge paste / no client-side input size cap on submit | `model.go:1843-1868`; `api.go:51-77` |
| R21 | TUI ask-user Esc cancel sends no skip signal + no feedback; run silently resumes/fails up to 5 min later | `askuser.go:193-195`; server backstop `internal/harness/ask_user_broker.go:51-52,75-86` |
| R22 | TUI `/quit` mid-run exits without cancelling the server-side run (unlike Ctrl+C/Esc) | `model.go:970-972` |
| R23 | TUI star/unstar persistence swallows config Load/Save errors silently; untested round-trip | `model.go:1776-1787` |
| R24 | TUI viewport `lines` grows unbounded (SetMaxHistory never called); untested at scale | `model.go` (no SetMaxHistory); `components/viewport/model.go:19,35` |
| R25 | diffview huge-diff truncation never tested at production default (MaxLines 40 never set by tooluse) | `components/diffview/view.go:14`; `tooluse/model.go:132-136` |
| R26 | tooluse bash 512KB byte-truncation is not rune-aware (splits multi-byte UTF-8); untested | `components/tooluse/bashoutput.go:59-63` |
| R27 | diffview false-positive: any tool output with a line starting `--- ` renders as a diff (no tool-name gate); `looksLikeUnifiedDiff` untested | `components/tooluse/model.go:86-93` |
| R28 | Clipboard reports "Copied!" on OSC52 write success even when the terminal/mux swallows it (TERM-only heuristic) | `cmd/harnesscli/tui/clipboard.go:13-27` |
| R29 | File autocomplete/expand allow `../` traversal by design; expanded content flows to the LLM; huge dir does full O(n) read before 20-cap | `cmd/harnesscli/tui/filecomplete.go:77-103`; `fileexpand.go` |
| R30 | CLI connection-refused untested for 4/6 network call sites (startRun POST, initial events GET, list/cancel/status) | `main.go:232-235,292-297`; `runctl.go:65-69,140-144,196-200` |
| R31 | Plugin bash slash commands run synchronously inside Update() — a slow command freezes rendering/input/SSE polling for up to the 10s timeout | `cmd/harnesscli/tui/model.go:1816`; `plugin/execute.go:44-47` |

UNKNOWN: SIGINT-during-write atomicity for `auth login` config + SQLite (`auth.go:81-86,115-127`); relay worker auto-staleness lives in `internal/relay/router.go` (not read); MCP JSON-RPC framing errors terminate outside `internal/server`; `POST /v1/summarize` and Linear real replay-header availability lack test/doc evidence.

---

# A) CLI Surface (`cmd/harnesscli`)

## Startup & flag parsing

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| No args, no subcommand | `harnesscli` | "prompt is required", exit 1 | OK | `main.go:180-183`; `TestRunRejectsMissingPrompt` (main_test.go:274) |
| Unknown/malformed flag | `harnesscli -unknown-flag=bad` | parse error, exit 1 | OK | `main.go:161-164`; `TestRunBadFlagParse` (main_coverage_test.go:214) |
| `-h` / `--help` | `harnesscli -h` | should be clean help + exit 0; instead treated as parse failure, exit 1 | RISKY | `main.go:161-164` (no `errors.Is(err, flag.ErrHelp)`); no `-h`-specific test |
| Extra trailing positional args | `harnesscli -prompt=hi extra garbage` | error or documented ignore; silently dropped | RISKY | `main.go:161-218` never calls `flags.NArg()` (unlike runctl 123-130,179-186) |
| Subcommand token after a flag | `harnesscli -base-url=http://x list` | routes to `list`; instead falls into `run()`, drops "list", says "prompt is required" | RISKY | `dispatch()` inspects only `args[0]` (auth.go:147-163) |
| Empty `--base-url=""` | `-base-url= -prompt=hi` | clear "invalid base URL"; instead cryptic Go transport error | RISKY | `main.go:226,232-235`; no client-side URL validation |
| Scheme-less `--base-url` | `-base-url=localhost:8080` | clear error; opaque transport error | RISKY | `main.go:226,232-235` |
| `--workspace` nonexistent path | `-workspace=/nope -prompt=hi` | forwarded, server validates | OK-by-design/RISKY coverage | `resolveWorkspacePath` main.go:440-448 (no check, no test) |
| `--workspace` omitted | `-prompt=hi` | defaults to `os.Getwd()` | OK | `main.go:440-448` (exercised main_test.go:117) |
| Repeatable + CSV `--prompt-behavior` | `-prompt-behavior=a,b -prompt-behavior=c` | all collected in order | OK | `csvListFlag.Set` main.go:126-135; `TestRunParsesPromptFlagsIntoRunCreateRequest` (main_prompt_test.go:13) |
| CSV blanks skipped | `-prompt-behavior=a,, ,b` | blanks dropped | OK | `main.go:127-131`; `TestCsvListFlagSetSkipsEmpty` (main_coverage_test.go:36) |
| No extension flags | `-prompt=simple` | `PromptExtensions` nil, not `{}` | OK | `main.go:186-192`; `TestRunWithNoExtensionFlags` (main_coverage_test.go:232) |
| `--model` / `--system-prompt` | set | forwarded verbatim | OK | `main.go:195-204`; `TestRunWithSystemPromptAndModel` (main_coverage_test.go:277) |
| `--agent-intent`/`--task-context`/`--prompt-profile` | set | forwarded verbatim | OK | `main.go:195-204`; main_prompt_test.go:13 |
| `--prompt-profile` unknown | `-prompt-profile=nope` | server rejects, CLI surfaces | RISKY | `main.go:201`; generic `formatAPIError` path, no end-to-end test |

## One-shot `--prompt` run (create + stream)

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path | `-base-url=<ok> -prompt=hi` | prints run_id, streams, terminal_event, exit 0 | OK | `main.go:194-217`; `TestRunCreatesAndStreamsToCompletion` (main_test.go:117) |
| Run-create 4xx/5xx | server 400 | `start run: status 400...`, exit 1 | OK | `main.go:205-208,566-579`; `TestRunCreateFailureReturnsErrorExit` (main_test.go:198) |
| Missing `run_id` | server omits it | "missing run_id", exit 1 | OK | `main.go:251-253`; `TestStartRunMissingRunID` (main_coverage_test.go:158) |
| Non-JSON create body | `not json` | decode error, exit 1 | RISKY | `main.go:248-250`; no invalid-JSON test |
| Create POST connection refused | closed port | `send run request: ...`, exit 1 | RISKY | `main.go:232-235`; no dedicated test |
| Stream GET 5xx | 500 on connect | `stream events: status 500`, no retry, exit 1 | OK | `main.go:301-307`; `TestRunStreamFailureReturnsErrorExit` (main_test.go:234), `TestStreamRunEvents_DoesNotRetryOnHTTPErrorResponse` (regression_findings_test.go:309) |
| Initial events GET refused | refused after run created | error, exit 1, no hang | RISKY | `main.go:292-297`; no dedicated test |
| Malformed SSE `data:` | `data: {not-json}` | `errInvalidSSEData`, no retry | OK | `main.go:370-373`; `TestStreamRunEventsRejectsInvalidSSEData` (main_test.go:295) |
| SSE block missing event/data | malformed block | "invalid sse block" | OK | `main.go:423-425`; main_coverage_test.go:112,123 |
| Oversized single SSE line (>4MB) | huge tool-output event | parse or clear error; instead 4 identical failed retries → opaque "token too long" | BROKEN | `main.go:310,337-339` |
| Truncated mid-JSON on clean early close | server closes mid-`data:` | reconnect/retry; instead permanent `errInvalidSSEData` | BROKEN | `main.go:341-354,370-373` (boundary case retries: regression_findings_test.go:266) |
| SSE keepalive comments | `: ping` interleaved | ignored, stream continues | OK (fixed) | `main.go:405-408,416-420`; regression_findings_test.go:25,32 |
| Multi-`data:` block join | one payload over two data lines | joined with `""` not `"\n"` (spec deviation), untested | RISKY | `main.go:422` |
| Reconnect after boundary drop | 1 event then drop | reconnect via Last-Event-ID, completes | OK (fixed) | `main.go:257-275,288-290`; regression_findings_test.go:266 |
| Reconnect budget exhausted | drops every attempt | give up after 4 | RISKY | `main.go:78-81,266-268`; only single-drop tested |
| Stream ends, no terminal event | clean close early | error not silent success | OK | `main.go:354`; `TestStreamRunEventsStreamEndsBeforeTerminal` (main_coverage_test.go:176) |
| Terminal event as last block, no trailing blank | close without `\n\n` | still recognized | OK | `main.go:341-352`; `TestStreamRunEventsTrailingBlockTerminal` (main_coverage_test.go:195) |
| Huge non-2xx body | large error body | bounded read | RISKY | `io.ReadAll` no cap `main.go:238,302` |
| SIGINT during streaming | Ctrl-C mid-stream | cancel run or guide user; instead bare process death | RISKY | no `os/signal` anywhere; `main.go:210` prints run_id |
| Concurrent invocations | two one-shot procs | independent | OK-by-construction | stateless apart from read-only loadConfig (`main.go:560`) |

## `--tui` launch

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| `--tui` on a TTY | `harnesscli --tui` | launches TUI | OK | `main.go:172-178,460-471` |
| `--tui` non-TTY stdout | `--tui > out.txt` | clear error | OK | `main.go:461-463`; `TestRunTUIRequiresTerminal` (main_tui_test.go:11) |
| `--tui` + `--prompt` (+ ext flags) | `--tui --prompt "x"` | seed session or warn; instead silently discarded | RISKY | `main.go:172-178` returns before prompt read |
| `--tui` + `--list-profiles` | both set | one clear behavior; list-profiles silently wins | OK/undocumented | `main.go:166-168` |
| `--tui` + `--workspace` | `--tui --workspace=/d` | passed into TUI config | OK | `main.go:170-178,450-458`; `TestNewTUIConfigIncludesWorkspace` (main_test.go:185) |

## `--list-profiles`

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path | `--list-profiles` | sorted table, exit 0 | OK | `main.go:488-546`; `TestListProfiles_Success`/`_OutputFormat` (list_profiles_test.go:12,123) |
| Empty list | `{"profiles":[]}` | "No profiles available", exit 0 | OK | `main.go:522-525`; `TestListProfiles_EmptyList` (list_profiles_test.go:63) |
| Server error | 5xx | formatted error, exit 1 | OK | `main.go:511-514`; `TestListProfiles_ServerError` (list_profiles_test.go:90) |
| Server unreachable | refused | error, exit 1 | OK | `main.go:498-502`; `TestListProfiles_NetworkError` (list_profiles_test.go:109) |
| Empty description/model fields | omitted | placeholders shown | OK | `main.go:535-542`; list_profiles_test.go:123 |
| `--list-profiles` + `--prompt` | both | deterministic; list-profiles wins, prompt ignored | OK/undocumented | `main.go:166-168` |
| Malformed JSON 2xx body | non-JSON | decode error, exit 1 | RISKY | `main.go:517-520`; untested |

## `auth login`

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path, no DB | `auth login` | key + config 0600, printed | OK | `auth.go:33-92`; `TestRunAuthLogin_GeneratesAndSavesKey` (auth_test.go:11) |
| DB set, persisted | `HARNESS_RUN_DB=... auth login` | key in SQLite, validates | OK (fixed) | `auth.go:51-61,94-128`; `TestRunAuthLogin_PersistsKeyToRunStore...` (regression_findings_test.go:336) |
| Bad flag | `auth login -bogus=1` | parse error, exit 1 | RISKY | `auth.go:40-43`; no auth-specific test |
| DB relative path | `HARNESS_RUN_DB=state.db` | resolved to CWD | OK | `auth.go:99-109`; regression_findings_test.go:336 |
| DB invalid/corrupt target | dir or non-sqlite | clear error | RISKY | `auth.go:115-127`; untested |
| Config dir uncreatable | unwritable $HOME | clear error | RISKY | `auth.go:69-72`; untested |
| Overwrite existing config | run twice | O_TRUNC replaces | OK-by-construction | `auth.go:74` |
| Concurrent `auth login` | two procs | no crash | RISKY | no locking (`auth.go:74-86,124`) |
| `auth` no subcommand | `harnesscli auth` | error, exit 1 | OK | `auth.go:132-135`; `TestRunAuth_NoSubcommand` (auth_test.go:90) |
| `auth` unknown subcommand | `auth bogus` | error, exit 1 | OK | `auth.go:139-141`; `TestRunAuth_UnknownSubcommand` (auth_test.go:74) |
| Dispatch routes auth | `auth login` | routed | OK | `auth.go:147-163`; `TestDispatch_AuthRouted` (auth_test.go:106) |
| Non-known first token → run | `harnesscli somethingelse` | run() flags | OK | `auth.go:159-161`; `TestDispatch_FallsBackToRun` (auth_test.go:123) |
| loadConfig absent | fresh machine | nil,nil, no header | OK (fixed) | `auth.go:167-171`; `TestLoadConfigNotExist` (auth_test.go:140), regression_findings_test.go:187 |
| loadConfig corrupt JSON | hand-edited config | surfaced error/warning; instead silent unauth fallback | RISKY | `main.go:560`; `auth.go:175-178` |

## Run control: `list` / `cancel` / `status`

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| `list` happy | `harnesscli list` | run table, exit 0 | OK | `runctl.go:35-108`; `TestRunList_ShowsAllRunIDs` (runctl_test.go:44) |
| `list --status=` | `-status=completed` | server-side filter | OK | `runctl.go:49-51`; `TestRunList_StatusFilter` (runctl_test.go:81) |
| `list --status=garbage` | `-status=bogus` | client reject or clear surface | RISKY | no enum validation `runctl.go:39,49-51` |
| `list --conversation-id=` | `-conversation-id=abc` | filtered | OK | `runctl.go:52-54`; `TestRunList_ConversationIDFilter` (runctl_test.go:350) |
| `list` no store (501) | server 501 | clear error, exit 1 | OK | `runctl.go:78-81`; `TestRunList_501NoStore` (runctl_test.go:115) |
| `list` empty | `{"runs":[]}` | "No runs found", exit 0 | OK | `runctl.go:89-92`; `TestRunList_EmptyResult` (runctl_test.go:378) |
| `list` query injection | `&`/`=` in filter | URL-encoded | OK | `runctl.go:48-57`; `TestRunList_QueryParamInjection` (runctl_test.go:472) |
| `list` unreachable | refused | error, exit 1 | RISKY | `runctl.go:65-69`; no test |
| `list` huge response | thousands of runs | bounded | RISKY | `io.ReadAll` no cap `runctl.go:72` |
| `list` malformed JSON | non-JSON 2xx | decode error | RISKY | `runctl.go:84-87`; untested |
| `cancel <id>` happy | `cancel run_123` | "cancelling", exit 0 | OK | `runctl.go:113-165`; `TestRunCancel_Success` (runctl_test.go:141) |
| `cancel` no ID | `cancel` | "run ID is required", exit 1 | OK | `runctl.go:123-126`; runctl_test.go:199,320 |
| `cancel` too many args | `cancel a b` | error, exit 1 | OK | `runctl.go:127-130`; `TestRunCancel_RejectsExtraArgs` (runctl_test.go:501) |
| `cancel` 404 | `cancel nope` | `run "nope" not found`, exit 1 | OK | `runctl.go:153-156`; `TestRunCancel_NotFound` (runctl_test.go:173) |
| `cancel` special chars in ID | slashes | percent-escaped | OK | `runctl.go:133`; `TestRunCancel_PathEscapesRunID` (runctl_test.go:408) |
| `cancel` unreachable | refused | error, exit 1 | RISKY | `runctl.go:140-144`; no test |
| `cancel` correct method/path | any | `POST /v1/runs/{id}/cancel` | OK | `runctl.go:133-134`; `TestRunCancel_SendsPostToCorrectPath` (runctl_test.go:530) |
| `status <id>` happy | `status run_123` | details, exit 0 | OK | `runctl.go:169-245`; `TestRunStatus_ShowsDetails` (runctl_test.go:214) |
| `status` no ID | `status` | required error | OK | `runctl.go:179-182`; runctl_test.go:276,334 |
| `status` too many args | `status a b` | error | OK | `runctl.go:183-186`; `TestRunStatus_RejectsExtraArgs` (runctl_test.go:515) |
| `status` 404 | `status nope` | not found, exit 1 | OK | `runctl.go:209-212`; `TestRunStatus_NotFound` (runctl_test.go:250) |
| `status` special chars | slashes | percent-escaped | OK | `runctl.go:189`; `TestRunStatus_PathEscapesRunID` (runctl_test.go:441) |
| `status` unreachable | refused | error | RISKY | `runctl.go:196-200`; no test |
| `status` long prompt | >80 chars | truncated `...` | OK/RISKY | `runctl.go:235-239`; truncation branch untested |
| Dispatch routing | `list/cancel/status` | routed | OK | `auth.go:147-163`; runctl_test.go:291,320,334 |
| No API key set | no config | request sans header (server may 401) | OK | `runctl.go:59,134,190` via `newAuthedRequest`; regression_findings_test.go:187,238 |

## Ask-user prompt on stdin (non-TUI `run.waiting_for_user`)

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path single question | valid stdin answer | printed, POSTed, continues | OK | `askuser.go:51-121`; askuser_test.go:20, regression_findings_test.go:65 |
| Non-TTY stdin | piped input, run needs input | fail fast | OK (fixed) | `main.go:383-386`; regression_findings_test.go:110 |
| Invalid option typed | `"InvalidOption"` | error, no POST | OK | `askuser.go:99-103`; askuser_test.go:90 |
| `MultiSelect:true` question | multi-select payload | pick multiple; instead unanswerable | BROKEN | `askuser.go:36,82-105` |
| GET/POST input Authorization | auth-enabled server | authenticated like other calls; instead no header → 401 | BROKEN | `askuser.go:54,111` (bypass main.go:552-564); server http.go:189, auth_scope_test.go:334-362 |
| GET input network error | unreachable | error | OK | `askuser.go:54-57`; askuser_test.go:128 |
| GET input non-200 | 404/500 | error w/ status | RISKY | `askuser.go:59-61`; untested |
| Empty `Questions` | zero questions | error not silent | OK/RISKY | `askuser.go:68-70`; no named test |
| Deadline expired | past `deadline_at` | error immediately | OK | `askuser.go:73-76`; askuser_test.go:312 |
| Multiple questions one call | 2+ single-select | one line each, all POSTed | OK | `askuser.go:82-105`; askuser_test.go:236 |
| EOF mid-question | pipe closes early | error not hang | RISKY | `askuser.go:138-160`; message unclear, untested |
| Run ID with `/` | slashes | percent-escaped GET+POST | OK | `askuser.go:53,108`; askuser_test.go:364 |
| POST body shape | success | `{"answers":{q:label}}` | OK | `askuser.go:108-110`; askuser_test.go:140,180 |
| POST non-2xx | answer rejected | error surfaced | OK/RISKY | `askuser.go:116-118`; untested |
| 10s HTTP timeout | hanging server | fails after 10s | RISKY | `askuser.go:20`; no slow-server test |
| Human think time | slow typer | unbounded (stdin read) | OK | `askuser.go:82-105` vs client timeout at 20 |

## Signal handling / SIGINT (cross-cutting)

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| SIGINT during streaming | Ctrl-C | cleanup/guidance; instead bare exit, server run not cancelled | RISKY | no `os/signal`; `runctl.go:113-165` cancel exists but unwired |
| SIGINT during config write | Ctrl-C mid-write | no corrupt config | UNKNOWN | `auth.go:79,81-86`; timing not statically determinable |
| SIGINT during SQLite write | Ctrl-C mid-persist | no corruption | UNKNOWN | `auth.go:115-127`; SQLite crash-safety property |
| SIGTERM | process manager | graceful; none | RISKY | no signal handling |

Cross-cutting: unbounded `io.ReadAll` (no LimitReader) at `main.go:238,302,505`, `runctl.go:72,147,203` — RISKY throughout (memory growth under pathological responses).

---

# B) TUI Surface

## B.1 Core interaction engine (`model.go` Update, bridge, api, askuser, cmd_parser, plugins)

### Startup

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Cold start, no config | empty config.json | defaults, no crash | OK | `model.go:272-330`; `TestTUI003_RootModelImplementsTeaModel`, `_ViewReturnsNonEmpty` (model_test.go) |
| Corrupt sessions.json | malformed JSON | starts empty | OK | `sessionstore.go:64-69`; `TestSS_LoadCorruptJSONStartsFresh` |
| Corrupt config.json | malformed persisted config | silent zero-value fallback | RISKY | `model.go:285-292`; invisible failure, no TUI-side test |
| First WindowSizeMsg | terminal size | ready flips, viewport built | OK | `model.go:1191-1223`; `TestTUI003_UpdateHandlesWindowSizeMsg` |
| View before ready | render pre-size | "Initializing..." not panic | OK | `model.go:2375-2378` |
| Server unreachable at startup | harnessd down | error on first action, no crash | RISKY | `Init()` no health check `model.go:1156-1172`; RunFailedMsg path only synthetic-tested (sse_events_test.go) |
| Overlay opened while server down | `/model`/`/keys` | load error/empty, no hang | OK | `model.go:2203-2204`; `api.go:202-227`; `TestModelsFetchErrorMsg_SetsModelSwitcherError` (model_init_test.go) |
| Resume session at startup | prior sessions | picker populated | OK | `model.go:293-296`; `TestSS001_AddAndGetByID`, `TestSS_SaveLoadRoundtrip` |

### Sending a prompt

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Type + Enter | normal text | user bubble, run starts | OK | `model.go:1804-1868`; `TestRegression_AssistantResponseRendered` (sse_field_test.go) |
| Empty input + Enter | no text | no-op | OK | `components/inputarea/model.go:161-164` |
| Whitespace-only + Enter | `"   "` | treat as empty; instead submits blank prompt | RISKY | `inputarea/model.go:162` (`==""` not TrimSpace); `model.go:1842-1868` |
| `@path` expansion | `@file.txt` | contents inlined | OK | `model.go:1842-1849`; fileexpand_test.go, `TestModel_FileExpandErrorShowsStatusMsg` |
| `@path` error | bad @path | status msg, input restored | OK | `model.go:1843-1849`; `TestModel_FileExpandErrorPreservesInput` |
| Huge paste | MBs of text | no hang/crash; no client cap | RISKY | `model.go:1843-1868`; `api.go:51-77`; untested |
| Multi-line (shift+enter/ctrl+j) | compose | newline not submit | UNKNOWN | `keys.go:37-40`; terminal-emulator dependent |
| Slash dropdown Enter | `/mo` → `/model` | accept, auto-exec zero-arg | OK | `model.go:1496-1520`; `TestDropdown_EnterAcceptsSelection` |
| Submit with no model selected | never used `/model` | block or send; sends empty, may 400 | RISKY | `model.go:2440-2447,1866-1868`; no test |

### Streaming a response

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Assistant text delta | `assistant.message.delta` | accumulates one line | OK | `model.go:1999-2015`; `TestSSEDelta_AccumulatesOnOneLine` |
| Thinking delta | `assistant.thinking.delta` | thinking bar | OK | `model.go:2016-2022`; `TestThinkingRouting_SSEThinkingDeltaUsesContentField` |
| Tool streaming | started/delta/completed | block updates in place | OK | `model.go:2023-2055,801-923`; `TestToolCallChunk_AccumulatesStreamedOutput` |
| Tool error | completed w/ error | error rendering | OK | `model.go:2040-2055`; `TestSSEEventMsg_ToolCallCompleted_WithError_UsesErrorRendering` |
| Malformed inner JSON | garbage arguments | ignored, no panic | OK | `model.go:2023-2031`; `TestSSEEventMsg_ToolCallStarted_InvalidJSON_NoPanic` (+others) |
| Malformed SSE envelope | corrupt data line | SSEErrorMsg, continues | OK | `bridge.go:120-124`; `TestSSEErrorMsg_AppendsWarningAndContinues` |
| Unknown event type | future event type | continue polling | OK (general) | `model.go:1998-2086` no default; poll re-issue outside switch (2087-2090) |
| run.failed via SSE | terminal failure | error shown, run inactive | OK | `model.go:2101-2127`; `TestSSEDoneMsg_RunFailed_*` |
| Connection drop mid-stream | socket close | flush partial, terminal, no leak | OK | `bridge.go:99-113`; `TestTUI004_BridgeStopsOnContextCancel`, `_BridgeNoGoroutineLeak` |
| Backpressure/channel full | fast burst | non-blocking drop, hard-error at 10 | OK | `bridge.go:75-91`; `TestTUI004_BridgeHandlesOverflow` |
| Very long response | MBs assistant text | linear cost (production uses AppendChunk) | OK/RISKY | `model.go:2014` OK; dead `AssistantDeltaMsg` handler is O(n²) if ever wired (`model.go:1870-1872,925-938`) |
| Stale SSE from cancelled run | old bridge msg | dropped | OK (fixed) | `model.go:1991-1995`; `TestRegression_StaleSSEEventDoesNotLeakIntoNewRun` |

### Tool approval prompt

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Approve pending tool | `tool.approval_required` | prompt + POST /approve | BROKEN | `model.go:1998-2086` (no case); `api.go` (no approve); server runner.go:63-80, http_runs.go:296-372 |
| Deny pending tool | same | prompt + POST /deny | BROKEN | same — no deny path anywhere |
| Approval timeout | no action | visible resolution | UNKNOWN/BROKEN | prompt never surfaced |

Any harnessd config requiring manual approval is unusable from the TUI.

### Ask-user question prompt

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Question single-select | `run.waiting_for_user` | overlay w/ options | OK | `model.go:2073-2082,2144-2161`; `TestAskUser_WaitingForUserSSE_SetsOverlayActive` (+2) |
| Navigate options | ↑/↓ | cursor moves | OK | `askuser.go:172-180`; `TestAskUser_Overlay_ArrowKeysNavigateOptions` |
| Answer Enter | select + Enter | POST /input, dismiss | OK | `askuser.go:181-192`; `TestAskUser_Enter_SubmitsAnswerAndDismissesOverlay` |
| Submit fails | network/HTTP error | status msg | OK | `askuser.go:126-141`; `TestAskUser_SubmitFailure_ShowsError` |
| Cancel via Esc | Esc | dismiss locally; no server skip + no feedback | RISKY | `askuser.go:193-195`; server backstop ask_user_broker.go:51-52,75-86 |
| Client deadline expires | timer fires | dismiss + warning | OK | `model.go:2171-2178`; `TestAskUser_DeadlineExpired_ShowsTimeoutAndDismisses` |
| Stale timeout | prior question timer | ignored by CallID | OK | `messages.go:59-65`; `TestAskUser_StaleTimeout_DoesNotDismissNewerPrompt` |
| run.resumed arrives | server resumes | overlay dismiss | OK | `model.go:2083-2085`; `TestAskUser_RunResumed_DismissesOverlay` |
| Multi-select question | `MultiSelect:true` | pick multiple; instead one, warned | RISKY | `askuser.go:213-215,181-192`; `TestAskUser_MultiSelect_ShowsWarningIndicator` |
| Empty questions payload | 200 `{"questions":[]}` | recover; instead total input lockup incl. Ctrl+C | BROKEN | `model.go:2073-2082,2148,1226-1233`; `askuser.go:165-168` |

### Slash commands

Registry (`cmd_parser.go:79-194`): clear, context, export, help, keys, model, quit, stats, subagents, profiles, sessions, new, search, history. `/compact` is NOT implemented.

| Command | Expected | Status | Evidence |
|---------|----------|--------|----------|
| `/clear` | wipes transcript | OK | `model.go:940-949`; `TestBuildCommandRegistry_DispatchClearDoesNotPanic`, `TestSearch_RegressionClearStillWorks` |
| `/context` | context grid | OK | `model.go:958-962`; `TestBuildCommandRegistry_AllCommandsDispatchable` |
| `/export` | writes markdown | OK | `model.go:974-987`; export_integration_test.go |
| `/help` | help dialog | OK | `model.go:951-956,1527-1535`; model_init_test.go |
| `/keys` | API-key overlay | OK | `model.go:1010-1017`; model_apikeys_test.go (15) |
| `/model` | model switcher | OK | `model.go:989-1008`; model_command_test.go (13) |
| `/quit` | tea.Quit | OK (thin) | `cmd_parser.go:129-136`; `TestBuildCommandRegistry_AllCommandsDispatchable` |
| `/stats` | stats overlay | OK | `model.go:964-968`; model_init_test.go |
| `/subagents` | list remote subagents | OK | `model.go:1019-1024`; `TestLoadSubagentsCmdReturnsDecodedSubagents` |
| `/hooks` | list loaded/skipped lifecycle hooks | OK | `model.go` (executeHooksCommand); `TestLoadHooksCmdDecodes`, `TestFormatHooksLines` |
| `/profiles` | profile picker | OK | `model.go:1026-1033`; model_init_test.go |
| `/sessions` | session picker | OK | `model.go:1035-1042`; `TestSS006_SessionsCommandOpensOverlay` |
| `/new` | reset conversationID | OK | `model.go:1044-1053`; `TestSS005_NewCommandResetsConversationID` |
| `/title <text>` | set session title; persists; shown in statusbar + picker | OK | `model.go` (`executeTitleCommand`); title_test.go |
| `/title` (no args) | show current title / "No title set" hint | OK | title_test.go |
| `/title clear` | remove session title | OK | title_test.go |
| `/init` | run generation prompt; write `<workspace>/AGENTS.md` on completion | OK | `init_agents.go` (`executeInitCommand`); init_agents_test.go |
| `/init` (AGENTS.md exists) | refuse overwrite; hint `/init confirm`; file untouched | OK | init_agents_test.go |
| `/init confirm` | overwrite existing AGENTS.md | OK | init_agents_test.go |
| `/init` (run active) | refuse; no write on the other run's completion | OK | init_agents_test.go |
| `/add-dir <path>` | attach extra root; sent as `extra_dirs` on runs; file tools confined to workspace + added roots | OK | `add_dir.go` (`executeAddDirCommand`); add_dir_test.go; `TestExtraDirs_*` (internal/harness) |
| `/add-dir` (no args) | list attached directories | OK | add_dir_test.go |
| `/add-dir remove <path>` | detach a directory | OK | add_dir_test.go |
| `extra_dirs` validation | non-absolute/nonexistent/not-a-dir → HTTP 400 | OK | `validateExtraDirs` (runner.go); `TestStartRunExtraDirsValidation`; http_extra_dirs_test.go |
| `/feedback` | zip rollouts + redacted config + runtime info; print path | OK | `feedback.go` (`executeFeedbackCommand`); feedback_test.go |
| `/feedback` redaction | canary secret never survives into the bundle | OK | feedback_internal_test.go (table: sk-, JWT, AWS, conn-string, pasted keys) |
| `/feedback` (no rollout dir) | bundle notes absence (`rollouts/NOT_PRESENT.txt`) | OK | feedback_internal_test.go |
| `/search <q>` | search transcript | OK | `model.go:1055-1068`; search_test.go (12+) |
| `/search` (no args) | usage hint | OK | `model.go:1056-1058`; `TestSearch_BT002_EmptyQueryStatusMessage` |
| `/history <q>` | search session meta | OK | `model.go:1070-1083`; `TestSearch_BT006_HistorySearchOpensOverlay` |
| `/history` (no args) | usage hint | OK | `TestSearch_HistoryNoQueryShowsUsage` |
| Unknown command | `/nope` | hint | OK | `cmd_parser.go:244-256`; `TestTUI041_UnknownCommandReturnsHint` |
| Case-insensitive | `/CLEAR` | normalized | OK | `cmd_parser.go:36`; `TestTUI041_CaseInsensitive` |
| Plugin bash handler | user JSON, bash | runs, shows output | RISKY | `plugin_loader.go:35-58`; `plugin/execute.go:37-82`; unit-only |
| Plugin prompt handler | prompt template | expands, shows | OK | `plugin/execute.go:84-90`; `TestExecutePrompt_WithArgsPlaceholder` |
| Plugin name collision | names `clear` | skipped, warned | OK | `plugin_loader.go:22-27`; `TestLoadAndRegisterPlugins_SkipsCommandCollisions` |
| Plugin dir missing | no dir | empty set | OK | `plugin/loader.go:15-22`; `TestLoadPlugins_DirectoryNotExist` |
| Malformed plugin JSON | bad file | per-file warning | OK | `plugin/loader.go:48-57`; `TestLoadPlugins_MalformedJSON` |
| Plugin bash timeout | long command | times out | OK (unit)/RISKY (UI thread) | `plugin/execute.go:44-47`; `TestExecuteBash_Timeout` |
| Plugin bash output >30KB | large stdout | head+tail cap | OK | `plugin/execute.go:92-159`; `TestExecuteBash_OutputCappedAt30KB` |

### Ctrl+C / Esc cancel

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Ctrl+C idle | nothing running | quit | OK | `model.go:1235-1251`; `TestCtrlC_NoRunIsQuit`, `TestTUI008_CtrlCQuitsModel` |
| Ctrl+C active run | mid-stream | cancel local + server, no quit | OK (fixed) | `model.go:1236-1249`; `api.go:412-429`; `TestCtrlC_ActiveRunCallsCancel`, `TestRegression_CancelSendsServerSideCancelRequest` |
| Esc idle empty | nothing | no-op | OK | `model.go:1356-1358`; `TestTUI049_EscapeNoOpWhenIdle` |
| Esc input has text | composing | clear input | OK | `model.go:1351-1356`; `TestTUI049_EscapeClearsInput` |
| Esc active run | mid-stream | cancel like Ctrl+C | OK | `model.go:1339-1350`; `TestTUI049_EscapeCancelsRun` |
| Esc overlay open | e.g. /help | close overlay only | OK | `model.go:1259-1338`; `TestTUI049_EscapeOverlayTakesPriorityOverRun` |
| Esc ask-user overlay | during question | dismiss (see ask-user RISKY) | OK/RISKY | `askuser.go:193-195` |
| Esc twice | overlay then input | close then clear | OK | `TestTUI049_TwoPressesClosesThenClears` |
| Cancel no run | defensive | no panic | OK | `TestTUI039_CancelNoRunIsNoOp` |
| Concurrent cancel | double | no race | OK | `TestTUI039_CancelConcurrentSafe`, `TestTUI039_ConcurrentEscape` |
| Server cancel POST fails | 5xx on /cancel | local cancel + warning | OK/RISKY | `api.go:412-429`; `model.go:2138-2142`; failure path untested |
| Timer after mid-tool abort | Ctrl+C in-flight tool | finalize tool state; instead frozen "running…" forever | BROKEN | `model.go:1339-1350` (never clears toolTimers/toolViews/activeToolCallID) |

### Ctrl+O expand / plan mode

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Ctrl+O toggle tool view | active tool call | expand/collapse | OK | `model.go:1359-1367`; `TestRegression_SSEToolCompleted_CtrlOTogglesExpandedOutput` |
| Ctrl+O no active tool | activeToolCallID=="" | no-op | OK | `model.go:1361` guard |
| Plan Mode | ctrl+o intent | plan UI; instead unreachable, ExpandTool wins | BROKEN/dead | `keys.go:24,81-84,89-92`; `model.go:1359`; PlanApproved/Rejected msgs no case |

### Scrolling / resize / quit / resume

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Up/Down no overlay | chatting | command history nav | OK | `model.go:1699-1708,1730-1739`; `TestBT008_UpDownRoutesToHistoryWhenNoOverlayActive` |
| PageUp/PageDown | any time | scroll half-height | OK/RISKY | `model.go:1741-1744`; TUI-level wiring test absent |
| Up/Down in overlays | model/keys/search/sessions | nav lists | OK | `model.go:1616-1723`; model_command_test.go, model_apikeys_test.go, search_test.go |
| Scroll while `/context` open | Up | inert keystroke (vp hidden) | RISKY | `model.go:1679-1709,2404-2410` |
| Resize with content | terminal resize | content preserved, reflow | OK (fixed) | `model.go:1202-1208`; `TestRegression_WindowResizePreservesViewportContent` |
| Resize preserves history | resize then Up | history intact | OK | `model.go:1209-1216`; `TestRegression_WindowResizePreservesHistory` |
| Resize during active run | resize mid-stream | keeps streaming | OK/RISKY | resize_cancel_regression_test.go; not combined with open SSE bridge |
| Resize re-wires autocomplete | input recreated | completion works | OK | `model.go:1216-1220`; `TestTabCompletion_PersistsAfterResize` |
| `/clear` | wipe | OK | OK | `model.go:940-949`; `TestSearch_RegressionClearStillWorks` |
| `/compact` | — | not implemented | Not implemented | grep confirms zero refs |
| Ctrl+C idle quit | — | quit | OK | `TestCtrlC_NoRunIsQuit` |
| `/quit` | submit | tea.Quit | OK (thin) | `cmd_parser.go:129-136` |
| Plain `q` | keystroke | literal char (not quit) | OK-by-design | no `q` binding in keys.go |
| Ctrl+D | keystroke | not bound | RISKY | grep CtrlD = none |
| `/quit` mid-run | submit while running | exits without server cancel | RISKY | `model.go:970-972` |
| Multi-turn same session | msg 2,3... | conversationID reused | OK | `model.go:1893-1901`; `TestModel_ConversationID_PersistsAcrossRuns` |
| Session tracked in store | RunStartedMsg | entry upserted | OK | `model.go:1903-1926`; `TestSS004_RunStartedCreatesSessionEntry` |
| Resume past session | select in `/sessions` | fetch prior turns; instead cosmetic, empty transcript | BROKEN | `model.go:2320-2336`; `api.go:363-393`, `messages.go:218-224` unwired |
| Delete session | `d` | removed | OK | `model.go:2337-2343`; `TestSS013_DeleteKeyRemovesSessionFromStore` |
| Session picker Esc | Esc | close | OK | `model.go:1318-1323`; `TestSS_Regression_SessionPickerEscapeClosesOverlay` |
| Session store perms | write | 0600/0700 | OK | `sessionstore.go:78,86`; `TestSS_Regression_SessionStoreFilePermissions` |

Auth caveat: `TUIConfig` has no `APIKey` field (`cmd/harnesscli/tui/config.go:4-21`); no TUI HTTP/SSE call site sets Authorization. The main.go Authorization fix did not reach the TUI path. Noted per instruction, not re-ranked.

## B.2 Overlays, panels & pickers (`components/`)

### Dead components (headline)

Eight fully-built, unit-tested overlay packages are never imported outside their own tests (repo-wide grep) and unreachable via the slash registry or `model.go` dispatch (`model.go:2398-2438`):

| Component | Claimed feature | Status | Evidence |
|-----------|-----------------|--------|----------|
| permissionprompt | per-call approve/deny/allow-all | BROKEN unreachable | `components/permissionprompt/model.go:1-165` |
| permissionspanel | bulk permission-rule view | BROKEN unreachable | `components/permissionspanel/model.go:1-60` |
| planoverlay | plan-review state machine | BROKEN unreachable | `components/planoverlay/model.go:1-96` |
| configpanel | config form (model.go uses its own inline panel instead) | BROKEN unreachable | `components/configpanel/model.go:1-40` |
| interruptui | graceful-stop banner (replaced by 3s toast) | BROKEN unreachable | `components/interruptui/model.go:1-40` |
| outputmode | compact/verbose toggle | BROKEN unreachable | `components/outputmode/model.go:1-50` |
| costdisplay | running cost (statusbar has its own inline) | BROKEN unreachable | `components/costdisplay` |
| spinner | rotating-verb thinking (thinkingbar is a static 30-line stub) | BROKEN unreachable | `components/spinner/verbs.go`; `components/thinkingbar/model.go:1-30` |

Related dead code: `KeyMap.FullHelp()/ShortHelp()` never called; `keys.PlanMode`/`EditMode` vestigial; `keys.Help` never matched; `transcriptexport.StatusModel` never constructed.

### Session picker

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Open | `/sessions` | refreshed list | OK | `model.go:1035-1041`; `TestSS006_SessionsCommandOpensOverlay` |
| Navigate | ↑/↓/j/k | cursor wraps, window follows | OK | `sessionpicker/model.go:74-92`; `TestTUI053_SelectDownAdvancesSelection` (+wraps) |
| Select | Enter | switch conversationID, clear vp, "Resumed" line | OK | `model.go:1658-1659,2320-2335`; `TestSS007_SessionPickerSelectUpdatesConversationID` |
| Delete | `d` | removed + saved | OK | `sessionpicker/model.go:138-158`; `TestSS013_DeleteKeyRemovesSessionFromStore` |
| Cancel | Esc | close | OK | `model.go:1318-1322`; `TestSS_Regression_SessionPickerEscapeClosesOverlay` |
| Empty list | 0 sessions | "No sessions found", no-op nav | OK | `sessionpicker/view.go:44-53`; `TestTUI053_ViewShowsNoSessionsOnEmptyList` |
| Huge list | 100+ | windowed 10 + "N more"; store caps 100 | OK | `sessionpicker/view.go:114-120`; `TestSS002_MaxSessionsEvictsOldest` |
| Extreme widths | 0/huge | clamp ≥20, no panic | OK | `sessionpicker/view.go:26-35`; `TestTUI053_ViewNoPanicAtExtremeWidths` |
| Corrupt store | bad JSON | fresh start | OK | `TestSS_LoadCorruptJSONStartsFresh`, `TestSS_LoadReadError` |
| Write failure | disk full | error path | OK | `TestSS_SaveMkdirAllError`, `TestSS_SaveWriteFileError` |

### Profile picker

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Open | `/profiles` | Loading then populate | OK | `model.go:1023-1031,2296-2311`; `TestProfilesCommand_Opens_Overlay`, `TestProfilesLoaded_OpensPicker` |
| Navigate | ↑/↓/j/k | scrollutil nav | OK | `profilepicker/model.go:69-87`; profilepicker/model_test.go:92,103 |
| Select | Enter | status, close | OK | `model.go:2313-2320`; `TestProfileSelected_UpdatesModel` |
| Cancel | Esc | close | OK | `model.go:1312-1317`; `TestProfiles_EscapeClosesOverlay` |
| No profiles | empty | empty-state, no-op | OK | `profilepicker/model.go:70-96`; `TestView_Empty` |
| Load error | server error | close, surface | OK | `TestProfilesLoaded_Error_ClosesOverlay` |
| Full keyboard nav | up/down/enter/esc | wired | OK | `TestProfilesOverlay_KeyboardNavigation` |
| Redundant internal Esc | picker also handles Esc | dead path (top-level intercepts) | RISKY | `model.go:1259`; `profilepicker/model.go:130-131` |

### Model switcher

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Open provider list | `/model` | cursor on current provider | OK | `modelswitcher/model.go:217-233`; `TestTUI057_OpenSetsVisible` |
| Drill into provider | Enter | level 1, filtered | OK | `modelswitcher/model.go:505-527`; `TestDrillIntoProvider_SetsBrowseLevel1` |
| Back to providers | Esc | restore cursor | OK | `modelswitcher/model.go:531-544`; `TestExitToProviderList_ResetsBrowseLevel` |
| Type-to-filter | printable | ranked cross-provider search | OK | `modelswitcher/model.go:396-476`; `TestModelSearch_SetSearchFiltersByDisplayName` |
| Backspace in search | Backspace | remove rune | OK/RISKY | `model.go:1749-1755`; backspace wiring untested |
| Esc clears search first | Esc w/ query | clear, stay open | OK | `model.go:1296-1300` |
| Star/unstar | `s` (level 1, no search) | toggle + persist | RISKY | `model.go:1776-1787`; persistence side-effect untested, errors swallowed |
| Star at level 0 | `s` | literal search char | OK-by-design | `model.go:1779` |
| vim j/k | `j`/`k` | level-aware nav | OK | `model.go:1756-1775` |
| Config panel drill-in | Enter on model | inline gateway/apikey/reasoning panel | OK | `model.go:1427-1494,2670+` |
| Unconfigured provider redirect | Enter unavailable | to /keys pre-positioned | OK | `model.go:1468-1487`; model_315_test.go |
| API key entry | K/Enter → type → Enter | accumulate, ctrl+u clears | OK | `model.go:1603-1615`; model_apikeys_test.go (15) |
| Gateway Direct vs OpenRouter | provider overlay | GatewaySelectedMsg, recompute effective | OK | `model.go:2560-2593`; model_gateway_test.go (20) |
| MaxHeight clipping | small terminal / big catalog | clips + scroll indicators | OK | `modelswitcher/model.go:255-289`; `TestIssue572_MaxHeightClipsOutput` |
| Extreme widths | tiny/huge | no panic | OK | `TestTUI137_ViewLevel1NoPanicAtExtremeWidths` |
| Availability indicator | unconfigured provider | dimmed, selectable | OK | `modelswitcher/model.go:687-712`; availability_test.go (14) |
| OpenRouter fetch failure | HTTP error | LoadError state | OK | `TestFetchOpenRouterModelsCmd_HttpError` (openrouter_models_test.go:89) |

### Diff view

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Genuine diff renders | result starts diff/---/@@ | diffview.View() | OK | `tooluse/model.go:131-161`; `TestDiffViewRouting_SSECompletedToolResultUsesDiffComponent` |
| False-positive detection | any `\n--- ` output | misrendered as diff | RISKY | `tooluse/model.go:87-92`; looksLikeUnifiedDiff untested |
| Empty diff fallback | formatter yields "" | ExpandedView | OK | `tooluse/model.go:133-163` |
| Diff scrolling | long diff | shared viewport scroll | OK-by-design | `diffview/model.go:4-36` |
| Empty diff | blank | no crash | OK | `diffview/formatter.go:33-36`; `TestTUI032_DiffViewerEmptyDiff` |
| No-newline-at-EOF marker | `\ No newline...` | header line | BROKEN (untested) | `diffview/formatter.go:111-117` |
| Huge diff truncation | >40 lines | Clip + indicator | RISKY | `diffview/view.go:14`; tooluse never sets MaxLines; only tested at MaxLines:5 |
| Binary/ANSI diff content | control bytes | sanitize; instead written raw | BROKEN | `diffview/formatter.go`,`view.go` (no utf8/ANSI handling) |

### Transcript export

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Export success | `/export` | markdown outside CWD | OK | `model.go:974-987`; `TestExportCommandWritesOutsideWorkingDirectory` |
| Fixed markdown format | — | no picker (by design) | OK | `transcriptexport/export.go:94,109-135` |
| Write error | unwritable dir | real reason; instead generic "Export failed" | BROKEN | `model.go:980-983,1974-1979`; `transcriptexport/model.go:26-76` StatusModel unwired |
| Huge transcript | thousands of entries | stream/bound; instead full in-memory | RISKY | `export.go:108-139`; `model.go:975-976` full slice copy |
| Empty transcript | nothing said | no panic | OK | `TestTUI059_ExportEmptyEntries` |
| Default output dir | no flag | cache→home skip-unwritable | OK | `transcriptexport/export.go:20-40`; `TestSelectRuntimeSafeOutputDirSkipsUnwritableCandidates` |

### Help / context / cost / stats / status bar

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| `/help` open | slash | overlay | OK | `model.go:951-956`; `TestHelpOverlay_ShowsCommands` |
| `ctrl+h`/`?` help | keybinding | open; instead literal text | BROKEN | `keys.go:69-72`; grep keys.Help in model.go = 0 |
| Help tab nav | Tab/Shift+Tab/h/l | cycle tabs | OK | `model.go:1527-1535`; overlay_keyboard_test.go:20-107 |
| Help keybinding list freshness | — | only 9 hand-curated, drifts | RISKY | `model.go:354-362` |
| `/context` open | slash | token bar | OK | `model.go:958-962`; `TestContextGrid_ShowsContextData` |
| Context no-data | 0 tokens | 0%, no div-by-zero | OK | `contextgrid/model.go:29-39` |
| Context "not available" fallback | — | dead code (View never "") | BROKEN (dead) | `model.go:2408-2410` vs `contextgrid/model.go:28-71` |
| Context TotalTokens accuracy | any model | real window; instead always 200k | RISKY | `contextgrid/model.go:10`; never set |
| Cost display | — | statusbar inline (costdisplay dead) | BROKEN unreachable | `components/costdisplay` |
| `/stats` open | slash | heatmap | OK | `model.go:964-968`; `TestStatsPanel_ShowsUsageData` |
| Stats toggle period | `r` | Week/Month/Year | OK | `model.go:1536-1542`; overlay_keyboard_test.go:137-184 |
| Stats no-data | nil | empty grid, no panic | OK | `statspanel/model.go:140-247`; `TestTUI045_EmptyDataShowsEmptyGrid` |
| Statusbar cost | costUSD>0 | `$X.XXXX` | OK | `statusbar/model.go:71-72` |
| Statusbar zero/neg/NaN cost | edge | hidden; NaN silently hidden | OK/RISKY | `statusbar/model.go:71-72` |
| Model name truncation | narrow terminal | ≤24; instead max+2 (off-by-two) | BROKEN | `statusbar/model.go:144-150` |
| Segment priority drop | too-narrow | drop low-priority; instead dead code, blunt cut | BROKEN | `statusbar/model.go:89-108` (`_ = plainLen`) |
| Resize preserves model/cost | WindowSizeMsg | survives | OK | statusbar_regression_test.go:20-141 |

### Tool-use / stream render / viewport / autocomplete / clipboard / a11y

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Collapse/expand toggle | ctrl+o | rerender | OK | `model.go:1359-1367,789-799` |
| Toggle stale tool ID | after resize/switch | safe no-op | OK (untested) | `model.go:789-799`; fields never reset on switch/resize |
| Bash output huge lines | 1000+ lines | cap 10 visible | OK | `tooluse/bashoutput.go:20,84-99`; `TestTUI035_BashOutputTruncatesLongText` |
| Bash output >512KB | huge bytes | truncate; not rune-aware | RISKY | `tooluse/bashoutput.go:59-63`; untested |
| Underlying accumulator | any size | grows uncapped | RISKY | `tooluse/accumulator.go:29-45` |
| Streaming alloc cost | 500-4000 chunks | linear | OK | `TestToolCallChunk_StreamingCostIsLinear` |
| ANSI in stream/tool text | escapes in content | neutralize; instead raw + wrap desync | BROKEN | `streamrenderer/model.go`,`tokenizer.go` |
| Non-UTF8/binary content | binary output | sanitize; instead raw | BROKEN | tooluse/streamrenderer path |
| Nested tool calls | deep tree | depth/cycle guard; none | BROKEN | `tooluse/nested.go` |
| Error text with control seq | colorized stderr | strip; instead raw | BROKEN | `tooluse/errorview.go` |
| Long single-line error | 200+ char word | wraps | OK | `errorview.go:112-191`; `TestTUI037_ErrorTextWraps` |
| Timer negative clamp | clock skew | clamp 0 (untested) | OK | `tooluse/timing.go:82-97` |
| Timer after abort mid-tool | Ctrl+C in-flight | finalize; instead frozen | BROKEN | `model.go:1339-1350` |
| Two duration formatters | Timer vs durationfmt | consistent; durationfmt no clamp | RISKY | `durationfmt/durationfmt.go:10-20` |
| Scroll clamp | past bounds | clamp 0/max | OK | `viewport/model.go:146-166`; `TestTUI013_ScrollUpClamps` |
| PageUp/Down | pgup/pgdn | half-height | OK | `model.go:1741-1744`; scroll_test.go:35-154 |
| No Home/End | — | jump-to-top/bottom missing | BROKEN (affordance) | `keys.go` (only Up/Down + PgUp/Dn) |
| Empty content | 0 lines | no panic | OK | `viewport/model.go:231-238`; `TestTUI013_EmptyViewport` |
| Huge content render | 100k+ lines | O(1) window | OK | `viewport/virtualization.go` |
| Huge content memory | long session | `lines` uncapped (SetMaxHistory unused) | RISKY | `model.go` (no SetMaxHistory); `viewport/model.go:19,35` |
| scrollutil maxVisible<=0 | degenerate | guard; none | RISKY | `scrollutil.go` |
| Autocomplete trigger | Tab after @path | suggests, cap 20 | OK | `filecomplete.go:9,49-58,77-103` |
| Nonexistent dir | Tab into missing | nil, no panic | OK | `TestFilePathCompleter_NonexistentDirReturnsNil` |
| Path traversal `../` | typed | no confinement (content → LLM) | RISKY | `fileexpand.go`; untested as security case |
| Huge dir | node_modules | full O(n) read pre-cap | RISKY | `filecomplete.go:77-103` |
| Symlinks in autocomplete | symlinked entry | no recursion, safe | OK-by-construction | `filecomplete.go` |
| Expand missing file | `@/no/file` | error, input preserved | OK | `fileexpand.go:172-174`; fileexpand_test.go:48 |
| Expand binary file | `@binary` | rejected | OK | `fileexpand.go:199-208`; fileexpand_test.go:127 |
| Expand huge file | >1MB | rejected pre-read | OK | `fileexpand.go:12,189-194`; fileexpand_test.go:97 |
| Expand symlink | `@symlink` | rejected | OK | `fileexpand.go:179-181`; `TestExpandAtPaths_SymlinkRejected` |
| Expand directory | `@dir` | rejected | OK | `fileexpand.go:183-186`; fileexpand_test.go:421 |
| >10 @paths | 11+ mentions | rejected pre-I/O | OK | `fileexpand.go:66-70`; fileexpand_test.go:214 |
| XML/CDATA injection | `]]>` in file | escaped | OK | `fileexpand.go:136-149`; fileexpand_test.go:353 |
| Copy last response | ctrl+s | OSC52 write | OK | `clipboard.go:16-19`; clipboard_test.go:13-108 |
| Headless / dumb TERM | no TTY | "Copy unavailable" | OK | `clipboard.go:24-27`; `TestTUI028_CopyActionGracefulInHeadless` |
| SSH w/o OSC52 forwarding | TERM set, mux swallows | false "Copied!" | RISKY | `clipboard.go:13-27` (TERM-only heuristic) |
| Empty buffer copy | no response | handled | OK | `TestTUI028_CopyEmptyBuffer` |
| Layout tiny/negative sizes | 1x1/negative | stable | OK | `layout/constraints.go`; `TestTUI005_LayoutStaysStableAtTinySizes` |
| Separator ASCII fallback | no-unicode | ASCII borders | OK | `separators.go`; `TestTUI019_BorderFallbackAscii` |
| a11y hints | no-color/non-TTY | plain labels; instead never consumed | BROKEN | `a11y.go:5-19` |
| Unknown overlay kind | unexpected value | viewport fallback | OK | `model.go:2435-2437` |
| Concurrent value-model access | — | immutable copies | OK | `TestTUI057_ConcurrentModels` (+picker/gateway/clipboard) |

---

# C) API Surface (`internal/server`)

## Auth & tenant middleware

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Bearer token | header | validated against store | OK | `auth.go:182-193`; `TestAuthMiddleware_Valid` |
| `?token=` fallback | SSE EventSource | same validation | OK | `auth.go:191-192`; `TestAuthMiddleware_QueryParam` |
| Missing/invalid/malformed auth | none/wrong | 401 | OK | `auth.go:86-100`; `TestAuthMiddleware_Invalid` |
| Expired key | past ExpiresAt | 401 api key expired | OK | `auth.go:94-97`; `TestAuthMiddleware_ExpiredKey` |
| Auth disabled/no store | flag/nil | pass, tenant "" | OK | `auth.go:74-82`; `TestAuthMiddleware_Disabled`, `_NoStore` |
| Scope hierarchy | scoped routes | 403 on shortfall | OK | `auth.go:125-145`; auth_scope_test.go (12+) |
| effectiveTenantID mismatch | foreign tenant_id | 400, no leak | OK | `auth.go:206-226`; `TestEffectiveTenantID_PostRun`/`_ListRuns`/`_ListConversations` |
| Concurrent key validation | parallel | no race | OK | `TestAuthMiddleware_ConcurrentValidation` |
| Unauth route body caps | webhook/trigger | 1 MiB cap | OK | `http.go:246-264,354-368` |
| Authenticated core body size | /runs,/continue,/steer,/compact | some bound; none | RISKY | `http_runs.go:45,414,482,525`; `http_conversations.go:240` |

## Run create

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path | valid prompt | 202, run_id | OK | `http_runs.go:43-74`; `TestRunLifecycleEndpoints` |
| Malformed JSON | `{` | 400 | OK | `http_runs.go:45-48`; `TestRunsEndpointMethodNotAllowedAndInvalidJSON` |
| Empty prompt | `{}` | 400 | OK | `runner.go:431-433` (shared w/ continue tested) |
| Unknown agent_intent | missing | 400 | OK | `TestRunsEndpointReturns400ForUnknownIntent` |
| prompt_extensions | payload | 202 | OK | `TestRunsEndpointAcceptsPromptExtensionsPayload` |
| Cross-tenant tenant_id | mismatch | 400, no leak | OK | `TestEffectiveTenantID_PostRun` |
| Persistence exactly-once | counting store | CreateRun once | OK | `TestPostRunPersistsExactlyOnce` |
| Special chars/unicode/null | metachars/emoji | round-trips | OK | `TestPromptSpecialCharactersRoundTrip` (13) |
| Method not allowed | DELETE | 405 | OK | `TestRunsEndpointMethodNotAllowedAndInvalidJSON` |
| Huge body | multi-GB | bounded | RISKY | see body-size row above |
| Negative max_steps/max_cost | adversarial | 400 | OK | `runner.go:434-441`; `http_runs.go:64-68` |

## Run stream (SSE)

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Happy path | GET .../events | history + live, terminal | OK | `http_runs.go:679-755`; `TestRunLifecycleEndpoints` |
| SSE frame format | any event | id/retry/event/data | OK | `http.go:381-399`; `TestWriteSSE_IncludesIDAndRetry` |
| Keepalive ping | idle | `: ping` comment | OK | `http.go:415-421`; `TestWriteSSEPing`, `TestSSEKeepalivePingsInEventStream` |
| Last-Event-ID reconnect | reconnect | skip seen | OK | `http_runs.go:696-705`; `TestLastEventIDSkipsSeenEvents` |
| Unknown run | GET missing | 404 | OK | `http_runs.go:689-693`; `TestRunByIDEndpointsNotFoundAndMethodValidation` |
| Method not allowed | POST | 405 | OK | same |
| Non-flushable writer | no Flusher | 500 stream_unsupported | RISKY | `http_runs.go:707-711`; untested |
| Client disconnect | close conn | stop, cleanup | OK | `http_runs.go:694,732-744` (defer cancel) |
| Cross-tenant /events | foreign token | 404 | RISKY | `http_runs.go:684-687`; not in TestRunEndpoints_CrossTenantDenied |
| Stale cross-run leak | concurrent runs | no mixing | OK (fixed) | `runner.go:1427-1460` |

## Run steer/continue

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Steer active | .../steer | 202 | OK | `http_runs.go:400-441`; `TestHandleSteer_Success` |
| Steer unknown | nonexistent | 404 | OK | `TestHandleSteer_RunNotFound` |
| Steer empty prompt | `""` | 400 | OK | `TestHandleSteer_EmptyMessage` |
| Steer completed | terminal | 409 run_not_active | OK | `TestHandleSteer_CompletedRun` |
| Steer method | GET | 405 | OK | `TestHandleSteer_MethodNotAllowed` |
| Steering buffer full | rapid steers | 429 | RISKY | `http_runs.go:432-435`; only unit-level `TestSteerRun_BufferFull` |
| Continue completed | +prompt | 202 new run_id | OK | `TestContinueRunEndpointBasic` |
| Continue tool/perm overrides | overrides | applied | OK | `TestContinueRunEndpointAllowsToolAndPermissionOverrides` |
| Continue unknown | nonexistent | 404 | OK | `TestContinueRunEndpointNotFound` |
| Continue malformed JSON | bad | 400 | OK | `TestContinueRunEndpointInvalidJSON` |
| Continue empty prompt | `""` | 400 | OK | `TestContinueRunEndpointEmptyMessage` |
| Continue invalid perms | bad scope | 400 | OK | `TestContinueRunEndpointInvalidPermissions` |
| Continue still-running | not terminal | 409 | OK | `TestContinueRunEndpointRunningConflict` |
| Continue method | GET | 405 | OK | `TestContinueRunEndpointMethodNotAllowed` |
| Continue SSE resume | subscribe new | run.completed | OK | `TestContinueRunEndpointSSEResumedEvent` |
| Double-continue race | concurrent | one wins | OK | `runner.go:1145-1158`; `TestContinueRunConcurrencyRace` |
| Continue failed run | status failed | 409 or new | OK | `TestContinueRunFailedRun` |
| GET/POST /input | ask-user | 200/400/202/409 | OK | `http_runs.go:558-626`; `TestRunInputEndpoints` |
| Cross-tenant steer/continue/input | foreign token | 404 | RISKY | `http_runs.go:406,515,559`; not in cross-tenant test |

## Run approve/deny

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Unknown run | approve/deny | 404 | OK | `http_runs.go:357-360,385-388`; `TestHandleApprove_NotFound`, `TestHandleDeny_NotFound` |
| Method not allowed | GET | 405 | OK | `TestHandleApprove_MethodNotAllowed` |
| No broker | nil | 501 | OK | `http_runs.go:353-356,381-384` |
| Full approve flow | pause→approve | 200, completes | OK | `TestHandleApproveAndDeny_IntegrationFlow` |
| Full deny flow | pause→deny | 200, completes | OK | `TestHandleDeny_IntegrationFlow` |
| Not awaiting approval | /approve no pending | 404 | OK | `http_runs.go:361-365`; approval_broker_test.go:255-263 |
| Double-approve | second call | 404 | RISKY | broker unit only (approval_broker_test.go:243-251); no HTTP test |
| Two concurrent approvals | 2 tool calls | queue/graceful | RISKY | `approval_broker.go:114-118` rejects 2nd; downstream UNKNOWN |
| Cross-tenant approve/deny | foreign token | 404 | RISKY | `http_runs.go:357,385`; untested |

## Run cancel

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Cancel active | .../cancel | 200, cancelled | OK | `http_runs.go:319-343`; `TestHandleCancel_Success` |
| Cancel unknown | nonexistent | 404 | OK | `TestHandleCancel_NotFound` |
| Cancel terminal | completed | 200 idempotent | OK | `runner.go:2888-2891`; `TestHandleCancel_TerminalRun`, `TestCancelRun_AlreadyTerminal` |
| Double-cancel | twice | 200, no panic | OK | `TestCancelRun_DoubleCancelIdempotent` |
| Cancel waiting-for-user | paused | takes effect | OK | `TestCancelRun_WaitingForUser` |
| Cancel emits event | active | run.cancelled | OK | `TestCancelRun_EmitsEvent` |
| Method not allowed | GET | 405 | OK | `TestHandleCancel_MethodNotAllowed` |
| Scope enforcement | read-only key | 403 | OK | `TestScope_ReadOnly_CannotCancelRun` |
| Cross-tenant cancel | foreign token | 404 | OK | `TestRunEndpoints_CrossTenantDenied` |
| Concurrent cancels | true race | idempotent | OK (reasoned) | `runner.go:2895-2897` (CancelFunc safe) |

## Run summary

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Summary happy | completed run | 200 metrics | OK | `http_runs.go:658-677`; `TestRunSummaryEndpoint` |
| Summary unknown | missing | 404 | OK | `TestRunSummaryNotFound` |
| Summary still-running | status running | 409 run_not_finished | RISKY | `runner.go:1303-1305`; untested |
| Summary cancelled | after cancel | same 409 path | RISKY | `runner.go:1303`; undocumented |
| Cross-tenant summary | foreign token | 404 | RISKY | `http_runs.go:663`; untested |

## Conversation list/search

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| List no store | GET / | 501 | OK | `TestHandleListConversationsNoStore` |
| List method | POST | 405 | OK | `TestHandleListConversationsMethodNotAllowed` |
| List filters | query params | correct filter | OK | `TestHandleListConversationsFilterBy*` |
| List cross-tenant `?tenant_id=` | mismatch | 400 | OK | `TestEffectiveTenantID_ListConversations` |
| `?q=` delegates to search | GET /?q=foo | delegates | OK | `http_conversations.go:348-350`; `TestListConversations_QParam_DelegatesToSearch` |
| Search cross-tenant leak | any tenant, any query | own snippets only; instead leaks all tenants | BROKEN | `http_conversations.go:156-186`; interface conversation_store.go:64-66; SQL conversation_store_sqlite.go:647-675 |
| Search missing q | no query | 400 | OK | `TestHandleSearchConversations_MissingQuery` |
| Search store error | error | 500 | OK | `TestHandleSearchConversations_StoreError` |
| Search method | POST | 405 | OK | `TestHandleSearchConversations_MethodNotAllowed` |
| Search no store | nil | 501 | OK | `TestHandleSearchConversations_NoStore` |
| Pagination negative/non-numeric | limit=-5/abc | default fallback | RISKY | `parsePositiveInt` http.go:370-379; negative/overflow untested |
| Pagination overflow | huge digit string | reject/clamp; instead can wrap negative → unlimited scan | RISKY | `http.go:370-379` |

## Conversation messages/export

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET messages | existing | 200 | OK | `http_conversations.go:73-95`; `TestConversationMessagesEndpoint` |
| GET messages unknown | nonexistent | 404 | OK | `TestConversationMessagesEndpoint404` |
| Export happy | recent conv | 200 NDJSON | OK | `http_conversations.go:188-220`; `TestHandleExportConversation_FromStore` |
| Export unknown | nonexistent | 404 | OK | `TestHandleExportConversation_NotFound` |
| Export method | POST | 405 | OK | `TestHandleExportConversation_MethodNotAllowed` |
| Export store error | LoadMessages err | 500 | OK | `TestHandleExportConversation_StoreError` |
| Export no store | nil | 404 | OK | `TestHandleExportConversation_StoreNotConfigured` |
| Export empty conversation | 0 messages | 200 empty; instead 404 (== not found) | RISKY | `http_conversations.go:206-209` |
| Export huge conversation | thousands | streams (good); mid-stream write fail after 200 | RISKY | `http_conversations.go:212-219` |
| Cross-tenant messages/export | foreign token | 404 | OK | `TestConversationEndpoints_CrossTenantDenied` |
| GET /{id}/runs | list runs | 200 tenant-scoped | OK | `http_runs.go:110-138`; `TestConversationRunsEndpoint` |

## Conversation compact

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Compact happy | keep_from + summary | 200 | OK | `http_conversations.go:224-303`; `TestCompactConversationEndpoint_Basic` |
| Compact no store | nil | 501 | OK | `TestCompactConversationEndpoint_NoStore` |
| Compact malformed JSON | bad | 400 | OK | `TestCompactConversationEndpoint_InvalidJSON` |
| Compact empty summary | auto-generate | LLM summary, 404 if 0 msgs | OK | `TestCompactConversationEndpoint_EmptySummary` (+auto tests) |
| Compact negative keep_from | -1 | 400 | OK | `TestCompactConversationEndpoint_NegativeKeepFrom` |
| Compact nonexistent | unknown | 404 | OK | `TestCompactConversationEndpoint_NonExistentConversation` |
| Compact method | GET | 405 | OK | `TestCompactConversationEndpoint_WrongMethod` |
| Compact role default/custom | with/without role | system default | OK | `TestCompactConversationEndpoint_SummaryRoleDefault`, `_CustomRole` |
| Concurrent compacts | 5 parallel diff convs | independent | OK | `TestCompactConversationEndpoint_Concurrent` |
| Double-compact | already compacted | no corrupt; undocumented | RISKY | `conversation_store_sqlite.go:519-537`; no guard, untested |
| Run-level compact happy | POST /runs/{id}/compact | 200 | OK | `http_runs.go:466-508`; `TestCompactEndpointTriggersCompaction` |
| Run-level compact edges | unknown/inactive/bad mode | 404/409/400 | OK | `TestCompactEndpoint404ForUnknownRun` (+others) |
| Cross-tenant conv compact | foreign token | 404 | RISKY | `http_conversations.go:225`; not in cross-tenant test |

## Conversation delete / cleanup

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Delete happy | existing | 200 | OK | `http_conversations.go:395-410`; `TestHandleDeleteConversationSuccess` |
| Delete no store | nil | 501 | OK | `TestHandleDeleteConversationNoStore` |
| Delete store error | error | 500 | OK | `TestHandleDeleteConversationStoreError` |
| Cross-tenant delete | foreign token | 404 | OK | `TestConversationEndpoints_CrossTenantDenied` |
| Delete in-flight run's conv | active run | blocked/sticks; instead likely resurrected by upsert | RISKY | `http_conversations.go:395-410`; `runner.go:2324` |
| `POST /conversations/cleanup` | any runs:write | own tenant only; instead deletes ALL tenants | BROKEN | `http_conversations.go:305-340`; SQL conversation_store_sqlite.go:472-490 |

## Checkpoints

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET checkpoint | valid ID | 200 | OK | `http_checkpoints.go:59-74`; `TestHandleCheckpointResume` |
| Resume checkpoint | valid + payload | 200 resumed | OK | `http_checkpoints.go:76-102`; `TestHandleCheckpointResume` |
| Service not configured | nil | 501 | OK | `http_checkpoints.go:28-31` |
| Unknown checkpoint | nonexistent | 404 | RISKY | `http_checkpoints.go:66-69,94-97`; untested |
| Resume malformed JSON | bad body | 400 / default {} | OK (partial) | `http_checkpoints.go:85-91`; malformed untested |
| No tenant scoping | any tenant + ID | owner only; instead any tenant reads/resumes | BROKEN | `http_checkpoints.go:14-17,59-102`; `internal/checkpoints/store.go:27-43` (no TenantID) |

## Replay

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| Simulate mode | valid rollout_path | 200 summary | OK | `http_replay.go:82-99`; `TestHandleRunReplay_Simulate` |
| Fork mode | +fork_step | 202 new run_id | OK | `http_replay.go:101-144`; `TestHandleRunReplay_Fork` |
| Method not allowed | GET | 405 | OK | `TestHandleRunReplay_MethodNotAllowed` |
| Missing rollout_path | omitted | 400 | OK | `TestHandleRunReplay_MissingRolloutPath` |
| Invalid mode | bad | 400 | OK | `TestHandleRunReplay_InvalidMode` |
| Rollout not found | nonexistent | 404 | OK | `http_replay.go:156-169`; `TestHandleRunReplay_RolloutNotFound` |
| fork_step exceeds max | 99 on 2-event | 400 | OK | `TestHandleRunReplay_ForkStepExceedsMax` |
| Malformed JSON | bad | 400 | OK | `TestHandleRunReplay_InvalidJSON` |
| Absolute-path traversal | /etc/passwd | 400 | OK (fixed) | `http_replay.go:71-73`; `TestHandleRunReplay_RejectsAbsolutePath` |
| `..` traversal | escape | 400 | OK (fixed) | `http_replay.go:74-78`; `TestHandleRunReplay_RejectsPathTraversal` |
| Unconfigured base dir | empty | 400 | OK | `http_replay.go:67-70`; `TestHandleRunReplay_UnconfiguredBaseDir` |
| Fork zero messages | no user msgs | 400 | OK | `http_replay.go:115-118` |
| replay doesn't shadow /{id} | GET nonexistent | 404 | OK | `http_runs.go:175-182`; `TestHandleRunByID_StillWorks` |
| No tenant check on rollout | any tenant, any path | own runs only; instead can replay/fork foreign run if UUID leaks | RISKY | `http_replay.go:67-80,125-129` |

## Subagents

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET/POST /v1/subagents | list/create | scope-checked, 501 unconfigured | OK | `http_subagents.go:15-59`; `TestSubagentsEndpoint_Create`/`_ListAndGet`/`_NotConfigured` |
| POST invalid JSON | malformed | 400 | RISKY | `http_subagents.go:41-44`; untested |
| GET/DELETE /{id} | by-ID | 404/409 | OK | `http_subagents.go:93-131`; `TestSubagentsEndpoint_GetNotFound`, `_DeleteActiveReturns409` |
| POST /{id}/wait | block until terminal | 200/ctx timeout | OK/RISKY | `http_subagents.go:134-169`; no max cap, 408 untested |
| POST /{id}/cancel | cancel | 200/404 | OK | `http_subagents.go:171-185`; `TestSubagentsEndpoint_CancelReturnsCancellingStatus` |
| Unknown action | /{id}/frobnicate | 404 | RISKY | `http_subagents.go:83-85`; untested |
| Cross-tenant access | tenant B reads A's subagent | 404; instead full access | BROKEN | `internal/subagents/manager.go:63-77`; `http_subagents.go:15-185` (no checkTenantOwnership) |

## Workflows (legacy)

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET /v1/workflows | list | scope-checked | OK | `http_workflows.go:30-45`; `TestHandleWorkflowRoutes` |
| GET /{name} | get | 404 unknown | RISKY | `http_workflows.go:59-74`; not-found untested |
| POST /{name}/runs | start | 202/400 | OK/RISKY | `http_workflows.go:76-99`; error paths untested |
| GET /workflow-runs/{id} | get | 404 unknown | RISKY | `http_workflows.go:115-137`; untested |
| POST /workflow-runs/{id}/resume | resume | 202/400 | RISKY | `http_workflows.go:139-164`; zero HTTP test |
| GET .../events SSE | stream | replay+live+ping | OK | `http_workflows.go:165-220`; `TestHandleWorkflowEventsStreamKeepalivePing` |
| SSE run not found | unknown | 404 | RISKY | `http_workflows.go:175-178`; untested |
| Cross-tenant access | any tenant | tenant-scoped; instead global | BROKEN | `workflows.Run` no TenantID; no checkTenantOwnership |

## Script-workflows

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET /v1/script-workflows | list | scope-checked, 501 | OK | `http_script_workflows.go:30-45`; `TestPOC1_ListScriptWorkflows`, `TestPOC10_NilManager...` |
| GET /{name} | get meta | 404 | OK | `http_script_workflows.go:60-77`; `TestPOC2_GetScriptWorkflowByName` |
| POST /{name}/runs | start | 202; malformed args swallowed not 400 | RISKY | `http_script_workflows.go:89-98` |
| GET /script-workflow-runs/{id} | get | 404 | OK | `http_script_workflows.go:124-147`; `TestPOC4_...`, `TestPOC8_ErrorHandling` |
| POST .../resume failed run | resume | 202 | OK | `http_script_workflows.go:150-174`; `TestPOC5_ResumeFailedRun` |
| POST .../resume non-failed | running/completed | 400 | OK/RISKY | engine.go:194-197; no HTTP test |
| POST .../resume malformed JSON | bad | swallowed | RISKY | `http_script_workflows.go:161-163` |
| GET .../events SSE | stream | replay+live+ping | OK | `http_script_workflows.go:177-230`; `TestPOC4_StreamEvents`, `TestPOC9_SSEEventFormat` |
| SSE disconnect | client close | stop, cancel | RISKY | `http_script_workflows.go:191,212-213`; untested |
| Concurrent fan-out | many starts | no contamination | OK | `TestPOC7_ConcurrentWorkflowRuns`, `TestPOC14_ConcurrentFanOut` |
| Advanced patterns | composite scripts | correct sequencing | OK | `TestPOC11..15` (advanced) |
| Cross-tenant access | any tenant | tenant-scoped; instead global | BROKEN | `internal/workflow.Run` no TenantID; no checkTenantOwnership |

## Skills / Profiles

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET /v1/skills | list | 200/501 | OK | `http_skills.go:14-30`; `TestSkillsList_Returns200WithList` (+empty,+501) |
| GET /v1/skills wrong method | POST | 405 | OK | `TestSkillsList_MethodNotAllowed` |
| GET /{name} | by name | 200/404 | OK | `http_skills.go:63-73`; `TestSkillGetByName_Returns200`/`_404ForUnknown` |
| POST /{name}/verify | mark verified | 200/404 | OK | `http_skills.go:94-129`; `TestSkillVerify_*` |
| Verify then Get fails | race | 500 | RISKY | `http_skills.go:123-127`; untested |
| Unknown subpath | /foo/bar | 404 | RISKY | `http_skills.go:57-59`; untested |
| Skills not tenant-scoped | any tenant | N/A shared config | OK-by-design | http.go:41-47 |
| GET /v1/profiles | list tiers | 200 | OK | `http_profiles.go:14-24,66-79`; `TestListProfilesHandler_ReturnsJSON` |
| GET /{name} | get one | 200/404 | OK | `http_profiles.go:82-119`; `TestGetProfileHandler_*` |
| POST /{name} | create | 201/409/400/501 | OK | `http_profiles.go:148-181`; `TestCreateProfileHandler_*` |
| PUT /{name} | update | 200/403/404/400 | OK | `http_profiles.go:184-249`; `TestUpdateProfileHandler_*` |
| PUT early os.Stat on raw name | traversal-y name | reject pre-stat | RISKY | `http_profiles.go:191-192`; untested at HTTP layer |
| DELETE /{name} | delete | 200/403/404 | OK | `http_profiles.go:252-277`; `TestDeleteProfileHandler_*` |
| Profiles not tenant-scoped | any tenant | N/A operator config | OK-by-design | internal/profiles |

## Relay workers

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET /v1/relay/workers | list | tenant-filtered | OK | `http_relay_workers.go:92-117`; `TestRelayListWorkers` (+query,+empty) |
| POST /v1/relay/workers | register | 201/validated/409 | OK (mechanics)/BROKEN (tenant) | `TestRelayRegisterWorker*` |
| Registration tenant spoofing | body tenant_id=victim | forced to caller tenant; instead trusted | BROKEN | `http_relay_workers.go:120-198` (123,143-145,168; no effectiveTenantID) |
| GET/PUT/DELETE /{id} cross-tenant | foreign worker | 404 | OK | `http_relay_workers.go:211-307`; `TestRelayWorkerEndpoints_CrossTenantDenied` |
| POST /{id}/heartbeat | heartbeat | 200/400/404 | OK | `http_relay_workers.go:320-372`; `TestRelayWorkerHeartbeat*` |
| Duplicate worker ID | same ID | 409 | OK | `http_relay_workers.go:188-192`; `TestRelayRegisterWorkerDuplicate` |
| Worker offline / staleness sweep | no heartbeat | auto stale/offline | UNKNOWN | logic in internal/relay/router.go (not read) |
| Malformed JSON | bad body | 400 | OK | `http_relay_workers.go:130-133`; `TestRelayRegisterWorkerInvalidJSON` |
| Method not allowed | wrong verb | 405 + Allow | OK | `http_relay_workers.go:32-34,86-88`; `TestRelayWorkerMethodsNotAllowed` |
| Not configured | nil store | 501 | OK | `http_relay_workers.go:15-18,40-43`; `TestRelayWorkersNotConfigured` |

## Webhooks (GitHub / Slack / Linear) & External trigger

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GitHub valid sig start/steer | new/active | 202 | OK | `http_github_webhook.go:26-68`; `TestHandleGitHubWebhook_StartNewRun`, `_SteerActiveRun` |
| GitHub invalid sig | tampered | 401 | OK | `http_github_webhook.go:61-64`; validator.go:46-57; `TestHandleGitHubWebhook_InvalidSignature` |
| GitHub missing sig/headers | malformed | 401/400 | OK | `TestHandleGitHubWebhook_MissingSig`/`_MissingEventHeader`/`_MissingDeliveryHeader` |
| GitHub unknown event | fork | 400 | OK | `TestHandleGitHubWebhook_UnknownEventType` |
| GitHub adapter nil | unconfigured | 401 | OK | `TestHandleGitHubWebhook_AdapterNotConfigured` |
| GitHub wrong method | GET | 405 | OK | `TestHandleGitHubWebhook_MethodNotAllowed` |
| GitHub oversized body | >1 MiB | 413; instead 400 (memory still capped) | BROKEN (minor) | `internal/github/adapter.go:55-58` vs `http_external_trigger.go:38-46`; untested |
| GitHub delivery replay | resend delivery | dedup; none | UNKNOWN | no dedup in adapter |
| Slack valid/invalid sig | HMAC+timestamp | 202/401 | OK | validator.go:73-102; `TestHandleSlackWebhook_ValidRequest`/`_InvalidSignature` |
| Slack missing headers | no ts/sig | 400/401 | OK | slack/adapter.go:38-46; `TestHandleSlackWebhook_Missing*` |
| Slack stale replay | old ts, valid sig | rejected >300s | OK (mechanism) | validator.go:91-93; no named replay test |
| Slack oversized body | >1 MiB | 413; instead 400 | BROKEN (minor) | slack/adapter.go:48-51; untested |
| Linear valid/invalid sig | hex HMAC | 202/401 | OK | validator.go:111-122; `TestHandleLinearWebhook_IssueCreate`/`_InvalidSignature` |
| Linear unsupported event | bad type | 400 | OK | `TestHandleLinearWebhook_UnsupportedEventType` |
| Linear adapter nil / wrong method | — | 401/405 | OK | `TestHandleLinearWebhook_AdapterNotConfigured`/`_MethodNotAllowed` |
| Linear replay attack | resend valid | rejected; instead no freshness check | RISKY | validator.go:110-122 (no timestamp/nonce) |
| Linear oversized body | >1 MiB | 413; instead 400 | BROKEN (minor) | linear/adapter.go:34-37; untested |
| External trigger start/steer/continue | valid sig | 202 | OK | `http_external_trigger.go:30-98`; `TestHandleExternalTrigger_*` |
| External trigger state mismatch | steer completed | 409 | OK | `http_external_trigger.go:162-193`; `TestHandleExternalTrigger_SteerCompletedRun_Conflict` |
| External trigger no thread | steer/continue | 404 | OK | `TestHandleExternalTrigger_SteerNoThread_NotFound` |
| External trigger invalid sig / no validator | — | 401 | OK | `TestHandleExternalTrigger_InvalidSignature`/`_NoValidatorForSource` |
| External trigger missing fields | — | 400 | OK | `TestHandleExternalTrigger_MissingRequiredFields` |
| External trigger oversized body | >1 MiB | 413 | OK | `http_external_trigger.go:37-46`; `TestHandleExternalTrigger_BodyTooLarge` |
| External trigger wrong method / no store | — | 405/501 | OK | `TestHandleExternalTrigger_MethodNotAllowed` |
| External trigger tenant_id spoofing | body tenant_id=victim | source-bound tenant; instead act under any tenant | BROKEN | `http_external_trigger.go:49-54,107-216`; types.go:14; validator.go:15-36 |
| Webhook-sourced tenant separation | gh/slack/linear | per-source tenant; instead all "default" | RISKY (design gap) | github/adapter.go:73-83 never sets TenantID |

## MCP / Healthz / Cron / Recipes / Networks / Sourcegraph / Catalog / Todos / Agents

| Path | Trigger | Expected | Status | Evidence |
|------|---------|----------|--------|----------|
| GET /v1/mcp/servers | list | 200 | OK | `http_mcp.go:33-64`; `TestMCPListEmpty` |
| POST /v1/mcp/servers | connect (admin) | 201 | OK | `http_mcp.go:42-53,66-112`; `TestMCPConnectAndList` |
| POST missing url / invalid JSON | — | 400 | OK | `TestMCPConnectMissingURL`, `TestMCPConnectInvalidJSON` |
| POST connect fails | Connect() err | 502 | OK | `http_mcp.go:92-96`; `TestMCPConnectError` |
| POST nil connector | — | 501 | OK | `TestMCPNilConnector` |
| Method not allowed | — | 405 | OK | `TestMCPMethodNotAllowed` |
| Name derivation | url→name | derived | OK | `TestMCPConnectDerivedName` |
| Concurrent connect/list | race | RWMutex safe | OK | `TestMCPConcurrentAccess` |
| JSON-RPC protocol errors | — | N/A this layer | UNKNOWN | management surface only |
| No DELETE/disconnect | remove server | re-POST replaces | RISKY | `http_mcp.go:107-109` |
| No tenant scoping | any tenant sees all | tenant-scoped/documented | RISKY | `http.go:302`; connectedMCPServer no TenantID |
| GET /healthz | liveness | 200 no-auth | OK | `http.go:186,350-352`; `TestAuthMiddleware_Healthz` |
| /healthz degraded | DB/cron/relay down | reflect health; instead always ok | RISKY | `http.go:350-352` (no dependency checks; CronClient.Health never called) |
| Cron CRUD/pause/resume | list/create/by-ID | 200/201/400/404 | OK | `http_cron.go:13-206`; `TestCron*` |
| Cron no tenant scoping | any tenant | tenant-scoped; instead global | BROKEN | `tools.CronJob` no TenantID; no checkTenantOwnership |
| Recipes list/get/schema | read-only | 200/404 | OK | `http_recipes.go:33-112`; `TestRecipe*` |
| Networks list/get/start | — | scope-checked | OK | `http_networks.go:20-95`; `TestHandleNetworkRoutes` |
| Networks unknown/malformed | 404/400 | untested edges | RISKY | `http_networks.go:62-66,82-85` |
| Networks no tenant scoping | any tenant | tenant-scoped; instead global | BROKEN | workflows.Run no TenantID |
| Sourcegraph search | POST /v1/search/code | scope-checked, 501 | OK (mechanics) | `http_sourcegraph.go:21-116` |
| Sourcegraph edges | empty query/limit/upstream | 400/clamp/502 | UNKNOWN | no test file found — zero coverage |
| Models/Providers list | GET | 200 | OK | `http_catalog.go:46-255`; `TestModelsEndpoint*`, `TestProvidersEndpoint*` |
| PUT /providers/{name}/key | admin secret-set | 204/400/501 | OK (mechanics)/RISKY | `http_catalog.go:85-118`; no test |
| POST /v1/summarize | summarize | 200/400/503 | UNKNOWN | `http_catalog.go:122-162`; no test found |
| GET/PUT /runs/{id}/todos | get/set | scope-checked, 501 | OK (mechanics) | `http_todos.go:12-57`; `TestTodos*` |
| Todos cross-tenant | tenant B reads A's | 404; instead full access (only run sub-route missing authorizeRun) | BROKEN | `http_runs.go:271-286` vs 328,357,385,406,450,473,515,559,633,663,684 |
| Agents prompt/skill exec | POST /v1/agents | 200/400/404/408/501 | OK | `http_agents.go:78-200`; `TestAgentsEndpoint_*` |
| Agents tenant scoping | — | N/A stateless | OK-by-design | writes nothing to runStore |
