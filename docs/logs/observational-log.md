# Observational Log

Use this file for observations about system behavior without immediately prescribing code changes.

## 2026-06-28 (Config-Driven Hooks Epic #737)

- Security observation: the trust boundary that matters is directory ownership, not file content. Any directory a project can influence (its own `.harness/hooks/`, plus extra `[hooks] dirs` that could be named by a project-level config) must classify as trust-required; only the user-global dir can be implicit-trust. Classifying extra dirs as project-level closed an injection path where a malicious repo config names its own "trusted" directory.
- Testing observation: process-timeline tests (exec a script, wait for timeout, assert kill) have two independent flake axes — reaping lag (fix: poll for death) and startup lag under suite contention (fix: coordinate pid discovery before the timeout budget). Tests that assert on real PIDs need both handled explicitly.
- Testing observation: Go's `net/http` server only propagates client disconnect into `r.Context().Done()` after the handler consumes the request body; a timeout-test handler that blocks without reading the body hangs until its backstop, which looked like a 30s "slow suite" but was actually a protocol-semantics bug in the test.
- API-shape observation: computing the `/v1/hooks` listing once at startup (rather than re-discovering per request) guarantees the listing can never disagree with what the runner actually registered — the summary is the registration record, not a second query of the filesystem.

## 2026-06-26

- Eval observation: the useful boundary is adapter facts versus oracle facts. The harness can report run status, tokens, cost, tools, and logs, but Terminal-Bench must remain the only source for task pass/fail.
- Baseline observation: the existing `baseline.json` still reads as sample data until a green real-provider campaign records full provenance; accepting it without a live run would make future regressions misleading.
- CI observation: a fake-provider preflight and postprocessing smoke can cover most artifact contract regressions without requiring Docker or paid model calls in pull requests.
- Real-provider smoke observation: the first 2026-06-27 smoke correctly identified an adapter/client stream parsing defect (`harnesscli` rejected SSE keepalive comments), and the accepted rerun proved the fix by producing per-task benchmark and telemetry artifacts for all seven tasks.
- Artifact observation: command transcripts can become secret-bearing artifacts if provider credentials are placed inline in tmux commands; using copied env files keeps `commands.txt` useful without exposing key values.
- Cost observation: the accepted baseline is operationally real but not priced for dollars yet because `gpt-5-mini` is absent from `catalog/pricing.json`; cost gates should treat `cost_status=unpriced_model` as an explicit caveat, not as free execution.

## 2026-04-05

- Process observation: separating umbrella plans, stage specs, implementation logs, and public docs makes it much harder for “planned” orchestration routes or features to leak into operator-facing documentation by accident.
- Refactor observation: `cmd/harnessd` already had enough bootstrap seams that the first runtime-container step could stay inside the package and remain behavior-preserving instead of forcing a broad new `internal/runtime` package immediately.
- Testing observation: direct helper tests for runtime assembly are a useful complement to the existing full-entrypoint startup tests because they pin the extraction seam without weakening the higher-level behavior contract.

## 2026-03-29

- Concurrency observation: training data-structure exercises that try to be clever with fine-grained locking can become less correct than a coarse RW lock when the tests care about determinism more than throughput.
- Testing observation: parent tests that wait on `t.Parallel()` subtests can deadlock because the subtests are scheduled only after the parent returns.
- Matching observation: for these training regex packages, direct AST-based full-string matching was easier to reason about and align with the test expectations than repairing the existing buggy NFA execution paths.

## 2026-03-28

- Repository-shape observation: when experimental snippets live in the module root, they blur the entrypoint for new contributors and can break `go test ./...` before product packages are even evaluated.
- Boundary observation: a separate `playground/` module is a clean way to preserve exploratory code without making product verification depend on training-example correctness.

## 2026-03-18

- Runner observation: concurrent non-terminal emits can reach the recorder channel in a different order than their assigned `Seq` even when the code is race-clean.
- Recorder observation: flushing JSONL by contiguous `Seq` restores file-line ordering to match the canonical in-memory event ledger.
- Message-state observation: the durable contract is still `state.messages` as the only source of truth; step-local snapshots are safe only when reloaded at step boundaries.
- Process observation: recent provider/model feature history landed the core behavior first and then needed follow-up fixes in adjacent surfaces such as gateway config, TUI routing/navigation, API key management, and server `ProviderRegistry` wiring.
- Process observation: making the integration surface explicit under four headings (`config`, `server API`, `TUI state`, `regression tests`) is a lightweight way to expose missing follow-through before merge.

## 2026-03-25

- Step-engine observation: the cleanest extraction seam is the existing `runStepEngine(...)` boundary itself, with `Runner` continuing to own lifecycle/state APIs while a dedicated helper type owns the loop internals.
- Step-boundary observation: steering is drained after `run.step.started` and before the next `llm.turn.requested`, and that ordering is stable enough to pin directly in a focused harness test.
- Persistence observation: before this fix, both direct `/v1/runs` and external-trigger start/continue paths attempted `CreateRun` twice when the server and runner shared the same store.
- Ownership observation: the cleanest contract is runner-owned initial persistence with the server staying read/transport-focused.
- Discovery observation: OpenRouter is the current provider where live model discovery materially reduces backend drift from the real model surface.
- Safety observation: keeping live discovery additive over the static catalog preserves deterministic pricing and alias behavior while still exposing dynamic OpenRouter slugs.
- Failure-mode observation: a TTL cache with stale-cache fallback is enough to keep `/v1/models` and runtime routing from degenerating into fetch-on-every-request behavior.

## 2026-04-05

- Checkpoint observation: the existing approval and ask-user seams were already broker-shaped, which made it practical to replace in-memory maps with a persisted checkpoint service without changing the runner’s public pause/resume API.
- Workflow observation: the harness runner and registry were already separated enough that a workflow layer could stay above them and treat `run` and `tool` steps as orchestration primitives instead of rewriting the step loop.
- Memory-layer observation: explicit working memory works best as a small scoped key/value surface injected ahead of observational recall, not as another transcript mutation mechanism.
- Network observation: compiling v1 networks into workflow-backed sequential run steps kept the new role topology feature from turning into a second orchestration engine.

## 2026-03-05

- Streaming observation: the harness can now surface provider text/tool-call deltas before `llm.turn.completed`, which means clients no longer need to wait for the entire turn to render assistant output.
- Streaming observation: OpenAI streamed tool-call arguments arrive in partial chunks and must be assembled by tool-call `index` before execution.

## 2026-03-04

- Baseline observation: repository initialized with no implementation code yet.
- Harness observation: a run started through `POST /v1/runs` can be consumed via SSE from `GET /v1/runs/{runID}/events` even if the subscriber attaches after initial events, because event history is replayed before live streaming.
- Tool safety observation: default file tools reject workspace-escape paths and the test runner tool bounds execution with a timeout.
- Toolset observation: replacing tools with `read/write/edit/bash` preserved harness loop behavior and SSE outputs; only tool-call semantics changed.
- Bash observation: deny-list command guardrails reject clearly dangerous inputs (for example `rm -rf /`) while still allowing bounded command execution in workspace context.
- Coverage observation: after adding targeted tests for entrypoint, runner failure paths, and HTTP error handlers, all functions now show non-zero execution coverage in `go tool cover -func`.
- Regression observation: automated regression script now catches both total coverage drops and per-function `0.0%` coverage regressions before merge.
- CI observation: regression workflow is runnable in GitHub Actions without extra repository-specific setup beyond Go toolchain availability.
- Hook observation: hook events are emitted around LLM turns and can be consumed by clients for pre/post policy visibility (`hook.started`, `hook.completed`, `hook.failed`).
- Baseline tools observation: `ls`, `glob`, `grep`, `apply_patch`, `git_status`, and `git_diff` are callable through the same tool loop and appear with full lifecycle events.
- Live-run observation: model-driven `apply_patch` replaced the first matching occurrence in the file (title) when `find` was broad, demonstrating deterministic but occurrence-sensitive patch behavior.
- CLI observation: the new `harnesscli` client can attach to the existing SSE API and reliably terminate on `run.completed`/`run.failed` without hanging, making it a practical test harness for manual integration checks.

## Entry Template

- Date:
- Environment/context:
- Observation:
- Evidence:
- Hypothesis:
- Suggested follow-up:
- Modular-tooling observation: moving tools into `internal/harness/tools/` preserved registry-driven execution semantics while making per-tool changes isolated and easier to test.
- Policy observation: `permissions` mode cleanly blocks mutating/fetch/execute actions with structured `permission_denied`/`permission_error` payloads, while `full_auto` remains fast-path default.
- Live schema observation: OpenAI tool schema validation rejects array properties without `items`; adding explicit `items` on `apply_patch.edits` and `todos.todos` resolved request-time failures.
- Live-run observation: after schema fix, a tmux-hosted `gpt-5-nano` run completed successfully and exercised new `read` pagination/line metadata in event stream outputs.
- AskUserQuestion observation: a run now exposes a deterministic paused state (`waiting_for_user`) with explicit `run.waiting_for_user` and `run.resumed` events, enabling frontend clients to render input prompts without polling ambiguous tool state.
- Broker observation: invalid answer submissions no longer break run execution; they return `400` while preserving pending question state until a valid submission arrives.
- Timeout observation: when no answer is submitted before `HARNESS_ASK_USER_TIMEOUT_SECONDS`, the run fails immediately after the AskUserQuestion tool call with a timeout error, preventing indefinite stalled runs.

- Observational-memory observation: run-level transcript snapshots now exist in runner state and can be consumed by tools through a read-only context interface, avoiding direct mutable message-array access from tools.
- Observational-memory observation: local mode uses SQLite WAL + per-scope in-process ordering, which keeps standalone behavior deterministic while preserving a migration path to external coordination.
- Observational-memory observation: model-backed observer updates are emitted as explicit `memory.observe.*` events, giving client UIs an auditable trace for automatic memory writes.
- Observational-memory observation: memory control is now explicit and reversible (`enable`/`disable`) through a first-class tool, keeping default execution behavior unchanged unless memory is activated.

- Prompt-system observation: static prompt composition is now deterministic and file-backed; section ordering remains stable across runs for the same intent/model/extensions input.
- Runtime-context observation: runtime metadata is injected every turn without transcript growth, so previous runtime snapshots do not accumulate across tool loops.
- Validation observation: invalid intent/profile/extension identifiers now fail run creation immediately, preventing silent prompt drift.
- Compatibility observation: explicit `system_prompt` requests continue to bypass prompt composition, preserving previous operator override behavior.
- Benchmark observation: a small private Terminal Bench suite is the right level of signal for this repo right now because it can exercise the real harness loop without turning paid benchmark runs into a pre-merge gate.
- Benchmark observation: copying the live checkout into each Terminal Bench task container avoids drift between benchmark code and the code under test, which is especially useful for local operator runs.
