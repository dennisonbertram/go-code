# Long-Term Thinking Log

## 2026-07-19 (Theme System Slice 4 — Epic #810)

- Command intent: Implement only slice 4 of epic #810 — persist the /theme selection to ~/.config/harnesscli/config.json and apply it at startup — on branch `epic/810-theme-system-s4`, strict TDD, push and open a PR without merging.
- User intent: select a theme once and keep it across restarts; deleting or breaking the theme file must never prevent the TUI from starting.
- Success definition: `Config` round-trips a `theme,omitempty` field; picker select persists after successful apply; `newTUIConfig` fills `TUIConfig.Theme` from the saved config; `tui.New` resolves it via the slice-1 loader with silent default fallback (missing/malformed file); config-panel `theme` row reflects the active theme; `go test ./cmd/harnesscli/... -count=1` green; no test touches the real HOME.
- Guardrails/constraints: slice 4 only — docs site and example theme are slice 5; reuse the existing load-mutate-save config pattern (errors ignored, consistent with gateway/starring); no changes to slice-1 loader semantics.

## 2026-07-19 (Theme System Slice 3 — Epic #810)

- Command intent: Implement only slice 3 of epic #810 — a `/theme` picker overlay that re-scans the themes directory on every open and applies a selection live — on branch `epic/810-theme-system-s3`, strict TDD, push and open a PR without merging.
- User intent: drop a JSON theme file into `~/.config/harnesscli/themes/`, open `/theme`, see it listed, select it, and watch the TUI restyle without a restart; a broken theme file must never strand the user without a theme.
- Success definition: themepicker component mirrors profilepicker's pattern; `/theme` registered and wired at the same sites as `/profiles`; re-open after adding a file lists it; Enter resolves via the slice-1 loader and applies via slice-2 `SetTheme` (statusbar restyle proves it); malformed JSON keeps the current theme and sets an error status; `go test ./cmd/harnesscli/... -count=1` green.
- Guardrails/constraints: slice 3 only — no persistence (slice 4), no docs site/example (slice 5); `themesDir` model field is the test seam so tests never touch the real home directory; picker styling itself mirrors profilepicker and stays outside the themed-component set.

## 2026-07-19 (Theme System Slice 2 — Epic #810)

- Command intent: Implement only slice 2 of epic #810 — thread the resolved theme through statusbar, diffview, messagebubble, approval overlay, and spinner — on branch `epic/810-theme-system-s2`, strict TDD, push and open a PR without merging.
- User intent: loading a theme (slice 1 loader) visibly restyles the TUI; `Model.theme` stops being write-only; slice 3's picker gets a working hot-reload foundation.
- Success definition: each named component accepts injected styles (`Styles` + `DefaultStyles()`); `SetTheme` re-distributes to live and ephemeral components (render funnels `renderMessageBubble`/`appendToolUseView`); styles survive statusbar re-creation on resize and spinner re-creation on run start; injecting distinctive warning/diffAdd colors visibly changes statusbar and diff rendering in tests; default rendering stays byte-identical everywhere except the previously-unstyled approval overlay (deliberate, documented); `go test ./cmd/harnesscli/... -count=1` green.
- Guardrails/constraints: slice 2 only — no picker (slice 3), no persistence (slice 4), no docs site/example (slice 5); tooluse chrome, plan-approval overlay, inputarea, and model.go overlay literals stay hardcoded (outside the epic's named set); zero-drift token mappings unless a path was unstyled before.

## 2026-07-20 (TUI Subscription Credential Import — Issue #854)

- Command intent: make the existing Codex and Kimi subscription imports usable directly from `/keys`, reload the live daemon, test and document the exact same-host security boundary, then push and open a PR.
- User intent: an already logged-in vendor CLI should become immediately usable by a running `harnessd` without a second terminal or restart.
- Success definition: startup availability reads the harness-owned stores; bodyless provider-scoped import endpoints reuse existing importers and bootstrap token-source wiring; `/keys` `i` imports only subscription rows and refetches live status; fake fixtures prove both provider transitions and absent vendor login remains actionable.
- Guardrails: no token request/response field, vendor files read-only, no credential logging, and the daemon-host-only limitation is explicit.

## 2026-07-20 (Kimi Code Subscription Auth — Epic #848)

- Command intent: Let an existing Kimi Code CLI subscription authenticate the separate `kimi-subscription` provider, with strict test-first slices, verification, PR creation, and no vendor credential writes.
- User intent: Reuse an already-paid Kimi Code subscription safely through go-code, including ordinary 15-minute access-token refreshes.
- Success definition: read-only CLI credential import into `~/.harness/subscription-auth/kimi.json`; `auth kimi login|status|logout`; 30-second refresh safety margin; derived (not duplicated) Kimi model catalog; dynamic bearer plus client headers; fake-server completion/refresh integration coverage; no credential logging.
- Guardrails: Live `/api/oauth/token` was confirmed only as POST-capable by one unauthenticated OPTIONS probe. No authenticated refresh or completion was sent to the live service; conventional OAuth2 form details and OpenAI compatibility require manual verification before production reliance.

## 2026-07-20 (Codex ChatGPT-Subscription Authentication — Epic #847)

- Command intent: Implement, test, commit by slice, push, and open (without merging) the `codex-subscription` authentication provider.
- User intent: Reuse an existing ChatGPT-authenticated vendor Codex session without a separate metered OpenAI API key.
- Success definition: a read-only import creates a `0600` harness-owned credential; refresh uses the verified OAuth contract; the catalog mirrors OpenAI models structurally; harnessd sends the dynamic bearer and account header; CLI/TUI surface safe status; a fake HTTPS request survives forced expiry refresh.
- Guardrails: never write under `~/.codex`, never log credentials, no OAuth dependency, retain OpenAI API-key behavior, and explicitly report whether live ChatGPT validation was possible.

## 2026-07-20 (Subscription-Auth Foundation — Epic #846)

- Command intent: Add provider-layer dynamic bearer credential and extra-header plumbing, a generic refresh cache, and registry token-source support; commit each test-first slice, verify, push, and open a PR without merging it.
- User intent: Establish a safe reusable foundation for expiring Codex and Kimi subscription credentials without implementing either provider's OAuth or disk import yet.
- Success definition: `provider.TokenSource` and `StaticToken` exist; OpenAI-compatible requests dynamically obtain bearer tokens and apply static extra headers; a provider-neutral cache single-flights refreshes and retains a still-valid token when refresh fails; the registry treats a token source as credentials and forwards it to factories; static-key behavior remains unchanged.
- Guardrails: Never log, print, fixture, or expose token values; use obviously-fake placeholders only; no OAuth dependency, TUI edits, refresh endpoints, or credential persistence.

## 2026-07-19 (Theme System Slice 1 — Epic #810)

- Command intent: Implement only slice 1 of epic #810 — theme token schema and JSON loader with base-palette fallback — on branch `epic/810-theme-system`, strict TDD, then push and open a PR without merging.
- User intent: Users can drop JSON theme files into `~/.config/harnesscli/themes/` and have them resolve into complete themes without ever breaking TUI rendering (kimi-code parity foundation for slices 2–5).
- Success definition: 17-token schema (string or adaptive `{light,dark}` values) loads from `<name>.json`; omitted/invalid tokens fall back per token and per side to the built-in dark/light base palette; built-ins `default-dark`/`default-light` equal `DefaultTheme()`; `List` returns built-ins + sorted filename-derived names; token→style mapping covers every `Theme` field; `go test ./cmd/harnesscli/tui/... -count=1` green with zero default-appearance drift; malformed JSON errors while still returning a usable base theme.
- Guardrails/constraints: slice 1 only — no component re-wiring (slice 2), no picker (slice 3), no persistence (slice 4), no website docs/example file (slice 5). `theme.go` untouched; overlay resolution onto `DefaultTheme()` copies; tests first per `docs/runbooks/testing.md`.

## 2026-07-19 (Agent Client Protocol Server Mode — Epic #746)

- Command intent: Implement ACP server mode and all eight child slices (#751, #754, #758, #760, #771, #772, #773, #774), commit each slice, then push and open a PR without merging it.
- User intent: Let ACP editors start and continue harnessd conversations, receive live assistant/tool/plan state, decide approvals, and cancel a turn without creating a second execution path.
- Success definition: `harness-acp` completes ACP initialize over stdio; session/new and session/prompt map to existing harnessd APIs; SSE is projected as ACP updates; approval/cancel/todos map to their existing APIs; fake-provider integration is key-free; docs include a Zed checklist.
- Guardrails/constraints: SDK v0.13.5 is compatible with Go 1.25 (requires Go 1.21+); use it rather than hand-rolling transport. The adapter owns wire translation only and must reuse harnessd HTTP/SSE, approval broker routes, and todo state.

## 2026-07-19 (Plan Mode — Epic #740)

- Command intent: Implement all six plan-mode slices in this worktree, verify, push, and open a PR without merging it.
- User intent: Make planning genuinely read-only except for a single designated plan artifact, and require the operator's approval before implementation edits can proceed.
- Success definition: `RunRequest.PlanMode` reaches live runner state; real policy-wrapped tool dispatch rejects non-plan mutations; plan exit pauses through the existing broker/routes; plan content persists with its conversation; both CLI surfaces render and submit the state.
- Guardrails: reuse `ApplyPolicy`, permission-rule matching, `ApprovalBroker`, and SQLite migrations; do not touch #567; six test-first commits plus a merge commit.

## 2026-07-19 (Session Rewind — Epic #739)

- Command intent: Implement all six rewind slices in this worktree, push the branch, and open (without merging) a PR.
- User intent: Safely undo a chosen sequence of agent file edits and conversation turns without asking a model to recreate historical file contents.
- Success definition: Mutating file tools capture bounded pre-images; restore detects external modifications, restores/deletes files, truncates the persisted conversation, is reachable through tenant-scoped HTTP and a confirmed TUI `/rewind` picker, and snapshot data follows conversation retention.
- Guardrails: Reuse existing mutation classification and SQLite conversation store; capture failures never fail tool calls; never touch the unrelated human-checkpoint subsystem; every slice is test-first and committed separately.

## 2026-07-19 (Multi-run TUI Dashboard — Epic #738)

- Command intent: Deliver the multi-run TUI dashboard and six child slices in a dedicated branch, then open (but do not merge) its PR.
- User intent: Let an operator monitor and control concurrent harness runs without leaving the current TUI session.
- Success definition: `/dashboard` offers lifecycle-bound polling grouped by run state, selected-run SSE peek, steer/cancel, and new-run dispatch solely through existing run routes; focused TDD coverage and project gates remain green.
- Guardrails: TUI-only, no new server endpoints or Go dependencies, and no more than one dashboard peek bridge at a time.

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

