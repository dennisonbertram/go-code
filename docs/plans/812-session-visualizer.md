# Plan: Session Visualizer — Slice 1: embedded /viz static shell behind Bearer auth

Epic: #812 (parent #803). This plan covers **Slice 1 only**.

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
- [ ] Commit, push `epic/812-session-visualizer`, open PR against repo.

## Risks and Mitigations

- Risk: issue text says runs:write-only key → 403, but `hasScope` grants runs:write → runs:read (superscope, tested in `auth_scope_test.go`).
- Mitigation: assert 200 for runs:write and 403 for a scope-less key; document the deviation in the PR body.
