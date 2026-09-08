# CLAUDE.md

This repository is a Go coding harness with a streamed run API, a CLI smoke-test client, and a growing catalog of local and optional remote tools.

## Session rewind

`GET /v1/conversations/{id}/rewind-points` lists snapshot points. `POST /v1/conversations/{id}/rewind` restores a `point_id` (and accepts `force`). This is destructive: it writes files and truncates later conversation history; normal restore refuses externally modified files. The TUI command is `/rewind <point-id> confirm`.

## Git & Merge Discipline

- Every change requires a GitHub issue created from a current structured Issue
  Form before a branch or implementation starts. Every PR must close that
  same-repository issue with `Closes #N`.
- Do not begin implementation until acceptance criteria, scope boundaries,
  current-architecture search evidence, cross-surface impact analysis,
  test-first evidence, verification, and rollout/rollback are concrete in the
  issue. Update the issue before continuing if the design or scope changes.
- Minor changes do not bypass the issue/PR contract. The minor form is strictly
  documentation-only, limited to allowed paths, 5 files, and 150 changed lines.
  Any source, test, workflow, dependency, API, config, schema, persistence,
  security, deployment, provider/model, UI copy, or runtime behavior change
  must use the full engineering-change or bug form.
- Epics do not authorize implementation directly. Close a shippable child issue
  and reference the epic as related. Research PRs are documentation-only.
- This is currently a process-guided pilot rather than a GitHub
  branch-protection gate. The absence of a required status check is not
  permission to skip the issue, impact analysis, or PR contract.
- **Merge at the end of a unit of work — do not leave branches sitting.** This repo's `main` moves fast (many concurrent squash-merged PRs) and subsystems get reimplemented in parallel, so a branch left unmerged drifts behind quickly and turns into a conflict-heavy, duplicated-work mess to reconcile. It gets messy if you don't.
- When a unit of work is reviewable: open the PR, get CI green (re-run known-flaky checks rather than merging red), and squash-merge to `main` promptly. Then delete the branch.
- Prefer small, scoped PRs that merge quickly over long-lived branches that accumulate multiple units of work. Always branch from the latest `origin/main`, not an older base.
- **An issue is a ticket to work, not a place to park a problem.** Filing it is
  the first step of the job, not the deliverable. Having filed one, implement
  it, verify it, open the PR, get CI green, merge, and close it in the same
  stretch of work. Stopping after filing to ask whether to proceed turns a
  ticket into a question and leaves the work undone. The only exception is when
  the user asked for the issue alone.
- If a problem is discovered mid-task and is genuinely separate, file it as its
  own ticket, say so, and finish the task in hand — then work the new ticket
  unless the user redirects. Discovering work is not a reason to stop doing
  work.
- If a diagnosis turns out to be wrong after the issue is filed, correct the
  issue before implementing. Never build a fix against a story the evidence has
  already contradicted, and never close an issue whose stated cause was never
  confirmed — say what remains unproven and leave it open.

## Current Source Of Truth

- The canonical implementation details are in `internal/server`, `internal/harness`, `internal/config`, `cmd/harnessd`, and `cmd/harnesscli`.
- **There is exactly one tool catalog.** Tool implementations live in `internal/harness/tools/core` (always-on) and `internal/harness/tools/deferred` (activated on demand); `internal/harness/tools` itself holds only shared infrastructure (types, policy, sandbox, path confinement, SSRF guard, job manager) plus `find_tool` and `reset_context`. `NewDefaultRegistryWithOptions` (`internal/harness/tools_default.go`) is the single builder; consumers needing a flat `[]tools.Tool` use `Registry.CatalogTools()`. Do not add a second catalog builder — the previous one drifted from this one and security fixes had to be written twice.
- The public-facing docs should stay aligned with the current routes, run request fields, event names, tool catalog, and environment variables.
- If a docs change reveals a mismatch, update the docs rather than preserving stale prose.
- Installable plugin bundles live in `internal/plugins`: `plugin.json` bundles are installed under `~/.go-harness/plugins`, with enabled visibility independent from executable trust. Skills/commands reuse their existing loaders; trusted bundles alone reach profiles, MCP validation, and hooks. This is separate from compile-time Go plugins in `plugins/`.

## Workflow Engine (`internal/workflow/`)

A Claude Code-style workflow orchestration engine for composing multi-agent pipelines:

- **Script-based**: Register `Script` functions (Go `func(ctx *Context) (any, error)`) with `Engine.Register(name, script)`.
- **Core primitives**: `ctx.Agent()` (sub-agent), `ctx.Parallel()` (barrier), `ctx.Pipeline()` (no-barrier stages), `ctx.Phase()` (progress), `ctx.Log()` (messages), `ctx.Workflow()` (nested).
- **Budget tracking**: `ctx.Budget` with `Spent()`, `Remaining()`, `Spend()`, `Clone()` — shared across nested workflows.
- **Schema validation**: `workflow.ValidateSchema(schema, data)` and `workflow.ParseStructuredOutput(output, schema)` with JSON Schema subset + markdown extraction.
- **Concurrency**: Semaphore-capped at `min(16, NumCPU-2)`. Only `Agent()` acquires the semaphore — `Parallel`/`Pipeline` goroutines do not, preventing deadlocks when thunks/stages call `Agent()`.
- **Events**: `workflow.started`, `workflow.agent.{started,completed,failed}`, `workflow.phase.started`, `workflow.log`, `workflow.{completed,failed}` — subscribable via `Engine.Subscribe(runID)`.
- **Resume**: Failed runs can be resumed via `Engine.Resume(ctx, runID, args)`.
- **Storage**: In-memory by default; pluggable `Store` interface for persistence.
- **Tests**: `internal/workflow/engine_test.go` (unit) and `internal/workflow/comprehensive_test.go` (22 scenario tests covering all primitives, combinations, edge cases, and real-world patterns).

### Script Workflow HTTP API

The workflow engine is exposed via HTTP routes in the server (`internal/server/http_script_workflows.go`):

- `GET /v1/script-workflows` — list registered script workflows
- `GET /v1/script-workflows/{name}` — get workflow metadata
- `POST /v1/script-workflows/{name}/runs` — start a workflow run with args
- `GET /v1/script-workflow-runs/{id}` — get run status and result
- `GET /v1/script-workflow-runs/{id}/events` — SSE event stream
- `POST /v1/script-workflow-runs/{id}/resume` — resume a failed run

### Lifecycle Hooks HTTP API

Config-driven lifecycle hooks (shell/HTTP, epic #737) are listed via:

- `GET /v1/hooks` — startup-computed listing of loaded hooks (name, event, kind, source, matcher)
  and skipped hook files (file, reason: untrusted / modified_since_trusted / invalid).
  Read-only; trust is managed offline with `harnesscli hooks trust|revoke|list`.
  See `docs/design/plugins.md` → "Config-driven hooks" for the hook-file schema and wire protocol.

### TUI Dashboard

`/dashboard` (or `Ctrl+D`) is a TUI-only multi-run overlay. It uses the existing
`/v1/runs`, run-control, and SSE event routes; do not add dashboard server routes.

Wiring: `ServerOptions.ScriptWorkflows` accepts a `scriptWorkflowManager` interface.
20 POC tests in `internal/server/http_script_workflows_test.go` and `*_advanced_test.go`
cover CRUD, SSE streaming, resume, adversarial verify, loop-until-dry, concurrent fan-out,
and error recovery chains.

### Enforced Plan Mode

`harnesscli --plan-mode` and the TUI `Ctrl+O` toggle send `plan_mode` in the
normal run request. The runner injects live per-run state into the central
`ApplyPolicy` wrapper: while active, mutations fail closed unless the
permission-rule matcher accepts the designated `.harness/plan.md` plan file.
Plan exit uses the existing approval broker and `/v1/runs/{id}/approve|deny`;
the SQLite conversation store persists the latest plan content per conversation.

## Provider Note

- OpenAI is the primary provider path.
- Anthropic provider support exists in the provider catalog and should not be described as merely planned.
- Subscription-auth foundation: `internal/provider.TokenSource` supplies request-time bearer credentials; `internal/provider/tokencache` supplies generic in-memory expiry-margin refresh caching. OpenAI-compatible clients accept optional `TokenSource` and static `ExtraHeaders`; the provider registry exposes `SetTokenSource`, which evicts its cached client. Never log credential values. Codex/Kimi OAuth refresh transport, credential import, persistence, and TUI surfaces are intentionally separate follow-on work.

### Kimi Code subscription auth (Epic #848)

- `harnesscli auth kimi login|status|logout` manages only `~/.harness/subscription-auth/kimi.json`; it must never write under `~/.kimi-code/`.
- The subscription provider mirrors models from metered `kimi` via `models_from`, uses a 30-second safety margin for 900-second tokens, and sends `X-Kimi-Client-*` headers through the OpenAI-compatible client.
- Do not claim live Kimi OAuth or completion verification: only one unauthenticated OPTIONS probe confirmed `POST` is allowed at `/api/oauth/token`; fake-server tests cover the convention-based OAuth request.

## Benchmarks

- `benchmarks/` and `harness_agent/` are Python (not Go). They need external pip deps (`terminal_bench`, `harbor`) that are not vendored here.
- Key-free deterministic smoke: `go test ./internal/server/... -run TestRunSmoke` (no key, no Docker).
- Shell smoke: `bash scripts/run-bench-smoke.sh` (builds harnessd, uses `HARNESS_PROVIDER=fake`).
- Full benchmark runbook (smokes, result schema, comparison harness, Python paths, honesty caveats): `docs/runbooks/benchmark-smoke.md`.

## Delegation

- **Use `/efficient-fable` for token-heavy work.** Delegate the parts that
  consume context without needing judgment: broad repo or docs searches, full
  regression sweeps and log reduction, documentation drafting from a settled
  design, and live browser or pty capture runs. Keep the decision layer —
  architecture, diagnosis, resolving conflicting reports, the final diff review,
  and what to tell the user — with the primary agent.
- Write a handoff packet as if the subagent has no context: repo path, exact
  objective, files in and out of scope, the evidence format to return, the
  commands to run and what success looks like, and stop conditions. Tell it to
  stop and report rather than improvise when the code does not match the prompt.
- **Treat subagent reports as leads, not facts.** Before acting on a finding,
  opening a PR, or telling the user something is done, reopen the cited files
  and confirm the line references and failures yourself. Subagents have reported
  confidently wrong results in this repo, including a build failure that was
  pre-existing and unrelated, and a test failure caused by another session's
  processes rather than the change under review.
- Run independent slices in parallel; keep coupled or delicate work local. Small
  tasks stay direct — a subagent that costs more to brief than to do is waste.

## Operational Reminder

- Respond concisely but educationally: explain what changed and why it matters.
  When a blocker or confusing implementation detail is solved, record the
  symptom, cause, and fix in the relevant durable log or plan note.
- Keep `docs/logs/long-term-thinking-log.md` in sync with any durable intent or success-criteria changes.
- Keep `docs/runbooks/` aligned with the current CLI and server behavior.
- **Green tests are not proof the change works — drive the real thing.** A
  change is not done until the actual binary, TUI, or CLI has been exercised on
  the path the user takes, and the observed result reported. Report what you
  saw, not that you ran something.
  - What counts: launching the rebuilt binary and using the feature; driving the
    TUI in a pty and reading the rendered frames; running the CLI end to end and
    inspecting its output and exit code; checking the file or database the
    change was supposed to write. `docs/runbooks/harnesscli-live-testing.md` and
    `docs/runbooks/benchmark-smoke.md` describe how; the fake provider makes
    most of it key-free.
  - What does not count: a passing unit test, a successful build, or a
    subagent's assurance that it worked.
  - State plainly what you could **not** exercise and what therefore rests on
    unit tests alone. An honest gap is a result; a silent one is a false claim.
  - This is not ceremony. Real cases from this repo: a spinner label that passed
    every test and still ate the cancel hint at 40 columns; colour detection that
    passed its tests while never colouring stdout, because the check ran inside
    a command substitution; a persistence change that passed unit tests while
    the suite quietly wrote into the developer's real config file. Each was
    green and wrong, and each was caught only by looking at the real output.
- **Rebuild the apps after changing them — merging is not shipping.** The
  installed binaries are build artifacts and a running process holds its code in
  memory, so a merged fix stays invisible until it is rebuilt and the app is
  restarted. This has twice looked like "the fix didn't work" when the fix was
  fine and the binary was stale.
  - Go binaries (`cmd/harnessd`, `cmd/harnesscli`, or anything under
    `internal/` they reach): run `scripts/install.sh`, which reinstalls
    `go-code`, `harnesscli`, and `harnessd` into `~/.local/bin`.
  - macOS app (`macapp/`): run `swift build` in `macapp/`.
  - Say so when reporting done, and tell the user to restart any running TUI or
    app — reinstalling does not touch a live process.
  - Docs-only changes need no rebuild; state that rather than skipping silently.
    If a rebuild fails, report the failure instead of calling the change done.
- For a new worktree, run `scripts/init.sh <task-slug>` first. `scripts/bootstrap-worktree.sh` is only a compatibility wrapper. `scripts/init.sh` creates the worktree, downloads dependencies, builds local binaries, writes a sourceable env file, and can start `harnessd` in tmux when requested.
### Agent Client Protocol (ACP)

`harness-acp` is the stdio ACP entrypoint for editor integrations. It proxies
ACP session lifecycle, streamed updates, cancellation, and approvals to the
existing harnessd HTTP/SSE API. See `docs/runbooks/acp.md` for setup and the
manual Zed verification checklist.

## 2026-07-20 — Live model discovery (Epic #849)

- Live model discovery is provider-agnostic: configured OpenRouter, OpenAI, Anthropic, and DeepSeek entries refresh on a five-minute TTL. Discovery failures never remove static models; cached results are served stale after a failed refresh, and static metadata wins on ID conflicts.
### Codex subscription auth (Epic #847)

- `codex-subscription` reuses a ChatGPT-authenticated vendor Codex session through a harness-owned credential copy only. Never write under `~/.codex/`; import from it is read-only.
- Setup is `codex login` followed by `harnesscli auth codex login`; `status` is safe to display and `logout` removes only `~/.harness/subscription-auth/codex.json`.
- Keep this provider additive: `openai` remains the documented `OPENAI_API_KEY` path. Do not log access, refresh, or ID token values.

### In-TUI subscription import (Issue #854)

- In `/keys`, select `codex-subscription` or `kimi-subscription` and press `i` to import and activate the vendor CLI login without restarting `harnessd`. The action refetches the live provider status on success.
- The request is bodyless and imports only from vendor files already present on the **harnessd host**. It cannot transfer a TUI machine's credential to a remote daemon: log into the relevant vendor CLI (`codex login` or `kimi-code login`) on the daemon host, then retry.
