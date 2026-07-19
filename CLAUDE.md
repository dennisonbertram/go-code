# CLAUDE.md

This repository is a Go coding harness with a streamed run API, a CLI smoke-test client, and a growing catalog of local and optional remote tools.

## Git & Merge Discipline

- **Merge at the end of a unit of work — do not leave branches sitting.** This repo's `main` moves fast (many concurrent squash-merged PRs) and subsystems get reimplemented in parallel, so a branch left unmerged drifts behind quickly and turns into a conflict-heavy, duplicated-work mess to reconcile. It gets messy if you don't.
- When a unit of work is reviewable: open the PR, get CI green (re-run known-flaky checks rather than merging red), and squash-merge to `main` promptly. Then delete the branch.
- Prefer small, scoped PRs that merge quickly over long-lived branches that accumulate multiple units of work. Always branch from the latest `origin/main`, not an older base.

## Current Source Of Truth

- The canonical implementation details are in `internal/server`, `internal/harness`, `internal/config`, `cmd/harnessd`, and `cmd/harnesscli`.
- The public-facing docs should stay aligned with the current routes, run request fields, event names, tool catalog, and environment variables.
- If a docs change reveals a mismatch, update the docs rather than preserving stale prose.

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

Wiring: `ServerOptions.ScriptWorkflows` accepts a `scriptWorkflowManager` interface.
15 POC tests in `internal/server/http_script_workflows_test.go` and `*_advanced_test.go`
cover CRUD, SSE streaming, resume, adversarial verify, loop-until-dry, concurrent fan-out,
and error recovery chains.

## Provider Note

- OpenAI is the primary provider path.
- Anthropic provider support exists in the provider catalog and should not be described as merely planned.

## Benchmarks

- `benchmarks/` and `harness_agent/` are Python (not Go). They need external pip deps (`terminal_bench`, `harbor`) that are not vendored here.
- Key-free deterministic smoke: `go test ./internal/server/... -run TestRunSmoke` (no key, no Docker).
- Shell smoke: `bash scripts/run-bench-smoke.sh` (builds harnessd, uses `HARNESS_PROVIDER=fake`).
- Full benchmark runbook (smokes, result schema, comparison harness, Python paths, honesty caveats): `docs/runbooks/benchmark-smoke.md`.

## Operational Reminder

- Respond concisely but educationally: explain what changed and why it matters.
  When a blocker or confusing implementation detail is solved, record the
  symptom, cause, and fix in the relevant durable log or plan note.
- Keep `docs/logs/long-term-thinking-log.md` in sync with any durable intent or success-criteria changes.
- Keep `docs/runbooks/` aligned with the current CLI and server behavior.
- For a new worktree, run `scripts/init.sh <task-slug>` first. `scripts/bootstrap-worktree.sh` is only a compatibility wrapper. `scripts/init.sh` creates the worktree, downloads dependencies, builds local binaries, writes a sourceable env file, and can start `harnessd` in tmux when requested.
### Agent Client Protocol (ACP)

`harness-acp` is the stdio ACP entrypoint for editor integrations. It proxies
ACP session lifecycle, streamed updates, cancellation, and approvals to the
existing harnessd HTTP/SSE API. See `docs/runbooks/acp.md` for setup and the
manual Zed verification checklist.
