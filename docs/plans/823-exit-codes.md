# Plan: headless exit-code contract — epic #823, Slice 1 (docs)

## Context

- Problem: `harnesscli -prompt ...` exits 0 for every terminal run state (including `run.failed` and `run.cancelled`), so shell scripts and CI cannot branch on run outcomes without parsing stdout.
- User impact: headless automation (CI, wrapper scripts) gets no reliable success/failure signal.
- Constraints: Slice 1 is docs-only — ratify and document the exit-code table; no behavior changes (Slices 2–4 implement). Worktree-only flow; no merge to main from this branch.

## Scope

- In scope:
  - New `website/docs/reference/exit-codes.md`: full exit-code table (run terminal events, waiting/blocked states, client errors, interrupt), kimi-code 0/3/6 alignment rationale, reserved goal-status mapping, `max_turns.exhausted` and `run.cost_limit_reached` semantics, precise blocked signals, current-vs-contracted table.
  - Cross-links from `website/docs/cli/harnesscli.md` and `website/docs/reference/events-catalog.md`.
  - `docs/plans/INDEX.md` entry for this plan.
- Out of scope: implementing the mapping (Slice 2), blocked detection (Slice 3), e2e assertions and per-command doc updates (Slice 4). No Go code changes.

## Documentation Contract

- Feature status: `planned` (contract ratified by this slice; implementation in later slices — the page states this explicitly per `docs/runbooks/documentation-maintenance.md` "public docs describe implemented behavior only", so the page clearly separates current behavior from contracted behavior).
- Public docs affected: `website/docs/reference/exit-codes.md` (new), `website/docs/cli/harnesscli.md`, `website/docs/reference/events-catalog.md`.
- Spec docs to update before code: this plan.
- Implementation notes to add after code: none for Slice 1 (docs-only).

## Test Plan (TDD)

- New failing tests to add first: none — epic designates Slice 1 as docs-only ("no behavior tests apply; reviewer validates the table against the event constants in `internal/harness/events.go`").
- Existing tests to update: none.
- Regression tests required: none for this slice. Validation = website build (Docusaurus broken-link check) + manual trace of every documented code to an event constant, run status, or current CLI behavior.

## Cross-Surface Impact Map

- None — docs-only slice; no provider/model flows, gateway routing, model catalogs, API-key management, or server/TUI provider plumbing touched.

## Implementation Checklist

- [x] Verify every epic citation against the source (event constants, run statuses, CLI return paths, wrapper propagation).
- [x] Write `website/docs/reference/exit-codes.md`.
- [x] Cross-link from `website/docs/cli/harnesscli.md` and `website/docs/reference/events-catalog.md`.
- [x] Update `docs/plans/INDEX.md`.
- [x] Build the website to validate links (`npm run build` green with `onBrokenLinks: 'throw'`); no Go packages touched, so no Go tests apply.
- [ ] Commit, push `epic/823-exit-codes`, open PR against the repo (no merge).

## Risks and Mitigations

- Risk: contract page drifts from code reality (documents behavior that does not exist yet).
- Mitigation: explicit "current vs. contracted behavior" table and a traceability table mapping every code to its source constant/status/CLI path; page is labeled as the contract target for Slices 2–4.
