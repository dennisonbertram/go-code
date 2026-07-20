# Plan: Session Visualizer — Slices 1–2

Epic: #812 (parent #803).

## Slice 1 (merged, PR #829): embedded /viz static shell behind Bearer auth

## Context

- Problem: harnessd exposes runs/events/summary over HTTP but has no UI; inspecting a run requires `harnesscli` or raw `curl`.
- User impact: users cannot open a browser to inspect runs.
- Constraints: embedded static assets only (`go:embed`), no build step, no CDN, no new auth mechanism, no mutating endpoints, read-only under existing Bearer auth + `runs:read` scope.

## Scope

- In scope:
  - New `internal/server/viz` package: `//go:embed static`, handler serving the shell.
  - `internal/server/http.go` `buildMux()`: register `/viz` and `/viz/` wrapped in `auth(read(...))`.
  - Static shell: `index.html` + `app.js` + `style.css`; token via landing form or `?token=`, stored in `sessionStorage`, `fetch("/v1/runs?limit=1")` with Bearer header to prove connectivity; hash-based placeholders for list/detail views (no client routing yet).
- Out of scope: slices 2–6 (harnesscli subcommand, real list/detail views, timeline, search, docs); any new endpoint, event type, or persistence.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none this slice (docs land in slice 6).
- Spec docs to update before code: none.
- Implementation notes to add after code: none this slice.

## Test Plan (TDD)

- New failing tests to add first (`internal/server/http_viz_test.go`):
  - `GET /viz/` without token → 401.
  - `GET /viz` without token → 401 (registered behind middleware, no unauthenticated redirect leak).
  - `GET /viz/` with a key holding no scopes → 403 `insufficient_scope` / `required: runs:read`.
  - `GET /viz/` with `runs:write`-only key → 200 (documented superscope rule: `runs:write` satisfies `runs:read`, consistent with every other read route — the issue text says 403 here but the codebase rule wins; flagging in PR).
  - `GET /viz/` with `runs:read` → 200, `text/html`, contains shell markup.
  - `GET /viz/app.js` → 200 + JavaScript content type; `GET /viz/style.css` → 200 + CSS content type.
  - `GET /viz` with `runs:read` → redirect to `/viz/`.
  - Path traversal (`/viz/../`, `%2e%2e`) does not serve embedded content.
  - Regression: `/healthz` still 200 unauthenticated.
- Existing tests to update: none.
- Regression tests required: `go test ./internal/server/... -count=1`.

## Cross-Surface Impact Map

- Not a provider/model flow change: Config — None; Server API — two new mux registrations only; TUI state — None; Regression tests — existing `internal/server` suite must stay green.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first; watch them fail (404).
- [x] Implement `internal/server/viz` (embed + handler) and mux registration.
- [x] Static shell files.
- [x] Tests green; gofmt/go vet clean.
- [x] Update `docs/plans/INDEX.md`.
- [x] Run touched-package tests.
- [x] Commit, push `epic/812-session-visualizer`, open PR against repo.

## Risks and Mitigations

- Risk: issue text says runs:write-only key → 403, but `hasScope` grants runs:write → runs:read (superscope, tested in `auth_scope_test.go`).
- Mitigation: assert 200 for runs:write and 403 for a scope-less key; document the deviation in the PR body.

---

## Slice 2 (this branch): `feat(harnesscli): add viz subcommand that prints/opens the visualizer URL`

### Context

- Problem: users must know the `/viz/` URL exists to use the shell from slice 1.
- User impact: one-command path into the UI: `harnesscli viz` / `harnesscli viz --open`.
- Constraints: no real browser launch in tests (inject the opener); base URL resolved the same way `runStatus`/`runList` do (`-base-url` flag, default `http://localhost:8080`); no token in the printed URL.

### Scope

- In scope:
  - `cmd/harnesscli/auth.go` `dispatch`: add `case "viz": return runViz(args[1:])`.
  - New `cmd/harnesscli/viz.go`: `runViz` prints `<base>/viz/`; `--open` launches the OS browser (`open` on darwin, `xdg-open` elsewhere) via an injectable package var (precedent: `servicePlatform`/`serviceRunLifecycle` in `service.go`), falling back to the already-printed URL on failure.
- Out of scope: slices 3–6; embedding tokens in the URL; probing the daemon's reachability.

### Test Plan (TDD)

- New failing tests (`cmd/harnesscli/viz_test.go`):
  - `viz` prints `http://localhost:8080/viz/` by default.
  - `viz -base-url http://host:9000/` trims the trailing slash and prints `http://host:9000/viz/`.
  - positional args → exit 1 with usage on stderr.
  - `viz --open` invokes the injected opener with the URL; success → exit 0.
  - opener failure → exit 1, stderr diagnostic, URL still printed (fallback).
  - `vizOpenerName`: darwin → `open`, linux → `xdg-open`.
  - dispatch routes `viz` to `runViz`.
- Existing tests to update: none.
- Regression tests required: `go test ./cmd/harnesscli/... -count=1`.

### Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first; watch them fail (undefined symbols).
- [x] Implement `cmd/harnesscli/viz.go` and the dispatch case.
- [x] Tests green; gofmt/go vet clean.
- [x] Update this plan.
- [x] Run touched-package tests.
- [x] Commit, push `epic/812-session-visualizer-s2`, open PR against repo. (Merged, PR #857.)

---

## Slice 3 (this branch): `feat(server): runs list and run detail views in /viz`

### Context

- Problem: the slice-1 shell only proves connectivity; users see placeholders, not runs.
- User impact: list runs newest-first, click into a run's metadata + summary (tokens/cost where present) without leaving `/viz`.
- Constraints: no new endpoints; client-side filtering only; JS stays plain (no build step); read-only GET calls only.
- API facts verified in code:
  - `GET /v1/runs` → `{"runs": [...]}` of serialized `store.Run` (id, conversation_id, tenant_id, agent_id, model, provider_name, prompt, status, output, error, recap, created_at, updated_at), ordered `created_at DESC` (sqlite.go:269). Cost/usage fields are `json:"-"` — never present in list payloads; the cost column renders "—" unless a field appears (`total_cost_usd`/`cost_usd` probed defensively).
  - `GET /v1/runs/{id}` → runner state or `storeRunToHarness` map (same field names).
  - `GET /v1/runs/{id}/summary` → `RunSummary{run_id,status,steps_taken,total_prompt_tokens,total_completion_tokens,total_cost_usd,cost_status,tool_calls,cache_hit_rate,error}` — runner in-memory only: 404 for historical runs, 409 for unfinished runs; UI renders "summary unavailable" gracefully.

### Scope

- In scope:
  - `internal/server/viz/static/app.js`: hash routing `#/runs` / `#/runs/{id}`; list view (status/model/created/cost columns, prompt excerpt linking to detail); detail view (metadata + summary payload); client-side status + substring filters; 401/403 → token form; empty store / empty filter / unknown-run / summary-unavailable states.
  - `internal/server/viz/static/index.html`: crumb text update (list/detail exist now).
  - `internal/server/viz/static/style.css`: table, filter bar, badges, detail grid, state messages.
  - Guard test: parse served `index.html` for `/viz/` asset refs, assert each resolves 200 under a `runs:read` key.
- Out of scope: slices 4–6 (timeline, search, ops doc); any endpoint or payload change; client re-sorting (server order is authoritative).

### Test Plan (TDD)

- Note per slice spec: JS is test-light by design. The behavior change is client-side only, so there is no new Go-observable behavior to red-green; the prescribed Go test is a guard against broken embeds/paths, run before and after the JS rewrite. JS correctness is verified by `node --check` (syntax) and a seeded-daemon end-to-end smoke (curl the exact endpoints the views consume), with a scripted browser checklist in the PR.
- New guard test (`internal/server/http_viz_test.go`): extract `/viz/...` refs from served `index.html` (`src=`/`href=`), GET each under `runs:read` → 200.
- Existing tests to update: none.
- Regression tests required: `go test ./internal/server/... -count=1`.

### Implementation Checklist

- [x] Verify API response shapes and store ordering in code.
- [x] Add guard test; run before JS rewrite.
- [x] Rewrite `app.js` (routing, list, detail, filters, states); update `index.html` crumbs; extend `style.css`.
- [x] `node --check` the JS; gofmt/go vet clean.
- [ ] Run touched-package tests.
- [x] Seeded-daemon e2e smoke (HARNESS_RUN_DB + seeded key/runs; curl list/detail; confirm new assets served) + DOM-stubbed render smoke (15/15).
- [ ] Commit, push `epic/812-session-visualizer-s3`, open PR against repo.
