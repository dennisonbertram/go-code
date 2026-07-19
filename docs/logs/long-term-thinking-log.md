# Long-Term Thinking Log

## 2026-06-28 (Config-Driven Lifecycle Hooks — Epic #737)

- Command intent: Implement epic #737 and all six child issues (#741, #744, #750, #755, #759, #763) in a dedicated worktree, landing config-driven shell/HTTP lifecycle hooks end to end, then open a PR.
- User intent: Let end users attach shell commands or HTTP calls to the four runner lifecycle events (PreMessage, PostMessage, PreToolUse, PostToolUse) — with PreToolUse deny support — without writing Go code, while keeping cloned-repo project hooks opt-in via explicit trust.
- Success definition:
  - `internal/hooks` package: hook-file schema, trust-aware loader with structured skip records, command + HTTP adapters implementing the four existing `internal/harness` hook interfaces unchanged, content-hash trust store in the user-global dir.
  - `[hooks]` TOML section in `internal/config` following the raw-layer merge pattern.
  - `harnessd` startup appends config-driven adapters to existing `RunnerConfig` hook slices after compiled-in plugins (conclusion-watcher pattern), with structured startup logs per loaded/skipped hook.
  - `harnesscli hooks trust|revoke|list` manages project-hook trust; `GET /v1/hooks` and TUI `/hooks` render the startup-computed loaded/skipped summary.
  - A command PreToolUse hook returning deny blocks the tool and the LLM sees the reason; hook errors honor `HookFailureMode` (both modes tested).
  - Strict TDD per slice; fast PR gate and `./scripts/test-regression.sh` green before PR.
- Non-goals: SessionStart/Stop events (no runner call sites), hook retries/auth/mTLS, interactive TUI trust flows, hook file hot-reload, hook sandboxing, message request/response mutation via config hooks.
- Guardrails/constraints:
  - No changes to the four hook interface signatures; no parallel hook system; adapters only return errors/decisions (failure policy stays in the runner).
  - Trust decisions keyed by (path, SHA-256 of content); store lives under `~/.harness/`, never the project tree.
  - Per-hook timeout bounds every subprocess/HTTP call; timeouts kill the child process.
  - Plan: `docs/plans/2026-06-28-config-driven-hooks-epic-737-plan.md`.
- Open questions: none blocking — SSE recon confirmed the runner already emits hook-name-attributed started/completed/failed events, so no new run-event types are needed for #759.
- Next verification step: implement slices in dependency order with failing tests first, then run `go test ./internal/... ./cmd/...` and `./scripts/test-regression.sh` before opening the PR.

## 2026-06-27 (Go-Authored Custom Workflows)

- Command intent: Implement custom workflows that agents can create, save, run, and monitor as Go source without restarting `harnessd`.
- User intent: Bring go-code closer to Claude-style custom workflows while keeping workflows transferable through skills and usable by parent agents through feedback.
- Success definition:
  - Agents can create validated Go workflow bundles through `create_workflow`.
  - Agents can run workflows through `run_workflow` and receive structured feedback events.
  - Workflow bundles are discovered from global, workspace, and skill directories.
  - Watched workflow and skill directories can reload discovered bundles without a `harnessd` restart.
  - `/v1/script-workflows` is backed by a real runtime in `harnessd`.
  - Focused tests cover creation, discovery, feedback, question events, child-process failure handling, subagent RPC forwarding, SSE history, and wiring.
- Non-goals:
  - Replacing the existing YAML `/v1/workflows` runtime.
  - Full Claude plugin parity for commands, hooks, agents, and MCP manifests in this slice.
- Guardrails/constraints:
  - Treat Go workflow code as trusted local automation, similar to script tools.
  - Keep generated workflow execution in a child process so `harnessd` stays alive.
- Solved struggle: Go plugins looked tempting for dynamic loading, but child-process binaries are safer for hot reload, cancellation, and repeated agent-generated code.
- Solved struggle: The embedded description regression failed because new markdown tool descriptions must be added to both explicit inventories in `embed_test.go`; the fix was to register `create_workflow` and `run_workflow` in both lists.
- Solved struggle: Directory discovery alone did not satisfy no-restart hot loading for manually saved bundles; the fix was to route the existing watcher through `scriptWorkflowServiceRef.Reload` for workflow dirs and skill dirs.
- Solved struggle: The full regression later failed the no-zero-coverage rule even though total coverage was above threshold; the fix was to add behavior tests for the new harnessd adapters, nested workflow/stderr/resume paths, and uncovered TUI editor helpers instead of weakening coveragegate.

## 2026-06-28 (Go Relay PR #689 Review Repair)

- Command intent: Fix the PR that implements the Go Relay epic by addressing the blocker review findings and pushing the repaired branch back to the existing PR.
- User intent: Salvage the useful Go Relay implementation without rebuilding from scratch, while making the branch safe enough to continue reviewing.
- Success definition:
  - Current `origin/main` merge conflicts are resolved.
  - Relay worker HTTP APIs enforce authenticated tenant ownership for list, register, get, update, delete, and heartbeat operations.
  - Placement routing rejects workers that lack required capability inventory, repo access, browser, Docker, tool, MCP, memory, secret, or output-surface requirements.
  - `harnessd` can enable Relay worker endpoints through a configured SQLite worker store instead of leaving the API test-only.
  - Operator run summaries redact non-local capability details based on selected worker location.
  - Focused regressions fail before implementation and pass afterward; touched package suites pass.
- Non-goals:
  - Rebuilding the Go Relay PR from scratch.
  - Implementing hosted Relay service, WebSocket transport, cloud provisioning, or a dashboard in this repair slice.
- Guardrails/constraints:
  - Keep direct local `go-code` behavior unchanged.
  - Preserve main-branch server hardening and replay safety changes while resolving conflicts.
  - Do not weaken tenant isolation or secret redaction to preserve prototype behavior.
- Next verification step: run the fast PR gate (`go test ./internal/... ./cmd/...`) and push the repaired PR branch if green.

## 2026-06-27 (Go Relay Multi-Location Control Plane — Epic #676 Implementation)

- Command intent: Implement the Go Relay epic (#676) and all 11 subissues as the multi-location product/control-plane layer around `go-code`.
- User intent: Route and compose coding work across registered local machines, worktrees, containers, cloud VMs, sandboxes, and connector-triggered workflows.
- Success definition:
  - Design document (#678) defines the Go Relay terminology, run contract schema, product boundary, and field ownership.
  - Worker registration (#679) with stable IDs, heartbeats, stale detection, tenant isolation, and CRUD HTTP API (`/v1/relay/workers`).
  - Capability inventory (#680) with tool, MCP server, memory, repo, secret, output surface, browser, and Docker types; capability-pack store; sanitization for display.
  - Deterministic placement routing (#681) with hard constraints (tenant, trust tier, location type, workspace modes), soft scoring (local-first, clean workspace, cloud preference, load), explainable placement records, and tie-breaking.
  - Run contract composition (#682) with trigger source, workspace target, capability pack, permissions, limits, output expectations, mobility class, metadata, and context hydration with truncation.
  - Local worker transport (#683) with session management (register/remove/reconnect), run dispatch, event bus (pub/sub), command queue (cancel/steer/approve), and active run tracking.
  - Cloud/sandbox worker pool (#684) with provider configuration, cloud placement validation, workspace-mode-to-location mapping, and cost/risk summaries.
  - Event/artifact persistence (#685) with placement records, event log (append/query with seq), artifact CRUD, and SQLite-backed storage.
  - Capability policy (#686) with tool gating by trust tier, secret reference leak detection, output surface source matching, MCP server secret gating, and filtered capability-pack generation.
  - Checkpointed task handoff (#687) with mobility classes (pinned/resumable/cloneable/ephemeral), handoff packages, target validation, lineage tracking, and non-portable state identification.
  - Operator UX (#688) with worker summaries, run summaries, placement explanations, sanitized capability views, artifact references, and duration formatting.
  - All 80+ relay tests pass; full project builds; server tests pass with no regressions.
  - New `internal/relay` package: 27 files, ~3,700 lines of production code + ~3,100 lines of test code.
- Non-goals:
  - Building a production hosted Relay service.
  - Full tunnel/transport protocol implementation (WebSocket vs. long-poll deferred).
  - Cloud VM provisioning automation.
  - Polished SaaS dashboard (API-first surface delivered).
- Guardrails/constraints:
  - `go-code` remains the execution runtime; Go Relay owns orchestration.
  - Local/private/dirty workspaces remain first-class; cloud is optional placement.
  - Deterministic, explainable routing before model-driven placement.
  - Current direct local `go-code` usage (TUI, single-shot, daemon mode) preserved.
  - Secret values never stored in capability records; only references.
- Next steps:
  - Wire transport to actual WebSocket or long-poll HTTP endpoint.
  - Implement cloud VM provisioning (Hetzner, sandbox) on placement.
  - Add HTTP endpoints for operator UX summaries.
  - Integration test: Slack-triggered task → compose → route → dispatch → event relay → artifact.
  - Document operator setup flows.

## 2026-06-27 (TUI-First Harness Completion Slice)

- Command intent: Continue the dirty workspace implementation of the TUI-first personal `go-code` harness plan after the first daily-command and reliability slice landed locally.
- User intent: Make `go-code` trustworthy as a daily terminal coding harness before adding broad web, cloud, or team surfaces.
- Success definition:
  - Finish the #644 reliability hardening slices and keep the regression gate green.
  - Replace guidance-only daily TUI run-control commands with useful list/cancel/replay/resume behavior.
  - Persist a searchable workflow recap for completed runs: goal, changed files, tests run, failure cause, fix pattern, useful commands, and next continuation prompt.
  - Expose a first-class self-improvement command that plans or runs the existing autoresearch/test loop and can score the repo with native checks.
  - Preserve `go-code`, single-shot prompts, daemon mode, and `harnesscli -prompt`.
- Non-goals:
  - Runtime rewrite, web UI, cloud/team features, or product onboarding polish.
  - Weakening the coverage gate to pass around missing tests.
- Guardrails/constraints:
  - Work with the dirty workspace without reverting existing changes.
  - Strict TDD for behavior changes and meaningful tests for coverage gaps.
  - Keep docs/logs/indexes current and do not commit unless explicitly asked.
- Next verification step: run `go test ./...`, `go test ./... -race`, and `./scripts/test-regression.sh` after the final documentation pass.

## 2026-06-26 (TUI-First Personal Harness Implementation)

- Command intent: Implement the TUI-first personal `go-code` harness plan by improving daily terminal ergonomics while beginning the reliability work that must make the baseline trustworthy.
- User intent: Turn `go-code` into a dependable personal coding harness that can be launched from any repository, controlled from the terminal, resumed/replayed/searched without doc spelunking, and hardened against long-session failure modes before broader product surfaces grow.
- Success definition:
  - Preserve existing `go-code`, `go-code "prompt"`, `go-code --server`, and `harnesscli -prompt` behavior.
  - Expose first-pass daily run-control commands through the installed wrapper and `harnesscli`: runs/list, show/status, cancel, continue, replay, and search.
  - Register expected daily TUI slash-command entry points and keep snapshot coverage current.
  - Add Conductor repository settings so workspaces can build and run the daemon consistently.
  - Start P0 reliability hardening with a failing-first regression for a concrete #644 child slice.
  - Keep changes narrow; do not rewrite the runtime or add broad cloud/team surfaces.
- Non-goals:
  - Completing all 15 reliability slices in one workspace change.
  - Replacing the existing TUI/session architecture.
  - Shipping web UI, public onboarding polish, or cloud/team control-plane features.
- Guardrails/constraints:
  - Strict TDD for behavior changes.
  - Keep public docs limited to implemented behavior.
  - Treat full-suite/race/regression gates as pending until the broader P0 merge path is executed.
- Next verification step: finish the remaining #644 slices in order, then run `go test ./...`, `go test ./... -race`, and `./scripts/test-regression.sh` before promoting the reliability epic.

## 2026-06-26 (Adapter-First Eval Harness)

- Command intent: Implement the adapter-first eval harness plan by making Terminal-Bench runs reproducible, schema-validated, explainable, and regression-reportable before introducing any native `go-code eval` surface.
- User intent: Turn `go-code` into a serious coding harness for Terminal-Bench-style evaluations where pass/fail, cost, logs, and failures can be trusted and compared over time.
- Success definition:
  - `scripts/run-terminal-bench.sh` has deterministic preflight for Docker, tmux, Terminal-Bench, model/provider configuration, and fake-provider key-free smoke mode.
  - The Terminal-Bench adapter writes per-trial harness facts (`benchmark_result.json`, telemetry, and logs) without inventing task pass/fail.
  - Postprocessing merges Terminal-Bench oracle results into schema-validated `results.jsonl`, writes campaign provenance, classifies failures, and generates an actionable report.
  - Fast CI covers the artifact merge/report/preflight contract without requiring Docker or paid model calls.
  - No new baseline is accepted until a green real-provider smoke campaign records git SHA, model, provider, Terminal-Bench version, task-set hash, concurrency, attempts, timeouts, and cost.
- Non-goals:
  - Adding a public native `go-code eval` command in this slice.
  - Expanding the task suite beyond the existing smoke tier in this slice.
  - Replacing sample baselines with real baselines without running a verified real-provider campaign.
- Guardrails/constraints:
  - Keep `is_resolved` and parser results external to the harness adapter.
  - Keep paid Terminal-Bench runs out of PR gates unless explicitly enabled.
  - Preserve Terminal-Bench as the primary operator interface.
- Verification update on 2026-06-27:
  - `scripts/test-regression.sh` now passes with `coveragegate: PASS (total=84.6%, min=80.0%, zero-functions=0)`.
  - Initial real-provider attempts exposed and fixed three adapter blockers: `harnesscli` treated SSE keepalive comments as fatal, task command logs exposed inline provider credentials, and adapter JSON fetches parsed tmux-wrapped output instead of raw container stdout.
  - The final real-provider smoke campaign ran under `.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42` with `provider=openai`, `model=gpt-5-mini`, Terminal-Bench `0.2.18`, dataset hash `31b29122bfa16205e6a66967fc444f5d46924a8ed9f39167cb27fc1e676d5457`, concurrency `1`, attempts `1`, and timeouts `1800/300`.
  - The final campaign passed 7/7 and produced raw `results.json`, merged `results.jsonl`, `run-env.json`, `summary.json`, per-task `benchmark_result.json`, per-task `harness_telemetry.json`, logs, and `report.md`.
  - `baseline.json` was promoted from the green campaign. Cost is recorded as `0.0` with `cost_status=unpriced_model` because `catalog/pricing.json` does not yet include `gpt-5-mini`.
- Next verification step: add catalog pricing for `gpt-5-mini` or run a priced model before making cost-sensitive gates stricter than the current explicit `unpriced_model` baseline.

## 2026-05-03 (Repository Rename and Public README Cleanup)

- Command intent: Rename the repository to `go-code` and make the public project presentation clear for first-time visitors.
- User intent: Move the harness closer to a distributable, installable tool that feels understandable from the README and project page.
- Success definition:
  - GitHub repository and Pages URLs use `go-code`.
  - README starts with a wide watercolor hero image and explains what the project is, how to install it, how to run it, and where the important code and docs live.
  - Distribution docs, Pages copy, and docs indexes use the new public name.
  - Tracked root scratch files are removed so the repository front door is cleaner.
- Non-goals:
  - Renaming the Go module path in this slice.
  - Building release archives or a Homebrew tap in this slice.
- Guardrails/constraints:
  - Keep installed command behavior unchanged.
  - Avoid broad import-path churn while the product rename lands.
- Next verification step: confirm the Pages workflow publishes the updated site at `https://dennisonbertram.github.io/go-code/`.

## 2026-05-03 (Repository Hygiene Cleanup)

- Command intent: Clean up root-level and generated repository clutter after the public rename.
- User intent: Make the repository feel presentable and easier to browse without losing useful training examples.
- Success definition:
  - Generated/local state is no longer tracked.
  - Root-level scratch and training outputs no longer crowd the project root.
  - Durable training examples live under `playground/training/`.
  - Incomplete examples/exercises remain available but do not poison stable product or playground test baselines.
  - `.gitignore` prevents the same clutter from coming back.
- Non-goals:
  - Moving product packages such as `cmd/`, `internal/`, `catalog/`, `prompts/`, or `scripts/`.
  - Rewriting benchmark harness imports or Python adapter layout.
- Guardrails/constraints:
  - Prefer mechanical moves and deletions over package refactors.
  - Keep main CLI/TUI tests green.
- Next verification step: audit whether `skills/` should remain bundled at the repository root or move behind a plugin/package boundary in a separate design slice.

## 2026-05-01 (User-Local Installer and Workspace-Aware TUI)

- Command intent: Turn the repo-local `go-code` wrapper into a practical installer so the harness can be used from normal projects without manual sudo copy steps.
- User intent: Make the harness feel like an installed development tool: easy to install, easy to put on PATH, and reliable when launched from any repository.
- Success definition:
  - `make install` no longer requires `/usr/local/bin` write permissions by default.
  - `scripts/install.sh` builds and installs `go-code`, `harnesscli`, and `harnessd` to a user-local bin directory with clear PATH guidance.
  - Runtime `prompts/` and `catalog/` assets are installed with the wrapper so the command works after being launched from another repository.
  - Optional system installs and uninstall are still available.
  - The installed TUI and single-shot modes both send the caller's workspace path to harnessd.
  - Installer syntax, install dry-run, install-to-temp-prefix, focused tests, broader CLI/TUI tests, and rebuilt binaries pass.
- Non-goals:
  - Packaging Homebrew, npm, or signed release artifacts in this slice.
  - Automatically editing shell startup files without an explicit `--add-to-path`.
- Guardrails/constraints:
  - Keep the default install path sudo-free.
  - Preserve `/usr/local` as an explicit opt-in for system installs.
  - Do not rely on the harness repo as the current working directory once the command is installed.
- Next verification step: run `./scripts/install.sh --add-to-path`, open a new shell, then launch `go-code --server` and `go-code` from another project directory.

## 2026-05-01 (Distribution Docs and GitHub Pages)

- Command intent: Document the distribution direction and create a GitHub Pages landing page for Go Agent Harness.
- User intent: Make the project feel closer to an installable product, with a clear public-facing page and a practical path from source install to real distribution.
- Success definition:
  - Public site source exists in a GitHub Pages-compatible folder.
  - A workflow can publish the site with GitHub Actions.
  - README explains the installed `go-code` path.
  - A runbook captures source install, release archives, Homebrew, and future single-binary packaging.
  - Documentation indexes and logs reflect the new docs.
- Non-goals:
  - Building release archives in this slice.
  - Creating a Homebrew tap in this slice.
  - Replacing the current multi-binary installer with a single binary immediately.
- Guardrails/constraints:
  - Keep Pages static and dependency-free.
  - Keep the default distribution path sudo-free.
  - Describe future packaging as roadmap, not implemented behavior.
- Next verification step: enable Pages in repository settings with GitHub Actions as the source, then run the `pages` workflow manually and check the published page.

- 2026-04-29
  - Command intent: Complete GitHub issue `#557` by preventing `TestContainerWorkspace_Provision_Success` from reusing the fixed Docker container name `workspace-test-provision` across test runs.
  - User intent: Make the Docker-backed workspace provision test rerunnable after normal failures or aborted runs without manual `docker rm` cleanup.
  - Success definition: the success test uses a unique, readable workspace/container ID per run, registers cleanup for successfully provisioned containers, has a regression test for ID uniqueness, and targeted workspace validation passes or reports an environment-specific Docker blocker.
  - Non-goals: redesigning production workspace container naming or changing non-container workspace behavior.
  - Guardrails/constraints: strict TDD, keep changes scoped, avoid unrelated dirty files, and do not move GitHub state beyond gates that cannot be satisfied locally.
  - Open questions: whether a non-sandboxed environment with Docker/image availability can exercise the Docker-backed success path without the local `bind :0` restriction.
  - Next verification step: rerun the workspace package and repo regression gate in an environment that permits loopback listeners, then publish the branch/PR and update the GitHub workpad.

- 2026-04-05
  - Command intent: implement the staged Mastra-style orchestration program with documentation-first guardrails and strict TDD so planned capabilities do not leak into public docs before they exist.
  - User intent: make the harness more orchestration-capable without losing trust in the docs, the existing runtime behavior, or the regression baseline.
  - Success definition: the repo has an umbrella orchestration plan plus stage-complete specs, the runbooks explicitly prohibit ghost features and require characterization before refactors, and stage 1 runtime-container work lands behind failing-first tests without changing current user-facing behavior.
  - Non-goals: shipping checkpoints, workflows, memory layering, and agent networks in this same slice.
  - Guardrails/constraints: keep `/v1/runs*` compatibility intact, do not add README claims for planned routes, and treat spec updates as mandatory when implementation scope changes.
  - Open questions: how far the runtime-container extraction should go before a new `internal/runtime` package becomes worth the churn.
  - Next verification step: land the documentation guardrails, add direct stage-1 assembly tests in `cmd/harnessd`, and rerun targeted startup tests after the extraction.

- 2026-04-01
  - Command intent: fix the remaining per-run sandbox gap so tool execution follows the current run or continuation permissions instead of the registry startup default.
  - User intent: establish a clear trust boundary where a continued conversation can intentionally change tools and permissions without leaking the prior session's runtime affordances.
  - Success definition: bash/job execution reads sandbox scope from the live run context, continuation runs can narrow or broaden sandbox permissions without rebuilding the registry, and regression tests prove the boundary at both run start and continuation time.

- 2026-03-29
  - Command intent: fix the remaining failing tests and hangs so the repository returns to a clean `go test ./...` baseline.
  - User intent: stop carrying forward known red packages after the structural cleanup and make the repo trustworthy again.
  - Success definition: the failing pubsub, skiplist, regex, and trie training packages are corrected and `go test ./...` exits cleanly.

- 2026-03-28
  - Command intent: clean up the repository so product code, experiments, and training snippets stop mixing at the module root.
  - User intent: make the repo feel cleaner, more modular, and easier to understand by giving each section a clearer purpose boundary.
  - Success definition: the module root no longer contains ad hoc Go source, experimental snippets live behind a dedicated `playground/` boundary, product verification no longer depends on playground code, and the structure is documented clearly.

- 2026-03-25
  - Command intent: make new pull requests move quickly instead of waiting on the full regression gate.
  - User intent: preserve meaningful protection while removing the slow GitHub check from the normal PR feedback loop.
  - Success definition: pull requests use a fast required test pass, while the full regression suite remains available on `main` and on a scheduled/manual path.

Purpose: keep durable intent and success criteria visible so agents can make good decisions without re-discovery.

Decision rule: when uncertain, default to `command intent` and `user intent` below.

## Entry Template

- Date:
- Command intent:
- User intent:
- Success definition:
- Non-goals:
- Guardrails/constraints:
- Open questions:
- Next verification step:

## 2026-03-25 (Issue #425 Step Engine Extraction)

- Command intent: Complete GitHub issue `#425` by extracting the core runner step loop into a focused internal step-engine abstraction without changing run behavior.
- User intent: Make the highest-change runner path easier to reason about and review while keeping the current tool, compaction, steering, and accounting semantics intact.
- Success definition:
  - `Runner.runStepEngine(...)` becomes a thin delegator into a dedicated internal step-engine type.
  - The extracted component owns per-step provider calls, hook execution, tool orchestration, accounting, memory observation, compaction, and steering timing.
  - Existing step-boundary behavior remains unchanged for `run.step.started`, `steering.received`, `llm.turn.requested`, and `run.step.completed`.
  - Focused harness tests and the package-wide `internal/harness` suite pass after the extraction.
- Non-goals:
  - Redesigning the run state model.
  - Changing HTTP/event contracts.
  - Changing tool policy semantics or approval behavior.
- Guardrails/constraints:
  - Strict TDD with characterization coverage before the move.
  - Keep the abstraction internal and narrow.
  - Preserve existing event ordering and message-state ownership.
- Open questions:
  - Whether a single `stepEngine` type is enough for this pass, or whether the tool-execution branch needs to be split further in a follow-up.
- Next verification step: rerun focused harness step/steering tests, then rerun `go test ./internal/harness`, then run the repo regression gate before opening the PR.

## 2026-03-25 (Issue #426 Bootstrap Wiring)

- Command intent: Complete GitHub issue `#426` by splitting `harnessd` bootstrap assembly into focused helpers while preserving startup/shutdown behavior.
- User intent: Make the `harnessd` entrypoint easier to evolve and review by moving subsystem wiring out of the monolithic `runWithSignals(...)` flow.
- Success definition:
  - `cmd/harnessd/main.go` becomes more orchestration-focused and delegates bootstrap assembly to smaller helpers.
  - The extracted seams cover provider/catalog startup, persistence/cron startup, and webhook/server assembly without changing runtime behavior.
  - New failing-first tests pin the extracted behavior directly.
  - `go test ./cmd/harnessd` passes.
  - The repo regression gate and PR CI pass so the PR is mergeable.
- Non-goals:
  - Changing runtime behavior or public API contracts.
  - Refactoring unrelated runner or transport code.
- Guardrails/constraints:
  - Strict TDD.
  - Keep changes narrow and reviewable.
  - Preserve existing env/config-driven optional subsystem behavior.
- Open questions:
  - Which bootstrap slices can be extracted most cleanly without forcing broad new seams into already-tested startup behavior.
- Next verification step: run the new helper tests red, implement the missing helper layer, then rerun `go test ./cmd/harnessd` before the full regression gate.

## 2026-03-25 (Issue #422 Run Persistence Ownership)

- Command intent: Complete GitHub issue `#422` by consolidating run-record persistence ownership into the runner boundary and removing duplicate HTTP-side `CreateRun` calls.
- User intent: Make run persistence predictable and test-backed so transports stop guessing whether they need to write run records themselves.
- Success definition:
  - Issue `#422` is the only implementation target in this run.
  - `POST /v1/runs` persists exactly once when a store is configured.
  - External-trigger `start` and `continue` paths also persist exactly once for new run records.
  - Store-backed get/list behavior remains unchanged for clients.
  - Focused tests pass and the repo regression gate is rerun before merge.
- Non-goals:
  - Redesigning the store API.
  - Broadly refactoring the HTTP transport.
  - Changing response shapes or persistence fatality semantics.
- Guardrails/constraints:
  - Strict TDD with failing tests first.
  - Keep the change narrow and centered on ownership.
  - Preserve best-effort persistence behavior where it is already non-fatal.
- Open questions:
  - Whether any non-HTTP transport path still duplicates run creation beyond the currently visible `/v1/runs` and external-trigger handlers.
- Next verification step: capture the baseline test status, add failing single-write regressions in `internal/server`, then remove the duplicate transport-layer `CreateRun` calls and rerun targeted plus repo-wide verification.

## 2026-03-25 (Issue #430 Allowed-Tools Fallback Integrity)

- Command intent: Complete GitHub issue `#430` end to end by preserving `allowed_tools` restrictions on agent and skill fallback paths, with failing tests first, regression coverage, PR, and mergeable CI.
- User intent: Fix the security-sensitive gap where constrained agent/skill requests can silently become unconstrained when execution falls back to plain `RunPrompt(...)`, and land the change cleanly.
- Success definition:
  - Issue `#430` is the only implementation issue worked in this run.
  - Failing-first regression tests reproduce the `allowed_tools` leak on `/v1/agents`, `internal/harness/tools/skill.go`, and `internal/harness/tools/core/skill.go` fallback paths.
  - The production fix preserves allowlists consistently across primary and fallback execution without changing unrestricted behavior when `allowed_tools` is omitted.
  - Relevant package tests pass, and the repo regression gate is addressed enough for the resulting PR to be cleanly mergeable.
  - GitHub issue comments clearly capture claim/progress/result and the PR includes a concise summary plus any residual notes.
- Non-goals:
  - Broad refactors outside the fallback allowlist plumbing.
  - New tool-policy features beyond preserving existing restrictions.
- Guardrails/constraints:
  - Strict TDD: failing tests first, then minimal implementation.
  - Keep the change scoped to the fallback run paths and any small shared helper needed.
  - Do not fix unrelated pre-existing failures except where required to make this PR mergeable.
- Open questions:
  - Whether the narrowest safe fix is a new constrained runner entrypoint or routing fallback execution through an existing constrained path.
- Next verification step: finish the baseline targeted-package run, add failing regression tests for all three fallback surfaces, then implement the smallest shared fix and rerun targeted packages plus the repo regression script.

## 2026-03-25 (Issue #427 HTTP Feature Decomposition)

- Command intent: Complete GitHub issue `#427` by extracting another HTTP transport slice out of `internal/server/http.go` without changing behavior.
- User intent: Make the server surface easier to maintain and extend while still landing one backlog issue end to end with a clean, mergeable PR.
- Success definition:
  - Run and conversation transport logic no longer lives inline in `internal/server/http.go`.
  - Route paths, method handling, scope checks, and payloads remain unchanged.
  - `go test ./internal/server` passes before and after the extraction.
  - The repo regression gate and PR CI are green before merge.
- Non-goals:
  - Redesigning the server API.
  - Refactoring runner/domain behavior.
  - Touching unrelated server features already split into sibling files.
- Guardrails/constraints:
  - Keep the extraction narrow and reviewable.
  - Prefer file moves and small helper extraction over logic changes.
  - Treat the existing server tests as the primary contract for this refactor.
- Open questions:
  - Whether the route-registration helper extraction alone is enough, or whether moving the run/conversation handlers themselves yields a clearer seam in one PR.
- Next verification step: move the run and conversation transport code into dedicated files, rerun `go test ./internal/server`, then run the repo regression script.

## 2026-03-25 (Issue #429 Forked Child-Run Failure Propagation)

- Command intent: Complete GitHub issue `#429` by ensuring callers do not report forked child runs as successful when `RunForkedSkill(...)` returns `ForkResult.Error` with a nil Go error.
- User intent: Make `/v1/agents` and fork-context skill tools surface real child-run failures instead of silently treating them as success.
- Success definition:
  - `/v1/agents` returns an execution error when a forked skill returns `ForkResult{Error: ...}`.
  - The flat skill tool and core skill tool both fail fast on `ForkResult.Error` instead of returning `status: completed`.
  - Healthy forked success paths still prefer `Summary` over `Output` exactly as before.
  - Focused regression tests cover all three caller surfaces.
- Non-goals:
  - Changing fallback `RunPrompt(...)` behavior.
  - Refactoring the runner orchestration path beyond what is needed to expose the failure consistently.
  - Addressing unrelated `allowed_tools` fallback work in issue `#430`.
- Guardrails/constraints:
  - Strict TDD: add failing regressions first.
  - Keep the fix narrow and behavior-preserving for successful forked runs.
  - Do not fix unrelated pre-existing failures if broader verification exposes them.
- Open questions:
  - Whether other `ForkResult` consumers outside this issue already normalize `result.Error` consistently enough or should be audited in a later pass.
- Next verification step: run the focused package suite, then the repo regression gate, then open a PR and verify CI completes cleanly.

## 2026-03-25 (Issue #421 Config Runtime Contract)

- Command intent: Complete GitHub issue `#421` by making the merged harness config the authoritative runtime contract for `harnessd`, with failing-first projection tests and the required docs/log updates.
- User intent: Close one scoped backlog issue end to end so operators can trust that merged config values, especially `auto_compact` and `forensics`, actually affect live runner behavior instead of being silently ignored.
- Success definition:
  - `cmd/harnessd` builds `harness.RunnerConfig` through one authoritative projection path instead of scattered field assignment.
  - Focused tests fail first and then pass for the currently-supported `auto_compact` fields:
    - `enabled`
    - `mode`
    - `threshold`
    - `keep_last`
    - `model_context_window`
  - Focused tests fail first and then pass for the currently-supported `forensics` fields:
    - `trace_tool_decisions`
    - `detect_anti_patterns`
    - `trace_hook_mutations`
    - `capture_request_envelope`
    - `snapshot_memory_snippet`
    - `error_chain_enabled`
    - `error_context_depth`
    - `capture_reasoning`
    - `cost_anomaly_detection_enabled`
    - `cost_anomaly_step_multiplier`
    - `audit_trail_enabled`
    - `context_window_snapshot_enabled`
    - `context_window_warning_threshold`
    - `causal_graph_enabled`
    - `rollout_dir`
  - Existing `model`, `max_steps`, default prompt, and role-model behavior remain unchanged.
  - The issue branch has a PR with passing required checks and no merge conflicts.
- Non-goals:
  - Broad `harnessd` bootstrap decomposition beyond what is needed to centralize runner-config projection.
  - Changing config precedence rules or profile/env semantics.
  - Fixing unrelated pre-existing regression/coverage failures outside this issue's scope.
- Guardrails/constraints:
  - Strict TDD: write failing tests first.
  - Keep the fix narrow and centered on config-to-runtime projection.
  - Preserve existing runtime behavior except where config values are currently ignored.
  - Do not weaken the repo regression gate; solve mergeability without papering over unrelated debt.
- Open questions:
  - Whether any runner-config fields besides `auto_compact` and `forensics` should move into the shared projection helper in this pass for consistency.
- Next verification step: add failing projection tests in `cmd/harnessd/main_test.go`, introduce the minimal shared builder/helper, run targeted package tests, then run `./scripts/test-regression.sh`.
## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Command intent: Complete GitHub issue `#428` by ensuring timed-out or cancelled parent calls actively cancel spawned child runs instead of leaving them executing in the background.
- User intent: Make agent and skill timeout behavior trustworthy so callers do not get a timeout while hidden work continues consuming tokens or mutating state.
- Success definition:
  - `waitForTerminalResult(...)` actively requests child-run cancellation when the parent context ends before a terminal event arrives.
  - Regression coverage proves the child run transitions to `cancelled` for both `RunPrompt(...)` and `RunForkedSkill(...)`.
  - Existing successful terminal-result behavior remains intact when the child run already finished.
  - Relevant harness/server tests pass after the fix.
- Non-goals:
  - Redesigning the runner lifecycle model.
  - Changing unrelated timeout or error mapping behavior.
  - Fixing unrelated pre-existing test failures outside the touched surfaces.
- Guardrails/constraints:
  - Follow strict TDD: write the failing tests first.
  - Keep the fix scoped to run lifecycle/cancellation plumbing.
  - Preserve idempotent cancellation and terminal result behavior.
- Open questions:
  - Whether `/v1/agents` needs additional direct assertions beyond harness-level cancellation coverage.
- Next verification step: run the current harness/server baseline, add failing orchestration tests that expose the leak, then implement the minimal cancellation propagation fix.
## 2026-03-25 (Issue #424 Event Journal Extraction)

- Command intent: Complete GitHub issue `#424` by extracting the runner event journal/sink path from `emit()` while preserving event ordering, terminal sealing, recorder behavior, subscriber fanout, and store-append semantics.
- User intent: Make the hottest runner event path easier to reason about and evolve without changing any public harness behavior or weakening the existing forensic guarantees.
- Success definition:
  - `Runner.emit()` delegates the event journal/sink responsibilities to a narrower internal boundary instead of keeping all logic inline.
  - Existing behavior remains unchanged for event IDs, sequence numbers, timestamps, subscriber fanout, recorder writes, terminal sealing, and store append ordering.
  - New or updated characterization tests pin the extracted boundary directly, especially around store append ordering and recorder terminal handling.
  - `go test ./internal/harness` passes in the issue worktree.
- Non-goals:
  - Refactoring the step engine or workspace preflight logic.
  - Changing SSE/event payload contracts.
  - Redesigning the store or recorder abstractions beyond what is needed for a narrow extraction seam.
- Guardrails/constraints:
  - Strict TDD: add a failing test first for the extraction seam or uncovered invariant.
  - Keep the change scoped and reviewable.
  - Preserve terminal-event sealing behavior exactly.
- Open questions:
  - Whether the cleanest seam is a helper on `runState`, a focused sink struct, or a small internal method family on `Runner`.
- Next verification step: add a failing characterization test around the event journal/store ordering boundary, then implement the minimal extraction and rerun `go test ./internal/harness`.

## 2026-03-25 (Backend OpenRouter Model Discovery)

- Command intent: Implement a backend model discovery layer with OpenRouter live discovery, TTL caching, static-overlay merge behavior, runtime routing support, `/v1/models` integration, tests, and docs.
- User intent: Make backend model selection and model listing behave like the already-improved startup/TUI paths, so dynamic OpenRouter slugs work without depending on a fully hardcoded catalog.
- Success definition:
  - Backend discovery exists as an additive layer over the existing provider catalog.
  - OpenRouter live models can be fetched from `https://openrouter.ai/api/v1/models` with in-memory TTL caching.
  - Static catalog metadata continues to win when present for pricing, aliases, quirks, and context defaults.
  - Runtime provider resolution can route `moonshotai/kimi-k2.5` through OpenRouter when OpenRouter is configured and no explicit provider is set.
  - `GET /v1/models` includes live OpenRouter models and falls back safely to cache or static catalog when live discovery fails.
  - Existing static-catalog providers remain unchanged.
  - Focused regression tests cover fetch decode, cache behavior, merged listing, dynamic routing, and fallback behavior.
- Non-goals:
  - Generalizing discovery for every provider in this pass.
  - Replacing the static catalog or startup bootstrap behavior outright.
  - Making startup block on network discovery.
- Guardrails/constraints:
  - Keep changes small and reviewable.
  - Follow strict TDD.
  - Use cached data when possible and static fallback otherwise.
  - Do not break existing catalog-driven providers or `/v1/models` consumers.
- Open questions:
  - Whether the backend `list_models` tool should be discovery-aware now or in a follow-up once `/v1/models` and routing are stabilized.
- Next verification step: add failing tests for discovery/cache/merge/routing, implement the minimal backend layer, then run targeted packages and the regression suite.

## 2026-03-25 (Issue #431 Startup Cleaner Cancellation)

- Command intent: Process GitHub issue `#431` end to end by fixing the `harnessd` conversation-cleaner startup leak, landing regression coverage, and getting a clean PR ready to merge.
- User intent: Close one bounded backlog issue fully instead of stopping at analysis, with explicit TDD, issue updates, and CI-clean merge readiness.
- Success definition:
  - A failing regression test reproduces a startup failure after the conversation cleaner has already been started.
  - `cmd/harnessd/main.go` guarantees the cleaner cancel function is used on all exit paths after initialization.
  - Existing clean-shutdown behavior remains unchanged.
  - `go test ./cmd/harnessd` passes.
  - `go vet ./internal/... ./cmd/...` no longer reports the `convCleanerCancel` leak.
  - The issue is updated and a PR is opened with passing checks.
- Non-goals:
  - Broad `harnessd` bootstrap decomposition.
  - Behavior changes to conversation retention beyond correct cleanup.
  - Fixing unrelated failing tests outside this issue.
- Guardrails/constraints:
  - Strict TDD with the regression test written first.
  - Keep the production fix small and reviewable.
  - Use repo-local Go caches in this sandbox so builds/tests stay writable.
- Open questions:
  - Whether repo-wide regression or CI will expose unrelated blockers after the targeted fix is green.
- Next verification step: add the startup-failure regression test, run it red, apply the minimal cleanup fix, then rerun `go test ./cmd/harnessd` and `go vet ./internal/... ./cmd/...`.

## 2026-03-25 (Issue #423 Runner Preflight Extraction)

- Command intent: Complete GitHub issue `#423` by extracting the `Runner.execute()` preflight/setup path into an explicit helper without changing runtime behavior.
- User intent: Make the first seam in the runner monolith concrete and test-backed so future workspace/profile/MCP changes are easier to review and safer to evolve.
- Success definition:
  - `execute()` delegates preflight responsibilities instead of inlining them.
  - Direct tests cover workspace-type fallback, workspace provisioning failure, prompt re-resolution with the provisioned workspace path, and per-run MCP setup.
  - `go test ./internal/harness` passes after the extraction.
  - A PR is opened and reaches a clean mergeable state.
- Non-goals:
  - Extracting the event journal or step loop.
  - Changing workspace/profile/MCP semantics beyond preserving current behavior.
  - Broad server or provider refactors.
- Guardrails/constraints:
  - Strict TDD: failing characterization tests first.
  - Keep event ordering and terminal behavior unchanged.
  - Use the current dedicated automation worktree and a focused issue branch only.
- Open questions:
  - Whether the cleanest seam is a helper function, a small struct, or a pair of helpers split between workspace and MCP setup.
- Next verification step: add the direct failing preflight tests, implement the minimal extraction, and rerun `go test ./internal/harness` before opening the PR.

## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Command intent: Complete GitHub issue `#428` end to end by ensuring timed-out or cancelled subruns created through `RunPrompt(...)` and `RunForkedSkill(...)` are actively cancelled instead of continuing in the background.
- User intent: Make parent timeout/cancellation behavior trustworthy so `/v1/agents` and forked skill execution do not leak cost or keep mutating state after the caller already got a timeout/cancel result.
- Success definition:
  - `waitForTerminalResult(...)` actively cancels a still-running child run when the waiting parent context ends.
  - Already-terminal child runs still return their terminal result instead of being overwritten by a parent cancellation race.
  - Direct regression coverage exists for `RunPrompt(...)`, `RunForkedSkill(...)`, and the `/v1/agents` timeout path.
  - The targeted harness/server tests pass, and the branch is validated through the repo regression/CI gate before completion.
- Non-goals:
  - Redesigning runner lifecycle ownership beyond the cancellation handoff needed here.
  - Refactoring unrelated timeout handling or issue surfaces outside the affected subrun wait path.
- Guardrails/constraints:
  - Strict TDD: add failing regression tests first.
  - Keep the fix narrow and preserve existing terminal-result behavior.
  - Do not fix unrelated failing tests unless they are required to make this branch mergeable under repo policy.
- Open questions:
  - Whether any detached/worktree-specific regression-gate failures still remain after rebasing onto current `main`.
- Next verification step: add failing cancellation regressions around the wait path, implement the narrow runner fix, then run targeted packages and the full regression gate.

## 2026-03-24 (Worktree Bootstrap Script)

- Command intent: Build a reusable setup script that creates a fresh agent worktree and leaves it ready for local development and verification.
- User intent: Give agents a consistent, low-friction bootstrap path so they do not have to assemble the worktree environment by hand.
- Success definition:
  - `scripts/init.sh` creates or reuses a dedicated worktree under `.codex-worktrees/`.
  - `scripts/bootstrap-worktree.sh` remains as a compatibility wrapper only.
  - The script downloads Go dependencies and builds local binaries inside the worktree instead of dirtying the main checkout.
  - The script writes a sourceable env file with the key workspace paths and binary locations.
  - The script can optionally start `harnessd` in tmux for long-running local development.
  - `AGENTS.md`, `CLAUDE.md`, and the worktree runbook point agents at the canonical init script.
- Non-goals:
  - Replacing the full worktree policy or test-gated merge workflow.
  - Adding new runtime behavior to `harnessd`.
- Guardrails/constraints:
  - Long-running processes must still run in tmux.
  - Keep the script safe to rerun on an existing worktree.
  - Do not overwrite unrelated user changes.
- Open questions:
  - Whether future bootstrap automation should also start a smoke-test session by default.
- Next verification step: run the script in `--check` mode, verify the shell syntax, and confirm the docs reference the new entrypoint.

## 2026-03-18 (Issue #316 Context Grid Coverage)

- Command intent: Take one open backlog issue to completion by adding direct regression coverage for the TUI context usage grid component and merging the work.
- User intent: Close a clearly scoped backlog item end to end with strict TDD, proving the `/context` usage grid’s rendering contract directly instead of relying on indirect overlay tests.
- Success definition:
  - Issue `#316` is the only issue worked in this run.
  - Dedicated tests exist for `cmd/harnesscli/tui/components/contextgrid`.
  - Tests cover default total fallback, used-token clamping, width fallback/bar limits, and rendered usage text.
  - The repo regression gate passes before merge.
  - A PR is opened and merged, or a concrete GitHub permission blocker is reported.
- Non-goals:
  - Refactoring unrelated TUI code.
  - Expanding scope to additional coverage-only issues.
- Guardrails/constraints:
  - Strict TDD: failing tests first, then minimal implementation.
  - Keep changes inside the current worktree/branch.
  - Preserve existing behavior unless acceptance-criteria coverage exposes a small required fix.
- Open questions:
  - Whether any production code change is needed, or the issue resolves with tests only.
- Next verification step: Add the new package tests, run them red then green, and execute `./scripts/test-regression.sh` before opening the PR.

## 2026-03-18 (Repo-Wide Zero-Coverage Gate)

- Command intent: Fix the repo-wide zero-coverage regression gate so pushes are no longer blocked.
- User intent: Make the required regression script pass end to end without weakening the coverage protections that are supposed to catch real test erosion.
- Success definition:
  - `./scripts/test-regression.sh` completes successfully.
  - Coverage collection reflects repo-wide execution instead of package-local blind spots where appropriate.
  - Remaining zero-covered functions in `./internal/...` and `./cmd/...` are exercised by targeted regression tests rather than ignored.
  - Any incidental regression blockers encountered while reaching the coverage gate are resolved or made deterministic.
- Non-goals:
  - Lowering the minimum coverage threshold.
  - Disabling the zero-function coverage rule.
  - Broad refactors unrelated to the current push blocker.
- Guardrails/constraints:
  - Keep runtime behavior unchanged unless a deterministic test fix requires a minimal correction.
  - Prefer small focused tests over sweeping placeholder coverage tests.
  - Update the repo docs/logs to reflect the coverage-gate behavior change.
- Open questions:
  - Whether the race-path harness failure is a one-off flake or needs a deterministic fix in this pass.
- Next verification step: Run a repo-wide coverage pass with `-coverpkg`, add the missing targeted tests, and rerun `./scripts/test-regression.sh`.

## 2026-03-18 (Runner Concurrency Invariants)

- Command intent: Implement the review feedback by making the runner's concurrency and lifecycle invariants explicit and test-enforced.
- User intent: Preserve the recorder/message-state fixes by making future changes defend clear ownership, serialization, and state-transition rules instead of relying on race-clean runs alone.
- Success definition:
  - The runner code documents the concurrency invariants for recorder ordering, message-state ownership, and payload isolation.
  - Regression coverage explicitly checks the JSONL ledger matches in-memory event history.
  - Existing compaction and forensic-isolation tests are aligned with the invariant framing.
- Non-goals:
  - Redesigning the runner concurrency model.
  - Introducing new behavior beyond invariant enforcement/documentation.
- Guardrails/constraints:
  - Keep implementation scoped to the runner/test surface touched by the review.
  - Preserve current runtime behavior.
  - Do not overwrite unrelated user changes in the worktree.
- Open questions:
  - Whether the team later wants a dedicated invariant checklist in review docs beyond code comments and tests.
- Next verification step: Run targeted harness tests for recorder ordering/completeness and compaction source-of-truth behavior, then record the result in the logs.

## 2026-03-18 (Provider/Model Impact Map Guardrail)

- Command intent: Implement the repo review finding by requiring a cross-surface impact map for provider/model flow work.
- User intent: Prevent feature slices from landing with missing integration coverage across config, server wiring, TUI behavior, or regression tests.
- Success definition:
  - A reusable impact-map template exists in `docs/plans/`.
  - The bootstrap, plan template, and worktree flow all direct contributors to create the artifact before implementation.
  - The four required headings are explicit: config, server API, TUI state, regression tests.
  - Blank headings are called out as a warning, with `None` plus rationale required when a surface is truly unaffected.
- Non-goals:
  - Adding CI enforcement in this pass.
  - Retrofitting older tasks with new impact maps.
- Guardrails/constraints:
  - Keep the artifact lightweight and one-page.
  - Only require it for provider/model flow work rather than every task.
  - Fit the rule into the repo's existing planning workflow.
- Open questions:
  - Whether future automation should lint for missing impact maps on provider/model changes.
- Next verification step: Confirm the new template and runbook are reachable from `AGENTS.md`, `PLAN_TEMPLATE.md`, and `docs/runbooks/worktree-flow.md`.

## 2026-03-18 (Ownership And Copy-Semantics Hardening)

- Command intent: Build and apply a concrete ownership/copy-semantics checklist grounded in the repo's runner review history.
- User intent: Stop repeating shallow-copy regressions by making clone boundaries explicit in code and documentation instead of rediscovering them in review loops.
- Success definition:
  - Exported or state-storing harness types with mutable fields have explicit clone behavior.
  - Registry and runner snapshot paths stop relying on ad hoc shallow struct copies where shared maps/slices can leak through.
  - A reusable internal checklist exists for reviewing slices, maps, pointers, and nil semantics before code review.
  - Ownership-focused tests pass for the touched surfaces.
- Non-goals:
  - Solving every historical runner concurrency issue in the same pass.
  - Refactoring unrelated packages just to use clone helpers.
- Guardrails/constraints:
  - Preserve existing nil semantics where callers may distinguish nil from empty.
  - Keep the change narrow, reviewable, and grounded in current code rather than generic guidance.
  - Run the package tests and the repo regression gate before considering the task complete.
- Open questions:
  - Which additional exported types outside `internal/harness` should adopt the same contract in a follow-up pass.
- Next verification step: Run `go test ./internal/harness` and `./scripts/test-regression.sh`, then record the concrete pass/fail result in the engineering log.

## 2026-03-18 (Issue #332 Runner Orchestration Coverage)

- Command intent: Complete GitHub issue `#332` by adding direct regression coverage for `SubmitInput`, `RunPrompt`, and `RunForkedSkill`.
- User intent: Make runner orchestration extraction safer by pinning the public helper semantics that currently rely on incidental coverage.
- Success definition:
  - `SubmitInput` error mapping is asserted directly.
  - terminal-history, stream-closure, and terminal-result mapping behavior are covered through deterministic orchestration tests.
  - `go test ./internal/harness` passes with the new regression coverage in place.
- Non-goals:
  - broader runner refactors beyond what is needed to expose the wait-path contract.
  - fixing unrelated packages that fail only because the sandbox forbids opening localhost listeners.
- Guardrails/constraints:
  - Keep behavior unchanged while making orchestration wait semantics directly testable.
  - Follow strict TDD and stop if the full repo regression gate is blocked by unrelated failures.
- Open questions:
  - Whether the repo regression script should eventually detect sandboxed localhost restrictions and skip listener-based packages in this environment.
- Next verification step: run the targeted harness tests, then `go test ./internal/harness`, then `./scripts/test-regression.sh` and record the blocker if the sandbox still prevents listener-based tests.

## 2026-03-17 (Untested Feature Issue Backlog)

- Command intent: Identify implemented features that are missing test coverage and create GitHub issues for them.
- User intent: Turn the remaining untested feature surface into concrete, trackable work items instead of leaving test gaps implicit.
- Success definition:
  - Remaining feature areas with no meaningful tests are identified from the current codebase.
  - GitHub issues are created with scope, impact, and acceptance criteria for each missing-test feature area.
  - The issue set is grounded in the current implementation rather than stale documentation.
- Non-goals:
  - Writing the missing tests in this pass.
  - Reworking features that are already adequately covered.
- Guardrails/constraints:
  - Prefer feature-level gaps over file-by-file nitpicks.
  - Use the repo code and test layout as the source of truth.
  - Keep issue scope specific enough for a remote agent to execute directly.
- Open questions:
  - Whether the unimplemented `thinkingbar` should be treated as a missing-test issue only or folded into a broader implementation issue later.
- Next verification step: Confirm the created issues map to packages with zero direct test coverage and record the issue numbers in the task handoff.

## 2026-03-19 (Post-Review Stabilization Backlog)

- Command intent: Convert the harness/TUI review into a concrete, dependency-ordered GitHub issue backlog.
- User intent: Work through the next tranche of high-value improvements methodically without guessing what should happen next or over-investing in low-value new features.
- Success definition:
  - Review findings are turned into a small ordered set of implementation issues.
  - Each issue names the target behavior, tests required, regression coverage required, and any dependency order.
  - The backlog favors stabilization/productization over speculative feature growth.
- Non-goals:
  - Implementing the fixes in this pass.
  - Expanding the feature surface beyond what is needed to make the current system coherent.
- Guardrails/constraints:
  - Prefer issues that remove architectural friction, deployment friction, or user-facing rough edges.
  - Separate harness and TUI concerns clearly.
  - Make each ticket executable by a remote agent without additional grooming.
- Open questions:
  - Whether the TUI command/render consolidation should be delivered as one PR or a short stack of smaller PRs.
- Next verification step: Create the GitHub issues, then capture the resulting issue numbers and dependency order in the handoff.

## 2026-03-19 (Issue #361 Golden Path Deployment Contract)

- Command intent: Implement issue `#361` by making the documented golden-path deployment actually bootable and by backing it with repeatable regression coverage plus a live smoke entrypoint.
- User intent: Work through the backlog in dependency order with real TDD, so the harness has one trustworthy deployment path before more feature work lands.
- Success definition:
  - `harnessd` has a real, repo-supported `full` startup contract instead of a broken documented profile path.
  - Regression tests fail first and then pass for profile resolution and persistence-backed startup/readback.
  - The smoke script validates health, provider/model discovery, run creation, event streaming, at least one tool call, terminal completion, and persistence readback.
  - The golden-path runbook matches the actual startup contract.
- Non-goals:
  - Adding CI enforcement for live-provider smoke.
  - Expanding the golden path to S3, extra MCP servers, or third-party integrations.
- Guardrails/constraints:
  - Strict TDD.
  - Keep the path provider-agnostic where practical after the #362 bootstrap fix.
  - Preserve the current harness API surface unless a startup contract bug requires a small fix.
- Open questions:
  - Whether the cleanest supported `full` contract should resolve through config-layer builtins or project-level profile discovery.
- Next verification step: add the failing startup/profile regression test, reproduce the smoke-script failure locally, then implement the smallest fix that makes `--profile full` and the persistence-backed smoke path real.

## 2026-03-17 (Docs And Contract Sync)

- Command intent: Update the user-facing documentation so it matches the current harness codebase.
- User intent: Make the README, agent guidance, and live CLI runbook reflect the actual routes, run payload, event surface, tool catalog, and configuration behavior.
- Success definition:
  - README describes the current HTTP routes, run request shape, event families, tool surface, and configuration knobs.
  - CLAUDE.md no longer says provider support is only planned.
  - The harness CLI runbook reflects the current flags and live-testing flow.
  - The long-term thinking log records the docs-sync effort.
- Non-goals:
  - Changing runtime behavior.
  - Adding new APIs or tools.
- Guardrails/constraints:
  - Treat the implementation as the source of truth.
  - Avoid documenting unsupported flags, routes, or environment variables.
- Open questions:
  - Whether the README should later split the long environment list into a dedicated config reference doc.
- Next verification step: Reconcile any future API or config changes against these docs before release.

## 2026-03-04

- Command intent: Set up a new git repository with a strong documentation system, strict TDD workflow, worktree-based delivery, test-gated merge discipline, and operational runbooks.
- User intent: Make the project easy for multiple agents to understand and execute quickly, while keeping technical rigor without over-engineering beyond MVP needs.
- Success definition:
  - Repo initialized on `main`.
  - Documentation folders and indexes exist.
  - Engineering, observational, and system logs exist.
  - Plans/checklist workflow exists and is required.
  - UX requirements and nightly task guidance exist.
  - Agent policy points to these documents and explains intent precedence.
- Non-goals:
  - Full enterprise process stack.
  - Premature scaling optimization.
- Guardrails/constraints:
  - Security best practices remain mandatory.
  - Tests must be meaningful and run before commit.
  - Bugs must produce regression tests and issue tracking.
- Open questions:
  - Final CI/test tooling conventions once implementation code exists.
  - Deployment target/platform details.
- Next verification step: Validate all indexes and cross-references after each new documentation file is added.

## 2026-03-04 (Workflow Adjustment)

- Command intent: Keep the workflow lightweight and practical for early-stage execution, with automatic merge/push to `main`.
- User intent: Reduce operational friction from branch tracking while retaining test-first discipline and clear docs.
- Success definition:
  - Merge helper script auto-pushes `main` on success.
  - No hard enforcement gates are introduced yet.
  - Process expectations remain clear in docs.
- Non-goals:
  - Hook/CI enforcement during early-stage setup.
- Guardrails/constraints:
  - Continue strict TDD and meaningful test requirements.
  - Keep regression-test + issue + logging discipline for bugs.
- Open questions:
  - When to transition from process-guided to hard-gated enforcement.
- Next verification step: Revisit enforcement level once contributor volume and deployment risk increase.

## 2026-03-04 (OpenAI Harness POC)

- Command intent: Design and implement a proof-of-concept Go coding harness powered by OpenAI as a service/server that emits events for easy GUI/TUI integration.
- User intent: Validate the architecture quickly with a minimal but real tool-calling runtime and a streamable event surface.
- Success definition:
  - Runnable Go server exists with API endpoints for run creation, status lookup, and event streaming.
  - Harness loop calls OpenAI and executes a small coding-oriented toolset.
  - Event stream exposes lifecycle/tool/assistant events suitable for client rendering.
  - Tests cover harness loop behavior, tool behavior, and HTTP/SSE behavior.
- Non-goals:
  - Durable persistence across process restarts.
  - Production-hardening of permissions, authn/authz, and multi-tenant isolation.
- Guardrails/constraints:
  - Keep implementation scope small and deterministic.
  - Preserve workspace boundaries for file tools.
  - Enforce bounded execution (`max_steps`, tool command timeout).
- Open questions:
  - Should future iterations expose token-level streaming deltas from provider responses?
  - Should run queueing/cancellation become session-aware in v2?
- Next verification step: Run an end-to-end manual check with a live API key (`POST /v1/runs` + `GET /v1/runs/{id}/events`) and confirm event consumption in a prototype client.

## 2026-03-04 (Toolset Rename and Capability Adjustment)

- Command intent: Update harness tools to include `read`, `write`, `edit`, and `bash`.
- User intent: Make the coding harness expose a more practical editing and shell-command interface for interactive clients.
- Success definition:
  - Default registry only exposes requested tool names.
  - File tools remain workspace-scoped and reject traversal attempts.
  - `edit` provides deterministic text replacement behavior.
  - `bash` executes commands with timeout and basic safety rejection.
  - Tests validate new toolset behavior.
- Non-goals:
  - Full sandboxing/authorization model for arbitrary shell execution.
  - Advanced patch semantics beyond exact text replacement.
- Guardrails/constraints:
  - Keep command execution bounded by timeout.
  - Prevent obvious dangerous shell patterns.
  - Preserve existing run loop and SSE API.
- Open questions:
  - Should `bash` evolve to an allow-list instead of a deny-list?
  - Should `edit` support multi-hunk line-range operations in a future revision?
- Next verification step: Execute a live run that uses all four tools and confirm client-side event rendering with final file state validation.

## 2026-03-04 (All Functions Tested Request)

- Command intent: Test all functions in the current harness codebase.
- User intent: Increase confidence that each function has at least one executed test path.
- Success definition:
  - Every function in `go tool cover -func` reports non-zero coverage.
  - Tests include entrypoint/runtime failure paths and HTTP error handlers, not only happy paths.
- Non-goals:
  - 100% statement/branch coverage.
  - Live external integration tests.
- Guardrails/constraints:
  - Keep runtime semantics unchanged while enabling testability.
  - Avoid introducing behavior-only-for-tests beyond lightweight hook points.
- Open questions:
  - Whether to enforce minimum package-level statement coverage thresholds in CI.
- Next verification step: Decide CI coverage gate policy (for example minimum total + per-package thresholds) and wire into pipeline.

## 2026-03-05 (Regression Enforcement for Ongoing Development)

- Command intent: Ensure complete testing and regression protection as the harness grows.
- User intent: Prevent future feature additions from reducing test confidence.
- Success definition:
  - Single regression script runs core tests + race checks + coverage gates.
  - CI workflow executes same regression script for PRs/pushes.
  - Gate fails on low total coverage and on any function with `0.0%` coverage.
  - Default tool contract has explicit regression test.
- Non-goals:
  - External integration test coverage of third-party systems.
  - Branch protection policy administration.
- Guardrails/constraints:
  - Keep thresholds configurable while default is strict enough to catch regressions.
  - Ensure local and CI use the exact same gate command.
- Open questions:
  - Whether to add per-package minimum coverage thresholds in addition to total threshold.
- Next verification step: Observe CI behavior across next few PRs and tune `MIN_TOTAL_COVERAGE` only if signal/noise ratio is poor.

## 2026-03-05 (Hooks and Baseline Tooling Completion)

- Command intent: Implement pre/post message hook support and add baseline tools (`ls`, `glob`, `grep`, `apply_patch`, `git_status`, `git_diff`) with full TDD and live OpenAI verification.
- User intent: Make the harness extensible around message flow and practical for basic coding/repo tasks with strong regression discipline.
- Success definition:
  - Hook pipeline integrated in runner with event emissions and tested blocking/mutation/error modes.
  - Baseline tools added in harness registry and covered by tests.
  - Regression suite remains green under enforced coverage gate.
  - Live `gpt-5-nano` task succeeds with `run.completed` and real tool usage.
- Non-goals:
  - Production-grade sandbox policy engine for all shell/file operations.
  - Persistent storage for hook execution audit beyond event stream history.
- Guardrails/constraints:
  - Keep run loop deterministic and bounded by `HARNESS_MAX_STEPS`.
  - Maintain workspace boundary checks for path-based tools.
  - Preserve threshold-based regression gating.
- Open questions:
  - Whether `apply_patch` should support targeted nth-occurrence/hunk semantics to reduce accidental first-match replacements.
  - Whether to add hook registration via HTTP API instead of code-level config only.
- Next verification step: Add a focused follow-up for richer patch targeting semantics and optional per-tool policy hooks.

## 2026-03-05 (Sample CLI Test Harness)

- Command intent: Build a small CLI test tool that connects to the harness service and validates run/event behavior quickly.
- User intent: Have an easy way to test the server from terminal and use it for real live smoke tasks.
- Success definition:
  - CLI creates runs through `POST /v1/runs`.
  - CLI streams events through `GET /v1/runs/{id}/events` and exits on terminal events.
  - Unit tests cover payload contract, SSE parsing, success path, and error paths.
  - Full regression suite remains green.
  - Live OpenAI-backed run succeeds with real tool usage.
- Non-goals:
  - Interactive shell/TUI behavior.
  - Persisted local history in the CLI.
- Guardrails/constraints:
  - Keep implementation minimal and deterministic.
  - Reuse current API contracts without introducing server-side changes.
  - Maintain regression gates and coverage threshold.
- Open questions:
  - Whether to add `--run-id` attach mode for streaming existing runs started by another client.
  - Whether to support JSONL/raw-output mode for easier machine parsing.
- Next verification step: Evaluate whether GUI/TUI prototypes should consume CLI output directly or connect to SSE endpoint natively.

## 2026-03-05 (Incremental Modular Tooling Implementation)

- Command intent: Implement the full incremental migration plan to modular, crush-informed tooling with strict TDD and regression gates.
- User intent: Make tools cleanly organized so adding a new tool is low-friction, while expanding tool coverage and preserving quality.
- Success definition:
  - Tool logic moved into `internal/harness/tools/` with catalog-driven registration.
  - Default harness registry remains backward-compatible while exposing expanded tool surface.
  - Approval mode seam exists with `full_auto` default and strict `permissions` behavior available.
  - Regression suite and coverage gate remain passing after migration.
  - Live OpenAI smoke run succeeds with new modular stack.
- Non-goals:
  - UI-driven permission prompts in this iteration.
  - Production-hardened external integration backends for every optional tool.
- Guardrails/constraints:
  - Keep tool contracts deterministic and JSON-schema compatible with OpenAI function calling.
  - Maintain no-zero-function-coverage enforcement.
  - Keep unsupported integrations dependency-gated instead of silently stubbed in runtime.
- Open questions:
  - Whether to default-enable optional external integrations when adapters become available at runtime.
  - Whether to evolve `permissions` mode from policy hook to interactive approval broker in a future iteration.
- Next verification step: add one integration test pack for real MCP adapter wiring and strict-mode policy behavior under active harness runs.

## 2026-03-05 (AskUserQuestion Interactive Clarification Flow)

- Command intent: Implement Claude-compatible `AskUserQuestion` behavior with full server/runner support, strict TDD coverage, and documented operational contracts.
- User intent: Allow upstream clients to drive structured user clarification prompts mid-run and resume safely, without ad hoc protocol handling.
- Success definition:
  - `AskUserQuestion` tool is available in default registry with compatible question/answer schema.
  - Runner supports `waiting_for_user` status and emits explicit wait/resume events.
  - Input API endpoints exist for fetching pending prompts and submitting answers.
  - Timeout is configurable and enforced with deterministic run failure.
  - Tests cover tool validation, broker lifecycle, runner transitions, and HTTP error semantics.
- Non-goals:
  - Interactive CLI prompt UX in this iteration.
  - Persistent pending-question storage across process restarts.
- Guardrails/constraints:
  - Keep structured JSON contracts deterministic for client UI builders.
  - Preserve existing run/event semantics outside the new waiting-input flow.
  - Maintain regression gate discipline and non-zero function coverage constraints.
- Open questions:
  - Whether to add CLI interactive answer collection behind a flag in a follow-up iteration.
- Next verification step: Run full regression gate (`go test`, `go test -race`, `./scripts/test-regression.sh`) and verify event payload shapes in a live harness session.

## 2026-03-05 (Provider Token Streaming)

- Command intent: Check the tracked streaming issues and implement token-by-token model streaming through the harness event surface.
- User intent: Allow clients to render assistant output progressively instead of waiting for a whole provider turn to complete.
- Success definition:
  - Runner accepts incremental provider deltas and emits SSE-visible assistant/tool-call delta events.
  - OpenAI provider uses streaming chat completions and assembles final content/tool calls correctly.
  - Existing turn completion, tool execution, usage accounting, and final assistant message behavior remain intact.
  - Tests cover streamed text, streamed tool-call assembly, and runner event emission order.
- Non-goals:
  - Streaming stdout/stderr from long-running tools.
  - Reworking client UX beyond exposing events.
- Guardrails/constraints:
  - Keep existing REST endpoints unchanged.
  - Do not execute tools until streamed tool-call arguments are fully assembled.
  - Maintain deterministic final run state and regression gate coverage.
- Open questions:
  - Whether to expose separate event types for tool-call creation vs argument deltas in a later iteration.
- Next verification step: Run provider and runner tests, then full regression suite to confirm new streaming events do not break existing clients.

## 2026-03-05 (Optional Observational Memory, Local-First with Scale Path)

- Command intent: Implement observational memory with local standalone viability first, while keeping architecture migration-safe for many-agent and future production deployment.
- User intent: Avoid premature optimization, but build with explicit interfaces, logs, and docs so scaling to many/thousands of agents is a planned expansion rather than a rewrite.
- Success definition:
  - Memory is optional and tool-controlled per scope.
  - Local sqlite + in-process ordered writes work end-to-end.
  - Runner can inject memory snippets and observe transcript deltas.
  - Documentation and logs clearly describe current behavior and scale path.
- Non-goals:
  - Remote coordinator transport implementation in this phase.
  - Full postgres runtime support in this phase.
- Guardrails/constraints:
  - Keep message transcript access read-only for tools.
  - Keep defaults safe (`memory disabled` unless enabled).
  - Preserve existing run loop behavior when memory is inactive.
- Open questions:
  - Remote coordinator wire protocol shape (HTTP vs queue) for multi-instance mode.
  - Postgres locking strategy and operational SLOs for high-write contention.
- Next verification step: Execute local run smoke coverage for `enable -> observe -> export -> review` and confirm event stream + sqlite state transitions.

## 2026-03-05 (System Prompt Modularity and Intent Routing)

- Command intent: Implement a clean modular system prompt architecture with intent-driven startup prompts, model-specific overlays, and runtime context injection.
- User intent: Make system prompt behavior easy to find, audit, and evolve while enabling harness-coordinated specialist agents (for example code review vs frontend design).
- Success definition:
  - Prompt system has its own module and file assets.
  - Run API supports intent/profile/extension fields.
  - Unknown prompt references fail early (`invalid_request`).
  - Runtime context is refreshed per turn without transcript bloat.
  - Prompt-resolution and warning events are visible in run streams.
- Non-goals:
  - Claude Skills runtime integration in this iteration.
  - Real usage/cost injection in this iteration.
- Guardrails/constraints:
  - Preserve `system_prompt` override semantics.
  - Keep startup deterministic and fail-fast on invalid prompt catalog.
  - Keep phase-1 cost reporting explicit (`unavailable_phase1`) rather than implicit estimates.
- Open questions:
  - Final phase-2 approach for provider usage/cost normalization across model providers.
  - Governance workflow for prompt extension additions and review ownership.
- Next verification step: Run full regression script and validate `prompt.resolved` / `prompt.warning` event payloads in an end-to-end live run.

## 2026-03-05 (Token Counting and Cost Tracking Design)

- Command intent: Think through and document a concrete approach to add token counting and cost tracking as a dedicated architecture subsection.
- User intent: Make phase-2 usage/cost work implementation-ready, auditable, and explicit rather than leaving high-level placeholder notes.
- Success definition:
  - Design doc contains a standalone token/cost subsection with data model, provider normalization, pricing strategy, runtime integration, and test coverage.
  - Runtime context replacement path for `cost_status: unavailable_phase1` is clearly defined.
  - Failure states (`estimated`, `unpriced_model`, `provider_unreported`) are explicit for clients/operators.
- Non-goals:
  - Implementing runtime code changes in this documentation update.
  - Finalizing provider pricing numbers in this pass.
- Guardrails/constraints:
  - Keep provider-reported usage as primary source when available.
  - Preserve deterministic run behavior when usage/cost data is unavailable.
  - Keep runtime context ephemeral and avoid transcript bloat.
- Open questions:
  - Canonical location and update policy for pricing catalog ownership.
  - Whether to expose detailed token classes in public API by default or behind optional fields.
- Next verification step: Implement usage normalization + pricing resolver with fixture-based tests, then validate end-to-end events and runtime context output in a live run.

## 2026-03-06 (Periodic Terminal Bench Harness Suite)

- Command intent: Create a Terminal Bench-based test suite that can periodically exercise the real harness end-to-end.
- User intent: Catch regressions that only show up when the harness performs actual terminal tasks, without depending only on unit tests or ad hoc live checks.
- Success definition:
  - Private Terminal Bench tasks exist in-repo and are stable enough for recurring runs.
  - A custom agent bridge runs the current `go-agent-harness` checkout inside task containers.
  - A local runner script exists for operators.
  - A scheduled GitHub Actions workflow can run the suite and keep artifacts.
- Non-goals:
  - Full public benchmark coverage or leaderboard submission.
  - PR-blocking on paid benchmark runs.
- Guardrails/constraints:
  - Keep the suite small, deterministic, and inexpensive.
  - Test the real harness API path (`harnessd` + `harnesscli`), not a mocked adapter.
  - Preserve existing repo regression workflow as the primary pre-merge gate.
- Open questions:
  - Whether to expand the suite beyond smoke coverage once failure patterns stabilize.
  - Whether to add result summarization or alerting beyond artifact upload.
- Next verification step: Run `./scripts/run-terminal-bench.sh` with a real API key and inspect per-task artifacts under `.tmp/terminal-bench/`.

## 2026-03-06 (Issue #18 Head-Tail Buffer for Long Command Output)

- Command intent: Take a tracked GitHub issue, plan it according to project rules, implement it with tests, and merge when the full test gate passes.
- User intent: Improve harness reliability by preventing unbounded command-output growth while preserving useful diagnostics.
- Success definition:
  - Command output handling keeps both leading and trailing content for oversized output.
  - `bash` foreground and background `job_output` paths use bounded output capture.
  - Tests are written first and cover truncation behavior explicitly.
  - Regression gate passes before merge.
- Non-goals:
  - Token streaming changes.
  - Persistent archival of full command logs.
- Guardrails/constraints:
  - Preserve existing tool result schema fields.
  - Keep omission explicit so users know output was truncated.
  - Follow strict TDD and documentation/index maintenance.
- Open questions:
  - Whether additional command-backed tools should share the same bounded output helper immediately.
- Next verification step: Add failing tests for oversized output in both foreground/background flows, implement bounded buffer, then run `./scripts/test-regression.sh`.

## 2026-03-25 (Issue #384 Parent Context Handoff Bundles)

- Command intent: Complete GitHub issue `#384` by replacing ad hoc parent-to-child prompt stuffing with a typed, size-bounded context handoff bundle for delegated subagent and forked executions.
- User intent: Make delegated runs inspectable, replayable, and safely bounded so child prompts get the right parent context without silent loss or runaway prompt growth.
- Success definition:
  - A typed `ParentContextHandoff` contract exists in the tools/harness layer and serializes deterministically.
  - Parent context extraction is bounded by message count, per-message size, and total serialized size, with pinned truncation behavior.
  - `run_agent`, `spawn_agent`, and fork-context skills render the handoff before the child task content in a consistent order.
  - The runner and subagent manager propagate/store the handoff on child runs for debugging and replay.
  - Focused delegation/handoff package tests pass after failing-first coverage is added.
- Non-goals:
  - Redesigning task-complete or child-result payloads.
  - Broad profile/runtime isolation changes unrelated to handoff propagation.
  - Passing the full unbounded parent transcript into child prompts.
- Guardrails/constraints:
  - Strict TDD: failing tests first for serialization, truncation, and prompt order.
  - Keep the handoff deterministic and reviewable.
  - Preserve current behavior when no parent context is available.
- Open questions:
  - Whether the `spawn_agent` system prompt should continue duplicating the task text while the typed handoff block becomes the canonical parent-context contract.
- Next verification step: add failing handoff tests in tools/subagents/harness packages, implement the shared handoff helpers and propagation fields, then rerun focused suites and broader relevant tests.

## 2026-06-26 (Issue #649 Harness Retention and Coveragegate Restoration)

- Command intent: Complete the current T01 reliability slice and restore the full repository regression gate, including coveragegate.
- User intent: Keep the harness reliability work reviewable while proving the repo can pass its actual pre-merge gate without weakening coverage rules.
- Success definition:
  - Completed harness run state and conversation mirrors are pruned with bounded retention.
  - Durable-store compatibility is preserved for completed run lookup.
  - Existing T01 pruning regressions remain green.
  - Coveragegate reports zero uncovered functions without reducing `MIN_TOTAL_COVERAGE`, removing the zero-function rule, or excluding code just to pass.
  - `./scripts/test-regression.sh` exits 0 and prints `[regression] PASS`.
- Non-goals:
  - Broad reliability epic implementation outside T01.
  - Refactoring unrelated runtime, workflow, or tool packages.
- Guardrails/constraints:
  - Add focused behavior tests for uncovered functions instead of superficial call-only coverage.
  - Preserve existing local work and branch name.
  - Update engineering evidence and local tracker state after verification.
- Open questions:
  - None for T01 completion.
- Next verification step: review the scoped diff and, if accepted, promote through the repo's normal verify-and-merge flow.

## 2026-07-14 (Sandbox Default Flip + WebFetcher SSRF Guard + Operator Allowlist)

- Command intent: Close three gaps left by the `fix/tool-sandbox-ssrf` branch on `fix/sandbox-defaults-webfetch`: (1) the workspace-confinement fix was inert because `DefaultPermissionConfig()` still defaulted to `SandboxScopeUnrestricted`; (2) the WebFetcher-backed tools (`web_fetch`, `web_search`, `agentic_fetch`) had no SSRF guard; (3) `tools_default.go` never threaded an operator `NetworkAllowlist` into `BuildOptions`.
- User intent: Err on the side of safety — flip the sandbox default only after measuring blast radius, and give operators an explicit, auditable opt-out/opt-in rather than leaving either an inert protection or an unconditionally locked-down default.
- Success definition:
  - `DefaultPermissionConfig()` and the empty-Sandbox fallback in `normalizePermissionConfig` default to `SandboxScopeWorkspace`; a fresh acceptance test proves a default-configured run cannot read an absolute path outside the workspace (e.g. a stand-in for `~/.ssh/id_rsa`).
  - Blast radius measured before deciding: full suite went from 2 failures (one stale assertion, one genuine macOS seatbelt symlink bug — `/var` vs `/private/var` — now fixed) to 0 after the flip; decision was to keep the flip per the task's own stated threshold.
  - `GuardedWebFetcher` (new, `internal/harness/tools/web_fetcher_guard.go`) reuses `ssrf_guard.go`'s dial-time guard for `Fetch`, is wired at both `BuildCatalog` and `NewDefaultRegistryWithOptions`, and preserves a wrapped base implementation's actual `Fetch`/`Search` content (proven by the pre-existing mock-based `TestMCPDynamicAndAgentTools`, which stayed green through the change).
  - `DefaultRegistryOptions` gained `NetworkAllowlist` and `WebFetcher` fields (neither existed before) so an operator can legitimately allow specific hosts; default stays empty/deny.
  - Full suite (`go test ./...`), `go test ./internal/harness/... -race`, and `go vet ./internal/harness/...` all clean.
- Non-goals:
  - Building or wiring a real production `WebFetcher` implementation (none exists in this repo; the investigation found `web_fetch`/`web_search`/`agentic_fetch` were previously dead/unregistered tools in every production entry point).
  - Wiring `NetworkAllowlist` through a CLI flag/env var in `cmd/harnessd/main.go` (out of scope; mirrors `SandboxScope`, which also has no CLI flag today).
- Guardrails/constraints:
  - Strict TDD: red-then-green per gap, plus a final cross-gap regression commit; never weaken an existing security assertion to make a test pass (the one pre-existing test edited, `TestPermissionConfigDefaults`, only updates the asserted default value).
  - Measure the default-flip blast radius on the real suite before deciding to keep or revert it, per the task's explicit two-step directive.
- Open questions:
  - None outstanding for this branch; see the worker's final report for the full acceptance-question answer and file list.
- Next verification step: review the diff against `fix/tool-sandbox-ssrf`, then promote through the repo's normal PR/CI/squash-merge flow.

## 2026-07-18 (Tier-1 Critical Harness Fixes: #785 #786 #787)

- Command intent: Land three critical tool-layer fixes on `codex/fix-harness-tier1-critical` with strict TDD and per-bug commits — hybrid-compaction tool_call pairing (#787), bash process-group kill on timeout/job_kill (#786), and linux bwrap PID/IPC namespace isolation (#785).
- User intent: Stop providers from rejecting compacted transcripts (400s on orphan tool messages), stop orphaned grandchildren from hanging `cmd.Wait()` and surviving kills, and close the Linux sandbox parity gap that let sandboxed processes signal host processes and read host secrets from `/proc`.
- Success definition:
  - #787: after hybrid compaction, every surviving tool result's `tool_call_id` appears in a preceding assistant message's `tool_calls`, and every assistant `tool_calls` id has a following tool result; proven by `TestCompactHistoryTool_HybridModePreservesToolCallPairing` and `TestCompactHistoryTool_Core_HybridModePreservesToolCallPairing` (red pre-fix, green post-fix), with both `compactHybrid` copies logic-identical modulo package prefixes and all pre-existing compact tests green.
  - #786: a timeout or `job_kill` of `bash -lc 'sleep N & ...; wait'` returns in ~timeout time (not ~N) and the grandchild pid reaches ESRCH; proven by `TestRunForegroundTimeoutKillsProcessGroup`, `TestJobKillKillsBackgroundJobGroup`, `TestRunCommandOnceTimeoutKillsProcessGroup` (red pre-fix: ~10s hangs + live canary; green post-fix); `TestRunCommand_TimeoutReturnsNilError`, `TestRunCommand_ExternalSignalKillRetriesThenErrors`, and the full `internal/harness/tools` suite stay green, including under `-race`.
  - #785: bwrap argv for both Workspace and Local scopes contains `--unshare-pid`, `--unshare-ipc`, `--new-session` (plus pre-existing `--unshare-net`, `--die-with-parent`); on a Linux host a sandboxed command cannot signal a host canary nor read its `/proc/<pid>/environ`. `GOOS=linux go build/vet ./internal/harness/tools/` pass; runtime red/green deferred to a Linux host with bwrap (authored on macOS).
  - Three commits total, one per bug, each carrying its engineering-log entry; clean `git status`; no merge/push (merge gate handled by the parent agent).
- Non-goals:
  - Deduping the two `compact_history.go` copies (scheduled for a later tier).
  - Changing `kill()` semantics beyond the `Cancel` override, sandbox profile content on darwin, or `--die-with-parent`/`--as-pid-1` behavior.
  - Fixing the pre-existing, unrelated `go build ./...` failures in `benchmarks/terminal_bench/reference_solutions/*` (main-less `package main` benchmark fixtures, red since #732).
- Guardrails/constraints:
  - Strict TDD per bug: failing regression tests first, confirmed red for the expected reason, minimal fix, confirmed green.
  - Changes confined to `internal/harness/tools` (+ per-bug engineering-log entries and this ledger); `gofmt` on all edited files; commit messages reference their issue.
  - Pre-existing failures unrelated to the change are reported, not fixed.
- Open questions:
  - None for this tier; Linux runtime verification of #785 is the only deferred evidence (environment constraint, not a design question).
- Next verification step: on a Linux host with bubblewrap, run `go test ./internal/harness/tools/ -run 'TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC|TestSandboxLinuxPIDNamespaceHidesHostProcesses' -count=1 -v`, then promote the branch through the repo's normal verify-and-merge flow.

## 2026-07-18 (Tier-2 Harness Security Fixes: #788 #789 #790)

- Command intent: Land three security fixes on `fix-harness-tier2-security` with strict TDD and per-bug commits — recipe steps bypass approval/policy (#788), git option injection via unvalidated refs (#789), and deploy `workspace` arg accepting any absolute path (#790).
- User intent: One approval of `run_recipe` must never expand into N unapproved steps; read-classified git tools must never become arbitrary file writes; the deploy tool must never package and upload arbitrary host directories.
- Success definition:
  - #788: both registration paths (`tools_default.go`, `tools/catalog.go`) build the recipe handler map AFTER `ApplyPolicy` wrapping and wrap the recipe tool individually; proven by `TestRunRecipeTool_PolicyAppliesToSteps`/`_PolicyAllowsSteps` and `TestDefaultRegistry_RecipeStepsRespectPolicy`/`_RecipeStepsAllowedByPolicy` (red pre-fix: bash step ran `exit_code:0` and the `pwned` marker file existed with no `permission_denied` in output; green post-fix). Side effect accepted: recipe-addressable membership expands to later-registered tools (script/workflow/deploy/deep-git/subagent/goals) — additive only, all policy-wrapped.
  - #789: all four ref-to-argv sites (`git_diff` target x2, `git_blame_context` rev, `git_diff_range` from/to) reject refs beginning with `-` via `tools.ValidateGitRef` before exec; proven by the table-driven `TestValidateGitRef_*` and five reject/accept tests across `tools`, `tools/core`, `tools/deferred` (red pre-fix: no error, git created the injected `--output=` file — verified empirically for `diff`, `blame`, and the glued `from..to` range; green post-fix). Glued `--since=`/`--grep=`/`-S` sites intentionally untouched (fixed option name, safe value position).
  - #790: deploy `workspace` override is unconditionally confined via `ResolveWorkspacePath` + `ConfineWorkspacePath(SandboxScopeWorkspace)` ahead of the detect branch so all four actions are covered; proven by `TestDeployTool_WorkspaceOverride_{OutsideRejected,InsideAllowed,RelativeInsideAllowed,TraversalRejected}` (red pre-fix — verified by stashing the fix: outside dir read succeeded with nil error, relative path used raw, traversal error lacked `escapes workspace`; green post-fix). Deliberate behavior change: relative values now resolve against the workspace root.
  - Three commits total, one per bug, each carrying its engineering-log entry (this ledger rides the third commit, as in tier-1); `go build ./internal/... ./cmd/...`, `go vet ./internal/harness/...`, `go test ./internal/harness/... -count=1` (7 pkgs), focused `-run 'Recipe|Policy|Git|Deploy' -v` (126 subtests), and `go test ./cmd/... -count=1` (35 pkgs) all green; clean `git status`; no merge/push (merge gate handled by the parent agent).
- Non-goals:
  - Broader git argv hardening beyond the four bare-ref sites (`--end-of-options` migration, `check-ref-format` validation — rejected in the #789 commit message).
  - Passing a sandbox scope into `DeployTool` so confinement could follow the caller's scope instead of being unconditional (no such plumbing exists; the unconditional default is the safe choice).
  - Deduping the `git_diff.go`/`core/git.go` and compact-history copies (scheduled for a later tier).
- Guardrails/constraints:
  - Strict TDD per bug: failing regression tests first, confirmed red for the expected reason, minimal fix, confirmed green; per-bug commits referencing their issue.
  - Anti-ghost-feature rule: `descriptions/*.md` updated to describe only implemented behavior; `TestToolDescriptionsContainBehavioralDirectives` and line-1 directive phrasing preserved.
  - No work outside the worktree; no merge, push, or `scripts/verify-and-merge.sh`.
- Open questions:
  - None for this tier.
- Next verification step: review the three-commit diff on `fix-harness-tier2-security`, then promote through the repo's normal verify-and-merge flow.
