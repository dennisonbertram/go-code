# System Log

Use this file to document systems, interfaces, and interactions as they are built.

## 2026-06-28 (Config-Driven Lifecycle Hooks — Epic #737)

- System/component: `internal/hooks` (schema, loader, adapters, trust store, builder), `internal/config` `[hooks]` section, `cmd/harnessd` bootstrap wiring, `internal/server` `GET /v1/hooks`, `cmd/harnesscli` `hooks` subcommand + TUI `/hooks`.
- Responsibilities:
  - `internal/hooks` owns the hook-file JSON schema, discovery (`Load`/`LoadWithOptions`), the JSON wire protocol (`wire.go`, shared verbatim by both transports), command/HTTP adapters onto the existing `harness` hook interfaces, the content-hash trust store, and the def→adapter `Build` plus startup `Summary`.
  - `internal/config` owns `[hooks] enabled/dirs` via the existing rawLayer merge.
  - `cmd/harnessd` owns startup-time loading (trust-aware), adapter registration onto existing `RunnerConfig` slices (after compiled-in plugins), and startup logging; it keeps no hook logic of its own.
  - The runner owns all failure policy (`HookFailureMode`) and hook event emission; adapters only return decisions/errors.
  - `internal/server` serves the startup summary read-only; `harnesscli hooks trust|revoke|list` owns trust management offline; the TUI renders server truth.
- Inputs/outputs:
  - Input: `*.json` hook files in `~/.harness/hooks/` (implicit trust) and `<workspace>/.harness/hooks/` + `[hooks] dirs` (trust-required); stdin/stdout JSON for command hooks; POST/response JSON for HTTP hooks.
  - Output: hook decisions (allow/deny/block + reason, modified args/results) into the runner's existing hook loops; structured startup logs; `{"hooks": [...], "skipped": [...]}` listing.
- Dependencies: stdlib only in `internal/hooks` (`os/exec`, `net/http`, `crypto/sha256`); `internal/harness` interfaces unchanged; trust store at `~/.harness/hooks-trust.json` (never the project tree).
- Failure modes:
  - Hook process/HTTP failure → adapter error → runner `HookFailureMode` (fail_closed default: deny/abort; fail_open: continue).
  - Hung hook → per-hook timeout kills the whole process group / aborts the request.
  - Untrusted or modified project hook file → skipped with structured reason (`untrusted` / `modified_since_trusted`), surfaced in startup logs and `/v1/hooks`.
  - Unreadable trust store → fail closed: project hooks disabled, startup continues.
  - Oversized hook output → 1 MiB cap fails the hook call.

## 2026-06-26 (Terminal-Bench Artifact Boundary)

- System/component: `benchmarks/terminal_bench/agent.py`, `scripts/run-terminal-bench.sh`, and `scripts/terminal_bench_artifacts.py`.
- Responsibilities:
  - The Terminal-Bench adapter owns harness execution and emits harness-grounded facts only: run record, run summary, telemetry, and logs.
  - Terminal-Bench owns task oracle results: `is_resolved` and `parser_results`.
  - The postprocessor owns the merge boundary, schema validation, failure classification, baseline comparison summary, and report generation.
- Inputs/outputs:
  - Input: Terminal-Bench `results.json` plus per-trial `benchmark_result.json`.
  - Output: schema-validated `results.jsonl`, `summary.json`, `run-env.json`, and `report.md`.
- Dependencies:
  - `tb` or `uv tool run --python 3.12 terminal-bench` must be runnable.
  - Docker and tmux must be available for real Terminal-Bench campaigns.
  - Fake-provider smoke mode requires `HARNESS_PROVIDER=fake` and `HARNESS_FAKE_TURNS`.
- Failure modes:
  - Missing adapter artifacts become synthetic `infra_error` rows rather than silently disappearing.
  - Harness/tool/provider/workspace failures are classified from run status and error messages but do not override Terminal-Bench oracle truth.
- Operational notes:
  - `is_resolved` must never be written by the adapter.
  - `baseline.json` is authoritative for the smoke tier as of the accepted 2026-06-27 real-provider campaign at `.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42`.
  - The accepted campaign preserved raw `results.json`, merged `results.jsonl`, `run-env.json`, `summary.json`, `report.md`, task `commands.txt`, task pane logs, per-task `benchmark_result.json`, per-task `harness_telemetry.json`, and per-task `agent-logs/harnessd.log`.
  - Adapter credential propagation uses copied env files so Terminal-Bench command artifacts do not contain raw provider keys.
  - Missing adapter artifacts still fail baseline promotion, because cost, steps, tool calls, final harness status, and replay data would be synthetic or unavailable.
  - The current smoke baseline records `cost_status=unpriced_model`; cost-sensitive gates require a pricing-catalog update for `gpt-5-mini` or a rerun on a priced model.

## 2026-04-05 (Harnessd Runtime Composition Boundary)

- System/component: `cmd/harnessd/main.go` and `cmd/harnessd/runtime_container.go`.
- Responsibilities:
  - `runMCPStdio(...)` remains the public stdio entrypoint but now delegates catalog/server assembly to `buildMCPStdioRuntime(...)`.
  - `runWithSignals(...)` remains the public HTTP entrypoint but now delegates runner/subagent/server assembly to `buildHTTPRuntime(...)`.
  - The runtime helpers own internal composition only; they do not change route, config, or runner semantics.
- Inputs/outputs:
  - Input: already-resolved workspace/config/provider/tool-registry/bootstrap dependencies from `main.go`.
  - Output: assembled MCP stdio runtime or HTTP runtime objects with the same startup/shutdown behavior as before.
- Dependencies:
  - Existing bootstrap helpers still own catalog, cron, persistence, trigger, and server-option subassembly.
  - `buildHTTPRuntime(...)` depends on the existing runner, subagent manager, and server option contracts rather than inventing a new runtime subsystem package.
- Failure modes:
  - MCP tool-catalog or stdio-server creation errors still fail startup immediately.
  - Subagent manager creation errors still fail HTTP startup before listening begins.
- Operational notes:
  - This is a stage-1 internal refactor only.
  - The broader orchestration runtime, checkpoints, workflows, memory layering, and agent networks remain planned work documented in stage specs, not implemented behavior.

## 2026-03-28 (Product Module vs Playground Boundary)

- System/component: repo root, `playground/`, and `internal/quality/repostructure`.
- Responsibilities:
  - The main module root now acts as a navigation boundary for first-class product directories and repo metadata only.
  - `playground/` owns exploratory, training, and snippet-style Go code behind its own `go.mod`.
  - `internal/quality/repostructure` enforces that the root stays free of Go source and that `playground/` remains isolated.
- Inputs/outputs:
  - Input: contributor file placement decisions for new snippets or experiments.
  - Output: deterministic repo structure plus focused product-module verification that excludes playground code.
- Dependencies:
  - `go test ./...` in the main module depends on the root staying free of product-unrelated Go files.
  - Playground verification, when desired, runs from inside `playground/`.
- Failure modes:
  - If new Go files are added at the root, the structure guard test fails.
  - If `playground/` loses its own module boundary, product verification can inherit snippet-package failures again.
- Operational notes:
  - This is a structural separation only; it does not impose product-quality expectations on all playground snippets.
  - Contributors should place future experiments in `playground/` rather than at the repo root.

## 2026-03-25 (Runner Step Engine Boundary)

- System/component: `internal/harness/runner.go` and `internal/harness/runner_step_engine.go`.
- Responsibilities:
  - `Runner.runStepEngine(...)` owns the public boundary and delegates execution to an internal `stepEngine` helper.
  - `stepEngine` owns the per-step LLM/tool loop, including steering drain timing, hook application, tool execution, accounting, memory observation, compaction, and terminal step completion.
- Inputs/outputs:
  - Input: preflighted run execution state (`runPreflightResult`), run request metadata, max-step budget, fork depth, and approval policy.
  - Output: unchanged runner events, message mutations, tool side effects, run completion/failure transitions, and accounting snapshots.
- Dependencies:
  - `Runner` remains the authority for run state, event emission, tool registry access, and persistence/memory helpers.
  - `stepEngine` depends on those runner-owned APIs rather than reaching into external packages directly.
- Failure modes:
  - Context cancellation still terminates the run through the existing `cancelledRun(...)` path.
  - Hook/tool/provider failures still route through the same `failRun(...)` and approval/wait-state handling.
- Operational notes:
  - This extraction is intentionally behavior-preserving; it narrows ownership without changing event or transport contracts.
  - Step-boundary ordering is now pinned directly by `runner_step_engine_test.go`.

## 2026-03-25 (Run Persistence Ownership Boundary)

- System/component: `internal/harness/runner.go`, `internal/server/http.go`, and `internal/server/http_external_trigger.go`.
- Responsibilities:
  - The runner owns initial run-record persistence for both `StartRun(...)` and `ContinueRun(...)`.
  - HTTP transports return run IDs/status and rely on the runner’s shared store wiring rather than duplicating `CreateRun`.
- Inputs/outputs:
  - Input: transport requests that create runs through direct `/v1/runs` or external-trigger `start`/`continue`.
  - Output: exactly one `CreateRun` attempt per logical new run record, followed by the existing non-fatal update path as the run progresses.
- Dependencies:
  - A shared `store.Store` must be wired into the runner when persistence is desired.
  - The server may still read from the store for historical `GET /v1/runs` and list surfaces.
- Failure modes:
  - If the shared store is absent from the runner, the server no longer compensates by inserting the run record itself.
  - Store create failures remain non-fatal where the runner already treats persistence as best-effort.
- Operational notes:
  - This makes the runner/domain layer the single persistence authority for new run records.
  - External-trigger flows now match the same ownership rule as direct runner-driven continuation.

## 2026-03-25 (Forked Child-Run Failure Contract)

- System/component: `/v1/agents` forked execution plus fork-context skill tools in `internal/server/http_agents.go`, `internal/harness/tools/skill.go`, and `internal/harness/tools/core/skill.go`.
- Responsibilities:
  - Treat `ForkResult.Error` as authoritative terminal child-run failure information even when the transport call returned a nil Go error.
  - Preserve the existing `Summary`-then-`Output` success rendering for healthy forked runs.
- Inputs/outputs:
  - Input: `RunForkedSkill(...)` responses containing `Output`, `Summary`, and optional terminal failure text in `Error`.
  - Output: HTTP execution errors or tool-call failures when `Error` is populated; successful response payloads only when the child run actually succeeded.
- Dependencies:
  - `/v1/agents` uses a local result guard because the server package cannot import the harness tools package without creating the wrong dependency shape.
  - Tool-layer callers share `ForkResultExecutionError(...)` in `internal/harness/tools/fork_result.go`.
- Failure modes:
  - If a child run fails normally and returns `ForkResult.Error`, callers now fail fast instead of reporting `status: completed`.
  - If the fork transport itself fails, the existing Go `error` path remains authoritative.
- Operational notes:
  - This change is behavior-preserving for successful forked runs.
  - Fallback `RunPrompt(...)` paths are unchanged in this pass.

## 2026-03-18 (Runner Event Ledger Ordering Contract)

- System/component: `internal/harness/runner.go`
- Responsibilities:
  - Treat `emit()` as the canonical per-run event ledger writer.
  - Mirror that ledger to the rollout recorder without reordering relative to assigned `Seq`.
  - Preserve `state.messages` as the source of truth across compaction and step execution.
- Inputs/outputs:
  - Input: concurrently emitted runner events carrying pre-assigned `Seq` values.
  - Output: in-memory `state.events`, subscriber fanout, and JSONL rollout lines in the same logical order.
- Dependencies:
  - `r.mu` for canonical event sequencing.
  - `compactMu` for message replacement serialization.
  - `copyMessages` / payload deep-clone helpers for ownership isolation.
- Failure modes:
  - If the recorder channel overflows, the dropped event is represented by `recorder.drop_detected` at the same `Seq`.
  - Recorder write panics are isolated from the run loop, but the in-memory ledger remains canonical.
- Operational notes:
  - The recorder goroutine buffers out-of-order arrivals and flushes only contiguous `Seq` values, so file order matches logical event order.
  - Existing compaction tests remain the guardrail for `state.messages` source-of-truth behavior.

## 2026-03-18 (Provider/Model Impact Mapping Workflow)

- System/component: planning and worktree workflow docs (`AGENTS.md`, `docs/plans/PLAN_TEMPLATE.md`, `docs/runbooks/worktree-flow.md`, `docs/runbooks/provider-model-impact-mapping.md`).
- Responsibilities:
  - Require provider/model flow work to map cross-surface impact before implementation begins.
  - Keep the required surfaces explicit: config, server API, TUI state, regression tests.
  - Make missing sections visible as process warnings instead of silent omissions.
- Inputs/outputs:
  - Input: planned feature or bugfix that changes provider/model selection, routing, API-key handling, model catalogs, or provider plumbing.
  - Output: task-specific impact map in `docs/plans/` linked from the task plan.
- Dependencies:
  - Contributor adherence to the documented planning workflow.
  - Existing plan and worktree runbooks as the entry points for implementation.
- Failure modes:
  - If the impact map is skipped, adjacent integration surfaces may remain under-scoped until follow-up fixes are needed.
  - If headings are left blank, reviewers lack a clear signal about whether the surface was checked.
- Operational notes:
  - This is process-guided enforcement only in the current pass.
  - Unaffected surfaces must be documented as `None` with rationale rather than left blank.

## 2026-03-25 (Hybrid Model Discovery Path)

- System/component: `internal/provider/catalog/discovery.go`, `internal/provider/catalog/registry.go`, `internal/server/http.go`, `cmd/harnessd/main.go`.
- Responsibilities:
  - Fetch live OpenRouter model ids and names on demand.
  - Cache discovery results in memory with a TTL.
  - Merge live OpenRouter results with static catalog metadata for runtime routing and `GET /v1/models`.
  - Preserve the static catalog as the baseline behavior for non-OpenRouter providers.
- Inputs/outputs:
  - Input: static provider/model catalog plus `GET https://openrouter.ai/api/v1/models` responses.
  - Output: merged provider resolution decisions and merged `/v1/models` response rows.
- Dependencies:
  - The loaded model catalog must contain an `openrouter` provider entry before live discovery is enabled.
  - `ProviderRegistry` remains the central provider-resolution surface for server/runtime callers.
- Failure modes:
  - Live fetch failure returns stale cached data when present.
  - If there is no cache, callers fall back to the static catalog view.
  - Startup never depends on a successful discovery request.
- Operational notes:
  - Static metadata remains authoritative on overlap, especially aliases, pricing, and default model attributes.
  - OpenRouter-only live models are surfaced with minimal metadata when no static overlay exists.

## 2026-03-05 (Provider Token Streaming)

- System/component: `internal/provider/openai/client.go` + `internal/harness/runner.go`.
- Responsibilities:
  - Consume streamed OpenAI chat completion chunks in real time.
  - Reassemble assistant text and tool-call arguments into the existing final completion shape.
  - Emit incremental SSE events for client-side progressive rendering.
- Inputs/outputs:
  - Input: streaming `/v1/chat/completions` SSE chunks with `choices[].delta` content/tool-call fields and optional usage.
  - Output: `assistant.message.delta` and `tool.call.delta` events during a turn, followed by the existing final turn/tool events.
- Dependencies:
  - OpenAI chat completions streaming semantics.
  - Existing runner event fanout/subscriber model.
- Failure modes:
  - Malformed stream chunks fail the run via provider error propagation.
  - Invalid streamed tool-call indexes are rejected before tool execution.
  - If the provider stream ends before `[DONE]`, the turn fails explicitly.
- Operational notes:
  - Tool execution still waits for fully assembled tool-call arguments.
  - Existing REST endpoints remain unchanged; only the event taxonomy expands.

## 2026-03-04

- System state: foundational workflow and documentation system only.
- Notable interfaces:
  - `AGENTS.md` defines operational policy.
  - `docs/runbooks/*` define execution playbooks.
  - `scripts/verify-and-merge.sh` operationalizes test-gated merges.

## 2026-03-04 (OpenAI Harness POC)

- System/component: `cmd/harnessd` + `internal/harness` + `internal/provider/openai` + `internal/server`.
- Responsibilities:
  - Accept run requests and execute deterministic LLM/tool loop.
  - Expose run status and event stream for external clients (GUI/TUI).
  - Execute bounded workspace tools for coding-oriented actions.
- Inputs/outputs:
  - Input: HTTP JSON request (`POST /v1/runs`), OpenAI API responses, tool arguments.
  - Output: run state (`GET /v1/runs/{runID}`), SSE lifecycle events (`/events`), tool result envelopes back to model.
- Dependencies:
  - OpenAI API (`/v1/chat/completions`) via `OPENAI_API_KEY`.
  - Local Go toolchain for `run_go_test`.
- Failure modes:
  - Provider request failures or malformed model outputs result in `run.failed`.
  - Unknown tool/tool argument errors are returned as tool-output error payloads to continue loop.
  - Slow SSE clients may miss live events but can retrieve persisted event history for the run.
- Operational notes:
  - Runtime state is in-memory only.
  - `HARNESS_MAX_STEPS` bounds loop depth.
  - Tool execution is bounded and event-emitting per run step.

## 2026-03-04 (Toolset Interface Revision)

- System/component: `internal/harness/tools_default.go`.
- Responsibilities:
  - Provide standardized coding tool interface: `read`, `write`, `edit`, `bash`.
  - Enforce workspace path boundaries for file operations.
  - Execute bounded shell commands for command-line workflows.
- Inputs/outputs:
  - Input: structured JSON arguments from model tool calls.
  - Output: JSON result envelopes (`content`, `bytes_written`, `replacements`, `exit_code`, etc.).
- Dependencies:
  - Local filesystem permissions.
  - `/bin/bash` availability for `bash` tool execution.
- Failure modes:
  - `edit` fails when `old_text` cannot be matched.
  - `bash` rejects commands matching danger deny-list patterns.
  - Path traversal attempts fail before filesystem access.
- Operational notes:
  - `bash` command execution remains timeout-bounded and workspace-rooted.
  - Deny-list guardrails are heuristic and should be reviewed before production exposure.

## 2026-03-04 (Entrypoint Testability and Coverage)

- System/component: `cmd/harnessd/main.go` testability boundary.
- Responsibilities:
  - Keep `main` as process entrypoint while allowing deterministic tests for startup/exit behavior.
  - Preserve server startup/shutdown behavior with signal-driven termination.
- Inputs/outputs:
  - Input: environment variables + signal channel.
  - Output: process exit behavior in `main`, error returns from `run`/`runWithSignals`.
- Dependencies:
  - OpenAI provider construction callback.
  - HTTP server lifecycle (`ListenAndServe`, `Shutdown`).
- Failure modes:
  - Missing API key/provider construction failure now return explicit errors through `runWithSignals`.
  - Server startup fatal errors surface through returned error channel.
- Operational notes:
  - Added lightweight test hooks (`runMain`, `exitFunc`, `runWithSignalsFunc`) to isolate process-level behavior in unit tests.

## 2026-03-05 (Regression Quality Gate System)

- System/component: `scripts/test-regression.sh` + `cmd/coveragegate` + `internal/quality/coveragegate`.
- Responsibilities:
  - Execute standard regression suite locally and in CI.
  - Enforce minimum total statement coverage threshold.
  - Enforce non-zero function coverage across codebase.
- Inputs/outputs:
  - Input: coverage profile (`coverage.out`), configured minimum threshold (`MIN_TOTAL_COVERAGE`).
  - Output: pass/fail exit code and gate summary (`PASS` with total and zero-function count).
- Dependencies:
  - Go toolchain (`go test`, `go tool cover`).
  - GitHub Actions runner for CI execution.
- Failure modes:
  - Missing/invalid coverage profile fails gate.
  - Any function at `0.0%` fails gate.
  - Total coverage below threshold fails gate.
- Operational notes:
  - Default threshold is `80.0%`, configurable via environment variable.
  - Workflow file: `.github/workflows/test-regression.yml`.

## 2026-03-05 (Hook Pipeline + Tool Surface Expansion)

- System/component: `internal/harness/runner.go` hook pipeline and `internal/harness/tools_default.go` baseline tools.
- Responsibilities:
  - Execute hook chain before and after each provider turn.
  - Allow hook-driven request/response mutation or blocking.
  - Emit hook lifecycle events for UI/TUI observability.
  - Provide repository-oriented baseline tools for traversal, search, patching, and git inspection.
- Inputs/outputs:
  - Input: hook implementations in `RunnerConfig`, model tool-call arguments.
  - Output: updated requests/responses, run failures on blocked/error hooks (depending on mode), tool JSON outputs.
- Dependencies:
  - Local filesystem and git binary availability for `git_status`/`git_diff`.
  - Provider call loop in runner execution.
- Failure modes:
  - Hook fail-closed mode converts hook errors into `run.failed`.
  - Hook fail-open mode emits `hook.failed` and continues run.
  - Tool validation errors are returned as tool error payloads and surfaced in `tool.call.completed`.
- Operational notes:
  - Hook failure mode defaults to `fail_closed`.
  - Baseline tool names now include:
    - `ls`, `glob`, `grep`, `apply_patch`, `git_status`, `git_diff`
    - plus `read`, `write`, `edit`, `bash`.

## 2026-03-05 (CLI Test Client)

- System/component: `cmd/harnesscli`.
- Responsibilities:
  - Provide a minimal operator-facing CLI to test the harness API without manual `curl` orchestration.
  - Start a run and stream run events until terminal completion/failure.
- Inputs/outputs:
  - Input: command flags (`-base-url`, `-prompt`, `-model`, `-system-prompt`).
  - Output: run id and line-by-line event stream in terminal, plus terminal event summary.
- Dependencies:
  - Harness HTTP API endpoints (`POST /v1/runs`, `GET /v1/runs/{id}/events`).
  - JSON SSE event payload format from server.
- Failure modes:
  - Non-2xx create/stream responses return non-zero exit with API error context.
  - Invalid SSE `data` payload returns non-zero exit (`invalid sse data`).
  - Missing prompt returns immediate validation error.
- Operational notes:
  - Stream reader handles framed SSE blocks and stops explicitly on `run.completed` or `run.failed`.

## Entry Template

- Date:
- System/component:
- Responsibilities:
- Inputs/outputs:
- Dependencies:
- Failure modes:
- Operational notes:

## 2026-03-05 (Modular Tool Registry + Approval Modes)

- System/component: `internal/harness/tools` modular tool subsystem + compatibility wrapper in `internal/harness/tools_default.go`.
- Responsibilities:
  - Provide a catalog-based, pluggable tool registration flow.
  - Isolate each tool into its own implementation unit.
  - Apply approval policy middleware (`full_auto` or `permissions`) at tool handler boundary.
- Inputs/outputs:
  - Input: `BuildOptions` (workspace root, approval mode, integrations, HTTP client, sourcegraph config).
  - Output: sorted tool catalog with wrapped handlers and JSON result envelopes.
- Dependencies:
  - Optional external integrations for LSP (`gopls`), Sourcegraph HTTP endpoint/token, MCP registry, agent runner, and web fetcher.
- Failure modes:
  - In `permissions` mode, mutating/fetch/execute actions emit structured denial payloads when policy denies or errors.
  - Missing external dependencies produce deterministic runtime errors from the affected tool handlers.
  - Invalid tool JSON schema (for arrays without `items`) causes provider-side request rejection; fixed for current arrays.
- Operational notes:
  - Default server mode remains `full_auto` via `HARNESS_TOOL_APPROVAL_MODE` default.
  - Run-scoped context key (`run_id`) is now injected for tool execution to support run-local state (`todos`).

## 2026-03-05 (AskUserQuestion Pause/Resume Interface)

- System/component: `internal/harness/tools/ask_user_question.go`, `internal/harness/ask_user_broker.go`, `internal/harness/runner.go`, `internal/server/http.go`.
- Responsibilities:
  - Allow model turns to issue structured user clarification requests through `AskUserQuestion`.
  - Pause a run in `waiting_for_user` state until answers are submitted.
  - Resume execution after valid answers or fail the run on timeout.
- Inputs/outputs:
  - Input: tool args `{questions:[...]}` and API submissions `{answers:{...}}`.
  - Output: tool result JSON `{questions:[...], answers:{...}}`, run state transitions, and wait/resume events.
- Dependencies:
  - In-memory `AskUserQuestionBroker` shared by runner and tool layer.
  - HTTP input endpoints (`GET/POST /v1/runs/{id}/input`) for user answer submission.
- Failure modes:
  - Invalid tool question shape returns tool-call error payload (run continues unless timeout path).
  - Invalid submitted answers return `400 invalid_request` and keep question pending.
  - Missing pending input returns `409 no_pending_input`.
  - Timeout returns typed error and transitions run to `run.failed`.
- Operational notes:
  - `HARNESS_ASK_USER_TIMEOUT_SECONDS` controls per-question wait timeout (default 300s).
  - Event stream now includes `run.waiting_for_user` and `run.resumed` for UI/CLI orchestration.

## 2026-03-05 (Observational Memory Subsystem)

- System/component: `internal/observationalmemory` + runner/tool integration.
- Responsibilities:
  - Persist optional observational memory by `(tenant_id, conversation_id, agent_id)` scope.
  - Inject bounded memory snippets into model turns when enabled.
  - Execute ordered per-scope memory mutations in local coordinator mode.
  - Expose operator/model control via `observational_memory` tool.
- Inputs/outputs:
  - Input: run transcript snapshots, tool actions (`enable|disable|status|export|review|reflect_now`), environment memory settings.
  - Output: memory records/operations/markers in DB, SSE memory lifecycle events, optional export files.
- Dependencies:
  - SQLite store in v1 (`modernc.org/sqlite`).
  - Existing provider for observer/reflector model calls (tools disabled).
- Failure modes:
  - Observer/reflector failures emit `memory.observe.failed` and preserve run continuity.
  - Misconfigured memory store startup fails harness boot with explicit error.
  - Postgres mode currently returns explicit not-implemented errors.
- Operational notes:
  - `HARNESS_MEMORY_MODE=off|auto|local_coordinator`.
  - `auto` resolves to local coordinator behavior in v1.
  - Transcript is exposed to tools as read-only snapshot through context interfaces.

## 2026-03-05 (System Prompt Composition Pipeline)

- System/component: `internal/systemprompt` + runner integration in `internal/harness/runner.go`.
- Responsibilities:
  - Resolve static prompt layers by intent/model/extensions at run creation.
  - Inject per-turn runtime context as ephemeral system message.
  - Emit prompt-resolution telemetry events for clients.
- Inputs/outputs:
  - Input: `RunRequest` prompt fields (`agent_intent`, `task_context`, `prompt_profile`, `prompt_extensions`) and `prompts/catalog.yaml` assets.
  - Output: provider-facing system messages and run events (`prompt.resolved`, `prompt.warning`).
- Dependencies:
  - YAML catalog parser (`gopkg.in/yaml.v3`).
  - Prompt asset files under `prompts/`.
- Failure modes:
  - Invalid prompt catalog/paths fail harness startup.
  - Unknown intent/profile/behavior/talent fails `POST /v1/runs` as `invalid_request`.
  - Reserved `skills` field is ignored with warning event.
- Operational notes:
  - `system_prompt` request field bypasses prompt engine completely.
- Runtime context includes `run_started_at_utc`, `current_time_utc`, `elapsed_seconds`, `step`, and phase-1 cost placeholder.
- New config vars: `HARNESS_PROMPTS_DIR`, `HARNESS_DEFAULT_AGENT_INTENT`.

## 2026-03-05 (Usage and Cost Accounting Pipeline)

- System/component: `internal/provider/openai`, `internal/provider/pricing`, `internal/harness/runner`, `internal/systemprompt/runtime_context`.
- Responsibilities:
  - Normalize per-turn provider usage into harness accounting fields.
  - Compute per-turn USD cost when pricing metadata/catalog is available.
  - Accumulate run-level usage/cost totals and expose them to APIs/events.
  - Inject live accounting fields into runtime context on every turn.
- Inputs/outputs:
  - Input: provider completion response usage fields, optional explicit provider cost fields, optional pricing catalog JSON.
  - Output:
    - `usage.delta` event per completion turn.
    - `run.completed` / `run.failed` payload totals (`usage_totals`, `cost_totals`).
    - `GET /v1/runs/{id}` totals in run state.
    - runtime context fields (`prompt_tokens_total`, `cost_usd_total`, etc.).
- Dependencies:
  - Optional env-configured pricing catalog path: `HARNESS_PRICING_CATALOG_PATH`.
  - OpenAI usage response schema (`prompt_tokens`, `completion_tokens`, details objects).
- Failure modes:
  - Missing usage from provider does not fail run; accounting defaults to zero with `provider_unreported`.
  - Missing model pricing does not fail run; cost remains zero with `unpriced_model`.
  - Invalid pricing catalog path/content fails startup with explicit load error.
- Operational notes:
  - No bundled default price table is required; pricing is opt-in via catalog path.
  - `CostUSD` remains populated for backward compatibility while richer cost structure is also exposed.

## 2026-03-06 (Terminal Bench Smoke Benchmark System)

- System/component: `benchmarks/terminal_bench/agent.py` + `benchmarks/terminal_bench/tasks/*` + `scripts/run-terminal-bench.sh` + `.github/workflows/terminal-bench-periodic.yml`.
- Responsibilities:
  - Execute a small recurring benchmark against the real harness implementation.
  - Bridge Terminal Bench task execution to `harnessd` and `harnesscli`.
  - Produce reproducible per-task artifacts for regression triage.
- Inputs/outputs:
  - Input: Terminal Bench task instructions, current repository checkout, `OPENAI_API_KEY`, optional benchmark model/env overrides.
  - Output: Terminal Bench run artifacts in `.tmp/terminal-bench/`, uploaded workflow artifacts, and task pass/fail outcomes.
- Dependencies:
  - Terminal Bench CLI (`tb` or `uv tool run terminal-bench`).
  - Docker, tmux, and asciinema in task containers.
  - OpenAI-compatible API access for the harness under test.
- Failure modes:
  - Missing API key returns agent installation failure before task execution.
  - Harness startup failures surface through `/tmp/harnessd.log` in task logs.
  - Upstream Terminal Bench import-path or CLI contract changes can break the runner script.
- Operational notes:
  - The benchmark agent copies the current checkout into `/opt/go-agent-harness` inside each task container rather than cloning a remote branch.
  - The suite is intentionally small and suited for nightly smoke coverage, not merge gating.

## 2026-04-05 (Orchestration Runtime Stack)

- System/component: `internal/checkpoints`, `internal/workflows`, `internal/workingmemory`, `internal/networks`, plus `cmd/harnessd` runtime wiring.
- Responsibilities:
  - persist human-in-the-loop pause state through checkpoints
  - execute deterministic workflow graphs over runner/tool/checkpoint primitives
  - maintain explicit scoped working memory alongside observational memory
  - compile sequential network role definitions into workflow-backed execution
- Inputs/outputs:
  - Input: YAML definitions from `HARNESS_WORKFLOWS_DIR` and `HARNESS_NETWORKS_DIR`, checkpoint resume payloads, scoped working-memory tool writes.
  - Output:
    - checkpoint records in shared SQLite state
    - workflow run state, step state, and workflow SSE event streams
    - network launch surface backed by workflow runs
    - provider-facing prompt context with working-memory snippet ahead of observational-memory recall
- Dependencies:
  - shared SQLite runtime state database (same path family as runtime memory state)
  - existing runner/tool registry/subagent bootstrap
  - checkpoint-backed approval and ask-user brokers
- Failure modes:
  - invalid YAML definitions fail load during startup wiring
  - missing checkpoint/workflow/network services return explicit `not_implemented` from HTTP routes
  - workflow or network execution failures are persisted as terminal failed runs with step-level error text
- Operational notes:
  - workflow and network routes are now real but remain intentionally conservative in v1
  - sequential network execution is implemented; parallel fan-out remains deferred
# 2026-07-19 — Plugin bundle subsystem

- `internal/plugins` owns bundle validation, safe staged installation, persisted lifecycle state, marketplace index parsing, and discovery. `harnessd` loads enabled skills/commands and gates agents/MCP/hooks on trust.
