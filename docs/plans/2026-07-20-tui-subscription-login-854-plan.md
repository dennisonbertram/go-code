# Plan: TUI subscription credential import (#854)

## Context

- Problem: `/keys` uses a nonexistent subscription environment variable as an early availability hint, and a running daemon cannot import/reload a vendor CLI credential.
- User impact: operators can import an existing Codex or Kimi Code login from `/keys` and use it immediately without restarting `harnessd`.
- Constraints: imports are local-file-only on the daemon host; no request body or credential values cross HTTP; vendor files remain read-only.

## Scope

- In scope: real local startup checks, a scoped provider import endpoint, a subscription-row TUI action, regression coverage, and operator documentation.
- Out of scope: vendor OAuth changes, remote credential transfer, vendor-file writes, and changes to `harnesscli auth` behavior.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: `CLAUDE.md`
- Spec docs to update before code: this plan and its linked impact map
- Implementation notes to add after code: engineering and system logs

## Test Plan (TDD)

- New failing tests to add first: startup store hints; endpoint import-to-catalog transition and missing-vendor error; subscription-only TUI keybinding and refresh.
- Existing tests to update: provider API and `/keys` overlay tests.
- Regression tests required: no token-bearing HTTP request shape; server configured transition; TUI subscription gating.

## Cross-Surface Impact Map

- `docs/plans/2026-07-20-tui-subscription-login-854-impact-map.md`

## Implementation Checklist

- [ ] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Add the one-page provider/model impact map before implementation.
- [ ] Slice 1: fix(tui) real local credential-store startup hints.
- [ ] Slice 2: feat(server) import and reload subscription credentials.
- [ ] Slice 3: feat(tui) import keybinding and provider refresh.
- [ ] Slice 4: test coverage and endpoint request-shape regression guard.
- [ ] Slice 5: docs operator workflow and same-host limitation.
- [ ] Run scoped formatting, vet, and regression suite.

## Risks and Mitigations

- Risk: a user invokes the action against a remote daemon that cannot see their vendor login. Mitigation: return the import function's actionable local-host error and document the same-host boundary.
- Risk: a stale provider registry keeps the daemon unavailable. Mitigation: re-register the exact bootstrap token source after every successful import and refetch providers in the TUI.
